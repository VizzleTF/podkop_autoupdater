#!/bin/ash

# Installer script for podkop_updater.sh
# Downloads, configures, and schedules podkop_updater.sh on OpenWrt

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

# Step 4: Prompt for Telegram bot token
echo "Please enter your Telegram bot token (obtained from @BotFather):"
read -r BOT_TOKEN
if [ -z "$BOT_TOKEN" ]; then
  echo "Error: Bot token cannot be empty. Exiting."
  exit 1
fi

# Step 5: Prompt for Telegram chat ID
echo "Please enter your Telegram chat ID (obtained from @get_id_bot or similar):"
read -r CHAT_ID
if [ -z "$CHAT_ID" ]; then
  echo "Error: Chat ID cannot be empty. Exiting."
  exit 1
fi

# Step 6: Configure podkop_updater.sh
echo "Configuring podkop_updater.sh with bot token and chat ID..."
sed -i "s|BOT_TOKEN=\"your_bot_token\"|BOT_TOKEN=\"$BOT_TOKEN\"|" $UPDATER_PATH
sed -i "s|CHAT_ID=\"your_chat_id\"|CHAT_ID=\"$CHAT_ID\"|" $UPDATER_PATH
if ! grep -q "BOT_TOKEN=\"$BOT_TOKEN\"" $UPDATER_PATH || ! grep -q "CHAT_ID=\"$CHAT_ID\"" $UPDATER_PATH; then
  echo "Error: Failed to configure podkop_updater.sh. Please check the script file."
  exit 1
fi
echo "Configuration complete."

# Step 7: Prompt for cron frequency
echo "How often should the script run (in hours, e.g., 1 for hourly, 6 for every 6 hours)?"
read -r CRON_HOURS
if ! echo "$CRON_HOURS" | grep -q '^[0-9]\+$' || [ "$CRON_HOURS" -lt 1 ]; then
  echo "Error: Invalid input. Using default of 1 hour."
  CRON_HOURS=1
fi

# Step 8: Configure cron job
echo "Setting up cron job to run every $CRON_HOURS hour(s)..."
CRON_FILE="/etc/crontabs/root"
if [ ! -f "$CRON_FILE" ]; then
  touch "$CRON_FILE"
fi
# Remove existing podkop_updater.sh cron jobs
sed -i "/podkop_updater.sh/d" "$CRON_FILE"
# Add new cron job
echo "0 */$CRON_HOURS * * * $UPDATER_PATH" >> "$CRON_FILE"
# Restart cron service
/etc/init.d/cron restart > /dev/null 2>&1
echo "Cron job configured."

# Step 9: Verify network access
echo "Verifying network access to GitHub and Telegram APIs..."
if ! curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest > /dev/null; then
  echo "Warning: Cannot reach GitHub API. The script may fail to check for updates."
fi
if ! curl -s "https://api.telegram.org/bot$BOT_TOKEN/getMe" | grep -q '"ok":true'; then
  echo "Warning: Cannot reach Telegram API or invalid bot token. The script may fail to send notifications."
fi

# Step 10: Final instructions
echo "Installation complete!"
echo "The script is installed at $UPDATER_PATH and configured to run every $CRON_HOURS hour(s)."
echo "Logs will be written to $LOG_FILE."
echo "To test the script, run: $UPDATER_PATH"
echo "If a new version is available, reply 'yes' to the Telegram message to update."