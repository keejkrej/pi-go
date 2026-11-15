package core

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestCompactionShouldCompactAndTokens(t *testing.T) {
	if got := CalculateContextTokens(ai.Usage{Input: 100, Output: 50, CacheRead: 200, CacheWrite: 25, TotalTokens: 375}); got != 375 {
		t.Fatalf("expected CalculateContextTokens=375, got %d", got)
	}
	if ShouldCompact(100_000, 128_000, CompactionSettings{Enabled: false, ReserveTokens: 16384}) {
		t.Fatal("ShouldCompact must return false when disabled")
	}
	if !ShouldCompact(112_000, 128_000, CompactionSettings{Enabled: true, ReserveTokens: 16384}) {
		t.Fatal("expected ShouldCompact=true when > contextWindow - reserveTokens")
	}
	if ShouldCompact(111_000, 128_000, CompactionSettings{Enabled: true, ReserveTokens: 16384}) {
		t.Fatal("expected ShouldCompact=false below threshold")
	}
}

func TestFindCutPointAndPrepareCompaction(t *testing.T) {
	user := func(id, parent string, text string) *SessionMessageEntry {
		var p *string
		if parent != "" {
			cp := parent
			p = &cp
		}
		return &SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", Id: id, ParentId: p},
			Message: &ai.UserMessage{
				Role:    "user",
				Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: text}}},
			},
		}
	}
	asst := func(id, parent, text string, usage ai.Usage) *SessionMessageEntry {
		cp := parent
		return &SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", Id: id, ParentId: &cp},
			Message: &ai.AssistantMessage{
				Role:       "assistant",
				StopReason: ai.StopReasonStop,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
				Usage:      usage,
			},
		}
	}

	entries := []SessionEntry{
		user("u1", "", strings.Repeat("a", 8000)),
		asst("a1", "u1", strings.Repeat("b", 8000), ai.Usage{Input: 2000, Output: 2000, TotalTokens: 4000}),
		user("u2", "a1", strings.Repeat("c", 400)),
		asst("a2", "u2", strings.Repeat("d", 400), ai.Usage{Input: 4100, Output: 100, TotalTokens: 4200}),
	}

	prep := PrepareCompaction(entries, CompactionSettings{
		Enabled:          true,
		ReserveTokens:    1000,
		KeepRecentTokens: 150,
	})
	if prep == nil {
		t.Fatal("expected non-nil CompactionPreparation")
	}
	if prep.FirstKeptEntryId != "u2" {
		t.Fatalf("expected FirstKeptEntryId=u2, got %q", prep.FirstKeptEntryId)
	}
	if len(prep.MessagesToSummarize) == 0 {
		t.Fatal("expected history messages to summarize")
	}
	if prep.TokensBefore != 4200 {
		t.Fatalf("expected TokensBefore=4200 from assistant a2 usage, got %d", prep.TokensBefore)
	}

	// Trailing CompactionEntry returns nil preparation.
	withTrailing := append(entries, &CompactionEntry{
		SessionEntryBase: SessionEntryBase{Type: "compaction", Id: "c1"},
		Summary:          "already",
		FirstKeptEntryId: "u2",
	})
	if got := PrepareCompaction(withTrailing, DefaultCompactionSettings); got != nil {
		t.Fatalf("expected nil PrepareCompaction after trailing compaction, got %+v", got)
	}
}

func TestCompactGeneratesSummaryAndTracksFileOps(t *testing.T) {
	args := jsonx.NewObject()
	args.Set("path", "foo/bar.go")
	writeArgs := jsonx.NewObject()
	writeArgs.Set("path", "foo/out.go")

	u1ID := "u1"
	a1ID := "a1"
	u1 := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: "message", Id: u1ID},
		Message: &ai.UserMessage{
			Role:    "user",
			Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "Read bar.go and write out.go"}}},
		},
	}
	a1 := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: "message", Id: a1ID, ParentId: &u1ID},
		Message: &ai.AssistantMessage{
			Role:       "assistant",
			StopReason: ai.StopReasonStop,
			Content: ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "read", Arguments: args},
				&ai.ToolCall{Type: "toolCall", Id: "t2", Name: "write", Arguments: writeArgs},
			},
		},
	}
	u2 := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: "message", Id: "u2", ParentId: &a1ID},
		Message: &ai.UserMessage{
			Role:    "user",
			Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "Recent user turn"}}},
		},
	}

	prep := PrepareCompaction([]SessionEntry{u1, a1, u2}, CompactionSettings{
		Enabled:          true,
		ReserveTokens:    100,
		KeepRecentTokens: 4,
	})
	if prep == nil {
		t.Fatal("expected non-nil CompactionPreparation")
	}

	model := &ai.Model{
		BaseModel:     ai.BaseModel{Id: "claude-sonnet", Name: "Claude", Provider: "anthropic", Api: "anthropic-messages"},
		ContextWindow: 100_000,
		MaxTokens:     4096,
	}
	streamFn := agent.StreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		s := ai.NewAssistantMessageEventStream()
		s.End(&ai.AssistantMessage{
			Role:       "assistant",
			StopReason: ai.StopReasonStop,
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "## Goal\nDone testing."}},
			Usage:      ai.Usage{Input: 50, Output: 20, TotalTokens: 70},
		})
		return s, nil
	})
	apiKey := "test-key"
	res, err := Compact(context.Background(), *prep, model, &apiKey, &CompactRunOptions{StreamFn: streamFn})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !strings.Contains(res.Summary, "## Goal\nDone testing.") {
		t.Fatalf("missing LLM summary in %q", res.Summary)
	}
	if !strings.Contains(res.Summary, `<read-files>`) || !strings.Contains(res.Summary, `foo/bar.go`) {
		t.Fatalf("expected tracked read file in summary:\n%s", res.Summary)
	}
	if !strings.Contains(res.Summary, `<modified-files>`) || !strings.Contains(res.Summary, `foo/out.go`) {
		t.Fatalf("expected tracked modified file in summary:\n%s", res.Summary)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 70 {
		t.Fatalf("expected accumulated usage TotalTokens=70, got %+v", res.Usage)
	}
}
