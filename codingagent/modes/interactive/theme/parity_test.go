package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/ansi"
	"github.com/keejkrej/pi-go/tui"
)

func TestVisualTruncateAndPreviewParity(t *testing.T) {
	_, _ = InitTheme("dark")

	// Empty text returns empty visual lines slice and 0 skipped without padding.
	empty := TruncateToVisualLines("", 3, 20)
	if empty.SkippedCount != 0 || len(empty.VisualLines) != 0 {
		t.Fatalf("empty truncate mismatch: %+v", empty)
	}

	// Keep="start" preserves top lines; Keep="end" (default) preserves tail lines.
	lines := "line1\nline2\nline3\nline4"
	startRes := TruncateToVisualLinesKeep(lines, 2, 40, 0, "start")
	if startRes.SkippedCount != 2 || len(startRes.VisualLines) != 2 ||
		strings.TrimRight(startRes.VisualLines[0], " ") != "line1" ||
		strings.TrimRight(startRes.VisualLines[1], " ") != "line2" {
		t.Fatalf("keep=start mismatch: %+v", startRes)
	}
	endRes := TruncateToVisualLines(lines, 2, 40)
	if endRes.SkippedCount != 2 || len(endRes.VisualLines) != 2 ||
		strings.TrimRight(endRes.VisualLines[0], " ") != "line3" ||
		strings.TrimRight(endRes.VisualLines[1], " ") != "line4" {
		t.Fatalf("keep=end mismatch: %+v", endRes)
	}

	// VisualLinePreview implements tui.Component, caches per-width, truncates hint, invalidates cleanly.
	var comp tui.Component = NewVisualLinePreview(VisualLinePreviewOptions{
		Text:           "alpha\nbeta\ngamma\ndelta",
		MaxVisualLines: 2,
		Keep:           "end",
		FormatHint:     func(hidden int) string { return strings.Repeat("h", hidden) + " more" },
	})
	rendered := comp.Render(12)
	if len(rendered) != 3 || !strings.Contains(rendered[0], "more") ||
		strings.TrimRight(rendered[1], " ") != "gamma" ||
		strings.TrimRight(rendered[2], " ") != "delta" {
		t.Fatalf("VisualLinePreview render mismatch: %q", rendered)
	}
	for _, r := range rendered {
		if w := tui.VisibleWidth(r); w > 12 {
			t.Fatalf("line exceeded width 12 (%d): %q", w, r)
		}
	}
	// Repeat render at same width uses cache; Invalidate resets.
	rendered2 := comp.Render(12)
	if !slices.Equal(rendered, rendered2) {
		t.Fatalf("cached render mismatch: %q vs %q", rendered2, rendered)
	}
	comp.Invalidate()
	renderedStart := NewVisualLinePreview(VisualLinePreviewOptions{
		Text:           "one\ntwo\nthree",
		MaxVisualLines: 2,
		Keep:           "start",
		FormatHint:     func(hidden int) string { return "hidden" },
	}).Render(20)
	if len(renderedStart) != 3 ||
		strings.TrimRight(renderedStart[0], " ") != "one" ||
		strings.TrimRight(renderedStart[1], " ") != "two" ||
		strings.TrimRight(renderedStart[2], " ") != "hidden" {
		t.Fatalf("VisualLinePreview keep=start mismatch: %q", renderedStart)
	}
}

