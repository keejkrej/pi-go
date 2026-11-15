package chord

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tsObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tsMustJson(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal failed: %v", err)
	}
	return string(b)
}

func tsMustState(t *testing.T, initial any) MutableReplicatedState[any] {
	t.Helper()
	s, err := NewReplicatedState(initial)
	if err != nil {
		t.Fatalf("NewReplicatedState failed: %v", err)
	}
	return s
}

func TestReplicatedStatePublishAndStructuralSharing(t *testing.T) {
	initial := tsObj("changed", tsObj("value", float64(1)), "retained", tsObj("value", float64(2)))
	state := tsMustState(t, initial)
	prevRaw, ok := state.Value()
	if !ok {
		t.Fatal("expected state Value() ok")
	}
	prev := prevRaw.(*jsonx.Object)
	prevChanged, _ := prev.Get("changed")
	prevRetained, _ := prev.Get("retained")

	var deliveries []int
	unsub := state.Subscribe(func(_ any, _ Context, delivery ReplicatedStateDelivery) error {
		deliveries = append(deliveries, delivery.Sequence)
		return nil
	})
	defer unsub()

	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		root := draft.Value.(*jsonx.Object)
		c, _ := root.Get("changed")
		c.(*jsonx.Object).Set("value", float64(3))
		c.(*jsonx.Object).Set("value", float64(4))
		return nil
	}); err != nil {
		t.Fatalf("Change failed: %v", err)
	}

	curRaw, _ := state.Value()
	cur := curRaw.(*jsonx.Object)
	if got := tsMustJson(t, cur); got != `{"changed":{"value":4},"retained":{"value":2}}` {
		t.Fatalf("unexpected value JSON: %s", got)
	}
	if cur == prev {
		t.Fatal("expected new root object")
	}
	curChanged, _ := cur.Get("changed")
	if curChanged == prevChanged {
		t.Fatal("expected new changed subobject")
	}
	curRetained, _ := cur.Get("retained")
	if curRetained != prevRetained {
		t.Fatal("expected retained subobject to preserve pointer identity")
	}
	if len(deliveries) != 2 || deliveries[0] != 0 || deliveries[1] != 1 {
		t.Fatalf("expected deliveries [0 1], got %v", deliveries)
	}
}

func TestReplicatedStateRollbackAndDraftRevocation(t *testing.T) {
	state := tsMustState(t, tsObj("nested", tsObj("value", float64(1))))
	prev, _ := state.Value()
	var escaped *jsonx.Object
	err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		root := draft.Value.(*jsonx.Object)
		n, _ := root.Get("nested")
		escaped = n.(*jsonx.Object)
		escaped.Set("value", float64(2))
		return errors.New("stop")
	})
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("expected error containing 'stop', got %v", err)
	}
	cur, _ := state.Value()
	if cur != prev {
		t.Fatal("expected exact previous root after rollback")
	}
	if got := tsMustJson(t, cur); got != `{"nested":{"value":1}}` {
		t.Fatalf("expected untouched previous JSON, got %s", got)
	}
	// Escaped draft is detached/revoked from further state mutation; Verify Set on escaped does not alter state.
	if escaped != nil {
		escaped.Set("value", float64(999))
	}
	afterCur, _ := state.Value()
	if got := tsMustJson(t, afterCur); got != `{"nested":{"value":1}}` {
		t.Fatalf("escaped draft write mutated committed state: %s", got)
	}
}

