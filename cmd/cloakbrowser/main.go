// Command cloakbrowser manages the CloakBrowser stealth Chromium binary.
//
//	cloakbrowser install      # Download the Chromium binary
//	cloakbrowser info         # Show binary version, path, platform
//	cloakbrowser update       # Check for and download a newer binary
//	cloakbrowser clear-cache  # Remove cached binaries
package main

import (
	"fmt"
	"log"
	"os"

	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

func usage() {
	fmt.Fprint(os.Stderr, `cloakbrowser — manage the CloakBrowser stealth Chromium binary.

Usage:
  cloakbrowser install      Download the Chromium binary
  cloakbrowser info         Show binary version, path, and platform
  cloakbrowser update       Check for and download a newer binary
  cloakbrowser clear-cache  Remove all cached binaries
`)
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "install":
		err = cmdInstall()
	case "info":
		err = cmdInfo()
	case "update":
		err = cmdUpdate()
	case "clear-cache":
		err = cmdClearCache()
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdInstall() error {
	path, err := cb.EnsureBinary()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func cmdInfo() error {
	info := cb.GetBinaryInfo()
	fmt.Printf("Version:   %s\n", info.Version)
	fmt.Printf("Platform:  %s\n", info.Platform)
	fmt.Printf("Binary:    %s\n", info.BinaryPath)
	fmt.Printf("Installed: %t\n", info.Installed)
	fmt.Printf("Cache:     %s\n", info.CacheDir)
	if override := cb.LocalBinaryOverride(); override != "" {
		fmt.Printf("Override:  %s (CLOAKBROWSER_BINARY_PATH)\n", override)
	}
	return nil
}

func cmdUpdate() error {
	log.Println("Checking for updates...")
	newVersion, err := cb.CheckForUpdate()
	if err != nil {
		return err
	}
	if newVersion != "" {
		fmt.Printf("Updated to Chromium %s\n", newVersion)
	} else {
		fmt.Println("Already up to date.")
	}
	return nil
}

func cmdClearCache() error {
	if _, err := os.Stat(cb.CacheDir()); err != nil {
		fmt.Println("No cache to clear.")
		return nil
	}
	if err := cb.ClearCache(); err != nil {
		return err
	}
	fmt.Println("Cache cleared.")
	return nil
}
