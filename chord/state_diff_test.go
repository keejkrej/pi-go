// Ported from packages/chord/test/state-diff.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsdiObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tsdiCloneJson(v JsonValue) JsonValue {
	cloned, err := CopyJson(v, nil)
	if err != nil {
		panic(err)
	}
	return cloned
}

func tsdiMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func tsdiJsonEqual(a, b any) bool {
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
			if !tsdiJsonEqual(va[i], vb[i]) {
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
			if !exists || !tsdiJsonEqual(v, other) {
				return false
			}
		}
		return true
	default:
		return tsdiMustJson(a) == tsdiMustJson(b)
	}
}

func tsdiOpsEqual(a, b OpList) bool {
	if len(a) != len(b) {
		return false
	}
	return tsdiMustJson(a) == tsdiMustJson(b)
}

func tsdiExpectDiff(t *testing.T, before JsonValue, after JsonValue, expected OpList) {
	t.Helper()
	ops := DiffRevisions(before, after)
	if !tsdiOpsEqual(ops, expected) {
		t.Fatalf("ops mismatch:\n got: %s\nwant: %s", tsdiMustJson(ops), tsdiMustJson(expected))
	}
	applied, err := ApplyImmutable(before, ops)
	if err != nil {
		t.Fatalf("ApplyImmutable failed: %v", err)
	}
	if !tsdiJsonEqual(applied, after) {
		t.Fatalf("applied mismatch:\n got: %s\nwant: %s", tsdiMustJson(applied), tsdiMustJson(after))
	}
}

