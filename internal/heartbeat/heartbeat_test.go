package heartbeat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Krakaw/syscheckr/internal/check"
	"github.com/Krakaw/syscheckr/internal/report"
)

func serve(t *testing.T, reg *Registry) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reg.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/ping", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPingValidation(t *testing.T) {
	reg := New("")
	srv := serve(t, reg)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"valid", `{"key":"web1","timeout":"5m"}`, 204},
		{"extra fields ignored", `{"key":"web2","timeout":"5m","results":[{"check":"cpu"}],"future":1}`, 204},
		{"not json", `nope`, 400},
		{"missing key", `{"timeout":"5m"}`, 400},
		{"key charset", `{"key":"bad key/../x","timeout":"5m"}`, 400},
		{"key too long", `{"key":"` + strings.Repeat("a", 129) + `","timeout":"5m"}`, 400},
		{"timeout unparseable", `{"key":"web1","timeout":"soon"}`, 400},
		{"timeout zero", `{"key":"web1","timeout":"0s"}`, 400},
		{"timeout negative", `{"key":"web1","timeout":"-5m"}`, 400},
		{"timeout above cap", `{"key":"web1","timeout":"25h"}`, 400},
		{"body too large", `{"key":"web1","timeout":"5m","pad":"` + strings.Repeat("x", maxBody) + `"}`, 413},
	}
	for _, tc := range cases {
		if got := post(t, srv.URL, "", tc.body); got != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, got)
		}
	}

	// Only the two valid pings should have registered a key.
	if n := len(reg.Results()); n != 2 {
		t.Errorf("want 2 registered keys, got %d", n)
	}
}

func TestPingRejectsNonPost(t *testing.T) {
	srv := serve(t, New(""))
	resp, err := http.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("want 405 for GET, got %d", resp.StatusCode)
	}
}

func TestPingToken(t *testing.T) {
	reg := New("s3cret")
	srv := serve(t, reg)

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"correct", "s3cret", 204},
		{"wrong", "guess", 401},
		{"missing", "", 401},
		{"prefix of the real token", "s3cre", 401},
	} {
		if got := post(t, srv.URL, tc.token, `{"key":"web1","timeout":"5m"}`); got != tc.want {
			t.Errorf("%s token: want %d, got %d", tc.name, tc.want, got)
		}
	}
}

func TestResultsGoCritAfterTimeout(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reg := New("")
	reg.clock = func() time.Time { return now }
	srv := serve(t, reg)

	if got := post(t, srv.URL, "", `{"key":"web1","timeout":"5m"}`); got != 204 {
		t.Fatalf("ping failed: %d", got)
	}

	for _, tc := range []struct {
		elapsed time.Duration
		want    check.Status
	}{
		{0, check.StatusOK},
		{5 * time.Minute, check.StatusOK}, // exactly at the deadline is not yet late
		{5*time.Minute + time.Second, check.StatusCrit},
		{time.Hour, check.StatusCrit},
	} {
		reg.clock = func() time.Time { return now.Add(tc.elapsed) }
		results := reg.Results()
		if len(results) != 1 {
			t.Fatalf("want 1 result, got %d", len(results))
		}
		if results[0].Status != tc.want {
			t.Errorf("after %s: want %s, got %s (%q)", tc.elapsed, tc.want, results[0].Status, results[0].Summary)
		}
		if results[0].Check != "heartbeat:web1" {
			t.Errorf("want per-key result name, got %q", results[0].Check)
		}
	}

	// A fresh ping clears the alert.
	reg.clock = func() time.Time { return now.Add(time.Hour) }
	if got := post(t, srv.URL, "", `{"key":"web1","timeout":"5m"}`); got != 204 {
		t.Fatalf("re-ping failed: %d", got)
	}
	if s := reg.Results()[0].Status; s != check.StatusOK {
		t.Errorf("re-ping should recover to ok, got %s", s)
	}
}

func TestResultsEmptyBeforeAnyPing(t *testing.T) {
	if got := New("").Results(); len(got) != 0 {
		t.Errorf("want no results before any ping, got %v", got)
	}
}

// TestRoundTrip wires the real client reporter to the real server handler. It
// is the only test that fails if the two halves' wire formats drift apart.
func TestRoundTrip(t *testing.T) {
	reg := New("s3cret")
	srv := serve(t, reg)

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
	if len(got) != 1 || got[0].Check != "heartbeat:web1" {
		t.Fatalf("server did not register the ping: %+v", got)
	}
	if got[0].Status != check.StatusOK {
		t.Errorf("fresh ping should be ok, got %s", got[0].Status)
	}
	if got[0].Details["timeout"] != "5m0s" {
		t.Errorf("client timeout did not survive the round trip: %v", got[0].Details["timeout"])
	}
}
