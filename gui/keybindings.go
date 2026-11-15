package gui

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// EditorAction identifies an input/editing/navigation action mirroring tui keybindings.
type EditorAction string

const (
	ActionCursorUp           EditorAction = "tui.editor.cursorUp"
	ActionCursorDown         EditorAction = "tui.editor.cursorDown"
	ActionHistoryPrevious    EditorAction = "tui.editor.historyPrevious"
	ActionHistoryNext        EditorAction = "tui.editor.historyNext"
	ActionCursorLeft         EditorAction = "tui.editor.cursorLeft"
	ActionCursorRight        EditorAction = "tui.editor.cursorRight"
	ActionCursorWordLeft     EditorAction = "tui.editor.cursorWordLeft"
	ActionCursorWordRight    EditorAction = "tui.editor.cursorWordRight"
	ActionCursorLineStart    EditorAction = "tui.editor.cursorLineStart"
	ActionCursorLineEnd      EditorAction = "tui.editor.cursorLineEnd"
	ActionJumpForward        EditorAction = "tui.editor.jumpForward"
	ActionJumpBackward       EditorAction = "tui.editor.jumpBackward"
	ActionPageUp             EditorAction = "tui.editor.pageUp"
	ActionPageDown           EditorAction = "tui.editor.pageDown"
	ActionDeleteCharBackward EditorAction = "tui.editor.deleteCharBackward"
	ActionDeleteCharForward  EditorAction = "tui.editor.deleteCharForward"
	ActionDeleteWordBackward EditorAction = "tui.editor.deleteWordBackward"
	ActionDeleteWordForward  EditorAction = "tui.editor.deleteWordForward"
	ActionDeleteToLineStart  EditorAction = "tui.editor.deleteToLineStart"
	ActionDeleteToLineEnd    EditorAction = "tui.editor.deleteToLineEnd"
	ActionYank               EditorAction = "tui.editor.yank"
	ActionYankPop            EditorAction = "tui.editor.yankPop"
	ActionUndo               EditorAction = "tui.editor.undo"
	ActionRedo               EditorAction = "tui.editor.redo"

	ActionNewLine EditorAction = "tui.input.newLine"
	ActionSubmit  EditorAction = "tui.input.submit"
	ActionTab     EditorAction = "tui.input.tab"
	ActionCopy    EditorAction = "tui.input.copy"

	ActionSelectUp       EditorAction = "tui.select.up"
	ActionSelectDown     EditorAction = "tui.select.down"
	ActionSelectPageUp   EditorAction = "tui.select.pageUp"
	ActionSelectPageDown EditorAction = "tui.select.pageDown"
	ActionSelectConfirm  EditorAction = "tui.select.confirm"
	ActionSelectCancel   EditorAction = "tui.select.cancel"

	ActionSearch         EditorAction = "tui.altScreen.search"
	ActionSearchNext     EditorAction = "tui.altScreen.searchNext"
	ActionSearchPrevious EditorAction = "tui.altScreen.searchPrevious"
	ActionSearchClose    EditorAction = "tui.altScreen.searchClose"
	ActionScrollTop      EditorAction = "tui.altScreen.top"
	ActionScrollBottom   EditorAction = "tui.altScreen.bottom"
)

// AppAction identifies an application-level shortcut mirroring coding-agent's
// keybinding registry.
type AppAction string

