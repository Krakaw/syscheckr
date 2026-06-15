package check

import (
	"context"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

func TestMountCheckRequiresPath(t *testing.T) {
	if _, err := New("mount", "m", map[string]any{}); err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestMountCheckMissing verifies a path that is not a mount point reports crit
// rather than unknown — the directory may or may not exist, but it is not a
// mount, which is the failure the check exists to catch.
func TestMountCheckMissing(t *testing.T) {
	c, _ := New("mount", "m", map[string]any{"path": "/no/such/mount/here"})
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit for missing mount, got %v (%s)", res.Status, res.Summary)
	}
}

// TestMountCheckRootFound verifies the root mount is located on the host. Its
// read-only state differs across platforms (macOS mounts / read-only), so the
// test only asserts the mount was found and identified, not its status.
func TestMountCheckRootFound(t *testing.T) {
	c, _ := New("mount", "root", map[string]any{"path": "/"})
	res := c.Run(context.Background())
	if res.Status == StatusUnknown {
		t.Fatalf("unexpected unknown: %v", res.Error)
	}
	if dev, _ := res.Details["device"].(string); dev == "" {
		t.Fatalf("root mount not located, details=%v", res.Details)
	}
}

func TestMountEvaluate(t *testing.T) {
	part := disk.PartitionStat{
		Device:     "/dev/sda1",
		Mountpoint: "/data",
		Fstype:     "ext4",
		Opts:       []string{"rw", "relatime"},
	}
	roPart := part
	roPart.Opts = []string{"ro", "relatime"}

	tests := []struct {
		name     string
		device   string
		fstype   string
		readOnly bool
		part     disk.PartitionStat
		want     Status
	}{
		{"healthy rw", "", "", false, part, StatusOK},
		{"device match", "/dev/sda1", "", false, part, StatusOK},
		{"device mismatch", "/dev/sdb1", "", false, part, StatusCrit},
		{"fstype match (case-insensitive)", "", "EXT4", false, part, StatusOK},
		{"fstype mismatch", "", "xfs", false, part, StatusCrit},
		{"failed mount remounted ro", "", "", false, roPart, StatusCrit},
		{"expected ro is ro", "", "", true, roPart, StatusOK},
		{"expected ro but rw", "", "", true, part, StatusCrit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &mountCheck{
				Base:     Base{CheckName: "m"},
				path:     "/data",
				device:   tt.device,
				fstype:   tt.fstype,
				readOnly: tt.readOnly,
			}
			if got := c.evaluate(tt.part).Status; got != tt.want {
				t.Errorf("evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasOpt(t *testing.T) {
	opts := []string{"rw", " ro ", "noexec"}
	if !hasOpt(opts, "ro") {
		t.Error("expected ro to be found (trimmed)")
	}
	if hasOpt(opts, "nosuid") {
		t.Error("did not expect nosuid")
	}
}
