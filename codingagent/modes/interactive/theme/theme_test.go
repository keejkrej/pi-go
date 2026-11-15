package theme

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func testRgb(t *testing.T, hex string) tui.RgbColor {
	t.Helper()
	c, err := tui.ParseColor(hex)
	if err != nil {
		t.Fatal(err)
	}
	return tui.ColorToRgb(c)
}

func testLightness(c tui.RgbColor) float64 {
	v, _ := tui.NewRgbColor(c.R, c.G, c.B)
	return tui.ColorToOklch(v).L
}

func testChroma(c tui.RgbColor) float64 {
	v, _ := tui.NewRgbColor(c.R, c.G, c.B)
	return tui.ColorToOklch(v).C
}

func testHue(c tui.RgbColor) float64 {
	v, _ := tui.NewRgbColor(c.R, c.G, c.B)
	return tui.ColorToOklch(v).H
}

func testNormalizeJsonMaps(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		m := make(map[string]any, x.Len())
		for k, val := range x.All() {
			m[k] = testNormalizeJsonMaps(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = testNormalizeJsonMaps(el)
		}
		return out
	default:
		return v
	}
}

func testLoadDarkJsonMap(t *testing.T) map[string]any {
	t.Helper()
	b, err := fs.ReadFile(utils.ConfigAssetsFS(), "theme/dark.json")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jsonx.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return testNormalizeJsonMaps(parsed).(map[string]any)
}