const (
	AppInterrupt                AppAction = "app.interrupt"
	AppClear                    AppAction = "app.clear"
	AppExit                     AppAction = "app.exit"
	AppSuspend                  AppAction = "app.suspend"
	AppThinkingCycle            AppAction = "app.thinking.cycle"
	AppThinkingSave             AppAction = "app.thinking.save"
	AppModelCycleForward        AppAction = "app.model.cycleForward"
	AppModelCycleBackward       AppAction = "app.model.cycleBackward"
	AppModelSelect              AppAction = "app.model.select"
	AppToolsExpand              AppAction = "app.tools.expand"
	AppThinkingToggle           AppAction = "app.thinking.toggle"
	AppSessionToggleNamedFilter AppAction = "app.session.toggleNamedFilter"
	AppEditorExternal           AppAction = "app.editor.external"
	AppMessageCopy              AppAction = "app.message.copy"
	AppMessageFollowUp          AppAction = "app.message.followUp"
	AppMessageDequeue           AppAction = "app.message.dequeue"
	AppClipboardPasteImage      AppAction = "app.clipboard.pasteImage"
	AppSessionNew               AppAction = "app.session.new"
	AppSessionTree              AppAction = "app.session.tree"
	AppSessionFork              AppAction = "app.session.fork"
	AppSessionResume            AppAction = "app.session.resume"
	AppTreeFoldOrUp             AppAction = "app.tree.foldOrUp"
	AppTreeUnfoldOrDown         AppAction = "app.tree.unfoldOrDown"
	AppTreeEditLabel            AppAction = "app.tree.editLabel"
	AppTreeToggleLabelTimestamp AppAction = "app.tree.toggleLabelTimestamp"
	AppSessionTogglePath        AppAction = "app.session.togglePath"
	AppSessionToggleSort        AppAction = "app.session.toggleSort"
	AppSessionRename            AppAction = "app.session.rename"
	AppSessionDelete            AppAction = "app.session.delete"
	AppSessionDeleteNoninvasive AppAction = "app.session.deleteNoninvasive"
	AppModelsSave               AppAction = "app.models.save"
	AppModelsEnableAll          AppAction = "app.models.enableAll"
	AppModelsClearAll           AppAction = "app.models.clearAll"
	AppModelsToggleProvider     AppAction = "app.models.toggleProvider"
	AppModelsReorderUp          AppAction = "app.models.reorderUp"
	AppModelsReorderDown        AppAction = "app.models.reorderDown"
	AppTreeFilterDefault        AppAction = "app.tree.filter.default"
	AppTreeFilterNoTools        AppAction = "app.tree.filter.noTools"
	AppTreeFilterUserOnly       AppAction = "app.tree.filter.userOnly"
	AppTreeFilterLabeledOnly    AppAction = "app.tree.filter.labeledOnly"
	AppTreeFilterAll            AppAction = "app.tree.filter.all"
	AppTreeFilterCycleForward   AppAction = "app.tree.filter.cycleForward"
	AppTreeFilterCycleBackward  AppAction = "app.tree.filter.cycleBackward"
)

// Shortcut binds a normalized MyGo modifier mask + key to a textual spec.
type Shortcut struct {
	Mods ui.Modifiers
	Key  ui.Key
	Raw  string
}

// MatchesKey checks whether (mods, key) matches this shortcut exactly.
func (s Shortcut) MatchesKey(mods ui.Modifiers, key ui.Key) bool {
	return s.Key != ui.KeyUnknown && s.Key == key && s.Mods == mods
}

// MatchesTextKey reports whether k matches this shortcut.
func (s Shortcut) MatchesTextKey(k ui.TextKey) bool {
	return s.MatchesKey(k.Mods, k.Key)
}

// MatchesContext checks c.Shortcut(s.Mods, s.Key) during an immediate-mode build pass.
func (s Shortcut) MatchesContext(c *ui.Context) bool {
	if c == nil || s.Key == ui.KeyUnknown {
		return false
	}
	return c.Shortcut(s.Mods, s.Key)
}

// MatchesOverlay checks el.OverlayShortcut(s.Mods, s.Key).
func (s Shortcut) MatchesOverlay(el ui.Element) bool {
	if s.Key == ui.KeyUnknown {
		return false
	}
	return el.OverlayShortcut(s.Mods, s.Key)
}

// String returns the canonical normalized shortcut string (e.g. "shift+enter").
func (s Shortcut) String() string {
	var parts []string
	if s.Mods&ui.Ctrl != 0 {
		parts = append(parts, "ctrl")
	}
	if s.Mods&ui.Alt != 0 {
		parts = append(parts, "alt")
	}
	if s.Mods&ui.Shift != 0 {
		parts = append(parts, "shift")
	}
	if s.Mods&ui.Super != 0 {
		if runtime.GOOS == "darwin" {
			parts = append(parts, "cmd")
		} else {
			parts = append(parts, "super")
		}
	}
	if name := keyToName(s.Key); name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, "+")
}

// DisplayString returns a human-readable hint string formatted for UI labels.
func (s Shortcut) DisplayString() string {
	var parts []string
	if s.Mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if s.Mods&ui.Alt != 0 {
		if runtime.GOOS == "darwin" {
			parts = append(parts, "Option")
		} else {
			parts = append(parts, "Alt")
		}
	}
	if s.Mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if s.Mods&ui.Super != 0 {
		if runtime.GOOS == "darwin" {
			parts = append(parts, "Cmd")
		} else {
			parts = append(parts, "Super")
		}
	}
	if name := keyDisplayName(s.Key); name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, "+")
}

