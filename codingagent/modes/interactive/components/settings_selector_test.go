// Ported from packages/coding-agent/test/settings-selector.test.ts (pi v1.5.2).

package components

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func tsseInitEnv(t *testing.T) {
	t.Helper()
	theme.InitTheme("dark", false)
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
}

func TestSettingsSelectorComponent(t *testing.T) {
	tsseInitEnv(t)

	t.Run("cycles through fullscreen settings", func(t *testing.T) {
		var exitOutputs []core.FullscreenExitOutput
		var scrollbars []tui.ScrollViewScrollbar
		var copyOnSelects []bool
		var wheelScrolls []tui.WheelScrollLines

		config := SettingsConfig{
			FullscreenExitOutput:       core.FullscreenExitOutputTranscript,
			FullscreenScrollbar:        tui.ScrollViewScrollbarAuto,
			FullscreenCopyOnSelect:     true,
			FullscreenWheelScrollLines: tui.WheelScrollLines{Lines: 7},
			DefaultModel:               "not set",
			ModelThinkingLevels:        map[string]agent.ThinkingLevel{},
		}
		callbacks := SettingsCallbacks{
			OnFullscreenExitOutputChange: func(output core.FullscreenExitOutput) {
				exitOutputs = append(exitOutputs, output)
			},
			OnFullscreenScrollbarChange: func(mode tui.ScrollViewScrollbar) {
				scrollbars = append(scrollbars, mode)
			},
			OnFullscreenCopyOnSelectChange: func(enabled bool) {
				copyOnSelects = append(copyOnSelects, enabled)
			},
			OnFullscreenWheelScrollLinesChange: func(lines tui.WheelScrollLines) {
				wheelScrolls = append(wheelScrolls, lines)
			},
		}

		cycle := func(label string, count int) {
			list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
			for _, ch := range label {
				list.HandleInput(string(ch))
			}
			for range count {
				list.HandleInput("\r")
			}
		}

		cycle("Fullscreen exit output", 2)
		if !slices.Equal(exitOutputs, []core.FullscreenExitOutput{core.FullscreenExitOutputResumeHint, core.FullscreenExitOutputTranscript}) {
			t.Fatalf("exitOutputs = %v", exitOutputs)
		}

		cycle("Fullscreen scrollbar", 3)
		if !slices.Equal(scrollbars, []tui.ScrollViewScrollbar{tui.ScrollViewScrollbarAlways, tui.ScrollViewScrollbarHidden, tui.ScrollViewScrollbarAuto}) {
			t.Fatalf("scrollbars = %v", scrollbars)
		}

		cycle("Fullscreen copy on select", 2)
		if !slices.Equal(copyOnSelects, []bool{false, true}) {
			t.Fatalf("copyOnSelects = %v", copyOnSelects)
		}

		// Custom values from settings.json stay in the cycle.
		cycle("Fullscreen wheel scrolling", 3)
		wantWheel := []tui.WheelScrollLines{{Lines: 10}, {Auto: true}, {Lines: 1}}
		if !slices.Equal(wheelScrolls, wantWheel) {
			t.Fatalf("wheelScrolls = %+v, want %+v", wheelScrolls, wantWheel)
		}
	})

	t.Run("cycles through share destinations", func(t *testing.T) {
		var destinations []core.SessionShareDestination
		config := SettingsConfig{
			SessionShareDestination: core.SessionShareDestinationAuto,
			DefaultModel:            "not set",
			ModelThinkingLevels:     map[string]agent.ThinkingLevel{},
		}
		callbacks := SettingsCallbacks{
			OnSessionShareDestinationChange: func(destination core.SessionShareDestination) {
				destinations = append(destinations, destination)
			},
		}
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
		list.SelectItem("share-destination")

		for range 3 {
			list.HandleInput("\r")
		}

		want := []core.SessionShareDestination{
			core.SessionShareDestinationGist,
			core.SessionShareDestinationRadius,
			core.SessionShareDestinationAuto,
		}
		if !slices.Equal(destinations, want) {
			t.Fatalf("destinations = %v, want %v", destinations, want)
		}
	})

	t.Run("keeps the configured fixed theme marked while browsing", func(t *testing.T) {
		config := SettingsConfig{
			DefaultModel:        "not set",
			ModelThinkingLevels: map[string]agent.ThinkingLevel{},
			CurrentTheme:        "dark",
			TerminalTheme:       tui.TerminalColorSchemeDark,
			AvailableThemes:     []string{"system", "dark", "light"},
		}
		callbacks := SettingsCallbacks{
			OnThemePreview: func(string) {},
			OnCancel:       func() {},
		}
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()

		list.SelectItem("theme")
		list.HandleInput("\r")
		output := utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		pattern := regexp.MustCompile(` {4}system +Theme created from your terminal's colors\n {4}automatic +Use separate themes`)
		if !pattern.MatchString(output) {
			t.Fatalf("unexpected theme submenu layout:\n%s", output)
		}
		if !strings.Contains(output, "\u2192 \u2713 dark") {
			t.Fatalf("expected active theme marker on dark:\n%s", output)
		}

		list.HandleInput("\x1b[B")
		output = utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		if !strings.Contains(output, "  \u2713 dark") || !strings.Contains(output, "\u2192   light") {
			t.Fatalf("expected configured theme check preserved while browsing:\n%s", output)
		}
	})

	t.Run("keeps a configured automatic theme marked while browsing", func(t *testing.T) {
		config := SettingsConfig{
			DefaultModel:        "not set",
			ModelThinkingLevels: map[string]agent.ThinkingLevel{},
			CurrentTheme:        "light/dark",
			TerminalTheme:       tui.TerminalColorSchemeDark,
			AvailableThemes:     []string{"dark", "light", "other"},
		}
		callbacks := SettingsCallbacks{
			OnThemePreview: func(string) {},
			OnCancel:       func() {},
		}
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()

		list.SelectItem("theme")
		list.HandleInput("\r")
		list.HandleInput("\r")
		output := utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		if !strings.Contains(output, "\u2192 \u2713 light") {
			t.Fatalf("expected light theme marked on entry:\n%s", output)
		}

		list.HandleInput("\x1b[B")
		output = utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		if !strings.Contains(output, "  \u2713 light") || !strings.Contains(output, "\u2192   other") {
			t.Fatalf("expected light theme check preserved while browsing:\n%s", output)
		}
	})

	t.Run("keeps the configured per-model thinking level marked while browsing", func(t *testing.T) {
		model := &ai.Model{
			BaseModel: ai.BaseModel{
				Id:       "thinking-model",
				Name:     "Thinking Model",
				Provider: "anthropic",
			},
			Reasoning: true,
		}
		modelKey := string(model.Provider) + "/" + model.Id
		config := SettingsConfig{
			DefaultModel:           modelKey,
			AvailableDefaultModels: []*ai.Model{model},
			ThinkingLevel:          agent.ThinkingLevelHigh,
			ModelThinkingLevels: map[string]agent.ThinkingLevel{
				modelKey: agent.ThinkingLevelMedium,
			},
		}
		callbacks := SettingsCallbacks{
			OnCancel: func() {},
		}
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()

		list.SelectItem("model-thinking")
		list.HandleInput("\r")
		list.HandleInput("\r")

		output := utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		if !strings.Contains(output, "\u2192 \u2713 medium") {
			t.Fatalf("expected medium override marked:\n%s", output)
		}
		if !strings.Contains(output, "    (clear override)") {
			t.Fatalf("expected clear override option:\n%s", output)
		}

		list.HandleInput("\x1b[B")
		output = utils.StripAnsi(strings.Join(list.Render(120), "\n"))
		if !strings.Contains(output, "  \u2713 medium") || !strings.Contains(output, "\u2192   high") {
			t.Fatalf("expected medium check preserved while moving to high:\n%s", output)
		}
	})

	t.Run("pages a searchable submenu with selection page keys (#7629)", func(t *testing.T) {
		var selected string
		items := make([]tui.SelectItem, 20)
		for i := range 20 {
			val := fmt.Sprintf("theme-%d", i)
			items[i] = tui.SelectItem{Value: val, Label: val}
		}
		submenu := NewSelectSubmenu(
			"Theme",
			"",
			items,
			"theme-0",
			func(value string) { selected = value },
			func() {},
			nil,
			&SelectSubmenuOptions{Searchable: true},
		)

		submenu.HandleInput("\x1b[6~") // PageDown
		submenu.HandleInput("\r")

		if selected != "theme-10" {
			t.Fatalf("selected = %q, want %q", selected, "theme-10")
		}
	})
}
