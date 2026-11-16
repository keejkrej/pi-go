package gui

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// ErrDesktopUnavailable is returned when a native desktop API (dialogs,
// system notifications, or OS clipboard) is invoked outside a running
// mygo.Application without a UI context/test hook.
var ErrDesktopUnavailable = errors.New("gui: desktop service unavailable outside running application")

// ProgramStatusState represents the terminal/window activity state mirroring
// pi's program-status protocol.
type ProgramStatusState string

const (
	StatusIdle    ProgramStatusState = "idle"
	StatusWorking ProgramStatusState = "working"
	StatusBlocked ProgramStatusState = "blocked"
	StatusDone    ProgramStatusState = "done"
	StatusError   ProgramStatusState = "error"
	StatusClear   ProgramStatusState = "clear"
)

// ProgramAttentionKind classifies blocked states requiring user interaction.
type ProgramAttentionKind string

const (
	AttentionPermission ProgramAttentionKind = "permission"
	AttentionQuestion   ProgramAttentionKind = "question"
	AttentionAuth       ProgramAttentionKind = "auth"
)

// ProgramStatus mirrors packages/tui/src/program-status.ts and drives native
// window title badges and taskbar/dock progress indicators.
type ProgramStatus struct {
	State    ProgramStatusState
	App      string
	Kind     ProgramAttentionKind
	Message  string
	Progress float64 // Optional [0, 1] fraction when State == StatusWorking.
}

// DesktopHooks allows tests or embedders to intercept OS desktop helpers
// (clipboard, native file/message dialogs, notifications) without a live OS loop.
type DesktopHooks struct {
	ReadClipboardText  func() string
	WriteClipboardText func(text string) error
	ReadClipboardPNG   func() ([]byte, error)
	WriteClipboardPNG  func(png []byte) error
	OpenFileDialog     func(opts mygo.OpenDialogOptions) ([]string, error)
	SaveFileDialog     func(opts mygo.SaveDialogOptions) (string, error)
	MessageDialog      func(opts mygo.MessageOptions) (mygo.MessageResult, error)
	ErrorDialog        func(title, message string)
	Notify             func(opts mygo.NotificationOptions) error
}

// Options configures a GUI controller instance.
type Options struct {
	Title                string
	Theme                *GuiTheme
	RootView             func(c *ui.Context)
	Window               mygo.WindowOptions
	IncludeStatusInTitle bool
	Desktop              *DesktopHooks
}

// OverlayOptions configures a modal overlay pushed onto GUI's overlay stack.
type OverlayOptions struct {
	Width                float32
	Height               float32
	MaxWidth             float32
	MaxHeight            float32
	MaxWidthPercent      float32
	MaxHeightPercent     float32
	BackdropColor        ui.Color
	PanelBackground      ui.Color
	NoBackdrop           bool
	DisableEscape        bool
	DisableBackdropClose bool
	OnDismiss            func()
}

// OverlayHandle references a single overlay on the GUI stack.
type OverlayHandle struct {
	gui *GUI
	id  uint64
}

type overlayEntry struct {
	id     uint64
	build  func(c *ui.Context)
	opts   OverlayOptions
	hidden bool
}

// GUI coordinates the root native view, thread-safe background updates (Post &
// RequestRender), modal overlay stack, window title / ProgramStatus, and
// desktop integration helpers.
type GUI struct {
	mu                   sync.Mutex
	win                  *mygo.Window
	winOpts              mygo.WindowOptions
	running              bool
	stopped              bool
	title                string
	includeStatusInTitle bool
	programStatus        ProgramStatus
	theme                *GuiTheme
	rootView             func(c *ui.Context)
	desktop              *DesktopHooks

	nextOverlayID uint64
	overlays      []*overlayEntry

	services    ui.Services
	hasServices bool

	pendingPosts []func()
	renderCount  atomic.Uint64
}

// New constructs a new GUI controller with the supplied options.
func New(opts ...Options) *GUI {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return &GUI{
		title:                o.Title,
		theme:                o.Theme,
		rootView:             o.RootView,
		winOpts:              o.Window,
		includeStatusInTitle: o.IncludeStatusInTitle,
		desktop:              o.Desktop,
	}
}

