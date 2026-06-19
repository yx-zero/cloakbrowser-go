package cdp

import (
	"context"
	"encoding/json"
)

// Typed thin wrappers around the CDP domains the driver uses. These are not a
// full protocol binding — only the commands and event payloads we need.

// ---------------------------------------------------------------------------
// Target domain (browser-level)
// ---------------------------------------------------------------------------

// TargetInfo describes a CDP target (page, iframe, worker, ...).
type TargetInfo struct {
	TargetID         string `json:"targetId"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	Attached         bool   `json:"attached"`
	BrowserContextID string `json:"browserContextId"`
}

// CreateBrowserContext creates an isolated browser context (incognito-like) and
// returns its id. proxyServer/proxyBypass are optional per-context overrides.
func (c *Conn) CreateBrowserContext(ctx context.Context, proxyServer, proxyBypass string) (string, error) {
	params := map[string]any{}
	if proxyServer != "" {
		params["proxyServer"] = proxyServer
	}
	if proxyBypass != "" {
		params["proxyBypassList"] = proxyBypass
	}
	var out struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := c.Send(ctx, "Target.createBrowserContext", params, &out); err != nil {
		return "", err
	}
	return out.BrowserContextID, nil
}

// DisposeBrowserContext destroys a browser context and all its pages.
func (c *Conn) DisposeBrowserContext(ctx context.Context, browserContextID string) error {
	return c.Send(ctx, "Target.disposeBrowserContext",
		map[string]any{"browserContextId": browserContextID}, nil)
}

// CreateTarget opens a new page (tab) and returns its targetId. If
// browserContextID is non-empty the page belongs to that context.
func (c *Conn) CreateTarget(ctx context.Context, url, browserContextID string) (string, error) {
	if url == "" {
		url = "about:blank"
	}
	params := map[string]any{"url": url}
	if browserContextID != "" {
		params["browserContextId"] = browserContextID
	}
	var out struct {
		TargetID string `json:"targetId"`
	}
	if err := c.Send(ctx, "Target.createTarget", params, &out); err != nil {
		return "", err
	}
	return out.TargetID, nil
}

// AttachToTarget attaches (flat mode) to a target and returns its sessionId.
func (c *Conn) AttachToTarget(ctx context.Context, targetID string) (string, error) {
	var out struct {
		SessionID string `json:"sessionId"`
	}
	err := c.Send(ctx, "Target.attachToTarget",
		map[string]any{"targetId": targetID, "flatten": true}, &out)
	if err != nil {
		return "", err
	}
	return out.SessionID, nil
}

// CloseTarget closes a page target.
func (c *Conn) CloseTarget(ctx context.Context, targetID string) error {
	return c.Send(ctx, "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
}

// GetTargets returns all current targets.
func (c *Conn) GetTargets(ctx context.Context) ([]TargetInfo, error) {
	var out struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := c.Send(ctx, "Target.getTargets", nil, &out); err != nil {
		return nil, err
	}
	return out.TargetInfos, nil
}

// SetDiscoverTargets toggles target discovery events.
func (c *Conn) SetDiscoverTargets(ctx context.Context, discover bool) error {
	return c.Send(ctx, "Target.setDiscoverTargets", map[string]any{"discover": discover}, nil)
}

// ---------------------------------------------------------------------------
// Browser domain
// ---------------------------------------------------------------------------

// BrowserVersion holds the result of Browser.getVersion.
type BrowserVersion struct {
	ProtocolVersion string `json:"protocolVersion"`
	Product         string `json:"product"`
	Revision        string `json:"revision"`
	UserAgent       string `json:"userAgent"`
	JSVersion       string `json:"jsVersion"`
}

// GetVersion returns the browser version info.
func (c *Conn) GetVersion(ctx context.Context) (BrowserVersion, error) {
	var out BrowserVersion
	err := c.Send(ctx, "Browser.getVersion", nil, &out)
	return out, err
}

// CloseBrowser asks the browser to shut down gracefully.
func (c *Conn) CloseBrowser(ctx context.Context) error {
	return c.Send(ctx, "Browser.close", nil, nil)
}

// ---------------------------------------------------------------------------
// Session-scoped helpers (Page / Runtime / Input / Network / Emulation)
// ---------------------------------------------------------------------------

// Session is a convenience wrapper binding a sessionId to a Conn.
type Session struct {
	Conn      *Conn
	SessionID string
}

// NewSession binds a sessionId.
func (c *Conn) NewSession(sessionID string) *Session {
	return &Session{Conn: c, SessionID: sessionID}
}

// Send issues a command on this session.
func (s *Session) Send(ctx context.Context, method string, params, out any) error {
	return s.Conn.SendSession(ctx, s.SessionID, method, params, out)
}

// On registers an event listener scoped to this session.
func (s *Session) On(method string, cb func(json.RawMessage)) func() {
	return s.Conn.On(s.SessionID, method, cb)
}

// WaitEvent waits for an event scoped to this session.
func (s *Session) WaitEvent(ctx context.Context, method string) (json.RawMessage, error) {
	return s.Conn.WaitEvent(ctx, s.SessionID, method)
}

// EventSub is a one-shot event subscription with an explicit cancel.
type EventSub struct {
	Ch     chan json.RawMessage
	Cancel func()
}

// WaitEventChan subscribes to an event and returns immediately with a channel,
// letting callers register the listener *before* triggering the action that
// fires it (avoiding a race). Caller must call Cancel when done.
func (s *Session) WaitEventChan(ctx context.Context, method string) *EventSub {
	ch := make(chan json.RawMessage, 1)
	remove := s.Conn.On(s.SessionID, method, func(params json.RawMessage) {
		select {
		case ch <- params:
		default:
		}
	})
	return &EventSub{Ch: ch, Cancel: remove}
}

// --- Page ---

// PageEnable enables the Page domain (needed for lifecycle events).
func (s *Session) PageEnable(ctx context.Context) error {
	return s.Send(ctx, "Page.enable", nil, nil)
}

// RuntimeEnable enables the Runtime domain.
func (s *Session) RuntimeEnable(ctx context.Context) error {
	return s.Send(ctx, "Runtime.enable", nil, nil)
}

// NetworkEnable enables the Network domain.
func (s *Session) NetworkEnable(ctx context.Context) error {
	return s.Send(ctx, "Network.enable", nil, nil)
}

// Navigate triggers Page.navigate.
func (s *Session) Navigate(ctx context.Context, url string) error {
	var out struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.Send(ctx, "Page.navigate", map[string]any{"url": url}, &out); err != nil {
		return err
	}
	if out.ErrorText != "" && out.ErrorText != "net::ERR_ABORTED" {
		return &ProtocolError{Code: -1, Message: out.ErrorText}
	}
	return nil
}

// FrameNode is one frame in the page's frame hierarchy.
type FrameNode struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	URL      string `json:"url"`
	Name     string `json:"name"`
}

// FrameTreeNode is a recursive frame tree entry.
type FrameTreeNode struct {
	Frame       FrameNode       `json:"frame"`
	ChildFrames []FrameTreeNode `json:"childFrames"`
}

// FrameTree describes the page's frame hierarchy.
type FrameTree struct {
	FrameTree FrameTreeNode `json:"frameTree"`
}

// GetFrameTree returns the page's frame tree (recursive, with URLs and names).
func (s *Session) GetFrameTree(ctx context.Context) (FrameTree, error) {
	var out FrameTree
	err := s.Send(ctx, "Page.getFrameTree", nil, &out)
	return out, err
}

// CreateIsolatedWorld creates an isolated execution context in a frame and
// returns its executionContextId.
func (s *Session) CreateIsolatedWorld(ctx context.Context, frameID string) (int, error) {
	var out struct {
		ExecutionContextID int `json:"executionContextId"`
	}
	err := s.Send(ctx, "Page.createIsolatedWorld", map[string]any{
		"frameId":             frameID,
		"worldName":           "",
		"grantUniveralAccess": true,
	}, &out)
	return out.ExecutionContextID, err
}

// CaptureScreenshot returns base64-encoded PNG (or the requested format) data.
func (s *Session) CaptureScreenshot(ctx context.Context, format string, fullPage bool) (string, error) {
	params := map[string]any{"captureBeyondViewport": fullPage}
	if format != "" {
		params["format"] = format
	}
	var out struct {
		Data string `json:"data"`
	}
	err := s.Send(ctx, "Page.captureScreenshot", params, &out)
	return out.Data, err
}

// NavigationEntry is one entry in the navigation history (subset).
type NavigationEntry struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// NavigationHistory is the result of Page.getNavigationHistory.
type NavigationHistory struct {
	CurrentIndex int               `json:"currentIndex"`
	Entries      []NavigationEntry `json:"entries"`
}

// GetNavigationHistory returns the page's navigation history.
func (s *Session) GetNavigationHistory(ctx context.Context) (NavigationHistory, error) {
	var out NavigationHistory
	err := s.Send(ctx, "Page.getNavigationHistory", nil, &out)
	return out, err
}

// NavigateToHistoryEntry navigates to a specific history entry id.
func (s *Session) NavigateToHistoryEntry(ctx context.Context, entryID int) error {
	return s.Send(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": entryID}, nil)
}

// AddScriptToEvaluateOnNewDocument injects a script that runs before any page
// script on every new document (Playwright's add_init_script equivalent).
func (s *Session) AddScriptToEvaluateOnNewDocument(ctx context.Context, source string) (string, error) {
	var out struct {
		Identifier string `json:"identifier"`
	}
	err := s.Send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": source}, &out)
	return out.Identifier, err
}

// SetExtraHTTPHeaders sets headers sent with every request from this page.
func (s *Session) SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error {
	return s.Send(ctx, "Network.setExtraHTTPHeaders", map[string]any{"headers": headers}, nil)
}

// --- Runtime ---

// RemoteObject is a CDP Runtime.RemoteObject (subset).
type RemoteObject struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	ClassName   string          `json:"className"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description"`
	ObjectID    string          `json:"objectId"`
}

