// Ported from packages/durable/test/harness-tasks.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type thtkStep struct {
	Phase string `json:"phase"`
}

func thtkOneStep[R any](
	name string,
	run func(ctx chord.Context, task durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, R, struct{}]) error,
	abort ...durable.PhaseHandler[any, thtkStep, R, struct{}],
) *durable.Task[any, thtkStep, R, struct{}] {
	abortFn := durable.PhaseHandler[any, thtkStep, R, struct{}](func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, R, struct{}]) error {
		return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, R], error) {
			next := testkit.AbortedWith[thtkStep, R](current.Checkpoint, "test")
			return &next, nil
		})
	})
	if len(abort) > 0 && abort[0] != nil {
		abortFn = abort[0]
	}
	return durable.DefineTask(durable.TaskDefinition[any, thtkStep, R, struct{}]{
		Name:    name,
		Version: 1,
		Initial: func(_ any) thtkStep { return thtkStep{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[any, thtkStep, R, struct{}]{
			"run": run,
		},
		Abort: abortFn,
	})
}

func thtkGated(name string, gate <-chan struct{}) *durable.Task[any, thtkStep, any, struct{}] {
	return thtkOneStep[any](name, func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
		<-gate
		return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
			next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
			return &next, nil
		})
	})
}

func thtkStart(t *testing.T, conv durable.Conversation, task durable.AnyTask, background ...bool) durable.TaskId {
	t.Helper()
	opts := durable.TaskOptions{
		Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
	}
	if len(background) > 0 && background[0] {
		bg := true
		opts.Background = &bg
	}
	idAny, err := conv.Commit(nil, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(task, nil, opts)
	})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", task.TaskKind(), err)
	}
	return idAny.(durable.TaskId)
}

type thtkRoot struct {
	*testkit.OpenTasksResult
	Root durable.Conversation
}

func thtkOpenRoot(t *testing.T, tasks []durable.AnyTask, st durable.Storage, opts ...testkit.OpenTasksOptions) *thtkRoot {
	t.Helper()
	if st == nil {
		st = storage.NewMemoryStorage()
	}
	res, err := testkit.OpenTasks(st, tasks, opts...)
	if err != nil {
		t.Fatalf("OpenTasks: %v", err)
	}
	root, err := res.Harness.Root(nil)
	if err != nil {
		_ = res.Harness.Close(nil)
		t.Fatalf("Root: %v", err)
	}
	return &thtkRoot{OpenTasksResult: res, Root: root}
}

func thtkMarkDurably(t *testing.T, h durable.Harness, id durable.TaskId) <-chan struct {
	res durable.TaskAbortResult
	err error
} {
	t.Helper()
	ch := make(chan struct {
		res durable.TaskAbortResult
		err error
	}, 1)
	go func() {
		r, e := h.AbortTask(nil, id)
		ch <- struct {
			res durable.TaskAbortResult
			err error
		}{r, e}
	}()
	thtrEventually(t, func() bool {
		rec, err := h.GetTask(nil, id)
		return err == nil && rec != nil && rec.AbortRequested
	})
	return ch
}

type thtkPingHooks struct {
	Ping func()
}

type thtkClosingReader struct {
	registry durable.RegistryReader
	mu       sync.Mutex
	arm      bool
	harness  durable.Harness
	closing  *testkit.Deferred[chan error]
}

func (r *thtkClosingReader) Snapshot() durable.RegistrySnapshot {
	r.mu.Lock()
	doClose := r.arm
	if doClose {
		r.arm = false
	}
	h := r.harness
	r.mu.Unlock()
	if doClose && h != nil {
		idle := make(chan error, 1)
		go func() { idle <- h.WaitForIdle(nil) }()
		closeCh := make(chan error, 1)
		go func() { closeCh <- h.Close(nil) }()
		r.closing.Resolve(closeCh)
		<-idle
	}
	return r.registry.Snapshot()
}

func (r *thtkClosingReader) Subscribe(listener func()) func() {
	return r.registry.Subscribe(listener)
}

