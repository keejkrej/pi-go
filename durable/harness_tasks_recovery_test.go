// Ported from packages/durable/test/harness-tasks-recovery.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"fmt"
	"path/filepath"
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
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func thtrSqlitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.sqlite")
}

func thtrCreateIn(t *testing.T, h durable.Harness, task durable.AnyTask, input any) durable.TaskId {
	t.Helper()
	root, err := h.Root(nil)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	idAny, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(task, input, durable.TaskOptions{
			Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
		})
	})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", task.TaskKind(), err)
	}
	return idAny.(durable.TaskId)
}

type thtrLog struct {
	mu    sync.Mutex
	items []string
}

func (l *thtrLog) Add(s string) {
	l.mu.Lock()
	l.items = append(l.items, s)
	l.mu.Unlock()
}

func (l *thtrLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

func (l *thtrLog) Contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.items, s)
}

func (l *thtrLog) Snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.items)
}

// thtrTransferService models an external service whose operations are idempotent by request key.
type thtrTransferService struct {
	mu      sync.Mutex
	applied map[string]int
	calls   int
}

func thtrNewTransferService() *thtrTransferService {
	return &thtrTransferService{
		applied: make(map[string]int),
	}
}

func (s *thtrTransferService) Apply(key string, amount int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if _, ok := s.applied[key]; !ok {
		s.applied[key] = amount * 10
	}
	return s.applied[key]
}

func (s *thtrTransferService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *thtrTransferService) AppliedSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.applied)
}

type thtrTransferInput struct {
	Amount int `json:"amount"`
}

type thtrTransferCheckpoint struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitzero"`
}

type thtrTransferResult struct {
	Receipt int `json:"receipt"`
}

func thtrTransferTask(service *thtrTransferService, interruptFirst *atomic.Bool) *durable.Task[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}] {
	return durable.DefineTask(durable.TaskDefinition[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}]{
		Name:    "test.transfer",
		Version: 1,
		Initial: func(_ thtrTransferInput) thtrTransferCheckpoint {
			return thtrTransferCheckpoint{Phase: "prepare"}
		},
		Phases: map[string]durable.PhaseHandler[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}]{
			"prepare": func(ctx chord.Context, task durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint], rt *durable.TaskRuntime[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}]) error {
				if _, err := rt.MemoPut(ctx, "requested", float64(task.Input.Amount)); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint]) (*durable.NextTaskState[thtrTransferCheckpoint, thtrTransferResult], error) {
					return &durable.NextTaskState[thtrTransferCheckpoint, thtrTransferResult]{
						Status:     durable.TaskStateRunning,
						Checkpoint: thtrTransferCheckpoint{Phase: "apply", Key: fmt.Sprintf("transfer-%d", task.Id)},
					}, nil
				})
			},
			"apply": func(ctx chord.Context, task durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint], rt *durable.TaskRuntime[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}]) error {
				receipt := service.Apply(task.Checkpoint.Key, task.Input.Amount)
				if interruptFirst.CompareAndSwap(true, false) {
					_ = testkit.Aborted(rt.Signal())
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint]) (*durable.NextTaskState[thtrTransferCheckpoint, thtrTransferResult], error) {
					next := testkit.Completed(task.Checkpoint, thtrTransferResult{Receipt: receipt})
					return &next, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint], rt *durable.TaskRuntime[thtrTransferInput, thtrTransferCheckpoint, thtrTransferResult, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[thtrTransferInput, thtrTransferCheckpoint]) (*durable.NextTaskState[thtrTransferCheckpoint, thtrTransferResult], error) {
				return &durable.NextTaskState[thtrTransferCheckpoint, thtrTransferResult]{
					Status:     durable.TaskStateTerminal,
					Checkpoint: current.Checkpoint,
					Outcome:    &durable.TypedTaskOutcome[thtrTransferResult]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	})
}

type thtrStep struct {
	Phase string `json:"phase"`
}

func thtrVersioned(
	version int,
	result string,
	migrate ...func(durable.JsonValue, durable.JsonValue, int) (any, thtrStep, error),
) *durable.Task[any, thtrStep, durable.JsonValue, struct{}] {
	def := durable.TaskDefinition[any, thtrStep, durable.JsonValue, struct{}]{
		Name:    "test.versioned",
		Version: version,
		Initial: func(_ any) thtrStep {
			return thtrStep{Phase: "run"}
		},
		Phases: map[string]durable.PhaseHandler[any, thtrStep, durable.JsonValue, struct{}]{
			"run": func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, durable.JsonValue, struct{}]) error {
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, durable.JsonValue], error) {
					next := testkit.Completed[thtrStep, durable.JsonValue](current.Checkpoint, result)
					return &next, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, durable.JsonValue, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, durable.JsonValue], error) {
				next := testkit.AbortedWith[thtrStep, durable.JsonValue](current.Checkpoint, result)
				return &next, nil
			})
		},
	}
	if len(migrate) > 0 && migrate[0] != nil {
		def.Migrate = migrate[0]
	}
	return durable.DefineTask(def)
}

func thtrEventually(t *testing.T, cond func() bool) {
	t.Helper()
	if err := testkit.Eventually(func() (bool, error) {
		return cond(), nil
	}); err != nil {
		t.Fatalf("Eventually: %v", err)
	}
}

