package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseShortcutAndKeybindingsManager(t *testing.T) {
	sc, err := ParseShortcut("shift+enter")
	if err != nil {
		t.Fatalf("ParseShortcut err: %v", err)
	}
	if sc.Mods != ui.Shift || sc.Key != ui.KeyEnter {
		t.Fatalf("unexpected shortcut %+v", sc)
	}
	if !sc.MatchesKey(ui.Shift, ui.KeyEnter) || sc.MatchesKey(0, ui.KeyEnter) {
		t.Fatal("MatchesKey failed")
	}

	scCmd, err := ParseShortcut("cmd+f")
	if err != nil || scCmd.Mods != ui.Cmd || scCmd.Key != ui.KeyF {
		t.Fatalf("ParseShortcut(cmd+f) = %+v, err=%v", scCmd, err)
	}

	if _, err := ParseShortcut("bogusmod+enter"); err == nil {
		t.Fatal("expected error for unknown modifier")
	}
	if _, err := ParseShortcut("ctrl+nonexistentkey"); err == nil {
		t.Fatal("expected error for unknown key")
	}

	km := NewKeybindingsManager(map[string][]string{
		string(ActionSubmit):            {"ctrl+enter"},
		string(AppModelSelect):          {"cmd+m"},
		string(ActionDeleteToLineStart): {"ctrl+k"}, // conflicts with ActionDeleteToLineEnd
	})
	if !km.MatchesEditorKey(ui.Ctrl, ui.KeyEnter, ActionSubmit) {
		t.Fatal("expected overridden submit shortcut ctrl+enter to match")
	}
	if km.MatchesEditorKey(0, ui.KeyEnter, ActionSubmit) {
		t.Fatal("expected overridden submit shortcut to replace enter default")
	}
	if !km.MatchesAppKey(ui.Cmd, ui.KeyM, AppModelSelect) {
		t.Fatal("expected overridden AppModelSelect to match")
	}

	conflicts := km.Conflicts()
	foundCtrlK := false
	for _, c := range conflicts {
		if c.Key == "ctrl+k" {
			foundCtrlK = true
		}
	}
	if !foundCtrlK {
		t.Fatalf("expected ctrl+k conflict reported, got %+v", conflicts)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(cfgPath, []byte(`{"app.model.select":"ctrl+m","tui.input.newLine":["alt+enter"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadKeybindingsFromFile(cfgPath)
	if err != nil {
		t.Fatalf("LoadKeybindingsFromFile err: %v", err)
	}
	if !loaded.MatchesAppKey(ui.Ctrl, ui.KeyM, AppModelSelect) {
		t.Fatal("expected file override ctrl+m for app.model.select")
	}
	if !loaded.MatchesEditorKey(ui.Alt, ui.KeyEnter, ActionNewLine) {
		t.Fatal("expected file override alt+enter for tui.input.newLine")
	}
}

func TestKeybindingsContextShortcut(t *testing.T) {
	km := NewKeybindingsManager()
	var opened bool
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill()
		km.OnAppContext(c, AppModelSelect, func() {
			opened = true
		})
	}, 200, 100)

	tt.Key(ui.Cmd, ui.KeyL)
	if !opened {
		t.Fatal("expected OnAppContext to trigger on Cmd+L")
	}
}
