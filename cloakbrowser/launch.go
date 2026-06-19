package cloakbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// HumanPreset selects a humanize timing preset.
type HumanPreset string

const (
	// PresetDefault is normal human speed.
	PresetDefault HumanPreset = "default"
	// PresetCareful is slower and more deliberate.
	PresetCareful HumanPreset = "careful"
)

// LaunchOptions configures a browser launch. Zero value is a sensible default
// (headless, stealth args on).
type LaunchOptions struct {
	// Headless runs Chromium without a visible window (default true).
	Headless bool
	// Proxy is a raw proxy URL string (e.g. "http://user:pass@host:8080" or
	// "socks5://host:1080"). Use ProxyDict for a structured proxy.
	Proxy string
	// ProxyDict is a structured proxy; takes precedence over Proxy when Server is set.
	ProxyDict *Proxy
	// Args are additional Chromium CLI arguments.
	Args []string
	// StealthArgs includes the default stealth fingerprint args (default true).
	StealthArgs bool
	// Timezone is an IANA timezone (sets --fingerprint-timezone).
	Timezone string
	// Locale is a BCP 47 locale (sets --lang / --fingerprint-locale).
	Locale string
	// GeoIP auto-detects timezone/locale from the proxy IP.
	GeoIP bool
	// Backend selects the driver backend. Only "" / "playwright" (native) are
	// supported; "patchright" returns an error (no Go equivalent).
	Backend string
	// Humanize enables human-like mouse, keyboard and scroll behavior.
	Humanize bool
	// HumanPreset selects the humanize preset (default "default").
	HumanPreset HumanPreset
	// HumanConfig overrides individual humanize parameters.
	HumanConfig map[string]any
	// ExtensionPaths are Chrome extension paths to load.
	ExtensionPaths []string

	// headlessSet tracks whether Headless was explicitly provided.
	headlessSet bool
}

// WithHeadless explicitly sets the headless flag and marks it as provided so the
// default-true logic does not override it.
func (o LaunchOptions) WithHeadless(v bool) LaunchOptions {
	o.Headless = v
	o.headlessSet = true
	return o
}

// applyDefaults fills implicit defaults (headless on, stealth on).
func (o *LaunchOptions) applyDefaults() {
	if !o.headlessSet && !o.Headless {
		o.Headless = true
	}
	// StealthArgs defaults to true unless the caller built options as a literal
	// with StealthArgs:false. To preserve "default true" we treat a freshly
	// zero-value struct specially in Launch via NewLaunchOptions; see below.
}

// NewLaunchOptions returns LaunchOptions with all the upstream defaults applied
// (Headless: true, StealthArgs: true, HumanPreset: "default").
func NewLaunchOptions() LaunchOptions {
	return LaunchOptions{
		Headless:    true,
		headlessSet: true,
		StealthArgs: true,
		HumanPreset: PresetDefault,
	}
}

// proxyInputFrom derives the internal proxy representation from options.
func (o *LaunchOptions) proxyInputFrom() proxyInput {
	if o.ProxyDict != nil && o.ProxyDict.Server != "" {
		return proxyInput{isDict: true, dict: *o.ProxyDict}
	}
	if o.Proxy != "" {
		return proxyInput{str: o.Proxy}
	}
	return proxyInput{}
}

// resolveBackend validates the requested backend.
func resolveBackend(backend string) (string, error) {
	b := backend
	if b == "" {
		b = os.Getenv("CLOAKBROWSER_BACKEND")
	}
	if b == "" {
		b = "playwright"
	}
	switch b {
	case "playwright":
		return b, nil
	case "patchright":
		return "", fmt.Errorf("backend 'patchright' is not available in the pure-Go port; use the default native backend")
	default:
		return "", fmt.Errorf("unknown backend %q. Use the default native backend", b)
	}
}

// baseChromeArgs are flags the driver always adds (the subset of Playwright's
// defaults that matter for a clean automation session). We deliberately omit
// --enable-automation and --enable-unsafe-swiftshader (the IgnoreDefaultArgs).
var baseChromeArgs = []string{
	"--no-first-run",
	"--no-default-browser-check",
	"--disable-background-networking",
	"--disable-backgrounding-occluded-windows",
	"--disable-renderer-backgrounding",
	"--disable-background-timer-throttling",
	"--disable-popup-blocking",
	"--disable-prompt-on-repost",
	"--disable-hang-monitor",
	"--disable-sync",
	"--metrics-recording-only",
	"--no-service-autorun",
	"--password-store=basic",
	"--use-mock-keychain",
	"--remote-allow-origins=*",
}

// launchConfig is the resolved launch state shared by Launch variants.
type launchConfig struct {
	binaryPath  string
	chromeArgs  []string
	userDataDir string
	tempProfile bool
	headless    bool
	humanize    bool
	humanCfg    *resolvedHumanConfig
}

