package report

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/confutil"
)

// webhookReporter POSTs a JSON payload of results to an arbitrary URL. An
// optional HMAC-SHA256 signature over the body is sent in X-Syscheckr-Signature
// so receivers can verify authenticity.
type webhookReporter struct {
	name    string
	url     string
	headers map[string]string
	secret  string
	redact  bool
	client  *http.Client
}

func init() {
	Register("webhook", newWebhookReporter)
}

// payload is the JSON body POSTed to the webhook URL.
type payload struct {
	Summary summary        `json:"summary"`
	Results []check.Result `json:"results"`
}

type summary struct {
	Total  int            `json:"total"`
	Worst  string         `json:"worst_status"`
	Counts map[string]int `json:"counts"`
}

// newWebhookReporter config keys:
//
//	url:     destination URL (required)
//	headers: extra request headers (optional)
//	secret:  if set, HMAC-SHA256 sign the body (optional)
//	redact:  strip log samples / command output from details (default false)
//	timeout: request timeout (default 15s)
func newWebhookReporter(name string, cfg map[string]any) (Reporter, error) {
	m := confutil.New(name, cfg)
	r := &webhookReporter{
		name:    name,
		url:     m.Required("url"),
		headers: m.StringMap("headers"),
		secret:  m.StringDefault("secret", ""),
		redact:  m.Bool("redact", false),
		client:  &http.Client{Timeout: m.Duration("timeout", 15*time.Second)},
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *webhookReporter) Name() string { return r.name }

func (r *webhookReporter) Report(ctx context.Context, results []check.Result) error {
	if r.redact {
		results = redactedResults(results)
	}
	body, err := json.Marshal(buildPayload(results))
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	if r.secret != "" {
		req.Header.Set("X-Syscheckr-Signature", "sha256="+sign(r.secret, body))
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	// Fully drain so the connection to this fixed webhook host is reused.
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// buildPayload assembles the JSON payload and summary counts from results.
func buildPayload(results []check.Result) payload {
	counts := map[string]int{}
	worst := check.StatusOK
	for _, res := range results {
		counts[res.Status.String()]++
		if res.Status.Severity() > worst.Severity() {
			worst = res.Status
		}
	}
	return payload{
		Summary: summary{Total: len(results), Worst: worst.String(), Counts: counts},
		Results: results,
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
