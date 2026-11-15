// Ported from packages/tui/src/components/settings-list.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"strings"
	"sync"
)

func csliBoolPtr(v bool) *bool {
	return &v
}

// SettingsListSubmenuOptions is the optional second argument of a SettingItem submenu done callback.
type SettingsListSubmenuOptions struct {
	// NavigateTo moves the cursor to this item id after close, then opens that item.
	NavigateTo *string
}

// SettingItem is one settings row.
type SettingItem struct {
	// Id is the unique identifier for this setting.
	Id string
	// Label is the left-hand display text.
	Label string
	// Description is shown when the row is selected.
	Description *string
	// CurrentValue is the right-hand value.
	CurrentValue string
	// Values, when non-nil and non-empty, are cycled by Enter and Space.
	Values []string
	// Submenu, when non-nil, is opened by Enter.
	// done's selectedValue nil means leave the value unchanged.
	// done's options nil means do not navigate after close.
	Submenu func(currentValue string, done func(selectedValue *string, options *SettingsListSubmenuOptions)) Component
}

// SettingsListTheme styles a SettingsList.
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// SettingsListOptions configures a SettingsList. Nil means search is off.
type SettingsListOptions struct {
	// EnableSearch shows a filter input. Nil means false.
	EnableSearch *bool
}

// SettingsList is a scrollable settings menu with optional search and submenus.
type SettingsList struct {
	mu                 sync.Mutex
	items              []SettingItem
	filteredItems      []SettingItem
	theme              SettingsListTheme
	selectedIndex      int
	mousePressedIndex  *int
	maxVisible         int
	onChange           func(id string, newValue string)
	onCancel           func()
	searchInput        *Input
	searchEnabled      bool
	submenuComponent   Component
	submenuItemIndex   *int
	navigateAfterClose *string
}

// NewSettingsList returns a settings list.
// Nil options means search is disabled.
func NewSettingsList(items []SettingItem, maxVisible int, theme SettingsListTheme, onChange func(id string, newValue string), onCancel func(), options *SettingsListOptions) *SettingsList {
	itemsCopy := make([]SettingItem, len(items))
	copy(itemsCopy, items)
	filteredCopy := make([]SettingItem, len(items))
	copy(filteredCopy, items)

	searchEnabled := options != nil && options.EnableSearch != nil && *options.EnableSearch
	var searchInput *Input
	if searchEnabled {
		searchInput = NewInput(nil)
		searchInput.SetFocused(true)
	}

	return &SettingsList{
		items:         itemsCopy,
		filteredItems: filteredCopy,
		theme:         theme,
		maxVisible:    maxVisible,
		onChange:      onChange,
		onCancel:      onCancel,
		searchInput:   searchInput,
		searchEnabled: searchEnabled,
	}
}

func (s *SettingsList) updateValueLocked(id string, newValue string) {
	for i := range s.items {
		if s.items[i].Id == id {
			s.items[i].CurrentValue = newValue
		}
	}
	for i := range s.filteredItems {
		if s.filteredItems[i].Id == id {
			s.filteredItems[i].CurrentValue = newValue
		}
	}
}

// UpdateValue sets the current value of the item with id. Unknown ids are ignored.
func (s *SettingsList) UpdateValue(id string, newValue string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateValueLocked(id, newValue)
}

// SelectItem moves the selection to id. Unknown ids are ignored.
func (s *SettingsList) SelectItem(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	displayItems := s.getDisplayItems()
	for i, item := range displayItems {
		if item.Id == id {
			s.selectedIndex = i
			return
		}
	}
}

// Invalidate invalidates the open submenu, if any.
func (s *SettingsList) Invalidate() {
	s.mu.Lock()
	sub := s.submenuComponent
	s.mu.Unlock()
	if sub != nil {
		sub.Invalidate()
	}
}

