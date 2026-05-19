# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Single static Go binary (`podkop_updater`) that runs as a procd service on OpenWrt/ImmortalWrt routers. It polls Telegram for inline-menu commands and watches the upstream [`podkop`](https://github.com/itdoginfo/podkop) GitHub repo for new releases, exposing check / restart / update actions through a tracked Telegram message.

All Go source lives under `go/`. The repo root holds `install.sh` (the curl|sh installer), the READMEs, and `.github/workflows/release.yml` (GitHub only discovers workflows at the repo root, not in `go/`).

## Commands

All `make` targets run from `go/`:

```sh
cd go
make build           # current host → dist/podkop_updater
make build-all       # cross-compile amd64, arm64, armv7, mipsle, mips (softfloat)
make upx             # UPX-pack the build-all matrix in place (skips if upx missing)
make test            # go test ./...
make fmt vet tidy    # standard hygiene
```

Run a single test:

```sh
cd go
go test ./internal/transport -run TestTierCascade -v
```

Release: push an annotated `v*` tag from `main`. The workflow builds the five-arch matrix, UPX-packs each binary in place, and uploads one asset per arch (`podkop_updater-<arch>`) to a GitHub release. Asset names carry no `.upx` suffix — the packed binary is the distributed artifact.

## Architecture

### Module wiring (`go/cmd/podkop_updater/main.go`)
`main` constructs everything: loads UCI config → opens log + PID lock → picks SOCKS5 endpoint via `service.DetectSocksAddr` → builds the tiered transport → creates an `http.Client` for Telegram (timeout = long-poll window + 15s) and a separate one for updates (5min, install.sh fetches can outlast long-poll) → starts `runEmergencyIPRefresh` goroutine → boots the Telegram bot. PID lockfile at `/tmp/podkop_updater.pid`; signal 0 used to verify the recorded PID is alive before refusing to start.

### Three-tier HTTP transport (`go/internal/transport/`)
`TieredTransport.RoundTrip` snapshots tiers under a mutex and walks starting from a sticky index; first success updates sticky. Tiers in order:

1. SOCKS5 to podkop's mixed inbound (only added if `service.DetectSocksAddr` finds one in the live sing-box config).
2. Direct.
3. Emergency tiers — one per discovered Telegram IP. Each pins `api.telegram.org` to a specific IP (DNS override scoped to that host, SNI preserved so TLS still validates).

`discovery.go` queries DoH (endpoint pulled from podkop's UCI; falls back to `transport.DefaultDoHEndpoints`) for current `api.telegram.org` A records, filters to known Telegram CIDRs. Refresh schedule: initial run 30s after startup, then every 24h. Discovered IPs are merged with the compiled `defaultEmergencyIPs` in `main.go` so a thin DoH reply never shrinks coverage. Merged set is persisted to UCI (`emergency_ips`) so reboots start with the last known list.

### Telegram state machine (`go/internal/telegram/`)
Built on `github.com/go-telegram/bot`. The bot owns exactly one tracked menu message at a time, cycling through states:

```
Default (3 buttons) → Busy (no buttons) → Result + ОК → Default
                                                       ↘ Update-available (single Обновить) → Busy → Result
```

Periodic auto-detection of a new podkop release **deletes** the current tracked menu and **sends a fresh message** in Update-available state — Telegram does not push notifications for edits, so a delete-then-send is required to wake the user. Tracked message ID is persisted to UCI (`menu_mid`) via `PersistMenuMID` so restarts don't strand the menu. Orphan adoption: if a click arrives on a non-tracked old menu, that message gets promoted to "tracked" and the previously tracked one is deleted.

### Self-update (`go/internal/selfupdate/`)
1. Download `releases/latest/download/podkop_updater-<arch>`.
2. Atomic rename: `current → current.bak`, `new → current`.
3. `time.AfterFunc(3s, os.Exit)` so the bot can draw a "перезапуск через 3с" message before procd respawns.
4. On next start, `CleanupBackup` removes the `.bak`. Manual rollback if procd's respawn budget is exhausted: `mv podkop_updater.bak podkop_updater`.

### podkop update path (`go/internal/updater/`)
`InstalledVersion` reads from `opkg` or `apk`. `PODKOP_FAKE_INSTALLED` env var overrides it (normalized through `Normalize` first) — use this for local end-to-end testing without downgrading the real package. `install.sh` runner pipes `"y\ny\n"` into the upstream installer to bypass interactive prompts.

### DNS health check (`go/internal/service/`)
After every restart/update, polls `fakeip.podkop.fyi` via `127.0.0.42` every 2s up to a 60s budget, waiting for the response to land in podkop's fakeip CIDR. This is the signal that podkop's stack came back up cleanly.

## Persistence (UCI: `/etc/config/podkop_updater`, section `settings`)

| key             | type   | meaning                                                     |
|-----------------|--------|-------------------------------------------------------------|
| `bot_token`     | string | Telegram bot token (required)                               |
| `chat_id`       | int    | Telegram chat ID (required)                                 |
| `check_interval`| int    | Hours between podkop version checks (default 6)             |
| `router_label`  | string | Optional human-readable router name; prefixed in bold to every menu message via `Bot.withLabel`. Empty = fall back to hostname. Useful when multiple routers post into one shared chat. |
| `emergency_ips` | string | Space-separated; written by the daemon, read on next start  |
| `menu_mid`      | int    | Tracked Telegram menu message ID; written on every transition |

In-memory state only besides UCI. Log lives at `/tmp/podkop_update.log` with in-place rotation at ~200 lines (keeps the last 100).

## Conventions specific to this repo

- All exported `internal/` packages are wired from `main.go`; don't add a second composition root.
- The bash version is gone (commit 3325cc3). The installer still removes a stray `/usr/bin/podkop_updater.sh` if a user re-runs `install.sh` against a legacy install — keep that branch until it's safe to drop.
- Conflicts with the upstream podkop's own `/usr/bin/podkop_bot` are a real failure mode (two daemons stealing each other's Telegram updates). The installer and README both warn about it; don't paper over this in code.
- `clientTotalBudget = longPollTimeout + 15s` in `main.go` — if you change long-poll timing, change the client timeout too, or the long poll will be killed mid-flight.
- Telegram bot tokens are `<bot_id>:<secret>`. `install.sh` prints only the prefix before `:`. Don't log the full token anywhere.
