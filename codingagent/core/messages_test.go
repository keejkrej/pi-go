package core

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
)

func TestBashExecutionToText(t *testing.T) {
	exit2 := 2
	fullOut := "/tmp/bash.log"
	txt := BashExecutionToText(BashExecutionMessage{
		Role:           "bashExecution",
		Command:        "ls -la",
		Output:         "total 10",
		ExitCode:       &exit2,
		Truncated:      true,
		FullOutputPath: &fullOut,
	})
	for _, want := range []string{
		"Ran `ls -la`",
		"total 10",
		"Command exited with code 2",
		"[Output truncated. Full output: /tmp/bash.log]",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in %q", want, txt)
		}
	}

	cancelled := BashExecutionToText(BashExecutionMessage{
		Command:   "sleep 10",
		Cancelled: true,
	})
	if !strings.Contains(cancelled, "(no output)") || !strings.Contains(cancelled, "(command cancelled)") {
		t.Fatalf("unexpected cancelled text: %q", cancelled)
	}
}

func TestConvertToLlm(t *testing.T) {
	excluded := true
	ts := "2026-01-01T00:00:00.000Z"

	userMsg := &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "hello"}}},
		Timestamp: 100,
	}
	bashIncluded := &BashExecutionMessage{
		Role:      "bashExecution",
		Command:   "echo ok",
		Output:    "ok",
		Timestamp: 200,
	}
	bashExcluded := &BashExecutionMessage{
		Role:               "bashExecution",
		Command:            "secret",
		Output:             "hidden",
		Timestamp:          250,
		ExcludeFromContext: &excluded,
	}
	customText := CreateCustomMessage("ext-note", "custom note", true, map[string]any{"k": "v"}, ts)
	branch := CreateBranchSummaryMessage("branch sum", "leaf-1", ts)
	compact := CreateCompactionSummaryMessage("compact sum", 1234, ts)

	got := ConvertToLlm([]agent.AgentMessage{
		nil,
		userMsg,
		bashIncluded,
		bashExcluded,
		customText,
		branch,
		compact,
	})
	if len(got) != 5 {
		t.Fatalf("expected 5 messages (excluding nil & ExcludeFromContext), got %d", len(got))
	}
	if customText.GetRole() != "custom" || customText.Timestamp == 0 {
		t.Fatalf("unexpected CustomMessage metadata: role=%q ts=%d", customText.GetRole(), customText.Timestamp)
	}
	if !strings.Contains(ai.ContentText(got[2].(*ai.UserMessage).Content), "custom note") {
		t.Fatalf("unexpected converted custom message: %#v", got[2])
	}
	if !strings.Contains(ai.ContentText(got[3].(*ai.UserMessage).Content), BranchSummaryPrefix+"branch sum"+BranchSummarySuffix) {
		t.Fatalf("unexpected branch summary message: %#v", got[3])
	}
	if !strings.Contains(ai.ContentText(got[4].(*ai.UserMessage).Content), CompactionSummaryPrefix+"compact sum"+CompactionSummarySuffix) {
		t.Fatalf("unexpected compaction summary message: %#v", got[4])
	}
}