func TestKeybindingHintsParity(t *testing.T) {
	_, _ = InitTheme("dark")
	origPlat := khPlatform
	t.Cleanup(func() { khPlatform = origPlat })

	// Darwin translates alt -> option and preserves "escape" without mapping to "esc".
	khPlatform = func() string { return "darwin" }
	if got := FormatKeyText("alt+enter"); got != "option+enter" {
		t.Fatalf("darwin FormatKeyText(alt+enter) = %q", got)
	}
	if got := FormatKeyText("Alt+Shift+a", KeyTextFormatOptions{Capitalize: true}); got != "Option+Shift+A" {
		t.Fatalf("darwin capitalized FormatKeyText = %q", got)
	}
	if got := FormatKeys([]tui.KeyId{"escape", "ctrl+c"}, KeyTextFormatOptions{Capitalize: true}); got != "Escape/Ctrl+C" {
		t.Fatalf("FormatKeys capitalized = %q", got)
	}

	// Non-darwin keeps alt unchanged.
	khPlatform = func() string { return "linux" }
	if got := FormatKeyText("alt+enter"); got != "alt+enter" {
		t.Fatalf("linux FormatKeyText(alt+enter) = %q", got)
	}
	if got := FormatKeys(nil); got != "" {
		t.Fatalf("FormatKeys(nil) = %q", got)
	}

	// KeyText / KeyDisplayText use active keybindings manager.
	if kt := KeyText(tui.KeybindingTuiEditorCursorWordLeft); kt == "" {
		t.Fatal("expected non-empty KeyText for cursorWordLeft")
	}
	if kdt := KeyDisplayText(tui.KeybindingTuiEditorCursorWordLeft); kdt == "" {
		t.Fatal("expected non-empty KeyDisplayText for cursorWordLeft")
	}

	// RawKeyHint wraps key in FgDim and " "+description in FgMuted separately.
	th := CurrentTheme()
	wantHint := th.Fg(FgDim, "ctrl+c") + th.Fg(FgMuted, " abort")
	if got := RawKeyHint(th, "ctrl+c", "abort"); got != wantHint {
		t.Fatalf("RawKeyHint = %q, want %q", got, wantHint)
	}
	if got := KeyHint("ctrl+c", "abort"); got != wantHint {
		t.Fatalf("KeyHint = %q, want %q", got, wantHint)
	}
}

func TestDiffParity(t *testing.T) {
	_, _ = InitTheme("dark")
	origLevel := ansi.Default().Level()
	ansi.Default().SetLevel(3)
	t.Cleanup(func() { ansi.Default().SetLevel(origLevel) })

	// 1. Multi-line diff block does NOT perform intra-line inverse highlighting.
	multi := "-old1\n-old2\n+new1\n+new2"
	renderedMulti := RenderDiff(multi)
	if strings.Contains(renderedMulti, "\x1b[7m") {
		t.Fatalf("did not expect intra-line inverse highlight on multi-line hunk: %q", renderedMulti)
	}

	// 2. Tabs inside diff content are replaced by 3 spaces and "-   foo" preserves whitespace.
	tabDiff := "- 1 x\tfoo\n+ 1 x\tbar"
	renderedTabs := RenderDiff(tabDiff)
	if strings.Contains(renderedTabs, "\t") {
		t.Fatalf("expected tabs replaced in RenderDiff output: %q", renderedTabs)
	}
	if !strings.Contains(renderedTabs, "x   ") {
		t.Fatalf("expected 3-space tab expansion in %q", renderedTabs)
	}

	// 3. Space-prefixed hunk with leading spaces preserves prefix + indent.
	dashSpaces := "-   foo"
	if got := RenderDiff(dashSpaces); !strings.Contains(got, "-   foo") {
		t.Fatalf("expected '-   foo' preserved, got %q", got)
	}

	// 4. Single changed line pairs common prefix outside inverse and wraps changed part in inverse.
	single := "- 1   const a = 1\n+ 1   const b = 1"
	renderedSingle := RenderDiff(single)
	if !strings.Contains(renderedSingle, CurrentTheme().Inverse("a")) || !strings.Contains(renderedSingle, CurrentTheme().Inverse("b")) {
		t.Fatalf("expected intra-line inverse highlight on changed tokens, got %q", renderedSingle)
	}
}

func TestThemeSchemaAndBomParity(t *testing.T) {
	// Missing required token under colors reports friendly missing tokens section cleanly.
	darkMap := testLoadDarkJsonMap(t)
	colors := darkMap["colors"].(map[string]any)
	delete(colors, FgAccent)
	if _, err := ValidateThemeJson("missing-accent", darkMap); err == nil ||
		!strings.Contains(err.Error(), "Missing required color tokens:") ||
		!strings.Contains(err.Error(), "- "+FgAccent) {
		t.Fatalf("expected missing required color token %s error, got %v", FgAccent, err)
	}

	// Extra top-level property reports additional properties error cleanly.
	extraMap := testLoadDarkJsonMap(t)
	extraMap["unexpectedField"] = 123
	if _, err := ValidateThemeJson("extra-prop", extraMap); err == nil || !strings.Contains(err.Error(), "must not have additional properties") {
		t.Fatalf("expected additional properties error, got %v", err)
	}

	// Invalid color value is rejected by TypeBox ColorValueSchema (non-string/non-0..255 int),
	// while invalid hex string passes schema (strings may be var names) and fails color resolution.
	badTypeMap := testLoadDarkJsonMap(t)
	badTypeMap["colors"].(map[string]any)[FgAccent] = true
	if _, err := ValidateThemeJson("bad-type", badTypeMap); err == nil {
		t.Fatal("expected invalid boolean color type error")
	}
	badIndexMap := testLoadDarkJsonMap(t)
	badIndexMap["colors"].(map[string]any)[FgAccent] = 256
	if _, err := ValidateThemeJson("bad-index", badIndexMap); err == nil {
		t.Fatal("expected out-of-range ANSI index error")
	}
	badHexMap := testLoadDarkJsonMap(t)
	badHexMap["colors"].(map[string]any)[FgAccent] = "#12345"
	badHexPath := testWriteTempThemeJson(t, t.TempDir(), "bad-hex.json", badHexMap)
	if _, err := LoadThemeFromPath(badHexPath); err == nil {
		t.Fatal("expected invalid hex string to fail LoadThemeFromPath color resolution")
	}

	// ParseAndValidateThemeJson strips UTF-8 BOM.
	bomJson := "\uFEFF" + `{"name":"bom-bad","colors":{}}`
	if _, err := ParseAndValidateThemeJson("bom-bad", []byte(bomJson)); err == nil || !strings.Contains(err.Error(), "Missing required color tokens:") {
		t.Fatalf("expected schema error after BOM strip, got %v", err)
	}
}

