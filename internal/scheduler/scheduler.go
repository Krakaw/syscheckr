// Package scheduler implements daemon mode: it runs checks on their cron
// schedules, reports results, and serves an optional health endpoint until the
// context is canceled (SIGINT/SIGTERM).
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/runner"
	"github.com/robfig/cron/v3"
)

// defaultSchedule is used for checks that do not specify one.
const defaultSchedule = "@every 1m"

// Options configures the scheduler.
type Options struct {
	HealthzAddr string // if non-empty, serve GET /healthz here (e.g. ":8080")
}

// Scheduler runs checks on cron schedules.
type Scheduler struct {
	cfg    *config.Config
	runner *runner.Runner
	opts   Options
	cron   *cron.Cron
	log    *slog.Logger

	mu       sync.Mutex
	lastRun  time.Time
	lastErr  error
	runCount int
}

// New builds a scheduler, grouping checks by their schedule so checks sharing a
// schedule run and report together.
func New(cfg *config.Config, r *runner.Runner, opts Options) (*Scheduler, error) {
	s := &Scheduler{
		cfg:    cfg,
		runner: r,
		opts:   opts,
		cron:   cron.New(),
		log:    slog.Default(),
	}

	groups := map[string][]string{} // schedule -> check names
	for _, ch := range cfg.Checks {
		sched := ch.Schedule
		if sched == "" {
			sched = defaultSchedule
		}
		groups[sched] = append(groups[sched], ch.Name)
	}

	for sched, names := range groups {
		names := names
		if _, err := s.cron.AddFunc(sched, func() { s.runGroup(names) }); err != nil {
			return nil, fmt.Errorf("invalid schedule %q for %v: %w", sched, names, err)
		}
	}
	return s, nil
}

// runGroup executes a group of checks and reports their results.
func (s *Scheduler) runGroup(names []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	results := s.runner.RunSelected(ctx, names)
	err := s.runner.Report(ctx, results)

	s.mu.Lock()
	s.lastRun = time.Now()
	s.lastErr = err
	s.runCount++
	s.mu.Unlock()

	if err != nil {
		s.log.Warn("reporting failed", "checks", names, "error", err)
	}
}

// Run starts the scheduler and optional health server, blocking until ctx is
// canceled, then shuts down gracefully.
func (s *Scheduler) Run(ctx context.Context) error {
	var srv *http.Server
	if s.opts.HealthzAddr != "" {
		srv = s.startHealthz()
	}

	s.cron.Start()
	s.log.Info("syscheckr daemon started",
		"checks", len(s.cfg.Checks), "healthz", s.opts.HealthzAddr)

	<-ctx.Done()
	s.log.Info("shutting down")

	// Stop scheduling and wait for in-flight jobs to finish.
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()

	if srv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}

	// Release check/reporter resources (open log files, idle HTTP connections)
	// now that no more jobs will run.
	if err := s.runner.Close(); err != nil {
		s.log.Warn("error closing runner", "error", err)
	}
	return nil
}

// startHealthz serves a simple health endpoint reflecting scheduler liveness.
func (s *Scheduler) startHealthz() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		last, count := s.lastRun, s.runCount
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","runs":%d,"last_run":%q}`+"\n", count, last.Format(time.RFC3339))
	})
	srv := &http.Server{Addr: s.opts.HealthzAddr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("healthz server failed", "error", err)
		}
	}()
	return srv
}
