// Ported from packages/coding-agent/src/experimental/durable/tui.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// DurableTui manages the interactive alternate-screen TUI for durableagent.
type DurableTui struct {
	mu           sync.Mutex
	runtime      *DurableAgentRuntime
	terminal     tui.Terminal
	screen       *tui.TuiAltScreen
	transcript   *tui.Container
	scrollView   *tui.ScrollView
	editor       *components.CustomEditor
	working      *components.WorkingStatusIndicator
	statusLine   *tui.Text
	activeTheme  *theme.Theme
	doneCh       chan struct{}
	stopOnce     sync.Once
	watch        durable.ConversationWatch
	renderedHash string
	lastBusy     bool
}

// RunDurableTui launches the interactive TUI loop and blocks until the user exits or ctx is canceled.
func RunDurableTui(ctx context.Context, rt *DurableAgentRuntime) error {
	if rt == nil || rt.Bundle == nil {
		return fmt.Errorf("runtime is required")
	}

	var themeName string
	if rt.Bundle.SettingsManager != nil {
		if ts := rt.Bundle.SettingsManager.GetTheme(); ts != nil {
			themeName = *ts
		}
	}
	th, err := theme.InitTheme(themeName, true)
	if err != nil {
		th, _ = theme.InitTheme("", false)
	}
	if th == nil {
		th = theme.CurrentTheme()
	}

	kb := core.KeybindingsManagerCreate(rt.Bundle.AgentDir)
	tui.SetKeybindings(kb.KeybindingsManager)

	term := tui.NewProcessTerminal()
	showCursor := false
	followEnd := tui.ScrollViewFollowEnd
	scrollbarAuto := tui.ScrollViewScrollbarAuto
	primary := true

	screen := tui.NewTuiAltScreen(term, &showCursor, nil, nil)
	transcript := tui.NewContainer()
	scrollView, err := tui.NewScrollView(transcript, &tui.ScrollViewOptions{
		Follow:    &followEnd,
		Scrollbar: &scrollbarAuto,
		Primary:   &primary,
	})
	if err != nil {
		return err
	}

	embedWorking := true
	editor := components.NewCustomEditor(screen, theme.GetEditorTheme(), kb, &components.CustomEditorOptions{
		EmbedWorkingStatus: &embedWorking,
	})
	working := components.NewWorkingStatusIndicator(screen, "Working...", nil)
	editor.SetWorkingStatusIndicator(working.StatusIndicator)
	statusLine := tui.NewText("", 1, 0, nil)

	growOne := 1
	rootStack := tui.NewVStack([]tui.StackChild{
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: scrollView,
			StackEntryOptions: tui.StackEntryOptions{
				Grow: &growOne,
			},
		}},
		&tui.StackChildComponent{Component: editor},
		&tui.StackChildComponent{Component: statusLine},
	}, nil)
	screen.SetLayoutRoot(rootStack)
	screen.SetFocus(editor)

	dt := &DurableTui{
		runtime:     rt,
		terminal:    term,
		screen:      screen,
		transcript:  transcript,
		scrollView:  scrollView,
		editor:      editor,
		working:     working,
		statusLine:  statusLine,
		activeTheme: th,
		doneCh:      make(chan struct{}),
	}
	dt.durWireHandlers(ctx)

	chordCtx := hsChordContext(ctx)
	watch, err := rt.Bundle.Root.Watch(chordCtx)
	if err != nil {
		return err
	}
	dt.watch = watch
	dt.durApplyView(watch.Value())

	if err := watch.Start(func(view durable.ConversationView, _ []chord.Op, _ chord.Context) error {
		screen.Post(func() {
			dt.durApplyView(view)
			screen.RequestRender(nil)
		})
		return nil
	}); err != nil {
		_ = watch.Stop()
		return err
	}

	screen.Start()
	dt.durUpdateStatusLine("Ready")
	screen.RequestRender(nil)

	select {
	case <-ctx.Done():
	case <-dt.doneCh:
	case <-watch.Done():
	}

	_ = watch.Stop()
	dt.working.Dispose()
	screen.Stop(nil)
	return nil
}

// Stop signals the TUI event loop to exit.
func (d *DurableTui) Stop() {
	d.stopOnce.Do(func() {
		close(d.doneCh)
	})
}