func TestHarnessTaskRecovery(t *testing.T) {
	t.Run("resumes an intent/effect/outcome task interrupted after its intent across close and reopen", func(t *testing.T) {
		path := thtrSqlitePath(t)
		service := thtrNewTransferService()
		var interruptFirst atomic.Bool
		interruptFirst.Store(true)
		transfer := thtrTransferTask(service, &interruptFirst)

		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 1: %v", err)
		}
		first, err := testkit.OpenTasks(st1, []durable.AnyTask{transfer})
		if err != nil {
			t.Fatalf("OpenTasks 1: %v", err)
		}
		id := thtrCreateIn(t, first.Harness, transfer, thtrTransferInput{Amount: 7})
		if err := first.Harness.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		thtrEventually(t, func() bool { return service.Calls() == 1 })
		if err := first.Harness.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 2: %v", err)
		}
		second, err := testkit.OpenTasks(st2, []durable.AnyTask{transfer})
		if err != nil {
			t.Fatalf("OpenTasks 2: %v", err)
		}
		rec, err := second.Harness.GetTask(nil, id)
		if err != nil || rec == nil {
			t.Fatalf("GetTask 2: rec=%v err=%v", rec, err)
		}
		var cp thtrTransferCheckpoint
		if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil {
			t.Fatalf("decode checkpoint: %v", err)
		}
		memoReq, _ := rec.Memos.Get("requested")
		if rec.AbortRequested || fmt.Sprint(memoReq) != "7" || rec.State.Status != durable.TaskStatePending || cp.Phase != "apply" || cp.Key != fmt.Sprintf("transfer-%d", id) {
			t.Fatalf("unexpected reconciled task=%+v cp=%+v memo=%v", rec, cp, memoReq)
		}
		if service.Calls() != 1 {
			t.Fatalf("calls=%d, want 1", service.Calls())
		}
		if err := second.Harness.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		receipt, err := second.Harness.WaitForTask(nil, id)
		if err != nil || receipt == nil {
			t.Fatalf("WaitForTask 2: rec=%v err=%v", receipt, err)
		}
		resRaw, ok := receipt.State.Outcome.Result.Get()
		if !ok || receipt.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("unexpected outcome=%+v", receipt.State.Outcome)
		}
		var tr thtrTransferResult
		if err := jsonx.Decode(resRaw, &tr); err != nil || tr.Receipt != 70 {
			t.Fatalf("result=%v err=%v, want receipt 70", tr, err)
		}
		if service.Calls() != 2 || service.AppliedSize() != 1 {
			t.Fatalf("calls=%d appliedSize=%d, want 2/1", service.Calls(), service.AppliedSize())
		}
		if err := second.Harness.Close(nil); err != nil {
			t.Fatalf("Close 2: %v", err)
		}

		st3, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 3: %v", err)
		}
		third, err := testkit.OpenTasks(st3, []durable.AnyTask{transfer})
		if err != nil {
			t.Fatalf("OpenTasks 3: %v", err)
		}
		finalRec, err := third.Harness.GetTask(nil, id)
		if err != nil || finalRec == nil {
			t.Fatalf("GetTask 3: %v", err)
		}
		finalJSON, err1 := jsonx.Stringify(finalRec)
		receiptJSON, err2 := jsonx.Stringify(receipt)
		if err1 != nil || err2 != nil || finalJSON != receiptJSON {
			t.Fatalf("reopened receipt %s (%v) != %s (%v)", finalJSON, err1, receiptJSON, err2)
		}
		_ = third.Harness.Close(nil)
	})

	t.Run("resumes abort work after close at every direct-task abort stage", func(t *testing.T) {
		path := thtrSqlitePath(t)
		log := &thtrLog{}
		var abortBlock atomic.Bool
		abortBlock.Store(true)
		abortReached := testkit.NewDeferred[struct{}]()
		runRelease := testkit.NewDeferred[struct{}]()

		abortable := durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
			Name:    "test.abortable",
			Version: 1,
			Initial: func(_ any) thtrStep { return thtrStep{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
				"run": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					log.Add("run")
					// Ignores the abort signal until released, so the mark is durable while the run is active.
					_, _ = runRelease.Wait()
					return nil
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				log.Add("abort")
				if abortBlock.Load() {
					abortReached.Resolve(struct{}{})
					_ = testkit.Aborted(rt.Signal())
					return nil
				}
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
					next := testkit.AbortedWith[thtrStep, any](current.Checkpoint, "stop")
					return &next, nil
				})
			},
		})

		// Stage 1: the mark is committed while the run invocation is still active.
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 1: %v", err)
		}
		opened, err := testkit.OpenTasks(st1, []durable.AnyTask{abortable})
		if err != nil {
			t.Fatalf("open 1: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, abortable, nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("resume 1: %v", err)
		}
		thtrEventually(t, func() bool { return log.Len() == 1 })
		type abortRes struct {
			res durable.TaskAbortResult
			err error
		}
		abortCh := make(chan abortRes, 1)
		go func() {
			r, e := opened.Harness.AbortTask(nil, id)
			abortCh <- abortRes{r, e}
		}()
		thtrEventually(t, func() bool {
			rec, err := opened.Harness.GetTask(nil, id)
			return err == nil && rec != nil && rec.AbortRequested
		})
		waitIdle := make(chan error, 1)
		go func() {
			waitIdle <- opened.Harness.WaitForIdle(nil)
		}()
		closeCh := make(chan error, 1)
		go func() {
			closeCh <- opened.Harness.Close(nil)
		}()
		<-waitIdle
		runRelease.Resolve(struct{}{})
		if err := <-closeCh; err != nil {
			t.Fatalf("close 1: %v", err)
		}
		ar := <-abortCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("aborting=%+v, want marked", ar)
		}
		if !slices.Equal(log.Snapshot(), []string{"run"}) {
			t.Fatalf("stage 1 log=%v", log.Snapshot())
		}

		// Stage 2: reopen dispatches the abort invocation, never the run; close while it is active.
		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 2: %v", err)
		}
		opened, err = testkit.OpenTasks(st2, []durable.AnyTask{abortable})
		if err != nil {
			t.Fatalf("open 2: %v", err)
		}
		rec2, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec2 == nil || !rec2.AbortRequested || rec2.State.Status != durable.TaskStatePending {
			t.Fatalf("stage 2 rec=%+v err=%v", rec2, err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("resume 2: %v", err)
		}
		_, _ = abortReached.Wait()
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 2: %v", err)
		}
		if !slices.Equal(log.Snapshot(), []string{"run", "abort"}) {
			t.Fatalf("stage 2 log=%v", log.Snapshot())
		}

		// Stage 3: a fresh abort invocation settles the task.
		abortBlock.Store(false)
		st3, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 3: %v", err)
		}
		opened, err = testkit.OpenTasks(st3, []durable.AnyTask{abortable})
		if err != nil {
			t.Fatalf("open 3: %v", err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("resume 3: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled == nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "stop" {
			t.Fatalf("stage 3 settled=%+v err=%v", settled, err)
		}
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 3: %v", err)
		}
		if !slices.Equal(log.Snapshot(), []string{"run", "abort", "abort"}) {
			t.Fatalf("stage 3 log=%v", log.Snapshot())
		}

		// Stage 4: the terminal receipt survives reopen and nothing runs again.
		st4, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 4: %v", err)
		}
		opened, err = testkit.OpenTasks(st4, []durable.AnyTask{abortable})
		if err != nil {
			t.Fatalf("open 4: %v", err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("resume 4: %v", err)
		}
		testkit.Flush()
		res4, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res4 != durable.TaskAbortTerminal {
			t.Fatalf("stage 4 abort=%s err=%v", res4, err)
		}
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 4: %v", err)
		}
		if !slices.Equal(log.Snapshot(), []string{"run", "abort", "abort"}) {
			t.Fatalf("stage 4 log=%v", log.Snapshot())
		}
	})
}

