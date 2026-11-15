// Ported from packages/coding-agent/test/rpc-prompt-response-semantics.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type trprsMockRuntimeHost struct {
	mu           sync.Mutex
	session      *core.AgentSession
	disposeCount atomic.Int32
	disposeFn    func(ctx context.Context) error
}

func (m *trprsMockRuntimeHost) CurrentSession() *core.AgentSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.session
}

func (m *trprsMockRuntimeHost) NewSession(_ context.Context, _ ...*core.NewSessionRuntimeOptions) (*core.NewSessionResult, error) {
	return &core.NewSessionResult{Cancelled: false}, nil
}

func (m *trprsMockRuntimeHost) SwitchSession(_ context.Context, _ string) (*core.SwitchSessionResult, error) {
	return &core.SwitchSessionResult{Cancelled: false}, nil
}

func (m *trprsMockRuntimeHost) Fork(_ context.Context, _ string, _ ...*core.ForkSessionOptions) (*core.ForkSessionResult, error) {
	return &core.ForkSessionResult{Cancelled: false}, nil
}

func (m *trprsMockRuntimeHost) Dispose(ctx context.Context) error {
	m.disposeCount.Add(1)
	if m.disposeFn != nil {
		return m.disposeFn(ctx)
	}
	return nil
}

type trprsHarness struct {
	runtime *core.AgentSessionRuntime
	faux    *providers.FauxProviderRegistration
	stdinW  *io.PipeWriter
	stdout  *trprsLineFeed
	exitCh  chan int
	doneCh  chan error
	cancel  context.CancelFunc
	cleanup func()
}

type trprsLineFeed struct {
	mu    sync.Mutex
	buf   []byte
	lines []*jsonx.Object
}

func (f *trprsLineFeed) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf = append(f.buf, p...)
	for {
		idx := -1
		for i, b := range f.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(f.buf[:idx]), "\r")
		f.buf = f.buf[idx+1:]
		if v, err := jsonx.Parse([]byte(line)); err == nil {
			if obj, ok := v.(*jsonx.Object); ok && obj != nil {
				f.lines = append(f.lines, obj)
			}
		}
	}
	return len(p), nil
}

func (f *trprsLineFeed) Snapshot() []*jsonx.Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*jsonx.Object, len(f.lines))
	copy(out, f.lines)
	return out
}

func (f *trprsLineFeed) Reset() {
	f.mu.Lock()
	f.lines = nil
	f.mu.Unlock()
}

func (f *trprsLineFeed) WaitFor(t *testing.T, timeout time.Duration, pred func([]*jsonx.Object) bool) []*jsonx.Object {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap := f.Snapshot()
		if pred(snap) {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap := f.Snapshot()
	t.Fatalf("timed out waiting for expected JSONL output; got %d lines", len(snap))
	return snap
}

func trprsBuildRuntime(t *testing.T, withAuth bool) (*core.AgentSessionRuntime, *providers.FauxProviderRegistration, func()) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	agentDir := tempDir + "/agent"
	cwd := tempDir + "/cwd"
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	reasoning := true
	faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{Id: "faux-1", Reasoning: &reasoning},
			{Id: "faux-2", Reasoning: &reasoning},
		},
	})
	faux.SetResponses([]providers.FauxResponseStep{})

	model := faux.GetModel()
	key := "faux-key"

	createRuntime := func(rtCtx context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(rtCtx, &core.CreateAgentSessionServicesOptions{
			Cwd:               args.Cwd,
			AgentDir:          args.AgentDir,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			NoContextFiles:    true,
		})
		if err != nil {
			return nil, err
		}
		apiStr := faux.Api
		baseUrl := model.BaseUrl
		var providerModels []core.ProviderModelConfig
		for _, m := range faux.Models {
			apiCopy := m.Api
			baseCopy := m.BaseUrl
			providerModels = append(providerModels, &core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:          m.Id,
					Name:        m.Name,
					Api:         &apiCopy,
					BaseUrl:     &baseCopy,
					Input:       m.Input,
					InputLimits: m.InputLimits,
					Cost:        m.Cost,
				},
				Reasoning:     m.Reasoning,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxTokens,
			})
		}
		if withAuth {
			if _, err := services.AuthStorage.Modify(rtCtx, string(model.Provider), func(ai.Credential) (ai.Credential, error) {
				return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
			}, nil); err != nil {
				return nil, err
			}
			_ = services.ModelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
				BaseUrl: &baseUrl,
				ApiKey:  &key,
				Api:     &apiStr,
				Models:  providerModels,
			})
		} else {
			_ = services.ModelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
				BaseUrl: &baseUrl,
				Api:     &apiStr,
				Models:  providerModels,
			})
		}
		res, err := core.CreateAgentSessionFromServices(rtCtx, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    args.SessionManager,
			SessionStartEvent: args.SessionStartEvent,
			Model:             model,
		})
		if err != nil {
			return nil, err
		}
		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *res,
			Services:                 services,
		}, nil
	}

	rt, err := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: core.SessionManagerInMemory(cwd),
	})
	if err != nil {
		faux.Unregister()
		t.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	return rt, faux, func() {
		_ = rt.Dispose(context.Background())
		faux.Unregister()
	}
}

