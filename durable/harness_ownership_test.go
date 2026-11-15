// Ported from packages/durable/test/harness-ownership.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type thowEnding string

const (
	thowEndingCompleted thowEnding = "completed"
	thowEndingFailed    thowEnding = "failed"
)

type thowHoldInput struct {
	Name      string `json:"name"`
	SlowAbort bool   `json:"slowAbort,omitzero"`
}

type thowHoldCheckpoint struct {
	Phase string `json:"phase"`
}

type thowWaiterInput struct {
	On []durable.TaskId `json:"on"`
}

type thowWaiterCheckpoint struct {
	Phase string `json:"phase"`
}

type thowHarnessEnv struct {
	harness durable.Harness
	root    durable.Conversation
	opened  *testkit.OpenTasksResult
	mu      sync.Mutex
	gates   map[string]*testkit.Deferred[thowEnding]
	runs    map[string]int
	hold    *durable.Task[thowHoldInput, thowHoldCheckpoint, any, struct{}]
	waiter  *durable.Task[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]
	unreg   *durable.Task[thowHoldInput, thowHoldCheckpoint, any, struct{}]
}

func thowNewEnv(t *testing.T, st durable.Storage) *thowHarnessEnv {
	t.Helper()
	if st == nil {
		st = storage.NewMemoryStorage()
	}
	env := &thowHarnessEnv{
		gates: make(map[string]*testkit.Deferred[thowEnding]),
		runs:  make(map[string]int),
	}
	getGate := func(name string) *testkit.Deferred[thowEnding] {
		env.mu.Lock()
		defer env.mu.Unlock()
		g := env.gates[name]
		if g == nil {
			g = testkit.NewDeferred[thowEnding]()
			env.gates[name] = g
		}
		return g
	}

	env.hold = durable.DefineTask(durable.TaskDefinition[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
		Name:    "test.hold",
		Version: 1,
		Initial: func(thowHoldInput) thowHoldCheckpoint { return thowHoldCheckpoint{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
			"hold": func(ctx chord.Context, task durable.RunningTask[thowHoldInput, thowHoldCheckpoint], runtime *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
				env.mu.Lock()
				env.runs[task.Input.Name]++
				env.mu.Unlock()
				g := getGate(task.Input.Name)
				var sigDone <-chan struct{}
				if sig := runtime.Signal(); sig != nil {
					sigDone = sig.Done()
				}
				var ending thowEnding
				select {
				case <-g.Done():
					ending, _ = g.Wait()
				case <-sigDone:
					return context.Cause(runtime.Signal())
				}
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowHoldInput, thowHoldCheckpoint]) (*durable.NextTaskState[thowHoldCheckpoint, any], error) {
					if ending == thowEndingCompleted {
						return &durable.NextTaskState[thowHoldCheckpoint, any]{
							Status: durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[any]{
								Status: durable.TaskOutcomeCompleted,
								Result: nil,
								HasRes: true,
							},
						}, nil
					}
					return &durable.NextTaskState[thowHoldCheckpoint, any]{
						Status: durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{
							Status: durable.TaskOutcomeFailed,
							Error:  &durable.TaskOutcomeError{Message: "gate failed"},
						},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, task durable.RunningTask[thowHoldInput, thowHoldCheckpoint], runtime *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
			if task.Input.SlowAbort {
				g := getGate("abort." + task.Input.Name)
				var sigDone <-chan struct{}
				if sig := runtime.Signal(); sig != nil {
					sigDone = sig.Done()
				}
				select {
				case <-g.Done():
				case <-sigDone:
					return context.Cause(runtime.Signal())
				}
			}
			return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowHoldInput, thowHoldCheckpoint]) (*durable.NextTaskState[thowHoldCheckpoint, any], error) {
				return &durable.NextTaskState[thowHoldCheckpoint, any]{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[any]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	})

	env.waiter = durable.DefineTask(durable.TaskDefinition[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]{
		Name:    "test.waiter",
		Version: 1,
		Initial: func(thowWaiterInput) thowWaiterCheckpoint { return thowWaiterCheckpoint{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]{
			"wait": func(ctx chord.Context, task durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint], runtime *durable.TaskRuntime[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint]) (*durable.NextTaskState[thowWaiterCheckpoint, any], error) {
					return &durable.NextTaskState[thowWaiterCheckpoint, any]{
						Status:     durable.TaskStateWaiting,
						Checkpoint: thowWaiterCheckpoint{Phase: "done"},
						On:         task.Input.On,
						Policy:     durable.JoinPolicyAllSettled,
					}, nil
				})
			},
			"done": func(ctx chord.Context, _ durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint], runtime *durable.TaskRuntime[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint]) (*durable.NextTaskState[thowWaiterCheckpoint, any], error) {
					return &durable.NextTaskState[thowWaiterCheckpoint, any]{
						Status: durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{
							Status: durable.TaskOutcomeCompleted,
							Result: nil,
							HasRes: true,
						},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint], runtime *durable.TaskRuntime[thowWaiterInput, thowWaiterCheckpoint, any, struct{}]) error {
			return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowWaiterInput, thowWaiterCheckpoint]) (*durable.NextTaskState[thowWaiterCheckpoint, any], error) {
				return &durable.NextTaskState[thowWaiterCheckpoint, any]{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[any]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	})

	env.unreg = durable.DefineTask(durable.TaskDefinition[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
		Name:    "test.unregistered",
		Version: 1,
		Initial: func(thowHoldInput) thowHoldCheckpoint { return thowHoldCheckpoint{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
			"hold": func(chord.Context, durable.RunningTask[thowHoldInput, thowHoldCheckpoint], *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
				return nil
			},
		},
		Abort: func(chord.Context, durable.RunningTask[thowHoldInput, thowHoldCheckpoint], *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
			return nil
		},
	})

	opened, err := testkit.OpenTasks(st, []durable.AnyTask{env.hold, env.waiter})
	if err != nil {
		t.Fatalf("OpenTasks: %v", err)
	}
	root, err := opened.Harness.Root(nil)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if err := opened.Harness.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	env.harness = opened.Harness
	env.root = root
	env.opened = opened
	return env
}

func (e *thowHarnessEnv) open(name string, ending thowEnding) {
	e.mu.Lock()
	g := e.gates[name]
	if g == nil {
		g = testkit.NewDeferred[thowEnding]()
		e.gates[name] = g
	}
	e.mu.Unlock()
	g.Resolve(ending)
}

func (e *thowHarnessEnv) runCount(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[name]
}

type thowTree struct {
	owner durable.TaskId
	child durable.ConversationId
	inner durable.TaskId
}

func (e *thowHarnessEnv) ownedChild(t *testing.T, parent durable.Conversation, name string, bg, slowInner, slowOwner bool) thowTree {
	t.Helper()
	res, err := parent.Commit(nil, func(tx durable.Tx) (any, error) {
		owner, err := tx.CreateTask(e.hold, thowHoldInput{Name: name, SlowAbort: slowOwner}, durable.TaskOptions{
			Ownership:  durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			Background: thowPtr(bg),
		})
		if err != nil {
			return nil, err
		}
		child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: owner},
		})
		if err != nil {
			return nil, err
		}
		inner, err := tx.CreateTask(e.hold, thowHoldInput{Name: name + ".inner", SlowAbort: slowInner}, durable.TaskOptions{
			Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			ConversationId: &child.Id,
		})
		if err != nil {
			return nil, err
		}
		return thowTree{owner: owner, child: child.Id, inner: inner}, nil
	})
	if err != nil {
		t.Fatalf("ownedChild: %v", err)
	}
	return res.(thowTree)
}

