package core

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

type tcwRecordedCall struct {
	model   *ai.Model
	options *ai.ModelsSimpleStreamOptions
	ctx     context.Context
}

type tcwMockStreamClient struct {
	mu      sync.Mutex
	calls   []tcwRecordedCall
	respond func(ctx context.Context, model *ai.Model, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream
}

func (c *tcwMockStreamClient) StreamSimple(ctx context.Context, model *ai.Model, _ *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	c.mu.Lock()
	c.calls = append(c.calls, tcwRecordedCall{model: model, options: options, ctx: ctx})
	respond := c.respond
	c.mu.Unlock()
	if respond != nil {
		return respond(ctx, model, options)
	}
	s := ai.NewAssistantMessageEventStream()
	s.End(&ai.AssistantMessage{
		Role:       "assistant",
		Api:        ai.Api(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		StopReason: ai.StopReasonStop,
		Usage:      tcwWarmUsage(),
	})
	return s
}

type tcwMockSessionStore struct {
	mu          sync.Mutex
	branch      []SessionEntry
	appendCalls []tcwAppendCall
}

type tcwAppendCall struct {
	kind     string
	provider string
	model    string
	usage    ai.Usage
	note     string
}

func (s *tcwMockSessionStore) AppendUsage(kind, provider, model string, usage ai.Usage, note ...string) (*UsageEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var noteVal string
	var notePtr *string
	if len(note) > 0 && note[0] != "" {
		noteVal = note[0]
		notePtr = &noteVal
	}
	s.appendCalls = append(s.appendCalls, tcwAppendCall{
		kind:     kind,
		provider: provider,
		model:    model,
		usage:    usage,
		note:     noteVal,
	})
	return &UsageEntry{
		SessionEntryBase: SessionEntryBase{Type: "usage", Id: "usage-1"},
		Kind:             kind,
		Provider:         provider,
		Model:            model,
		Usage:            usage,
		Note:             notePtr,
	}, nil
}

func (s *tcwMockSessionStore) GetBranch(_ ...string) []SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branch
}

func tcwWarmUsage() ai.Usage {
	return ai.Usage{
		Input:       20,
		Output:      1,
		CacheRead:   100_000,
		CacheWrite:  0,
		TotalTokens: 100_021,
		Cost:        ai.UsageCost{Input: 0.0001, Output: 0.000025, CacheRead: 0.05, Total: 0.050125},
	}
}

func tcwModels() (adaptive, budget, openai, unknown *ai.Model) {
	short := 300
	long := 3600
	openaiLong := 86400
	adaptiveFlag := true
	adaptive = &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-opus-4-6",
			Name:     "Adaptive",
			Api:      "anthropic-messages",
			Provider: "anthropic",
			BaseUrl:  "https://api.anthropic.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
			},
		},
		Reasoning:     true,
		PromptCache:   &ai.ModelPromptCache{Short: &short, Long: &long},
		ContextWindow: 200_000,
		MaxTokens:     16_384,
		Compat:        ai.ModelCompatValue{V: &ai.AnthropicMessagesCompat{ForceAdaptiveThinking: &adaptiveFlag}},
	}
	budget = &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-sonnet-4-5",
			Name:     "Budget",
			Api:      "anthropic-messages",
			Provider: "anthropic",
			BaseUrl:  "https://api.anthropic.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
			},
		},
		Reasoning:     true,
		PromptCache:   &ai.ModelPromptCache{Short: &short, Long: &long},
		ContextWindow: 200_000,
		MaxTokens:     16_384,
	}
	openai = &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "gpt-5",
			Name:     "OpenAI",
			Api:      "openai-responses",
			Provider: "openai",
			BaseUrl:  "https://api.openai.com/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		PromptCache:   &ai.ModelPromptCache{Long: &openaiLong},
		ContextWindow: 400_000,
		MaxTokens:     16_384,
	}
	unknown = &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "unknown-cache",
			Name:     "Unknown",
			Api:      "openai-responses",
			Provider: "custom",
			BaseUrl:  "https://example.invalid",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128_000,
		MaxTokens:     4096,
	}
	return adaptive, budget, openai, unknown
}

