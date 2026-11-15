// Ported from packages/tui/src/components/select-list.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

var cslNewlineRunRegex = jsre.MustCompile(`[\r\n]+`, "g")

func cslBoolPtr(v bool) *bool {
	return &v
}

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
	itemsCopy := make([]SelectItem, len(items))
	copy(itemsCopy, items)
	filteredCopy := make([]SelectItem, len(items))
	copy(filteredCopy, items)
	layoutOpts := SelectListLayoutOptions{}
	if layout != nil {
		layoutOpts = *layout
	}
	return &SelectList{
		items:         itemsCopy,
		filteredItems: filteredCopy,
		maxVisible:    maxVisible,
		theme:         theme,
		layout:        layoutOpts,
	}
}

// SetFilter keeps items whose value starts with filter, case-insensitively, and resets the selection.
func (l *SelectList) SetFilter(filter string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lower := strings.ToLower(filter)
	filtered := make([]SelectItem, 0, len(l.items))
	for _, item := range l.items {
		if strings.HasPrefix(strings.ToLower(item.Value), lower) {
			filtered = append(filtered, item)
		}
	}
	l.filteredItems = filtered
	l.selectedIndex = 0
}

// SetSelectedIndex clamps index into the filtered items.
func (l *SelectList) SetSelectedIndex(index int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.filteredItems) == 0 {
		l.selectedIndex = 0
		return
	}
	l.selectedIndex = cslClamp(index, 0, len(l.filteredItems)-1)
}

// Invalidate drops cached render state. SelectList has none.
func (l *SelectList) Invalidate() {}

// Render draws the visible rows and the scroll indicator.
func (l *SelectList) Render(width int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.filteredItems) == 0 {
		return []string{l.theme.NoMatch("  No matching commands")}
	}

	startIndex, endIndex := l.getVisibleRange()
	primaryColumnWidth := l.getPrimaryColumnWidth()
	lines := make([]string, 0, endIndex-startIndex+1)
	for i := startIndex; i < endIndex; i++ {
		item := l.filteredItems[i]
		isSelected := i == l.selectedIndex
		var descSingleLine *string
		if item.Description != nil {
			normalized := cslNormalizeToSingleLine(*item.Description)
			if normalized != "" {
				descSingleLine = &normalized
			}
		}
		lines = append(lines, l.renderItem(item, isSelected, width, descSingleLine, primaryColumnWidth))
	}

	if startIndex > 0 || endIndex < len(l.filteredItems) {
		scrollText := fmt.Sprintf("  (%d/%d)", l.selectedIndex+1, len(l.filteredItems))
		lines = append(lines, l.theme.ScrollInfo(scrollText))
	}
	return lines
}

// HandleMouse scrolls on wheel and selects or confirms on left press and click.
func (l *SelectList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	l.mu.Lock()
	if len(l.filteredItems) == 0 {
		l.mu.Unlock()
		return nil
	}

	if event.Type == TuiMouseEventTypeWheel && event.WheelDelta != nil && *event.WheelDelta != 0 {
		delta := 1
		if *event.WheelDelta < 0 {
			delta = -1
		}
		prev := l.selectedIndex
		l.selectedIndex = cslClamp(l.selectedIndex+delta, 0, len(l.filteredItems)-1)
		changed := l.selectedIndex != prev
		selected := l.filteredItems[l.selectedIndex]
		onChange := l.OnSelectionChange
		l.mu.Unlock()
		if changed && onChange != nil {
			onChange(selected)
		}
		return &TuiMouseEventResult{
			Handled: cslBoolPtr(true),
			Render:  cslBoolPtr(changed),
		}
	}

	if event.Button != TuiMouseButtonLeft || (event.Type != TuiMouseEventTypePress && event.Type != TuiMouseEventTypeClick) {
		l.mu.Unlock()
		return nil
	}

	startIndex, endIndex := l.getVisibleRange()
	itemIndex := startIndex + event.Y
	if itemIndex < startIndex || itemIndex >= endIndex {
		l.mu.Unlock()
		return nil
	}

	if event.Type == TuiMouseEventTypePress {
		idx := itemIndex
		l.mousePressedIndex = &idx
		prev := l.selectedIndex
		l.selectedIndex = itemIndex
		changed := l.selectedIndex != prev
		selected := l.filteredItems[l.selectedIndex]
		onChange := l.OnSelectionChange
		l.mu.Unlock()
		if changed && onChange != nil {
			onChange(selected)
		}
		return &TuiMouseEventResult{
			Handled: cslBoolPtr(true),
			Focus:   cslBoolPtr(true),
		}
	}

	targetIndex := itemIndex
	if l.mousePressedIndex != nil {
		targetIndex = *l.mousePressedIndex
	}
	l.mousePressedIndex = nil
	l.selectedIndex = cslClamp(targetIndex, 0, len(l.filteredItems)-1)
	selected := l.filteredItems[l.selectedIndex]
	onSelect := l.OnSelect
	l.mu.Unlock()
	if onSelect != nil {
		onSelect(selected)
	}
	return &TuiMouseEventResult{
		Handled: cslBoolPtr(true),
	}
}

