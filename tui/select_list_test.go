package tui

import (
	"reflect"
	"strings"
	"testing"
)

func tslTheme() SelectListTheme {
	return SelectListTheme{
		SelectedPrefix: func(s string) string { return "\x1b[34m" + s + "\x1b[39m" },
		SelectedText:   func(s string) string { return "\x1b[1m" + s + "\x1b[22m" },
		Description:    func(s string) string { return "\x1b[2m" + s + "\x1b[22m" },
		ScrollInfo:     func(s string) string { return "\x1b[2m" + s + "\x1b[22m" },
		NoMatch:        func(s string) string { return "\x1b[2m" + s + "\x1b[22m" },
	}
}

func tslDesc(s string) *string { return &s }

func TestSelectListUnit_FilterSelectionCallbacksAndMouse(t *testing.T) {
	items := []SelectItem{
		{Value: "alpha", Label: "Alpha", Description: tslDesc("first option")},
		{Value: "beta", Label: "Beta", Description: tslDesc("second option")},
		{Value: "gamma", Label: "Gamma", Description: tslDesc("third option")},
	}
	list := NewSelectList(items, 5, tslTheme(), nil)

	if !list.CapturesViewportInput() {
		t.Fatal("CapturesViewportInput() = false, want true")
	}
	if list.WantsKeyRelease() {
		t.Fatal("WantsKeyRelease() = true, want false")
	}
	if sel := list.GetSelectedItem(); sel == nil || sel.Value != "alpha" {
		t.Fatalf("initial GetSelectedItem = %#v, want alpha", sel)
	}

	var changed []string
	var chosen []string
	list.OnSelectionChange = func(it SelectItem) { changed = append(changed, it.Value) }
	list.OnSelect = func(it SelectItem) { chosen = append(chosen, it.Value) }

	list.SetSelectedIndex(2)
	if sel := list.GetSelectedItem(); sel == nil || sel.Value != "gamma" {
		t.Fatalf("after SetSelectedIndex(2) = %#v, want gamma", sel)
	}
	list.SetSelectedIndex(999)
	if sel := list.GetSelectedItem(); sel == nil || sel.Value != "gamma" {
		t.Fatalf("clamped SetSelectedIndex(999) = %#v, want gamma", sel)
	}

	list.SetFilter("be")
	if sel := list.GetSelectedItem(); sel == nil || sel.Value != "beta" {
		t.Fatalf("after SetFilter(be) = %#v, want beta", sel)
	}

	list.SetFilter("")
	delta := 1
	wheelRes := list.HandleMouse(TuiMouseEvent{
		Type:       TuiMouseEventTypeWheel,
		Button:     TuiMouseButtonNone,
		WheelDelta: &delta,
	})
	if wheelRes == nil || wheelRes.Handled == nil || !*wheelRes.Handled {
		t.Fatalf("wheel HandleMouse = %#v, want handled", wheelRes)
	}

	pressRes := list.HandleMouse(TuiMouseEvent{
		Type:   TuiMouseEventTypePress,
		Button: TuiMouseButtonLeft,
		X:      0,
		Y:      2,
	})
	if pressRes == nil || pressRes.Handled == nil || !*pressRes.Handled {
		t.Fatalf("press HandleMouse = %#v, want handled", pressRes)
	}
	if sel := list.GetSelectedItem(); sel == nil || sel.Value != "gamma" {
		t.Fatalf("selected after row press = %#v, want gamma", sel)
	}

	clickRes := list.HandleMouse(TuiMouseEvent{
		Type:   TuiMouseEventTypeClick,
		Button: TuiMouseButtonLeft,
		X:      0,
		Y:      2,
	})
	if clickRes == nil || clickRes.Handled == nil || !*clickRes.Handled {
		t.Fatalf("click HandleMouse = %#v, want handled", clickRes)
	}
	if !reflect.DeepEqual(chosen, []string{"gamma"}) {
		t.Fatalf("OnSelect calls = %#v, want [gamma]", chosen)
	}
	if len(changed) == 0 {
		t.Fatal("expected OnSelectionChange to fire")
	}
	list.Invalidate()
}

func TestSelectList_RenderAndKeyboardNavigation(t *testing.T) {
	items := []SelectItem{
		{Value: "one", Label: "One", Description: tslDesc("Line 1\nLine 2")},
		{Value: "two", Label: "Two", Description: tslDesc("Desc Two")},
		{Value: "three", Label: "Three"},
	}
	list := NewSelectList(items, 5, tslTheme(), nil)

	lines := list.Render(80)
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	if strings.Contains(tmarkStripAnsi(lines[0]), "\n") || !strings.Contains(tmarkStripAnsi(lines[0]), "Line 1 Line 2") {
		t.Fatalf("multiline description not normalized to single line: %q", lines[0])
	}

	var selectedVal string
	list.OnSelect = func(it SelectItem) { selectedVal = it.Value }
	list.HandleInput("\x1b[B") // Down
	list.HandleInput("\r")
	if selectedVal != "two" {
		t.Fatalf("selected = %q, want two", selectedVal)
	}
}
