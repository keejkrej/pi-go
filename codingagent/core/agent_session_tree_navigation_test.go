// Ported from packages/coding-agent/test/agent-session-tree-navigation.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
)

func tastnFloatPtr(v float64) *float64 { return &v }
func tastnStrPtr(s string) *string     { return &s }

func tastnTextResponse(text string) providers.FauxResponseStep {
	return providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil),
	}
}

func tastnFirstUserEntry(t *testing.T, sm *core.SessionManager) *core.SessionMessageEntry {
	t.Helper()
	for _, e := range sm.GetEntries() {
		if me, ok := e.(*core.SessionMessageEntry); ok && me.Message.GetRole() == "user" {
			return me
		}
	}
	t.Fatalf("expected at least one user entry in session")
	return nil
}

func TestAgentSessionTreeNavigation(t *testing.T) {
	ctx := context.Background()

	t.Run("navigates to user message and puts text in editor", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{KeepRecentTokens: tastnFloatPtr(1)},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("Reply 1"),
			tastnTextResponse("Reply 2"),
		})
		if err := h.Session.Prompt(ctx, "First message"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		_ = h.Session.WaitForIdle(ctx)
		if err := h.Session.Prompt(ctx, "Second message"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		_ = h.Session.WaitForIdle(ctx)

		tree := h.SessionManager.GetTree()
		if len(tree) != 1 {
			t.Fatalf("tree len = %d, want 1", len(tree))
		}
		rootNode := tree[0]
		if rootNode.Entry.GetType() != "message" {
			t.Fatalf("root entry type = %q, want message", rootNode.Entry.GetType())
		}
		u1 := tastnFirstUserEntry(t, h.SessionManager)

		res, err := h.Session.NavigateTree(ctx, u1.Id, &core.NavigateTreeOptions{Summarize: false})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled {
			t.Fatalf("expected Cancelled=false")
		}
		if res.EditorText == nil || *res.EditorText != "First message" {
			t.Fatalf("EditorText = %v, want 'First message'", res.EditorText)
		}
		leafId := h.SessionManager.GetLeafId()
		if (u1.ParentId == nil) != (leafId == nil) || (leafId != nil && *leafId != *u1.ParentId) {
			t.Fatalf("leafId = %v, want parentId %v", leafId, u1.ParentId)
		}
	})

	t.Run("navigates to non-user message without editor text", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{tastnTextResponse("Hi there")})
		if err := h.Session.Prompt(ctx, "Hello"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		_ = h.Session.WaitForIdle(ctx)

		var assistantEntryId string
		for _, e := range h.SessionManager.GetEntries() {
			if me, ok := e.(*core.SessionMessageEntry); ok && me.Message.GetRole() == "assistant" {
				assistantEntryId = me.Id
				break
			}
		}
		if assistantEntryId == "" {
			t.Fatalf("expected assistant entry")
		}

		res, err := h.Session.NavigateTree(ctx, assistantEntryId, &core.NavigateTreeOptions{Summarize: false})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled || res.EditorText != nil {
			t.Fatalf("unexpected NavigateTree result: %#v", res)
		}
		leafId := h.SessionManager.GetLeafId()
		if leafId == nil || *leafId != assistantEntryId {
			t.Fatalf("leafId = %v, want %q", leafId, assistantEntryId)
		}
	})

	t.Run("creates branch summary when navigating with summarize=true", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{KeepRecentTokens: tastnFloatPtr(1)},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("4"),
			tastnTextResponse("6"),
			tastnTextResponse("Discussed arithmetic: 2+2 and 3+3."),
		})
		_ = h.Session.Prompt(ctx, "What is 2+2?")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "What is 3+3?")
		_ = h.Session.WaitForIdle(ctx)

		u1 := tastnFirstUserEntry(t, h.SessionManager)
		res, err := h.Session.NavigateTree(ctx, u1.Id, &core.NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled || res.EditorText == nil || *res.EditorText != "What is 2+2?" {
			t.Fatalf("unexpected NavigateTree result: %#v", res)
		}
		if res.SummaryEntry == nil || res.SummaryEntry.Type != "branch_summary" || res.SummaryEntry.Summary == "" {
			t.Fatalf("expected populated branch_summary SummaryEntry, got %#v", res.SummaryEntry)
		}
		if (u1.ParentId == nil) != (res.SummaryEntry.ParentId == nil) || (res.SummaryEntry.ParentId != nil && *res.SummaryEntry.ParentId != *u1.ParentId) {
			t.Fatalf("expected SummaryEntry.ParentId=%v, got %v", u1.ParentId, res.SummaryEntry.ParentId)
		}
		leafId := h.SessionManager.GetLeafId()
		if leafId == nil || *leafId != res.SummaryEntry.Id {
			t.Fatalf("leafId = %v, want %q", leafId, res.SummaryEntry.Id)
		}
	})

	t.Run("attaches summary to correct parent when navigating to nested user message", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("One"),
			tastnTextResponse("Two"),
			tastnTextResponse("Three"),
			tastnTextResponse("Summarized branch from u2 onward."),
		})
		_ = h.Session.Prompt(ctx, "Message one")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Message two")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Message three")
		_ = h.Session.WaitForIdle(ctx)

		var userEntries []*core.SessionMessageEntry
		entries := h.SessionManager.GetEntries()
		for _, e := range entries {
			if me, ok := e.(*core.SessionMessageEntry); ok && me.Message.GetRole() == "user" {
				userEntries = append(userEntries, me)
			}
		}
		if len(userEntries) != 3 {
			t.Fatalf("userEntries len = %d, want 3", len(userEntries))
		}
		u2 := userEntries[1]
		if u2.ParentId == nil {
			t.Fatalf("expected u2.ParentId != nil")
		}
		a1Id := *u2.ParentId

		res, err := h.Session.NavigateTree(ctx, u2.Id, &core.NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled || res.EditorText == nil || *res.EditorText != "Message two" || res.SummaryEntry == nil {
			t.Fatalf("unexpected NavigateTree result: %#v", res)
		}
		if res.SummaryEntry.ParentId == nil || *res.SummaryEntry.ParentId != a1Id {
			t.Fatalf("SummaryEntry.ParentId = %v, want %q", res.SummaryEntry.ParentId, a1Id)
		}

		children := h.SessionManager.GetChildren(a1Id)
		if len(children) != 2 {
			t.Fatalf("children len = %d, want 2", len(children))
		}
		hasSummary, hasMessage := false, false
		for _, c := range children {
			switch c.GetType() {
			case "branch_summary":
				hasSummary = true
			case "message":
				hasMessage = true
			}
		}
		if !hasSummary || !hasMessage {
			t.Fatalf("expected both branch_summary and message children, got %#v", children)
		}
	})

	t.Run("attaches summary to selected node when navigating to assistant message", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("Hello back"),
			tastnTextResponse("Bye"),
			tastnTextResponse("Left goodbye branch."),
		})
		_ = h.Session.Prompt(ctx, "Hello")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Goodbye")
		_ = h.Session.WaitForIdle(ctx)

		var a1Id string
		for _, e := range h.SessionManager.GetEntries() {
			if me, ok := e.(*core.SessionMessageEntry); ok && me.Message.GetRole() == "assistant" {
				a1Id = me.Id
				break
			}
		}
		res, err := h.Session.NavigateTree(ctx, a1Id, &core.NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled || res.EditorText != nil || res.SummaryEntry == nil {
			t.Fatalf("unexpected NavigateTree result: %#v", res)
		}
		if res.SummaryEntry.ParentId == nil || *res.SummaryEntry.ParentId != a1Id {
			t.Fatalf("SummaryEntry.ParentId = %v, want %q", res.SummaryEntry.ParentId, a1Id)
		}
		leafId := h.SessionManager.GetLeafId()
		if leafId == nil || *leafId != res.SummaryEntry.Id {
			t.Fatalf("leafId = %v, want %q", leafId, res.SummaryEntry.Id)
		}
	})

	t.Run("handles abort during summarization", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		sumStarted := make(chan struct{}, 1)
		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("First reply"),
			tastnTextResponse("Second reply"),
			providers.FauxResponseFactory(func(reqCtx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				select {
				case sumStarted <- struct{}{}:
				default:
				}
				select {
				case <-reqCtx.Done():
					return providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
						StopReason: func() *ai.StopReason { r := ai.StopReasonAborted; return &r }(),
					}), reqCtx.Err()
				case <-time.After(5 * time.Second):
					return providers.FauxAssistantMessage(providers.FauxAssistantText("Finished summary"), nil), nil
				}
			}),
		})
		_ = h.Session.Prompt(ctx, "Tell me about something")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Continue")
		_ = h.Session.WaitForIdle(ctx)

		entriesBefore := len(h.SessionManager.GetEntries())
		leafBefore := h.SessionManager.GetLeafId()
		rootId := h.SessionManager.GetTree()[0].Entry.GetId()

		resCh := make(chan *core.NavigateTreeResult, 1)
		errCh := make(chan error, 1)
		go func() {
			r, nErr := h.Session.NavigateTree(ctx, rootId, &core.NavigateTreeOptions{Summarize: true})
			resCh <- r
			errCh <- nErr
		}()

		select {
		case <-sumStarted:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for summarization stream start")
		}
		if !h.Session.IsCompacting() {
			t.Fatalf("expected IsCompacting() == true during branch summarization")
		}
		h.Session.AbortBranchSummary()

		res := <-resCh
		navErr := <-errCh
		if navErr != nil {
			t.Fatalf("NavigateTree returned error: %v", navErr)
		}
		if res == nil || !res.Cancelled || !res.Aborted || res.SummaryEntry != nil {
			t.Fatalf("expected cancelled+aborted with nil SummaryEntry, got %#v", res)
		}
		if got := len(h.SessionManager.GetEntries()); got != entriesBefore {
			t.Fatalf("entries len changed from %d to %d", entriesBefore, got)
		}
		leafAfter := h.SessionManager.GetLeafId()
		if (leafBefore == nil) != (leafAfter == nil) || (leafBefore != nil && *leafBefore != *leafAfter) {
			t.Fatalf("leaf changed from %v to %v", leafBefore, leafAfter)
		}
	})

	t.Run("does not create summary when navigating without summarize option", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("R1"),
			tastnTextResponse("R2"),
		})
		_ = h.Session.Prompt(ctx, "First")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Second")
		_ = h.Session.WaitForIdle(ctx)

		entriesBefore := len(h.SessionManager.GetEntries())
		rootId := h.SessionManager.GetTree()[0].Entry.GetId()
		if _, err := h.Session.NavigateTree(ctx, rootId, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		entriesAfter := h.SessionManager.GetEntries()
		if len(entriesAfter) != entriesBefore {
			t.Fatalf("entries len changed from %d to %d", entriesBefore, len(entriesAfter))
		}
		for _, e := range entriesAfter {
			if e.GetType() == "branch_summary" {
				t.Fatalf("expected no branch_summary entries")
			}
		}
	})

	t.Run("handles navigation to same position as no-op", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{tastnTextResponse("Hi")})
		_ = h.Session.Prompt(ctx, "Hello")
		_ = h.Session.WaitForIdle(ctx)

		leafBefore := h.SessionManager.GetLeafId()
		if leafBefore == nil {
			t.Fatalf("expected non-nil leafBefore")
		}
		entriesBefore := len(h.SessionManager.GetEntries())

		res, err := h.Session.NavigateTree(ctx, *leafBefore, &core.NavigateTreeOptions{Summarize: false})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled {
			t.Fatalf("expected Cancelled=false")
		}
		leafAfter := h.SessionManager.GetLeafId()
		if leafAfter == nil || *leafAfter != *leafBefore {
			t.Fatalf("leafAfter = %v, want %q", leafAfter, *leafBefore)
		}
		if got := len(h.SessionManager.GetEntries()); got != entriesBefore {
			t.Fatalf("entries len = %d, want %d", got, entriesBefore)
		}
	})

	t.Run("supports custom summarization instructions", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		var seenPrompt string
		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("TypeScript is typed JS."),
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				if tr != nil && len(tr.Messages) > 0 {
					for _, m := range tr.Messages {
						if um, ok := m.(*ai.UserMessage); ok {
							if um.Content.Text != nil {
								seenPrompt += *um.Content.Text
							} else {
								for _, b := range um.Content.Blocks {
									if tb, ok := b.(*ai.TextContent); ok {
										seenPrompt += tb.Text
									}
								}
							}
						}
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Summary. MONKEY MONKEY MONKEY"), nil), nil
			}),
		})
		_ = h.Session.Prompt(ctx, "What is TypeScript?")
		_ = h.Session.WaitForIdle(ctx)

		rootId := h.SessionManager.GetTree()[0].Entry.GetId()
		res, err := h.Session.NavigateTree(ctx, rootId, &core.NavigateTreeOptions{
			Summarize:          true,
			CustomInstructions: tastnStrPtr("After the summary, you MUST end with exactly: MONKEY MONKEY MONKEY."),
		})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if !strings.Contains(seenPrompt, "MONKEY MONKEY MONKEY") {
			t.Fatalf("expected custom instructions forwarded to summarization prompt, got %q", seenPrompt)
		}
		if res.SummaryEntry == nil || !strings.Contains(res.SummaryEntry.Summary, "MONKEY MONKEY MONKEY") {
			t.Fatalf("expected summary containing marker, got %#v", res.SummaryEntry)
		}
	})

	t.Run("navigates between branches correctly", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			tastnTextResponse("Main 1"),
			tastnTextResponse("Main 2"),
			tastnTextResponse("Branch 1"),
			tastnTextResponse("Left branch path summary"),
		})
		_ = h.Session.Prompt(ctx, "Main branch start")
		_ = h.Session.WaitForIdle(ctx)
		_ = h.Session.Prompt(ctx, "Main branch continue")
		_ = h.Session.WaitForIdle(ctx)

		entries := h.SessionManager.GetEntries()
		var a1Id string
		var userIds []string
		for _, e := range entries {
			if me, ok := e.(*core.SessionMessageEntry); ok {
				if a1Id == "" && me.Message.GetRole() == "assistant" {
					a1Id = me.Id
				}
				if me.Message.GetRole() == "user" {
					userIds = append(userIds, me.Id)
				}
			}
		}
		if err := h.SessionManager.Branch(a1Id); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		_ = h.Session.Prompt(ctx, "Branch path")
		_ = h.Session.WaitForIdle(ctx)

		u2Id := userIds[1]
		res, err := h.Session.NavigateTree(ctx, u2Id, &core.NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.Cancelled || res.EditorText == nil || *res.EditorText != "Main branch continue" {
			t.Fatalf("unexpected NavigateTree result: %#v", res)
		}
		if res.SummaryEntry == nil || len(res.SummaryEntry.Summary) == 0 {
			t.Fatalf("expected non-empty branch summary")
		}
	})
}
