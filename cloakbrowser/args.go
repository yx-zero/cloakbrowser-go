package cloakbrowser

import (
	"log"
	"path/filepath"
	"runtime"
	"strings"
)

// Argument assembly — ports build_args, maybe_resolve_geoip and the WebRTC IP
// resolution from cloakbrowser/browser.py.

// flagKey returns everything before the first '=' in a Chrome flag.
func flagKey(arg string) string {
	if i := strings.Index(arg, "="); i >= 0 {
		return arg[:i]
	}
	return arg
}

// BuildArgs combines stealth args with user-provided args and locale/timezone
// flags, deduplicating by flag key. Priority: stealth defaults < user args <
// dedicated params (timezone/locale).
func BuildArgs(stealthArgs bool, extraArgs []string, timezone, locale string, headless bool, extensionPaths []string) []string {
	// Preserve insertion order while deduping by key.
	order := []string{}
	seen := map[string]string{}
	set := func(arg string) {
		key := flagKey(arg)
		if _, ok := seen[key]; !ok {
			order = append(order, key)
		}
		seen[key] = arg
	}

	if stealthArgs {
		for _, a := range GetDefaultStealthArgs() {
			set(a)
		}
	}

	// GPU blocklist bypass: headed (all platforms) or Windows (all modes).
	if !headless || runtime.GOOS == "windows" {
		set("--ignore-gpu-blocklist")
	}

	for _, a := range extraArgs {
		if prev, ok := seen[flagKey(a)]; ok && prev != a {
			log.Printf("cloakbrowser: arg override: %s -> %s", prev, a)
		}
		set(a)
	}

	if timezone != "" {
		set("--fingerprint-timezone=" + timezone)
	}
	if locale != "" {
		set("--lang=" + locale)
		set("--fingerprint-locale=" + locale)
	}

	if len(extensionPaths) > 0 {
		absPaths := make([]string, len(extensionPaths))
		for i, p := range extensionPaths {
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			absPaths[i] = abs
		}
		extVal := strings.Join(absPaths, ",")
		set("--load-extension=" + extVal)
		set("--disable-extensions-except=" + extVal)
	}

	out := make([]string, len(order))
	for i, key := range order {
		out[i] = seen[key]
	}
	return out
}

// maybeResolveGeoIP auto-fills timezone/locale from a proxy IP when geoip is
// enabled. Returns the (possibly updated) timezone, locale and the exit IP
// (a free bonus from the lookup, reused for WebRTC spoofing).
func maybeResolveGeoIP(geoip bool, pi proxyInput, timezone, locale string) (string, string, string) {
	if !geoip || pi.empty() {
		return timezone, locale, ""
	}
	proxyURL := pi.extractURL()
	if proxyURL == "" {
		return timezone, locale, ""
	}

	if timezone != "" && locale != "" {
		return timezone, locale, resolveProxyExitIP(proxyURL)
	}

	res := resolveProxyGeoWithIP(proxyURL)
	if timezone == "" {
		timezone = res.timezone
	}
	if locale == "" {
		locale = res.locale
	}
	return timezone, locale, res.exitIP
}

// resolveWebRTCArgs replaces --fingerprint-webrtc-ip=auto with the resolved
// proxy exit IP. Returns args unchanged if no auto value is present.
func resolveWebRTCArgs(args []string, pi proxyInput) []string {
	idx := -1
	for i, a := range args {
		if a == "--fingerprint-webrtc-ip=auto" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return args
	}
	proxyURL := pi.extractURL()
	out := append([]string(nil), args...)
	if proxyURL == "" {
		log.Printf("cloakbrowser: --fingerprint-webrtc-ip=auto requires a proxy; removing flag")
		return append(out[:idx], out[idx+1:]...)
	}
	exitIP := resolveProxyExitIP(proxyURL)
	if exitIP != "" {
		out[idx] = "--fingerprint-webrtc-ip=" + exitIP
		return out
	}
	log.Printf("cloakbrowser: could not resolve proxy exit IP for WebRTC spoofing; removing --fingerprint-webrtc-ip=auto")
	return append(out[:idx], out[idx+1:]...)
}

// hasWebRTCIPArg reports whether args already contains a --fingerprint-webrtc-ip flag.
func hasWebRTCIPArg(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--fingerprint-webrtc-ip") {
			return true
		}
	}
	return false
}
