// Session event subscription, GUI thread marshaling, and GUI-backed ExtensionUIContext
// implementation for codingagent/modes/gui.
package gui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func gevSubscribeSession(m *GuiMode, sess *core.AgentSession) func() {
	if m == nil || sess == nil {
		return nil
	}
	return sess.Subscribe(func(ev core.AgentSessionEvent) {
		if ev == nil {
			return
		}
		m.postUI(func() {
			gevHandleSessionEvent(m, ev)
		})
	})
}

func gevHandleSessionEvent(m *GuiMode, rawEv core.AgentSessionEvent) {
	if m == nil || rawEv == nil {
		return
	}
	sess := m.Session()
	th := m.currentGuiTheme()

	switch ev := rawEv.(type) {
	case *core.AgentSessionEventAgentStart:
		m.mu.Lock()
		msg := m.workingMessage
		visible := m.workingVisible
		m.mu.Unlock()
		if visible && m.statusBanner != nil {
			m.statusBanner.SetWorking(true, msg)
		}
		m.updateProgramStatus(gui.StatusWorking, msg)
		m.updateFooterAndTitle()

	case *core.AgentSessionEventTurnStart:
		m.mu.Lock()
		msg := m.workingMessage
		visible := m.workingVisible
		m.mu.Unlock()
		if visible && m.statusBanner != nil {
			m.statusBanner.SetWorking(true, msg)
		}
		m.updateFooterAndTitle()

	case *core.AgentSessionEventTurnEnd:
		m.updateFooterAndTitle()

	case *core.AgentSessionEventMessageStart:
		gevHandleMessageStart(m, sess, th, ev.Message)

	case *core.AgentSessionEventMessageUpdate:
		gevHandleMessageUpdate(m, ev.Message)

	case *core.AgentSessionEventMessageEnd:
		gevHandleMessageEnd(m, sess, ev.Message)

	case *core.AgentSessionEventToolExecutionStart:
		gevHandleToolStart(m, sess, th, ev)

	case *core.AgentSessionEventToolExecutionUpdate:
		gevHandleToolUpdate(m, ev)

	case *core.AgentSessionEventToolExecutionEnd:
		gevHandleToolEnd(m, ev)

	case *core.AgentSessionEventAgentEnd:
		m.mu.Lock()
		m.streamingCard = nil
		m.streamingEntryID = ""
		m.mu.Unlock()
		m.updateFooterAndTitle()
		if !ev.WillRetry {
			if m.statusBanner != nil && sess != nil && !sess.IsBashRunning() && !sess.IsCompacting() {
				m.statusBanner.SetWorking(false, "")
			}
			m.updateProgramStatus(gui.StatusDone, "")
		}

	case *core.AgentSessionEventAgentSettled:
		if m.statusBanner != nil && (sess == nil || (!sess.IsBashRunning() && !sess.IsCompacting())) {
			m.statusBanner.SetWorking(false, "")
		}
		m.syncProgramStatusFromSession()
		m.updateFooterAndTitle()

	case *core.AgentSessionEventQueueUpdate:
		m.mu.Lock()
		m.steeringQueue = append([]string(nil), ev.Steering...)
		m.followUpQueue = append([]string(nil), ev.FollowUp...)
		m.mu.Unlock()
		if m.queueBanner != nil {
			m.queueBanner.SetQueues(ev.Steering, ev.FollowUp)
		}
		m.updateFooterAndTitle()

	case *core.AgentSessionEventEntryAppended:
		m.updateFooterAndTitle()

	case *core.AgentSessionEventSessionInfoChanged:
		m.updateFooterAndTitle()

	case *core.AgentSessionEventThinkingLevelChanged:
		m.updateFooterAndTitle()

	case *core.AgentSessionEventCompactionStart:
		m.mu.Lock()
		m.compacting = true
		m.mu.Unlock()
		if m.statusBanner != nil {
			m.statusBanner.SetCompacting(true)
		}
		m.updateProgramStatus(gui.StatusWorking, "Compacting context...")
		m.updateFooterAndTitle()

	case *core.AgentSessionEventCompactionEnd:
		m.mu.Lock()
		m.compacting = false
		expanded := m.toolsExpanded
		m.mu.Unlock()
		if m.statusBanner != nil {
			m.statusBanner.SetCompacting(false)
		}
		if ev.Aborted {
			m.ShowWarning("Compaction", "Context compaction aborted")
		} else if ev.ErrorMessage != nil && strings.TrimSpace(*ev.ErrorMessage) != "" {
			m.ShowError("Compaction failed: " + *ev.ErrorMessage)
		} else if ev.Result != nil {
			if sess != nil && sess.SessionManager != nil && !sess.IsStreaming() {
				m.rebuildTranscript()
			} else {
				cmsg := &core.CompactionSummaryMessage{
					Role:         "compactionSummary",
					Summary:      ev.Result.Summary,
					TokensBefore: ev.Result.TokensBefore,
					Timestamp:    time.Now().UnixMilli(),
				}
				card := NewCompactionSummaryCard(cmsg, WithCardTheme(th), WithCardExpanded(expanded), WithCardOnLink(gmOpenLink))
				m.appendCard("compaction", m.nextEntryID("compaction"), card)
			}
		}
		m.syncProgramStatusFromSession()
		m.updateFooterAndTitle()

	case *core.AgentSessionEventAutoRetryStart:
		delay := time.Duration(ev.DelayMs) * time.Millisecond
		m.mu.Lock()
		m.retrying = true
		sc := m.streamingCard
		m.mu.Unlock()
		if sc != nil {
			sc.SetRetryStatus(ev.Attempt, ev.MaxAttempts, delay, ev.ErrorMessage)
		}
		if m.statusBanner != nil {
			m.statusBanner.SetRetrying(ev.Attempt, ev.MaxAttempts, delay, ev.ErrorMessage)
		}
		m.updateProgramStatus(gui.StatusWorking, fmt.Sprintf("Retrying (%d/%d)...", ev.Attempt, ev.MaxAttempts))
		m.updateFooterAndTitle()

	case *core.AgentSessionEventAutoRetryEnd:
		m.mu.Lock()
		m.retrying = false
		sc := m.streamingCard
		m.mu.Unlock()
		if sc != nil {
			sc.ClearRetryStatus()
		}
		if m.statusBanner != nil {
			m.statusBanner.SetRetrying(0, 0, 0, "")
		}
		if !ev.Success && ev.FinalError != nil && strings.TrimSpace(*ev.FinalError) != "" {
			m.ShowError(fmt.Sprintf("Retry failed after %d attempts: %s", ev.Attempt, *ev.FinalError))
		}
		m.syncProgramStatusFromSession()
		m.updateFooterAndTitle()

	case *core.AgentSessionEventSummarizationRetryScheduled:
		delay := time.Duration(ev.DelayMs) * time.Millisecond
		if m.statusBanner != nil {
			m.statusBanner.SetRetrying(ev.Attempt, ev.MaxAttempts, delay, ev.ErrorMessage)
		}

	case *core.AgentSessionEventSummarizationRetryAttemptStart:
		reason := "Retrying summarization..."
		if ev.Reason != nil && strings.TrimSpace(*ev.Reason) != "" {
			reason = *ev.Reason
		}
		if m.statusBanner != nil {
			m.statusBanner.SetWorking(true, reason)
		}

	case *core.AgentSessionEventSummarizationRetryFinished:
		if m.statusBanner != nil {
			m.statusBanner.SetRetrying(0, 0, 0, "")
		}
		m.syncProgramStatusFromSession()

	case *core.AgentSessionEventBashExecutionUpdate:
		m.mu.Lock()
		bc := m.bashCard
		m.mu.Unlock()
		if bc != nil && ev.Delta != "" {
			bc.AppendOutput(ev.Delta)
			if m.host != nil {
				m.host.RequestRender()
			}
		}
	}
}

