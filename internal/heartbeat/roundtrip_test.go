// This file is package heartbeat_test rather than heartbeat: report imports
// heartbeat (for MaxTimeout), so an in-package test could not import report.
package heartbeat_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// The client's bound and the server's must agree, or a misconfigured client
// would fail every run with a 400 the operator only sees in the logs.
func TestClientRejectsTimeoutAboveServerCap(t *testing.T) {
	for _, tc := range []struct {
		timeout string
		wantErr bool
	}{
		{"5m", false},
		{"24h", false},
		{"25h", true},
		{"0s", true},
		{"-1m", true},
	} {
		_, err := report.New("heartbeat", "hb", map[string]any{
			"url": "http://mon.test/ping", "key": "web1", "timeout": tc.timeout,
		})
		if (err != nil) != tc.wantErr {
			t.Errorf("timeout %q: wantErr=%v, got %v", tc.timeout, tc.wantErr, err)
		}
	}
}
