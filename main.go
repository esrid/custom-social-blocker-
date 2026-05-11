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
	pacPort     = 8878
	dailyLimit  = 120 // minutes per day
	hourlyLimit = 25  // minutes per clock-hour
	nightStart  = 20  // block at 20:00
	nightEnd    = 8   // unblock at 08:00
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
	Date            string      `json:"date"`
	TotalMinutes    int         `json:"total_minutes"`
	HourlyMinutes   map[int]int `json:"hourly_minutes"`
	NightBlockUntil time.Time   `json:"night_block_until"`
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
	fresh := State{Date: today, HourlyMinutes: make(map[int]int)}

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
	if s.HourlyMinutes == nil {
		s.HourlyMinutes = make(map[int]int)
	}
	if s.Date != today {
		b.state = State{
			Date:            today,
			HourlyMinutes:   make(map[int]int),
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
	if b.state.HourlyMinutes[now.Hour()] >= hourlyLimit {
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
	src, _, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}

	src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(dst, src)
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

func (b *Blocker) servePAC(w http.ResponseWriter, r *http.Request) {
	var entries []string
	for _, d := range socialDomains {
		entries = append(entries, `"`+d+`"`)
	}
	pac := fmt.Sprintf(`function FindProxyForURL(url, host) {
    var blocked = [%s];
    var h = host.toLowerCase();
    if (h.substring(0,4) === "www.") h = h.substring(4);
    for (var i = 0; i < blocked.length; i++) {
        if (h === blocked[i] || h.substr(-(blocked[i].length+1)) === "."+blocked[i])
            return "PROXY 127.0.0.1:%d";
    }
    return "DIRECT";
}
`, strings.Join(entries, ","), proxyPort)
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Write([]byte(pac))
}

// nsCmd builds a networksetup command, prefixed with sudo when running as non-root.
func nsCmd(args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("/usr/sbin/networksetup", args...)
	}
	return exec.Command("sudo", append([]string{"/usr/sbin/networksetup"}, args...)...)
}

func getNetworkServices() []string {
	out, err := nsCmd("-listallnetworkservices").Output()
	if err != nil {
		log.Printf("listallnetworkservices: %v", err)
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
	pacURL := fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", pacPort)
	for _, svc := range getNetworkServices() {
		nsCmd("-setautoproxyurl", svc, pacURL).Run()
		nsCmd("-setautoproxystate", svc, "on").Run()
		log.Printf("proxy enabled: %s", svc)
	}
}

func disableProxy() {
	for _, svc := range getNetworkServices() {
		nsCmd("-setautoproxystate", svc, "off").Run()
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
				HourlyMinutes:   make(map[int]int),
				NightBlockUntil: saved,
			}
			log.Printf("daily reset: %s", today)
		}

		if atomic.LoadInt64(&b.activeCount) > 0 && !b.isBlockedLocked() {
			h := now.Hour()
			b.state.TotalMinutes++
			b.state.HourlyMinutes[h]++
			log.Printf("usage: %d/%d daily, h%d: %d/%d",
				b.state.TotalMinutes, dailyLimit,
				h, b.state.HourlyMinutes[h], hourlyLimit)
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

	pacMux := http.NewServeMux()
	pacMux.HandleFunc("/proxy.pac", b.servePAC)
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", pacPort), pacMux); err != nil {
			log.Fatalf("PAC server: %v", err)
		}
	}()

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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	log.Println("shutting down, disabling proxy...")
	disableProxy()
	log.Println("done")
}
