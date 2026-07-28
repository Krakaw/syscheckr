package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAppliesDefaults(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want 10s", cfg.Defaults.Timeout)
	}
	if cfg.Checks[0].Timeout != 10*time.Second {
		t.Errorf("check inherited timeout = %v, want 10s", cfg.Checks[0].Timeout)
	}
}

func TestParseEnvExpansion(t *testing.T) {
	t.Setenv("MY_HOOK", "https://hooks.example/abc")
	raw := `
checks:
  - name: disk
    type: disk
reporters:
  - name: hook
    type: webhook
    config:
      url: ${MY_HOOK}
      fallback: ${MISSING:-defaulted}
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	rc := cfg.Reporters[0]
	if rc.Config["url"] != "https://hooks.example/abc" {
		t.Errorf("env not expanded: %v", rc.Config["url"])
	}
	if rc.Config["fallback"] != "defaulted" {
		t.Errorf("default not applied: %v", rc.Config["fallback"])
	}
}

func TestValidateDuplicateNames(t *testing.T) {
	raw := `
checks:
  - name: dup
    type: disk
  - name: dup
    type: cpu
`
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "duplicate check name") {
		t.Fatalf("expected duplicate name error, got %v", err)
	}
}

func TestValidateUnknownCheckReference(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
reporters:
  - name: slack
    type: slack
    checks: [does-not-exist]
`
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "unknown check") {
		t.Fatalf("expected unknown check error, got %v", err)
	}
}

func TestValidateRejectsBadSeverity(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
reporters:
  - name: slack
    type: slack
    min_severity: nope
`
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "invalid min_severity") {
		t.Fatalf("expected severity error, got %v", err)
	}
}

func TestValidateRequiresChecks(t *testing.T) {
	_, err := Parse([]byte("reporters: []\n"))
	if err == nil || !strings.Contains(err.Error(), "at least one check") {
		t.Fatalf("expected at-least-one-check error, got %v", err)
	}
}

func TestParseAlertSuppressionFields(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
reporters:
  - name: console
    type: log
    repeat_alerts: true
  - name: slack
    type: slack
    dedupe_window: 4h
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// Alert-on-change must survive between one-shot runs, so state gets a real
	// file rather than the in-memory store.
	if cfg.State.Path != "syscheckr-state.json" {
		t.Errorf("default state path = %q, want syscheckr-state.json", cfg.State.Path)
	}
	if cfg.Reporters[0].RepeatAlerts == nil || !*cfg.Reporters[0].RepeatAlerts {
		t.Errorf("repeat_alerts = %v, want true", cfg.Reporters[0].RepeatAlerts)
	}
	// Unset must stay nil so the log/linear type defaults can still apply.
	if cfg.Reporters[1].RepeatAlerts != nil {
		t.Errorf("unset repeat_alerts should be nil, got %v", *cfg.Reporters[1].RepeatAlerts)
	}
	if cfg.Reporters[1].DedupeWindow == nil || *cfg.Reporters[1].DedupeWindow != 4*time.Hour {
		t.Errorf("dedupe_window = %v, want 4h", cfg.Reporters[1].DedupeWindow)
	}
	if cfg.Reporters[0].DedupeWindow != nil {
		t.Errorf("unset dedupe_window should be nil, got %v", *cfg.Reporters[0].DedupeWindow)
	}
}

func TestValidateRejectsNegativeDedupeWindow(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
reporters:
  - name: slack
    type: slack
    dedupe_window: -1h
`
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "dedupe_window must not be negative") {
		t.Fatalf("expected negative dedupe_window error, got %v", err)
	}
}

func TestParseHonoursExplicitStatePath(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
state:
  path: /var/lib/syscheckr/state.json
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.Path != "/var/lib/syscheckr/state.json" {
		t.Errorf("state path = %q, want the configured path", cfg.State.Path)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	raw := `
checks:
  - name: disk
    type: disk
    bogus_field: 1
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected error for unknown field")
	}
}