func TestHarnessTaskPhases(t *testing.T) {
	t.Run("continues one invocation through checkpoint progress and completes with a typed result", func(t *testing.T) {
		type countInput struct {
			To int `json:"to"`
		}
		type countCP struct {
			Phase string `json:"phase"`
			N     int    `json:"n"`
		}
		var mu sync.Mutex
		var seen []int
		runtimes := make(map[durable.AnyTaskRuntime]struct{})

		counter := durable.DefineTask(durable.TaskDefinition[countInput, countCP, int, struct{}]{
			Name:    "test.counter",
			Version: 1,
			Initial: func(_ countInput) countCP { return countCP{Phase: "count", N: 0} },
			Phases: map[string]durable.PhaseHandler[countInput, countCP, int, struct{}]{
				"count": func(ctx chord.Context, task durable.RunningTask[countInput, countCP], rt *durable.TaskRuntime[countInput, countCP, int, struct{}]) error {
					mu.Lock()
					seen = append(seen, task.Checkpoint.N)
					runtimes[rt.Base()] = struct{}{}
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[countInput, countCP]) (*durable.NextTaskState[countCP, int], error) {
						n := current.Checkpoint.N + 1
						if n == task.Input.To {
							next := testkit.Completed(current.Checkpoint, n)
							return &next, nil
						}
						return &durable.NextTaskState[countCP, int]{
							Status:     durable.TaskStateRunning,
							Checkpoint: countCP{Phase: "count", N: n},
						}, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[countInput, countCP], _ *durable.TaskRuntime[countInput, countCP, int, struct{}]) error {
				return nil
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{counter}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		idAny, err := opened.Root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(counter, countInput{To: 3}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		id := idAny.(durable.TaskId)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		receipt, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		resRaw, ok := receipt.State.Outcome.Result.Get()
		var got int
		if !ok || jsonx.Decode(resRaw, &got) != nil || got != 3 {
			t.Fatalf("result=%v ok=%v, want 3", resRaw, ok)
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		gotRuntimes := len(runtimes)
		mu.Unlock()
		if !slices.Equal(gotSeen, []int{0, 1, 2}) || gotRuntimes != 1 {
			t.Fatalf("seen=%v runtimes=%d", gotSeen, gotRuntimes)
		}
	})

	t.Run("faults a phase without durable progress and a throwing phase", func(t *testing.T) {
		idle := thtkOneStep[any]("test.idle", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return nil
		})
		docOnly := thtkOneStep[any]("test.document-only", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				return nil, nil
			})
		})
		throws := thtkOneStep[any]("test.throws", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return errors.New("boom")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{idle, docOnly, throws}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		ids := []durable.TaskId{
			thtkStart(t, opened.Root, idle),
			thtkStart(t, opened.Root, docOnly),
			thtkStart(t, opened.Root, throws),
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		wantMsgs := []string{
			"Task test.idle phase run returned without durable progress",
			"Task test.document-only phase run returned without durable progress",
			"boom",
		}
		for i, id := range ids {
			settled, err := opened.Harness.WaitForTask(nil, id)
			if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeFaulted || settled.State.Outcome.Error == nil || settled.State.Outcome.Error.Message != wantMsgs[i] {
				t.Fatalf("task[%d] outcome=%+v err=%v, want %q", i, settled.State.Outcome, err, wantMsgs[i])
			}
		}
	})

	t.Run("keeps a committed terminal outcome when the handler throws afterwards", func(t *testing.T) {
		lateErrD := testkit.NewDeferred[error]()
		done := thtkOneStep[string]("test.done", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, string, struct{}]) error {
			if err := rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, string], error) {
				next := testkit.Completed(current.Checkpoint, "ok")
				return &next, nil
			}); err != nil {
				return err
			}
			lateErr := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, string], error) {
				return nil, nil
			})
			lateErrD.Resolve(lateErr)
			return errors.New("after terminal")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{done}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, done)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		resVal, _ := settled.State.Outcome.Result.Get()
		if resVal != "ok" {
			t.Fatalf("result=%v, want ok", resVal)
		}
		lateErr, _ := lateErrD.Wait()
		if lateErr == nil || !strings.Contains(lateErr.Error(), fmt.Sprintf("Task %d is terminal", id)) {
			t.Fatalf("lateErr=%v, want Task %d is terminal", lateErr, id)
		}
	})

	t.Run("compares checkpoints by value, including arrays", func(t *testing.T) {
		type collectCP struct {
			Phase string   `json:"phase"`
			Items []string `json:"items"`
		}
		collect := durable.DefineTask(durable.TaskDefinition[any, collectCP, any, struct{}]{
			Name:    "test.collect",
			Version: 1,
			Initial: func(_ any) collectCP { return collectCP{Phase: "collect", Items: []string{}} },
			Phases: map[string]durable.PhaseHandler[any, collectCP, any, struct{}]{
				"collect": func(ctx chord.Context, task durable.RunningTask[any, collectCP], rt *durable.TaskRuntime[any, collectCP, any, struct{}]) error {
					items := task.Checkpoint.Items
					nextItems := slices.Clone(items)
					if len(items) < 2 {
						nextItems = append(nextItems, fmt.Sprintf("item%d", len(items)))
					}
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, collectCP]) (*durable.NextTaskState[collectCP, any], error) {
						return &durable.NextTaskState[collectCP, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: collectCP{Phase: "collect", Items: nextItems},
						}, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, collectCP], _ *durable.TaskRuntime[any, collectCP, any, struct{}]) error {
				return nil
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{collect}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		idAny, err := opened.Root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(collect, nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		id := idAny.(durable.TaskId)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		wantErr := "Task test.collect phase collect returned without durable progress"
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeFaulted || settled.State.Outcome.Error == nil || settled.State.Outcome.Error.Message != wantErr {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
	})

	t.Run("commits results with entries atomically, keeps memos until terminal, and retires task documents", func(t *testing.T) {
		progressDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.task-progress",
			Version: 1,
			Scope:   durable.DocumentScopeTask,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("lines", []any{}) },
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		answerEntry, err := durable.DefineEntry("answer")
		if err != nil {
			t.Fatalf("DefineEntry: %v", err)
		}
		child := thtkOneStep[any]("test.child", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		var mu sync.Mutex
		var childId durable.TaskId
		var progressSeen []string
		type writerRes struct {
			EntryId durable.EntryId `json:"entryId"`
		}
		writer := durable.DefineTask(durable.TaskDefinition[any, thtkStep, writerRes, struct{}]{
			Name:    "test.writer",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "write"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, writerRes, struct{}]{
				"write": func(ctx chord.Context, task durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, writerRes, struct{}]) error {
					if v, ok, err := rt.Memo(ctx, "choice"); err != nil || ok || v != nil {
						t.Fatalf("initial memo choice=(%v, %v, %v)", v, ok, err)
					}
					w1, err := rt.MemoPut(ctx, "choice", "a")
					if err != nil {
						return err
					}
					w2, err := rt.MemoPut(ctx, "choice", "b")
					if err != nil {
						return err
					}
					if w1 != "a" || w2 != "a" {
						t.Fatalf("winners=(%v, %v), want (a, a)", w1, w2)
					}
					if v, ok, err := rt.Memo(ctx, "choice"); err != nil || !ok || v != "a" {
						t.Fatalf("memo choice=(%v, %v, %v)", v, ok, err)
					}
					if v, ok, err := rt.Memo(ctx, "toString"); err != nil || ok || v != nil {
						t.Fatalf("memo toString=(%v, %v, %v)", v, ok, err)
					}
					if v, err := rt.MemoPut(ctx, "toString", "own"); err != nil || v != "own" {
						t.Fatalf("memoPut toString=(%v, %v)", v, err)
					}
					return rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, writerRes], error) {
						draft, err := tx.Doc(progressDoc, task.Id)
						if err != nil {
							return nil, err
						}
						draft.Value.Set("lines", []any{"wrote"})
						cid, err := tx.CreateTask(child, nil, durable.TaskOptions{
							Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
						})
						if err != nil {
							return nil, err
						}
						mu.Lock()
						childId = cid
						mu.Unlock()
						return &durable.NextTaskState[thtkStep, writerRes]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "answer"},
						}, nil
					})
				},
				"answer": func(ctx chord.Context, task durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, writerRes, struct{}]) error {
					cVal, _ := task.Memos.Get("choice")
					sVal, _ := task.Memos.Get("toString")
					if cVal != "a" || sVal != "own" {
						t.Fatalf("memos=%v", task.Memos)
					}
					if err := rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, writerRes], error) {
						draft, err := tx.Doc(progressDoc, task.Id)
						if err != nil {
							return nil, err
						}
						rawLines, _ := draft.Value.Get("lines")
						var lines []string
						if arr, ok := rawLines.([]any); ok {
							for _, el := range arr {
								lines = append(lines, fmt.Sprint(el))
							}
						}
						mu.Lock()
						progressSeen = lines
						mu.Unlock()
						return nil, nil
					}); err != nil {
						return err
					}
					return rt.Commit(ctx, func(tx durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, writerRes], error) {
						entry, err := tx.AppendEntry(current.ConversationId, durable.EntryDraft{
							Kind:  "answer",
							Model: ai.MessageList{testkit.User("done")},
						})
						if err != nil {
							return nil, err
						}
						next := testkit.Completed(current.Checkpoint, writerRes{EntryId: entry.Id})
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, writerRes, struct{}]) error {
				return nil
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{writer, child}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		conv, err := opened.Harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		idAny, err := conv.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(writer, nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		id := idAny.(durable.TaskId)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		receipt, err := opened.Harness.WaitForTask(nil, id)
		if err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		if receipt.State.Checkpoint != nil || receipt.Memos != nil {
			t.Fatalf("expected checkpoint & memos stripped from terminal receipt: %+v", receipt)
		}
		resRaw, ok := receipt.State.Outcome.Result.Get()
		var wr writerRes
		if !ok || receipt.State.Outcome.Status != durable.TaskOutcomeCompleted || jsonx.Decode(resRaw, &wr) != nil {
			t.Fatalf("unexpected outcome=%+v", receipt.State.Outcome)
		}
		var gotEntry *durable.EntryRecord
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			var err error
			gotEntry, err = tx.Entry(wr.EntryId)
			return err
		}); err != nil {
			t.Fatalf("Entry: %v", err)
		}
		if gotEntry == nil || !answerEntry.Is(gotEntry) || gotEntry.ConversationId != conv.Id() {
			t.Fatalf("entry=%+v conv=%d", gotEntry, conv.Id())
		}
		mu.Lock()
		gotProgress := slices.Clone(progressSeen)
		gotChildId := childId
		mu.Unlock()
		if !slices.Equal(gotProgress, []string{"wrote"}) {
			t.Fatalf("progressSeen=%v", gotProgress)
		}
		snap, err := opened.Harness.Snapshot(nil, progressDoc, id)
		if err != nil || snap != nil {
			t.Fatalf("expected retired doc snap=nil, got %v err=%v", snap, err)
		}
		gotRec, err := opened.Harness.GetTask(nil, id)
		if err != nil || gotRec == nil {
			t.Fatalf("GetTask: %v err=%v", gotRec, err)
		}
		recJSON, _ := jsonx.Stringify(gotRec)
		receiptJSON, _ := jsonx.Stringify(receipt)
		if recJSON != receiptJSON {
			t.Fatalf("GetTask %s != %s", recJSON, receiptJSON)
		}
		childRec, err := opened.Harness.WaitForTask(nil, gotChildId)
		if err != nil || childRec.ConversationId != conv.Id() || opened.Root.Id() == conv.Id() {
			t.Fatalf("child=%+v conv=%d root=%d err=%v", childRec, conv.Id(), opened.Root.Id(), err)
		}
	})

	t.Run("resumes a waiting task once every task in on is terminal, whatever the outcome", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		order := &thtrLog{}
		var mu sync.Mutex
		var on []durable.TaskId
		var outcomes []durable.TaskOutcomeStatus

		waiter := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, struct{}]{
			Name:    "test.waiter",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, struct{}]{
				"wait": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					order.Add("wait")
					mu.Lock()
					deps := slices.Clone(on)
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateWaiting,
							Checkpoint: thtkStep{Phase: "resume"},
							On:         deps,
							Policy:     durable.JoinPolicyAllSettled,
						}, nil
					})
				},
				"resume": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					order.Add("resume")
					mu.Lock()
					deps := slices.Clone(on)
					mu.Unlock()
					res, err := rt.Outcomes(ctx, deps)
					if err != nil {
						return err
					}
					var statuses []durable.TaskOutcomeStatus
					for _, o := range res {
						statuses = append(statuses, o.Status)
					}
					mu.Lock()
					outcomes = statuses
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
						return &next, nil
					})
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "test")
					return &next, nil
				})
			},
		})
		firstTask := thtkOneStep[any]("test.first", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			order.Add("first")
			_, _ = gate.Wait()
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		faultingTask := thtkOneStep[any]("test.faulting", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			order.Add("faulting")
			return errors.New("fails")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{firstTask, faultingTask, waiter}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		first := thtkStart(t, opened.Root, firstTask)
		faulting := thtkStart(t, opened.Root, faultingTask)
		mu.Lock()
		on = []durable.TaskId{first, faulting}
		mu.Unlock()
		wId := thtkStart(t, opened.Root, waiter)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return order.Len() == 3 })
		testkit.Flush()
		sortedOrder := order.Snapshot()
		slices.Sort(sortedOrder)
		if !slices.Equal(sortedOrder, []string{"faulting", "first", "wait"}) {
			t.Fatalf("order=%v", sortedOrder)
		}
		wRec, err := opened.Harness.GetTask(nil, wId)
		if err != nil || wRec == nil || wRec.State.Status != durable.TaskStateWaiting || !slices.Equal(wRec.State.On, []durable.TaskId{first, faulting}) {
			t.Fatalf("waiter rec=%+v err=%v", wRec, err)
		}
		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, wId); err != nil {
			t.Fatalf("WaitForTask waiter: %v", err)
		}
		finalOrder := order.Snapshot()
		mu.Lock()
		gotOutcomes := slices.Clone(outcomes)
		mu.Unlock()
		if len(finalOrder) == 0 || finalOrder[len(finalOrder)-1] != "resume" || !slices.Equal(gotOutcomes, []durable.TaskOutcomeStatus{durable.TaskOutcomeCompleted, durable.TaskOutcomeFaulted}) {
			t.Fatalf("order=%v outcomes=%v", finalOrder, gotOutcomes)
		}
	})

	t.Run("keeps one registry snapshot per phase and refreshes it at the phase boundary", func(t *testing.T) {
		type snapObs struct {
			phase string
			tools []string
			same  bool
		}
		var mu sync.Mutex
		var seen []snapObs
		phaseGate := testkit.NewDeferred[struct{}]()
		entered := testkit.NewDeferred[struct{}]()
		toolNames := func(snap durable.RegistrySnapshot) []string {
			var names []string
			for _, item := range snap.Tools() {
				names = append(names, item.Tool.Name)
			}
			return names
		}
		snapshots := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, struct{}]{
			Name:    "test.snapshots",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, struct{}]{
				"a": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					before := rt.Registry()
					entered.Resolve(struct{}{})
					_, _ = phaseGate.Wait()
					mu.Lock()
					seen = append(seen, snapObs{phase: "a", tools: toolNames(rt.Registry()), same: rt.Registry() == before})
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "b"},
						}, nil
					})
				},
				"b": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					mu.Lock()
					seen = append(seen, snapObs{phase: "b", tools: toolNames(rt.Registry()), same: true})
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				return nil
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{snapshots}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, snapshots)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = entered.Wait()
		if _, err := testkit.AddTool(opened.Registry, testkit.Tool("late", "late")); err != nil {
			t.Fatalf("AddTool late: %v", err)
		}
		phaseGate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		if len(gotSeen) != 2 || len(gotSeen[0].tools) != 0 || !gotSeen[0].same || !slices.Equal(gotSeen[1].tools, []string{"late"}) || !gotSeen[1].same {
			t.Fatalf("seen=%+v", gotSeen)
		}
	})

	t.Run("resolves the agent at first use in a phase from its snapshot, keeps it for the phase, and anew at the next", func(t *testing.T) {
		var mu sync.Mutex
		var pings []string
		type seenEntry struct {
			phase    string
			pings    []string
			thinking ai.ModelThinkingLevel
			same     bool
		}
		var seen []seenEntry
		beforeUse := testkit.NewDeferred[struct{}]()
		afterUse := testkit.NewDeferred[struct{}]()
		waitingBeforeUse := testkit.NewDeferred[struct{}]()
		waitingAfterUse := testkit.NewDeferred[struct{}]()

		runPings := func(rt *durable.TaskRuntime[any, thtkStep, any, thtkPingHooks]) []string {
			mu.Lock()
			pings = nil
			mu.Unlock()
			_ = rt.Hooks().Each(func(h thtkPingHooks) error {
				if h.Ping != nil {
					h.Ping()
				}
				return nil
			})
			mu.Lock()
			out := slices.Clone(pings)
			mu.Unlock()
			return out
		}
		phases := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, thtkPingHooks]{
			Name:    "test.agent-phases",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, thtkPingHooks]{
				"a": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, thtkPingHooks]) error {
					waitingBeforeUse.Resolve(struct{}{})
					_, _ = beforeUse.Wait()
					first, err := rt.Agent(ctx)
					if err != nil {
						return err
					}
					p1 := runPings(rt)
					mu.Lock()
					seen = append(seen, seenEntry{phase: "a", pings: p1, thinking: first.ThinkingLevel})
					mu.Unlock()
					waitingAfterUse.Resolve(struct{}{})
					_, _ = afterUse.Wait()
					second, err := rt.Agent(ctx)
					if err != nil {
						return err
					}
					p2 := runPings(rt)
					mu.Lock()
					seen = append(seen, seenEntry{phase: "a", pings: p2, thinking: second.ThinkingLevel, same: second == first})
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "b"},
						}, nil
					})
				},
				"b": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, thtkPingHooks]) error {
					agent, err := rt.Agent(ctx)
					if err != nil {
						return err
					}
					pb := runPings(rt)
					mu.Lock()
					seen = append(seen, seenEntry{phase: "b", pings: pb, thinking: agent.ThinkingLevel})
					mu.Unlock()
					return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, thtkPingHooks]) error {
				return nil
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{phases}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		if _, err := testkit.AddHooks(opened.Registry, phases, thtkPingHooks{
			Ping: func() {
				mu.Lock()
				pings = append(pings, "before")
				mu.Unlock()
			},
		}); err != nil {
			t.Fatalf("AddHooks before: %v", err)
		}
		id := thtkStart(t, opened.Root, phases)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = waitingBeforeUse.Wait()
		if err := opened.Root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)}, nil); err != nil {
			t.Fatalf("Configure low: %v", err)
		}
		beforeUse.Resolve(struct{}{})
		_, _ = waitingAfterUse.Wait()
		if _, err := testkit.AddHooks(opened.Registry, phases, thtkPingHooks{
			Ping: func() {
				mu.Lock()
				pings = append(pings, "late")
				mu.Unlock()
			},
		}); err != nil {
			t.Fatalf("AddHooks late: %v", err)
		}
		if err := opened.Root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, nil); err != nil {
			t.Fatalf("Configure high: %v", err)
		}
		afterUse.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		if len(gotSeen) != 3 ||
			gotSeen[0].phase != "a" || !slices.Equal(gotSeen[0].pings, []string{"before"}) || gotSeen[0].thinking != ai.ModelThinkingLevelLow ||
			gotSeen[1].phase != "a" || !slices.Equal(gotSeen[1].pings, []string{"before"}) || gotSeen[1].thinking != ai.ModelThinkingLevelLow || !gotSeen[1].same ||
			gotSeen[2].phase != "b" || !slices.Equal(gotSeen[2].pings, []string{"before", "late"}) || gotSeen[2].thinking != ai.ModelThinkingLevelHigh {
			t.Fatalf("seen=%+v", gotSeen)
		}
	})

	t.Run("rejects an agent() wait whose caller context is cancelled, without affecting the phase's resolution", func(t *testing.T) {
		var mu sync.Mutex
		var cancelled error
		var resolved ai.ModelThinkingLevel
		agentTask := thtkOneStep[any]("test.agent-cancel", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			sigCtx, cancel := context.WithCancelCause(context.Background())
			cancel(errors.New("caller gone"))
			caller := chord.WithAbortSignal(sigCtx, ctx)
			_, err := rt.Agent(caller)
			ag, err2 := rt.Agent(ctx)
			if err2 != nil {
				return err2
			}
			mu.Lock()
			cancelled = err
			resolved = ag.ThinkingLevel
			mu.Unlock()
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{agentTask}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, agentTask)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		mu.Lock()
		gotCancel := cancelled
		gotResolved := resolved
		mu.Unlock()
		if gotCancel == nil || !strings.Contains(gotCancel.Error(), "caller gone") || gotResolved != ai.ModelThinkingLevelOff {
			t.Fatalf("cancelled=%v resolved=%s", gotCancel, gotResolved)
		}
	})
}

