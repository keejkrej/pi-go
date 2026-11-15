// Ported from packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts (pi v1.5.2).

package components

import (
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// SkillInvocationMessageComponent renders a collapsible loaded-skill message box.
type SkillInvocationMessageComponent struct {
	*tui.Box
	skillBlock           *core.ParsedSkillBlock
	hasCustomTheme       bool
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	messageId            *string
	timestamp            *string
	expanded             bool
}

// NewSkillInvocationMessageComponent creates a SkillInvocationMessageComponent.
func NewSkillInvocationMessageComponent(skillBlock *core.ParsedSkillBlock, options ...*MessageComponentOptions) *SkillInvocationMessageComponent {
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

	c := &SkillInvocationMessageComponent{
		Box:                  box,
		skillBlock:           skillBlock,
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

// SetExpanded toggles whether the full skill Markdown content is displayed.
func (c *SkillInvocationMessageComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

// SetOutputPad updates horizontal padding and rebuilds the view.
func (c *SkillInvocationMessageComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate drops cached rendering state and rebuilds under the current theme.
func (c *SkillInvocationMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Box.Invalidate()
	c.rebuild()
}

func (c *SkillInvocationMessageComponent) rebuild() {
	c.Clear()
	c.SetPaddingX(c.outputPad)

	th := theme.CurrentTheme()
	name := ""
	location := ""
	content := ""
	if c.skillBlock != nil {
		name = c.skillBlock.Name
		location = c.skillBlock.Location
		content = c.skillBlock.Content
	}

	header := tui.NewContainer()
	label := th.Fg(theme.FgCustomMessageLabel, "\x1b[1m[skill]\x1b[22m")
	header.AddChild(tui.NewText(label, 0, 0, nil))
	header.AddChild(tui.NewSpacer(1))

	if !c.expanded {
		collapsed := fmt.Sprintf(
			"%s %s",
			th.Fg(theme.FgCustomMessageText, fmt.Sprintf("Loaded skill: %s", name)),
			th.Fg(theme.FgMuted, "("+theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand")+")"),
		)
		header.AddChild(tui.NewText(collapsed, 0, 0, nil))
	} else {
		headerLine := th.Fg(theme.FgCustomMessageText, "\x1b[1m"+name+"\x1b[22m")
		if location != "" {
			headerLine += "\n" + th.Fg(theme.FgMuted, location)
		}
		header.AddChild(tui.NewText(headerLine, 0, 0, nil))
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

	if c.expanded {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewMarkdown(
			content,
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
					core.MarkdownMessageTypeCustom,
					func() bool { return false },
					c.markdownTransformers,
					c.messageId,
					c.timestamp,
				),
			},
		))
	}
}
