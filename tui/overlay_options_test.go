package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

func ooIntPtr(v int) *int                                { return &v }
func ooBoolPtr(v bool) *bool                             { return &v }
func ooAnchorPtr(a tui.OverlayAnchor) *tui.OverlayAnchor { return &a }

type ooStaticOverlay struct {
	lines          []string
	requestedWidth int
}

func (o *ooStaticOverlay) Render(width int) []string {
	o.requestedWidth = width
	return o.lines
}
func (o *ooStaticOverlay) Invalidate() {}

type ooEmptyContent struct{}

func (e *ooEmptyContent) Render(width int) []string { return []string{} }
func (e *ooEmptyContent) Invalidate()               {}

type ooStyledContent struct{}

func (s *ooStyledContent) Render(width int) []string {
	line := "\x1b[1m\x1b[38;2;255;0;0m" + strings.Repeat("X", width) + "\x1b[0m"
	return []string{line, line, line}
}
func (s *ooStyledContent) Invalidate() {}

type ooHyperlinkContent struct{}

func (h *ooHyperlinkContent) Render(width int) []string {
	link := "\x1b]8;;file:///path/to/file.ts\x07file.ts\x1b]8;;\x07"
	fill := max(0, width-30)
	line := "See " + link + " for details " + strings.Repeat("X", fill)
	return []string{line, line, line}
}
func (h *ooHyperlinkContent) Invalidate() {}

type ooCursorTrackingTerminal struct {
	*testkit.VirtualTerminal
	cursorVisible bool
}

func newOoCursorTrackingTerminal(cols, rows int) *ooCursorTrackingTerminal {
	return &ooCursorTrackingTerminal{
		VirtualTerminal: testkit.NewVirtualTerminal(cols, rows),
		cursorVisible:   true,
	}
}

func (t *ooCursorTrackingTerminal) HideCursor() {
	t.cursorVisible = false
	t.VirtualTerminal.HideCursor()
}

func (t *ooCursorTrackingTerminal) ShowCursor() {
	t.cursorVisible = true
	t.VirtualTerminal.ShowCursor()
}

func ooRenderAndFlush(ui tui.TUI, term *testkit.VirtualTerminal) {
	ui.RenderNow(ooBoolPtr(true))
	term.Flush()
}

