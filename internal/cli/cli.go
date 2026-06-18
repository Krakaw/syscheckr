// Package cli wires the cobra command tree for syscheckr.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/dotenv"
	"github.com/Krakaw/syscheckr/internal/report"
	"github.com/Krakaw/syscheckr/internal/runner"
	"github.com/Krakaw/syscheckr/internal/scheduler"
	"github.com/Krakaw/syscheckr/internal/version"
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
	root.AddCommand(runCmd(), daemonCmd(), validateCmd(), listChecksCmd(), listReportersCmd(), updateCmd(), versionCmd())
	return root
}

// loadRunner loads env files, then the config at configPath, and constructs a
// Runner. Env files are applied before config so ${ENV} interpolation sees them.
func loadRunner(configPath, envFile string) (*config.Config, *runner.Runner, error) {
	if err := loadEnvFiles(envFile, configPath); err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	r, err := runner.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	return cfg, r, nil
}

// loadEnvFiles applies environment variables from a .env file. When envFile is
// set it is required (missing is an error). Otherwise it auto-loads a ".env"
// from the working directory and from the config file's directory, if present.
// Existing process env vars always win, so secrets injected by systemd/CI are
// not clobbered.
func loadEnvFiles(envFile, configPath string) error {
	if envFile != "" {
		if _, err := dotenv.Load(envFile, false); err != nil {
			return fmt.Errorf("load env file: %w", err)
		}
		return nil
	}
	seen := map[string]bool{}
	for _, p := range []string{".env", filepath.Join(filepath.Dir(configPath), ".env")} {
		abs, err := filepath.Abs(p)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		if _, err := os.Stat(p); err != nil {
			continue // absent: auto-load is best-effort
		}
		if _, err := dotenv.Load(p, false); err != nil {
			return fmt.Errorf("load env file %s: %w", p, err)
		}
	}
	return nil
}

func runCmd() *cobra.Command {
	var configPath, envFile string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run all checks once, report, and exit",
		Long: "Runs every configured check a single time, routes results to reporters, " +
			"and exits non-zero if any check is failing (warn=0 by default, crit/unknown=2).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, r, err := loadRunner(configPath, envFile)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			results := r.RunAll(ctx)
			if err := r.Report(ctx, results); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
			// Close before exiting so any open log file is flushed/closed;
			// os.Exit skips deferred cleanup, so this must be explicit.
			if err := r.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "warning: close:", err)
			}
			os.Exit(exitCode(runner.WorstStatus(results)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "syscheckr.yaml", "path to config file")
	cmd.Flags().StringVar(&envFile, "env-file", "", "load secrets from this .env file (default: auto-load ./.env)")
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
	var configPath, healthz, envFile string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run checks on their schedules until interrupted",
		Long: "Starts a long-running scheduler that runs each check on its cron `schedule` " +
			"(checks without a schedule default to every minute). Stops cleanly on SIGINT/SIGTERM.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, r, err := loadRunner(configPath, envFile)
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
	cmd.Flags().StringVar(&envFile, "env-file", "", "load secrets from this .env file (default: auto-load ./.env)")
	return cmd
}

func validateCmd() *cobra.Command {
	var configPath, envFile string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate the config without running checks",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, _, err := loadRunner(configPath, envFile)
			if err != nil {
				return err
			}
			fmt.Printf("config OK: %d check(s), %d reporter(s)\n", len(cfg.Checks), len(cfg.Reporters))
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "syscheckr.yaml", "path to config file")
	cmd.Flags().StringVar(&envFile, "env-file", "", "load secrets from this .env file (default: auto-load ./.env)")
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