func testWriteTempThemeJson(t *testing.T, dir string, fileName string, obj map[string]any) string {
	t.Helper()
	b, err := jsonx.MarshalIndent(obj, "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, fileName)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func testDraculaInput(t *testing.T) SystemThemeInput {
	bg := testRgb(t, "#282a36")
	fg := testRgb(t, "#f8f8f2")
	paletteHex := []string{
		"#21222c", "#ff5555", "#50fa7b", "#f1fa8c", "#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
		"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5", "#d6acff", "#ff92df", "#a4ffff", "#ffffff",
	}
	palette := make([]tui.RgbColor, len(paletteHex))
	for i, h := range paletteHex {
		palette[i] = testRgb(t, h)
	}
	return SystemThemeInput{Background: &bg, Foreground: &fg, Palette: palette}
}

func testResolvedSysColor(t *testing.T, input SystemThemeInput, token string) tui.RgbColor {
	t.Helper()
	res := GenerateSystemThemeColors(input)
	val := res.Colors[token]
	if s, ok := val.(string); ok && s == "" {
		if IsBgToken(token) {
			return *input.Background
		}
		return *input.Foreground
	}
	return testRgb(t, val.(string))
}

func TestSystemThemeGenerationAndBehavior(t *testing.T) {
	t.Cleanup(func() { SetTerminalColors(tui.TerminalColors{}) })

	dracula := testDraculaInput(t)
	solBg := testRgb(t, "#fdf6e3")
	solFg := testRgb(t, "#657b83")
	bgOnly := testRgb(t, "#1e1e1e")
	midBg := testRgb(t, "#808080")
	midFg := testRgb(t, "#ffffff")

	terminals := map[string]SystemThemeInput{
		"dracula":        dracula,
		"solarizedLight": {Background: &solBg, Foreground: &solFg},
		"backgroundOnly": {Background: &bgOnly},
		"midGray":        {Background: &midBg, Foreground: &midFg},
	}

	panels := []string{BgUserMessage, BgToolPending, BgToolSuccess, BgToolError, BgSelected}

	for name, inp := range terminals {
		txt := testResolvedSysColor(t, inp, FgText)
		for _, surf := range []tui.RgbColor{*inp.Background, testResolvedSysColor(t, inp, BgSelected)} {
			if c := WcagContrast(txt, surf); c < 4.5 {
				t.Fatalf("%s text contrast=%.2f < 4.5", name, c)
			}
		}
		if c := WcagContrast(testResolvedSysColor(t, inp, FgToolTitle), testResolvedSysColor(t, inp, BgToolError)); c < 4.5 {
			t.Fatalf("%s toolTitle on toolErrorBg contrast=%.2f < 4.5", name, c)
		}

		bgL := testLightness(*inp.Background)
		off := func(tok string) float64 { return testLightness(testResolvedSysColor(t, inp, tok)) - bgL }
		if name != "midGray" {
			if math.Abs(off(FgText)) <= math.Abs(off(FgMuted)) {
				t.Fatalf("%s expected |offset(text)| > |offset(muted)|", name)
			}
			if math.Abs(off(FgMuted)) <= math.Abs(off(FgDim)) {
				t.Fatalf("%s expected |offset(muted)| > |offset(dim)|", name)
			}
		}
		isDark := GenerateSystemThemeColors(inp).Appearance == tui.TerminalColorSchemeDark
		for _, p := range panels {
			panelRgb := testResolvedSysColor(t, inp, p)
			if c := WcagContrast(panelRgb, *inp.Background); c >= 2 {
				t.Fatalf("%s %s contrast=%.2f >= 2", name, p, c)
			}
			if (off(p) > 0) != isDark {
				t.Fatalf("%s %s polarity mismatch: offset=%.3f isDark=%v", name, p, off(p), isDark)
			}
		}
	}

	if got := GenerateSystemThemeColors(dracula).Colors[FgText]; got != "" {
		t.Fatalf("expected Dracula text to use terminal default, got %v", got)
	}
	if got := GenerateSystemThemeColors(terminals["solarizedLight"]).Colors[FgText]; got == "" {
		t.Fatal("expected Solarized light text to be darkened away from unreadable foreground")
	}
	errHue := testHue(testResolvedSysColor(t, dracula, FgError))
	if math.Abs(errHue-testHue(dracula.Palette[1])) >= 8 {
		t.Fatalf("Dracula error hue drifted: %.1f vs %.1f", errHue, testHue(dracula.Palette[1]))
	}

	// #10255: pastel Catppuccin Frappe palette preserves lightness & avoids chroma inflation
	frBg := testRgb(t, "#303446")
	frFg := testRgb(t, "#c6d0f5")
	frPalHex := []string{
		"#51576d", "#e78284", "#a6d189", "#e5c890", "#8caaee", "#f4b8e4", "#81c8be", "#b5bfe2",
		"#626880", "#e67172", "#8ec772", "#d9ba73", "#7b9ef0", "#f2a4db", "#5abfb5", "#a5adce",
	}
	frPal := make([]tui.RgbColor, len(frPalHex))
	for i, h := range frPalHex {
		frPal[i] = testRgb(t, h)
	}
	frappe := SystemThemeInput{Background: &frBg, Foreground: &frFg, Palette: frPal}
	pink := frappe.Palette[5]
	accent := testResolvedSysColor(t, frappe, FgAccent)
	if testLightness(accent) >= testLightness(pink)-0.05 {
		t.Fatalf("expected Frappe accent L (%.3f) < pink L (%.3f) - 0.05", testLightness(accent), testLightness(pink))
	}
	if testChroma(accent) > testChroma(pink)*1.03 {
		t.Fatalf("Frappe accent chroma inflated: %.3f > %.3f", testChroma(accent), testChroma(pink)*1.03)
	}
	for _, p := range []string{BgUserMessage, BgCustomMessage} {
		if ch := testChroma(testResolvedSysColor(t, frappe, p)); ch > 0.1 {
			t.Fatalf("Frappe panel %s chroma=%.3f > 0.1", p, ch)
		}
	}

	// Saturation=0 yields grayscale
	zero := 0.0
	grayDrac := dracula
	grayDrac.Saturation = &zero
	if col, _ := tui.ParseColor(GenerateSystemThemeColors(grayDrac).Colors[FgError]); tui.ColorToOklch(col).C >= 0.005 {
		t.Fatal("expected zero saturation to produce grayscale error color")
	}

	// No background -> palette indices & faint muted
	lightHint := tui.TerminalColorSchemeLight
	noBg := GenerateSystemThemeColors(SystemThemeInput{AppearanceHint: &lightHint})
	if noBg.Appearance != tui.TerminalColorSchemeLight ||
		noBg.Colors[FgError] != 1 || noBg.Colors[FgText] != "" || noBg.Colors[BgUserMessage] != "" ||
		!slices.Contains(noBg.Dim, FgMuted) {
		t.Fatalf("unexpected no-background fallback: %+v", noBg)
	}
	if got := GenerateSystemThemeColors(SystemThemeInput{Saturation: &zero}).Colors[FgError]; got != "" {
		t.Fatalf("expected default empty error when saturation=0 & no background, got %v", got)
	}

	// System theme instance behavior
	avail := GetAvailableThemes()
	if len(avail) == 0 || avail[0] != "system" {
		t.Fatalf("expected system first in GetAvailableThemes: %v", avail)
	}
	if exp := GetThemeExportColors("system"); exp != (ThemeExportColors{}) {
		t.Fatalf("expected empty export colors for system: %+v", exp)
	}

	SetTerminalColors(tui.TerminalColors{Background: dracula.Background, Foreground: dracula.Foreground, Palette: dracula.Palette})
	sysT, err := GetThemeByName("system")
	if err != nil || sysT.Appearance != tui.TerminalColorSchemeDark || sysT.GetFgAnsi(FgText) != "\x1b[39m" {
		t.Fatalf("unexpected system theme with Dracula: t=%+v err=%v", sysT, err)
	}
	if !strings.HasPrefix(sysT.GetFgAnsi(FgError), "\x1b[38;") {
		t.Fatalf("expected truecolor/256color fg ansi for error, got %q", sysT.GetFgAnsi(FgError))
	}

	SetTerminalColors(tui.TerminalColors{})
	sysDefault, err := GetThemeByName("system")
	if err != nil {
		t.Fatal(err)
	}
	if got := sysDefault.Fg(FgMuted, "x"); got != "\x1b[39m\x1b[2mx\x1b[22;39m" {
		t.Fatalf("sysDefault.Fg(muted) = %q", got)
	}
	if got := sysDefault.Style("x", ThemeStyleOptions{Fg: FgMuted}); got != "\x1b[39m\x1b[2mx\x1b[22m\x1b[39m" {
		t.Fatalf("sysDefault.Style(muted) = %q", got)
	}
}

func TestThemeDetectionAndColorModes(t *testing.T) {
	t.Cleanup(func() {
		tui.ResetCapabilitiesCache()
		SetTerminalColors(tui.TerminalColors{})
		_, _ = InitTheme("dark")
	})

	if got := DetectColorFgBgTheme(map[string]string{"COLORFGBG": "15;0"}); got == nil || *got != tui.TerminalColorSchemeDark {
		t.Fatalf("COLORFGBG 15;0 = %v", got)
	}
	if got := DetectColorFgBgTheme(map[string]string{"COLORFGBG": "0;7;15"}); got == nil || *got != tui.TerminalColorSchemeLight {
		t.Fatalf("COLORFGBG 0;7;15 = %v", got)
	}
	if got := DetectColorFgBgTheme(map[string]string{"COLORFGBG": "12;8"}); got == nil || *got != tui.TerminalColorSchemeDark {
		t.Fatalf("COLORFGBG 12;8 = %v", got)
	}
	if got := DetectColorFgBgTheme(map[string]string{"COLORFGBG": "15;default"}); got != nil {
		t.Fatalf("COLORFGBG default = %v", *got)
	}
	if got := DetectColorFgBgTheme(map[string]string{}); got != nil {
		t.Fatalf("COLORFGBG empty = %v", *got)
	}

	env := map[string]string{"COLORFGBG": "0;15"}
	darkScheme := tui.TerminalColorSchemeDark
	lightScheme := tui.TerminalColorSchemeLight
	darkBg := tui.RgbColor{R: 8, G: 8, B: 8}
	if got := DetectTerminalTheme(tui.TerminalColors{Background: &darkBg}, &lightScheme, env); got != tui.TerminalColorSchemeDark {
		t.Fatalf("expected dark background precedence, got %s", got)
	}
	if got := DetectTerminalTheme(tui.TerminalColors{}, &darkScheme, env); got != tui.TerminalColorSchemeDark {
		t.Fatalf("expected scheme precedence, got %s", got)
	}
	if got := DetectTerminalTheme(tui.TerminalColors{}, nil, env); got != tui.TerminalColorSchemeLight {
		t.Fatalf("expected COLORFGBG fallback, got %s", got)
	}
	if got := DetectTerminalTheme(tui.TerminalColors{}, nil, map[string]string{}); got != tui.TerminalColorSchemeDark {
		t.Fatalf("expected dark default fallback, got %s", got)
	}

	gray118 := tui.RgbColor{R: 118, G: 118, B: 118}
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	mid128 := tui.RgbColor{R: 128, G: 128, B: 128}
	if got := DetectTerminalTheme(tui.TerminalColors{Background: &gray118}, nil); got != tui.TerminalColorSchemeLight {
		t.Fatalf("expected 118 bg light, got %s", got)
	}
	if got := DetectTerminalTheme(tui.TerminalColors{Background: &gray118, Foreground: &white}, nil); got != tui.TerminalColorSchemeDark {
		t.Fatalf("expected 118 bg + white fg dark, got %s", got)
	}
	if got := DetectTerminalTheme(tui.TerminalColors{Background: &mid128, Foreground: &white}, nil); got != tui.TerminalColorSchemeLight {
		t.Fatalf("expected 128 bg + white fg light, got %s", got)
	}

	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: false})
	ansi256, err := GetThemeByName("dark")
	if err != nil || ansi256 == nil {
		t.Fatalf("GetThemeByName(dark) failed in 256color mode: err=%v theme=%v", err, ansi256)
	}
	if ansi256.GetColorMode() != tui.TerminalColorMode256color ||
		!regexp.MustCompile(`^\x1b\[38;5;\d+m$`).MatchString(ansi256.GetFgAnsi(FgAccent)) {
		t.Fatalf("256color check failed: mode=%v ansi=%q", ansi256.GetColorMode(), ansi256.GetFgAnsi(FgAccent))
	}

	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tc, err := GetThemeByName("dark")
	if err != nil || tc == nil {
		t.Fatalf("GetThemeByName(dark) failed in truecolor mode: err=%v theme=%v", err, tc)
	}
	if tc.GetColorMode() != tui.TerminalColorModeTruecolor ||
		!regexp.MustCompile(`^\x1b\[38;2;\d+;\d+;\d+m$`).MatchString(tc.GetFgAnsi(FgAccent)) {
		t.Fatalf("truecolor check failed: mode=%v ansi=%q", tc.GetColorMode(), tc.GetFgAnsi(FgAccent))
	}

	auto := ParseAutoThemeSetting("light/dark")
	if auto == nil || auto.LightTheme != "light" || auto.DarkTheme != "dark" {
		t.Fatalf("ParseAutoThemeSetting = %+v", auto)
	}
	if ResolveThemeSetting("dark", lightScheme) != "dark" ||
		ResolveThemeSetting("light/dark", lightScheme) != "light" ||
		ResolveThemeSetting("light/dark", darkScheme) != "dark" ||
		ResolveThemeSetting("light/dark/extra", darkScheme) != "" {
		t.Fatal("ResolveThemeSetting failed")
	}
}

