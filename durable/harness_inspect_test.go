// Ported from packages/durable/test/harness-inspect.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
)

type thisStep struct {
	Phase string `json:"phase"`
}

func thisTask(name string, version int, gate <-chan struct{}, migrate func()) *durable.Task[any, thisStep, any, struct{}] {
	var mig func(input durable.JsonValue, checkpoint durable.JsonValue, fromVersion int) (any, thisStep, error)
	if migrate != nil {
		mig = func(_ durable.JsonValue, _ durable.JsonValue, _ int) (any, thisStep, error) {
			migrate()
			return nil, thisStep{Phase: "run"}, nil
		}
	}
	return durable.DefineTask(durable.TaskDefinition[any, thisStep, any, struct{}]{
		Name:    name,
		Version: version,
		Initial: func(_ any) thisStep {
			return thisStep{Phase: "run"}
		},
		Phases: map[string]durable.PhaseHandler[any, thisStep, any, struct{}]{
			"run": func(ctx chord.Context, _ durable.RunningTask[any, thisStep], runtime *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
				if gate != nil {
					<-gate
				}
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thisStep]) (*durable.NextTaskState[thisStep, any], error) {
					next := testkit.Completed[thisStep, any](thisStep{Phase: "run"}, nil)
					return &next, nil
				})
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[any, thisStep], _ *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
			return nil
		},
		Migrate: mig,
	})
}

func thisStateOf(tasks []durable.TaskInspection, id durable.TaskId) *durable.TaskInspectionState {
	for i := range tasks {
		if tasks[i].Record.Id == id {
			st := tasks[i].State
			return &st
		}
	}
	return nil
}

