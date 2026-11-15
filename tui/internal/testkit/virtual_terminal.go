// Ported from packages/tui/test/virtual-terminal.ts (pi v1.5.2).

package testkit

import (
	"fmt"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/keejkrej/pi-go/tui"
)

// VirtualTerminalCursorPosition is the zero-based terminal cursor coordinate.
type VirtualTerminalCursorPosition struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// VirtualTerminal emulates a terminal over github.com/charmbracelet/x/vt for TUI tests.
type VirtualTerminal struct {
	mu            sync.Mutex
	emulator      *vt.Emulator
	inputHandler  func(data string)
	resizeHandler func()
	columns       int
	rows          int
}

// NewVirtualTerminal creates a VirtualTerminal of the given dimensions.
// Values <= 0 fall back to 80 columns and 24 rows, matching TS defaults.
func NewVirtualTerminal(columns, rows int) *VirtualTerminal {
	if columns <= 0 {
		columns = 80
	}
	if rows <= 0 {
		rows = 24
	}
	emu := vt.NewEmulator(columns, rows)
	emu.RegisterApcHandler(func([]byte) bool { return true })
	return &VirtualTerminal{
		emulator: emu,
		columns:  columns,
		rows:     rows,
	}
}

func (v *VirtualTerminal) Start(onInput func(data string), onResize func()) {
	v.mu.Lock()
	v.inputHandler = onInput
	v.resizeHandler = onResize
	_, _ = v.emulator.WriteString("\x1b[?2004h")
	v.mu.Unlock()
}

func (v *VirtualTerminal) DrainInput(_ *int64, _ *int64) error {
	return nil
}

func (v *VirtualTerminal) Stop() {
	v.mu.Lock()
	_, _ = v.emulator.WriteString("\x1b[?2004l")
	v.inputHandler = nil
	v.resizeHandler = nil
	v.mu.Unlock()
}

func (v *VirtualTerminal) Write(data string) {
	v.mu.Lock()
	_, _ = v.emulator.WriteString(data)
	v.mu.Unlock()
}

func (v *VirtualTerminal) Columns() int {
	v.mu.Lock()
	cols := v.columns
	v.mu.Unlock()
	return cols
}

func (v *VirtualTerminal) Rows() int {
	v.mu.Lock()
	rows := v.rows
	v.mu.Unlock()
	return rows
}

func (v *VirtualTerminal) KittyProtocolActive() bool {
	return true
}

func (v *VirtualTerminal) MoveBy(lines int) {
	if lines == 0 {
		return
	}
	v.mu.Lock()
	if lines > 0 {
		_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dB", lines))
	} else {
		_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dA", -lines))
	}
	v.mu.Unlock()
}

func (v *VirtualTerminal) HideCursor() {
	v.Write("\x1b[?25l")
}

func (v *VirtualTerminal) ShowCursor() {
	v.Write("\x1b[?25h")
}

func (v *VirtualTerminal) ClearLine() {
	v.Write("\x1b[K")
}

func (v *VirtualTerminal) ClearFromCursor() {
	v.Write("\x1b[J")
}

func (v *VirtualTerminal) ClearScreen() {
	v.Write("\x1b[2J\x1b[H")
}

func (v *VirtualTerminal) SetTitle(title string) {
	v.Write("\x1b]0;" + title + "\x07")
}

func (v *VirtualTerminal) SetProgress(_ bool) {}

func (v *VirtualTerminal) SetProgramStatus(_ tui.ProgramStatus) {}

// SendInput simulates keyboard/mouse input reaching the terminal's input handler.
func (v *VirtualTerminal) SendInput(data string) {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

// Resize updates the virtual terminal dimensions and notifies the resize handler.
func (v *VirtualTerminal) Resize(columns, rows int) {
	if columns <= 0 {
		columns = 1
	}
	if rows <= 0 {
		rows = 1
	}
	v.mu.Lock()
	v.columns = columns
	v.rows = rows
	v.emulator.Resize(columns, rows)
	handler := v.resizeHandler
	v.mu.Unlock()
	if handler != nil {
		handler()
	}
}

// Flush completes all pending writes synchronously.
func (v *VirtualTerminal) Flush() {}

// FlushAndGetViewport flushes pending writes and returns the visible viewport rows.
func (v *VirtualTerminal) FlushAndGetViewport() []string {
	v.Flush()
	return v.GetViewport()
}

func vtCellsPlainText(cells []uv.Cell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.IsZero() {
			continue
		}
		if c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (v *VirtualTerminal) viewportLinesLocked() []string {
	lines := make([]string, v.rows)
	for y := 0; y < v.rows; y++ {
		var b strings.Builder
		for x := 0; x < v.columns; x++ {
			c := v.emulator.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue
			}
			if c.Content == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Content)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return lines
}

// GetViewport returns the visible viewport rows with trailing spaces trimmed.
func (v *VirtualTerminal) GetViewport() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.viewportLinesLocked()
}

// GetScrollBuffer returns the active scrollback buffer followed by visible rows.
func (v *VirtualTerminal) GetScrollBuffer() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var lines []string
	if !v.emulator.IsAltScreen() {
		if sb := v.emulator.Scrollback(); sb != nil {
			for _, sbLine := range sb.Lines() {
				lines = append(lines, vtCellsPlainText(sbLine))
			}
		}
	}
	lines = append(lines, v.viewportLinesLocked()...)
	return lines
}

// Clear clears the virtual terminal scrollback and screen.
func (v *VirtualTerminal) Clear() {
	v.mu.Lock()
	v.emulator.ClearScrollback()
	_, _ = v.emulator.WriteString("\x1b[3J\x1b[2J\x1b[H")
	v.mu.Unlock()
}

// Reset performs a full terminal reset (RIS) and clears scrollback.
func (v *VirtualTerminal) Reset() {
	v.mu.Lock()
	v.emulator.ClearScrollback()
	_, _ = v.emulator.WriteString("\x1bc")
	v.mu.Unlock()
}

// GetCursorPosition returns the current cursor position in zero-based coordinates.
func (v *VirtualTerminal) GetCursorPosition() VirtualTerminalCursorPosition {
	v.mu.Lock()
	pos := v.emulator.CursorPosition()
	v.mu.Unlock()
	return VirtualTerminalCursorPosition{X: pos.X, Y: pos.Y}
}

// CellAt returns a copy of the screen cell at zero-based column x and row y, or nil if out of bounds.
func (v *VirtualTerminal) CellAt(x, y int) *uv.Cell {
	v.mu.Lock()
	defer v.mu.Unlock()
	c := v.emulator.CellAt(x, y)
	if c == nil {
		return nil
	}
	return c.Clone()
}

// WaitForRender waits for TUI's throttled render pipeline to settle.
func (v *VirtualTerminal) WaitForRender() {
	time.Sleep(30 * time.Millisecond)
	v.Flush()
}

var _ tui.Terminal = (*VirtualTerminal)(nil)
