// Ported from packages/durable/src/harness/task-graph.ts (pi v1.0.0).

package durable

import (
	"cmp"
	"slices"
	"strconv"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// TaskGraphState is a live task's durable status without its checkpoint or outcome payloads.
type TaskGraphState struct {
	Status  string            `json:"status"` // "pending" | "running" | "waiting" | "completing"
	Phase   string            `json:"phase,omitzero"`
	On      []TaskId          `json:"on,omitzero"`
	Policy  *JoinPolicy       `json:"policy,omitzero"`
	Outcome TaskOutcomeStatus `json:"outcome,omitzero"`
}

// TaskGraphNode describes one live task in a TaskGraph snapshot.
type TaskGraphNode struct {
	Id             TaskId           `json:"id"`
	Kind           string           `json:"kind"`
	ConversationId ConversationId   `json:"conversationId"`
	Owner          *TaskId          `json:"owner,omitzero"`
	Background     bool             `json:"background"`
	AbortRequested bool             `json:"abortRequested"`
	State          TaskGraphState   `json:"state"`
	Conversations  []ConversationId `json:"conversations"`
}

// TaskGraph holds every live task of a Session keyed by its decimal ID string.
type TaskGraph struct {
	Tasks map[string]TaskGraphNode `json:"tasks"`
}

// TaskGraphWatch aliases WatchHandle[TaskGraph].
type TaskGraphWatch = WatchHandle[TaskGraph]

const htgScanPageSize = 256

var htgLiveStatuses = []TaskStateStatus{
	TaskStatePending,
	TaskStateRunning,
	TaskStateWaiting,
	TaskStateCompleting,
}

type htgObserver interface {
	Advance(value TaskGraph, ops []chord.Op, ctx chord.Context)
	CloseSession()
}

type htgMount struct {
	value     TaskGraph
	observers map[htgObserver]struct{}
}

// TaskGraphView manages the Session-wide live TaskGraph mount, built on demand by its first observer
// and dropped with its last.
type TaskGraphView struct {
	session *SessionImpl
	storage Storage

	mu     sync.Mutex
	mount  *htgMount
	closed bool
}

// NewTaskGraphView constructs a TaskGraphView subscribed to session commits and closure.
func NewTaskGraphView(session *SessionImpl, storage Storage) *TaskGraphView {
	g := &TaskGraphView{
		session: session,
		storage: storage,
	}
	session.SubscribeCommits(func(publication CommitPublication, ctx chord.Context) {
		g.onPublication(publication, ctx)
	})
	session.SubscribeClose(func() {
		g.onClose()
	})
	return g
}

// State returns a disposable read-only Chord ReplicatedState of the task graph.
func (g *TaskGraphView) State(ctx chord.Context) (chord.AttachedReplicatedState[TaskGraph], error) {
	obs, detach, err := htgAttach(g, func(value TaskGraph, release func()) *soCommittedStateSource[TaskGraph] {
		return soNewCommittedStateSource(value, release)
	}, ctx)
	if err != nil {
		return nil, err
	}
	state, err := chord.AttachReplicatedStateSource[TaskGraph](obs, nil)
	if err != nil {
		detach()
		return nil, err
	}
	return state, nil
}

// Watch returns a serialized exact-frame WatchHandle of the task graph; cancelling ctx stops it.
func (g *TaskGraphView) Watch(ctx chord.Context) (TaskGraphWatch, error) {
	obs, _, err := htgAttach(g, func(value TaskGraph, release func()) *soCommittedWatch[TaskGraph] {
		return soNewCommittedWatch(value, release, nil)
	}, ctx)
	if err != nil {
		return nil, err
	}
	sig := hutAbortSignal(ctx)
	if sig != nil && sig.Err() != nil {
		obs.Cancel()
		return nil, hutSignalErr(sig)
	}
	if sig != nil {
		_ = obs.ObserveCancellation(sig)
	}
	return obs, nil
}

func htgAttach[O htgObserver](
	g *TaskGraphView,
	create func(value TaskGraph, release func()) O,
	ctx chord.Context,
) (O, func(), error) {
	var observer O
	var detach func()
	err := g.session.ReadOnLine(func() error {
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return hutClosedError()
		}
		mount := g.mount
		g.mu.Unlock()

		if mount == nil {
			built, err := g.build(ctx)
			if err != nil {
				return err
			}
			mount = &htgMount{
				value:     built,
				observers: make(map[htgObserver]struct{}),
			}
		}

		detach = func() {
			g.mu.Lock()
			delete(mount.observers, observer)
			if len(mount.observers) == 0 && g.mount == mount {
				g.mount = nil
			}
			g.mu.Unlock()
		}

		observer = create(mount.value, detach)

		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return hutClosedError()
		}
		if err := hutCheckAborted(ctx); err != nil {
			g.mu.Unlock()
			return err
		}
		g.mount = mount
		mount.observers[observer] = struct{}{}
		g.mu.Unlock()
		return nil
	})
	if err != nil {
		var zero O
		return zero, nil, err
	}
	return observer, detach, nil
}

