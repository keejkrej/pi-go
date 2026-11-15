package components

import (
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/tui"
)

// CustomEditorOptions configures a CustomEditor instance.
type CustomEditorOptions struct {
	PaddingX               *int
	AutocompleteMaxVisible *int
	EmbedWorkingStatus     *bool
}

// CustomEditor extends tui.Editor with coding-agent keybindings, extension
// shortcut interception, and an optional working status indicator in the top border.
type CustomEditor struct {
	*tui.Editor

	keybindings            *core.KeybindingsManager
	workingStatusIndicator *StatusIndicator

	EmbedWorkingStatus  bool
	ActionHandlers      map[tui.Keybinding]func()
	OnEscape            func()
	OnCtrlD             func()
	OnPasteImage        func()
	OnExtensionShortcut func(data string) bool
}

// NewCustomEditor constructs a CustomEditor wired with TUI overrides.
func NewCustomEditor(
	ui tui.TUI,
	editorTheme tui.EditorTheme,
	keybindings *core.KeybindingsManager,
	options ...*CustomEditorOptions,
) *CustomEditor {
	embedWorkingStatus := true
	var editorOpts *tui.EditorOptions
	if len(options) > 0 && options[0] != nil {
		opt := options[0]
		if opt.EmbedWorkingStatus != nil {
			embedWorkingStatus = *opt.EmbedWorkingStatus
		}
		if opt.PaddingX != nil || opt.AutocompleteMaxVisible != nil {
			editorOpts = &tui.EditorOptions{
				PaddingX:               opt.PaddingX,
				AutocompleteMaxVisible: opt.AutocompleteMaxVisible,
			}
		}
	}

	e := &CustomEditor{
		Editor:             tui.NewEditor(ui, editorTheme, editorOpts),
		keybindings:        keybindings,
		EmbedWorkingStatus: embedWorkingStatus,
		ActionHandlers:     make(map[tui.Keybinding]func()),
	}
	e.Editor.SetOverrides(e)
	return e
}

// SetWorkingStatusIndicator embeds or clears the active status indicator in the top border.
func (e *CustomEditor) SetWorkingStatusIndicator(indicator *StatusIndicator) {
	e.workingStatusIndicator = indicator
	e.Invalidate()
}

// OnAction registers a handler invoked when an app keybinding matches.
func (e *CustomEditor) OnAction(action tui.Keybinding, handler func()) {
	if e.ActionHandlers == nil {
		e.ActionHandlers = make(map[tui.Keybinding]func())
	}
	if handler == nil {
		delete(e.ActionHandlers, action)
		return
	}
	e.ActionHandlers[action] = handler
}

// RenderTopBorder embeds the working status indicator into the editor top border when active.
func (e *CustomEditor) RenderTopBorder(width, hiddenLineCount int) string {
	if !e.EmbedWorkingStatus || e.workingStatusIndicator == nil || hiddenLineCount > 0 || width < 8 {
		return e.Editor.RenderTopBorder(width, hiddenLineCount)
	}

	borderFn := e.Editor.BorderColor
	if borderFn == nil {
		borderFn = func(s string) string { return s }
	}

	// Reserve "── " (3) + status + " " (1) + at least "──" (2) = 6 cells of chrome.
	maxStatusWidth := width - 6
	rendered := e.workingStatusIndicator.RenderInBorder(maxStatusWidth)
	if rendered == "" || tui.VisibleWidth(rendered) > maxStatusWidth {
		rendered = e.workingStatusIndicator.RenderSpinnerInBorder(maxStatusWidth)
	}
	if rendered == "" {
		return e.Editor.RenderTopBorder(width, hiddenLineCount)
	}

	statusWidth := tui.VisibleWidth(rendered)
	rightLen := max(0, width-3-statusWidth-1)
	if rightLen < 2 {
		return e.Editor.RenderTopBorder(width, hiddenLineCount)
	}
	return borderFn("── ") + rendered + borderFn(" "+strings.Repeat("─", rightLen))
}

// RenderBottomBorder delegates bottom border rendering to the embedded Editor.
func (e *CustomEditor) RenderBottomBorder(width, hiddenLineCount int) string {
	return e.Editor.RenderBottomBorder(width, hiddenLineCount)
}

// HandleInput prioritizes extension shortcuts, autocomplete pagination, app
// keybindings (only deferring app.interrupt to autocomplete cancellation),
// explicit history navigation, and finally delegates remaining keystrokes to tui.Editor.
func (e *CustomEditor) HandleInput(data string) {
	// Intercept extension-registered keyboard shortcuts first.
	if e.OnExtensionShortcut != nil && e.OnExtensionShortcut(data) {
		return
	}

	// Pass through autocomplete page navigation before checking app keybindings.
	if e.IsShowingAutocomplete() &&
		(e.ceMatches(data, tui.KeybindingTuiSelectPageUp) || e.ceMatches(data, tui.KeybindingTuiSelectPageDown)) {
		e.Editor.HandleInput(data)
		return
	}

	// Paste image keybinding.
	if e.ceMatches(data, core.KeybindingAppClipboardPasteImage) {
		if e.OnPasteImage != nil {
			e.OnPasteImage()
		} else if h, ok := e.ActionHandlers[core.KeybindingAppClipboardPasteImage]; ok && h != nil {
			h()
		}
		return
	}

	// Interrupt cancels autocomplete when open, otherwise runs OnEscape / app.interrupt.
	if e.ceMatches(data, core.KeybindingAppInterrupt) {
		if !e.IsShowingAutocomplete() {
			if e.OnEscape != nil {
				e.OnEscape()
			} else if h, ok := e.ActionHandlers[core.KeybindingAppInterrupt]; ok && h != nil {
				h()
			}
			return
		}
		e.Editor.HandleInput(data)
		return
	}

	// Exit (Ctrl+D) exits when buffer is empty regardless of autocomplete; otherwise falls through to delete-char-forward.
	if e.ceMatches(data, core.KeybindingAppExit) {
		if len(e.GetText()) == 0 {
			if e.OnCtrlD != nil {
				e.OnCtrlD()
			} else if h, ok := e.ActionHandlers[core.KeybindingAppExit]; ok && h != nil {
				h()
			}
			return
		}
	}

	// Explicit editor history navigation takes precedence over overlapping app keybindings.
	if e.ceMatches(data, tui.KeybindingTuiEditorHistoryPrevious) || e.ceMatches(data, tui.KeybindingTuiEditorHistoryNext) {
		e.Editor.HandleInput(data)
		return
	}

	// App action handlers (including app.clear / Ctrl+C) fire even when autocomplete is open.
	if len(e.ActionHandlers) > 0 {
		actions := make([]string, 0, len(e.ActionHandlers))
		for act := range e.ActionHandlers {
			if act == core.KeybindingAppClipboardPasteImage || act == core.KeybindingAppInterrupt || act == core.KeybindingAppExit {
				continue
			}
			actions = append(actions, string(act))
		}
		sort.Strings(actions)
		for _, name := range actions {
			act := tui.Keybinding(name)
			if e.ceMatches(data, act) {
				if h := e.ActionHandlers[act]; h != nil {
					h()
				}
				return
			}
		}
	}

	e.Editor.HandleInput(data)
}

func (e *CustomEditor) ceMatches(data string, action tui.Keybinding) bool {
	return e.keybindings != nil && e.keybindings.Matches(data, action)
}
