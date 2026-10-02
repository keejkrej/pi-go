package js_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/keejkrej/pi-go/internal/js"
)

// The testdata/*.json vectors were produced by Node v24.21.0 (ICU 78.3) with
// TZ=America/New_York; see testdata/README.md.

func loadVectors(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

func fromBits(t *testing.T, s string) float64 {
	t.Helper()
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return math.Float64frombits(u)
}

func sameFloat(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

type strVector struct {
	S      string
	Len    int
	Slices [][4]json.RawMessage
	From   [][6]json.RawMessage
	Idx    []struct {
		Sub         string
		IndexOf     [][3]int
		LastIndexOf int
	}
	Splits [][2]json.RawMessage
	Pads   [][4]json.RawMessage
	UTF16  []uint16 `json:"utf16"`
}

func raw[T any](t *testing.T, m json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(m, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVectors_Strings(t *testing.T) {
	var data struct {
		StrCases  []strVector
		CaseCases [][3]string
		NormCases [][6]string
		TrimCases [][4]string
	}
	loadVectors(t, "strings.json", &data)
	for _, c := range data.StrCases {
		s := c.S
		if got := js.Len(s); got != c.Len {
			t.Errorf("Len(%q) = %d, want %d", s, got, c.Len)
		}
		if got := js.ToUTF16(s); !slices.Equal(got, c.UTF16) && !(len(got) == 0 && len(c.UTF16) == 0) {
			t.Errorf("ToUTF16(%q) = %v, want %v", s, got, c.UTF16)
		}
		if got := js.FromUTF16(c.UTF16); got != s {
			t.Errorf("FromUTF16(%v) = %q, want %q", c.UTF16, got, s)
		}
		for _, sl := range c.Slices {
			a, b := raw[int](t, sl[0]), raw[int](t, sl[1])
			if got, want := js.Slice(s, a, b), raw[string](t, sl[2]); got != want {
				t.Errorf("Slice(%q, %d, %d) = %q, want %q", s, a, b, got, want)
			}
			if got, want := js.Substring(s, a, b), raw[string](t, sl[3]); got != want {
				t.Errorf("Substring(%q, %d, %d) = %q, want %q", s, a, b, got, want)
			}
		}
		for _, f := range c.From {
			a := raw[int](t, f[0])
			if got, want := js.SliceFrom(s, a), raw[string](t, f[1]); got != want {
				t.Errorf("SliceFrom(%q, %d) = %q, want %q", s, a, got, want)
			}
			if got, want := js.SubstringFrom(s, a), raw[string](t, f[2]); got != want {
				t.Errorf("SubstringFrom(%q, %d) = %q, want %q", s, a, got, want)
			}
			wantCode := -1
			if string(f[3]) != "null" {
				wantCode = raw[int](t, f[3])
			}
			if got := js.CharCodeAt(s, a); got != wantCode {
				t.Errorf("CharCodeAt(%q, %d) = %d, want %d", s, a, got, wantCode)
			}
			if got, want := js.CodePointAt(s, a), raw[int](t, f[4]); got != want {
				t.Errorf("CodePointAt(%q, %d) = %d, want %d", s, a, got, want)
			}
			if got, want := js.CharAt(s, a), raw[string](t, f[5]); got != want {
				t.Errorf("CharAt(%q, %d) = %q, want %q", s, a, got, want)
			}
		}
		for _, ix := range c.Idx {
			if got := js.LastIndexOf(s, ix.Sub); got != ix.LastIndexOf {
				t.Errorf("LastIndexOf(%q, %q) = %d, want %d", s, ix.Sub, got, ix.LastIndexOf)
			}
			for _, f := range ix.IndexOf {
				if got := js.IndexOf(s, ix.Sub, f[0]); got != f[1] {
					t.Errorf("IndexOf(%q, %q, %d) = %d, want %d", s, ix.Sub, f[0], got, f[1])
				}
				if got := js.LastIndexOfFrom(s, ix.Sub, f[0]); got != f[2] {
					t.Errorf("LastIndexOfFrom(%q, %q, %d) = %d, want %d", s, ix.Sub, f[0], got, f[2])
				}
			}
		}
		for _, sp := range c.Splits {
			sep, want := raw[string](t, sp[0]), raw[[]string](t, sp[1])
			if got := js.Split(s, sep); !slices.Equal(got, want) {
				t.Errorf("Split(%q, %q) = %q, want %q", s, sep, got, want)
			}
		}
		for _, p := range c.Pads {
			fill, n := raw[string](t, p[0]), raw[int](t, p[1])
			if got, want := js.PadStart(s, n, fill), raw[string](t, p[2]); got != want {
				t.Errorf("PadStart(%q, %d, %q) = %q, want %q", s, n, fill, got, want)
			}
			if got, want := js.PadEnd(s, n, fill), raw[string](t, p[3]); got != want {
				t.Errorf("PadEnd(%q, %d, %q) = %q, want %q", s, n, fill, got, want)
			}
		}
	}
	for _, c := range data.CaseCases {
		if got := js.ToUpper(c[0]); got != c[1] {
			t.Errorf("ToUpper(%q) = %q, want %q", c[0], got, c[1])
		}
		if got := js.ToLower(c[0]); got != c[2] {
			t.Errorf("ToLower(%q) = %q, want %q", c[0], got, c[2])
		}
	}
	for _, c := range data.NormCases {
		for i, form := range []string{"NFC", "NFD", "NFKC", "NFKD", ""} {
			if got := js.Normalize(c[0], form); got != c[i+1] {
				t.Errorf("Normalize(%q, %q) = %q, want %q", c[0], form, got, c[i+1])
			}
		}
	}
	for _, c := range data.TrimCases {
		if got := js.Trim(c[0]); got != c[1] {
			t.Errorf("Trim(%q) = %q, want %q", c[0], got, c[1])
		}
		if got := js.TrimStart(c[0]); got != c[2] {
			t.Errorf("TrimStart(%q) = %q, want %q", c[0], got, c[2])
		}
		if got := js.TrimEnd(c[0]); got != c[3] {
			t.Errorf("TrimEnd(%q) = %q, want %q", c[0], got, c[3])
		}
	}
}

func TestVectors_Collation(t *testing.T) {
	var data struct {
		Words       []string
		LocalePairs [][3]json.RawMessage
		DefaultSort []string
	}
	loadVectors(t, "collation.json", &data)
	sorted := slices.Clone(data.Words)
	slices.SortStableFunc(sorted, js.CompareUTF16)
	if !slices.Equal(sorted, data.DefaultSort) {
		t.Errorf("CompareUTF16 sort = %q, want %q", sorted, data.DefaultSort)
	}
	mismatches := 0
	for _, p := range data.LocalePairs {
		a, b, want := raw[string](t, p[0]), raw[string](t, p[1]), raw[int](t, p[2])
		if got := js.LocaleCompare(a, b); got != want {
			mismatches++
			t.Errorf("LocaleCompare(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
	if mismatches > 0 {
		t.Logf("%d of %d localeCompare pairs differ", mismatches, len(data.LocalePairs))
	}
}

func TestVectors_Numbers(t *testing.T) {
	var data struct {
		NumCases []struct {
			X      string
			Str    string
			Fixed  []string
			Prec   []string
			Radix  []string
			Round  string
			Locale []string
		}
		ParseCases []struct {
			S      string
			Int    []string
			Float  string
			Number string
		}
		Radixes []int
	}
	loadVectors(t, "numbers.json", &data)
	fixedDigits := []int{0, 1, 2, 3, 4, 5, 8, 10, 15, 20, 25, 50, 100}
	precisions := []int{}
	for p := 1; p <= 21; p++ {
		precisions = append(precisions, p)
	}
	precisions = append(precisions, 50, 100)
	radixes := []int{2, 3, 7, 8, 16, 32, 36}
	for _, c := range data.NumCases {
		x := fromBits(t, c.X)
		if got := js.NumberToString(x); got != c.Str {
			t.Errorf("NumberToString(%v) = %q, want %q", x, got, c.Str)
		}
		for i, d := range fixedDigits {
			if got := js.ToFixed(x, d); got != c.Fixed[i] {
				t.Errorf("ToFixed(%v, %d) = %q, want %q", x, d, got, c.Fixed[i])
			}
		}
		for i, p := range precisions {
			if got := js.ToPrecision(x, p); got != c.Prec[i] {
				t.Errorf("ToPrecision(%v, %d) = %q, want %q", x, p, got, c.Prec[i])
			}
		}
		for i, r := range radixes {
			if got := js.NumberToStringRadix(x, r); got != c.Radix[i] {
				t.Errorf("NumberToStringRadix(%v, %d) = %q, want %q", x, r, got, c.Radix[i])
			}
		}
		if got, want := js.Round(x), fromBits(t, c.Round); !sameFloat(got, want) {
			t.Errorf("Round(%v) = %v, want %v", x, got, want)
		}
		locale := []string{
			js.FormatNumberEnUS(x, 0, 3),
			js.FormatNumberEnUS(x, 0, 0),
			js.FormatNumberEnUS(x, 2, 2),
			js.FormatNumberEnUS(x, 0, 1),
			js.FormatNumberEnUS(x, 1, 4),
		}
		for i := range locale {
			if locale[i] != c.Locale[i] {
				t.Errorf("FormatNumberEnUS(%v) variant %d = %q, want %q", x, i, locale[i], c.Locale[i])
			}
		}
	}
	for _, c := range data.ParseCases {
		for i, r := range data.Radixes {
			want := fromBits(t, c.Int[i])
			// The vectors come from an arm64 Node, where V8's generic-radix
			// accumulation uses a fused multiply-add; x86 builds round
			// separately, so results above 2^53 legitimately differ there.
			if (runtime.GOARCH == "amd64" || runtime.GOARCH == "386") && r&(r-1) != 0 && r != 10 && math.Abs(want) > 1<<53 {
				continue
			}
			if got := js.ParseInt(c.S, r); !sameFloat(got, want) {
				t.Errorf("ParseInt(%q, %d) = %v, want %v", c.S, r, got, want)
			}
		}
		if got, want := js.ParseFloat(c.S), fromBits(t, c.Float); !sameFloat(got, want) {
			t.Errorf("ParseFloat(%q) = %v, want %v", c.S, got, want)
		}
		if got, want := js.ToNumber(c.S), fromBits(t, c.Number); !sameFloat(got, want) {
			t.Errorf("ToNumber(%q) = %v, want %v", c.S, got, want)
		}
	}
}

func TestVectors_URI(t *testing.T) {
	type result struct {
		Ok  *string
		Err string
	}
	var data struct {
		URICases []struct {
			S    string
			EncC string
			Enc  string
			DecC result
			Dec  result
		}
	}
	loadVectors(t, "uri.json", &data)
	check := func(name, s string, got string, err error, want result) {
		t.Helper()
		if want.Ok != nil {
			if err != nil || got != *want.Ok {
				t.Errorf("%s(%q) = %q, %v; want %q", name, s, got, err, *want.Ok)
			}
			return
		}
		if err == nil || js.ErrorString(err) != want.Err {
			t.Errorf("%s(%q) error = %v; want %s", name, s, err, want.Err)
		}
	}
	for _, c := range data.URICases {
		if got := js.EncodeURIComponent(c.S); got != c.EncC {
			t.Errorf("EncodeURIComponent(%q) = %q, want %q", c.S, got, c.EncC)
		}
		if got := js.EncodeURI(c.S); got != c.Enc {
			t.Errorf("EncodeURI(%q) = %q, want %q", c.S, got, c.Enc)
		}
		got, err := js.DecodeURIComponent(c.S)
		check("DecodeURIComponent", c.S, got, err, c.DecC)
		got, err = js.DecodeURI(c.S)
		check("DecodeURI", c.S, got, err, c.Dec)
	}
}

func TestVectors_TextDecoder(t *testing.T) {
	var data struct {
		DecoderCases []struct {
			Bytes  []int
			Whole  string
			Ignore string
			Fatal  struct {
				Ok   *string
				Err  string
				Code string
			}
			Chunked [][3]json.RawMessage
		}
		Reuse []string
	}
	loadVectors(t, "decoder.json", &data)
	for _, c := range data.DecoderCases {
		b := make([]byte, len(c.Bytes))
		for i, v := range c.Bytes {
			b[i] = byte(v)
		}
		if got := js.DecodeUTF8(b); got != c.Whole {
			t.Errorf("DecodeUTF8(% x) = %q, want %q", b, got, c.Whole)
		}
		if got := js.NewTextDecoder(&js.TextDecoderOptions{IgnoreBOM: true}).DecodeFinal(b); got != c.Ignore {
			t.Errorf("IgnoreBOM decode(% x) = %q, want %q", b, got, c.Ignore)
		}
		got, err := js.NewTextDecoder(&js.TextDecoderOptions{Fatal: true}).DecodeChunk(b, false)
		if c.Fatal.Ok != nil {
			if err != nil || got != *c.Fatal.Ok {
				t.Errorf("fatal decode(% x) = %q, %v; want %q", b, got, err, *c.Fatal.Ok)
			}
		} else {
			var jsErr *js.JSError
			if err == nil || js.ErrorString(err) != c.Fatal.Err {
				t.Errorf("fatal decode(% x) error = %v; want %s", b, err, c.Fatal.Err)
			} else if e, ok := err.(*js.JSError); !ok || e.Code != c.Fatal.Code {
				t.Errorf("fatal decode(% x) code = %v; want %s", b, jsErr, c.Fatal.Code)
			}
		}
		for _, ch := range c.Chunked {
			i, j, want := raw[int](t, ch[0]), raw[int](t, ch[1]), raw[[]string](t, ch[2])
			d := js.NewUTF8StreamDecoder()
			parts := []string{d.Decode(b[:i]), d.Decode(b[i:j]), d.Decode(b[j:]), d.Flush()}
			if !slices.Equal(parts, want) {
				t.Errorf("stream decode(% x) split %d,%d = %q, want %q", b, i, j, parts, want)
			}
		}
	}
	d := js.NewUTF8StreamDecoder()
	reuse := []string{
		d.DecodeFinal([]byte{0xef, 0xbb, 0xbf, 0x41}),
		d.DecodeFinal([]byte{0xef, 0xbb, 0xbf, 0x42}),
		d.Decode([]byte{0xe2}),
		d.DecodeFinal([]byte{0x82, 0xac}),
	}
	if !slices.Equal(reuse, data.Reuse) {
		t.Errorf("decoder reuse = %q, want %q", reuse, data.Reuse)
	}
}

func TestVectors_Dates(t *testing.T) {
	var data struct {
		TZ              string
		DateCases       [][2]json.RawMessage
		IsoCases        [][2]json.RawMessage
		LocaleDateCases [][2]json.RawMessage
	}
	loadVectors(t, "dates.json", &data)
	loc, err := time.LoadLocation(data.TZ)
	if err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	for _, c := range data.DateCases {
		s := raw[string](t, c[0])
		want := math.NaN()
		if string(c[1]) != "null" {
			want = raw[float64](t, c[1])
		}
		if got := js.DateParse(s); !sameFloat(got, want) {
			t.Errorf("DateParse(%q) = %v, want %v", s, fmtMs(got), fmtMs(want))
		}
	}
	for _, c := range data.IsoCases {
		ms := raw[float64](t, c[0])
		want := raw[string](t, c[1])
		if got := js.ToISOString(time.UnixMilli(int64(ms))); got != want {
			t.Errorf("ToISOString(%v) = %q, want %q", ms, got, want)
		}
	}
	for _, c := range data.LocaleDateCases {
		ms := raw[float64](t, c[0])
		want := raw[string](t, c[1])
		if got := js.DateToLocaleString(time.UnixMilli(int64(ms)).In(loc)); got != want {
			t.Errorf("DateToLocaleString(%v) = %q, want %q", ms, got, want)
		}
	}
}

func fmtMs(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	return fmt.Sprintf("%.0f (%s)", v, time.UnixMilli(int64(v)).UTC().Format(time.RFC3339Nano))
}

func TestVectors_TextDecoderStreamFuzz(t *testing.T) {
	type result struct {
		Ok  *string
		Err string
	}
	var data struct {
		Cases []struct {
			Fatal     bool
			IgnoreBOM bool `json:"ignoreBOM"`
			Calls     []struct {
				Bytes  []int
				Stream bool
				Res    result
			}
		}
		FromCodePoint [][2]json.RawMessage
	}
	loadVectors(t, "decoder_stream.json", &data)
	for n, c := range data.Cases {
		d := js.NewTextDecoder(&js.TextDecoderOptions{Fatal: c.Fatal, IgnoreBOM: c.IgnoreBOM})
		for i, call := range c.Calls {
			var b []byte
			for _, v := range call.Bytes {
				b = append(b, byte(v))
			}
			got, err := d.DecodeChunk(b, call.Stream)
			if call.Res.Ok != nil {
				if err != nil || got != *call.Res.Ok {
					t.Errorf("case %d call %d decode(% x, stream=%v) = %q, %v; want %q", n, i, b, call.Stream, got, err, *call.Res.Ok)
				}
			} else if err == nil || js.ErrorString(err) != call.Res.Err {
				t.Errorf("case %d call %d decode(% x, stream=%v) = %q, %v; want error %s", n, i, b, call.Stream, got, err, call.Res.Err)
			}
		}
	}
	for _, c := range data.FromCodePoint {
		cp := raw[float64](t, c[0])
		want := raw[result](t, c[1])
		if cp != math.Trunc(cp) {
			continue // non-integer code points cannot be expressed in the Go signature
		}
		got, err := js.FromCodePoint(int(cp))
		if want.Ok != nil {
			if err != nil || got != *want.Ok {
				t.Errorf("FromCodePoint(%v) = %q, %v; want %q", cp, got, err, *want.Ok)
			}
		} else if err == nil || js.ErrorString(err) != want.Err {
			t.Errorf("FromCodePoint(%v) error = %v; want %s", cp, err, want.Err)
		}
	}
}
