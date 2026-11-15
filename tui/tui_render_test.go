package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type trStaticComp struct {
	mu          sync.Mutex
	lines       []string
	renderCount int
	onRender    func(lines []string)
}

func (c *trStaticComp) setLines(lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = lines
}

func (c *trStaticComp) getLines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.lines)
}

func (c *trStaticComp) getRenderCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renderCount
}

func (c *trStaticComp) Render(width int) []string {
	c.mu.Lock()
	c.renderCount++
	out := slices.Clone(c.lines)
	cb := c.onRender
	c.mu.Unlock()
	if cb != nil {
		cb(out)
	}
	return out
}

func (c *trStaticComp) Invalidate() {}

type trInputComp struct {
	trStaticComp
}

func (c *trInputComp) HandleInput(data string) {
	c.setLines([]string{data})
}

type trBoundedWriteTerm struct {
	mu     sync.Mutex
	writes []string
	rows   int
	cols   int
}

func newTrBoundedWriteTerm(cols, rows int) *trBoundedWriteTerm {
	return &trBoundedWriteTerm{cols: cols, rows: rows}
}

func (t *trBoundedWriteTerm) Start(onInput func(data string), onResize func()) {}
func (t *trBoundedWriteTerm) Stop()                                            {}
func (t *trBoundedWriteTerm) DrainInput(maxMs *int64, idleMs *int64) error     { return nil }
func (t *trBoundedWriteTerm) Write(data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writes = append(t.writes, data)
}
func (t *trBoundedWriteTerm) Columns() int                       { return t.cols }
func (t *trBoundedWriteTerm) Rows() int                          { return t.rows }
func (t *trBoundedWriteTerm) KittyProtocolActive() bool          { return false }
func (t *trBoundedWriteTerm) MoveBy(lines int)                   {}
func (t *trBoundedWriteTerm) HideCursor()                        {}
func (t *trBoundedWriteTerm) ShowCursor()                        {}
func (t *trBoundedWriteTerm) ClearLine()                         {}
func (t *trBoundedWriteTerm) ClearFromCursor()                   {}
func (t *trBoundedWriteTerm) ClearScreen()                       {}
func (t *trBoundedWriteTerm) SetTitle(title string)              {}
func (t *trBoundedWriteTerm) SetProgress(active bool)            {}
func (t *trBoundedWriteTerm) SetProgramStatus(tui.ProgramStatus) {}

func (t *trBoundedWriteTerm) getWrites() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.writes)
}

func (t *trBoundedWriteTerm) joinedWrites() string {
	return strings.Join(t.getWrites(), "")
}

type trLoggingVT struct {
	*testkit.VirtualTerminal
	mu     sync.Mutex
	writes strings.Builder
}

func newTrLoggingVT(cols, rows int) *trLoggingVT {
	return &trLoggingVT{VirtualTerminal: testkit.NewVirtualTerminal(cols, rows)}
}

func (l *trLoggingVT) Write(data string) {
	l.mu.Lock()
	l.writes.WriteString(data)
	l.mu.Unlock()
	l.VirtualTerminal.Write(data)
}

func (l *trLoggingVT) clearWrites() {
	l.mu.Lock()
	l.writes.Reset()
	l.mu.Unlock()
}

func (l *trLoggingVT) getWrites() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writes.String()
}

func trRenderAndFlush(ui tui.TUI, term *testkit.VirtualTerminal, force bool) {
	ui.RenderNow(&force)
	term.Flush()
}

func trBoolPtr(v bool) *bool { return &v }

func trKittyImageBlock(rows, cols int, imageID int) []string {
	line := tui.EncodeKitty("AAAA", &tui.EncodeKittyOptions{
		Columns:    cols,
		Rows:       rows,
		ImageId:    imageID,
		MoveCursor: trBoolPtr(false),
	})
	lines := make([]string, rows)
	lines[0] = line
	return lines
}

