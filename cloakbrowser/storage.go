package cloakbrowser

import (
	"context"
	"encoding/json"
	"os"

	"github.com/yx-zero/cloakbrowser-go/cloakbrowser/cdp"
)

// Storage state export/import — Playwright-compatible storage_state. Captures
// cookies plus per-origin localStorage so a session can be saved to JSON and
// restored later without a persistent user-data-dir.

// StorageEntry is a single localStorage key/value pair.
type StorageEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OriginStorage holds the localStorage for one origin.
type OriginStorage struct {
	Origin       string         `json:"origin"`
	LocalStorage []StorageEntry `json:"localStorage"`
}

// StorageState is the portable session snapshot (Playwright-compatible shape).
type StorageState struct {
	Cookies []cdp.Cookie    `json:"cookies"`
	Origins []OriginStorage `json:"origins"`
}

// StorageState captures cookies + the current document's localStorage for this
// page. The localStorage is recorded under the page's current origin.
func (p *Page) StorageState(ctx context.Context) (*StorageState, error) {
	cookies, err := p.session.GetCookies(ctx, nil)
	if err != nil {
		return nil, err
	}

	var snap struct {
		Origin  string         `json:"origin"`
		Storage []StorageEntry `json:"storage"`
	}
	expr := `(() => {
		const out = [];
		try {
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				out.push({name: k, value: localStorage.getItem(k)});
			}
		} catch (e) {}
		return {origin: location.origin, storage: out};
	})()`
	_ = p.Evaluate(ctx, expr, &snap)

	state := &StorageState{Cookies: cookies}
	if snap.Origin != "" && snap.Origin != "null" && len(snap.Storage) > 0 {
		state.Origins = append(state.Origins, OriginStorage{Origin: snap.Origin, LocalStorage: snap.Storage})
	}
	return state, nil
}

// SaveStorageState writes the current page's storage state to a JSON file.
func (p *Page) SaveStorageState(ctx context.Context, path string) error {
	state, err := p.StorageState(ctx)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadStorageState restores cookies and localStorage from a previously saved
// state. Cookies are set immediately; localStorage for an origin is applied when
// the page is on that origin — navigate to the origin first, then call this.
func (p *Page) LoadStorageState(ctx context.Context, state *StorageState) error {
	if state == nil {
		return nil
	}
	if len(state.Cookies) > 0 {
		if err := p.session.SetCookies(ctx, state.Cookies); err != nil {
			return err
		}
	}
	if len(state.Origins) == 0 {
		return nil
	}
	// Apply localStorage for the origin matching the page's current location.
	var current string
	_ = p.Evaluate(ctx, "location.origin", &current)
	for _, o := range state.Origins {
		if o.Origin != current {
			continue
		}
		pairs, _ := jsonMarshal(o.LocalStorage)
		expr := `(() => {
			try {
				const items = ` + pairs + `;
				for (const it of items) localStorage.setItem(it.name, it.value);
			} catch (e) {}
			return true;
		})()`
		var ok bool
		_ = p.Evaluate(ctx, expr, &ok)
	}
	return nil
}

// LoadStorageStateFile restores storage state from a JSON file.
func (p *Page) LoadStorageStateFile(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state StorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	return p.LoadStorageState(ctx, &state)
}

// StorageState captures cookies for the whole context (localStorage is captured
// only for pages whose origin is currently loaded; use Page.StorageState for
// per-page localStorage).
func (bc *BrowserContext) StorageState(ctx context.Context) (*StorageState, error) {
	bc.mu.Lock()
	var p *Page
	if len(bc.pages) > 0 {
		p = bc.pages[0]
	}
	bc.mu.Unlock()
	if p == nil {
		var err error
		p, err = bc.NewPage(ctx)
		if err != nil {
			return nil, err
		}
	}
	return p.StorageState(ctx)
}
