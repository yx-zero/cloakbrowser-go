package cloakbrowser

import (
	"context"
	"math/rand"
	"strings"
	"unicode"
)

// Human-like keyboard input — ports cloakbrowser/human/keyboard.py.
//
// Shift symbols are typed via CDP Input.dispatchKeyEvent (isTrusted=true, no
// evaluate stack trace), exactly as the stealth path in the Python wrapper.

var shiftSymbols = `@#!$%^&*()_+{}|:"<>?~`

var nearbyKeys = map[rune]string{
	'a': "sqwz", 'b': "vghn", 'c': "xdfv", 'd': "sfecx", 'e': "wrsdf",
	'f': "dgrtcv", 'g': "fhtyb", 'h': "gjybn", 'i': "ujko", 'j': "hkunm",
	'k': "jloi", 'l': "kop", 'm': "njk", 'n': "bhjm", 'o': "iklp",
	'p': "ol", 'q': "wa", 'r': "edft", 's': "awedxz", 't': "rfgy",
	'u': "yhji", 'v': "cfgb", 'w': "qase", 'x': "zsdc", 'y': "tghu",
	'z': "asx",
	'1': "2q", '2': "13qw", '3': "24we", '4': "35er", '5': "46rt",
	'6': "57ty", '7': "68yu", '8': "79ui", '9': "80io", '0': "9p",
}

// CDP code for each shift symbol's physical key.
var shiftSymbolCodes = map[rune]string{
	'!': "Digit1", '@': "Digit2", '#': "Digit3", '$': "Digit4",
	'%': "Digit5", '^': "Digit6", '&': "Digit7", '*': "Digit8",
	'(': "Digit9", ')': "Digit0", '_': "Minus", '+': "Equal",
	'{': "BracketLeft", '}': "BracketRight", '|': "Backslash",
	':': "Semicolon", '"': "Quote", '<': "Comma", '>': "Period",
	'?': "Slash", '~': "Backquote",
}

// Windows virtual key codes for Input.dispatchKeyEvent.
var shiftSymbolKeycodes = map[rune]int{
	'!': 49, '@': 50, '#': 51, '$': 52, '%': 53,
	'^': 54, '&': 55, '*': 56, '(': 57, ')': 48,
	'_': 189, '+': 187, '{': 219, '}': 221, '|': 220,
	':': 186, '"': 222, '<': 188, '>': 190, '?': 191,
	'~': 192,
}

func getNearbyKey(ch rune) rune {
	lower := unicode.ToLower(ch)
	if neighbors, ok := nearbyKeys[lower]; ok && len(neighbors) > 0 {
		wrong := rune(neighbors[rand.Intn(len(neighbors))])
		if unicode.IsUpper(ch) {
			return unicode.ToUpper(wrong)
		}
		return wrong
	}
	return ch
}

func isASCII(ch rune) bool { return ch < 128 }

// humanType types text with human-like per-character timing.
func humanType(ctx context.Context, rk *RawKeyboard, text string, cfg *resolvedHumanConfig) {
	runes := []rune(text)
	for i, ch := range runes {
		// Non-ASCII characters — insert directly.
		if !isASCII(ch) {
			sleepMs(ctx, randRange(cfg.KeyHold))
			rk.InsertText(ctx, string(ch))
			if i < len(runes)-1 {
				interCharDelay(ctx, cfg)
			}
			continue
		}

		// Mistype chance — only for ASCII alphanumeric.
		if rand.Float64() < cfg.MistypeChance && isAlnum(ch) {
			wrong := getNearbyKey(ch)
			typeNormalChar(ctx, rk, wrong, cfg)
			sleepMs(ctx, randRange(cfg.MistypeDelayNotice))
			rk.Down(ctx, "Backspace")
			sleepMs(ctx, randRange(cfg.KeyHold))
			rk.Up(ctx, "Backspace")
			sleepMs(ctx, randRange(cfg.MistypeDelayCorrect))
		}

		switch {
		case unicode.IsUpper(ch) && unicode.IsLetter(ch):
			typeShiftedChar(ctx, rk, ch, cfg)
		case strings.ContainsRune(shiftSymbols, ch):
			typeShiftSymbol(ctx, rk, ch, cfg)
		default:
			typeNormalChar(ctx, rk, ch, cfg)
		}

		if i < len(runes)-1 {
			interCharDelay(ctx, cfg)
		}
	}
}

func isAlnum(ch rune) bool { return unicode.IsLetter(ch) || unicode.IsDigit(ch) }

func typeNormalChar(ctx context.Context, rk *RawKeyboard, ch rune, cfg *resolvedHumanConfig) {
	rk.Down(ctx, string(ch))
	sleepMs(ctx, randRange(cfg.KeyHold))
	rk.Up(ctx, string(ch))
}

func typeShiftedChar(ctx context.Context, rk *RawKeyboard, ch rune, cfg *resolvedHumanConfig) {
	rk.Down(ctx, "Shift")
	sleepMs(ctx, randRange(cfg.ShiftDownDelay))
	rk.Down(ctx, string(ch))
	sleepMs(ctx, randRange(cfg.KeyHold))
	rk.Up(ctx, string(ch))
	sleepMs(ctx, randRange(cfg.ShiftUpDelay))
	rk.Up(ctx, "Shift")
}

// typeShiftSymbol types a shift symbol via CDP Input.dispatchKeyEvent with the
// Shift modifier — isTrusted=true, clean stack (the stealth path).
func typeShiftSymbol(ctx context.Context, rk *RawKeyboard, ch rune, cfg *resolvedHumanConfig) {
	code := shiftSymbolCodes[ch]
	keyCode := shiftSymbolKeycodes[ch]

	rk.Down(ctx, "Shift")
	sleepMs(ctx, randRange(cfg.ShiftDownDelay))

	_ = rk.p.session.DispatchKeyEvent(ctx, map[string]any{
		"type":                  "keyDown",
		"modifiers":             8, // Shift
		"key":                   string(ch),
		"code":                  code,
		"windowsVirtualKeyCode": keyCode,
		"text":                  string(ch),
		"unmodifiedText":        string(ch),
	})
	sleepMs(ctx, randRange(cfg.KeyHold))
	_ = rk.p.session.DispatchKeyEvent(ctx, map[string]any{
		"type":                  "keyUp",
		"modifiers":             8,
		"key":                   string(ch),
		"code":                  code,
		"windowsVirtualKeyCode": keyCode,
	})

	sleepMs(ctx, randRange(cfg.ShiftUpDelay))
	rk.Up(ctx, "Shift")
}

func interCharDelay(ctx context.Context, cfg *resolvedHumanConfig) {
	if rand.Float64() < cfg.TypingPauseChance {
		sleepMs(ctx, randRange(cfg.TypingPauseRange))
		return
	}
	delay := cfg.TypingDelay + (rand.Float64()-0.5)*2*cfg.TypingDelaySpread
	if delay < 10 {
		delay = 10
	}
	sleepMs(ctx, delay)
}