func thtrCrashTask(log *thtrLog, blockAbort bool, stop <-chan struct{}) *durable.Task[any, thtrStep, any, struct{}] {
	return durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
		Name:    "test.crash",
		Version: 1,
		Initial: func(_ any) thtrStep { return thtrStep{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
			"run": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				log.Add("run")
				<-stop
				return nil
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
			log.Add("abort")
			if blockAbort {
				<-stop
				return nil
			}
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
				next := testkit.AbortedWith[thtrStep, any](current.Checkpoint, "recovered")
				return &next, nil
			})
		},
	})
}

func thtrRecoverCrash(t *testing.T, st *testkit.ControlledStorage, log *thtrLog, stop <-chan struct{}) (durable.Harness, durable.TaskId) {
	t.Helper()
	st.Crash()
	res, err := testkit.OpenTasks(st, []durable.AnyTask{thtrCrashTask(log, false, stop)})
	if err != nil {
		t.Fatalf("OpenTasks recover: %v", err)
	}
	var page durable.Page[durable.TaskRecord, durable.Cursor]
	if err := res.Harness.Commit(nil, func(tx durable.Tx) error {
		kind := "test.crash"
		var err error
		page, err = tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 1, nil)
		return err
	}); err != nil {
		t.Fatalf("ScanTasks: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatalf("no test.crash task found")
	}
	return res.Harness, page.Items[0].Id
}

func thtrStartCrashedRun(t *testing.T, log *thtrLog, stop <-chan struct{}) (*testkit.ControlledStorage, durable.Harness, durable.TaskId) {
	t.Helper()
	st := testkit.NewControlledStorage()
	res, err := testkit.OpenTasks(st, []durable.AnyTask{thtrCrashTask(log, true, stop)})
	if err != nil {
		t.Fatalf("OpenTasks: %v", err)
	}
	id := thtrCreateIn(t, res.Harness, thtrCrashTask(log, true, stop), nil)
	if err := res.Harness.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	thtrEventually(t, func() bool { return log.Len() == 1 })
	return st, res.Harness, id
}