func TestTuiRenderScheduling(t *testing.T) {
	term := testkit.NewVirtualTerminal(40, 10)
	ui := tui.NewTuiMainScreen(term, nil, nil)
	rendered := make(chan []string, 8)
	comp := &trInputComp{
		trStaticComp: trStaticComp{
			lines: []string{"initial"},
			onRender: func(lines []string) {
				select {
				case rendered <- lines:
				default:
				}
			},
		},
	}
	ui.AddChild(comp)
	ui.SetFocus(comp)
	ui.Start()
	defer ui.Stop(nil)

	ui.RenderNow(nil)
	<-rendered
	beforeCount := comp.getRenderCount()

	// Queue a throttled render immediately after RenderNow, then send keyboard input.
	comp.setLines([]string{"pending"})
	ui.RequestRender(nil)
	term.SendInput("typed")

	select {
	case got := <-rendered:
		if !reflect.DeepEqual(got, []string{"typed"}) {
			t.Fatalf("rendered lines = %#v, want [typed]", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for immediate input render")
	}

	if gotCount := comp.getRenderCount(); gotCount != beforeCount+1 {
		t.Fatalf("renderCount = %d, want %d", gotCount, beforeCount+1)
	}
}

func TestTuiDebugLogging(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv("PI_TUI_DEBUG_REDRAW", "1")

	term := testkit.NewVirtualTerminal(40, 10)
	ui := tui.NewTuiMainScreen(term, nil, &logDir)
	comp := &trStaticComp{lines: []string{"test"}}
	ui.AddChild(comp)

	ui.Start()
	ui.RenderNow(nil)
	ui.Stop(nil)

	data, err := os.ReadFile(filepath.Join(logDir, "pi-tui-debug.log"))
	if err != nil {
		t.Fatalf("failed to read debug log: %v", err)
	}
	if !strings.Contains(string(data), "fullRender: first render") {
		t.Fatalf("debug log = %q, want 'fullRender: first render'", string(data))
	}
}

func TestTuiBoundedRenderOutput(t *testing.T) {
	t.Run("does not write duplicate cursor markers to the terminal", func(t *testing.T) {
		term := newTrBoundedWriteTerm(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{
			lines: []string{"\x1b[31m界🙂\x1b[0m" + tui.CursorMarker + " world" + tui.CursorMarker},
		}
		ui.AddChild(comp)

		ui.RenderNow(nil)

		output := term.joinedWrites()
		if !strings.Contains(output, "\x1b[31m界🙂\x1b[0m world") {
			t.Fatalf("visible styled text missing in %q", output)
		}
		if strings.Contains(output, tui.CursorMarker) {
			t.Fatalf("internal CursorMarker leaked to terminal: %q", output)
		}
		if !strings.Contains(output, "\x1b[5G") {
			t.Fatalf("expected hardware cursor positioning '\\x1b[5G' in %q", output)
		}
	})

	t.Run("does not write cursor markers from other visible or scrollback lines", func(t *testing.T) {
		term := newTrBoundedWriteTerm(80, 2)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{
			lines: []string{"offscreen" + tui.CursorMarker, "a" + tui.CursorMarker, "visible-b" + tui.CursorMarker},
		}
		ui.AddChild(comp)

		ui.RenderNow(nil)

		output := term.joinedWrites()
		if !strings.Contains(output, "offscreen") || !strings.Contains(output, "a") {
			t.Fatalf("full render dropped expected text: %q", output)
		}
		if strings.Contains(output, tui.CursorMarker) {
			t.Fatalf("CursorMarker leaked into output: %q", output)
		}
		if !strings.Contains(output, "\x1b[10G") {
			t.Fatalf("expected bottom marker cursor column '\\x1b[10G' in %q", output)
		}
	})

	const maxRenderWriteChars = 1024 * 1024

	t.Run("splits a large full render without changing its output", func(t *testing.T) {
		term := newTrBoundedWriteTerm(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		kittyLine := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1_200_000) + "\x1b\\"
		ui.AddChild(&trStaticComp{lines: []string{kittyLine, kittyLine}})

		ui.RenderNow(nil)

		writes := term.getWrites()
		if len(writes) <= 2 {
			t.Fatalf("expected > 2 write chunks for large output, got %d", len(writes))
		}
		for i, w := range writes {
			if len(w) > maxRenderWriteChars {
				t.Fatalf("write[%d] len = %d > %d", i, len(w), maxRenderWriteChars)
			}
		}
		want := "\x1b[?2026h" + kittyLine + "\r\n" + kittyLine + "\x1b[?2026l"
		if got := strings.Join(writes, ""); got != want {
			t.Fatalf("chunked render output mismatch (got len=%d want len=%d)", len(got), len(want))
		}
	})

	t.Run("splits large differential updates without a full redraw", func(t *testing.T) {
		term := newTrBoundedWriteTerm(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"before"}}
		ui.AddChild(comp)
		ui.RenderNow(nil)

		term.mu.Lock()
		term.writes = nil
		term.mu.Unlock()

		kittyLine := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1_200_000) + "\x1b\\"
		comp.setLines([]string{"before", kittyLine, kittyLine})
		ui.RenderNow(nil)

		writes := term.getWrites()
		if len(writes) <= 2 {
			t.Fatalf("expected > 2 write chunks on large diff, got %d", len(writes))
		}
		for i, w := range writes {
			if len(w) > maxRenderWriteChars {
				t.Fatalf("write[%d] len = %d > %d", i, len(w), maxRenderWriteChars)
			}
		}
		joined := strings.Join(writes, "")
		if !strings.HasPrefix(joined, "\x1b[?2026h") || !strings.HasSuffix(joined, "\x1b[?2026l") {
			t.Fatalf("missing synchronized output framing")
		}
		if strings.Contains(joined, "\x1b[2J") {
			t.Fatalf("differential update unexpectedly performed full clear")
		}
	})
}

func TestTuiOsc133Markers(t *testing.T) {
	term := newTrBoundedWriteTerm(80, 24)
	ui := tui.NewTuiMainScreen(term, nil, nil)
	comp := &trStaticComp{
		lines: []string{"\x1b]133;A\x07prompt", "\x1b]133;B\x07\x1b]133;C\x07output"},
	}
	ui.AddChild(comp)
	ui.RenderNow(nil)

	comp.setLines(append(comp.getLines(), "\x1b]133;A\x1b\\reply", "\x1b]133;B\x1b\\\x1b]133;C\x1b\\done"))
	ui.RenderNow(nil)

	output := term.joinedWrites()
	if strings.Contains(output, "\x1b]133;") {
		t.Fatalf("regular mode emitted OSC 133 marker: %q", output)
	}
	for _, want := range []string{"prompt", "output", "reply", "done"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in output %q", want, output)
		}
	}
}

