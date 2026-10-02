package jsonx_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tdEntry struct {
	Type      string              `json:"type"`
	ID        string              `json:"id"`
	ParentID  jsonx.Opt[string]   `json:"parentId"`
	Timestamp string              `json:"timestamp"`
	Message   *tdMessage          `json:"message,omitzero"`
	Data      any                 `json:"data,omitzero"`
	Details   *jsonx.Object       `json:"details,omitzero"`
	Tags      []string            `json:"tags,omitzero"`
	Counts    map[string]int      `json:"counts,omitzero"`
	When      time.Time           `json:"when,omitzero"`
	Raw       json.RawMessage     `json:"raw,omitzero"`
	Label     jsonx.Opt[*tdLabel] `json:"label,omitzero"`
}

type tdMessage struct {
	Role    string  `json:"role"`
	Content []any   `json:"content"`
	Tokens  int     `json:"tokens"`
	Cost    float64 `json:"cost"`
}

type tdLabel struct {
	Name string `json:"name"`
}

func TestDecode_SessionEntryRoundTrip(t *testing.T) {
	line := `{"type":"message","id":"a1","parentId":null,"timestamp":"2024-01-01T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}],"tokens":3,"cost":0.5},"data":{"z":1,"0":[true]},"details":{"b":{"c":1}},"tags":[],"counts":{"x":2},"when":"2024-01-01T00:00:00.123Z","raw":{"k" : 1},"label":{"name":"n"},"unknown":5}`
	v, err := jsonx.ParseString(line)
	if err != nil {
		t.Fatal(err)
	}
	var e tdEntry
	if err := jsonx.Decode(v, &e); err != nil {
		t.Fatal(err)
	}
	if !e.ParentID.IsNull() || e.ParentID.IsZero() {
		t.Fatalf("parentId state: %+v", e.ParentID)
	}
	if e.Message.Tokens != 3 || e.Message.Cost != 0.5 {
		t.Fatalf("message = %+v", e.Message)
	}
	if _, ok := e.Message.Content[0].(*jsonx.Object); !ok {
		t.Fatalf("any content decoded as %T, want *jsonx.Object", e.Message.Content[0])
	}
	if _, ok := e.Data.(*jsonx.Object); !ok {
		t.Fatalf("data decoded as %T", e.Data)
	}
	if e.Tags == nil || len(e.Tags) != 0 {
		t.Fatalf("tags = %#v", e.Tags)
	}
	if !e.When.Equal(time.UnixMilli(1704067200123)) {
		t.Fatalf("when = %v", e.When)
	}
	if string(e.Raw) != `{"k":1}` {
		t.Fatalf("raw = %s", e.Raw)
	}
	if l, ok := e.Label.Get(); !ok || l.Name != "n" {
		t.Fatalf("label = %+v", e.Label)
	}
	// *Object and any targets alias the parsed document like a JS reference.
	doc := v.(*jsonx.Object)
	docDetails, _ := doc.GetObject("details")
	if docDetails != e.Details {
		t.Fatal("details not aliased")
	}
	out, err := jsonx.Stringify(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"message","id":"a1","parentId":null,"timestamp":"2024-01-01T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}],"tokens":3,"cost":0.5},"data":{"0":[true],"z":1},"details":{"b":{"c":1}},"tags":[],"counts":{"x":2},"when":"2024-01-01T00:00:00.123Z","raw":{"k":1},"label":{"name":"n"}}`
	if out != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}

	// Absent Opt stays absent and is omitted with omitzero; plain Opt writes null.
	var e2 tdEntry
	if err := jsonx.Unmarshal([]byte(`{"type":"x","id":"b"}`), &e2); err != nil {
		t.Fatal(err)
	}
	if !e2.ParentID.IsZero() || e2.Label.IsZero() == false {
		t.Fatal("absent Opt not zero")
	}
	if got := jsonx.MustStringify(e2); got != `{"type":"x","id":"b","parentId":null,"timestamp":""}` {
		t.Fatalf("got %s", got)
	}
}

