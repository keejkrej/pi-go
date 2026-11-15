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

	// Waku surface & chrome hierarchy tokens
	SidebarBg     ui.Color
	SidebarItemBg ui.Color
	SidebarBorder ui.Color
	Raised        ui.Color
	ComposerBg    ui.Color
	Inset         ui.Color
	Overlay       ui.Color
	OverlayStrong ui.Color

	// Borders
	Border       ui.Color
	BorderAccent ui.Color
	BorderMuted  ui.Color
	BorderStrong ui.Color

	// Foreground and interactive accents
	Accent        ui.Color
	AccentHover   ui.Color
	AccentPressed ui.Color
	AccentText    ui.Color
	Text          ui.Color
	TextSecondary ui.Color
	TextTertiary  ui.Color
	TextGhost     ui.Color
	Muted         ui.Color
	Dim           ui.Color
	SelectedBg    ui.Color
	SelectedText  ui.Color
	FocusRing     ui.Color
	Selection     ui.Color
	Inverse       ui.Color
	OnInverse     ui.Color
	Gauge         ui.Color
	ResizeHandle  ui.Color
	CodeText      ui.Color
	CodeWash      ui.Color

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

// DarkGuiTheme returns the default Waku-aligned dark theme.
func DarkGuiTheme() *GuiTheme {
	bg := ui.Hex("#1A1A1A")
	surface := ui.Hex("#1A1A1A")
	sidebarBg := ui.Hex("#181818")
	raised := ui.Hex("#232323")
	composerBg := ui.Hex("#212121")
	inset := ui.Hex("#151515")

	text := ui.Hex("#E2E2E2")
	textSecondary := ui.Hex("#A3A3A3")
	textTertiary := ui.Hex("#7D7D7D")
	textGhost := ui.Hex("#575757")

	accent := ui.Hex("#E2795B")
	gauge := ui.Hex("#3B82F6")
	codeText := ui.Hex("#E0A882")
	codeWash := ui.RGBA(227, 229, 232, 0.08)

	green := ui.Hex("#62C987")
	yellow := ui.Hex("#E0B36A")
	red := ui.Hex("#E2726A")

	border := ui.RGBA(227, 229, 232, 0.07)
	borderStrong := ui.RGBA(227, 229, 232, 0.14)
	overlay := ui.RGBA(227, 229, 232, 0.05)
	overlayStrong := ui.RGBA(227, 229, 232, 0.09)

	return &GuiTheme{
		Name: "dark",
		Dark: true,

		Background:      bg,
		Surface:         surface,
		SurfaceElevated: raised,
		SurfaceHover:    overlay,
		SurfacePressed:  overlayStrong,
		OverlayBackdrop: ui.RGBA(0, 0, 0, 0.48),

		SidebarBg:     sidebarBg,
		SidebarItemBg: ui.RGBA(240, 240, 240, 0.06),
		SidebarBorder: ui.Hex("#292929"),
		Raised:        raised,
		ComposerBg:    composerBg,
		Inset:         inset,
		Overlay:       overlay,
		OverlayStrong: overlayStrong,

		Border:       border,
		BorderAccent: accent,
		BorderMuted:  ui.RGBA(227, 229, 232, 0.05),
		BorderStrong: borderStrong,

		Accent:        accent,
		AccentHover:   accent.MixOklab(text, 0.12),
		AccentPressed: accent.MixOklab(bg, 0.15),
		AccentText:    ui.RGB(255, 255, 255),
		Text:          text,
		TextSecondary: textSecondary,
		TextTertiary:  textTertiary,
		TextGhost:     textGhost,
		Muted:         textSecondary,
		Dim:           textTertiary,
		SelectedBg:    ui.RGBA(240, 240, 240, 0.08),
		SelectedText:  text,
		FocusRing:     gauge.Alpha(0.75),
		Selection:     gauge.Alpha(0.28),
		Inverse:       ui.Hex("#E7E9EC"),
		OnInverse:     ui.Hex("#17181C"),
		Gauge:         gauge,
		ResizeHandle:  gauge,
		CodeText:      codeText,
		CodeWash:      codeWash,

		Success: green,
		Warning: yellow,
		Error:   red,

		ScrollbarTrack:  ui.Transparent,
		ScrollbarThumb:  ui.RGBA(227, 229, 232, 0.18),
		SearchMatchBg:   ui.RGBA(224, 179, 106, 0.25),
		SearchMatchText: text,

		UserMessageBg:      raised,
		UserMessageText:    text,
		CustomMessageBg:    raised,
		CustomMessageText:  textSecondary,
		CustomMessageLabel: accent,

		ToolPendingBg: ui.RGBA(227, 229, 232, 0.03),
		ToolSuccessBg: ui.RGBA(227, 229, 232, 0.025),
		ToolErrorBg:   ui.RGBA(226, 114, 106, 0.08),
		ToolTitle:     text,
		ToolOutput:    textSecondary,

		BashMode:     accent,
		ThinkingText: textSecondary,

		ThinkingOff:     textGhost,
		ThinkingMinimal: textTertiary,
		ThinkingLow:     ui.Hex("#60A5FA"),
		ThinkingMedium:  ui.Hex("#A78BFA"),
		ThinkingHigh:    accent,
		ThinkingXHigh:   yellow,
		ThinkingMax:     red,

		MdHeading:         text,
		MdLink:            ui.Hex("#60A5FA"),
		MdLinkUrl:         textTertiary,
		MdCode:            codeText,
		MdCodeBg:          codeWash,
		MdCodeBlockBg:     inset,
		MdCodeBlockBorder: borderStrong,
		MdQuote:           textSecondary,
		MdQuoteBorder:     borderStrong,
		MdHr:              border,
		MdListBullet:      textTertiary,

		DiffAdded:     green,
		DiffRemoved:   red,
		DiffModified:  gauge,
		DiffContext:   textSecondary,
		DiffAddedBg:   ui.RGBA(98, 201, 135, 0.13),
		DiffRemovedBg: ui.RGBA(226, 114, 106, 0.13),

		SyntaxComment:     textTertiary,
		SyntaxKeyword:     ui.Hex("#E2795B"),
		SyntaxFunction:    ui.Hex("#E0B36A"),
		SyntaxVariable:    ui.Hex("#93C5FD"),
		SyntaxString:      ui.Hex("#A3E635"),
		SyntaxNumber:      green,
		SyntaxType:        ui.Hex("#C4B5FD"),
		SyntaxOperator:    textSecondary,
		SyntaxPunctuation: textSecondary,
		SyntaxConstant:    green,
		SyntaxProperty:    ui.Hex("#93C5FD"),
		SyntaxTag:         accent,
		SyntaxAttribute:   yellow,

		FontSize:     13.5,
		MonoFontSize: 12.5,
		Radius:       8,
		Spacing:      4,
	}
}

