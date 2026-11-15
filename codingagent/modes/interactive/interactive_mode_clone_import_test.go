// Ported from packages/coding-agent/test/interactive-mode-clone-command.test.ts and
// interactive-mode-import-command.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai/providers"
)

func TestSlashNameSessionExportImportCloneNewAndDiagnostics(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode
	session := mode.Session()

	// Empty session /clone warns gently instead of panicking.
	_, err := mode.imcmdHandleSlashCommand(context.Background(), "/clone")
	if err != nil {
		t.Fatalf("/clone on empty session: %v", err)
	}
	tithDrainUI(t, mode)
	if chat := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100); !strings.Contains(chat, "Nothing to clone") {
		t.Fatalf("expected empty clone warning, got:\n%s", chat)
	}

	// Produce one real turn so session has messages + leaf entry.
	mf.Faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("cloneable answer"), nil)},
	})
	if err := session.Prompt(context.Background(), "turn before clone", nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	tithDrainUI(t, mode)

	// /name sets session name and updates terminal title.
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/name feature-alpha"); !handled || err != nil {
		t.Fatalf("/name handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	if got := session.SessionManager.GetSessionName(); got == nil || *got != "feature-alpha" {
		t.Fatalf("expected session name feature-alpha, got %v", got)
	}

	// /session renders session summary cleanly.
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/session"); !handled || err != nil {
		t.Fatalf("/session handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	if chat := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100); !strings.Contains(chat, "feature-alpha") {
		t.Fatalf("expected session info output containing name, got:\n%s", chat)
	}

	// /export <file> writes HTML transcript, and /import <jsonl> restores an exported JSONL file.
	exportHTML := filepath.Join(t.TempDir(), "transcript.html")
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/export "+exportHTML); !handled || err != nil {
		t.Fatalf("/export handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	if info, statErr := os.Stat(exportHTML); statErr != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty HTML export at %s: stat=%v err=%v", exportHTML, info, statErr)
	}

	exportJSONL := filepath.Join(t.TempDir(), "portable.jsonl")
	if err := ExportSessionForShare(exportJSONL, session); err != nil {
		t.Fatalf("ExportSessionForShare: %v", err)
	}
	origSessionID := session.SessionId()

	// /clone forks at leaf into a new session ID and keeps messages.
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/clone"); !handled || err != nil {
		t.Fatalf("/clone handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	clonedSession := mode.Session()
	if clonedSession.SessionId() == origSessionID {
		t.Fatalf("expected /clone to create a distinct session ID from %q", origSessionID)
	}
	if len(clonedSession.Messages()) < 2 {
		t.Fatalf("expected cloned session to preserve messages, got %d", len(clonedSession.Messages()))
	}

	// /new resets to a fresh empty session.
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/new"); !handled || err != nil {
		t.Fatalf("/new handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	if got := len(mode.Session().Messages()); got != 0 {
		t.Fatalf("expected /new to start empty session, got %d messages", got)
	}

	// /import <portable.jsonl> switches back into the exported session history.
	if handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/import "+exportJSONL); !handled || err != nil {
		t.Fatalf("/import handled=%v err=%v", handled, err)
	}
	tithDrainUI(t, mode)
	if got := len(mode.Session().Messages()); got < 2 {
		t.Fatalf("expected /import to restore messages, got %d", got)
	}

	// /hotkeys and /changelog slash commands execute cleanly without error.
	for _, cmd := range []string{"/hotkeys", "/changelog"} {
		if handled, err := mode.imcmdHandleSlashCommand(context.Background(), cmd); !handled || err != nil {
			t.Fatalf("%s handled=%v err=%v", cmd, handled, err)
		}
	}
	// Shift+Ctrl+D / imcmdHandleDebugCommand works as the debug callback cleanly.
	mode.imcmdHandleDebugCommand()
	tithDrainUI(t, mode)
	chat := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100)
	if !strings.Contains(chat, "Imported session from") {
		t.Fatalf("expected import confirmation in chat:\n%s", chat)
	}
}