func gevHandleMessageStart(m *GuiMode, sess *core.AgentSession, th *gui.GuiTheme, raw agent.AgentMessage) {
	if raw == nil {
		return
	}

	m.mu.Lock()
	expanded := m.toolsExpanded
	hideThinking := m.hideThinking
	thinkingExpanded := m.thinkingExpanded
	hiddenLabel := m.hiddenThinkingLabel
	customRenderers := m.customMessageRenderers
	m.mu.Unlock()

	switch msg := raw.(type) {
	case *ai.UserMessage:
		txt, imgs := gevExtractUserContent(msg.Content)
		if sb := core.ParseSkillBlock(txt); sb != nil {
			skCard := NewSkillInvocationCard(*sb,
				WithCardTheme(th),
				WithCardExpanded(expanded),
				WithCardOnLink(gmOpenLink),
			)
			m.appendCard("skill", m.nextEntryID(fmt.Sprintf("skill:%d", msg.Timestamp)), skCard)
			if sb.UserMessage != nil && strings.TrimSpace(*sb.UserMessage) != "" {
				uc := NewUserMessageCard(*sb.UserMessage,
					WithUserImages(imgs),
					WithUserTimestamp(msg.Timestamp),
					WithUserTheme(th),
					WithUserOnLink(gmOpenLink),
				)
				m.appendCard("user", m.nextEntryID(fmt.Sprintf("user:%d", msg.Timestamp)), uc)
			}
			return
		}
		uc := NewUserMessageCard(txt,
			WithUserImages(imgs),
			WithUserTimestamp(msg.Timestamp),
			WithUserTheme(th),
			WithUserOnLink(gmOpenLink),
		)
		m.appendCard("user", m.nextEntryID(fmt.Sprintf("user:%d", msg.Timestamp)), uc)

	case *ai.AssistantMessage:
		aid := m.nextEntryID(fmt.Sprintf("assistant:%d", msg.Timestamp))
		ac := NewAssistantMessageCard(msg,
			WithAssistantStreaming(true),
			WithAssistantHideThinking(hideThinking),
			WithAssistantThinkingExpanded(thinkingExpanded),
			WithAssistantHiddenThinkingLabel(hiddenLabel),
			WithAssistantShowUsage(true),
			WithAssistantTheme(th),
			WithAssistantOnLink(gmOpenLink),
			WithAssistantOnCopyMessage(func(s string) {
				_ = gmWriteClipboardText(m.host, s)
			}),
			WithAssistantOnCopyCode(func(_, code string) {
				_ = gmWriteClipboardText(m.host, code)
			}),
		)
		m.mu.Lock()
		if prev := m.streamingCard; prev != nil {
			prev.SetStreaming(false)
		}
		m.streamingCard = ac
		m.streamingEntryID = aid
		m.mu.Unlock()
		m.appendCard("assistant", aid, ac)
		m.updateFooterAndTitle()

	case *core.BashExecutionMessage:
		m.mu.Lock()
		active := m.bashCard
		m.mu.Unlock()
		if active != nil && active.Command() == msg.Command {
			active.SetFromMessage(msg)
			return
		}
		excl := msg.ExcludeFromContext != nil && *msg.ExcludeFromContext
		bc := NewBashExecutionCard(msg.Command, excl, WithBashExpanded(expanded), WithBashTheme(th))
		bc.SetFromMessage(msg)
		m.appendCard("bash", m.nextEntryID(fmt.Sprintf("bash:%d", msg.Timestamp)), bc)

	case *core.CompactionSummaryMessage:
		cc := NewCompactionSummaryCard(msg, WithCardExpanded(expanded), WithCardTheme(th), WithCardOnLink(gmOpenLink))
		m.appendCard("compaction", m.nextEntryID(fmt.Sprintf("compaction:%d", msg.Timestamp)), cc)

	case *core.BranchSummaryMessage:
		bsc := NewBranchSummaryCard(msg, WithCardExpanded(expanded), WithCardTheme(th), WithCardOnLink(gmOpenLink))
		m.appendCard("branch", m.nextEntryID(fmt.Sprintf("branch:%d", msg.Timestamp)), bsc)

	case *core.CustomMessage:
		if !msg.Display {
			return
		}
		var customFn CustomMessageRenderer
		if customRenderers != nil {
			customFn = customRenderers[msg.CustomType]
		}
		cmc := NewCustomMessageCard(msg,
			WithCustomRenderer(customFn),
			WithCustomExpanded(expanded),
			WithCustomTheme(th),
			WithCustomOnLink(gmOpenLink),
		)
		m.appendCard("custom", m.nextEntryID(fmt.Sprintf("custom:%d", msg.Timestamp)), cmc)
	}
	_ = sess
}

