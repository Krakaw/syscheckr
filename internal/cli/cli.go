// Package cli wires the cobra command tree for syscheckr.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/keith/syscheckr/internal/check"
	"github.com/keith/syscheckr/internal/config"
	"github.com/keith/syscheckr/internal/report"
	"github.com/keith/syscheckr/internal/runner"
	"github.com/keith/syscheckr/internal/scheduler"
	"github.com/keith/syscheckr/internal/version"
	"github.com/spf13/cobra"
)

// Root builds the root command and attaches all subcommands.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "syscheckr",
		Short:         "Custom system health checks with pluggable reporting",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(runCmd(), daemonCmd(), validateCmd(), listChecksCmd(), listReportersCmd(), versionCmd())
	return root
}

// loadRunner loads the config at path and constructs a Runner from it.
func loadRunner(path string) (*config.Config, *runner.Runner, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	r, err := runner.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	return cfg, r, nil
}

func runCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run all checks once, report, and exit",
		Long: "Runs every configured check a single time, routes results to reporters, " +
			"and exits non-zero if any check is failing (warn=0 by default, crit/unknown=2).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, r, err := loadRunner(configPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			results := r.RunAll(ctx)
			if err := r.Report(ctx, results); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
			os.Exit(exitCode(runner.WorstStatus(results)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "syscheckr.yaml", "path to config file")
	return cmd
}

// exitCode maps the worst status to a process exit code: OK/warn -> 0,
// crit/unknown -> 2. Warn is non-fatal so a warning does not fail a cron job.
func exitCode(worst check.Status) int {
	switch worst {
	case check.StatusCrit, check.StatusUnknown:
		return 2
	default:
		return 0
	}
}

func daemonCmd() *cobra.Command {
	var configPath, healthz string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run checks on their schedules until interrupted",
		Long: "Starts a long-running scheduler that runs each check on its cron `schedule` " +
			"(checks without a schedule default to every minute). Stops cleanly on SIGINT/SIGTERM.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, r, err := loadRunner(configPath)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			sched, err := scheduler.New(cfg, r, scheduler.Options{HealthzAddr: healthz})
			if err != nil {
				return err
			}
			return sched.Run(ctx)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "syscheckr.yaml", "path to config file")
	cmd.Flags().StringVar(&healthz, "healthz", "", "if set (e.g. :8080), serve a /healthz endpoint")
	return cmd
}

func validateCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate the config without running checks",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, _, err := loadRunner(configPath)
			if err != nil {
				return err
			}
			fmt.Printf("config OK: %d check(s), %d reporter(s)\n", len(cfg.Checks), len(cfg.Reporters))
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "syscheckr.yaml", "path to config file")
	return cmd
}

func listChecksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-checks",
		Short: "List registered check types",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(strings.Join(check.Types(), "\n"))
		},
	}
}

func listReportersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-reporters",
		Short: "List registered reporter types",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(strings.Join(report.Types(), "\n"))
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(version.String())
		},
	}
}
