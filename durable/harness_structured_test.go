// Ported from packages/durable/test/harness-structured.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
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

type thsrEnding string

const (
	thsrEndCompleted thsrEnding = "completed"
	thsrEndFailed    thsrEnding = "failed"
	thsrEndThrow     thsrEnding = "throw"
)

type thsrNodeInput struct {
	Name string `json:"name"`
}

type thsrNodeCheckpoint struct {
	Phase string `json:"phase"`
	Round int    `json:"round,omitzero"`
}

type thsrRuntime = *durable.TaskRuntime[thsrNodeInput, thsrNodeCheckpoint, string, struct{}]
type thsrNext = durable.NextTaskState[thsrNodeCheckpoint, string]

type thsrBehavior struct {
	run    func(rt thsrRuntime, ctx chord.Context) error
	resume func(rt thsrRuntime, ctx chord.Context, round int) error
	abort  func(rt thsrRuntime, ctx chord.Context) error
}

type thsrGate struct {
	once   sync.Once
	ch     chan struct{}
	mu     sync.Mutex
	ending thsrEnding
}

func (g *thsrGate) resolve(ending thsrEnding) {
	g.once.Do(func() {
		g.mu.Lock()
		g.ending = ending
		g.mu.Unlock()
		close(g.ch)
	})
}

func (g *thsrGate) wait(sig context.Context) (thsrEnding, error) {
	if sig == nil {
		<-g.ch
		g.mu.Lock()
		e := g.ending
		g.mu.Unlock()
		return e, nil
	}
	select {
	case <-g.ch:
		g.mu.Lock()
		e := g.ending
		g.mu.Unlock()
		return e, nil
	case <-sig.Done():
		return "", testkit.Aborted(sig)
	}
}

type thsrWorld struct {
	mu        sync.Mutex
	behaviors map[string]thsrBehavior
	gates     map[string]*thsrGate
	log       []string
}

func thsrNewWorld() *thsrWorld {
	return &thsrWorld{
		behaviors: make(map[string]thsrBehavior),
		gates:     make(map[string]*thsrGate),
	}
}

func (w *thsrWorld) gate(name string) *thsrGate {
	w.mu.Lock()
	defer w.mu.Unlock()
	g, ok := w.gates[name]
	if !ok {
		g = &thsrGate{ch: make(chan struct{})}
		w.gates[name] = g
	}
	return g
}

func (w *thsrWorld) open(name string, endings ...thsrEnding) {
	ending := thsrEndCompleted
	if len(endings) > 0 {
		ending = endings[0]
	}
	w.gate(name).resolve(ending)
}

func (w *thsrWorld) script(name string, b thsrBehavior) {
	w.mu.Lock()
	w.behaviors[name] = b
	w.mu.Unlock()
}

func (w *thsrWorld) behavior(name string) thsrBehavior {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.behaviors[name]
}

func (w *thsrWorld) pushLog(line string) {
	w.mu.Lock()
	w.log = append(w.log, line)
	w.mu.Unlock()
}

func (w *thsrWorld) snapshotLog() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.log)
}

func (w *thsrWorld) hasLog(line string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Contains(w.log, line)
}

func (w *thsrWorld) logFiltered(pred func(string) bool) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, line := range w.log {
		if pred(line) {
			out = append(out, line)
		}
	}
	return out
}

func thsrEnd(ending thsrEnding, name string) *thsrNext {
	if ending == thsrEndCompleted {
		return &thsrNext{
			Status:  durable.TaskStateTerminal,
			Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: name},
		}
	}
	return &thsrNext{
		Status: durable.TaskStateTerminal,
		Outcome: &durable.TypedTaskOutcome[string]{
			Status: durable.TaskOutcomeFailed,
			Error:  &durable.TaskOutcomeError{Message: name + " failed"},
		},
	}
}

func (w *thsrWorld) defaultRun(rt thsrRuntime, ctx chord.Context, name string) error {
	ending, err := w.gate(name).wait(rt.Signal())
	if err != nil {
		return err
	}
	if ending == thsrEndThrow {
		return errors.New(name + " threw")
	}
	return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
		return thsrEnd(ending, name), nil
	})
}

func (w *thsrWorld) nodeTask() *durable.Task[thsrNodeInput, thsrNodeCheckpoint, string, struct{}] {
	return durable.DefineTask(durable.TaskDefinition[thsrNodeInput, thsrNodeCheckpoint, string, struct{}]{
		Name:    "test.node",
		Version: 1,
		Initial: func(_ thsrNodeInput) thsrNodeCheckpoint {
			return thsrNodeCheckpoint{Phase: "run"}
		},
		Phases: map[string]durable.PhaseHandler[thsrNodeInput, thsrNodeCheckpoint, string, struct{}]{
			"run": func(ctx chord.Context, task durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint], rt thsrRuntime) error {
				name := task.Input.Name
				w.pushLog("run:" + name)
				b := w.behavior(name)
				if b.run != nil {
					return b.run(rt, ctx)
				}
				return w.defaultRun(rt, ctx, name)
			},
			"resume": func(ctx chord.Context, task durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint], rt thsrRuntime) error {
				name := task.Input.Name
				w.pushLog("resume:" + name)
				b := w.behavior(name)
				if b.resume != nil {
					return b.resume(rt, ctx, task.Checkpoint.Round)
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrEnd(thsrEndCompleted, name), nil
				})
			},
		},
		Abort: func(ctx chord.Context, task durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint], rt thsrRuntime) error {
			name := task.Input.Name
			w.pushLog("abort:" + name)
			b := w.behavior(name)
			if b.abort != nil {
				return b.abort(rt, ctx)
			}
			return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
				return &thsrNext{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted, Result: name, HasRes: true},
				}, nil
			})
		},
	})
}

var thsrUnregistered = durable.DefineTask(durable.TaskDefinition[thsrNodeInput, thsrNodeCheckpoint, string, struct{}]{
	Name:    "test.unregistered",
	Version: 1,
	Initial: func(_ thsrNodeInput) thsrNodeCheckpoint { return thsrNodeCheckpoint{Phase: "run"} },
	Phases: map[string]durable.PhaseHandler[thsrNodeInput, thsrNodeCheckpoint, string, struct{}]{
		"run": func(_ chord.Context, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint], _ thsrRuntime) error {
			return nil
		},
	},
	Abort: func(_ chord.Context, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint], _ thsrRuntime) error {
		return nil
	},
})

var thsrTaskNotes = func() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "test.task-notes",
		Version: 1,
		Scope:   durable.DocumentScopeTask,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

var thsrOwnConv = durable.TaskOptions{
	Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
}

func thsrOwned(owner durable.TaskId) durable.TaskOptions {
	return durable.TaskOptions{
		Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: owner},
	}
}

func thsrSpawn(tx durable.Tx, w *thsrWorld, owner durable.TaskId, name string) (durable.TaskId, error) {
	return tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: name}, thsrOwned(owner))
}

func thsrWaitOn(on []durable.TaskId, policy durable.JoinPolicy, roundOpt ...int) *thsrNext {
	round := 1
	if len(roundOpt) > 0 {
		round = roundOpt[0]
	}
	return &thsrNext{
		Status:     durable.TaskStateWaiting,
		Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: round},
		On:         on,
		Policy:     policy,
	}
}

func thsrStart(t *testing.T, conv durable.Conversation, w *thsrWorld, name string, optsOpt ...durable.TaskOptions) durable.TaskId {
	t.Helper()
	opts := thsrOwnConv
	if len(optsOpt) > 0 {
		opts = optsOpt[0]
	}
	res, err := conv.Commit(nil, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: name}, opts)
	})
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	return res.(durable.TaskId)
}

type thsrOpened struct {
	harness  durable.Harness
	root     durable.Conversation
	registry durable.Registry
	reports  func() []error
}

func thsrOpenNodes(t *testing.T, w *thsrWorld, st durable.Storage) thsrOpened {
	t.Helper()
	if st == nil {
		st = storage.NewMemoryStorage()
	}
	opened, err := testkit.OpenTasks(st, []durable.AnyTask{w.nodeTask()})
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
	return thsrOpened{
		harness:  opened.Harness,
		root:     root,
		registry: opened.Registry,
		reports:  opened.Reports,
	}
}

func thsrState(t *testing.T, h durable.Harness, id durable.TaskId) durable.TaskState {
	t.Helper()
	rec, err := h.GetTask(nil, id)
	if err != nil || rec == nil {
		t.Fatalf("GetTask(%d): rec=%v err=%v", id, rec, err)
	}
	return rec.State
}

func thsrOutcomeOf(t *testing.T, h durable.Harness, id durable.TaskId) durable.TaskOutcomeStatus {
	t.Helper()
	rec, err := h.WaitForTask(nil, id)
	if err != nil {
		t.Fatalf("WaitForTask(%d): %v", id, err)
	}
	if rec.State.Outcome == nil {
		t.Fatalf("task %d settled without outcome: %+v", id, rec.State)
	}
	return rec.State.Outcome.Status
}

func thsrUntil(t *testing.T, check func() (bool, error)) {
	t.Helper()
	if err := testkit.Eventually(check); err != nil {
		t.Fatalf("Eventually: %v", err)
	}
}

func thsrUntilBool(t *testing.T, check func() bool) {
	t.Helper()
	thsrUntil(t, func() (bool, error) { return check(), nil })
}

func thsrBoolPtr(v bool) *bool { return &v }

func thsrTextPtr(v string) *string { return &v }

func thsrSpawnAndFinish(w *thsrWorld, name, child string) func() (durable.TaskId, bool) {
	var mu sync.Mutex
	var childId durable.TaskId
	var hasId bool
	w.script(name, thsrBehavior{
		run: func(rt thsrRuntime, ctx chord.Context) error {
			return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
				id, err := thsrSpawn(tx, w, running.Id, child)
				if err != nil {
					return nil, err
				}
				mu.Lock()
				childId = id
				hasId = true
				mu.Unlock()
				return &thsrNext{
					Status:     durable.TaskStateRunning,
					Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1},
				}, nil
			})
		},
	})
	return func() (durable.TaskId, bool) {
		mu.Lock()
		defer mu.Unlock()
		return childId, hasId
	}
}

type thsrParentTracker struct {
	mu       sync.Mutex
	ids      []durable.TaskId
	outcomes []durable.TaskOutcomeStatus
}

func (p *thsrParentTracker) getIds() []durable.TaskId {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ids)
}

func (p *thsrParentTracker) setIds(ids []durable.TaskId) {
	p.mu.Lock()
	p.ids = slices.Clone(ids)
	p.mu.Unlock()
}

func (p *thsrParentTracker) getOutcomes() []durable.TaskOutcomeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.outcomes)
}

func thsrParentOf(w *thsrWorld, parent string, children []string, policy durable.JoinPolicy) *thsrParentTracker {
	found := &thsrParentTracker{}
	w.script(parent, thsrBehavior{
		run: func(rt thsrRuntime, ctx chord.Context) error {
			return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
				var ids []durable.TaskId
				for _, name := range children {
					id, err := thsrSpawn(tx, w, running.Id, name)
					if err != nil {
						return nil, err
					}
					ids = append(ids, id)
				}
				found.setIds(ids)
				return thsrWaitOn(ids, policy), nil
			})
		},
		resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
			outcomes, err := rt.Outcomes(ctx, found.getIds())
			if err != nil {
				return err
			}
			statuses := make([]durable.TaskOutcomeStatus, len(outcomes))
			for i, o := range outcomes {
				statuses[i] = o.Status
			}
			found.mu.Lock()
			found.outcomes = statuses
			found.mu.Unlock()
			return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
				return thsrEnd(thsrEndCompleted, parent), nil
			})
		},
	})
	return found
}

type thsrRejectingStorage struct {
	durable.Storage
	mu     sync.Mutex
	reject *durable.TaskId
}

func (s *thsrRejectingStorage) setReject(id durable.TaskId) {
	s.mu.Lock()
	cp := id
	s.reject = &cp
	s.mu.Unlock()
}

func (s *thsrRejectingStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	target := s.reject
	shouldReject := false
	if target != nil {
		for _, w := range writes {
			if tw, ok := w.(durable.StorageWriteTask); ok {
				if tw.Value.Id == *target && tw.Value.State.Status == durable.TaskStateTerminal {
					shouldReject = true
					s.reject = nil
					break
				}
			}
		}
	}
	s.mu.Unlock()
	if shouldReject {
		return 0, durable.NewStorageRejected("rejected once", nil)
	}
	return s.Storage.Commit(ctx, writes)
}

func thsrAsyncWaitForTask(h durable.Harness, id durable.TaskId) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		_, _ = h.WaitForTask(nil, id)
	}()
	return ch
}

func thsrAsyncWaitForIdle(conv durable.Conversation) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		_ = conv.WaitForIdle(nil)
	}()
	return ch
}

