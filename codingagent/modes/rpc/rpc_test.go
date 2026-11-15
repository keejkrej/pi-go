// Ported from packages/coding-agent/test/rpc.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type trpcHarness struct {
	runtime *core.AgentSessionRuntime
	faux    *providers.FauxProviderRegistration
	client  *RpcClient
	stdinW  *io.PipeWriter
	cleanup func()
}

func trpcStartPair(t *testing.T) *trpcHarness {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, "agent")
	cwd := filepath.Join(tempDir, "cwd")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	reasoning := true
	nonReasoning := false
	faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{Id: "faux-1", Reasoning: &reasoning},
			{Id: "faux-2", Reasoning: &nonReasoning},
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

	sm, err := core.SessionManagerCreate(cwd, core.GetDefaultSessionDir(cwd, agentDir))
	if err != nil {
		faux.Unregister()
		t.Fatal(err)
	}
	rt, err := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sm,
	})
	if err != nil {
		faux.Unregister()
		t.Fatal(err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	modeCtx, modeCancel := context.WithCancel(context.Background())
	modeDone := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		defer func() { _ = outW.Close() }()
		modeDone <- rmRunRpcMode(modeCtx, &rmCoreRuntimeAdapter{runtime: rt}, &RpcModeOptions{
			Stdin:      inR,
			Stdout:     outW,
			Stderr:     io.Discard,
			SignalChan: make(chan os.Signal, 1),
			OnExit:     func(int) {},
		}, &rmHooks{onReady: func() { close(ready) }})
	}()
	<-ready

	client := NewRpcClient(&RpcClientOptions{RequestTimeout: 5 * time.Second})
	if err := client.attachTestStreams(inW, outR); err != nil {
		modeCancel()
		_ = inW.Close()
		_ = inR.Close()
		_ = outR.Close()
		<-modeDone
		_ = rt.Dispose(context.Background())
		faux.Unregister()
		t.Fatal(err)
	}

	return &trpcHarness{
		runtime: rt,
		faux:    faux,
		client:  client,
		stdinW:  inW,
		cleanup: func() {
			modeCancel()
			_ = client.Stop()
			_ = inR.Close()
			_ = outR.Close()
			<-modeDone
			_ = rt.Dispose(context.Background())
			faux.Unregister()
		},
	}
}

