package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

// GuiTheme defines the full color, surface, semantic, markdown, and code
// syntax token set for pi's native GUI layer. It mirrors Pi's semantic tokens
// (dark.json / light.json) and maps cleanly to mygo ui.Theme, markdown.Theme,
// and editor.Theme.
type GuiTheme struct {
	Name string
	Dark bool

	// Surfaces and window chrome
	Background      ui.Color
	Surface         ui.Color
	SurfaceElevated ui.Color
	SurfaceHover    ui.Color
	SurfacePressed  ui.Color
	OverlayBackdrop ui.Color

	// Borders
	Border       ui.Color
	BorderAccent ui.Color
	BorderMuted  ui.Color

	// Foreground and interactive accents
	Accent        ui.Color
	AccentHover   ui.Color
	AccentPressed ui.Color
	AccentText    ui.Color
	Text          ui.Color
	Muted         ui.Color
	Dim           ui.Color
	SelectedBg    ui.Color
	SelectedText  ui.Color
	FocusRing     ui.Color
	Selection     ui.Color

	// Status colors
	Success ui.Color
	Warning ui.Color
	Error   ui.Color

	// Scrollbar & search highlights
	ScrollbarTrack  ui.Color
	ScrollbarThumb  ui.Color
	SearchMatchBg   ui.Color
	SearchMatchText ui.Color

	// Message bubbles & cards
	UserMessageBg      ui.Color
	UserMessageText    ui.Color
	CustomMessageBg    ui.Color
	CustomMessageText  ui.Color
	CustomMessageLabel ui.Color

	ToolPendingBg ui.Color
	ToolSuccessBg ui.Color
	ToolErrorBg   ui.Color
	ToolTitle     ui.Color
	ToolOutput    ui.Color

	BashMode     ui.Color
	ThinkingText ui.Color

	// Thinking level colors
	ThinkingOff     ui.Color
	ThinkingMinimal ui.Color
	ThinkingLow     ui.Color
	ThinkingMedium  ui.Color
	ThinkingHigh    ui.Color
	ThinkingXHigh   ui.Color
	ThinkingMax     ui.Color

	// Markdown tokens
	MdHeading         ui.Color
	MdLink            ui.Color
	MdLinkUrl         ui.Color
	MdCode            ui.Color
	MdCodeBg          ui.Color
	MdCodeBlockBg     ui.Color
	MdCodeBlockBorder ui.Color
	MdQuote           ui.Color
	MdQuoteBorder     ui.Color
	MdHr              ui.Color
	MdListBullet      ui.Color

	// Diff tokens
	DiffAdded     ui.Color
	DiffRemoved   ui.Color
	DiffModified  ui.Color
	DiffContext   ui.Color
	DiffAddedBg   ui.Color
	DiffRemovedBg ui.Color

	// Code syntax tokens
	SyntaxComment     ui.Color
	SyntaxKeyword     ui.Color
	SyntaxFunction    ui.Color
	SyntaxVariable    ui.Color
	SyntaxString      ui.Color
	SyntaxNumber      ui.Color
	SyntaxType        ui.Color
	SyntaxOperator    ui.Color
	SyntaxPunctuation ui.Color
	SyntaxConstant    ui.Color
	SyntaxProperty    ui.Color
	SyntaxTag         ui.Color
	SyntaxAttribute   ui.Color

	// Typography & spacing metrics
	Font         string
	MonoFont     string
	FontSize     float32
	MonoFontSize float32
	Radius       float32
	Spacing      float32
}

func mustOKHSL(spec string) ui.Color {
	c, ok := ParseOKHSL(spec)
	if !ok {
		panic("gui: invalid builtin okhsl color " + spec)
	}
	return c
}