func TestHarnessStructured_TaskOwnership(t *testing.T) {
	t.Run("creates a child in its owner's conversation with its owner recorded", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var child durable.TaskId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					id, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = id
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{id}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return child != 0
		})
		mu.Lock()
		childId := child
		mu.Unlock()
		rec, err := opened.harness.GetTask(nil, childId)
		if err != nil || rec == nil {
			t.Fatalf("GetTask(child): %v %v", rec, err)
		}
		if rec.Owner == nil || *rec.Owner != parent || rec.ConversationId != opened.root.Id() || rec.Background {
			t.Fatalf("unexpected child record: %+v", rec)
		}
		parentRec, err := opened.harness.GetTask(nil, parent)
		if err != nil || parentRec.Owner != nil {
			t.Fatalf("expected parent.Owner == nil, got %+v err=%v", parentRec, err)
		}
		w.open("child")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
	})

	t.Run("rejects no ownership, a missing owner, a child in another conversation, and a background child", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		parent := thsrStart(t, opened.root, w, "parent")
		other, err := opened.harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		create := func(opts durable.TaskOptions) error {
			_, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
				return tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "x"}, opts)
			})
			return err
		}
		if err := create(durable.TaskOptions{}); err == nil {
			t.Fatalf("expected missing ownership rejection")
		}
		if err := create(thsrOwned(durable.TaskId(999999))); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected does not exist, got %v", err)
		}
		otherId := other.Id()
		optsOther := thsrOwned(parent)
		optsOther.ConversationId = &otherId
		if err := create(optsOther); err == nil || !strings.Contains(err.Error(), "owner's conversation") {
			t.Fatalf("expected owner's conversation rejection, got %v", err)
		}
		optsBg := thsrOwned(parent)
		optsBg.Background = thsrBoolPtr(true)
		if err := create(optsBg); err == nil || !strings.Contains(err.Error(), "cannot be background") {
			t.Fatalf("expected cannot be background rejection, got %v", err)
		}
		childRes, err := other.Commit(nil, func(tx durable.Tx) (any, error) {
			return thsrSpawn(tx, w, parent, "child")
		})
		if err != nil {
			t.Fatalf("other.Commit(spawn): %v", err)
		}
		childRec, err := opened.harness.GetTask(nil, childRes.(durable.TaskId))
		if err != nil || childRec.ConversationId != opened.root.Id() {
			t.Fatalf("unexpected child conv: %+v err=%v", childRec, err)
		}
		w.open("parent")
		w.open("child")
		if _, err := opened.harness.WaitForTask(nil, parent); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
	})

	t.Run("rejects new owned work below an owner that is completing, terminal, or abort-marked", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					if _, err := thsrSpawn(tx, w, running.Id, "child"); err != nil {
						return nil, err
					}
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		createChild := func() error {
			_, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
				return thsrSpawn(tx, w, parent, "late")
			})
			return err
		}
		createConversation := func() error {
			_, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
				return tx.CreateConversation(durable.ConversationCreateTxOptions{
					Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: parent},
				})
			})
			return err
		}
		for _, fn := range []func() error{createChild, createConversation} {
			if err := fn(); err == nil || !strings.Contains(err.Error(), "is completing") {
				t.Fatalf("expected 'is completing', got %v", err)
			}
		}
		w.open("child")
		if _, err := opened.harness.WaitForTask(nil, parent); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		for _, fn := range []func() error{createChild, createConversation} {
			if err := fn(); err == nil || !strings.Contains(err.Error(), "is terminal") {
				t.Fatalf("expected 'is terminal', got %v", err)
			}
		}

		w.script("slow", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.slow").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		slow := thsrStart(t, opened.root, w, "slow")
		thsrUntilBool(t, func() bool { return w.hasLog("run:slow") })
		if _, err := opened.harness.AbortTask(nil, slow); err != nil {
			t.Fatalf("AbortTask(slow): %v", err)
		}
		if _, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return thsrSpawn(tx, w, slow, "late")
		}); err == nil || !strings.Contains(err.Error(), "is abort-marked") {
			t.Fatalf("expected 'is abort-marked', got %v", err)
		}
		w.open("abort.slow")
		if got := thsrOutcomeOf(t, opened.harness, slow); got != durable.TaskOutcomeAborted {
			t.Fatalf("slow outcome = %s", got)
		}
	})

	t.Run("cannot create a child in its finishing commit, but work it starts in an owned conversation holds it", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("eager", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					if _, err := thsrSpawn(tx, w, running.Id, "never"); err != nil {
						return nil, err
					}
					return thsrEnd(thsrEndCompleted, "eager"), nil
				})
			},
		})
		eager := thsrStart(t, opened.root, w, "eager")
		settledEager, err := opened.harness.WaitForTask(nil, eager)
		if err != nil {
			t.Fatalf("WaitForTask(eager): %v", err)
		}
		if settledEager.State.Outcome == nil || settledEager.State.Outcome.Status != durable.TaskOutcomeFaulted ||
			settledEager.State.Outcome.Error == nil || !strings.Contains(settledEager.State.Outcome.Error.Message, "is completing") {
			t.Fatalf("unexpected eager outcome: %+v", settledEager.State.Outcome)
		}
		var names []string
		if err := opened.harness.Commit(nil, func(tx durable.Tx) error {
			kind := "test.node"
			page, err := tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 20, nil)
			if err != nil {
				return err
			}
			for _, item := range page.Items {
				var in thsrNodeInput
				if err := jsonx.Decode(item.Input, &in); err != nil {
					return err
				}
				names = append(names, in.Name)
			}
			return nil
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if !slices.Equal(names, []string{"eager"}) {
			t.Fatalf("expected [eager], got %v", names)
		}

		var mu sync.Mutex
		var childConv durable.ConversationId
		w.script("host", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					childConv = conv.Id
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
			resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
				return rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					mu.Lock()
					cid := childConv
					mu.Unlock()
					opts := thsrOwnConv
					opts.ConversationId = &cid
					if _, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "inner"}, opts); err != nil {
						return nil, err
					}
					return thsrEnd(thsrEndCompleted, "host"), nil
				})
			},
		})
		host := thsrStart(t, opened.root, w, "host")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, host).Status == durable.TaskStateCompleting, nil
		})
		if testkit.SettledChannel(thsrAsyncWaitForTask(opened.harness, host)) {
			t.Fatalf("expected host waiter to still be pending")
		}
		w.open("inner")
		if got := thsrOutcomeOf(t, opened.harness, host); got != durable.TaskOutcomeCompleted {
			t.Fatalf("host outcome = %s", got)
		}
	})
}

func TestHarnessStructured_Waiting(t *testing.T) {
	t.Run("resumes once every awaited task is terminal and reads their outcomes in order (allSettled)", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		tracker := &thsrParentTracker{}
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					var ids []durable.TaskId
					for _, name := range []string{"ok", "fails", "throws", "aborted"} {
						id, err := thsrSpawn(tx, w, running.Id, name)
						if err != nil {
							return nil, err
						}
						ids = append(ids, id)
					}
					orphanId, err := tx.CreateTask(thsrUnregistered, thsrNodeInput{Name: "orphan"}, thsrOwned(running.Id))
					if err != nil {
						return nil, err
					}
					ids = append(ids, orphanId)
					tracker.setIds(ids)
					return thsrWaitOn(ids, durable.JoinPolicyAllSettled), nil
				})
			},
			resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
				outcomes, err := rt.Outcomes(ctx, tracker.getIds())
				if err != nil {
					return err
				}
				statuses := make([]durable.TaskOutcomeStatus, len(outcomes))
				for i, o := range outcomes {
					statuses[i] = o.Status
				}
				tracker.mu.Lock()
				tracker.outcomes = statuses
				tracker.mu.Unlock()
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrEnd(thsrEndCompleted, "parent"), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool { return len(tracker.getIds()) == 5 })
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		ids := tracker.getIds()
		w.open("ok")
		w.open("fails", thsrEndFailed)
		w.open("throws", thsrEndThrow)
		if _, err := opened.harness.AbortTask(nil, ids[3]); err != nil {
			t.Fatalf("AbortTask(aborted): %v", err)
		}
		res, err := opened.harness.AbortTask(nil, ids[4])
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask(orphan) = %s, %v", res, err)
		}
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		want := []durable.TaskOutcomeStatus{
			durable.TaskOutcomeCompleted,
			durable.TaskOutcomeFailed,
			durable.TaskOutcomeFaulted,
			durable.TaskOutcomeAborted,
			durable.TaskOutcomeOrphaned,
		}
		if !slices.Equal(tracker.getOutcomes(), want) {
			t.Fatalf("outcomes = %v, want %v", tracker.getOutcomes(), want)
		}
		aborts := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") })
		if !slices.Equal(aborts, []string{"abort:aborted"}) {
			t.Fatalf("aborts = %v", aborts)
		}
	})

	for _, ending := range []thsrEnding{thsrEndFailed, thsrEndThrow} {
		label := "failed"
		expectedStatus := durable.TaskOutcomeFailed
		if ending == thsrEndThrow {
			label = "faulted"
			expectedStatus = durable.TaskOutcomeFaulted
		}
		t.Run("fails fast when a child ends "+label+": its live siblings are aborted, the parent is not", func(t *testing.T) {
			w := thsrNewWorld()
			opened := thsrOpenNodes(t, w, nil)
			defer func() { _ = opened.harness.Close(nil) }()

			found := thsrParentOf(w, "checkout", []string{"p1", "p2", "p3", "p4"}, durable.JoinPolicyFailFast)
			parent := thsrStart(t, opened.root, w, "checkout")
			thsrUntil(t, func() (bool, error) {
				return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
			})
			w.open("p2", ending)
			if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
				t.Fatalf("parent outcome = %s", got)
			}
			want := []durable.TaskOutcomeStatus{
				durable.TaskOutcomeAborted,
				expectedStatus,
				durable.TaskOutcomeAborted,
				durable.TaskOutcomeAborted,
			}
			if !slices.Equal(found.getOutcomes(), want) {
				t.Fatalf("outcomes = %v, want %v", found.getOutcomes(), want)
			}
			rec, err := opened.harness.GetTask(nil, parent)
			if err != nil || rec.AbortRequested {
				t.Fatalf("expected parent.AbortRequested == false, got %+v err=%v", rec, err)
			}
			aborts := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") })
			slices.Sort(aborts)
			if !slices.Equal(aborts, []string{"abort:p1", "abort:p3", "abort:p4"}) {
				t.Fatalf("aborts = %v", aborts)
			}
		})
	}

	t.Run("fails fast on a held failure before the failing child drains", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		tracker := &thsrParentTracker{}
		w.script("p1", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					if _, err := thsrSpawn(tx, w, running.Id, "grandchild"); err != nil {
						return nil, err
					}
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
			resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrEnd(thsrEndFailed, "p1"), nil
				})
			},
		})
		w.script("grandchild", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.grandchild").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		var mu sync.Mutex
		var outside durable.TaskId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					var ids []durable.TaskId
					for _, name := range []string{"p1", "p2"} {
						id, err := thsrSpawn(tx, w, running.Id, name)
						if err != nil {
							return nil, err
						}
						ids = append(ids, id)
					}
					outId, err := thsrSpawn(tx, w, running.Id, "outside")
					if err != nil {
						return nil, err
					}
					tracker.setIds(ids)
					mu.Lock()
					outside = outId
					mu.Unlock()
					return thsrWaitOn(ids, durable.JoinPolicyFailFast), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool { return len(tracker.getIds()) == 2 })
		ids := tracker.getIds()
		if got := thsrOutcomeOf(t, opened.harness, ids[1]); got != durable.TaskOutcomeAborted {
			t.Fatalf("p2 outcome = %s", got)
		}
		p1State := thsrState(t, opened.harness, ids[0])
		if p1State.Status != durable.TaskStateCompleting || p1State.Outcome == nil || p1State.Outcome.Status != durable.TaskOutcomeFailed {
			t.Fatalf("unexpected p1 state: %+v", p1State)
		}
		if got := thsrState(t, opened.harness, parent).Status; got != durable.TaskStateWaiting {
			t.Fatalf("expected parent waiting, got %s", got)
		}
		w.open("abort.grandchild")
		thsrUntilBool(t, func() bool { return w.hasLog("resume:parent") })
		marked := func(id durable.TaskId) bool {
			r, err := opened.harness.GetTask(nil, id)
			if err != nil || r == nil {
				t.Fatalf("GetTask(%d): %v", id, err)
			}
			return r.AbortRequested
		}
		if got := thsrOutcomeOf(t, opened.harness, ids[0]); got != durable.TaskOutcomeFailed {
			t.Fatalf("p1 outcome = %s", got)
		}
		mu.Lock()
		outsideId := outside
		mu.Unlock()
		if got := []bool{marked(ids[0]), marked(ids[1]), marked(outsideId)}; !slices.Equal(got, []bool{false, true, false}) {
			t.Fatalf("marked = %v", got)
		}
		if marked(parent) {
			t.Fatalf("expected parent not abort-marked")
		}
		if st := thsrState(t, opened.harness, outsideId).Status; st != durable.TaskStateRunning {
			t.Fatalf("outside status = %s", st)
		}
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		w.open("outside")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
	})

	t.Run("waits with allSettled on tasks it does not own, including already terminal ones", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		done := thsrStart(t, opened.root, w, "done")
		w.open("done")
		if _, err := opened.harness.WaitForTask(nil, done); err != nil {
			t.Fatalf("WaitForTask(done): %v", err)
		}
		live := thsrStart(t, opened.root, w, "live")
		var mu sync.Mutex
		var outcomes []durable.TaskOutcomeStatus
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn([]durable.TaskId{done, live}, durable.JoinPolicyAllSettled), nil
				})
			},
			resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
				list, err := rt.Outcomes(ctx, []durable.TaskId{done, live})
				if err != nil {
					return err
				}
				mu.Lock()
				for _, o := range list {
					outcomes = append(outcomes, o.Status)
				}
				mu.Unlock()
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrEnd(thsrEndCompleted, "parent"), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		w.open("live", thsrEndFailed)
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		mu.Lock()
		gotOutcomes := slices.Clone(outcomes)
		mu.Unlock()
		if !slices.Equal(gotOutcomes, []durable.TaskOutcomeStatus{durable.TaskOutcomeCompleted, durable.TaskOutcomeFailed}) {
			t.Fatalf("outcomes = %v", gotOutcomes)
		}
	})

	t.Run("rejects waits on itself, its owner, a missing task, and failFast on a task it does not own", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		other := thsrStart(t, opened.root, w, "other")
		type waitCase struct {
			name   string
			on     func(rt thsrRuntime) []durable.TaskId
			policy durable.JoinPolicy
			msg    string
		}
		cases := []waitCase{
			{"self", func(rt thsrRuntime) []durable.TaskId { return []durable.TaskId{rt.TaskId()} }, durable.JoinPolicyAllSettled, "cannot wait on itself or its owner"},
			{"missing", func(_ thsrRuntime) []durable.TaskId { return []durable.TaskId{999999} }, durable.JoinPolicyAllSettled, "does not exist"},
			{"foreign", func(_ thsrRuntime) []durable.TaskId { return []durable.TaskId{other} }, durable.JoinPolicyFailFast, "only on tasks it owns"},
		}
		for _, tc := range cases {
			c := tc
			w.script(c.name, thsrBehavior{
				run: func(rt thsrRuntime, ctx chord.Context) error {
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
						return thsrWaitOn(c.on(rt), c.policy), nil
					})
				},
			})
			id := thsrStart(t, opened.root, w, c.name)
			settled, err := opened.harness.WaitForTask(nil, id)
			if err != nil {
				t.Fatalf("WaitForTask(%s): %v", c.name, err)
			}
			if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeFaulted ||
				settled.State.Outcome.Error == nil || !strings.Contains(settled.State.Outcome.Error.Message, c.msg) {
				t.Fatalf("case %s unexpected outcome: %+v", c.name, settled.State.Outcome)
			}
		}

		var mu sync.Mutex
		var child, parent durable.TaskId
		w.script("child", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				mu.Lock()
				p := parent
				mu.Unlock()
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn([]durable.TaskId{p}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					c, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = c
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{c}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		pId := thsrStart(t, opened.root, w, "parent")
		mu.Lock()
		parent = pId
		mu.Unlock()
		if got := thsrOutcomeOf(t, opened.harness, pId); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		mu.Lock()
		cId := child
		mu.Unlock()
		settledChild, err := opened.harness.WaitForTask(nil, cId)
		if err != nil {
			t.Fatalf("WaitForTask(child): %v", err)
		}
		if settledChild.State.Outcome == nil || settledChild.State.Outcome.Status != durable.TaskOutcomeFaulted ||
			settledChild.State.Outcome.Error == nil || !strings.Contains(settledChild.State.Outcome.Error.Message, "cannot wait on itself or its owner") {
			t.Fatalf("unexpected child outcome: %+v", settledChild.State.Outcome)
		}
		w.open("other")
	})

	t.Run("resumes at the next pass when it waits on nothing", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn(nil, durable.JoinPolicyFailFast), nil
				})
			},
		})
		if got := thsrOutcomeOf(t, opened.harness, thsrStart(t, opened.root, w, "parent")); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		if got := w.snapshotLog(); !slices.Equal(got, []string{"run:parent", "resume:parent"}) {
			t.Fatalf("log = %v", got)
		}
	})

	t.Run("keeps running phases after spawning and waits on subsets in sequence", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var ids []durable.TaskId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					var spawned []durable.TaskId
					for _, name := range []string{"a", "b", "c"} {
						id, err := thsrSpawn(tx, w, running.Id, name)
						if err != nil {
							return nil, err
						}
						spawned = append(spawned, id)
					}
					mu.Lock()
					ids = spawned
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 0}}, nil
				})
			},
			resume: func(rt thsrRuntime, ctx chord.Context, round int) error {
				w.pushLog(fmt.Sprintf("round:%d", round))
				mu.Lock()
				cur := slices.Clone(ids)
				mu.Unlock()
				if round == 0 {
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
						return thsrWaitOn([]durable.TaskId{cur[0]}, durable.JoinPolicyAllSettled, 1), nil
					})
				}
				if round == 1 {
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
						return thsrWaitOn([]durable.TaskId{cur[1], cur[2]}, durable.JoinPolicyFailFast, 2), nil
					})
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrEnd(thsrEndCompleted, "parent"), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool { return w.hasLog("round:0") })
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		w.open("b")
		mu.Lock()
		bId := ids[1]
		mu.Unlock()
		if !testkit.SettledChannel(thsrAsyncWaitForTask(opened.harness, bId)) {
			t.Fatalf("expected b waiter settled")
		}
		if st := thsrState(t, opened.harness, parent).Status; st != durable.TaskStateWaiting {
			t.Fatalf("expected parent waiting, got %s", st)
		}
		w.open("a")
		thsrUntilBool(t, func() bool { return w.hasLog("round:1") })
		w.open("c")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		rounds := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "round:") })
		if !slices.Equal(rounds, []string{"round:0", "round:1", "round:2"}) {
			t.Fatalf("rounds = %v", rounds)
		}
	})
}

