package tui_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type overlayImgStaticComp struct {
	lines []string
}

func (c *overlayImgStaticComp) Render(width int) []string { return c.lines }
func (c *overlayImgStaticComp) Invalidate()               {}

type overlayImgCaptureTerm struct {
	mu     sync.Mutex
	writes []string
}

func (m *overlayImgCaptureTerm) Start(onInput func(data string), onResize func()) {}
func (m *overlayImgCaptureTerm) Stop()                                            {}
func (m *overlayImgCaptureTerm) DrainInput(maxMs *int64, idleMs *int64) error     { return nil }
func (m *overlayImgCaptureTerm) Write(data string) {
	m.mu.Lock()
	m.writes = append(m.writes, data)
	m.mu.Unlock()
}
func (m *overlayImgCaptureTerm) Columns() int                              { return 60 }
func (m *overlayImgCaptureTerm) Rows() int                                 { return 12 }
func (m *overlayImgCaptureTerm) KittyProtocolActive() bool                 { return false }
func (m *overlayImgCaptureTerm) MoveBy(lines int)                          {}
func (m *overlayImgCaptureTerm) HideCursor()                               {}
func (m *overlayImgCaptureTerm) ShowCursor()                               {}
func (m *overlayImgCaptureTerm) ClearLine()                                {}
func (m *overlayImgCaptureTerm) ClearFromCursor()                          {}
func (m *overlayImgCaptureTerm) ClearScreen()                              {}
func (m *overlayImgCaptureTerm) SetTitle(title string)                     {}
func (m *overlayImgCaptureTerm) SetProgress(active bool)                   {}
func (m *overlayImgCaptureTerm) SetProgramStatus(status tui.ProgramStatus) {}

func (m *overlayImgCaptureTerm) takeWrites() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := strings.Join(m.writes, "")
	m.writes = nil
	return out
}

func TestOverlaysOverTerminalImages(t *testing.T) {
	t.Run("GetImageLineExtent measures Kitty and iTerm2 image rectangles", func(t *testing.T) {
		if got := tui.GetImageLineExtent("plain line"); got != nil {
			t.Fatalf("plain line extent = %+v, want nil", got)
		}

		kittyLine := "  \x1b_Ga=T,f=100,i=1,c=12,r=4;AAAA\x1b\\"
		kittyExtent := tui.GetImageLineExtent(kittyLine)
		if kittyExtent == nil || kittyExtent.FirstRow != 0 || kittyExtent.LastRow != 3 || kittyExtent.StartCol != 2 || kittyExtent.EndCol != 14 {
			t.Fatalf("kittyExtent = %+v, want FirstRow=0 LastRow=3 StartCol=2 EndCol=14", kittyExtent)
		}

		// iTerm2 cursor-up escape sits before the OSC 1337 File= sequence.
		itermLine := "\x1b[3A\x1b]1337;File=inline=1;width=10;height=4:AAAA\x07"
		itermExtent := tui.GetImageLineExtent(itermLine)
		if itermExtent == nil || itermExtent.FirstRow != -3 || itermExtent.LastRow != 0 || itermExtent.StartCol != 0 || itermExtent.EndCol != 10 {
			t.Fatalf("itermExtent = %+v, want FirstRow=-3 LastRow=0 StartCol=0 EndCol=10", itermExtent)
		}
	})

	t.Run("TuiMainScreen strips overlapping image sequences under visible overlays only", func(t *testing.T) {
		term := &overlayImgCaptureTerm{}
		ui := tui.NewTuiMainScreen(term, nil, nil)

		imgLine := "\x1b_Ga=T,f=100,i=7,c=20,r=4;AAAA\x1b\\"
		ui.AddChild(&overlayImgStaticComp{
			lines: []string{
				"header",
				imgLine,
				"spacer 2",
				"spacer 3",
				"spacer 4",
				"footer",
			},
		})
		ui.Start()
		defer ui.Stop(nil)

		// Non-overlapping overlay on column 30 keeps the Kitty image intact.
		farOverlay := ui.ShowOverlay(&overlayImgStaticComp{lines: []string{"FAR"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 8},
			Row:   &tui.SizeValueCells{Value: 1},
			Col:   &tui.SizeValueCells{Value: 30},
		})
		ui.RenderNow(nil)
		out := term.takeWrites()
		if !strings.Contains(out, "\x1b_G") {
			t.Fatalf("expected non-overlapping render to keep Kitty sequence, got %q", out)
		}
		farOverlay.Hide()

		// Overlapping overlay across rows 2..3 cols 5..15 suppresses the overlapping Kitty image.
		overOverlay := ui.ShowOverlay(&overlayImgStaticComp{lines: []string{"MODAL", "BODY"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 10},
			Row:   &tui.SizeValueCells{Value: 2},
			Col:   &tui.SizeValueCells{Value: 5},
		})
		force := true
		ui.RenderNow(&force)
		out = term.takeWrites()
		if strings.Contains(out, "\x1b_Ga=T") {
			t.Fatalf("rendered output still contains Kitty placement under overlapping overlay: %q", out)
		}
		if !strings.Contains(out, "\x1b_Ga=d,d=I,i=7") {
			t.Fatalf("expected Kitty image delete escape for suppressed image i=7, got %q", out)
		}
		if !strings.Contains(out, "MODAL") || !strings.Contains(out, "BODY") {
			t.Fatalf("expected overlapping overlay text in output: %q", out)
		}

		// Temporarily hiding the overlay lets the image render again.
		overOverlay.SetHidden(true)
		ui.RenderNow(&force)
		out = term.takeWrites()
		if !strings.Contains(out, "\x1b_Ga=T") {
			t.Fatalf("expected hidden overlay not to strip Kitty image placement, got %q", out)
		}
	})
}
