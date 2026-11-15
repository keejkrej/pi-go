// Ported from packages/durable/src/harness/tool.ts (pi v1.0.0).

package durable

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ToolTaskInput identifies the assistant entry and tool call ID for a built-in tool task.
type ToolTaskInput struct {
	Assistant EntryId `json:"assistant"`
	CallId    string  `json:"callId"`
}

// ToolTaskCheckpoint stores the durable phase state of a ToolTask.
type ToolTaskCheckpoint struct {
	Phase     string           `json:"phase"`
	Arguments JsonObject       `json:"arguments,omitzero"`
	Replay    ToolReplayPolicy `json:"replay,omitzero"`
}

// ToolTaskResult records the appended result entry ID and optional tool control request.
type ToolTaskResult struct {
	EntryId EntryId      `json:"entryId"`
	Control *ToolControl `json:"control,omitzero"`
}

type htlRuntime = *TaskRuntime[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, ToolHooks]
type htlNext = NextTaskState[ToolTaskCheckpoint, ToolTaskResult]

type htlEnding struct {
	status  TaskOutcomeStatus
	message string
}

var htlCompleted = htlEnding{status: TaskOutcomeCompleted}

// ToolTask is the built-in durable tool execution task ("pi.tool").
var ToolTask = DefineTask(TaskDefinition[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, ToolHooks]{
	Name:    "pi.tool",
	Version: 1,
	Initial: func(_ ToolTaskInput) ToolTaskCheckpoint {
		return ToolTaskCheckpoint{Phase: "call"}
	},
	Phases: map[string]PhaseHandler[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, ToolHooks]{
		"call": func(ctx chord.Context, task RunningTask[ToolTaskInput, ToolTaskCheckpoint], runtime htlRuntime) error {
			call, err := htlReadCall(ctx, runtime, task.Input)
			if err != nil {
				return err
			}
			agent, err := runtime.Agent(ctx)
			if err != nil {
				return err
			}
			tool := htlFindTool(agent.Tools, call.Name)
			if tool == nil {
				errRes := HarnessError("tool_unavailable", fmt.Sprintf("Tool %s is not available", call.Name))
				return htlSettle(ctx, runtime, call, htlCompleted, func(_ *ToolSlot) ToolExecutionResult {
					return errRes
				})
			}

			args, errMsg := htlPrepare(tool, call.Arguments)
			if errMsg == "" {
				args, errMsg = htlValidate(tool, call, args)
			}
			if errMsg != "" {
				return htlSettle(ctx, runtime, call, htlCompleted, func(_ *ToolSlot) ToolExecutionResult {
					return htlInvalid(errMsg)
				})
			}

			var block *string
			if err := runtime.Hooks().Each(func(h ToolHooks) error {
				if h.BeforeTool == nil || block != nil {
					return nil
				}
				callCopy := *call
				callCopy.Arguments = args
				decision, err := h.BeforeTool(&callCopy, runtime.Base(), ctx)
				if err != nil {
					if runtime.Signal().Err() != nil {
						return err
					}
					msg := err.Error()
					block = &msg
					return nil
				}
				if decision != nil {
					if decision.Block != nil {
						b := *decision.Block
						block = &b
					} else if decision.Arguments != nil {
						args = decision.Arguments
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if block != nil {
				blocked := HarnessError("blocked", fmt.Sprintf("Tool call blocked: %s", *block))
				return htlSettle(ctx, runtime, call, htlCompleted, func(_ *ToolSlot) ToolExecutionResult {
					return blocked
				})
			}

			finalArgs, valErr := htlValidate(tool, call, args)
			if valErr != "" {
				return htlSettle(ctx, runtime, call, htlCompleted, func(_ *ToolSlot) ToolExecutionResult {
					return htlInvalid(valErr)
				})
			}

			replay := tool.Replay
			if replay == "" {
				replay = ToolReplayUnsafe
			}
			if err := runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				if slot, ok := GetToolSlot(live.Value, runtime.TaskId()); ok {
					slot.Set("status", string(ToolSlotRunning))
				}
				return &htlNext{
					Status: TaskStateRunning,
					Checkpoint: ToolTaskCheckpoint{
						Phase:     "execute",
						Arguments: finalArgs,
						Replay:    replay,
					},
				}, nil
			}); err != nil {
				return err
			}
			return htlRun(ctx, runtime, call, tool, finalArgs)
		},
		"execute": func(ctx chord.Context, task RunningTask[ToolTaskInput, ToolTaskCheckpoint], runtime htlRuntime) error {
			call, err := htlReadCall(ctx, runtime, task.Input)
			if err != nil {
				return err
			}
			agent, err := runtime.Agent(ctx)
			if err != nil {
				return err
			}
			tool := htlFindTool(agent.Tools, call.Name)
			if task.Checkpoint.Replay == ToolReplaySafe && tool != nil && tool.Replay == ToolReplaySafe {
				if err := runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
					live, err := tx.Doc(LiveDoc, runtime.ConversationId())
					if err != nil {
						return nil, err
					}
					if slot, ok := GetToolSlot(live.Value, runtime.TaskId()); ok {
						ClearSlotProgress(slot)
					}
					return nil, nil
				}); err != nil {
					return err
				}
				return htlRun(ctx, runtime, call, tool, task.Checkpoint.Arguments)
			}
			msg := fmt.Sprintf("Tool %s was interrupted and may have partially run", call.Name)
			ending := htlEnding{status: TaskOutcomeFailed, message: msg}
			return htlSettle(ctx, runtime, call, ending, func(slot *ToolSlot) ToolExecutionResult {
				return htlFromSlot(slot, "interrupted", msg)
			})
		},
	},
	Abort: func(ctx chord.Context, task RunningTask[ToolTaskInput, ToolTaskCheckpoint], runtime htlRuntime) error {
		call, err := htlReadCall(ctx, runtime, task.Input)
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("Tool %s was aborted", call.Name)
		return htlSettle(ctx, runtime, call, htlEnding{status: TaskOutcomeAborted}, func(slot *ToolSlot) ToolExecutionResult {
			return htlFromSlot(slot, "aborted", msg)
		})
	},
})

