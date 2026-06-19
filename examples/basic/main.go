// Basic example: launch headless stealth Chromium, navigate, print title.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	browser, err := cb.Launch(ctx, cb.NewLaunchOptions())
	if err != nil {
		log.Fatalf("launch: %v", err)
	}
	defer browser.Close(ctx)

	page, err := browser.NewPage(ctx)
	if err != nil {
		log.Fatalf("new page: %v", err)
	}

	if err := page.Goto(ctx, "https://example.com"); err != nil {
		log.Fatalf("goto: %v", err)
	}

	title, _ := page.Title(ctx)
	fmt.Println("Title:", title)

	if _, err := page.Screenshot(ctx, "example.png", false); err != nil {
		log.Printf("screenshot: %v", err)
	} else {
		fmt.Println("Saved example.png")
	}
}
