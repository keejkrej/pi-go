package gui

import (
	"maps"
	"math"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/tui"
)

// ThemeSet bundles resolved Layer-L3 theme, Layer-L0 GuiTheme, and MyGo sub-themes
// so callers can apply consistent styling across widgets, markdown views, and editors.
type ThemeSet struct {
	Source   *theme.Theme
	Gui      *gui.GuiTheme
	UI       *ui.Theme
	Markdown *markdown.Theme
	Editor   *editor.Theme
}

var (
	taListenerMu     sync.Mutex
	taListeners      = make(map[uint64]func(*ThemeSet))
	taNextListenerId atomic.Uint64
	taWatcherBound   atomic.Bool
)

func taClampByte(v float64) uint8 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	r := math.Round(v)
	if r >= 255 {
		return 255
	}
	if r <= 0 {
		return 0
	}
	return uint8(r)
}

// TuiColorToUIColor converts a Layer-L0 tui.Color (indexed, truecolor RGB, or OKLCH)
// into a MyGo ui.Color. Nil colors return ui.Transparent.
func TuiColorToUIColor(col tui.Color) ui.Color {
	if col == nil {
		return ui.Transparent
	}
	rgb := tui.ColorToRgb(col)
	return ui.RGB(taClampByte(rgb.R), taClampByte(rgb.G), taClampByte(rgb.B))
}

func taSnapshotThemeColors(th *theme.Theme) (colors map[string]ui.Color, isLight bool, name string) {
	colors = make(map[string]ui.Color)
	if th == nil {
		th = theme.CurrentTheme()
		if th == nil {
			return colors, false, "dark"
		}
		isLight = theme.IsLightTheme()
		name = th.Name
		for tok, hex := range theme.GetResolvedThemeColors() {
			if c, ok := gui.ParseHexColor(hex); ok {
				colors[tok] = c
			}
		}
		return colors, isLight, name
	}

	if active := theme.CurrentTheme(); active == th {
		isLight = theme.IsLightTheme()
		name = th.Name
		for tok, hex := range theme.GetResolvedThemeColors() {
			if c, ok := gui.ParseHexColor(hex); ok {
				colors[tok] = c
			}
		}
		return colors, isLight, name
	}

	isLight = th.Appearance == tui.TerminalColorSchemeLight
	name = th.Name
	for tok, col := range th.Colors {
		if c := TuiColorToUIColor(col); c != ui.Transparent {
			colors[tok] = c
		}
	}
	return colors, isLight, name
}

// ThemeTokenToUIColor looks up a resolved foreground or background token on th,
// falling back to fallback if th is nil or does not define the token.
func ThemeTokenToUIColor(th *theme.Theme, token string, fallback ui.Color) ui.Color {
	if token == "" {
		return fallback
	}
	colors, _, _ := taSnapshotThemeColors(th)
	if col, ok := colors[token]; ok && col != ui.Transparent {
		return col
	}
	return fallback
}

func taApplyToken(colors map[string]ui.Color, tok string, dst *ui.Color) {
	if c, ok := colors[tok]; ok && c != ui.Transparent {
		*dst = c
	}
}

