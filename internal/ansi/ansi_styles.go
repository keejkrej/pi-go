// Ported from chalk@6.0.0 source/vendor/ansi-styles/index.js.

package ansi

import (
	"math"
	"regexp"
	"strconv"
)

const (
	ansiBackgroundOffset = 10
	ansiUnderlineOffset  = 20
)

// StyleCodes is one ansi-styles entry: the escape that opens the style and the one that closes it.
type StyleCodes struct {
	Open  string
	Close string
}

type styleDef struct {
	name  string
	open  string // leading SGR parameter(s), e.g. "1", "4:3", "58;5;1"
	close int
}

var modifierDefs = []styleDef{
	{"reset", "0", 0},
	// 21 isn't widely supported and 22 does the same thing
	{"bold", "1", 22},
	{"dim", "2", 22},
	{"italic", "3", 23},
	{"underline", "4", 24},
	// Extended underline styles (`SGR 4:x` sub-parameters). Not in upstream `ansi-styles`.
	{"underlineDouble", "4:2", 24},
	{"underlineCurly", "4:3", 24},
	{"underlineDotted", "4:4", 24},
	{"underlineDashed", "4:5", 24},
	{"overline", "53", 55},
	{"inverse", "7", 27},
	{"hidden", "8", 28},
	{"strikethrough", "9", 29},
}

var colorDefs = []styleDef{
	{"black", "30", 39},
	{"red", "31", 39},
	{"green", "32", 39},
	{"yellow", "33", 39},
	{"blue", "34", 39},
	{"magenta", "35", 39},
	{"cyan", "36", 39},
	{"white", "37", 39},

	// Bright color
	{"blackBright", "90", 39},
	{"gray", "90", 39}, // Alias of `blackBright`
	{"grey", "90", 39}, // Alias of `blackBright`
	{"redBright", "91", 39},
	{"greenBright", "92", 39},
	{"yellowBright", "93", 39},
	{"blueBright", "94", 39},
	{"magentaBright", "95", 39},
	{"cyanBright", "96", 39},
	{"whiteBright", "97", 39},
}

var bgColorDefs = []styleDef{
	{"bgBlack", "40", 49},
	{"bgRed", "41", 49},
	{"bgGreen", "42", 49},
	{"bgYellow", "43", 49},
	{"bgBlue", "44", 49},
	{"bgMagenta", "45", 49},
	{"bgCyan", "46", 49},
	{"bgWhite", "47", 49},

	// Bright color
	{"bgBlackBright", "100", 49},
	{"bgGray", "100", 49}, // Alias of `bgBlackBright`
	{"bgGrey", "100", 49}, // Alias of `bgBlackBright`
	{"bgRedBright", "101", 49},
	{"bgGreenBright", "102", 49},
	{"bgYellowBright", "103", 49},
	{"bgBlueBright", "104", 49},
	{"bgMagentaBright", "105", 49},
	{"bgCyanBright", "106", 49},
	{"bgWhiteBright", "107", 49},
}

// Underline color (`SGR 58`/`59`). Not in upstream `ansi-styles`.
var underlineColorDefs = []styleDef{
	{"underlineBlack", "58;5;0", 59},
	{"underlineRed", "58;5;1", 59},
	{"underlineGreen", "58;5;2", 59},
	{"underlineYellow", "58;5;3", 59},
	{"underlineBlue", "58;5;4", 59},
	{"underlineMagenta", "58;5;5", 59},
	{"underlineCyan", "58;5;6", 59},
	{"underlineWhite", "58;5;7", 59},

	// Bright color
	{"underlineBlackBright", "58;5;8", 59},
	{"underlineGray", "58;5;8", 59}, // Alias of `underlineBlackBright`
	{"underlineGrey", "58;5;8", 59}, // Alias of `underlineBlackBright`
	{"underlineRedBright", "58;5;9", 59},
	{"underlineGreenBright", "58;5;10", 59},
	{"underlineYellowBright", "58;5;11", 59},
	{"underlineBlueBright", "58;5;12", 59},
	{"underlineMagentaBright", "58;5;13", 59},
	{"underlineCyanBright", "58;5;14", 59},
	{"underlineWhiteBright", "58;5;15", 59},
}

