// Ported from packages/tui/test/keybindings.test.ts (pi v1.5.2).

package tui_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

func keybTestKeys(keys ...tui.KeyId) *tui.KeybindingKeys {
	return &tui.KeybindingKeys{Keys: keys}
}

func keybTestSingle(key tui.KeyId) *tui.KeybindingKeys {
	return &tui.KeybindingKeys{Single: &key}
}

func TestKeybindingsManager(t *testing.T) {
	t.Run("matches default keybindings", func(t *testing.T) {
		manager := tui.NewKeybindingsManager(tui.TuiKeybindings, nil)

		if !manager.Matches("\x1b[A", tui.KeybindingTuiEditorCursorUp) {
			t.Fatalf("expected up arrow to match cursorUp")
		}
		if !manager.Matches("\x03", tui.KeybindingTuiSelectCancel) {
			t.Fatalf("expected ctrl+c to match select.cancel")
		}
		if !manager.Matches("\x1b", tui.KeybindingTuiSelectCancel) {
			t.Fatalf("expected escape to match select.cancel")
		}
		if manager.Matches("\r", tui.KeybindingTuiInputNewLine) {
			t.Fatalf("expected plain enter not to match newLine")
		}
		if !manager.Matches("\n", tui.KeybindingTuiInputNewLine) {
			t.Fatalf("expected ctrl+j (LF) to match newLine")
		}
		if !manager.Matches("\x1b[13;2u", tui.KeybindingTuiInputNewLine) {
			t.Fatalf("expected shift+enter CSI-u to match newLine")
		}
		if !manager.Matches("\x1b[1;5H", tui.KeybindingTuiAltScreenTop) {
			t.Fatalf("expected ctrl+home to match tui.altScreen.top")
		}
		if !manager.Matches("\x1b[1;5F", tui.KeybindingTuiAltScreenBottom) {
			t.Fatalf("expected ctrl+end to match tui.altScreen.bottom")
		}
		if manager.Matches("\x1b[1;5H", tui.KeybindingTuiEditorCursorLineStart) {
			t.Fatalf("ctrl+home must not match v1.5.2 cursorLineStart default")
		}
	})

	t.Run("applies user bindings and supports unbinding with empty array", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		cfg.Set(string(tui.KeybindingTuiEditorCursorUp), keybTestSingle("ctrl+p"))
		cfg.Set(string(tui.KeybindingTuiEditorDeleteWordBackward), keybTestKeys())
		manager := tui.NewKeybindingsManager(tui.TuiKeybindings, cfg)

		if !manager.Matches("\x10", tui.KeybindingTuiEditorCursorUp) {
			t.Fatalf("expected ctrl+p to match rebound cursorUp")
		}
		if manager.Matches("\x1b[A", tui.KeybindingTuiEditorCursorUp) {
			t.Fatalf("expected up arrow no longer to match rebound cursorUp")
		}
		if len(manager.GetKeys(tui.KeybindingTuiEditorDeleteWordBackward)) != 0 {
			t.Fatalf("expected empty keys for unbound action")
		}
	})

	t.Run("does not evict selector confirm when input submit is rebound", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		cfg.Set(string(tui.KeybindingTuiInputSubmit), keybTestSingle("ctrl+enter"))
		manager := tui.NewKeybindingsManager(tui.TuiKeybindings, cfg)

		if len(manager.GetConflicts()) != 0 {
			t.Fatalf("expected no conflicts, got %+v", manager.GetConflicts())
		}
		if !slices.Equal(manager.GetKeys(tui.KeybindingTuiSelectConfirm), []tui.KeyId{"enter"}) {
			t.Fatalf("select.confirm keys changed: %v", manager.GetKeys(tui.KeybindingTuiSelectConfirm))
		}
		if !manager.Matches("\r", tui.KeybindingTuiSelectConfirm) {
			t.Fatalf("expected enter still to match select.confirm")
		}
	})

	t.Run("reports direct user binding conflicts without evicting defaults", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		cfg.Set(string(tui.KeybindingTuiInputSubmit), keybTestSingle("ctrl+x"))
		cfg.Set(string(tui.KeybindingTuiSelectCancel), keybTestSingle("ctrl+x"))
		manager := tui.NewKeybindingsManager(tui.TuiKeybindings, cfg)

		conflicts := manager.GetConflicts()
		if len(conflicts) != 1 || conflicts[0].Key != "ctrl+x" {
			t.Fatalf("expected conflict on ctrl+x, got %+v", conflicts)
		}
		if !slices.Equal(conflicts[0].Keybindings, []string{
			string(tui.KeybindingTuiInputSubmit),
			string(tui.KeybindingTuiSelectCancel),
		}) {
			t.Fatalf("unexpected conflict keybindings: %v", conflicts[0].Keybindings)
		}
		if !slices.Equal(manager.GetKeys(tui.KeybindingTuiSelectConfirm), []tui.KeyId{"enter"}) {
			t.Fatalf("defaults should stay intact")
		}
	})

	t.Run("keeps user bindings isolated from defaults and resolves bindings", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		cfg.Set(string(tui.KeybindingTuiInputNewLine), keybTestKeys("alt+enter", "alt+enter"))
		manager := tui.NewKeybindingsManager(tui.TuiKeybindings, cfg)

		resolved := manager.GetResolvedBindings()
		newLineResolved, ok := resolved.Get(string(tui.KeybindingTuiInputNewLine))
		if !ok || newLineResolved == nil || newLineResolved.Single == nil || *newLineResolved.Single != "alt+enter" {
			t.Fatalf("expected single-key resolved binding for deduplicated alt+enter, got %+v", newLineResolved)
		}

		// SetUserBindings updates resolved bindings
		next := omap.NewMap[string, *tui.KeybindingKeys]()
		next.Set(string(tui.KeybindingTuiAltScreenHalfPageUp), keybTestSingle("ctrl+u"))
		manager.SetUserBindings(next)
		if !manager.Matches("\x15", tui.KeybindingTuiAltScreenHalfPageUp) {
			t.Fatalf("expected ctrl+u to match after SetUserBindings")
		}
	})

	t.Run("global GetKeybindings/SetKeybindings and GetSelectPageIndex", func(t *testing.T) {
		prev := tui.GetKeybindings()
		defer tui.SetKeybindings(prev)

		mgr := tui.NewKeybindingsManager(tui.TuiKeybindings, nil)
		tui.SetKeybindings(mgr)
		if tui.GetKeybindings() != mgr {
			t.Fatalf("expected installed global keybindings manager")
		}

		// pageUp / pageDown sequences
		pageUp := "\x1b[5~"
		pageDown := "\x1b[6~"

		if got := tui.GetSelectPageIndex(mgr, "a", 3, 10, 5); got != nil {
			t.Fatalf("non-page key should return nil, got %d", *got)
		}
		if got := tui.GetSelectPageIndex(mgr, pageUp, 7, 10, 5); got == nil || *got != 2 {
			t.Fatalf("pageUp from 7 by 5: got %v", got)
		}
		if got := tui.GetSelectPageIndex(mgr, pageUp, 2, 10, 5); got == nil || *got != 0 {
			t.Fatalf("pageUp clamp to 0: got %v", got)
		}
		if got := tui.GetSelectPageIndex(mgr, pageDown, 6, 10, 5); got == nil || *got != 9 {
			t.Fatalf("pageDown clamp to 9: got %v", got)
		}
		if got := tui.GetSelectPageIndex(mgr, pageDown, 0, 0, 5); got == nil || *got != 0 {
			t.Fatalf("pageDown empty list should return 0: got %v", got)
		}
	})
}
