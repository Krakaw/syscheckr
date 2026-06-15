package check

import (
	"context"
	"testing"
)

// These are smoke tests that exercise the real gopsutil-backed checks against
// the host. They assert the check runs and returns a valid status with the
// expected detail keys, rather than pinning machine-specific values.

func TestDiskCheckSmoke(t *testing.T) {
	c, err := New("disk", "root", map[string]any{"path": "/", "warn_percent": 99.9, "crit_percent": 100})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	if res.Status == StatusUnknown {
		t.Fatalf("unexpected unknown: %v", res.Error)
	}
	for _, key := range []string{"used_percent", "total_bytes", "free_bytes"} {
		if _, ok := res.Details[key]; !ok {
			t.Errorf("missing detail %q", key)
		}
	}
}

func TestDiskCheckBadPath(t *testing.T) {
	c, _ := New("disk", "x", map[string]any{"path": "/no/such/mount/here"})
	res := c.Run(context.Background())
	if res.Status != StatusUnknown {
		t.Fatalf("want unknown for bad path, got %v", res.Status)
	}
}

func TestDiskThresholdCrit(t *testing.T) {
	// Any non-empty disk is >= 0% used; warn at 0 forces a warn, crit at 0 forces crit.
	c, _ := New("disk", "root", map[string]any{"path": "/", "crit_percent": 0.0001})
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit with near-zero crit threshold, got %v", res.Status)
	}
}

func TestCPUCheckSmoke(t *testing.T) {
	c, err := New("cpu", "cpu", map[string]any{"sample": "100ms"})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	if res.Status == StatusUnknown {
		t.Fatalf("unexpected unknown: %v", res.Error)
	}
	if _, ok := res.Details["busy_percent"]; !ok {
		t.Error("missing busy_percent detail")
	}
}

func TestMemoryCheckSmoke(t *testing.T) {
	c, err := New("memory", "mem", map[string]any{"warn_percent": 99.99, "crit_percent": 100})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	if res.Status == StatusUnknown {
		t.Fatalf("unexpected unknown: %v", res.Error)
	}
	if _, ok := res.Details["used_percent"]; !ok {
		t.Error("missing used_percent detail")
	}
}
