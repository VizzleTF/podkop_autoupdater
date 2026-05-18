# podkop_updater (Go daemon) — Design

## Context

Replacement for `../podkop_updater.sh` (~790 lines bash, daemon mode). Same external behavior, same UCI config, same procd integration. Goal: fix structural problems (P1–P6 in audit), reduce external runtime dependencies (`jq`, `curl`, `wget`, `nslookup`), enable real testing.

**Out of scope (this iteration):**
- LuCI web UI page (separate concern, future phase)
- Legacy cron modes (1, 2, 3 in current installer) — daemon is the default and most-used path; cron modes covered by `podkop_updater check` and `podkop_updater force-update` subcommands

## Non-goals

- Binary size below 2 MB (already measured ~1.7 MB UPX on mipsle, acceptable)
- Removing shell-out for opkg/apk/uci (idiomatic on OpenWrt, no benefit to reimplement)
- Webhook mode (long polling sufficient, simpler)

## Architecture overview

```
┌──────────────────────────────────────────────────────────┐
│ cmd/podkop_updater/main.go                               │
│   wire dependencies, parse flags (--daemon, check,       │
│   force-update, self-update), call subcommand            │
└──────────┬───────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────┐
│ internal/                                                │
│                                                          │
│  config/  ──── UCI read via `uci -q get` exec            │
│                                                          │
│  transport/ ── http.RoundTripper with sticky-tier        │
│                fallback (SOCKS5 → direct → emergency IP) │
│                                                          │
│  telegram/ ─── thin wrapper over github.com/go-telegram/ │
│                bot, injecting transport.RoundTripper     │
│                                                          │
│  updater/ ──── GitHub API release fetch, semver compare, │
│                run podkop install.sh                     │
│                                                          │
│  service/ ──── /etc/init.d/podkop restart, DNS check     │
│                                                          │
│  selfupdate/ ─ atomic binary swap with .bak rollback     │
│                                                          │
│  logger/ ───── log rotation, leveled output to LOG_FILE  │
└──────────────────────────────────────────────────────────┘
```

## External dependencies

Single Go module dependency for HTTP+TLS+JSON+Telegram bot:

| Package | Purpose | Notes |
|---------|---------|-------|
| `github.com/go-telegram/bot` v1.20.0+ | Telegram Bot API client | context-aware, idiomatic, used in prototype, all Bot API 7.x methods |
| `golang.org/x/net/proxy` | SOCKS5 dialer | stdlib-adjacent |
| stdlib `net/http`, `encoding/json`, `crypto/tls`, `context` | rest | — |

**Deliberately NOT used:** `cobra`, `viper`, `logrus`, `zap`, `goreleaser`. Avoid bloat — single binary should stay under 2 MB UPX'd. Use stdlib `flag`, stdlib `log/slog`.

## Module / package decisions

### Single binary, multiple subcommands (no separate binaries)

```
podkop_updater --daemon           # main mode (procd)
podkop_updater check              # one-shot check, exit 0/1
podkop_updater force-update       # check + update without TG confirm
podkop_updater self-update        # download new binary, restart
podkop_updater dry-run            # simulate flow
```

Implementation: dispatch on `os.Args[1]` in `main.go`. Total ~50 lines of CLI scaffolding, no `cobra`.

### Config (UCI)

`internal/config/uci.go`:
```go
type Config struct {
    BotToken      string
    ChatID        int64
    CheckInterval time.Duration
}

func Load() (*Config, error) {
    // exec.Command("uci", "-q", "get", "podkop_updater.settings.bot_token")
}
```
Shell-out to `uci`. No native parser — UCI format has edge cases (lists, includes) we don't need to handle.

### Transport (`internal/transport`)

Custom `http.RoundTripper` wrapping multiple `http.Transport` instances:

```go
type TieredTransport struct {
    mu       sync.Mutex
    current  tier             // sticky, last-known good
    tiers    []tier           // ordered: SOCKS, direct, emergency IPs
    emergencyIPs []string
    socksAddr string
}

func (t *TieredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    // 1. Try sticky tier with short connect-timeout (5s)
    // 2. If fail, walk cascade, set new sticky on success
    // 3. Return last error if all fail
}
```

Each tier is a `*http.Transport` with custom `DialContext`:
- SOCKS5 tier: `proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct).Dial`
- Direct tier: stdlib default
- Emergency IP tier: `func(ctx, network, addr) { addr = ip + ":443"; return d.DialContext(...) }` — SNI preserved by TLS layer

Same `RoundTripper` injected into:
- Telegram bot's `http.Client`
- GitHub API client
- podkop install.sh downloader (`curl_pipe_sh()` equivalent)

This solves P2 (sticky tier duplication) and P3 (transport tied to telegram only).

### Self-update (`internal/selfupdate`)