func htlFindTool(tools []*ToolRegistration, name string) *ToolRegistration {
	for _, t := range tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func htlReadCall(ctx chord.Context, runtime htlRuntime, input ToolTaskInput) (*ai.ToolCall, error) {
	entry, err := runtime.TypedEntry(ctx, AssistantEntry, input.Assistant)
	if err != nil {
		return nil, err
	}
	if entry != nil && len(entry.Model) > 0 {
		if asst, ok := entry.Model[0].(*ai.AssistantMessage); ok {
			for _, block := range asst.Content {
				if tc, ok := block.(*ai.ToolCall); ok && tc.Id == input.CallId {
					return tc, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Entry %d has no tool call %s", input.Assistant, input.CallId)
}

func htlPrepare(tool *ToolRegistration, args JsonObject) (res JsonObject, errMsg string) {
	if args == nil {
		args = jsonx.NewObject()
	}
	if tool.PrepareArguments == nil {
		return args, ""
	}
	defer func() {
		if r := recover(); r != nil {
			errMsg = fmt.Sprint(r)
		}
	}()
	prepared := tool.PrepareArguments(args)
	obj, ok := prepared.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Sprintf("prepareArguments returned %T instead of *jsonx.Object", prepared)
	}
	return obj, ""
}

func htlValidate(tool *ToolRegistration, call *ai.ToolCall, args JsonObject) (JsonObject, string) {
	if tool == nil {
		return nil, "Tool is nil"
	}
	callCopy := *call
	callCopy.Arguments = args
	validated, err := ai.ValidateToolArguments(tool.AiTool(), &callCopy)
	if err != nil {
		return nil, err.Error()
	}
	if obj, ok := validated.(*jsonx.Object); ok && obj != nil {
		return obj, ""
	}
	encoded, err := jsonx.Encode(validated)
	if err != nil {
		return nil, err.Error()
	}
	if obj, ok := encoded.(*jsonx.Object); ok && obj != nil {
		return obj, ""
	}
	return jsonx.NewObject(), ""
}

func htlInvalid(message string) ToolExecutionResult {
	return HarnessError("invalid_arguments", message)
}

type htlReported struct {
	mu          sync.Mutex
	output      *OutputBuffer
	limits      OutputLimits
	diagnostics []ToolDiagnostic
	hasDetails  bool
	details     JsonValue
	ended       bool
}

type htlToolApi struct {
	runtime  htlRuntime
	call     *ai.ToolCall
	reported *htlReported
	progress *hoProgress
	execEnv  env.ExecutionEnv
	hasEnv   bool
}

func (a *htlToolApi) assertLive() {
	a.reported.mu.Lock()
	ended := a.reported.ended
	a.reported.mu.Unlock()
	if ended {
		panic(fmt.Sprintf("Tool call %s has settled", a.call.Id))
	}
}

func (a *htlToolApi) Snapshot(ctx chord.Context, token AnyDocToken, args ...any) (JsonObject, error) {
	return a.runtime.Snapshot(ctx, token, args...)
}

func (a *htlToolApi) SnapshotAsOf(ctx chord.Context, token AnyDocToken, conversationId ConversationId, args ...any) (JsonObject, error) {
	return a.runtime.SnapshotAsOf(ctx, token, conversationId, args...)
}

func (a *htlToolApi) WatchDoc(ctx chord.Context, token AnyDocToken, args ...any) (DocumentWatch, error) {
	return a.runtime.WatchDoc(ctx, token, args...)
}

func (a *htlToolApi) TaskId() TaskId                 { return a.runtime.TaskId() }
func (a *htlToolApi) ConversationId() ConversationId { return a.runtime.ConversationId() }
func (a *htlToolApi) CallId() string                 { return a.call.Id }
func (a *htlToolApi) Registry() RegistrySnapshot     { return a.runtime.Registry() }
func (a *htlToolApi) Agent(ctx chord.Context) (*Agent, error) {
	return a.runtime.Agent(ctx)
}
func (a *htlToolApi) Env(ctx chord.Context) (env.ExecutionEnv, error) {
	if a.hasEnv {
		if a.runtime.Signal().Err() != nil {
			return a.runtime.Env(ctx)
		}
		return a.execEnv, nil
	}
	return a.runtime.Env(ctx)
}

func (a *htlToolApi) Output(text string) {
	a.assertLive()
	if a.reported.output.Push(text) {
		a.progress.Mark()
	}
}

func (a *htlToolApi) OutputBytes(data []byte) {
	a.assertLive()
	if a.reported.output.PushBytes(data) {
		a.progress.Mark()
	}
}

func (a *htlToolApi) Diagnostic(diagnostic ToolDiagnostic) {
	a.assertLive()
	copied, err := stCopyStrictJsonValue(diagnostic)
	if err != nil {
		return
	}
	var diag ToolDiagnostic
	if err := jsonx.Decode(copied, &diag); err != nil {
		diag = diagnostic
	}
	a.reported.mu.Lock()
	a.reported.diagnostics = append(a.reported.diagnostics, diag)
	a.reported.mu.Unlock()
	a.progress.Mark()
}

func (a *htlToolApi) Details(value JsonValue, ctx chord.Context) error {
	a.assertLive()
	if err := hutCheckAborted(ctx); err != nil {
		return err
	}
	copied, err := stCopyStrictJsonValue(value)
	if err != nil {
		return err
	}
	a.reported.mu.Lock()
	a.reported.details = copied
	a.reported.hasDetails = true
	a.reported.mu.Unlock()
	return a.progress.MarkAndWait(ctx)
}

func (a *htlToolApi) Commit(ctx chord.Context, change func(tx Tx) (any, error)) (any, error) {
	var result any
	if err := a.runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
		var err error
		result, err = change(tx)
		return nil, err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *htlToolApi) Memo(ctx chord.Context, name string) (JsonValue, bool, error) {
	return a.runtime.Memo(ctx, name)
}

func (a *htlToolApi) MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error) {
	return a.runtime.MemoPut(ctx, name, candidate)
}

func (a *htlToolApi) CreateTask(ctx chord.Context, task AnyTask, input any, options TaskOptions) (TaskId, error) {
	var id TaskId
	if err := a.runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
		var err error
		id, err = tx.CreateTask(task, input, options)
		return nil, err
	}); err != nil {
		return 0, err
	}
	return id, nil
}

func (a *htlToolApi) GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error) {
	return a.runtime.GetTask(ctx, id)
}

