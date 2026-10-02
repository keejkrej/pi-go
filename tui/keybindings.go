// Ported from packages/tui/src/keybindings.ts (pi v1.0.0).

package tui

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

// Keybindings is the TS declaration-merging registry of action ids.
// Each TS property is the literal true. Go cannot merge that interface across
// packages. Built-in tui ids are the Keybinding constants below. Other packages
// add ids with the same Keybinding string type (for example "app.interrupt").
type Keybindings struct{}

// Keybinding is one action id (TS keyof Keybindings). The set is open.
type Keybinding string

const (
	KeybindingTuiEditorCursorUp           Keybinding = "tui.editor.cursorUp"
	KeybindingTuiEditorCursorDown         Keybinding = "tui.editor.cursorDown"
	KeybindingTuiEditorHistoryPrevious    Keybinding = "tui.editor.historyPrevious"
	KeybindingTuiEditorHistoryNext        Keybinding = "tui.editor.historyNext"
	KeybindingTuiEditorCursorLeft         Keybinding = "tui.editor.cursorLeft"
	KeybindingTuiEditorCursorRight        Keybinding = "tui.editor.cursorRight"
	KeybindingTuiEditorCursorWordLeft     Keybinding = "tui.editor.cursorWordLeft"
	KeybindingTuiEditorCursorWordRight    Keybinding = "tui.editor.cursorWordRight"
	KeybindingTuiEditorCursorLineStart    Keybinding = "tui.editor.cursorLineStart"
	KeybindingTuiEditorCursorLineEnd      Keybinding = "tui.editor.cursorLineEnd"
	KeybindingTuiEditorJumpForward        Keybinding = "tui.editor.jumpForward"
	KeybindingTuiEditorJumpBackward       Keybinding = "tui.editor.jumpBackward"
	KeybindingTuiEditorPageUp             Keybinding = "tui.editor.pageUp"
	KeybindingTuiEditorPageDown           Keybinding = "tui.editor.pageDown"
	KeybindingTuiEditorDeleteCharBackward Keybinding = "tui.editor.deleteCharBackward"
	KeybindingTuiEditorDeleteCharForward  Keybinding = "tui.editor.deleteCharForward"
	KeybindingTuiEditorDeleteWordBackward Keybinding = "tui.editor.deleteWordBackward"
	KeybindingTuiEditorDeleteWordForward  Keybinding = "tui.editor.deleteWordForward"
	KeybindingTuiEditorDeleteToLineStart  Keybinding = "tui.editor.deleteToLineStart"
	KeybindingTuiEditorDeleteToLineEnd    Keybinding = "tui.editor.deleteToLineEnd"
	KeybindingTuiEditorYank               Keybinding = "tui.editor.yank"
	KeybindingTuiEditorYankPop            Keybinding = "tui.editor.yankPop"
	KeybindingTuiEditorUndo               Keybinding = "tui.editor.undo"
	KeybindingTuiInputNewLine             Keybinding = "tui.input.newLine"
	KeybindingTuiInputSubmit              Keybinding = "tui.input.submit"
	KeybindingTuiInputTab                 Keybinding = "tui.input.tab"
	KeybindingTuiInputCopy                Keybinding = "tui.input.copy"
	KeybindingTuiSelectUp                 Keybinding = "tui.select.up"
	KeybindingTuiSelectDown               Keybinding = "tui.select.down"
	KeybindingTuiSelectPageUp             Keybinding = "tui.select.pageUp"
	KeybindingTuiSelectPageDown           Keybinding = "tui.select.pageDown"
	KeybindingTuiSelectConfirm            Keybinding = "tui.select.confirm"
	KeybindingTuiSelectCancel             Keybinding = "tui.select.cancel"
	KeybindingTuiAltScreenPageUp          Keybinding = "tui.altScreen.pageUp"
	KeybindingTuiAltScreenPageDown        Keybinding = "tui.altScreen.pageDown"
	KeybindingTuiAltScreenHalfPageUp      Keybinding = "tui.altScreen.halfPageUp"
	KeybindingTuiAltScreenHalfPageDown    Keybinding = "tui.altScreen.halfPageDown"
	KeybindingTuiAltScreenLineUp          Keybinding = "tui.altScreen.lineUp"
	KeybindingTuiAltScreenLineDown        Keybinding = "tui.altScreen.lineDown"
	KeybindingTuiAltScreenPreviousPrompt  Keybinding = "tui.altScreen.previousPrompt"
	KeybindingTuiAltScreenNextPrompt      Keybinding = "tui.altScreen.nextPrompt"
	KeybindingTuiAltScreenSearch          Keybinding = "tui.altScreen.search"
	KeybindingTuiAltScreenSearchNext      Keybinding = "tui.altScreen.searchNext"
	KeybindingTuiAltScreenSearchPrevious  Keybinding = "tui.altScreen.searchPrevious"
	KeybindingTuiAltScreenSearchClose     Keybinding = "tui.altScreen.searchClose"
	KeybindingTuiAltScreenTop             Keybinding = "tui.altScreen.top"
	KeybindingTuiAltScreenBottom          Keybinding = "tui.altScreen.bottom"
)

