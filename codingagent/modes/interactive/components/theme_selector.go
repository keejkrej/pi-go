// Ported from packages/coding-agent/src/modes/interactive/components/theme-selector.ts (pi v1.5.2).

package components

import (
	"slices"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func thmselIntPtr(v int) *int {
	return &v
}

func thmselStringPtr(v string) *string {
	return &v
}

var thmselSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: thmselIntPtr(12),
	MaxPrimaryColumnWidth: thmselIntPtr(32),
}

// ThemeSelectorComponent renders a bordered theme picker with live preview support.
type ThemeSelectorComponent struct {
	*tui.Container
	selectList *tui.SelectList
	onPreview  func(themeName string)
}

// NewThemeSelectorComponent constructs a theme selector preselecting currentTheme.
func NewThemeSelectorComponent(
	currentTheme string,
	onSelect func(themeName string),
	onCancel func(),
	onPreview ...func(themeName string),
) *ThemeSelectorComponent {
	var previewFn func(string)
	if len(onPreview) > 0 {
		previewFn = onPreview[0]
	}

	c := &ThemeSelectorComponent{
		Container: tui.NewContainer(),
		onPreview: previewFn,
	}

	themes := theme.GetAvailableThemes()
	themeItems := make([]tui.SelectItem, len(themes))
	for i, name := range themes {
		var desc *string
		if name == currentTheme {
			desc = thmselStringPtr("(current)")
		}
		themeItems[i] = tui.SelectItem{
			Value:       name,
			Label:       name,
			Description: desc,
		}
	}

	c.AddChild(NewDynamicBorder())

	c.selectList = tui.NewSelectList(themeItems, 10, theme.GetSelectListTheme(), thmselSelectListLayout)
	if currentIndex := slices.Index(themes, currentTheme); currentIndex != -1 {
		c.selectList.SetSelectedIndex(currentIndex)
	}
	c.selectList.OnSelect = func(item tui.SelectItem) {
		if onSelect != nil {
			onSelect(item.Value)
		}
	}
	c.selectList.OnCancel = func() {
		if onCancel != nil {
			onCancel()
		}
	}
	c.selectList.OnSelectionChange = func(item tui.SelectItem) {
		if c.onPreview != nil {
			c.onPreview(item.Value)
		}
	}

	c.AddChild(c.selectList)
	c.AddChild(NewDynamicBorder())
	return c
}

// GetSelectList returns the inner SelectList component.
func (t *ThemeSelectorComponent) GetSelectList() *tui.SelectList {
	return t.selectList
}