func styleNames(defs []styleDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.name
	}
	return names
}

// ModifierNames lists the modifier style names in ansi-styles order.
var ModifierNames = styleNames(modifierDefs)

// ForegroundColorNames lists the foreground color style names in ansi-styles order.
var ForegroundColorNames = styleNames(colorDefs)

// BackgroundColorNames lists the background color style names in ansi-styles order.
var BackgroundColorNames = styleNames(bgColorDefs)

// UnderlineColorNames lists the underline color style names in ansi-styles order.
var UnderlineColorNames = styleNames(underlineColorDefs)

// ColorNames is ForegroundColorNames followed by BackgroundColorNames.
var ColorNames = append(append([]string{}, ForegroundColorNames...), BackgroundColorNames...)

// Modifiers is the deprecated alias of ModifierNames.
var Modifiers = ModifierNames

// ForegroundColors is the deprecated alias of ForegroundColorNames.
var ForegroundColors = ForegroundColorNames

// BackgroundColors is the deprecated alias of BackgroundColorNames.
var BackgroundColors = BackgroundColorNames

// Colors is the deprecated alias of ColorNames.
var Colors = ColorNames

var (
	styleTable = map[string]StyleCodes{}
	// Codes maps the leading SGR parameter of every style to its closing parameter (ansi-styles `codes`).
	Codes = map[int]int{}
)

func init() {
	for _, group := range [][]styleDef{modifierDefs, colorDefs, bgColorDefs, underlineColorDefs} {
		for _, d := range group {
			styleTable[d.name] = StyleCodes{Open: "\x1b[" + d.open + "m", Close: "\x1b[" + strconv.Itoa(d.close) + "m"}
			// Only the leading SGR parameter identifies a style, so `4:3` and `58;5;1` are keyed as `4` and `58`.
			Codes[ansiStylesLeadingInt(d.open)] = d.close
		}
	}
}

