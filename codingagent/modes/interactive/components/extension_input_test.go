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

type textinMockTUI struct {
	tui.TUI
	renders atomic.Int32
}

func (m *textinMockTUI) RequestRender(*bool) { m.renders.Add(1) }
func (m *textinMockTUI) Post(fn func())      { fn() }

func TestExtensionInputComponent(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
	})

	initVal := "abc"
	desc := "Enter something"
	var timeoutMs int64 = 5000
	ui := &textinMockTUI{}

	var submitted string
	cancelled := false
	in := NewExtensionInputComponent(
		"Input Title",
		nil,
		func(v string) { submitted = v },
		func() { cancelled = true },
		&ExtensionInputOptions{
			Tui:          ui,
			Timeout:      &timeoutMs,
			InitialValue: &initVal,
			Description:  &desc,
		},
	)
	defer in.Dispose()

	in.SetFocused(true)
	if !in.Focused() {
		t.Fatal("Focused() = false, want true")
	}

	out := utils.StripAnsi(strings.Join(in.Render(80), "\n"))
	if !strings.Contains(out, "Input Title (5s)") || !strings.Contains(out, "Enter something") {
		t.Fatalf("unexpected render:\n%s", out)
	}

	in.HandleInput("\x05") // Ctrl+E to move to end of initialValue
	in.HandleInput("d")
	in.HandleInput("\n")
	if submitted != "abcd" {
		t.Fatalf("submitted = %q, want abcd", submitted)
	}

	in.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("expected cancel callback")
	}
}
