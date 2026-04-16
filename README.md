[Русская версия](./README_ru.md)

# Podkop Updater for OpenWrt

Automatic update checker for [podkop](https://github.com/itdoginfo/podkop) on OpenWrt/ImmortalWrt routers with Telegram bot control.

## Features
- Persistent Telegram bot with inline menu (daemon mode, default)
- Two buttons: "Check version" and "Restart podkop", always available
- Menu switches to "Update" / "Cancel" when a new version is detected
- Automatic periodic version check (configurable interval)
- 3-tier transport fallback: Podkop SOCKS5 proxy → Direct → Emergency Telegram IPs
- Post-update/restart DNS health check
- procd init.d service with auto-restart on crash
- Legacy modes: cron with Telegram confirmation, cron with auto-update, manual

## Requirements
- OpenWrt or ImmortalWrt router
- Packages: `curl`, `jq`, `wget`, `nslookup` (installed automatically)
- Telegram bot: token from [@BotFather](https://t.me/BotFather), chat ID from [@getmyid_bot](https://t.me/getmyid_bot)

## Installation
```sh
sh <(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)
```

The installer will guide you through mode selection and configuration.

### Update modes
| Mode | Description |
|------|-------------|
| 1 | Manual — run via console, no automation |
| 2 | Automatic — cron, no Telegram |
| 3 | Cron + Telegram confirmation |
| **4 (default)** | **Daemon with persistent Telegram menu** |

## Usage

| Command | Description |
|---------|-------------|
| `podkop_updater.sh --daemon` | Run as persistent Telegram bot (used by init.d) |
| `podkop_updater.sh` | One-shot update check (Telegram confirmation) |
| `podkop_updater.sh --force` | Auto-update without confirmation |
| `podkop_updater.sh --dry-run` | Test full flow without making changes |

### Service management (daemon mode)
```sh
/etc/init.d/podkop_updater start
/etc/init.d/podkop_updater stop
/etc/init.d/podkop_updater restart
```

## Configuration

Credentials are stored in UCI (`/etc/config/podkop_updater`):
```sh
uci set podkop_updater.settings.bot_token="YOUR_TOKEN"
uci set podkop_updater.settings.chat_id="YOUR_CHAT_ID"
uci set podkop_updater.settings.check_interval=6  # hours, daemon mode only
uci commit podkop_updater
```

## Troubleshooting

Check logs: `cat /tmp/podkop_update.log`

Common issues:
- **No Telegram message**: Verify bot token and chat ID, check network access to `api.telegram.org`
- **DNS check fails**: Normal if podkop service isn't running yet
- **Daemon not starting**: Check `/etc/init.d/podkop_updater status`, review logs

## License
[MIT](https://opensource.org/licenses/MIT)
