// This file is package heartbeat_test rather than heartbeat: report imports
// heartbeat (for MaxTimeout), so an in-package test could not import report.
package heartbeat_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/heartbeat"
	"github.com/Krakaw/syscheckr/internal/report"
)

// TestRoundTrip wires the real client reporter to the real server handler. It
// is the only test that fails if the two halves' wire formats drift apart.
func TestRoundTrip(t *testing.T) {
	reg := heartbeat.New("s3cret")
	mux := http.NewServeMux()
	reg.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rep, err := report.New("heartbeat", "hb", map[string]any{
		"url":     srv.URL + "/ping",
		"key":     "web1",
		"timeout": "5m",
		"token":   "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := []check.Result{{Check: "cpu", Status: check.StatusWarn, Summary: "cpu hot"}}
	if err := rep.Report(context.Background(), results); err != nil {
		t.Fatal(err)
	}

	got := reg.Results()
	if len(got) != 1 || got[0].Check != heartbeat.ResultPrefix+"web1" {
		t.Fatalf("server did not register the ping: %+v", got)
	}
	if got[0].Status != check.StatusOK {
		t.Errorf("fresh ping should be ok, got %s", got[0].Status)
	}
	if got[0].Details["timeout"] != "5m0s" {
		t.Errorf("client timeout did not survive the round trip: %v", got[0].Details["timeout"])
	}
}

// The client's rules and the server's must agree, or a misconfigured client
// would start fine and then fail every run with a 400 the operator only sees
// in the logs.
func TestClientRejectsWhatTheServerWould(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		timeout string
		wantErr bool
	}{
		{"ok", "web1", "5m", false},
		{"every allowed key character", "web-1.a_b:c", "5m", false},
		{"timeout at the cap", "web1", "24h", false},
		{"timeout above the cap", "web1", "25h", true},
		{"timeout zero", "web1", "0s", true},
		{"timeout negative", "web1", "-1m", true},
		{"key with a space", "web 1", "5m", true},
		{"key with a slash", "web/1", "5m", true},
		{"key too long", strings.Repeat("a", 129), "5m", true},
	} {
		_, err := report.New("heartbeat", "hb", map[string]any{
			"url": "http://mon.test/ping", "key": tc.key, "timeout": tc.timeout,
		})
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: wantErr=%v, got %v", tc.name, tc.wantErr, err)
		}
	}
}
