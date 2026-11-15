package core

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func TestKeybindingsSchemaValidation(t *testing.T) {
	v, err := typebox.Compile(KeybindingsSchema)
	if err != nil {
		t.Fatalf("KeybindingsSchema compile failed: %v", err)
	}

	valid := []string{"a", "9", "pageUp", "+", "ctrl+shift+x", "alt+ctrl+?", "ctrl+shift+alt+super+f12"}
	for _, binding := range valid {
		doc := jsonx.ObjectOf("extension.action", binding)
		if !v.Check(doc) {
			t.Fatalf("expected valid binding %q", binding)
		}
	}

	invalid := []string{"", "Ctrl+x", "control+x", "ctrl+not-a-key", "ctrl+ctrl+x", "ctrl+shift+alt+super+ctrl+x"}
	for _, binding := range invalid {
		doc := jsonx.ObjectOf("extension.action", binding)
		if v.Check(doc) {
			t.Fatalf("expected invalid binding %q", binding)
		}
	}

	if !v.Check(jsonx.ObjectOf("$schema", "https://pi.dev/schemas/keybindings.schema.json", "app.session.new", "ctrl+n", "extension.action", []any{"alt+x"})) {
		t.Fatal("expected valid document with $schema and array binding")
	}
	if v.Check(jsonx.ObjectOf("app.session.new", float64(42))) {
		t.Fatal("expected numeric binding rejected")
	}
}
