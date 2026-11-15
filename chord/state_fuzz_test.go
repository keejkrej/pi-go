// Ported from packages/chord/test/state-fuzz.test.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"slices"
	"testing"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsfObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tsfClone(v any) any {
	c, err := CopyJson(v, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func tsfMustJson(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type tsfSliceKey struct {
	ptr *any
	len int
}

func tsfExpectAliasFree(t *testing.T, v any) {
	t.Helper()
	seenObj := make(map[*jsonx.Object]string)
	seenSlice := make(map[tsfSliceKey]string)
	var visit func(cur any, path string)
	visit = func(cur any, path string) {
		switch x := cur.(type) {
		case *jsonx.Object:
			if prev, ok := seenObj[x]; ok {
				t.Fatalf("container at %s aliases %s", path, prev)
			}
			seenObj[x] = path
			for k, val := range x.All() {
				visit(val, fmt.Sprintf("%s.%s", path, k))
			}
		case []any:
			if len(x) > 0 {
				k := tsfSliceKey{ptr: unsafe.SliceData(x), len: len(x)}
				if prev, ok := seenSlice[k]; ok {
					t.Fatalf("slice at %s aliases %s", path, prev)
				}
				seenSlice[k] = path
			}
			for i, elem := range x {
				visit(elem, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	visit(v, "$root")
}

func tsfNewRng(seed uint32) func() float64 {
	return func() float64 {
		seed += 0x6d2b79f5
		v := uint32(int32(seed^(seed>>15)) * int32(1|seed))
		v = (v + uint32(int32(v^(v>>7))*int32(61|v))) ^ v
		return float64(v^(v>>14)) / 4294967296.0
	}
}

func tsfMutate(doc *jsonx.Object, choice int, value int) {
	item := func() *jsonx.Object {
		return tsfObj("id", float64(value), "text", fmt.Sprintf("item-%d", value), "score", float64(value%7))
	}
	textAny, _ := doc.Get("text")
	text := textAny.(string)
	itemsAny, _ := doc.Get("items")
	items := itemsAny.([]any)
	metaAny, _ := doc.Get("meta")
	meta := metaAny.(*jsonx.Object)

	switch choice {
	case 0:
		doc.Set("text", fmt.Sprintf("%s-%d", text, value))
	case 1:
		cut := 2
		if len(text) < cut {
			cut = len(text)
		}
		doc.Set("text", fmt.Sprintf("%s%d", text[cut:], value))
	case 2:
		doc.Set("items", append(items, item()))
	case 3:
		doc.Set("items", append([]any{item()}, items...))
	case 4:
		if len(items) > 0 {
			doc.Set("items", items[1:])
		}
	case 5:
		if len(items) > 0 {
			doc.Set("items", items[:len(items)-1])
		}
	case 6:
		idx := 0
		delCount := 0
		if len(items) > 0 {
			idx = value % (len(items) + 1)
			delCount = value % 2
		}
		end := idx + delCount
		if end > len(items) {
			end = len(items)
		}
		doc.Set("items", slices.Replace(items, idx, end, any(item())))
	case 7:
		slices.Reverse(items)
		doc.Set("items", items)
	case 8:
		slices.SortFunc(items, func(a, b any) int {
			ia, _ := a.(*jsonx.Object).Get("id")
			ib, _ := b.(*jsonx.Object).Get("id")
			fa, fb := ia.(float64), ib.(float64)
			if fa < fb {
				return -1
			}
			if fa > fb {
				return 1
			}
			return 0
		})
		doc.Set("items", items)
	case 9:
		if len(items) > 0 {
			items[value%len(items)].(*jsonx.Object).Set("score", float64(value))
		}
	case 10:
		rev, _ := meta.Get("revision")
		meta.Set("revision", rev.(float64)+1)
		meta.Set("label", fmt.Sprintf("revision-%d", value))
	case 11:
		meta.Delete("label")
	case 12:
		if len(items) > 1 {
			items[1] = tsfClone(items[0])
		}
	default:
		limit := 2
		if len(items) < limit {
			limit = len(items)
		}
		for idx := 0; idx < limit; idx++ {
			items[idx] = item()
		}
	}
}

func TestStateFuzz(t *testing.T) {
	for seed := 1; seed <= 100; seed++ {
		rng := tsfNewRng(uint32(seed))
		initItems := make([]any, 4)
		for id := 0; id < 4; id++ {
			initItems[id] = tsfObj("id", float64(id), "text", fmt.Sprintf("item-%d", id), "score", float64(0))
		}
		initial := tsfObj("items", initItems, "text", "start", "meta", tsfObj("revision", float64(0)))
		tracker := Track(initial)
		expected := tsfClone(initial).(*jsonx.Object)
		replica := tsfClone(tracker.Value()).(*jsonx.Object)

		for step := 0; step < 100; step++ {
			choice := int(rng() * 14)
			value := seed*1000 + step
			baseRoot := tracker.Value()
			baseSnap := tsfMustJson(baseRoot)

			tsfMutate(expected, choice, value)
			change := tracker.BeginChange()
			draft, err := change.State()
			if err != nil {
				t.Fatalf("seed %d step %d State failed: %v", seed, step, err)
			}
			tsfMutate(draft.Value, choice, value)
			prepared, err := change.Prepare()
			if err != nil {
				t.Fatalf("seed %d step %d Prepare failed: %v", seed, step, err)
			}
			if tsfMustJson(baseRoot) != baseSnap {
				t.Fatalf("prepare base mutated seed %d step %d choice %d", seed, step, choice)
			}
			if prepared.Base() != baseRoot {
				t.Fatalf("prepared.Base mismatch seed %d step %d", seed, step)
			}
			opsBytes, err := jsonx.Marshal(OpList(prepared.Ops()))
			if err != nil {
				t.Fatalf("marshal ops failed: %v", err)
			}
			rawOps, err := jsonx.Parse(opsBytes)
			if err != nil {
				t.Fatalf("parse ops failed: %v", err)
			}
			var decodedOps OpList
			for _, rawOp := range rawOps.([]any) {
				op, err := DecodeOp(rawOp)
				if err != nil {
					t.Fatalf("DecodeOp failed: %v", err)
				}
				decodedOps = append(decodedOps, op)
			}
			replica, err = ApplyImmutable(replica, decodedOps)
			if err != nil {
				t.Fatalf("ApplyImmutable replica failed seed %d step %d choice %d: %v", seed, step, choice, err)
			}
			if tsfMustJson(replica) != tsfMustJson(prepared.Value()) {
				t.Fatalf("replay mismatch seed %d step %d choice %d\ngot:  %s\nwant: %s", seed, step, choice, tsfMustJson(replica), tsfMustJson(prepared.Value()))
			}
			if err := tracker.Adopt(prepared); err != nil {
				t.Fatalf("Adopt failed seed %d step %d: %v", seed, step, err)
			}
			if tsfMustJson(baseRoot) != baseSnap {
				t.Fatalf("adopt base mutated seed %d step %d", seed, step)
			}
			if tracker.Value() != prepared.Value() {
				t.Fatalf("tracker.Value() pointer mismatch seed %d step %d", seed, step)
			}
			wantJSON := tsfMustJson(expected)
			if tsfMustJson(tracker.Value()) != wantJSON {
				t.Fatalf("state mismatch seed %d step %d choice %d\ngot:  %s\nwant: %s", seed, step, choice, tsfMustJson(tracker.Value()), wantJSON)
			}
			if tsfMustJson(replica) != wantJSON {
				t.Fatalf("replica mismatch seed %d step %d choice %d", seed, step, choice)
			}
			tsfExpectAliasFree(t, tracker.Value())
		}
	}
}
