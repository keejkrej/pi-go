// Ported from packages/chord/test/state-value.test.ts (pi v1.0.0).

package chord

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsvObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tsvMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestImmutableRevisionOwnership(t *testing.T) {
	t.Run("takes ownership of an alias-free mutable JSON root", func(t *testing.T) {
		left := tsvObj("value", float64(1))
		right := tsvObj("value", float64(1))
		input := tsvObj("left", left, "right", right)
		tracker := Track(input)
		if tracker.Value() != input {
			t.Fatalf("expected tracker.Value() == input")
		}
		gotLeft, _ := tracker.Value().Get("left")
		gotRight, _ := tracker.Value().Get("right")
		if gotLeft == gotRight {
			t.Fatalf("expected distinct left and right")
		}
	})

	t.Run("commits transaction copies while sharing unchanged branches", func(t *testing.T) {
		tracker := Track(tsvObj(
			"changed", tsvObj("value", float64(1)),
			"retained", tsvObj("value", float64(2)),
		))
		base := tracker.Value()
		baseSnapshot := tsvMustJson(base)
		baseRetained, _ := base.Get("retained")
		baseChanged, _ := base.Get("changed")

		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State failed: %v", err)
		}
		draftChanged, _ := draft.Value.Get("changed")
		draftChanged.(*jsonx.Object).Set("value", float64(3))

		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		prepChanged, _ := prepared.Value().Get("changed")
		prepRetained, _ := prepared.Value().Get("retained")
		if prepChanged == baseChanged {
			t.Fatalf("expected changed branch to have new pointer")
		}
		if prepRetained != baseRetained {
			t.Fatalf("expected retained branch pointer preserved")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if tsvMustJson(base) != baseSnapshot {
			t.Fatalf("base mutated")
		}
		if tracker.Value() != prepared.Value() {
			t.Fatalf("expected tracker.Value() == prepared.Value()")
		}
	})

	t.Run("makes repeated placements independent", func(t *testing.T) {
		tracker := Track(tsvObj("values", []any{}))
		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State failed: %v", err)
		}
		shared := tsvObj("value", float64(1))
		draft.Value.Set("values", []any{shared, shared})
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		valsAny, _ := prepared.Value().Get("values")
		vals := valsAny.([]any)
		if vals[0] == vals[1] {
			t.Fatalf("expected independent pointers for repeated placements")
		}
	})
}
