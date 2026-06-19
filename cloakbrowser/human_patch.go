package cloakbrowser

import (
	"context"
	"fmt"
	"time"
)

// Humanize patching — wires human-like click/fill/type onto a Page.
//
// Equivalent to cloakbrowser/human.patch_page: it replaces the page's
// interaction primitives with implementations that move a Bézier-curved cursor,
// scroll elements into view smoothly, click with realistic timing, and type with
// per-character delays and occasional typos. DOM reads use the stealth isolated
// world (EvaluateIsolated), exactly like the upstream stealth path.

// patchPage installs humanized interaction functions on p.
func patchPage(p *Page, cfg *resolvedHumanConfig) {
	p.humanCfg = cfg

	p.clickFn = func(ctx context.Context, selector string, opts ClickOptions) error {
		c := cfg
		if len(opts.HumanConfig) > 0 {
			merged := *cfg
			applyHumanOverrides(&merged, opts.HumanConfig)
			c = &merged
		}
		return humanizedClick(ctx, p, selector, opts.Timeout, c)
	}

	p.fillFn = func(ctx context.Context, selector, value string, opts FillOptions) error {
		c := cfg
		if len(opts.HumanConfig) > 0 {
			merged := *cfg
			applyHumanOverrides(&merged, opts.HumanConfig)
			c = &merged
		}
		return humanizedFill(ctx, p, selector, value, opts.Timeout, c)
	}

	p.typeFn = func(ctx context.Context, selector, text string, opts TypeOptions) error {
		c := cfg
		if len(opts.HumanConfig) > 0 {
			merged := *cfg
			applyHumanOverrides(&merged, opts.HumanConfig)
			c = &merged
		}
		return humanizedType(ctx, p, selector, text, opts.Timeout, c)
	}
}

// initCursor lazily seeds the cursor at a believable starting point (as if the
// pointer came from the address bar area).
func (p *Page) initCursor(cfg *resolvedHumanConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cursor.initialized {
		return
	}
	p.cursor.x = randRange(cfg.InitialCursorX)
	p.cursor.y = randRange(cfg.InitialCursorY)
	p.cursor.initialized = true
}

// Idle performs human-like idle cursor drift for the given duration. No-op when
// humanize is disabled.
func (p *Page) Idle(ctx context.Context, seconds float64) {
	if p.humanCfg == nil {
		return
	}
	p.initCursor(p.humanCfg)
	rm := p.RawMouse()
	cx, cy := rm.Position()
	humanIdle(ctx, rm, seconds, cx, cy, p.humanCfg)
}

func resolveTimeout(t time.Duration) time.Duration {
	if t <= 0 {
		return DefaultTimeout
	}
	return t
}

func errNoBox(selector string) error {
	return fmt.Errorf("element %q has no bounding box", selector)
}

// humanizedClick scrolls to, moves to, and clicks the element.
func humanizedClick(ctx context.Context, p *Page, selector string, timeout time.Duration, cfg *resolvedHumanConfig) error {
	timeout = resolveTimeout(timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	p.initCursor(cfg)

	rm := p.RawMouse()
	cx, cy := rm.Position()

	getBox := func() *BoundingBox {
		b, _ := p.BoundingBox(ctx, selector)
		return b
	}
	box, ncx, ncy, _ := humanScrollIntoView(ctx, p, rm, getBox, cx, cy, cfg)
	if box == nil {
		var err error
		box, err = p.BoundingBox(ctx, selector)
		if err != nil || box == nil {
			return errNoBox(selector)
		}
		ncx, ncy = rm.Position()
	}

	isInput := p.isInputElement(ctx, selector)
	target := clickTarget(box, isInput, cfg)
	humanMove(ctx, rm, ncx, ncy, target.x, target.y, cfg)
	humanClick(ctx, rm, isInput, cfg)
	return nil
}

// humanizedFill clicks the field, selects all, deletes, then types the value.
func humanizedFill(ctx context.Context, p *Page, selector, value string, timeout time.Duration, cfg *resolvedHumanConfig) error {
	timeout = resolveTimeout(timeout)
	if err := humanizedClick(ctx, p, selector, timeout, cfg); err != nil {
		return err
	}
	rk := p.RawKeyboard()

	// Select-all + delete to clear existing content.
	rk.Down(ctx, "Control")
	rk.Down(ctx, "a")
	rk.Up(ctx, "a")
	rk.Up(ctx, "Control")
	sleepMs(ctx, randRange(cfg.KeyHold))
	rk.Down(ctx, "Delete")
	rk.Up(ctx, "Delete")

	humanType(ctx, rk, value, cfg)
	return nil
}

// humanizedType clicks the field (without clearing) then types text.
func humanizedType(ctx context.Context, p *Page, selector, text string, timeout time.Duration, cfg *resolvedHumanConfig) error {
	timeout = resolveTimeout(timeout)
	if !p.isSelectorFocused(ctx, selector) {
		if err := humanizedClick(ctx, p, selector, timeout, cfg); err != nil {
			return err
		}
	}
	rk := p.RawKeyboard()
	humanType(ctx, rk, text, cfg)
	return nil
}
