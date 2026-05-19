# podkop_updater architecture

A single Go binary that runs as a long-lived procd service on the router
and polls Telegram for inline-menu commands. Configuration lives in UCI
(`/etc/config/podkop_updater`); state in memory; logs in
`/tmp/podkop_update.log`.

## Modules

```
cmd/podkop_updater/main.go        Wire dependencies; PID-file lock;
                                  signal handling; periodic IP refresh.

internal/config/                  Load UCI settings: bot_token, chat_id,
                                  check_interval (hours), emergency_ips
                                  (space-separated string). SaveEmergencyIPs
                                  persists the discovered list back to UCI.

internal/logger/                  File logger with in-place rotation when
                                  the log crosses a soft line limit
                                  (~200 lines). Single global instance.

internal/telegram/                Thin layer over github.com/go-telegram/bot.
                                  Owns the one tracked menu message and its
                                  state machine (default / busy / result /
                                  update-available). Periodic version check
                                  goroutine triggers a fresh notification
                                  message when a new podkop release is
                                  discovered. Orphan-message adoption: if
                                  the user clicks an old menu, the bot
                                  promotes that message to "tracked" and
                                  deletes the previous one.

internal/transport/               http.RoundTripper that walks tiers in
                                  order on failure and stays sticky on
                                  success. Tier 0 (SOCKS5 to podkop's
                                  mixed inbound) is added only if the
                                  podkop sing-box config exposes one;
                                  tier 1 is direct; further tiers pin
                                  api.telegram.org to specific IPs
                                  (override DNS for that host only, SNI
                                  preserved so TLS still validates).
                                  discovery.go queries DoH (the endpoint
                                  configured in podkop's UCI) for current
                                  api.telegram.org A records, filtering
                                  to known Telegram CIDRs.

internal/service/                 podkop restart, DNS health check (polls
                                  fakeip.podkop.fyi via 127.0.0.42 every
                                  2s up to a 60s budget), SOCKS5 endpoint
                                  detection from podkop UCI + sing-box
                                  config, and the Runner that implements
                                  telegram.UpdateRunner.

internal/selfupdate/              Atomic binary replacement:
                                  current -> current.bak, new -> current,
                                  then exit so procd respawns. The .bak
                                  is removed on the next clean startup
                                  (CleanupBackup).

internal/updater/                 GitHub release fetch (LatestRelease for
                                  podkop, LatestSelfRelease for
                                  podkop_autoupdater itself), semver
                                  comparison, installed-version reader
                                  (opkg or apk; honors PODKOP_FAKE_INSTALLED
                                  env for testing), and the install.sh
                                  runner that pipes "y\ny\n" into the
                                  upstream installer.
```

## Transport cascade

`TieredTransport.RoundTrip` snapshots the tier slice under a mutex, then
iterates starting from the sticky index, wrapping around. On the first
success it updates the sticky index. Tier rebuild
(`RebuildEmergency`) preserves SOCKS5 and direct, replaces emergency
entries, and resets sticky to 0.

Defaults:
- per-tier connect timeout 3s, TLS handshake 5s
- emergency IP refresh every 24h (initial run 30s after startup)
- discovered IPs merged with compiled defaults so a single-record DoH
  reply doesn't shrink coverage

## Self-update flow

1. User taps "Обновить updater".
2. Runner.RunSelfUpdate downloads `releases/latest/download/podkop_updater-<arch>`.
3. Atomic rename: current → .bak, new → current.
4. `time.AfterFunc(3s, os.Exit)` to give the bot time to draw the
   "перезапуск через 3с..." status message.
5. procd respawns the new binary.
6. On startup the new binary calls `CleanupBackup` to remove the .bak.
7. If the new binary failed to start (procd's respawn budget exhausted)
   the user can manually restore: `mv podkop_updater.bak podkop_updater`
   and start the service.

## Telegram menu state machine

```
        +----------+
        | Default  |  (3 buttons)
        +----+-----+
             |
             |   any check/restart/update press
             v
        +--------+
        | Busy   |  (no buttons)
        +---+----+
            |
            |   action returns status
            v
   +--------+-------+
   | Result + ОК    |
   +-+--------+-----+
     |        |
     | ОК     | (update available)
     v        v
   Default  +-----------------+
            | Update available|  (single Обновить button)
            +--+--------------+
               |
               |   Обновить
               v
            Busy → ... → Result
```

Periodic auto-find of a new podkop release deletes the current tracked
menu and sends a fresh message in the Update-available state (so the
user gets a notification, not a silent edit).

## Persistence

UCI keys under `podkop_updater.settings`:

| key             | type   | meaning                                   |
|-----------------|--------|-------------------------------------------|
| bot_token       | string | Telegram bot token (required)             |
| chat_id         | int    | Telegram chat ID (required)               |
| check_interval  | int    | hours between podkop version checks (default 6) |
| emergency_ips   | string | space-separated IPs written by daemon     |

## Testing

`PODKOP_FAKE_INSTALLED` env var overrides the result of
`updater.InstalledVersion`. Useful for exercising the podkop update path
without downgrading the real package.

`go test ./...` covers semver normalization, comparison, and the tier
cascade (mock RoundTrippers, sticky behavior, all-fail).

## CI

`../.github/workflows/release.yml` builds a five-architecture matrix
(amd64, arm64, armv7, mipsle softfloat, mips softfloat) on push of any
`v*` tag, UPX-packs each binary in place, and uploads one asset per arch
(`podkop_updater-<arch>`) to the GitHub release. No `.upx` suffix — the
packed binary is the only distributed artifact, so install.sh and
self-update both fetch it under its plain name.
