# blocker

macOS social media blocker for Safari. Works with iCloud Private Relay enabled.

## How it works

Runs a local HTTP/HTTPS proxy on `127.0.0.1:8877` and configures it as the system proxy on every network interface. Because macOS automatically disables iCloud Private Relay when a system proxy is set, all Safari traffic is intercepted regardless of privacy settings.

**Limits enforced:**
- 25 min per rolling 60-minute window
- 2 hours total per day (resets at midnight)
- 20:00 → 08:00 fully blocked every night

**Blocked platforms:** Facebook, Instagram, X/Twitter, TikTok, Reddit, YouTube, LinkedIn, Snapchat, Pinterest, Tumblr, Twitch, Threads, Discord, VK + their CDN domains.

## Requirements

- macOS (tested on Sequoia 15.x)
- Go 1.21+
- Xcode Command Line Tools

## Install

```bash
make install
```

Installs the binary to `/usr/local/bin/blocker`, registers it as a LaunchAgent (auto-starts at login), and configures the system proxy. Requires `sudo` once for the binary install and a sudoers rule.

## Uninstall

```bash
make uninstall
```

Stops the daemon, removes all files, and clears proxy settings.

## Commands

```bash
make status      # service status, proxy config, current state, last log lines
make test        # force blocked state and verify proxy intercepts correctly
make proxy-on    # re-apply proxy settings (e.g. after connecting to new network)
make proxy-off   # temporarily disable proxy
```

## Notes

- **New network interface** (VPN, new Wi-Fi): run `make proxy-on` to apply settings to the new interface.
- **X / Twitter cache**: if X still loads after the limit is reached, open Safari → Develop → Empty Caches.
- **State file**: `~/Library/Application Support/blocker/state.json` — daily usage persists across reboots.
- **Log**: `~/Library/Logs/blocker.log`
