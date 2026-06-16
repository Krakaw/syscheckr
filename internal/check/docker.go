package check

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Krakaw/syscheckr/internal/confutil"
	"github.com/Krakaw/syscheckr/internal/dockerapi"
)

// dockerClient lazily builds and caches a single dockerapi.Client. Construction
// is deferred to first use (not the check constructor) so a bad DOCKER_HOST
// surfaces as a failing check at run time rather than aborting startup — while
// the client is still built only once and reused across runs, avoiding the
// leaked transport-per-run that a fresh client each Run would cause.
type dockerClient struct {
	once   sync.Once
	client *dockerapi.Client
	err    error
}

func (d *dockerClient) get() (*dockerapi.Client, error) {
	d.once.Do(func() { d.client, d.err = dockerapi.New() })
	return d.client, d.err
}

// Close releases the client's idle connections if one was ever created.
func (d *dockerClient) Close() error {
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

// dockerRunningCheck verifies the Docker daemon is reachable.
type dockerRunningCheck struct {
	Base
	dockerClient
}

// dockerContainerCheck verifies a named container exists and is in the expected
// state (and optionally healthy).
type dockerContainerCheck struct {
	Base
	dockerClient
	container string
	wantState string
	healthy   bool
}

func init() {
	Register("docker_running", newDockerRunningCheck)
	Register("docker_container", newDockerContainerCheck)
}

func newDockerRunningCheck(name string, cfg map[string]any) (Check, error) {
	return &dockerRunningCheck{Base: Base{CheckName: name}}, nil
}

func (c *dockerRunningCheck) Run(ctx context.Context) Result {
	cli, err := c.get()
	if err != nil {
		return c.Unknown("cannot init docker client", err)
	}
	if err := cli.Ping(ctx); err != nil {
		return c.Crit("Docker daemon not reachable", map[string]any{"error": err.Error()})
	}
	return c.OK("Docker daemon is running", nil)
}

// newDockerContainerCheck config keys:
//
//	name:    container name to look for (required)
//	state:   expected state, default "running"
//	healthy: if true, require a "(healthy)" status (default false)
func newDockerContainerCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &dockerContainerCheck{
		Base:      Base{CheckName: name},
		container: m.Required("name"),
		wantState: strings.ToLower(m.StringDefault("state", "running")),
		healthy:   m.Bool("healthy", false),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *dockerContainerCheck) Run(ctx context.Context) Result {
	cli, err := c.get()
	if err != nil {
		return c.Unknown("cannot init docker client", err)
	}
	containers, err := cli.ListContainers(ctx, true)
	if err != nil {
		return c.Unknown("cannot list containers", err)
	}

	var found *dockerapi.Container
	for i := range containers {
		if containers[i].HasName(c.container) {
			found = &containers[i]
			break
		}
	}
	if found == nil {
		return c.Crit(fmt.Sprintf("container %q not found", c.container),
			map[string]any{"container": c.container})
	}

	details := map[string]any{
		"container": c.container,
		"state":     found.State,
		"status":    found.Status,
		"image":     found.Image,
	}
	if !strings.EqualFold(found.State, c.wantState) {
		return c.Crit(fmt.Sprintf("container %q is %s, want %s", c.container, found.State, c.wantState), details)
	}
	if c.healthy {
		if h := found.Health(); h != "" && !strings.EqualFold(h, "healthy") {
			details["health"] = h
			return c.Crit(fmt.Sprintf("container %q is %s but %s", c.container, found.State, h), details)
		}
	}
	return c.OK(fmt.Sprintf("container %q is %s", c.container, found.State), details)
}