// LightGuiTheme returns the default Waku-aligned light theme.
func LightGuiTheme() *GuiTheme {
	bg := ui.Hex("#F6F5F6")
	surface := ui.Hex("#F6F5F6")
	sidebarBg := ui.Hex("#F3F3F3")
	raised := ui.Hex("#ECECEC")
	composerBg := ui.Hex("#FFFFFF")
	inset := ui.Hex("#EFEFEF")
	codeBlockBg := ui.Hex("#FFFFFF")

	text := ui.Hex("#242424")
	textSecondary := ui.Hex("#666666")
	textTertiary := ui.Hex("#858585")
	textGhost := ui.Hex("#A4A4A4")

	accent := ui.Hex("#C85F44")
	gauge := ui.Hex("#2563EB")
	codeText := ui.Hex("#9A5528")
	codeWash := ui.RGBA(28, 30, 34, 0.07)

	green := ui.Hex("#2F8F52")
	yellow := ui.Hex("#A66B20")
	red := ui.Hex("#C64A42")

	border := ui.RGBA(28, 30, 34, 0.08)
	borderStrong := ui.RGBA(28, 30, 34, 0.15)
	overlay := ui.RGBA(28, 30, 34, 0.04)
	overlayStrong := ui.RGBA(28, 30, 34, 0.08)

	return &GuiTheme{
		Name: "light",
		Dark: false,

		Background:      bg,
		Surface:         surface,
		SurfaceElevated: composerBg,
		SurfaceHover:    overlay,
		SurfacePressed:  overlayStrong,
		OverlayBackdrop: ui.RGBA(18, 20, 28, 0.28),

		SidebarBg:     sidebarBg,
		SidebarItemBg: ui.RGBA(20, 20, 20, 0.06),
		SidebarBorder: ui.RGBA(20, 20, 20, 0.12),
		Raised:        raised,
		ComposerBg:    composerBg,
		Inset:         inset,
		Overlay:       overlay,
		OverlayStrong: overlayStrong,

		Border:       border,
		BorderAccent: accent,
		BorderMuted:  ui.RGBA(28, 30, 34, 0.05),
		BorderStrong: borderStrong,

		Accent:        accent,
		AccentHover:   accent.MixOklab(text, 0.10),
		AccentPressed: accent.MixOklab(text, 0.20),
		AccentText:    ui.RGB(255, 255, 255),
		Text:          text,
		TextSecondary: textSecondary,
		TextTertiary:  textTertiary,
		TextGhost:     textGhost,
		Muted:         textSecondary,
		Dim:           textTertiary,
		SelectedBg:    ui.RGBA(20, 20, 20, 0.07),
		SelectedText:  text,
		FocusRing:     gauge.Alpha(0.70),
		Selection:     gauge.Alpha(0.18),
		Inverse:       ui.Hex("#202227"),
		OnInverse:     ui.Hex("#F8F8F9"),
		Gauge:         gauge,
		ResizeHandle:  gauge,
		CodeText:      codeText,
		CodeWash:      codeWash,

		Success: green,
		Warning: yellow,
		Error:   red,

		ScrollbarTrack:  ui.Transparent,
		ScrollbarThumb:  ui.RGBA(28, 30, 34, 0.20),
		SearchMatchBg:   ui.RGBA(166, 107, 32, 0.20),
		SearchMatchText: text,

		UserMessageBg:      raised,
		UserMessageText:    text,
		CustomMessageBg:    composerBg,
		CustomMessageText:  textSecondary,
		CustomMessageLabel: accent,

		ToolPendingBg: ui.RGBA(28, 30, 34, 0.03),
		ToolSuccessBg: composerBg,
		ToolErrorBg:   ui.RGBA(198, 74, 66, 0.08),
		ToolTitle:     text,
		ToolOutput:    textSecondary,

		BashMode:     accent,
		ThinkingText: textSecondary,

		ThinkingOff:     textGhost,
		ThinkingMinimal: textTertiary,
		ThinkingLow:     gauge,
		ThinkingMedium:  ui.Hex("#7C3AED"),
		ThinkingHigh:    accent,
		ThinkingXHigh:   yellow,
		ThinkingMax:     red,

		MdHeading:         text,
		MdLink:            gauge,
		MdLinkUrl:         textTertiary,
		MdCode:            codeText,
		MdCodeBg:          codeWash,
		MdCodeBlockBg:     codeBlockBg,
		MdCodeBlockBorder: borderStrong,
		MdQuote:           textSecondary,
		MdQuoteBorder:     borderStrong,
		MdHr:              border,
		MdListBullet:      textTertiary,

		DiffAdded:     green,
		DiffRemoved:   red,
		DiffModified:  gauge,
		DiffContext:   textSecondary,
		DiffAddedBg:   ui.RGBA(47, 143, 82, 0.12),
		DiffRemovedBg: ui.RGBA(198, 74, 66, 0.12),

		SyntaxComment:     textTertiary,
		SyntaxKeyword:     accent,
		SyntaxFunction:    ui.Hex("#9A5528"),
		SyntaxVariable:    gauge,
		SyntaxString:      green,
		SyntaxNumber:      ui.Hex("#0F766E"),
		SyntaxType:        ui.Hex("#7C3AED"),
		SyntaxOperator:    textSecondary,
		SyntaxPunctuation: textSecondary,
		SyntaxConstant:    green,
		SyntaxProperty:    gauge,
		SyntaxTag:         accent,
		SyntaxAttribute:   yellow,

		FontSize:     13.5,
		MonoFontSize: 12.5,
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
	cp.FontSize = 14
	if t.Font != "" {
		cp.Font = t.Font
	}
	if t.MonoFont != "" {
		cp.CodeFont = t.MonoFont
	}
	cp.LineHeight = 1.58
	cp.CodeLineHeight = 1.45
	cp.Text = t.Text
	cp.Muted = t.Muted
	cp.Link = t.MdLink
	cp.Strong = t.Text
	cp.Code = t.MdCode
	if t.CodeText.A > 0 && t.MdCode == t.CodeText {
		cp.Code = t.CodeText
	}
	cp.CodeBackground = t.MdCodeBg
	if t.CodeWash.A > 0 && t.MdCodeBg == t.CodeWash {
		cp.CodeBackground = t.CodeWash
	}
	cp.CodeSize = 0.875
	cp.QuoteBar = t.MdQuoteBorder
	cp.QuoteText = t.MdQuote
	cp.Rule = t.MdHr
	cp.CodeBlockBackground = t.MdCodeBlockBg
	cp.CodeBlockBorder = t.MdCodeBlockBorder
	cp.CodeBlockRadius = 10
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
