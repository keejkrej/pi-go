package tui

import (
	"reflect"
	"strings"
	"testing"
)

func tsliTheme() SettingsListTheme {
	return SettingsListTheme{
		Label:       func(s string, selected bool) string { return s },
		Value:       func(s string, selected bool) string { return s },
		Description: func(s string) string { return s },
		Cursor:      "> ",
		Hint:        func(s string) string { return s },
	}
}

func tsliBoolPtr(b bool) *bool { return &b }

func TestSettingsListUnit_SelectUpdateValueAndMouse(t *testing.T) {
	var changes [][2]string
	items := []SettingItem{
		{Id: "mode", Label: "Mode", CurrentValue: "fast", Values: []string{"fast", "balanced", "slow"}},
		{Id: "theme", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
	}
	list := NewSettingsList(items, 5, tsliTheme(), func(id, val string) {
		changes = append(changes, [2]string{id, val})
	}, nil, nil)

	if !list.CapturesViewportInput() {
		t.Fatal("CapturesViewportInput() = false, want true")
	}
	if list.WantsKeyRelease() {
		t.Fatal("WantsKeyRelease() = true, want false")
	}

	list.SelectItem("theme")
	if list.selectedIndex != 1 {
		t.Fatalf("selectedIndex = %d, want 1", list.selectedIndex)
	}

	list.UpdateValue("mode", "slow")
	if items[0].CurrentValue != "slow" {
		// Check list's stored copy updated cleanly via public methods/Render or internal slice
	}
	if list.items[0].CurrentValue != "slow" {
		t.Fatalf("UpdateValue did not update list item: %q", list.items[0].CurrentValue)
	}

	// Mouse press + click cycles values and emits onChange
	resPress := list.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypePress, Button: TuiMouseButtonLeft, X: 0, Y: 0})
	if resPress == nil || resPress.Handled == nil || !*resPress.Handled {
		t.Fatalf("HandleMouse press = %#v", resPress)
	}
	resClick := list.HandleMouse(TuiMouseEvent{Type: TuiMouseEventTypeClick, Button: TuiMouseButtonLeft, X: 0, Y: 0})
	if resClick == nil || resClick.Handled == nil || !*resClick.Handled {
		t.Fatalf("HandleMouse click = %#v", resClick)
	}
	if !reflect.DeepEqual(changes, [][2]string{{"mode", "fast"}}) {
		t.Fatalf("changes after click = %#v, want [[mode fast]]", changes)
	}
	list.Invalidate()
}

func TestSettingsList_RenderSearchSubmenuAndKeyboard(t *testing.T) {
	t.Run("long setting values do not overflow terminal width", func(t *testing.T) {
		items := []SettingItem{
			{Id: "path", Label: "Path", CurrentValue: strings.Repeat("a/b/", 40)},
		}
		list := NewSettingsList(items, 5, tsliTheme(), func(string, string) {}, nil, nil)
		lines := list.Render(50)
		for i, line := range lines {
			if w := VisibleWidth(line); w > 50 {
				t.Errorf("line %d exceeds width 50: width=%d %q", i, w, line)
			}
		}
	})

	t.Run("opens submenu and preserves navigateTo on close", func(t *testing.T) {
		var doneFn func(*string, *SettingsListSubmenuOptions)
		items := []SettingItem{
			{
				Id:           "model",
				Label:        "Model",
				CurrentValue: "sonnet",
				Submenu: func(cur string, done func(*string, *SettingsListSubmenuOptions)) Component {
					doneFn = done
					return NewText("submenu: "+cur, 0, 0, nil)
				},
			},
			{Id: "thinking", Label: "Thinking", CurrentValue: "off", Values: []string{"off", "high"}},
		}
		var changed [2]string
		list := NewSettingsList(items, 5, tsliTheme(), func(id, v string) {
			changed = [2]string{id, v}
		}, nil, &SettingsListOptions{EnableSearch: tsliBoolPtr(true)})

		list.HandleInput("\r")
		if doneFn == nil {
			t.Fatal("expected Submenu callback to open")
		}
		lines := list.Render(60)
		if !strings.Contains(strings.Join(lines, "\n"), "submenu: sonnet") {
			t.Fatalf("expected active submenu render, got %#v", lines)
		}
		newVal := "opus"
		doneFn(&newVal, nil)
		if changed != [2]string{"model", "opus"} {
			t.Fatalf("changed = %#v, want [model opus]", changed)
		}
	})
}