func TestTuiCrashDumpWithoutConfiguredLogDirectory(t *testing.T) {
	crashDir := t.TempDir()
	crashLogPath := filepath.Join(crashDir, "pi-tui-crash.log")
	t.Setenv("TMPDIR", crashDir)
	t.Setenv("TEMP", crashDir)
	t.Setenv("TMP", crashDir)

	term := testkit.NewVirtualTerminal(40, 10)
	ui := tui.NewTuiMainScreen(term, nil, nil)
	comp := &trStaticComp{lines: []string{"ok"}}
	ui.AddChild(comp)
	ui.Start()
	ui.RenderNow(nil)

	comp.setLines([]string{"ok", strings.Repeat("x", 60)})
	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		ui.RenderNow(nil)
	}()

	if recovered == nil {
		ui.Stop(nil)
		t.Fatal("expected width overflow panic from RenderNow")
	}
	errMsg := fmt.Sprint(recovered)
	if !strings.Contains(errMsg, crashLogPath) {
		t.Fatalf("panic message %q missing crash log path %q", errMsg, crashLogPath)
	}
	content, err := os.ReadFile(crashLogPath)
	if err != nil {
		t.Fatalf("failed reading crash log: %v", err)
	}
	if !strings.Contains(string(content), "Terminal width: 40") {
		t.Fatalf("crash log missing terminal width: %q", string(content))
	}
}

