package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func tftsetupReset(t *testing.T) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestFirstTimeSetupComponent(t *testing.T) {
	t.Run("previews themes, transitions to analytics, and submits result", func(t *testing.T) {
		tftsetupReset(t)
		var previews []string
		var submitted *FirstTimeSetupResult
		dlg := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
			OnThemePreview: func(name string) { previews = append(previews, name) },
			OnSubmit: func(res FirstTimeSetupResult) {
				r := res
				submitted = &r
			},
			OnCancel: func() {},
		})

		out := utils.StripAnsi(strings.Join(dlg.Render(100), "\n"))
		if !strings.Contains(out, "Welcome to "+utils.AppName+", the minimal coding agent.") {
			t.Fatalf("missing welcome text:\n%s", out)
		}
		if !strings.Contains(out, "→ System (matches your terminal colors)") {
			t.Fatalf("missing initial System theme selection:\n%s", out)
		}

		dlg.HandleInput("j") // Dark
		if len(previews) != 1 || previews[0] != "dark" {
			t.Fatalf("previews = %v, want [dark]", previews)
		}
		dlg.Invalidate()
		dlg.HandleInput("\r") // proceed to analytics step

		out2 := utils.StripAnsi(strings.Join(dlg.Render(100), "\n"))
		if !strings.Contains(out2, "Opt-in to anonymous usage data sharing?") {
			t.Fatalf("missing analytics prompt:\n%s", out2)
		}

		dlg.HandleInput("\x1b[B") // Don't share
		dlg.HandleInput("\n")
		if submitted == nil || submitted.Theme != "dark" || submitted.ShareAnalytics {
			t.Fatalf("submitted = %+v, want {Theme: dark, ShareAnalytics: false}", submitted)
		}
	})

	t.Run("cancels on escape", func(t *testing.T) {
		tftsetupReset(t)
		cancelled := false
		dlg := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
			OnCancel: func() { cancelled = true },
		})

		dlg.HandleInput("\x1b")
		if !cancelled {
			t.Fatal("expected cancel callback")
		}
	})
}