// BuildGuiTheme converts a codingagent/modes/interactive/theme.Theme (dark, light,
// system, or custom JSON) into a complete *gui.GuiTheme while preserving desktop surface
// hierarchy and contrast.
func BuildGuiTheme(th *theme.Theme) *gui.GuiTheme {
	colors, isLight, name := taSnapshotThemeColors(th)
	base := gui.ThemeForAppearance(isLight).Clone()
	if name != "" {
		base.Name = name
	}
	base.Dark = !isLight

	// Foreground semantic & chrome tokens
	taApplyToken(colors, theme.FgAccent, &base.Accent)
	taApplyToken(colors, theme.FgBorder, &base.Border)
	taApplyToken(colors, theme.FgBorderAccent, &base.BorderAccent)
	taApplyToken(colors, theme.FgBorderMuted, &base.BorderMuted)
	taApplyToken(colors, theme.FgSuccess, &base.Success)
	taApplyToken(colors, theme.FgError, &base.Error)
	taApplyToken(colors, theme.FgWarning, &base.Warning)
	taApplyToken(colors, theme.FgMuted, &base.Muted)
	taApplyToken(colors, theme.FgDim, &base.Dim)
	taApplyToken(colors, theme.FgText, &base.Text)
	taApplyToken(colors, theme.FgThinkingText, &base.ThinkingText)
	taApplyToken(colors, theme.FgScrollbarTrack, &base.ScrollbarTrack)
	taApplyToken(colors, theme.FgScrollbarThumb, &base.ScrollbarThumb)
	taApplyToken(colors, theme.FgSearchMatchText, &base.SearchMatchText)
	taApplyToken(colors, theme.FgUserMessageText, &base.UserMessageText)
	taApplyToken(colors, theme.FgCustomMessageText, &base.CustomMessageText)
	taApplyToken(colors, theme.FgCustomMessageLabel, &base.CustomMessageLabel)
	taApplyToken(colors, theme.FgToolTitle, &base.ToolTitle)
	taApplyToken(colors, theme.FgToolOutput, &base.ToolOutput)
	taApplyToken(colors, theme.FgBashMode, &base.BashMode)

	// Thinking level accents
	taApplyToken(colors, theme.FgThinkingOff, &base.ThinkingOff)
	taApplyToken(colors, theme.FgThinkingMinimal, &base.ThinkingMinimal)
	taApplyToken(colors, theme.FgThinkingLow, &base.ThinkingLow)
	taApplyToken(colors, theme.FgThinkingMedium, &base.ThinkingMedium)
	taApplyToken(colors, theme.FgThinkingHigh, &base.ThinkingHigh)
	taApplyToken(colors, theme.FgThinkingXhigh, &base.ThinkingXHigh)
	taApplyToken(colors, theme.FgThinkingMax, &base.ThinkingMax)

	// Markdown tokens
	taApplyToken(colors, theme.FgMdHeading, &base.MdHeading)
	taApplyToken(colors, theme.FgMdLink, &base.MdLink)
	taApplyToken(colors, theme.FgMdLinkUrl, &base.MdLinkUrl)
	taApplyToken(colors, theme.FgMdCode, &base.MdCode)
	taApplyToken(colors, theme.FgMdCodeBlock, &base.MdCodeBg)
	taApplyToken(colors, theme.FgMdCodeBlockBorder, &base.MdCodeBlockBorder)
	taApplyToken(colors, theme.FgMdQuote, &base.MdQuote)
	taApplyToken(colors, theme.FgMdQuoteBorder, &base.MdQuoteBorder)
	taApplyToken(colors, theme.FgMdHr, &base.MdHr)
	taApplyToken(colors, theme.FgMdListBullet, &base.MdListBullet)

	// Diff tokens
	taApplyToken(colors, theme.FgToolDiffAdded, &base.DiffAdded)
	taApplyToken(colors, theme.FgToolDiffRemoved, &base.DiffRemoved)
	taApplyToken(colors, theme.FgToolDiffContext, &base.DiffContext)

	// Syntax highlight tokens
	taApplyToken(colors, theme.FgSyntaxComment, &base.SyntaxComment)
	taApplyToken(colors, theme.FgSyntaxKeyword, &base.SyntaxKeyword)
	taApplyToken(colors, theme.FgSyntaxFunction, &base.SyntaxFunction)
	taApplyToken(colors, theme.FgSyntaxVariable, &base.SyntaxVariable)
	taApplyToken(colors, theme.FgSyntaxString, &base.SyntaxString)
	taApplyToken(colors, theme.FgSyntaxNumber, &base.SyntaxNumber)
	taApplyToken(colors, theme.FgSyntaxType, &base.SyntaxType)
	taApplyToken(colors, theme.FgSyntaxOperator, &base.SyntaxOperator)
	taApplyToken(colors, theme.FgSyntaxPunctuation, &base.SyntaxPunctuation)

	// Background tokens
	taApplyToken(colors, theme.BgSelected, &base.SelectedBg)
	taApplyToken(colors, theme.BgSearchMatch, &base.SearchMatchBg)
	taApplyToken(colors, theme.BgUserMessage, &base.UserMessageBg)
	taApplyToken(colors, theme.BgCustomMessage, &base.CustomMessageBg)
	taApplyToken(colors, theme.BgToolPending, &base.ToolPendingBg)
	taApplyToken(colors, theme.BgToolSuccess, &base.ToolSuccessBg)
	taApplyToken(colors, theme.BgToolError, &base.ToolErrorBg)

	// Derive interactive & surface tones harmonized with the custom theme's palette
	if c, ok := colors[theme.BgUserMessage]; ok && c != ui.Transparent {
		base.Surface = c
		if isLight {
			base.Background = gui.MixOklab(c, ui.RGB(255, 255, 255), 0.45)
			base.SurfaceElevated = gui.MixOklab(c, ui.RGB(255, 255, 255), 0.65)
			base.SurfaceHover = gui.MixOklab(c, ui.RGB(0, 0, 0), 0.05)
			base.SurfacePressed = gui.MixOklab(c, ui.RGB(0, 0, 0), 0.11)
		} else {
			base.Background = gui.MixOklab(c, ui.RGB(10, 10, 14), 0.55)
			base.SurfaceElevated = gui.MixOklab(c, ui.RGB(255, 255, 255), 0.06)
			base.SurfaceHover = gui.MixOklab(c, ui.RGB(255, 255, 255), 0.09)
			base.SurfacePressed = gui.MixOklab(c, ui.RGB(255, 255, 255), 0.14)
		}
	}

	if isLight {
		base.AccentHover = gui.MixOklab(base.Accent, ui.RGB(0, 0, 0), 0.12)
		base.AccentPressed = gui.MixOklab(base.Accent, ui.RGB(0, 0, 0), 0.22)
		base.DiffAddedBg = gui.WithAlpha(base.DiffAdded, 0.14)
		base.DiffRemovedBg = gui.WithAlpha(base.DiffRemoved, 0.14)
		base.Selection = gui.WithAlpha(base.Accent, 0.20)
	} else {
		base.AccentHover = gui.MixOklab(base.Accent, ui.RGB(255, 255, 255), 0.12)
		base.AccentPressed = gui.MixOklab(base.Accent, ui.RGB(255, 255, 255), 0.22)
		base.DiffAddedBg = gui.WithAlpha(base.DiffAdded, 0.16)
		base.DiffRemovedBg = gui.WithAlpha(base.DiffRemoved, 0.16)
		base.Selection = gui.WithAlpha(base.Accent, 0.26)
	}

	base.FocusRing = base.BorderAccent
	base.AccentText = gui.ContrastingText(base.Accent, ui.RGB(248, 250, 252), ui.RGB(16, 18, 22))
	base.SelectedText = gui.ContrastingText(base.SelectedBg, base.Text, ui.RGB(16, 18, 22))
	base.DiffModified = base.Accent
	base.SyntaxConstant = base.SyntaxNumber
	base.SyntaxProperty = base.SyntaxVariable
	base.SyntaxTag = base.SyntaxKeyword
	base.SyntaxAttribute = base.SyntaxType

	if c, ok := colors[theme.FgMdCodeBlock]; ok && c != ui.Transparent {
		base.MdCodeBg = gui.WithAlpha(c, 0.12)
		if isLight {
			base.MdCodeBlockBg = gui.MixOklab(base.Surface, ui.RGB(0, 0, 0), 0.03)
		} else {
			base.MdCodeBlockBg = gui.MixOklab(base.Background, base.Surface, 0.5)
		}
	}

	return base
}