func trprsStartMode(t *testing.T, withAuth bool) *trprsHarness {
	t.Helper()
	rt, faux, cleanRt := trprsBuildRuntime(t, withAuth)
	stdinR, stdinW := io.Pipe()
	feed := &trprsLineFeed{}
	ctx, cancel := context.WithCancel(context.Background())
	exitCh := make(chan int, 1)
	doneCh := make(chan error, 1)
	ready := make(chan struct{})

	go func() {
		err := rmRunRpcMode(ctx, &rmCoreRuntimeAdapter{runtime: rt}, &RpcModeOptions{
			Stdin:      stdinR,
			Stdout:     feed,
			Stderr:     io.Discard,
			SignalChan: make(chan os.Signal, 1),
			OnExit: func(code int) {
				select {
				case exitCh <- code:
				default:
				}
			},
		}, &rmHooks{onReady: func() { close(ready) }})
		doneCh <- err
	}()
	<-ready

	return &trprsHarness{
		runtime: rt,
		faux:    faux,
		stdinW:  stdinW,
		stdout:  feed,
		exitCh:  exitCh,
		doneCh:  doneCh,
		cancel:  cancel,
		cleanup: func() {
			cancel()
			_ = stdinW.Close()
			_ = stdinR.Close()
			<-doneCh
			cleanRt()
		},
	}
}

func trprsSendLine(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatalf("WriteString(%s): %v", line, err)
	}
}

func trprsResponsesForId(lines []*jsonx.Object, id string) []*jsonx.Object {
	var out []*jsonx.Object
	for _, obj := range lines {
		typ, _ := obj.GetString("type")
		cmd, _ := obj.GetString("command")
		gotId, _ := obj.GetString("id")
		if typ == "response" && cmd == "prompt" && gotId == id {
			out = append(out, obj)
		}
	}
	return out
}

func TestRpcPromptPreflightRejection(t *testing.T) {
	h := trprsStartMode(t, false)
	defer h.cleanup()

	trprsSendLine(t, h.stdinW, `{"id":"b1","type":"prompt","message":"Hello"}`)
	lines := h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "b1")) == 1
	})
	resp := trprsResponsesForId(lines, "b1")[0]
	if ok, _ := resp.GetBool("success"); ok {
		t.Fatalf("expected failure response, got %v", resp)
	}
	errMsg, _ := resp.GetString("error")
	if !strings.Contains(errMsg, "No API key found for") {
		t.Fatalf("expected missing API key error, got %q", errMsg)
	}
}

func TestRpcPromptPreflightStarted(t *testing.T) {
	h := trprsStartMode(t, true)
	defer h.cleanup()

	h.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Hi!"), nil),
		},
	})
	trprsSendLine(t, h.stdinW, `{"id":"b2","type":"prompt","message":"Hello"}`)
	lines := h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "b2")) == 1
	})
	resp := trprsResponsesForId(lines, "b2")[0]
	if ok, _ := resp.GetBool("success"); !ok {
		t.Fatalf("expected success response, got %v", resp)
	}
	dataVal, _ := resp.Get("data")
	dataObj, _ := dataVal.(*jsonx.Object)
	disp, _ := dataObj.GetString("disposition")
	if disp != "started" {
		t.Fatalf("expected disposition=started, got %q", disp)
	}
}

func TestRpcPromptQueuedDuringStreaming(t *testing.T) {
	h := trprsStartMode(t, true)
	defer h.cleanup()

	gate := make(chan struct{})
	gateEntered := make(chan struct{})
	h.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			close(gateEntered)
			select {
			case <-gate:
			case <-ctx.Done():
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil), nil
		}),
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil),
		},
	})

	trprsSendLine(t, h.stdinW, `{"id":"b3-start","type":"prompt","message":"Start"}`)
	<-gateEntered
	h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "b3-start")) == 1
	})
	h.stdout.Reset()

	trprsSendLine(t, h.stdinW, `{"id":"b3","type":"prompt","message":"Queue this","streamingBehavior":"followUp"}`)
	lines := h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "b3")) == 1
	})
	close(gate)

	resp := trprsResponsesForId(lines, "b3")[0]
	dataVal, _ := resp.Get("data")
	dataObj, _ := dataVal.(*jsonx.Object)
	if disp, _ := dataObj.GetString("disposition"); disp != "queued" {
		t.Fatalf("expected disposition=queued, got %q", disp)
	}

	h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		for _, obj := range lines {
			if typ, _ := obj.GetString("type"); typ == "agent_settled" {
				return true
			}
		}
		return false
	})
}

