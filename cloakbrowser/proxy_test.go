package cloakbrowser

import "testing"

func TestResolveProxyArgsHTTPWithCreds(t *testing.T) {
	pi := proxyInput{str: "http://user:pass@host:8080"}
	args := resolveProxyArgs(pi)
	if len(args) != 1 || args[0] != "--proxy-server=http://user:pass@host:8080" {
		t.Errorf("got %v", args)
	}
}

func TestResolveProxyArgsHTTPNoCreds(t *testing.T) {
	pi := proxyInput{str: "host:8080"}
	args := resolveProxyArgs(pi)
	if len(args) != 1 || args[0] != "--proxy-server=http://host:8080" {
		t.Errorf("got %v", args)
	}
}

func TestResolveProxyArgsSocks(t *testing.T) {
	pi := proxyInput{str: "socks5://user:pass@host:1080"}
	args := resolveProxyArgs(pi)
	if len(args) != 1 || args[0] != "--proxy-server=socks5://user:pass@host:1080" {
		t.Errorf("got %v", args)
	}
}

func TestResolveProxyArgsSocksSpecialChars(t *testing.T) {
	// Password with '=' must be percent-encoded so Chromium doesn't truncate it.
	pi := proxyInput{str: "socks5://user:p=ss@host:1080"}
	args := resolveProxyArgs(pi)
	if len(args) != 1 || args[0] != "--proxy-server=socks5://user:p%3Dss@host:1080" {
		t.Errorf("got %v, want encoded '='", args)
	}
}

func TestResolveProxyArgsDict(t *testing.T) {
	pi := proxyInput{isDict: true, dict: Proxy{
		Server:   "http://host:8080",
		Username: "u",
		Password: "p",
		Bypass:   ".google.com",
	}}
	args := resolveProxyArgs(pi)
	if len(args) != 2 {
		t.Fatalf("expected proxy-server + bypass, got %v", args)
	}
	if args[0] != "--proxy-server=http://u:p@host:8080" {
		t.Errorf("server arg = %q", args[0])
	}
	if args[1] != "--proxy-bypass-list=.google.com" {
		t.Errorf("bypass arg = %q", args[1])
	}
}

func TestResolveProxyArgsSocksDict(t *testing.T) {
	pi := proxyInput{isDict: true, dict: Proxy{
		Server:   "socks5://host:1080",
		Username: "u",
		Password: "p@ss",
	}}
	args := resolveProxyArgs(pi)
	if len(args) != 1 || args[0] != "--proxy-server=socks5://u:p%40ss@host:1080" {
		t.Errorf("got %v, want encoded '@'", args)
	}
}

func TestProxyInputHelpers(t *testing.T) {
	if (proxyInput{}).empty() != true {
		t.Error("empty proxyInput should be empty")
	}
	if !(proxyInput{str: "socks5://h:1"}).isSocks() {
		t.Error("should detect socks")
	}
	if !(proxyInput{str: "http://u:p@h:1"}).hasCredentials() {
		t.Error("should detect inline credentials")
	}
	if (proxyInput{str: "http://h:1"}).hasCredentials() {
		t.Error("should not detect credentials when absent")
	}
}

func TestPctEncode(t *testing.T) {
	cases := map[string]string{
		"abc":      "abc",
		"a b":      "a%20b",
		"p=ss":     "p%3Dss",
		"p@ss":     "p%40ss",
		"a-b_c.d~": "a-b_c.d~",
	}
	for in, want := range cases {
		if got := pctEncode(in); got != want {
			t.Errorf("pctEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractProxyURL(t *testing.T) {
	if got := (proxyInput{str: "host:8080"}).extractURL(); got != "http://host:8080" {
		t.Errorf("extractURL = %q", got)
	}
	if got := (proxyInput{str: "socks5://host:1080"}).extractURL(); got != "socks5://host:1080" {
		t.Errorf("extractURL socks = %q", got)
	}
}
