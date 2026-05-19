#!/bin/sh
# Installer for podkop_updater (Go daemon) on OpenWrt / ImmortalWrt.
#
#   sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
#
# Detects arch, downloads latest release binary, sets up procd init.d service,
# and migrates an existing bash-based podkop_updater installation.
set -e

REPO="VizzleTF/podkop_autoupdater"
BIN_DEST="/usr/bin/podkop_updater"
INITD_DEST="/etc/init.d/podkop_updater"
UCI_PKG="podkop_updater"
UCI_SEC="settings"
LEGACY_BASH="/usr/bin/podkop_updater.sh"

step() { echo "==> $*"; }
fail() { echo "Error: $*" >&2; exit 1; }

# --- 1. OS check ---
grep -qE '(OpenWrt|immortalwrt)' /etc/os-release || \
    fail "Not OpenWrt/ImmortalWrt"
step "OpenWrt detected: $(grep '^PRETTY_NAME' /etc/os-release | cut -d= -f2-)"

# --- 2. Arch detection ---
case "$(uname -m)" in
    x86_64)    ARCH=amd64  ;;
    aarch64)   ARCH=arm64  ;;
    armv7l)    ARCH=armv7  ;;
    mips)      ARCH=mips   ;;
    mipsel)    ARCH=mipsle ;;
    *) fail "Unsupported arch: $(uname -m)" ;;
esac
step "Architecture: $ARCH"

# --- 3. Stop existing daemon (bash or Go) ---
if [ -f "$INITD_DEST" ]; then
    step "Stopping existing service"
    "$INITD_DEST" stop 2>/dev/null || true
    "$INITD_DEST" disable 2>/dev/null || true
fi
pkill -9 -f "podkop_updater(\\.sh)? --daemon" 2>/dev/null || true
sleep 1
rm -rf /tmp/podkop_updater.lock /tmp/podkop_updater.pid 2>/dev/null || true

# --- 4. Migrate from pre-Go bash version, if present ---
# Until the bash version has fully aged out we still want to clean up
# its file when an existing user re-runs the installer.
if [ -f "$LEGACY_BASH" ]; then
    step "Removing previous bash version at $LEGACY_BASH"
    rm -f "$LEGACY_BASH"
fi

# --- 5. Download binary ---
URL="https://github.com/${REPO}/releases/latest/download/podkop_updater-${ARCH}"
step "Downloading $URL"
TMP="${BIN_DEST}.new"
if curl -fL "$URL" -o "$TMP"; then
    :
elif command -v wget >/dev/null 2>&1 && wget -O "$TMP" "$URL"; then
    :
else
    fail "Download failed. Check that release asset exists."
fi
[ -s "$TMP" ] || fail "Downloaded binary is empty."
chmod +x "$TMP"
mv "$TMP" "$BIN_DEST"
SIZE=$(ls -l "$BIN_DEST" | awk '{print $5}')
step "Installed $BIN_DEST ($SIZE bytes)"

# --- 6. Configure UCI ---
EXISTING_TOKEN=$(uci -q get "${UCI_PKG}.${UCI_SEC}.bot_token" 2>/dev/null || true)
EXISTING_CHAT=$(uci -q get "${UCI_PKG}.${UCI_SEC}.chat_id" 2>/dev/null || true)
if [ -n "$EXISTING_TOKEN" ] && [ -n "$EXISTING_CHAT" ]; then
    # Bot ID (digits before colon) is public; the secret after the colon
    # must never be printed.
    BOT_ID="${EXISTING_TOKEN%%:*}"
    step "UCI already configured (bot_id=${BOT_ID}, chat_id=$EXISTING_CHAT)"
else
    echo "Telegram bot token (from @BotFather):"
    read -r TOKEN
    [ -z "$TOKEN" ] && fail "Empty token"
    echo "Telegram chat ID (from @get_id_bot):"
    read -r CHAT
    [ -z "$CHAT" ] && fail "Empty chat id"
    echo "Auto-check interval in hours (default 6):"
    read -r INTERVAL
    [ -z "$INTERVAL" ] && INTERVAL=6
    echo "Router label shown in chat (e.g. 'Home', 'Dacha'; leave empty to use hostname):"
    read -r ROUTER_LABEL

    [ -f "/etc/config/${UCI_PKG}" ] || touch "/etc/config/${UCI_PKG}"
    uci -q delete "${UCI_PKG}.${UCI_SEC}" 2>/dev/null || true
    uci set "${UCI_PKG}.${UCI_SEC}=${UCI_SEC}"
    uci set "${UCI_PKG}.${UCI_SEC}.bot_token=${TOKEN}"
    uci set "${UCI_PKG}.${UCI_SEC}.chat_id=${CHAT}"
    uci set "${UCI_PKG}.${UCI_SEC}.check_interval=${INTERVAL}"
    [ -n "$ROUTER_LABEL" ] && uci set "${UCI_PKG}.${UCI_SEC}.router_label=${ROUTER_LABEL}"
    uci commit "${UCI_PKG}"
    step "UCI saved to /etc/config/${UCI_PKG}"
fi

# --- 7. Init.d service ---
step "Installing procd init.d service"
cat > "$INITD_DEST" <<'INITD_EOF'
#!/bin/sh /etc/rc.common
# procd init.d service for podkop_updater (Go daemon).

START=99
STOP=10
USE_PROCD=1

PROG=/usr/bin/podkop_updater

start_service() {
    procd_open_instance
    procd_set_param command "$PROG" --daemon
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 0
    procd_set_param stderr 0
    procd_close_instance
}
INITD_EOF
chmod +x "$INITD_DEST"
"$INITD_DEST" enable
"$INITD_DEST" start

# --- 8. Status check ---
sleep 2
if pgrep -f "$BIN_DEST --daemon" >/dev/null; then
    step "Service running. Logs: /tmp/podkop_update.log"
    echo
    echo "Done. Open the Telegram chat with your bot and send /menu."
else
    fail "Service failed to start. Check /tmp/podkop_update.log"
fi
