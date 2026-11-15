// Ported from packages/protocol/test/cbor/cbor.test.ts (pi v1.0.0).

package protocol

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol/cbor"
)

func tccFromHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture %q: %v", s, err)
	}
	return b
}

func tccIntPtr(v int) *int {
	return &v
}

func TestCborCodec(t *testing.T) {
	negZero := math.Copysign(0, -1)
	knownVectors := []struct {
		name  string
		value any
		wire  string
	}{
		{"null", nil, "f6"},
		{"false", false, "f4"},
		{"true", true, "f5"},
		{"0", float64(0), "00"},
		{"1", float64(1), "01"},
		{"10", float64(10), "0a"},
		{"23", float64(23), "17"},
		{"24", float64(24), "1818"},
		{"25", float64(25), "1819"},
		{"100", float64(100), "1864"},
		{"1000", float64(1000), "1903e8"},
		{"1000000", float64(1_000_000), "1a000f4240"},
		{"1000000000000", float64(1_000_000_000_000), "1b000000e8d4a51000"},
		{"max_safe_int", float64(9007199254740991), "1b001fffffffffffff"},
		{"-1", float64(-1), "20"},
		{"-10", float64(-10), "29"},
		{"-24", float64(-24), "37"},
		{"-25", float64(-25), "3818"},
		{"-100", float64(-100), "3863"},
		{"-1000", float64(-1000), "3903e7"},
		{"-1000000", float64(-1_000_000), "3a000f423f"},
		{"min_safe_int", float64(-9007199254740991), "3b001ffffffffffffe"},
		{"1.1", 1.1, "fb3ff199999999999a"},
		{"-0", negZero, "fb8000000000000000"},
		{"bytes", []byte{1, 2, 3, 4}, "4401020304"},
		{"empty_string", "", "60"},
		{"ascii", "IETF", "6449455446"},
		{"umlaut", "ü", "62c3bc"},
		{"cjk", "水", "63e6b0b4"},
		{"astral", "𐅑", "64f0908591"},
		{"empty_array", []any{}, "80"},
		{"array_123", []any{float64(1), float64(2), float64(3)}, "83010203"},
		{"nested_array", []any{float64(1), []any{float64(2), float64(3)}, []any{float64(4), float64(5)}}, "8301820203820405"},
		{"map", jsonx.ObjectOf("a", float64(1), "b", []any{float64(2), float64(3)}), "a26161016162820203"},
	}

	for _, tc := range knownVectors {
		t.Run("vector_"+tc.name, func(t *testing.T) {
			encoded, err := cbor.EncodeCbor(tc.value, nil)
			if err != nil {
				t.Fatalf("EncodeCbor failed: %v", err)
			}
			gotHex := hex.EncodeToString(encoded)
			if gotHex != tc.wire {
				t.Fatalf("got hex %s, want %s", gotHex, tc.wire)
			}
			decoded, err := cbor.DecodeCbor(tccFromHex(t, tc.wire), nil)
			if err != nil {
				t.Fatalf("DecodeCbor failed: %v", err)
			}
			if tc.name == "-0" {
				f, ok := decoded.(float64)
				if !ok || f != 0 || !math.Signbit(f) {
					t.Fatalf("expected -0 float64, got %#v", decoded)
				}
				return
			}
			if b, ok := tc.value.([]byte); ok {
				db, dok := decoded.([]byte)
				if !dok || !bytes.Equal(db, b) {
					t.Fatalf("got %#v, want %#v", decoded, b)
				}
				return
			}
			if !jsonx.DeepEqual(decoded, tc.value) {
				t.Fatalf("got %#v, want %#v", decoded, tc.value)
			}
		})
	}

	t.Run("preserves falsey values and leading BOM and __proto__", func(t *testing.T) {
		val := jsonx.ObjectOf("zero", float64(0), "empty", "", "no", false, "nil", nil)
		enc, err := cbor.EncodeCbor(val, nil)
		if err != nil {
			t.Fatalf("EncodeCbor failed: %v", err)
		}
		dec, err := cbor.DecodeCbor(enc, nil)
		if err != nil {
			t.Fatalf("DecodeCbor failed: %v", err)
		}
		if !jsonx.DeepEqual(dec, val) {
			t.Fatalf("round-trip mismatch: got %#v", dec)
		}

		bom, err := cbor.DecodeCbor(tccFromHex(t, "63efbbbf"), nil)
		if err != nil || bom != "\ufeff" {
			t.Fatalf("expected BOM string, got (%#v, %v)", bom, err)
		}

		protoObj := jsonx.ObjectOf("__proto__", "safe")
		penc, err := cbor.EncodeCbor(protoObj, nil)
		if err != nil {
			t.Fatalf("EncodeCbor protoObj failed: %v", err)
		}
		pdec, err := cbor.DecodeCbor(penc, nil)
		if err != nil {
			t.Fatalf("DecodeCbor protoObj failed: %v", err)
		}
		pobj, ok := pdec.(*jsonx.Object)
		if !ok || !pobj.Has("__proto__") {
			t.Fatalf("expected __proto__ property preserved, got %#v", pdec)
		}
		if got, _ := pobj.Get("__proto__"); got != "safe" {
			t.Fatalf("got %#v, want safe", got)
		}
	})

	t.Run("rejects unsupported encoder values", func(t *testing.T) {
		unsupported := []struct {
			label string
			val   any
		}{
			{"NaN", math.NaN()},
			{"+Inf", math.Inf(1)},
			{"-Inf", math.Inf(-1)},
			{"unsafe positive int", float64(9007199254740992)},
			{"unsafe negative int", float64(-9007199254740992)},
			{"go int", 1},
			{"struct", struct{}{}},
			{"go map", map[string]any{}},
			{"func", func() {}},
			{"nil object pointer", (*jsonx.Object)(nil)},
			{"nil any slice", []any(nil)},
		}
		for _, tc := range unsupported {
			_, err := cbor.EncodeCbor(tc.val, nil)
			var cborErr *cbor.CborError
			if !errors.As(err, &cborErr) {
				t.Fatalf("%s: expected CborError, got %v", tc.label, err)
			}
		}
	})

	t.Run("rejects lossy strings, cycles, and excessive encoder depth", func(t *testing.T) {
		if _, err := cbor.EncodeCbor("\xed\xa0\x80", nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unicode") {
			t.Fatalf("expected Unicode error for surrogate bytes, got %v", err)
		}

		cyclic := make([]any, 1)
		cyclic[0] = cyclic
		if _, err := cbor.EncodeCbor(cyclic, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cycles") {
			t.Fatalf("expected cycles error for slice, got %v", err)
		}

		cyclicObj := jsonx.NewObject()
		cyclicObj.Set("self", cyclicObj)
		if _, err := cbor.EncodeCbor(cyclicObj, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cycles") {
			t.Fatalf("expected cycles error for object, got %v", err)
		}

		var tooDeep any
		for range cbor.DefaultMaxCborDepth + 1 {
			tooDeep = []any{tooDeep}
		}
		if _, err := cbor.EncodeCbor(tooDeep, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "depth") {
			t.Fatalf("expected depth error, got %v", err)
		}
	})

	t.Run("rejects invalid decoder inputs", func(t *testing.T) {
		invalidWires := []struct {
			label string
			wire  string
		}{
			{"empty input", ""},
			{"truncated integer", "18"},
			{"reserved additional information", "1c"},
			{"indefinite byte string", "5f"},
			{"indefinite text string", "7f"},
			{"indefinite array", "9f"},
			{"indefinite map", "bf"},
			{"tag", "c000"},
			{"undefined", "f7"},
			{"unsupported simple value", "e0"},
			{"break outside an indefinite item", "ff"},
			{"float16", "f93c00"},
			{"float32", "fa3f800000"},
			{"positive infinity", "fb7ff0000000000000"},
			{"NaN", "fb7ff8000000000000"},
			{"truncated float64", "fb3ff00000"},
			{"truncated byte string", "44010203"},
			{"truncated text string", "636162"},
			{"truncated array", "8201"},
			{"truncated map", "a16161"},
			{"trailing data", "0000"},
			{"non-string map key", "a10102"},
			{"duplicate map key", "a2616101616102"},
			{"invalid UTF-8 byte", "61ff"},
			{"overlong UTF-8", "62c080"},
			{"UTF-8 surrogate", "63eda080"},
			{"unsafe positive integer", "1b0020000000000000"},
			{"unsafe negative integer", "3b001fffffffffffff"},
			{"unsafe integer encoded as float64", "fb4340000000000000"},
		}
		for _, tc := range invalidWires {
			_, err := cbor.DecodeCbor(tccFromHex(t, tc.wire), nil)
			var cborErr *cbor.CborError
			if !errors.As(err, &cborErr) {
				t.Fatalf("%s: expected CborError, got %v", tc.label, err)
			}
		}
	})

	t.Run("enforces depth and declared length limits before traversing", func(t *testing.T) {
		tooDeep := make([]byte, cbor.DefaultMaxCborDepth+2)
		for i := 0; i < len(tooDeep)-1; i++ {
			tooDeep[i] = 0x81
		}
		tooDeep[len(tooDeep)-1] = 0xf6
		if _, err := cbor.DecodeCbor(tooDeep, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "depth") {
			t.Fatalf("expected depth error, got %v", err)
		}

		oversizedBytes := tccFromHex(t, "5a01000001")
		oversizedText := tccFromHex(t, "7a01000001")
		oversizedArray := tccFromHex(t, "9a000f4241")
		oversizedMap := tccFromHex(t, "ba000f4241")
		for _, wire := range [][]byte{oversizedBytes, oversizedText, oversizedArray, oversizedMap} {
			if _, err := cbor.DecodeCbor(wire, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "limit") {
				t.Fatalf("expected limit error for %x, got %v", wire, err)
			}
		}
	})

	t.Run("supports stricter caller limits and validates options", func(t *testing.T) {
		if _, err := cbor.DecodeCbor(tccFromHex(t, "83010203"), &cbor.CborOptions{MaxContainerLength: tccIntPtr(2)}); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("expected container limit error, got %v", err)
		}
		if _, err := cbor.DecodeCbor(tccFromHex(t, "626162"), &cbor.CborOptions{MaxByteLength: tccIntPtr(2)}); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("expected byte limit error, got %v", err)
		}
		if _, err := cbor.EncodeCbor([]any{float64(1), float64(2), float64(3)}, &cbor.CborOptions{MaxContainerLength: tccIntPtr(2)}); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("expected encoder container limit error, got %v", err)
		}
		if _, err := cbor.EncodeCbor("ab", &cbor.CborOptions{MaxByteLength: tccIntPtr(2)}); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("expected encoder byte limit error, got %v", err)
		}
		if _, err := cbor.ResolveOptions(&cbor.CborOptions{MaxDepth: tccIntPtr(513)}); err == nil {
			t.Fatalf("expected RangeError for MaxDepth=513")
		} else {
			var jsErr *js.JSError
			if !errors.As(err, &jsErr) || jsErr.Name() != "RangeError" {
				t.Fatalf("expected RangeError, got %v", err)
			}
		}
	})
}
