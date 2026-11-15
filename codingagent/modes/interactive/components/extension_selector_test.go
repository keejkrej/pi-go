package components

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func textselReset(t *testing.T) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func textselRender(sel *ExtensionSelectorComponent) string {
	return utils.StripAnsi(strings.Join(sel.Render(80), "\n"))
}

func textselVisibleOptions(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "option-") {
			trimmed := strings.TrimSpace(line)
			trimmed = strings.TrimPrefix(trimmed, "→ ")
			out = append(out, trimmed)
		}
	}
	return out
}

func TestExtensionSelectorComponent(t *testing.T) {
	t.Run("windows long option lists around the selection", func(t *testing.T) {
		textselReset(t)
		options := make([]string, 15)
		for i := range options {
			options[i] = fmt.Sprintf("option-%d", i+1)
		}
		sel := NewExtensionSelectorComponent("Choose", options, func(string) {}, func() {})
		defer sel.Dispose()

		out := textselRender(sel)
		if !slices.Equal(textselVisibleOptions(out), options[:10]) {
			t.Fatalf("visible = %v, want %v", textselVisibleOptions(out), options[:10])
		}
		if !strings.Contains(out, "→ option-1") || !strings.Contains(out, "  (1/15)") {
			t.Fatalf("unexpected initial render:\n%s", out)
		}

		for i := 0; i < 7; i++ {
			sel.HandleInput("\x1b[B")
		}
		out = textselRender(sel)
		if !slices.Equal(textselVisibleOptions(out), options[2:12]) {
			t.Fatalf("visible = %v, want %v", textselVisibleOptions(out), options[2:12])
		}
		if !strings.Contains(out, "→ option-8") || !strings.Contains(out, "  (8/15)") {
			t.Fatalf("unexpected mid render:\n%s", out)
		}

		for i := 0; i < 7; i++ {
			sel.HandleInput("\x1b[B")
		}
		out = textselRender(sel)
		if !slices.Equal(textselVisibleOptions(out), options[5:15]) {
			t.Fatalf("visible = %v, want %v", textselVisibleOptions(out), options[5:15])
		}
		if !strings.Contains(out, "→ option-15") || !strings.Contains(out, "  (15/15)") {
			t.Fatalf("unexpected bottom render:\n%s", out)
		}
	})

	t.Run("renders short and empty option lists without a scroll indicator", func(t *testing.T) {
		textselReset(t)
		short := []string{"option-1", "option-2", "option-3"}
		selShort := NewExtensionSelectorComponent("Choose", short, func(string) {}, func() {})
		defer selShort.Dispose()
		shortOut := textselRender(selShort)
		if !slices.Equal(textselVisibleOptions(shortOut), short) {
			t.Fatalf("visible = %v, want %v", textselVisibleOptions(shortOut), short)
		}
		if !strings.Contains(shortOut, "→ option-1") || strings.Contains(shortOut, "(1/3)") {
			t.Fatalf("unexpected short render:\n%s", shortOut)
		}

		selEmpty := NewExtensionSelectorComponent("Choose", nil, func(string) {}, func() {})
		defer selEmpty.Dispose()
		emptyOut := textselRender(selEmpty)
		if !strings.Contains(emptyOut, "Choose") || !strings.Contains(emptyOut, "↑↓ navigate") || strings.Contains(emptyOut, "→") {
			t.Fatalf("unexpected empty render:\n%s", emptyOut)
		}
	})

	t.Run("supports InitialIndex, Description, OnToggleToolsExpanded, and select/cancel", func(t *testing.T) {
		textselReset(t)
		idx := 1
		desc := "Pick one"
		toggled := false
		var chosen string
		cancelled := false
		sel := NewExtensionSelectorComponent(
			"Title",
			[]string{"first", "second", "third"},
			func(opt string) { chosen = opt },
			func() { cancelled = true },
			&ExtensionSelectorOptions{
				InitialIndex:          &idx,
				Description:           &desc,
				OnToggleToolsExpanded: func() { toggled = true },
			},
		)
		defer sel.Dispose()

		out := textselRender(sel)
		if !strings.Contains(out, "Pick one") || !strings.Contains(out, "→ second") {
			t.Fatalf("unexpected render:\n%s", out)
		}
		sel.HandleInput("\x0f") // ctrl+o
		if !toggled {
			t.Fatal("expected OnToggleToolsExpanded callback")
		}
		sel.HandleInput("\n")
		if chosen != "second" {
			t.Fatalf("chosen = %q, want second", chosen)
		}
		sel.HandleInput("\x1b")
		if !cancelled {
			t.Fatal("expected cancel callback")
		}
	})
}
