# syscheckr

A single Go binary for custom system health checks with pluggable reporting.
Define checks (disk, mounts, CPU, memory, Docker, logs, HTTP, arbitrary commands) in
YAML, and route results by severity to logs, Slack, generic webhooks, or Linear
tickets. Run it once from cron/systemd/launchd, or as a long-running daemon with
its own cron scheduler.

## Install

Download and install the latest Linux release (auto-detects amd64/arm64, verifies
the SHA-256 checksum):

```sh
curl -fsSL https://raw.githubusercontent.com/Krakaw/syscheckr/main/scripts/install.sh | bash
```

Overrides: `VERSION=v0.1.2` pins a release; `INSTALL_DIR="$HOME/.local/bin"`
installs without sudo. Prebuilt `.tar.gz` archives are on the
[releases page](https://github.com/Krakaw/syscheckr/releases).

Or build from source (any platform with Go 1.25+):

```sh
go build -o syscheckr ./cmd/syscheckr
```

Once installed, `syscheckr update` self-updates in place — it resolves the latest
release (or `--version vX.Y.Z`), verifies the checksum, and atomically replaces the
running binary. Re-run with `sudo` if syscheckr lives in a root-owned directory.

## Quick start

```sh
./syscheckr init                           # interactive: pick checks/reporters, writes syscheckr.yaml
# or: cp config.example.yaml syscheckr.yaml   # start from the annotated example instead
./syscheckr validate -c syscheckr.yaml     # parse + validate, no execution
./syscheckr run -c syscheckr.yaml          # run once, report, exit
./syscheckr daemon -c syscheckr.yaml       # run on schedules until Ctrl-C
```

`run` exits `0` when the worst result is OK/warn and `2` when any check is
critical or unknown — so a cron job fails loudly only on real problems.

## Commands

| Command | Description |
|---|---|
| `init` | Interactively pick check/reporter types and write a starter config (`-o -` for stdout, `--force` to overwrite). |
| `run` | Run every check once, report, exit (0 ok/warn, 2 crit/unknown). |
| `daemon` | Run checks on their cron `schedule` until SIGINT/SIGTERM. `--healthz :8080` serves a JSON health endpoint. |
| `validate` | Parse and validate the config without running anything. |
| `list-checks` / `list-reporters` | Print registered types (`--describe` shows each type's config fields). |
| `update` | Download the latest release (or `--version vX.Y.Z`) and replace this binary in place. |
| `version` | Print build info. |

## Configuration

Config is YAML with `${ENV}` / `${ENV:-default}` interpolation for secrets. See
[`config.example.yaml`](./config.example.yaml) for a complete annotated file.

### Secrets

`${ENV}` references expand from the process environment at load time. Provide them
however you run syscheckr:

- **`.env` file (auto-loaded):** `run`/`daemon`/`validate` automatically load a
  `.env` from the working directory and from the config file's directory, if
  present. Real environment variables always win over the file, so systemd/CI
  secrets are never clobbered.
- **`--env-file path`:** load a specific file (required to exist).
- **Real env vars:** export them yourself, or via systemd `EnvironmentFile=`,
  launchd, or a cron wrapper.

```sh
# secrets.env  (chmod 600, git-ignored)
SLACK_WEBHOOK_URL=https://hooks.slack.com/...
LINEAR_API_KEY=lin_api_xxx
LINEAR_TEAM_ID=team-uuid
```
```sh
./syscheckr run --env-file secrets.env      # explicit
./syscheckr run                             # auto-loads ./.env if present
```

`.env` format: `KEY=VALUE` per line, `#` comments, optional `export ` prefix,
single quotes are literal, double quotes process `\n`/`\t`/`\"`/`\\`. A missing
required secret fails fast with `config "webhook_url": is required`.

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
| `mount` | Path is mounted (crit if missing or remounted read-only) | `path`, `device`, `fstype`, `read_only` |
| `cpu` | CPU busy % over a sample | `sample`, `warn_percent`, `crit_percent` |
| `memory` | Virtual memory used % | `warn_percent`, `crit_percent` |
| `docker_running` | Docker daemon reachable | — |
| `docker_container` | Named container (or all `prefix` replicas) in expected state | `name`\|`prefix`, `state`, `healthy` |
| `log` | Count regex matches in a file | `path`, `pattern`, `window`, `warn_count`, `crit_count` |
| `http` | Probe an endpoint (status + latency) | `url`, `expect_status`, `warn_ms`, `crit_ms`, `headers` |
| `command` | Run any command, map exit/output to status | `command`, `args`, `shell`, `pwd`, `expect_exit`, `match_pattern`, `warn_pattern`, `crit_pattern` |

Docker checks talk to the Docker Engine API over the socket from `DOCKER_HOST`
(default `unix:///var/run/docker.sock`) — no Docker CLI or SDK required.

### Reporter types

| Type | Purpose | Key config |
|---|---|---|
| `log` | Structured stdout/file output (slog) | `format` (text/json), `output`, `level` |
| `slack` | Incoming-webhook message, attachment per result | `webhook_url`, `username`, `channel` |
| `webhook` | POST a JSON payload to any URL | `url`, `headers`, `secret` (HMAC-SHA256), `redact` |
| `linear` | Create Linear issues for failing checks | `api_key`, `team_id`, `label_ids`, `redact` |
| `heartbeat` | Prove this host is alive to a syscheckr server | `url`, `key`, `timeout`, `token`, `redact`, `http_timeout` |

`redact: true` strips `samples` (matched log lines) and `output` (command stdout) from the data sent to that reporter, so secret-bearing log/command content stays off-box. The `slack` reporter always omits these from its fields.

### Routing

Each reporter filters which results it sees:

- `min_severity` — drop results below this severity (`ok`/`warn`/`crit`/`unknown`).
- `only_failing` — drop OK results entirely.
- `checks` — only these check names.
- `tags` — only results carrying one of these tags.

So warnings can go to Slack while only criticals open Linear tickets.

### Alert on change

A reporter is only told about a check when its **status changes**, so a disk
sitting at 85% alerts once instead of every run:

```
ok → warn   alert          warn → warn  quiet
warn → crit alert          crit → crit  quiet
crit → ok   alert          ok → ok      quiet
```

Recovery (`→ ok`) only reaches reporters whose route accepts OK results, so a
reporter with `only_failing: true` stays silent on recovery. Two per-reporter
knobs:

- `repeat_alerts: true` — send every run, as before. Defaults to `true` for the
  `log` reporter, which is a record rather than an alert, and `false` elsewhere.
- `dedupe_window: 24h` — re-send an unchanged status once this long has passed.
  Defaults to `24h` for `linear` (one ticket per check per day while it stays
  failing) and `0` — change only — elsewhere; set it to `0` explicitly on a
  linear reporter to get change-only tickets. A status change always breaks
  through the window, so an escalation to crit is never held back.

```yaml
reporters:
  - name: slack-alerts
    type: slack
    min_severity: warn
    dedupe_window: 4h       # nag every 4h while it stays broken
```

The last-alerted status per reporter/check lives in a JSON file so this works
under cron (`syscheckr run`) as well as `daemon`; set its location with
`state.path` (default `syscheckr-state.json`). A reporter that fails to deliver
is not recorded, so the next run retries it.

## Heartbeat: watching whole hosts

A host that dies stops reporting, and silence looks exactly like health. To
catch that, one instance can run as a **server**: clients ping it with a key and
their own expected timeout, and a missed deadline becomes a `crit` result on the
server, routed through the server's own reporters.

Same binary on both ends — the config file decides the role.

**Server** (`daemon` mode only):

```yaml
server:
  listen: ":8080"                # empty/absent disables the server
  token: "${SYSCHECKR_TOKEN}"    # clients send it as: Authorization: Bearer <token>
  schedule: "@every 30s"         # how often deadlines are evaluated
reporters:
  - name: slack
    type: slack
    config:
      webhook_url: "${SLACK_WEBHOOK}"
```

Leave `only_failing` off here: it drops OK results, so the server would tell
you a host went dark but never that it came back.

A server needs no `checks:` of its own — its results come from its clients.
`server.listen` serves `/healthz` too, and `--healthz` still works: if both are
given the flag wins.

**Client** — just another reporter, so it rides the normal check cycle:

```yaml
reporters:
  - name: home-server
    type: heartbeat
    config:
      url: http://mon:8080/ping
      key: laptop                # this host's identity on the server
      timeout: 5m                # server alerts if it sees no ping for this long; max 24h
      token: "${SYSCHECKR_TOKEN}"
      # http_timeout: 15s        # request timeout for the ping itself
```

Results appear on the server as `heartbeat:<key>`, one per client, so each host
alerts and recovers independently under the usual [alert on
change](#alert-on-change) rules — one crit when it goes dark, one ok when it
comes back.

Notes:

- Set `timeout` comfortably above the client's check schedule (default
  `@every 1m`), or the server will alert between pings. It must be `>0` and at
  most `24h` — both ends enforce that, so a bad value fails at startup rather
  than every run.
- Detection is late by up to one `server.schedule` tick, plus the client's own
  schedule.
- Without `token` anyone who can reach the port can register or refresh a key —
  set it, or keep the port on a VPN/LAN.
- Keys are learned from the first ping, so a client that has *never* pinged is
  invisible, and restarting the server forgets every key until each client pings
  again.
- Clients send their full results alongside the ping. The server ignores them
  today; that is the seam for server-side stats processing.

`POST /ping` takes `{"key":"laptop","timeout":"5m"}` and answers `204`, or
`400`/`401`/`405`/`413` — so anything that can make an HTTP request can act as a
client:

```sh
curl -X POST http://mon:8080/ping -H "Authorization: Bearer $SYSCHECKR_TOKEN" \
  -d '{"key":"backup-job","timeout":"23h"}'
```

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
internal/scheduler   daemon mode: cron-per-schedule, graceful shutdown, HTTP server
internal/heartbeat   heartbeat server: /ping ingest, deadline -> crit result
internal/dockerapi   tiny stdlib Docker Engine API client
internal/state       JSON key/time store for reporter dedupe
internal/confutil    typed accessors over raw config maps
```

## Development

```sh
go test ./...        # unit tests (table-driven thresholds, routing, httptest reporters)
go vet ./...
```