func tcwBranchWithPrompt(tokens int) []SessionEntry {
	if tokens <= 0 {
		return nil
	}
	return []SessionEntry{
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", Id: "a1"},
			Message: &ai.AssistantMessage{
				Role:       "assistant",
				StopReason: ai.StopReasonStop,
				Usage:      ai.Usage{Input: tokens, TotalTokens: tokens},
			},
		},
	}
}

func tcwTriggerRefresh(t *testing.T, warmer *CacheWarmer, clock *int64, deltaMs int64) {
	t.Helper()
	*clock += deltaMs
	warmer.mu.Lock()
	run := warmer.run
	if run != nil && run.timer != nil {
		run.timer.Stop()
		run.timer = nil
	}
	warmer.mu.Unlock()
	if run == nil {
		t.Fatal("expected active cache-warming run")
	}
	warmer.refresh(run)
}

func TestCacheWarmingEligibilityReplayDeadlinesAndOverrides(t *testing.T) {
	adaptive, budget, openai, unknown := tcwModels()

	longRet := ai.CacheRetentionLong
	noneRet := ai.CacheRetentionNone
	ttlPtr := func(p *int64) int64 {
		if p == nil {
			return -1
		}
		return *p
	}
	if ttlPtr(ai.GetPromptCacheTtlMs(adaptive, nil)) != 300_000 ||
		ttlPtr(ai.GetPromptCacheTtlMs(adaptive, &ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{CacheRetention: &longRet}})) != 3_600_000 ||
		ttlPtr(ai.GetPromptCacheTtlMs(adaptive, &ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{CacheRetention: &noneRet}})) != -1 ||
		ttlPtr(ai.GetPromptCacheTtlMs(adaptive, &ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Env: ai.ProviderEnv{"PI_CACHE_RETENTION": "long"}}}})) != 3_600_000 ||
		ttlPtr(ai.GetPromptCacheTtlMs(openai, &ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{CacheRetention: &longRet}})) != 86_400_000 ||
		ttlPtr(ai.GetPromptCacheTtlMs(unknown, nil)) != -1 {
		t.Fatal("unexpected GetPromptCacheTtlMs matrix values")
	}
	if ttlPtr(ai.GetCacheWarmingDelayMs(300_000)) != 270_000 ||
		ttlPtr(ai.GetCacheWarmingDelayMs(60_000)) != 50_000 ||
		ttlPtr(ai.GetCacheWarmingDelayMs(10_000)) != -1 {
		t.Fatal("unexpected GetCacheWarmingDelayMs values")
	}

	med := ai.ThinkingLevel("medium")
	if ai.IsCacheWarmingReplayable(budget, &ai.SimpleStreamOptions{Reasoning: &med}) ||
		!ai.IsCacheWarmingReplayable(budget, nil) ||
		!ai.IsCacheWarmingReplayable(adaptive, &ai.SimpleStreamOptions{Reasoning: &med}) ||
		!ai.IsCacheWarmingReplayable(openai, &ai.SimpleStreamOptions{Reasoning: &med}) {
		t.Fatal("unexpected IsCacheWarmingReplayable values")
	}

	t.Run("replays profitable requests and preserves options across refreshes", func(t *testing.T) {
		var nowMs int64 = 1_000_000
		client := &tcwMockStreamClient{}
		store := &tcwMockSessionStore{branch: tcwBranchWithPrompt(100_000)}
		var events []CacheWarmingDecisionEvent
		warmedCh := make(chan *UsageEntry, 4)
		warmer := NewCacheWarmer(client, store, func() CacheWarmingMode { return "idle" }, func(_ context.Context, ev CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			events = append(events, ev)
			return ev.Action, nil
		})
		warmer.now = func() time.Time { return time.UnixMilli(nowMs) }
		warmer.OnWarmed = func(entry *UsageEntry) { warmedCh <- entry }
		defer warmer.Cancel()

		high := ai.ThinkingLevel("high")
		sessId := "s"
		calledTransform := false
		opts := &ai.ModelsSimpleStreamOptions{
			SimpleStreamOptions: ai.SimpleStreamOptions{
				Reasoning: &high,
				StreamOptions: ai.StreamOptions{
					SessionId: &sessId,
				},
			},
			ModelsRequestTransforms: ai.ModelsRequestTransforms{
				TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
					calledTransform = true
					return h, nil
				},
			},
		}
		warmer.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}, Options: opts}, func() bool { return true })
		tcwTriggerRefresh(t, warmer, &nowMs, 270_000)

		if len(client.calls) != 1 {
			t.Fatalf("expected 1 warm call, got %d", len(client.calls))
		}
		reqOpts := client.calls[0].options
		if reqOpts == nil || reqOpts.Reasoning == nil || *reqOpts.Reasoning != high ||
			reqOpts.SessionId == nil || *reqOpts.SessionId != "s" ||
			reqOpts.MaxTokens == nil || *reqOpts.MaxTokens != 1 ||
			reqOpts.MaxRetries == nil || *reqOpts.MaxRetries != 0 ||
			reqOpts.DisableContinuation == nil || !*reqOpts.DisableContinuation ||
			reqOpts.TransformHeaders == nil {
			t.Fatalf("unexpected warm request options: %+v", reqOpts)
		}
		_, _ = reqOpts.TransformHeaders(nil)
		if !calledTransform {
			t.Fatal("expected TransformHeaders preserved")
		}
		if len(events) != 1 || events[0].Action != ai.CacheWarmingActionWarm || math.Abs(events[0].MissCost-0.575) > 1e-6 || math.Abs(events[0].WarmCost-0.050025) > 1e-6 {
			t.Fatalf("unexpected cache warming decision event: %+v", events)
		}
		if len(store.appendCalls) != 1 || store.appendCalls[0].kind != "cache_warm" {
			t.Fatalf("expected 1 AppendUsage call, got %+v", store.appendCalls)
		}
		select {
		case warmedEntry := <-warmedCh:
			if warmedEntry == nil || warmedEntry.Kind != "cache_warm" {
				t.Fatalf("unexpected OnWarmed entry: %+v", warmedEntry)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for OnWarmed callback")
		}

		tcwTriggerRefresh(t, warmer, &nowMs, 270_000)
		if len(client.calls) != 2 {
			t.Fatalf("expected second refresh call, got %d", len(client.calls))
		}
	})

	t.Run("rejects refreshes after safe deadline or extension delay", func(t *testing.T) {
		var nowMs int64
		client := &tcwMockStreamClient{}
		store := &tcwMockSessionStore{branch: tcwBranchWithPrompt(100_000)}
		warmer := NewCacheWarmer(client, store, func() CacheWarmingMode { return "idle" })
		warmer.now = func() time.Time { return time.UnixMilli(nowMs) }
		defer warmer.Cancel()

		warmer.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, warmer, &nowMs, 285_001)
		st := warmer.Status()
		if len(client.calls) != 0 || st.State != "inactive" || st.Reason == nil || *st.Reason != "cache refresh deadline missed" {
			t.Fatalf("expected deadline miss stop, calls=%d status=%+v", len(client.calls), st)
		}

		nowMs = 0
		warmer2 := NewCacheWarmer(client, store, func() CacheWarmingMode { return "idle" }, func(_ context.Context, _ CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			nowMs = 285_001
			return ai.CacheWarmingActionWarm, nil
		})
		warmer2.now = func() time.Time { return time.UnixMilli(nowMs) }
		defer warmer2.Cancel()
		warmer2.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, warmer2, &nowMs, 0)
		st2 := warmer2.Status()
		if len(client.calls) != 0 || st2.State != "inactive" || st2.Reason == nil || *st2.Reason != "cache refresh deadline missed" {
			t.Fatalf("expected post-extension deadline miss, calls=%d status=%+v", len(client.calls), st2)
		}
	})

	t.Run("applies economic decisions extension overrides stop reasons and formatting", func(t *testing.T) {
		var nowMs int64
		unprofClient := &tcwMockStreamClient{}
		unprofStore := &tcwMockSessionStore{branch: tcwBranchWithPrompt(5_000)}
		unprof := NewCacheWarmer(unprofClient, unprofStore, func() CacheWarmingMode { return "idle" })
		unprof.now = func() time.Time { return time.UnixMilli(nowMs) }
		defer unprof.Cancel()
		unprof.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, unprof, &nowMs, 270_000)
		st := unprof.Status()
		if len(unprofClient.calls) != 0 || st.State != "inactive" || st.Decision == nil || st.Decision.Action != ai.CacheWarmingActionStop || !st.Decision.EconomicsAvailable || st.ExtensionOverride == nil || *st.ExtensionOverride {
			t.Fatalf("unexpected unprofitable status: calls=%d status=%+v", len(unprofClient.calls), st)
		}

		nowMs = 0
		forcedClient := &tcwMockStreamClient{}
		forcedStore := &tcwMockSessionStore{branch: tcwBranchWithPrompt(5_000)}
		forcedWarmedCh := make(chan *UsageEntry, 4)
		forced := NewCacheWarmer(forcedClient, forcedStore, func() CacheWarmingMode { return "idle" }, func(_ context.Context, _ CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			return ai.CacheWarmingActionWarm, nil
		})
		forced.now = func() time.Time { return time.UnixMilli(nowMs) }
		forced.OnWarmed = func(e *UsageEntry) { forcedWarmedCh <- e }
		defer forced.Cancel()
		forced.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, forced, &nowMs, 270_000)
		var forcedWarmed *UsageEntry
		select {
		case forcedWarmed = <-forcedWarmedCh:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for forced OnWarmed callback")
		}
		if len(forcedClient.calls) != 1 || forcedWarmed == nil || forcedWarmed.Note == nil || *forcedWarmed.Note != "extension override" {
			t.Fatalf("expected extension override warm entry, calls=%d warmed=%+v", len(forcedClient.calls), forcedWarmed)
		}

		nowMs = 0
		vetoClient := &tcwMockStreamClient{}
		vetoStore := &tcwMockSessionStore{branch: tcwBranchWithPrompt(100_000)}
		vetoed := NewCacheWarmer(vetoClient, vetoStore, func() CacheWarmingMode { return "idle" }, func(_ context.Context, _ CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			return ai.CacheWarmingActionStop, nil
		})
		vetoed.now = func() time.Time { return time.UnixMilli(nowMs) }
		defer vetoed.Cancel()
		vetoed.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, vetoed, &nowMs, 270_000)
		vst := vetoed.Status()
		if len(vetoClient.calls) != 0 || vst.State != "inactive" || vst.ExtensionOverride == nil || !*vst.ExtensionOverride {
			t.Fatalf("expected vetoed status, calls=%d status=%+v", len(vetoClient.calls), vst)
		}

		// Mode/lifetime/replayability/context/settled stop reasons.
		mode := CacheWarmingMode("off")
		stopClient := &tcwMockStreamClient{}
		stopStore := &tcwMockSessionStore{branch: tcwBranchWithPrompt(400_000)}
		w := NewCacheWarmer(stopClient, stopStore, func() CacheWarmingMode { return mode })
		defer w.Cancel()
		w.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		if w.Status().Reason == nil || *w.Status().Reason != "cache warming disabled" {
			t.Fatalf("expected cache warming disabled, got %+v", w.Status())
		}
		mode = "idle"
		w.Start(CacheWarmRequest{Model: unknown, Context: &ai.Context{}}, func() bool { return true })
		if w.Status().Reason == nil || *w.Status().Reason != "cache lifetime unavailable" {
			t.Fatalf("expected cache lifetime unavailable, got %+v", w.Status())
		}
		high := ai.ThinkingLevel("high")
		w.Start(CacheWarmRequest{Model: budget, Context: &ai.Context{}, Options: &ai.ModelsSimpleStreamOptions{SimpleStreamOptions: ai.SimpleStreamOptions{Reasoning: &high}}}, func() bool { return true })
		if w.Status().Reason == nil || *w.Status().Reason != "request cannot be replayed safely" {
			t.Fatalf("expected request cannot be replayed safely, got %+v", w.Status())
		}
		stillCurrent := true
		w.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return stillCurrent })
		stillCurrent = false
		if w.Status().Reason == nil || *w.Status().Reason != "conversation context changed" {
			t.Fatalf("expected conversation context changed, got %+v", w.Status())
		}
		mode = "streaming"
		w.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		w.OnAgentSettled()
		if w.Status().Reason == nil || *w.Status().Reason != "agent run settled" {
			t.Fatalf("expected agent run settled, got %+v", w.Status())
		}

		// Failed response must not record AppendUsage.
		errMsg := "boom"
		errClient := &tcwMockStreamClient{respond: func(_ context.Context, m *ai.Model, _ *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
			s := ai.NewAssistantMessageEventStream()
			s.End(&ai.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonError, ErrorMessage: &errMsg})
			return s
		}}
		errStore := &tcwMockSessionStore{branch: tcwBranchWithPrompt(100_000)}
		nowMs = 0
		errWarmer := NewCacheWarmer(errClient, errStore, func() CacheWarmingMode { return "idle" })
		errWarmer.now = func() time.Time { return time.UnixMilli(nowMs) }
		defer errWarmer.Cancel()
		errWarmer.Start(CacheWarmRequest{Model: adaptive, Context: &ai.Context{}}, func() bool { return true })
		tcwTriggerRefresh(t, errWarmer, &nowMs, 270_000)
		if len(errStore.appendCalls) != 0 {
			t.Fatalf("expected zero AppendUsage calls on failed refresh, got %d", len(errStore.appendCalls))
		}

		// Formatting helpers parity.
		nextWarmAt := int64(222_000)
		decision := &ai.CacheWarmingDecision{
			Phase:                   ai.CacheWarmingPhaseIdle,
			WarmCost:                0.013,
			MissCost:                0.621,
			ContinuationProbability: 0.6,
			ExpectedSavings:         0.36,
			EconomicsAvailable:      true,
			Action:                  ai.CacheWarmingActionWarm,
		}
		gotStatus := FormatCacheWarmingStatus(CacheWarmingStatus{
			State:      "scheduled",
			NextWarmAt: &nextWarmAt,
			Decision:   decision,
		}, 0)
		const wantStatus = "Decision in 3m 42s (60% continuation probability, expected savings $0.360 >= $0.050 -> warm)"
		if gotStatus != wantStatus {
			t.Fatalf("unexpected FormatCacheWarmingStatus:\nwant: %s\ngot:  %s", wantStatus, gotStatus)
		}
		gotUsage := FormatCacheWarmingUsage(0.02949725, "extension override")
		if gotUsage != "Cache warmed (extension override): $0.029497" && !strings.Contains(gotUsage, "Cache warmed (extension override):") {
			t.Fatalf("unexpected FormatCacheWarmingUsage: %q", gotUsage)
		}
		if gotUsage != "Cache warmed (extension override): $0.029497" {
			t.Fatalf("expected exact rounded FormatCacheWarmingUsage, got %q", gotUsage)
		}
	})
}
