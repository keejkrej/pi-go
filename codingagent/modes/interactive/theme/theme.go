// Ported from packages/coding-agent/src/modes/interactive/theme/theme.ts (pi v1.5.2).

package theme

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/ansi"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// ThemeStyleOptions configures foreground, background, and SGR attributes in Theme.Style.
type ThemeStyleOptions struct {
	Fg            string
	Bg            string
	Bold          bool
	Dim           bool
	Italic        bool
	Underline     bool
	Inverse       bool
	Strikethrough bool
}

// ThemeExportColors holds CSS-ready color values used by HTML session export.
type ThemeExportColors struct {
	PageBg string `json:"pageBg,omitzero"`
	CardBg string `json:"cardBg,omitzero"`
	InfoBg string `json:"infoBg,omitzero"`
}

// AutoThemeSetting holds parsed light/dark theme names from a "light/dark" theme setting.
type AutoThemeSetting struct {
	LightTheme string
	DarkTheme  string
}

// ThemeWithPath pairs an available theme name with the JSON path on disk (empty for built-ins).
type ThemeWithPath struct {
	Name string
	Path string
}

// SetThemeResult describes the outcome of SetTheme.
type SetThemeResult struct {
	Success bool
	Error   string
}

// Theme represents a resolved UI color palette capable of emitting truecolor or 256-color ANSI.
type Theme struct {
	mu             sync.RWMutex
	Name           string
	SourcePath     string
	SourceInfo     *utils.SourceInfo
	Appearance     tui.TerminalColorScheme
	Colors         map[string]tui.Color
	mode           tui.TerminalColorMode
	fgAnsi         map[string]string
	bgAnsi         map[string]string
	isDefaultColor map[string]bool
	paletteSlot    map[string]int
	dimTokens      map[string]bool
	rawAppearance  string
	exportRaw      *ThemeExportJson
	varsRaw        map[string]any
}

var (
	themeMu            sync.RWMutex
	themeActive        *Theme
	themeActiveName    string
	themeTerminalCols  tui.TerminalColors
	themeColorScheme   *tui.TerminalColorScheme
	themeColorsPending bool
	themeRegistered    []*Theme
	themeValidator     func(name string, raw any) (ThemeJson, error) = ValidateThemeJson
	themeWatcher       *utils.FSWatcher
	themeWatchCallback func()
	themeChangeGen     atomic.Int64
	themeLiveInstances []*Theme
)

// Default foreground/background hex values matching dark.json / light.json defaults.
var (
	themeDefaultDarkFg, _  = tui.ParseColor("#e5e5e7")
	themeDefaultDarkBg, _  = tui.ParseColor("#000000")
	themeDefaultLightFg, _ = tui.ParseColor("#1c1c1e")
	themeDefaultLightBg, _ = tui.ParseColor("#ffffff")
)

// CurrentTheme returns the active global theme, lazily initializing the built-in "system" theme if needed.
func CurrentTheme() *Theme {
	themeMu.RLock()
	active := themeActive
	themeMu.RUnlock()
	if active != nil {
		return active
	}
	t, _ := InitTheme("")
	if t != nil {
		return t
	}
	return themeBuildSystem(tui.GetTerminalColorMode(nil))
}

func themeBuildFallbackDark() *Theme {
	t, err := themeLoadBuiltin("dark", tui.GetTerminalColorMode(nil))
	if err == nil && t != nil {
		return t
	}
	cols := make(map[string]tui.Color, len(ThemeColorFgTokens)+len(ThemeColorBgTokens))
	fg := make(map[string]string, len(ThemeColorFgTokens))
	bg := make(map[string]string, len(ThemeColorBgTokens))
	isDef := make(map[string]bool)
	for _, k := range ThemeColorFgTokens {
		cols[k] = themeDefaultDarkFg
		fg[k] = "\x1b[39m"
		isDef[k] = true
	}
	for _, k := range ThemeColorBgTokens {
		cols[k] = themeDefaultDarkBg
		bg[k] = "\x1b[49m"
		isDef[k] = true
	}
	return &Theme{
		Name:           "dark",
		Appearance:     tui.TerminalColorSchemeDark,
		Colors:         cols,
		mode:           tui.GetTerminalColorMode(nil),
		fgAnsi:         fg,
		bgAnsi:         bg,
		isDefaultColor: isDef,
		rawAppearance:  "dark",
	}
}

// DetectColorFgBgTheme inspects the COLORFGBG environment variable using Vim's background heuristic.
func DetectColorFgBgTheme(env ...map[string]string) *tui.TerminalColorScheme {
	var val string
	if len(env) > 0 && env[0] != nil {
		val = env[0]["COLORFGBG"]
	} else if len(env) == 0 {
		val = os.Getenv("COLORFGBG")
	}
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ";")
	last := strings.TrimSpace(parts[len(parts)-1])
	n, err := strconv.Atoi(last)
	if err != nil {
		return nil
	}
	var scheme tui.TerminalColorScheme
	if (n >= 0 && n <= 6) || n == 8 {
		scheme = tui.TerminalColorSchemeDark
	} else {
		scheme = tui.TerminalColorSchemeLight
	}
	return &scheme
}

// DetectTerminalTheme infers "dark" vs "light" preferring reported terminal background
// (and readable foreground), then reported scheme, then COLORFGBG, defaulting to "dark".
func DetectTerminalTheme(colors tui.TerminalColors, scheme *tui.TerminalColorScheme, env ...map[string]string) tui.TerminalColorScheme {
	if colors.Background != nil {
		return TerminalAppearance(*colors.Background, colors.Foreground)
	}
	if scheme != nil && (*scheme == tui.TerminalColorSchemeDark || *scheme == tui.TerminalColorSchemeLight) {
		return *scheme
	}
	if fgbg := DetectColorFgBgTheme(env...); fgbg != nil {
		return *fgbg
	}
	return tui.TerminalColorSchemeDark
}

