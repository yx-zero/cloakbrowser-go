package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

// chromeProcess is one launched stealth Chromium keyed by seed.
type chromeProcess struct {
	seed        string
	cmd         *exec.Cmd
	cdpPort     int
	userDataDir string
	timezone    string
	locale      string
	proxy       string
}

func (cp *chromeProcess) alive() bool {
	if cp.cmd == nil || cp.cmd.Process == nil {
		return false
	}
	// ProcessState is set once Wait returns; we don't Wait, so probe via Signal 0.
	return cp.cmd.ProcessState == nil
}

// chromePool manages multiple Chrome processes keyed by fingerprint seed.
type chromePool struct {
	binary     string
	globalArgs []string
	cfg        config

	mu          sync.Mutex
	processes   map[string]*chromeProcess
	seedLocks   map[string]*sync.Mutex
	connections map[string]int
	idleTimers  map[string]*time.Timer
	nextPort    int
}

func newChromePool(binary string, globalArgs []string, cfg config) *chromePool {
	return &chromePool{
		binary:      binary,
		globalArgs:  globalArgs,
		cfg:         cfg,
		processes:   map[string]*chromeProcess{},
		seedLocks:   map[string]*sync.Mutex{},
		connections: map[string]int{},
		idleTimers:  map[string]*time.Timer{},
		nextPort:    baseCDPPort,
	}
}

func (p *chromePool) seedLock(key string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.seedLocks[key]; ok {
		return l
	}
	l := &sync.Mutex{}
	p.seedLocks[key] = l
	return l
}

func (p *chromePool) allocatePort() (int, error) {
	for i := 0; i < 200; i++ {
		p.mu.Lock()
		port := p.nextPort
		p.nextPort++
		p.mu.Unlock()
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free ports available for Chrome CDP")
}

// connect increments the connection refcount and cancels any idle reaping.
func (p *chromePool) connect(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.idleTimers[key]; ok {
		t.Stop()
		delete(p.idleTimers, key)
	}
	p.connections[key]++
}

// disconnect decrements the refcount and schedules idle cleanup when it hits 0.
func (p *chromePool) disconnect(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections[key]--
	if p.connections[key] <= 0 {
		delete(p.connections, key)
		p.scheduleIdleCleanupLocked(key)
	}
}

func (p *chromePool) scheduleIdleCleanupLocked(key string) {
	if p.cfg.idleTimeout <= 0 {
		return
	}
	if _, ok := p.processes[key]; !ok {
		return
	}
	if t, ok := p.idleTimers[key]; ok {
		t.Stop()
	}
	p.idleTimers[key] = time.AfterFunc(p.cfg.idleTimeout, func() {
		p.mu.Lock()
		_, hasTimer := p.idleTimers[key]
		conns := p.connections[key]
		p.mu.Unlock()
		if !hasTimer || conns > 0 {
			return
		}
		log.Printf("cleaning up idle Chrome process (seed=%s)", key)
		p.cleanupProcess(key)
	})
}

// getOrLaunch returns the running process for a seed, launching one if needed.
// seed == "" routes to the shared default process under key "__default__".
func (p *chromePool) getOrLaunch(seed string, extraArgs []string, timezone, locale, proxy string, geoip bool) (*chromeProcess, error) {
	if seed == "" && p.cfg.defaultSeed != "" {
		seed = p.cfg.defaultSeed
	}
	if locale == "" {
		locale = p.cfg.defaultLocale
	}
	if timezone == "" {
		timezone = p.cfg.defaultTimezone
	}

	var seedKey, actualSeed string
	if seed == "" {
		seedKey = "__default__"
		actualSeed = fmt.Sprintf("%d", rand.Intn(90000)+10000)
	} else {
		if !cb.SafeSeed(seed) {
			return nil, fmt.Errorf("invalid fingerprint seed")
		}
		seedKey = seed
		actualSeed = seed
	}

	lock := p.seedLock(seedKey)
	lock.Lock()
	defer lock.Unlock()

	p.mu.Lock()
	if proc, ok := p.processes[seedKey]; ok && proc.alive() {
		p.mu.Unlock()
		if len(extraArgs) > 0 || timezone != "" || locale != "" || proxy != "" || geoip {
			log.Printf("seed %s already running (port %d) — ignoring new params (first-launch wins)", seedKey, proc.cdpPort)
		}
		return proc, nil
	}
	p.mu.Unlock()
	// Dead entry — clean up before relaunch.
	p.cleanupProcess(seedKey)

	args, tz, loc := cb.BuildSeedArgs(cb.SeedArgsOptions{
		Seed:      actualSeed,
		ExtraArgs: extraArgs,
		Timezone:  timezone,
		Locale:    locale,
		Proxy:     proxy,
		GeoIP:     geoip,
		Headless:  p.cfg.headless,
	})

	port, err := p.allocatePort()
	if err != nil {
		return nil, err
	}
	userDataDir := filepath.Join(p.cfg.dataDir, seedKey)
	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, err
	}

	full := append([]string(nil), baseChromeArgs...)
	if p.cfg.headless {
		full = append(full, "--headless=new")
	}
	full = append(full, args...)
	full = append(full, p.globalArgs...)
	full = append(full,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir="+userDataDir,
	)

	log.Printf("launching Chrome (seed=%s, port=%d)", actualSeed, port)
	cmd := exec.Command(p.binary, full...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Chrome: %w", err)
	}

	if !waitForCDP(port, 15*time.Second) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		safeRemove(p.cfg.dataDir, userDataDir)
		return nil, fmt.Errorf("Chrome failed to start")
	}

	cp := &chromeProcess{
		seed:        actualSeed,
		cmd:         cmd,
		cdpPort:     port,
		userDataDir: userDataDir,
		timezone:    tz,
		locale:      loc,
		proxy:       proxy,
	}
	p.mu.Lock()
	p.processes[seedKey] = cp
	p.mu.Unlock()
	log.Printf("Chrome ready (seed=%s, port=%d, pid=%d)", actualSeed, port, cmd.Process.Pid)
	return cp, nil
}

