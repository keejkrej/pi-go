package partialjson_test

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/partialjson"
)

// repr renders a parse result the same way the vector generator does in JS: numbers as
// String(x) (with -0 kept), strings as JSON.stringify, objects with own keys in order.
func repr(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		case x == 0 && math.Signbit(x):
			return "-0"
		}
		return jsonx.FormatNumber(x)
	case string:
		s, err := jsonx.Stringify(x)
		if err != nil {
			t.Fatalf("stringify %q: %v", x, err)
		}
		return s
	case []any:
		if x == nil {
			t.Fatalf("nil slice in result")
		}
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = repr(t, item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *jsonx.Object:
		parts := make([]string, 0, x.Len())
		for k, item := range x.All() {
			parts = append(parts, repr(t, k)+":"+repr(t, item))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	t.Fatalf("unexpected value type %T", v)
	return ""
}

func errorKind(err error) string {
	var pj *partialjson.PartialJSON
	var mj *partialjson.MalformedJSON
	var se *jsonx.SyntaxError
	switch {
	case errors.As(err, &pj):
		return "PartialJSON"
	case errors.As(err, &mj):
		return "MalformedJSON"
	case errors.As(err, &se):
		return "SyntaxError"
	}
	return "Error"
}

// testdata/vectors.json was produced by running partial-json 0.1.7 under node v24 with a
// generator script (not kept) over hand-picked inputs and every prefix of a set of
// documents, for several allow masks. It is loaded with jsonx so lone surrogates in the
// inputs survive.
func loadVectors(t *testing.T) *jsonx.Object {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Object)
}

func TestPartialjson_Vectors(t *testing.T) {
	root := loadVectors(t)
	cases, _ := root.GetArray("cases")
	if len(cases) < 1000 {
		t.Fatalf("too few vectors: %d", len(cases))
	}
	failures := 0
	for _, c := range cases {
		obj := c.(*jsonx.Object)
		input, _ := obj.GetString("input")
		mask, _ := obj.GetNumber("mask")
		ok, _ := obj.GetBool("ok")
		got, err := partialjson.Parse(input, partialjson.Allow(int(mask)))
		if ok {
			want, _ := obj.GetString("value")
			if err != nil {
				t.Errorf("Parse(%q, %d): unexpected error %v, want %s", input, int(mask), err, want)
				failures++
			} else if r := repr(t, got); r != want {
				t.Errorf("Parse(%q, %d) = %s, want %s", input, int(mask), r, want)
				failures++
			}
		} else {
			wantKind, _ := obj.GetString("kind")
			wantMsg, _ := obj.GetString("message")
			if err == nil {
				t.Errorf("Parse(%q, %d) = %s, want %s error %q", input, int(mask), repr(t, got), wantKind, wantMsg)
				failures++
			} else if kind := errorKind(err); kind != wantKind || err.Error() != wantMsg {
				t.Errorf("Parse(%q, %d) error = %s %q, want %s %q", input, int(mask), kind, err.Error(), wantKind, wantMsg)
				failures++
			}
		}
		if failures > 50 {
			t.Fatal("too many failures")
		}
	}
}

func TestPartialjson_DefaultAllowIsALL(t *testing.T) {
	root := loadVectors(t)
	defaults, _ := root.GetArray("defaults")
	for _, c := range defaults {
		obj := c.(*jsonx.Object)
		input, _ := obj.GetString("input")
		want, _ := obj.GetString("value")
		got, err := partialjson.Parse(input, partialjson.ALL)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if r := repr(t, got); r != want {
			t.Errorf("Parse(%q) = %s, want %s", input, r, want)
		}
	}
}

