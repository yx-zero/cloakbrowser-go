package cloakbrowser

import (
	"context"
	"sync"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// ContextOptions configures a BrowserContext (viewport, UA, locale emulation).
//
// Note: in the stealth model, locale and timezone are best set via the binary
// flags at launch (--lang / --fingerprint-timezone), NOT via detectable CDP
// emulation. These options exist for parity and explicit overrides.
type ContextOptions struct {
	// UserAgent overrides the navigator.userAgent for pages in this context.
	UserAgent string
	// Viewport sets the emulated viewport. When nil, the default depends on
	// headless mode (see applyDefaults). Set an explicit value to force
	// emulation in any mode, or set NoViewport to disable it.
	Viewport *Viewport
	// NoViewport disables viewport emulation (use the OS window size).
	NoViewport bool
	// ColorScheme is "light", "dark" or "no-preference".
	ColorScheme string
}

// applyDefaults resolves the viewport policy for the given headless mode.
//
// An explicit Viewport or NoViewport is always honored. When neither is set,
// headless mode emulates a realistic DefaultViewport, while headed mode uses
// the real OS window (NoViewport) — applying setDeviceMetricsOverride to a
// visible window creates an "impossible window" size mismatch that is a strong
// bot-detection signal.
func (o *ContextOptions) applyDefaults(headless bool) {
	if o.NoViewport || o.Viewport != nil {
		return
	}
	if headless {
		v := DefaultViewport
		o.Viewport = &v
	} else {
		o.NoViewport = true
	}
}

// NewContextOptions returns default ContextOptions. The viewport is resolved at
// launch based on headless mode (emulated default when headless, real window
// when headed); set Viewport or NoViewport explicitly to override.
func NewContextOptions() ContextOptions {
	return ContextOptions{}
}

// BrowserContext is an isolated browsing context (cookies, storage, pages).
type BrowserContext struct {
	browser          *Browser
	browserContextID string // "" for the default/persistent context
	opts             ContextOptions
	isDefault        bool
	ownsBrowser      bool // when true, Close also closes the browser

	mu     sync.Mutex
	pages  []*Page
	closed bool
}

// Browser returns the parent Browser.
func (bc *BrowserContext) Browser() *Browser { return bc.browser }

// NewPage opens a new page in this context.
func (bc *BrowserContext) NewPage(ctx context.Context) (*Page, error) {
	targetID, err := bc.browser.conn.CreateTarget(ctx, "about:blank", bc.browserContextID)
	if err != nil {
		return nil, err
	}
	sessionID, err := bc.browser.conn.AttachToTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	sess := bc.browser.conn.NewSession(sessionID)

	p := &Page{
		ctxt:     bc,
		targetID: targetID,
		session:  sess,
	}
	if err := p.init(ctx); err != nil {
		_ = bc.browser.conn.CloseTarget(ctx, targetID)
		return nil, err
	}

	if bc.browser.humanize && bc.browser.humanCfg != nil {
		patchPage(p, bc.browser.humanCfg)
	}

	bc.mu.Lock()
	bc.pages = append(bc.pages, p)
	bc.mu.Unlock()
	return p, nil
}

// Cookies returns cookies visible to this context (optionally filtered by URL).
func (bc *BrowserContext) Cookies(ctx context.Context, urls ...string) ([]cdp.Cookie, error) {
	// Cookies are read via any page session; use a throwaway page if none exist.
	bc.mu.Lock()
	var sess *cdp.Session
	if len(bc.pages) > 0 {
		sess = bc.pages[0].session
	}
	bc.mu.Unlock()
	if sess == nil {
		p, err := bc.NewPage(ctx)
		if err != nil {
			return nil, err
		}
		sess = p.session
	}
	return sess.GetCookies(ctx, urls)
}

// AddCookies sets cookies in this context.
func (bc *BrowserContext) AddCookies(ctx context.Context, cookies []cdp.Cookie) error {
	bc.mu.Lock()
	var sess *cdp.Session
	if len(bc.pages) > 0 {
		sess = bc.pages[0].session
	}
	bc.mu.Unlock()
	if sess == nil {
		p, err := bc.NewPage(ctx)
		if err != nil {
			return err
		}
		sess = p.session
	}
	return sess.SetCookies(ctx, cookies)
}

// Close closes all pages in this context (and the browser if this context owns it).
func (bc *BrowserContext) Close(ctx context.Context) error {
	bc.mu.Lock()
	if bc.closed {
		bc.mu.Unlock()
		return nil
	}
	bc.closed = true
	pages := append([]*Page(nil), bc.pages...)
	bc.mu.Unlock()

	for _, p := range pages {
		_ = p.Close(ctx)
	}
	if bc.browserContextID != "" {
		_ = bc.browser.conn.DisposeBrowserContext(ctx, bc.browserContextID)
	}
	if bc.ownsBrowser {
		return bc.browser.Close(ctx)
	}
	return nil
}
