// Ported from packages/coding-agent/src/modes/interactive/components/login-dialog.ts (pi v1.5.2).

package components

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// ErrLoginCancelled is returned when an interactive login prompt is cancelled or aborted.
var ErrLoginCancelled = errors.New("Login cancelled")

var (
	lgndlgCopyToClipboard = utils.CopyToClipboard
	lgndlgOpenBrowser     = utils.OpenBrowser
)

type lgndlgInputResult struct {
	val string
	err error
}

type lgndlgDeviceCodeAction struct {
	info ai.AuthEventDeviceCode
	hint *tui.Container
}

func lgndlgWebLink(text, url string) string {
	target := utils.WebUrl(url)
	if target == nil {
		return text
	}
	return tui.Hyperlink(text, *target)
}

func lgndlgStringPtr(s string) *string {
	return &s
}

func lgndlgBoolOrDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func lgndlgIsDarwin() bool {
	return runtime.GOOS == "darwin"
}

// LoginDialogComponent replaces the prompt editor during OAuth and API key login flows.
type LoginDialogComponent struct {
	*tui.Container
	mu               sync.Mutex
	contentContainer *tui.Container
	input            *tui.Input
	ui               tui.TUI
	authUrl          *AuthUrlComponent
	ctx              context.Context
	cancelCtx        context.CancelFunc
	pendingInput     chan lgndlgInputResult
	deviceCodeAction *lgndlgDeviceCodeAction
	deviceCodeHint   *tui.Container
	onComplete       func(success bool, message *string)
	completed        bool
	focused          bool
}

var _ tui.Focusable = (*LoginDialogComponent)(nil)

// NewLoginDialogComponent constructs a login dialog for providerId.
// Optional trailing arguments override provider display name (index 0) and dialog title (index 1).
func NewLoginDialogComponent(
	ui tui.TUI,
	providerId string,
	onComplete func(success bool, message *string),
	overrides ...string,
) *LoginDialogComponent {
	providerName := providerId
	if len(overrides) > 0 && overrides[0] != "" {
		providerName = overrides[0]
	}
	title := "Login to " + providerName
	if len(overrides) > 1 && overrides[1] != "" {
		title = overrides[1]
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	th := theme.CurrentTheme()

	d := &LoginDialogComponent{
		Container:        tui.NewContainer(),
		contentContainer: tui.NewContainer(),
		input:            tui.NewInput(nil),
		ui:               ui,
		ctx:              ctx,
		cancelCtx:        cancelCtx,
		onComplete:       onComplete,
	}

	d.AddChild(NewDynamicBorder())
	d.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0, nil))
	d.AddChild(d.contentContainer)
	d.AddChild(NewDynamicBorder())

	d.input.OnSubmit = func(string) {
		d.mu.Lock()
		ch := d.pendingInput
		if ch == nil {
			d.mu.Unlock()
			return
		}
		value := d.input.GetValue()
		d.replaceInputWithSubmittedTextLocked(value)
		d.pendingInput = nil
		d.mu.Unlock()
		ch <- lgndlgInputResult{val: value}
	}
	d.input.OnEscape = func() {
		d.cancel()
	}

	return d
}

// Signal returns a context cancelled when the user aborts the login dialog.
func (l *LoginDialogComponent) Signal() context.Context {
	return l.ctx
}

// Focused reports whether the dialog input holds focus.
func (l *LoginDialogComponent) Focused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.focused
}

// SetFocused propagates focus state to the embedded Input.
func (l *LoginDialogComponent) SetFocused(focused bool) {
	l.mu.Lock()
	l.focused = focused
	in := l.input
	l.mu.Unlock()
	in.SetFocused(focused)
}

// Invalidate drops cached rendering state.
func (l *LoginDialogComponent) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Container.Invalidate()
}

// Render renders the login dialog contents for width.
func (l *LoginDialogComponent) Render(width int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Container.Render(width)
}

func (l *LoginDialogComponent) requestRender(force *bool) {
	if l.ui != nil {
		l.ui.RequestRender(force)
	}
}

