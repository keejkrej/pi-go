// Ported from packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const smsMaxVisible = 8

// ModelsConfig supplies the catalog models and initial scoped enabled IDs for ScopedModelsSelectorComponent.
// EnabledModelIds == nil means all models are enabled; a non-nil empty slice means all models are cleared.
type ModelsConfig struct {
	AllModels       []*ai.Model
	EnabledModelIds []string
	RefreshStatus   *string
}

// ModelsCallbacks handles session changes, persistence requests, and cancel events from ScopedModelsSelectorComponent.
type ModelsCallbacks struct {
	OnChange  func(enabledModelIds []string)
	OnPersist func(enabledModelIds []string)
	OnCancel  func()
}

type smsModelItem struct {
	fullId  string
	model   *ai.Model
	enabled bool
}

func smsCopyIds(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

func smsIsEnabled(enabledIds []string, id string) bool {
	return enabledIds == nil || slices.Contains(enabledIds, id)
}

func smsNormalizeEnabled(result []string, allIds []string) []string {
	if len(result) == len(allIds) {
		allPresent := true
		for _, id := range result {
			if !slices.Contains(allIds, id) {
				allPresent = false
				break
			}
		}
		if allPresent {
			return nil
		}
	}
	return result
}

func smsToggle(enabledIds []string, allIds []string, id string) []string {
	if enabledIds == nil {
		out := make([]string, 0, len(allIds))
		for _, modelId := range allIds {
			if modelId != id {
				out = append(out, modelId)
			}
		}
		return out
	}
	if idx := slices.Index(enabledIds, id); idx >= 0 {
		out := make([]string, 0, len(enabledIds)-1)
		out = append(out, enabledIds[:idx]...)
		out = append(out, enabledIds[idx+1:]...)
		return out
	}
	next := make([]string, len(enabledIds)+1)
	copy(next, enabledIds)
	next[len(enabledIds)] = id
	return smsNormalizeEnabled(next, allIds)
}

func smsEnableAll(enabledIds []string, allIds []string, targetIds []string) []string {
	if enabledIds == nil {
		return nil
	}
	targets := allIds
	if targetIds != nil {
		targets = targetIds
	}
	result := smsCopyIds(enabledIds)
	for _, id := range targets {
		if !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return smsNormalizeEnabled(result, allIds)
}

func smsClearAll(enabledIds []string, allIds []string, targetIds []string) []string {
	if enabledIds == nil {
		if targetIds == nil {
			return []string{}
		}
		targetSet := make(map[string]struct{}, len(targetIds))
		for _, id := range targetIds {
			targetSet[id] = struct{}{}
		}
		out := make([]string, 0, len(allIds))
		for _, id := range allIds {
			if _, drop := targetSet[id]; !drop {
				out = append(out, id)
			}
		}
		return out
	}
	toDrop := enabledIds
	if targetIds != nil {
		toDrop = targetIds
	}
	targetSet := make(map[string]struct{}, len(toDrop))
	for _, id := range toDrop {
		targetSet[id] = struct{}{}
	}
	out := make([]string, 0, len(enabledIds))
	for _, id := range enabledIds {
		if _, drop := targetSet[id]; !drop {
			out = append(out, id)
		}
	}
	return out
}

func smsMove(enabledIds []string, id string, delta int) []string {
	if enabledIds == nil {
		return nil
	}
	idx := slices.Index(enabledIds, id)
	if idx < 0 {
		return smsCopyIds(enabledIds)
	}
	newIdx := idx + delta
	if newIdx < 0 || newIdx >= len(enabledIds) {
		return smsCopyIds(enabledIds)
	}
	result := smsCopyIds(enabledIds)
	result[idx], result[newIdx] = result[newIdx], result[idx]
	return result
}

func smsGetSortedIds(enabledIds []string, allIds []string) []string {
	if enabledIds == nil {
		return allIds
	}
	enabledSet := make(map[string]struct{}, len(enabledIds))
	for _, id := range enabledIds {
		enabledSet[id] = struct{}{}
	}
	out := make([]string, 0, len(enabledIds)+len(allIds))
	out = append(out, enabledIds...)
	for _, id := range allIds {
		if _, exists := enabledSet[id]; !exists {
			out = append(out, id)
		}
	}
	return out
}

func smsNonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ScopedModelsSelectorComponent lets users toggle and reorder models included in Ctrl+P model cycling.
type ScopedModelsSelectorComponent struct {
	*tui.Container
	modelsById        map[string]*ai.Model
	allIds            []string
	enabledIds        []string
	filteredItems     []smsModelItem
	selectedIndex     int
	searchInput       *tui.Input
	listContainer     *tui.Container
	footerText        *tui.Text
	refreshStatusText *tui.Text
	callbacks         ModelsCallbacks
	maxVisible        int
	isDirty           bool
	focused           bool
}

var _ tui.Focusable = (*ScopedModelsSelectorComponent)(nil)

// NewScopedModelsSelectorComponent constructs a scoped models picker for config and callbacks.
func NewScopedModelsSelectorComponent(config ModelsConfig, callbacks ModelsCallbacks) *ScopedModelsSelectorComponent {
	th := theme.CurrentTheme()
	c := &ScopedModelsSelectorComponent{
		Container:  tui.NewContainer(),
		modelsById: make(map[string]*ai.Model, len(config.AllModels)),
		enabledIds: smsCopyIds(config.EnabledModelIds),
		callbacks:  callbacks,
		maxVisible: smsMaxVisible,
	}

	for _, model := range config.AllModels {
		if model == nil {
			continue
		}
		fullId := string(model.Provider) + "/" + model.Id
		c.modelsById[fullId] = model
		c.allIds = append(c.allIds, fullId)
	}
	c.filteredItems = c.buildItems()

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("Model Configuration")), 0, 0, nil))
	c.AddChild(tui.NewText(
		th.Fg(theme.FgMuted, fmt.Sprintf("Session-only. %s to save to settings.", theme.KeyDisplayText(core.KeybindingAppModelsSave))),
		0,
		0,
		nil,
	))
	c.AddChild(tui.NewSpacer(1))

	c.searchInput = tui.NewInput(nil)
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))

	c.listContainer = tui.NewContainer()
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))

	if config.RefreshStatus != nil && *config.RefreshStatus != "" {
		c.refreshStatusText = tui.NewText(th.Fg(theme.FgMuted, "  "+*config.RefreshStatus), 0, 0, nil)
		c.AddChild(c.refreshStatusText)
	}
	c.footerText = tui.NewText(c.getFooterText(), 0, 0, nil)
	c.AddChild(c.footerText)
	c.AddChild(NewDynamicBorder())

	c.updateList()
	return c
}

