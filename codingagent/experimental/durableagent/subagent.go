// Ported from packages/coding-agent/src/experimental/durable/subagent.ts (pi v1.0.0).

package durableagent

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type subaArgs struct {
	Task string `json:"task"`
}

func subaAnswerText(api durable.ToolExecutionApi, answer durable.EntryId, ctx chord.Context) (string, error) {
	raw, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.TypedEntry(durable.AssistantEntry, answer)
	})
	if err != nil {
		return "", err
	}
	entry, _ := raw.(*durable.EntryRecord)
	if entry == nil || len(entry.Model) == 0 {
		return "", nil
	}
	msg, _ := entry.Model[0].(*ai.AssistantMessage)
	if msg == nil {
		return "", nil
	}
	var sb strings.Builder
	for _, block := range msg.Content {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), nil
}

// Subagent is a foreground subagent extension: each call creates a child conversation
// owned by the call's task, runs the task there, and returns the child's answer.
// Aborting the call aborts the child. The child outlives the call, so the user can
// switch to it and keep talking.
var Subagent *durable.Extension

func init() {
	Subagent = subaCreateExtension()
}

func subaCreateExtension() *durable.Extension {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("task", typebox.String(jsonx.ObjectOf(
		"description", "What the subagent should do",
	)))
	schema := typebox.Object(props, jsonx.ObjectOf("required", []any{"task"}))

	return durable.DefineExtension(&durable.Extension{
		Name: "subagent",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "subagent",
				Description: "Delegate a self-contained task to a subagent with the same tools and get its answer back. Give it everything it needs to know; it does not see this conversation.",
				Parameters:  schema,
				// A rerun after a crash finds the child it created and the submission it made.
				Replay: durable.ToolReplaySafe,
				Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
					var input subaArgs
					if err := jsonx.Decode(args, &input); err != nil {
						return nil, err
					}
					rawChild, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
						tid := api.TaskId()
						existing, scanErr := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &tid}, 1, nil)
						if scanErr != nil {
							return durable.ConversationId(0), scanErr
						}
						if len(existing.Items) > 0 {
							return existing.Items[0].Id, nil
						}
						// Starts as a copy of this conversation's agent; without this extension it cannot delegate further.
						created, createErr := tx.CreateConversation(durable.ConversationCreateTxOptions{
							Ownership: durable.ConversationOwnership{
								Kind:   durable.ConversationOwnershipTask,
								TaskId: tid,
							},
						})
						if createErr != nil {
							return durable.ConversationId(0), createErr
						}
						if cfgErr := durable.Configure(tx, created.Id, durable.AgentChange{
							Extensions: jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{Subagent})),
						}); cfgErr != nil {
							return durable.ConversationId(0), cfgErr
						}
						return created.Id, nil
					})
					if err != nil {
						return nil, err
					}
					child, _ := rawChild.(durable.ConversationId)
					if err := api.Details(jsonx.ObjectOf("conversationId", float64(child)), ctx); err != nil {
						return nil, err
					}
					handle, err := api.Conversation(ctx, child)
					if err != nil {
						return nil, err
					}
					sub, err := handle.Submit(durable.InputSubmissionDraft{
						RequestId: fmt.Sprintf("subagent:%d", api.TaskId()),
						Content:   durable.UserInput{Text: &input.Task},
					}, ctx)
					if err != nil {
						return nil, err
					}
					settled, err := sub.Wait(ctx)
					if err != nil {
						return nil, err
					}
					if settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
						return nil, fmt.Errorf("Subagent %d failed: %s", child, settled.Status)
					}
					text, err := subaAnswerText(api, *settled.Answer, ctx)
					if err != nil {
						return nil, err
					}
					return &durable.ToolExecutionResult{
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
						HasContent: true,
						Details:    jsonx.ObjectOf("conversationId", float64(child)),
						HasDetails: true,
					}, nil
				},
			}),
		},
	})
}