// ParseAutoThemeSetting splits a "light/dark" automatic theme setting into its two names.
func ParseAutoThemeSetting(setting string) *AutoThemeSetting {
	if setting == "" || !strings.ContainsRune(setting, '/') {
		return nil
	}
	parts := strings.Split(setting, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	return &AutoThemeSetting{
		LightTheme: parts[0],
		DarkTheme:  parts[1],
	}
}

// ResolveThemeSetting selects the concrete theme name from a fixed setting or "light/dark" pair.
func ResolveThemeSetting(setting string, scheme tui.TerminalColorScheme) string {
	if !strings.ContainsRune(setting, '/') {
		return setting
	}
	parsed := ParseAutoThemeSetting(setting)
	if parsed == nil {
		return ""
	}
	if scheme == tui.TerminalColorSchemeLight {
		return parsed.LightTheme
	}
	return parsed.DarkTheme
}

// SetThemeJsonValidator overrides the JSON validator used when loading themes.
func SetThemeJsonValidator(validator func(name string, raw any) (ThemeJson, error)) {
	themeMu.Lock()
	defer themeMu.Unlock()
	if validator == nil {
		themeValidator = ValidateThemeJson
	} else {
		themeValidator = validator
	}
}

// SetRegisteredThemes registers extension-supplied themes searched after built-ins.
func SetRegisteredThemes(themes []*Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	themeRegistered = slices.Clone(themes)
}

func themeCloneScheme(s *tui.TerminalColorScheme) *tui.TerminalColorScheme {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func themeRefreshAll(colors tui.TerminalColors, live []*Theme, active *Theme) {
	seenActive := false
	for _, t := range live {
		if t == nil {
			continue
		}
		if t == active {
			seenActive = true
		}
		if t.Name == SystemThemeName {
			t.refreshSystem(colors)
		} else {
			t.refreshDynamicColors(colors)
		}
	}
	if active != nil && !seenActive {
		if active.Name == SystemThemeName {
			active.refreshSystem(colors)
		} else {
			active.refreshDynamicColors(colors)
		}
	}
}

// SetTerminalColors updates cached terminal foreground/background/palette colors and refreshes
// live default-color / palette-indexed tokens and system theme state.
func SetTerminalColors(colors tui.TerminalColors) {
	themeMu.Lock()
	themeTerminalCols = colors
	themeColorsPending = false
	live := make([]*Theme, len(themeLiveInstances))
	copy(live, themeLiveInstances)
	active := themeActive
	themeMu.Unlock()

	themeRefreshAll(colors, live, active)
	themeChangeGen.Add(1)
}

// SetTerminalColorScheme stores the reported terminal color scheme hint and refreshes
// any live system themes.
func SetTerminalColorScheme(scheme *tui.TerminalColorScheme) {
	themeMu.Lock()
	themeColorScheme = themeCloneScheme(scheme)
	cols := themeTerminalCols
	live := make([]*Theme, len(themeLiveInstances))
	copy(live, themeLiveInstances)
	active := themeActive
	themeMu.Unlock()

	themeRefreshAll(cols, live, active)
	themeChangeGen.Add(1)
}

// MarkTerminalColorsPending marks terminal color detection as in flight so the system
// theme renders with muted grayscale saturation until SetTerminalColors completes.
func MarkTerminalColorsPending() {
	themeMu.Lock()
	themeColorsPending = true
	cols := themeTerminalCols
	live := make([]*Theme, len(themeLiveInstances))
	copy(live, themeLiveInstances)
	active := themeActive
	themeMu.Unlock()

	themeRefreshAll(cols, live, active)
	themeChangeGen.Add(1)
}

// GetTerminalTheme returns the detected terminal light/dark scheme from reported
// colors, stored scheme hint, or COLORFGBG.
func GetTerminalTheme() tui.TerminalColorScheme {
	themeMu.RLock()
	cols := themeTerminalCols
	scheme := themeCloneScheme(themeColorScheme)
	themeMu.RUnlock()
	return DetectTerminalTheme(cols, scheme)
}

func themeGetTerminalColors() tui.TerminalColors {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return themeTerminalCols
}

func themeTrackLive(t *Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	themeLiveInstances = append(themeLiveInstances, t)
}

func themeResolveVar(val any, vars map[string]any, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("circular variable reference in theme")
	}
	str, ok := val.(string)
	if !ok || str == "" || vars == nil {
		return val, nil
	}
	// Resolve named variables in vars map recursively.
	if target, exists := vars[str]; exists {
		return themeResolveVar(target, vars, depth+1)
	}
	return val, nil
}

func themeConcreteFallbackColor(token string, rawVal any, appearance tui.TerminalColorScheme, termCols tui.TerminalColors, paletteSlot int) tui.Color {
	if paletteSlot >= 0 && paletteSlot < len(termCols.Palette) {
		p := termCols.Palette[paletteSlot]
		if c, err := tui.NewRgbColor(p.R, p.G, p.B); err == nil {
			return c
		}
	}
	if IsBgToken(token) {
		if termCols.Background != nil {
			if c, err := tui.NewRgbColor(termCols.Background.R, termCols.Background.G, termCols.Background.B); err == nil {
				return c
			}
		}
		if appearance == tui.TerminalColorSchemeLight {
			return themeDefaultLightBg
		}
		return themeDefaultDarkBg
	}
	if termCols.Foreground != nil {
		if c, err := tui.NewRgbColor(termCols.Foreground.R, termCols.Foreground.G, termCols.Foreground.B); err == nil {
			return c
		}
	}
	if appearance == tui.TerminalColorSchemeLight {
		return themeDefaultLightFg
	}
	return themeDefaultDarkFg
}

func themeBuildFromJson(parsed ThemeJson, mode tui.TerminalColorMode, sourcePath ...string) (*Theme, error) {
	termCols := themeGetTerminalColors()
	dimSet := make(map[string]bool)

	rawApp := parsed.Appearance
	app := tui.TerminalColorSchemeDark
	if rawApp == string(tui.TerminalColorSchemeLight) {
		app = tui.TerminalColorSchemeLight
	}

	colorsMap := make(map[string]tui.Color, len(ThemeColorFgTokens)+len(ThemeColorBgTokens))
	fgAnsiMap := make(map[string]string, len(ThemeColorFgTokens))
	bgAnsiMap := make(map[string]string, len(ThemeColorBgTokens))
	isDefaultMap := make(map[string]bool)
	paletteSlots := make(map[string]int)

	resolveTokenValue := func(token string) (any, error) {
		if v, exists := parsed.Colors[token]; exists {
			return themeResolveVar(v, parsed.Vars, 0)
		}
		if fb, hasFb := ThemeOptionalFallbacks[token]; hasFb {
			if v, exists := parsed.Colors[fb]; exists {
				return themeResolveVar(v, parsed.Vars, 0)
			}
		}
		return "", nil
	}

	parseResolved := func(token string, resolved any, curApp tui.TerminalColorScheme) (tui.Color, string, bool, int, error) {
		if s, ok := resolved.(string); ok && s == "" {
			slot := -1
			concrete := themeConcreteFallbackColor(token, resolved, curApp, termCols, slot)
			if IsBgToken(token) {
				return concrete, "\x1b[49m", true, slot, nil
			}
			return concrete, "\x1b[39m", true, slot, nil
		}

		// Record ANSI 0..15 palette index so terminal palette updates propagate dynamically.
		slot := -1
		switch idx := resolved.(type) {
		case int:
			if idx >= 0 && idx < 16 {
				slot = idx
			}
		case float64:
			if idx >= 0 && idx < 16 && idx == float64(int(idx)) {
				slot = int(idx)
			}
		}

		col, err := tui.ParseColor(resolved)
		if err != nil {
			return nil, "", false, slot, fmt.Errorf("Invalid color for %q: %w", token, err)
		}
		var ansi string
		if IsBgToken(token) {
			ansi = tui.BackgroundAnsi(col, mode)
		} else {
			ansi = tui.ForegroundAnsi(col, mode)
		}
		if slot >= 0 && slot < len(termCols.Palette) {
			col = themeConcreteFallbackColor(token, resolved, curApp, termCols, slot)
		}
		return col, ansi, false, slot, nil
	}

	// Detect appearance from theme background/text colors if not explicitly declared.
	if rawApp == "" {
		app = themeDetectJsonAppearance(parsed, termCols)
	}

	allTokens := make([]string, 0, len(ThemeColorFgTokens)+len(ThemeColorBgTokens))
	allTokens = append(allTokens, ThemeColorFgTokens...)
	allTokens = append(allTokens, ThemeColorBgTokens...)

	for _, tok := range allTokens {
		resolved, err := resolveTokenValue(tok)
		if err != nil {
			return nil, err
		}
		col, ansi, isDef, slot, err := parseResolved(tok, resolved, app)
		if err != nil {
			return nil, err
		}
		colorsMap[tok] = col
		if isDef {
			isDefaultMap[tok] = true
		}
		if slot >= 0 {
			paletteSlots[tok] = slot
		}
		if IsBgToken(tok) {
			bgAnsiMap[tok] = ansi
		} else {
			fgAnsiMap[tok] = ansi
		}
	}

	srcPath := ""
	if len(sourcePath) > 0 {
		srcPath = sourcePath[0]
	}
	t := &Theme{
		Name:           parsed.Name,
		SourcePath:     srcPath,
		Appearance:     app,
		Colors:         colorsMap,
		mode:           mode,
		fgAnsi:         fgAnsiMap,
		bgAnsi:         bgAnsiMap,
		isDefaultColor: isDefaultMap,
		paletteSlot:    paletteSlots,
		dimTokens:      dimSet,
		rawAppearance:  rawApp,
		exportRaw:      parsed.Export,
		varsRaw:        parsed.Vars,
	}
	themeTrackLive(t)
	return t, nil
}

func themeDetectJsonAppearance(parsed ThemeJson, termCols tui.TerminalColors) tui.TerminalColorScheme {
	// Check whether panel backgrounds are ANSI 0-15 palette references (which follow terminal background).
	resolvedBg, err := themeResolveVar(parsed.Colors[BgUserMessage], parsed.Vars, 0)
	if err == nil {
		if idx, ok := themeAsPaletteIdx(resolvedBg); ok && idx < 16 {
			if termCols.Background != nil {
				return DetectTerminalTheme(termCols, nil)
			}
			// Palette index 0..6 and 8 are dark; 7 and 9..15 are light.
			if (idx >= 0 && idx <= 6) || idx == 8 {
				return tui.TerminalColorSchemeDark
			}
			return tui.TerminalColorSchemeLight
		}
		if col, err := tui.ParseColor(resolvedBg); err == nil {
			rgb := tui.ColorToRgb(col)
			if stOklch(rgb).L >= 0.55 {
				return tui.TerminalColorSchemeLight
			}
			return tui.TerminalColorSchemeDark
		}
	}
	if termCols.Background != nil {
		return DetectTerminalTheme(termCols, nil)
	}
	return tui.TerminalColorSchemeDark
}

func themeAsPaletteIdx(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, x >= 0 && x <= 255
	case float64:
		if x >= 0 && x <= 255 && x == float64(int(x)) {
			return int(x), true
		}
	}
	return 0, false
}

