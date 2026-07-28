#!/bin/sh
# The daemon needs a Telegram bot token and a chat id, and neither can come from a
# package: they are the user's, and asking for them is what install.sh does
# interactively. So an install from a feed leaves the service present, not running,
# and says what the remaining step is -- rather than enabling a service that would
# respawn every five seconds against an empty configuration.
[ -n "${IPKG_INSTROOT}" ] && exit 0

if uci -q get podkop_updater.main.bot_token >/dev/null 2>&1; then
	/etc/init.d/podkop_updater enable
	/etc/init.d/podkop_updater restart
	exit 0
fi

cat <<'EOF'

podkop-updater is installed and not yet running: it needs a Telegram bot token and a
chat id, which only you can supply.

  uci set podkop_updater.main=main
  uci set podkop_updater.main.bot_token='<token from @BotFather>'
  uci set podkop_updater.main.chat_id='<id from @get_id_bot>'
  uci set podkop_updater.main.check_interval='6'
  uci commit podkop_updater
  /etc/init.d/podkop_updater enable
  /etc/init.d/podkop_updater start

EOF