// Render draws the submenu when one is open, otherwise the list.
func (s *SettingsList) Render(width int) []string {
	s.mu.Lock()
	sub := s.submenuComponent
	s.mu.Unlock()
	if sub != nil {
		return sub.Render(width)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renderMainList(width)
}

// HandleMouse forwards to the submenu or search input, or selects a row.
func (s *SettingsList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	s.mu.Lock()
	sub := s.submenuComponent
	if sub != nil {
		s.mu.Unlock()
		if mh, ok := sub.(interface {
			HandleMouse(TuiMouseEvent) *TuiMouseEventResult
		}); ok {
			res := mh.HandleMouse(event)
			if res == nil {
				return nil
			}
			copied := *res
			copied.Focus = csliBoolPtr(true)
			return &copied
		}
		return nil
	}

	if s.searchEnabled && s.searchInput != nil {
		if event.Y == 0 {
			si := s.searchInput
			s.mu.Unlock()
			res := si.HandleMouse(event)
			if res == nil {
				return nil
			}
			copied := *res
			copied.Focus = csliBoolPtr(true)
			return &copied
		}
		if event.Y == 1 {
			s.mu.Unlock()
			return nil
		}
	}

	displayItems := s.getDisplayItems()
	if len(displayItems) == 0 {
		s.mu.Unlock()
		return nil
	}

	if event.Type == TuiMouseEventTypeWheel && event.WheelDelta != nil && *event.WheelDelta != 0 {
		delta := 1
		if *event.WheelDelta < 0 {
			delta = -1
		}
		prev := s.selectedIndex
		next := s.selectedIndex + delta
		if next < 0 {
			next = 0
		} else if next >= len(displayItems) {
			next = len(displayItems) - 1
		}
		s.selectedIndex = next
		changed := s.selectedIndex != prev
		s.mu.Unlock()
		return &TuiMouseEventResult{
			Handled: csliBoolPtr(true),
			Render:  csliBoolPtr(changed),
		}
	}

	if event.Button != TuiMouseButtonLeft || (event.Type != TuiMouseEventTypePress && event.Type != TuiMouseEventTypeClick) {
		s.mu.Unlock()
		return nil
	}

	rowOffset := 0
	if s.searchEnabled {
		rowOffset = 2
	}
	startIndex, endIndex := s.getVisibleRange(displayItems)
	itemIndex := startIndex + event.Y - rowOffset
	if itemIndex < startIndex || itemIndex >= endIndex {
		s.mu.Unlock()
		return nil
	}

	if event.Type == TuiMouseEventTypePress {
		idx := itemIndex
		s.mousePressedIndex = &idx
		s.selectedIndex = itemIndex
		s.mu.Unlock()
		return &TuiMouseEventResult{
			Handled: csliBoolPtr(true),
			Focus:   csliBoolPtr(true),
		}
	}

	targetIndex := itemIndex
	if s.mousePressedIndex != nil {
		targetIndex = *s.mousePressedIndex
	}
	s.mousePressedIndex = nil
	if targetIndex < 0 {
		targetIndex = 0
	} else if targetIndex >= len(displayItems) {
		targetIndex = len(displayItems) - 1
	}
	s.selectedIndex = targetIndex
	s.mu.Unlock()
	s.activateItem()
	return &TuiMouseEventResult{
		Handled: csliBoolPtr(true),
	}
}

// HandleInput forwards to the submenu or edits the list and search query.
func (s *SettingsList) HandleInput(data string) {
	s.mu.Lock()
	sub := s.submenuComponent
	if sub != nil {
		s.mu.Unlock()
		if ih, ok := sub.(interface{ HandleInput(string) }); ok {
			ih.HandleInput(data)
		}
		return
	}

	kb := GetKeybindings()
	displayItems := s.getDisplayItems()
	pageIndex := GetSelectPageIndex(kb, data, s.selectedIndex, len(displayItems), s.maxVisible)

	if kb.Matches(data, KeybindingTuiSelectUp) {
		if len(displayItems) > 0 {
			if s.selectedIndex == 0 {
				s.selectedIndex = len(displayItems) - 1
			} else {
				s.selectedIndex--
			}
		}
		s.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiSelectDown) {
		if len(displayItems) > 0 {
			if s.selectedIndex == len(displayItems)-1 {
				s.selectedIndex = 0
			} else {
				s.selectedIndex++
			}
		}
		s.mu.Unlock()
		return
	}
	if pageIndex != nil {
		if len(displayItems) > 0 {
			s.selectedIndex = *pageIndex
		}
		s.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiSelectConfirm) ||
		(data == " " && (!s.searchEnabled || s.searchInput == nil || s.searchInput.GetValue() == "")) {
		s.mu.Unlock()
		s.activateItem()
		return
	}
	if kb.Matches(data, KeybindingTuiSelectCancel) {
		onCancel := s.onCancel
		s.mu.Unlock()
		if onCancel != nil {
			onCancel()
		}
		return
	}
	if s.searchEnabled && s.searchInput != nil {
		si := s.searchInput
		s.mu.Unlock()
		si.HandleInput(data)
		s.mu.Lock()
		s.applyFilter(si.GetValue())
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
}

// WantsKeyRelease reports whether key-release events should be delivered.
func (s *SettingsList) WantsKeyRelease() bool {
	s.mu.Lock()
	sub := s.submenuComponent
	s.mu.Unlock()
	if f, ok := sub.(interface{ WantsKeyRelease() bool }); ok {
		return f.WantsKeyRelease()
	}
	return false
}

// CapturesViewportInput reports whether this list captures alt-screen viewport keys when focused.
func (s *SettingsList) CapturesViewportInput() bool { return true }

func (s *SettingsList) renderMainList(width int) []string {
	var lines []string

	if s.searchEnabled && s.searchInput != nil {
		lines = append(lines, s.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	if len(s.items) == 0 {
		lines = append(lines, s.theme.Hint("  No settings available"))
		if s.searchEnabled {
			lines = s.addHintLine(lines, width)
		}
		return lines
	}

	displayItems := s.getDisplayItems()
	if len(displayItems) == 0 {
		lines = append(lines, TruncateToWidth(s.theme.Hint("  No matching settings"), max(0, width), "...", false))
		lines = s.addHintLine(lines, width)
		return lines
	}

	startIndex, endIndex := s.getVisibleRange(displayItems)

	maxLabelWidth := 0
	for _, item := range s.items {
		if w := VisibleWidth(item.Label); w > maxLabelWidth {
			maxLabelWidth = w
		}
	}
	if maxLabelWidth > 36 {
		maxLabelWidth = 36
	}

	for i := startIndex; i < endIndex; i++ {
		item := displayItems[i]
		isSelected := i == s.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = s.theme.Cursor
		}
		prefixWidth := VisibleWidth(prefix)

		padCount := maxLabelWidth - VisibleWidth(item.Label)
		if padCount < 0 {
			padCount = 0
		}
		labelPadded := item.Label + strings.Repeat(" ", padCount)
		labelText := s.theme.Label(labelPadded, isSelected)

		separator := "  "
		usedWidth := prefixWidth + maxLabelWidth + VisibleWidth(separator)
		valueMaxWidth := width - usedWidth - 2
		if valueMaxWidth < 0 {
			valueMaxWidth = 0
		}
		valueText := s.theme.Value(TruncateToWidth(item.CurrentValue, valueMaxWidth, "", false), isSelected)
		lines = append(lines, TruncateToWidth(prefix+labelText+separator+valueText, max(0, width), "...", false))
	}

	if startIndex > 0 || endIndex < len(displayItems) {
		scrollText := fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(displayItems))
		scrollMax := width - 2
		if scrollMax < 0 {
			scrollMax = 0
		}
		lines = append(lines, s.theme.Hint(TruncateToWidth(scrollText, scrollMax, "", false)))
	}

	if s.selectedIndex >= 0 && s.selectedIndex < len(displayItems) {
		selectedItem := displayItems[s.selectedIndex]
		if selectedItem.Description != nil && *selectedItem.Description != "" {
			lines = append(lines, "")
			descWidth := width - 4
			if descWidth < 1 {
				descWidth = 1
			}
			wrappedDesc := WrapTextWithAnsi(*selectedItem.Description, descWidth)
			for _, line := range wrappedDesc {
				lines = append(lines, s.theme.Description("  "+line))
			}
		}
	}

	lines = s.addHintLine(lines, width)
	return lines
}

func (s *SettingsList) getDisplayItems() []SettingItem {
	if s.searchEnabled {
		return s.filteredItems
	}
	return s.items
}

func (s *SettingsList) getVisibleRange(displayItems []SettingItem) (startIndex int, endIndex int) {
	maxStart := len(displayItems) - s.maxVisible
	centeredStart := s.selectedIndex - s.maxVisible/2
	if centeredStart < maxStart {
		maxStart = centeredStart
	}
	if maxStart < 0 {
		maxStart = 0
	}
	startIndex = maxStart
	endIndex = startIndex + s.maxVisible
	if endIndex > len(displayItems) {
		endIndex = len(displayItems)
	}
	return startIndex, endIndex
}

func (s *SettingsList) activateItem() {
	s.mu.Lock()
	displayItems := s.getDisplayItems()
	if s.selectedIndex < 0 || s.selectedIndex >= len(displayItems) {
		s.mu.Unlock()
		return
	}
	item := displayItems[s.selectedIndex]
	if item.Submenu != nil {
		idx := s.selectedIndex
		s.submenuItemIndex = &idx
		itemId := item.Id
		currentValue := item.CurrentValue
		submenuFn := item.Submenu
		doneCalled := false
		s.mu.Unlock()

		comp := submenuFn(currentValue, func(selectedValue *string, options *SettingsListSubmenuOptions) {
			var onChangeFn func(string, string)
			s.mu.Lock()
			doneCalled = true
			if selectedValue != nil {
				s.updateValueLocked(itemId, *selectedValue)
				onChangeFn = s.onChange
			}
			if options != nil && options.NavigateTo != nil {
				nav := *options.NavigateTo
				s.navigateAfterClose = &nav
			}
			s.mu.Unlock()
			if selectedValue != nil && onChangeFn != nil {
				onChangeFn(itemId, *selectedValue)
			}
			s.closeSubmenu()
		})

		s.mu.Lock()
		if !doneCalled {
			s.submenuComponent = comp
		}
		s.mu.Unlock()
		return
	}

	if len(item.Values) > 0 {
		currentIndex := -1
		for i, v := range item.Values {
			if v == item.CurrentValue {
				currentIndex = i
				break
			}
		}
		nextIndex := (currentIndex + 1) % len(item.Values)
		newValue := item.Values[nextIndex]
		s.updateValueLocked(item.Id, newValue)
		onChangeFn := s.onChange
		itemId := item.Id
		s.mu.Unlock()
		if onChangeFn != nil {
			onChangeFn(itemId, newValue)
		}
		return
	}

	s.mu.Unlock()
}

func (s *SettingsList) closeSubmenu() {
	s.mu.Lock()
	s.submenuComponent = nil
	if s.navigateAfterClose != nil {
		id := *s.navigateAfterClose
		s.navigateAfterClose = nil
		s.submenuItemIndex = nil
		displayItems := s.getDisplayItems()
		for i, item := range displayItems {
			if item.Id == id {
				s.selectedIndex = i
				break
			}
		}
		s.mu.Unlock()
		s.activateItem()
		return
	}
	if s.submenuItemIndex != nil {
		s.selectedIndex = *s.submenuItemIndex
		s.submenuItemIndex = nil
	}
	s.mu.Unlock()
}

func (s *SettingsList) applyFilter(query string) {
	s.filteredItems = FuzzyFilter(s.items, query, func(item SettingItem) string {
		return item.Label
	})
	s.selectedIndex = 0
}

func (s *SettingsList) addHintLine(lines []string, width int) []string {
	lines = append(lines, "")
	hint := "  Enter/Space to change · Esc to cancel"
	if s.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	lines = append(lines, TruncateToWidth(s.theme.Hint(hint), max(0, width), "...", false))
	return lines
}

var _ Component = (*SettingsList)(nil)
