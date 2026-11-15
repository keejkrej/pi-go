package core_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tascmpBoolPtr(v bool) *bool        { return &v }
func tascmpIntPtr(v int) *int           { return &v }
func tascmpFloatPtr(v float64) *float64 { return &v }
func tascmpStrPtr(v string) *string     { return &v }

func tascmpSeedCompactionTurns(t *testing.T, ctx context.Context, h *testkit.SuiteHarness, turns int) {
	t.Helper()
	var resps []providers.FauxResponseStep
	for i := range turns {
		resps = append(resps, providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantText("assistant-reply-"+string(rune('0'+i))),
				nil,
			),
		})
	}
	h.SetResponses(resps)
	for i := range turns {
		if err := h.Session.Prompt(ctx, strings.Repeat("user-prompt-", 40)+string(rune('0'+i))); err != nil {
			t.Fatalf("seed Prompt %d failed: %v", i, err)
		}
	}
}

func TestAgentSessionCompaction(t *testing.T) {
	ctx := context.Background()

	t.Run("should trigger manual compaction via compact()", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 3)

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Compacted summary text"), nil),
			},
		})

		result, err := h.Session.Compact(ctx)
		if err != nil {
			t.Fatalf("Compact failed: %v", err)
		}
		if result == nil || result.Summary != "Compacted summary text" {
			t.Fatalf("unexpected compaction result: %+v", result)
		}
		if result.FirstKeptEntryId == "" {
			t.Fatalf("expected non-empty FirstKeptEntryId")
		}
		if result.TokensBefore <= 0 {
			t.Fatalf("expected positive TokensBefore, got %d", result.TokensBefore)
		}
	})

	t.Run("should maintain valid session state after compaction", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 3)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Summary after turn 3"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Post-compaction reply"), nil),
			},
		})

		if _, err := h.Session.Compact(ctx); err != nil {
			t.Fatalf("Compact failed: %v", err)
		}

		// Session messages should contain a compactionSummary and kept messages, and accept follow-up prompts cleanly.
		hasSummary := false
		for _, msg := range h.Session.Messages() {
			if _, ok := msg.(*core.CompactionSummaryMessage); ok {
				hasSummary = true
				break
			}
		}
		if !hasSummary {
			t.Fatalf("expected CompactionSummaryMessage in session messages after compaction")
		}

		if err := h.Session.Prompt(ctx, "continue after compaction"); err != nil {
			t.Fatalf("Prompt after compaction failed: %v", err)
		}
		last := h.Session.GetLastAssistantText()
		if last == nil || *last != "Post-compaction reply" {
			t.Fatalf("unexpected post-compaction assistant reply: %v", last)
		}
	})

	t.Run("should persist compaction to session file", func(t *testing.T) {
		tmpDir := t.TempDir()
		sm, err := core.SessionManagerCreate(tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate failed: %v", err)
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			SessionManager: sm,
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 3)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Persisted summary"), nil),
			},
		})

		if _, err := h.Session.Compact(ctx); err != nil {
			t.Fatalf("Compact failed: %v", err)
		}
		sf := h.Session.SessionFile()
		if sf == nil || *sf == "" {
			t.Fatalf("expected non-empty session file path")
		}
		raw, err := os.ReadFile(*sf)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", *sf, err)
		}
		if !strings.Contains(string(raw), `"type":"compaction"`) || !strings.Contains(string(raw), "Persisted summary") {
			t.Fatalf("expected compaction entry persisted in JSONL, got:\n%s", string(raw))
		}
		reopened, err := core.SessionManagerOpen(*sf)
		if err != nil {
			t.Fatalf("SessionManagerOpen failed: %v", err)
		}
		foundCompaction := false
		for _, entry := range reopened.GetEntries() {
			if ce, ok := entry.(*core.CompactionEntry); ok && ce.Summary == "Persisted summary" {
				foundCompaction = true
				break
			}
		}
		if !foundCompaction {
			t.Fatalf("expected reopened session to contain CompactionEntry")
		}
	})

	t.Run("should work with in-memory session manager", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		if h.Session.SessionFile() != nil {
			t.Fatalf("expected nil SessionFile in in-memory mode")
		}
		tascmpSeedCompactionTurns(t, ctx, h, 2)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("In-memory summary"), nil),
			},
		})
		res, err := h.Session.Compact(ctx)
		if err != nil {
			t.Fatalf("Compact in-memory failed: %v", err)
		}
		if res.Summary != "In-memory summary" {
			t.Fatalf("unexpected summary: %q", res.Summary)
		}
	})

	t.Run("should emit compaction events during manual compaction", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 3)
		h.ResetEvents()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Event summary"), nil),
			},
		})

		if _, err := h.Session.Compact(ctx); err != nil {
			t.Fatalf("Compact failed: %v", err)
		}

		starts := h.EventsOfType("compaction_start")
		ends := h.EventsOfType("compaction_end")
		if len(starts) != 1 || len(ends) != 1 {
			t.Fatalf("expected 1 start and 1 end event, got %d / %d", len(starts), len(ends))
		}
		cs := starts[0].(*core.AgentSessionEventCompactionStart)
		ce := ends[0].(*core.AgentSessionEventCompactionEnd)
		if cs.Reason != "manual" || ce.Reason != "manual" {
			t.Fatalf("expected manual reason, got %q / %q", cs.Reason, ce.Reason)
		}
		if ce.Aborted || ce.WillRetry || ce.Result == nil || ce.Result.Summary != "Event summary" {
			t.Fatalf("unexpected compaction_end event: %+v", ce)
		}
	})

	t.Run("manually compacts using an extension-provided summary", func(t *testing.T) {
		summaryUsage := ai.Usage{
			Input:       10,
			Output:      20,
			CacheRead:   30,
			CacheWrite:  40,
			TotalTokens: 100,
			Cost: ai.UsageCost{
				Input:      0.1,
				Output:     0.2,
				CacheRead:  0.3,
				CacheWrite: 0.4,
				Total:      1,
			},
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(1),
				},
			},
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "custom-compaction-summary",
					Factory: func(pi core.ExtensionAPI) error {
						pi.OnSessionBeforeCompact(func(_ context.Context, ev *core.SessionBeforeCompactEvent, _ core.ExtensionContext) (*core.SessionBeforeCompactResult, error) {
							u := summaryUsage
							return &core.SessionBeforeCompactResult{
								Compaction: &core.CompactionResult{
									Summary:          "summary from extension",
									FirstKeptEntryId: ev.Preparation.FirstKeptEntryId,
									TokensBefore:     ev.Preparation.TokensBefore,
									Usage:            &u,
									Details:          map[string]any{"source": "extension"},
								},
							}, nil
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)
		statsBefore := h.Session.GetSessionStats()

		res, err := h.Session.Compact(ctx)
		if err != nil {
			t.Fatalf("Compact failed: %v", err)
		}
		if res == nil || res.Summary != "summary from extension" {
			t.Fatalf("unexpected compaction result: %+v", res)
		}
		if res.Usage == nil || *res.Usage != summaryUsage {
			t.Fatalf("result.Usage = %#v, want %#v", res.Usage, summaryUsage)
		}

		estimatedTokensAfter := 0
		for _, m := range h.Session.Messages() {
			estimatedTokensAfter += core.EstimateTokens(m)
		}
		if res.EstimatedTokensAfter == nil || *res.EstimatedTokensAfter != estimatedTokensAfter {
			t.Fatalf("EstimatedTokensAfter = %v, want %d", res.EstimatedTokensAfter, estimatedTokensAfter)
		}

		var compactionEntries []*core.CompactionEntry
		for _, entry := range h.SessionManager.GetEntries() {
			if ce, ok := entry.(*core.CompactionEntry); ok && ce != nil {
				compactionEntries = append(compactionEntries, ce)
			}
		}
		if len(compactionEntries) != 1 {
			t.Fatalf("expected 1 compaction entry, got %d", len(compactionEntries))
		}
		if compactionEntries[0].Usage == nil || *compactionEntries[0].Usage != summaryUsage {
			t.Fatalf("compactionEntries[0].Usage = %#v, want %#v", compactionEntries[0].Usage, summaryUsage)
		}

		statsAfter := h.Session.GetSessionStats()
		if statsAfter.Tokens.Input != statsBefore.Tokens.Input+summaryUsage.Input ||
			statsAfter.Tokens.Output != statsBefore.Tokens.Output+summaryUsage.Output ||
			statsAfter.Tokens.CacheRead != statsBefore.Tokens.CacheRead+summaryUsage.CacheRead ||
			statsAfter.Tokens.CacheWrite != statsBefore.Tokens.CacheWrite+summaryUsage.CacheWrite ||
			statsAfter.Cost != statsBefore.Cost+summaryUsage.Cost.Total {
			t.Fatalf("statsAfter = %+v, statsBefore = %+v, summaryUsage = %+v", statsAfter, statsBefore, summaryUsage)
		}

		msgs := h.Session.Messages()
		if len(msgs) < 2 || msgs[0].GetRole() != "system" || msgs[1].GetRole() != "compactionSummary" {
			t.Fatalf("expected [system, compactionSummary, ...], got roles %v", func() []string {
				roles := make([]string, len(msgs))
				for i, m := range msgs {
					roles[i] = m.GetRole()
				}
				return roles
			}())
		}
	})

	t.Run("allows a queued prompt to start when manual compaction ends", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)

		started := make(chan struct{})
		release := make(chan struct{})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(started)
				<-release
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Manual summary"), nil), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after compaction"), nil),
			},
		})

		compactDone := make(chan error, 1)
		go func() {
			_, err := h.Session.Compact(ctx)
			compactDone <- err
		}()

		select {
		case <-started:
		case err := <-compactDone:
			t.Fatalf("Compact finished early: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for compaction start")
		}
		// Prompt while compaction is running must fail immediately.
		if err := h.Session.Prompt(ctx, "blocked prompt"); err == nil || !strings.Contains(err.Error(), "Cannot submit a prompt while compaction is in progress") {
			t.Fatalf("expected compaction in progress error, got %v", err)
		}

		close(release)
		if err := <-compactDone; err != nil {
			t.Fatalf("Compact failed: %v", err)
		}

		if err := h.Session.Prompt(ctx, "next prompt"); err != nil {
			t.Fatalf("Prompt after compaction failed: %v", err)
		}
		if last := h.Session.GetLastAssistantText(); last == nil || *last != "after compaction" {
			t.Fatalf("unexpected reply after compaction: %v", last)
		}
	})

	t.Run("throws when compacting without a model", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		h.Session.Agent.SetModel(nil)
		_, err = h.Session.Compact(ctx)
		if err == nil || !strings.Contains(err.Error(), "No model selected") {
			t.Fatalf("expected 'No model selected' error, got %v", err)
		}
	})

	t.Run("throws when compacting without configured auth", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			WithConfiguredAuth: tascmpBoolPtr(false),
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(1),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		// Seed two turns directly in SessionManager without Prompt since auth is absent.
		sm := h.Session.SessionManager
		now := time.Now().UnixMilli()
		_, _ = sm.AppendMessage(&ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Text: tascmpStrPtr("seed user 1")},
			Timestamp: now,
		})
		_, _ = sm.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("seed assistant 1"), nil))
		_, _ = sm.AppendMessage(&ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Text: tascmpStrPtr("seed user 2")},
			Timestamp: now + 1,
		})
		_, _ = sm.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("seed assistant 2"), nil))
		h.Session.Agent.ReplaceMessages(sm.BuildSessionProjection().Messages)

		_, err = h.Session.Compact(ctx)
		expected := "No API key found for " + string(h.GetModel().Provider)
		if err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected %q error, got %v", expected, err)
		}
	})

	t.Run("manually compacts with a custom streamFn when registry auth is absent", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			WithConfiguredAuth: tascmpBoolPtr(false),
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(10),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		sm := h.Session.SessionManager
		for i := range 2 {
			_, _ = sm.AppendMessage(&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: tascmpStrPtr("turn-" + string(rune('0'+i)) + strings.Repeat("x", 300))},
				Timestamp: time.Now().UnixMilli(),
			})
			_, _ = sm.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("reply-"+string(rune('0'+i))), nil))
		}
		h.Session.Agent.ReplaceMessages(sm.BuildSessionProjection().Messages)

		var sawOptions *ai.SimpleStreamOptions
		h.Session.Agent.SetStreamFn(func(reqCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			sawOptions = options
			stream := ai.CreateAssistantMessageEventStream()
			msg := providers.FauxAssistantMessage(providers.FauxAssistantText("custom stream summary"), nil)
			stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
			stream.End(msg)
			return stream, nil
		})

		res, err := h.Session.Compact(ctx)
		if err != nil {
			t.Fatalf("Compact with custom StreamFn failed: %v", err)
		}
		if res == nil || res.Summary != "custom stream summary" {
			t.Fatalf("unexpected compaction result: %+v", res)
		}
		_ = sawOptions
	})

	t.Run("uses the standalone compaction request context", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 3)

		var compactionCtx *ai.TranscriptContext
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				compactionCtx = tc
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Standalone summary"), nil), nil
			}),
		})

		if _, err := h.Session.Compact(ctx); err != nil {
			t.Fatalf("Compact failed: %v", err)
		}
		if compactionCtx == nil {
			t.Fatalf("expected compaction request context to be captured")
		}
		if len(ai.GetCurrentTools(compactionCtx.Messages)) != 0 {
			t.Fatalf("expected standalone compaction request to have no tools, got %d", len(ai.GetCurrentTools(compactionCtx.Messages)))
		}
		if sysPrompt := ai.GetCurrentSystemPrompt(compactionCtx.Messages); !strings.Contains(sysPrompt, "context summarization assistant") {
			t.Fatalf("expected compaction summarization system prompt, got %q", sysPrompt)
		}
	})

	t.Run("persists usage from pi-generated manual compaction", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)

		compactionMsg := providers.FauxAssistantMessage(providers.FauxAssistantText("Summary with usage"), nil)
		compactionMsg.Usage = ai.Usage{
			Input:       120,
			Output:      45,
			TotalTokens: 165,
			Cost:        ai.UsageCost{Input: 0.001, Output: 0.002, Total: 0.003},
		}
		// Use custom StreamFn so FauxProvider does not overwrite custom Usage values.
		h.Session.Agent.SetStreamFn(func(reqCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			stream := ai.CreateAssistantMessageEventStream()
			stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: compactionMsg})
			stream.End(compactionMsg)
			return stream, nil
		})

		res, err := h.Session.Compact(ctx)
		if err != nil {
			t.Fatalf("Compact failed: %v", err)
		}
		if res.Usage == nil || res.Usage.TotalTokens != 165 || res.Usage.Cost.Total != 0.003 {
			t.Fatalf("expected compaction usage persisted, got %+v", res.Usage)
		}
	})

	t.Run("compacts and resumes after a length stop below the desired output limit", func(t *testing.T) {
		maxTok := 16000
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: tascmpIntPtr(200_000), MaxTokens: &maxTok},
			},
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled:          tascmpBoolPtr(true),
					KeepRecentTokens: tascmpFloatPtr(1),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		lengthMsg := providers.FauxAssistantMessage(providers.FauxAssistantText("partial truncated"), &providers.FauxAssistantMessageOptions{
			StopReason: func() *ai.StopReason { r := ai.StopReasonLength; return &r }(),
		})
		lengthMsg.Usage = ai.Usage{Input: 1000, Output: 500, TotalTokens: 1500}

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: lengthMsg},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Length summary"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Recovered after length compaction"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "do something"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}

		ends := h.EventsOfType("compaction_end")
		if len(ends) != 1 {
			t.Fatalf("expected 1 compaction_end, got %d", len(ends))
		}
		ce := ends[0].(*core.AgentSessionEventCompactionEnd)
		if ce.Reason != "overflow" || !ce.WillRetry {
			t.Fatalf("expected overflow compaction with WillRetry=true, got %+v", ce)
		}
		if last := h.Session.GetLastAssistantText(); last == nil || *last != "Recovered after length compaction" {
			t.Fatalf("expected recovered reply, got %v", last)
		}
	})

	for _, isErrorOverflow := range []bool{false, true} {
		name := "compacts and resumes on overflow (isError=false)"
		if isErrorOverflow {
			name = "compacts and resumes on overflow (isError=true)"
		}
		t.Run(name, func(t *testing.T) {
			h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
				Models: []providers.FauxModelDefinition{
					{Id: "faux-1", ContextWindow: tascmpIntPtr(10_000)},
				},
				Settings: &core.Settings{
					Compaction: &core.SettingsCompactionSettings{
						Enabled:          tascmpBoolPtr(true),
						ReserveTokens:    tascmpFloatPtr(1000),
						KeepRecentTokens: tascmpFloatPtr(60),
					},
				},
			})
			if err != nil {
				t.Fatalf("CreateSuiteHarness failed: %v", err)
			}
			defer h.Cleanup()

			tascmpSeedCompactionTurns(t, ctx, h, 2)
			h.ResetEvents()

			var overflowMsg *ai.AssistantMessage
			if isErrorOverflow {
				errStr := "prompt is too long"
				stopErr := ai.StopReasonError
				overflowMsg = providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
					StopReason:   &stopErr,
					ErrorMessage: &errStr,
				})
			} else {
				overflowMsg = providers.FauxAssistantMessage(providers.FauxAssistantText("final answer"), nil)
				overflowMsg.Usage = ai.Usage{Input: 12_000, TotalTokens: 12_000}
			}

			responses := []*ai.AssistantMessage{
				overflowMsg,
				providers.FauxAssistantMessage(providers.FauxAssistantText("Overflow summary"), nil),
			}
			if isErrorOverflow {
				// Omitting the errored overflow message produces a split turn (history + turn-prefix summary)
				// before Agent.Continue() issues the resumed assistant request.
				responses = append(responses,
					providers.FauxAssistantMessage(providers.FauxAssistantText("Overflow turn-prefix summary"), nil),
					providers.FauxAssistantMessage(providers.FauxAssistantText("Recovered from overflow"), nil),
				)
			}
			var callIdx int
			h.Session.Agent.SetStreamFn(func(reqCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				msg := responses[callIdx]
				callIdx++
				stream := ai.CreateAssistantMessageEventStream()
				stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: msg.StopReason, Message: msg})
				stream.End(msg)
				return stream, nil
			})

			if err := h.Session.Prompt(ctx, strings.Repeat("trigger-overflow-", 20)); err != nil {
				t.Fatalf("Prompt failed: %v", err)
			}

			ends := h.EventsOfType("compaction_end")
			if len(ends) != 1 {
				t.Fatalf("expected 1 compaction_end, got %d", len(ends))
			}
			ce := ends[0].(*core.AgentSessionEventCompactionEnd)
			if ce.Reason != "overflow" || ce.WillRetry != isErrorOverflow {
				t.Fatalf("expected reason=overflow willRetry=%v, got %+v", isErrorOverflow, ce)
			}
			if isErrorOverflow {
				if last := h.Session.GetLastAssistantText(); last == nil || *last != "Recovered from overflow" {
					t.Fatalf("unexpected recovery text: %v", last)
				}
			}
		})
	}

	t.Run("includes steering queued during compaction in the resumed assistant request", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled:          tascmpBoolPtr(true),
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)

		compactionStarted := make(chan struct{})
		releaseCompaction := make(chan struct{})
		var resumedSawSteer bool

		errStr := "prompt is too long"
		stopErr := ai.StopReasonError
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
					StopReason:   &stopErr,
					ErrorMessage: &errStr,
				}),
			},
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(compactionStarted)
				<-releaseCompaction
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Compaction summary"), nil), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn prefix summary"), nil),
			},
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && testkit.GetMessageText(um) == "steer during compaction" {
						resumedSawSteer = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("resumed with steer"), nil), nil
			}),
		})

		promptDone := make(chan error, 1)
		go func() {
			promptDone <- h.Session.Prompt(ctx, strings.Repeat("trigger-overflow-", 20))
		}()

		select {
		case <-compactionStarted:
		case err := <-promptDone:
			t.Fatalf("Prompt finished before compaction started: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for compaction start")
		}
		if _, err := h.Session.Steer(ctx, "steer during compaction", nil); err != nil {
			t.Fatalf("Steer failed: %v", err)
		}
		close(releaseCompaction)
		if err := <-promptDone; err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if !resumedSawSteer {
			t.Fatalf("expected resumed LLM request to include steering message queued during compaction")
		}
	})

	t.Run("does not compact after a terminating tool result", func(t *testing.T) {
		abortTool := &agent.AgentTool{
			Name:        "fail_tool",
			Label:       "Fail",
			Description: "Returns error",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{}, errors.New("tool boom")
			},
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{abortTool},
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: tascmpIntPtr(200_000)},
			},
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled:       tascmpBoolPtr(true),
					ReserveTokens: tascmpFloatPtr(1000),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		tcMsg := providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxToolCall("fail_tool", jsonx.NewObject(), nil)}, &providers.FauxAssistantMessageOptions{
			StopReason: &stopTool,
		})
		// ToolCall assistant turn followed by explicit error final turn (not overflow)
		errStr := "fatal stop"
		stopErr := ai.StopReasonError
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tcMsg},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
					StopReason:   &stopErr,
					ErrorMessage: &errStr,
				}),
			},
		})

		if err := h.Session.Prompt(ctx, "run fail tool"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if got := h.EventsOfType("compaction_start"); len(got) != 0 {
			t.Fatalf("expected no compaction after terminating error, got %d", len(got))
		}
	})

	t.Run("does not compact when a length stop reaches the desired output limit", func(t *testing.T) {
		maxTok := 4096
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: tascmpIntPtr(200_000), MaxTokens: &maxTok},
			},
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled: tascmpBoolPtr(true),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		stopLen := ai.StopReasonLength
		msg := providers.FauxAssistantMessage(providers.FauxAssistantText("long output"), &providers.FauxAssistantMessageOptions{
			StopReason: &stopLen,
		})
		// Output equals model.MaxTokens -> normal output truncation, not context overflow!
		msg.Usage = ai.Usage{Input: 1000, Output: 4096, TotalTokens: 5096}
		h.SetResponses([]providers.FauxResponseStep{providers.FauxResponseMessage{Message: msg}})

		if err := h.Session.Prompt(ctx, "hello"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if got := h.EventsOfType("compaction_start"); len(got) != 0 {
			t.Fatalf("expected no compaction when length stop reaches model MaxTokens, got %d", len(got))
		}
	})

	t.Run("stops after one compact-and-retry when a second response is also truncated", func(t *testing.T) {
		maxTok := 16000
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: tascmpIntPtr(200_000), MaxTokens: &maxTok},
			},
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled:          tascmpBoolPtr(true),
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)
		h.ResetEvents()

		stopLen := ai.StopReasonLength
		msg1 := providers.FauxAssistantMessage(providers.FauxAssistantText("truncated 1"), &providers.FauxAssistantMessageOptions{StopReason: &stopLen})
		msg1.Usage = ai.Usage{Input: 1000, Output: 200, TotalTokens: 1200}

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: msg1},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Summary"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn prefix summary"), nil),
			},
			providers.FauxResponseFactory(func(ctx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				msg2 := providers.FauxAssistantMessage(providers.FauxAssistantText("truncated 2"), &providers.FauxAssistantMessageOptions{StopReason: &stopLen})
				msg2.Usage = ai.Usage{Input: 1000, Output: 200, TotalTokens: 1200}
				msg2.Timestamp = time.Now().UnixMilli() + 10_000
				return msg2, nil
			}),
		})

		if err := h.Session.Prompt(ctx, strings.Repeat("trigger-length-", 20)); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}

		ends := h.EventsOfType("compaction_end")
		if len(ends) != 2 {
			t.Fatalf("expected 2 compaction_end events, got %d", len(ends))
		}
		second := ends[1].(*core.AgentSessionEventCompactionEnd)
		if second.WillRetry || second.ErrorMessage == nil || !strings.Contains(*second.ErrorMessage, "Truncated response recovery failed") {
			t.Fatalf("expected truncated recovery failure error on second compaction_end, got %+v", second)
		}
	})

	t.Run("keeps overflow wording when a repeated length stop fills the context window", func(t *testing.T) {
		maxTok := 16000
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: tascmpIntPtr(10_000), MaxTokens: &maxTok},
			},
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					Enabled:          tascmpBoolPtr(true),
					ReserveTokens:    tascmpFloatPtr(1000),
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)
		h.ResetEvents()

		stopLen := ai.StopReasonLength
		msg1 := providers.FauxAssistantMessage(providers.FauxAssistantText("truncated 1"), &providers.FauxAssistantMessageOptions{StopReason: &stopLen})
		msg1.Usage = ai.Usage{Input: 10_000, Output: 0, TotalTokens: 10_000}
		msg2 := providers.FauxAssistantMessage(providers.FauxAssistantText("truncated 2"), &providers.FauxAssistantMessageOptions{StopReason: &stopLen})
		msg2.Usage = ai.Usage{Input: 10_000, Output: 0, TotalTokens: 10_000}

		responses := []*ai.AssistantMessage{
			msg1,
			providers.FauxAssistantMessage(providers.FauxAssistantText("Summary"), nil),
			providers.FauxAssistantMessage(providers.FauxAssistantText("Turn prefix summary"), nil),
			msg2,
		}
		var callIdx int
		h.Session.Agent.SetStreamFn(func(reqCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			msg := responses[callIdx]
			if callIdx == 3 {
				msg.Timestamp = time.Now().UnixMilli() + 10_000
			}
			callIdx++
			stream := ai.CreateAssistantMessageEventStream()
			stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: msg.StopReason, Message: msg})
			stream.End(msg)
			return stream, nil
		})

		if err := h.Session.Prompt(ctx, strings.Repeat("overflow-length-", 20)); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}

		ends := h.EventsOfType("compaction_end")
		if len(ends) != 2 {
			t.Fatalf("expected 2 compaction_end events, got %d", len(ends))
		}
		second := ends[1].(*core.AgentSessionEventCompactionEnd)
		if second.WillRetry || second.ErrorMessage == nil || !strings.Contains(*second.ErrorMessage, "Context overflow recovery failed") {
			t.Fatalf("expected Context overflow recovery failed wording, got %+v", second)
		}
	})

	t.Run("cancels in-progress manual compaction when abortCompaction is called", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)

		started := make(chan struct{})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(started)
				<-reqCtx.Done()
				return nil, reqCtx.Err()
			}),
		})

		type resErr struct {
			err error
		}
		doneCh := make(chan resErr, 1)
		go func() {
			_, err := h.Session.Compact(ctx)
			doneCh <- resErr{err: err}
		}()

		select {
		case <-started:
		case res := <-doneCh:
			t.Fatalf("Compact finished early: %v", res.err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for compaction start")
		}
		h.Session.AbortCompaction()

		res := <-doneCh
		if res.err == nil {
			t.Fatalf("expected Compact to return an error when aborted")
		}
		ends := h.EventsOfType("compaction_end")
		if len(ends) != 1 || !ends[0].(*core.AgentSessionEventCompactionEnd).Aborted {
			t.Fatalf("expected aborted compaction_end event, got %+v", ends)
		}
	})

	t.Run("aborts an in-progress manual compaction and waits until the session is idle", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tascmpFloatPtr(60),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		tascmpSeedCompactionTurns(t, ctx, h, 2)

		started := make(chan struct{})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				close(started)
				<-reqCtx.Done()
				return nil, reqCtx.Err()
			}),
		})

		compactDone := make(chan error, 1)
		go func() {
			_, err := h.Session.Compact(ctx)
			compactDone <- err
		}()

		select {
		case <-started:
		case err := <-compactDone:
			t.Fatalf("Compact finished early: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for compaction start")
		}
		if err := h.Session.Abort(ctx); err != nil {
			t.Fatalf("Abort failed: %v", err)
		}
		if !h.Session.IsIdle() {
			t.Fatalf("expected session to be idle after Abort during compaction")
		}
		if err := <-compactDone; err == nil {
			t.Fatalf("expected aborted Compact error")
		}
	})

	t.Run("compaction model overrides", func(t *testing.T) {
		seedOverrideHistory := func(h *testkit.SuiteHarness, totalTokens int) string {
			model := h.Session.Model()
			var recentUserId string
			now := time.Now().UnixMilli()
			for _, label := range []string{"old", "recent"} {
				uid, err := h.SessionManager.AppendMessage(&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Blocks: []ai.Content{&ai.TextContent{Type: "text", Text: label + strings.Repeat("x", 400)}}},
					Timestamp: now - 2000,
				})
				if err != nil {
					t.Fatalf("AppendMessage user: %v", err)
				}
				recentUserId = uid
				asst := providers.FauxAssistantMessage(providers.FauxAssistantText(label+strings.Repeat("y", 400)), nil)
				asst.Api = ai.Api(model.Api)
				asst.Provider = model.Provider
				asst.Model = model.Id
				asst.Timestamp = now - 1000
				asst.Usage = ai.Usage{Input: totalTokens, TotalTokens: totalTokens}
				if _, err := h.SessionManager.AppendMessage(asst); err != nil {
					t.Fatalf("AppendMessage assistant: %v", err)
				}
			}
			h.Session.Agent.ReplaceMessages(h.SessionManager.BuildSessionProjection().Messages)
			return recentUserId
		}

		for _, path := range []string{"manual", "pre-prompt", "post-run", "overflow"} {
			t.Run("uses model token settings for "+path, func(t *testing.T) {
				providerName := "faux"
				overrides := omap.NewMap[string, *core.CompactionModelOverride]()
				overrides.Set("faux/faux-1", &core.CompactionModelOverride{
					ReserveTokens:    tascmpFloatPtr(2000),
					KeepRecentTokens: tascmpFloatPtr(150),
				})
				h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
					Provider: &providerName,
					Models:   []providers.FauxModelDefinition{{Id: "faux-1", ContextWindow: tascmpIntPtr(4000)}},
					Tools:    []*agent.AgentTool{},
					Settings: &core.Settings{
						Compaction: &core.SettingsCompactionSettings{
							Enabled:          tascmpBoolPtr(path != "manual"),
							ReserveTokens:    tascmpFloatPtr(10),
							KeepRecentTokens: tascmpFloatPtr(20000),
							ModelOverrides:   overrides,
						},
					},
				})
				if err != nil {
					t.Fatalf("CreateSuiteHarness failed: %v", err)
				}
				defer h.Cleanup()

				initTokens := 650
				if path == "pre-prompt" {
					initTokens = 2500
				}
				recentUserId := seedOverrideHistory(h, initTokens)
				h.ResetEvents()

				summaryMsg := providers.FauxAssistantMessage(providers.FauxAssistantText("compacted history"), nil)
				switch path {
				case "manual":
					h.SetResponses([]providers.FauxResponseStep{providers.FauxResponseMessage{Message: summaryMsg}})
					if _, err := h.Session.Compact(ctx); err != nil {
						t.Fatalf("Compact failed: %v", err)
					}
				case "pre-prompt":
					// Pre-prompt checks before prompt LLM call, then prompts "done"
					h.SetResponses([]providers.FauxResponseStep{
						providers.FauxResponseMessage{Message: summaryMsg},
						providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
					})
					if err := h.Session.Prompt(ctx, "continue"); err != nil {
						t.Fatalf("Prompt failed: %v", err)
					}
				case "post-run":
					// Prompt response has large usage so post-run threshold compaction fires (contextWindow=4000, reserve=2000 -> threshold=2000).
					// Keep user prompt > override keepRecentTokens (150) and assistant reply < 150 so cut lands cleanly on user prompt without split turn.
					bigAsst := providers.FauxAssistantMessage(providers.FauxAssistantText("post-run assistant reply"), nil)
					bigAsst.Usage = ai.Usage{Input: 3000, TotalTokens: 3000}
					responses := []*ai.AssistantMessage{bigAsst, summaryMsg}
					var callIdx int
					h.Session.Agent.SetStreamFn(func(reqCtx context.Context, model *ai.Model, tc *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
						msg := responses[callIdx]
						callIdx++
						stream := ai.CreateAssistantMessageEventStream()
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: msg.StopReason, Message: msg})
						stream.End(msg)
						return stream, nil
					})
					if err := h.Session.Prompt(ctx, strings.Repeat("continue-", 90)); err != nil {
						t.Fatalf("Prompt failed: %v", err)
					}
				case "overflow":
					errStr := "prompt is too long"
					stopErr := ai.StopReasonError
					h.SetResponses([]providers.FauxResponseStep{
						providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
							StopReason:   &stopErr,
							ErrorMessage: &errStr,
						})},
						providers.FauxResponseMessage{Message: summaryMsg},
						providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("turn prefix summary"), nil)},
						providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("recovered"), nil)},
					})
					if err := h.Session.Prompt(ctx, strings.Repeat("continue-", 90)); err != nil {
						t.Fatalf("Prompt failed: %v", err)
					}
				}

				ends := h.EventsOfType("compaction_end")
				if len(ends) != 1 {
					t.Fatalf("expected 1 compaction_end event for %s, got %d", path, len(ends))
				}
				ce := ends[0].(*core.AgentSessionEventCompactionEnd)
				if ce.Aborted || ce.WillRetry != (path == "overflow") {
					t.Fatalf("unexpected compaction_end flags for %s: %+v", path, ce)
				}
				if ce.Result == nil || (path != "overflow" && ce.Result.Summary != "compacted history") || (path == "overflow" && !strings.HasPrefix(ce.Result.Summary, "compacted history")) {
					t.Fatalf("unexpected compaction_end result for %s: %+v", path, ce.Result)
				}
				if path == "manual" || path == "pre-prompt" {
					if ce.Result.FirstKeptEntryId != recentUserId {
						t.Fatalf("expected FirstKeptEntryId %q using override keepRecentTokens=150, got %q", recentUserId, ce.Result.FirstKeptEntryId)
					}
				}
				if h.GetPendingResponseCount() != 0 {
					t.Fatalf("expected 0 pending responses, got %d", h.GetPendingResponseCount())
				}
			})
		}

		t.Run("uses newly selected model without changing ordinary settings", func(t *testing.T) {
			providerName := "faux"
			overrides := omap.NewMap[string, *core.CompactionModelOverride]()
			overrides.Set("faux/big", &core.CompactionModelOverride{
				ReserveTokens:    tascmpFloatPtr(8000),
				KeepRecentTokens: tascmpFloatPtr(150),
			})
			h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
				Provider: &providerName,
				Models: []providers.FauxModelDefinition{
					{Id: "small", ContextWindow: tascmpIntPtr(4000)},
					{Id: "big", ContextWindow: tascmpIntPtr(10000)},
				},
				Tools: []*agent.AgentTool{},
				Settings: &core.Settings{
					Compaction: &core.SettingsCompactionSettings{
						ReserveTokens:  tascmpFloatPtr(10),
						ModelOverrides: overrides,
					},
				},
			})
			if err != nil {
				t.Fatalf("CreateSuiteHarness failed: %v", err)
			}
			defer h.Cleanup()

			seedOverrideHistory(h, 2500)
			h.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("small response"), nil)},
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("big model summary"), nil)},
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("big response"), nil)},
			})
			if err := h.Session.Prompt(ctx, "continue on small"); err != nil {
				t.Fatalf("Prompt on small failed: %v", err)
			}
			if got := h.EventsOfType("compaction_start"); len(got) != 0 {
				t.Fatalf("expected no compaction on small model, got %d", len(got))
			}

			seedOverrideHistory(h, 2500)
			if err := h.Session.SetModel(ctx, h.GetModelId("big")); err != nil {
				t.Fatalf("SetModel(big) failed: %v", err)
			}
			if err := h.Session.Prompt(ctx, "continue on big"); err != nil {
				t.Fatalf("Prompt on big failed: %v", err)
			}
			ends := h.EventsOfType("compaction_end")
			if len(ends) != 1 {
				t.Fatalf("expected 1 compaction_end after switching to big model, got %d", len(ends))
			}
			if ce := ends[0].(*core.AgentSessionEventCompactionEnd); ce.Result == nil || ce.Result.Summary != "big model summary" {
				t.Fatalf("unexpected compaction result: %+v", ce.Result)
			}
			if got, err := h.SettingsManager.GetCompactionReserveTokens(nil); err != nil || got != 10 {
				t.Fatalf("expected global ReserveTokens unchanged (10), got %d (err=%v)", got, err)
			}
			if err := h.Session.SetModel(ctx, h.GetModelId("small")); err != nil {
				t.Fatalf("SetModel(small) failed: %v", err)
			}
			if got, err := h.SettingsManager.GetCompactionReserveTokens(h.Session.Model()); err != nil || got != 10 {
				t.Fatalf("expected small ReserveTokens unchanged (10), got %d (err=%v)", got, err)
			}
		})

		t.Run("captures model identity before awaiting summarization auth", func(t *testing.T) {
			providerName := "faux"
			overrides := omap.NewMap[string, *core.CompactionModelOverride]()
			overrides.Set("faux/first", &core.CompactionModelOverride{
				ReserveTokens:    tascmpFloatPtr(2000),
				KeepRecentTokens: tascmpFloatPtr(150),
			})
			overrides.Set("faux/second", &core.CompactionModelOverride{
				ReserveTokens:    tascmpFloatPtr(4000),
				KeepRecentTokens: tascmpFloatPtr(20000),
			})
			h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
				Provider: &providerName,
				Models:   []providers.FauxModelDefinition{{Id: "first"}, {Id: "second"}},
				Settings: &core.Settings{
					Compaction: &core.SettingsCompactionSettings{
						ModelOverrides: overrides,
					},
				},
			})
			if err != nil {
				t.Fatalf("CreateSuiteHarness failed: %v", err)
			}
			defer h.Cleanup()

			seedOverrideHistory(h, 650)

			var mu sync.Mutex
			var requestedModels []string
			h.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
					mu.Lock()
					requestedModels = append(requestedModels, model.Id)
					mu.Unlock()
					h.Session.Agent.SetModel(h.GetModelId("second"))
					return providers.FauxAssistantMessage(providers.FauxAssistantText("summary"), nil), nil
				}),
			})

			if _, err := h.Session.Compact(ctx); err != nil {
				t.Fatalf("Compact failed: %v", err)
			}
			if len(requestedModels) != 1 || requestedModels[0] != "first" {
				t.Fatalf("expected summarization request for 'first', got %v", requestedModels)
			}
		})
	})
}