// KeybindingKeys is KeyId | KeyId[].
// A non-nil Single is the string form. Otherwise Keys is the array form,
// including a non-nil empty slice for []. Nil Single and nil Keys is also [].
// A nil *KeybindingKeys, or a missing map key, means undefined.
type KeybindingKeys struct {
	Single *KeyId
	Keys   []KeyId
}

// KeybindingDefinition is one action's default keys and description.
// Field order is defaultKeys, description.
type KeybindingDefinition struct {
	DefaultKeys KeybindingKeys `json:"defaultKeys"`
	Description *string        `json:"description,omitzero"`
}

// KeybindingDefinitions is the action table (TS Record<string, KeybindingDefinition>).
// Iteration order is insertion order.
type KeybindingDefinitions = *omap.Map[string, *KeybindingDefinition]

// KeybindingsConfig is user or resolved bindings
// (TS Record<string, KeyId | KeyId[] | undefined>).
// A stored nil *KeybindingKeys is an explicit undefined value.
// Iteration order is insertion order.
type KeybindingsConfig = *omap.Map[string, *KeybindingKeys]

// KeybindingConflict is one key claimed by more than one user binding.
// Field order is key, keybindings.
type KeybindingConflict struct {
	Key         KeyId    `json:"key"`
	Keybindings []string `json:"keybindings"`
}

// TuiKeybindings is the built-in tui action table (TS TUI_KEYBINDINGS).
var TuiKeybindings = keybTuiKeybindings()

// KeybindingsManager resolves default and user key bindings.
// mu guards the fields. Do not hold it across MatchesKey.
type KeybindingsManager struct {
	mu           sync.Mutex
	definitions  KeybindingDefinitions
	userBindings KeybindingsConfig
	keysById     map[Keybinding][]KeyId
	conflicts    []KeybindingConflict
}

// NewKeybindingsManager builds a manager. Nil userBindings means no user config.
func NewKeybindingsManager(definitions KeybindingDefinitions, userBindings KeybindingsConfig) *KeybindingsManager {
	panic("unported: NewKeybindingsManager")
}

func (m *KeybindingsManager) rebuild() {
	panic("unported: KeybindingsManager.rebuild")
}

// Matches reports whether data matches any key bound to keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding Keybinding) bool {
	panic("unported: KeybindingsManager.Matches")
}

// GetKeys returns a copy of the keys bound to keybinding.
func (m *KeybindingsManager) GetKeys(keybinding Keybinding) []KeyId {
	panic("unported: KeybindingsManager.GetKeys")
}

// GetDefinition returns the definition for keybinding. Nil if it is not in the table.
func (m *KeybindingsManager) GetDefinition(keybinding Keybinding) *KeybindingDefinition {
	panic("unported: KeybindingsManager.GetDefinition")
}

// GetConflicts returns a copy of user-binding conflicts.
func (m *KeybindingsManager) GetConflicts() []KeybindingConflict {
	panic("unported: KeybindingsManager.GetConflicts")
}

// SetUserBindings replaces the user config and rebuilds.
func (m *KeybindingsManager) SetUserBindings(userBindings KeybindingsConfig) {
	panic("unported: KeybindingsManager.SetUserBindings")
}

// GetUserBindings returns a shallow copy of the user config.
func (m *KeybindingsManager) GetUserBindings() KeybindingsConfig {
	panic("unported: KeybindingsManager.GetUserBindings")
}

// GetResolvedBindings returns the effective keys for every defined action.
// One key is the string form; any other length, including zero, is the array form.
func (m *KeybindingsManager) GetResolvedBindings() KeybindingsConfig {
	panic("unported: KeybindingsManager.GetResolvedBindings")
}

// keybGlobal is the process-wide manager. Nil until SetKeybindings or GetKeybindings.
var keybGlobal *KeybindingsManager

// SetKeybindings installs the process-wide manager.
func SetKeybindings(keybindings *KeybindingsManager) {
	panic("unported: SetKeybindings")
}

// GetKeybindings returns the process-wide manager, creating one from TuiKeybindings on first use.
func GetKeybindings() *KeybindingsManager {
	panic("unported: GetKeybindings")
}