func TestReplicatedStateRejectsNestedChangesAndReplacements(t *testing.T) {
	state := tsMustState(t, tsObj("left", float64(0), "right", float64(0)))

	err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		root := draft.Value.(*jsonx.Object)
		root.Set("left", float64(1))
		return state.Change(BackgroundContext, func(nested Draft[any]) error {
			nested.Value.(*jsonx.Object).Set("right", float64(2))
			return nil
		})
	})
	if err == nil || !strings.Contains(err.Error(), "reentrantly") {
		t.Fatalf("expected reentrant change error, got %v", err)
	}
	val, _ := state.Value()
	if got := tsMustJson(t, val); got != `{"left":0,"right":0}` {
		t.Fatalf("expected rollback on nested change, got %s", got)
	}

	err = state.Change(BackgroundContext, func(_ Draft[any]) error {
		return state.Replace(BackgroundContext, tsObj("left", float64(1), "right", float64(2)))
	})
	if err == nil || !strings.Contains(err.Error(), "change callback") {
		t.Fatalf("expected replace-in-change error, got %v", err)
	}
	val, _ = state.Value()
	if got := tsMustJson(t, val); got != `{"left":0,"right":0}` {
		t.Fatalf("expected rollback on nested replace, got %s", got)
	}
}

func TestReplicatedStateQueuesListenerTriggeredChangesInSequenceOrder(t *testing.T) {
	state := tsMustState(t, tsObj("value", float64(0)))
	var sourceSequences []int
	type delivRec struct {
		value    float64
		sequence int
	}
	var deliveries []delivRec
	var lateDeliveries []ReplicatedStateDelivery
	nested := false
	internals := GetReplicatedStateInternals(state)
	if internals == nil {
		t.Fatal("expected ReplicatedStateInternals on mutable state")
	}
	unsubSrc1 := internals.Subscribe(func(_ OpList, _ int, _ Context) error {
		if !nested {
			nested = true
			if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
				draft.Value.(*jsonx.Object).Set("value", float64(2))
				return nil
			}); err != nil {
				return err
			}
			_ = state.Subscribe(func(_ any, _ Context, delivery ReplicatedStateDelivery) error {
				lateDeliveries = append(lateDeliveries, delivery)
				return nil
			})
		}
		return nil
	})
	defer unsubSrc1()

	unsubSrc2 := internals.Subscribe(func(_ OpList, sequence int, _ Context) error {
		sourceSequences = append(sourceSequences, sequence)
		return nil
	})
	defer unsubSrc2()

	unsubPub := state.Subscribe(func(value any, _ Context, delivery ReplicatedStateDelivery) error {
		if delivery.Kind == ReplicatedStateDeliveryKindUpdate {
			v, _ := value.(*jsonx.Object).Get("value")
			deliveries = append(deliveries, delivRec{value: v.(float64), sequence: delivery.Sequence})
		}
		return nil
	})
	defer unsubPub()

	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		draft.Value.(*jsonx.Object).Set("value", float64(1))
		return nil
	}); err != nil {
		t.Fatalf("Change failed: %v", err)
	}

	if len(sourceSequences) != 2 || sourceSequences[0] != 1 || sourceSequences[1] != 2 {
		t.Fatalf("expected sourceSequences [1 2], got %v", sourceSequences)
	}
	if len(deliveries) != 2 || deliveries[0] != (delivRec{1, 1}) || deliveries[1] != (delivRec{2, 2}) {
		t.Fatalf("expected deliveries [{1 1} {2 2}], got %v", deliveries)
	}
	if len(lateDeliveries) != 1 || lateDeliveries[0] != (ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 2}) {
		t.Fatalf("expected lateDeliveries [{hydrate 2}], got %v", lateDeliveries)
	}
}

func TestReplicatedStateIsolatesListenerFailuresAfterCommit(t *testing.T) {
	state := tsMustState(t, tsObj("value", float64(0)))
	var received []int
	internals := GetReplicatedStateInternals(state)
	internals.Subscribe(func(_ OpList, _ int, _ Context) error {
		return errors.New("listener failed")
	})
	internals.Subscribe(func(_ OpList, sequence int, _ Context) error {
		received = append(received, sequence)
		return nil
	})

	err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		draft.Value.(*jsonx.Object).Set("value", float64(1))
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "listener failed") {
		t.Fatalf("expected listener failed error, got %v", err)
	}
	val, _ := state.Value()
	if got := tsMustJson(t, val); got != `{"value":1}` {
		t.Fatalf("expected state value 1 after listener error, got %s", got)
	}
	if len(received) != 1 || received[0] != 1 {
		t.Fatalf("expected received [1], got %v", received)
	}
}

