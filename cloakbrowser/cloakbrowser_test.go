package cloakbrowser

import (
	"reflect"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"146.0.7680.177.5", "145.0.7632.109.2", true},
		{"145.0.7632.109.2", "146.0.7680.177.5", false},
		{"146.0.7680.177.5", "146.0.7680.177.5", false},
		{"146.0.7680.178", "146.0.7680.177.5", true},
		{"146.0.7680.177.5", "146.0.7680.177", true}, // longer, extra .5 > absent
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestBuildArgsDedupAndOverride(t *testing.T) {
	// User arg should override a stealth default with the same key.
	args := BuildArgs(true, []string{"--no-sandbox", "--foo=bar"}, "", "", true, nil)

	// Collect into a map keyed by flag key.
	got := map[string]string{}
	for _, a := range args {
		got[flagKey(a)] = a
	}
	if got["--no-sandbox"] != "--no-sandbox" {
		t.Errorf("--no-sandbox missing: %v", args)
	}
	if got["--foo"] != "--foo=bar" {
		t.Errorf("--foo override missing: %v", args)
	}
	if got["--fingerprint-platform"] == "" {
		t.Errorf("stealth fingerprint-platform missing: %v", args)
	}
	// No duplicate keys.
	seen := map[string]bool{}
	for _, a := range args {
		k := flagKey(a)
		if seen[k] {
			t.Errorf("duplicate flag key %q in %v", k, args)
		}
		seen[k] = true
	}
}