// CapturesViewportInput reports that this selector intercepts viewport scroll keys.
func (s *ScopedModelsSelectorComponent) CapturesViewportInput() bool {
	return true
}

// Focused reports whether the search input holds focus.
func (s *ScopedModelsSelectorComponent) Focused() bool {
	return s.focused
}

// SetFocused propagates focus state to the embedded search input.
func (s *ScopedModelsSelectorComponent) SetFocused(focused bool) {
	s.focused = focused
	s.searchInput.SetFocused(focused)
}

// GetSearchInput returns the embedded search input widget.
func (s *ScopedModelsSelectorComponent) GetSearchInput() *tui.Input {
	return s.searchInput
}

// UpdateModels replaces the available model catalog (and optionally enabledModelIds) while preserving selection.
func (s *ScopedModelsSelectorComponent) UpdateModels(allModels []*ai.Model, enabledModelIds ...[]string) {
	var selectedId string
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredItems) {
		selectedId = s.filteredItems[s.selectedIndex].fullId
	}
	if len(enabledModelIds) > 0 {
		s.enabledIds = smsCopyIds(enabledModelIds[0])
	}
	s.modelsById = make(map[string]*ai.Model, len(allModels))
	s.allIds = make([]string, 0, len(allModels))
	for _, model := range allModels {
		if model == nil {
			continue
		}
		fullId := string(model.Provider) + "/" + model.Id
		s.modelsById[fullId] = model
		s.allIds = append(s.allIds, fullId)
	}
	s.refresh()
	if selectedId != "" {
		if refreshedIdx := slices.IndexFunc(s.filteredItems, func(item smsModelItem) bool {
			return item.fullId == selectedId
		}); refreshedIdx >= 0 {
			s.selectedIndex = refreshedIdx
			s.updateList()
		}
	}
}

// SetRefreshStatus updates the optional refresh status footer text.
func (s *ScopedModelsSelectorComponent) SetRefreshStatus(message *string, kind ...string) {
	if s.refreshStatusText == nil {
		return
	}
	if message == nil {
		s.refreshStatusText.SetText("")
		return
	}
	token := theme.FgMuted
	if len(kind) > 0 {
		switch kind[0] {
		case "success":
			token = theme.FgSuccess
		case "warning":
			token = theme.FgWarning
		}
	}
	s.refreshStatusText.SetText(theme.CurrentTheme().Fg(token, "  "+*message))
}

func (s *ScopedModelsSelectorComponent) buildItems() []smsModelItem {
	sorted := smsGetSortedIds(s.enabledIds, s.allIds)
	items := make([]smsModelItem, len(sorted))
	for i, id := range sorted {
		items[i] = smsModelItem{
			fullId:  id,
			model:   s.modelsById[id],
			enabled: smsIsEnabled(s.enabledIds, id),
		}
	}
	return items
}

