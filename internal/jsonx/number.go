package jsonx

import (
	"math"
	"strconv"
)

// appendNumber appends x as JSON.stringify writes a number: ES Number::toString for
// finite values (-0 becomes "0"), and "null" for NaN and ±Infinity.
func appendNumber(dst []byte, x float64) []byte {
	return appendFloat(dst, x, 64)
}

// appendFloat formats x with the ES Number::toString rules using the shortest decimal
// representation that round-trips at the given bit size.
func appendFloat(dst []byte, x float64, bits int) []byte {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return append(dst, "null"...)
	}
	if x == 0 {
		return append(dst, '0')
	}
	abs := math.Abs(x)
	if bits == 64 && abs < 1<<53 && x == math.Trunc(x) {
		return strconv.AppendInt(dst, int64(x), 10)
	}
	// ES: fixed notation for 1e-7 < |x| < 1e21, exponent notation otherwise.
	format := byte('f')
	if bits == 64 {
		if abs < 1e-6 || abs >= 1e21 {
			format = 'e'
		}
	} else if float32(abs) < 1e-6 || float32(abs) >= 1e21 {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, x, format, -1, bits)
	if format == 'e' {
		// Go writes e-07; ES writes e-7.
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}

// maxExactInt is 2^53: every integer of smaller magnitude is exactly a double.
const maxExactInt = 1 << 53

// appendInt writes a Go integer as the JS number it stands for: exact up to 2^53, the
// Number::toString text of the nearest double beyond (9007199254740993 is written as
// 9007199254740992, math.MaxInt64 as 9223372036854776000).
func appendInt(dst []byte, n int64) []byte {
	if n >= -maxExactInt && n <= maxExactInt {
		return strconv.AppendInt(dst, n, 10)
	}
	return appendNumber(dst, float64(n))
}

// appendUint is appendInt for unsigned integers.
func appendUint(dst []byte, n uint64) []byte {
	if n <= maxExactInt {
		return strconv.AppendUint(dst, n, 10)
	}
	return appendNumber(dst, float64(n))
}

// formatNumber returns String(x) for finite x and "null" otherwise (JSON number text).
func formatNumber(x float64) string {
	return string(appendNumber(nil, x))
}

// FormatNumber returns the text JSON.stringify writes for the number x.
func FormatNumber(x float64) string { return formatNumber(x) }
