package cloakbrowser

import (
	"context"
	"fmt"
	"time"
)

// Additional Page interaction methods, porting the humanized actions from
// cloakbrowser/human/__init__.py (hover, dblclick, tap, check/uncheck,
// set_checked, select_option, clear, drag_to, press_sequentially) plus their
// non-humanized fallbacks. When humanize is enabled these route through the same
// Bézier-cursor / realistic-timing primitives used by Click/Fill/Type.

// HoverOptions configures Hover.
type HoverOptions struct {
	Timeout     time.Duration
	HumanConfig map[string]any
}

// humanCfgFor returns the effective humanize config for a per-call override map,
// or nil when humanize is disabled.
func (p *Page) humanCfgFor(overrides map[string]any) *resolvedHumanConfig {
	if p.humanCfg == nil {
		return nil
	}
	if len(overrides) == 0 {
		return p.humanCfg
	}
	merged := *p.humanCfg
	applyHumanOverrides(&merged, overrides)
	return &merged
}

// Hover moves the pointer over the element matching selector (no click).
func (p *Page) Hover(ctx context.Context, selector string, opts HoverOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	if cfg := p.humanCfgFor(opts.HumanConfig); cfg != nil {
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
		return nil
	}
	// Plain: move to element center.
	_ = p.scrollIntoView(ctx, selector)
	box, err := p.BoundingBox(ctx, selector)
	if err != nil {
		return err
	}
	if box == nil {
		return errNoBox(selector)
	}
	p.RawMouse().Move(ctx, box.X+box.Width/2, box.Y+box.Height/2)
	return nil
}

// DblClick double-clicks the element matching selector.
func (p *Page) DblClick(ctx context.Context, selector string, opts ClickOptions) error {
	if err := p.Click(ctx, selector, opts); err != nil {
		return err
	}
	if cfg := p.humanCfgFor(opts.HumanConfig); cfg != nil {
		sleepMs(ctx, randFloat(40, 120))
	}
	// Second click at the same spot — reuse the raw mouse position.
	rm := p.RawMouse()
	rm.Down(ctx)
	rm.Up(ctx)
	return nil
}

// Tap behaves like Click (touch tap maps to a click in this driver).
func (p *Page) Tap(ctx context.Context, selector string, opts ClickOptions) error {
	return p.Click(ctx, selector, opts)
}

// IsChecked reports whether a checkbox/radio matching selector is checked.
func (p *Page) IsChecked(ctx context.Context, selector string) (bool, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return null;
		return !!el.checked;
	})()`
	var v *bool
	if err := p.EvaluateIsolated(ctx, expr, &v); err != nil {
		return false, err
	}
	if v == nil {
		return false, fmt.Errorf("element %q not found", selector)
	}
	return *v, nil
}

// Check ensures the element matching selector is checked (clicks only if needed).
func (p *Page) Check(ctx context.Context, selector string, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	p.maybeIdleBetween(ctx, opts.HumanConfig)
	checked, err := p.IsChecked(ctx, selector)
	if err != nil {
		return err
	}
	if !checked {
		return p.Click(ctx, selector, opts)
	}
	return nil
}

// Uncheck ensures the element matching selector is unchecked.
func (p *Page) Uncheck(ctx context.Context, selector string, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	p.maybeIdleBetween(ctx, opts.HumanConfig)
	checked, err := p.IsChecked(ctx, selector)
	if err != nil {
		return err
	}
	if checked {
		return p.Click(ctx, selector, opts)
	}
	return nil
}

// SetChecked sets the checkbox/radio to the desired state (clicks if different).
func (p *Page) SetChecked(ctx context.Context, selector string, checked bool, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	current, err := p.IsChecked(ctx, selector)
	if err != nil {
		return err
	}
	if current != checked {
		return p.Click(ctx, selector, opts)
	}
	return nil
}

// maybeIdleBetween performs human idle drift between actions when the config
// opts in (mirrors the idle_between_actions behavior in check/uncheck).
func (p *Page) maybeIdleBetween(ctx context.Context, overrides map[string]any) {
	cfg := p.humanCfgFor(overrides)
	if cfg == nil || !cfg.IdleBetweenActions {
		return
	}
	p.initCursor(cfg)
	rm := p.RawMouse()
	cx, cy := rm.Position()
	humanIdle(ctx, rm, randRange(cfg.IdleBetweenDuration), cx, cy, cfg)
}

// SelectOption selects option(s) by value in a <select> matching selector.
// When humanize is enabled it hovers the element first, then sets the value.
func (p *Page) SelectOption(ctx context.Context, selector string, values []string, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	if cfg := p.humanCfgFor(opts.HumanConfig); cfg != nil {
		_ = p.Hover(ctx, selector, HoverOptions{Timeout: timeout, HumanConfig: opts.HumanConfig})
		sleepMs(ctx, randFloat(100, 300))
	}
	valuesJSON, _ := jsonMarshal(values)
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		const want = ` + valuesJSON + `;
		let changed = false;
		for (const opt of el.options) {
			const sel = want.includes(opt.value);
			if (opt.selected !== sel) { opt.selected = sel; changed = true; }
		}
		el.dispatchEvent(new Event('input', {bubbles: true}));
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return changed;
	})()`
	var ok bool
	return p.Evaluate(ctx, expr, &ok)
}