func TestTuiKittyImageCleanup(t *testing.T) {
	t.Run("clears reserved Kitty image rows before drawing appended image placements", func(t *testing.T) {
		tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
		tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 10})
		t.Cleanup(func() {
			tui.ResetCapabilitiesCache()
			tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})
		})

		term := newTrLoggingVT(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"before"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)

		ui.RenderNow(nil)
		term.clearWrites()

		imageLines := trKittyImageBlock(2, 2, 0)
		imageSequence := imageLines[0]
		nextLines := append([]string{"before"}, imageLines...)
		nextLines = append(nextLines, "after")
		comp.setLines(nextLines)
		ui.RenderNow(nil)

		writes := term.getWrites()
		if !strings.Contains(writes, "\x1b[2K\r\n\x1b[2K\x1b[1A"+imageSequence+"\x1b[1B") {
			t.Fatalf("reserved rows not pre-cleared before image placement: %q", writes)
		}
		if strings.Contains(writes, imageSequence+"\r\n\x1b[2K") {
			t.Fatalf("reserved row clear unexpectedly ran after placement: %q", writes)
		}
	})

	t.Run("falls back to full redraw when Kitty image pre-clear would scroll", func(t *testing.T) {
		tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
		tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 10})
		t.Cleanup(func() {
			tui.ResetCapabilitiesCache()
			tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})
		})

		term := newTrLoggingVT(40, 2)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"before"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)

		ui.RenderNow(nil)
		redrawsBefore := ui.FullRedraws()
		term.clearWrites()

		imageLines := trKittyImageBlock(3, 3, 0)
		nextLines := append([]string{"before"}, imageLines...)
		nextLines = append(nextLines, "after")
		comp.setLines(nextLines)
		ui.RenderNow(nil)

		if ui.FullRedraws() <= redrawsBefore {
			t.Fatalf("expected full redraw when pre-clear would scroll: before=%d after=%d", redrawsBefore, ui.FullRedraws())
		}
		if !strings.Contains(term.getWrites(), "\x1b[2J") {
			t.Fatalf("expected fallback clear '\\x1b[2J', got %q", term.getWrites())
		}
	})

	t.Run("reserves Kitty image rows before drawing during full redraw fallbacks", func(t *testing.T) {
		tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
		tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 10})
		t.Cleanup(func() {
			tui.ResetCapabilitiesCache()
			tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})
		})

		term := newTrLoggingVT(40, 5)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"l0", "l1", "l2", "l3", "l4"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)

		ui.RenderNow(nil)
		redrawsBefore := ui.FullRedraws()
		term.clearWrites()

		imageLines := trKittyImageBlock(3, 3, 0)
		imageSequence := imageLines[0]
		nextLines := append([]string{"l0", "l1", "l2", "l3", "l4"}, imageLines...)
		nextLines = append(nextLines, "after")
		comp.setLines(nextLines)
		ui.RenderNow(nil)

		writes := term.getWrites()
		if ui.FullRedraws() <= redrawsBefore {
			t.Fatalf("expected full redraw for scrolling image append")
		}
		if !strings.Contains(writes, "\r\n\r\n\x1b[2A"+imageSequence+"\x1b[2B") {
			t.Fatalf("full redraw missing reserved rows before placement: %q", writes)
		}
		if strings.Contains(writes, imageSequence+"\r\n\x1b[0m") {
			t.Fatalf("full redraw wrote reserved rows after placement: %q", writes)
		}
	})

	t.Run("does not use cursor-up placement for Kitty images taller than the viewport", func(t *testing.T) {
		tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
		tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 10})
		t.Cleanup(func() {
			tui.ResetCapabilitiesCache()
			tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})
		})

		term := newTrLoggingVT(40, 5)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"before"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)

		ui.RenderNow(nil)
		term.clearWrites()

		imageLines := trKittyImageBlock(6, 6, 0)
		imageSequence := imageLines[0]
		nextLines := append([]string{"before"}, imageLines...)
		nextLines = append(nextLines, "after")
		comp.setLines(nextLines)
		ui.RenderNow(trBoolPtr(true))

		writes := term.getWrites()
		if !strings.Contains(writes, imageSequence) {
			t.Fatalf("image placement missing in writes: %q", writes)
		}
		disallowed := fmt.Sprintf("\x1b[%dA%s", len(imageLines)-1, imageSequence)
		if strings.Contains(writes, disallowed) {
			t.Fatalf("taller-than-viewport image unexpectedly used cursor-up placement: %q", writes)
		}
	})

	t.Run("deletes changed image ids before drawing moved placements", func(t *testing.T) {
		term := newTrLoggingVT(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{}
		ui.AddChild(comp)

		oldImage := tui.EncodeKitty("AAAA", &tui.EncodeKittyOptions{
			Columns:    2,
			Rows:       2,
			ImageId:    42,
			MoveCursor: trBoolPtr(false),
		})
		comp.setLines([]string{"top", oldImage})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)
		term.clearWrites()

		newImage := tui.EncodeKitty("BBBB", &tui.EncodeKittyOptions{
			Columns:    2,
			Rows:       1,
			ImageId:    42,
			MoveCursor: trBoolPtr(false),
		})
		comp.setLines([]string{newImage, ""})
		ui.RenderNow(nil)

		writes := term.getWrites()
		delStr := tui.DeleteKittyImage(42)
		delIdx := strings.Index(writes, delStr)
		drawIdx := strings.Index(writes, newImage)
		if delIdx < 0 || drawIdx < 0 || delIdx >= drawIdx {
			t.Fatalf("expected delete (%d) before draw (%d) in %q", delIdx, drawIdx, writes)
		}
	})

	t.Run("redraws image lines when an earlier reserved image row changes", func(t *testing.T) {
		term := newTrLoggingVT(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{}
		ui.AddChild(comp)

		image := tui.EncodeKitty("AAAA", &tui.EncodeKittyOptions{
			Columns:    2,
			Rows:       2,
			ImageId:    88,
			MoveCursor: trBoolPtr(false),
		})
		comp.setLines([]string{"", image})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)
		term.clearWrites()

		comp.setLines([]string{"covered", image})
		ui.RenderNow(nil)

		writes := term.getWrites()
		delIdx := strings.Index(writes, tui.DeleteKittyImage(88))
		drawIdx := strings.Index(writes, image)
		if delIdx < 0 || drawIdx < 0 || delIdx >= drawIdx {
			t.Fatalf("expected delete (%d) before redraw (%d) in %q", delIdx, drawIdx, writes)
		}
		if strings.Contains(writes, "\x1b[2J") {
			t.Fatalf("expected differential redraw without full clear, got %q", writes)
		}
	})

	t.Run("deletes previously rendered image ids during full redraws", func(t *testing.T) {
		term := newTrLoggingVT(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{
			lines: []string{
				tui.EncodeKitty("AAAA", &tui.EncodeKittyOptions{
					Columns:    2,
					Rows:       2,
					ImageId:    77,
					MoveCursor: trBoolPtr(false),
				}),
			},
		}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)
		term.clearWrites()

		comp.setLines([]string{"plain text"})
		ui.RenderNow(trBoolPtr(true))

		writes := term.getWrites()
		delIdx := strings.Index(writes, tui.DeleteKittyImage(77))
		clearIdx := strings.Index(writes, "\x1b[2J")
		if delIdx < 0 || clearIdx < 0 || delIdx >= clearIdx {
			t.Fatalf("expected delete (%d) before screen clear (%d) in %q", delIdx, clearIdx, writes)
		}
	})
}

