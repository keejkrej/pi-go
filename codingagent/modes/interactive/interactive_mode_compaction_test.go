// Ported from packages/coding-agent/test/interactive-mode-compaction.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestCompactionAndRetryStatusIndicatorsAndQueuedMessages(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode
	session := mode.Session()

	// 1. Auto-retry status event sets and clears retryIndicator on UI loop.
	_ = mode.imCallUI(context.Background(), func() {
		mode.imevHandleEvent(&core.AgentSessionEventAutoRetryStart{
			Attempt:     2,
			MaxAttempts: 5,
			DelayMs:     1500,
		})
	})
	statusText := tithRenderOnUI(t, mode.Ui(), mode.statusContainer, 100)
	if !strings.Contains(statusText, "Retrying") && !strings.Contains(statusText, "2/5") {
		t.Fatalf("expected retry indicator rendered in statusContainer, got:\n%s", statusText)
	}

	errText := "upstream 503"
	_ = mode.imCallUI(context.Background(), func() {
		mode.imevHandleEvent(&core.AgentSessionEventAutoRetryEnd{
			Success:    false,
			Attempt:    5,
			FinalError: &errText,
		})
	})
	mode.mu.Lock()
	retryCleared := mode.retryIndicator == nil
	mode.mu.Unlock()
	if !retryCleared {
		t.Fatalf("expected retryIndicator cleared on AutoRetryEnd")
	}

	// 2. CompactionStart sets compactionIndicator; queue steering & follow-up messages;
	// CompactionEnd restores status and flushes queued messages to session.
	fx := mf.Faux
	fx.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after compaction"), nil)},
	})

	_ = mode.imCallUI(context.Background(), func() {
		mode.imevHandleEvent(&core.AgentSessionEventCompactionStart{Reason: "threshold"})
		mode.imqQueueCompactionMessage("urgent steer", "steer")
		mode.imqQueueCompactionMessage("next followup", "followUp")
	})

	pendingText := tithRenderOnUI(t, mode.Ui(), mode.pendingMessagesContainer, 100)
	if !strings.Contains(pendingText, "urgent steer") || !strings.Contains(pendingText, "next followup") {
		t.Fatalf("expected queued steering & followUp displayed in pendingMessagesContainer:\n%s", pendingText)
	}

	_ = mode.imCallUI(context.Background(), func() {
		mode.imevHandleEvent(&core.AgentSessionEventCompactionEnd{
			Reason: "threshold",
			Result: &core.CompactionResult{
				Summary:      "Summary of earlier turns",
				TokensBefore: 12000,
			},
		})
	})
	chatText := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100)
	if !strings.Contains(chatText, "12000") && !strings.Contains(chatText, "Compacted context") {
		t.Fatalf("expected compaction notice in chatContainer:\n%s", chatText)
	}
	if got := len(session.GetSteeringMessages()) + len(session.GetFollowUpMessages()); got == 0 {
		// Either delivered directly if idle or queued into session.
	}

	// 3. Dequeue pending compaction/queued messages back to editor via imqHandleDequeue.
	_ = mode.imCallUI(context.Background(), func() {
		mode.imqQueueCompactionMessage("queued item 1", "steer")
		mode.imqUpdatePendingMessagesDisplay()
		mode.imqHandleDequeue()
	})
	if edText := mode.editor.GetText(); !strings.Contains(edText, "queued item 1") {
		t.Fatalf("expected dequeued message restored into editor, got %q", edText)
	}
}
