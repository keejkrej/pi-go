// Ported from packages/chord/test/delta.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tdeltaObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tdeltaMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func tdeltaCloneJson(v any) any {
	c, err := CopyJson(v, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func TestDeltaTrackerLifecycle(t *testing.T) {
	t.Run("adopts an immutable next revision by pointer swap", func(t *testing.T) {
		input := tdeltaObj(
			"count", float64(1),
			"nested", tdeltaObj("text", "a"),
			"values", []any{float64(1)},
		)
		tracker := Track(input)
		if tracker.Value() != input {
			t.Fatalf("expected tracker.Value() == input")
		}

		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State() failed: %v", err)
		}
		draft.Value.Set("count", float64(2))
		nested, _ := draft.Value.Get("nested")
		nestedObj := nested.(*jsonx.Object)
		text, _ := nestedObj.Get("text")
		nestedObj.Set("text", text.(string)+"b")
		vals, _ := draft.Value.Get("values")
		draft.Value.Set("values", append(vals.([]any), float64(2)))

		if tdeltaMustJson(tracker.Value()) != tdeltaMustJson(input) {
			t.Fatalf("input mutated unexpectedly")
		}

		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare() failed: %v", err)
		}
		if prepared.Base() != input {
			t.Fatalf("expected prepared.Base() == input")
		}
		wantNext := `{"count":2,"nested":{"text":"ab"},"values":[1,2]}`
		if got := tdeltaMustJson(prepared.Value()); got != wantNext {
			t.Fatalf("prepared.Value mismatch: got %s, want %s", got, wantNext)
		}
		wantOps := `[["s",["count"],2],["a",["nested","text"],"b"],["p",["values"],1,0,[2]]]`
		if got := tdeltaMustJson(OpList(prepared.Ops())); got != wantOps {
			t.Fatalf("prepared.Ops mismatch: got %s, want %s", got, wantOps)
		}
		if _, err := change.State(); err == nil {
			t.Fatalf("expected settled draft State() to fail")
		}
		if tracker.Value() != input {
			t.Fatalf("tracker mutated before Adopt")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if tracker.Value() != prepared.Value() {
			t.Fatalf("expected tracker.Value() == prepared.Value()")
		}
	})

	t.Run("aborts and revokes without changing the committed value", func(t *testing.T) {
		tracker := Track(tdeltaObj("child", tdeltaObj("value", float64(1))))
		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State failed: %v", err)
		}
		child, _ := draft.Value.Get("child")
		child.(*jsonx.Object).Set("value", float64(2))
		change.Abort()

		committedChild, _ := tracker.Value().Get("child")
		if v, _ := committedChild.(*jsonx.Object).Get("value"); v != float64(1) {
			t.Fatalf("committed value mutated: got %v", v)
		}
		if _, err := change.State(); err == nil {
			t.Fatalf("expected settled State() to fail")
		}
		change.Abort() // idempotent
		if _, err := change.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
			t.Fatalf("expected settled Prepare error, got %v", err)
		}
		next := tracker.BeginChange()
		next.Abort()
	})

	t.Run("grows arrays with explicit nulls and revokes drafts after preparation", func(t *testing.T) {
		tracker := Track(tdeltaObj("values", []any{float64(1), float64(2)}))
		change := tracker.BeginChange()
		draft, err := change.State()
		if err != nil {
			t.Fatalf("State failed: %v", err)
		}
		draft.Value.Set("values", []any{float64(1), float64(2), nil, nil})
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		vals, _ := prepared.Value().Get("values")
		if got := tdeltaMustJson(vals); got != `[1,2,null,null]` {
			t.Fatalf("got %s", got)
		}
		if _, err := change.State(); err == nil {
			t.Fatalf("expected settled State() error")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
	})

	t.Run("normalizes a deep no-op to exact previous identity", func(t *testing.T) {
		tracker := Track(tdeltaObj("value", tdeltaObj("nested", []any{float64(1), float64(2)})))
		change := tracker.BeginChange()
		draft, _ := change.State()
		draft.Value.Set("value", tdeltaObj("nested", []any{float64(1), float64(2)}))
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		if prepared.Value() != prepared.Base() {
			t.Fatalf("expected exact base identity")
		}
		if len(prepared.Ops()) != 0 {
			t.Fatalf("expected empty ops")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if tracker.Value() != prepared.Base() {
			t.Fatalf("expected tracker.Value() == prepared.Base()")
		}
	})

	t.Run("takes immutable ownership of replacement input and applies no-op normalization", func(t *testing.T) {
		tracker := Track(tdeltaObj("nested", tdeltaObj("value", float64(1))))
		replacement := tdeltaObj("nested", tdeltaObj("value", float64(2)))
		prepared, err := tracker.PrepareReplace(replacement)
		if err != nil {
			t.Fatalf("PrepareReplace failed: %v", err)
		}
		if prepared.Value() != replacement {
			t.Fatalf("expected prepared.Value() == replacement")
		}
		if len(prepared.Ops()) != 1 || prepared.Ops()[0].Verb() != "r" {
			t.Fatalf("expected r op")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}

		noOp, err := tracker.PrepareReplace(tdeltaObj("nested", tdeltaObj("value", float64(2))))
		if err != nil {
			t.Fatalf("PrepareReplace noOp failed: %v", err)
		}
		if noOp.Value() != tracker.Value() {
			t.Fatalf("expected noOp.Value() == tracker.Value()")
		}
		if len(noOp.Ops()) != 0 {
			t.Fatalf("expected empty ops on no-op")
		}
	})

	t.Run("shares immutable operation placements with the prepared candidate", func(t *testing.T) {
		tracker := Track(tdeltaObj("rows", []any{}))
		change := tracker.BeginChange()
		draft, _ := change.State()
		draft.Value.Set("rows", []any{tdeltaObj("id", float64(1))})
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}
		if len(prepared.Ops()) != 1 {
			t.Fatalf("expected 1 op, got %d", len(prepared.Ops()))
		}
		splice, ok := prepared.Ops()[0].(OpSplice)
		if !ok {
			t.Fatalf("expected OpSplice, got %T", prepared.Ops()[0])
		}
		rowsVal, _ := prepared.Value().Get("rows")
		rowsSlice := rowsVal.([]any)
		if splice.Items[0] != rowsSlice[0] {
			t.Fatalf("expected shared payload pointer with prepared candidate")
		}
	})

	t.Run("rejects foreign, stale, and repeated preparations while allowing competing changes", func(t *testing.T) {
		first := Track(tdeltaObj("value", float64(0)))
		second := Track(tdeltaObj("value", float64(0)))
		change := first.BeginChange()
		competing := first.BeginChange()
		d1, _ := change.State()
		d2, _ := competing.State()
		d1.Value.Set("value", float64(1))
		d2.Value.Set("value", float64(2))
		prepared, _ := change.Prepare()
		competingPrepared, _ := competing.Prepare()

		if err := second.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "different tracker") {
			t.Fatalf("expected different tracker error, got %v", err)
		}
		if err := first.Adopt(prepared); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if err := first.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "already been used") {
			t.Fatalf("expected already been used error, got %v", err)
		}
		if err := first.Adopt(competingPrepared); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected stale error, got %v", err)
		}

		stale, _ := first.PrepareReplace(tdeltaObj("value", float64(2)))
		winner, _ := first.PrepareReplace(tdeltaObj("value", float64(3)))
		if err := first.Adopt(winner); err != nil {
			t.Fatalf("Adopt winner failed: %v", err)
		}
		if err := first.Adopt(stale); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected stale error, got %v", err)
		}
		if err := first.Adopt(stale); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected repeated stale error, got %v", err)
		}
	})

	t.Run("invalidates a prepared result when its change is aborted", func(t *testing.T) {
		tracker := Track(tdeltaObj("value", float64(0)))
		change := tracker.BeginChange()
		d, _ := change.State()
		d.Value.Set("value", float64(1))
		prepared, _ := change.Prepare()
		change.Abort()
		if err := tracker.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("expected aborted error, got %v", err)
		}
		change.Abort()
	})

	t.Run("makes competing same-base preparations stale after adopting a no-op", func(t *testing.T) {
		tracker := Track(tdeltaObj("value", tdeltaObj("count", float64(1))))
		first, _ := tracker.PrepareReplace(tdeltaObj("value", tdeltaObj("count", float64(1))))
		competing, _ := tracker.PrepareReplace(tdeltaObj("value", tdeltaObj("count", float64(1))))
		if first.Value() != first.Base() || competing.Value() != competing.Base() {
			t.Fatalf("expected base identity on no-op")
		}
		if err := tracker.Adopt(first); err != nil {
			t.Fatalf("Adopt failed: %v", err)
		}
		if err := tracker.Adopt(first); err == nil || !strings.Contains(err.Error(), "already been used") {
			t.Fatalf("expected already used error, got %v", err)
		}
		if err := tracker.Adopt(competing); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected stale error, got %v", err)
		}
	})

	t.Run("emits an owned root replacement without traversing large replacement input", func(t *testing.T) {
		rows := make([]any, 10000)
		for i := range rows {
			rows[i] = tdeltaObj("value", float64(i), "stable", tdeltaObj("value", float64(i)))
		}
		tracker := Track(tdeltaObj("rows", rows))
		replacementRows := make([]any, len(rows))
		copy(replacementRows, rows)
		midStable, _ := replacementRows[5000].(*jsonx.Object).Get("stable")
		replacementRows[5000] = tdeltaObj("value", float64(-1), "stable", midStable)
		replacement := tdeltaObj("rows", replacementRows)

		prepared, err := tracker.PrepareReplace(replacement)
		if err != nil {
			t.Fatalf("PrepareReplace failed: %v", err)
		}
		if prepared.Value() != replacement {
			t.Fatalf("expected exact replacement pointer")
		}
		if len(prepared.Ops()) != 1 || prepared.Ops()[0].Verb() != "r" {
			t.Fatalf("expected single r op")
		}
		applied, err := ApplyImmutable(prepared.Base(), prepared.Ops())
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if tdeltaMustJson(applied) != tdeltaMustJson(prepared.Value()) {
			t.Fatalf("applied mismatch")
		}
	})
}

