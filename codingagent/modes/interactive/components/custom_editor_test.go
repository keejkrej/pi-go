package components

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

type tceMockTerm struct {
	tui.Terminal
}

func (tceMockTerm) Columns() int              { return 80 }
func (tceMockTerm) Rows() int                 { return 24 }
func (tceMockTerm) KittyProtocolActive() bool { return false }

type tceMockTUI struct {
	tui.TUI
	term    tceMockTerm
	mu      sync.Mutex
	renders int
}

func (m *tceMockTUI) Terminal() tui.Terminal { return m.term }
func (m *tceMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}
func (m *tceMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestCustomEditorKeybindingsAutocompleteAndStatusBorder(t *testing.T) {
	ui := &tceMockTUI{}
	kb := core.NewKeybindingsManager(nil)
	ed := NewCustomEditor(ui, theme.GetEditorTheme(), kb)

	escaped := 0
	ed.OnEscape = func() { escaped++ }
	ctrlD := 0
	ed.OnCtrlD = func() { ctrlD++ }
	expanded := 0
	ed.OnAction(core.KeybindingAppToolsExpand, func() { expanded++ })
	pasted := 0
	ed.OnPasteImage = func() { pasted++ }

	shortcutConsumed := false
	ed.OnExtensionShortcut = func(data string) bool {
		if data == "\x18" { // ctrl+x
			shortcutConsumed = true
			return true
		}
		return false
	}

	// Extension shortcut intercepted cleanly.
	ed.HandleInput("\x18")
	if !shortcutConsumed {
		t.Fatalf("expected OnExtensionShortcut to intercept ctrl+x")
	}

	// Paste image fires OnPasteImage.
	ed.HandleInput("\x16") // ctrl+v
	if pasted != 1 {
		t.Fatalf("expected OnPasteImage once, got %d", pasted)
	}

	// Ctrl+D on empty buffer triggers OnCtrlD.
	ed.HandleInput("\x04")
	if ctrlD != 1 {
		t.Fatalf("expected OnCtrlD on empty buffer")
	}

	// Ctrl+O triggers registered ActionHandler.
	ed.HandleInput("\x0f")
	if expanded != 1 {
		t.Fatalf("expected ctrl+o ActionHandler once")
	}

	// Ctrl+D on non-empty buffer forwards to Editor (delete forward) instead of exiting.
	ed.SetText("ab")
	ed.HandleInput("\x04")
	if ctrlD != 1 {
		t.Fatalf("expected ctrl+d not to trigger OnCtrlD on non-empty editor")
	}

	// Escape outside autocomplete calls OnEscape.
	ed.HandleInput("\x1b")
	if escaped != 1 {
		t.Fatalf("expected OnEscape when autocomplete closed")
	}

	// Escape inside autocomplete cancels autocomplete rather than firing OnEscape.
	ed.SetText("")
	ed.SetAutocompleteProvider(tui.NewCombinedAutocompleteProvider([]tui.AutocompleteCommand{
		&tui.SlashCommand{Name: "help"},
	}, t.TempDir(), nil))
	ed.HandleInput("/")
	deadline := time.Now().Add(500 * time.Millisecond)
	for !ed.IsShowingAutocomplete() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !ed.IsShowingAutocomplete() {
		t.Fatalf("expected slash command to open autocomplete")
	}
	ed.HandleInput("\x1b")
	if escaped != 1 {
		t.Fatalf("expected Escape while autocomplete showing NOT to fire OnEscape")
	}

	// Top border embeds working status indicator cleanly within width.
	si := NewWorkingStatusIndicator(ui, "Thinking...", nil)
	defer si.Dispose()
	ed.SetWorkingStatusIndicator(si.StatusIndicator)

	lines := ed.Render(60)
	if len(lines) == 0 {
		t.Fatalf("expected rendered editor lines")
	}
	topPlain := utils.StripAnsi(lines[0])
	if !strings.Contains(topPlain, "Thinking...") {
		t.Fatalf("expected top border to embed working status message, got %q", topPlain)
	}
	if w := tui.VisibleWidth(lines[0]); w != 60 {
		t.Fatalf("expected top border visible width 60, got %d", w)
	}
}

func TestCustomEditorCtrlCAndAutocompletePrecedence(t *testing.T) {
	ui := &tceMockTUI{}
	kb := core.NewKeybindingsManager(nil)
	ed := NewCustomEditor(ui, theme.GetEditorTheme(), kb)

	ctrlCCalls := 0
	ed.OnAction(core.KeybindingAppClear, func() {
		ctrlCCalls++
		ed.SetText("")
	})
	ctrlDCalls := 0
	ed.OnCtrlD = func() {
		ctrlDCalls++
	}

	// Non-empty buffer: raw ctrl+c and Kitty CSI-u ctrl+c both invoke app.clear.
	ed.SetText("hello")
	ed.HandleInput("\x03")
	if ctrlCCalls != 1 || ed.GetText() != "" {
		t.Fatalf("expected ctrl+c to invoke app.clear on non-empty buffer, got calls=%d text=%q", ctrlCCalls, ed.GetText())
	}

	ed.SetText("world")
	ed.HandleInput("\x1b[99;5u")
	if ctrlCCalls != 2 || ed.GetText() != "" {
		t.Fatalf("expected Kitty CSI-u ctrl+c to invoke app.clear, got calls=%d text=%q", ctrlCCalls, ed.GetText())
	}

	// Empty buffer: ctrl+c still invokes app.clear.
	ed.HandleInput("\x03")
	if ctrlCCalls != 3 {
		t.Fatalf("expected ctrl+c on empty buffer to invoke app.clear, got %d", ctrlCCalls)
	}

	// While slash autocomplete is open, app.clear (ctrl+c) must still fire.
	ed.SetAutocompleteProvider(tui.NewCombinedAutocompleteProvider([]tui.AutocompleteCommand{
		&tui.SlashCommand{Name: "help"},
	}, t.TempDir(), nil))
	ed.HandleInput("/")
	deadline := time.Now().Add(500 * time.Millisecond)
	for !ed.IsShowingAutocomplete() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !ed.IsShowingAutocomplete() {
		t.Fatalf("expected slash command to open autocomplete")
	}

	ed.HandleInput("\x03")
	if ctrlCCalls != 4 || ed.GetText() != "" {
		t.Fatalf("expected ctrl+c while autocomplete open to invoke app.clear, got calls=%d text=%q", ctrlCCalls, ed.GetText())
	}

	// Empty buffer: ctrl+d fires OnCtrlD even when autocomplete provider is configured.
	ed.HandleInput("\x04")
	if ctrlDCalls != 1 {
		t.Fatalf("expected ctrl+d on empty buffer to invoke OnCtrlD, got %d", ctrlDCalls)
	}

	// Explicit editor history navigation takes precedence over conflicting app actions (e.g. ctrl+p).
	prevKB := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(prevKB) })
	cfg := omap.NewMap[string, *tui.KeybindingKeys]()
	ctrlP := tui.KeyId("ctrl+p")
	cfg.Set(string(tui.KeybindingTuiEditorHistoryPrevious), &tui.KeybindingKeys{Single: &ctrlP})
	customCfg := core.NewKeybindingsManager(cfg)
	tui.SetKeybindings(customCfg.KeybindingsManager)

	edHist := NewCustomEditor(ui, theme.GetEditorTheme(), customCfg)
	cycleCalls := 0
	edHist.OnAction(core.KeybindingAppModelCycleForward, func() { cycleCalls++ })
	edHist.AddToHistory("first prompt")
	edHist.HandleInput("\x10") // ctrl+p
	if cycleCalls != 0 {
		t.Fatalf("expected explicit historyPrevious (ctrl+p) to take precedence over conflicting app action, got %d calls", cycleCalls)
	}
	if got := edHist.GetText(); got != "first prompt" {
		t.Fatalf("expected ctrl+p to recall history entry %q, got %q", "first prompt", got)
	}
}
