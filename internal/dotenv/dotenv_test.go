package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	in := `
# a comment
export FOO=bar
EMPTY=
QUOTED="hello world"
SINGLE='no $expansion here'
ESCAPED="line1\nline2"
URL=https://example.com/path#frag
SPACED =  trimmed
`
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"FOO":     "bar",
		"EMPTY":   "",
		"QUOTED":  "hello world",
		"SINGLE":  "no $expansion here",
		"ESCAPED": "line1\nline2",
		"URL":     "https://example.com/path#frag", // '#' in unquoted value preserved
		"SPACED":  "trimmed",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse(strings.NewReader("NOEQUALS")); err == nil {
		t.Error("expected error for line without '='")
	}
	if _, err := Parse(strings.NewReader("1BAD=x")); err == nil {
		t.Error("expected error for key starting with digit")
	}
	if _, err := Parse(strings.NewReader("has space=x")); err == nil {
		t.Error("expected error for key with space")
	}
}

func TestLoadDoesNotOverrideByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("PRESET=fromfile\nNEWVAR=fromfile\n"), 0o600)

	t.Setenv("PRESET", "fromenv")
	n, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PRESET") != "fromenv" {
		t.Errorf("existing env var should win, got %q", os.Getenv("PRESET"))
	}
	if os.Getenv("NEWVAR") != "fromfile" {
		t.Errorf("new var should be set from file, got %q", os.Getenv("NEWVAR"))
	}
	if n != 1 {
		t.Errorf("should have set exactly 1 var, set %d", n)
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("PRESET=fromfile\n"), 0o600)

	t.Setenv("PRESET", "fromenv")
	if _, err := Load(path, true); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PRESET") != "fromfile" {
		t.Errorf("override=true should replace, got %q", os.Getenv("PRESET"))
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.env"), false); err == nil {
		t.Error("expected error for missing file")
	}
}
