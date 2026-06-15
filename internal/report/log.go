package report

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/keith/syscheckr/internal/check"
	"github.com/keith/syscheckr/internal/confutil"
)

// logReporter writes results using log/slog. It is the baseline reporter and is
// always useful for debugging and stdout/file capture.
type logReporter struct {
	name   string
	logger *slog.Logger
	closer io.Closer // non-nil when writing to a file we opened
}

func init() {
	Register("log", newLogReporter)
}

// newLogReporter builds a log reporter. Config keys:
//
//	format: "text" (default) or "json"
//	output: "stdout" (default), "stderr", or a file path
//	level:  minimum slog level, "info" (default)/"debug"/"warn"/"error"
func newLogReporter(name string, cfg map[string]any) (Reporter, error) {
	m := confutil.New(name, cfg)
	format := strings.ToLower(m.StringDefault("format", "text"))
	output := m.StringDefault("output", "stdout")
	if err := m.Err(); err != nil {
		return nil, err
	}

	var w io.Writer
	var closer io.Closer
	switch output {
	case "stdout", "":
		w = os.Stdout
	case "stderr":
		w = os.Stderr
	default:
		f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("%s: open log output: %w", name, err)
		}
		w, closer = f, f
	}

	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("%s: unknown log format %q", name, format)
	}

	return &logReporter{name: name, logger: slog.New(handler), closer: closer}, nil
}

func (r *logReporter) Name() string { return r.name }

func (r *logReporter) Report(_ context.Context, results []check.Result) error {
	for _, res := range results {
		level := slog.LevelInfo
		switch res.Status {
		case check.StatusWarn:
			level = slog.LevelWarn
		case check.StatusCrit, check.StatusUnknown:
			level = slog.LevelError
		}
		attrs := []any{
			slog.String("check", res.Check),
			slog.String("status", res.Status.String()),
			slog.Duration("duration", res.Duration),
		}
		if len(res.Tags) > 0 {
			attrs = append(attrs, slog.String("tags", strings.Join(res.Tags, ",")))
		}
		if res.Error != "" {
			attrs = append(attrs, slog.String("error", res.Error))
		}
		for k, v := range res.Details {
			attrs = append(attrs, slog.Any(k, v))
		}
		r.logger.LogAttrs(context.Background(), level, res.Summary, toAttrs(attrs)...)
	}
	return nil
}

func toAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv))
	for _, a := range kv {
		if at, ok := a.(slog.Attr); ok {
			out = append(out, at)
		}
	}
	return out
}

// Close releases the underlying file if this reporter opened one.
func (r *logReporter) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}
