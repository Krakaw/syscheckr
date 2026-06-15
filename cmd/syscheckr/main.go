// Command syscheckr runs custom system health checks and reports results
// through pluggable channels. See `syscheckr --help` for subcommands.
package main

import (
	"fmt"
	"os"

	"github.com/keith/syscheckr/internal/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
