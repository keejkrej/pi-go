// Ported from packages/coding-agent/test/suite/agent-session-tool-orchestration.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	core "github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
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
		var mu sync.Mutex
		var toolCallEvents []string
		var seenAvailableInLoadout []string

		extractResultText := func(res agent.AgentToolResult) string {
			var parts []string
			for _, blk := range res.Content {
				if tc, ok := blk.(*ai.TextContent); ok && tc != nil {
					parts = append(parts, tc.Text)
				}
			}
			return strings.Join(parts, "")
		}

		orchestratorExtension := core.InlineExtension{
			Name: "orchestrator-ext",
			Factory: func(pi core.ExtensionAPI) error {
				echoParams := omap.NewMap[string, *typebox.Schema]()
				echoParams.Set("text", typebox.String())
				pi.RegisterTool(core.ToolDefinition{
					Name:        "echo",
					Label:       "Echo",
					Description: "Echo input text",
					Exposure:    core.ToolExposureDirect,
					Parameters:  typebox.Object(echoParams),
					Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						var text string
						if params != nil {
							if v, ok := params.Get("text"); ok {
								if s, ok := v.(string); ok {
									text = s
								}
							}
						}
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "echo: " + text}},
						}, nil
					},
				})

				pi.RegisterTool(core.ToolDefinition{
					Name:        "helper",
					Label:       "Helper",
					Description: "Codemode-only helper tool",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  typebox.Object(nil),
					Execute: func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "helped"}},
						}, nil
					},
				})

				pi.RegisterTool(core.ToolDefinition{
					Name:        "run_tools",
					Label:       "Run Tools",
					Description: "Orchestrates nested tools",
					Exposure:    core.ToolExposureModelOnly,
					Parameters:  typebox.Object(nil),
					PrepareLoadout: func(loadout core.ToolLoadout) *core.ToolLoadoutChanges {
						mu.Lock()
						seenAvailableInLoadout = nil
						for _, avail := range loadout.Available {
							if avail != nil {
								seenAvailableInLoadout = append(seenAvailableInLoadout, avail.Name)
							}
						}
						mu.Unlock()
						return &core.ToolLoadoutChanges{
							Descriptions: map[string]string{
								"run_tools": "Orchestrates nested tools (loadout customized)",
							},
							HideDeclarations: []string{"echo"},
						}
					},
					Execute: func(toolCtx context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, ectx core.ExtensionToolContext) (agent.AgentToolResult, error) {
						outHelper, err := ectx.ExecuteTool(toolCtx, "helper", jsonx.NewObject())
						if err != nil {
							return agent.AgentToolResult{}, err
						}
						outEcho, err := ectx.ExecuteTool(toolCtx, "echo", jsonx.ObjectOf("text", "hi"))
						if err != nil {
							return agent.AgentToolResult{}, err
						}
						outSelf, err := ectx.ExecuteTool(toolCtx, "run_tools", jsonx.NewObject())
						if err != nil {
							return agent.AgentToolResult{}, err
						}
						combined := strings.Join([]string{
							extractResultText(outHelper.Result),
							extractResultText(outEcho.Result),
							extractResultText(outSelf.Result),
						}, " | ")
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: combined}},
						}, nil
					},
				})

				pi.OnToolCall(func(_ context.Context, ev *core.ToolCallEvent, _ core.ExtensionContext) (*core.ToolCallEventResult, error) {
					if ev != nil {
						parent := "none"
						if ev.ParentToolCallId != nil {
							parent = *ev.ParentToolCallId
						}
						mu.Lock()
						toolCallEvents = append(toolCallEvents, ev.ToolName+":"+parent)
						mu.Unlock()
					}
					return nil, nil
				})
				return nil
			},
		}

		plainTool := &agent.AgentTool{
			Name:        "plain",
			Label:       "Plain",
			Description: "Direct tool without nested calls",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "plain ok"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools:              []*agent.AgentTool{plainTool},
			ExtensionFactories: []core.InlineExtension{orchestratorExtension},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		activeTools := h.Session.GetActiveToolNames()
		if !slices.Equal(activeTools, []string{"plain", "echo", "run_tools"}) {
			t.Fatalf("GetActiveToolNames = %v, want [plain echo run_tools]", activeTools)
		}
		callableTools := h.Session.GetCallableToolNames()
		slices.Sort(callableTools)
		if !slices.Equal(callableTools, []string{"echo", "helper", "plain"}) {
			t.Fatalf("GetCallableToolNames = %v, want [echo helper plain]", callableTools)
		}

		stopTool := ai.StopReasonToolUse
		runCallID := "run_1"
		plainCallID := "plain_1"
		var sentToolNames []string
		var customizedRunToolsDesc string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				sentToolNames = nil
				for _, tool := range ai.GetCurrentTools(tc.Messages) {
					sentToolNames = append(sentToolNames, tool.Name)
					if tool.Name == "run_tools" {
						customizedRunToolsDesc = tool.Description
					}
				}
				return providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("run_tools", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &runCallID})},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("plain", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &plainCallID})},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "orchestrate"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		slices.Sort(sentToolNames)
		if !slices.Equal(sentToolNames, []string{"plain", "run_tools"}) {
			t.Fatalf("LLM request tool names = %v, want [plain run_tools]", sentToolNames)
		}
		if customizedRunToolsDesc != "Orchestrates nested tools (loadout customized)" {
			t.Fatalf("expected customized run_tools description from PrepareLoadout, got %q", customizedRunToolsDesc)
		}

		mu.Lock()
		gotAvailable := slices.Clone(seenAvailableInLoadout)
		gotToolCallEvents := slices.Clone(toolCallEvents)
		mu.Unlock()
		slices.Sort(gotAvailable)
		if !slices.Equal(gotAvailable, []string{"echo", "helper", "plain"}) {
			t.Fatalf("PrepareLoadout loadout.Available = %v, want [echo helper plain]", gotAvailable)
		}
		wantToolCallEvents := []string{
			"run_tools:none",
			"helper:run_1",
			"echo:run_1",
			"plain:none",
		}
		if !slices.Equal(gotToolCallEvents, wantToolCallEvents) {
			t.Fatalf("tool_call events = %v, want %v", gotToolCallEvents, wantToolCallEvents)
		}

		var runToolsResult *ai.ToolResultMessage
		var plainResult *ai.ToolResultMessage
		for _, m := range h.Session.Messages() {
			if tr, ok := m.(*ai.ToolResultMessage); ok && tr != nil {
				switch tr.ToolCallId {
				case "run_1":
					runToolsResult = tr
				case "plain_1":
					plainResult = tr
				}
			}
		}
		if runToolsResult == nil {
			t.Fatal("expected run_1 ToolResultMessage in session messages")
		}
		if got := testkit.GetMessageText(runToolsResult); got != "helped | echo: hi | Tool run_tools not found" {
			t.Fatalf("run_tools result text = %q, want %q", got, "helped | echo: hi | Tool run_tools not found")
		}
		if runToolsResult.NestedCalls == nil || len(runToolsResult.NestedCalls.Calls) != 3 {
			t.Fatalf("expected 3 NestedCalls on run_tools session message, got %+v", runToolsResult.NestedCalls)
		}
		if runToolsResult.NestedCalls.Calls[0].Name != "helper" || runToolsResult.NestedCalls.Calls[0].Status != ai.NestedToolCallStatusOk ||
			runToolsResult.NestedCalls.Calls[1].Name != "echo" || runToolsResult.NestedCalls.Calls[1].Status != ai.NestedToolCallStatusOk ||
			runToolsResult.NestedCalls.Calls[2].Name != "run_tools" || runToolsResult.NestedCalls.Calls[2].Status != ai.NestedToolCallStatusError {
			t.Fatalf("unexpected NestedCalls entries: %+v", runToolsResult.NestedCalls.Calls)
		}
		if plainResult == nil || plainResult.NestedCalls != nil {
			t.Fatalf("expected plain tool result to have nil NestedCalls, got %+v", plainResult)
		}

		// Verify NestedCalls is persisted on the SessionManager branch entry too.
		var persistedRunResult *ai.ToolResultMessage
		for _, entry := range h.Session.SessionManager.GetBranch() {
			if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
				if tr, ok := me.Message.(*ai.ToolResultMessage); ok && tr != nil && tr.ToolCallId == "run_1" {
					persistedRunResult = tr
				}
			}
		}
		if persistedRunResult == nil || persistedRunResult.NestedCalls == nil || len(persistedRunResult.NestedCalls.Calls) != 3 {
			t.Fatalf("expected 3 NestedCalls on persisted branch entry, got %+v", persistedRunResult)
		}

		// Verify DefaultActive: false excludes an extension tool from initial active tools while keeping it callable/registered.
		falseVal := false
		hDefault, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{},
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "default-active-ext",
					Factory: func(pi core.ExtensionAPI) error {
						pi.RegisterTool(core.ToolDefinition{
							Name:        "always_on",
							Label:       "Always On",
							Description: "Default active tool",
							Parameters:  typebox.Object(nil),
							Execute: func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
								return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "on"}}}, nil
							},
						})
						pi.RegisterTool(core.ToolDefinition{
							Name:          "opt_in",
							Label:         "Opt In",
							Description:   "Inactive by default",
							DefaultActive: &falseVal,
							Parameters:    typebox.Object(nil),
							Execute: func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
								return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "opt"}}}, nil
							},
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness (DefaultActive): %v", err)
		}
		defer hDefault.Cleanup()

		if got := hDefault.Session.GetActiveToolNames(); !slices.Equal(got, []string{"always_on"}) {
			t.Fatalf("DefaultActive=false active tools = %v, want [always_on]", got)
		}
		if got := hDefault.Session.GetCallableToolNames(); !slices.Equal(got, []string{"always_on"}) {
			t.Fatalf("DefaultActive=false callable tools = %v, want [always_on]", got)
		}
		var allNames []string
		for _, info := range hDefault.Session.GetAllTools() {
			allNames = append(allNames, info.Name)
		}
		slices.Sort(allNames)
		if !slices.Equal(allNames, []string{"always_on", "opt_in"}) {
			t.Fatalf("DefaultActive=false GetAllTools = %v, want [always_on opt_in]", allNames)
		}
		hDefault.Session.SetActiveToolsByName([]string{"always_on", "opt_in"})
		if got := hDefault.Session.GetActiveToolNames(); !slices.Equal(got, []string{"always_on", "opt_in"}) {
			t.Fatalf("after SetActiveToolsByName active tools = %v, want [always_on opt_in]", got)
		}
		if got := hDefault.Session.GetCallableToolNames(); !slices.Equal(got, []string{"always_on", "opt_in"}) {
			t.Fatalf("after SetActiveToolsByName callable tools = %v, want [always_on opt_in]", got)
		}
		// Also verify AllowedToolNames and InitialActiveToolNames can explicitly opt in a DefaultActive=false tool.
		defaultExt := core.InlineExtension{
			Name: "default-active-explicit-ext",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:          "opt_in",
					Label:         "Opt In",
					Description:   "Inactive by default",
					DefaultActive: &falseVal,
					Parameters:    typebox.Object(nil),
					Execute: func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "opt"}}}, nil
					},
				})
				return nil
			},
		}
		hAllowed, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			AllowedToolNames:   []string{"read", "opt_in"},
			ExtensionFactories: []core.InlineExtension{defaultExt},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness (AllowedToolNames): %v", err)
		}
		defer hAllowed.Cleanup()
		if got := hAllowed.Session.GetActiveToolNames(); !slices.Equal(got, []string{"read", "opt_in"}) {
			t.Fatalf("AllowedToolNames active tools = %v, want [read opt_in]", got)
		}

		hInitial, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			InitialActiveToolNames: []string{"opt_in"},
			ExtensionFactories:     []core.InlineExtension{defaultExt},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness (InitialActiveToolNames): %v", err)
		}
		defer hInitial.Cleanup()
		if got := hInitial.Session.GetActiveToolNames(); !slices.Equal(got, []string{"opt_in"}) {
			t.Fatalf("InitialActiveToolNames active tools = %v, want [opt_in]", got)
		}
	})
}