func (g *TaskGraphView) build(ctx chord.Context) (TaskGraph, error) {
	var records []TaskRecord
	for _, status := range htgLiveStatuses {
		s := status
		pageItems, err := hutScanAll(func(cursor Cursor) (Page[TaskRecord, Cursor], error) {
			return g.storage.ScanTasks(ctx, TaskQuery{Status: s}, htgScanPageSize, cursor)
		})
		if err != nil {
			return TaskGraph{}, err
		}
		records = append(records, pageItems...)
	}
	slices.SortFunc(records, func(a, b TaskRecord) int {
		return cmp.Compare(a.Id, b.Id)
	})

	tasks := make(map[string]TaskGraphNode, len(records))
	for _, record := range records {
		ownerId := record.Id
		owned, err := hutScanAll(func(cursor Cursor) (Page[ConversationRecord, Cursor], error) {
			return g.storage.ScanConversations(ctx, ConversationQuery{OwnerTaskId: &ownerId}, htgScanPageSize, cursor)
		})
		if err != nil {
			return TaskGraph{}, err
		}
		conversations := make([]ConversationId, len(owned))
		for i, c := range owned {
			conversations[i] = c.Id
		}
		slices.Sort(conversations)
		tasks[strconv.FormatInt(int64(record.Id), 10)] = htgNodeOf(record, conversations)
	}
	return TaskGraph{Tasks: tasks}, nil
}

func (g *TaskGraphView) onClose() {
	g.mu.Lock()
	g.closed = true
	var observers []htgObserver
	if g.mount != nil {
		for obs := range g.mount.observers {
			observers = append(observers, obs)
		}
	}
	g.mount = nil
	g.mu.Unlock()

	for _, obs := range observers {
		obs.CloseSession()
	}
}

func (g *TaskGraphView) onPublication(publication CommitPublication, ctx chord.Context) {
	g.mu.Lock()
	mount := g.mount
	if mount == nil {
		g.mu.Unlock()
		return
	}
	next, ops := htgAdvanceMount(mount.value, publication)
	if len(ops) == 0 {
		g.mu.Unlock()
		return
	}
	mount.value = next
	observers := make([]htgObserver, 0, len(mount.observers))
	for obs := range mount.observers {
		observers = append(observers, obs)
	}
	g.mu.Unlock()

	frameCtx := chord.WithoutAbortSignal(ctx)
	for _, obs := range observers {
		obs.Advance(next, ops, frameCtx)
	}
}