// HandleInput moves the selection, confirms, or cancels.
func (l *SelectList) HandleInput(keyData string) {
	kb := GetKeybindings()
	l.mu.Lock()

	if kb.Matches(keyData, KeybindingTuiSelectUp) {
		if len(l.filteredItems) == 0 {
			l.mu.Unlock()
			return
		}
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.filteredItems) - 1
		} else {
			l.selectedIndex--
		}
		selected := l.filteredItems[l.selectedIndex]
		onChange := l.OnSelectionChange
		l.mu.Unlock()
		if onChange != nil {
			onChange(selected)
		}
		return
	}

	if kb.Matches(keyData, KeybindingTuiSelectDown) {
		if len(l.filteredItems) == 0 {
			l.mu.Unlock()
			return
		}
		if l.selectedIndex == len(l.filteredItems)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
		selected := l.filteredItems[l.selectedIndex]
		onChange := l.OnSelectionChange
		l.mu.Unlock()
		if onChange != nil {
			onChange(selected)
		}
		return
	}

	if pageIndex := GetSelectPageIndex(kb, keyData, l.selectedIndex, len(l.filteredItems), l.maxVisible); pageIndex != nil {
		if len(l.filteredItems) == 0 || *pageIndex == l.selectedIndex {
			l.mu.Unlock()
			return
		}
		l.selectedIndex = *pageIndex
		selected := l.filteredItems[l.selectedIndex]
		onChange := l.OnSelectionChange
		l.mu.Unlock()
		if onChange != nil {
			onChange(selected)
		}
		return
	}

	if kb.Matches(keyData, KeybindingTuiSelectConfirm) {
		if len(l.filteredItems) == 0 || l.selectedIndex < 0 || l.selectedIndex >= len(l.filteredItems) {
			l.mu.Unlock()
			return
		}
		selected := l.filteredItems[l.selectedIndex]
		onSelect := l.OnSelect
		l.mu.Unlock()
		if onSelect != nil {
			onSelect(selected)
		}
		return
	}

	if kb.Matches(keyData, KeybindingTuiSelectCancel) {
		onCancel := l.OnCancel
		l.mu.Unlock()
		if onCancel != nil {
			onCancel()
		}
		return
	}

	l.mu.Unlock()
}

// GetSelectedItem returns the highlighted filtered item, or nil when there is none.
func (l *SelectList) GetSelectedItem() *SelectItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.filteredItems) == 0 || l.selectedIndex < 0 || l.selectedIndex >= len(l.filteredItems) {
		return nil
	}
	item := l.filteredItems[l.selectedIndex]
	return &item
}

// WantsKeyRelease reports whether key-release events should be delivered.
// SelectList does not request them.
func (l *SelectList) WantsKeyRelease() bool { return false }

// CapturesViewportInput reports whether this list captures alt-screen viewport keys when focused.
func (l *SelectList) CapturesViewportInput() bool { return true }

func (l *SelectList) getVisibleRange() (startIndex int, endIndex int) {
	maxStart := len(l.filteredItems) - l.maxVisible
	centeredStart := l.selectedIndex - l.maxVisible/2
	if centeredStart < maxStart {
		maxStart = centeredStart
	}
	if maxStart < 0 {
		maxStart = 0
	}
	startIndex = maxStart
	endIndex = startIndex + l.maxVisible
	if endIndex > len(l.filteredItems) {
		endIndex = len(l.filteredItems)
	}
	return startIndex, endIndex
}

