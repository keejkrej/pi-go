// Ported from packages/coding-agent/src/modes/interactive/components/custom-message.ts (pi v1.5.2).

package components

import (
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// CustomMessageComponent renders a custom extension message using an optional custom renderer or default themed box.
type CustomMessageComponent struct {
	*tui.Container
	box                  *tui.Box
	message              *core.CustomMessage
	customRenderer       core.MessageRenderer
	hasCustomTheme       bool
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	messageId            *string
	timestamp            *string
	expanded             bool
}

// NewCustomMessageComponent creates a CustomMessageComponent.
func NewCustomMessageComponent(
	message *core.CustomMessage,
	customRenderer core.MessageRenderer,
	options ...*MessageComponentOptions,
) *CustomMessageComponent {
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

	root := tui.NewContainer()
	box := tui.NewBox(outputPad, 1, func(t string) string {
		return theme.CurrentTheme().Bg(theme.BgCustomMessage, t)
	})

	c := &CustomMessageComponent{
		Container:            root,
		box:                  box,
		message:              message,
		customRenderer:       customRenderer,
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

// SetExpanded updates the expanded flag and rebuilds content.
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

// SetOutputPad updates horizontal padding and rebuilds content.
func (c *CustomMessageComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate drops cached rendering state and rebuilds under the current theme.
func (c *CustomMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomMessageComponent) rebuild() {
	c.Clear()
	c.box.Clear()

	th := theme.CurrentTheme()
	if c.customRenderer != nil && c.message != nil {
		opts := core.MessageRenderOptions{
			KeybindingHints: core.DefaultKeybindingHints(),
			Expanded:        c.expanded,
			OutputPad:       c.outputPad,
		}
		if rendered := cmSafeInvokeRenderer(c.customRenderer, c.message, opts, th); rendered != nil {
			c.AddChild(rendered)
			return
		}
	}

	c.box.SetPaddingX(c.outputPad)
	customType := ""
	var text string
	if c.message != nil {
		customType = c.message.CustomType
		text = cmExtractText(c.message.Content)
	}

	label := th.Fg(theme.FgCustomMessageLabel, "\x1b[1m["+customType+"]\x1b[22m")
	c.box.AddChild(tui.NewText(label, 0, 0, nil))
	c.box.AddChild(tui.NewSpacer(1))
	c.box.AddChild(tui.NewMarkdown(
		text,
		0,
		0,
		c.markdownTheme,
		&tui.DefaultTextStyle{
			Color: func(t string) string {
				return theme.CurrentTheme().Fg(theme.FgCustomMessageText, t)
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
	c.AddChild(c.box)
}

func cmSafeInvokeRenderer(
	renderer core.MessageRenderer,
	message *core.CustomMessage,
	opts core.MessageRenderOptions,
	th *theme.Theme,
) (comp tui.Component) {
	defer func() {
		if recover() != nil {
			comp = nil
		}
	}()
	return renderer(message, opts, th)
}

func cmExtractText(content ai.UserMessageContent) string {
	if content.Text != nil {
		return *content.Text
	}
	var parts []string
	for _, block := range content.Blocks {
		if tc, ok := block.(*ai.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