func htgAdvanceMount(current TaskGraph, publication CommitPublication) (TaskGraph, []chord.Op) {
	var ops []chord.Op
	changed := make(map[string]*TaskGraphNode)
	nodeOf := func(key string) (TaskGraphNode, bool) {
		if c, seen := changed[key]; seen {
			if c == nil {
				return TaskGraphNode{}, false
			}
			return *c, true
		}
		n, ok := current.Tasks[key]
		return n, ok
	}

	for _, change := range publication.Changes {
		taskChange, ok := change.(StorageWriteTask)
		if !ok {
			continue
		}
		record := taskChange.Value
		key := strconv.FormatInt(int64(record.Id), 10)
		prev, hasPrev := nodeOf(key)
		if record.State.Status == TaskStateTerminal {
			if !hasPrev {
				continue
			}
			ops = append(ops, chord.OpDelete{
				Path: chord.Path{chord.SegString("tasks"), chord.SegString(key)},
			})
			changed[key] = nil
			continue
		}
		var convs []ConversationId
		if hasPrev && len(prev.Conversations) > 0 {
			convs = append([]ConversationId(nil), prev.Conversations...)
		} else {
			convs = []ConversationId{}
		}
		next := htgNodeOf(record, convs)
		if hasPrev && htgNodesEqual(prev, next) {
			continue
		}
		encoded, err := jsonx.Encode(next)
		if err != nil {
			encoded = next
		}
		ops = append(ops, chord.OpSet{
			Path:  chord.Path{chord.SegString("tasks"), chord.SegString(key)},
			Value: encoded,
		})
		nCopy := next
		changed[key] = &nCopy
	}

	created := make(map[string][]ConversationId)
	for _, change := range publication.Changes {
		convChange, ok := change.(StorageWriteConversation)
		if !ok || convChange.Value.Owner == nil {
			continue
		}
		key := strconv.FormatInt(int64(convChange.Value.Owner.TaskId), 10)
		if _, ok := nodeOf(key); ok {
			created[key] = append(created[key], convChange.Value.Id)
		}
	}
	for key, ids := range created {
		n, _ := nodeOf(key)
		merged := make([]ConversationId, 0, len(n.Conversations)+len(ids))
		merged = append(merged, n.Conversations...)
		merged = append(merged, ids...)
		slices.Sort(merged)
		encoded, err := jsonx.Encode(merged)
		if err != nil {
			encoded = merged
		}
		ops = append(ops, chord.OpSet{
			Path:  chord.Path{chord.SegString("tasks"), chord.SegString(key), chord.SegString("conversations")},
			Value: encoded,
		})
		n.Conversations = merged
		nCopy := n
		changed[key] = &nCopy
	}

	if len(ops) == 0 {
		return current, nil
	}

	nextTasks := make(map[string]TaskGraphNode, len(current.Tasks)+len(changed))
	for k, v := range current.Tasks {
		nextTasks[k] = v
	}
	for k, v := range changed {
		if v == nil {
			delete(nextTasks, k)
		} else {
			nextTasks[k] = *v
		}
	}
	return TaskGraph{Tasks: nextTasks}, ops
}

func htgNodeOf(record TaskRecord, conversations []ConversationId) TaskGraphNode {
	var owner *TaskId
	if record.Owner != nil {
		id := *record.Owner
		owner = &id
	}
	if conversations == nil {
		conversations = []ConversationId{}
	}
	return TaskGraphNode{
		Id:             record.Id,
		Kind:           record.Kind,
		ConversationId: record.ConversationId,
		Owner:          owner,
		Background:     record.Background,
		AbortRequested: record.AbortRequested,
		State:          htgStateOf(record),
		Conversations:  conversations,
	}
}

func htgStateOf(record TaskRecord) TaskGraphState {
	switch record.State.Status {
	case TaskStatePending, TaskStateRunning:
		return TaskGraphState{
			Status: string(record.State.Status),
			Phase:  htgPhaseOf(record.State.Checkpoint),
		}
	case TaskStateWaiting:
		on := append([]TaskId(nil), record.State.On...)
		if on == nil {
			on = []TaskId{}
		}
		var policy *JoinPolicy
		if record.State.Policy != "" {
			p := record.State.Policy
			policy = &p
		}
		return TaskGraphState{
			Status: string(TaskStateWaiting),
			Phase:  htgPhaseOf(record.State.Checkpoint),
			On:     on,
			Policy: policy,
		}
	default:
		var outcome TaskOutcomeStatus
		if record.State.Outcome != nil {
			outcome = record.State.Outcome.Status
		}
		return TaskGraphState{
			Status:  string(TaskStateCompleting),
			Outcome: outcome,
		}
	}
}

func htgPhaseOf(checkpoint JsonValue) string {
	if obj, ok := checkpoint.(*jsonx.Object); ok && obj != nil {
		if phase, ok := obj.Get("phase"); ok {
			if s, ok := phase.(string); ok {
				return s
			}
		}
	}
	return ""
}

func htgNodesEqual(a, b TaskGraphNode) bool {
	ba, errA := jsonx.Marshal(a)
	bb, errB := jsonx.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ba) == string(bb)
}
