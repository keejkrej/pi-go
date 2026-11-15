package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func TestMigrateKeybindingsConfig(t *testing.T) {
	t.Run("migrates legacy names and orders canonical before alphabetical extras", func(t *testing.T) {
		raw := jsonx.ObjectOf(
			"zeta.custom", "alt+z",
			"interrupt", "ctrl+q",
			"cursorUp", "ctrl+p",
			"alpha.custom", "alt+a",
		)
		migratedObj, migrated := MigrateKeybindingsConfig(raw)
		if !migrated {
			t.Fatal("expected migrated=true")
		}
		if v, ok := migratedObj.Get(string(KeybindingAppInterrupt)); !ok || v != "ctrl+q" {
			t.Fatalf("app.interrupt = (%v, %v)", v, ok)
		}
		if v, ok := migratedObj.Get(string(tui.KeybindingTuiEditorCursorUp)); !ok || v != "ctrl+p" {
			t.Fatalf("tui.editor.cursorUp = (%v, %v)", v, ok)
		}
		keys := migratedObj.Keys()
		// extras should follow canonical keys and be sorted alphabetically
		n := len(keys)
		if n < 4 || keys[n-2] != "alpha.custom" || keys[n-1] != "zeta.custom" {
			t.Fatalf("unexpected key order: %v", keys)
		}
	})

	t.Run("canonical key takes precedence over legacy key", func(t *testing.T) {
		raw := jsonx.ObjectOf(
			"interrupt", "ctrl+c",
			string(KeybindingAppInterrupt), "ctrl+q",
		)
		migratedObj, migrated := MigrateKeybindingsConfig(raw)
		if !migrated {
			t.Fatal("expected migrated=true when legacy key removed")
		}
		if v, _ := migratedObj.Get(string(KeybindingAppInterrupt)); v != "ctrl+q" {
			t.Fatalf("expected canonical ctrl+q kept, got %v", v)
		}
	})

	t.Run("KeybindingsManagerCreate applies legacy migration on load", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{
			"newSession": "alt+n",
			"deleteSessionNoninvasive": "ctrl+backspace"
		}`), 0o644); err != nil {
			t.Fatal(err)
		}
		km := KeybindingsManagerCreate(dir)
		if !slices.Equal(km.GetKeys(KeybindingAppSessionNew), []tui.KeyId{"alt+n"}) {
			t.Fatalf("migrated newSession = %v", km.GetKeys(KeybindingAppSessionNew))
		}
		if !slices.Equal(km.GetKeys(KeybindingAppSessionDeleteNoninv), []tui.KeyId{"ctrl+backspace"}) {
			t.Fatalf("migrated deleteSessionNoninvasive = %v", km.GetKeys(KeybindingAppSessionDeleteNoninv))
		}
	})
}