// ResolveActiveGuiTheme returns a GuiTheme built from theme.CurrentTheme().
func ResolveActiveGuiTheme() *gui.GuiTheme {
	return BuildGuiTheme(theme.CurrentTheme())
}

// ThinkingLevelColor maps an agent.ThinkingLevel to its themed GUI color.
func ThinkingLevelColor(th *gui.GuiTheme, level agent.ThinkingLevel) ui.Color {
	if th == nil {
		th = gui.DarkGuiTheme()
	}
	return th.ThinkingColor(string(level))
}

// SyncUIThemes builds the full ThemeSet (GuiTheme, MyGo ui.Theme, markdown.Theme, editor.Theme)
// derived from th (or theme.CurrentTheme() when th is nil).
func SyncUIThemes(th *theme.Theme) ThemeSet {
	if th == nil {
		th = theme.CurrentTheme()
	}
	gt := BuildGuiTheme(th)
	return ThemeSet{
		Source:   th,
		Gui:      gt,
		UI:       gt.UITheme(),
		Markdown: gt.MarkdownTheme(),
		Editor:   gt.EditorTheme(),
	}
}

// ApplyThemeToHost switches codingagent's active theme by name (supporting built-ins,
// "system", "light/dark" auto specs, and custom JSON themes), pushes the converted
// GuiTheme to host when non-nil, and returns both the switch outcome and ThemeSet.
func ApplyThemeToHost(host *gui.GUI, name string) (theme.SetThemeResult, *ThemeSet) {
	res := theme.SetTheme(name, true)
	ts := SyncUIThemes(theme.CurrentTheme())
	if host != nil && ts.Gui != nil {
		host.SetTheme(ts.Gui)
		host.RequestRender()
	}
	return res, &ts
}