func (s *ScopedModelsSelectorComponent) getFooterText() string {
	th := theme.CurrentTheme()
	var countText string
	if s.enabledIds == nil {
		countText = "all enabled"
	} else {
		enabledCount := 0
		unavailableCount := 0
		for _, id := range s.enabledIds {
			if _, ok := s.modelsById[id]; ok {
				enabledCount++
			} else {
				unavailableCount++
			}
		}
		countText = fmt.Sprintf("%d/%d enabled", enabledCount, len(s.allIds))
		if unavailableCount > 0 {
			countText += fmt.Sprintf(" · %d unavailable", unavailableCount)
		}
	}
	parts := []string{
		theme.KeyDisplayText(tui.KeybindingTuiSelectConfirm) + " toggle",
		theme.KeyDisplayText(core.KeybindingAppModelsEnableAll) + " all",
		theme.KeyDisplayText(core.KeybindingAppModelsClearAll) + " clear",
		theme.KeyDisplayText(core.KeybindingAppModelsToggleProvider) + " provider",
		theme.KeyDisplayText(core.KeybindingAppModelsReorderUp) + "/" + theme.KeyDisplayText(core.KeybindingAppModelsReorderDown) + " reorder",
		theme.KeyDisplayText(core.KeybindingAppModelsSave) + " save",
		countText,
	}
	if s.isDirty {
		return th.Fg(theme.FgDim, "  "+strings.Join(parts, " · ")+" ") + th.Fg(theme.FgWarning, "(unsaved)")
	}
	return th.Fg(theme.FgDim, "  "+strings.Join(parts, " · "))
}

func (s *ScopedModelsSelectorComponent) refresh() {
	query := s.searchInput.GetValue()
	items := s.buildItems()
	if query != "" {
		s.filteredItems = tui.FuzzyFilter(items, query, func(item smsModelItem) string {
			if item.model != nil {
				return GetModelSearchText(ModelSearchItem{
					Id:       item.model.Id,
					Provider: string(item.model.Provider),
					Name:     smsNonEmptyPtr(item.model.Name),
				})
			}
			return item.fullId
		})
	} else {
		s.filteredItems = items
	}
	s.selectedIndex = min(s.selectedIndex, max(0, len(s.filteredItems)-1))
	s.updateList()
	s.footerText.SetText(s.getFooterText())
}

func (s *ScopedModelsSelectorComponent) notifyChange() {
	if s.callbacks.OnChange != nil {
		s.callbacks.OnChange(smsCopyIds(s.enabledIds))
	}
}

func (s *ScopedModelsSelectorComponent) updateList() {
	th := theme.CurrentTheme()
	s.listContainer.Clear()

	if len(s.filteredItems) == 0 {
		s.listContainer.AddChild(tui.NewText(th.Fg(theme.FgMuted, "  No matching models"), 0, 0, nil))
		return
	}

	startIndex := max(0, min(s.selectedIndex-s.maxVisible/2, len(s.filteredItems)-s.maxVisible))
	endIndex := min(startIndex+s.maxVisible, len(s.filteredItems))

	for i := startIndex; i < endIndex; i++ {
		item := s.filteredItems[i]
		isSelected := i == s.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = th.Fg(theme.FgAccent, "→ ")
		}

		id := item.fullId
		styledId := th.Strikethrough(id)
		if item.model != nil {
			id = item.model.Id
			styledId = id
		}
		modelText := styledId
		if isSelected {
			modelText = th.Fg(theme.FgAccent, styledId)
		}

		nameText := ""
		if item.model != nil {
			if distinct := GetDistinctModelName(ModelSearchItem{
				Id:   item.model.Id,
				Name: smsNonEmptyPtr(item.model.Name),
			}); distinct != nil && *distinct != "" {
				nameText = th.Fg(theme.FgMuted, fmt.Sprintf(" (%s)", *distinct))
			}
		}

		providerBadge := th.Fg(theme.FgMuted, " [unavailable]")
		if item.model != nil {
			providerBadge = th.Fg(theme.FgMuted, fmt.Sprintf(" [%s]", item.model.Provider))
		}
		status := "  "
		if item.model != nil && item.enabled {
			status = th.Fg(theme.FgAccent, "✓ ")
		}

		s.listContainer.AddChild(tui.NewText(prefix+status+modelText+nameText+providerBadge, 0, 0, nil))
	}

	if startIndex > 0 || endIndex < len(s.filteredItems) {
		s.listContainer.AddChild(tui.NewText(
			th.Fg(theme.FgMuted, fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.filteredItems))),
			0,
			0,
			nil,
		))
	}

	if len(s.filteredItems) > 0 && s.selectedIndex < len(s.filteredItems) {
		selected := s.filteredItems[s.selectedIndex]
		s.listContainer.AddChild(tui.NewSpacer(1))
		detail := "  Model unavailable"
		if selected.model != nil {
			detail = "  Model Name: " + selected.model.Name
		}
		s.listContainer.AddChild(tui.NewText(th.Fg(theme.FgMuted, detail), 0, 0, nil))
	}
}

