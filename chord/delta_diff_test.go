// Ported from packages/chord/test/delta-diff.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tddObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tddCloneJson(v JsonValue) JsonValue {
	cloned, err := CopyJson(v, nil)
	if err != nil {
		panic(err)
	}
	return cloned
}

func tddMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func tddJsonEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch va := a.(type) {
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case float64:
		vb, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(va) && math.IsNaN(vb) {
			return true
		}
		return va == vb && math.Signbit(va) == math.Signbit(vb)
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case []any:
		vb, ok := b.([]any)
		if !ok || (va == nil) != (vb == nil) || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !tddJsonEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	case *jsonx.Object:
		vb, ok := b.(*jsonx.Object)
		if !ok || (va == nil) != (vb == nil) || va.Len() != vb.Len() {
			return false
		}
		for k, v := range va.All() {
			other, exists := vb.Get(k)
			if !exists || !tddJsonEqual(v, other) {
				return false
			}
		}
		return true
	default:
		return tddMustJson(a) == tddMustJson(b)
	}
}

func tddOpsEqual(a, b OpList) bool {
	if len(a) != len(b) {
		return false
	}
	return tddMustJson(a) == tddMustJson(b)
}

func tddExpectDiff(t *testing.T, before JsonValue, after JsonValue, expected OpList) {
	t.Helper()
	ops := DiffRevisions(before, after)
	if !tddOpsEqual(ops, expected) {
		t.Fatalf("ops mismatch:\n got: %s\nwant: %s", tddMustJson(ops), tddMustJson(expected))
	}
	applied, err := ApplyImmutable(before, ops)
	if err != nil {
		t.Fatalf("ApplyImmutable failed: %v", err)
	}
	if !tddJsonEqual(applied, after) {
		t.Fatalf("applied mismatch:\n got: %s\nwant: %s", tddMustJson(applied), tddMustJson(after))
	}
}