func (l *LoginDialogComponent) replaceInputWithSubmittedTextLocked(value string) {
	children := l.contentContainer.Children()
	replaced := make([]tui.Component, len(children))
	for i, child := range children {
		if child == l.input {
			replaced[i] = tui.NewText("> "+value, 0, 0, nil)
		} else {
			replaced[i] = child
		}
	}
	l.contentContainer.SetChildren(replaced)
}

func (l *LoginDialogComponent) clearDeviceCodeActionLocked() {
	if l.deviceCodeHint != nil {
		l.contentContainer.RemoveChild(l.deviceCodeHint)
	}
	l.deviceCodeHint = nil
	l.deviceCodeAction = nil
}

func (l *LoginDialogComponent) cancel() {
	l.mu.Lock()
	l.clearDeviceCodeActionLocked()
	l.cancelCtx()
	ch := l.pendingInput
	l.pendingInput = nil
	var onComplete func(bool, *string)
	if !l.completed && l.onComplete != nil {
		l.completed = true
		onComplete = l.onComplete
	}
	l.mu.Unlock()

	if ch != nil {
		ch <- lgndlgInputResult{err: ErrLoginCancelled}
	}
	if onComplete != nil {
		onComplete(false, lgndlgStringPtr(ErrLoginCancelled.Error()))
	}
}

// ShowAuth displays the OAuth authorization URL, optional instructions, and opens web URLs in a browser.
func (l *LoginDialogComponent) ShowAuth(url string, instructions ...string) {
	target := utils.WebUrl(url)
	shown := tui.ReplaceControlCharacters(url)
	if target != nil {
		shown = *target
	}
	// Construct AuthUrlComponent before acquiring l.mu because NewAuthUrlComponent may invoke ui.RequestRender.
	authUrl := NewAuthUrlComponent(l.ui, shown)

	l.mu.Lock()
	l.clearDeviceCodeActionLocked()
	l.contentContainer.Clear()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.authUrl = authUrl
	l.contentContainer.AddChild(authUrl)
	if len(instructions) > 0 && instructions[0] != "" {
		th := theme.CurrentTheme()
		l.contentContainer.AddChild(tui.NewSpacer(1))
		l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgWarning, instructions[0]), 1, 0, nil))
	}
	l.mu.Unlock()

	if target != nil {
		lgndlgOpenBrowser(*target)
	}
	l.requestRender(nil)
}

// ShowDeviceCode displays the verification URL, user code, and optional confirmation action to open/copy.
func (l *LoginDialogComponent) ShowDeviceCode(info ai.AuthEventDeviceCode) {
	openOnConfirm := lgndlgBoolOrDefault(info.OpenBrowserOnConfirm, true)
	copyOnConfirm := lgndlgBoolOrDefault(info.CopyCodeOnConfirm, true)
	info.OpenBrowserOnConfirm = &openOnConfirm
	info.CopyCodeOnConfirm = &copyOnConfirm

	th := theme.CurrentTheme()
	shownUrl := utils.DisplayUrl(info.VerificationUri)

	l.mu.Lock()
	l.authUrl = nil
	l.clearDeviceCodeActionLocked()
	l.contentContainer.Clear()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgAccent, lgndlgWebLink(shownUrl, shownUrl)), 1, 0, nil))

	clickHint := "Ctrl+click to open"
	if lgndlgIsDarwin() {
		clickHint = "Cmd+click to open"
	}
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgDim, lgndlgWebLink(clickHint, info.VerificationUri)), 1, 0, nil))
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgWarning, "Enter code: "+info.UserCode), 1, 0, nil))

	if l.ctx.Err() == nil && (openOnConfirm || copyOnConfirm) {
		var actionLabel string
		if openOnConfirm {
			if copyOnConfirm {
				actionLabel = "to open browser and copy code"
			} else {
				actionLabel = "to open browser"
			}
		} else {
			actionLabel = "to copy code"
		}
		hint := tui.NewContainer()
		hint.AddChild(tui.NewTruncatedText("("+theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, actionLabel)+")", 1, 0))
		l.contentContainer.AddChild(hint)
		l.deviceCodeHint = hint
		l.deviceCodeAction = &lgndlgDeviceCodeAction{info: info, hint: hint}
	}
	l.mu.Unlock()

	l.requestRender(nil)
}

