package cloakbrowser

import (
	"context"
	"fmt"
	"time"
)

// Playwright-style actionability checks, porting cloakbrowser/human/actionability.py.
//
// Before a humanized interaction, the target is verified: attached, visible,
// enabled, editable, and (after scrolling, at the actual click coordinates) that
// it actually receives pointer events (is not covered by an overlay). Each check
// retries with a [100,250,500,1000]ms backoff until the timeout elapses.

// ActionabilityError is the base for all actionability failures.
type ActionabilityError struct {
	Selector string
	Check    string
	Message  string
}

func (e *ActionabilityError) Error() string {
	return fmt.Sprintf("element %q failed %s check: %s", e.Selector, e.Check, e.Message)
}

func newActionabilityError(selector, check, message string) *ActionabilityError {
	return &ActionabilityError{Selector: selector, Check: check, Message: message}
}

func errNotAttached(sel string) *ActionabilityError {
	return newActionabilityError(sel, "attached", "element not found in DOM")
}
func errNotVisible(sel string) *ActionabilityError {
	return newActionabilityError(sel, "visible", "element is not visible")
}
func errNotStable(sel string) *ActionabilityError {
	return newActionabilityError(sel, "stable", "element position is still changing")
}
func errNotEnabled(sel string) *ActionabilityError {
	return newActionabilityError(sel, "enabled", "element is disabled")
}
func errNotEditable(sel string) *ActionabilityError {
	return newActionabilityError(sel, "editable", "element is not editable")
}
func errNotReceivingEvents(sel, covering string) *ActionabilityError {
	return newActionabilityError(sel, "pointer_events", fmt.Sprintf("element is covered by <%s>", covering))
}

// Check identifies a single actionability precondition.
type Check string

const (
	checkAttached      Check = "attached"
	checkVisible       Check = "visible"
	checkEnabled       Check = "enabled"
	checkEditable      Check = "editable"
	checkPointerEvents Check = "pointer_events"
)

// Check-set constants mirror the upstream CHECKS_* frozensets.
var (
	checksClick = map[Check]bool{checkAttached: true, checkVisible: true, checkEnabled: true, checkPointerEvents: true}
	checksHover = map[Check]bool{checkAttached: true, checkVisible: true, checkPointerEvents: true}
	checksInput = map[Check]bool{checkAttached: true, checkVisible: true, checkEnabled: true, checkEditable: true, checkPointerEvents: true}
	checksFocus = map[Check]bool{checkAttached: true, checkVisible: true, checkEnabled: true}
	checksCheck = map[Check]bool{checkAttached: true, checkVisible: true, checkEnabled: true, checkPointerEvents: true}
)

var backoffMs = []int{100, 250, 500, 1000}

func backoffSleep(ctx context.Context, attempt int) {
	idx := attempt
	if idx >= len(backoffMs) {
		idx = len(backoffMs) - 1
	}
	sleepMs(ctx, float64(backoffMs[idx]))
}

// isEnabled reports whether the element matching selector is enabled (not
// disabled, not aria-disabled).
func (p *Page) isEnabled(ctx context.Context, selector string) bool {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		if (el.disabled) return false;
		if (el.getAttribute('aria-disabled') === 'true') return false;
		let n = el;
		while (n) {
			if (n.disabled) return false;
			n = n.parentElement;
		}
		return true;
	})()`
	var v bool
	_ = p.EvaluateIsolated(ctx, expr, &v)
	return v
}

// isEditable reports whether the element matching selector is editable.
func (p *Page) isEditable(ctx context.Context, selector string) bool {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		if (el.disabled || el.readOnly) return false;
		const tag = el.tagName.toLowerCase();
		if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
		return el.isContentEditable === true;
	})()`
	var v bool
	_ = p.EvaluateIsolated(ctx, expr, &v)
	return v
}

