package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Krakaw/syscheckr/internal/check"
)

func TestLogReporterWritesToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.log")
	rep, err := newLogReporter("console", map[string]any{"format": "json", "output": out})
	if err != nil {
		t.Fatal(err)
	}
	results := []check.Result{
		{Check: "disk", Status: check.StatusOK, Summary: "fine"},
		{Check: "cpu", Status: check.StatusWarn, Summary: "hot", Details: map[string]any{"busy_percent": 91.0}},
		{Check: "logs", Status: check.StatusCrit, Summary: "errors", Error: "too many"},
	}
	if err := rep.Report(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	if c, ok := rep.(*logReporter); ok {
		c.Close()
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d:\n%s", len(lines), data)
	}

	// Parse each line and assert level + fields by check.
	byCheck := map[string]map[string]any{}
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line not JSON: %q", line)
		}
		byCheck[entry["check"].(string)] = entry
	}
	if byCheck["disk"]["level"] != "INFO" {
		t.Errorf("ok should log INFO, got %v", byCheck["disk"]["level"])
	}
	if byCheck["cpu"]["level"] != "WARN" {
		t.Errorf("warn should log WARN, got %v", byCheck["cpu"]["level"])
	}
	if byCheck["logs"]["level"] != "ERROR" {
		t.Errorf("crit should log ERROR, got %v", byCheck["logs"]["level"])
	}
	if byCheck["logs"]["error"] != "too many" {
		t.Errorf("error field missing: %v", byCheck["logs"])
	}
	if byCheck["cpu"]["busy_percent"] != 91.0 {
		t.Errorf("detail not emitted: %v", byCheck["cpu"])
	}
}

func TestLogReporterRejectsBadFormat(t *testing.T) {
	if _, err := newLogReporter("x", map[string]any{"format": "xml"}); err == nil {
		t.Error("expected error for unknown format")
	}
}
