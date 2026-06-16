package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeDocker spins up an httptest server speaking the Docker API subset and
// points DOCKER_HOST at it for the duration of the test.
func fakeDocker(t *testing.T, containersJSON string, pingStatus int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(pingStatus)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(containersJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", srv.URL)
}

func TestDockerRunningOK(t *testing.T) {
	fakeDocker(t, "[]", 200)
	c, _ := newDockerRunningCheck("docker", nil)
	res := c.Run(context.Background())
	if res.Status != StatusOK {
		t.Fatalf("want ok, got %v (%s)", res.Status, res.Summary)
	}
}

func TestDockerRunningDown(t *testing.T) {
	fakeDocker(t, "[]", 500)
	c, _ := newDockerRunningCheck("docker", nil)
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit when daemon errors, got %v", res.Status)
	}
}

const containersBody = `[
	{"Id":"a","Names":["/my-api"],"Image":"api:1","State":"running","Status":"Up 3 hours (healthy)"},
	{"Id":"b","Names":["/worker"],"Image":"w:1","State":"exited","Status":"Exited (1) ago"},
	{"Id":"c","Names":["/sick"],"Image":"s:1","State":"running","Status":"Up 1 min (unhealthy)"}
]`

func TestDockerContainerRunning(t *testing.T) {
	fakeDocker(t, containersBody, 200)
	c, err := newDockerContainerCheck("api", map[string]any{"name": "my-api"})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Run(context.Background())
	if res.Status != StatusOK {
		t.Fatalf("want ok for running container, got %v (%s)", res.Status, res.Summary)
	}
}

func TestDockerContainerMissing(t *testing.T) {
	fakeDocker(t, containersBody, 200)
	c, _ := newDockerContainerCheck("api", map[string]any{"name": "ghost"})
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit for missing container, got %v", res.Status)
	}
}

func TestDockerContainerWrongState(t *testing.T) {
	fakeDocker(t, containersBody, 200)
	c, _ := newDockerContainerCheck("api", map[string]any{"name": "worker", "state": "running"})
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit for exited container, got %v (%s)", res.Status, res.Summary)
	}
}

func TestDockerContainerUnhealthy(t *testing.T) {
	fakeDocker(t, containersBody, 200)
	c, _ := newDockerContainerCheck("api", map[string]any{"name": "sick", "healthy": true})
	res := c.Run(context.Background())
	if res.Status != StatusCrit {
		t.Fatalf("want crit for unhealthy container, got %v (%s)", res.Status, res.Summary)
	}
	// Without the healthy requirement, running is enough -> ok.
	c2, _ := newDockerContainerCheck("api", map[string]any{"name": "sick"})
	if res2 := c2.Run(context.Background()); res2.Status != StatusOK {
		t.Fatalf("want ok when healthy not required, got %v", res2.Status)
	}
}

func TestDockerContainerRequiresName(t *testing.T) {
	if _, err := newDockerContainerCheck("api", map[string]any{}); err == nil {
		t.Error("expected error when name missing")
	}
}

// Docker checks build their client lazily and reuse it across runs (instead of
// leaking a transport per run), but a bad DOCKER_HOST must surface only when the
// check runs — construction always succeeds so daemon startup isn't aborted.
func TestDockerBadHostFailsAtRunNotConstruction(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ftp://nope")

	c, err := newDockerRunningCheck("docker", nil)
	if err != nil {
		t.Fatalf("construction should not fail on bad DOCKER_HOST: %v", err)
	}
	if res := c.Run(context.Background()); res.Status != StatusUnknown {
		t.Errorf("want unknown when client init fails, got %v (%s)", res.Status, res.Summary)
	}

	cc, err := newDockerContainerCheck("api", map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("construction should not fail on bad DOCKER_HOST: %v", err)
	}
	if res := cc.Run(context.Background()); res.Status != StatusUnknown {
		t.Errorf("want unknown when client init fails, got %v (%s)", res.Status, res.Summary)
	}
}