// ansiStylesLeadingInt is Number.parseInt(s, 10) for the non-negative decimal prefixes used in the style table.
func ansiStylesLeadingInt(s string) int {
	n := 0
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// Style returns the open/close escapes of a named ansi-styles entry (for example "bold" or "bgRed").
func Style(name string) (StyleCodes, bool) {
	s, ok := styleTable[name]
	return s, ok
}

// Group close codes (ansi-styles `color.close`, `bgColor.close`, `underlineColor.close`).
const (
	ColorClose          = "\x1b[39m"
	BgColorClose        = "\x1b[49m"
	UnderlineColorClose = "\x1b[59m"
)

func wrapAnsi16(offset int) func(code int) string {
	return func(code int) string { return "\x1b[" + strconv.Itoa(code+offset) + "m" }
}

func wrapAnsi256(offset int) func(code int) string {
	return func(code int) string { return "\x1b[" + strconv.Itoa(38+offset) + ";5;" + strconv.Itoa(code) + "m" }
}

func wrapAnsi16m(offset int) func(red, green, blue int) string {
	return func(red, green, blue int) string {
		return "\x1b[" + strconv.Itoa(38+offset) + ";2;" + strconv.Itoa(red) + ";" + strconv.Itoa(green) + ";" + strconv.Itoa(blue) + "m"
	}
}

// `SGR 58` has no basic 16-color form, so the basic color code is mapped to its palette index instead.
func wrapUnderlineAnsi(code int) string {
	idx := code - 90 + 8
	if code < 90 {
		idx = code - 30
	}
	return "\x1b[58;5;" + strconv.Itoa(idx) + "m"
}

// Escape builders per color group (ansi-styles `color.ansi`, `color.ansi256`, `color.ansi16m`, ...).
var (
	ColorAnsi              = wrapAnsi16(0)
	ColorAnsi256           = wrapAnsi256(0)
	ColorAnsi16m           = wrapAnsi16m(0)
	BgColorAnsi            = wrapAnsi16(ansiBackgroundOffset)
	BgColorAnsi256         = wrapAnsi256(ansiBackgroundOffset)
	BgColorAnsi16m         = wrapAnsi16m(ansiBackgroundOffset)
	UnderlineColorAnsi     = wrapUnderlineAnsi
	UnderlineColorAnsi256  = wrapAnsi256(ansiUnderlineOffset)
	UnderlineColorAnsi16m  = wrapAnsi16m(ansiUnderlineOffset)
	ansiStylesHexColorExpr = regexp.MustCompile(`(?i)[\da-f]{6}|[\da-f]{3}`)
)

// jsMathRound is Math.round: round half toward +Infinity.
func jsMathRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// RgbToAnsi256 converts an RGB color to the closest xterm 256-color palette index.
// From https://github.com/Qix-/color-convert/blob/3f0e0d4e92e235796ccb17f6e85c72094a651f49/conversions.js
func RgbToAnsi256(red, green, blue int) int {
	// We use the extended greyscale palette here, with the exception of
	// black and white. normal palette only has 4 greyscale shades.
	if red == green && green == blue {
		if red < 8 {
			return 16
		}
		if red > 248 {
			return 231
		}
		return int(jsMathRound((float64(red-8)/247)*24)) + 232
	}

	return 16 +
		36*int(jsMathRound(float64(red)/255*5)) +
		6*int(jsMathRound(float64(green)/255*5)) +
		int(jsMathRound(float64(blue)/255*5))
}

// HexToRgb parses the first 6- or 3-digit hex run of hex; it returns [0, 0, 0] when there is none.
func HexToRgb(hex string) [3]int {
	colorString := ansiStylesHexColorExpr.FindString(hex)
	if colorString == "" {
		return [3]int{0, 0, 0}
	}
	if len(colorString) == 3 {
		doubled := make([]byte, 0, 6)
		for i := 0; i < 3; i++ {
			doubled = append(doubled, colorString[i], colorString[i])
		}
		colorString = string(doubled)
	}
	integer, _ := strconv.ParseInt(colorString, 16, 64)
	return [3]int{int((integer >> 16) & 0xFF), int((integer >> 8) & 0xFF), int(integer & 0xFF)}
}

// HexToAnsi256 converts a hex color to the closest xterm 256-color palette index.
func HexToAnsi256(hex string) int {
	rgb := HexToRgb(hex)
	return RgbToAnsi256(rgb[0], rgb[1], rgb[2])
}

// Ansi256ToAnsi converts a 256-color palette index to the closest basic (16-color) SGR foreground code.
func Ansi256ToAnsi(code int) int {
	if code < 8 {
		return 30 + code
	}
	if code < 16 {
		return 90 + (code - 8)
	}

	var red, green, blue float64
	if code >= 232 {
		red = (float64((code-232)*10) + 8) / 255
		green = red
		blue = red
	} else {
		code -= 16
		remainder := code % 36
		red = math.Floor(float64(code)/36) / 5
		green = math.Floor(float64(remainder)/6) / 5
		blue = float64(remainder%6) / 5
	}

	value := math.Max(red, math.Max(green, blue)) * 2
	if value == 0 {
		return 30
	}

	result := 30 + ((int(jsMathRound(blue)) << 2) | (int(jsMathRound(green)) << 1) | int(jsMathRound(red)))
	if value == 2 {
		result += 60
	}
	return result
}

// RgbToAnsi converts an RGB color to the closest basic (16-color) SGR foreground code.
func RgbToAnsi(red, green, blue int) int {
	return Ansi256ToAnsi(RgbToAnsi256(red, green, blue))
}

// HexToAnsi converts a hex color to the closest basic (16-color) SGR foreground code.
func HexToAnsi(hex string) int {
	return Ansi256ToAnsi(HexToAnsi256(hex))
}
