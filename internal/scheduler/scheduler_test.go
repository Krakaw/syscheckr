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