type thtkBorrowStorage struct {
	durable.Storage
	fail *atomic.Bool
}

func (b *thtkBorrowStorage) Document(ctx chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if b.fail.Load() {
		return nil, errors.New("settings broke")
	}
	return b.Storage.Document(ctx, id, at)
}

func (b *thtkBorrowStorage) Close(_ chord.Context) error { return nil }

func TestHarnessTaskAgentResolutionFailureReopen(t *testing.T) {
	t.Run("observes a failed agent resolution whose only caller stopped waiting", func(t *testing.T) {
		underlying := storage.NewMemoryStorage()
		defer func() { _ = underlying.Close(nil) }()
		var fail atomic.Bool
		st := &thtkBorrowStorage{Storage: underlying, fail: &fail}

		var mu sync.Mutex
		var resolved error
		agentTask := thtkOneStep[any]("test.agent-failure", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			sigCtx, cancel := context.WithCancelCause(context.Background())
			cancel(errors.New("caller gone"))
			caller := chord.WithAbortSignal(sigCtx, ctx)
			fail.Store(true)
			_, _ = rt.Agent(caller)
			time.Sleep(5 * time.Millisecond)
			// Even after storage stops failing, the cached rejected resolution for this phase wins.
			fail.Store(false)
			_, err := rt.Agent(ctx)
			mu.Lock()
			resolved = err
			mu.Unlock()
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		h1 := thtkOpenRoot(t, []durable.AnyTask{agentTask}, st)
		id := thtkStart(t, h1.Root, agentTask)
		if err := h1.Harness.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h2, err := testkit.OpenTasks(st, []durable.AnyTask{agentTask})
		if err != nil {
			t.Fatalf("OpenTasks 2: %v", err)
		}
		defer func() { _ = h2.Harness.Close(nil) }()
		if err := h2.Harness.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		if _, err := h2.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		mu.Lock()
		gotErr := resolved
		mu.Unlock()
		if gotErr == nil || !strings.Contains(gotErr.Error(), "settings broke") {
			t.Fatalf("resolved=%v, want settings broke", gotErr)
		}
	})
}

func TestHarnessTaskRuntime(t *testing.T) {
	t.Run("rejects runtime operations after the invocation ends and stops its watches", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.task-notes",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatalf("DefineDoc notes: %v", err)
		}
		absentDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.task-absent",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatalf("DefineDoc absent: %v", err)
		}
		capturedD := testkit.NewDeferred[*durable.TaskRuntime[any, thtkStep, any, struct{}]]()
		handlerCtxD := testkit.NewDeferred[chord.Context]()
		watchClosedD := testkit.NewDeferred[durable.WatchEnd]()
		absentD := testkit.NewDeferred[durable.DocumentWatch]()
		delivered := &thtrLog{}

		watcher := thtkOneStep[any]("test.watcher", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			capturedD.Resolve(rt)
			handlerCtxD.Resolve(ctx)
			abs, err := rt.WatchDoc(ctx, absentDoc)
			if err != nil {
				return err
			}
			absentD.Resolve(abs)
			w, err := rt.WatchDoc(ctx, notesDoc)
			if err != nil {
				return err
			}
			if err := w.Start(func(val durable.JsonObject, _ []chord.Op, _ chord.Context) error {
				if val == nil {
					delivered.Add("retired")
				} else {
					txt, _ := val.Get("text")
					delivered.Add(fmt.Sprint(txt))
				}
				return nil
			}); err != nil {
				return err
			}
			go func() {
				watchClosedD.Resolve(w.Closed())
			}()
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{watcher}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			draft, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			draft.Value.Set("text", "hello")
			return nil
		}); err != nil {
			t.Fatalf("Commit notes: %v", err)
		}
		id := thtkStart(t, opened.Root, watcher)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		abs, _ := absentD.Wait()
		if abs != nil {
			t.Fatalf("expected absent watch nil, got %v", abs)
		}
		wEnd, _ := watchClosedD.Wait()
		if wEnd.Reason != durable.WatchEndStopped {
			t.Fatalf("watchEnd=%+v, want stopped", wEnd)
		}
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			draft, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			draft.Value.Set("text", "after")
			return nil
		}); err != nil {
			t.Fatalf("Commit after: %v", err)
		}
		testkit.Flush()
		if delivered.Len() != 0 {
			t.Fatalf("delivered=%v, want empty", delivered.Snapshot())
		}
		rt, _ := capturedD.Wait()
		hCtx, _ := handlerCtxD.Wait()
		expectEnded := func(name string, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "invocation has ended") {
				t.Fatalf("%s: err=%v, want invocation has ended", name, err)
			}
		}
		expectEnded("Commit", rt.Commit(nil, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
			return nil, nil
		}))
		_, _, err = rt.Memo(nil, "x")
		expectEnded("Memo", err)
		_, err = rt.MemoPut(nil, "x", 1)
		expectEnded("MemoPut", err)
		expectEnded("Sleep", rt.Sleep(nil, 0))
		_, err = rt.WatchDoc(nil, notesDoc)
		expectEnded("WatchDoc", err)
		_, err = rt.Agent(hCtx)
		expectEnded("Agent", err)

		func() {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(fmt.Sprint(r), "invocation has ended") {
					t.Fatalf("Now panic=%v, want invocation has ended", r)
				}
			}()
			_ = rt.Now()
		}()
		func() {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(fmt.Sprint(r), "invocation has ended") {
					t.Fatalf("Report panic=%v, want invocation has ended", r)
				}
			}()
			rt.Report(errors.New("late"))
		}()
	})

	t.Run("reads committed documents and context through the runtime, and forwards the clock and reports", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.runtime-notes",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		capturedD := testkit.NewDeferred[*durable.TaskRuntime[any, thtkStep, any, struct{}]]()
		var mu sync.Mutex
		var seen []any

		reader := thtkOneStep[any]("test.reader", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			capturedD.Resolve(rt)
			cvAll, err := rt.Context(ctx, rt.ConversationId())
			if err != nil || len(cvAll.Entries) < 2 {
				return fmt.Errorf("context entries=%v err=%v", cvAll, err)
			}
			firstId := cvAll.Entries[0].Id
			secondId := cvAll.Entries[1].Id
			snapNow, err := rt.Snapshot(ctx, notesDoc, rt.ConversationId())
			if err != nil {
				return err
			}
			snapAsOf, err := rt.SnapshotAsOf(ctx, notesDoc, rt.ConversationId(), firstId)
			if err != nil {
				return err
			}
			cvFirst, err := rt.Context(ctx, rt.ConversationId(), firstId)
			if err != nil {
				return err
			}
			cvSecond, err := rt.Context(ctx, rt.ConversationId(), secondId)
			if err != nil {
				return err
			}
			vNow, _ := snapNow.Get("text")
			vAsOf, _ := snapAsOf.Get("text")
			mu.Lock()
			seen = append(seen, vNow, vAsOf, len(cvFirst.Entries), len(cvSecond.Messages), rt.Now())
			mu.Unlock()
			rt.Report(errors.New("reported"))
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{reader}, nil, testkit.OpenTasksOptions{
			Now: func() int64 { return 1234 },
		})
		defer func() { _ = opened.Harness.Close(nil) }()
		for _, text := range []string{"one", "two"} {
			if _, err := opened.Root.Commit(nil, func(tx durable.Tx) (any, error) {
				draft, err := tx.Doc(notesDoc, opened.Root.Id())
				if err != nil {
					return nil, err
				}
				draft.Value.Set("text", text)
				return tx.AppendEntry(opened.Root.Id(), durable.EntryDraft{
					Kind:  "message",
					Model: ai.MessageList{testkit.User(text)},
				})
			}); err != nil {
				t.Fatalf("seed %s: %v", text, err)
			}
		}
		id := thtkStart(t, opened.Root, reader)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		if len(gotSeen) != 5 || gotSeen[0] != "two" || gotSeen[1] != "one" || gotSeen[2] != 1 || gotSeen[3] != 2 || gotSeen[4] != int64(1234) {
			t.Fatalf("seen=%v", gotSeen)
		}
		reports := opened.Reports()
		if len(reports) != 1 || reports[0].Error() != "reported" {
			t.Fatalf("reports=%v", reports)
		}
		testkit.Flush()
		rt, _ := capturedD.Wait()
		if _, err := rt.Snapshot(nil, notesDoc, opened.Root.Id()); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("late Snapshot=%v", err)
		}
		if _, err := rt.Context(nil, opened.Root.Id()); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("late Context=%v", err)
		}
	})

	t.Run("orders runtime commits against the step: one queued before it lands, one after it rejects", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		rtD := testkit.NewDeferred[*durable.TaskRuntime[any, thtkStep, string, struct{}]]()
		beforeCh := make(chan error, 1)
		harnessD := testkit.NewDeferred[durable.Harness]()

		detached := thtkOneStep[string]("test.detached", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, string, struct{}]) error {
			hRef, _ := harnessD.Wait()
			g := st.HoldCommits()
			heldD.Resolve(g)
			rtD.Resolve(rt)
			go func() {
				_ = hRef.Commit(nil, func(tx durable.Tx) error {
					_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
					return err
				})
			}()
			<-g.Entered
			go func() {
				beforeCh <- rt.Commit(nil, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, string], error) {
					next := testkit.Completed(current.Checkpoint, "before")
					return &next, nil
				})
			}()
			// Ensure `before` has entered the Session commit queue before returning (so before the step).
			time.Sleep(15 * time.Millisecond)
			return nil
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{detached}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		harnessD.Resolve(opened.Harness)
		id := thtkStart(t, opened.Root, detached)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		held, _ := heldD.Wait()
		<-held.Entered
		rt, _ := rtD.Wait()
		// Let Detached return and queue the scheduler's fault step behind `before`.
		time.Sleep(25 * time.Millisecond)
		afterCh := make(chan error, 1)
		go func() {
			afterCh <- rt.Commit(nil, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, string], error) {
				next := testkit.Completed(current.Checkpoint, "after")
				return &next, nil
			})
		}()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		if err := <-beforeCh; err != nil {
			t.Fatalf("before err=%v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		resVal, _ := settled.State.Outcome.Result.Get()
		if err != nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted || resVal != "before" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		afterErr := <-afterCh
		if afterErr == nil || (!strings.Contains(afterErr.Error(), "invocation has ended") && !strings.Contains(afterErr.Error(), "is terminal")) {
			t.Fatalf("afterErr=%v", afterErr)
		}
	})

	t.Run("stops a watch whose acquisition finishes after the invocation ended", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.late-watch",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		st := testkit.NewControlledStorage()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		rtD := testkit.NewDeferred[*durable.TaskRuntime[any, thtkStep, any, struct{}]]()
		harnessD := testkit.NewDeferred[durable.Harness]()

		late := thtkOneStep[any]("test.late-watch", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			hRef, _ := harnessD.Wait()
			g := st.HoldCommits()
			heldD.Resolve(g)
			rtD.Resolve(rt)
			go func() {
				_ = hRef.Commit(nil, func(tx durable.Tx) error {
					_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
					return err
				})
			}()
			<-g.Entered
			return nil
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{late}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		harnessD.Resolve(opened.Harness)
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			draft, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			draft.Value.Set("text", "x")
			return nil
		}); err != nil {
			t.Fatalf("seed notes: %v", err)
		}
		id := thtkStart(t, opened.Root, late)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		held, _ := heldD.Wait()
		<-held.Entered
		rt, _ := rtD.Wait()
		// Let `late` return so the fault step queues on the Session line behind `blocker`.
		time.Sleep(20 * time.Millisecond)
		watchErrCh := make(chan error, 1)
		go func() {
			_, err := rt.WatchDoc(nil, notesDoc)
			watchErrCh <- err
		}()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		watchErr := <-watchErrCh
		if watchErr == nil || !strings.Contains(watchErr.Error(), "invocation has ended") {
			t.Fatalf("watchErr=%v, want invocation has ended", watchErr)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
	})

	t.Run("sleeps until the Harness clock reaches the deadline, rechecking after each timer", func(t *testing.T) {
		var clock atomic.Int64
		clock.Store(1_000)
		woke := testkit.NewDeferred[struct{}]()
		sleeper := thtkOneStep[any]("test.sleeper", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			if err := rt.Sleep(ctx, 900); err != nil {
				return err
			}
			if err := rt.Sleep(ctx, 1_005); err != nil {
				return err
			}
			woke.Resolve(struct{}{})
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{sleeper}, nil, testkit.OpenTasksOptions{
			Now: func() int64 { return clock.Load() },
		})
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, sleeper)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
		if testkit.SettledChannel(woke.Done()) {
			t.Fatalf("expected sleeper not to wake before clock reaches deadline")
		}
		clock.Store(1_005)
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
	})

	t.Run("rejects a sleep when the invocation is signalled or the sleep's own context is cancelled", func(t *testing.T) {
		results := &thtrLog{}
		sleeping := testkit.NewDeferred[struct{}]()
		signalled := thtkOneStep[any]("test.sleep-signalled", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			sleeping.Resolve(struct{}{})
			err := rt.Sleep(ctx, time.Now().UnixMilli()+60_000)
			if err != nil {
				results.Add("signalled:" + err.Error())
				return err
			}
			return nil
		})
		cancelled := thtkOneStep[any]("test.sleep-cancelled", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			sigCtx, cancel := context.WithCancelCause(context.Background())
			errCh := make(chan error, 1)
			go func() {
				errCh <- rt.Sleep(chord.WithAbortSignal(sigCtx, ctx), time.Now().UnixMilli()+60_000)
			}()
			cancel(errors.New("stop sleeping"))
			if err := <-errCh; err != nil {
				results.Add("cancelled:" + err.Error())
			}
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{signalled, cancelled}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		sId := thtkStart(t, opened.Root, signalled)
		cId := thtkStart(t, opened.Root, cancelled)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, cId); err != nil {
			t.Fatalf("WaitForTask cancelled: %v", err)
		}
		_, _ = sleeping.Wait()
		if _, err := opened.Harness.AbortTask(nil, sId); err != nil {
			t.Fatalf("AbortTask signalled: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, sId)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "test" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		want := []string{"cancelled:stop sleeping", "signalled:Task abort requested"}
		if !slices.Equal(results.Snapshot(), want) {
			t.Fatalf("results=%v, want %v", results.Snapshot(), want)
		}
	})
}

