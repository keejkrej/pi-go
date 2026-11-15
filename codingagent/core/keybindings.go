// Ported from packages/coding-agent/src/core/keybindings.ts (pi v1.5.2).

package core

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

// Application keybinding action IDs appended alongside tui.* definitions.
const (
	KeybindingAppInterrupt              tui.Keybinding = "app.interrupt"
	KeybindingAppClear                  tui.Keybinding = "app.clear"
	KeybindingAppExit                   tui.Keybinding = "app.exit"
	KeybindingAppSuspend                tui.Keybinding = "app.suspend"
	KeybindingAppThinkingCycle          tui.Keybinding = "app.thinking.cycle"
	KeybindingAppThinkingSave           tui.Keybinding = "app.thinking.save"
	KeybindingAppModelCycleForward      tui.Keybinding = "app.model.cycleForward"
	KeybindingAppModelCycleBackward     tui.Keybinding = "app.model.cycleBackward"
	KeybindingAppModelSelect            tui.Keybinding = "app.model.select"
	KeybindingAppToolsExpand            tui.Keybinding = "app.tools.expand"
	KeybindingAppThinkingToggle         tui.Keybinding = "app.thinking.toggle"
	KeybindingAppSessionToggleNamed     tui.Keybinding = "app.session.toggleNamedFilter"
	KeybindingAppEditorExternal         tui.Keybinding = "app.editor.external"
	KeybindingAppMessageCopy            tui.Keybinding = "app.message.copy"
	KeybindingAppMessageFollowUp        tui.Keybinding = "app.message.followUp"
	KeybindingAppMessageDequeue         tui.Keybinding = "app.message.dequeue"
	KeybindingAppClipboardPasteImage    tui.Keybinding = "app.clipboard.pasteImage"
	KeybindingAppSessionNew             tui.Keybinding = "app.session.new"
	KeybindingAppSessionTree            tui.Keybinding = "app.session.tree"
	KeybindingAppSessionFork            tui.Keybinding = "app.session.fork"
	KeybindingAppSessionResume          tui.Keybinding = "app.session.resume"
	KeybindingAppTreeFoldOrUp           tui.Keybinding = "app.tree.foldOrUp"
	KeybindingAppTreeUnfoldOrDown       tui.Keybinding = "app.tree.unfoldOrDown"
	KeybindingAppTreeEditLabel          tui.Keybinding = "app.tree.editLabel"
	KeybindingAppTreeToggleLabelTs      tui.Keybinding = "app.tree.toggleLabelTimestamp"
	KeybindingAppSessionTogglePath      tui.Keybinding = "app.session.togglePath"
	KeybindingAppSessionToggleSort      tui.Keybinding = "app.session.toggleSort"
	KeybindingAppSessionRename          tui.Keybinding = "app.session.rename"
	KeybindingAppSessionDelete          tui.Keybinding = "app.session.delete"
	KeybindingAppSessionDeleteNoninv    tui.Keybinding = "app.session.deleteNoninvasive"
	KeybindingAppModelsSave             tui.Keybinding = "app.models.save"
	KeybindingAppModelsEnableAll        tui.Keybinding = "app.models.enableAll"
	KeybindingAppModelsClearAll         tui.Keybinding = "app.models.clearAll"
	KeybindingAppModelsToggleProvider   tui.Keybinding = "app.models.toggleProvider"
	KeybindingAppModelsReorderUp        tui.Keybinding = "app.models.reorderUp"
	KeybindingAppModelsReorderDown      tui.Keybinding = "app.models.reorderDown"
	KeybindingAppTreeFilterDefault      tui.Keybinding = "app.tree.filter.default"
	KeybindingAppTreeFilterNoTools      tui.Keybinding = "app.tree.filter.noTools"
	KeybindingAppTreeFilterUserOnly     tui.Keybinding = "app.tree.filter.userOnly"
	KeybindingAppTreeFilterLabeledOnly  tui.Keybinding = "app.tree.filter.labeledOnly"
	KeybindingAppTreeFilterAll          tui.Keybinding = "app.tree.filter.all"
	KeybindingAppTreeFilterCycleForward tui.Keybinding = "app.tree.filter.cycleForward"
	KeybindingAppTreeFilterCycleBack    tui.Keybinding = "app.tree.filter.cycleBackward"
)

