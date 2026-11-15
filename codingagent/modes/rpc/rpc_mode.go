// Ported from packages/coding-agent/src/modes/rpc/rpc-mode.ts (pi v1.5.2).

package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// RpcModeOptions configures headless JSONL stdin/stdout RPC mode.
type RpcModeOptions struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	SignalChan <-chan os.Signal
	OnExit     func(code int)
}

type rmRuntimeHost interface {
	CurrentSession() *core.AgentSession
	NewSession(ctx context.Context, options ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error)
	SwitchSession(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error)
	Fork(ctx context.Context, entryId string, options ...*core.ForkSessionOptions) (*core.ForkSessionResult, error)
	Dispose(ctx context.Context) error
}

type rmCoreRuntimeAdapter struct {
	runtime *core.AgentSessionRuntime
}

func (a *rmCoreRuntimeAdapter) CurrentSession() *core.AgentSession {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Session
}

func (a *rmCoreRuntimeAdapter) NewSession(ctx context.Context, options ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error) {
	return a.runtime.NewSession(ctx, options...)
}

func (a *rmCoreRuntimeAdapter) SwitchSession(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error) {
	return a.runtime.SwitchSession(ctx, sessionPath)
}

func (a *rmCoreRuntimeAdapter) Fork(ctx context.Context, entryId string, options ...*core.ForkSessionOptions) (*core.ForkSessionResult, error) {
	return a.runtime.Fork(ctx, entryId, options...)
}

func (a *rmCoreRuntimeAdapter) Dispose(ctx context.Context) error {
	return a.runtime.Dispose(ctx)
}

// RunRpcMode runs pi in headless JSON stdin/stdout RPC mode.
// Commands are read as JSONL lines from stdin and responses/events/extension UI
// requests are emitted as JSONL lines on stdout until stdin closes, an extension
// requests shutdown cleanly, or a termination signal is received.
func RunRpcMode(ctx context.Context, runtimeHost *core.AgentSessionRuntime, opts ...*RpcModeOptions) error {
	var opt *RpcModeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	return rmRunRpcMode(ctx, &rmCoreRuntimeAdapter{runtime: runtimeHost}, opt, nil)
}

type rmHooks struct {
	onReady func()
}

type rmPendingUIRequest struct {
	ch chan RpcExtensionUIResponse
}

type rmRunner struct {
	baseCtx     context.Context
	dialogCtx   context.Context
	dialogStop  context.CancelFunc
	runtimeHost rmRuntimeHost
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	signalChan  <-chan os.Signal
	stopSignals func()
	onExit      func(code int)
	tookStdout  bool

	outMu sync.Mutex

	subMu                sync.Mutex
	session              *core.AgentSession
	unsubscribe          func()
	unsubscribeBackpress func()

	pendingMu sync.Mutex
	pendingUI map[string]rmPendingUIRequest

	lifecycleMu       sync.Mutex
	shutdownRequested bool
	shuttingDown      bool
	inputReady        bool
	activeCommands    int
	signalCount       int

	detachOnce sync.Once
	detachFn   func()
	exitOnce   sync.Once
	doneOnce   sync.Once
	doneCh     chan struct{}
}

func rmSignalExitCode(sig os.Signal) int {
	if sig == syscall.SIGHUP {
		return 129
	}
	return 143
}

func rmDefaultSignalChannel() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 4)
	if runtime.GOOS != "windows" {
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)
	} else {
		signal.Notify(ch, syscall.SIGTERM)
	}
	return ch, func() { signal.Stop(ch) }
}

func rmRunRpcMode(ctx context.Context, runtimeHost rmRuntimeHost, opts *RpcModeOptions, hooks *rmHooks) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtimeHost == nil || runtimeHost.CurrentSession() == nil {
		return errors.New("RPC mode error: session runtime is not initialized")
	}

	var options RpcModeOptions
	if opts != nil {
		options = *opts
	}
	stdin := options.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	tookStdout := false
	if options.Stdout == nil {
		core.TakeOverStdout()
		tookStdout = true
	}

	sigCh := options.SignalChan
	var stopSignals func()
	if sigCh == nil {
		sigCh, stopSignals = rmDefaultSignalChannel()
	}

	dialogCtx, dialogStop := context.WithCancel(ctx)
	r := &rmRunner{
		baseCtx:     ctx,
		dialogCtx:   dialogCtx,
		dialogStop:  dialogStop,
		runtimeHost: runtimeHost,
		stdin:       stdin,
		stdout:      options.Stdout,
		stderr:      stderr,
		signalChan:  sigCh,
		stopSignals: stopSignals,
		onExit:      options.OnExit,
		tookStdout:  tookStdout,
		session:     runtimeHost.CurrentSession(),
		pendingUI:   make(map[string]rmPendingUIRequest),
		doneCh:      make(chan struct{}),
	}

	if err := r.rebindSession(ctx); err != nil {
		r.stopSignalWatcher()
		dialogStop()
		if tookStdout {
			core.RestoreStdout()
		}
		return err
	}

	signalWatcherStop := make(chan struct{})
	signalWatcherDone := make(chan struct{})
	go func() {
		defer close(signalWatcherDone)
		for {
			select {
			case sig, ok := <-r.signalChan:
				if !ok || sig == nil {
					return
				}
				code := rmSignalExitCode(sig)
				utils.KillTrackedDetachedChildren()
				r.lifecycleMu.Lock()
				r.signalCount++
				count := r.signalCount
				alreadyShuttingDown := r.shuttingDown
				r.lifecycleMu.Unlock()
				if !alreadyShuttingDown {
					r.startShutdown(code, sig)
				} else if count > 1 {
					r.forceExit(code)
				}
			case <-signalWatcherStop:
				return
			}
		}
	}()

	contextWatcherStop := make(chan struct{})
	contextWatcherDone := make(chan struct{})
	go func() {
		defer close(contextWatcherDone)
		select {
		case <-ctx.Done():
			r.startShutdown(0, nil)
		case <-contextWatcherStop:
			return
		}
	}()

	var detachReader func()
	detachReader = AttachJsonlLineReader(ctx, r.stdin, func(line string) {
		r.onStdinLine(line)
	}, func() {
		r.startShutdown(0, nil)
	})
	r.setDetach(detachReader)

	r.lifecycleMu.Lock()
	r.inputReady = true
	r.lifecycleMu.Unlock()

	if hooks != nil && hooks.onReady != nil {
		hooks.onReady()
	}

	// Handle a shutdown requested during startup (e.g. from session_start).
	r.checkShutdownRequested()

	<-r.doneCh
	close(signalWatcherStop)
	close(contextWatcherStop)
	<-signalWatcherDone
	<-contextWatcherDone
	r.detachInput()
	dialogStop()
	return nil
}

