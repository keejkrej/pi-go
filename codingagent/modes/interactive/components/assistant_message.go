// Ported from packages/coding-agent/src/modes/interactive/components/assistant-message.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const (
	amOsc133ZoneStart = "\x1b]133;A\x07"
	amOsc133ZoneEnd   = "\x1b]133;B\x07"
	amOsc133ZoneFinal = "\x1b]133;C\x07"
)

// AssistantMessageOptions configures an AssistantMessageComponent.
type AssistantMessageOptions struct {
	HideThinkingBlock    *bool
	HiddenThinkingLabel  *string
	MarkdownTheme        *tui.MarkdownTheme
	OutputPad            *int
	MarkdownTransformers []core.MarkdownTransformer
	MessageId            *string
	Timestamp            *string
}

type amMarkdownSlot struct {
	kind     string // "text" or "thinking"
	markdown *tui.Markdown
}

// AssistantMessageComponent renders an assistant turn's thinking blocks, text blocks, and stop errors.
type AssistantMessageComponent struct {
	*tui.Container
	contentContainer            *tui.Container
	hideThinkingBlock           bool
	hiddenThinkingLabel         string
	hasCustomTheme              bool
	markdownTheme               tui.MarkdownTheme
	outputPad                   int
	markdownTransformers        []core.MarkdownTransformer
	messageId                   *string
	timestamp                   *string
	lastMessage                 *ai.AssistantMessage
	hasToolCalls                bool
	isStreaming                 bool
	markdownSlots               []amMarkdownSlot
	thinkingVisibilityOverrides map[int]bool
}

// NewAssistantMessageComponent creates an AssistantMessageComponent for the optional initial message.
func NewAssistantMessageComponent(message *ai.AssistantMessage, options ...*AssistantMessageOptions) *AssistantMessageComponent {
	var opts *AssistantMessageOptions
	if len(options) > 0 {
		opts = options[0]
	}

	hideThinking := false
	hiddenLabel := "Thinking..."
	outputPad := 1
	hasCustomTheme := false
	var mdTheme tui.MarkdownTheme
	var transformers []core.MarkdownTransformer
	var messageId, timestamp *string

	if opts != nil {
		if opts.HideThinkingBlock != nil {
			hideThinking = *opts.HideThinkingBlock
		}
		if opts.HiddenThinkingLabel != nil {
			hiddenLabel = *opts.HiddenThinkingLabel
		}
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
	contentContainer := tui.NewContainer()
	root.AddChild(contentContainer)

	c := &AssistantMessageComponent{
		Container:                   root,
		contentContainer:            contentContainer,
		hideThinkingBlock:           hideThinking,
		hiddenThinkingLabel:         hiddenLabel,
		hasCustomTheme:              hasCustomTheme,
		markdownTheme:               mdTheme,
		outputPad:                   outputPad,
		markdownTransformers:        transformers,
		messageId:                   messageId,
		timestamp:                   timestamp,
		thinkingVisibilityOverrides: make(map[int]bool),
	}
	if message != nil {
		c.UpdateContent(message)
	}
	return c
}

// SetHideThinkingBlock sets whether thinking runs start collapsed and resets manual per-run overrides.
func (c *AssistantMessageComponent) SetHideThinkingBlock(hide bool) {
	c.hideThinkingBlock = hide
	clear(c.thinkingVisibilityOverrides)
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage)
	}
}

// SetHiddenThinkingLabel sets the label displayed for collapsed thinking runs.
func (c *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	c.hiddenThinkingLabel = label
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage)
	}
}

// SetOutputPad updates horizontal padding and rebuilds markdown components cleanly.
func (c *AssistantMessageComponent) SetOutputPad(padding int) {
	next := max(0, padding)
	if next != c.outputPad {
		c.outputPad = next
		c.markdownSlots = nil
	}
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage)
	}
}

// Invalidate drops cached rendering state and re-renders the current message under current theme.
func (c *AssistantMessageComponent) Invalidate() {
	if !c.hasCustomTheme {
		c.markdownTheme = theme.GetMarkdownTheme()
		c.markdownSlots = nil
	}
	c.Container.Invalidate()
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage)
	}
}

// Render renders the assistant content and attaches OSC 133 semantic markers when no tool calls are present.
func (c *AssistantMessageComponent) Render(width int) []string {
	lines := c.Container.Render(width)
	if c.hasToolCalls || len(lines) == 0 {
		return lines
	}
	lines[0] = amOsc133ZoneStart + lines[0]
	lines[len(lines)-1] = amOsc133ZoneEnd + amOsc133ZoneFinal + lines[len(lines)-1]
	return lines
}

