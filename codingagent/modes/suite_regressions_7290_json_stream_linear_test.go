// Ported from packages/coding-agent/test/suite/regressions/7290-json-stream-linear.test.ts (pi v1.5.2).

package modes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsr7jslMeasureUpdateBytes(t *testing.T, text string) int {
	t.Helper()
	ctx := context.Background()
	h, err := testkit.CreateSuiteHarness(ctx)
	if err != nil {
		t.Fatalf("CreateSuiteHarness: %v", err)
	}
	defer h.Cleanup()

	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil),
		},
	})

	if err := h.Session.Prompt(ctx, "respond"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	sessionUpdates := h.EventsOfType("message_update")
	if len(sessionUpdates) == 0 {
		t.Fatalf("expected message_update events")
	}

	totalBytes := 0
	for _, ev := range sessionUpdates {
		muEv, ok := ev.(*core.AgentSessionEventMessageUpdate)
		if !ok {
			t.Fatalf("expected *core.AgentSessionEventMessageUpdate, got %T", ev)
		}
		if muEv.Message == nil {
			t.Fatalf("expected session message_update to carry message")
		}

		wire, err := modes.ToJsonEvent(ev)
		if err != nil {
			t.Fatalf("ToJsonEvent: %v", err)
		}
		encoded, err := jsonx.Marshal(wire)
		if err != nil {
			t.Fatalf("jsonx.Marshal: %v", err)
		}
		raw := string(encoded)
		if strings.Contains(raw, `"message":`) {
			t.Fatalf("expected message field stripped from wire update, got %s", raw)
		}
		if strings.Contains(raw, `"partial":`) {
			t.Fatalf("expected partial field stripped from wire update, got %s", raw)
		}
		totalBytes += len(encoded)
	}
	return totalBytes
}

func TestRegression7290_JsonStreamLinear(t *testing.T) {
	smallBytes := tsr7jslMeasureUpdateBytes(t, strings.Repeat("x", 2000))
	largeBytes := tsr7jslMeasureUpdateBytes(t, strings.Repeat("x", 4000))

	if largeBytes <= smallBytes {
		t.Fatalf("expected largeBytes (%d) > smallBytes (%d)", largeBytes, smallBytes)
	}
	ratio := float64(largeBytes) / float64(smallBytes)
	if ratio >= 2.2 {
		t.Fatalf("expected linear scaling (< 2.2x), got %.2fx (%d vs %d)", ratio, largeBytes, smallBytes)
	}
}
