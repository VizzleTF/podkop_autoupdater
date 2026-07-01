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
`main` constructs everything: loads UCI config → opens log + PID lock → resolves the SOCKS5 endpoint from `socks_addr` (`"auto"` → `service.DetectSocksAddr`, empty → none) → builds the tiered transport → creates an `http.Client` for Telegram (timeout = long-poll window + 15s) and a separate one for updates (5min, install.sh fetches can outlast long-poll) → starts `runEmergencyIPRefresh` goroutine → boots the Telegram bot. PID lockfile at `/tmp/podkop_updater.pid`; signal 0 used to verify the recorded PID is alive before refusing to start.

### DNS-free HTTP transport (`go/internal/transport/`)
The Telegram data path **never** consults the system resolver — that was the failure mode (DNS works, later breaks, bot dies). `TieredTransport.RoundTrip` snapshots tiers under a mutex and walks starting from a sticky index; first success updates sticky. A `runStickyReset` goroutine in `main.go` calls `ResetSticky` every 10min to drop the sticky index back to tier 0, so a transient outage that pushed traffic onto the pinned-IP tier doesn't pin it there after SOCKS5 recovers. At most two tiers, in order:

1. SOCKS5 — only when the operator explicitly sets `socks_addr` (verbatim address, or `"auto"` to detect podkop's mixed inbound). The proxy resolves `api.telegram.org` itself, so the local resolver is bypassed.
2. Pinned IPs — a **single** `pinnedTransport` whose `DialTLSContext` substitutes a known Telegram IP for `api.telegram.org` (SNI preserved, so TLS still validates) and walks the current IP set until one completes a **full TLS handshake**. The handshake runs *inside the dialer*, not at the `http.Transport` layer: a hardcoded DC IP that is unreachable from a given geo often accepts TCP but then serves broken/foreign TLS (garbage record or immediate EOF) — a TCP-only check would hand that poisoned connection up and the request would fail with no retry onto another IP. Handshaking in the dialer lets a poisoned IP be skipped within the same request, so the path stays up as long as *any* pinned IP works. Per-IP health (`ipHealth` ok/error timestamps, mirroring TDLib's `DcOptionsSet::Stat`) ranks the dial order via `orderedIPs`: healthy IPs first (most-recent success first), never-tried next, failed last (stalest failure first, so a temporarily-unreachable IP is eventually re-probed rather than pinned-dead forever). Steady state dials one healthy IP; dead/poisoned IPs sink out of the way instead of costing a connect timeout on every reconnect. Health is pruned to the current IP set on `SetEmergencyIPs`. One transport, one connection pool, forced HTTP/1.1. Any non-pinned host (e.g. github for the update path) is resolved + dialed normally via the system resolver — those IPs aren't pinned, and github fetches are on-demand, not the always-on poll. The IP set is swapped in place by `SetEmergencyIPs` (no tier rebuild, sticky-safe).

Background IP refresh is the only place DNS is used, and it uses DoH (fixed resolver IPs over HTTPS) — see below. If DoH fails, the previously known IP set is kept.

`Degraded()` reports whether the *last successful* request fell through to a non-preferred tier (e.g. SOCKS5 down → on pinned IPs). It tracks the last actual success, **not** the sticky index, so `ResetSticky`'s optimistic 10min nudge to tier 0 doesn't make it flap — the transport watchdog (below) polls this and would otherwise emit a spurious recover/degrade pair every reset cycle. Always false for the default single-tier (pinned-IP-only) setup.

### Transport watchdog (`go/internal/telegram/watchdog.go`)
`transportWatch` is a goroutine (started by `Bot.Start`) that proactively alerts the user when the path to Telegram degrades or breaks, and again when it recovers — Telegram never pushes a notification for a silent fallback, so otherwise the user only learns the link broke when a command goes unanswered. Liveness is **passive**: a healthy long-poll completes one round-trip per poll window, so `Tiers.LastSuccess()` advances continuously; the watchdog measures the *actual* update-carrying path rather than a synthetic probe on a fresh connection, and costs zero extra API calls while healthy. Every 30s (a cheap in-memory read) it samples `LastSuccess()` and `Degraded()`. A `getMe` probe is fired **on-demand only** when `LastSuccess()` is stale (> `watchLivenessStale`, 100s — above the 60s poll window + 15s client slack so an idle-but-cycling long-poll never looks stale), to confirm a genuine outage before alerting. Two edge-triggered episodes, each a standalone (untracked) message via `sendAlert`:

- **Outage**: no successful round-trip within `watchLivenessStale` **and** the on-demand `getMe` confirmation fails. The "entered" alert can't be delivered (we're offline), so only the recovery message is sent, reporting how long the path was down (measured from the last successful round-trip). Recovery is detected **passively** — when the long-poll resumes, `LastSuccess()` advances and no probe is needed.
- **Degradation**: `Degraded()` true while still reachable (primary path down, on fallback). Both enter and recover alerts are deliverable. No-op for the single-tier default.

`discovery.go` queries DoH (`transport.DefaultDoHEndpoints`, all IP-literal URLs; podkop's own DoH endpoint is prepended by `dohEndpointsFromPodkop` **only when its host is an IP literal** — a hostname endpoint would need the possibly-broken system resolver to bootstrap, so it's skipped) for current `api.telegram.org` A records **concurrently** (one goroutine per endpoint, so a blocked resolver can't serialize the rest), filters to known Telegram CIDRs, and logs any answer that falls outside every known CIDR (early signal the hardcoded list is drifting). Refresh schedule: initial run 30s after startup, then every 24h. Discovered IPs are merged with the compiled `defaultEmergencyIPs` in `main.go` so a thin DoH reply never shrinks coverage. Merged set is persisted to UCI (`emergency_ips`) so reboots start with the last known list.

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
`InstalledVersion` reads from `opkg` or `apk`. `PODKOP_FAKE_INSTALLED` env var overrides it (normalized through `Normalize` first) — use this for local end-to-end testing without downgrading the real package. `install.sh` runner pipes `"y\ny\n"` into the upstream installer to bypass interactive prompts. The script is fetched **pinned to the detected release tag** (`refs/tags/<tag>/install.sh`), not branch HEAD, so an accidental/compromised commit to podkop's `main` is not executed as root; it falls back to `main` only if the tag has no `install.sh`. After install, `RunUpdate` re-reads the installed version and flags a mismatch with the target, and warns prominently if the post-update DNS check didn't recover (no true downgrade — install.sh only moves forward).

GitHub release lookups (`github.go`) carry an in-memory ETag cache: a `304 Not Modified` short-circuits to the last decoded version, keeping frequent checks under the 60 req/h unauthenticated rate limit. `LatestReleaseFull` also returns the raw tag used for the pinned install.sh.

### DNS health check (`go/internal/service/`)
After every restart/update, polls `fakeip.podkop.fyi` via `127.0.0.42` every 2s up to a 60s budget, waiting for the response to land in podkop's fakeip CIDR. This is the signal that podkop's stack came back up cleanly.

### Access control & concurrency (`go/internal/telegram/`)
`admin_ids` (UCI) gates every command by `From.ID`; empty = anyone in the configured chat (backward-compatible). Denied callbacks get a "Нет доступа" alert. The destructive long-running actions (restart / update podkop / self-update) run under a single `botState.busy` guard (`tryBusy`/`clearBusy`) so a double-click or an overlapping auto-update cannot launch two `install.sh` runs as root at once; a rejected click answers "Занято". `/status` and `/log` are read-only and use `allowTextCmd` (no menu reset). Self-update verifies a `<asset>.sha256` published alongside the binary (best-effort: a 404 is tolerated with a warning, a mismatch is fatal).

## Persistence (UCI: `/etc/config/podkop_updater`, section `settings`)

| key             | type   | meaning                                                     |
|-----------------|--------|-------------------------------------------------------------|
| `bot_token`     | string | Telegram bot token (required)                               |
| `chat_id`       | int    | Telegram chat ID (required)                                 |
| `check_interval`| int    | Hours between podkop version checks (default 6)             |
| `router_label`  | string | Optional human-readable router name; prefixed in bold to every menu message via `Bot.withLabel`. Empty = fall back to hostname. Useful when multiple routers post into one shared chat. |
| `admin_ids`     | string | Space-separated Telegram user IDs allowed to issue commands. Empty = anyone in the chat (backward-compatible). Gated by `From.ID` on every callback and slash command. |
| `auto_update`   | bool   | `1`/`true` = periodic check auto-installs new podkop releases (still notify-only for the updater itself). Default off. |
| `socks_addr`    | string | Explicit SOCKS5 endpoint for the Telegram data path. Empty/unset = pinned-IP only (no system DNS). `"auto"` = detect podkop's mixed inbound via `service.DetectSocksAddr`. Any other value = used verbatim as `host:port`. |
| `emergency_ips` | string | Space-separated; written by the daemon, read on next start  |
| `menu_mid`      | int    | Tracked Telegram menu message ID; written on every transition |

In-memory state only besides UCI. Log lives at `/tmp/podkop_update.log` with in-place rotation at ~200 lines (keeps the last 100).

## Conventions specific to this repo

- All exported `internal/` packages are wired from `main.go`; don't add a second composition root.
- The bash version is gone (commit 3325cc3). The installer still removes a stray `/usr/bin/podkop_updater.sh` if a user re-runs `install.sh` against a legacy install — keep that branch until it's safe to drop.
- Conflicts with the upstream podkop's own `/usr/bin/podkop_bot` are a real failure mode (two daemons stealing each other's Telegram updates). The installer and README both warn about it; don't paper over this in code.
- `clientTotalBudget = longPollTimeout + 15s` in `main.go` — if you change long-poll timing, change the client timeout too, or the long poll will be killed mid-flight.
- Telegram bot tokens are `<bot_id>:<secret>`. `install.sh` prints only the prefix before `:`. Don't log the full token anywhere.
