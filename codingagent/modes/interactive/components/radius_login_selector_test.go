package components

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tradlsMockTUI struct {
	tui.TUI
	renders atomic.Int32
}

func (m *tradlsMockTUI) RequestRender(*bool) { m.renders.Add(1) }
func (m *tradlsMockTUI) Post(fn func())      { fn() }

func TestRadiusLoginSelector(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
	})

	ui := &tradlsMockTUI{}
	radiusOpt := RadiusOption{Label: "Sign in with Radius (free quota)", Text: "Sign in with Radius"}
	var selected string
	sel := CreateLoginMenuSelector(
		ui,
		"Login method",
		[]string{radiusOpt.Label, "Other provider"},
		radiusOpt,
		func(opt string) { selected = opt },
		func() {},
	)
	defer sel.Dispose()

	th := theme.CurrentTheme()
	lines := strings.Join(sel.Render(100), "\n")
	plain := utils.StripAnsi(lines)
	if !strings.Contains(plain, radiusOpt.Label) {
		t.Fatalf("missing radius label:\n%s", plain)
	}
	if strings.Contains(lines, th.Fg(theme.FgAccent, radiusOpt.Label)) {
		t.Fatalf("expected animated Radius shimmer instead of static accent:\n%q", lines)
	}

	sel.HandleInput("\x1b[B")
	moved := utils.StripAnsi(strings.Join(sel.Render(100), "\n"))
	if !strings.Contains(moved, "→ Other provider") {
		t.Fatalf("expected Other provider selected:\n%s", moved)
	}
	sel.HandleInput("\r")
	if selected != "Other provider" {
		t.Fatalf("selected = %q, want Other provider", selected)
	}
}
