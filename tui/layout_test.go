package tui_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type lTextComp struct {
	lines []string
}

func (c *lTextComp) Render(width int) []string {
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}
func (c *lTextComp) Invalidate() {}

type lScrollState struct {
	scrollTop      int
	primary        bool
	overscroll     tui.ScrollLayoutOverscroll
	viewportHeight int
	contentPadding int
	lastContentH   int
	lastViewportH  int
	renderReqCount int
}

func (s *lScrollState) ScrollTop() int                         { return s.scrollTop }
func (s *lScrollState) Primary() bool                          { return s.primary }
func (s *lScrollState) Overscroll() tui.ScrollLayoutOverscroll { return s.overscroll }
func (s *lScrollState) ViewportHeight() int                    { return s.viewportHeight }
func (s *lScrollState) GetContentWidth(width int) int          { return max(0, width-s.contentPadding) }
func (s *lScrollState) UpdateLayout(contentHeight int, viewportHeight int, requestRender func()) {
	s.lastContentH = contentHeight
	s.lastViewportH = viewportHeight
	s.viewportHeight = viewportHeight
	if requestRender != nil {
		s.renderReqCount++
	}
}

type lScrollComp struct {
	child tui.Component
	state tui.ScrollLayoutState
}

func (c *lScrollComp) Render(width int) []string { return c.child.Render(width) }
func (c *lScrollComp) Invalidate()               { c.child.Invalidate() }
func (c *lScrollComp) LayoutNode() tui.LayoutNode {
	return &tui.ScrollLayoutNode{
		Type:      tui.LayoutNodeTypeScroll,
		Component: c.child,
		State:     c.state,
	}
}

func TestLayoutUtilities(t *testing.T) {
	t.Run("GetLayoutNode reads LayoutComponent interface and handles nil safely", func(t *testing.T) {
		if got := tui.GetLayoutNode(nil); got != nil {
			t.Fatalf("GetLayoutNode(nil) = %v, want nil", got)
		}
		if got := tui.GetLayoutNode(&lTextComp{}); got != nil {
			t.Fatalf("GetLayoutNode(plain) = %v, want nil", got)
		}
		scroll := &lScrollComp{child: &lTextComp{}, state: &lScrollState{}}
		if got := tui.GetLayoutNode(scroll); got == nil {
			t.Fatal("GetLayoutNode(scroll) = nil, want ScrollLayoutNode")
		}
	})

	t.Run("CropKittyImageLine adjusts row count or returns unchanged out-of-range", func(t *testing.T) {
		tui.RegisterKittyImageMetadata(tui.KittyImageMetadata{
			ImageId:  55,
			WidthPx:  100,
			HeightPx: 120,
			Columns:  10,
			Rows:     6,
		})
		line := "  \x1b_Ga=T,f=100,i=55,c=10,r=6;AAAA\x1b\\"
		cropped := tui.CropKittyImageLine(line, 2, 3)
		rows := tui.GetKittyImagePlacementRows(cropped)
		if rows == nil || *rows != 3 || !strings.Contains(cropped, "y=40") {
			t.Fatalf("cropped placement rows=%v (cropped=%q), want r=3 and y=40", rows, cropped)
		}
		if unchanged := tui.CropKittyImageLine(line, -1, 3); unchanged != line {
			t.Fatalf("negative hiddenRows mutated line: %q", unchanged)
		}
		if unchanged := tui.CropKittyImageLine("plain", 1, 1); unchanged != "plain" {
			t.Fatalf("non-kitty line mutated: %q", unchanged)
		}
	})
}

