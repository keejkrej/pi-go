// Ported from packages/coding-agent/src/modes/interactive/components/model-selector.ts (pi v1.5.2).

package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

const (
	mseMaxVisible       = 10
	mseRefreshTimeoutMs = 15 * time.Second
)

// DefaultModelRef identifies the configured default model by provider and ID.
type DefaultModelRef struct {
	Provider string
	Id       string
}

type mseModelScope string

const (
	mseScopeAll    mseModelScope = "all"
	mseScopeScoped mseModelScope = "scoped"
)

type mseModelItem struct {
	provider string
	id       string
	model    *ai.Model
}

type mseModelRuntime interface {
	ModelCatalogRuntime
	GetAvailableSnapshot() []*ai.Model
	GetModel(provider, id string) *ai.Model
	GetError() *string
}

func mseNonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mseStrPtr(s string) *string {
	return &s
}

// ModelSelectorComponent renders a searchable model picker with scope toggle and background catalog refresh.
type ModelSelectorComponent struct {
	*tui.Container
	mu                        sync.Mutex
	searchInput               *tui.Input
	listContainer             *tui.Container
	allModels                 []mseModelItem
	scopedModelItems          []mseModelItem
	activeModels              []mseModelItem
	filteredModels            []mseModelItem
	selectedIndex             int
	currentModel              *ai.Model
	modelRuntime              mseModelRuntime
	onSelectCallback          func(model *ai.Model)
	onSelectAsDefaultCallback func(model *ai.Model)
	onCancelCallback          func()
	errorMessage              *string
	refreshStatusMessage      string
	refreshStatusSuccess      bool
	ui                        tui.TUI
	scopedModels              []core.ScopedModel
	defaultModel              *DefaultModelRef
	scope                     mseModelScope
	scopeText                 *tui.Text
	scopeHintText             *tui.Text
	refreshCancel             context.CancelFunc
	refreshTimer              *time.Timer
	timedOut                  atomic.Bool
	closed                    bool
	focused                   bool
}

var _ tui.Focusable = (*ModelSelectorComponent)(nil)

// NewModelSelectorComponent constructs a model selector backed by modelRuntime and starts an async catalog refresh.
func NewModelSelectorComponent(
	ui tui.TUI,
	currentModel *ai.Model,
	modelRuntime *core.ModelRuntime,
	scopedModels []core.ScopedModel,
	onSelect func(model *ai.Model),
	onCancel func(),
	initialSearchInput *string,
	onSelectAsDefault func(model *ai.Model),
	defaultModel *DefaultModelRef,
) *ModelSelectorComponent {
	var rt mseModelRuntime
	if modelRuntime != nil {
		rt = modelRuntime
	}
	return mseNewModelSelectorWithRuntime(
		ui,
		currentModel,
		rt,
		scopedModels,
		onSelect,
		onCancel,
		initialSearchInput,
		onSelectAsDefault,
		defaultModel,
	)
}

