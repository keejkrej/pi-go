// Ported from packages/chord/test/state-draft.test.ts (pi v1.0.0).

package chord

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsdrObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tsdrMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestTransactionalOverlayDrafts(t *testing.T) {
	t.Run("copies only changed branches", func(t *testing.T) {
		tracker := Track(tsdrObj(
			"changed", tsdrObj("count", float64(1), "sibling", tsdrObj("value", "kept")),
			"untouched", tsdrObj("value", float64(2)),
		))
		base := tracker.Value()
		baseChanged, _ := base.Get("changed")
		baseSibling, _ := baseChanged.(*jsonx.Object).Get("sibling")
		baseUntouched, _ := base.Get("untouched")

		change := tracker.BeginChange()
		d1, _ := change.State()
		d2, _ := change.State()
		if d1.Value != d2.Value {
			t.Fatalf("expected same draft root across repeated State() calls")
		}
		changed, _ := d1.Value.Get("changed")
		changed.(*jsonx.Object).Set("count", float64(3))

		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		if prepared.Value() == base {
			t.Fatalf("expected new root pointer")
		}
		gotChanged, _ := prepared.Value().Get("changed")
		if gotChanged == baseChanged {
			t.Fatalf("expected new changed pointer")
		}
		gotSibling, _ := gotChanged.(*jsonx.Object).Get("sibling")
		if gotSibling != baseSibling {
			t.Fatalf("expected sibling branch pointer preserved")
		}
		gotUntouched, _ := prepared.Value().Get("untouched")
		if gotUntouched != baseUntouched {
			t.Fatalf("expected untouched branch pointer preserved")
		}
	})

	t.Run("supports deletion and array mutations", func(t *testing.T) {
		tracker := Track(tsdrObj("optional", "remove", "values", []any{float64(3), float64(1), float64(2)}))
		change := tracker.BeginChange()
		draft, _ := change.State()
		draft.Value.Delete("optional")
		valsAny, _ := draft.Value.Get("values")
		vals := valsAny.([]any)
		// push 4, pop, unshift 0, shift, splice(1,1,5,4), sort, reverse, fill, copyWithin -> [5,5,9,2]
		vals = append(vals, float64(4))
		vals = vals[:len(vals)-1]
		vals = append([]any{float64(0)}, vals...)
		vals = vals[1:]
		vals = slices.Replace(vals, 1, 2, any(float64(5)), any(float64(4))) // [3, 5, 4, 2]
		slices.SortFunc(vals, func(a, b any) int {
			fa, fb := a.(float64), b.(float64)
			if fa < fb {
				return -1
			}
			if fa > fb {
				return 1
			}
			return 0
		})
		slices.Reverse(vals) // [5, 4, 3, 2]
		vals[1] = float64(9)
		vals[2] = float64(9)
		copy(vals[1:3], vals[0:2]) // [5, 5, 9, 2]
		draft.Value.Set("values", vals)

		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		if got := tsdrMustJson(prepared.Value()); got != `{"values":[5,5,9,2]}` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("inserts 100,000 items cleanly without overflow", func(t *testing.T) {
		tracker := Track(tsdrObj("values", []any{float64(-1)}))
		change := tracker.BeginChange()
		draft, _ := change.State()
		items := make([]any, 100000)
		for i := range items {
			items[i] = float64(i)
		}
		next := make([]any, 0, len(items)+1)
		next = append(next, items...)
		next = append(next, float64(-1))
		draft.Value.Set("values", next)

		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		gotVals, _ := prepared.Value().Get("values")
		s := gotVals.([]any)
		if len(s) != 100001 || s[0] != float64(0) || s[99999] != float64(99999) {
			t.Fatalf("unexpected slice state len=%d", len(s))
		}
		replayed, err := ApplyImmutable(prepared.Base(), prepared.Ops())
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if tsdrMustJson(replayed) != tsdrMustJson(prepared.Value()) {
			t.Fatalf("replay mismatch")
		}
	})

	t.Run("rejects non-strict JSON placements without mutating base", func(t *testing.T) {
		tracker := Track(tsdrObj("number", float64(0), "payload", nil, "values", []any{float64(1), float64(2)}))
		change := tracker.BeginChange()
		draft, _ := change.State()
		draft.Value.Set("values", []any{tsdrObj("nested", []byte{1})})
		if _, err := change.Prepare(); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error, got %v", err)
		}
		if got := tsdrMustJson(tracker.Value()); got != `{"number":0,"payload":null,"values":[1,2]}` {
			t.Fatalf("base mutated: %s", got)
		}
	})
}
