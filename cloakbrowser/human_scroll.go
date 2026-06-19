package cloakbrowser

import (
	"context"
	"math"
	"math/rand"
)

// Human-like scrolling via mouse wheel events — ports cloakbrowser/human/scroll.py.

func isInViewport(box *BoundingBox, viewportHeight float64, cfg *resolvedHumanConfig) bool {
	topEdge := box.Y
	bottomEdge := box.Y + box.Height
	zoneTop := viewportHeight * cfg.ScrollTargetZone[0]
	zoneBottom := viewportHeight * cfg.ScrollTargetZone[1]
	return topEdge >= zoneTop && bottomEdge <= zoneBottom
}

// smoothWheel sends one logical scroll as a burst of small wheel events.
func smoothWheel(ctx context.Context, rm *RawMouse, delta int, cfg *resolvedHumanConfig) {
	absD := math.Abs(float64(delta))
	sign := 1.0
	if delta < 0 {
		sign = -1.0
	}
	var sent float64
	for sent < absD {
		stepSize := randFloat(20, 40)
		chunk := math.Min(stepSize, absD-sent)
		rm.Wheel(ctx, 0, math.Round(chunk)*sign)
		sent += chunk
		sleepMs(ctx, randFloat(8, 20))
	}
}

// humanScrollIntoView scrolls an element (resolved via getBox) into view with
// accelerate -> cruise -> decelerate -> overshoot -> settle dynamics.
// Returns the final box, the cursor position, and whether a scroll happened.
func humanScrollIntoView(
	ctx context.Context,
	p *Page,
	rm *RawMouse,
	getBox func() *BoundingBox,
	cursorX, cursorY float64,
	cfg *resolvedHumanConfig,
) (*BoundingBox, float64, float64, bool) {
	viewport, err := p.ViewportSize(ctx)
	if err != nil || viewport.Height == 0 {
		return getBox(), cursorX, cursorY, false
	}
	vh := float64(viewport.Height)
	vw := float64(viewport.Width)

	box := getBox()
	if box == nil {
		return nil, cursorX, cursorY, false
	}
	if isInViewport(box, vh, cfg) {
		return box, cursorX, cursorY, false
	}

	scrollAreaX := math.Round(vw * randFloat(0.3, 0.7))
	scrollAreaY := math.Round(vh * randFloat(0.3, 0.7))
	humanMove(ctx, rm, cursorX, cursorY, scrollAreaX, scrollAreaY, cfg)
	cursorX, cursorY = scrollAreaX, scrollAreaY
	sleepMs(ctx, randRange(cfg.ScrollPreMoveDelay))

	targetY := vh * randFloat(cfg.ScrollTargetZone[0], cfg.ScrollTargetZone[1])
	elementCenter := box.Y + box.Height/2
	distanceToScroll := elementCenter - targetY

	direction := 1.0
	if distanceToScroll < 0 {
		direction = -1.0
	}
	absDistance := math.Abs(distanceToScroll)
	avgDelta := (cfg.ScrollDeltaBase[0] + cfg.ScrollDeltaBase[1]) / 2
	totalClicks := int(math.Ceil(absDistance / avgDelta))
	if totalClicks < 3 {
		totalClicks = 3
	}
	accelSteps := randIntRange(cfg.ScrollAccelSteps)
	decelSteps := randIntRange(cfg.ScrollDecelSteps)

	var scrolled float64
	for i := 0; i < totalClicks; i++ {
		var delta, pause float64
		switch {
		case i < accelSteps:
			delta = randFloat(80, 100)
			pause = randRange(cfg.ScrollPauseSlow)
		case i >= totalClicks-decelSteps:
			delta = randFloat(60, 90)
			pause = randRange(cfg.ScrollPauseSlow)
		default:
			delta = randRange(cfg.ScrollDeltaBase)
			pause = randRange(cfg.ScrollPauseFast)
		}
		delta *= 1 + (rand.Float64()-0.5)*2*cfg.ScrollDeltaVariance
		d := int(math.Round(delta) * direction)

		smoothWheel(ctx, rm, d, cfg)
		scrolled += math.Abs(float64(d))
		sleepMs(ctx, pause)

		if i%3 == 2 || i == totalClicks-1 {
			box = getBox()
			if box != nil && isInViewport(box, vh, cfg) {
				break
			}
		}
		if scrolled >= absDistance*1.1 {
			break
		}
	}

	if rand.Float64() < cfg.ScrollOvershootChance {
		overshootPx := int(math.Round(randRange(cfg.ScrollOvershootPx)) * direction)
		smoothWheel(ctx, rm, overshootPx, cfg)
		sleepMs(ctx, randRange(cfg.ScrollSettleDelay))
		corrections := randIntRange(rangeF{1, 2})
		for c := 0; c < corrections; c++ {
			corrDelta := int(math.Round(randFloat(40, 80)) * -direction)
			smoothWheel(ctx, rm, corrDelta, cfg)
			sleepMs(ctx, randFloat(100, 250))
		}
	}

	sleepMs(ctx, randRange(cfg.ScrollSettleDelay))

	box = getBox()
	return box, cursorX, cursorY, true
}