// Start creates and shows a native *mygo.Window backed by g.View if one is not
// already attached. Must be called after mygo.App is ready.
func (g *GUI) Start(opts ...mygo.WindowOptions) *mygo.Window {
	g.mu.Lock()
	if g.win != nil {
		w := g.win
		g.running = true
		g.stopped = false
		g.mu.Unlock()
		return w
	}
	wOpts := g.winOpts
	if len(opts) > 0 {
		wOpts = opts[0]
	}
	if wOpts.Title == "" {
		wOpts.Title = g.effectiveTitleLocked()
	}
	if wOpts.Width == 0 && wOpts.Height == 0 {
		wOpts.Width = 1240
		wOpts.Height = 820
	}
	if wOpts.MinWidth == 0 && wOpts.MinHeight == 0 {
		wOpts.MinWidth = 760
		wOpts.MinHeight = 520
	}
	if wOpts.TitleBarStyle == "" {
		wOpts.TitleBarStyle = mygo.TitleBarHidden
	}
	if wOpts.TrafficLightPosition == nil {
		wOpts.TrafficLightPosition = &mygo.Point{X: 16, Y: 16}
	}
	if wOpts.TitleBarHeight == 0 {
		wOpts.TitleBarHeight = 48
	}
	if wOpts.Vibrancy == "" {
		wOpts.Vibrancy = mygo.VibrancySidebar
	}
	if wOpts.BackgroundColor == "" {
		wOpts.BackgroundColor = "light-dark(#f6f5f6, #1a1a1a)"
	}
	if wOpts.StateKey == "" {
		wOpts.StateKey = "pi-gui-main"
	}
	wOpts.Content = ui.View(g.View)
	g.mu.Unlock()

	win := mygo.NewWindow(wOpts)

	g.mu.Lock()
	g.win = win
	g.running = true
	g.stopped = false
	pending := g.pendingPosts
	g.pendingPosts = nil
	st := g.programStatus
	effTitle := g.effectiveTitleLocked()
	g.mu.Unlock()

	if effTitle != "" {
		win.SetTitle(effTitle)
	}
	applyProgressToWindow(win, st)
	if len(pending) > 0 {
		win.Update(func() {
			for _, fn := range pending {
				if fn != nil {
					fn()
				}
			}
		})
	}
	return win
}

// AttachWindow binds an externally created *mygo.Window to g.
func (g *GUI) AttachWindow(win *mygo.Window) {
	g.mu.Lock()
	g.win = win
	g.running = win != nil
	g.stopped = false
	pending := g.pendingPosts
	g.pendingPosts = nil
	effTitle := g.effectiveTitleLocked()
	st := g.programStatus
	g.mu.Unlock()

	if win == nil {
		return
	}
	if effTitle != "" {
		win.SetTitle(effTitle)
	}
	applyProgressToWindow(win, st)
	if len(pending) > 0 {
		win.Update(func() {
			for _, fn := range pending {
				if fn != nil {
					fn()
				}
			}
		})
	}
}

// Window returns the attached *mygo.Window, or nil in headless test mode.
func (g *GUI) Window() *mygo.Window {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.win
}

// Running reports whether GUI has an active started session that has not stopped.
func (g *GUI) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running && !g.stopped
}

// Stop tears down the GUI controller and closes its attached window if open.
func (g *GUI) Stop() {
	g.mu.Lock()
	g.stopped = true
	g.running = false
	w := g.win
	g.win = nil
	g.overlays = nil
	g.pendingPosts = nil
	g.mu.Unlock()

	if w != nil && !w.IsDestroyed() {
		w.Close()
	}
}

// SetRootView sets the main content view builder function.
func (g *GUI) SetRootView(fn func(c *ui.Context)) {
	g.mu.Lock()
	g.rootView = fn
	g.mu.Unlock()
	g.RequestRender()
}

// SetTheme sets an explicit GuiTheme override. Pass nil to follow system/window
// dark or light appearance automatically.
func (g *GUI) SetTheme(theme *GuiTheme) {
	g.mu.Lock()
	g.theme = theme
	g.mu.Unlock()
	g.RequestRender()
}

// Theme returns the configured GuiTheme override (or nil when auto-following).
func (g *GUI) Theme() *GuiTheme {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.theme
}

