// Ported from packages/coding-agent/src/modes/print-mode.ts (pi v1.5.2).

package modes

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// PrintModeOutput selects the print-mode output format.
type PrintModeOutput string

const (
	// PrintModeOutputText prints the final assistant text response.
	PrintModeOutputText PrintModeOutput = "text"
	// PrintModeOutputJson streams delta-stripped session JSON events on stdout.
	PrintModeOutputJson PrintModeOutput = "json"
)

// PrintModeOptions configures RunPrintMode.
type PrintModeOptions struct {
	// Mode selects "text" (final response only) or "json" (all events).
	Mode PrintModeOutput
	// Messages holds additional prompts sent after InitialMessage.
	Messages []string
	// InitialMessage is the first message to send (may contain expanded @file content).
	InitialMessage *string
	// InitialImages holds images attached to InitialMessage.
	InitialImages []*ai.ImageContent
	// Stdout overrides raw stdout output for tests; when nil, core.WriteRawStdout is used.
	Stdout io.Writer
	// Stderr overrides error output for tests; when nil, os.Stderr is used.
	Stderr io.Writer
}

type pmSession interface {
	SessionHeader() *core.SessionHeader
	BindExtensions(ctx context.Context, bindings core.ExtensionBindings) error
	Subscribe(listener core.AgentSessionEventListener) func()
	SubscribeAgent(listener func(event agent.AgentEvent)) func()
	Prompt(ctx context.Context, text string, options ...*core.PromptOptions) error
	Messages() agent.AgentMessageList
	WaitForIdle(ctx context.Context) error
	NavigateTree(ctx context.Context, targetId string, options ...*core.NavigateTreeOptions) (*core.NavigateTreeResult, error)
	Reload(ctx context.Context) error
	CreateReplacedSessionContext() core.ReplacedSessionContext
}

type pmRuntimeHost interface {
	CurrentSession() pmSession
	NewSession(ctx context.Context, options ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error)
	Fork(ctx context.Context, entryId string, options ...*core.ForkSessionOptions) (*core.ForkSessionResult, error)
	SwitchSession(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error)
	Dispose(ctx context.Context) error
}

type pmHooks struct {
	signalCh <-chan os.Signal
	stopSig  func()
	exitFn   func(code int)
}

type pmCoreSessionAdapter struct {
	session *core.AgentSession
}

func (a *pmCoreSessionAdapter) SessionHeader() *core.SessionHeader {
	if a.session == nil || a.session.SessionManager == nil {
		return nil
	}
	return a.session.SessionManager.GetHeader()
}

func (a *pmCoreSessionAdapter) BindExtensions(ctx context.Context, bindings core.ExtensionBindings) error {
	return a.session.BindExtensions(ctx, bindings)
}

func (a *pmCoreSessionAdapter) Subscribe(listener core.AgentSessionEventListener) func() {
	return a.session.Subscribe(listener)
}

func (a *pmCoreSessionAdapter) SubscribeAgent(listener func(event agent.AgentEvent)) func() {
	if a.session.Agent == nil {
		return func() {}
	}
	return a.session.Agent.Subscribe(listener)
}

func (a *pmCoreSessionAdapter) Prompt(ctx context.Context, text string, options ...*core.PromptOptions) error {
	return a.session.Prompt(ctx, text, options...)
}

func (a *pmCoreSessionAdapter) Messages() agent.AgentMessageList {
	return a.session.Messages()
}

func (a *pmCoreSessionAdapter) WaitForIdle(ctx context.Context) error {
	return a.session.WaitForIdle(ctx)
}

func (a *pmCoreSessionAdapter) NavigateTree(ctx context.Context, targetId string, options ...*core.NavigateTreeOptions) (*core.NavigateTreeResult, error) {
	return a.session.NavigateTree(ctx, targetId, options...)
}

func (a *pmCoreSessionAdapter) Reload(ctx context.Context) error {
	return a.session.Reload(ctx)
}

func (a *pmCoreSessionAdapter) CreateReplacedSessionContext() core.ReplacedSessionContext {
	return a.session.CreateReplacedSessionContext()
}

type pmCoreRuntimeAdapter struct {
	runtime *core.AgentSessionRuntime
}

func (r *pmCoreRuntimeAdapter) CurrentSession() pmSession {
	if r.runtime == nil || r.runtime.Session == nil {
		return nil
	}
	return &pmCoreSessionAdapter{session: r.runtime.Session}
}

func (r *pmCoreRuntimeAdapter) NewSession(ctx context.Context, options ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error) {
	return r.runtime.NewSession(ctx, options...)
}

func (r *pmCoreRuntimeAdapter) Fork(ctx context.Context, entryId string, options ...*core.ForkSessionOptions) (*core.ForkSessionResult, error) {
	return r.runtime.Fork(ctx, entryId, options...)
}

func (r *pmCoreRuntimeAdapter) SwitchSession(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error) {
	return r.runtime.SwitchSession(ctx, sessionPath)
}

func (r *pmCoreRuntimeAdapter) Dispose(ctx context.Context) error {
	return r.runtime.Dispose(ctx)
}