func keybTuiKeybindings() KeybindingDefinitions {
	m := omap.NewMap[string, *KeybindingDefinition]()
	m.Set(string(KeybindingTuiEditorCursorUp), &KeybindingDefinition{
		DefaultKeys: keybOne("up"),
		Description: keybDesc("Move cursor up"),
	})
	m.Set(string(KeybindingTuiEditorCursorDown), &KeybindingDefinition{
		DefaultKeys: keybOne("down"),
		Description: keybDesc("Move cursor down"),
	})
	m.Set(string(KeybindingTuiEditorHistoryPrevious), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Select previous prompt history entry"),
	})
	m.Set(string(KeybindingTuiEditorHistoryNext), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Select next prompt history entry"),
	})
	m.Set(string(KeybindingTuiEditorCursorLeft), &KeybindingDefinition{
		DefaultKeys: keybMany("left", "ctrl+b"),
		Description: keybDesc("Move cursor left"),
	})
	m.Set(string(KeybindingTuiEditorCursorRight), &KeybindingDefinition{
		DefaultKeys: keybMany("right", "ctrl+f"),
		Description: keybDesc("Move cursor right"),
	})
	m.Set(string(KeybindingTuiEditorCursorWordLeft), &KeybindingDefinition{
		DefaultKeys: keybMany("alt+left", "ctrl+left", "alt+b"),
		Description: keybDesc("Move cursor word left"),
	})
	m.Set(string(KeybindingTuiEditorCursorWordRight), &KeybindingDefinition{
		DefaultKeys: keybMany("alt+right", "ctrl+right", "alt+f"),
		Description: keybDesc("Move cursor word right"),
	})
	m.Set(string(KeybindingTuiEditorCursorLineStart), &KeybindingDefinition{
		DefaultKeys: keybMany("home", "ctrl+home", "ctrl+a"),
		Description: keybDesc("Move to line start"),
	})
	m.Set(string(KeybindingTuiEditorCursorLineEnd), &KeybindingDefinition{
		DefaultKeys: keybMany("end", "ctrl+end", "ctrl+e"),
		Description: keybDesc("Move to line end"),
	})
	m.Set(string(KeybindingTuiEditorJumpForward), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+]"),
		Description: keybDesc("Jump forward to character"),
	})
	m.Set(string(KeybindingTuiEditorJumpBackward), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+alt+]"),
		Description: keybDesc("Jump backward to character"),
	})
	m.Set(string(KeybindingTuiEditorPageUp), &KeybindingDefinition{
		DefaultKeys: keybMany("pageUp", "ctrl+pageUp"),
		Description: keybDesc("Page up"),
	})
	m.Set(string(KeybindingTuiEditorPageDown), &KeybindingDefinition{
		DefaultKeys: keybMany("pageDown", "ctrl+pageDown"),
		Description: keybDesc("Page down"),
	})
	m.Set(string(KeybindingTuiEditorDeleteCharBackward), &KeybindingDefinition{
		DefaultKeys: keybOne("backspace"),
		Description: keybDesc("Delete character backward"),
	})
	m.Set(string(KeybindingTuiEditorDeleteCharForward), &KeybindingDefinition{
		DefaultKeys: keybMany("delete", "ctrl+d"),
		Description: keybDesc("Delete character forward"),
	})
	m.Set(string(KeybindingTuiEditorDeleteWordBackward), &KeybindingDefinition{
		DefaultKeys: keybMany("ctrl+w", "alt+backspace"),
		Description: keybDesc("Delete word backward"),
	})
	m.Set(string(KeybindingTuiEditorDeleteWordForward), &KeybindingDefinition{
		DefaultKeys: keybMany("alt+d", "alt+delete"),
		Description: keybDesc("Delete word forward"),
	})
	m.Set(string(KeybindingTuiEditorDeleteToLineStart), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+u"),
		Description: keybDesc("Delete to line start"),
	})
	m.Set(string(KeybindingTuiEditorDeleteToLineEnd), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+k"),
		Description: keybDesc("Delete to line end"),
	})
	m.Set(string(KeybindingTuiEditorYank), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+y"),
		Description: keybDesc("Yank"),
	})
	m.Set(string(KeybindingTuiEditorYankPop), &KeybindingDefinition{
		DefaultKeys: keybOne("alt+y"),
		Description: keybDesc("Yank pop"),
	})
	m.Set(string(KeybindingTuiEditorUndo), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+-"),
		Description: keybDesc("Undo"),
	})
	m.Set(string(KeybindingTuiInputNewLine), &KeybindingDefinition{
		DefaultKeys: keybMany("shift+enter", "ctrl+j"),
		Description: keybDesc("Insert newline"),
	})
	m.Set(string(KeybindingTuiInputSubmit), &KeybindingDefinition{
		DefaultKeys: keybOne("enter"),
		Description: keybDesc("Submit input"),
	})
	m.Set(string(KeybindingTuiInputTab), &KeybindingDefinition{
		DefaultKeys: keybOne("tab"),
		Description: keybDesc("Tab / autocomplete"),
	})
	m.Set(string(KeybindingTuiInputCopy), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+c"),
		Description: keybDesc("Copy selection"),
	})
	m.Set(string(KeybindingTuiSelectUp), &KeybindingDefinition{
		DefaultKeys: keybOne("up"),
		Description: keybDesc("Move selection up"),
	})
	m.Set(string(KeybindingTuiSelectDown), &KeybindingDefinition{
		DefaultKeys: keybOne("down"),
		Description: keybDesc("Move selection down"),
	})
	m.Set(string(KeybindingTuiSelectPageUp), &KeybindingDefinition{
		DefaultKeys: keybOne("pageUp"),
		Description: keybDesc("Selection page up"),
	})
	m.Set(string(KeybindingTuiSelectPageDown), &KeybindingDefinition{
		DefaultKeys: keybOne("pageDown"),
		Description: keybDesc("Selection page down"),
	})
	m.Set(string(KeybindingTuiSelectConfirm), &KeybindingDefinition{
		DefaultKeys: keybOne("enter"),
		Description: keybDesc("Confirm selection"),
	})
	m.Set(string(KeybindingTuiSelectCancel), &KeybindingDefinition{
		DefaultKeys: keybMany("escape", "ctrl+c"),
		Description: keybDesc("Cancel selection"),
	})
	// These intentionally shadow the unmodified editor bindings in fullscreen mode.
	m.Set(string(KeybindingTuiAltScreenPageUp), &KeybindingDefinition{
		DefaultKeys: keybOne("pageUp"),
		Description: keybDesc("Scroll viewport up one page"),
	})
	m.Set(string(KeybindingTuiAltScreenPageDown), &KeybindingDefinition{
		DefaultKeys: keybOne("pageDown"),
		Description: keybDesc("Scroll viewport down one page"),
	})
	m.Set(string(KeybindingTuiAltScreenHalfPageUp), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Scroll viewport up half a page"),
	})
	m.Set(string(KeybindingTuiAltScreenHalfPageDown), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Scroll viewport down half a page"),
	})
	m.Set(string(KeybindingTuiAltScreenLineUp), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Scroll viewport up one line"),
	})
	m.Set(string(KeybindingTuiAltScreenLineDown), &KeybindingDefinition{
		DefaultKeys: keybNone(),
		Description: keybDesc("Scroll viewport down one line"),
	})
	m.Set(string(KeybindingTuiAltScreenPreviousPrompt), &KeybindingDefinition{
		DefaultKeys: keybMany("ctrl+shift+up", "ctrl+up"),
		Description: keybDesc("Jump to previous semantic prompt"),
	})
	m.Set(string(KeybindingTuiAltScreenNextPrompt), &KeybindingDefinition{
		DefaultKeys: keybMany("ctrl+shift+down", "ctrl+down"),
		Description: keybDesc("Jump to next semantic prompt"),
	})
	m.Set(string(KeybindingTuiAltScreenSearch), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+shift+f"),
		Description: keybDesc("Search the primary scroll view"),
	})
	m.Set(string(KeybindingTuiAltScreenSearchNext), &KeybindingDefinition{
		DefaultKeys: keybMany("enter", "ctrl+g"),
		Description: keybDesc("Select the next search match"),
	})
	m.Set(string(KeybindingTuiAltScreenSearchPrevious), &KeybindingDefinition{
		DefaultKeys: keybMany("shift+enter", "ctrl+shift+g"),
		Description: keybDesc("Select the previous search match"),
	})
	m.Set(string(KeybindingTuiAltScreenSearchClose), &KeybindingDefinition{
		DefaultKeys: keybOne("escape"),
		Description: keybDesc("Close transcript search"),
	})
	m.Set(string(KeybindingTuiAltScreenTop), &KeybindingDefinition{
		DefaultKeys: keybOne("home"),
		Description: keybDesc("Scroll viewport to top"),
	})
	m.Set(string(KeybindingTuiAltScreenBottom), &KeybindingDefinition{
		DefaultKeys: keybOne("end"),
		Description: keybDesc("Scroll viewport to bottom"),
	})
	return m
}

func keybOne(s string) KeybindingKeys {
	k := KeyId(s)
	return KeybindingKeys{Single: &k}
}

func keybMany(ss ...string) KeybindingKeys {
	keys := make([]KeyId, len(ss))
	for i, s := range ss {
		keys[i] = KeyId(s)
	}
	return KeybindingKeys{Keys: keys}
}

func keybNone() KeybindingKeys {
	return KeybindingKeys{Keys: []KeyId{}}
}

func keybDesc(s string) *string {
	return &s
}
