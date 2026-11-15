package gui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

// ============================================================================
// Common Dialog Chrome & Overlay Handle
// ============================================================================

// DialogHandle wraps an active modal overlay created by a GUI selector dialog.
type DialogHandle struct {
	mu      sync.Mutex
	overlay *gui.OverlayHandle
	closed  bool
	onClose func()
}

// Close hides and removes the underlying overlay if it is still open.
func (d *DialogHandle) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	ov := d.overlay
	cb := d.onClose
	d.mu.Unlock()

	if ov != nil {
		ov.Hide()
	}
	if cb != nil {
		cb()
	}
}

// Visible reports whether the dialog overlay is currently active and visible.
func (d *DialogHandle) Visible() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	closed := d.closed
	ov := d.overlay
	d.mu.Unlock()
	return !closed && ov != nil && ov.Visible()
}

// Overlay returns the underlying *gui.OverlayHandle for inspection or low-level control.
func (d *DialogHandle) Overlay() *gui.OverlayHandle {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	ov := d.overlay
	d.mu.Unlock()
	return ov
}

func selShowOverlay(
	host *gui.GUI,
	opts gui.OverlayOptions,
	onCancel func(),
	build func(c *ui.Context, confirmDone func(), cancel func()),
) *DialogHandle {
	handle := &DialogHandle{}
	if host == nil {
		return handle
	}

	var mu sync.Mutex
	var closedOrCancelled bool

	confirmDone := func() {
		mu.Lock()
		if closedOrCancelled {
			mu.Unlock()
			return
		}
		closedOrCancelled = true
		mu.Unlock()
		handle.Close()
	}

	cancel := func() {
		mu.Lock()
		if closedOrCancelled {
			mu.Unlock()
			return
		}
		closedOrCancelled = true
		mu.Unlock()
		handle.Close()
		if onCancel != nil {
			onCancel()
		}
	}

	userDismiss := opts.OnDismiss
	opts.OnDismiss = func() {
		if userDismiss != nil {
			userDismiss()
		}
		cancel()
	}

	ov := host.ShowOverlay(func(c *ui.Context) {
		build(c, confirmDone, cancel)
	}, opts)

	handle.mu.Lock()
	handle.overlay = ov
	handle.mu.Unlock()
	return handle
}

func selRenderDialogHeader(
	c *ui.Context,
	th *gui.GuiTheme,
	title string,
	subtitle string,
	headerRight func(),
) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
		ui.Column(c).Shrink(1).Grow(1).Gap(2).Children(func() {
			ui.Text(c, title).TextColor(th.Text).FontWeight(600).FontSize(14)
			if subtitle != "" {
				ui.Text(c, subtitle).TextColor(th.TextTertiary).FontSize(11).SingleLine()
			}
		})
		if headerRight != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(headerRight)
		}
	})
	ui.Box(c).FillWidth().Height(1).Background(th.Border)
}

func selRenderDialogFooter(
	c *ui.Context,
	th *gui.GuiTheme,
	hints string,
	actions func(),
) {
	ui.Box(c).FillWidth().Height(1).Background(th.Border)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
		if hints != "" {
			ui.Text(c, hints).TextColor(th.TextTertiary).FontSize(11)
		} else {
			ui.Spacer(c)
		}
		if actions != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(actions)
		}
	})
}

func selTruncateSingleLine(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func selFormatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func selFilterPill(c *ui.Context, th *gui.GuiTheme, label string, active bool) bool {
	btn := ui.Button(c, label).
		Radius(8).
		Padding(4, 10).
		FontSize(11).
		Cursor(ui.CursorPointer)
	if active {
		fg := gui.ContrastingText(th.Accent, ui.RGB(248, 250, 252), ui.RGB(20, 20, 24))
		btn.Background(th.Accent).Border(1, th.Accent).TextColor(fg)
	} else {
		btn.Background(th.Inset).Border(1, th.Border).TextColor(th.TextSecondary)
	}
	return btn.Clicked()
}

func selModelKey(provider, modelId string) string {
	return provider + "/" + modelId
}

// ============================================================================
// ModelSelectorDialog
// ============================================================================

// ModelSelectorConfig configures a ModelSelectorDialog.
type ModelSelectorConfig struct {
	Models          []*ai.Model
	ScopedModels    []core.ScopedModel
	CurrentModel    *ai.Model
	ScopeOnly       bool
	SettingsManager *core.SettingsManager
	ModelRegistry   *core.ModelRegistry
	Theme           *gui.GuiTheme
	OnSelect        func(model *ai.Model, thinkingOverride *agent.ThinkingLevel)
	OnCancel        func()
}

// ModelSelectorDialog presents available or scoped models in a searchable overlay list.
type ModelSelectorDialog struct {
	mu              sync.Mutex
	models          []*ai.Model
	scopedModels    []core.ScopedModel
	currentModel    *ai.Model
	scopeOnly       bool
	settingsManager *core.SettingsManager
	modelRegistry   *core.ModelRegistry
	theme           *gui.GuiTheme
	list            *gui.SelectList
	entryByKey      map[string]selModelChoice
	onSelect        func(model *ai.Model, thinkingOverride *agent.ThinkingLevel)
	onCancel        func()
	closeHandle     func()
	cancelHandle    func()
}

type selModelChoice struct {
	model            *ai.Model
	thinkingOverride *agent.ThinkingLevel
}

// NewModelSelectorDialog constructs a searchable model selector modal.
func NewModelSelectorDialog(cfg ModelSelectorConfig) *ModelSelectorDialog {
	d := &ModelSelectorDialog{
		models:          slices.Clone(cfg.Models),
		scopedModels:    slices.Clone(cfg.ScopedModels),
		currentModel:    cfg.CurrentModel,
		scopeOnly:       cfg.ScopeOnly,
		settingsManager: cfg.SettingsManager,
		modelRegistry:   cfg.ModelRegistry,
		theme:           cfg.Theme,
		onSelect:        cfg.OnSelect,
		onCancel:        cfg.OnCancel,
		entryByKey:      make(map[string]selModelChoice),
	}
	if len(d.models) == 0 && d.modelRegistry != nil {
		d.models = d.modelRegistry.GetAvailable()
	}
	if d.scopeOnly && len(d.scopedModels) == 0 {
		d.scopeOnly = false
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetPlaceholder("Search models by name, provider, or capability...")
	sl.SetMaxHeight(360)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(item.Value)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

// SetModels replaces the available model catalog displayed in the selector.
func (d *ModelSelectorDialog) SetModels(models []*ai.Model) {
	d.mu.Lock()
	d.models = slices.Clone(models)
	d.mu.Unlock()
	d.rebuildItems()
}

// SetScopedModels updates the scoped model set shown when scoping is active.
func (d *ModelSelectorDialog) SetScopedModels(scoped []core.ScopedModel) {
	d.mu.Lock()
	d.scopedModels = slices.Clone(scoped)
	if len(d.scopedModels) == 0 {
		d.scopeOnly = false
	}
	d.mu.Unlock()
	d.rebuildItems()
}

// SetCurrentModel highlights the currently active model in the list.
func (d *ModelSelectorDialog) SetCurrentModel(model *ai.Model) {
	d.mu.Lock()
	d.currentModel = model
	d.mu.Unlock()
	d.rebuildItems()
}

// SetScopeOnly toggles between all available models and scoped models.
func (d *ModelSelectorDialog) SetScopeOnly(scopeOnly bool) {
	d.mu.Lock()
	if scopeOnly && len(d.scopedModels) == 0 {
		scopeOnly = false
	}
	d.scopeOnly = scopeOnly
	d.mu.Unlock()
	d.rebuildItems()
}

// FilteredModels returns the ordered model list under the active All/Scoped mode.
func (d *ModelSelectorDialog) FilteredModels() []*ai.Model {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.scopeOnly && len(d.scopedModels) > 0 {
		out := make([]*ai.Model, 0, len(d.scopedModels))
		for _, sm := range d.scopedModels {
			if sm.Model != nil {
				out = append(out, sm.Model)
			}
		}
		return out
	}
	return slices.Clone(d.models)
}

// SetTheme updates the GUI theme used by the dialog and its list.
func (d *ModelSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *ModelSelectorDialog) rebuildItems() {
	d.mu.Lock()
	models := slices.Clone(d.models)
	scoped := slices.Clone(d.scopedModels)
	current := d.currentModel
	scopeOnly := d.scopeOnly && len(scoped) > 0
	list := d.list

	scopedOverride := make(map[string]*agent.ThinkingLevel, len(scoped))
	for _, sm := range scoped {
		if sm.Model == nil {
			continue
		}
		k := selModelKey(string(sm.Model.Provider), sm.Model.Id)
		if sm.ThinkingLevel != nil {
			lvl := *sm.ThinkingLevel
			scopedOverride[k] = &lvl
		}
	}

	var choices []selModelChoice
	if scopeOnly {
		for _, sm := range scoped {
			if sm.Model == nil {
				continue
			}
			var ov *agent.ThinkingLevel
			if sm.ThinkingLevel != nil {
				lvl := *sm.ThinkingLevel
				ov = &lvl
			}
			choices = append(choices, selModelChoice{model: sm.Model, thinkingOverride: ov})
		}
	} else {
		for _, m := range models {
			if m == nil {
				continue
			}
			k := selModelKey(string(m.Provider), m.Id)
			choices = append(choices, selModelChoice{model: m, thinkingOverride: scopedOverride[k]})
		}
	}

	entryByKey := make(map[string]selModelChoice, len(choices))
	items := make([]gui.SelectItem, 0, len(choices))
	selectedIdx := -1

	for i, ch := range choices {
		m := ch.model
		key := selModelKey(string(m.Provider), m.Id)
		entryByKey[key] = ch

		isCurrent := current != nil && current.Provider == m.Provider && current.Id == m.Id
		if isCurrent && selectedIdx == -1 {
			selectedIdx = i
		}

		label := m.Name
		if label == "" {
			label = m.Id
		}
		if isCurrent {
			label = "✓ " + label
		}

		var descParts []string
		descParts = append(descParts, key)
		if m.ContextWindow > 0 {
			descParts = append(descParts, FormatTokens(m.ContextWindow)+" ctx")
		}
		if m.Cost.Input > 0 || m.Cost.Output > 0 {
			descParts = append(descParts, fmt.Sprintf("$%.2f/$%.2f/M", m.Cost.Input, m.Cost.Output))
		}
		if m.Reasoning {
			descParts = append(descParts, "thinking")
		}
		if slices.Contains(m.Input, ai.ModelModalityImage) {
			descParts = append(descParts, "vision")
		}

		badge := string(m.Provider)
		if ch.thinkingOverride != nil && *ch.thinkingOverride != "" {
			badge = badge + " · " + string(*ch.thinkingOverride)
		} else if isCurrent {
			badge = "active"
		}

		items = append(items, gui.SelectItem{
			Value:       key,
			Label:       label,
			Description: strings.Join(descParts, " · "),
			Badge:       badge,
		})
	}
	d.entryByKey = entryByKey
	d.mu.Unlock()

	if list != nil {
		list.SetItems(items)
		if selectedIdx >= 0 {
			list.SetSelectedIndex(selectedIdx)
		}
	}
}

func (d *ModelSelectorDialog) handleSelect(key string) {
	d.mu.Lock()
	ch, ok := d.entryByKey[key]
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if !ok || ch.model == nil {
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(ch.model, ch.thinkingOverride)
	}
}

func (d *ModelSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders the ModelSelectorDialog content inside an immediate-mode context.
func (d *ModelSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	allCount := len(d.models)
	scopedCount := len(d.scopedModels)
	scopeOnly := d.scopeOnly && scopedCount > 0
	current := d.currentModel
	list := d.list
	d.mu.Unlock()

	subtitle := "Choose the active model for responses"
	if current != nil {
		subtitle = "Current: " + selModelKey(string(current.Provider), current.Id)
	}

	var toggleAllClicked, toggleScopedClicked, confirmClicked, cancelClicked bool

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Select Model", subtitle, func() {
			if scopedCount > 0 {
				if selFilterPill(c, th, fmt.Sprintf("All (%d)", allCount), !scopeOnly) {
					toggleAllClicked = true
				}
				if selFilterPill(c, th, fmt.Sprintf("Scoped (%d)", scopedCount), scopeOnly) {
					toggleScopedClicked = true
				}
			} else {
				ui.Badge(c, fmt.Sprintf("%d models", allCount))
			}
		})

		if list != nil {
			list.View(c)
		}

		selRenderDialogFooter(c, th, "Enter to select · Esc to cancel · Type to filter", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Select").Clicked() {
				confirmClicked = true
			}
		})
	})

	if toggleAllClicked {
		d.SetScopeOnly(false)
	} else if toggleScopedClicked {
		d.SetScopeOnly(true)
	} else if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes ModelSelectorDialog as a modal overlay on host.
