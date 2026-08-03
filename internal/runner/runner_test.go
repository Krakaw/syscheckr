package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/report"
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
	captureMu sync.Mutex
	captures  = map[string]*captureReporter{}
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

// closerCheck/closerReporter implement io.Closer to verify Runner.Close fans
// out to both checks and reporters that hold releasable resources.
type closerCheck struct {
	check.Base
	closed *bool
}

func (c *closerCheck) Run(_ context.Context) check.Result { return c.OK("ok", nil) }
func (c *closerCheck) Close() error                       { *c.closed = true; return nil }

type closerReporter struct {
	name   string
	closed *bool
}

func (r *closerReporter) Name() string                                 { return r.name }
func (r *closerReporter) Report(context.Context, []check.Result) error { return nil }
func (r *closerReporter) Close() error                                 { *r.closed = true; return nil }

var (
	closeStateMu   sync.Mutex
	checkClosed    bool
	reporterClosed bool
)

func init() {
	check.Register("closer_check", func(name string, _ map[string]any) (check.Check, error) {
		return &closerCheck{Base: check.Base{CheckName: name}, closed: &checkClosed}, nil
	})
	report.Register("closer_reporter", func(name string, _ map[string]any) (report.Reporter, error) {
		return &closerReporter{name: name, closed: &reporterClosed}, nil
	})
}

