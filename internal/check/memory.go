package check

import (
	"context"
	"fmt"

	"github.com/keith/syscheckr/internal/confutil"
	"github.com/shirou/gopsutil/v4/mem"
)

// memoryCheck reports virtual memory used percent against warn/crit thresholds.
type memoryCheck struct {
	Base
	warn float64
	crit float64
}

func init() {
	Register("memory", newMemoryCheck)
}

// newMemoryCheck config keys:
//
//	warn_percent: warn at/above this used percent (default 85)
//	crit_percent: crit at/above this used percent (default 95)
func newMemoryCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &memoryCheck{
		Base: Base{CheckName: name},
		warn: m.Float("warn_percent", 85),
		crit: m.Float("crit_percent", 95),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *memoryCheck) Run(ctx context.Context) Result {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return c.Unknown("cannot read memory stats", err)
	}
	status := FromThreshold(vm.UsedPercent, c.warn, c.crit)
	details := map[string]any{
		"used_percent": round2(vm.UsedPercent),
		"used_bytes":   vm.Used,
		"total_bytes":  vm.Total,
		"available":    vm.Available,
	}
	summary := fmt.Sprintf("Memory at %.1f%% used (%s/%s)",
		vm.UsedPercent, humanBytes(vm.Used), humanBytes(vm.Total))
	return c.result(status, summary).withDetails(details)
}
