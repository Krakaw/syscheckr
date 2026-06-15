// Package dockerapi is a tiny dependency-free client for the Docker Engine API.
// It speaks HTTP over the Docker unix socket (or DOCKER_HOST) so syscheckr can
// check daemon health and container state without the heavy Docker SDK.
package dockerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to a single Docker engine endpoint.
type Client struct {
	http *http.Client
	host string // for error messages
}

// Container is the subset of the /containers/json response we care about.
type Container struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`  // created, running, paused, exited, ...
	Status string   `json:"Status"` // e.g. "Up 3 hours (healthy)"
}

// HasName reports whether the container is known by the given name (Docker
// prefixes names with "/").
func (c Container) HasName(name string) bool {
	want := "/" + strings.TrimPrefix(name, "/")
	for _, n := range c.Names {
		if n == want {
			return true
		}
	}
	return false
}

// Health extracts a Docker health phrase from Status when present, e.g. the
// "healthy" in "Up 3 hours (healthy)". Only recognized health states are
// returned; parenthesized text like an exit code ("Exited (0) ago") is ignored.
func (c Container) Health() string {
	open := strings.Index(c.Status, "(")
	closeIdx := strings.Index(c.Status, ")")
	if open < 0 || closeIdx <= open {
		return ""
	}
	switch inner := c.Status[open+1 : closeIdx]; inner {
	case "healthy", "unhealthy", "health: starting", "starting":
		return inner
	default:
		return ""
	}
}

// New builds a client from DOCKER_HOST, falling back to the default unix socket.
// Supported schemes: unix:// (path) and tcp:// (host:port).
func New() (*Client, error) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	transport := &http.Transport{}
	switch {
	case strings.HasPrefix(host, "unix://"):
		sock := strings.TrimPrefix(host, "unix://")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"):
		// net/http dials the host directly; nothing special needed.
	default:
		return nil, fmt.Errorf("unsupported DOCKER_HOST scheme: %q", host)
	}
	return &Client{
		http: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		host: host,
	}, nil
}

// baseURL returns a dummy http host; for unix sockets the dialer ignores it,
// for tcp we rewrite the scheme to http.
func (c *Client) url(path string) string {
	if strings.HasPrefix(c.host, "tcp://") {
		return "http://" + strings.TrimPrefix(c.host, "tcp://") + path
	}
	if strings.HasPrefix(c.host, "http://") {
		return strings.TrimRight(c.host, "/") + path
	}
	return "http://docker" + path // unix socket: host is irrelevant
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

// Ping reports whether the Docker daemon is reachable and responsive.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.get(ctx, "/_ping")
	if err != nil {
		return fmt.Errorf("docker daemon unreachable at %s: %w", c.host, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// ListContainers returns containers. When all is false, only running ones.
func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	resp, err := c.get(ctx, "/containers/json?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("list containers: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out []Container
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}
	return out, nil
}