func gevHandleMessageUpdate(m *GuiMode, raw agent.AgentMessage) {
	am, ok := raw.(*ai.AssistantMessage)
	if !ok || am == nil {
		return
	}
	m.mu.Lock()
	sc := m.streamingCard
	m.mu.Unlock()
	if sc != nil {
		sc.Update(am)
		if m.scrollView != nil && !m.scrollView.Locked() {
			m.scrollView.JumpToBottom()
		}
		if m.host != nil {
			m.host.RequestRender()
		}
	}
}

func gevHandleMessageEnd(m *GuiMode, sess *core.AgentSession, raw agent.AgentMessage) {
	if am, ok := raw.(*ai.AssistantMessage); ok && am != nil {
		m.mu.Lock()
		sc := m.streamingCard
		m.streamingCard = nil
		m.streamingEntryID = ""
		m.mu.Unlock()
		if sc != nil {
			sc.Update(am)
			sc.SetStreaming(false)
			sc.ClearRetryStatus()
		}
		if am.StopReason == "error" && am.ErrorMessage != nil && strings.TrimSpace(*am.ErrorMessage) != "" {
			if m.statusBanner != nil {
				m.statusBanner.SetCustomNotice(*am.ErrorMessage, true)
			}
			m.updateProgramStatus(gui.StatusError, *am.ErrorMessage)
		}
		m.updateFooterAndTitle()
		if m.host != nil {
			m.host.RequestRender()
		}
	}
	_ = sess
}

