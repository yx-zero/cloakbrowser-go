package cloakbrowser

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// Humanize configuration — ports cloakbrowser/human/config.py.
//
// All numeric parameters for human-like behavior are centralized here. Two
// built-in presets: "default" (normal human speed) and "careful" (slower).

// rangeF is a (min, max) float pair.
type rangeF [2]float64

// resolvedHumanConfig holds all tunable parameters for human-like behavior.
type resolvedHumanConfig struct {
	// Keyboard
	TypingDelay       float64
	TypingDelaySpread float64
	TypingPauseChance float64
	TypingPauseRange  rangeF
	ShiftDownDelay    rangeF
	ShiftUpDelay      rangeF
	KeyHold           rangeF

	// Mistype (typo simulation)
	MistypeChance       float64
	MistypeDelayNotice  rangeF
	MistypeDelayCorrect rangeF

	FieldSwitchDelay rangeF

	// Mouse — movement
	MouseStepsDivisor    float64
	MouseMinSteps        int
	MouseMaxSteps        int
	MouseWobbleMax       float64
	MouseOvershootChance float64
	MouseOvershootPx     rangeF
	MouseBurstSize       rangeF
	MouseBurstPause      rangeF

	// Mouse — clicks
	ClickAimDelayInput  rangeF
	ClickAimDelayButton rangeF
	ClickHoldInput      rangeF
	ClickHoldButton     rangeF
	ClickInputXRange    rangeF

	// Mouse — idle
	IdleDriftPx    float64
	IdlePauseRange rangeF

	// Scroll
	ScrollDeltaBase       rangeF
	ScrollDeltaVariance   float64
	ScrollPauseFast       rangeF
	ScrollPauseSlow       rangeF
	ScrollAccelSteps      rangeF
	ScrollDecelSteps      rangeF
	ScrollOvershootChance float64
	ScrollOvershootPx     rangeF
	ScrollSettleDelay     rangeF
	ScrollTargetZone      rangeF
	ScrollPreMoveDelay    rangeF

	// Initial cursor position (as if coming from the address bar area)
	InitialCursorX rangeF
	InitialCursorY rangeF

	// Idle micro-movements between actions
	IdleBetweenActions  bool
	IdleBetweenDuration rangeF
}

// defaultHumanConfig returns the "default" preset (normal human speed).
func defaultHumanConfig() resolvedHumanConfig {
	return resolvedHumanConfig{
		TypingDelay:       70,
		TypingDelaySpread: 40,
		TypingPauseChance: 0.1,
		TypingPauseRange:  rangeF{400, 1000},
		ShiftDownDelay:    rangeF{30, 70},
		ShiftUpDelay:      rangeF{20, 50},
		KeyHold:           rangeF{15, 35},

		MistypeChance:       0.02,
		MistypeDelayNotice:  rangeF{100, 300},
		MistypeDelayCorrect: rangeF{50, 150},

		FieldSwitchDelay: rangeF{800, 1500},

		MouseStepsDivisor:    8,
		MouseMinSteps:        25,
		MouseMaxSteps:        80,
		MouseWobbleMax:       1.5,
		MouseOvershootChance: 0.15,
		MouseOvershootPx:     rangeF{3, 6},
		MouseBurstSize:       rangeF{3, 5},
		MouseBurstPause:      rangeF{8, 18},

		ClickAimDelayInput:  rangeF{60, 140},
		ClickAimDelayButton: rangeF{80, 200},
		ClickHoldInput:      rangeF{40, 100},
		ClickHoldButton:     rangeF{60, 150},
		ClickInputXRange:    rangeF{0.05, 0.30},

		IdleDriftPx:    3,
		IdlePauseRange: rangeF{300, 1000},

		ScrollDeltaBase:       rangeF{80, 130},
		ScrollDeltaVariance:   0.2,
		ScrollPauseFast:       rangeF{30, 80},
		ScrollPauseSlow:       rangeF{80, 200},
		ScrollAccelSteps:      rangeF{2, 3},
		ScrollDecelSteps:      rangeF{2, 3},
		ScrollOvershootChance: 0.1,
		ScrollOvershootPx:     rangeF{50, 150},
		ScrollSettleDelay:     rangeF{300, 600},
		ScrollTargetZone:      rangeF{0.20, 0.80},
		ScrollPreMoveDelay:    rangeF{100, 300},

		InitialCursorX: rangeF{400, 700},
		InitialCursorY: rangeF{45, 60},

		IdleBetweenActions:  false,
		IdleBetweenDuration: rangeF{0.3, 0.8},
	}
}

