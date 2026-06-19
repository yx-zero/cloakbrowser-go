package cloakbrowser

import (
	"log"
	"net/url"
	"strings"
)

// Proxy resolution — ports the proxy helpers from cloakbrowser/browser.py.
//
// Proxies with credentials (SOCKS5 or HTTP/HTTPS) are passed via Chrome's
// --proxy-server flag with inline credentials, bypassing the auth interceptor
// that breaks on some proxies and Google domains. Since the native driver has
// no Playwright proxy dict, every proxy is expressed as Chrome args.

// Proxy is a Playwright-compatible proxy configuration. Either provide a raw URL
// string in LaunchOptions.Proxy, or build one of these and use ProxyDict.
type Proxy struct {
	Server   string
	Bypass   string
	Username string
	Password string
}

// ensureProxyScheme prepends http:// to schemeless proxy URLs so parsers can
// extract the hostname.
func ensureProxyScheme(proxyURL string) string {
	if strings.Contains(proxyURL, "://") {
		return proxyURL
	}
	return "http://" + proxyURL
}

// isSocksProxyStr reports whether a proxy URL uses the SOCKS5 protocol.
func isSocksProxyStr(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "socks5://") || strings.HasPrefix(l, "socks5h://")
}

// assembleProxyURL builds a proxy URL from already-percent-encoded credentials
// and host parts. encPass == nil means no password (no colon in userinfo);
// empty string means present-but-empty (colon preserved).
func assembleProxyURL(scheme, host, port, encUser string, encPass *string, path, rawQuery, fragment string) string {
	if strings.Contains(host, ":") { // IPv6 literal — re-add brackets
		host = "[" + host + "]"
	}
	var userinfo string
	if encPass != nil {
		userinfo = encUser + ":" + *encPass + "@"
	} else if encUser != "" {
		userinfo = encUser + "@"
	}
	netloc := userinfo + host
	if port != "" {
		netloc += ":" + port
	}
	u := scheme + "://" + netloc + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	if fragment != "" {
		u += "#" + fragment
	}
	return u
}

// pctEncode percent-encodes a userinfo component the way Python's
// urllib.parse.quote(s, safe="") does (encode everything non-unreserved).
func pctEncode(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// reEncodeCred decodes then re-encodes a credential so Chromium's parser doesn't
// truncate at special chars (mirrors quote(unquote(x), safe="")).
func reEncodeCred(raw string) string {
	if raw == "" {
		return ""
	}
	dec, err := url.QueryUnescape(raw)
	if err != nil {
		dec = raw
	}
	return pctEncode(dec)
}

// normalizeSocksStringURL re-encodes credentials in a SOCKS5 URL string so
// Chromium's parser doesn't truncate them at special chars like '='.
func normalizeSocksStringURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("cloakbrowser: malformed SOCKS5 proxy URL, passing through unchanged: %v", err)
		return rawURL
	}
	user := u.User
	if user == nil {
		return rawURL // no credentials at all
	}
	rawUser := user.Username()
	rawPass, hasPass := user.Password()

	encUser := ""
	if rawUser != "" {
		encUser = reEncodeCred(rawUser)
	}
	var encPass *string
	if hasPass {
		e := ""
		if rawPass != "" {
			e = reEncodeCred(rawPass)
		}
		encPass = &e
	}

	normalized := assembleProxyURL(u.Scheme, u.Hostname(), u.Port(), encUser, encPass,
		u.Path, u.RawQuery, u.Fragment)

	if encUser != rawUser || (encPass != nil && *encPass != rawPass) {
		log.Printf("cloakbrowser: auto URL-encoded SOCKS5 proxy credentials (special characters detected)")
	}
	return normalized
}

// normalizeHTTPStringURL re-encodes credentials in an HTTP(S) proxy URL string
// for --proxy-server.
func normalizeHTTPStringURL(rawURL string) string {
	normalized := rawURL
	if !strings.Contains(rawURL, "://") {
		normalized = "http://" + rawURL
	}
	u, err := url.Parse(normalized)
	if err != nil {
		log.Printf("cloakbrowser: malformed HTTP proxy URL, passing through unchanged: %v", err)
		return normalized
	}
	user := u.User
	if user == nil {
		return normalized
	}
	rawUser := user.Username()
	rawPass, hasPass := user.Password()

	encUser := ""
	if rawUser != "" {
		encUser = reEncodeCred(rawUser)
	}
	var encPass *string
	if hasPass {
		e := ""
		if rawPass != "" {
			e = reEncodeCred(rawPass)
		}
		encPass = &e
	}

	result := assembleProxyURL(u.Scheme, u.Hostname(), u.Port(), encUser, encPass,
		u.Path, u.RawQuery, u.Fragment)

	if encUser != rawUser || (encPass != nil && *encPass != rawPass) {
		log.Printf("cloakbrowser: auto URL-encoded HTTP proxy credentials (special characters detected)")
	}
	return result
}