func TestRpcClearQueueSemantics(t *testing.T) {
	h := trprsStartMode(t, true)
	defer h.cleanup()

	gate := make(chan struct{})
	gateEntered := make(chan struct{})
	h.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			close(gateEntered)
			select {
			case <-gate:
			case <-ctx.Done():
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil), nil
		}),
	})

	trprsSendLine(t, h.stdinW, `{"id":"c1","type":"prompt","message":"Start"}`)
	<-gateEntered
	trprsSendLine(t, h.stdinW, `{"id":"c2","type":"prompt","message":"Change direction","streamingBehavior":"steer"}`)
	h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "c2")) == 1
	})
	trprsSendLine(t, h.stdinW, `{"id":"c3","type":"prompt","message":"Summarize when finished","streamingBehavior":"followUp"}`)
	h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		return len(trprsResponsesForId(lines, "c3")) == 1
	})

	trprsSendLine(t, h.stdinW, `{"id":"clear","type":"clear_queue"}`)
	lines := h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		for _, obj := range lines {
			if id, _ := obj.GetString("id"); id == "clear" {
				return true
			}
		}
		return false
	})
	close(gate)

	var clearResp *jsonx.Object
	for _, obj := range lines {
		if id, _ := obj.GetString("id"); id == "clear" {
			clearResp = obj
			break
		}
	}
	dataVal, _ := clearResp.Get("data")
	var got core.QueuedMessagesText
	if err := jsonx.Decode(dataVal, &got); err != nil {
		t.Fatalf("Decode clear_queue data: %v", err)
	}
	if len(got.Steering) != 1 || got.Steering[0] != "Change direction" {
		t.Fatalf("unexpected steering: %#v", got.Steering)
	}
	if len(got.FollowUp) != 1 || got.FollowUp[0] != "Summarize when finished" {
		t.Fatalf("unexpected followUp: %#v", got.FollowUp)
	}

	h.stdout.WaitFor(t, 5*time.Second, func(lines []*jsonx.Object) bool {
		for _, obj := range lines {
			if typ, _ := obj.GetString("type"); typ == "agent_settled" {
				return true
			}
		}
		return false
	})
	agentStarts := 0
	for _, obj := range h.stdout.Snapshot() {
		if typ, _ := obj.GetString("type"); typ == "agent_start" {
			agentStarts++
		}
	}
	if agentStarts != 1 {
		t.Fatalf("expected 1 agent_start after clear_queue, got %d", agentStarts)
	}
}

