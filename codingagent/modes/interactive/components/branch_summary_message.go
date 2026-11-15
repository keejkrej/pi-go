// Ported from packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts (pi v1.5.2).

package components

import (
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// BranchSummaryMessageComponent renders a collapsible summary of an abandoned branch.
type BranchSummaryMessageComponent struct {
	*tui.Box
	message              *core.BranchSummaryMessage
	hasCustomTheme       bool
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	messageId            *string
	timestamp            *string
	expanded             bool
}

// NewBranchSummaryMessageComponent creates a BranchSummaryMessageComponent.
func NewBranchSummaryMessageComponent(message *core.BranchSummaryMessage, options ...*MessageComponentOptions) *BranchSummaryMessageComponent {
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

	c := &BranchSummaryMessageComponent{
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

// SetExpanded toggles whether the full summary Markdown is shown.
func (c *BranchSummaryMessageComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

// SetOutputPad sets horizontal box padding.
func (c *BranchSummaryMessageComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate clears caches and rebuilds with the current theme.
func (c *BranchSummaryMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Box.Invalidate()
	c.rebuild()
}

func (c *BranchSummaryMessageComponent) rebuild() {
	c.Clear()
	c.SetPaddingX(c.outputPad)

	th := theme.CurrentTheme()
	header := tui.NewContainer()
	label := th.Fg(theme.FgCustomMessageLabel, "\x1b[1m[branch]\x1b[22m")
	header.AddChild(tui.NewText(label, 0, 0, nil))
	header.AddChild(tui.NewSpacer(1))

	if !c.expanded {
		collapsed := fmt.Sprintf(
			"%s %s",
			th.Fg(theme.FgCustomMessageText, "Branch Summary"),
			th.Fg(theme.FgMuted, "("+theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand")+")"),
		)
		header.AddChild(tui.NewText(collapsed, 0, 0, nil))
	} else {
		header.AddChild(tui.NewText(th.Fg(theme.FgCustomMessageText, "\x1b[1mBranch Summary\x1b[22m"), 0, 0, nil))
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
