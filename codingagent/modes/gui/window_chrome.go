package gui

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/gui"
)

type wakuToastKind string

const (
	wakuToastInfo    wakuToastKind = "info"
	wakuToastWarning wakuToastKind = "warning"
	wakuToastError   wakuToastKind = "error"
)

type wakuToastState struct {
	mu      sync.Mutex
	kind    wakuToastKind
	title   string
	message string
	seq     uint64
	visible bool
}

func newWakuToastState() *wakuToastState {
	return &wakuToastState{}
}

func (t *wakuToastState) show(kind wakuToastKind, title, message string, onExpire func()) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.seq++
	seq := t.seq
	t.kind = kind
	t.title = strings.TrimSpace(title)
	t.message = strings.TrimSpace(message)
	t.visible = t.title != "" || t.message != ""
	t.mu.Unlock()

	if t.visible && kind != wakuToastError {
		dur := 4500 * time.Millisecond
		time.AfterFunc(dur, func() {
			t.mu.Lock()
			changed := t.visible && t.seq == seq
			if changed {
				t.visible = false
			}
			t.mu.Unlock()
			if changed && onExpire != nil {
				onExpire()
			}
		})
	}
}

func (t *wakuToastState) dismiss() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.visible = false
	t.mu.Unlock()
}

func (t *wakuToastState) snapshot() (visible bool, kind wakuToastKind, title, message string) {
	if t == nil {
		return false, "", "", ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visible, t.kind, t.title, t.message
}

// renderWakuFloatingToast renders a Waku-style top-center floating pill toast inside the
// center column when a non-empty notification is active.
func renderWakuFloatingToast(c *ui.Context, m *GuiMode, th *gui.GuiTheme) {
	if m == nil || m.toast == nil {
		return
	}
	visible, kind, title, message := m.toast.snapshot()
	if !visible {
		return
	}

	accent := th.Accent
	svg := gui.IconInfo
	if kind == wakuToastWarning {
		accent = th.Warning
		svg = gui.IconAlert
	} else if kind == wakuToastError {
		accent = th.Error
		svg = gui.IconAlert
	}

	var dismissClicked bool
	wrap := ui.Row(c).
		Key("waku-toast").
		Absolute().
		Top(54).
		Left(16).
		Right(16).
		Justify(ui.Center)

	wrap.Children(func() {
		pill := ui.Row(c).
			MaxWidth(560).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			Radius(10).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28)).
			Padding(7, 10, 7, 12).
			AlignItems(ui.Center).
			Gap(8)
		if kind == wakuToastError {
			pill.Border(1, th.Error.Alpha(0.65))
		}
		pill.Children(func() {
			gui.IconView(c, svg, 14, accent)
			if title != "" {
				ui.Text(c, title).TextColor(th.Text).FontSize(12).FontWeight(600).SingleLine()
			}
			if message != "" {
				ui.Text(c, message).TextColor(th.TextSecondary).FontSize(12).Wrap()
			}
			closeBtn := ui.ButtonBase(c).
				Size(20, 20).
				Radius(5).
				AlignItems(ui.Center).
				Justify(ui.Center).
				Cursor(ui.CursorPointer).
				Label("Dismiss notification")
			if closeBtn.Hovered() {
				closeBtn.Background(th.Overlay)
			}
			closeBtn.Children(func() {
				gui.IconView(c, gui.IconClose, 12, th.TextTertiary)
			})
			if closeBtn.Clicked() {
				dismissClicked = true
			}
		})
	})

	if dismissClicked {
		m.toast.dismiss()
	}
}

const (
	wakuTrafficLightClearance float32 = 86
	wakuHeaderHeightPx        float32 = 48
	wakuContentMaxWidth       float32 = 720
	wakuWorkspaceFooterHeight float32 = 28
	wakuComposerRadius        float32 = 13
	wakuUserBubbleMaxWidth    float32 = 540
	wakuUserBubbleRadius      float32 = 12
)

func wakuHeaderHeight(c *ui.Context) float32 {
	if c != nil {
		if h := c.TitleBar().Height; h > 0 {
			return max(h, wakuHeaderHeightPx)
		}
	}
	return wakuHeaderHeightPx
}

func wakuTrafficLightInset(c *ui.Context) float32 {
	if c != nil {
		tb := c.TitleBar()
		if tb.Left > 0 {
			return max(tb.Left, wakuTrafficLightClearance)
		}
		if tb.Height > 0 {
			return wakuTrafficLightClearance
		}
	}
	if runtime.GOOS == "darwin" {
		return wakuTrafficLightClearance
	}
	return 0
}

