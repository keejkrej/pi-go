// Ported from packages/coding-agent/src/experimental/durable/tui.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// tuiListSelector is a filterable list rendered in place of the editor.
type tuiListSelector struct {
	*tui.Container
	input         *tui.Input
	listContainer *tui.Container
	items         []tui.SelectItem
	onSelect      func(value string)
	onCancel      func()
	list          *tui.SelectList
	focused       bool
}

func tuiNewListSelector(title string, items []tui.SelectItem, onSelect func(value string), onCancel func()) *tuiListSelector {
	sel := &tuiListSelector{
		Container:     tui.NewContainer(),
		input:         tui.NewInput(nil),
		listContainer: tui.NewContainer(),
		items:         append([]tui.SelectItem(nil), items...),
		onSelect:      onSelect,
		onCancel:      onCancel,
	}
	sel.list = sel.build(sel.items)
	th := theme.CurrentTheme()
	sel.AddChild(components.NewDynamicBorder())
	sel.AddChild(tui.NewSpacer(1))
	sel.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0, nil))
	sel.AddChild(sel.input)
	sel.AddChild(tui.NewSpacer(1))
	sel.AddChild(sel.listContainer)
	sel.AddChild(components.NewDynamicBorder())
	return sel
}

func (s *tuiListSelector) Focused() bool {
	return s.focused
}

func (s *tuiListSelector) SetFocused(focused bool) {
	s.focused = focused
	s.input.SetFocused(focused)
}

func (s *tuiListSelector) HandleInput(data string) {
	kb := tui.GetKeybindings()
	forwarded := []tui.Keybinding{
		tui.KeybindingTuiSelectUp,
		tui.KeybindingTuiSelectDown,
		tui.KeybindingTuiSelectConfirm,
		tui.KeybindingTuiSelectCancel,
	}
	for _, action := range forwarded {
		if kb.Matches(data, action) {
			s.list.HandleInput(data)
			return
		}
	}
	s.input.HandleInput(data)
	query := s.input.GetValue()
	filtered := s.items
	if len(query) > 0 {
		filtered = tui.FuzzyFilter(s.items, query, func(item tui.SelectItem) string {
			return item.Label + " " + item.Value
		})
	}
	s.list = s.build(filtered)
}

func (s *tuiListSelector) build(items []tui.SelectItem) *tui.SelectList {
	list := tui.NewSelectList(items, 10, theme.GetSelectListTheme(), nil)
	list.OnSelect = func(item tui.SelectItem) {
		if s.onSelect != nil {
			s.onSelect(item.Value)
		}
	}
	list.OnCancel = func() {
		if s.onCancel != nil {
			s.onCancel()
		}
	}
	s.listContainer.Clear()
	s.listContainer.AddChild(list)
	return list
}

// tuiCompactionComponent displays the summary that replaced earlier context: collapsed to one line until expanded.
type tuiCompactionComponent struct {
	*tui.Box
	summary string
}

func tuiNewCompactionComponent(summary string, expanded bool) *tuiCompactionComponent {
	box := tui.NewBox(1, 1, func(text string) string {
		return theme.CurrentTheme().Bg(theme.BgCustomMessage, text)
	})
	comp := &tuiCompactionComponent{
		Box:     box,
		summary: summary,
	}
	comp.SetExpanded(expanded)
	return comp
}