func TestThemeDetectionExportStyleAndDiscoveryParity(t *testing.T) {
	origLevel := ansi.Default().Level()
	t.Cleanup(func() {
		ansi.Default().SetLevel(origLevel)
		tui.ResetCapabilitiesCache()
		SetTerminalColorScheme(nil)
		SetTerminalColors(tui.TerminalColors{})
		SetRegisteredThemes(nil)
		_, _ = InitTheme("dark")
	})

	// 1. SetTerminalColorScheme + GetTerminalTheme
	lightScheme := tui.TerminalColorSchemeLight
	SetTerminalColors(tui.TerminalColors{})
	SetTerminalColorScheme(&lightScheme)
	if got := GetTerminalTheme(); got != tui.TerminalColorSchemeLight {
		t.Fatalf("GetTerminalTheme = %q, want light", got)
	}

	// 2. MarkTerminalColorsPending -> grayscale saturation -> cleared by SetTerminalColors.
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	_, err := InitTheme("system")
	if err != nil {
		t.Fatalf("InitTheme(system) failed: %v", err)
	}
	SetTerminalColors(tui.TerminalColors{
		Background: &tui.RgbColor{R: 40, G: 42, B: 54},
		Foreground: &tui.RgbColor{R: 248, G: 248, B: 242},
	})
	normalErrHex := CurrentTheme().Colors[FgError]
	if tui.ColorToOklch(normalErrHex).C <= 0.01 {
		t.Fatalf("expected non-gray error color when not pending: %+v", normalErrHex)
	}
	MarkTerminalColorsPending()
	pendingErrHex := CurrentTheme().Colors[FgError]
	if tui.ColorToOklch(pendingErrHex).C >= 0.005 {
		t.Fatalf("expected grayscale chroma when pending, got %+v", pendingErrHex)
	}
	SetTerminalColors(tui.TerminalColors{
		Background: &tui.RgbColor{R: 40, G: 42, B: 54},
		Foreground: &tui.RgbColor{R: 248, G: 248, B: 242},
	})
	restoredErrHex := CurrentTheme().Colors[FgError]
	if tui.ColorToOklch(restoredErrHex).C <= 0.01 {
		t.Fatalf("expected saturation restored after SetTerminalColors, got %+v", restoredErrHex)
	}

	// 3. InitTheme("") defaults to system; invalid InitTheme/SetTheme falls back active theme to system.
	sysT, err := InitTheme("")
	if err != nil || sysT == nil || sysT.Name != SystemThemeName {
		t.Fatalf("InitTheme(\"\") = (%+v, %v)", sysT, err)
	}
	res := SetTheme("definitely-nonexistent-theme-xyz")
	if res.Success || CurrentTheme().Name != SystemThemeName {
		t.Fatalf("expected fallback to system after invalid SetTheme, got res=%+v active=%q", res, CurrentTheme().Name)
	}
	if _, err := InitTheme("light/dark/extra"); err == nil || CurrentTheme().Name != SystemThemeName {
		t.Fatalf("expected fallback to system on invalid auto setting, got err=%v active=%q", err, CurrentTheme().Name)
	}

	// 4. GetResolvedThemeColors & IsLightTheme
	if !IsLightTheme("light") || IsLightTheme("dark") {
		t.Fatalf("IsLightTheme mismatch: light=%v dark=%v", IsLightTheme("light"), IsLightTheme("dark"))
	}
	lightColors := GetResolvedThemeColors("light")
	if lightColors[FgAccent] == "" {
		t.Fatalf("expected non-empty resolved accent color for light theme")
	}

	// 5. BOM-prefixed custom theme loading, SourcePath, validation filtering in discovery, locale sort, built-in paths.
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	themesDir := filepath.Join(agentDir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(utils.EnvAgentDir, agentDir)

	validPath := testWriteTempThemeJson(t, themesDir, "zeta.json", func() map[string]any {
		m := testLoadDarkJsonMap(t)
		m["name"] = "zeta"
		return m
	}())
	// Write a BOM-prefixed valid theme and an invalid JSON theme.
	bomRaw, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	bomRaw = append([]byte("\uFEFF"), []byte(strings.ReplaceAll(string(bomRaw), `"zeta"`, `"alpha"`))...)
	alphaPath := filepath.Join(themesDir, "alpha.json")
	if err := os.WriteFile(alphaPath, bomRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "invalid.json"), []byte(`{"name":"broken","colors":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	loadedAlpha, err := LoadThemeFromPath(alphaPath)
	if err != nil || loadedAlpha.Name != "alpha" || loadedAlpha.SourcePath != alphaPath {
		t.Fatalf("BOM LoadThemeFromPath failed: t=%+v err=%v", loadedAlpha, err)
	}

	availPaths := GetAvailableThemesWithPaths()
	if len(availPaths) < 5 || availPaths[0].Name != SystemThemeName {
		t.Fatalf("expected system first in GetAvailableThemesWithPaths: %+v", availPaths)
	}
	if slices.ContainsFunc(availPaths, func(item ThemeWithPath) bool { return item.Name == "broken" }) {
		t.Fatalf("invalid theme should be filtered during discovery: %+v", availPaths)
	}
	var darkEntry, alphaEntry ThemeWithPath
	var names []string
	for _, item := range availPaths {
		names = append(names, item.Name)
		if item.Name == "dark" {
			darkEntry = item
		}
		if item.Name == "alpha" {
			alphaEntry = item
		}
	}
	if darkEntry.Path == "" || !strings.HasSuffix(darkEntry.Path, filepath.Join("theme", "dark.json")) {
		t.Fatalf("expected built-in dark path in ThemesDir, got %+v", darkEntry)
	}
	if alphaEntry.Path != alphaPath {
		t.Fatalf("expected custom alpha path %s, got %+v", alphaPath, alphaEntry)
	}
	alphaIdx := slices.Index(names, "alpha")
	darkIdx := slices.Index(names, "dark")
	zetaIdx := slices.Index(names, "zeta")
	if !(0 < alphaIdx && alphaIdx < darkIdx && darkIdx < zetaIdx) {
		t.Fatalf("expected sorted order after system (alpha < dark < zeta), got %v", names)
	}

	// 6. Bold/Italic/etc. delegate to internal/ansi; GetBgAnsi panics on unknown; component themes match TS.
	_, _ = InitTheme("dark")
	ansi.Default().SetLevel(0)
	if got := CurrentTheme().Bold("hi"); got != "hi" {
		t.Fatalf("expected unstyled Bold at ansi level 0, got %q", got)
	}
	ansi.Default().SetLevel(3)
	if got := CurrentTheme().Bold("hi"); got != "\x1b[1mhi\x1b[22m" {
		t.Fatalf("expected SGR bold at ansi level 3, got %q", got)
	}

	func() {
		defer func() {
			r := recover()
			if r != "Unknown theme color: nope" {
				t.Fatalf("unexpected GetBgAnsi panic: %v", r)
			}
		}()
		CurrentTheme().GetBgAnsi("nope")
	}()

	th := CurrentTheme()
	if got := GetSelectListTheme().ScrollInfo("scroll"); got != th.Fg(FgMuted, "scroll") {
		t.Fatalf("ScrollInfo used wrong token: %q", got)
	}
	if got := GetEditorTheme().BorderColor("│"); got != th.Fg(FgBorderMuted, "│") {
		t.Fatalf("BorderColor used wrong token: %q", got)
	}
	if got := GetSettingsListTheme().Label("item", false); got != "item" {
		t.Fatalf("unselected SettingsList Label should be unstyled, got %q", got)
	}
	if got := GetSettingsListTheme().Description("desc"); got != th.Fg(FgDim, "desc") {
		t.Fatalf("SettingsList Description used wrong token: %q", got)
	}
}
