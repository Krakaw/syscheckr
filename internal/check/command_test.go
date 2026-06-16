package check

import (
	"context"
	"strings"
	"testing"
	"time"
)

func runCommand(t *testing.T, cfg map[string]any) Result {
	t.Helper()
	c, err := newCommandCheck("cmd", cfg)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	return c.Run(context.Background())
}

func TestCommandOK(t *testing.T) {
	res := runCommand(t, map[string]any{"command": "true"})
	if res.Status != StatusOK {
		t.Fatalf("want ok, got %v (%s)", res.Status, res.Summary)
	}
	if res.Details["exit_code"] != 0 {
		t.Errorf("exit_code = %v", res.Details["exit_code"])
	}
}

// TestLimitedBufferCaps verifies the command output buffer discards bytes past
// its cap so an unbounded-output command cannot exhaust memory.
func TestLimitedBufferCaps(t *testing.T) {
	b := &limitedBuffer{max: 10}
	n, err := b.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	// This write straddles the cap: only 5 more bytes are kept, but the writer
	// must still report the full length and no error so the child isn't killed.
	n, err = b.Write([]byte("world!!!!!extra"))
	if n != 15 || err != nil {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if got := b.String(); got != "helloworld" {
		t.Fatalf("buffer = %q, want %q", got, "helloworld")
	}
}

func TestCommandNonZeroExit(t *testing.T) {
	res := runCommand(t, map[string]any{"command": "false"})
	if res.Status != StatusCrit {
		t.Fatalf("want crit for exit 1, got %v", res.Status)
	}
}

func TestCommandExpectExit(t *testing.T) {
	// `false` exits 1; declare 1 as the OK code.
	res := runCommand(t, map[string]any{"command": "false", "expect_exit": 1})
	if res.Status != StatusOK {
		t.Fatalf("want ok when expect_exit matches, got %v", res.Status)
	}
}

func TestCommandWithArgs(t *testing.T) {
	res := runCommand(t, map[string]any{
		"command": "echo",
		"args":    []any{"hello", "world"},
	})
	if res.Status != StatusOK {
		t.Fatalf("want ok, got %v", res.Status)
	}
	if res.Details["output"] != "hello world" {
		t.Errorf("output = %q", res.Details["output"])
	}
}

func TestCommandShellAndPatterns(t *testing.T) {
	// match_pattern present in output -> ok.
	res := runCommand(t, map[string]any{
		"shell": true, "command": "echo all-good", "match_pattern": "good",
	})
	if res.Status != StatusOK {
		t.Fatalf("match_pattern present should be ok, got %v", res.Status)
	}

	// match_pattern absent -> crit.
	res = runCommand(t, map[string]any{
		"shell": true, "command": "echo nope", "match_pattern": "MISSING",
	})
	if res.Status != StatusCrit {
		t.Fatalf("match_pattern absent should be crit, got %v", res.Status)
	}

	// crit_pattern found -> crit.
	res = runCommand(t, map[string]any{
		"shell": true, "command": "echo EXPIRED soon", "crit_pattern": "EXPIRED",
	})
	if res.Status != StatusCrit {
		t.Fatalf("crit_pattern should be crit, got %v", res.Status)
	}

	// warn_pattern found -> warn.
	res = runCommand(t, map[string]any{
		"shell": true, "command": "echo expiring soon", "warn_pattern": "expiring",
	})
	if res.Status != StatusWarn {
		t.Fatalf("warn_pattern should be warn, got %v", res.Status)
	}
}

func TestCommandPwd(t *testing.T) {
	dir := t.TempDir()
	res := runCommand(t, map[string]any{"command": "pwd", "pwd": dir})
	if res.Status != StatusOK {
		t.Fatalf("want ok, got %v (%s)", res.Status, res.Summary)
	}
	// macOS resolves TempDir under /private; pwd reports the real path, so match
	// on the suffix to stay portable across symlinked temp roots.
	out, _ := res.Details["output"].(string)
	if !strings.HasSuffix(out, dir) && !strings.HasSuffix(dir, out) {
		t.Errorf("output = %q, want command to run in %q", out, dir)
	}
}

func TestCommandTimeout(t *testing.T) {
	c, err := newCommandCheck("cmd", map[string]any{"shell": true, "command": "sleep 5"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := c.Run(ctx)
	if res.Status != StatusUnknown {
		t.Fatalf("want unknown on timeout, got %v (%s)", res.Status, res.Summary)
	}
}

func TestCommandInvalidPattern(t *testing.T) {
	_, err := newCommandCheck("cmd", map[string]any{"command": "true", "warn_pattern": "("})
	if err == nil {
		t.Error("expected error for invalid regexp")
	}
}

func TestCommandRequiresCommand(t *testing.T) {
	if _, err := newCommandCheck("cmd", map[string]any{}); err == nil {
		t.Error("expected error when command missing")
	}
}