// AppKeybinding aliases tui.Keybinding for coding-agent actions.
type AppKeybinding = tui.Keybinding

// UseWindowsKeybindings reports whether Windows-style keybinding defaults apply on the current host.
func UseWindowsKeybindings() bool {
	return UseWindowsKeybindingsFor(runtime.GOOS, map[string]string{
		"WSL_DISTRO_NAME": os.Getenv("WSL_DISTRO_NAME"),
		"WSL_INTEROP":     os.Getenv("WSL_INTEROP"),
	})
}

// UseWindowsKeybindingsFor checks whether Windows-style keybinding defaults apply for platform/env.
func UseWindowsKeybindingsFor(platform string, env map[string]string) bool {
	if platform == "win32" || platform == "windows" {
		return true
	}
	if platform == "linux" && env != nil {
		return env["WSL_DISTRO_NAME"] != "" || env["WSL_INTEROP"] != ""
	}
	return false
}

func keybOne(k tui.KeyId) tui.KeybindingKeys {
	return tui.KeybindingKeys{Single: &k}
}

func keybMany(ks ...tui.KeyId) tui.KeybindingKeys {
	return tui.KeybindingKeys{Keys: slices.Clone(ks)}
}

func keybNone() tui.KeybindingKeys {
	return tui.KeybindingKeys{Keys: []tui.KeyId{}}
}

func keybDesc(s string) *string {
	return &s
}

func keybCloneDef(def *tui.KeybindingDefinition) *tui.KeybindingDefinition {
	if def == nil {
		return nil
	}
	out := &tui.KeybindingDefinition{}
	if def.DefaultKeys.Single != nil {
		k := *def.DefaultKeys.Single
		out.DefaultKeys.Single = &k
	} else if def.DefaultKeys.Keys != nil {
		out.DefaultKeys.Keys = slices.Clone(def.DefaultKeys.Keys)
	}
	if def.Description != nil {
		d := *def.Description
		out.Description = &d
	}
	return out
}