func gevHandleToolStart(m *GuiMode, sess *core.AgentSession, th *gui.GuiTheme, ev *core.AgentSessionEventToolExecutionStart) {
	if ev == nil {
		return
	}
	cwd := gmRuntimeCwd(m.runtime)
	showImages := true
	if sess != nil {
		cwd = sess.Cwd()
		if sess.SettingsManager != nil {
			showImages = sess.SettingsManager.GetShowImages()
		}
	}

	m.mu.Lock()
	existing := m.streamingTools[ev.ToolCallId]
	expanded := m.toolsExpanded
	customRenderer := m.toolRenderers[ev.ToolName]
	m.mu.Unlock()

	argsObj := gevAnyToJSONObject(ev.Args)
	if existing != nil {
		if argsObj != nil {
			existing.UpdateArgs(argsObj)
		}
		existing.SetArgsComplete(true)
		return
	}

	card := NewToolExecutionCard(ev.ToolCallId, ev.ToolName, argsObj,
		WithToolCwd(cwd),
		WithToolExpanded(expanded),
		WithToolShowImages(showImages),
		WithToolTheme(th),
		WithToolCustomRenderer(customRenderer),
		WithToolOnCopy(func(s string) {
			_ = gmWriteClipboardText(m.host, s)
		}),
	)
	card.SetArgsComplete(true)

	m.mu.Lock()
	m.streamingTools[ev.ToolCallId] = card
	m.mu.Unlock()
	m.appendCard("tool", "tool:"+ev.ToolCallId, card)
}

func gevHandleToolUpdate(m *GuiMode, ev *core.AgentSessionEventToolExecutionUpdate) {
	if ev == nil {
		return
	}
	m.mu.Lock()
	card := m.streamingTools[ev.ToolCallId]
	m.mu.Unlock()
	if card == nil {
		return
	}
	if argsObj := gevAnyToJSONObject(ev.Args); argsObj != nil {
		card.UpdateArgs(argsObj)
	}
	card.UpdateResult(gevAnyToToolResult(ev.PartialResult, false), true)
	if m.host != nil {
		m.host.RequestRender()
	}
}

func gevHandleToolEnd(m *GuiMode, ev *core.AgentSessionEventToolExecutionEnd) {
	if ev == nil {
		return
	}
	m.mu.Lock()
	card := m.streamingTools[ev.ToolCallId]
	m.mu.Unlock()
	if card != nil {
		res := gevAnyToToolResult(ev.Result, ev.IsError)
		card.Complete(res, ev.IsError, ev.DurationMs)
	}
	m.updateFooterAndTitle()
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) syncProgramStatusFromSession() {
	sess := m.Session()
	if sess == nil {
		m.updateProgramStatus(gui.StatusIdle, "")
		return
	}
	if sess.IsCompacting() {
		m.updateProgramStatus(gui.StatusWorking, "Compacting context...")
		return
	}
	if sess.IsStreaming() || sess.IsBashRunning() {
		m.mu.Lock()
		msg := m.workingMessage
		m.mu.Unlock()
		m.updateProgramStatus(gui.StatusWorking, msg)
		return
	}
	m.updateProgramStatus(gui.StatusIdle, "")
}

func gevExtractUserContent(content ai.UserMessageContent) (string, []ai.ImageContent) {
	if content.Text != nil {
		return *content.Text, nil
	}
	var texts []string
	var imgs []ai.ImageContent
	for _, blk := range content.Blocks {
		switch b := blk.(type) {
		case *ai.TextContent:
			if b != nil && b.Text != "" {
				texts = append(texts, b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				imgs = append(imgs, *b)
			}
		}
	}
	return strings.Join(texts, "\n"), imgs
}

func gevAnyToJSONObject(raw any) *jsonx.Object {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case *jsonx.Object:
		return v
	case map[string]any:
		obj := jsonx.NewObject()
		for k, val := range v {
			obj.Set(k, val)
		}
		return obj
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{") {
			if parsed, err := jsonx.Parse([]byte(trimmed)); err == nil {
				if obj, ok := parsed.(*jsonx.Object); ok {
					return obj
				}
			}
		}
	}
	return nil
}

func gevAnyToToolResult(raw any, isError bool) agent.AgentToolResult {
	switch v := raw.(type) {
	case agent.AgentToolResult:
		return v
	case *agent.AgentToolResult:
		if v != nil {
			return *v
		}
	case string:
		errFlag := isError
		return agent.AgentToolResult{
			Content: ai.ContentList{&ai.TextContent{Type: "text", Text: v}},
			IsError: &errFlag,
		}
	}
	errFlag := isError
	return agent.AgentToolResult{IsError: &errFlag}
}

