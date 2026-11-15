// Ported from packages/coding-agent/src/modes/interactive/components/show-images-selector.ts (pi v1.5.2).

package components

import (
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func shimgIntPtr(v int) *int {
	return &v
}

func shimgStringPtr(v string) *string {
	return &v
}

var shimgSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: shimgIntPtr(12),
	MaxPrimaryColumnWidth: shimgIntPtr(32),
}

// ShowImagesSelectorComponent renders a bordered Yes/No selector for inline terminal images.
type ShowImagesSelectorComponent struct {
	*tui.Container
	selectList *tui.SelectList
}

// NewShowImagesSelectorComponent constructs a show-images selector preselecting currentValue.
func NewShowImagesSelectorComponent(
	currentValue bool,
	onSelect func(show bool),
	onCancel func(),
) *ShowImagesSelectorComponent {
	c := &ShowImagesSelectorComponent{
		Container: tui.NewContainer(),
	}

	items := []tui.SelectItem{
		{Value: "yes", Label: "Yes", Description: shimgStringPtr("Show images inline in terminal")},
		{Value: "no", Label: "No", Description: shimgStringPtr("Show text placeholder instead")},
	}

	c.AddChild(NewDynamicBorder())

	c.selectList = tui.NewSelectList(items, 5, theme.GetSelectListTheme(), shimgSelectListLayout)
	if currentValue {
		c.selectList.SetSelectedIndex(0)
	} else {
		c.selectList.SetSelectedIndex(1)
	}
	c.selectList.OnSelect = func(item tui.SelectItem) {
		if onSelect != nil {
			onSelect(item.Value == "yes")
		}
	}
	c.selectList.OnCancel = func() {
		if onCancel != nil {
			onCancel()
		}
	}

	c.AddChild(c.selectList)
	c.AddChild(NewDynamicBorder())
	return c
}

// GetSelectList returns the inner SelectList component.
func (s *ShowImagesSelectorComponent) GetSelectList() *tui.SelectList {
	return s.selectList
}