func TestPartialjson_ReadmeExamples(t *testing.T) {
	tests := []struct {
		input string
		allow partialjson.Allow
		want  string
	}{
		{`{"key":"value"}`, partialjson.ALL, `{"key":"value"}`},
		{`{"key": "v`, partialjson.STR | partialjson.OBJ, `{"key":"v"}`},
		{`{"key": "v`, partialjson.OBJ, `{}`},
		{`{"key": "value"`, partialjson.OBJ, `{"key":"value"}`},
		{`[ {"key1": "value1", "key2": [ "value2`, partialjson.ALL, `[{"key1":"value1","key2":["value2"]}]`},
		{`-Inf`, partialjson.ALL, `-Infinity`},
		{`[{"a": 1, "b": 2}, {"a": 3,`, partialjson.ARR, `[{"a":1,"b":2}]`},
		{`[{"a": 1, "b": 2}, {"a": 3,`, ^partialjson.OBJ, `[{"a":1,"b":2}]`},
		{`["complete string", "incompl`, ^partialjson.STR, `["complete string"]`},
		{`"hello \u12`, partialjson.ALL, `"hello "`},
		// The README claims `123.` parses as 123; the code only truncates at the last "e",
		// so a partial exponent is what NUM recovers (a top-level `123.` is MalformedJSON).
		{`{"n": 12e`, partialjson.ALL, `{"n":12}`},
		{`nu`, partialjson.ALL, `null`},
		{`tr`, partialjson.ALL, `true`},
		{`fa`, partialjson.ALL, `false`},
		{`Na`, partialjson.ALL, `NaN`},
		{`Inf`, partialjson.ALL, `Infinity`},
	}
	for _, tt := range tests {
		got, err := partialjson.Parse(tt.input, tt.allow)
		if err != nil {
			t.Errorf("Parse(%q, %d): %v", tt.input, tt.allow, err)
			continue
		}
		if r := repr(t, got); r != tt.want {
			t.Errorf("Parse(%q, %d) = %s, want %s", tt.input, tt.allow, r, tt.want)
		}
	}
}

func TestPartialjson_MalformedMessage(t *testing.T) {
	_, err := partialjson.Parse("wrong", partialjson.ALL)
	var mj *partialjson.MalformedJSON
	if !errors.As(err, &mj) {
		t.Fatalf("want MalformedJSON, got %T %v", err, err)
	}
	want := `SyntaxError: Unexpected token 'w', "wrong" is not valid JSON at position 0`
	if mj.Error() != want {
		t.Errorf("message = %q, want %q", mj.Error(), want)
	}
	if mj.Name() != "Error" {
		t.Errorf("Name() = %q, want Error", mj.Name())
	}
}

func TestPartialjson_PartialMessage(t *testing.T) {
	_, err := partialjson.Parse(`[1, 2`, partialjson.ALL&^partialjson.ARR)
	var pj *partialjson.PartialJSON
	if !errors.As(err, &pj) {
		t.Fatalf("want PartialJSON, got %T %v", err, err)
	}
	if want := "Expected ']' at end of array at position 5"; pj.Error() != want {
		t.Errorf("message = %q, want %q", pj.Error(), want)
	}
	if pj.Name() != "Error" {
		t.Errorf("Name() = %q, want Error", pj.Name())
	}
}

func TestPartialjson_EmptyInput(t *testing.T) {
	_, err := partialjson.Parse(" \t", partialjson.ALL)
	if err == nil || err.Error() != " \t is empty" {
		t.Fatalf("err = %v", err)
	}
}

func TestPartialjson_AllowConstants(t *testing.T) {
	tests := []struct {
		got  partialjson.Allow
		want int
	}{
		{partialjson.STR, 1}, {partialjson.NUM, 2}, {partialjson.ARR, 4}, {partialjson.OBJ, 8},
		{partialjson.NULL, 16}, {partialjson.BOOL, 32}, {partialjson.NAN, 64}, {partialjson.INFINITY, 128},
		{partialjson.Infinity, 256}, {partialjson.INF, 384}, {partialjson.SPECIAL, 496}, {partialjson.ATOM, 499},
		{partialjson.COLLECTION, 12}, {partialjson.ALL, 511},
	}
	for i, tt := range tests {
		if int(tt.got) != tt.want {
			t.Errorf("constant %d = %d, want %d", i, tt.got, tt.want)
		}
	}
}

func TestPartialjson_ResultsStringifyLikeJS(t *testing.T) {
	// NaN and ±Infinity come out of partial-json; JSON.stringify writes them as null.
	v, err := partialjson.Parse(`{"a": NaN, "b": [Infinity, -Inf`, partialjson.ALL)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonx.Stringify(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":null,"b":[null,null]}`; s != want {
		t.Errorf("Stringify = %s, want %s", s, want)
	}
	// Empty arrays are non-nil so they stringify as [].
	v, err = partialjson.Parse(`{"edits": [`, partialjson.ALL)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = jsonx.Stringify(v)
	if want := `{"edits":[]}`; s != want {
		t.Errorf("Stringify = %s, want %s", s, want)
	}
}
