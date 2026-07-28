// Package runner builds checks and reporters from config, executes checks
// concurrently with per-check timeouts, and fans results out to reporters
// according to their routing rules.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/report"
	"github.com/Krakaw/syscheckr/internal/state"
)

// boundCheck pairs a constructed Check with the per-check settings the runner
// needs at execution time.
type boundCheck struct {
	check    check.Check
	timeout  time.Duration
	schedule string
	tags     []string
}

// boundReporter pairs a constructed Reporter with its routing rule.
type boundReporter struct {
	reporter report.Reporter
	route    report.Route
}

// Runner holds the constructed checks and reporters for a config.
type Runner struct {
	checks    []boundCheck
	reporters []boundReporter
	clock     func() time.Time
	// store remembers the status each reporter was last told about each check,
	// so alerts fire on change rather than every run.
	store *state.Store
}

// New builds a Runner from a validated config, constructing every check and
// reporter. It returns an error aggregating any construction failure.
func New(cfg *config.Config) (*Runner, error) {
	r := &Runner{clock: time.Now}

	store, err := state.Open(cfg.State.Path)
	if err != nil {
		return nil, err
	}
	r.store = store

	for _, cc := range cfg.Checks {
		c, err := check.New(cc.Type, cc.Name, cc.Config)
		if err != nil {
			return nil, fmt.Errorf("check %q: %w", cc.Name, err)
		}
		r.checks = append(r.checks, boundCheck{
			check:    c,
			timeout:  cc.Timeout,
			schedule: cc.Schedule,
			tags:     cc.Tags,
		})
	}

	for _, rc := range cfg.Reporters {
		rep, err := report.New(rc.Type, rc.Name, rc.Config)
		if err != nil {
			return nil, fmt.Errorf("reporter %q: %w", rc.Name, err)
		}
		route, err := buildRoute(rc)
		if err != nil {
			return nil, fmt.Errorf("reporter %q: %w", rc.Name, err)
		}
		r.reporters = append(r.reporters, boundReporter{reporter: rep, route: route})
	}

	return r, nil
}

func buildRoute(rc config.ReporterConfig) (report.Route, error) {
	route := report.Route{
		Checks:      rc.Checks,
		Tags:        rc.Tags,
		OnlyFailing: rc.OnlyFailing,
	}
	// Type-dependent defaults: a log reporter is a record of every run, not an
	// alert; linear kept a 24h re-file window before it was generalised here.
	switch rc.Type {
	case "log":
		route.RepeatAlerts = true
	case "linear":
		route.DedupeWindow = 24 * time.Hour
	}
	if rc.RepeatAlerts != nil {
		route.RepeatAlerts = *rc.RepeatAlerts
	}
	if rc.DedupeWindow != nil {
		route.DedupeWindow = *rc.DedupeWindow
	}
	if rc.MinSeverity != "" {
		s, err := check.ParseStatus(rc.MinSeverity)
		if err != nil {
			return route, err
		}
		route.MinSeverity = s
	}
	return route, nil
}

// CheckNames returns the configured check names in declaration order.
func (r *Runner) CheckNames() []string {
	out := make([]string, len(r.checks))
	for i, c := range r.checks {
		out[i] = c.check.Name()
	}
	return out
}

// RunAll executes every check concurrently and returns results sorted by check
// name. Each check runs under its own timeout derived from config.
func (r *Runner) RunAll(ctx context.Context) []check.Result {
	results := make([]check.Result, len(r.checks))
	var wg sync.WaitGroup
	for i, bc := range r.checks {
		wg.Add(1)
		go func(i int, bc boundCheck) {
			defer wg.Done()
			results[i] = r.runOne(ctx, bc)
		}(i, bc)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Check < results[j].Check })
	return results
}

// RunSelected executes only the named checks concurrently and returns results
// sorted by check name. Unknown names are ignored. Used by the daemon to run a
// group of checks that share a schedule.
func (r *Runner) RunSelected(ctx context.Context, names []string) []check.Result {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var selected []boundCheck
	for _, bc := range r.checks {
		if want[bc.check.Name()] {
			selected = append(selected, bc)
		}
	}
	results := make([]check.Result, len(selected))
	var wg sync.WaitGroup
	for i, bc := range selected {
		wg.Add(1)
		go func(i int, bc boundCheck) {
			defer wg.Done()
			results[i] = r.runOne(ctx, bc)
		}(i, bc)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Check < results[j].Check })
	return results
}

// runOne executes a single check with its timeout and recovers from panics so a
// misbehaving check cannot crash the run.
func (r *Runner) runOne(ctx context.Context, bc boundCheck) (res check.Result) {
	start := r.clock()
	cctx, cancel := context.WithTimeout(ctx, bc.timeout)
	defer cancel()

	defer func() {
		if rec := recover(); rec != nil {
			res = check.Result{
				Check:   bc.check.Name(),
				Status:  check.StatusUnknown,
				Summary: fmt.Sprintf("check panicked: %v", rec),
				Error:   fmt.Sprintf("panic: %v", rec),
			}
		}
		res.Timestamp = start
		res.Duration = r.clock().Sub(start)
		// Merge check-level tags configured in YAML with any the check set.
		res.Tags = mergeTags(res.Tags, bc.tags)
	}()

	return bc.check.Run(cctx)
}

