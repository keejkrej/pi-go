package gui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func tmsgStrPtr(s string) *string { return &s }
func tmsgIntPtr(n int) *int       { return &n }
func tmsgBoolPtr(b bool) *bool    { return &b }

func TestUserAndAssistantMessageCards(t *testing.T) {
	var copied string
	userCard := NewUserMessageCard("Hello **pi**",
		WithUserTimestamp(1700000000000),
		WithUserOnCopy(func(s string) { copied = s }),
	)
	ttUser := ui.NewTester(func(c *ui.Context) {
		userCard.View(c)
	}, 640, 240)
	if !ttUser.HasText("You") || !ttUser.HasText("Hello pi") {
		t.Fatalf("expected user card text, got %v", ttUser.Texts())
	}
	ttUser.Click("Copy")
	if copied != "Hello **pi**" {
		t.Fatalf("copied = %q, want %q", copied, "Hello **pi**")
	}

	// Streaming assistant message with thinking + text + retry + completion usage
	lvl := ai.ModelThinkingLevel("high")
	msg := &ai.AssistantMessage{
		Role:          "assistant",
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-5",
		ThinkingLevel: &lvl,
		Content: ai.ContentList{
			&ai.ThinkingContent{Type: "thinking", Thinking: "step by step secret reasoning"},
			&ai.TextContent{Type: "text", Text: "Answer part 1"},
		},
		StopReason: ai.StopReasonPending,
	}
	asst := NewAssistantMessageCard(msg, WithAssistantStreaming(true))
	asst.SetRetryStatus(1, 3, 2*time.Second, "rate limited")

	ttAsst := ui.NewTester(func(c *ui.Context) {
		asst.View(c)
	}, 720, 420)
	if !ttAsst.HasText("anthropic/claude-sonnet-4-5") || !ttAsst.HasText("Thinking") || !ttAsst.HasText("Retrying (1/3 in 2.0s): rate limited") {
		t.Fatalf("expected assistant header/thinking/retry, got %v", ttAsst.Texts())
	}
	if ttAsst.HasText("step by step secret reasoning") {
		t.Fatalf("expected thinking block collapsed initially, texts=%v", ttAsst.Texts())
	}

	// Expand thinking disclosure by clicking trigger
	ttAsst.Click("Thinking")
	ttAsst.Advance(300 * time.Millisecond)
	if !ttAsst.HasText("step by step secret reasoning") {
		t.Fatalf("expected thinking text after click, got %v", ttAsst.Texts())
	}

	// Complete streaming and verify usage badges + error message variant
	asst.ClearRetryStatus()
	asst.SetStreaming(false)
	dur := int64(1450)
	asst.Update(&ai.AssistantMessage{
		Role:         "assistant",
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-5",
		Content:      ai.ContentList{&ai.TextContent{Type: "text", Text: "Final reply"}},
		Usage:        ai.Usage{Input: 1200, Output: 450, CacheRead: 8000, Cost: ai.UsageCost{Total: 0.015}},
		StopReason:   ai.StopReasonError,
		ErrorMessage: tmsgStrPtr("upstream timeout"),
		DurationMs:   &dur,
	})
	ttAsst.Frame()
	if !ttAsst.HasText("Final reply") || !ttAsst.HasText("Error: upstream timeout") || !ttAsst.HasText("↑1.2k ↓450") {
		t.Fatalf("expected final text, error banner, and usage badge, got %v", ttAsst.Texts())
	}
}