func themeBuildSystem(mode tui.TerminalColorMode) *Theme {
	termCols := themeGetTerminalColors()
	t := &Theme{
		Name:           "system",
		mode:           mode,
		Colors:         make(map[string]tui.Color),
		fgAnsi:         make(map[string]string),
		bgAnsi:         make(map[string]string),
		isDefaultColor: make(map[string]bool),
		paletteSlot:    make(map[string]int),
		dimTokens:      make(map[string]bool),
	}
	t.refreshSystem(termCols)
	themeTrackLive(t)
	return t
}

func (t *Theme) refreshSystem(termCols tui.TerminalColors) {
	themeMu.RLock()
	scheme := themeCloneScheme(themeColorScheme)
	pending := themeColorsPending
	themeMu.RUnlock()

	hint := DetectTerminalTheme(termCols, scheme)
	sat := 1.0
	if pending {
		sat = 0.0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	sys := GenerateSystemThemeColors(SystemThemeInput{
		Background:     termCols.Background,
		Foreground:     termCols.Foreground,
		Palette:        termCols.Palette,
		AppearanceHint: &hint,
		Saturation:     &sat,
	})
	t.Appearance = sys.Appearance
	t.rawAppearance = string(sys.Appearance)

	t.dimTokens = make(map[string]bool, len(sys.Dim))
	for _, d := range sys.Dim {
		t.dimTokens[d] = true
	}
	t.isDefaultColor = make(map[string]bool)
	t.paletteSlot = make(map[string]int)

	for tok, raw := range sys.Colors {
		if s, ok := raw.(string); ok && s == "" {
			t.isDefaultColor[tok] = true
			t.Colors[tok] = themeConcreteFallbackColor(tok, raw, sys.Appearance, termCols, -1)
			if IsBgToken(tok) {
				t.bgAnsi[tok] = "\x1b[49m"
			} else {
				t.fgAnsi[tok] = "\x1b[39m"
			}
			continue
		}
		if slot, ok := themeAsPaletteIdx(raw); ok && slot < 16 {
			t.paletteSlot[tok] = slot
		}
		col, err := tui.ParseColor(raw)
		if err != nil {
			continue
		}
		t.Colors[tok] = col
		if IsBgToken(tok) {
			t.bgAnsi[tok] = tui.BackgroundAnsi(col, t.mode)
		} else {
			t.fgAnsi[tok] = tui.ForegroundAnsi(col, t.mode)
		}
	}
}

func (t *Theme) refreshDynamicColors(termCols tui.TerminalColors) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.rawAppearance == "" && termCols.Background != nil && len(t.paletteSlot) > 0 {
		t.Appearance = DetectTerminalTheme(termCols, nil)
	}
	for tok := range t.isDefaultColor {
		t.Colors[tok] = themeConcreteFallbackColor(tok, "", t.Appearance, termCols, -1)
	}
	for tok, slot := range t.paletteSlot {
		if slot < len(termCols.Palette) {
			p := termCols.Palette[slot]
			if c, err := tui.NewRgbColor(p.R, p.G, p.B); err == nil {
				t.Colors[tok] = c
			}
		}
	}
}

