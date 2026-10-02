package jsonx_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// testdata/v8_vectors.json was produced by running node v24 on a generator script
// (/tmp/jsonx-scratch/gen.js, not kept) that records JSON.parse / JSON.stringify results.
// Strings are base64 of their WTF-8 bytes (lone surrogates as ED xx xx), so the file is
// loaded with encoding/json and never depends on the code under test.
type v8Vectors struct {
	Generator string `json:"generator"`
	RoundTrip []struct {
		Text     []byte `json:"text"`
		Compact  []byte `json:"compact"`
		Gap      []byte `json:"gap"`
		Indented []byte `json:"indented"`
	} `json:"roundTrip"`
	ParseCases []struct {
		Text []byte `json:"text"`
		OK   bool   `json:"ok"`
		Out  []byte `json:"out"`
	} `json:"parseCases"`
	ByteCases []struct {
		Bytes []byte `json:"bytes"`
		OK    bool   `json:"ok"`
		Out   []byte `json:"out"`
	} `json:"byteCases"`
	NumberCases []struct {
		Bits string `json:"bits"`
		Out  string `json:"out"`
	} `json:"numberCases"`
	DateCases []struct {
		Ms  float64 `json:"ms"`
		Out string  `json:"out"`
	} `json:"dateCases"`
}

func loadV8Vectors(t *testing.T) *v8Vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/v8_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v v8Vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestV8Vectors_ParseStringifyRoundTrip(t *testing.T) {
	vec := loadV8Vectors(t)
	if len(vec.RoundTrip) == 0 {
		t.Fatal("no vectors")
	}
	for i, c := range vec.RoundTrip {
		v, err := jsonx.Parse(c.Text)
		if err != nil {
			t.Errorf("case %d: Parse(%q): %v", i, c.Text, err)
			continue
		}
		got, err := jsonx.Stringify(v)
		if err != nil || got != string(c.Compact) {
			t.Errorf("case %d: Stringify(Parse(%q))\n got %q (%v)\nwant %q", i, c.Text, got, err, c.Compact)
		}
		ind, err := jsonx.StringifyIndent(v, string(c.Gap))
		if err != nil || ind != string(c.Indented) {
			t.Errorf("case %d: StringifyIndent(Parse(%q), %q)\n got %q (%v)\nwant %q", i, c.Text, c.Gap, ind, err, c.Indented)
		}
		b, err := jsonx.MarshalIndent(v, string(c.Gap))
		if err != nil || string(b) != string(c.Indented) {
			t.Errorf("case %d: MarshalIndent mismatch: %q", i, b)
		}
		// A json.RawMessage holding the messy text must be normalized to the same bytes.
		raw, err := jsonx.StringifyIndent(json.RawMessage(c.Text), string(c.Gap))
		if err != nil || raw != string(c.Indented) {
			t.Errorf("case %d: RawMessage(%q) re-emitted\n got %q (%v)\nwant %q", i, c.Text, raw, err, c.Indented)
		}
		// Clone and Encode keep the value.
		again, _ := jsonx.Stringify(jsonx.CloneValue(v))
		if again != string(c.Compact) {
			t.Errorf("case %d: clone changed output: %q", i, again)
		}
	}
}

func TestV8Vectors_ParseErrors(t *testing.T) {
	vec := loadV8Vectors(t)
	for i, c := range vec.ParseCases {
		v, err := jsonx.Parse(c.Text)
		if c.OK {
			if err != nil {
				t.Errorf("case %d: Parse(%q) failed: %v", i, c.Text, err)
				continue
			}
			got, _ := jsonx.Stringify(v)
			if got != string(c.Out) {
				t.Errorf("case %d: Stringify(Parse(%q)) = %q, want %q", i, c.Text, got, c.Out)
			}
			continue
		}
		var se *jsonx.SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("case %d: Parse(%q) = %v, want SyntaxError %q", i, c.Text, err, c.Out)
			continue
		}
		if se.Message != string(c.Out) {
			t.Errorf("case %d: Parse(%q)\n got %q\nwant %q", i, c.Text, se.Message, c.Out)
		}
		if got := jsonx.ParseErrorMessage(err, string(c.Text)); got != string(c.Out) {
			t.Errorf("case %d: ParseErrorMessage = %q", i, got)
		}
		// encoding/json errors map to the same V8 message.
		var sink any
		if jerr := json.Unmarshal(c.Text, &sink); jerr != nil {
			if got := jsonx.ParseErrorMessage(jerr, string(c.Text)); got != string(c.Out) {
				t.Errorf("case %d: ParseErrorMessage(encoding/json error) = %q, want %q", i, got, c.Out)
			}
		}
	}
}

func TestV8Vectors_InvalidUTF8Input(t *testing.T) {
	vec := loadV8Vectors(t)
	for i, c := range vec.ByteCases {
		v, err := jsonx.Parse(c.Bytes)
		if c.OK {
			if err != nil {
				t.Errorf("case %d: Parse(% x) failed: %v", i, c.Bytes, err)
				continue
			}
			got, _ := jsonx.Stringify(v)
			if got != string(c.Out) {
				t.Errorf("case %d: Parse(% x) -> %q, want %q", i, c.Bytes, got, c.Out)
			}
			continue
		}
		if err == nil || err.Error() != string(c.Out) {
			t.Errorf("case %d: Parse(% x) error %v, want %q", i, c.Bytes, err, c.Out)
		}
	}
}

func TestV8Vectors_NumberToString(t *testing.T) {
	vec := loadV8Vectors(t)
	for _, c := range vec.NumberCases {
		bits, err := strconv.ParseUint(c.Bits, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		x := math.Float64frombits(bits)
		if got := jsonx.FormatNumber(x); got != c.Out {
			t.Errorf("FormatNumber(%v [%s]) = %q, want %q", x, c.Bits, got, c.Out)
		}
		if got, _ := jsonx.Stringify(x); got != c.Out {
			t.Errorf("Stringify(%v) = %q, want %q", x, got, c.Out)
		}
	}
}

func TestV8Vectors_DateToJSON(t *testing.T) {
	vec := loadV8Vectors(t)
	for _, c := range vec.DateCases {
		tm := time.UnixMilli(int64(c.Ms))
		if got, _ := jsonx.Stringify(tm); got != c.Out {
			t.Errorf("Stringify(Date(%v)) = %q, want %q", c.Ms, got, c.Out)
		}
	}
}