func (d *ModelSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            680,
		Height:           480,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// ThinkingSelectorDialog
// ============================================================================

// ThinkingSelectorConfig configures a ThinkingSelectorDialog.
type ThinkingSelectorConfig struct {
	CurrentLevel    agent.ThinkingLevel
	AvailableLevels []agent.ThinkingLevel
	Theme           *gui.GuiTheme
	OnSelect        func(level agent.ThinkingLevel)
	OnCancel        func()
}

// ThinkingSelectorDialog lets the user choose a reasoning / thinking level.
type ThinkingSelectorDialog struct {
	mu           sync.Mutex
	currentLevel agent.ThinkingLevel
	levels       []agent.ThinkingLevel
	theme        *gui.GuiTheme
	list         *gui.SelectList
	onSelect     func(level agent.ThinkingLevel)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

func selThinkingDescription(level agent.ThinkingLevel) string {
	switch level {
	case ai.ModelThinkingLevelOff:
		return "No reasoning"
	case ai.ModelThinkingLevelMinimal:
		return "Very brief reasoning (~1k tokens)"
	case ai.ModelThinkingLevelLow:
		return "Light reasoning (~4k tokens)"
	case ai.ModelThinkingLevelMedium:
		return "Balanced reasoning (~10k tokens)"
	case ai.ModelThinkingLevelHigh:
		return "Deep reasoning (~32k tokens)"
	case ai.ModelThinkingLevelXhigh:
		return "Maximum reasoning budget (model-dependent)"
	case ai.ModelThinkingLevelMax:
		return "Unbounded / maximum supported effort"
	default:
		return string(level)
	}
}

// NewThinkingSelectorDialog constructs a thinking-level picker dialog.
func NewThinkingSelectorDialog(cfg ThinkingSelectorConfig) *ThinkingSelectorDialog {
	levels := slices.Clone(cfg.AvailableLevels)
	if len(levels) == 0 {
		levels = slices.Clone(core.ThinkingLevelOptions)
	}
	d := &ThinkingSelectorDialog{
		currentLevel: cfg.CurrentLevel,
		levels:       levels,
		theme:        cfg.Theme,
		onSelect:     cfg.OnSelect,
		onCancel:     cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(false)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetMaxHeight(320)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(agent.ThinkingLevel(item.Value))
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

// SetCurrentLevel highlights level as the active thinking setting.
func (d *ThinkingSelectorDialog) SetCurrentLevel(level agent.ThinkingLevel) {
	d.mu.Lock()
	d.currentLevel = level
	d.mu.Unlock()
	d.rebuildItems()
}

// SetAvailableLevels updates the selectable thinking levels.
func (d *ThinkingSelectorDialog) SetAvailableLevels(levels []agent.ThinkingLevel) {
	if len(levels) == 0 {
		levels = core.ThinkingLevelOptions
	}
	d.mu.Lock()
	d.levels = slices.Clone(levels)
	d.mu.Unlock()
	d.rebuildItems()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ThinkingSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *ThinkingSelectorDialog) rebuildItems() {
	d.mu.Lock()
	levels := slices.Clone(d.levels)
	cur := d.currentLevel
	list := d.list
	d.mu.Unlock()

	items := make([]gui.SelectItem, 0, len(levels))
	selectedIdx := 0
	for i, lvl := range levels {
		label := string(lvl)
		badge := ""
		if lvl == cur {
			label = "✓ " + label
			badge = "active"
			selectedIdx = i
		}
		items = append(items, gui.SelectItem{
			Value:       string(lvl),
			Label:       label,
			Description: selThinkingDescription(lvl),
			Badge:       badge,
		})
	}
	if list != nil {
		list.SetItems(items)
		list.SetSelectedIndex(selectedIdx)
	}
}

func (d *ThinkingSelectorDialog) handleSelect(level agent.ThinkingLevel) {
	d.mu.Lock()
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(level)
	}
}

func (d *ThinkingSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders the ThinkingSelectorDialog content inside c.
func (d *ThinkingSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	cur := d.currentLevel
	list := d.list
	d.mu.Unlock()

	var confirmClicked, cancelClicked bool

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Select Thinking Level", "Reasoning budget for subsequent turns", func() {
			pillCol := ThinkingLevelColor(th, cur)
			ui.Box(c).Background(pillCol).Radius(th.Radius-2).Padding(3, 8).Children(func() {
				lbl := string(cur)
				if lbl == "" {
					lbl = string(ai.ModelThinkingLevelOff)
				}
				ui.Text(c, lbl).TextColor(gui.ContrastingText(pillCol, ui.RGB(248, 250, 252), ui.RGB(20, 20, 24))).FontSize(11).FontWeight(700)
			})
		})

		if list != nil {
			list.View(c)
		}

		selRenderDialogFooter(c, th, "Enter to select · Esc to cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Apply").Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes ThinkingSelectorDialog onto host as a modal overlay.
func (d *ThinkingSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            480,
		Height:           440,
		MaxWidthPercent:  90,
		MaxHeightPercent: 80,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// SessionSelectorDialog
// ============================================================================

// LoadSessionSelectorItems lists persisted sessions either for cwd or across all projects.
func LoadSessionSelectorItems(ctx context.Context, cwd, sessionDir string, allProjects bool) ([]core.SessionInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if allProjects {
		return core.SessionManagerListAll(ctx, sessionDir)
	}
	return core.SessionManagerList(ctx, cwd, sessionDir)
}

// SessionSelectorConfig configures a SessionSelectorDialog (/resume).
type SessionSelectorConfig struct {
	Sessions           []core.SessionInfo
	CurrentSessionFile string
	CurrentSessionId   string
	ShowAllProjects    bool
	Cwd                string
	SessionDir         string
	Theme              *gui.GuiTheme
	OnSelect           func(session core.SessionInfo)
	OnRename           func(session core.SessionInfo, newName string) error
	OnDelete           func(session core.SessionInfo) error
	OnFork             func(session core.SessionInfo)
	OnToggleScope      func(allProjects bool) ([]core.SessionInfo, error)
	OnCancel           func()
}

// SessionSelectorDialog presents persisted sessions with search, cwd/all toggle,
// inline rename, two-step delete confirmation, and optional fork actions.
type SessionSelectorDialog struct {
	mu                 sync.Mutex
	sessions           []core.SessionInfo
	sessionByPath      map[string]core.SessionInfo
	currentSessionFile string
	currentSessionId   string
	showAllProjects    bool
	cwd                string
	sessionDir         string
	theme              *gui.GuiTheme
	list               *gui.SelectList

	renamingPath   string
	renameDraft    string
	confirmDelPath string
	statusError    string

	onSelect      func(session core.SessionInfo)
	onRename      func(session core.SessionInfo, newName string) error
	onDelete      func(session core.SessionInfo) error
	onFork        func(session core.SessionInfo)
	onToggleScope func(allProjects bool) ([]core.SessionInfo, error)
	onCancel      func()
	closeHandle   func()
	cancelHandle  func()
}

// NewSessionSelectorDialog constructs a /resume session selector modal.
func NewSessionSelectorDialog(cfg SessionSelectorConfig) *SessionSelectorDialog {
	d := &SessionSelectorDialog{
		sessions:           slices.Clone(cfg.Sessions),
		sessionByPath:      make(map[string]core.SessionInfo),
		currentSessionFile: cfg.CurrentSessionFile,
		currentSessionId:   cfg.CurrentSessionId,
		showAllProjects:    cfg.ShowAllProjects,
		cwd:                cfg.Cwd,
		sessionDir:         cfg.SessionDir,
		theme:              cfg.Theme,
		onSelect:           cfg.OnSelect,
		onRename:           cfg.OnRename,
		onDelete:           cfg.OnDelete,
		onFork:             cfg.OnFork,
		onToggleScope:      cfg.OnToggleScope,
		onCancel:           cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(false)
	sl.SetPlaceholder("Search sessions by name, message text, id, or directory...")
	sl.SetMaxHeight(340)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(item.Value)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

// SetSessions replaces the displayed session list.
func (d *SessionSelectorDialog) SetSessions(sessions []core.SessionInfo) {
	d.mu.Lock()
	d.sessions = slices.Clone(sessions)
	d.renamingPath = ""
	d.confirmDelPath = ""
	d.statusError = ""
	d.mu.Unlock()
	d.rebuildItems()
}

// SetShowAllProjects toggles the scope mode and updates displayed metadata.
func (d *SessionSelectorDialog) SetShowAllProjects(all bool) {
	d.mu.Lock()
	d.showAllProjects = all
	d.mu.Unlock()
	d.rebuildItems()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *SessionSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *SessionSelectorDialog) rebuildItems() {
	d.mu.Lock()
	sessions := slices.Clone(d.sessions)
	curFile := d.currentSessionFile
	curId := d.currentSessionId
	showAll := d.showAllProjects
	list := d.list

	byPath := make(map[string]core.SessionInfo, len(sessions))
	items := make([]gui.SelectItem, 0, len(sessions))
	selectedIdx := -1

	for i, s := range sessions {
		key := s.Path
		if key == "" {
			key = s.Id
		}
		byPath[key] = s

		isCurrent := (curFile != "" && s.Path == curFile) || (curId != "" && s.Id == curId)
		if isCurrent && selectedIdx == -1 {
			selectedIdx = i
		}

		title := ""
		if s.Name != nil && strings.TrimSpace(*s.Name) != "" {
			title = strings.TrimSpace(*s.Name)
		} else if strings.TrimSpace(s.FirstMessage) != "" {
			title = selTruncateSingleLine(s.FirstMessage, 72)
		} else {
			title = s.Id
		}
		if isCurrent {
			title = "✓ " + title
		}

		var desc []string
		desc = append(desc, fmt.Sprintf("%d msgs", s.MessageCount))
		if showAll && s.Cwd != "" {
			desc = append(desc, utils.ShortenHomePath(s.Cwd))
		}
		if s.FirstMessage != "" && s.Name != nil && strings.TrimSpace(*s.Name) != "" {
			desc = append(desc, selTruncateSingleLine(s.FirstMessage, 64))
		}
		if s.Id != "" {
			shortId := s.Id
			if len(shortId) > 8 {
				shortId = shortId[:8]
			}
			desc = append(desc, shortId)
		}

		badge := selFormatRelativeTime(s.Modified)
		if isCurrent {
			if badge != "" {
				badge = "current · " + badge
			} else {
				badge = "current"
			}
		}

		items = append(items, gui.SelectItem{
			Value:       key,
			Label:       title,
			Description: strings.Join(desc, " · "),
			Badge:       badge,
		})
	}
	d.sessionByPath = byPath
	d.mu.Unlock()

	if list != nil {
		list.SetItems(items)
		if selectedIdx >= 0 {
			list.SetSelectedIndex(selectedIdx)
		}
	}
}

func (d *SessionSelectorDialog) handleSelect(key string) {
	d.mu.Lock()
	sess, ok := d.sessionByPath[key]
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if !ok {
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(sess)
	}
}

func (d *SessionSelectorDialog) toggleScope(all bool) {
	d.mu.Lock()
	if d.showAllProjects == all {
		d.mu.Unlock()
		return
	}
	d.showAllProjects = all
	cb := d.onToggleScope
	cwd := d.cwd
	dir := d.sessionDir
	d.statusError = ""
	d.mu.Unlock()

	if cb != nil {
		sessions, err := cb(all)
		if err != nil {
			d.mu.Lock()
			d.statusError = err.Error()
			d.mu.Unlock()
			d.rebuildItems()
			return
		}
		d.SetSessions(sessions)
		return
	}
	if cwd != "" || dir != "" {
		sessions, err := LoadSessionSelectorItems(context.Background(), cwd, dir, all)
		if err != nil {
			d.mu.Lock()
			d.statusError = err.Error()
			d.mu.Unlock()
			d.rebuildItems()
			return
		}
		d.SetSessions(sessions)
		return
	}
	d.rebuildItems()
}

func (d *SessionSelectorDialog) commitRename(path, newName string) {
	trimmed := strings.TrimSpace(newName)
	d.mu.Lock()
	sess, ok := d.sessionByPath[path]
	onRename := d.onRename
	d.mu.Unlock()
	if !ok {
		return
	}
	var err error
	if onRename != nil {
		err = onRename(sess, trimmed)
	} else if sess.Path != "" {
		sm, openErr := core.SessionManagerOpen(sess.Path)
		if openErr != nil {
			err = openErr
		} else {
			_, err = sm.AppendSessionInfo(trimmed)
		}
	}
	d.mu.Lock()
	if err != nil {
		d.statusError = fmt.Sprintf("Rename failed: %v", err)
	} else {
		d.statusError = ""
		d.renamingPath = ""
		for i := range d.sessions {
			if d.sessions[i].Path == sess.Path && d.sessions[i].Id == sess.Id {
				if trimmed == "" {
					d.sessions[i].Name = nil
				} else {
					cp := trimmed
					d.sessions[i].Name = &cp
				}
				break
			}
		}
	}
	d.mu.Unlock()
	d.rebuildItems()
}

func (d *SessionSelectorDialog) commitDelete(path string) {
	d.mu.Lock()
	sess, ok := d.sessionByPath[path]
	onDelete := d.onDelete
	d.mu.Unlock()
	if !ok {
		return
	}
	var err error
	if onDelete != nil {
		err = onDelete(sess)
	} else if sess.Path != "" {
		err = os.Remove(sess.Path)
	}
	d.mu.Lock()
	if err != nil {
		d.statusError = fmt.Sprintf("Delete failed: %v", err)
	} else {
		d.statusError = ""
		d.confirmDelPath = ""
		filtered := d.sessions[:0]
		for _, s := range d.sessions {
			if s.Path == sess.Path && s.Id == sess.Id {
				continue
			}
			filtered = append(filtered, s)
		}
		d.sessions = filtered
	}
	d.mu.Unlock()
	d.rebuildItems()
}

func (d *SessionSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders the SessionSelectorDialog inside c.
func (d *SessionSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	showAll := d.showAllProjects
	renamingPath := d.renamingPath
	confirmDelPath := d.confirmDelPath
	statusErr := d.statusError
	list := d.list
	hasFork := d.onFork != nil
	count := len(d.sessions)
	byPath := make(map[string]core.SessionInfo, len(d.sessionByPath))
	for k, v := range d.sessionByPath {
		byPath[k] = v
	}
	d.mu.Unlock()

	var highlighted *core.SessionInfo
	var highlightedKey string
	if list != nil {
		if sel, ok := list.SelectedItem(); ok {
			highlightedKey = sel.Value
			if s, found := byPath[sel.Value]; found {
				highlighted = &s
			}
		}
	}

	var (
		toggleCurrentClicked bool
		toggleAllClicked     bool
		startRenameClicked   bool
		saveRenameClicked    bool
		cancelRenameClicked  bool
		startDeleteClicked   bool
		confirmDeleteClicked bool
		cancelDeleteClicked  bool
		forkClicked          bool
		resumeClicked        bool
		cancelClicked        bool
	)

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Resume Session", fmt.Sprintf("%d sessions available", count), func() {
			if selFilterPill(c, th, "Current Folder", !showAll) {
				toggleCurrentClicked = true
			}
			if selFilterPill(c, th, "All Projects", showAll) {
				toggleAllClicked = true
			}
		})

		if list != nil {
			list.View(c)
		}

		if statusErr != "" {
			ui.Text(c, statusErr).TextColor(th.Error).FontSize(11)
		}

		if renamingPath != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).
				Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius-2).Padding(8, 10).
				Children(func() {
					ui.Text(c, "Rename:").TextColor(th.Muted).FontSize(12)
					in := ui.TextInput(c, &d.renameDraft).Placeholder("Session title...").FillWidth().AutoFocus()
					if in.Submitted() {
						saveRenameClicked = true
					}
					if ui.Button(c, "Save").Clicked() {
						saveRenameClicked = true
					}
					if ui.Button(c, "Cancel").Clicked() {
						cancelRenameClicked = true
					}
				})
		} else if confirmDelPath != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).
				Background(th.Surface).Border(1, th.Error).Radius(th.Radius-2).Padding(8, 10).
				Children(func() {
					ui.Text(c, "Permanently delete this session file?").TextColor(th.Error).FontSize(12).FontWeight(600)
					ui.Row(c).Shrink(0).Gap(6).Children(func() {
						if ui.Button(c, "Keep").Clicked() {
							cancelDeleteClicked = true
						}
						if ui.Button(c, "Confirm Delete").Clicked() {
							confirmDeleteClicked = true
						}
					})
				})
		}

		selRenderDialogFooter(c, th, "Enter to resume · Esc to cancel", func() {
			if highlighted != nil && renamingPath == "" && confirmDelPath == "" {
				if ui.Button(c, "Rename").Clicked() {
					startRenameClicked = true
				}
				if hasFork && ui.Button(c, "Fork").Clicked() {
					forkClicked = true
				}
				if ui.Button(c, "Delete").Clicked() {
					startDeleteClicked = true
				}
			}
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if highlighted != nil && ui.Button(c, "Resume").Clicked() {
				resumeClicked = true
			}
		})
	})

	switch {
	case toggleCurrentClicked:
		d.toggleScope(false)
	case toggleAllClicked:
		d.toggleScope(true)
	case startRenameClicked && highlighted != nil:
		d.mu.Lock()
		d.confirmDelPath = ""
		d.renamingPath = highlightedKey
		if highlighted.Name != nil {
			d.renameDraft = *highlighted.Name
		} else {
			d.renameDraft = ""
		}
		d.mu.Unlock()
	case saveRenameClicked && renamingPath != "":
		d.commitRename(renamingPath, d.renameDraft)
	case cancelRenameClicked:
		d.mu.Lock()
		d.renamingPath = ""
		d.mu.Unlock()
	case startDeleteClicked && highlighted != nil:
		d.mu.Lock()
		d.renamingPath = ""
		d.confirmDelPath = highlightedKey
		d.mu.Unlock()
	case confirmDeleteClicked && confirmDelPath != "":
		d.commitDelete(confirmDelPath)
	case cancelDeleteClicked:
		d.mu.Lock()
		d.confirmDelPath = ""
		d.mu.Unlock()
	case forkClicked && highlighted != nil:
		d.mu.Lock()
		forkFn := d.onFork
		closeFn := d.closeHandle
		d.mu.Unlock()
		if closeFn != nil {
			closeFn()
		}
		if forkFn != nil {
			forkFn(*highlighted)
		}
	case resumeClicked && highlighted != nil:
		d.handleSelect(highlightedKey)
	case cancelClicked:
		d.triggerCancel()
	}

	return root
}

