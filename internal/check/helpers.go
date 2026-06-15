package check

import (
	"fmt"
	"math"
)

// withDetails attaches a details map to a result and returns it, enabling a
// fluent style: c.result(...).withDetails(...).
func (r Result) withDetails(d map[string]any) Result {
	r.Details = d
	return r
}

// round2 rounds a float to two decimal places for tidy detail output.
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// humanBytes formats a byte count using binary units (KiB, MiB, ...).
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