func TestBashExecutionAndSummaryCards(t *testing.T) {
	var cancelled bool
	bashCard := NewBashExecutionCard("ls -la", false, WithBashOnCancel(func() { cancelled = true }))
	for i := 1; i <= 25; i++ {
		bashCard.AppendOutput(fmt.Sprintf("line-%02d\n", i))
	}

	ttBash := ui.NewTester(func(c *ui.Context) {
		bashCard.View(c)
	}, 720, 480)
	if !ttBash.HasText("$ ls -la") || !ttBash.HasText("earlier lines hidden") || !ttBash.HasText("line-25") {
		t.Fatalf("expected bash header & tail preview, got %v", ttBash.Texts())
	}
	if ttBash.HasText("line-01") {
		t.Fatalf("expected early lines hidden while collapsed, got %v", ttBash.Texts())
	}

	ttBash.Click("Cancel")
	if !cancelled {
		t.Fatal("expected cancel callback to fire")
	}

	// Expand and complete with non-zero exit + truncation
	bashCard.SetComplete(tmsgIntPtr(2), false, true, tmsgStrPtr("/tmp/bash.log"), &core.TruncationResult{
		OutputLines: 20,
		TotalLines:  25,
		OutputBytes: 400,
		TotalBytes:  600,
	})
	ttBash.Frame()
	if !ttBash.HasText("exit 2") || !ttBash.HasText("/tmp/bash.log") {
		t.Fatalf("expected exit 2 and truncation path, got %v", ttBash.Texts())
	}
	ttBash.Click("Expand")
	ttBash.Frame()
	if !ttBash.HasText("line-01") {
		t.Fatalf("expected line-01 visible after expand, got %v", ttBash.Texts())
	}

	// Compaction, Branch, Custom, Skill cards
	compCard := NewCompactionSummaryCard(&core.CompactionSummaryMessage{
		Role:         "compactionSummary",
		Summary:      "Preserved goals and file edits",
		TokensBefore: 120000,
	})
	branchCard := NewBranchSummaryCard(&core.BranchSummaryMessage{
		Role:    "branchSummary",
		Summary: "Tried approach A then pivoted",
		FromId:  tmsgStrPtr("entry-abcdef123456"),
	})
	customCard := NewCustomMessageCard(core.CreateCustomMessage("ext-note", "Custom **notice** body", true, nil, ""))
	skillCard := NewSkillInvocationCard(core.ParsedSkillBlock{
		Name:    "my-skill",
		Content: "Skill instructions here",
	})

	ttCards := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			compCard.View(c)
			branchCard.View(c)
			customCard.View(c)
			skillCard.View(c)
		})
	}, 720, 600)
	if !ttCards.HasText("[compaction]") || !ttCards.HasText("[branch]") || !ttCards.HasText("[ext-note]") || !ttCards.HasText("[skill]") {
		t.Fatalf("expected all card badges, got %v", ttCards.Texts())
	}
	if !ttCards.HasText("Custom notice body") {
		t.Fatalf("expected custom markdown body, got %v", ttCards.Texts())
	}
}

func TestHeaderCostQueueAndStatusChrome(t *testing.T) {
	header := NewHeaderAnnouncement(HeaderAnnouncementOptions{
		Version:           "1.5.2",
		ModelLabel:        "anthropic/claude-sonnet-4-5",
		Cwd:               "/tmp/demo",
		Extensions:        []string{"ext1"},
		Skills:            []string{"skill1", "skill2"},
		AnnouncementTitle: "Announcement",
		Warnings:          []string{"Deprecated flag --old"},
	})
	cost := NewCostSummaryCard(
		core.UsageTotals{Input: 5000, Output: 1200, CacheRead: 10000, Cost: 0.084},
		[]core.UsageCostBreakdownEntry{{Key: "anthropic/claude-sonnet-4-5", Cost: 0.084, Tokens: 16200}},
	)
	queue := NewQueueBanner()
	queue.SetQueues([]string{"Focus on tests"}, []string{"Summarize results"})
	status := NewStatusBanner()
	status.SetWorking(true, "Generating...")

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			header.View(c)
			cost.View(c)
			queue.View(c)
			status.View(c)
		})
	}, 760, 720)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{
		"pi", "v1.5.2", "Deprecated flag --old",
		"Session Cost & Usage", "$0.084",
		"Steering (1)", "Follow-up (1)",
		"Generating...",
	} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in rendered chrome:\n%s", want, texts)
		}
	}

	// Dismiss warnings
	tt.Click("Dismiss Warnings")
	tt.Frame()
	if tt.HasText("Deprecated flag --old") {
		t.Fatal("expected warnings hidden after dismissal")
	}

	// Concurrent BashExecutionCard AppendOutput stress under -race
	raceBash := NewBashExecutionCard("echo race", true)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			raceBash.AppendOutput("chunk\n")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			ttBash := ui.NewTester(func(c *ui.Context) {
				raceBash.View(c)
			}, 400, 200)
			_ = ttBash.Texts()
		}
	}()
	wg.Wait()
}
