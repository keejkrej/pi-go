// Ported from packages/coding-agent/test/print-mode.test.ts (pi v1.5.2).

package modes

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

type tpmPromptCall struct {
	text    string
	options []*core.PromptOptions
}

type tpmMockSession struct {
	header          *core.SessionHeader
	messages        agent.AgentMessageList
	bindCalls       []core.ExtensionBindings
	promptCalls     []tpmPromptCall
	subscribeCalls  int
	agentSubCalls   int
	reloadCalls     int
	listeners       []core.AgentSessionEventListener
	agentListeners  []func(agent.AgentEvent)
	promptErr       error
	onBind          func(core.ExtensionBindings)
	mu              sync.Mutex
	boundMode       core.ExtensionMode
	hasShutdownHook bool
	shutdownCount   int
}

func (s *tpmMockSession) SessionHeader() *core.SessionHeader {
	return s.header
}

func (s *tpmMockSession) BindExtensions(_ context.Context, bindings core.ExtensionBindings) error {
	s.mu.Lock()
	s.bindCalls = append(s.bindCalls, bindings)
	s.boundMode = bindings.Mode
	cb := s.onBind
	s.mu.Unlock()
	if cb != nil {
		cb(bindings)
	}
	return nil
}

func (s *tpmMockSession) Subscribe(listener core.AgentSessionEventListener) func() {
	s.mu.Lock()
	s.subscribeCalls++
	idx := len(s.listeners)
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if idx < len(s.listeners) {
			s.listeners[idx] = nil
		}
		s.mu.Unlock()
	}
}

func (s *tpmMockSession) SubscribeAgent(listener func(event agent.AgentEvent)) func() {
	s.mu.Lock()
	s.agentSubCalls++
	idx := len(s.agentListeners)
	s.agentListeners = append(s.agentListeners, listener)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if idx < len(s.agentListeners) {
			s.agentListeners[idx] = nil
		}
		s.mu.Unlock()
	}
}

func (s *tpmMockSession) Prompt(_ context.Context, text string, options ...*core.PromptOptions) error {
	s.mu.Lock()
	s.promptCalls = append(s.promptCalls, tpmPromptCall{text: text, options: options})
	err := s.promptErr
	s.mu.Unlock()
	return err
}

func (s *tpmMockSession) Messages() agent.AgentMessageList {
	return s.messages
}

func (s *tpmMockSession) WaitForIdle(context.Context) error {
	return nil
}

func (s *tpmMockSession) NavigateTree(context.Context, string, ...*core.NavigateTreeOptions) (*core.NavigateTreeResult, error) {
	return &core.NavigateTreeResult{Cancelled: false}, nil
}

func (s *tpmMockSession) Reload(context.Context) error {
	s.mu.Lock()
	s.reloadCalls++
	s.mu.Unlock()
	return nil
}

func (s *tpmMockSession) CreateReplacedSessionContext() core.ReplacedSessionContext {
	return nil
}

type tpmMockRuntimeHost struct {
	session      *tpmMockSession
	disposeCalls int
	disposeFn    func(ctx context.Context) error
	newSessFn    func(ctx context.Context, opts ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error)
	forkFn       func(ctx context.Context, entryId string, opts ...*core.ForkSessionOptions) (*core.ForkSessionResult, error)
	switchFn     func(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error)
	mu           sync.Mutex
}

func (r *tpmMockRuntimeHost) CurrentSession() pmSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *tpmMockRuntimeHost) NewSession(ctx context.Context, options ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error) {
	if r.newSessFn != nil {
		return r.newSessFn(ctx, options...)
	}
	return &core.NewSessionResult{Cancelled: false}, nil
}

func (r *tpmMockRuntimeHost) Fork(ctx context.Context, entryId string, options ...*core.ForkSessionOptions) (*core.ForkSessionResult, error) {
	if r.forkFn != nil {
		return r.forkFn(ctx, entryId, options...)
	}
	return &core.ForkSessionResult{Cancelled: false}, nil
}

func (r *tpmMockRuntimeHost) SwitchSession(ctx context.Context, sessionPath string) (*core.SwitchSessionResult, error) {
	if r.switchFn != nil {
		return r.switchFn(ctx, sessionPath)
	}
	return &core.SwitchSessionResult{Cancelled: false}, nil
}

