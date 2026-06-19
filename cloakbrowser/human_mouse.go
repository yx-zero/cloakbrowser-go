package cloakbrowser

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Human-like mouse movement and clicking — ports cloakbrowser/human/mouse.py.

type point struct{ x, y float64 }

func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

func bezier(p0, p1, p2, p3 point, t float64) point {
	u := 1 - t
	uu := u * u
	uuu := uu * u
	tt := t * t
	ttt := tt * t
	return point{
		x: uuu*p0.x + 3*uu*t*p1.x + 3*u*tt*p2.x + ttt*p3.x,
		y: uuu*p0.y + 3*uu*t*p1.y + 3*u*tt*p2.y + ttt*p3.y,
	}
}

func randomControlPoints(start, end point) (point, point) {
	dx := end.x - start.x
	dy := end.y - start.y
	dist := math.Hypot(dx, dy)
	if dist == 0 {
		dist = 1
	}
	px := -dy / dist
	py := dx / dist
	bias1 := randFloat(-0.3, 0.3) * dist
	bias2 := randFloat(-0.3, 0.3) * dist
	return point{start.x + dx*0.25 + px*bias1, start.y + dy*0.25 + py*bias1},
		point{start.x + dx*0.75 + px*bias2, start.y + dy*0.75 + py*bias2}
}

// humanMove moves the mouse from (startX,startY) to (endX,endY) along a Bézier
// curve with wobble, burst pauses and optional overshoot.
func humanMove(ctx context.Context, rm *RawMouse, startX, startY, endX, endY float64, cfg *resolvedHumanConfig) {
	dist := math.Hypot(endX-startX, endY-startY)
	if dist < 1 {
		return
	}
	steps := int(math.Round(dist / cfg.MouseStepsDivisor))
	if steps < cfg.MouseMinSteps {
		steps = cfg.MouseMinSteps
	}
	if steps > cfg.MouseMaxSteps {
		steps = cfg.MouseMaxSteps
	}

	start := point{startX, startY}
	end := point{endX, endY}
	cp1, cp2 := randomControlPoints(start, end)

	burstCounter := 0
	burstSize := randIntRange(cfg.MouseBurstSize)

	for i := 0; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		easedT := easeInOut(progress)
		pt := bezier(start, cp1, cp2, end, easedT)

		wobbleAmp := math.Sin(math.Pi*progress) * cfg.MouseWobbleMax
		wx := pt.x + (rand.Float64()-0.5)*2*wobbleAmp
		wy := pt.y + (rand.Float64()-0.5)*2*wobbleAmp
		rm.Move(ctx, wx, wy)

		burstCounter++
		if burstCounter >= burstSize && i < steps {
			sleepMs(ctx, randRange(cfg.MouseBurstPause))
			burstCounter = 0
		}
	}

	if rand.Float64() < cfg.MouseOvershootChance {
		overshootDist := randRange(cfg.MouseOvershootPx)
		angle := math.Atan2(endY-startY, endX-startX)
		rm.Move(ctx, endX+math.Cos(angle)*overshootDist, endY+math.Sin(angle)*overshootDist)
		sleepMs(ctx, randFloat(30, 70))
		rm.Move(ctx, endX+(rand.Float64()-0.5)*4, endY+(rand.Float64()-0.5)*4)
	}
}

// clickTarget computes a humanized click point within a bounding box.
func clickTarget(box *BoundingBox, isInput bool, cfg *resolvedHumanConfig) point {
	var xFrac, yFrac float64
	if isInput {
		xFrac = randRange(cfg.ClickInputXRange)
		yFrac = randFloat(0.30, 0.70)
	} else {
		xFrac = randFloat(0.35, 0.65)
		yFrac = randFloat(0.35, 0.65)
	}
	return point{
		x: math.Round(box.X + box.Width*xFrac),
		y: math.Round(box.Y + box.Height*yFrac),
	}
}

// humanClick performs a click with realistic aim delay and hold time.
func humanClick(ctx context.Context, rm *RawMouse, isInput bool, cfg *resolvedHumanConfig) {
	var aimDelay, holdTime float64
	if isInput {
		aimDelay = randRange(cfg.ClickAimDelayInput)
		holdTime = randRange(cfg.ClickHoldInput)
	} else {
		aimDelay = randRange(cfg.ClickAimDelayButton)
		holdTime = randRange(cfg.ClickHoldButton)
	}
	sleepMs(ctx, aimDelay)
	rm.Down(ctx)
	sleepMs(ctx, holdTime)
	rm.Up(ctx)
}

// humanIdle drifts the cursor with small random movements for the given number
// of seconds (mirrors human_idle in mouse.py).
func humanIdle(ctx context.Context, rm *RawMouse, seconds, cx, cy float64, cfg *resolvedHumanConfig) {
	deadline := time.Now().Add(time.Duration(seconds * float64(time.Second)))
	x, y := cx, cy
	for time.Now().Before(deadline) {
		x += (rand.Float64() - 0.5) * 2 * cfg.IdleDriftPx
		y += (rand.Float64() - 0.5) * 2 * cfg.IdleDriftPx
		rm.Move(ctx, x, y)
		sleepMs(ctx, randRange(cfg.IdlePauseRange))
	}
}
