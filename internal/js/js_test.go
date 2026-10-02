package js_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
)

func TestStrings_ByteAndUTF16Offsets(t *testing.T) {
	s := "a😀é日"
	// units: a(1) 😀(2) é(1) 日(1); bytes: a(1) 😀(4) é(2) 日(3)
	byteToU16 := map[int]int{-3: 0, 0: 0, 1: 1, 2: 1, 3: 1, 4: 1, 5: 3, 6: 3, 7: 4, 8: 4, 9: 4, 10: 5, 99: 5}
	for b, want := range byteToU16 {
		if got := js.ByteToU16(s, b); got != want {
			t.Errorf("ByteToU16(%q, %d) = %d, want %d", s, b, got, want)
		}
	}
	u16ToByte := map[int]int{-1: 0, 0: 0, 1: 1, 2: 1, 3: 5, 4: 7, 5: 10, 6: 10}
	for u, want := range u16ToByte {
		if got := js.U16ToByte(s, u); got != want {
			t.Errorf("U16ToByte(%q, %d) = %d, want %d", s, u, got, want)
		}
	}
}

func TestStrings_InvalidUTF8CountsOneUnitPerByte(t *testing.T) {
	s := "a\xffb\xe2\x82"
	if got := js.Len(s); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}
	if got := js.CharCodeAt(s, 1); got != 0xFFFD {
		t.Errorf("CharCodeAt(1) = %#x, want 0xfffd", got)
	}
	if got := js.Slice(s, 1, 3); got != "\xffb" {
		t.Errorf("Slice(1, 3) = %q", got)
	}
	if got := js.IndexOf(s, "b", 0); got != 2 {
		t.Errorf("IndexOf(b) = %d, want 2", got)
	}
	if got := js.IndexOf("x€", "\x82", 0); got != -1 {
		t.Errorf("IndexOf must not match inside a code point, got %d", got)
	}
	if got := js.LastIndexOf(s, "a"); got != 0 {
		t.Errorf("LastIndexOf(a) = %d, want 0", got)
	}
	if got := js.CompareUTF16("\xff", "\uFFFD"); got != 0 {
		t.Errorf("CompareUTF16(invalid byte, U+FFFD) = %d, want 0", got)
	}
	if got := js.ToUTF16("\xff"); len(got) != 1 || got[0] != 0xFFFD {
		t.Errorf("ToUTF16(invalid) = %v", got)
	}
}

func TestStrings_CompareUTF16OrdersBySurrogates(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "ab", -1},
		{"\uFFFF", "😀", 1}, // 0xFFFF > 0xD83D
		{"\uE000", "😀", 1},
		{"\uD7FF", "😀", -1},
		{"😀", "😁", -1},
		{"x😀", "x😀", 0},
	}
	for _, c := range cases {
		if got := js.CompareUTF16(c.a, c.b); got != c.want {
			t.Errorf("CompareUTF16(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestStrings_SplitLimitAndFromCharCode(t *testing.T) {
	if got := js.SplitLimit("a,b,c", ",", 2); strings.Join(got, "|") != "a|b" {
		t.Errorf("SplitLimit = %q", got)
	}
	if got := js.SplitLimit("a,b,c", ",", 0); len(got) != 0 {
		t.Errorf("SplitLimit(0) = %q", got)
	}
	if got := js.Split("", ","); len(got) != 1 || got[0] != "" {
		t.Errorf(`Split("", ",") = %q`, got)
	}
	if got := js.Split("", ""); len(got) != 0 {
		t.Errorf(`Split("", "") = %q`, got)
	}
	if got := js.FromCharCode(0xD83D, 0xDE00, 0x41, 0xD800, 0x10041); got != "😀A\uFFFDA" {
		t.Errorf("FromCharCode = %q", got)
	}
}

func TestStrings_WhitespaceClassMatchesIsJSWhitespace(t *testing.T) {
	re := regexp.MustCompile(`^[` + js.WhitespaceClass + `]$`)
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if got, want := re.MatchString(string(r)), js.IsJSWhitespace(r); got != want {
			t.Fatalf("U+%04X: class match %v, IsJSWhitespace %v", r, got, want)
		}
	}
	// JS \s: [\t\n\v\f\r \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]
	for _, r := range []rune{'\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2000, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF} {
		if !js.IsJSWhitespace(r) {
			t.Errorf("U+%04X should be whitespace", r)
		}
	}
	for _, r := range []rune{0x85, 0x180E, 0x200B, 0x2060, 'a'} {
		if js.IsJSWhitespace(r) {
			t.Errorf("U+%04X should not be whitespace", r)
		}
	}
}

func TestStrings_NormalizeInvalidFormPanics(t *testing.T) {
	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok || js.ErrorString(err) != "RangeError: The normalization form should be one of NFC, NFD, NFKC, NFKD." {
			t.Fatalf("recover() = %v", r)
		}
	}()
	js.Normalize("a", "nfc")
}

