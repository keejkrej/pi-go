package gui

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// ScrollView wraps MyGo's virtualized ui.List with auto-following at the end,
// smooth animated scrolling on new items, scroll-up lock detection, and a
// floating "Jump to bottom" pill button when the user scrolls away from the end.
type ScrollView struct {
	mu              sync.Mutex
	list            ui.ListState
	followEnd       bool
	smoothFollow    bool
	showJumpButton  bool
	jumpButtonLabel string
	manualLocked    bool
	laidOut         bool
	lastCount       int
	theme           *GuiTheme
	keyFn           func(index int) any
	emptyView       func(c *ui.Context)
	onScrollChange  func(atEnd bool)
	wasAtEnd        bool
}

// NewScrollView constructs a ScrollView with FollowEnd, smooth scroll, and the
// floating scroll-to-bottom button enabled by default.
func NewScrollView() *ScrollView {
	sv := &ScrollView{
		followEnd:       true,
		smoothFollow:    true,
		showJumpButton:  true,
		jumpButtonLabel: "Scroll to bottom",
		wasAtEnd:        true,
	}
	sv.list.FollowEnd = true
	sv.list.FollowAnimation = &ui.Smooth
	return sv
}

// ListState returns the underlying ui.ListState.
func (s *ScrollView) ListState() *ui.ListState {
	return &s.list
}

// SetKeyFunc sets the row identity function passed to ui.ListState.Key so
// focus, selections, and scroll position stay anchored across prepend/updates.
func (s *ScrollView) SetKeyFunc(fn func(index int) any) {
	s.mu.Lock()
	s.keyFn = fn
	s.list.Key = fn
	s.mu.Unlock()
}

// SetEmptyView sets an optional placeholder builder rendered when item count is 0.
func (s *ScrollView) SetEmptyView(fn func(c *ui.Context)) {
	s.mu.Lock()
	s.emptyView = fn
	s.mu.Unlock()
}

// SetFollowEnd controls whether the list starts at and auto-follows its end.
func (s *ScrollView) SetFollowEnd(follow bool) {
	s.mu.Lock()
	s.followEnd = follow
	s.manualLocked = !follow
	s.list.FollowEnd = follow && !s.manualLocked
	s.mu.Unlock()
}

// SetSmoothFollow enables or disables ui.Smooth animated following.
func (s *ScrollView) SetSmoothFollow(smooth bool) {
	s.mu.Lock()
	s.smoothFollow = smooth
	if smooth {
		s.list.FollowAnimation = &ui.Smooth
	} else {
		s.list.FollowAnimation = nil
	}
	s.mu.Unlock()
}

// SetShowJumpButton toggles the floating "Jump to bottom" overlay pill.
func (s *ScrollView) SetShowJumpButton(show bool) {
	s.mu.Lock()
	s.showJumpButton = show
	s.mu.Unlock()
}