func (c *tuiCompactionComponent) SetExpanded(expanded bool) {
	c.Clear()
	th := theme.CurrentTheme()
	c.AddChild(tui.NewText(th.Fg(theme.FgCustomMessageLabel, th.Bold("[compaction]")), 0, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	if expanded {
		mdTheme := theme.GetMarkdownTheme()
		c.AddChild(tui.NewMarkdown(c.summary, 0, 0, mdTheme, &tui.DefaultTextStyle{
			Color: func(text string) string {
				return th.Fg(theme.FgCustomMessageText, text)
			},
		}, nil))
	} else {
		msg := th.Fg(theme.FgCustomMessageText, "Earlier context summarized (") +
			th.Fg(theme.FgDim, theme.KeyText(core.KeybindingAppToolsExpand)) +
			th.Fg(theme.FgCustomMessageText, " to expand)")
		c.AddChild(tui.NewText(msg, 0, 0, nil))
	}
}

type tuiHandlers struct {
	submit        func(text string)
	followUp      func(text string)
	abort         func()
	exit          func()
	selectModel   func()
	cycleThinking func()
}

type tuiView struct {
	renderers       map[core.ToolName]core.ToolRenderers
	ui              *tui.TuiAltScreen
	chat            *tui.Container
	tasks           *tui.Container
	queue           *tui.Container
	notices         *tui.Container
	footer          *tui.Container
	footerStats     *tui.Text
	footerHints     *tui.Text
	editorContainer *tui.Container
	editor          *components.CustomEditor
	cwd             string
	tools           map[string]*components.ToolExecutionComponent
	cards           []*components.ToolExecutionComponent
	streamingCalls  map[string]bool
	summaries       []*tuiCompactionComponent
	expanded        bool
	renderedIds     []durable.EntryId
	streaming       *components.AssistantMessageComponent
	indicator       *components.WorkingStatusIndicator
	statusText      string
	rebuilt         bool
	transcript      *tui.ScrollView
}

func tuiNewView(cwd string, handlers tuiHandlers) (*tuiView, error) {
	showHardwareCursor := false
	agentDir := utils.GetAgentDir()
	ui := tui.NewTuiAltScreen(tui.NewProcessTerminal(), &showHardwareCursor, &agentDir, nil)
	keybindings := core.KeybindingsManagerCreate()
	tui.SetKeybindings(keybindings.KeybindingsManager)

	padX := 1
	embedWorking := true
	editor := components.NewCustomEditor(ui, theme.GetEditorTheme(), keybindings, &components.CustomEditorOptions{
		PaddingX:           &padX,
		EmbedWorkingStatus: &embedWorking,
	})

	view := &tuiView{
		renderers:       core.CreateAllToolRenderers(),
		ui:              ui,
		chat:            tui.NewContainer(),
		tasks:           tui.NewContainer(),
		queue:           tui.NewContainer(),
		notices:         tui.NewContainer(),
		footer:          tui.NewContainer(),
		footerStats:     tui.NewText("", 1, 0, nil),
		footerHints:     tui.NewText("", 1, 0, nil),
		editorContainer: tui.NewContainer(),
		editor:          editor,
		cwd:             cwd,
		tools:           make(map[string]*components.ToolExecutionComponent),
		streamingCalls:  make(map[string]bool),
	}

	editor.OnSubmit = handlers.submit
	editor.OnEscape = handlers.abort
	editor.OnCtrlD = handlers.exit
	editor.OnAction(core.KeybindingAppClear, handlers.exit)
	editor.OnAction(core.KeybindingAppModelSelect, handlers.selectModel)
	editor.OnAction(core.KeybindingAppThinkingCycle, handlers.cycleThinking)
	editor.OnAction(core.KeybindingAppToolsExpand, func() {
		view.expanded = !view.expanded
		for _, card := range view.cards {
			card.SetExpanded(view.expanded)
		}
		for _, summary := range view.summaries {
			summary.SetExpanded(view.expanded)
		}
		view.ui.RequestRender(nil)
	})
	editor.OnAction(core.KeybindingAppMessageFollowUp, func() {
		text := strings.TrimSpace(view.editor.GetText())
		if text == "" {
			return
		}
		view.editor.SetText("")
		handlers.followUp(text)
	})

	view.editorContainer.AddChild(view.editor)
	view.footer.AddChild(view.footerStats)
	view.footer.AddChild(view.footerHints)

	// One empty line between the transcript and everything below it.
	content := tui.NewContainer()
	content.AddChild(view.chat)
	content.AddChild(tui.NewSpacer(1))

	followEnd := tui.ScrollViewFollowEnd
	primary := true
	overscrollChain := tui.ScrollLayoutOverscrollChain
	transcript, err := tui.NewScrollView(content, &tui.ScrollViewOptions{
		Follow:     &followEnd,
		Primary:    &primary,
		Overscroll: &overscrollChain,
	})
	if err != nil {
		return nil, err
	}
	view.transcript = transcript

	zero := 0
	one := 1
	three := 3
	dock := tui.NewVStack([]tui.StackChild{
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component:         view.tasks,
			StackEntryOptions: tui.StackEntryOptions{Shrink: &one, MinSize: &zero},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component:         view.queue,
			StackEntryOptions: tui.StackEntryOptions{Shrink: &one, MinSize: &zero},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component:         view.notices,
			StackEntryOptions: tui.StackEntryOptions{Shrink: &one, MinSize: &zero},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component:         view.editorContainer,
			StackEntryOptions: tui.StackEntryOptions{Shrink: &one, MinSize: &three},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component:         view.footer,
			StackEntryOptions: tui.StackEntryOptions{Shrink: &one, MinSize: &zero},
		}},
	}, nil)

	for _, child := range []tui.Component{
		view.chat,
		view.tasks,
		view.queue,
		view.notices,
		view.editorContainer,
		view.footer,
	} {
		view.ui.AddChild(child)
	}
	view.ui.SetLayoutRoot(tui.NewVStack([]tui.StackChild{
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: transcript,
			StackEntryOptions: tui.StackEntryOptions{
				Basis:   &tui.StackLayoutBasisCells{Value: 0},
				Grow:    &one,
				Shrink:  &one,
				MinSize: &one,
			},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: dock,
			StackEntryOptions: tui.StackEntryOptions{
				Basis:   &tui.StackLayoutBasisAuto{},
				Grow:    &zero,
				Shrink:  &one,
				MinSize: &one,
			},
		}},
	}, nil))
	view.ui.SetFocus(view.editor)
	return view, nil
}