func TestHarnessTaskScheduling(t *testing.T) {
	t.Run("retries reservation on the next wakeup after a rejected reservation commit", func(t *testing.T) {
		var runs atomic.Int32
		once := thtkOneStep[any]("test.once", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			runs.Add(1)
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{once}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, once)
		st.FailNextCommit(durable.NewStorageRejected("busy", nil))
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return len(opened.Reports()) == 1 })
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		if _, err := testkit.AddTool(opened.Registry, testkit.Tool("wake", "wake")); err != nil {
			t.Fatalf("AddTool wake: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		if runs.Load() != 1 {
			t.Fatalf("runs=%d, want 1", runs.Load())
		}
	})

	t.Run("keeps a wakeup that arrives while a rejected reservation commit is in storage", func(t *testing.T) {
		firstTask := thtkOneStep[any]("test.wake-first", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		lateTask := thtkOneStep[any]("test.wake-late", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{firstTask}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		first := thtkStart(t, opened.Root, firstTask)
		late := thtkStart(t, opened.Root, lateTask)
		held := st.HoldCommits()
		st.FailNextCommit(durable.NewStorageRejected("busy", nil))
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		<-held.Entered
		if _, err := testkit.AddTask(opened.Registry, lateTask); err != nil {
			t.Fatalf("AddTask late: %v", err)
		}
		held.Release()
		if _, err := opened.Harness.WaitForTask(nil, first); err != nil {
			t.Fatalf("WaitForTask first: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, late); err != nil {
			t.Fatalf("WaitForTask late: %v", err)
		}
	})

	t.Run("reruns a task whose fault write was rejected", func(t *testing.T) {
		var runs atomic.Int32
		st := testkit.NewControlledStorage()
		throws := thtkOneStep[any]("test.rejected-fault", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			if runs.Add(1) == 1 {
				st.FailNextCommit(durable.NewStorageRejected("busy", nil))
			}
			return errors.New("boom")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{throws}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, throws)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeFaulted || settled.State.Outcome.Error == nil || settled.State.Outcome.Error.Message != "boom" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if runs.Load() != 2 {
			t.Fatalf("runs=%d, want 2", runs.Load())
		}
		reports := opened.Reports()
		var sr *durable.StorageRejected
		if len(reports) != 1 || !errors.As(reports[0], &sr) || sr.Error() != "busy" {
			t.Fatalf("reports=%v, want [StorageRejected(busy)]", reports)
		}
	})

	t.Run("waits for Harness and conversation idleness, counting blocked work and ignoring background tasks", func(t *testing.T) {
		var mu sync.Mutex
		gates := make(map[durable.TaskId]*testkit.Deferred[struct{}])
		gated := thtkOneStep[any]("test.gated", func(ctx chord.Context, task durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			d := testkit.NewDeferred[struct{}]()
			mu.Lock()
			gates[task.Id] = d
			mu.Unlock()
			_, _ = d.Wait()
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{gated}, nil)
		if err := opened.Harness.WaitForIdle(nil); err != nil {
			t.Fatalf("initial WaitForIdle: %v", err)
		}
		other, err := opened.Harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		foreground := thtkStart(t, opened.Root, gated)
		background := thtkStart(t, opened.Root, gated, true)
		elsewhere := thtkStart(t, other, gated)

		cancelCtx, cancel := context.WithCancelCause(context.Background())
		cancelErrCh := make(chan error, 1)
		go func() {
			cancelErrCh <- opened.Harness.WaitForIdle(chord.WithAbortSignal(cancelCtx, nil))
		}()
		cancel(errors.New("stop waiting"))
		if err := <-cancelErrCh; err == nil || !strings.Contains(err.Error(), "stop waiting") {
			t.Fatalf("cancelledWait=%v", err)
		}
		abortedCtx, cancelAborted := context.WithCancelCause(context.Background())
		cancelAborted(errors.New("already cancelled"))
		if err := opened.Root.WaitForIdle(chord.WithAbortSignal(abortedCtx, nil)); err == nil || !strings.Contains(err.Error(), "already cancelled") {
			t.Fatalf("already cancelled wait=%v", err)
		}

		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(gates) == 3
		})
		rootIdleCh := make(chan error, 1)
		harnessIdleCh := make(chan error, 1)
		go func() { rootIdleCh <- opened.Root.WaitForIdle(nil) }()
		go func() { harnessIdleCh <- opened.Harness.WaitForIdle(nil) }()
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		fgGate := gates[foreground]
		ewGate := gates[elsewhere]
		bgGate := gates[background]
		mu.Unlock()

		fgGate.Resolve(struct{}{})
		if err := <-rootIdleCh; err != nil {
			t.Fatalf("rootIdle=%v", err)
		}
		select {
		case err := <-harnessIdleCh:
			t.Fatalf("expected harnessIdle not yet settled, got %v", err)
		case <-time.After(15 * time.Millisecond):
		}
		ewGate.Resolve(struct{}{})
		if err := <-harnessIdleCh; err != nil {
			t.Fatalf("harnessIdle=%v", err)
		}
		bgRec, err := opened.Harness.GetTask(nil, background)
		if err != nil || bgRec == nil || bgRec.State.Status != durable.TaskStateRunning {
			t.Fatalf("bgRec=%+v err=%v", bgRec, err)
		}
		bgGate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, background); err != nil {
			t.Fatalf("WaitForTask bg: %v", err)
		}
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := opened.Harness.WaitForIdle(nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
			t.Fatalf("closed WaitForIdle=%v", err)
		}
		if err := opened.Root.WaitForIdle(nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
			t.Fatalf("closed root WaitForIdle=%v", err)
		}
	})

	t.Run("rejects unknown tasks and reports terminal tasks", func(t *testing.T) {
		done := thtkOneStep[any]("test.quick", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{done}, nil)
		unknown := durable.TaskId(999_999)
		rec, err := opened.Harness.GetTask(nil, unknown)
		if err != nil || rec != nil {
			t.Fatalf("GetTask unknown=(%v, %v)", rec, err)
		}
		if _, err := opened.Harness.WaitForTask(nil, unknown); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("WaitForTask unknown=%v", err)
		}
		if _, err := opened.Harness.AbortTask(nil, unknown); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("AbortTask unknown=%v", err)
		}
		id := thtkStart(t, opened.Root, done)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Status != durable.TaskStateTerminal {
			t.Fatalf("WaitForTask 1=(%+v, %v)", settled, err)
		}
		settled2, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled2.State.Status != durable.TaskStateTerminal {
			t.Fatalf("WaitForTask 2=(%+v, %v)", settled2, err)
		}
		abortRes, err := opened.Harness.AbortTask(nil, id)
		if err != nil || abortRes != durable.TaskAbortTerminal {
			t.Fatalf("AbortTask terminal=(%s, %v)", abortRes, err)
		}
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := opened.Harness.Resume(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
			t.Fatalf("Resume after close=%v", err)
		}
	})

	t.Run("rejects task waits cancelled or closed while queued on the line, and pending waits on close", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		blocking := thtkGated("test.wait-close", gate.Done())
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{blocking}, st)
		id := thtkStart(t, opened.Root, blocking)

		held := st.HoldCommits()
		blockerCh := make(chan error, 1)
		go func() {
			blockerCh <- opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(opened.Root.Id(), durable.EntryDraft{Kind: "blocker"})
				return err
			})
		}()
		<-held.Entered
		sigCtx, cancel := context.WithCancelCause(context.Background())
		cancelledCh := make(chan error, 1)
		go func() {
			_, err := opened.Harness.WaitForTask(chord.WithAbortSignal(sigCtx, nil), id)
			cancelledCh <- err
		}()
		time.Sleep(15 * time.Millisecond)
		cancel(errors.New("wait cancelled"))
		held.Release()
		if err := <-blockerCh; err != nil {
			t.Fatalf("blocker=%v", err)
		}
		if err := <-cancelledCh; err == nil || !strings.Contains(err.Error(), "wait cancelled") {
			t.Fatalf("cancelledWhileQueued=%v", err)
		}

		pendingCh := make(chan error, 1)
		idleCh := make(chan error, 1)
		convIdleCh := make(chan error, 1)
		go func() {
			_, err := opened.Harness.WaitForTask(nil, id)
			pendingCh <- err
		}()
		go func() { idleCh <- opened.Harness.WaitForIdle(nil) }()
		go func() { convIdleCh <- opened.Root.WaitForIdle(nil) }()
		time.Sleep(15 * time.Millisecond)

		heldAgain := st.HoldCommits()
		blockerAgainCh := make(chan error, 1)
		go func() {
			blockerAgainCh <- opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(opened.Root.Id(), durable.EntryDraft{Kind: "blocker"})
				return err
			})
		}()
		<-heldAgain.Entered
		closedQueuedCh := make(chan error, 1)
		go func() {
			_, err := opened.Harness.WaitForTask(nil, id)
			closedQueuedCh <- err
		}()
		time.Sleep(15 * time.Millisecond)
		closingCh := make(chan error, 1)
		go func() { closingCh <- opened.Harness.Close(nil) }()
		time.Sleep(15 * time.Millisecond)
		heldAgain.Release()
		if err := <-blockerAgainCh; err != nil {
			t.Fatalf("blockerAgain=%v", err)
		}
		for name, ch := range map[string]chan error{
			"closedWhileQueued": closedQueuedCh,
			"pending":           pendingCh,
			"idle":              idleCh,
			"conversationIdle":  convIdleCh,
		} {
			if err := <-ch; err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
				t.Fatalf("%s=%v, want closed", name, err)
			}
		}
		gate.Resolve(struct{}{})
		if err := <-closingCh; err != nil {
			t.Fatalf("closing=%v", err)
		}
	})
}

