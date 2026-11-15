// Ported from packages/coding-agent/src/experimental/vacation/vacation.ts (pi v1.0.0).

package vacation

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// VacationExtensionName identifies the built-in durable vacation extension.
const VacationExtensionName = "pi.vacation"

// VacationDoc tracks active/latest durable sleep/vacation window per conversation.
var VacationDoc = vacaMustDefineDoc()

func vacaMustDefineDoc() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "pi.vacation",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject {
			return jsonx.NewObject()
		},
	})
	if err != nil {
		panic(err)
	}
	return tok
}

// VacationTaskInput configures one durable sleep/vacation task.
type VacationTaskInput struct {
	UntilMs int64  `json:"untilMs"`
	Reason  string `json:"reason,omitzero"`
}

// VacationTaskState holds the durable phase checkpoint for VacationTask.
type VacationTaskState struct {
	Phase   string `json:"phase"`
	UntilMs int64  `json:"untilMs"`
	Reason  string `json:"reason,omitzero"`
}

// VacationTaskResult reports completion metadata of a finished VacationTask.
type VacationTaskResult struct {
	WokeAtMs int64  `json:"wokeAtMs"`
	UntilMs  int64  `json:"untilMs"`
	Reason   string `json:"reason,omitzero"`
}

// VacationState is persisted in VacationDoc for UI/inspection.
type VacationState struct {
	Active  bool    `json:"active"`
	UntilMs int64   `json:"untilMs,omitzero"`
	Reason  *string `json:"reason,omitzero"`
	TaskId  int64   `json:"taskId,omitzero"`
}

// VacationToolInput specifies the arguments accepted by the `vacation` / `sleep` tool.
type VacationToolInput struct {
	DurationSeconds *float64 `json:"durationSeconds,omitzero"`
	DurationMs      *int64   `json:"durationMs,omitzero"`
	Until           *string  `json:"until,omitzero"`
	Reason          *string  `json:"reason,omitzero"`
}

// VacationTask is the durable sleep task resumed cleanly across process restarts.
var VacationTask = durable.DefineTask[VacationTaskInput, VacationTaskState, VacationTaskResult, struct{}](
	durable.TaskDefinition[VacationTaskInput, VacationTaskState, VacationTaskResult, struct{}]{
		Name:    "vacation.sleep",
		Version: 1,
		Initial: func(input VacationTaskInput) VacationTaskState {
			return VacationTaskState{
				Phase:   "sleep",
				UntilMs: input.UntilMs,
				Reason:  input.Reason,
			}
		},
		Phases: map[string]durable.PhaseHandler[VacationTaskInput, VacationTaskState, VacationTaskResult, struct{}]{
			"sleep": func(ctx chord.Context, task durable.RunningTask[VacationTaskInput, VacationTaskState], runtime *durable.TaskRuntime[VacationTaskInput, VacationTaskState, VacationTaskResult, struct{}]) error {
				if task.AbortRequested {
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[VacationTaskInput, VacationTaskState]) (*durable.NextTaskState[VacationTaskState, VacationTaskResult], error) {
						return &durable.NextTaskState[VacationTaskState, VacationTaskResult]{
							Status:  durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[VacationTaskResult]{Status: durable.TaskOutcomeAborted},
						}, nil
					})
				}

				if runtime.Now() < task.Input.UntilMs {
					if err := runtime.Sleep(ctx, task.Input.UntilMs); err != nil {
						return err
					}
				}

				wokeAt := runtime.Now()
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[VacationTaskInput, VacationTaskState]) (*durable.NextTaskState[VacationTaskState, VacationTaskResult], error) {
					if doc, err := tx.Doc(VacationDoc, task.ConversationId); err == nil && doc.Value != nil {
						doc.Value.Set("active", false)
					}
					return &durable.NextTaskState[VacationTaskState, VacationTaskResult]{
						Status:     durable.TaskStateTerminal,
						Checkpoint: VacationTaskState{Phase: "done", UntilMs: task.Input.UntilMs, Reason: task.Input.Reason},
						Outcome: &durable.TypedTaskOutcome[VacationTaskResult]{
							Status: durable.TaskOutcomeCompleted,
							HasRes: true,
							Result: VacationTaskResult{
								WokeAtMs: wokeAt,
								UntilMs:  task.Input.UntilMs,
								Reason:   task.Input.Reason,
							},
						},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, task durable.RunningTask[VacationTaskInput, VacationTaskState], runtime *durable.TaskRuntime[VacationTaskInput, VacationTaskState, VacationTaskResult, struct{}]) error {
			return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[VacationTaskInput, VacationTaskState]) (*durable.NextTaskState[VacationTaskState, VacationTaskResult], error) {
				if doc, err := tx.Doc(VacationDoc, task.ConversationId); err == nil && doc.Value != nil {
					doc.Value.Set("active", false)
				}
				return &durable.NextTaskState[VacationTaskState, VacationTaskResult]{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[VacationTaskResult]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	},
)

type vacaMemoRecord struct {
	TaskId durable.TaskId `json:"taskId"`
}

func vacaTextResult(text string, details durable.JsonValue, isError bool) *durable.ToolExecutionResult {
	return &durable.ToolExecutionResult{
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		HasContent: true,
		Details:    details,
		HasDetails: details != nil,
		IsError:    isError,
	}
}

func vacaResolveUntilMs(input VacationToolInput, nowMs int64) (int64, error) {
	if input.Until != nil && strings.TrimSpace(*input.Until) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*input.Until))
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, strings.TrimSpace(*input.Until))
			if err != nil {
				return 0, fmt.Errorf("invalid ISO timestamp %q: %w", *input.Until, err)
			}
		}
		return parsed.UnixMilli(), nil
	}
	if input.DurationMs != nil {
		if *input.DurationMs < 0 {
			return 0, fmt.Errorf("durationMs must be non-negative")
		}
		return nowMs + *input.DurationMs, nil
	}
	if input.DurationSeconds != nil {
		if *input.DurationSeconds < 0 {
			return 0, fmt.Errorf("durationSeconds must be non-negative")
		}
		return nowMs + int64(math.Round(*input.DurationSeconds*1000)), nil
	}
	return 0, fmt.Errorf("one of durationSeconds, durationMs, or until is required")
}

