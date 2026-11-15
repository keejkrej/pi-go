// Ported from packages/coding-agent/src/modes/interactive/tui-renderer.ts (pi v1.5.2).

package interactive

import (
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// InteractiveTuiOptions configures CreateInteractiveTui.
type InteractiveTuiOptions struct {
	TuiMode                    core.TuiMode
	ShowHardwareCursor         bool
	LogDirectory               string
	Terminal                   tui.Terminal
	OnRightClickPaste          func()
	FullscreenCopyOnSelect     *bool
	FullscreenWheelScrollLines *tui.WheelScrollLines
}

// CreateInteractiveTui constructs either a fullscreen TuiAltScreen or regular TuiMainScreen renderer.
func CreateInteractiveTui(options InteractiveTuiOptions) tui.TUI {
	terminal := options.Terminal
	if terminal == nil {
		terminal = tui.NewProcessTerminal()
	}
	var logDir *string
	if options.LogDirectory != "" {
		dir := options.LogDirectory
		logDir = &dir
	}
	showHardwareCursor := options.ShowHardwareCursor

	if options.TuiMode == tui.TuiModeFullscreen {
		styleSearchMatch := func(text string) string {
			th := theme.CurrentTheme()
			return th.Bg(theme.BgSearchMatch, th.Fg(theme.FgSearchMatchText, text))
		}
		wheelScrollLines := options.FullscreenWheelScrollLines
		if wheelScrollLines == nil {
			auto := tui.WheelScrollLines{Auto: true}
			wheelScrollLines = &auto
		}
		return tui.NewTuiAltScreen(terminal, &showHardwareCursor, logDir, &tui.TuiAltScreenOptions{
			SearchMatchStyle: func(text string) string {
				return theme.CurrentTheme().Underline(styleSearchMatch(text))
			},
			SearchCurrentMatchStyle: func(text string) string {
				th := theme.CurrentTheme()
				return th.Bold(th.Inverse(styleSearchMatch(text)))
			},
			SearchNavigationButtonStyle: func(text string, hovered bool) string {
				if hovered {
					return theme.CurrentTheme().Underline(text)
				}
				return text
			},
			ScrollToEndIndicator: func() string {
				shortcut := theme.KeyDisplayText(tui.KeybindingTuiAltScreenBottom)
				label := " ↓ Jump to latest message"
				if shortcut != "" {
					label += " · " + shortcut
				}
				label += " "
				th := theme.CurrentTheme()
				return th.Bg(theme.BgSelected, th.Fg(theme.FgText, label))
			},
			OpenUrl:           utils.OpenBrowser,
			OnRightClickPaste: options.OnRightClickPaste,
			CopyOnSelect:      options.FullscreenCopyOnSelect,
			WheelScrollLines:  wheelScrollLines,
			CopySelection: func(text string) (any, error) {
				if err := utils.CopyToClipboard(text); err != nil {
					return err.Error(), nil
				}
				return true, nil
			},
		})
	}

	return tui.NewTuiMainScreen(terminal, &showHardwareCursor, logDir)
}

// CreateInteractiveTuiReference returns a stable tui.TUI proxy delegating to the current renderer returned by getTui.
func CreateInteractiveTuiReference(getTui func() tui.TUI) tui.TUI {
	return &trTuiReference{getTui: getTui}
}

type trTuiReference struct {
	getTui func() tui.TUI
}

var (
	_ tui.TUI         = (*trTuiReference)(nil)
	_ tui.ViewportTUI = (*trTuiReference)(nil)
)

func (r *trTuiReference) trCurrent() tui.TUI {
	if r == nil || r.getTui == nil {
		return nil
	}
	return r.getTui()
}

// UnwrapTui returns the currently active underlying renderer.
func (r *trTuiReference) UnwrapTui() tui.TUI {
	return r.trCurrent()
}

func (r *trTuiReference) Render(width int) []string {
	if cur := r.trCurrent(); cur != nil {
		return cur.Render(width)
	}
	return nil
}

func (r *trTuiReference) Invalidate() {
	if cur := r.trCurrent(); cur != nil {
		cur.Invalidate()
	}
}

func (r *trTuiReference) Mode() tui.TuiMode {
	if cur := r.trCurrent(); cur != nil {
		return cur.Mode()
	}
	return tui.TuiModeRegular
}

func (r *trTuiReference) Children() []tui.Component {
	if cur := r.trCurrent(); cur != nil {
		return cur.Children()
	}
	return nil
}

func (r *trTuiReference) SetChildren(children []tui.Component) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetChildren(children)
	}
}

func (r *trTuiReference) Terminal() tui.Terminal {
	if cur := r.trCurrent(); cur != nil {
		return cur.Terminal()
	}
	return nil
}

func (r *trTuiReference) OnDebug() func() {
	if cur := r.trCurrent(); cur != nil {
		return cur.OnDebug()
	}
	return nil
}

func (r *trTuiReference) SetOnDebug(fn func()) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetOnDebug(fn)
	}
}

func (r *trTuiReference) FullRedraws() int {
	if cur := r.trCurrent(); cur != nil {
		return cur.FullRedraws()
	}
	return 0
}