// DarkGuiTheme returns the default dark theme matching pi's dark.json tokens
// adapted for native desktop surfaces.
func DarkGuiTheme() *GuiTheme {
	text := mustOKHSL("okhsl(234 3% 89%)")
	muted := mustOKHSL("okhsl(229 6% 67%)")
	violet := mustOKHSL("okhsl(295 50% 67%)")
	blue := mustOKHSL("okhsl(232 54% 67%)")
	green := mustOKHSL("okhsl(159 59% 67%)")
	red := mustOKHSL("okhsl(20 72% 67%)")
	yellow := mustOKHSL("okhsl(83 88% 67%)")
	blueBg := mustOKHSL("okhsl(233 41% 24%)")

	bg := mustOKHSL("okhsl(262 14% 14%)")
	surface := mustOKHSL("okhsl(262 14% 17%)")
	elevated := mustOKHSL("okhsl(264 13% 21%)")

	return &GuiTheme{
		Name: "dark",
		Dark: true,

		Background:      bg,
		Surface:         surface,
		SurfaceElevated: elevated,
		SurfaceHover:    surface.MixOklab(text, 0.08),
		SurfacePressed:  surface.MixOklab(text, 0.14),
		OverlayBackdrop: ui.RGBA(0, 0, 0, 0.55),

		Border:       mustOKHSL("okhsl(231 57% 65%)").Alpha(0.35),
		BorderAccent: mustOKHSL("okhsl(295 53% 64%)"),
		BorderMuted:  mustOKHSL("okhsl(229 8% 53%)").Alpha(0.45),

		Accent:        violet,
		AccentHover:   violet.MixOklab(text, 0.12),
		AccentPressed: violet.MixOklab(bg, 0.15),
		AccentText:    ui.RGB(18, 16, 24),
		Text:          text,
		Muted:         muted,
		Dim:           mustOKHSL("okhsl(229 8% 56%)"),
		SelectedBg:    blueBg,
		SelectedText:  text,
		FocusRing:     violet,
		Selection:     blueBg.MixOklab(violet, 0.25).Alpha(0.75),

		Success: green,
		Warning: yellow,
		Error:   red,

		ScrollbarTrack:  mustOKHSL("okhsl(237 7% 33%)"),
		ScrollbarThumb:  mustOKHSL("okhsl(232 7% 65%)"),
		SearchMatchBg:   mustOKHSL("okhsl(53 51% 24%)"),
		SearchMatchText: muted,

		UserMessageBg:      blueBg,
		UserMessageText:    text,
		CustomMessageBg:    mustOKHSL("okhsl(295 42% 24%)"),
		CustomMessageText:  muted,
		CustomMessageLabel: violet,

		ToolPendingBg: mustOKHSL("okhsl(229 5% 24%)"),
		ToolSuccessBg: mustOKHSL("okhsl(158 46% 25%)"),
		ToolErrorBg:   mustOKHSL("okhsl(19 54% 25%)"),
		ToolTitle:     text,
		ToolOutput:    muted,

		BashMode:     mustOKHSL("okhsl(159 64% 65%)"),
		ThinkingText: mustOKHSL("okhsl(226 7% 65%)"),

		ThinkingOff:     mustOKHSL("okhsl(229 8% 49%)"),
		ThinkingMinimal: mustOKHSL("okhsl(232 20% 52%)"),
		ThinkingLow:     mustOKHSL("okhsl(232 45% 54%)"),
		ThinkingMedium:  mustOKHSL("okhsl(263 59% 56%)"),
		ThinkingHigh:    mustOKHSL("okhsl(295 73% 59%)"),
		ThinkingXHigh:   mustOKHSL("okhsl(337 81% 61%)"),
		ThinkingMax:     mustOKHSL("okhsl(20 99% 63%)"),

		MdHeading:         yellow,
		MdLink:            blue,
		MdLinkUrl:         muted,
		MdCode:            violet,
		MdCodeBg:          elevated,
		MdCodeBlockBg:     mustOKHSL("okhsl(262 14% 12%)"),
		MdCodeBlockBorder: mustOKHSL("okhsl(229 8% 53%)").Alpha(0.4),
		MdQuote:           muted,
		MdQuoteBorder:     muted,
		MdHr:              muted,
		MdListBullet:      violet,

		DiffAdded:     green,
		DiffRemoved:   red,
		DiffModified:  blue,
		DiffContext:   muted,
		DiffAddedBg:   mustOKHSL("okhsl(158 46% 22%)"),
		DiffRemovedBg: mustOKHSL("okhsl(19 54% 22%)"),

		SyntaxComment:     muted,
		SyntaxKeyword:     blue,
		SyntaxFunction:    yellow,
		SyntaxVariable:    mustOKHSL("okhsl(202 58% 67%)"),
		SyntaxString:      mustOKHSL("okhsl(52 67% 67%)"),
		SyntaxNumber:      green,
		SyntaxType:        violet,
		SyntaxOperator:    muted,
		SyntaxPunctuation: muted,
		SyntaxConstant:    green,
		SyntaxProperty:    mustOKHSL("okhsl(202 58% 67%)"),
		SyntaxTag:         violet,
		SyntaxAttribute:   yellow,

		FontSize:     14,
		MonoFontSize: 13,
		Radius:       8,
		Spacing:      4,
	}
}

