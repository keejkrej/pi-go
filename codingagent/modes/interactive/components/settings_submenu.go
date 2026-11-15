// Ported from packages/coding-agent/src/modes/interactive/components/settings-submenu.ts (pi v1.5.2).

package components

import (
	"fmt"
	"maps"
	"slices"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func ssuIntPtr(v int) *int {
	return &v
}

var ssuSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: ssuIntPtr(12),
	MaxPrimaryColumnWidth: ssuIntPtr(32),
}

// SelectSubmenuOptions configures a SelectSubmenu.
type SelectSubmenuOptions struct {
	// Searchable enables type-to-search fuzzy filtering.
	Searchable bool
	// Layout overrides the select list column widths.
	Layout *tui.SelectListLayoutOptions
}

// SelectSubmenu renders a single-step titled select list submenu.
// With Searchable enabled, typing filters the list using fuzzy matching.
type SelectSubmenu struct {
	*tui.Container
	selectList          *tui.SelectList
	listChildIndex      int
	allOptions          []tui.SelectItem
	listLayout          *tui.SelectListLayoutOptions
	searchInput         *tui.Input
	onSelectCb          func(value string)
	onCancelCb          func()
	onSelectionChangeCb func(value string)
}

// NewSelectSubmenu constructs a single-step submenu for choosing one option.
func NewSelectSubmenu(
	title string,
	description string,
	options []tui.SelectItem,
	currentValue string,
	onSelect func(value string),
	onCancel func(),
	onSelectionChange func(value string),
	submenuOptions ...*SelectSubmenuOptions,
) *SelectSubmenu {
	searchable := false
	layout := ssuSelectListLayout
	if len(submenuOptions) > 0 && submenuOptions[0] != nil {
		searchable = submenuOptions[0].Searchable
		if submenuOptions[0].Layout != nil {
			layout = submenuOptions[0].Layout
		}
	}

	s := &SelectSubmenu{
		Container:           tui.NewContainer(),
		allOptions:          slices.Clone(options),
		listLayout:          layout,
		onSelectCb:          onSelect,
		onCancelCb:          onCancel,
		onSelectionChangeCb: onSelectionChange,
	}

	t := theme.CurrentTheme()
	s.AddChild(tui.NewText(t.Bold(t.Fg(theme.FgAccent, title)), 0, 0, nil))

	if description != "" {
		s.AddChild(tui.NewSpacer(1))
		s.AddChild(tui.NewText(t.Fg(theme.FgMuted, description), 0, 0, nil))
	}

	if searchable {
		s.AddChild(tui.NewSpacer(1))
		s.searchInput = tui.NewInput(nil)
		s.searchInput.OnSubmit = func(string) {
			s.selectList.HandleInput("\r")
		}
		s.AddChild(s.searchInput)
	}

	s.AddChild(tui.NewSpacer(1))

	s.selectList = s.buildSelectList(s.allOptions, currentValue)
	s.listChildIndex = len(s.Children())
	s.AddChild(s.selectList)

	s.AddChild(tui.NewSpacer(1))
	hint := "  Enter to select \u00b7 Esc to go back"
	if searchable {
		hint = "  Type to filter \u00b7 Enter to select \u00b7 Esc to go back"
	}
	s.AddChild(tui.NewText(t.Fg(theme.FgDim, hint), 0, 0, nil))

	return s
}

func (s *SelectSubmenu) buildSelectList(options []tui.SelectItem, preselect string) *tui.SelectList {
	list := tui.NewSelectList(options, min(len(options), 10), theme.GetSelectListTheme(), s.listLayout)

	if idx := slices.IndexFunc(options, func(item tui.SelectItem) bool {
		return item.Value == preselect
	}); idx != -1 {
		list.SetSelectedIndex(idx)
	}

	list.OnSelect = func(item tui.SelectItem) {
		if s.onSelectCb != nil {
			s.onSelectCb(item.Value)
		}
	}
	list.OnCancel = func() {
		if s.onCancelCb != nil {
			s.onCancelCb()
		}
	}
	if s.onSelectionChangeCb != nil {
		cb := s.onSelectionChangeCb
		list.OnSelectionChange = func(item tui.SelectItem) {
			cb(item.Value)
		}
	}

	return list
}

func (s *SelectSubmenu) applyFilter(query string) {
	filtered := s.allOptions
	if query != "" {
		filtered = tui.FuzzyFilter(s.allOptions, query, func(item tui.SelectItem) string {
			if item.Description != nil && *item.Description != "" {
				return item.Label + " " + *item.Description
			}
			return item.Label + " "
		})
	}

	newList := s.buildSelectList(filtered, "")
	children := slices.Clone(s.Children())
	children[s.listChildIndex] = newList
	s.SetChildren(children)
	s.selectList = newList
}

// HandleInput forwards navigation keys to the select list or updates the search filter when searchable.
func (s *SelectSubmenu) HandleInput(data string) {
	if s.searchInput != nil {
		kb := tui.GetKeybindings()
		isNav := kb.Matches(data, tui.KeybindingTuiSelectUp) ||
			kb.Matches(data, tui.KeybindingTuiSelectDown) ||
			kb.Matches(data, tui.KeybindingTuiSelectPageUp) ||
			kb.Matches(data, tui.KeybindingTuiSelectPageDown) ||
			kb.Matches(data, tui.KeybindingTuiSelectConfirm) ||
			kb.Matches(data, tui.KeybindingTuiSelectCancel)
		if isNav {
			s.selectList.HandleInput(data)
		} else {
			s.searchInput.HandleInput(data)
			s.applyFilter(s.searchInput.GetValue())
		}
		return
	}
	s.selectList.HandleInput(data)
}