func (a *htlToolApi) WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error) {
	return a.runtime.WaitForTask(ctx, id)
}

func (a *htlToolApi) Conversation(ctx chord.Context, id ConversationId) (ConversationHandle, error) {
	return a.runtime.Conversation(ctx, id)
}

func htlRun(
	ctx chord.Context,
	runtime htlRuntime,
	call *ai.ToolCall,
	tool *ToolRegistration,
	args JsonObject,
) error {
	limits := OutputLimits{
		MaxBytes: DefaultMaxBytes,
		MaxLines: DefaultMaxLines,
		Retain:   ToolOutputRetainHead,
	}
	if tool.OutputLimits != nil {
		if tool.OutputLimits.MaxBytes != nil {
			limits.MaxBytes = *tool.OutputLimits.MaxBytes
		}
		if tool.OutputLimits.MaxLines != nil {
			limits.MaxLines = *tool.OutputLimits.MaxLines
		}
		if tool.OutputLimits.Retain != "" {
			limits.Retain = tool.OutputLimits.Retain
		}
	}
	reported := &htlReported{
		output: NewOutputBuffer(limits),
		limits: limits,
	}
	progress := htlPublishProgress(ctx, runtime, reported)
	api := &htlToolApi{
		runtime:  runtime,
		call:     call,
		reported: reported,
		progress: progress,
	}

	var result *ToolExecutionResult
	var execErr error
	ending := htlCompleted
	if execEnv, err := runtime.Env(ctx); err != nil {
		execErr = err
	} else {
		api.execEnv = execEnv
		api.hasEnv = true
		if tool.Execute == nil {
			execErr = errors.New("tool execute function is nil")
		} else {
			_, execErr = htlExecuteCall(ctx, tool, args, api, &result)
		}
	}
	if execErr != nil {
		if runtime.Signal().Err() != nil {
			reported.mu.Lock()
			reported.ended = true
			reported.mu.Unlock()
			pending := progress.Stop()
			for _, w := range pending {
				w <- execErr
			}
			return execErr
		}
		res := ToolExecutionResult{
			IsError:     true,
			Diagnostics: []ToolDiagnostic{htlToolDiagnostic("tool_error", execErr.Error())},
		}
		result = &res
		ending = htlEnding{
			status:  TaskOutcomeFailed,
			message: fmt.Sprintf("Tool %s threw", call.Name),
		}
	}
	if result == nil {
		result = &ToolExecutionResult{}
	}

	reported.mu.Lock()
	reported.ended = true
	reported.mu.Unlock()
	reported.output.End()
	pending := progress.Stop()

	settled, err := htlFinalResult(ctx, runtime, call, *result, reported)
	if err == nil {
		err = htlSettle(ctx, runtime, call, ending, func(_ *ToolSlot) ToolExecutionResult {
			return settled
		})
	}
	if err != nil {
		for _, w := range pending {
			w <- err
		}
		return err
	}
	for _, w := range pending {
		w <- nil
	}
	return nil
}

