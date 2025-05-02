# Podkop Updater for OpenWrt

This script (`podkop_updater.sh`) automates checking for updates to the `podkop` package on an OpenWrt router. It compares the installed version with the latest release on GitHub, sends a Telegram notification if an update is available, and $\(\text{update}\), and applies the update upon user confirmation via Telegram. The script handles interactive prompts in the `podkop` update script automatically.

## Features
- Checks the latest `podkop` version via GitHub API.
- Compares with the installed version using `opkg`.
- Sends a Telegram message asking to update (requires a "yes" or "no" reply).
- Automates the update process, answering two prompts: upgrade `podkop` and install Russian translation.
- Logs all actions to `/tmp/podkop_update.log` for debugging.
- Supports case-insensitive Telegram responses ("yes", "Yes", "YES", etc.).

## Requirements
- **OpenWrt router** with internet access.
- **Installed packages**:
  - `curl`: For API requests (usually pre-installed).
  - `jq`: For JSON parsing (dependency of `podkop`).
  - `wget`: For downloading the update script.
- **Telegram bot**:
  - Create a bot via [@BotFather](https://t.me/BotFather) and obtain a token.
  - Get your chat ID via [@get_id_bot](https://t.me/get_id_bot) or similar.
- **Network access** to:
  - GitHub API (`api.github.com`).
  - Telegram API (`api.telegram.org`).
  - OpenWrt package repositories and `podkop` update script URL.

## Installation
Run the installer script with a single command:
```sh
sh <(wget -O - https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/install.sh)
```
The installer will:
- Download `podkop_updater.sh` to `/usr/bin/`.
- Install required packages (`curl`, `jq`, `wget`).
- Prompt for Telegram bot token, chat ID, and cron frequency (in hours).
- Configure the script and set up a cron job.
- Verify network access to GitHub and Telegram APIs.

## Manual Installation
1. **Save the Script**:
   ```sh
   wget -O /usr/bin/podkop_updater.sh https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/podkop_updater.sh
   chmod +x /usr/bin/podkop_updater.sh
   ```
2. **Configure Telegram**:
   - Edit the script (`vi /usr/bin/podkop_updater.sh`).
   - Replace `your_bot_token` with your Telegram bot token.
   - Replace `your_chat_id` with your Telegram chat ID.
3. **Verify Dependencies**:
   ```sh
   opkg update && opkg install curl jq wget
   ```

## Usage
1. **Run Manually**:
   ```sh
   /usr/bin/podkop_updater.sh
   ```
   - If a new `podkop` version is available, you’ll receive a Telegram message:
     ```
     New version available: 0.3.43. Current: 0.3.41-1. Reply to this message with 'yes' to update or 'no' to cancel.
     ```
   - Reply **directly** to the message with "yes" to update or "no" to cancel.
   - The script waits 5 minutes for a response and logs actions to `/tmp/podkop_update.log`.
2. **Automate with Cron**:
   - The installer sets up a cron job based on your specified frequency (e.g., hourly, every 6 hours).
   - To modify, edit the crontab:
     ```sh
     crontab -e
     ```
     Example for hourly execution:
     ```sh
     PATH=/usr/bin:/bin:/usr/sbin:/sbin
     0 * * * * /usr/bin/podkop_updater.sh
     ```

## How It Works
1. **Version Check**:
   - Fetches the latest `podkop` release from [GitHub](https://api.github.com/repos/itdoginfo/podkop/releases/latest).
   - Gets the installed version via `opkg info podkop`.
2. **Notification**:
   - If a new version is available, sends a Telegram message.
3. **User Response**:
   - Polls Telegram API every 5 seconds for a "yes" or "no" reply (case-insensitive).
4. **Update**:
   - On "yes," runs the update script (`https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh`).
   - Automatically answers two prompts:
     - "Just upgrade podkop?" → `y`
     - "Need a Russian translation?" → `y`
5. **Logging**:
   - All actions, including Telegram responses and update script output, are logged to `/tmp/podkop_update.log`.

## Troubleshooting
- **Telegram message not sent**:
  - Check `/tmp/podkop_update.log` for errors (e.g., "Cannot connect to Telegram API").
  - Verify `BOT_TOKEN` and `CHAT_ID`.
  - Ensure the router can reach `api.telegram.org`:
    ```sh
    ping api.telegram.org
    curl -s https://api.telegram.org/bot<your_bot_token>/getMe
    ```
- **"Yes" reply not detected**:
  - Ensure you reply **directly** to the bot’s message (click "Reply" in Telegram).
  - Check bot privacy settings in [@BotFather](https://t.me/BotFather):
    ```sh
    /setprivacy -> Select your bot -> Disable
    ```
  - Manually query Telegram API:
    ```sh
    curl -s "https://api.telegram.org/bot<your_bot_token>/getUpdates"
    ```
    Look for your "yes" reply with `reply_to_message.message_id` matching the bot’s message ID.
- **Update script fails**:
  - Test the update script manually:
    ```sh
    echo -e "y\ny\n" | sh <(wget -O - https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh)
    ```
  - Check `/tmp/podkop_update.log` for errors (e.g., "Failed to fetch update script").
  - Ensure `wget` is installed and the router has enough memory/storage:
    ```sh
    df -h
    free
    ```
- **No new version detected**:
  - Verify GitHub API access:
    ```sh
    curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest
    ```
  - Check the installed version:
    ```sh
    opkg info podkop
    ```

## Example Log
A successful run might look like:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, including package downloads and installation]
Update script executed successfully
```

## License
This script is released under the [MIT License](https://opensource.org/licenses/MIT).

## Contributing
Feel free to submit issues or pull requests to improve the script. Suggestions for better error handling, additional features, or support for other languages are welcome.

## Credits
Developed for automating `podkop` updates on OpenWrt routers, leveraging the [podkop](https://github.com/itdoginfo/podkop) project’s installation script.