// LightGuiTheme returns the default light theme matching pi's light.json tokens
// adapted for native desktop surfaces.
func LightGuiTheme() *GuiTheme {
	text := mustOKHSL("okhsl(225 5% 27%)")
	muted := mustOKHSL("okhsl(229 8% 47%)")
	violet := mustOKHSL("okhsl(295 60% 46%)")
	blue := mustOKHSL("okhsl(231 68% 47%)")
	green := mustOKHSL("okhsl(159 75% 46%)")
	red := mustOKHSL("okhsl(20 91% 47%)")
	yellow := mustOKHSL("okhsl(83 99% 47%)")
	blueBg := mustOKHSL("okhsl(235 19% 91%)")

	bg := mustOKHSL("okhsl(17 3% 96%)")
	surface := mustOKHSL("okhsl(17 5% 98%)")
	elevated := mustOKHSL("okhsl(17 5% 100%)")

	return &GuiTheme{
		Name: "light",
		Dark: false,

		Background:      bg,
		Surface:         surface,
		SurfaceElevated: elevated,
		SurfaceHover:    surface.MixOklab(text, 0.05),
		SurfacePressed:  surface.MixOklab(text, 0.10),
		OverlayBackdrop: ui.RGBA(18, 20, 28, 0.35),

		Border:       mustOKHSL("okhsl(235 7% 66%)").Alpha(0.55),
		BorderAccent: mustOKHSL("okhsl(295 59% 55%)"),
		BorderMuted:  mustOKHSL("okhsl(235 7% 66%)").Alpha(0.35),

		Accent:        violet,
		AccentHover:   violet.MixOklab(text, 0.10),
		AccentPressed: violet.MixOklab(text, 0.20),
		AccentText:    ui.RGB(255, 255, 255),
		Text:          text,
		Muted:         muted,
		Dim:           mustOKHSL("okhsl(229 7% 59%)"),
		SelectedBg:    blueBg,
		SelectedText:  text,
		FocusRing:     violet,
		Selection:     blueBg.MixOklab(violet, 0.2).Alpha(0.85),

		Success: green,
		Warning: yellow,
		Error:   red,

		ScrollbarTrack:  mustOKHSL("okhsl(248 3% 90%)"),
		ScrollbarThumb:  mustOKHSL("okhsl(226 7% 65%)"),
		SearchMatchBg:   mustOKHSL("okhsl(56 22% 91%)"),
		SearchMatchText: muted,

		UserMessageBg:      blueBg,
		UserMessageText:    text,
		CustomMessageBg:    mustOKHSL("okhsl(295 25% 91%)"),
		CustomMessageText:  muted,
		CustomMessageLabel: violet,

		ToolPendingBg: mustOKHSL("okhsl(248 3% 91%)"),
		ToolSuccessBg: mustOKHSL("okhsl(156 21% 91%)"),
		ToolErrorBg:   mustOKHSL("okhsl(24 23% 91%)"),
		ToolTitle:     text,
		ToolOutput:    muted,

		BashMode:     mustOKHSL("okhsl(159 74% 40%)"),
		ThinkingText: mustOKHSL("okhsl(234 8% 55%)"),

		ThinkingOff:     mustOKHSL("okhsl(223 5% 80%)"),
		ThinkingMinimal: mustOKHSL("okhsl(229 14% 78%)"),
		ThinkingLow:     mustOKHSL("okhsl(232 33% 76%)"),
		ThinkingMedium:  mustOKHSL("okhsl(264 48% 74%)"),
		ThinkingHigh:    mustOKHSL("okhsl(295 62% 72%)"),
		ThinkingXHigh:   mustOKHSL("okhsl(337 74% 70%)"),
		ThinkingMax:     mustOKHSL("okhsl(20 98% 68%)"),

		MdHeading:         blue,
		MdLink:            blue,
		MdLinkUrl:         muted,
		MdCode:            violet,
		MdCodeBg:          mustOKHSL("okhsl(248 3% 93%)"),
		MdCodeBlockBg:     mustOKHSL("okhsl(248 3% 94%)"),
		MdCodeBlockBorder: mustOKHSL("okhsl(235 7% 66%)").Alpha(0.5),
		MdQuote:           muted,
		MdQuoteBorder:     muted,
		MdHr:              muted,
		MdListBullet:      violet,

		DiffAdded:     green,
		DiffRemoved:   red,
		DiffModified:  blue,
		DiffContext:   muted,
		DiffAddedBg:   mustOKHSL("okhsl(156 21% 91%)"),
		DiffRemovedBg: mustOKHSL("okhsl(24 23% 91%)"),

		SyntaxComment:     muted,
		SyntaxKeyword:     blue,
		SyntaxFunction:    mustOKHSL("okhsl(42 85% 36%)"),
		SyntaxVariable:    mustOKHSL("okhsl(203 73% 42%)"),
		SyntaxString:      mustOKHSL("okhsl(52 84% 36%)"),
		SyntaxNumber:      green,
		SyntaxType:        violet,
		SyntaxOperator:    muted,
		SyntaxPunctuation: muted,
		SyntaxConstant:    green,
		SyntaxProperty:    mustOKHSL("okhsl(203 73% 42%)"),
		SyntaxTag:         violet,
		SyntaxAttribute:   mustOKHSL("okhsl(42 85% 36%)"),

		FontSize:     14,
		MonoFontSize: 13,
		Radius:       8,
		Spacing:      4,
	}
}

