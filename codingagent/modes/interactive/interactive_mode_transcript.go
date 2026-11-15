package interactive

import (
	"fmt"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type imtrAddMessageOptions struct {
	PopulateHistory bool
	MessageId       *string
	Timestamp       *string
}

type imtrRenderSessionContextOptions struct {
	ShowHeaders bool
}

func (m *InteractiveMode) imtrBuildMarkdownTransformers() []core.MarkdownTransformer {
	var transformers []core.MarkdownTransformer
	if runner := m.imExtensionRunner(); runner != nil {
		transformers = append(transformers, runner.GetMarkdownTransformers()...)
	}
	transformers = append(transformers, components.CreateMermaidMarkdownTransformer(components.MermaidTransformerOptions{
		GetMode: func() core.MermaidRenderingMode {
			if sm := m.imSettingsManager(); sm != nil {
				return sm.GetMermaidRenderingMode()
			}
			return core.MermaidRenderingOff
		},
		Theme: theme.CurrentTheme(),
	}))
	return transformers
}

func imtrFormatTimestampString(tsMs int64) *string {
	if tsMs <= 0 {
		return nil
	}
	formatted := time.UnixMilli(tsMs).UTC().Format(time.RFC3339)
	return &formatted
}

func imtrExtractUserText(msg *ai.UserMessage) string {
	if msg == nil {
		return ""
	}
	if msg.Content.Text != nil {
		return *msg.Content.Text
	}
	var parts []string
	for _, block := range msg.Content.Blocks {
		if t, ok := block.(*ai.TextContent); ok && t != nil {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func imtrToJsonObject(raw any) *jsonx.Object {
	if raw == nil {
		return nil
	}
	if obj, ok := raw.(*jsonx.Object); ok {
		return obj
	}
	if obj, ok := raw.(jsonx.Object); ok {
		cp := obj
		return &cp
	}
	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil
	}
	var out jsonx.Object
	if err := jsonx.Unmarshal(encoded, &out); err != nil {
		return nil
	}
	return &out
}

func (m *InteractiveMode) imtrResolveToolRenderers(toolName string) *core.ToolRenderers {
	session := m.imSession()
	runner := m.imExtensionRunner()

	var baseFn func() *core.ToolRenderers
	if session != nil {
		if def := session.GetToolDefinition(toolName); def != nil {
			baseFn = func() *core.ToolRenderers {
				return components.ToolRenderersFromDefinition(def)
			}
		}
	}
	if runner != nil {
		return runner.ResolveToolRenderers(toolName, baseFn)
	}
	if baseFn != nil {
		return baseFn()
	}
	return nil
}

func (m *InteractiveMode) imtrClearChatContainer() {
	m.mu.Lock()
	chat := m.chatContainer
	m.pendingTools = make(map[string]*components.ToolExecutionComponent)
	m.pendingToolsOrder = nil
	m.streamingComponent = nil
	m.streamingMessage = nil
	m.mu.Unlock()

	if chat == nil {
		return
	}
	for _, child := range chat.Children() {
		imhDisposeComponent(child)
	}
	chat.Clear()
}

func (m *InteractiveMode) imtrRenderInitialMessages() {
	sm := m.imSessionManager()
	if sm == nil {
		return
	}
	m.imtrRenderBranchEntries(sm.GetBranch(), imtrRenderSessionContextOptions{ShowHeaders: true})
}

func (m *InteractiveMode) imtrRebuildTranscript() {
	sm := m.imSessionManager()
	if sm == nil {
		return
	}
	m.mu.Lock()
	m.transcriptRenderGeneration++
	m.mu.Unlock()

	m.imtrRenderBranchEntries(sm.GetBranch(), imtrRenderSessionContextOptions{ShowHeaders: true})
}

func (m *InteractiveMode) imtrRenderHeadersIfRequested(showHeaders bool) {
	if !showHeaders {
		return
	}
	m.imcRenderHeaderAndChangelog()
	m.mu.Lock()
	m.headerRendered = true
	m.mu.Unlock()
	m.imcFlushQueuedStartupNotices()
}

func (m *InteractiveMode) imtrRenderSessionContext(sessionContext core.SessionContext, options ...imtrRenderSessionContextOptions) {
	opt := imtrRenderSessionContextOptions{}
	if len(options) > 0 {
		opt = options[0]
	}

	m.imtrClearChatContainer()

	m.mu.Lock()
	if opt.ShowHeaders {
		m.headerRendered = false
	}
	chat := m.chatContainer
	m.mu.Unlock()

	m.imtrRenderHeadersIfRequested(opt.ShowHeaders)
	if chat == nil {
		return
	}

	rendered := 0
	toolComps := make(map[string]*components.ToolExecutionComponent)

	for _, msg := range sessionContext.Messages {
		switch v := msg.(type) {
		case *ai.ToolResultMessage:
			if v == nil {
				continue
			}
			if tc := toolComps[v.ToolCallId]; tc != nil {
				var details any
				if d, ok := v.Details.Get(); ok {
					details = d
				}
				tc.UpdateResult(components.ToolExecutionResult{
					Content: v.Content,
					Details: details,
					IsError: v.IsError,
				}, false)
			}
		case *ai.AssistantMessage:
			if v == nil {
				continue
			}
			m.imtrAddMessageToChat(v, imtrAddMessageOptions{Timestamp: imtrFormatTimestampString(v.Timestamp)})
			rendered++
			for _, block := range v.Content {
				tc, ok := block.(*ai.ToolCall)
				if !ok || tc == nil {
					continue
				}
				comp := m.imtrCreateToolExecutionComponent(tc.Name, tc.Id, tc.Arguments)
				if comp != nil {
					comp.SetArgsComplete()
					chat.AddChild(comp)
					toolComps[tc.Id] = comp
				}
			}
		default:
			m.imtrAddMessageToChat(msg, imtrAddMessageOptions{PopulateHistory: true})
			rendered++
		}
	}

	m.mu.Lock()
	m.transcriptRenderedCount = rendered
	m.mu.Unlock()

	m.imRequestRender()
}

func (m *InteractiveMode) imtrRenderBranchEntries(branch []core.SessionEntry, opt imtrRenderSessionContextOptions) {
	m.imtrClearChatContainer()

	m.mu.Lock()
	if opt.ShowHeaders {
		m.headerRendered = false
	}
	chat := m.chatContainer
	expanded := m.toolExpanded
	pad := m.imOutputPad()
	m.mu.Unlock()

	m.imtrRenderHeadersIfRequested(opt.ShowHeaders)
	if chat == nil {
		return
	}

	toolComps := make(map[string]*components.ToolExecutionComponent)
	rendered := 0
	runner := m.imExtensionRunner()
	hasAssistant := false

	for _, entry := range branch {
		if entry == nil {
			continue
		}
		switch e := entry.(type) {
		case *core.SessionMessageEntry:
			ts := e.Timestamp
			id := e.Id
			switch msg := e.Message.(type) {
			case *ai.ToolResultMessage:
				if msg == nil {
					continue
				}
				if tc := toolComps[msg.ToolCallId]; tc != nil {
					var details any
					if d, ok := msg.Details.Get(); ok {
						details = d
					}
					tc.UpdateResult(components.ToolExecutionResult{
						Content: msg.Content,
						Details: details,
						IsError: msg.IsError,
					}, false)
				}
			case *ai.AssistantMessage:
				if msg == nil {
					continue
				}
				hasAssistant = true
				m.imtrAddMessageToChat(msg, imtrAddMessageOptions{
					MessageId: &id,
					Timestamp: &ts,
				})
				rendered++
				for _, block := range msg.Content {
					tc, ok := block.(*ai.ToolCall)
					if !ok || tc == nil {
						continue
					}
					comp := m.imtrCreateToolExecutionComponent(tc.Name, tc.Id, tc.Arguments)
					if comp != nil {
						comp.SetArgsComplete()
						chat.AddChild(comp)
						toolComps[tc.Id] = comp
					}
				}
			default:
				m.imtrAddMessageToChat(e.Message, imtrAddMessageOptions{
					PopulateHistory: true,
					MessageId:       &id,
					Timestamp:       &ts,
				})
				rendered++
			}

		case *core.CompactionEntry:
			ts := e.Timestamp
			id := e.Id
			compMsg := &core.CompactionSummaryMessage{
				Role:         "compactionSummary",
				Summary:      e.Summary,
				TokensBefore: e.TokensBefore,
			}
			m.imtrAddMessageToChat(compMsg, imtrAddMessageOptions{
				MessageId: &id,
				Timestamp: &ts,
			})
			rendered++

		case *core.BranchSummaryEntry:
			ts := e.Timestamp
			id := e.Id
			fromID := e.FromId
			branchMsg := &core.BranchSummaryMessage{
				Role:    "branchSummary",
				Summary: e.Summary,
				FromId:  &fromID,
			}
			m.imtrAddMessageToChat(branchMsg, imtrAddMessageOptions{
				MessageId: &id,
				Timestamp: &ts,
			})
			rendered++

		case *core.CustomEntry:
			if runner == nil {
				continue
			}
			entryRenderer := runner.GetEntryRenderer(e.CustomType)
			if entryRenderer == nil {
				continue
			}
			comp := components.NewCustomEntryComponent(e, entryRenderer, pad)
			if comp.HasContent() {
				comp.SetExpanded(expanded)
				chat.AddChild(comp)
				rendered++
			}

		case *core.ThinkingLevelChangeEntry:
			m.mu.Lock()
			m.lastThinkingLevel = e.ThinkingLevel
			m.mu.Unlock()
		}
	}

	if hasAssistant {
		m.mu.Lock()
		m.transcriptRenderedCount = rendered
		m.mu.Unlock()
	} else {
		m.mu.Lock()
		m.transcriptRenderedCount = rendered
		m.mu.Unlock()
	}

	m.imlcRefreshFooter()
	m.imRequestRender()
}

func (m *InteractiveMode) imtrCreateToolExecutionComponent(toolName, toolCallID string, args *jsonx.Object) *components.ToolExecutionComponent {
	ui := m.imCurrentUI()
	if ui == nil {
		return nil
	}
	showImages := m.imShowImages()
	imageWidth := m.imImageWidthCells()
	pad := m.imOutputPad()
	renderers := m.imtrResolveToolRenderers(toolName)

	comp := components.NewToolExecutionComponent(
		toolName,
		toolCallID,
		args,
		&components.ToolExecutionOptions{
			ShowImages:      &showImages,
			ImageWidthCells: &imageWidth,
			OutputPad:       &pad,
		},
		renderers,
		ui,
		m.imCwd(),
	)

	m.mu.Lock()
	expanded := m.toolExpanded
	m.mu.Unlock()
	comp.SetExpanded(expanded)
	return comp
}

func (m *InteractiveMode) imtrAddMessageToChat(message agent.AgentMessage, options ...imtrAddMessageOptions) tui.Component {
	if message == nil {
		return nil
	}
	opt := imtrAddMessageOptions{}
	if len(options) > 0 {
		opt = options[0]
	}

	m.mu.Lock()
	chat := m.chatContainer
	expanded := m.toolExpanded
	hideThinking := m.hideThinkingBlock
	hiddenLabel := m.hiddenThinkingLabel
	ed := m.editor
	m.mu.Unlock()

	if chat == nil {
		return nil
	}

	pad := m.imOutputPad()
	mdTheme := theme.GetMarkdownTheme()
	transformers := m.imtrBuildMarkdownTransformers()

	switch msg := message.(type) {
	case *ai.UserMessage:
		if msg == nil {
			return nil
		}
		text := imtrExtractUserText(msg)
		if opt.PopulateHistory && ed != nil && strings.TrimSpace(text) != "" {
			ed.AddToHistory(text)
		}
		ts := opt.Timestamp
		if ts == nil {
			ts = imtrFormatTimestampString(msg.Timestamp)
		}
		msgOpts := &components.MessageComponentOptions{
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			MessageId:            opt.MessageId,
			Timestamp:            ts,
		}
		if skillBlock := core.ParseSkillBlock(text); skillBlock != nil {
			skillComp := components.NewSkillInvocationMessageComponent(skillBlock, msgOpts)
			skillComp.SetExpanded(expanded)
			chat.AddChild(skillComp)
			userMsg := imhDerefStr(skillBlock.UserMessage)
			if strings.TrimSpace(userMsg) != "" {
				userComp := components.NewUserMessageComponent(userMsg, msgOpts)
				chat.AddChild(userComp)
				return userComp
			}
			return skillComp
		}
		userComp := components.NewUserMessageComponent(text, msgOpts)
		chat.AddChild(userComp)
		return userComp

	case *ai.AssistantMessage:
		if msg == nil {
			return nil
		}
		ts := opt.Timestamp
		if ts == nil {
			ts = imtrFormatTimestampString(msg.Timestamp)
		}
		comp := components.NewAssistantMessageComponent(msg, &components.AssistantMessageOptions{
			HideThinkingBlock:    &hideThinking,
			HiddenThinkingLabel:  hiddenLabel,
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			MessageId:            opt.MessageId,
			Timestamp:            ts,
		})
		chat.AddChild(comp)
		return comp

	case *core.BashExecutionMessage:
		if msg == nil {
			return nil
		}
		excludeFromCtx := msg.ExcludeFromContext != nil && *msg.ExcludeFromContext
		comp := components.NewBashExecutionComponent(msg.Command, m.imCurrentUI(), excludeFromCtx, pad)
		if msg.Output != "" {
			comp.AppendOutput(msg.Output)
		}
		var trunc *core.TruncationResult
		if msg.Truncated {
			trunc = &core.TruncationResult{Truncated: true}
		}
		comp.SetComplete(msg.ExitCode, msg.Cancelled, trunc, msg.FullOutputPath)
		comp.SetExpanded(expanded)
		chat.AddChild(comp)
		return comp

	case *core.CustomMessage:
		if msg == nil || !msg.Display {
			return nil
		}
		var customRenderer core.MessageRenderer
		if runner := m.imExtensionRunner(); runner != nil {
			customRenderer = runner.GetMessageRenderer(msg.CustomType)
		}
		ts := opt.Timestamp
		if ts == nil {
			ts = imtrFormatTimestampString(msg.Timestamp)
		}
		comp := components.NewCustomMessageComponent(msg, customRenderer, &components.MessageComponentOptions{
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			MessageId:            opt.MessageId,
			Timestamp:            ts,
		})
		comp.SetExpanded(expanded)
		chat.AddChild(comp)
		return comp

	case *core.BranchSummaryMessage:
		if msg == nil {
			return nil
		}
		ts := opt.Timestamp
		if ts == nil {
			ts = imtrFormatTimestampString(msg.Timestamp)
		}
		comp := components.NewBranchSummaryMessageComponent(msg, &components.MessageComponentOptions{
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			MessageId:            opt.MessageId,
			Timestamp:            ts,
		})
		comp.SetExpanded(expanded)
		chat.AddChild(comp)
		return comp

	case *core.CompactionSummaryMessage:
		if msg == nil {
			return nil
		}
		ts := opt.Timestamp
		if ts == nil {
			ts = imtrFormatTimestampString(msg.Timestamp)
		}
		comp := components.NewCompactionSummaryMessageComponent(msg, &components.MessageComponentOptions{
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			MessageId:            opt.MessageId,
			Timestamp:            ts,
		})
		comp.SetExpanded(expanded)
		chat.AddChild(comp)
		return comp

	case *ai.ToolResultMessage:
		return nil

	default:
		text := fmt.Sprintf("%v", message)
		if strings.TrimSpace(text) != "" {
			comp := tui.NewText(text, pad, 0, nil)
			chat.AddChild(comp)
			return comp
		}
		return nil
	}
}