func TestDecode_TypeErrorsMatchEncodingJSON(t *testing.T) {
	var m tdMessage
	err := jsonx.Unmarshal([]byte(`{"role":5,"tokens":1.5,"cost":"x","content":[1]}`), &m)
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v", err)
	}
	if err.Error() != "json: cannot unmarshal number 5 into Go struct field tdMessage.role of type string" {
		t.Fatalf("err = %v", err)
	}
	// Decoding continues after the first error.
	if len(m.Content) != 1 || m.Content[0] != 1.0 {
		t.Fatalf("content = %#v", m.Content)
	}
	var n int
	if err := jsonx.Unmarshal([]byte(`1e2`), &n); err != nil || n != 100 {
		t.Fatalf("1e2 into int: %d %v", n, err)
	}
	if err := jsonx.Unmarshal([]byte(`1.5`), &n); err == nil || err.Error() != "json: cannot unmarshal number 1.5 into Go value of type int" {
		t.Fatalf("1.5 into int: %v", err)
	}
	var u uint8
	if err := jsonx.Unmarshal([]byte(`300`), &u); err == nil {
		t.Fatal("overflow accepted")
	}
	if err := jsonx.Decode(nil, n); err == nil {
		t.Fatal("non-pointer accepted")
	}
	var o *jsonx.Object
	if err := jsonx.Unmarshal([]byte(`[1]`), &o); err == nil || err.Error() != "json: cannot unmarshal array into Go value of type jsonx.Object" {
		t.Fatalf("array into *Object: %v", err)
	}
	if err := jsonx.Unmarshal([]byte(`{"a":`), &o); err == nil || err.Error() != "Unexpected end of JSON input" {
		t.Fatalf("syntax error: %v", err)
	}
}

func TestDecode_FieldMatchingAndNull(t *testing.T) {
	type T struct {
		Name  string            `json:"name"`
		Ptr   *int              `json:"ptr"`
		Slice []int             `json:"slice"`
		Map   map[string]string `json:"map"`
		Any   any               `json:"any"`
		Keep  string            `json:"keep"`
		Plain int
	}
	one := 1
	x := T{Name: "a", Ptr: &one, Slice: []int{1}, Map: map[string]string{"a": "b"}, Any: "x", Keep: "k"}
	err := jsonx.Unmarshal([]byte(`{"NAME":"folded","ptr":null,"slice":null,"map":null,"any":null,"keep":null,"plain":7}`), &x)
	if err != nil {
		t.Fatal(err)
	}
	want := T{Name: "folded", Keep: "k", Plain: 7}
	if diff := cmp.Diff(want, x); diff != "" {
		t.Fatal(diff)
	}
	// Exact match wins over a case-insensitive one.
	type U struct {
		A string `json:"a"`
		B string `json:"A"`
	}
	var y U
	if err := jsonx.Unmarshal([]byte(`{"A":"upper","a":"lower"}`), &y); err != nil || y.A != "lower" || y.B != "upper" {
		t.Fatalf("y = %+v %v", y, err)
	}
}

func TestDecode_CollectionsAndPointers(t *testing.T) {
	var arr [3]int
	arr[2] = 9
	if err := jsonx.Unmarshal([]byte(`[1,2]`), &arr); err != nil || arr != [3]int{1, 2, 0} {
		t.Fatalf("arr = %v %v", arr, err)
	}
	var keys map[int]string
	if err := jsonx.Unmarshal([]byte(`{"10":"a","2":"b"}`), &keys); err != nil || keys[10] != "a" || keys[2] != "b" {
		t.Fatalf("keys = %v %v", keys, err)
	}
	var pp **tdLabel
	if err := jsonx.Unmarshal([]byte(`{"name":"x"}`), &pp); err != nil || (*pp).Name != "x" {
		t.Fatalf("pp = %v", err)
	}
	var b []byte
	if err := jsonx.Unmarshal([]byte(`"aGk="`), &b); err != nil || string(b) != "hi" {
		t.Fatalf("b = %q %v", b, err)
	}
	var anyV any
	if err := jsonx.Unmarshal([]byte(`{"b":1,"a":[1,"x",null]}`), &anyV); err != nil {
		t.Fatal(err)
	}
	if jsonx.MustStringify(anyV) != `{"b":1,"a":[1,"x",null]}` {
		t.Fatalf("any = %s", jsonx.MustStringify(anyV))
	}
	var empty []string
	if err := jsonx.Unmarshal([]byte(`[]`), &empty); err != nil || empty == nil {
		t.Fatalf("empty slice decoded as nil")
	}
	var num json.Number
	if err := jsonx.Unmarshal([]byte(`1.5e3`), &num); err != nil || num != "1500" {
		t.Fatalf("num = %q %v", num, err)
	}
	type Q struct {
		N int  `json:"n,string"`
		B bool `json:"b,string"`
	}
	var q Q
	if err := jsonx.Unmarshal([]byte(`{"n":"12","b":"true"}`), &q); err != nil || q.N != 12 || !q.B {
		t.Fatalf("q = %+v %v", q, err)
	}
	if err := jsonx.Unmarshal([]byte(`{"n":12}`), &q); err == nil || !strings.Contains(err.Error(), "invalid use of ,string struct tag") {
		t.Fatalf("unquoted ,string value: %v", err)
	}
	// Decoding into an existing *Object fills it (it is the Unmarshal target).
	target := jsonx.NewObject()
	if err := jsonx.Unmarshal([]byte(`{"a":1}`), target); err != nil || jsonx.MustStringify(target) != `{"a":1}` {
		t.Fatalf("target = %v %v", target, err)
	}
}

