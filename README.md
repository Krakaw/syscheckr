# syscheckr

A single Go binary for custom system health checks with pluggable reporting.
Define checks (disk, CPU, memory, Docker, logs, HTTP, arbitrary commands) in
YAML, and route results by severity to logs, Slack, generic webhooks, or Linear
tickets. Run it once from cron/systemd/launchd, or as a long-running daemon with
its own cron scheduler.

## Quick start

```sh
go build -o syscheckr ./cmd/syscheckr

cp config.example.yaml syscheckr.yaml      # edit to taste
./syscheckr validate -c syscheckr.yaml     # parse + validate, no execution
./syscheckr run -c syscheckr.yaml          # run once, report, exit
./syscheckr daemon -c syscheckr.yaml       # run on schedules until Ctrl-C
```

`run` exits `0` when the worst result is OK/warn and `2` when any check is
critical or unknown — so a cron job fails loudly only on real problems.

## Commands

| Command | Description |
|---|---|
| `run` | Run every check once, report, exit (0 ok/warn, 2 crit/unknown). |
| `daemon` | Run checks on their cron `schedule` until SIGINT/SIGTERM. `--healthz :8080` serves a JSON health endpoint. |
| `validate` | Parse and validate the config without running anything. |
| `list-checks` / `list-reporters` | Print registered types. |
| `version` | Print build info. |

## Configuration

Config is YAML with `${ENV}` / `${ENV:-default}` interpolation for secrets. See
[`config.example.yaml`](./config.example.yaml) for a complete annotated file.

```yaml
defaults:
  timeout: 10s
checks:
  - name: root-disk
    type: disk
    schedule: "*/5 * * * *"     # daemon mode only (cron or @every syntax)
    tags: [system]
    config: { path: /, warn_percent: 80, crit_percent: 90 }
reporters:
  - name: slack-alerts
    type: slack
    min_severity: warn          # route by severity
    only_failing: true
    config: { webhook_url: ${SLACK_WEBHOOK_URL} }
```

### Check types

| Type | Purpose | Key config |
|---|---|---|
| `disk` | Filesystem usage % | `path`, `warn_percent`, `crit_percent` |
| `cpu` | CPU busy % over a sample | `sample`, `warn_percent`, `crit_percent` |
| `memory` | Virtual memory used % | `warn_percent`, `crit_percent` |
| `docker_running` | Docker daemon reachable | — |
| `docker_container` | Named container in expected state | `name`, `state`, `healthy` |
| `log` | Count regex matches in a file | `path`, `pattern`, `window`, `warn_count`, `crit_count` |
| `http` | Probe an endpoint (status + latency) | `url`, `expect_status`, `warn_ms`, `crit_ms`, `headers` |
| `command` | Run any command, map exit/output to status | `command`, `args`, `shell`, `expect_exit`, `match_pattern`, `warn_pattern`, `crit_pattern` |

Docker checks talk to the Docker Engine API over the socket from `DOCKER_HOST`
(default `unix:///var/run/docker.sock`) — no Docker CLI or SDK required.

### Reporter types

| Type | Purpose | Key config |
|---|---|---|
| `log` | Structured stdout/file output (slog) | `format` (text/json), `output`, `level` |
| `slack` | Incoming-webhook message, attachment per result | `webhook_url`, `username`, `channel` |
| `webhook` | POST a JSON payload to any URL | `url`, `headers`, `secret` (HMAC-SHA256), `redact` |
| `linear` | Create Linear issues for failing checks, deduped | `api_key`, `team_id`, `label_ids`, `dedupe_window`, `redact`, `state_path` |

`redact: true` strips `samples` (matched log lines) and `output` (command stdout) from the data sent to that reporter, so secret-bearing log/command content stays off-box. The `slack` reporter always omits these from its fields.

### Routing

Each reporter filters which results it sees:

- `min_severity` — drop results below this severity (`ok`/`warn`/`crit`/`unknown`).
- `only_failing` — drop OK results entirely.
- `checks` — only these check names.
- `tags` — only results carrying one of these tags.

So warnings can go to Slack while only criticals open Linear tickets. The Linear
reporter records a timestamp per check in a JSON state file (`state_path`) and
suppresses re-filing within `dedupe_window` (default 24h), so you get one ticket
per check per window rather than one every run.

## Extending

Checks and reporters are Go interfaces backed by registries. To add a type,
implement the interface and register it in an `init()`:

```go
// internal/check/mycheck.go
func init() { check.Register("mycheck", newMyCheck) }

func newMyCheck(name string, cfg map[string]any) (check.Check, error) { ... }
// type satisfies: Name() string, Run(ctx) check.Result
```

Reporters follow the same pattern with `report.Register`. The `confutil` package
provides typed, error-collecting accessors over the raw `config:` map.

## Architecture

```
cmd/syscheckr        CLI entrypoint
internal/cli         cobra command tree
internal/config      YAML schema, ${ENV} expansion, validation
internal/check       Check interface + registry, built-in checks
internal/report      Reporter interface + registry + routing, built-in reporters
internal/runner      builds checks/reporters, runs concurrently w/ timeouts, fans out
internal/scheduler   daemon mode: cron-per-schedule, graceful shutdown, /healthz
internal/dockerapi   tiny stdlib Docker Engine API client
internal/state       JSON key/time store for reporter dedupe
internal/confutil    typed accessors over raw config maps
```

## Development

```sh
go test ./...        # unit tests (table-driven thresholds, routing, httptest reporters)
go vet ./...
```
