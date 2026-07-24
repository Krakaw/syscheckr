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
	container string // exact name; empty when prefix is set
	prefix    string // match all containers whose name starts with this
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
//	name:    exact container name to look for
//	prefix:  match every container whose name starts with this (e.g. "dev-api-"
//	         to check all scaled replicas dev-api-1, dev-api-2, ...); every
//	         match must satisfy state/healthy, and at least one must exist
//	state:   expected state, default "running"
//	healthy: if true, require a "(healthy)" status (default false)
//
// Exactly one of name or prefix is required.
func newDockerContainerCheck(name string, cfg map[string]any) (Check, error) {
	m := confutil.New(name, cfg)
	c := &dockerContainerCheck{
		Base:      Base{CheckName: name},
		container: m.String("name"),
		prefix:    m.String("prefix"),
		wantState: strings.ToLower(m.StringDefault("state", "running")),
		healthy:   m.Bool("healthy", false),
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	if (c.container == "") == (c.prefix == "") {
		return nil, fmt.Errorf("%s: exactly one of \"name\" or \"prefix\" is required", name)
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

	var matches []dockerapi.Container
	for i := range containers {
		if (c.prefix != "" && containers[i].NameHasPrefix(c.prefix)) ||
			(c.container != "" && containers[i].HasName(c.container)) {
			matches = append(matches, containers[i])
		}
	}
	if len(matches) == 0 {
		return c.Crit(fmt.Sprintf("%s not found", c.label()),
			map[string]any{"match": c.label()})
	}

	// Every match must satisfy state (and health, if required); the first
	// failure wins so the summary names the offending replica.
	for i := range matches {
		found := matches[i]
		details := map[string]any{
			"container": strings.TrimPrefix(strings.Join(found.Names, ","), "/"),
			"state":     found.State,
			"status":    found.Status,
			"image":     found.Image,
		}
		if !strings.EqualFold(found.State, c.wantState) {
			return c.Crit(fmt.Sprintf("container %q is %s, want %s", details["container"], found.State, c.wantState), details)
		}
		if c.healthy {
			if h := found.Health(); h != "" && !strings.EqualFold(h, "healthy") {
				details["health"] = h
				return c.Crit(fmt.Sprintf("container %q is %s but %s", details["container"], found.State, h), details)
			}
		}
	}
	return c.OK(fmt.Sprintf("%s: %d %s", c.label(), len(matches), c.wantState),
		map[string]any{"match": c.label(), "count": len(matches)})
}

// label describes what the check is looking for, for messages and details.
func (c *dockerContainerCheck) label() string {
	if c.prefix != "" {
		return fmt.Sprintf("containers with prefix %q", c.prefix)
	}
	return fmt.Sprintf("container %q", c.container)
}
