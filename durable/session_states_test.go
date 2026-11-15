// Ported from packages/durable/test/session-states.test.ts (pi v1.0.0).

package durable_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsstMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%s) failed: %v", def.Kind, err)
	}
	return tok
}

var (
	tsstStateDoc = func() *durable.DocToken {
		tok, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "state.state",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject {
				return jsonx.ObjectOf("value", float64(0), "retained", jsonx.ObjectOf("label", "stable"))
			},
		})
		if err != nil {
			panic(err)
		}
		return tok
	}()

	tsstFamilyDoc = func() *durable.DocFamilyToken {
		tok, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
			Kind:    "state.family",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				return jsonx.ObjectOf("value", float64(len(seed.(string))))
			},
		})
		if err != nil {
			panic(err)
		}
		return tok
	}()
)

func tsstCreateState(t *testing.T) *testkit.TestSession {
	t.Helper()
	ts := testkit.OpenTestSession()
	if err := ts.Session.Commit(testkit.TestContext, func(tx durable.Tx) error {
		_, err := tx.Doc(tsstStateDoc)
		return err
	}); err != nil {
		t.Fatalf("createState commit failed: %v", err)
	}
	testkit.Flush()
	return ts
}

func tsstValNum(v durable.JsonObject) float64 {
	if v == nil {
		return -1
	}
	n, _ := v.Get("value")
	f, _ := n.(float64)
	return f
}

