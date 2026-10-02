// Ported from packages/tui/src/components/scroll-view.ts (pi v1.0.0).

package tui

import (
	"sync"
	"time"
)

// ScrollViewScrollbar is where a scroll view draws its bar.
type ScrollViewScrollbar string

const (
	ScrollViewScrollbarHidden ScrollViewScrollbar = "hidden"
	ScrollViewScrollbarAuto   ScrollViewScrollbar = "auto"
	ScrollViewScrollbarAlways ScrollViewScrollbar = "always"
)

// ScrollViewAxis is the scroll axis. Only vertical is supported.
type ScrollViewAxis string

const (
	ScrollViewAxisVertical ScrollViewAxis = "vertical"
)

// ScrollViewFollow is whether the view sticks to the content end.
type ScrollViewFollow string

const (
	ScrollViewFollowNone ScrollViewFollow = "none"
	ScrollViewFollowEnd  ScrollViewFollow = "end"
)

// ScrollViewOptions configures a ScrollView.
// Nil Axis means vertical. Any other axis makes NewScrollView return an error.
// Nil Follow means none. Nil Primary means false.
// Nil Overscroll means chain (ScrollLayoutOverscrollChain), not the zero string.
// Nil Scrollbar means hidden, not the zero string.
// Nil style funcs mean the default dim track and white thumb wrappers.
// Nil ScrollbarHideDelayMs means 1000. The zero int64 is not 1000.
// Field order matches the ScrollViewOptions interface.
type ScrollViewOptions struct {
	Axis                 *ScrollViewAxis          `json:"axis,omitzero"`
	Follow               *ScrollViewFollow        `json:"follow,omitzero"`
	Primary              *bool                    `json:"primary,omitzero"`
	Overscroll           *ScrollLayoutOverscroll  `json:"overscroll,omitzero"`
	Scrollbar            *ScrollViewScrollbar     `json:"scrollbar,omitzero"`
	ScrollbarTrackStyle  func(text string) string `json:"-"`
	ScrollbarThumbStyle  func(text string) string `json:"-"`
	ScrollbarHideDelayMs *int64                   `json:"scrollbarHideDelayMs,omitzero"`
}

// ScrollViewScrollToOptions is the optional argument of ScrollTo.
// Nil DisableFollow means false.
type ScrollViewScrollToOptions struct {
	// DisableFollow keeps follow-end disabled even when the target is the current content end.
	DisableFollow *bool `json:"disableFollow,omitzero"`
}

// ScrollView scrolls exactly one child vertically.
// It implements Component, LayoutComponent, and ScrollLayoutState.
// mu guards layout state because the auto-scrollbar hide timer fires off the caller goroutine.
// NewScrollView must install the style funcs, overscroll chain, scrollbar hidden, and hide delay 1000
// when the corresponding options are nil. Those zero values are not the TypeScript defaults.
// Default track style wraps text in ESC [90m / ESC [39m. Default thumb style uses ESC [37m / ESC [39m.
type ScrollView struct {
	mu sync.Mutex
	Container

	child                     Component
	followEnd                 bool
	primary                   bool
	overscroll                ScrollLayoutOverscroll
	ScrollbarTrackStyle       func(text string) string
	ScrollbarThumbStyle       func(text string) string
	currentScrollbar          ScrollViewScrollbar
	scrollbarHideDelayMs      int64
	currentScrollTop          int
	contentHeight             int
	currentViewportHeight     int
	followingEnd              bool
	followSuppressedAtEnd     bool
	requestRenderCallback     func()
	transientScrollbarVisible bool
	scrollbarActive           bool
	scrollbarHideTimer        *time.Timer
}

// NewScrollView returns a scroll view around component.
// Nil options means the defaults described on ScrollViewOptions.
// An axis other than nil or vertical returns an error whose message is
// "Unsupported ScrollView axis: " plus the axis text.
func NewScrollView(component Component, options *ScrollViewOptions) (*ScrollView, error) {
	panic("unported: NewScrollView")
}

// FollowEnd reports whether this view was constructed to follow the content end.
func (s *ScrollView) FollowEnd() bool { return s.followEnd }

// Primary reports whether this view is the primary scroll view.
func (s *ScrollView) Primary() bool { return s.primary }

