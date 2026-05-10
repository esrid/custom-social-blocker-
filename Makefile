BINARY_NAME=blocker
PLIST_NAME=com.ap.blocker.plist
PLIST_PATH=/Library/LaunchDaemons/$(PLIST_NAME)
INSTALL_DIR=/Users/ap/dev/blocker

build:
	go build -o $(BINARY_NAME) main.go

install: build
	@echo "Installing blocker service..."
	# Copy plist to LaunchDaemons
	sudo cp $(PLIST_NAME) $(PLIST_PATH)
	# Set correct ownership and permissions for the plist
	sudo chown root $(PLIST_PATH)
	sudo chmod 644 $(PLIST_PATH)
	# Load the daemon
	sudo launchctl load -w $(PLIST_PATH)
	@echo "Blocker installed and started."

uninstall:
	@echo "Uninstalling blocker service..."
	# Unload the daemon
	-sudo launchctl unload -w $(PLIST_PATH)
	# Remove the plist
	-sudo rm $(PLIST_PATH)
	# Remove the binary
	-rm $(BINARY_NAME)
	@echo "Blocker uninstalled."

clean:
	rm -f $(BINARY_NAME)
	rm -f blocker.log
	rm -f state.json
