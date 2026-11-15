// Ported from packages/durable/test/examples/11-extension-state.ts (pi v1.0.0).

package testkit

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e1esTodoArgs struct {
	Item string `json:"item"`
}

type e1esTodosState struct {
	Items []string `json:"items"`
}

// Example11ExtensionState runs the 11-extension-state example.
func Example11ExtensionState(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	todosDoc, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "example.todos",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("items", []any{}) },
	})
	if err != nil {
		return err
	}

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("item", typebox.String())
	todoExt := durable.DefineExtension(&durable.Extension{
		Name: "todo",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "todo",
				Description: "Add an item to your todo list",
				Parameters:  typebox.Object(props),
				Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					var in e1esTodoArgs
					if err := jsonx.Decode(args, &in); err != nil {
						return nil, err
					}
					if _, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
						d, err := tx.Doc(todosDoc, api.ConversationId())
						if err != nil {
							return nil, err
						}
						var st e1esTodosState
						if err := jsonx.Decode(d.Value, &st); err != nil {
							return nil, err
						}
						st.Items = append(st.Items, in.Item)
						rawItems := make([]any, len(st.Items))
						for i, s := range st.Items {
							rawItems[i] = s
						}
						d.Value.Set("items", rawItems)
						return nil, nil
					}); err != nil {
						return nil, err
					}
					return &durable.ToolExecutionResult{
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "added " + in.Item}},
						HasContent: true,
					}, nil
				},
			}),
		},
		Sections: []*durable.PromptSection{
			durable.Section("todos", func(input durable.PromptInput, renderCtx chord.Context) (string, bool, error) {
				snap, err := input.Read.Snapshot(renderCtx, todosDoc, input.ConversationId)
				if err != nil || snap == nil {
					return "", false, err
				}
				var st e1esTodosState
				if err := jsonx.Decode(snap, &st); err != nil {
					return "", false, err
				}
				if len(st.Items) == 0 {
					return "", false, nil
				}
				return strings.Join(st.Items, "\n"), true, nil
			}),
		},
	})

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	registry := durable.CreateRegistry()
	if err := registry.Install(todoExt); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
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

	toolUse := ai.StopReasonToolUse
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("todo", jsonx.ObjectOf("item", "fix the build"), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Noted."), nil)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Working on it."), nil)},
	})

	q1 := "Remember to fix the build."
	s1, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserMessageContent{Text: &q1}}, ctx)
	if err != nil {
		return err
	}
	r1, err := s1.Wait(ctx)
	if err != nil {
		return err
	}
	if r1.Status != durable.SubmissionStatusDone {
		return errors.New("expected first turn done")
	}
	todosSnap, err := harness.Snapshot(ctx, todosDoc, root.Id())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "todos: %v\n", todosSnap)

	q2 := "What is next?"
	s2, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserMessageContent{Text: &q2}}, ctx)
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
	var sysSummary []string
	for _, msg := range view.Messages {
		if sys, ok := msg.(*ai.SystemMessage); ok {
			rawSec, _ := jsonx.Stringify(sys.Sections)
			var added []string
			for _, t := range sys.ToolsAdded {
				added = append(added, t.Name)
			}
			sysSummary = append(sysSummary, fmt.Sprintf("{sections:%s toolsAdded:%v}", rawSec, added))
		}
	}
	_, _ = fmt.Fprintf(out, "system messages: %v\n", sysSummary)
	return harness.Close(ctx)
}