func (l *LoginDialogComponent) setDeviceCodeStatus(hint *tui.Container, message string) {
	l.mu.Lock()
	if l.ctx.Err() != nil || !l.focused || !slices.Contains(l.contentContainer.Children(), tui.Component(hint)) {
		l.mu.Unlock()
		return
	}
	th := theme.CurrentTheme()
	hint.SetChildren([]tui.Component{tui.NewTruncatedText(th.Fg(theme.FgDim, message), 1, 0)})
	l.mu.Unlock()

	l.requestRender(nil)
}

func (l *LoginDialogComponent) activateDeviceCode(info ai.AuthEventDeviceCode, hint *tui.Container) {
	copyOnConfirm := lgndlgBoolOrDefault(info.CopyCodeOnConfirm, true)
	openOnConfirm := lgndlgBoolOrDefault(info.OpenBrowserOnConfirm, true)

	initialMsg := "Browser open requested"
	if copyOnConfirm {
		initialMsg = "Copying code..."
	}
	l.setDeviceCodeStatus(hint, initialMsg)

	if copyOnConfirm {
		code := info.UserCode
		ui := l.ui
		go func() {
			msg := "Code copied to clipboard"
			if err := lgndlgCopyToClipboard(code); err != nil {
				msg = "Copy code manually"
			}
			if ui != nil {
				ui.Post(func() {
					l.setDeviceCodeStatus(hint, msg)
				})
			} else {
				l.setDeviceCodeStatus(hint, msg)
			}
		}()
	}

	if openOnConfirm {
		if target := utils.WebUrl(info.VerificationUri); target != nil {
			func() {
				defer func() { _ = recover() }()
				lgndlgOpenBrowser(*target)
			}()
		}
	}
}

func (l *LoginDialogComponent) awaitInput(ctx context.Context, ch chan lgndlgInputResult) (string, error) {
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	select {
	case res := <-ch:
		return res.val, res.err
	case <-l.ctx.Done():
		l.mu.Lock()
		if l.pendingInput == ch {
			l.pendingInput = nil
		}
		l.mu.Unlock()
		return "", ErrLoginCancelled
	case <-ctxDone:
		l.mu.Lock()
		if l.pendingInput == ch {
			l.pendingInput = nil
		}
		l.mu.Unlock()
		return "", ctx.Err()
	}
}

// ShowManualInput renders a manual code/URL entry field and blocks until submitted, cancelled, or ctx ends.
func (l *LoginDialogComponent) ShowManualInput(ctx context.Context, prompt string) (string, error) {
	l.mu.Lock()
	if l.ctx.Err() != nil {
		l.mu.Unlock()
		return "", ErrLoginCancelled
	}
	if ctx != nil && ctx.Err() != nil {
		l.mu.Unlock()
		return "", ctx.Err()
	}
	th := theme.CurrentTheme()
	l.clearDeviceCodeActionLocked()
	l.input.SetValue("")
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgDim, prompt), 1, 0, nil))
	l.contentContainer.AddChild(l.input)
	l.contentContainer.AddChild(tui.NewText("("+theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "to cancel")+")", 1, 0, nil))
	ch := make(chan lgndlgInputResult, 1)
	l.pendingInput = ch
	l.mu.Unlock()

	l.requestRender(nil)
	return l.awaitInput(ctx, ch)
}

