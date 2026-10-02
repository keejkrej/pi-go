// Ported from packages/tui/src/components/select-list.ts (pi v1.0.0).

package tui

import "sync"

const (
	cslDefaultPrimaryColumnWidth = 32
	cslPrimaryColumnGap          = 2
	cslMinDescriptionWidth       = 10
)

// SelectItem is one row in a SelectList.
type SelectItem struct {
	Value       string
	Label       string
	Description *string
}

// SelectListTheme styles a SelectList.
type SelectListTheme struct {
	SelectedPrefix func(text string) string
	SelectedText   func(text string) string
	Description    func(text string) string
	ScrollInfo     func(text string) string
	NoMatch        func(text string) string
}

// SelectListTruncatePrimaryContext is the argument of SelectListLayoutOptions.TruncatePrimary.
type SelectListTruncatePrimaryContext struct {
	Text        string
	MaxWidth    int
	ColumnWidth int
	Item        SelectItem
	IsSelected  bool
}

// SelectListLayoutOptions tunes the primary column.
// A nil *SelectListLayoutOptions means the defaults.
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth *int
	MaxPrimaryColumnWidth *int
	TruncatePrimary       func(context SelectListTruncatePrimaryContext) string
}

// SelectList is a filterable single-column picker.
type SelectList struct {
	mu                sync.Mutex
	items             []SelectItem
	filteredItems     []SelectItem
	selectedIndex     int
	mousePressedIndex *int
	maxVisible        int
	theme             SelectListTheme
	layout            SelectListLayoutOptions
	// OnSelect is called with the chosen item. Nil skips the callback.
	OnSelect func(item SelectItem)
	// OnCancel is called on cancel. Nil skips the callback.
	OnCancel func()
	// OnSelectionChange is called when the highlighted row changes. Nil skips the callback.
	OnSelectionChange func(item SelectItem)
}

// NewSelectList returns a list showing items. Nil layout uses the defaults.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout *SelectListLayoutOptions) *SelectList {
	panic("unported: NewSelectList")
}

// SetFilter keeps items whose value starts with filter, case-insensitively, and resets the selection.
func (l *SelectList) SetFilter(filter string) {
	panic("unported: SelectList.SetFilter")
}

// SetSelectedIndex clamps index into the filtered items.
func (l *SelectList) SetSelectedIndex(index int) {
	panic("unported: SelectList.SetSelectedIndex")
}

// Invalidate drops cached render state. SelectList has none.
func (l *SelectList) Invalidate() {}

// Render draws the visible rows and the scroll indicator.
func (l *SelectList) Render(width int) []string {
	panic("unported: SelectList.Render")
}

// HandleMouse scrolls on wheel and selects or confirms on left press and click.
func (l *SelectList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: SelectList.HandleMouse")
}

// HandleInput moves the selection, confirms, or cancels.
func (l *SelectList) HandleInput(keyData string) {
	panic("unported: SelectList.HandleInput")
}

// GetSelectedItem returns the highlighted filtered item, or nil when there is none.
func (l *SelectList) GetSelectedItem() *SelectItem {
	panic("unported: SelectList.GetSelectedItem")
}

// WantsKeyRelease reports whether key-release events should be delivered.
// SelectList does not request them.
func (l *SelectList) WantsKeyRelease() bool { return false }

func (l *SelectList) getVisibleRange() (startIndex int, endIndex int) {
	panic("unported: SelectList.getVisibleRange")
}

func (l *SelectList) renderItem(item SelectItem, isSelected bool, width int, descriptionSingleLine *string, primaryColumnWidth int) string {
	panic("unported: SelectList.renderItem")
}

func (l *SelectList) getPrimaryColumnWidth() int {
	panic("unported: SelectList.getPrimaryColumnWidth")
}

func (l *SelectList) getPrimaryColumnBounds() (minWidth int, maxWidth int) {
	panic("unported: SelectList.getPrimaryColumnBounds")
}

func (l *SelectList) truncatePrimary(item SelectItem, isSelected bool, maxWidth int, columnWidth int) string {
	panic("unported: SelectList.truncatePrimary")
}

func (l *SelectList) getDisplayValue(item SelectItem) string {
	panic("unported: SelectList.getDisplayValue")
}

func (l *SelectList) notifySelectionChange() {
	panic("unported: SelectList.notifySelectionChange")
}

func cslNormalizeToSingleLine(text string) string {
	panic("unported: cslNormalizeToSingleLine")
}

func cslClamp(value int, min int, max int) int {
	panic("unported: cslClamp")
}

var _ Component = (*SelectList)(nil)
