// Ported from packages/durable/test/examples/ (pi v1.0.0).

package testkit

import (
	"bytes"
	"testing"
)

func TestExamples(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*bytes.Buffer) error
	}{
		{"00-conversation", func(b *bytes.Buffer) error { return Example00Conversation(b) }},
		{"01-documents", func(b *bytes.Buffer) error { return Example01Documents(b) }},
		{"02-forks", func(b *bytes.Buffer) error { return Example02Forks(b) }},
		{"03-owned-conversations", func(b *bytes.Buffer) error { return Example03OwnedConversations(b) }},
		{"04-chord-state", func(b *bytes.Buffer) error { return Example04ChordState(b) }},
		{"05-watches", func(b *bytes.Buffer) error { return Example05Watches(b) }},
		{"06-harness", func(b *bytes.Buffer) error { return Example06Harness(b) }},
		{"07-configuration", func(b *bytes.Buffer) error { return Example07Configuration(b) }},
		{"08-harness-conversations", func(b *bytes.Buffer) error { return Example08HarnessConversations(b) }},
		{"09-context", func(b *bytes.Buffer) error { return Example09Context(b) }},
		{"10-registry-reload", func(b *bytes.Buffer) error { return Example10RegistryReload(b) }},
		{"11-extension-state", func(b *bytes.Buffer) error { return Example11ExtensionState(b) }},
		{"12-tasks", func(b *bytes.Buffer) error { return Example12Tasks(b) }},
		{"13-recovery", func(b *bytes.Buffer) error { return Example13Recovery(b) }},
		{"14-chat", func(b *bytes.Buffer) error { return Example14Chat(b) }},
		{"15-system-prompt", func(b *bytes.Buffer) error { return Example15SystemPrompt(b) }},
		{"17-coding-tools", func(b *bytes.Buffer) error { return Example17CodingTools(b) }},
		{"18-print", func(b *bytes.Buffer) error { return Example18Print(b) }},
		{"19-json", func(b *bytes.Buffer) error { return Example19Json(b) }},
		{"20-inbox", func(b *bytes.Buffer) error { return Example20Inbox(b) }},
		{"21-late-join", func(b *bytes.Buffer) error { return Example21LateJoin(b) }},
		{"22-subagent-foreground", func(b *bytes.Buffer) error { return Example22SubagentForeground(b) }},
		{"23-subagent-background", func(b *bytes.Buffer) error { return Example23SubagentBackground(b) }},
		{"24-child-tasks", func(b *bytes.Buffer) error { return Example24ChildTasks(b) }},
		{"25-compaction", func(b *bytes.Buffer) error { return Example25Compaction(b) }},
		{"26-coding-agent", func(b *bytes.Buffer) error { return Example26CodingAgent(b) }},
		{"27-plan-mode", func(b *bytes.Buffer) error { return Example27PlanMode(b) }},
		{"28-reviewer", func(b *bytes.Buffer) error { return Example28Reviewer(b) }},
		{"29-sandbox-per-conversation", func(b *bytes.Buffer) error { return Example29SandboxPerConversation(b) }},
		{"30-tool-override", func(b *bytes.Buffer) error { return Example30ToolOverride(b) }},
		{"31-reload-and-restart", func(b *bytes.Buffer) error { return Example31ReloadAndRestart(b) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.fn(&buf); err != nil {
				t.Fatalf("%s failed: %v\noutput:\n%s", tc.name, err, buf.String())
			}
			if buf.Len() == 0 {
				t.Fatalf("%s produced empty output", tc.name)
			}
		})
	}
}
