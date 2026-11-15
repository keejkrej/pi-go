// Ported from packages/coding-agent/src/experimental/durable/subagent.ts (pi v1.0.0).

package durableagent

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// SubagentExtensionName identifies the built-in durable subagent extension.
const SubagentExtensionName = "pi.durable-subagent"

// SubagentToolInput represents arguments accepted by the durable subagent tool.
type SubagentToolInput struct {
	Prompt       string   `json:"prompt"`
	Instructions *string  `json:"instructions,omitzero"`
	Cwd          *string  `json:"cwd,omitzero"`
	Provider     *string  `json:"provider,omitzero"`
	Model        *string  `json:"model,omitzero"`
	Tools        []string `json:"tools,omitzero"`
}

// SubagentToolDetails describes execution metadata returned by the subagent tool.
type SubagentToolDetails struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	SubmissionId   durable.SubmissionId   `json:"submissionId"`
	Status         string                 `json:"status"`
}

type subaMemoRecord struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	RequestId      string                 `json:"requestId"`
}

func subaTextResult(text string, details durable.JsonValue, isError bool) *durable.ToolExecutionResult {
	return &durable.ToolExecutionResult{
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		HasContent: true,
		Details:    details,
		HasDetails: details != nil,
		IsError:    isError,
	}
}

