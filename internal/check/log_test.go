package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempLog(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLogCheckCounts(t *testing.T) {
	log := "INFO started\nERROR boom\nWARN hmm\nFATAL dead\nINFO ok\n"
	path := writeTempLog(t, log)

	c, err := newLogCheck("errs", map[string]any{
		"path":       path,
		"pattern":    "ERROR|FATAL",
		"warn_count": 1,
		"crit_count": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit for 2 matches, got %v (%s)", res.Status, res.Summary)
	}
	if res.Details["matches"] != 2 {
		t.Errorf("want 2 matches, got %v", res.Details["matches"])
	}
}

func TestLogCheckBelowThreshold(t *testing.T) {
	path := writeTempLog(t, "INFO a\nINFO b\n")
	c, _ := newLogCheck("errs", map[string]any{
		"path": path, "pattern": "ERROR", "warn_count": 1,
	})
	res := c.Run(context.Background())
	if res.Status != StatusOK {
		t.Fatalf("want ok for 0 matches, got %v", res.Status)
	}
}

func TestLogCheckMissingFile(t *testing.T) {
	c, _ := newLogCheck("errs", map[string]any{
		"path": "/no/such/file.log", "pattern": "ERROR",
	})
	res := c.Run(context.Background())
	if res.Status != StatusUnknown {
		t.Fatalf("want unknown for missing file, got %v", res.Status)
	}
}

func TestLogCheckRequiresPattern(t *testing.T) {
	if _, err := newLogCheck("x", map[string]any{"path": "/tmp/x"}); err == nil {
		t.Error("expected error when pattern missing")
	}
}

func TestLogCheckWindowRFC3339(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-1 * time.Hour).Format(time.RFC3339)   // Z-suffixed, outside window
	fresh := now.Add(-1 * time.Minute).Format(time.RFC3339) // Z-suffixed, inside window
	// Mix of Z-form timestamps; only the fresh ERROR should count within a 5m window.
	content := old + " ERROR stale boom\n" + fresh + " ERROR recent boom\nERROR no-timestamp\n"
	path := writeTempLog(t, content)

	c, err := newLogCheck("errs", map[string]any{
		"path":       path,
		"pattern":    "ERROR",
		"window":     "5m",
		"warn_count": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	// fresh (inside) + no-timestamp (uncountable -> counted) = 2; stale excluded.
	if res.Details["matches"] != 2 {
		t.Fatalf("want 2 matches (fresh + untimestamped), got %v", res.Details["matches"])
	}
}

func TestLogCheckWindowSpaceLayout(t *testing.T) {
	layout := "2006-01-02 15:04:05"
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour).Format(layout)
	fresh := now.Add(-30 * time.Second).Format(layout)
	content := old + " ERROR stale\n" + fresh + " ERROR fresh\n"
	path := writeTempLog(t, content)

	c, _ := newLogCheck("errs", map[string]any{
		"path": path, "pattern": "ERROR", "window": "5m",
		"time_layout": layout, "warn_count": 1,
	})
	res := c.Run(context.Background())
	if res.Details["matches"] != 1 {
		t.Fatalf("want 1 fresh match with space layout, got %v", res.Details["matches"])
	}
}
