package cloakbrowser

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// Browser is a running stealth Chromium instance driven over CDP.
type Browser struct {
	conn        *cdp.Conn
	cmd         *exec.Cmd
	userDataDir string
	tempProfile bool

	humanize bool
	humanCfg *resolvedHumanConfig

	mu       sync.Mutex
	contexts []*BrowserContext
	closed   bool
}

// Conn exposes the underlying CDP connection for advanced use.
func (b *Browser) Conn() *cdp.Conn { return b.conn }

// NewContext creates a fresh, isolated browser context (incognito-like).
func (b *Browser) NewContext(ctx context.Context, opts ContextOptions) (*BrowserContext, error) {
	opts.applyDefaults()
	var proxyServer, proxyBypass string
	// Per-context proxy override is not derived from launch proxy here; the
	// launch-level proxy already applies process-wide via --proxy-server.
	id, err := b.conn.CreateBrowserContext(ctx, proxyServer, proxyBypass)
	if err != nil {
		return nil, err
	}
	bc := &BrowserContext{
		browser:          b,
		browserContextID: id,
		opts:             opts,
	}
	b.mu.Lock()
	b.contexts = append(b.contexts, bc)
	b.mu.Unlock()
	return bc, nil
}

// defaultContext returns a BrowserContext bound to the browser's default
// (non-incognito) context — used for persistent profiles.
func (b *Browser) defaultContext(opts ContextOptions) *BrowserContext {
	opts.applyDefaults()
	bc := &BrowserContext{
		browser:          b,
		browserContextID: "", // empty => default context
		opts:             opts,
		isDefault:        true,
	}
	b.mu.Lock()
	b.contexts = append(b.contexts, bc)
	b.mu.Unlock()
	return bc
}

// NewPage opens a new page in a fresh default context. For most uses this is the
// quickest way to get a Page.
func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	bc := b.defaultContext(NewContextOptions())
	return bc.NewPage(ctx)
}

// Close shuts down the browser and cleans up the temp profile.
func (b *Browser) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	// Try a graceful Browser.close first.
	closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_ = b.conn.CloseBrowser(closeCtx)
	cancel()

	_ = b.conn.Close()

	if b.cmd != nil && b.cmd.Process != nil {
		done := make(chan struct{})
		go func() { b.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = b.cmd.Process.Kill()
			<-done
		}
	}

	if b.tempProfile && b.userDataDir != "" {
		_ = os.RemoveAll(b.userDataDir)
	}
	return nil
}