// buildLaunchConfig resolves binary, geoip, proxy, args and humanize config.
func buildLaunchConfig(o LaunchOptions, userDataDir string) (*launchConfig, error) {
	if _, err := resolveBackend(o.Backend); err != nil {
		return nil, err
	}

	binaryPath, err := EnsureBinary()
	if err != nil {
		return nil, err
	}

	pi := o.proxyInputFrom()
	timezone, locale, exitIP := maybeResolveGeoIP(o.GeoIP, pi, o.Timezone, o.Locale)

	args := append([]string(nil), o.Args...)
	args = resolveWebRTCArgs(args, pi)
	if exitIP != "" && !hasWebRTCIPArg(args) {
		args = append(args, "--fingerprint-webrtc-ip="+exitIP)
	}

	proxyArgs := resolveProxyArgs(pi)
	extra := append(args, proxyArgs...)

	chromeArgs := BuildArgs(o.StealthArgs, extra, timezone, locale, o.Headless, o.ExtensionPaths)

	cfg := &launchConfig{
		binaryPath: binaryPath,
		chromeArgs: chromeArgs,
		headless:   o.Headless,
		humanize:   o.Humanize,
	}

	if userDataDir != "" {
		cfg.userDataDir = userDataDir
		seedWidevineHint(userDataDir, binaryPath)
	} else {
		tmp, err := os.MkdirTemp("", "cloakbrowser-profile-")
		if err != nil {
			return nil, err
		}
		cfg.userDataDir = tmp
		cfg.tempProfile = true
	}

	if o.Humanize {
		preset := o.HumanPreset
		if preset == "" {
			preset = PresetDefault
		}
		hc, err := resolveHumanConfig(preset, o.HumanConfig)
		if err != nil {
			return nil, err
		}
		cfg.humanCfg = hc
	}

	return cfg, nil
}

// spawnChromium starts the patched Chromium and connects a CDP client.
func spawnChromium(ctx context.Context, cfg *launchConfig) (*Browser, error) {
	fullArgs := append([]string(nil), baseChromeArgs...)
	if cfg.headless {
		fullArgs = append(fullArgs, "--headless=new")
	}
	fullArgs = append(fullArgs, "--user-data-dir="+cfg.userDataDir)
	fullArgs = append(fullArgs, "--remote-debugging-port=0")
	fullArgs = append(fullArgs, cfg.chromeArgs...)
	fullArgs = append(fullArgs, "about:blank")

	cmd := exec.Command(cfg.binaryPath, fullArgs...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Chromium: %w", err)
	}

	wsURL, err := discoverWebSocketURL(cfg.userDataDir, stderr, 30*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}

	conn, err := cdp.Dial(ctx, wsURL)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}

	b := &Browser{
		conn:        conn,
		cmd:         cmd,
		userDataDir: cfg.userDataDir,
		tempProfile: cfg.tempProfile,
		humanize:    cfg.humanize,
		humanCfg:    cfg.humanCfg,
	}
	if err := conn.SetDiscoverTargets(ctx, true); err != nil {
		b.Close(ctx)
		return nil, err
	}
	return b, nil
}

// discoverWebSocketURL reads the DevToolsActivePort file (written by Chromium to
// the user-data-dir) and resolves the browser WebSocket debugger URL. It also
// drains stderr to avoid blocking the child process.
func discoverWebSocketURL(userDataDir string, stderr io.Reader, timeout time.Duration) (string, error) {
	go io.Copy(io.Discard, stderr)

	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
			if len(lines) >= 1 && lines[0] != "" {
				port := strings.TrimSpace(lines[0])
				if ws := fetchBrowserWSURL(port); ws != "" {
					return ws, nil
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for Chromium DevTools endpoint")
}

// fetchBrowserWSURL hits /json/version to get the webSocketDebuggerUrl.
func fetchBrowserWSURL(port string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/json/version")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	return v.WebSocketDebuggerURL
}

// Launch starts a stealth Chromium browser and returns a Browser. Call
// Browser.NewPage to open pages and Browser.Close when done.
//
// Pass NewLaunchOptions() and tweak fields, or a literal LaunchOptions. With a
// literal, remember StealthArgs defaults to false (Go zero value) — use
// NewLaunchOptions() to get the upstream default of true.
func Launch(ctx context.Context, opts LaunchOptions) (*Browser, error) {
	opts.applyDefaults()
	cfg, err := buildLaunchConfig(opts, "")
	if err != nil {
		return nil, err
	}
	return spawnChromium(ctx, cfg)
}

// LaunchContext launches a stealth browser and returns a fresh BrowserContext
// with common options pre-applied (viewport, user agent, etc. via NewContext).
func LaunchContext(ctx context.Context, opts LaunchOptions, ctxOpts ContextOptions) (*BrowserContext, error) {
	b, err := Launch(ctx, opts)
	if err != nil {
		return nil, err
	}
	bc, err := b.NewContext(ctx, ctxOpts)
	if err != nil {
		b.Close(ctx)
		return nil, err
	}
	bc.ownsBrowser = true
	return bc, nil
}

// LaunchPersistentContext launches a stealth browser with a persistent profile
// stored in userDataDir and returns its default BrowserContext.
func LaunchPersistentContext(ctx context.Context, userDataDir string, opts LaunchOptions, ctxOpts ContextOptions) (*BrowserContext, error) {
	opts.applyDefaults()
	if userDataDir == "" {
		return nil, fmt.Errorf("userDataDir must not be empty for a persistent context")
	}
	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, err
	}
	cfg, err := buildLaunchConfig(opts, userDataDir)
	if err != nil {
		return nil, err
	}
	b, err := spawnChromium(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// The default (persistent) context is the browser's first browser context.
	bc := b.defaultContext(ctxOpts)
	bc.ownsBrowser = true
	return bc, nil
}