func mergeTags(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, t := range append(append([]string{}, a...), b...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Report routes results to every reporter and returns a combined error if any
// reporter fails. Reporters run sequentially; a failure does not stop the rest.
//
// Unless the route sets RepeatAlerts, a result is only sent when its status
// changed since that reporter was last told about the check (or the route's
// DedupeWindow has elapsed), so a disk sitting at 85% alerts once instead of
// every run. This has to happen here rather than in Route.Filter or a reporter:
// a route with only_failing or min_severity never sees the OK result, so the
// recovery transition is only observable where every result meets every
// reporter.
func (r *Runner) Report(ctx context.Context, results []check.Result) error {
	now := r.clock()
	dirty := false

	// Whether a status changed is a property of the check, not of any route: a
	// reporter with only_failing or min_severity never sees the OK result, so
	// tracking "what this reporter was last told" would miss the recovery and
	// then swallow the next threshold crossing.
	changed := make(map[string]bool, len(results))
	for _, res := range results {
		last, ok := r.store.Get(statusKey(res.Check))
		changed[res.Check] = !ok || last.Status != res.Status.String()
	}

	var errs []error
	for _, br := range r.reporters {
		filtered := br.route.Filter(results)
		if !br.route.RepeatAlerts {
			filtered = r.unnotified(br.reporter.Name(), br.route, filtered, changed, now)
		}
		if len(filtered) == 0 {
			continue
		}
		// undelivered stays nil on success. On failure it names the checks to
		// leave unmarked so the next run retries them; an all-or-nothing
		// reporter (one HTTP call for the batch) fails the whole slice.
		var undelivered map[string]bool
		if err := br.reporter.Report(ctx, filtered); err != nil {
			errs = append(errs, fmt.Errorf("reporter %q: %w", br.reporter.Name(), err))
			var partial *report.PartialError
			if !errors.As(err, &partial) {
				continue
			}
			undelivered = partial.Failed
		}
		if br.route.RepeatAlerts {
			continue // never suppressed, so nothing to remember
		}
		for _, res := range filtered {
			if undelivered[res.Check] {
				continue
			}
			r.store.Set(notifyKey(br.reporter.Name(), res.Check), state.Entry{
				Time:   now,
				Status: res.Status.String(),
			})
			dirty = true
		}
	}

	for _, res := range results {
		if changed[res.Check] {
			r.store.Set(statusKey(res.Check), state.Entry{Time: now, Status: res.Status.String()})
			dirty = true
		}
	}
	if dirty {
		if err := r.store.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("persist alert state: %w", err))
		}
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return nil
}

// unnotified drops results whose status has not moved since this reporter was
// last alerted, unless the route's dedupe window has since elapsed.
func (r *Runner) unnotified(reporter string, route report.Route, results []check.Result, changed map[string]bool, now time.Time) []check.Result {
	out := make([]check.Result, 0, len(results))
	for _, res := range results {
		last, notified := r.store.Get(notifyKey(reporter, res.Check))
		send := changed[res.Check] || // the check moved, even if this route couldn't see it
			!notified || // a reporter that has never been told about this check
			last.Status != res.Status.String() || // told, but at a different status: a
			// delivery that failed still holds the older status here, and without this
			// the retry is lost as soon as the check stops changing
			(route.DedupeWindow > 0 && now.Sub(last.Time) >= route.DedupeWindow)
		if send {
			out = append(out, res)
		}
	}
	return out
}

// statusKey holds a check's last observed status; notifyKey holds when a
// reporter was last alerted about it, for the dedupe window.
func statusKey(checkName string) string { return "status:" + checkName }

func notifyKey(reporter, checkName string) string {
	return "notify:" + reporter + ":" + checkName
}

// Close releases resources held by checks and reporters that implement
// io.Closer — e.g. an open log file or a reporter's/check's idle HTTP
// connections. It is safe to call once after all runs have finished (daemon
// shutdown, or before a one-shot process exits) and aggregates any close
// errors. After Close the Runner must not be reused.
func (r *Runner) Close() error {
	var errs []error
	for _, br := range r.reporters {
		if c, ok := br.reporter.(io.Closer); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, fmt.Errorf("reporter %q: %w", br.reporter.Name(), err))
			}
		}
	}
	for _, bc := range r.checks {
		if c, ok := bc.check.(io.Closer); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, fmt.Errorf("check %q: %w", bc.check.Name(), err))
			}
		}
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return nil
}

// WorstStatus returns the most severe status across results, defaulting to OK.
func WorstStatus(results []check.Result) check.Status {
	worst := check.StatusOK
	for _, res := range results {
		if res.Status.Severity() > worst.Severity() {
			worst = res.Status
		}
	}
	return worst
}

func joinErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := "multiple reporter errors:"
	for _, e := range errs {
		msg += "\n  - " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}
