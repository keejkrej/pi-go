package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tcbsStrPtr(s string) *string { return &s }

func tcbsTestModel(reasoning bool) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "test-model",
			Name:     "Test Model",
			Api:      "anthropic-messages",
			Provider: "anthropic",
			BaseUrl:  "https://api.anthropic.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     reasoning,
		ContextWindow: 200_000,
		MaxTokens:     8192,
	}
}

func tcbsUserEntry(id string, parentId *string, text string) *SessionMessageEntry {
	return &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			Id:        id,
			ParentId:  parentId,
			Timestamp: "1970-01-01T00:00:00.001Z",
		},
		Message: &ai.UserMessage{
			Role:    "user",
			Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: text}}},
		},
	}
}

func tcbsResponse(model *ai.Model, stopReason ai.StopReason, content ai.ContentList) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Api:        ai.Api(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		StopReason: stopReason,
		Content:    content,
	}
}

func TestGenerateBranchSummaryThinkingToolCallLengthAndContextEdits(t *testing.T) {
	model := tcbsTestModel(false)
	reasoningModel := tcbsTestModel(true)
	entries := []SessionEntry{tcbsUserEntry("branch-user", nil, "Abandoned request")}

	t.Run("passes thinking level only for reasoning-capable models", func(t *testing.T) {
		var seen []string
		streamFn := agent.StreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			if opts != nil && opts.Reasoning != nil {
				seen = append(seen, string(*opts.Reasoning))
			} else {
				seen = append(seen, "")
			}
			s := ai.NewAssistantMessageEventStream()
			s.End(tcbsResponse(model, ai.StopReasonStop, ai.ContentList{&ai.TextContent{Type: "text", Text: "summary"}}))
			return s, nil
		})

		xhigh := agent.ThinkingLevel("xhigh")
		off := agent.ThinkingLevel("off")
		high := agent.ThinkingLevel("high")

		if _, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: reasoningModel, StreamFn: streamFn, ThinkingLevel: &xhigh,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: reasoningModel, StreamFn: streamFn, ThinkingLevel: &off,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: model, StreamFn: streamFn, ThinkingLevel: &high,
		}); err != nil {
			t.Fatal(err)
		}

		if len(seen) != 3 || seen[0] != "xhigh" || seen[1] != "" || seen[2] != "" {
			t.Fatalf("unexpected seen reasoning levels: %+v", seen)
		}
	})

	t.Run("does not override tool choice", func(t *testing.T) {
		var sawToolChoice *ai.ToolChoice
		called := false
		streamFn := agent.StreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			called = true
			if opts != nil {
				sawToolChoice = opts.ToolChoice
			}
			s := ai.NewAssistantMessageEventStream()
			s.End(tcbsResponse(model, ai.StopReasonStop, ai.ContentList{&ai.TextContent{Type: "text", Text: "summary"}}))
			return s, nil
		})

		if _, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: model, StreamFn: streamFn,
		}); err != nil {
			t.Fatal(err)
		}
		if !called || sawToolChoice != nil {
			t.Fatalf("expected called=true and nil toolChoice, got called=%v toolChoice=%+v", called, sawToolChoice)
		}
	})

	t.Run("rejects tool calls from branch summaries", func(t *testing.T) {
		args := jsonx.NewObject()
		args.Set("path", "README.md")
		streamFn := agent.StreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			s := ai.NewAssistantMessageEventStream()
			s.End(tcbsResponse(model, ai.StopReasonToolUse, ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "tool-call-1", Name: "read", Arguments: args},
			}))
			return s, nil
		})

		res, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: model, StreamFn: streamFn,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Error == nil || *res.Error != "Branch summarization attempted to call a tool" {
			t.Fatalf("unexpected error result: %+v", res)
		}
	})

	t.Run("rejects length-limited branch summaries", func(t *testing.T) {
		streamFn := agent.StreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			s := ai.NewAssistantMessageEventStream()
			s.End(tcbsResponse(model, ai.StopReasonLength, ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}}))
			return s, nil
		})

		res, err := GenerateBranchSummary(context.Background(), entries, GenerateBranchSummaryOptions{
			Model: model, StreamFn: streamFn,
		})
		if err != nil {
			t.Fatal(err)
		}
		const expected = "Branch summarization failed: generation hit the token cap and the summary is incomplete"
		if res.Error == nil || *res.Error != expected {
			t.Fatalf("expected %q, got %+v", expected, res)
		}
	})

	t.Run("applies context edits to branch entries", func(t *testing.T) {
		errMsg := "lost"
		u1Parent := "u1"
		failedParent := "failed"
		e1Parent := "e1"
		okParent := "ok"
		replaced := &ContextEditReplacement{Content: ai.UserMessageContent{Text: tcbsStrPtr("REPLACED_TEXT")}}

		branch := []SessionEntry{
			tcbsUserEntry("u1", nil, "Investigate login"),
			&SessionMessageEntry{
				SessionEntryBase: SessionEntryBase{Type: "message", Id: "failed", ParentId: &u1Parent},
				Message: &ai.AssistantMessage{
					Role:         "assistant",
					StopReason:   ai.StopReasonError,
					ErrorMessage: &errMsg,
					Content:      ai.ContentList{&ai.TextContent{Type: "text", Text: "FAILED_ATTEMPT"}},
				},
			},
			&ContextEditEntry{
				SessionEntryBase: SessionEntryBase{Type: "context_edit", Id: "e1", ParentId: &failedParent},
				TargetId:         "failed",
				Replacement:      nil,
			},
			&SessionMessageEntry{
				SessionEntryBase: SessionEntryBase{Type: "message", Id: "ok", ParentId: &e1Parent},
				Message: &ai.AssistantMessage{
					Role:       "assistant",
					StopReason: ai.StopReasonStop,
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ORIGINAL_TEXT"}},
				},
			},
			&ContextEditEntry{
				SessionEntryBase: SessionEntryBase{Type: "context_edit", Id: "e2", ParentId: &okParent},
				TargetId:         "ok",
				Replacement:      replaced,
			},
		}

		prep := PrepareBranchEntries(branch)
		serialized := SerializeConversation(ConvertToLlm(prep.Messages))
		if !strings.Contains(serialized, "Investigate login") || strings.Contains(serialized, "FAILED_ATTEMPT") || strings.Contains(serialized, "ORIGINAL_TEXT") || !strings.Contains(serialized, "REPLACED_TEXT") {
			t.Fatalf("unexpected context-edited branch serialization:\n%s", serialized)
		}
	})
}

