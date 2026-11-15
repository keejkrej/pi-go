package interactive

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
)

func (m *InteractiveMode) imevSubscribeToAgent() {
	m.mu.Lock()
	if m.unsubscribeSession != nil {
		m.unsubscribeSession()
		m.unsubscribeSession = nil
	}
	session := m.imSession()
	m.mu.Unlock()

	if session == nil {
		return
	}

	unsub := session.Subscribe(func(event core.AgentSessionEvent) {
		m.imPostUI(func() {
			m.imevHandleEvent(event)
		})
	})

	m.mu.Lock()
	m.unsubscribeSession = unsub
	m.mu.Unlock()
}

func (m *InteractiveMode) imevHandleEvent(event core.AgentSessionEvent) {
	if event == nil || m.imIsStopped() {
		return
	}

	session := m.imSession()
	if session == nil {
		return
	}

	m.mu.Lock()
	inited := m.isInitialized
	psr := m.programStatusReporter
	m.mu.Unlock()

	if !inited {
		if err := m.Init(m.bgCtx); err != nil {
			return
		}
	}

	if psr != nil {
		psr.HandleEvent(event)
	}

	m.mu.Lock()
	headerRendered := m.headerRendered
	m.mu.Unlock()
	if !headerRendered {
		m.imcRenderHeaderAndChangelog()
		m.mu.Lock()
		m.headerRendered = true
		m.mu.Unlock()
	}
	m.imlcRefreshFooter()

	switch ev := event.(type) {
	case *core.AgentSessionEventAgentStart:
		m.imevHandleAgentStart()

	case *core.AgentSessionEventTurnStart:
		m.imlcEnsureWorkingStatusIndicator()
		m.imRequestRender()

	case *core.AgentSessionEventMessageStart:
		m.imevHandleMessageStart(ev.Message)

	case *core.AgentSessionEventMessageUpdate:
		m.imevHandleMessageUpdate(ev.Message, ev.AssistantMessageEvent)

	case *core.AgentSessionEventMessageEnd:
		m.imevHandleMessageEnd(ev.Message)

	case *core.AgentSessionEventToolExecutionStart:
		m.imevHandleToolExecutionStart(ev)

	case *core.AgentSessionEventToolExecutionUpdate:
		m.imevHandleToolExecutionUpdate(ev)

	case *core.AgentSessionEventToolExecutionEnd:
		m.imevHandleToolExecutionEnd(ev)

	case *core.AgentSessionEventAgentEnd:
		m.imevHandleAgentEnd(ev)

	case *core.AgentSessionEventAgentSettled:
		m.imevHandleAgentSettled(ev)

	case *core.AgentSessionEventQueueUpdate:
		m.mu.Lock()
		m.queuedSteering = append([]string(nil), ev.Steering...)
		m.queuedFollowUps = append([]string(nil), ev.FollowUp...)
		m.mu.Unlock()
		m.imqUpdatePendingMessagesDisplay()
		m.imRequestRender()

	case *core.AgentSessionEventCompactionStart:
		reason := components.CompactionStatusReasonThreshold
		switch ev.Reason {
		case string(components.CompactionStatusReasonManual):
			reason = components.CompactionStatusReasonManual
		case string(components.CompactionStatusReasonOverflow):
			reason = components.CompactionStatusReasonOverflow
		}
		m.imlcSetCompactionStatus(reason)
		m.imRequestRender()

	case *core.AgentSessionEventCompactionEnd:
		m.imlcClearCompactionStatus()
		if ev.ErrorMessage != nil && strings.TrimSpace(*ev.ErrorMessage) != "" && !ev.WillRetry {
			m.ShowError(*ev.ErrorMessage)
		} else if ev.Aborted {
			m.ShowStatus("Compaction cancelled")
		} else if ev.Result != nil {
			m.imtrRebuildTranscript()
			if m.chatContainer != nil {
				m.ShowStatus(fmt.Sprintf("Compacted context (%d tokens before)", ev.Result.TokensBefore))
			}
		}
		m.imqFlushCompactionQueue(&imqFlushOptions{WillRetry: ev.WillRetry})
		m.imlcRefreshFooter()
		m.imRequestRender()

	case *core.AgentSessionEventEntryAppended:
		m.imevHandleEntryAppended(ev.Entry)

	case *core.AgentSessionEventSessionInfoChanged:
		m.imcUpdateTerminalTitle()
		m.imcReportProgramStatus()
		m.imlcRefreshFooter()
		m.imRequestRender()

	case *core.AgentSessionEventThinkingLevelChanged:
		m.mu.Lock()
		m.lastThinkingLevel = string(ev.Level)
		m.mu.Unlock()
		m.imlcUpdateEditorBorderColor()
		m.imlcRefreshFooter()
		m.imRequestRender()

	case *core.AgentSessionEventAutoRetryStart:
		m.imlcClearWorkingStatusIndicator()
		m.imlcSetRetryStatus(ev.Attempt, ev.MaxAttempts, ev.DelayMs)
		m.imRequestRender()

	case *core.AgentSessionEventAutoRetryEnd:
		m.imlcClearRetryStatus()
		if !ev.Success && ev.FinalError != nil && strings.TrimSpace(*ev.FinalError) != "" {
			m.ShowError(fmt.Sprintf("Retry failed after %d attempts: %s", ev.Attempt, *ev.FinalError))
		}
		m.imRequestRender()

	case *core.AgentSessionEventSummarizationRetryScheduled:
		if m.compactionIndicator != nil {
			m.ShowStatus(fmt.Sprintf("Retrying summarization (attempt %d/%d)...", ev.Attempt, ev.MaxAttempts))
		}
		m.imRequestRender()

	case *core.AgentSessionEventSummarizationRetryAttemptStart:
		if strings.TrimSpace(imhDerefStr(ev.Reason)) != "" {
			m.ShowStatus(*ev.Reason)
		}
		m.imRequestRender()

	case *core.AgentSessionEventSummarizationRetryFinished:
		m.imRequestRender()

	case *core.AgentSessionEventBashExecutionUpdate:
		m.mu.Lock()
		bc := m.bashComponent
		m.mu.Unlock()
		if bc != nil && ev.Delta != "" {
			bc.AppendOutput(ev.Delta)
			m.imRequestRender()
		}
	}
}