// RunPrintMode runs pi in single-shot print mode ("text" or "json"),
// sending prompts to runtimeHost, emitting stdout output, and disposing runtimeHost.
func RunPrintMode(ctx context.Context, runtimeHost *core.AgentSessionRuntime, opts *PrintModeOptions) int {
	return pmRunPrintMode(ctx, &pmCoreRuntimeAdapter{runtime: runtimeHost}, opts, nil)
}

func pmSignalExitCode(sig os.Signal) int {
	if sig == syscall.SIGHUP {
		return 129
	}
	return 143
}

func pmDefaultSignalChannel() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 4)
	if runtime.GOOS != "windows" {
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)
	} else {
		signal.Notify(ch, syscall.SIGTERM)
	}
	return ch, func() { signal.Stop(ch) }
}

func pmRunPrintMode(ctx context.Context, runtimeHost pmRuntimeHost, opts *PrintModeOptions, hooks *pmHooks) (exitCode int) {
	if ctx == nil {
		ctx = context.Background()
	}
	var options PrintModeOptions
	if opts != nil {
		options = *opts
	}
	mode := options.Mode
	if mode == "" {
		mode = PrintModeOutputText
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	writeStdout := func(text string) {
		if options.Stdout != nil {
			_, _ = io.WriteString(options.Stdout, text)
			return
		}
		core.WriteRawStdout(text)
	}
	waitBackpressure := func() {
		if options.Stdout != nil {
			return
		}
		core.WaitForRawStdoutBackpressure()
	}
	flushStdout := func() {
		if options.Stdout != nil {
			return
		}
		_ = core.FlushRawStdout()
	}

	if runtimeHost == nil || runtimeHost.CurrentSession() == nil {
		_, _ = fmt.Fprintln(stderr, "Print mode error: session runtime is not initialized")
		return 1
	}

	var (
		subMu                 sync.Mutex
		session               = runtimeHost.CurrentSession()
		unsubscribe           func()
		unsubscribeBackpress  func()
		disposeOnce           sync.Once
		disposeDone           = make(chan struct{})
		signalMu              sync.Mutex
		signalCount           int
		signalWatcherStopChan = make(chan struct{})
		signalWatcherDone     = make(chan struct{})
	)

	disposeRuntime := func() {
		disposeOnce.Do(func() {
			subMu.Lock()
			unsub := unsubscribe
			unsubBP := unsubscribeBackpress
			unsubscribe = nil
			unsubscribeBackpress = nil
			subMu.Unlock()

			if unsub != nil {
				unsub()
			}
			if unsubBP != nil {
				unsubBP()
			}
			_ = runtimeHost.Dispose(ctx)
			close(disposeDone)
		})
		<-disposeDone
	}

	var sigCh <-chan os.Signal
	var stopSig func()
	exitFn := utils.ConfigExit
	if hooks != nil {
		sigCh = hooks.signalCh
		stopSig = hooks.stopSig
		if hooks.exitFn != nil {
			exitFn = hooks.exitFn
		}
	}
	if sigCh == nil {
		sigCh, stopSig = pmDefaultSignalChannel()
	} else if stopSig == nil {
		stopSig = func() {}
	}

	go func() {
		defer close(signalWatcherDone)
		for {
			select {
			case <-signalWatcherStopChan:
				return
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				code := pmSignalExitCode(sig)
				utils.KillTrackedDetachedChildren()
				signalMu.Lock()
				signalCount++
				count := signalCount
				signalMu.Unlock()
				if count > 1 {
					exitFn(code)
					continue
				}
				go func(exitCode int) {
					disposeRuntime()
					exitFn(exitCode)
				}(code)
			}
		}
	}()

	defer func() {
		// Keep signal handlers active until disposal is done: without one, a signal
		// would terminate pi mid-dispose before extension cleanup finishes.
		disposeRuntime()
		stopSig()
		close(signalWatcherStopChan)
		<-signalWatcherDone
		flushStdout()
	}()

	var rebindSession func(context.Context) error
	rebindSession = func(c context.Context) error {
		curr := runtimeHost.CurrentSession()
		if curr == nil {
			return fmt.Errorf("current session is nil")
		}
		subMu.Lock()
		session = curr
		subMu.Unlock()

		extMode := core.ExtensionModePrint
		if mode == PrintModeOutputJson {
			extMode = core.ExtensionModeJson
		}

		err := curr.BindExtensions(c, core.ExtensionBindings{
			Mode: extMode,
			CommandContextActions: &core.ExtensionCommandContextActions{
				WaitForIdle: func(waitCtx context.Context) error {
					return curr.WaitForIdle(waitCtx)
				},
				NewSession: func(cmdCtx context.Context, newOpts ...*core.ExtensionNewSessionOptions) (core.CancelledResult, error) {
					var rtOpts *core.NewSessionRuntimeOptions
					var withSession func(context.Context, core.ReplacedSessionContext) error
					if len(newOpts) > 0 && newOpts[0] != nil {
						opt := newOpts[0]
						withSession = opt.WithSession
						rtOpts = &core.NewSessionRuntimeOptions{
							ParentSession: opt.ParentSession,
						}
						if opt.Setup != nil {
							rtOpts.Setup = func(sm *core.SessionManager) error {
								return opt.Setup(cmdCtx, sm)
							}
						}
					}
					res, err := runtimeHost.NewSession(cmdCtx, rtOpts)
					if err != nil {
						return core.CancelledResult{}, err
					}
					if res == nil || res.Cancelled {
						return core.CancelledResult{Cancelled: true}, nil
					}
					if err := rebindSession(cmdCtx); err != nil {
						return core.CancelledResult{}, err
					}
					if withSession != nil {
						if next := runtimeHost.CurrentSession(); next != nil {
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
					res, err := runtimeHost.Fork(cmdCtx, entryId, rtOpts)
					if err != nil {
						return core.CancelledResult{}, err
					}
					if res == nil || res.Cancelled {
						return core.CancelledResult{Cancelled: true}, nil
					}
					if err := rebindSession(cmdCtx); err != nil {
						return core.CancelledResult{}, err
					}
					if withSession != nil {
						if next := runtimeHost.CurrentSession(); next != nil {
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
					res, err := runtimeHost.SwitchSession(cmdCtx, sessionPath)
					if err != nil {
						return core.CancelledResult{}, err
					}
					if res == nil || res.Cancelled {
						return core.CancelledResult{Cancelled: true}, nil
					}
					if err := rebindSession(cmdCtx); err != nil {
						return core.CancelledResult{}, err
					}
					if withSession != nil {
						if next := runtimeHost.CurrentSession(); next != nil {
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
			OnError: func(extErr core.ExtensionError) {
				_, _ = fmt.Fprintf(stderr, "Extension error (%s): %s\n", extErr.ExtensionPath, extErr.Error)
			},
		})
		if err != nil {
			return err
		}

		subMu.Lock()
		prevUnsub := unsubscribe
		prevUnsubBP := unsubscribeBackpress
		unsubscribe = nil
		unsubscribeBackpress = nil
		subMu.Unlock()

		if prevUnsub != nil {
			prevUnsub()
		}
		if prevUnsubBP != nil {
			prevUnsubBP()
		}

		nextUnsub := curr.Subscribe(func(event core.AgentSessionEvent) {
			if mode == PrintModeOutputJson {
				wireEvent, err := ToJsonEvent(event)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "JSON event serialization error: %v\n", err)
					return
				}
				line, err := jsonx.Marshal(wireEvent)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "JSON event marshal error: %v\n", err)
					return
				}
				writeStdout(string(line) + "\n")
			}
		})

		var nextUnsubBP func()
		if mode == PrintModeOutputJson {
			nextUnsubBP = curr.SubscribeAgent(func(agent.AgentEvent) {
				waitBackpressure()
			})
		}

		subMu.Lock()
		unsubscribe = nextUnsub
		unsubscribeBackpress = nextUnsubBP
		subMu.Unlock()
		return nil
	}

	if mode == PrintModeOutputJson {
		if header := session.SessionHeader(); header != nil {
			if line, err := jsonx.Marshal(header); err == nil {
				writeStdout(string(line) + "\n")
			}
		}
	}

	if err := rebindSession(ctx); err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 1
	}

	if options.InitialMessage != nil && *options.InitialMessage != "" {
		var promptOpts []*core.PromptOptions
		if len(options.InitialImages) > 0 {
			promptOpts = append(promptOpts, &core.PromptOptions{Images: options.InitialImages})
		}
		subMu.Lock()
		curr := session
		subMu.Unlock()
		if err := curr.Prompt(ctx, *options.InitialMessage, promptOpts...); err != nil {
			_, _ = fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}

	for _, message := range options.Messages {
		subMu.Lock()
		curr := session
		subMu.Unlock()
		if err := curr.Prompt(ctx, message); err != nil {
			_, _ = fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}

	if mode == PrintModeOutputText {
		subMu.Lock()
		curr := session
		subMu.Unlock()
		messages := curr.Messages()
		if len(messages) > 0 {
			lastMessage := messages[len(messages)-1]
			if assistantMsg, ok := lastMessage.(*ai.AssistantMessage); ok && assistantMsg != nil {
				if assistantMsg.StopReason == ai.StopReasonError || assistantMsg.StopReason == ai.StopReasonAborted {
					msg := fmt.Sprintf("Request %s", assistantMsg.StopReason)
					if assistantMsg.ErrorMessage != nil && *assistantMsg.ErrorMessage != "" {
						msg = *assistantMsg.ErrorMessage
					}
					_, _ = fmt.Fprintln(stderr, msg)
					exitCode = 1
				} else {
					for _, content := range assistantMsg.Content {
						if textContent, ok := content.(*ai.TextContent); ok && textContent != nil {
							writeStdout(textContent.Text + "\n")
						}
					}
					if assistantMsg.StopReason == ai.StopReasonLength {
						_, _ = fmt.Fprintln(stderr, "Response was truncated: the model reached its output token limit.")
						exitCode = 1
					}
				}
			}
		}
	}

	return exitCode
}