func (e *thowHarnessEnv) status(t *testing.T, id durable.TaskId) durable.TaskState {
	t.Helper()
	rec, err := e.harness.GetTask(nil, id)
	if err != nil || rec == nil {
		t.Fatalf("GetTask(%d): rec=%v err=%v", id, rec, err)
	}
	return rec.State
}

func thowBusy(t *testing.T, conv durable.Conversation, task durable.TaskId) {
	t.Helper()
	if _, err := conv.Commit(nil, func(tx durable.Tx) (any, error) {
		draft, err := tx.Doc(durable.LiveDoc, conv.Id())
		if err != nil {
			return nil, err
		}
		draft.Value.Set("run", jsonx.ObjectOf("taskId", float64(task), "inputs", []any{}))
		return nil, nil
	}); err != nil {
		t.Fatalf("busy: %v", err)
	}
}

func thowSettled(fn func() error) bool {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case <-done:
		return true
	case <-time.After(30 * time.Millisecond):
		return false
	}
}

type thowRejectingSqlite struct {
	durable.Storage
	mu         sync.Mutex
	rejectMark *durable.TaskId
}

func (s *thowRejectingSqlite) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	target := s.rejectMark
	match := false
	if target != nil {
		for _, w := range writes {
			if tw, ok := w.(durable.StorageWriteTask); ok && tw.Value.Id == *target && tw.Value.AbortRequested {
				match = true
				break
			}
		}
	}
	if match {
		s.rejectMark = nil
		s.mu.Unlock()
		return 0, durable.NewStorageRejected("rejected once", nil)
	}
	s.mu.Unlock()
	return s.Storage.Commit(ctx, writes)
}

