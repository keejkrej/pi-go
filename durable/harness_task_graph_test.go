// Ported from packages/durable/test/harness-task-graph.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type thtgChildInput struct {
	Late bool `json:"late"`
}

type thtgPhaseStep struct {
	Phase string          `json:"phase"`
	Child *durable.TaskId `json:"child,omitzero"`
}

func thtgFamily(t *testing.T, childGate <-chan struct{}, lateGate <-chan struct{}) (durable.AnyTask, durable.AnyTask) {
	t.Helper()
	childTask := durable.DefineTask(durable.TaskDefinition[thtgChildInput, thtgPhaseStep, any, struct{}]{
		Name:    "test.graph-child",
		Version: 1,
		Initial: func(_ thtgChildInput) thtgPhaseStep { return thtgPhaseStep{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[thtgChildInput, thtgPhaseStep, any, struct{}]{
			"work": func(ctx chord.Context, rt durable.RunningTask[thtgChildInput, thtgPhaseStep], runtime *durable.TaskRuntime[thtgChildInput, thtgPhaseStep, any, struct{}]) error {
				if rt.Input.Late {
					<-lateGate
				} else {
					<-childGate
				}
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[thtgChildInput, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					next := testkit.Completed[thtgPhaseStep, any](thtgPhaseStep{Phase: "work"}, nil)
					return &next, nil
				})
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[thtgChildInput, thtgPhaseStep], _ *durable.TaskRuntime[thtgChildInput, thtgPhaseStep, any, struct{}]) error {
			return nil
		},
	})

	parentTask := durable.DefineTask(durable.TaskDefinition[any, thtgPhaseStep, any, struct{}]{
		Name:    "test.graph-parent",
		Version: 1,
		Initial: func(_ any) thtgPhaseStep { return thtgPhaseStep{Phase: "spawn"} },
		Phases: map[string]durable.PhaseHandler[any, thtgPhaseStep, any, struct{}]{
			"spawn": func(ctx chord.Context, rt durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
				ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: rt.Id}
				// Two conversations in a commit that leaves the parent's record unchanged.
				if err := runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					if _, err := tx.CreateConversation(durable.ConversationCreateTxOptions{Ownership: ownership}); err != nil {
						return nil, err
					}
					if _, err := tx.CreateConversation(durable.ConversationCreateTxOptions{Ownership: ownership}); err != nil {
						return nil, err
					}
					return nil, nil
				}); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					childId, err := tx.CreateTask(childTask, thtgChildInput{Late: false}, durable.TaskOptions{
						Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: rt.Id},
					})
					if err != nil {
						return nil, err
					}
					cid := childId
					return &durable.NextTaskState[thtgPhaseStep, any]{
						Status:     durable.TaskStateWaiting,
						Checkpoint: thtgPhaseStep{Phase: "join", Child: &cid},
						On:         []durable.TaskId{childId},
						Policy:     durable.JoinPolicyAllSettled,
					}, nil
				})
			},
			"join": func(ctx chord.Context, rt durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					if _, err := tx.CreateTask(childTask, thtgChildInput{Late: true}, durable.TaskOptions{
						Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: rt.Id},
					}); err != nil {
						return nil, err
					}
					return &durable.NextTaskState[thtgPhaseStep, any]{
						Status:     durable.TaskStateRunning,
						Checkpoint: thtgPhaseStep{Phase: "finish"},
					}, nil
				})
			},
			"finish": func(ctx chord.Context, _ durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					next := testkit.Completed[thtgPhaseStep, any](thtgPhaseStep{Phase: "finish"}, nil)
					return &next, nil
				})
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[any, thtgPhaseStep], _ *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
			return nil
		},
	})

	return parentTask, childTask
}

func thtgStatuses(graph durable.TaskGraph) map[string]string {
	out := make(map[string]string, len(graph.Tasks))
	for _, node := range graph.Tasks {
		out[node.Kind+"#"+strconv.FormatInt(int64(node.Id), 10)] = node.State.Status
	}
	return out
}

func thtgEncode(t *testing.T, v any) durable.JsonValue {
	t.Helper()
	enc, err := jsonx.Encode(v)
	if err != nil {
		t.Fatalf("jsonx.Encode: %v", err)
	}
	return enc
}

func thtgTaskKey(id durable.TaskId) string {
	return strconv.FormatInt(int64(id), 10)
}