func mseNewModelSelectorWithRuntime(
	ui tui.TUI,
	currentModel *ai.Model,
	runtime mseModelRuntime,
	scopedModels []core.ScopedModel,
	onSelect func(model *ai.Model),
	onCancel func(),
	initialSearchInput *string,
	onSelectAsDefault func(model *ai.Model),
	defaultModel *DefaultModelRef,
) *ModelSelectorComponent {
	copiedScoped := make([]core.ScopedModel, len(scopedModels))
	copy(copiedScoped, scopedModels)

	scope := mseScopeAll
	if len(copiedScoped) > 0 {
		scope = mseScopeScoped
	}

	var defCopy *DefaultModelRef
	if defaultModel != nil {
		d := *defaultModel
		defCopy = &d
	}

	refreshCtx, refreshCancel := context.WithCancel(context.Background())

	c := &ModelSelectorComponent{
		Container:                 tui.NewContainer(),
		listContainer:             tui.NewContainer(),
		currentModel:              currentModel,
		modelRuntime:              runtime,
		onSelectCallback:          onSelect,
		onSelectAsDefaultCallback: onSelectAsDefault,
		onCancelCallback:          onCancel,
		refreshStatusMessage:      "Refreshing model catalogs…",
		ui:                        ui,
		scopedModels:              copiedScoped,
		defaultModel:              defCopy,
		scope:                     scope,
		refreshCancel:             refreshCancel,
	}

	th := theme.CurrentTheme()
	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	if len(copiedScoped) > 0 {
		c.scopeText = tui.NewText(c.getScopeText(), 0, 0, nil)
		c.AddChild(c.scopeText)
		c.scopeHintText = tui.NewText(c.getScopeHintText(), 0, 0, nil)
		c.AddChild(c.scopeHintText)
	} else {
		hintText := "Only showing models from configured providers. Use /login to add providers."
		c.AddChild(tui.NewText(th.Fg(theme.FgWarning, hintText), 0, 0, nil))
	}
	c.AddChild(tui.NewSpacer(1))

	c.searchInput = tui.NewInput(nil)
	if initialSearchInput != nil && *initialSearchInput != "" {
		c.searchInput.SetValue(*initialSearchInput)
	}
	c.searchInput.OnSubmit = func(string) {
		c.mu.Lock()
		var selected *ai.Model
		if c.selectedIndex >= 0 && c.selectedIndex < len(c.filteredModels) {
			selected = c.filteredModels[c.selectedIndex].model
		}
		c.mu.Unlock()
		if selected != nil {
			c.handleSelect(selected)
		}
	}
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))

	if onSelectAsDefault != nil {
		hint := fmt.Sprintf(
			"  %s to select · %s to set as default · %s to cancel",
			theme.KeyDisplayText(tui.KeybindingTuiSelectConfirm),
			theme.KeyDisplayText(core.KeybindingAppModelsSave),
			theme.KeyDisplayText(tui.KeybindingTuiSelectCancel),
		)
		c.AddChild(tui.NewText(th.Fg(theme.FgDim, hint), 0, 0, nil))
	}
	c.AddChild(NewDynamicBorder())

	c.mu.Lock()
	c.loadModelsFromSnapshotLocked()
	if initialSearchInput != nil && *initialSearchInput != "" {
		c.filterModelsLocked(*initialSearchInput, nil)
	} else {
		c.updateListLocked()
	}
	if runtime != nil {
		c.refreshTimer = time.AfterFunc(mseRefreshTimeoutMs, func() {
			c.timedOut.Store(true)
			refreshCancel()
		})
	}
	c.mu.Unlock()

	if ui != nil {
		ui.RequestRender(nil)
	}
	if runtime != nil {
		go c.refreshModels(refreshCtx)
	}
	return c
}

// CapturesViewportInput reports that this selector intercepts viewport scroll keys.
func (m *ModelSelectorComponent) CapturesViewportInput() bool {
	return true
}

// Focused reports whether the search input holds focus.
func (m *ModelSelectorComponent) Focused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.focused
}

// SetFocused propagates focus state to the embedded search input.
func (m *ModelSelectorComponent) SetFocused(focused bool) {
	m.mu.Lock()
	m.focused = focused
	in := m.searchInput
	m.mu.Unlock()
	in.SetFocused(focused)
}

// Invalidate drops cached rendering state.
func (m *ModelSelectorComponent) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Container.Invalidate()
}

// Render draws the model selector safely under lock.
func (m *ModelSelectorComponent) Render(width int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Container.Render(width)
}

// GetSearchInput returns the inner search input widget.
func (m *ModelSelectorComponent) GetSearchInput() *tui.Input {
	return m.searchInput
}

// Dispose aborts any in-flight catalog refresh and stops the refresh timeout timer.
func (m *ModelSelectorComponent) Dispose() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	timer := m.refreshTimer
	m.refreshTimer = nil
	cancel := m.refreshCancel
	m.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	if cancel != nil {
		cancel()
	}
}

func (m *ModelSelectorComponent) loadModelsFromSnapshotLocked() {
	if m.modelRuntime != nil {
		snapshot := m.modelRuntime.GetAvailableSnapshot()
		items := make([]mseModelItem, 0, len(snapshot))
		for _, model := range snapshot {
			if model == nil {
				continue
			}
			items = append(items, mseModelItem{
				provider: string(model.Provider),
				id:       model.Id,
				model:    model,
			})
		}
		m.allModels = m.sortModels(items)

		for i, scoped := range m.scopedModels {
			if scoped.Model == nil {
				continue
			}
			if refreshed := m.modelRuntime.GetModel(string(scoped.Model.Provider), scoped.Model.Id); refreshed != nil {
				m.scopedModels[i].Model = refreshed
			}
		}
	}

	scopedItems := make([]mseModelItem, 0, len(m.scopedModels))
	for _, scoped := range m.scopedModels {
		if scoped.Model == nil {
			continue
		}
		scopedItems = append(scopedItems, mseModelItem{
			provider: string(scoped.Model.Provider),
			id:       scoped.Model.Id,
			model:    scoped.Model,
		})
	}
	m.scopedModelItems = scopedItems

	if m.scope == mseScopeScoped {
		m.activeModels = m.scopedModelItems
	} else {
		m.activeModels = m.allModels
	}
	m.filteredModels = m.activeModels

	currentIndex := slices.IndexFunc(m.filteredModels, func(item mseModelItem) bool {
		return ai.ModelsAreEqual(m.currentModel, item.model)
	})
	if currentIndex >= 0 {
		m.selectedIndex = currentIndex
	} else {
		m.selectedIndex = min(m.selectedIndex, max(0, len(m.filteredModels)-1))
	}
}

