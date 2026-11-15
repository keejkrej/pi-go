// Ported from packages/tui/src/colors.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

// colorsWs is one JS \s, as an RE2 character class.
const colorsWs = `[` + js.WhitespaceClass + `]`

// colorsNumberPattern is NUMBER_PATTERN.
const colorsNumberPattern = `[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?`

// colorsHexPattern is the #rgb / #rrggbb match (flag i).
const colorsHexPattern = `(?i)^#([\da-f]{3}|[\da-f]{6})$`

// colorsOklchPattern is OKLCH_PATTERN (flag i).
const colorsOklchPattern = `(?i)^oklch\(` + colorsWs + `*(` + colorsNumberPattern + `)(%)?` + colorsWs + `+(` + colorsNumberPattern + `)` + colorsWs + `+(` + colorsNumberPattern + `)(?:deg)?` + colorsWs + `*\)$`

// colorsOkhslPattern is OKHSL_PATTERN (flag i).
const colorsOkhslPattern = `(?i)^okhsl\(` + colorsWs + `*(` + colorsNumberPattern + `)(?:deg)?` + colorsWs + `+(` + colorsNumberPattern + `)(%)?` + colorsWs + `+(` + colorsNumberPattern + `)(%)?` + colorsWs + `*\)$`

// colorsBasicColors is ANSI indexes 0-15.
var colorsBasicColors = []RgbColor{
	{R: 0, G: 0, B: 0},
	{R: 128, G: 0, B: 0},
	{R: 0, G: 128, B: 0},
	{R: 128, G: 128, B: 0},
	{R: 0, G: 0, B: 128},
	{R: 128, G: 0, B: 128},
	{R: 0, G: 128, B: 128},
	{R: 192, G: 192, B: 192},
	{R: 128, G: 128, B: 128},
	{R: 255, G: 0, B: 0},
	{R: 0, G: 255, B: 0},
	{R: 255, G: 255, B: 0},
	{R: 0, G: 0, B: 255},
	{R: 255, G: 0, B: 255},
	{R: 0, G: 255, B: 255},
	{R: 255, G: 255, B: 255},
}

// colorsCubeValues is the 6-step color cube (indexes 16-231).
var colorsCubeValues = []float64{0, 95, 135, 175, 215, 255}

// colorsGrayValues is ANSI indexes 232-255: 8 + index*10.
var colorsGrayValues = []float64{
	8, 18, 28, 38, 48, 58, 68, 78, 88, 98, 108, 118,
	128, 138, 148, 158, 168, 178, 188, 198, 208, 218, 228, 238,
}

// IndexedColor is an ANSI indexed color, 0-255.
type IndexedColor struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

func (*IndexedColor) isColor() {}

// RgbColorValue is an sRGB color. Channels are 0-255 and may be fractional.
type RgbColorValue struct {
	Kind string  `json:"kind"`
	R    float64 `json:"r"`
	G    float64 `json:"g"`
	B    float64 `json:"b"`
}

func (*RgbColorValue) isColor() {}

// OklchColorValue is an OKLCH color. L is 0-1, C is non-negative, H is degrees in [0, 360).
type OklchColorValue struct {
	Kind string  `json:"kind"`
	L    float64 `json:"l"`
	C    float64 `json:"c"`
	H    float64 `json:"h"`
}

func (*OklchColorValue) isColor() {}

// Color is a concrete color: *IndexedColor, *RgbColorValue, or *OklchColorValue.
// Every color can be converted to sRGB, so color math never fails.
type Color interface{ isColor() }

// TerminalColorMode is the ANSI color depth used when painting.
type TerminalColorMode string

const (
	TerminalColorMode256color  TerminalColorMode = "256color"
	TerminalColorModeTruecolor TerminalColorMode = "truecolor"
)

// ColorMixSpace is the space MixColors interpolates in.
type ColorMixSpace string

const (
	ColorMixSpaceOklch ColorMixSpace = "oklch"
	ColorMixSpaceSrgb  ColorMixSpace = "srgb"
)

