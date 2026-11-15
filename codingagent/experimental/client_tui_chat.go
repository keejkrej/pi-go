// Ported from packages/coding-agent/src/experimental/client-tui-chat.ts (pi v1.0.0).

package experimental

import (
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// ClientChatOptions configures the experimental interactive chat component.
type ClientChatOptions struct {
	UI             tui.TUI
	Keybindings    *core.KeybindingsManager
	Settings       *core.SettingsManager
	Agent          services.AgentController
	Transcript     services.Transcript
	SlashCommands  services.SlashCommands
	AgentDir       string
	Cwd            string
	RequestRender  func()
	OnExit         func()
	OnOpenSessions func()
}

type ctcNotice struct {
	text    string
	isError bool
}

// ClientChat renders the attached session transcript, queued submissions, status, and editor.
type ClientChat struct {
	UI                  tui.TUI
	TranscriptContainer *tui.Container
	PendingContainer    *tui.Container
	StatusContainer     *tui.Container
	Editor              *components.CustomEditor

	mu            sync.Mutex
	root          tui.Component
	regularRoot   *tui.Container
	viewport      *interactive.ChatViewport
	keybindings   *core.KeybindingsManager
	settings      *core.SettingsManager
	toolRenderers map[core.ToolName]core.ToolRenderers
	cwd           string
	requestRender func()
	onExit        func()
	onOpenSess    func()

	agent         services.AgentController
	transcript    services.Transcript
	slashCommands services.SlashCommands

	unsubTranscript    func()
	unsubSlashCommands func()

	working  *components.WorkingStatusIndicator
	lastView *durable.ConversationView
	lastBusy bool
	notices  []ctcNotice
	disposed bool

	cancelCtx chord.CancelContext
	wg        sync.WaitGroup
}

// CreateClientChat constructs an interactive chat component wired to experimental services.
func CreateClientChat(options ClientChatOptions) (*ClientChat, error) {
	if options.UI == nil {
		return nil, fmt.Errorf("ClientChat requires UI")
	}
	cwd := options.Cwd
	if cwd == "" {
		cwd = "."
	}
	kb := options.Keybindings
	if kb == nil {
		kb = core.KeybindingsManagerCreate(options.AgentDir)
	}
	tui.SetKeybindings(kb.KeybindingsManager)

	if theme.CurrentTheme() == nil {
		_, _ = theme.InitTheme("dark", false)
	}

	reqRender := options.RequestRender
	if reqRender == nil {
		reqRender = func() {
			options.UI.RequestRender(nil)
		}
	}

	transcriptContainer := tui.NewContainer()
	pendingContainer := tui.NewContainer()
	statusContainer := tui.NewContainer()
	editor := components.NewCustomEditor(options.UI, theme.GetEditorTheme(), kb)

	var root tui.Component
	var regularRoot *tui.Container
	var viewport *interactive.ChatViewport
	if options.UI.Mode() == tui.TuiModeFullscreen {
		editor.EmbedWorkingStatus = true
		th := theme.CurrentTheme()
		viewport = interactive.CreateChatViewport(interactive.ChatViewportOptions{
			Document:        transcriptContainer,
			PendingMessages: pendingContainer,
			Status:          statusContainer,
			Editor:          editor,
			ScrollbarTrackStyle: func(text string) string {
				if th != nil {
					return th.Fg(theme.FgScrollbarTrack, text)
				}
				return text
			},
			ScrollbarThumbStyle: func(text string) string {
				if th != nil {
					return th.Fg(theme.FgScrollbarThumb, text)
				}
				return text
			},
		})
		root = viewport.Root
	} else {
		regularRoot = tui.NewContainer()
		regularRoot.AddChild(transcriptContainer)
		regularRoot.AddChild(pendingContainer)
		regularRoot.AddChild(statusContainer)
		regularRoot.AddChild(editor)
		root = regularRoot
	}

	chat := &ClientChat{
		UI:                  options.UI,
		TranscriptContainer: transcriptContainer,
		PendingContainer:    pendingContainer,
		StatusContainer:     statusContainer,
		Editor:              editor,
		root:                root,
		regularRoot:         regularRoot,
		viewport:            viewport,
		keybindings:         kb,
		settings:            options.Settings,
		toolRenderers:       core.CreateAllToolRenderers(),
		cwd:                 cwd,
		requestRender:       reqRender,
		onExit:              options.OnExit,
		onOpenSess:          options.OnOpenSessions,
		slashCommands:       options.SlashCommands,
		cancelCtx:           chord.WithCancel(chord.BackgroundContext),
	}

	chat.ctcWireEditorHandlers()

	if options.SlashCommands != nil {
		chat.ctcRebuildAutocomplete(options.SlashCommands.List())
		chat.unsubSlashCommands = options.SlashCommands.Subscribe(func(cmds []services.SlashCommandContribution) {
			chat.ctcRebuildAutocomplete(cmds)
		})
	} else {
		chat.ctcRebuildAutocomplete(nil)
	}

	if options.Agent != nil || options.Transcript != nil {
		chat.BindSession(options.Agent, options.Transcript)
	}
	return chat, nil
}

// Root returns the top-level TUI component rendered by ClientChat.
func (c *ClientChat) Root() tui.Component {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.root
}

// BindSession binds or rebinds ClientChat to the active session's AgentController and Transcript.
func (c *ClientChat) BindSession(agent services.AgentController, transcript services.Transcript) {
	c.mu.Lock()
	oldUnsub := c.unsubTranscript
	c.unsubTranscript = nil
	c.agent = agent
	c.transcript = transcript
	c.lastView = nil
	c.lastBusy = false
	c.notices = nil
	c.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}

	if transcript == nil {
		c.ctcClearView()
		c.requestRender()
		return
	}

	state := transcript.State()
	if initial, ok := state.Value(); ok {
		c.ctcApplyView(initial)
	}
	unsub := state.Subscribe(func(view durable.ConversationView, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
		c.ctcApplyView(view)
		return nil
	})

	c.mu.Lock()
	if c.disposed || c.transcript != transcript {
		c.mu.Unlock()
		unsub()
		return
	}
	c.unsubTranscript = unsub
	c.mu.Unlock()

	if view, ok := state.Value(); ok {
		c.ctcApplyView(view)
	}
}

