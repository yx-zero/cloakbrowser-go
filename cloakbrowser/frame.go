package cloakbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// Frame represents a single frame (the main document or an <iframe>) within a
// Page. DOM queries, evaluation and interactions can be scoped to a frame so
// elements inside cross-origin iframes (embedded forms, widgets, challenges)
// are reachable — something the main-frame-only selector path cannot do.
//
// Each frame has its own CDP isolated world (execution context). Clicks inside
// a child frame are mapped from frame-local coordinates into page space using
// the iframe element's offset, so the humanize cursor lands correctly.
type Frame struct {
	page    *Page
	frameID string
	url     string
	name    string

	mu        sync.Mutex
	ctxID     int
	parentSel string // CSS selector that locates this frame's <iframe> in its parent (best-effort)
}

// MainFrame returns the page's top-level frame.
func (p *Page) MainFrame() *Frame {
	p.mu.Lock()
	fid := p.mainFrameID
	p.mu.Unlock()
	return &Frame{page: p, frameID: fid}
}

// Frames returns all frames in the page (main frame first, then descendants).
func (p *Page) Frames(ctx context.Context) ([]*Frame, error) {
	tree, err := p.session.GetFrameTree(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Frame
	var walk func(node cdp.FrameTreeNode)
	walk = func(node cdp.FrameTreeNode) {
		out = append(out, &Frame{
			page:    p,
			frameID: node.Frame.ID,
			url:     node.Frame.URL,
			name:    node.Frame.Name,
		})
		for _, c := range node.ChildFrames {
			walk(c)
		}
	}
	walk(tree.FrameTree)
	if len(out) > 0 {
		p.mu.Lock()
		if p.mainFrameID == "" {
			p.mainFrameID = out[0].frameID
		}
		p.mu.Unlock()
	}
	return out, nil
}

// FrameByURL returns the first frame whose URL contains substr, or nil.
func (p *Page) FrameByURL(ctx context.Context, substr string) (*Frame, error) {
	frames, err := p.Frames(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range frames {
		if substr != "" && strings.Contains(f.url, substr) {
			return f, nil
		}
	}
	return nil, fmt.Errorf("no frame with URL containing %q", substr)
}

// FrameByName returns the frame with the given name/id attribute, or nil.
func (p *Page) FrameByName(ctx context.Context, name string) (*Frame, error) {
	frames, err := p.Frames(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range frames {
		if f.name == name {
			return f, nil
		}
	}
	return nil, fmt.Errorf("no frame named %q", name)
}

// FrameLocator returns a frame located by an <iframe> CSS selector in the main
// document, scoping subsequent queries to that frame's content. The frame's
// iframe selector is remembered so click coordinates can be offset correctly.
func (p *Page) FrameLocator(ctx context.Context, iframeSelector string) (*Frame, error) {
	// Resolve the iframe element's src/name to match it against the frame tree.
	var info struct {
		Src  string `json:"src"`
		Name string `json:"name"`
		OK   bool   `json:"ok"`
	}
	expr := `(() => {
		const el = document.querySelector(` + jsonString(iframeSelector) + `);
		if (!el || el.tagName.toLowerCase() !== 'iframe') return {ok:false};
		return {ok:true, src: el.src || "", name: el.name || el.id || ""};
	})()`
	if err := p.EvaluateIsolated(ctx, expr, &info); err != nil {
		return nil, err
	}
	if !info.OK {
		return nil, fmt.Errorf("selector %q is not an iframe", iframeSelector)
	}

	frames, err := p.Frames(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range frames {
		if f.frameID == p.mainFrameID {
			continue
		}
		if (info.Src != "" && f.url == info.Src) || (info.Name != "" && f.name == info.Name) {
			f.parentSel = iframeSelector
			return f, nil
		}
	}
	// Fall back: first non-main frame whose URL shares the src prefix.
	for _, f := range frames {
		if f.frameID != p.mainFrameID && info.Src != "" && strings.Contains(f.url, trimURL(info.Src)) {
			f.parentSel = iframeSelector
			return f, nil
		}
	}
	return nil, fmt.Errorf("no frame matched iframe %q", iframeSelector)
}

// URL returns the frame's URL.
func (f *Frame) URL() string { return f.url }

// Name returns the frame's name.
func (f *Frame) Name() string { return f.name }

// ensureWorld creates (or reuses) this frame's isolated execution context.
func (f *Frame) ensureWorld(ctx context.Context) (int, error) {
	f.mu.Lock()
	if f.ctxID != 0 {
		id := f.ctxID
		f.mu.Unlock()
		return id, nil
	}
	fid := f.frameID
	f.mu.Unlock()

	if fid == "" {
		tree, err := f.page.session.GetFrameTree(ctx)
		if err != nil {
			return 0, err
		}
		fid = tree.FrameTree.Frame.ID
		f.mu.Lock()
		f.frameID = fid
		f.mu.Unlock()
	}
	id, err := f.page.session.CreateIsolatedWorld(ctx, fid)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.ctxID = id
	f.mu.Unlock()
	return id, nil
}

func (f *Frame) invalidate() {
	f.mu.Lock()
	f.ctxID = 0
	f.mu.Unlock()
}

// Evaluate runs JS in this frame's isolated world.
func (f *Frame) Evaluate(ctx context.Context, expression string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		ctxID, err := f.ensureWorld(ctx)
		if err != nil {
			return err
		}
		res, exc, err := f.page.session.Evaluate(ctx, expression, ctxID, true, false)
		if err != nil || exc != nil {
			f.invalidate()
			if attempt == 0 {
				continue
			}
			if exc != nil {
				return fmt.Errorf("evaluate exception: %s", exc.Text)
			}
			return err
		}
		if out != nil && len(res.Value) > 0 {
			return json.Unmarshal(res.Value, out)
		}
		return nil
	}
	return nil
}

// frameOffset returns the (x,y) page-space offset of this frame's content, i.e.
// the position of the iframe element in the top document. (0,0) for the main
// frame or when the offset can't be determined.
func (f *Frame) frameOffset(ctx context.Context) (float64, float64) {
	if f.frameID == f.page.mainFrameID || f.parentSel == "" {
		return 0, 0
	}
	box, err := f.page.BoundingBox(ctx, f.parentSel)
	if err != nil || box == nil {
		return 0, 0
	}
	return box.X, box.Y
}

// BoundingBox returns the page-space bounding box of an element inside the frame
// (frame-local rect plus the frame's offset), so clicks land in the right place.
func (f *Frame) BoundingBox(ctx context.Context, selector string) (*BoundingBox, error) {
	expr := `(() => {
		const el = document.querySelector(` + jsonString(selector) + `);
		if (!el) return null;
		const r = el.getBoundingClientRect();
		if (r.width === 0 && r.height === 0) return null;
		return {x: r.x, y: r.y, width: r.width, height: r.height};
	})()`
	var box *BoundingBox
	if err := f.Evaluate(ctx, expr, &box); err != nil {
		return nil, err
	}
	if box == nil {
		return nil, nil
	}
	ox, oy := f.frameOffset(ctx)
	box.X += ox
	box.Y += oy
	return box, nil
}

// WaitForSelector polls until an element exists in the frame (or timeout).
func (f *Frame) WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	expr := "!!document.querySelector(" + jsonString(selector) + ")"
	for time.Now().Before(deadline) {
		var found bool
		if err := f.Evaluate(ctx, expr, &found); err == nil && found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout %s waiting for selector %q in frame", timeout, selector)
}

// TextContent returns the textContent of an element in the frame.
func (f *Frame) TextContent(ctx context.Context, selector string) (string, error) {
	var v *string
	expr := `(() => { const el = document.querySelector(` + jsonString(selector) + `); return el ? el.textContent : null; })()`
	if err := f.Evaluate(ctx, expr, &v); err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("element %q not found in frame", selector)
	}
	return *v, nil
}

// Click clicks an element inside the frame using page-space coordinates derived
// from the frame offset, driving the page's mouse (humanized when enabled).
func (f *Frame) Click(ctx context.Context, selector string, opts ClickOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := f.WaitForSelector(ctx, selector, timeout); err != nil {
		return err
	}
	box, err := f.BoundingBox(ctx, selector)
	if err != nil {
		return err
	}
	if box == nil {
		return errNoBox(selector)
	}
	tx := box.X + box.Width/2
	ty := box.Y + box.Height/2

	rm := f.page.RawMouse()
	if cfg := f.page.humanCfgFor(opts.HumanConfig); cfg != nil {
		f.page.initCursor(cfg)
		cx, cy := rm.Position()
		humanMove(ctx, rm, cx, cy, tx, ty, cfg)
		humanClick(ctx, rm, false, cfg)
		return nil
	}
	rm.Move(ctx, tx, ty)
	rm.Down(ctx)
	rm.Up(ctx)
	return nil
}

// Fill sets an input/textarea value inside the frame.
func (f *Frame) Fill(ctx context.Context, selector, value string, opts FillOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := f.WaitForSelector(ctx, selector, timeout); err != nil {
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
	return f.Evaluate(ctx, expr, &ok)
}

// helpers ------------------------------------------------------------------

// trimURL strips query/fragment from a URL for loose matching.
func trimURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i]
	}
	return u
}