func TestSessionDocumentStates(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("never creates an absent document", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		commits := len(ts.Storage.Commits())
		if s, err := ts.Session.DocumentState(ctx, tsstStateDoc); err != nil || s != nil {
			t.Fatalf("expected nil DocumentState for absent stateDoc, got (%v, %v)", s, err)
		}
		if s, err := ts.Session.DocumentState(ctx, tsstFamilyDoc, "missing"); err != nil || s != nil {
			t.Fatalf("expected nil DocumentState for absent family member, got (%v, %v)", s, err)
		}
		if len(ts.Storage.Commits()) != commits || ts.Storage.MintCount() != 0 {
			t.Fatalf("unexpected commit or mint on absent DocumentState")
		}
	})

	t.Run("returns an immediately hydrated read-only state with contiguous Chord deliveries", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		baseline, _ := ts.Session.Snapshot(ctx, tsstStateDoc)
		state, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()

		type deliveryRec struct {
			value    durable.JsonObject
			sequence int
		}
		var mu sync.Mutex
		var deliveries []deliveryRec
		state.Subscribe(func(value durable.JsonObject, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
			mu.Lock()
			deliveries = append(deliveries, deliveryRec{value: value, sequence: delivery.Sequence})
			mu.Unlock()
			return nil
		})
		if got, ok := state.Value(); !ok || got != baseline {
			t.Fatalf("initial state.Value() = (%v, %v), want %v", got, ok, baseline)
		}

		for _, next := range []float64{1, 2} {
			v := next
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, err := tx.Doc(tsstStateDoc)
				if err != nil {
					return err
				}
				d.Value.Set("value", v)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", v, err)
			}
		}

		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(deliveries) == 3, nil
		}); err != nil {
			t.Fatalf("Eventually deliveries failed: %v", err)
		}
		mu.Lock()
		var seqs []int
		var vals []float64
		for _, d := range deliveries {
			seqs = append(seqs, d.sequence)
			vals = append(vals, tsstValNum(d.value))
		}
		mu.Unlock()
		if !reflect.DeepEqual(seqs, []int{0, 1, 2}) {
			t.Fatalf("seqs = %v, want [0 1 2]", seqs)
		}
		if !reflect.DeepEqual(vals, []float64{0, 1, 2}) {
			t.Fatalf("vals = %v, want [0 1 2]", vals)
		}
		snap, _ := ts.Session.Snapshot(ctx, tsstStateDoc)
		if got, _ := state.Value(); got != snap {
			t.Fatalf("state.Value() pointer != snapshot pointer")
		}
	})

	t.Run("creates independent disposable states for one incarnation", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		first, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState 1 failed: %v", err)
		}
		second, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState 2 failed: %v", err)
		}
		if first == second {
			t.Fatalf("expected distinct AttachedReplicatedState handles")
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v1, _ := first.Value()
			v2, _ := second.Value()
			return tsstValNum(v1) == 1 && tsstValNum(v2) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually v=1 failed: %v", err)
		}

		first.Dispose()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(2))
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v2, _ := second.Value()
			return tsstValNum(v2) == 2, nil
		}); err != nil {
			t.Fatalf("Eventually second v=2 failed: %v", err)
		}
		v1, _ := first.Value()
		if tsstValNum(v1) != 1 {
			t.Fatalf("disposed first value = %v, want 1", v1)
		}
		second.Dispose()
	})

	t.Run("shares exact committed value and operation references with Chord", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		state, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()

		var opsMu sync.Mutex
		var receivedOps chord.OpList
		internals := chord.GetReplicatedStateInternals(state)
		if internals == nil {
			t.Fatalf("expected non-nil ReplicatedStateInternals")
		}
		unsub := internals.Subscribe(func(ops chord.OpList, _ int, _ chord.Context) error {
			opsMu.Lock()
			receivedOps = ops
			opsMu.Unlock()
			return nil
		})
		defer unsub()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(4))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			opsMu.Lock()
			defer opsMu.Unlock()
			return len(receivedOps) > 0, nil
		}); err != nil {
			t.Fatalf("Eventually internals failed: %v", err)
		}
		pubs := ts.Publications()
		published := testkit.DocumentChanges(pubs[len(pubs)-1])[0]
		stateVal, _ := state.Value()
		if stateVal != published.Value {
			t.Fatalf("state.Value() pointer != published.Value pointer")
		}
		opsMu.Lock()
		if len(receivedOps) != len(published.Ops) || &receivedOps[0] != &published.Ops[0] {
			t.Fatalf("expected shared ops backing array between internals and published")
		}
		opsMu.Unlock()
		snap, _ := ts.Session.Snapshot(ctx, tsstStateDoc)
		r1, _ := stateVal.Get("retained")
		r2, _ := snap.Get("retained")
		if r1 != r2 {
			t.Fatalf("expected retained branch pointer sharing")
		}
	})

	t.Run("captures a late baseline without redelivering an already covered commit", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		state, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()

		var mu sync.Mutex
		var deliveries []int
		state.Subscribe(func(_ durable.JsonObject, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
			mu.Lock()
			deliveries = append(deliveries, delivery.Sequence)
			mu.Unlock()
			return nil
		})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(deliveries) >= 1, nil
		}); err != nil {
			t.Fatalf("Eventually baseline delivery failed: %v", err)
		}
		testkit.Flush()
		v, _ := state.Value()
		if tsstValNum(v) != 1 {
			t.Fatalf("value = %v, want 1", v)
		}
		mu.Lock()
		if !reflect.DeepEqual(deliveries, []int{0}) {
			t.Fatalf("deliveries = %v, want [0]", deliveries)
		}
		mu.Unlock()
	})

	t.Run("publishes null retirement and never follows a replacement incarnation", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		oldState, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState old failed: %v", err)
		}
		defer oldState.Dispose()

		var mu sync.Mutex
		var retirementOps chord.OpList
		internals := chord.GetReplicatedStateInternals(oldState)
		unsub := internals.Subscribe(func(ops chord.OpList, _ int, _ chord.Context) error {
			mu.Lock()
			retirementOps = ops
			mu.Unlock()
			return nil
		})
		defer unsub()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if err := tx.RetireDoc(tsstStateDoc); err != nil {
				return err
			}
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(10))
			return nil
		}); err != nil {
			t.Fatalf("retire+recreate failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(retirementOps) > 0, nil
		}); err != nil {
			t.Fatalf("Eventually retirement failed: %v", err)
		}
		if v, ok := oldState.Value(); !ok || v != nil {
			t.Fatalf("oldState.Value() = (%v, %v), want (nil, true)", v, ok)
		}
		mu.Lock()
		if !reflect.DeepEqual(retirementOps, chord.OpList{chord.OpReplace{Value: nil}}) {
			t.Fatalf("retirementOps = %#v, want [r nil]", retirementOps)
		}
		mu.Unlock()

		replacement, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState replacement failed: %v", err)
		}
		defer replacement.Dispose()
		if v, _ := replacement.Value(); tsstValNum(v) != 10 {
			t.Fatalf("replacement initial = %v, want 10", v)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(11))
			return nil
		}); err != nil {
			t.Fatalf("update 11 failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := replacement.Value()
			return tsstValNum(v) == 11, nil
		}); err != nil {
			t.Fatalf("Eventually replacement 11 failed: %v", err)
		}
		if v, _ := oldState.Value(); v != nil {
			t.Fatalf("oldState should remain nil, got %v", v)
		}
	})

	t.Run("cold-loads a definition-free fork copy", func(t *testing.T) {
		copied := tsstMustDoc(t, durable.DocDefinition{
			Kind:    "state.copied",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", float64(0)) },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var entryId durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			if err != nil {
				return err
			}
			entryId = e.Id
			d, err := tx.Doc(copied, parentId)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(7))
			return nil
		}); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, entryId, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		reads := ts.Storage.DocumentReadCount()
		state, err := ts.Session.DocumentState(ctx, copied, childId)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()
		v, _ := state.Value()
		if !reflect.DeepEqual(v, jsonx.ObjectOf("value", float64(7))) {
			t.Fatalf("state value = %v", v)
		}
		if ts.Storage.DocumentReadCount() <= reads {
			t.Fatalf("expected document read on cold load of fork copy")
		}
	})

	t.Run("hydrates a migrated tracker without writing and skips an equal version-base update", func(t *testing.T) {
		old := tsstMustDoc(t, durable.DocDefinition{
			Kind:    "state.migration",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", float64(3)) },
		})
		current := tsstMustDoc(t, durable.DocDefinition{
			Kind:    "state.migration",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", float64(0), "migrated", false) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				v, _ := value.Get("value")
				return jsonx.ObjectOf("value", v, "migrated", true)
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		state, err := ts.Session.DocumentState(ctx, current)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()
		baseline, _ := state.Value()
		if !reflect.DeepEqual(baseline, jsonx.ObjectOf("value", float64(3), "migrated", true)) {
			t.Fatalf("baseline = %v", baseline)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits grew on read-only DocumentState hydration")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(current)
			return err
		}); err != nil {
			t.Fatalf("migration persist failed: %v", err)
		}
		testkit.Flush()
		if len(ts.Storage.Commits()) != commits+1 {
			t.Fatalf("commits = %d, want %d", len(ts.Storage.Commits()), commits+1)
		}
		if curVal, _ := state.Value(); curVal != baseline {
			t.Fatalf("expected equal version-base update skipped on state")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(current)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(4))
			return nil
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := state.Value()
			return reflect.DeepEqual(v, jsonx.ObjectOf("value", float64(4), "migrated", true)), nil
		}); err != nil {
			t.Fatalf("Eventually v=4 failed: %v", err)
		}
	})

	t.Run("continues from exact committed values after the tracker cache unloads", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		state, err := ts.Session.DocumentState(ctx, tsstStateDoc)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()
		baseline, _ := state.Value()
		reads := ts.Storage.DocumentReadCount()
		_ = ts.Session.UnloadDocuments()
		reloaded, _ := ts.Session.Snapshot(ctx, tsstStateDoc)
		if !reflect.DeepEqual(reloaded, baseline) || reloaded == baseline {
			t.Fatalf("expected fresh equal reloaded snapshot")
		}
		if ts.Storage.DocumentReadCount() <= reads {
			t.Fatalf("expected storage read after unload")
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(tsstStateDoc)
			if err != nil {
				return err
			}
			d.Value.Set("value", float64(6))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := state.Value()
			return tsstValNum(v) == 6, nil
		}); err != nil {
			t.Fatalf("Eventually 6 failed: %v", err)
		}
	})

	t.Run("exposes trusted shared immutable values without mutation", func(t *testing.T) {
		ts := tsstCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		snapshot, _ := ts.Session.Snapshot(ctx, tsstStateDoc)
		state, _ := ts.Session.DocumentState(ctx, tsstStateDoc)
		defer state.Dispose()
		if v, ok := state.Value(); !ok || v != snapshot {
			t.Fatalf("expected state.Value() == snapshot pointer")
		}
	})
}