// GetColorMode returns the active ANSI color mode ("truecolor" or "256color") for this theme.
func (t *Theme) GetColorMode() tui.TerminalColorMode {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.mode
}

// GetFgAnsi returns the SGR escape sequence that selects the foreground color for token.
func (t *Theme) GetFgAnsi(token string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !IsFgToken(token) {
		panic("Unknown theme color: " + token)
	}
	ans := t.fgAnsi[token]
	if t.dimTokens[token] {
		return ans + "\x1b[2m"
	}
	return ans
}

// GetBgAnsi returns the SGR escape sequence that selects the background color for token.
func (t *Theme) GetBgAnsi(token string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !IsBgToken(token) {
		panic("Unknown theme color: " + token)
	}
	return t.bgAnsi[token]
}

// Fg styles text using the foreground theme color token. Panics on unknown/non-foreground tokens.
func (t *Theme) Fg(token, text string) string {
	t.mu.RLock()
	if !IsFgToken(token) {
		t.mu.RUnlock()
		panic("Unknown theme color: " + token)
	}
	ansi := t.fgAnsi[token]
	isDim := t.dimTokens[token]
	t.mu.RUnlock()

	if isDim {
		return ansi + "\x1b[2m" + text + "\x1b[22;39m"
	}
	return ansi + text + "\x1b[39m"
}

// Bg styles text using the background theme color token. Panics on unknown/non-background tokens.
func (t *Theme) Bg(token, text string) string {
	t.mu.RLock()
	if !IsBgToken(token) {
		t.mu.RUnlock()
		panic("Unknown theme color: " + token)
	}
	ansi := t.bgAnsi[token]
	t.mu.RUnlock()

	return ansi + text + "\x1b[49m"
}

// Style renders text using any combination of theme foreground, background, and SGR attributes,
// matching pi-tui's StyleText conventions while handling default-color & dim theme tokens.
func (t *Theme) Style(text string, opts ThemeStyleOptions) string {
	t.mu.RLock()
	mode := t.mode
	var fgCol tui.Color
	var fgIsDef, fgIsDim bool
	if opts.Fg != "" {
		if !IsFgToken(opts.Fg) {
			t.mu.RUnlock()
			panic("Unknown theme color: " + opts.Fg)
		}
		fgCol = t.Colors[opts.Fg]
		fgIsDef = t.isDefaultColor[opts.Fg]
		fgIsDim = t.dimTokens[opts.Fg]
	}

	var bgCol tui.Color
	var bgIsDef bool
	if opts.Bg != "" {
		if !IsBgToken(opts.Bg) {
			t.mu.RUnlock()
			panic("Unknown theme color: " + opts.Bg)
		}
		bgCol = t.Colors[opts.Bg]
		bgIsDef = t.isDefaultColor[opts.Bg]
	}
	t.mu.RUnlock()

	useDim := opts.Dim || fgIsDim
	if (opts.Fg != "" && fgIsDef) || (opts.Bg != "" && bgIsDef) {
		innerOpts := opts
		innerOpts.Fg = ""
		innerOpts.Bg = ""
		var styledFg, styledBg tui.Color
		if opts.Fg != "" && !fgIsDef {
			styledFg = fgCol
		}
		if opts.Bg != "" && !bgIsDef {
			styledBg = bgCol
		}
		attrs := tui.TextAttributes{}
		if opts.Bold {
			v := true
			attrs.Bold = &v
		}
		if useDim {
			v := true
			attrs.Dim = &v
		}
		if opts.Italic {
			v := true
			attrs.Italic = &v
		}
		if opts.Underline {
			v := true
			attrs.Underline = &v
		}
		if opts.Inverse {
			v := true
			attrs.Inverse = &v
		}
		if opts.Strikethrough {
			v := true
			attrs.Strikethrough = &v
		}
		out := tui.StyleText(text, tui.TextStyle{
			TextAttributes: attrs,
			Fg:             styledFg,
			Bg:             styledBg,
		}, mode)
		if opts.Fg != "" && fgIsDef {
			out = "\x1b[39m" + out + "\x1b[39m"
		}
		if opts.Bg != "" && bgIsDef {
			out = "\x1b[49m" + out + "\x1b[49m"
		}
		return out
	}

	attrs := tui.TextAttributes{}
	if opts.Bold {
		v := true
		attrs.Bold = &v
	}
	if useDim {
		v := true
		attrs.Dim = &v
	}
	if opts.Italic {
		v := true
		attrs.Italic = &v
	}
	if opts.Underline {
		v := true
		attrs.Underline = &v
	}
	if opts.Inverse {
		v := true
		attrs.Inverse = &v
	}
	if opts.Strikethrough {
		v := true
		attrs.Strikethrough = &v
	}
	return tui.StyleText(text, tui.TextStyle{
		TextAttributes: attrs,
		Fg:             fgCol,
		Bg:             bgCol,
	}, mode)
}