// SteppedSubmenuStep defines one step in a SteppedSubmenu.
type SteppedSubmenuStep struct {
	// Key stores the selected value in the result context map.
	Key string
	// Title sets a static step heading (used when TitleFn is nil).
	Title string
	// TitleFn computes a dynamic step heading from prior selections.
	TitleFn func(context map[string]string) string
	// Description sets a static description (used when DescriptionFn is nil).
	Description string
	// DescriptionFn computes a dynamic description from prior selections.
	DescriptionFn func(context map[string]string) string
	// Options builds the items shown in this step given prior selections.
	Options func(context map[string]string) []tui.SelectItem
	// Preselect optionally returns a value to preselect when entering this step.
	Preselect func(context map[string]string) *string
	// Searchable enables fuzzy filtering for this step.
	Searchable bool
	// Layout optionally overrides select list column widths for this step.
	Layout *tui.SelectListLayoutOptions
}

// SteppedSubmenuOptions configures initial state and loop behavior for SteppedSubmenu.
type SteppedSubmenuOptions struct {
	StartAtStep    int
	InitialContext map[string]string
	Loop           bool
}

// SteppedSubmenu renders a multi-step submenu built on SelectSubmenu.
type SteppedSubmenu struct {
	*tui.Container
	steps           []SteppedSubmenuStep
	onComplete      func(context map[string]string)
	onCancel        func()
	opts            SteppedSubmenuOptions
	activeComponent tui.Component
	context         map[string]string
}

// NewSteppedSubmenu constructs a multi-step submenu starting at StartAtStep (0 by default).
func NewSteppedSubmenu(
	steps []SteppedSubmenuStep,
	onComplete func(context map[string]string),
	onCancel func(),
	opts ...*SteppedSubmenuOptions,
) *SteppedSubmenu {
	var resolved SteppedSubmenuOptions
	if len(opts) > 0 && opts[0] != nil {
		resolved = *opts[0]
	}

	ctx := make(map[string]string, len(resolved.InitialContext))
	maps.Copy(ctx, resolved.InitialContext)

	s := &SteppedSubmenu{
		Container:  tui.NewContainer(),
		steps:      slices.Clone(steps),
		onComplete: onComplete,
		onCancel:   onCancel,
		opts:       resolved,
		context:    ctx,
	}
	if len(s.steps) > 0 {
		start := max(0, min(resolved.StartAtStep, len(s.steps)-1))
		s.activeComponent = s.buildStep(start)
	}
	return s
}

func (s *SteppedSubmenu) buildStep(stepIndex int) *SelectSubmenu {
	step := s.steps[stepIndex]
	total := len(s.steps)
	stepLabel := ""
	if total > 1 {
		stepLabel = fmt.Sprintf("Step %d/%d \u00b7 ", stepIndex+1, total)
	}

	title := step.Title
	if step.TitleFn != nil {
		title = step.TitleFn(s.context)
	}
	desc := step.Description
	if step.DescriptionFn != nil {
		desc = step.DescriptionFn(s.context)
	}
	var items []tui.SelectItem
	if step.Options != nil {
		items = step.Options(s.context)
	}
	preselect := ""
	if step.Preselect != nil {
		if val := step.Preselect(s.context); val != nil {
			preselect = *val
		}
	}

	var submenuOpts *SelectSubmenuOptions
	if step.Searchable || step.Layout != nil {
		submenuOpts = &SelectSubmenuOptions{
			Searchable: step.Searchable,
			Layout:     step.Layout,
		}
	}

	return NewSelectSubmenu(
		title,
		stepLabel+desc,
		items,
		preselect,
		func(value string) {
			s.context[step.Key] = value
			if stepIndex < total-1 {
				s.activeComponent = s.buildStep(stepIndex + 1)
				return
			}
			if s.onComplete != nil {
				s.onComplete(maps.Clone(s.context))
			}
			if s.opts.Loop {
				s.context = make(map[string]string)
				s.activeComponent = s.buildStep(0)
			} else if s.onCancel != nil {
				s.onCancel()
			}
		},
		func() {
			if stepIndex > 0 {
				delete(s.context, step.Key)
				s.activeComponent = s.buildStep(stepIndex - 1)
			} else if s.onCancel != nil {
				s.onCancel()
			}
		},
		nil,
		submenuOpts,
	)
}

// Render renders the active step submenu.
func (s *SteppedSubmenu) Render(width int) []string {
	if s.activeComponent == nil {
		return nil
	}
	return s.activeComponent.Render(width)
}

// HandleInput forwards terminal input to the active step submenu.
func (s *SteppedSubmenu) HandleInput(data string) {
	if handler, ok := s.activeComponent.(interface{ HandleInput(string) }); ok {
		handler.HandleInput(data)
	}
}

// Invalidate clears cached state on the active step submenu.
func (s *SteppedSubmenu) Invalidate() {
	if s.activeComponent != nil {
		s.activeComponent.Invalidate()
	}
}