// ThemeForAppearance returns DarkGuiTheme when dark is true, else LightGuiTheme.
func ThemeForAppearance(dark bool) *GuiTheme {
	if dark {
		return DarkGuiTheme()
	}
	return LightGuiTheme()
}

// ResolveGuiTheme returns t if non-nil, otherwise DarkGuiTheme/LightGuiTheme
// matching the current ui.Context theme appearance.
func ResolveGuiTheme(c *ui.Context, t *GuiTheme) *GuiTheme {
	if t != nil {
		return t
	}
	dark := true
	if c != nil && c.Theme() != nil {
		dark = c.Theme().Dark
	}
	return ThemeForAppearance(dark)
}

// Clone returns a shallow copy of t so callers can safely customize tokens.
func (t *GuiTheme) Clone() *GuiTheme {
	if t == nil {
		return DarkGuiTheme()
	}
	cp := *t
	return &cp
}

// ThinkingColor returns the color associated with a thinking budget level
// ("off", "minimal", "low", "medium", "high", "xhigh", "max").
func (t *GuiTheme) ThinkingColor(level string) ui.Color {
	switch level {
	case "off":
		return t.ThinkingOff
	case "minimal":
		return t.ThinkingMinimal
	case "low":
		return t.ThinkingLow
	case "medium":
		return t.ThinkingMedium
	case "high":
		return t.ThinkingHigh
	case "xhigh":
		return t.ThinkingXHigh
	case "max":
		return t.ThinkingMax
	default:
		return t.ThinkingText
	}
}

// UITheme builds an independent *ui.Theme configured from this GuiTheme.
func (t *GuiTheme) UITheme() *ui.Theme {
	var base *ui.Theme
	if t.Dark {
		base = ui.DarkTheme()
	} else {
		base = ui.LightTheme()
	}
	cp := *base
	cp.Dark = t.Dark
	cp.Background = t.Background
	cp.Surface = t.Surface
	cp.SurfaceHover = t.SurfaceHover
	cp.SurfacePressed = t.SurfacePressed
	cp.Border = t.Border
	cp.Text = t.Text
	cp.TextMuted = t.Muted
	cp.Accent = t.Accent
	cp.AccentHover = t.AccentHover
	cp.AccentPressed = t.AccentPressed
	cp.AccentText = t.AccentText
	cp.InactiveAccent = t.Accent.Alpha(0.55)
	cp.Danger = t.Error
	cp.Warning = t.Warning
	cp.Success = t.Success
	cp.Selection = t.Selection
	cp.Focus = t.FocusRing
	if t.Radius > 0 {
		cp.Radius = t.Radius
	}
	if t.Spacing > 0 {
		cp.Spacing = t.Spacing
	}
	if t.FontSize > 0 {
		cp.FontSize = t.FontSize
	}
	if t.Font != "" {
		cp.Font = t.Font
	}
	if t.ScrollbarThumb.A > 0 {
		cp.Scrollbar = t.ScrollbarThumb
	}
	return &cp
}