func TestBuildArgsTimezoneLocale(t *testing.T) {
	args := BuildArgs(false, nil, "America/New_York", "en-US", true, nil)
	want := map[string]string{
		"--fingerprint-timezone": "--fingerprint-timezone=America/New_York",
		"--lang":                 "--lang=en-US",
		"--fingerprint-locale":   "--fingerprint-locale=en-US",
	}
	got := map[string]string{}
	for _, a := range args {
		got[flagKey(a)] = a
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("flag %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestBuildArgsExtensions(t *testing.T) {
	args := BuildArgs(false, nil, "", "", true, []string{"./ext-a", "./ext-b"})
	var foundLoad, foundExcept bool
	for _, a := range args {
		if flagKey(a) == "--load-extension" {
			foundLoad = true
		}
		if flagKey(a) == "--disable-extensions-except" {
			foundExcept = true
		}
	}
	if !foundLoad || !foundExcept {
		t.Errorf("extension flags missing: %v", args)
	}
}

func TestCountryLocaleMap(t *testing.T) {
	cases := map[string]string{"US": "en-US", "DE": "de-DE", "JP": "ja-JP", "BR": "pt-BR"}
	for country, want := range cases {
		if got := countryLocaleMap[country]; got != want {
			t.Errorf("countryLocaleMap[%q] = %q, want %q", country, got, want)
		}
	}
}

func TestPlatformTag(t *testing.T) {
	// Should resolve to a known tag for the current platform, or error.
	tag, err := PlatformTag()
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	if _, ok := PlatformChromiumVersions[tag]; !ok {
		t.Errorf("platform tag %q has no chromium version mapping", tag)
	}
}

func TestResolveHumanConfigPresets(t *testing.T) {
	def, err := resolveHumanConfig(PresetDefault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if def.TypingDelay != 70 {
		t.Errorf("default TypingDelay = %v, want 70", def.TypingDelay)
	}
	careful, err := resolveHumanConfig(PresetCareful, nil)
	if err != nil {
		t.Fatal(err)
	}
	if careful.TypingDelay != 100 {
		t.Errorf("careful TypingDelay = %v, want 100", careful.TypingDelay)
	}
	if !careful.IdleBetweenActions {
		t.Error("careful preset should enable IdleBetweenActions")
	}
	if _, err := resolveHumanConfig("bogus", nil); err == nil {
		t.Error("expected error for unknown preset")
	}
}

func TestResolveHumanConfigOverrides(t *testing.T) {
	cfg, err := resolveHumanConfig(PresetDefault, map[string]any{
		"typing_delay":         200.0,
		"key_hold":             []float64{5, 9},
		"mistype_chance":       0.0,
		"MouseMinSteps":        10,
		"idle_between_actions": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TypingDelay != 200 {
		t.Errorf("TypingDelay override = %v, want 200", cfg.TypingDelay)
	}
	if cfg.KeyHold != (rangeF{5, 9}) {
		t.Errorf("KeyHold override = %v, want {5 9}", cfg.KeyHold)
	}
	if cfg.MistypeChance != 0 {
		t.Errorf("MistypeChance override = %v, want 0", cfg.MistypeChance)
	}
	if cfg.MouseMinSteps != 10 {
		t.Errorf("MouseMinSteps override = %v, want 10", cfg.MouseMinSteps)
	}
	if !cfg.IdleBetweenActions {
		t.Error("idle_between_actions override not applied")
	}
}

func TestEaseInOut(t *testing.T) {
	if got := easeInOut(0); got != 0 {
		t.Errorf("easeInOut(0) = %v, want 0", got)
	}
	if got := easeInOut(1); got != 1 {
		t.Errorf("easeInOut(1) = %v, want 1", got)
	}
	mid := easeInOut(0.5)
	if mid < 0.49 || mid > 0.51 {
		t.Errorf("easeInOut(0.5) = %v, want ~0.5", mid)
	}
}

func TestBezierEndpoints(t *testing.T) {
	p0 := point{0, 0}
	p1 := point{10, 10}
	p2 := point{20, 0}
	p3 := point{30, 30}
	if got := bezier(p0, p1, p2, p3, 0); !reflect.DeepEqual(got, p0) {
		t.Errorf("bezier(t=0) = %v, want %v", got, p0)
	}
	if got := bezier(p0, p1, p2, p3, 1); !reflect.DeepEqual(got, p3) {
		t.Errorf("bezier(t=1) = %v, want %v", got, p3)
	}
}

func TestKeyboardResolve(t *testing.T) {
	rk := &RawKeyboard{}
	cases := []struct {
		in       string
		wantCode string
		wantVK   int
	}{
		{"a", "KeyA", 65},
		{"Z", "KeyZ", 90},
		{"5", "Digit5", 53},
		{"Enter", "Enter", 13},
		{"Backspace", "Backspace", 8},
	}
	for _, c := range cases {
		def := rk.resolve(c.in)
		if def.Code != c.wantCode {
			t.Errorf("resolve(%q).Code = %q, want %q", c.in, def.Code, c.wantCode)
		}
		if def.WindowsVirtualKeyCode != c.wantVK {
			t.Errorf("resolve(%q).VK = %d, want %d", c.in, def.WindowsVirtualKeyCode, c.wantVK)
		}
	}
}

func TestKeyboardModifierTracking(t *testing.T) {
	rk := &RawKeyboard{}
	rk.trackModifier("Control", true)
	if rk.Modifiers() != 2 {
		t.Errorf("Control modifier = %d, want 2", rk.Modifiers())
	}
	rk.trackModifier("Shift", true)
	if rk.Modifiers() != 10 { // 2 | 8
		t.Errorf("Control+Shift modifier = %d, want 10", rk.Modifiers())
	}
	rk.trackModifier("Control", false)
	if rk.Modifiers() != 8 {
		t.Errorf("after release Control = %d, want 8", rk.Modifiers())
	}
}

func TestSafeSeed(t *testing.T) {
	valid := []string{"12345", "abc-99", "seed_1", "A1b2C3"}
	for _, s := range valid {
		if !SafeSeed(s) {
			t.Errorf("SafeSeed(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "__default__", "has space", "bad/slash", "a@b"}
	for _, s := range invalid {
		if SafeSeed(s) {
			t.Errorf("SafeSeed(%q) = true, want false", s)
		}
	}
	// 128 chars ok, 129 not.
	long := make([]byte, 128)
	for i := range long {
		long[i] = 'a'
	}
	if !SafeSeed(string(long)) {
		t.Error("128-char seed should be valid")
	}
	if SafeSeed(string(long) + "a") {
		t.Error("129-char seed should be invalid")
	}
}

func TestBuildSeedArgs(t *testing.T) {
	args, tz, loc := BuildSeedArgs(SeedArgsOptions{
		Seed:     "12345",
		Timezone: "America/New_York",
		Locale:   "en-US",
		Headless: true,
	})
	if tz != "America/New_York" || loc != "en-US" {
		t.Errorf("tz/loc = %q/%q", tz, loc)
	}
	got := map[string]string{}
	for _, a := range args {
		got[flagKey(a)] = a
	}
	if got["--fingerprint"] != "--fingerprint=12345" {
		t.Errorf("fingerprint flag = %q", got["--fingerprint"])
	}
	if got["--fingerprint-timezone"] != "--fingerprint-timezone=America/New_York" {
		t.Errorf("timezone flag = %q", got["--fingerprint-timezone"])
	}
	if got["--lang"] != "--lang=en-US" {
		t.Errorf("lang flag = %q", got["--lang"])
	}
}

func TestBuildSeedArgsWithSocksProxy(t *testing.T) {
	args, _, _ := BuildSeedArgs(SeedArgsOptions{
		Seed:     "1",
		Proxy:    "socks5://user:p=ss@host:1080",
		Headless: true,
	})
	var found string
	for _, a := range args {
		if flagKey(a) == "--proxy-server" {
			found = a
		}
	}
	if found != "--proxy-server=socks5://user:p%3Dss@host:1080" {
		t.Errorf("proxy-server arg = %q (want encoded '=')", found)
	}
}

func TestBoxesDiffer(t *testing.T) {
	a := &BoundingBox{X: 10, Y: 10, Width: 100, Height: 20}
	b := &BoundingBox{X: 10.5, Y: 10, Width: 100, Height: 20} // <1px diff
	if boxesDiffer(a, b) {
		t.Error("0.5px difference should not count as differing")
	}
	c := &BoundingBox{X: 13, Y: 10, Width: 100, Height: 20} // 3px diff
	if !boxesDiffer(a, c) {
		t.Error("3px difference should count as differing")
	}
}

func TestURLMatches(t *testing.T) {
	cases := []struct {
		url, pattern string
		want         bool
	}{
		{"https://x.com/dashboard", "dashboard", true}, // substring
		{"https://x.com/dashboard", "/login", false},   // substring miss
		{"https://x.com/users/42", "https://x.com/users/*", true},
		{"https://x.com/users/42/edit", "https://x.com/users/*", true},
		{"https://x.com/posts/42", "https://x.com/users/*", false},
		{"https://x.com/a/b/c", "*/b/*", true},
		{"https://x.com/a/x/c", "*/b/*", false},
		{"https://x.com/end", "*end", true},
		{"https://x.com/end?q=1", "*end", false}, // anchored suffix
		{"anything", "", true},                   // empty pattern matches all
	}
	for _, c := range cases {
		if got := urlMatches(c.url, c.pattern); got != c.want {
			t.Errorf("urlMatches(%q, %q) = %v, want %v", c.url, c.pattern, got, c.want)
		}
	}
}

func TestTrimURL(t *testing.T) {
	cases := map[string]string{
		"https://a.com/x":       "https://a.com/x",
		"https://a.com/x?q=1":   "https://a.com/x",
		"https://a.com/x#frag":  "https://a.com/x",
		"https://a.com/x?q=1#f": "https://a.com/x",
	}
	for in, want := range cases {
		if got := trimURL(in); got != want {
			t.Errorf("trimURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContextViewportDefaults(t *testing.T) {
	// Headless: neither Viewport nor NoViewport set => emulate DefaultViewport.
	var headlessOpts ContextOptions
	headlessOpts.applyDefaults(true)
	if headlessOpts.NoViewport {
		t.Error("headless default should emulate a viewport, not set NoViewport")
	}
	if headlessOpts.Viewport == nil || *headlessOpts.Viewport != DefaultViewport {
		t.Errorf("headless default Viewport = %v, want %v", headlessOpts.Viewport, DefaultViewport)
	}

	// Headed: neither set => NoViewport (use real OS window, no impossible-window tell).
	var headedOpts ContextOptions
	headedOpts.applyDefaults(false)
	if !headedOpts.NoViewport {
		t.Error("headed default should set NoViewport to avoid the impossible-window tell")
	}
	if headedOpts.Viewport != nil {
		t.Errorf("headed default should not emulate a viewport, got %v", headedOpts.Viewport)
	}

	// Explicit Viewport is honored even when headed.
	custom := Viewport{Width: 800, Height: 600}
	explicitVP := ContextOptions{Viewport: &custom}
	explicitVP.applyDefaults(false)
	if explicitVP.NoViewport || explicitVP.Viewport == nil || *explicitVP.Viewport != custom {
		t.Errorf("explicit viewport not honored when headed: %+v", explicitVP)
	}

	// Explicit NoViewport is honored even when headless.
	explicitNo := ContextOptions{NoViewport: true}
	explicitNo.applyDefaults(true)
	if !explicitNo.NoViewport || explicitNo.Viewport != nil {
		t.Errorf("explicit NoViewport not honored when headless: %+v", explicitNo)
	}
}

func TestEffectiveFingerprintPlatform(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--fingerprint-platform=windows"}, "windows"},
		{[]string{"--fingerprint-platform=macOS"}, "macos"}, // lowercased
		{[]string{"--no-sandbox"}, ""},
		{nil, ""},
		// last one wins
		{[]string{"--fingerprint-platform=linux", "--fingerprint-platform=windows"}, "windows"},
	}
	for _, c := range cases {
		if got := effectiveFingerprintPlatform(c.args); got != c.want {
			t.Errorf("effectiveFingerprintPlatform(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestFontWarningSuppressed(t *testing.T) {
	const env = "CLOAKBROWSER_SUPPRESS_FONT_WARNING"
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"off":   false,
		"no":    false,
		"1":     true,
		"true":  true,
		"yes":   true,
	}
	for val, want := range cases {
		t.Setenv(env, val)
		if got := fontWarningSuppressed(); got != want {
			t.Errorf("fontWarningSuppressed() with %s=%q = %v, want %v", env, val, got, want)
		}
	}
}

func TestFontFamilyPresent(t *testing.T) {
	stems := map[string]bool{"segoeui": true, "arial": true}
	if !fontFamilyPresent(fontFamily{"Segoe UI", []string{"segoeui"}}, stems) {
		t.Error("Segoe UI should be detected present")
	}
	if fontFamilyPresent(fontFamily{"Calibri", []string{"calibri"}}, stems) {
		t.Error("Calibri should be detected absent")
	}
}