// Show pushes SessionSelectorDialog onto host as a modal overlay.
func (d *SessionSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            720,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// TreeSelectorDialog
// ============================================================================

// TreeSelectorConfig configures a TreeSelectorDialog (/tree).
type TreeSelectorConfig struct {
	Nodes          []core.SessionTreeNode
	CurrentLeafId  string
	FilterMode     core.TreeFilterMode
	Theme          *gui.GuiTheme
	OnSelect       func(entryId string)
	OnLabel        func(entryId string, label *string)
	OnFilterChange func(mode core.TreeFilterMode)
	OnCancel       func()
}

type selTreeRow struct {
	entryId     string
	role        string
	summary     string
	label       string
	depth       int
	prefix      string
	hasChildren bool
	folded      bool
	isLeaf      bool
	isActive    bool
	searchText  string
}

// TreeSelectorDialog presents the branching conversation DAG for navigation and labeling.
type TreeSelectorDialog struct {
	mu            sync.Mutex
	nodes         []core.SessionTreeNode
	currentLeafId string
	filterMode    core.TreeFilterMode
	searchQuery   string
	selectedId    string
	foldedIds     map[string]bool
	editingLabel  string
	labelDraft    string
	theme         *gui.GuiTheme

	onSelect       func(entryId string)
	onLabel        func(entryId string, label *string)
	onFilterChange func(mode core.TreeFilterMode)
	onCancel       func()
	closeHandle    func()
	cancelHandle   func()
}

// NewTreeSelectorDialog constructs a /tree navigation modal.
func NewTreeSelectorDialog(cfg TreeSelectorConfig) *TreeSelectorDialog {
	mode := cfg.FilterMode
	if mode == "" {
		mode = core.TreeFilterModeDefault
	}
	return &TreeSelectorDialog{
		nodes:          slices.Clone(cfg.Nodes),
		currentLeafId:  cfg.CurrentLeafId,
		filterMode:     mode,
		selectedId:     cfg.CurrentLeafId,
		foldedIds:      make(map[string]bool),
		theme:          cfg.Theme,
		onSelect:       cfg.OnSelect,
		onLabel:        cfg.OnLabel,
		onFilterChange: cfg.OnFilterChange,
		onCancel:       cfg.OnCancel,
	}
}

// SetNodes replaces the session tree nodes displayed by the dialog.
func (d *TreeSelectorDialog) SetNodes(nodes []core.SessionTreeNode) {
	d.mu.Lock()
	d.nodes = slices.Clone(nodes)
	d.mu.Unlock()
}

// SetCurrentLeafId updates the currently active branch leaf identifier.
func (d *TreeSelectorDialog) SetCurrentLeafId(leafId string) {
	d.mu.Lock()
	d.currentLeafId = leafId
	if d.selectedId == "" {
		d.selectedId = leafId
	}
	d.mu.Unlock()
}

// SetFilterMode switches the tree entry filter mode.
func (d *TreeSelectorDialog) SetFilterMode(mode core.TreeFilterMode) {
	if mode == "" {
		mode = core.TreeFilterModeDefault
	}
	d.mu.Lock()
	d.filterMode = mode
	cb := d.onFilterChange
	d.mu.Unlock()
	if cb != nil {
		cb(mode)
	}
}

// SetTheme updates the GUI theme used by the dialog.
func (d *TreeSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func selSummarizeTreeNode(node core.SessionTreeNode) (role, summary, label string, isUser, isTool, isMeta bool) {
	if node.Label != nil {
		label = strings.TrimSpace(*node.Label)
	}
	if node.Entry == nil {
		return "entry", "", label, false, false, true
	}
	switch e := node.Entry.(type) {
	case *core.SessionMessageEntry:
		switch m := e.Message.(type) {
		case *ai.UserMessage:
			txt, imgs := msgExtractTextAndImages(m.Content)
			if txt == "" && len(imgs) > 0 {
				txt = fmt.Sprintf("[%d image(s)]", len(imgs))
			}
			return "user", selTruncateSingleLine(txt, 90), label, true, false, false
		case *ai.AssistantMessage:
			var parts []string
			var toolNames []string
			for _, blk := range m.Content {
				switch b := blk.(type) {
				case *ai.TextContent:
					if strings.TrimSpace(b.Text) != "" {
						parts = append(parts, b.Text)
					}
				case *ai.ToolCall:
					if b.Name != "" {
						toolNames = append(toolNames, b.Name)
					}
				}
			}
			summary = selTruncateSingleLine(strings.Join(parts, " "), 90)
			if summary == "" && len(toolNames) > 0 {
				summary = "Tools: " + strings.Join(toolNames, ", ")
			}
			if summary == "" && m.ErrorMessage != nil {
				summary = selTruncateSingleLine(*m.ErrorMessage, 90)
			}
			return "assistant", summary, label, false, false, false
		case *ai.ToolResultMessage:
			txt := ""
			for _, blk := range m.Content {
				if tc, ok := blk.(*ai.TextContent); ok {
					txt += tc.Text + " "
				}
			}
			name := m.ToolName
			if name == "" {
				name = "tool"
			}
			return "tool:" + name, selTruncateSingleLine(txt, 80), label, false, true, false
		case *core.BashExecutionMessage:
			return "bash", "$ " + selTruncateSingleLine(m.Command, 80), label, true, false, false
		case *core.CompactionSummaryMessage:
			return "compaction", fmt.Sprintf("Compacted (%s tokens): %s", FormatTokens(m.TokensBefore), selTruncateSingleLine(m.Summary, 64)), label, false, false, false
		case *core.BranchSummaryMessage:
			return "branch", selTruncateSingleLine(m.Summary, 80), label, false, false, false
		case *core.CustomMessage:
			return "custom:" + m.CustomType, selTruncateSingleLine(m.CustomType, 80), label, false, false, false
		default:
			return "message", string(e.GetType()), label, false, false, false
		}
	case *core.CompactionEntry:
		return "compaction", fmt.Sprintf("Compacted (%s tokens): %s", FormatTokens(e.TokensBefore), selTruncateSingleLine(e.Summary, 64)), label, false, false, false
	case *core.BranchSummaryEntry:
		return "branch", selTruncateSingleLine(e.Summary, 80), label, false, false, false
	case *core.CustomMessageEntry:
		return "custom:" + e.CustomType, e.CustomType, label, false, false, false
	case *core.CustomEntry:
		return "custom-state", e.CustomType, label, false, false, true
	case *core.ModelChangeEntry:
		return "model", e.Provider + "/" + e.ModelId, label, false, false, true
	case *core.ThinkingLevelChangeEntry:
		return "thinking", e.ThinkingLevel, label, false, false, true
	case *core.LabelEntry:
		val := ""
		if e.Label != nil {
			val = *e.Label
		}
		return "label", e.TargetId + " = " + val, label, false, false, true
	case *core.SessionInfoEntry:
		val := ""
		if e.Name != nil {
			val = *e.Name
		}
		return "session", val, label, false, false, true
	case *core.ContextEditEntry:
		return "edit", e.TargetId, label, false, false, true
	default:
		return string(e.GetType()), string(e.GetType()), label, false, false, true
	}
}

func selMatchesTreeFilter(mode core.TreeFilterMode, isUser, isTool, isMeta bool, hasLabel bool) bool {
	switch mode {
	case core.TreeFilterModeAll:
		return true
	case core.TreeFilterModeUserOnly:
		return isUser
	case core.TreeFilterModeLabeledOnly:
		return hasLabel
	case core.TreeFilterModeNoTools:
		return !isTool && !isMeta
	default: // TreeFilterModeDefault
		return !isMeta
	}
}

func selComputeActivePath(nodes []core.SessionTreeNode, leafId string) map[string]bool {
	active := make(map[string]bool)
	if leafId == "" {
		return active
	}
	var walk func(core.SessionTreeNode) bool
	walk = func(n core.SessionTreeNode) bool {
		id := ""
		if n.Entry != nil {
			id = n.Entry.GetId()
		}
		found := id == leafId
		for _, ch := range n.Children {
			if walk(ch) {
				found = true
			}
		}
		if found && id != "" {
			active[id] = true
		}
		return found
	}
	for _, n := range nodes {
		walk(n)
	}
	return active
}

func selBuildTreeRows(
	nodes []core.SessionTreeNode,
	leafId string,
	mode core.TreeFilterMode,
	query string,
	folded map[string]bool,
) []selTreeRow {
	activeSet := selComputeActivePath(nodes, leafId)
	q := strings.ToLower(strings.TrimSpace(query))

	var rows []selTreeRow
	var walk func(node core.SessionTreeNode, depth int, prefix string, isLast bool, hasParent bool)
	walk = func(node core.SessionTreeNode, depth int, prefix string, isLast bool, hasParent bool) {
		role, summary, label, isUser, isTool, isMeta := selSummarizeTreeNode(node)
		id := ""
		if node.Entry != nil {
			id = node.Entry.GetId()
		}
		visible := selMatchesTreeFilter(mode, isUser, isTool, isMeta, label != "")
		searchBlob := strings.ToLower(role + " " + summary + " " + label + " " + id)
		if q != "" && !strings.Contains(searchBlob, q) {
			visible = false
		}

		isFolded := id != "" && folded[id]
		if visible {
			conn := ""
			childPrefix := prefix
			if hasParent {
				if isLast {
					conn = prefix + "└─ "
					childPrefix = prefix + "   "
				} else {
					conn = prefix + "├─ "
					childPrefix = prefix + "│  "
				}
			}
			rows = append(rows, selTreeRow{
				entryId:     id,
				role:        role,
				summary:     summary,
				label:       label,
				depth:       depth,
				prefix:      conn,
				hasChildren: len(node.Children) > 0,
				folded:      isFolded,
				isLeaf:      id != "" && id == leafId,
				isActive:    id != "" && activeSet[id],
				searchText:  searchBlob,
			})
			if isFolded {
				return
			}
			for i, ch := range node.Children {
				walk(ch, depth+1, childPrefix, i == len(node.Children)-1, true)
			}
			return
		}

		// Promote children transparently when this node is filtered out.
		for i, ch := range node.Children {
			walk(ch, depth, prefix, hasParent && isLast && i == len(node.Children)-1, hasParent)
		}
	}

	for i, n := range nodes {
		walk(n, 0, "", i == len(nodes)-1, false)
	}
	return rows
}

func selUpdateNodeLabel(nodes []core.SessionTreeNode, targetId string, label *string) bool {
	for i := range nodes {
		if nodes[i].Entry != nil && nodes[i].Entry.GetId() == targetId {
			if label == nil || strings.TrimSpace(*label) == "" {
				nodes[i].Label = nil
			} else {
				cp := strings.TrimSpace(*label)
				nodes[i].Label = &cp
			}
			return true
		}
		if selUpdateNodeLabel(nodes[i].Children, targetId, label) {
			return true
		}
	}
	return false
}

func (d *TreeSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders the TreeSelectorDialog content inside c.
func (d *TreeSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	nodes := slices.Clone(d.nodes)
	leafId := d.currentLeafId
	mode := d.filterMode
	selectedId := d.selectedId
	editingLabelId := d.editingLabel
	folded := make(map[string]bool, len(d.foldedIds))
	for k, v := range d.foldedIds {
		folded[k] = v
	}
	hasLabelCb := d.onLabel != nil
	d.mu.Unlock()

	rows := selBuildTreeRows(nodes, leafId, mode, d.searchQuery, folded)
	var selectedRow *selTreeRow
	for i := range rows {
		if rows[i].entryId == selectedId {
			selectedRow = &rows[i]
			break
		}
	}
	if selectedRow == nil && len(rows) > 0 {
		selectedId = rows[0].entryId
		selectedRow = &rows[0]
		d.mu.Lock()
		d.selectedId = selectedId
		d.mu.Unlock()
	}

	var (
		nextFilterMode    core.TreeFilterMode
		toggleFoldId      string
		clickedRowId      string
		navigateId        string
		startEditLabelId  string
		saveLabelClicked  bool
		clearLabelClicked bool
		cancelEditClicked bool
		cancelClicked     bool
	)

	filterModes := []struct {
		mode  core.TreeFilterMode
		label string
	}{
		{core.TreeFilterModeDefault, "Default"},
		{core.TreeFilterModeNoTools, "No Tools"},
		{core.TreeFilterModeUserOnly, "User Only"},
		{core.TreeFilterModeLabeledOnly, "Labeled"},
		{core.TreeFilterModeAll, "All"},
	}

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Session Tree", fmt.Sprintf("%d visible nodes", len(rows)), func() {
			for _, fm := range filterModes {
				m := fm.mode
				if selFilterPill(c, th, fm.label, mode == m) {
					nextFilterMode = m
				}
			}
		})

		ui.TextInput(c, &d.searchQuery).
			Placeholder("Filter tree by text, role, label, or entry id...").
			FillWidth()

		if len(rows) == 0 {
			ui.Box(c).FillWidth().Padding(16).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius - 2).Children(func() {
				ui.Text(c, "No matching tree entries").TextColor(th.Muted).FontSize(12)
			})
		} else {
			scroll := ui.Scroll(c).FillWidth().MaxHeight(340).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius - 2).Padding(4)
			scroll.Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					for _, row := range rows {
						r := row
						isSelected := r.entryId != "" && r.entryId == selectedId
						rowBox := ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).
							Padding(5, 8).Radius(th.Radius - 3).Cursor(ui.CursorPointer)
						if isSelected {
							rowBox.Background(th.SelectedBg)
						}
						rowBox.Children(func() {
							ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(6).Children(func() {
								if r.prefix != "" {
									ui.Text(c, r.prefix).Font("monospace").TextColor(th.Dim).FontSize(11)
								}
								if r.hasChildren {
									chev := "▾"
									if r.folded {
										chev = "▸"
									}
									btn := ui.Text(c, chev).Font("monospace").TextColor(th.Muted).FontSize(11).Padding(1, 3).Cursor(ui.CursorPointer)
									if btn.Clicked() {
										toggleFoldId = r.entryId
									}
								}
								roleColor := th.Muted
								if r.role == "user" {
									roleColor = th.Accent
								} else if r.isActive {
									roleColor = th.Success
								}
								ui.Text(c, "["+r.role+"]").TextColor(roleColor).FontSize(11).FontWeight(600)
								ui.Text(c, r.summary).TextColor(th.Text).FontSize(12).SingleLine()
							})
							ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
								if r.label != "" {
									ui.Badge(c, "#"+r.label)
								}
								if r.isLeaf {
									ui.Badge(c, "active")
								}
							})
						})
						if toggleFoldId != r.entryId && rowBox.Clicked() {
							clickedRowId = r.entryId
						}
					}
				})
			})
		}

		if editingLabelId != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).
				Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius-2).Padding(8, 10).
				Children(func() {
					ui.Text(c, "Label:").TextColor(th.Muted).FontSize(12)
					in := ui.TextInput(c, &d.labelDraft).Placeholder("Bookmark label...").FillWidth().AutoFocus()
					if in.Submitted() {
						saveLabelClicked = true
					}
					if ui.Button(c, "Save").Clicked() {
						saveLabelClicked = true
					}
					if ui.Button(c, "Clear").Clicked() {
						clearLabelClicked = true
					}
					if ui.Button(c, "Cancel").Clicked() {
						cancelEditClicked = true
					}
				})
		}

		selRenderDialogFooter(c, th, "Click row to select · Esc to cancel", func() {
			if hasLabelCb && selectedRow != nil && editingLabelId == "" {
				if ui.Button(c, "Edit Label").Clicked() {
					startEditLabelId = selectedRow.entryId
				}
			}
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if selectedRow != nil && ui.Button(c, "Navigate Here").Clicked() {
				navigateId = selectedRow.entryId
			}
		})
	})

	switch {
	case nextFilterMode != "":
		d.SetFilterMode(nextFilterMode)
	case toggleFoldId != "":
		d.mu.Lock()
		d.foldedIds[toggleFoldId] = !d.foldedIds[toggleFoldId]
		d.mu.Unlock()
	case clickedRowId != "":
		d.mu.Lock()
		d.selectedId = clickedRowId
		d.mu.Unlock()
	case startEditLabelId != "" && selectedRow != nil:
		d.mu.Lock()
		d.editingLabel = startEditLabelId
		d.labelDraft = selectedRow.label
		d.mu.Unlock()
	case saveLabelClicked && editingLabelId != "":
		val := strings.TrimSpace(d.labelDraft)
		var ptr *string
		if val != "" {
			ptr = &val
		}
		d.mu.Lock()
		selUpdateNodeLabel(d.nodes, editingLabelId, ptr)
		d.editingLabel = ""
		cb := d.onLabel
		d.mu.Unlock()
		if cb != nil {
			cb(editingLabelId, ptr)
		}
	case clearLabelClicked && editingLabelId != "":
		d.mu.Lock()
		selUpdateNodeLabel(d.nodes, editingLabelId, nil)
		d.editingLabel = ""
		cb := d.onLabel
		d.mu.Unlock()
		if cb != nil {
			cb(editingLabelId, nil)
		}
	case cancelEditClicked:
		d.mu.Lock()
		d.editingLabel = ""
		d.mu.Unlock()
	case navigateId != "":
		d.mu.Lock()
		closeFn := d.closeHandle
		cb := d.onSelect
		d.mu.Unlock()
		if closeFn != nil {
			closeFn()
		}
		if cb != nil {
			cb(navigateId)
		}
	case cancelClicked:
		d.triggerCancel()
	}

	return root
}