var nameToKey = map[string]ui.Key{
	"enter":        ui.KeyEnter,
	"return":       ui.KeyEnter,
	"escape":       ui.KeyEscape,
	"esc":          ui.KeyEscape,
	"backspace":    ui.KeyBackspace,
	"tab":          ui.KeyTab,
	"space":        ui.KeySpace,
	" ":            ui.KeySpace,
	"delete":       ui.KeyDelete,
	"del":          ui.KeyDelete,
	"insert":       ui.KeyInsert,
	"home":         ui.KeyHome,
	"end":          ui.KeyEnd,
	"pageup":       ui.KeyPageUp,
	"pagedown":     ui.KeyPageDown,
	"left":         ui.KeyLeft,
	"right":        ui.KeyRight,
	"up":           ui.KeyUp,
	"down":         ui.KeyDown,
	"f1":           ui.KeyF1,
	"f2":           ui.KeyF2,
	"f3":           ui.KeyF3,
	"f4":           ui.KeyF4,
	"f5":           ui.KeyF5,
	"f6":           ui.KeyF6,
	"f7":           ui.KeyF7,
	"f8":           ui.KeyF8,
	"f9":           ui.KeyF9,
	"f10":          ui.KeyF10,
	"f11":          ui.KeyF11,
	"f12":          ui.KeyF12,
	"-":            ui.KeyMinus,
	"minus":        ui.KeyMinus,
	"=":            ui.KeyEqual,
	"equal":        ui.KeyEqual,
	",":            ui.KeyComma,
	"comma":        ui.KeyComma,
	".":            ui.KeyPeriod,
	"period":       ui.KeyPeriod,
	"/":            ui.KeySlash,
	"slash":        ui.KeySlash,
	";":            ui.KeySemicolon,
	"semicolon":    ui.KeySemicolon,
	"'":            ui.KeyQuote,
	"quote":        ui.KeyQuote,
	"[":            ui.KeyBracketLeft,
	"bracketleft":  ui.KeyBracketLeft,
	"]":            ui.KeyBracketRight,
	"bracketright": ui.KeyBracketRight,
	"\\":           ui.KeyBackslash,
	"backslash":    ui.KeyBackslash,
	"`":            ui.KeyBackquote,
	"backquote":    ui.KeyBackquote,
	"contextmenu":  ui.KeyContextMenu,
	"0":            ui.Key0,
	"1":            ui.Key1,
	"2":            ui.Key2,
	"3":            ui.Key3,
	"4":            ui.Key4,
	"5":            ui.Key5,
	"6":            ui.Key6,
	"7":            ui.Key7,
	"8":            ui.Key8,
	"9":            ui.Key9,
	"a":            ui.KeyA,
	"b":            ui.KeyB,
	"c":            ui.KeyC,
	"d":            ui.KeyD,
	"e":            ui.KeyE,
	"f":            ui.KeyF,
	"g":            ui.KeyG,
	"h":            ui.KeyH,
	"i":            ui.KeyI,
	"j":            ui.KeyJ,
	"k":            ui.KeyK,
	"l":            ui.KeyL,
	"m":            ui.KeyM,
	"n":            ui.KeyN,
	"o":            ui.KeyO,
	"p":            ui.KeyP,
	"q":            ui.KeyQ,
	"r":            ui.KeyR,
	"s":            ui.KeyS,
	"t":            ui.KeyT,
	"u":            ui.KeyU,
	"v":            ui.KeyV,
	"w":            ui.KeyW,
	"x":            ui.KeyX,
	"y":            ui.KeyY,
	"z":            ui.KeyZ,
}

var keyToNameMap = func() map[ui.Key]string {
	m := make(map[ui.Key]string, len(nameToKey))
	preferred := []string{
		"enter", "escape", "backspace", "tab", "space", "delete", "insert",
		"home", "end", "pageup", "pagedown", "left", "right", "up", "down",
		"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12",
		"-", "=", ",", ".", "/", ";", "'", "[", "]", "\\", "`", "contextmenu",
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m",
		"n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
	}
	for _, name := range preferred {
		if k, ok := nameToKey[name]; ok {
			m[k] = name
		}
	}
	return m
}()