func (m *ModelSelectorComponent) refreshModels(ctx context.Context) {
	result, err := RefreshModelCatalogs(ctx, m.modelRuntime)

	m.mu.Lock()
	timer := m.refreshTimer
	m.refreshTimer = nil
	closed := m.closed
	m.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if closed {
		return
	}

	timedOut := m.timedOut.Load()
	applyUpdate := func() {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return
		}
		m.refreshStatusMessage = ""
		m.refreshStatusSuccess = false
		if err != nil {
			if timedOut || errors.Is(err, context.DeadlineExceeded) {
				m.errorMessage = mseStrPtr("Model refresh timed out; showing cached models.")
			} else if errors.Is(err, context.Canceled) {
				m.mu.Unlock()
				return
			} else {
				m.errorMessage = mseStrPtr(fmt.Sprintf("Could not refresh model catalogs: %s", err.Error()))
			}
			m.updateListLocked()
			m.mu.Unlock()
			if m.ui != nil {
				m.ui.RequestRender(nil)
			}
			return
		}

		if result.Aborted && timedOut {
			m.errorMessage = mseStrPtr("Model refresh timed out; showing cached models.")
		} else if result.Errors != nil && result.Errors.Len() == 1 {
			keys := result.Errors.Keys()
			m.errorMessage = mseStrPtr(fmt.Sprintf("Could not refresh %s; showing cached models.", keys[0]))
		} else if result.Errors != nil && result.Errors.Len() > 1 {
			keys := result.Errors.Keys()
			m.errorMessage = mseStrPtr(fmt.Sprintf(
				"Could not refresh %d model catalogs (%s); showing cached models.",
				len(keys),
				strings.Join(keys, ", "),
			))
		} else {
			m.errorMessage = m.modelRuntime.GetError()
			if m.errorMessage == nil || *m.errorMessage == "" {
				m.refreshStatusMessage = "Model catalogs refreshed."
				m.refreshStatusSuccess = true
			}
		}

		var selectedModel *ai.Model
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.filteredModels) {
			selectedModel = m.filteredModels[m.selectedIndex].model
		}
		m.loadModelsFromSnapshotLocked()
		query := m.searchInput.GetValue()
		m.filterModelsLocked(query, selectedModel)
		m.mu.Unlock()

		if m.ui != nil {
			m.ui.RequestRender(nil)
		}
	}

	if m.ui != nil {
		m.ui.Post(applyUpdate)
	} else {
		applyUpdate()
	}
}

func (m *ModelSelectorComponent) sortModels(models []mseModelItem) []mseModelItem {
	sorted := make([]mseModelItem, len(models))
	copy(sorted, models)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		aIsCurrent := ai.ModelsAreEqual(m.currentModel, a.model)
		bIsCurrent := ai.ModelsAreEqual(m.currentModel, b.model)
		if aIsCurrent != bIsCurrent {
			return aIsCurrent
		}
		aIsDefault := m.isDefaultModel(a.model)
		bIsDefault := m.isDefaultModel(b.model)
		if aIsDefault != bIsDefault {
			return aIsDefault
		}
		return js.LocaleCompare(a.provider, b.provider) < 0
	})
	return sorted
}

func (m *ModelSelectorComponent) getScopeText() string {
	th := theme.CurrentTheme()
	allText := th.Fg(theme.FgMuted, "all")
	if m.scope == mseScopeAll {
		allText = th.Fg(theme.FgAccent, "all")
	}
	scopedText := th.Fg(theme.FgMuted, "scoped")
	if m.scope == mseScopeScoped {
		scopedText = th.Fg(theme.FgAccent, "scoped")
	}
	return th.Fg(theme.FgMuted, "Scope: ") + allText + th.Fg(theme.FgMuted, " | ") + scopedText
}

func (m *ModelSelectorComponent) getScopeHintText() string {
	th := theme.CurrentTheme()
	return theme.AppKeyHint(th, tui.KeybindingTuiInputTab, "scope") + th.Fg(theme.FgMuted, " (all/scoped)")
}