func TestHarnessInspect(t *testing.T) {
	t.Run("derives every live task's state without running task code", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		gateTask := thisTask("test.gate", 1, gate.Done(), nil)
		var gateId durable.TaskId
		dependent := durable.DefineTask(durable.TaskDefinition[any, thisStep, any, struct{}]{
			Name:    "test.dependent",
			Version: 1,
			Initial: func(_ any) thisStep {
				return thisStep{Phase: "wait"}
			},
			Phases: map[string]durable.PhaseHandler[any, thisStep, any, struct{}]{
				"wait": func(ctx chord.Context, _ durable.RunningTask[any, thisStep], runtime *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thisStep]) (*durable.NextTaskState[thisStep, any], error) {
						return &durable.NextTaskState[thisStep, any]{
							Status:     durable.TaskStateWaiting,
							Checkpoint: thisStep{Phase: "done"},
							On:         []durable.TaskId{gateId},
							Policy:     durable.JoinPolicyAllSettled,
						}, nil
					})
				},
				"done": func(ctx chord.Context, _ durable.RunningTask[any, thisStep], runtime *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thisStep]) (*durable.NextTaskState[thisStep, any], error) {
						next := testkit.Completed[thisStep, any](thisStep{Phase: "done"}, nil)
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thisStep], _ *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
				return nil
			},
		})
		var migrations atomic.Int64
		migrating := thisTask("test.migrating", 2, nil, func() {
			migrations.Add(1)
		})
		noMigration := thisTask("test.no-migration", 2, nil, nil)
		failing := durable.DefineTask(durable.TaskDefinition[any, thisStep, any, struct{}]{
			Name:    "test.failing",
			Version: 2,
			Initial: func(_ any) thisStep { return thisStep{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[any, thisStep, any, struct{}]{
				"run": func(ctx chord.Context, _ durable.RunningTask[any, thisStep], runtime *durable.TaskRuntime[any, thisStep, any, struct{}]) error {
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thisStep]) (*durable.NextTaskState[thisStep, any], error) {
						next := testkit.Completed[thisStep, any](thisStep{Phase: "run"}, nil)
						return &next, nil
					})
				},
			},
			Migrate: func(_ durable.JsonValue, _ durable.JsonValue, _ int) (any, thisStep, error) {
				return nil, thisStep{}, errors.New("cannot migrate")
			},
		})
		tooOld := thisTask("test.too-old", 1, nil, nil)
		registered := []durable.AnyTask{gateTask, dependent, migrating, noMigration, failing, tooOld}

		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), registered)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}

		type createdIds struct {
			gate        durable.TaskId
			dependent   durable.TaskId
			migrating   durable.TaskId
			noMigration durable.TaskId
			failing     durable.TaskId
			tooOld      durable.TaskId
			missing     durable.TaskId
		}
		rawIds, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			var out createdIds
			var err error
			if out.gate, err = tx.CreateTask(gateTask, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			gateId = out.gate
			if out.dependent, err = tx.CreateTask(dependent, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			if out.migrating, err = tx.CreateTask(thisTask("test.migrating", 1, nil, nil), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			if out.noMigration, err = tx.CreateTask(thisTask("test.no-migration", 1, nil, nil), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			if out.failing, err = tx.CreateTask(thisTask("test.failing", 1, nil, nil), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			if out.tooOld, err = tx.CreateTask(thisTask("test.too-old", 2, nil, nil), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			if out.missing, err = tx.CreateTask(thisTask("test.missing", 1, nil, nil), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}}); err != nil {
				return nil, err
			}
			return out, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := rawIds.(createdIds)

		paused, err := opened.Harness.Inspect(nil)
		if err != nil {
			t.Fatal(err)
		}
		if paused.Scheduling != durable.HarnessSchedulingPaused {
			t.Fatalf("paused.Scheduling = %q", paused.Scheduling)
		}
		var pausedOrder []durable.TaskId
		for _, entry := range paused.Tasks {
			pausedOrder = append(pausedOrder, entry.Record.Id)
		}
		wantOrder := []durable.TaskId{ids.gate, ids.dependent, ids.migrating, ids.noMigration, ids.failing, ids.tooOld, ids.missing}
		if !slices.Equal(pausedOrder, wantOrder) {
			t.Fatalf("paused task order = %v, want %v", pausedOrder, wantOrder)
		}
		if st := thisStateOf(paused.Tasks, ids.gate); st == nil || st.Kind != durable.TaskInspectionReady || st.Migrates {
			t.Fatalf("gate state = %+v", st)
		}
		if st := thisStateOf(paused.Tasks, ids.dependent); st == nil || st.Kind != durable.TaskInspectionReady || st.Migrates {
			t.Fatalf("dependent state = %+v", st)
		}
		if st := thisStateOf(paused.Tasks, ids.migrating); st == nil || st.Kind != durable.TaskInspectionReady || !st.Migrates {
			t.Fatalf("migrating state = %+v", st)
		}
		// A migration that was never tried is not run to find out.
		if st := thisStateOf(paused.Tasks, ids.failing); st == nil || st.Kind != durable.TaskInspectionReady || !st.Migrates {
			t.Fatalf("failing state = %+v", st)
		}
		if st := thisStateOf(paused.Tasks, ids.noMigration); st == nil || st.Kind != durable.TaskInspectionBlocked || st.Reason != durable.TaskBlockedMigrationFailed || st.Error == nil || st.Error.Error() != "Task test.no-migration version 2 has no migration from 1" {
			t.Fatalf("noMigration state = %+v", st)
		}
		if st := thisStateOf(paused.Tasks, ids.tooOld); st == nil || st.Kind != durable.TaskInspectionBlocked || st.Reason != durable.TaskBlockedTaskTooOld {
			t.Fatalf("tooOld state = %+v", st)
		}
		if st := thisStateOf(paused.Tasks, ids.missing); st == nil || st.Kind != durable.TaskInspectionBlocked || st.Reason != durable.TaskBlockedMissingTask {
			t.Fatalf("missing state = %+v", st)
		}
		if got := migrations.Load(); got != 0 {
			t.Fatalf("migrations = %d, want 0", got)
		}
		if again, err := opened.Harness.Inspect(nil); err != nil || again.Scheduling != durable.HarnessSchedulingPaused {
			t.Fatalf("second inspect = %+v err=%v", again, err)
		}

		if err := opened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		if err := testkit.Eventually(func() (bool, error) { return migrations.Load() == 1, nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := opened.Harness.WaitForTask(nil, ids.migrating); err != nil {
			t.Fatal(err)
		}
		var running *durable.HarnessInspection
		if err := testkit.WaitFor(func() (bool, error) {
			var err error
			running, err = opened.Harness.Inspect(nil)
			if err != nil {
				return false, err
			}
			sg := thisStateOf(running.Tasks, ids.gate)
			sd := thisStateOf(running.Tasks, ids.dependent)
			return sg != nil && sg.Kind == durable.TaskInspectionRunning && sd != nil && sd.Kind == durable.TaskInspectionWaiting, nil
		}); err != nil {
			t.Fatal(err)
		}
		if running.Scheduling != durable.HarnessSchedulingRunning {
			t.Fatalf("running.Scheduling = %q", running.Scheduling)
		}
		if sd := thisStateOf(running.Tasks, ids.dependent); sd == nil || sd.Kind != durable.TaskInspectionWaiting || !slices.Equal(sd.On, []durable.TaskId{ids.gate}) {
			t.Fatalf("dependent state = %+v", sd)
		}
		if thisStateOf(running.Tasks, ids.migrating) != nil {
			t.Fatalf("expected migrating task to be cleared")
		}
		if sf := thisStateOf(running.Tasks, ids.failing); sf == nil || sf.Kind != durable.TaskInspectionBlocked || sf.Reason != durable.TaskBlockedMigrationFailed || sf.Error == nil || sf.Error.Error() != "cannot migrate" {
			t.Fatalf("failing state = %+v", sf)
		}

		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, ids.dependent); err != nil {
			t.Fatal(err)
		}
		settled, err := opened.Harness.Inspect(nil)
		if err != nil {
			t.Fatal(err)
		}
		var settledIds []durable.TaskId
		for _, entry := range settled.Tasks {
			settledIds = append(settledIds, entry.Record.Id)
		}
		if !slices.Equal(settledIds, []durable.TaskId{ids.noMigration, ids.failing, ids.tooOld, ids.missing}) {
			t.Fatalf("settledIds = %v", settledIds)
		}
	})

	t.Run("lists unsettled submissions", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		busyStep, reached := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{busyStep})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		other, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "note"},
		}, nil); err != nil {
			t.Fatal(err)
		}
		text := "hi"
		input, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: &text},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		<-reached

		inspection, err := harness.Inspect(nil)
		if err != nil {
			t.Fatal(err)
		}
		st, err := input.Status(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(inspection.Submissions) != 1 || inspection.Submissions[0].Id != st.Id || inspection.Submissions[0].Status != st.Status {
			t.Fatalf("submissions = %+v, want [%+v]", inspection.Submissions, st)
		}
		if len(inspection.Tasks) != 1 || inspection.Tasks[0].Record.Kind != "pi.generation" || inspection.Tasks[0].State.Kind != durable.TaskInspectionRunning {
			t.Fatalf("tasks = %+v", inspection.Tasks)
		}
	})
}