// Show pushes TreeSelectorDialog onto host as a modal overlay.
func (d *TreeSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            740,
		MaxWidthPercent:  94,
		MaxHeightPercent: 88,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// SettingsSelectorDialog + ApplySettingsChange
// ============================================================================

// SettingsSelectorConfig configures a SettingsSelectorDialog (/settings).
type SettingsSelectorConfig struct {
	SettingsManager *core.SettingsManager
	Session         *core.AgentSession
	AvailableThemes []string
	AvailableModels []*ai.Model
	Theme           *gui.GuiTheme
	OnChange        func(id, value string)
	OnThemePreview  func(themeName string)
	OnCancel        func()
}

// SettingsSelectorDialog renders categorized pi settings using gui.SettingsList
// with nested submenu drill-down for default model, theme, and thinking level.
type SettingsSelectorDialog struct {
	mu              sync.Mutex
	settingsManager *core.SettingsManager
	session         *core.AgentSession
	availableThemes []string
	availableModels []*ai.Model
	theme           *gui.GuiTheme
	list            *gui.SettingsList

	submenuID    string
	submenuTitle string
	submenuList  *gui.SelectList

	onChange       func(id, value string)
	onThemePreview func(themeName string)
	onCancel       func()
	closeHandle    func()
	cancelHandle   func()
}

func selBoolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func selParseBool(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

func selQuietStartupString(q core.QuietStartup) string {
	if q.Header {
		return "header"
	}
	if q.Silent {
		return "true"
	}
	return "false"
}

// ApplySettingsChange applies an interactive setting change to SettingsManager and
// (when non-nil) immediately updates the running AgentSession.
func ApplySettingsChange(sm *core.SettingsManager, session *core.AgentSession, id, value string) {
	switch id {
	case "autocompact":
		b := selParseBool(value)
		if sm != nil {
			sm.SetCompactionEnabled(b)
		}
		if session != nil {
			session.SetAutoCompactionEnabled(b)
		}
	case "autoRetry":
		b := selParseBool(value)
		if sm != nil {
			sm.SetRetryEnabled(b)
		}
		if session != nil {
			session.SetAutoRetryEnabled(b)
		}
	case "steeringMode":
		m := agent.QueueMode(value)
		if sm != nil {
			sm.SetSteeringMode(m)
		}
		if session != nil {
			session.SetSteeringMode(m)
		}
	case "followUpMode":
		m := agent.QueueMode(value)
		if sm != nil {
			sm.SetFollowUpMode(m)
		}
		if session != nil {
			session.SetFollowUpMode(m)
		}
	case "transport":
		if sm != nil {
			sm.SetTransport(core.TransportSetting(value))
		}
	case "cacheWarming":
		if sm != nil {
			sm.SetCacheWarmingMode(core.CacheWarmingMode(value))
		}
	case "hideThinkingBlock":
		if sm != nil {
			sm.SetHideThinkingBlock(selParseBool(value))
		}
	case "showCacheMissNotices":
		if sm != nil {
			sm.SetShowCacheMissNotices(selParseBool(value))
		}
	case "showImages":
		if sm != nil {
			sm.SetShowImages(selParseBool(value))
		}
	case "imageAutoResize":
		if sm != nil {
			sm.SetImageAutoResize(selParseBool(value))
		}
	case "blockImages":
		if sm != nil {
			sm.SetBlockImages(selParseBool(value))
		}
	case "collapseChangelog":
		if sm != nil {
			sm.SetCollapseChangelog(selParseBool(value))
		}
	case "doubleEscapeAction":
		if sm != nil {
			sm.SetDoubleEscapeAction(core.DoubleEscapeAction(value))
		}
	case "treeFilterMode":
		if sm != nil {
			sm.SetTreeFilterMode(core.TreeFilterMode(value))
		}
	case "mermaidRenderingMode":
		if sm != nil {
			sm.SetMermaidRenderingMode(core.MermaidRenderingMode(value))
		}
	case "defaultProjectTrust":
		if sm != nil {
			sm.SetDefaultProjectTrust(core.DefaultProjectTrust(value))
		}
	case "quietStartup":
		if sm != nil {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "header":
				sm.SetQuietStartup(core.QuietStartupHeader())
			case "true":
				sm.SetQuietStartup(core.QuietStartupBool(true))
			default:
				sm.SetQuietStartup(core.QuietStartupBool(false))
			}
		}
	case "thinkingLevel":
		lvl := agent.ThinkingLevel(value)
		if sm != nil {
			sm.SetDefaultThinkingLevel(lvl)
		}
		if session != nil {
			session.SetThinkingLevel(lvl)
		}
	case "theme":
		if sm != nil {
			sm.SetTheme(value)
		}
	case "defaultModel":
		if sm != nil {
			if prov, modelId, ok := strings.Cut(value, "/"); ok {
				sm.SetDefaultModelAndProvider(prov, modelId)
			} else if value != "" {
				sm.SetDefaultModel(value)
			}
		}
	}
}

// NewSettingsSelectorDialog constructs a /settings configuration dialog.
func NewSettingsSelectorDialog(cfg SettingsSelectorConfig) *SettingsSelectorDialog {
	d := &SettingsSelectorDialog{
		settingsManager: cfg.SettingsManager,
		session:         cfg.Session,
		availableThemes: slices.Clone(cfg.AvailableThemes),
		availableModels: slices.Clone(cfg.AvailableModels),
		theme:           cfg.Theme,
		onChange:        cfg.OnChange,
		onThemePreview:  cfg.OnThemePreview,
		onCancel:        cfg.OnCancel,
	}
	if len(d.availableThemes) == 0 {
		d.availableThemes = theme.GetAvailableThemes()
	}
	if len(d.availableModels) == 0 && d.session != nil && d.session.ModelRegistry() != nil {
		d.availableModels = d.session.ModelRegistry().GetAvailable()
	}

	sl := gui.NewSettingsList(nil)
	sl.SetEnableSearch(true)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnChange(func(id, newValue string) {
		d.handleSettingChange(id, newValue)
	})
	sl.SetOnOpenSubmenu(func(item gui.SettingItem) {
		d.openSubmenu(item)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.RefreshFromManager()
	return d
}

// RefreshFromManager reloads all setting rows from SettingsManager and AgentSession.
func (d *SettingsSelectorDialog) RefreshFromManager() {
	d.mu.Lock()
	sm := d.settingsManager
	sess := d.session
	list := d.list
	d.mu.Unlock()

	autoCompact := true
	autoRetry := true
	steering := string(agent.QueueModeOneAtATime)
	followUp := string(agent.QueueModeOneAtATime)
	transport := string(core.TransportAuto)
	cacheWarming := string(core.CacheWarmingOff)
	hideThinking := false
	showCacheMiss := false
	showImages := true
	autoResize := true
	blockImages := false
	collapseChangelog := false
	doubleEscape := string(core.DoubleEscapeActionTree)
	treeFilter := string(core.TreeFilterModeDefault)
	mermaidMode := string(core.MermaidRenderingFinal)
	projectTrust := string(core.DefaultProjectTrustAsk)
	quietStartup := "false"
	thinkingVal := string(core.DefaultThinkingLevel)
	themeVal := "system"
	defaultModelVal := "(none)"

	if sm != nil {
		autoCompact = sm.GetCompactionEnabled()
		autoRetry = sm.GetRetryEnabled()
		steering = string(sm.GetSteeringMode())
		followUp = string(sm.GetFollowUpMode())
		transport = string(sm.GetTransport())
		cacheWarming = string(sm.GetCacheWarmingMode())
		hideThinking = sm.GetHideThinkingBlock()
		showCacheMiss = sm.GetShowCacheMissNotices()
		showImages = sm.GetShowImages()
		autoResize = sm.GetImageAutoResize()
		blockImages = sm.GetBlockImages()
		collapseChangelog = sm.GetCollapseChangelog()
		doubleEscape = string(sm.GetDoubleEscapeAction())
		treeFilter = string(sm.GetTreeFilterMode())
		mermaidMode = string(sm.GetMermaidRenderingMode())
		projectTrust = string(sm.GetDefaultProjectTrust())
		quietStartup = selQuietStartupString(sm.GetQuietStartup())
		if t := sm.GetThemeSetting(); t != nil && *t != "" {
			themeVal = *t
		} else if t := sm.GetTheme(); t != nil && *t != "" {
			themeVal = *t
		}
		if tl := sm.GetDefaultThinkingLevel(); tl != nil && *tl != "" {
			thinkingVal = string(*tl)
		}
		prov := ""
		if p := sm.GetDefaultProvider(); p != nil {
			prov = *p
		}
		mod := ""
		if m := sm.GetDefaultModel(); m != nil {
			mod = *m
		}
		if prov != "" && mod != "" {
			defaultModelVal = selModelKey(prov, mod)
		} else if mod != "" {
			defaultModelVal = mod
		}
	}
	if sess != nil {
		autoCompact = sess.AutoCompactionEnabled()
		autoRetry = sess.AutoRetryEnabled()
		if tl := sess.ThinkingLevel(); tl != "" {
			thinkingVal = string(tl)
		}
	}

	items := []gui.SettingItem{
		{ID: "theme", Label: "Theme", Description: "Active color theme (built-in, system, or custom JSON)", Kind: gui.SettingSubmenu, CurrentValue: themeVal},
		{ID: "thinkingLevel", Label: "Thinking Level", Description: "Reasoning budget for reasoning-capable models", Kind: gui.SettingSubmenu, CurrentValue: thinkingVal},
		{ID: "defaultModel", Label: "Default Model", Description: "Default provider/model used for new sessions", Kind: gui.SettingSubmenu, CurrentValue: defaultModelVal},
		{ID: "autocompact", Label: "Auto-Compact", Description: "Automatically compact context near the window limit", Kind: gui.SettingToggle, CurrentValue: selBoolString(autoCompact)},
		{ID: "autoRetry", Label: "Auto-Retry", Description: "Automatically retry transient provider/rate-limit errors", Kind: gui.SettingToggle, CurrentValue: selBoolString(autoRetry)},
		{ID: "steeringMode", Label: "Steering Mode", Description: "How queued steering messages are delivered during runs", Kind: gui.SettingEnum, CurrentValue: steering, Values: []string{string(agent.QueueModeOneAtATime), string(agent.QueueModeAll)}},
		{ID: "followUpMode", Label: "Follow-up Mode", Description: "How queued follow-up messages are delivered after a turn", Kind: gui.SettingEnum, CurrentValue: followUp, Values: []string{string(agent.QueueModeOneAtATime), string(agent.QueueModeAll)}},
		{ID: "transport", Label: "Transport", Description: "Preferred API wire protocol (SSE vs WebSocket)", Kind: gui.SettingEnum, CurrentValue: transport, Values: []string{string(core.TransportAuto), string(core.TransportSse), string(core.TransportWebsocket), string(core.TransportWebsocketCached)}},
		{ID: "cacheWarming", Label: "Cache Warming", Description: "Prompt cache warming cadence during streaming/idle", Kind: gui.SettingEnum, CurrentValue: cacheWarming, Values: []string{string(core.CacheWarmingOff), string(core.CacheWarmingStreaming), string(core.CacheWarmingIdle)}},
		{ID: "hideThinkingBlock", Label: "Hide Thinking Blocks", Description: "Collapse/hide assistant reasoning blocks by default", Kind: gui.SettingToggle, CurrentValue: selBoolString(hideThinking)},
		{ID: "showCacheMissNotices", Label: "Show Cache Miss Notices", Description: "Display provider prompt cache miss diagnostics", Kind: gui.SettingToggle, CurrentValue: selBoolString(showCacheMiss)},
		{ID: "showImages", Label: "Show Images", Description: "Render image attachments inline in transcript and tool views", Kind: gui.SettingToggle, CurrentValue: selBoolString(showImages)},
		{ID: "imageAutoResize", Label: "Auto-Resize Images", Description: "Downscale oversized images before upload", Kind: gui.SettingToggle, CurrentValue: selBoolString(autoResize)},
		{ID: "blockImages", Label: "Block Images", Description: "Prevent attaching or sending image payloads to providers", Kind: gui.SettingToggle, CurrentValue: selBoolString(blockImages)},
		{ID: "treeFilterMode", Label: "Default Tree Filter", Description: "Initial filter when opening /tree", Kind: gui.SettingEnum, CurrentValue: treeFilter, Values: []string{string(core.TreeFilterModeDefault), string(core.TreeFilterModeNoTools), string(core.TreeFilterModeUserOnly), string(core.TreeFilterModeLabeledOnly), string(core.TreeFilterModeAll)}},
		{ID: "doubleEscapeAction", Label: "Double-Escape Action", Description: "Action when pressing Esc twice in an empty editor", Kind: gui.SettingEnum, CurrentValue: doubleEscape, Values: []string{string(core.DoubleEscapeActionTree), string(core.DoubleEscapeActionFork), string(core.DoubleEscapeActionNone)}},
		{ID: "mermaidRenderingMode", Label: "Mermaid Rendering", Description: "When to render fenced mermaid diagrams", Kind: gui.SettingEnum, CurrentValue: mermaidMode, Values: []string{string(core.MermaidRenderingOff), string(core.MermaidRenderingFinal), string(core.MermaidRenderingStreaming)}},
		{ID: "defaultProjectTrust", Label: "Default Project Trust", Description: "Project configuration trust prompt policy", Kind: gui.SettingEnum, CurrentValue: projectTrust, Values: []string{string(core.DefaultProjectTrustAsk), string(core.DefaultProjectTrustAlways), string(core.DefaultProjectTrustNever)}},
		{ID: "quietStartup", Label: "Quiet Startup", Description: "Suppress welcome chrome on launch", Kind: gui.SettingEnum, CurrentValue: quietStartup, Values: []string{"false", "true", "header"}},
		{ID: "collapseChangelog", Label: "Collapse Changelog", Description: "Show condensed changelog summary on updates", Kind: gui.SettingToggle, CurrentValue: selBoolString(collapseChangelog)},
	}

	if list != nil {
		list.SetItems(items)
	}
}

func (d *SettingsSelectorDialog) handleSettingChange(id, newValue string) {
	d.mu.Lock()
	sm := d.settingsManager
	sess := d.session
	onChange := d.onChange
	onPreview := d.onThemePreview
	d.mu.Unlock()

	ApplySettingsChange(sm, sess, id, newValue)
	if id == "theme" && onPreview != nil {
		onPreview(newValue)
	}
	if onChange != nil {
		onChange(id, newValue)
	}
	d.RefreshFromManager()
}

func (d *SettingsSelectorDialog) openSubmenu(item gui.SettingItem) {
	d.mu.Lock()
	th := d.theme
	themes := slices.Clone(d.availableThemes)
	models := slices.Clone(d.availableModels)
	sess := d.session
	d.mu.Unlock()

	if len(models) == 0 && sess != nil && sess.ModelRegistry() != nil {
		models = sess.ModelRegistry().GetAvailable()
	}
	if len(themes) == 0 {
		themes = theme.GetAvailableThemes()
	}

	var selectItems []gui.SelectItem
	selectedIdx := 0
	searchable := false
	switch item.ID {
	case "theme":
		for i, name := range themes {
			badge := ""
			if name == item.CurrentValue {
				badge = "active"
				selectedIdx = i
			}
			selectItems = append(selectItems, gui.SelectItem{Value: name, Label: name, Badge: badge})
		}
	case "thinkingLevel":
		levels := core.ThinkingLevelOptions
		if sess != nil {
			levels = sess.GetAvailableThinkingLevels()
		}
		for i, lvl := range levels {
			s := string(lvl)
			badge := ""
			if s == item.CurrentValue {
				badge = "active"
				selectedIdx = i
			}
			selectItems = append(selectItems, gui.SelectItem{
				Value:       s,
				Label:       s,
				Description: selThinkingDescription(lvl),
				Badge:       badge,
			})
		}
	case "defaultModel":
		searchable = true
		for i, m := range models {
			if m == nil {
				continue
			}
			key := selModelKey(string(m.Provider), m.Id)
			badge := string(m.Provider)
			if key == item.CurrentValue {
				badge = "active"
				selectedIdx = i
			}
			lbl := m.Name
			if lbl == "" {
				lbl = m.Id
			}
			selectItems = append(selectItems, gui.SelectItem{
				Value:       key,
				Label:       lbl,
				Description: key,
				Badge:       badge,
			})
		}
	}

	sub := gui.NewSelectList(selectItems)
	sub.SetSearchable(searchable)
	sub.SetAutoFocus(true)
	sub.SetConfirmOnClick(true)
	sub.SetMaxHeight(340)
	sub.SetSelectedIndex(selectedIdx)
	if th != nil {
		sub.SetTheme(th)
	}
	sub.SetOnSelect(func(chosen gui.SelectItem) {
		d.handleSettingChange(item.ID, chosen.Value)
		d.closeSubmenu()
	})
	sub.SetOnCancel(func() {
		d.closeSubmenu()
	})

	d.mu.Lock()
	d.submenuID = item.ID
	d.submenuTitle = item.Label
	d.submenuList = sub
	d.mu.Unlock()
}

func (d *SettingsSelectorDialog) closeSubmenu() {
	d.mu.Lock()
	d.submenuID = ""
	d.submenuTitle = ""
	d.submenuList = nil
	d.mu.Unlock()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *SettingsSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	sub := d.submenuList
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
	if sub != nil {
		sub.SetTheme(th)
	}
}

func (d *SettingsSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders SettingsSelectorDialog or its active submenu inside c.
func (d *SettingsSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	list := d.list
	subID := d.submenuID
	subTitle := d.submenuTitle
	subList := d.submenuList
	d.mu.Unlock()

	var backClicked, doneClicked bool

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		if subID != "" && subList != nil {
			selRenderDialogHeader(c, th, "Settings · "+subTitle, "Choose a value to apply", func() {
				if ui.Button(c, "← Back").Clicked() {
					backClicked = true
				}
			})
			subList.View(c)
			selRenderDialogFooter(c, th, "Enter to apply · Esc to return", func() {
				if ui.Button(c, "Back").Clicked() {
					backClicked = true
				}
			})
		} else {
			selRenderDialogHeader(c, th, "Settings", "Changes apply immediately and persist when backed by SettingsManager", nil)
			if list != nil {
				list.View(c)
			}
			selRenderDialogFooter(c, th, "Click toggle or cycle enum · Esc to close", func() {
				if ui.Button(c, "Done").Clicked() {
					doneClicked = true
				}
			})
		}
	})

	if backClicked {
		d.closeSubmenu()
	} else if doneClicked {
		d.triggerCancel()
	}
	return root
}

// Show pushes SettingsSelectorDialog onto host as a modal overlay.
func (d *SettingsSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            680,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// ThemeSelectorDialog
// ============================================================================

// ThemeSelectorConfig configures a ThemeSelectorDialog (/theme).
type ThemeSelectorConfig struct {
	Themes       []theme.ThemeWithPath
	CurrentTheme string
	Host         *gui.GUI
	Theme        *gui.GuiTheme
	OnPreview    func(name string)
	OnSelect     func(name string)
	OnCancel     func()
}

// ThemeSelectorDialog provides live theme previewing on highlight/hover and reverts
// to the previous theme if cancelled.
type ThemeSelectorDialog struct {
	mu           sync.Mutex
	themes       []theme.ThemeWithPath
	currentTheme string
	prevInstance *theme.Theme
	host         *gui.GUI
	theme        *gui.GuiTheme
	list         *gui.SelectList
	committed    bool
	onPreview    func(name string)
	onSelect     func(name string)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewThemeSelectorDialog constructs a theme selector with live preview & revert.
func NewThemeSelectorDialog(cfg ThemeSelectorConfig) *ThemeSelectorDialog {
	themes := slices.Clone(cfg.Themes)
	if len(themes) == 0 {
		themes = theme.GetAvailableThemesWithPaths()
	}
	prevInst := theme.CurrentTheme()
	curName := strings.TrimSpace(cfg.CurrentTheme)
	if curName == "" {
		if prevInst != nil && prevInst.Name != "" {
			curName = prevInst.Name
		} else {
			curName = "dark"
		}
	}

	d := &ThemeSelectorDialog{
		themes:       themes,
		currentTheme: curName,
		prevInstance: prevInst,
		host:         cfg.Host,
		theme:        cfg.Theme,
		onPreview:    cfg.OnPreview,
		onSelect:     cfg.OnSelect,
		onCancel:     cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetMaxHeight(340)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnHighlight(func(item gui.SelectItem) {
		d.Highlight(item.Value)
	})
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.confirm(item.Value)
	})
	sl.SetOnCancel(func() {
		d.revertAndCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

func (d *ThemeSelectorDialog) rebuildItems() {
	d.mu.Lock()
	themes := slices.Clone(d.themes)
	cur := d.currentTheme
	list := d.list
	d.mu.Unlock()

	items := make([]gui.SelectItem, 0, len(themes))
	selectedIdx := 0
	for i, tw := range themes {
		desc := "Built-in theme"
		if tw.Name == theme.SystemThemeName {
			desc = "Follow OS dark/light appearance automatically"
		} else if tw.Path != "" {
			desc = utils.ShortenHomePath(tw.Path)
		}
		badge := ""
		label := tw.Name
		if tw.Name == cur {
			badge = "active"
			label = "✓ " + tw.Name
			selectedIdx = i
		}
		items = append(items, gui.SelectItem{
			Value:       tw.Name,
			Label:       label,
			Description: desc,
			Badge:       badge,
		})
	}
	if list != nil {
		list.SetItems(items)
		list.SetSelectedIndex(selectedIdx)
	}
}

// Highlight previews the named theme on the host immediately without committing it.
func (d *ThemeSelectorDialog) Highlight(name string) {
	if name == "" {
		return
	}
	d.mu.Lock()
	host := d.host
	onPreview := d.onPreview
	list := d.list
	d.mu.Unlock()

	res, ts := ApplyThemeToHost(host, name)
	if !res.Success {
		// Built-in or custom fallback if not found
		return
	}
	if ts != nil && ts.Gui != nil {
		d.mu.Lock()
		d.theme = ts.Gui
		d.mu.Unlock()
		if list != nil {
			list.SetTheme(ts.Gui)
		}
	}
	if onPreview != nil {
		onPreview(name)
	}
}

func (d *ThemeSelectorDialog) confirm(name string) {
	if name == "" {
		return
	}
	d.mu.Lock()
	host := d.host
	d.committed = true
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()

	ApplyThemeToHost(host, name)
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(name)
	}
}

func (d *ThemeSelectorDialog) revertIfUncommitted() {
	d.mu.Lock()
	committed := d.committed
	curName := d.currentTheme
	prevInst := d.prevInstance
	host := d.host
	d.mu.Unlock()

	if committed {
		return
	}
	if curName != "" {
		res, _ := ApplyThemeToHost(host, curName)
		if res.Success {
			return
		}
	}
	if prevInst != nil {
		ApplyThemeInstanceToHost(host, prevInst)
	}
}

func (d *ThemeSelectorDialog) revertAndCancel() {
	d.revertIfUncommitted()
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ThemeSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

// View renders ThemeSelectorDialog inside c.
func (d *ThemeSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	cur := d.currentTheme
	list := d.list
	d.mu.Unlock()

	var confirmClicked, cancelClicked bool

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Select Theme", "Initial: "+cur+" (live preview on selection move)", nil)
		if list != nil {
			list.View(c)
		}
		selRenderDialogFooter(c, th, "Enter to apply · Esc to revert and cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Apply Theme").Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.revertAndCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes ThemeSelectorDialog onto host as a modal overlay and binds host for live preview.
func (d *ThemeSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	d.mu.Lock()
	if host != nil {
		d.host = host
	}
	d.committed = false
	d.mu.Unlock()

	return selShowOverlay(host, gui.OverlayOptions{
		Width:            540,
		MaxWidthPercent:  90,
		MaxHeightPercent: 84,
	}, func() {
		d.revertIfUncommitted()
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// ScopedModelsSelectorDialog
// ============================================================================

// ScopedModelsSelectorConfig configures a ScopedModelsSelectorDialog (/scoped-models).
type ScopedModelsSelectorConfig struct {
	AllModels       []*ai.Model
	ScopedModels    []core.ScopedModel
	SettingsManager *core.SettingsManager
	Theme           *gui.GuiTheme
	OnSave          func(scoped []core.ScopedModel, persist bool)
	OnCancel        func()
}

type selScopedRow struct {
	key      string
	model    *ai.Model
	enabled  bool
	thinking agent.ThinkingLevel // empty means model/session default
}

// ScopedModelsSelectorDialog edits the Ctrl+P model cycling scope with enable/disable,
// filtered bulk toggle, reordering, thinking level overrides, and persistence checkbox.
type ScopedModelsSelectorDialog struct {
	mu              sync.Mutex
	rows            []*selScopedRow
	searchQuery     string
	persist         bool
	settingsManager *core.SettingsManager
	theme           *gui.GuiTheme
	onSave          func(scoped []core.ScopedModel, persist bool)
	onCancel        func()
	closeHandle     func()
	cancelHandle    func()
}

// NewScopedModelsSelectorDialog constructs a scoped-models selector overlay.
func NewScopedModelsSelectorDialog(cfg ScopedModelsSelectorConfig) *ScopedModelsSelectorDialog {
	d := &ScopedModelsSelectorDialog{
		settingsManager: cfg.SettingsManager,
		theme:           cfg.Theme,
		onSave:          cfg.OnSave,
		onCancel:        cfg.OnCancel,
	}
	d.SetModels(cfg.AllModels, cfg.ScopedModels)
	return d
}

// SetModels replaces the underlying model catalog and enabled scope order.
func (d *ScopedModelsSelectorDialog) SetModels(all []*ai.Model, scoped []core.ScopedModel) {
	d.mu.Lock()
	defer d.mu.Unlock()

	seen := make(map[string]bool)
	rows := make([]*selScopedRow, 0, len(all)+len(scoped))

	for _, sm := range scoped {
		if sm.Model == nil {
			continue
		}
		k := selModelKey(string(sm.Model.Provider), sm.Model.Id)
		if seen[k] {
			continue
		}
		seen[k] = true
		var tl agent.ThinkingLevel
		if sm.ThinkingLevel != nil {
			tl = *sm.ThinkingLevel
		}
		rows = append(rows, &selScopedRow{
			key:      k,
			model:    sm.Model,
			enabled:  true,
			thinking: tl,
		})
	}
	for _, m := range all {
		if m == nil {
			continue
		}
		k := selModelKey(string(m.Provider), m.Id)
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, &selScopedRow{
			key:     k,
			model:   m,
			enabled: false,
		})
	}
	d.rows = rows
}

// ScopedModels returns the currently enabled scoped models in cycle order.
func (d *ScopedModelsSelectorDialog) ScopedModels() []core.ScopedModel {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []core.ScopedModel
	for _, r := range d.rows {
		if !r.enabled || r.model == nil {
			continue
		}
		var tl *agent.ThinkingLevel
		if r.thinking != "" {
			cp := r.thinking
			tl = &cp
		}
		out = append(out, core.ScopedModel{
			Model:         r.model,
			ThinkingLevel: tl,
		})
	}
	return out
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ScopedModelsSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func selRowMatchesQuery(r *selScopedRow, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	name := strings.ToLower(r.model.Name)
	return strings.Contains(strings.ToLower(r.key), q) || strings.Contains(name, q)
}

func selCycleThinkingOverride(m *ai.Model, cur agent.ThinkingLevel) agent.ThinkingLevel {
	levels := ai.GetSupportedThinkingLevels(m)
	if cur == "" {
		if len(levels) > 0 {
			return levels[0]
		}
		return ai.ModelThinkingLevelOff
	}
	idx := slices.Index(levels, cur)
	if idx < 0 || idx+1 >= len(levels) {
		return ""
	}
	return levels[idx+1]
}

func (d *ScopedModelsSelectorDialog) save() {
	scoped := d.ScopedModels()
	d.mu.Lock()
	persist := d.persist
	sm := d.settingsManager
	onSave := d.onSave
	closeFn := d.closeHandle
	d.mu.Unlock()

	if persist && sm != nil {
		patterns := make([]string, 0, len(scoped))
		for _, sc := range scoped {
			if sc.Model == nil {
				continue
			}
			pat := selModelKey(string(sc.Model.Provider), sc.Model.Id)
			if sc.ThinkingLevel != nil && *sc.ThinkingLevel != "" {
				pat += ":" + string(*sc.ThinkingLevel)
			}
			patterns = append(patterns, pat)
		}
		sm.SetEnabledModels(patterns)
	}
	if closeFn != nil {
		closeFn()
	}
	if onSave != nil {
		onSave(scoped, persist)
	}
}

func (d *ScopedModelsSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders ScopedModelsSelectorDialog inside c.
func (d *ScopedModelsSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	rows := slices.Clone(d.rows)
	d.mu.Unlock()

	query := d.searchQuery
	var filteredIdx []int
	enabledCount := 0
	for i, r := range rows {
		if r.enabled {
			enabledCount++
		}
		if selRowMatchesQuery(r, query) {
			filteredIdx = append(filteredIdx, i)
		}
	}

	var (
		enableAllClicked bool
		clearAllClicked  bool
		toggleIdx        = -1
		toggleVal        bool
		moveUpIdx        = -1
		moveDownIdx      = -1
		cycleThinkIdx    = -1
		saveClicked      bool
		cancelClicked    bool
	)

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Scoped Models", fmt.Sprintf("%d enabled for cycling", enabledCount), func() {
			if ui.Button(c, "Enable All").Clicked() {
				enableAllClicked = true
			}
			if ui.Button(c, "Clear All").Clicked() {
				clearAllClicked = true
			}
		})

		ui.TextInput(c, &d.searchQuery).Placeholder("Filter models by provider or id...").FillWidth().AutoFocus()

		scroll := ui.Scroll(c).FillWidth().MaxHeight(320).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius - 2).Padding(6)
		scroll.Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, idx := range filteredIdx {
					i := idx
					row := rows[i]
					rowKey := row.key
					origEnabled := row.enabled
					checked := origEnabled
					rowThink := row.thinking
					rowReasoning := row.model.Reasoning
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Padding(4, 6).Children(func() {
						label := row.model.Name
						if label == "" {
							label = row.model.Id
						}
						ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Checkbox(c, &checked, label+" ("+rowKey+")")
						})
						if checked != origEnabled {
							toggleIdx = i
							toggleVal = checked
						}
						ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
							if rowReasoning && origEnabled {
								lbl := "default"
								if rowThink != "" {
									lbl = string(rowThink)
								}
								if ui.Button(c, "think: "+lbl).Clicked() {
									cycleThinkIdx = i
								}
							}
							if origEnabled {
								if i > 0 && ui.Button(c, "↑").Clicked() {
									moveUpIdx = i
								}
								if i+1 < len(rows) && ui.Button(c, "↓").Clicked() {
									moveDownIdx = i
								}
							}
						})
					})
				}
			})
		})

		selRenderDialogFooter(c, th, "Toggle models to include in Ctrl+P cycling", func() {
			ui.Checkbox(c, &d.persist, "Persist to settings")
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, fmt.Sprintf("Save (%d)", enabledCount)).Clicked() {
				saveClicked = true
			}
		})
	})

	switch {
	case toggleIdx >= 0 && toggleIdx < len(rows):
		d.mu.Lock()
		d.rows[toggleIdx].enabled = toggleVal
		d.mu.Unlock()
	case enableAllClicked:
		d.mu.Lock()
		for _, idx := range filteredIdx {
			d.rows[idx].enabled = true
		}
		d.mu.Unlock()
	case clearAllClicked:
		d.mu.Lock()
		for _, idx := range filteredIdx {
			d.rows[idx].enabled = false
		}
		d.mu.Unlock()
	case cycleThinkIdx >= 0 && cycleThinkIdx < len(rows):
		d.mu.Lock()
		r := d.rows[cycleThinkIdx]
		r.thinking = selCycleThinkingOverride(r.model, r.thinking)
		d.mu.Unlock()
	case moveUpIdx > 0 && moveUpIdx < len(rows):
		d.mu.Lock()
		d.rows[moveUpIdx-1], d.rows[moveUpIdx] = d.rows[moveUpIdx], d.rows[moveUpIdx-1]
		d.mu.Unlock()
	case moveDownIdx >= 0 && moveDownIdx+1 < len(rows):
		d.mu.Lock()
		d.rows[moveDownIdx], d.rows[moveDownIdx+1] = d.rows[moveDownIdx+1], d.rows[moveDownIdx]
		d.mu.Unlock()
	case saveClicked:
		d.save()
	case cancelClicked:
		d.triggerCancel()
	}

	return root
}

