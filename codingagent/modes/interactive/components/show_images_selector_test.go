package components

import (
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func TestShowImagesSelectorComponent(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
	})

	var results []bool
	sel := NewShowImagesSelectorComponent(true, func(show bool) {
		results = append(results, show)
	}, func() {})

	list := sel.GetSelectList()
	if cur := list.GetSelectedItem(); cur == nil || cur.Value != "yes" {
		t.Fatalf("initial item = %+v, want yes", cur)
	}
	list.HandleInput("\x1b[B") // no
	list.HandleInput("\r")
	if len(results) != 1 || results[0] {
		t.Fatalf("results = %v, want [false]", results)
	}

	cancelled := false
	selNo := NewShowImagesSelectorComponent(false, func(bool) {}, func() { cancelled = true })
	if cur := selNo.GetSelectList().GetSelectedItem(); cur == nil || cur.Value != "no" {
		t.Fatalf("initial item = %+v, want no", cur)
	}
	selNo.GetSelectList().HandleInput("\x1b")
	if !cancelled {
		t.Fatal("expected cancel callback")
	}
}
