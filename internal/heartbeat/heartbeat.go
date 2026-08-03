// Package heartbeat implements the server half of liveness monitoring: an HTTP
// endpoint that clients ping with a key and their own expected timeout, and an
// evaluation that turns a missed deadline into a crit check.Result.
//
// The results are named "heartbeat:<key>" and handed straight to the runner's
// normal reporting path, so routing, min_severity, and alert-on-change all
// apply per client with no special-casing.
package heartbeat

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
)

const (
	// maxTimeout bounds the client-supplied deadline. A client asking to be
	// excused for a month is almost certainly a config error, and accepting it
	// would silently disable its own alert.
	maxTimeout = 24 * time.Hour
	// maxBody caps the ping payload. Clients send their full results, so this is
	// generous, but it is still attacker-controlled input.
	maxBody = 64 << 10
	// ResultPrefix names the results this package emits.
	ResultPrefix = "heartbeat:"
)

// keyPattern constrains the client-chosen key: it ends up in result names, in
// state-file keys, and in alert text sent to Slack/Linear.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// entry is the last ping seen for one key.
type entry struct {
	lastSeen time.Time
	timeout  time.Duration
}

// Registry tracks the last ping per key and evaluates deadlines.
//
// ponytail: keys are learned from the first ping and held in memory only. A
// client that never starts is invisible (nothing to go stale), and a server
// restart forgets every key until each client pings again. Add a `server.expect`
// list, or persist through state.Store, if either case starts to matter.
type Registry struct {
	mu    sync.Mutex
	clock func() time.Time
	token string
	keys  map[string]entry
}

// New returns a Registry. If token is non-empty, pings must present it as
// "Authorization: Bearer <token>".
func New(token string) *Registry {
	return &Registry{clock: time.Now, token: token, keys: map[string]entry{}}
}

// Register mounts the ping endpoint on mux. Method routing is left to the mux
// pattern, which answers anything but POST with 405 and an Allow header.
func (r *Registry) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /ping", r.servePing)
}

// ping is the request body. Clients send their full results alongside the
// liveness fields; unknown/extra fields are ignored, which is the seam for
// server-side stats processing later.
type ping struct {
	Key     string `json:"key"`
	Timeout string `json:"timeout"`
}

func (r *Registry) servePing(w http.ResponseWriter, req *http.Request) {
	if !r.authorized(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, maxBody)
	var p ping
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if !keyPattern.MatchString(p.Key) {
		http.Error(w, "key must be 1-128 chars of [A-Za-z0-9._:-]", http.StatusBadRequest)
		return
	}
	timeout, err := time.ParseDuration(p.Timeout)
	if err != nil {
		http.Error(w, "timeout must be a duration string, e.g. 5m", http.StatusBadRequest)
		return
	}
	if timeout <= 0 || timeout > maxTimeout {
		http.Error(w, fmt.Sprintf("timeout must be >0 and <=%s", maxTimeout), http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	r.keys[p.Key] = entry{lastSeen: r.clock(), timeout: timeout}
	r.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (r *Registry) authorized(req *http.Request) bool {
	if r.token == "" {
		return true
	}
	// Constant-time: a plain == leaks how much of the token was guessed.
	got, want := req.Header.Get("Authorization"), "Bearer "+r.token
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Results returns one result per tracked key: OK while the last ping is inside
// its timeout, crit once the deadline has passed.
func (r *Registry) Results() []check.Result {
	now := r.clock()

	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]check.Result, 0, len(r.keys))
	for key, e := range r.keys {
		age := now.Sub(e.lastSeen).Round(time.Second)
		res := check.Result{
			Check:     ResultPrefix + key,
			Tags:      []string{"heartbeat"},
			Timestamp: now,
			Details: map[string]any{
				"key":       key,
				"last_seen": e.lastSeen.UTC().Format(time.RFC3339),
				"timeout":   e.timeout.String(),
				"age":       age.String(),
			},
		}
		if now.Sub(e.lastSeen) > e.timeout {
			res.Status = check.StatusCrit
			res.Summary = fmt.Sprintf("no ping for %s (timeout %s)", age, e.timeout)
		} else {
			res.Status = check.StatusOK
			res.Summary = fmt.Sprintf("last ping %s ago (timeout %s)", age, e.timeout)
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Check < out[j].Check })
	return out
}