// ExceptionDetails describes a thrown JS exception (subset).
type ExceptionDetails struct {
	Text      string       `json:"text"`
	Exception RemoteObject `json:"exception"`
}

// Evaluate runs an expression. contextID==0 means the default world.
func (s *Session) Evaluate(ctx context.Context, expression string, contextID int, returnByValue, awaitPromise bool) (*RemoteObject, *ExceptionDetails, error) {
	params := map[string]any{
		"expression":    expression,
		"returnByValue": returnByValue,
		"awaitPromise":  awaitPromise,
	}
	if contextID != 0 {
		params["contextId"] = contextID
	}
	var out struct {
		Result           RemoteObject      `json:"result"`
		ExceptionDetails *ExceptionDetails `json:"exceptionDetails"`
	}
	if err := s.Send(ctx, "Runtime.evaluate", params, &out); err != nil {
		return nil, nil, err
	}
	return &out.Result, out.ExceptionDetails, nil
}

// --- Input ---

// DispatchMouseEvent sends a synthetic mouse event with isTrusted=true.
func (s *Session) DispatchMouseEvent(ctx context.Context, params map[string]any) error {
	return s.Send(ctx, "Input.dispatchMouseEvent", params, nil)
}

// DispatchKeyEvent sends a synthetic key event with isTrusted=true.
func (s *Session) DispatchKeyEvent(ctx context.Context, params map[string]any) error {
	return s.Send(ctx, "Input.dispatchKeyEvent", params, nil)
}

// --- Network (cookies) ---

// Cookie is a CDP network cookie (subset matching Network.setCookie params).
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	URL      string  `json:"url,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	Size     int     `json:"size,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	Session  bool    `json:"session,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

// GetCookies returns cookies for the given URLs (all if empty).
func (s *Session) GetCookies(ctx context.Context, urls []string) ([]Cookie, error) {
	params := map[string]any{}
	if len(urls) > 0 {
		params["urls"] = urls
	}
	var out struct {
		Cookies []Cookie `json:"cookies"`
	}
	err := s.Send(ctx, "Network.getCookies", params, &out)
	return out.Cookies, err
}

// SetCookies sets cookies via Network.setCookies.
func (s *Session) SetCookies(ctx context.Context, cookies []Cookie) error {
	return s.Send(ctx, "Network.setCookies", map[string]any{"cookies": cookies}, nil)
}

// --- Emulation ---

// SetDeviceMetricsOverride emulates a viewport.
func (s *Session) SetDeviceMetricsOverride(ctx context.Context, width, height int, mobile bool) error {
	return s.Send(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             width,
		"height":            height,
		"deviceScaleFactor": 0,
		"mobile":            mobile,
	}, nil)
}
