// Ported from packages/coding-agent/src/modes/interactive/theme/system-theme.ts (pi v1.5.2).

package theme

import (
	"fmt"
	"math"
	"slices"

	"github.com/keejkrej/pi-go/tui"
)

// SystemThemeName is the built-in adaptive terminal system theme identifier.
const SystemThemeName = "system"

// SystemThemeInput holds queried terminal colors and generation hints for the adaptive system theme.
type SystemThemeInput struct {
	Background     *tui.RgbColor            `json:"background,omitzero"`
	Foreground     *tui.RgbColor            `json:"foreground,omitzero"`
	Palette        []tui.RgbColor           `json:"palette,omitzero"`
	AppearanceHint *tui.TerminalColorScheme `json:"appearanceHint,omitzero"`
	Saturation     *float64                 `json:"saturation,omitzero"`
}

// SystemThemeResult holds generated system theme colors, detected appearance, and tokens
// that should use terminal faint/dim SGR 2 formatting when no terminal background is available.
type SystemThemeResult struct {
	Appearance tui.TerminalColorScheme
	Colors     map[string]any
	Dim        []string
}

type stSaturationRange struct {
	min float64
	max float64
}

type stFamily struct {
	hue        float64
	saturation stSaturationRange
	slot       int
}

type stCurve struct {
	coefficients [6]float64
	reachable    [2]float64
}

type stLevelCurves struct {
	dark  stCurve
	light stCurve
}

type stRule struct {
	token string
	on    []string
	level string
}

type stSourceColor struct {
	tui.OkhslChannels
	chroma float64
}

const stTextMinimumWcagContrast = 4.5

