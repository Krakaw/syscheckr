package check

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Krakaw/syscheckr/internal/confutil"
)

// httpCheck probes an HTTP endpoint and asserts the status code and latency.
type httpCheck struct {
	Base
	url        string
	method     string
	wantStatus int
	warnMS     float64
	critMS     float64
	headers    map[string]string
}

func init() {
	Register("http", newHTTPCheck)
}

// newHTTPCheck config keys:
//
//	url:          endpoint to probe (required)
//	method:       HTTP method (default GET)
//	expect_status: required status code (default 200)
//	warn_ms:      warn at/above this latency in ms (optional)
//	crit_ms:      crit at/above this latency in ms (optional)
//	headers:      map of request headers (optional)
func newHTTPCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &httpCheck{
		Base:       Base{CheckName: name},
		url:        m.Required("url"),
		method:     m.StringDefault("method", http.MethodGet),
		wantStatus: m.Int("expect_status", 200),
		warnMS:     m.Float("warn_ms", 0),
		critMS:     m.Float("crit_ms", 0),
		headers:    m.StringMap("headers"),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *httpCheck) Run(ctx context.Context) Result {
	req, err := http.NewRequestWithContext(ctx, c.method, c.url, nil)
	if err != nil {
		return c.Unknown("bad request", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return c.Crit(fmt.Sprintf("request to %s failed", c.url), map[string]any{"error": err.Error()})
	}
	defer resp.Body.Close()
	// Drain the body so the keep-alive connection can return to the pool and be
	// reused across runs, but cap the read so probing a large endpoint doesn't
	// download its whole body each tick. Responses larger than the cap simply
	// don't get reused — no leak, just a fresh connection next time.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	latency := time.Since(start)
	latencyMS := float64(latency.Microseconds()) / 1000.0

	details := map[string]any{
		"url":         c.url,
		"status_code": resp.StatusCode,
		"latency_ms":  round2(latencyMS),
	}
	if resp.StatusCode != c.wantStatus {
		return c.Crit(fmt.Sprintf("%s returned %d, want %d", c.url, resp.StatusCode, c.wantStatus), details)
	}
	// Status is good; escalate on latency thresholds if configured.
	status := FromThreshold(latencyMS, c.warnMS, c.critMS)
	return c.result(status, fmt.Sprintf("%s %d in %.0fms", c.url, resp.StatusCode, latencyMS)).withDetails(details)
}
