# Running syscheckr as a service

`syscheckr daemon` runs the checks on their cron schedules until it receives
SIGINT/SIGTERM. To keep it running across reboots and restart it if it crashes,
supervise it with your init system.

## Linux (systemd)

Use the example unit in [`syscheckr.service`](./syscheckr.service). The header
comment lists the exact install steps. The key bits for "restart if it fails":

```ini
Restart=on-failure   # restart on non-zero exit, kill, or timeout — not on a clean stop
RestartSec=5s        # wait 5s between restarts
```

with a rate limit in `[Unit]` so a persistently broken config doesn't crash-loop:

```ini
StartLimitIntervalSec=60
StartLimitBurst=5     # >5 restarts in 60s => give up and stay failed
```

```sh
sudo systemctl enable --now syscheckr   # start now + on boot
journalctl -u syscheckr -f              # tail logs
```

## macOS (launchd)

launchd supervises with `KeepAlive`. Put this at
`~/Library/LaunchAgents/ca.syscheckr.daemon.plist` and load it with
`launchctl load ~/Library/LaunchAgents/ca.syscheckr.daemon.plist`.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>            <string>ca.syscheckr.daemon</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/syscheckr</string>
    <string>daemon</string>
    <string>-c</string>
    <string>/usr/local/etc/syscheckr/syscheckr.yaml</string>
    <string>--healthz</string>
    <string>:8080</string>
  </array>
  <!-- Restart on crash, but back off so a broken config doesn't spin. -->
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key> <false/>
  </dict>
  <key>ThrottleInterval</key> <integer>10</integer>
  <key>RunAtLoad</key>        <true/>
  <key>StandardOutPath</key>  <string>/usr/local/var/log/syscheckr.log</string>
  <key>StandardErrorPath</key><string>/usr/local/var/log/syscheckr.log</string>
</dict>
</plist>
```

## Verifying

Either platform: confirm the daemon is up via the health endpoint enabled by
`--healthz :8080`:

```sh
curl -s localhost:8080/healthz
```