func htlExecuteCall(
	ctx chord.Context,
	tool *ToolRegistration,
	args JsonObject,
	api ToolExecutionApi,
	out **ToolExecutionResult,
) (panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	res, err := tool.Execute(args, api, ctx)
	*out = res
	return false, err
}

func htlPublishProgress(ctx chord.Context, runtime htlRuntime, reported *htlReported) *hoProgress {
	writtenText := ""
	var writtenDetails JsonValue
	writtenDetailsSet := false
	writtenDiagnostics := 0

	return hoNewProgress(
		func() (int, error) {
			snapshot := reported.output.Snapshot()
			reported.mu.Lock()
			curDetails := reported.details
			curHasDetails := reported.hasDetails
			diagCount := len(reported.diagnostics)
			var added []ToolDiagnostic
			if diagCount > writtenDiagnostics {
				added = append([]ToolDiagnostic(nil), reported.diagnostics[writtenDiagnostics:diagCount]...)
			}
			reported.mu.Unlock()

			detailsChanged := (curHasDetails != writtenDetailsSet) || (curHasDetails && !heSameJsonRef(curDetails, writtenDetails))
			bytes := 0
			if snapshot.Text != writtenText {
				shared := 0
				if strings.HasPrefix(snapshot.Text, writtenText) {
					shared = js.Len(writtenText)
				} else {
					shared = chord.Overlap(writtenText, snapshot.Text, 65536)
				}
				bytes += Utf8ByteLength(js.SliceFrom(snapshot.Text, shared))
			}
			if detailsChanged {
				encoded, err := jsonx.Stringify(curDetails)
				if err == nil {
					bytes += Utf8ByteLength(encoded)
				}
			}
			if len(added) > 0 {
				encoded, err := jsonx.Stringify(added)
				if err == nil {
					bytes += Utf8ByteLength(encoded)
				}
			}

			var encodedAdded []any
			if len(added) > 0 {
				encodedAdded = make([]any, 0, len(added))
				for _, d := range added {
					enc, err := jsonx.Encode(d)
					if err != nil {
						return 0, err
					}
					encodedAdded = append(encodedAdded, enc)
				}
			}

			if err := runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				slot, ok := GetToolSlot(live.Value, runtime.TaskId())
				if !ok {
					return nil, nil
				}
				curOut := ""
				if rawOut, has := slot.Get("output"); has {
					if s, ok := rawOut.(string); ok {
						curOut = s
					}
				}
				if curOut != snapshot.Text {
					slot.Set("output", snapshot.Text)
				}
				if snapshot.DroppedBytes > 0 {
					slot.Set("droppedBytes", float64(snapshot.DroppedBytes))
				}
				if snapshot.DroppedLines > 0 {
					slot.Set("droppedLines", float64(snapshot.DroppedLines))
				}
				if detailsChanged && curHasDetails {
					hjAssignJson(slot, "details", curDetails)
				}
				if len(encodedAdded) > 0 {
					var existing []any
					if rawDiags, has := slot.Get("diagnostics"); has {
						if arr, isArr := rawDiags.([]any); isArr {
							existing = append([]any(nil), arr...)
						}
					}
					existing = append(existing, encodedAdded...)
					slot.Set("diagnostics", existing)
				}
				return nil, nil
			}); err != nil {
				return 0, err
			}

			writtenText = snapshot.Text
			writtenDetails = curDetails
			writtenDetailsSet = curHasDetails
			writtenDiagnostics = diagCount
			return bytes, nil
		},
		func(err error) {
			if runtime.Signal().Err() == nil {
				runtime.Report(err)
			}
		},
	)
}

