// Ported from packages/tui/src/keybindings.ts (pi v1.5.2).

package tui

import (
	"slices"
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

func keybNormalizeKeys(keys *KeybindingKeys) []KeyId {
	if keys == nil {
		return []KeyId{}
	}
	if keys.Single != nil {
		return []KeyId{*keys.Single}
	}
	seen := make(map[KeyId]struct{}, len(keys.Keys))
	result := make([]KeyId, 0, len(keys.Keys))
	for _, key := range keys.Keys {
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}
	return result
}

// NewKeybindingsManager builds a manager. Nil userBindings means no user config.
func NewKeybindingsManager(definitions KeybindingDefinitions, userBindings KeybindingsConfig) *KeybindingsManager {
	if definitions == nil {
		definitions = omap.NewMap[string, *KeybindingDefinition]()
	}
	if userBindings == nil {
		userBindings = omap.NewMap[string, *KeybindingKeys]()
	}
	m := &KeybindingsManager{
		definitions:  definitions,
		userBindings: userBindings,
		keysById:     make(map[Keybinding][]KeyId),
	}
	m.rebuild()
	return m
}

func (m *KeybindingsManager) rebuild() {
	m.keysById = make(map[Keybinding][]KeyId)
	m.conflicts = nil

	userClaims := omap.NewMap[KeyId, *omap.Map[Keybinding, struct{}]]()
	for _, keybinding := range m.userBindings.Keys() {
		if _, ok := m.definitions.Get(keybinding); !ok {
			continue
		}
		keys, _ := m.userBindings.Get(keybinding)
		for _, key := range keybNormalizeKeys(keys) {
			claimants, ok := userClaims.Get(key)
			if !ok {
				claimants = omap.NewMap[Keybinding, struct{}]()
				userClaims.Set(key, claimants)
			}
			claimants.Set(Keybinding(keybinding), struct{}{})
		}
	}

	for _, key := range userClaims.Keys() {
		claimants, _ := userClaims.Get(key)
		if claimants.Len() > 1 {
			ids := make([]string, 0, claimants.Len())
			for _, kb := range claimants.Keys() {
				ids = append(ids, string(kb))
			}
			m.conflicts = append(m.conflicts, KeybindingConflict{
				Key:         key,
				Keybindings: ids,
			})
		}
	}

	for _, id := range m.definitions.Keys() {
		def, _ := m.definitions.Get(id)
		if def == nil {
			continue
		}
		userKeys, hasUser := m.userBindings.Get(id)
		var keys []KeyId
		if !hasUser || userKeys == nil {
			keys = keybNormalizeKeys(&def.DefaultKeys)
		} else {
			keys = keybNormalizeKeys(userKeys)
		}
		m.keysById[Keybinding(id)] = keys
	}
}

// Matches reports whether data matches any key bound to keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding Keybinding) bool {
	m.mu.Lock()
	keys := m.keysById[keybinding]
	m.mu.Unlock()
	for _, key := range keys {
		if MatchesKey(data, key) {
			return true
		}
	}
	return false
}

// GetKeys returns a copy of the keys bound to keybinding.
func (m *KeybindingsManager) GetKeys(keybinding Keybinding) []KeyId {
	m.mu.Lock()
	keys := m.keysById[keybinding]
	m.mu.Unlock()
	if len(keys) == 0 {
		return []KeyId{}
	}
	return slices.Clone(keys)
}

// GetDefinition returns the definition for keybinding. Nil if it is not in the table.
func (m *KeybindingsManager) GetDefinition(keybinding Keybinding) *KeybindingDefinition {
	m.mu.Lock()
	def, _ := m.definitions.Get(string(keybinding))
	m.mu.Unlock()
	return def
}

// GetConflicts returns a copy of user-binding conflicts.
func (m *KeybindingsManager) GetConflicts() []KeybindingConflict {
	m.mu.Lock()
	out := make([]KeybindingConflict, len(m.conflicts))
	for i, c := range m.conflicts {
		out[i] = KeybindingConflict{
			Key:         c.Key,
			Keybindings: slices.Clone(c.Keybindings),
		}
	}
	m.mu.Unlock()
	return out
}