// carefulHumanConfig returns the "careful" preset — everything slower.
func carefulHumanConfig() resolvedHumanConfig {
	c := defaultHumanConfig()
	c.TypingDelay = 100
	c.TypingDelaySpread = 50
	c.TypingPauseChance = 0.15
	c.TypingPauseRange = rangeF{500, 1200}
	c.ShiftDownDelay = rangeF{40, 90}
	c.ShiftUpDelay = rangeF{30, 70}
	c.KeyHold = rangeF{20, 45}
	c.FieldSwitchDelay = rangeF{1000, 2000}

	c.MouseOvershootChance = 0.10
	c.MouseBurstPause = rangeF{12, 25}

	c.ClickAimDelayInput = rangeF{80, 180}
	c.ClickAimDelayButton = rangeF{120, 280}
	c.ClickHoldInput = rangeF{60, 140}
	c.ClickHoldButton = rangeF{80, 200}

	c.ScrollPauseFast = rangeF{100, 200}
	c.ScrollPauseSlow = rangeF{250, 600}
	c.ScrollSettleDelay = rangeF{400, 800}
	c.ScrollPreMoveDelay = rangeF{150, 400}

	c.IdleBetweenActions = true
	c.IdleBetweenDuration = rangeF{0.4, 1.0}
	return c
}

// resolveHumanConfig resolves a preset name + optional overrides.
//
// overrides is a map of field names (matching struct fields, snake_case keys as
// in the Python API are also accepted) to values. Supported value kinds: float64
// for scalars, []float64{lo, hi} or [2]float64 for ranges, bool for flags.
func resolveHumanConfig(preset HumanPreset, overrides map[string]any) (*resolvedHumanConfig, error) {
	var base resolvedHumanConfig
	switch preset {
	case "", PresetDefault:
		base = defaultHumanConfig()
	case PresetCareful:
		base = carefulHumanConfig()
	default:
		return nil, fmt.Errorf("unknown humanize preset %q. Valid presets: default, careful", preset)
	}
	if len(overrides) > 0 {
		applyHumanOverrides(&base, overrides)
	}
	return &base, nil
}

