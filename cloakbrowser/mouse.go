package cloakbrowser

import (
	"context"
	"math"
)

// RawMouse issues low-level CDP Input.dispatchMouseEvent commands. All events
// are isTrusted=true (dispatched by the browser, not synthetic DOM events).
type RawMouse struct {
	p       *Page
	lastX   float64
	lastY   float64
	pressed bool
}

// RawMouse returns a raw mouse bound to this page. It tracks the last position
// so down/up/wheel report coherent coordinates.
func (p *Page) RawMouse() *RawMouse {
	p.mu.Lock()
	x, y := p.cursor.x, p.cursor.y
	p.mu.Unlock()
	return &RawMouse{p: p, lastX: x, lastY: y}
}

func (m *RawMouse) syncCursor() {
	m.p.mu.Lock()
	m.p.cursor.x = m.lastX
	m.p.cursor.y = m.lastY
	m.p.cursor.initialized = true
	m.p.mu.Unlock()
}

// Move moves the mouse to (x, y).
func (m *RawMouse) Move(ctx context.Context, x, y float64) {
	m.lastX, m.lastY = x, y
	button := "none"
	buttons := 0
	if m.pressed {
		button = "left"
		buttons = 1
	}
	_ = m.p.session.DispatchMouseEvent(ctx, map[string]any{
		"type":    "mouseMoved",
		"x":       round(x),
		"y":       round(y),
		"button":  button,
		"buttons": buttons,
	})
	m.syncCursor()
}

// Down presses the left mouse button at the current position.
func (m *RawMouse) Down(ctx context.Context) {
	m.pressed = true
	_ = m.p.session.DispatchMouseEvent(ctx, map[string]any{
		"type":       "mousePressed",
		"x":          round(m.lastX),
		"y":          round(m.lastY),
		"button":     "left",
		"buttons":    1,
		"clickCount": 1,
	})
}

// Up releases the left mouse button at the current position.
func (m *RawMouse) Up(ctx context.Context) {
	m.pressed = false
	_ = m.p.session.DispatchMouseEvent(ctx, map[string]any{
		"type":       "mouseReleased",
		"x":          round(m.lastX),
		"y":          round(m.lastY),
		"button":     "left",
		"buttons":    0,
		"clickCount": 1,
	})
}

// Wheel dispatches a mouse wheel scroll by (deltaX, deltaY).
func (m *RawMouse) Wheel(ctx context.Context, deltaX, deltaY float64) {
	_ = m.p.session.DispatchMouseEvent(ctx, map[string]any{
		"type":   "mouseWheel",
		"x":      round(m.lastX),
		"y":      round(m.lastY),
		"deltaX": deltaX,
		"deltaY": deltaY,
	})
}

// Position returns the mouse's last known position.
func (m *RawMouse) Position() (float64, float64) { return m.lastX, m.lastY }

func round(f float64) int { return int(math.Round(f)) }
