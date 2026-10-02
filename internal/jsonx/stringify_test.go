package jsonx_test

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tsText struct {
	Type          string  `json:"type"`
	Text          string  `json:"text"`
	TextSignature *string `json:"textSignature,omitzero"`
}

type tsUsage struct {
	Input  int     `json:"input"`
	Output int     `json:"output"`
	Cost   float64 `json:"cost"`
}

type tsMessage struct {
	Role         string                `json:"role"`
	Content      []any                 `json:"content"`
	Usage        *tsUsage              `json:"usage,omitzero"`
	StopReason   jsonx.Opt[string]     `json:"stopReason"`
	ErrorMessage jsonx.Opt[string]     `json:"errorMessage,omitzero"`
	Details      *jsonx.Object         `json:"details,omitzero"`
	Timestamp    int64                 `json:"timestamp"`
	Hook         func()                `json:"hook"`
	Internal     string                `json:"-"`
	Extra        map[string]any        `json:"extra,omitempty"`
	Labels       []string              `json:"labels,omitempty"`
	Parent       jsonx.Opt[*tsMessage] `json:"parent,omitzero"`
}

func TestStringify_TypedStructFollowsTagsAndJSRules(t *testing.T) {
	sig := "sig"
	a, b := 0.1, 0.2 // runtime addition; the constant 0.1 + 0.2 folds to exactly 0.3
	m := tsMessage{
		Role:       "assistant",
		Content:    []any{&tsText{Type: "text", Text: "hi <b> &  "}, tsText{Type: "text", Text: "x", TextSignature: &sig}},
		Usage:      &tsUsage{Input: 1, Output: 2, Cost: a + b},
		StopReason: jsonx.Null[string](),
		Details:    jsonx.ObjectOf("b", 1, "0", "first"),
		Timestamp:  1704067200123,
		Hook:       func() {},
		Internal:   "secret",
		Labels:     []string{},
	}
	got, err := jsonx.Stringify(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":[{"type":"text","text":"hi <b> & ` + " " + `"},{"type":"text","text":"x","textSignature":"sig"}],` +
		`"usage":{"input":1,"output":2,"cost":0.30000000000000004},"stopReason":null,"details":{"0":"first","b":1},"timestamp":1704067200123}`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}

	m.StopReason = jsonx.Some("stop")
	m.ErrorMessage = jsonx.Null[string]()
	m.Details = nil
	m.Usage = nil
	m.Content = nil
	m.Extra = map[string]any{"z": 1, "10": true, "2": nil, "a": []any{}}
	m.Parent = jsonx.Some(&tsMessage{Role: "user", Content: []any{}})
	got, err = jsonx.StringifyIndent(m, "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = `{
  "role": "assistant",
  "content": null,
  "stopReason": "stop",
  "errorMessage": null,
  "timestamp": 1704067200123,
  "extra": {
    "2": null,
    "10": true,
    "a": [],
    "z": 1
  },
  "parent": {
    "role": "user",
    "content": [],
    "stopReason": null,
    "timestamp": 0
  }
}`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestStringify_NumbersAndSpecialValues(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{math.Copysign(0, -1), "0"},
		{math.NaN(), "null"},
		{math.Inf(-1), "null"},
		{[]any{math.Inf(1), 1e21, 1e-7, 1.5e-7, 123456789012345680000.0, 5e-324}, "[null,1e+21,1e-7,1.5e-7,123456789012345680000,5e-324]"},
		// Go integers are JS numbers: beyond 2^53 they print as the nearest double.
		// node: JSON.stringify(Number(9007199254740993n)), Number(2n**63n-1n), Number(2n**64n-1n)
		{int64(9007199254740993), "9007199254740992"},
		{int64(math.MaxInt64), "9223372036854776000"},
		{int(math.MinInt64), "-9223372036854776000"},
		{uint64(math.MaxUint64), "18446744073709552000"},
		{int64(1 << 53), "9007199254740992"},
		{-int64(1 << 53), "-9007199254740992"},
		{struct {
			N uint64 `json:"n"`
			S int64  `json:"s,string"`
		}{N: 1<<53 + 1, S: 1<<53 + 1}, `{"n":9007199254740992,"s":"9007199254740992"}`},
		{uint8(7), "7"},
		{float32(0.1), "0.1"},
		{[]byte("hi"), `"aGk="`},
		{[]int(nil), "null"},
		{map[string]int(nil), "null"},
		{(*tsUsage)(nil), "null"},
		{json.Number("1.50"), "1.5"},
		{json.Number("-0"), "0"},
		// node: JSON.stringify(JSON.parse("123456789012345678901"))
		{json.Number("123456789012345678901"), "123456789012345680000"},
		{json.Number("1E400"), "null"},
		{"  \u007f\x00\x1f\b\f\n\r\t\"\\/", `"` + "  \u007f" + `\u0000\u001f\b\f\n\r\t\"\\/"`},
		{[]any{func() {}, nil}, "[null,null]"},
		{map[int]string{10: "a", 2: "b", -1: "c"}, `{"2":"b","10":"a","-1":"c"}`},
		{struct {
			A int `json:"a,string"`
			B bool
			C string `json:",string"`
			d int
		}{A: 5, B: true, C: "x", d: 1}, `{"a":"5","B":true,"C":"\"x\""}`},
		{time.UnixMilli(1704067200123).In(time.FixedZone("x", 3600)), `"2024-01-01T00:00:00.123Z"`},
		{&[]time.Time{time.UnixMilli(0)}, `["1970-01-01T00:00:00.000Z"]`},
		{json.RawMessage(` { "b" : "<" , "1" : [ 1.0 , -0 ] } `), `{"1":[1,0],"b":"<"}`},
	}
	for i, c := range cases {
		got, err := jsonx.Stringify(c.v)
		if err != nil || got != c.want {
			t.Errorf("case %d: Stringify(%#v) = %s (%v), want %s", i, c.v, got, err, c.want)
		}
	}
}

func TestStringify_UndefinedTopLevel(t *testing.T) {
	got, err := jsonx.Stringify(func() {})
	if err != nil || got != "" {
		t.Fatalf("Stringify(func) = %q, %v", got, err)
	}
	b, err := jsonx.Marshal(func() {})
	if err != nil || b != nil {
		t.Fatalf("Marshal(func) = %q, %v", b, err)
	}
	if got := jsonx.MustStringify(jsonx.ObjectOf("f", func() {}, "a", 1)); got != `{"a":1}` {
		t.Fatalf("function property not omitted: %s", got)
	}
	// JSON.stringify({a: () => 1}, null, 2) === "{}"
	if got, _ := jsonx.StringifyIndent(jsonx.ObjectOf("a", func() {}), "  "); got != "{}" {
		t.Fatalf("got %q", got)
	}
}

func TestStringify_LoneSurrogatesAndInvalidUTF8(t *testing.T) {
	v, err := jsonx.ParseString(`["\ud800","\udc00\ud800","😀","😀x"]`)
	if err != nil {
		t.Fatal(err)
	}
	arr := v.([]any)
	if arr[0].(string) != "\xed\xa0\x80" {
		t.Fatalf("lone surrogate stored as % x", arr[0])
	}
	if arr[2].(string) != "😀" || arr[3].(string) != "😀x" {
		t.Fatalf("pair not joined: %q %q", arr[2], arr[3])
	}
	// JSON.stringify(["\ud800","\udc00\ud800","😀"]) === '["\\ud800","\\udc00\\ud800","😀"]'
	if got := jsonx.MustStringify(arr[:3]); got != `["\ud800","\udc00\ud800","😀"]` {
		t.Fatalf("got %s", got)
	}
	// Two WTF-8 halves concatenated in Go form one character, as in a UTF-16 JS string.
	if got := jsonx.Quote("\xed\xa0\xbd" + "\xed\xb8\x80"); got != `"😀"` {
		t.Fatalf("got %s", got)
	}
	// Invalid UTF-8: one U+FFFD per maximal invalid subsequence.
	if got := jsonx.Quote("a\xe2\x82b\xff\xc3"); got != "\"a�b��\"" {
		t.Fatalf("got %q", got)
	}
	if got := string(jsonx.AppendQuote([]byte("x="), "q")); got != `x="q"` {
		t.Fatalf("AppendQuote = %s", got)
	}
}

func TestStringify_IndentGap(t *testing.T) {
	// JSON.stringify({b: 1, a: [], c: {}}, null, "\t")
	o := jsonx.ObjectOf("b", 1, "a", []any{}, "c", jsonx.NewObject())
	if got, _ := jsonx.StringifyIndent(o, "\t"); got != "{\n\t\"b\": 1,\n\t\"a\": [],\n\t\"c\": {}\n}" {
		t.Fatalf("got %q", got)
	}
	// Gap is cut to 10 UTF-16 units: 6 emoji (12 units) become 5.
	if got, _ := jsonx.StringifyIndent(jsonx.ObjectOf("a", "x"), strings.Repeat("😀", 6)); got != "{\n"+strings.Repeat("😀", 5)+"\"a\": \"x\"\n}" {
		t.Fatalf("got %q", got)
	}
	if got, _ := jsonx.StringifyIndent([]any{1}, ""); got != "[1]" {
		t.Fatalf("empty gap: %q", got)
	}
}

type tsMarshaler struct{ v string }

func (m tsMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"html": m.v, "n": 1.0, "nested": []any{}})
}

type tsPtrMarshaler struct{ n int }

func (m *tsPtrMarshaler) MarshalJSON() ([]byte, error) {
	if m.n < 0 {
		return nil, errors.New("negative")
	}
	return []byte(`{"n":` + string(rune('0'+m.n)) + `}`), nil
}

type tsTextKey struct{ a, b string }

func (k tsTextKey) MarshalText() ([]byte, error) { return []byte(k.a + "/" + k.b), nil }

func TestStringify_MarshalersAreNormalized(t *testing.T) {
	got, err := jsonx.StringifyIndent(map[string]any{"m": tsMarshaler{"<a&b>"}}, " ")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n \"m\": {\n  \"html\": \"<a&b>\",\n  \"n\": 1,\n  \"nested\": []\n }\n}"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	type holder struct {
		P tsPtrMarshaler  `json:"p"`
		Q *tsPtrMarshaler `json:"q"`
	}
	// Addressable values use pointer-receiver MarshalJSON, as in encoding/json.
	h := &holder{P: tsPtrMarshaler{n: 3}}
	if got := jsonx.MustStringify(h); got != `{"p":{"n":3},"q":null}` {
		t.Fatalf("got %s", got)
	}
	// Unlike encoding/json, non-addressable values (a struct passed by value, map values,
	// interface contents) use the pointer-receiver MarshalJSON too.
	if got := jsonx.MustStringify(*h); got != `{"p":{"n":3},"q":null}` {
		t.Fatalf("by value: %s", got)
	}
	if got := jsonx.MustStringify(map[string]tsPtrMarshaler{"a": {n: 2}}); got != `{"a":{"n":2}}` {
		t.Fatalf("map value: %s", got)
	}
	if got := jsonx.MustStringify([]any{tsPtrMarshaler{n: 1}}); got != `[{"n":1}]` {
		t.Fatalf("interface value: %s", got)
	}
	h.Q = &tsPtrMarshaler{n: -1}
	_, err = jsonx.Stringify(h)
	var me *json.MarshalerError
	if !errors.As(err, &me) || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("err = %v", err)
	}
	if got := jsonx.MustStringify(map[tsTextKey]int{{"x", "y"}: 1}); got != `{"x/y":1}` {
		t.Fatalf("text key: %s", got)
	}
}

func TestStringify_EmbeddedStructs(t *testing.T) {
	type Base struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type inner struct {
		Hidden string `json:"hidden"`
	}
	type Entry struct {
		Type string `json:"type"`
		Base
		*inner
		Name string `json:"name"`
	}
	e := Entry{Type: "t", Base: Base{ID: "1", Name: "base"}, Name: "outer"}
	if got := jsonx.MustStringify(e); got != `{"type":"t","id":"1","name":"outer"}` {
		t.Fatalf("got %s", got)
	}
	e.inner = &inner{Hidden: "h"}
	if got := jsonx.MustStringify(e); got != `{"type":"t","id":"1","hidden":"h","name":"outer"}` {
		t.Fatalf("got %s", got)
	}
}

func TestStringify_CircularStructureMessages(t *testing.T) {
	// Messages recorded from node v24.
	a := jsonx.NewObject()
	a.Set("self", a)
	_, err := jsonx.Stringify(a)
	want := "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    --- property 'self' closes the circle"
	var te *jsonx.TypeError
	if !errors.As(err, &te) || te.Message != want || te.Name() != "TypeError" {
		t.Fatalf("err = %v", err)
	}

	// const a = {b: {c: {d: {e: {f: {}}}}}}; a.b.c.d.e.f.g = a.b;
	root := jsonx.NewObject()
	b := jsonx.NewObject()
	c := jsonx.NewObject()
	d := jsonx.NewObject()
	e := jsonx.NewObject()
	f := jsonx.NewObject()
	root.Set("b", b)
	b.Set("c", c)
	c.Set("d", d)
	d.Set("e", e)
	e.Set("f", f)
	f.Set("g", b)
	_, err = jsonx.Stringify(root)
	want = "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    |     property 'c' -> object with constructor 'Object'\n    |     property 'd' -> object with constructor 'Object'\n    |     ...\n    |     property 'f' -> object with constructor 'Object'\n    --- property 'g' closes the circle"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	// const a = [1, {x: []}]; a[1].x.push(a);
	arr := []any{1.0, nil}
	x := []any{nil}
	arr[1] = jsonx.ObjectOf("x", x)
	x[0] = arr
	_, err = jsonx.Stringify(arr)
	want = "Converting circular structure to JSON\n    --> starting at object with constructor 'Array'\n    |     index 1 -> object with constructor 'Object'\n    |     property 'x' -> object with constructor 'Array'\n    --- index 0 closes the circle"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	// const a = {}; const b = {"": a}; a.k = b;
	oa := jsonx.NewObject()
	ob := jsonx.ObjectOf("", oa)
	oa.Set("k", ob)
	_, err = jsonx.Stringify(oa)
	want = "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    |     property 'k' -> object with constructor 'Object'\n    --- <anonymous> closes the circle"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	// A deep but acyclic sharing of one object is fine (JS allows repeated references).
	shared := jsonx.ObjectOf("v", 1)
	if got := jsonx.MustStringify([]any{shared, shared}); got != `[{"v":1},{"v":1}]` {
		t.Fatalf("got %s", got)
	}

	// Deep structures switch to map-based cycle checks.
	deep := jsonx.NewObject()
	cur := deep
	for range 100 {
		next := jsonx.NewObject()
		cur.Set("n", next)
		cur = next
	}
	cur.Set("loop", deep)
	if _, err := jsonx.Stringify(deep); err == nil || !strings.HasSuffix(err.Error(), "--- property 'loop' closes the circle") {
		t.Fatalf("deep cycle: %v", err)
	}

	type node struct {
		Next *node `json:"next"`
	}
	n := &node{}
	n.Next = n
	if _, err := jsonx.Stringify(n); err == nil || !strings.HasPrefix(err.Error(), "Converting circular structure to JSON") {
		t.Fatalf("struct cycle: %v", err)
	}
}

func TestStringify_BigIntIsATypeError(t *testing.T) {
	_, err := jsonx.Stringify(map[string]any{"a": big.NewInt(1)})
	if err == nil || err.Error() != "Do not know how to serialize a BigInt" {
		t.Fatalf("err = %v", err)
	}
	if got := jsonx.MustStringify(struct {
		B *big.Int `json:"b"`
	}{}); got != `{"b":null}` {
		t.Fatalf("nil big.Int: %s", got)
	}
}

func TestStringify_UnsupportedTypes(t *testing.T) {
	_, err := jsonx.Stringify(make(chan int))
	var ute *json.UnsupportedTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v", err)
	}
}

func TestStringifyWithReplacer_MatchesJS(t *testing.T) {
	// node: JSON.stringify(v, replacer, 2) with the replacer below; see the calls list.
	v, _ := jsonx.ParseString(`{"apiKey":"sk-1","nested":{"token":null,"url":"https://x?key=1","list":[1,"a",{"password":"p"}]},"n":2}`)
	var calls []string
	isSecret := func(k string) bool {
		k = strings.ToLower(k)
		return strings.Contains(k, "key") || strings.Contains(k, "token") || strings.Contains(k, "password")
	}
	got, err := jsonx.StringifyWithReplacer(v, func(key string, child any) (any, bool) {
		calls = append(calls, key)
		if child != nil && isSecret(key) {
			return "[REDACTED]", true
		}
		if key == "n" || key == "1" {
			return nil, false
		}
		return child, true
	}, "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"apiKey\": \"[REDACTED]\",\n  \"nested\": {\n    \"token\": null,\n    \"url\": \"https://x?key=1\",\n    \"list\": [\n      1,\n      null,\n      {\n        \"password\": \"[REDACTED]\"\n      }\n    ]\n  }\n}"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	wantCalls := []string{"", "apiKey", "nested", "token", "url", "list", "0", "1", "2", "password", "n"}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("calls = %q", calls)
	}
	// JSON.stringify({a: 1}, () => undefined) === undefined
	got, err = jsonx.StringifyWithReplacer(jsonx.ObjectOf("a", 1), func(string, any) (any, bool) { return nil, false }, "")
	if err != nil || got != "" {
		t.Fatalf("got %q %v", got, err)
	}
	// Typed values reach the replacer already converted (toJSON semantics).
	got, _ = jsonx.StringifyWithReplacer(tsUsage{Input: 1}, func(key string, child any) (any, bool) {
		if key == "" {
			if _, ok := child.(*jsonx.Object); !ok {
				t.Errorf("root passed as %T", child)
			}
		}
		return child, true
	}, "")
	if got != `{"input":1,"output":0,"cost":0}` {
		t.Fatalf("got %s", got)
	}
}

func TestStringify_DeepNestingIsARangeError(t *testing.T) {
	// node v24 from a shallow stack: JSON.stringify of 6185 nested arrays or objects
	// works, deeper throws "RangeError: Maximum call stack size exceeded" (the exact
	// limit depends on the caller's stack; jsonx uses 6185).
	nested := func(n int, open, close string) any {
		v, err := jsonx.ParseString(strings.Repeat(open, n) + "0" + strings.Repeat(close, n))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ open, close string }{{"[", "]"}, {`{"a":`, "}"}} {
		ok := nested(6185, c.open, c.close)
		if _, err := jsonx.Stringify(ok); err != nil {
			t.Fatalf("depth 6185 %s: %v", c.open, err)
		}
		if _, err := jsonx.StringifyIndent(ok, "  "); err != nil {
			t.Fatalf("depth 6185 %s indented: %v", c.open, err)
		}
		deep := nested(6186, c.open, c.close)
		_, err := jsonx.Stringify(deep)
		var re *jsonx.RangeError
		if !errors.As(err, &re) || re.Message != "Maximum call stack size exceeded" || re.Name() != "RangeError" {
			t.Fatalf("depth 6186 %s: %v", c.open, err)
		}
		if _, err := jsonx.StringifyWithReplacer(deep, func(_ string, v any) (any, bool) { return v, true }, ""); !errors.As(err, &re) {
			t.Fatalf("replacer depth 6186 %s: %v", c.open, err)
		}
		if _, err := jsonx.Encode(deep); !errors.As(err, &re) {
			t.Fatalf("Encode depth 6186 %s: %v", c.open, err)
		}
	}
	// Typed values count too.
	type node struct {
		Next *node `json:"next,omitzero"`
	}
	root := &node{}
	cur := root
	for range 6185 {
		cur.Next = &node{}
		cur = cur.Next
	}
	var re *jsonx.RangeError
	if _, err := jsonx.Marshal(root); !errors.As(err, &re) {
		t.Fatalf("typed depth 6186: %v", err)
	}
	if _, err := jsonx.Marshal(root.Next); err != nil {
		t.Fatalf("typed depth 6185: %v", err)
	}
}

func TestStringifyWithReplacer_CircularStructureMessages(t *testing.T) {
	identity := func(_ string, v any) (any, bool) { return v, true }
	// node: const a = {}; a.self = a; JSON.stringify(a, (k, v) => v)
	a := jsonx.NewObject()
	a.Set("self", a)
	_, err := jsonx.StringifyWithReplacer(a, identity, "")
	want := "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    --- property 'self' closes the circle"
	var te *jsonx.TypeError
	if !errors.As(err, &te) || te.Message != want {
		t.Fatalf("err = %v", err)
	}
	// node: const b = {x: [1, {y: null}]}; b.x[1].y = b; JSON.stringify(b, (k, v) => v, 2)
	b := jsonx.NewObject()
	inner := jsonx.ObjectOf("y", nil)
	b.Set("x", []any{1.0, inner})
	inner.Set("y", b)
	_, err = jsonx.StringifyWithReplacer(b, identity, "  ")
	want = "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    |     property 'x' -> object with constructor 'Array'\n    |     index 1 -> object with constructor 'Object'\n    --- property 'y' closes the circle"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
	// The replacer sees the cyclic value first and may cut the cycle, as in JS.
	got, err := jsonx.StringifyWithReplacer(b, func(k string, v any) (any, bool) {
		if k == "y" {
			return "[cycle]", true
		}
		return v, true
	}, "")
	if err != nil || got != `{"x":[1,{"y":"[cycle]"}]}` {
		t.Fatalf("got %s %v", got, err)
	}
}
