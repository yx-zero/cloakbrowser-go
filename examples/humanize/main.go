// Humanize example: launch with human-like behavior and fill a search box.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	cb "github.com/yx-zero/cloakbrowser-go/cloakbrowser"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	opts := cb.NewLaunchOptions()
	opts.Humanize = true
	opts.HumanPreset = cb.PresetDefault
	opts = opts.WithHeadless(false) // watch the cursor move (set true for CI)

	browser, err := cb.Launch(ctx, opts)
	if err != nil {
		log.Fatalf("launch: %v", err)
	}
	defer browser.Close(ctx)

	page, err := browser.NewPage(ctx)
	if err != nil {
		log.Fatalf("new page: %v", err)
	}

	if err := page.Goto(ctx, "https://duckduckgo.com/"); err != nil {
		log.Fatalf("goto: %v", err)
	}

	// Human-like typing into the search box (Bézier cursor + per-char timing).
	if err := page.Fill(ctx, "input[name=q]", "cloakbrowser pure go", cb.FillOptions{}); err != nil {
		log.Printf("fill: %v", err)
	}
	page.Idle(ctx, 0.8)
	_ = page.Press(ctx, "Enter")

	time.Sleep(2 * time.Second)
	title, _ := page.Title(ctx)
	fmt.Println("Title:", title)
}