func (v *tuiView) start() {
	v.ui.Start()
}

func (v *tuiView) stop() {
	v.discardTools()
	if v.indicator != nil {
		v.indicator.Dispose()
		v.indicator = nil
	}
	v.ui.Stop(nil)
}

func (v *tuiView) discardTools() {
	for _, component := range v.cards {
		component.UpdateResult(components.ToolExecutionResult{IsError: false}, false)
	}
	v.cards = nil
	v.tools = make(map[string]*components.ToolExecutionComponent)
	v.streamingCalls = make(map[string]bool)
}

func (v *tuiView) mount(component tui.Component) {
	v.editorContainer.Clear()
	v.editorContainer.AddChild(component)
	v.ui.SetFocus(component)
	v.ui.RequestRender(nil)
}

func (v *tuiView) restoreEditor() {
	v.mount(v.editor)
}

func (v *tuiView) apply(view DurableView) {
	live := tuiLiveOf(view.Conversation)
	v.syncTranscript(view.Conversation.Entries)
	var message *ai.AssistantMessage
	if live.Generation != nil {
		message = live.Generation.Message
	}
	// A partial without its entry was dropped, for example by a retry: render the transcript again.
	if message == nil && v.streaming != nil {
		v.rebuild(view.Conversation.Entries)
	}
	if message != nil {
		v.syncStreaming(message)
	}
	for _, slot := range live.Tools {
		if slot.Status == durable.ToolSlotPending {
			continue
		}
		component := v.toolCard(slot.Name, slot.CallId, nil, false)
		component.SetArgsComplete()
		if slot.Status != durable.ToolSlotRunning {
			continue
		}
		component.MarkExecutionStarted()
		child, hasChild := tuiSlotConversationId(slot.Details)
		if slot.Output == nil && hasChild {
			text := fmt.Sprintf("Subagent %d is working. /agents switches to it.", child)
			component.UpdateResult(components.ToolExecutionResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
				Details: slot.Details,
				IsError: false,
			}, true)
		} else if slot.Output != nil {
			component.UpdateResult(components.ToolExecutionResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: *slot.Output}},
				Details: slot.Details,
				IsError: false,
			}, true)
		}
	}
	v.syncTasks(view.Tasks)
	v.syncQueue(tuiInboxOf(view.Conversation))
	v.syncNotices(view)
	thinking := ai.ModelThinkingLevelOff
	if t := AgentOf(view.Conversation).ThinkingLevel; t != nil && *t != "" {
		thinking = *t
	}
	v.editor.BorderColor = theme.CurrentTheme().GetThinkingBorderColor(thinking)
	v.syncStatus(live)
	v.syncFooter(view)
	if v.rebuilt {
		v.transcript.ScrollToEnd()
	}
	rebuilt := v.rebuilt
	v.ui.RequestRender(&rebuilt)
	v.rebuilt = false
}

