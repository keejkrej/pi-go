package tui_test

import (
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type exitRestoreCall struct {
	before string
	after  string
}

type exitRestoreMockTerm struct {
	mu           sync.Mutex
	writes       []string
	restoreCalls []exitRestoreCall
	started      bool
	stopped      bool
	columns      int
	rows         int
}

func (m *exitRestoreMockTerm) Start(onInput func(data string), onResize func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.stopped = false
}

func (m *exitRestoreMockTerm) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
}

func (m *exitRestoreMockTerm) DrainInput(maxMs *int64, idleMs *int64) error { return nil }

func (m *exitRestoreMockTerm) Write(data string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, data)
}

func (m *exitRestoreMockTerm) Columns() int                              { return m.columns }
func (m *exitRestoreMockTerm) Rows() int                                 { return m.rows }
func (m *exitRestoreMockTerm) KittyProtocolActive() bool                 { return false }
func (m *exitRestoreMockTerm) MoveBy(lines int)                          {}
func (m *exitRestoreMockTerm) HideCursor()                               {}
func (m *exitRestoreMockTerm) ShowCursor()                               {}
func (m *exitRestoreMockTerm) ClearLine()                                {}
func (m *exitRestoreMockTerm) ClearFromCursor()                          {}
func (m *exitRestoreMockTerm) ClearScreen()                              {}
func (m *exitRestoreMockTerm) SetTitle(title string)                     {}
func (m *exitRestoreMockTerm) SetProgress(active bool)                   {}
func (m *exitRestoreMockTerm) SetProgramStatus(status tui.ProgramStatus) {}

func (m *exitRestoreMockTerm) RestoreOnExit(before string, after string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreCalls = append(m.restoreCalls, exitRestoreCall{before: before, after: after})
}

func TestExitRestore(t *testing.T) {
	t.Run("RestoreOnExit runs only while started and unstopped", func(t *testing.T) {
		term := &exitRestoreMockTerm{columns: 80, rows: 24}
		ui := tui.NewTuiMainScreen(term, nil, nil)

		ui.RestoreOnExit()
		if len(term.restoreCalls) != 0 {
			t.Fatalf("RestoreOnExit before Start called terminal %d times", len(term.restoreCalls))
		}

		ui.Start()
		ui.RestoreOnExit()
		if len(term.restoreCalls) != 1 {
			t.Fatalf("RestoreOnExit while active count = %d, want 1", len(term.restoreCalls))
		}
		if got := term.restoreCalls[0]; got.before != "\x1b[?25h" || got.after != "" {
			t.Fatalf("RestoreOnExit args = %+v, want before=%q after=%q", got, "\x1b[?25h", "")
		}

		ui.SetTerminalColorSchemeNotifications(true)
		// Second RestoreOnExit within the same active cycle is a no-op.
		ui.RestoreOnExit()
		if len(term.restoreCalls) != 1 {
			t.Fatalf("duplicate RestoreOnExit count = %d, want 1", len(term.restoreCalls))
		}

		ui.Stop(nil)
		ui.RestoreOnExit()
		if len(term.restoreCalls) != 1 {
			t.Fatalf("RestoreOnExit after Stop count = %d, want 1", len(term.restoreCalls))
		}

		ui.Stop(nil)
		ui.Start()
		ui.RestoreOnExit()
		if len(term.restoreCalls) != 2 {
			t.Fatalf("RestoreOnExit after restart count = %d, want 2", len(term.restoreCalls))
		}
		if got := term.restoreCalls[1]; got.before != "\x1b[?2031l\x1b[?25h" || got.after != "" {
			t.Fatalf("RestoreOnExit with color notifications = %+v", got)
		}
		ui.Stop(nil)
	})

	t.Run("RestoreOnExit is safe when terminal does not implement ExitRestoreTerminal", func(t *testing.T) {
		vt := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(vt, nil, nil)
		ui.Start()
		ui.RestoreOnExit()
		ui.Stop(nil)
	})
}