func TestHarnessStructured_Completing(t *testing.T) {
	t.Run("holds a finished task until its owned work drains; waiters and task documents wait for the final commit", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var child durable.TaskId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					doc, err := tx.Doc(thsrTaskNotes, running.Id)
					if err != nil {
						return nil, err
					}
					doc.Value.Set("text", "notes")
					c, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = c
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		st := thsrState(t, opened.harness, parent)
		if resVal, ok := st.Outcome.Result.Get(); st.Status != durable.TaskStateCompleting || st.Outcome == nil || st.Outcome.Status != durable.TaskOutcomeCompleted ||
			!ok || resVal != "parent" {
			t.Fatalf("unexpected completing state: %+v", st)
		}
		waiterDone := thsrAsyncWaitForTask(opened.harness, parent)
		if testkit.SettledChannel(waiterDone) {
			t.Fatalf("expected waiter pending")
		}
		snap, err := opened.harness.Snapshot(nil, thsrTaskNotes, parent)
		textVal, _ := snap.Get("text")
		if err != nil || snap == nil || textVal != "notes" {
			t.Fatalf("unexpected task notes snapshot: %v err=%v", snap, err)
		}
		insp, err := opened.harness.Inspect(nil)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		var foundState *durable.TaskInspectionState
		for _, item := range insp.Tasks {
			if item.Record.Id == parent {
				cp := item.State
				foundState = &cp
				break
			}
		}
		if foundState == nil || foundState.Kind != durable.TaskInspectionCompleting {
			t.Fatalf("unexpected inspection state: %+v", foundState)
		}
		if testkit.SettledChannel(thsrAsyncWaitForIdle(opened.root)) {
			t.Fatalf("expected root WaitForIdle pending")
		}
		w.open("child")
		settledParent, err := opened.harness.WaitForTask(nil, parent)
		if resVal, ok := settledParent.State.Outcome.Result.Get(); err != nil || settledParent.State.Outcome == nil || settledParent.State.Outcome.Status != durable.TaskOutcomeCompleted ||
			!ok || resVal != "parent" {
			t.Fatalf("settledParent = %+v err=%v", settledParent, err)
		}
		<-waiterDone
		snapAfter, err := opened.harness.Snapshot(nil, thsrTaskNotes, parent)
		if err != nil || snapAfter != nil {
			t.Fatalf("expected retired TaskNotes snapshot == nil, got %v err=%v", snapAfter, err)
		}
		if err := opened.root.WaitForIdle(nil); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		mu.Lock()
		hasChild := child != 0
		mu.Unlock()
		if !hasChild {
			t.Fatalf("expected child set")
		}
	})

	t.Run("keeps holding for ordinary work created during the hold", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var convId durable.ConversationId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					convId = conv.Id
					mu.Unlock()
					opts := thsrOwnConv
					opts.ConversationId = &conv.Id
					if _, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "first"}, opts); err != nil {
						return nil, err
					}
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		mu.Lock()
		cid := convId
		mu.Unlock()
		childConv, err := opened.harness.Conversation(cid, nil)
		if err != nil || childConv == nil {
			t.Fatalf("Conversation: %v %v", childConv, err)
		}
		thsrStart(t, childConv, w, "second")
		w.open("first")
		thsrUntilBool(t, func() bool { return w.hasLog("run:second") })
		if got := thsrState(t, opened.harness, parent).Status; got != durable.TaskStateCompleting {
			t.Fatalf("expected parent still completing, got %s", got)
		}
		w.open("second")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
	})

	for _, tc := range []struct {
		label  string
		ending thsrEnding
		want   durable.TaskOutcomeStatus
	}{
		{"a held failure", thsrEndFailed, durable.TaskOutcomeFailed},
		{"a held scheduler fault", thsrEndThrow, durable.TaskOutcomeFaulted},
	} {
		t.Run("aborts the work below "+tc.label+", then finishes with the held outcome", func(t *testing.T) {
			w := thsrNewWorld()
			opened := thsrOpenNodes(t, w, nil)
			defer func() { _ = opened.harness.Close(nil) }()

			var mu sync.Mutex
			var child durable.TaskId
			w.script("parent", thsrBehavior{
				run: func(rt thsrRuntime, ctx chord.Context) error {
					return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
						c, err := thsrSpawn(tx, w, running.Id, "child")
						if err != nil {
							return nil, err
						}
						mu.Lock()
						child = c
						mu.Unlock()
						return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
					})
				},
				resume: func(rt thsrRuntime, ctx chord.Context, _ int) error {
					if tc.ending == thsrEndThrow {
						return errors.New("parent threw")
					}
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
						return thsrEnd(thsrEndFailed, "parent"), nil
					})
				},
			})
			parent := thsrStart(t, opened.root, w, "parent")
			thsrUntilBool(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return child != 0
			})
			mu.Lock()
			childId := child
			mu.Unlock()
			if got := thsrOutcomeOf(t, opened.harness, childId); got != durable.TaskOutcomeAborted {
				t.Fatalf("child outcome = %s", got)
			}
			if got := thsrOutcomeOf(t, opened.harness, parent); got != tc.want {
				t.Fatalf("parent outcome = %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("only marks a completing task when aborted: the work below is aborted, the held outcome stays", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var child durable.TaskId
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					c, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = c
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		if res, err := opened.harness.AbortTask(nil, parent); err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask(parent) = %s, %v", res, err)
		}
		mu.Lock()
		childId := child
		mu.Unlock()
		if got := thsrOutcomeOf(t, opened.harness, childId); got != durable.TaskOutcomeAborted {
			t.Fatalf("child outcome = %s", got)
		}
		settledParent, err := opened.harness.WaitForTask(nil, parent)
		if resVal, _ := settledParent.State.Outcome.Result.Get(); err != nil || settledParent.State.Outcome == nil || settledParent.State.Outcome.Status != durable.TaskOutcomeCompleted ||
			resVal != "parent" || !settledParent.AbortRequested {
			t.Fatalf("unexpected settledParent: %+v err=%v", settledParent, err)
		}
		if w.hasLog("abort:parent") {
			t.Fatalf("did not expect abort:parent in log: %v", w.snapshotLog())
		}
		if res, err := opened.harness.AbortTask(nil, parent); err != nil || res != durable.TaskAbortTerminal {
			t.Fatalf("second AbortTask = %s, %v", res, err)
		}
	})
}

