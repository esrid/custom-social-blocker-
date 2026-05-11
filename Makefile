BINARY      := blocker
INSTALL     := /usr/local/bin/$(BINARY)
PLIST       := com.ap.blocker.plist
AGENTS_DIR  := $(HOME)/Library/LaunchAgents
PLIST_DST   := $(AGENTS_DIR)/$(PLIST)
SUDOERS_DST := /etc/sudoers.d/blocker
UID         := $(shell id -u)
LABEL       := com.ap.blocker

.PHONY: build install uninstall clean status

build:
	go build -o $(BINARY) .

install: build
	@echo ">>> Step 0/4: remove old system daemon if present"
	-sudo launchctl bootout system/$(LABEL) 2>/dev/null
	-sudo launchctl unload -w /Library/LaunchDaemons/$(PLIST) 2>/dev/null
	-sudo rm -f /Library/LaunchDaemons/$(PLIST)

	@echo ">>> Step 1/4: install binary (sudo required)"
	sudo install -m 755 $(BINARY) $(INSTALL)

	@echo ">>> Step 2/4: allow networksetup without password"
	@printf '%s ALL=(root) NOPASSWD: /usr/sbin/networksetup -setautoproxyurl *\n' "$(shell whoami)" | sudo tee $(SUDOERS_DST) > /dev/null
	@printf '%s ALL=(root) NOPASSWD: /usr/sbin/networksetup -setautoproxystate *\n' "$(shell whoami)" | sudo tee -a $(SUDOERS_DST) > /dev/null
	@printf '%s ALL=(root) NOPASSWD: /usr/sbin/networksetup -listallnetworkservices\n' "$(shell whoami)" | sudo tee -a $(SUDOERS_DST) > /dev/null
	sudo chmod 0440 $(SUDOERS_DST)
	sudo chown root:wheel $(SUDOERS_DST)
	sudo visudo -cf $(SUDOERS_DST)   # validate — fails safe if syntax is wrong

	@echo ">>> Step 3/4: install LaunchAgent plist"
	mkdir -p $(AGENTS_DIR)
	cp $(PLIST) $(PLIST_DST)

	@echo ">>> Step 4/4: load agent"
	# Try modern bootstrap first, fall back to legacy load
	launchctl bootstrap gui/$(UID) $(PLIST_DST) 2>/dev/null || \
	    launchctl load -w $(PLIST_DST)

	@echo ""
	@echo ">>> Blocker installed and running."
	@echo "    Proxy  : http://127.0.0.1:8877"
	@echo "    PAC    : http://127.0.0.1:8878/proxy.pac"
	@echo "    Log    : ~/Library/Logs/blocker.log"
	@echo "    State  : ~/Library/Application Support/blocker/state.json"
	@echo ""
	@echo "    Limits : 25 min/hour · 2 h/day"
	@echo "    Night  : 20:00 → 08:00 fully blocked"

uninstall:
	@echo ">>> Unloading agent..."
	-launchctl bootout gui/$(UID)/$(LABEL) 2>/dev/null || \
	    launchctl unload -w $(PLIST_DST) 2>/dev/null
	@sleep 1
	@echo ">>> Disabling proxy..."
	-$(INSTALL) disable-proxy 2>/dev/null
	@echo ">>> Removing files..."
	-rm -f $(PLIST_DST)
	-sudo rm -f $(INSTALL)
	-sudo rm -f $(SUDOERS_DST)
	-rm -rf "$(HOME)/Library/Application Support/blocker"
	@echo ">>> Done. Proxy settings cleared."

status:
	@echo "=== Service ==="
	@launchctl list $(LABEL) 2>/dev/null || echo "(not loaded)"
	@echo ""
	@echo "=== State ==="
	@STATE="$(HOME)/Library/Application Support/blocker/state.json"; \
	    test -f "$$STATE" && cat "$$STATE" || echo "(no state file)"
	@echo ""
	@echo "=== Log (last 20 lines) ==="
	@LOG="$(HOME)/Library/Logs/blocker.log"; \
	    test -f "$$LOG" && tail -20 "$$LOG" || echo "(no log)"

clean:
	rm -f $(BINARY)