// CreateVacationExtension defines the durable vacation sleep tool, prompt section, and task.
func CreateVacationExtension() *durable.Extension {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("durationSeconds", typebox.Optional(typebox.Number(jsonx.ObjectOf(
		"description", "Number of seconds to sleep.",
	))))
	props.Set("durationMs", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
		"description", "Number of milliseconds to sleep.",
	))))
	props.Set("until", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "ISO-8601/RFC3339 timestamp to sleep until.",
	))))
	props.Set("reason", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Optional reason for pausing execution.",
	))))
	schema := typebox.Object(props)

	sleepTool := durable.DefineTool(&durable.ToolRegistration{
		Name:          "sleep",
		Description:   "Durably suspend until a delay elapses or target time is reached, surviving process restarts.",
		Parameters:    schema,
		Replay:        durable.ToolReplaySafe,
		ExecutionMode: durable.ToolExecutionModeParallel,
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			var input VacationToolInput
			if err := jsonx.Decode(args, &input); err != nil {
				return vacaTextResult("Invalid sleep arguments: "+err.Error(), nil, true), nil
			}
			nowMs := time.Now().UnixMilli()
			untilMs, err := vacaResolveUntilMs(input, nowMs)
			if err != nil {
				return vacaTextResult(err.Error(), nil, true), nil
			}
			reason := ""
			if input.Reason != nil {
				reason = strings.TrimSpace(*input.Reason)
			}

			var memo vacaMemoRecord
			if existing, ok, memoErr := api.Memo(ctx, "sleep"); memoErr == nil && ok {
				_ = jsonx.Decode(existing, &memo)
			}
			if memo.TaskId == 0 {
				convId := api.ConversationId()
				taskId, createErr := api.CreateTask(ctx, VacationTask, VacationTaskInput{
					UntilMs: untilMs,
					Reason:  reason,
				}, durable.TaskOptions{
					Ownership: durable.TaskOwnership{
						Kind: durable.TaskOwnershipConversation,
					},
					ConversationId: &convId,
				})
				if createErr != nil {
					return nil, createErr
				}
				_, _ = api.Commit(ctx, func(tx durable.Tx) (any, error) {
					if doc, docErr := tx.Doc(VacationDoc, convId); docErr == nil && doc.Value != nil {
						doc.Value.Set("active", true)
						doc.Value.Set("untilMs", untilMs)
						doc.Value.Set("taskId", int64(taskId))
						if reason != "" {
							doc.Value.Set("reason", reason)
						}
					}
					return nil, nil
				})
				encodedMemo, _ := jsonx.Encode(vacaMemoRecord{TaskId: taskId})
				putVal, putErr := api.MemoPut(ctx, "sleep", encodedMemo)
				if putErr != nil {
					return nil, putErr
				}
				_ = jsonx.Decode(putVal, &memo)
				if memo.TaskId == 0 {
					memo.TaskId = taskId
				}
			}

			settled, err := api.WaitForTask(ctx, memo.TaskId)
			if err != nil {
				return nil, err
			}
			if settled != nil && settled.State.Outcome != nil && settled.State.Outcome.Status != durable.TaskOutcomeCompleted {
				encodedOutcome, _ := jsonx.Encode(settled.State.Outcome)
				return vacaTextResult(fmt.Sprintf("Sleep ended early (%s)", settled.State.Outcome.Status), encodedOutcome, true), nil
			}

			var res VacationTaskResult
			if settled != nil && settled.State.Outcome != nil {
				if rawRes, ok := settled.State.Outcome.Result.Get(); ok {
					_ = jsonx.Decode(rawRes, &res)
				}
			}
			if res.WokeAtMs == 0 {
				res.WokeAtMs = time.Now().UnixMilli()
				res.UntilMs = untilMs
				res.Reason = reason
			}
			summary := fmt.Sprintf("Woke up at %s", time.UnixMilli(res.WokeAtMs).UTC().Format(time.RFC3339))
			if reason != "" {
				summary += " (reason: " + reason + ")"
			}
			encodedRes, _ := jsonx.Encode(res)
			return vacaTextResult(summary, encodedRes, false), nil
		},
	})

	section := durable.SimplePromptSection(
		"vacation",
		"You have a durable `sleep` tool. Use `sleep` whenever asked to wait, pause, poll on a schedule, or resume work at a later time.",
		true,
	)

	return durable.DefineExtension(&durable.Extension{
		Name:     VacationExtensionName,
		Tools:    []*durable.ToolRegistration{sleepTool},
		Sections: []*durable.PromptSection{section},
		Tasks:    []durable.AnyTask{VacationTask},
	})
}
