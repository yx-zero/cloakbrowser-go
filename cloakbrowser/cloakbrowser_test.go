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