// EditorTheme derives an *editor.Theme with syntax and diff colors from GuiTheme.
func (t *GuiTheme) EditorTheme() *editor.Theme {
	uiTheme := t.UITheme()
	def := editor.DefaultTheme(uiTheme)
	cp := *def
	if t.MdCodeBlockBg.A > 0 {
		cp.Background = t.MdCodeBlockBg
	} else {
		cp.Background = t.Surface
	}
	cp.Text = t.Text
	cp.LineNumber = t.Dim
	cp.ActiveLineNumber = t.Muted
	cp.ActiveLine = t.SurfaceHover
	cp.Selection = t.Selection
	cp.Cursor = t.Accent
	cp.Match = t.SearchMatchBg
	cp.CurrentMatch = t.Accent.Alpha(0.35)
	cp.Error = t.Error
	cp.Warning = t.Warning
	cp.Info = t.MdLink
	cp.Hint = t.Muted
	cp.Added = t.DiffAdded
	cp.Deleted = t.DiffRemoved
	cp.Modified = t.DiffModified

	cp.Tokens[editor.TokenText].Color = t.Text
	cp.Tokens[editor.TokenComment].Color = t.SyntaxComment
	cp.Tokens[editor.TokenKeyword].Color = t.SyntaxKeyword
	cp.Tokens[editor.TokenFunction].Color = t.SyntaxFunction
	cp.Tokens[editor.TokenVariable].Color = t.SyntaxVariable
	cp.Tokens[editor.TokenString].Color = t.SyntaxString
	cp.Tokens[editor.TokenNumber].Color = t.SyntaxNumber
	cp.Tokens[editor.TokenType].Color = t.SyntaxType
	cp.Tokens[editor.TokenOperator].Color = t.SyntaxOperator
	cp.Tokens[editor.TokenPunctuation].Color = t.SyntaxPunctuation
	cp.Tokens[editor.TokenConstant].Color = t.SyntaxConstant
	cp.Tokens[editor.TokenProperty].Color = t.SyntaxProperty
	cp.Tokens[editor.TokenTag].Color = t.SyntaxTag
	cp.Tokens[editor.TokenAttribute].Color = t.SyntaxAttribute
	cp.Tokens[editor.TokenHeading].Color = t.MdHeading
	cp.Tokens[editor.TokenLink].Color = t.MdLink
	cp.Tokens[editor.TokenCode].Color = t.MdCode
	cp.Tokens[editor.TokenInserted].Color = t.DiffAdded
	cp.Tokens[editor.TokenDeleted].Color = t.DiffRemoved
	return &cp
}

// MarkdownTheme derives a *markdown.Theme matching GuiTheme's typography and colors.
func (t *GuiTheme) MarkdownTheme() *markdown.Theme {
	uiTheme := t.UITheme()
	def := markdown.DefaultTheme(uiTheme)
	cp := *def
	if t.FontSize > 0 {
		cp.FontSize = t.FontSize
	}
	if t.Font != "" {
		cp.Font = t.Font
	}
	if t.MonoFont != "" {
		cp.CodeFont = t.MonoFont
	}
	cp.Text = t.Text
	cp.Muted = t.Muted
	cp.Link = t.MdLink
	cp.Strong = t.Text
	cp.Code = t.MdCode
	cp.CodeBackground = t.MdCodeBg
	cp.QuoteBar = t.MdQuoteBorder
	cp.QuoteText = t.MdQuote
	cp.Rule = t.MdHr
	cp.CodeBlockBackground = t.MdCodeBlockBg
	cp.CodeBlockBorder = t.MdCodeBlockBorder
	cp.CodeColors = t.EditorTheme()
	cp.Checkbox = t.Accent
	cp.TableBorder = t.Border
	cp.TableHeader = t.SurfaceElevated
	cp.TableStripe = t.SurfaceHover.Alpha(0.5)
	for i := range cp.Headings {
		if i == 0 {
			cp.Headings[i].Color = t.MdHeading
		}
	}
	return &cp
}
