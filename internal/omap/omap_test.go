package omap_test

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/keejkrej/pi-go/internal/omap"
)

func collect[K comparable, V any](m *omap.Map[K, V]) []K {
	var out []K
	for k := range m.All() {
		out = append(out, k)
	}
	return out
}

func TestMap_InsertionOrderAndUpdates(t *testing.T) {
	m := omap.NewMap[string, int]()
	m.Set("b", 1)
	m.Set("a", 2)
	m.Set("c", 3)
	m.Set("b", 4) // existing key keeps its position
	if diff := cmp.Diff([]string{"b", "a", "c"}, m.Keys()); diff != "" {
		t.Fatalf("keys (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{4, 2, 3}, m.Values()); diff != "" {
		t.Fatalf("values (-want +got):\n%s", diff)
	}
	if v, ok := m.Get("b"); !ok || v != 4 {
		t.Fatalf("Get(b) = %d, %v", v, ok)
	}
	if _, ok := m.Get("zz"); ok {
		t.Fatal("Get(zz) found")
	}
	if !m.Delete("b") || m.Delete("b") {
		t.Fatal("Delete(b) should succeed once")
	}
	m.Set("b", 5) // re-added key goes to the end
	if diff := cmp.Diff([]string{"a", "c", "b"}, collect(m)); diff != "" {
		t.Fatalf("order after re-add (-want +got):\n%s", diff)
	}
	if m.Len() != 3 || !m.Has("a") || m.Has("zz") {
		t.Fatalf("Len/Has wrong: %d", m.Len())
	}
	clone := m.Clone()
	m.Clear()
	if m.Len() != 0 || len(m.Keys()) != 0 {
		t.Fatal("Clear left entries")
	}
	if diff := cmp.Diff([]string{"a", "c", "b"}, clone.Keys()); diff != "" {
		t.Fatalf("clone (-want +got):\n%s", diff)
	}
}

func TestMap_ZeroValueAndNil(t *testing.T) {
	var m omap.Map[string, int]
	m.Set("x", 1)
	if v, ok := m.Get("x"); !ok || v != 1 {
		t.Fatal("zero-value map unusable")
	}
	var nilMap *omap.Map[string, int]
	if nilMap.Len() != 0 || nilMap.Has("x") || nilMap.Delete("x") || nilMap.Keys() != nil {
		t.Fatal("nil map reads should behave as empty")
	}
	for range nilMap.All() {
		t.Fatal("nil map iterated")
	}
	b, err := json.Marshal(nilMap)
	if err != nil || string(b) != "null" {
		t.Fatalf("nil map JSON = %s, %v", b, err)
	}
}

func TestMap_IterationSemanticsMatchJSMap(t *testing.T) {
	// for (const [k] of m) { if (k === "a") { m.delete("b"); m.set("d", 4); } }
	m := omap.NewMap[string, int]()
	for i, k := range []string{"a", "b", "c"} {
		m.Set(k, i)
	}
	var visited []string
	for k := range m.All() {
		visited = append(visited, k)
		if k == "a" {
			m.Delete("b")
			m.Set("d", 4)
		}
		if k == "c" {
			m.Delete("c") // deleting the current entry is safe
		}
	}
	if diff := cmp.Diff([]string{"a", "c", "d"}, visited); diff != "" {
		t.Fatalf("visited (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a", "d"}, m.Keys()); diff != "" {
		t.Fatalf("keys (-want +got):\n%s", diff)
	}

	// Delete then re-add during iteration: the key is visited again at the end.
	m = omap.NewMap[string, int]()
	m.Set("x", 1)
	m.Set("y", 2)
	visited = nil
	for k := range m.All() {
		visited = append(visited, k)
		if k == "x" && len(visited) == 1 {
			m.Delete("x")
			m.Set("x", 3)
		}
	}
	if diff := cmp.Diff([]string{"x", "y", "x"}, visited); diff != "" {
		t.Fatalf("visited after re-add (-want +got):\n%s", diff)
	}

	// clear() during iteration ends it unless entries are added afterwards.
	m = omap.NewMap[string, int]()
	m.Set("p", 1)
	m.Set("q", 2)
	visited = nil
	for k := range m.All() {
		visited = append(visited, k)
		if k == "p" {
			m.Clear()
			m.Set("r", 3)
		}
	}
	if diff := cmp.Diff([]string{"p", "r"}, visited); diff != "" {
		t.Fatalf("visited after clear (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"r"}, m.Keys()); diff != "" {
		t.Fatalf("keys after clear (-want +got):\n%s", diff)
	}

	// Breaking out of the loop restores normal compaction.
	for k := range m.All() {
		_ = k
		break
	}
}

func TestMap_CompactionKeepsOrder(t *testing.T) {
	m := omap.NewMap[int, int]()
	for i := range 1000 {
		m.Set(i, i)
	}
	for i := 0; i < 1000; i += 3 {
		m.Delete(i)
	}
	for i := range 10 {
		m.Set(-i-1, i)
	}
	var want []int
	for i := range 1000 {
		if i%3 != 0 {
			want = append(want, i)
		}
	}
	for i := range 10 {
		want = append(want, -i-1)
	}
	if diff := cmp.Diff(want, m.Keys()); diff != "" {
		t.Fatalf("keys (-want +got):\n%s", diff)
	}
	if m.Len() != len(want) {
		t.Fatalf("Len = %d, want %d", m.Len(), len(want))
	}
	for _, k := range want {
		if !m.Has(k) {
			t.Fatalf("missing %d", k)
		}
	}
}

func TestMap_MarshalJSONUsesJSPropertyOrder(t *testing.T) {
	// Node: JSON.stringify(Object.assign({}, {b:1,"4294967295":2,"4294967294":3,"01":4,"1":5,"-1":6}))
	// = {"1":5,"4294967294":3,"b":1,"4294967295":2,"01":4,"-1":6}
	m := omap.NewMap[string, int]()
	for _, kv := range []struct {
		k string
		v int
	}{{"b", 1}, {"4294967295", 2}, {"4294967294", 3}, {"01", 4}, {"1", 5}, {"-1", 6}} {
		m.Set(kv.k, kv.v)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"1":5,"4294967294":3,"b":1,"4294967295":2,"01":4,"-1":6}`; got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
	keys, err := m.ObjectKeys()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1", "4294967294", "b", "4294967295", "01", "-1"}, keys); diff != "" {
		t.Errorf("ObjectKeys (-want +got):\n%s", diff)
	}
	// Iteration stays in insertion order (JS Map semantics).
	if diff := cmp.Diff([]string{"b", "4294967295", "4294967294", "01", "1", "-1"}, m.Keys()); diff != "" {
		t.Errorf("Keys (-want +got):\n%s", diff)
	}
}

func TestMap_MarshalJSONValues(t *testing.T) {
	type item struct {
		Name string   `json:"name"`
		Tags []string `json:"tags,omitzero"`
	}
	m := omap.NewMap[string, any]()
	m.Set("html", "<a & b>")
	m.Set("item", item{Name: "x"})
	inner := omap.NewMap[string, float64]()
	inner.Set("z", 1.5)
	inner.Set("a", 1e21)
	m.Set("inner", inner)
	m.Set("list", []any{1.0, "two", nil})
	// MarshalJSON itself does not HTML-escape (JSON.stringify parity). Note that
	// json.Marshal re-escapes Marshaler output; use an Encoder with
	// SetEscapeHTML(false) to keep it.
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"html":"<a & b>","item":{"name":"x"},"inner":{"z":1.5,"a":1e+21},"list":[1,"two",null]}`
	if string(b) != want {
		t.Errorf("JSON = %s\nwant   %s", b, want)
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSuffix(buf.String(), "\n"); got != want {
		t.Errorf("Encoder JSON = %s\nwant   %s", got, want)
	}
	empty, _ := json.Marshal(omap.NewMap[string, int]())
	if string(empty) != "{}" {
		t.Errorf("empty map JSON = %s", empty)
	}
	ints := omap.NewMap[int, string]()
	ints.Set(10, "ten")
	ints.Set(-1, "neg")
	ints.Set(2, "two")
	b, _ = json.Marshal(ints)
	if got := string(b); got != `{"2":"two","10":"ten","-1":"neg"}` {
		t.Errorf("int-key JSON = %s", got)
	}
}

func TestMap_MarshalJSONMatchesJSONStringifySpecials(t *testing.T) {
	// Node 24 JSON.stringify: U+2028/U+2029 are raw, U+0000 is \u0000,
	// -0 is 0, NaN and ±Inf are null. A key whose text is \u2028 stays escaped.
	neg := math.Copysign(0, -1)
	m := omap.NewMap[string, any]()
	m.Set("\u2028", 1)
	m.Set("\u0000", 2)
	m.Set(`\u2028`, 3)
	m.Set("\u2029", 4)
	m.Set("n", math.NaN())
	m.Set("p", math.Inf(1))
	m.Set("m", math.Inf(-1))
	m.Set("z", neg)
	m.Set("pz", &neg)
	m.Set("list", []any{math.NaN(), neg, 1.0})
	m.Set("nest", map[string]any{"q": math.Inf(-1)})
	m.Set("f32", []float32{float32(math.NaN()), 1})
	want := `{"` + "\u2028" + `":1,"\u0000":2,"\\u2028":3,"` + "\u2029" + `":4,"n":null,"p":null,"m":null,"z":0,"pz":0,"list":[null,0,1],"nest":{"q":null},"f32":[null,1]}`
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("JSON = %s\nwant   %s", b, want)
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSuffix(buf.String(), "\n"); got != want {
		t.Errorf("Encoder JSON = %s\nwant   %s", got, want)
	}

	type box struct {
		N float64 `json:"n"`
	}
	withZero := omap.NewMap[string, box]()
	withZero.Set("a", box{N: neg})
	b, err = withZero.MarshalJSON()
	if err != nil || string(b) != `{"a":{"n":0}}` {
		t.Fatalf("struct -0 JSON = %s, %v", b, err)
	}
	withNaN := omap.NewMap[string, box]()
	withNaN.Set("a", box{N: math.NaN()})
	if _, err := withNaN.MarshalJSON(); err == nil {
		t.Fatal("struct NaN field encoded; encoding/json rejects it")
	}
}

func TestMap_UnmarshalJSONOrder(t *testing.T) {
	var m omap.Map[string, int]
	// Object.keys(JSON.parse('{"b":1,"2":2,"a":3,"1":4,"b":5}')) = ["1","2","b","a"], b === 5
	if err := json.Unmarshal([]byte(`{"b":1,"2":2,"a":3,"1":4,"b":5}`), &m); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1", "2", "b", "a"}, m.Keys()); diff != "" {
		t.Fatalf("keys (-want +got):\n%s", diff)
	}
	if v, _ := m.Get("b"); v != 5 {
		t.Fatalf("b = %d, want 5 (last duplicate wins)", v)
	}
	if err := json.Unmarshal([]byte(`[1]`), &m); err == nil {
		t.Fatal("array accepted")
	}
	if err := json.Unmarshal([]byte(`{"a":"x"}`), &m); err == nil {
		t.Fatal("type mismatch accepted")
	}
	var ints omap.Map[int, bool]
	if err := json.Unmarshal([]byte(`{"3":true,"-4":false}`), &ints); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int{3, -4}, ints.Keys()); diff != "" {
		t.Fatalf("int keys (-want +got):\n%s", diff)
	}
	if err := json.Unmarshal([]byte(`{"x":true}`), &ints); err == nil {
		t.Fatal("non-integer key accepted for int map")
	}
	type holder struct {
		M *omap.Map[string, []int] `json:"m"`
	}
	var h holder
	if err := json.Unmarshal([]byte(`{"m":{"k":[1,2]}}`), &h); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.M.Get("k"); !slices.Equal(v, []int{1, 2}) {
		t.Fatalf("nested = %v", v)
	}
	b, _ := json.Marshal(h)
	if string(b) != `{"m":{"k":[1,2]}}` {
		t.Fatalf("round trip = %s", b)
	}
}

func TestSet_Semantics(t *testing.T) {
	s := omap.NewSetOf("b", "a", "b", "c")
	if diff := cmp.Diff([]string{"b", "a", "c"}, s.Values()); diff != "" {
		t.Fatalf("values (-want +got):\n%s", diff)
	}
	s.Add("a")
	if s.Len() != 3 || !s.Has("a") || s.Has("z") {
		t.Fatal("Add/Has/Len wrong")
	}
	var visited []string
	for v := range s.All() {
		visited = append(visited, v)
		if v == "b" {
			s.Delete("a")
			s.Add("d")
		}
	}
	if diff := cmp.Diff([]string{"b", "c", "d"}, visited); diff != "" {
		t.Fatalf("visited (-want +got):\n%s", diff)
	}
	b, err := json.Marshal(s)
	if err != nil || string(b) != `["b","c","d"]` {
		t.Fatalf("JSON = %s, %v", b, err)
	}
	var back omap.Set[string]
	if err := json.Unmarshal([]byte(`["x","y","x"]`), &back); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"x", "y"}, back.Values()); diff != "" {
		t.Fatalf("unmarshal (-want +got):\n%s", diff)
	}
	if err := json.Unmarshal([]byte(`{}`), &back); err == nil {
		t.Fatal("object accepted for Set")
	}
	clone := s.Clone()
	s.Clear()
	if s.Len() != 0 || clone.Len() != 3 {
		t.Fatal("Clear/Clone wrong")
	}
	var nilSet *omap.Set[int]
	if nilSet.Len() != 0 || nilSet.Has(1) || nilSet.Delete(1) || nilSet.Values() != nil {
		t.Fatal("nil set reads should behave as empty")
	}
	var zero omap.Set[int]
	for i := range 5 {
		zero.Add(i * 7 % 5)
	}
	got := []string{}
	for v := range zero.All() {
		got = append(got, strconv.Itoa(v))
	}
	if diff := cmp.Diff([]string{"0", "2", "4", "1", "3"}, got); diff != "" {
		t.Fatalf("zero-value set (-want +got):\n%s", diff)
	}
}