func TestTuiResizeHandling(t *testing.T) {
	t.Run("triggers full re-render when terminal height changes", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "")
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&trStaticComp{lines: []string{"Line 0", "Line 1", "Line 2"}})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)

		initialRedraws := ui.FullRedraws()
		term.Resize(40, 15)
		ui.RenderNow(nil)

		if ui.FullRedraws() <= initialRedraws {
			t.Fatalf("height change did not trigger full redraw: before=%d after=%d", initialRedraws, ui.FullRedraws())
		}
		if vp := term.FlushAndGetViewport(); !strings.Contains(vp[0], "Line 0") {
			t.Fatalf("content lost after height change: %#v", vp)
		}
	})

	t.Run("skips full re-render on height changes in Termux", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "1")
		term := newTrLoggingVT(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		lines := make([]string, 20)
		for i := range lines {
			lines[i] = fmt.Sprintf("Line %d", i)
		}
		ui.AddChild(&trStaticComp{lines: lines})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)
		term.clearWrites()

		initialRedraws := ui.FullRedraws()
		for _, h := range []int{15, 8, 14, 11} {
			term.Resize(40, h)
			ui.RenderNow(nil)
		}
		if ui.FullRedraws() != initialRedraws {
			t.Fatalf("Termux height resize triggered full redraw: %d != %d", ui.FullRedraws(), initialRedraws)
		}
		writes := term.getWrites()
		if strings.Contains(writes, "\x1b[2J") || strings.Contains(writes, "\x1b[3J") {
			t.Fatalf("Termux resize emitted clear sequence: %q", writes)
		}
		if got := strings.Join(term.FlushAndGetViewport(), "\n"); !strings.Contains(got, "Line 10") {
			t.Fatalf("existing content missing after Termux resizes: %#v", term.GetViewport())
		}
	})

	t.Run("triggers full re-render when terminal width changes", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&trStaticComp{lines: []string{"Line 0", "Line 1", "Line 2"}})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)

		initialRedraws := ui.FullRedraws()
		term.Resize(60, 10)
		ui.RenderNow(nil)

		if ui.FullRedraws() <= initialRedraws {
			t.Fatalf("width change did not trigger full redraw")
		}
	})
}