func TestStateDiff(t *testing.T) {
	t.Run("emits sets and deletes", func(t *testing.T) {
		tsdiExpectDiff(
			t,
			tsdiObj("keep", float64(1), "change", float64(1), "remove", true),
			tsdiObj("keep", float64(1), "change", float64(2), "add", float64(3)),
			OpList{
				OpSet{Path: Path{SegString("change")}, Value: float64(2)},
				OpSet{Path: Path{SegString("add")}, Value: float64(3)},
				OpDelete{Path: Path{SegString("remove")}},
			},
		)
	})

	t.Run("emits string append and front truncation", func(t *testing.T) {
		tsdiExpectDiff(t, tsdiObj("text", "hello"), tsdiObj("text", "hello world"), OpList{
			OpAppend{Path: Path{SegString("text")}, Text: " world"},
		})
		tsdiExpectDiff(t, tsdiObj("text", "hello world"), tsdiObj("text", "world"), OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 6},
		})
		tsdiExpectDiff(t, tsdiObj("text", "abcdefgh"), tsdiObj("text", "defghxyz"), OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 3},
			OpAppend{Path: Path{SegString("text")}, Text: "xyz"},
		})
	})

	t.Run("represents array insertion, removal, and shift with splices", func(t *testing.T) {
		a := tsdiObj("id", "a")
		b := tsdiObj("id", "b")
		c := tsdiObj("id", "c")
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b}), tsdiObj("values", []any{a, c, b}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 0, Items: []JsonValue{c}},
		})
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b, c}), tsdiObj("values", []any{b, c}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
		})
	})

	t.Run("collapses a same-length queue update to two splices", func(t *testing.T) {
		a := tsdiObj("id", "a")
		b := tsdiObj("id", "b")
		c := tsdiObj("id", "c")
		d := tsdiObj("id", "d")
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b, c}), tsdiObj("values", []any{b, c, d}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
			OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 0, Items: []JsonValue{d}},
		})
	})

	t.Run("emits a permutation for a pure reorder", func(t *testing.T) {
		a := tsdiObj("id", "a")
		b := tsdiObj("id", "b")
		c := tsdiObj("id", "c")
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b, c}), tsdiObj("values", []any{c, a, b}), OpList{
			OpMove{Path: Path{SegString("values")}, Permutation: []int{2, 0, 1}},
		})
	})

	t.Run("normalizes reordered distinct deeply-equal objects to a no-op", func(t *testing.T) {
		first := tsdiObj("nested", tsdiObj("value", float64(1)))
		second := tsdiObj("nested", tsdiObj("value", float64(1)))
		if first == second {
			t.Fatalf("expected distinct object pointers")
		}
		ops := DiffRevisions(tsdiObj("values", []any{first, second}), tsdiObj("values", []any{second, first}))
		if len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tsdiMustJson(ops))
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
		if got := tsdiMustJson(wire); got != expectedWire {
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
		if !tsdiOpsEqual(decoded, operations) {
			t.Fatalf("decoded mismatch:\n got: %s\nwant: %s", tsdiMustJson(decoded), tsdiMustJson(operations))
		}
		if _, err := AssertValidOp([]any{"m", []any{"values"}, []any{float64(0), float64(0)}}); err == nil || !strings.Contains(err.Error(), "bijection") {
			t.Fatalf("expected bijection error, got %v", err)
		}
	})

	t.Run("emits nothing for deeply equal reconstructed values", func(t *testing.T) {
		if ops := DiffRevisions(
			tsdiObj("value", tsdiObj("nested", []any{float64(1), float64(2)})),
			tsdiObj("value", tsdiObj("nested", []any{float64(1), float64(2)})),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tsdiMustJson(ops))
		}
		if ops := DiffRevisions(
			tsdiObj("values", []any{tsdiObj("id", float64(1)), tsdiObj("id", float64(2))}),
			tsdiObj("values", []any{tsdiObj("id", float64(1)), tsdiObj("id", float64(2))}),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tsdiMustJson(ops))
		}
		if ops := DiffRevisions(
			tsdiObj("values", []any{true, true, true}),
			tsdiObj("values", []any{true, true, true}),
		); len(ops) != 0 {
			t.Fatalf("expected empty ops, got %s", tsdiMustJson(ops))
		}
	})

	t.Run("keeps a leaf edit inside a reconstructed array narrow", func(t *testing.T) {
		tsdiExpectDiff(
			t,
			tsdiObj("values", []any{
				tsdiObj("id", float64(1), "label", "one"),
				tsdiObj("id", float64(2), "label", "two"),
			}),
			tsdiObj("values", []any{
				tsdiObj("id", float64(1), "label", "one"),
				tsdiObj("id", float64(2), "label", "changed"),
			}),
			OpList{
				OpSet{Path: Path{SegString("values"), SegNumber(1), SegString("label")}, Value: "changed"},
			},
		)
	})

	t.Run("emits payload-free splices for scattered removals", func(t *testing.T) {
		a := tsdiObj("id", "a")
		b := tsdiObj("id", "b")
		c := tsdiObj("id", "c")
		d := tsdiObj("id", "d")
		e := tsdiObj("id", "e")
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b, c, d, e}), tsdiObj("values", []any{a, c, e}), OpList{
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
			tsdiExpectDiff(
				t,
				tsdiObj("values", slices.Clone(tc.before)),
				tsdiObj("values", slices.Clone(tc.after)),
				tc.expected,
			)
		})
	}

	t.Run("does not field-diff unrelated shifted objects with common fields", func(t *testing.T) {
		before := []any{
			tsdiObj("type", "row", "value", float64(1)),
			tsdiObj("type", "row", "value", float64(2)),
			tsdiObj("type", "row", "value", float64(3)),
		}
		after := []any{
			tsdiObj("type", "row", "value", float64(2)),
			tsdiObj("type", "row", "value", float64(3)),
			tsdiObj("type", "row", "value", float64(4)),
		}
		tsdiExpectDiff(t, tsdiObj("values", before), tsdiObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1, Items: []JsonValue{}},
			OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 0, Items: []JsonValue{tsdiObj("type", "row", "value", float64(4))}},
		})
	})

	t.Run("does not treat coincidental id or key fields as structural identity", func(t *testing.T) {
		left := tsdiObj("value", "left")
		right := tsdiObj("value", "right")
		before := []any{
			left,
			tsdiObj("id", float64(1), "key", "a", "value", "first"),
			tsdiObj("id", float64(2), "key", "b", "value", "second"),
			right,
		}
		replacements := []JsonValue{
			tsdiObj("id", float64(2), "key", "b", "value", "edited-second"),
			tsdiObj("id", float64(1), "key", "a", "value", "edited-first"),
		}
		after := []any{left, replacements[0], replacements[1], right}
		tsdiExpectDiff(t, tsdiObj("values", before), tsdiObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 2, Items: replacements},
		})
	})

	t.Run("combines removals, append, and a shared-subtree survivor edit", func(t *testing.T) {
		a := tsdiObj("id", "a", "stable", tsdiObj(), "detail", tsdiObj("text", "a"))
		b := tsdiObj("id", "b", "stable", tsdiObj(), "detail", tsdiObj("text", "b"))
		cStable := tsdiObj()
		c := tsdiObj("id", "c", "stable", cStable, "detail", tsdiObj("text", "c"))
		d := tsdiObj("id", "d", "stable", tsdiObj(), "detail", tsdiObj("text", "d"))
		changedC := tsdiObj("id", "c", "stable", cStable, "detail", tsdiObj("text", "changed"))
		appended := tsdiObj("id", "e", "stable", tsdiObj(), "detail", tsdiObj("text", "e"))
		tsdiExpectDiff(t, tsdiObj("values", []any{a, b, c, d}), tsdiObj("values", []any{a, changedC, d, appended}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
			OpAppend{Path: Path{SegString("values"), SegNumber(1), SegString("detail"), SegString("text")}, Text: "hanged"},
			OpSplice{Path: Path{SegString("values")}, Index: 3, Remove: 0, Items: []JsonValue{appended}},
		})
	})

	for _, size := range []int{256 * 1024, 1024 * 1024} {
		t.Run(fmt.Sprintf("does not retain a %d-byte removed-neighbor payload", size), func(t *testing.T) {
			payload := strings.Repeat("x", size)
			retained := []any{
				tsdiObj("id", "a", "payload", payload),
				tsdiObj("id", "b", "payload", payload),
				tsdiObj("id", "c", "payload", payload),
				tsdiObj("id", "d", "payload", payload),
				tsdiObj("id", "e", "payload", payload),
			}
			beforeValues := slices.Clone(retained)
			before := tsdiObj("values", beforeValues)
			after := tsdiObj("values", []any{retained[0], retained[2], retained[4]})
			ops := DiffRevisions(before, after)
			expected := OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{}},
				OpSplice{Path: Path{SegString("values")}, Index: 2, Remove: 1, Items: []JsonValue{}},
			}
			if !tsdiOpsEqual(ops, expected) {
				t.Fatalf("ops mismatch: %s", tsdiMustJson(ops))
			}
			if encoded := tsdiMustJson(ops); len(encoded) >= 100 {
				t.Fatalf("expected encoded len < 100, got %d", len(encoded))
			}
			applied, err := ApplyImmutable(before, ops)
			if err != nil {
				t.Fatalf("ApplyImmutable failed: %v", err)
			}
			if !tsdiJsonEqual(applied, after) {
				t.Fatalf("applied mismatch")
			}
			if gotBeforeValues, _ := before.Get("values"); !tsdiJsonEqual(gotBeforeValues, retained) {
				t.Fatalf("before mutated")
			}
		})
	}

	for _, size := range []int{1001, 10000} {
		t.Run(fmt.Sprintf("keeps push, pop, and middle removal narrow at %d items", size), func(t *testing.T) {
			values := make([]any, size)
			for i := 0; i < size; i++ {
				values[i] = tsdiObj("value", float64(i))
			}
			appended := tsdiObj("value", float64(size))
			withPush := make([]any, size+1)
			copy(withPush, values)
			withPush[size] = appended
			tsdiExpectDiff(t, tsdiObj("values", values), tsdiObj("values", withPush), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: size, Remove: 0, Items: []JsonValue{appended}},
			})
			tsdiExpectDiff(t, tsdiObj("values", values), tsdiObj("values", slices.Clone(values[:size-1])), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: size - 1, Remove: 1, Items: []JsonValue{}},
			})
			middle := size / 2
			withoutMid := make([]any, 0, size-1)
			withoutMid = append(withoutMid, values[:middle]...)
			withoutMid = append(withoutMid, values[middle+1:]...)
			tsdiExpectDiff(t, tsdiObj("values", values), tsdiObj("values", withoutMid), OpList{
				OpSplice{Path: Path{SegString("values")}, Index: middle, Remove: 1, Items: []JsonValue{}},
			})
		})
	}

	t.Run("keeps forty-thousand-row sparse edits narrow", func(t *testing.T) {
		values := make([]any, 40000)
		for i := 0; i < len(values); i++ {
			values[i] = tsdiObj("value", float64(i), "stable", tsdiObj("value", float64(i)))
		}
		after := slices.Clone(values)
		expected := OpList{}
		for idx := 100; idx < len(after); idx += 400 {
			orig := values[idx].(*jsonx.Object)
			stable, _ := orig.Get("stable")
			after[idx] = tsdiObj("value", float64(-idx), "stable", stable)
			expected = append(expected, OpSet{
				Path:  Path{SegString("values"), SegNumber(idx), SegString("value")},
				Value: float64(-idx),
			})
		}
		ops := DiffRevisions(tsdiObj("values", values), tsdiObj("values", after))
		if !tsdiOpsEqual(ops, expected) {
			t.Fatalf("sparse ops mismatch: got %d ops, want %d ops", len(ops), len(expected))
		}
		if encoded := tsdiMustJson(ops); len(encoded) >= 7500 {
			t.Fatalf("expected encoded len < 7500, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tsdiObj("values", values), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tsdiJsonEqual(applied, tsdiObj("values", after)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("keeps a reconstructed large-array leaf edit narrow", func(t *testing.T) {
		before := make([]any, 1000)
		for i := 0; i < 1000; i++ {
			before[i] = tsdiObj("value", float64(i), "label", fmt.Sprintf("row-%d", i))
		}
		after := tsdiCloneJson(before).([]any)
		after[700].(*jsonx.Object).Set("label", "changed")
		ops := DiffRevisions(tsdiObj("values", before), tsdiObj("values", after))
		expected := OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(700), SegString("label")}, Value: "changed"},
		}
		if !tsdiOpsEqual(ops, expected) {
			t.Fatalf("ops mismatch: got %s", tsdiMustJson(ops))
		}
		if encoded := tsdiMustJson(ops); len(encoded) >= 100 {
			t.Fatalf("expected encoded len < 100, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tsdiObj("values", before), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tsdiJsonEqual(applied, tsdiObj("values", after)) {
			t.Fatalf("applied mismatch")
		}
	})

	t.Run("splices an ambiguous equal-count moved-and-edited gap", func(t *testing.T) {
		left := tsdiObj("value", "left")
		right := tsdiObj("value", "right")
		before := []any{
			left,
			tsdiObj("id", float64(1), "value", "a"),
			tsdiObj("id", float64(2), "value", "b"),
			right,
		}
		replacements := []JsonValue{
			tsdiObj("id", float64(2), "value", "edited"),
			tsdiObj("id", float64(1), "value", "also-edited"),
		}
		after := []any{left, replacements[0], replacements[1], right}
		tsdiExpectDiff(t, tsdiObj("values", before), tsdiObj("values", after), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 2, Items: replacements},
		})
	})

	t.Run("encodes five hundred unshifts without snapshotting retained rows", func(t *testing.T) {
		retained := make([]any, 10000)
		payload := strings.Repeat("x", 100)
		for i := 0; i < 10000; i++ {
			retained[i] = tsdiObj("value", float64(i), "payload", payload)
		}
		inserted := make([]JsonValue, 500)
		for i := 0; i < 500; i++ {
			inserted[i] = tsdiObj("value", float64(-i-1))
		}
		after := make([]any, 0, 10500)
		for _, item := range inserted {
			after = append(after, item)
		}
		after = append(after, retained...)
		ops := DiffRevisions(tsdiObj("values", retained), tsdiObj("values", after))
		expected := OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 0, Items: inserted},
		}
		if !tsdiOpsEqual(ops, expected) {
			t.Fatalf("ops mismatch")
		}
		if encoded := tsdiMustJson(ops); len(encoded) >= 20000 {
			t.Fatalf("expected encoded len < 20000, got %d", len(encoded))
		}
		applied, err := ApplyImmutable(tsdiObj("values", retained), ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if !tsdiJsonEqual(applied, tsdiObj("values", after)) {
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

	t.Run("falls back to a base operation when leaf operations are larger", func(t *testing.T) {
		beforeArr := make([]any, 40000)
		afterArr := make([]any, 40000)
		for i := 0; i < 40000; i++ {
			beforeArr[i] = float64(0)
			afterArr[i] = float64(1)
		}
		before := tsdiObj("values", beforeArr)
		after := tsdiObj("values", afterArr)
		ops := DiffRevisions(before, after)
		if len(ops) != 1 || ops[0].Verb() != "r" {
			t.Fatalf("expected single r op, got %d ops", len(ops))
		}
	})
}