func TestThemePickerExportStyleSchemaAndHighlight(t *testing.T) {
	t.Cleanup(func() {
		tui.ResetCapabilitiesCache()
		SetTerminalColors(tui.TerminalColors{})
		SetRegisteredThemes(nil)
		_, _ = InitTheme("dark")
	})

	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	themesDir := filepath.Join(agentDir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(utils.EnvAgentDir, agentDir)

	// Picker reads JSON name instead of filename
	customPicker := testLoadDarkJsonMap(t)
	customPicker["name"] = "bar"
	pickerPath := testWriteTempThemeJson(t, themesDir, "foo.json", customPicker)
	if !slices.Contains(GetAvailableThemes(), "bar") || slices.Contains(GetAvailableThemes(), "foo") {
		t.Fatalf("GetAvailableThemes picker mismatch: %v", GetAvailableThemes())
	}
	if !slices.Contains(GetAvailableThemesWithPaths(), ThemeWithPath{Name: "bar", Path: pickerPath}) {
		t.Fatalf("GetAvailableThemesWithPaths missing bar: %v", GetAvailableThemesWithPaths())
	}

	// Export colors: vars, recursive vars, okhsl -> hex, 256-color -> hex, oklch preserved
	expTheme := testLoadDarkJsonMap(t)
	expTheme["name"] = "custom-export-test"
	expVars := expTheme["vars"].(map[string]any)
	expVars["deepPageBg"] = "#abcdef"
	expVars["pageBgAlias"] = "deepPageBg"
	expVars["cardBgAnsi"] = 24
	expVars["okhslVar"] = "okhsl(250 20% 20%)"
	expTheme["export"] = map[string]any{
		"pageBg": "pageBgAlias",
		"cardBg": "cardBgAnsi",
		"infoBg": "oklch(30% 0.05 80)",
	}
	testWriteTempThemeJson(t, themesDir, "custom-export-test.json", expTheme)
	expCols := GetThemeExportColors("custom-export-test")
	if expCols.PageBg != "#abcdef" || expCols.CardBg != "#005f87" || expCols.InfoBg != "oklch(30% 0.05 80)" {
		t.Fatalf("GetThemeExportColors mismatch: %+v", expCols)
	}

	// OKHSL export conversion
	okhslTheme := testLoadDarkJsonMap(t)
	okhslTheme["name"] = "custom-export-okhsl"
	okhslTheme["vars"].(map[string]any)["card"] = "okhsl(250 20% 20%)"
	okhslTheme["export"] = map[string]any{"pageBg": "okhsl(250 20% 15%)", "cardBg": "card", "infoBg": ""}
	testWriteTempThemeJson(t, themesDir, "custom-export-okhsl.json", okhslTheme)
	okhslCols := GetThemeExportColors("custom-export-okhsl")
	c1, _ := tui.OkhslColor(250, 0.2, 0.15)
	c2, _ := tui.OkhslColor(250, 0.2, 0.2)
	if okhslCols.PageBg != tui.ColorToHex(c1) || okhslCols.CardBg != tui.ColorToHex(c2) || okhslCols.InfoBg != "" {
		t.Fatalf("OKHSL export mismatch: %+v", okhslCols)
	}

	// Missing optional scrollbar / search / thinkingMax tokens fall back cleanly
	legacy := testLoadDarkJsonMap(t)
	legacy["name"] = "legacy-fallbacks"
	lColors := legacy["colors"].(map[string]any)
	delete(lColors, FgScrollbarTrack)
	delete(lColors, FgScrollbarThumb)
	delete(lColors, BgSearchMatch)
	delete(lColors, FgSearchMatchText)
	delete(lColors, FgThinkingMax)
	legacyPath := testWriteTempThemeJson(t, tmp, "legacy.json", legacy)
	loadedLegacy, err := LoadThemeFromPath(legacyPath, tui.TerminalColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	if loadedLegacy.GetFgAnsi(FgScrollbarTrack) != loadedLegacy.GetFgAnsi(FgMuted) ||
		loadedLegacy.GetFgAnsi(FgScrollbarThumb) != loadedLegacy.GetFgAnsi(FgText) ||
		loadedLegacy.GetBgAnsi(BgSearchMatch) != loadedLegacy.GetBgAnsi(BgSelected) ||
		loadedLegacy.GetFgAnsi(FgSearchMatchText) != loadedLegacy.GetFgAnsi(FgText) ||
		loadedLegacy.GetFgAnsi(FgThinkingMax) != loadedLegacy.GetFgAnsi(FgThinkingXhigh) {
		t.Fatal("optional theme fallbacks did not resolve cleanly")
	}

	// Theme.Style matches tui.StyleText + panics on invalid token slots
	if got := loadedLegacy.Style("Ready", ThemeStyleOptions{Fg: FgSuccess, Bg: BgToolSuccess, Bold: true}); got != tui.StyleText("Ready", tui.TextStyle{
		TextAttributes: tui.TextAttributes{Bold: new(true)},
		Fg:             loadedLegacy.Colors[FgSuccess],
		Bg:             loadedLegacy.Colors[BgToolSuccess],
	}, tui.TerminalColorModeTruecolor) {
		t.Fatalf("Style mismatch: %q", got)
	}
	assertPanicsWith := func(sub string, fn func()) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(r.(string), sub) {
				t.Fatalf("expected panic containing %q, got %v", sub, r)
			}
		}()
		fn()
	}
	assertPanicsWith("Unknown theme color: notAToken", func() {
		loadedLegacy.Style("x", ThemeStyleOptions{Fg: "notAToken"})
	})
	assertPanicsWith("Unknown theme color: userMessageBg", func() {
		loadedLegacy.Style("x", ThemeStyleOptions{Fg: BgUserMessage})
	})

	// Empty tokens emit default reset SGR & resolve dynamically via SetTerminalColors
	defObj := testLoadDarkJsonMap(t)
	defObj["name"] = "default-tokens"
	defObj["colors"].(map[string]any)[FgText] = ""
	defObj["colors"].(map[string]any)[BgUserMessage] = ""
	defPath := testWriteTempThemeJson(t, tmp, "default-tokens.json", defObj)
	defT, err := LoadThemeFromPath(defPath, tui.TerminalColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	if defT.Fg(FgText, "x") != "\x1b[39mx\x1b[39m" || defT.Bg(BgUserMessage, "x") != "\x1b[49mx\x1b[49m" {
		t.Fatalf("empty token SGR mismatch: fg=%q bg=%q", defT.Fg(FgText, "x"), defT.Bg(BgUserMessage, "x"))
	}
	if tui.ColorToHex(defT.Colors[FgText]) != "#e5e5e7" || tui.ColorToHex(defT.Colors[BgUserMessage]) != "#000000" {
		t.Fatalf("initial default hex mismatch: fg=%s bg=%s", tui.ColorToHex(defT.Colors[FgText]), tui.ColorToHex(defT.Colors[BgUserMessage]))
	}
	SetTerminalColors(tui.TerminalColors{
		Foreground: &tui.RgbColor{R: 200, G: 210, B: 220},
		Background: &tui.RgbColor{R: 10, G: 20, B: 30},
	})
	if tui.ColorToHex(defT.Colors[FgText]) != "#c8d2dc" || tui.ColorToHex(defT.Colors[BgUserMessage]) != "#0a141e" {
		t.Fatalf("live terminal update mismatch: fg=%s bg=%s", tui.ColorToHex(defT.Colors[FgText]), tui.ColorToHex(defT.Colors[BgUserMessage]))
	}

	// Schema validation checks (config-schemas.test.ts)
	badName := testLoadDarkJsonMap(t)
	badName["name"] = "invalid/name"
	if _, err := ValidateThemeJson("invalid", badName); err == nil || !strings.Contains(err.Error(), "theme names cannot contain") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
	badEmptyColors := testLoadDarkJsonMap(t)
	badEmptyColors["colors"] = map[string]any{}
	if _, err := ValidateThemeJson("invalid", badEmptyColors); err == nil || !strings.Contains(err.Error(), "Missing required color tokens") {
		t.Fatalf("expected missing required colors error, got %v", err)
	}
	badExtraTop := testLoadDarkJsonMap(t)
	badExtraTop["unexpected"] = true
	if _, err := ValidateThemeJson("invalid", badExtraTop); err == nil || !strings.Contains(strings.ToLower(err.Error()), "additional properties") {
		t.Fatalf("expected additional properties error, got %v", err)
	}

	// GetLanguageFromPath & HighlightCode (#10143) & CodeHighlighter caching
	if l := GetLanguageFromPath("lib/main.dart"); l == nil || *l != "dart" {
		t.Fatalf("GetLanguageFromPath(.dart) = %v", l)
	}
	if l := GetLanguageFromPath("build.gradle"); l == nil || *l != "groovy" {
		t.Fatalf("GetLanguageFromPath(build.gradle) = %v", l)
	}
	if l := GetLanguageFromPath("src/Main.kt"); l == nil || *l != "kotlin" {
		t.Fatalf("GetLanguageFromPath(.kt) = %v", l)
	}

	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	_, _ = InitTheme("dark")
	docCode := "\"\"\"\nline one\n\nline two\n\"\"\"\nafter"
	pyLang := "python"
	docLines := HighlightCode(docCode, &pyLang)
	strAnsi := CurrentTheme().GetFgAnsi(FgSyntaxString)
	wantDoc := []string{
		strAnsi + "\"\"\"\x1b[39m",
		strAnsi + "line one\x1b[39m",
		"",
		strAnsi + "line two\x1b[39m",
		strAnsi + "\"\"\"\x1b[39m",
		"after",
	}
	if !slices.Equal(docLines, wantDoc) {
		t.Fatalf("HighlightCode python docstrings:\ngot:  %q\nwant: %q", docLines, wantDoc)
	}

	highlighter := NewCodeHighlighter()
	jsLang := "javascript"
	jsSample := `const page = html` + "`" + `<div class="x">ok</div>` + "`;"
	first := highlighter.Highlight(jsSample, &jsLang)
	second := highlighter.Highlight(jsSample, &jsLang)
	if &first[0] != &second[0] {
		t.Fatal("expected CodeHighlighter to return cached slice for identical inputs")
	}
	_, _ = InitTheme("light")
	afterThemeSwitch := highlighter.Highlight(jsSample, &jsLang)
	if slices.Equal(first, afterThemeSwitch) {
		t.Fatal("expected CodeHighlighter to invalidate cache when theme switches")
	}

	utils.LoadAllHighlightLanguages()
	afterLangLoad := highlighter.Highlight(jsSample, &jsLang)
	if &afterThemeSwitch[0] == &afterLangLoad[0] {
		t.Fatal("expected CodeHighlighter to recompute when language count increases")
	}

	_, _ = InitTheme("dark")
	diffLang := "diff"
	diffLines := HighlightCode("-old\n+new\n", &diffLang)
	if len(diffLines) < 2 || diffLines[0] != CurrentTheme().GetFgAnsi(FgToolDiffRemoved)+"-old\x1b[39m" ||
		diffLines[1] != CurrentTheme().GetFgAnsi(FgToolDiffAdded)+"+new\x1b[39m" {
		t.Fatalf("HighlightCode diff mismatch: %q", diffLines)
	}
}