// Render renders the chat component hierarchy for width.
func (c *ClientChat) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.root.Render(width)
}

// RenderEditorOnly renders just the chat editor while detached/reconnecting.
func (c *ClientChat) RenderEditorOnly(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Editor.Render(width)
}

// Invalidate clears cached rendering state on the chat component hierarchy.
func (c *ClientChat) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.root.Invalidate()
}

// IsBusy reports whether the active conversation has a live run, generation, or compaction.
func (c *ClientChat) IsBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastBusy
}

// AppendNotice adds an informational or error status line at the bottom of the transcript.
func (c *ClientChat) AppendNotice(text string, isError bool) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	c.notices = append(c.notices, ctcNotice{text: text, isError: isError})
	view := c.lastView
	c.mu.Unlock()

	if view != nil {
		c.ctcApplyView(*view)
		return
	}
	c.mu.Lock()
	c.TranscriptContainer.AddChild(c.ctcBuildNoticeComponent(ctcNotice{text: text, isError: isError}))
	c.mu.Unlock()
	c.requestRender()
}

// SubmitText submits prompt text or slash commands just as pressing Enter in the editor does.
func (c *ClientChat) SubmitText(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	c.mu.Lock()
	c.Editor.SetText("")
	c.Editor.AddToHistory(trimmed)
	c.mu.Unlock()
	c.requestRender()

	if strings.HasPrefix(trimmed, "/") {
		c.ctcExecuteSlashCommand(trimmed)
		return
	}

	c.mu.Lock()
	agent := c.agent
	busy := c.lastBusy
	ctx := c.cancelCtx.Context
	disposed := c.disposed
	c.mu.Unlock()

	if disposed || agent == nil {
		c.AppendNotice("Session is not attached yet", true)
		return
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if busy {
			resp, err := agent.Steer(services.AgentPromptRequest{Message: trimmed}, ctx)
			if err != nil {
				c.AppendNotice("Failed to steer: "+err.Error(), true)
				return
			}
			if !resp.Accepted && resp.Error != nil && resp.Error.Message != "" {
				c.AppendNotice(resp.Error.Message, true)
			}
			return
		}
		resp, err := agent.Prompt(services.AgentPromptRequest{Message: trimmed}, ctx)
		if err != nil {
			c.AppendNotice("Failed to send prompt: "+err.Error(), true)
			return
		}
		if !resp.Accepted && resp.Error != nil && resp.Error.Message != "" {
			c.AppendNotice(resp.Error.Message, true)
		}
	}()
}

