// Ported from packages/coding-agent/src/modes/interactive/components/trust-selector.ts (pi v1.5.2).

package components

import (
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// TrustSelection represents the chosen project trust outcome and trust.json updates.
type TrustSelection struct {
	Trusted bool
	Updates []core.ProjectTrustUpdate
}

// TrustSelectorOptions configures a TrustSelectorComponent.
type TrustSelectorOptions struct {
	Cwd            string
	SavedDecision  *core.ProjectTrustStoreEntry
	ProjectTrusted bool
	OnSelect       func(selection TrustSelection)
	OnCancel       func()
}

func trstselFormatDecision(trustPath *string, decision *core.ProjectTrustStoreEntry) string {
	if decision == nil {
		return "none"
	}
	label := "untrusted"
	if decision.Decision {
		label = "trusted"
	}
	if trustPath != nil && decision.Path != *trustPath {
		return fmt.Sprintf("%s (inherited from %s)", label, decision.Path)
	}
	return fmt.Sprintf("%s (%s)", label, decision.Path)
}

// TrustSelectorComponent renders an interactive project trust decision dialog.
type TrustSelectorComponent struct {
	*tui.Container
	selectedIndex    int
	listContainer    *tui.Container
	trustOptions     []core.ProjectTrustOption
	savedDecision    *core.ProjectTrustStoreEntry
	onSelectCallback func(selection TrustSelection)
	onCancelCallback func()
}

// NewTrustSelectorComponent constructs a project trust selector for options.Cwd.
func NewTrustSelectorComponent(options TrustSelectorOptions) *TrustSelectorComponent {
	trustOptions := core.GetProjectTrustOptions(options.Cwd)
	c := &TrustSelectorComponent{
		Container:        tui.NewContainer(),
		listContainer:    tui.NewContainer(),
		trustOptions:     trustOptions,
		savedDecision:    options.SavedDecision,
		onSelectCallback: options.OnSelect,
		onCancelCallback: options.OnCancel,
	}

	savedIdx := -1
	for i, opt := range trustOptions {
		if c.isSavedOption(opt) {
			savedIdx = i
			break
		}
	}
	c.selectedIndex = max(0, savedIdx)

	th := theme.CurrentTheme()
	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("Project trust")), 1, 0, nil))
	c.AddChild(tui.NewText(th.Fg(theme.FgMuted, options.Cwd), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))

	var trustPath *string
	if len(trustOptions) > 0 {
		trustPath = trustOptions[0].SavedPath
	}
	c.AddChild(tui.NewText(th.Fg(theme.FgMuted, "Saved decision: "+trstselFormatDecision(trustPath, options.SavedDecision)), 1, 0, nil))

	sessionStatus := "untrusted"
	if options.ProjectTrusted {
		sessionStatus = "trusted"
	}
	c.AddChild(tui.NewText(th.Fg(theme.FgMuted, "Current session: "+sessionStatus), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))

	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(
		theme.RawKeyHint(th, "↑↓", "navigate")+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "save")+
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

func (t *TrustSelectorComponent) isSavedOption(option core.ProjectTrustOption) bool {
	return option.SavedPath != nil &&
		t.savedDecision != nil &&
		t.savedDecision.Decision == option.Trusted &&
		t.savedDecision.Path == *option.SavedPath
}

func (t *TrustSelectorComponent) updateList() {
	th := theme.CurrentTheme()
	t.listContainer.Clear()
	for i, option := range t.trustOptions {
		isSelected := i == t.selectedIndex
		isCurrent := t.isSavedOption(option)

		currentMarker := "  "
		if isCurrent {
			currentMarker = th.Fg(theme.FgAccent, "✓ ")
		}
		prefix := "  "
		if isSelected {
			prefix = th.Fg(theme.FgAccent, "→ ")
		}
		label := th.Fg(theme.FgText, option.Label)
		if isSelected {
			label = th.Fg(theme.FgAccent, option.Label)
		}
		t.listContainer.AddChild(tui.NewText(prefix+currentMarker+label, 1, 0, nil))
	}
}

// HandleInput processes up/down navigation, confirm, and cancel keys.
func (t *TrustSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp) || keyData == "k":
		t.selectedIndex = max(0, t.selectedIndex-1)
		t.updateList()
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown) || keyData == "j":
		if len(t.trustOptions) > 0 {
			t.selectedIndex = min(len(t.trustOptions)-1, t.selectedIndex+1)
			t.updateList()
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) || keyData == "\n":
		if t.selectedIndex >= 0 && t.selectedIndex < len(t.trustOptions) && t.onSelectCallback != nil {
			selected := t.trustOptions[t.selectedIndex]
			t.onSelectCallback(TrustSelection{
				Trusted: selected.Trusted,
				Updates: selected.Updates,
			})
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		if t.onCancelCallback != nil {
			t.onCancelCallback()
		}
	}
}