func TestHarnessOwnership(t *testing.T) {
	t.Run("keeps a conversation busy while its owned foreground subtree has live work holding the completed owner", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "owner", false, false, false)
		env.open("owner", thowEndingCompleted)
		if err := testkit.WaitFor(func() (bool, error) {
			return env.status(t, tree.owner).Status == durable.TaskStateCompleting, nil
		}); err != nil {
			t.Fatal(err)
		}
		if thowSettled(func() error { return env.root.WaitForIdle(nil) }) {
			t.Fatal("expected root WaitForIdle to block while owned foreground inner runs")
		}
		if thowSettled(func() error { return env.harness.WaitForIdle(nil) }) {
			t.Fatal("expected harness WaitForIdle to block while owned foreground inner runs")
		}
		env.open("owner.inner", thowEndingCompleted)
		if err := env.root.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if err := env.harness.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if st := env.status(t, tree.owner); st.Outcome == nil || st.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("owner state = %+v", st)
		}
	})

	t.Run("stops idle traversal at a background owner", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "background", true, false, false)
		if err := env.root.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if err := env.harness.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		child, _ := env.harness.Conversation(tree.child, nil)
		if thowSettled(func() error { return child.WaitForIdle(nil) }) {
			t.Fatal("expected background child to remain busy")
		}
		if err := child.Abort(nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cascades an abort mark to the owned foreground subtree and withdraws its queued inputs", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "owner", false, false, false)
		nested, _ := env.harness.Conversation(tree.child, nil)
		deeper := env.ownedChild(t, nested, "deeper", false, false, false)
		shielded := env.ownedChild(t, nested, "shielded", true, false, false)

		thowBusy(t, nested, tree.inner)
		queuedSub, _ := nested.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("later")}, nil)
		res, err := env.harness.AbortTask(nil, tree.owner)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = %s err=%v", res, err)
		}
		for _, id := range []durable.TaskId{tree.owner, tree.inner, deeper.owner, deeper.inner} {
			st, _ := env.harness.WaitForTask(nil, id)
			if st.State.Outcome == nil || st.State.Outcome.Status != durable.TaskOutcomeAborted {
				t.Fatalf("task %d outcome = %+v, want aborted", id, st.State.Outcome)
			}
		}
		if env.status(t, shielded.owner).Status == durable.TaskStateTerminal || env.status(t, shielded.inner).Status == durable.TaskStateTerminal {
			t.Fatal("expected nested background subtree to survive parent cascade")
		}
		qSub, _ := env.harness.Submission(nil, queuedSub.Id())
		qSt, _ := qSub.Status(nil)
		if qSt.Status != durable.SubmissionStatusUnanswered || qSt.Reason == nil || *qSt.Reason != "aborted" {
			t.Fatalf("queued sub status = %+v", qSt)
		}
		_, _ = env.harness.AbortTask(nil, shielded.owner)
	})

	t.Run("cascades a failed owner but not a completed one", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		failed := env.ownedChild(t, env.root, "failed", false, false, false)
		completed := env.ownedChild(t, env.root, "done", false, false, false)
		env.open("failed", thowEndingFailed)
		env.open("done", thowEndingCompleted)

		if st, _ := env.harness.WaitForTask(nil, failed.inner); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("failed.inner = %+v", st.State)
		}
		if st, _ := env.harness.WaitForTask(nil, failed.owner); st.State.Outcome.Status != durable.TaskOutcomeFailed {
			t.Fatalf("failed.owner = %+v", st.State)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			return env.status(t, completed.owner).Status == durable.TaskStateCompleting, nil
		}); err != nil {
			t.Fatal(err)
		}
		if env.status(t, completed.inner).Status == durable.TaskStateTerminal {
			t.Fatal("expected completed.inner still non-terminal")
		}
		env.open("done.inner", thowEndingCompleted)
		if st, _ := env.harness.WaitForTask(nil, completed.owner); st.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("completed.owner = %+v", st.State)
		}
	})

	t.Run("aborts a background task directly with its ordinary subtree", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "background", true, false, false)
		_, _ = env.harness.AbortTask(nil, tree.owner)
		if st, _ := env.harness.WaitForTask(nil, tree.inner); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("inner = %+v", st.State)
		}
	})

	t.Run("aborts a conversation: queued inputs withdrawn writes kept foreground aborted background kept", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		foreground := env.ownedChild(t, env.root, "foreground", false, false, false)
		background := env.ownedChild(t, env.root, "background", true, false, false)
		thowBusy(t, env.root, foreground.owner)
		input, _ := env.root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("later")}, nil)
		write, _ := env.root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}, nil)

		if err := env.root.Abort(nil); err != nil {
			t.Fatal(err)
		}
		for _, id := range []durable.TaskId{foreground.owner, foreground.inner} {
			if env.status(t, id).Status != durable.TaskStateTerminal {
				t.Fatalf("expected foreground task %d terminal", id)
			}
		}
		if env.status(t, background.owner).Status == durable.TaskStateTerminal {
			t.Fatal("expected background owner alive")
		}
		if err := env.root.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		inSt, _ := input.Status(nil)
		wrSt, _ := write.Status(nil)
		if inSt.Status != durable.SubmissionStatusUnanswered || inSt.Reason == nil || *inSt.Reason != "aborted" {
			t.Fatalf("input status = %+v", inSt)
		}
		if wrSt.Status != durable.SubmissionStatusQueued {
			t.Fatalf("write status = %+v", wrSt)
		}

		if err := env.root.Abort(nil, durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []durable.TaskId{background.owner, background.inner} {
			if env.status(t, id).Status != durable.TaskStateTerminal {
				t.Fatalf("expected background task %d terminal after background:true", id)
			}
		}
	})

	t.Run("keeps WaitForIdle and ownership traversal intact across reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		env1 := thowNewEnv(t, st1)
		fg := env1.ownedChild(t, env1.root, "fg", false, false, false)
		fgChild, _ := env1.harness.Conversation(fg.child, nil)
		deeper := env1.ownedChild(t, fgChild, "fg2", false, false, false)
		bg := env1.ownedChild(t, env1.root, "bg", true, false, false)
		env1.open("fg", thowEndingCompleted)
		env1.open("fg2", thowEndingCompleted)
		_ = testkit.WaitFor(func() (bool, error) {
			return env1.status(t, fg.owner).Status == durable.TaskStateCompleting, nil
		})
		_ = testkit.WaitFor(func() (bool, error) {
			return env1.status(t, deeper.owner).Status == durable.TaskStateCompleting, nil
		})
		_ = env1.harness.Close(nil)

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		env2 := thowNewEnv(t, st2)
		defer func() { _ = env2.harness.Close(nil) }()

		if thowSettled(func() error { return env2.root.WaitForIdle(nil) }) {
			t.Fatal("expected root WaitForIdle to stay busy across reopen")
		}
		if thowSettled(func() error { return env2.harness.WaitForIdle(nil) }) {
			t.Fatal("expected harness WaitForIdle to stay busy across reopen")
		}
		env2.open("fg.inner", thowEndingCompleted)
		env2.open("fg2.inner", thowEndingCompleted)
		if err := env2.root.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if err := env2.harness.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if env2.status(t, fg.owner).Status != durable.TaskStateTerminal {
			t.Fatal("expected fg.owner terminal")
		}
		if env2.status(t, bg.inner).Status == durable.TaskStateTerminal {
			t.Fatal("expected bg.inner still alive")
		}
		_, _ = env2.harness.AbortTask(nil, bg.owner)
	})

	for _, tc := range []struct {
		label          string
		abortRequested bool
		state          durable.TaskState
	}{
		{
			label:          "a held failed owner",
			abortRequested: false,
			state: durable.TaskState{
				Status: durable.TaskStateCompleting,
				Outcome: &durable.TaskOutcome{
					Status: durable.TaskOutcomeFailed,
					Error:  &durable.TaskOutcomeError{Message: "crash"},
				},
			},
		},
		{
			label:          "an abort-marked owner",
			abortRequested: true,
			state: durable.TaskState{
				Status:     durable.TaskStatePending,
				Checkpoint: map[string]any{"phase": "hold"},
			},
		},
	} {
		t.Run("derives marks a crash left unapplied below "+tc.label+" at open", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.sqlite")
			st1, err := sqlite.OpenNodeSqliteStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			sess := durable.NewSession(st1)
			tmpHold := durable.DefineTask(durable.TaskDefinition[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
				Name:    "test.hold",
				Version: 1,
				Initial: func(thowHoldInput) thowHoldCheckpoint { return thowHoldCheckpoint{Phase: "hold"} },
				Phases:  map[string]durable.PhaseHandler[thowHoldInput, thowHoldCheckpoint, any, struct{}]{},
			})
			var ownerId, innerId durable.TaskId
			if err := sess.Commit(nil, func(tx durable.Tx) error {
				rootConv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
					Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
				})
				if err != nil {
					return err
				}
				ownerId, err = tx.CreateTask(tmpHold, thowHoldInput{Name: "gone"}, durable.TaskOptions{
					Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
					ConversationId: &rootConv.Id,
				})
				if err != nil {
					return err
				}
				childConv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
					Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: ownerId},
				})
				if err != nil {
					return err
				}
				innerId, err = tx.CreateTask(tmpHold, thowHoldInput{Name: "orphan"}, durable.TaskOptions{
					Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
					ConversationId: &childConv.Id,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := sess.Commit(nil, func(tx durable.Tx) error {
				rec, err := tx.Task(ownerId)
				if err != nil {
					return err
				}
				updated := *rec
				updated.AbortRequested = tc.abortRequested
				updated.State = tc.state
				return tx.(*durable.Transaction).SetTask(updated)
			}); err != nil {
				t.Fatal(err)
			}
			_ = sess.Close(nil)

			st2, err := sqlite.OpenNodeSqliteStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			env2 := thowNewEnv(t, st2)
			defer func() { _ = env2.harness.Close(nil) }()

			if st, _ := env2.harness.WaitForTask(nil, innerId); st.State.Outcome.Status != durable.TaskOutcomeAborted {
				t.Fatalf("inner = %+v", st.State)
			}
			wantOutcome := durable.TaskOutcomeFailed
			if tc.abortRequested {
				wantOutcome = durable.TaskOutcomeAborted
			}
			if st, _ := env2.harness.WaitForTask(nil, ownerId); st.State.Outcome.Status != wantOutcome {
				t.Fatalf("owner = %+v, want %s", st.State, wantOutcome)
			}
		})
	}

	t.Run("withdraws an input queued below a held failed owner after its cascade", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "owner", false, true, false)
		child, _ := env.harness.Conversation(tree.child, nil)
		thowBusy(t, child, tree.inner)
		env.open("owner", thowEndingFailed)
		_ = testkit.WaitFor(func() (bool, error) {
			rec, err := env.harness.GetTask(nil, tree.inner)
			return err == nil && rec != nil && rec.AbortRequested, err
		})
		late, _ := child.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("late")}, nil)
		settled, _ := late.Wait(nil)
		if settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("late settled = %+v", settled)
		}
		env.open("abort.owner.inner", thowEndingCompleted)
		_, _ = env.harness.WaitForTask(nil, tree.owner)
	})

	t.Run("marks work admitted after reopen below a cancelled owner whose edge was not loaded", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		st1, _ := sqlite.OpenNodeSqliteStorage(path)
		env1 := thowNewEnv(t, st1)
		tree := env1.ownedChild(t, env1.root, "owner", false, false, true)
		env1.open("owner.inner", thowEndingCompleted)
		_, _ = env1.harness.WaitForTask(nil, tree.inner)
		_, _ = env1.harness.AbortTask(nil, tree.owner)
		_ = env1.harness.Close(nil)

		st2, _ := sqlite.OpenNodeSqliteStorage(path)
		env2 := thowNewEnv(t, st2)
		defer func() { _ = env2.harness.Close(nil) }()

		child, _ := env2.harness.Conversation(tree.child, nil)
		lateAny, _ := child.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(env2.hold, thowHoldInput{Name: "late"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		late := lateAny.(durable.TaskId)
		if st, _ := env2.harness.WaitForTask(nil, late); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("late = %+v", st.State)
		}
		env2.open("abort.owner", thowEndingCompleted)
		if st, _ := env2.harness.WaitForTask(nil, tree.owner); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("owner = %+v", st.State)
		}
	})

	t.Run("cascades from an owner the scheduler orphans", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		idsAny, err := env.root.Commit(nil, func(tx durable.Tx) (any, error) {
			owner, err := tx.CreateTask(env.unreg, thowHoldInput{Name: "unregistered"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: owner},
			})
			if err != nil {
				return nil, err
			}
			inner, err := tx.CreateTask(env.hold, thowHoldInput{Name: "below"}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &child.Id,
			})
			return [2]durable.TaskId{owner, inner}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := idsAny.([2]durable.TaskId)
		owner, inner := ids[0], ids[1]

		res, _ := env.harness.AbortTask(nil, owner)
		if res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = %s", res)
		}
		ownerSettled, _ := env.harness.WaitForTask(nil, owner)
		wantReason := "missing_task"
		if ownerSettled.State.Outcome.Status != durable.TaskOutcomeOrphaned || ownerSettled.State.Outcome.Reason == nil || *ownerSettled.State.Outcome.Reason != wantReason {
			t.Fatalf("owner outcome = %+v", ownerSettled.State.Outcome)
		}
		if innerSettled, _ := env.harness.WaitForTask(nil, inner); innerSettled.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("inner = %+v", innerSettled.State)
		}
	})

	t.Run("cancels only the caller wait never the shared work", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "owner", false, false, false)
		waitCtx := chord.WithCancel(nil)
		idleErr := testkit.NewDeferred[error]()
		go func() {
			idleErr.Resolve(env.root.WaitForIdle(waitCtx.Context))
		}()
		waitCtx.Cancel(errors.New("stop waiting"))
		if err, _ := idleErr.Wait(); err == nil || !strings.Contains(err.Error(), "stop waiting") {
			t.Fatalf("idleErr = %v", err)
		}
		if env.status(t, tree.inner).Status == durable.TaskStateTerminal {
			t.Fatal("expected inner work to continue")
		}

		slow := env.ownedChild(t, env.root, "slow", false, false, false)
		_, _ = env.root.Commit(nil, func(tx durable.Tx) (any, error) {
			rec, _ := tx.Task(slow.owner)
			upd := *rec
			upd.Input = jsonx.ObjectOf("name", "slow", "slowAbort", true)
			return nil, tx.(*durable.Transaction).SetTask(upd)
		})
		abortCtx := chord.WithCancel(nil)
		abortErr := testkit.NewDeferred[error]()
		go func() {
			abortErr.Resolve(env.root.Abort(abortCtx.Context))
		}()
		_ = testkit.WaitFor(func() (bool, error) {
			rec, err := env.harness.GetTask(nil, slow.owner)
			return err == nil && rec != nil && rec.AbortRequested, err
		})
		abortCtx.Cancel(errors.New("stop aborting"))
		if err, _ := abortErr.Wait(); err == nil || !strings.Contains(err.Error(), "stop aborting") {
			t.Fatalf("abortErr = %v", err)
		}
		env.open("abort.slow", thowEndingCompleted)
		for _, id := range []durable.TaskId{tree.owner, tree.inner, slow.owner, slow.inner} {
			if st, _ := env.harness.WaitForTask(nil, id); st.State.Outcome.Status != durable.TaskOutcomeAborted {
				t.Fatalf("task %d outcome = %+v", id, st.State.Outcome)
			}
		}
	})

	t.Run("aborts a waiting child whose awaited task completes in the commit that marks its owner", func(t *testing.T) {
		env := thowNewEnv(t, nil)
		defer func() { _ = env.harness.Close(nil) }()

		idsAny, _ := env.root.Commit(nil, func(tx durable.Tx) (any, error) {
			owner, _ := tx.CreateTask(env.hold, thowHoldInput{Name: "owner"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			child, _ := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: owner},
			})
			dep, _ := tx.CreateTask(env.hold, thowHoldInput{Name: "dependency"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			blocked, _ := tx.CreateTask(env.waiter, thowWaiterInput{On: []durable.TaskId{dep}}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &child.Id,
			})
			return [3]durable.TaskId{owner, dep, blocked}, nil
		})
		ids := idsAny.([3]durable.TaskId)
		owner, dep, blocked := ids[0], ids[1], ids[2]

		_ = testkit.WaitFor(func() (bool, error) {
			return env.runCount("dependency") == 1 && env.runCount("owner") == 1, nil
		})
		_ = testkit.WaitFor(func() (bool, error) {
			return env.status(t, blocked).Status == durable.TaskStateWaiting, nil
		})
		_, _ = env.root.Commit(nil, func(tx durable.Tx) (any, error) {
			ttx := tx.(*durable.Transaction)
			depRec, _ := tx.Task(dep)
			ownerRec, _ := tx.Task(owner)
			depDone := *depRec
			depDone.State = durable.TaskState{
				Status: durable.TaskStateTerminal,
				Outcome: &durable.TaskOutcome{
					Status: durable.TaskOutcomeCompleted,
					Result: jsonx.Null[durable.JsonValue](),
				},
			}
			ownerMarked := *ownerRec
			ownerMarked.AbortRequested = true
			if err := ttx.SetTask(depDone); err != nil {
				return nil, err
			}
			return nil, ttx.SetTask(ownerMarked)
		})
		if st, _ := env.harness.WaitForTask(nil, blocked); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("blocked = %+v", st.State)
		}
	})

	t.Run("retries marks found through an edge loaded after reopen when their commit is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		st1, _ := sqlite.OpenNodeSqliteStorage(path)
		env1 := thowNewEnv(t, st1)
		tree := env1.ownedChild(t, env1.root, "owner", false, false, true)
		env1.open("owner.inner", thowEndingCompleted)
		_, _ = env1.harness.WaitForTask(nil, tree.inner)
		_, _ = env1.harness.AbortTask(nil, tree.owner)
		_ = env1.harness.Close(nil)

		rawSt2, _ := sqlite.OpenNodeSqliteStorage(path)
		st2 := &thowRejectingSqlite{Storage: rawSt2}
		env2 := thowNewEnv(t, st2)
		defer func() { _ = env2.harness.Close(nil) }()

		child, _ := env2.harness.Conversation(tree.child, nil)
		lateAny, _ := child.Commit(nil, func(tx durable.Tx) (any, error) {
			id, err := tx.CreateTask(env2.hold, thowHoldInput{Name: "late"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			st2.mu.Lock()
			st2.rejectMark = &id
			st2.mu.Unlock()
			return id, err
		})
		late := lateAny.(durable.TaskId)
		_ = testkit.WaitFor(func() (bool, error) {
			st2.mu.Lock()
			done := st2.rejectMark == nil
			st2.mu.Unlock()
			return done, nil
		})
		if st, _ := env2.harness.WaitForTask(nil, late); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("late = %+v", st.State)
		}
		env2.open("abort.owner", thowEndingCompleted)
	})

	t.Run("retries a cascade whose commit the Storage rejected", func(t *testing.T) {
		st := &thowRejectingSqlite{Storage: storage.NewMemoryStorage()}
		env := thowNewEnv(t, st)
		defer func() { _ = env.harness.Close(nil) }()

		tree := env.ownedChild(t, env.root, "owner", false, false, false)
		st.mu.Lock()
		st.rejectMark = &tree.inner
		st.mu.Unlock()
		env.open("owner", thowEndingFailed)

		_ = testkit.WaitFor(func() (bool, error) {
			for _, r := range env.opened.Reports() {
				var sr *durable.StorageRejected
				if errors.As(r, &sr) {
					return true, nil
				}
			}
			return false, nil
		})
		if env.status(t, tree.owner).Status != durable.TaskStateCompleting {
			t.Fatalf("owner status = %s, want completing", env.status(t, tree.owner).Status)
		}
		if env.status(t, tree.inner).Status == durable.TaskStateTerminal {
			t.Fatal("expected inner still non-terminal")
		}
		_, _ = env.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(env.root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if st, _ := env.harness.WaitForTask(nil, tree.inner); st.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("inner = %+v", st.State)
		}
		if st, _ := env.harness.WaitForTask(nil, tree.owner); st.State.Outcome.Status != durable.TaskOutcomeFailed {
			t.Fatalf("owner = %+v", st.State)
		}
	})
}