func TestCanonicalStrings(t *testing.T) {
	t.Run("emits append and rolling-window operations", func(t *testing.T) {
		tracker := Track(tdeltaObj("text", "abcdefgh"))
		change := tracker.BeginChange()
		d, _ := change.State()
		s, _ := d.Value.Get("text")
		d.Value.Set("text", s.(string)+"ij")
		prepared, _ := change.Prepare()
		if got := tdeltaMustJson(OpList(prepared.Ops())); got != `[["a",["text"],"ij"]]` {
			t.Fatalf("got %s", got)
		}
		_ = tracker.Adopt(prepared)

		change = tracker.BeginChange()
		d, _ = change.State()
		s, _ = d.Value.Get("text")
		d.Value.Set("text", s.(string)[3:]+"xyz")
		prepared, _ = change.Prepare()
		if got := tdeltaMustJson(OpList(prepared.Ops())); got != `[["t",["text"],3],["a",["text"],"xyz"]]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("finds bounded overlaps", func(t *testing.T) {
		if got := Overlap("abcdefgh", "defghxyz", 65536); got != 5 {
			t.Fatalf("got %d", got)
		}
		if got := Overlap("abcdef", "defghi", 0); got != 0 {
			t.Fatalf("got %d", got)
		}
	})
}

func TestOperationApplicationAndValidation(t *testing.T) {
	t.Run("applies mutable and immutable operations", func(t *testing.T) {
		ops := OpList{
			OpAppend{Path: Path{SegString("text")}, Text: "b"},
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{float64(3), float64(4)}},
			OpSet{Path: Path{SegString("nested"), SegString("value")}, Value: float64(2)},
		}
		stable := tdeltaObj("value", float64(9))
		base := tdeltaObj(
			"text", "a",
			"values", []any{float64(1), float64(2)},
			"nested", tdeltaObj("value", float64(1)),
			"stable", stable,
		)
		immutable, err := ApplyImmutable(base, ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		want := `{"text":"ab","values":[1,3,4],"nested":{"value":2},"stable":{"value":9}}`
		if got := tdeltaMustJson(immutable); got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
		if gotStable, _ := immutable.Get("stable"); gotStable != stable {
			t.Fatalf("expected stable subtree pointer shared")
		}
		mutated, err := Apply(tdeltaCloneJson(base).(*jsonx.Object), ops)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if tdeltaMustJson(mutated) != want {
			t.Fatalf("mutable apply mismatch")
		}
	})

	t.Run("supports root replacement, root splice, and permutations", func(t *testing.T) {
		base := OpList{OpReplace{Value: []any{float64(1), float64(2), float64(3)}}}
		if !IsBase(base) {
			t.Fatalf("expected IsBase true")
		}
		val, err := Apply[any](nil, base)
		if err != nil {
			t.Fatalf("Apply replace failed: %v", err)
		}
		val, err = Apply(val, OpList{OpSplice{Path: Path{}, Index: 1, Remove: 1, Items: []JsonValue{float64(4)}}})
		if err != nil {
			t.Fatalf("Apply splice failed: %v", err)
		}
		val, err = Apply(val, OpList{OpMove{Path: Path{}, Permutation: []int{2, 0, 1}}})
		if err != nil {
			t.Fatalf("Apply move failed: %v", err)
		}
		if got := tdeltaMustJson(val); got != `[3,1,4]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("rejects unsafe and malformed paths", func(t *testing.T) {
		if _, err := Apply(tdeltaObj(), OpList{OpSet{Path: Path{SegString("constructor"), SegString("prototype"), SegString("x")}, Value: true}}); err == nil {
			t.Fatalf("expected UnsafePathError")
		} else if _, ok := err.(*UnsafePathError); !ok {
			t.Fatalf("expected *UnsafePathError, got %T", err)
		}
		if _, err := Apply(tdeltaObj("values", []any{float64(1)}), OpList{OpSet{Path: Path{SegString("values"), SegNumber(3)}, Value: float64(2)}}); err == nil {
			t.Fatalf("expected UnsafePathError for array gap")
		} else if _, ok := err.(*UnsafePathError); !ok {
			t.Fatalf("expected *UnsafePathError, got %T", err)
		}
		if _, err := Apply(tdeltaObj("value", float64(1)), OpList{OpAppend{Path: Path{SegString("value")}, Text: "x"}}); err == nil {
			t.Fatalf("expected error appending to number")
		}
		if _, err := AssertValidOp([]any{"s", "value", float64(1)}); err == nil {
			t.Fatalf("expected non-array path error")
		}
		if _, err := AssertValidOp([]any{"m", []any{}, []any{float64(0), float64(0)}}); err == nil || !strings.Contains(err.Error(), "bijection") {
			t.Fatalf("expected bijection error, got %v", err)
		}
	})

	t.Run("validates decoded and wire vocabularies separately", func(t *testing.T) {
		if _, err := AssertValidOp([]any{"s", []any{"value"}, float64(1)}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := AssertValidOp([]any{"s", float64(1)}); err == nil {
			t.Fatalf("expected error for wire short op passed to AssertValidOp")
		}
		if _, err := AssertValidWireOp([]any{"s", float64(1)}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := AssertValidWireOp([]any{"#", float64(0), []any{"value"}}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDeltaCodec(t *testing.T) {
	t.Run("interns paths, omits adjacent paths, and round-trips", func(t *testing.T) {
		path := Path{SegString("nested"), SegString("text")}
		enc := NewEncoder()
		dec := NewDecoder()
		first := OpList{
			OpTruncate{Path: path, Count: 1},
			OpAppend{Path: path, Text: "x"},
		}
		gotFirst, err := dec.Decode(enc.Encode(first))
		if err != nil {
			t.Fatalf("Decode first failed: %v", err)
		}
		if tdeltaMustJson(gotFirst) != tdeltaMustJson(first) {
			t.Fatalf("first roundtrip mismatch")
		}
		second := OpList{OpAppend{Path: path, Text: "y"}}
		wire := enc.Encode(second)
		if got := tdeltaMustJson(wire); got != `[["#",0,["nested","text"]],["a",0,"y"]]` {
			t.Fatalf("wire mismatch: %s", got)
		}
		gotSecond, err := dec.Decode(wire)
		if err != nil {
			t.Fatalf("Decode second failed: %v", err)
		}
		if tdeltaMustJson(gotSecond) != tdeltaMustJson(second) {
			t.Fatalf("second roundtrip mismatch")
		}
	})

	t.Run("resets path dictionaries on a base", func(t *testing.T) {
		enc := NewEncoder()
		path := Path{SegString("value")}
		enc.Encode(OpList{OpSet{Path: path, Value: float64(1)}})
		enc.Encode(OpList{OpSet{Path: path, Value: float64(2)}})
		if got := tdeltaMustJson(enc.Encode(OpList{OpReplace{Value: tdeltaObj("value", float64(3))}})); got != `[["r",{"value":3}]]` {
			t.Fatalf("got %s", got)
		}
		if got := tdeltaMustJson(enc.Encode(OpList{OpSet{Path: path, Value: float64(4)}})); got != `[["s",["value"],4]]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("rejects unresolved short forms and unsafe interned paths", func(t *testing.T) {
		if _, err := NewDecoder().Decode(WireOpList{WireOpAppendShort{Text: "x"}}); err == nil {
			t.Fatalf("expected unresolved short form error")
		}
		wire := WireOpList{
			WireOpDefine{Id: 0, Path: Path{SegString("__proto__")}},
			WireOpSet{Ref: PathRef{Id: 0, IsId: true}, Value: true},
		}
		if _, err := NewDecoder().Decode(wire); err == nil {
			t.Fatalf("expected UnsafePathError")
		} else if _, ok := err.(*UnsafePathError); !ok {
			t.Fatalf("expected *UnsafePathError, got %T", err)
		}
	})

	t.Run("omits an adjacent repeated path and interns on second use", func(t *testing.T) {
		enc := NewEncoder()
		path := Path{SegString("a"), SegString("deep")}
		if got := tdeltaMustJson(enc.Encode(OpList{OpAppend{Path: path, Text: "1"}})); got != `[["a",["a","deep"],"1"]]` {
			t.Fatalf("got %s", got)
		}
		if got := tdeltaMustJson(enc.Encode(OpList{OpAppend{Path: path, Text: "2"}})); got != `[["#",0,["a","deep"]],["a",0,"2"]]` {
			t.Fatalf("got %s", got)
		}
		if got := tdeltaMustJson(NewEncoder().Encode(OpList{
			OpSet{Path: Path{SegString("value")}, Value: float64(1)},
			OpSet{Path: Path{SegString("value")}, Value: float64(2)},
		})); got != `[["s",["value"],1],["s",2]]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("does not collide paths containing null characters", func(t *testing.T) {
		ops := OpList{
			OpSet{Path: Path{SegString("a\x00b")}, Value: float64(1)},
			OpSet{Path: Path{SegString("a"), SegString("b")}, Value: float64(2)},
		}
		dec, err := NewDecoder().Decode(NewEncoder().Encode(ops))
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if tdeltaMustJson(dec) != tdeltaMustJson(ops) {
			t.Fatalf("roundtrip mismatch")
		}
	})

	t.Run("clears decoder ids on a base batch and makes batches after base self-contained", func(t *testing.T) {
		dec := NewDecoder()
		_, _ = dec.Decode(WireOpList{
			WireOpDefine{Id: 0, Path: Path{SegString("a")}},
			WireOpAppend{Ref: PathRef{Id: 0, IsId: true}, Text: "1"},
		})
		_, _ = dec.Decode(WireOpList{WireOpReplace{Value: tdeltaObj("a", "")}})
		if _, err := dec.Decode(WireOpList{WireOpAppend{Ref: PathRef{Id: 0, IsId: true}, Text: "2"}}); err == nil {
			t.Fatalf("expected cleared id error")
		}

		enc := NewEncoder()
		path := Path{SegString("a"), SegString("deep")}
		enc.Encode(OpList{OpAppend{Path: path, Text: "1"}})
		enc.Encode(OpList{OpAppend{Path: path, Text: "2"}})
		base := enc.Encode(OpList{OpReplace{Value: tdeltaObj("a", tdeltaObj("deep", "x"))}})
		after := enc.Encode(OpList{OpAppend{Path: path, Text: "3"}})
		if tdeltaMustJson(after) != `[["a",["a","deep"],"3"]]` {
			t.Fatalf("got %s", tdeltaMustJson(after))
		}
		freshDec := NewDecoder()
		if _, err := freshDec.Decode(base); err != nil {
			t.Fatalf("Decode base failed: %v", err)
		}
		gotAfter, err := freshDec.Decode(after)
		if err != nil || tdeltaMustJson(gotAfter) != `[["a",["a","deep"],"3"]]` {
			t.Fatalf("got=%s err=%v", tdeltaMustJson(gotAfter), err)
		}
	})

	t.Run("round-trips deterministic mixed operation streams", func(t *testing.T) {
		batches := make([]OpList, 100)
		for i := 0; i < 100; i++ {
			batches[i] = OpList{
				OpSet{Path: Path{SegString("rows"), SegNumber(i), SegString("value")}, Value: float64(i)},
				OpAppend{Path: Path{SegString("output")}, Text: fmt.Sprintf("%d", i)},
				OpSplice{Path: Path{SegString("tail")}, Index: i, Remove: 0, Items: []JsonValue{float64(i)}},
			}
		}
		enc := NewEncoder()
		dec := NewDecoder()
		for i, batch := range batches {
			got, err := dec.Decode(enc.Encode(batch))
			if err != nil {
				t.Fatalf("batch %d failed: %v", i, err)
			}
			if tdeltaMustJson(got) != tdeltaMustJson(batch) {
				t.Fatalf("batch %d mismatch", i)
			}
		}
	})
}

func TestImmutableApplicationOwnership(t *testing.T) {
	t.Run("does not mutate a replacement payload targeted by a later operation", func(t *testing.T) {
		replacement := tdeltaObj("nested", tdeltaObj("value", float64(1)))
		next, err := ApplyImmutable[any](nil, OpList{
			OpReplace{Value: replacement},
			OpSet{Path: Path{SegString("nested"), SegString("value")}, Value: float64(2)},
		})
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		nestedOrig, _ := replacement.Get("nested")
		if v, _ := nestedOrig.(*jsonx.Object).Get("value"); v != float64(1) {
			t.Fatalf("original replacement mutated: %v", v)
		}
		nestedNext, _ := next.(*jsonx.Object).Get("nested")
		if v, _ := nestedNext.(*jsonx.Object).Get("value"); v != float64(2) {
			t.Fatalf("next mismatch: %v", v)
		}
	})

	t.Run("adopts a mutable replacement payload rather than copying it", func(t *testing.T) {
		batch := OpList{OpReplace{Value: tdeltaObj("value", float64(0))}}
		first, _ := Apply[*jsonx.Object](nil, batch)
		second, _ := Apply[*jsonx.Object](nil, batch)
		first.Set("value", float64(1))
		if v, _ := second.Get("value"); v != float64(1) {
			t.Fatalf("expected mutable replacement alias in Apply, got %v", v)
		}
	})
}

func TestPathAndPrototypeSafety(t *testing.T) {
	t.Run("rejects constructor walks and forbidden interned paths", func(t *testing.T) {
		op, err := UnmarshalOp([]byte(`["s",["constructor","prototype","gadget"],true]`))
		if err != nil {
			t.Fatalf("UnmarshalOp failed: %v", err)
		}
		if _, err := Apply(tdeltaObj(), OpList{op}); err == nil {
			t.Fatalf("expected error")
		}
		wire := WireOpList{
			WireOpDefine{Id: 0, Path: Path{SegString("__proto__"), SegString("w")}},
			WireOpSet{Ref: PathRef{Id: 0, IsId: true}, Value: true},
		}
		if _, err := NewDecoder().Decode(wire); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("allows reserved names inside values without prototype pollution", func(t *testing.T) {
		val, err := jsonx.Parse([]byte(`{"__proto__":{"z":1}}`))
		if err != nil {
			t.Fatalf("Parse failed: %v", err)
		}
		out, err := Apply(tdeltaObj(), OpList{OpSet{Path: Path{SegString("value")}, Value: val}})
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		v, _ := out.Get("value")
		if _, ok := v.(*jsonx.Object).Get("__proto__"); !ok {
			t.Fatalf("expected __proto__ data property preserved")
		}
	})
}

func TestArrayIndexSafety(t *testing.T) {
	t.Run("writes an existing index and appends exactly one past the end", func(t *testing.T) {
		got1, err := Apply(tdeltaObj("values", []any{float64(1), float64(2), float64(3)}), OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(1)}, Value: float64(9)},
		})
		if err != nil || tdeltaMustJson(got1) != `{"values":[1,9,3]}` {
			t.Fatalf("got=%s err=%v", tdeltaMustJson(got1), err)
		}
		got2, err := Apply(tdeltaObj("values", []any{float64(1), float64(2), float64(3)}), OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(3)}, Value: float64(9)},
		})
		if err != nil || tdeltaMustJson(got2) != `{"values":[1,2,3,9]}` {
			t.Fatalf("got=%s err=%v", tdeltaMustJson(got2), err)
		}
	})

	t.Run("rejects gaps, huge indices, and string-spelled indices", func(t *testing.T) {
		if _, err := Apply(tdeltaObj("values", []any{float64(1), float64(2), float64(3)}), OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(5)}, Value: float64(9)},
		}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := Apply(tdeltaObj("values", []any{}), OpList{
			OpSet{Path: Path{SegString("values"), SegNumber(4294967290)}, Value: float64(1)},
		}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := Apply(tdeltaObj("values", []any{float64(1)}), OpList{
			OpSet{Path: Path{SegString("values"), SegString("0")}, Value: float64(9)},
		}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := Apply(tdeltaObj("values", []any{"a"}), OpList{
			OpAppend{Path: Path{SegString("values"), SegString("0")}, Text: "b"},
		}); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("allows explicit growth values and rejects deletion past the end", func(t *testing.T) {
		got, err := Apply(tdeltaObj("values", []any{float64(1)}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 0, Items: []JsonValue{nil, nil, float64(9)}},
		})
		if err != nil || tdeltaMustJson(got) != `{"values":[1,null,null,9]}` {
			t.Fatalf("got=%s err=%v", tdeltaMustJson(got), err)
		}
		if _, err := Apply(tdeltaObj("values", []any{float64(1)}), OpList{
			OpDelete{Path: Path{SegString("values"), SegNumber(1)}},
		}); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("applies large splice payloads without overflow", func(t *testing.T) {
		items := make([]JsonValue, 300000)
		got, err := Apply(tdeltaObj("values", []any{}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 0, Items: items},
		})
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		vals, _ := got.Get("values")
		if len(vals.([]any)) != len(items) {
			t.Fatalf("expected %d items, got %d", len(items), len(vals.([]any)))
		}
	})
}

func TestOperationStructureSafety(t *testing.T) {
	t.Run("rejects invalid append and truncation operations", func(t *testing.T) {
		if _, err := Apply(tdeltaObj("value", float64(1)), OpList{OpAppend{Path: Path{SegString("missing")}, Text: "x"}}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := Apply(tdeltaObj("value", float64(1)), OpList{OpAppend{Path: Path{SegString("value")}, Text: "x"}}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := Apply(tdeltaObj("value", "abc"), OpList{OpTruncate{Path: Path{SegString("value")}, Count: -1}}); err == nil {
			t.Fatalf("expected error")
		}
		if _, err := NewDecoder().Decode(WireOpList{WireOpTruncate{Ref: PathRef{Path: Path{SegString("value")}}, Count: -1}}); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("clamps splice removal past the end", func(t *testing.T) {
		got, err := Apply(tdeltaObj("values", []any{float64(1), float64(2)}), OpList{
			OpSplice{Path: Path{SegString("values")}, Index: 0, Remove: 1_000_000_000, Items: []JsonValue{}},
		})
		if err != nil || tdeltaMustJson(got) != `{"values":[]}` {
			t.Fatalf("got=%s err=%v", tdeltaMustJson(got), err)
		}
	})
}

func TestOperationAssertions(t *testing.T) {
	validOps := []any{
		[]any{"r", tdeltaObj("value", float64(1))},
		[]any{"s", []any{"value"}, float64(1)},
		[]any{"d", []any{"value"}},
		[]any{"a", []any{"value"}, "x"},
		[]any{"t", []any{"value"}, float64(2)},
		[]any{"p", []any{"value"}, float64(0), float64(0), []any{}},
		[]any{"m", []any{"value"}, []any{float64(0)}},
	}
	for _, op := range validOps {
		if _, err := AssertValidOp(op); err != nil {
			t.Fatalf("AssertValidOp(%v) failed: %v", op, err)
		}
	}
	wireOnlyOps := []any{
		[]any{"s", float64(1)},
		[]any{"d"},
		[]any{"a", "x"},
		[]any{"t", float64(2)},
		[]any{"p", float64(0), float64(0), []any{}},
		[]any{"#", float64(0), []any{"value"}},
		[]any{"s", float64(0), float64(1)},
	}
	for _, op := range wireOnlyOps {
		if _, err := AssertValidOp(op); err == nil {
			t.Fatalf("expected AssertValidOp error for %v", op)
		}
		if _, err := AssertValidWireOp(op); err != nil {
			t.Fatalf("AssertValidWireOp(%v) failed: %v", op, err)
		}
	}
}
