// Ported from packages/chord/test/delta-apply-immutable.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tdaiObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tdaiMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func tdaiClone(v any) any {
	c, err := CopyJson(v, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func TestCheckedImmutableOperationApplication(t *testing.T) {
	t.Run("copies each touched container once while preserving input and payload ownership", func(t *testing.T) {
		shared := tdaiObj("nested", tdaiObj("value", float64(1)))
		untouched := tdaiObj("value", float64(9))
		rowPayload := tdaiObj("id", float64(4), "label", "placed")
		stable := tdaiObj("value", float64(7))
		branch := tdaiObj("value", float64(1))
		base := tdaiObj(
			"text", "abcdef",
			"stable", stable,
			"branch", branch,
			"copy", nil,
			"placed", nil,
			"untouched", nil,
			"left", nil,
			"right", nil,
			"meta", tdaiObj("count", float64(0), "obsolete", true),
			"rows", []any{
				tdaiObj("id", float64(1), "label", "one"),
				tdaiObj("id", float64(2), "label", "two"),
				tdaiObj("id", float64(3), "label", "three"),
			},
		)
		ops := OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 2},
			OpAppend{Path: Path{SegString("text")}, Text: "!"},
			OpSet{Path: Path{SegString("meta"), SegString("count")}, Value: float64(1)},
			OpSet{Path: Path{SegString("meta"), SegString("count")}, Value: float64(2)},
			OpDelete{Path: Path{SegString("meta"), SegString("obsolete")}},
			OpSet{Path: Path{SegString("copy")}, Value: branch},
			OpSet{Path: Path{SegString("copy"), SegString("value")}, Value: float64(2)},
			OpSet{Path: Path{SegString("placed")}, Value: shared},
			OpSet{Path: Path{SegString("placed"), SegString("nested"), SegString("value")}, Value: float64(2)},
			OpSet{Path: Path{SegString("untouched")}, Value: untouched},
			OpSet{Path: Path{SegString("left")}, Value: shared},
			OpSet{Path: Path{SegString("right")}, Value: shared},
			OpSet{Path: Path{SegString("left"), SegString("nested"), SegString("value")}, Value: float64(3)},
			OpSplice{Path: Path{SegString("rows")}, Index: 1, Remove: 1, Items: []JsonValue{rowPayload}},
			OpSet{Path: Path{SegString("rows"), SegNumber(1), SegString("label")}, Value: "edited"},
			OpMove{Path: Path{SegString("rows")}, Permutation: []int{1, 0, 2}},
			OpSet{Path: Path{SegString("rows"), SegNumber(0), SegString("label")}, Value: "moved"},
		}

		result, err := ApplyImmutable(base, ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		mutableClone := tdaiClone(base).(*jsonx.Object)
		opsClone := OpList{
			OpTruncate{Path: Path{SegString("text")}, Count: 2},
			OpAppend{Path: Path{SegString("text")}, Text: "!"},
			OpSet{Path: Path{SegString("meta"), SegString("count")}, Value: float64(1)},
			OpSet{Path: Path{SegString("meta"), SegString("count")}, Value: float64(2)},
			OpDelete{Path: Path{SegString("meta"), SegString("obsolete")}},
			OpSet{Path: Path{SegString("copy")}, Value: tdaiClone(branch)},
			OpSet{Path: Path{SegString("copy"), SegString("value")}, Value: float64(2)},
			OpSet{Path: Path{SegString("placed")}, Value: tdaiClone(shared)},
			OpSet{Path: Path{SegString("placed"), SegString("nested"), SegString("value")}, Value: float64(2)},
			OpSet{Path: Path{SegString("untouched")}, Value: tdaiClone(untouched)},
			OpSet{Path: Path{SegString("left")}, Value: tdaiClone(shared)},
			OpSet{Path: Path{SegString("right")}, Value: tdaiClone(shared)},
			OpSet{Path: Path{SegString("left"), SegString("nested"), SegString("value")}, Value: float64(3)},
			OpSplice{Path: Path{SegString("rows")}, Index: 1, Remove: 1, Items: []JsonValue{tdaiClone(rowPayload)}},
			OpSet{Path: Path{SegString("rows"), SegNumber(1), SegString("label")}, Value: "edited"},
			OpMove{Path: Path{SegString("rows")}, Permutation: []int{1, 0, 2}},
			OpSet{Path: Path{SegString("rows"), SegNumber(0), SegString("label")}, Value: "moved"},
		}
		mutableResult, err := Apply(mutableClone, opsClone)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if tdaiMustJson(result) != tdaiMustJson(mutableResult) {
			t.Fatalf("mismatch:\n got: %s\nwant: %s", tdaiMustJson(result), tdaiMustJson(mutableResult))
		}
		if gotText, _ := result.Get("text"); gotText != "cdef!" {
			t.Fatalf("text=%v", gotText)
		}
		if gotStable, _ := result.Get("stable"); gotStable != stable {
			t.Fatalf("expected stable shared")
		}
		if gotUntouched, _ := result.Get("untouched"); gotUntouched != untouched {
			t.Fatalf("expected untouched shared")
		}
		if gotCopy, _ := result.Get("copy"); gotCopy == branch || tdaiMustJson(gotCopy) != `{"value":2}` {
			t.Fatalf("copy=%v", gotCopy)
		}
		if gotPlaced, _ := result.Get("placed"); gotPlaced == shared || tdaiMustJson(gotPlaced) != `{"nested":{"value":2}}` {
			t.Fatalf("placed=%v", gotPlaced)
		}
		if gotLeft, _ := result.Get("left"); gotLeft == shared {
			t.Fatalf("left must not alias shared")
		}
		if gotRight, _ := result.Get("right"); gotRight != shared {
			t.Fatalf("right must equal shared")
		}
		rowsVal, _ := result.Get("rows")
		rowsSlice := rowsVal.([]any)
		if rowsSlice[0] == rowPayload || tdaiMustJson(rowsSlice[0]) != `{"id":4,"label":"moved"}` {
			t.Fatalf("row0=%s", tdaiMustJson(rowsSlice[0]))
		}
		if v, _ := branch.Get("value"); v != float64(1) {
			t.Fatalf("branch mutated")
		}
		if nested, _ := shared.Get("nested"); nested.(*jsonx.Object).Keys()[0] != "value" || tdaiMustJson(nested) != `{"value":1}` {
			t.Fatalf("shared mutated")
		}
		if v, _ := rowPayload.Get("label"); v != "placed" {
			t.Fatalf("rowPayload mutated")
		}
	})

	t.Run("protects root replacement payloads before later object and array edits", func(t *testing.T) {
		replacement := tdaiObj("nested", tdaiObj("value", float64(1)), "values", []any{float64(1), float64(2), float64(3)})
		result, err := ApplyImmutableBatches[*jsonx.Object](nil, func(yield func(OpList) bool) {
			if !yield(OpList{OpReplace{Value: replacement}}) {
				return
			}
			if !yield(OpList{OpSet{Path: Path{SegString("nested"), SegString("value")}, Value: float64(2)}}) {
				return
			}
			yield(OpList{
				OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{float64(4), float64(5)}},
				OpMove{Path: Path{SegString("values")}, Permutation: []int{3, 0, 1, 2}},
			})
		})
		if err != nil {
			t.Fatalf("ApplyImmutableBatches failed: %v", err)
		}
		if tdaiMustJson(result) != `{"nested":{"value":2},"values":[3,1,4,5]}` {
			t.Fatalf("got %s", tdaiMustJson(result))
		}
		if tdaiMustJson(replacement) != `{"nested":{"value":1},"values":[1,2,3]}` {
			t.Fatalf("replacement mutated")
		}

		array := []any{float64(1), float64(2), float64(3)}
		arrayResult, err := ApplyImmutable(array, OpList{
			OpSplice{Path: Path{}, Index: 1, Remove: 1, Items: []JsonValue{float64(4), float64(5)}},
			OpMove{Path: Path{}, Permutation: []int{3, 0, 1, 2}},
			OpDelete{Path: Path{SegNumber(1)}},
		})
		if err != nil {
			t.Fatalf("ApplyImmutable array failed: %v", err)
		}
		if tdaiMustJson(arrayResult) != `[3,4,5]` || tdaiMustJson(array) != `[1,2,3]` {
			t.Fatalf("array mismatch: got=%s orig=%s", tdaiMustJson(arrayResult), tdaiMustJson(array))
		}
	})

	t.Run("shares one private copy-on-write scope across batch partitions", func(t *testing.T) {
		base := tdaiObj(
			"text", "abcdef",
			"meta", tdaiObj("count", float64(0)),
			"values", []any{
				tdaiObj("id", float64(1), "value", float64(1)),
				tdaiObj("id", float64(2), "value", float64(2)),
				tdaiObj("id", float64(3), "value", float64(3)),
			},
		)
		batches := []OpList{
			{
				OpSet{Path: Path{SegString("meta"), SegString("count")}, Value: float64(1)},
				OpSplice{Path: Path{SegString("values")}, Index: 1, Remove: 1, Items: []JsonValue{tdaiObj("id", float64(4), "value", float64(4))}},
			},
			{},
			{
				OpMove{Path: Path{SegString("values")}, Permutation: []int{2, 0, 1}},
				OpSet{Path: Path{SegString("values"), SegNumber(2), SegString("value")}, Value: float64(40)},
			},
			{
				OpTruncate{Path: Path{SegString("text")}, Count: 2},
				OpAppend{Path: Path{SegString("text")}, Text: "!"},
			},
		}

		intermediate, err := ApplyImmutable(base, batches[0])
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		intermediateSnapshot := tdaiMustJson(intermediate)
		sequential := intermediate
		for i := 1; i < len(batches); i++ {
			sequential, err = ApplyImmutable(sequential, batches[i])
			if err != nil {
				t.Fatalf("batch %d failed: %v", i, err)
			}
		}
		streamed, err := ApplyImmutableBatches(base, func(yield func(OpList) bool) {
			for _, b := range batches {
				if !yield(b) {
					return
				}
			}
		})
		if err != nil {
			t.Fatalf("streamed failed: %v", err)
		}
		var flattenedOps OpList
		for _, b := range batches {
			flattenedOps = append(flattenedOps, b...)
		}
		flattened, err := ApplyImmutable(base, flattenedOps)
		if err != nil {
			t.Fatalf("flattened failed: %v", err)
		}

		if tdaiMustJson(streamed) != tdaiMustJson(sequential) || tdaiMustJson(streamed) != tdaiMustJson(flattened) {
			t.Fatalf("mismatch across batch partitions")
		}
		if tdaiMustJson(intermediate) != intermediateSnapshot {
			t.Fatalf("intermediate mutated")
		}
	})

	t.Run("handles tracker-produced batches across arbitrary revision boundaries", func(t *testing.T) {
		initValues := make([]any, 8)
		for i := range initValues {
			initValues[i] = tdaiObj("id", float64(i), "score", float64(0))
		}
		initial := tdaiObj("text", "start", "values", initValues, "revision", float64(0))
		tracker := Track(initial)
		var batches []OpList

		for rev := 1; rev <= 40; rev++ {
			change := tracker.BeginChange()
			draft, err := change.State()
			if err != nil {
				t.Fatalf("State failed at rev %d: %v", rev, err)
			}
			draft.Value.Set("revision", float64(rev))
			curText, _ := draft.Value.Get("text")
			draft.Value.Set("text", fmt.Sprintf("%s%d", curText.(string)[1:], rev))
			curValsAny, _ := draft.Value.Get("values")
			vals := curValsAny.([]any)
			switch rev % 5 {
			case 0:
				for i, j := 0, len(vals)-1; i < j; i, j = i+1, j-1 {
					vals[i], vals[j] = vals[j], vals[i]
				}
			case 1:
				vals = append(vals, tdaiObj("id", float64(100+rev), "score", float64(rev)))
			case 2:
				vals = vals[1:]
			case 3:
				vals[rev%len(vals)].(*jsonx.Object).Set("score", float64(rev))
			default:
				newItem := tdaiObj("id", float64(200+rev), "score", float64(rev))
				nextVals := make([]any, 0, len(vals))
				nextVals = append(nextVals, vals[:1]...)
				nextVals = append(nextVals, newItem)
				nextVals = append(nextVals, vals[2:]...)
				vals = nextVals
			}
			draft.Value.Set("values", vals)
			prepared, err := change.Prepare()
			if err != nil {
				t.Fatalf("Prepare failed at rev %d: %v", rev, err)
			}
			batches = append(batches, prepared.Ops())
			if err := tracker.Adopt(prepared); err != nil {
				t.Fatalf("Adopt failed at rev %d: %v", rev, err)
			}
		}

		replayed, err := ApplyImmutableBatches(initial, func(yield func(OpList) bool) {
			for _, b := range batches {
				if !yield(b) {
					return
				}
			}
		})
		if err != nil {
			t.Fatalf("ApplyImmutableBatches failed: %v", err)
		}
		if tdaiMustJson(replayed) != tdaiMustJson(tracker.Value()) {
			t.Fatalf("replay mismatch:\n got: %s\nwant: %s", tdaiMustJson(replayed), tdaiMustJson(tracker.Value()))
		}
	})

	t.Run("does not expose partial application when validation fails", func(t *testing.T) {
		base := tdaiObj("nested", tdaiObj("value", float64(1)))
		_, err := ApplyImmutableBatches(base, func(yield func(OpList) bool) {
			if !yield(OpList{OpSet{Path: Path{SegString("nested"), SegString("value")}, Value: float64(2)}}) {
				return
			}
			yield(OpList{OpSet{Path: Path{SegString("constructor"), SegString("prototype"), SegString("polluted")}, Value: true}})
		})
		if err == nil {
			t.Fatalf("expected error")
		}
		nested, _ := base.Get("nested")
		if v, _ := nested.(*jsonx.Object).Get("value"); v != float64(1) {
			t.Fatalf("base mutated")
		}

		advancedPastInvalid := false
		_, err = ApplyImmutableBatches(base, func(yield func(OpList) bool) {
			if !yield(OpList{OpSet{Path: Path{SegString("nested"), SegString("value")}, Value: float64(4)}}) {
				return
			}
			if !yield(OpList{OpSet{Path: Path{SegString("__proto__"), SegString("polluted")}, Value: true}}) {
				return
			}
			advancedPastInvalid = true
		})
		if _, ok := err.(*UnsafePathError); !ok {
			t.Fatalf("expected *UnsafePathError, got %T (%v)", err, err)
		}
		if advancedPastInvalid {
			t.Fatalf("expected iterator to stop on invalid batch")
		}

		if _, err := ApplyImmutable(tdaiObj("values", []any{}), OpList{
			OpSet{Path: Path{SegString("values"), SegString("missing"), SegString("value")}, Value: float64(1)},
		}); err == nil {
			t.Fatalf("expected PathError")
		} else if _, ok := err.(*PathError); !ok {
			t.Fatalf("expected *PathError, got %T", err)
		}

		if _, err := ApplyImmutable(tdaiObj("values", []any{tdaiObj()}), OpList{
			OpSet{Path: Path{SegString("values"), SegString("0"), SegString("value")}, Value: float64(1)},
		}); err == nil {
			t.Fatalf("expected UnsafePathError")
		} else if _, ok := err.(*UnsafePathError); !ok {
			t.Fatalf("expected *UnsafePathError, got %T", err)
		}
	})

	t.Run("allows one immutable batch to fan out without mutating shared payloads", func(t *testing.T) {
		payload := tdaiObj("nested", tdaiObj("value", float64(1)))
		ops := OpList{
			OpSet{Path: Path{SegString("placed")}, Value: payload},
			OpSet{Path: Path{SegString("placed"), SegString("nested"), SegString("value")}, Value: float64(2)},
		}
		base := tdaiObj("placed", nil)
		first, err := ApplyImmutable(base, ops)
		if err != nil {
			t.Fatalf("first failed: %v", err)
		}
		second, err := ApplyImmutable(base, ops)
		if err != nil {
			t.Fatalf("second failed: %v", err)
		}
		if tdaiMustJson(first) != tdaiMustJson(second) || first == second {
			t.Fatalf("fanout mismatch")
		}
		p1, _ := first.Get("placed")
		p2, _ := second.Get("placed")
		if p1 == p2 {
			t.Fatalf("expected independent placed copies")
		}
		nested, _ := payload.Get("nested")
		if v, _ := nested.(*jsonx.Object).Get("value"); v != float64(1) {
			t.Fatalf("payload mutated")
		}
	})

	t.Run("copies wide object once rather than once per repeated write", func(t *testing.T) {
		const width = 20000
		base := jsonx.NewObject()
		for i := 0; i < width; i++ {
			base.Set(fmt.Sprintf("field%d", i), float64(i))
		}
		ops := make(OpList, 1000)
		for i := range ops {
			ops[i] = OpSet{Path: Path{SegString(fmt.Sprintf("field%d", i))}, Value: float64(-i)}
		}
		result, err := ApplyImmutable(base, ops)
		if err != nil {
			t.Fatalf("ApplyImmutable failed: %v", err)
		}
		if v, _ := result.Get("field999"); v != float64(-999) {
			t.Fatalf("got %v", v)
		}
		if v, _ := base.Get("field999"); v != float64(999) {
			t.Fatalf("base mutated: %v", v)
		}
		if !strings.Contains(tdaiMustJson(result.Keys()[:2]), "field0") {
			t.Fatalf("keys unexpected")
		}
	})
}
