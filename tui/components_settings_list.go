// Ported from packages/tui/src/components/settings-list.ts (pi v1.0.0).

package tui

import "sync"

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
	panic("unported: NewSettingsList")
}

// UpdateValue sets the current value of the item with id. Unknown ids are ignored.
func (s *SettingsList) UpdateValue(id string, newValue string) {
	panic("unported: SettingsList.UpdateValue")
}

// SelectItem moves the selection to id. Unknown ids are ignored.
func (s *SettingsList) SelectItem(id string) {
	panic("unported: SettingsList.SelectItem")
}

// Invalidate invalidates the open submenu, if any.
func (s *SettingsList) Invalidate() {
	panic("unported: SettingsList.Invalidate")
}

// Render draws the submenu when one is open, otherwise the list.
func (s *SettingsList) Render(width int) []string {
	panic("unported: SettingsList.Render")
}

// HandleMouse forwards to the submenu or search input, or selects a row.
func (s *SettingsList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: SettingsList.HandleMouse")
}

// HandleInput forwards to the submenu or edits the list and search query.
func (s *SettingsList) HandleInput(data string) {
	panic("unported: SettingsList.HandleInput")
}

// WantsKeyRelease reports whether key-release events should be delivered.
// SettingsList does not request them.
func (s *SettingsList) WantsKeyRelease() bool { return false }

func (s *SettingsList) renderMainList(width int) []string {
	panic("unported: SettingsList.renderMainList")
}

func (s *SettingsList) getDisplayItems() []SettingItem {
	panic("unported: SettingsList.getDisplayItems")
}

func (s *SettingsList) getVisibleRange(displayItems []SettingItem) (startIndex int, endIndex int) {
	panic("unported: SettingsList.getVisibleRange")
}

func (s *SettingsList) activateItem() {
	panic("unported: SettingsList.activateItem")
}

func (s *SettingsList) closeSubmenu() {
	panic("unported: SettingsList.closeSubmenu")
}

func (s *SettingsList) applyFilter(query string) {
	panic("unported: SettingsList.applyFilter")
}

func (s *SettingsList) addHintLine(lines []string, width int) []string {
	panic("unported: SettingsList.addHintLine")
}

var _ Component = (*SettingsList)(nil)
