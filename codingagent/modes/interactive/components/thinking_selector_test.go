package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

func tthkselReset(t *testing.T, userBindings tui.KeybindingsConfig) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	kb := core.NewKeybindingsManager(userBindings)
	tui.SetKeybindings(kb.KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestThinkingSelector(t *testing.T) {
	t.Run("keeps the current thinking level marked while browsing", func(t *testing.T) {
		tthkselReset(t, nil)
		selector := NewThinkingSelectorComponent(
			ai.ModelThinkingLevelMedium,
			[]ai.ModelThinkingLevel{ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh},
			func(ai.ModelThinkingLevel) {},
			func() {},
			nil,
		)

		getLevelRow := func(level string) string {
			for _, line := range selector.GetSelectList().Render(80) {
				plain := utils.StripAnsi(line)
				if strings.Contains(plain, level) {
					return plain
				}
			}
			return ""
		}

		selected := selector.GetSelectList().GetSelectedItem()
		if selected == nil || selected.Label != "✓ medium" {
			t.Fatalf("selected item = %+v, want Label %q", selected, "✓ medium")
		}
		if row := getLevelRow("medium"); !strings.HasPrefix(row, "→ ✓ medium") {
			t.Fatalf("medium row = %q, want prefix %q", row, "→ ✓ medium")
		}

		selector.HandleInput("\x1b[B")
		if row := getLevelRow("medium"); !strings.HasPrefix(row, "  ✓ medium") {
			t.Fatalf("after down, medium row = %q, want prefix %q", row, "  ✓ medium")
		}
		if row := getLevelRow("high"); !strings.HasPrefix(row, "→   high") {
			t.Fatalf("after down, high row = %q, want prefix %q", row, "→   high")
		}
	})

	t.Run("uses the configured save binding", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		saveKey := tui.KeyId("ctrl+r")
		cfg.Set(string(core.KeybindingAppThinkingSave), &tui.KeybindingKeys{Single: &saveKey})
		tthkselReset(t, cfg)
		var saved []ai.ModelThinkingLevel
		selector := NewThinkingSelectorComponent(
			ai.ModelThinkingLevelMedium,
			[]ai.ModelThinkingLevel{ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh},
			func(ai.ModelThinkingLevel) {},
			func() {},
			func(level ai.ModelThinkingLevel) {
				saved = append(saved, level)
			},
			ai.ModelThinkingLevelHigh,
		)

		rendered := utils.StripAnsi(strings.Join(selector.Render(80), "\n"))
		if !strings.Contains(rendered, "Ctrl+R to set as default") {
			t.Fatalf("rendered missing Ctrl+R hint:\n%s", rendered)
		}
		if !strings.Contains(rendered, "· default") {
			t.Fatalf("rendered missing default suffix:\n%s", rendered)
		}

		selector.HandleInput("\x13")
		if len(saved) != 0 {
			t.Fatalf("saved after unbound default Ctrl+S = %v, want empty", saved)
		}
		selector.HandleInput("\x12")
		if len(saved) != 1 || saved[0] != ai.ModelThinkingLevelMedium {
			t.Fatalf("saved after Ctrl+R = %v, want [medium]", saved)
		}
	})

	t.Run("filters options via search input and handles focus", func(t *testing.T) {
		tthkselReset(t, nil)
		var chosen ai.ModelThinkingLevel
		selector := NewThinkingSelectorComponent(
			ai.ModelThinkingLevelLow,
			[]ai.ModelThinkingLevel{ai.ModelThinkingLevelOff, ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh},
			func(level ai.ModelThinkingLevel) { chosen = level },
			func() {},
			nil,
		)

		selector.SetFocused(true)
		if !selector.Focused() {
			t.Fatal("Focused() = false, want true")
		}
		for _, ch := range "deep" {
			selector.HandleInput(string(ch))
		}
		selector.HandleInput("\r")
		if chosen != ai.ModelThinkingLevelHigh {
			t.Fatalf("chosen = %q, want %q", chosen, ai.ModelThinkingLevelHigh)
		}
	})
}
