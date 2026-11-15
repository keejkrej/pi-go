// Ported from packages/tui/src/components/scroll-view.ts (pi v1.0.0).

package tui

import (
	"fmt"
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
	if options != nil && options.Axis != nil && *options.Axis != ScrollViewAxisVertical {
		return nil, fmt.Errorf("Unsupported ScrollView axis: %s", *options.Axis)
	}

	followEnd := options != nil && options.Follow != nil && *options.Follow == ScrollViewFollowEnd
	primary := options != nil && options.Primary != nil && *options.Primary
	overscroll := ScrollLayoutOverscrollChain
	if options != nil && options.Overscroll != nil {
		overscroll = *options.Overscroll
	}
	scrollbar := ScrollViewScrollbarHidden
	if options != nil && options.Scrollbar != nil {
		scrollbar = *options.Scrollbar
	}
	trackStyle := func(text string) string { return "\x1b[90m" + text + "\x1b[39m" }
	if options != nil && options.ScrollbarTrackStyle != nil {
		trackStyle = options.ScrollbarTrackStyle
	}
	thumbStyle := func(text string) string { return "\x1b[37m" + text + "\x1b[39m" }
	if options != nil && options.ScrollbarThumbStyle != nil {
		thumbStyle = options.ScrollbarThumbStyle
	}
	hideDelayMs := int64(1000)
	if options != nil && options.ScrollbarHideDelayMs != nil {
		hideDelayMs = max(int64(0), *options.ScrollbarHideDelayMs)
	}

	sv := &ScrollView{
		child:                component,
		followEnd:            followEnd,
		followingEnd:         followEnd,
		primary:              primary,
		overscroll:           overscroll,
		ScrollbarTrackStyle:  trackStyle,
		ScrollbarThumbStyle:  thumbStyle,
		currentScrollbar:     scrollbar,
		scrollbarHideDelayMs: hideDelayMs,
	}
	sv.Container.SetChildren([]Component{component})
	return sv, nil
}

// FollowEnd reports whether this view was constructed to follow the content end.
func (s *ScrollView) FollowEnd() bool { return s.followEnd }

// Primary reports whether this view is the primary scroll view.
func (s *ScrollView) Primary() bool { return s.primary }

// Overscroll reports what happens to leftover wheel deltas.
func (s *ScrollView) Overscroll() ScrollLayoutOverscroll { return s.overscroll }

// ScrollTop returns the current content row at the top of the viewport.
func (s *ScrollView) ScrollTop() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollTop
}

// IsFollowingEnd reports whether the view is currently stuck to the content end.
func (s *ScrollView) IsFollowingEnd() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.followingEnd
}

// ViewportHeight returns the last viewport height passed to UpdateLayout.
func (s *ScrollView) ViewportHeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentViewportHeight
}

// Scrollbar returns the current scrollbar mode.
func (s *ScrollView) Scrollbar() ScrollViewScrollbar {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollbar
}

// IsScrollbarVisible reports whether the bar should be painted.
// "always" is visible when the viewport height is positive.
// "auto" is visible only while the transient show is active and the content overflows.
func (s *ScrollView) IsScrollbarVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isScrollbarVisibleLocked()
}

func (s *ScrollView) isScrollbarVisibleLocked() bool {
	if s.currentScrollbar == ScrollViewScrollbarAlways {
		return s.currentViewportHeight > 0
	}
	return s.currentScrollbar == ScrollViewScrollbarAuto &&
		s.contentHeight > s.currentViewportHeight &&
		s.transientScrollbarVisible
}

// IsScrollbarActive reports whether the pointer is over the scrollbar.
func (s *ScrollView) IsScrollbarActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrollbarActive
}

