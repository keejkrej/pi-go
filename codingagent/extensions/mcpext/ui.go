// Ported from packages/coding-agent/src/extensions/mcp/ui.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const uiMaxVisibleItems = 12

// McpMenu describes one interactive selection screen shown by McpUi.Menu.
type McpMenu struct {
	Title        string
	Details      *string
	Error        *string
	Items        []tui.SelectItem
	Empty        *string
	Selected     *string
	ConfirmLabel string
	CancelLabel  string
}

// McpUi abstracts the interactive screens used by `/mcp` and `/mcp login`.
type McpUi interface {
	Menu(ctx context.Context, build func() McpMenu, subscribe func(listener func()) func()) *string
	Status(title, message string, onCancel func())
	RedirectUrl(ctx context.Context, title, authorizationUrl string) *string
}

type uiInputHandler interface {
	HandleInput(data string)
}

type uiCancelHandler struct {
	keybindings *tui.KeybindingsManager
	onCancel    func()
}

func (h *uiCancelHandler) HandleInput(data string) {
	if h.keybindings != nil && h.keybindings.Matches(data, tui.KeybindingTuiSelectCancel) && h.onCancel != nil {
		h.onCancel()
	}
}

type uiRedirectHandler struct {
	keybindings *tui.KeybindingsManager
	input       *tui.Input
	link        *components.AuthUrlComponent
	onSubmit    func(value string)
	onCancel    func()
}

func (h *uiRedirectHandler) HandleInput(data string) {
	if h.keybindings != nil {
		if h.keybindings.Matches(data, tui.KeybindingTuiSelectConfirm) {
			value := strings.TrimSpace(h.input.GetValue())
			if value != "" && h.onSubmit != nil {
				h.onSubmit(value)
			}
			return
		}
		if h.keybindings.Matches(data, tui.KeybindingTuiSelectCancel) {
			if h.onCancel != nil {
				h.onCancel()
			}
			return
		}
		if h.keybindings.Matches(data, core.KeybindingAppMessageCopy) {
			_ = h.link.Copy()
			return
		}
	}
	h.input.HandleInput(data)
}

func uiFrame(th *theme.Theme, title string, body []tui.Component, footer *string) *tui.Container {
	accentBorder := components.NewDynamicBorder(func(text string) string {
		return th.Fg(theme.FgAccent, text)
	})
	container := tui.NewContainer()
	container.AddChild(accentBorder)
	container.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0, nil))
	for _, child := range body {
		container.AddChild(child)
	}
	if footer != nil && *footer != "" {
		container.AddChild(tui.NewSpacer(1))
		container.AddChild(tui.NewText(th.Fg(theme.FgDim, *footer), 1, 0, nil))
	}
	container.AddChild(components.NewDynamicBorder(func(text string) string {
		return th.Fg(theme.FgAccent, text)
	}))
	return container
}

// McpManagerView implements McpUi as a focusable TUI component for `/mcp`.
type McpManagerView struct {
	tui         tui.TUI
	theme       *theme.Theme
	keybindings *tui.KeybindingsManager

	mu           sync.Mutex
	content      *tui.Container
	inputHandler uiInputHandler
	inputTarget  tui.Focusable
	focused      bool
}

// NewMcpManagerView constructs an McpManagerView.
func NewMcpManagerView(t tui.TUI, th *theme.Theme, kb *tui.KeybindingsManager) *McpManagerView {
	if th == nil {
		th = theme.CurrentTheme()
	}
	view := &McpManagerView{
		tui:         t,
		theme:       th,
		keybindings: kb,
	}
	view.content = uiFrame(th, "MCP servers", []tui.Component{
		tui.NewText(th.Fg(theme.FgMuted, "Loading…"), 1, 1, nil),
	}, nil)
	return view
}

func (v *McpManagerView) Focused() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.focused
}

func (v *McpManagerView) SetFocused(value bool) {
	v.mu.Lock()
	v.focused = value
	target := v.inputTarget
	v.mu.Unlock()
	if target != nil {
		target.SetFocused(value)
	}
}