// ============================================================================
// ExtensionUIContext Implementation backed by GUI overlays & components
// ============================================================================

type gevExtensionUIContext struct {
	mode *GuiMode
}

var _ core.ExtensionUIContext = (*gevExtensionUIContext)(nil)

func gevNewExtensionUIContext(m *GuiMode) *gevExtensionUIContext {
	return &gevExtensionUIContext{mode: m}
}

func gevDialogContextAndTimeout(ctx context.Context, opts ...*core.ExtensionUIDialogOptions) (context.Context, time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	var timeout time.Duration
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Ctx != nil {
			ctx = opt.Ctx
		}
		if opt.Timeout != nil && *opt.Timeout > 0 {
			timeout = time.Duration(*opt.Timeout) * time.Millisecond
		}
	}
	return ctx, timeout
}

func gevWaitModal[T any](
	m *GuiMode,
	ctx context.Context,
	timeout time.Duration,
	attentionTitle string,
	fallback T,
	showFn func(resolve func(T)) *DialogHandle,
) (T, error) {
	if m == nil || m.host == nil {
		return fallback, nil
	}
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return fallback, nil
	}

	type box struct {
		val T
	}
	ch := make(chan box, 1)
	var once sync.Once
	resolve := func(v T) {
		once.Do(func() {
			ch <- box{val: v}
		})
	}

	var handleMu sync.Mutex
	var handle *DialogHandle

	m.updateProgramStatus(gui.StatusBlocked, attentionTitle, gui.AttentionPermission)
	defer m.syncProgramStatusFromSession()

	m.postUI(func() {
		dlg := showFn(resolve)
		handleMu.Lock()
		handle = dlg
		handleMu.Unlock()
		m.setActiveDialog(dlg)
	})

	var timer <-chan time.Time
	var tmr *time.Timer
	if timeout > 0 {
		tmr = time.NewTimer(timeout)
		defer tmr.Stop()
		timer = tmr.C
	}

	closeOverlay := func() {
		m.postUI(func() {
			handleMu.Lock()
			dlg := handle
			handleMu.Unlock()
			if dlg != nil {
				dlg.Close()
			}
		})
	}

	select {
	case res := <-ch:
		return res.val, nil
	case <-timer:
		closeOverlay()
		return fallback, nil
	case <-ctx.Done():
		closeOverlay()
		return fallback, nil
	case <-m.doneCh:
		closeOverlay()
		return fallback, nil
	}
}

func (u *gevExtensionUIContext) Select(ctx context.Context, title string, options []string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	if len(options) == 0 {
		return nil, nil
	}
	dctx, timeout := gevDialogContextAndTimeout(ctx, opts...)
	return gevWaitModal(u.mode, dctx, timeout, title, nil, func(resolve func(*string)) *DialogHandle {
		dlg := NewExtensionSelectDialog(ExtensionSelectConfig{
			Title:   title,
			Options: options,
			Theme:   u.mode.currentGuiTheme(),
			OnSelect: func(choice string) {
				cp := choice
				resolve(&cp)
			},
			OnCancel: func() {
				resolve(nil)
			},
		})
		return dlg.Show(u.mode.host)
	})
}

func (u *gevExtensionUIContext) Confirm(ctx context.Context, title, message string, opts ...*core.ExtensionUIDialogOptions) (bool, error) {
	dctx, timeout := gevDialogContextAndTimeout(ctx, opts...)
	return gevWaitModal(u.mode, dctx, timeout, title, false, func(resolve func(bool)) *DialogHandle {
		dlg := NewExtensionConfirmDialog(ExtensionConfirmConfig{
			Title:   title,
			Message: message,
			Theme:   u.mode.currentGuiTheme(),
			OnConfirm: func(confirmed bool) {
				resolve(confirmed)
			},
		})
		return dlg.Show(u.mode.host)
	})
}

func (u *gevExtensionUIContext) Input(ctx context.Context, title string, placeholder *string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	dctx, timeout := gevDialogContextAndTimeout(ctx, opts...)
	ph := ""
	if placeholder != nil {
		ph = *placeholder
	}
	return gevWaitModal(u.mode, dctx, timeout, title, nil, func(resolve func(*string)) *DialogHandle {
		dlg := NewExtensionInputDialog(ExtensionInputConfig{
			Title:       title,
			Placeholder: ph,
			Theme:       u.mode.currentGuiTheme(),
			OnSubmit: func(val string) {
				cp := val
				resolve(&cp)
			},
			OnCancel: func() {
				resolve(nil)
			},
		})
		return dlg.Show(u.mode.host)
	})
}