func TestHarnessTaskAbort(t *testing.T) {
	t.Run("rejects the run's commits and memo writes after the mark, and settles through a fresh abort invocation", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		lateErrors := &thtrLog{}
		var mu sync.Mutex
		var memoRead durable.JsonValue
		var abortRuntimes []*durable.TaskRuntime[any, thtkStep, any, struct{}]
		var runRuntime *durable.TaskRuntime[any, thtkStep, any, struct{}]

		marked := thtkOneStep[any](
			"test.marked",
			func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				mu.Lock()
				runRuntime = rt
				mu.Unlock()
				if _, err := rt.MemoPut(ctx, "kept", float64(1)); err != nil {
					return err
				}
				reached.Resolve(struct{}{})
				_ = testkit.Aborted(rt.Signal())
				v, _, err := rt.Memo(nil, "kept")
				if err != nil {
					return err
				}
				mu.Lock()
				memoRead = v
				mu.Unlock()
				if _, err := rt.MemoPut(nil, "late", 2); err != nil {
					lateErrors.Add(err.Error())
				}
				if err := rt.Commit(nil, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
					return &next, nil
				}); err != nil {
					lateErrors.Add(err.Error())
				}
				return nil
			},
			func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				mu.Lock()
				abortRuntimes = append(abortRuntimes, rt)
				mu.Unlock()
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					kept, _ := current.Memos.Get("kept")
					if !current.AbortRequested || fmt.Sprint(kept) != "1" {
						return nil, fmt.Errorf("unexpected current=%+v", current)
					}
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "mark")
					return &next, nil
				})
			},
		)
		opened := thtkOpenRoot(t, []durable.AnyTask{marked}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, marked)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%s err=%v", res, err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "mark" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		mu.Lock()
		gotMemo := memoRead
		gotRun := runRuntime
		gotAborts := slices.Clone(abortRuntimes)
		mu.Unlock()
		wantMsg := fmt.Sprintf("Task %d has a durable abort mark", id)
		if fmt.Sprint(gotMemo) != "1" || !slices.Equal(lateErrors.Snapshot(), []string{wantMsg, wantMsg}) || len(gotAborts) != 1 || gotAborts[0] == gotRun {
			t.Fatalf("memo=%v errors=%v aborts=%d same=%v", gotMemo, lateErrors.Snapshot(), len(gotAborts), len(gotAborts) == 1 && gotAborts[0] == gotRun)
		}
	})

	t.Run("starts no further phase after a mark that lands during a phase with progress", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		proceed := testkit.NewDeferred[struct{}]()
		phases := &thtrLog{}
		two := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, struct{}]{
			Name:    "test.mark-boundary",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, struct{}]{
				"one": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("one")
					if err := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "two"},
						}, nil
					}); err != nil {
						return err
					}
					reached.Resolve(struct{}{})
					_, _ = proceed.Wait()
					return nil
				},
				"two": func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("two")
					return nil
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				phases.Add("abort")
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "boundary")
					return &next, nil
				})
			},
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{two}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, two)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		abortCh := thtkMarkDurably(t, opened.Harness, id)
		proceed.Resolve(struct{}{})
		ar := <-abortCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("abort=%+v", ar)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "boundary" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if !slices.Equal(phases.Snapshot(), []string{"one", "abort"}) {
			t.Fatalf("phases=%v", phases.Snapshot())
		}
	})

	t.Run("signals and joins the run before returning, then runs the abort handler", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		abortGate := testkit.NewDeferred[struct{}]()
		defer abortGate.Resolve(struct{}{})
		var runEnded atomic.Bool
		var abortRanAfterRun atomic.Bool
		signalled := thtkOneStep[any]("test.signalled",
			func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				reached.Resolve(struct{}{})
				defer runEnded.Store(true)
				_ = testkit.Aborted(rt.Signal())
				return nil
			},
			func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				abortRanAfterRun.Store(runEnded.Load())
				_, _ = abortGate.Wait()
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "test")
					return &next, nil
				})
			},
		)
		opened := thtkOpenRoot(t, []durable.AnyTask{signalled}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, signalled)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked || !runEnded.Load() {
			t.Fatalf("AbortTask=%s err=%v runEnded=%v", res, err, runEnded.Load())
		}
		res2, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res2 != durable.TaskAbortMarked {
			t.Fatalf("AbortTask 2=%s err=%v", res2, err)
		}
		abortGate.Resolve(struct{}{})
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || !abortRanAfterRun.Load() || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "test" {
			t.Fatalf("settled=%+v abortRanAfterRun=%v err=%v", settled, abortRanAfterRun.Load(), err)
		}
	})

	t.Run("aborts waiting work before its wait ends and faults abort handlers that throw or settle nothing", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		firstTask := thtkGated("test.dependency", gate.Done())
		var mu sync.Mutex
		var firstId durable.TaskId
		waitHandler := func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			mu.Lock()
			dep := firstId
			mu.Unlock()
			return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
				return &durable.NextTaskState[thtkStep, any]{
					Status:     durable.TaskStateWaiting,
					Checkpoint: thtkStep{Phase: "run"},
					On:         []durable.TaskId{dep},
					Policy:     durable.JoinPolicyAllSettled,
				}, nil
			})
		}
		lazy := thtkOneStep[any]("test.lazy-abort", waitHandler, func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return nil
		})
		throwing := thtkOneStep[any]("test.throwing-abort", waitHandler, func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			return errors.New("abort failed")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{firstTask, lazy, throwing}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		fId := thtkStart(t, opened.Root, firstTask)
		mu.Lock()
		firstId = fId
		mu.Unlock()
		lId := thtkStart(t, opened.Root, lazy)
		tId := thtkStart(t, opened.Root, throwing)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool {
			r1, e1 := opened.Harness.GetTask(nil, tId)
			r2, e2 := opened.Harness.GetTask(nil, lId)
			return e1 == nil && e2 == nil && r1 != nil && r2 != nil && r1.State.Status == durable.TaskStateWaiting && r2.State.Status == durable.TaskStateWaiting
		})
		if _, err := opened.Harness.AbortTask(nil, lId); err != nil {
			t.Fatalf("AbortTask lazy: %v", err)
		}
		if _, err := opened.Harness.AbortTask(nil, tId); err != nil {
			t.Fatalf("AbortTask throwing: %v", err)
		}
		lazySettled, err := opened.Harness.WaitForTask(nil, lId)
		wantLazy := fmt.Sprintf("Abort handler of task %d returned without a terminal outcome", lId)
		if err != nil || lazySettled.State.Outcome == nil || lazySettled.State.Outcome.Status != durable.TaskOutcomeFaulted || lazySettled.State.Outcome.Error == nil || lazySettled.State.Outcome.Error.Message != wantLazy {
			t.Fatalf("lazy=%+v err=%v", lazySettled, err)
		}
		throwSettled, err := opened.Harness.WaitForTask(nil, tId)
		if err != nil || throwSettled.State.Outcome == nil || throwSettled.State.Outcome.Status != durable.TaskOutcomeFaulted || throwSettled.State.Outcome.Error == nil || throwSettled.State.Outcome.Error.Message != "abort failed" {
			t.Fatalf("throwing=%+v err=%v", throwSettled, err)
		}
		fRec, err := opened.Harness.GetTask(nil, fId)
		if err != nil || fRec == nil || fRec.State.Status != durable.TaskStateRunning {
			t.Fatalf("first rec=%+v err=%v", fRec, err)
		}
		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, fId); err != nil {
			t.Fatalf("WaitForTask first: %v", err)
		}
	})

	t.Run("does not signal a running abort handler when aborted again, and a cancelled caller leaves the mark durable", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		proceed := testkit.NewDeferred[struct{}]()
		runRelease := testkit.NewDeferred[struct{}]()
		var abortSignalled atomic.Bool
		var aborts atomic.Int32

		run := thtkOneStep[any](
			"test.abort-again",
			func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				_, _ = runRelease.Wait()
				return nil
			},
			func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				aborts.Add(1)
				reached.Resolve(struct{}{})
				_, _ = proceed.Wait()
				abortSignalled.Store(rt.Signal().Err() != nil)
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "once")
					return &next, nil
				})
			},
		)
		opened := thtkOpenRoot(t, []durable.AnyTask{run}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, run)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		testkit.Flush()
		sigCtx, cancel := context.WithCancelCause(context.Background())
		cancelErrCh := make(chan error, 1)
		go func() {
			_, err := opened.Harness.AbortTask(chord.WithAbortSignal(sigCtx, nil), id)
			cancelErrCh <- err
		}()
		thtrEventually(t, func() bool {
			rec, err := opened.Harness.GetTask(nil, id)
			return err == nil && rec != nil && rec.AbortRequested
		})
		cancel(errors.New("caller gave up"))
		if err := <-cancelErrCh; err == nil || !strings.Contains(err.Error(), "caller gave up") {
			t.Fatalf("cancelled AbortTask=%v", err)
		}
		runRelease.Resolve(struct{}{})
		_, _ = reached.Wait()
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%s err=%v", res, err)
		}
		proceed.Resolve(struct{}{})
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "once" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if abortSignalled.Load() || aborts.Load() != 1 {
			t.Fatalf("abortSignalled=%v aborts=%d", abortSignalled.Load(), aborts.Load())
		}
	})

	t.Run("keeps a terminal outcome committed by an abort handler that throws afterwards", func(t *testing.T) {
		run := thtkOneStep[any](
			"test.abort-then-throw",
			func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				_ = testkit.Aborted(rt.Signal())
				return nil
			},
			func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				if err := rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.AbortedWith[thtkStep, any](current.Checkpoint, "done")
					return &next, nil
				}); err != nil {
					return err
				}
				return errors.New("after terminal")
			},
		)
		opened := thtkOpenRoot(t, []durable.AnyTask{run}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, run)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		testkit.Flush()
		if _, err := opened.Harness.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "done" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
	})

	t.Run("never starts phase one when the abort lands while the reservation settles", func(t *testing.T) {
		var ran atomic.Bool
		reserved := thtkOneStep[any]("test.reserved", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			ran.Store(true)
			_ = testkit.Aborted(rt.Signal())
			return nil
		})
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{reserved}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtkStart(t, opened.Root, reserved)
		held := st.HoldCommits()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		<-held.Entered
		type abortRes struct {
			res durable.TaskAbortResult
			err error
		}
		abortCh := make(chan abortRes, 1)
		go func() {
			r, e := opened.Harness.AbortTask(nil, id)
			abortCh <- abortRes{r, e}
		}()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		ar := <-abortCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%+v", ar)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "test" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if ran.Load() {
			t.Fatalf("expected phase one not to start")
		}
	})

	t.Run("lets an abort mark win over a fault that races it", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		markingCh := make(chan struct {
			res durable.TaskAbortResult
			err error
		}, 1)
		heldD := testkit.NewDeferred[*testkit.Gate]()
		harnessD := testkit.NewDeferred[durable.Harness]()

		racing := thtkOneStep[any]("test.racing-fault", func(_ chord.Context, task durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			hRef, _ := harnessD.Wait()
			g := st.HoldCommits()
			heldD.Resolve(g)
			go func() {
				r, e := hRef.AbortTask(nil, task.Id)
				markingCh <- struct {
					res durable.TaskAbortResult
					err error
				}{r, e}
			}()
			<-g.Entered
			return errors.New("would fault")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{racing}, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		harnessD.Resolve(opened.Harness)
		id := thtkStart(t, opened.Root, racing)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		held, _ := heldD.Wait()
		<-held.Entered
		// Wait for racing handler to return error and queue the fault step behind AbortTask.
		time.Sleep(20 * time.Millisecond)
		held.Release()
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "test" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		ar := <-markingCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("marking=%+v", ar)
		}
	})
}