// UpdateContent updates the displayed message blocks, optionally marking the stream state.
func (c *AssistantMessageComponent) UpdateContent(message *ai.AssistantMessage, isStreaming ...bool) {
	c.lastMessage = message
	if len(isStreaming) > 0 {
		c.isStreaming = isStreaming[0]
	}

	c.contentContainer.Clear()
	if message == nil {
		c.hasToolCalls = false
		c.markdownSlots = nil
		return
	}

	previousSlots := c.markdownSlots
	c.markdownSlots = nil
	markdownBlock := func(kind, text string, create func() *tui.Markdown) *tui.Markdown {
		idx := len(c.markdownSlots)
		var md *tui.Markdown
		if idx < len(previousSlots) && previousSlots[idx].kind == kind {
			md = previousSlots[idx].markdown
		} else {
			md = create()
		}
		md.SetText(text)
		c.markdownSlots = append(c.markdownSlots, amMarkdownSlot{kind: kind, markdown: md})
		return md
	}

	needsSpacer := true
	addVisibleChild := func(child tui.Component) {
		if needsSpacer {
			c.contentContainer.AddChild(tui.NewSpacer(1))
			needsSpacer = false
		}
		c.contentContainer.AddChild(child)
	}

	th := theme.CurrentTheme()
	thinkingRunIndex := 0
	hasToolCalls := false

	for i := 0; i < len(message.Content); i++ {
		block := message.Content[i]
		switch b := block.(type) {
		case *ai.TextContent:
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			addVisibleChild(markdownBlock("text", text, func() *tui.Markdown {
				return tui.NewMarkdown(text, c.outputPad, 0, c.markdownTheme, nil, &tui.MarkdownOptions{
					Transform: CreateMarkdownTransform(
						core.MarkdownMessageTypeAssistant,
						func() bool { return c.isStreaming },
						c.markdownTransformers,
						c.messageId,
						c.timestamp,
					),
				})
			}))
		case *ai.ThinkingContent:
			var thinkingBlocks []string
			for ; i < len(message.Content); i++ {
				tb, ok := message.Content[i].(*ai.ThinkingContent)
				if !ok {
					break
				}
				trimmed := strings.TrimSpace(tb.Thinking)
				if trimmed != "" {
					thinkingBlocks = append(thinkingBlocks, trimmed)
				}
			}
			i--

			if len(thinkingBlocks) == 0 {
				continue
			}

			runIndex := thinkingRunIndex
			thinkingRunIndex++
			hidden, ok := c.thinkingVisibilityOverrides[runIndex]
			if !ok {
				hidden = c.hideThinkingBlock
			}
			if hidden && strings.TrimSpace(c.hiddenThinkingLabel) == "" {
				continue
			}

			thinking := strings.Join(thinkingBlocks, "\n\n")
			var thinkingComp tui.Component
			if hidden {
				thinkingComp = tui.NewText(
					th.Italic(th.Fg(theme.FgThinkingText, c.hiddenThinkingLabel)),
					c.outputPad,
					0,
					nil,
				)
			} else {
				italicTrue := true
				thinkingComp = markdownBlock("thinking", thinking, func() *tui.Markdown {
					return tui.NewMarkdown(
						thinking,
						c.outputPad,
						0,
						c.markdownTheme,
						&tui.DefaultTextStyle{
							Color: func(text string) string {
								return theme.CurrentTheme().Fg(theme.FgThinkingText, text)
							},
							Italic: &italicTrue,
						},
						&tui.MarkdownOptions{
							Transform: CreateMarkdownTransform(
								core.MarkdownMessageTypeAssistantThinking,
								func() bool { return c.isStreaming },
								c.markdownTransformers,
								c.messageId,
								c.timestamp,
							),
						},
					)
				})
			}

			runIdxCopy := runIndex
			wasHidden := hidden
			addVisibleChild(tui.NewMouseRegion(thinkingComp, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
				if event.Type != tui.TuiMouseEventTypeClick || event.Button != tui.TuiMouseButtonLeft {
					return nil
				}
				c.thinkingVisibilityOverrides[runIdxCopy] = !wasHidden
				if c.lastMessage != nil {
					c.UpdateContent(c.lastMessage)
				}
				handled := true
				return &tui.TuiMouseEventResult{Handled: &handled}
			}))
			needsSpacer = true
		case *ai.ToolCall:
			hasToolCalls = true
		}
	}

	c.hasToolCalls = hasToolCalls
	if message.StopReason == ai.StopReasonLength {
		c.contentContainer.AddChild(tui.NewSpacer(1))
		c.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgError, "Response was truncated before completion."), c.outputPad, 0, nil))
	} else if !hasToolCalls {
		switch message.StopReason {
		case ai.StopReasonAborted:
			abortMessage := "Operation aborted"
			if message.ErrorMessage != nil && *message.ErrorMessage != "" && *message.ErrorMessage != "Request was aborted" {
				abortMessage = *message.ErrorMessage
			}
			c.contentContainer.AddChild(tui.NewSpacer(1))
			c.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgError, abortMessage), c.outputPad, 0, nil))
		case ai.StopReasonError:
			errorMsg := "Unknown error"
			if message.ErrorMessage != nil && *message.ErrorMessage != "" {
				errorMsg = *message.ErrorMessage
			}
			c.contentContainer.AddChild(tui.NewSpacer(1))
			c.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgError, fmt.Sprintf("Error: %s", errorMsg)), c.outputPad, 0, nil))
		}
	}
}