func (s *ScopedModelsSelectorComponent) filteredTargetIds() []string {
	if s.searchInput.GetValue() == "" {
		return nil
	}
	ids := make([]string, len(s.filteredItems))
	for i, item := range s.filteredItems {
		ids[i] = item.fullId
	}
	return ids
}

// HandleInput processes navigation, enable/disable/reorder shortcuts, persistence, and search input.
func (s *ScopedModelsSelectorComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()

	if kb.Matches(data, tui.KeybindingTuiSelectUp) {
		if len(s.filteredItems) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filteredItems) - 1
		} else {
			s.selectedIndex--
		}
		s.updateList()
		return
	}
	if kb.Matches(data, tui.KeybindingTuiSelectDown) {
		if len(s.filteredItems) == 0 {
			return
		}
		if s.selectedIndex == len(s.filteredItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.updateList()
		return
	}
	if pageIndex := tui.GetSelectPageIndex(kb, data, s.selectedIndex, len(s.filteredItems), s.maxVisible); pageIndex != nil {
		if len(s.filteredItems) == 0 {
			return
		}
		s.selectedIndex = *pageIndex
		s.updateList()
		return
	}

	reorderUp := kb.Matches(data, core.KeybindingAppModelsReorderUp)
	reorderDown := kb.Matches(data, core.KeybindingAppModelsReorderDown)
	if reorderUp || reorderDown {
		if s.enabledIds == nil {
			return
		}
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredItems) {
			item := s.filteredItems[s.selectedIndex]
			if smsIsEnabled(s.enabledIds, item.fullId) {
				delta := 1
				if reorderUp {
					delta = -1
				}
				curIdx := slices.Index(s.enabledIds, item.fullId)
				newIdx := curIdx + delta
				if newIdx >= 0 && newIdx < len(s.enabledIds) {
					s.enabledIds = smsMove(s.enabledIds, item.fullId, delta)
					s.isDirty = true
					s.selectedIndex += delta
					s.refresh()
					s.notifyChange()
				}
			}
		}
		return
	}

	if kb.Matches(data, tui.KeybindingTuiSelectConfirm) {
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredItems) {
			item := s.filteredItems[s.selectedIndex]
			s.enabledIds = smsToggle(s.enabledIds, s.allIds, item.fullId)
			s.isDirty = true
			s.refresh()
			s.notifyChange()
		}
		return
	}

	if kb.Matches(data, core.KeybindingAppModelsEnableAll) {
		s.enabledIds = smsEnableAll(s.enabledIds, s.allIds, s.filteredTargetIds())
		s.isDirty = true
		s.refresh()
		s.notifyChange()
		return
	}

	if kb.Matches(data, core.KeybindingAppModelsClearAll) {
		s.enabledIds = smsClearAll(s.enabledIds, s.allIds, s.filteredTargetIds())
		s.isDirty = true
		s.refresh()
		s.notifyChange()
		return
	}

	if kb.Matches(data, core.KeybindingAppModelsToggleProvider) {
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredItems) {
			item := s.filteredItems[s.selectedIndex]
			if item.model != nil {
				provider := item.model.Provider
				var providerIds []string
				for _, id := range s.allIds {
					if m := s.modelsById[id]; m != nil && m.Provider == provider {
						providerIds = append(providerIds, id)
					}
				}
				allEnabled := true
				for _, id := range providerIds {
					if !smsIsEnabled(s.enabledIds, id) {
						allEnabled = false
						break
					}
				}
				if allEnabled {
					s.enabledIds = smsClearAll(s.enabledIds, s.allIds, providerIds)
				} else {
					s.enabledIds = smsEnableAll(s.enabledIds, s.allIds, providerIds)
				}
				s.isDirty = true
				s.refresh()
				s.notifyChange()
			}
		}
		return
	}

	if kb.Matches(data, core.KeybindingAppModelsSave) {
		if s.callbacks.OnPersist != nil {
			s.callbacks.OnPersist(smsCopyIds(s.enabledIds))
		}
		s.isDirty = false
		s.footerText.SetText(s.getFooterText())
		return
	}

	if tui.MatchesKey(data, tui.KeyCtrl("c")) {
		if s.searchInput.GetValue() != "" {
			s.searchInput.SetValue("")
			s.refresh()
		} else if s.callbacks.OnCancel != nil {
			s.callbacks.OnCancel()
		}
		return
	}

	if tui.MatchesKey(data, tui.KeyEscape) {
		if s.callbacks.OnCancel != nil {
			s.callbacks.OnCancel()
		}
		return
	}

	s.searchInput.HandleInput(data)
	s.refresh()
}