func TestNumber_ArrayIndex(t *testing.T) {
	cases := map[string]bool{
		"0": true, "1": true, "10": true, "4294967294": true,
		"4294967295": false, "01": false, "-1": false, "1.0": false, "": false, " 1": false, "1e3": false, "99999999999": false,
	}
	for key, want := range cases {
		if _, got := js.ArrayIndex(key); got != want {
			t.Errorf("ArrayIndex(%q) = %v, want %v", key, got, want)
		}
	}
	if n, _ := js.ArrayIndex("4294967294"); n != 4294967294 {
		t.Errorf("ArrayIndex value = %d", n)
	}
}

func TestNumber_RangeErrorsPanic(t *testing.T) {
	cases := map[string]func(){
		"RangeError: toFixed() digits argument must be between 0 and 100": func() { js.ToFixed(1, 101) },
		"RangeError: toPrecision() argument must be between 1 and 100":    func() { js.ToPrecision(1, 0) },
		"RangeError: toString() radix must be between 2 and 36":           func() { js.NumberToStringRadix(1, 37) },
	}
	for want, f := range cases {
		func() {
			defer func() {
				r := recover()
				err, ok := r.(error)
				if !ok || js.ErrorString(err) != want {
					t.Errorf("recover() = %v, want %s", r, want)
				}
			}()
			f()
		}()
	}
	if got := js.ToPrecision(math.NaN(), 0); got != "NaN" {
		t.Errorf("ToPrecision(NaN, 0) = %q (JS checks NaN before the range)", got)
	}
}

type namedErr struct{ name, msg string }

func (e namedErr) Error() string { return e.msg }
func (e namedErr) Name() string  { return e.name }

func TestErrors_ErrorString(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "undefined"},
		{errors.New("boom"), "Error: boom"},
		{errors.New(""), "Error"},
		{namedErr{"ValidationError", "bad"}, "ValidationError: bad"},
		{namedErr{"", "only message"}, "only message"},
		{namedErr{"OnlyName", ""}, "OnlyName"},
		{js.NewTypeError("x is not a function"), "TypeError: x is not a function"},
		{js.NewURIError("URI malformed"), "URIError: URI malformed"},
		{context.Canceled, "AbortError: This operation was aborted"},
		{context.DeadlineExceeded, "TimeoutError: The operation was aborted due to timeout"},
		{fmt.Errorf("wrapped: %w", context.Canceled), "Error: wrapped: context canceled"},
	}
	for _, c := range cases {
		if got := js.ErrorString(c.err); got != c.want {
			t.Errorf("ErrorString(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestSleep_Behaviour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if err := js.Sleep(context.Background(), 1500); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d != 1500*time.Millisecond {
			t.Errorf("slept %v, want 1.5s", d)
		}

		start = time.Now()
		if err := js.Sleep(nil, 0); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d != time.Millisecond {
			t.Errorf("Sleep(0) slept %v, want 1ms (setTimeout clamp)", d)
		}

		start = time.Now()
		if err := js.Sleep(context.Background(), 1<<31); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d != time.Millisecond {
			t.Errorf("Sleep(2^31) slept %v, want 1ms (setTimeout overflow)", d)
		}

		reason := errors.New("stop")
		ctx, cancel := context.WithCancelCause(context.Background())
		time.AfterFunc(200*time.Millisecond, func() { cancel(reason) })
		start = time.Now()
		if err := js.Sleep(ctx, 10_000); !errors.Is(err, reason) {
			t.Errorf("Sleep after abort = %v, want %v", err, reason)
		}
		if d := time.Since(start); d != 200*time.Millisecond {
			t.Errorf("aborted after %v, want 200ms", d)
		}
		if err := js.Sleep(ctx, 10); !errors.Is(err, reason) {
			t.Errorf("Sleep on aborted ctx = %v, want %v", err, reason)
		}

		plain, cancelPlain := context.WithCancel(context.Background())
		cancelPlain()
		if err := js.Sleep(plain, 10); !errors.Is(err, context.Canceled) {
			t.Errorf("Sleep on canceled ctx = %v, want context.Canceled", err)
		}
	})
}

func TestPlatform_KnownValues(t *testing.T) {
	known := map[string]bool{"darwin": true, "linux": true, "win32": true, "freebsd": true, "openbsd": true, "netbsd": true, "sunos": true, "aix": true, "android": true}
	if p := js.Platform(); !known[p] {
		t.Errorf("Platform() = %q", p)
	}
	knownArch := map[string]bool{"x64": true, "arm64": true, "ia32": true, "arm": true, "ppc64": true, "s390x": true, "riscv64": true, "loong64": true, "mips": true, "mipsel": true, "mips64el": true, "mips64": true}
	if a := js.Arch(); !knownArch[a] {
		t.Errorf("Arch() = %q", a)
	}
}

