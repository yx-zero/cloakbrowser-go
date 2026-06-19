package cloakbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Navigation, waits, headers and init scripts. Rounds out the automation API to
// cover the common Playwright surface used by scrapers and stealth flows.

// urlMatches reports whether url matches pattern. The pattern supports simple
// glob '*' wildcards; a pattern with no '*' is treated as a substring match.
func urlMatches(url, pattern string) bool {
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return strings.Contains(url, pattern)
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(url[pos:], part)
		if idx < 0 {
			return false
		}
		// First non-empty part must anchor at the start unless pattern began with '*'.
		if i == 0 && idx != 0 {
			return false
		}
		pos += idx + len(part)
	}
	// Last part must anchor at the end unless pattern ended with '*'.
	if last := parts[len(parts)-1]; last != "" && !strings.HasSuffix(url, last) {
		return false
	}
	return true
}

// WaitForURL polls until the page URL matches pattern (substring or '*' glob)
// or the timeout elapses. Use after an action that triggers navigation.
func (p *Page) WaitForURL(ctx context.Context, pattern string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cur, err := p.URL(ctx)
		if err == nil && urlMatches(cur, pattern) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	cur, _ := p.URL(ctx)
	return fmt.Errorf("timeout %s waiting for URL to match %q (current: %s)", timeout, pattern, cur)
}

// WaitForNavigation waits for a main-frame navigation to commit (the next
// Page.frameNavigated event for the top frame). Register it before triggering
// the action that navigates, then await the returned function.
//
//	wait := page.WaitForNavigation(ctx)
//	page.Click(ctx, "a#next", cb.ClickOptions{})
//	newURL, err := wait()
func (p *Page) WaitForNavigation(ctx context.Context) func() (string, error) {
	type result struct {
		url string
	}
	ch := make(chan result, 1)
	remove := p.session.On("Page.frameNavigated", func(params json.RawMessage) {
		var ev struct {
			Frame struct {
				URL      string `json:"url"`
				ParentID string `json:"parentId"`
			} `json:"frame"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Frame.ParentID == "" {
			select {
			case ch <- result{url: ev.Frame.URL}:
			default:
			}
		}
	})
	return func() (string, error) {
		defer remove()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case r := <-ch:
			return r.url, nil
		}
	}
}

// GoBack navigates to the previous history entry and waits for load.
func (p *Page) GoBack(ctx context.Context) error {
	return p.navigateHistory(ctx, -1)
}

// GoForward navigates to the next history entry and waits for load.
func (p *Page) GoForward(ctx context.Context) error {
	return p.navigateHistory(ctx, +1)
}

func (p *Page) navigateHistory(ctx context.Context, delta int) error {
	hist, err := p.session.GetNavigationHistory(ctx)
	if err != nil {
		return err
	}
	target := hist.CurrentIndex + delta
	if target < 0 || target >= len(hist.Entries) {
		return fmt.Errorf("cannot navigate %+d in history (index %d of %d)", delta, hist.CurrentIndex, len(hist.Entries))
	}
	loaded := p.session.WaitEventChan(ctx, "Page.loadEventFired")
	defer loaded.Cancel()
	if err := p.session.NavigateToHistoryEntry(ctx, hist.Entries[target].ID); err != nil {
		return err
	}
	select {
	case <-loaded.Ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForFunction polls a JS expression until it returns a truthy value or the
// timeout elapses. The expression is evaluated in the main world.
func (p *Page) WaitForFunction(ctx context.Context, expression string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var truthy bool
		if err := p.Evaluate(ctx, "!!("+expression+")", &truthy); err == nil && truthy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout %s waiting for function", timeout)
}

// LoadState identifies a navigation lifecycle milestone.
type LoadState string

const (
	// LoadStateLoad waits for the load event.
	LoadStateLoad LoadState = "load"
	// LoadStateDOMContentLoaded waits for DOMContentLoaded.
	LoadStateDOMContentLoaded LoadState = "domcontentloaded"
	// LoadStateNetworkIdle waits until there are no network connections for 500ms.
	LoadStateNetworkIdle LoadState = "networkidle"
)

// WaitForLoadState waits until the given lifecycle state is reached.
func (p *Page) WaitForLoadState(ctx context.Context, state LoadState, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch state {
	case LoadStateDOMContentLoaded:
		return p.waitDocumentReady(wctx, "interactive")
	case LoadStateNetworkIdle:
		return p.waitNetworkIdle(wctx, 500*time.Millisecond)
	default:
		return p.waitDocumentReady(wctx, "complete")
	}
}

func (p *Page) waitDocumentReady(ctx context.Context, minState string) error {
	want := map[string]int{"loading": 0, "interactive": 1, "complete": 2}
	threshold := want[minState]
	for {
		var rs string
		if err := p.Evaluate(ctx, "document.readyState", &rs); err == nil {
			if want[rs] >= threshold {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// waitNetworkIdle watches Network request/response lifecycle events and resolves
// once no requests have been in flight for the quiet period.
func (p *Page) waitNetworkIdle(ctx context.Context, quiet time.Duration) error {
	inflight := make(chan int, 256) // +1 on send, -1 on receive (delta stream)

	removeSent := p.session.On("Network.requestWillBeSent", func(json.RawMessage) {
		select {
		case inflight <- +1:
		default:
		}
	})
	defer removeSent()
	decr := func(json.RawMessage) {
		select {
		case inflight <- -1:
		default:
		}
	}
	removeFin := p.session.On("Network.loadingFinished", decr)
	defer removeFin()
	removeFail := p.session.On("Network.loadingFailed", decr)
	defer removeFail()

	count := 0
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	// If already idle, start the quiet countdown immediately (above).
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d := <-inflight:
			count += d
			if count < 0 {
				count = 0
			}
			if count == 0 {
				resetTimer(timer, quiet)
			} else {
				stopTimer(timer)
			}
		case <-timer.C:
			if count == 0 {
				return nil
			}
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	stopTimer(t)
	t.Reset(d)
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

// SetExtraHTTPHeaders sets headers sent with every subsequent request.
func (p *Page) SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error {
	return p.session.SetExtraHTTPHeaders(ctx, headers)
}

// AddInitScript registers JS that runs before any page script on every new
// document (Playwright's add_init_script). Returns the script identifier.
func (p *Page) AddInitScript(ctx context.Context, source string) (string, error) {
	return p.session.AddScriptToEvaluateOnNewDocument(ctx, source)
}

// OnResponse registers a callback invoked for each network response. It returns
// a function that removes the listener. The callback receives the response URL
// and HTTP status.
func (p *Page) OnResponse(cb func(url string, status int)) func() {
	return p.session.On("Network.responseReceived", func(params json.RawMessage) {
		var ev struct {
			Response struct {
				URL    string `json:"url"`
				Status int    `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(params, &ev) == nil {
			cb(ev.Response.URL, ev.Response.Status)
		}
	})
}