// ensureActionable waits for the element to pass the given pre-scroll checks
// (attached/visible/enabled/editable) with retry backoff. force skips all checks.
func (p *Page) ensureActionable(ctx context.Context, selector string, checks map[Check]bool, timeout time.Duration, force bool) error {
	if force {
		return nil
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	attempt := 0
	var lastErr *ActionabilityError

	for {
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return newActionabilityError(selector, "timeout", "timeout expired before first check")
		}

		err := func() *ActionabilityError {
			if checks[checkAttached] {
				var exists bool
				_ = p.EvaluateIsolated(ctx, "!!document.querySelector("+jsonString(selector)+")", &exists)
				if !exists {
					return errNotAttached(selector)
				}
			}
			if checks[checkVisible] {
				if v, _ := p.IsVisible(ctx, selector); !v {
					return errNotVisible(selector)
				}
			}
			if checks[checkEnabled] {
				if !p.isEnabled(ctx, selector) {
					return errNotEnabled(selector)
				}
			}
			if checks[checkEditable] {
				if !p.isEditable(ctx, selector) {
					return errNotEditable(selector)
				}
			}
			return nil
		}()

		if err == nil {
			return nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return newActionabilityError(selector, "cancelled", ctx.Err().Error())
		default:
		}
		backoffSleep(ctx, attempt)
		attempt++
	}
}

// boxesDiffer reports whether two boxes differ by more than 1px on any axis.
func boxesDiffer(a, b *BoundingBox) bool {
	return absF(a.X-b.X) > 1 || absF(a.Y-b.Y) > 1 ||
		absF(a.Width-b.Width) > 1 || absF(a.Height-b.Height) > 1
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ensureStable waits for the element's position to settle (two samples 100ms
// apart). Only meaningful after a scroll.
func (p *Page) ensureStable(ctx context.Context, selector string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	attempt := 0
	for {
		if !time.Now().Before(deadline) {
			return errNotStable(selector)
		}
		box1, _ := p.BoundingBox(ctx, selector)
		if box1 == nil {
			return errNotAttached(selector)
		}
		sleepMs(ctx, 100)
		box2, _ := p.BoundingBox(ctx, selector)
		if box2 == nil {
			return errNotAttached(selector)
		}
		if !boxesDiffer(box1, box2) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errNotStable(selector)
		}
		backoffSleep(ctx, attempt)
		attempt++
	}
}

// pointerEventsJS checks that elementFromPoint(x,y) hits the expected element.
const pointerEventsJS = `(() => {
	const el = document.querySelector(%s);
	if (!el) return {hit: false, covering: 'none'};
	const target = document.elementFromPoint(%f, %f);
	if (!target) return {hit: false, reason: 'no_element_at_point', covering: 'none'};
	let node = target;
	while (node) { if (node === el) return {hit: true}; node = node.parentNode; }
	if (el.contains(target)) return {hit: true};
	return {hit: false, reason: 'covered', covering: target.tagName || 'unknown'};
})()`

// checkPointerEvents verifies that a click at (x,y) would actually land on the
// element (not on an overlay covering it). Retries with backoff. Fails open: if
// the check can't be determined it proceeds rather than blocking a legit click.
func (p *Page) checkPointerEvents(ctx context.Context, selector string, x, y float64, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	attempt := 0
	for {
		var res *struct {
			Hit      bool   `json:"hit"`
			Covering string `json:"covering"`
		}
		expr := fmt.Sprintf(pointerEventsJS, jsonString(selector), x, y)
		err := p.EvaluateIsolated(ctx, expr, &res)

		// Proceed on hit OR if indeterminate (err / nil result) — fail open.
		if err != nil || res == nil || res.Hit {
			return nil
		}
		covering := res.Covering
		if covering == "" {
			covering = "unknown"
		}
		if !time.Now().Before(deadline) {
			return errNotReceivingEvents(selector, covering)
		}
		backoffSleep(ctx, attempt)
		attempt++
	}
}
