package cloakbrowser

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Widevine CDM hint-file seeding for persistent contexts.
//
// CloakBrowser's binary is built with Widevine support but ships no CDM. Users
// sideload it by copying a WidevineCdm/ directory from a real Chrome install
// next to the binary. This module pre-seeds the hint file before launch so a
// sideloaded CDM works on the very first launch. Linux only.

const widevineHintFilename = "latest-component-updated-widevine-cdm"

// widevineSeedingDisabled reports whether CLOAKBROWSER_WIDEVINE is a falsey kill switch.
func widevineSeedingDisabled() bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv("CLOAKBROWSER_WIDEVINE")))
	switch val {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// resolveWidevineCDMDir locates a sideloaded Widevine CDM directory, or "" if absent.
//
// If CLOAKBROWSER_WIDEVINE_CDM is set it is used exclusively (an invalid value
// — no manifest.json — skips seeding). Otherwise <dir of chrome binary>/WidevineCdm.
// A directory counts only if it contains manifest.json.
func resolveWidevineCDMDir(binaryPath string) string {
	custom, isSet := os.LookupEnv("CLOAKBROWSER_WIDEVINE_CDM")
	var cdmDir string
	if isSet {
		cdmDir = custom
	} else {
		cdmDir = filepath.Join(filepath.Dir(binaryPath), "WidevineCdm")
	}
	if fi, err := os.Stat(filepath.Join(cdmDir, "manifest.json")); err == nil && !fi.IsDir() {
		if abs, err := filepath.Abs(cdmDir); err == nil {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				return resolved
			}
			return abs
		}
		return cdmDir
	}
	return ""
}

// seedWidevineHint writes the Widevine CDM hint file into a persistent profile
// before launch. No-op on non-Linux, when disabled, or when no CDM is present.
// Never returns an error — a failure here must not break the browser launch.
func seedWidevineHint(userDataDir, binaryPath string) {
	if runtime.GOOS != "linux" {
		return
	}
	if widevineSeedingDisabled() {
		return
	}
	if userDataDir == "" {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("cloakbrowser: failed to seed Widevine CDM hint file: %v", r)
		}
	}()

	cdmDir := resolveWidevineCDMDir(binaryPath)
	if cdmDir == "" {
		if _, isSet := os.LookupEnv("CLOAKBROWSER_WIDEVINE_CDM"); isSet {
			log.Printf("cloakbrowser: CLOAKBROWSER_WIDEVINE_CDM is set but has no manifest.json; skipping Widevine hint seeding")
		}
		return
	}

	hintDir := filepath.Join(userDataDir, "WidevineCdm")
	if err := os.MkdirAll(hintDir, 0o755); err != nil {
		log.Printf("cloakbrowser: failed to create Widevine hint dir: %v", err)
		return
	}
	hintFile := filepath.Join(hintDir, widevineHintFilename)

	// Compact separators byte-match the JS wrapper's JSON.stringify output.
	content, _ := json.Marshal(map[string]string{"Path": cdmDir})

	if existing, err := os.ReadFile(hintFile); err == nil && string(existing) == string(content) {
		return // already seeded correctly
	}

	if err := os.WriteFile(hintFile, content, 0o644); err != nil {
		log.Printf("cloakbrowser: failed to write Widevine hint file: %v", err)
		return
	}
	log.Printf("cloakbrowser: seeded Widevine CDM hint -> %s", cdmDir)
}
