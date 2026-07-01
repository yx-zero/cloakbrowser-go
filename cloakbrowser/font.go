package cloakbrowser

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Windows-font-absence warning.
//
// On Linux the default stealth profile spoofs --fingerprint-platform=windows.
// If the host lacks the Windows system fonts (Segoe UI, Calibri, …), font
// enumeration and canvas text-metric probes reveal the mismatch and defeat the
// Windows fingerprint — a silent stealth tell. This module warns once per
// process when that situation is detected. Silence with
// CLOAKBROWSER_SUPPRESS_FONT_WARNING.

// fontFamily is a Windows font family and the lowercased, space-stripped
// filename stems that indicate it is installed.
type fontFamily struct {
	name    string
	markers []string
}

// windowsFontFamilies are the detection-relevant Windows fonts. Segoe UI is the
// Windows system UI font and the strongest tell when absent.
var windowsFontFamilies = []fontFamily{
	{"Segoe UI", []string{"segoeui"}},
	{"Calibri", []string{"calibri"}},
	{"Marlett", []string{"marlett"}},
	{"Tahoma", []string{"tahoma"}},
	{"Arial", []string{"arial"}},
}

// fontExtensions are the font container extensions worth scanning.
var fontExtensions = map[string]bool{
	".ttf": true, ".ttc": true, ".otf": true, ".pfb": true,
}

var fontWarnOnce sync.Once

// warnMissingWindowsFonts emits a one-time warning when spoofing Windows on
// Linux while the host is missing Windows fonts. No-op elsewhere, when the
// platform isn't windows, or when suppressed. Never fails a launch.
func warnMissingWindowsFonts(chromeArgs []string) {
	if runtime.GOOS != "linux" {
		return
	}
	if effectiveFingerprintPlatform(chromeArgs) != "windows" {
		return
	}
	if fontWarningSuppressed() {
		return
	}
	fontWarnOnce.Do(func() {
		defer func() { recover() }()
		stems := installedFontFileStems()
		var missing []string
		for _, fam := range windowsFontFamilies {
			if !fontFamilyPresent(fam, stems) {
				missing = append(missing, fam.name)
			}
		}
		if len(missing) == 0 {
			return
		}
		log.Printf("cloakbrowser: WARNING — spoofing --fingerprint-platform=windows on Linux, "+
			"but these Windows fonts appear to be missing: %s.\n"+
			"  Font enumeration and canvas text metrics can reveal the mismatch and defeat the Windows fingerprint.\n"+
			"  Install msttcorefonts (e.g. `sudo apt install ttf-mscorefonts-installer`) and copy Segoe UI / Calibri\n"+
			"  from a Windows install into ~/.fonts, or set CLOAKBROWSER_SUPPRESS_FONT_WARNING=1 to silence this.",
			strings.Join(missing, ", "))
	})
}

// effectiveFingerprintPlatform returns the lowercased value of the last
// --fingerprint-platform= flag in args, or "" if none is present.
func effectiveFingerprintPlatform(args []string) string {
	const prefix = "--fingerprint-platform="
	val := ""
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			val = a[len(prefix):]
		}
	}
	return strings.ToLower(strings.TrimSpace(val))
}

// fontWarningSuppressed reports whether CLOAKBROWSER_SUPPRESS_FONT_WARNING is
// set to a truthy value. Empty or falsey tokens do not suppress.
func fontWarningSuppressed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLOAKBROWSER_SUPPRESS_FONT_WARNING"))) {
	case "", "0", "false", "off", "no":
		return false
	}
	return true
}

// linuxFontDirs are the standard font search paths on Linux.
func linuxFontDirs() []string {
	dirs := []string{
		"/usr/share/fonts",
		"/usr/local/share/fonts",
		"/usr/share/wine/fonts",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".fonts"),
			filepath.Join(home, ".local", "share", "fonts"),
		)
	}
	return dirs
}

// installedFontFileStems walks the font directories and returns the set of
// lowercased, space-stripped filename stems of the font files found.
func installedFontFileStems() map[string]bool {
	stems := map[string]bool{}
	for _, dir := range linuxFontDirs() {
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entry — skip, keep walking
			}
			if d.IsDir() {
				return nil
			}
			name := strings.ToLower(d.Name())
			ext := filepath.Ext(name)
			if !fontExtensions[ext] {
				return nil
			}
			stem := strings.ReplaceAll(strings.TrimSuffix(name, ext), " ", "")
			stems[stem] = true
			return nil
		})
	}
	return stems
}

// fontFamilyPresent reports whether any of a family's marker stems is installed.
func fontFamilyPresent(fam fontFamily, stems map[string]bool) bool {
	for _, m := range fam.markers {
		if stems[m] {
			return true
		}
	}
	return false
}
