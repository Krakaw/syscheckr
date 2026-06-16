// Package runner builds checks and reporters from config, executes checks
// concurrently with per-check timeouts, and fans results out to reporters
// according to their routing rules.
package runner

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/report"
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
}

// New builds a Runner from a validated config, constructing every check and
// reporter. It returns an error aggregating any construction failure.
func New(cfg *config.Config) (*Runner, error) {
	r := &Runner{clock: time.Now}

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
func (r *Runner) Report(ctx context.Context, results []check.Result) error {
	var errs []error
	for _, br := range r.reporters {
		filtered := br.route.Filter(results)
		if len(filtered) == 0 {
			continue
		}
		if err := br.reporter.Report(ctx, filtered); err != nil {
			errs = append(errs, fmt.Errorf("reporter %q: %w", br.reporter.Name(), err))
		}
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return nil
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