func (l *SelectList) renderItem(item SelectItem, isSelected bool, width int, descriptionSingleLine *string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = l.theme.SelectedPrefix("→ ")
	}
	prefixWidth := VisibleWidth(prefix)

	if descriptionSingleLine != nil && *descriptionSingleLine != "" {
		maxPrimaryCol := width - prefixWidth - cslMinDescriptionWidth
		if maxPrimaryCol >= cslPrimaryColumnGap+1 {
			colWidth := primaryColumnWidth
			if colWidth > maxPrimaryCol {
				colWidth = maxPrimaryCol
			}
			maxTextWidth := colWidth - cslPrimaryColumnGap
			if maxTextWidth < 1 {
				maxTextWidth = 1
			}
			truncatedPrimary := l.truncatePrimary(item, isSelected, maxTextWidth, colWidth)
			truncatedWidth := VisibleWidth(truncatedPrimary)
			spacingCount := colWidth - truncatedWidth
			if spacingCount < 1 {
				spacingCount = 1
			}
			spacing := strings.Repeat(" ", spacingCount)
			remainingWidth := width - prefixWidth - truncatedWidth - spacingCount
			if remainingWidth >= cslMinDescriptionWidth {
				descText := TruncateToWidth(*descriptionSingleLine, remainingWidth, "", false)
				if isSelected {
					return prefix + l.theme.SelectedText(truncatedPrimary+spacing+descText)
				}
				return prefix + truncatedPrimary + spacing + l.theme.Description(descText)
			}
		}
	}

	maxTitleWidth := width - prefixWidth
	if maxTitleWidth < 0 {
		maxTitleWidth = 0
	}
	displayValue := TruncateToWidth(l.getDisplayValue(item), maxTitleWidth, "...", false)
	if isSelected {
		return prefix + l.theme.SelectedText(displayValue)
	}
	return prefix + displayValue
}

func (l *SelectList) getPrimaryColumnWidth() int {
	minWidth, maxWidth := l.getPrimaryColumnBounds()
	longest := 0
	for _, item := range l.filteredItems {
		if w := VisibleWidth(l.getDisplayValue(item)); w > longest {
			longest = w
		}
	}
	return cslClamp(longest+cslPrimaryColumnGap, minWidth, maxWidth)
}

func (l *SelectList) getPrimaryColumnBounds() (minWidth int, maxWidth int) {
	maxWidth = cslDefaultPrimaryColumnWidth
	if l.layout.MaxPrimaryColumnWidth != nil {
		maxWidth = *l.layout.MaxPrimaryColumnWidth
		if maxWidth < 1 {
			maxWidth = 1
		}
	}
	minWidth = cslDefaultPrimaryColumnWidth
	if minWidth > maxWidth {
		minWidth = maxWidth
	}
	if l.layout.MinPrimaryColumnWidth != nil {
		minWidth = *l.layout.MinPrimaryColumnWidth
		if minWidth < 1 {
			minWidth = 1
		}
		if l.layout.MaxPrimaryColumnWidth == nil && minWidth > maxWidth {
			maxWidth = minWidth
		}
	}
	if minWidth > maxWidth {
		minWidth = maxWidth
	}
	return minWidth, maxWidth
}

func (l *SelectList) truncatePrimary(item SelectItem, isSelected bool, maxWidth int, columnWidth int) string {
	displayValue := l.getDisplayValue(item)
	if l.layout.TruncatePrimary != nil {
		return l.layout.TruncatePrimary(SelectListTruncatePrimaryContext{
			Text:        displayValue,
			MaxWidth:    maxWidth,
			ColumnWidth: columnWidth,
			Item:        item,
			IsSelected:  isSelected,
		})
	}
	return TruncateToWidth(displayValue, maxWidth, "...", false)
}

func (l *SelectList) getDisplayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func (l *SelectList) notifySelectionChange() {
	if len(l.filteredItems) == 0 || l.selectedIndex < 0 || l.selectedIndex >= len(l.filteredItems) {
		return
	}
	if l.OnSelectionChange != nil {
		l.OnSelectionChange(l.filteredItems[l.selectedIndex])
	}
}

func cslNormalizeToSingleLine(text string) string {
	replaced := cslNewlineRunRegex.Replace(text, " ")
	return js.Trim(replaced)
}

func cslClamp(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

var _ Component = (*SelectList)(nil)