var (
	stBlack = tui.RgbColor{R: 0, G: 0, B: 0}
	stWhite = tui.RgbColor{R: 255, G: 255, B: 255}

	stFamilies = map[string]stFamily{
		"neutral":            {hue: 231.49, saturation: stSaturationRange{min: 0.02, max: 0.08}, slot: 8},
		"blue":               {hue: 231.49, saturation: stSaturationRange{min: 0.1, max: 0.68}, slot: 4},
		"green":              {hue: 158.68, saturation: stSaturationRange{min: 0.1, max: 0.76}, slot: 2},
		"red":                {hue: 20, saturation: stSaturationRange{min: 0.1, max: 0.92}, slot: 1},
		"yellow":             {hue: 82.36, saturation: stSaturationRange{min: 0.5, max: 1}, slot: 3},
		"orange":             {hue: 52, saturation: stSaturationRange{min: 0.12, max: 0.85}, slot: 3},
		"violet":             {hue: 295, saturation: stSaturationRange{min: 0.2, max: 0.6}, slot: 5},
		"calamine":           {hue: 202.43, saturation: stSaturationRange{min: 0.1, max: 0.74}, slot: 6},
		"thinkingSlate":      {hue: 231.49, saturation: stSaturationRange{min: 0.08, max: 0.2}, slot: 4},
		"thinkingBlue":       {hue: 231.49, saturation: stSaturationRange{min: 0.2, max: 0.45}, slot: 4},
		"thinkingPeriwinkle": {hue: 263.25, saturation: stSaturationRange{min: 0.3, max: 0.6}, slot: 6},
		"thinkingViolet":     {hue: 295, saturation: stSaturationRange{min: 0.4, max: 0.75}, slot: 5},
		"thinkingMagenta":    {hue: 337.5, saturation: stSaturationRange{min: 0.5, max: 0.85}, slot: 13},
		"thinkingRed":        {hue: 20, saturation: stSaturationRange{min: 0.95, max: 1}, slot: 1},
	}

	stTokenFamilies = map[string]string{
		BgSelected:           "blue",
		BgSearchMatch:        "orange",
		BgUserMessage:        "blue",
		BgCustomMessage:      "violet",
		BgToolPending:        "neutral",
		BgToolSuccess:        "green",
		BgToolError:          "red",
		FgText:               "neutral",
		FgUserMessageText:    "neutral",
		FgCustomMessageText:  "neutral",
		FgToolTitle:          "neutral",
		FgSyntaxOperator:     "neutral",
		FgSyntaxPunctuation:  "neutral",
		FgMuted:              "neutral",
		FgDim:                "neutral",
		FgThinkingText:       "neutral",
		FgToolOutput:         "neutral",
		FgMdLinkUrl:          "neutral",
		FgMdQuote:            "neutral",
		FgMdQuoteBorder:      "neutral",
		FgMdHr:               "neutral",
		FgMdCodeBlockBorder:  "neutral",
		FgToolDiffContext:    "neutral",
		FgSyntaxComment:      "neutral",
		FgScrollbarTrack:     "neutral",
		FgScrollbarThumb:     "neutral",
		FgSearchMatchText:    "neutral",
		FgBorderMuted:        "neutral",
		FgAccent:             "violet",
		FgBorderAccent:       "violet",
		FgCustomMessageLabel: "violet",
		FgMdCode:             "violet",
		FgMdListBullet:       "violet",
		FgSyntaxType:         "violet",
		FgBorder:             "blue",
		FgMdLink:             "blue",
		FgSyntaxKeyword:      "blue",
		FgSyntaxVariable:     "calamine",
		FgSuccess:            "green",
		FgMdCodeBlock:        "green",
		FgToolDiffAdded:      "green",
		FgBashMode:           "green",
		FgSyntaxNumber:       "green",
		FgError:              "red",
		FgToolDiffRemoved:    "red",
		FgWarning:            "yellow",
		FgMdHeading:          "yellow",
		FgSyntaxFunction:     "yellow",
		FgSyntaxString:       "orange",
		FgThinkingOff:        "neutral",
		FgThinkingMinimal:    "thinkingSlate",
		FgThinkingLow:        "thinkingBlue",
		FgThinkingMedium:     "thinkingPeriwinkle",
		FgThinkingHigh:       "thinkingViolet",
		FgThinkingXhigh:      "thinkingMagenta",
		FgThinkingMax:        "thinkingRed",
	}

	stTokenSlots = map[string]int{
		FgSyntaxString: 2,
		FgSyntaxNumber: 5,
		BgSearchMatch:  3,
	}

	stLevels = map[string]stLevelCurves{
		"panel": {
			dark:  stCurve{coefficients: [6]float64{0.29131, -0.39746, 2.33185, -0.85524, -1.2076, 0.86276}, reachable: [2]float64{0, 0.979}},
			light: stCurve{coefficients: [6]float64{-3.74073, 27.94549, -78.44258, 112.6798, -79.60015, 22.11277}, reachable: [2]float64{0.348, 1}},
		},
		"track": {
			dark:  stCurve{coefficients: [6]float64{0.39028, -0.23015, 0.83573, 2.43829, -4.38292, 2.01582}, reachable: [2]float64{0, 0.946}},
			light: stCurve{coefficients: [6]float64{-5.24921, 38.37322, -107.28833, 152.10005, -106.17127, 29.18061}, reachable: [2]float64{0.368, 1}},
		},
		"thinking0": {
			dark:  stCurve{coefficients: [6]float64{0.52988, -0.05809, -0.30924, 4.63567, -6.52933, 2.89108}, reachable: [2]float64{0, 0.873}},
			light: stCurve{coefficients: [6]float64{-28.27749, 182.85284, -469.62416, 603.15916, -384.59976, 97.35147}, reachable: [2]float64{0.51, 1}},
		},
		"thinking1": {
			dark:  stCurve{coefficients: [6]float64{0.55278, -0.03667, -0.45659, 4.95347, -6.90265, 3.0706}, reachable: [2]float64{0, 0.858}},
			light: stCurve{coefficients: [6]float64{-37.10484, 235.86282, -596.62344, 754.3633, -474.00763, 118.3551}, reachable: [2]float64{0.535, 1}},
		},
		"thinking2": {
			dark:  stCurve{coefficients: [6]float64{0.57486, -0.01765, -0.58987, 5.25227, -7.27175, 3.25532}, reachable: [2]float64{0, 0.842}},
			light: stCurve{coefficients: [6]float64{-59.89653, 377.05024, -945.07843, 1182.03145, -734.96375, 181.68658}, reachable: [2]float64{0.556, 1}},
		},
		"thinking3": {
			dark:  stCurve{coefficients: [6]float64{0.59621, -0.00062, -0.71148, 5.53588, -7.6392, 3.44606}, reachable: [2]float64{0, 0.827}},
			light: stCurve{coefficients: [6]float64{-72.07122, 445.84082, -1099.57352, 1353.88793, -829.53392, 202.26164}, reachable: [2]float64{0.58, 1}},
		},
		"thinking4": {
			dark:  stCurve{coefficients: [6]float64{0.61691, 0.01462, -0.82288, 5.80651, -8.00641, 3.64333}, reachable: [2]float64{0, 0.811}},
			light: stCurve{coefficients: [6]float64{-110.14338, 674.21488, -1645.75941, 2004.32367, -1215.15899, 293.3183}, reachable: [2]float64{0.6, 1}},
		},
		"thinking5": {
			dark:  stCurve{coefficients: [6]float64{0.63702, 0.02826, -0.92498, 6.06465, -8.37246, 3.84651}, reachable: [2]float64{0, 0.795}},
			light: stCurve{coefficients: [6]float64{-175.47701, 1063.54495, -2570.70594, 3098.80776, -1860.15527, 444.76392}, reachable: [2]float64{0.62, 1}},
		},
		"thinking6": {
			dark:  stCurve{coefficients: [6]float64{0.65658, 0.04044, -1.01835, 6.30989, -8.73529, 4.05439}, reachable: [2]float64{0, 0.779}},
			light: stCurve{coefficients: [6]float64{-183.81712, 1094.70055, -2602.68539, 3088.71276, -1826.91131, 430.75931}, reachable: [2]float64{0.643, 1}},
		},
		"subtle": {
			dark:  stCurve{coefficients: [6]float64{0.56762, -0.02475, -0.5383, 5.12628, -7.10931, 3.17324}, reachable: [2]float64{0, 0.848}},
			light: stCurve{coefficients: [6]float64{-232.85459, 1376.54473, -3249.11801, 3827.91186, -2248.29472, 526.55751}, reachable: [2]float64{0.657, 1}},
		},
		"thumb": {
			dark:  stCurve{coefficients: [6]float64{0.60323, 0.00278, -0.73328, 5.57157, -7.68067, 3.46933}, reachable: [2]float64{0, 0.823}},
			light: stCurve{coefficients: [6]float64{-82.89897, 511.01355, -1255.98095, 1540.76821, -940.68087, 228.58523}, reachable: [2]float64{0.586, 1}},
		},
		"readable": {
			dark:  stCurve{coefficients: [6]float64{0.66937, 0.04704, -1.06871, 6.43941, -8.9332, 4.17229}, reachable: [2]float64{0, 0.77}},
			light: stCurve{coefficients: [6]float64{-1554.52576, 8733.56817, -19604.93507, 21977.72696, -12300.99599, 2749.81288}, reachable: [2]float64{0.751, 1}},
		},
		"emphasis": {
			dark:  stCurve{coefficients: [6]float64{0.7303, 0.07695, -1.31626, 7.1681, -10.14436, 4.92846}, reachable: [2]float64{0, 0.712}},
			light: stCurve{coefficients: [6]float64{-4948.31942, 26870.91986, -58334.48399, 63280.17197, -34298.01053, 7430.30146}, reachable: [2]float64{0.811, 1}},
		},
		"textOnPanel": {
			dark:  stCurve{coefficients: [6]float64{0.86713, 0.05232, -0.89428, 4.79014, -5.5432, 1.75023}, reachable: [2]float64{0, 0.542}},
			light: stCurve{coefficients: [6]float64{-8570.89457, 43954.60805, -90084.00702, 92220.6791, -47152.15802, 9632.27113}, reachable: [2]float64{0.867, 1}},
		},
		"text": {
			dark:  stCurve{coefficients: [6]float64{0.89242, 0.02311, -0.44862, 2.34417, -0.06084, -2.63844}, reachable: [2]float64{0, 0.5}},
			light: stCurve{coefficients: [6]float64{-2004.67048, 6664.47299, -6060.70202, -1792.61209, 5133.82359, -1939.85583}, reachable: [2]float64{0.894, 1}},
		},
	}

	stToolPanels = []string{BgToolPending, BgToolSuccess, BgToolError}
	stMsgPanels  = []string{BgUserMessage, BgCustomMessage}
	stPanels     = []string{
		BgUserMessage,
		BgToolPending,
		BgToolSuccess,
		BgToolError,
		BgSelected,
		BgSearchMatch,
		BgCustomMessage,
	}
	stThinkingTokens = []string{
		FgThinkingOff,
		FgThinkingMinimal,
		FgThinkingLow,
		FgThinkingMedium,
		FgThinkingHigh,
		FgThinkingXhigh,
		FgThinkingMax,
	}
	stThinkingLevels = []string{
		"thinking0",
		"thinking1",
		"thinking2",
		"thinking3",
		"thinking4",
		"thinking5",
		"thinking6",
	}
	stForegroundTokens = []string{FgText, FgUserMessageText, FgToolTitle}

	stRules      []stRule
	stSolveOrder []string
)

