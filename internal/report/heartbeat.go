package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/confutil"
	"github.com/Krakaw/syscheckr/internal/heartbeat"
)

// heartbeatReporter is the client half of liveness monitoring: it POSTs a
// key and a deadline to another syscheckr instance running `server:`, proving
// this host is still alive. It rides the normal reporting path, so a host that
// stops running checks — or stops running at all — stops pinging.
//
// The full results ride along so the server can grow stats processing without
// a client-side change.
type heartbeatReporter struct {
	name    string
	url     string
	key     string
	timeout time.Duration
	token   string
	redact  bool
	client  *http.Client
}

func init() {
	Register("heartbeat", newHeartbeatReporter)
}

// heartbeatPayload is the JSON body POSTed to the server's /ping endpoint.
type heartbeatPayload struct {
	Key     string `json:"key"`
	Timeout string `json:"timeout"`
	payload
}

// newHeartbeatReporter config keys:
//
//	url:          server ping endpoint, e.g. http://mon:8080/ping (required)
//	key:          this host's identity on the server (required)
//	timeout:      alert if the server sees no ping for this long (default 5m)
//	token:        server.token, sent as an Authorization bearer (optional)
//	redact:       strip log samples / command output from results (default true)
//	http_timeout: request timeout (default 15s)
func newHeartbeatReporter(name string, cfg map[string]any) (Reporter, error) {
	m := confutil.New(name, cfg)
	r := &heartbeatReporter{
		name: name,
		url:  m.Required("url"),
		key:  m.Required("key"),
		// The deadline the server enforces. It must comfortably exceed this
		// host's check schedule or the server will alert between pings.
		timeout: m.Duration("timeout", 5*time.Minute),
		token:   m.StringDefault("token", ""),
		// Defaults to true unlike webhook: a heartbeat ships every run to a
		// host that only asked whether we are alive.
		redact: m.Bool("redact", true),
		client: &http.Client{Timeout: m.Duration("http_timeout", 15*time.Second)},
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	// The server enforces the same rules, so reject them here rather than
	// failing every run with a 400 the operator only sees in the logs.
	if !heartbeat.ValidKey(r.key) {
		return nil, fmt.Errorf("%s: config %q: must be %s", name, "key", heartbeat.KeyFormat)
	}
	if r.timeout <= 0 || r.timeout > heartbeat.MaxTimeout {
		return nil, fmt.Errorf("%s: config %q: must be >0 and <=%s", name, "timeout", heartbeat.MaxTimeout)
	}
	return r, nil
}

func (r *heartbeatReporter) Name() string { return r.name }

func (r *heartbeatReporter) Report(ctx context.Context, results []check.Result) error {
	if r.redact {
		results = redactedResults(results)
	}
	body, err := json.Marshal(heartbeatPayload{
		Key:     r.key,
		Timeout: r.timeout.String(),
		payload: buildPayload(results),
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("post heartbeat: %w", err)
	}
	defer resp.Body.Close()
	// Fully drain so the connection to this fixed server is reused.
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat returned status %d", resp.StatusCode)
	}
	return nil
}
