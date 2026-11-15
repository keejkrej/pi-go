// Ported from packages/coding-agent/src/modes/interactive/components/provider-brand.ts (pi v1.5.2).

package components

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const (
	pbCharsPerColor    = 4.0
	pbCharsPerSecond   = 10.0
	pbGlowPeriodMs     = 2400.0
	pbAnimationFrameMs = 50 * time.Millisecond
)

// ProviderBrandStyle colors text for animation time elapsedMs.
type ProviderBrandStyle = func(text string, elapsedMs float64) string

var (
	pbRadiusColors = pbParseColors("#4d9abf", "#83ccd2", "#f1be57", "#f09082")
	pbOpenRouter   = struct {
		darkBase, darkPeak   tui.Color
		lightBase, lightPeak tui.Color
	}{
		darkBase:  pbMustParseColor("#c6f243"),
		darkPeak:  pbMustParseColor("#efffb0"),
		lightBase: pbMustParseColor("#5d32e1"),
		lightPeak: pbMustParseColor("#8f6bff"),
	}
)

func pbMustParseColor(hex string) tui.Color {
	c, _ := tui.ParseColor(hex)
	return c
}

func pbParseColors(hexes ...string) []tui.Color {
	out := make([]tui.Color, 0, len(hexes))
	for _, h := range hexes {
		out = append(out, pbMustParseColor(h))
	}
	return out
}

// GetProviderBrandStyle returns the animated brand style for providerId, or nil when none exists.
func GetProviderBrandStyle(providerId string) ProviderBrandStyle {
	switch providerId {
	case core.RadiusProviderId:
		return RadiusShimmer
	case "openrouter":
		return pbOpenRouterGlow
	default:
		return nil
	}
}

// RadiusShimmer colors text with the four Radius logo colors streaming left to right.
func RadiusShimmer(text string, elapsedMs float64) string {
	mode := theme.CurrentTheme().GetColorMode()
	cycle := float64(len(pbRadiusColors)) * pbCharsPerColor
	offset := (elapsedMs / 1000.0) * pbCharsPerSecond
	var result strings.Builder
	index := 0
	for _, ch := range text {
		position := math.Mod(math.Mod(float64(index)-offset, cycle)+cycle, cycle)
		band := int(math.Floor(position / pbCharsPerColor))
		if band < 0 || band >= len(pbRadiusColors) {
			band = 0
		}
		t := position/pbCharsPerColor - float64(band)
		// Smoothstep keeps each band recognizable while still blending into the next one.
		amount := t * t * (3 - 2*t)
		from := pbRadiusColors[band]
		to := pbRadiusColors[(band+1)%len(pbRadiusColors)]
		mixed, err := tui.MixColors(from, to, amount, tui.ColorMixSpaceSrgb)
		if err != nil {
			mixed = from
		}
		result.WriteString(tui.ForegroundAnsi(mixed, mode))
		result.WriteRune(ch)
		index++
	}
	result.WriteString("\x1b[39m")
	return result.String()
}

func pbOpenRouterGlow(text string, elapsedMs float64) string {
	th := theme.CurrentTheme()
	base, peak := pbOpenRouter.darkBase, pbOpenRouter.darkPeak
	if th.Appearance == tui.TerminalColorSchemeLight {
		base, peak = pbOpenRouter.lightBase, pbOpenRouter.lightPeak
	}
	amount := (1.0 - math.Cos((2.0*math.Pi*elapsedMs)/pbGlowPeriodMs)) / 2.0
	mixed, err := tui.MixColors(base, peak, amount, tui.ColorMixSpaceOklch)
	if err != nil {
		mixed = base
	}
	return tui.ForegroundAnsi(mixed, th.GetColorMode()) + text + "\x1b[39m"
}

// StartBrandAnimation requests renders on ui every 50ms while isAnimating returns true and returns a stop function.
func StartBrandAnimation(ui tui.TUI, isAnimating func() bool) func() {
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	var stopped atomic.Bool
	var pending atomic.Bool

	go func() {
		ticker := time.NewTicker(pbAnimationFrameMs)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
			}
			if stopped.Load() || ui == nil || isAnimating == nil {
				continue
			}
			if !pending.CompareAndSwap(false, true) {
				continue
			}
			ui.Post(func() {
				defer pending.Store(false)
				if stopped.Load() {
					return
				}
				if isAnimating() {
					ui.RequestRender(nil)
				}
			})
		}
	}()

	return func() {
		stopped.Store(true)
		stopOnce.Do(func() { close(stopCh) })
	}
}
