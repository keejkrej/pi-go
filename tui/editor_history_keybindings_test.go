package tui

import (
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
)

func tehkDefaultEditorTheme() EditorTheme {
	return EditorTheme{
		BorderColor: func(s string) string { return s },
		SelectList: SelectListTheme{
			SelectedPrefix: func(s string) string { return s },
			SelectedText:   func(s string) string { return s },
			Description:    func(s string) string { return s },
			ScrollInfo:     func(s string) string { return s },
			NoMatch:        func(s string) string { return s },
		},
	}
}

func tehkKeyPtr(k KeyId) *KeybindingKeys {
	return &KeybindingKeys{Single: &k}
}

func TestEditorHistoryKeybindings_DirectHistoryNavigation(t *testing.T) {
	defer SetKeybindings(NewKeybindingsManager(TuiKeybindings, nil))

	cfg := omap.NewMap[string, *KeybindingKeys]()
	cfg.Set(string(KeybindingTuiEditorHistoryPrevious), tehkKeyPtr("ctrl+p"))
	cfg.Set(string(KeybindingTuiEditorHistoryNext), tehkKeyPtr("ctrl+n"))
	SetKeybindings(NewKeybindingsManager(TuiKeybindings, cfg))

	editor := NewEditor(&tcloMockTUI{}, tehkDefaultEditorTheme(), nil)
	editor.AddToHistory("older prompt")
	editor.AddToHistory("newer\nmultiline prompt")
	editor.SetText("draft")
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[D")

	editor.HandleInput("\x10") // Ctrl+P
	if got := editor.GetText(); got != "newer\nmultiline prompt" {
		t.Fatalf("after 1st Ctrl+P text = %q, want newer\\nmultiline prompt", got)
	}
	if cur := editor.GetCursor(); !reflect.DeepEqual(cur, EditorCursor{Line: 0, Col: 0}) {
		t.Fatalf("after 1st Ctrl+P cursor = %#v, want {0 0}", cur)
	}

	editor.HandleInput("\x10") // Ctrl+P
	if got := editor.GetText(); got != "older prompt" {
		t.Fatalf("after 2nd Ctrl+P text = %q, want older prompt", got)
	}

	editor.HandleInput("\x0e") // Ctrl+N
	if got := editor.GetText(); got != "newer\nmultiline prompt" {
		t.Fatalf("after 1st Ctrl+N text = %q, want newer\\nmultiline prompt", got)
	}
	if cur := editor.GetCursor(); !reflect.DeepEqual(cur, EditorCursor{Line: 1, Col: 16}) {
		t.Fatalf("after 1st Ctrl+N cursor = %#v, want {1 16}", cur)
	}

	editor.HandleInput("\x0e") // Ctrl+N
	if got := editor.GetText(); got != "draft" {
		t.Fatalf("after 2nd Ctrl+N text = %q, want draft", got)
	}
	if cur := editor.GetCursor(); !reflect.DeepEqual(cur, EditorCursor{Line: 0, Col: 3}) {
		t.Fatalf("after 2nd Ctrl+N cursor = %#v, want {0 3}", cur)
	}
}
