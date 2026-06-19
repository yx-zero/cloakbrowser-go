package cloakbrowser

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Binary download and cache management. Downloads the patched Chromium binary on
// first use, caches it locally — similar to how Playwright downloads its own
// bundled Chromium.

const updateCheckInterval = time.Hour

var httpClient = &http.Client{Timeout: 0} // per-request timeouts set via context where needed

// EnsureBinary ensures the stealth Chromium binary is available, downloading if
// needed, and returns the path to the chrome executable.
//
// Set CLOAKBROWSER_BINARY_PATH to skip download and use a local build.
func EnsureBinary() (string, error) {
	if override := LocalBinaryOverride(); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("CLOAKBROWSER_BINARY_PATH set to %q but file does not exist", override)
		}
		return override, nil
	}

	if err := checkPlatformAvailable(); err != nil {
		return "", err
	}

	effective := EffectiveVersion()
	binaryPath := BinaryPath(effective)
	if isExecutable(binaryPath) {
		showWelcome()
		maybeTriggerUpdateCheck()
		return binaryPath, nil
	}

	platformVersion := ChromiumVersion()
	if effective != platformVersion {
		fallback := BinaryPath(platformVersion)
		if isExecutable(fallback) {
			maybeTriggerUpdateCheck()
			return fallback, nil
		}
	}

	tag, _ := PlatformTag()
	log.Printf("cloakbrowser: stealth Chromium %s not found. Downloading for %s...", platformVersion, tag)
	if err := downloadAndExtract(""); err != nil {
		return "", err
	}

	binaryPath = BinaryPath("")
	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("download completed but binary not found at expected path: %s", binaryPath)
	}
	maybeTriggerUpdateCheck()
	return binaryPath, nil
}

var welcomeOnce sync.Once

func showWelcome() {
	marker := filepath.Join(CacheDir(), ".welcome_shown")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	welcomeOnce.Do(func() {
		fmt.Fprint(os.Stderr, "\n  CloakBrowser — stealth Chromium for automation\n"+
			"  https://github.com/CloakHQ/CloakBrowser\n\n"+
			"  Star us if CloakBrowser helps your project!\n\n")
		_ = os.MkdirAll(filepath.Dir(marker), 0o755)
		_ = os.WriteFile(marker, []byte{}, 0o644)
	})
}

// downloadAndExtract downloads the binary archive and extracts it to the cache.
// Tries the primary server first, falls back to GitHub Releases. Verifies the
// SHA-256 checksum before extraction when available.
func downloadAndExtract(version string) error {
	primaryURL := downloadURL(version)
	fallbackURL := fallbackDownloadURL(version)
	binaryDir := BinaryDir(version)
	binaryPath := BinaryPath(version)

	if err := os.MkdirAll(filepath.Dir(binaryDir), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "cloakbrowser-*"+archiveExt())
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := downloadFile(primaryURL, tmpPath); err != nil {
		if os.Getenv("CLOAKBROWSER_DOWNLOAD_URL") != "" {
			return err
		}
		log.Printf("cloakbrowser: primary download failed (%v), trying GitHub Releases...", err)
		if err := downloadFile(fallbackURL, tmpPath); err != nil {
			return err
		}
	}

	if strings.ToLower(os.Getenv("CLOAKBROWSER_SKIP_CHECKSUM")) != "true" {
		if err := verifyDownloadChecksum(tmpPath, version); err != nil {
			return err
		}
	}

	if err := extractArchive(tmpPath, binaryDir, binaryPath); err != nil {
		return err
	}
	showWelcome()
	return nil
}

func verifyDownloadChecksum(filePath, version string) error {
	checksums := fetchChecksums(version)
	name := archiveName("")
	if checksums == nil {
		log.Printf("cloakbrowser: SHA256SUMS not available for this release — skipping checksum verification")
		return nil
	}
	expected, ok := checksums[name]
	if !ok {
		log.Printf("cloakbrowser: SHA256SUMS found but no entry for %s — skipping verification", name)
		return nil
	}
	return verifyChecksum(filePath, expected)
}

