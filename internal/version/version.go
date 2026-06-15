// Package version exposes build metadata, settable via -ldflags at build time.
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semantic version, overridden at build time.
	Version = "dev"
	// Commit is the git commit hash, overridden at build time.
	Commit = "none"
	// Date is the build date, overridden at build time.
	Date = "unknown"
)

// String returns a one-line human-readable version banner.
func String() string {
	return fmt.Sprintf("syscheckr %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