// Show pushes ScopedModelsSelectorDialog onto host as a modal overlay.
func (d *ScopedModelsSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            700,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// OAuthSelectorDialog & LoginDialog
// ============================================================================

// OAuthProviderEntry describes an OAuth provider option for /login or /logout.
type OAuthProviderEntry struct {
	ID         string
	Name       string
	LoggedIn   bool
	Credential string
}

// OAuthSelectorConfig configures an OAuthSelectorDialog.
type OAuthSelectorConfig struct {
	Mode      string // "login" (default) or "logout"
	Providers []OAuthProviderEntry
	Theme     *gui.GuiTheme
	OnSelect  func(provider OAuthProviderEntry)
	OnCancel  func()
}

// OAuthSelectorDialog lets the user choose an OAuth provider for login or logout.
type OAuthSelectorDialog struct {
	mu           sync.Mutex
	mode         string
	providers    []OAuthProviderEntry
	byID         map[string]OAuthProviderEntry
	theme        *gui.GuiTheme
	list         *gui.SelectList
	onSelect     func(provider OAuthProviderEntry)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewOAuthSelectorDialog constructs a provider selection dialog for /login or /logout.
func NewOAuthSelectorDialog(cfg OAuthSelectorConfig) *OAuthSelectorDialog {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "logout" {
		mode = "login"
	}
	d := &OAuthSelectorDialog{
		mode:      mode,
		providers: slices.Clone(cfg.Providers),
		byID:      make(map[string]OAuthProviderEntry),
		theme:     cfg.Theme,
		onSelect:  cfg.OnSelect,
		onCancel:  cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(len(cfg.Providers) > 6)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetPlaceholder("Filter providers...")
	sl.SetMaxHeight(320)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(item.Value)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

// SetProviders replaces the listed OAuth providers.
func (d *OAuthSelectorDialog) SetProviders(providers []OAuthProviderEntry) {
	d.mu.Lock()
	d.providers = slices.Clone(providers)
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetSearchable(len(providers) > 6)
	}
	d.rebuildItems()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *OAuthSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *OAuthSelectorDialog) rebuildItems() {
	d.mu.Lock()
	providers := slices.Clone(d.providers)
	list := d.list
	byID := make(map[string]OAuthProviderEntry, len(providers))
	items := make([]gui.SelectItem, 0, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
		badge := "not signed in"
		if p.LoggedIn {
			badge = "✓ logged in"
		}
		if p.Credential != "" {
			badge = p.Credential
		}
		lbl := p.Name
		if lbl == "" {
			lbl = p.ID
		}
		items = append(items, gui.SelectItem{
			Value:       p.ID,
			Label:       lbl,
			Description: p.ID,
			Badge:       badge,
		})
	}
	d.byID = byID
	d.mu.Unlock()
	if list != nil {
		list.SetItems(items)
	}
}

func (d *OAuthSelectorDialog) handleSelect(id string) {
	d.mu.Lock()
	entry, ok := d.byID[id]
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if !ok {
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(entry)
	}
}

func (d *OAuthSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders OAuthSelectorDialog inside c.
func (d *OAuthSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	mode := d.mode
	list := d.list
	d.mu.Unlock()

	title := "Sign In to Provider"
	subtitle := "Select an authentication provider"
	confirmText := "Sign In"
	if mode == "logout" {
		title = "Sign Out of Provider"
		subtitle = "Select a logged-in provider to clear saved credentials"
		confirmText = "Sign Out"
	}

	var confirmClicked, cancelClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, subtitle, nil)
		if list != nil {
			list.View(c)
		}
		selRenderDialogFooter(c, th, "Enter to select · Esc to cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, confirmText).Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes OAuthSelectorDialog onto host as a modal overlay.
func (d *OAuthSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            520,
		MaxWidthPercent:  90,
		MaxHeightPercent: 80,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// LoginDialogConfig configures a LoginDialog implementing ai.AuthInteraction.
type LoginDialogConfig struct {
	ProviderName string
	Host         *gui.GUI
	Theme        *gui.GuiTheme
	OnCancel     func()
}

type selLoginPromptReq struct {
	seq    uint64
	prompt ai.AuthPrompt
	respCh chan selLoginPromptResp
}

type selLoginPromptResp struct {
	value string
	err   error
}

type selLoginInfoEntry struct {
	message string
	links   []ai.AuthInfoLink
}

// LoginDialog implements ai.AuthInteraction for interactive desktop OAuth / device-code /
// API-key login flows, waking the UI thread cleanly when notifications or prompts arrive.
type LoginDialog struct {
	mu           sync.Mutex
	providerName string
	host         *gui.GUI
	theme        *gui.GuiTheme

	infoEntries  []selLoginInfoEntry
	authURL      string
	instructions string
	deviceCode   *ai.AuthEventDeviceCode
	progressMsg  string

	activePrompt *selLoginPromptReq
	nextSeq      uint64

	completed bool
	success   bool
	doneMsg   string
	cancelled bool
	cancelCh  chan struct{}

	// UI-thread-only buffer and synced prompt sequence.
	uiPromptSeq uint64
	uiInputBuf  string
	uiSelectIdx int
	copiedNote  string

	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

var _ ai.AuthInteraction = (*LoginDialog)(nil)

// NewLoginDialog constructs a LoginDialog implementing ai.AuthInteraction.
func NewLoginDialog(cfg LoginDialogConfig) *LoginDialog {
	return &LoginDialog{
		providerName: cfg.ProviderName,
		host:         cfg.Host,
		theme:        cfg.Theme,
		onCancel:     cfg.OnCancel,
		cancelCh:     make(chan struct{}),
	}
}

func (d *LoginDialog) wakeHost(host *gui.GUI) {
	if host != nil {
		host.RequestRender()
	}
}

// Notify handles AuthEvent updates (info links, auth URLs, device codes, progress).
func (d *LoginDialog) Notify(event ai.AuthEvent) {
	if event == nil {
		return
	}
	d.mu.Lock()
	host := d.host
	switch ev := event.(type) {
	case *ai.AuthEventInfo:
		if ev != nil {
			d.infoEntries = append(d.infoEntries, selLoginInfoEntry{
				message: ev.Message,
				links:   slices.Clone(ev.Links),
			})
		}
	case *ai.AuthEventAuthUrl:
		if ev != nil {
			d.authURL = ev.Url
			if ev.Instructions != nil {
				d.instructions = *ev.Instructions
			}
		}
	case *ai.AuthEventDeviceCode:
		if ev != nil {
			cp := *ev
			d.deviceCode = &cp
		}
	case *ai.AuthEventProgress:
		if ev != nil {
			d.progressMsg = ev.Message
		}
	}
	d.mu.Unlock()
	d.wakeHost(host)
}

// Prompt displays an AuthPrompt inside the dialog and waits until the user submits
// a response, the dialog is cancelled, or ctx is cancelled.
func (d *LoginDialog) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	if d.cancelled {
		d.mu.Unlock()
		return "", context.Canceled
	}
	d.nextSeq++
	req := &selLoginPromptReq{
		seq:    d.nextSeq,
		prompt: prompt,
		respCh: make(chan selLoginPromptResp, 1),
	}
	d.activePrompt = req
	host := d.host
	cancelCh := d.cancelCh
	d.mu.Unlock()

	d.wakeHost(host)

	select {
	case resp := <-req.respCh:
		return resp.value, resp.err
	case <-cancelCh:
		return "", context.Canceled
	case <-ctx.Done():
		d.mu.Lock()
		if d.activePrompt == req {
			d.activePrompt = nil
		}
		h := d.host
		d.mu.Unlock()
		d.wakeHost(h)
		return "", ctx.Err()
	}
}

func (d *LoginDialog) submitPrompt(seq uint64, val string) {
	d.mu.Lock()
	req := d.activePrompt
	if req == nil || req.seq != seq {
		d.mu.Unlock()
		return
	}
	d.activePrompt = nil
	host := d.host
	d.mu.Unlock()

	select {
	case req.respCh <- selLoginPromptResp{value: val}:
	default:
	}
	d.wakeHost(host)
}

// Complete finishes the login flow with a success or error state.
func (d *LoginDialog) Complete(success bool, message string) {
	d.mu.Lock()
	d.completed = true
	d.success = success
	d.doneMsg = message
	d.progressMsg = ""
	if d.activePrompt != nil {
		req := d.activePrompt
		d.activePrompt = nil
		select {
		case req.respCh <- selLoginPromptResp{err: context.Canceled}:
		default:
		}
	}
	host := d.host
	d.mu.Unlock()
	d.wakeHost(host)
}

// Cancel aborts any pending Prompt and closes the dialog overlay.
func (d *LoginDialog) Cancel() {
	d.mu.Lock()
	already := d.cancelled
	d.cancelled = true
	req := d.activePrompt
	d.activePrompt = nil
	if !already && d.cancelCh != nil {
		close(d.cancelCh)
	}
	cancelFn := d.cancelHandle
	closeFn := d.closeHandle
	onCancel := d.onCancel
	host := d.host
	d.mu.Unlock()

	if req != nil {
		select {
		case req.respCh <- selLoginPromptResp{err: context.Canceled}:
		default:
		}
	}
	d.wakeHost(host)

	if cancelFn != nil {
		cancelFn()
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if !already && onCancel != nil {
		onCancel()
	}
}

// SetTheme updates the GUI theme used by the dialog.
func (d *LoginDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

// View renders LoginDialog inside c.
func (d *LoginDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	providerName := d.providerName
	infos := slices.Clone(d.infoEntries)
	authURL := d.authURL
	instructions := d.instructions
	var devCode *ai.AuthEventDeviceCode
	if d.deviceCode != nil {
		cp := *d.deviceCode
		devCode = &cp
	}
	progressMsg := d.progressMsg
	req := d.activePrompt
	completed := d.completed
	success := d.success
	doneMsg := d.doneMsg
	closeFn := d.closeHandle
	d.mu.Unlock()

	if req != nil && d.uiPromptSeq != req.seq {
		d.uiPromptSeq = req.seq
		d.uiInputBuf = ""
		d.uiSelectIdx = 0
	}

	var (
		openURL        string
		copyText       string
		submitValue    *string
		cancelClicked  bool
		closeOnSuccess bool
	)

	title := "Sign In"
	if providerName != "" {
		title = "Sign In · " + providerName
	}

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, instructions, nil)

		if len(infos) > 0 {
			ui.Column(c).FillWidth().Gap(4).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius-2).Padding(8, 10).Children(func() {
				for _, entry := range infos {
					if entry.message != "" {
						ui.Text(c, entry.message).TextColor(th.Text).FontSize(12)
					}
					for _, lnk := range entry.links {
						lbl := lnk.Url
						if lnk.Label != nil && *lnk.Label != "" {
							lbl = *lnk.Label
						}
						target := lnk.Url
						if ui.Button(c, lbl).Clicked() {
							openURL = target
						}
					}
				}
			})
		}

		if devCode != nil {
			ui.Column(c).FillWidth().Gap(6).Background(th.Surface).Border(1, th.Accent).Radius(th.Radius-2).Padding(10, 12).Children(func() {
				ui.Text(c, "Verification Code").TextColor(th.Muted).FontSize(11).FontWeight(600)
				ui.Text(c, devCode.UserCode).TextColor(th.Accent).Font("monospace").FontSize(18).FontWeight(700)
				if devCode.VerificationUri != "" {
					ui.Text(c, devCode.VerificationUri).TextColor(th.Text).FontSize(11).SingleLine()
				}
				ui.Row(c).Gap(6).Children(func() {
					if ui.Button(c, "Copy Code").Clicked() {
						copyText = devCode.UserCode
					}
					if devCode.VerificationUri != "" && ui.Button(c, "Open Verification Page").Clicked() {
						openURL = devCode.VerificationUri
					}
				})
			})
		} else if authURL != "" {
			ui.Column(c).FillWidth().Gap(6).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius-2).Padding(10, 12).Children(func() {
				ui.Text(c, "Authorize in Browser").TextColor(th.Muted).FontSize(11).FontWeight(600)
				ui.Text(c, authURL).TextColor(th.MdLink).FontSize(11)
				ui.Row(c).Gap(6).Children(func() {
					if ui.Button(c, "Open Browser").Clicked() {
						openURL = authURL
					}
					if ui.Button(c, "Copy URL").Clicked() {
						copyText = authURL
					}
				})
			})
		}

		if d.copiedNote != "" {
			ui.Text(c, d.copiedNote).TextColor(th.Success).FontSize(11)
		}

		if completed {
			col := th.Success
			if !success {
				col = th.Error
			}
			msg := doneMsg
			if msg == "" {
				if success {
					msg = "Authentication complete."
				} else {
					msg = "Authentication failed."
				}
			}
			ui.Box(c).FillWidth().Background(th.Surface).Border(1, col).Radius(th.Radius-2).Padding(10, 12).Children(func() {
				ui.Text(c, msg).TextColor(col).FontSize(12).FontWeight(600)
			})
		} else if req != nil {
			ui.Column(c).FillWidth().Gap(8).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius-2).Padding(10, 12).Children(func() {
				switch p := req.prompt.(type) {
				case *ai.AuthPromptText:
					ui.Text(c, p.Message).TextColor(th.Text).FontSize(12).FontWeight(600)
					ph := ""
					if p.Placeholder != nil {
						ph = *p.Placeholder
					}
					in := ui.TextInput(c, &d.uiInputBuf).Placeholder(ph).FillWidth().AutoFocus()
					if in.Submitted() {
						v := d.uiInputBuf
						submitValue = &v
					}
				case *ai.AuthPromptSecret:
					ui.Text(c, p.Message).TextColor(th.Text).FontSize(12).FontWeight(600)
					ph := ""
					if p.Placeholder != nil {
						ph = *p.Placeholder
					}
					in := ui.TextInput(c, &d.uiInputBuf).Password().Placeholder(ph).FillWidth().AutoFocus()
					if in.Submitted() {
						v := d.uiInputBuf
						submitValue = &v
					}
				case *ai.AuthPromptManualCode:
					ui.Text(c, p.Message).TextColor(th.Text).FontSize(12).FontWeight(600)
					ph := "Paste authorization code or redirect URL..."
					if p.Placeholder != nil {
						ph = *p.Placeholder
					}
					in := ui.TextInput(c, &d.uiInputBuf).Placeholder(ph).FillWidth().AutoFocus()
					if in.Submitted() {
						v := d.uiInputBuf
						submitValue = &v
					}
				case *ai.AuthPromptSelect:
					ui.Text(c, p.Message).TextColor(th.Text).FontSize(12).FontWeight(600)
					for idx, opt := range p.Options {
						i := idx
						row := ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).
							Padding(6, 8).Radius(th.Radius - 3).Cursor(ui.CursorPointer)
						if d.uiSelectIdx == i {
							row.Background(th.SelectedBg).Border(1, th.Accent)
						} else {
							row.Border(1, th.BorderMuted)
						}
						row.Children(func() {
							ui.Column(c).Shrink(1).Grow(1).Gap(2).Children(func() {
								ui.Text(c, opt.Label).TextColor(th.Text).FontSize(12).FontWeight(600)
								if opt.Description != nil && *opt.Description != "" {
									ui.Text(c, *opt.Description).TextColor(th.Muted).FontSize(11)
								}
							})
						})
						if row.Clicked() {
							d.uiSelectIdx = i
						}
					}
				}
			})
		} else if progressMsg != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(6, 0).Children(func() {
				ui.Spinner(c).TextColor(th.Accent).Label(progressMsg)
				ui.Text(c, progressMsg).TextColor(th.Muted).FontSize(12)
			})
		}

		hints := "Esc to cancel"
		if completed {
			hints = "Close to continue"
		}
		selRenderDialogFooter(c, th, hints, func() {
			if completed {
				if ui.Button(c, "Close").Clicked() {
					closeOnSuccess = true
				}
				return
			}
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if req != nil && ui.Button(c, "Submit").Clicked() {
				if selP, ok := req.prompt.(*ai.AuthPromptSelect); ok && len(selP.Options) > 0 {
					idx := min(max(0, d.uiSelectIdx), len(selP.Options)-1)
					v := selP.Options[idx].Id
					submitValue = &v
				} else {
					v := d.uiInputBuf
					submitValue = &v
				}
			}
		})
	})

	if openURL != "" {
		c.OpenURL(openURL)
	}
	if copyText != "" {
		c.WriteClipboard(copyText)
		_ = utils.CopyToClipboard(copyText)
		d.copiedNote = "Copied to clipboard"
	}
	if submitValue != nil && req != nil {
		d.submitPrompt(req.seq, *submitValue)
	} else if closeOnSuccess {
		if closeFn != nil {
			closeFn()
		}
	} else if cancelClicked {
		d.Cancel()
	}

	return root
}

