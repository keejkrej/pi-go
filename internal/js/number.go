package js

import (
	"math"
	"math/big"
	"runtime"
	"strconv"
	"strings"
)

// shortestDigits returns the shortest round-trip decimal digits of x > 0 and
// the exponent n such that x = 0.d1d2...dk * 10^n (ES Number::toString's n).
func shortestDigits(x float64) (digits string, n int) {
	s := strconv.FormatFloat(x, 'e', -1, 64) // d.ddde±XX
	mant, exp, _ := strings.Cut(s, "e")
	e, _ := strconv.Atoi(exp)
	digits = strings.Replace(mant, ".", "", 1)
	return digits, e + 1
}

// NumberToString returns String(x) (Number::toString with radix 10).
func NumberToString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case x == 0:
		return "0"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x < 0:
		return "-" + NumberToString(-x)
	}
	digits, n := shortestDigits(x)
	k := len(digits)
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	e := n - 1
	sign := "+"
	if e < 0 {
		sign = "-"
		e = -e
	}
	if k == 1 {
		return digits + "e" + sign + strconv.Itoa(e)
	}
	return digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
}

const radixDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// NumberToStringRadix returns x.toString(radix) for radix 2..36, using V8's
// DoubleToRadixCString algorithm for non-decimal radices. A radix outside
// 2..36 is a programmer error (JS throws a RangeError) and panics.
func NumberToStringRadix(x float64, radix int) string {
	if radix < 2 || radix > 36 {
		panic(NewRangeError("toString() radix must be between 2 and 36"))
	}
	if radix == 10 {
		return NumberToString(x)
	}
	switch {
	case math.IsNaN(x):
		return "NaN"
	case x == 0:
		return "0"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	}
	negative := x < 0
	if negative {
		x = -x
	}
	integer := math.Floor(x)
	fraction := x - integer
	delta := 0.5 * (math.Nextafter(x, math.Inf(1)) - x)
	delta = math.Max(math.Nextafter(0, 1), delta)
	var frac []byte
	if fraction >= delta {
		frac = append(frac, '.')
		for {
			// Explicit float64 conversions keep each step rounded like V8's
			// C++ (Go may otherwise fuse multiply and subtract).
			fraction = float64(fraction * float64(radix))
			delta = float64(delta * float64(radix))
			digit := int(fraction)
			frac = append(frac, radixDigits[digit])
			fraction = float64(fraction - float64(digit))
			if fraction > 0.5 || (fraction == 0.5 && digit&1 == 1) {
				if fraction+delta > 1 {
					// Round up, propagating the carry through written digits.
					for {
						last := len(frac) - 1
						if last == 0 {
							integer++
							frac = frac[:0]
							break
						}
						c := frac[last]
						d := strings.IndexByte(radixDigits, c)
						frac = frac[:last]
						if d+1 < radix {
							frac = append(frac, radixDigits[d+1])
							break
						}
					}
					break
				}
			}
			if fraction < delta {
				break
			}
		}
		if len(frac) == 1 {
			frac = frac[:0]
		}
	}
	var intDigits []byte
	for exponentOf(integer/float64(radix)) > 0 {
		integer /= float64(radix)
		intDigits = append(intDigits, '0')
	}
	for {
		remainder := math.Mod(integer, float64(radix))
		intDigits = append(intDigits, radixDigits[int(remainder)])
		integer = (integer - remainder) / float64(radix)
		if integer <= 0 {
			break
		}
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i := len(intDigits) - 1; i >= 0; i-- {
		b.WriteByte(intDigits[i])
	}
	b.Write(frac)
	return b.String()
}

// exponentOf returns V8's Double::Exponent: e such that x = f * 2^e with a
// 53-bit integer significand f.
func exponentOf(x float64) int {
	bits := math.Float64bits(x)
	biased := int(bits>>52) & 0x7FF
	if biased == 0 {
		return -1074
	}
	return biased - 0x3FF - 52
}

// pow10Rat returns 10^n as a big.Rat (n may be negative).
func pow10Rat(n int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(n))), nil)
	if n >= 0 {
		return new(big.Rat).SetInt(p)
	}
	return new(big.Rat).SetFrac(big.NewInt(1), p)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// roundHalfUp returns the integer nearest to r >= 0, picking the larger one on
// a tie.
func roundHalfUp(r *big.Rat) *big.Int {
	twice := new(big.Rat).Mul(r, big.NewRat(2, 1))
	twice.Add(twice, big.NewRat(1, 1))
	// floor((2r + 1) / 2)
	num := new(big.Int).Set(twice.Num())
	den := new(big.Int).Mul(twice.Denom(), big.NewInt(2))
	return num.Quo(num, den)
}