// ResolvedTheme returns the active GuiTheme for the given UI context.
func (g *GUI) ResolvedTheme(c *ui.Context) *GuiTheme {
	g.mu.Lock()
	t := g.theme
	g.mu.Unlock()
	return ResolveGuiTheme(c, t)
}

// SetDesktopHooks installs optional desktop helper overrides (primarily for tests).
func (g *GUI) SetDesktopHooks(hooks *DesktopHooks) {
	g.mu.Lock()
	g.desktop = hooks
	g.mu.Unlock()
}

// Post schedules fn to execute on the UI thread and request a frame redraw.
// Never hold locks while calling Post; GUI never holds its mutex when calling fn.
func (g *GUI) Post(fn func()) {
	if fn == nil {
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	w := g.win
	if w != nil {
		g.mu.Unlock()
		w.Update(fn)
		return
	}
	g.pendingPosts = append(g.pendingPosts, fn)
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if hasSvc {
		svc.Invalidate()
	}
}

// RequestRender invalidates the view so it rebuilds on the next frame.
// Safe to call from any goroutine.
func (g *GUI) RequestRender() {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	w := g.win
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if w != nil {
		w.Invalidate()
		return
	}
	if hasSvc {
		svc.Invalidate()
	}
}

// FlushPending executes queued headless Post callbacks immediately on the caller
// goroutine. Useful in pure unit tests without a running ui.Tester frame loop.
func (g *GUI) FlushPending() int {
	g.mu.Lock()
	batch := g.pendingPosts
	g.pendingPosts = nil
	g.mu.Unlock()

	for _, fn := range batch {
		if fn != nil {
			fn()
		}
	}
	return len(batch)
}

// RenderCount returns how many times GUI.View has executed.
func (g *GUI) RenderCount() uint64 {
	return g.renderCount.Load()
}

// SetTitle updates the base window title.
func (g *GUI) SetTitle(title string) {
	g.mu.Lock()
	g.title = title
	w := g.win
	eff := g.effectiveTitleLocked()
	g.mu.Unlock()

	if w != nil {
		w.Update(func() {
			w.SetTitle(eff)
		})
	}
}

// Title returns the configured base window title.
func (g *GUI) Title() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.title
}

// EffectiveTitle returns the window title including any active ProgramStatus badge.
func (g *GUI) EffectiveTitle() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.effectiveTitleLocked()
}

func (g *GUI) effectiveTitleLocked() string {
	base := g.title
	if base == "" {
		base = g.programStatus.App
	}
	if !g.includeStatusInTitle {
		return base
	}
	st := g.programStatus
	if st.State == "" || st.State == StatusClear || st.State == StatusIdle {
		return base
	}
	var label string
	switch st.State {
	case StatusWorking:
		label = "Working"
	case StatusBlocked:
		label = "Needs attention"
	case StatusDone:
		label = "Done"
	case StatusError:
		label = "Error"
	default:
		label = string(st.State)
	}
	if st.Message != "" {
		label = st.Message
	}
	if base == "" {
		return label
	}
	return fmt.Sprintf("%s — %s", base, label)
}

// SetProgramStatus updates the desktop status, title, and window progress bar.
func (g *GUI) SetProgramStatus(status ProgramStatus) {
	g.mu.Lock()
	if status.State == StatusClear {
		g.programStatus = ProgramStatus{}
	} else {
		g.programStatus = status
	}
	w := g.win
	effTitle := g.effectiveTitleLocked()
	current := g.programStatus
	g.mu.Unlock()

	if w != nil {
		w.Update(func() {
			if effTitle != "" {
				w.SetTitle(effTitle)
			}
			applyProgressToWindow(w, current)
		})
		return
	}
	g.RequestRender()
}

// ProgramStatus returns a copy of the current ProgramStatus.
func (g *GUI) ProgramStatus() ProgramStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.programStatus
}

func applyProgressToWindow(w *mygo.Window, st ProgramStatus) {
	if w == nil {
		return
	}
	switch st.State {
	case "", StatusClear, StatusIdle, StatusDone:
		w.SetProgressBar(mygo.ProgressBar{})
	case StatusWorking:
		if st.Progress > 0 {
			val := st.Progress
			if val > 1 {
				val = 1
			}
			w.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressNormal, Value: val})
		} else {
			w.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressIndeterminate})
		}
	case StatusBlocked:
		w.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressPaused, Value: 1})
	case StatusError:
		w.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressError, Value: 1})
	}
}

