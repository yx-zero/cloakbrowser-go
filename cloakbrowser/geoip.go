package cloakbrowser

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	geoip2 "github.com/oschwald/geoip2-golang"
	xproxy "golang.org/x/net/proxy"
)

// GeoIP-based timezone and locale detection from a proxy IP.
//
// Downloads GeoLite2-City.mmdb (~70 MB) on first use, caches in
// ~/.cloakbrowser/geoip/. Background re-download after 30 days.

const (
	geoipDBURL          = "https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-City.mmdb"
	geoipDBFilename     = "GeoLite2-City.mmdb"
	geoipUpdateInterval = 30 * 24 * time.Hour
	defaultGeoIPTimeout = 5 * time.Second
	geoipTimeoutEnv     = "CLOAKBROWSER_GEOIP_TIMEOUT_SECONDS"
)

// countryLocaleMap maps an ISO country code to a BCP 47 locale.
var countryLocaleMap = map[string]string{
	"US": "en-US", "GB": "en-GB", "AU": "en-AU", "CA": "en-CA", "NZ": "en-NZ",
	"IE": "en-IE", "ZA": "en-ZA", "SG": "en-SG",
	"DE": "de-DE", "AT": "de-AT", "CH": "de-CH",
	"FR": "fr-FR", "BE": "fr-BE",
	"ES": "es-ES", "MX": "es-MX", "AR": "es-AR", "CO": "es-CO", "CL": "es-CL",
	"BR": "pt-BR", "PT": "pt-PT",
	"IT": "it-IT", "NL": "nl-NL",
	"JP": "ja-JP", "KR": "ko-KR", "CN": "zh-CN", "TW": "zh-TW", "HK": "zh-HK",
	"RU": "ru-RU", "UA": "uk-UA", "PL": "pl-PL", "CZ": "cs-CZ", "RO": "ro-RO",
	"IL": "he-IL", "TR": "tr-TR", "SA": "ar-SA", "AE": "ar-AE", "EG": "ar-EG",
	"IN": "hi-IN", "ID": "id-ID", "PH": "en-PH",
	"TH": "th-TH", "VN": "vi-VN", "MY": "ms-MY",
	"SE": "sv-SE", "NO": "nb-NO", "DK": "da-DK", "FI": "fi-FI",
	"GR": "el-GR", "HU": "hu-HU", "BG": "bg-BG",
}

// ipEchoURLs are fast, no-auth services that return just the IP.
var ipEchoURLs = []string{
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://ifconfig.me/ip",
}

// geoResult bundles the resolved timezone, locale and exit IP.
type geoResult struct {
	timezone string
	locale   string
	exitIP   string
}

// resolveProxyGeoWithIP resolves timezone, locale and exit IP from a proxy URL.
// Any field may be empty on failure (missing DB, lookup miss). Never panics.
func resolveProxyGeoWithIP(proxyURL string) geoResult {
	dbPath := ensureGeoIPDB()
	if dbPath == "" {
		return geoResult{}
	}

	timeout := geoIPTimeout()
	deadline := time.Now().Add(timeout)

	ip := resolveExitIP(proxyURL, timeout)
	if ip == "" && time.Now().Before(deadline) {
		ip = resolveProxyIP(proxyURL)
	}
	if ip == "" || !time.Now().Before(deadline) {
		if !time.Now().Before(deadline) {
			log.Printf("cloakbrowser: GeoIP resolution timed out after %.1fs; continuing without GeoIP", timeout.Seconds())
		}
		return geoResult{}
	}

	db, err := geoip2.Open(dbPath)
	if err != nil {
		log.Printf("cloakbrowser: GeoIP DB open failed: %v", err)
		return geoResult{exitIP: ip}
	}
	defer db.Close()

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return geoResult{exitIP: ip}
	}
	rec, err := db.City(parsed)
	if err != nil {
		log.Printf("cloakbrowser: GeoIP lookup failed for %s: %v", ip, err)
		return geoResult{exitIP: ip}
	}
	tz := rec.Location.TimeZone
	country := rec.Country.IsoCode
	locale := countryLocaleMap[country]
	return geoResult{timezone: tz, locale: locale, exitIP: ip}
}

// resolveProxyExitIP resolves only the proxy exit IP, bounded by the GeoIP timeout.
func resolveProxyExitIP(proxyURL string) string {
	return resolveExitIP(proxyURL, geoIPTimeout())
}

// ---------------------------------------------------------------------------
// Proxy IP resolution
// ---------------------------------------------------------------------------

func resolveProxyIP(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return host
	}
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0]
}

// proxyHTTPClient builds an http.Client that routes through the given proxy URL.
// Supports http/https/socks5/socks5h.
func proxyHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	u, err := url.Parse(ensureProxyScheme(proxyURL))
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if isSocksProxyStr(proxyURL) {
		var auth *xproxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: pass}
		}
		dialer, err := xproxy.SOCKS5("tcp", u.Host, auth, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cd, ok := dialer.(xproxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return dialer.Dial(network, addr)
		}
	} else {
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func resolveExitIP(proxyURL string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for _, echo := range ipEchoURLs {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ""
		}
		reqTimeout := 10 * time.Second
		if remaining < reqTimeout {
			reqTimeout = remaining
		}
		client, err := proxyHTTPClient(proxyURL, reqTimeout)
		if err != nil {
			log.Printf("cloakbrowser: proxy client setup failed: %v", err)
			return ""
		}
		resp, err := client.Get(echo)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode >= 400 {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	log.Printf("cloakbrowser: failed to discover exit IP through proxy")
	return ""
}

func geoIPTimeout() time.Duration {
	raw := os.Getenv(geoipTimeoutEnv)
	if raw == "" {
		return defaultGeoIPTimeout
	}
	secs, err := strconv.ParseFloat(raw, 64)
	if err != nil || secs < 0 {
		log.Printf("cloakbrowser: invalid %s=%q; using %.1fs", geoipTimeoutEnv, raw, defaultGeoIPTimeout.Seconds())
		return defaultGeoIPTimeout
	}
	return time.Duration(secs * float64(time.Second))
}

// ---------------------------------------------------------------------------
// GeoIP database management
// ---------------------------------------------------------------------------

func geoIPDir() string {
	return filepath.Join(CacheDir(), "geoip")
}

func ensureGeoIPDB() string {
	dbPath := filepath.Join(geoIPDir(), geoipDBFilename)
	if fi, err := os.Stat(dbPath); err == nil {
		maybeTriggerGeoIPUpdate(dbPath, fi.ModTime())
		return dbPath
	}
	if err := downloadGeoIPDB(dbPath); err != nil {
		log.Printf("cloakbrowser: failed to download GeoIP database: %v", err)
		return ""
	}
	return dbPath
}

func downloadGeoIPDB(dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	log.Printf("cloakbrowser: downloading GeoIP database (~70 MB) ...")

	tmp, err := os.CreateTemp(filepath.Dir(dest), "*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(geoipDBURL)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		tmp.Close()
		os.Remove(tmpPath)
		return errHTTP(resp.StatusCode)
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return err
	}
	log.Printf("cloakbrowser: GeoIP database ready: %s", dest)
	return nil
}

func maybeTriggerGeoIPUpdate(dbPath string, modTime time.Time) {
	if time.Since(modTime) < geoipUpdateInterval {
		return
	}
	go func() {
		defer func() { recover() }()
		_ = downloadGeoIPDB(dbPath)
	}()
}

type errHTTP int

func (e errHTTP) Error() string { return "HTTP " + strconv.Itoa(int(e)) }
