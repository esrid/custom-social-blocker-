package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	proxyPort   = 8877
	dailyLimit  = 120
	hourlyLimit = 25
	nightStart  = 20
	nightEnd    = 8
)

var (
	stateDir  string
	stateFile string
	logFile   string
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	if os.Getuid() == 0 {
		stateDir = "/var/db/blocker"
		stateFile = "/var/db/blocker/state.json"
		logFile = "/var/log/blocker.log"
	} else {
		stateDir = filepath.Join(home, "Library", "Application Support", "blocker")
		stateFile = filepath.Join(stateDir, "state.json")
		logFile = filepath.Join(home, "Library", "Logs", "blocker.log")
	}
}

var socialDomains = []string{
	"facebook.com",
	"fbcdn.net",
	"instagram.com",
	"cdninstagram.com",
	"twitter.com",
	"twimg.com",
	"x.com",
	"t.co",
	"tiktok.com",
	"tiktokcdn.com",
	"reddit.com",
	"redd.it",
	"redditmedia.com",
	"redditstatic.com",
	"youtube.com",
	"youtu.be",
	"ytimg.com",
	"googlevideo.com",
	"linkedin.com",
	"licdn.com",
	"snapchat.com",
	"pinterest.com",
	"pinimg.com",
	"tumblr.com",
	"twitch.tv",
	"twitchsvc.net",
	"threads.net",
	"discord.com",
	"discordapp.com",
	"discordapp.net",
	"vk.com",
}

type State struct {
	Date            string    `json:"date"`
	TotalMinutes    int       `json:"total_minutes"`
	WindowStart     time.Time `json:"window_start"`      // start of current 60-min rolling window
	WindowMinutes   int       `json:"window_minutes"`    // minutes used in that window
	NightBlockUntil time.Time `json:"night_block_until"`
}

type Blocker struct {
	mu          sync.RWMutex
	state       State
	activeCount int64 // atomic: open social media CONNECT tunnels
}

func newBlocker() *Blocker {
	b := &Blocker{}
	b.loadState()
	return b
}

func (b *Blocker) loadState() {
	b.mu.Lock()
	defer b.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	fresh := State{Date: today}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		b.state = fresh
		return
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		b.state = fresh
		return
	}
	if s.Date != today {
		b.state = State{
			Date:            today,
			NightBlockUntil: s.NightBlockUntil,
		}
	} else {
		b.state = s
	}
	log.Printf("state loaded: %s %d/%d min", b.state.Date, b.state.TotalMinutes, dailyLimit)
}

func (b *Blocker) saveState() {
	os.MkdirAll(stateDir, 0755)
	data, _ := json.MarshalIndent(b.state, "", "  ")
	os.WriteFile(stateFile, data, 0644)
}

func (b *Blocker) isBlockedLocked() bool {
	now := time.Now()
	if !b.state.NightBlockUntil.IsZero() && now.Before(b.state.NightBlockUntil) {
		return true
	}
	if b.state.TotalMinutes >= dailyLimit {
		return true
	}
	// Rolling 60-min window: if 25 min used and window not yet expired → blocked
	if b.state.WindowMinutes >= hourlyLimit &&
		now.Before(b.state.WindowStart.Add(time.Hour)) {
		return true
	}
	return false
}

func (b *Blocker) isBlocked() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isBlockedLocked()
}

func (b *Blocker) isDomainSocial(host string) bool {
	h := strings.ToLower(host)
	if i := strings.LastIndex(h, ":"); i != -1 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "www.")
	for _, d := range socialDomains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

func blockedHTML() string {
	return `<!DOCTYPE html><html>
<head><meta charset="utf-8"><title>Blocked</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#0f0f1a;color:#e0e0e0;font-family:-apple-system,sans-serif;
     display:flex;align-items:center;justify-content:center;height:100vh}
.card{text-align:center;padding:3rem;border:1px solid #2a2a4a;border-radius:16px;
      background:#16162a;max-width:400px}
h1{font-size:3.5rem;margin-bottom:1rem}
p{color:#7070a0;font-size:1.1rem;line-height:1.8}
</style></head>
<body><div class="card">
<h1>🚫</h1>
<p>Social media blocked.<br>Time limit reached.<br>
<strong style="color:#e0e0e0">You got this.</strong></p>
</div></body></html>`
}

func (b *Blocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		b.handleConnect(w, r)
	} else {
		b.handleHTTP(w, r)
	}
}

func (b *Blocker) handleConnect(w http.ResponseWriter, r *http.Request) {
	isSocial := b.isDomainSocial(r.Host)

	if isSocial && b.isBlocked() {
		log.Printf("BLOCK CONNECT %s", r.Host)
		http.Error(w, "Blocked by social media blocker", http.StatusForbidden)
		return
	}

	if isSocial {
		atomic.AddInt64(&b.activeCount, 1)
		defer atomic.AddInt64(&b.activeCount, -1)
	}

	dst, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		dst.Close()
		return
	}
	src, buf, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}

	src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	log.Printf("TUNNEL %s", r.Host)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(dst, buf) // drain bufio buffer first, then stream
		dst.Close()
	}()
	go func() {
		defer wg.Done()
		io.Copy(src, dst)
		src.Close()
	}()
	wg.Wait()
}