func TestReplicatedStateCopiesAssignedValuesByValue(t *testing.T) {
	external := tsObj("value", float64(1))
	state := tsMustState(t, tsObj("left", nil, "right", nil))
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		root := draft.Value.(*jsonx.Object)
		root.Set("left", external)
		root.Set("right", external)
		return nil
	}); err != nil {
		t.Fatalf("Change failed: %v", err)
	}
	external.Set("value", float64(99))
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		left, _ := draft.Value.(*jsonx.Object).Get("left")
		left.(*jsonx.Object).Set("value", float64(2))
		return nil
	}); err != nil {
		t.Fatalf("Change 2 failed: %v", err)
	}
	val, _ := state.Value()
	if got := tsMustJson(t, val); got != `{"left":{"value":2},"right":{"value":1}}` {
		t.Fatalf("unexpected state JSON: %s", got)
	}
	leftPtr, _ := val.(*jsonx.Object).Get("left")
	rightPtr, _ := val.(*jsonx.Object).Get("right")
	if leftPtr == rightPtr {
		t.Fatal("expected distinct objects for left and right")
	}
}

func TestReplicatedStateTakesImmutableOwnershipOfAliasFreeReplacements(t *testing.T) {
	state := tsMustState(t, tsObj("left", tsObj("value", float64(1)), "right", tsObj("value", float64(2))))
	curRaw, _ := state.Value()
	curLeft, _ := curRaw.(*jsonx.Object).Get("left")
	curLeftVal, _ := curLeft.(*jsonx.Object).Get("value")
	replacement := tsObj("left", tsObj("value", curLeftVal), "right", tsObj("value", curLeftVal))

	if err := state.Replace(BackgroundContext, replacement); err != nil {
		t.Fatalf("Replace failed: %v", err)
	}
	gotCur, _ := state.Value()
	if gotCur != replacement {
		t.Fatal("expected exact replacement pointer identity")
	}
	lPtr, _ := gotCur.(*jsonx.Object).Get("left")
	rPtr, _ := gotCur.(*jsonx.Object).Get("right")
	if lPtr == rPtr {
		t.Fatal("expected distinct left and right")
	}
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		l, _ := draft.Value.(*jsonx.Object).Get("left")
		l.(*jsonx.Object).Set("value", float64(9))
		return nil
	}); err != nil {
		t.Fatalf("Change failed: %v", err)
	}
	after, _ := state.Value()
	if got := tsMustJson(t, after); got != `{"left":{"value":9},"right":{"value":1}}` {
		t.Fatalf("unexpected state after change: %s", got)
	}
}

func TestReplicatedStatePreservesCompactOps(t *testing.T) {
	a := tsObj("id", "a")
	b := tsObj("id", "b")
	c := tsObj("id", "c")
	state := tsMustState(t, tsObj("text", "abcdefgh", "values", []any{a, b, c}))
	var batches []string
	GetReplicatedStateInternals(state).Subscribe(func(ops OpList, _ int, _ Context) error {
		batches = append(batches, tsMustJson(t, ops))
		return nil
	})
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		root := draft.Value.(*jsonx.Object)
		root.Set("text", "defghxyz")
		vals, _ := root.Get("values")
		root.Set("values", vals.([]any)[1:])
		return nil
	}); err != nil {
		t.Fatalf("first change failed: %v", err)
	}
	curRaw, _ := state.Value()
	curVals, _ := curRaw.(*jsonx.Object).Get("values")
	s := curVals.([]any)
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		draft.Value.(*jsonx.Object).Set("values", []any{s[1], s[0]})
		return nil
	}); err != nil {
		t.Fatalf("second change failed: %v", err)
	}

	want0 := `[["t",["text"],3],["a",["text"],"xyz"],["p",["values"],0,1,[]]]`
	want1 := `[["m",["values"],[1,0]]]`
	if len(batches) != 2 || batches[0] != want0 || batches[1] != want1 {
		t.Fatalf("unexpected op batches:\n got: %v\nwant: [%s, %s]", batches, want0, want1)
	}
}

