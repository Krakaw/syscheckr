package report

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keith/syscheckr/internal/check"
)

func sampleResults() []check.Result {
	return []check.Result{
		{Check: "disk", Status: check.StatusCrit, Summary: "disk full", Details: map[string]any{"used_percent": 99.0}},
		{Check: "cpu", Status: check.StatusWarn, Summary: "cpu hot"},
	}
}

func TestWebhookReporterPostsPayload(t *testing.T) {
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Syscheckr-Signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	rep, err := newWebhookReporter("hook", map[string]any{"url": srv.URL, "secret": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Report(context.Background(), sampleResults()); err != nil {
		t.Fatal(err)
	}

	var p payload
	if err := json.Unmarshal(gotBody, &p); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if p.Summary.Total != 2 || p.Summary.Worst != "crit" {
		t.Errorf("bad summary: %+v", p.Summary)
	}
	if p.Summary.Counts["crit"] != 1 || p.Summary.Counts["warn"] != 1 {
		t.Errorf("bad counts: %+v", p.Summary.Counts)
	}
	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Errorf("missing/invalid signature: %q", gotSig)
	}
}

func TestWebhookReporterRedactsDetails(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	results := []check.Result{{
		Check: "logs", Status: check.StatusCrit, Summary: "errors",
		Details: map[string]any{"matches": 3, "samples": []string{"secret token=abc"}, "path": "/var/log/app.log"},
	}}

	rep, _ := newWebhookReporter("hook", map[string]any{"url": srv.URL, "redact": true})
	if err := rep.Report(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotBody), "secret token") {
		t.Fatalf("redact:true must strip samples, body still contains it: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), "/var/log/app.log") {
		t.Errorf("redaction should keep non-verbose details like path")
	}

	// Original result must not be mutated by redaction.
	if _, ok := results[0].Details["samples"]; !ok {
		t.Error("redaction mutated the caller's results")
	}
}

func TestWebhookReporterReportsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	rep, _ := newWebhookReporter("hook", map[string]any{"url": srv.URL})
	if err := rep.Report(context.Background(), sampleResults()); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestSlackReporterBuildsAttachments(t *testing.T) {
	var msg slackMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&msg)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	rep, err := newSlackReporter("slack", map[string]any{"webhook_url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Report(context.Background(), sampleResults()); err != nil {
		t.Fatal(err)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(msg.Attachments))
	}
	if msg.Attachments[0].Color != "danger" {
		t.Errorf("crit should be danger, got %q", msg.Attachments[0].Color)
	}
}

func TestLinearReporterCreatesAndDedupes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"issueCreate":{"success":true,"issue":{"id":"i1","identifier":"ENG-1"}}}}`)
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "state.json")
	rep, err := newLinearReporter("linear", map[string]any{
		"api_key":       "lin_xxx",
		"team_id":       "team-1",
		"api_url":       srv.URL,
		"dedupe_window": "24h",
		"state_path":    statePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pin time so the dedupe window is deterministic.
	lr := rep.(*linearReporter)
	fixed := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	lr.now = func() time.Time { return fixed }

	res := []check.Result{{Check: "disk", Status: check.StatusCrit, Summary: "full"}}
	if err := rep.Report(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first report should create 1 issue, got %d", calls)
	}
	// Second report within the window must be suppressed.
	if err := rep.Report(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("dedupe failed: issue created again, calls=%d", calls)
	}

	// Advancing past the window allows a new issue.
	lr.now = func() time.Time { return fixed.Add(25 * time.Hour) }
	if err := rep.Report(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("after window should create again, calls=%d", calls)
	}
}

func TestLinearReporterSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"bad team"}]}`)
	}))
	defer srv.Close()
	rep, _ := newLinearReporter("linear", map[string]any{
		"api_key": "k", "team_id": "t", "api_url": srv.URL,
		"state_path": filepath.Join(t.TempDir(), "s.json"),
	})
	err := rep.Report(context.Background(), []check.Result{{Check: "x", Status: check.StatusCrit}})
	if err == nil || !strings.Contains(err.Error(), "bad team") {
		t.Fatalf("expected linear error surfaced, got %v", err)
	}
}