// ToFixed returns x.toFixed(digits) for digits 0..100. It rounds the exact
// binary value, picking the larger magnitude on a tie (unlike strconv's
// round-half-even), and returns String(x) when |x| >= 1e21. A digits value
// outside 0..100 is a programmer error (JS throws a RangeError) and panics.
func ToFixed(x float64, digits int) string {
	if digits < 0 || digits > 100 {
		panic(NewRangeError("toFixed() digits argument must be between 0 and 100"))
	}
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.Abs(x) >= 1e21 || math.IsInf(x, 0) {
		return NumberToString(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	r := new(big.Rat).SetFloat64(x)
	r.Mul(r, pow10Rat(digits))
	m := roundHalfUp(r).String()
	if digits == 0 {
		return sign + m
	}
	if len(m) <= digits {
		m = strings.Repeat("0", digits+1-len(m)) + m
	}
	k := len(m)
	return sign + m[:k-digits] + "." + m[k-digits:]
}

// ToPrecision returns x.toPrecision(precision) for precision 1..100, rounding
// the exact binary value half up. A precision outside 1..100 is a programmer
// error (JS throws a RangeError) and panics.
func ToPrecision(x float64, precision int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.IsInf(x, 0) {
		return NumberToString(x)
	}
	if precision < 1 || precision > 100 {
		panic(NewRangeError("toPrecision() argument must be between 1 and 100"))
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	var m string
	var e int
	if x == 0 {
		m = strings.Repeat("0", precision)
		e = 0
	} else {
		r := new(big.Rat).SetFloat64(x)
		_, n := shortestDigits(x)
		e = n - 1
		// Make 10^e <= r < 10^(e+1) exact.
		for r.Cmp(pow10Rat(e)) < 0 {
			e--
		}
		for r.Cmp(pow10Rat(e+1)) >= 0 {
			e++
		}
		scaled := new(big.Rat).Mul(r, pow10Rat(precision-1-e))
		nInt := roundHalfUp(scaled)
		limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
		if nInt.Cmp(limit) >= 0 {
			nInt.Quo(nInt, big.NewInt(10))
			e++
		}
		m = nInt.String()
	}
	if e < -6 || e >= precision {
		a, b := m[:1], m[1:]
		if b != "" {
			a += "." + b
		}
		if e >= 0 {
			return sign + a + "e+" + strconv.Itoa(e)
		}
		return sign + a + "e-" + strconv.Itoa(-e)
	}
	if e == precision-1 {
		return sign + m
	}
	if e >= 0 {
		return sign + m[:e+1] + "." + m[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + m
}

// Round returns Math.round(x): the nearest integer, ties toward +Infinity,
// keeping -0 for inputs in [-0.5, -0].
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && x < 0 {
		return math.Copysign(0, -1)
	}
	return r
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

// ParseInt returns parseInt(s, radix) (also Number.parseInt). radix 0 means
// undefined: base 10, or 16 with a "0x"/"0X" prefix. It returns NaN when no
// digits parse or radix is outside 2..36.
func ParseInt(s string, radix int) float64 {
	s = TrimStart(s)
	sign := 1.0
	if s != "" && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	stripPrefix := true
	if radix != 0 {
		if radix < 2 || radix > 36 {
			return math.NaN()
		}
		if radix != 16 {
			stripPrefix = false
		}
	} else {
		radix = 10
	}
	if stripPrefix && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
		radix = 16
	}
	end := 0
	for end < len(s) && digitValue(s[end]) < radix {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	z := s[:end]
	var v float64
	switch {
	case radix == 10:
		v, _ = strconv.ParseFloat(z, 64)
	case radix&(radix-1) == 0:
		// Power-of-two radices are exact with round-half-even, as in V8.
		n, _ := new(big.Int).SetString(strings.ToLower(z), radix)
		v, _ = new(big.Float).SetInt(n).Float64()
	default:
		v = parseIntGeneric(z, radix)
	}
	return sign * v
}

// parseIntGeneric is V8's NumberParseIntHelper::HandleGenericCase: digits are
// accumulated in 32-bit parts and folded into a double, which approximates
// values above 2^53 exactly as V8 does.
func parseIntGeneric(z string, radix int) float64 {
	const maxMultiplier = 0xFFFFFFFF / 36
	result := 0.0
	i := 0
	for i < len(z) {
		part, multiplier := uint32(0), uint32(1)
		for i < len(z) {
			m := multiplier * uint32(radix)
			if m > maxMultiplier {
				break
			}
			part = part*uint32(radix) + uint32(digitValue(z[i]))
			multiplier = m
			i++
		}
		if parseIntFusedMultiplyAdd {
			result = math.FMA(result, float64(multiplier), float64(part))
		} else {
			result = float64(result*float64(multiplier)) + float64(part)
		}
	}
	return result
}

// parseIntFusedMultiplyAdd reports whether V8's `result * multiplier + part`
// is compiled to a fused multiply-add, which clang does by default on arm64
// and other targets with FMA in the base ISA, but not on x86.
var parseIntFusedMultiplyAdd = runtime.GOARCH != "amd64" && runtime.GOARCH != "386"

// scanDecimalLiteral returns the length of the longest StrDecimalLiteral
// prefix of s ([+-]? (Infinity | digits [. digits] [exp] | . digits [exp])),
// or 0 when there is none.
func scanDecimalLiteral(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if strings.HasPrefix(s[i:], "Infinity") {
		return i + len("Infinity")
	}
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intDigits := i - intStart
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		fracDigits = j - i - 1
		if intDigits > 0 || fracDigits > 0 {
			i = j
		}
	}
	if intDigits == 0 && fracDigits == 0 {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		expStart := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > expStart {
			i = j
		}
	}
	return i
}

func parseDecimalLiteral(lit string) float64 {
	switch lit {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	v, _ := strconv.ParseFloat(lit, 64)
	return v
}

// ParseFloat returns parseFloat(s) (also Number.parseFloat): the longest
// decimal-literal prefix after leading whitespace, or NaN.
func ParseFloat(s string) float64 {
	s = TrimStart(s)
	n := scanDecimalLiteral(s)
	if n == 0 {
		return math.NaN()
	}
	return parseDecimalLiteral(s[:n])
}

// ToNumber returns Number(s) (also unary +s) for a string: surrounding
// whitespace is ignored, "" is 0, "0x"/"0o"/"0b" prefixes are allowed without a
// sign, and anything else that is not a complete decimal literal is NaN.
func ToNumber(s string) float64 {
	s = Trim(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		radix := 0
		switch s[1] {
		case 'x', 'X':
			radix = 16
		case 'o', 'O':
			radix = 8
		case 'b', 'B':
			radix = 2
		}
		if radix != 0 {
			digits := s[2:]
			for i := 0; i < len(digits); i++ {
				if digitValue(digits[i]) >= radix {
					return math.NaN()
				}
			}
			n, _ := new(big.Int).SetString(strings.ToLower(digits), radix)
			v, _ := new(big.Float).SetInt(n).Float64()
			return v
		}
	}
	n := scanDecimalLiteral(s)
	if n != len(s) {
		return math.NaN()
	}
	return parseDecimalLiteral(s)
}

// ArrayIndex reports whether key is a canonical array index ("0" ..
// "4294967294", no leading zeros), the keys JS orders first, ascending, in
// object property order, and returns its value.
func ArrayIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	if n > 4294967294 {
		return 0, false
	}
	return uint32(n), true
}

// FormatNumberEnUS returns x.toLocaleString("en-US", {minimumFractionDigits:
// minFrac, maximumFractionDigits: maxFrac}): "," grouping, "." decimal point,
// rounding half away from zero on the shortest decimal form of x (as ICU
// does), "-" for negative values including -0, "∞" and "NaN". Plain
// toLocaleString() is FormatNumberEnUS(x, 0, 3).
func FormatNumberEnUS(x float64, minFrac, maxFrac int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	sign := ""
	if math.Signbit(x) {
		sign = "-"
		x = -x
	}
	if math.IsInf(x, 0) {
		return sign + "∞"
	}
	if maxFrac < minFrac {
		maxFrac = minFrac
	}
	var intPart, fracPart string
	if x == 0 {
		intPart = "0"
	} else {
		digits, n := shortestDigits(x)
		// Decimal digits with the point after position n (n may be <= 0 or > len).
		var whole, frac string
		switch {
		case n <= 0:
			whole = "0"
			frac = strings.Repeat("0", -n) + digits
		case n >= len(digits):
			whole = digits + strings.Repeat("0", n-len(digits))
		default:
			whole = digits[:n]
			frac = digits[n:]
		}
		if len(frac) > maxFrac {
			roundUp := frac[maxFrac] >= '5'
			frac = frac[:maxFrac]
			if roundUp {
				whole, frac = incrementDecimal(whole, frac)
			}
		}
		intPart = strings.TrimLeft(whole, "0")
		if intPart == "" {
			intPart = "0"
		}
		fracPart = frac
	}
	fracPart = strings.TrimRight(fracPart, "0")
	if len(fracPart) < minFrac {
		fracPart += strings.Repeat("0", minFrac-len(fracPart))
	}
	var b strings.Builder
	b.WriteString(sign)
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(intPart[i])
	}
	if fracPart != "" {
		b.WriteByte('.')
		b.WriteString(fracPart)
	}
	return b.String()
}

// incrementDecimal adds one unit in the last place of whole.frac.
func incrementDecimal(whole, frac string) (string, string) {
	digits := []byte(whole + frac)
	i := len(digits) - 1
	for ; i >= 0; i-- {
		if digits[i] == '9' {
			digits[i] = '0'
			continue
		}
		digits[i]++
		break
	}
	s := string(digits)
	if i < 0 {
		s = "1" + s
	}
	cut := len(s) - len(frac)
	return s[:cut], s[cut:]
}
