package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	HostPath       = "/etc/hosts"
	Localhost      = "127.0.0.1"
	StartTag       = "# start-blocker"
	EndTag         = "# end-blocker"
	StateFile      = "/Users/ap/dev/blocker/state.json"
	DailyLimit     = 2 * time.Hour
	SessionLimit   = 25 * time.Minute
	BlockDuration  = 1 * time.Hour
	CheckInterval  = 5 * time.Second
	TargetFileName = HostPath
)

var socialMediaDomains = []string{
	"www.youtube.com",
	"youtube.com",
	"www.reddit.com",
	"reddit.com",
	"www.x.com",
	"x.com",
	"twitter.com",
	"www.facebook.com",
	"facebook.com",
	"www.instagram.com",
	"instagram.com",
}

type State struct {
	LastResetDate  string        `json:"last_reset_date"`
	TotalTimeToday time.Duration `json:"total_time_today"`
	SessionTime    time.Duration `json:"session_time"`
	IsBlocked      bool          `json:"is_blocked"`
	UnblockAt      time.Time     `json:"unblock_at"`
}

func loadState() State {
	var state State
	data, err := os.ReadFile(StateFile)
	if err != nil {
		return State{
			LastResetDate: time.Now().Format("2006-01-02"),
		}
	}
	json.Unmarshal(data, &state)

	// Reset if it's a new day
	today := time.Now().Format("2006-01-02")
	if state.LastResetDate != today {
		state.LastResetDate = today
		state.TotalTimeToday = 0
		state.SessionTime = 0
		state.IsBlocked = false
	}
	return state
}

func (s State) save() {
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(StateFile, data, 0644)
}

func isSocialMediaActive() bool {
	// Check Chrome and Safari on macOS
	scripts := []string{
		`tell application "Google Chrome" to get URL of active tab of front window`,
		`tell application "Safari" to get URL of current tab of front window`,
	}

	for _, script := range scripts {
		out, err := exec.Command("osascript", "-e", script).Output()
		if err == nil {
			url := strings.ToLower(string(out))
			for _, domain := range socialMediaDomains {
				if strings.Contains(url, domain) {
					return true
				}
			}
		}
	}
	return false
}

func applyBlock(filename string) {
	if isBlockedInFile(filename) {
		return
	}
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		fmt.Printf("Error blocking: %v\n", err)
		return
	}
	defer f.Close()

	fmt.Fprintln(f, "\n"+StartTag)
	for _, domain := range socialMediaDomains {
		fmt.Fprintf(f, "%s\t%s\n", Localhost, domain)
	}
	fmt.Fprintln(f, EndTag)
}

func unblock(filename string) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	skip := false
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == StartTag {
			skip = true
			found = true
			continue
		}
		if trimmed == EndTag {
			skip = false
			continue
		}
		if !skip {
			newLines = append(newLines, line)
		}
	}

	if found {
		os.WriteFile(filename, []byte(strings.Join(newLines, "\n")), 0644)
	}
}

func isBlockedInFile(filename string) bool {
	content, err := os.ReadFile(filename)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), StartTag)
}

func timer() {
	for {
		state := loadState()
		now := time.Now()

		if state.IsBlocked {
			if now.After(state.UnblockAt) && state.TotalTimeToday < DailyLimit {
				fmt.Println("Unblocking social media...")
				unblock(TargetFileName)
				state.IsBlocked = false
				state.SessionTime = 0
			}
		} else {
			if isSocialMediaActive() {
				state.SessionTime += CheckInterval
				state.TotalTimeToday += CheckInterval
				fmt.Printf("Social media active. Session: %v, Daily: %v\n", state.SessionTime, state.TotalTimeToday)

				if state.TotalTimeToday >= DailyLimit {
					fmt.Println("Daily limit reached! Blocking...")
					applyBlock(TargetFileName)
					state.IsBlocked = true
					// Block for the rest of the day (until next reset)
					state.UnblockAt = now.Add(24 * time.Hour) 
				} else if state.SessionTime >= SessionLimit {
					fmt.Println("Session limit reached! Blocking for 1 hour...")
					applyBlock(TargetFileName)
					state.IsBlocked = true
					state.UnblockAt = now.Add(BlockDuration)
				}
			} else {
				// Optionally reset session time if not active for a while? 
				// The prompt says "des que j'ai atteint 25min bloqué", implying cumulative session time.
			}
		}

		state.save()
		time.Sleep(CheckInterval)
	}
}

func main() {
	fmt.Println("Social Media Blocker started...")
	// Ensure we start in a clean state if not supposed to be blocked
	state := loadState()
	if !state.IsBlocked {
		unblock(TargetFileName)
	} else {
		applyBlock(TargetFileName)
	}
	
	timer()
}