func htlFinalResult(
	ctx chord.Context,
	runtime htlRuntime,
	call *ai.ToolCall,
	result ToolExecutionResult,
	reported *htlReported,
) (ToolExecutionResult, error) {
	var retained *BoundedOutput
	var content ai.ContentList
	if result.HasContent || result.Content != nil {
		content = result.Content
		if content == nil {
			content = ai.ContentList{}
		}
	} else {
		snap := reported.output.Snapshot()
		retained = &snap
		if snap.Text == "" {
			content = ai.ContentList{}
		} else {
			content = ai.ContentList{&ai.TextContent{Type: "text", Text: snap.Text}}
		}
	}

	reported.mu.Lock()
	repDetails := reported.details
	repHasDetails := reported.hasDetails
	repDiags := append([]ToolDiagnostic(nil), reported.diagnostics...)
	reported.mu.Unlock()

	details := result.Details
	hasDetails := result.HasDetails || result.Details != nil
	if !hasDetails && repHasDetails {
		details = repDetails
		hasDetails = true
	}

	allDiags := make([]ToolDiagnostic, 0, len(repDiags)+len(result.Diagnostics))
	allDiags = append(allDiags, repDiags...)
	allDiags = append(allDiags, result.Diagnostics...)

	final := ToolExecutionResult{
		Content:     content,
		HasContent:  true,
		IsError:     result.IsError,
		Details:     details,
		HasDetails:  hasDetails,
		Diagnostics: allDiags,
		Usage:       result.Usage,
		Control:     result.Control,
	}

	if err := runtime.Hooks().Each(func(h ToolHooks) error {
		if h.AfterTool == nil {
			return nil
		}
		copyRes := final
		next, err := h.AfterTool(call, &copyRes, runtime.Base(), ctx)
		if err != nil {
			return err
		}
		if next != nil {
			final = *next
		}
		return nil
	}); err != nil {
		return ToolExecutionResult{}, err
	}

	var harness []ToolDiagnostic
	if retained != nil && retained.DroppedBytes > 0 && htlSameContentSlice(final.Content, content) {
		harness = append(harness, htlTruncated(retained.DroppedLines, retained.DroppedBytes, &reported.limits.Retain))
	}
	boundedContent, droppedBytes, droppedLines := htlBoundContent(final.Content, reported.limits)
	if droppedBytes > 0 {
		harness = append(harness, htlTruncated(droppedLines, droppedBytes, &reported.limits.Retain))
	}
	final.Content = boundedContent
	final.HasContent = true
	final.Diagnostics = append(append([]ToolDiagnostic(nil), final.Diagnostics...), harness...)
	return final, nil
}