// applyHumanOverrides applies a sparse override map onto cfg. Unknown keys are
// ignored to keep the API forgiving (mirrors merge_config).
func applyHumanOverrides(cfg *resolvedHumanConfig, overrides map[string]any) {
	for k, v := range overrides {
		switch k {
		case "typing_delay", "TypingDelay":
			cfg.TypingDelay = asFloat(v, cfg.TypingDelay)
		case "typing_delay_spread", "TypingDelaySpread":
			cfg.TypingDelaySpread = asFloat(v, cfg.TypingDelaySpread)
		case "typing_pause_chance", "TypingPauseChance":
			cfg.TypingPauseChance = asFloat(v, cfg.TypingPauseChance)
		case "typing_pause_range", "TypingPauseRange":
			cfg.TypingPauseRange = asRange(v, cfg.TypingPauseRange)
		case "shift_down_delay", "ShiftDownDelay":
			cfg.ShiftDownDelay = asRange(v, cfg.ShiftDownDelay)
		case "shift_up_delay", "ShiftUpDelay":
			cfg.ShiftUpDelay = asRange(v, cfg.ShiftUpDelay)
		case "key_hold", "KeyHold":
			cfg.KeyHold = asRange(v, cfg.KeyHold)
		case "field_switch_delay", "FieldSwitchDelay":
			cfg.FieldSwitchDelay = asRange(v, cfg.FieldSwitchDelay)
		case "mistype_chance", "MistypeChance":
			cfg.MistypeChance = asFloat(v, cfg.MistypeChance)
		case "mistype_delay_notice", "MistypeDelayNotice":
			cfg.MistypeDelayNotice = asRange(v, cfg.MistypeDelayNotice)
		case "mistype_delay_correct", "MistypeDelayCorrect":
			cfg.MistypeDelayCorrect = asRange(v, cfg.MistypeDelayCorrect)
		case "mouse_steps_divisor", "MouseStepsDivisor":
			cfg.MouseStepsDivisor = asFloat(v, cfg.MouseStepsDivisor)
		case "mouse_min_steps", "MouseMinSteps":
			cfg.MouseMinSteps = int(asFloat(v, float64(cfg.MouseMinSteps)))
		case "mouse_max_steps", "MouseMaxSteps":
			cfg.MouseMaxSteps = int(asFloat(v, float64(cfg.MouseMaxSteps)))
		case "mouse_wobble_max", "MouseWobbleMax":
			cfg.MouseWobbleMax = asFloat(v, cfg.MouseWobbleMax)
		case "mouse_overshoot_chance", "MouseOvershootChance":
			cfg.MouseOvershootChance = asFloat(v, cfg.MouseOvershootChance)
		case "mouse_overshoot_px", "MouseOvershootPx":
			cfg.MouseOvershootPx = asRange(v, cfg.MouseOvershootPx)
		case "mouse_burst_size", "MouseBurstSize":
			cfg.MouseBurstSize = asRange(v, cfg.MouseBurstSize)
		case "mouse_burst_pause", "MouseBurstPause":
			cfg.MouseBurstPause = asRange(v, cfg.MouseBurstPause)
		case "click_aim_delay_input", "ClickAimDelayInput":
			cfg.ClickAimDelayInput = asRange(v, cfg.ClickAimDelayInput)
		case "click_aim_delay_button", "ClickAimDelayButton":
			cfg.ClickAimDelayButton = asRange(v, cfg.ClickAimDelayButton)
		case "click_hold_input", "ClickHoldInput":
			cfg.ClickHoldInput = asRange(v, cfg.ClickHoldInput)
		case "click_hold_button", "ClickHoldButton":
			cfg.ClickHoldButton = asRange(v, cfg.ClickHoldButton)
		case "click_input_x_range", "ClickInputXRange":
			cfg.ClickInputXRange = asRange(v, cfg.ClickInputXRange)
		case "idle_drift_px", "IdleDriftPx":
			cfg.IdleDriftPx = asFloat(v, cfg.IdleDriftPx)
		case "idle_pause_range", "IdlePauseRange":
			cfg.IdlePauseRange = asRange(v, cfg.IdlePauseRange)
		case "scroll_delta_base", "ScrollDeltaBase":
			cfg.ScrollDeltaBase = asRange(v, cfg.ScrollDeltaBase)
		case "scroll_delta_variance", "ScrollDeltaVariance":
			cfg.ScrollDeltaVariance = asFloat(v, cfg.ScrollDeltaVariance)
		case "scroll_pause_fast", "ScrollPauseFast":
			cfg.ScrollPauseFast = asRange(v, cfg.ScrollPauseFast)
		case "scroll_pause_slow", "ScrollPauseSlow":
			cfg.ScrollPauseSlow = asRange(v, cfg.ScrollPauseSlow)
		case "scroll_accel_steps", "ScrollAccelSteps":
			cfg.ScrollAccelSteps = asRange(v, cfg.ScrollAccelSteps)
		case "scroll_decel_steps", "ScrollDecelSteps":
			cfg.ScrollDecelSteps = asRange(v, cfg.ScrollDecelSteps)
		case "scroll_overshoot_chance", "ScrollOvershootChance":
			cfg.ScrollOvershootChance = asFloat(v, cfg.ScrollOvershootChance)
		case "scroll_overshoot_px", "ScrollOvershootPx":
			cfg.ScrollOvershootPx = asRange(v, cfg.ScrollOvershootPx)
		case "scroll_settle_delay", "ScrollSettleDelay":
			cfg.ScrollSettleDelay = asRange(v, cfg.ScrollSettleDelay)
		case "scroll_target_zone", "ScrollTargetZone":
			cfg.ScrollTargetZone = asRange(v, cfg.ScrollTargetZone)
		case "scroll_pre_move_delay", "ScrollPreMoveDelay":
			cfg.ScrollPreMoveDelay = asRange(v, cfg.ScrollPreMoveDelay)
		case "initial_cursor_x", "InitialCursorX":
			cfg.InitialCursorX = asRange(v, cfg.InitialCursorX)
		case "initial_cursor_y", "InitialCursorY":
			cfg.InitialCursorY = asRange(v, cfg.InitialCursorY)
		case "idle_between_actions", "IdleBetweenActions":
			if b, ok := v.(bool); ok {
				cfg.IdleBetweenActions = b
			}
		case "idle_between_duration", "IdleBetweenDuration":
			cfg.IdleBetweenDuration = asRange(v, cfg.IdleBetweenDuration)
		}
	}
}

func asFloat(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return fallback
}

func asRange(v any, fallback rangeF) rangeF {
	switch r := v.(type) {
	case rangeF:
		return r
	case [2]float64:
		return rangeF(r)
	case []float64:
		if len(r) == 2 {
			return rangeF{r[0], r[1]}
		}
	case [2]int:
		return rangeF{float64(r[0]), float64(r[1])}
	case []int:
		if len(r) == 2 {
			return rangeF{float64(r[0]), float64(r[1])}
		}
	case []any:
		if len(r) == 2 {
			return rangeF{asFloat(r[0], fallback[0]), asFloat(r[1], fallback[1])}
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Random + timing helpers
// ---------------------------------------------------------------------------

func randFloat(lo, hi float64) float64 { return lo + rand.Float64()*(hi-lo) }

func randRange(r rangeF) float64 { return randFloat(r[0], r[1]) }

func randIntRange(r rangeF) int {
	lo, hi := int(r[0]), int(r[1])
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo + rand.Intn(hi-lo+1)
}

// sleepMs sleeps for ms milliseconds (respecting context cancellation).
func sleepMs(ctx context.Context, ms float64) {
	if ms <= 0 {
		return
	}
	t := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