// Clear focuses the field, selects all and deletes its content.
func (p *Page) Clear(ctx context.Context, selector string, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	if !p.isSelectorFocused(ctx, selector) {
		if err := p.Click(ctx, selector, opts); err != nil {
			return err
		}
	}
	if cfg := p.humanCfgFor(opts.HumanConfig); cfg != nil {
		sleepMs(ctx, randFloat(50, 100))
	}
	rk := p.RawKeyboard()
	rk.Down(ctx, "Control")
	rk.Down(ctx, "a")
	rk.Up(ctx, "a")
	rk.Up(ctx, "Control")
	if cfg := p.humanCfgFor(opts.HumanConfig); cfg != nil {
		sleepMs(ctx, randFloat(30, 80))
	}
	rk.Down(ctx, "Backspace")
	rk.Up(ctx, "Backspace")
	return nil
}

// PressSequentially focuses the field then types text key-by-key (humanized
// when enabled). Unlike Fill, it does not clear existing content first.
func (p *Page) PressSequentially(ctx context.Context, selector, text string, opts TypeOptions) error {
	return p.Type(ctx, selector, text, opts)
}

// DragTo drags the source element to the center of the target element.
func (p *Page) DragTo(ctx context.Context, sourceSelector, targetSelector string, opts ClickOptions) error {
	timeout := resolveTimeout(opts.Timeout)
	if err := p.WaitForSelector(ctx, sourceSelector, timeout); err != nil {
		return err
	}
	if err := p.WaitForSelector(ctx, targetSelector, timeout); err != nil {
		return err
	}
	srcBox, err := p.BoundingBox(ctx, sourceSelector)
	if err != nil || srcBox == nil {
		return errNoBox(sourceSelector)
	}
	tgtBox, err := p.BoundingBox(ctx, targetSelector)
	if err != nil || tgtBox == nil {
		return errNoBox(targetSelector)
	}
	sx := srcBox.X + srcBox.Width/2
	sy := srcBox.Y + srcBox.Height/2
	tx := tgtBox.X + tgtBox.Width/2
	ty := tgtBox.Y + tgtBox.Height/2

	rm := p.RawMouse()
	cfg := p.humanCfgFor(opts.HumanConfig)
	if cfg != nil {
		cx, cy := rm.Position()
		humanMove(ctx, rm, cx, cy, sx, sy, cfg)
		sleepMs(ctx, randFloat(100, 200))
		rm.Down(ctx)
		sleepMs(ctx, randFloat(80, 150))
		humanMove(ctx, rm, sx, sy, tx, ty, cfg)
		sleepMs(ctx, randFloat(80, 150))
		rm.Up(ctx)
		return nil
	}
	rm.Move(ctx, sx, sy)
	rm.Down(ctx)
	rm.Move(ctx, tx, ty)
	rm.Up(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// DOM read helpers
// ---------------------------------------------------------------------------

// IsVisible reports whether the element matching selector is visible.
func (p *Page) IsVisible(ctx context.Context, selector string) (bool, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (style.visibility === 'hidden' || style.display === 'none' || parseFloat(style.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	})()`
	var v bool
	err := p.EvaluateIsolated(ctx, expr, &v)
	return v, err
}

// GetAttribute returns an element's attribute value, or "" + ok=false if absent.
func (p *Page) GetAttribute(ctx context.Context, selector, name string) (string, bool, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return null;
		const v = el.getAttribute(` + jsonString(name) + `);
		return v === null ? null : v;
	})()`
	var v *string
	if err := p.EvaluateIsolated(ctx, expr, &v); err != nil {
		return "", false, err
	}
	if v == nil {
		return "", false, nil
	}
	return *v, true, nil
}

// TextContent returns the textContent of the element matching selector.
func (p *Page) TextContent(ctx context.Context, selector string) (string, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		return el ? el.textContent : null;
	})()`
	var v *string
	if err := p.EvaluateIsolated(ctx, expr, &v); err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("element %q not found", selector)
	}
	return *v, nil
}

// InnerText returns the innerText of the element matching selector.
func (p *Page) InnerText(ctx context.Context, selector string) (string, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		return el ? el.innerText : null;
	})()`
	var v *string
	if err := p.EvaluateIsolated(ctx, expr, &v); err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("element %q not found", selector)
	}
	return *v, nil
}