func (u *gevExtensionUIContext) Notify(message string, notifyType ...string) {
	level := gmNoticeInfo
	title := "Notice"
	if len(notifyType) > 0 {
		switch strings.ToLower(strings.TrimSpace(notifyType[0])) {
		case "warning", "warn":
			level = gmNoticeWarning
			title = "Warning"
		case "error", "err":
			level = gmNoticeError
			title = "Error"
		}
	}
	u.mode.postUI(func() {
		u.mode.appendNotice(level, title, message)
		if u.mode.statusBanner != nil && level == gmNoticeError {
			u.mode.statusBanner.SetCustomNotice(message, true)
		} else if u.mode.statusBanner != nil {
			u.mode.statusBanner.SetCustomNotice(message, false)
		}
	})
}

func (u *gevExtensionUIContext) OnTerminalInput(handler core.TerminalInputHandler) func() {
	if handler == nil {
		return func() {}
	}
	m := u.mode
	m.mu.Lock()
	idx := len(m.terminalInputHandlers)
	m.terminalInputHandlers = append(m.terminalInputHandlers, handler)
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if idx < len(m.terminalInputHandlers) {
			m.terminalInputHandlers[idx] = nil
		}
		m.mu.Unlock()
	}
}

func (u *gevExtensionUIContext) SetStatus(key string, text *string) {
	m := u.mode
	if m.footerProvider != nil {
		m.footerProvider.SetExtensionStatus(key, text)
	}
	m.postUI(func() {
		m.updateFooterAndTitle()
	})
}

func (u *gevExtensionUIContext) SetWorkingMessage(message ...string) {
	m := u.mode
	msg := ""
	if len(message) > 0 {
		msg = message[0]
	}
	m.mu.Lock()
	m.workingMessage = msg
	visible := m.workingVisible
	m.mu.Unlock()
	m.postUI(func() {
		if m.statusBanner != nil && visible {
			sess := m.Session()
			active := sess != nil && (sess.IsStreaming() || sess.IsBashRunning())
			if active || msg != "" {
				m.statusBanner.SetWorking(true, msg)
			}
		}
		if m.footer != nil {
			m.footer.SetWorkingMessage(msg)
		}
	})
}

func (u *gevExtensionUIContext) SetWorkingVisible(visible bool) {
	m := u.mode
	m.mu.Lock()
	m.workingVisible = visible
	msg := m.workingMessage
	m.mu.Unlock()
	m.postUI(func() {
		if m.statusBanner != nil {
			if !visible {
				m.statusBanner.SetWorking(false, "")
			} else if sess := m.Session(); sess != nil && sess.IsStreaming() {
				m.statusBanner.SetWorking(true, msg)
			}
		}
	})
}

func (u *gevExtensionUIContext) SetWorkingIndicator(options *core.WorkingIndicatorOptions) {
	if options == nil {
		return
	}
	m := u.mode
	m.mu.Lock()
	m.workingFrames = append([]string(nil), options.Frames...)
	m.mu.Unlock()
}

func (u *gevExtensionUIContext) SetHiddenThinkingLabel(label ...string) {
	m := u.mode
	val := ""
	if len(label) > 0 {
		val = label[0]
	}
	m.mu.Lock()
	m.hiddenThinkingLabel = val
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	m.mu.Unlock()
	m.postUI(func() {
		for _, e := range entries {
			if ac, ok := e.card.(*AssistantMessageCard); ok {
				ac.SetHiddenThinkingLabel(val)
			}
		}
	})
}

func (u *gevExtensionUIContext) SetWidgetLines(key string, content []string, options ...*core.ExtensionWidgetOptions) {
	placement := core.WidgetPlacementAboveEditor
	if len(options) > 0 && options[0] != nil && options[0].Placement != "" {
		placement = options[0].Placement
	}
	m := u.mode
	m.mu.Lock()
	m.widgets = gevUpsertWidget(m.widgets, gmWidgetItem{
		key:       key,
		placement: placement,
		lines:     append([]string(nil), content...),
	})
	m.mu.Unlock()
	m.postUI(func() {})
}