func TestHarnessTaskGraph(t *testing.T) {
	t.Run("follows every live task through its statuses, owner edges, and owned conversations", func(t *testing.T) {
		child := testkit.NewDeferred[struct{}]()
		late := testkit.NewDeferred[struct{}]()
		parentTask, childTask := thtgFamily(t, child.Done(), late.Done())
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), []durable.AnyTask{parentTask, childTask})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			child.Resolve(struct{}{})
			late.Resolve(struct{}{})
			_ = opened.Harness.Close(nil)
		}()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}

		var mu sync.Mutex
		var seen []durable.TaskGraph
		observe := func() chord.AttachedReplicatedState[durable.TaskGraph] {
			state, err := opened.Harness.TaskGraph(nil)
			if err != nil {
				t.Fatal(err)
			}
			state.Subscribe(func(value durable.TaskGraph, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
				if delivery.Kind == chord.ReplicatedStateDeliveryKindUpdate {
					mu.Lock()
					seen = append(seen, value)
					mu.Unlock()
				}
				return nil
			})
			return state
		}

		graph := observe()
		initVal, ok := graph.Value()
		if !ok || len(initVal.Tasks) != 0 {
			t.Fatalf("initial graph = %+v, ok=%v, want empty", initVal, ok)
		}
		rebuild := func() {
			advanced, _ := graph.Value()
			graph.Dispose()
			graph = observe()
			rebuiltVal, _ := graph.Value()
			if reflect.ValueOf(rebuiltVal.Tasks).Pointer() == reflect.ValueOf(advanced.Tasks).Pointer() {
				t.Fatalf("expected rebuilt graph map to be a fresh instance")
			}
			if !jsonx.DeepEqual(thtgEncode(t, rebuiltVal), thtgEncode(t, advanced)) {
				t.Fatalf("rebuilt graph = %+v, want %+v", rebuiltVal, advanced)
			}
		}

		rawParent, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(parentTask, nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		parent := rawParent.(durable.TaskId)
		parentKey := thtgTaskKey(parent)
		testkit.Flush()

		cur, _ := graph.Value()
		gotNode, hasNode := cur.Tasks[parentKey]
		wantPending := durable.TaskGraphNode{
			Id:             parent,
			Kind:           "test.graph-parent",
			ConversationId: root.Id(),
			Background:     false,
			AbortRequested: false,
			State:          durable.TaskGraphState{Status: "pending", Phase: "spawn"},
			Conversations:  []durable.ConversationId{},
		}
		if !hasNode || !jsonx.DeepEqual(thtgEncode(t, gotNode), thtgEncode(t, wantPending)) {
			t.Fatalf("pending node = %+v (has=%v), want %+v", gotNode, hasNode, wantPending)
		}

		if err := opened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		childIdStr := func() string {
			v, _ := graph.Value()
			for _, node := range v.Tasks {
				if node.Kind == "test.graph-child" {
					return thtgTaskKey(node.Id)
				}
			}
			return ""
		}
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := graph.Value()
			cid := childIdStr()
			return len(v.Tasks) == 2 && cid != "" && thtgStatuses(v)["test.graph-child#"+cid] == "running", nil
		}); err != nil {
			t.Fatal(err)
		}

		firstNum, err := strconv.ParseInt(childIdStr(), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		first := durable.TaskId(firstNum)
		cur, _ = graph.Value()
		parentNode := cur.Tasks[parentKey]
		allSettled := durable.JoinPolicyAllSettled
		wantWaiting := durable.TaskGraphState{
			Status: "waiting",
			Phase:  "join",
			On:     []durable.TaskId{first},
			Policy: &allSettled,
		}
		if !jsonx.DeepEqual(thtgEncode(t, parentNode.State), thtgEncode(t, wantWaiting)) {
			t.Fatalf("parentNode.State = %+v, want %+v", parentNode.State, wantWaiting)
		}
		if len(parentNode.Conversations) != 2 {
			t.Fatalf("len(parentNode.Conversations) = %d, want 2", len(parentNode.Conversations))
		}
		sortedConvs := slices.Clone(parentNode.Conversations)
		slices.Sort(sortedConvs)
		if !slices.Equal(sortedConvs, parentNode.Conversations) {
			t.Fatalf("conversations not sorted: %v", parentNode.Conversations)
		}
		ownedConv := parentNode.Conversations[0]
		childNode := cur.Tasks[thtgTaskKey(first)]
		if childNode.Owner == nil || *childNode.Owner != parent || childNode.ConversationId != root.Id() {
			t.Fatalf("childNode = %+v, want owner=%d conversationId=%d", childNode, parent, root.Id())
		}
		convHandle, err := opened.Harness.Conversation(ownedConv, nil)
		if err != nil || convHandle == nil {
			t.Fatalf("owned conversation missing: handle=%v err=%v", convHandle, err)
		}
		rebuild()

		child.Resolve(struct{}{})
		firstKey := thtgTaskKey(first)
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := graph.Value()
			n, ok := v.Tasks[parentKey]
			if !ok || n.State.Status != "completing" || len(v.Tasks) != 2 {
				return false, nil
			}
			if _, exists := v.Tasks[firstKey]; exists {
				return false, nil
			}
			for _, taskNode := range v.Tasks {
				if taskNode.Kind == "test.graph-child" && taskNode.State.Status == "running" {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		cur, _ = graph.Value()
		wantCompleting := durable.TaskGraphState{
			Status:  "completing",
			Outcome: durable.TaskOutcomeCompleted,
		}
		if !jsonx.DeepEqual(thtgEncode(t, cur.Tasks[parentKey].State), thtgEncode(t, wantCompleting)) {
			t.Fatalf("completing state = %+v, want %+v", cur.Tasks[parentKey].State, wantCompleting)
		}
		if _, exists := cur.Tasks[thtgTaskKey(first)]; exists {
			t.Fatalf("expected finished child %d to be removed from live graph", first)
		}
		if !slices.Equal(cur.Tasks[parentKey].Conversations, parentNode.Conversations) {
			t.Fatalf("completing conversations = %v, want %v", cur.Tasks[parentKey].Conversations, parentNode.Conversations)
		}
		rebuild()

		late.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, parent); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		cur, _ = graph.Value()
		if len(cur.Tasks) != 0 {
			t.Fatalf("final graph = %+v, want empty", cur)
		}

		mu.Lock()
		seenCopy := slices.Clone(seen)
		mu.Unlock()
		for i := 1; i < len(seenCopy); i++ {
			if jsonx.DeepEqual(thtgEncode(t, seenCopy[i]), thtgEncode(t, seenCopy[i-1])) {
				t.Fatalf("duplicate consecutive frame at %d: %+v", i, seenCopy[i])
			}
		}
		hasTwoConvs := false
		for _, g := range seenCopy {
			if n, ok := g.Tasks[parentKey]; ok && len(n.Conversations) == 2 {
				hasTwoConvs = true
				break
			}
		}
		if !hasTwoConvs {
			t.Fatalf("expected revision setting 2 owned conversations on parent %s", parentKey)
		}
		graph.Dispose()
	})

	t.Run("builds from committed tasks, shows surviving tasks as pending after reopen, and marks aborts", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "session.sqlite")
		gate := testkit.NewDeferred[struct{}]()
		workTask := durable.DefineTask(durable.TaskDefinition[any, thtgPhaseStep, any, struct{}]{
			Name:    "test.graph-work",
			Version: 1,
			Initial: func(_ any) thtgPhaseStep { return thtgPhaseStep{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[any, thtgPhaseStep, any, struct{}]{
				"work": func(ctx chord.Context, _ durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
					select {
					case <-gate.Done():
					case <-runtime.Signal().Done():
					}
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
						next := testkit.Completed[thtgPhaseStep, any](thtgPhaseStep{Phase: "work"}, nil)
						return &next, nil
					})
				},
			},
			Abort: func(ctx chord.Context, _ durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
					next := testkit.AbortedWith[thtgPhaseStep, any](thtgPhaseStep{Phase: "work"}, "test")
					return &next, nil
				})
			},
		})

		st1, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		first, err := testkit.OpenTasks(st1, []durable.AnyTask{workTask})
		if err != nil {
			t.Fatal(err)
		}
		firstRoot, err := first.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}
		type workIds struct {
			fg    durable.TaskId
			bg    durable.TaskId
			owned durable.ConversationId
		}
		rawIds, err := firstRoot.Commit(nil, func(tx durable.Tx) (any, error) {
			fg, err := tx.CreateTask(workTask, nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			bgFlag := true
			bg, err := tx.CreateTask(workTask, nil, durable.TaskOptions{
				Ownership:  durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				Background: &bgFlag,
			})
			if err != nil {
				return nil, err
			}
			conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: fg},
			})
			if err != nil {
				return nil, err
			}
			return workIds{fg: fg, bg: bg, owned: conv.Id}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := rawIds.(workIds)
		if err := first.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			insp, err := first.Harness.Inspect(nil)
			if err != nil {
				return false, err
			}
			if len(insp.Tasks) == 0 {
				return false, nil
			}
			for _, task := range insp.Tasks {
				if task.State.Kind != durable.TaskInspectionRunning {
					return false, nil
				}
			}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		runningState, err := first.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		runningVal, _ := runningState.Value()
		wantRunning := map[string]string{
			"test.graph-work#" + thtgTaskKey(ids.fg): "running",
			"test.graph-work#" + thtgTaskKey(ids.bg): "running",
		}
		if !reflect.DeepEqual(thtgStatuses(runningVal), wantRunning) {
			t.Fatalf("running statuses = %v, want %v", thtgStatuses(runningVal), wantRunning)
		}
		runningState.Dispose()
		if err := first.Harness.Close(nil); err != nil {
			t.Fatal(err)
		}

		// Reopen from sqlite.
		st2, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := testkit.OpenTasks(st2, []durable.AnyTask{workTask})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reopened.Harness.Close(nil) }()

		watch, err := reopened.Harness.WatchTaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		wantPending := map[string]string{
			"test.graph-work#" + thtgTaskKey(ids.fg): "pending",
			"test.graph-work#" + thtgTaskKey(ids.bg): "pending",
		}
		if !reflect.DeepEqual(thtgStatuses(watch.Value()), wantPending) {
			t.Fatalf("reopened pending statuses = %v, want %v", thtgStatuses(watch.Value()), wantPending)
		}
		if got := watch.Value().Tasks[thtgTaskKey(ids.fg)].Conversations; !slices.Equal(got, []durable.ConversationId{ids.owned}) {
			t.Fatalf("foreground conversations = %v, want [%d]", got, ids.owned)
		}
		if !watch.Value().Tasks[thtgTaskKey(ids.bg)].Background {
			t.Fatalf("expected background=true for task %d", ids.bg)
		}

		replica := thtgEncode(t, watch.Value())
		var frameMu sync.Mutex
		var frames [][]chord.Op
		if err := watch.Start(func(value durable.TaskGraph, ops []chord.Op, _ chord.Context) error {
			frameMu.Lock()
			defer frameMu.Unlock()
			next, err := chord.ApplyImmutable(replica, chord.OpList(ops))
			if err != nil {
				return err
			}
			replica = next
			if !jsonx.DeepEqual(replica, thtgEncode(t, value)) {
				t.Fatalf("replayed replica != delivered value")
			}
			frames = append(frames, ops)
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		if err := reopened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		bgKey := thtgTaskKey(ids.bg)
		if err := testkit.Eventually(func() (bool, error) {
			node, ok := watch.Value().Tasks[bgKey]
			return ok && node.State.Status == "running", nil
		}); err != nil {
			t.Fatal(err)
		}
		abortRes, err := reopened.Harness.AbortTask(nil, ids.bg)
		if err != nil || abortRes != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = (%q, %v), want marked", abortRes, err)
		}
		if _, err := reopened.Harness.WaitForTask(nil, ids.bg); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		_ = watch.Stop()

		frameMu.Lock()
		gotFrames := slices.Clone(frames)
		gotReplica := replica
		frameMu.Unlock()

		hasAbortMark := false
		for _, frame := range gotFrames {
			if len(frame) != 1 {
				continue
			}
			if op, ok := frame[0].(chord.OpSet); ok && len(op.Path) == 2 && op.Path[0] == chord.SegString("tasks") && op.Path[1] == chord.SegString(bgKey) {
				if obj, ok := op.Value.(*jsonx.Object); ok {
					if ar, _ := obj.Get("abortRequested"); ar == true {
						hasAbortMark = true
					}
				}
			}
		}
		if !hasAbortMark {
			t.Fatalf("expected abortRequested=true OpSet frame for task %s", bgKey)
		}
		if len(gotFrames) == 0 {
			t.Fatalf("expected frames")
		}
		lastFrame := gotFrames[len(gotFrames)-1]
		wantDel := chord.OpDelete{Path: chord.Path{chord.SegString("tasks"), chord.SegString(bgKey)}}
		if len(lastFrame) != 1 || !reflect.DeepEqual(lastFrame[0], wantDel) {
			t.Fatalf("last frame = %#v, want [%#v]", lastFrame, wantDel)
		}
		repObj := gotReplica.(*jsonx.Object)
		repTasksVal, _ := repObj.Get("tasks")
		repTasksObj := repTasksVal.(*jsonx.Object)
		if keys := repTasksObj.Keys(); !slices.Equal(keys, []string{thtgTaskKey(ids.fg)}) {
			t.Fatalf("replica task keys = %v, want [%s]", keys, thtgTaskKey(ids.fg))
		}

		gate.Resolve(struct{}{})
		if _, err := reopened.Harness.WaitForTask(nil, ids.fg); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("lists owned conversations in ID order whatever order one commit creates them in", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		var createdMu sync.Mutex
		var created []durable.ConversationId
		type spawnerInput struct {
			At durable.EntryId `json:"at"`
		}
		spawner := durable.DefineTask(durable.TaskDefinition[spawnerInput, thtgPhaseStep, any, struct{}]{
			Name:    "test.graph-spawner",
			Version: 1,
			Initial: func(_ spawnerInput) thtgPhaseStep { return thtgPhaseStep{Phase: "spawn"} },
			Phases: map[string]durable.PhaseHandler[spawnerInput, thtgPhaseStep, any, struct{}]{
				"spawn": func(ctx chord.Context, rt durable.RunningTask[spawnerInput, thtgPhaseStep], runtime *durable.TaskRuntime[spawnerInput, thtgPhaseStep, any, struct{}]) error {
					if err := runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[spawnerInput, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
						ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: rt.Id}
						forked, err := tx.ForkConversation(runtime.ConversationId(), rt.Input.At, durable.ConversationCreateTxOptions{Ownership: ownership})
						if err != nil {
							return nil, err
						}
						fresh, err := tx.CreateConversation(durable.ConversationCreateTxOptions{Ownership: ownership})
						if err != nil {
							return nil, err
						}
						createdMu.Lock()
						created = append(created, forked.Id, fresh.Id)
						createdMu.Unlock()
						return nil, nil
					}); err != nil {
						return err
					}
					<-gate.Done()
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[spawnerInput, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
						next := testkit.Completed[thtgPhaseStep, any](thtgPhaseStep{Phase: "spawn"}, nil)
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[spawnerInput, thtgPhaseStep], _ *durable.TaskRuntime[spawnerInput, thtgPhaseStep, any, struct{}]) error {
				return nil
			},
		})

		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), []durable.AnyTask{spawner})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}
		graph, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		rawId, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			if err != nil {
				return nil, err
			}
			return tx.CreateTask(spawner, spawnerInput{At: entry.Id}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		id := rawId.(durable.TaskId)
		key := thtgTaskKey(id)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := graph.Value()
			n, ok := v.Tasks[key]
			return ok && len(n.Conversations) == 2, nil
		}); err != nil {
			t.Fatal(err)
		}
		advanced, _ := graph.Value()
		createdMu.Lock()
		wantConvs := slices.Clone(created)
		createdMu.Unlock()
		slices.Sort(wantConvs)
		if !slices.Equal(advanced.Tasks[key].Conversations, wantConvs) {
			t.Fatalf("conversations = %v, want %v", advanced.Tasks[key].Conversations, wantConvs)
		}
		graph.Dispose()

		rebuilt, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		rebuiltVal, _ := rebuilt.Value()
		if !jsonx.DeepEqual(thtgEncode(t, rebuiltVal), thtgEncode(t, advanced)) {
			t.Fatalf("rebuilt = %+v, want %+v", rebuiltVal, advanced)
		}
		rebuilt.Dispose()
		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("registers nothing for an acquisition cancelled while it waits for the line", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}
		never := make(chan struct{})
		_, childTask := thtgFamily(t, never, never)
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(childTask, thtgChildInput{Late: false}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		}); err != nil {
			t.Fatal(err)
		}

		held := st.HoldCommits()
		blockingErr := make(chan error, 1)
		go func() {
			blockingErr <- opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
				return err
			})
		}()
		<-held.Entered

		childCtx := chord.WithCancel(nil)
		cancelErr := make(chan error, 1)
		go func() {
			_, err := opened.Harness.WatchTaskGraph(childCtx.Context)
			cancelErr <- err
		}()
		testkit.Flush()
		childCtx.Cancel(errors.New("cancelled"))
		held.Release()
		if err := <-blockingErr; err != nil {
			t.Fatal(err)
		}
		if err := <-cancelErr; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled error, got %v", err)
		}

		first, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		firstVal, _ := first.Value()
		first.Dispose()
		second, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		secondVal, _ := second.Value()
		second.Dispose()
		if reflect.ValueOf(secondVal.Tasks).Pointer() == reflect.ValueOf(firstVal.Tasks).Pointer() {
			t.Fatalf("expected no shared mount left behind")
		}
		if !jsonx.DeepEqual(thtgEncode(t, secondVal), thtgEncode(t, firstVal)) {
			t.Fatalf("second = %+v, want %+v", secondVal, firstVal)
		}
	})

	t.Run("publishes no revision for a commit that changes no node, and shares one mount between observers", func(t *testing.T) {
		gate := testkit.NewDeferred[struct{}]()
		reached := testkit.NewDeferred[struct{}]()
		memoTask := durable.DefineTask(durable.TaskDefinition[any, thtgPhaseStep, any, struct{}]{
			Name:    "test.graph-memo",
			Version: 1,
			Initial: func(_ any) thtgPhaseStep { return thtgPhaseStep{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[any, thtgPhaseStep, any, struct{}]{
				"work": func(ctx chord.Context, _ durable.RunningTask[any, thtgPhaseStep], runtime *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
					if _, err := runtime.MemoPut(ctx, "seen", true); err != nil {
						return err
					}
					reached.Resolve(struct{}{})
					<-gate.Done()
					return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thtgPhaseStep]) (*durable.NextTaskState[thtgPhaseStep, any], error) {
						next := testkit.Completed[thtgPhaseStep, any](thtgPhaseStep{Phase: "work"}, nil)
						return &next, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, thtgPhaseStep], _ *durable.TaskRuntime[any, thtgPhaseStep, any, struct{}]) error {
				return nil
			},
		})

		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), []durable.AnyTask{memoTask})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, err := opened.Harness.Root(nil)
		if err != nil {
			t.Fatal(err)
		}
		rawId, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(memoTask, nil, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		id := rawId.(durable.TaskId)
		key := thtgTaskKey(id)

		first, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		second, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		firstVal, _ := first.Value()
		secondVal, _ := second.Value()
		if reflect.ValueOf(secondVal.Tasks).Pointer() != reflect.ValueOf(firstVal.Tasks).Pointer() {
			t.Fatalf("expected second observer to share mount with first")
		}

		var upMu sync.Mutex
		var updates []string
		first.Subscribe(func(value durable.TaskGraph, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
			if delivery.Kind != chord.ReplicatedStateDeliveryKindUpdate {
				return nil
			}
			upMu.Lock()
			if node, ok := value.Tasks[key]; ok {
				updates = append(updates, node.State.Status)
			} else {
				updates = append(updates, "gone")
			}
			upMu.Unlock()
			return nil
		})

		if err := opened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		<-reached.Done()
		testkit.Flush()
		upMu.Lock()
		gotBefore := slices.Clone(updates)
		upMu.Unlock()
		if !slices.Equal(gotBefore, []string{"running"}) {
			t.Fatalf("updates before completion = %v, want [running]", gotBefore)
		}

		gate.Resolve(struct{}{})
		if _, err := opened.Harness.WaitForTask(nil, id); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		upMu.Lock()
		gotAfter := slices.Clone(updates)
		upMu.Unlock()
		if !slices.Equal(gotAfter, []string{"running", "gone"}) {
			t.Fatalf("updates after completion = %v, want [running gone]", gotAfter)
		}

		lastVal, _ := first.Value()
		first.Dispose()
		second.Dispose()

		rebuilt, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer rebuilt.Dispose()
		rebuiltVal, _ := rebuilt.Value()
		if !jsonx.DeepEqual(thtgEncode(t, rebuiltVal), thtgEncode(t, lastVal)) {
			t.Fatalf("rebuilt = %+v, want %+v", rebuiltVal, lastVal)
		}
		if reflect.ValueOf(rebuiltVal.Tasks).Pointer() == reflect.ValueOf(lastVal.Tasks).Pointer() {
			t.Fatalf("expected rebuilt mount after last observer disposed")
		}
	})
}