func TestCollectEntriesForBranchSummary(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "branch-session.jsonl")
	jsonl := strings.Join([]string{
		`{"type":"session","version":3,"id":"sess-branch","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"root","timestamp":1}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"user","content":"common","timestamp":2}}`,
		`{"type":"message","id":"b1","parentId":"a1","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"user","content":"old branch step","timestamp":3}}`,
		`{"type":"message","id":"b2","parentId":"b1","timestamp":"2026-01-01T00:00:04.000Z","message":{"role":"user","content":"old branch answer","timestamp":4}}`,
		`{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"user","content":"new target","timestamp":5}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o600); err != nil {
		t.Fatal(err)
	}
	sm, err := SessionManagerOpen(sessionPath)
	if err != nil {
		t.Fatalf("SessionManagerOpen failed: %v", err)
	}
	oldLeaf := "b2"
	collected := CollectEntriesForBranchSummary(sm, &oldLeaf, "t1")
	if collected.CommonAncestorId == nil || *collected.CommonAncestorId != "a1" {
		t.Fatalf("expected common ancestor a1, got %v", collected.CommonAncestorId)
	}
	if len(collected.Entries) != 2 || collected.Entries[0].GetId() != "b1" || collected.Entries[1].GetId() != "b2" {
		t.Fatalf("unexpected collected branch entries: %+v", collected.Entries)
	}
}
