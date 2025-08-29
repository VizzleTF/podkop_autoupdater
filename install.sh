#!/bin/ash

# Installer script for podkop_updater.sh
# Downloads, configures, and optionally schedules podkop_updater.sh on OpenWrt

# Constants
UPDATER_URL="https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/podkop_updater.sh"  # URL to download the updater script
UPDATER_PATH="/usr/bin/podkop_updater.sh"  # Installation path for the updater script
LOG_FILE="/tmp/podkop_update.log"  # Log file for update operations
DEFAULT_BOT_TOKEN="your_bot_token"  # Default placeholder for Telegram bot token
DEFAULT_CHAT_ID="your_chat_id"  # Default placeholder for Telegram chat ID
DEFAULT_CRON_HOURS=1  # Default cron interval in hours
TOKEN_DISPLAY_LENGTH=10  # Number of characters to show when displaying bot token
TEMP_UPDATER_PATH="/tmp/podkop_updater_new.sh"  # Temporary path for downloaded updater script
CRON_FILE="/etc/crontabs/root"  # Path to root crontab file
OS_RELEASE_FILE="/etc/os-release"  # System OS release information file
GITHUB_API_URL="https://api.github.com/repos/itdoginfo/podkop/releases/latest"  # GitHub API URL for latest podkop release
TELEGRAM_API_BASE="https://api.telegram.org/bot"  # Base URL for Telegram Bot API
CRON_SERVICE="/etc/init.d/cron"  # Cron service control script

# Functions
exit_with_error() {
  local message="$1"
  echo "Error: $message"
  exit 1
}

download_file() {
  local url="$1"
  local output_path="$2"
  wget -O "$output_path" "$url" > /dev/null 2>&1
  if [ $? -ne 0 ]; then
    exit_with_error "Failed to download from $url. Please check your internet connection."
  fi
}

setup_cron_job() {
  local cron_hours="$1"
  local updater_command="$2"
  echo "Setting up cron job to run every $cron_hours hour(s)..."
  if [ ! -f "$CRON_FILE" ]; then
    touch "$CRON_FILE"
  fi
  sed -i "/podkop_updater.sh/d" "$CRON_FILE"
  echo "0 */$cron_hours * * * $updater_command" >> "$CRON_FILE"
  $CRON_SERVICE restart > /dev/null 2>&1
}

validate_cron_hours() {
  local hours="$1"
  if ! echo "$hours" | grep -q '^[0-9]\+$' || [ "$hours" -lt 1 ]; then
    echo "Error: Invalid input. Using default of $DEFAULT_CRON_HOURS hour."
    echo $DEFAULT_CRON_HOURS
  else
    echo "$hours"
  fi
}

# Step 1: Check if running on OpenWrt
if ! grep -q -e "OpenWrt" -e "immortalwrt" $OS_RELEASE_FILE; then
  exit_with_error "This script is designed for OpenWrt or ImmortalWrt. Exiting."
fi

# Step 2: Install dependencies
echo "Installing required packages (curl, jq, wget)..."
opkg update > /dev/null 2>&1
for pkg in curl jq wget; do
  if ! opkg list-installed | grep -q "^$pkg "; then
    opkg install $pkg > /dev/null 2>&1
    if [ $? -ne 0 ]; then
      exit_with_error "Failed to install $pkg. Please check your internet connection and opkg repositories."
    fi
  fi
done
echo "Dependencies installed."

