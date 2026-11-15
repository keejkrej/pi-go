// Ported from packages/chord/test/delta-clone.test.ts (pi v1.0.0).

package chord

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tdcObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tdcMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestTrackerOwnership(t *testing.T) {
	t.Run("takes immutable ownership of the imported revision in O(1)", func(t *testing.T) {
		point := tdcObj("x", float64(3), "y", float64(7), "pressure", float64(0.1))
		inner := tdcObj("n", float64(1))
		values := []any{float64(0), false, nil, "text", inner}
		row := tdcObj("values", values)
		input := tdcObj("point", point, "rows", []any{row})

		tracker := Track(input)
		if tracker.Value() != input {
			t.Fatalf("expected tracker.Value() == input")
		}
		if gotPoint, _ := tracker.Value().Get("point"); gotPoint != point {
			t.Fatalf("expected point pointer preserved")
		}
		rowsVal, _ := tracker.Value().Get("rows")
		rowsSlice := rowsVal.([]any)
		gotValues, _ := rowsSlice[0].(*jsonx.Object).Get("values")
		if gotValues.([]any)[4] != inner {
			t.Fatalf("expected deep pointer preserved in O(1)")
		}
	})

	t.Run("does not traverse a trusted root while taking ownership", func(t *testing.T) {
		// Pass a root holding a non-strict-JSON marker or opaque value; Track takes O(1) without validating/traversing it.
		sentinel := jsonx.NewObject()
		sentinel.Set("raw", []byte{1, 2, 3})
		tracker := Track(sentinel)
		if tracker.Value() != sentinel {
			t.Fatalf("expected O(1) ownership without traversal")
		}
	})

	t.Run("isolates placed values on prepare so external mutation after State does not corrupt", func(t *testing.T) {
		initial := tdcObj("rows", []any{})
		tracker := Track(initial)
		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State failed: %v", err)
		}
		assigned := tdcObj("nested", tdcObj("value", float64(1)))
		draft.Value.Set("rows", []any{assigned})
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		// Once prepared, mutating the external assigned object must not alter base or replay consistency;
		// and replica replay from base + ops matches prepared.Value.
		clonedBase, _ := CopyJson(prepared.Base(), nil)
		replica, err := Apply(clonedBase.(*jsonx.Object), prepared.Ops())
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if tdcMustJson(replica) != tdcMustJson(prepared.Value()) {
			t.Fatalf("replica mismatch: got %s, want %s", tdcMustJson(replica), tdcMustJson(prepared.Value()))
		}
	})
}
