package check

import (
	"context"
	"fmt"
	"time"

	"github.com/Krakaw/syscheckr/internal/confutil"
	"github.com/shirou/gopsutil/v4/cpu"
)

// cpuCheck samples total CPU utilization over a short interval and compares it
// against warn/crit thresholds.
type cpuCheck struct {
	Base
	sample time.Duration
	warn   float64
	crit   float64
}

func init() {
	Register("cpu", newCPUCheck)
}

// newCPUCheck config keys:
//
//	sample:       sampling window, e.g. "1s" (default 1s)
//	warn_percent: warn at/above this busy percent (default 85)
//	crit_percent: crit at/above this busy percent (default 95)
func newCPUCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &cpuCheck{
		Base:   Base{CheckName: name},
		sample: m.Duration("sample", time.Second),
		warn:   m.Float("warn_percent", 85),
		crit:   m.Float("crit_percent", 95),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *cpuCheck) Run(ctx context.Context) Result {
	// Sampled percentage over the configured window (false = aggregate, not per-core).
	pcts, err := cpu.PercentWithContext(ctx, c.sample, false)
	if err != nil {
		return c.Unknown("cannot sample CPU", err)
	}
	if len(pcts) == 0 {
		return c.Unknown("cpu sample returned no data", fmt.Errorf("empty percent slice"))
	}
	busy := pcts[0]
	status := FromThreshold(busy, c.warn, c.crit)
	details := map[string]any{"busy_percent": round2(busy), "sample": c.sample.String()}
	return c.result(status, fmt.Sprintf("CPU at %.1f%% busy", busy)).withDetails(details)
}