func (b *Blocker) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if b.isDomainSocial(r.Host) && b.isBlocked() {
		log.Printf("BLOCK HTTP %s", r.Host)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(blockedHTML()))
		return
	}

	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authorization")

	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}


// networkSetup runs networksetup with sudo -n (non-interactive, requires NOPASSWD sudoers rule).
func networkSetup(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", "/usr/sbin/networksetup"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("networksetup %v: %v — %s", args, err, out)
	}
	return err
}

func getNetworkServices() []string {
	out, err := exec.Command("sudo", "-n", "/usr/sbin/networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var svcs []string
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, line)
	}
	return svcs
}

func enableProxy() {
	host := "127.0.0.1"
	port := fmt.Sprintf("%d", proxyPort)
	for _, svc := range getNetworkServices() {
		networkSetup("-setwebproxy", svc, host, port)
		networkSetup("-setwebproxystate", svc, "on")
		networkSetup("-setsecurewebproxy", svc, host, port)
		networkSetup("-setsecurewebproxystate", svc, "on")
		networkSetup("-setautoproxystate", svc, "off") // disable any old PAC
		log.Printf("proxy enabled: %s", svc)
	}
}

func disableProxy() {
	for _, svc := range getNetworkServices() {
		networkSetup("-setwebproxystate", svc, "off")
		networkSetup("-setsecurewebproxystate", svc, "off")
		networkSetup("-setautoproxystate", svc, "off")
		log.Printf("proxy disabled: %s", svc)
	}
}

func (b *Blocker) nightScheduler() {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), nightStart, 0, 0, 0, now.Location())
		if !now.Before(next) {
			next = next.AddDate(0, 0, 1)
		}
		log.Printf("next night block: %v", next)
		time.Sleep(time.Until(next))

		b.mu.Lock()
		n := time.Now()
		until := time.Date(n.Year(), n.Month(), n.Day()+1, nightEnd, 0, 0, 0, n.Location())
		b.state.NightBlockUntil = until
		b.saveState()
		b.mu.Unlock()
		log.Printf("night block active until %v", until)
	}
}

func (b *Blocker) usageTracker() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		today := now.Format("2006-01-02")
		b.mu.Lock()

		if b.state.Date != today {
			saved := b.state.NightBlockUntil
			b.state = State{
				Date:            today,
				NightBlockUntil: saved,
			}
			log.Printf("daily reset: %s", today)
		}

		// Roll window if 60 min have passed since it started
		if now.Sub(b.state.WindowStart) >= time.Hour {
			b.state.WindowStart = now
			b.state.WindowMinutes = 0
		}

		if atomic.LoadInt64(&b.activeCount) > 0 && !b.isBlockedLocked() {
			b.state.TotalMinutes++
			b.state.WindowMinutes++
			windowExpires := b.state.WindowStart.Add(time.Hour)
			log.Printf("usage: %d/%d daily, window: %d/%d (resets %s)",
				b.state.TotalMinutes, dailyLimit,
				b.state.WindowMinutes, hourlyLimit,
				windowExpires.Format("15:04"))
			b.saveState()
		}

		b.mu.Unlock()
	}
}

func proxyMaintainer() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		enableProxy()
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "disable-proxy" {
		disableProxy()
		return
	}

	os.MkdirAll(filepath.Dir(logFile), 0755)
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags)
	log.Println("=== blocker starting ===")

	b := newBlocker()

	// Activate night block immediately if we start during night hours.
	now := time.Now()
	h := now.Hour()
	b.mu.Lock()
	// Init rolling window if never set
	if b.state.WindowStart.IsZero() {
		b.state.WindowStart = now
	}
	if h >= nightStart || h < nightEnd {
		if b.state.NightBlockUntil.IsZero() || now.After(b.state.NightBlockUntil) {
			var until time.Time
			if h >= nightStart {
				until = time.Date(now.Year(), now.Month(), now.Day()+1, nightEnd, 0, 0, 0, now.Location())
			} else {
				until = time.Date(now.Year(), now.Month(), now.Day(), nightEnd, 0, 0, 0, now.Location())
			}
			b.state.NightBlockUntil = until
			b.saveState()
			log.Printf("startup: night block until %v", until)
		}
	}
	b.mu.Unlock()

	go func() {
		srv := &http.Server{
			Addr:    fmt.Sprintf("127.0.0.1:%d", proxyPort),
			Handler: b,
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("proxy: %v", err)
		}
	}()

	// Give listeners a moment before touching system proxy.
	time.Sleep(500 * time.Millisecond)

	enableProxy()

	go b.nightScheduler()
	go b.usageTracker()
	go proxyMaintainer()

	log.Println("blocker running")

	// SIGUSR1 → reload state from disk (used by make test / make block-test)
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	go func() {
		for range usr1 {
			b.loadState()
			log.Println("state reloaded from disk")
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	log.Println("shutting down, disabling proxy...")
	disableProxy()
	log.Println("done")
}