func (m *InteractiveMode) imevHandleAgentStart() {
	m.mu.Lock()
	if m.retryIndicator != nil {
		m.retryIndicator.Stop()
		m.retryIndicator = nil
	}
	if m.statusContainer != nil {
		m.statusContainer.Clear()
	}
	m.pendingWorkingMessage = nil
	m.mu.Unlock()

	m.imlcEnsureWorkingStatusIndicator()
	m.imcReportProgramStatus()
	m.imlcUpdateEditorBorderColor()
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleMessageStart(msg agent.AgentMessage) {
	switch v := msg.(type) {
	case *ai.UserMessage:
		m.imtrAddMessageToChat(v, imtrAddMessageOptions{PopulateHistory: true})
		m.imRequestRender()

	case *core.BashExecutionMessage, *core.CustomMessage, *core.BranchSummaryMessage, *core.CompactionSummaryMessage:
		m.imtrAddMessageToChat(v)
		m.imRequestRender()

	case *ai.AssistantMessage:
		m.mu.Lock()
		chat := m.chatContainer
		hideThinking := m.hideThinkingBlock
		hiddenLabel := m.hiddenThinkingLabel
		m.mu.Unlock()
		if chat == nil || v == nil {
			return
		}
		pad := m.imOutputPad()
		mdTheme := theme.GetMarkdownTheme()
		transformers := m.imtrBuildMarkdownTransformers()
		isStreaming := true
		comp := components.NewAssistantMessageComponent(v, &components.AssistantMessageOptions{
			HideThinkingBlock:    &hideThinking,
			HiddenThinkingLabel:  hiddenLabel,
			MarkdownTheme:        &mdTheme,
			OutputPad:            &pad,
			MarkdownTransformers: transformers,
			Timestamp:            imtrFormatTimestampString(v.Timestamp),
		})
		comp.UpdateContent(v, isStreaming)
		chat.AddChild(comp)

		m.mu.Lock()
		m.streamingComponent = comp
		m.streamingMessage = v
		m.mu.Unlock()
		m.imRequestRender()
	}
}

func (m *InteractiveMode) imevHandleMessageUpdate(msg agent.AgentMessage, assistantEvent ai.AssistantMessageEvent) {
	assistantMsg, ok := msg.(*ai.AssistantMessage)
	if !ok || assistantMsg == nil {
		return
	}

	m.mu.Lock()
	comp := m.streamingComponent
	chat := m.chatContainer
	m.mu.Unlock()

	if comp == nil {
		m.imevHandleMessageStart(assistantMsg)
		m.mu.Lock()
		comp = m.streamingComponent
		m.mu.Unlock()
		if comp == nil {
			return
		}
	}

	m.mu.Lock()
	m.streamingMessage = assistantMsg
	m.mu.Unlock()

	comp.UpdateContent(assistantMsg, true)

	// Create or update streaming tool components on partial tool calls.
	for _, block := range assistantMsg.Content {
		tc, ok := block.(*ai.ToolCall)
		if !ok || tc == nil || tc.Id == "" {
			continue
		}
		m.mu.Lock()
		toolComp := m.pendingTools[tc.Id]
		m.mu.Unlock()

		if toolComp == nil && chat != nil {
			toolComp = m.imtrCreateToolExecutionComponent(tc.Name, tc.Id, tc.Arguments)
			if toolComp != nil {
				chat.AddChild(toolComp)
				m.mu.Lock()
				if m.pendingTools == nil {
					m.pendingTools = make(map[string]*components.ToolExecutionComponent)
				}
				m.pendingTools[tc.Id] = toolComp
				m.pendingToolsOrder = append(m.pendingToolsOrder, tc.Id)
				m.mu.Unlock()
			}
		} else if toolComp != nil {
			toolComp.UpdateArgs(tc.Arguments)
		}
	}

	if tcEnd, ok := assistantEvent.(*ai.AssistantMessageEventToolcallEnd); ok && tcEnd != nil && tcEnd.ToolCall != nil {
		m.mu.Lock()
		toolComp := m.pendingTools[tcEnd.ToolCall.Id]
		m.mu.Unlock()
		if toolComp != nil {
			toolComp.UpdateArgs(tcEnd.ToolCall.Arguments)
			toolComp.SetArgsComplete()
		}
	}

	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleMessageEnd(msg agent.AgentMessage) {
	assistantMsg, ok := msg.(*ai.AssistantMessage)
	if !ok || assistantMsg == nil {
		return
	}

	m.mu.Lock()
	comp := m.streamingComponent
	m.streamingComponent = nil
	m.streamingMessage = nil
	m.mu.Unlock()

	if comp != nil {
		comp.UpdateContent(assistantMsg, false)
	}

	// Mark any tool calls emitted without prior tool_execution_start as complete.
	for _, block := range assistantMsg.Content {
		tc, ok := block.(*ai.ToolCall)
		if !ok || tc == nil {
			continue
		}
		m.mu.Lock()
		toolComp := m.pendingTools[tc.Id]
		m.mu.Unlock()
		if toolComp != nil {
			toolComp.UpdateArgs(tc.Arguments)
			toolComp.SetArgsComplete()
		}
	}

	m.imevCheckAssistantPostConditions(assistantMsg)
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleToolExecutionStart(ev *core.AgentSessionEventToolExecutionStart) {
	if ev == nil {
		return
	}
	argsObj := imtrToJsonObject(ev.Args)

	m.mu.Lock()
	toolComp := m.pendingTools[ev.ToolCallId]
	chat := m.chatContainer
	m.mu.Unlock()

	if toolComp == nil && chat != nil {
		toolComp = m.imtrCreateToolExecutionComponent(ev.ToolName, ev.ToolCallId, argsObj)
		if toolComp != nil {
			chat.AddChild(toolComp)
			m.mu.Lock()
			if m.pendingTools == nil {
				m.pendingTools = make(map[string]*components.ToolExecutionComponent)
			}
			m.pendingTools[ev.ToolCallId] = toolComp
			m.pendingToolsOrder = append(m.pendingToolsOrder, ev.ToolCallId)
			m.mu.Unlock()
		}
	} else if toolComp != nil {
		toolComp.UpdateArgs(argsObj)
	}

	if toolComp != nil {
		toolComp.SetArgsComplete()
		toolComp.MarkExecutionStarted()
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleToolExecutionUpdate(ev *core.AgentSessionEventToolExecutionUpdate) {
	if ev == nil {
		return
	}
	m.mu.Lock()
	toolComp := m.pendingTools[ev.ToolCallId]
	m.mu.Unlock()
	if toolComp == nil {
		return
	}
	res := imevToToolExecutionResult(ev.PartialResult, false, nil)
	toolComp.UpdateResult(res, true)
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleToolExecutionEnd(ev *core.AgentSessionEventToolExecutionEnd) {
	if ev == nil {
		return
	}
	m.mu.Lock()
	toolComp := m.pendingTools[ev.ToolCallId]
	delete(m.pendingTools, ev.ToolCallId)
	m.mu.Unlock()

	if toolComp == nil {
		return
	}
	res := imevToToolExecutionResult(ev.Result, ev.IsError, ev.DurationMs)
	toolComp.UpdateResult(res, false)
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleAgentEnd(ev *core.AgentSessionEventAgentEnd) {
	m.imlcClearWorkingStatusIndicator()

	if ev == nil || !ev.WillRetry {
		m.mu.Lock()
		m.streamingComponent = nil
		m.streamingMessage = nil
		m.pendingTools = make(map[string]*components.ToolExecutionComponent)
		m.pendingToolsOrder = nil
		m.mu.Unlock()
	}

	m.imlcUpdateEditorBorderColor()
	m.imlcRefreshFooter()
	m.imcReportProgramStatus()
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleAgentSettled(_ *core.AgentSessionEventAgentSettled) {
	m.imlcClearWorkingStatusIndicator()
	m.imlcUpdateEditorBorderColor()
	m.imlcRefreshFooterSync(false)
	m.imcReportProgramStatus()
	m.imRequestRender()
}

func (m *InteractiveMode) imevHandleEntryAppended(entry core.SessionEntry) {
	customEntry, ok := entry.(*core.CustomEntry)
	if !ok || customEntry == nil {
		return
	}
	runner := m.imExtensionRunner()
	if runner == nil {
		return
	}
	entryRenderer := runner.GetEntryRenderer(customEntry.CustomType)
	if entryRenderer == nil {
		return
	}

	m.mu.Lock()
	chat := m.chatContainer
	expanded := m.toolExpanded
	m.mu.Unlock()
	if chat == nil {
		return
	}

	comp := components.NewCustomEntryComponent(customEntry, entryRenderer, m.imOutputPad())
	if comp.HasContent() {
		comp.SetExpanded(expanded)
		chat.AddChild(comp)
		m.imRequestRender()
	}
}

func (m *InteractiveMode) imevCheckAssistantPostConditions(msg *ai.AssistantMessage) {
	if msg == nil {
		return
	}

	// Surface assistant errors clearly unless auto-retry will recover transparently.
	if msg.StopReason == ai.StopReasonError || msg.StopReason == ai.StopReasonAborted {
		errText := imhDerefStr(msg.ErrorMessage)
		if errText != "" && !ai.IsRetryableAssistantError(msg) {
			m.ShowError(errText)
		}
		m.imevMaybeWarnAnthropicSubscription(msg)
	}
}

func (m *InteractiveMode) imevMaybeWarnAnthropicSubscription(msg *ai.AssistantMessage) {
	if msg == nil || string(msg.Provider) != "anthropic" {
		return
	}

	m.mu.Lock()
	alreadyShown := m.hasShownAnthropicWarning
	m.mu.Unlock()
	if alreadyShown {
		return
	}

	errText := strings.ToLower(imhDerefStr(msg.ErrorMessage))
	if errText == "" {
		return
	}
	if !strings.Contains(errText, "429") &&
		!strings.Contains(errText, "rate limit") &&
		!strings.Contains(errText, "overloaded") &&
		!strings.Contains(errText, "usage limit") &&
		!strings.Contains(errText, "subscription") {
		return
	}

	session := m.imSession()
	mr := m.imModelRuntime()
	if session == nil || mr == nil {
		return
	}
	model := session.Model()
	if model == nil || !mr.IsUsingOAuth(string(model.Provider)) {
		return
	}

	m.mu.Lock()
	m.hasShownAnthropicWarning = true
	m.mu.Unlock()

	m.ShowWarning("Anthropic OAuth/subscription request was rate-limited or rejected. Consider switching to an API key or another model with /model.")
}

func imevToToolExecutionResult(res any, isError bool, durationMs *int64) components.ToolExecutionResult {
	out := components.ToolExecutionResult{
		IsError:    isError,
		DurationMs: durationMs,
	}
	switch v := res.(type) {
	case nil:
		return out
	case components.ToolExecutionResult:
		v.IsError = v.IsError || isError
		if v.DurationMs == nil {
			v.DurationMs = durationMs
		}
		return v
	case *components.ToolExecutionResult:
		if v == nil {
			return out
		}
		cp := *v
		cp.IsError = cp.IsError || isError
		if cp.DurationMs == nil {
			cp.DurationMs = durationMs
		}
		return cp
	case agent.AgentToolResult:
		out.Content = v.Content
		out.Details = v.Details
		if v.IsError != nil && *v.IsError {
			out.IsError = true
		}
		return out
	case *agent.AgentToolResult:
		if v == nil {
			return out
		}
		out.Content = v.Content
		out.Details = v.Details
		if v.IsError != nil && *v.IsError {
			out.IsError = true
		}
		return out
	default:
		out.Content = ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("%v", res)}}
		return out
	}
}
