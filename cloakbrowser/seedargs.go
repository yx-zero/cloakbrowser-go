package cloakbrowser

import (
	"fmt"
	"strings"
)

// SeedArgsOptions configures BuildSeedArgs for the cloakserve multiplexer.
type SeedArgsOptions struct {
	Seed      string   // fingerprint seed (e.g. "12345")
	ExtraArgs []string // generic --fingerprint-{name}={val} args from query params
	Timezone  string
	Locale    string
	Proxy     string // raw proxy URL (http/socks5)
	GeoIP     bool
	Headless  bool
}

// BuildSeedArgs assembles the full stealth Chrome argument list for one
// fingerprint seed, mirroring the per-connection logic in upstream cloakserve:
// fingerprint seed, generic fingerprint params, proxy (with re-encoded creds),
// GeoIP-derived timezone/locale + WebRTC exit IP, then the shared BuildArgs
// dedup. Returns the args plus the resolved timezone/locale (which GeoIP may
// have filled in).
func BuildSeedArgs(o SeedArgsOptions) (args []string, timezone, locale string) {
	timezone, locale = o.Timezone, o.Locale

	var exitIP string
	var pi proxyInput
	if o.Proxy != "" {
		pi = proxyInput{str: o.Proxy}
	}
	if o.GeoIP && o.Proxy != "" {
		timezone, locale, exitIP = maybeResolveGeoIP(true, pi, timezone, locale)
	}

	fpExtra := []string{fmt.Sprintf("--fingerprint=%s", o.Seed)}
	fpExtra = append(fpExtra, o.ExtraArgs...)
	if o.Proxy != "" {
		if isSocksProxyStr(o.Proxy) {
			fpExtra = append(fpExtra, "--proxy-server="+normalizeSocksStringURL(o.Proxy))
		} else {
			fpExtra = append(fpExtra, "--proxy-server="+normalizeHTTPStringURL(o.Proxy))
		}
	}

	fpExtra = resolveWebRTCArgs(fpExtra, pi)
	if exitIP != "" && !hasWebRTCIPArg(fpExtra) {
		fpExtra = append(fpExtra, "--fingerprint-webrtc-ip="+exitIP)
	}

	args = BuildArgs(true, fpExtra, timezone, locale, o.Headless, nil)
	warnMissingWindowsFonts(args)
	return args, timezone, locale
}

// SafeSeed reports whether a seed is a valid identifier (1-128 chars of
// [A-Za-z0-9_-]) and not a reserved key.
func SafeSeed(seed string) bool {
	if seed == "" || len(seed) > 128 {
		return false
	}
	if seed == "__default__" {
		return false
	}
	for _, r := range seed {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// IsSocksProxy reports whether a proxy URL uses SOCKS5 (exported for cloakserve).
func IsSocksProxy(u string) bool { return isSocksProxyStr(u) }

// NormalizeProxyURL re-encodes credentials in a proxy URL so Chromium's parser
// handles special characters (exported for cloakserve).
func NormalizeProxyURL(u string) string {
	if isSocksProxyStr(u) {
		return normalizeSocksStringURL(u)
	}
	if strings.Contains(u, "://") || strings.Contains(u, "@") {
		return normalizeHTTPStringURL(u)
	}
	return u
}
