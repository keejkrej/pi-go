// Ported from packages/tui/src/colors.ts (pi v1.0.0).

package tui

import "github.com/keejkrej/pi-go/internal/js"

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

func colorsRequireFinite(value float64, name string) error {
	panic("unported: colorsRequireFinite")
}

// NewIndexedColor is TS indexedColor.
// The function is NewIndexedColor because the result type keeps the name IndexedColor.
func NewIndexedColor(index int) (*IndexedColor, error) {
	panic("unported: NewIndexedColor")
}

// NewRgbColor is TS rgbColor. Channels must be finite and in 0..255.
// The function is NewRgbColor so the RgbColor type keeps its name.
func NewRgbColor(r float64, g float64, b float64) (*RgbColorValue, error) {
	panic("unported: NewRgbColor")
}

// OklchColor is TS oklchColor. Hue is wrapped into [0, 360).
func OklchColor(l float64, c float64, h float64) (*OklchColorValue, error) {
	panic("unported: OklchColor")
}

// OkhslColor is an OKHSL color converted to sRGB.
// Saturation is relative to the sRGB gamut at the hue and lightness, so equal saturation looks equally colorful across hues and lightness.
// h is hue in degrees, s is saturation 0-1, and l is lightness 0-1.
func OkhslColor(h float64, s float64, l float64) (*RgbColorValue, error) {
	panic("unported: OkhslColor")
}

// ColorToOkhsl converts color to OKHSL channels.
func ColorToOkhsl(color Color) OkhslChannels {
	panic("unported: ColorToOkhsl")
}

// ParseColor parses a #rgb or #rrggbb hex color, an oklch() or okhsl() string, or an ANSI index.
// value is a string, or a numeric ANSI index (int or float64).
func ParseColor(value any) (Color, error) {
	panic("unported: ParseColor")
}

func colorsIndexedToRgb(index int) RgbColor {
	panic("unported: colorsIndexedToRgb")
}

func colorsIsInSrgbGamut(linear [3]float64) bool {
	panic("unported: colorsIsInSrgbGamut")
}

func colorsOklchToRgb(channels OklchChannels) RgbColor {
	panic("unported: colorsOklchToRgb")
}

// ColorToRgb converts color to sRGB channels. Indexed and OKLCH colors are resolved; RGB channels are copied.
func ColorToRgb(color Color) RgbColor {
	panic("unported: ColorToRgb")
}

// ColorToOklch converts color to OKLCH. An OKLCH color keeps its stored channels, including chroma outside the sRGB gamut.
func ColorToOklch(color Color) OklchChannels {
	panic("unported: ColorToOklch")
}

// ColorToHex converts color to a #rrggbb string of rounded sRGB channels.
func ColorToHex(color Color) string {
	panic("unported: ColorToHex")
}

// MixColors blends first toward second by amount (0..1).
// The zero ColorMixSpace means oklch, the TypeScript default. Any value other than ColorMixSpaceSrgb uses OKLCH.
func MixColors(first Color, second Color, amount float64, space ColorMixSpace) (Color, error) {
	panic("unported: MixColors")
}

func colorsFindClosest(values []float64, target float64) int {
	panic("unported: colorsFindClosest")
}

func colorsColorDistance(first RgbColor, second RgbColor) float64 {
	dr := first.R - second.R
	dg := first.G - second.G
	db := first.B - second.B
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func colorsRgbToAnsi256(color RgbColor) int {
	panic("unported: colorsRgbToAnsi256")
}

func colorsColorAnsi(color Color, mode TerminalColorMode, background bool) string {
	panic("unported: colorsColorAnsi")
}

// ForegroundAnsi returns the SGR sequence that sets the foreground to color.
func ForegroundAnsi(color Color, mode TerminalColorMode) string {
	panic("unported: ForegroundAnsi")
}

// BackgroundAnsi returns the SGR sequence that sets the background to color.
func BackgroundAnsi(color Color, mode TerminalColorMode) string {
	panic("unported: BackgroundAnsi")
}

// StyleText paints text with options in mode.
func StyleText(text string, options TextStyle, mode TerminalColorMode) string {
	panic("unported: StyleText")
}

// StyleTextWithAnsi is StyleText with precomputed color escape sequences, for example cached theme colors.
// Colors in options are ignored. Nil or empty fgAnsi and bgAnsi mean no color sequence.
func StyleTextWithAnsi(text string, fgAnsi *string, bgAnsi *string, options TextAttributes) string {
	panic("unported: StyleTextWithAnsi")
}
