package cloakbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// DefaultTimeout mirrors Playwright's 30s default for waits.
const DefaultTimeout = 30 * time.Second

// BoundingBox is an element's bounding rectangle in CSS pixels.
type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Page is a single tab driven over CDP.
type Page struct {
	ctxt     *BrowserContext
	targetID string
	session  *cdp.Session

	mu            sync.Mutex
	mainFrameID   string
	isolatedCtxID int
	closed        bool

	// Humanize hooks (nil when humanize disabled). Set by patchPage.
	humanCfg *resolvedHumanConfig
	cursor   cursorState
	clickFn  func(ctx context.Context, selector string, opts ClickOptions) error
	fillFn   func(ctx context.Context, selector, value string, opts FillOptions) error
	typeFn   func(ctx context.Context, selector, text string, opts TypeOptions) error
}

type cursorState struct {
	x, y        float64
	initialized bool
}

// Session exposes the underlying CDP session for advanced use.
func (p *Page) Session() *cdp.Session { return p.session }

// TargetID returns the CDP target id for this page.
func (p *Page) TargetID() string { return p.targetID }

func (p *Page) init(ctx context.Context) error {
	if err := p.session.PageEnable(ctx); err != nil {
		return err
	}
	if err := p.session.RuntimeEnable(ctx); err != nil {
		return err
	}
	if err := p.session.NetworkEnable(ctx); err != nil {
		return err
	}
	tree, err := p.session.GetFrameTree(ctx)
	if err != nil {
		return err
	}
	p.mainFrameID = tree.FrameTree.Frame.ID

	// Apply viewport emulation if requested.
	opts := p.ctxt.opts
	if opts.Viewport != nil && !opts.NoViewport {
		_ = p.session.SetDeviceMetricsOverride(ctx, opts.Viewport.Width, opts.Viewport.Height, false)
	}
	if opts.UserAgent != "" {
		_ = p.session.Send(ctx, "Emulation.setUserAgentOverride",
			map[string]any{"userAgent": opts.UserAgent}, nil)
	}

	// Invalidate the isolated world on navigation.
	p.session.On("Page.frameNavigated", func(params json.RawMessage) {
		var ev struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
			} `json:"frame"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Frame.ParentID == "" {
			p.mu.Lock()
			p.isolatedCtxID = 0
			p.mu.Unlock()
		}
	})
	return nil
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// Goto navigates to url and waits for the load event (or context timeout).
func (p *Page) Goto(ctx context.Context, url string) error {
	loaded := p.session.WaitEventChan(ctx, "Page.loadEventFired")
	defer loaded.Cancel()
	if err := p.session.Navigate(ctx, url); err != nil {
		return err
	}
	select {
	case <-loaded.Ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reload reloads the current page.
func (p *Page) Reload(ctx context.Context) error {
	loaded := p.session.WaitEventChan(ctx, "Page.loadEventFired")
	defer loaded.Cancel()
	if err := p.session.Send(ctx, "Page.reload", map[string]any{}, nil); err != nil {
		return err
	}
	select {
	case <-loaded.Ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

// Evaluate runs a JS expression in the main world and unmarshals its
// JSON-serializable result into out (which may be nil).
func (p *Page) Evaluate(ctx context.Context, expression string, out any) error {
	res, exc, err := p.session.Evaluate(ctx, expression, 0, true, true)
	if err != nil {
		return err
	}
	if exc != nil {
		return fmt.Errorf("evaluate exception: %s", exc.Text)
	}
	if out != nil && len(res.Value) > 0 {
		return json.Unmarshal(res.Value, out)
	}
	return nil
}

// EvaluateIsolated runs a JS expression in a stealth isolated world (clean stack
// traces, invisible to main-world monkey-patches). Used by the humanize layer.
func (p *Page) EvaluateIsolated(ctx context.Context, expression string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		ctxID, err := p.ensureIsolatedWorld(ctx)
		if err != nil {
			return err
		}
		res, exc, err := p.session.Evaluate(ctx, expression, ctxID, true, false)
		if err != nil {
			p.mu.Lock()
			p.isolatedCtxID = 0
			p.mu.Unlock()
			if attempt == 0 {
				continue
			}
			return err
		}
		if exc != nil {
			p.mu.Lock()
			p.isolatedCtxID = 0
			p.mu.Unlock()
			if attempt == 0 {
				continue
			}
			return fmt.Errorf("evaluate exception: %s", exc.Text)
		}
		if out != nil && len(res.Value) > 0 {
			return json.Unmarshal(res.Value, out)
		}
		return nil
	}
	return nil
}

func (p *Page) ensureIsolatedWorld(ctx context.Context) (int, error) {
	p.mu.Lock()
	if p.isolatedCtxID != 0 {
		id := p.isolatedCtxID
		p.mu.Unlock()
		return id, nil
	}
	frameID := p.mainFrameID
	p.mu.Unlock()

	if frameID == "" {
		tree, err := p.session.GetFrameTree(ctx)
		if err != nil {
			return 0, err
		}
		frameID = tree.FrameTree.Frame.ID
		p.mu.Lock()
		p.mainFrameID = frameID
		p.mu.Unlock()
	}
	id, err := p.session.CreateIsolatedWorld(ctx, frameID)
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.isolatedCtxID = id
	p.mu.Unlock()
	return id, nil
}

// ---------------------------------------------------------------------------
// Content / title / screenshot
// ---------------------------------------------------------------------------

// Content returns the full HTML of the document.
func (p *Page) Content(ctx context.Context) (string, error) {
	var html string
	err := p.Evaluate(ctx, "document.documentElement.outerHTML", &html)
	return html, err
}

// Title returns the document title.
func (p *Page) Title(ctx context.Context) (string, error) {
	var title string
	err := p.Evaluate(ctx, "document.title", &title)
	return title, err
}

// URL returns the current page URL.
func (p *Page) URL(ctx context.Context) (string, error) {
	var u string
	err := p.Evaluate(ctx, "document.location.href", &u)
	return u, err
}

// Screenshot captures a PNG screenshot. If path is non-empty the PNG is also
// written to disk. Returns the raw PNG bytes.
func (p *Page) Screenshot(ctx context.Context, path string, fullPage bool) ([]byte, error) {
	data, err := p.session.CaptureScreenshot(ctx, "png", fullPage)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return raw, err
		}
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// DOM queries & waits
// ---------------------------------------------------------------------------

// jsonString JSON-encodes a string for safe embedding in evaluated JS.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsonMarshal JSON-encodes any value for embedding in evaluated JS.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// WaitForSelector polls until an element matching selector exists (or timeout).
func (p *Page) WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	expr := "!!document.querySelector(" + jsonString(selector) + ")"
	for time.Now().Before(deadline) {
		var found bool
		if err := p.EvaluateIsolated(ctx, expr, &found); err == nil && found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout %s waiting for selector %q", timeout, selector)
}

// BoundingBox returns the bounding box of the first element matching selector,
// or nil if it has no box (not visible / not found).
func (p *Page) BoundingBox(ctx context.Context, selector string) (*BoundingBox, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return null;
		const r = el.getBoundingClientRect();
		if (r.width === 0 && r.height === 0) return null;
		return {x: r.x, y: r.y, width: r.width, height: r.height};
	})()`
	var box *BoundingBox
	if err := p.EvaluateIsolated(ctx, expr, &box); err != nil {
		return nil, err
	}
	return box, nil
}

// isInputElement reports whether selector is an input/textarea/contenteditable.
func (p *Page) isInputElement(ctx context.Context, selector string) bool {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		const tag = el.tagName.toLowerCase();
		return tag === 'input' || tag === 'textarea' || el.getAttribute('contenteditable') === 'true';
	})()`
	var v bool
	_ = p.EvaluateIsolated(ctx, expr, &v)
	return v
}

// isSelectorFocused reports whether the element matching selector is focused.
func (p *Page) isSelectorFocused(ctx context.Context, selector string) bool {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		return el === document.activeElement;
	})()`
	var v bool
	_ = p.EvaluateIsolated(ctx, expr, &v)
	return v
}

