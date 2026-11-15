// Ported from packages/coding-agent/src/modes/interactive/components/first-time-setup.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// FirstTimeSetupResult holds the theme and analytics opt-in choices submitted from setup.
type FirstTimeSetupResult struct {
	Theme          string
	ShareAnalytics bool
}

// FirstTimeSetupOptions configures callbacks for FirstTimeSetupComponent.
type FirstTimeSetupOptions struct {
	OnThemePreview func(themeName string)
	OnSubmit       func(result FirstTimeSetupResult)
	OnCancel       func()
}

type ftsetupStep string

const (
	ftsetupStepTheme     ftsetupStep = "theme"
	ftsetupStepAnalytics ftsetupStep = "analytics"
)

type ftsetupThemeOption struct {
	value string
	label string
}

type ftsetupAnalyticsOption struct {
	value bool
	label string
}

var (
	ftsetupThemeOptions = []ftsetupThemeOption{
		{value: theme.SystemThemeName, label: "System (matches your terminal colors)"},
		{value: "dark", label: "Dark"},
		{value: "light", label: "Light"},
	}
	ftsetupAnalyticsOptions = []ftsetupAnalyticsOption{
		{value: true, label: "Share anonymous usage data"},
		{value: false, label: "Don't share"},
	}
	ftsetupLogoLines = []string{"██████", "██  ██", "████  ██", "██    ██"}
)

// FirstTimeSetupComponent renders the interactive first-run theme and analytics setup wizard.
type FirstTimeSetupComponent struct {
	*tui.Container
	step           ftsetupStep
	themeIndex     int
	analyticsIndex int
	options        FirstTimeSetupOptions
}

// NewFirstTimeSetupComponent constructs a first-time setup dialog.
func NewFirstTimeSetupComponent(options FirstTimeSetupOptions) *FirstTimeSetupComponent {
	c := &FirstTimeSetupComponent{
		Container: tui.NewContainer(),
		step:      ftsetupStepTheme,
		options:   options,
	}
	c.update()
	return c
}

// Invalidate rebuilds the dialog on theme changes (e.g., system theme color updates) and invalidates children.
func (f *FirstTimeSetupComponent) Invalidate() {
	f.update()
	f.Container.Invalidate()
}

func (f *FirstTimeSetupComponent) update() {
	th := theme.CurrentTheme()
	f.Clear()
	f.AddChild(NewDynamicBorder())
	f.AddChild(tui.NewSpacer(1))
	f.AddChild(tui.NewText(th.Fg(theme.FgAccent, strings.Join(ftsetupLogoLines, "\n")), 1, 0, nil))
	f.AddChild(tui.NewSpacer(1))
	f.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold(fmt.Sprintf("Welcome to %s, the minimal coding agent.", utils.AppName))), 1, 0, nil))
	f.AddChild(tui.NewSpacer(1))

	if f.step == ftsetupStepTheme {
		f.AddChild(tui.NewText(th.Fg(theme.FgText, "Pick a theme."), 1, 0, nil))
		f.AddChild(tui.NewSpacer(1))
		labels := make([]string, len(ftsetupThemeOptions))
		for i, opt := range ftsetupThemeOptions {
			labels[i] = opt.label
		}
		f.addOptionList(labels, f.themeIndex)
	} else {
		f.AddChild(tui.NewText(th.Fg(theme.FgText, "Opt-in to anonymous usage data sharing?"), 1, 0, nil))
		f.AddChild(tui.NewText(
			th.Fg(
				theme.FgMuted,
				"Opting in stores a tracking identifier in settings.json and enables anonymous\nusage analytics. This helps us to better debug, reproduce, and resolve issues\nand bugs within Pi. You can observe what is shared using /privacy and make\nchanges anytime in settings.json.",
			),
			1,
			0,
			nil,
		))
		f.AddChild(tui.NewSpacer(1))
		labels := make([]string, len(ftsetupAnalyticsOptions))
		for i, opt := range ftsetupAnalyticsOptions {
			labels[i] = opt.label
		}
		f.addOptionList(labels, f.analyticsIndex)
	}

	confirmAction := "finish"
	if f.step == ftsetupStepTheme {
		confirmAction = "continue"
	}
	f.AddChild(tui.NewSpacer(1))
	f.AddChild(tui.NewText(
		theme.RawKeyHint(th, "↑↓", "navigate")+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, confirmAction)+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "skip setup"),
		1,
		0,
		nil,
	))
	f.AddChild(tui.NewSpacer(1))
	f.AddChild(NewDynamicBorder())
}

func (f *FirstTimeSetupComponent) addOptionList(labels []string, selectedIndex int) {
	th := theme.CurrentTheme()
	for i, text := range labels {
		isSelected := i == selectedIndex
		prefix := "  "
		label := th.Fg(theme.FgText, text)
		if isSelected {
			prefix = th.Fg(theme.FgAccent, "→ ")
			label = th.Fg(theme.FgAccent, text)
		}
		f.AddChild(tui.NewText(prefix+label, 1, 0, nil))
	}
}

func (f *FirstTimeSetupComponent) moveSelection(delta int) {
	if f.step == ftsetupStepTheme {
		next := max(0, min(len(ftsetupThemeOptions)-1, f.themeIndex+delta))
		if next != f.themeIndex {
			f.themeIndex = next
			if f.options.OnThemePreview != nil {
				f.options.OnThemePreview(ftsetupThemeOptions[f.themeIndex].value)
			}
		}
	} else {
		f.analyticsIndex = max(0, min(len(ftsetupAnalyticsOptions)-1, f.analyticsIndex+delta))
	}
	f.update()
}

// HandleInput processes up/down navigation, confirm, and cancel keys.
func (f *FirstTimeSetupComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp) || keyData == "k":
		f.moveSelection(-1)
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown) || keyData == "j":
		f.moveSelection(1)
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) || keyData == "\n":
		if f.step == ftsetupStepTheme {
			f.step = ftsetupStepAnalytics
			f.update()
		} else if f.options.OnSubmit != nil {
			f.options.OnSubmit(FirstTimeSetupResult{
				Theme:          ftsetupThemeOptions[f.themeIndex].value,
				ShareAnalytics: ftsetupAnalyticsOptions[f.analyticsIndex].value,
			})
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		if f.options.OnCancel != nil {
			f.options.OnCancel()
		}
	}
}