func TestHarnessOwnedConversationsFromToolsAndSupervisors(t *testing.T) {
	t.Run("gives a tool an invocation-bound handle whose submissions stay durable after call ends", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		var handle durable.ConversationHandle
		var missing durable.ConversationHandle
		var sub durable.Submission

		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "delegate",
			Description: "Delegates",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				childAny, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					if err != nil {
						return nil, err
					}
					if err := durable.Configure(tx, created.Id, durable.AgentChange{
						Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
					}); err != nil {
						return nil, err
					}
					return created.Id, nil
				})
				if err != nil {
					return nil, err
				}
				childId := childAny.(durable.ConversationId)
				missing, err = api.Conversation(callCtx, durable.ConversationId(99_999))
				if err != nil {
					return nil, err
				}
				handle, err = api.Conversation(callCtx, childId)
				if err != nil {
					return nil, err
				}
				sub, err = handle.Submit(durable.InputSubmissionDraft{
					Content:   thowUserText("child task"),
					RequestId: "child",
				}, callCtx)
				if err != nil {
					return nil, err
				}
				settled, err := sub.Wait(callCtx)
				if err != nil {
					return nil, err
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: string(settled.Status)}},
					HasContent: true,
				}, nil
			},
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("delegate", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thowPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("child answer")}, nil)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(nil) }()

		rootSub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("go")}, nil)
		_, _ = rootSub.Wait(nil)
		if missing != nil {
			t.Fatalf("expected nil missing conversation handle, got %v", missing)
		}
		if _, err := handle.Submit(durable.InputSubmissionDraft{Content: thowUserText("again")}, nil); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("expected ended error on Submit, got %v", err)
		}
		if err := handle.WaitForIdle(nil); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("expected ended error on WaitForIdle, got %v", err)
		}
		if err := handle.Abort(nil); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("expected ended error on Abort, got %v", err)
		}
		if _, err := sub.Wait(nil); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("expected ended error on sub.Wait, got %v", err)
		}

		childConv, _ := harness.Conversation(handle.Id(), nil)
		childEntries, _ := childConv.Entries(durable.ConversationEntryQuery{}, 100, nil, nil)
		userCount := 0
		for _, e := range childEntries.Items {
			if e.Kind == "pi.user" {
				userCount++
			}
		}
		if userCount != 1 {
			t.Fatalf("expected 1 child user entry, got %d", userCount)
		}
		insp, _ := harness.Inspect(nil)
		for _, task := range insp.Tasks {
			if task.Record.ConversationId == handle.Id() {
				t.Fatalf("expected no remaining child tasks, got %+v", task)
			}
		}
		persistedSub, _ := harness.Submission(nil, sub.Id())
		st, _ := persistedSub.Status(nil)
		if st.Status != durable.SubmissionStatusDone {
			t.Fatalf("persisted status = %+v", st)
		}
	})

	t.Run("rejects a handle operation queued on the line when the invocation ends before it runs", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		type readyInfo struct {
			child  durable.ConversationId
			taskId durable.TaskId
		}
		ready := testkit.NewDeferred[readyInfo]()
		goSig := testkit.NewDeferred[struct{}]()
		queued := testkit.NewDeferred[*testkit.Deferred[error]]()

		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "delegate",
			Description: "Delegates late",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				childAny, _ := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					if err != nil {
						return nil, err
					}
					return created.Id, nil
				})
				child := childAny.(durable.ConversationId)
				handle, _ := api.Conversation(callCtx, child)
				ready.Resolve(readyInfo{child: child, taskId: api.TaskId()})
				_, _ = goSig.Wait()
				subErr := testkit.NewDeferred[error]()
				subStarted := make(chan struct{})
				go func() {
					close(subStarted)
					_, err := handle.Submit(durable.InputSubmissionDraft{Content: thowUserText("late")}, nil)
					subErr.Resolve(err)
				}()
				<-subStarted
				queued.Resolve(subErr)

				return nil, testkit.Aborted(callCtx.AbortSignal())
			},
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("delegate", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thowPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})},
		})
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(nil) }()

		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("go")}, nil)
		info, _ := ready.Wait()
		release := testkit.NewDeferred[struct{}]()
		holdEntered := make(chan struct{})
		holding := testkit.NewDeferred[error]()
		go func() {
			_, err := root.Commit(nil, func(durable.Tx) (any, error) {
				close(holdEntered)
				_, _ = release.Wait()
				return nil, nil
			})
			holding.Resolve(err)
		}()
		<-holdEntered

		aborting := testkit.NewDeferred[error]()
		abortStarted := make(chan struct{})
		go func() {
			close(abortStarted)
			_, err := harness.AbortTask(nil, info.taskId)
			aborting.Resolve(err)
		}()
		<-abortStarted
		for range 5 {
			testkit.Flush()
		}
		goSig.Resolve(struct{}{})
		subErr, _ := queued.Wait()
		for range 5 {
			testkit.Flush()
		}
		release.Resolve(struct{}{})
		_, _ = holding.Wait()
		_, _ = aborting.Wait()
		if err, _ := subErr.Wait(); err == nil {
			t.Fatal("expected late handle submit error")
		}
		childConv, _ := harness.Conversation(info.child, nil)
		if entries, _ := childConv.Entries(durable.ConversationEntryQuery{}, 10, nil, nil); len(entries.Items) != 0 {
			t.Fatalf("expected 0 child entries, got %+v", entries.Items)
		}
		insp, _ := harness.Inspect(nil)
		for _, s := range insp.Submissions {
			if s.ConversationId == info.child {
				t.Fatalf("expected 0 child submissions, got %+v", s)
			}
		}
	})

	t.Run("aborts a subagent run with its parent conversation rejecting the waiting tool", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		var childMu sync.Mutex
		var childId durable.ConversationId
		toolWait := testkit.NewDeferred[string]()
		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "delegate",
			Description: "Delegates",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				cAny, _ := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					created, _ := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					_ = durable.Configure(tx, created.Id, durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})})
					return created.Id, nil
				})
				cid := cAny.(durable.ConversationId)
				childMu.Lock()
				childId = cid
				childMu.Unlock()
				handle, _ := api.Conversation(callCtx, cid)
				sub, _ := handle.Submit(durable.InputSubmissionDraft{Content: thowUserText("child task")}, callCtx)
				settled, err := sub.Wait(callCtx)
				if err != nil {
					toolWait.Resolve(err.Error())
					return nil, err
				}
				return &durable.ToolExecutionResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: string(settled.Status)}}, HasContent: true}, nil
			},
		}))
		childRun := thinGated(thinAnswer("never"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("delegate", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thowPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})},
			childRun.step,
		})
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("go")}, nil)
		<-childRun.reached
		_ = root.Abort(nil)

		settledIn, _ := input.Wait(nil)
		if settledIn.Status != durable.SubmissionStatusUnanswered || settledIn.Reason == nil || *settledIn.Reason != "aborted" {
			t.Fatalf("input = %+v", settledIn)
		}
		if msg, _ := toolWait.Wait(); msg == "" {
			t.Fatal("expected toolWait rejection message")
		}
		childMu.Lock()
		cid := childId
		childMu.Unlock()
		childConv, _ := harness.Conversation(cid, nil)
		_ = childConv.WaitForIdle(nil)
		if live, _ := harness.Snapshot(nil, durable.LiveDoc, cid); !jsonx.DeepEqual(live, jsonx.NewObject()) {
			t.Fatalf("expected empty child LiveDoc, got %v", live)
		}
		insp, _ := harness.Inspect(nil)
		for _, s := range insp.Submissions {
			if s.ConversationId == cid {
				t.Fatalf("expected no child submissions remaining, got %+v", s)
			}
		}
		_ = harness.WaitForIdle(nil)
	})

	t.Run("lets a running tool abort its owned child and continue", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		childRun := thinGated(thinAnswer("never"))
		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "delegate",
			Description: "Delegates and cancels",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				cAny, _ := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					created, _ := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					_ = durable.Configure(tx, created.Id, durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})})
					return created.Id, nil
				})
				cid := cAny.(durable.ConversationId)
				handle, _ := api.Conversation(callCtx, cid)
				sub, _ := handle.Submit(durable.InputSubmissionDraft{Content: thowUserText("child task")}, callCtx)
				<-childRun.reached
				if err := handle.Abort(callCtx); err != nil {
					return nil, err
				}
				settledChild, err := sub.Wait(callCtx)
				if err != nil {
					return nil, err
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "child " + string(settledChild.Status)}},
					HasContent: true,
				}, nil
			},
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("delegate", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thowPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})},
			childRun.step,
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(nil) }()

		sub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("go")}, nil)
		if settled, _ := sub.Wait(nil); settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v", settled)
		}
	})

	t.Run("aborts the children of a tool call that throws or is interrupted while run continues", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup, _ := testkit.NewChatSetup()
		hold := durable.DefineTask(durable.TaskDefinition[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
			Name:    "test.hold",
			Version: 1,
			Initial: func(thowHoldInput) thowHoldCheckpoint { return thowHoldCheckpoint{Phase: "hold"} },
			Phases: map[string]durable.PhaseHandler[thowHoldInput, thowHoldCheckpoint, any, struct{}]{
				"hold": func(_ chord.Context, _ durable.RunningTask[thowHoldInput, thowHoldCheckpoint], runtime *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
					<-runtime.Signal().Done()
					return context.Cause(runtime.Signal())
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[thowHoldInput, thowHoldCheckpoint], runtime *durable.TaskRuntime[thowHoldInput, thowHoldCheckpoint, any, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thowHoldInput, thowHoldCheckpoint]) (*durable.NextTaskState[thowHoldCheckpoint, any], error) {
					return &durable.NextTaskState[thowHoldCheckpoint, any]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		})
		_, _ = testkit.AddTask(setup.Registry, hold)

		var mu sync.Mutex
		var children []durable.TaskId
		toolRunning := testkit.NewDeferred[struct{}]()

		params := omap.NewMap[string, *typebox.Schema]()
		params.Set("mode", typebox.String())
		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "spawn",
			Description: "Starts owned work then throws or hangs",
			Parameters:  typebox.Object(params),
			Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				childAny, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					if err != nil {
						return nil, err
					}
					return tx.CreateTask(hold, thowHoldInput{Name: "child." + api.CallId()}, durable.TaskOptions{
						Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
						ConversationId: &created.Id,
					})
				})
				if err != nil {
					return nil, err
				}
				mu.Lock()
				children = append(children, childAny.(durable.TaskId))
				mu.Unlock()
				mode := ""
				if obj, ok := args.(*jsonx.Object); ok {
					if m, ok := obj.Get("mode"); ok {
						mode, _ = m.(string)
					}
				}
				if mode == "throw" {
					return nil, errors.New("spawn failed")
				}
				toolRunning.Resolve(struct{}{})

				return nil, testkit.Aborted(callCtx.AbortSignal())
			},
		}))
		call := func(mode, id string) *providers.FauxResponseMessage {
			return &providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("spawn", jsonx.ObjectOf("mode", mode), &providers.FauxToolCallOptions{Id: &id}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})}
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			call("throw", "c1"),
			&providers.FauxResponseMessage{Message: thinAnswer("after throw")},
			call("hang", "c2"),
		})

		st1, _ := sqlite.OpenNodeSqliteStorage(path)
		h1, root1, _ := testkit.OpenChat(st1, setup)
		subOne, _ := root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("one")}, nil)
		if st, _ := subOne.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("one settled = %+v", st)
		}
		mu.Lock()
		c0 := children[0]
		mu.Unlock()
		if tSt, _ := h1.WaitForTask(nil, c0); tSt.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("child[0] = %+v", tSt.State)
		}

		subTwo, _ := root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("two")}, nil)
		twoId := subTwo.Id()
		_, _ = toolRunning.Wait()
		_ = h1.Close(nil)

		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thinAnswer("after interrupt")},
		})
		st2, _ := sqlite.OpenNodeSqliteStorage(path)
		h2, _, _ := testkit.OpenChat(st2, setup)
		defer func() { _ = h2.Close(nil) }()
		_ = h2.Resume()

		mu.Lock()
		c1 := children[1]
		mu.Unlock()
		if tSt, _ := h2.WaitForTask(nil, c1); tSt.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("child[1] = %+v", tSt.State)
		}
		reTwo, _ := h2.Submission(nil, twoId)
		if st, _ := reTwo.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("two settled = %+v", st)
		}
		var tools []durable.TaskRecord
		_ = h2.Commit(nil, func(tx durable.Tx) error {
			p, err := tx.ScanTasks(durable.TaskQuery{Kind: thowPtr("pi.tool")}, 10, nil)
			tools = p.Items
			return err
		})
		var outcomes []durable.TaskOutcomeStatus
		for _, tr := range tools {
			if tr.State.Outcome != nil {
				outcomes = append(outcomes, tr.State.Outcome.Status)
			}
		}
		if !slices.Equal(outcomes, []durable.TaskOutcomeStatus{durable.TaskOutcomeFailed, durable.TaskOutcomeFailed}) {
			t.Fatalf("tool outcomes = %v", outcomes)
		}
	})

	t.Run("reruns a replay-safe subagent tool after a restart with the same child and submission", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup, _ := testkit.NewChatSetup()
		var mu sync.Mutex
		var children []durable.ConversationId
		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "subagent",
			Description: "Delegates",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Replay:      durable.ToolReplaySafe,
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				childAny, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					tid := api.TaskId()
					existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &tid}, 1, nil)
					if err != nil {
						return nil, err
					}
					if len(existing.Items) > 0 {
						return existing.Items[0].Id, nil
					}
					created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					if err != nil {
						return nil, err
					}
					if err := durable.Configure(tx, created.Id, durable.AgentChange{
						Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
					}); err != nil {
						return nil, err
					}
					return created.Id, nil
				})
				if err != nil {
					return nil, err
				}
				childId := childAny.(durable.ConversationId)
				mu.Lock()
				children = append(children, childId)
				mu.Unlock()
				handle, _ := api.Conversation(callCtx, childId)
				sub, err := handle.Submit(durable.InputSubmissionDraft{
					Content:   thowUserText("child task"),
					RequestId: fmt.Sprintf("subagent:%d", api.TaskId()),
				}, callCtx)
				if err != nil {
					return nil, err
				}
				settled, err := sub.Wait(callCtx)
				if err != nil {
					return nil, err
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: string(settled.Status)}},
					HasContent: true,
				}, nil
			},
		}))
		childRun := thinGated(thinAnswer("never"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("subagent", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thowPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thowPtr(ai.StopReasonToolUse)})},
			childRun.step,
		})

		st1, _ := sqlite.OpenNodeSqliteStorage(path)
		h1, root1, _ := testkit.OpenChat(st1, setup)
		inSub, _ := root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thowUserText("go")}, nil)
		inId := inSub.Id()
		<-childRun.reached
		_ = h1.Close(nil)

		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thinAnswer("child answer")},
			&providers.FauxResponseMessage{Message: thinAnswer("done")},
		})
		st2, _ := sqlite.OpenNodeSqliteStorage(path)
		h2, root2, _ := testkit.OpenChat(st2, setup)
		defer func() { _ = h2.Close(nil) }()

		reIn, _ := h2.Submission(nil, inId)
		if st, _ := reIn.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input settled = %+v", st)
		}
		mu.Lock()
		gotChildren := slices.Clone(children)
		mu.Unlock()
		if len(gotChildren) != 2 || gotChildren[1] != gotChildren[0] {
			t.Fatalf("children = %v", gotChildren)
		}
		childConv, _ := h2.Conversation(gotChildren[0], nil)
		childEntries, _ := childConv.Entries(durable.ConversationEntryQuery{}, 100, nil, nil)
		userCount := 0
		for _, e := range childEntries.Items {
			if e.Kind == "pi.user" {
				userCount++
			}
		}
		if userCount != 1 {
			t.Fatalf("expected 1 child user entry, got %d", userCount)
		}
		ctxView, _ := root2.Context(nil)
		var isErrors []bool
		for _, m := range ctxView.Messages {
			if tr, ok := m.(*ai.ToolResultMessage); ok {
				isErrors = append(isErrors, tr.IsError)
			}
		}
		if !slices.Equal(isErrors, []bool{false}) {
			t.Fatalf("toolResult isErrors = %v", isErrors)
		}
	})

	t.Run("lets a background supervisor resubmit after a restart without submitting twice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		childrenDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.children",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkInitial,
			Initial: func() durable.JsonObject { return jsonx.NewObject() },
		})
		if err != nil {
			t.Fatal(err)
		}
		type supervisorInput struct {
			Parent durable.ConversationId `json:"parent"`
		}
		type supervisorCheckpoint struct {
			Phase string `json:"phase"`
		}
		type supervisorResult struct {
			Answer durable.EntryId `json:"answer"`
		}
		supervisorTask := durable.DefineTask(durable.TaskDefinition[supervisorInput, supervisorCheckpoint, supervisorResult, struct{}]{
			Name:    "test.supervisor",
			Version: 1,
			Initial: func(supervisorInput) supervisorCheckpoint { return supervisorCheckpoint{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[supervisorInput, supervisorCheckpoint, supervisorResult, struct{}]{
				"run": func(ctx chord.Context, task durable.RunningTask[supervisorInput, supervisorCheckpoint], runtime *durable.TaskRuntime[supervisorInput, supervisorCheckpoint, supervisorResult, struct{}]) error {
					snap, err := runtime.Snapshot(ctx, childrenDoc, task.Input.Parent)
					if err != nil {
						return fmt.Errorf("snapshot: %w", err)
					}
					rawCid, _ := snap.Get("child")
					cid := durable.ConversationId(rawCid.(float64))
					child, err := runtime.Conversation(ctx, cid)
					if err != nil {
						return fmt.Errorf("conversation: %w", err)
					}
					sub, err := child.Submit(durable.InputSubmissionDraft{
						Content:   thowUserText("work"),
						RequestId: "stable",
					}, ctx)
					if err != nil {
						return fmt.Errorf("submit: %w", err)
					}
					settled, err := sub.Wait(ctx)
					if err != nil {
						return fmt.Errorf("wait: %w", err)
					}
					if settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
						return fmt.Errorf("settled status=%s type=%v answer=%v", settled.Status, settled.Type, settled.Answer)
					}
					if err := runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[supervisorInput, supervisorCheckpoint]) (*durable.NextTaskState[supervisorCheckpoint, supervisorResult], error) {
						return &durable.NextTaskState[supervisorCheckpoint, supervisorResult]{
							Status: durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[supervisorResult]{
								Status: durable.TaskOutcomeCompleted,
								Result: supervisorResult{Answer: *settled.Answer},
								HasRes: true,
							},
						}, nil
					}); err != nil {
						return fmt.Errorf("commit: %w", err)
					}
					return nil
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[supervisorInput, supervisorCheckpoint], runtime *durable.TaskRuntime[supervisorInput, supervisorCheckpoint, supervisorResult, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[supervisorInput, supervisorCheckpoint]) (*durable.NextTaskState[supervisorCheckpoint, supervisorResult], error) {
					return &durable.NextTaskState[supervisorCheckpoint, supervisorResult]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[supervisorResult]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		})

		setup, _ := testkit.NewChatSetup()
		_, _ = testkit.AddTask(setup.Registry, supervisorTask)
		first := thinGated(thinAnswer("never"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("answer")},
		})

		st1, _ := sqlite.OpenNodeSqliteStorage(path)
		h1, root1, _ := testkit.OpenChat(st1, setup)
		pairAny, err := root1.Commit(nil, func(tx durable.Tx) (any, error) {
			sup, err := tx.CreateTask(supervisorTask, supervisorInput{Parent: root1.Id()}, durable.TaskOptions{
				Ownership:  durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				Background: thowPtr(true),
			})
			if err != nil {
				return nil, err
			}
			created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: sup},
			})
			if err != nil {
				return nil, err
			}
			if err := durable.Configure(tx, created.Id, durable.AgentChange{
				Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
			}); err != nil {
				return nil, err
			}
			d, err := tx.Doc(childrenDoc, root1.Id())
			if err != nil {
				return nil, err
			}
			d.Value.Set("child", float64(created.Id))
			return [2]int64{int64(sup), int64(created.Id)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		pair := pairAny.([2]int64)
		supervisorId := durable.TaskId(pair[0])
		childId := durable.ConversationId(pair[1])
		_ = h1.Resume()
		<-first.reached
		_ = h1.Close(nil)

		st2, _ := sqlite.OpenNodeSqliteStorage(path)
		h2, root2, _ := testkit.OpenChat(st2, setup)
		defer func() { _ = h2.Close(nil) }()
		_ = h2.Resume()
		done, err := h2.WaitForTask(nil, supervisorId)
		if err != nil || done.State.Outcome == nil || done.State.Outcome.Status != durable.TaskOutcomeCompleted {
			var errDetail any
			if done != nil && done.State.Outcome != nil {
				errDetail = done.State.Outcome.Error
			}
			t.Fatalf("supervisor done = %+v outcome = %#v outcomeErr = %+v err=%v", done, done.State.Outcome, errDetail, err)
		}
		childConv, _ := h2.Conversation(childId, nil)
		childEntries, _ := childConv.Entries(durable.ConversationEntryQuery{}, 100, nil, nil)
		userCount := 0
		for _, e := range childEntries.Items {
			if e.Kind == "pi.user" {
				userCount++
			}
		}
		if userCount != 1 {
			t.Fatalf("expected 1 child user entry, got %d", userCount)
		}
		if err := root2.WaitForIdle(nil); err != nil {
			t.Fatalf("root2.WaitForIdle: %v", err)
		}
	})

	_ = reflect.TypeOf(0)
}

func thowPtr[T any](v T) *T { return &v }

func thowUserText(text string) ai.UserMessageContent {
	return ai.UserMessageContent{Text: &text}
}