// ShowPrompt appends a prompt input field (preserving existing auth content) and blocks until resolved.
func (l *LoginDialogComponent) ShowPrompt(ctx context.Context, message string, placeholder ...string) (string, error) {
	l.mu.Lock()
	if l.ctx.Err() != nil {
		l.mu.Unlock()
		return "", ErrLoginCancelled
	}
	if ctx != nil && ctx.Err() != nil {
		l.mu.Unlock()
		return "", ctx.Err()
	}
	th := theme.CurrentTheme()
	l.clearDeviceCodeActionLocked()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgText, message), 1, 0, nil))
	if len(placeholder) > 0 && placeholder[0] != "" {
		l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgDim, "e.g., "+placeholder[0]), 1, 0, nil))
	}
	l.contentContainer.AddChild(l.input)
	l.contentContainer.AddChild(tui.NewText(
		"("+theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "to cancel,")+" "+theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "to submit")+")",
		1,
		0,
		nil,
	))
	l.input.SetValue("")
	ch := make(chan lgndlgInputResult, 1)
	l.pendingInput = ch
	l.mu.Unlock()

	l.requestRender(nil)
	return l.awaitInput(ctx, ch)
}

// ShowDetails clears prior auth/device state and displays informational lines before another step.
func (l *LoginDialogComponent) ShowDetails(lines []string) {
	l.mu.Lock()
	l.authUrl = nil
	l.clearDeviceCodeActionLocked()
	l.contentContainer.Clear()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	for _, line := range lines {
		l.contentContainer.AddChild(tui.NewText(line, 1, 0, nil))
	}
	l.mu.Unlock()

	l.requestRender(nil)
}

// ShowInfo displays informational text, optional links, and an optional close hint.
func (l *LoginDialogComponent) ShowInfo(message string, links []ai.AuthInfoLink, showCloseHint ...bool) {
	closeHint := len(showCloseHint) > 0 && showCloseHint[0]
	th := theme.CurrentTheme()

	l.mu.Lock()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgText, message), 1, 0, nil))
	for _, link := range links {
		url := utils.DisplayUrl(link.Url)
		text := url
		if link.Label != nil && *link.Label != "" {
			text = *link.Label + ": " + url
		}
		l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgAccent, lgndlgWebLink(text, url)), 1, 0, nil))
	}
	if closeHint {
		l.contentContainer.AddChild(tui.NewSpacer(1))
		l.contentContainer.AddChild(tui.NewText("("+theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "to close")+")", 1, 0, nil))
	}
	l.mu.Unlock()

	l.requestRender(nil)
}

// ShowWaiting renders a polling status line with a cancel hint.
func (l *LoginDialogComponent) ShowWaiting(message string) {
	th := theme.CurrentTheme()
	l.mu.Lock()
	l.contentContainer.AddChild(tui.NewSpacer(1))
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgDim, message), 1, 0, nil))
	l.contentContainer.AddChild(tui.NewText("("+theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "to cancel")+")", 1, 0, nil))
	l.mu.Unlock()

	l.requestRender(nil)
}

// ShowProgress appends a dimmed progress line.
func (l *LoginDialogComponent) ShowProgress(message string) {
	th := theme.CurrentTheme()
	l.mu.Lock()
	l.contentContainer.AddChild(tui.NewText(th.Fg(theme.FgDim, message), 1, 0, nil))
	l.mu.Unlock()

	l.requestRender(nil)
}

// HandleInput processes cancel, URL copy, device-code confirmation, and input entry keys.
func (l *LoginDialogComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()
	if kb.Matches(data, tui.KeybindingTuiSelectCancel) {
		l.cancel()
		return
	}

	l.mu.Lock()
	authUrl := l.authUrl
	if authUrl != nil && kb.Matches(data, core.KeybindingAppMessageCopy) {
		l.mu.Unlock()
		_ = authUrl.Copy()
		return
	}

	var action *lgndlgDeviceCodeAction
	if l.deviceCodeAction != nil &&
		l.pendingInput == nil &&
		l.ctx.Err() == nil &&
		l.focused &&
		kb.Matches(data, tui.KeybindingTuiSelectConfirm) {
		action = l.deviceCodeAction
		l.deviceCodeAction = nil
	}
	in := l.input
	l.mu.Unlock()

	if action != nil {
		l.activateDeviceCode(action.info, action.hint)
		l.requestRender(nil)
		return
	}

	in.HandleInput(data)
}
