// Ported from packages/tui/src/terminal-colors.ts (pi v1.0.0).

package tui

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
	panic("unported: tcHexToRgb")
}

// tcParseOscHexChannel parses one OSC hex channel. Nil means the channel is not hex.
func tcParseOscHexChannel(channel string) *int {
	panic("unported: tcParseOscHexChannel")
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
	panic("unported: ParseOscColorResponse")
}

func tcParseOscColorValue(rawValue string) *RgbColor {
	panic("unported: tcParseOscColorValue")
}

// ParseTerminalColorSchemeReport parses a DECRPM color-scheme report.
// Nil means data is not a report. The returned scheme is "light" for mode 2 and "dark" otherwise.
func ParseTerminalColorSchemeReport(data string) *TerminalColorScheme {
	panic("unported: ParseTerminalColorSchemeReport")
}