func htlSameContentSlice(a, b ai.ContentList) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return (a == nil) == (b == nil)
	}
	return &a[0] == &b[0]
}

func htlSettle(
	ctx chord.Context,
	runtime htlRuntime,
	call *ai.ToolCall,
	ending htlEnding,
	build func(slot *ToolSlot) ToolExecutionResult,
) error {
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[ToolTaskInput, ToolTaskCheckpoint]) (*htlNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		rawSlot, hasSlot := GetToolSlot(live.Value, runtime.TaskId())
		var decodedSlot *ToolSlot
		if hasSlot {
			var s ToolSlot
			if err := jsonx.Decode(rawSlot, &s); err == nil {
				decodedSlot = &s
			}
		}
		result := build(decodedSlot)
		entry, err := AppendToolResult(tx, runtime.ConversationId(), call, result, runtime.Now())
		if err != nil {
			return nil, err
		}
		if hasSlot {
			FinishSlot(rawSlot, &entry.Id)
		}
		entryId := entry.Id
		if ending.status == TaskOutcomeAborted {
			return &htlNext{
				Status: TaskStateTerminal,
				Outcome: &TypedTaskOutcome[ToolTaskResult]{
					Status: TaskOutcomeAborted,
					Result: ToolTaskResult{EntryId: entryId},
					HasRes: true,
				},
			}, nil
		}
		if ending.status == TaskOutcomeFailed {
			return &htlNext{
				Status: TaskStateTerminal,
				Outcome: &TypedTaskOutcome[ToolTaskResult]{
					Status: TaskOutcomeFailed,
					Error:  &TaskOutcomeError{Message: ending.message},
					Result: ToolTaskResult{EntryId: entryId},
					HasRes: true,
				},
			}, nil
		}
		var cleanControl *ToolControl
		if result.Control != nil {
			cleanJson, err := stCopyStrictJsonValue(result.Control)
			if err != nil {
				return nil, err
			}
			var ctrl ToolControl
			if err := jsonx.Decode(cleanJson, &ctrl); err != nil {
				return nil, err
			}
			cleanControl = &ctrl
		}
		return &htlNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[ToolTaskResult]{
				Status: TaskOutcomeCompleted,
				Result: ToolTaskResult{EntryId: entryId, Control: cleanControl},
				HasRes: true,
			},
		}, nil
	})
}

func htlFromSlot(slot *ToolSlot, code string, message string) ToolExecutionResult {
	var diags []ToolDiagnostic
	if slot != nil {
		diags = append(diags, slot.Diagnostics...)
		droppedBytes := 0
		if slot.DroppedBytes != nil {
			droppedBytes = *slot.DroppedBytes
		}
		if droppedBytes > 0 {
			droppedLines := 0
			if slot.DroppedLines != nil {
				droppedLines = *slot.DroppedLines
			}
			diags = append(diags, htlTruncated(droppedLines, droppedBytes, nil))
		}
	}
	diags = append(diags, htlToolDiagnostic(code, message))
	var content ai.ContentList
	if slot != nil && slot.Output != nil && *slot.Output != "" {
		content = ai.ContentList{&ai.TextContent{Type: "text", Text: *slot.Output}}
	} else {
		content = ai.ContentList{}
	}
	res := ToolExecutionResult{
		Content:     content,
		HasContent:  true,
		IsError:     true,
		Diagnostics: diags,
	}
	if slot != nil && slot.Details != nil {
		res.Details = slot.Details
		res.HasDetails = true
	}
	return res
}

