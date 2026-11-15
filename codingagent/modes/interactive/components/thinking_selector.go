// Ported from packages/coding-agent/src/modes/interactive/components/thinking-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func thkselIntPtr(v int) *int {
	return &v
}

func thkselStringPtr(v string) *string {
	return &v
}

var thkselSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: thkselIntPtr(12),
	MaxPrimaryColumnWidth: thkselIntPtr(32),
}

var thkselLevelDescriptions = map[ai.ModelThinkingLevel]string{
	ai.ModelThinkingLevelOff:     "No reasoning",
	ai.ModelThinkingLevelMinimal: "Very brief reasoning (~1k tokens)",
	ai.ModelThinkingLevelLow:     "Light reasoning (~2k tokens)",
	ai.ModelThinkingLevelMedium:  "Moderate reasoning (~8k tokens)",
	ai.ModelThinkingLevelHigh:    "Deep reasoning (~16k tokens)",
	ai.ModelThinkingLevelXhigh:   "Extra-high reasoning (~32k tokens)",
	ai.ModelThinkingLevelMax:     "Maximum reasoning",
}

// ThinkingSelectorComponent renders a searchable bordered thinking-level picker.
type ThinkingSelectorComponent struct {
	*tui.Container
	searchInput          *tui.Input
	selectList           *tui.SelectList
	selectListChildIndex int
	allItems             []tui.SelectItem
	onSelect             func(level ai.ModelThinkingLevel)
	onCancel             func()
	onSelectAsDefault    func(level ai.ModelThinkingLevel)
	focused              bool
}

var _ tui.Focusable = (*ThinkingSelectorComponent)(nil)

// NewThinkingSelectorComponent constructs a thinking-level selector with optional default-save support.
func NewThinkingSelectorComponent(
	currentLevel ai.ModelThinkingLevel,
	availableLevels []ai.ModelThinkingLevel,
	onSelect func(level ai.ModelThinkingLevel),
	onCancel func(),
	onSelectAsDefault func(level ai.ModelThinkingLevel),
	defaultThinkingLevel ...ai.ModelThinkingLevel,
) *ThinkingSelectorComponent {
	var defaultLevel ai.ModelThinkingLevel
	if len(defaultThinkingLevel) > 0 {
		defaultLevel = defaultThinkingLevel[0]
	}

	items := make([]tui.SelectItem, len(availableLevels))
	for i, level := range availableLevels {
		prefix := "  "
		if level == currentLevel {
			prefix = "✓ "
		}
		desc := thkselLevelDescriptions[level]
		if defaultLevel != "" && level == defaultLevel {
			if desc != "" {
				desc += " · default"
			} else {
				desc = "default"
			}
		}
		items[i] = tui.SelectItem{
			Value:       string(level),
			Label:       prefix + string(level),
			Description: thkselStringPtr(desc),
		}
	}

	th := theme.CurrentTheme()
	c := &ThinkingSelectorComponent{
		Container:         tui.NewContainer(),
		allItems:          items,
		onSelect:          onSelect,
		onCancel:          onCancel,
		onSelectAsDefault: onSelectAsDefault,
	}

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText("Thinking Level", 0, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(theme.KeyDisplayText(core.KeybindingAppThinkingCycle)+" cycles thinking levels in-session", 0, 0, nil))
	c.AddChild(tui.NewSpacer(1))

	c.searchInput = tui.NewInput(nil)
	c.searchInput.OnSubmit = func(string) {
		c.selectList.HandleInput("\r")
	}
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))

	c.selectList = c.buildSelectList(c.allItems, currentLevel)
	c.selectListChildIndex = len(c.Children())
	c.AddChild(c.selectList)
	c.AddChild(tui.NewSpacer(1))

	hint := fmt.Sprintf(
		"  %s to select · %s to set as default · %s to cancel",
		theme.KeyDisplayText(tui.KeybindingTuiSelectConfirm),
		theme.KeyDisplayText(core.KeybindingAppThinkingSave),
		theme.KeyDisplayText(tui.KeybindingTuiSelectCancel),
	)
	c.AddChild(tui.NewText(th.Fg(theme.FgDim, hint), 0, 0, nil))
	c.AddChild(NewDynamicBorder())
	return c
}

// Focused reports whether the search input currently holds focus.
func (t *ThinkingSelectorComponent) Focused() bool {
	return t.focused
}

// SetFocused propagates focus state to the embedded search input.
func (t *ThinkingSelectorComponent) SetFocused(focused bool) {
	t.focused = focused
	t.searchInput.SetFocused(focused)
}

// CapturesViewportInput reports that this selector intercepts viewport scroll keys.
func (t *ThinkingSelectorComponent) CapturesViewportInput() bool {
	return true
}

// GetSelectList returns the active SelectList child.
func (t *ThinkingSelectorComponent) GetSelectList() *tui.SelectList {
	return t.selectList
}

func (t *ThinkingSelectorComponent) buildSelectList(items []tui.SelectItem, preselect ai.ModelThinkingLevel) *tui.SelectList {
	list := tui.NewSelectList(items, max(1, len(items)), theme.GetSelectListTheme(), thkselSelectListLayout)
	if preselect != "" {
		if idx := slices.IndexFunc(items, func(item tui.SelectItem) bool {
			return item.Value == string(preselect)
		}); idx != -1 {
			list.SetSelectedIndex(idx)
		}
	}
	list.OnSelect = func(item tui.SelectItem) {
		if t.onSelect != nil {
			t.onSelect(ai.ModelThinkingLevel(item.Value))
		}
	}
	list.OnCancel = func() {
		if t.onCancel != nil {
			t.onCancel()
		}
	}
	return list
}

func (t *ThinkingSelectorComponent) applyFilter(query string) {
	filtered := t.allItems
	if query != "" {
		filtered = tui.FuzzyFilter(t.allItems, query, func(item tui.SelectItem) string {
			desc := ""
			if item.Description != nil {
				desc = *item.Description
			}
			return item.Value + " " + desc
		})
	}
	var preselect ai.ModelThinkingLevel
	if selected := t.selectList.GetSelectedItem(); selected != nil {
		preselect = ai.ModelThinkingLevel(selected.Value)
	}
	newList := t.buildSelectList(filtered, preselect)
	children := t.Children()
	children[t.selectListChildIndex] = newList
	t.SetChildren(children)
	t.selectList = newList
}

// HandleInput processes default-save shortcuts, selection navigation, and search filtering.
func (t *ThinkingSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, core.KeybindingAppThinkingSave) && t.onSelectAsDefault != nil {
		if item := t.selectList.GetSelectedItem(); item != nil {
			t.onSelectAsDefault(ai.ModelThinkingLevel(item.Value))
		}
		return
	}

	isNav := kb.Matches(keyData, tui.KeybindingTuiSelectUp) ||
		kb.Matches(keyData, tui.KeybindingTuiSelectDown) ||
		kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) ||
		kb.Matches(keyData, tui.KeybindingTuiSelectCancel)
	if isNav {
		t.selectList.HandleInput(keyData)
		return
	}

	t.searchInput.HandleInput(keyData)
	t.applyFilter(t.searchInput.GetValue())
}