func (v *tuiView) syncTasks(graph *durable.TaskGraph) {
	v.tasks.Clear()
	if graph == nil {
		return
	}
	keys := make([]string, 0, len(graph.Tasks))
	for k := range graph.Tasks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	nodes := make([]durable.TaskGraphNode, 0, len(keys))
	for _, k := range keys {
		nodes = append(nodes, graph.Tasks[k])
	}
	th := theme.CurrentTheme()
	lines := []string{th.Fg(theme.FgAccent, fmt.Sprintf("Tasks (%d live, /tasks to hide)", len(nodes)))}

	owned := make(map[durable.ConversationId]bool)
	for _, node := range nodes {
		for _, cid := range node.Conversations {
			owned[cid] = true
		}
	}
	childrenOf := func(node durable.TaskGraphNode) []durable.TaskGraphNode {
		var out []durable.TaskGraphNode
		for _, candidate := range nodes {
			if candidate.Owner != nil && *candidate.Owner == node.Id {
				out = append(out, candidate)
				continue
			}
			if candidate.Owner == nil {
				for _, cid := range node.Conversations {
					if cid == candidate.ConversationId {
						out = append(out, candidate)
						break
					}
				}
			}
		}
		return out
	}
	var visit func(node durable.TaskGraphNode, depth int)
	visit = func(node durable.TaskGraphNode, depth int) {
		lines = append(lines, strings.Repeat("  ", depth+1)+tuiDescribeTask(node))
		for _, child := range childrenOf(node) {
			visit(child, depth+1)
		}
	}
	for _, node := range nodes {
		if node.Owner == nil && !owned[node.ConversationId] {
			visit(node, 0)
		}
	}
	for _, line := range lines {
		v.tasks.AddChild(tui.NewTruncatedText(th.Fg(theme.FgMuted, line), 1, 0))
	}
}

func (v *tuiView) syncQueue(inbox durable.InboxState) {
	v.queue.Clear()
	th := theme.CurrentTheme()
	for _, item := range inbox.Items {
		var text string
		if item.Mode == durable.InboxModeWrite {
			kind := ""
			if item.Entry != nil {
				if raw, ok := item.Entry.Get("kind"); ok {
					kind = fmt.Sprint(raw)
				}
			}
			text = "<" + kind + ">"
		} else {
			text = tuiInboxText(item.Content)
		}
		v.queue.AddChild(tui.NewTruncatedText(th.Fg(theme.FgMuted, fmt.Sprintf("[%s] %s", item.Mode, text)), 1, 0))
	}
}

func (v *tuiView) syncNotices(view DurableView) {
	v.notices.Clear()
	th := theme.CurrentTheme()
	items := view.Notices
	if len(items) > 4 {
		items = items[len(items)-4:]
	}
	for _, item := range items {
		color := theme.FgMuted
		if item.Level == NoticeLevelError {
			color = theme.FgError
		} else if item.Level == NoticeLevelWarning {
			color = theme.FgWarning
		}
		v.notices.AddChild(tui.NewTruncatedText(th.Fg(color, item.Message), 1, 0))
	}
}

func (v *tuiView) syncStatus(live durable.LiveState) {
	var text string
	var runningTool *durable.ToolSlot
	for i := range live.Tools {
		if live.Tools[i].Status == durable.ToolSlotRunning {
			runningTool = &live.Tools[i]
			break
		}
	}
	switch {
	case live.Generation != nil && live.Generation.Retry != nil:
		text = fmt.Sprintf("Retrying (attempt %d): %s", live.Generation.Attempt+1, live.Generation.Retry.Error)
	case live.Generation != nil && live.Generation.Deferred != nil:
		text = "Waiting for deferred response..."
	case len(live.Compactions) > 0:
		comp := live.Compactions[0]
		if comp.Retry != nil {
			text = fmt.Sprintf("Retrying %s compaction (attempt %d)...", comp.Reason, comp.Attempt+1)
		} else {
			text = fmt.Sprintf("Compacting (%s)...", comp.Reason)
		}
	case runningTool != nil:
		text = fmt.Sprintf("Running %s... (esc to abort)", runningTool.Name)
	case live.Run != nil:
		text = "Working... (esc to abort)"
	}
	if text == v.statusText {
		return
	}
	v.statusText = text
	if v.indicator != nil {
		v.indicator.Dispose()
		v.indicator = nil
	}
	if text != "" {
		v.indicator = components.NewWorkingStatusIndicator(v.ui, text, nil, func(part string) string {
			if v.editor.BorderColor != nil {
				return v.editor.BorderColor(part)
			}
			return part
		})
		v.editor.SetWorkingStatusIndicator(v.indicator.StatusIndicator)
	} else {
		v.editor.SetWorkingStatusIndicator(nil)
	}
}

