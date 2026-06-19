// Command cloakserve is a CDP multiplexer: it spawns one stealth Chromium
// process per fingerprint seed behind a single port, routing CDP WebSocket
// connections to the right process. Ports the Python bin/cloakserve.
//
//	cloakserve                          # default, port 9222
//	cloakserve --port=9222
//	cloakserve --idle-timeout=300       # reap idle seeds after 5 min
//
// Client:
//
//	connect_over_cdp("http://host:9222/json/version?fingerprint=12345")
//	connect_over_cdp("http://host:9222/json/version?fingerprint=12345&timezone=America/New_York&locale=en-US")
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

var baseChromeArgs = []string{
	"--no-first-run",
	"--no-default-browser-check",
	"--disable-dev-shm-usage",
	"--disable-popup-blocking",
	"--disable-background-networking",
	"--metrics-recording-only",
	"--ignore-gpu-blocklist",
	"--remote-allow-origins=*",
}

const baseCDPPort = 5100

func main() {
	log.SetFlags(log.Ltime)

	binary, err := cb.EnsureBinary()
	if err != nil {
		log.Fatalf("cloakserve: %v", err)
	}

	cfg, globalArgs := parseCLIArgs(os.Args[1:])
	if cfg.defaultSeed != "" && !cb.SafeSeed(cfg.defaultSeed) {
		log.Fatalf("cloakserve: invalid --fingerprint seed: %s", cfg.defaultSeed)
	}

	pool := newChromePool(binary, globalArgs, cfg)
	defer pool.shutdown()

	mux := http.NewServeMux()
	srv := &server{pool: pool, port: cfg.port}
	mux.HandleFunc("/", srv.handleRoot)
	mux.HandleFunc("/json/version", srv.handleJSONVersion)
	mux.HandleFunc("/json/list", srv.handleJSONList)
	mux.HandleFunc("/devtools/", srv.handleWSDefault)
	mux.HandleFunc("/fingerprint/", srv.handleWSSeed)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.port)
	httpSrv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("cloakserve listening on http://%s (idle-timeout=%.0fs)", addr, cfg.idleTimeout.Seconds())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("cloakserve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("cloakserve shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// CLI config
// ---------------------------------------------------------------------------

type config struct {
	port            int
	headless        bool
	dataDir         string
	defaultSeed     string
	defaultLocale   string
	defaultTimezone string
	idleTimeout     time.Duration
}

func defaultDataDir() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/tmp/cloakserve"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloakbrowser", "cloakserve")
}

func parseIdleTimeout(v string) time.Duration {
	v = strings.TrimSpace(strings.ToLower(v))
	switch v {
	case "0", "false", "off", "none", "disabled", "":
		return 0
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

func parseCLIArgs(argv []string) (config, []string) {
	c := config{port: 9222, headless: true}
	if v := os.Getenv("CLOAKSERVE_IDLE_TIMEOUT"); v != "" {
		c.idleTimeout = parseIdleTimeout(v)
	}
	var passthrough []string
	stripPrefixes := []string{"--remote-debugging-port=", "--remote-debugging-address="}

	for _, arg := range argv {
		switch {
		case strings.HasPrefix(arg, "--port="):
			c.port, _ = strconv.Atoi(arg[len("--port="):])
		case strings.HasPrefix(arg, "--data-dir="):
			c.dataDir = arg[len("--data-dir="):]
		case strings.HasPrefix(arg, "--idle-timeout="):
			c.idleTimeout = parseIdleTimeout(arg[len("--idle-timeout="):])
		case arg == "--headless=false" || arg == "--headless=False":
			c.headless = false
			passthrough = append(passthrough, arg)
		case hasAnyPrefix(arg, stripPrefixes):
			// strip silently
		case strings.HasPrefix(arg, "--fingerprint-locale="):
			c.defaultLocale = arg[len("--fingerprint-locale="):]
		case strings.HasPrefix(arg, "--fingerprint-timezone="):
			c.defaultTimezone = arg[len("--fingerprint-timezone="):]
		case strings.HasPrefix(arg, "--fingerprint="):
			c.defaultSeed = arg[len("--fingerprint="):]
		default:
			passthrough = append(passthrough, arg)
		}
	}
	if c.dataDir == "" {
		c.dataDir = defaultDataDir()
	}
	return c, passthrough
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
