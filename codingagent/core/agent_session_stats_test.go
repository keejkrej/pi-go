// Ported from packages/coding-agent/test/agent-session-stats.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
)

func tasstModel() *ai.Model {
	return compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
}

func tasstCreateUsage(totalTokens int) ai.Usage {
	return ai.Usage{
		Input:       totalTokens,
		Output:      0,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: totalTokens,
		Cost: ai.UsageCost{
			Input:      0,
			Output:     0,
			CacheRead:  0,
			CacheWrite: 0,
			Total:      0,
		},
	}
}

func tasstCreateAssistantMessage(m *ai.Model, text string, totalTokens int, timestamp int64) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		Api:        ai.Api(m.Api),
		Provider:   m.Provider,
		Model:      m.Id,
		Usage:      tasstCreateUsage(totalTokens),
		StopReason: ai.StopReasonStop,
		Timestamp:  timestamp,
	}
}

func tasstCreateUserMessage(text string, timestamp int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: timestamp,
	}
}

func tasstCreateToolResultMessage(usage ai.Usage) *ai.ToolResultMessage {
	u := usage
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "tool-call-1",
		ToolName:   "test_tool",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "tool result"}},
		Usage:      &u,
		IsError:    false,
		Timestamp:  1,
	}
}

func tasstCreateSession(t *testing.T) (*core.AgentSession, *core.SessionManager) {
	t.Helper()
	ctx := context.Background()
	model := tasstModel()
	if model == nil {
		t.Fatalf("GetModel(anthropic, claude-sonnet-4-5) returned nil")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	settingsManager := core.SettingsManagerInMemory()
	sessionManager := core.SessionManagerInMemory()
	authStorage := core.AuthStorageInMemory()
	_, err = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
		key := "test-key"
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil)
	if err != nil {
		t.Fatalf("authStorage.Modify: %v", err)
	}
	registry, err := testkit.CreateInMemoryModelRegistry(ctx, authStorage)
	if err != nil {
		t.Fatalf("CreateInMemoryModelRegistry: %v", err)
	}
	runtime, err := testkit.GetModelRuntime(registry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
		StreamFn: func(streamCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			var msgs ai.MessageList
			if tc != nil {
				msgs = tc.Messages
			}
			return compat.StreamSimple(streamCtx, model, &ai.Context{Messages: msgs}, options), nil
		},
		InitialState: &agent.AgentInitialState{
			Model:         model,
			SystemPrompt:  "You are a helpful assistant.",
			Tools:         []*agent.AgentTool{},
			ThinkingLevel: agent.ThinkingLevelHigh,
		},
	})
	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           ag,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Cwd:             cwd,
		ModelRuntime:    runtime,
		ResourceLoader:  testkit.CreateTestResourceLoader(),
	})
	return session, sessionManager
}

func tasstSyncAgentMessages(session *core.AgentSession, sessionManager *core.SessionManager) {
	session.Agent.ReplaceMessages(sessionManager.BuildSessionContext().Messages)
}