// ShowOverlay pushes a modal/floating overlay onto the top of the overlay stack.
func (g *GUI) ShowOverlay(build func(c *ui.Context), opts ...OverlayOptions) *OverlayHandle {
	var o OverlayOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	g.mu.Lock()
	g.nextOverlayID++
	id := g.nextOverlayID
	g.overlays = append(g.overlays, &overlayEntry{
		id:    id,
		build: build,
		opts:  o,
	})
	g.mu.Unlock()

	g.RequestRender()
	return &OverlayHandle{gui: g, id: id}
}

// HideOverlay dismisses the top-most visible overlay on the stack (if any).
func (g *GUI) HideOverlay() bool {
	g.mu.Lock()
	var target *overlayEntry
	idx := -1
	for i := len(g.overlays) - 1; i >= 0; i-- {
		if !g.overlays[i].hidden {
			target = g.overlays[i]
			idx = i
			break
		}
	}
	if idx < 0 {
		g.mu.Unlock()
		return false
	}
	g.overlays = append(g.overlays[:idx], g.overlays[idx+1:]...)
	dismiss := target.opts.OnDismiss
	g.mu.Unlock()

	if dismiss != nil {
		dismiss()
	}
	g.RequestRender()
	return true
}

// HasOverlay reports whether at least one visible overlay is open.
func (g *GUI) HasOverlay() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, ov := range g.overlays {
		if !ov.hidden {
			return true
		}
	}
	return false
}

// OverlayCount returns the number of overlays currently on the stack (including hidden ones).
func (g *GUI) OverlayCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.overlays)
}

// ID returns the unique identifier of this overlay handle.
func (h *OverlayHandle) ID() uint64 {
	if h == nil {
		return 0
	}
	return h.id
}

// Hide removes this overlay from the stack and invokes its OnDismiss hook once.
func (h *OverlayHandle) Hide() {
	if h == nil || h.gui == nil {
		return
	}
	g := h.gui
	g.mu.Lock()
	var dismiss func()
	for i, ov := range g.overlays {
		if ov.id == h.id {
			dismiss = ov.opts.OnDismiss
			g.overlays = append(g.overlays[:i], g.overlays[i+1:]...)
			break
		}
	}
	g.mu.Unlock()

	if dismiss != nil {
		dismiss()
	}
	g.RequestRender()
}

// SetHidden hides or reveals this overlay without removing it from the stack.
func (h *OverlayHandle) SetHidden(hidden bool) {
	if h == nil || h.gui == nil {
		return
	}
	g := h.gui
	changed := false
	g.mu.Lock()
	for _, ov := range g.overlays {
		if ov.id == h.id {
			if ov.hidden != hidden {
				ov.hidden = hidden
				changed = true
			}
			break
		}
	}
	g.mu.Unlock()
	if changed {
		g.RequestRender()
	}
}

// IsHidden reports whether this overlay is currently hidden.
func (h *OverlayHandle) IsHidden() bool {
	if h == nil || h.gui == nil {
		return true
	}
	g := h.gui
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, ov := range g.overlays {
		if ov.id == h.id {
			return ov.hidden
		}
	}
	return true
}

// Visible reports whether the overlay exists on the stack and is not hidden.
func (h *OverlayHandle) Visible() bool {
	return !h.IsHidden()
}

