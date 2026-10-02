package jsonx_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestParse_AcceptsAnyNestingDepth(t *testing.T) {
	// V8's JSON.parse keeps an explicit continuation stack, so nesting is unlimited.
	// node: JSON.parse("[".repeat(1e6)) throws "Unexpected end of JSON input".
	for _, n := range []int{1e5, 1e6} {
		_, err := jsonx.Parse([]byte(strings.Repeat("[", n)))
		var se *jsonx.SyntaxError
		if !errors.As(err, &se) || se.Message != "Unexpected end of JSON input" || se.Position != n {
			t.Fatalf("depth %d: %v", n, err)
		}
	}
	// node: JSON.parse("[".repeat(1e5) + "0" + "]".repeat(1e5)) nests 100000 arrays around 0.
	v, err := jsonx.Parse([]byte(strings.Repeat("[", 1e5) + "0" + strings.Repeat("]", 1e5)))
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for {
		arr, ok := v.([]any)
		if !ok {
			break
		}
		if len(arr) != 1 {
			t.Fatalf("level %d has %d elements", depth, len(arr))
		}
		v = arr[0]
		depth++
	}
	if depth != 1e5 || v != 0.0 {
		t.Fatalf("depth %d, innermost %v", depth, v)
	}
	// Errors deep inside keep V8's messages; node:
	// JSON.parse('{"a":['.repeat(5000) + "1,}") throws the message below.
	text := strings.Repeat(`{"a":[`, 5000) + `1,}`
	_, err = jsonx.ParseString(text)
	want := "Unexpected token '}', ..." + `":[{"a":[1,}"` + " is not valid JSON"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestParse_IncompleteSurrogateBytesDecodeLikeNode(t *testing.T) {
	// Raw bytes as node decodes them (Buffer.toString("utf8"), WHATWG): an incomplete
	// WTF-8 surrogate is invalid at its second byte. Outputs recorded from node v24:
	// JSON.stringify(JSON.parse(Buffer.from(bytes).toString("utf8"))) or the error message.
	cases := []struct {
		in   string
		want string
	}{
		{"\"\xed\xa0\"", "\"��\""},
		{"\"\xed\xa0A\"", "\"��A\""},
		{"\"\xed\xbf\"", "\"��\""},
		{"\"\xed\x9f\xbf\"", "\"퟿\""},
		{"\"\xed\xa0", "Unterminated string in JSON at position 3 (line 1 column 4)"},
		{"[\xed\xa0]", "Unexpected token '�', \"[��]\" is not valid JSON"},
		{"\"\\\xed\xa0\"", "Unexpected token '�', \"\"\\��\"\" is not valid JSON"},
	}
	for _, c := range cases {
		v, err := jsonx.Parse([]byte(c.in))
		got := ""
		if err != nil {
			got = err.Error()
		} else {
			got = jsonx.MustStringify(v)
		}
		if got != c.want {
			t.Errorf("Parse(% x) = %q, want %q", c.in, got, c.want)
		}
	}
	// Stringify of a Go string holding the same bytes agrees: node
	// JSON.stringify(Buffer.from([0xed, 0xa0, 0x41]).toString("utf8")) === '"��A"'.
	if got := jsonx.Quote("\xed\xa0A"); got != "\"��A\"" {
		t.Fatalf("Quote = %q", got)
	}
	// A complete WTF-8 surrogate stays a lone surrogate (package doc).
	if got := jsonx.Quote("\xed\xa0\x80"); got != `"\ud800"` {
		t.Fatalf("Quote = %q", got)
	}
}
