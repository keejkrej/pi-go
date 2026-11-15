// Ported from packages/coding-agent/test/suite/regressions/7925-toolcall-start-metadata.test.ts (pi v1.5.2).

package modes_test

import (
	"context"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRegression7925_ToolcallStartMetadata(t *testing.T) {
	ctx := context.Background()
	h, err := testkit.CreateSuiteHarness(ctx)
	if err != nil {
		t.Fatalf("CreateSuiteHarness: %v", err)
	}
	defer h.Cleanup()

	callId := "call_7925"
	toolUse := ai.StopReasonToolUse
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{
					Block: providers.FauxToolCall(
						"write",
						jsonx.ObjectOf("path", "output.txt", "content", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
						&providers.FauxToolCallOptions{Id: &callId},
					),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
		},
	})

	if err := h.Session.Prompt(ctx, "write a file"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	var update *core.AgentSessionEventMessageUpdate
	var am *ai.AssistantMessage
	for _, ev := range h.EventsOfType("message_update") {
		muEv, ok := ev.(*core.AgentSessionEventMessageUpdate)
		if !ok {
			continue
		}
		if _, isStart := muEv.AssistantMessageEvent.(*ai.AssistantMessageEventToolcallStart); isStart {
			assistantMsg, ok := muEv.Message.(*ai.AssistantMessage)
			if !ok {
				t.Fatalf("expected assistant message on toolcall_start update")
			}
			update = muEv
			am = assistantMsg
			break
		}
	}
	if update == nil {
		t.Fatalf("expected toolcall_start assistant update")
	}

	wireEv, err := modes.ToJsonEvent(update)
	if err != nil {
		t.Fatalf("ToJsonEvent: %v", err)
	}
	wireUpdate, ok := wireEv.(*modes.JsonMessageUpdateEvent)
	if !ok {
		t.Fatalf("expected *modes.JsonMessageUpdateEvent, got %T", wireEv)
	}
	if wireUpdate.Usage != am.Usage {
		t.Fatalf("usage mismatch: got %+v want %+v", wireUpdate.Usage, am.Usage)
	}
	tcStart, ok := wireUpdate.AssistantMessageEvent.(*modes.JsonAssistantMessageEventToolcallStart)
	if !ok {
		t.Fatalf("expected *modes.JsonAssistantMessageEventToolcallStart, got %T", wireUpdate.AssistantMessageEvent)
	}
	if tcStart.Type != "toolcall_start" || tcStart.ContentIndex != 0 || tcStart.Id != "call_7925" || tcStart.ToolName != "write" {
		t.Fatalf("unexpected toolcall_start wire payload: %+v", tcStart)
	}
}
