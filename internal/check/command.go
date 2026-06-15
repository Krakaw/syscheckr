package check

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/keith/syscheckr/internal/confutil"
)

// commandCheck runs an arbitrary command and maps the result to a status. This
// is the flexible escape hatch for checks that have no dedicated type.
//
// Status logic:
//   - non-zero exit  -> crit (unless expect_exit overrides the OK code)
//   - match_pattern set and not found in output -> crit
//   - crit_pattern set and found in output       -> crit
//   - warn_pattern set and found in output       -> warn
//   - otherwise OK
type commandCheck struct {
	Base
	command      string
	args         []string
	shell        bool
	expectExit   int
	matchPattern *regexp.Regexp
	warnPattern  *regexp.Regexp
	critPattern  *regexp.Regexp
}

func init() {
	Register("command", newCommandCheck)
}

// newCommandCheck config keys:
//
//	command:       program to run (required); with shell:true, the full command line
//	args:          argument list (ignored when shell:true)
//	shell:         run via `sh -c` so pipes/globs work (default false)
//	expect_exit:   exit code considered OK (default 0)
//	match_pattern: regexp that MUST appear in output, else crit (optional)
//	warn_pattern:  regexp that, if present, yields warn (optional)
//	crit_pattern:  regexp that, if present, yields crit (optional)
func newCommandCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &commandCheck{
		Base:       Base{CheckName: name},
		command:    m.Required("command"),
		shell:      m.Bool("shell", false),
		expectExit: m.Int("expect_exit", 0),
	}
	if raw, ok := cfg["args"].([]any); ok {
		for _, a := range raw {
			c.args = append(c.args, fmt.Sprint(a))
		}
	}
	var err error
	if c.matchPattern, err = compileOpt(m.StringDefault("match_pattern", "")); err != nil {
		return nil, fmt.Errorf("%s: match_pattern: %w", name, err)
	}
	if c.warnPattern, err = compileOpt(m.StringDefault("warn_pattern", "")); err != nil {
		return nil, fmt.Errorf("%s: warn_pattern: %w", name, err)
	}
	if c.critPattern, err = compileOpt(m.StringDefault("crit_pattern", "")); err != nil {
		return nil, fmt.Errorf("%s: crit_pattern: %w", name, err)
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func compileOpt(pat string) (*regexp.Regexp, error) {
	if pat == "" {
		return nil, nil
	}
	return regexp.Compile(pat)
}

func (c *commandCheck) Run(ctx context.Context) Result {
	var cmd *exec.Cmd
	if c.shell {
		cmd = exec.CommandContext(ctx, "sh", "-c", c.command)
	} else {
		cmd = exec.CommandContext(ctx, c.command, c.args...)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	output := strings.TrimRight(out.String(), "\n")
	exitCode := cmd.ProcessState.ExitCode()

	details := map[string]any{
		"command":   c.command,
		"exit_code": exitCode,
		"output":    truncate(output, 2000),
	}

	if ctx.Err() != nil {
		return c.Unknown("command timed out", ctx.Err()).withDetails(details)
	}
	if exitCode != c.expectExit {
		summary := fmt.Sprintf("exit %d (want %d)", exitCode, c.expectExit)
		if runErr != nil && exitCode < 0 {
			summary = fmt.Sprintf("failed to run: %v", runErr)
		}
		return c.Crit(summary, details)
	}
	if c.matchPattern != nil && !c.matchPattern.MatchString(output) {
		return c.Crit("required pattern not found in output", details)
	}
	if c.critPattern != nil && c.critPattern.MatchString(output) {
		return c.Crit("crit pattern matched in output", details)
	}
	if c.warnPattern != nil && c.warnPattern.MatchString(output) {
		return c.Warn("warn pattern matched in output", details)
	}
	return c.OK(fmt.Sprintf("command exited %d", exitCode), details)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
