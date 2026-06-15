package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func runHTTP(t *testing.T, cfg map[string]any) Result {
	t.Helper()
	c, err := newHTTPCheck("http", cfg)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	return c.Run(context.Background())
}

func TestHTTPCheckOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "abc" {
			t.Errorf("header not sent")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	res := runHTTP(t, map[string]any{
		"url":     srv.URL,
		"headers": map[string]any{"X-Token": "abc"},
	})
	if res.Status != StatusOK {
		t.Fatalf("want ok, got %v (%s)", res.Status, res.Summary)
	}
	if res.Details["status_code"] != 200 {
		t.Errorf("status_code = %v", res.Details["status_code"])
	}
}

func TestHTTPCheckWrongStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	res := runHTTP(t, map[string]any{"url": srv.URL, "expect_status": 200})
	if res.Status != StatusCrit {
		t.Fatalf("want crit for 500, got %v", res.Status)
	}
}

func TestHTTPCheckLatencyThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	res := runHTTP(t, map[string]any{
		"url":     srv.URL,
		"warn_ms": 10.0,
		"crit_ms": 10000.0,
	})
	if res.Status != StatusWarn {
		t.Fatalf("want warn for slow response, got %v (%s)", res.Status, res.Summary)
	}
}

func TestHTTPCheckConnRefused(t *testing.T) {
	// Connect to a closed port.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	res := runHTTP(t, map[string]any{"url": url})
	if res.Status != StatusCrit {
		t.Fatalf("want crit on connection failure, got %v", res.Status)
	}
}

func TestHTTPCheckRequiresURL(t *testing.T) {
	if _, err := newHTTPCheck("http", map[string]any{}); err == nil {
		t.Error("expected error when url missing")
	}
}