Atomic with rollback:
```
1. Download new bin → /tmp/podkop_updater.new
2. Verify size > 0, exec bit settable
3. Backup: rename /usr/bin/podkop_updater → /usr/bin/podkop_updater.bak
4. Rename /tmp/podkop_updater.new → /usr/bin/podkop_updater
5. Write rollback sentinel: /tmp/podkop_updater.update_pending
6. Exit (procd respawn picks up new binary)
7. On startup, if sentinel exists:
     - Start health check timer (60s)
     - If reaches normal poll loop → delete sentinel, delete .bak
     - If crashes/exits before timer → procd respawn cycle ends:
       installer-style watchdog OR cold rollback via init.d wrapper
```

For MVP: skip cold-rollback (rare case). Document `.bak` and rollback procedure for manual recovery. Solves P5.

### Telegram (`internal/telegram`)

Thin wrapper. Idiomatic usage of `go-telegram/bot`:

```go
type Bot struct {
    bot     *bot.Bot
    chatID  int64
    menuMID int  // protected by mu
    state   menuState
    mu      sync.Mutex
}

// register handlers in Init()
b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check", bot.MatchTypeExact, b.onCheck)
b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restart", bot.MatchTypeExact, b.onRestart)
// ...
```

The lib handles long-polling, offset tracking, callback answering. Solves P1 (no manual jq pipelines per update).

### Logger (`internal/logger`)

stdlib `log/slog` with custom file handler:
- Rotation: on each log call, check file size; if > 200 lines, truncate to last 100 (port from `rotate_log` 109-115 but atomic via rename)
- Output: text format, prefix with timestamp
- Single global `*slog.Logger`

### Service ops (`internal/service`)

`/etc/init.d/podkop restart` via `exec.Command`.
DNS check via `net.Resolver` with custom Dial to `127.0.0.42:53` — no `nslookup` binary needed.
**Critical fix for M5**: DNS check 60s wait runs in goroutine with `context.WithTimeout`. Daemon stays responsive to callbacks.

## File layout

```
go/
├── go.mod
├── go.sum
├── DESIGN.md (this file)
├── README.md (user-facing)
├── Makefile                        # build, build-all, clean
├── cmd/
│   └── podkop_updater/
│       └── main.go                 # CLI dispatch + wire
├── internal/
│   ├── config/uci.go
│   ├── transport/
│   │   ├── transport.go            # TieredTransport
│   │   └── transport_test.go
│   ├── telegram/
│   │   ├── bot.go                  # init bot, register handlers
│   │   ├── menu.go                 # send_or_edit, default/update menu
│   │   └── handlers.go             # callback handlers
│   ├── updater/
│   │   ├── github.go               # latest release fetch
│   │   ├── version.go              # semver compare, opkg/apk read
│   │   ├── update.go               # run install.sh
│   │   └── version_test.go
│   ├── service/
│   │   ├── podkop.go               # restart, dns check
│   │   └── socks.go                # detect_socks_proxy port
│   ├── selfupdate/
│   │   └── selfupdate.go
│   └── logger/
│       └── logger.go
├── scripts/
│   ├── init.d/podkop_updater       # procd stub
│   └── install.sh                  # arch-detect + GitHub release download
└── .github/
    └── workflows/
        └── release.yml             # cross-compile matrix on tag push
```

## CI / release

`.github/workflows/release.yml` on tag push:
- Matrix: `{amd64, arm64, armv7, mipsle-softfloat, mips-softfloat}`
- Build with `-ldflags="-s -w" -trimpath`, `CGO_ENABLED=0`
- Run UPX if available (skip mips-bigendian where UPX has issues)
- Upload to release as `podkop_updater-${arch}`
- `scripts/install.sh` downloads from `releases/latest/download/podkop_updater-${arch}`

## Testing strategy

- Unit: `transport_test.go` (mock dialer, verify tier cascade), `version_test.go` (semver edge cases)
- Integration: `httptest.Server` mocking Telegram API + GitHub API
- Manual: deploy to qemu-mipsel + real router (post-MVP)

## Implementation phases

| Phase | Scope | Output |
|-------|-------|--------|
| **0** | Scaffolding (this commit) | DESIGN.md, folder layout, go.mod, stub main.go, Makefile, init.d stub |
| **1** | Core daemon happy-path | UCI load, GitHub fetch, version compare, send menu, handle callbacks. Direct transport only. ~400 lines |
| **2** | Tiered transport | SOCKS detection, custom RoundTripper, emergency IPs, sticky tier. ~150 lines + tests |
| **3** | Update flow | `do_update` (run install.sh), `do_dns_check` async, `do_restart_podkop`. ~150 lines |
| **4** | Self-update | atomic swap + .bak. ~100 lines |
| **5** | CI + install.sh | release.yml matrix, arch-detect install. |
| **6** | Hardware test | mipsle + aarch64 real routers |

Estimated 3–5 days for phases 1–5. Phase 6 dependent on hardware availability.

## Open questions

- Use `slog` (Go 1.21+) — confirmed Go 1.25 available on dev box, OK
- mips-bigendian: include in CI or skip? (lower-priority arch; defer)
- Binary location on router: keep `/usr/bin/podkop_updater` (matches current install)
- Migration path from bash version: install.sh detects existing `.sh` at `/usr/bin/podkop_updater.sh`, stops daemon, removes shell version, drops binary. UCI config preserved.