func TestDate_NowAndISO(t *testing.T) {
	before := time.Now().UnixMilli()
	now := js.DateNow()
	after := time.Now().UnixMilli()
	if now < before || now > after {
		t.Errorf("DateNow() = %d, want in [%d, %d]", now, before, after)
	}
	ts := time.Date(2024, 1, 2, 3, 4, 5, 678_999_999, time.FixedZone("x", 3600))
	if got := js.ToISOString(ts); got != "2024-01-02T02:04:05.678Z" {
		t.Errorf("ToISOString = %q", got)
	}
	ms := js.DateParse(js.ToISOString(ts))
	if int64(ms) != ts.UnixMilli() {
		t.Errorf("DateParse(ToISOString) = %v, want %d", ms, ts.UnixMilli())
	}
}

func TestDate_GapAndOverlapUseOffsetBeforeTransition(t *testing.T) {
	// Node 24 Date.parse with TZ set to the location. Date-time forms without
	// an offset are local time.
	cases := []struct {
		loc  string
		when string
		want float64
	}{
		{"America/New_York", "2024-03-10T01:30:00", 1710052200000},
		{"America/New_York", "2024-03-10T02:00:00", 1710054000000},
		{"America/New_York", "2024-03-10T02:30:00", 1710055800000},
		{"America/New_York", "2024-03-10T03:30:00", 1710055800000},
		{"America/New_York", "2024-11-03T01:30:00", 1730611800000},
		{"America/New_York", "2010-03-14T02:30:00", 1268551800000},
		{"Australia/Sydney", "2024-10-06T02:30:00", 1728145800000},
		{"Australia/Sydney", "2024-04-07T02:00:00", 1712415600000},
		{"Australia/Sydney", "2024-04-07T02:30:00", 1712417400000},
		{"Australia/Sydney", "2024-04-07T03:00:00", 1712422800000},
		{"Australia/Lord_Howe", "2024-10-06T02:15:00", 1728143100000},
		{"Europe/London", "2024-03-31T01:30:00", 1711848600000},
		{"Europe/London", "2024-10-27T01:30:00", 1729989000000},
		{"Pacific/Apia", "2011-12-30T00:00:00", 1325239200000},
	}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	for _, c := range cases {
		loc, err := time.LoadLocation(c.loc)
		if err != nil {
			t.Fatal(err)
		}
		time.Local = loc
		if got := js.DateParse(c.when); got != c.want {
			t.Errorf("DateParse(%s %s) = %.0f, want %.0f", c.loc, c.when, got, c.want)
		}
	}
}

func TestParseInt_LeadingZerosUseV8ChunkBoundary(t *testing.T) {
	// Node 24 parseInt on x64. Leading zeros change the 32-bit chunk split for
	// a generic radix; the bits below are the trimmed-digit result.
	cases := []struct {
		radix int
		s     string
		bits  uint64
	}{
		{3, "0" + strings.Repeat("1", 48), 0x44a0e425c56daffb},
		{3, "00" + "1010101010101010101010101010101010101010", 0x43cfa2a1cf67b5fc},
		{3, strings.Repeat("0", 8) + strings.Repeat("1", 80), 0x47cbccbc7be2c629},
		{3, "-" + strings.Repeat("0", 3) + strings.Repeat("1", 40), 0xc3d517168a4523fd},
		{6, "0" + strings.Repeat("5", 21), 0x43537be29597ffff},
		{6, "0123450123450123450123450123450", 0x44a674d53220093d},
		{36, "0" + strings.Repeat("z", 20), 0x466517168a4523fd},
		{36, "00" + strings.Repeat("z", 12), 0x43d070872e384000},
		{11, "0" + strings.Repeat("a", 30), 0x466b87c1e83d2037},
		{16, strings.Repeat("0", 3) + strings.Repeat("f", 20), 0x44f0000000000000},
		{10, strings.Repeat("0", 3) + strings.Repeat("9", 40), 0x483d6329f1c35ca5},
		{2, "0001" + strings.Repeat("0", 60), 0x43b0000000000000},
		{8, strings.Repeat("0", 5) + strings.Repeat("7", 30), 0x4590000000000000},
		{36, "000", 0},
		{7, "-000", 0x8000000000000000},
	}
	for _, c := range cases {
		got := js.ParseInt(c.s, c.radix)
		if math.Float64bits(got) != c.bits {
			t.Errorf("ParseInt(%q, %d) = %x, want %x", c.s, c.radix, math.Float64bits(got), c.bits)
		}
	}
}

func TestDecoder_DecodeUTF8ValidFastPath(t *testing.T) {
	in := []byte("plain ascii and ünïcödé")
	if got := js.DecodeUTF8(in); got != string(in) {
		t.Errorf("DecodeUTF8 = %q", got)
	}
	if !utf8.ValidString(js.DecodeUTF8([]byte{0xff, 0xfe})) {
		t.Error("DecodeUTF8 must return valid UTF-8")
	}
}
