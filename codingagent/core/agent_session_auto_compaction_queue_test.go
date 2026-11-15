package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

type tasacqResourceLoader struct{}

func (*tasacqResourceLoader) Reload(context.Context, ...*ResourceLoaderReloadOptions) error {
	return nil
}
func (*tasacqResourceLoader) GetExtensions() LoadExtensionsResult {
	return LoadExtensionsResult{}
}
func (*tasacqResourceLoader) GetSkills() LoadSkillsResult {
	return LoadSkillsResult{}
}
func (*tasacqResourceLoader) GetPrompts() LoadPromptTemplatesResult {
	return LoadPromptTemplatesResult{}
}
func (*tasacqResourceLoader) GetThemes() LoadThemesResult {
	return LoadThemesResult{}
}
func (*tasacqResourceLoader) GetAgentsFiles() LoadContextFilesResult {
	return LoadContextFilesResult{}
}
func (*tasacqResourceLoader) GetSystemPrompt() *string                   { return nil }
func (*tasacqResourceLoader) GetSystemPromptSource() *SystemPromptSource { return nil }
func (*tasacqResourceLoader) GetAppendSystemPrompt() []string            { return nil }
func (*tasacqResourceLoader) GetAppendSystemPromptSources() []SystemPromptSource {
	return nil
}
func (*tasacqResourceLoader) ExtendResources(ResourceExtensionPaths) {}
func (*tasacqResourceLoader) GetPathMetadata() map[string]utils.PathMetadata {
	return map[string]utils.PathMetadata{}
}

type tasacqHarness struct {
	session *AgentSession
	faux    *providers.FauxProviderRegistration
	cleanup func()
}

func tasacqIntPtr(v int) *int           { return &v }
func tasacqBoolPtr(v bool) *bool        { return &v }
func tasacqFloatPtr(v float64) *float64 { return &v }
func tasacqStrPtr(v string) *string     { return &v }

func tasacqSetupSession(t *testing.T, contextWindow int, compactionSettings *SettingsCompactionSettings) *tasacqHarness {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()

	faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-1",
				ContextWindow: tasacqIntPtr(contextWindow),
			},
		},
	})

	authStorage := AuthStorageInMemory()
	key := "faux-key"
	if _, err := authStorage.Modify(ctx, string(faux.GetModel().Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil); err != nil {
		faux.Unregister()
		t.Fatalf("AuthStorage.Modify failed: %v", err)
	}

	settings := &Settings{
		Compaction: compactionSettings,
	}
	settingsManager := SettingsManagerInMemory(settings)
	sessionManager := SessionManagerInMemory(tmpDir)

	emptyModelsPath := ""
	mrRuntime, err := CreateModelRuntime(ctx, &CreateModelRuntimeOptions{
		Credentials: authStorage,
		ModelsPath:  &emptyModelsPath,
		ModelsStore: NewInMemoryCodingAgentModelsStore(),
	})
	if err != nil {
		faux.Unregister()
		t.Fatalf("CreateModelRuntime failed: %v", err)
	}
	modelRegistry := NewModelRegistry(mrRuntime)
	m := faux.GetModel()
	apiStr := string(m.Api)
	baseUrl := m.BaseUrl
	if err := modelRegistry.RegisterProvider(string(m.Provider), ProviderConfigInput{
		BaseUrl: &baseUrl,
		ApiKey:  &key,
		Api:     &apiStr,
		Models: []ProviderModelConfig{
			&ProviderChatModelConfig{
				ProviderModelConfigBase: ProviderModelConfigBase{
					Id:      m.Id,
					Name:    m.Name,
					Api:     &apiStr,
					BaseUrl: &baseUrl,
					Input:   m.Input,
					Cost:    m.Cost,
				},
				Reasoning:     m.Reasoning,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxTokens,
			},
		},
	}); err != nil {
		mrRuntime.Close()
		faux.Unregister()
		t.Fatalf("RegisterProvider failed: %v", err)
	}

	res, err := CreateAgentSession(ctx, &CreateAgentSessionOptions{
		Cwd:               tmpDir,
		AgentDir:          tmpDir,
		AuthStorage:       authStorage,
		ModelRegistry:     modelRegistry,
		ModelRuntime:      mrRuntime,
		SettingsManager:   settingsManager,
		SessionManager:    sessionManager,
		ResourceLoader:    &tasacqResourceLoader{},
		Model:             m,
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		NoContextFiles:    true,
	})
	if err != nil {
		modelRegistry.UnregisterProvider(string(m.Provider))
		mrRuntime.Close()
		faux.Unregister()
		t.Fatalf("CreateAgentSession failed: %v", err)
	}

	return &tasacqHarness{
		session: res.Session,
		faux:    faux,
		cleanup: func() {
			res.Session.Dispose()
			modelRegistry.UnregisterProvider(string(m.Provider))
			mrRuntime.Close()
			faux.Unregister()
		},
	}
}

