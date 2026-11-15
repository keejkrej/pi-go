package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestThemeSelectorComponent(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
		_, _ = theme.InitTheme("dark", false)
	})

	var previewed, selected string
	cancelled := false
	sel := NewThemeSelectorComponent(
		"dark",
		func(name string) { selected = name },
		func() { cancelled = true },
		func(name string) { previewed = name },
	)

	list := sel.GetSelectList()
	cur := list.GetSelectedItem()
	if cur == nil || cur.Value != "dark" || cur.Description == nil || *cur.Description != "(current)" {
		t.Fatalf("initial item = %+v, want dark with (current)", cur)
	}

	rendered := utils.StripAnsi(strings.Join(sel.Render(80), "\n"))
	if !strings.Contains(rendered, "dark") || !strings.Contains(rendered, "(current)") {
		t.Fatalf("rendered missing current theme:\n%s", rendered)
	}

	list.HandleInput("\x1b[B") // move to light
	if previewed == "" || previewed == "dark" {
		t.Fatalf("previewed = %q, want non-dark theme", previewed)
	}
	list.HandleInput("\r")
	if selected != previewed {
		t.Fatalf("selected = %q, want %q", selected, previewed)
	}

	list.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("expected cancel callback")
	}
}