func (u *gevExtensionUIContext) SetWidgetFactory(key string, factory func(t tui.TUI, th *theme.Theme) tui.Component, options ...*core.ExtensionWidgetOptions) {
	placement := core.WidgetPlacementAboveEditor
	if len(options) > 0 && options[0] != nil && options[0].Placement != "" {
		placement = options[0].Placement
	}
	m := u.mode
	if factory == nil {
		m.mu.Lock()
		m.widgets = gevUpsertWidget(m.widgets, gmWidgetItem{key: key})
		m.mu.Unlock()
		m.postUI(func() {})
		return
	}
	comp := factory(nil, u.Theme())
	if comp == nil {
		return
	}
	lines := comp.Render(96)
	m.mu.Lock()
	m.widgets = gevUpsertWidget(m.widgets, gmWidgetItem{
		key:       key,
		placement: placement,
		lines:     lines,
	})
	m.mu.Unlock()
	m.postUI(func() {})
}

func (u *gevExtensionUIContext) SetFooter(factory func(t tui.TUI, th *theme.Theme, footerData core.ReadonlyFooterDataProvider) tui.Component) {
	m := u.mode
	banner := ""
	if factory != nil {
		if comp := factory(nil, u.Theme(), m.footerProvider); comp != nil {
			banner = strings.Join(comp.Render(100), "\n")
		}
	}
	m.mu.Lock()
	m.footerBanner = banner
	m.mu.Unlock()
	m.postUI(func() {})
}

func (u *gevExtensionUIContext) SetHeader(factory func(t tui.TUI, th *theme.Theme) tui.Component) {
	m := u.mode
	banner := ""
	if factory != nil {
		if comp := factory(nil, u.Theme()); comp != nil {
			banner = strings.Join(comp.Render(100), "\n")
		}
	}
	m.mu.Lock()
	m.headerBanner = banner
	m.mu.Unlock()
	m.postUI(func() {})
}

func (u *gevExtensionUIContext) SetTitle(title string) {
	m := u.mode
	m.mu.Lock()
	m.titleOverride = title
	m.mu.Unlock()
	m.postUI(func() {
		if m.host != nil {
			m.host.SetTitle(title)
		}
	})
}

func (u *gevExtensionUIContext) Custom(
	ctx context.Context,
	factory func(t tui.TUI, th *theme.Theme, keybindings *tui.KeybindingsManager, done func(any)) (tui.Component, error),
	options ...*core.ExtensionCustomOptions,
) (any, error) {
	if factory == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resCh := make(chan any, 1)
	var doneOnce sync.Once
	var ovHandle *gui.OverlayHandle
	var handleMu sync.Mutex

	doneFn := func(val any) {
		doneOnce.Do(func() {
			resCh <- val
			u.mode.postUI(func() {
				handleMu.Lock()
				ov := ovHandle
				handleMu.Unlock()
				if ov != nil {
					ov.Hide()
				}
			})
		})
	}

	comp, err := factory(nil, u.Theme(), nil, doneFn)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, nil
	}

	u.mode.postUI(func() {
		if u.mode.host == nil {
			return
		}
		ov := u.mode.host.ShowOverlay(func(c *ui.Context) {
			th := u.mode.currentGuiTheme()
			lines := comp.Render(90)
			panel := ui.Column(c).
				Background(th.Surface).
				Border(1, th.Border).
				Radius(th.Radius).
				Padding(12, 16).
				Gap(4)
			panel.Children(func() {
				for _, line := range lines {
					ui.Text(c, line).TextColor(th.Text).FontSize(13)
				}
				closeBtn := ui.Button(c, "Close").Cursor(ui.CursorPointer)
				if closeBtn.Clicked() {
					doneFn(nil)
				}
			})
		}, gui.OverlayOptions{
			MaxWidth: 720,
			OnDismiss: func() {
				doneFn(nil)
			},
		})
		handleMu.Lock()
		ovHandle = ov
		handleMu.Unlock()
	})

	select {
	case v := <-resCh:
		return v, nil
	case <-ctx.Done():
		doneFn(nil)
		return nil, ctx.Err()
	case <-u.mode.doneCh:
		doneFn(nil)
		return nil, nil
	}
}

func (u *gevExtensionUIContext) PasteToEditor(text string) {
	if u.mode.editor == nil {
		return
	}
	cur := u.mode.editor.Text()
	u.mode.editor.SetText(cur + text)
	if u.mode.host != nil {
		u.mode.host.RequestRender()
	}
}

func (u *gevExtensionUIContext) SetEditorText(text string) {
	if u.mode.editor == nil {
		return
	}
	u.mode.editor.SetText(text)
	if u.mode.host != nil {
		u.mode.host.RequestRender()
	}
}

func (u *gevExtensionUIContext) GetEditorText() string {
	if u.mode.editor == nil {
		return ""
	}
	return u.mode.editor.Text()
}