# Step 3: Check if podkop_updater.sh already exists and is configured
if [ -f "$UPDATER_PATH" ]; then
  echo "Found existing podkop_updater.sh at $UPDATER_PATH"
  
  # Check if it's already configured (not default values)
  EXISTING_BOT_TOKEN=$(grep '^BOT_TOKEN=' $UPDATER_PATH | cut -d'"' -f2)
  EXISTING_CHAT_ID=$(grep '^CHAT_ID=' $UPDATER_PATH | cut -d'"' -f2)
  
  if [ "$EXISTING_BOT_TOKEN" != "$DEFAULT_BOT_TOKEN" ] && [ "$EXISTING_CHAT_ID" != "$DEFAULT_CHAT_ID" ]; then
    echo "Script is already configured with:"
    echo "  Bot Token: ${EXISTING_BOT_TOKEN:0:$TOKEN_DISPLAY_LENGTH}..."
    echo "  Chat ID: $EXISTING_CHAT_ID"
    echo ""
    echo "Choose an option:"
    echo "1) Keep existing configuration and exit"
    echo "2) Reconfigure with new settings"
    echo "3) Update script but keep existing configuration"
    echo "Enter 1, 2, or 3 (default: 1):"
    read -r EXISTING_CONFIG_CHOICE
    
    case "$EXISTING_CONFIG_CHOICE" in
      2)
        echo "Reconfiguring with new settings..."
        ;;
      3)
        echo "Updating script while preserving configuration..."
        # Download new version but preserve config
        download_file $UPDATER_URL $TEMP_UPDATER_PATH
        # Replace config in new version with existing values
        sed -i "s|BOT_TOKEN=\"$DEFAULT_BOT_TOKEN\"|BOT_TOKEN=\"$EXISTING_BOT_TOKEN\"|" $TEMP_UPDATER_PATH
        sed -i "s|CHAT_ID=\"$DEFAULT_CHAT_ID\"|CHAT_ID=\"$EXISTING_CHAT_ID\"|" $TEMP_UPDATER_PATH
        mv $TEMP_UPDATER_PATH $UPDATER_PATH
        chmod +x $UPDATER_PATH
        echo "Script updated with preserved configuration."
        echo "Installation complete! The script is ready to use."
        exit 0
        ;;
      1|*)
        echo "Keeping existing configuration. Installation complete!"
        exit 0
        ;;
    esac
  fi
fi

# Download podkop_updater.sh
echo "Downloading podkop_updater.sh from $UPDATER_URL..."
download_file $UPDATER_URL $UPDATER_PATH
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
    CRON_HOURS=$(validate_cron_hours "$CRON_HOURS")
    # Configure cron job
    setup_cron_job "$CRON_HOURS" "$UPDATER_PATH --force"
    echo "Cron job configured for automatic updates."
    ;;
  3|*)
    echo "Automatic with Telegram bot confirmation selected."
    # Prompt for Telegram bot token
    echo "Please enter your Telegram bot token (obtained from @BotFather):"
    read -r BOT_TOKEN
    if [ -z "$BOT_TOKEN" ]; then
      exit_with_error "Bot token cannot be empty. Exiting."
    fi
    # Prompt for Telegram chat ID
    echo "Please enter your Telegram chat ID (obtained from @get_id_bot or similar):"
    read -r CHAT_ID
    if [ -z "$CHAT_ID" ]; then
      exit_with_error "Chat ID cannot be empty. Exiting."
    fi
    # Configure podkop_updater.sh
    echo "Configuring podkop_updater.sh with bot token and chat ID..."
    sed -i "s|BOT_TOKEN=\"$DEFAULT_BOT_TOKEN\"|BOT_TOKEN=\"$BOT_TOKEN\"|" $UPDATER_PATH
    sed -i "s|CHAT_ID=\"$DEFAULT_CHAT_ID\"|CHAT_ID=\"$CHAT_ID\"|" $UPDATER_PATH
    if ! grep -q "BOT_TOKEN=\"$BOT_TOKEN\"" $UPDATER_PATH || ! grep -q "CHAT_ID=\"$CHAT_ID\"" $UPDATER_PATH; then
      exit_with_error "Failed to configure podkop_updater.sh. Please check the script file."
    fi
    # Prompt for cron frequency
    echo "How often should the script run (in hours, e.g., 1 for hourly, 6 for every 6 hours)?"
    read -r CRON_HOURS
    CRON_HOURS=$(validate_cron_hours "$CRON_HOURS")
    # Configure cron job
    setup_cron_job "$CRON_HOURS" "$UPDATER_PATH"
    echo "Cron job configured for Telegram-confirmed updates."
    ;;
esac

# Step 5: Verify network access
echo "Verifying network access to GitHub and Telegram APIs..."
if ! curl -s $GITHUB_API_URL > /dev/null; then
  echo "Warning: Cannot reach GitHub API. The script may fail to check for updates."
fi
if [ "$UPDATE_MODE" = "3" ] || [ -z "$UPDATE_MODE" ]; then
  if ! curl -s "${TELEGRAM_API_BASE}$BOT_TOKEN/getMe" | grep -q '"ok":true'; then
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