func TestTuiRenderDifferentialAndShrinkage(t *testing.T) {
	t.Run("clears empty rows when content shrinks significantly", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.SetClearOnShrink(true)
		comp := &trStaticComp{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4", "Line 5"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		initialRedraws := ui.FullRedraws()
		comp.setLines([]string{"Line 0", "Line 1"})
		trRenderAndFlush(ui, term, false)

		if ui.FullRedraws() <= initialRedraws {
			t.Fatalf("expected full redraw on shrink with clearOnShrink")
		}
		vp := term.GetViewport()
		if !strings.Contains(vp[0], "Line 0") || !strings.Contains(vp[1], "Line 1") || vp[2] != "" || vp[3] != "" {
			t.Fatalf("unexpected viewport after shrink: %#v", vp)
		}
	})

	t.Run("handles shrink to single line and shrink to empty", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.SetClearOnShrink(true)
		comp := &trStaticComp{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		comp.setLines([]string{"Only line"})
		trRenderAndFlush(ui, term, false)
		if vp := term.GetViewport(); vp[0] != "Only line" || vp[1] != "" {
			t.Fatalf("shrink to single line failed: %#v", vp[:3])
		}

		comp.setLines(nil)
		trRenderAndFlush(ui, term, false)
		if vp := term.GetViewport(); vp[0] != "" || vp[1] != "" {
			t.Fatalf("shrink to empty failed: %#v", vp[:3])
		}

		comp.setLines([]string{"New Line 0", "New Line 1"})
		trRenderAndFlush(ui, term, false)
		if vp := term.GetViewport(); vp[0] != "New Line 0" || vp[1] != "New Line 1" {
			t.Fatalf("empty -> content failed: %#v", vp[:3])
		}
	})

	t.Run("tracks cursor correctly when content shrinks with unchanged remaining lines", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		comp.setLines([]string{"Line 0", "Line 1", "Line 2"})
		trRenderAndFlush(ui, term, false)

		comp.setLines([]string{"Line 0", "CHANGED", "Line 2"})
		trRenderAndFlush(ui, term, false)
		if vp := term.GetViewport(); vp[1] != "CHANGED" {
			t.Fatalf("vp[1] = %q, want CHANGED (%#v)", vp[1], vp)
		}
	})

	t.Run("renders spinner middle line, first line, last line, and non-adjacent changes", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"Header", "Working...", "Footer", "Extra", "End"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		for _, frame := range []string{"|", "/", "-", "\\"} {
			comp.setLines([]string{"Header", "Working " + frame, "Footer", "Extra", "End"})
			trRenderAndFlush(ui, term, false)
			vp := term.GetViewport()
			if vp[0] != "Header" || vp[1] != "Working "+frame || vp[2] != "Footer" {
				t.Fatalf("spinner frame %q viewport = %#v", frame, vp[:4])
			}
		}

		comp.setLines([]string{"FIRST", "Working \\", "Footer", "CHANGED 3", "LAST"})
		trRenderAndFlush(ui, term, false)
		vp := term.GetViewport()
		want := []string{"FIRST", "Working \\", "Footer", "CHANGED 3", "LAST"}
		if !reflect.DeepEqual(vp[:5], want) {
			t.Fatalf("diff update vp[:5] = %#v, want %#v", vp[:5], want)
		}
	})

	t.Run("resets styles after each rendered line", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(20, 6)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&trStaticComp{lines: []string{"\x1b[3mItalic", "Plain"}})
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		if cell := term.CellAt(0, 0); cell == nil || cell.Style.Attrs&uv.AttrItalic == 0 {
			t.Fatalf("row 0 col 0 expected AttrItalic, got %+v", cell)
		}
		if cell := term.CellAt(0, 1); cell != nil && cell.Style.Attrs&uv.AttrItalic != 0 {
			t.Fatalf("row 1 col 0 leaked AttrItalic across lines: %+v", cell)
		}
	})

	t.Run("full re-renders when deleted lines move viewport upward and appends cleanly afterward", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(20, 5)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		lines12 := make([]string, 12)
		for i := range lines12 {
			lines12[i] = fmt.Sprintf("Line %d", i)
		}
		comp := &trStaticComp{lines: lines12}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		initialRedraws := ui.FullRedraws()
		comp.setLines(lines12[:7])
		trRenderAndFlush(ui, term, false)
		if ui.FullRedraws() <= initialRedraws {
			t.Fatalf("shrink above viewportTop did not trigger full redraw")
		}
		want := []string{"Line 2", "Line 3", "Line 4", "Line 5", "Line 6"}
		if vp := term.GetViewport(); !reflect.DeepEqual(vp, want) {
			t.Fatalf("vp = %#v, want %#v", vp, want)
		}

		// Shrink into viewport then append stays on differential path.
		term2 := testkit.NewVirtualTerminal(20, 5)
		ui2 := tui.NewTuiMainScreen(term2, nil, nil)
		comp2 := &trStaticComp{lines: lines12[:8]}
		ui2.AddChild(comp2)
		ui2.Start()
		defer ui2.Stop(nil)
		trRenderAndFlush(ui2, term2, false)

		comp2.setLines([]string{"Line 0", "Line 1"})
		trRenderAndFlush(ui2, term2, false)
		afterShrink := ui2.FullRedraws()

		comp2.setLines([]string{"Line 0", "Line 1", "Line 2"})
		trRenderAndFlush(ui2, term2, false)
		if ui2.FullRedraws() != afterShrink {
			t.Fatalf("append after shrink unnecessarily triggered full redraw")
		}
		if vp := term2.GetViewport(); !reflect.DeepEqual(vp, []string{"Line 0", "Line 1", "Line 2", "", ""}) {
			t.Fatalf("vp after append = %#v", vp)
		}
	})

	t.Run("clears stale content when maxLinesRendered was inflated by a transient component", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		chat := &trStaticComp{}
		editor := &trStaticComp{lines: []string{"Editor 0", "Editor 1", "Editor 2"}}
		ui.AddChild(chat)
		ui.AddChild(editor)

		longChat := make([]string, 15)
		for i := range longChat {
			longChat[i] = fmt.Sprintf("Chat %d", i)
		}
		selectorLines := make([]string, 8)
		for i := range selectorLines {
			selectorLines[i] = fmt.Sprintf("Selector %d", i)
		}

		chat.setLines(longChat)
		ui.Start()
		defer ui.Stop(nil)
		trRenderAndFlush(ui, term, false)

		editor.setLines(selectorLines)
		trRenderAndFlush(ui, term, false)

		editor.setLines([]string{"Editor 0", "Editor 1", "Editor 2"})
		trRenderAndFlush(ui, term, false)

		redrawsBeforeSwitch := ui.FullRedraws()
		chat.setLines(longChat[:12])
		trRenderAndFlush(ui, term, false)
		if ui.FullRedraws() <= redrawsBeforeSwitch {
			t.Fatalf("branch switch shrink did not trigger full redraw")
		}
		want := []string{
			"Chat 5", "Chat 6", "Chat 7", "Chat 8", "Chat 9",
			"Chat 10", "Chat 11", "Editor 0", "Editor 1", "Editor 2",
		}
		if vp := term.GetViewport(); !reflect.DeepEqual(vp, want) {
			t.Fatalf("vp = %#v, want %#v", vp, want)
		}
	})
}