func (u *gevExtensionUIContext) Editor(ctx context.Context, title string, prefill ...string) (*string, error) {
	initText := ""
	if len(prefill) > 0 {
		initText = prefill[0]
	}
	dctx, timeout := gevDialogContextAndTimeout(ctx)
	return gevWaitModal(u.mode, dctx, timeout, title, nil, func(resolve func(*string)) *DialogHandle {
		dlg := NewExtensionEditorDialog(ExtensionEditorConfig{
			Title:       title,
			InitialText: initText,
			Theme:       u.mode.currentGuiTheme(),
			OnSubmit: func(text string) {
				cp := text
				resolve(&cp)
			},
			OnCancel: func() {
				resolve(nil)
			},
		})
		return dlg.Show(u.mode.host)
	})
}

func (u *gevExtensionUIContext) AddAutocompleteProvider(factory core.AutocompleteProviderFactory) {
	if factory == nil {
		return
	}
	m := u.mode
	m.mu.Lock()
	m.autocompleteFactories = append(m.autocompleteFactories, factory)
	m.mu.Unlock()
	m.postUI(func() {
		m.RefreshAutocomplete()
	})
}

func (u *gevExtensionUIContext) SetEditorComponent(factory core.EditorFactory) {
	m := u.mode
	m.mu.Lock()
	m.editorFactory = factory
	m.mu.Unlock()
}

func (u *gevExtensionUIContext) GetEditorComponent() core.EditorFactory {
	m := u.mode
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.editorFactory
}

func (u *gevExtensionUIContext) Theme() *theme.Theme {
	u.mode.mu.Lock()
	ts := u.mode.themeSet
	u.mode.mu.Unlock()
	if ts != nil && ts.Source != nil {
		return ts.Source
	}
	return theme.CurrentTheme()
}

func (u *gevExtensionUIContext) GetAllThemes() []core.ThemeInfo {
	items := theme.GetAvailableThemesWithPaths()
	out := make([]core.ThemeInfo, 0, len(items))
	for _, it := range items {
		var p *string
		if it.Path != "" {
			cp := it.Path
			p = &cp
		}
		out = append(out, core.ThemeInfo{
			Name: it.Name,
			Path: p,
		})
	}
	return out
}

func (u *gevExtensionUIContext) GetTheme(name string) *theme.Theme {
	th, err := theme.GetThemeByName(name)
	if err != nil {
		return nil
	}
	return th
}

func (u *gevExtensionUIContext) SetThemeByName(name string) core.SetThemeResult {
	res, ts := ApplyThemeToHost(u.mode.host, name)
	if !res.Success {
		errMsg := res.Error
		return core.SetThemeResult{Success: false, Error: &errMsg}
	}
	if ts != nil {
		u.mode.applyThemeSet(ts)
	}
	return core.SetThemeResult{Success: true}
}

func (u *gevExtensionUIContext) SetTheme(th *theme.Theme) core.SetThemeResult {
	if th == nil {
		errMsg := "nil theme"
		return core.SetThemeResult{Success: false, Error: &errMsg}
	}
	ts := ApplyThemeInstanceToHost(u.mode.host, th)
	if ts != nil {
		u.mode.applyThemeSet(ts)
	}
	return core.SetThemeResult{Success: true}
}

func (u *gevExtensionUIContext) GetToolsExpanded() bool {
	u.mode.mu.Lock()
	defer u.mode.mu.Unlock()
	return u.mode.toolsExpanded
}

func (u *gevExtensionUIContext) SetToolsExpanded(expanded bool) {
	u.mode.mu.Lock()
	u.mode.toolsExpanded = expanded
	entries := append([]gmTranscriptEntry(nil), u.mode.entries...)
	u.mode.mu.Unlock()
	u.mode.postUI(func() {
		for _, e := range entries {
			switch c := e.card.(type) {
			case *ToolExecutionCard:
				c.SetExpanded(expanded)
			case *BashExecutionCard:
				c.SetExpanded(expanded)
			case *CompactionSummaryCard:
				c.SetExpanded(expanded)
			case *BranchSummaryCard:
				c.SetExpanded(expanded)
			case *SkillInvocationCard:
				c.SetExpanded(expanded)
			case *CustomMessageCard:
				c.SetExpanded(expanded)
			}
		}
		if u.mode.host != nil {
			u.mode.host.RequestRender()
		}
	})
}

func gevUpsertWidget(items []gmWidgetItem, next gmWidgetItem) []gmWidgetItem {
	remove := len(next.lines) == 0 && next.render == nil
	out := make([]gmWidgetItem, 0, len(items)+1)
	replaced := false
	for _, it := range items {
		if it.key == next.key {
			replaced = true
			if !remove {
				out = append(out, next)
			}
			continue
		}
		out = append(out, it)
	}
	if !replaced && !remove {
		out = append(out, next)
	}
	return out
}