func (r *rmRunner) setDetach(fn func()) {
	r.lifecycleMu.Lock()
	r.detachFn = fn
	r.lifecycleMu.Unlock()
}

func (r *rmRunner) detachInput() {
	r.detachOnce.Do(func() {
		r.lifecycleMu.Lock()
		fn := r.detachFn
		r.lifecycleMu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

func (r *rmRunner) stopSignalWatcher() {
	if r.stopSignals != nil {
		r.stopSignals()
		r.stopSignals = nil
	}
}

func (r *rmRunner) currentSession() *core.AgentSession {
	r.subMu.Lock()
	s := r.session
	r.subMu.Unlock()
	return s
}

func (r *rmRunner) output(v any) {
	line, err := SerializeJsonLine(v)
	if err != nil {
		return
	}
	r.outMu.Lock()
	if r.stdout != nil {
		_, _ = io.WriteString(r.stdout, line)
	} else {
		core.WriteRawStdout(line)
	}
	r.outMu.Unlock()
}

func (r *rmRunner) waitBackpressure() {
	if r.stdout != nil {
		return
	}
	core.WaitForRawStdoutBackpressure()
}

func (r *rmRunner) flushStdout() {
	if r.stdout != nil {
		return
	}
	_ = core.FlushRawStdout()
}

func rmSuccessResponse(id *string, command string, data jsonx.Opt[any]) RpcResponse {
	return RpcResponse{
		Id:      id,
		Type:    "response",
		Command: command,
		Success: true,
		Data:    data,
	}
}

func rmSuccessEmpty(id *string, command string) RpcResponse {
	return rmSuccessResponse(id, command, jsonx.Opt[any]{})
}

func rmSuccessData(id *string, command string, data any) RpcResponse {
	return rmSuccessResponse(id, command, jsonx.Some(data))
}

func rmSuccessNull(id *string, command string) RpcResponse {
	return rmSuccessResponse(id, command, jsonx.Null[any]())
}

func rmErrorResponse(id *string, command, msg string) RpcResponse {
	return RpcResponse{
		Id:      id,
		Type:    "response",
		Command: command,
		Success: false,
		Error:   &msg,
	}
}

func (r *rmRunner) createDialog(
	callCtx context.Context,
	opts []*core.ExtensionUIDialogOptions,
	req RpcExtensionUIRequest,
) (RpcExtensionUIResponse, bool) {
	var dlgOpts *core.ExtensionUIDialogOptions
	if len(opts) > 0 {
		dlgOpts = opts[0]
	}

	dlgCtx, cancel := context.WithCancel(r.dialogCtx)
	defer cancel()

	if callCtx != nil {
		if callCtx.Err() != nil {
			return RpcExtensionUIResponse{}, false
		}
		stopCall := context.AfterFunc(callCtx, cancel)
		defer stopCall()
	}
	if dlgOpts != nil && dlgOpts.Ctx != nil {
		if dlgOpts.Ctx.Err() != nil {
			return RpcExtensionUIResponse{}, false
		}
		stopOpt := context.AfterFunc(dlgOpts.Ctx, cancel)
		defer stopOpt()
	}
	if r.dialogCtx.Err() != nil {
		return RpcExtensionUIResponse{}, false
	}

	var timeoutPtr *int64
	if dlgOpts != nil && dlgOpts.Timeout != nil {
		t := *dlgOpts.Timeout
		timeoutPtr = &t
	}

	id := ai.Uuidv7()
	req.Type = "extension_ui_request"
	req.Id = id
	if req.Timeout == nil {
		req.Timeout = timeoutPtr
	}

	ch := make(chan RpcExtensionUIResponse, 1)
	r.pendingMu.Lock()
	r.pendingUI[id] = rmPendingUIRequest{ch: ch}
	r.pendingMu.Unlock()

	defer func() {
		r.pendingMu.Lock()
		delete(r.pendingUI, id)
		r.pendingMu.Unlock()
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeoutPtr != nil && *timeoutPtr > 0 {
		timer = time.NewTimer(time.Duration(*timeoutPtr) * time.Millisecond)
		timerCh = timer.C
		defer timer.Stop()
	}

	r.output(req)

	select {
	case resp := <-ch:
		return resp, true
	case <-dlgCtx.Done():
		return RpcExtensionUIResponse{}, false
	case <-timerCh:
		return RpcExtensionUIResponse{}, false
	}
}

type rmExtensionUIContext struct {
	r *rmRunner
}

func (u *rmExtensionUIContext) Select(ctx context.Context, title string, options []string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	optsCopy := options
	if optsCopy == nil {
		optsCopy = []string{}
	}
	resp, ok := u.r.createDialog(ctx, opts, RpcExtensionUIRequest{
		Method:  "select",
		Title:   &title,
		Options: optsCopy,
	})
	if !ok || (resp.Cancelled != nil && *resp.Cancelled) {
		return nil, nil
	}
	return resp.Value, nil
}

func (u *rmExtensionUIContext) Confirm(ctx context.Context, title, message string, opts ...*core.ExtensionUIDialogOptions) (bool, error) {
	resp, ok := u.r.createDialog(ctx, opts, RpcExtensionUIRequest{
		Method:  "confirm",
		Title:   &title,
		Message: &message,
	})
	if !ok || (resp.Cancelled != nil && *resp.Cancelled) || resp.Confirmed == nil {
		return false, nil
	}
	return *resp.Confirmed, nil
}

func (u *rmExtensionUIContext) Input(ctx context.Context, title string, placeholder *string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	resp, ok := u.r.createDialog(ctx, opts, RpcExtensionUIRequest{
		Method:      "input",
		Title:       &title,
		Placeholder: placeholder,
	})
	if !ok || (resp.Cancelled != nil && *resp.Cancelled) {
		return nil, nil
	}
	return resp.Value, nil
}

func (u *rmExtensionUIContext) Notify(message string, notifyType ...string) {
	var nt *string
	if len(notifyType) > 0 && notifyType[0] != "" {
		v := notifyType[0]
		nt = &v
	}
	u.r.output(RpcExtensionUIRequest{
		Type:       "extension_ui_request",
		Id:         ai.Uuidv7(),
		Method:     "notify",
		Message:    &message,
		NotifyType: nt,
	})
}

func (u *rmExtensionUIContext) OnTerminalInput(_ core.TerminalInputHandler) func() {
	return func() {}
}

func (u *rmExtensionUIContext) SetStatus(key string, text *string) {
	u.r.output(RpcExtensionUIRequest{
		Type:       "extension_ui_request",
		Id:         ai.Uuidv7(),
		Method:     "setStatus",
		StatusKey:  &key,
		StatusText: text,
	})
}

func (u *rmExtensionUIContext) SetWorkingMessage(_ ...string)                       {}
func (u *rmExtensionUIContext) SetWorkingVisible(_ bool)                            {}
func (u *rmExtensionUIContext) SetWorkingIndicator(_ *core.WorkingIndicatorOptions) {}
func (u *rmExtensionUIContext) SetHiddenThinkingLabel(_ ...string)                  {}

func (u *rmExtensionUIContext) SetWidgetLines(key string, content []string, options ...*core.ExtensionWidgetOptions) {
	var placement *string
	if len(options) > 0 && options[0] != nil && options[0].Placement != "" {
		p := string(options[0].Placement)
		placement = &p
	}
	u.r.output(RpcExtensionUIRequest{
		Type:            "extension_ui_request",
		Id:              ai.Uuidv7(),
		Method:          "setWidget",
		WidgetKey:       &key,
		WidgetLines:     content,
		WidgetPlacement: placement,
	})
}

func (u *rmExtensionUIContext) SetWidgetFactory(_ string, _ func(t tui.TUI, th *theme.Theme) tui.Component, _ ...*core.ExtensionWidgetOptions) {
}
func (u *rmExtensionUIContext) SetFooter(_ func(t tui.TUI, th *theme.Theme, footerData core.ReadonlyFooterDataProvider) tui.Component) {
}
func (u *rmExtensionUIContext) SetHeader(_ func(t tui.TUI, th *theme.Theme) tui.Component) {}

func (u *rmExtensionUIContext) SetTitle(title string) {
	u.r.output(RpcExtensionUIRequest{
		Type:   "extension_ui_request",
		Id:     ai.Uuidv7(),
		Method: "setTitle",
		Title:  &title,
	})
}

func (u *rmExtensionUIContext) Custom(
	_ context.Context,
	_ func(t tui.TUI, th *theme.Theme, keybindings *tui.KeybindingsManager, done func(any)) (tui.Component, error),
	_ ...*core.ExtensionCustomOptions,
) (any, error) {
	return nil, nil
}

func (u *rmExtensionUIContext) PasteToEditor(text string) {
	u.SetEditorText(text)
}

func (u *rmExtensionUIContext) SetEditorText(text string) {
	u.r.output(RpcExtensionUIRequest{
		Type:   "extension_ui_request",
		Id:     ai.Uuidv7(),
		Method: "set_editor_text",
		Text:   &text,
	})
}

func (u *rmExtensionUIContext) GetEditorText() string {
	return ""
}

func (u *rmExtensionUIContext) Editor(ctx context.Context, title string, prefill ...string) (*string, error) {
	var prefillPtr *string
	if len(prefill) > 0 {
		p := prefill[0]
		prefillPtr = &p
	}
	resp, ok := u.r.createDialog(ctx, nil, RpcExtensionUIRequest{
		Method:  "editor",
		Title:   &title,
		Prefill: prefillPtr,
	})
	if !ok || (resp.Cancelled != nil && *resp.Cancelled) {
		return nil, nil
	}
	return resp.Value, nil
}

func (u *rmExtensionUIContext) AddAutocompleteProvider(_ core.AutocompleteProviderFactory) {}
func (u *rmExtensionUIContext) SetEditorComponent(_ core.EditorFactory)                    {}
func (u *rmExtensionUIContext) GetEditorComponent() core.EditorFactory                     { return nil }
func (u *rmExtensionUIContext) Theme() *theme.Theme                                        { return theme.CurrentTheme() }
func (u *rmExtensionUIContext) GetAllThemes() []core.ThemeInfo                             { return nil }
func (u *rmExtensionUIContext) GetTheme(_ string) *theme.Theme                             { return nil }

func (u *rmExtensionUIContext) SetThemeByName(_ string) core.SetThemeResult {
	msg := "Theme switching not supported in RPC mode"
	return core.SetThemeResult{Success: false, Error: &msg}
}

func (u *rmExtensionUIContext) SetTheme(_ *theme.Theme) core.SetThemeResult {
	msg := "Theme switching not supported in RPC mode"
	return core.SetThemeResult{Success: false, Error: &msg}
}

func (u *rmExtensionUIContext) GetToolsExpanded() bool  { return false }
func (u *rmExtensionUIContext) SetToolsExpanded(_ bool) {}

func (r *rmRunner) rebindSession(ctx context.Context) error {
	curr := r.runtimeHost.CurrentSession()
	if curr == nil {
		return nil
	}
	r.subMu.Lock()
	r.session = curr
	prevUnsub := r.unsubscribe
	prevUnsubBP := r.unsubscribeBackpress
	r.unsubscribe = nil
	r.unsubscribeBackpress = nil
	r.subMu.Unlock()

	if prevUnsub != nil {
		prevUnsub()
	}
	if prevUnsubBP != nil {
		prevUnsubBP()
	}

	uiCtx := &rmExtensionUIContext{r: r}
	err := curr.BindExtensions(ctx, core.ExtensionBindings{
		UIContext: uiCtx,
		Mode:      core.ExtensionModeRpc,
		CommandContextActions: &core.ExtensionCommandContextActions{
			WaitForIdle: func(cmdCtx context.Context) error {
				return curr.WaitForIdle(cmdCtx)
			},
			NewSession: func(cmdCtx context.Context, options ...*core.ExtensionNewSessionOptions) (core.CancelledResult, error) {
				var rtOpts *core.NewSessionRuntimeOptions
				var withSession func(context.Context, core.ReplacedSessionContext) error
				if len(options) > 0 && options[0] != nil {
					opt := options[0]
					withSession = opt.WithSession
					if opt.ParentSession != nil || opt.Setup != nil {
						var setupFn func(*core.SessionManager) error
						if opt.Setup != nil {
							extSetup := opt.Setup
							setupFn = func(sm *core.SessionManager) error {
								return extSetup(cmdCtx, sm)
							}
						}
						rtOpts = &core.NewSessionRuntimeOptions{
							ParentSession: opt.ParentSession,
							Setup:         setupFn,
						}
					}
				}
				res, err := r.runtimeHost.NewSession(cmdCtx, rtOpts)
				if err != nil {
					return core.CancelledResult{}, err
				}
				if res == nil || res.Cancelled {
					return core.CancelledResult{Cancelled: true}, nil
				}
				if err := r.rebindSession(cmdCtx); err != nil {
					return core.CancelledResult{}, err
				}
				if withSession != nil {
					if next := r.runtimeHost.CurrentSession(); next != nil {
						if err := withSession(cmdCtx, next.CreateReplacedSessionContext()); err != nil {
							return core.CancelledResult{}, err
						}
					}
				}
				return core.CancelledResult{Cancelled: false}, nil
			},
			Fork: func(cmdCtx context.Context, entryId string, forkOpts ...*core.ExtensionForkOptions) (core.CancelledResult, error) {
				var rtOpts *core.ForkSessionOptions
				var withSession func(context.Context, core.ReplacedSessionContext) error
				if len(forkOpts) > 0 && forkOpts[0] != nil {
					opt := forkOpts[0]
					withSession = opt.WithSession
					if opt.Position != "" {
						rtOpts = &core.ForkSessionOptions{Position: opt.Position}
					}
				}
				res, err := r.runtimeHost.Fork(cmdCtx, entryId, rtOpts)
				if err != nil {
					return core.CancelledResult{}, err
				}
				if res == nil || res.Cancelled {
					return core.CancelledResult{Cancelled: true}, nil
				}
				if err := r.rebindSession(cmdCtx); err != nil {
					return core.CancelledResult{}, err
				}
				if withSession != nil {
					if next := r.runtimeHost.CurrentSession(); next != nil {
						if err := withSession(cmdCtx, next.CreateReplacedSessionContext()); err != nil {
							return core.CancelledResult{}, err
						}
					}
				}
				return core.CancelledResult{Cancelled: false}, nil
			},
			NavigateTree: func(cmdCtx context.Context, targetId string, navOpts ...*core.ExtensionNavigateTreeOptions) (core.CancelledResult, error) {
				var coreNavOpts *core.NavigateTreeOptions
				if len(navOpts) > 0 && navOpts[0] != nil {
					opt := navOpts[0]
					coreNavOpts = &core.NavigateTreeOptions{
						Summarize:           opt.Summarize,
						CustomInstructions:  opt.CustomInstructions,
						ReplaceInstructions: opt.ReplaceInstructions,
						Label:               opt.Label,
					}
				}
				res, err := curr.NavigateTree(cmdCtx, targetId, coreNavOpts)
				if err != nil {
					return core.CancelledResult{}, err
				}
				cancelled := res != nil && res.Cancelled
				return core.CancelledResult{Cancelled: cancelled}, nil
			},
			SwitchSession: func(cmdCtx context.Context, sessionPath string, switchOpts ...*core.ExtensionSwitchSessionOptions) (core.CancelledResult, error) {
				var withSession func(context.Context, core.ReplacedSessionContext) error
				if len(switchOpts) > 0 && switchOpts[0] != nil {
					withSession = switchOpts[0].WithSession
				}
				res, err := r.runtimeHost.SwitchSession(cmdCtx, sessionPath)
				if err != nil {
					return core.CancelledResult{}, err
				}
				if res == nil || res.Cancelled {
					return core.CancelledResult{Cancelled: true}, nil
				}
				if err := r.rebindSession(cmdCtx); err != nil {
					return core.CancelledResult{}, err
				}
				if withSession != nil {
					if next := r.runtimeHost.CurrentSession(); next != nil {
						if err := withSession(cmdCtx, next.CreateReplacedSessionContext()); err != nil {
							return core.CancelledResult{}, err
						}
					}
				}
				return core.CancelledResult{Cancelled: false}, nil
			},
			Reload: func(cmdCtx context.Context) error {
				return curr.Reload(cmdCtx)
			},
		},
		ShutdownHandler: func() {
			r.lifecycleMu.Lock()
			r.shutdownRequested = true
			r.lifecycleMu.Unlock()
			// An idle session has no command response or agent_settled left to check the flag.
			// Check asynchronously on the next tick so the requesting handler finishes first.
			go r.checkShutdownRequested()
		},
		OnError: func(extErr core.ExtensionError) {
			r.output(RpcExtensionErrorEvent{
				Type:          "extension_error",
				ExtensionPath: extErr.ExtensionPath,
				Event:         extErr.Event,
				Error:         extErr.Error,
			})
		},
	})
	if err != nil {
		return err
	}

	unsub := curr.Subscribe(func(event core.AgentSessionEvent) {
		jsonEv, convErr := modes.ToJsonEvent(event)
		if convErr == nil && jsonEv != nil {
			r.output(jsonEv)
		}
		if event != nil && event.AgentSessionEventType() == "agent_settled" {
			go r.checkShutdownRequested()
		}
	})
	var unsubBP func()
	if curr.Agent != nil {
		unsubBP = curr.Agent.Subscribe(func(agent.AgentEvent) {
			r.waitBackpressure()
		})
	}

	r.subMu.Lock()
	if r.session != curr {
		r.subMu.Unlock()
		unsub()
		if unsubBP != nil {
			unsubBP()
		}
		return nil
	}
	r.unsubscribe = unsub
	r.unsubscribeBackpress = unsubBP
	r.subMu.Unlock()
	return nil
}

func (r *rmRunner) checkShutdownRequested() {
	r.lifecycleMu.Lock()
	req := r.shutdownRequested
	shuttingDown := r.shuttingDown
	ready := r.inputReady
	active := r.activeCommands
	r.lifecycleMu.Unlock()

	if !req || shuttingDown || !ready || active > 0 {
		return
	}
	sess := r.currentSession()
	if sess != nil && !sess.IsIdle() {
		return
	}
	r.startShutdown(0, nil)
}

func (r *rmRunner) startShutdown(exitCode int, sig os.Signal) {
	r.lifecycleMu.Lock()
	if r.shuttingDown {
		r.lifecycleMu.Unlock()
		return
	}
	r.shuttingDown = true
	r.lifecycleMu.Unlock()

	go r.finishShutdown(exitCode, sig)
}

func (r *rmRunner) finishShutdown(exitCode int, sig os.Signal) {
	r.subMu.Lock()
	unsub := r.unsubscribe
	unsubBP := r.unsubscribeBackpress
	r.unsubscribe = nil
	r.unsubscribeBackpress = nil
	r.subMu.Unlock()

	if unsub != nil {
		unsub()
	}
	if unsubBP != nil {
		unsubBP()
	}

	r.dialogStop()
	_ = r.runtimeHost.Dispose(context.Background())

	r.detachInput()
	r.stopSignalWatcher()
	if sig != syscall.SIGTERM {
		r.flushStdout()
	}
	if r.tookStdout {
		core.RestoreStdout()
		r.tookStdout = false
	}

	r.completeAndExit(exitCode, sig != nil)
}

func (r *rmRunner) forceExit(exitCode int) {
	r.stopSignalWatcher()
	if r.tookStdout {
		core.RestoreStdout()
		r.tookStdout = false
	}
	r.completeAndExit(exitCode, true)
}

func (r *rmRunner) completeAndExit(exitCode int, fromSignal bool) {
	r.exitOnce.Do(func() {
		if r.onExit != nil {
			r.onExit(exitCode)
		} else if fromSignal || exitCode != 0 {
			utils.ConfigExit(exitCode)
		}
	})
	r.doneOnce.Do(func() {
		close(r.doneCh)
	})
}

func (r *rmRunner) onStdinLine(line string) {
	val, parseErr := jsonx.Parse([]byte(line))
	if parseErr != nil {
		r.lifecycleMu.Lock()
		r.activeCommands++
		r.lifecycleMu.Unlock()
		go func() {
			r.output(rmErrorResponse(nil, "parse", fmt.Sprintf("Failed to parse command: %v", parseErr)))
			r.waitBackpressure()
			r.lifecycleMu.Lock()
			r.activeCommands--
			r.lifecycleMu.Unlock()
			r.checkShutdownRequested()
		}()
		return
	}

	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		r.lifecycleMu.Lock()
		r.activeCommands++
		r.lifecycleMu.Unlock()
		go func() {
			r.output(rmErrorResponse(nil, "parse", "Failed to parse command: expected JSON object"))
			r.waitBackpressure()
			r.lifecycleMu.Lock()
			r.activeCommands--
			r.lifecycleMu.Unlock()
			r.checkShutdownRequested()
		}()
		return
	}

	if typ, _ := obj.GetString("type"); typ == "extension_ui_response" {
		var resp RpcExtensionUIResponse
		if err := jsonx.Decode(obj, &resp); err == nil {
			r.pendingMu.Lock()
			pending, exists := r.pendingUI[resp.Id]
			if exists {
				delete(r.pendingUI, resp.Id)
			}
			r.pendingMu.Unlock()
			if exists {
				select {
				case pending.ch <- resp:
				default:
				}
			}
		}
		return
	}

	cmd, cmdErr := UnmarshalRpcCommand(obj)
	if cmdErr != nil {
		var idPtr *string
		if idStr, hasId := obj.GetString("id"); hasId {
			idPtr = &idStr
		}
		typStr, _ := obj.GetString("type")
		if typStr == "" {
			typStr = "parse"
		}
		r.lifecycleMu.Lock()
		r.activeCommands++
		r.lifecycleMu.Unlock()
		go func() {
			r.output(rmErrorResponse(idPtr, typStr, fmt.Sprintf("Failed to parse command: %v", cmdErr)))
			r.waitBackpressure()
			r.lifecycleMu.Lock()
			r.activeCommands--
			r.lifecycleMu.Unlock()
			r.checkShutdownRequested()
		}()
		return
	}

	r.lifecycleMu.Lock()
	r.activeCommands++
	r.lifecycleMu.Unlock()

	go r.runCommand(cmd)
}

func (r *rmRunner) runCommand(command RpcCommand) {
	sess := r.currentSession()
	if sess == nil {
		r.output(rmErrorResponse(command.CommandId(), string(command.CommandType()), "Session is not initialized"))
		r.waitBackpressure()
		r.finishCommand()
		return
	}

	if command.CommandType() == RpcCmdPrompt {
		cmd := command.(*RpcPromptCommand)
		id := cmd.Id
		preflightSucceeded := false
		promptOpts := &core.PromptOptions{
			Images: cmd.Images,
			Source: core.InputSourceRpc,
			PreflightResult: func(disposition core.PromptDisposition) {
				preflightSucceeded = true
				r.output(rmSuccessData(id, string(RpcCmdPrompt), RpcPromptData{Disposition: disposition}))
			},
		}
		if cmd.StreamingBehavior != nil {
			promptOpts.StreamingBehavior = *cmd.StreamingBehavior
		}
		if err := sess.Prompt(r.baseCtx, cmd.Message, promptOpts); err != nil && !preflightSucceeded {
			r.output(rmErrorResponse(id, string(RpcCmdPrompt), err.Error()))
		}
		r.finishCommand()
		return
	}

	resp := r.handleCommand(sess, command)
	r.output(resp)
	r.waitBackpressure()
	r.finishCommand()
}

func (r *rmRunner) finishCommand() {
	r.lifecycleMu.Lock()
	r.activeCommands--
	r.lifecycleMu.Unlock()
	r.checkShutdownRequested()
}

func (r *rmRunner) handleCommand(sess *core.AgentSession, command RpcCommand) RpcResponse {
	id := command.CommandId()
	cmdType := string(command.CommandType())

	switch cmd := command.(type) {
	case *RpcSteerCommand:
		disp, err := sess.Steer(r.baseCtx, cmd.Message, cmd.Images, &core.QueueInputOptions{Source: core.InputSourceRpc})
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, RpcQueuedInputData{Disposition: disp})

	case *RpcFollowUpCommand:
		disp, err := sess.FollowUp(r.baseCtx, cmd.Message, cmd.Images, &core.QueueInputOptions{Source: core.InputSourceRpc})
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, RpcQueuedInputData{Disposition: disp})

	case *RpcAbortCommand:
		if err := sess.Abort(r.baseCtx); err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessEmpty(id, cmdType)

	case *RpcClearQueueCommand:
		cleared := sess.ClearQueue()
		if cleared.Steering == nil {
			cleared.Steering = []string{}
		}
		if cleared.FollowUp == nil {
			cleared.FollowUp = []string{}
		}
		return rmSuccessData(id, cmdType, cleared)

	case *RpcNewSessionCommand:
		var rtOpts *core.NewSessionRuntimeOptions
		if cmd.ParentSession != nil && *cmd.ParentSession != "" {
			rtOpts = &core.NewSessionRuntimeOptions{ParentSession: cmd.ParentSession}
		}
		res, err := r.runtimeHost.NewSession(r.baseCtx, rtOpts)
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		cancelled := res != nil && res.Cancelled
		if !cancelled {
			if err := r.rebindSession(r.baseCtx); err != nil {
				return rmErrorResponse(id, cmdType, err.Error())
			}
		}
		return rmSuccessData(id, cmdType, RpcCancelledData{Cancelled: cancelled})

	case *RpcGetStateCommand:
		state := RpcSessionState{
			Model:                 sess.Model(),
			ThinkingLevel:         sess.ThinkingLevel(),
			IsStreaming:           sess.IsStreaming(),
			IsCompacting:          sess.IsCompacting(),
			SteeringMode:          sess.SteeringMode(),
			FollowUpMode:          sess.FollowUpMode(),
			SessionFile:           sess.SessionFile(),
			SessionId:             sess.SessionId(),
			SessionName:           sess.SessionName(),
			AutoCompactionEnabled: sess.AutoCompactionEnabled(),
			MessageCount:          len(sess.Messages()),
			PendingMessageCount:   sess.PendingMessageCount(),
		}
		return rmSuccessData(id, cmdType, state)

	case *RpcSetModelCommand:
		var models []*ai.Model
		if mr := sess.ModelRuntime(); mr != nil {
			models = mr.GetAvailableSnapshot()
		}
		var target *ai.Model
		for _, m := range models {
			if m != nil && string(m.Provider) == cmd.Provider && m.Id == cmd.ModelId {
				target = m
				break
			}
		}
		if target == nil {
			return rmErrorResponse(id, cmdType, fmt.Sprintf("Model not found: %s/%s", cmd.Provider, cmd.ModelId))
		}
		if err := sess.SetModel(r.baseCtx, target); err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, target)

	case *RpcCycleModelCommand:
		res, err := sess.CycleModel(r.baseCtx, "forward")
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		if res == nil {
			return rmSuccessNull(id, cmdType)
		}
		return rmSuccessData(id, cmdType, res)

	case *RpcGetAvailableModelsCommand:
		var models []*ai.Model
		if mr := sess.ModelRuntime(); mr != nil {
			models = mr.GetAvailableSnapshot()
		}
		if models == nil {
			models = []*ai.Model{}
		}
		return rmSuccessData(id, cmdType, RpcAvailableModelsData{Models: models})

	case *RpcSetThinkingLevelCommand:
		sess.SetThinkingLevel(cmd.Level)
		return rmSuccessEmpty(id, cmdType)

	case *RpcCycleThinkingLevelCommand:
		level := sess.CycleThinkingLevel()
		if level == nil {
			return rmSuccessNull(id, cmdType)
		}
		return rmSuccessData(id, cmdType, RpcCycleThinkingLevelData{Level: *level})

	case *RpcGetAvailableThinkingLevelsCommand:
		levels := sess.GetAvailableThinkingLevels()
		if levels == nil {
			levels = []agent.ThinkingLevel{}
		}
		return rmSuccessData(id, cmdType, RpcAvailableThinkingLevelsData{Levels: levels})

	case *RpcSetSteeringModeCommand:
		sess.SetSteeringMode(cmd.Mode)
		return rmSuccessEmpty(id, cmdType)

	case *RpcSetFollowUpModeCommand:
		sess.SetFollowUpMode(cmd.Mode)
		return rmSuccessEmpty(id, cmdType)

	case *RpcCompactCommand:
		var res *core.CompactionResult
		var err error
		if cmd.CustomInstructions != nil {
			res, err = sess.Compact(r.baseCtx, *cmd.CustomInstructions)
		} else {
			res, err = sess.Compact(r.baseCtx)
		}
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, res)

	case *RpcSetAutoCompactionCommand:
		sess.SetAutoCompactionEnabled(cmd.Enabled)
		return rmSuccessEmpty(id, cmdType)

	case *RpcSetAutoRetryCommand:
		sess.SetAutoRetryEnabled(cmd.Enabled)
		return rmSuccessEmpty(id, cmdType)

	case *RpcAbortRetryCommand:
		sess.AbortRetry()
		return rmSuccessEmpty(id, cmdType)

	case *RpcBashCommand:
		exclude := false
		if cmd.ExcludeFromContext != nil {
			exclude = *cmd.ExcludeFromContext
		}
		cwd := ""
		if sess.SessionManager != nil {
			cwd = sess.SessionManager.GetCwd()
		}
		var eventResult *core.UserBashEventResult
		if runner := sess.ExtensionRunner(); runner != nil {
			eventResult = runner.EmitUserBash(r.baseCtx, core.UserBashEvent{
				Type:               "user_bash",
				Command:            cmd.Command,
				ExcludeFromContext: exclude,
				Cwd:                cwd,
			})
		}
		if eventResult != nil && eventResult.Result != nil {
			sess.RecordBashResult(cmd.Command, *eventResult.Result, &core.RecordBashResultOptions{
				ExcludeFromContext: exclude,
			})
			return rmSuccessData(id, cmdType, *eventResult.Result)
		}
		var ops core.BashOperations
		if eventResult != nil {
			ops = eventResult.Operations
		}
		res, err := sess.ExecuteBash(r.baseCtx, cmd.Command, nil, &core.ExecuteBashOptions{
			ExcludeFromContext: exclude,
			Id:                 id,
			Operations:         ops,
		})
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, res)

	case *RpcAbortBashCommand:
		sess.AbortBash()
		return rmSuccessEmpty(id, cmdType)

	case *RpcGetSessionStatsCommand:
		return rmSuccessData(id, cmdType, sess.GetSessionStats())

	case *RpcExportHtmlCommand:
		outPath := ""
		if cmd.OutputPath != nil {
			outPath = *cmd.OutputPath
		}
		exportedPath, err := sess.ExportToHtml(outPath)
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		return rmSuccessData(id, cmdType, RpcExportHtmlData{Path: exportedPath})

	case *RpcSwitchSessionCommand:
		res, err := r.runtimeHost.SwitchSession(r.baseCtx, cmd.SessionPath)
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		cancelled := res != nil && res.Cancelled
		if !cancelled {
			if err := r.rebindSession(r.baseCtx); err != nil {
				return rmErrorResponse(id, cmdType, err.Error())
			}
		}
		return rmSuccessData(id, cmdType, RpcCancelledData{Cancelled: cancelled})

	case *RpcForkCommand:
		res, err := r.runtimeHost.Fork(r.baseCtx, cmd.EntryId)
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		cancelled := res != nil && res.Cancelled
		if !cancelled {
			if err := r.rebindSession(r.baseCtx); err != nil {
				return rmErrorResponse(id, cmdType, err.Error())
			}
		}
		text := ""
		if res != nil && res.SelectedText != nil {
			text = *res.SelectedText
		}
		return rmSuccessData(id, cmdType, RpcForkData{Text: text, Cancelled: cancelled})

	case *RpcCloneCommand:
		var leafId *string
		if sess.SessionManager != nil {
			leafId = sess.SessionManager.GetLeafId()
		}
		if leafId == nil || *leafId == "" {
			return rmErrorResponse(id, cmdType, "Cannot clone session: no current entry selected")
		}
		res, err := r.runtimeHost.Fork(r.baseCtx, *leafId, &core.ForkSessionOptions{Position: "at"})
		if err != nil {
			return rmErrorResponse(id, cmdType, err.Error())
		}
		cancelled := res != nil && res.Cancelled
		if !cancelled {
			if err := r.rebindSession(r.baseCtx); err != nil {
				return rmErrorResponse(id, cmdType, err.Error())
			}
		}
		return rmSuccessData(id, cmdType, RpcCancelledData{Cancelled: cancelled})

	case *RpcGetForkMessagesCommand:
		msgs := sess.GetUserMessagesForForking()
		if msgs == nil {
			msgs = []core.ForkableUserMessage{}
		}
		return rmSuccessData(id, cmdType, RpcForkMessagesData{Messages: msgs})

	case *RpcGetEntriesCommand:
		sm := sess.SessionManager
		var entries []core.SessionEntry
		var leafId *string
		if sm != nil {
			entries = sm.GetEntries()
			leafId = sm.GetLeafId()
		}
		if cmd.Since != nil {
			idx := -1
			for i, entry := range entries {
				if entry != nil && entry.GetId() == *cmd.Since {
					idx = i
					break
				}
			}
			if idx == -1 {
				return rmErrorResponse(id, cmdType, fmt.Sprintf("Entry not found: %s", *cmd.Since))
			}
			entries = entries[idx+1:]
		}
		if entries == nil {
			entries = []core.SessionEntry{}
		}
		return rmSuccessData(id, cmdType, RpcEntriesData{Entries: entries, LeafId: leafId})

	case *RpcGetTreeCommand:
		sm := sess.SessionManager
		var tree []core.SessionTreeNode
		var leafId *string
		if sm != nil {
			tree = sm.GetTree()
			leafId = sm.GetLeafId()
		}
		if tree == nil {
			tree = []core.SessionTreeNode{}
		}
		return rmSuccessData(id, cmdType, RpcTreeData{Tree: tree, LeafId: leafId})

	case *RpcGetLastAssistantTextCommand:
		return rmSuccessData(id, cmdType, RpcLastAssistantTextData{Text: sess.GetLastAssistantText()})

	case *RpcSetSessionNameCommand:
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			return rmErrorResponse(id, cmdType, "Session name cannot be empty")
		}
		sess.SetSessionName(name)
		return rmSuccessEmpty(id, cmdType)

	case *RpcGetMessagesCommand:
		msgs := sess.Messages()
		if msgs == nil {
			msgs = agent.AgentMessageList{}
		}
		return rmSuccessData(id, cmdType, RpcMessagesData{Messages: msgs})

	case *RpcGetCommandsCommand:
		commands := make([]RpcSlashCommand, 0)
		if runner := sess.ExtensionRunner(); runner != nil {
			for _, extCmd := range runner.GetRegisteredCommands() {
				commands = append(commands, RpcSlashCommand{
					Name:        extCmd.InvocationName,
					Description: extCmd.Description,
					Source:      RpcSlashCommandSourceExtension,
					SourceInfo:  extCmd.SourceInfo,
				})
			}
		}
		for _, tmpl := range sess.PromptTemplates() {
			desc := tmpl.Description
			commands = append(commands, RpcSlashCommand{
				Name:        tmpl.Name,
				Description: &desc,
				Source:      RpcSlashCommandSourcePrompt,
				SourceInfo:  tmpl.SourceInfo,
			})
		}
		if rl := sess.ResourceLoader(); rl != nil {
			for _, skill := range rl.GetSkills().Skills {
				desc := skill.Description
				commands = append(commands, RpcSlashCommand{
					Name:        "skill:" + skill.Name,
					Description: &desc,
					Source:      RpcSlashCommandSourceSkill,
					SourceInfo:  skill.SourceInfo,
				})
			}
		}
		return rmSuccessData(id, cmdType, RpcCommandsData{Commands: commands})

	default:
		return rmErrorResponse(id, cmdType, fmt.Sprintf("Unknown command: %s", cmdType))
	}
}