func keyToName(k ui.Key) string {
	return keyToNameMap[k]
}

func keyDisplayName(k ui.Key) string {
	n := keyToName(k)
	if len(n) == 1 {
		return strings.ToUpper(n)
	}
	switch n {
	case "enter":
		return "Enter"
	case "escape":
		return "Esc"
	case "backspace":
		return "Backspace"
	case "tab":
		return "Tab"
	case "space":
		return "Space"
	case "delete":
		return "Delete"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "home":
		return "Home"
	case "end":
		return "End"
	default:
		return strings.ToUpper(n)
	}
}

// ParseShortcut parses a keybinding string (e.g. "shift+enter", "ctrl+alt+z",
// "cmd+f", "escape") into a normalized Shortcut.
func ParseShortcut(spec string) (Shortcut, error) {
	trimmed := strings.TrimSpace(strings.ToLower(spec))
	if trimmed == "" {
		return Shortcut{}, fmt.Errorf("empty shortcut")
	}

	// Support trailing literal "+" if needed, though pi uses named keys ("=", "equal").
	parts := strings.Split(trimmed, "+")
	var mods ui.Modifiers
	var keyPart string

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if i < len(parts)-1 {
			switch part {
			case "ctrl", "control":
				mods |= ui.Ctrl
			case "shift":
				mods |= ui.Shift
			case "alt", "opt", "option":
				mods |= ui.Alt
			case "super", "meta", "win":
				mods |= ui.Super
			case "cmd", "mod":
				mods |= ui.Cmd
			default:
				return Shortcut{}, fmt.Errorf("unknown modifier %q in shortcut %q", part, spec)
			}
			continue
		}
		keyPart = part
	}

	k, ok := nameToKey[keyPart]
	if !ok {
		return Shortcut{}, fmt.Errorf("unknown key %q in shortcut %q", keyPart, spec)
	}
	return Shortcut{Mods: mods, Key: k, Raw: spec}, nil
}

// KeybindingDefinition holds the default key sequence(s) and human description
// for an editor or application action.
type KeybindingDefinition struct {
	DefaultKeys []string
	Description string
}