func TestHarnessTaskCrashRecovery(t *testing.T) {
	t.Run("crash while the mark commit is in storage: the run resumes", func(t *testing.T) {
		stop := make(chan struct{})
		var stopOnce sync.Once
		stopNow := func() { stopOnce.Do(func() { close(stop) }) }
		t.Cleanup(stopNow)
		log := &thtrLog{}
		st, harness, id := thtrStartCrashedRun(t, log, stop)
		held := st.HoldCommits()
		go func() { _, _ = harness.AbortTask(nil, id) }()
		<-held.Entered
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() {
			stopNow()
			held.Release()
			_ = recH.Close(nil)
		}()
		rec, err := recH.GetTask(nil, id)
		if err != nil || rec == nil || rec.AbortRequested || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume recover: %v", err)
		}
		thtrEventually(t, func() bool { return log.Len() == 2 })
		if !slices.Equal(log.Snapshot(), []string{"run", "run"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
	})

	t.Run("crash after the mark before the run joins: only the abort handler runs", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		log := &thtrLog{}
		st, harness, id := thtrStartCrashedRun(t, log, stop)
		var joined atomic.Bool
		go func() {
			_, _ = harness.AbortTask(nil, id)
			joined.Store(true)
		}()
		thtrEventually(t, func() bool {
			rec, err := harness.GetTask(nil, id)
			return err == nil && rec != nil && rec.AbortRequested
		})
		testkit.Flush()
		if joined.Load() {
			t.Fatalf("expected AbortTask not to finish joining the stuck run")
		}
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() { _ = recH.Close(nil) }()
		rec, err := recH.GetTask(nil, id)
		if err != nil || rec == nil || !rec.AbortRequested || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := recH.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "recovered" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if !slices.Equal(log.Snapshot(), []string{"run", "abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
	})

	t.Run("crash while the abort handler runs: a fresh abort invocation settles the task", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		log := &thtrLog{}
		st := testkit.NewControlledStorage()
		crash := thtrCrashTask(log, true, stop)
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{crash})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, crash, nil)
		if _, err := opened.Harness.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return log.Len() == 1 })
		if !slices.Equal(log.Snapshot(), []string{"abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStateRunning {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() { _ = recH.Close(nil) }()
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume rec: %v", err)
		}
		settled, err := recH.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "recovered" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if !slices.Equal(log.Snapshot(), []string{"abort", "abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
	})

	t.Run("crash while the abort outcome is in storage: the abort handler runs again", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		log := &thtrLog{}
		st := testkit.NewControlledStorage()
		reached := testkit.NewDeferred[struct{}]()
		proceed := testkit.NewDeferred[struct{}]()
		crash := durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
			Name:    "test.crash",
			Version: 1,
			Initial: func(_ any) thtrStep { return thtrStep{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
				"run": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					return nil
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				log.Add("abort")
				reached.Resolve(struct{}{})
				_, _ = proceed.Wait()
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
					next := testkit.AbortedWith[thtrStep, any](current.Checkpoint, "lost")
					return &next, nil
				})
			},
		})
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{crash})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, crash, nil)
		if _, err := opened.Harness.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		held := st.HoldCommits()
		proceed.Resolve(struct{}{})
		<-held.Entered
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() { _ = recH.Close(nil) }()
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume rec: %v", err)
		}
		settled, err := recH.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "recovered" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		if !slices.Equal(log.Snapshot(), []string{"abort", "abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
		held.Release()
	})

	t.Run("crash after the terminal outcome: nothing runs again", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		log := &thtrLog{}
		st := testkit.NewControlledStorage()
		crash := thtrCrashTask(log, false, stop)
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{crash})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, crash, nil)
		if _, err := opened.Harness.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() { _ = recH.Close(nil) }()
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume rec: %v", err)
		}
		testkit.Flush()
		if !slices.Equal(log.Snapshot(), []string{"abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
		rec, err := recH.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStateTerminal || rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeAborted || rec.State.Outcome.Reason == nil || *rec.State.Outcome.Reason != "recovered" {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
	})

	t.Run("crash while the reservation commit is in storage: the task is still pending", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		log := &thtrLog{}
		st := testkit.NewControlledStorage()
		crash := thtrCrashTask(log, false, stop)
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{crash})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, crash, nil)
		held := st.HoldCommits()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		<-held.Entered
		recH, _ := thtrRecoverCrash(t, st, log, stop)
		defer func() { _ = recH.Close(nil) }()
		rec, err := recH.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		if log.Len() != 0 {
			t.Fatalf("log=%v, want empty", log.Snapshot())
		}
		if _, err := recH.AbortTask(nil, id); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if err := recH.Resume(); err != nil {
			t.Fatalf("Resume rec: %v", err)
		}
		if _, err := recH.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask rec: %v", err)
		}
		if !slices.Equal(log.Snapshot(), []string{"abort"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
		held.Release()
	})
}

