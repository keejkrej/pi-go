package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type textedMockTerminal struct {
	tui.Terminal
}

func (m *textedMockTerminal) Columns() int { return 80 }
func (m *textedMockTerminal) Rows() int    { return 24 }

type textedMockTUI struct {
	tui.TUI
	term    *textedMockTerminal
	stops   int
	starts  int
	renders int
}

func (m *textedMockTUI) Terminal() tui.Terminal   { return m.term }
func (m *textedMockTUI) Stop(*tui.TuiStopOptions) { m.stops++ }
func (m *textedMockTUI) Start()                   { m.starts++ }
func (m *textedMockTUI) RequestRender(*bool)      { m.renders++ }

func TestExtensionEditorComponent(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	kb := core.NewKeybindingsManager(nil)
	tui.SetKeybindings(kb.KeybindingsManager)
	origEdit := extedEditInExternalEditor
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
		extedEditInExternalEditor = origEdit
	})

	ui := &textedMockTUI{term: &textedMockTerminal{}}
	prefill := "hello"
	desc := "Describe the issue"
	pad := 2
	cmd := "custom-ed"
	var submitted string
	cancelled := false

	ed := NewExtensionEditorComponent(
		ui,
		kb,
		"Bug Description",
		&prefill,
		func(v string) { submitted = v },
		func() { cancelled = true },
		&ExtensionEditorOptions{
			EditorOptions: tui.EditorOptions{PaddingX: &pad},
			Description:   &desc,
		},
		&cmd,
	)

	ed.SetFocused(true)
	if !ed.Focused() {
		t.Fatal("Focused() = false, want true")
	}

	out := utils.StripAnsi(strings.Join(ed.Render(80), "\n"))
	if !strings.Contains(out, "Bug Description") || !strings.Contains(out, "Describe the issue") {
		t.Fatalf("unexpected render:\n%s", out)
	}

	extedEditInExternalEditor = func(opts ExternalEditorOptions) ExternalEditorResult {
		if opts.Command != "custom-ed" || opts.Content != "hello" {
			t.Fatalf("unexpected external editor options: %+v", opts)
		}
		return ExternalEditorResult{Status: ExternalEditorStatusFailed, Message: "editor failed"}
	}
	ed.HandleInput("\x07") // ctrl+g external editor
	outFailed := utils.StripAnsi(strings.Join(ed.Render(80), "\n"))
	if !strings.Contains(outFailed, "editor failed") {
		t.Fatalf("expected failure message:\n%s", outFailed)
	}

	extedEditInExternalEditor = func(ExternalEditorOptions) ExternalEditorResult {
		return ExternalEditorResult{Status: ExternalEditorStatusComplete, Content: "updated"}
	}
	ed.HandleInput("\x07")
	if ui.stops != 2 || ui.starts != 2 || ui.renders != 2 {
		t.Fatalf("ui lifecycle = (%d, %d, %d), want (2, 2, 2)", ui.stops, ui.starts, ui.renders)
	}

	ed.HandleInput("\r")
	if submitted != "updated" {
		t.Fatalf("submitted = %q, want updated", submitted)
	}

	ed.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("expected cancel callback")
	}
}