func (r *trTuiReference) AddChild(component tui.Component) {
	if cur := r.trCurrent(); cur != nil {
		cur.AddChild(component)
	}
}

func (r *trTuiReference) RemoveChild(component tui.Component) {
	if cur := r.trCurrent(); cur != nil {
		cur.RemoveChild(component)
	}
}

func (r *trTuiReference) Clear() {
	if cur := r.trCurrent(); cur != nil {
		cur.Clear()
	}
}

func (r *trTuiReference) GetShowHardwareCursor() bool {
	if cur := r.trCurrent(); cur != nil {
		return cur.GetShowHardwareCursor()
	}
	return false
}

func (r *trTuiReference) SetShowHardwareCursor(enabled bool) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetShowHardwareCursor(enabled)
	}
}

func (r *trTuiReference) GetClearOnShrink() bool {
	if cur := r.trCurrent(); cur != nil {
		return cur.GetClearOnShrink()
	}
	return false
}

func (r *trTuiReference) SetClearOnShrink(enabled bool) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetClearOnShrink(enabled)
	}
}

func (r *trTuiReference) SetFocus(component tui.Component) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetFocus(component)
	}
}

func (r *trTuiReference) ShowOverlay(component tui.Component, options *tui.OverlayOptions) tui.OverlayHandle {
	if cur := r.trCurrent(); cur != nil {
		return cur.ShowOverlay(component, options)
	}
	return nil
}

func (r *trTuiReference) HideOverlay() {
	if cur := r.trCurrent(); cur != nil {
		cur.HideOverlay()
	}
}

func (r *trTuiReference) HasOverlay() bool {
	if cur := r.trCurrent(); cur != nil {
		return cur.HasOverlay()
	}
	return false
}

func (r *trTuiReference) Start() {
	if cur := r.trCurrent(); cur != nil {
		cur.Start()
	}
}

func (r *trTuiReference) Stop(options *tui.TuiStopOptions) {
	if cur := r.trCurrent(); cur != nil {
		cur.Stop(options)
	}
}

func (r *trTuiReference) RenderNow(force *bool) {
	if cur := r.trCurrent(); cur != nil {
		cur.RenderNow(force)
	}
}

func (r *trTuiReference) RequestRender(force *bool) {
	if cur := r.trCurrent(); cur != nil {
		cur.RequestRender(force)
	}
}

func (r *trTuiReference) AddInputListener(listener tui.TuiInputListener) func() {
	if cur := r.trCurrent(); cur != nil {
		return cur.AddInputListener(listener)
	}
	return func() {}
}

func (r *trTuiReference) RemoveInputListener(listener tui.TuiInputListener) {
	if cur := r.trCurrent(); cur != nil {
		cur.RemoveInputListener(listener)
	}
}

func (r *trTuiReference) OnTerminalColorSchemeChange(listener func(scheme tui.TerminalColorScheme)) func() {
	if cur := r.trCurrent(); cur != nil {
		return cur.OnTerminalColorSchemeChange(listener)
	}
	return func() {}
}

func (r *trTuiReference) SetTerminalColorSchemeNotifications(enabled bool) {
	if cur := r.trCurrent(); cur != nil {
		cur.SetTerminalColorSchemeNotifications(enabled)
	}
}

func (r *trTuiReference) QueryTerminalColors(options tui.TuiQueryTerminalColorsOptions) (tui.TerminalColors, error) {
	if cur := r.trCurrent(); cur != nil {
		return cur.QueryTerminalColors(options)
	}
	return tui.TerminalColors{}, nil
}

func (r *trTuiReference) Post(fn func()) {
	if cur := r.trCurrent(); cur != nil {
		cur.Post(fn)
	}
}

func (r *trTuiReference) ViewportTui() bool {
	return tui.IsViewportTui(r.trCurrent())
}

func (r *trTuiReference) SetLayoutRoot(component tui.Component) {
	if vt, ok := r.trCurrent().(tui.ViewportTUI); ok {
		vt.SetLayoutRoot(component)
	}
}

func (r *trTuiReference) ResetTextSelection() {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.ResetTextSelection()
	}
}

func (r *trTuiReference) SetCopyOnSelect(enabled bool) {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.SetCopyOnSelect(enabled)
	}
}

func (r *trTuiReference) GetCopyOnSelect() bool {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		return alt.GetCopyOnSelect()
	}
	return false
}

func (r *trTuiReference) SetWheelScrollLines(lines tui.WheelScrollLines) {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.SetWheelScrollLines(lines)
	}
}

func (r *trTuiReference) HasActiveSelection() bool {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		return alt.HasActiveSelection()
	}
	return false
}

func (r *trTuiReference) CopyActiveSelectionToClipboard() (bool, error) {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		return alt.CopyActiveSelectionToClipboard()
	}
	return false, nil
}

func (r *trTuiReference) Flash(message string, durationMs *int64) {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.Flash(message, durationMs)
	}
}

func (r *trTuiReference) ScrollBy(lines int) {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.ScrollBy(lines)
	}
}

func (r *trTuiReference) ScrollToTop() {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.ScrollToTop()
	}
}

func (r *trTuiReference) ScrollToBottom() {
	if alt, ok := r.trCurrent().(*tui.TuiAltScreen); ok {
		alt.ScrollToBottom()
	}
}