func thtkExtractTaskWrites(batch []durable.StorageWrite, id *durable.TaskId) []durable.TaskRecord {
	var out []durable.TaskRecord
	for _, w := range batch {
		var rec *durable.TaskRecord
		switch tw := w.(type) {
		case durable.StorageWriteTask:
			rec = &tw.Value
		case *durable.StorageWriteTask:
			rec = &tw.Value
		}
		if rec != nil && (id == nil || rec.Id == *id) {
			out = append(out, *rec)
		}
	}
	return out
}

func TestHarnessTaskClose(t *testing.T) {
	t.Run("stops without outcomes and starts no fresh phase or abort invocation while closing", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		proceed := testkit.NewDeferred[struct{}]()
		phases := &thtrLog{}
		var abortRan atomic.Bool

		two := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, struct{}]{
			Name:    "test.two",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, struct{}]{
				"one": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("one")
					if err := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "two"},
						}, nil
					}); err != nil {
						return err
					}
					reached.Resolve(struct{}{})
					_, _ = proceed.Wait()
					return nil
				},
				"two": func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("two")
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				abortRan.Store(true)
				return nil
			},
		})
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{two}, st)
		id := thtkStart(t, opened.Root, two)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		abortCh := thtkMarkDurably(t, opened.Harness, id)
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		commitsBefore := len(st.Commits())

		waitIdle := make(chan error, 1)
		go func() { waitIdle <- opened.Harness.WaitForIdle(nil) }()
		closeCh := make(chan error, 1)
		go func() { closeCh <- opened.Harness.Close(nil) }()
		<-waitIdle
		proceed.Resolve(struct{}{})
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		ar := <-abortCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("abort=%+v", ar)
		}
		if len(st.Commits()) != commitsBefore || !slices.Equal(phases.Snapshot(), []string{"one"}) || abortRan.Load() {
			t.Fatalf("commits=%d/%d phases=%v abortRan=%v", len(st.Commits()), commitsBefore, phases.Snapshot(), abortRan.Load())
		}
		var cp thtkStep
		if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil || !rec.AbortRequested || rec.State.Status != durable.TaskStateRunning || cp.Phase != "two" {
			t.Fatalf("rec=%+v cp=%+v err=%v", rec, cp, err)
		}
		if err := opened.Harness.Commit(nil, func(_ durable.Tx) error { return nil }); err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
			t.Fatalf("commit after close=%v", err)
		}
	})

	t.Run("seals admission before signalling handlers and stops watches before joining them", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.close-notes",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		reached := testkit.NewDeferred[struct{}]()
		fromListenerD := testkit.NewDeferred[error]()
		harnessD := testkit.NewDeferred[durable.Harness]()
		watchEndD := testkit.NewDeferred[durable.WatchEnd]()

		stubborn := thtkOneStep[any]("test.stubborn", func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			hRef, _ := harnessD.Wait()
			w, err := rt.WatchDoc(ctx, notesDoc)
			if err != nil {
				return err
			}
			stopAfter := context.AfterFunc(rt.Signal(), func() {
				fromListenerD.Resolve(hRef.Commit(nil, func(_ durable.Tx) error { return nil }))
			})
			defer stopAfter()
			reached.Resolve(struct{}{})
			watchEndD.Resolve(w.Closed())
			return nil
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{stubborn}, nil)
		harnessD.Resolve(opened.Harness)
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			draft, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			draft.Value.Set("text", "x")
			return nil
		}); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		_ = thtkStart(t, opened.Root, stubborn)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("Close: %v", err)
		}
		fromErr, _ := fromListenerD.Wait()
		if fromErr == nil || !strings.Contains(strings.ToLower(fromErr.Error()), "closed") {
			t.Fatalf("fromListener=%v, want closed", fromErr)
		}
		wEnd, _ := watchEndD.Wait()
		if wEnd.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("watchEnd=%+v, want session_closed", wEnd)
		}
	})

	t.Run("writes no fault when close seals while the step after a failed phase is queued", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		harnessD := testkit.NewDeferred[durable.Harness]()

		throws := thtkOneStep[any]("test.close-fault", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			hRef, _ := harnessD.Wait()
			g := st.HoldCommits()
			heldD.Resolve(g)
			go func() {
				_ = hRef.Commit(nil, func(tx durable.Tx) error {
					_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
					return err
				})
			}()
			<-g.Entered
			return errors.New("would fault")
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{throws}, st)
		harnessD.Resolve(opened.Harness)
		id := thtkStart(t, opened.Root, throws)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		held, _ := heldD.Wait()
		<-held.Entered
		time.Sleep(20 * time.Millisecond)
		closeCh := make(chan error, 1)
		go func() { closeCh <- opened.Harness.Close(nil) }()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		var statuses []durable.TaskStateStatus
		for _, batch := range st.Commits() {
			for _, rec := range thtkExtractTaskWrites(batch, &id) {
				statuses = append(statuses, rec.State.Status)
			}
		}
		if !slices.Equal(statuses, []durable.TaskStateStatus{durable.TaskStatePending, durable.TaskStateRunning}) {
			t.Fatalf("statuses=%v", statuses)
		}
	})

	t.Run("starts no abort handler whose reservation settles while closing", func(t *testing.T) {
		var ran atomic.Bool
		marked := thtkOneStep[any](
			"test.close-abort-reservation",
			func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				return nil
			},
			func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				ran.Store(true)
				return nil
			},
		)
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{marked}, st)
		id := thtkStart(t, opened.Root, marked)
		if _, err := opened.Harness.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		held := st.HoldCommits()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		<-held.Entered
		closeCh := make(chan error, 1)
		go func() { closeCh <- opened.Harness.Close(nil) }()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		if ran.Load() {
			t.Fatalf("expected abort handler not to run")
		}
		commits := st.Commits()
		lastWrites := thtkExtractTaskWrites(commits[len(commits)-1], nil)
		if len(lastWrites) != 1 || lastWrites[0].Id != id || !lastWrites[0].AbortRequested || lastWrites[0].State.Status != durable.TaskStateRunning {
			t.Fatalf("lastWrites=%+v", lastWrites)
		}
	})

	t.Run("rejects a runtime commit that was queued on the line when close sealed it", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		queuedD := testkit.NewDeferred[error]()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		harnessD := testkit.NewDeferred[durable.Harness]()
		proceed := testkit.NewDeferred[struct{}]()

		queued := thtkOneStep[any]("test.close-queued-commit", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			hRef, _ := harnessD.Wait()
			g := st.HoldCommits()
			heldD.Resolve(g)
			go func() {
				_ = hRef.Commit(nil, func(tx durable.Tx) error {
					_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
					return err
				})
			}()
			<-g.Entered
			go func() {
				queuedD.Resolve(rt.Commit(nil, func(_ durable.Tx, current durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
					next := testkit.Completed[thtkStep, any](current.Checkpoint, nil)
					return &next, nil
				}))
			}()
			_, _ = proceed.Wait()
			return nil
		})
		opened := thtkOpenRoot(t, []durable.AnyTask{queued}, st)
		harnessD.Resolve(opened.Harness)
		id := thtkStart(t, opened.Root, queued)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		held, _ := heldD.Wait()
		<-held.Entered
		time.Sleep(20 * time.Millisecond)
		closeCh := make(chan error, 1)
		go func() { closeCh <- opened.Harness.Close(nil) }()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		queuedErr, _ := queuedD.Wait()
		if queuedErr == nil || !strings.Contains(queuedErr.Error(), "Harness is closed") {
			t.Fatalf("queuedErr=%v, want Harness is closed", queuedErr)
		}
		proceed.Resolve(struct{}{})
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		var taskWriteCount int
		for _, batch := range st.Commits() {
			taskWriteCount += len(thtkExtractTaskWrites(batch, &id))
		}
		if taskWriteCount != 2 {
			t.Fatalf("taskWriteCount=%d, want 2", taskWriteCount)
		}
	})

	t.Run("starts no next phase when close seals during a step that decided to continue", func(t *testing.T) {
		phases := &thtrLog{}
		registry := durable.CreateRegistry()
		reader := &thtkClosingReader{
			registry: registry,
			closing:  testkit.NewDeferred[chan error](),
		}
		two := durable.DefineTask(durable.TaskDefinition[any, thtkStep, any, struct{}]{
			Name:    "test.close-in-step",
			Version: 1,
			Initial: func(_ any) thtkStep { return thtkStep{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[any, thtkStep, any, struct{}]{
				"one": func(ctx chord.Context, _ durable.RunningTask[any, thtkStep], rt *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("one")
					if err := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtkStep]) (*durable.NextTaskState[thtkStep, any], error) {
						return &durable.NextTaskState[thtkStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtkStep{Phase: "two"},
						}, nil
					}); err != nil {
						return err
					}
					reader.mu.Lock()
					reader.arm = true
					reader.mu.Unlock()
					return nil
				},
				"two": func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
					phases.Add("two")
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
				return nil
			},
		})
		if _, err := testkit.AddTask(registry, two); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		h, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
			Models:   ai.CreateModels(nil),
			Registry: reader,
		}, nil)
		if err != nil {
			t.Fatalf("HarnessOpen: %v", err)
		}
		reader.mu.Lock()
		reader.harness = h
		reader.mu.Unlock()
		root, err := h.Root(nil)
		if err != nil {
			t.Fatalf("Root: %v", err)
		}
		_ = thtkStart(t, root, two)
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		closeCh, _ := reader.closing.Wait()
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		if !slices.Equal(phases.Snapshot(), []string{"one"}) {
			t.Fatalf("phases=%v, want [one]", phases.Snapshot())
		}
	})

	t.Run("joins a reservation that settles while closing without starting its handler", func(t *testing.T) {
		var ran atomic.Bool
		never := thtkOneStep[any]("test.close-reservation", func(_ chord.Context, _ durable.RunningTask[any, thtkStep], _ *durable.TaskRuntime[any, thtkStep, any, struct{}]) error {
			ran.Store(true)
			return nil
		})
		st := testkit.NewControlledStorage()
		opened := thtkOpenRoot(t, []durable.AnyTask{never}, st)
		id := thtkStart(t, opened.Root, never)
		held := st.HoldCommits()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		<-held.Entered
		closeCh := make(chan error, 1)
		go func() { closeCh <- opened.Harness.Close(nil) }()
		time.Sleep(15 * time.Millisecond)
		held.Release()
		if err := <-closeCh; err != nil {
			t.Fatalf("Close: %v", err)
		}
		if ran.Load() {
			t.Fatalf("expected handler not to run")
		}
		commits := st.Commits()
		writes := thtkExtractTaskWrites(commits[len(commits)-1], nil)
		if len(writes) != 1 || writes[0].Id != id || writes[0].State.Status != durable.TaskStateRunning {
			t.Fatalf("writes=%+v", writes)
		}
	})
}
