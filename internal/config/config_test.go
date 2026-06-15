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