// Show pushes LoginDialog onto host as a modal overlay and binds host for async render wakes.
func (d *LoginDialog) Show(host *gui.GUI) *DialogHandle {
	d.mu.Lock()
	if host != nil {
		d.host = host
	}
	d.mu.Unlock()

	return selShowOverlay(host, gui.OverlayOptions{
		Width:            560,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		already := d.cancelled
		d.cancelled = true
		req := d.activePrompt
		d.activePrompt = nil
		if !already && d.cancelCh != nil {
			close(d.cancelCh)
		}
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if req != nil {
			select {
			case req.respCh <- selLoginPromptResp{err: context.Canceled}:
			default:
			}
		}
		if !already && fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// TrustSelectorDialog
// ============================================================================

// TrustSelectorConfig configures a TrustSelectorDialog for project configuration trust.
type TrustSelectorConfig struct {
	Cwd      string
	Title    string
	Message  string
	Options  []core.ProjectTrustOption
	Theme    *gui.GuiTheme
	OnSelect func(option core.ProjectTrustOption)
	OnCancel func()
}

// TrustSelectorDialog asks the user how to trust project-local pi settings/extensions.
type TrustSelectorDialog struct {
	mu           sync.Mutex
	cwd          string
	title        string
	message      string
	options      []core.ProjectTrustOption
	byIdx        map[string]core.ProjectTrustOption
	theme        *gui.GuiTheme
	list         *gui.SelectList
	onSelect     func(option core.ProjectTrustOption)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewTrustSelectorDialog constructs a project-trust confirmation modal.
func NewTrustSelectorDialog(cfg TrustSelectorConfig) *TrustSelectorDialog {
	title := cfg.Title
	if title == "" {
		title = "Project Trust"
	}
	msg := cfg.Message
	if msg == "" {
		msg = "This project defines workspace-local configuration. Choose whether to trust it:"
	}
	opts := slices.Clone(cfg.Options)
	if len(opts) == 0 {
		opts = []core.ProjectTrustOption{
			{Label: "Trust this project", Trusted: true},
			{Label: "Do not trust (use global settings only)", Trusted: false},
		}
	}
	d := &TrustSelectorDialog{
		cwd:      cfg.Cwd,
		title:    title,
		message:  msg,
		options:  opts,
		byIdx:    make(map[string]core.ProjectTrustOption),
		theme:    cfg.Theme,
		onSelect: cfg.OnSelect,
		onCancel: cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(false)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetMaxHeight(260)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(item.Value)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

func (d *TrustSelectorDialog) rebuildItems() {
	d.mu.Lock()
	opts := slices.Clone(d.options)
	list := d.list
	byIdx := make(map[string]core.ProjectTrustOption, len(opts))
	items := make([]gui.SelectItem, 0, len(opts))
	for i, opt := range opts {
		key := strconv.Itoa(i)
		byIdx[key] = opt
		badge := "untrusted"
		if opt.Trusted {
			badge = "trusted"
		}
		desc := ""
		if opt.SavedPath != nil && *opt.SavedPath != "" {
			desc = "Save preference to " + utils.ShortenHomePath(*opt.SavedPath)
		}
		items = append(items, gui.SelectItem{
			Value:       key,
			Label:       opt.Label,
			Description: desc,
			Badge:       badge,
		})
	}
	d.byIdx = byIdx
	d.mu.Unlock()
	if list != nil {
		list.SetItems(items)
	}
}

func (d *TrustSelectorDialog) handleSelect(key string) {
	d.mu.Lock()
	opt, ok := d.byIdx[key]
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if !ok {
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(opt)
	}
}

// SetTheme updates the GUI theme used by the dialog.
func (d *TrustSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *TrustSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders TrustSelectorDialog inside c.
func (d *TrustSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	msg := d.message
	cwd := d.cwd
	list := d.list
	d.mu.Unlock()

	sub := ""
	if cwd != "" {
		sub = utils.ShortenHomePath(cwd)
	}
	var confirmClicked, cancelClicked bool

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, sub, nil)
		if msg != "" {
			ui.Text(c, msg).TextColor(th.Muted).FontSize(12)
		}
		if list != nil {
			list.View(c)
		}
		selRenderDialogFooter(c, th, "Enter to confirm · Esc to cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Confirm").Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes TrustSelectorDialog onto host as a modal overlay.
func (d *TrustSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            560,
		MaxWidthPercent:  92,
		MaxHeightPercent: 80,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// ForkSelectorDialog
// ============================================================================

// ForkSelectorConfig configures a ForkSelectorDialog (/fork).
type ForkSelectorConfig struct {
	Messages []core.ForkableUserMessage
	Theme    *gui.GuiTheme
	OnSelect func(msg core.ForkableUserMessage)
	OnCancel func()
}

// ForkSelectorDialog lists past user turns so the user can fork a session from any turn.
type ForkSelectorDialog struct {
	mu           sync.Mutex
	messages     []core.ForkableUserMessage
	byId         map[string]core.ForkableUserMessage
	theme        *gui.GuiTheme
	list         *gui.SelectList
	onSelect     func(msg core.ForkableUserMessage)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewForkSelectorDialog constructs a /fork user-message picker modal.
func NewForkSelectorDialog(cfg ForkSelectorConfig) *ForkSelectorDialog {
	d := &ForkSelectorDialog{
		messages: slices.Clone(cfg.Messages),
		byId:     make(map[string]core.ForkableUserMessage),
		theme:    cfg.Theme,
		onSelect: cfg.OnSelect,
		onCancel: cfg.OnCancel,
	}

	sl := gui.NewSelectList(nil)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetPlaceholder("Search user messages to fork from...")
	sl.SetMaxHeight(340)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.handleSelect(item.Value)
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	d.rebuildItems()
	return d
}

// SetMessages replaces the forkable user messages shown by the dialog.
func (d *ForkSelectorDialog) SetMessages(msgs []core.ForkableUserMessage) {
	d.mu.Lock()
	d.messages = slices.Clone(msgs)
	d.mu.Unlock()
	d.rebuildItems()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ForkSelectorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *ForkSelectorDialog) rebuildItems() {
	d.mu.Lock()
	msgs := slices.Clone(d.messages)
	list := d.list
	byId := make(map[string]core.ForkableUserMessage, len(msgs))
	items := make([]gui.SelectItem, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		byId[m.EntryId] = m
		shortId := m.EntryId
		if len(shortId) > 8 {
			shortId = shortId[:8]
		}
		items = append(items, gui.SelectItem{
			Value:       m.EntryId,
			Label:       selTruncateSingleLine(m.Text, 88),
			Description: fmt.Sprintf("Turn #%d · entry %s", i+1, shortId),
			Badge:       fmt.Sprintf("#%d", i+1),
		})
	}
	d.byId = byId
	d.mu.Unlock()
	if list != nil {
		list.SetItems(items)
	}
}

func (d *ForkSelectorDialog) handleSelect(entryId string) {
	d.mu.Lock()
	m, ok := d.byId[entryId]
	onSel := d.onSelect
	closeFn := d.closeHandle
	d.mu.Unlock()
	if !ok {
		return
	}
	if closeFn != nil {
		closeFn()
	}
	if onSel != nil {
		onSel(m)
	}
}

func (d *ForkSelectorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders ForkSelectorDialog inside c.
func (d *ForkSelectorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	count := len(d.messages)
	list := d.list
	d.mu.Unlock()

	var confirmClicked, cancelClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, "Fork Session", fmt.Sprintf("Choose one of %d user messages to branch from", count), nil)
		if list != nil {
			list.View(c)
		}
		selRenderDialogFooter(c, th, "Enter to fork · Esc to cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Fork").Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes ForkSelectorDialog onto host as a modal overlay.
func (d *ForkSelectorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            640,
		MaxWidthPercent:  92,
		MaxHeightPercent: 84,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// ExtensionUIContext Modals: Select, Confirm, Input, Editor
// ============================================================================

// ExtensionSelectConfig configures ExtensionSelectDialog.
type ExtensionSelectConfig struct {
	Title        string
	Subtitle     string
	Options      []string
	InitialIndex int
	Theme        *gui.GuiTheme
	OnSelect     func(choice string)
	OnCancel     func()
}

// ExtensionSelectDialog implements ExtensionUIContext.Select in native GUI overlays.
type ExtensionSelectDialog struct {
	mu           sync.Mutex
	title        string
	subtitle     string
	theme        *gui.GuiTheme
	list         *gui.SelectList
	onSelect     func(choice string)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewExtensionSelectDialog constructs an extension option picker modal.
func NewExtensionSelectDialog(cfg ExtensionSelectConfig) *ExtensionSelectDialog {
	title := cfg.Title
	if title == "" {
		title = "Select Option"
	}
	d := &ExtensionSelectDialog{
		title:    title,
		subtitle: cfg.Subtitle,
		theme:    cfg.Theme,
		onSelect: cfg.OnSelect,
		onCancel: cfg.OnCancel,
	}

	items := make([]gui.SelectItem, 0, len(cfg.Options))
	for _, opt := range cfg.Options {
		items = append(items, gui.SelectItem{Value: opt, Label: opt})
	}
	sl := gui.NewSelectList(items)
	sl.SetSearchable(len(items) > 8)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetMaxHeight(340)
	if cfg.InitialIndex >= 0 && cfg.InitialIndex < len(items) {
		sl.SetSelectedIndex(cfg.InitialIndex)
	}
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		d.mu.Lock()
		onSel := d.onSelect
		closeFn := d.closeHandle
		d.mu.Unlock()
		if closeFn != nil {
			closeFn()
		}
		if onSel != nil {
			onSel(item.Value)
		}
	})
	sl.SetOnCancel(func() {
		d.triggerCancel()
	})
	d.list = sl
	return d
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ExtensionSelectDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(th)
	}
}

func (d *ExtensionSelectDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders ExtensionSelectDialog inside c.
func (d *ExtensionSelectDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	sub := d.subtitle
	list := d.list
	d.mu.Unlock()

	var confirmClicked, cancelClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, sub, nil)
		if list != nil {
			list.View(c)
		}
		selRenderDialogFooter(c, th, "Enter to select · Esc to cancel", func() {
			if ui.Button(c, "Cancel").Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, "Select").Clicked() {
				confirmClicked = true
			}
		})
	})

	if cancelClicked {
		d.triggerCancel()
	} else if confirmClicked && list != nil {
		list.Confirm()
	}
	return root
}

// Show pushes ExtensionSelectDialog onto host as a modal overlay.
func (d *ExtensionSelectDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            520,
		MaxWidthPercent:  90,
		MaxHeightPercent: 82,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ExtensionConfirmConfig configures ExtensionConfirmDialog.
type ExtensionConfirmConfig struct {
	Title        string
	Message      string
	ConfirmLabel string
	CancelLabel  string
	Destructive  bool
	Theme        *gui.GuiTheme
	OnConfirm    func(confirmed bool)
}

// ExtensionConfirmDialog implements ExtensionUIContext.Confirm in native GUI overlays.
type ExtensionConfirmDialog struct {
	mu           sync.Mutex
	title        string
	message      string
	confirmLabel string
	cancelLabel  string
	destructive  bool
	theme        *gui.GuiTheme
	onConfirm    func(confirmed bool)
	answered     bool
	closeHandle  func()
	cancelHandle func()
}

// NewExtensionConfirmDialog constructs a confirmation dialog.
func NewExtensionConfirmDialog(cfg ExtensionConfirmConfig) *ExtensionConfirmDialog {
	title := cfg.Title
	if title == "" {
		title = "Confirm"
	}
	confirmLbl := cfg.ConfirmLabel
	if confirmLbl == "" {
		confirmLbl = "Confirm"
	}
	cancelLbl := cfg.CancelLabel
	if cancelLbl == "" {
		cancelLbl = "Cancel"
	}
	return &ExtensionConfirmDialog{
		title:        title,
		message:      cfg.Message,
		confirmLabel: confirmLbl,
		cancelLabel:  cancelLbl,
		destructive:  cfg.Destructive,
		theme:        cfg.Theme,
		onConfirm:    cfg.OnConfirm,
	}
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ExtensionConfirmDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func (d *ExtensionConfirmDialog) answer(confirmed bool) {
	d.mu.Lock()
	if d.answered {
		d.mu.Unlock()
		return
	}
	d.answered = true
	cb := d.onConfirm
	closeFn := d.closeHandle
	cancelFn := d.cancelHandle
	d.mu.Unlock()

	if confirmed && closeFn != nil {
		closeFn()
	} else if !confirmed && cancelFn != nil {
		cancelFn()
	}
	if cb != nil {
		cb(confirmed)
	}
}

// View renders ExtensionConfirmDialog inside c.
func (d *ExtensionConfirmDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	msg := d.message
	confirmLbl := d.confirmLabel
	cancelLbl := d.cancelLabel
	destructive := d.destructive
	d.mu.Unlock()

	var yesClicked, noClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, "", nil)
		if msg != "" {
			col := th.Text
			if destructive {
				col = th.Warning
			}
			ui.Text(c, msg).TextColor(col).FontSize(13)
		}
		selRenderDialogFooter(c, th, "Esc to cancel", func() {
			if ui.Button(c, cancelLbl).Clicked() {
				noClicked = true
			}
			if ui.Button(c, confirmLbl).Clicked() {
				yesClicked = true
			}
		})
	})

	if yesClicked {
		d.answer(true)
	} else if noClicked {
		d.answer(false)
	}
	return root
}

// Show pushes ExtensionConfirmDialog onto host as a modal overlay.
func (d *ExtensionConfirmDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            460,
		MaxWidthPercent:  90,
		MaxHeightPercent: 70,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		d.mu.Unlock()
		d.answer(false)
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ExtensionInputConfig configures ExtensionInputDialog.
type ExtensionInputConfig struct {
	Title        string
	Message      string
	Placeholder  string
	InitialValue string
	ConfirmLabel string
	CancelLabel  string
	Password     bool
	Theme        *gui.GuiTheme
	OnSubmit     func(value string)
	OnCancel     func()
}

// ExtensionInputDialog implements ExtensionUIContext.Input in native GUI overlays.
type ExtensionInputDialog struct {
	mu           sync.Mutex
	title        string
	message      string
	placeholder  string
	confirmLabel string
	cancelLabel  string
	password     bool
	value        string
	valueVersion uint64
	uiSeenVer    uint64
	uiBuf        string
	theme        *gui.GuiTheme
	onSubmit     func(value string)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewExtensionInputDialog constructs a single-line extension text/secret prompt modal.
func NewExtensionInputDialog(cfg ExtensionInputConfig) *ExtensionInputDialog {
	title := cfg.Title
	if title == "" {
		title = "Input"
	}
	confirmLbl := cfg.ConfirmLabel
	if confirmLbl == "" {
		confirmLbl = "Submit"
	}
	cancelLbl := cfg.CancelLabel
	if cancelLbl == "" {
		cancelLbl = "Cancel"
	}
	return &ExtensionInputDialog{
		title:        title,
		message:      cfg.Message,
		placeholder:  cfg.Placeholder,
		confirmLabel: confirmLbl,
		cancelLabel:  cancelLbl,
		password:     cfg.Password,
		value:        cfg.InitialValue,
		valueVersion: 1,
		theme:        cfg.Theme,
		onSubmit:     cfg.OnSubmit,
		onCancel:     cfg.OnCancel,
	}
}

// Value returns the current input text safely.
func (d *ExtensionInputDialog) Value() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.value
}

// SetValue updates the input text programmatically.
func (d *ExtensionInputDialog) SetValue(v string) {
	d.mu.Lock()
	d.value = v
	d.valueVersion++
	d.mu.Unlock()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ExtensionInputDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func (d *ExtensionInputDialog) submit(val string) {
	d.mu.Lock()
	d.value = val
	onSub := d.onSubmit
	closeFn := d.closeHandle
	d.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	if onSub != nil {
		onSub(val)
	}
}

func (d *ExtensionInputDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders ExtensionInputDialog inside c.
func (d *ExtensionInputDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	msg := d.message
	ph := d.placeholder
	confirmLbl := d.confirmLabel
	cancelLbl := d.cancelLabel
	password := d.password
	ver := d.valueVersion
	valSnap := d.value
	d.mu.Unlock()

	if d.uiSeenVer != ver {
		d.uiBuf = valSnap
		d.uiSeenVer = ver
	}

	var submitClicked, cancelClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, msg, nil)
		in := ui.TextInput(c, &d.uiBuf).Placeholder(ph).FillWidth().AutoFocus()
		if password {
			in.Password()
		}
		if in.Submitted() {
			submitClicked = true
		}
		selRenderDialogFooter(c, th, "Enter to submit · Esc to cancel", func() {
			if ui.Button(c, cancelLbl).Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, confirmLbl).Clicked() {
				submitClicked = true
			}
		})
	})

	if d.uiBuf != valSnap {
		d.mu.Lock()
		d.value = d.uiBuf
		d.valueVersion++
		d.uiSeenVer = d.valueVersion
		d.mu.Unlock()
	}

	if submitClicked {
		d.submit(d.uiBuf)
	} else if cancelClicked {
		d.triggerCancel()
	}
	return root
}

// Show pushes ExtensionInputDialog onto host as a modal overlay.
func (d *ExtensionInputDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            480,
		MaxWidthPercent:  90,
		MaxHeightPercent: 72,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ExtensionEditorConfig configures ExtensionEditorDialog.
type ExtensionEditorConfig struct {
	Title       string
	Description string
	InitialText string
	SubmitLabel string
	CancelLabel string
	Theme       *gui.GuiTheme
	OnSubmit    func(text string)
	OnCancel    func()
}

// ExtensionEditorDialog implements ExtensionUIContext.Editor as a multi-line modal editor.
type ExtensionEditorDialog struct {
	mu           sync.Mutex
	title        string
	description  string
	submitLabel  string
	cancelLabel  string
	text         string
	textVersion  uint64
	uiSeenVer    uint64
	uiBuf        string
	theme        *gui.GuiTheme
	onSubmit     func(text string)
	onCancel     func()
	closeHandle  func()
	cancelHandle func()
}

// NewExtensionEditorDialog constructs a multi-line extension editor modal.
func NewExtensionEditorDialog(cfg ExtensionEditorConfig) *ExtensionEditorDialog {
	title := cfg.Title
	if title == "" {
		title = "Edit Text"
	}
	subLbl := cfg.SubmitLabel
	if subLbl == "" {
		subLbl = "Save"
	}
	cancelLbl := cfg.CancelLabel
	if cancelLbl == "" {
		cancelLbl = "Cancel"
	}
	return &ExtensionEditorDialog{
		title:       title,
		description: cfg.Description,
		submitLabel: subLbl,
		cancelLabel: cancelLbl,
		text:        cfg.InitialText,
		textVersion: 1,
		theme:       cfg.Theme,
		onSubmit:    cfg.OnSubmit,
		onCancel:    cfg.OnCancel,
	}
}

// Text returns the current multi-line buffer contents safely.
func (d *ExtensionEditorDialog) Text() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.text
}

// SetText updates the multi-line buffer contents programmatically.
func (d *ExtensionEditorDialog) SetText(s string) {
	d.mu.Lock()
	d.text = s
	d.textVersion++
	d.mu.Unlock()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *ExtensionEditorDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func (d *ExtensionEditorDialog) submit(val string) {
	d.mu.Lock()
	d.text = val
	onSub := d.onSubmit
	closeFn := d.closeHandle
	d.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	if onSub != nil {
		onSub(val)
	}
}

func (d *ExtensionEditorDialog) triggerCancel() {
	d.mu.Lock()
	cancelFn := d.cancelHandle
	onCancel := d.onCancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return
	}
	if onCancel != nil {
		onCancel()
	}
}

// View renders ExtensionEditorDialog inside c.
func (d *ExtensionEditorDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	desc := d.description
	subLbl := d.submitLabel
	cancelLbl := d.cancelLabel
	ver := d.textVersion
	textSnap := d.text
	d.mu.Unlock()

	if d.uiSeenVer != ver {
		d.uiBuf = textSnap
		d.uiSeenVer = ver
	}

	var submitClicked, cancelClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, desc, nil)
		ui.TextArea(c, &d.uiBuf).FillWidth().Height(220).AutoFocus().Label(title)
		selRenderDialogFooter(c, th, "Save to confirm · Esc to cancel", func() {
			if ui.Button(c, cancelLbl).Clicked() {
				cancelClicked = true
			}
			if ui.Button(c, subLbl).Clicked() {
				submitClicked = true
			}
		})
	})

	if d.uiBuf != textSnap {
		d.mu.Lock()
		d.text = d.uiBuf
		d.textVersion++
		d.uiSeenVer = d.textVersion
		d.mu.Unlock()
	}

	if submitClicked {
		d.submit(d.uiBuf)
	} else if cancelClicked {
		d.triggerCancel()
	}
	return root
}

// Show pushes ExtensionEditorDialog onto host as a modal overlay.
func (d *ExtensionEditorDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            640,
		MaxWidthPercent:  92,
		MaxHeightPercent: 86,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		d.cancelHandle = nil
		fn := d.onCancel
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ============================================================================
// HotkeysDialog
// ============================================================================

// HotkeyEntry describes one keyboard shortcut shown in HotkeysDialog.
type HotkeyEntry struct {
	Category    string
	Keys        string
	Description string
}

// DefaultHotkeys returns the standard GUI mode keyboard shortcut reference entries.
func DefaultHotkeys() []HotkeyEntry {
	return []HotkeyEntry{
		{Category: "Composer", Keys: "Enter", Description: "Send prompt (or queue steering when busy)"},
		{Category: "Composer", Keys: "Shift+Enter", Description: "Insert newline in prompt"},
		{Category: "Composer", Keys: "Alt+Enter", Description: "Queue follow-up message after current turn"},
		{Category: "Composer", Keys: "Esc", Description: "Abort streaming run / clear composer / close dialog"},
		{Category: "Models & Thinking", Keys: "Ctrl+P", Description: "Cycle through scoped models"},
		{Category: "Models & Thinking", Keys: "Shift+Tab", Description: "Cycle thinking / reasoning level"},
		{Category: "Models & Thinking", Keys: "Ctrl+L", Description: "Open model selector"},
		{Category: "Navigation & View", Keys: "Ctrl+T", Description: "Toggle thinking blocks visibility"},
		{Category: "Navigation & View", Keys: "Ctrl+O", Description: "Expand / collapse tool execution outputs"},
		{Category: "Navigation & View", Keys: "Ctrl+R", Description: "Open session resume selector"},
		{Category: "Navigation & View", Keys: "Ctrl+G", Description: "Open external editor ($VISUAL / $EDITOR)"},
	}
}

// HotkeysDialogConfig configures HotkeysDialog.
type HotkeysDialogConfig struct {
	Title   string
	Entries []HotkeyEntry
	Theme   *gui.GuiTheme
	OnClose func()
}

// HotkeysDialog renders grouped keyboard shortcut reference cards inside a modal overlay.
type HotkeysDialog struct {
	mu          sync.Mutex
	title       string
	entries     []HotkeyEntry
	theme       *gui.GuiTheme
	onClose     func()
	closeHandle func()
}

// NewHotkeysDialog constructs a keyboard shortcut reference dialog.
func NewHotkeysDialog(cfg HotkeysDialogConfig) *HotkeysDialog {
	title := cfg.Title
	if title == "" {
		title = "Keyboard Shortcuts"
	}
	entries := slices.Clone(cfg.Entries)
	if len(entries) == 0 {
		entries = DefaultHotkeys()
	}
	return &HotkeysDialog{
		title:   title,
		entries: entries,
		theme:   cfg.Theme,
		onClose: cfg.OnClose,
	}
}

// SetEntries replaces the displayed shortcut list.
func (d *HotkeysDialog) SetEntries(entries []HotkeyEntry) {
	d.mu.Lock()
	if len(entries) == 0 {
		entries = DefaultHotkeys()
	}
	d.entries = slices.Clone(entries)
	d.mu.Unlock()
}

// SetTheme updates the GUI theme used by the dialog.
func (d *HotkeysDialog) SetTheme(th *gui.GuiTheme) {
	d.mu.Lock()
	d.theme = th
	d.mu.Unlock()
}

func (d *HotkeysDialog) close() {
	d.mu.Lock()
	closeFn := d.closeHandle
	onClose := d.onClose
	d.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	if onClose != nil {
		onClose()
	}
}

// View renders HotkeysDialog inside c.
func (d *HotkeysDialog) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	th := gui.ResolveGuiTheme(c, d.theme)
	title := d.title
	entries := slices.Clone(d.entries)
	d.mu.Unlock()

	grouped := make(map[string][]HotkeyEntry)
	var categories []string
	for _, e := range entries {
		cat := e.Category
		if cat == "" {
			cat = "General"
		}
		if _, exists := grouped[cat]; !exists {
			categories = append(categories, cat)
		}
		grouped[cat] = append(grouped[cat], e)
	}
	if len(categories) > 1 {
		// Preserve insertion order except move empty/General last if present.
		sort.SliceStable(categories, func(i, j int) bool {
			return categories[i] != "General" && categories[j] == "General"
		})
	}

	var closeClicked bool
	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, fmt.Sprintf("%d shortcuts", len(entries)), nil)
		scroll := ui.Scroll(c).FillWidth().MaxHeight(360).Background(th.Surface).Border(1, th.BorderMuted).Radius(th.Radius - 2).Padding(10)
		scroll.Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				for _, cat := range categories {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						ui.Text(c, cat).TextColor(th.Accent).FontSize(12).FontWeight(700)
						for _, e := range grouped[cat] {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(10).Padding(2, 0).Children(func() {
								ui.Text(c, e.Description).TextColor(th.Text).FontSize(12)
								ui.Badge(c, e.Keys)
							})
						}
					})
				}
			})
		})
		selRenderDialogFooter(c, th, "Esc to close", func() {
			if ui.Button(c, "Close").Clicked() {
				closeClicked = true
			}
		})
	})

	if closeClicked {
		d.close()
	}
	return root
}