// SetJumpButtonLabel customizes the floating button's accessible label.
func (s *ScrollView) SetJumpButtonLabel(label string) {
	s.mu.Lock()
	if label == "" {
		label = "Scroll to bottom"
	}
	s.jumpButtonLabel = label
	s.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (s *ScrollView) SetTheme(theme *GuiTheme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

// SetOnScrollChange sets a listener notified when AtEnd transitions between true/false.
func (s *ScrollView) SetOnScrollChange(fn func(atEnd bool)) {
	s.mu.Lock()
	s.onScrollChange = fn
	s.mu.Unlock()
}

// JumpToBottom unlocks auto-follow (if followEnd was enabled) and scrolls to the last row.
func (s *ScrollView) JumpToBottom() {
	s.mu.Lock()
	s.manualLocked = false
	s.list.FollowEnd = s.followEnd
	s.list.ScrollToEnd()
	s.mu.Unlock()
}

// SetLocked explicitly pins or unpins the scroll position away from auto-following.
func (s *ScrollView) SetLocked(locked bool) {
	s.mu.Lock()
	s.manualLocked = locked
	s.list.FollowEnd = s.followEnd && !locked
	s.mu.Unlock()
}

// Locked reports whether auto-following is paused because the user scrolled up
// or SetLocked(true) was called.
func (s *ScrollView) Locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manualLocked {
		return true
	}
	return s.laidOut && s.lastCount > 0 && !s.list.AtEnd()
}

// AtEnd reports whether the virtual list is currently scrolled to its end.
func (s *ScrollView) AtEnd() bool {
	s.mu.Lock()
	c := s.lastCount
	s.mu.Unlock()
	if c == 0 {
		return true
	}
	return s.list.AtEnd()
}

// ScrollTo scrolls row into position aligned per align.
func (s *ScrollView) ScrollTo(row int, align ui.Align) {
	s.list.ScrollTo(row, align)
}

// ScrollIntoView scrolls just enough for row to become visible.
func (s *ScrollView) ScrollIntoView(row int) {
	s.list.ScrollIntoView(row)
}

// HoldPlace preserves the visible viewport anchor across one layout change.
func (s *ScrollView) HoldPlace() {
	s.list.HoldPlace()
}

// VisibleRange returns the first and last row indexes currently in view.
func (s *ScrollView) VisibleRange() (first, last int) {
	return s.list.Visible()
}

// View builds the virtualized ScrollView and floating "Jump to bottom" pill inside c.
func (s *ScrollView) View(c *ui.Context, count int, renderRow func(c *ui.Context, index int)) ui.Element {
	s.mu.Lock()
	theme := ResolveGuiTheme(c, s.theme)
	follow := s.followEnd
	smooth := s.smoothFollow
	showJump := s.showJumpButton
	jumpLabel := s.jumpButtonLabel
	manualLocked := s.manualLocked
	laidOut := s.laidOut
	keyFn := s.keyFn
	emptyFn := s.emptyView
	onScrollChange := s.onScrollChange

	s.list.Key = keyFn
	s.list.FollowEnd = follow && !manualLocked
	if smooth {
		s.list.FollowAnimation = &ui.Smooth
	} else {
		s.list.FollowAnimation = nil
	}
	prevAtEnd := s.wasAtEnd
	s.lastCount = count
	if count == 0 {
		s.laidOut = false
		s.wasAtEnd = true
	}
	s.mu.Unlock()

	if count == 0 {
		if laidOut && !prevAtEnd && onScrollChange != nil {
			onScrollChange(true)
		}
		container := ui.Box(c).Fill()
		container.Children(func() {
			if emptyFn != nil {
				emptyFn(c)
			}
		})
		return container
	}

	atEnd := true
	if laidOut {
		atEnd = s.list.AtEnd()
		s.mu.Lock()
		s.wasAtEnd = atEnd
		s.mu.Unlock()
		if atEnd != prevAtEnd && onScrollChange != nil {
			onScrollChange(atEnd)
		}
	}

	container := ui.Box(c).Fill()
	container.Children(func() {
		ui.List(c, &s.list, count, func(i int) {
			if renderRow != nil {
				renderRow(c, i)
			}
		}).Fill()

		if showJump && ((laidOut && !s.list.AtEnd()) || (manualLocked && !follow)) {
			bg := theme.ComposerBg
			if bg.A == 0 {
				bg = theme.Raised
			}
			if bg.A == 0 {
				bg = theme.SurfaceElevated
			}
			bdr := theme.BorderStrong
			if bdr.A == 0 {
				bdr = theme.Border
			}
			hoverBg := theme.Raised
			if hoverBg.A == 0 {
				hoverBg = theme.OverlayStrong
			}
			pill := ui.ButtonBase(c).
				Label(jumpLabel).
				Absolute().
				LeftPercent(50).
				Bottom(8).
				Margin(0, 0, 0, -16).
				Size(32, 32).
				AlignItems(ui.Center).
				Justify(ui.Center).
				Background(bg).
				Border(1, bdr).
				Radius(999).
				Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.18)).
				Cursor(ui.CursorPointer)
			if pill.Hovered() {
				pill.Background(hoverBg)
			}
			pill.Children(func() {
				IconView(c, IconArrowDown, 16, theme.Text)
			})
			if pill.Clicked() {
				s.JumpToBottom()
				c.Invalidate()
			}
		}
	})

	s.mu.Lock()
	s.laidOut = true
	s.mu.Unlock()
	return container
}
