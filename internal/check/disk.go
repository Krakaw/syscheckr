package check

import (
	"context"
	"fmt"

	"github.com/Krakaw/syscheckr/internal/confutil"
	"github.com/shirou/gopsutil/v4/disk"
)

// diskCheck reports filesystem usage percent for a mount path against warn/crit
// thresholds.
type diskCheck struct {
	Base
	path string
	warn float64
	crit float64
}

func init() {
	Register("disk", newDiskCheck)
}

// newDiskCheck config keys:
//
//	path:         mount point to inspect (default "/")
//	warn_percent: warn at/above this used percent (default 80)
//	crit_percent: crit at/above this used percent (default 90)
func newDiskCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &diskCheck{
		Base: Base{CheckName: name},
		path: m.StringDefault("path", "/"),
		warn: m.Float("warn_percent", 80),
		crit: m.Float("crit_percent", 90),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *diskCheck) Run(_ context.Context) Result {
	usage, err := disk.Usage(c.path)
	if err != nil {
		return c.Unknown(fmt.Sprintf("cannot stat %s", c.path), err)
	}
	details := map[string]any{
		"path":         c.path,
		"used_percent": round2(usage.UsedPercent),
		"used_bytes":   usage.Used,
		"total_bytes":  usage.Total,
		"free_bytes":   usage.Free,
	}
	status := FromThreshold(usage.UsedPercent, c.warn, c.crit)
	summary := fmt.Sprintf("%s at %.1f%% used (%s/%s)", c.path,
		usage.UsedPercent, humanBytes(usage.Used), humanBytes(usage.Total))
	return c.result(status, summary).withDetails(details)
}