// Dispose unsubscribes state listeners, stops active spinners, and cancels in-flight RPCs.
func (c *ClientChat) Dispose() {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	c.disposed = true
	unsubT := c.unsubTranscript
	c.unsubTranscript = nil
	unsubS := c.unsubSlashCommands
	c.unsubSlashCommands = nil
	working := c.working
	c.working = nil
	c.Editor.SetWorkingStatusIndicator(nil)
	c.StatusContainer.Clear()
	cancel := c.cancelCtx.Cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel(nil)
	}
	if unsubT != nil {
		unsubT()
	}
	if unsubS != nil {
		unsubS()
	}
	if working != nil {
		working.Dispose()
	}
	c.wg.Wait()
}

func (c *ClientChat) ctcWireEditorHandlers() {
	c.Editor.OnSubmit = func(text string) {
		c.SubmitText(text)
	}

	abortActive := func() {
		c.mu.Lock()
		agent := c.agent
		busy := c.lastBusy
		ctx := c.cancelCtx.Context
		disposed := c.disposed
		c.mu.Unlock()
		if disposed || !busy || agent == nil {
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			if err := agent.Abort(ctx); err != nil {
				c.AppendNotice("Abort failed: "+err.Error(), true)
			}
		}()
	}

	c.Editor.OnEscape = abortActive
	c.Editor.OnAction(core.KeybindingAppInterrupt, abortActive)
	c.Editor.OnAction(core.KeybindingAppClear, func() {
		c.mu.Lock()
		onExit := c.onExit
		c.mu.Unlock()
		if onExit != nil {
			onExit()
		}
	})

	c.Editor.OnAction(core.KeybindingAppMessageFollowUp, func() {
		c.mu.Lock()
		trimmed := strings.TrimSpace(c.Editor.GetText())
		agent := c.agent
		ctx := c.cancelCtx.Context
		disposed := c.disposed
		if trimmed != "" && !disposed && agent != nil {
			c.Editor.SetText("")
			c.Editor.AddToHistory(trimmed)
		}
		c.mu.Unlock()
		if trimmed == "" || disposed || agent == nil {
			return
		}
		c.requestRender()
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			resp, err := agent.FollowUp(services.AgentPromptRequest{Message: trimmed}, ctx)
			if err != nil {
				c.AppendNotice("Failed to queue follow-up: "+err.Error(), true)
				return
			}
			if !resp.Accepted && resp.Error != nil && resp.Error.Message != "" {
				c.AppendNotice(resp.Error.Message, true)
			}
		}()
	})

	c.Editor.OnCtrlD = func() {
		c.mu.Lock()
		empty := strings.TrimSpace(c.Editor.GetText()) == ""
		onExit := c.onExit
		c.mu.Unlock()
		if empty && onExit != nil {
			onExit()
		}
	}
}

func (c *ClientChat) ctcExecuteSlashCommand(raw string) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return
	}
	name := strings.TrimPrefix(fields[0], "/")
	args := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))

	switch strings.ToLower(name) {
	case "exit", "quit":
		c.mu.Lock()
		onExit := c.onExit
		c.mu.Unlock()
		if onExit != nil {
			onExit()
		}
		return
	case "sessions":
		c.mu.Lock()
		onOpen := c.onOpenSess
		c.mu.Unlock()
		if onOpen != nil {
			onOpen()
		}
		return
	case "abort":
		c.mu.Lock()
		agent := c.agent
		ctx := c.cancelCtx.Context
		disposed := c.disposed
		c.mu.Unlock()
		if !disposed && agent != nil {
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				_ = agent.Abort(ctx)
			}()
		}
		return
	}

	c.mu.Lock()
	slash := c.slashCommands
	ctx := c.cancelCtx.Context
	disposed := c.disposed
	c.mu.Unlock()
	if disposed || slash == nil {
		c.AppendNotice("Unknown command: /"+name, true)
		return
	}

	var matched *services.SlashCommandContribution
	for _, item := range slash.List() {
		if item.Name == name {
			cp := item
			matched = &cp
			break
		}
	}
	if matched == nil || matched.Run == nil {
		c.AppendNotice("Unknown command: /"+name, true)
		return
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		res, err := matched.Run(args, ctx)
		if err != nil {
			c.AppendNotice(err.Error(), true)
			return
		}
		switch r := res.(type) {
		case services.AgentOperationResponse:
			if !r.Accepted && r.Error != nil && r.Error.Message != "" {
				c.AppendNotice(r.Error.Message, true)
			}
		case *services.AgentOperationResponse:
			if r != nil && !r.Accepted && r.Error != nil && r.Error.Message != "" {
				c.AppendNotice(r.Error.Message, true)
			}
		case services.AgentQueueResponse:
			if !r.Accepted && r.Error != nil && r.Error.Message != "" {
				c.AppendNotice(r.Error.Message, true)
			}
		case *services.AgentQueueResponse:
			if r != nil && !r.Accepted && r.Error != nil && r.Error.Message != "" {
				c.AppendNotice(r.Error.Message, true)
			}
		}
	}()
}