func (v *tuiView) syncFooter(view DurableView) {
	th := theme.CurrentTheme()
	agent := AgentOf(view.Conversation)
	usage := tuiTotalUsage(tuiUsageOf(view.Conversation))
	stats := make([]string, 0, 7)
	if usage.Input > 0 {
		stats = append(stats, "↑"+components.FormatTokens(usage.Input))
	}
	if usage.Output > 0 {
		stats = append(stats, "↓"+components.FormatTokens(usage.Output))
	}
	if usage.CacheRead > 0 {
		stats = append(stats, "R"+components.FormatTokens(usage.CacheRead))
	}
	if usage.CacheWrite > 0 {
		stats = append(stats, "W"+components.FormatTokens(usage.CacheWrite))
	}
	stats = append(stats, fmt.Sprintf("$%.3f", usage.Cost.Total))

	contextWindow := 0
	if agent.Model != nil {
		for _, m := range view.Models {
			if m.Provider == agent.Model.Provider && m.ModelId == agent.Model.ModelId {
				contextWindow = m.ContextWindow
				break
			}
		}
	}
	if contextWindow > 0 {
		tokens, hasTokens := tuiContextTokens(view.Conversation.Entries)
		pctText := "?"
		high := false
		if hasTokens {
			pct := (float64(tokens) / float64(contextWindow)) * 100
			pctText = fmt.Sprintf("%.1f%%", pct)
			high = pct > 90
		}
		text := pctText + "/" + components.FormatTokens(contextWindow)
		if high {
			text = th.Fg(theme.FgError, text)
		}
		stats = append(stats, text)
	}
	v.footerStats.SetText(th.Fg(theme.FgDim, strings.Join(stats, " ")+"  "+view.Session.Cwd))

	modelStr := "no model"
	if agent.Model != nil {
		modelStr = agent.Model.Provider + "/" + agent.Model.ModelId
	}
	label := fmt.Sprintf("conversation %d", view.Conversation.Conversation.Id)
	for _, candidate := range view.Conversations {
		if candidate.Id == view.Conversation.Conversation.Id {
			label = candidate.Label
			break
		}
	}
	labelColor := theme.FgAccent
	if label == "main" {
		labelColor = theme.FgDim
	}
	thinking := ai.ModelThinkingLevelOff
	if agent.ThinkingLevel != nil && *agent.ThinkingLevel != "" {
		thinking = *agent.ThinkingLevel
	}
	hints := fmt.Sprintf(
		" · %s · thinking:%s (%s) · %s or /model · /agents · /compact · /tasks · %s follow-up · %s exit",
		modelStr,
		thinking,
		theme.KeyText(core.KeybindingAppThinkingCycle),
		theme.KeyText(core.KeybindingAppModelSelect),
		theme.KeyText(core.KeybindingAppMessageFollowUp),
		theme.KeyText(core.KeybindingAppClear),
	)
	v.footerHints.SetText(th.Fg(labelColor, label) + th.Fg(theme.FgDim, hints))
}

func (v *tuiView) syncTranscript(entries []durable.EntryRecord) {
	mismatch := false
	for i, id := range v.renderedIds {
		if i >= len(entries) || entries[i].Id != id {
			mismatch = true
			break
		}
	}
	if mismatch {
		v.rebuild(entries)
	}
	for _, entry := range entries[len(v.renderedIds):] {
		v.addEntry(entry)
		v.renderedIds = append(v.renderedIds, entry.Id)
	}
}

func (v *tuiView) rebuild(entries []durable.EntryRecord) {
	v.chat.Clear()
	v.discardTools()
	v.summaries = nil
	v.rebuilt = true
	v.renderedIds = nil
	v.streaming = nil
	for _, entry := range entries {
		v.addEntry(entry)
		v.renderedIds = append(v.renderedIds, entry.Id)
	}
}