func TestRpcExtensionShutdownWhenIdle(t *testing.T) {
	rt, _, cleanRt := trprsBuildRuntime(t, false)
	defer cleanRt()

	stdinR, stdinW := io.Pipe()
	defer func() {
		_ = stdinW.Close()
		_ = stdinR.Close()
	}()

	host := &trprsMockRuntimeHost{session: rt.Session}
	sigCh := make(chan os.Signal, 2)
	exitCh := make(chan int, 1)
	doneCh := make(chan error, 1)
	ready := make(chan struct{})

	go func() {
		doneCh <- rmRunRpcMode(context.Background(), host, &RpcModeOptions{
			Stdin:      stdinR,
			Stdout:     io.Discard,
			Stderr:     io.Discard,
			SignalChan: sigCh,
			OnExit: func(code int) {
				exitCh <- code
			},
		}, &rmHooks{onReady: func() { close(ready) }})
	}()
	<-ready

	rt.Session.ExtensionRunner().CreateContext().Shutdown()

	select {
	case code := <-exitCh:
		if code != 0 {
			t.Fatalf("expected exit 0, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for idle extension shutdown")
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("RunRpcMode returned error: %v", err)
	}
	if host.disposeCount.Load() != 1 {
		t.Fatalf("expected 1 Dispose call, got %d", host.disposeCount.Load())
	}
}

func TestRpcShutdownTriggers(t *testing.T) {
	t.Run("stdin EOF with one SIGTERM exits 0 after unblock", func(t *testing.T) {
		rt, _, cleanRt := trprsBuildRuntime(t, false)
		defer cleanRt()
		stdinR, stdinW := io.Pipe()
		defer func() { _ = stdinR.Close() }()
		sigCh := make(chan os.Signal, 4)
		exitCh := make(chan int, 4)
		disposeStarted := make(chan struct{}, 1)
		releaseDispose := make(chan struct{})
		doneCh := make(chan error, 1)
		ready := make(chan struct{})

		host := &trprsMockRuntimeHost{
			session: rt.Session,
			disposeFn: func(context.Context) error {
				disposeStarted <- struct{}{}
				<-releaseDispose
				return nil
			},
		}
		go func() {
			doneCh <- rmRunRpcMode(context.Background(), host, &RpcModeOptions{
				Stdin:      stdinR,
				Stdout:     io.Discard,
				Stderr:     io.Discard,
				SignalChan: sigCh,
				OnExit:     func(code int) { exitCh <- code },
			}, &rmHooks{onReady: func() { close(ready) }})
		}()
		<-ready

		_ = stdinW.Close()
		<-disposeStarted
		sigCh <- syscall.SIGTERM
		time.Sleep(20 * time.Millisecond)
		select {
		case code := <-exitCh:
			t.Fatalf("unexpected early exit %d", code)
		default:
		}

		close(releaseDispose)
		select {
		case code := <-exitCh:
			if code != 0 {
				t.Fatalf("expected exit 0, got %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for exit 0")
		}
		<-doneCh
		if host.disposeCount.Load() != 1 {
			t.Fatalf("expected Dispose count 1, got %d", host.disposeCount.Load())
		}
	})

	t.Run("one SIGTERM then stdin EOF waits for dispose and exits 143", func(t *testing.T) {
		rt, _, cleanRt := trprsBuildRuntime(t, false)
		defer cleanRt()
		stdinR, stdinW := io.Pipe()
		defer func() { _ = stdinR.Close() }()
		sigCh := make(chan os.Signal, 4)
		exitCh := make(chan int, 4)
		disposeStarted := make(chan struct{}, 1)
		releaseDispose := make(chan struct{})
		doneCh := make(chan error, 1)
		ready := make(chan struct{})

		host := &trprsMockRuntimeHost{
			session: rt.Session,
			disposeFn: func(context.Context) error {
				disposeStarted <- struct{}{}
				<-releaseDispose
				return nil
			},
		}
		go func() {
			doneCh <- rmRunRpcMode(context.Background(), host, &RpcModeOptions{
				Stdin:      stdinR,
				Stdout:     io.Discard,
				Stderr:     io.Discard,
				SignalChan: sigCh,
				OnExit:     func(code int) { exitCh <- code },
			}, &rmHooks{onReady: func() { close(ready) }})
		}()
		<-ready

		sigCh <- syscall.SIGTERM
		<-disposeStarted
		_ = stdinW.Close()
		time.Sleep(20 * time.Millisecond)
		select {
		case code := <-exitCh:
			t.Fatalf("unexpected early exit %d", code)
		default:
		}

		close(releaseDispose)
		select {
		case code := <-exitCh:
			if code != 143 {
				t.Fatalf("expected exit 143, got %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for exit 143")
		}
		<-doneCh
		if host.disposeCount.Load() != 1 {
			t.Fatalf("expected Dispose count 1, got %d", host.disposeCount.Load())
		}
	})

	t.Run("exits at once on repeated SIGTERM during shutdown", func(t *testing.T) {
		rt, _, cleanRt := trprsBuildRuntime(t, false)
		defer cleanRt()
		stdinR, stdinW := io.Pipe()
		defer func() { _ = stdinR.Close() }()
		sigCh := make(chan os.Signal, 4)
		exitCh := make(chan int, 4)
		disposeStarted := make(chan struct{}, 1)
		releaseDispose := make(chan struct{})
		doneCh := make(chan error, 1)
		ready := make(chan struct{})

		host := &trprsMockRuntimeHost{
			session: rt.Session,
			disposeFn: func(context.Context) error {
				disposeStarted <- struct{}{}
				<-releaseDispose
				return nil
			},
		}
		go func() {
			doneCh <- rmRunRpcMode(context.Background(), host, &RpcModeOptions{
				Stdin:      stdinR,
				Stdout:     io.Discard,
				Stderr:     io.Discard,
				SignalChan: sigCh,
				OnExit:     func(code int) { exitCh <- code },
			}, &rmHooks{onReady: func() { close(ready) }})
		}()
		<-ready

		_ = stdinW.Close()
		<-disposeStarted
		sigCh <- syscall.SIGTERM
		sigCh <- syscall.SIGTERM

		select {
		case code := <-exitCh:
			if code != 143 {
				t.Fatalf("expected immediate exit 143, got %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for repeated signal exit")
		}
		close(releaseDispose)
		<-doneCh
	})
}
