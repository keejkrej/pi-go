// Ported from packages/coding-agent/test/suite/regressions/7911-json-stream-usage.test.ts (pi v1.5.2).

package modes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRegression7911_JsonStreamUsage(t *testing.T) {
	ctx := context.Background()
	h, err := testkit.CreateSuiteHarness(ctx)
	if err != nil {
		t.Fatalf("CreateSuiteHarness: %v", err)
	}
	defer h.Cleanup()

	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello"), nil),
		},
	})

	if err := h.Session.Prompt(ctx, "respond"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	var found *core.AgentSessionEventMessageUpdate
	var foundMsg *ai.AssistantMessage
	for _, ev := range h.EventsOfType("message_update") {
		muEv, ok := ev.(*core.AgentSessionEventMessageUpdate)
		if !ok {
			continue
		}
		if am, ok := muEv.Message.(*ai.AssistantMessage); ok && am != nil && am.Usage.TotalTokens > 0 {
			found = muEv
			foundMsg = am
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an assistant message_update with populated usage")
	}

	wireEv, err := modes.ToJsonEvent(found)
	if err != nil {
		t.Fatalf("ToJsonEvent: %v", err)
	}
	wireUpdate, ok := wireEv.(*modes.JsonMessageUpdateEvent)
	if !ok {
		t.Fatalf("expected *modes.JsonMessageUpdateEvent, got %T", wireEv)
	}
	if wireUpdate.Usage.TotalTokens != foundMsg.Usage.TotalTokens ||
		wireUpdate.Usage.Input != foundMsg.Usage.Input ||
		wireUpdate.Usage.Output != foundMsg.Usage.Output {
		t.Fatalf("usage mismatch: got %+v want %+v", wireUpdate.Usage, foundMsg.Usage)
	}

	encoded, err := jsonx.Marshal(wireUpdate)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	raw := string(encoded)
	if strings.Contains(raw, `"message":`) {
		t.Fatalf("unexpected message property in %s", raw)
	}
	if strings.Contains(raw, `"partial":`) {
		t.Fatalf("unexpected partial property in %s", raw)
	}
}