func (v *tuiView) addEntry(entry durable.EntryRecord) {
	var msg any
	if len(entry.Model) > 0 {
		msg = entry.Model[0]
	}
	switch entry.Kind {
	case durable.UserEntry.Kind:
		if um, ok := msg.(*ai.UserMessage); ok && um != nil {
			v.chat.AddChild(tui.NewSpacer(1))
			v.chat.AddChild(components.NewUserMessageComponent(tuiUserText(um.Content)))
		}
	case durable.AssistantEntry.Kind:
		if am, ok := msg.(*ai.AssistantMessage); ok && am != nil {
			comp := v.streaming
			if comp == nil {
				comp = components.NewAssistantMessageComponent(am)
				v.chat.AddChild(comp)
			} else {
				comp.UpdateContent(am, false)
			}
			v.streaming = nil

			// Only a tool-calling answer runs its calls; an aborted, failed, or truncated one never does.
			ran := am.StopReason == ai.StopReasonToolUse
			for _, block := range am.Content {
				tc, ok := block.(*ai.ToolCall)
				if !ok || tc == nil {
					continue
				}
				streamed := v.streamingCalls[tc.Id]
				if !ran && !streamed {
					continue
				}
				card := v.toolCard(tc.Name, tc.Id, tc.Arguments, !streamed)
				card.SetArgsComplete()
				if !ran {
					card.UpdateResult(components.ToolExecutionResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "Not run: the answer was interrupted."}},
						IsError: true,
					}, false)
				}
			}
			v.streamingCalls = make(map[string]bool)
		}
	case durable.ToolResultEntry.Kind:
		if tr, ok := msg.(*ai.ToolResultMessage); ok && tr != nil {
			v.toolCard(tr.ToolName, tr.ToolCallId, nil, false).UpdateResult(components.ToolExecutionResult{
				Content: tr.Content,
				Details: tr.Details,
				IsError: tr.IsError,
			})
		}
	case durable.CompactionEntry.Kind:
		summaryText := ""
		if um, ok := msg.(*ai.UserMessage); ok && um != nil {
			summaryText = tuiUserText(um.Content)
		}
		summary := tuiNewCompactionComponent(summaryText, v.expanded)
		v.summaries = append(v.summaries, summary)
		v.chat.AddChild(tui.NewSpacer(1))
		v.chat.AddChild(summary)
	case durable.ResetEntry.Kind:
		v.addText("[new context]")
	}
}

func (v *tuiView) syncStreaming(message *ai.AssistantMessage) {
	if v.streaming == nil {
		v.streaming = components.NewAssistantMessageComponent(message)
		v.chat.AddChild(v.streaming)
	}
	v.streaming.UpdateContent(message, true)
	for _, block := range message.Content {
		tc, ok := block.(*ai.ToolCall)
		if !ok || tc == nil {
			continue
		}
		v.toolCard(tc.Name, tc.Id, tc.Arguments, !v.streamingCalls[tc.Id])
		v.streamingCalls[tc.Id] = true
	}
}

func (v *tuiView) addText(text string) {
	v.chat.AddChild(tui.NewSpacer(1))
	v.chat.AddChild(tui.NewText(theme.CurrentTheme().Fg(theme.FgMuted, text), 1, 0, nil))
}

func (v *tuiView) toolCard(name, callId string, args any, fresh bool) *components.ToolExecutionComponent {
	if !fresh {
		if existing := v.tools[callId]; existing != nil {
			if args != nil {
				existing.UpdateArgs(tuiObjectFromAny(args))
			}
			return existing
		}
	}
	var renderer *core.ToolRenderers
	if r, ok := v.renderers[core.ToolName(name)]; ok {
		renderer = &r
	}
	component := components.NewToolExecutionComponent(
		name,
		callId,
		tuiObjectFromAny(args),
		&components.ToolExecutionOptions{},
		renderer,
		v.ui,
		v.cwd,
	)
	component.SetExpanded(v.expanded)
	v.chat.AddChild(component)
	v.cards = append(v.cards, component)
	v.tools[callId] = component
	return component
}

func tuiDescribeTask(node durable.TaskGraphNode) string {
	var status string
	switch node.State.Status {
	case "waiting":
		ids := make([]string, 0, len(node.State.On))
		for _, id := range node.State.On {
			ids = append(ids, strconv.FormatInt(int64(id), 10))
		}
		status = "waiting on " + strings.Join(ids, ", ")
	case "completing":
		status = fmt.Sprintf("completing (%s)", node.State.Outcome)
	default:
		status = strings.TrimSpace(fmt.Sprintf("%s %s", node.State.Status, node.State.Phase))
	}
	var flags []string
	if node.Background {
		flags = append(flags, "background")
	}
	if node.AbortRequested {
		flags = append(flags, "aborting")
	}
	flagText := ""
	if len(flags) > 0 {
		flagText = " [" + strings.Join(flags, ", ") + "]"
	}
	ownedText := ""
	if len(node.Conversations) > 0 {
		cids := make([]string, 0, len(node.Conversations))
		for _, cid := range node.Conversations {
			cids = append(cids, strconv.FormatInt(int64(cid), 10))
		}
		ownedText = " owns conversation " + strings.Join(cids, ", ")
	}
	return fmt.Sprintf("%s #%d: %s%s%s", node.Kind, node.Id, status, flagText, ownedText)
}