func TestHarnessBlockedTasks(t *testing.T) {
	t.Run("keeps a task with a missing definition pending, and live for idle waits, until registration", func(t *testing.T) {
		v1 := thtrVersioned(1, "v1")
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtrCreateIn(t, opened.Harness, v1, nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		testkit.Flush()
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		idleCh := make(chan error, 1)
		go func() { idleCh <- opened.Harness.WaitForIdle(nil) }()
		testkit.Flush()
		if _, err := testkit.AddTask(opened.Registry, v1); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		if err := <-idleCh; err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		finalRec, err := opened.Harness.GetTask(nil, id)
		if err != nil || finalRec == nil || finalRec.State.Status != durable.TaskStateTerminal || finalRec.State.Outcome == nil || finalRec.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("finalRec=%+v err=%v", finalRec, err)
		}
		resVal, _ := finalRec.State.Outcome.Result.Get()
		if resVal != "v1" {
			t.Fatalf("result=%v, want v1", resVal)
		}
	})

	t.Run("keeps a task stored by a newer version pending until a fitting definition is registered", func(t *testing.T) {
		registry := durable.CreateRegistry()
		if _, err := testkit.AddTask(registry, thtrVersioned(1, "old")); err != nil {
			t.Fatalf("AddTask v1: %v", err)
		}
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil, testkit.OpenTasksOptions{Registry: registry})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtrCreateIn(t, opened.Harness, thtrVersioned(2, "new"), nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		testkit.Flush()
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		if _, err := testkit.AddTask(registry, thtrVersioned(2, "new")); err != nil {
			t.Fatalf("AddTask v2: %v", err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		resVal, _ := settled.State.Outcome.Result.Get()
		if resVal != "new" {
			t.Fatalf("result=%v, want new", resVal)
		}
	})

	t.Run("migrates at reservation, leaves the record unchanged when migration fails, and retries only for a new definition", func(t *testing.T) {
		path := thtrSqlitePath(t)
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 1: %v", err)
		}
		opened, err := testkit.OpenTasks(st1, nil)
		if err != nil {
			t.Fatalf("open 1: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, thtrVersioned(1, "v1"), nil)
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 1: %v", err)
		}

		registry := durable.CreateRegistry()
		var failures atomic.Int32
		if _, err := testkit.AddTask(registry, thtrVersioned(2, "v2", func(_ durable.JsonValue, _ durable.JsonValue, _ int) (any, thtrStep, error) {
			failures.Add(1)
			return nil, thtrStep{}, errors.New("cannot migrate")
		})); err != nil {
			t.Fatalf("AddTask broken v2: %v", err)
		}
		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 2: %v", err)
		}
		opened, err = testkit.OpenTasks(st2, nil, testkit.OpenTasksOptions{Registry: registry})
		if err != nil {
			t.Fatalf("open 2: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return len(opened.Reports()) == 1 })

		// An unrelated registry change wakes the scheduler without retrying the same failed definition.
		if _, err := testkit.AddTool(registry, testkit.Tool("unrelated", "unrelated")); err != nil {
			t.Fatalf("AddTool unrelated: %v", err)
		}
		testkit.Flush()
		if failures.Load() != 1 || len(opened.Reports()) != 1 {
			t.Fatalf("failures=%d reports=%v, want 1", failures.Load(), opened.Reports())
		}
		if !strings.Contains(opened.Reports()[0].Error(), "cannot migrate") {
			t.Fatalf("report=%q", opened.Reports()[0].Error())
		}
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.Version != 1 || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		var cp thtrStep
		if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil || cp.Phase != "run" {
			t.Fatalf("cp=%+v err=%v", cp, err)
		}

		var mu sync.Mutex
		var migrations []int
		if _, err := testkit.AddTask(registry, thtrVersioned(2, "v2", func(_ durable.JsonValue, checkpoint durable.JsonValue, fromVersion int) (any, thtrStep, error) {
			mu.Lock()
			migrations = append(migrations, fromVersion)
			mu.Unlock()
			var decoded thtrStep
			if err := jsonx.Decode(checkpoint, &decoded); err != nil {
				return nil, thtrStep{}, err
			}
			return nil, decoded, nil
		})); err != nil {
			t.Fatalf("AddTask good v2: %v", err)
		}
		receipt, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || receipt.Version != 2 || receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		resVal, _ := receipt.State.Outcome.Result.Get()
		if resVal != "v2" {
			t.Fatalf("result=%v", resVal)
		}
		mu.Lock()
		gotMigrations := slices.Clone(migrations)
		mu.Unlock()
		if !slices.Equal(gotMigrations, []int{1}) {
			t.Fatalf("migrations=%v, want [1]", gotMigrations)
		}
	})

	t.Run("blocks an older record whose newer definition has no migration", func(t *testing.T) {
		path := thtrSqlitePath(t)
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 1: %v", err)
		}
		opened, err := testkit.OpenTasks(st1, nil)
		if err != nil {
			t.Fatalf("open 1: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, thtrVersioned(1, "v1"), nil)
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 1: %v", err)
		}

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 2: %v", err)
		}
		opened, err = testkit.OpenTasks(st2, []durable.AnyTask{thtrVersioned(2, "v2")})
		if err != nil {
			t.Fatalf("open 2: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return len(opened.Reports()) == 1 })
		if !strings.Contains(opened.Reports()[0].Error(), "has no migration from 1") {
			t.Fatalf("report=%q", opened.Reports()[0].Error())
		}
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.Version != 1 || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
	})

	t.Run("settles an aborted blocked task as orphaned and retires its documents", func(t *testing.T) {
		scratch, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.orphan-scratch",
			Version: 1,
			Scope:   durable.DocumentScopeTask,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("n", 0) },
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatalf("Root: %v", err)
		}
		idAny, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			created, err := tx.CreateTask(thtrVersioned(1, "x"), nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			draft, err := tx.Doc(scratch, created)
			if err != nil {
				return nil, err
			}
			draft.Value.Set("n", 1)
			return created, nil
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		id := idAny.(durable.TaskId)
		// Before resume: the marking commit settles the blocked task directly.
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%s err=%v", res, err)
		}
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.State.Status != durable.TaskStateTerminal || rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeOrphaned || rec.State.Outcome.Reason == nil || *rec.State.Outcome.Reason != string(durable.TaskBlockedMissingTask) {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		snap, err := opened.Harness.Snapshot(nil, scratch, id)
		if err != nil || snap != nil {
			t.Fatalf("snap=%v err=%v, want nil", snap, err)
		}
	})

	t.Run("orphans a marked task whose definition disappeared while its run was active", func(t *testing.T) {
		reached := testkit.NewDeferred[struct{}]()
		running := durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
			Name:    "test.vanishing",
			Version: 1,
			Initial: func(_ any) thtrStep { return thtrStep{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
				"run": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					reached.Resolve(struct{}{})
					_ = testkit.Aborted(rt.Signal())
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				return errors.New("must not run")
			},
		})
		registry := durable.CreateRegistry()
		reg, err := testkit.AddTask(registry, running)
		if err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil, testkit.OpenTasksOptions{Registry: registry})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtrCreateIn(t, opened.Harness, running, nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		reg.Dispose()
		// An active run means the mark does not orphan directly; the scheduler orphans once the run has ended.
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%s err=%v", res, err)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeOrphaned || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "missing_task" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
	})

	t.Run("orphans a reopened abort-marked task without a definition once scheduling resumes", func(t *testing.T) {
		path := thtrSqlitePath(t)
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 1: %v", err)
		}
		opened, err := testkit.OpenTasks(st1, []durable.AnyTask{thtrVersioned(1, "x")})
		if err != nil {
			t.Fatalf("open 1: %v", err)
		}
		id := thtrCreateIn(t, opened.Harness, thtrVersioned(1, "x"), nil)
		res, err := opened.Harness.AbortTask(nil, id)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%s err=%v", res, err)
		}
		if err := opened.Harness.Close(nil); err != nil {
			t.Fatalf("close 1: %v", err)
		}

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("sqlite 2: %v", err)
		}
		opened, err = testkit.OpenTasks(st2, nil)
		if err != nil {
			t.Fatalf("open 2: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || !rec.AbortRequested || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		idleCh := make(chan error, 1)
		go func() { idleCh <- opened.Harness.WaitForIdle(nil) }()
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if err := <-idleCh; err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		finalRec, err := opened.Harness.GetTask(nil, id)
		if err != nil || finalRec == nil || finalRec.State.Status != durable.TaskStateTerminal || finalRec.State.Outcome == nil || finalRec.State.Outcome.Status != durable.TaskOutcomeOrphaned || finalRec.State.Outcome.Reason == nil || *finalRec.State.Outcome.Reason != "missing_task" {
			t.Fatalf("finalRec=%+v err=%v", finalRec, err)
		}
	})
}

type thtrHandoverGates struct {
	a     *testkit.Deferred[struct{}]
	b     *testkit.Deferred[struct{}]
	onEnd func()
}

func thtrHandoverTask(
	label string,
	version int,
	log *thtrLog,
	gates *thtrHandoverGates,
	migrate ...func(durable.JsonValue, durable.JsonValue, int) (any, thtrStep, error),
) *durable.Task[any, thtrStep, any, struct{}] {
	advance := func(phase string, nextPhase string) durable.PhaseHandler[any, thtrStep, any, struct{}] {
		return func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
			log.Add(fmt.Sprintf("%s:%s start", label, phase))
			if gates != nil {
				if phase == "a" && gates.a != nil {
					_, _ = gates.a.Wait()
				}
				if phase == "b" && gates.b != nil {
					_, _ = gates.b.Wait()
				}
			}
			if err := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
				return &durable.NextTaskState[thtrStep, any]{
					Status:     durable.TaskStateRunning,
					Checkpoint: thtrStep{Phase: nextPhase},
				}, nil
			}); err != nil {
				return err
			}
			testkit.Flush()
			if gates != nil && gates.onEnd != nil {
				gates.onEnd()
			}
			log.Add(fmt.Sprintf("%s:%s end", label, phase))
			return nil
		}
	}
	def := durable.TaskDefinition[any, thtrStep, any, struct{}]{
		Name:    "test.handover",
		Version: version,
		Initial: func(_ any) thtrStep { return thtrStep{Phase: "a"} },
		Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
			"a": advance("a", "b"),
			"b": advance("b", "c"),
			"c": func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				log.Add(fmt.Sprintf("%s:c", label))
				return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
					next := testkit.Completed[thtrStep, any](current.Checkpoint, nil)
					return &next, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
			log.Add(fmt.Sprintf("%s:abort", label))
			return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
				next := testkit.AbortedWith[thtrStep, any](current.Checkpoint, label)
				return &next, nil
			})
		},
	}
	if len(migrate) > 0 && migrate[0] != nil {
		def.Migrate = migrate[0]
	}
	return durable.DefineTask(def)
}

