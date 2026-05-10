# Social Media Blocker

A lightweight macOS background service that monitors browser activity and enforces time limits on social media usage by modifying the `/etc/hosts` file.

## Features
- **Session Limit:** 25 minutes of active usage followed by a 1-hour block.
- **Daily Limit:** 2 hours total usage per day.
- **Browser Support:** Monitors active tabs in Safari and Google Chrome.
- **Persistence:** Tracks usage in `state.json` to survive restarts.
- **Background Service:** Runs as a macOS LaunchDaemon.

## Installation

1. **Clone the repository** to `/Users/ap/dev/blocker` (or update the paths in `main.go`, `Makefile`, and `com.ap.blocker.plist`).
2. **Install the service**:
   ```bash
   make install
   ```
3. **Grant Permissions**: 
   - Go to **System Settings > Privacy & Security > Automation**.
   - Ensure the service has permission to control your browser (Safari/Chrome).

## Usage
- **Check Logs**: `tail -f blocker.log`
- **Check Usage**: `cat state.json`
- **Uninstall**: `make uninstall`

## Configuration
Modify the constants in `main.go` to adjust time limits or add new domains:
- `DailyLimit`: Total allowed time per day.
- `SessionLimit`: Allowed time before a temporary block.
- `BlockDuration`: How long the temporary block lasts.
- `socialMediaDomains`: List of domains to block.