func tasstCloseFloat(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestAgentSessionGetSessionStats(t *testing.T) {
	model := tasstModel()
	if model == nil {
		t.Fatalf("GetModel(anthropic, claude-sonnet-4-5) returned nil")
	}

	t.Run("exposes the current context usage alongside token totals", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		if _, err := sessionManager.AppendMessage(tasstCreateUserMessage("hello", 1)); err != nil {
			t.Fatalf("AppendMessage user: %v", err)
		}
		if _, err := sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "hi", 200, 2)); err != nil {
			t.Fatalf("AppendMessage assistant: %v", err)
		}
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		cu := session.GetContextUsage()
		if stats.ContextUsage == nil || cu == nil {
			t.Fatalf("expected non-nil ContextUsage, stats=%#v cu=%#v", stats.ContextUsage, cu)
		}
		if tok, ok := stats.ContextUsage.Tokens.Get(); !ok || tok != 200 {
			t.Fatalf("ContextUsage.Tokens = (%v, %v), want 200", tok, ok)
		}
		if stats.ContextUsage.ContextWindow != model.ContextWindow {
			t.Fatalf("ContextWindow = %d, want %d", stats.ContextUsage.ContextWindow, model.ContextWindow)
		}
		wantPct := (200.0 / float64(model.ContextWindow)) * 100.0
		if pct, ok := stats.ContextUsage.Percent.Get(); !ok || !tasstCloseFloat(pct, wantPct) {
			t.Fatalf("ContextUsage.Percent = (%v, %v), want %v", pct, ok, wantPct)
		}
	})

	t.Run("reports unknown current context usage immediately after compaction", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("first", 1))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response1", 180_000, 2))
		keptUserId, _ := sessionManager.AppendMessage(tasstCreateUserMessage("second", 3))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response2", 195_000, 4))
		_, _ = sessionManager.AppendCompaction("summary", &keptUserId, 195_000)
		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("third", 5))
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		if stats.Tokens.Input != 375_000 {
			t.Fatalf("Tokens.Input = %d, want 375000", stats.Tokens.Input)
		}
		if stats.ContextUsage == nil {
			t.Fatalf("expected defined ContextUsage")
		}
		if !stats.ContextUsage.Tokens.IsNull() {
			t.Fatalf("expected null ContextUsage.Tokens after compaction, got %#v", stats.ContextUsage.Tokens)
		}
		if !stats.ContextUsage.Percent.IsNull() {
			t.Fatalf("expected null ContextUsage.Percent after compaction, got %#v", stats.ContextUsage.Percent)
		}
	})

	t.Run("uses post-compaction usage for current context instead of stale kept usage", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("first", 1))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response1", 180_000, 2))
		keptUserId, _ := sessionManager.AppendMessage(tasstCreateUserMessage("second", 3))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response2", 195_000, 4))
		_, _ = sessionManager.AppendCompaction("summary", &keptUserId, 195_000)
		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("third", 5))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response3", 25_000, 6))
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		if stats.Tokens.Input != 400_000 {
			t.Fatalf("Tokens.Input = %d, want 400000", stats.Tokens.Input)
		}
		if stats.ContextUsage == nil {
			t.Fatalf("expected defined ContextUsage")
		}
		if tok, ok := stats.ContextUsage.Tokens.Get(); !ok || tok != 25_000 {
			t.Fatalf("ContextUsage.Tokens = (%v, %v), want 25000", tok, ok)
		}
		wantPct := (25_000.0 / float64(model.ContextWindow)) * 100.0
		if pct, ok := stats.ContextUsage.Percent.Get(); !ok || !tasstCloseFloat(pct, wantPct) {
			t.Fatalf("ContextUsage.Percent = (%v, %v), want %v", pct, ok, wantPct)
		}
	})

	t.Run("includes branch summary usage in session totals", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		u := ai.Usage{
			Input:       10,
			Output:      20,
			CacheRead:   30,
			CacheWrite:  40,
			TotalTokens: 100,
			Cost:        ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		_, err := sessionManager.BranchWithSummary(nil, "summary", &core.BranchWithSummaryOptions{Usage: &u})
		if err != nil {
			t.Fatalf("BranchWithSummary: %v", err)
		}
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		wantTokens := core.SessionStatsTokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100}
		if stats.Tokens != wantTokens {
			t.Fatalf("Tokens = %#v, want %#v", stats.Tokens, wantTokens)
		}
		if !tasstCloseFloat(stats.Cost, 1) {
			t.Fatalf("Cost = %v, want 1", stats.Cost)
		}
	})

	t.Run("includes compaction usage in session totals", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		firstKeptEntryId, _ := sessionManager.AppendMessage(tasstCreateUserMessage("hello", 1))
		u := ai.Usage{
			Input:       10,
			Output:      20,
			CacheRead:   30,
			CacheWrite:  40,
			TotalTokens: 100,
			Cost:        ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		if _, err := sessionManager.AppendCompaction("summary", &firstKeptEntryId, 100, &core.AppendCompactionOptions{Usage: &u}); err != nil {
			t.Fatalf("AppendCompaction: %v", err)
		}
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		wantTokens := core.SessionStatsTokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100}
		if stats.Tokens != wantTokens {
			t.Fatalf("Tokens = %#v, want %#v", stats.Tokens, wantTokens)
		}
		if !tasstCloseFloat(stats.Cost, 1) {
			t.Fatalf("Cost = %v, want 1", stats.Cost)
		}
	})

	t.Run("includes cache-warming usage exactly once without adding messages", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		if _, err := sessionManager.AppendUsage(
			"cache_warm",
			"anthropic",
			model.Id,
			ai.Usage{
				Input:       2,
				Output:      1,
				CacheRead:   97,
				CacheWrite:  0,
				TotalTokens: 100,
				Cost:        ai.UsageCost{Input: 0.001, Output: 0.002, CacheRead: 0.007, CacheWrite: 0, Total: 0.01},
			},
			"extension override",
		); err != nil {
			t.Fatalf("AppendUsage: %v", err)
		}

		entries := sessionManager.GetEntries()
		if len(entries) != 1 {
			t.Fatalf("entries len = %d, want 1", len(entries))
		}
		usageEntry, ok := entries[0].(*core.UsageEntry)
		if !ok || usageEntry.Kind != "cache_warm" || usageEntry.Note == nil || *usageEntry.Note != "extension override" {
			t.Fatalf("unexpected usage entry: %#v", entries[0])
		}
		stats := session.GetSessionStats()
		wantTokens := core.SessionStatsTokens{Input: 2, Output: 1, CacheRead: 97, CacheWrite: 0, Total: 100}
		if stats.Tokens != wantTokens {
			t.Fatalf("Tokens = %#v, want %#v", stats.Tokens, wantTokens)
		}
		if stats.TotalMessages != 0 {
			t.Fatalf("TotalMessages = %d, want 0", stats.TotalMessages)
		}
		if len(sessionManager.BuildSessionContext().Messages) != 0 {
			t.Fatalf("expected empty BuildSessionContext messages")
		}
		breakdown := core.GetUsageCostBreakdown(sessionManager.GetEntries())
		if len(breakdown) != 1 || breakdown[0].Key != "anthropic/"+model.Id || !tasstCloseFloat(breakdown[0].Cost, 0.01) || breakdown[0].Tokens != 100 {
			t.Fatalf("breakdown = %#v", breakdown)
		}
	})

	t.Run("includes tool result usage in session totals", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		_, _ = sessionManager.AppendMessage(tasstCreateToolResultMessage(ai.Usage{
			Input:       10,
			Output:      20,
			CacheRead:   30,
			CacheWrite:  40,
			TotalTokens: 100,
			Cost:        ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}))
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		wantTokens := core.SessionStatsTokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100}
		if stats.Tokens != wantTokens {
			t.Fatalf("Tokens = %#v, want %#v", stats.Tokens, wantTokens)
		}
		if !tasstCloseFloat(stats.Cost, 1) {
			t.Fatalf("Cost = %v, want 1", stats.Cost)
		}
	})

	t.Run("groups tool and summary usage separately from model-attributed usage", func(t *testing.T) {
		sessionManager := core.SessionManagerInMemory()
		rootId, _ := sessionManager.AppendMessage(tasstCreateUserMessage("hello", 1))
		aMsg := tasstCreateAssistantMessage(model, "response", 100, 2)
		aMsg.Usage.Cost.Total = 0.5
		_, _ = sessionManager.AppendMessage(aMsg)
		tu := tasstCreateUsage(100)
		tu.Cost.Total = 1
		_, _ = sessionManager.AppendMessage(tasstCreateToolResultMessage(tu))
		cu := tasstCreateUsage(100)
		cu.Cost.Total = 2
		_, _ = sessionManager.AppendCompaction("summary", &rootId, 100, &core.AppendCompactionOptions{Usage: &cu})
		bu := tasstCreateUsage(100)
		bu.Cost.Total = 3
		_, _ = sessionManager.BranchWithSummary(nil, "branch summary", &core.BranchWithSummaryOptions{Usage: &bu})

		breakdown := core.GetUsageCostBreakdown(sessionManager.GetEntries())
		if len(breakdown) != 2 {
			t.Fatalf("breakdown len = %d (%#v), want 2", len(breakdown), breakdown)
		}
		if breakdown[0].Key != "Tools/summaries" || !tasstCloseFloat(breakdown[0].Cost, 6) || breakdown[0].Tokens != 300 {
			t.Fatalf("breakdown[0] = %#v, want Tools/summaries cost=6 tokens=300", breakdown[0])
		}
		wantModelKey := string(model.Provider) + "/" + model.Id
		if breakdown[1].Key != wantModelKey || !tasstCloseFloat(breakdown[1].Cost, 0.5) || breakdown[1].Tokens != 100 {
			t.Fatalf("breakdown[1] = %#v, want %s cost=0.5 tokens=100", breakdown[1], wantModelKey)
		}
	})

	t.Run("ignores zero-usage messages when checking for post-compaction context usage", func(t *testing.T) {
		session, sessionManager := tasstCreateSession(t)
		defer session.Dispose()

		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("first", 1))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response1", 180_000, 2))
		keptUserId, _ := sessionManager.AppendMessage(tasstCreateUserMessage("second", 3))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response2", 195_000, 4))
		_, _ = sessionManager.AppendCompaction("summary", &keptUserId, 195_000)
		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("third", 5))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "response3", 25_000, 6))
		_, _ = sessionManager.AppendMessage(tasstCreateUserMessage("continue", 7))
		_, _ = sessionManager.AppendMessage(tasstCreateAssistantMessage(model, "partial", 0, 8))
		tasstSyncAgentMessages(session, sessionManager)

		stats := session.GetSessionStats()
		if stats.ContextUsage == nil {
			t.Fatalf("expected defined ContextUsage")
		}
		tok, ok := stats.ContextUsage.Tokens.Get()
		if !ok || tok <= 25_000 {
			t.Fatalf("ContextUsage.Tokens = (%v, %v), want > 25000", tok, ok)
		}
	})
}
