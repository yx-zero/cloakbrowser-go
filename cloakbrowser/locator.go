package cloakbrowser

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// Locator is a lazy handle to one or more elements matching a CSS selector,
// loosely mirroring Playwright's Locator. Unlike a raw selector string it can
// address the Nth match and exposes the same humanize + actionability-gated
// interactions as the Page (Click/Fill/Type/Hover/Check/...), all routed back
// through the owning Page so behavior is identical.
//
// Resolution strategy: when an index is set (or to disambiguate dynamic DOMs),
// the locator tags its target element with a unique data attribute and operates
// on that attribute selector, so every existing Page method works unchanged.
type Locator struct {
	page     *Page
	selector string
	index    int // -1 = no specific index (first match)
}

var locatorCounter int64

// Locator returns a Locator for the given CSS selector (first match).
func (p *Page) Locator(selector string) *Locator {
	return &Locator{page: p, selector: selector, index: -1}
}

// Nth returns a Locator scoped to the element at the given index.
func (l *Locator) Nth(i int) *Locator {
	return &Locator{page: l.page, selector: l.selector, index: i}
}

// First returns a Locator scoped to the first match.
func (l *Locator) First() *Locator { return l.Nth(0) }

// Selector returns the underlying CSS selector.
func (l *Locator) Selector() string { return l.selector }

