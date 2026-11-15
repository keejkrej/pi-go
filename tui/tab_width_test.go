package tui_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type tabWidthStaticComp struct {
	lines []string
}

func (c *tabWidthStaticComp) Render(width int) []string { return c.lines }
func (c *tabWidthStaticComp) Invalidate()               {}

type tabWidthCaptureTerm struct {
	mu     sync.Mutex
	writes []string
}

func (m *tabWidthCaptureTerm) Start(onInput func(data string), onResize func()) {}
func (m *tabWidthCaptureTerm) Stop()                                            {}
func (m *tabWidthCaptureTerm) DrainInput(maxMs *int64, idleMs *int64) error     { return nil }
func (m *tabWidthCaptureTerm) Write(data string) {
	m.mu.Lock()
	m.writes = append(m.writes, data)
	m.mu.Unlock()
}
func (m *tabWidthCaptureTerm) Columns() int                              { return 16 }
func (m *tabWidthCaptureTerm) Rows() int                                 { return 4 }
func (m *tabWidthCaptureTerm) KittyProtocolActive() bool                 { return false }
func (m *tabWidthCaptureTerm) MoveBy(lines int)                          {}
func (m *tabWidthCaptureTerm) HideCursor()                               {}
func (m *tabWidthCaptureTerm) ShowCursor()                               {}
func (m *tabWidthCaptureTerm) ClearLine()                                {}
func (m *tabWidthCaptureTerm) ClearFromCursor()                          {}
func (m *tabWidthCaptureTerm) ClearScreen()                              {}
func (m *tabWidthCaptureTerm) SetTitle(title string)                     {}
func (m *tabWidthCaptureTerm) SetProgress(active bool)                   {}
func (m *tabWidthCaptureTerm) SetProgramStatus(status tui.ProgramStatus) {}

func TestTabWidth(t *testing.T) {
	t.Run("renders lines containing tabs without exceeding width or leaving artifacts", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(16, 4)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&tabWidthStaticComp{lines: []string{"\ta", "\t\tb", "\txxx"}})

		ui.Start()
		ui.RenderNow(nil)
		vp := term.FlushAndGetViewport()
		ui.Stop(nil)

		want := []string{"   a", "      b", "   xxx", ""}
		if !reflect.DeepEqual(vp, want) {
			t.Fatalf("viewport = %#v, want %#v", vp, want)
		}
	})

	t.Run("preserves full line width when compositing overlays over lines containing tabs", func(t *testing.T) {
		pad16 := func(s string) string {
			if len(s) >= 16 {
				return s
			}
			return s + strings.Repeat(" ", 16-len(s))
		}

		// Verify rendered frame written by TuiMainScreen preserves width 16 on composited rows.
		capTerm := &tabWidthCaptureTerm{}
		capUI := tui.NewTuiMainScreen(capTerm, nil, nil)
		capUI.AddChild(&tabWidthStaticComp{
			lines: []string{pad16("base 0"), pad16("base 1"), pad16("base 2")},
		})
		capUI.ShowOverlay(&tabWidthStaticComp{lines: []string{"\tX"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 4},
			Row:   &tui.SizeValueCells{Value: 1},
			Col:   &tui.SizeValueCells{Value: 4},
		})
		capUI.Start()
		capUI.RenderNow(nil)
		capUI.Stop(nil)

		raw := strings.Join(capTerm.writes, "")
		if !strings.Contains(raw, "base ") || !strings.Contains(tui.StripTerminalSequences(raw), "base   X        ") {
			t.Fatalf("expected composited 16-col row %q in rendered output %q", "base   X        ", raw)
		}

		// Verify rendered output on VirtualTerminal.
		vt := testkit.NewVirtualTerminal(16, 4)
		vtUI := tui.NewTuiMainScreen(vt, nil, nil)
		vtUI.AddChild(&tabWidthStaticComp{
			lines: []string{pad16("base 0"), pad16("base 1"), pad16("base 2")},
		})
		vtUI.ShowOverlay(&tabWidthStaticComp{lines: []string{"\tX"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 4},
			Row:   &tui.SizeValueCells{Value: 1},
			Col:   &tui.SizeValueCells{Value: 4},
		})
		vtUI.Start()
		vtUI.RenderNow(nil)
		vp := vt.FlushAndGetViewport()
		vtUI.Stop(nil)

		wantVp := []string{"base 0", "base   X", "base 2", ""}
		if !reflect.DeepEqual(vp, wantVp) {
			t.Fatalf("viewport = %#v, want %#v", vp, wantVp)
		}
	})

	t.Run("keeps tabs inside terminal control sequences byte-identical", func(t *testing.T) {
		controlSequences := []string{
			"\x1b]8;;https://example.test/a\tb\x07",
			"\x1b]0;window\ttitle\x1b\\",
			"\x1b_payload\tdata\x1b\\",
		}
		for _, seq := range controlSequences {
			got := tui.NormalizeTerminalOutput(seq + "label\ttext")
			want := seq + "label   text"
			if got != want {
				t.Fatalf("NormalizeTerminalOutput(%q) = %q, want %q", seq+"label\ttext", got, want)
			}
		}
	})

	t.Run("keeps slice helper and overlay segment widths consistent with visible width", func(t *testing.T) {
		text := "out 192M\t.pi/skill-tests/results-ha"
		slice := tui.SliceWithWidth(text, 0, 10, true)
		if slice.Text != "out 192M" || slice.Width != 8 || tui.VisibleWidth(slice.Text) != slice.Width {
			t.Fatalf("SliceWithWidth(tab) = %+v", slice)
		}

		// Does not carry APC cursor markers into later slices
		cursorLine := "ab" + tui.CursorMarker + "cdefghij"
		if got := tui.SliceWithWidth(cursorLine, 4, 2, true); got != (tui.SliceWithWidthResult{Text: "ef", Width: 2}) {
			t.Fatalf("SliceWithWidth(cursorLine,4,2) = %+v", got)
		}
		if got := tui.SliceWithWidth(cursorLine, 6, 4, true); got != (tui.SliceWithWidthResult{Text: "ghij", Width: 4}) {
			t.Fatalf("SliceWithWidth(cursorLine,6,4) = %+v", got)
		}

		// Continues to carry persistent SGR styling into later slices
		if got := tui.SliceWithWidth("\x1b[31mabcdefghij\x1b[0m", 4, 2, true); got != (tui.SliceWithWidthResult{Text: "\x1b[31mef", Width: 2}) {
			t.Fatalf("SliceWithWidth(red,4,2) = %+v", got)
		}

		segs := tui.ExtractSegments(text, 10, 13, 10, true)
		if segs.Before != "out 192M" || segs.BeforeWidth != 8 || tui.VisibleWidth(segs.Before) != segs.BeforeWidth {
			t.Fatalf("ExtractSegments(beforeEnd=10) = %+v", segs)
		}
		tabFits := tui.ExtractSegments(text, 11, 13, 10, true)
		if tabFits.Before != "out 192M\t" || tabFits.BeforeWidth != 11 || tui.VisibleWidth(tabFits.Before) != tabFits.BeforeWidth {
			t.Fatalf("ExtractSegments(beforeEnd=11) = %+v", tabFits)
		}
	})
}
