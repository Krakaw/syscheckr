// Package check defines the Check interface, result/status types, and a
// registry of built-in check types. New check types register themselves via
// Register (typically in an init function) so they can be constructed from
// declarative config.
package check

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is the severity outcome of a single check run, ordered from healthy
// to most severe so they can be compared numerically.
type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusCrit
	StatusUnknown
)

// String returns the lowercase canonical name of the status.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusCrit:
		return "crit"
	case StatusUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// Severity reports how serious a status is for comparison and routing. OK is
// the lowest; Unknown is treated as more severe than Crit because a check that
// failed to run is an operational problem.
func (s Status) Severity() int {
	switch s {
	case StatusOK:
		return 0
	case StatusWarn:
		return 1
	case StatusCrit:
		return 2
	case StatusUnknown:
		return 3
	default:
		return 3
	}
}

// IsFailing reports whether the status is anything other than OK.
func (s Status) IsFailing() bool { return s != StatusOK }

// ParseStatus converts a textual status (as used in config, e.g. min_severity)
// into a Status value.
func ParseStatus(s string) (Status, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok":
		return StatusOK, nil
	case "warn", "warning":
		return StatusWarn, nil
	case "crit", "critical":
		return StatusCrit, nil
	case "unknown":
		return StatusUnknown, nil
	default:
		return StatusUnknown, fmt.Errorf("unknown status %q", s)
	}
}

// Result is the outcome of running a single check.
type Result struct {
	Check     string         `json:"check"`
	Status    Status         `json:"status"`
	Summary   string         `json:"summary"`
	Details   map[string]any `json:"details,omitempty"`
	Err       error          `json:"-"`
	Error     string         `json:"error,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Duration  time.Duration  `json:"duration_ms"`
}

// Check is the interface implemented by every check type. Run must respect the
// context deadline and must not panic; it should return an Unknown result with
// Err set on internal failure rather than returning an error.
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// Factory constructs a Check of a particular type from its name and the raw
// config map taken from the YAML `config:` block.
type Factory func(name string, cfg map[string]any) (Check, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a check factory under typeName. It panics on duplicate
// registration, which can only happen from a programming error at init time.
func Register(typeName string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[typeName]; dup {
		panic(fmt.Sprintf("check: type %q already registered", typeName))
	}
	registry[typeName] = f
}

// New constructs a check of the given type. It returns an error if the type is
// not registered or the factory rejects the config.
func New(typeName, name string, cfg map[string]any) (Check, error) {
	registryMu.RLock()
	f, ok := registry[typeName]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown check type %q", typeName)
	}
	return f(name, cfg)
}

// Types returns the sorted list of registered check type names.
func Types() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Base is a small helper embedded by concrete checks to supply Name and to
// build results with the check name, tags, and timing pre-filled.
type Base struct {
	CheckName string
	CheckTags []string
}

// Name returns the configured check name.
func (b Base) Name() string { return b.CheckName }

// result builds a Result with name/tags/timestamp populated. Callers set
// status, summary, details, and error.
func (b Base) result(status Status, summary string) Result {
	return Result{
		Check:   b.CheckName,
		Status:  status,
		Summary: summary,
		Tags:    b.CheckTags,
	}
}

// OK builds an OK result.
func (b Base) OK(summary string, details map[string]any) Result {
	r := b.result(StatusOK, summary)
	r.Details = details
	return r
}

// Warn builds a Warn result.
func (b Base) Warn(summary string, details map[string]any) Result {
	r := b.result(StatusWarn, summary)
	r.Details = details
	return r
}

// Crit builds a Crit result.
func (b Base) Crit(summary string, details map[string]any) Result {
	r := b.result(StatusCrit, summary)
	r.Details = details
	return r
}

// Unknown builds an Unknown result carrying the underlying error.
func (b Base) Unknown(summary string, err error) Result {
	r := b.result(StatusUnknown, summary)
	r.Err = err
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

// FromThreshold returns the status implied by a measured value against warn and
// crit thresholds, where higher values are worse (e.g. usage percent). A
// threshold <= 0 disables that level.
func FromThreshold(value, warn, crit float64) Status {
	switch {
	case crit > 0 && value >= crit:
		return StatusCrit
	case warn > 0 && value >= warn:
		return StatusWarn
	default:
		return StatusOK
	}
}
