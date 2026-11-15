// Ported from packages/coding-agent/src/modes/interactive/components/extension-editor.ts (pi v1.5.2).

package components

import (
	"os"
	"runtime"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

var extedEditInExternalEditor = EditInExternalEditor

// ExtensionEditorOptions configures the multi-line extension editor dialog and underlying tui.Editor.
type ExtensionEditorOptions struct {
	tui.EditorOptions
	Description *string
}

// ExtensionEditorComponent renders a multi-line editor dialog with external editor support.
type ExtensionEditorComponent struct {
	*tui.Container
	editor                *tui.Editor
	onSubmitCallback      func(value string)
	onCancelCallback      func()
	ui                    tui.TUI
	keybindings           *core.KeybindingsManager
	externalEditorCommand string
	externalEditorStatus  *tui.Text
	focused               bool
}

var _ tui.Focusable = (*ExtensionEditorComponent)(nil)

func extedResolveEditorCommand(explicit *string) string {
	if explicit != nil && *explicit != "" {
		return *explicit
	}
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "nano"
}

// NewExtensionEditorComponent constructs a multi-line editor dialog for extensions and /bug.
func NewExtensionEditorComponent(
	ui tui.TUI,
	keybindings *core.KeybindingsManager,
	title string,
	prefill *string,
	onSubmit func(value string),
	onCancel func(),
	options *ExtensionEditorOptions,
	externalEditorCommand ...*string,
) *ExtensionEditorComponent {
	if keybindings == nil {
		keybindings = core.NewKeybindingsManager(nil)
	}
	var explicitCmd *string
	if len(externalEditorCommand) > 0 {
		explicitCmd = externalEditorCommand[0]
	}

	var editorOpts *tui.EditorOptions
	var description string
	if options != nil {
		copied := options.EditorOptions
		editorOpts = &copied
		if options.Description != nil {
			description = *options.Description
		}
	}

	th := theme.CurrentTheme()
	c := &ExtensionEditorComponent{
		Container:             tui.NewContainer(),
		onSubmitCallback:      onSubmit,
		onCancelCallback:      onCancel,
		ui:                    ui,
		keybindings:           keybindings,
		externalEditorCommand: extedResolveEditorCommand(explicitCmd),
		externalEditorStatus:  tui.NewText("", 1, 0, nil),
	}

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	c.AddChild(tui.NewText(th.Fg(theme.FgAccent, title), 1, 0, nil))
	if description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(th.Fg(theme.FgText, description), 1, 0, nil))
	}
	c.AddChild(tui.NewSpacer(1))

	c.editor = tui.NewEditor(ui, theme.GetEditorTheme(), editorOpts)
	if prefill != nil && *prefill != "" {
		c.editor.SetText(*prefill)
	}
	c.editor.OnSubmit = func(text string) {
		if c.onSubmitCallback != nil {
			c.onSubmitCallback(text)
		}
	}
	c.AddChild(c.editor)
	c.AddChild(c.externalEditorStatus)
	c.AddChild(tui.NewSpacer(1))

	hint := theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "submit") +
		"  " +
		theme.AppKeyHint(th, tui.KeybindingTuiInputNewLine, "newline") +
		"  " +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel") +
		"  " +
		theme.AppKeyHint(th, core.KeybindingAppEditorExternal, "external editor")
	c.AddChild(tui.NewText(hint, 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())
	return c
}

// Focused reports whether the embedded editor holds focus.
func (e *ExtensionEditorComponent) Focused() bool {
	return e.focused
}

// SetFocused propagates focus state to the embedded Editor.
func (e *ExtensionEditorComponent) SetFocused(focused bool) {
	e.focused = focused
	e.editor.SetFocused(focused)
}

// HandleInput processes cancel, external editor launch, and multi-line editing keys.
func (e *ExtensionEditorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, tui.KeybindingTuiSelectCancel) {
		if e.onCancelCallback != nil {
			e.onCancelCallback()
		}
		return
	}
	if e.keybindings.Matches(keyData, core.KeybindingAppEditorExternal) {
		e.handleOpenExternalEditor()
		return
	}
	e.editor.HandleInput(keyData)
}

func (e *ExtensionEditorComponent) handleOpenExternalEditor() {
	content := e.editor.GetText()
	if e.ui != nil {
		e.ui.Stop(nil)
		defer func() {
			e.ui.Start()
			force := true
			e.ui.RequestRender(&force)
		}()
	}
	result := extedEditInExternalEditor(ExternalEditorOptions{
		Command: e.externalEditorCommand,
		Content: content,
	})
	if result.Status == ExternalEditorStatusFailed {
		e.externalEditorStatus.SetText(theme.CurrentTheme().Fg(theme.FgWarning, result.Message))
	} else {
		e.externalEditorStatus.SetText("")
	}
	if result.Status == ExternalEditorStatusComplete {
		e.editor.SetText(result.Content)
	}
}
