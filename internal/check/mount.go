package check

import (
	"context"
	"fmt"
	"strings"

	"github.com/Krakaw/syscheckr/internal/confutil"
	"github.com/shirou/gopsutil/v4/disk"
)

// mountCheck verifies that a path is actually mounted (not just an existing
// directory) and, optionally, that it was mounted from the expected device with
// the expected filesystem type. A mount that the kernel has remounted read-only
// — the usual signature of an I/O error on the backing store — is treated as a
// failed mount.
type mountCheck struct {
	Base
	path     string
	device   string
	fstype   string
	readOnly bool
}

func init() {
	Register("mount", newMountCheck)
}

// newMountCheck config keys:
//
//	path:      mount point that must be mounted (required)
//	device:    expected source device, e.g. /dev/sda1 (optional)
//	fstype:    expected filesystem type, e.g. ext4 (optional)
//	read_only: if true the mount is expected to be read-only; if false (default)
//	           a read-only mount is reported as a failed mount
func newMountCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &mountCheck{
		Base:     Base{CheckName: name},
		path:     m.Required("path"),
		device:   m.StringDefault("device", ""),
		fstype:   m.StringDefault("fstype", ""),
		readOnly: m.Bool("read_only", false),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *mountCheck) Run(ctx context.Context) Result {
	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return c.Unknown("cannot list mounts", err)
	}

	for i := range parts {
		if parts[i].Mountpoint == c.path {
			return c.evaluate(parts[i])
		}
	}
	return c.Crit(fmt.Sprintf("%s is not mounted", c.path),
		map[string]any{"path": c.path})
}

// evaluate scores a mount that was found at the configured path, checking the
// device, fstype, and read-only expectations.
func (c *mountCheck) evaluate(p disk.PartitionStat) Result {
	details := map[string]any{
		"path":   c.path,
		"device": p.Device,
		"fstype": p.Fstype,
		"opts":   p.Opts,
	}

	if c.device != "" && p.Device != c.device {
		return c.Crit(fmt.Sprintf("%s mounted from %s, want %s", c.path, p.Device, c.device), details)
	}
	if c.fstype != "" && !strings.EqualFold(p.Fstype, c.fstype) {
		return c.Crit(fmt.Sprintf("%s is %s, want %s", c.path, p.Fstype, c.fstype), details)
	}

	ro := hasOpt(p.Opts, "ro")
	if ro && !c.readOnly {
		return c.Crit(fmt.Sprintf("%s is mounted read-only (failed mount?)", c.path), details)
	}
	if !ro && c.readOnly {
		return c.Crit(fmt.Sprintf("%s is mounted read-write, want read-only", c.path), details)
	}

	return c.OK(fmt.Sprintf("%s mounted (%s, %s)", c.path, p.Device, p.Fstype), details)
}

// hasOpt reports whether opt appears in a mount's option list.
func hasOpt(opts []string, opt string) bool {
	for _, o := range opts {
		if strings.EqualFold(strings.TrimSpace(o), opt) {
			return true
		}
	}
	return false
}
