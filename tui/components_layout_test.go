package tui

import (
	"reflect"
	"strings"
	"testing"
)

type tcloDummyComponent struct {
	lines       []string
	invalidated int
	renderWidth int
}

func (c *tcloDummyComponent) Render(width int) []string {
	c.renderWidth = width
	return append([]string(nil), c.lines...)
}

func (c *tcloDummyComponent) Invalidate() {
	c.invalidated++
}

type tcloMockTUI struct {
	Container
	renders int
}

func (m *tcloMockTUI) Mode() TuiMode               { return TuiModeRegular }
func (m *tcloMockTUI) Terminal() Terminal          { return nil }
func (m *tcloMockTUI) OnDebug() func()             { return nil }
func (m *tcloMockTUI) SetOnDebug(func())           {}
func (m *tcloMockTUI) FullRedraws() int            { return 0 }
func (m *tcloMockTUI) GetShowHardwareCursor() bool { return false }
func (m *tcloMockTUI) SetShowHardwareCursor(bool)  {}
func (m *tcloMockTUI) GetClearOnShrink() bool      { return false }
func (m *tcloMockTUI) SetClearOnShrink(bool)       {}
func (m *tcloMockTUI) SetFocus(Component)          {}
func (m *tcloMockTUI) ShowOverlay(Component, *OverlayOptions) OverlayHandle {
	return nil
}
func (m *tcloMockTUI) HideOverlay()                                                 {}
func (m *tcloMockTUI) HasOverlay() bool                                             { return false }
func (m *tcloMockTUI) Start()                                                       {}
func (m *tcloMockTUI) Stop(*TuiStopOptions)                                         {}
func (m *tcloMockTUI) RenderNow(*bool)                                              {}
func (m *tcloMockTUI) RequestRender(*bool)                                          { m.renders++ }
func (m *tcloMockTUI) AddInputListener(TuiInputListener) func()                     { return func() {} }
func (m *tcloMockTUI) RemoveInputListener(TuiInputListener)                         {}
func (m *tcloMockTUI) OnTerminalColorSchemeChange(func(TerminalColorScheme)) func() { return func() {} }
func (m *tcloMockTUI) SetTerminalColorSchemeNotifications(bool)                     {}
func (m *tcloMockTUI) QueryTerminalColors(TuiQueryTerminalColorsOptions) (TerminalColors, error) {
	return TerminalColors{}, nil
}
func (m *tcloMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func tcloIntPtr(v int) *int                                       { return &v }
func tcloBoolPtr(v bool) *bool                                    { return &v }
func tcloAlignPtr(v StackLayoutAlign) *StackLayoutAlign           { return &v }
func tcloScrollBarPtr(v ScrollViewScrollbar) *ScrollViewScrollbar { return &v }
func tcloFollowPtr(v ScrollViewFollow) *ScrollViewFollow          { return &v }

func TestLayoutComponentsUnit_SpacerBoxStackScrollViewLoaderFlash(t *testing.T) {
	t.Run("Spacer renders requested empty lines", func(t *testing.T) {
		sp := NewSpacer(3)
		if got := sp.Render(20); !reflect.DeepEqual(got, []string{"", "", ""}) {
			t.Fatalf("Spacer(3).Render = %#v", got)
		}
		sp.SetLines(1)
		if got := sp.Render(20); !reflect.DeepEqual(got, []string{""}) {
			t.Fatalf("Spacer(1).Render = %#v", got)
		}
		sp.Invalidate()
	})

	t.Run("Box child management, padding bounds, and empty render", func(t *testing.T) {
		box := NewBox(2, 1, nil)
		if got := box.Render(20); len(got) != 0 {
			t.Fatalf("empty Box.Render = %#v, want []", got)
		}
		child := &tcloDummyComponent{lines: []string{"hi"}}
		box.AddChild(child)
		box.Invalidate()
		if child.invalidated != 1 {
			t.Fatalf("Box.Invalidate did not invalidate child")
		}
		// Point inside left/top padding must return nil without invoking DispatchMouseEvent
		if res := box.HandleMouse(TuiMouseEvent{X: 0, Y: 0, Width: 20, Height: 5}); res != nil {
			t.Fatalf("HandleMouse inside padding = %#v, want nil", res)
		}
		box.SetPaddingX(1)
		box.SetBgFn(func(s string) string { return s })
		box.RemoveChild(child)
		if len(box.Children) != 0 {
			t.Fatalf("RemoveChild failed: %d children remain", len(box.Children))
		}
		box.AddChild(child)
		box.Clear()
		if len(box.Children) != 0 {
			t.Fatalf("Clear failed: %d children remain", len(box.Children))
		}
	})

	t.Run("VStack and HStack LayoutNode and empty Render", func(t *testing.T) {
		vs := NewVStack(nil, &StackOptions{
			Gap:   tcloIntPtr(1),
			Align: tcloAlignPtr(StackLayoutAlignStretch),
		})
		node, ok := vs.LayoutNode().(*StackLayoutNode)
		if !ok || node.Type != LayoutNodeTypeVstack || len(node.Entries) != 0 || node.Gap != 1 {
			t.Fatalf("unexpected VStack.LayoutNode = %#v", vs.LayoutNode())
		}
		if got := vs.Render(10); len(got) != 0 {
			t.Fatalf("empty VStack.Render = %#v, want []", got)
		}

		hs := NewHStack(nil, nil)
		hnode, ok := hs.LayoutNode().(*StackLayoutNode)
		if !ok || hnode.Type != LayoutNodeTypeHstack {
			t.Fatalf("unexpected HStack.LayoutNode = %#v", hs.LayoutNode())
		}
		if got := hs.Render(10); len(got) != 0 {
			t.Fatalf("empty HStack.Render = %#v, want []", got)
		}
	})

	t.Run("ScrollView options, scrolling, follow, scrollbar, and Render", func(t *testing.T) {
		invalidAxis := ScrollViewAxis("horizontal")
		if _, err := NewScrollView(&tcloDummyComponent{}, &ScrollViewOptions{Axis: &invalidAxis}); err == nil {
			t.Fatal("expected error for non-vertical ScrollViewAxis")
		}

		child := &tcloDummyComponent{lines: []string{"l0", "l1", "l2", "l3", "l4"}}
		sv, err := NewScrollView(child, &ScrollViewOptions{
			Follow:    tcloFollowPtr(ScrollViewFollowEnd),
			Primary:   tcloBoolPtr(true),
			Scrollbar: tcloScrollBarPtr(ScrollViewScrollbarAlways),
		})
		if err != nil {
			t.Fatalf("NewScrollView: %v", err)
		}
		if !sv.Primary() || !sv.FollowEnd() || sv.Scrollbar() != ScrollViewScrollbarAlways {
			t.Fatalf("unexpected ScrollView accessors")
		}
		if w := sv.GetContentWidth(20); w != 19 {
			t.Fatalf("GetContentWidth(20) with right scrollbar = %d, want 19", w)
		}
		sv.UpdateLayout(5, 3, nil)
		if !sv.IsFollowingEnd() || sv.ScrollTop() != 2 || sv.ViewportHeight() != 3 {
			t.Fatalf("after UpdateLayout followEnd=%v top=%d vh=%d", sv.IsFollowingEnd(), sv.ScrollTop(), sv.ViewportHeight())
		}
		sv.ScrollBy(-1)
		if sv.ScrollTop() != 1 || sv.IsFollowingEnd() {
			t.Fatalf("after ScrollBy(-1): top=%d following=%v", sv.ScrollTop(), sv.IsFollowingEnd())
		}
		sv.ScrollToEnd()
		if sv.ScrollTop() != 2 || !sv.IsFollowingEnd() {
			t.Fatalf("after ScrollToEnd: top=%d following=%v", sv.ScrollTop(), sv.IsFollowingEnd())
		}
		sv.ScrollToStart()
		if sv.ScrollTop() != 0 {
			t.Fatalf("after ScrollToStart: top=%d, want 0", sv.ScrollTop())
		}
		rendered := sv.Render(10)
		wantPadded := []string{"l0 ", "l1 ", "l2 ", "l3 ", "l4 "}
		if len(rendered) != len(wantPadded) || rendered[0] != "l0 " || rendered[4] != "l4 " {
			t.Fatalf("ScrollView.Render(10) = %#v, want %#v", rendered, wantPadded)
		}
		sv.SetScrollbar(ScrollViewScrollbarHidden)
		if w := sv.GetContentWidth(20); w != 20 {
			t.Fatalf("GetContentWidth(20) hidden = %d, want 20", w)
		}
		if plain := sv.Render(20); len(plain) != 5 || plain[0] != "l0" {
			t.Fatalf("ScrollView.Render(20) hidden = %#v, want unpadded lines", plain)
		}
	})

	t.Run("Loader, CancellableLoader, FlashContainer, Text, Image unit states", func(t *testing.T) {
		ui := &tcloMockTUI{}
		zero := int64(0)
		ld := NewLoader(ui, func(s string) string { return s }, func(s string) string { return s }, "Working", &LoaderIndicatorOptions{
			Frames:     []string{},
			IntervalMs: &zero,
		})
		ld.Stop()

		cl := NewCancellableLoader(ui, nil, nil, "Wait", &LoaderIndicatorOptions{Frames: []string{}})
		if cl.Aborted() || cl.Signal().Err() != nil {
			t.Fatal("new CancellableLoader started aborted")
		}
		cl.Dispose()

		flash := NewAltScreenFlashContainer(func() { ui.RequestRender(nil) })
		if got := flash.Render(20); len(got) != 0 {
			t.Fatalf("empty flash Render = %#v, want []", got)
		}
		flash.Flash("copied", tcloInt64Ptr(500))
		flash.Invalidate()
		flash.Dispose()

		txt := NewText("", 1, 1, nil)
		if got := txt.Render(20); len(got) != 0 {
			t.Fatalf("empty Text.Render = %#v, want []", got)
		}

		imgId := 42
		img := NewImage("AAAA", "image/png", ImageTheme{FallbackColor: func(s string) string { return s }}, &ImageOptions{ImageId: &imgId}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
		if got := img.GetImageId(); got == nil || *got != 42 {
			t.Fatalf("GetImageId = %v, want 42", got)
		}
		img.Invalidate()
	})
}

func tcloInt64Ptr(v int64) *int64 { return &v }

func TestTextAndBoxAndVStackAndHStack_Render(t *testing.T) {
	t.Run("VStack children, gap, remove, and Render", func(t *testing.T) {
		c1 := &tcloDummyComponent{lines: []string{"a", "b"}}
		c2 := &tcloDummyComponent{lines: []string{"c"}}
		vs := NewVStack([]StackChild{
			&StackChildComponent{Component: c1},
			&StackChildEntry{Entry: StackEntry{
				Component: c2,
				StackEntryOptions: StackEntryOptions{
					Basis: &StackLayoutBasisCells{Value: 2},
					Grow:  tcloIntPtr(1),
				},
			}},
		}, &StackOptions{
			Gap:   tcloIntPtr(1),
			Align: tcloAlignPtr(StackLayoutAlignStretch),
		})
		if got := vs.Render(10); !reflect.DeepEqual(got, []string{"a", "b", "", "c", ""}) {
			t.Fatalf("VStack.Render = %#v, want [a b '' c '']", got)
		}
		vs.RemoveChild(c1)
		if len(vs.Children()) != 1 {
			t.Fatalf("VStack RemoveChild len = %d, want 1", len(vs.Children()))
		}
		vs.Clear()
	})
	t.Run("Text wraps and pads with customBgFn", func(t *testing.T) {
		txt := NewText("hello world", 1, 1, func(s string) string { return "[" + s + "]" })
		lines := txt.Render(20)
		if len(lines) != 3 {
			t.Fatalf("len(lines) = %d, want 3", len(lines))
		}
		for i, line := range lines {
			if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
				t.Errorf("line %d missing custom bg wrap: %q", i, line)
			}
		}
	})

	t.Run("Box renders padded lines and HStack aligns side by side", func(t *testing.T) {
		box := NewBox(2, 1, nil)
		box.AddChild(NewText("hi", 0, 0, nil))
		lines := box.Render(10)
		if len(lines) != 3 {
			t.Fatalf("Box.Render len = %d, want 3", len(lines))
		}

		hs := NewHStack([]StackChild{
			&StackChildComponent{Component: &tcloDummyComponent{lines: []string{"L"}}},
			&StackChildComponent{Component: &tcloDummyComponent{lines: []string{"R"}}},
		}, &StackOptions{Gap: tcloIntPtr(2)})
		hl := hs.Render(10)
		if len(hl) != 1 || VisibleWidth(hl[0]) == 0 {
			t.Fatalf("HStack.Render = %#v", hl)
		}
	})

	t.Run("Image.Render falls back cleanly when images disabled", func(t *testing.T) {
		SetCapabilities(TerminalCapabilities{Images: ""})
		defer ResetCapabilitiesCache()
		img := NewImage("AAAA", "image/png", ImageTheme{FallbackColor: func(s string) string { return s }}, nil, &ImageDimensions{WidthPx: 100, HeightPx: 50})
		lines := img.Render(40)
		if len(lines) != 1 || !strings.Contains(lines[0], "image") {
			t.Fatalf("Image.Render fallback = %#v", lines)
		}
	})
}
