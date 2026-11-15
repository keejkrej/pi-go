// Ported from packages/coding-agent/test/interactive-mode-tree-navigation.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai/providers"
)

func timtnWaitEditorChildText(t *testing.T, mode *InteractiveMode, substr string) {
	t.Helper()
	tithWaitUntil(t, 3*time.Second, "editorContainer shows "+substr, func() bool {
		return strings.Contains(tithRenderOnUI(t, mode.Ui(), mode.editorContainer, 100), substr)
	})
}

func TestTreeNavigationRebuildsTranscriptAndTreeSelectorCancelsCleanly(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode
	session := mode.Session()

	mf.Faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply one"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply two"), nil)},
	})
	if err := session.Prompt(context.Background(), "question one", nil); err != nil {
		t.Fatalf("Prompt 1: %v", err)
	}
	tithDrainUI(t, mode)
	firstLeaf := session.SessionManager.GetLeafId()
	if firstLeaf == nil {
		t.Fatalf("expected first leaf ID")
	}

	if err := session.Prompt(context.Background(), "question two", nil); err != nil {
		t.Fatalf("Prompt 2: %v", err)
	}
	tithDrainUI(t, mode)

	// Open /tree selector and cancel via Escape -> editor returns and no branch change happens.
	mode.imPostUI(func() {
		mode.imselShowTreeSelector()
	})
	timtnWaitEditorChildText(t, mode, "question one")
	mf.Term.tithSendInput("\x1b")
	tithWaitUntil(t, 2*time.Second, "editor restored after cancelling tree selector", func() bool {
		var restored bool
		_ = mode.imCallUI(context.Background(), func() {
			children := mode.editorContainer.Children()
			restored = len(children) == 1 && children[0] == mode.editor
		})
		return restored
	})

	// Navigate back to firstLeaf cleanly through imselNavigateTreeWithOptions ("No summary").
	navDone := make(chan struct{})
	go func() {
		defer close(navDone)
		mode.imselNavigateTreeWithOptions(*firstLeaf)
	}()
	timtnWaitEditorChildText(t, mode, "Summarize branch?")
	mf.Term.tithSendInput("\r")
	select {
	case <-navDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("imselNavigateTreeWithOptions did not complete")
	}
	tithDrainUI(t, mode)

	chat := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100)
	if !strings.Contains(chat, "reply one") {
		t.Fatalf("expected reply one retained in transcript:\n%s", chat)
	}
	if strings.Contains(chat, "reply two") {
		t.Fatalf("did not expect reply two after navigating back to first leaf:\n%s", chat)
	}
}
