[Русская версия](./README_ru.md)

<a href="https://www.buymeacoffee.com/vizzletf" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me a Coffee" height="60" width="217"></a>

# podkop_updater

Single static Go binary (procd service) that watches
[podkop](https://github.com/itdoginfo/podkop) releases and drives
update / rollback / restart / config-backup from a Telegram dashboard.

## Features
- Dashboard menu (status card + buttons) and slash commands: `/menu`,
  `/check_podkop`, `/check_self`, `/check_dns`, `/restart`, `/status`, `/log`
- Periodic check (default 6h) for podkop and the updater; optional auto-update for each
- Rollback podkop to any release ≥ 0.7.0; auto-rollback offered if an update breaks DNS
- Config backup / restore / delete (versioned, timestamped) with retention;
  a downgrade restores the matching config
- Settings menu (⚙️), applied live: auto-update toggles, check interval,
  retention, label, admins, emergency-IP refresh, show config
- Access control by Telegram user ID; double-click guard on root actions
- `install.sh` pinned to the release tag; self-update verified against a published `.sha256`
- Signed OpenWrt packages for both release lines, built with [owfeed](https://github.com/VizzleTF/owfeed)
- Tiered transport: podkop SOCKS5 → direct → emergency Telegram IPs (DoH-refreshed, sticky)
- DNS health check after every restart / update / rollback

## Install

### From the community feed (recommended)

Releases ship signed OpenWrt packages, so `apk`/`opkg` verifies what it installs and
`apk upgrade` keeps it current. Add the feed once —
[VizzleTF/owfeed-packages](https://github.com/VizzleTF/owfeed-packages) has the snippet for
your release line — then:

```sh
apk add podkop-updater      # 25.12 and later
opkg install podkop-updater # 24.10 and earlier
```

The package installs the binary and the procd service and stops there: the daemon needs a bot
token ([@BotFather](https://t.me/BotFather)) and a chat id ([@get_id_bot](https://t.me/get_id_bot)),
which only you can supply, and it prints the three commands that finish the job.

### From the installer script

```sh
sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
```
Detects the arch, installs the procd service, prompts for a bot token
([@BotFather](https://t.me/BotFather)) and chat id ([@get_id_bot](https://t.me/get_id_bot)).
If podkop ships its own `/usr/bin/podkop_bot`, disable it first. Two daemons
on one token steal each other's updates.

## Releases

Every release ships signed OpenWrt packages for both release lines alongside the bare
binaries. [RELEASING.md](RELEASING.md) has how to cut one, and what the signing key
`37ddece4c0eef357` does and does not vouch for.

## Settings (UCI `/etc/config/podkop_updater`)

| key | meaning |
|-----|---------|
| `bot_token` | Telegram bot token (required) |
| `chat_id` | Telegram chat id (required) |
| `check_interval` | hours between checks (default 6) |
| `router_label` | bold name in message header; empty = hostname |
| `admin_ids` | space-separated allowed user IDs; empty = anyone in chat |
| `auto_update` | `1` = auto-install podkop releases |
| `auto_update_self` | `1` = auto-install updater releases (sha256-verified) |
| `backup_keep` | config backups to keep (0 = unlimited) |

Everything except `bot_token`/`chat_id` is editable live from the ⚙️ menu
(written back to UCI). The daemon also manages `emergency_ips` and `menu_mid`.

## License
[MIT](https://opensource.org/licenses/MIT)