func (t *Theme) Bold(text string) string          { return ansi.Bold(text) }
func (t *Theme) Italic(text string) string        { return ansi.Italic(text) }
func (t *Theme) Underline(text string) string     { return ansi.Underline(text) }
func (t *Theme) Inverse(text string) string       { return ansi.Inverse(text) }
func (t *Theme) Strikethrough(text string) string { return ansi.Strikethrough(text) }

// GetThinkingBorderColor returns a foreground styling closure corresponding to thinking level.
func (t *Theme) GetThinkingBorderColor(level ai.ModelThinkingLevel) func(string) string {
	var token string
	switch level {
	case ai.ModelThinkingLevelOff:
		token = FgThinkingOff
	case ai.ModelThinkingLevelMinimal:
		token = FgThinkingMinimal
	case ai.ModelThinkingLevelLow:
		token = FgThinkingLow
	case ai.ModelThinkingLevelMedium:
		token = FgThinkingMedium
	case ai.ModelThinkingLevelHigh:
		token = FgThinkingHigh
	case ai.ModelThinkingLevelXhigh:
		token = FgThinkingXhigh
	case ai.ModelThinkingLevelMax:
		token = FgThinkingMax
	default:
		token = FgThinkingOff
	}
	return func(s string) string { return t.Fg(token, s) }
}

func themeLoadBuiltin(name string, mode tui.TerminalColorMode) (*Theme, error) {
	if name == "system" {
		return themeBuildSystem(mode), nil
	}
	var assetPath string
	switch name {
	case "dark":
		assetPath = "theme/dark.json"
	case "light":
		assetPath = "theme/light.json"
	default:
		return nil, fmt.Errorf("unknown built-in theme: %s", name)
	}
	data, err := fs.ReadFile(utils.ConfigAssetsFS(), assetPath)
	if err != nil {
		return nil, err
	}
	clean := utils.StripBom(string(data))
	var raw any
	if err := jsonx.Unmarshal([]byte(clean), &raw); err != nil {
		return nil, err
	}
	themeMu.RLock()
	validator := themeValidator
	themeMu.RUnlock()
	parsed, err := validator(name, raw)
	if err != nil {
		return nil, err
	}
	return themeBuildFromJson(parsed, mode)
}

// LoadThemeFromPath reads, validates, and constructs a Theme from a JSON file path.
func LoadThemeFromPath(themePath string, mode ...tui.TerminalColorMode) (*Theme, error) {
	data, err := os.ReadFile(themePath)
	if err != nil {
		return nil, err
	}
	clean := utils.StripBom(string(data))
	var raw any
	if err := jsonx.Unmarshal([]byte(clean), &raw); err != nil {
		return nil, err
	}
	baseName := strings.TrimSuffix(filepath.Base(themePath), ".json")
	themeMu.RLock()
	validator := themeValidator
	themeMu.RUnlock()
	parsed, err := validator(baseName, raw)
	if err != nil {
		return nil, err
	}
	colorMode := tui.GetTerminalColorMode(nil)
	if len(mode) > 0 && mode[0] != "" {
		colorMode = mode[0]
	}
	return themeBuildFromJson(parsed, colorMode, themePath)
}

func themeDiscoverCustomPaths() []ThemeWithPath {
	customDir := utils.GetCustomThemesDir()
	entries, err := os.ReadDir(customDir)
	if err != nil {
		return nil
	}
	var out []ThemeWithPath
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(customDir, entry.Name())
		t, err := LoadThemeFromPath(fullPath)
		if err != nil || t == nil || t.Name == "" {
			continue
		}
		out = append(out, ThemeWithPath{
			Name: t.Name,
			Path: fullPath,
		})
	}
	return out
}

// GetThemeByName resolves a built-in ("system", "dark", "light"), registered, or custom theme by name.
func GetThemeByName(name string) (*Theme, error) {
	mode := tui.GetTerminalColorMode(nil)
	if name == "system" || name == "dark" || name == "light" {
		return themeLoadBuiltin(name, mode)
	}
	themeMu.RLock()
	registered := slices.Clone(themeRegistered)
	themeMu.RUnlock()
	for _, r := range registered {
		if r != nil && r.Name == name {
			return r, nil
		}
	}
	for _, custom := range themeDiscoverCustomPaths() {
		if custom.Name == name {
			return LoadThemeFromPath(custom.Path, mode)
		}
	}
	directPath := filepath.Join(utils.GetCustomThemesDir(), name+".json")
	if _, err := os.Stat(directPath); err == nil {
		return LoadThemeFromPath(directPath, mode)
	}
	return nil, fmt.Errorf("Theme not found: %s", name)
}

// GetAvailableThemes lists available theme names with "system" first, followed by
// remaining themes sorted with locale ordering.
func GetAvailableThemes() []string {
	withPaths := GetAvailableThemesWithPaths()
	names := make([]string, 0, len(withPaths))
	for _, item := range withPaths {
		names = append(names, item.Name)
	}
	return names
}