func TestHarnessStructured_AbortOrder(t *testing.T) {
	chain := func(w *thsrWorld, harnessFn func() durable.Harness, name, child string) func() (durable.TaskId, bool) {
		var mu sync.Mutex
		var foundId durable.TaskId
		var hasId bool
		w.script(name, thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					id, err := thsrSpawn(tx, w, running.Id, child)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					foundId = id
					hasId = true
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{id}, durable.JoinPolicyAllSettled), nil
				})
			},
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				mu.Lock()
				cid := foundId
				mu.Unlock()
				rec, err := harnessFn().GetTask(nil, cid)
				if err != nil || rec == nil {
					return fmt.Errorf("get child %d: %v", cid, err)
				}
				w.pushLog(fmt.Sprintf("%s saw %s", name, rec.State.Status))
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		return func() (durable.TaskId, bool) {
			mu.Lock()
			defer mu.Unlock()
			return foundId, hasId
		}
	}

	t.Run("runs abort handlers bottom-up across three levels, each after the level below is terminal", func(t *testing.T) {
		w := thsrNewWorld()
		var hMu sync.Mutex
		var hRef durable.Harness
		getH := func() durable.Harness {
			hMu.Lock()
			defer hMu.Unlock()
			return hRef
		}
		getB := chain(w, getH, "a", "b")
		getC := chain(w, getH, "b", "c")
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()
		hMu.Lock()
		hRef = opened.harness
		hMu.Unlock()

		a := thsrStart(t, opened.root, w, "a")
		thsrUntilBool(t, func() bool { return w.hasLog("run:c") })
		thsrUntil(t, func() (bool, error) {
			cId, ok := getC()
			if !ok {
				return false, nil
			}
			return thsrState(t, opened.harness, cId).Status == durable.TaskStateRunning, nil
		})
		if res, err := opened.harness.AbortTask(nil, a); err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask(a) = %s, %v", res, err)
		}
		if got := thsrOutcomeOf(t, opened.harness, a); got != durable.TaskOutcomeAborted {
			t.Fatalf("a outcome = %s", got)
		}
		got := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") || strings.Contains(s, " saw ") })
		want := []string{"abort:c", "abort:b", "b saw terminal", "abort:a", "a saw terminal"}
		if !slices.Equal(got, want) {
			t.Fatalf("log = %v, want %v", got, want)
		}
		if _, ok := getB(); !ok {
			t.Fatalf("expected b id defined")
		}
	})

	t.Run("does not wait for a task it waits on but does not own", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		other := thsrStart(t, opened.root, w, "other")
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn([]durable.TaskId{other}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		if _, err := opened.harness.AbortTask(nil, parent); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeAborted {
			t.Fatalf("parent outcome = %s", got)
		}
		if got := thsrState(t, opened.harness, other).Status; got != durable.TaskStateRunning {
			t.Fatalf("other status = %s", got)
		}
		w.open("other")
	})

	t.Run("reports an abort-marked task as waiting for its live owned work", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var child durable.TaskId
		w.script("child", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.child").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		w.script("parent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					c, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = c
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{c}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		if _, err := opened.harness.AbortTask(nil, parent); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		thsrUntilBool(t, func() bool { return w.hasLog("abort:child") })
		insp, err := opened.harness.Inspect(nil)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		var found *durable.TaskInspectionState
		for _, item := range insp.Tasks {
			if item.Record.Id == parent {
				cp := item.State
				found = &cp
				break
			}
		}
		mu.Lock()
		childId := child
		mu.Unlock()
		if found == nil || found.Kind != durable.TaskInspectionWaiting || !slices.Equal(found.On, []durable.TaskId{childId}) {
			t.Fatalf("unexpected inspected state: %+v", found)
		}
		w.open("abort.child")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeAborted {
			t.Fatalf("parent outcome = %s", got)
		}
	})

	t.Run("faults an abort handler that tries to wait", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("parent", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn(nil, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool { return w.hasLog("run:parent") })
		if _, err := opened.harness.AbortTask(nil, parent); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settledParent, err := opened.harness.WaitForTask(nil, parent)
		if err != nil || settledParent.State.Outcome == nil || settledParent.State.Outcome.Status != durable.TaskOutcomeFaulted ||
			settledParent.State.Outcome.Error == nil || !strings.Contains(settledParent.State.Outcome.Error.Message, "cannot wait") {
			t.Fatalf("unexpected settledParent: %+v err=%v", settledParent, err)
		}
	})

	t.Run("orphans a blocked task only after its owned work drained", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		res, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			owner, err := tx.CreateTask(thsrUnregistered, thsrNodeInput{Name: "owner"}, thsrOwnConv)
			if err != nil {
				return nil, err
			}
			child, err := thsrSpawn(tx, w, owner, "child")
			if err != nil {
				return nil, err
			}
			return [2]durable.TaskId{owner, child}, nil
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		pair := res.([2]durable.TaskId)
		owner, child := pair[0], pair[1]
		thsrUntilBool(t, func() bool { return w.hasLog("run:child") })
		if res, err := opened.harness.AbortTask(nil, owner); err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask(owner) = %s, %v", res, err)
		}
		if got := thsrOutcomeOf(t, opened.harness, child); got != durable.TaskOutcomeAborted {
			t.Fatalf("child outcome = %s", got)
		}
		settledOwner, err := opened.harness.WaitForTask(nil, owner)
		if err != nil || settledOwner.State.Outcome == nil || settledOwner.State.Outcome.Status != durable.TaskOutcomeOrphaned ||
			settledOwner.State.Outcome.Reason == nil || *settledOwner.State.Outcome.Reason != "missing_task" {
			t.Fatalf("unexpected settledOwner: %+v err=%v", settledOwner, err)
		}
	})

	t.Run("cascades through task and conversation edges, bottom-up", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var b, x durable.TaskId
		w.script("a", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					bId, err := thsrSpawn(tx, w, running.Id, "b")
					if err != nil {
						return nil, err
					}
					mu.Lock()
					b = bId
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{bId}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		w.script("b", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					opts := thsrOwnConv
					opts.ConversationId = &conv.Id
					xId, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "x"}, opts)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					x = xId
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
			resume: func(rt thsrRuntime, _ chord.Context, _ int) error {
				return testkit.Aborted(rt.Signal())
			},
		})
		a := thsrStart(t, opened.root, w, "a")
		thsrUntilBool(t, func() bool { return w.hasLog("run:x") })
		if _, err := opened.harness.AbortTask(nil, a); err != nil {
			t.Fatalf("AbortTask(a): %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, a); got != durable.TaskOutcomeAborted {
			t.Fatalf("a outcome = %s", got)
		}
		mu.Lock()
		xId := x
		_ = b
		mu.Unlock()
		if got := thsrOutcomeOf(t, opened.harness, xId); got != durable.TaskOutcomeAborted {
			t.Fatalf("x outcome = %s", got)
		}
		aborts := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") })
		if !slices.Equal(aborts, []string{"abort:x", "abort:b", "abort:a"}) {
			t.Fatalf("aborts = %v", aborts)
		}
	})
}

func TestHarnessStructured_Boundaries(t *testing.T) {
	t.Run("keeps background work through Conversation.abort(); { background: true } aborts it and waits", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		foreground := thsrStart(t, opened.root, w, "foreground")
		var mu sync.Mutex
		var child, below durable.TaskId
		w.script("background", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					cId, err := thsrSpawn(tx, w, running.Id, "child")
					if err != nil {
						return nil, err
					}
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					opts := thsrOwnConv
					opts.ConversationId = &conv.Id
					bId, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "below"}, opts)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = cId
					below = bId
					mu.Unlock()
					return thsrWaitOn([]durable.TaskId{cId}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		bgOpts := thsrOwnConv
		bgOpts.Background = thsrBoolPtr(true)
		bg := thsrStart(t, opened.root, w, "background", bgOpts)
		thsrUntilBool(t, func() bool { return w.hasLog("run:below") && w.hasLog("run:child") })
		if err := opened.root.Abort(nil); err != nil {
			t.Fatalf("root.Abort: %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, foreground); got != durable.TaskOutcomeAborted {
			t.Fatalf("foreground outcome = %s", got)
		}
		mu.Lock()
		childId, belowId := child, below
		mu.Unlock()
		for _, id := range []durable.TaskId{bg, childId, belowId} {
			if st := thsrState(t, opened.harness, id).Status; st == durable.TaskStateTerminal {
				t.Fatalf("expected task %d non-terminal, got %s", id, st)
			}
		}
		if err := opened.root.Abort(nil, durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatalf("root.Abort(background): %v", err)
		}
		for _, id := range []durable.TaskId{bg, childId, belowId} {
			if st := thsrState(t, opened.harness, id).Status; st != durable.TaskStateTerminal {
				t.Fatalf("expected task %d terminal, got %s", id, st)
			}
		}
	})

	t.Run("never cascades from a terminal owner: an aborted subagent's conversation runs new work normally", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		var mu sync.Mutex
		var convId durable.ConversationId
		w.script("agent", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					convId = conv.Id
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
			resume: func(rt thsrRuntime, _ chord.Context, _ int) error {
				return testkit.Aborted(rt.Signal())
			},
		})
		agent := thsrStart(t, opened.root, w, "agent")
		thsrUntilBool(t, func() bool { return w.hasLog("resume:agent") })
		if _, err := opened.harness.AbortTask(nil, agent); err != nil {
			t.Fatalf("AbortTask(agent): %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, agent); got != durable.TaskOutcomeAborted {
			t.Fatalf("agent outcome = %s", got)
		}
		mu.Lock()
		cid := convId
		mu.Unlock()
		childConv, err := opened.harness.Conversation(cid, nil)
		if err != nil || childConv == nil {
			t.Fatalf("Conversation: %v %v", childConv, err)
		}
		question := thsrStart(t, childConv, w, "question")
		w.open("question")
		if got := thsrOutcomeOf(t, opened.harness, question); got != durable.TaskOutcomeCompleted {
			t.Fatalf("question outcome = %s", got)
		}
	})
}

type thsrSeeded struct {
	parent   durable.TaskId
	children []durable.TaskId
}

func thsrSeed(
	t *testing.T,
	path string,
	w *thsrWorld,
	children []string,
	edit func(set func(id durable.TaskId, patch func(*durable.TaskRecord)) error, seeded thsrSeeded) error,
) thsrSeeded {
	t.Helper()
	st, err := sqlite.OpenNodeSqliteStorage(path)
	if err != nil {
		t.Fatalf("OpenNodeSqliteStorage: %v", err)
	}
	session := durable.NewSession(st)
	var seeded thsrSeeded
	if err := session.Commit(nil, func(tx durable.Tx) error {
		root, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		opts := thsrOwnConv
		opts.ConversationId = &root.Id
		parent, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "parent"}, opts)
		if err != nil {
			return err
		}
		var ids []durable.TaskId
		for _, name := range children {
			c, err := thsrSpawn(tx, w, parent, name)
			if err != nil {
				return err
			}
			ids = append(ids, c)
		}
		seeded = thsrSeeded{parent: parent, children: ids}
		return nil
	}); err != nil {
		t.Fatalf("seed Commit: %v", err)
	}
	setFn := func(id durable.TaskId, patch func(*durable.TaskRecord)) error {
		return session.Commit(nil, func(tx durable.Tx) error {
			rec, err := tx.Task(id)
			if err != nil || rec == nil {
				return fmt.Errorf("tx.Task(%d): %v %v", id, rec, err)
			}
			updated := *rec
			patch(&updated)
			return tx.(*durable.Transaction).SetTask(updated)
		})
	}
	if err := edit(setFn, seeded); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := session.Close(nil); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
	return seeded
}

func thsrCompletedState(result string) durable.TaskState {
	return durable.TaskState{
		Status: durable.TaskStateTerminal,
		Outcome: &durable.TaskOutcome{
			Status: durable.TaskOutcomeCompleted,
			Result: jsonx.Some[durable.JsonValue](result),
		},
	}
}

func thsrFailedState(msg string) durable.TaskState {
	return durable.TaskState{
		Status: durable.TaskStateTerminal,
		Outcome: &durable.TaskOutcome{
			Status: durable.TaskOutcomeFailed,
			Error:  &durable.TaskOutcomeError{Message: msg},
		},
	}
}

func thsrMustSqlite(t *testing.T, path string) durable.Storage {
	t.Helper()
	st, err := sqlite.OpenNodeSqliteStorage(path)
	if err != nil {
		t.Fatalf("OpenNodeSqliteStorage(%s): %v", path, err)
	}
	return st
}