func fetchChecksums(version string) map[string]string {
	v := version
	if v == "" {
		v = ChromiumVersion()
	}
	urls := []string{fmt.Sprintf("%s/chromium-v%s/SHA256SUMS", DownloadBaseURL(), v)}
	if os.Getenv("CLOAKBROWSER_DOWNLOAD_URL") == "" {
		urls = append(urls, fmt.Sprintf("%s/chromium-v%s/SHA256SUMS", githubDownloadBaseURL, v))
	}
	for _, u := range urls {
		resp, err := httpClient.Get(u)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode >= 400 {
			continue
		}
		return parseChecksums(string(body))
	}
	return nil
}

func parseChecksums(text string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			filename := strings.TrimPrefix(parts[1], "*")
			result[filename] = strings.ToLower(parts[0])
		}
	}
	return result
}

func verifyChecksum(filePath, expected string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != strings.ToLower(expected) {
		return fmt.Errorf("checksum verification failed!\n  Expected: %s\n  Got:      %s\n  File may be corrupted or tampered with", expected, actual)
	}
	log.Printf("cloakbrowser: checksum verified: SHA-256 OK")
	return nil
}

func downloadFile(url, dest string) error {
	log.Printf("cloakbrowser: downloading from %s", url)
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	var downloaded int64
	lastPct := -1
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			downloaded += int64(n)
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct >= lastPct+10 {
					lastPct = pct
					log.Printf("cloakbrowser: download progress: %d%% (%d/%d MB)", pct, downloaded/(1<<20), total/(1<<20))
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	log.Printf("cloakbrowser: download complete: %d MB", downloaded/(1<<20))
	return nil
}

func extractArchive(archivePath, destDir, binaryPath string) error {
	log.Printf("cloakbrowser: extracting to %s", destDir)
	if _, err := os.Stat(destDir); err == nil {
		os.RemoveAll(destDir)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	var err error
	if strings.HasSuffix(archivePath, ".zip") {
		err = extractZip(archivePath, destDir)
	} else {
		err = extractTar(archivePath, destDir)
	}
	if err != nil {
		return err
	}

	flattenSingleSubdir(destDir)

	bp := binaryPath
	if bp == "" {
		bp = BinaryPath("")
	}
	if _, err := os.Stat(bp); err == nil {
		makeExecutable(bp)
		log.Printf("cloakbrowser: binary ready: %s", bp)
	}
	return nil
}

func extractTar(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	destAbs, _ := filepath.Abs(destDir)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			// Allow symlinks — macOS .app bundles require them (Framework layout).
			if filepath.IsAbs(hdr.Linkname) || containsDotDot(hdr.Linkname) {
				log.Printf("cloakbrowser: skipping suspicious symlink: %s -> %s", hdr.Name, hdr.Linkname)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeDir:
			if !pathWithin(target, destAbs) {
				return fmt.Errorf("archive contains path traversal: %s", hdr.Name)
			}
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0o700); err != nil {
				return err
			}
		default:
			if !pathWithin(target, destAbs) {
				return fmt.Errorf("archive contains path traversal: %s", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	destAbs, _ := filepath.Abs(destDir)
	for _, fheader := range zr.File {
		target := filepath.Join(destDir, fheader.Name)
		if !pathWithin(target, destAbs) {
			return fmt.Errorf("archive contains path traversal: %s", fheader.Name)
		}
		if fheader.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := fheader.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fheader.Mode()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		out.Close()
	}
	return nil
}

func containsDotDot(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func pathWithin(target, baseAbs string) bool {
	abs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	return abs == baseAbs || strings.HasPrefix(abs, baseAbs+string(os.PathSeparator))
}

// flattenSingleSubdir moves contents up if extraction created a single subdir
// (e.g. fingerprint-chromium-142/chrome -> chrome). Never flattens .app bundles.
func flattenSingleSubdir(destDir string) {
	entries, err := os.ReadDir(destDir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return
	}
	sub := entries[0]
	if strings.HasSuffix(sub.Name(), ".app") {
		return
	}
	subPath := filepath.Join(destDir, sub.Name())
	items, err := os.ReadDir(subPath)
	if err != nil {
		return
	}
	for _, item := range items {
		os.Rename(filepath.Join(subPath, item.Name()), filepath.Join(destDir, item.Name()))
	}
	os.Remove(subPath)
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

func makeExecutable(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	os.Chmod(path, fi.Mode()|0o111)
}

// ClearCache removes all cached binaries, forcing re-download on next launch.
func ClearCache() error {
	dir := CacheDir()
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		log.Printf("cloakbrowser: cache cleared: %s", dir)
	}
	return nil
}

// BinaryInfo holds metadata about the current binary installation.
type BinaryInfo struct {
	Version        string
	BundledVersion string
	Platform       string
	BinaryPath     string
	Installed      bool
	CacheDir       string
	DownloadURL    string
}

// GetBinaryInfo returns info about the current binary installation.
func GetBinaryInfo() BinaryInfo {
	effective := EffectiveVersion()
	bp := BinaryPath(effective)
	tag, _ := PlatformTag()
	_, err := os.Stat(bp)
	return BinaryInfo{
		Version:        effective,
		BundledVersion: CHROMIUMVersion,
		Platform:       tag,
		BinaryPath:     bp,
		Installed:      err == nil,
		CacheDir:       BinaryDir(effective),
		DownloadURL:    downloadURL(effective),
	}
}

// ---------------------------------------------------------------------------
// Auto-update
// ---------------------------------------------------------------------------

// CheckForUpdate manually checks for a newer Chromium version, downloading it if
// available. Returns the new version or "" if already up to date.
func CheckForUpdate() (string, error) {
	latest := getLatestChromiumVersion()
	if latest == "" || !versionNewer(latest, ChromiumVersion()) {
		return "", nil
	}
	if _, err := os.Stat(BinaryDir(latest)); err == nil {
		writeVersionMarker(latest)
		return latest, nil
	}
	log.Printf("cloakbrowser: downloading Chromium %s...", latest)
	if err := downloadAndExtract(latest); err != nil {
		return "", err
	}
	writeVersionMarker(latest)
	return latest, nil
}

func shouldCheckForUpdate() bool {
	if strings.ToLower(os.Getenv("CLOAKBROWSER_AUTO_UPDATE")) == "false" {
		return false
	}
	if LocalBinaryOverride() != "" || os.Getenv("CLOAKBROWSER_DOWNLOAD_URL") != "" {
		return false
	}
	checkFile := filepath.Join(CacheDir(), ".last_update_check")
	if data, err := os.ReadFile(checkFile); err == nil {
		if last, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil {
			if time.Since(time.Unix(int64(last), 0)) < updateCheckInterval {
				return false
			}
		}
	}
	return true
}

func getLatestChromiumVersion() string {
	req, _ := http.NewRequest("GET", githubAPIURL+"?per_page=10", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := jsonDecode(resp.Body, &releases); err != nil {
		return ""
	}
	platformTarball := archiveName("")
	for _, r := range releases {
		if strings.HasPrefix(r.TagName, "chromium-v") && !r.Draft {
			for _, a := range r.Assets {
				if a.Name == platformTarball {
					return strings.TrimPrefix(r.TagName, "chromium-v")
				}
			}
		}
	}
	return ""
}

func writeVersionMarker(version string) {
	cacheDir := CacheDir()
	os.MkdirAll(cacheDir, 0o755)
	tag, _ := PlatformTag()
	marker := filepath.Join(cacheDir, "latest_version_"+tag)
	tmp := marker + ".tmp"
	if err := os.WriteFile(tmp, []byte(version), 0o644); err == nil {
		os.Rename(tmp, marker)
	}
}

func checkAndDownloadUpdate() {
	defer func() { recover() }()
	checkFile := filepath.Join(CacheDir(), ".last_update_check")
	os.MkdirAll(filepath.Dir(checkFile), 0o755)
	os.WriteFile(checkFile, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)

	platformVersion := ChromiumVersion()
	latest := getLatestChromiumVersion()
	if latest == "" || !versionNewer(latest, platformVersion) {
		return
	}
	if _, err := os.Stat(BinaryDir(latest)); err == nil {
		writeVersionMarker(latest)
		return
	}
	log.Printf("cloakbrowser: newer Chromium available: %s (current: %s). Downloading in background...", latest, platformVersion)
	if err := downloadAndExtract(latest); err != nil {
		return
	}
	writeVersionMarker(latest)
	log.Printf("cloakbrowser: background update complete: Chromium %s ready. Will use on next launch.", latest)
}

func maybeTriggerUpdateCheck() {
	if !shouldCheckForUpdate() {
		return
	}
	go checkAndDownloadUpdate()
}