func tuiUserText(content ai.UserMessageContent) string {
	if content.Text != nil {
		return *content.Text
	}
	var sb strings.Builder
	for _, block := range content.Blocks {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func tuiInboxText(raw any) string {
	if s, ok := raw.(string); ok {
		return s
	}
	var content ai.UserMessageContent
	if err := jsonx.Decode(raw, &content); err == nil {
		return tuiUserText(content)
	}
	return ""
}

func tuiTotalUsage(state durable.UsageState) ai.Usage {
	var total ai.Usage
	for _, u := range state.Models {
		total.Input += u.Input
		total.Output += u.Output
		total.CacheRead += u.CacheRead
		total.CacheWrite += u.CacheWrite
		total.Cost.Total += u.Cost.Total
	}
	for _, u := range state.Tools {
		total.Input += u.Input
		total.Output += u.Output
		total.CacheRead += u.CacheRead
		total.CacheWrite += u.CacheWrite
		total.Cost.Total += u.Cost.Total
	}
	return total
}

// tuiContextTokens computes context size from the newest successful answer after the newest compaction.
func tuiContextTokens(entries []durable.EntryRecord) (int, bool) {
	var compacted durable.EntryId
	for _, entry := range entries {
		if entry.Kind == durable.CompactionEntry.Kind && entry.Id > compacted {
			compacted = entry.Id
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Id < compacted || entry.Kind != durable.AssistantEntry.Kind || len(entry.Model) == 0 {
			continue
		}
		msg, ok := entry.Model[0].(*ai.AssistantMessage)
		if !ok || msg == nil {
			continue
		}
		if msg.StopReason == ai.StopReasonAborted || msg.StopReason == ai.StopReasonError {
			continue
		}
		tokens := msg.Usage.TotalTokens
		if tokens == 0 {
			tokens = msg.Usage.Input + msg.Usage.Output + msg.Usage.CacheRead + msg.Usage.CacheWrite
		}
		return tokens, true
	}
	return 0, false
}

func tuiLiveOf(view durable.ConversationView) durable.LiveState {
	var live durable.LiveState
	if raw := view.Docs[durable.LiveDoc.Definition.Kind]; raw != nil {
		_ = jsonx.Decode(raw, &live)
	}
	return live
}

func tuiInboxOf(view durable.ConversationView) durable.InboxState {
	var inbox durable.InboxState
	if raw := view.Docs[durable.InboxDoc.Definition.Kind]; raw != nil {
		_ = jsonx.Decode(raw, &inbox)
	}
	return inbox
}

func tuiUsageOf(view durable.ConversationView) durable.UsageState {
	var usage durable.UsageState
	if raw := view.Docs[durable.UsageDoc.Definition.Kind]; raw != nil {
		_ = jsonx.Decode(raw, &usage)
	}
	return usage
}

func tuiSlotConversationId(details durable.JsonValue) (int64, bool) {
	if details == nil {
		return 0, false
	}
	var payload struct {
		ConversationId *int64 `json:"conversationId,omitzero"`
	}
	if err := jsonx.Decode(details, &payload); err == nil && payload.ConversationId != nil {
		return *payload.ConversationId, true
	}
	return 0, false
}

func tuiObjectFromAny(v any) *jsonx.Object {
	if obj, ok := v.(*jsonx.Object); ok && obj != nil {
		return obj
	}
	obj := jsonx.NewObject()
	if v != nil {
		_ = jsonx.Decode(v, obj)
	}
	return obj
}

// RunDurableTui runs the interactive durable TUI until the user exits or ctx is canceled.
func RunDurableTui(
	ctx context.Context,
	source DurableViewSource,
	controller DurableController,
	settings *core.SettingsManager,
) error {
	if source == nil || controller == nil {
		return fmt.Errorf("view source and controller are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if settings != nil {
		capOverrides := settings.GetTerminalCapabilityOverrides()
		tui.SetCapabilityOverrides(&capOverrides)
	}
	// The system theme until the controller resolves the user's theme against the terminal's colors.
	if _, err := theme.InitTheme("", false); err != nil {
		return err
	}

	exited := make(chan struct{})
	var exitOnce tuiSyncExit
	exit := func() {
		exitOnce.doClose(exited)
	}

	var view *tuiView
	selectModel := func() {
		snapshot := source.Current()
		current := AgentOf(snapshot.Conversation).Model
		isCurrent := func(m ModelSummary) bool {
			return current != nil && m.Provider == current.Provider && m.ModelId == current.ModelId
		}
		models := append([]ModelSummary(nil), snapshot.Models...)
		sort.SliceStable(models, func(i, j int) bool {
			ci, cj := isCurrent(models[i]), isCurrent(models[j])
			return ci && !cj
		})
		items := make([]tui.SelectItem, 0, len(models))
		for _, m := range models {
			desc := m.Provider
			items = append(items, tui.SelectItem{
				Value:       m.Provider + "/" + m.ModelId,
				Label:       m.ModelId,
				Description: &desc,
			})
		}
		selector := tuiNewListSelector(
			"Select model:",
			items,
			func(value string) {
				view.restoreEditor()
				if sep := strings.IndexByte(value, '/'); sep >= 0 {
					go func() {
						_ = controller.SetModel(ctx, durable.ModelRef{
							Provider: value[:sep],
							ModelId:  value[sep+1:],
						})
					}()
				}
			},
			func() {
				view.restoreEditor()
			},
		)
		view.mount(selector)
	}

	selectConversation := func() {
		snapshot := source.Current()
		items := make([]tui.SelectItem, 0, len(snapshot.Conversations))
		for i := len(snapshot.Conversations) - 1; i >= 0; i-- {
			candidate := snapshot.Conversations[i]
			prefix := ""
			if candidate.Id == snapshot.Conversation.Conversation.Id {
				prefix = "(shown) "
			}
			title := ""
			if candidate.Title != nil {
				title = *candidate.Title
			}
			desc := prefix + title
			items = append(items, tui.SelectItem{
				Value:       strconv.FormatInt(int64(candidate.Id), 10),
				Label:       candidate.Label,
				Description: &desc,
			})
		}
		selector := tuiNewListSelector(
			"Switch to:",
			items,
			func(value string) {
				view.restoreEditor()
				if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
					go func() {
						_ = controller.SwitchConversation(ctx, durable.ConversationId(parsed))
					}()
				}
			},
			func() {
				view.restoreEditor()
			},
		)
		view.mount(selector)
	}

	var err error
	view, err = tuiNewView(source.Current().Session.Cwd, tuiHandlers{
		submit: func(text string) {
			trimmed := strings.TrimSpace(text)
			if trimmed == "" {
				return
			}
			switch {
			case trimmed == "/model":
				selectModel()
			case trimmed == "/tasks":
				go func() { _ = controller.ToggleTasks(ctx) }()
			case trimmed == "/agents":
				selectConversation()
			case trimmed == "/compact" || strings.HasPrefix(trimmed, "/compact "):
				instructions := strings.TrimSpace(strings.TrimPrefix(trimmed, "/compact"))
				var ptr *string
				if instructions != "" {
					ptr = &instructions
				}
				go func() { _ = controller.Compact(ctx, ptr) }()
			default:
				go func() { _ = controller.Submit(ctx, trimmed, durable.SubmissionWhenBusySteer) }()
			}
		},
		followUp: func(text string) {
			go func() { _ = controller.Submit(ctx, text, durable.SubmissionWhenBusyFollowUp) }()
		},
		abort: func() {
			go func() { _ = controller.Abort(ctx) }()
		},
		exit:          exit,
		selectModel:   selectModel,
		cycleThinking: func() { go func() { _ = controller.CycleThinking(ctx) }() },
	})
	if err != nil {
		return err
	}

	// pi's theme handling: the theme setting (also light/dark pairs) resolved against the terminal's reported colors.
	themes := core.NewInteractiveThemeController(core.InteractiveThemeControllerOptions{
		SettingsManager: settings,
		Ui:              view.ui,
		OnThemeApplied:  func() { view.ui.RequestRender(nil) },
		OnThemeError:    func(message string) { _, _ = fmt.Fprintln(os.Stderr, message) },
	})

	var stopped atomic.Bool
	unsubscribe := source.Subscribe(func() {
		if stopped.Load() {
			return
		}
		view.ui.Post(func() {
			if stopped.Load() {
				return
			}
			view.apply(source.Current())
		})
	})

	view.start()
	_, _ = themes.ApplyFromSettings()
	view.apply(source.Current())

	select {
	case <-ctx.Done():
	case <-exited:
	}

	stopped.Store(true)
	unsubscribe()
	themes.Dispose()
	view.stop()
	return nil
}

type tuiSyncExit struct {
	done atomic.Bool
}

func (s *tuiSyncExit) doClose(ch chan struct{}) {
	if s.done.CompareAndSwap(false, true) {
		close(ch)
	}
}