func (d *DurableTui) durWireHandlers(ctx context.Context) {
	d.editor.OnSubmit = func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}
		d.editor.SetText("")
		d.editor.AddToHistory(trimmed)

		if strings.HasPrefix(trimmed, "/") {
			d.durHandleSlashCommand(ctx, trimmed)
			return
		}

		go func() {
			if _, err := d.runtime.SubmitPrompt(ctx, trimmed, d.durIsBusy()); err != nil {
				d.screen.Post(func() {
					d.durAppendNotice("Error submitting prompt: "+err.Error(), true)
					d.screen.RequestRender(nil)
				})
			}
		}()
	}

	d.editor.OnEscape = func() {
		if d.durIsBusy() {
			go func() {
				_ = d.runtime.Abort(ctx)
			}()
		}
	}

	d.editor.OnCtrlD = func() {
		if strings.TrimSpace(d.editor.GetText()) == "" {
			d.Stop()
		}
	}

	d.editor.OnAction(tui.Keybinding("tui.select.cancel"), func() {
		if d.durIsBusy() {
			go func() {
				_ = d.runtime.Abort(ctx)
			}()
		}
	})
}

func (d *DurableTui) durHandleSlashCommand(ctx context.Context, raw string) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return
	}
	cmd := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))

	switch cmd {
	case "/quit", "/exit":
		d.Stop()
	case "/abort":
		go func() {
			_ = d.runtime.Abort(ctx)
		}()
	case "/new", "/reset":
		var handoff *string
		if arg != "" {
			handoff = &arg
		}
		go func() {
			if err := d.runtime.Reset(ctx, handoff); err != nil {
				d.screen.Post(func() {
					d.durAppendNotice("Reset error: "+err.Error(), true)
					d.screen.RequestRender(nil)
				})
			}
		}()
	case "/compact":
		var instructions *string
		if arg != "" {
			instructions = &arg
		}
		go func() {
			if _, err := d.runtime.Compact(ctx, instructions); err != nil {
				d.screen.Post(func() {
					d.durAppendNotice("Compact error: "+err.Error(), true)
					d.screen.RequestRender(nil)
				})
			}
		}()
	case "/tasks", "/status":
		go func() {
			insp, err := d.runtime.Inspect(ctx)
			d.screen.Post(func() {
				if err != nil {
					d.durAppendNotice("Inspect error: "+err.Error(), true)
				} else if insp != nil {
					d.durAppendNotice(fmt.Sprintf("Scheduling=%s tasks=%d submissions=%d", insp.Scheduling, len(insp.Tasks), len(insp.Submissions)), false)
				}
				d.screen.RequestRender(nil)
			})
		}()
	default:
		d.durAppendNotice("Unknown command: "+cmd, false)
		d.screen.RequestRender(nil)
	}
}

func (d *DurableTui) durIsBusy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastBusy
}

func (d *DurableTui) durApplyView(view durable.ConversationView) {
	live := d.durDecodeLiveState(view.Docs[durable.LiveDoc.Definition.Kind])
	busy := live != nil && (live.Run != nil || live.Generation != nil)

	d.mu.Lock()
	d.lastBusy = busy
	d.mu.Unlock()

	mdTheme := theme.GetMarkdownTheme()
	hideThinking := false
	if d.runtime.Bundle != nil && d.runtime.Bundle.SettingsManager != nil {
		hideThinking = d.runtime.Bundle.SettingsManager.GetHideThinkingBlock()
	}
	cwd := "."
	if d.runtime.Bundle != nil && d.runtime.Bundle.Cwd != "" {
		cwd = d.runtime.Bundle.Cwd
	}

	callArgs := make(map[string]*jsonx.Object)
	d.transcript.Clear()
	for _, entry := range view.Entries {
		for _, msg := range entry.Model {
			switch m := msg.(type) {
			case *ai.UserMessage:
				txt := promptExtractMessageText(m)
				if txt != "" {
					d.transcript.AddChild(components.NewUserMessageComponent(txt, &components.MessageComponentOptions{
						MarkdownTheme: &mdTheme,
					}))
				}
			case *ai.AssistantMessage:
				comp := components.NewAssistantMessageComponent(m, &components.AssistantMessageOptions{
					HideThinkingBlock: &hideThinking,
					MarkdownTheme:     &mdTheme,
				})
				d.transcript.AddChild(comp)
				for _, block := range m.Content {
					if tc, ok := block.(*ai.ToolCall); ok && tc != nil {
						argsObj := durObjectFromAny(tc.Arguments)
						callArgs[tc.Id] = argsObj
						toolComp := components.NewToolExecutionComponent(tc.Name, tc.Id, argsObj, nil, nil, d.screen, cwd)
						toolComp.SetArgsComplete()
						d.transcript.AddChild(toolComp)
					}
				}
			case *ai.ToolResultMessage:
				argsObj := callArgs[m.ToolCallId]
				if argsObj == nil {
					argsObj = jsonx.NewObject()
				}
				toolComp := components.NewToolExecutionComponent(m.ToolName, m.ToolCallId, argsObj, nil, nil, d.screen, cwd)
				toolComp.UpdateResult(components.ToolExecutionResult{
					Content: m.Content,
					Details: m.Details,
					IsError: m.IsError,
				}, false)
				d.transcript.AddChild(toolComp)
			}
		}
	}

	if live != nil {
		if live.Generation != nil && live.Generation.Message != nil {
			d.transcript.AddChild(components.NewAssistantMessageComponent(live.Generation.Message, &components.AssistantMessageOptions{
				HideThinkingBlock: &hideThinking,
				MarkdownTheme:     &mdTheme,
			}))
		}
		for _, slot := range live.Tools {
			if slot.Status == durable.ToolSlotDone {
				continue
			}
			argsObj := callArgs[slot.CallId]
			if argsObj == nil {
				argsObj = jsonx.NewObject()
			}
			toolComp := components.NewToolExecutionComponent(slot.Name, slot.CallId, argsObj, nil, nil, d.screen, cwd)
			if slot.Status == durable.ToolSlotRunning {
				toolComp.MarkExecutionStarted()
			}
			d.transcript.AddChild(toolComp)
		}
	}

	if busy {
		d.editor.EmbedWorkingStatus = true
		d.working.SetMessage("Working...")
	} else {
		d.editor.EmbedWorkingStatus = false
	}

	status := fmt.Sprintf("Model: %s/%s | Entries: %d", d.runtime.Bundle.ModelConfig.ModelRef.Provider, d.runtime.Bundle.ModelConfig.ModelRef.ModelId, len(view.Entries))
	if busy {
		status += " | Busy"
	}
	d.durUpdateStatusLine(status)
}