func (m *ModelSelectorComponent) isDefaultModel(model *ai.Model) bool {
	return m.defaultModel != nil && model != nil &&
		m.defaultModel.Provider == string(model.Provider) &&
		m.defaultModel.Id == model.Id
}

func (m *ModelSelectorComponent) isDefaultSearch(query string) bool {
	normalized := js.ToLower(js.Trim(query))
	return normalized != "" && strings.HasPrefix("default", normalized)
}

func (m *ModelSelectorComponent) setScopeLocked(scope mseModelScope) {
	if m.scope == scope {
		return
	}
	m.scope = scope
	if m.scope == mseScopeScoped {
		m.activeModels = m.scopedModelItems
	} else {
		m.activeModels = m.allModels
	}
	currentIndex := slices.IndexFunc(m.activeModels, func(item mseModelItem) bool {
		return ai.ModelsAreEqual(m.currentModel, item.model)
	})
	if currentIndex >= 0 {
		m.selectedIndex = currentIndex
	} else {
		m.selectedIndex = 0
	}
	m.filterModelsLocked(m.searchInput.GetValue(), nil)
	if m.scopeText != nil {
		m.scopeText.SetText(m.getScopeText())
	}
}

func (m *ModelSelectorComponent) filterModelsLocked(query string, preferredModel *ai.Model) {
	if query != "" {
		filtered := tui.FuzzyFilter(m.activeModels, query, func(item mseModelItem) string {
			defaultText := ""
			if m.isDefaultModel(item.model) {
				defaultText = " default"
			}
			return GetModelSelectorSearchText(ModelSearchItem{
				Id:       item.id,
				Provider: item.provider,
				Name:     mseNonEmptyPtr(item.model.Name),
			}) + defaultText
		})
		if m.isDefaultSearch(query) {
			var defaultItems []mseModelItem
			defaultKeys := make(map[string]struct{})
			for _, item := range m.activeModels {
				if m.isDefaultModel(item.model) {
					defaultItems = append(defaultItems, item)
					defaultKeys[item.provider+"\x00"+item.id] = struct{}{}
				}
			}
			combined := make([]mseModelItem, 0, len(filtered)+len(defaultItems))
			combined = append(combined, defaultItems...)
			for _, item := range filtered {
				if _, exists := defaultKeys[item.provider+"\x00"+item.id]; !exists {
					combined = append(combined, item)
				}
			}
			m.filteredModels = combined
		} else {
			m.filteredModels = filtered
		}
	} else {
		m.filteredModels = m.activeModels
	}

	preferredIndex := -1
	if preferredModel != nil {
		preferredIndex = slices.IndexFunc(m.filteredModels, func(item mseModelItem) bool {
			return ai.ModelsAreEqual(preferredModel, item.model)
		})
	}
	switch {
	case preferredIndex >= 0:
		m.selectedIndex = preferredIndex
	case query != "":
		m.selectedIndex = 0
	default:
		m.selectedIndex = min(m.selectedIndex, max(0, len(m.filteredModels)-1))
	}
	m.updateListLocked()
}

