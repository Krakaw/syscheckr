package dockerapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeDockerHandler serves the small subset of the Docker API we use.
func fakeDockerHandler(t *testing.T, pingStatus int, containersJSON string) http.Handler {
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
	return mux
}

func TestPingOK(t *testing.T) {
	srv := httptest.NewServer(fakeDockerHandler(t, 200, "[]"))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", srv.URL) // http:// branch

	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestPingBadStatus(t *testing.T) {
	srv := httptest.NewServer(fakeDockerHandler(t, 500, "[]"))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", srv.URL)

	c, _ := New()
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error on 500 ping")
	}
}

func TestPingUnreachable(t *testing.T) {
	srv := httptest.NewServer(fakeDockerHandler(t, 200, "[]"))
	url := srv.URL
	srv.Close()
	t.Setenv("DOCKER_HOST", url)

	c, _ := New()
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error when daemon unreachable")
	}
}

func TestListContainers(t *testing.T) {
	body := `[
		{"Id":"abc","Names":["/my-api"],"Image":"api:1","State":"running","Status":"Up 3 hours (healthy)"},
		{"Id":"def","Names":["/db"],"Image":"pg:16","State":"exited","Status":"Exited (0) 2 hours ago"}
	]`
	srv := httptest.NewServer(fakeDockerHandler(t, 200, body))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", srv.URL)

	c, _ := New()
	got, err := c.ListContainers(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 containers, got %d", len(got))
	}
	if !got[0].HasName("my-api") || !got[0].HasName("/my-api") {
		t.Errorf("HasName failed for %v", got[0].Names)
	}
	if got[0].HasName("db") {
		t.Error("HasName matched wrong container")
	}
	if h := got[0].Health(); h != "healthy" {
		t.Errorf("Health = %q, want healthy", h)
	}
	if h := got[1].Health(); h != "" {
		t.Errorf("Health for db = %q, want empty", h)
	}
}

func TestListContainersBadStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("DOCKER_HOST", srv.URL)

	c, _ := New()
	if _, err := c.ListContainers(context.Background(), false); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestUnsupportedScheme(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ftp://nope")
	if _, err := New(); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

// TestUnixSocketDialer covers the default unix:// transport path by serving the
// fake API over a real unix socket.
func TestUnixSocketDialer(t *testing.T) {
	dir, err := os.MkdirTemp("", "sck")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	if len(sock) > 100 {
		t.Skipf("socket path too long for this platform: %s", sock)
	}

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: fakeDockerHandler(t, 200, `[{"Id":"x","Names":["/web"],"State":"running","Status":"Up"}]`)}
	go srv.Serve(ln)
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "unix://"+sock)
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping over unix socket: %v", err)
	}
	got, err := c.ListContainers(context.Background(), true)
	if err != nil {
		t.Fatalf("list over unix socket: %v", err)
	}
	if len(got) != 1 || !got[0].HasName("web") {
		t.Fatalf("unexpected containers over unix socket: %v", got)
	}
}