// ViewportSize returns the emulated viewport, or the window's inner size.
func (p *Page) ViewportSize(ctx context.Context) (Viewport, error) {
	if p.ctxt.opts.Viewport != nil && !p.ctxt.opts.NoViewport {
		return *p.ctxt.opts.Viewport, nil
	}
	var v Viewport
	err := p.Evaluate(ctx, "({width: window.innerWidth, height: window.innerHeight})", &v)
	return v, err
}

// ---------------------------------------------------------------------------
// Interactions (non-humanized defaults; humanize overrides via patchPage)
// ---------------------------------------------------------------------------

// ClickOptions configures Click.
type ClickOptions struct {
	Timeout     time.Duration
	HumanConfig map[string]any
}

// FillOptions configures Fill.
type FillOptions struct {
	Timeout     time.Duration
	HumanConfig map[string]any
}

// TypeOptions configures Type.
type TypeOptions struct {
	Timeout     time.Duration
	HumanConfig map[string]any
}

// Click clicks the element matching selector.
func (p *Page) Click(ctx context.Context, selector string, opts ClickOptions) error {
	if p.clickFn != nil {
		return p.clickFn(ctx, selector, opts)
	}
	return p.plainClick(ctx, selector, opts)
}

// plainClick performs a non-humanized click: scroll into view, move, press,
// release at the element center via raw CDP Input events.
func (p *Page) plainClick(ctx context.Context, selector string, opts ClickOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	_ = p.scrollIntoView(ctx, selector)
	box, err := p.BoundingBox(ctx, selector)
	if err != nil {
		return err
	}
	if box == nil {
		return fmt.Errorf("element %q has no bounding box", selector)
	}
	cx := box.X + box.Width/2
	cy := box.Y + box.Height/2
	rm := p.RawMouse()
	rm.Move(ctx, cx, cy)
	rm.Down(ctx)
	rm.Up(ctx)
	return nil
}

