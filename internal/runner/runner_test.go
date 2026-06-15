package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/keith/syscheckr/internal/check"
	"github.com/keith/syscheckr/internal/config"
	"github.com/keith/syscheckr/internal/report"
)

func TestWorstStatus(t *testing.T) {
	rs := []check.Result{
		{Status: check.StatusOK},
		{Status: check.StatusWarn},
		{Status: check.StatusCrit},
		{Status: check.StatusOK},
	}
	if got := WorstStatus(rs); got != check.StatusCrit {
		t.Fatalf("want crit, got %v", got)
	}
	if got := WorstStatus(nil); got != check.StatusOK {
		t.Fatalf("empty should be ok, got %v", got)
	}
}

// fakeCheck is a registered check type used to exercise the runner without
// touching real system resources.
type fakeCheck struct {
	check.Base
	status check.Status
}

func (f *fakeCheck) Run(_ context.Context) check.Result {
	return f.OK("ok", nil)
}

// panicCheck panics in Run to exercise the runner's panic recovery.
type panicCheck struct{ check.Base }

func (p *panicCheck) Run(_ context.Context) check.Result { panic("boom") }

// captureReporter records the results it receives, for routing assertions.
type captureReporter struct {
	mu      sync.Mutex
	name    string
	got     []check.Result
	failErr error
}

func (c *captureReporter) Name() string { return c.name }
func (c *captureReporter) Report(_ context.Context, rs []check.Result) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, rs...)
	return c.failErr
}

// shared capture instances keyed so tests can retrieve them after New().
var (
	captureMu   sync.Mutex
	captures    = map[string]*captureReporter{}
)

func init() {
	check.Register("fake_ok", func(name string, _ map[string]any) (check.Check, error) {
		return &fakeCheck{Base: check.Base{CheckName: name}, status: check.StatusOK}, nil
	})
	check.Register("fake_panic", func(name string, _ map[string]any) (check.Check, error) {
		return &panicCheck{Base: check.Base{CheckName: name}}, nil
	})
	report.Register("capture", func(name string, cfg map[string]any) (report.Reporter, error) {
		r := &captureReporter{name: name}
		if cfg["fail"] == true {
			r.failErr = errors.New("reporter failed")
		}
		captureMu.Lock()
		captures[name] = r
		captureMu.Unlock()
		return r, nil
	})
}

func getCapture(name string) *captureReporter {
	captureMu.Lock()
	defer captureMu.Unlock()
	return captures[name]
}

func TestRunOneRecoversPanic(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "p", Type: "fake_panic", Timeout: time.Second}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	results := r.RunAll(context.Background())
	if len(results) != 1 || results[0].Status != check.StatusUnknown {
		t.Fatalf("panic should yield unknown result, got %+v", results)
	}
}

func TestReportRoutesBySeverity(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "all", Type: "capture"},
			{Name: "critonly", Type: "capture", MinSeverity: "crit"},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	results := []check.Result{
		{Check: "a", Status: check.StatusOK},
		{Check: "b", Status: check.StatusCrit},
	}
	if err := r.Report(context.Background(), results); err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := len(getCapture("all").got); got != 2 {
		t.Errorf("'all' reporter should see 2 results, saw %d", got)
	}
	crit := getCapture("critonly").got
	if len(crit) != 1 || crit[0].Check != "b" {
		t.Errorf("'critonly' should see only crit, saw %+v", crit)
	}
}

func TestReportAggregatesErrors(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "boom", Type: "capture", Config: map[string]any{"fail": true}},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Report(context.Background(), []check.Result{{Check: "x", Status: check.StatusCrit}})
	if err == nil {
		t.Fatal("expected reporter error to surface")
	}
}

func TestRunAllSortsResults(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks: []config.CheckConfig{
			{Name: "zeta", Type: "fake_ok", Timeout: time.Second},
			{Name: "alpha", Type: "fake_ok", Timeout: time.Second},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	results := r.RunAll(context.Background())
	if len(results) != 2 || results[0].Check != "alpha" || results[1].Check != "zeta" {
		t.Fatalf("results not sorted: %+v", results)
	}
	for _, res := range results {
		if res.Duration < 0 || res.Timestamp.IsZero() {
			t.Errorf("runner did not stamp timing on %s", res.Check)
		}
	}
}

func TestRunSelected(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks: []config.CheckConfig{
			{Name: "a", Type: "fake_ok", Timeout: time.Second},
			{Name: "b", Type: "fake_ok", Timeout: time.Second},
		},
	}
	r, _ := New(cfg)
	results := r.RunSelected(context.Background(), []string{"b"})
	if len(results) != 1 || results[0].Check != "b" {
		t.Fatalf("RunSelected returned %+v", results)
	}
}