// DefaultEditorKeybindings returns the default editor/input/select bindings
// mirroring pi's TUI_KEYBINDINGS adapted for native desktop input.
func DefaultEditorKeybindings() map[EditorAction]KeybindingDefinition {
	undoKey := "cmd+z"
	redoKeys := []string{"cmd+shift+z"}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		redoKeys = append(redoKeys, "ctrl+y")
	}
	return map[EditorAction]KeybindingDefinition{
		ActionCursorUp:           {DefaultKeys: []string{"up"}, Description: "Move cursor up"},
		ActionCursorDown:         {DefaultKeys: []string{"down"}, Description: "Move cursor down"},
		ActionHistoryPrevious:    {DefaultKeys: []string{}, Description: "Select previous prompt history entry"},
		ActionHistoryNext:        {DefaultKeys: []string{}, Description: "Select next prompt history entry"},
		ActionCursorLeft:         {DefaultKeys: []string{"left", "ctrl+b"}, Description: "Move cursor left"},
		ActionCursorRight:        {DefaultKeys: []string{"right", "ctrl+f"}, Description: "Move cursor right"},
		ActionCursorWordLeft:     {DefaultKeys: []string{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
		ActionCursorWordRight:    {DefaultKeys: []string{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
		ActionCursorLineStart:    {DefaultKeys: []string{"home", "ctrl+a"}, Description: "Move to line start"},
		ActionCursorLineEnd:      {DefaultKeys: []string{"end", "ctrl+e"}, Description: "Move to line end"},
		ActionJumpForward:        {DefaultKeys: []string{"ctrl+]"}, Description: "Jump forward to character"},
		ActionJumpBackward:       {DefaultKeys: []string{"ctrl+alt+]"}, Description: "Jump backward to character"},
		ActionPageUp:             {DefaultKeys: []string{"pageup"}, Description: "Scroll page up"},
		ActionPageDown:           {DefaultKeys: []string{"pagedown"}, Description: "Scroll page down"},
		ActionDeleteCharBackward: {DefaultKeys: []string{"backspace"}, Description: "Delete character backward"},
		ActionDeleteCharForward:  {DefaultKeys: []string{"delete", "ctrl+d"}, Description: "Delete character forward"},
		ActionDeleteWordBackward: {DefaultKeys: []string{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
		ActionDeleteWordForward:  {DefaultKeys: []string{"alt+delete", "alt+d"}, Description: "Delete word forward"},
		ActionDeleteToLineStart:  {DefaultKeys: []string{"ctrl+u"}, Description: "Delete to line start"},
		ActionDeleteToLineEnd:    {DefaultKeys: []string{"ctrl+k"}, Description: "Delete to line end"},
		ActionYank:               {DefaultKeys: []string{"ctrl+y"}, Description: "Yank deleted text"},
		ActionYankPop:            {DefaultKeys: []string{"alt+y"}, Description: "Cycle yanked text"},
		ActionUndo:               {DefaultKeys: []string{undoKey, "ctrl+-"}, Description: "Undo last edit"},
		ActionRedo:               {DefaultKeys: redoKeys, Description: "Redo last edit"},
		ActionNewLine:            {DefaultKeys: []string{"shift+enter"}, Description: "Insert new line"},
		ActionSubmit:             {DefaultKeys: []string{"enter"}, Description: "Submit input"},
		ActionTab:                {DefaultKeys: []string{"tab"}, Description: "Trigger or accept completion"},
		ActionCopy:               {DefaultKeys: []string{"cmd+c"}, Description: "Copy selection"},
		ActionSelectUp:           {DefaultKeys: []string{"up"}, Description: "Move selection up"},
		ActionSelectDown:         {DefaultKeys: []string{"down"}, Description: "Move selection down"},
		ActionSelectPageUp:       {DefaultKeys: []string{"pageup"}, Description: "Page up in list"},
		ActionSelectPageDown:     {DefaultKeys: []string{"pagedown"}, Description: "Page down in list"},
		ActionSelectConfirm:      {DefaultKeys: []string{"enter"}, Description: "Confirm selection"},
		ActionSelectCancel:       {DefaultKeys: []string{"escape"}, Description: "Cancel selection"},
		ActionSearch:             {DefaultKeys: []string{"cmd+f", "ctrl+shift+f"}, Description: "Find in view"},
		ActionSearchNext:         {DefaultKeys: []string{"cmd+g", "f3"}, Description: "Next search match"},
		ActionSearchPrevious:     {DefaultKeys: []string{"cmd+shift+g", "shift+f3"}, Description: "Previous search match"},
		ActionSearchClose:        {DefaultKeys: []string{"escape"}, Description: "Close search"},
		ActionScrollTop:          {DefaultKeys: []string{"cmd+home"}, Description: "Scroll to top"},
		ActionScrollBottom:       {DefaultKeys: []string{"cmd+end"}, Description: "Scroll to bottom"},
	}
}

// DefaultAppKeybindings returns the default application keybindings mirroring
// coding-agent's APP_KEYBINDINGS adapted cleanly for native desktop apps.
func DefaultAppKeybindings() map[AppAction]KeybindingDefinition {
	isWin := runtime.GOOS == "windows"
	followUp := "alt+enter"
	dequeue := "alt+up"
	cycleBack := "shift+ctrl+p"
	pasteKeys := []string{"cmd+v", "ctrl+v"}
	if isWin {
		followUp = "ctrl+q"
		dequeue = "alt+q"
		cycleBack = "alt+p"
		pasteKeys = []string{"alt+v", "ctrl+v"}
	}
	return map[AppAction]KeybindingDefinition{
		AppInterrupt:                {DefaultKeys: []string{"escape"}, Description: "Cancel or abort"},
		AppClear:                    {DefaultKeys: []string{"ctrl+c"}, Description: "Clear editor"},
		AppExit:                     {DefaultKeys: []string{"ctrl+d", "cmd+q"}, Description: "Exit when editor is empty"},
		AppSuspend:                  {DefaultKeys: []string{"ctrl+z"}, Description: "Minimize or suspend"},
		AppThinkingCycle:            {DefaultKeys: []string{"shift+tab"}, Description: "Cycle thinking level"},
		AppThinkingSave:             {DefaultKeys: []string{"cmd+s"}, Description: "Save thinking level"},
		AppModelCycleForward:        {DefaultKeys: []string{"ctrl+p"}, Description: "Cycle to next model"},
		AppModelCycleBackward:       {DefaultKeys: []string{cycleBack}, Description: "Cycle to previous model"},
		AppModelSelect:              {DefaultKeys: []string{"cmd+l", "ctrl+l"}, Description: "Open model selector"},
		AppToolsExpand:              {DefaultKeys: []string{"ctrl+o"}, Description: "Toggle tool output"},
		AppThinkingToggle:           {DefaultKeys: []string{"ctrl+t"}, Description: "Toggle thinking blocks"},
		AppSessionToggleNamedFilter: {DefaultKeys: []string{"ctrl+n"}, Description: "Toggle named session filter"},
		AppEditorExternal:           {DefaultKeys: []string{"ctrl+g"}, Description: "Open external editor"},
		AppMessageCopy:              {DefaultKeys: []string{"ctrl+x"}, Description: "Copy selection or last assistant message"},
		AppMessageFollowUp:          {DefaultKeys: []string{followUp}, Description: "Queue follow-up message"},
		AppMessageDequeue:           {DefaultKeys: []string{dequeue}, Description: "Restore queued messages"},
		AppClipboardPasteImage:      {DefaultKeys: pasteKeys, Description: "Paste files, images, or text from clipboard"},
		AppSessionNew:               {DefaultKeys: []string{"cmd+shift+n"}, Description: "Start a new session"},
		AppSessionTree:              {DefaultKeys: []string{}, Description: "Open session tree"},
		AppSessionFork:              {DefaultKeys: []string{}, Description: "Fork current session"},
		AppSessionResume:            {DefaultKeys: []string{}, Description: "Resume a session"},
		AppTreeFoldOrUp:             {DefaultKeys: []string{"alt+left", "ctrl+left"}, Description: "Fold tree branch or move up"},
		AppTreeUnfoldOrDown:         {DefaultKeys: []string{"alt+right", "ctrl+right"}, Description: "Unfold tree branch or move down"},
		AppTreeEditLabel:            {DefaultKeys: []string{"shift+l"}, Description: "Edit tree label"},
		AppTreeToggleLabelTimestamp: {DefaultKeys: []string{"shift+t"}, Description: "Toggle tree label timestamps"},
		AppSessionTogglePath:        {DefaultKeys: []string{"ctrl+p"}, Description: "Toggle session path display"},
		AppSessionToggleSort:        {DefaultKeys: []string{"ctrl+s"}, Description: "Toggle session sort mode"},
		AppSessionRename:            {DefaultKeys: []string{"ctrl+r"}, Description: "Rename session"},
		AppSessionDelete:            {DefaultKeys: []string{"ctrl+d"}, Description: "Delete session"},
		AppSessionDeleteNoninvasive: {DefaultKeys: []string{"ctrl+backspace"}, Description: "Delete session when query is empty"},
		AppModelsSave:               {DefaultKeys: []string{"ctrl+s"}, Description: "Save model selection"},
		AppModelsEnableAll:          {DefaultKeys: []string{"ctrl+a"}, Description: "Enable all models"},
		AppModelsClearAll:           {DefaultKeys: []string{"ctrl+x"}, Description: "Clear all models"},
		AppModelsToggleProvider:     {DefaultKeys: []string{"ctrl+p"}, Description: "Toggle all models for provider"},
		AppModelsReorderUp:          {DefaultKeys: []string{"alt+up"}, Description: "Move model up in order"},
		AppModelsReorderDown:        {DefaultKeys: []string{"alt+down"}, Description: "Move model down in order"},
		AppTreeFilterDefault:        {DefaultKeys: []string{"ctrl+d"}, Description: "Tree filter: default view"},
		AppTreeFilterNoTools:        {DefaultKeys: []string{"ctrl+t"}, Description: "Tree filter: hide tool results"},
		AppTreeFilterUserOnly:       {DefaultKeys: []string{"ctrl+u"}, Description: "Tree filter: user messages only"},
		AppTreeFilterLabeledOnly:    {DefaultKeys: []string{"ctrl+l"}, Description: "Tree filter: labeled entries only"},
		AppTreeFilterAll:            {DefaultKeys: []string{"ctrl+a"}, Description: "Tree filter: show all entries"},
		AppTreeFilterCycleForward:   {DefaultKeys: []string{"ctrl+o"}, Description: "Tree filter: cycle forward"},
		AppTreeFilterCycleBackward:  {DefaultKeys: []string{"shift+ctrl+o"}, Description: "Tree filter: cycle backward"},
	}
}

// KeybindingConflict describes a shortcut assigned to multiple actions in the
// same binding namespace/context.
type KeybindingConflict struct {
	Key     string
	Actions []string
}

// KeybindingsManager resolves editor and application shortcuts, supporting user
// JSON overrides and conflict inspection.
type KeybindingsManager struct {
	mu           sync.RWMutex
	defaults     map[string]KeybindingDefinition
	userBindings map[string][]string
	actionToKeys map[string][]Shortcut
	conflicts    []KeybindingConflict
}

// NewKeybindingsManager constructs a manager combining DefaultEditorKeybindings
// and DefaultAppKeybindings with optional user overrides.
func NewKeybindingsManager(userOverrides ...map[string][]string) *KeybindingsManager {
	defaults := make(map[string]KeybindingDefinition)
	for k, v := range DefaultEditorKeybindings() {
		defaults[string(k)] = v
	}
	for k, v := range DefaultAppKeybindings() {
		defaults[string(k)] = v
	}
	km := &KeybindingsManager{
		defaults:     defaults,
		userBindings: make(map[string][]string),
		actionToKeys: make(map[string][]Shortcut),
	}
	if len(userOverrides) > 0 && userOverrides[0] != nil {
		km.SetUserBindings(userOverrides[0])
	} else {
		km.rebuild()
	}
	return km
}

// LoadKeybindingsFromFile loads a pi keybindings.json override file (supporting
// either a string or []string per action). Missing files return defaults.
func LoadKeybindingsFromFile(path string) (*KeybindingsManager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewKeybindingsManager(), nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := jsonx.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	overrides := make(map[string][]string)
	for k, v := range raw {
		if k == "$schema" {
			continue
		}
		switch tv := v.(type) {
		case string:
			overrides[k] = []string{tv}
		case []any:
			var list []string
			for _, item := range tv {
				if s, ok := item.(string); ok {
					list = append(list, s)
				}
			}
			overrides[k] = list
		}
	}
	return NewKeybindingsManager(overrides), nil
}

// SetUserBindings replaces user overrides and rebuilds parsed shortcut tables.
func (km *KeybindingsManager) SetUserBindings(overrides map[string][]string) {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.userBindings = make(map[string][]string, len(overrides))
	for k, v := range overrides {
		cp := make([]string, len(v))
		copy(cp, v)
		km.userBindings[k] = cp
	}
	km.rebuild()
}

func (km *KeybindingsManager) rebuild() {
	actionToKeys := make(map[string][]Shortcut, len(km.defaults))
	userOnly := make(map[string][]string)

	for action, def := range km.defaults {
		rawList := def.DefaultKeys
		if user, ok := km.userBindings[action]; ok {
			rawList = user
			userOnly[action] = user
		}
		var shortcuts []Shortcut
		for _, spec := range rawList {
			if sc, err := ParseShortcut(spec); err == nil {
				shortcuts = append(shortcuts, sc)
			}
		}
		actionToKeys[action] = shortcuts
	}
	// Preserve custom user actions not in defaults.
	for action, specs := range km.userBindings {
		if _, ok := actionToKeys[action]; ok {
			continue
		}
		var shortcuts []Shortcut
		for _, spec := range specs {
			if sc, err := ParseShortcut(spec); err == nil {
				shortcuts = append(shortcuts, sc)
			}
		}
		actionToKeys[action] = shortcuts
	}

	// Detect explicit conflicts within user/app global overrides per namespace prefix.
	byContextAndKey := make(map[string][]string)
	for action, shortcuts := range actionToKeys {
		ns := conflictNamespace(action)
		if ns == "" {
			continue
		}
		seen := make(map[string]bool)
		for _, sc := range shortcuts {
			kStr := sc.String()
			if seen[kStr] {
				continue
			}
			seen[kStr] = true
			mapKey := ns + "|" + kStr
			byContextAndKey[mapKey] = append(byContextAndKey[mapKey], action)
		}
	}

	var conflicts []KeybindingConflict
	for mapKey, actions := range byContextAndKey {
		if len(actions) > 1 {
			sort.Strings(actions)
			parts := strings.SplitN(mapKey, "|", 2)
			conflicts = append(conflicts, KeybindingConflict{
				Key:     parts[1],
				Actions: actions,
			})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Key < conflicts[j].Key
	})

	km.actionToKeys = actionToKeys
	km.conflicts = conflicts
}

func conflictNamespace(action string) string {
	switch {
	case strings.HasPrefix(action, "tui.editor."):
		return "tui.editor"
	case strings.HasPrefix(action, "tui.select."):
		return "tui.select"
	case strings.HasPrefix(action, "app.tree.filter."):
		return "app.tree.filter"
	case strings.HasPrefix(action, "app.session."):
		return "app.session"
	case strings.HasPrefix(action, "app.models."):
		return "app.models"
	default:
		return ""
	}
}

// BindingsFor returns the resolved Shortcut slice for any action key string.
func (km *KeybindingsManager) BindingsFor(action string) []Shortcut {
	km.mu.RLock()
	defer km.mu.RUnlock()
	src := km.actionToKeys[action]
	out := make([]Shortcut, len(src))
	copy(out, src)
	return out
}

// EditorBindings returns the shortcuts bound to an EditorAction.
func (km *KeybindingsManager) EditorBindings(action EditorAction) []Shortcut {
	return km.BindingsFor(string(action))
}

// AppBindings returns the shortcuts bound to an AppAction.
func (km *KeybindingsManager) AppBindings(action AppAction) []Shortcut {
	return km.BindingsFor(string(action))
}

// MatchesKey reports whether (mods, key) triggers action.
func (km *KeybindingsManager) MatchesKey(mods ui.Modifiers, key ui.Key, action string) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()
	for _, sc := range km.actionToKeys[action] {
		if sc.MatchesKey(mods, key) {
			return true
		}
	}
	return false
}

// MatchesEditorKey reports whether (mods, key) triggers an EditorAction.
func (km *KeybindingsManager) MatchesEditorKey(mods ui.Modifiers, key ui.Key, action EditorAction) bool {
	return km.MatchesKey(mods, key, string(action))
}

// MatchesAppKey reports whether (mods, key) triggers an AppAction.
func (km *KeybindingsManager) MatchesAppKey(mods ui.Modifiers, key ui.Key, action AppAction) bool {
	return km.MatchesKey(mods, key, string(action))
}

// MatchesTextKey reports whether k triggers the given action string.
func (km *KeybindingsManager) MatchesTextKey(k ui.TextKey, action string) bool {
	return km.MatchesKey(k.Mods, k.Key, action)
}

// MatchesContext checks whether any shortcut bound to action fired in c during
// the current build pass.
func (km *KeybindingsManager) MatchesContext(c *ui.Context, action string) bool {
	if c == nil {
		return false
	}
	km.mu.RLock()
	shortcuts := km.actionToKeys[action]
	km.mu.RUnlock()
	for _, sc := range shortcuts {
		if sc.MatchesContext(c) {
			return true
		}
	}
	return false
}

// MatchesAppContext checks an AppAction against c.Shortcut.
func (km *KeybindingsManager) MatchesAppContext(c *ui.Context, action AppAction) bool {
	return km.MatchesContext(c, string(action))
}

// OnAppContext registers c.OnShortcut listeners for every shortcut bound to action.
func (km *KeybindingsManager) OnAppContext(c *ui.Context, action AppAction, fn func()) {
	if c == nil || fn == nil {
		return
	}
	for _, sc := range km.AppBindings(action) {
		if sc.Key != ui.KeyUnknown {
			c.OnShortcut(sc.Mods, sc.Key, fn)
		}
	}
}

// DisplayKeys returns a comma-separated human-readable shortcut hint string for action.
func (km *KeybindingsManager) DisplayKeys(action string) string {
	scs := km.BindingsFor(action)
	if len(scs) == 0 {
		return ""
	}
	parts := make([]string, len(scs))
	for i, sc := range scs {
		parts[i] = sc.DisplayString()
	}
	return strings.Join(parts, " / ")
}

// Conflicts returns any intra-context keybinding conflicts detected.
func (km *KeybindingsManager) Conflicts() []KeybindingConflict {
	km.mu.RLock()
	defer km.mu.RUnlock()
	out := make([]KeybindingConflict, len(km.conflicts))
	copy(out, km.conflicts)
	return out
}
