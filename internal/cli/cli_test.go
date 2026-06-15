package cli

import (
	"testing"

	"github.com/keith/syscheckr/internal/check"
)

func TestExitCode(t *testing.T) {
	cases := map[check.Status]int{
		check.StatusOK:      0,
		check.StatusWarn:    0, // warnings do not fail a cron job
		check.StatusCrit:    2,
		check.StatusUnknown: 2,
	}
	for status, want := range cases {
		if got := exitCode(status); got != want {
			t.Errorf("exitCode(%v) = %d, want %d", status, got, want)
		}
	}
}

func TestRootHasSubcommands(t *testing.T) {
	root := Root()
	want := map[string]bool{
		"run": false, "daemon": false, "validate": false,
		"list-checks": false, "list-reporters": false, "version": false,
	}
	for _, c := range root.Commands() {
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}
