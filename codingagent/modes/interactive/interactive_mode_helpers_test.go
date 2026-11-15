// Unit tests for interactive_mode_helpers.go (PORTING.md §4.2 timh stem).

package interactive

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestInteractiveModeHelperFormattingAndPredicates(t *testing.T) {
	if got := FormatCrashExtensionHint(nil); got != nil {
		t.Fatalf("expected nil hint for empty slice, got %q", *got)
	}
	one := FormatCrashExtensionHint([]string{"foo.ts"})
	if one == nil || !strings.Contains(*one, "Likely caused by extension: foo.ts") {
		t.Fatalf("unexpected single hint: %v", one)
	}
	many := FormatCrashExtensionHint([]string{"a.ts", "b.ts", "a.ts"})
	if many == nil || !strings.Contains(*many, "Likely caused by extensions: a.ts, b.ts") {
		t.Fatalf("unexpected deduped multi hint: %v", many)
	}

	smMem := core.SessionManagerInMemory(t.TempDir())
	if got := FormatResumeCommand(smMem); got != nil {
		t.Fatalf("expected nil resume command for ephemeral session, got %q", *got)
	}

	tempRoot := t.TempDir()
	smFile, err := core.SessionManagerCreate(filepath.Join(tempRoot, "my worktree"), filepath.Join(tempRoot, "sessions"))
	if err != nil {
		t.Fatalf("SessionManagerCreate: %v", err)
	}
	// Persist at least one entry so session file is considered resumable
	if _, err := smFile.AppendSessionInfo("named"); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	res := FormatResumeCommand(smFile)
	if res == nil || !strings.Contains(*res, "--session ") || !strings.Contains(*res, `"`) {
		t.Fatalf("expected quoted resume command, got %v", res)
	}

	if !imhIsAnthropicSubscriptionAuthKey(tithStrPtr("sk-ant-oat01-secret")) {
		t.Fatalf("expected true for sk-ant-oat key")
	}
	if imhIsAnthropicSubscriptionAuthKey(tithStrPtr("sk-ant-api03-normal")) {
		t.Fatalf("expected false for standard API key")
	}
	if !imhIsUnknownModel(&ai.Model{Id: "does-not-exist", Name: "Unknown model"}) {
		t.Fatalf("expected imhIsUnknownModel true")
	}
	if imhIsUnknownModel(&ai.Model{Id: "claude-sonnet", Name: "Claude Sonnet"}) {
		t.Fatalf("expected imhIsUnknownModel false for known model")
	}
}