func (d *DurableTui) durDecodeLiveState(raw durable.JsonObject) *durable.LiveState {
	if raw == nil {
		return nil
	}
	var live durable.LiveState
	if err := jsonx.Decode(raw, &live); err != nil {
		return nil
	}
	return &live
}

func (d *DurableTui) durAppendNotice(text string, isErr bool) {
	if isErr && d.activeTheme != nil {
		text = d.activeTheme.Fg(theme.FgError, text)
	} else if d.activeTheme != nil {
		text = d.activeTheme.Fg(theme.FgMuted, text)
	}
	d.transcript.AddChild(tui.NewText(text, 1, 0, nil))
}

func (d *DurableTui) durUpdateStatusLine(text string) {
	if d.activeTheme != nil {
		text = d.activeTheme.Fg(theme.FgMuted, text)
	}
	d.statusLine.SetText(text)
}

func durObjectFromAny(v any) *jsonx.Object {
	if obj, ok := v.(*jsonx.Object); ok && obj != nil {
		return obj
	}
	obj := jsonx.NewObject()
	_ = jsonx.Decode(v, obj)
	return obj
}

// PickDurableSession prompts interactively for a durable session path from sessionDir.
func PickDurableSession(ctx context.Context, sessionDir string) (string, error) {
	sessions, err := ListDurableSessions(ctx, sessionDir)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return NewDurableSessionPath(sessionDir), nil
	}

	_, _ = theme.InitTheme("", false)
	term := tui.NewProcessTerminal()
	showCursor := false
	screen := tui.NewTuiAltScreen(term, &showCursor, nil, nil)

	items := make([]tui.SelectItem, 0, len(sessions)+1)
	items = append(items, tui.SelectItem{
		Value: "__new__",
		Label: "New durable session",
	})
	for _, s := range sessions {
		desc := fmt.Sprintf("%s (%d entries)", s.UpdatedAt.Local().Format(time.RFC822), s.EntryCount)
		if s.Locked {
			desc += " [locked]"
		}
		label := s.FirstText
		if label == "" {
			label = s.Id
		}
		items = append(items, tui.SelectItem{
			Value:       s.Path,
			Label:       label,
			Description: &desc,
		})
	}

	list := tui.NewSelectList(items, 12, theme.GetSelectListTheme(), nil)
	doneCh := make(chan string, 1)
	list.OnSelect = func(item tui.SelectItem) {
		val := item.Value
		if val == "__new__" {
			val = NewDurableSessionPath(sessionDir)
		}
		select {
		case doneCh <- val:
		default:
		}
	}
	list.OnCancel = func() {
		select {
		case doneCh <- "":
		default:
		}
	}

	container := tui.NewVStack([]tui.StackChild{
		&tui.StackChildComponent{Component: tui.NewText("Select Durable Session", 1, 1, nil)},
		&tui.StackChildComponent{Component: list},
	}, nil)
	screen.SetLayoutRoot(container)
	screen.SetFocus(list)
	screen.Start()
	defer screen.Stop(nil)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case picked := <-doneCh:
		if picked == "" {
			return "", fmt.Errorf("session selection cancelled")
		}
		return picked, nil
	}
}
