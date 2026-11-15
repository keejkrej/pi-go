// Ported from packages/coding-agent/src/modes/interactive/components/extension-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const extselMaxVisible = 10

// ExtensionSelectorOptions configures optional timeout, description, and initial selection for ExtensionSelectorComponent.
type ExtensionSelectorOptions struct {
	Tui                   tui.TUI
	Timeout               *int64
	InitialIndex          *int
	Description           *string
	OnToggleToolsExpanded func()
}

// ExtensionSelectorComponent displays a navigable option list for extension dialogs and /login menus.
type ExtensionSelectorComponent struct {
	*tui.Container
	options               []string
	selectedIndex         int
	listContainer         *tui.Container
	onSelectCallback      func(option string)
	onCancelCallback      func()
	titleText             *tui.Text
	baseTitle             string
	countdown             *CountdownTimer
	onToggleToolsExpanded func()

	renderOverride  func(width int, base func(int) []string) []string
	disposeOverride func()
}

// NewExtensionSelectorComponent constructs an extension option selector dialog.
func NewExtensionSelectorComponent(
	title string,
	options []string,
	onSelect func(option string),
	onCancel func(),
	opts ...*ExtensionSelectorOptions,
) *ExtensionSelectorComponent {
	var cfg *ExtensionSelectorOptions
	if len(opts) > 0 {
		cfg = opts[0]
	}

	copied := make([]string, len(options))
	copy(copied, options)

	selectedIndex := 0
	if cfg != nil && cfg.InitialIndex != nil && len(copied) > 0 {
		selectedIndex = max(0, min(len(copied)-1, *cfg.InitialIndex))
	}

	th := theme.CurrentTheme()
	c := &ExtensionSelectorComponent{
		Container:        tui.NewContainer(),
		options:          copied,
		selectedIndex:    selectedIndex,
		listContainer:    tui.NewContainer(),
		onSelectCallback: onSelect,
		onCancelCallback: onCancel,
		baseTitle:        title,
	}
	if cfg != nil {
		c.onToggleToolsExpanded = cfg.OnToggleToolsExpanded
	}

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	c.titleText = tui.NewText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0, nil)
	c.AddChild(c.titleText)
	if cfg != nil && cfg.Description != nil && *cfg.Description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(th.Fg(theme.FgText, *cfg.Description), 1, 0, nil))
	}
	c.AddChild(tui.NewSpacer(1))

	if cfg != nil && cfg.Timeout != nil && *cfg.Timeout > 0 && cfg.Tui != nil {
		c.countdown = NewCountdownTimer(
			*cfg.Timeout,
			cfg.Tui,
			func(s int) {
				cur := theme.CurrentTheme()
				c.titleText.SetText(cur.Fg(theme.FgAccent, cur.Bold(fmt.Sprintf("%s (%ds)", c.baseTitle, s))))
			},
			func() {
				if c.onCancelCallback != nil {
					c.onCancelCallback()
				}
			},
		)
	}

	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(
		theme.RawKeyHint(th, "↑↓", "navigate")+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "select")+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel"),
		1,
		0,
		nil,
	))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())

	c.updateList()
	return c
}

func (e *ExtensionSelectorComponent) updateList() {
	th := theme.CurrentTheme()
	e.listContainer.Clear()

	startIndex := max(0, min(e.selectedIndex-extselMaxVisible/2, len(e.options)-extselMaxVisible))
	endIndex := min(startIndex+extselMaxVisible, len(e.options))

	for i := startIndex; i < endIndex; i++ {
		var text string
		if i == e.selectedIndex {
			text = th.Fg(theme.FgAccent, "→ ") + th.Fg(theme.FgAccent, e.options[i])
		} else {
			text = "  " + th.Fg(theme.FgText, e.options[i])
		}
		e.listContainer.AddChild(tui.NewText(text, 1, 0, nil))
	}

	if startIndex > 0 || endIndex < len(e.options) {
		scrollInfo := th.Fg(theme.FgMuted, fmt.Sprintf("  (%d/%d)", e.selectedIndex+1, len(e.options)))
		e.listContainer.AddChild(tui.NewText(scrollInfo, 0, 0, nil))
	}
}

// Render renders the selector dialog, invoking any registered subclass render hook.
func (e *ExtensionSelectorComponent) Render(width int) []string {
	if e.renderOverride != nil {
		return e.renderOverride(width, e.Container.Render)
	}
	return e.Container.Render(width)
}

// HandleInput processes selection navigation, tool expansion, confirm, and cancel keys.
func (e *ExtensionSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, core.KeybindingAppToolsExpand):
		if e.onToggleToolsExpanded != nil {
			e.onToggleToolsExpanded()
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp) || keyData == "k":
		e.selectedIndex = max(0, e.selectedIndex-1)
		e.updateList()
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown) || keyData == "j":
		if len(e.options) > 0 {
			e.selectedIndex = min(len(e.options)-1, e.selectedIndex+1)
			e.updateList()
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) || keyData == "\n":
		if e.selectedIndex >= 0 && e.selectedIndex < len(e.options) && e.onSelectCallback != nil {
			e.onSelectCallback(e.options[e.selectedIndex])
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		if e.onCancelCallback != nil {
			e.onCancelCallback()
		}
	}
}

// Dispose stops any active countdown timer and invokes any registered subclass cleanup hook.
func (e *ExtensionSelectorComponent) Dispose() {
	if e.disposeOverride != nil {
		e.disposeOverride()
	}
	e.disposeBase()
}

func (e *ExtensionSelectorComponent) disposeBase() {
	if e.countdown != nil {
		e.countdown.Dispose()
		e.countdown = nil
	}
	_ = strings.Builder{}
}