func TestHarnessStructured_Recovery(t *testing.T) {
	t.Run("resumes a parent whose awaited children finished before the crash", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		seeded := thsrSeed(t, path, w, []string{"c1"}, func(set func(durable.TaskId, func(*durable.TaskRecord)) error, s thsrSeeded) error {
			if err := set(s.children[0], func(r *durable.TaskRecord) {
				r.State = thsrCompletedState("done")
			}); err != nil {
				return err
			}
			return set(s.parent, func(r *durable.TaskRecord) {
				r.State = durable.TaskState{
					Status:     durable.TaskStateWaiting,
					Checkpoint: jsonx.ObjectOf("phase", "resume", "round", float64(1)),
					On:         s.children,
					Policy:     durable.JoinPolicyAllSettled,
				}
			})
		})
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		if got := thsrOutcomeOf(t, opened.harness, seeded.parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		if got := w.snapshotLog(); !slices.Equal(got, []string{"resume:parent"}) {
			t.Fatalf("log = %v", got)
		}
	})

	t.Run("marks failFast siblings a crash left unmarked", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		seeded := thsrSeed(t, path, w, []string{"c1", "c2"}, func(set func(durable.TaskId, func(*durable.TaskRecord)) error, s thsrSeeded) error {
			if err := set(s.children[0], func(r *durable.TaskRecord) {
				r.State = thsrFailedState("declined")
			}); err != nil {
				return err
			}
			return set(s.parent, func(r *durable.TaskRecord) {
				r.State = durable.TaskState{
					Status:     durable.TaskStateWaiting,
					Checkpoint: jsonx.ObjectOf("phase", "resume", "round", float64(1)),
					On:         s.children,
					Policy:     durable.JoinPolicyFailFast,
				}
			})
		})
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		if got := thsrOutcomeOf(t, opened.harness, seeded.children[1]); got != durable.TaskOutcomeAborted {
			t.Fatalf("c2 outcome = %s", got)
		}
		if got := thsrOutcomeOf(t, opened.harness, seeded.parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		if w.hasLog("run:c2") {
			t.Fatalf("did not expect run:c2 in log: %v", w.snapshotLog())
		}
	})

	t.Run("finalizes a held outcome whose work drained before the crash, and keeps one whose work lives", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		held := durable.TaskState{
			Status:  durable.TaskStateCompleting,
			Outcome: &durable.TaskOutcome{Status: durable.TaskOutcomeCompleted, Result: jsonx.Some[durable.JsonValue]("parent")},
		}
		seeded := thsrSeed(t, path, w, []string{"c1"}, func(set func(durable.TaskId, func(*durable.TaskRecord)) error, s thsrSeeded) error {
			return set(s.parent, func(r *durable.TaskRecord) { r.State = held })
		})
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		thsrUntilBool(t, func() bool { return w.hasLog("run:c1") })
		st := thsrState(t, opened.harness, seeded.parent)
		if resVal, _ := st.Outcome.Result.Get(); st.Status != durable.TaskStateCompleting || st.Outcome == nil || st.Outcome.Status != durable.TaskOutcomeCompleted ||
			resVal != "parent" {
			t.Fatalf("unexpected held state: %+v", st)
		}
		_ = opened.harness.Close(nil)

		opened = thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		w.open("c1")
		if got := thsrOutcomeOf(t, opened.harness, seeded.children[0]); got != durable.TaskOutcomeCompleted {
			t.Fatalf("c1 outcome = %s", got)
		}
		if got := thsrOutcomeOf(t, opened.harness, seeded.parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		_ = opened.harness.Close(nil)

		drainedPath := filepath.Join(t.TempDir(), "drained.sqlite")
		second := thsrSeed(t, drainedPath, w, []string{"c1"}, func(set func(durable.TaskId, func(*durable.TaskRecord)) error, s thsrSeeded) error {
			if err := set(s.children[0], func(r *durable.TaskRecord) { r.State = thsrCompletedState("done") }); err != nil {
				return err
			}
			return set(s.parent, func(r *durable.TaskRecord) { r.State = held })
		})
		opened = thsrOpenNodes(t, w, thsrMustSqlite(t, drainedPath))
		defer func() { _ = opened.harness.Close(nil) }()
		if got := thsrOutcomeOf(t, opened.harness, second.parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("second parent outcome = %s", got)
		}
	})

	t.Run("resumes a bottom-up abort a crash interrupted before the cascade", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		seeded := thsrSeed(t, path, w, []string{"c1"}, func(set func(durable.TaskId, func(*durable.TaskRecord)) error, s thsrSeeded) error {
			return set(s.parent, func(r *durable.TaskRecord) {
				r.AbortRequested = true
				r.State = durable.TaskState{
					Status:     durable.TaskStateWaiting,
					Checkpoint: jsonx.ObjectOf("phase", "resume", "round", float64(1)),
					On:         s.children,
					Policy:     durable.JoinPolicyAllSettled,
				}
			})
		})
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		if got := thsrOutcomeOf(t, opened.harness, seeded.parent); got != durable.TaskOutcomeAborted {
			t.Fatalf("parent outcome = %s", got)
		}
		if got := thsrOutcomeOf(t, opened.harness, seeded.children[0]); got != durable.TaskOutcomeAborted {
			t.Fatalf("child outcome = %s", got)
		}
		aborts := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") })
		if !slices.Equal(aborts, []string{"abort:c1", "abort:parent"}) {
			t.Fatalf("aborts = %v", aborts)
		}
	})

	t.Run("reopens a checkout waiting on live payments and finishes it", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		found := thsrParentOf(w, "checkout", []string{"p1", "p2"}, durable.JoinPolicyFailFast)
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		parent := thsrStart(t, opened.root, w, "checkout")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateWaiting, nil
		})
		thsrUntilBool(t, func() bool { return w.hasLog("run:p1") && w.hasLog("run:p2") })
		_ = opened.harness.Close(nil)

		opened = thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		found.setIds(thsrState(t, opened.harness, parent).On)
		w.open("p1")
		w.open("p2")
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		want := []durable.TaskOutcomeStatus{durable.TaskOutcomeCompleted, durable.TaskOutcomeCompleted}
		if !slices.Equal(found.getOutcomes(), want) {
			t.Fatalf("outcomes = %v, want %v", found.getOutcomes(), want)
		}
	})
}

func thsrBlockingTool(name string) (<-chan struct{}, *durable.ToolRegistration) {
	started := make(chan struct{})
	var once sync.Once
	reg := durable.DefineTool(&durable.ToolRegistration{
		Name:          name,
		Description:   fmt.Sprintf("The %s tool", name),
		Parameters:    typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		ExecutionMode: durable.ToolExecutionModeSequential,
		Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("partial")
			once.Do(func() { close(started) })
			return nil, testkit.Aborted(callCtx.AbortSignal())
		},
	})
	return started, reg
}

type thsrCallSpec struct {
	name string
	id   string
}

func thsrToolCalls(calls ...thsrCallSpec) *ai.AssistantMessage {
	blocks := make(providers.FauxAssistantBlocks, len(calls))
	for i, c := range calls {
		id := c.id
		blocks[i] = providers.FauxToolCall(c.name, jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &id})
	}
	stop := ai.StopReasonToolUse
	return providers.FauxAssistantMessage(blocks, &providers.FauxAssistantMessageOptions{StopReason: &stop})
}

func thsrLiveSnapshot(t *testing.T, h durable.Harness, cid durable.ConversationId) *durable.LiveState {
	t.Helper()
	raw, err := h.Snapshot(nil, durable.LiveDoc, cid)
	if err != nil {
		t.Fatalf("Snapshot(LiveDoc): %v", err)
	}
	if raw == nil {
		return nil
	}
	var st durable.LiveState
	if err := jsonx.Decode(raw, &st); err != nil {
		t.Fatalf("decode LiveState: %v", err)
	}
	return &st
}

type thsrInvalidModels struct {
	ai.MutableModels
}

func (m *thsrInvalidModels) StreamSimple(
	ctx context.Context,
	_ *ai.Model,
	_ *ai.Context,
	_ *ai.ModelsSimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	stopPending := ai.StopReasonPending
	partial := providers.FauxAssistantMessage(
		providers.FauxAssistantBlocks{providers.FauxText("partial")},
		&providers.FauxAssistantMessageOptions{StopReason: &stopPending},
	)
	go func() {
		stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		final := providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxText("final")},
			nil,
		)
		final.Usage.Cost.Input = math.NaN()
		stream.End(final)
	}()
	return stream
}

func thsrListenEvents(t *testing.T, h durable.Harness, conv durable.Conversation) (durable.AgentEventStream, func() []durable.AgentEvent) {
	t.Helper()
	stream, err := durable.WatchEvents(h, conv.Id(), nil)
	if err != nil {
		t.Fatalf("WatchEvents: %v", err)
	}
	var mu sync.Mutex
	var events []durable.AgentEvent
	if err := stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
		mu.Lock()
		events = append(events, batch...)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("stream.Start: %v", err)
	}
	return stream, func() []durable.AgentEvent {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(events)
	}
}

func thsrEventLabels(events []durable.AgentEvent) []string {
	var out []string
	for _, ev := range events {
		switch e := ev.(type) {
		case *durable.ToolExecutionEndEvent:
			out = append(out, fmt.Sprintf("end:%s:%v", e.ToolCallId, e.Entry != nil))
		case *durable.MessageEndEvent:
			if len(e.Entry.Model) > 0 {
				if tr, ok := e.Entry.Model[0].(*ai.ToolResultMessage); ok {
					out = append(out, "result:"+tr.ToolCallId)
					continue
				}
				out = append(out, "message:"+e.Entry.Model[0].GetRole())
			} else {
				out = append(out, "message:")
			}
		}
	}
	return out
}

func thsrSubmitInput(t *testing.T, conv durable.Conversation, text string) durable.Submission {
	t.Helper()
	sub, err := conv.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &text},
	}, nil)
	if err != nil {
		t.Fatalf("Submit(%s): %v", text, err)
	}
	return sub
}