func thtrStartHandover(t *testing.T, log *thtrLog, gates *thtrHandoverGates, st durable.Storage) (*testkit.OpenTasksResult, testkit.Installed, durable.TaskId) {
	t.Helper()
	if st == nil {
		st = storage.NewMemoryStorage()
	}
	registry := durable.CreateRegistry()
	old, err := testkit.AddTask(registry, thtrHandoverTask("old", 1, log, gates))
	if err != nil {
		t.Fatalf("AddTask old: %v", err)
	}
	opened, err := testkit.OpenTasks(st, nil, testkit.OpenTasksOptions{Registry: registry})
	if err != nil {
		t.Fatalf("OpenTasks: %v", err)
	}
	id := thtrCreateIn(t, opened.Harness, thtrHandoverTask("old", 1, log, nil), nil)
	if err := opened.Harness.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	thtrEventually(t, func() bool { return log.Len() == 1 })
	return opened, old, id
}

func TestHarnessDefinitionHandover(t *testing.T) {
	t.Run("hands over at the next phase boundary to a same-version replacement without overlap", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		opened, _, id := thtrStartHandover(t, log, &thtrHandoverGates{a: gate}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		if _, err := testkit.AddTask(opened.Registry, thtrHandoverTask("new", 1, log, nil)); err != nil {
			t.Fatalf("AddTask new: %v", err)
		}
		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		want := []string{"old:a start", "old:a end", "new:b start", "new:b end", "new:c"}
		if !slices.Equal(log.Snapshot(), want) {
			t.Fatalf("log=%v, want %v", log.Snapshot(), want)
		}
	})

	t.Run("keeps the memos across a handover", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		reached := testkit.NewDeferred[struct{}]()
		memoTask := func(label string) *durable.Task[any, thtrStep, string, struct{}] {
			return durable.DefineTask(durable.TaskDefinition[any, thtrStep, string, struct{}]{
				Name:    "test.handover-memo",
				Version: 1,
				Initial: func(_ any) thtrStep { return thtrStep{Phase: "a"} },
				Phases: map[string]durable.PhaseHandler[any, thtrStep, string, struct{}]{
					"a": func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, string, struct{}]) error {
						if _, err := rt.MemoPut(ctx, "picked", label); err != nil {
							return err
						}
						reached.Resolve(struct{}{})
						_, _ = gate.Wait()
						return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, string], error) {
							return &durable.NextTaskState[thtrStep, string]{
								Status:     durable.TaskStateRunning,
								Checkpoint: thtrStep{Phase: "b"},
							}, nil
						})
					},
					"b": func(ctx chord.Context, _ durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, string, struct{}]) error {
						pickedVal, err := rt.MemoPut(ctx, "picked", label)
						if err != nil {
							return err
						}
						picked := fmt.Sprint(pickedVal)
						log.Add(fmt.Sprintf("%s:b %s", label, picked))
						return rt.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, string], error) {
							next := testkit.Completed(current.Checkpoint, picked)
							return &next, nil
						})
					},
				},
				Abort: func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, string, struct{}]) error {
					return nil
				},
			})
		}

		registry := durable.CreateRegistry()
		if _, err := testkit.AddTask(registry, memoTask("old")); err != nil {
			t.Fatalf("AddTask old: %v", err)
		}
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil, testkit.OpenTasksOptions{Registry: registry})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		id := thtrCreateIn(t, opened.Harness, memoTask("old"), nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_, _ = reached.Wait()
		if _, err := testkit.AddTask(registry, memoTask("new")); err != nil {
			t.Fatalf("AddTask new: %v", err)
		}
		gate.Resolve(struct{}{})
		receipt, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		resVal, _ := receipt.State.Outcome.Result.Get()
		if resVal != "old" || !slices.Equal(log.Snapshot(), []string{"new:b old"}) {
			t.Fatalf("res=%v log=%v", resVal, log.Snapshot())
		}
	})

	t.Run("hands over to a newer version with a migration", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		opened, _, id := thtrStartHandover(t, log, &thtrHandoverGates{a: gate}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		if _, err := testkit.AddTask(opened.Registry, thtrHandoverTask("v2", 2, log, nil, func(_ durable.JsonValue, _ durable.JsonValue, _ int) (any, thtrStep, error) {
			return nil, thtrStep{Phase: "c"}, nil
		})); err != nil {
			t.Fatalf("AddTask v2: %v", err)
		}
		gate.Resolve(struct{}{})
		receipt, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || receipt.Version != 2 {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		if !slices.Equal(log.Snapshot(), []string{"old:a start", "old:a end", "v2:c"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
	})

	t.Run("hands over to a newer version whose migration fails and leaves the task blocked", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		opened, _, id := thtrStartHandover(t, log, &thtrHandoverGates{a: gate}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		if _, err := testkit.AddTask(opened.Registry, thtrHandoverTask("broken", 2, log, nil, func(_ durable.JsonValue, _ durable.JsonValue, _ int) (any, thtrStep, error) {
			return nil, thtrStep{}, errors.New("broken migration")
		})); err != nil {
			t.Fatalf("AddTask broken: %v", err)
		}
		gate.Resolve(struct{}{})
		thtrEventually(t, func() bool { return len(opened.Reports()) == 1 })
		testkit.Flush()
		rec, err := opened.Harness.GetTask(nil, id)
		if err != nil || rec == nil || rec.Version != 1 || rec.State.Status != durable.TaskStatePending {
			t.Fatalf("rec=%+v err=%v", rec, err)
		}
		var cp thtrStep
		if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil || cp.Phase != "b" {
			t.Fatalf("cp=%+v err=%v", cp, err)
		}
		if !slices.Equal(log.Snapshot(), []string{"old:a start", "old:a end"}) {
			t.Fatalf("log=%v", log.Snapshot())
		}
	})

	t.Run("keeps running under the old definition when the replacement is missing or cannot take the task", func(t *testing.T) {
		log := &thtrLog{}
		gateA := testkit.NewDeferred[struct{}]()
		gateB := testkit.NewDeferred[struct{}]()
		opened, old, id := thtrStartHandover(t, log, &thtrHandoverGates{a: gateA, b: gateB}, nil)
		defer func() { _ = opened.Harness.Close(nil) }()
		old.Dispose()
		gateA.Resolve(struct{}{})
		thtrEventually(t, func() bool { return log.Contains("old:b start") })
		if _, err := testkit.AddTask(opened.Registry, thtrHandoverTask("incompatible", 2, log, nil)); err != nil {
			t.Fatalf("AddTask incompatible: %v", err)
		}
		gateB.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		want := []string{"old:a start", "old:a end", "old:b start", "old:b end", "old:c"}
		if !slices.Equal(log.Snapshot(), want) {
			t.Fatalf("log=%v, want %v", log.Snapshot(), want)
		}
		reports := opened.Reports()
		if len(reports) != 2 || !strings.Contains(reports[0].Error(), "missing_task") || !strings.Contains(reports[1].Error(), "incompatible_task") {
			t.Fatalf("reports=%v", reports)
		}
	})

	t.Run("rejects a runtime commit of the old invocation queued behind its handover commit", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		st := testkit.NewControlledStorage()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		oldRtD := testkit.NewDeferred[*durable.TaskRuntime[any, thtrStep, any, struct{}]]()
		harnessReady := testkit.NewDeferred[durable.Harness]()
		registry := durable.CreateRegistry()

		old := durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
			Name:    "test.handover",
			Version: 1,
			Initial: func(_ any) thtrStep { return thtrStep{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
				"a": func(ctx chord.Context, task durable.RunningTask[any, thtrStep], rt *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					oldRtD.Resolve(rt)
					_, _ = gate.Wait()
					if err := rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
						return &durable.NextTaskState[thtrStep, any]{
							Status:     durable.TaskStateRunning,
							Checkpoint: thtrStep{Phase: "b"},
						}, nil
					}); err != nil {
						return err
					}
					hRef, _ := harnessReady.Wait()
					g := st.HoldCommits()
					heldD.Resolve(g)
					go func() {
						_ = hRef.Commit(nil, func(tx durable.Tx) error {
							_, err := tx.AppendEntry(task.ConversationId, durable.EntryDraft{Kind: "blocker"})
							return err
						})
					}()
					return nil
				},
				"b": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					return nil
				},
				"c": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				return nil
			},
		})
		if _, err := testkit.AddTask(registry, old); err != nil {
			t.Fatalf("AddTask old: %v", err)
		}
		opened, err := testkit.OpenTasks(st, nil, testkit.OpenTasksOptions{Registry: registry})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		harnessReady.Resolve(opened.Harness)
		id := thtrCreateIn(t, opened.Harness, old, nil)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		oldRt, _ := oldRtD.Wait()
		if _, err := testkit.AddTask(registry, thtrHandoverTask("new", 1, log, nil)); err != nil {
			t.Fatalf("AddTask new: %v", err)
		}
		gate.Resolve(struct{}{})
		held, _ := heldD.Wait()
		<-held.Entered
		testkit.Flush()
		time.Sleep(15 * time.Millisecond)

		lateCh := make(chan error, 1)
		go func() {
			lateCh <- oldRt.Commit(nil, func(_ durable.Tx, current durable.RunningTask[any, thtrStep]) (*durable.NextTaskState[thtrStep, any], error) {
				next := testkit.Completed[thtrStep, any](current.Checkpoint, nil)
				return &next, nil
			})
		}()
		testkit.Flush()
		held.Release()
		lateErr := <-lateCh
		if lateErr == nil || !strings.Contains(lateErr.Error(), "invocation has ended") {
			t.Fatalf("lateErr=%v, want invocation has ended", lateErr)
		}
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		want := []string{"new:b start", "new:b end", "new:c"}
		if !slices.Equal(log.Snapshot(), want) {
			t.Fatalf("log=%v, want %v", log.Snapshot(), want)
		}
	})

	t.Run("preserves an abort mark that races the handover commit; the new definition aborts", func(t *testing.T) {
		log := &thtrLog{}
		gate := testkit.NewDeferred[struct{}]()
		st := testkit.NewControlledStorage()
		heldD := testkit.NewDeferred[*testkit.Gate]()
		var once sync.Once
		gates := &thtrHandoverGates{
			a: gate,
			onEnd: func() {
				once.Do(func() {
					heldD.Resolve(st.HoldCommits())
				})
			},
		}
		opened, _, id := thtrStartHandover(t, log, gates, st)
		defer func() { _ = opened.Harness.Close(nil) }()
		if _, err := testkit.AddTask(opened.Registry, thtrHandoverTask("new", 1, log, nil)); err != nil {
			t.Fatalf("AddTask new: %v", err)
		}
		gate.Resolve(struct{}{})
		held, _ := heldD.Wait()
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
		testkit.Flush()
		held.Release()
		ar := <-abortCh
		if ar.err != nil || ar.res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask=%+v, want marked", ar)
		}
		settled, err := opened.Harness.WaitForTask(nil, id)
		if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted || settled.State.Outcome.Reason == nil || *settled.State.Outcome.Reason != "new" {
			t.Fatalf("settled=%+v err=%v", settled, err)
		}
		var states []string
		for _, batch := range st.Commits() {
			for _, w := range batch {
				var taskRec *durable.TaskRecord
				switch tw := w.(type) {
				case durable.StorageWriteTask:
					taskRec = &tw.Value
				case *durable.StorageWriteTask:
					taskRec = &tw.Value
				}
				if taskRec != nil && taskRec.Id == id {
					s := string(taskRec.State.Status)
					if taskRec.AbortRequested {
						s += "+mark"
					}
					states = append(states, s)
				}
			}
		}
		wantStates := []string{
			"pending",
			"running",
			"running",
			"pending",
			"pending+mark",
			"running+mark",
			"terminal+mark",
		}
		if !slices.Equal(states, wantStates) {
			t.Fatalf("states=%v, want %v", states, wantStates)
		}
		wantLog := []string{"old:a start", "old:a end", "new:abort"}
		if !slices.Equal(log.Snapshot(), wantLog) {
			t.Fatalf("log=%v, want %v", log.Snapshot(), wantLog)
		}
	})
}