// reconstructSocksURL builds a SOCKS5 URL with inline credentials from a Proxy.
func reconstructSocksURL(p Proxy) string {
	if p.Username == "" {
		return p.Server
	}
	u, err := url.Parse(p.Server)
	if err != nil {
		return p.Server
	}
	encUser := pctEncode(p.Username)
	var encPass *string
	if p.Password != "" {
		e := pctEncode(p.Password)
		encPass = &e
	}
	return assembleProxyURL(u.Scheme, u.Hostname(), u.Port(), encUser, encPass, u.Path, "", "")
}

// reconstructHTTPURL builds an HTTP(S) proxy URL with inline credentials from a Proxy.
func reconstructHTTPURL(p Proxy) string {
	if p.Username == "" {
		return p.Server
	}
	u, err := url.Parse(ensureProxyScheme(p.Server))
	if err != nil {
		return p.Server
	}
	encUser := pctEncode(p.Username)
	var encPass *string
	if p.Password != "" {
		e := pctEncode(p.Password)
		encPass = &e
	}
	return assembleProxyURL(u.Scheme, u.Hostname(), u.Port(), encUser, encPass, u.Path, "", "")
}

// proxyInput is the internal normalized form of a user-provided proxy.
type proxyInput struct {
	isDict bool
	str    string
	dict   Proxy
}

func (pi proxyInput) empty() bool {
	if pi.isDict {
		return pi.dict.Server == ""
	}
	return pi.str == ""
}

func (pi proxyInput) isSocks() bool {
	if pi.isDict {
		return isSocksProxyStr(pi.dict.Server)
	}
	return isSocksProxyStr(pi.str)
}

func (pi proxyInput) hasCredentials() bool {
	if pi.isDict {
		return pi.dict.Username != ""
	}
	return strings.Contains(pi.str, "@")
}

// extractProxyURL extracts and normalizes a proxy URL string from a proxy input.
func (pi proxyInput) extractURL() string {
	if pi.empty() {
		return ""
	}
	if pi.isDict {
		if pi.isSocks() {
			return reconstructSocksURL(pi.dict)
		}
		return ensureProxyScheme(pi.dict.Server)
	}
	return ensureProxyScheme(pi.str)
}

// resolveProxyArgs resolves a proxy input into the Chrome --proxy-server args.
//
// Unlike the Python wrapper (which can defer credential-less proxies to
// Playwright's proxy dict), the native driver passes every proxy via
// --proxy-server. Chrome handles HTTP and SOCKS5 auth natively from the URL.
func resolveProxyArgs(pi proxyInput) []string {
	if pi.empty() {
		return nil
	}

	if pi.isSocks() {
		if pi.isDict {
			args := []string{"--proxy-server=" + reconstructSocksURL(pi.dict)}
			if pi.dict.Bypass != "" {
				args = append(args, "--proxy-bypass-list="+pi.dict.Bypass)
			}
			return args
		}
		return []string{"--proxy-server=" + normalizeSocksStringURL(pi.str)}
	}

	// HTTP/HTTPS — with or without credentials, express via --proxy-server.
	if pi.isDict {
		var server string
		if pi.dict.Username != "" {
			server = reconstructHTTPURL(pi.dict)
		} else {
			server = ensureProxyScheme(pi.dict.Server)
		}
		args := []string{"--proxy-server=" + server}
		if pi.dict.Bypass != "" {
			args = append(args, "--proxy-bypass-list="+pi.dict.Bypass)
		}
		return args
	}
	if pi.hasCredentials() {
		return []string{"--proxy-server=" + normalizeHTTPStringURL(pi.str)}
	}
	return []string{"--proxy-server=" + ensureProxyScheme(pi.str)}
}
