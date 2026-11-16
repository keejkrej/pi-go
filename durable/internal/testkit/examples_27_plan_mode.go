// Ported from packages/durable/test/examples/27-plan-mode.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e2pmPlanArgs struct {
	Steps []string `json:"steps"`
}

// Example27PlanMode runs the 27-plan-mode example.
func Example27PlanMode(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext

	planDoc, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.plan",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("steps", []any{}) },
	})
	if err != nil {
		return err
	}

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("steps", typebox.Array(typebox.String()))
	submitPlan := durable.DefineTool(&durable.ToolRegistration{
		Name:        "submit_plan",
		Description: "Submit the plan as a list of steps.",
		Parameters:  typebox.Object(props),
		Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			var in e2pmPlanArgs
			if err := jsonx.Decode(args, &in); err != nil {
				return nil, err
			}
			if _, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
				doc, err := tx.Doc(planDoc, api.ConversationId())
				if err != nil {
					return nil, err
				}
				rawSteps := make([]any, len(in.Steps))
				for i, s := range in.Steps {
					rawSteps[i] = s
				}
				doc.Value.Set("steps", rawSteps)
				return nil, nil
			}); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "Plan submitted."}},
				HasContent: true,
				Control:    &durable.ToolControl{Terminate: true},
			}, nil
		},
	})
	plan := durable.DefineExtension(&durable.Extension{
		Name:  "plan",
		Tools: []*durable.ToolRegistration{submitPlan},
		Sections: []*durable.PromptSection{
			durable.Section("plan_mode", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You are in plan mode. Read the code, then call submit_plan. Change nothing.", true, nil
			}),
		},
	})

	read := tools.CreateReadTool()
	enterPlanMode := durable.AgentChange{
		Extensions: jsonx.Some(durable.AgentExtensionsEdit([]*durable.Extension{plan}, nil)),
		Tools:      jsonx.Some(durable.AgentToolsSet(read, submitPlan)),
	}
	leavePlanMode := durable.AgentChange{
		Extensions: jsonx.Null[durable.AgentExtensionsChange](),
		Tools:      jsonx.Null[durable.AgentToolsChange](),
	}

	directory, err := os.MkdirTemp("", "pi-durable-plan-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	if err := os.WriteFile(filepath.Join(directory, "server.ts"), []byte("app.listen(3000);\n"), 0o600); err != nil {
		return err
	}

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("read", jsonx.ObjectOf("path", "server.ts"), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("submit_plan", jsonx.ObjectOf(
				"steps", []any{"Read PORT from the environment", "Default to 3000"},
			), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Implementing step 1."), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	if err := registry.Install(plan); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		StaticSettings: &durable.HarnessSettings{
			Extensions:    []*durable.Extension{tools.CodingTools},
			HasExtensions: true,
		},
		Env: func(_ durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: directory}), nil
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
	})
	if err != nil {
		return err
	}
	toolNames := func() ([]string, error) {
		ag, err := root.Agent(ctx)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(ag.Tools))
		for i, tool := range ag.Tools {
			names[i] = tool.Name
		}
		return names, nil
	}

	t0, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "tools: %v\n", t0)

	if err := root.Configure(enterPlanMode, ctx); err != nil {
		return err
	}
	t1, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "plan mode tools: %v\n", t1)
	q1 := "Make the port configurable."
	s1, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q1},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := s1.Wait(ctx); err != nil {
		return err
	}
	planSnap, err := harness.Snapshot(ctx, planDoc, root.Id())
	if err != nil {
		return err
	}
	if planSnap != nil {
		steps, _ := planSnap.Get("steps")
		_, _ = fmt.Fprintf(out, "plan: %v\n", steps)
	}

	if err := root.Configure(leavePlanMode, ctx); err != nil {
		return err
	}
	t2, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "tools again: %v\n", t2)
	q2 := "Go ahead."
	s2, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q2},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := s2.Wait(ctx); err != nil {
		return err
	}

	view, err := root.Context(ctx)
	if err != nil {
		return err
	}
	for _, message := range view.Messages {
		sys, ok := message.(*ai.SystemMessage)
		if !ok {
			continue
		}
		secStr, _ := jsonx.Stringify(sys.Sections)
		var added, removed []string
		for _, tool := range sys.ToolsAdded {
			added = append(added, tool.Name)
		}
		for _, tool := range sys.ToolsRemoved {
			removed = append(removed, tool.Name)
		}
		_, _ = fmt.Fprintf(out, "system: sections=%s added=%v removed=%v\n", secStr, added, removed)
	}
	return harness.Close(ctx)
}
