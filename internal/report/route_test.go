package report

import (
	"testing"

	"github.com/keith/syscheckr/internal/check"
)

func results() []check.Result {
	return []check.Result{
		{Check: "disk", Status: check.StatusOK, Tags: []string{"system"}},
		{Check: "cpu", Status: check.StatusWarn, Tags: []string{"system"}},
		{Check: "docker", Status: check.StatusCrit, Tags: []string{"docker"}},
		{Check: "logs", Status: check.StatusUnknown},
	}
}

func names(rs []check.Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Check
	}
	return out
}

func TestRouteMinSeverity(t *testing.T) {
	got := Route{MinSeverity: check.StatusWarn}.Filter(results())
	// warn, crit, unknown pass; ok dropped.
	if len(got) != 3 {
		t.Fatalf("want 3, got %d (%v)", len(got), names(got))
	}
}

func TestRouteOnlyFailing(t *testing.T) {
	got := Route{OnlyFailing: true}.Filter(results())
	for _, r := range got {
		if r.Status == check.StatusOK {
			t.Fatalf("OnlyFailing leaked an OK result: %v", names(got))
		}
	}
	if len(got) != 3 {
		t.Fatalf("want 3 failing, got %d", len(got))
	}
}

func TestRouteCheckFilter(t *testing.T) {
	got := Route{Checks: []string{"docker"}}.Filter(results())
	if len(got) != 1 || got[0].Check != "docker" {
		t.Fatalf("want only docker, got %v", names(got))
	}
}

func TestRouteTagFilter(t *testing.T) {
	got := Route{Tags: []string{"docker"}}.Filter(results())
	if len(got) != 1 || got[0].Check != "docker" {
		t.Fatalf("want only docker-tagged, got %v", names(got))
	}
}

func TestRouteCombined(t *testing.T) {
	// crit+ on the system tag => nothing (system results are ok/warn).
	got := Route{MinSeverity: check.StatusCrit, Tags: []string{"system"}}.Filter(results())
	if len(got) != 0 {
		t.Fatalf("want 0, got %v", names(got))
	}
}

func TestRouteEmptyMatchesAll(t *testing.T) {
	if got := (Route{}).Filter(results()); len(got) != 4 {
		t.Fatalf("empty route should match all, got %d", len(got))
	}
}