func TestDecode_TypedSourceValues(t *testing.T) {
	var lbl tdLabel
	if err := jsonx.Decode(map[string]any{"name": "from map"}, &lbl); err != nil || lbl.Name != "from map" {
		t.Fatalf("lbl = %+v %v", lbl, err)
	}
	var n float64
	if err := jsonx.Decode(7, &n); err != nil || n != 7 {
		t.Fatalf("n = %v %v", n, err)
	}
}

func TestEncode_TypedToUntyped(t *testing.T) {
	v, err := jsonx.Encode(tdMessage{Role: "user", Content: []any{"x"}, Tokens: 2})
	if err != nil {
		t.Fatal(err)
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		t.Fatalf("Encode returned %T", v)
	}
	if diff := cmp.Diff([]string{"role", "content", "tokens", "cost"}, o.Keys()); diff != "" {
		t.Fatal(diff)
	}
	if n, _ := o.GetNumber("tokens"); n != 2 {
		t.Fatalf("tokens = %v", n)
	}
	if v, err := jsonx.Encode(func() {}); v != nil || err != nil {
		t.Fatalf("Encode(func) = %v %v", v, err)
	}
	if v, _ := jsonx.Encode(math.Copysign(0, -1)); v != 0.0 || math.Signbit(v.(float64)) {
		t.Fatalf("Encode(-0) = %v", v)
	}
}

func TestOpt_States(t *testing.T) {
	var absent jsonx.Opt[int]
	if !absent.IsZero() || absent.IsNull() || absent.IsSome() {
		t.Fatal("absent state")
	}
	if v, ok := absent.Get(); ok || v != 0 {
		t.Fatal("absent Get")
	}
	null := jsonx.Null[int]()
	if null.IsZero() || !null.IsNull() || null.OrElse(5) != 5 {
		t.Fatal("null state")
	}
	some := jsonx.Some(3)
	if v, ok := some.Get(); !ok || v != 3 || some.OrElse(5) != 3 {
		t.Fatal("some state")
	}
	for _, c := range []struct {
		o    jsonx.Opt[int]
		want string
	}{{absent, "null"}, {null, "null"}, {some, "3"}} {
		b, err := c.o.MarshalJSON()
		if err != nil || string(b) != c.want {
			t.Fatalf("MarshalJSON = %s %v", b, err)
		}
	}
	var o jsonx.Opt[string]
	if err := o.UnmarshalJSON([]byte(`"x"`)); err != nil || o != jsonx.Some("x") {
		t.Fatalf("o = %+v %v", o, err)
	}
	if err := o.UnmarshalJSON([]byte(`null`)); err != nil || !o.IsNull() {
		t.Fatalf("o = %+v %v", o, err)
	}
	if !jsonx.Some(jsonx.ObjectOf("a", 1)).Equal(jsonx.Some(jsonx.ObjectOf("a", 1))) || jsonx.Some(1).Equal(jsonx.Null[int]()) {
		t.Fatal("Equal")
	}
	// encoding/json interop: omitzero uses IsZero; MarshalJSON writes the value.
	type S struct {
		A jsonx.Opt[int] `json:"a,omitzero"`
		B jsonx.Opt[int] `json:"b"`
	}
	b, err := json.Marshal(S{B: jsonx.Some(2)})
	if err != nil || string(b) != `{"b":2}` {
		t.Fatalf("encoding/json = %s %v", b, err)
	}
	var s S
	if err := json.Unmarshal([]byte(`{"a":null,"b":4}`), &s); err != nil || !s.A.IsNull() || s.B != jsonx.Some(4) {
		t.Fatalf("encoding/json decode = %+v %v", s, err)
	}
}

type tdListUnmarshaler struct{ items []any }

func (l *tdListUnmarshaler) UnmarshalJSON(b []byte) error {
	v, err := jsonx.Parse(b)
	if err != nil {
		return err
	}
	l.items, _ = v.([]any)
	return nil
}

func TestDecode_UnmarshalerTargetsAreNotDepthLimited(t *testing.T) {
	// Decode is a Go-side conversion (a TS cast), so a json.Unmarshaler field must take a
	// value nested deeper than JSON.stringify allows.
	type holder struct {
		List tdListUnmarshaler `json:"list"`
	}
	text := `{"list":[` + strings.Repeat("[", 10000) + strings.Repeat("]", 10000) + `]}`
	var h holder
	if err := jsonx.Unmarshal([]byte(text), &h); err != nil {
		t.Fatal(err)
	}
	if len(h.List.items) != 1 {
		t.Fatalf("items = %d", len(h.List.items))
	}
}