func TestRenderLayoutFrame(t *testing.T) {
	t.Run("renders leaf component and floors width/height to at least 1", func(t *testing.T) {
		leaf := &lTextComp{lines: []string{"alpha", "beta"}}
		frame := tui.RenderLayoutFrame(leaf, 0, 0, nil)
		if frame.Width != 1 || frame.Height != 1 {
			t.Fatalf("floored dims = (%d,%d), want (1,1)", frame.Width, frame.Height)
		}
		if !reflect.DeepEqual(frame.Lines, []string{"alpha"}) {
			t.Fatalf("frame.Lines = %#v, want [alpha]", frame.Lines)
		}
	})

	t.Run("renders ScrollLayoutNode viewport, updates state, translates clip/hits, and handles empty helpers", func(t *testing.T) {
		content := &lTextComp{
			lines: []string{"row 0", "row 1", "row 2", "row 3", "row 4", "row 5"},
		}
		state := &lScrollState{
			scrollTop:      2,
			primary:        true,
			overscroll:     tui.ScrollLayoutOverscrollContain,
			contentPadding: 2,
		}
		scrollRoot := &lScrollComp{child: content, state: state}

		frame := tui.RenderLayoutFrame(scrollRoot, 20, 3, func() {})
		if frame.Width != 20 || frame.Height != 3 {
			t.Fatalf("unexpected frame dims: %+v", frame)
		}
		if state.lastContentH != 6 || state.lastViewportH != 3 {
			t.Fatalf("UpdateLayout got (%d,%d), want (6,3)", state.lastContentH, state.lastViewportH)
		}
		wantPlain := []string{"row 2", "row 3", "row 4"}
		for i, line := range frame.Lines {
			got := strings.TrimRight(tui.StripTerminalSequences(line), " ")
			if got != wantPlain[i] {
				t.Fatalf("frame.Lines[%d] plain = %q, want %q (raw=%q)", i, got, wantPlain[i], line)
			}
			if w := tui.VisibleWidth(line); w != 20 {
				t.Fatalf("frame.Lines[%d] width = %d, want 20", i, w)
			}
		}

		hits := tui.GetLayoutBoxesAt(frame, 1, 1)
		if len(hits) != 2 || hits[0].Component != content || hits[1].Component != scrollRoot {
			t.Fatalf("GetLayoutBoxesAt(1,1) = %#v", hits)
		}
		// Translated child Y should be -scrollTop (-2).
		if hits[0].Rect.Y != -2 || hits[0].Rect.Width != 18 {
			t.Fatalf("child rect = %+v, want Y=-2 Width=18", hits[0].Rect)
		}
		if misses := tui.GetLayoutBoxesAt(frame, 20, 1); len(misses) != 0 {
			t.Fatalf("expected out-of-bounds hit empty, got %d", len(misses))
		}
		if sv := tui.GetScrollViewBox(frame, nil); sv != nil {
			t.Fatalf("GetScrollViewBox(nil) = %v, want nil", sv)
		}
		if svs := tui.GetScrollViewsAt(frame, 1, 1); len(svs) != 0 {
			t.Fatalf("GetScrollViewsAt expected empty without *ScrollView, got %d", len(svs))
		}
		if geom := tui.GetScrollbarGeometry(nil, false); geom != nil {
			t.Fatalf("GetScrollbarGeometry(nil) = %+v, want nil", geom)
		}
	})

	t.Run("sub-width scroll viewport composites wide CJK lines cleanly to frame width", func(t *testing.T) {
		wide := &lTextComp{lines: []string{strings.Repeat("あ", 15)}}
		scrollRoot := &lScrollComp{
			child: wide,
			state: &lScrollState{contentPadding: 3},
		}
		frame := tui.RenderLayoutFrame(scrollRoot, 10, 2, nil)
		if w := tui.VisibleWidth(frame.Lines[0]); w != 10 {
			t.Fatalf("frame.Lines[0] width = %d, want 10 (%q)", w, frame.Lines[0])
		}
		trimmed := strings.TrimRight(tui.StripTerminalSequences(frame.Lines[0]), " ")
		if trimmed != "あああ" {
			t.Fatalf("sub-width clipped CJK text = %q, want %q (7-col budget holds 3 wide chars)", trimmed, "あああ")
		}
	})
}