// View is the top-level ui.View entry point passed to ui.View(g.View) or ui.NewTester(g.View, w, h).
func (g *GUI) View(c *ui.Context) {
	g.renderCount.Add(1)

	// Capture window services and drain pending Post callbacks outside locks.
	g.mu.Lock()
	g.services = c.Services()
	g.hasServices = true
	pending := g.pendingPosts
	g.pendingPosts = nil
	themeOverride := g.theme
	rootFn := g.rootView
	g.mu.Unlock()

	for _, fn := range pending {
		if fn != nil {
			fn()
		}
	}

	resolved := ResolveGuiTheme(c, themeOverride)
	c.SetTheme(resolved.UITheme())

	rootBg := resolved.Background
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
		rootBg = ui.Transparent
	}
	rootBox := ui.Box(c).Fill().Background(rootBg)
	rootBox.Children(func() {
		if rootFn != nil {
			rootFn(c)
		}
	})

	// Snapshot visible overlays without holding g.mu across builds or dismiss callbacks.
	g.mu.Lock()
	var visible []*overlayEntry
	for _, ov := range g.overlays {
		if !ov.hidden {
			visible = append(visible, ov)
		}
	}
	g.mu.Unlock()

	var toDismiss []uint64
	for idx, ov := range visible {
		isTop := idx == len(visible)-1
		entry := ov
		ui.Overlay(c, func() {
			back := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
				Justify(ui.Center).AlignItems(ui.Center).Modal()
			if !entry.opts.NoBackdrop {
				bg := entry.opts.BackdropColor
				if bg.A == 0 {
					bg = resolved.OverlayBackdrop
				}
				back.Background(bg)
			}
			back.Children(func() {
				panelBg := entry.opts.PanelBackground
				if panelBg.A == 0 {
					panelBg = resolved.Raised
				}
				panelBorder := resolved.BorderStrong
				if panelBorder.A == 0 {
					panelBorder = resolved.Border
				}
				panel := ui.Column(c).
					Background(panelBg).
					Border(1, panelBorder).
					Radius(13).
					Shadow(0, 14, 36, 0, ui.RGBA(0, 0, 0, 0.34)).
					Padding(16).
					Gap(10)
				if entry.opts.Width > 0 {
					panel.Width(entry.opts.Width)
				}
				if entry.opts.Height > 0 {
					panel.Height(entry.opts.Height)
				}
				if entry.opts.MaxWidth > 0 {
					panel.MaxWidth(entry.opts.MaxWidth)
				}
				if entry.opts.MaxHeight > 0 {
					panel.MaxHeight(entry.opts.MaxHeight)
				}
				if entry.opts.MaxWidthPercent > 0 {
					panel.MaxWidthPercent(entry.opts.MaxWidthPercent)
				} else if entry.opts.Width == 0 && entry.opts.MaxWidth == 0 {
					panel.MaxWidthPercent(88)
				}
				if entry.opts.MaxHeightPercent > 0 {
					panel.MaxHeightPercent(entry.opts.MaxHeightPercent)
				} else if entry.opts.Height == 0 && entry.opts.MaxHeight == 0 {
					panel.MaxHeightPercent(85)
				}

				if isTop {
					escPressed := panel.OverlayShortcut(0, ui.KeyEscape)
					outside := !entry.opts.DisableBackdropClose && panel.PressedOutside()
					if (escPressed && !entry.opts.DisableEscape) || outside {
						toDismiss = append(toDismiss, entry.id)
					}
				}
				panel.Children(func() {
					if entry.build != nil {
						entry.build(c)
					}
				})
			})
		})
	}

	if len(toDismiss) > 0 {
		var callbacks []func()
		g.mu.Lock()
		for _, id := range toDismiss {
			for i, ov := range g.overlays {
				if ov.id == id {
					if ov.opts.OnDismiss != nil {
						callbacks = append(callbacks, ov.opts.OnDismiss)
					}
					g.overlays = append(g.overlays[:i], g.overlays[i+1:]...)
					break
				}
			}
		}
		g.mu.Unlock()
		for _, cb := range callbacks {
			cb()
		}
		c.Invalidate()
	}
}

// ReadClipboardText reads plain text from the active clipboard (desktop hook,
// active UI context Services, or system Clipboard when running).
func (g *GUI) ReadClipboardText() string {
	g.mu.Lock()
	hooks := g.desktop
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if hooks != nil && hooks.ReadClipboardText != nil {
		return hooks.ReadClipboardText()
	}
	if hasSvc {
		return svc.ReadClipboard()
	}
	if mygo.App.IsReady() {
		return mygo.Clipboard.ReadText()
	}
	return ""
}

// WriteClipboardText copies plain text to the active clipboard.
func (g *GUI) WriteClipboardText(text string) error {
	g.mu.Lock()
	hooks := g.desktop
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if hooks != nil && hooks.WriteClipboardText != nil {
		return hooks.WriteClipboardText(text)
	}
	if hasSvc {
		svc.WriteClipboard(text)
		return nil
	}
	if mygo.App.IsReady() {
		mygo.Clipboard.WriteText(text)
		return nil
	}
	return ErrDesktopUnavailable
}