// Fill sets the value of an input/textarea matching selector.
func (p *Page) Fill(ctx context.Context, selector, value string, opts FillOptions) error {
	if p.fillFn != nil {
		return p.fillFn(ctx, selector, value, opts)
	}
	return p.plainFill(ctx, selector, value, opts)
}

func (p *Page) plainFill(ctx context.Context, selector, value string, opts FillOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return false;
		el.focus();
		const set = Object.getOwnPropertyDescriptor(el.__proto__, 'value');
		if (set && set.set) { set.set.call(el, ` + jsonString(value) + `); }
		else { el.value = ` + jsonString(value) + `; }
		el.dispatchEvent(new Event('input', {bubbles: true}));
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return true;
	})()`
	var ok bool
	return p.Evaluate(ctx, expr, &ok)
}

// Type types text into the element matching selector, character by character.
func (p *Page) Type(ctx context.Context, selector, text string, opts TypeOptions) error {
	if p.typeFn != nil {
		return p.typeFn(ctx, selector, text, opts)
	}
	return p.plainType(ctx, selector, text, opts)
}

func (p *Page) plainType(ctx context.Context, selector, text string, opts TypeOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := p.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	_ = p.Click(ctx, selector, ClickOptions{Timeout: timeout})
	rk := p.RawKeyboard()
	rk.Type(ctx, text)
	return nil
}

// Press dispatches a single key (e.g. "Enter", "Backspace").
func (p *Page) Press(ctx context.Context, key string) error {
	rk := p.RawKeyboard()
	rk.Down(ctx, key)
	rk.Up(ctx, key)
	return nil
}

// scrollIntoView scrolls the element matching selector into view (instant).
func (p *Page) scrollIntoView(ctx context.Context, selector string) error {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (el && el.scrollIntoView) { el.scrollIntoView({block: 'center', inline: 'center'}); return true; }
		return false;
	})()`
	var ok bool
	return p.EvaluateIsolated(ctx, expr, &ok)
}

// ScrollIntoView brings the element matching selector into view. When humanize
// is enabled it uses smooth accelerate/cruise/decelerate wheel dynamics;
// otherwise it does an instant scrollIntoView.
func (p *Page) ScrollIntoView(ctx context.Context, selector string) error {
	if p.humanCfg != nil {
		if err := p.WaitForSelector(ctx, selector, DefaultTimeout); err != nil {
			return err
		}
		p.initCursor(p.humanCfg)
		rm := p.RawMouse()
		cx, cy := rm.Position()
		getBox := func() *BoundingBox {
			b, _ := p.BoundingBox(ctx, selector)
			return b
		}
		humanScrollIntoView(ctx, p, rm, getBox, cx, cy, p.humanCfg)
		return nil
	}
	return p.scrollIntoView(ctx, selector)
}

// ---------------------------------------------------------------------------
// Cookies
// ---------------------------------------------------------------------------

// Cookies returns cookies visible to this page (optionally filtered by URL).
func (p *Page) Cookies(ctx context.Context, urls ...string) ([]cdp.Cookie, error) {
	return p.session.GetCookies(ctx, urls)
}

// AddCookies sets cookies for this page's context.
func (p *Page) AddCookies(ctx context.Context, cookies []cdp.Cookie) error {
	return p.session.SetCookies(ctx, cookies)
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Close closes this page.
func (p *Page) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	return p.ctxt.browser.conn.CloseTarget(ctx, p.targetID)
}