// SetScrollbar changes the scrollbar mode and requests a render when it changes.
func (s *ScrollView) SetScrollbar(scrollbar ScrollViewScrollbar) {
	s.mu.Lock()
	if scrollbar == s.currentScrollbar {
		s.mu.Unlock()
		return
	}
	s.currentScrollbar = scrollbar
	if scrollbar != ScrollViewScrollbarAuto {
		s.hideTransientScrollbarLocked()
	} else if s.scrollbarActive {
		s.markScrollbarActivityLocked()
	}
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// GetContentWidth returns the width offered to the child.
// "always" reserves one column when width is greater than 1.
func (s *ScrollView) GetContentWidth(width int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getContentWidthLocked(width)
}

func (s *ScrollView) getContentWidthLocked(width int) int {
	if s.currentScrollbar == ScrollViewScrollbarAlways && width > 1 {
		return width - 1
	}
	return width
}

func (s *ScrollView) markScrollbarActivityLocked() {
	if s.currentScrollbar != ScrollViewScrollbarAuto || s.contentHeight <= s.currentViewportHeight {
		return
	}
	s.transientScrollbarVisible = true
	if s.scrollbarHideTimer != nil {
		s.scrollbarHideTimer.Stop()
		s.scrollbarHideTimer = nil
	}
	if s.scrollbarActive {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Duration(s.scrollbarHideDelayMs)*time.Millisecond, func() {
		s.mu.Lock()
		if s.scrollbarHideTimer != timer {
			s.mu.Unlock()
			return
		}
		s.scrollbarHideTimer = nil
		s.transientScrollbarVisible = false
		cb := s.requestRenderCallback
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
	})
	s.scrollbarHideTimer = timer
}

func (s *ScrollView) hideTransientScrollbarLocked() {
	s.transientScrollbarVisible = false
	if s.scrollbarHideTimer == nil {
		return
	}
	s.scrollbarHideTimer.Stop()
	s.scrollbarHideTimer = nil
}

// SetScrollbarActive sets whether the pointer is over the scrollbar.
func (s *ScrollView) SetScrollbarActive(active bool) {
	s.mu.Lock()
	if active == s.scrollbarActive {
		s.mu.Unlock()
		return
	}
	s.scrollbarActive = active
	s.markScrollbarActivityLocked()
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// ScrollTo moves to scrollTop. Nil options means follow-end is updated from the position.
func (s *ScrollView) ScrollTo(scrollTop int, options *ScrollViewScrollToOptions) {
	s.mu.Lock()
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	next := max(0, min(maxScrollTop, scrollTop))
	nextFollowSuppressedAtEnd := options != nil && options.DisableFollow != nil && *options.DisableFollow && next == maxScrollTop
	nextFollowingEnd := !nextFollowSuppressedAtEnd && s.followEnd && next == maxScrollTop
	if next == s.currentScrollTop && nextFollowingEnd == s.followingEnd && nextFollowSuppressedAtEnd == s.followSuppressedAtEnd {
		s.mu.Unlock()
		return
	}
	moved := next != s.currentScrollTop
	s.currentScrollTop = next
	s.followingEnd = nextFollowingEnd
	s.followSuppressedAtEnd = nextFollowSuppressedAtEnd
	if moved {
		s.markScrollbarActivityLocked()
	}
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// ScrollBy moves by lines. It returns the portion that was not applied
// (TypeScript returns requested minus moved, the leftover overscroll).
func (s *ScrollView) ScrollBy(lines int) int {
	if lines == 0 {
		return 0
	}
	s.mu.Lock()
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	start := s.currentScrollTop
	if s.followingEnd {
		start = maxScrollTop
	}
	next := max(0, min(maxScrollTop, start+lines))
	moved := next - start
	wasFollowingEnd := s.followingEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd && next == maxScrollTop
	s.followSuppressedAtEnd = false
	if moved != 0 {
		s.markScrollbarActivityLocked()
	}
	changed := moved != 0 || s.followingEnd != wasFollowingEnd
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if changed && cb != nil {
		cb()
	}
	return lines - moved
}

// ScrollToStart scrolls to row 0.
func (s *ScrollView) ScrollToStart() {
	s.mu.Lock()
	nextFollowing := s.followEnd && s.contentHeight <= s.currentViewportHeight
	changed := s.currentScrollTop != 0 || s.followingEnd != nextFollowing
	s.currentScrollTop = 0
	s.followingEnd = nextFollowing
	s.followSuppressedAtEnd = false
	var cb func()
	if changed {
		s.markScrollbarActivityLocked()
		cb = s.requestRenderCallback
	}
	s.mu.Unlock()
	if changed && cb != nil {
		cb()
	}
}

// ScrollToEnd scrolls to the last viewport of content and resumes follow-end when configured.
func (s *ScrollView) ScrollToEnd() {
	s.mu.Lock()
	next := max(0, s.contentHeight-s.currentViewportHeight)
	changed := s.currentScrollTop != next || s.followingEnd != s.followEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd
	s.followSuppressedAtEnd = false
	var cb func()
	if changed {
		s.markScrollbarActivityLocked()
		cb = s.requestRenderCallback
	}
	s.mu.Unlock()
	if changed && cb != nil {
		cb()
	}
}

// UpdateLayout records the measured content and viewport and clamps the scroll position.
func (s *ScrollView) UpdateLayout(contentHeight int, viewportHeight int, requestRender func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentHeight = max(0, contentHeight)
	s.currentViewportHeight = max(0, viewportHeight)
	s.requestRenderCallback = requestRender
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	if s.followingEnd {
		s.currentScrollTop = maxScrollTop
	} else {
		s.currentScrollTop = max(0, min(s.currentScrollTop, maxScrollTop))
	}
	if s.currentScrollTop < maxScrollTop {
		s.followSuppressedAtEnd = false
	}
	if s.followEnd && s.currentScrollTop == maxScrollTop && !s.followSuppressedAtEnd {
		s.followingEnd = true
	}
	if s.contentHeight <= s.currentViewportHeight {
		s.hideTransientScrollbarLocked()
	}
}

// AddChild always fails. ScrollView has exactly one child.
func (s *ScrollView) AddChild(component Component) {
	panic("ScrollView has exactly one child")
}

// RemoveChild always fails. The scroll view child cannot be removed.
func (s *ScrollView) RemoveChild(component Component) {
	panic("ScrollView child cannot be removed")
}

// Clear always fails. The scroll view child cannot be cleared.
func (s *ScrollView) Clear() {
	panic("ScrollView child cannot be cleared")
}

// Render draws the child at the content width, padding a column when the scrollbar reserves one.
func (s *ScrollView) Render(width int) []string {
	contentWidth := s.GetContentWidth(width)
	lines := s.child.Render(contentWidth)
	if contentWidth == width {
		return lines
	}
	padded := make([]string, len(lines))
	for i, line := range lines {
		padded[i] = line + " "
	}
	return padded
}

// LayoutNode returns the scroll layout description. State is this scroll view.
func (s *ScrollView) LayoutNode() LayoutNode {
	return &ScrollLayoutNode{
		Type:      LayoutNodeTypeScroll,
		Component: s.child,
		State:     s,
	}
}

var (
	_ Component         = (*ScrollView)(nil)
	_ LayoutComponent   = (*ScrollView)(nil)
	_ ScrollLayoutState = (*ScrollView)(nil)
)
