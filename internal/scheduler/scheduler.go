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
	"github.com/Krakaw/syscheckr/internal/heartbeat"
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
	addr   string              // resolved HTTP listen address ("" = no server)
	hb     *heartbeat.Registry // non-nil when the heartbeat server is enabled

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

	// The --healthz flag wins over server.listen: both describe the same
	// listener, and a flag is the more explicit of the two.
	s.addr = opts.HealthzAddr
	if s.addr == "" {
		s.addr = cfg.Server.Listen
	}
	if cfg.Server.Enabled() {
		s.hb = heartbeat.New(cfg.Server.Token)
		if _, err := s.cron.AddFunc(cfg.Server.Schedule, s.reportHeartbeats); err != nil {
			return nil, fmt.Errorf("invalid server schedule %q: %w", cfg.Server.Schedule, err)
		}
	}
	return s, nil
}

// reportHeartbeats evaluates client deadlines and routes the results through
// the normal reporting path, so alert-on-change applies per client key.
func (s *Scheduler) reportHeartbeats() {
	results := s.hb.Results()
	if len(results) == 0 {
		return // no client has pinged yet
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.runner.Report(ctx, results); err != nil {
		s.log.Warn("reporting heartbeats failed", "error", err)
	}
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
	if s.addr != "" {
		srv = s.startHTTP()
	}

	s.cron.Start()
	s.log.Info("syscheckr daemon started",
		"checks", len(s.cfg.Checks), "listen", s.addr, "heartbeat_server", s.hb != nil)

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

// startHTTP serves the health endpoint reflecting scheduler liveness, plus the
// heartbeat ingest endpoint when the heartbeat server is enabled. /ping is
// mounted only in that case, so an existing --healthz user does not silently
// gain a write endpoint.
func (s *Scheduler) startHTTP() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		last, count := s.lastRun, s.runCount
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","runs":%d,"last_run":%q}`+"\n", count, last.Format(time.RFC3339))
	})
	if s.hb != nil {
		s.hb.Register(mux)
	}
	srv := &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("http server failed", "error", err)
		}
	}()
	return srv
}
