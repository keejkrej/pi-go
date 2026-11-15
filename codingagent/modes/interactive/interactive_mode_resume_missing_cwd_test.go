// Ported from packages/coding-agent/test/interactive-mode-resume-missing-cwd.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestResumeSessionPromptsOnMissingCwdAndUsesCurrentDirectory(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode

	// Create a persisted session bound to a temporary directory that we then delete.
	vanishedDir := filepath.Join(t.TempDir(), "deleted-workspace")
	if err := os.MkdirAll(vanishedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	otherSM, err := core.SessionManagerCreate(vanishedDir, mf.SessionDir)
	if err != nil {
		t.Fatalf("SessionManagerCreate: %v", err)
	}
	if _, err := otherSM.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("seed message"), nil)); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := otherSM.AppendSessionInfo("resumable-missing-cwd"); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	sessionFile := otherSM.GetSessionFile()
	if sessionFile == nil || *sessionFile == "" {
		t.Fatalf("expected persisted session file")
	}
	if _, err := os.Stat(*sessionFile); err != nil {
		t.Fatalf("expected session file on disk before removing cwd: %v", err)
	}
	if err := os.RemoveAll(vanishedDir); err != nil {
		t.Fatalf("RemoveAll(%s): %v", vanishedDir, err)
	}

	// Resuming the vanished-cwd session invokes imselPromptMissingSessionCwd dialog.
	resumedDone := make(chan struct{})
	go func() {
		defer close(resumedDone)
		mode.imselResumeSession(context.Background(), *sessionFile)
	}()

	tithWaitUntil(t, 3*time.Second, "missing cwd dialog displayed", func() bool {
		return strings.Contains(tithRenderOnUI(t, mode.Ui(), mode.editorContainer, 100), "Continue in ")
	})
	// Confirm "Continue in <cwd>" (first option)
	mf.Term.tithSendInput("\r")

	select {
	case <-resumedDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("imselResumeSession did not complete after confirming missing-cwd fallback")
	}

	if got := mode.Session().SessionManager.GetSessionName(); got == nil || *got != "resumable-missing-cwd" {
		t.Fatalf("expected resumed session name resumable-missing-cwd, got %v", got)
	}
	if got := mode.runtime.Cwd(); got != mf.Cwd {
		t.Fatalf("expected session rebound to current cwd %q, got %q", mf.Cwd, got)
	}
}