// GetAvailableThemesWithPaths lists all available themes alongside their JSON file paths on disk.
func GetAvailableThemesWithPaths() []ThemeWithPath {
	seen := make(map[string]struct{})
	var out []ThemeWithPath
	add := func(item ThemeWithPath) {
		if item.Name == "" {
			return
		}
		if _, ok := seen[item.Name]; ok {
			return
		}
		seen[item.Name] = struct{}{}
		out = append(out, item)
	}

	themesDir := utils.GetThemesDir()
	add(ThemeWithPath{Name: SystemThemeName})
	add(ThemeWithPath{Name: "dark", Path: filepath.Join(themesDir, "dark.json")})
	add(ThemeWithPath{Name: "light", Path: filepath.Join(themesDir, "light.json")})

	themeMu.RLock()
	registered := slices.Clone(themeRegistered)
	themeMu.RUnlock()
	for _, r := range registered {
		if r != nil {
			add(ThemeWithPath{Name: r.Name, Path: r.SourcePath})
		}
	}
	for _, custom := range themeDiscoverCustomPaths() {
		add(custom)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == SystemThemeName {
			return out[j].Name != SystemThemeName
		}
		if out[j].Name == SystemThemeName {
			return false
		}
		return js.LocaleCompare(out[i].Name, out[j].Name) < 0
	})
	return out
}

func themeConvertExportValue(raw any, vars map[string]any) string {
	if raw == nil {
		return ""
	}
	resolved, err := themeResolveVar(raw, vars, 0)
	if err != nil || resolved == nil {
		return ""
	}
	if s, ok := resolved.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return ""
		}
		// CSS supports oklch() and hex directly, whereas okhsl() and 256-color indices must become #rrggbb hex.
		if strings.HasPrefix(strings.ToLower(trimmed), "okhsl(") {
			if col, err := tui.ParseColor(trimmed); err == nil {
				return tui.ColorToHex(col)
			}
		}
		return trimmed
	}
	if _, isIdx := themeAsPaletteIdx(resolved); isIdx {
		if col, err := tui.ParseColor(resolved); err == nil {
			return tui.ColorToHex(col)
		}
	}
	return ""
}

func themeResolveTarget(themeName ...string) *Theme {
	targetName := ""
	if len(themeName) > 0 {
		targetName = themeName[0]
	}
	scheme := GetTerminalTheme()
	if targetName != "" {
		if resolved := ResolveThemeSetting(targetName, scheme); resolved != "" {
			if loaded, err := GetThemeByName(resolved); err == nil && loaded != nil {
				return loaded
			}
		}
		return CurrentTheme()
	}

	themeMu.RLock()
	active := themeActive
	activeName := themeActiveName
	themeMu.RUnlock()
	if active != nil {
		return active
	}
	if activeName != "" {
		if resolved := ResolveThemeSetting(activeName, scheme); resolved != "" {
			if loaded, err := GetThemeByName(resolved); err == nil && loaded != nil {
				return loaded
			}
		}
	}
	return CurrentTheme()
}

// GetResolvedThemeColors returns a map of all theme color tokens to their resolved hex colors.
func GetResolvedThemeColors(themeName ...string) map[string]string {
	t := themeResolveTarget(themeName...)
	if t == nil {
		return map[string]string{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]string, len(t.Colors))
	for tok, col := range t.Colors {
		out[tok] = tui.ColorToHex(col)
	}
	return out
}

// IsLightTheme reports whether the target (or currently active) theme has light appearance.
func IsLightTheme(themeName ...string) bool {
	t := themeResolveTarget(themeName...)
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Appearance == tui.TerminalColorSchemeLight
}

// GetThemeExportColors returns CSS colors for HTML session export for themeName (or the active
// theme if omitted/missing). Returns empty ThemeExportColors for "system".
func GetThemeExportColors(themeName ...string) ThemeExportColors {
	targetName := ""
	if len(themeName) > 0 {
		targetName = themeName[0]
	}
	if targetName == SystemThemeName {
		return ThemeExportColors{}
	}

	var t *Theme
	if targetName != "" {
		if resolved := ResolveThemeSetting(targetName, GetTerminalTheme()); resolved != "" {
			if resolved == SystemThemeName {
				return ThemeExportColors{}
			}
			loaded, err := GetThemeByName(resolved)
			if err == nil && loaded != nil {
				t = loaded
			}
		}
	}
	if t == nil {
		t = CurrentTheme()
	}
	if t == nil || t.Name == SystemThemeName || t.exportRaw == nil {
		return ThemeExportColors{}
	}
	return ThemeExportColors{
		PageBg: themeConvertExportValue(t.exportRaw.PageBg, t.varsRaw),
		CardBg: themeConvertExportValue(t.exportRaw.CardBg, t.varsRaw),
		InfoBg: themeConvertExportValue(t.exportRaw.InfoBg, t.varsRaw),
	}
}

// InitTheme initializes the global active theme and optionally watches custom theme files.
func InitTheme(themeName string, enableWatcher ...bool) (*Theme, error) {
	StopThemeWatcher()

	requested := themeName
	if requested == "" {
		requested = SystemThemeName
	}

	scheme := GetTerminalTheme()
	resolvedName := ResolveThemeSetting(requested, scheme)

	var t *Theme
	var err error
	activeName := requested
	if resolvedName == "" {
		err = fmt.Errorf("Invalid theme setting: %s", themeName)
	} else {
		t, err = GetThemeByName(resolvedName)
	}
	if err != nil || t == nil {
		// Fall back safely to "system" when initialization fails.
		t = themeBuildSystem(tui.GetTerminalColorMode(nil))
		activeName = SystemThemeName
	}

	themeMu.Lock()
	themeActive = t
	themeActiveName = activeName
	themeMu.Unlock()
	themeChangeGen.Add(1)

	watch := len(enableWatcher) > 0 && enableWatcher[0]
	if watch && err == nil {
		themeStartWatcher(resolvedName)
	}

	return t, err
}