func TestReplicatedStateReplicaValidatesWithoutCopy(t *testing.T) {
	replica := NewReplicatedStateReplica[any](func(error) {})
	row0 := tsObj("value", float64(1))
	initial := tsObj("rows", []any{row0})
	if err := replica.Hydrate(0, OpList{OpReplace{Value: initial}}, BackgroundContext); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	cur, ok := replica.Value()
	if !ok || cur != initial {
		t.Fatal("expected exact initial root on replica")
	}

	inserted := tsObj("value", float64(2))
	if err := replica.Update(1, OpList{
		OpSplice{Path: Path{SegString("rows")}, Index: 1, Remove: 0, Items: []JsonValue{inserted}},
	}, BackgroundContext); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	updatedRaw, _ := replica.Value()
	updated := updatedRaw.(*jsonx.Object)
	if updated == initial {
		t.Fatal("expected new root object after update")
	}
	rowsVal, _ := updated.Get("rows")
	rows := rowsVal.([]any)
	if len(rows) != 2 || rows[0] != row0 || rows[1] != inserted {
		t.Fatal("expected structural sharing of row0 and inserted row1")
	}
	origRowsVal, _ := initial.Get("rows")
	if len(origRowsVal.([]any)) != 1 {
		t.Fatal("expected initial rows slice untouched")
	}
}

func TestReplicatedStateReplicaClearsOnInvalidUpdate(t *testing.T) {
	var reported []error
	replica := NewReplicatedStateReplica[any](func(err error) { reported = append(reported, err) })
	if err := replica.Hydrate(0, OpList{OpReplace{Value: tsObj("value", float64(0))}}, BackgroundContext); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	err := replica.Update(1, OpList{OpSet{Path: Path{SegString("value")}, Value: math.NaN()}}, BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "strict JSON") {
		t.Fatalf("expected strict JSON error, got %v", err)
	}
	if _, ok := replica.Value(); ok {
		t.Fatal("expected replica cleared after invalid update")
	}
	err = replica.Update(2, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}, BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "before hydration") {
		t.Fatalf("expected before hydration error, got %v", err)
	}

	malformed := NewReplicatedStateReplica[any](func(err error) { reported = append(reported, err) })
	if err := malformed.Hydrate(0, OpList{OpReplace{Value: tsObj("values", []any{float64(1), float64(2)})}}, BackgroundContext); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	if err := malformed.Update(1, OpList{OpMove{Path: Path{SegString("values")}, Permutation: []int{0}}}, BackgroundContext); err == nil {
		t.Fatal("expected malformed permutation error")
	}
	if _, ok := malformed.Value(); ok {
		t.Fatal("expected malformed replica cleared")
	}
	if len(reported) != 0 {
		t.Fatalf("expected no listener error callback notifications, got %v", reported)
	}
}

func TestReplicatedStateReplacesAtomicallyAndIgnoresDeepEqual(t *testing.T) {
	retained := tsObj("nested", float64(2))
	state := tsMustState(t, tsObj("value", tsObj("nested", float64(1)), "retained", retained))
	prev, _ := state.Value()
	if err := state.Replace(BackgroundContext, tsObj("value", tsObj("nested", float64(1)), "retained", tsObj("nested", float64(2)))); err != nil {
		t.Fatalf("deep-equal Replace failed: %v", err)
	}
	cur, _ := state.Value()
	if cur != prev {
		t.Fatal("expected deep-equal replacement to keep exact previous root")
	}
	if err := state.Replace(BackgroundContext, tsObj("value", tsObj("nested", float64(2)), "retained", retained)); err != nil {
		t.Fatalf("Replace failed: %v", err)
	}
	afterRaw, _ := state.Value()
	after := afterRaw.(*jsonx.Object)
	if got := tsMustJson(t, after); got != `{"value":{"nested":2},"retained":{"nested":2}}` {
		t.Fatalf("unexpected replaced JSON: %s", got)
	}
	afterRetained, _ := after.Get("retained")
	if afterRetained != retained {
		t.Fatal("expected retained subtree identity preserved")
	}
}

