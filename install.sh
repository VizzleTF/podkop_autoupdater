#!/bin/ash

# Installer script for podkop_updater.sh
# Downloads, configures, and optionally schedules podkop_updater.sh on OpenWrt

# URLs and paths
UPDATER_URL="https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/podkop_updater.sh"
UPDATER_PATH="/usr/bin/podkop_updater.sh"
LOG_FILE="/tmp/podkop_update.log"

# Step 1: Check if running on OpenWrt
if ! grep -q "OpenWrt" /etc/os-release; then
  echo "Error: This script is designed for OpenWrt. Exiting."
  exit 1
fi

# Step 2: Install dependencies
echo "Installing required packages (curl, jq, wget)..."
opkg update > /dev/null 2>&1
for pkg in curl jq wget; do
  if ! opkg list-installed | grep -q "^$pkg "; then
    opkg install $pkg > /dev/null 2>&1
    if [ $? -ne 0 ]; then
      echo "Error: Failed to install $pkg. Please check your internet connection and opkg repositories."
      exit 1
    fi
  fi
done
echo "Dependencies installed."

# Step 3: Download podkop_updater.sh
echo "Downloading podkop_updater.sh from $UPDATER_URL..."
wget -O $UPDATER_PATH $UPDATER_URL > /dev/null 2>&1
if [ $? -ne 0 ]; then
  echo "Error: Failed to download podkop_updater.sh. Please check your internet connection."
  exit 1
fi
chmod +x $UPDATER_PATH
echo "Downloaded and set executable: $UPDATER_PATH"

# Step 4: Prompt for update mode
echo "Choose update mode:"
echo "1) Manual (run via console, no cron)"
echo "2) Automatic (run via cron, no Telegram confirmation)"
echo "3) Automatic with Telegram bot confirmation (default, run via cron)"
echo "Enter 1, 2, or 3 (default: 3):"
read -r UPDATE_MODE
case "$UPDATE_MODE" in
  1)
    echo "Manual mode selected. Script installed without cron or Telegram configuration."
    ;;
  2)
    echo "Automatic mode selected."
    # Prompt for cron frequency
    echo "How often should the script run (in hours, e.g., 1 for hourly, 6 for every 6 hours)?"
    read -r CRON_HOURS
    if ! echo "$CRON_HOURS" | grep -q '^[0-9]\+$' || [ "$CRON_HOURS" -lt 1 ]; then
      echo "Error: Invalid input. Using default of 1 hour."
      CRON_HOURS=1
    fi
    # Configure cron job
    echo "Setting up cron job to run every $CRON_HOURS hour(s)..."
    CRON_FILE="/etc/crontabs/root"
    if [ ! -f "$CRON_FILE" ]; then
      touch "$CRON_FILE"
    fi
    sed -i "/podkop_updater.sh/d" "$CRON_FILE"
    echo "0 */$CRON_HOURS * * * $UPDATER_PATH --force" >> "$CRON_FILE"
    /etc/init.d/cron restart > /dev/null 2>&1
    echo "Cron job configured for automatic updates."
    ;;
  3|*)
    echo "Automatic with Telegram bot confirmation selected."
    # Prompt for Telegram bot token
    echo "Please enter your Telegram bot token (obtained from @BotFather):"
    read -r BOT_TOKEN
    if [ -z "$BOT_TOKEN" ]; then
      echo "Error: Bot token cannot be empty. Exiting."
      exit 1
    fi
    # Prompt for Telegram chat ID
    echo "Please enter your Telegram chat ID (obtained from @get_id_bot or similar):"
    read -r CHAT_ID
    if [ -z "$CHAT_ID" ]; then
      echo "Error: Chat ID cannot be empty. Exiting."
      exit 1
    fi
    # Configure podkop_updater.sh
    echo "Configuring podkop_updater.sh with bot token and chat ID..."
    sed -i "s|BOT_TOKEN=\"your_bot_token\"|BOT_TOKEN=\"$BOT_TOKEN\"|" $UPDATER_PATH
    sed -i "s|CHAT_ID=\"your_chat_id\"|CHAT_ID=\"$CHAT_ID\"|" $UPDATER_PATH
    if ! grep -q "BOT_TOKEN=\"$BOT_TOKEN\"" $UPDATER_PATH || ! grep -q "CHAT_ID=\"$CHAT_ID\"" $UPDATER_PATH; then
      echo "Error: Failed to configure podkop_updater.sh. Please check the script file."
      exit 1
    fi
    # Prompt for cron frequency
    echo "How often should the script run (in hours, e.g., 1 for hourly, 6 for every 6 hours)?"
    read -r CRON_HOURS
    if ! echo "$CRON_HOURS" | grep -q '^[0-9]\+$' || [ "$CRON_HOURS" -lt 1 ]; then
      echo "Error: Invalid input. Using default of 1 hour."
      CRON_HOURS=1
    fi
    # Configure cron job
    echo "Setting up cron job to run every $CRON_HOURS hour(s)..."
    CRON_FILE="/etc/crontabs/root"
    if [ ! -f "$CRON_FILE" ]; then
      touch "$CRON_FILE"
    fi
    sed -i "/podkop_updater.sh/d" "$CRON_FILE"
    echo "0 */$CRON_HOURS * * * $UPDATER_PATH" >> "$CRON_FILE"
    /etc/init.d/cron restart > /dev/null 2>&1
    echo "Cron job configured for Telegram-confirmed updates."
    ;;
esac

# Step 5: Verify network access
echo "Verifying network access to GitHub and Telegram APIs..."
if ! curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest > /dev/null; then
  echo "Warning: Cannot reach GitHub API. The script may fail to check for updates."
fi
if [ "$UPDATE_MODE" = "3" ] || [ -z "$UPDATE_MODE" ]; then
  if ! curl -s "https://api.telegram.org/bot$BOT_TOKEN/getMe" | grep -q '"ok":true'; then
    echo "Warning: Cannot reach Telegram API or invalid bot token. The script may fail to send notifications."
  fi
fi

# Step 6: Final instructions
echo "Installation complete!"
echo "The script is installed at $UPDATER_PATH."
if [ "$UPDATE_MODE" = "1" ]; then
  echo "Run manually with: $UPDATER_PATH"
elif [ "$UPDATE_MODE" = "2" ]; then
  echo "Automatic updates are scheduled every $CRON_HOURS hour(s)."
else
  echo "Telegram-confirmed updates are scheduled every $CRON_HOURS hour(s)."
  echo "Reply 'yes' to Telegram messages to update or 'no' to cancel."
fi
echo "Logs will be written to $LOG_FILE."
echo "To test the script, run: $UPDATER_PATH"