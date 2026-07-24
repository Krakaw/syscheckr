package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/report"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func initCmd() *cobra.Command {
	var output string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively generate a starter config",
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "-" && !force {
				switch _, err := os.Stat(output); {
				case err == nil:
					return fmt.Errorf("%s already exists; use --force to overwrite or -o to choose another path", output)
				case !errors.Is(err, os.ErrNotExist):
					return fmt.Errorf("cannot access %s: %w", output, err)
				}
			}
			// Prompts go to stderr so `init -o -` leaves stdout as clean YAML for piping.
			cfg, err := runWizard(bufio.NewScanner(os.Stdin), os.Stderr)
			if err != nil {
				return err
			}
			out, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			// Fail before writing if the answers produced an invalid config (e.g. a
			// bad min_severity), rather than writing a file that only `validate` rejects.
			if _, err := config.Parse(out); err != nil {
				return fmt.Errorf("generated config is invalid: %w", err)
			}
			if output == "-" {
				_, err = os.Stdout.Write(out)
				return err
			}
			if err := os.WriteFile(output, out, 0o644); err != nil {
				return err
			}
			fmt.Printf("wrote %s — edit it, then run `syscheckr validate`\n", output)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "syscheckr.yaml", "output path (use - for stdout)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the output file if it exists")
	return cmd
}

// runWizard drives the interactive prompts, reading from in and writing prompts
// to w, and returns the assembled config.
func runWizard(in *bufio.Scanner, w io.Writer) (*config.Config, error) {
	cfg := &config.Config{}

	checkTypes, err := selectTypes(in, w, "checks", check.Types())
	if err != nil {
		return nil, err
	}
	if len(checkTypes) == 0 {
		return nil, fmt.Errorf("at least one check is required")
	}
	names := map[string]bool{}
	for _, t := range checkTypes {
		fmt.Fprintf(w, "\n--- check: %s ---\n", t)
		name, err := uniqueName(in, w, t, names)
		if err != nil {
			return nil, err
		}
		conf, err := buildConfigBlock(in, w, check.Specs[t])
		if err != nil {
			return nil, err
		}
		cfg.Checks = append(cfg.Checks, config.CheckConfig{Name: name, Type: t, Config: conf})
	}

	repTypes, err := selectTypes(in, w, "reporters", report.Types())
	if err != nil {
		return nil, err
	}
	repNames := map[string]bool{}
	for _, t := range repTypes {
		fmt.Fprintf(w, "\n--- reporter: %s ---\n", t)
		name, err := uniqueName(in, w, t, repNames)
		if err != nil {
			return nil, err
		}
		sev, err := ask(in, w, "min_severity (ok/warn/crit, blank = all)", "")
		if err != nil {
			return nil, err
		}
		conf, err := buildConfigBlock(in, w, report.Specs[t])
		if err != nil {
			return nil, err
		}
		cfg.Reporters = append(cfg.Reporters, config.ReporterConfig{Name: name, Type: t, MinSeverity: sev, Config: conf})
	}
	return cfg, nil
}

// selectTypes prints a numbered menu and reads a comma-separated selection,
// "all", or blank.
func selectTypes(in *bufio.Scanner, w io.Writer, kind string, types []string) ([]string, error) {
	fmt.Fprintf(w, "\nAvailable %s:\n", kind)
	for i, t := range types {
		fmt.Fprintf(w, "  %d) %s\n", i+1, t)
	}
	ans, err := ask(in, w, fmt.Sprintf("Select %s (comma-separated numbers, 'all', or blank for none)", kind), "")
	if err != nil {
		return nil, err
	}
	return parseSelection(ans, types)
}

// parseSelection resolves a selection string against the numbered type list,
// dropping duplicates while preserving order.
func parseSelection(ans string, types []string) ([]string, error) {
	ans = strings.TrimSpace(ans)
	if ans == "" {
		return nil, nil
	}
	if strings.EqualFold(ans, "all") {
		return append([]string(nil), types...), nil
	}
	var out []string
	seen := map[int]bool{}
	for _, part := range strings.Split(ans, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > len(types) {
			return nil, fmt.Errorf("invalid selection %q", part)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, types[n-1])
		}
	}
	return out, nil
}

// uniqueName prompts for a name (defaulting to the first free type-based name)
// and records it so no two entries collide.
func uniqueName(in *bufio.Scanner, w io.Writer, typ string, used map[string]bool) (string, error) {
	def := typ
	for i := 2; used[def]; i++ {
		def = fmt.Sprintf("%s-%d", typ, i)
	}
	for {
		name, err := ask(in, w, "name", def)
		if err != nil {
			return "", err
		}
		if used[name] {
			fmt.Fprintf(w, "  name %q already used, pick another\n", name)
			continue
		}
		used[name] = true
		return name, nil
	}
}

// buildConfigBlock prompts for each required field and emits meaningful defaults
// for the rest. A blank required answer becomes a ${KEY} placeholder to fill in.
func buildConfigBlock(in *bufio.Scanner, w io.Writer, fields []check.Field) (map[string]any, error) {
	conf := map[string]any{}
	for _, f := range fields {
		if f.Required {
			label := f.Prompt
			if label == "" {
				label = f.Key
			}
			val, err := ask(in, w, "  "+label, "")
			if err != nil {
				return nil, err
			}
			if val == "" {
				val = "${" + strings.ToUpper(f.Key) + "}"
			}
			conf[f.Key] = val
			continue
		}
		if meaningful(f.Default) {
			conf[f.Key] = f.Default
		}
	}
	if len(conf) == 0 {
		return nil, nil
	}
	return conf, nil
}

// describeTypes prints each type followed by its config fields, marking required
// ones and showing meaningful defaults. Shared by list-checks/list-reporters
// --describe (check.Specs and report.Specs are both map[string][]check.Field).
func describeTypes(w io.Writer, types []string, specs map[string][]check.Field) {
	for _, t := range types {
		fmt.Fprintln(w, t)
		fields := specs[t]
		if len(fields) == 0 {
			fmt.Fprintln(w, "    (no config)")
			fmt.Fprintln(w)
			continue
		}
		width := 0
		for _, f := range fields {
			if len(f.Key) > width {
				width = len(f.Key)
			}
		}
		for _, f := range fields {
			var meta string
			switch {
			case f.Required:
				meta = "(required)"
			case meaningful(f.Default):
				meta = fmt.Sprintf("= %v", f.Default)
			default:
				meta = "(optional)"
			}
			desc := ""
			if f.Prompt != "" {
				desc = "  " + f.Prompt
			}
			fmt.Fprintf(w, "    %-*s  %-12s%s\n", width, f.Key, meta, desc)
		}
		fmt.Fprintln(w)
	}
}

// meaningful reports whether a default is worth emitting: skip empty strings,
// false bools, and zero numbers (their "unset" values) to keep the file terse.
func meaningful(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case int:
		return x != 0
	case float64:
		return x != 0
	default:
		return true
	}
}

// ask prints "label [def]: " and returns the trimmed answer, or def when blank.
func ask(in *bufio.Scanner, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	if !in.Scan() {
		return "", fmt.Errorf("no input (stdin closed); run `syscheckr init` in a terminal")
	}
	s := strings.TrimSpace(in.Text())
	if s == "" {
		return def, nil
	}
	return s, nil
}
