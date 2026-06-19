// Package cloakbrowser is a pure-Go reimplementation of CloakHQ/CloakBrowser.
//
// It launches the stealth-patched Chromium binary and drives it over a native
// Go Chrome DevTools Protocol (CDP) client — no Node.js, Playwright, Python or
// cgo involved. The patched Chromium binary itself is downloaded/cached exactly
// as the upstream wrapper does.
package cloakbrowser

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Version is the wrapper (this library) version. Mirrors cloakbrowser/_version.py.
const Version = "0.3.31"

// CHROMIUMVersion is the Chromium version shipped with this release — the latest
// across all platforms (for display/reference). Use ChromiumVersion() for the
// current platform's actual version.
const CHROMIUMVersion = "146.0.7680.177.5"

// PlatformChromiumVersions maps a platform tag to the Chromium version built for it.
var PlatformChromiumVersions = map[string]string{
	"linux-x64":    "146.0.7680.177.5",
	"linux-arm64":  "146.0.7680.177.3",
	"darwin-arm64": "145.0.7632.109.2",
	"darwin-x64":   "145.0.7632.109.2",
	"windows-x64":  "146.0.7680.177.5",
}

// IgnoreDefaultArgs are the automation-leaking flags upstream strips from
// Playwright's defaults. The native driver simply never adds them, so this set
// is kept for reference/compat (e.g. callers building their own arg lists).
var IgnoreDefaultArgs = []string{"--enable-automation", "--enable-unsafe-swiftshader"}

// DefaultViewport is a realistic maximized Chrome on a 1080p Windows screen.
var DefaultViewport = Viewport{Width: 1920, Height: 947}

// Viewport is a width/height pair in CSS pixels.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// supportedPlatforms maps (GOOS, GOARCH) to a platform tag.
var supportedPlatforms = map[[2]string]string{
	{"linux", "amd64"}:   "linux-x64",
	{"linux", "arm64"}:   "linux-arm64",
	{"darwin", "arm64"}:  "darwin-arm64",
	{"darwin", "amd64"}:  "darwin-x64",
	{"windows", "amd64"}: "windows-x64",
}

func availablePlatforms() map[string]struct{} {
	out := make(map[string]struct{}, len(PlatformChromiumVersions))
	for k := range PlatformChromiumVersions {
		out[k] = struct{}{}
	}
	return out
}

// GetDefaultStealthArgs builds stealth args with a random fingerprint seed per
// launch. On macOS it runs as a native Mac browser (spoofing Windows on Mac
// creates detectable mismatches); on Linux/Windows it uses the Windows profile.
func GetDefaultStealthArgs() []string {
	seed := rand.Intn(90000) + 10000 // 10000-99999
	base := []string{
		"--no-sandbox",
		fmt.Sprintf("--fingerprint=%d", seed),
	}
	if runtime.GOOS == "darwin" {
		return append(base, "--fingerprint-platform=macos")
	}
	return append(base, "--fingerprint-platform=windows")
}

// ChromiumVersion returns the Chromium version for the current platform.
func ChromiumVersion() string {
	tag, err := PlatformTag()
	if err != nil {
		return CHROMIUMVersion
	}
	if v, ok := PlatformChromiumVersions[tag]; ok {
		return v
	}
	return CHROMIUMVersion
}

// PlatformTag returns the platform tag for binary download (e.g. "linux-x64").
func PlatformTag() (string, error) {
	if tag, ok := supportedPlatforms[[2]string{runtime.GOOS, runtime.GOARCH}]; ok {
		return tag, nil
	}
	keys := make([]string, 0, len(supportedPlatforms))
	for k := range supportedPlatforms {
		keys = append(keys, k[0]+"-"+k[1])
	}
	sort.Strings(keys)
	return "", fmt.Errorf("unsupported platform: %s %s. Supported: %s",
		runtime.GOOS, runtime.GOARCH, strings.Join(keys, ", "))
}

// CacheDir returns the cache directory for downloaded binaries.
// Override with CLOAKBROWSER_CACHE_DIR. Default: ~/.cloakbrowser/.
func CacheDir() string {
	if custom := os.Getenv("CLOAKBROWSER_CACHE_DIR"); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".cloakbrowser")
}

