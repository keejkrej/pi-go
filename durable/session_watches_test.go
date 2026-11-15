// Ported from packages/durable/test/session-watches.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tswtMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%s) failed: %v", def.Kind, err)
	}
	return tok
}

var tswtStateDoc = func() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "watch.state",
		Version: 1,
		Scope:   durable.DocumentScopeSession,
		Initial: func() durable.JsonObject {
			return jsonx.ObjectOf("value", float64(0), "items", []any{"a", "b"}, "retained", jsonx.ObjectOf("label", "stable"))
		},
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

func tswtCreateState(t *testing.T) *testkit.TestSession {
	t.Helper()
	ts := testkit.OpenTestSession()
	if err := ts.Session.Commit(testkit.TestContext, func(tx durable.Tx) error {
		_, err := tx.Doc(tswtStateDoc)
		return err
	}); err != nil {
		t.Fatalf("createState commit failed: %v", err)
	}
	return ts
}

func tswtValNum(v durable.JsonObject) float64 {
	if v == nil {
		return -1
	}
	n, _ := v.Get("value")
	f, _ := n.(float64)
	return f
}

func TestSessionDocumentWatches(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("never creates an absent document", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		commits := len(ts.Storage.Commits())
		if w, err := ts.Session.WatchDoc(ctx, tswtStateDoc); err != nil || w != nil {
			t.Fatalf("expected nil WatchDoc, got (%v, %v)", w, err)
		}
		if len(ts.Storage.Commits()) != commits || ts.Storage.MintCount() != 0 {
			t.Fatalf("unexpected commit or mint")
		}
	})

	t.Run("keeps the acquisition revision until start and delivers exact committed frames", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, err := ts.Session.WatchDoc(ctx, tswtStateDoc)
		if err != nil || watch == nil {
			t.Fatalf("WatchDoc failed: %v %v", watch, err)
		}
		initial := watch.Value()

		var firstPublished, secondPublished durable.DocumentCommitChangeUpdate
		for _, v := range []float64{1, 2} {
			val := v
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, err := tx.Doc(tswtStateDoc)
				if err != nil {
					return err
				}
				d.Value.Set("value", val)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", val, err)
			}
			testkit.Flush()
			pubs := ts.Publications()
			ch := testkit.DocumentChanges(pubs[len(pubs)-1])[0]
			if val == 1 {
				firstPublished = ch
			} else {
				secondPublished = ch
			}
		}
		if watch.Value() != initial {
			t.Fatalf("watch.Value() changed before Start")
		}

		type delivery struct {
			value durable.JsonObject
			ops   []chord.Op
		}
		var inline atomic.Bool
		inline.Store(true)
		var mu sync.Mutex
		var deliveries []delivery
		if err := watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			if inline.Load() {
				t.Errorf("Start invoked listener inline")
			}
			if watch.Value() != value {
				t.Errorf("watch.Value() != delivered value")
			}
			mu.Lock()
			deliveries = append(deliveries, delivery{value: value, ops: ops})
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		inline.Store(false)

		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(deliveries) == 2, nil
		}); err != nil {
			t.Fatalf("Eventually 2 deliveries failed: %v", err)
		}
		mu.Lock()
		if len(deliveries) != 2 ||
			tswtValNum(deliveries[0].value) != 1 || tswtValNum(deliveries[1].value) != 2 ||
			deliveries[0].value != firstPublished.Value || &deliveries[0].ops[0] != &firstPublished.Ops[0] ||
			deliveries[1].value != secondPublished.Value || &deliveries[1].ops[0] != &secondPublished.Ops[0] {
			t.Fatalf("unexpected deliveries: %#v", deliveries)
		}
		mu.Unlock()
		if tswtValNum(initial) != 0 {
			t.Fatalf("initial value mutated: %v", initial)
		}
		watch.Stop()
	})

	t.Run("serializes callbacks and buffers exact frames committed while one is in flight", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()

		var mu sync.Mutex
		var values []float64
		var active, maxActive atomic.Int32
		_ = watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			cur := active.Add(1)
			for {
				prev := maxActive.Load()
				if cur <= prev || maxActive.CompareAndSwap(prev, cur) {
					break
				}
			}
			mu.Lock()
			values = append(values, tswtValNum(value))
			first := len(values) == 1
			mu.Unlock()
			if first {
				entered.Resolve(struct{}{})
				_, _ = release.Wait()
			}
			active.Add(-1)
			return nil
		})

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit 1 failed: %v", err)
		}
		_, _ = entered.Wait()

		for val := 2; val <= 20; val++ {
			v := float64(val)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, _ := tx.Doc(tswtStateDoc)
				d.Value.Set("value", v)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", v, err)
			}
		}
		mu.Lock()
		if !reflect.DeepEqual(values, []float64{1}) {
			mu.Unlock()
			t.Fatalf("values while blocked = %v, want [1]", values)
		}
		mu.Unlock()

		release.Resolve(struct{}{})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(values) == 20, nil
		}); err != nil {
			t.Fatalf("Eventually 20 values failed: %v", err)
		}
		if maxActive.Load() != 1 {
			t.Fatalf("maxActive = %d, want 1", maxActive.Load())
		}
		want := make([]float64, 20)
		for i := range want {
			want[i] = float64(i + 1)
		}
		mu.Lock()
		if !reflect.DeepEqual(values, want) {
			mu.Unlock()
			t.Fatalf("values = %v, want %v", values, want)
		}
		mu.Unlock()
		watch.Stop()
	})

	t.Run("allows a listener to initiate a later Session commit", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		completed := testkit.NewDeferred[struct{}]()

		var mu sync.Mutex
		var values []float64
		_ = watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			cur := tswtValNum(value)
			mu.Lock()
			values = append(values, cur)
			mu.Unlock()
			if cur == 1 {
				return ts.Session.Commit(ctx, func(tx durable.Tx) error {
					d, err := tx.Doc(tswtStateDoc)
					if err != nil {
						return err
					}
					d.Value.Set("value", float64(2))
					return nil
				})
			}
			completed.Resolve(struct{}{})
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit 1 failed: %v", err)
		}
		_, _ = completed.Wait()
		mu.Lock()
		if !reflect.DeepEqual(values, []float64{1, 2}) {
			mu.Unlock()
			t.Fatalf("values = %v, want [1 2]", values)
		}
		mu.Unlock()
		watch.Stop()
	})

	t.Run("collapses 101 pending commits to one root replacement", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		for val := 1; val <= 101; val++ {
			v := float64(val)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, _ := tx.Doc(tswtStateDoc)
				d.Value.Set("value", v)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", v, err)
			}
		}
		type delivery struct {
			value float64
			ops   []chord.Op
		}
		var mu sync.Mutex
		var deliveries []delivery
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			mu.Lock()
			deliveries = append(deliveries, delivery{value: tswtValNum(value), ops: ops})
			mu.Unlock()
			return nil
		})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(deliveries) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually collapsed frame failed: %v", err)
		}
		testkit.Flush()
		mu.Lock()
		if len(deliveries) != 1 || deliveries[0].value != 101 || len(deliveries[0].ops) != 1 {
			mu.Unlock()
			t.Fatalf("deliveries = %#v", deliveries)
		}
		if rep, ok := deliveries[0].ops[0].(chord.OpReplace); !ok || !reflect.DeepEqual(rep.Value, watch.Value()) {
			mu.Unlock()
			t.Fatalf("expected OpReplace(watch.Value()), got %#v vs %v", deliveries[0].ops[0], watch.Value())
		}
		mu.Unlock()
		watch.Stop()
	})

	t.Run("never folds the in-flight frame into an overflow reset", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()

		type delivery struct {
			value float64
			ops   []chord.Op
		}
		var mu sync.Mutex
		var deliveries []delivery
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			mu.Lock()
			deliveries = append(deliveries, delivery{value: tswtValNum(value), ops: ops})
			first := len(deliveries) == 1
			mu.Unlock()
			if first {
				entered.Resolve(struct{}{})
				_, _ = release.Wait()
			}
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit 1 failed: %v", err)
		}
		_, _ = entered.Wait()

		for val := 2; val <= 102; val++ {
			v := float64(val)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, _ := tx.Doc(tswtStateDoc)
				d.Value.Set("value", v)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", v, err)
			}
		}
		release.Resolve(struct{}{})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(deliveries) == 2, nil
		}); err != nil {
			t.Fatalf("Eventually 2 frames failed: %v", err)
		}
		testkit.Flush()
		mu.Lock()
		if len(deliveries) != 2 || deliveries[0].value != 1 || deliveries[1].value != 102 {
			mu.Unlock()
			t.Fatalf("deliveries = %#v", deliveries)
		}
		if rep, ok := deliveries[1].ops[0].(chord.OpReplace); !ok || !reflect.DeepEqual(rep.Value, watch.Value()) {
			mu.Unlock()
			t.Fatalf("expected OpReplace(watch.Value()) on frame 2, got %#v", deliveries[1].ops)
		}
		mu.Unlock()
		watch.Stop()
	})

	t.Run("folds retirement into an overflow reset and then closes", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		for val := 1; val <= 100; val++ {
			v := float64(val)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, _ := tx.Doc(tswtStateDoc)
				d.Value.Set("value", v)
				return nil
			}); err != nil {
				t.Fatalf("commit %v failed: %v", v, err)
			}
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(tswtStateDoc)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		type delivery struct {
			value durable.JsonObject
			ops   []chord.Op
		}
		var mu sync.Mutex
		var deliveries []delivery
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			mu.Lock()
			deliveries = append(deliveries, delivery{value: value, ops: ops})
			mu.Unlock()
			return nil
		})
		end := watch.Closed()
		if end.Reason != durable.WatchEndRetired {
			t.Fatalf("end = %#v, want retired", end)
		}
		mu.Lock()
		if len(deliveries) != 1 || deliveries[0].value != nil || !reflect.DeepEqual(deliveries[0].ops, []chord.Op{chord.OpReplace{Value: nil}}) {
			mu.Unlock()
			t.Fatalf("deliveries = %#v", deliveries)
		}
		mu.Unlock()
	})

	t.Run("delivers structural slice edit ops to the watch listener", func(t *testing.T) {
		// Go chord.Draft uses value diffing (DiffRevisions), so identical before/after commits produce
		// zero ops and are suppressed, while array reorders produce non-empty permutation/splice ops.
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		initial := watch.Value()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			raw, _ := d.Value.Get("items")
			s := slices.Clone(raw.([]any))
			s[0], s[1] = s[1], s[0]
			d.Value.Set("items", s)
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		newest, _ := ts.Session.Snapshot(ctx, tswtStateDoc)
		if newest == initial {
			t.Fatalf("expected distinct root pointer")
		}
		var mu sync.Mutex
		var batches [][]chord.Op
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			if value != newest {
				t.Errorf("delivered value pointer != newest")
			}
			mu.Lock()
			batches = append(batches, ops)
			mu.Unlock()
			return nil
		})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(batches) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually failed: %v", err)
		}
		mu.Lock()
		if len(batches[0]) == 0 {
			t.Fatalf("expected non-empty ops")
		}
		mu.Unlock()
		watch.Stop()
	})

	t.Run("preserves commit Context values without inheriting producer cancellation", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		key := chord.CreateContextKey[string]("watch-test")
		parentStdCtx, parentCancel := context.WithCancel(context.Background())
		commitContext := chord.WithContextValue(key, "newest-commit", chord.WithAbortSignal(parentStdCtx, chord.BackgroundContext))

		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()
		inFlightDone := testkit.NewDeferred[struct{}]()
		var deliveryCtx chord.Context
		_ = watch.Start(func(_ durable.JsonObject, _ []chord.Op, delivered chord.Context) error {
			deliveryCtx = delivered
			entered.Resolve(struct{}{})
			_, _ = release.Wait()
			inFlightDone.Resolve(struct{}{})
			return nil
		})
		if err := ts.Session.Commit(commitContext, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		_, _ = entered.Wait()
		if got, ok := deliveryCtx.Value(key); !ok || got != "newest-commit" {
			t.Fatalf("deliveryCtx.Value(key) = (%v, %v), want newest-commit", got, ok)
		}
		if deliveryCtx.AbortSignal() != nil {
			t.Fatalf("expected nil AbortSignal in deliveryCtx")
		}
		parentCancel()
		end := watch.Stop()
		if end.Reason != durable.WatchEndStopped {
			t.Fatalf("end = %#v, want stopped", end)
		}
		if deliveryCtx.AbortSignal() != nil {
			t.Fatalf("expected deliveryCtx.AbortSignal() to remain nil")
		}
		release.Resolve(struct{}{})
		_, _ = inFlightDone.Wait()
	})

	t.Run("keeps earlier immutable revisions stable", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		initial := watch.Value()
		var mu sync.Mutex
		var delivered durable.JsonObject
		_ = watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			mu.Lock()
			delivered = value
			mu.Unlock()
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return delivered != nil, nil
		}); err != nil {
			t.Fatalf("Eventually failed: %v", err)
		}
		mu.Lock()
		if delivered != watch.Value() || delivered == initial {
			mu.Unlock()
			t.Fatalf("delivered = %v, watch.Value() = %v, initial = %v", delivered, watch.Value(), initial)
		}
		r1, _ := delivered.Get("retained")
		r2, _ := initial.Get("retained")
		if r1 != r2 {
			mu.Unlock()
			t.Fatalf("expected retained pointer sharing")
		}
		mu.Unlock()
		if tswtValNum(initial) != 0 {
			t.Fatalf("initial mutated")
		}
		watch.Stop()
	})

	t.Run("delivers retirement and does not follow recreation", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		oldWatch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		var mu sync.Mutex
		var values []durable.JsonObject
		_ = oldWatch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			mu.Lock()
			values = append(values, value)
			mu.Unlock()
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if err := tx.RetireDoc(tswtStateDoc); err != nil {
				return err
			}
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(10))
			return nil
		}); err != nil {
			t.Fatalf("retire+recreate failed: %v", err)
		}
		if end := oldWatch.Closed(); end.Reason != durable.WatchEndRetired {
			t.Fatalf("end = %#v, want retired", end)
		}
		mu.Lock()
		if len(values) != 1 || values[0] != nil {
			mu.Unlock()
			t.Fatalf("values = %#v, want [nil]", values)
		}
		mu.Unlock()
		if oldWatch.Value() != nil {
			t.Fatalf("oldWatch.Value() = %v, want nil", oldWatch.Value())
		}

		replacement, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		if tswtValNum(replacement.Value()) != 10 {
			t.Fatalf("replacement = %v, want 10", replacement.Value())
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(11))
			return nil
		}); err != nil {
			t.Fatalf("commit 11 failed: %v", err)
		}
		testkit.Flush()
		if oldWatch.Value() != nil {
			t.Fatalf("oldWatch mutated")
		}
		replacement.Stop()
	})

	t.Run("Session close discards retirement buffered before start", func(t *testing.T) {
		ts := tswtCreateState(t)
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		baseline := watch.Value()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(tswtStateDoc)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		_ = ts.Session.Close(ctx)
		if end := watch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("end = %#v, want session_closed", end)
		}
		if watch.Value() != baseline {
			t.Fatalf("watch.Value() changed after Session.Close before Start")
		}
		if err := watch.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("expected stopped error, got %v", err)
		}
	})

	t.Run("Session close discards retirement behind an in-flight callback", func(t *testing.T) {
		ts := tswtCreateState(t)
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()
		inFlightDone := testkit.NewDeferred[struct{}]()
		var mu sync.Mutex
		var values []float64
		_ = watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			mu.Lock()
			values = append(values, tswtValNum(value))
			mu.Unlock()
			entered.Resolve(struct{}{})
			_, _ = release.Wait()
			inFlightDone.Resolve(struct{}{})
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit 1 failed: %v", err)
		}
		_, _ = entered.Wait()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(tswtStateDoc)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		_ = ts.Session.Close(ctx)
		if end := watch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("end = %#v, want session_closed", end)
		}
		release.Resolve(struct{}{})
		_, _ = inFlightDone.Wait()
		testkit.Flush()
		mu.Lock()
		if !reflect.DeepEqual(values, []float64{1}) {
			mu.Unlock()
			t.Fatalf("values = %v, want [1]", values)
		}
		mu.Unlock()
	})

	t.Run("supports idempotent stop and rejects repeated or late start", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()

		started, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		if err := started.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error { return nil }); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if err := started.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "already started") {
			t.Fatalf("expected already started error, got %v", err)
		}
		first := started.Stop()
		second := started.Stop()
		if first.Reason != durable.WatchEndStopped || second != first {
			t.Fatalf("stop ends = (%#v, %#v)", first, second)
		}

		stopped, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		if end := stopped.Stop(); end.Reason != durable.WatchEndStopped {
			t.Fatalf("stopped.Stop() = %#v", end)
		}
		if err := stopped.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("expected stopped error, got %v", err)
		}
	})

	t.Run("cancels acquisition without leaking a registered watch", func(t *testing.T) {
		ts := tswtCreateState(t)
		_ = ts.Session.UnloadDocuments()
		gate := ts.Storage.HoldFindDocument()
		child := chord.WithCancel(ctx)
		acqCh := make(chan error, 1)
		go func() {
			_, err := ts.Session.WatchDoc(child.Context, tswtStateDoc)
			acqCh <- err
		}()
		<-gate.Entered
		child.Cancel(errors.New("cancel acquisition"))
		gate.Release()
		if err := <-acqCh; err == nil || !strings.Contains(err.Error(), "cancel acquisition") {
			t.Fatalf("expected cancel acquisition error, got %v", err)
		}
		_ = ts.Session.Close(ctx)
	})

	t.Run("cancels future delivery without aborting an in-flight callback", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		child := chord.WithCancel(ctx)
		watch, _ := ts.Session.WatchDoc(child.Context, tswtStateDoc)
		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()
		inFlightDone := testkit.NewDeferred[struct{}]()
		var deliveryCtx chord.Context
		_ = watch.Start(func(_ durable.JsonObject, _ []chord.Op, delivered chord.Context) error {
			deliveryCtx = delivered
			entered.Resolve(struct{}{})
			_, _ = release.Wait()
			inFlightDone.Resolve(struct{}{})
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		_, _ = entered.Wait()
		child.Cancel(nil)
		if end := watch.Closed(); end.Reason != durable.WatchEndCancelled {
			t.Fatalf("end = %#v, want cancelled", end)
		}
		if deliveryCtx.AbortSignal() != nil {
			t.Fatalf("expected nil AbortSignal on deliveryCtx")
		}
		release.Resolve(struct{}{})
		_, _ = inFlightDone.Wait()
	})

	t.Run("Session close stops future delivery without joining an in-flight callback", func(t *testing.T) {
		ts := tswtCreateState(t)
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()
		inFlightDone := testkit.NewDeferred[struct{}]()
		var deliveryCtx chord.Context
		_ = watch.Start(func(_ durable.JsonObject, _ []chord.Op, delivered chord.Context) error {
			deliveryCtx = delivered
			entered.Resolve(struct{}{})
			_, _ = release.Wait()
			inFlightDone.Resolve(struct{}{})
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		_, _ = entered.Wait()
		_ = ts.Session.Close(ctx)
		if deliveryCtx.AbortSignal() != nil {
			t.Fatalf("expected nil AbortSignal on deliveryCtx")
		}
		if end := watch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("end = %#v, want session_closed", end)
		}
		release.Resolve(struct{}{})
		_, _ = inFlightDone.Wait()
	})

	t.Run("settles listener failure on only the affected watch", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		failed, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		healthy, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		_ = failed.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			return errors.New("listener failed")
		})
		var healthyCalls atomic.Int32
		_ = healthy.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			healthyCalls.Add(1)
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		end := failed.Closed()
		if end.Reason != durable.WatchEndListenerError || end.Error == nil || end.Error.Error() != "listener failed" {
			t.Fatalf("end = %#v, want listener_error", end)
		}
		if err := testkit.Eventually(func() (bool, error) {
			return healthyCalls.Load() == 1, nil
		}); err != nil {
			t.Fatalf("Eventually healthy call failed: %v", err)
		}
		healthy.Stop()
	})

	t.Run("hydrates migration without writing and observes the later exact edit", func(t *testing.T) {
		old := tswtMustDoc(t, durable.DocDefinition{
			Kind:    "watch.migration",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", float64(3)) },
		})
		current := tswtMustDoc(t, durable.DocDefinition{
			Kind:    "watch.migration",
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
		watch, _ := ts.Session.WatchDoc(ctx, current)
		if !reflect.DeepEqual(watch.Value(), jsonx.ObjectOf("value", float64(3), "migrated", true)) {
			t.Fatalf("watch.Value() = %v", watch.Value())
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("unexpected commit during migration hydration")
		}
		var mu sync.Mutex
		var values []float64
		_ = watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			mu.Lock()
			values = append(values, tswtValNum(value))
			mu.Unlock()
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(current)
			return err
		}); err != nil {
			t.Fatalf("migration persist failed: %v", err)
		}
		testkit.Flush()
		mu.Lock()
		if len(values) != 0 {
			mu.Unlock()
			t.Fatalf("values = %v, want empty", values)
		}
		mu.Unlock()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(current)
			d.Value.Set("value", float64(4))
			return nil
		}); err != nil {
			t.Fatalf("update 4 failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(values) == 1 && values[0] == 4, nil
		}); err != nil {
			t.Fatalf("Eventually [4] failed: %v", err)
		}
		watch.Stop()
	})

	t.Run("can replay every delivered exact operation batch from the acquisition revision", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		replica := watch.Value()
		done := make(chan struct{}, 1)
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			next, err := chord.ApplyImmutable(replica, chord.OpList(ops))
			if err != nil {
				return err
			}
			replica = next
			if !reflect.DeepEqual(replica, value) {
				t.Errorf("replica %v != delivered %v", replica, value)
			}
			select {
			case done <- struct{}{}:
			default:
			}
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(3))
			d.Value.Set("items", []any{"b"})
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		<-done
		if !reflect.DeepEqual(replica, watch.Value()) {
			t.Fatalf("replica %v != watch.Value() %v", replica, watch.Value())
		}
		watch.Stop()
	})

	t.Run("continues after the tracker cache unloads", func(t *testing.T) {
		ts := tswtCreateState(t)
		defer func() { _ = ts.Session.Close(ctx) }()
		watch, _ := ts.Session.WatchDoc(ctx, tswtStateDoc)
		baseline := watch.Value()
		reads := ts.Storage.DocumentReadCount()
		_ = ts.Session.UnloadDocuments()
		reloaded, _ := ts.Session.Snapshot(ctx, tswtStateDoc)
		if !reflect.DeepEqual(reloaded, baseline) || reloaded == baseline {
			t.Fatalf("expected fresh equal reloaded snapshot")
		}
		if ts.Storage.DocumentReadCount() <= reads {
			t.Fatalf("expected storage read after unload")
		}
		_ = watch.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error { return nil })
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(tswtStateDoc)
			d.Value.Set("value", float64(7))
			return nil
		}); err != nil {
			t.Fatalf("commit 7 failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			return tswtValNum(watch.Value()) == 7, nil
		}); err != nil {
			t.Fatalf("Eventually 7 failed: %v", err)
		}
		watch.Stop()
	})
}