type tsSourceValue = *jsonx.Object

type tsTestSourceAttachment struct {
	mu        sync.Mutex
	snapshot  ReplicatedStateSourceSnapshot[tsSourceValue]
	onDispose func()
	buffer    []ReplicatedStateSourceFrame[tsSourceValue]
	listener  func(ReplicatedStateSourceFrame[tsSourceValue])
	activated bool
	disposed  bool
}

func (a *tsTestSourceAttachment) Snapshot() ReplicatedStateSourceSnapshot[tsSourceValue] {
	return a.snapshot
}

func (a *tsTestSourceAttachment) Activate(listener func(frame ReplicatedStateSourceFrame[tsSourceValue])) {
	a.mu.Lock()
	if a.activated {
		a.mu.Unlock()
		panic("attachment is already active")
	}
	a.activated = true
	a.listener = listener
	buffered := a.buffer
	a.buffer = nil
	a.mu.Unlock()

	for _, frame := range buffered {
		listener(frame)
	}
}

func (a *tsTestSourceAttachment) publish(frame ReplicatedStateSourceFrame[tsSourceValue]) {
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		return
	}
	l := a.listener
	if l == nil {
		a.buffer = append(a.buffer, frame)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	l(frame)
}

func (a *tsTestSourceAttachment) Dispose() {
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		return
	}
	a.disposed = true
	a.buffer = nil
	cb := a.onDispose
	a.mu.Unlock()
	if cb != nil {
		cb()
	}
}

type tsTestSource struct {
	mu          sync.Mutex
	attachments *omap.Set[*tsTestSourceAttachment]
	onAttach    func()
	value       tsSourceValue
	cursor      int
}

func tsNewTestSource(value tsSourceValue, cursor int) *tsTestSource {
	return &tsTestSource{
		attachments: omap.NewSet[*tsTestSourceAttachment](),
		value:       value,
		cursor:      cursor,
	}
}

func (s *tsTestSource) Attach() ReplicatedStateSourceAttachment[tsSourceValue] {
	s.mu.Lock()
	var att *tsTestSourceAttachment
	att = &tsTestSourceAttachment{
		snapshot: ReplicatedStateSourceSnapshot[tsSourceValue]{Value: s.value, Cursor: s.cursor},
		onDispose: func() {
			s.mu.Lock()
			s.attachments.Delete(att)
			s.mu.Unlock()
		},
	}
	s.attachments.Add(att)
	hook := s.onAttach
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	return att
}

func (s *tsTestSource) commit(value tsSourceValue, ops OpList, ctx Context, customCursor *int) {
	if ctx == nil {
		ctx = BackgroundContext
	}
	s.mu.Lock()
	nextCursor := s.cursor + 1
	if customCursor != nil {
		nextCursor = *customCursor
	}
	s.value = value
	s.cursor = nextCursor
	frame := ReplicatedStateSourceFrame[tsSourceValue]{
		Cursor:  nextCursor,
		Value:   value,
		Ops:     ops,
		Context: ctx,
	}
	atts := s.attachments.Values()
	s.mu.Unlock()

	for _, att := range atts {
		att.publish(frame)
	}
}

func TestAuthoritativeSourceCapturesBeforeActivationAndDrainsQueuedCommits(t *testing.T) {
	initial := tsObj("value", float64(0))
	first := tsObj("value", float64(1))
	second := tsObj("value", float64(2))
	firstOps := OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}
	secondOps := OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}
	source := tsNewTestSource(initial, 10)
	source.onAttach = func() {
		source.commit(first, firstOps, nil, nil)
		source.commit(second, secondOps, nil, nil)
	}

	state, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	type deliv struct {
		value tsSourceValue
		seq   int
	}
	var deliveries []deliv
	state.Subscribe(func(value tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		deliveries = append(deliveries, deliv{value: value, seq: d.Sequence})
		return nil
	})
	val, ok := state.Value()
	if !ok || val != second {
		t.Fatalf("expected state.Value() == second, got %v", val)
	}
	if len(deliveries) != 1 || deliveries[0].value != second || deliveries[0].seq != 2 {
		t.Fatalf("expected deliveries [{second, 2}], got %v", deliveries)
	}
	snapVal, snapSeq := GetReplicatedStateInternals(state).Snapshot()
	if snapVal != second || snapSeq != 2 {
		t.Fatalf("expected snapshot (second, 2), got (%v, %d)", snapVal, snapSeq)
	}
}