func TestTuiFakeCursor(t *testing.T) {
	t.Run("draws focused fake cursor in reverse video and omits it when hardware cursor is shown", func(t *testing.T) {
		term := newTrLoggingVT(20, 5)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &trStaticComp{lines: []string{"> " + tui.CursorMarker + tui.RenderFakeCursor("a") + "bc"}}
		ui.AddChild(comp)
		ui.SetFocus(comp)
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)

		if w := term.getWrites(); !strings.Contains(w, "> \x1b[7ma\x1b[27mbc") {
			t.Fatalf("expected reverse-video fake cursor in %q", w)
		}

		term.clearWrites()
		ui.SetShowHardwareCursor(true)
		ui.RenderNow(nil)
		w := term.getWrites()
		if strings.Contains(w, "\x1b[7m") || strings.Contains(w, "\x1b_pi:") {
			t.Fatalf("expected fake cursor omitted when hardware cursor enabled: %q", w)
		}
	})

	t.Run("keeps non-focused fake cursor without CursorMarker even when hardware cursor is shown", func(t *testing.T) {
		term := newTrLoggingVT(20, 5)
		show := true
		ui := tui.NewTuiMainScreen(term, &show, nil)
		comp := &trStaticComp{lines: []string{"> " + tui.RenderFakeCursor("a") + "bc"}}
		ui.AddChild(comp)
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)

		if w := term.getWrites(); !strings.Contains(w, "> \x1b[7ma\x1b[27mbc") {
			t.Fatalf("expected non-hardware fake cursor retained in %q", w)
		}
	})

	t.Run("omits focused fake cursor when truncation dropped its end marker", func(t *testing.T) {
		term := newTrLoggingVT(20, 5)
		show := true
		ui := tui.NewTuiMainScreen(term, &show, nil)
		truncated := tui.SliceByColumn("ab"+tui.CursorMarker+tui.RenderFakeCursor("c")+"def", 0, 3, true)
		ui.AddChild(&trStaticComp{lines: []string{truncated}})
		ui.Start()
		defer ui.Stop(nil)
		ui.RenderNow(nil)

		w := term.getWrites()
		if strings.Contains(w, "\x1b[7m") {
			t.Fatalf("truncated fake cursor leaked reverse-video escape: %q", w)
		}
		if !strings.HasSuffix(w, "\x1b[3G") {
			t.Fatalf("expected hardware cursor at col 3 (\\x1b[3G), got %q", w)
		}
	})

	t.Run("keeps cursor when overlay ends directly left of it", func(t *testing.T) {
		for _, show := range []bool{false, true} {
			term := newTrLoggingVT(20, 5)
			ui := tui.NewTuiMainScreen(term, trBoolPtr(show), nil)
			ui.AddChild(&trStaticComp{lines: []string{"abc" + tui.CursorMarker + tui.RenderFakeCursor("d") + "ef"}})
			ui.ShowOverlay(&trStaticComp{lines: []string{"XXX"}}, &tui.OverlayOptions{
				Row:   &tui.SizeValueCells{Value: 0},
				Col:   &tui.SizeValueCells{Value: 0},
				Width: &tui.SizeValueCells{Value: 3},
			})
			ui.Start()
			ui.RenderNow(nil)
			w := term.getWrites()
			ui.Stop(nil)

			if !strings.Contains(w, "XXX") {
				t.Fatalf("show=%v missing overlay text in %q", show, w)
			}
			if gotReverse := strings.Contains(w, "\x1b[7md"); gotReverse != !show {
				t.Fatalf("show=%v reverse fake cursor = %v, want %v (%q)", show, gotReverse, !show, w)
			}
			if !strings.HasSuffix(w, "\x1b[4G") {
				t.Fatalf("show=%v expected hardware cursor suffix '\\x1b[4G', got %q", show, w)
			}
		}
	})
}