func wakuIconButton(
	c *ui.Context,
	th *gui.GuiTheme,
	svg *ui.SVG,
	label string,
	active bool,
	iconColor ui.Color,
) bool {
	btn := ui.ButtonBase(c).
		Size(26, 26).
		Radius(6).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Cursor(ui.CursorPointer).
		Label(label)
	if active || btn.Hovered() {
		btn.Background(th.Overlay)
	}
	btn.Children(func() {
		gui.IconView(c, svg, 14, iconColor)
	})
	return btn.Clicked()
}

func renderWakuHistoryNavButtons(c *ui.Context, th *gui.GuiTheme, canBack, canForward bool) (goBack, goForward bool) {
	backCol := th.TextSecondary
	if !canBack {
		backCol = th.TextGhost
	}
	if wakuIconButton(c, th, gui.IconArrowLeft, "Previous Session", false, backCol) && canBack {
		goBack = true
	}
	fwdCol := th.TextSecondary
	if !canForward {
		fwdCol = th.TextGhost
	}
	if wakuIconButton(c, th, gui.IconArrowRight, "Next Session", false, fwdCol) && canForward {
		goForward = true
	}
	return goBack, goForward
}

// renderWakuCenterHeader renders the 48px Waku top header bar above the center conversation pane.
func renderWakuCenterHeader(
	c *ui.Context,
	m *GuiMode,
	th *gui.GuiTheme,
	sidebarOpen bool,
	sidebarWidth float32,
	rightOpen bool,
	canBack bool,
	canForward bool,
	sessionTitle string,
) {
	h := wakuHeaderHeight(c)
	inset := wakuTrafficLightInset(c)
	leftPad := float32(14)
	if sidebarOpen {
		if inset > sidebarWidth {
			leftPad += inset - sidebarWidth
		}
	} else if inset > 8 {
		leftPad = 8
	}

	bar := ui.Row(c).
		Key("waku-center-header").
		FillWidth().
		Height(h).
		AlignItems(ui.Center).
		Padding(0, 12, 0, leftPad).
		Gap(8)

	var toggleSidebar, goBack, goForward, toggleRight bool

	bar.Children(func() {
		// When the left sidebar is closed, window chrome controls (macOS traffic-light clearance,
		// sidebar toggle, and session history navigation) live in the center header.
		if !sidebarOpen {
			if inset > 8 {
				ui.Box(c).Width(inset - 8).FillHeight().DragWindow()
			}
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(2).Children(func() {
				if wakuIconButton(c, th, gui.IconPanelLeft, "Toggle Sidebar", false, th.TextSecondary) {
					toggleSidebar = true
				}
				goBack, goForward = renderWakuHistoryNavButtons(c, th, canBack, canForward)
			})
		}

		title := strings.TrimSpace(sessionTitle)
		if title == "" {
			title = "New task"
		}
		ui.Text(c, title).TextColor(th.Text).FontSize(13).FontWeight(500).SingleLine().Shrink(1)

		// Non-interactive window drag spacer
		ui.Box(c).Grow(1).FillHeight().DragWindow()

		if !rightOpen {
			if wakuIconButton(c, th, gui.IconPanelRight, "Toggle Right Panel", false, th.TextSecondary) {
				toggleRight = true
			}
		}
	})

	if toggleSidebar {
		m.toggleLeftSidebar()
	}
	if goBack {
		m.navigateSessionHistory(-1)
	}
	if goForward {
		m.navigateSessionHistory(1)
	}
	if toggleRight {
		m.toggleRightPanel()
	}
}

type wakuSplitDragTracker struct {
	mu      sync.Mutex
	wasHeld bool
}

func (s *wakuSplitDragTracker) view(
	c *ui.Context,
	key string,
	th *gui.GuiTheme,
	lineColor ui.Color,
	curWidth float32,
	minW float32,
	maxW float32,
	invert bool,
	onResize func(float32),
	onRelease func(),
) {
	strip := ui.Row(c).
		Key(key).
		Width(5).
		FillHeight().
		AlignItems(ui.Center).
		Justify(ui.Center).
		Cursor(ui.CursorResizeEW)

	dx, _, held := strip.Dragged()
	col := lineColor
	if held || strip.Hovered() {
		col = th.ResizeHandle
	}
	strip.Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(col)
	})

	if held && dx != 0 && onResize != nil {
		delta := dx
		if invert {
			delta = -dx
		}
		next := curWidth + delta
		if next < minW {
			next = minW
		}
		if next > maxW {
			next = maxW
		}
		onResize(next)
	}

	s.mu.Lock()
	released := s.wasHeld && !held
	s.wasHeld = held
	s.mu.Unlock()
	if released && onRelease != nil {
		onRelease()
	}
}

func wakuFormatShortAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