func (c *ClientChat) ctcRebuildAutocomplete(cmds []services.SlashCommandContribution) {
	autoCmds := make([]tui.AutocompleteCommand, 0, len(cmds)+3)
	seen := make(map[string]struct{}, len(cmds)+3)
	for _, cmd := range cmds {
		seen[cmd.Name] = struct{}{}
		var descPtr, hintPtr *string
		if cmd.Description != "" {
			d := cmd.Description
			descPtr = &d
		}
		if cmd.ArgumentHint != "" {
			h := cmd.ArgumentHint
			hintPtr = &h
		}
		var getCompletions func(string) ([]tui.AutocompleteItem, error)
		if cmd.GetArgumentCompletions != nil {
			fn := cmd.GetArgumentCompletions
			getCompletions = func(prefix string) ([]tui.AutocompleteItem, error) {
				items, err := fn(prefix)
				if err != nil {
					return nil, err
				}
				if items == nil {
					return nil, nil
				}
				out := make([]tui.AutocompleteItem, 0, len(items))
				for _, item := range items {
					var itemDesc *string
					if item.Description != "" {
						d := item.Description
						itemDesc = &d
					}
					out = append(out, tui.AutocompleteItem{
						Value:       item.Value,
						Label:       item.Label,
						Description: itemDesc,
					})
				}
				return out, nil
			}
		}
		autoCmds = append(autoCmds, &tui.SlashCommand{
			Name:                   cmd.Name,
			Description:            descPtr,
			ArgumentHint:           hintPtr,
			GetArgumentCompletions: getCompletions,
		})
	}

	builtins := []struct {
		name string
		desc string
	}{
		{name: "abort", desc: "Abort active run"},
		{name: "sessions", desc: "Open session switcher"},
		{name: "exit", desc: "Exit client"},
	}
	for _, b := range builtins {
		if _, ok := seen[b.name]; ok {
			continue
		}
		d := b.desc
		autoCmds = append(autoCmds, &tui.SlashCommand{
			Name:        b.name,
			Description: &d,
		})
	}

	c.mu.Lock()
	if !c.disposed {
		c.Editor.SetAutocompleteProvider(tui.NewCombinedAutocompleteProvider(autoCmds, c.cwd, nil))
	}
	c.mu.Unlock()
}

func (c *ClientChat) ctcClearView() {
	c.mu.Lock()
	oldWorking := c.working
	c.working = nil
	c.Editor.SetWorkingStatusIndicator(nil)
	c.TranscriptContainer.Clear()
	c.PendingContainer.Clear()
	c.StatusContainer.Clear()
	for _, notice := range c.notices {
		c.TranscriptContainer.AddChild(c.ctcBuildNoticeComponent(notice))
	}
	c.mu.Unlock()

	if oldWorking != nil {
		oldWorking.Dispose()
	}
}

