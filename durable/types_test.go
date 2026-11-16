// Ported from packages/durable/test/types.test.ts (pi v1.0.0).

package durable_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestDurableTypesBrandsNumericIDsAndTaskResults(t *testing.T) {
	conversationId := durable.IdFromNumber[durable.ConversationId](1)
	entryId := durable.IdFromNumber[durable.EntryId](2)
	answerId := durable.IdFromNumber[durable.EntryId](3)
	taskId := durable.IdFromNumber[durable.TaskId](4)
	submissionId := durable.IdFromNumber[durable.SubmissionId](5)
	documentId := durable.IdFromNumber[durable.DocumentId](6)
	seq := durable.SeqFromNumber(1)

	if int64(conversationId) != 1 || int64(entryId) != 2 || int64(answerId) != 3 ||
		int64(submissionId) != 5 || int64(documentId) != 6 || int64(seq) != 1 {
		t.Fatalf("unexpected branded id/seq numeric values")
	}
	encodedTaskId, err := jsonx.Stringify(taskId)
	if err != nil {
		t.Fatalf("jsonx.Stringify(taskId) failed: %v", err)
	}
	if encodedTaskId != "4" {
		t.Fatalf("expected taskId JSON %q, got %q", "4", encodedTaskId)
	}
}

func TestDurableTypesDiscriminatorDependentFields(t *testing.T) {
	conversationId := durable.IdFromNumber[durable.ConversationId](1)
	entryId := durable.IdFromNumber[durable.EntryId](2)
	answerId := durable.IdFromNumber[durable.EntryId](3)
	submissionId := durable.IdFromNumber[durable.SubmissionId](5)
	documentId := durable.IdFromNumber[durable.DocumentId](6)

	omit := durable.ContextEdit{Target: entryId, Action: durable.ContextEditActionOmit}
	replace := durable.ContextEdit{Target: entryId, Action: durable.ContextEditActionReplace, Messages: ai.MessageList{}}
	pending := durable.TaskState{
		Status:     durable.TaskStatePending,
		Checkpoint: jsonx.ObjectOf("phase", "ready"),
	}
	terminal := durable.TaskState{
		Status: durable.TaskStateTerminal,
		Outcome: &durable.TaskOutcome{
			Status: durable.TaskOutcomeCompleted,
			Result: jsonx.Some[durable.JsonValue](jsonx.ObjectOf("value", 1)),
		},
	}
	completedInput := durable.SubmissionRecord{
		Id:             submissionId,
		ConversationId: conversationId,
		Type:           durable.SubmissionTypeInput,
		Status:         durable.SubmissionStatusDone,
		Entry:          &entryId,
		Answer:         &answerId,
	}
	completedWrite := durable.SubmissionRecord{
		Id:             submissionId,
		ConversationId: conversationId,
		Type:           durable.SubmissionTypeWrite,
		Status:         durable.SubmissionStatusDone,
		Entry:          &entryId,
	}
	queuedWriteCreate := durable.SubmissionCreate{
		ConversationId: conversationId,
		Type:           durable.SubmissionTypeWrite,
		Status:         durable.SubmissionStatusQueued,
	}
	baseContent := durable.DocumentContent{
		Kind:    durable.DocumentContentBase,
		Version: 1,
		Value:   jsonx.ObjectOf("count", 1),
	}
	deltaContent := durable.DocumentContent{
		Kind:    durable.DocumentContentDelta,
		Version: 1,
		Ops:     chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: 2}},
	}
	conversationDocument := durable.DocumentCreate{
		Id:      documentId,
		Kind:    "test",
		Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: conversationId},
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
	}

	if omit.Action != durable.ContextEditActionOmit || replace.Action != durable.ContextEditActionReplace {
		t.Fatalf("unexpected context edit actions")
	}
	if pending.Status != durable.TaskStatePending || terminal.Status != durable.TaskStateTerminal {
		t.Fatalf("unexpected task statuses")
	}
	if completedInput.Answer == nil || *completedInput.Answer != answerId || completedWrite.Type != durable.SubmissionTypeWrite {
		t.Fatalf("unexpected submission records")
	}
	if queuedWriteCreate.Status != durable.SubmissionStatusQueued {
		t.Fatalf("unexpected queued write create status")
	}
	if baseContent.Kind != durable.DocumentContentBase || deltaContent.Kind != durable.DocumentContentDelta {
		t.Fatalf("unexpected document content kinds")
	}
	if conversationDocument.Fork != durable.DocumentForkAsOf {
		t.Fatalf("unexpected fork policy %q", conversationDocument.Fork)
	}
}

type ttypStepperHooks struct {
	BeforeStep func(step int, api durable.HookApi, ctx chord.Context) (skip bool, err error)
}

type ttypStepperInput struct {
	Steps int `json:"steps"`
}

type ttypStepperCheckpoint struct {
	Phase string `json:"phase"`
	Steps int    `json:"steps,omitzero"`
	Step  int    `json:"step,omitzero"`
}

type ttypStepperResult struct {
	Ran int `json:"ran"`
}