func TestHarnessStructured_ToolRounds(t *testing.T) {
	t.Run("owns its tool tasks, waits for them, and hands the run to a conversation-owned generation", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "noop",
			Description: "Does nothing",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{Content: ai.ContentList{}}, nil
			},
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"noop", "c1"}, thsrCallSpec{"noop", "c2"})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub := thsrSubmitInput(t, root, "go")
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub.Wait = %+v err=%v", settled, err)
		}
		var tasks []durable.TaskRecord
		cid := root.Id()
		if err := harness.Commit(nil, func(tx durable.Tx) error {
			page, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 20, nil)
			if err != nil {
				return err
			}
			tasks = page.Items
			return nil
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		var gens, tools []durable.TaskRecord
		for _, task := range tasks {
			switch task.Kind {
			case "pi.generation":
				gens = append(gens, task)
			case "pi.tool":
				tools = append(tools, task)
			}
		}
		if len(gens) != 2 || len(tools) != 2 {
			t.Fatalf("expected 2 gens and 2 tools, got %d gens %d tools", len(gens), len(tools))
		}
		first, second := gens[0], gens[1]
		for _, tool := range tools {
			if tool.Owner == nil || *tool.Owner != first.Id {
				t.Fatalf("expected tool owner %d, got %v", first.Id, tool.Owner)
			}
		}
		if first.Owner != nil || second.Owner != nil {
			t.Fatalf("expected nil owners on generations, got %v %v", first.Owner, second.Owner)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var assistant durable.EntryRecord
		foundAssistant := false
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				assistant = e
				foundAssistant = true
				break
			}
		}
		if !foundAssistant {
			t.Fatalf("missing pi.assistant entry")
		}
		resVal, ok := first.State.Outcome.Result.Get()
		if first.State.Status != durable.TaskStateTerminal || first.State.Outcome == nil ||
			first.State.Outcome.Status != durable.TaskOutcomeCompleted || !ok {
			t.Fatalf("unexpected first gen state: %+v", first.State)
		}
		var genRes durable.GenerationResult
		if err := jsonx.Decode(resVal, &genRes); err != nil || genRes.EntryId != assistant.Id {
			t.Fatalf("unexpected genRes: %+v want EntryId=%d err=%v", genRes, assistant.Id, err)
		}
	})

	t.Run("aborts a parallel round with its generation: tools first, then the generation, with every result written", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		oneStarted, oneReg := thsrBlockingTool("one")
		twoStarted, twoReg := thsrBlockingTool("two")
		oneReg.ExecutionMode = durable.ToolExecutionModeParallel
		twoReg.ExecutionMode = durable.ToolExecutionModeParallel
		if _, err := testkit.AddTool(setup.Registry, oneReg); err != nil {
			t.Fatalf("AddTool(one): %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, twoReg); err != nil {
			t.Fatalf("AddTool(two): %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"one", "c1"}, thsrCallSpec{"two", "c2"})},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub := thsrSubmitInput(t, root, "go")
		<-oneStarted
		<-twoStarted
		live := thsrLiveSnapshot(t, harness, root.Id())
		genId := live.Run.TaskId
		if _, err := harness.AbortTask(nil, genId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("sub.Wait = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var callIds []string
		for _, e := range entries {
			if durable.ToolResultEntry.Is(&e) {
				callIds = append(callIds, e.Model[0].(*ai.ToolResultMessage).ToolCallId)
			}
		}
		slices.Sort(callIds)
		if !slices.Equal(callIds, []string{"c1", "c2"}) {
			t.Fatalf("callIds = %v", callIds)
		}
		snap, err := harness.Snapshot(nil, durable.LiveDoc, root.Id())
		if err != nil || snap.Len() != 0 {
			t.Fatalf("expected empty LiveDoc, got %v err=%v", snap, err)
		}
		if got := thsrOutcomeOf(t, harness, genId); got != durable.TaskOutcomeAborted {
			t.Fatalf("gen outcome = %s", got)
		}
	})

	t.Run("answers the unstarted calls of an aborted sequential round with aborted results, in call order", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		oneStarted, oneReg := thsrBlockingTool("one")
		_, twoReg := thsrBlockingTool("two")
		if _, err := testkit.AddTool(setup.Registry, oneReg); err != nil {
			t.Fatalf("AddTool(one): %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, twoReg); err != nil {
			t.Fatalf("AddTool(two): %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"one", "c1"}, thsrCallSpec{"two", "c2"}, thsrCallSpec{"two", "c3"})},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub := thsrSubmitInput(t, root, "go")
		<-oneStarted
		live := thsrLiveSnapshot(t, harness, root.Id())
		hasIds := make([]bool, len(live.Tools))
		for i, slot := range live.Tools {
			hasIds[i] = slot.TaskId != nil
		}
		if !slices.Equal(hasIds, []bool{true, false, false}) {
			t.Fatalf("slot taskId presence = %v", hasIds)
		}
		genId := live.Run.TaskId
		if _, err := harness.AbortTask(nil, genId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("sub.Wait = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var results []*ai.ToolResultMessage
		for _, e := range entries {
			if durable.ToolResultEntry.Is(&e) {
				results = append(results, e.Model[0].(*ai.ToolResultMessage))
			}
		}
		if len(results) != 3 ||
			results[0].ToolCallId != "c1" || results[1].ToolCallId != "c2" || results[2].ToolCallId != "c3" ||
			!results[0].IsError || !results[1].IsError || !results[2].IsError {
			t.Fatalf("unexpected tool results: %+v", results)
		}
		if len(results[1].Content) != 1 {
			t.Fatalf("expected 1 content block on unstarted result, got %+v", results[1].Content)
		}
		if tc, ok := results[1].Content[0].(*ai.TextContent); !ok || tc.Text != "<harness>\n[error] Tool two was aborted\n</harness>" {
			t.Fatalf("unexpected unstarted content: %+v", results[1].Content[0])
		}
		var toolCount int
		cid := root.Id()
		kind := "pi.tool"
		if err := harness.Commit(nil, func(tx durable.Tx) error {
			page, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &cid, Kind: &kind}, 20, nil)
			if err != nil {
				return err
			}
			toolCount = len(page.Items)
			return nil
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if toolCount != 1 {
			t.Fatalf("expected 1 pi.tool task, got %d", toolCount)
		}
	})

	t.Run("ends a turn at the generation's hold, before its successor's turn starts", func(t *testing.T) {
		w := thsrNewWorld()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddTask(setup.Registry, w.nodeTask()); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "noop",
			Description: "Does nothing",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{Content: ai.ContentList{}}, nil
			},
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		var hMu sync.Mutex
		var openedH durable.Harness
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterTools: func(_ durable.EntryId, _ []durable.EntryId, api durable.HookApi, callCtx chord.Context) error {
				hMu.Lock()
				h := openedH
				hMu.Unlock()
				return h.Commit(callCtx, func(tx durable.Tx) error {
					_, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "hooked"}, thsrOwned(api.TaskId()))
					return err
				})
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"noop", "c1"})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()
		hMu.Lock()
		openedH = harness
		hMu.Unlock()

		stream, getEvents := thsrListenEvents(t, harness, root)
		sub := thsrSubmitInput(t, root, "go")
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		var first durable.TaskRecord
		genKind := "pi.generation"
		if err := harness.Commit(nil, func(tx durable.Tx) error {
			page, err := tx.ScanTasks(durable.TaskQuery{Kind: &genKind}, 1, nil)
			if err != nil {
				return err
			}
			first = page.Items[0]
			return nil
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if st := thsrState(t, harness, first.Id).Status; st != durable.TaskStateCompleting {
			t.Fatalf("expected first gen completing, got %s", st)
		}
		turns := func() []string {
			var out []string
			for _, ev := range getEvents() {
				if strings.HasPrefix(ev.AgentEventType(), "turn_") {
					out = append(out, ev.AgentEventType())
				}
			}
			return out
		}
		thsrUntilBool(t, func() bool { return len(turns()) == 4 })
		if !slices.Equal(turns(), []string{"turn_start", "turn_end", "turn_start", "turn_end"}) {
			t.Fatalf("turns = %v", turns())
		}
		w.open("hooked")
		if got := thsrOutcomeOf(t, harness, first.Id); got != durable.TaskOutcomeCompleted {
			t.Fatalf("first gen outcome = %s", got)
		}
		thsrUntilBool(t, func() bool { return len(turns()) == 4 })
		stream.Stop()
	})

	t.Run("keeps run control with a faulted generation until its owned work drains, and retries a rejected final commit", func(t *testing.T) {
		w := thsrNewWorld()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Models = &thsrInvalidModels{MutableModels: setup.Models}
		if _, err := testkit.AddTask(setup.Registry, w.nodeTask()); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		var hMu sync.Mutex
		var openedH durable.Harness
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			BeforeRequest: func(_ durable.GenerationRequest, api durable.HookApi, callCtx chord.Context) (*durable.GenerationRequest, error) {
				hMu.Lock()
				h := openedH
				hMu.Unlock()
				if err := h.Commit(callCtx, func(tx durable.Tx) error {
					_, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "hooked"}, thsrOwned(api.TaskId()))
					return err
				}); err != nil {
					return nil, err
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		w.script("hooked", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.hooked").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		rejecting := &thsrRejectingStorage{Storage: storage.NewMemoryStorage()}
		harness, root, err := testkit.OpenChat(rejecting, setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()
		hMu.Lock()
		openedH = harness
		hMu.Unlock()

		sub := thsrSubmitInput(t, root, "hi")
		thsrUntilBool(t, func() bool {
			live := thsrLiveSnapshot(t, harness, root.Id())
			if live == nil || live.Run == nil {
				return false
			}
			rec, err := harness.GetTask(nil, live.Run.TaskId)
			return err == nil && rec != nil && rec.State.Status == durable.TaskStateCompleting
		})
		live := thsrLiveSnapshot(t, harness, root.Id())
		genId := live.Run.TaskId
		genState := thsrState(t, harness, genId)
		if genState.Status != durable.TaskStateCompleting || genState.Outcome == nil || genState.Outcome.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("unexpected genState: %+v", genState)
		}
		waitSig := make(chan struct{})
		waitDone := make(chan *durable.SettledSubmissionRecord, 1)
		go func() {
			defer close(waitSig)
			rec, _ := sub.Wait(nil)
			waitDone <- rec
		}()
		if testkit.SettledChannel(waitSig) {
			t.Fatalf("expected submission still unsettled")
		}
		if gotLive := thsrLiveSnapshot(t, harness, root.Id()); gotLive.Run == nil || gotLive.Run.TaskId != genId {
			t.Fatalf("expected run.taskId=%d, got %+v", genId, gotLive.Run)
		}
		thsrUntilBool(t, func() bool { return w.hasLog("abort:hooked") })
		rejecting.setReject(genId)
		w.open("abort.hooked")
		thsrUntilBool(t, func() bool {
			for _, rep := range setup.Reports() {
				var sr *durable.StorageRejected
				if errors.As(rep, &sr) {
					return true
				}
			}
			return false
		})
		if gotLive := thsrLiveSnapshot(t, harness, root.Id()); gotLive == nil || gotLive.Run == nil || gotLive.Run.TaskId != genId {
			t.Fatalf("expected run.taskId still %d after rejected commit, got %+v", genId, gotLive)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		kinds := make([]string, len(entries))
		for i, e := range entries {
			kinds[i] = e.Kind
		}
		if !slices.Equal(kinds, []string{"pi.user"}) {
			t.Fatalf("expected [pi.user], got %v", kinds)
		}
		if testkit.SettledChannel(waitSig) {
			t.Fatalf("expected submission still unsettled after rejected commit")
		}
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatalf("append note: %v", err)
		}
		settled := <-waitDone
		if settled == nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "faulted" {
			t.Fatalf("unexpected settled submission: %+v", settled)
		}
		if snap, err := harness.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || snap.Len() != 0 {
			t.Fatalf("expected empty LiveDoc, got %v err=%v", snap, err)
		}
		entriesAfter, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries after: %v", err)
		}
		kindsAfter := make([]string, len(entriesAfter))
		for i, e := range entriesAfter {
			kindsAfter[i] = e.Kind
		}
		if !slices.Equal(kindsAfter, []string{"pi.user", "note", "pi.assistant"}) {
			t.Fatalf("kindsAfter = %v", kindsAfter)
		}
	})
}

type thsrVersionedInput struct {
	On []durable.TaskId `json:"on"`
}

type thsrVersionedCP struct {
	Phase string `json:"phase"`
}

func thsrVersioned(version int) *durable.Task[thsrVersionedInput, thsrVersionedCP, string, struct{}] {
	def := durable.TaskDefinition[thsrVersionedInput, thsrVersionedCP, string, struct{}]{
		Name:    "test.versioned",
		Version: version,
		Initial: func(_ thsrVersionedInput) thsrVersionedCP { return thsrVersionedCP{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[thsrVersionedInput, thsrVersionedCP, string, struct{}]{
			"wait": func(ctx chord.Context, task durable.RunningTask[thsrVersionedInput, thsrVersionedCP], rt *durable.TaskRuntime[thsrVersionedInput, thsrVersionedCP, string, struct{}]) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
					return &durable.NextTaskState[thsrVersionedCP, string]{
						Status:     durable.TaskStateWaiting,
						Checkpoint: thsrVersionedCP{Phase: "resume"},
						On:         task.Input.On,
						Policy:     durable.JoinPolicyAllSettled,
					}, nil
				})
			},
			"resume": func(ctx chord.Context, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP], rt *durable.TaskRuntime[thsrVersionedInput, thsrVersionedCP, string, struct{}]) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
					return &durable.NextTaskState[thsrVersionedCP, string]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: "v1"},
					}, nil
				})
			},
			"migrated": func(ctx chord.Context, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP], rt *durable.TaskRuntime[thsrVersionedInput, thsrVersionedCP, string, struct{}]) error {
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
					return &durable.NextTaskState[thsrVersionedCP, string]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: "v2"},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP], rt *durable.TaskRuntime[thsrVersionedInput, thsrVersionedCP, string, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrVersionedInput, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
				return &durable.NextTaskState[thsrVersionedCP, string]{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	}
	if version == 2 {
		def.Migrate = func(rawIn durable.JsonValue, _ durable.JsonValue, _ int) (thsrVersionedInput, thsrVersionedCP, error) {
			var in thsrVersionedInput
			if err := jsonx.Decode(rawIn, &in); err != nil {
				return thsrVersionedInput{}, thsrVersionedCP{}, err
			}
			return in, thsrVersionedCP{Phase: "migrated"}, nil
		}
	}
	return durable.DefineTask(def)
}