// OklchChannels is OKLCH lightness, chroma, and hue in degrees.
type OklchChannels struct {
	L float64 `json:"l"`
	C float64 `json:"c"`
	H float64 `json:"h"`
}

// OkhslChannels is OKHSL hue in degrees, with saturation and lightness 0-1.
// Saturation is relative to the most the sRGB gamut allows at that hue and lightness, so every value is in gamut.
type OkhslChannels struct {
	H float64 `json:"h"`
	S float64 `json:"s"`
	L float64 `json:"l"`
}

// TextAttributes is the SGR attributes of styled text. Nil means unset.
type TextAttributes struct {
	Bold          *bool `json:"bold,omitzero"`
	Dim           *bool `json:"dim,omitzero"`
	Italic        *bool `json:"italic,omitzero"`
	Underline     *bool `json:"underline,omitzero"`
	Inverse       *bool `json:"inverse,omitzero"`
	Strikethrough *bool `json:"strikethrough,omitzero"`
}

// TextStyle is TextAttributes plus optional foreground and background.
// Field order is the TextStyle interface: attributes, then fg, then bg.
type TextStyle struct {
	TextAttributes
	Fg Color `json:"fg,omitzero"`
	Bg Color `json:"bg,omitzero"`
}

var (
	colorsHexRe   = regexp.MustCompile(colorsHexPattern)
	colorsOklchRe = regexp.MustCompile(colorsOklchPattern)
	colorsOkhslRe = regexp.MustCompile(colorsOkhslPattern)
)

func colorsRequireFinite(value float64, name string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", name)
	}
	return nil
}

// NewIndexedColor is TS indexedColor.
// The function is NewIndexedColor because the result type keeps the name IndexedColor.
func NewIndexedColor(index int) (*IndexedColor, error) {
	if index < 0 || index > 255 {
		return nil, fmt.Errorf("ANSI color index must be an integer from 0 to 255: %d", index)
	}
	return &IndexedColor{Kind: "indexed", Index: index}, nil
}

// NewRgbColor is TS rgbColor. Channels must be finite and in 0..255.
// The function is NewRgbColor so the RgbColor type keeps its name.
func NewRgbColor(r float64, g float64, b float64) (*RgbColorValue, error) {
	for _, ch := range []struct {
		name  string
		value float64
	}{
		{"r", r},
		{"g", g},
		{"b", b},
	} {
		if err := colorsRequireFinite(ch.value, ch.name); err != nil {
			return nil, err
		}
		if ch.value < 0 || ch.value > 255 {
			return nil, fmt.Errorf("%s must be between 0 and 255: %v", ch.name, ch.value)
		}
	}
	return &RgbColorValue{Kind: "rgb", R: r, G: g, B: b}, nil
}

// OklchColor is TS oklchColor. Hue is wrapped into [0, 360).
func OklchColor(l float64, c float64, h float64) (*OklchColorValue, error) {
	if err := colorsRequireFinite(l, "l"); err != nil {
		return nil, err
	}
	if err := colorsRequireFinite(c, "c"); err != nil {
		return nil, err
	}
	if err := colorsRequireFinite(h, "h"); err != nil {
		return nil, err
	}
	if l < 0 || l > 1 {
		return nil, fmt.Errorf("l must be between 0 and 1: %v", l)
	}
	if c < 0 {
		return nil, fmt.Errorf("c must not be negative: %v", c)
	}
	wrappedH := math.Mod(math.Mod(h, 360)+360, 360)
	if wrappedH == 360 || wrappedH == 0 {
		wrappedH = 0
	}
	return &OklchColorValue{Kind: "oklch", L: l, C: c, H: wrappedH}, nil
}

// OkhslColor is an OKHSL color converted to sRGB.
// Saturation is relative to the sRGB gamut at the hue and lightness, so equal saturation looks equally colorful across hues and lightness.
// h is hue in degrees, s is saturation 0-1, and l is lightness 0-1.
func OkhslColor(h float64, s float64, l float64) (*RgbColorValue, error) {
	if err := colorsRequireFinite(h, "h"); err != nil {
		return nil, err
	}
	if err := colorsRequireFinite(s, "s"); err != nil {
		return nil, err
	}
	if err := colorsRequireFinite(l, "l"); err != nil {
		return nil, err
	}
	if s < 0 || s > 1 {
		return nil, fmt.Errorf("s must be between 0 and 1: %v", s)
	}
	if l < 0 || l > 1 {
		return nil, fmt.Errorf("l must be between 0 and 1: %v", l)
	}
	rgb := OkhslToRgb(h, s, l)
	return NewRgbColor(rgb.R, rgb.G, rgb.B)
}

