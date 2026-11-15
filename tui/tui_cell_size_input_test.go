package tui_test

import (
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type cellSizeMockTerm struct {
	mu      sync.Mutex
	onInput func(data string)
	writes  []string
}

func (m *cellSizeMockTerm) Start(onInput func(data string), onResize func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onInput = onInput
}

func (m *cellSizeMockTerm) Stop()                                        {}
func (m *cellSizeMockTerm) DrainInput(maxMs *int64, idleMs *int64) error { return nil }
func (m *cellSizeMockTerm) Write(data string) {
	m.mu.Lock()
	m.writes = append(m.writes, data)
	m.mu.Unlock()
}
func (m *cellSizeMockTerm) Columns() int                              { return 80 }
func (m *cellSizeMockTerm) Rows() int                                 { return 24 }
func (m *cellSizeMockTerm) KittyProtocolActive() bool                 { return false }
func (m *cellSizeMockTerm) MoveBy(lines int)                          {}
func (m *cellSizeMockTerm) HideCursor()                               {}
func (m *cellSizeMockTerm) ShowCursor()                               {}
func (m *cellSizeMockTerm) ClearLine()                                {}
func (m *cellSizeMockTerm) ClearFromCursor()                          {}
func (m *cellSizeMockTerm) ClearScreen()                              {}
func (m *cellSizeMockTerm) SetTitle(title string)                     {}
func (m *cellSizeMockTerm) SetProgress(active bool)                   {}
func (m *cellSizeMockTerm) SetProgramStatus(status tui.ProgramStatus) {}
func (m *cellSizeMockTerm) emitInput(data string) {
	m.mu.Lock()
	h := m.onInput
	m.mu.Unlock()
	if h != nil {
		h(data)
	}
}

type cellSizeFocusComp struct {
	inputs []string
}

func (c *cellSizeFocusComp) Render(width int) []string { return []string{"input"} }
func (c *cellSizeFocusComp) Invalidate()               {}
func (c *cellSizeFocusComp) HandleInput(data string)   { c.inputs = append(c.inputs, data) }

func TestTuiCellSizeInput(t *testing.T) {
	prev := tui.GetCellDimensions()
	t.Cleanup(func() {
		tui.SetCellDimensions(prev)
	})

	term := &cellSizeMockTerm{}
	ui := tui.NewTuiMainScreen(term, nil, nil)
	focus := &cellSizeFocusComp{}
	ui.AddChild(focus)
	ui.SetFocus(focus)

	var listened []string
	ui.AddInputListener(func(data string) *tui.TuiInputListenerResult {
		listened = append(listened, data)
		return nil
	})

	ui.Start()
	defer ui.Stop(nil)

	// Cell size response CSI 6 ; height ; width t is consumed before reaching focusedComponent.
	term.emitInput("\x1b[6;22;11t")
	dims := tui.GetCellDimensions()
	if dims.HeightPx != 22 || dims.WidthPx != 11 {
		t.Fatalf("GetCellDimensions() = %+v, want WidthPx=11 HeightPx=22", dims)
	}
	if len(focus.inputs) != 0 {
		t.Fatalf("cell size response leaked to focus=%q", focus.inputs)
	}
	if len(listened) != 1 || listened[0] != "\x1b[6;22;11t" {
		t.Fatalf("expected inputListener to observe sequence before consumeCellSizeResponse, got %q", listened)
	}

	// Regular keystrokes and unrelated sequences still pass through.
	term.emitInput("a")
	term.emitInput("\x1b[6;0;11t")
	if len(focus.inputs) != 1 || focus.inputs[0] != "a" {
		t.Fatalf("focus.inputs = %v, want [a] (zero height consumed cleanly)", focus.inputs)
	}
}
