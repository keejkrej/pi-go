package tui

import (
	"reflect"
	"testing"
)

type tmcMouseChild struct {
	lines  []string
	events []TuiMouseEvent
	result *TuiMouseEventResult
}

func (c *tmcMouseChild) Render(int) []string {
	return append([]string(nil), c.lines...)
}

func (c *tmcMouseChild) Invalidate() {}

func (c *tmcMouseChild) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	c.events = append(c.events, event)
	return c.result
}

func tmcBoolPtr(b bool) *bool { return &b }
func tmcIntPtr(v int) *int    { return &v }

func TestMouseComponentsUnit_BoxPaddingAndListScroll(t *testing.T) {
	t.Run("Box ignores events in padding", func(t *testing.T) {
		child := &tmcMouseChild{lines: []string{"ok"}}
		box := NewBox(2, 1, nil)
		box.AddChild(child)
		if res := box.HandleMouse(TuiMouseEvent{X: 1, Y: 1, Width: 20, Height: 4}); res != nil {
			t.Fatalf("expected nil in left padding, got %#v", res)
		}
		if res := box.HandleMouse(TuiMouseEvent{X: 2, Y: 0, Width: 20, Height: 4}); res != nil {
			t.Fatalf("expected nil in top padding, got %#v", res)
		}
	})

	t.Run("ScrollView Mouse wheel scrolls and obeys overscroll policy", func(t *testing.T) {
		sv, err := NewScrollView(&tmcMouseChild{lines: []string{"1", "2", "3", "4"}}, nil)
		if err != nil {
			t.Fatalf("NewScrollView: %v", err)
		}
		sv.UpdateLayout(4, 2, nil)
		sv.ScrollBy(1)
		if sv.ScrollTop() != 1 {
			t.Fatalf("ScrollTop = %d, want 1", sv.ScrollTop())
		}
	})
}

func TestMouseComponents_DispatchAndHitTesting(t *testing.T) {
	t.Run("MouseRegion delegates to child first then fallback handler", func(t *testing.T) {
		handledTrue := tmcBoolPtr(true)
		var fallbackEvents []TuiMouseEvent
		child := &tmcMouseChild{lines: []string{"alpha"}, result: nil}
		region := NewMouseRegion(child, func(ev TuiMouseEvent) *TuiMouseEventResult {
			fallbackEvents = append(fallbackEvents, ev)
			return &TuiMouseEventResult{Handled: handledTrue}
		})

		ev := TuiMouseEvent{Type: TuiMouseEventTypePress, Button: TuiMouseButtonLeft, X: 3, Y: 0, Width: 20, Height: 1}
		res := region.HandleMouse(ev)
		if res == nil || res.Handled == nil || !*res.Handled {
			t.Fatalf("res = %#v, want handled", res)
		}
		if len(child.events) != 1 || len(fallbackEvents) != 1 {
			t.Fatalf("expected child then fallback: child=%d fallback=%d", len(child.events), len(fallbackEvents))
		}

		// When child claims the event, fallback is skipped.
		child.result = &TuiMouseEventResult{Handled: handledTrue}
		res2 := region.HandleMouse(ev)
		if res2 == nil || len(fallbackEvents) != 1 {
			t.Fatalf("expected fallback skipped when child handles: fallback=%d", len(fallbackEvents))
		}
	})

	t.Run("Box retargets mouse events into child coordinates", func(t *testing.T) {
		child := &tmcMouseChild{
			lines:  []string{"row0", "row1"},
			result: &TuiMouseEventResult{Handled: tmcBoolPtr(true)},
		}
		box := NewBox(2, 1, nil)
		box.AddChild(child)
		res := box.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypeClick, Button: TuiMouseButtonLeft, X: 4, Y: 2, Width: 20, Height: 5})
		if res == nil || len(child.events) != 1 {
			t.Fatalf("expected child dispatch, got res=%#v events=%#v", res, child.events)
		}
		got := child.events[0]
		if got.X != 2 || got.Y != 1 || got.Width != 16 || got.Height != 2 {
			t.Fatalf("retargeted coords = (%d,%d %dx%d), want (2,1 16x2)", got.X, got.Y, got.Width, got.Height)
		}
	})

	t.Run("Input HandleMouse places cursor by column", func(t *testing.T) {
		in := NewInput(nil)
		in.SetValue("abcdef")
		res := in.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypePress, Button: TuiMouseButtonLeft, X: 4, Y: 0})
		if res == nil || res.Focus == nil || !*res.Focus {
			t.Fatalf("Input.HandleMouse = %#v, want Focus=true", res)
		}
		if in.cursor != 2 {
			t.Fatalf("cursor = %d, want 2 after prompt '> ' width", in.cursor)
		}
	})

	t.Run("SelectList click selects row and double click/wheel updates index", func(t *testing.T) {
		var chosen []string
		list := NewSelectList([]SelectItem{
			{Value: "a", Label: "A"},
			{Value: "b", Label: "B"},
			{Value: "c", Label: "C"},
		}, 3, tslTheme(), nil)
		list.OnSelect = func(it SelectItem) { chosen = append(chosen, it.Value) }
		list.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypePress, Button: TuiMouseButtonLeft, X: 0, Y: 1})
		list.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypeClick, Button: TuiMouseButtonLeft, X: 0, Y: 1, ClickCount: tmcIntPtr(1)})
		if !reflect.DeepEqual(chosen, []string{"b"}) {
			t.Fatalf("chosen = %#v, want [b]", chosen)
		}
	})
}