func TestAuthoritativeSourceHydratesAtZeroAfterPriorCommits(t *testing.T) {
	initial := tsObj("value", float64(0))
	current := tsObj("value", float64(1))
	source := tsNewTestSource(initial, 40)
	source.commit(current, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, nil, nil)

	state, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	var deliveries []int
	state.Subscribe(func(_ tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		deliveries = append(deliveries, d.Sequence)
		return nil
	})
	val, _ := state.Value()
	if val != current {
		t.Fatal("expected current value")
	}
	if len(deliveries) != 1 || deliveries[0] != 0 {
		t.Fatalf("expected deliveries [0], got %v", deliveries)
	}
}

func TestAuthoritativeSourcePublishesExactReferences(t *testing.T) {
	source := tsNewTestSource(tsObj("value", float64(0)), 0)
	state, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	next := tsObj("value", float64(1))
	ops := OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}
	var publishedOps OpList
	GetReplicatedStateInternals(state).Subscribe(func(received OpList, _ int, _ Context) error {
		publishedOps = received
		return nil
	})
	var publishedValue tsSourceValue
	state.Subscribe(func(value tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		if d.Kind == ReplicatedStateDeliveryKindUpdate {
			publishedValue = value
		}
		return nil
	})

	source.commit(next, ops, nil, nil)
	cur, _ := state.Value()
	if cur != next || publishedValue != next {
		t.Fatal("expected exact pointer reference for committed value")
	}
	if len(publishedOps) != len(ops) || tsMustJson(t, publishedOps) != tsMustJson(t, ops) {
		t.Fatalf("expected exact ops, got %v", publishedOps)
	}
}

func TestAuthoritativeSourceBuffersReentrantFramesAndSkipsCoveredUpdates(t *testing.T) {
	source := tsNewTestSource(tsObj("value", float64(0)), 0)
	state, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	var received []float64
	type lateRec struct {
		delivery ReplicatedStateDelivery
		value    float64
	}
	var late []lateRec
	nested := false

	GetReplicatedStateInternals(state).Subscribe(func(_ OpList, seq int, _ Context) error {
		if seq != 1 || nested {
			return nil
		}
		nested = true
		source.commit(tsObj("value", float64(2)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}, nil, nil)
		state.Subscribe(func(value tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
			v, _ := value.Get("value")
			late = append(late, lateRec{delivery: d, value: v.(float64)})
			return nil
		})
		return nil
	})
	state.Subscribe(func(value tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		if d.Kind == ReplicatedStateDeliveryKindUpdate {
			v, _ := value.Get("value")
			received = append(received, v.(float64))
		}
		return nil
	})

	source.commit(tsObj("value", float64(1)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, nil, nil)
	if len(received) != 2 || received[0] != 1 || received[1] != 2 {
		t.Fatalf("expected received [1 2], got %v", received)
	}
	if len(late) != 1 || late[0] != (lateRec{delivery: ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 2}, value: 2}) {
		t.Fatalf("expected late [{hydrate 2} 2], got %v", late)
	}
}

