package cli

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/config"
	"github.com/Krakaw/syscheckr/internal/report"
	"github.com/Krakaw/syscheckr/internal/runner"
)

func typeIndex(types []string, name string) string {
	for i, t := range types {
		if t == name {
			return strconv.Itoa(i + 1)
		}
	}
	return "0"
}

// TestWizardProducesValidConfig scripts a disk+http / log-reporter session and
// asserts the generated YAML round-trips through the real parser and runner.
func TestWizardProducesValidConfig(t *testing.T) {
	lines := []string{
		typeIndex(check.Types(), "disk") + "," + typeIndex(check.Types(), "http"),
		"",                    // disk name (default)
		"",                    // http name (default)
		"http://example.test", // http url
		typeIndex(report.Types(), "log"),
		"",     // log reporter name (default)
		"warn", // min_severity
	}
	in := bufio.NewScanner(strings.NewReader(strings.Join(lines, "\n") + "\n"))

	cfg, err := runWizard(in, io.Discard)
	if err != nil {
		t.Fatalf("wizard: %v", err)
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := config.Parse(out)
	if err != nil {
		t.Fatalf("generated config failed to parse: %v\n%s", err, out)
	}
	if _, err := runner.New(parsed); err != nil {
		t.Fatalf("generated config failed runner.New: %v\n%s", err, out)
	}
	if len(parsed.Checks) != 2 || len(parsed.Reporters) != 1 {
		t.Fatalf("got %d checks, %d reporters; want 2, 1", len(parsed.Checks), len(parsed.Reporters))
	}
}

func TestParseSelection(t *testing.T) {
	types := []string{"a", "b", "c"}
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"all", []string{"a", "b", "c"}, false},
		{"1,3", []string{"a", "c"}, false},
		{"2,2", []string{"b"}, false}, // dedup
		{"4", nil, true},              // out of range
		{"x", nil, true},              // not a number
	}
	for _, c := range cases {
		got, err := parseSelection(c.in, types)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}