// SetUserBindings replaces the user config and rebuilds.
func (m *KeybindingsManager) SetUserBindings(userBindings KeybindingsConfig) {
	if userBindings == nil {
		userBindings = omap.NewMap[string, *KeybindingKeys]()
	}
	m.mu.Lock()
	m.userBindings = userBindings
	m.rebuild()
	m.mu.Unlock()
}

// GetUserBindings returns a shallow copy of the user config.
func (m *KeybindingsManager) GetUserBindings() KeybindingsConfig {
	m.mu.Lock()
	out := omap.NewMap[string, *KeybindingKeys]()
	if m.userBindings != nil {
		for _, k := range m.userBindings.Keys() {
			v, _ := m.userBindings.Get(k)
			out.Set(k, v)
		}
	}
	m.mu.Unlock()
	return out
}

// GetResolvedBindings returns the effective keys for every defined action.
// One key is the string form; any other length, including zero, is the array form.
func (m *KeybindingsManager) GetResolvedBindings() KeybindingsConfig {
	m.mu.Lock()
	resolved := omap.NewMap[string, *KeybindingKeys]()
	if m.definitions != nil {
		for _, id := range m.definitions.Keys() {
			keys := m.keysById[Keybinding(id)]
			if len(keys) == 1 {
				k := keys[0]
				resolved.Set(id, &KeybindingKeys{Single: &k})
			} else {
				cp := make([]KeyId, len(keys))
				copy(cp, keys)
				resolved.Set(id, &KeybindingKeys{Keys: cp})
			}
		}
	}
	m.mu.Unlock()
	return resolved
}

var (
	keybMu sync.Mutex
	// keybGlobal is the process-wide manager. Nil until SetKeybindings or GetKeybindings.
	keybGlobal *KeybindingsManager
)

// SetKeybindings installs the process-wide manager.
func SetKeybindings(keybindings *KeybindingsManager) {
	keybMu.Lock()
	keybGlobal = keybindings
	keybMu.Unlock()
}

// GetKeybindings returns the process-wide manager, creating one from TuiKeybindings on first use.
func GetKeybindings() *KeybindingsManager {
	keybMu.Lock()
	if keybGlobal == nil {
		keybGlobal = NewKeybindingsManager(TuiKeybindings, nil)
	}
	mgr := keybGlobal
	keybMu.Unlock()
	return mgr
}

// GetSelectPageIndex returns the selection index after a tui.select.pageUp or
// tui.select.pageDown key, moved by pageSize and clamped to 0..itemCount-1.
// Nil means data is not a selection page key, and 0 is returned for a page key on
// an empty list, so callers must still check that the list has items before selecting.
func GetSelectPageIndex(keybindings *KeybindingsManager, data string, selectedIndex, itemCount, pageSize int) *int {
	var direction int
	if keybindings.Matches(data, KeybindingTuiSelectPageUp) {
		direction = -1
	} else if keybindings.Matches(data, KeybindingTuiSelectPageDown) {
		direction = 1
	} else {
		return nil
	}
	idx := selectedIndex + direction*pageSize
	if idx > itemCount-1 {
		idx = itemCount - 1
	}
	if idx < 0 {
		idx = 0
	}
	return &idx
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
		DefaultKeys: keybMany("home", "ctrl+a"),
		Description: keybDesc("Move to line start"),
	})
	m.Set(string(KeybindingTuiEditorCursorLineEnd), &KeybindingDefinition{
		DefaultKeys: keybMany("end", "ctrl+e"),
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
		DefaultKeys: keybOne("ctrl+home"),
		Description: keybDesc("Scroll viewport to top"),
	})
	m.Set(string(KeybindingTuiAltScreenBottom), &KeybindingDefinition{
		DefaultKeys: keybOne("ctrl+end"),
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