// CapturesViewportInput reports whether an active menu list captures alt-screen paging keys.
func (v *McpManagerView) CapturesViewportInput() bool {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if cv, ok := handler.(interface{ CapturesViewportInput() bool }); ok {
		return cv.CapturesViewportInput()
	}
	return false
}

func (v *McpManagerView) setContent(content *tui.Container, inputHandler uiInputHandler, inputTarget tui.Focusable) {
	v.mu.Lock()
	oldTarget := v.inputTarget
	v.content = content
	v.inputHandler = inputHandler
	v.inputTarget = inputTarget
	focused := v.focused
	v.mu.Unlock()

	if oldTarget != nil {
		oldTarget.SetFocused(false)
	}
	if inputTarget != nil {
		inputTarget.SetFocused(focused)
	}
	if v.tui != nil {
		v.tui.RequestRender(nil)
	}
}

// Menu shows an interactive menu built by build, rebuilding on subscribe notifications while keeping selection.
func (v *McpManagerView) Menu(ctx context.Context, build func() McpMenu, subscribe func(listener func()) func()) *string {
	if ctx == nil {
		ctx = context.Background()
	}
	resultCh := make(chan *string, 1)

	var finishOnce sync.Once
	var stateMu sync.Mutex
	var unsub func()
	var selected *string
	settled := false

	finish := func(value *string) {
		finishOnce.Do(func() {
			stateMu.Lock()
			settled = true
			fn := unsub
			stateMu.Unlock()
			if fn != nil {
				fn()
			}
			resultCh <- value
		})
	}

	var renderMenu func()
	renderMenu = func() {
		stateMu.Lock()
		isSettled := settled
		curSelected := selected
		stateMu.Unlock()
		if isSettled {
			return
		}

		menu := build()
		wanted := curSelected
		if wanted == nil {
			wanted = menu.Selected
		}

		var body []tui.Component
		if menu.Details != nil && *menu.Details != "" {
			body = append(body, tui.NewText(v.theme.Fg(theme.FgMuted, *menu.Details), 1, 0, nil))
		}
		if menu.Error != nil && *menu.Error != "" {
			body = append(body, tui.NewText(v.theme.Fg(theme.FgError, *menu.Error), 1, 0, nil))
		}
		body = append(body, tui.NewSpacer(1))

		cancelFooter := theme.AppKeyHint(v.theme, tui.KeybindingTuiSelectCancel, menu.CancelLabel)
		if len(menu.Items) == 0 {
			emptyText := "Nothing to show."
			if menu.Empty != nil && *menu.Empty != "" {
				emptyText = *menu.Empty
			}
			body = append(body, tui.NewText(v.theme.Fg(theme.FgMuted, emptyText), 1, 0, nil))
			v.setContent(
				uiFrame(v.theme, menu.Title, body, &cancelFooter),
				&uiCancelHandler{
					keybindings: v.keybindings,
					onCancel:    func() { finish(nil) },
				},
				nil,
			)
			return
		}

		footer := fmt.Sprintf(
			"%s • %s",
			theme.AppKeyHint(v.theme, tui.KeybindingTuiSelectConfirm, menu.ConfirmLabel),
			cancelFooter,
		)
		visible := min(len(menu.Items), uiMaxVisibleItems)
		list := tui.NewSelectList(menu.Items, visible, theme.GetSelectListTheme(), nil)
		if wanted != nil {
			for idx, item := range menu.Items {
				if item.Value == *wanted {
					list.SetSelectedIndex(idx)
					break
				}
			}
		}
		if sel := list.GetSelectedItem(); sel != nil {
			val := sel.Value
			stateMu.Lock()
			if !settled {
				selected = &val
			}
			stateMu.Unlock()
		}
		list.OnSelectionChange = func(item tui.SelectItem) {
			val := item.Value
			stateMu.Lock()
			selected = &val
			stateMu.Unlock()
		}
		list.OnSelect = func(item tui.SelectItem) {
			val := item.Value
			finish(&val)
		}
		list.OnCancel = func() {
			finish(nil)
		}
		body = append(body, list)
		v.setContent(uiFrame(v.theme, menu.Title, body, &footer), list, nil)
	}

	renderMenu()
	if subscribe != nil {
		u := subscribe(func() {
			renderMenu()
		})
		stateMu.Lock()
		isSettled := settled
		if !isSettled {
			unsub = u
		}
		stateMu.Unlock()
		if isSettled && u != nil {
			u()
		}
	}

	select {
	case val := <-resultCh:
		return val
	case <-ctx.Done():
		finish(nil)
		return <-resultCh
	}
}