func (c *ClientChat) ctcApplyView(view durable.ConversationView) {
	live := ctcDecodeLiveState(view.Docs[durable.LiveDoc.Definition.Kind])
	inbox := ctcDecodeInboxState(view.Docs[durable.InboxDoc.Definition.Kind])
	busy := live != nil && (live.Run != nil || live.Generation != nil || len(live.Compactions) > 0)

	mdTheme := theme.GetMarkdownTheme()
	hideThinking := false
	showImages := false
	imageWidthCells := 0
	if c.settings != nil {
		hideThinking = c.settings.GetHideThinkingBlock()
		showImages = c.settings.GetShowImages()
		imageWidthCells = c.settings.GetImageWidthCells()
	}

	callArgs := make(map[string]*jsonx.Object)
	var toolImgWidth *int
	if imageWidthCells > 0 {
		w := imageWidthCells
		toolImgWidth = &w
	}

	var transcriptItems []tui.Component
	for _, entry := range view.Entries {
		for _, msg := range entry.Model {
			switch m := msg.(type) {
			case *ai.UserMessage:
				txt := ctcExtractUserText(m)
				if txt != "" {
					transcriptItems = append(transcriptItems, components.NewUserMessageComponent(txt, &components.MessageComponentOptions{
						MarkdownTheme: &mdTheme,
					}))
				}
			case *ai.AssistantMessage:
				for _, tc := range ctcExtractAssistantToolCalls(m) {
					callArgs[tc.Id] = tc.Arguments
				}
				if ctcAssistantHasVisibleContent(m) {
					transcriptItems = append(transcriptItems, components.NewAssistantMessageComponent(m, &components.AssistantMessageOptions{
						HideThinkingBlock: &hideThinking,
						MarkdownTheme:     &mdTheme,
					}))
				}
			case *ai.ToolResultMessage:
				var rendererPtr *core.ToolRenderers
				if tr, ok := c.toolRenderers[core.ToolName(m.ToolName)]; ok {
					rendererCopy := tr
					rendererPtr = &rendererCopy
				}
				comp := components.NewToolExecutionComponent(
					m.ToolName,
					m.ToolCallId,
					callArgs[m.ToolCallId],
					&components.ToolExecutionOptions{
						ShowImages:      &showImages,
						ImageWidthCells: toolImgWidth,
					},
					rendererPtr,
					c.UI,
					c.cwd,
				)
				comp.SetArgsComplete()
				comp.MarkExecutionStarted()
				comp.UpdateResult(components.ToolExecutionResult{
					Content:    m.Content,
					Details:    m.Details.OrElse(nil),
					IsError:    m.IsError,
					DurationMs: m.DurationMs,
				})
				transcriptItems = append(transcriptItems, comp)
			}
		}
	}

	if live != nil && live.Generation != nil && live.Generation.Message != nil {
		msg := live.Generation.Message
		for _, tc := range ctcExtractAssistantToolCalls(msg) {
			callArgs[tc.Id] = tc.Arguments
		}
		if ctcAssistantHasVisibleContent(msg) {
			comp := components.NewAssistantMessageComponent(msg, &components.AssistantMessageOptions{
				HideThinkingBlock: &hideThinking,
				MarkdownTheme:     &mdTheme,
			})
			comp.UpdateContent(msg, true)
			transcriptItems = append(transcriptItems, comp)
		}
	}

	if live != nil {
		for _, slot := range live.Tools {
			if slot.Entry != nil {
				continue
			}
			var rendererPtr *core.ToolRenderers
			if tr, ok := c.toolRenderers[core.ToolName(slot.Name)]; ok {
				rendererCopy := tr
				rendererPtr = &rendererCopy
			}
			comp := components.NewToolExecutionComponent(
				slot.Name,
				slot.CallId,
				callArgs[slot.CallId],
				&components.ToolExecutionOptions{
					ShowImages:      &showImages,
					ImageWidthCells: toolImgWidth,
				},
				rendererPtr,
				c.UI,
				c.cwd,
			)
			comp.SetArgsComplete()
			if slot.Status != durable.ToolSlotPending {
				comp.MarkExecutionStarted()
			}
			var content ai.ContentList
			if slot.Output != nil && *slot.Output != "" {
				content = ai.ContentList{&ai.TextContent{Type: "text", Text: *slot.Output}}
			}
			comp.UpdateResult(components.ToolExecutionResult{
				Content: content,
				Details: slot.Details,
				IsError: false,
			}, slot.Status == durable.ToolSlotRunning || slot.Status == durable.ToolSlotPending)
			transcriptItems = append(transcriptItems, comp)
		}
	}

	var pendingItems []tui.Component
	if inbox != nil {
		th := theme.CurrentTheme()
		for _, item := range inbox.Items {
			label := "Queued"
			if item.Mode == durable.InboxModeSteer {
				label = "Steer"
			} else if item.Mode == durable.InboxModeFollowUp {
				label = "Follow-up"
			}
			text := label + ": " + ctcExtractInboxText(item.Content)
			if th != nil {
				text = th.Fg(theme.FgMuted, text)
			}
			pendingItems = append(pendingItems, tui.NewText(text, 1, 0, nil))
		}
	}

	statusMsg := ctcFormatStatusMessage(live)

	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	viewCopy := view
	c.lastView = &viewCopy
	c.lastBusy = busy

	c.TranscriptContainer.Clear()
	for _, comp := range transcriptItems {
		c.TranscriptContainer.AddChild(comp)
	}
	for _, notice := range c.notices {
		c.TranscriptContainer.AddChild(c.ctcBuildNoticeComponent(notice))
	}

	c.PendingContainer.Clear()
	for _, comp := range pendingItems {
		c.PendingContainer.AddChild(comp)
	}

	var oldWorking *components.WorkingStatusIndicator
	if busy {
		if c.working == nil {
			c.working = components.NewWorkingStatusIndicator(c.UI, statusMsg, nil)
			if c.Editor.EmbedWorkingStatus {
				c.Editor.SetWorkingStatusIndicator(c.working.StatusIndicator)
			} else {
				c.StatusContainer.Clear()
				c.StatusContainer.AddChild(c.working)
			}
		} else {
			c.working.SetMessage(statusMsg)
		}
	} else {
		oldWorking = c.working
		c.working = nil
		c.Editor.SetWorkingStatusIndicator(nil)
		c.StatusContainer.Clear()
	}
	c.mu.Unlock()

	if oldWorking != nil {
		oldWorking.Dispose()
	}
	c.requestRender()
}

