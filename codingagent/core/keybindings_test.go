package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

func TestUseWindowsKeybindingsFor(t *testing.T) {
	if !UseWindowsKeybindingsFor("windows", nil) || !UseWindowsKeybindingsFor("win32", nil) {
		t.Fatal("expected true on Windows platforms")
	}
	if !UseWindowsKeybindingsFor("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}) {
		t.Fatal("expected true when WSL_DISTRO_NAME is set on linux")
	}
	if !UseWindowsKeybindingsFor("linux", map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}) {
		t.Fatal("expected true when WSL_INTEROP is set on linux")
	}
	if UseWindowsKeybindingsFor("linux", map[string]string{}) {
		t.Fatal("expected false on non-WSL linux")
	}
	if UseWindowsKeybindingsFor("darwin", map[string]string{"WSL_DISTRO_NAME": "x"}) {
		t.Fatal("expected false on darwin even with WSL env")
	}
}

func TestKeybindingsManagerCreateAndReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"app.session.new": "ctrl+n",
		"app.model.select": ["ctrl+m", "alt+m"],
		"app.invalid": "ctrl+not-a-key"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	km := KeybindingsManagerCreate(dir)
	if !slices.Equal(km.GetKeys(KeybindingAppSessionNew), []tui.KeyId{"ctrl+n"}) {
		t.Fatalf("app.session.new = %v", km.GetKeys(KeybindingAppSessionNew))
	}
	if !slices.Equal(km.GetKeys(KeybindingAppModelSelect), []tui.KeyId{"ctrl+m", "alt+m"}) {
		t.Fatalf("app.model.select = %v", km.GetKeys(KeybindingAppModelSelect))
	}
	if km.GetEffectiveConfig().Has("app.invalid") {
		t.Fatal("invalid keybinding value should have been ignored")
	}

	if err := os.WriteFile(cfgPath, []byte(`{"app.session.new": "alt+n"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	km.Reload()
	if !slices.Equal(km.GetKeys(KeybindingAppSessionNew), []tui.KeyId{"alt+n"}) {
		t.Fatalf("after Reload app.session.new = %v", km.GetKeys(KeybindingAppSessionNew))
	}
}

func TestKeybindingsDefaultOverridesAndExtensionKeys(t *testing.T) {
	user := omap.NewMap[string, *tui.KeybindingKeys]()
	single := tui.KeyId("ctrl+x")
	user.Set(string(KeybindingAppInterrupt), &tui.KeybindingKeys{Single: &single})
	km := NewKeybindingsManager(user)

	if !slices.Equal(km.GetKeys(KeybindingAppInterrupt), []tui.KeyId{"ctrl+x"}) {
		t.Fatalf("override failed: %v", km.GetKeys(KeybindingAppInterrupt))
	}
	// Default binding still present when untouched.
	if len(km.GetKeys(KeybindingAppClear)) == 0 {
		t.Fatal("expected default app.clear keybinding")
	}
}