func TestDurableTypesSubmissionsTaskWaitsCompactionAndExtensions(t *testing.T) {
	ctx := testkit.TestContext
	input := durable.SubmissionDraft{
		Type:     durable.SubmissionTypeInput,
		Content:  ai.UserMessageContent{Text: new("hi")},
		WhenBusy: durable.SubmissionWhenBusySteer,
	}
	write := durable.SubmissionDraft{
		Type:  durable.SubmissionTypeWrite,
		Entry: &durable.EntryDraft{Kind: "note"},
	}
	if input.Type != durable.SubmissionTypeInput || write.Type != durable.SubmissionTypeWrite {
		t.Fatalf("unexpected submission draft types")
	}

	stepper := durable.DefineTask[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks](
		durable.TaskDefinition[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks]{
			Name:    "test.stepper",
			Version: 1,
			Initial: func(in ttypStepperInput) ttypStepperCheckpoint {
				return ttypStepperCheckpoint{Phase: "plan", Steps: in.Steps}
			},
			Phases: map[string]durable.PhaseHandler[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks]{
				"plan": func(c chord.Context, task durable.RunningTask[ttypStepperInput, ttypStepperCheckpoint], rt *durable.TaskRuntime[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks]) error {
					if task.Input.Steps < 0 || task.Checkpoint.Steps < 0 {
						t.Fatalf("unexpected negative steps")
					}
					return rt.Commit(c, func(_ durable.Tx, _ durable.RunningTask[ttypStepperInput, ttypStepperCheckpoint]) (*durable.NextTaskState[ttypStepperCheckpoint, ttypStepperResult], error) {
						return &durable.NextTaskState[ttypStepperCheckpoint, ttypStepperResult]{
							Status:     durable.TaskStateRunning,
							Checkpoint: ttypStepperCheckpoint{Phase: "run", Step: 0},
						}, nil
					})
				},
				"run": func(c chord.Context, task durable.RunningTask[ttypStepperInput, ttypStepperCheckpoint], rt *durable.TaskRuntime[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks]) error {
					if task.Checkpoint.Step < 0 {
						t.Fatalf("unexpected negative step")
					}
					if err := rt.Hooks().Each(func(h ttypStepperHooks) error {
						if h.BeforeStep != nil {
							_, hookErr := h.BeforeStep(1, rt.Base(), c)
							return hookErr
						}
						return nil
					}); err != nil {
						return err
					}
					return rt.Commit(c, func(_ durable.Tx, _ durable.RunningTask[ttypStepperInput, ttypStepperCheckpoint]) (*durable.NextTaskState[ttypStepperCheckpoint, ttypStepperResult], error) {
						next := testkit.Completed(task.Checkpoint, ttypStepperResult{Ran: 1})
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[ttypStepperInput, ttypStepperCheckpoint], _ *durable.TaskRuntime[ttypStepperInput, ttypStepperCheckpoint, ttypStepperResult, ttypStepperHooks]) error {
				return nil
			},
		},
	)

	reg := durable.Hook(stepper, ttypStepperHooks{
		BeforeStep: func(step int, _ durable.HookApi, _ chord.Context) (bool, error) {
			return step > 1, nil
		},
	})
	var erased durable.AnyTask = stepper
	ext := durable.DefineExtension(&durable.Extension{
		Name:  "stepper",
		Tasks: []durable.AnyTask{erased},
		Hooks: []durable.HookRegistration{reg},
	})
	if ext.Name != "stepper" || erased.TaskKind() != "test.stepper" {
		t.Fatalf("unexpected extension/task metadata: %q %q", ext.Name, erased.TaskKind())
	}

	st := testkit.NewControlledStorage()
	defer func() { _ = st.Close(ctx) }()
	res, err := testkit.OpenTasks(st, []durable.AnyTask{stepper})
	if err != nil {
		t.Fatalf("OpenTasks failed: %v", err)
	}
	defer func() { _ = res.Harness.Close(ctx) }()
	conv, err := res.Harness.Root(ctx)
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	rawId, err := conv.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(stepper, ttypStepperInput{Steps: 2}, durable.TaskOptions{
			Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
		})
	})
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	tid := rawId.(durable.TaskId)
	if err := res.Harness.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	settled, err := res.Harness.WaitForTask(ctx, tid)
	if err != nil {
		t.Fatalf("WaitForTask failed: %v", err)
	}
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted {
		t.Fatalf("expected completed stepper task, got %+v", settled.State.Outcome)
	}
	val, ok := settled.State.Outcome.Result.Get()
	if !ok {
		t.Fatalf("expected completed result")
	}
	encodedRes, err := jsonx.Stringify(val)
	if err != nil || !strings.Contains(encodedRes, `"ran":1`) {
		t.Fatalf("unexpected stepper result: %v (%v)", encodedRes, err)
	}

	compactionId, err := conv.Compact(nil, ctx)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	compSettled, err := res.Harness.WaitForTask(ctx, compactionId)
	if err != nil {
		t.Fatalf("WaitForTask(compaction) failed: %v", err)
	}
	if compSettled.State.Outcome == nil {
		t.Fatalf("expected compaction outcome")
	}
}