// HarnessError constructs a Harness-authored error ToolExecutionResult with one error diagnostic.
func HarnessError(code string, message string) ToolExecutionResult {
	return ToolExecutionResult{
		Content:     ai.ContentList{},
		HasContent:  true,
		IsError:     true,
		Diagnostics: []ToolDiagnostic{htlToolDiagnostic(code, message)},
	}
}

func htlToolDiagnostic(code string, message string) ToolDiagnostic {
	c := code
	return ToolDiagnostic{
		Severity: ToolDiagnosticError,
		Code:     &c,
		Message:  message,
	}
}

func htlTruncated(droppedLines int, droppedBytes int, retain *ToolOutputRetain) ToolDiagnostic {
	kept := ""
	if retain != nil {
		if *retain == ToolOutputRetainHead {
			kept = " to its beginning"
		} else {
			kept = " to its end"
		}
	}
	code := "truncated"
	return ToolDiagnostic{
		Severity: ToolDiagnosticWarn,
		Code:     &code,
		Message:  fmt.Sprintf("Output truncated%s: %d lines, %d bytes dropped", kept, droppedLines, droppedBytes),
	}
}

// AppendToolResult appends a pi.tool-result entry and records any usage in pi.usage within the same commit.
func AppendToolResult(
	tx Tx,
	conversationId ConversationId,
	call *ai.ToolCall,
	result ToolExecutionResult,
	timestamp int64,
) (*EntryRecord, error) {
	diags := append([]ToolDiagnostic(nil), result.Diagnostics...)
	if diags == nil {
		diags = []ToolDiagnostic{}
	}
	content := make(ai.ContentList, len(result.Content))
	copy(content, result.Content)
	if len(diags) > 0 {
		content = append(content, &ai.TextContent{
			Type: "text",
			Text: htlRenderDiagnostics(diags),
		})
	}
	var detailsOpt jsonx.Opt[any]
	if result.HasDetails || result.Details != nil {
		if result.Details == nil {
			detailsOpt = jsonx.Null[any]()
		} else {
			detailsOpt = jsonx.Some(result.Details)
		}
	}
	message := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: call.Id,
		ToolName:   call.Name,
		Content:    content,
		Details:    detailsOpt,
		Usage:      result.Usage,
		IsError:    result.IsError,
		Timestamp:  timestamp,
	}
	if result.Usage != nil {
		if err := huRecordUsage(tx, conversationId, "tools", call.Name, *result.Usage); err != nil {
			return nil, err
		}
	}
	return tx.AppendTypedEntry(ToolResultEntry, conversationId, EntryDraft{
		Model: ai.MessageList{message},
		Data:  jsonx.ObjectOf("diagnostics", diags),
	})
}

func htlRenderDiagnostics(diagnostics []ToolDiagnostic) string {
	lines := make([]string, len(diagnostics))
	for i, d := range diagnostics {
		lines[i] = fmt.Sprintf("[%s] %s", d.Severity, d.Message)
	}
	return "<harness>\n" + strings.Join(lines, "\n") + "\n</harness>"
}

func htlBoundContent(content ai.ContentList, limits OutputLimits) (ai.ContentList, int, int) {
	var texts []string
	var textIndices []int
	for i, item := range content {
		if tc, ok := item.(*ai.TextContent); ok {
			texts = append(texts, tc.Text)
			textIndices = append(textIndices, i)
		}
	}
	bounded := BoundOutput(strings.Join(texts, ""), limits)
	if bounded.DroppedBytes == 0 {
		return content, 0, 0
	}
	keepIndex := -1
	if len(textIndices) > 0 {
		if limits.Retain == ToolOutputRetainHead {
			keepIndex = textIndices[0]
		} else {
			keepIndex = textIndices[len(textIndices)-1]
		}
	}
	out := make(ai.ContentList, 0, len(content))
	for i, item := range content {
		tc, ok := item.(*ai.TextContent)
		if !ok {
			out = append(out, item)
			continue
		}
		if i == keepIndex {
			copied := *tc
			copied.Text = bounded.Text
			out = append(out, &copied)
		}
	}
	return out, bounded.DroppedBytes, bounded.DroppedLines
}

var _ = slices.Clone[[]int]
