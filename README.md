# cloakbrowser-go

A **pure-Go** reimplementation of [CloakHQ/CloakBrowser](https://github.com/CloakHQ/CloakBrowser) —
stealth Chromium for bot-detection bypass.

**Zero JavaScript. Zero Python. Zero cgo.** Builds and runs with `CGO_ENABLED=0`.

The upstream project is a thin launcher that drives a pre-patched, fingerprint-hardened Chromium
binary via **Playwright (a Node.js process)** over the Chrome DevTools Protocol (CDP). This port
keeps the same patched Chromium binary (downloaded/cached exactly as upstream does — it is not
"our" code and can't be reproduced in Go) but **replaces the Playwright/Node transport with a
native Go CDP client over a raw WebSocket**. No Node, Python, or cgo is ever spawned — the only
child process is the Chromium binary itself.

## Install

```bash
go get github.com/yx-zero/cloakbrowser-go
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

func main() {
	ctx := context.Background()
	browser, err := cb.Launch(ctx, cb.NewLaunchOptions())
	if err != nil { panic(err) }
	defer browser.Close(ctx)

	page, _ := browser.NewPage(ctx)
	page.Goto(ctx, "https://example.com")
	title, _ := page.Title(ctx)
	fmt.Println(title)
}
```

> Use `cb.NewLaunchOptions()` to get the upstream defaults (`Headless: true`, `StealthArgs: true`).
> A bare `cb.LaunchOptions{}` literal has Go zero values (`StealthArgs: false`), so prefer the
> constructor and tweak fields, e.g. `opts := cb.NewLaunchOptions(); opts.Humanize = true`.

## Launch variants

```go
// Returns a *Browser — call NewPage / NewContext on it.
browser, _ := cb.Launch(ctx, cb.NewLaunchOptions())

// Returns a *BrowserContext with viewport/UA/etc. pre-applied (closing it closes the browser).
bctx, _ := cb.LaunchContext(ctx, cb.NewLaunchOptions(), cb.NewContextOptions())

// Persistent profile — cookies/localStorage survive across runs in userDataDir.
pctx, _ := cb.LaunchPersistentContext(ctx, "./my-profile", cb.NewLaunchOptions(), cb.NewContextOptions())
```

## Proxy & GeoIP

```go
opts := cb.NewLaunchOptions()
opts.Proxy = "http://user:pass@host:8080"   // or "socks5://user:pass@host:1080"
// Auto-detect timezone & locale (and spoof WebRTC IP) from the proxy's exit IP:
opts.GeoIP = true
browser, _ := cb.Launch(ctx, opts)
```

Credentials with special characters (`@`, `=`, …) are percent-encoded automatically. HTTP and
SOCKS5 proxies are passed to Chromium via `--proxy-server` with inline auth (no CDP auth
interceptor). With `GeoIP` enabled, a `~70 MB` GeoLite2-City database is downloaded on first use and
cached in `~/.cloakbrowser/geoip/`. You can also pass `Timezone`/`Locale` explicitly to override.

## Features ported

| Area | Source (Python) | Go |
|---|---|---|
| Stealth args, versions, cache paths | `config.py` | `config.go` |
| Binary download / cache / SHA-256 / extract / auto-update | `download.py` | `download.go` |
| GeoIP timezone/locale + exit-IP via proxy (mmdb) | `geoip.py` | `geoip.go` |
| Widevine CDM hint seeding (Linux) | `widevine.py` | `widevine.go` |
| Proxy parse/normalize (HTTP + SOCKS5, inline creds) | `browser.py` | `proxy.go` |
| `build_args`, geoip wiring, WebRTC-IP resolution | `browser.py` | `args.go` |
| **Native CDP client (replaces Playwright)** | *(Playwright)* | `cdp/` |
| Browser / Context / Page / Mouse / Keyboard | *(Playwright)* | `browser.go`, `context.go`, `page.go`, `mouse.go`, `keyboard.go` |
| Page interactions: Click, Fill, Type, Hover, DblClick, Tap, Check/Uncheck/SetChecked, SelectOption, Clear, DragTo, PressSequentially, Press | *(Playwright)* | `page.go`, `page_actions.go` |
| Actionability checks: attached/visible/enabled/editable/stable/pointer-events with `[100,250,500,1000]ms` backoff + `Force` | `human/actionability.py` | `actionability.go` |
| Locator handle: `page.Locator(sel)`, `.Nth/.First`, Click/Fill/Type/Hover/Check/BoundingBox/TextContent/Count/... | `human/elementhandle.ts` | `locator.go` |
| Storage state: save/load cookies + per-origin localStorage as JSON | *(Playwright `storage_state`)* | `storage.go` |
| DOM reads: IsChecked, IsVisible, IsEnabled, IsEditable, GetAttribute, TextContent, InnerText, BoundingBox | *(Playwright)* | `page.go`, `page_actions.go` |
| Navigation/waits: Goto, Reload, GoBack/Forward, WaitForSelector(+states), WaitForFunction, WaitForLoadState (incl. networkidle), WaitForURL, WaitForNavigation | *(Playwright)* | `page.go`, `page_nav.go` |
| Frames/iframes: Frames, FrameLocator, FrameByURL/Name, per-frame Evaluate/BoundingBox/Click/Fill/WaitForSelector (offset-correct) | *(Playwright frames)* | `frame.go` |
| Headers, init scripts, response events: SetExtraHTTPHeaders, AddInitScript, OnResponse | *(Playwright)* | `page_nav.go` |
| Humanize: Bézier mouse, typing+typos, smooth scroll, presets | `human/` | `human_*.go` |
| CLI: install / info / update / clear-cache | `__main__.py` | `cmd/cloakbrowser` |
| CDP multiplexer: per-seed Chrome behind one port | `bin/cloakserve` | `cmd/cloakserve` |

## CLI

```bash
go run ./cmd/cloakbrowser install      # download the patched Chromium (~200 MB)
go run ./cmd/cloakbrowser info         # version / platform / path / cache
go run ./cmd/cloakbrowser update       # check for & download a newer binary
go run ./cmd/cloakbrowser clear-cache  # remove cached binaries
```

## CDP multiplexer (cloakserve)

`cloakserve` runs one stealth Chromium **per fingerprint seed** behind a single CDP port — connect
with `?fingerprint=<seed>` and each seed gets its own isolated browser identity.

```bash
go run ./cmd/cloakserve --port=9222 --idle-timeout=300
# then point any CDP client at:
#   http://host:9222/json/version?fingerprint=12345
#   http://host:9222/json/version?fingerprint=12345&timezone=America/New_York&locale=en-US&proxy=socks5://...
```

Per-connection query params: `fingerprint`, `timezone`, `locale`, `proxy`, `geoip`, and any
`--fingerprint-*` flag as `?name=value`. Idle seeds are reaped after `--idle-timeout` seconds
(0 = never). `GET /` returns a JSON health/status snapshot of all live processes.

## Humanize

```go
opts := cb.NewLaunchOptions()
opts.Humanize = true
opts.HumanPreset = cb.PresetCareful // or cb.PresetDefault
browser, _ := cb.Launch(ctx, opts)
page, _ := browser.NewPage(ctx)
page.Goto(ctx, "https://example.com")
page.Fill(ctx, "input[name=q]", "hello world", cb.FillOptions{}) // Bézier cursor + realistic typing
```

`page.Click`, `page.Fill`, `page.Type` and `page.ScrollIntoView` automatically use human-like
Bézier cursor movement, per-character timing with occasional typos, and smooth accelerate → cruise
→ decelerate → overshoot scrolling when humanize is enabled.

## Environment variables (same as upstream)

`CLOAKBROWSER_CACHE_DIR`, `CLOAKBROWSER_BINARY_PATH`, `CLOAKBROWSER_DOWNLOAD_URL`,
`CLOAKBROWSER_SKIP_CHECKSUM`, `CLOAKBROWSER_AUTO_UPDATE`, `CLOAKBROWSER_WIDEVINE`,
`CLOAKBROWSER_WIDEVINE_CDM`, `CLOAKBROWSER_GEOIP_TIMEOUT_SECONDS`,
`CLOAKBROWSER_SUPPRESS_FONT_WARNING`.

Set `CLOAKBROWSER_BINARY_PATH` to a local patched Chromium to skip the download entirely.

## Examples

- `examples/basic` — launch, navigate, screenshot.
- `examples/humanize` — human-like search box typing.

To verify stealth against a real anti-bot page, point the browser at a Cloudflare-protected URL
and check that the managed challenge clears (no checkbox) and the real content renders:

```go
page.Goto(ctx, "https://your-protected-target/")
// poll page.Title(ctx) until the real page title appears
```

## Not ported (yet)

- **macOS Gatekeeper xattr removal** (`xattr -cr` after extract) — no-op on other platforms.
- `patchright` backend — there is no Go equivalent; selecting it returns a clear error.
- Playwright/Puppeteer API-shape shims — the Go API is idiomatic, not a JS clone.

## Verify the build is pure Go

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
```