// BinaryDir returns the directory for a Chromium version binary.
func BinaryDir(version string) string {
	if version == "" {
		version = ChromiumVersion()
	}
	return filepath.Join(CacheDir(), "chromium-"+version)
}

// BinaryPath returns the expected path to the chrome executable.
func BinaryPath(version string) string {
	dir := BinaryDir(version)
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(dir, "Chromium.app", "Contents", "MacOS", "Chromium")
	case "windows":
		return filepath.Join(dir, "chrome.exe")
	default:
		return filepath.Join(dir, "chrome")
	}
}

// checkPlatformAvailable returns an error if no pre-built binary exists for this
// platform. Skipped when CLOAKBROWSER_BINARY_PATH is set.
func checkPlatformAvailable() error {
	if LocalBinaryOverride() != "" {
		return nil
	}
	tag, err := PlatformTag()
	if err != nil {
		return err
	}
	if _, ok := availablePlatforms()[tag]; !ok {
		avail := make([]string, 0, len(PlatformChromiumVersions))
		for k := range PlatformChromiumVersions {
			avail = append(avail, k)
		}
		sort.Strings(avail)
		return fmt.Errorf(
			"CloakBrowser — pre-built binaries are currently only available for: %s.\n\n"+
				"To use CloakBrowser now, set CLOAKBROWSER_BINARY_PATH to a local Chromium binary",
			strings.Join(avail, ", "))
	}
	return nil
}

// EffectiveVersion returns the best available version: auto-updated if a newer
// binary is present in cache, else the platform default.
func EffectiveVersion() string {
	base := ChromiumVersion()
	cache := CacheDir()
	tag, _ := PlatformTag()
	names := []string{"latest_version"}
	if tag != "" {
		names = []string{"latest_version_" + tag, "latest_version"}
	}
	for _, name := range names {
		marker := filepath.Join(cache, name)
		data, err := os.ReadFile(marker)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(data))
		if v != "" && versionNewer(v, base) {
			if _, err := os.Stat(BinaryPath(v)); err == nil {
				return v
			}
		}
	}
	return base
}

// versionTuple parses "145.0.7718.0" into [145, 0, 7718, 0] for comparison.
func versionTuple(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// versionNewer reports whether version a is strictly newer than version b.
func versionNewer(a, b string) bool {
	ta, tb := versionTuple(a), versionTuple(b)
	n := len(ta)
	if len(tb) > n {
		n = len(tb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(ta) {
			x = ta[i]
		}
		if i < len(tb) {
			y = tb[i]
		}
		if x > y {
			return true
		}
		if x < y {
			return false
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Download URLs
// ---------------------------------------------------------------------------

// DownloadBaseURL is the primary download host. Override with CLOAKBROWSER_DOWNLOAD_URL.
func DownloadBaseURL() string {
	if u := os.Getenv("CLOAKBROWSER_DOWNLOAD_URL"); u != "" {
		return u
	}
	return "https://cloakbrowser.dev"
}

const (
	githubAPIURL          = "https://api.github.com/repos/CloakHQ/cloakbrowser/releases"
	githubDownloadBaseURL = "https://github.com/CloakHQ/cloakbrowser/releases/download"
)

// archiveExt returns the archive extension for the current platform.
func archiveExt() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// archiveName returns the archive filename for a platform tag.
func archiveName(tag string) string {
	if tag == "" {
		tag, _ = PlatformTag()
	}
	return "cloakbrowser-" + tag + archiveExt()
}

// downloadURL returns the full download URL for the current platform's archive.
func downloadURL(version string) string {
	if version == "" {
		version = ChromiumVersion()
	}
	return fmt.Sprintf("%s/chromium-v%s/%s", DownloadBaseURL(), version, archiveName(""))
}

// fallbackDownloadURL returns the GitHub Releases fallback URL.
func fallbackDownloadURL(version string) string {
	if version == "" {
		version = ChromiumVersion()
	}
	return fmt.Sprintf("%s/chromium-v%s/%s", githubDownloadBaseURL, version, archiveName(""))
}

// LocalBinaryOverride returns the user-set local binary path, or "".
// Set CLOAKBROWSER_BINARY_PATH to use a locally built Chromium instead of downloading.
func LocalBinaryOverride() string {
	return os.Getenv("CLOAKBROWSER_BINARY_PATH")
}

// jsonDecode is a small helper for streaming JSON decode from a reader.
func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