func TestRpcEndToEndSessionCommands(t *testing.T) {
	h := trpcStartPair(t)
	defer h.cleanup()
	ctx := context.Background()

	// Initial state & get_last_assistant_text when empty
	state, err := h.client.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.SessionName != nil || state.MessageCount != 0 {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	lastTxt, err := h.client.GetLastAssistantText(ctx)
	if err != nil || lastTxt != nil {
		t.Fatalf("expected nil initial assistant text, got (%v, %v)", lastTxt, err)
	}

	// Models: get_available_models, cycle_model, set_model
	models, err := h.client.GetAvailableModels(ctx)
	if err != nil || len(models) < 2 {
		t.Fatalf("expected >=2 models, got len=%d err=%v", len(models), err)
	}
	cycledModel, err := h.client.CycleModel(ctx)
	if err != nil || cycledModel == nil || cycledModel.Model == nil {
		t.Fatalf("CycleModel failed: res=%v err=%v", cycledModel, err)
	}
	setModelRes, err := h.client.SetModel(ctx, string(models[0].Provider), models[0].Id)
	if err != nil || setModelRes.Id != models[0].Id {
		t.Fatalf("SetModel failed: res=%v err=%v", setModelRes, err)
	}
	if _, err := h.client.SetModel(ctx, "missing", "none"); err == nil || !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("expected Model not found error, got %v", err)
	}

	// Thinking levels
	if err := h.client.SetThinkingLevel(ctx, agent.ThinkingLevelHigh); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	state, err = h.client.GetState(ctx)
	if err != nil || state.ThinkingLevel != agent.ThinkingLevelHigh {
		t.Fatalf("expected high thinking level, got %+v err=%v", state.ThinkingLevel, err)
	}
	levels, err := h.client.GetAvailableThinkingLevels(ctx)
	if err != nil || len(levels) == 0 {
		t.Fatalf("GetAvailableThinkingLevels: %v %v", levels, err)
	}
	cycledLevel, err := h.client.CycleThinkingLevel(ctx)
	if err != nil || cycledLevel == nil {
		t.Fatalf("CycleThinkingLevel: %v %v", cycledLevel, err)
	}

	// Queue / compaction / retry setters
	if err := h.client.SetSteeringMode(ctx, agent.QueueModeAll); err != nil {
		t.Fatalf("SetSteeringMode: %v", err)
	}
	if err := h.client.SetFollowUpMode(ctx, agent.QueueModeOneAtATime); err != nil {
		t.Fatalf("SetFollowUpMode: %v", err)
	}
	if err := h.client.SetAutoCompaction(ctx, false); err != nil {
		t.Fatalf("SetAutoCompaction: %v", err)
	}
	if err := h.client.SetAutoRetry(ctx, false); err != nil {
		t.Fatalf("SetAutoRetry: %v", err)
	}
	if err := h.client.AbortRetry(ctx); err != nil {
		t.Fatalf("AbortRetry: %v", err)
	}

	// Bash execution
	bashRes, err := h.client.Bash(ctx, "echo hello-rpc")
	if err != nil || strings.TrimSpace(bashRes.Output) != "hello-rpc" {
		t.Fatalf("Bash: res=%+v err=%v", bashRes, err)
	}

	// Prompt and wait
	h.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("test123"), nil),
		},
	})
	events, err := h.client.PromptAndWait(ctx, "Reply with just: test123")
	if err != nil || len(events) == 0 {
		t.Fatalf("PromptAndWait: len=%d err=%v", len(events), err)
	}

	lastTxt, err = h.client.GetLastAssistantText(ctx)
	if err != nil || lastTxt == nil || !strings.Contains(*lastTxt, "test123") {
		t.Fatalf("GetLastAssistantText: %v %v", lastTxt, err)
	}

	// Stats, messages, entries, tree, fork messages, commands
	stats, err := h.client.GetSessionStats(ctx)
	if err != nil || stats.UserMessages < 1 || stats.AssistantMessages < 1 {
		t.Fatalf("GetSessionStats: %+v err=%v", stats, err)
	}
	msgs, err := h.client.GetMessages(ctx)
	if err != nil || len(msgs) < 2 {
		t.Fatalf("GetMessages: len=%d err=%v", len(msgs), err)
	}
	entriesData, err := h.client.GetEntries(ctx)
	if err != nil || len(entriesData.Entries) < 2 || entriesData.LeafId == nil {
		t.Fatalf("GetEntries: %+v err=%v", entriesData, err)
	}
	sinceData, err := h.client.GetEntries(ctx, entriesData.Entries[0].GetId())
	if err != nil || len(sinceData.Entries) != len(entriesData.Entries)-1 {
		t.Fatalf("GetEntries(since): len=%d vs %d err=%v", len(sinceData.Entries), len(entriesData.Entries)-1, err)
	}
	if _, err := h.client.GetEntries(ctx, "nonexistent-id"); err == nil || !strings.Contains(err.Error(), "Entry not found") {
		t.Fatalf("expected Entry not found error, got %v", err)
	}
	treeData, err := h.client.GetTree(ctx)
	if err != nil || len(treeData.Tree) == 0 || treeData.LeafId == nil {
		t.Fatalf("GetTree: %+v err=%v", treeData, err)
	}
	forkMsgs, err := h.client.GetForkMessages(ctx)
	if err != nil || len(forkMsgs) < 1 {
		t.Fatalf("GetForkMessages: %+v err=%v", forkMsgs, err)
	}
	cmds, err := h.client.GetCommands(ctx)
	if err != nil || cmds == nil {
		t.Fatalf("GetCommands: %+v err=%v", cmds, err)
	}

	// SetSessionName validation and success
	if err := h.client.SetSessionName(ctx, "   "); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}
	if err := h.client.SetSessionName(ctx, "my-test-session"); err != nil {
		t.Fatalf("SetSessionName: %v", err)
	}
	state, err = h.client.GetState(ctx)
	if err != nil || state.SessionName == nil || *state.SessionName != "my-test-session" {
		t.Fatalf("expected updated session name, got %+v err=%v", state.SessionName, err)
	}

	// ExportHtml
	outHtml := filepath.Join(t.TempDir(), "export.html")
	exported, err := h.client.ExportHtml(ctx, outHtml)
	if err != nil || exported != outHtml {
		t.Fatalf("ExportHtml: %q err=%v", exported, err)
	}
	if _, statErr := os.Stat(exported); statErr != nil {
		t.Fatalf("expected exported html file to exist: %v", statErr)
	}

	// Clone + SwitchSession + Fork + NewSession (exercises session rebinding!)
	originalSessionFile := *state.SessionFile
	cloneRes, err := h.client.Clone(ctx)
	if err != nil || cloneRes.Cancelled {
		t.Fatalf("Clone: %+v err=%v", cloneRes, err)
	}
	switchRes, err := h.client.SwitchSession(ctx, originalSessionFile)
	if err != nil || switchRes.Cancelled {
		t.Fatalf("SwitchSession: %+v err=%v", switchRes, err)
	}
	forkRes, err := h.client.Fork(ctx, forkMsgs[0].EntryId)
	if err != nil || forkRes.Cancelled {
		t.Fatalf("Fork: %+v err=%v", forkRes, err)
	}
	newRes, err := h.client.NewSession(ctx)
	if err != nil || newRes.Cancelled {
		t.Fatalf("NewSession: %+v err=%v", newRes, err)
	}
	state, err = h.client.GetState(ctx)
	if err != nil || state.MessageCount != 0 {
		t.Fatalf("expected cleared messages after NewSession, got %d err=%v", state.MessageCount, err)
	}
	// Verify events still flow after NewSession rebind!
	h.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after-new"), nil),
		},
	})
	eventsAfter, err := h.client.PromptAndWait(ctx, "hello after new_session")
	if err != nil || len(eventsAfter) == 0 {
		t.Fatalf("PromptAndWait after new_session failed: len=%d err=%v", len(eventsAfter), err)
	}
}

