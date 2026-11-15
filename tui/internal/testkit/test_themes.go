// Ported from packages/tui/test/test-themes.ts (pi v1.5.2).

package testkit

import (
	"strings"

	"github.com/keejkrej/pi-go/tui"
)

func ttWrapStyle(open, close, text string) string {
	if text == "" {
		return ""
	}
	if strings.Contains(text, close) {
		text = strings.ReplaceAll(text, close, close+open)
	}
	return open + text + close
}

func ttBlue(text string) string          { return ttWrapStyle("\x1b[34m", "\x1b[39m", text) }
func ttCyan(text string) string          { return ttWrapStyle("\x1b[36m", "\x1b[39m", text) }
func ttYellow(text string) string        { return ttWrapStyle("\x1b[33m", "\x1b[39m", text) }
func ttGreen(text string) string         { return ttWrapStyle("\x1b[32m", "\x1b[39m", text) }
func ttBold(text string) string          { return ttWrapStyle("\x1b[1m", "\x1b[22m", text) }
func ttDim(text string) string           { return ttWrapStyle("\x1b[2m", "\x1b[22m", text) }
func ttItalic(text string) string        { return ttWrapStyle("\x1b[3m", "\x1b[23m", text) }
func ttUnderline(text string) string     { return ttWrapStyle("\x1b[4m", "\x1b[24m", text) }
func ttStrikethrough(text string) string { return ttWrapStyle("\x1b[9m", "\x1b[29m", text) }
func ttBoldCyan(text string) string      { return ttBold(ttCyan(text)) }

// DefaultSelectListTheme provides chalk-equivalent ANSI styling for SelectList tests.
var DefaultSelectListTheme = tui.SelectListTheme{
	SelectedPrefix: ttBlue,
	SelectedText:   ttBold,
	Description:    ttDim,
	ScrollInfo:     ttDim,
	NoMatch:        ttDim,
}

// DefaultMarkdownTheme provides chalk-equivalent ANSI styling for Markdown tests.
var DefaultMarkdownTheme = tui.MarkdownTheme{
	Heading:         ttBoldCyan,
	Link:            ttBlue,
	LinkUrl:         ttDim,
	Code:            ttYellow,
	CodeBlock:       ttGreen,
	CodeBlockBorder: ttDim,
	Quote:           ttItalic,
	QuoteBorder:     ttDim,
	Hr:              ttDim,
	ListBullet:      ttCyan,
	Bold:            ttBold,
	Italic:          ttItalic,
	Strikethrough:   ttStrikethrough,
	Underline:       ttUnderline,
}

// DefaultEditorTheme provides chalk-equivalent ANSI styling for Editor tests.
var DefaultEditorTheme = tui.EditorTheme{
	BorderColor: ttDim,
	SelectList:  DefaultSelectListTheme,
}