func TestHarnessStructured_DefinitionsAndWaits(t *testing.T) {
	t.Run("keeps a waiting task blocked without its definition and resumes it migrated under a newer one", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		reg, err := testkit.AddTask(opened.registry, thsrVersioned(1))
		if err != nil {
			t.Fatalf("AddTask(v1): %v", err)
		}
		other := thsrStart(t, opened.root, w, "other")
		waiterRes, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(thsrVersioned(1), thsrVersionedInput{On: []durable.TaskId{other}}, thsrOwnConv)
		})
		if err != nil {
			t.Fatalf("CreateTask(waiter): %v", err)
		}
		waiter := waiterRes.(durable.TaskId)
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, waiter).Status == durable.TaskStateWaiting, nil
		})
		reg.Dispose()
		w.open("other")
		if _, err := opened.harness.WaitForTask(nil, other); err != nil {
			t.Fatalf("WaitForTask(other): %v", err)
		}
		insp, err := opened.harness.Inspect(nil)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		var waiterInsp *durable.TaskInspectionState
		for _, item := range insp.Tasks {
			if item.Record.Id == waiter {
				cp := item.State
				waiterInsp = &cp
				break
			}
		}
		if waiterInsp == nil || waiterInsp.Kind != durable.TaskInspectionBlocked || waiterInsp.Reason != durable.TaskBlockedMissingTask {
			t.Fatalf("unexpected waiterInsp: %+v", waiterInsp)
		}
		if st := thsrState(t, opened.harness, waiter).Status; st != durable.TaskStateWaiting {
			t.Fatalf("expected waiter waiting, got %s", st)
		}
		if _, err := testkit.AddTask(opened.registry, thsrVersioned(2)); err != nil {
			t.Fatalf("AddTask(v2): %v", err)
		}
		settled, err := opened.harness.WaitForTask(nil, waiter)
		if resVal, _ := settled.State.Outcome.Result.Get(); err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted ||
			resVal != "v2" {
			t.Fatalf("unexpected settled waiter: %+v err=%v", settled, err)
		}
	})

	t.Run("orphans an aborted waiting task without its definition at once, leaving the task it waits on running", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		reg, err := testkit.AddTask(opened.registry, thsrVersioned(1))
		if err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		other := thsrStart(t, opened.root, w, "other")
		waiterRes, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(thsrVersioned(1), thsrVersionedInput{On: []durable.TaskId{other}}, thsrOwnConv)
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		waiter := waiterRes.(durable.TaskId)
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, waiter).Status == durable.TaskStateWaiting, nil
		})
		reg.Dispose()
		res, err := opened.harness.AbortTask(nil, waiter)
		if err != nil || res != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = %s, %v", res, err)
		}
		waiterState := thsrState(t, opened.harness, waiter)
		if waiterState.Outcome == nil || waiterState.Outcome.Status != durable.TaskOutcomeOrphaned ||
			waiterState.Outcome.Reason == nil || *waiterState.Outcome.Reason != "missing_task" {
			t.Fatalf("unexpected waiter outcome: %+v", waiterState.Outcome)
		}
		if st := thsrState(t, opened.harness, other).Status; st != durable.TaskStateRunning {
			t.Fatalf("other status = %s", st)
		}
		w.open("other")
	})

	t.Run("never migrates a held outcome: a newer definition leaves it and its version alone", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		holder := func(version int) *durable.Task[*struct{}, thsrVersionedCP, string, struct{}] {
			return durable.DefineTask(durable.TaskDefinition[*struct{}, thsrVersionedCP, string, struct{}]{
				Name:    "test.holder",
				Version: version,
				Initial: func(_ *struct{}) thsrVersionedCP { return thsrVersionedCP{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[*struct{}, thsrVersionedCP, string, struct{}]{
					"run": func(ctx chord.Context, task durable.RunningTask[*struct{}, thsrVersionedCP], rt *durable.TaskRuntime[*struct{}, thsrVersionedCP, string, struct{}]) error {
						if err := rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[*struct{}, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
							if _, err := thsrSpawn(tx, w, task.Id, "child"); err != nil {
								return nil, err
							}
							return nil, nil
						}); err != nil {
							return err
						}
						return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[*struct{}, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
							return &durable.NextTaskState[thsrVersionedCP, string]{
								Status:  durable.TaskStateTerminal,
								Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: "held"},
							}, nil
						})
					},
				},
				Abort: func(ctx chord.Context, _ durable.RunningTask[*struct{}, thsrVersionedCP], rt *durable.TaskRuntime[*struct{}, thsrVersionedCP, string, struct{}]) error {
					return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[*struct{}, thsrVersionedCP]) (*durable.NextTaskState[thsrVersionedCP, string], error) {
						return &durable.NextTaskState[thsrVersionedCP, string]{
							Status:  durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted},
						}, nil
					})
				},
				Migrate: func(_ durable.JsonValue, _ durable.JsonValue, _ int) (*struct{}, thsrVersionedCP, error) {
					return nil, thsrVersionedCP{}, errors.New("never migrates")
				},
			})
		}
		if _, err := testkit.AddTask(opened.registry, holder(1)); err != nil {
			t.Fatalf("AddTask(holder v1): %v", err)
		}
		parentRes, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(holder(1), nil, thsrOwnConv)
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		parent := parentRes.(durable.TaskId)
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		if _, err := testkit.AddTask(opened.registry, holder(2)); err != nil {
			t.Fatalf("AddTask(holder v2): %v", err)
		}
		w.open("child")
		settled, err := opened.harness.WaitForTask(nil, parent)
		if resVal, _ := settled.State.Outcome.Result.Get(); err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeCompleted ||
			resVal != "held" || settled.Version != 1 {
			t.Fatalf("unexpected settled holder: %+v err=%v", settled, err)
		}
		if len(opened.reports()) != 0 {
			t.Fatalf("expected 0 reports, got %v", opened.reports())
		}
	})

	t.Run("treats a held task as live: outcomes() rejects, and a task waiting on it resumes at its final commit", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		thsrSpawnAndFinish(w, "held", "child")
		held := thsrStart(t, opened.root, w, "held")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, held).Status == durable.TaskStateCompleting, nil
		})
		var mu sync.Mutex
		var rejection string
		w.script("reader", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				_, err := rt.Outcomes(ctx, []durable.TaskId{held})
				msg := "resolved"
				if err != nil {
					msg = err.Error()
				}
				mu.Lock()
				rejection = msg
				mu.Unlock()
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return thsrWaitOn([]durable.TaskId{held}, durable.JoinPolicyAllSettled), nil
				})
			},
		})
		reader := thsrStart(t, opened.root, w, "reader")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, reader).Status == durable.TaskStateWaiting, nil
		})
		mu.Lock()
		gotRej := rejection
		mu.Unlock()
		if gotRej != fmt.Sprintf("Task %d is not terminal", held) {
			t.Fatalf("rejection = %q", gotRej)
		}
		w.open("child")
		if got := thsrOutcomeOf(t, opened.harness, reader); got != durable.TaskOutcomeCompleted {
			t.Fatalf("reader outcome = %s", got)
		}
		if !w.hasLog("resume:reader") {
			t.Fatalf("expected resume:reader in log: %v", w.snapshotLog())
		}
	})
}

func TestHarnessStructured_ConversationAbortAndBoundaries(t *testing.T) {
	t.Run("marks a held completed task with Conversation.abort(): the work below is aborted, the outcome stays", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		getChild := thsrSpawnAndFinish(w, "held", "child")
		held := thsrStart(t, opened.root, w, "held")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, held).Status == durable.TaskStateCompleting, nil
		})
		if err := opened.root.Abort(nil); err != nil {
			t.Fatalf("root.Abort: %v", err)
		}
		childId, ok := getChild()
		if !ok {
			t.Fatalf("expected child id")
		}
		if got := thsrOutcomeOf(t, opened.harness, childId); got != durable.TaskOutcomeAborted {
			t.Fatalf("child outcome = %s", got)
		}
		rec, err := opened.harness.WaitForTask(nil, held)
		if err != nil || rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeCompleted || !rec.AbortRequested {
			t.Fatalf("unexpected held record: %+v err=%v", rec, err)
		}
	})

	t.Run("marks and awaits only the background work reached when { background: true } is admitted", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("first", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.first").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		bg := thsrOwnConv
		bg.Background = thsrBoolPtr(true)
		first := thsrStart(t, opened.root, w, "first", bg)
		thsrUntilBool(t, func() bool { return w.hasLog("run:first") })

		abortErrCh := make(chan error, 1)
		go func() {
			abortErrCh <- opened.root.Abort(nil, durable.ConversationAbortOptions{Background: true})
		}()
		thsrUntil(t, func() (bool, error) {
			r, err := opened.harness.GetTask(nil, first)
			return err == nil && r != nil && r.AbortRequested, nil
		})
		later := thsrStart(t, opened.root, w, "later", bg)
		w.open("abort.first")
		if err := <-abortErrCh; err != nil {
			t.Fatalf("root.Abort: %v", err)
		}
		if st := thsrState(t, opened.harness, later).Status; st == durable.TaskStateTerminal {
			t.Fatalf("expected later non-terminal, got %s", st)
		}
		laterRec, err := opened.harness.GetTask(nil, later)
		if err != nil || laterRec.AbortRequested {
			t.Fatalf("expected later not abort-marked, got %+v err=%v", laterRec, err)
		}
		if _, err := opened.harness.AbortTask(nil, later); err != nil {
			t.Fatalf("AbortTask(later): %v", err)
		}
	})

	t.Run("stops a cascade at an unmarked background task, but not at a marked one", func(t *testing.T) {
		w := thsrNewWorld()
		opened := thsrOpenNodes(t, w, nil)
		defer func() { _ = opened.harness.Close(nil) }()

		w.script("owner", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.owner").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		type treeIds struct {
			owner, background durable.TaskId
			outer, inner      durable.ConversationId
		}
		res, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			owner, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "owner"}, thsrOwnConv)
			if err != nil {
				return nil, err
			}
			outer, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: owner},
			})
			if err != nil {
				return nil, err
			}
			bgOpts := thsrOwnConv
			bgOpts.ConversationId = &outer.Id
			bgOpts.Background = thsrBoolPtr(true)
			background, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "background"}, bgOpts)
			if err != nil {
				return nil, err
			}
			inner, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: background},
			})
			if err != nil {
				return nil, err
			}
			return treeIds{owner: owner, background: background, outer: outer.Id, inner: inner.Id}, nil
		})
		if err != nil {
			t.Fatalf("Commit tree: %v", err)
		}
		tree := res.(treeIds)
		thsrUntilBool(t, func() bool { return w.hasLog("run:owner") && w.hasLog("run:background") })
		if _, err := opened.harness.AbortTask(nil, tree.owner); err != nil {
			t.Fatalf("AbortTask(owner): %v", err)
		}
		thsrUntilBool(t, func() bool { return w.hasLog("abort:owner") })
		innerConv, err := opened.harness.Conversation(tree.inner, nil)
		if err != nil || innerConv == nil {
			t.Fatalf("Conversation(inner): %v", err)
		}
		outerConv, err := opened.harness.Conversation(tree.outer, nil)
		if err != nil || outerConv == nil {
			t.Fatalf("Conversation(outer): %v", err)
		}
		shielded := thsrStart(t, innerConv, w, "shielded")
		exposed := thsrStart(t, outerConv, w, "exposed")
		if got := thsrOutcomeOf(t, opened.harness, exposed); got != durable.TaskOutcomeAborted {
			t.Fatalf("exposed outcome = %s", got)
		}
		shieldedRec, err := opened.harness.GetTask(nil, shielded)
		if err != nil || shieldedRec.AbortRequested {
			t.Fatalf("expected shielded not abort-marked, got %+v err=%v", shieldedRec, err)
		}
		if _, err := opened.harness.AbortTask(nil, tree.background); err != nil {
			t.Fatalf("AbortTask(background): %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, shielded); got != durable.TaskOutcomeAborted {
			t.Fatalf("shielded outcome = %s", got)
		}
		if got := thsrOutcomeOf(t, opened.harness, tree.background); got != durable.TaskOutcomeAborted {
			t.Fatalf("background outcome = %s", got)
		}
		w.open("abort.owner")
		if got := thsrOutcomeOf(t, opened.harness, tree.owner); got != durable.TaskOutcomeAborted {
			t.Fatalf("owner outcome = %s", got)
		}
	})

	t.Run("retries a finalization the Storage rejected with the next commit", func(t *testing.T) {
		w := thsrNewWorld()
		rejecting := &thsrRejectingStorage{Storage: storage.NewMemoryStorage()}
		opened := thsrOpenNodes(t, w, rejecting)
		defer func() { _ = opened.harness.Close(nil) }()

		getChild := thsrSpawnAndFinish(w, "parent", "child")
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntil(t, func() (bool, error) {
			return thsrState(t, opened.harness, parent).Status == durable.TaskStateCompleting, nil
		})
		rejecting.setReject(parent)
		w.open("child")
		childId, _ := getChild()
		if _, err := opened.harness.WaitForTask(nil, childId); err != nil {
			t.Fatalf("WaitForTask(child): %v", err)
		}
		thsrUntilBool(t, func() bool {
			for _, rep := range opened.reports() {
				var sr *durable.StorageRejected
				if errors.As(rep, &sr) {
					return true
				}
			}
			return false
		})
		if st := thsrState(t, opened.harness, parent).Status; st != durable.TaskStateCompleting {
			t.Fatalf("expected parent still completing, got %s", st)
		}
		if _, err := opened.root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatalf("append note: %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
	})
}

