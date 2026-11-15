// Ported from packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// CompactionSummaryMessageComponent renders a collapsible compaction summary message.
type CompactionSummaryMessageComponent struct {
	*tui.Box
	message              *core.CompactionSummaryMessage
	hasCustomTheme       bool
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	messageId            *string
	timestamp            *string
	expanded             bool
}

// NewCompactionSummaryMessageComponent creates a CompactionSummaryMessageComponent.
func NewCompactionSummaryMessageComponent(message *core.CompactionSummaryMessage, options ...*MessageComponentOptions) *CompactionSummaryMessageComponent {
	var opts *MessageComponentOptions
	if len(options) > 0 {
		opts = options[0]
	}

	outputPad := 1
	hasCustomTheme := false
	var mdTheme tui.MarkdownTheme
	var transformers []core.MarkdownTransformer
	var messageId, timestamp *string

	if opts != nil {
		if opts.OutputPad != nil {
			outputPad = max(0, *opts.OutputPad)
		}
		if opts.MarkdownTheme != nil {
			mdTheme = *opts.MarkdownTheme
			hasCustomTheme = true
		}
		if len(opts.MarkdownTransformers) > 0 {
			transformers = append([]core.MarkdownTransformer(nil), opts.MarkdownTransformers...)
		}
		messageId = opts.MessageId
		timestamp = opts.Timestamp
	}
	if !hasCustomTheme {
		mdTheme = theme.GetMarkdownTheme()
	}

	box := tui.NewBox(outputPad, 1, func(t string) string {
		return theme.CurrentTheme().Bg(theme.BgCustomMessage, t)
	})

	c := &CompactionSummaryMessageComponent{
		Box:                  box,
		message:              message,
		hasCustomTheme:       hasCustomTheme,
		markdownTheme:        mdTheme,
		outputPad:            outputPad,
		markdownTransformers: transformers,
		messageId:            messageId,
		timestamp:            timestamp,
	}
	c.rebuild()
	return c
}

// SetExpanded toggles whether the compaction summary Markdown body is expanded.
func (c *CompactionSummaryMessageComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

// SetOutputPad updates horizontal box padding and rebuilds the view.
func (c *CompactionSummaryMessageComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate drops cached lines and rebuilds under the current theme.
func (c *CompactionSummaryMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Box.Invalidate()
	c.rebuild()
}

func (c *CompactionSummaryMessageComponent) rebuild() {
	c.Clear()
	c.SetPaddingX(c.outputPad)

	th := theme.CurrentTheme()
	header := tui.NewContainer()
	label := th.Fg(theme.FgCustomMessageLabel, "\x1b[1m[compaction]\x1b[22m")
	header.AddChild(tui.NewText(label, 0, 0, nil))
	header.AddChild(tui.NewSpacer(1))

	tokensBefore := 0
	if c.message != nil {
		tokensBefore = c.message.TokensBefore
	}
	summaryHeader := fmt.Sprintf("Compacted from %s tokens", csmFormatTokens(tokensBefore))

	if !c.expanded {
		collapsed := fmt.Sprintf(
			"%s %s",
			th.Fg(theme.FgCustomMessageText, summaryHeader),
			th.Fg(theme.FgMuted, "("+theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand")+")"),
		)
		header.AddChild(tui.NewText(collapsed, 0, 0, nil))
	} else {
		header.AddChild(tui.NewText(th.Fg(theme.FgCustomMessageText, "\x1b[1m"+summaryHeader+"\x1b[22m"), 0, 0, nil))
	}

	clickableHeader := tui.NewMouseRegion(header, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.TuiMouseEventTypeClick || event.Button != tui.TuiMouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expanded)
		handled := true
		return &tui.TuiMouseEventResult{Handled: &handled}
	})
	c.AddChild(clickableHeader)

	if c.expanded && c.message != nil {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewMarkdown(
			c.message.Summary,
			0,
			0,
			c.markdownTheme,
			&tui.DefaultTextStyle{
				Color: func(text string) string {
					return theme.CurrentTheme().Fg(theme.FgCustomMessageText, text)
				},
			},
			&tui.MarkdownOptions{
				Transform: CreateMarkdownTransform(
					core.MarkdownMessageTypeSummary,
					func() bool { return false },
					c.markdownTransformers,
					c.messageId,
					c.timestamp,
				),
			},
		))
	}
}

func csmFormatTokens(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if rem < len(s) {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