func TestHarnessOpenRecovery(t *testing.T) {
	t.Run("releases its registry subscription and closes the Session when open fails", func(t *testing.T) {
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		st := testkit.NewControlledStorage()
		log := &thtrLog{}
		stuck := durable.DefineTask(durable.TaskDefinition[any, thtrStep, any, struct{}]{
			Name:    "test.stuck",
			Version: 1,
			Initial: func(_ any) thtrStep { return thtrStep{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[any, thtrStep, any, struct{}]{
				"run": func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
					log.Add("run")
					<-stop
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtrStep], _ *durable.TaskRuntime[any, thtrStep, any, struct{}]) error {
				return nil
			},
		})
		// Leave a running task behind so open has a reconciliation commit to fail.
		first, err := testkit.OpenTasks(st, []durable.AnyTask{stuck})
		if err != nil {
			t.Fatalf("OpenTasks first: %v", err)
		}
		_ = thtrCreateIn(t, first.Harness, stuck, nil)
		if err := first.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		thtrEventually(t, func() bool { return log.Len() == 1 })

		registry := durable.CreateRegistry()
		reader := testkit.NewCountingReader(registry)
		st.FailNextCommit(errors.New("disk full"))
		_, err = durable.HarnessOpen(st, durable.HarnessOptions{
			Models:   ai.CreateModels(nil),
			Registry: reader,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("HarnessOpen err=%v, want disk full", err)
		}
		if reader.Subscriptions() != 0 {
			t.Fatalf("subscriptions=%d, want 0", reader.Subscriptions())
		}
		if _, err := st.Task(nil, 1); err == nil {
			t.Fatalf("expected closed storage Task(1) to return error")
		}
	})
}