func TestHarnessStructured_RecoveryThroughOwnershipEdges(t *testing.T) {
	t.Run("shows an abort-marked owner waiting for work in its owned conversation before resume after reopen", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		session := durable.NewSession(thsrMustSqlite(t, path))
		type treeIds struct{ owner, inner durable.TaskId }
		var tree treeIds
		if err := session.Commit(nil, func(tx durable.Tx) error {
			conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			optsOwner := thsrOwnConv
			optsOwner.ConversationId = &conv.Id
			owner, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "owner"}, optsOwner)
			if err != nil {
				return err
			}
			child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: owner},
			})
			if err != nil {
				return err
			}
			optsInner := thsrOwnConv
			optsInner.ConversationId = &child.Id
			inner, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "inner"}, optsInner)
			if err != nil {
				return err
			}
			tree = treeIds{owner: owner, inner: inner}
			return nil
		}); err != nil {
			t.Fatalf("session.Commit create: %v", err)
		}
		if err := session.Commit(nil, func(tx durable.Tx) error {
			rec, err := tx.Task(tree.owner)
			if err != nil {
				return err
			}
			updated := *rec
			updated.AbortRequested = true
			return tx.(*durable.Transaction).SetTask(updated)
		}); err != nil {
			t.Fatalf("session.Commit setTask: %v", err)
		}
		if err := session.Close(nil); err != nil {
			t.Fatalf("session.Close: %v", err)
		}

		openedTasks, err := testkit.OpenTasks(thsrMustSqlite(t, path), []durable.AnyTask{w.nodeTask()})
		if err != nil {
			t.Fatalf("OpenTasks: %v", err)
		}
		defer func() { _ = openedTasks.Harness.Close(nil) }()

		insp, err := openedTasks.Harness.Inspect(nil)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		var ownerState *durable.TaskInspectionState
		for _, item := range insp.Tasks {
			if item.Record.Id == tree.owner {
				cp := item.State
				ownerState = &cp
				break
			}
		}
		if ownerState == nil || ownerState.Kind != durable.TaskInspectionWaiting || !slices.Equal(ownerState.On, []durable.TaskId{tree.inner}) {
			t.Fatalf("unexpected ownerState before resume: %+v", ownerState)
		}
		if err := openedTasks.Harness.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if got := thsrOutcomeOf(t, openedTasks.Harness, tree.owner); got != durable.TaskOutcomeAborted {
			t.Fatalf("owner outcome = %s", got)
		}
		aborts := w.logFiltered(func(s string) bool { return strings.HasPrefix(s, "abort:") })
		if !slices.Equal(aborts, []string{"abort:inner", "abort:owner"}) {
			t.Fatalf("aborts = %v", aborts)
		}
	})

	t.Run("keeps the root busy after reopen for work below a child task's owned conversation", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		var mu sync.Mutex
		var inner durable.TaskId
		w.script("child", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					opts := thsrOwnConv
					opts.ConversationId = &conv.Id
					id, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "inner"}, opts)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					inner = id
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		thsrParentOf(w, "parent", []string{"child"}, durable.JoinPolicyAllSettled)
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		parent := thsrStart(t, opened.root, w, "parent")
		thsrUntilBool(t, func() bool { return w.hasLog("run:inner") })
		_ = opened.harness.Close(nil)

		opened = thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		if testkit.SettledChannel(thsrAsyncWaitForIdle(opened.root)) {
			t.Fatalf("expected root WaitForIdle pending")
		}
		w.open("inner")
		if err := opened.root.WaitForIdle(nil); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, parent); got != durable.TaskOutcomeCompleted {
			t.Fatalf("parent outcome = %s", got)
		}
		mu.Lock()
		hasInner := inner != 0
		mu.Unlock()
		if !hasInner {
			t.Fatalf("expected inner defined")
		}
	})

	t.Run("keeps a finished background ancestor a boundary after reopen, which { background: true } crosses", func(t *testing.T) {
		w := thsrNewWorld()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		var mu sync.Mutex
		var convId durable.ConversationId
		w.script("child", thsrBehavior{
			run: func(rt thsrRuntime, ctx chord.Context) error {
				return rt.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: running.Id},
					})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					convId = conv.Id
					mu.Unlock()
					return &thsrNext{Status: durable.TaskStateRunning, Checkpoint: thsrNodeCheckpoint{Phase: "resume", Round: 1}}, nil
				})
			},
		})
		thsrParentOf(w, "background", []string{"child"}, durable.JoinPolicyAllSettled)
		opened := thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		bgOpts := thsrOwnConv
		bgOpts.Background = thsrBoolPtr(true)
		background := thsrStart(t, opened.root, w, "background", bgOpts)
		if _, err := opened.harness.WaitForTask(nil, background); err != nil {
			t.Fatalf("WaitForTask(background): %v", err)
		}
		_ = opened.harness.Close(nil)

		opened = thsrOpenNodes(t, w, thsrMustSqlite(t, path))
		defer func() { _ = opened.harness.Close(nil) }()
		mu.Lock()
		cid := convId
		mu.Unlock()
		childConv, err := opened.harness.Conversation(cid, nil)
		if err != nil || childConv == nil {
			t.Fatalf("Conversation: %v", err)
		}
		below := thsrStart(t, childConv, w, "below")
		thsrUntilBool(t, func() bool { return w.hasLog("run:below") })
		if err := opened.root.WaitForIdle(nil); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if err := opened.root.Abort(nil); err != nil {
			t.Fatalf("root.Abort: %v", err)
		}
		if st := thsrState(t, opened.harness, below).Status; st != durable.TaskStateRunning {
			t.Fatalf("below status = %s", st)
		}
		if err := opened.root.Abort(nil, durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatalf("root.Abort(background): %v", err)
		}
		if got := thsrOutcomeOf(t, opened.harness, below); got != durable.TaskOutcomeAborted {
			t.Fatalf("below outcome = %s", got)
		}
	})
}

func TestHarnessStructured_ToolRoundsAndEvents(t *testing.T) {
	t.Run("ends an aborted sequential round's unstarted calls with their result entries, right before them", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		oneStarted, oneReg := thsrBlockingTool("one")
		_, twoReg := thsrBlockingTool("two")
		if _, err := testkit.AddTool(setup.Registry, oneReg); err != nil {
			t.Fatalf("AddTool(one): %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, twoReg); err != nil {
			t.Fatalf("AddTool(two): %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"one", "c1"}, thsrCallSpec{"two", "c2"}, thsrCallSpec{"two", "c3"})},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		stream, getEvents := thsrListenEvents(t, harness, root)
		defer stream.Stop()
		sub := thsrSubmitInput(t, root, "go")
		<-oneStarted
		genId := thsrLiveSnapshot(t, harness, root.Id()).Run.TaskId
		if _, err := harness.AbortTask(nil, genId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		thsrUntilBool(t, func() bool { return slices.Contains(thsrEventLabels(getEvents()), "result:c3") })
		var nonMsg []string
		for _, lbl := range thsrEventLabels(getEvents()) {
			if !strings.HasPrefix(lbl, "message:") {
				nonMsg = append(nonMsg, lbl)
			}
		}
		want := []string{"end:c1:true", "result:c1", "end:c2:true", "result:c2", "end:c3:true", "result:c3"}
		if !slices.Equal(nonMsg, want) {
			t.Fatalf("labels = %v, want %v", nonMsg, want)
		}
	})

	t.Run("emits one turn_end per generation, also for a stream attached while it holds", func(t *testing.T) {
		w := thsrNewWorld()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddTask(setup.Registry, w.nodeTask()); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "noop",
			Description: "Does nothing",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{Content: ai.ContentList{}}, nil
			},
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		var hMu sync.Mutex
		var openedH durable.Harness
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterTools: func(_ durable.EntryId, _ []durable.EntryId, api durable.HookApi, callCtx chord.Context) error {
				hMu.Lock()
				h := openedH
				hMu.Unlock()
				return h.Commit(callCtx, func(tx durable.Tx) error {
					_, err := tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "hooked"}, thsrOwned(api.TaskId()))
					return err
				})
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"noop", "c1"})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()
		hMu.Lock()
		openedH = harness
		hMu.Unlock()

		earlyStream, earlyEvents := thsrListenEvents(t, harness, root)
		defer earlyStream.Stop()
		if _, err := thsrSubmitInput(t, root, "go").Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		lateStream, lateEvents := thsrListenEvents(t, harness, root)
		defer lateStream.Stop()
		w.open("hooked")
		var first durable.TaskRecord
		genKind := "pi.generation"
		if err := harness.Commit(nil, func(tx durable.Tx) error {
			page, err := tx.ScanTasks(durable.TaskQuery{Kind: &genKind}, 1, nil)
			if err != nil {
				return err
			}
			first = page.Items[0]
			return nil
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if _, err := harness.WaitForTask(nil, first.Id); err != nil {
			t.Fatalf("WaitForTask: %v", err)
		}
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatalf("append note: %v", err)
		}
		thsrUntilBool(t, func() bool {
			for _, ev := range lateEvents() {
				if ev.AgentEventType() == "entry_appended" {
					return true
				}
			}
			return false
		})
		countTurns := func(events []durable.AgentEvent) int {
			n := 0
			for _, ev := range events {
				if ev.AgentEventType() == "turn_end" {
					n++
				}
			}
			return n
		}
		if countTurns(earlyEvents()) != 2 {
			t.Fatalf("early turn_ends = %d", countTurns(earlyEvents()))
		}
		if countTurns(lateEvents()) != 0 {
			t.Fatalf("late turn_ends = %d", countTurns(lateEvents()))
		}
	})

	t.Run("lets a tool that owns live work finish its call at the hold while the generation waits for its final commit", func(t *testing.T) {
		w := thsrNewWorld()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddTask(setup.Registry, w.nodeTask()); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "delegate",
			Description: "Starts work in a conversation it owns",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				if _, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: api.TaskId()},
					})
					if err != nil {
						return nil, err
					}
					opts := thsrOwnConv
					opts.ConversationId = &child.Id
					return tx.CreateTask(w.nodeTask(), thsrNodeInput{Name: "sub"}, opts)
				}); err != nil {
					return nil, err
				}
				return &durable.ToolExecutionResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "started"}}}, nil
			},
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"delegate", "c1"})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		stream, getEvents := thsrListenEvents(t, harness, root)
		defer stream.Stop()
		sub := thsrSubmitInput(t, root, "go")
		thsrUntilBool(t, func() bool {
			live := thsrLiveSnapshot(t, harness, root.Id())
			return live != nil && len(live.Tools) > 0 && live.Tools[0].Status == durable.ToolSlotDone
		})
		live := thsrLiveSnapshot(t, harness, root.Id())
		toolId := *live.Tools[0].TaskId
		if live.Tools[0].Entry == nil {
			t.Fatalf("expected tool entry set while held")
		}
		if st := thsrState(t, harness, toolId).Status; st != durable.TaskStateCompleting {
			t.Fatalf("expected tool completing, got %s", st)
		}
		if st := thsrState(t, harness, live.Run.TaskId).Status; st != durable.TaskStateWaiting {
			t.Fatalf("expected generation waiting, got %s", st)
		}
		thsrUntilBool(t, func() bool { return slices.Contains(thsrEventLabels(getEvents()), "end:c1:true") })
		waitSig := make(chan struct{})
		waitCh := make(chan *durable.SettledSubmissionRecord, 1)
		go func() {
			defer close(waitSig)
			rec, _ := sub.Wait(nil)
			waitCh <- rec
		}()
		if testkit.SettledChannel(waitSig) {
			t.Fatalf("expected submission still pending")
		}
		w.open("sub")
		settled := <-waitCh
		if settled == nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("unexpected settled submission: %+v", settled)
		}
		if got := thsrOutcomeOf(t, harness, toolId); got != durable.TaskOutcomeCompleted {
			t.Fatalf("tool outcome = %s", got)
		}
	})

	t.Run("holds a faulted tool's slot and task_failed until the work it owns drained", func(t *testing.T) {
		w := thsrNewWorld()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddTask(setup.Registry, w.nodeTask()); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		w.script("held", thsrBehavior{
			abort: func(rt thsrRuntime, ctx chord.Context) error {
				if _, err := w.gate("abort.held").wait(nil); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thsrNodeInput, thsrNodeCheckpoint]) (*thsrNext, error) {
					return &thsrNext{Status: durable.TaskStateTerminal, Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted}}, nil
				})
			},
		})
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "broken",
			Description: "Starts owned work, then returns a result that is not strict JSON",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				if _, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
					return thsrSpawn(tx, w, api.TaskId(), "held")
				}); err != nil {
					return nil, err
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{},
					HasDetails: true,
					Details:    jsonx.ObjectOf("fn", func() int { return 1 }),
				}, nil
			},
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thsrToolCalls(thsrCallSpec{"broken", "c1"})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = harness.Close(nil) }()

		stream, getEvents := thsrListenEvents(t, harness, root)
		defer stream.Stop()
		sub := thsrSubmitInput(t, root, "go")
		thsrUntilBool(t, func() bool { return w.hasLog("abort:held") })
		live := thsrLiveSnapshot(t, harness, root.Id())
		toolId := *live.Tools[0].TaskId
		toolState := thsrState(t, harness, toolId)
		if toolState.Status != durable.TaskStateCompleting || toolState.Outcome == nil || toolState.Outcome.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("unexpected toolState: %+v", toolState)
		}
		if live.Tools[0].Status == durable.ToolSlotDone {
			t.Fatalf("expected tool slot not done while held")
		}
		for _, ev := range getEvents() {
			if ev.AgentEventType() == "task_failed" {
				t.Fatalf("did not expect task_failed while tool held")
			}
		}
		w.open("abort.held")
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub.Wait = %+v err=%v", settled, err)
		}
		if got := thsrOutcomeOf(t, harness, toolId); got != durable.TaskOutcomeFaulted {
			t.Fatalf("tool outcome = %s", got)
		}
		thsrUntilBool(t, func() bool {
			for _, ev := range getEvents() {
				if ev.AgentEventType() == "task_failed" {
					return true
				}
			}
			return false
		})
		if !slices.Contains(thsrEventLabels(getEvents()), "end:c1:false") {
			t.Fatalf("expected end:c1:false in %v", thsrEventLabels(getEvents()))
		}
	})
}