func (p *chromePool) cleanupProcess(key string) {
	p.mu.Lock()
	proc := p.processes[key]
	delete(p.processes, key)
	if t, ok := p.idleTimers[key]; ok {
		t.Stop()
		delete(p.idleTimers, key)
	}
	delete(p.connections, key)
	p.mu.Unlock()

	if proc == nil {
		return
	}
	if proc.cmd != nil && proc.cmd.Process != nil {
		done := make(chan struct{})
		go func() { proc.cmd.Wait(); close(done) }()
		_ = proc.cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = proc.cmd.Process.Kill()
			<-done
		}
	}
	safeRemove(p.cfg.dataDir, proc.userDataDir)
}

func (p *chromePool) shutdown() {
	p.mu.Lock()
	keys := make([]string, 0, len(p.processes))
	for k := range p.processes {
		keys = append(keys, k)
	}
	p.mu.Unlock()
	for _, k := range keys {
		p.cleanupProcess(k)
	}
	log.Println("all Chrome processes terminated")
}

// status returns a snapshot for the health endpoint.
func (p *chromePool) status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	procs := map[string]any{}
	for key, proc := range p.processes {
		if !proc.alive() {
			continue
		}
		_, idlePending := p.idleTimers[key]
		procs[key] = map[string]any{
			"pid":                  proc.cmd.Process.Pid,
			"port":                 proc.cdpPort,
			"seed":                 proc.seed,
			"connections":          p.connections[key],
			"idle_cleanup_pending": idlePending,
			"timezone":             proc.timezone,
			"locale":               proc.locale,
			"proxy":                proc.proxy,
		}
	}
	return map[string]any{
		"status":       "ok",
		"active":       len(procs),
		"idle_timeout": p.cfg.idleTimeout.Seconds(),
		"processes":    procs,
	}
}

// safeRemove deletes path only if it is strictly inside dataDir.
func safeRemove(dataDir, path string) {
	resolved, err1 := filepath.Abs(path)
	base, err2 := filepath.Abs(dataDir)
	if err1 != nil || err2 != nil {
		return
	}
	if resolved == base || !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
		log.Printf("refusing to delete path outside data_dir: %s", resolved)
		return
	}
	_ = os.RemoveAll(path)
}

// waitForCDP polls Chrome's /json/version until it responds.
func waitForCDP(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	delay := 100 * time.Millisecond
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(delay)
		if delay < time.Second {
			delay *= 2
		}
	}
	return false
}

// fetchCDP GETs a CDP HTTP endpoint and returns the raw body.
func fetchCDP(ctx context.Context, port int, path string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