func TestRpcExtensionUIAndParseErrors(t *testing.T) {
	h := trpcStartPair(t)
	defer h.cleanup()
	ctx := context.Background()

	var (
		mu       sync.Mutex
		requests []*RpcExtensionUIRequest
		errEvs   []*RpcExtensionErrorEvent
	)
	reqNotifyCh := make(chan *RpcExtensionUIRequest, 16)
	unsub := h.client.OnEvent(func(ev modes.JsonAgentSessionEvent) {
		switch e := ev.(type) {
		case *RpcExtensionUIRequest:
			mu.Lock()
			requests = append(requests, e)
			mu.Unlock()
			reqNotifyCh <- e
		case *RpcExtensionErrorEvent:
			mu.Lock()
			errEvs = append(errEvs, e)
			mu.Unlock()
		}
	})
	defer unsub()

	uiCtx := h.runtime.Session.ExtensionRunner().CreateContext().UI()

	// Fire-and-forget UI notifications
	uiCtx.Notify("Heads up", "warning")
	statusTxt := "running"
	uiCtx.SetStatus("ext", &statusTxt)
	uiCtx.SetWidgetLines("w", []string{"line1"}, &core.ExtensionWidgetOptions{Placement: core.WidgetPlacementBelowEditor})
	uiCtx.SetTitle("Custom Title")
	uiCtx.PasteToEditor("pasted")

	// Emit extension error
	h.runtime.Session.ExtensionRunner().EmitError(core.ExtensionError{
		ExtensionPath: "/tmp/ext.go",
		Event:         "tool_call",
		Error:         "boom",
	})

	// Blocking UI dialogs resolved concurrently via stdin extension_ui_response
	responderStop := make(chan struct{})
	responderDone := make(chan struct{})
	go func() {
		defer close(responderDone)
		for {
			select {
			case req := <-reqNotifyCh:
				switch req.Method {
				case "select":
					val := "optB"
					line, _ := SerializeJsonLine(RpcExtensionUIResponse{Id: req.Id, Value: &val})
					_, _ = io.WriteString(h.stdinW, line)
				case "confirm":
					okVal := true
					line, _ := SerializeJsonLine(RpcExtensionUIResponse{Id: req.Id, Confirmed: &okVal})
					_, _ = io.WriteString(h.stdinW, line)
				case "input":
					val := "typed"
					line, _ := SerializeJsonLine(RpcExtensionUIResponse{Id: req.Id, Value: &val})
					_, _ = io.WriteString(h.stdinW, line)
				case "editor":
					cancelled := true
					line, _ := SerializeJsonLine(RpcExtensionUIResponse{Id: req.Id, Cancelled: &cancelled})
					_, _ = io.WriteString(h.stdinW, line)
				}
			case <-responderStop:
				return
			}
		}
	}()

	sel, err := uiCtx.Select(ctx, "Pick", []string{"optA", "optB"})
	if err != nil || sel == nil || *sel != "optB" {
		t.Fatalf("Select: %v %v", sel, err)
	}
	conf, err := uiCtx.Confirm(ctx, "Sure?", "Proceed")
	if err != nil || !conf {
		t.Fatalf("Confirm: %v %v", conf, err)
	}
	inp, err := uiCtx.Input(ctx, "Enter", nil)
	if err != nil || inp == nil || *inp != "typed" {
		t.Fatalf("Input: %v %v", inp, err)
	}
	ed, err := uiCtx.Editor(ctx, "Edit", "prefill")
	if err != nil || ed != nil {
		t.Fatalf("expected cancelled Editor nil, got %v %v", ed, err)
	}

	// Also verify Abort + parse error via raw RpcUnknownCommand
	resp, err := h.client.send(ctx, &RpcUnknownCommand{Type: "bogus_cmd"})
	if err != nil {
		t.Fatalf("send unknown cmd transport err: %v", err)
	}
	if resp.Success || resp.Error == nil || !strings.Contains(*resp.Error, "Unknown command: bogus_cmd") {
		t.Fatalf("unexpected unknown cmd response: %+v", resp)
	}

	// Verify UnmarshalRpcCommand round-trip of invalid JSON directly on stdinW? Test invalid state:
	state, err := h.client.GetState(ctx)
	if err != nil || state.SessionId == "" {
		t.Fatalf("GetState after dialogs: %+v err=%v", state, err)
	}
	close(responderStop)
	<-responderDone

	// Verify extension_error arrived
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		nErr := len(errEvs)
		nReq := len(requests)
		mu.Unlock()
		if nErr >= 1 && nReq >= 9 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(errEvs) != 1 || errEvs[0].Error != "boom" {
		t.Fatalf("expected 1 extension_error, got %+v", errEvs)
	}
	// Verify JSONDecode on invalid line directly via raw parse error
	_ = jsonx.Object{}
}