// Show pushes HotkeysDialog onto host as a modal overlay.
func (d *HotkeysDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            580,
		MaxWidthPercent:  90,
		MaxHeightPercent: 84,
	}, func() {
		d.mu.Lock()
		d.closeHandle = nil
		fn := d.onClose
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), _ func()) {
		d.mu.Lock()
		d.closeHandle = confirmDone
		d.mu.Unlock()
		d.View(c)
	})
}

// CommandPaletteEntry represents a single executable action in the Cmd+K palette.
type CommandPaletteEntry struct {
	ID          string
	Label       string
	Description string
	Badge       string
	OnRun       func()
}

// CommandPaletteConfig configures CommandPaletteDialog.
type CommandPaletteConfig struct {
	Title   string
	Entries []CommandPaletteEntry
	Theme   *gui.GuiTheme
	OnClose func()
}

// CommandPaletteDialog displays a Waku-styled Cmd+K quick command & action palette.
type CommandPaletteDialog struct {
	cfg     CommandPaletteConfig
	entries map[string]CommandPaletteEntry
	list    *gui.SelectList

	mu            sync.Mutex
	confirmHandle func()
	cancelHandle  func()
}

// NewCommandPaletteDialog constructs a searchable CommandPaletteDialog.
func NewCommandPaletteDialog(cfg CommandPaletteConfig) *CommandPaletteDialog {
	d := &CommandPaletteDialog{
		cfg:     cfg,
		entries: make(map[string]CommandPaletteEntry, len(cfg.Entries)),
	}
	items := make([]gui.SelectItem, 0, len(cfg.Entries))
	for _, entry := range cfg.Entries {
		if entry.ID == "" {
			continue
		}
		d.entries[entry.ID] = entry
		items = append(items, gui.SelectItem{
			Value:       entry.ID,
			Label:       entry.Label,
			Description: entry.Description,
			Badge:       entry.Badge,
		})
	}

	sl := gui.NewSelectList(items)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetPlaceholder("Type a command or search...")
	sl.SetMaxHeight(360)
	if cfg.Theme != nil {
		sl.SetTheme(cfg.Theme)
	}
	sl.SetOnSelect(func(item gui.SelectItem) {
		entry, ok := d.entries[item.Value]
		d.mu.Lock()
		confirmFn := d.confirmHandle
		d.mu.Unlock()
		if confirmFn != nil {
			confirmFn()
		}
		if ok && entry.OnRun != nil {
			entry.OnRun()
		}
	})
	sl.SetOnCancel(func() {
		d.mu.Lock()
		cancelFn := d.cancelHandle
		d.mu.Unlock()
		if cancelFn != nil {
			cancelFn()
		}
	})
	d.list = sl
	return d
}

