// Ported from packages/coding-agent/test/suite/agent-session-tool-orchestration.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	core "github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func TestAgentSessionToolOrchestration(t *testing.T) {
	ctx := context.Background()

	t.Run("top-level tool execution emits start and end events with nil ParentToolCallId", func(t *testing.T) {
		pingTool := &agent.AgentTool{
			Name:        "ping",
			Label:       "Ping",
			Description: "Ping tool",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{pingTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("ping", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "run ping"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		starts := h.EventsOfType("tool_execution_start")
		ends := h.EventsOfType("tool_execution_end")
		if len(starts) != 1 || len(ends) != 1 {
			t.Fatalf("expected 1 start and 1 end event, got %d / %d", len(starts), len(ends))
		}
		st := starts[0].(*core.AgentSessionEventToolExecutionStart)
		en := ends[0].(*core.AgentSessionEventToolExecutionEnd)
		if st.ParentToolCallId != nil || en.ParentToolCallId != nil {
			t.Fatalf("expected nil ParentToolCallId on top-level tool events, got start=%v end=%v", st.ParentToolCallId, en.ParentToolCallId)
		}
		if st.ToolName != "ping" || en.ToolName != "ping" || en.IsError {
			t.Fatalf("unexpected tool event fields: start=%+v end=%+v", st, en)
		}
	})

	t.Run("nested executeTool forwarding via extension host", func(t *testing.T) {
		t.Skip("barebone excludes extension executeTool forwarding and codemode")
	})
}