func (r *tpmMockRuntimeHost) Dispose(ctx context.Context) error {
	r.mu.Lock()
	r.disposeCalls++
	fn := r.disposeFn
	s := r.session
	r.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	if s != nil {
		s.mu.Lock()
		if s.hasShutdownHook {
			s.shutdownCount++
		}
		s.mu.Unlock()
	}
	return nil
}

func tpmCreateAssistantMessage(text string, stopReason ai.StopReason, errMsg *string) *ai.AssistantMessage {
	var content ai.ContentList
	if text != "" {
		content = ai.ContentList{&ai.TextContent{Type: "text", Text: text}}
	}
	if stopReason == "" {
		stopReason = ai.StopReasonStop
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      content,
		Api:          "openai-responses",
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		StopReason:   stopReason,
		ErrorMessage: errMsg,
		Timestamp:    1000,
	}
}

func tpmCreateHost(msg *ai.AssistantMessage) *tpmMockRuntimeHost {
	return &tpmMockRuntimeHost{
		session: &tpmMockSession{
			messages:        agent.AgentMessageList{msg},
			hasShutdownHook: true,
		},
	}
}

func TestRunPrintMode(t *testing.T) {
	t.Run("binds print extensions, sends prompt with images, writes text, and disposes", func(t *testing.T) {
		host := tpmCreateHost(tpmCreateAssistantMessage("done", ai.StopReasonStop, nil))
		images := []*ai.ImageContent{{Type: "image", MimeType: "image/png", Data: "abc"}}
		initial := "Say done"
		var stdout, stderr bytes.Buffer

		code := pmRunPrintMode(context.Background(), host, &PrintModeOptions{
			Mode:           PrintModeOutputText,
			InitialMessage: &initial,
			InitialImages:  images,
			Stdout:         &stdout,
			Stderr:         &stderr,
		}, &pmHooks{signalCh: make(chan os.Signal), stopSig: func() {}})

		if code != 0 {
			t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
		}
		if stdout.String() != "done\n" {
			t.Fatalf("expected stdout %q, got %q", "done\n", stdout.String())
		}
		if len(host.session.promptCalls) != 1 || host.session.promptCalls[0].text != "Say done" {
			t.Fatalf("unexpected promptCalls: %#v", host.session.promptCalls)
		}
		if len(host.session.promptCalls[0].options) != 1 || len(host.session.promptCalls[0].options[0].Images) != 1 {
			t.Fatalf("expected images forwarded to prompt")
		}
		if host.session.boundMode != core.ExtensionModePrint {
			t.Fatalf("expected ExtensionModePrint, got %q", host.session.boundMode)
		}
		if host.disposeCalls != 1 || host.session.shutdownCount != 1 {
			t.Fatalf("expected 1 dispose and 1 shutdown, got %d / %d", host.disposeCalls, host.session.shutdownCount)
		}
	})

	t.Run("binds json extensions, emits header and wire events, and disposes", func(t *testing.T) {
		host := tpmCreateHost(tpmCreateAssistantMessage("done", ai.StopReasonStop, nil))
		headerId := "sess-1"
		ver := core.CurrentSessionVersion
		host.session.header = &core.SessionHeader{
			Type:      "session",
			Version:   &ver,
			Id:        headerId,
			Timestamp: "2026-01-01T00:00:00Z",
			Cwd:       "/tmp",
		}
		var stdout, stderr bytes.Buffer

		code := pmRunPrintMode(context.Background(), host, &PrintModeOptions{
			Mode:     PrintModeOutputJson,
			Messages: []string{"hello"},
			Stdout:   &stdout,
			Stderr:   &stderr,
		}, &pmHooks{signalCh: make(chan os.Signal), stopSig: func() {}})

		if code != 0 {
			t.Fatalf("expected exit code 0, got %d", code)
		}
		if len(host.session.promptCalls) != 1 || host.session.promptCalls[0].text != "hello" {
			t.Fatalf("unexpected promptCalls: %#v", host.session.promptCalls)
		}
		if host.session.boundMode != core.ExtensionModeJson {
			t.Fatalf("expected ExtensionModeJson, got %q", host.session.boundMode)
		}
		if !strings.Contains(stdout.String(), `"id":"sess-1"`) {
			t.Fatalf("expected session header line in json stdout, got %q", stdout.String())
		}
		if host.disposeCalls != 1 || host.session.shutdownCount != 1 {
			t.Fatalf("expected 1 dispose and 1 shutdown, got %d / %d", host.disposeCalls, host.session.shutdownCount)
		}
	})

	t.Run("prints assistant error to stderr and returns non-zero", func(t *testing.T) {
		errMsg := "provider failure"
		host := tpmCreateHost(tpmCreateAssistantMessage("", ai.StopReasonError, &errMsg))
		var stdout, stderr bytes.Buffer

		code := pmRunPrintMode(context.Background(), host, &PrintModeOptions{
			Mode:   PrintModeOutputText,
			Stdout: &stdout,
			Stderr: &stderr,
		}, &pmHooks{signalCh: make(chan os.Signal), stopSig: func() {}})

		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if strings.TrimSpace(stderr.String()) != "provider failure" {
			t.Fatalf("expected stderr %q, got %q", "provider failure", stderr.String())
		}
		if host.disposeCalls != 1 || host.session.shutdownCount != 1 {
			t.Fatalf("expected dispose/shutdown")
		}
	})

	t.Run("warns on stderr and returns non-zero when truncated by length", func(t *testing.T) {
		host := tpmCreateHost(tpmCreateAssistantMessage("partial", ai.StopReasonLength, nil))
		var stdout, stderr bytes.Buffer

		code := pmRunPrintMode(context.Background(), host, &PrintModeOptions{
			Mode:   PrintModeOutputText,
			Stdout: &stdout,
			Stderr: &stderr,
		}, &pmHooks{signalCh: make(chan os.Signal), stopSig: func() {}})

		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if stdout.String() != "partial\n" {
			t.Fatalf("expected stdout %q, got %q", "partial\n", stdout.String())
		}
		if !strings.Contains(stderr.String(), "truncated") {
			t.Fatalf("expected truncation warning on stderr, got %q", stderr.String())
		}
	})

	t.Run("waits for final disposal on first SIGTERM and exits immediately on repeated SIGTERM", func(t *testing.T) {
		host := tpmCreateHost(tpmCreateAssistantMessage("done", ai.StopReasonStop, nil))
		disposeStarted := make(chan struct{})
		finishDispose := make(chan struct{})
		host.disposeFn = func(context.Context) error {
			close(disposeStarted)
			<-finishDispose
			return nil
		}

		sigCh := make(chan os.Signal, 4)
		var (
			exitMu    sync.Mutex
			exitCodes []int
		)
		exitOnce := make(chan int, 4)
		initial := "Say done"
		var stdout, stderr bytes.Buffer

		runDone := make(chan int, 1)
		go func() {
			runDone <- pmRunPrintMode(context.Background(), host, &PrintModeOptions{
				Mode:           PrintModeOutputText,
				InitialMessage: &initial,
				Stdout:         &stdout,
				Stderr:         &stderr,
			}, &pmHooks{
				signalCh: sigCh,
				stopSig:  func() {},
				exitFn: func(code int) {
					exitMu.Lock()
					exitCodes = append(exitCodes, code)
					exitMu.Unlock()
					exitOnce <- code
				},
			})
		}()

		<-disposeStarted

		// First SIGTERM waits for active disposal rather than calling exit immediately.
		sigCh <- syscall.SIGTERM
		time.Sleep(25 * time.Millisecond)
		exitMu.Lock()
		if len(exitCodes) != 0 {
			exitMu.Unlock()
			t.Fatalf("expected no immediate exit on first SIGTERM during disposal, got %v", exitCodes)
		}
		exitMu.Unlock()

		// Repeated SIGTERM forces immediate exit with 143.
		sigCh <- syscall.SIGTERM
		gotCode := <-exitOnce
		if gotCode != 143 {
			t.Fatalf("expected forced exit code 143, got %d", gotCode)
		}

		close(finishDispose)
		if finalCode := <-runDone; finalCode != 0 {
			t.Fatalf("expected normal return 0 after finishDispose, got %d", finalCode)
		}
		if host.disposeCalls != 1 {
			t.Fatalf("expected Dispose called exactly once, got %d", host.disposeCalls)
		}
	})
}