// View renders the command palette modal contents inside c.
func (d *CommandPaletteDialog) View(c *ui.Context) ui.Element {
	th := gui.ResolveGuiTheme(c, d.cfg.Theme)
	title := strings.TrimSpace(d.cfg.Title)
	if title == "" {
		title = "Command Palette"
	}

	root := ui.Column(c).FillWidth().Gap(10)
	root.Children(func() {
		selRenderDialogHeader(c, th, title, "Quick actions & slash commands", nil)
		if d.list != nil {
			d.list.View(c)
		}
		selRenderDialogFooter(c, th, "↑/↓ navigate · Enter run · Esc close", nil)
	})
	return root
}

// Show pushes CommandPaletteDialog onto host as a modal overlay.
func (d *CommandPaletteDialog) Show(host *gui.GUI) *DialogHandle {
	return selShowOverlay(host, gui.OverlayOptions{
		Width:            560,
		MaxWidthPercent:  90,
		MaxHeightPercent: 84,
	}, func() {
		d.mu.Lock()
		d.confirmHandle = nil
		d.cancelHandle = nil
		fn := d.cfg.OnClose
		d.mu.Unlock()
		if fn != nil {
			fn()
		}
	}, func(c *ui.Context, confirmDone func(), cancel func()) {
		d.mu.Lock()
		d.confirmHandle = confirmDone
		d.cancelHandle = cancel
		d.mu.Unlock()
		d.View(c)
	})
}

// ShowCommandPalette opens the Waku Cmd+K command palette aggregating panel, session,
// appearance, and slash command actions.
func (m *GuiMode) ShowCommandPalette() {
	if m == nil || m.host == nil {
		return
	}
	entries := []CommandPaletteEntry{
		{
			ID:          "action:new-task",
			Label:       "New Task",
			Description: "Start a fresh agent session",
			Badge:       "⌘N",
			OnRun:       func() { go m.newSession(m.currentCtx()) },
		},
		{
			ID:          "action:toggle-sidebar",
			Label:       "Toggle Left Sidebar",
			Description: "Show or hide the session history sidebar",
			Badge:       "⌘B",
			OnRun:       func() { m.toggleLeftSidebar() },
		},
		{
			ID:          "action:toggle-right-panel",
			Label:       "Toggle Right Panel (Changes)",
			Description: "Show or hide workspace git diffs and changes",
			Badge:       "⌘J",
			OnRun:       func() { m.toggleRightPanel() },
		},
		{
			ID:          "action:session-tree-panel",
			Label:       "Session Tree Panel",
			Description: "Open the interactive session tree in the right panel",
			Badge:       "Panel",
			OnRun: func() {
				m.ensureWakuState()
				m.rightPanel.mu.Lock()
				m.rightPanel.open = true
				m.rightPanel.activeTab = "tree"
				m.rightPanel.mu.Unlock()
				m.saveLayoutPrefs()
				if m.host != nil {
					m.host.RequestRender()
				}
			},
		},
		{
			ID:          "action:select-model",
			Label:       "Select Model…",
			Description: "Switch the active provider and model",
			Badge:       "/model",
			OnRun:       func() { m.ShowModelSelector() },
		},
		{
			ID:          "action:select-thinking",
			Label:       "Select Thinking Level…",
			Description: "Adjust reasoning budget for responses",
			Badge:       "/thinking",
			OnRun:       func() { m.ShowThinkingSelector() },
		},
		{
			ID:          "action:switch-theme",
			Label:       "Switch Theme…",
			Description: "Choose a color theme preset",
			Badge:       "/theme",
			OnRun:       func() { m.ShowThemeSelector() },
		},
		{
			ID:          "action:toggle-appearance",
			Label:       "Toggle Dark / Light Appearance",
			Description: "Switch between Waku Dark and Waku Light",
			Badge:       "Theme",
			OnRun:       func() { m.toggleQuickThemeDarkLight() },
		},
		{
			ID:          "action:open-settings",
			Label:       "Open Settings…",
			Description: "Configure pi runtime and GUI preferences",
			Badge:       "/settings",
			OnRun:       func() { m.ShowSettingsSelector() },
		},
		{
			ID:          "action:navigate-tree",
			Label:       "Navigate Session Tree…",
			Description: "Jump to any branch or turn in the session",
			Badge:       "/tree",
			OnRun:       func() { m.ShowTreeSelector() },
		},
		{
			ID:          "action:fork-session",
			Label:       "Fork Session…",
			Description: "Branch a new session from an earlier message",
			Badge:       "/fork",
			OnRun:       func() { m.ShowForkSelector() },
		},
		{
			ID:          "action:resume-session",
			Label:       "Resume Session…",
			Description: "Browse and load saved sessions",
			Badge:       "/resume",
			OnRun:       func() { m.ShowSessionSelector() },
		},
		{
			ID:          "action:compact-context",
			Label:       "Compact Context",
			Description: "Summarize older turns to free context window space",
			Badge:       "/compact",
			OnRun:       func() { go m.runCompactCommand(m.currentCtx(), "") },
		},
		{
			ID:          "action:copy-last-reply",
			Label:       "Copy Last Reply",
			Description: "Copy the most recent assistant message to clipboard",
			Badge:       "/copy",
			OnRun:       func() { m.copyLastAssistantMessage() },
		},
		{
			ID:          "action:hotkeys",
			Label:       "Keyboard Shortcuts",
			Description: "View available keyboard shortcuts",
			Badge:       "/hotkeys",
			OnRun:       func() { m.ShowHotkeysDialog() },
		},
	}

	for _, cmd := range m.buildSlashCommandList() {
		cmdName := strings.TrimSpace(cmd.Name)
		if cmdName == "" {
			continue
		}
		entries = append(entries, CommandPaletteEntry{
			ID:          "slash:" + cmdName,
			Label:       "/" + cmdName,
			Description: cmd.Description,
			Badge:       "Command",
			OnRun: func() {
				m.HandleSlashCommand(m.currentCtx(), "/"+cmdName)
			},
		})
	}

	dlg := NewCommandPaletteDialog(CommandPaletteConfig{
		Title:   "Command Palette",
		Entries: entries,
		Theme:   m.currentGuiTheme(),
	})
	m.setActiveDialog(dlg.Show(m.host))
}
