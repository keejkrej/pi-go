package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
)

func tsmcStrPtr(s string) *string { return &s }

func tsmcUserMsg(text string, ts ...int64) *ai.UserMessage {
	timestamp := int64(1)
	if len(ts) > 0 {
		timestamp = ts[0]
	}
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: timestamp,
	}
}

func tsmcAssistantMsg(text string, totalTokens ...int) *ai.AssistantMessage {
	tokens := 11
	if len(totalTokens) > 0 {
		tokens = totalTokens[0]
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		Api:        "faux",
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		Usage:      ai.Usage{Input: tokens - 1, Output: 1, TotalTokens: tokens},
		StopReason: ai.StopReasonStop,
		Timestamp:  2,
	}
}

func tsmcSystemMsg(text string) *ai.SystemMessage {
	return &ai.SystemMessage{
		Role:      "system",
		Content:   ai.SystemMessageContent{Text: &text},
		Timestamp: 1,
	}
}

func tsmcToolResultMsg(text string) *ai.ToolResultMessage {
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "call-1",
		ToolName:   "read",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		IsError:    true,
		Timestamp:  3,
	}
}

func tsmcMsgText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		var parts []string
		for _, c := range m.Content.Blocks {
			if tc, ok := c.(*ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "")
	case *ai.AssistantMessage:
		var parts []string
		for _, c := range m.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "")
	case *ai.ToolResultMessage:
		var parts []string
		for _, c := range m.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "")
	case *ai.SystemMessage:
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		return ""
	case *CompactionSummaryMessage:
		return m.Summary
	case *BranchSummaryMessage:
		return m.Summary
	case *CustomMessage:
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		return ""
	default:
		return ""
	}
}

func tsmcMsgRoles(msgs []agent.AgentMessage) []string {
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.GetRole()
	}
	return roles
}