// SetTheme switches the active global theme by name or "light/dark" setting.
func SetTheme(name string, enableWatcher ...bool) SetThemeResult {
	StopThemeWatcher()

	scheme := GetTerminalTheme()
	resolvedName := ResolveThemeSetting(name, scheme)
	if resolvedName == "" {
		fallback := themeBuildSystem(tui.GetTerminalColorMode(nil))
		themeMu.Lock()
		themeActive = fallback
		themeActiveName = SystemThemeName
		themeMu.Unlock()
		themeChangeGen.Add(1)
		return SetThemeResult{Success: false, Error: fmt.Sprintf("Invalid theme setting: %s", name)}
	}

	t, err := GetThemeByName(resolvedName)
	if err != nil {
		fallback := themeBuildSystem(tui.GetTerminalColorMode(nil))
		themeMu.Lock()
		themeActive = fallback
		themeActiveName = SystemThemeName
		themeMu.Unlock()
		themeChangeGen.Add(1)
		return SetThemeResult{Success: false, Error: err.Error()}
	}

	themeMu.Lock()
	themeActive = t
	themeActiveName = name
	cb := themeWatchCallback
	themeMu.Unlock()
	themeChangeGen.Add(1)

	if len(enableWatcher) > 0 && enableWatcher[0] {
		themeStartWatcher(resolvedName)
	}
	if cb != nil {
		cb()
	}
	return SetThemeResult{Success: true}
}

// SetThemeInstance directly installs a Theme instance as the active global theme.
func SetThemeInstance(t *Theme) {
	if t == nil {
		return
	}
	themeMu.Lock()
	themeActive = t
	themeActiveName = t.Name
	cb := themeWatchCallback
	themeMu.Unlock()
	themeChangeGen.Add(1)
	if cb != nil {
		cb()
	}
}

// OnThemeChange registers a callback invoked when the active theme changes or reloads.
func OnThemeChange(cb func()) {
	themeMu.Lock()
	defer themeMu.Unlock()
	themeWatchCallback = cb
}

// StopThemeWatcher stops any active custom theme file watcher.
func StopThemeWatcher() {
	themeMu.Lock()
	w := themeWatcher
	themeWatcher = nil
	themeMu.Unlock()
	if w != nil {
		utils.CloseWatcher(w)
	}
}

// Invalidate bumps the global theme generation so cached highlights/renders rebuild.
func Invalidate() {
	themeChangeGen.Add(1)
}

func themeStartWatcher(name string) {
	for _, item := range themeDiscoverCustomPaths() {
		if item.Name != name || item.Path == "" {
			continue
		}
		pathToWatch := item.Path
		w := utils.WatchWithErrorHandler(pathToWatch, func(eventType, _ string) {
			if reloaded, err := LoadThemeFromPath(pathToWatch); err == nil {
				SetThemeInstance(reloaded)
			}
		}, nil)
		themeMu.Lock()
		themeWatcher = w
		themeMu.Unlock()
		return
	}
}

var themeExtToLang = map[string]string{
	".ts":     "typescript",
	".tsx":    "typescript",
	".mts":    "typescript",
	".cts":    "typescript",
	".js":     "javascript",
	".jsx":    "javascript",
	".mjs":    "javascript",
	".cjs":    "javascript",
	".py":     "python",
	".pyi":    "python",
	".go":     "go",
	".rs":     "rust",
	".java":   "java",
	".kt":     "kotlin",
	".kts":    "kotlin",
	".scala":  "scala",
	".sc":     "scala",
	".swift":  "swift",
	".dart":   "dart",
	".groovy": "groovy",
	".gradle": "groovy",
	".c":      "c",
	".h":      "c",
	".cpp":    "cpp",
	".cc":     "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".hh":     "cpp",
	".hxx":    "cpp",
	".cs":     "csharp",
	".php":    "php",
	".rb":     "ruby",
	".pl":     "perl",
	".pm":     "perl",
	".lua":    "lua",
	".nix":    "nix",
	".sh":     "bash",
	".bash":   "bash",
	".zsh":    "bash",
	".json":   "json",
	".yaml":   "yaml",
	".yml":    "yaml",
	".toml":   "toml",
	".md":     "markdown",
	".css":    "css",
	".html":   "html",
	".htm":    "html",
	".xml":    "xml",
	".sql":    "sql",
	".diff":   "diff",
	".patch":  "diff",
}

// GetLanguageFromPath maps a filename or path extension to its highlight language identifier,
// or returns nil when unrecognized.
func GetLanguageFromPath(filePath string) *string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if lang, ok := themeExtToLang[ext]; ok {
		return &lang
	}
	base := strings.ToLower(filepath.Base(filePath))
	if base == "build.gradle" {
		lang := "groovy"
		return &lang
	}
	if base == "dockerfile" {
		lang := "dockerfile"
		return &lang
	}
	if base == "makefile" {
		lang := "makefile"
		return &lang
	}
	return nil
}

// BuildCliHighlightTheme constructs an utils.HighlightTheme mapping highlight.js scopes to theme styling functions.
func BuildCliHighlightTheme(t *Theme) utils.HighlightTheme {
	if t == nil {
		t = CurrentTheme()
	}
	fn := func(token string) func(string) string {
		return func(s string) string { return t.Fg(token, s) }
	}
	return utils.HighlightTheme{
		"comment":           fn(FgSyntaxComment),
		"quote":             fn(FgSyntaxComment),
		"keyword":           fn(FgSyntaxKeyword),
		"selector-tag":      fn(FgSyntaxKeyword),
		"name":              fn(FgSyntaxKeyword),
		"tag":               fn(FgSyntaxPunctuation),
		"title":             fn(FgSyntaxFunction),
		"title.function":    fn(FgSyntaxFunction),
		"function":          fn(FgSyntaxFunction),
		"section":           fn(FgSyntaxFunction),
		"variable":          fn(FgSyntaxVariable),
		"template-variable": fn(FgSyntaxVariable),
		"attr":              fn(FgSyntaxVariable),
		"params":            fn(FgSyntaxVariable),
		"property":          fn(FgSyntaxVariable),
		"string":            fn(FgSyntaxString),
		"regexp":            fn(FgSyntaxString),
		"bullet":            fn(FgSyntaxString),
		"number":            fn(FgSyntaxNumber),
		"literal":           fn(FgSyntaxNumber),
		"link":              fn(FgSyntaxNumber),
		"type":              fn(FgSyntaxType),
		"built_in":          fn(FgSyntaxType),
		"class":             fn(FgSyntaxType),
		"title.class":       fn(FgSyntaxType),
		"operator":          fn(FgSyntaxOperator),
		"symbol":            fn(FgSyntaxOperator),
		"punctuation":       fn(FgSyntaxPunctuation),
		"meta":              fn(FgMuted),
		"doctag":            fn(FgMuted),
		"addition":          fn(FgToolDiffAdded),
		"deletion":          fn(FgToolDiffRemoved),
	}
}

var themeAnsiSgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func themeSplitHighlightedLines(highlighted string) []string {
	lines := strings.Split(highlighted, "\n")
	activeFg := ""
	out := make([]string, len(lines))

	for i, line := range lines {
		if line == "" {
			// Keep empty lines empty rather than wrapping them in open+reset ANSI (#10143).
			out[i] = ""
			continue
		}
		var sb strings.Builder
		if activeFg != "" {
			sb.WriteString(activeFg)
		}
		sb.WriteString(line)

		// Track active ANSI state across matches in original line text.
		matches := themeAnsiSgrRe.FindAllString(line, -1)
		for _, seq := range matches {
			if seq == "\x1b[39m" || seq == "\x1b[0m" || seq == "\x1b[m" {
				activeFg = ""
			} else {
				activeFg = seq
			}
		}

		if activeFg != "" {
			sb.WriteString("\x1b[39m")
		}
		out[i] = sb.String()
	}
	return out
}

// HighlightCode syntax-highlights code using the active theme and splits it into per-line
// ANSI strings where each non-empty line is independently colored and closed.
func HighlightCode(code string, lang *string) []string {
	t := CurrentTheme()
	language := ""
	if lang != nil {
		language = *lang
	}
	if language != "" && !utils.SupportsLanguage(language) {
		language = ""
	}
	if language == "" {
		lines := strings.Split(code, "\n")
		for i, line := range lines {
			lines[i] = t.Fg(FgMdCodeBlock, line)
		}
		return lines
	}
	highlighted := utils.Highlight(code, utils.HighlightOptions{
		Language:       language,
		IgnoreIllegals: true,
		Theme:          BuildCliHighlightTheme(t),
	})
	return themeSplitHighlightedLines(highlighted)
}

// CodeHighlighter caches syntax-highlighted line slices across repeated renders until code,
// language, theme, or grammar generation changes.
type CodeHighlighter struct {
	mu          sync.Mutex
	lastCode    string
	lastLang    string
	hasLang     bool
	lastGen     int64
	lastLangNum int
	lastRes     []string
}

// NewCodeHighlighter returns a CodeHighlighter with an empty cache.
func NewCodeHighlighter() *CodeHighlighter {
	return &CodeHighlighter{}
}

// Highlight returns cached highlighted lines if inputs and theme generation match.
func (h *CodeHighlighter) Highlight(code string, lang *string) []string {
	gen := themeChangeGen.Load()
	langCount := utils.GetHighlightLanguageCount()
	langVal := ""
	hasLang := lang != nil
	if lang != nil {
		langVal = *lang
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastRes != nil && h.lastCode == code && h.hasLang == hasLang && h.lastLang == langVal && h.lastGen == gen && h.lastLangNum == langCount {
		return h.lastRes
	}
	res := HighlightCode(code, lang)
	h.lastCode = code
	h.lastLang = langVal
	h.hasLang = hasLang
	h.lastGen = gen
	h.lastLangNum = langCount
	h.lastRes = res
	return res
}

// GetMarkdownTheme returns a tui.MarkdownTheme wired to the active theme and HighlightCode.
func GetMarkdownTheme() tui.MarkdownTheme {
	t := CurrentTheme()
	return tui.MarkdownTheme{
		Heading:         func(s string) string { return t.Fg(FgMdHeading, s) },
		Link:            func(s string) string { return t.Fg(FgMdLink, s) },
		LinkUrl:         func(s string) string { return t.Fg(FgMdLinkUrl, s) },
		Code:            func(s string) string { return t.Fg(FgMdCode, s) },
		CodeBlock:       func(s string) string { return t.Fg(FgMdCodeBlock, s) },
		CodeBlockBorder: func(s string) string { return t.Fg(FgMdCodeBlockBorder, s) },
		Quote:           func(s string) string { return t.Fg(FgMdQuote, s) },
		QuoteBorder:     func(s string) string { return t.Fg(FgMdQuoteBorder, s) },
		Hr:              func(s string) string { return t.Fg(FgMdHr, s) },
		ListBullet:      func(s string) string { return t.Fg(FgMdListBullet, s) },
		Bold:            t.Bold,
		Italic:          t.Italic,
		Strikethrough:   t.Strikethrough,
		Underline:       t.Underline,
		HighlightCode:   HighlightCode,
	}
}

// GetSelectListTheme returns a tui.SelectListTheme styled with the active theme.
func GetSelectListTheme() tui.SelectListTheme {
	t := CurrentTheme()
	return tui.SelectListTheme{
		SelectedPrefix: func(s string) string { return t.Fg(FgAccent, s) },
		SelectedText:   func(s string) string { return t.Fg(FgAccent, s) },
		Description:    func(s string) string { return t.Fg(FgMuted, s) },
		ScrollInfo:     func(s string) string { return t.Fg(FgMuted, s) },
		NoMatch:        func(s string) string { return t.Fg(FgMuted, s) },
	}
}

// GetEditorTheme returns a tui.EditorTheme styled with the active theme.
func GetEditorTheme() tui.EditorTheme {
	t := CurrentTheme()
	return tui.EditorTheme{
		BorderColor: func(s string) string { return t.Fg(FgBorderMuted, s) },
		SelectList:  GetSelectListTheme(),
	}
}

// GetSettingsListTheme returns a tui.SettingsListTheme styled with the active theme.
func GetSettingsListTheme() tui.SettingsListTheme {
	t := CurrentTheme()
	return tui.SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return t.Fg(FgAccent, text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return t.Fg(FgAccent, text)
			}
			return t.Fg(FgMuted, text)
		},
		Description: func(s string) string { return t.Fg(FgDim, s) },
		Cursor:      t.Fg(FgAccent, "→ "),
		Hint:        func(s string) string { return t.Fg(FgDim, s) },
	}
}
