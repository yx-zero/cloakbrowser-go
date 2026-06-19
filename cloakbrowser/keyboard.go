package cloakbrowser

import (
	"context"
)

// keyDefinition describes how to dispatch a key via CDP Input.dispatchKeyEvent.
type keyDefinition struct {
	Key                   string
	Code                  string
	Text                  string
	WindowsVirtualKeyCode int
}

// rawKeyMap maps common named keys / printable chars to CDP key definitions.
// For ordinary printable ASCII we synthesize a "char" event with text.
var namedKeys = map[string]keyDefinition{
	"Enter":      {Key: "Enter", Code: "Enter", Text: "\r", WindowsVirtualKeyCode: 13},
	"Backspace":  {Key: "Backspace", Code: "Backspace", WindowsVirtualKeyCode: 8},
	"Tab":        {Key: "Tab", Code: "Tab", Text: "\t", WindowsVirtualKeyCode: 9},
	"Delete":     {Key: "Delete", Code: "Delete", WindowsVirtualKeyCode: 46},
	"Escape":     {Key: "Escape", Code: "Escape", WindowsVirtualKeyCode: 27},
	"ArrowLeft":  {Key: "ArrowLeft", Code: "ArrowLeft", WindowsVirtualKeyCode: 37},
	"ArrowUp":    {Key: "ArrowUp", Code: "ArrowUp", WindowsVirtualKeyCode: 38},
	"ArrowRight": {Key: "ArrowRight", Code: "ArrowRight", WindowsVirtualKeyCode: 39},
	"ArrowDown":  {Key: "ArrowDown", Code: "ArrowDown", WindowsVirtualKeyCode: 40},
	"Home":       {Key: "Home", Code: "Home", WindowsVirtualKeyCode: 36},
	"End":        {Key: "End", Code: "End", WindowsVirtualKeyCode: 35},
	"Shift":      {Key: "Shift", Code: "ShiftLeft", WindowsVirtualKeyCode: 16},
	"Control":    {Key: "Control", Code: "ControlLeft", WindowsVirtualKeyCode: 17},
	"Alt":        {Key: "Alt", Code: "AltLeft", WindowsVirtualKeyCode: 18},
	"Meta":       {Key: "Meta", Code: "MetaLeft", WindowsVirtualKeyCode: 91},
	"Space":      {Key: " ", Code: "Space", Text: " ", WindowsVirtualKeyCode: 32},
}

// RawKeyboard issues low-level CDP Input.dispatchKeyEvent commands.
type RawKeyboard struct {
	p        *Page
	modifier int
}

// RawKeyboard returns a raw keyboard bound to this page.
func (p *Page) RawKeyboard() *RawKeyboard { return &RawKeyboard{p: p} }

func (k *RawKeyboard) resolve(key string) keyDefinition {
	if def, ok := namedKeys[key]; ok {
		return def
	}
	// Single printable char — derive a physical code + virtual key code so
	// Chromium recognizes accelerators (e.g. Control+A select-all).
	if len(key) == 1 {
		c := key[0]
		switch {
		case c >= 'a' && c <= 'z':
			return keyDefinition{Key: key, Code: "Key" + string(c-32), Text: key, WindowsVirtualKeyCode: int(c - 32)}
		case c >= 'A' && c <= 'Z':
			return keyDefinition{Key: key, Code: "Key" + key, Text: key, WindowsVirtualKeyCode: int(c)}
		case c >= '0' && c <= '9':
			return keyDefinition{Key: key, Code: "Digit" + key, Text: key, WindowsVirtualKeyCode: int(c)}
		}
	}
	return keyDefinition{Key: key, Text: key}
}

func (k *RawKeyboard) trackModifier(key string, down bool) {
	var bit int
	switch key {
	case "Alt":
		bit = 1
	case "Control":
		bit = 2
	case "Meta":
		bit = 4
	case "Shift":
		bit = 8
	default:
		return
	}
	if down {
		k.modifier |= bit
	} else {
		k.modifier &^= bit
	}
}

// Down dispatches a keyDown for the given key.
func (k *RawKeyboard) Down(ctx context.Context, key string) {
	k.trackModifier(key, true)
	def := k.resolve(key)
	params := map[string]any{
		"type":      "keyDown",
		"key":       def.Key,
		"modifiers": k.modifier,
	}
	if def.Code != "" {
		params["code"] = def.Code
	}
	if def.WindowsVirtualKeyCode != 0 {
		params["windowsVirtualKeyCode"] = def.WindowsVirtualKeyCode
	}
	// Only emit text for printable keys with no active non-shift modifiers.
	if def.Text != "" && (k.modifier == 0 || k.modifier == 8) {
		params["text"] = def.Text
		params["unmodifiedText"] = def.Text
	}
	_ = k.p.session.DispatchKeyEvent(ctx, params)
}

// Up dispatches a keyUp for the given key.
func (k *RawKeyboard) Up(ctx context.Context, key string) {
	k.trackModifier(key, false)
	def := k.resolve(key)
	params := map[string]any{
		"type":      "keyUp",
		"key":       def.Key,
		"modifiers": k.modifier,
	}
	if def.Code != "" {
		params["code"] = def.Code
	}
	if def.WindowsVirtualKeyCode != 0 {
		params["windowsVirtualKeyCode"] = def.WindowsVirtualKeyCode
	}
	_ = k.p.session.DispatchKeyEvent(ctx, params)
}

// InsertText inserts text as if pasted/IME-composed (a single char event). Used
// for non-ASCII characters and shift symbols where a keyDown/Up is awkward.
func (k *RawKeyboard) InsertText(ctx context.Context, text string) {
	_ = k.p.session.Send(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

// Type types each rune of text via down/up (printable) or insertText (non-ASCII).
func (k *RawKeyboard) Type(ctx context.Context, text string) {
	for _, r := range text {
		ch := string(r)
		if r > 127 {
			k.InsertText(ctx, ch)
			continue
		}
		k.Down(ctx, ch)
		k.Up(ctx, ch)
	}
}

// Modifiers returns the current active modifier bitmask.
func (k *RawKeyboard) Modifiers() int { return k.modifier }
