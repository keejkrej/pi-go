// Ported from packages/coding-agent/src/modes/interactive/components/user-message.ts (pi v1.5.2).

package components

import (
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const (
	umOsc133ZoneStart = "\x1b]133;A\x07"
	umOsc133ZoneEnd   = "\x1b]133;B\x07"
	umOsc133ZoneFinal = "\x1b]133;C\x07"
)

// MessageComponentOptions configures rendering and Markdown transforms for transcript message components.
type MessageComponentOptions struct {
	MarkdownTheme        *tui.MarkdownTheme
	OutputPad            *int
	MarkdownTransformers []core.MarkdownTransformer
	MessageId            *string
	Timestamp            *string
}

// UserMessageComponent renders a user prompt with theme styling, Markdown formatting, and OSC 133 markers.
type UserMessageComponent struct {
	*tui.Container
	text                 string
	hasCustomTheme       bool
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	messageId            *string
	timestamp            *string
}

// NewUserMessageComponent creates a UserMessageComponent for the given prompt text and optional settings.
func NewUserMessageComponent(text string, options ...*MessageComponentOptions) *UserMessageComponent {
	var opts *MessageComponentOptions
	if len(options) > 0 {
		opts = options[0]
	}
	outputPad := 1
	var mdTheme tui.MarkdownTheme
	hasCustomTheme := false
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

	c := &UserMessageComponent{
		Container:            tui.NewContainer(),
		text:                 text,
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

// SetOutputPad updates horizontal padding and rebuilds the markdown child.
func (c *UserMessageComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate clears cached rendering state and refreshes the default markdown theme if uncustomized.
func (c *UserMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Container.Invalidate()
}

// Render renders the user message wrapped with OSC 133 semantic prompt zone markers.
func (c *UserMessageComponent) Render(width int) []string {
	lines := c.Container.Render(width)
	if len(lines) == 0 {
		return lines
	}
	lines[0] = umOsc133ZoneStart + lines[0]
	lines[len(lines)-1] = umOsc133ZoneEnd + umOsc133ZoneFinal + lines[len(lines)-1]
	return lines
}

func (c *UserMessageComponent) rebuild() {
	c.Clear()
	c.AddChild(tui.NewMarkdown(
		c.text,
		c.outputPad,
		1,
		c.markdownTheme,
		&tui.DefaultTextStyle{
			Color: func(t string) string {
				return theme.CurrentTheme().Fg(theme.FgUserMessageText, t)
			},
			BgColor: func(t string) string {
				return theme.CurrentTheme().Bg(theme.BgUserMessage, t)
			},
		},
		&tui.MarkdownOptions{
			Transform: CreateMarkdownTransform(
				core.MarkdownMessageTypeUser,
				func() bool { return false },
				c.markdownTransformers,
				c.messageId,
				c.timestamp,
			),
		},
	))
}