func (m *ModelSelectorComponent) updateListLocked() {
	th := theme.CurrentTheme()
	m.listContainer.Clear()

	startIndex := max(0, min(m.selectedIndex-mseMaxVisible/2, len(m.filteredModels)-mseMaxVisible))
	endIndex := min(startIndex+mseMaxVisible, len(m.filteredModels))

	for i := startIndex; i < endIndex; i++ {
		item := m.filteredModels[i]
		isSelected := i == m.selectedIndex
		isCurrent := ai.ModelsAreEqual(m.currentModel, item.model)
		isDefault := m.isDefaultModel(item.model)

		defaultBadge := ""
		if isDefault {
			defaultBadge = th.Fg(theme.FgMuted, " · default")
		}
		cursor := "  "
		if isSelected {
			cursor = th.Fg(theme.FgAccent, "→ ")
		}
		currentMarker := "  "
		if isCurrent {
			currentMarker = th.Fg(theme.FgAccent, "✓ ")
		}
		modelText := item.id
		if isSelected {
			modelText = th.Fg(theme.FgAccent, item.id)
		}
		nameText := ""
		if distinct := GetDistinctModelName(ModelSearchItem{
			Id:   item.id,
			Name: mseNonEmptyPtr(item.model.Name),
		}); distinct != nil && *distinct != "" {
			nameText = th.Fg(theme.FgMuted, fmt.Sprintf(" (%s)", *distinct))
		}
		providerBadge := th.Fg(theme.FgMuted, fmt.Sprintf("[%s]", item.provider))
		line := cursor + currentMarker + modelText + nameText + " " + providerBadge + defaultBadge
		m.listContainer.AddChild(tui.NewText(line, 0, 0, nil))
	}

	if startIndex > 0 || endIndex < len(m.filteredModels) {
		scrollInfo := th.Fg(theme.FgMuted, fmt.Sprintf("  (%d/%d)", m.selectedIndex+1, len(m.filteredModels)))
		m.listContainer.AddChild(tui.NewText(scrollInfo, 0, 0, nil))
	}

	switch {
	case m.errorMessage != nil && *m.errorMessage != "":
		for _, line := range strings.Split(*m.errorMessage, "\n") {
			m.listContainer.AddChild(tui.NewText(th.Fg(theme.FgError, line), 0, 0, nil))
		}
	case len(m.filteredModels) == 0:
		m.listContainer.AddChild(tui.NewText(th.Fg(theme.FgMuted, "  No matching models"), 0, 0, nil))
	default:
		selected := m.filteredModels[m.selectedIndex]
		m.listContainer.AddChild(tui.NewSpacer(1))
		m.listContainer.AddChild(tui.NewText(th.Fg(theme.FgMuted, "  Model Name: "+selected.model.Name), 0, 0, nil))
	}

	if m.refreshStatusMessage != "" {
		m.listContainer.AddChild(tui.NewSpacer(1))
		token := theme.FgMuted
		if m.refreshStatusSuccess {
			token = theme.FgSuccess
		}
		m.listContainer.AddChild(tui.NewText(th.Fg(token, "  "+m.refreshStatusMessage), 0, 0, nil))
	}
}

func (m *ModelSelectorComponent) handleSelect(model *ai.Model) {
	m.Dispose()
	if m.onSelectCallback != nil {
		m.onSelectCallback(model)
	}
}

// HandleInput processes scope toggle, navigation, selection, default saving, and search filtering.
func (m *ModelSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	m.mu.Lock()
	pageIndex := tui.GetSelectPageIndex(kb, keyData, m.selectedIndex, len(m.filteredModels), mseMaxVisible)

	switch {
	case kb.Matches(keyData, tui.KeybindingTuiInputTab):
		if len(m.scopedModelItems) > 0 {
			nextScope := mseScopeScoped
			if m.scope == mseScopeScoped {
				nextScope = mseScopeAll
			}
			m.setScopeLocked(nextScope)
			if m.scopeHintText != nil {
				m.scopeHintText.SetText(m.getScopeHintText())
			}
		}
		m.mu.Unlock()
		return

	case kb.Matches(keyData, tui.KeybindingTuiSelectUp):
		if len(m.filteredModels) == 0 {
			m.mu.Unlock()
			return
		}
		if m.selectedIndex == 0 {
			m.selectedIndex = len(m.filteredModels) - 1
		} else {
			m.selectedIndex--
		}
		m.updateListLocked()
		m.mu.Unlock()
		return

	case kb.Matches(keyData, tui.KeybindingTuiSelectDown):
		if len(m.filteredModels) == 0 {
			m.mu.Unlock()
			return
		}
		if m.selectedIndex == len(m.filteredModels)-1 {
			m.selectedIndex = 0
		} else {
			m.selectedIndex++
		}
		m.updateListLocked()
		m.mu.Unlock()
		return

	case pageIndex != nil:
		if len(m.filteredModels) == 0 {
			m.mu.Unlock()
			return
		}
		m.selectedIndex = *pageIndex
		m.updateListLocked()
		m.mu.Unlock()
		return

	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm):
		var selected *ai.Model
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.filteredModels) {
			selected = m.filteredModels[m.selectedIndex].model
		}
		m.mu.Unlock()
		if selected != nil {
			m.handleSelect(selected)
		}
		return

	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		cb := m.onCancelCallback
		m.mu.Unlock()
		m.Dispose()
		if cb != nil {
			cb()
		}
		return

	case kb.Matches(keyData, core.KeybindingAppModelsSave) && m.onSelectAsDefaultCallback != nil:
		var selected *ai.Model
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.filteredModels) {
			selected = m.filteredModels[m.selectedIndex].model
		}
		cb := m.onSelectAsDefaultCallback
		m.mu.Unlock()
		if selected != nil {
			m.Dispose()
			cb(selected)
		}
		return
	}

	in := m.searchInput
	m.mu.Unlock()

	in.HandleInput(keyData)
	query := in.GetValue()
	m.mu.Lock()
	m.filterModelsLocked(query, nil)
	m.mu.Unlock()
}