// ColorToOkhsl converts color to OKHSL channels.
func ColorToOkhsl(color Color) OkhslChannels {
	return RgbToOkhsl(ColorToRgb(color))
}

// ParseColor parses a #rgb or #rrggbb hex color, an oklch() or okhsl() string, or an ANSI index.
// value is a string, or a numeric ANSI index (int or float64).
func ParseColor(value any) (Color, error) {
	switch v := value.(type) {
	case int:
		return NewIndexedColor(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 0 || v > 255 {
			return nil, fmt.Errorf("ANSI color index must be an integer from 0 to 255: %v", v)
		}
		return NewIndexedColor(int(v))
	case string:
		if hex := colorsHexRe.FindStringSubmatch(v); hex != nil {
			digits := hex[1]
			if len(digits) == 3 {
				digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
			}
			r, _ := strconv.ParseInt(digits[0:2], 16, 64)
			g, _ := strconv.ParseInt(digits[2:4], 16, 64)
			b, _ := strconv.ParseInt(digits[4:6], 16, 64)
			return NewRgbColor(float64(r), float64(g), float64(b))
		}
		if oklch := colorsOklchRe.FindStringSubmatch(v); oklch != nil {
			l, err := strconv.ParseFloat(oklch[1], 64)
			if err != nil && !math.IsInf(l, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			if oklch[2] != "" {
				l /= 100
			}
			c, err := strconv.ParseFloat(oklch[3], 64)
			if err != nil && !math.IsInf(c, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			h, err := strconv.ParseFloat(oklch[4], 64)
			if err != nil && !math.IsInf(h, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			return OklchColor(l, c, h)
		}
		if okhsl := colorsOkhslRe.FindStringSubmatch(v); okhsl != nil {
			h, err := strconv.ParseFloat(okhsl[1], 64)
			if err != nil && !math.IsInf(h, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			s, err := strconv.ParseFloat(okhsl[2], 64)
			if err != nil && !math.IsInf(s, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			if okhsl[3] != "" {
				s /= 100
			}
			l, err := strconv.ParseFloat(okhsl[4], 64)
			if err != nil && !math.IsInf(l, 0) {
				return nil, fmt.Errorf("Invalid color value: %s", v)
			}
			if okhsl[5] != "" {
				l /= 100
			}
			return OkhslColor(h, s, l)
		}
		return nil, fmt.Errorf("Invalid color value: %s", v)
	default:
		return nil, fmt.Errorf("Invalid color value: %v", value)
	}
}

func colorsIndexedToRgb(index int) RgbColor {
	if index < 16 {
		return colorsBasicColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		return RgbColor{
			R: colorsCubeValues[cubeIndex/36],
			G: colorsCubeValues[(cubeIndex%36)/6],
			B: colorsCubeValues[cubeIndex%6],
		}
	}
	gray := float64(8 + (index-232)*10)
	return RgbColor{R: gray, G: gray, B: gray}
}

func colorsIsInSrgbGamut(linear [3]float64) bool {
	const epsilon = 1e-7
	for _, channel := range linear {
		if channel < -epsilon || channel > 1+epsilon {
			return false
		}
	}
	return true
}

func colorsOklchToRgb(channels OklchChannels) RgbColor {
	// Gamut mapping keeps the hue fixed, so its direction is computed once and scaled by chroma.
	radians := (channels.H * math.Pi) / 180
	cos := math.Cos(radians)
	sin := math.Sin(radians)
	atChroma := func(chroma float64) [3]float64 {
		return OklabToLinearSrgb([3]float64{channels.L, chroma * cos, chroma * sin})
	}

	direct := atChroma(channels.C)
	if colorsIsInSrgbGamut(direct) {
		return LinearSrgbToRgb(direct)
	}

	// Reduce chroma until the color fits. The achromatic color is always in gamut, so it is the
	// fallback when no bisection step fits, e.g. `oklch(100% 0.3 150)` must map to white.
	linear := atChroma(0)
	low := 0.0
	high := channels.C
	for index := 0; index < 20; index++ {
		chroma := (low + high) / 2
		candidate := atChroma(chroma)
		if colorsIsInSrgbGamut(candidate) {
			low = chroma
			linear = candidate
		} else {
			high = chroma
		}
	}
	return LinearSrgbToRgb(linear)
}

// ColorToRgb converts color to sRGB channels. Indexed and OKLCH colors are resolved; RGB channels are copied.
func ColorToRgb(color Color) RgbColor {
	switch c := color.(type) {
	case *IndexedColor:
		return colorsIndexedToRgb(c.Index)
	case *RgbColorValue:
		return RgbColor{R: c.R, G: c.G, B: c.B}
	case *OklchColorValue:
		return colorsOklchToRgb(OklchChannels{L: c.L, C: c.C, H: c.H})
	default:
		return RgbColor{}
	}
}

// ColorToOklch converts color to OKLCH. An OKLCH color keeps its stored channels, including chroma outside the sRGB gamut.
func ColorToOklch(color Color) OklchChannels {
	if oklch, ok := color.(*OklchColorValue); ok {
		return OklchChannels{L: oklch.L, C: oklch.C, H: oklch.H}
	}
	lab := RgbToOklab(ColorToRgb(color))
	l, a, b := lab[0], lab[1], lab[2]
	h := math.Mod((math.Atan2(b, a)*180)/math.Pi+360, 360)
	if h == 360 || h == 0 {
		h = 0
	}
	return OklchChannels{L: l, C: math.Hypot(a, b), H: h}
}

// ColorToHex converts color to a #rrggbb string of rounded sRGB channels.
func ColorToHex(color Color) string {
	rgb := ColorToRgb(color)
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(rgb.R)), int(math.Round(rgb.G)), int(math.Round(rgb.B)))
}

// MixColors blends first toward second by amount (0..1).
// The zero ColorMixSpace means oklch, the TypeScript default. Any value other than ColorMixSpaceSrgb uses OKLCH.
func MixColors(first Color, second Color, amount float64, space ColorMixSpace) (Color, error) {
	if err := colorsRequireFinite(amount, "amount"); err != nil {
		return nil, err
	}
	if amount < 0 || amount > 1 {
		return nil, fmt.Errorf("amount must be between 0 and 1: %v", amount)
	}

	if space == ColorMixSpaceSrgb {
		a := ColorToRgb(first)
		b := ColorToRgb(second)
		return NewRgbColor(a.R+(b.R-a.R)*amount, a.G+(b.G-a.G)*amount, a.B+(b.B-a.B)*amount)
	}

	a := ColorToOklch(first)
	b := ColorToOklch(second)
	firstHue := a.H
	if a.C < 1e-7 {
		firstHue = b.H
	}
	secondHue := b.H
	if b.C < 1e-7 {
		secondHue = firstHue
	}
	hueDelta := math.Mod(secondHue-firstHue+540, 360) - 180
	return OklchColor(a.L+(b.L-a.L)*amount, a.C+(b.C-a.C)*amount, firstHue+hueDelta*amount)
}

func colorsFindClosest(values []float64, target float64) int {
	closestIndex := 0
	closestDistance := math.Inf(1)
	for index, v := range values {
		distance := math.Abs(target - v)
		if distance < closestDistance {
			closestIndex = index
			closestDistance = distance
		}
	}
	return closestIndex
}

func colorsColorDistance(first RgbColor, second RgbColor) float64 {
	dr := first.R - second.R
	dg := first.G - second.G
	db := first.B - second.B
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func colorsRgbToAnsi256(color RgbColor) int {
	rIndex := colorsFindClosest(colorsCubeValues, color.R)
	gIndex := colorsFindClosest(colorsCubeValues, color.G)
	bIndex := colorsFindClosest(colorsCubeValues, color.B)
	cubeColor := RgbColor{R: colorsCubeValues[rIndex], G: colorsCubeValues[gIndex], B: colorsCubeValues[bIndex]}
	cubeIndex := 16 + 36*rIndex + 6*gIndex + bIndex

	gray := math.Round(0.299*color.R + 0.587*color.G + 0.114*color.B)
	grayOffset := colorsFindClosest(colorsGrayValues, gray)
	grayValue := colorsGrayValues[grayOffset]
	spread := math.Max(color.R, math.Max(color.G, color.B)) - math.Min(color.R, math.Min(color.G, color.B))
	if spread < 10 && colorsColorDistance(color, RgbColor{R: grayValue, G: grayValue, B: grayValue}) < colorsColorDistance(color, cubeColor) {
		return 232 + grayOffset
	}
	return cubeIndex
}

func colorsColorAnsi(color Color, mode TerminalColorMode, background bool) string {
	prefix := 38
	if background {
		prefix = 48
	}
	if indexed, ok := color.(*IndexedColor); ok {
		return fmt.Sprintf("\x1b[%d;5;%dm", prefix, indexed.Index)
	}
	rgb := ColorToRgb(color)
	if mode == TerminalColorModeTruecolor {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", prefix, int(math.Round(rgb.R)), int(math.Round(rgb.G)), int(math.Round(rgb.B)))
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", prefix, colorsRgbToAnsi256(rgb))
}

// ForegroundAnsi returns the SGR sequence that sets the foreground to color.
func ForegroundAnsi(color Color, mode TerminalColorMode) string {
	return colorsColorAnsi(color, mode, false)
}

// BackgroundAnsi returns the SGR sequence that sets the background to color.
func BackgroundAnsi(color Color, mode TerminalColorMode) string {
	return colorsColorAnsi(color, mode, true)
}

// StyleText paints text with options in mode.
func StyleText(text string, options TextStyle, mode TerminalColorMode) string {
	var fgAnsi, bgAnsi *string
	if options.Fg != nil {
		s := ForegroundAnsi(options.Fg, mode)
		fgAnsi = &s
	}
	if options.Bg != nil {
		s := BackgroundAnsi(options.Bg, mode)
		bgAnsi = &s
	}
	return StyleTextWithAnsi(text, fgAnsi, bgAnsi, options.TextAttributes)
}

// StyleTextWithAnsi is StyleText with precomputed color escape sequences, for example cached theme colors.
// Colors in options are ignored. Nil or empty fgAnsi and bgAnsi mean no color sequence.
func StyleTextWithAnsi(text string, fgAnsi *string, bgAnsi *string, options TextAttributes) string {
	var prefix strings.Builder
	suffix := ""
	if fgAnsi != nil && *fgAnsi != "" {
		prefix.WriteString(*fgAnsi)
		suffix = "\x1b[39m"
	}
	if bgAnsi != nil && *bgAnsi != "" {
		prefix.WriteString(*bgAnsi)
		suffix = "\x1b[49m" + suffix
	}
	bold := options.Bold != nil && *options.Bold
	dim := options.Dim != nil && *options.Dim
	if bold {
		prefix.WriteString("\x1b[1m")
	}
	if dim {
		prefix.WriteString("\x1b[2m")
	}
	if bold || dim {
		suffix = "\x1b[22m" + suffix
	}
	if options.Italic != nil && *options.Italic {
		prefix.WriteString("\x1b[3m")
		suffix = "\x1b[23m" + suffix
	}
	if options.Underline != nil && *options.Underline {
		prefix.WriteString("\x1b[4m")
		suffix = "\x1b[24m" + suffix
	}
	if options.Inverse != nil && *options.Inverse {
		prefix.WriteString("\x1b[7m")
		suffix = "\x1b[27m" + suffix
	}
	if options.Strikethrough != nil && *options.Strikethrough {
		prefix.WriteString("\x1b[9m")
		suffix = "\x1b[29m" + suffix
	}
	return prefix.String() + text + suffix
}
