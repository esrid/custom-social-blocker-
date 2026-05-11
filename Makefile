BINARY      := blocker
INSTALL     := /usr/local/bin/$(BINARY)
PLIST       := com.ap.blocker.plist
AGENTS_DIR  := $(HOME)/Library/LaunchAgents
PLIST_DST   := $(AGENTS_DIR)/$(PLIST)
SUDOERS_DST := /etc/sudoers.d/blocker
STATE_FILE  := $(HOME)/Library/Application Support/blocker/state.json
UID         := $(shell id -u)
USER        := $(shell whoami)
LABEL       := com.ap.blocker
PROXY_PORT  := 8877

.PHONY: build install uninstall clean status test proxy-on proxy-off

build:
	go build -o $(BINARY) .

install: build
	@echo ">>> Step 0: remove old system daemon if present"
	-sudo launchctl bootout system/$(LABEL) 2>/dev/null
	-sudo launchctl unload -w /Library/LaunchDaemons/$(PLIST) 2>/dev/null
	-sudo rm -f /Library/LaunchDaemons/$(PLIST)

	@echo ">>> Step 1: install binary"
	sudo install -m 755 $(BINARY) $(INSTALL)

	@echo ">>> Step 2: sudoers rule for networksetup"
	@{ \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -listallnetworkservices\n'; \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -setwebproxy *\n'; \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -setwebproxystate *\n'; \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -setsecurewebproxy *\n'; \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -setsecurewebproxystate *\n'; \
	  printf '$(USER) ALL=(root) NOPASSWD: /usr/sbin/networksetup -setautoproxystate *\n'; \
	} | sudo tee $(SUDOERS_DST) > /dev/null
	sudo chmod 0440 $(SUDOERS_DST)
	sudo chown root:wheel $(SUDOERS_DST)
	sudo visudo -cf $(SUDOERS_DST)

	@echo ">>> Step 3: configure system proxy (HTTP + HTTPS → 127.0.0.1:$(PROXY_PORT))"
	$(MAKE) proxy-on

	@echo ">>> Step 4: install + reload LaunchAgent"
	-launchctl bootout gui/$(UID)/$(LABEL) 2>/dev/null
	-launchctl unload -w $(PLIST_DST) 2>/dev/null
	@sleep 2
	mkdir -p $(AGENTS_DIR)
	cp $(PLIST) $(PLIST_DST)
	launchctl bootstrap gui/$(UID) $(PLIST_DST) 2>/dev/null || \
	    launchctl load -w $(PLIST_DST)
	@echo "  Waiting for daemon to start..."
	@sleep 3
	@launchctl list $(LABEL) 2>/dev/null | grep -q '"PID"' \
	    && echo "  ✓ Daemon running (PID found)" \
	    || echo "  ✗ Daemon not running — check: tail ~/Library/Logs/blocker.log"

	@echo ""
	@echo "✓ Blocker installed."
	@echo "  Proxy  : 127.0.0.1:$(PROXY_PORT)"
	@echo "  Log    : ~/Library/Logs/blocker.log"
	@echo "  Limits : 25 min/window · 2 h/day · blocked 20:00→08:00"

proxy-on:
	@echo "  Enabling proxy on all interfaces..."
	@sudo /usr/sbin/networksetup -listallnetworkservices 2>/dev/null | tail -n +2 | \
	while IFS= read -r svc; do \
	    [ -z "$$svc" ] && continue; \
	    echo "    $$svc"; \
	    sudo /usr/sbin/networksetup -setwebproxy "$$svc" 127.0.0.1 $(PROXY_PORT) 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setwebproxystate "$$svc" on 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setsecurewebproxy "$$svc" 127.0.0.1 $(PROXY_PORT) 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setsecurewebproxystate "$$svc" on 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setautoproxystate "$$svc" off 2>/dev/null; \
	done

proxy-off:
	@echo "  Disabling proxy on all interfaces..."
	@sudo /usr/sbin/networksetup -listallnetworkservices 2>/dev/null | tail -n +2 | \
	while IFS= read -r svc; do \
	    [ -z "$$svc" ] && continue; \
	    echo "    $$svc"; \
	    sudo /usr/sbin/networksetup -setwebproxystate "$$svc" off 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setsecurewebproxystate "$$svc" off 2>/dev/null; \
	    sudo /usr/sbin/networksetup -setautoproxystate "$$svc" off 2>/dev/null; \
	done

# Force block state + reload daemon in-memory state, then verify
test:
	@echo "Writing blocked state..."
	@mkdir -p "$(HOME)/Library/Application Support/blocker"
	@printf '{"date":"%s","total_minutes":120,"window_start":"%sT00:00:00Z","window_minutes":25,"night_block_until":"0001-01-01T00:00:00Z"}\n' \
	    "$$(date +%Y-%m-%d)" "$$(date +%Y-%m-%d)" \
	    > "$(STATE_FILE)"
	@PID=$$(launchctl list $(LABEL) 2>/dev/null | awk -F'"' '/"PID"/{print $$4}'); \
	    if [ -n "$$PID" ]; then \
	        kill -USR1 $$PID && echo "State reloaded in daemon (PID $$PID)"; \
	    else echo "WARNING: daemon not running"; fi
	@echo ""
	@echo "Testing proxy via HTTP (expect 403 Blocked)..."
	@curl -s -x http://127.0.0.1:$(PROXY_PORT) --max-time 5 -o /dev/null \
	    -w "HTTP status via proxy: %{http_code}\n" http://reddit.com
	@echo ""
	@echo "Close ALL social media tabs in Safari first, then open reddit.com / instagram.com / x.com"
	@echo "For X: Safari > Develop > Empty Caches first (iCloud cache bypass)"

uninstall:
	@echo ">>> Unloading agent..."
	-launchctl bootout gui/$(UID)/$(LABEL) 2>/dev/null || \
	    launchctl unload -w $(PLIST_DST) 2>/dev/null
	@echo ">>> Disabling proxy..."
	$(MAKE) proxy-off
	@echo ">>> Removing files..."
	-rm -f $(PLIST_DST)
	-sudo rm -f $(INSTALL)
	-sudo rm -f $(SUDOERS_DST)
	-rm -rf "$(HOME)/Library/Application Support/blocker"
	@echo "✓ Blocker removed."

status:
	@echo "=== Service ==="
	@launchctl list $(LABEL) 2>/dev/null || echo "(not loaded)"
	@echo ""
	@echo "=== Proxy (Wi-Fi) ==="
	@networksetup -getsecurewebproxy Wi-Fi 2>/dev/null || echo "(check manually)"
	@echo ""
	@echo "=== State ==="
	@test -f "$(STATE_FILE)" && cat "$(STATE_FILE)" || echo "(no state)"
	@echo ""
	@echo "=== Log (last 20) ==="
	@LOG="$(HOME)/Library/Logs/blocker.log"; \
	    test -f "$$LOG" && tail -20 "$$LOG" || echo "(no log)"

clean:
	rm -f $(BINARY)
