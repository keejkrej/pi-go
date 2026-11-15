// Ported from packages/chord/test/delta-tracker/tracker.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tdttObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tdttMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestDeltaTrackerExtended(t *testing.T) {
	t.Run("allows competing contexts and invalidates loser views", func(t *testing.T) {
		tracker := Track(tdttObj("value", float64(0)))
		first := tracker.BeginChange()
		second := tracker.BeginChange()
		d1, _ := first.State()
		d2, _ := second.State()
		d1.Value.Set("value", float64(1))
		d2.Value.Set("value", float64(2))

		firstPrepared, err := first.Prepare()
		if err != nil {
			t.Fatalf("first Prepare failed: %v", err)
		}
		secondPrepared, err := second.Prepare()
		if err != nil {
			t.Fatalf("second Prepare failed: %v", err)
		}
		held := secondPrepared.Value()

		if err := tracker.Adopt(firstPrepared); err != nil {
			t.Fatalf("Adopt first failed: %v", err)
		}
		if v, _ := tracker.Value().Get("value"); v != float64(1) {
			t.Fatalf("got %v, want 1", v)
		}
		if v, _ := held.Get("value"); v != float64(2) {
			t.Fatalf("loser candidate should remain readable, got %v", v)
		}
		if err := tracker.Adopt(secondPrepared); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected stale error, got %v", err)
		}
		if err := tracker.Adopt(firstPrepared); err == nil || !strings.Contains(err.Error(), "already been used") {
			t.Fatalf("expected already been used error, got %v", err)
		}
	})

	t.Run("invalidates competing open overlays without changing their base revision", func(t *testing.T) {
		initial := tdttObj("rows", []any{
			tdttObj("value", float64(0)),
			tdttObj("value", float64(1)),
		})
		tracker := Track(initial)
		staleChange := tracker.BeginChange()
		staleDraft, _ := staleChange.State()
		staleRows, _ := staleDraft.Value.Get("rows")
		staleRows.([]any)[1].(*jsonx.Object).Set("value", float64(2))

		winner := tracker.BeginChange()
		winnerDraft, _ := winner.State()
		winnerRows, _ := winnerDraft.Value.Get("rows")
		winnerRows.([]any)[0].(*jsonx.Object).Set("value", float64(3))
		prepared, err := winner.Prepare()
		if err != nil {
			t.Fatalf("winner Prepare failed: %v", err)
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}

		if got := tdttMustJson(initial); got != `{"rows":[{"value":0},{"value":1}]}` {
			t.Fatalf("initial mutated: %s", got)
		}
		if got := tdttMustJson(tracker.Value()); got != `{"rows":[{"value":3},{"value":1}]}` {
			t.Fatalf("tracker value mismatch: %s", got)
		}
		if _, err := staleChange.State(); err == nil || !strings.Contains(err.Error(), "settled") {
			t.Fatalf("expected settled State error, got %v", err)
		}
		if _, err := staleChange.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
			t.Fatalf("expected settled Prepare error, got %v", err)
		}
		staleChange.Abort()
	})

	t.Run("adopts object edits cleanly while leaving initial untouched", func(t *testing.T) {
		initial := tdttObj("first", float64(1), "second", float64(2))
		tracker := Track(initial)
		change := tracker.BeginChange()
		draft, _ := change.State()
		draft.Value.Set("first", float64(3))
		draft.Value.Delete("second")
		draft.Value.Set("third", float64(4))
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if tracker.Value() == initial {
			t.Fatalf("expected new revision pointer")
		}
		if got := tdttMustJson(initial); got != `{"first":1,"second":2}` {
			t.Fatalf("initial mutated: %s", got)
		}
		if got := tdttMustJson(tracker.Value()); got != `{"first":3,"third":4}` {
			t.Fatalf("tracker mismatch: %s", got)
		}
	})

	t.Run("supports adopt-then-publish ordering and retained publication reads", func(t *testing.T) {
		tracker := Track(tdttObj("value", float64(0), "nested", tdttObj("count", float64(0))))
		change := tracker.BeginChange()
		d1, _ := change.State()
		d1.Value.Set("value", float64(1))
		n1, _ := d1.Value.Get("nested")
		n1.(*jsonx.Object).Set("count", float64(1))
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		ops := prepared.Ops()
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if len(ops) != 2 {
			t.Fatalf("expected 2 ops, got %d", len(ops))
		}
		published := prepared.Value()
		if published != tracker.Value() {
			t.Fatalf("expected published == tracker.Value()")
		}

		next := tracker.BeginChange()
		d2, _ := next.State()
		d2.Value.Set("value", float64(2))
		nextPrepared, _ := next.Prepare()
		if err := tracker.Adopt(nextPrepared); err != nil {
			t.Fatalf("Adopt next failed: %v", err)
		}
		if published == tracker.Value() {
			t.Fatalf("expected distinct revision pointer")
		}
		if got := tdttMustJson(prepared.Value()); got != `{"value":1,"nested":{"count":1}}` {
			t.Fatalf("retained publication mutated: %s", got)
		}
		if got := tdttMustJson(tracker.Value()); got != `{"value":2,"nested":{"count":1}}` {
			t.Fatalf("tracker mismatch: %s", got)
		}
	})

	t.Run("keeps aborted and stale materialized candidates readable", func(t *testing.T) {
		tracker := Track(tdttObj("value", float64(0)))
		abortedChange := tracker.BeginChange()
		d1, _ := abortedChange.State()
		d1.Value.Set("value", float64(1))
		aborted, _ := abortedChange.Prepare()
		abortedView := aborted.Value()
		aborted.Abort()
		if v, _ := abortedView.Get("value"); v != float64(1) {
			t.Fatalf("aborted view mutated: %v", v)
		}

		loserChange := tracker.BeginChange()
		d2, _ := loserChange.State()
		d2.Value.Set("value", float64(2))
		loser, _ := loserChange.Prepare()
		loserView := loser.Value()
		winner, _ := tracker.PrepareReplace(tdttObj("value", float64(3)))
		_ = tracker.Adopt(winner)
		if v, _ := loserView.Get("value"); v != float64(2) {
			t.Fatalf("loser view mutated: %v", v)
		}
	})

	t.Run("lets a settled Change abort its prepared result", func(t *testing.T) {
		tracker := Track(tdttObj("value", float64(0)))
		change := tracker.BeginChange()
		d, _ := change.State()
		d.Value.Set("value", float64(1))
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		change.Abort()
		if err := tracker.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("expected aborted error, got %v", err)
		}
	})

	t.Run("rejects foreign and aborted PrepareReplace results", func(t *testing.T) {
		first := Track(tdttObj("value", float64(0)))
		second := Track(tdttObj("value", float64(0)))
		prepared, _ := first.PrepareReplace(tdttObj("value", float64(1)))
		if err := second.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "different tracker") {
			t.Fatalf("expected different tracker error, got %v", err)
		}
		prepared.Abort()
		prepared.Abort() // idempotent
		if err := first.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("expected aborted error, got %v", err)
		}
	})

	t.Run("preserves JS object key order across delete, re-add, and integer-like keys", func(t *testing.T) {
		tracker := Track(tdttObj("object", tdttObj("1", "one", "2", "two", "first", "a", "second", "b")))
		change := tracker.BeginChange()
		d, _ := change.State()
		objAny, _ := d.Value.Get("object")
		obj := objAny.(*jsonx.Object)
		obj.Delete("2")
		obj.Set("2", "two")
		obj.Delete("first")
		obj.Set("first", "a")
		obj.Set("3", "three")
		obj.Set("0", "zero")
		draftKeys := obj.Keys()
		wantDraftKeys := []string{"0", "1", "2", "3", "second", "first"}
		if strings.Join(draftKeys, ",") != strings.Join(wantDraftKeys, ",") {
			t.Fatalf("draft key order mismatch: got %v, want %v", draftKeys, wantDraftKeys)
		}
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		_ = tracker.Adopt(prepared)
		gotObj, _ := tracker.Value().Get("object")
		keys := gotObj.(*jsonx.Object).Keys()
		wantKeys := []string{"0", "1", "2", "3", "first", "second"}
		if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
			t.Fatalf("key order mismatch: got %v, want %v", keys, wantKeys)
		}
	})

	t.Run("enumerates wide objects without duplicate keys", func(t *testing.T) {
		tracker := Track(tdttObj("values", tdttObj()))
		change := tracker.BeginChange()
		d, _ := change.State()
		valuesAny, _ := d.Value.Get("values")
		values := valuesAny.(*jsonx.Object)
		for i := 0; i < 20000; i++ {
			values.Set(fmt.Sprintf("field%d", i), float64(i))
		}
		keys := values.Keys()
		if len(keys) != 20000 || keys[0] != "field0" || keys[len(keys)-1] != "field19999" {
			t.Fatalf("wide object keys unexpected: len=%d", len(keys))
		}
		change.Abort()
	})

	t.Run("rejects non-strict JSON placements in draft without changing tracker", func(t *testing.T) {
		tracker := Track(tdttObj("number", float64(0), "payload", nil))
		for name, badVal := range map[string]any{
			"NaN":       math.NaN(),
			"Inf":       math.Inf(1),
			"sliceInt":  []any{42},
			"struct":    struct{}{},
			"bytes":     []byte{1, 2},
			"nilSlice":  []any(nil),
			"nilObject": (*jsonx.Object)(nil),
		} {
			t.Run(name, func(t *testing.T) {
				change := tracker.BeginChange()
				d, _ := change.State()
				if name == "NaN" || name == "Inf" {
					d.Value.Set("number", badVal)
				} else {
					d.Value.Set("payload", badVal)
				}
				if _, err := change.Prepare(); err == nil || !strings.Contains(err.Error(), "strict JSON") {
					t.Fatalf("expected strict JSON error for %s, got %v", name, err)
				}
			})
		}

		// Cycle inside draft rejected
		cycChange := tracker.BeginChange()
		d, _ := cycChange.State()
		cyc := jsonx.NewObject()
		cyc.Set("self", cyc)
		d.Value.Set("payload", cyc)
		if _, err := cycChange.Prepare(); err == nil || !strings.Contains(err.Error(), "cycles") {
			t.Fatalf("expected cycle error, got %v", err)
		}
		if got := tdttMustJson(tracker.Value()); got != `{"number":0,"payload":null}` {
			t.Fatalf("tracker mutated: %s", got)
		}
	})

	t.Run("expands repeated source aliases into independent placements", func(t *testing.T) {
		tracker := Track(tdttObj("left", nil, "right", nil, "rows", []any{}))
		shared := tdttObj("nested", tdttObj("value", float64(1)))
		change := tracker.BeginChange()
		d, _ := change.State()
		d.Value.Set("left", shared)
		d.Value.Set("right", shared)
		d.Value.Set("rows", []any{shared, shared})
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		left, _ := prepared.Value().Get("left")
		right, _ := prepared.Value().Get("right")
		if left == right {
			t.Fatalf("expected left and right not to alias")
		}
		rowsAny, _ := prepared.Value().Get("rows")
		rows := rowsAny.([]any)
		if rows[0] == rows[1] || rows[0] == left || rows[0] == right {
			t.Fatalf("expected all row/property placements independent")
		}
	})
}

func TestDeltaTrackerConcurrency(t *testing.T) {
	tracker := Track(tdttObj("count", float64(0)))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = tracker.Value()
				_ = tracker.Revision()
				c := tracker.BeginChange()
				d, err := c.State()
				if err == nil {
					d.Value.Set("count", float64(id*100+i))
					p, err := c.Prepare()
					if err == nil {
						_ = tracker.Adopt(p)
					}
				}
				if i%7 == 0 {
					p, _ := tracker.PrepareReplace(tdttObj("count", float64(id*100+i)))
					_ = tracker.Adopt(p)
				}
				if i%11 == 0 {
					c2 := tracker.BeginChange()
					c2.Abort()
				}
			}
		}(g)
	}
	wg.Wait()
	if tracker.Revision() == 0 {
		t.Fatalf("expected at least one adopted revision")
	}
}