// ReadClipboardPNG reads PNG image bytes from the clipboard if available.
func (g *GUI) ReadClipboardPNG() ([]byte, error) {
	g.mu.Lock()
	hooks := g.desktop
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if hooks != nil && hooks.ReadClipboardPNG != nil {
		return hooks.ReadClipboardPNG()
	}
	if hasSvc {
		data := svc.ReadClipboardData(transfer.PNG)
		b, err := data.Read(transfer.PNG)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	if mygo.App.IsReady() {
		b := mygo.Clipboard.ReadImage()
		if len(b) == 0 {
			return nil, transfer.ErrFormat
		}
		return b, nil
	}
	return nil, ErrDesktopUnavailable
}

// WriteClipboardPNG writes PNG image bytes onto the clipboard.
func (g *GUI) WriteClipboardPNG(png []byte) error {
	g.mu.Lock()
	hooks := g.desktop
	svc := g.services
	hasSvc := g.hasServices
	g.mu.Unlock()

	if hooks != nil && hooks.WriteClipboardPNG != nil {
		return hooks.WriteClipboardPNG(png)
	}
	if hasSvc {
		data := transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, png)))
		svc.WriteClipboardData(data)
		return nil
	}
	if mygo.App.IsReady() {
		return mygo.Clipboard.WriteImage(png)
	}
	return ErrDesktopUnavailable
}

// OpenFileDialog displays a native file/directory picker sheet attached to the window.
func (g *GUI) OpenFileDialog(opts mygo.OpenDialogOptions) ([]string, error) {
	g.mu.Lock()
	hooks := g.desktop
	w := g.win
	g.mu.Unlock()

	if opts.Parent == nil {
		opts.Parent = w
	}
	if hooks != nil && hooks.OpenFileDialog != nil {
		return hooks.OpenFileDialog(opts)
	}
	if mygo.App.IsReady() {
		return mygo.Dialog.Open(opts)
	}
	return nil, ErrDesktopUnavailable
}

// SaveFileDialog displays a native save file dialog attached to the window.
func (g *GUI) SaveFileDialog(opts mygo.SaveDialogOptions) (string, error) {
	g.mu.Lock()
	hooks := g.desktop
	w := g.win
	g.mu.Unlock()

	if opts.Parent == nil {
		opts.Parent = w
	}
	if hooks != nil && hooks.SaveFileDialog != nil {
		return hooks.SaveFileDialog(opts)
	}
	if mygo.App.IsReady() {
		return mygo.Dialog.Save(opts)
	}
	return "", ErrDesktopUnavailable
}

// ShowMessageDialog displays a native alert/confirmation dialog.
func (g *GUI) ShowMessageDialog(opts mygo.MessageOptions) (mygo.MessageResult, error) {
	g.mu.Lock()
	hooks := g.desktop
	w := g.win
	g.mu.Unlock()

	if opts.Parent == nil {
		opts.Parent = w
	}
	if hooks != nil && hooks.MessageDialog != nil {
		return hooks.MessageDialog(opts)
	}
	if mygo.App.IsReady() {
		return mygo.Dialog.Message(opts)
	}
	return mygo.MessageResult{Button: -1}, ErrDesktopUnavailable
}

// ShowErrorDialog displays a native error dialog.
func (g *GUI) ShowErrorDialog(title, message string) {
	g.mu.Lock()
	hooks := g.desktop
	g.mu.Unlock()

	if hooks != nil && hooks.ErrorDialog != nil {
		hooks.ErrorDialog(title, message)
		return
	}
	if mygo.App.IsReady() {
		mygo.Dialog.Error(title, message)
	}
}

// Notify displays a desktop notification when supported by the OS and running app.
func (g *GUI) Notify(opts mygo.NotificationOptions) error {
	g.mu.Lock()
	hooks := g.desktop
	g.mu.Unlock()

	if hooks != nil && hooks.Notify != nil {
		return hooks.Notify(opts)
	}
	if !mygo.App.IsReady() {
		return ErrDesktopUnavailable
	}
	if !mygo.NotificationsSupported() {
		return ErrDesktopUnavailable
	}
	n := mygo.NewNotification(opts)
	n.Show()
	return nil
}