// resolve returns a concrete selector string the Page methods can act on. When
// no specific index is needed it is the raw selector; otherwise it tags the Nth
// match with a unique attribute and returns an attribute selector. The returned
// cleanup func removes the tag (safe to call even on the no-op path).
func (l *Locator) resolve(ctx context.Context) (string, func(), error) {
	if l.index < 0 {
		return l.selector, func() {}, nil
	}
	id := atomic.AddInt64(&locatorCounter, 1)
	attr := fmt.Sprintf("data-cloak-loc-%d", id)
	expr := fmt.Sprintf(`(() => {
		const els = document.querySelectorAll(%s);
		const el = els[%d];
		if (!el) return false;
		el.setAttribute(%q, "1");
		return true;
	})()`, jsonString(l.selector), l.index, attr)
	var ok bool
	if err := l.page.EvaluateIsolated(ctx, expr, &ok); err != nil {
		return "", func() {}, err
	}
	if !ok {
		return "", func() {}, fmt.Errorf("locator %q has no element at index %d", l.selector, l.index)
	}
	tagged := fmt.Sprintf("[%s]", attr)
	cleanup := func() {
		clean := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (el) el.removeAttribute(%q);
		})()`, tagged, attr)
		_ = l.page.EvaluateIsolated(ctx, clean, nil)
	}
	return tagged, cleanup, nil
}

// withSelector resolves the locator and runs fn with the concrete selector.
func (l *Locator) withSelector(ctx context.Context, fn func(sel string) error) error {
	sel, cleanup, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(sel)
}

// ---------------------------------------------------------------------------
// Interactions (delegate to Page, identical humanize + actionability behavior)
// ---------------------------------------------------------------------------

// Click clicks the located element.
func (l *Locator) Click(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Click(ctx, sel, opts) })
}

// DblClick double-clicks the located element.
func (l *Locator) DblClick(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.DblClick(ctx, sel, opts) })
}

// Tap taps the located element.
func (l *Locator) Tap(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Tap(ctx, sel, opts) })
}

// Hover hovers the located element.
func (l *Locator) Hover(ctx context.Context, opts HoverOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Hover(ctx, sel, opts) })
}

// Fill sets the value of the located field.
func (l *Locator) Fill(ctx context.Context, value string, opts FillOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Fill(ctx, sel, value, opts) })
}

// Type types text into the located field.
func (l *Locator) Type(ctx context.Context, text string, opts TypeOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Type(ctx, sel, text, opts) })
}

// Clear clears the located field.
func (l *Locator) Clear(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Clear(ctx, sel, opts) })
}

// Check checks the located checkbox/radio.
func (l *Locator) Check(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Check(ctx, sel, opts) })
}

// Uncheck unchecks the located checkbox/radio.
func (l *Locator) Uncheck(ctx context.Context, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.Uncheck(ctx, sel, opts) })
}

// SetChecked sets the located checkbox/radio to checked.
func (l *Locator) SetChecked(ctx context.Context, checked bool, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.SetChecked(ctx, sel, checked, opts) })
}

// SelectOption selects option(s) in the located <select>.
func (l *Locator) SelectOption(ctx context.Context, values []string, opts ClickOptions) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.SelectOption(ctx, sel, values, opts) })
}

// ScrollIntoView scrolls the located element into view.
func (l *Locator) ScrollIntoView(ctx context.Context) error {
	return l.withSelector(ctx, func(sel string) error { return l.page.ScrollIntoView(ctx, sel) })
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// BoundingBox returns the located element's bounding box.
func (l *Locator) BoundingBox(ctx context.Context) (*BoundingBox, error) {
	var box *BoundingBox
	err := l.withSelector(ctx, func(sel string) error {
		b, e := l.page.BoundingBox(ctx, sel)
		box = b
		return e
	})
	return box, err
}

// TextContent returns the located element's textContent.
func (l *Locator) TextContent(ctx context.Context) (string, error) {
	var out string
	err := l.withSelector(ctx, func(sel string) error {
		t, e := l.page.TextContent(ctx, sel)
		out = t
		return e
	})
	return out, err
}

// InnerText returns the located element's innerText.
func (l *Locator) InnerText(ctx context.Context) (string, error) {
	var out string
	err := l.withSelector(ctx, func(sel string) error {
		t, e := l.page.InnerText(ctx, sel)
		out = t
		return e
	})
	return out, err
}

// GetAttribute returns the located element's attribute value.
func (l *Locator) GetAttribute(ctx context.Context, name string) (string, bool, error) {
	var val string
	var ok bool
	err := l.withSelector(ctx, func(sel string) error {
		v, o, e := l.page.GetAttribute(ctx, sel, name)
		val, ok = v, o
		return e
	})
	return val, ok, err
}

// IsVisible reports whether the located element is visible.
func (l *Locator) IsVisible(ctx context.Context) (bool, error) {
	var v bool
	err := l.withSelector(ctx, func(sel string) error {
		b, e := l.page.IsVisible(ctx, sel)
		v = b
		return e
	})
	return v, err
}

// IsChecked reports whether the located checkbox/radio is checked.
func (l *Locator) IsChecked(ctx context.Context) (bool, error) {
	var v bool
	err := l.withSelector(ctx, func(sel string) error {
		b, e := l.page.IsChecked(ctx, sel)
		v = b
		return e
	})
	return v, err
}

// IsEnabled reports whether the located element is enabled.
func (l *Locator) IsEnabled(ctx context.Context) (bool, error) {
	var v bool
	err := l.withSelector(ctx, func(sel string) error {
		v = l.page.isEnabled(ctx, sel)
		return nil
	})
	return v, err
}

// IsEditable reports whether the located element is editable.
func (l *Locator) IsEditable(ctx context.Context) (bool, error) {
	var v bool
	err := l.withSelector(ctx, func(sel string) error {
		v = l.page.isEditable(ctx, sel)
		return nil
	})
	return v, err
}

// Count returns the number of elements matching the selector.
func (l *Locator) Count(ctx context.Context) (int, error) {
	var n int
	err := l.page.EvaluateIsolated(ctx, "document.querySelectorAll("+jsonString(l.selector)+").length", &n)
	return n, err
}

// WaitFor waits until the located element is attached to the DOM.
func (l *Locator) WaitFor(ctx context.Context, timeout time.Duration) error {
	return l.page.WaitForSelector(ctx, l.selector, timeout)
}

// Evaluate runs a JS function with the located element as the first argument.
// The expression must be a function literal, e.g. "el => el.value".
func (l *Locator) Evaluate(ctx context.Context, fnExpr string, out any) error {
	return l.withSelector(ctx, func(sel string) error {
		expr := "(" + fnExpr + ")(document.querySelector(" + jsonString(sel) + "))"
		return l.page.EvaluateIsolated(ctx, expr, out)
	})
}
