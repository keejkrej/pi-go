// Ported from packages/tui/src/terminal-colors.ts (pi v1.0.0).

package tui

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

// RgbColor is an sRGB triple. Channels may be fractional; OSC parsing stores rounded integers.
type RgbColor struct {
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
}

// TerminalColorScheme is the terminal's reported light or dark scheme.
type TerminalColorScheme string

const (
	TerminalColorSchemeDark  TerminalColorScheme = "dark"
	TerminalColorSchemeLight TerminalColorScheme = "light"
)

// TerminalColors holds the colors the terminal reports for its current theme.
type TerminalColors struct {
	// Foreground is the default foreground (OSC 10). Nil means unset.
	Foreground *RgbColor `json:"foreground,omitzero"`
	// Background is the default background (OSC 11). Nil means unset.
	Background *RgbColor `json:"background,omitzero"`
	// Palette is ANSI colors 0-15 (OSC 4). Nil unless the terminal reported all 16.
	Palette []RgbColor `json:"palette,omitzero"`
}

func tcHexToRgb(hex string) RgbColor {
	normalized := hex
	if strings.HasPrefix(normalized, "#") {
		normalized = normalized[1:]
	}
	r, _ := strconv.ParseInt(normalized[0:2], 16, 64)
	g, _ := strconv.ParseInt(normalized[2:4], 16, 64)
	b, _ := strconv.ParseInt(normalized[4:6], 16, 64)
	return RgbColor{R: float64(r), G: float64(g), B: float64(b)}
}

var (
	tcHexPattern             = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
	tcHex6Pattern            = regexp.MustCompile(`(?i)^[0-9a-f]{6}$`)
	tcHex12Pattern           = regexp.MustCompile(`(?i)^[0-9a-f]{12}$`)
	tcRgbaPrefixPattern      = regexp.MustCompile(`(?i)^rgba?:`)
	tcOscColorResponseRegex  = regexp.MustCompile(tcOscColorResponsePattern)
	tcColorSchemeReportRegex = regexp.MustCompile(tcColorSchemeReportPattern)
)

// tcParseOscHexChannel parses one OSC hex channel. Nil means the channel is not hex.
func tcParseOscHexChannel(channel string) *int {
	if !tcHexPattern.MatchString(channel) {
		return nil
	}
	maxVal := math.Pow(16, float64(len(channel))) - 1
	if maxVal <= 0 {
		return nil
	}
	var parsed float64
	if len(channel) <= 13 {
		v, err := strconv.ParseUint(channel, 16, 64)
		if err != nil {
			return nil
		}
		parsed = float64(v)
	} else {
		for i := 0; i < len(channel); i++ {
			c := channel[i]
			var digit float64
			switch {
			case c >= '0' && c <= '9':
				digit = float64(c - '0')
			case c >= 'a' && c <= 'f':
				digit = float64(c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				digit = float64(c - 'A' + 10)
			}
			parsed = parsed*16 + digit
		}
	}
	if math.IsInf(maxVal, 0) {
		v := 0
		return &v
	}
	out := int(math.Round((parsed / maxVal) * 255))
	return &out
}

// OscColorTarget is what an OSC color reply reports: the default foreground (OSC 10),
// the default background (OSC 11), or a palette index (OSC 4).
// Variants are *OscColorTargetForeground, *OscColorTargetBackground, and *OscColorTargetIndex.
// JS String(target) is "foreground", "background", or the decimal index.
type OscColorTarget interface{ isOscColorTarget() }

// OscColorTargetForeground is OSC 10.
type OscColorTargetForeground struct{}

func (*OscColorTargetForeground) isOscColorTarget() {}

// OscColorTargetBackground is OSC 11.
type OscColorTargetBackground struct{}

func (*OscColorTargetBackground) isOscColorTarget() {}

// OscColorTargetIndex is an OSC 4 palette index. Index may be 0.
type OscColorTargetIndex struct {
	Index int `json:"index"`
}

func (*OscColorTargetIndex) isOscColorTarget() {}

// OscColorResponse is one parsed OSC 10, 11, or 4 color reply.
// Rgb nil means the reply's color text did not parse.
type OscColorResponse struct {
	Target OscColorTarget `json:"target"`
	Rgb    *RgbColor      `json:"rgb,omitzero"`
}

// tcOscColorResponsePattern is OSC_COLOR_RESPONSE_PATTERN (flag i).
const tcOscColorResponsePattern = "(?i)^\x1b\\](?:(1[01])|4;(\\d{1,3}));([^\x07\x1b]*)(?:\x07|\x1b\\\\)$"

// tcColorSchemeReportPattern is COLOR_SCHEME_REPORT_PATTERN.
const tcColorSchemeReportPattern = "^(?:\x1b\\[\\?997;(1|2)n)+$"

// ParseOscColorResponse parses an OSC 10, 11, or 4 color reply.
// Nil means data is not such a reply. A non-nil result with nil Rgb is a reply whose color did not parse.
func ParseOscColorResponse(data string) *OscColorResponse {
	match := tcOscColorResponseRegex.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	var target OscColorTarget
	switch match[1] {
	case "10":
		target = &OscColorTargetForeground{}
	case "11":
		target = &OscColorTargetBackground{}
	default:
		idx, _ := strconv.Atoi(match[2])
		target = &OscColorTargetIndex{Index: idx}
	}
	return &OscColorResponse{
		Target: target,
		Rgb:    tcParseOscColorValue(match[3]),
	}
}

func tcParseOscColorValue(rawValue string) *RgbColor {
	value := js.Trim(rawValue)
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		if tcHex6Pattern.MatchString(hex) {
			rgb := tcHexToRgb(value)
			return &rgb
		}
		if tcHex12Pattern.MatchString(hex) {
			r := tcParseOscHexChannel(hex[0:4])
			g := tcParseOscHexChannel(hex[4:8])
			b := tcParseOscHexChannel(hex[8:12])
			if r != nil && g != nil && b != nil {
				return &RgbColor{R: float64(*r), G: float64(*g), B: float64(*b)}
			}
		}
		return nil
	}

	rgbValue := tcRgbaPrefixPattern.ReplaceAllString(value, "")
	parts := strings.Split(rgbValue, "/")
	if len(parts) < 3 {
		return nil
	}
	r := tcParseOscHexChannel(parts[0])
	g := tcParseOscHexChannel(parts[1])
	b := tcParseOscHexChannel(parts[2])
	if r != nil && g != nil && b != nil {
		return &RgbColor{R: float64(*r), G: float64(*g), B: float64(*b)}
	}
	return nil
}

// ParseTerminalColorSchemeReport parses a DECRPM color-scheme report.
// Nil means data is not a report. The returned scheme is "light" for mode 2 and "dark" otherwise.
func ParseTerminalColorSchemeReport(data string) *TerminalColorScheme {
	match := tcColorSchemeReportRegex.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	scheme := TerminalColorSchemeDark
	if match[1] == "2" {
		scheme = TerminalColorSchemeLight
	}
	return &scheme
}
