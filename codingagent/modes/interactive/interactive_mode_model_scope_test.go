// Ported from packages/coding-agent/test/interactive-mode-model-scope.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestModelAndThinkingCommandsAndCycling(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode
	session := mode.Session()

	if got := session.Model().Id; got != "faux-1" {
		t.Fatalf("expected initial model faux-1, got %q", got)
	}

	// /model faux-2 switches model synchronously via command handler
	handled, err := mode.imcmdHandleSlashCommand(context.Background(), "/model faux-2")
	if err != nil || !handled {
		t.Fatalf("/model faux-2 handled=%v err=%v", handled, err)
	}
	if got := session.Model().Id; got != "faux-2" {
		t.Fatalf("expected model switched to faux-2, got %q", got)
	}

	// /thinking high on reasoning-capable faux-2
	handled, err = mode.imcmdHandleSlashCommand(context.Background(), "/thinking high")
	if err != nil || !handled {
		t.Fatalf("/thinking high handled=%v err=%v", handled, err)
	}
	if got := session.ThinkingLevel(); got != agent.ThinkingLevelHigh {
		t.Fatalf("expected ThinkingLevelHigh, got %q", got)
	}

	// /thinking unknown should surface an error in chat container
	_, _ = mode.imcmdHandleSlashCommand(context.Background(), "/thinking bananas")
	tithDrainUI(t, mode)
	chatText := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100)
	if !strings.Contains(chatText, "Invalid thinking level") {
		t.Fatalf("expected invalid thinking level error, got:\n%s", chatText)
	}

	// Scoped model cycling with per-model thinking level
	thinkingLow := agent.ThinkingLevelLow
	thinkingMed := agent.ThinkingLevelMedium
	session.SetScopedModels([]core.ScopedModel{
		{Model: mf.Models[0], ThinkingLevel: &thinkingLow},
		{Model: mf.Models[1], ThinkingLevel: &thinkingMed},
	})

	mode.iminCycleModel("forward")
	tithWaitUntil(t, 2*time.Second, "cycle forward to faux-1", func() bool {
		return session.Model().Id == "faux-1"
	})

	mode.iminCycleModel("backward")
	tithWaitUntil(t, 2*time.Second, "cycle backward to faux-2", func() bool {
		return session.Model().Id == "faux-2"
	})
	if got := session.ThinkingLevel(); got != agent.ThinkingLevelMedium {
		t.Fatalf("expected scoped thinking level medium on faux-2, got %q", got)
	}

	// Direct thinking level cycling
	before := session.ThinkingLevel()
	mode.iminCycleThinkingLevel()
	after := session.ThinkingLevel()
	if after == before {
		t.Fatalf("expected iminCycleThinkingLevel to advance thinking level from %q", before)
	}
}