// Overscroll reports what happens to leftover wheel deltas.
func (s *ScrollView) Overscroll() ScrollLayoutOverscroll { return s.overscroll }

// ScrollTop returns the current content row at the top of the viewport.
func (s *ScrollView) ScrollTop() int { return s.currentScrollTop }

// IsFollowingEnd reports whether the view is currently stuck to the content end.
func (s *ScrollView) IsFollowingEnd() bool { return s.followingEnd }

// ViewportHeight returns the last viewport height passed to UpdateLayout.
func (s *ScrollView) ViewportHeight() int { return s.currentViewportHeight }

// Scrollbar returns the current scrollbar mode.
func (s *ScrollView) Scrollbar() ScrollViewScrollbar { return s.currentScrollbar }

// IsScrollbarVisible reports whether the bar should be painted.
// "always" is visible when the viewport height is positive.
// "auto" is visible only while the transient show is active and the content overflows.
func (s *ScrollView) IsScrollbarVisible() bool {
	panic("unported: ScrollView.IsScrollbarVisible")
}

// IsScrollbarActive reports whether the pointer is over the scrollbar.
func (s *ScrollView) IsScrollbarActive() bool { return s.scrollbarActive }

// SetScrollbar changes the scrollbar mode and requests a render when it changes.
func (s *ScrollView) SetScrollbar(scrollbar ScrollViewScrollbar) {
	panic("unported: ScrollView.SetScrollbar")
}

// GetContentWidth returns the width offered to the child.
// "always" reserves one column when width is greater than 1.
func (s *ScrollView) GetContentWidth(width int) int {
	panic("unported: ScrollView.GetContentWidth")
}

func (s *ScrollView) markScrollbarActivity() {
	panic("unported: ScrollView.markScrollbarActivity")
}

func (s *ScrollView) hideTransientScrollbar() {
	panic("unported: ScrollView.hideTransientScrollbar")
}

// SetScrollbarActive sets whether the pointer is over the scrollbar.
func (s *ScrollView) SetScrollbarActive(active bool) {
	panic("unported: ScrollView.SetScrollbarActive")
}

// ScrollTo moves to scrollTop. Nil options means follow-end is updated from the position.
func (s *ScrollView) ScrollTo(scrollTop int, options *ScrollViewScrollToOptions) {
	panic("unported: ScrollView.ScrollTo")
}

// ScrollBy moves by lines. It returns the portion that was not applied
// (TypeScript returns requested minus moved, the leftover overscroll).
func (s *ScrollView) ScrollBy(lines int) int {
	panic("unported: ScrollView.ScrollBy")
}

// ScrollToStart scrolls to row 0.
func (s *ScrollView) ScrollToStart() {
	panic("unported: ScrollView.ScrollToStart")
}

// ScrollToEnd scrolls to the last viewport of content and resumes follow-end when configured.
func (s *ScrollView) ScrollToEnd() {
	panic("unported: ScrollView.ScrollToEnd")
}

// UpdateLayout records the measured content and viewport and clamps the scroll position.
func (s *ScrollView) UpdateLayout(contentHeight int, viewportHeight int, requestRender func()) {
	panic("unported: ScrollView.UpdateLayout")
}

// AddChild always fails. ScrollView has exactly one child.
func (s *ScrollView) AddChild(component Component) {
	panic("unported: ScrollView.AddChild")
}

// RemoveChild always fails. The scroll view child cannot be removed.
func (s *ScrollView) RemoveChild(component Component) {
	panic("unported: ScrollView.RemoveChild")
}

// Clear always fails. The scroll view child cannot be cleared.
func (s *ScrollView) Clear() {
	panic("unported: ScrollView.Clear")
}

// Render draws the child at the content width, padding a column when the scrollbar reserves one.
func (s *ScrollView) Render(width int) []string {
	panic("unported: ScrollView.Render")
}

// LayoutNode returns the scroll layout description. State is this scroll view.
func (s *ScrollView) LayoutNode() LayoutNode {
	panic("unported: ScrollView.LayoutNode")
}

var (
	_ Component         = (*ScrollView)(nil)
	_ LayoutComponent   = (*ScrollView)(nil)
	_ ScrollLayoutState = (*ScrollView)(nil)
)