func TestDeltaDiff(t *testing.T) {
	t.Run("emits sets and deletes", func(t *testing.T) {
		tddExpectDiff(
			t,
			tddObj("keep", float64(1), "change", float64(1), "remove", true),
			tddObj("keep", float64(1), "change", float64(2), "add", float64(3)),
			OpList{
				OpSet{Path: Path{SegString("change")}, Value: float64(2)},
				OpSet{Path: Path{SegString("add")}, Value: float64(3)},
				OpDelete{Path: Path{SegString("remove")}},
			},
		)
	})

	t.Run("emits string append and front truncation", func(t *testing.T) {
		tddExpectDiff(t, tddObj("text", "hello"), tddObj("text", "hello world"), OpList{
			OpAppend{Path: Path{SegString("text")}, Text: " world"},
		})
		tddExpectDiff(t, tddObj("text", "hello world"), tddObj("text", "world"), OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 6},
		})
		tddExpectDiff(t, tddObj("text", "abcdefgh"), tddObj("text", "defghxyz"), OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 3},
			OpAppend{Path: Path{SegString("text")}, Text: "xyz"},
		})
	})

	t.Run("represents array insertion, removal, and shift with splices", func(t *testing.T) {
		a := tddObj("id", "a")
		b := tddObj("id", "b")
		c := tddObj("id", "c")
		tddExpectDiff(t, tddObj("values", []any{a, b}), tddObj("values", []any{a, c, b}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 0, Items: []JsonValue{c}},
		})
		tddExpectDiff(t, tddObj("values", []any{a, b, c}), tddObj("values", []any{b, c}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
		})
	})

	t.Run("collapses a same-length queue update to two splices", func(t *testing.T) {
		a := tddObj("id", "a")
		b := tddObj("id", "b")
		c := tddObj("id", "c")
		d := tddObj("id", "d")
		tddExpectDiff(t, tddObj("values", []any{a, b, c}), tddObj("values", []any{b, c, d}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
			OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 0, Items: []JsonValue{d}},
		})
	})

	t.Run("emits a permutation for a pure reorder", func(t *testing.T) {
		a := tddObj("id", "a")
		b := tddObj("id", "b")
		c := tddObj("id", "c")
		tddExpectDiff(t, tddObj("values", []any{a, b, c}), tddObj("values", []any{c, a, b}), OpList{
			OpMove{Path: Path{SegString("values")}, Permutation: []int{2, 0, 1}},
		})
	})

	t.Run("normalizes reordered distinct deeply-equal objects to a no-op", func(t *testing.T) {
		first := tddObj("nested", tddObj("value", float64(1)))
		second := tddObj("nested", tddObj("value", float64(1)))
		if first == second {
			t.Fatalf("expected distinct object pointers")
		}
		ops := DiffRevisions(tddObj("values", []any{first, second}), tddObj("values", []any{second, first}))
		if len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tddMustJson(ops))
		}
	})

	t.Run("validates and encodes permutations", func(t *testing.T) {
		operations := OpList{
			OpMove{Path: Path{SegString("values")}, Permutation: []int{2, 0, 1}},
			OpMove{Path: Path{SegString("values")}, Permutation: []int{1, 2, 0}},
		}
		for _, operation := range operations {
			if _, err := AssertValidOp(operation); err != nil {
				t.Fatalf("AssertValidOp failed: %v", err)
			}
		}
		wire := NewEncoder().Encode(operations)
		expectedWire := `[["m",["values"],[2,0,1]],["m",[1,2,0]]]`
		if got := tddMustJson(wire); got != expectedWire {
			t.Fatalf("wire mismatch:\n got: %s\nwant: %s", got, expectedWire)
		}
		for _, operation := range wire {
			if _, err := AssertValidWireOp(operation); err != nil {
				t.Fatalf("AssertValidWireOp failed: %v", err)
			}
		}
		decoded, err := NewDecoder().Decode(wire)
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if !tddOpsEqual(decoded, operations) {
			t.Fatalf("decoded mismatch:\n got: %s\nwant: %s", tddMustJson(decoded), tddMustJson(operations))
		}
		if _, err := AssertValidOp([]any{"m", []any{"values"}, []any{float64(0), float64(0)}}); err == nil || !strings.Contains(err.Error(), "bijection") {
			t.Fatalf("expected bijection error, got %v", err)
		}
	})

	t.Run("emits nothing for deeply equal reconstructed values", func(t *testing.T) {
		if ops := DiffRevisions(
			tddObj("value", tddObj("nested", []any{float64(1), float64(2)})),
			tddObj("value", tddObj("nested", []any{float64(1), float64(2)})),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tddMustJson(ops))
		}
		if ops := DiffRevisions(
			tddObj("values", []any{tddObj("id", float64(1)), tddObj("id", float64(2))}),
			tddObj("values", []any{tddObj("id", float64(1)), tddObj("id", float64(2))}),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tddMustJson(ops))
		}
		if ops := DiffRevisions(
			tddObj("values", []any{true, true, true}),
			tddObj("values", []any{true, true, true}),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tddMustJson(ops))
		}
	})

	t.Run("keeps a leaf edit inside a reconstructed array narrow", func(t *testing.T) {
		tddExpectDiff(
			t,
			tddObj("values", []any{
				tddObj("id", float64(1), "label", "one"),
				tddObj("id", float64(2), "label", "two"),
			}),
			tddObj("values", []any{
				tddObj("id", float64(1), "label", "one"),
				tddObj("id", float64(2), "label", "changed"),
			}),
			OpList{
				OpSet{Path: Path{SegString("values"), SegNumber(1), SegString("label")}, Value: "changed"},
			},
		)
	})

	t.Run("emits payload-free splices for scattered removals", func(t *testing.T) {
		a := tddObj("id", "a")
		b := tddObj("id", "b")
		c := tddObj("id", "c")
		d := tddObj("id", "d")
		e := tddObj("id", "e")
		tddExpectDiff(t, tddObj("values", []any{a, b, c, d, e}), tddObj("values", []any{a, c, e}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
			OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 1, Items: []JsonValue{}},
		})
	})

	removalCases := []struct {
		name     string
		before   []any
		after    []any
		expected OpList
	}{
		{
			name:   "front",
			before: []any{float64(1), float64(2), float64(3), float64(4)},
			after:  []any{float64(2), float64(3), float64(4)},
			expected: OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
			},
		},
		{
			name:   "tail",
			before: []any{float64(1), float64(2), float64(3), float64(4)},
			after:  []any{float64(1), float64(2), float64(3)},
			expected: OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 3, Remove: 1, Items: []JsonValue{}},
			},
		},
		{
			name:   "middle",
			before: []any{float64(1), float64(2), float64(3), float64(4)},
			after:  []any{float64(1), float64(3), float64(4)},
			expected: OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
			},
		},
		{
			name:   "all",
			before: []any{float64(1), float64(2), float64(3), float64(4)},
			after:  []any{},
			expected: OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 4, Items: []JsonValue{}},
			},
		},
		{
			name:     "none",
			before:   []any{float64(1), float64(2), float64(3), float64(4)},
			after:    []any{float64(1), float64(2), float64(3), float64(4)},
			expected: OpList{},
		},
	}
	for _, tc := range removalCases {
		t.Run("encodes "+tc.name+" removal canonically", func(t *testing.T) {
			tddExpectDiff(
				t,
				tddObj("values", slices.Clone(tc.before)),
				tddObj("values", slices.Clone(tc.after)),
				tc.expected,
			)
		})
	}

	t.Run("does not field-diff unrelated shifted objects with common fields", func(t *testing.T) {
		before := []any{
			tddObj("type", "row", "value", float64(1)),
			tddObj("type", "row", "value", float64(2)),
			tddObj("type", "row", "value", float64(3)),
		}
		after := []any{
			tddObj("type", "row", "value", float64(2)),
			tddObj("type", "row", "value", float64(3)),
			tddObj("type", "row", "value", float64(4)),
		}
		tddExpectDiff(t, tddObj("values", before), tddObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
			OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 0, Items: []JsonValue{tddObj("type", "row", "value", float64(4))}},
		})
	})

	t.Run("does not treat coincidental id or key fields as structural identity", func(t *testing.T) {
		left := tddObj("value", "left")
		right := tddObj("value", "right")
		before := []any{
			left,
			tddObj("id", float64(1), "key", "a", "value", "first"),
			tddObj("id", float64(2), "key", "b", "value", "second"),
			right,
		}
		replacements := []JsonValue{
			tddObj("id", float64(2), "key", "b", "value", "edited-second"),
			tddObj("id", float64(1), "key", "a", "value", "edited-first"),
		}
		after := []any{left, replacements[0], replacements[1], right}
		tddExpectDiff(t, tddObj("values", before), tddObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 2, Items: replacements},
		})
	})

	t.Run("combines removals, append, and a shared-subtree survivor edit", func(t *testing.T) {
		a := tddObj("id", "a", "stable", tddObj(), "detail", tddObj("text", "a"))
		b := tddObj("id", "b", "stable", tddObj(), "detail", tddObj("text", "b"))
		cStable := tddObj()
		c := tddObj("id", "c", "stable", cStable, "detail", tddObj("text", "c"))
		d := tddObj("id", "d", "stable", tddObj(), "detail", tddObj("text", "d"))
		changedC := tddObj("id", "c", "stable", cStable, "detail", tddObj("text", "changed"))
		appended := tddObj("id", "e", "stable", tddObj(), "detail", tddObj("text", "e"))
		tddExpectDiff(t, tddObj("values", []any{a, b, c, d}), tddObj("values", []any{a, changedC, d, appended}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
			OpAppend{Path: Path{SegString("values"), SegNumber(1), SegString("detail"), SegString("text")}, Text: "hanged"},
			OpSplice{Path: Path{SegString("values")}, Index: 3, Remove: 0, Items: []JsonValue{appended}},
		})
	})

	for _, size := range []int{256 * 1024, 1024 * 1024} {
		t.Run(fmt.Sprintf("does not retain a %d-byte removed-neighbor payload", size), func(t *testing.T) {
			payload := strings.Repeat("x", size)
			retained := []any{
				tddObj("id", "a", "payload", payload),
				tddObj("id", "b", "payload", payload),
				tddObj("id", "c", "payload", payload),
				tddObj("id", "d", "payload", payload),
				tddObj("id", "e", "payload", payload),
			}
			beforeValues := slices.Clone(retained)
			before := tddObj("values", beforeValues)
			after := tddObj("values", []any{retained[0], retained[2], retained[4]})
			ops := DiffRevisions(before, after)
			expected := OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
				OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 1, Items: []JsonValue{}},
			}
			if !tddOpsEqual(ops, expected) {
				t.Fatalf("ops mismatch: %s", tddMustJson(ops))
			}
			if encoded := tddMustJson(ops); len(encoded) >= 100 {
				t.Fatalf("expected encoded len < 100, got %d", len(encoded))
			}
			applied, err := ApplyImmutable(before, ops)
			if err != nil {
				t.Fatalf("ApplyImmutable failed: %v", err)
			}
			if !tddJsonEqual(applied, after) {
				t.Fatalf("applied mismatch")
			}
			if gotBeforeValues, _ := before.Get("values"); !tddJsonEqual(gotBeforeValues, retained) {
				t.Fatalf("before mutated")
			}
		})
	}

	for _, size := range []int{1001, 10000} {
		t.Run(fmt.Sprintf("keeps push, pop, and middle removal narrow at %d items", size), func(t *testing.T) {
			values := make([]any, size)
			for i := 0; i < size; i++ {
				values[i] = tddObj("value", float64(i))
			}
			appended := tddObj("value", float64(size))
			withPush := make([]any, size+1)
			copy(withPush, values)
			withPush[size] = appended
			tddExpectDiff(t, tddObj("values", values), tddObj("values", withPush), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: size, Remove: 0, Items: []JsonValue{appended}},
			})
			tddExpectDiff(t, tddObj("values", values), tddObj("values", slices.Clone(values[:size-1])), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: size - 1, Remove: 1, Items: []JsonValue{}},
			})
			middle := size / 2
			withoutMid := make([]any, 0, size-1)
			withoutMid = append(withoutMid, values[:middle]...)
			withoutMid = append(withoutMid, values[middle+1:]...)
			tddExpectDiff(t, tddObj("values", values), tddObj("values", withoutMid), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: middle, Remove: 1, Items: []JsonValue{}},
			})
		})
	}

	t.Run("keeps forty-thousand-row sparse edits narrow", func(t *testing.T) {
		values := make([]any, 40000)
		for i := 0; i < len(values); i++ {
			values[i] = tddObj("value", float64(i), "stable", tddObj("value", float64(i)))
		}
		after := slices.Clone(values)
		expected := OpList{}
		for idx := 100; idx < len(after); idx += 400 {
			orig := values[idx].(*jsonx.Object)
			stable, _ := orig.Get("stable")
			after[idx] = tddObj("value", float64(-idx), "stable", stable)
			expected = append(expected, OpSet{
				Path:  Path{SegString("values"), SegNumber(idx), SegString("value")},
				Value: float64(-idx),
			})
		}
		ops := DiffRevisions(tddObj("values", values), tddObj("values", after))
		if !tddOpsEqual(ops, expected) {
			t.Fatalf("sparse ops mismatch: got %d ops, want %d ops", len(ops), len(expected))
		}
		if encoded := tddMustJson(ops); len(encoded) >= 7500 {
			t.Fatalf("expected encoded len < 7500, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tddObj("values", values), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tddJsonEqual(applied, tddObj("values", after)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("keeps a reconstructed large-array leaf edit narrow", func(t *testing.T) {
		before := make([]any, 1000)
		for i := 0; i < 1000; i++ {
			before[i] = tddObj("value", float64(i), "label", fmt.Sprintf("row-%d", i))
		}
		after := tddCloneJson(before).([]any)
		after[700].(*jsonx.Object).Set("label", "changed")
		ops := DiffRevisions(tddObj("values", before), tddObj("values", after))
		expected := OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(700), SegString("label")}, Value: "changed"},
		}
		if !tddOpsEqual(ops, expected) {
			t.Fatalf("ops mismatch: got %s", tddMustJson(ops))
		}
		if encoded := tddMustJson(ops); len(encoded) >= 100 {
			t.Fatalf("expected encoded len < 100, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tddObj("values", before), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tddJsonEqual(applied, tddObj("values", after)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("splices an ambiguous equal-count moved-and-edited gap", func(t *testing.T) {
		left := tddObj("value", "left")
		right := tddObj("value", "right")
		before := []any{
			left,
			tddObj("id", float64(1), "value", "a"),
			tddObj("id", float64(2), "value", "b"),
			right,
		}
		replacements := []JsonValue{
			tddObj("id", float64(2), "value", "edited"),
			tddObj("id", float64(1), "value", "also-edited"),
		}
		after := []any{left, replacements[0], replacements[1], right}
		tddExpectDiff(t, tddObj("values", before), tddObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 2, Items: replacements},
		})
	})

	t.Run("keeps a large rotation payload-free", func(t *testing.T) {
		values := make([]any, 10000)
		for i := 0; i < 10000; i++ {
			values[i] = tddObj("value", float64(i))
		}
		rotated := make([]any, 0, 10000)
		rotated = append(rotated, values[1000:]...)
		rotated = append(rotated, values[:1000]...)
		ops := DiffRevisions(tddObj("values", values), tddObj("values", rotated))
		if len(ops) != 1 || ops[0].Verb() != "m" {
			t.Fatalf("expected single m op, got %s", tddMustJson(ops))
		}
		if encoded := tddMustJson(ops); len(encoded) >= 60000 {
			t.Fatalf("expected encoded len < 60000, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tddObj("values", values), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tddJsonEqual(applied, tddObj("values", rotated)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("encodes five hundred unshifts without snapshotting retained rows", func(t *testing.T) {
		retained := make([]any, 10000)
		payload := strings.Repeat("x", 100)
		for i := 0; i < 10000; i++ {
			retained[i] = tddObj("value", float64(i), "payload", payload)
		}
		inserted := make([]JsonValue, 500)
		for i := 0; i < 500; i++ {
			inserted[i] = tddObj("value", float64(-i-1))
		}
		after := make([]any, 0, 10500)
		for _, item := range inserted {
			after = append(after, item)
		}
		after = append(after, retained...)
		ops := DiffRevisions(tddObj("values", retained), tddObj("values", after))
		expected := OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 0, Items: inserted},
		}
		if !tddOpsEqual(ops, expected) {
			t.Fatalf("ops mismatch")
		}
		if encoded := tddMustJson(ops); len(encoded) >= 20000 {
			t.Fatalf("expected encoded len < 20000, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tddObj("values", retained), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tddJsonEqual(applied, tddObj("values", after)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("bounds wide-object operation emission with a root replacement", func(t *testing.T) {
		before := jsonx.NewObject()
		after := jsonx.NewObject()
		for i := 0; i < 20000; i++ {
			k := fmt.Sprintf("field%d", i)
			before.Set(k, float64(0))
			after.Set(k, float64(1))
		}
		ops := DiffRevisions(before, after)
		if len(ops) != 1 || ops[0].Verb() != "r" {
			t.Fatalf("expected single r op, got %d ops", len(ops))
		}
		if r, ok := ops[0].(OpReplace); !ok || r.Value != after {
			t.Fatalf("expected OpReplace{after}")
		}
	})

	t.Run("bounds a wide normalized array fallback with a root replacement", func(t *testing.T) {
		beforeArr := make([]any, 40000)
		afterArr := make([]any, 40000)
		for i := 0; i < 40000; i++ {
			beforeArr[i] = float64(0)
			afterArr[i] = float64(1)
		}
		before := tddObj("values", beforeArr)
		after := tddObj("values", afterArr)
		ops := DiffRevisions(before, after)
		if len(ops) != 1 || ops[0].Verb() != "r" {
			t.Fatalf("expected single r op, got %d ops", len(ops))
		}
		applied, err := ApplyImmutable(before, ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tddJsonEqual(applied, after) {
			t.Fatalf("applied mismatch")
		}
	})
}

func TestDeltaCorePrimitives(t *testing.T) {
	t.Run("Overlap handles ASCII and UTF-16 surrogate pairs", func(t *testing.T) {
		if got := Overlap("hello world", "world!", 64); got != 5 {
			t.Fatalf("expected overlap 5, got %d", got)
		}
		// "😀" is 2 UTF-16 code units; a="x😀", b="😀y" -> overlap is 2 UTF-16 code units
		if got := Overlap("x😀", "😀y", 64); got != 2 {
			t.Fatalf("expected surrogate pair overlap 2, got %d", got)
		}
		// Unpaired/non-splitting: must not report an overlap that splits a surrogate pair unless aligned
		if got := Overlap("abc", "def", 64); got != 0 {
			t.Fatalf("expected overlap 0, got %d", got)
		}
	})

	t.Run("AssertSafePath rejects reserved segments and negative indices", func(t *testing.T) {
		for _, seg := range []string{"__proto__", "constructor", "prototype"} {
			err := AssertSafePath(Path{SegString("ok"), SegString(seg)})
			if err == nil {
				t.Fatalf("expected error for reserved segment %q", seg)
			}
			if _, ok := err.(*UnsafePathError); !ok {
				t.Fatalf("expected *UnsafePathError, got %T", err)
			}
		}
		if err := AssertSafePath(Path{SegNumber(-1)}); err == nil {
			t.Fatalf("expected error for negative path index")
		}
		if err := AssertSafePath(Path{SegString("items"), SegNumber(0)}); err != nil {
			t.Fatalf("expected safe path, got %v", err)
		}
	})

	t.Run("UnmarshalOp and MarshalJSON round-trip all verbs", func(t *testing.T) {
		raws := []string{
			`["r",{"ok":true}]`,
			`["s",["a",0],"hello"]`,
			`["d",["a"]]`,
			`["a",["text"],"!"]`,
			`["t",["text"],3]`,
			`["p",[],0,1,[2]]`,
			`["m",["items"],[1,0]]`,
		}
		for _, raw := range raws {
			op, err := UnmarshalOp([]byte(raw))
			if err != nil {
				t.Fatalf("UnmarshalOp(%s) failed: %v", raw, err)
			}
			out, err := jsonx.Marshal(OpList{op})
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			if want := "[" + raw + "]"; string(out) != want {
				t.Fatalf("round-trip mismatch:\n got: %s\nwant: %s", string(out), want)
			}
		}

		// Malformed op rejected by AssertValidOp and AssertValidWireOp
		if _, err := AssertValidOp([]any{"x", []any{"a"}}); err == nil {
			t.Fatalf("expected error for unknown op")
		}
		if _, err := AssertValidWireOp([]any{"@", float64(-1), []any{"a"}}); err == nil {
			t.Fatalf("expected error for negative define id")
		}
	})

	t.Run("Apply, ApplyImmutableBatches, and ApplyImmutableTrusted", func(t *testing.T) {
		target := tddObj("text", "abcdef", "items", []any{float64(1), float64(2), float64(3)})
		ops := OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 3},
			OpAppend{Path: Path{SegString("text")}, Text: "xyz"},
			OpMove{Path: Path{SegString("items")}, Permutation: []int{2, 0, 1}},
			OpSplice{Path: Path{SegString("items")}, Index: 1, Remove: 1, Items: []JsonValue{float64(9)}},
			OpSet{Path: Path{SegString("added")}, Value: true},
		}

		// Mutable Apply
		mutableClone := tddCloneJson(target).(*jsonx.Object)
		mutated, err := Apply(mutableClone, ops)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		want := tddObj("text", "defxyz", "items", []any{float64(3), float64(9), float64(2)}, "added", true)
		if !tddJsonEqual(mutated, want) {
			t.Fatalf("Apply mismatch: got %s, want %s", tddMustJson(mutated), tddMustJson(want))
		}

		// Immutable Trusted
		snapshotBefore := tddMustJson(target)
		trusted, err := ApplyImmutableTrusted(target, ops)
		if err != nil {
			t.Fatalf("ApplyImmutableTrusted failed: %v", err)
		}
		if !tddJsonEqual(trusted, want) {
			t.Fatalf("ApplyImmutableTrusted mismatch: got %s, want %s", tddMustJson(trusted), tddMustJson(want))
		}
		if tddMustJson(target) != snapshotBefore {
			t.Fatalf("ApplyImmutableTrusted mutated input")
		}

		// ApplyImmutableBatches
		batched, err := ApplyImmutableBatches(target, func(yield func(OpList) bool) {
			if !yield(ops[:2]) {
				return
			}
			yield(ops[2:])
		})
		if err != nil {
			t.Fatalf("ApplyImmutableBatches failed: %v", err)
		}
		if !tddJsonEqual(batched, want) {
			t.Fatalf("ApplyImmutableBatches mismatch: got %s, want %s", tddMustJson(batched), tddMustJson(want))
		}
		if tddMustJson(target) != snapshotBefore {
			t.Fatalf("ApplyImmutableBatches mutated input")
		}
	})

	t.Run("JsonRevisionValidator validates revisions and catches invalid values or cycles", func(t *testing.T) {
		v := NewJsonRevisionValidator()
		shared := tddObj("k", "v")
		rev1 := tddObj("shared", shared, "arr", []any{float64(1), shared})
		if got, err := v.Validate(rev1); err != nil || got != rev1 {
			t.Fatalf("Validate rev1 failed: got=%v err=%v", got, err)
		}
		// Re-validating with shared immutable container should succeed quickly
		rev2 := tddObj("shared", shared, "extra", true)
		if _, err := v.Validate(rev2); err != nil {
			t.Fatalf("Validate rev2 failed: %v", err)
		}

		// Cycle rejected
		cyc := jsonx.NewObject()
		cyc.Set("self", cyc)
		if _, err := v.Validate(cyc); err == nil {
			t.Fatalf("expected cycle error")
		}

		// NaN rejected
		if _, err := v.Validate(math.NaN()); err == nil {
			t.Fatalf("expected NaN error")
		}

		// []byte rejected
		if _, err := v.Validate([]byte{1, 2}); err == nil {
			t.Fatalf("expected []byte error")
		}

		// Nil slice rejected
		if _, err := v.Validate([]any(nil)); err == nil {
			t.Fatalf("expected nil []any slice error")
		}

		// Typed nil object rejected
		if _, err := v.Validate((*jsonx.Object)(nil)); err == nil {
			t.Fatalf("expected typed nil *jsonx.Object error")
		}
	})
}