func TestBuildSessionContextAndResetLeaf(t *testing.T) {
	emptyCtx := BuildSessionContext(nil, nil)
	if len(emptyCtx.Messages) != 0 || emptyCtx.ThinkingLevel != "off" || emptyCtx.Model != nil {
		t.Fatalf("unexpected empty context: %+v", emptyCtx)
	}

	sm := SessionManagerInMemory()
	u1, err := sm.AppendMessage(tsmcUserMsg("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendModelChange("openai", "gpt-5"); err != nil {
		t.Fatal(err)
	}
	a1, err := sm.AppendMessage(tsmcAssistantMsg("answer 1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendUsage("warm", "openai", "gpt-5", ai.Usage{Input: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendCustomEntry("internal", map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendCustomMessageEntry("notice", "ext hello", true); err != nil {
		t.Fatal(err)
	}

	ctx := sm.BuildSessionContext()
	if !slices.Equal(tsmcMsgRoles(ctx.Messages), []string{"user", "assistant", "custom"}) {
		t.Fatalf("roles = %v", tsmcMsgRoles(ctx.Messages))
	}
	if ctx.ThinkingLevel != "high" {
		t.Fatalf("ThinkingLevel = %q, want high", ctx.ThinkingLevel)
	}
	// AssistantMessage overrides the earlier ModelChange entry with its own provider/model.
	if ctx.Model == nil || ctx.Model.Provider != "anthropic" || ctx.Model.ModelId != "claude-sonnet-4-5" {
		t.Fatalf("Model = %+v", ctx.Model)
	}
	// Subsequent ModelChange after assistant message becomes active model.
	if _, err := sm.AppendModelChange("openai", "gpt-5"); err != nil {
		t.Fatal(err)
	}
	if ctxAfter := sm.BuildSessionContext(); ctxAfter.Model == nil || ctxAfter.Model.Provider != "openai" || ctxAfter.Model.ModelId != "gpt-5" {
		t.Fatalf("ctxAfter.Model = %+v", ctxAfter.Model)
	}

	// Branching creates a separate path with isolated thinking/model updates.
	if err := sm.Branch(u1); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendThinkingLevelChange("minimal"); err != nil {
		t.Fatal(err)
	}
	bMsg, err := sm.AppendMessage(tsmcUserMsg("second branch"))
	if err != nil {
		t.Fatal(err)
	}
	branchCtx := sm.BuildSessionContext()
	if branchCtx.ThinkingLevel != "minimal" || branchCtx.Model != nil {
		t.Fatalf("branch context = %+v", branchCtx)
	}
	if !slices.Equal(tsmcMsgRoles(branchCtx.Messages), []string{"user", "user"}) {
		t.Fatalf("branch roles = %v", tsmcMsgRoles(branchCtx.Messages))
	}
	if tsmcMsgText(branchCtx.Messages[1]) != "second branch" {
		t.Fatalf("branch message[1] = %q", tsmcMsgText(branchCtx.Messages[1]))
	}

	// Switching leaf back to a1 recovers original path.
	if err := sm.Branch(a1); err != nil {
		t.Fatal(err)
	}
	oldBranchCtx := sm.BuildSessionContext()
	if oldBranchCtx.ThinkingLevel != "high" || oldBranchCtx.Model == nil || oldBranchCtx.Model.ModelId != "claude-sonnet-4-5" {
		t.Fatalf("oldBranchCtx = %+v", oldBranchCtx)
	}

	// ResetLeaf clears instance context without falling back to last entry, while standalone nil falls back.
	sm.ResetLeaf()
	resetCtx := sm.BuildSessionContext()
	if len(resetCtx.Messages) != 0 || resetCtx.ThinkingLevel != "off" || resetCtx.Model != nil {
		t.Fatalf("expected empty context after ResetLeaf, got %+v", resetCtx)
	}
	rawEntries := sm.GetEntries()
	standaloneDefault := BuildSessionContext(rawEntries, nil)
	if len(standaloneDefault.Messages) == 0 {
		t.Fatal("expected standalone BuildSessionContext(entries, nil) to fall back to last entry")
	}

	// BranchWithSummary inserts BranchSummaryMessage along branched path.
	fromRoot := bMsg
	if _, err := sm.BranchWithSummary(&fromRoot, "abandoned branch summary"); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(tsmcUserMsg("resumed work")); err != nil {
		t.Fatal(err)
	}
	afterSummary := sm.BuildSessionContext()
	if !slices.Equal(tsmcMsgRoles(afterSummary.Messages), []string{"user", "user", "branchSummary", "user"}) {
		t.Fatalf("afterSummary roles = %v", tsmcMsgRoles(afterSummary.Messages))
	}
	if tsmcMsgText(afterSummary.Messages[2]) != "abandoned branch summary" {
		t.Fatalf("branchSummary text = %q", tsmcMsgText(afterSummary.Messages[2]))
	}
}

func TestBuildSessionContextWithCompactionAndSystemMessage(t *testing.T) {
	sm := SessionManagerInMemory()
	if _, err := sm.AppendMessage(tsmcSystemMsg("effective system v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(tsmcUserMsg("discarded user")); err != nil {
		t.Fatal(err)
	}
	keptId, err := sm.AppendMessage(tsmcUserMsg("kept user"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(tsmcAssistantMsg("kept answer")); err != nil {
		t.Fatal(err)
	}
	compId, err := sm.AppendCompaction("first compaction summary", &keptId, 500)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(tsmcUserMsg("post compaction user")); err != nil {
		t.Fatal(err)
	}

	compEntry, ok := sm.GetEntry(compId).(*CompactionEntry)
	if !ok || compEntry.SystemMessage == nil || compEntry.SystemMessage.Content.Text == nil ||
		*compEntry.SystemMessage.Content.Text != "effective system v1" {
		t.Fatalf("expected CompactionEntry to capture SystemMessage, got %+v", sm.GetEntry(compId))
	}

	ctx := sm.BuildSessionContext()
	if !slices.Equal(tsmcMsgRoles(ctx.Messages), []string{"system", "compactionSummary", "user", "assistant", "user"}) {
		t.Fatalf("roles after compaction = %v", tsmcMsgRoles(ctx.Messages))
	}
	if tsmcMsgText(ctx.Messages[0]) != "effective system v1" ||
		tsmcMsgText(ctx.Messages[1]) != "first compaction summary" ||
		tsmcMsgText(ctx.Messages[2]) != "kept user" ||
		tsmcMsgText(ctx.Messages[4]) != "post compaction user" {
		t.Fatalf("unexpected compacted messages: %+v", ctx.Messages)
	}

	// Self-referencing compaction (nil firstKeptEntryId) retains zero prior non-system entries.
	if _, err := sm.AppendCompaction("handoff only", nil, 250); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(tsmcUserMsg("after handoff")); err != nil {
		t.Fatal(err)
	}
	handoffCtx := sm.BuildSessionProjection()
	if !slices.Equal(tsmcMsgRoles(handoffCtx.Messages), []string{"system", "compactionSummary", "user"}) {
		t.Fatalf("roles after self-ref compaction = %v", tsmcMsgRoles(handoffCtx.Messages))
	}
	if tsmcMsgText(handoffCtx.Messages[1]) != "handoff only" || tsmcMsgText(handoffCtx.Messages[2]) != "after handoff" {
		t.Fatalf("unexpected handoff messages")
	}
}

func TestSessionContextEditsProjection(t *testing.T) {
	// 1. Null edit omits target only from model projection, keeping entry in GetBranch.
	sm := SessionManagerInMemory()
	if _, err := sm.AppendMessage(tsmcUserMsg("request")); err != nil {
		t.Fatal(err)
	}
	aId, err := sm.AppendMessage(tsmcAssistantMsg("partial"))
	if err != nil {
		t.Fatal(err)
	}
	rId, err := sm.AppendMessage(tsmcToolResultMsg("raw output"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendContextEdit(aId, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendContextEdit(rId, nil); err != nil {
		t.Fatal(err)
	}
	if roles := tsmcMsgRoles(sm.BuildSessionProjection().Messages); !slices.Equal(roles, []string{"user"}) {
		t.Fatalf("projected roles = %v, want [user]", roles)
	}
	if msgEntry, ok := sm.GetEntry(rId).(*SessionMessageEntry); !ok || tsmcMsgText(msgEntry.Message) != "raw output" {
		t.Fatalf("raw entry mutated unexpectedly: %+v", sm.GetEntry(rId))
	}

	// 2. Replacement content only replaces content and latest edit wins (omission -> restoration).
	sm2 := SessionManagerInMemory()
	targetId, err := sm2.AppendMessage(tsmcAssistantMsg("original", 42))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm2.AppendContextEdit(targetId, &ContextEditReplacement{
		Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "first"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm2.AppendContextEdit(targetId, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sm2.AppendContextEdit(targetId, &ContextEditReplacement{
		Content: ai.UserMessageContent{Text: tsmcStrPtr("restored")},
	}); err != nil {
		t.Fatal(err)
	}

	proj2 := sm2.BuildSessionProjection()
	if len(proj2.Messages) != 1 {
		t.Fatalf("len(proj2.Messages) = %d, want 1", len(proj2.Messages))
	}
	asstProj, ok := proj2.Messages[0].(*ai.AssistantMessage)
	if !ok || tsmcMsgText(asstProj) != "restored" || asstProj.Usage.TotalTokens != 42 {
		t.Fatalf("unexpected projected assistant: %+v", proj2.Messages[0])
	}
	if rawEntry := sm2.GetEntry(targetId).(*SessionMessageEntry); tsmcMsgText(rawEntry.Message) != "original" {
		t.Fatalf("original assistant message mutated: %q", tsmcMsgText(rawEntry.Message))
	}

	// 3. String replacement normalized for array-only assistant and toolResult roles.
	editResId, err := sm2.AppendMessage(tsmcToolResultMsg("orig result"))
	if err != nil {
		t.Fatal(err)
	}
	resEditEntryId, err := sm2.AppendContextEdit(editResId, &ContextEditReplacement{
		Content: ai.UserMessageContent{Text: tsmcStrPtr("result replacement")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resEdit := sm2.GetEntry(resEditEntryId).(*ContextEditEntry)
	if resEdit.Replacement == nil || len(resEdit.Replacement.Content.Blocks) != 1 || resEdit.Replacement.Content.Text != nil {
		t.Fatalf("expected string replacement normalized to Blocks on toolResult edit, got %+v", resEdit.Replacement)
	}
	projAfterRes := sm2.BuildSessionProjection()
	if len(projAfterRes.Messages) != 2 || tsmcMsgText(projAfterRes.Messages[1]) != "result replacement" {
		t.Fatalf("projAfterRes[1] = %+v", projAfterRes.Messages)
	}

	// 4. Context edits remain branch-relative.
	smBranch := SessionManagerInMemory()
	bUserId, err := smBranch.AppendMessage(tsmcUserMsg("orig user"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smBranch.AppendContextEdit(bUserId, &ContextEditReplacement{
		Content: ai.UserMessageContent{Text: tsmcStrPtr("edited user")},
	}); err != nil {
		t.Fatal(err)
	}
	if tsmcMsgText(smBranch.BuildSessionProjection().Messages[0]) != "edited user" {
		t.Fatal("expected edited user before branching")
	}
	if err := smBranch.Branch(bUserId); err != nil {
		t.Fatal(err)
	}
	if tsmcMsgText(smBranch.BuildSessionProjection().Messages[0]) != "orig user" {
		t.Fatal("expected original user after branching before edit entry")
	}

	// 5. Post-compaction context edit modifies retained pre-compaction entry; repeated compaction keeps newest summary.
	smComp := SessionManagerInMemory()
	if _, err := smComp.AppendMessage(tsmcUserMsg("summarized first")); err != nil {
		t.Fatal(err)
	}
	retainedId, err := smComp.AppendMessage(tsmcUserMsg("original retained"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smComp.AppendCompaction("first summary", &retainedId, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := smComp.AppendMessage(tsmcAssistantMsg("after first compaction")); err != nil {
		t.Fatal(err)
	}
	if _, err := smComp.AppendCompaction("second summary", &retainedId, 80); err != nil {
		t.Fatal(err)
	}
	if _, err := smComp.AppendContextEdit(retainedId, &ContextEditReplacement{
		Content: ai.UserMessageContent{Text: tsmcStrPtr("edited retained")},
	}); err != nil {
		t.Fatal(err)
	}

	projComp := smComp.BuildSessionProjection()
	var summaries []string
	var texts []string
	for _, m := range projComp.Messages {
		texts = append(texts, tsmcMsgText(m))
		if cs, ok := m.(*CompactionSummaryMessage); ok {
			summaries = append(summaries, cs.Summary)
		}
	}
	if !slices.Equal(summaries, []string{"second summary"}) {
		t.Fatalf("summaries = %v, want [second summary]", summaries)
	}
	if !slices.Equal(texts, []string{"second summary", "edited retained", "after first compaction"}) {
		t.Fatalf("texts = %v", texts)
	}

	// 6. AppendContextEdit rejects target missing from current branch and empty replacement.
	if _, err := smComp.AppendContextEdit("nonexistent", nil); err == nil {
		t.Fatal("expected error for missing context edit target")
	}
	if _, err := smComp.AppendContextEdit(retainedId, &ContextEditReplacement{}); err == nil {
		t.Fatal("expected error for empty replacement")
	}
}