// ApplyThemeInstanceToHost installs an explicit *theme.Theme instance globally and on host.
func ApplyThemeInstanceToHost(host *gui.GUI, th *theme.Theme) *ThemeSet {
	if th != nil {
		theme.SetThemeInstance(th)
	}
	ts := SyncUIThemes(th)
	if host != nil && ts.Gui != nil {
		host.SetTheme(ts.Gui)
		host.RequestRender()
	}
	return &ts
}

// BindThemeChanges subscribes to live theme reloads/changes (including custom JSON
// file updates) and syncs host + optional onUpdate callback on each change.
// Call the returned cleanup function when disposing the GUI mode.
func BindThemeChanges(host *gui.GUI, onUpdate func(*ThemeSet)) func() {
	id := taNextListenerId.Add(1)
	taListenerMu.Lock()
	taListeners[id] = func(ts *ThemeSet) {
		if host != nil && ts != nil && ts.Gui != nil {
			host.SetTheme(ts.Gui)
			host.RequestRender()
		}
		if onUpdate != nil {
			onUpdate(ts)
		}
	}
	taListenerMu.Unlock()

	if taWatcherBound.CompareAndSwap(false, true) {
		theme.OnThemeChange(func() {
			ts := SyncUIThemes(theme.CurrentTheme())
			taListenerMu.Lock()
			snapshot := maps.Clone(taListeners)
			taListenerMu.Unlock()
			for _, fn := range snapshot {
				copySet := ts
				fn(&copySet)
			}
		})
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			taListenerMu.Lock()
			delete(taListeners, id)
			taListenerMu.Unlock()
		})
	}
}

// ResolveThemeForSetting resolves single names or "lightTheme/darkTheme" auto strings
// according to whether the host appearance is dark or light.
func ResolveThemeForSetting(setting string, isDark bool) string {
	scheme := tui.TerminalColorSchemeDark
	if !isDark {
		scheme = tui.TerminalColorSchemeLight
	}
	return theme.ResolveThemeSetting(setting, scheme)
}