// Status shows a status screen while an operation is running, optionally supporting cancellation.
func (v *McpManagerView) Status(title, message string, onCancel func()) {
	body := []tui.Component{
		tui.NewSpacer(1),
		tui.NewText(v.theme.Fg(theme.FgMuted, message), 1, 0, nil),
	}
	if onCancel == nil {
		v.setContent(uiFrame(v.theme, title, body, nil), nil, nil)
		return
	}
	footer := theme.AppKeyHint(v.theme, tui.KeybindingTuiSelectCancel, "cancel")
	v.setContent(
		uiFrame(v.theme, title, body, &footer),
		&uiCancelHandler{
			keybindings: v.keybindings,
			onCancel:    onCancel,
		},
		nil,
	)
}

// RedirectUrl shows the OAuth authorization URL and waits for either a pasted redirect URL or ctx cancellation.
func (v *McpManagerView) RedirectUrl(ctx context.Context, title, authorizationUrl string) *string {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil
	}

	resultCh := make(chan *string, 1)
	var finishOnce sync.Once
	finish := func(val *string) {
		finishOnce.Do(func() {
			resultCh <- val
		})
	}

	input := tui.NewInput(nil)
	link := components.NewAuthUrlComponent(v.tui, authorizationUrl)
	body := []tui.Component{
		tui.NewSpacer(1),
		tui.NewText(v.theme.Fg(theme.FgMuted, "Approve access in your browser. If it did not open, visit:"), 1, 0, nil),
		link,
		tui.NewSpacer(1),
		tui.NewText(
			v.theme.Fg(theme.FgMuted, "If the browser runs on another machine, paste the URL it was redirected to:"),
			1,
			0,
			nil,
		),
		input,
	}
	footer := fmt.Sprintf(
		"%s • %s",
		theme.AppKeyHint(v.theme, tui.KeybindingTuiSelectConfirm, "submit"),
		theme.AppKeyHint(v.theme, tui.KeybindingTuiSelectCancel, "cancel"),
	)
	v.setContent(
		uiFrame(v.theme, title, body, &footer),
		&uiRedirectHandler{
			keybindings: v.keybindings,
			input:       input,
			link:        link,
			onSubmit: func(val string) {
				s := val
				finish(&s)
			},
			onCancel: func() {
				finish(nil)
			},
		},
		input,
	)

	select {
	case val := <-resultCh:
		return val
	case <-ctx.Done():
		finish(nil)
		return <-resultCh
	}
}

func (v *McpManagerView) HandleInput(data string) {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if handler != nil {
		handler.HandleInput(data)
	}
	if v.tui != nil {
		v.tui.RequestRender(nil)
	}
}

func (v *McpManagerView) Render(width int) []string {
	v.mu.Lock()
	content := v.content
	v.mu.Unlock()
	lines := content.Render(width)
	out := make([]string, len(lines))
	for i, line := range lines {
		if tui.VisibleWidth(line) > width {
			out[i] = tui.TruncateToWidth(line, width, "", false)
		} else {
			out[i] = line
		}
	}
	return out
}

func (v *McpManagerView) Invalidate() {
	v.mu.Lock()
	content := v.content
	v.mu.Unlock()
	content.Invalidate()
}

// ShowMcpManager opens the interactive MCP manager overlay and runs manage until it returns.
func ShowMcpManager(ctx context.Context, ectx core.ExtensionCommandContext, manage func(ui McpUi) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := ectx.UI().Custom(ctx, func(t tui.TUI, th *theme.Theme, kb *tui.KeybindingsManager, done func(any)) (tui.Component, error) {
		view := NewMcpManagerView(t, th, kb)
		go func() {
			defer done(nil)
			if manage != nil {
				if err := manage(view); err != nil {
					ectx.UI().Notify(err.Error(), "error")
				}
			}
		}()
		return view, nil
	})
	return err
}
