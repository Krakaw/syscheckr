package check

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Krakaw/syscheckr/internal/confutil"
)

// logCheck counts lines in a file matching a regex and compares the count
// against warn/crit thresholds. When a window is set, only lines whose leading
// timestamp falls within the window are counted; otherwise the whole file
// (bounded by max_lines from the tail) is scanned.
type logCheck struct {
	Base
	path      string
	pattern   *regexp.Regexp
	window    time.Duration
	timeLayout string
	warnCount int
	critCount int
	maxLines  int
}

func init() {
	Register("log", newLogCheck)
}

// newLogCheck config keys:
//
//	path:        log file path (required)
//	pattern:     regexp matched per line (required)
//	window:      only count matches newer than now-window (optional, e.g. "5m")
//	time_layout: Go time layout for the leading timestamp (default RFC3339)
//	warn_count:  warn at/above this many matches (default 1)
//	crit_count:  crit at/above this many matches (optional)
//	max_lines:   cap lines read from the tail (default 10000)
func newLogCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	pat := m.Required("pattern")
	c := &logCheck{
		Base:       Base{CheckName: name},
		path:       m.Required("path"),
		window:     m.Duration("window", 0),
		timeLayout: m.StringDefault("time_layout", time.RFC3339),
		warnCount:  m.Int("warn_count", 1),
		critCount:  m.Int("crit_count", 0),
		maxLines:   m.Int("max_lines", 10000),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid pattern: %w", name, err)
	}
	c.pattern = re
	return c, nil
}

func (c *logCheck) Run(ctx context.Context) Result {
	f, err := os.Open(c.path)
	if err != nil {
		return c.Unknown(fmt.Sprintf("cannot open %s", c.path), err)
	}
	defer f.Close()

	cutoff := time.Time{}
	if c.window > 0 {
		cutoff = time.Now().Add(-c.window)
	}

	var matches, scanned int
	var samples []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return c.Unknown("log scan canceled", ctx.Err())
		}
		scanned++
		if c.maxLines > 0 && scanned > c.maxLines {
			break
		}
		line := sc.Text()
		if !c.pattern.MatchString(line) {
			continue
		}
		if !cutoff.IsZero() && !c.withinWindow(line, cutoff) {
			continue
		}
		matches++
		if len(samples) < 5 {
			samples = append(samples, line)
		}
	}
	if err := sc.Err(); err != nil {
		return c.Unknown("error scanning log", err)
	}

	details := map[string]any{
		"path":    c.path,
		"matches": matches,
		"scanned": scanned,
	}
	if len(samples) > 0 {
		details["samples"] = samples
	}
	status := countStatus(matches, c.warnCount, c.critCount)
	summary := fmt.Sprintf("%d match(es) in %s", matches, c.path)
	if c.window > 0 {
		summary += fmt.Sprintf(" within %s", c.window)
	}
	return c.result(status, summary).withDetails(details)
}

// withinWindow parses a leading timestamp from the line; if no timestamp can be
// parsed the line is counted (we cannot prove it is old).
//
// The timestamp candidate is the first whitespace-delimited token for layouts
// that contain no spaces (e.g. RFC3339, where the token width varies between
// "...Z" and "...-07:00"). For space-containing layouts (e.g. "2006-01-02
// 15:04:05") the timestamp is fixed-width, so we slice by the layout length.
func (c *logCheck) withinWindow(line string, cutoff time.Time) bool {
	candidate := line
	if strings.ContainsRune(c.timeLayout, ' ') {
		if len(line) < len(c.timeLayout) {
			return true
		}
		candidate = line[:len(c.timeLayout)]
	} else if i := strings.IndexFunc(line, unicode.IsSpace); i > 0 {
		candidate = line[:i]
	}
	ts, err := time.Parse(c.timeLayout, candidate)
	if err != nil {
		return true
	}
	return ts.After(cutoff)
}

// countStatus maps a match count to a status using warn/crit thresholds. A
// threshold of 0 disables that level.
func countStatus(count, warn, crit int) Status {
	switch {
	case crit > 0 && count >= crit:
		return StatusCrit
	case warn > 0 && count >= warn:
		return StatusWarn
	default:
		return StatusOK
	}
}