func keybBuildAppKeybindings(platform string, windowsKeybindings bool) tui.KeybindingDefinitions {
	m := omap.NewMap[string, *tui.KeybindingDefinition]()

	suspendKeys := keybOne("ctrl+z")
	if platform == "win32" || platform == "windows" {
		suspendKeys = keybNone()
	}
	cycleBackwardKey := tui.KeyId("shift+ctrl+p")
	if windowsKeybindings {
		cycleBackwardKey = "alt+p"
	}
	followUpKey := tui.KeyId("alt+enter")
	if windowsKeybindings {
		followUpKey = "ctrl+q"
	}
	dequeueKey := tui.KeyId("alt+up")
	if windowsKeybindings {
		dequeueKey = "alt+q"
	}
	pasteKeys := keybOne("ctrl+v")
	if windowsKeybindings {
		pasteKeys = keybMany("alt+v", "ctrl+v")
	}
	treeFoldKeys := keybMany("ctrl+left", "alt+left")
	treeUnfoldKeys := keybMany("ctrl+right", "alt+right")
	if platform == "darwin" {
		treeFoldKeys = keybMany("alt+left", "ctrl+left")
		treeUnfoldKeys = keybMany("alt+right", "ctrl+right")
	}

	m.Set(string(KeybindingAppInterrupt), &tui.KeybindingDefinition{DefaultKeys: keybOne("escape"), Description: keybDesc("Cancel or abort")})
	m.Set(string(KeybindingAppClear), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+c"), Description: keybDesc("Clear editor")})
	m.Set(string(KeybindingAppExit), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+d"), Description: keybDesc("Exit when editor is empty")})
	m.Set(string(KeybindingAppSuspend), &tui.KeybindingDefinition{DefaultKeys: suspendKeys, Description: keybDesc("Suspend to background")})
	m.Set(string(KeybindingAppThinkingCycle), &tui.KeybindingDefinition{DefaultKeys: keybOne("shift+tab"), Description: keybDesc("Cycle thinking level")})
	m.Set(string(KeybindingAppThinkingSave), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+s"), Description: keybDesc("Save thinking level")})
	m.Set(string(KeybindingAppModelCycleForward), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+p"), Description: keybDesc("Cycle to next model")})
	m.Set(string(KeybindingAppModelCycleBackward), &tui.KeybindingDefinition{DefaultKeys: keybOne(cycleBackwardKey), Description: keybDesc("Cycle to previous model")})
	m.Set(string(KeybindingAppModelSelect), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+l"), Description: keybDesc("Open model selector")})
	m.Set(string(KeybindingAppToolsExpand), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+o"), Description: keybDesc("Toggle tool output")})
	m.Set(string(KeybindingAppThinkingToggle), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+t"), Description: keybDesc("Toggle thinking blocks")})
	m.Set(string(KeybindingAppSessionToggleNamed), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+n"), Description: keybDesc("Toggle named session filter")})
	m.Set(string(KeybindingAppEditorExternal), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+g"), Description: keybDesc("Open external editor")})
	m.Set(string(KeybindingAppMessageCopy), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+x"), Description: keybDesc("Copy selection or last assistant message")})
	m.Set(string(KeybindingAppMessageFollowUp), &tui.KeybindingDefinition{DefaultKeys: keybOne(followUpKey), Description: keybDesc("Queue follow-up message")})
	m.Set(string(KeybindingAppMessageDequeue), &tui.KeybindingDefinition{DefaultKeys: keybOne(dequeueKey), Description: keybDesc("Restore queued messages")})
	m.Set(string(KeybindingAppClipboardPasteImage), &tui.KeybindingDefinition{DefaultKeys: pasteKeys, Description: keybDesc("Paste files on macOS, images, or text from clipboard")})
	m.Set(string(KeybindingAppSessionNew), &tui.KeybindingDefinition{DefaultKeys: keybNone(), Description: keybDesc("Start a new session")})
	m.Set(string(KeybindingAppSessionTree), &tui.KeybindingDefinition{DefaultKeys: keybNone(), Description: keybDesc("Open session tree")})
	m.Set(string(KeybindingAppSessionFork), &tui.KeybindingDefinition{DefaultKeys: keybNone(), Description: keybDesc("Fork current session")})
	m.Set(string(KeybindingAppSessionResume), &tui.KeybindingDefinition{DefaultKeys: keybNone(), Description: keybDesc("Resume a session")})
	m.Set(string(KeybindingAppTreeFoldOrUp), &tui.KeybindingDefinition{DefaultKeys: treeFoldKeys, Description: keybDesc("Fold tree branch or move up")})
	m.Set(string(KeybindingAppTreeUnfoldOrDown), &tui.KeybindingDefinition{DefaultKeys: treeUnfoldKeys, Description: keybDesc("Unfold tree branch or move down")})
	m.Set(string(KeybindingAppTreeEditLabel), &tui.KeybindingDefinition{DefaultKeys: keybOne("shift+l"), Description: keybDesc("Edit tree label")})
	m.Set(string(KeybindingAppTreeToggleLabelTs), &tui.KeybindingDefinition{DefaultKeys: keybOne("shift+t"), Description: keybDesc("Toggle tree label timestamps")})
	m.Set(string(KeybindingAppSessionTogglePath), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+p"), Description: keybDesc("Toggle session path display")})
	m.Set(string(KeybindingAppSessionToggleSort), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+s"), Description: keybDesc("Toggle session sort mode")})
	m.Set(string(KeybindingAppSessionRename), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+r"), Description: keybDesc("Rename session")})
	m.Set(string(KeybindingAppSessionDelete), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+d"), Description: keybDesc("Delete session")})
	m.Set(string(KeybindingAppSessionDeleteNoninv), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+backspace"), Description: keybDesc("Delete session when query is empty")})
	m.Set(string(KeybindingAppModelsSave), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+s"), Description: keybDesc("Save model selection")})
	m.Set(string(KeybindingAppModelsEnableAll), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+a"), Description: keybDesc("Enable all models")})
	m.Set(string(KeybindingAppModelsClearAll), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+x"), Description: keybDesc("Clear all models")})
	m.Set(string(KeybindingAppModelsToggleProvider), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+p"), Description: keybDesc("Toggle all models for provider")})
	m.Set(string(KeybindingAppModelsReorderUp), &tui.KeybindingDefinition{DefaultKeys: keybOne("alt+up"), Description: keybDesc("Move model up in order")})
	m.Set(string(KeybindingAppModelsReorderDown), &tui.KeybindingDefinition{DefaultKeys: keybOne("alt+down"), Description: keybDesc("Move model down in order")})
	m.Set(string(KeybindingAppTreeFilterDefault), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+d"), Description: keybDesc("Tree filter: default view")})
	m.Set(string(KeybindingAppTreeFilterNoTools), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+t"), Description: keybDesc("Tree filter: hide tool results")})
	m.Set(string(KeybindingAppTreeFilterUserOnly), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+u"), Description: keybDesc("Tree filter: user messages only")})
	m.Set(string(KeybindingAppTreeFilterLabeledOnly), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+l"), Description: keybDesc("Tree filter: labeled entries only")})
	m.Set(string(KeybindingAppTreeFilterAll), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+a"), Description: keybDesc("Tree filter: show all entries")})
	m.Set(string(KeybindingAppTreeFilterCycleForward), &tui.KeybindingDefinition{DefaultKeys: keybOne("ctrl+o"), Description: keybDesc("Tree filter: cycle forward")})
	m.Set(string(KeybindingAppTreeFilterCycleBack), &tui.KeybindingDefinition{DefaultKeys: keybOne("shift+ctrl+o"), Description: keybDesc("Tree filter: cycle backward")})
	return m
}

func keybBuildAllKeybindings(platform string, windowsKeybindings bool) tui.KeybindingDefinitions {
	all := omap.NewMap[string, *tui.KeybindingDefinition]()
	for _, id := range tui.TuiKeybindings.Keys() {
		def, _ := tui.TuiKeybindings.Get(id)
		all.Set(id, keybCloneDef(def))
	}

	undoKey := keybOne("ctrl+-")
	if platform == "win32" || platform == "windows" {
		undoKey = keybOne("ctrl+z")
	} else if windowsKeybindings {
		undoKey = keybOne("alt+z")
	}
	if def, ok := all.Get(string(tui.KeybindingTuiEditorUndo)); ok && def != nil {
		def.DefaultKeys = undoKey
	}

	prevPromptKeys := keybMany("ctrl+shift+up", "ctrl+up")
	nextPromptKeys := keybMany("ctrl+shift+down", "ctrl+down")
	searchKeys := keybOne("ctrl+shift+f")
	if windowsKeybindings {
		prevPromptKeys = keybOne("ctrl+up")
		nextPromptKeys = keybOne("ctrl+down")
		searchKeys = keybOne("ctrl+f")
	}
	if def, ok := all.Get(string(tui.KeybindingTuiAltScreenPreviousPrompt)); ok && def != nil {
		def.DefaultKeys = prevPromptKeys
	}
	if def, ok := all.Get(string(tui.KeybindingTuiAltScreenNextPrompt)); ok && def != nil {
		def.DefaultKeys = nextPromptKeys
	}
	if def, ok := all.Get(string(tui.KeybindingTuiAltScreenSearch)); ok && def != nil {
		def.DefaultKeys = searchKeys
	}

	app := keybBuildAppKeybindings(platform, windowsKeybindings)
	for _, id := range app.Keys() {
		def, _ := app.Get(id)
		all.Set(id, def)
	}
	return all
}

// Keybindings holds the full coding-agent + TUI keybinding definition table.
var Keybindings = keybBuildAllKeybindings(runtime.GOOS, UseWindowsKeybindings())

var keybNameMigrations = map[string]tui.Keybinding{
	"cursorUp":                 tui.KeybindingTuiEditorCursorUp,
	"cursorDown":               tui.KeybindingTuiEditorCursorDown,
	"cursorLeft":               tui.KeybindingTuiEditorCursorLeft,
	"cursorRight":              tui.KeybindingTuiEditorCursorRight,
	"cursorWordLeft":           tui.KeybindingTuiEditorCursorWordLeft,
	"cursorWordRight":          tui.KeybindingTuiEditorCursorWordRight,
	"cursorLineStart":          tui.KeybindingTuiEditorCursorLineStart,
	"cursorLineEnd":            tui.KeybindingTuiEditorCursorLineEnd,
	"jumpForward":              tui.KeybindingTuiEditorJumpForward,
	"jumpBackward":             tui.KeybindingTuiEditorJumpBackward,
	"pageUp":                   tui.KeybindingTuiEditorPageUp,
	"pageDown":                 tui.KeybindingTuiEditorPageDown,
	"deleteCharBackward":       tui.KeybindingTuiEditorDeleteCharBackward,
	"deleteCharForward":        tui.KeybindingTuiEditorDeleteCharForward,
	"deleteWordBackward":       tui.KeybindingTuiEditorDeleteWordBackward,
	"deleteWordForward":        tui.KeybindingTuiEditorDeleteWordForward,
	"deleteToLineStart":        tui.KeybindingTuiEditorDeleteToLineStart,
	"deleteToLineEnd":          tui.KeybindingTuiEditorDeleteToLineEnd,
	"yank":                     tui.KeybindingTuiEditorYank,
	"yankPop":                  tui.KeybindingTuiEditorYankPop,
	"undo":                     tui.KeybindingTuiEditorUndo,
	"newLine":                  tui.KeybindingTuiInputNewLine,
	"submit":                   tui.KeybindingTuiInputSubmit,
	"tab":                      tui.KeybindingTuiInputTab,
	"copy":                     tui.KeybindingTuiInputCopy,
	"selectUp":                 tui.KeybindingTuiSelectUp,
	"selectDown":               tui.KeybindingTuiSelectDown,
	"selectPageUp":             tui.KeybindingTuiSelectPageUp,
	"selectPageDown":           tui.KeybindingTuiSelectPageDown,
	"selectConfirm":            tui.KeybindingTuiSelectConfirm,
	"selectCancel":             tui.KeybindingTuiSelectCancel,
	"interrupt":                KeybindingAppInterrupt,
	"clear":                    KeybindingAppClear,
	"exit":                     KeybindingAppExit,
	"suspend":                  KeybindingAppSuspend,
	"cycleThinkingLevel":       KeybindingAppThinkingCycle,
	"cycleModelForward":        KeybindingAppModelCycleForward,
	"cycleModelBackward":       KeybindingAppModelCycleBackward,
	"selectModel":              KeybindingAppModelSelect,
	"expandTools":              KeybindingAppToolsExpand,
	"toggleThinking":           KeybindingAppThinkingToggle,
	"toggleSessionNamedFilter": KeybindingAppSessionToggleNamed,
	"externalEditor":           KeybindingAppEditorExternal,
	"followUp":                 KeybindingAppMessageFollowUp,
	"dequeue":                  KeybindingAppMessageDequeue,
	"pasteImage":               KeybindingAppClipboardPasteImage,
	"newSession":               KeybindingAppSessionNew,
	"tree":                     KeybindingAppSessionTree,
	"fork":                     KeybindingAppSessionFork,
	"resume":                   KeybindingAppSessionResume,
	"treeFoldOrUp":             KeybindingAppTreeFoldOrUp,
	"treeUnfoldOrDown":         KeybindingAppTreeUnfoldOrDown,
	"treeEditLabel":            KeybindingAppTreeEditLabel,
	"treeToggleLabelTimestamp": KeybindingAppTreeToggleLabelTs,
	"toggleSessionPath":        KeybindingAppSessionTogglePath,
	"toggleSessionSort":        KeybindingAppSessionToggleSort,
	"renameSession":            KeybindingAppSessionRename,
	"deleteSession":            KeybindingAppSessionDelete,
	"deleteSessionNoninvasive": KeybindingAppSessionDeleteNoninv,
}

var keybValidateValue = func() *typebox.Validator {
	v, err := typebox.Compile(KeybindingValueSchema)
	if err != nil {
		panic("core: compile KeybindingValueSchema: " + err.Error())
	}
	return v
}()

func keybToKeybindingsConfig(value *jsonx.Object) tui.KeybindingsConfig {
	cfg := omap.NewMap[string, *tui.KeybindingKeys]()
	if value == nil {
		return cfg
	}
	for _, key := range value.Keys() {
		if key == "$schema" {
			continue
		}
		binding, _ := value.Get(key)
		if !keybValidateValue.Check(binding) {
			continue
		}
		switch b := binding.(type) {
		case string:
			k := tui.KeyId(b)
			cfg.Set(key, &tui.KeybindingKeys{Single: &k})
		case []any:
			keys := make([]tui.KeyId, 0, len(b))
			for _, item := range b {
				if s, ok := item.(string); ok {
					keys = append(keys, tui.KeyId(s))
				}
			}
			cfg.Set(key, &tui.KeybindingKeys{Keys: keys})
		}
	}
	return cfg
}

// MigrateKeybindingsConfig rewrites legacy pre-namespaced keybinding keys to canonical IDs and sorts extras.
func MigrateKeybindingsConfig(rawConfig *jsonx.Object) (config *jsonx.Object, migrated bool) {
	cfg := jsonx.NewObject()
	if rawConfig == nil {
		return cfg, false
	}
	for _, key := range rawConfig.Keys() {
		val, _ := rawConfig.Get(key)
		nextKey := key
		if mapped, ok := keybNameMigrations[key]; ok {
			nextKey = string(mapped)
		}
		if nextKey != key {
			migrated = true
			if rawConfig.Has(nextKey) {
				continue
			}
		}
		cfg.Set(nextKey, val)
	}
	return keybOrderConfig(cfg), migrated
}

func keybOrderConfig(config *jsonx.Object) *jsonx.Object {
	ordered := jsonx.NewObject()
	for _, keybinding := range Keybindings.Keys() {
		if config.Has(keybinding) {
			val, _ := config.Get(keybinding)
			ordered.Set(keybinding, val)
		}
	}
	var extras []string
	for _, key := range config.Keys() {
		if !ordered.Has(key) {
			extras = append(extras, key)
		}
	}
	slices.SortFunc(extras, func(a, b string) int {
		return cmp.Compare(a, b)
	})
	for _, key := range extras {
		val, _ := config.Get(key)
		ordered.Set(key, val)
	}
	return ordered
}

func keybLoadRawConfig(path string) *jsonx.Object {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	parsed, err := jsonx.ParseString(utils.StripBom(string(data)))
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	return obj
}

// KeybindingsManager resolves coding-agent + TUI default and user keybindings.
type KeybindingsManager struct {
	*tui.KeybindingsManager
	configPath string
}

// NewKeybindingsManager creates a coding-agent KeybindingsManager with optional user bindings and config path.
func NewKeybindingsManager(userBindings tui.KeybindingsConfig, configPath ...string) *KeybindingsManager {
	if userBindings == nil {
		userBindings = omap.NewMap[string, *tui.KeybindingKeys]()
	}
	var cp string
	if len(configPath) > 0 {
		cp = configPath[0]
	}
	return &KeybindingsManager{
		KeybindingsManager: tui.NewKeybindingsManager(Keybindings, userBindings),
		configPath:         cp,
	}
}

// KeybindingsManagerCreate loads user keybindings from `<agentDir>/keybindings.json`.
func KeybindingsManagerCreate(agentDir ...string) *KeybindingsManager {
	dir := utils.GetAgentDir()
	if len(agentDir) > 0 && agentDir[0] != "" {
		dir = agentDir[0]
	}
	configPath := filepath.Join(dir, "keybindings.json")
	userBindings := keybLoadFromFile(configPath)
	return NewKeybindingsManager(userBindings, configPath)
}

func keybLoadFromFile(path string) tui.KeybindingsConfig {
	rawConfig := keybLoadRawConfig(path)
	if rawConfig == nil {
		return omap.NewMap[string, *tui.KeybindingKeys]()
	}
	migrated, _ := MigrateKeybindingsConfig(rawConfig)
	return keybToKeybindingsConfig(migrated)
}

// Reload reloads keybindings from disk when created via KeybindingsManagerCreate.
func (m *KeybindingsManager) Reload() {
	if m.configPath == "" {
		return
	}
	m.SetUserBindings(keybLoadFromFile(m.configPath))
}

// GetEffectiveConfig returns all resolved keybindings combining defaults and user overrides.
func (m *KeybindingsManager) GetEffectiveConfig() tui.KeybindingsConfig {
	return m.GetResolvedBindings()
}