func tasacqMakeAssistant(model *ai.Model, stopReason ai.StopReason, totalTokens int, errorMsg string, ts int64) *ai.AssistantMessage {
	usage := ai.Usage{
		Input:       totalTokens,
		TotalTokens: totalTokens,
	}
	if stopReason == ai.StopReasonError {
		usage = ai.Usage{}
	}
	var errPtr *string
	if errorMsg != "" {
		errPtr = &errorMsg
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList{&ai.TextContent{Type: "text", Text: strings.Repeat("y", 400)}},
		Api:          ai.Api(model.Api),
		Provider:     model.Provider,
		Model:        model.Id,
		Usage:        usage,
		StopReason:   stopReason,
		ErrorMessage: errPtr,
		Timestamp:    ts,
	}
}

func tasacqMakeUser(text string, ts int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: []ai.Content{&ai.TextContent{Type: "text", Text: text}}},
		Timestamp: ts,
	}
}

func tasacqSeedCompactableHistory(t *testing.T, s *AgentSession, firstTokens, secondTokens int) (firstEntryId string) {
	t.Helper()
	now := time.Now().UnixMilli()
	u1 := tasacqMakeUser(strings.Repeat("a", 500), now-4000)
	a1 := tasacqMakeAssistant(s.Model(), ai.StopReasonStop, firstTokens, "", now-3000)
	u2 := tasacqMakeUser(strings.Repeat("b", 500), now-2000)
	a2 := tasacqMakeAssistant(s.Model(), ai.StopReasonStop, secondTokens, "", now-1000)

	var err error
	firstEntryId, err = s.SessionManager.AppendMessage(u1)
	if err != nil {
		t.Fatalf("AppendMessage(u1): %v", err)
	}
	for _, msg := range []agent.AgentMessage{a1, u2, a2} {
		if _, err := s.SessionManager.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	proj := s.SessionManager.BuildSessionProjection()
	s.Agent.ReplaceMessages(proj.Messages)
	return firstEntryId
}

func TestAgentSessionAutoCompactionQueue(t *testing.T) {
	ctx := context.Background()

	t.Run("should resume after threshold compaction when only agent-level queued messages exist", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			KeepRecentTokens: tasacqFloatPtr(200),
		})
		defer h.cleanup()

		tasacqSeedCompactableHistory(t, h.session, 1000, 190_000)

		compactionStarted := make(chan struct{})
		releaseCompaction := make(chan struct{})
		resumedAnswerCalled := make(chan struct{})

		h.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(compactionStarted)
				<-releaseCompaction
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Compaction summary"), nil), nil
			}),
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(resumedAnswerCalled)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Followed up response"), nil), nil
			}),
		})

		type autoResult struct {
			didRetry bool
			err      error
		}
		doneCh := make(chan autoResult, 1)
		go func() {
			didRetry, err := h.session.as2RunAutoCompaction(ctx, "threshold", false)
			doneCh <- autoResult{didRetry: didRetry, err: err}
		}()

		select {
		case <-compactionStarted:
		case res := <-doneCh:
			t.Fatalf("as2RunAutoCompaction returned early before calling LLM: didRetry=%v err=%v", res.didRetry, res.err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for compaction LLM call")
		}
		if !h.session.IsCompacting() {
			t.Fatalf("expected IsCompacting to be true while threshold compaction runs")
		}

		// Enqueue directly on Agent (simulating agent-level queued messages without session wrapper).
		h.session.Agent.FollowUp(tasacqMakeUser("agent-queued follow-up", time.Now().UnixMilli()))
		close(releaseCompaction)

		res := <-doneCh
		if res.err != nil {
			t.Fatalf("as2RunAutoCompaction error: %v", res.err)
		}
		if !res.didRetry {
			t.Fatalf("expected as2RunAutoCompaction to report resume/retry=true when agent-level follow-up is queued")
		}
		if !h.session.Agent.HasQueuedMessages() {
			t.Fatalf("expected agent queue to still hold follow-up until caller calls Continue")
		}
		select {
		case <-resumedAnswerCalled:
			t.Fatalf("did not expect as2RunAutoCompaction itself to invoke Continue")
		default:
		}

		// Caller (as1HandlePostAgentRun) resumes agent loop with Continue.
		if err := h.session.Agent.Continue(ctx); err != nil {
			t.Fatalf("Agent.Continue error: %v", err)
		}
		if err := h.session.WaitForIdle(ctx); err != nil {
			t.Fatalf("WaitForIdle error: %v", err)
		}
		select {
		case <-resumedAnswerCalled:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for resumed assistant response after compaction")
		}

		if h.session.Agent.HasQueuedMessages() {
			t.Fatalf("expected agent queue to be drained after compaction resume")
		}
		lastText := h.session.GetLastAssistantText()
		if lastText == nil || *lastText != "Followed up response" {
			t.Fatalf("unexpected last assistant text: %v", lastText)
		}
	})

	t.Run("should not compact repeatedly after overflow recovery already attempted", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			KeepRecentTokens: tasacqFloatPtr(200),
		})
		defer h.cleanup()

		tasacqSeedCompactableHistory(t, h.session, 1000, 2000)

		var compactionStarts atomic.Int32
		var compactionErrMsgs []string
		h.session.Subscribe(func(ev AgentSessionEvent) {
			switch e := ev.(type) {
			case *AgentSessionEventCompactionStart:
				compactionStarts.Add(1)
			case *AgentSessionEventCompactionEnd:
				if e.ErrorMessage != nil {
					compactionErrMsgs = append(compactionErrMsgs, *e.ErrorMessage)
				}
			}
		})

		// Overflow compaction omits the errored overflow message, creating a split turn
		// that issues both a history summary and a turn-prefix summary call.
		h.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Compaction summary"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn prefix summary"), nil),
			},
		})

		now := time.Now().UnixMilli()
		u3 := tasacqMakeUser(strings.Repeat("c", 900), now-20)
		overflow1 := tasacqMakeAssistant(h.session.Model(), ai.StopReasonError, 0, "prompt is too long", now)
		if _, err := h.session.SessionManager.AppendMessage(u3); err != nil {
			t.Fatalf("AppendMessage(u3): %v", err)
		}
		if _, err := h.session.SessionManager.AppendMessage(overflow1); err != nil {
			t.Fatalf("AppendMessage(overflow1): %v", err)
		}
		willRetry, err := h.session.as2CheckCompaction(ctx, overflow1, false)
		if err != nil {
			t.Fatalf("first as2CheckCompaction error: %v", err)
		}
		if !willRetry {
			t.Fatalf("expected first overflow compaction to set willRetry=true")
		}

		overflow2 := tasacqMakeAssistant(h.session.Model(), ai.StopReasonError, 0, "prompt is too long", now+10)
		willRetry2, err := h.session.as2CheckCompaction(ctx, overflow2, false)
		if err != nil {
			t.Fatalf("second as2CheckCompaction error: %v", err)
		}
		if willRetry2 {
			t.Fatalf("expected second overflow check to not retry again")
		}
		if got := compactionStarts.Load(); got != 1 {
			t.Fatalf("expected 1 compaction_start, got %d", got)
		}
		if len(compactionErrMsgs) == 0 || !strings.Contains(compactionErrMsgs[0], "Context overflow recovery failed") {
			t.Fatalf("expected overflow recovery failure message, got %v", compactionErrMsgs)
		}
	})

	t.Run("should ignore stale pre-compaction assistant usage on pre-prompt compaction checks", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			ReserveTokens:    tasacqFloatPtr(16384),
			KeepRecentTokens: tasacqFloatPtr(100),
		})
		defer h.cleanup()

		now := time.Now().UnixMilli()
		u1 := tasacqMakeUser("before compaction", now-10_000)
		keptAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonStop, 190_000, "", now-9_000)
		firstKeptId, err := h.session.SessionManager.AppendMessage(u1)
		if err != nil {
			t.Fatalf("AppendMessage(u1): %v", err)
		}
		if _, err := h.session.SessionManager.AppendMessage(keptAssistant); err != nil {
			t.Fatalf("AppendMessage(keptAssistant): %v", err)
		}
		if _, err := h.session.SessionManager.AppendCompaction("prior summary", &firstKeptId, 190_000, &AppendCompactionOptions{FromHook: tasacqBoolPtr(false)}); err != nil {
			t.Fatalf("AppendCompaction: %v", err)
		}
		h.session.Agent.ReplaceMessages(h.session.SessionManager.BuildSessionProjection().Messages)

		var compactionStarts atomic.Int32
		h.session.Subscribe(func(ev AgentSessionEvent) {
			if _, ok := ev.(*AgentSessionEventCompactionStart); ok {
				compactionStarts.Add(1)
			}
		})

		// Calling as2CheckCompaction(skipAbortedCheck=true) with keptAssistant (pre-compaction)
		// must NOT trigger threshold compaction again because keptAssistant precedes the compaction entry.
		triggered, err := h.session.as2CheckCompaction(ctx, keptAssistant, true)
		if err != nil {
			t.Fatalf("as2CheckCompaction error: %v", err)
		}
		if triggered {
			t.Fatalf("expected stale pre-compaction assistant usage not to trigger compaction")
		}
		if compactionStarts.Load() != 0 {
			t.Fatalf("expected 0 compaction_start events, got %d", compactionStarts.Load())
		}
	})

	t.Run("should trigger threshold compaction for error messages using last successful usage", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			ReserveTokens:    tasacqFloatPtr(16384),
			KeepRecentTokens: tasacqFloatPtr(1),
		})
		defer h.cleanup()

		now := time.Now().UnixMilli()
		u1 := tasacqMakeUser("hello", now-2000)
		succAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonStop, 190_000, "", now-1500)
		u2 := tasacqMakeUser("retry", now-1000)
		errAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonError, 0, "529 overloaded", now-500)
		for _, m := range []agent.AgentMessage{u1, succAssistant, u2, errAssistant} {
			if _, err := h.session.SessionManager.AppendMessage(m); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
		}
		h.session.Agent.ReplaceMessages(h.session.SessionManager.BuildSessionProjection().Messages)

		var reasons []string
		h.session.Subscribe(func(ev AgentSessionEvent) {
			if cs, ok := ev.(*AgentSessionEventCompactionStart); ok {
				reasons = append(reasons, cs.Reason)
			}
		})

		h.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Threshold summary"), nil),
			},
		})

		didRetry, err := h.session.as2CheckCompaction(ctx, errAssistant, false)
		if err != nil {
			t.Fatalf("as2CheckCompaction error: %v", err)
		}
		if didRetry {
			t.Fatalf("expected threshold compaction on error message without queued follow-up not to trigger agent retry")
		}
		if len(reasons) != 1 || reasons[0] != "threshold" {
			t.Fatalf("expected [threshold] compaction_start, got %v", reasons)
		}
	})

	t.Run("should not trigger threshold compaction for error messages when no prior usage exists", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			ReserveTokens:    tasacqFloatPtr(16384),
			KeepRecentTokens: tasacqFloatPtr(1),
		})
		defer h.cleanup()

		now := time.Now().UnixMilli()
		u1 := tasacqMakeUser("hello", now-1000)
		errAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonError, 0, "529 overloaded", now)
		for _, m := range []agent.AgentMessage{u1, errAssistant} {
			if _, err := h.session.SessionManager.AppendMessage(m); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
		}
		h.session.Agent.ReplaceMessages(h.session.SessionManager.BuildSessionProjection().Messages)

		var compactionStarts atomic.Int32
		h.session.Subscribe(func(ev AgentSessionEvent) {
			if _, ok := ev.(*AgentSessionEventCompactionStart); ok {
				compactionStarts.Add(1)
			}
		})

		if _, err := h.session.as2CheckCompaction(ctx, errAssistant, false); err != nil {
			t.Fatalf("as2CheckCompaction error: %v", err)
		}
		if compactionStarts.Load() != 0 {
			t.Fatalf("expected 0 compaction_start events, got %d", compactionStarts.Load())
		}
	})

	t.Run("should not trigger threshold compaction for error messages when only kept pre-compaction usage exists", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			ReserveTokens:    tasacqFloatPtr(16384),
			KeepRecentTokens: tasacqFloatPtr(1),
		})
		defer h.cleanup()

		preTs := time.Now().UnixMilli() - 10_000
		u1 := tasacqMakeUser("before compaction", preTs-1000)
		keptAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonStop, 190_000, "", preTs)
		firstKeptId, err := h.session.SessionManager.AppendMessage(u1)
		if err != nil {
			t.Fatalf("AppendMessage(u1): %v", err)
		}
		if _, err := h.session.SessionManager.AppendMessage(keptAssistant); err != nil {
			t.Fatalf("AppendMessage(keptAssistant): %v", err)
		}
		if _, err := h.session.SessionManager.AppendCompaction("summary", &firstKeptId, 190_000, &AppendCompactionOptions{FromHook: tasacqBoolPtr(false)}); err != nil {
			t.Fatalf("AppendCompaction: %v", err)
		}
		u2 := tasacqMakeUser("new prompt", preTs+5000)
		errAssistant := tasacqMakeAssistant(h.session.Model(), ai.StopReasonError, 0, "529 overloaded", preTs+6000)
		if _, err := h.session.SessionManager.AppendMessage(u2); err != nil {
			t.Fatalf("AppendMessage(u2): %v", err)
		}
		if _, err := h.session.SessionManager.AppendMessage(errAssistant); err != nil {
			t.Fatalf("AppendMessage(errAssistant): %v", err)
		}
		h.session.Agent.ReplaceMessages(h.session.SessionManager.BuildSessionProjection().Messages)

		var compactionStarts atomic.Int32
		h.session.Subscribe(func(ev AgentSessionEvent) {
			if _, ok := ev.(*AgentSessionEventCompactionStart); ok {
				compactionStarts.Add(1)
			}
		})

		if _, err := h.session.as2CheckCompaction(ctx, errAssistant, false); err != nil {
			t.Fatalf("as2CheckCompaction error: %v", err)
		}
		if compactionStarts.Load() != 0 {
			t.Fatalf("expected 0 compaction_start events, got %d", compactionStarts.Load())
		}
	})

	t.Run("does not trigger threshold compaction below threshold or when disabled", func(t *testing.T) {
		below := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:       tasacqBoolPtr(true),
			ReserveTokens: tasacqFloatPtr(1000),
		})
		defer below.cleanup()

		disabled := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled: tasacqBoolPtr(false),
		})
		defer disabled.cleanup()

		var belowStarts, disabledStarts atomic.Int32
		below.session.Subscribe(func(ev AgentSessionEvent) {
			if _, ok := ev.(*AgentSessionEventCompactionStart); ok {
				belowStarts.Add(1)
			}
		})
		disabled.session.Subscribe(func(ev AgentSessionEvent) {
			if _, ok := ev.(*AgentSessionEventCompactionStart); ok {
				disabledStarts.Add(1)
			}
		})

		now := time.Now().UnixMilli()
		belowMsg := tasacqMakeAssistant(below.session.Model(), ai.StopReasonStop, 1_000, "", now)
		if _, err := below.session.SessionManager.AppendMessage(tasacqMakeUser("hi", now-100)); err != nil {
			t.Fatal(err)
		}
		if _, err := below.session.SessionManager.AppendMessage(belowMsg); err != nil {
			t.Fatal(err)
		}
		below.session.Agent.ReplaceMessages(below.session.SessionManager.BuildSessionProjection().Messages)
		if _, err := below.session.as2CheckCompaction(ctx, belowMsg, false); err != nil {
			t.Fatalf("below checkCompaction error: %v", err)
		}

		hugeMsg := tasacqMakeAssistant(disabled.session.Model(), ai.StopReasonStop, 1_000_000, "", now)
		if _, err := disabled.session.SessionManager.AppendMessage(tasacqMakeUser("hi", now-100)); err != nil {
			t.Fatal(err)
		}
		if _, err := disabled.session.SessionManager.AppendMessage(hugeMsg); err != nil {
			t.Fatal(err)
		}
		disabled.session.Agent.ReplaceMessages(disabled.session.SessionManager.BuildSessionProjection().Messages)
		if _, err := disabled.session.as2CheckCompaction(ctx, hugeMsg, false); err != nil {
			t.Fatalf("disabled checkCompaction error: %v", err)
		}

		if belowStarts.Load() != 0 {
			t.Fatalf("expected 0 compaction_start events below threshold, got %d", belowStarts.Load())
		}
		if disabledStarts.Load() != 0 {
			t.Fatalf("expected 0 compaction_start events when disabled, got %d", disabledStarts.Load())
		}
	})

	t.Run("compacts successful overflow responses without retrying", func(t *testing.T) {
		h := tasacqSetupSession(t, 200_000, &SettingsCompactionSettings{
			Enabled:          tasacqBoolPtr(true),
			KeepRecentTokens: tasacqFloatPtr(1),
		})
		defer h.cleanup()

		tasacqSeedCompactableHistory(t, h.session, 1000, 195_000)

		var starts []*AgentSessionEventCompactionStart
		h.session.Subscribe(func(ev AgentSessionEvent) {
			if cs, ok := ev.(*AgentSessionEventCompactionStart); ok {
				starts = append(starts, cs)
			}
		})

		h.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Overflow summary"), nil),
			},
		})

		now := time.Now().UnixMilli()
		overflowStop := tasacqMakeAssistant(h.session.Model(), ai.StopReasonStop, 210_000, "", now)
		if _, err := h.session.SessionManager.AppendMessage(overflowStop); err != nil {
			t.Fatal(err)
		}
		h.session.Agent.ReplaceMessages(h.session.SessionManager.BuildSessionProjection().Messages)

		willRetry, err := h.session.as2CheckCompaction(ctx, overflowStop, false)
		if err != nil {
			t.Fatalf("as2CheckCompaction error: %v", err)
		}
		if willRetry {
			t.Fatalf("expected successful overflow stop response not to retry")
		}
		if len(starts) != 1 || starts[0].Reason != "overflow" {
			t.Fatalf("expected [overflow] compaction_start, got %+v", starts)
		}
	})
}