const stForegroundLevel = "emphasis"

func stEachRule(tokens, on []string, level string) []stRule {
	out := make([]stRule, len(tokens))
	for i, tok := range tokens {
		out[i] = stRule{token: tok, on: on, level: level}
	}
	return out
}

func init() {
	bgOnly := []string{"background"}
	bgAndMsg := append([]string{"background"}, stMsgPanels...)
	bgAndTool := append([]string{"background"}, stToolPanels...)
	bgAndMsgAndTool := append(append([]string{"background"}, stMsgPanels...), stToolPanels...)
	bgAndSelectedAndTool := append([]string{"background", BgSelected}, stToolPanels...)
	bgAndSelectedAndCustomAndTool := append([]string{"background", BgSelected, BgCustomMessage}, stToolPanels...)

	stRules = append(stRules, stEachRule(stPanels, bgOnly, "panel")...)
	stRules = append(stRules,
		stRule{token: FgText, on: bgOnly, level: "text"},
		stRule{token: FgText, on: []string{BgSelected}, level: "textOnPanel"},
		stRule{token: FgUserMessageText, on: []string{BgUserMessage}, level: "textOnPanel"},
		stRule{token: FgToolTitle, on: stToolPanels, level: "textOnPanel"},
	)
	stRules = append(stRules, stEachRule([]string{FgAccent, FgSuccess, FgError, FgWarning}, bgAndSelectedAndTool, "readable")...)
	stRules = append(stRules,
		stRule{token: FgMuted, on: bgAndSelectedAndCustomAndTool, level: "readable"},
		stRule{token: FgDim, on: bgAndSelectedAndCustomAndTool, level: "subtle"},
		stRule{token: FgThinkingText, on: bgOnly, level: "readable"},
		stRule{token: FgCustomMessageText, on: append([]string{BgCustomMessage}, stToolPanels...), level: "readable"},
		stRule{token: FgCustomMessageLabel, on: append([]string{"background", BgCustomMessage, BgSelected}, stToolPanels...), level: "readable"},
		stRule{token: FgToolOutput, on: bgAndTool, level: "readable"},
	)
	stRules = append(stRules, stEachRule(
		[]string{FgMdHeading, FgMdLink, FgMdLinkUrl, FgMdCode, FgMdQuote, FgMdCodeBlockBorder, FgMdListBullet},
		bgAndMsg,
		"readable",
	)...)
	stRules = append(stRules, stRule{token: FgMdCodeBlock, on: bgAndMsgAndTool, level: "readable"})
	stRules = append(stRules, stEachRule([]string{FgToolDiffAdded, FgToolDiffRemoved, FgToolDiffContext}, bgAndTool, "readable")...)
	stRules = append(stRules, stEachRule([]string{
		FgSyntaxComment,
		FgSyntaxKeyword,
		FgSyntaxFunction,
		FgSyntaxVariable,
		FgSyntaxString,
		FgSyntaxNumber,
		FgSyntaxType,
		FgSyntaxOperator,
		FgSyntaxPunctuation,
	}, bgAndMsgAndTool, "readable")...)
	stRules = append(stRules, stRule{token: FgSearchMatchText, on: []string{BgSearchMatch}, level: "readable"})
	stRules = append(stRules, stEachRule([]string{FgBashMode, FgBorder, FgBorderAccent}, bgOnly, "readable")...)
	stRules = append(stRules, stRule{token: FgBorderMuted, on: bgOnly, level: "subtle"})
	stRules = append(stRules, stEachRule([]string{FgMdQuoteBorder, FgMdHr}, bgAndMsgAndTool, "readable")...)
	stRules = append(stRules,
		stRule{token: FgScrollbarTrack, on: bgOnly, level: "track"},
		stRule{token: FgScrollbarThumb, on: []string{FgScrollbarTrack}, level: "thumb"},
	)
	for i, tok := range stThinkingTokens {
		stRules = append(stRules, stRule{token: tok, on: bgOnly, level: stThinkingLevels[i]})
	}

	var visit func(tok string)
	visit = func(tok string) {
		if slices.Contains(stSolveOrder, tok) {
			return
		}
		for _, rule := range stRules {
			if rule.token != tok {
				continue
			}
			for _, surface := range rule.on {
				if surface != "background" {
					visit(surface)
				}
			}
		}
		stSolveOrder = append(stSolveOrder, tok)
	}
	for _, rule := range stRules {
		visit(rule.token)
	}
}

