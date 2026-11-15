package interactive

import (
	"context"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type imqFlushOptions struct {
	WillRetry bool
}

func (m *InteractiveMode) imqUpdatePendingMessagesDisplay() {
	m.mu.Lock()
	pending := m.pendingMessagesContainer
	steering := make([]string, len(m.queuedSteering))
	copy(steering, m.queuedSteering)
	followUps := make([]string, len(m.queuedFollowUps))
	copy(followUps, m.queuedFollowUps)
	compactionQueued := make([]imCompactionQueuedMessage, len(m.compactionQueuedMessages))
	copy(compactionQueued, m.compactionQueuedMessages)
	kb := m.keybindings
	m.mu.Unlock()

	if pending == nil {
		return
	}

	pending.Clear()

	var lines []string
	for _, msg := range compactionQueued {
		prefix := "Steering"
		if msg.Mode == "followUp" {
			prefix = "Follow-up"
		}
		lines = append(lines, prefix+": "+imqCompactPreviewLine(msg.Text))
	}
	for _, text := range steering {
		lines = append(lines, "Steering: "+imqCompactPreviewLine(text))
	}
	for _, text := range followUps {
		lines = append(lines, "Follow-up: "+imqCompactPreviewLine(text))
	}

	if len(lines) == 0 {
		return
	}

	th := theme.CurrentTheme()
	pad := m.imOutputPad()
	pending.AddChild(tui.NewSpacer(1))
	for _, line := range lines {
		pending.AddChild(tui.NewText(th.Fg(theme.FgMuted, line), pad, 0, nil))
	}

	hintKey := imhAppKey(th, kb, core.KeybindingAppMessageDequeue)
	hint := th.Fg(theme.FgDim, "↳ "+hintKey+" "+th.Fg(theme.FgDim, "to edit all queued messages"))
	pending.AddChild(tui.NewText(hint, pad, 0, nil))
}

func imqCompactPreviewLine(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if len(collapsed) > 120 {
		return collapsed[:117] + "..."
	}
	return collapsed
}

func (m *InteractiveMode) imqQueueCompactionMessage(text string, mode string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if mode != "followUp" {
		mode = "steer"
	}

	m.mu.Lock()
	m.compactionQueuedMessages = append(m.compactionQueuedMessages, imCompactionQueuedMessage{
		Text: text,
		Mode: mode,
	})
	m.mu.Unlock()

	m.imqUpdatePendingMessagesDisplay()
	m.imRequestRender()
}

func (m *InteractiveMode) imqFlushCompactionQueue(options *imqFlushOptions) {
	m.mu.Lock()
	queued := m.compactionQueuedMessages
	m.compactionQueuedMessages = nil
	m.mu.Unlock()

	m.imqUpdatePendingMessagesDisplay()
	if len(queued) == 0 {
		return
	}

	session := m.imSession()
	if session == nil {
		return
	}

	willRetry := options != nil && options.WillRetry
	if willRetry {
		for _, item := range queued {
			if item.Mode == "followUp" {
				_, _ = session.FollowUp(m.bgCtx, item.Text, nil, &core.QueueInputOptions{Source: core.InputSourceInteractive})
			} else {
				_, _ = session.Steer(m.bgCtx, item.Text, nil, &core.QueueInputOptions{Source: core.InputSourceInteractive})
			}
		}
		return
	}

	var steering []string
	var followUps []string
	for _, item := range queued {
		if item.Mode == "followUp" {
			followUps = append(followUps, item.Text)
		} else {
			steering = append(steering, item.Text)
		}
	}

	// Drain into prompt/queue off the UI thread so Prompt() never blocks TUI callbacks.
	m.imSpawn(func() {
		if m.imIsStopped() {
			return
		}
		if len(steering) > 0 {
			for _, f := range followUps {
				_, _ = session.FollowUp(m.bgCtx, f, nil, &core.QueueInputOptions{Source: core.InputSourceInteractive})
			}
			combined := strings.Join(steering, "\n\n")
			m.imcBeginSessionMutation()
			err := session.Prompt(m.bgCtx, combined, &core.PromptOptions{Source: core.InputSourceInteractive})
			m.imcEndSessionMutation()
			if err != nil {
				m.imPostUI(func() {
					m.ShowError(imhErrorMessage(err))
				})
			}
			return
		}
		if len(followUps) > 0 {
			combined := strings.Join(followUps, "\n\n")
			m.imcBeginSessionMutation()
			err := session.Prompt(m.bgCtx, combined, &core.PromptOptions{Source: core.InputSourceInteractive})
			m.imcEndSessionMutation()
			if err != nil {
				m.imPostUI(func() {
					m.ShowError(imhErrorMessage(err))
				})
			}
		}
	})
}

func (m *InteractiveMode) imqHandleDequeue() {
	session := m.imSession()
	var queuedSteering []string
	var queuedFollowUp []string
	if session != nil {
		cleared := session.ClearQueue()
		queuedSteering = cleared.Steering
		queuedFollowUp = cleared.FollowUp
	}

	m.mu.Lock()
	compactionQueued := m.compactionQueuedMessages
	m.compactionQueuedMessages = nil
	m.queuedSteering = nil
	m.queuedFollowUps = nil
	ed := m.editor
	m.mu.Unlock()

	var all []string
	for _, msg := range compactionQueued {
		if strings.TrimSpace(msg.Text) != "" {
			all = append(all, msg.Text)
		}
	}
	for _, s := range queuedSteering {
		if strings.TrimSpace(s) != "" {
			all = append(all, s)
		}
	}
	for _, f := range queuedFollowUp {
		if strings.TrimSpace(f) != "" {
			all = append(all, f)
		}
	}

	m.imqUpdatePendingMessagesDisplay()
	if len(all) == 0 || ed == nil {
		m.imlcUpdateEditorBorderColor()
		m.imRequestRender()
		return
	}

	queuedText := strings.Join(all, "\n\n")
	existing := ed.GetText()
	if strings.TrimSpace(existing) != "" {
		ed.SetText(queuedText + "\n\n" + existing)
	} else {
		ed.SetText(queuedText)
	}

	m.imlcUpdateEditorBorderColor()
	m.imRequestRender()
}

func (m *InteractiveMode) imqHandleFollowUpSubmission(ctx context.Context, text string) {
	session := m.imSession()
	if session == nil {
		return
	}
	if ctx == nil {
		ctx = m.bgCtx
	}

	if session.IsCompacting() {
		m.imPostUI(func() {
			m.imqQueueCompactionMessage(text, "followUp")
		})
		return
	}

	if session.IsStreaming() {
		if _, err := session.FollowUp(ctx, text, nil, &core.QueueInputOptions{Source: core.InputSourceInteractive}); err != nil {
			m.imPostUI(func() {
				m.ShowError(imhErrorMessage(err))
			})
		}
		return
	}

	m.imcBeginSessionMutation()
	err := session.Prompt(ctx, text, &core.PromptOptions{Source: core.InputSourceInteractive})
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(imhErrorMessage(err))
		})
	}
}