func (c *ClientChat) ctcBuildNoticeComponent(notice ctcNotice) tui.Component {
	th := theme.CurrentTheme()
	styled := notice.text
	if th != nil {
		if notice.isError {
			styled = th.Fg(theme.FgError, notice.text)
		} else {
			styled = th.Fg(theme.FgMuted, notice.text)
		}
	}
	return tui.NewText(styled, 1, 0, nil)
}

func ctcDecodeLiveState(raw durable.JsonObject) *durable.LiveState {
	if raw == nil {
		return nil
	}
	var out durable.LiveState
	if err := jsonx.Decode(raw, &out); err != nil {
		return nil
	}
	return &out
}

func ctcDecodeInboxState(raw durable.JsonObject) *durable.InboxState {
	if raw == nil {
		return nil
	}
	var out durable.InboxState
	if err := jsonx.Decode(raw, &out); err != nil {
		return nil
	}
	return &out
}

func ctcFormatStatusMessage(live *durable.LiveState) string {
	if live == nil {
		return "Working..."
	}
	if len(live.Compactions) > 0 {
		c := live.Compactions[0]
		if c.Retry != nil {
			return fmt.Sprintf("Compacting (retry %d)...", c.Attempt)
		}
		return "Compacting..."
	}
	if live.Generation != nil {
		if live.Generation.Retry != nil {
			return fmt.Sprintf("Retrying (attempt %d)...", live.Generation.Attempt)
		}
		if live.Generation.Deferred != nil {
			return "Waiting for response..."
		}
	}
	return "Working..."
}

func ctcExtractUserText(msg *ai.UserMessage) string {
	if msg == nil {
		return ""
	}
	if msg.Content.Text != nil {
		return *msg.Content.Text
	}
	var parts []string
	for _, b := range msg.Content.Blocks {
		switch blk := b.(type) {
		case *ai.TextContent:
			if blk.Text != "" {
				parts = append(parts, blk.Text)
			}
		case *ai.ImageContent:
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func ctcExtractAssistantToolCalls(msg *ai.AssistantMessage) []*ai.ToolCall {
	if msg == nil {
		return nil
	}
	var calls []*ai.ToolCall
	for _, b := range msg.Content {
		if tc, ok := b.(*ai.ToolCall); ok && tc != nil {
			calls = append(calls, tc)
		}
	}
	return calls
}

func ctcAssistantHasVisibleContent(msg *ai.AssistantMessage) bool {
	if msg == nil {
		return false
	}
	if msg.ErrorMessage != nil && *msg.ErrorMessage != "" {
		return true
	}
	for _, b := range msg.Content {
		switch blk := b.(type) {
		case *ai.TextContent:
			if strings.TrimSpace(blk.Text) != "" {
				return true
			}
		case *ai.ThinkingContent:
			if strings.TrimSpace(blk.Thinking) != "" {
				return true
			}
		}
	}
	return false
}

func ctcExtractInboxText(raw durable.JsonValue) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	if obj, ok := raw.(*jsonx.Object); ok && obj != nil {
		if txt, ok := obj.Get("text"); ok {
			if s, ok := txt.(string); ok {
				return s
			}
		}
		if content, ok := obj.Get("content"); ok {
			return ctcExtractInboxText(content)
		}
	}
	var userContent ai.UserMessageContent
	if err := jsonx.Decode(raw, &userContent); err == nil {
		if userContent.Text != nil {
			return *userContent.Text
		}
		var parts []string
		for _, b := range userContent.Blocks {
			if blk, ok := b.(*ai.TextContent); ok && blk.Text != "" {
				parts = append(parts, blk.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return fmt.Sprint(raw)
}