func stRelativeLuminance(c tui.RgbColor) float64 {
	linear := func(channel float64) float64 {
		value := channel / 255.0
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// RelativeLuminance returns WCAG 2 relative luminance in [0, 1] for an sRGB color.
func RelativeLuminance(c tui.RgbColor) float64 {
	return stRelativeLuminance(c)
}

// WcagContrast computes the WCAG 2.x contrast ratio (>= 1.0, <= 21.0) between two sRGB colors.
func WcagContrast(a, b tui.RgbColor) float64 {
	la := stRelativeLuminance(a)
	lb := stRelativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func stRgbColorValue(c tui.RgbColor) *tui.RgbColorValue {
	r, err := tui.NewRgbColor(stClamp(math.Round(c.R), 0, 255), stClamp(math.Round(c.G), 0, 255), stClamp(math.Round(c.B), 0, 255))
	if err != nil {
		r, _ = tui.NewRgbColor(0, 0, 0)
	}
	return r
}

func stOklch(c tui.RgbColor) tui.OklchChannels {
	return tui.ColorToOklch(stRgbColorValue(c))
}

func stOkhslOf(c tui.RgbColor) tui.OkhslChannels {
	return tui.ColorToOkhsl(stRgbColorValue(c))
}

func stOklabLightness(c tui.RgbColor) float64 {
	return stOklch(c).L
}

// TerminalAppearance determines whether a terminal is dark or light from its reported
// background (and optional foreground) colors, matching system-theme.ts terminalAppearance().
func TerminalAppearance(background tui.RgbColor, foreground *tui.RgbColor) tui.TerminalColorScheme {
	whiteContrast := WcagContrast(stWhite, background)
	blackContrast := WcagContrast(stBlack, background)
	if foreground != nil {
		foregroundL := stOklabLightness(*foreground)
		backgroundL := stOklabLightness(background)
		if math.Abs(foregroundL-backgroundL) > 0.05 {
			appearance := tui.TerminalColorSchemeLight
			best := blackContrast
			if foregroundL > backgroundL {
				appearance = tui.TerminalColorSchemeDark
				best = whiteContrast
			}
			if best >= stTextMinimumWcagContrast {
				return appearance
			}
		}
	}
	if whiteContrast >= blackContrast {
		return tui.TerminalColorSchemeDark
	}
	return tui.TerminalColorSchemeLight
}

func stClamp(value, minVal, maxVal float64) float64 {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

func stHexOf(c tui.RgbColor) string {
	r := int(stClamp(math.Round(c.R), 0, 255))
	g := int(stClamp(math.Round(c.G), 0, 255))
	b := int(stClamp(math.Round(c.B), 0, 255))
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func stBellWeight(lightness float64) float64 {
	g0 := math.Exp(-(0.5 * 0.5) / (2 * 0.25 * 0.25))
	dx := lightness - 0.5
	gx := math.Exp(-(dx * dx) / (2 * 0.25 * 0.25))
	return (gx - g0) / (1 - g0)
}

func stSaturationCurve(family stFamily, lightness float64) float64 {
	floor := 1.0
	if family.saturation.max > 0 {
		floor = family.saturation.min / family.saturation.max
	}
	return floor + (1-floor)*stBellWeight(lightness)
}

func stLevelTarget(level string, appearance tui.TerminalColorScheme, surfaceL float64) (float64, bool) {
	curves := stLevels[level]
	curve := curves.light
	if appearance == tui.TerminalColorSchemeDark {
		curve = curves.dark
	}
	if surfaceL < curve.reachable[0] || surfaceL > curve.reachable[1] {
		return 0, false
	}
	sum := 0.0
	p := 1.0
	for _, coef := range curve.coefficients {
		sum += coef * p
		p *= surfaceL
	}
	return sum, true
}

func stOkhslRgb(h, s, l float64) tui.RgbColor {
	if math.IsNaN(h) || math.IsInf(h, 0) {
		h = 0
	}
	l = stClamp(l, 0, 1)
	s = stClamp(s, 0, 1)
	if l <= 0 {
		return stBlack
	}
	if l >= 1 {
		return stWhite
	}
	return tui.OkhslToRgb(h, s, l)
}

func stSourceOf(c tui.RgbColor) stSourceColor {
	return stSourceColor{
		OkhslChannels: stOkhslOf(c),
		chroma:        stOklch(c).C,
	}
}

func stAnchored(source stSourceColor, family stFamily, lightness, saturation float64) tui.RgbColor {
	anchor := stSaturationCurve(family, source.L)
	falloff := 1.0
	if anchor > 0 {
		falloff = math.Min(1, stSaturationCurve(family, lightness)/anchor)
	}
	col := stOkhslRgb(source.H, source.S*falloff*saturation, lightness)
	capVal := source.chroma * falloff * saturation
	ok := stOklch(col)
	if ok.C <= capVal {
		return col
	}
	if capVal < 0 {
		capVal = 0
	}
	oklchVal, err := tui.OklchColor(stClamp(ok.L, 0, 1), capVal, source.H)
	if err != nil {
		return col
	}
	return tui.ColorToRgb(oklchVal)
}

func stWithTextContrast(color tui.RgbColor, surfaces []tui.RgbColor, lighter bool) tui.RgbColor {
	meets := func(candidate tui.RgbColor) bool {
		for _, surface := range surfaces {
			if WcagContrast(candidate, surface) < stTextMinimumWcagContrast {
				return false
			}
		}
		return true
	}
	if meets(color) {
		return color
	}
	ch := stOkhslOf(color)
	at := func(lightness float64) tui.RgbColor {
		return stOkhslRgb(ch.H, ch.S, lightness)
	}
	extreme := 0.0
	if lighter {
		extreme = 1.0
	}
	if !meets(at(extreme)) {
		return at(extreme)
	}
	low, high := ch.L, extreme
	for i := 0; i < 20; i++ {
		mid := (low + high) / 2
		if meets(at(mid)) {
			high = mid
		} else {
			low = mid
		}
	}
	return at(high)
}

func stIndexedColors(saturation float64, appearance *tui.TerminalColorScheme) SystemThemeResult {
	colors := make(map[string]any, len(stTokenFamilies))
	var dim []string
	for token, familyName := range stTokenFamilies {
		if slices.Contains(stPanels, token) {
			colors[token] = ""
			continue
		}
		neutral := familyName == "neutral"
		if !neutral && saturation > 0 {
			if slot, ok := stTokenSlots[token]; ok {
				colors[token] = slot
			} else {
				colors[token] = stFamilies[familyName].slot
			}
		} else {
			colors[token] = ""
		}
		if neutral && !slices.Contains(stForegroundTokens, token) {
			dim = append(dim, token)
		}
	}
	var app tui.TerminalColorScheme
	if appearance != nil {
		app = *appearance
	}
	return SystemThemeResult{
		Appearance: app,
		Colors:     colors,
		Dim:        dim,
	}
}

// GenerateSystemThemeColors synthesizes a WCAG-readable adaptive theme palette from
// reported terminal background, foreground, and ANSI palette colors.
func GenerateSystemThemeColors(input SystemThemeInput) SystemThemeResult {
	satInput := 1.0
	if input.Saturation != nil {
		satInput = *input.Saturation
	}
	saturation := stClamp(satInput, 0, 1)
	if input.Background == nil {
		return stIndexedColors(saturation, input.AppearanceHint)
	}

	background := *input.Background
	var palette []stSourceColor
	if len(input.Palette) >= 16 {
		palette = make([]stSourceColor, 16)
		for i := 0; i < 16; i++ {
			palette[i] = stSourceOf(input.Palette[i])
		}
	}

	appearance := TerminalAppearance(background, input.Foreground)
	lighter := appearance == tui.TerminalColorSchemeDark
	extreme := 0.0
	if lighter {
		extreme = 1.0
	}
	backgroundL := stOklabLightness(background)

	paint := func(token string, oklabL float64) tui.RgbColor {
		lightness := tui.OklabToOkhslLightness(oklabL)
		family := stFamilies[stTokenFamilies[token]]
		if palette == nil {
			s := (family.saturation.min + (family.saturation.max-family.saturation.min)*stBellWeight(lightness)) * saturation
			return stOkhslRgb(family.hue, s, lightness)
		}
		slot := family.slot
		if override, ok := stTokenSlots[token]; ok {
			slot = override
		}
		return stAnchored(palette[slot], family, lightness, saturation)
	}

	readableFloor := "subtle"
	if appearance == tui.TerminalColorSchemeDark {
		readableFloor = "readable"
	}

	target := func(level string, surfaceL, t float64) (float64, bool) {
		reached, ok := stLevelTarget(level, appearance, surfaceL)
		if !ok && t == 0 {
			return 0, false
		}
		if !ok {
			reached = extreme
		}
		distance := reached - surfaceL
		floorTarget, floorOK := stLevelTarget(readableFloor, appearance, surfaceL)
		if !floorOK {
			floorTarget = extreme
		}
		floor := floorTarget - surfaceL
		compressed := distance
		if math.Abs(distance) > math.Abs(floor) {
			compressed = distance - (distance-floor)*math.Min(t, 1)
		}
		return surfaceL + compressed*(1-math.Max(0, t-1)), true
	}

	extremeText := stBlack
	if lighter {
		extremeText = stWhite
	}
	readable := func(color tui.RgbColor) bool {
		return WcagContrast(extremeText, color) >= stTextMinimumWcagContrast
	}
	limitPanel := func(token string, l float64) tui.RgbColor {
		color := paint(token, l)
		if readable(color) {
			return color
		}
		low, high := backgroundL, l
		for i := 0; i < 20; i++ {
			mid := (low + high) / 2
			if readable(paint(token, mid)) {
				low = mid
			} else {
				high = mid
			}
		}
		return paint(token, low)
	}

	solve := func(t float64) (map[string]tui.RgbColor, bool) {
		solved := map[string]tui.RgbColor{"background": background}
		for _, token := range stSolveOrder {
			var bestL float64
			hasTarget := false
			for _, rule := range stRules {
				if rule.token != token {
					continue
				}
				for _, surface := range rule.on {
					surfaceCol, ok := solved[surface]
					if !ok {
						surfaceCol = background
					}
					val, ok := target(rule.level, stOklabLightness(surfaceCol), t)
					if !ok || val < 0 || val > 1 {
						return nil, false
					}
					if !hasTarget {
						bestL = val
						hasTarget = true
					} else if lighter {
						if val > bestL {
							bestL = val
						}
					} else if val < bestL {
						bestL = val
					}
				}
			}
			if !hasTarget {
				continue
			}
			if slices.Contains(stPanels, token) {
				solved[token] = limitPanel(token, bestL)
			} else {
				solved[token] = paint(token, bestL)
			}
		}
		return solved, true
	}

	relaxation := 0.0
	solved, ok := solve(0)
	if !ok {
		low, high := 0.0, 2.0
		solved, _ = solve(high)
		for i := 0; i < 20; i++ {
			mid := (low + high) / 2
			if attempt, attemptOK := solve(mid); attemptOK {
				high = mid
				solved = attempt
			} else {
				low = mid
			}
		}
		relaxation = high
	}
	if solved == nil {
		solved = map[string]tui.RgbColor{}
	}

	surfacesOf := func(token string) []tui.RgbColor {
		var out []tui.RgbColor
		for _, rule := range stRules {
			if rule.token != token {
				continue
			}
			for _, surface := range rule.on {
				if col, exists := solved[surface]; exists {
					out = append(out, col)
				} else {
					out = append(out, background)
				}
			}
		}
		return out
	}

	result := make(map[string]any, len(stTokenFamilies))
	for token := range stTokenFamilies {
		if col, exists := solved[token]; exists {
			result[token] = stHexOf(col)
		} else {
			result[token] = ""
		}
	}

	for _, token := range stForegroundTokens {
		surfaces := surfacesOf(token)
		textCol, hasText := solved[token]
		if input.Foreground != nil {
			fg := *input.Foreground
			allValid := true
			var needed float64
			for i, surf := range surfaces {
				val, valOK := target(stForegroundLevel, stOklabLightness(surf), relaxation)
				if !valOK || val < 0 || val > 1 {
					allValid = false
					break
				}
				if i == 0 {
					needed = val
				} else if lighter {
					if val > needed {
						needed = val
					}
				} else if val < needed {
					needed = val
				}
			}
			if allValid {
				fgL := stOklabLightness(fg)
				if (lighter && fgL >= needed) || (!lighter && fgL <= needed) {
					result[token] = ""
					continue
				}
				textCol = stAnchored(stSourceOf(fg), stFamilies["neutral"], tui.OklabToOkhslLightness(needed), saturation)
				hasText = true
			}
		}
		if hasText {
			result[token] = stHexOf(stWithTextContrast(textCol, surfaces, lighter))
		}
	}

	return SystemThemeResult{
		Appearance: appearance,
		Colors:     result,
	}
}