func TestCloseReleasesCheckAndReporterResources(t *testing.T) {
	closeStateMu.Lock()
	defer closeStateMu.Unlock()
	checkClosed, reporterClosed = false, false

	cfg := &config.Config{
		Defaults:  config.Defaults{Timeout: time.Second},
		Checks:    []config.CheckConfig{{Name: "c", Type: "closer_check", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{{Name: "r", Type: "closer_reporter"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !checkClosed {
		t.Error("Close did not close the check")
	}
	if !reporterClosed {
		t.Error("Close did not close the reporter")
	}
}

// reportOnce runs one Report cycle and returns how many results the named
// capture reporter received during it.
func reportOnce(t *testing.T, r *Runner, name string, results []check.Result) int {
	t.Helper()
	cap := getCapture(name)
	before := len(cap.got)
	if err := r.Report(context.Background(), results); err != nil {
		t.Fatalf("report: %v", err)
	}
	return len(cap.got) - before
}

func TestReportAlertsOnlyOnStatusChange(t *testing.T) {
	cfg := &config.Config{
		Defaults:  config.Defaults{Timeout: time.Second},
		Checks:    []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{{Name: "change", Type: "capture"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	at := func(s check.Status) []check.Result {
		return []check.Result{{Check: "disk", Status: s}}
	}
	steps := []struct {
		status check.Status
		want   int
		why    string
	}{
		{check.StatusWarn, 1, "first warn alerts"},
		{check.StatusWarn, 0, "still warn stays quiet"},
		{check.StatusCrit, 1, "escalation alerts"},
		{check.StatusCrit, 0, "still crit stays quiet"},
		{check.StatusOK, 1, "recovery alerts"},
		{check.StatusOK, 0, "still ok stays quiet"},
		{check.StatusWarn, 1, "re-crossing the threshold alerts again"},
	}
	for _, s := range steps {
		if got := reportOnce(t, r, "change", at(s.status)); got != s.want {
			t.Errorf("%s: sent %d results, want %d", s.why, got, s.want)
		}
	}
}

// A route that drops OK results still has to re-alert when the check recovers
// and fails again — disk 80% → 70% → 80% fires twice.
func TestReportReAlertsAfterUnseenRecovery(t *testing.T) {
	cfg := &config.Config{
		Defaults:  config.Defaults{Timeout: time.Second},
		Checks:    []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{{Name: "failing-only", Type: "capture", OnlyFailing: true}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := func(s check.Status) []check.Result {
		return []check.Result{{Check: "disk", Status: s}}
	}
	if got := reportOnce(t, r, "failing-only", at(check.StatusWarn)); got != 1 {
		t.Fatalf("crossing 80%% sent %d, want 1", got)
	}
	if got := reportOnce(t, r, "failing-only", at(check.StatusOK)); got != 0 {
		t.Fatalf("only_failing must not see the recovery, sent %d", got)
	}
	if got := reportOnce(t, r, "failing-only", at(check.StatusWarn)); got != 1 {
		t.Fatalf("re-crossing 80%% sent %d, want 1", got)
	}
}

func TestReportRepeatAlertsSendsEveryRun(t *testing.T) {
	repeat := true
	cfg := &config.Config{
		Defaults:  config.Defaults{Timeout: time.Second},
		Checks:    []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{{Name: "every", Type: "capture", RepeatAlerts: &repeat}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := []check.Result{{Check: "disk", Status: check.StatusWarn}}
	for i := 0; i < 3; i++ {
		if got := reportOnce(t, r, "every", res); got != 1 {
			t.Fatalf("run %d sent %d results, want 1", i, got)
		}
	}
}

func TestReportDedupeWindowResendsUnchanged(t *testing.T) {
	hour := time.Hour
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "window", Type: "capture", DedupeWindow: &hour},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	r.clock = func() time.Time { return now }

	res := []check.Result{{Check: "disk", Status: check.StatusCrit}}
	if got := reportOnce(t, r, "window", res); got != 1 {
		t.Fatalf("first report sent %d, want 1", got)
	}
	now = now.Add(30 * time.Minute)
	if got := reportOnce(t, r, "window", res); got != 0 {
		t.Fatalf("within the window sent %d, want 0", got)
	}
	now = now.Add(31 * time.Minute)
	if got := reportOnce(t, r, "window", res); got != 1 {
		t.Fatalf("past the window sent %d, want 1", got)
	}
}

// A delivery failure must not mark the alert as sent, or a transient Slack
// outage would swallow the alert until the next status change.
func TestReportRetriesAfterDeliveryFailure(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "flaky", Type: "capture", Config: map[string]any{"fail": true}},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := []check.Result{{Check: "disk", Status: check.StatusCrit}}
	if err := r.Report(context.Background(), res); err == nil {
		t.Fatal("expected the delivery error to surface")
	}
	getCapture("flaky").failErr = nil
	if got := reportOnce(t, r, "flaky", res); got != 1 {
		t.Fatalf("failed delivery should be retried, sent %d want 1", got)
	}
}

// Regression: a delivery that fails AFTER an earlier successful one must still
// retry. The check's own status stops changing once it settles, so a predicate
// that only looks at "did the status move" leaves the reporter stuck on the
// stale status it was last told and drops the alert for good.
func TestReportRetriesFailureAfterEarlierSuccess(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "drops", Type: "capture"},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := func(s check.Status) []check.Result {
		return []check.Result{{Check: "disk", Status: s}}
	}
	// Run 1: warn delivers cleanly.
	if got := reportOnce(t, r, "drops", at(check.StatusWarn)); got != 1 {
		t.Fatalf("first warn sent %d, want 1", got)
	}
	// Run 2: escalates to crit, but the reporter is down.
	getCapture("drops").failErr = errors.New("slack unreachable")
	if err := r.Report(context.Background(), at(check.StatusCrit)); err == nil {
		t.Fatal("expected the delivery error to surface")
	}
	// Run 3: still crit, reporter back up. The crit must land.
	getCapture("drops").failErr = nil
	if got := reportOnce(t, r, "drops", at(check.StatusCrit)); got != 1 {
		t.Fatalf("crit was dropped after the failed delivery, sent %d want 1", got)
	}
}

// partialReporter delivers per result (like linear filing one issue per check)
// and fails only the checks it is told to.
type partialReporter struct {
	name string
	got  []check.Result
	fail map[string]bool
}

func (p *partialReporter) Name() string { return p.name }
func (p *partialReporter) Report(_ context.Context, rs []check.Result) error {
	failed := map[string]bool{}
	for _, r := range rs {
		if p.fail[r.Check] {
			failed[r.Check] = true
			continue
		}
		p.got = append(p.got, r)
	}
	if len(failed) > 0 {
		return &report.PartialError{Failed: failed, Err: errors.New("some deliveries failed")}
	}
	return nil
}

var partials = map[string]*partialReporter{}

func init() {
	report.Register("partial", func(name string, _ map[string]any) (report.Reporter, error) {
		p := &partialReporter{name: name, fail: map[string]bool{}}
		partials[name] = p
		return p, nil
	})
}

// Regression: one check failing to deliver must not re-deliver the checks that
// succeeded alongside it. For linear that means duplicate tickets every run.
func TestReportKeepsPartialDeliveries(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Timeout: time.Second},
		Checks:   []config.CheckConfig{{Name: "ok", Type: "fake_ok", Timeout: time.Second}},
		Reporters: []config.ReporterConfig{
			{Name: "tickets", Type: "partial"},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := partials["tickets"]
	p.fail["cpu"] = true

	res := []check.Result{
		{Check: "disk", Status: check.StatusCrit},
		{Check: "cpu", Status: check.StatusCrit},
	}
	if err := r.Report(context.Background(), res); err == nil {
		t.Fatal("expected the partial failure to surface")
	}
	if len(p.got) != 1 || p.got[0].Check != "disk" {
		t.Fatalf("run 1 delivered %+v, want disk only", p.got)
	}

	// Run 2: cpu recovers. disk is unchanged and already delivered, so only cpu
	// should go out — disk must not be re-delivered.
	p.fail = map[string]bool{}
	p.got = nil
	if err := r.Report(context.Background(), res); err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(p.got) != 1 || p.got[0].Check != "cpu" {
		t.Fatalf("run 2 delivered %+v, want the retried cpu only (disk was already filed)", p.got)
	}
}

func TestBuildRouteTypeDefaults(t *testing.T) {
	// A log reporter is a record of every run, not an alert.
	route, err := buildRoute(config.ReporterConfig{Name: "l", Type: "log"})
	if err != nil {
		t.Fatal(err)
	}
	if !route.RepeatAlerts {
		t.Error("log should repeat by default")
	}
	// ...unless it asks to be quiet.
	quiet := false
	route, _ = buildRoute(config.ReporterConfig{Name: "l", Type: "log", RepeatAlerts: &quiet})
	if route.RepeatAlerts {
		t.Error("explicit repeat_alerts: false should win over the log default")
	}
	// linear keeps the 24h re-file window it had as a reporter config key.
	route, _ = buildRoute(config.ReporterConfig{Name: "li", Type: "linear"})
	if route.DedupeWindow != 24*time.Hour || route.RepeatAlerts {
		t.Errorf("linear defaults wrong: %+v", route)
	}
	// ...and an explicit 0 must be able to turn that default off.
	var off time.Duration
	route, _ = buildRoute(config.ReporterConfig{Name: "li", Type: "linear", DedupeWindow: &off})
	if route.DedupeWindow != 0 {
		t.Errorf("explicit dedupe_window: 0 should win over the linear default, got %v", route.DedupeWindow)
	}
	// A heartbeat must assert liveness every run: suppressing an unchanged
	// status would look exactly like the host having died.
	route, _ = buildRoute(config.ReporterConfig{Name: "hb", Type: "heartbeat"})
	if !route.RepeatAlerts {
		t.Error("heartbeat should repeat by default")
	}
	// Everything else alerts on change only.
	route, _ = buildRoute(config.ReporterConfig{Name: "s", Type: "slack"})
	if route.RepeatAlerts || route.DedupeWindow != 0 {
		t.Errorf("slack defaults wrong: %+v", route)
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
