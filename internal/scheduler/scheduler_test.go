package scheduler

import (
	"testing"
	"time"

	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/runner"
)

func buildRunner(t *testing.T, checks []config.CheckConfig) (*config.Config, *runner.Runner) {
	t.Helper()
	cfg := &config.Config{Defaults: config.Defaults{Timeout: time.Second}, Checks: checks}
	for i := range cfg.Checks {
		if cfg.Checks[i].Timeout == 0 {
			cfg.Checks[i].Timeout = time.Second
		}
	}
	r, err := runner.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, r
}

func TestSchedulerGroupsBySchedule(t *testing.T) {
	cfg, r := buildRunner(t, []config.CheckConfig{
		{Name: "cpu", Type: "cpu", Schedule: "@every 1m"},
		{Name: "mem", Type: "memory", Schedule: "@every 1m"},
		{Name: "disk", Type: "disk", Schedule: "@every 5m"},
	})
	s, err := New(cfg, r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Two distinct schedules -> two cron entries.
	if got := len(s.cron.Entries()); got != 2 {
		t.Fatalf("want 2 cron entries (grouped), got %d", got)
	}
}

func TestSchedulerDefaultSchedule(t *testing.T) {
	cfg, r := buildRunner(t, []config.CheckConfig{
		{Name: "cpu", Type: "cpu"}, // no schedule -> default
	})
	s, err := New(cfg, r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Fatalf("want 1 entry for default schedule, got %d", got)
	}
}

func TestSchedulerInvalidCron(t *testing.T) {
	cfg, r := buildRunner(t, []config.CheckConfig{
		{Name: "cpu", Type: "cpu", Schedule: "not a cron expr"},
	})
	if _, err := New(cfg, r, Options{}); err == nil {
		t.Fatal("expected error for invalid cron schedule")
	}
}

// The HTTP server is network-exposed once server.listen is set, so a
// connection that never finishes its headers must not hold a slot forever.
func TestHTTPServerSetsTimeouts(t *testing.T) {
	cfg, r := buildRunner(t, []config.CheckConfig{{Name: "cpu", Type: "cpu"}})
	cfg.Server = config.ServerConfig{Listen: "127.0.0.1:0", Schedule: "@every 30s"}
	s, err := New(cfg, r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := s.startHTTP()
	defer srv.Close()

	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Errorf("server left a timeout unset: %+v", srv)
	}
}
