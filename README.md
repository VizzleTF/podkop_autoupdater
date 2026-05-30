[Русская версия](./README_ru.md)

# podkop_updater

Telegram bot for OpenWrt/ImmortalWrt routers that watches
[podkop](https://github.com/itdoginfo/podkop) for new releases and lets you
trigger updates and restarts from chat. Implemented as a single static Go
binary managed by procd.

## Features
- **Dashboard menu**: a single tracked message shows a status card (podkop &
  updater versions, DNS, active transport tier, last check) with buttons to
  refresh, restart podkop, and open the Backups / Settings submenus.
  Contextual "⬆️ Обновить" rows appear when an update is available.
- Slash commands also work: `/menu`, `/check_podkop`, `/check_self`,
  `/check_dns`, `/restart`, `/status`, `/log`
- **Periodic version check** (default every 6h) for **both** podkop and the
  updater; a new release delivers a fresh notification.
- **Version rollback / downgrade**: roll podkop back to any supported release
  (0.7.0 or newer), installed straight from that release's package assets.
  Versions with a saved config backup are listed first. If an update leaves
  DNS broken, the bot also offers a one-tap rollback right there.
- **Config backup / restore** (📦 Бэкапы): versioned, timestamped snapshots of
  `/etc/config/podkop`. You can create one, restore by version then timestamp,
  or delete old ones, and the daemon prunes them to a retention limit. A
  downgrade restores the target version's config automatically.
- **Settings menu** (⚙️ Настройки), applied live without a restart. Toggle
  podkop and updater auto-update separately, change the check interval, set
  the backup retention limit, force an emergency-IP refresh, edit the router
  label or admin list, or dump the current config with the token masked.
- **Access control**: restrict commands to specific Telegram user IDs;
  a **concurrency guard** prevents a double-click from launching two
  `install.sh` runs as root at once.
- **Supply-chain hardening**: podkop's `install.sh` is fetched pinned to
  the detected release tag (not branch HEAD); self-update verifies the
  binary against a published `.sha256`.
- **Tiered HTTP transport**: Podkop SOCKS5 → direct → emergency Telegram IPs,
  with a sticky last-known-good tier that periodically resets to prefer the
  primary path again. Emergency IPs refreshed daily via DoH (concurrent
  queries) and persisted in UCI so they survive reboots.
- **Atomic self-update** with `.bak` rollback; procd respawns into the new
  binary. A DNS health check after every restart/update/rollback polls
  `fakeip.podkop.fyi`; a failure to recover is surfaced prominently.

## Requirements
- OpenWrt or ImmortalWrt router (supported archs: amd64, arm64, armv7,
  mipsle softfloat, mips softfloat)
- Telegram bot token (from [@BotFather](https://t.me/BotFather)) and chat
  ID (from [@get_id_bot](https://t.me/get_id_bot))

## Installation

```sh
sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
```

The installer detects the architecture, stops any previous version, downloads
the matching binary from the latest GitHub release, configures the procd
service, and prompts for the bot token + chat ID if they aren't already in
UCI.

If the upstream podkop package ships its own `/usr/bin/podkop_bot`, stop and
disable it first — both daemons polling the same bot token will steal
updates from each other.

## Configuration

Settings live in UCI (`/etc/config/podkop_updater`):

```sh
uci set podkop_updater.settings.bot_token="YOUR_TOKEN"
uci set podkop_updater.settings.chat_id="YOUR_CHAT_ID"
uci set podkop_updater.settings.check_interval=6   # hours
uci set podkop_updater.settings.router_label="Home"  # optional: shown in message header
uci set podkop_updater.settings.admin_ids="123456789 987654321"  # optional: allowed user IDs
uci set podkop_updater.settings.auto_update=1        # optional: auto-install podkop releases
uci set podkop_updater.settings.auto_update_self=1   # optional: auto-install updater releases
uci set podkop_updater.settings.backup_keep=10       # optional: keep N config backups (0 = unlimited)
uci commit podkop_updater
```

Most of these are also editable at runtime from the **⚙️ Настройки** menu in
chat (changes are written back to UCI and applied live). `bot_token` and
`chat_id` are set only here or via the installer.

| key | meaning |
|-----|---------|
| `router_label` | Name shown bold in every message header; disambiguates multiple routers in one chat. Empty = system hostname. |
| `admin_ids` | Space-separated allowlist of Telegram user IDs. When set, only they may issue commands (gated by `From.ID`); others get "access denied". Empty = anyone in the chat. |
| `auto_update` | `1` = periodic check auto-installs new **podkop** releases instead of only notifying. |
| `auto_update_self` | `1` = periodic check auto-installs new **updater** releases too (sha256-verified). Off by default so a bad self-release can't silently brick the bot. |
| `backup_keep` | How many config backups to retain; oldest pruned first. `0` = unlimited. |

The daemon also writes the discovered emergency IP list to
`emergency_ips` and tracks its menu message id in `menu_mid`. Config backups
are stored next to the live config as `/etc/config/podkop.bak-<version>-<timestamp>`
(the dotted name keeps UCI from loading them).

## Service

```sh
/etc/init.d/podkop_updater start
/etc/init.d/podkop_updater stop
/etc/init.d/podkop_updater restart
```

Logs: `/tmp/podkop_update.log` (rotates in place at ~200 lines).

## Build from source

```sh
cd go
make build           # current host
make build-all       # cross-compile for the five OpenWrt-relevant archs
make upx             # UPX-compress (requires upx installed)
```

See [`go/DESIGN.md`](./go/DESIGN.md) for the architectural notes.

## License
[MIT](https://opensource.org/licenses/MIT)