func TestOverlayOptions(t *testing.T) {
	t.Run("width overflow protection", func(t *testing.T) {
		t.Run("truncates overlay lines that exceed declared width", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			overlay := &ooStaticOverlay{lines: []string{strings.Repeat("X", 100)}}
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(overlay, &tui.OverlayOptions{Width: &tui.SizeValueCells{Value: 20}})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if len(vp) == 0 {
				t.Fatal("expected non-empty viewport")
			}
		})

		t.Run("handles overlay with complex ANSI sequences without crashing", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			complexLine := "\x1b[48;2;40;50;40m \x1b[38;2;128;128;128mSome styled content\x1b[39m\x1b[49m" +
				"\x1b]8;;http://example.com\x07link\x1b]8;;\x07" +
				strings.Repeat(" more content ", 10)
			overlay := &ooStaticOverlay{lines: []string{complexLine, complexLine, complexLine}}
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(overlay, &tui.OverlayOptions{Width: &tui.SizeValueCells{Value: 60}})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if len(term.GetViewport()) == 0 {
				t.Fatal("expected non-empty viewport")
			}
		})

		t.Run("handles overlay composited on styled base content", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooStyledContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"OVERLAY"}}, &tui.OverlayOptions{
				Width:  &tui.SizeValueCells{Value: 20},
				Anchor: ooAnchorPtr(tui.OverlayAnchorCenter),
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(strings.Join(term.GetViewport(), "\n"), "OVERLAY") {
				t.Fatalf("overlay not visible in %#v", term.GetViewport())
			}
		})

		t.Run("handles wide characters at overlay boundary", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			overlay := &ooStaticOverlay{lines: []string{"中文日本語한글テスト漢字"}}
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(overlay, &tui.OverlayOptions{Width: &tui.SizeValueCells{Value: 15}})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if len(term.GetViewport()) == 0 {
				t.Fatal("expected viewport rows")
			}
		})

		t.Run("handles overlay positioned at terminal edge", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{strings.Repeat("X", 50)}}, &tui.OverlayOptions{
				Col:   &tui.SizeValueCells{Value: 60},
				Width: &tui.SizeValueCells{Value: 20},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if len(term.GetViewport()) == 0 {
				t.Fatal("expected viewport rows")
			}
		})

		t.Run("handles overlay on base content with OSC sequences", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooHyperlinkContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"OVERLAY-TEXT"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorCenter),
				Width:  &tui.SizeValueCells{Value: 20},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(strings.Join(term.GetViewport(), "\n"), "OVERLAY-TEXT") {
				t.Fatalf("missing OVERLAY-TEXT in %#v", term.GetViewport())
			}
		})
	})

	t.Run("width percentage", func(t *testing.T) {
		t.Run("renders overlay at percentage of terminal width", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(100, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			overlay := &ooStaticOverlay{lines: []string{"test"}}
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(overlay, &tui.OverlayOptions{Width: &tui.SizeValuePercent{Text: "50%"}})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if overlay.requestedWidth != 50 {
				t.Fatalf("requestedWidth = %d, want 50", overlay.requestedWidth)
			}
		})

		t.Run("respects minWidth when widthPercent results in smaller width", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(100, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			overlay := &ooStaticOverlay{lines: []string{"test"}}
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(overlay, &tui.OverlayOptions{
				Width:    &tui.SizeValuePercent{Text: "10%"},
				MinWidth: ooIntPtr(30),
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if overlay.requestedWidth != 30 {
				t.Fatalf("requestedWidth = %d, want 30", overlay.requestedWidth)
			}
		})
	})

	t.Run("anchor positioning", func(t *testing.T) {
		t.Run("positions overlay at top-left", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"TOP-LEFT"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if !strings.HasPrefix(vp[0], "TOP-LEFT") {
				t.Fatalf("vp[0] = %q, want prefix TOP-LEFT", vp[0])
			}
		})

		t.Run("positions overlay at bottom-right", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"BTM-RIGHT"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorBottomRight),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if !strings.HasSuffix(strings.TrimRight(vp[23], " "), "BTM-RIGHT") {
				t.Fatalf("vp[23] = %q, want suffix BTM-RIGHT", vp[23])
			}
		})

		t.Run("positions overlay at top-center", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"CENTERED"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopCenter),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			col := strings.Index(vp[0], "CENTERED")
			if col < 30 || col > 40 {
				t.Fatalf("CENTERED col = %d, want [30,40] (vp[0]=%q)", col, vp[0])
			}
		})
	})

	t.Run("margin", func(t *testing.T) {
		t.Run("clamps negative margins to zero", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"NEG-MARGIN"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 12},
				Margin: &tui.OverlayMargin{Top: ooIntPtr(-5), Left: ooIntPtr(-10), Right: ooIntPtr(0), Bottom: ooIntPtr(0)},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if vp := term.GetViewport(); !strings.HasPrefix(vp[0], "NEG-MARGIN") {
				t.Fatalf("vp[0] = %q, want prefix NEG-MARGIN", vp[0])
			}
		})

		t.Run("respects margin as number", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"MARGIN"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
				Margin: &tui.OverlayMarginNumber{Value: 5},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if strings.Contains(vp[0], "MARGIN") || strings.Contains(vp[4], "MARGIN") {
				t.Fatalf("margin=5 unexpectedly drew on rows 0..4: %#v", vp[:6])
			}
			if col := strings.Index(vp[5], "MARGIN"); col != 5 {
				t.Fatalf("MARGIN col = %d, want 5 (vp[5]=%q)", col, vp[5])
			}
		})

		t.Run("respects margin object", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"MARGIN"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
				Margin: &tui.OverlayMargin{Top: ooIntPtr(2), Left: ooIntPtr(3), Right: ooIntPtr(0), Bottom: ooIntPtr(0)},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if col := strings.Index(vp[2], "MARGIN"); col != 3 {
				t.Fatalf("MARGIN col = %d, want 3 (vp[2]=%q)", col, vp[2])
			}
		})
	})

	t.Run("offset", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&ooEmptyContent{})
		ui.ShowOverlay(&ooStaticOverlay{lines: []string{"OFFSET"}}, &tui.OverlayOptions{
			Anchor:  ooAnchorPtr(tui.OverlayAnchorTopLeft),
			Width:   &tui.SizeValueCells{Value: 10},
			OffsetX: ooIntPtr(10),
			OffsetY: ooIntPtr(5),
		})
		ui.Start()
		defer ui.Stop(nil)
		ooRenderAndFlush(ui, term)
		vp := term.GetViewport()
		if col := strings.Index(vp[5], "OFFSET"); col != 10 {
			t.Fatalf("OFFSET col = %d, want 10 (vp[5]=%q)", col, vp[5])
		}
	})

	t.Run("percentage positioning", func(t *testing.T) {
		t.Run("positions with rowPercent and colPercent", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"PCT"}}, &tui.OverlayOptions{
				Width: &tui.SizeValueCells{Value: 10},
				Row:   &tui.SizeValuePercent{Text: "50%"},
				Col:   &tui.SizeValuePercent{Text: "50%"},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			foundRow := -1
			for i, line := range vp {
				if strings.Contains(line, "PCT") {
					foundRow = i
					break
				}
			}
			if foundRow < 10 || foundRow > 13 {
				t.Fatalf("foundRow = %d, want [10,13]", foundRow)
			}
		})

		t.Run("rowPercent 0 positions at top", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"TOP"}}, &tui.OverlayOptions{
				Width: &tui.SizeValueCells{Value: 10},
				Row:   &tui.SizeValuePercent{Text: "0%"},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(term.GetViewport()[0], "TOP") {
				t.Fatalf("expected TOP on row 0: %q", term.GetViewport()[0])
			}
		})

		t.Run("rowPercent 100 positions at bottom", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"BOTTOM"}}, &tui.OverlayOptions{
				Width: &tui.SizeValueCells{Value: 10},
				Row:   &tui.SizeValuePercent{Text: "100%"},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(term.GetViewport()[23], "BOTTOM") {
				t.Fatalf("expected BOTTOM on row 23: %q", term.GetViewport()[23])
			}
		})
	})

	t.Run("maxHeight", func(t *testing.T) {
		t.Run("truncates overlay to maxHeight", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"Line 1", "Line 2", "Line 3", "Line 4", "Line 5"}}, &tui.OverlayOptions{
				MaxHeight: &tui.SizeValueCells{Value: 3},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			content := strings.Join(term.GetViewport(), "\n")
			if !strings.Contains(content, "Line 1") || !strings.Contains(content, "Line 3") || strings.Contains(content, "Line 4") {
				t.Fatalf("unexpected maxHeight truncation:\n%s", content)
			}
		})

		t.Run("truncates overlay to maxHeightPercent", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 10)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8", "L9", "L10"}}, &tui.OverlayOptions{
				MaxHeight: &tui.SizeValuePercent{Text: "50%"},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			content := strings.Join(term.GetViewport(), "\n")
			if !strings.Contains(content, "L1") || !strings.Contains(content, "L5") || strings.Contains(content, "L6") {
				t.Fatalf("unexpected maxHeightPercent truncation:\n%s", content)
			}
		})
	})

	t.Run("absolute positioning overrides anchor", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.AddChild(&ooEmptyContent{})
		ui.ShowOverlay(&ooStaticOverlay{lines: []string{"ABSOLUTE"}}, &tui.OverlayOptions{
			Anchor: ooAnchorPtr(tui.OverlayAnchorBottomRight),
			Row:    &tui.SizeValueCells{Value: 3},
			Col:    &tui.SizeValueCells{Value: 5},
			Width:  &tui.SizeValueCells{Value: 10},
		})
		ui.Start()
		defer ui.Stop(nil)
		ooRenderAndFlush(ui, term)
		vp := term.GetViewport()
		if col := strings.Index(vp[3], "ABSOLUTE"); col != 5 {
			t.Fatalf("ABSOLUTE col = %d, want 5 (vp[3]=%q)", col, vp[3])
		}
	})

	t.Run("stacked overlays", func(t *testing.T) {
		t.Run("renders multiple overlays with later ones on top", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"FIRST-OVERLAY"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 20},
			})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"SECOND"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(term.GetViewport()[0], "SECOND") {
				t.Fatalf("expected SECOND on row 0: %q", term.GetViewport()[0])
			}
		})

		t.Run("handles overlays at different positions without interference", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"TOP-LEFT"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 15},
			})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"BTM-RIGHT"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorBottomRight),
				Width:  &tui.SizeValueCells{Value: 15},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			vp := term.GetViewport()
			if !strings.Contains(vp[0], "TOP-LEFT") || !strings.Contains(vp[23], "BTM-RIGHT") {
				t.Fatalf("unexpected viewports vp[0]=%q vp[23]=%q", vp[0], vp[23])
			}
		})

		t.Run("properly hides overlays in stack order", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.AddChild(&ooEmptyContent{})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"FIRST"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.ShowOverlay(&ooStaticOverlay{lines: []string{"SECOND"}}, &tui.OverlayOptions{
				Anchor: ooAnchorPtr(tui.OverlayAnchorTopLeft),
				Width:  &tui.SizeValueCells{Value: 10},
			})
			ui.Start()
			defer ui.Stop(nil)
			ooRenderAndFlush(ui, term)
			if !strings.Contains(term.GetViewport()[0], "SECOND") {
				t.Fatal("SECOND should be visible initially")
			}

			ui.HideOverlay()
			ooRenderAndFlush(ui, term)
			if !strings.Contains(term.GetViewport()[0], "FIRST") {
				t.Fatalf("FIRST should be visible after HideOverlay: %q", term.GetViewport()[0])
			}
		})
	})

	t.Run("hiding after stop leaves cursor visible", func(t *testing.T) {
		t1 := newOoCursorTrackingTerminal(80, 24)
		ui1 := tui.NewTuiMainScreen(t1, nil, nil)
		ui1.Start()
		ui1.ShowOverlay(&ooStaticOverlay{lines: []string{"OVERLAY"}}, &tui.OverlayOptions{NonCapturing: ooBoolPtr(true)})
		ui1.Stop(nil)
		ui1.HideOverlay()
		if !t1.cursorVisible {
			t.Fatal("HideOverlay after Stop hid cursor")
		}

		t2 := newOoCursorTrackingTerminal(80, 24)
		ui2 := tui.NewTuiMainScreen(t2, nil, nil)
		ui2.Start()
		h := ui2.ShowOverlay(&ooStaticOverlay{lines: []string{"OVERLAY"}}, &tui.OverlayOptions{NonCapturing: ooBoolPtr(true)})
		ui2.Stop(nil)
		h.Hide()
		if !t2.cursorVisible {
			t.Fatal("handle.Hide after Stop hid cursor")
		}
	})
}