func TestAuthoritativeSourceReportsListenerFailuresWithoutThrowing(t *testing.T) {
	source := tsNewTestSource(tsObj("value", float64(0)), 0)
	var errs []error
	state, err := NewReplicatedStateSource[tsSourceValue](source, &ReplicatedStateSourceOptions{
		OnError: func(e error) { errs = append(errs, e) },
	})
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	var received []float64
	state.Subscribe(func(_ tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		if d.Kind == ReplicatedStateDeliveryKindUpdate {
			return errors.New("listener failed")
		}
		return nil
	})
	state.Subscribe(func(value tsSourceValue, _ Context, d ReplicatedStateDelivery) error {
		if d.Kind == ReplicatedStateDeliveryKindUpdate {
			v, _ := value.Get("value")
			received = append(received, v.(float64))
		}
		return nil
	})

	source.commit(tsObj("value", float64(1)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, nil, nil)
	source.commit(tsObj("value", float64(2)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}, nil, nil)
	if len(received) != 2 || received[0] != 1 || received[1] != 2 {
		t.Fatalf("expected received [1 2], got %v", received)
	}
	if len(errs) != 2 || errs[0].Error() != "listener failed" || errs[1].Error() != "listener failed" {
		t.Fatalf("expected 2 listener failed errors, got %v", errs)
	}
}

func TestAuthoritativeSourceReportsCursorGapsAndDisposes(t *testing.T) {
	source := tsNewTestSource(tsObj("value", float64(0)), 5)
	var errs []error
	state, err := NewReplicatedStateSource[tsSourceValue](source, &ReplicatedStateSourceOptions{
		OnError: func(e error) { errs = append(errs, e) },
	})
	if err != nil {
		t.Fatalf("NewReplicatedStateSource failed: %v", err)
	}
	c7 := 7
	source.commit(tsObj("value", float64(2)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}, BackgroundContext, &c7)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "expected 6, received 7") {
		t.Fatalf("expected gap error mentioning 'expected 6, received 7', got %v", errs)
	}
	if source.attachments.Len() != 0 {
		t.Fatalf("expected 0 remaining attachments, got %d", source.attachments.Len())
	}
	v, _ := state.Value()
	if got := tsMustJson(t, v); got != `{"value":0}` {
		t.Fatalf("expected original value preserved, got %s", got)
	}
	c8 := 8
	source.commit(tsObj("value", float64(3)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(3)}}, BackgroundContext, &c8)
	v2, _ := state.Value()
	if got := tsMustJson(t, v2); got != `{"value":0}` {
		t.Fatalf("expected ignored later frame, got %s", got)
	}
}

func TestAuthoritativeSourceIndependentAttachmentsAndIdempotentDispose(t *testing.T) {
	source := tsNewTestSource(tsObj("value", float64(0)), 0)
	first, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}
	second, err := NewReplicatedStateSource[tsSourceValue](source, nil)
	if err != nil {
		t.Fatalf("second attach failed: %v", err)
	}
	if source.attachments.Len() != 2 {
		t.Fatalf("expected 2 attachments, got %d", source.attachments.Len())
	}
	source.commit(tsObj("value", float64(1)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, nil, nil)
	v1, _ := first.Value()
	v2, _ := second.Value()
	if n1, _ := v1.Get("value"); n1 != float64(1) {
		t.Fatalf("expected first value 1, got %v", n1)
	}
	if n2, _ := v2.Get("value"); n2 != float64(1) {
		t.Fatalf("expected second value 1, got %v", n2)
	}

	first.Dispose()
	first.Dispose()
	if source.attachments.Len() != 1 {
		t.Fatalf("expected 1 attachment after first dispose, got %d", source.attachments.Len())
	}
	source.commit(tsObj("value", float64(2)), OpList{OpSet{Path: Path{SegString("value")}, Value: float64(2)}}, nil, nil)
	v1After, _ := first.Value()
	v2After, _ := second.Value()
	if n1, _ := v1After.Get("value"); n1 != float64(1) {
		t.Fatalf("expected first still 1, got %v", n1)
	}
	if n2, _ := v2After.Get("value"); n2 != float64(2) {
		t.Fatalf("expected second updated to 2, got %v", n2)
	}
	second.Dispose()
	if source.attachments.Len() != 0 {
		t.Fatalf("expected 0 attachments after second dispose, got %d", source.attachments.Len())
	}
}