// CreateSubagentExtension creates a durable extension exposing the `subagent` tool.
func CreateSubagentExtension() *durable.Extension {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("prompt", typebox.String(jsonx.ObjectOf(
		"description", "Task prompt to delegate to the isolated child conversation.",
	)))
	props.Set("instructions", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Optional system instructions appended for the subagent.",
	))))
	props.Set("cwd", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Optional working directory override for the subagent.",
	))))
	props.Set("provider", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Optional model provider override.",
	))))
	props.Set("model", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Optional model id override.",
	))))
	props.Set("tools", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Optional explicit tool allowlist for the subagent.",
	))))
	schema := typebox.Object(props, jsonx.ObjectOf("required", []any{"prompt"}))

	subagentTool := durable.DefineTool(&durable.ToolRegistration{
		Name:          "subagent",
		Description:   "Run an isolated child durable agent conversation to complete a focused subtask and return its final response.",
		Parameters:    schema,
		Replay:        durable.ToolReplaySafe,
		ExecutionMode: durable.ToolExecutionModeParallel,
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			var input SubagentToolInput
			if err := jsonx.Decode(args, &input); err != nil {
				return subaTextResult("Invalid subagent arguments: "+err.Error(), nil, true), nil
			}
			promptText := strings.TrimSpace(input.Prompt)
			if promptText == "" {
				return subaTextResult("subagent prompt must not be empty", nil, true), nil
			}

			parentAgent, err := api.Agent(ctx)
			if err != nil {
				return nil, err
			}

			// Reuse any child conversation memoized before crash/replay.
			var memo subaMemoRecord
			if existing, ok, memoErr := api.Memo(ctx, "subagent"); memoErr == nil && ok {
				_ = jsonx.Decode(existing, &memo)
			}

			if memo.ConversationId == 0 {
				taskId := api.TaskId()
				reqId := fmt.Sprintf("subagent:%d:%s", taskId, uuid.NewString()[:8])

				var childChange durable.AgentChange
				if input.Provider != nil && input.Model != nil && *input.Provider != "" && *input.Model != "" {
					childChange.Model = jsonx.Some(durable.ModelRef{
						Provider: *input.Provider,
						ModelId:  *input.Model,
					})
				} else if parentAgent != nil && parentAgent.Model != nil {
					childChange.Model = jsonx.Some(*parentAgent.Model)
				}
				if parentAgent != nil && parentAgent.ThinkingLevel != "" {
					childChange.ThinkingLevel = jsonx.Some(parentAgent.ThinkingLevel)
				}
				if input.Cwd != nil && strings.TrimSpace(*input.Cwd) != "" {
					childChange.Cwd = jsonx.Some(strings.TrimSpace(*input.Cwd))
				} else if parentAgent != nil && parentAgent.Cwd != nil {
					childChange.Cwd = jsonx.Some(*parentAgent.Cwd)
				}
				if input.Instructions != nil && strings.TrimSpace(*input.Instructions) != "" {
					childChange.Instructions = jsonx.Some(strings.TrimSpace(*input.Instructions))
				}
				// Prevent unbounded recursive subagent nesting by default unless explicitly requested.
				if len(input.Tools) > 0 {
					var selected []*durable.ToolRegistration
					byName := make(map[string]*durable.ToolRegistration)
					for _, rt := range api.Registry().Tools() {
						if rt.Tool != nil {
							byName[rt.Tool.Name] = rt.Tool
						}
					}
					for _, name := range input.Tools {
						if t := byName[name]; t != nil {
							selected = append(selected, t)
						}
					}
					childChange.Tools = jsonx.Some(durable.AgentToolsSet(selected...))
				} else if parentAgent != nil {
					var filtered []*durable.ToolRegistration
					for _, t := range parentAgent.Tools {
						if t != nil && t.Name != "subagent" {
							filtered = append(filtered, t)
						}
					}
					childChange.Tools = jsonx.Some(durable.AgentToolsSet(filtered...))
				}

				createdVal, commitErr := api.Commit(ctx, func(tx durable.Tx) (any, error) {
					rec, createErr := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{
							Kind:   durable.ConversationOwnershipTask,
							TaskId: taskId,
						},
					})
					if createErr != nil {
						return nil, createErr
					}
					if cfgErr := durable.Configure(tx, rec.Id, childChange); cfgErr != nil {
						return nil, cfgErr
					}
					return rec.Id, nil
				})
				if commitErr != nil {
					return nil, commitErr
				}
				childId, _ := createdVal.(durable.ConversationId)
				candidate := subaMemoRecord{
					ConversationId: childId,
					RequestId:      reqId,
				}
				encodedCandidate, err := jsonx.Encode(candidate)
				if err != nil {
					return nil, err
				}
				putVal, putErr := api.MemoPut(ctx, "subagent", encodedCandidate)
				if putErr != nil {
					return nil, putErr
				}
				_ = jsonx.Decode(putVal, &memo)
				if memo.ConversationId == 0 {
					memo = candidate
				}
			}

			convHandle, err := api.Conversation(ctx, memo.ConversationId)
			if err != nil {
				return nil, err
			}
			submission, err := convHandle.Submit(durable.InputSubmissionDraft{
				RequestId: memo.RequestId,
				Content:   durable.UserInput{Text: &promptText},
			}, ctx)
			if err != nil {
				return nil, err
			}

			settled, err := submission.Wait(ctx)
			if err != nil {
				return nil, err
			}

			details := SubagentToolDetails{
				ConversationId: memo.ConversationId,
				SubmissionId:   settled.Id,
				Status:         string(settled.Status),
			}
			encodedDetails, _ := jsonx.Encode(details)
			_ = api.Details(encodedDetails, ctx)

			var replyText string
			_, _ = api.Commit(ctx, func(tx durable.Tx) (any, error) {
				if settled.Answer != nil {
					if entry, entryErr := tx.Entry(*settled.Answer); entryErr == nil && entry != nil && len(entry.Model) > 0 {
						replyText = promptExtractMessageText(entry.Model[0])
						return nil, nil
					}
				}
				page, scanErr := tx.ScanEntries(durable.EntryQuery{
					ConversationId: memo.ConversationId,
				}, 100, nil)
				if scanErr == nil {
					for i := len(page.Items) - 1; i >= 0; i-- {
						for _, m := range page.Items[i].Model {
							if txt := promptExtractMessageText(m); txt != "" {
								replyText = txt
								return nil, nil
							}
						}
					}
				}
				return nil, nil
			})
			if settled.Status != durable.SubmissionStatusDone && replyText == "" {
				reason := string(settled.Status)
				if settled.Reason != nil && *settled.Reason != "" {
					reason = *settled.Reason
				}
				return subaTextResult(fmt.Sprintf("Subagent %s", reason), encodedDetails, true), nil
			}
			return subaTextResult(replyText, encodedDetails, false), nil
		},
	})

	return durable.DefineExtension(&durable.Extension{
		Name:  SubagentExtensionName,
		Tools: []*durable.ToolRegistration{subagentTool},
	})
}
