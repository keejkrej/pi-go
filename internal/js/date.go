package js

import (
	"math"
	"strconv"
	"time"
)

// DateNow returns Date.now(): milliseconds since the Unix epoch.
func DateNow() int64 {
	return time.Now().UnixMilli()
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// ToISOString returns date.toISOString(): UTC, millisecond precision
// (truncated), "YYYY-MM-DDTHH:mm:ss.sssZ", with the "±YYYYYY" extended year
// outside 0..9999.
func ToISOString(t time.Time) string {
	t = t.UTC()
	y := t.Year()
	var year string
	switch {
	case y >= 0 && y <= 9999:
		year = PadStart(strconv.Itoa(y), 4, "0")
	case y < 0:
		year = "-" + PadStart(strconv.Itoa(-y), 6, "0")
	default:
		year = "+" + PadStart(strconv.Itoa(y), 6, "0")
	}
	ms := t.Nanosecond() / 1e6
	return year + "-" + pad2(int(t.Month())) + "-" + pad2(t.Day()) + "T" + pad2(t.Hour()) + ":" +
		pad2(t.Minute()) + ":" + pad2(t.Second()) + "." + PadStart(strconv.Itoa(ms), 3, "0") + "Z"
}

// DateToLocaleString returns date.toLocaleString() for Node's default en-US
// locale, in t's location: "M/D/YYYY, h:mm:ss AM".
func DateToLocaleString(t time.Time) string {
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	ampm := "AM"
	if t.Hour() >= 12 {
		ampm = "PM"
	}
	return strconv.Itoa(int(t.Month())) + "/" + strconv.Itoa(t.Day()) + "/" + strconv.Itoa(t.Year()) + ", " +
		strconv.Itoa(h) + ":" + pad2(t.Minute()) + ":" + pad2(t.Second()) + " " + ampm
}

// DateParse returns Date.parse(s) (also new Date(s).getTime()): epoch
// milliseconds, or NaN when V8 cannot parse s. It is a port of V8's
// DateParser: ES date-time strings first ("2024-01-02T03:04:05.678Z";
// date-only forms are UTC, date-time forms without an offset are local time),
// then V8's legacy fallback ("Wed, 21 Oct 2015 07:28:00 GMT", "Oct 21 2015",
// "2015/10/21 07:28", ...). Local time uses time.Local.
func DateParse(s string) float64 {
	var p dateParser
	p.in.units = ToUTF16(s)
	p.in.next()
	p.tok.in = &p.in
	p.tok.nextTok = p.tok.scan()
	out, ok := p.parse()
	if !ok {
		return math.NaN()
	}
	day := makeDay(out.year, out.month, out.day)
	tm := float64(out.hour)*3600000 + float64(out.minute)*60000 + float64(out.second)*1000 + float64(out.millisecond)
	date := day*86400000 + tm
	if math.IsNaN(date) {
		return math.NaN()
	}
	if !out.hasOffset {
		const maxBeforeUTC = 8.64e15 + 30*86400000
		if date < -maxBeforeUTC || date > maxBeforeUTC {
			return math.NaN()
		}
		date = localToUTC(int64(date))
	} else {
		date -= float64(out.utcOffset) * 1000
		if date < -8.64e15 || date > 8.64e15 {
			return math.NaN()
		}
	}
	return timeClip(date)
}

func timeClip(t float64) float64 {
	if math.IsNaN(t) || math.Abs(t) > 8.64e15 {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

// localToUTC interprets ms as local wall-clock time and returns epoch ms.
func localToUTC(ms int64) float64 {
	w := time.UnixMilli(ms).UTC()
	l := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), time.Local)
	return float64(l.UnixMilli())
}

// makeDay is V8's MakeDay (month is 0-based).
func makeDay(year, month, date int) float64 {
	const minYear, maxYear = -1000000, 1000000
	const minMonth, maxMonth = -10000000, 10000000
	if year < minYear || year > maxYear || month < minMonth || month > maxMonth {
		return math.NaN()
	}
	y := year + month/12
	m := month % 12
	if m < 0 {
		m += 12
		y--
	}
	const yearDelta = 399999
	base := 365*(1970+yearDelta) + (1970+yearDelta)/4 - (1970+yearDelta)/100 + (1970+yearDelta)/400
	dayFromYear := 365*(y+yearDelta) + (y+yearDelta)/4 - (y+yearDelta)/100 + (y+yearDelta)/400 - base
	if y%4 != 0 || (y%100 == 0 && y%400 != 0) {
		dayFromYear += [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}[m]
	} else {
		dayFromYear += [12]int{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}[m]
	}
	return float64(dayFromYear-1) + float64(date)
}

// ---- V8 DateParser port (src/date/dateparser*.{h,cc}) ----

const dpNone = math.MaxInt32

// dpMaxSignificantDigits caps numerals at 9 significant digits.
const dpMaxSignificantDigits = 9

type dpReader struct {
	units []uint16
	index int
	ch    uint32
}

func (r *dpReader) next() {
	if r.index < len(r.units) {
		r.ch = uint32(r.units[r.index])
	} else {
		r.ch = 0
	}
	r.index++
}

func (r *dpReader) position() int { return r.index }
func (r *dpReader) isEnd() bool   { return r.ch == 0 }
func (r *dpReader) isAsciiDigit() bool {
	return r.ch >= '0' && r.ch <= '9'
}
func (r *dpReader) isAsciiAlphaOrAbove() bool { return r.ch >= 'A' }
func (r *dpReader) isWhiteSpaceChar() bool    { return dpIsWhiteSpace(r.ch) }

func (r *dpReader) skip(c uint32) bool {
	if r.ch == c {
		r.next()
		return true
	}
	return false
}

func (r *dpReader) readUnsignedNumeral() int {
	n := 0
	i := 0
	for r.ch == '0' {
		r.next()
	}
	for r.isAsciiDigit() {
		if i < dpMaxSignificantDigits {
			n = n*10 + int(r.ch-'0')
		}
		i++
		r.next()
	}
	return n
}

func (r *dpReader) readWord(prefix *[3]uint32) int {
	length := 0
	for ; r.isAsciiAlphaOrAbove() && !r.isWhiteSpaceChar(); r.next() {
		if length < len(prefix) {
			prefix[length] = r.ch | 0x20
		}
		length++
	}
	for i := length; i < len(prefix); i++ {
		prefix[i] = 0
	}
	return length
}

func (r *dpReader) skipWhiteSpace() bool {
	if dpIsWhiteSpace(r.ch) || dpIsLineTerminator(r.ch) {
		r.next()
		return true
	}
	return false
}

func (r *dpReader) skipParentheses() bool {
	if r.ch != '(' {
		return false
	}
	balance := 0
	for {
		if r.ch == ')' {
			balance--
		} else if r.ch == '(' {
			balance++
		}
		r.next()
		if !(balance > 0 && r.ch != 0) {
			break
		}
	}
	return true
}

// dpIsWhiteSpace is V8's IsWhiteSpace: JS WhiteSpace without line terminators.
func dpIsWhiteSpace(c uint32) bool {
	return c <= 0xFFFF && IsJSWhitespace(rune(c)) && !dpIsLineTerminator(c)
}

func dpIsLineTerminator(c uint32) bool {
	return c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029
}

type dpKeywordType int

const (
	dpInvalid dpKeywordType = iota
	dpMonthName
	dpTimeZoneName
	dpTimeSeparator
	dpAmPm
)

type dpKeyword struct {
	prefix [3]uint32
	typ    dpKeywordType
	value  int
}

var dpKeywords = []dpKeyword{
	{[3]uint32{'j', 'a', 'n'}, dpMonthName, 1},
	{[3]uint32{'f', 'e', 'b'}, dpMonthName, 2},
	{[3]uint32{'m', 'a', 'r'}, dpMonthName, 3},
	{[3]uint32{'a', 'p', 'r'}, dpMonthName, 4},
	{[3]uint32{'m', 'a', 'y'}, dpMonthName, 5},
	{[3]uint32{'j', 'u', 'n'}, dpMonthName, 6},
	{[3]uint32{'j', 'u', 'l'}, dpMonthName, 7},
	{[3]uint32{'a', 'u', 'g'}, dpMonthName, 8},
	{[3]uint32{'s', 'e', 'p'}, dpMonthName, 9},
	{[3]uint32{'o', 'c', 't'}, dpMonthName, 10},
	{[3]uint32{'n', 'o', 'v'}, dpMonthName, 11},
	{[3]uint32{'d', 'e', 'c'}, dpMonthName, 12},
	{[3]uint32{'a', 'm', 0}, dpAmPm, 0},
	{[3]uint32{'p', 'm', 0}, dpAmPm, 12},
	{[3]uint32{'u', 't', 0}, dpTimeZoneName, 0},
	{[3]uint32{'u', 't', 'c'}, dpTimeZoneName, 0},
	{[3]uint32{'z', 0, 0}, dpTimeZoneName, 0},
	{[3]uint32{'g', 'm', 't'}, dpTimeZoneName, 0},
	{[3]uint32{'c', 'd', 't'}, dpTimeZoneName, -5},
	{[3]uint32{'c', 's', 't'}, dpTimeZoneName, -6},
	{[3]uint32{'e', 'd', 't'}, dpTimeZoneName, -4},
	{[3]uint32{'e', 's', 't'}, dpTimeZoneName, -5},
	{[3]uint32{'m', 'd', 't'}, dpTimeZoneName, -6},
	{[3]uint32{'m', 's', 't'}, dpTimeZoneName, -7},
	{[3]uint32{'p', 'd', 't'}, dpTimeZoneName, -7},
	{[3]uint32{'p', 's', 't'}, dpTimeZoneName, -8},
	{[3]uint32{'t', 0, 0}, dpTimeSeparator, 0},
}

func dpLookupKeyword(prefix [3]uint32, length int) (dpKeywordType, int) {
	for _, k := range dpKeywords {
		if k.prefix == prefix && (length <= 3 || k.typ == dpMonthName) {
			return k.typ, k.value
		}
	}
	return dpInvalid, 0
}

type dpTag int

const (
	dpTagInvalid dpTag = iota - 6
	dpTagUnknown
	dpTagWhiteSpace
	dpTagNumber
	dpTagSymbol
	dpTagEndOfInput
	dpTagKeyword // keyword tokens carry their dpKeywordType in kw
)

type dpToken struct {
	tag    dpTag
	kw     dpKeywordType
	length int
	value  int
}

func (t dpToken) isInvalid() bool    { return t.tag == dpTagInvalid }
func (t dpToken) isNumber() bool     { return t.tag == dpTagNumber }
func (t dpToken) isWhiteSpace() bool { return t.tag == dpTagWhiteSpace }
func (t dpToken) isEndOfInput() bool { return t.tag == dpTagEndOfInput }
func (t dpToken) isKeyword() bool    { return t.tag == dpTagKeyword }
func (t dpToken) isSymbol(c int) bool {
	return t.tag == dpTagSymbol && t.value == c
}
func (t dpToken) isKeywordType(k dpKeywordType) bool {
	return t.tag == dpTagKeyword && t.kw == k
}
func (t dpToken) isFixedLengthNumber(n int) bool {
	return t.tag == dpTagNumber && t.length == n
}
func (t dpToken) isAsciiSign() bool {
	return t.tag == dpTagSymbol && (t.value == '-' || t.value == '+')
}
func (t dpToken) asciiSign() int { return 44 - t.value }
func (t dpToken) isKeywordZ() bool {
	return t.tag == dpTagKeyword && t.kw == dpTimeZoneName && t.length == 1 && t.value == 0
}

type dpTokenizer struct {
	in      *dpReader
	nextTok dpToken
}

func (z *dpTokenizer) next() dpToken {
	t := z.nextTok
	z.nextTok = z.scan()
	return t
}

func (z *dpTokenizer) peek() dpToken { return z.nextTok }

func (z *dpTokenizer) skipSymbol(c int) bool {
	if z.nextTok.isSymbol(c) {
		z.nextTok = z.scan()
		return true
	}
	return false
}

func (z *dpTokenizer) scan() dpToken {
	in := z.in
	pre := in.position()
	if in.isEnd() {
		return dpToken{tag: dpTagEndOfInput, value: -1}
	}
	if in.isAsciiDigit() {
		n := in.readUnsignedNumeral()
		return dpToken{tag: dpTagNumber, length: in.position() - pre, value: n}
	}
	for _, c := range []uint32{':', '-', '+', '.', ')'} {
		if in.skip(c) {
			return dpToken{tag: dpTagSymbol, length: 1, value: int(c)}
		}
	}
	if in.isAsciiAlphaOrAbove() && !in.isWhiteSpaceChar() {
		var prefix [3]uint32
		length := in.readWord(&prefix)
		typ, value := dpLookupKeyword(prefix, length)
		return dpToken{tag: dpTagKeyword, kw: typ, length: length, value: value}
	}
	if in.skipWhiteSpace() {
		return dpToken{tag: dpTagWhiteSpace, length: in.position() - pre}
	}
	if in.skipParentheses() {
		return dpToken{tag: dpTagUnknown, length: 1, value: -1}
	}
	in.next()
	return dpToken{tag: dpTagUnknown, length: 1, value: -1}
}

type dpTimeZone struct {
	sign, hour, minute int
}

func newDpTimeZone() dpTimeZone { return dpTimeZone{dpNone, dpNone, dpNone} }

func (z *dpTimeZone) set(offsetHours int) {
	if offsetHours < 0 {
		z.sign = -1
	} else {
		z.sign = 1
	}
	z.hour = offsetHours * z.sign
	z.minute = 0
}

func (z *dpTimeZone) setSign(sign int) {
	if sign < 0 {
		z.sign = -1
	} else {
		z.sign = 1
	}
}

func (z *dpTimeZone) isExpecting(n int) bool {
	return z.hour != dpNone && z.minute == dpNone && dpIsMinute(n)
}
func (z *dpTimeZone) isUTC() bool   { return z.hour == 0 && z.minute == 0 }
func (z *dpTimeZone) isEmpty() bool { return z.hour == dpNone }

func (z *dpTimeZone) write(out *dpOutput) bool {
	if z.sign != dpNone {
		if z.hour == dpNone {
			z.hour = 0
		}
		if z.minute == dpNone {
			z.minute = 0
		}
		// V8 computes in unsigned 32-bit arithmetic and rejects totals above
		// Smi::kMaxValue (2^31-1 on Node's 64-bit builds).
		total := uint32(z.hour)*3600 + uint32(z.minute)*60
		if total > 1<<31-1 {
			return false
		}
		seconds := int(total)
		if z.sign < 0 {
			seconds = -seconds
		}
		out.utcOffset = seconds
		out.hasOffset = true
	} else {
		out.hasOffset = false
	}
	return true
}

type dpTime struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *dpTime) isEmpty() bool { return t.index == 0 }
func (t *dpTime) isExpecting(n int) bool {
	return (t.index == 1 && dpIsMinute(n)) || (t.index == 2 && dpIsSecond(n)) || (t.index == 3 && dpIsMillisecond(n))
}
func (t *dpTime) add(n int) bool {
	if t.index < len(t.comp) {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}
func (t *dpTime) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *dpTime) write(out *dpOutput) bool {
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	hour, minute, second, ms := t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dpNone {
		if !dpBetween(hour, 0, 12) {
			return false
		}
		hour %= 12
		hour += t.hourOffset
	}
	if !dpIsHour(hour) || !dpIsMinute(minute) || !dpIsSecond(second) || !dpIsMillisecond(ms) {
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return false
		}
	}
	out.hour, out.minute, out.second, out.millisecond = hour, minute, second, ms
	return true
}

type dpDay struct {
	comp       [3]int
	index      int
	namedMonth int
	isoDate    bool
}

func (d *dpDay) isEmpty() bool { return d.index == 0 }
func (d *dpDay) add(n int) bool {
	if d.index < len(d.comp) {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func (d *dpDay) write(out *dpOutput) bool {
	if d.index < 1 {
		return false
	}
	for d.index < len(d.comp) {
		d.comp[d.index] = 1
		d.index++
	}
	year := 0
	month, day := dpNone, dpNone
	if d.namedMonth == dpNone {
		if d.isoDate || (d.index == 3 && !dpIsDay(d.comp[0])) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day = d.comp[0], d.comp[1]
			if d.index == 3 {
				year = d.comp[2]
			}
		}
	} else {
		month = d.namedMonth
		if d.index == 1 {
			day = d.comp[0]
		} else if !dpIsDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.isoDate {
		if dpBetween(year, 0, 49) {
			year += 2000
		} else if dpBetween(year, 50, 99) {
			year += 1900
		}
	}
	if year < -(1<<31) || year > 1<<31-1 || !dpIsMonth(month) || !dpIsDay(day) {
		return false
	}
	out.year, out.month, out.day = year, month-1, day
	return true
}

func dpBetween(x, lo, hi int) bool { return x >= lo && x <= hi }
func dpIsMinute(x int) bool        { return dpBetween(x, 0, 59) }
func dpIsHour(x int) bool          { return dpBetween(x, 0, 23) }
func dpIsSecond(x int) bool        { return dpBetween(x, 0, 59) }
func dpIsMillisecond(x int) bool   { return dpBetween(x, 0, 999) }
func dpIsMonth(x int) bool         { return dpBetween(x, 1, 12) }
func dpIsDay(x int) bool           { return dpBetween(x, 1, 31) }

type dpOutput struct {
	year, month, day                  int
	hour, minute, second, millisecond int
	utcOffset                         int
	hasOffset                         bool
}

type dateParser struct {
	in  dpReader
	tok dpTokenizer
}

func dpReadMilliseconds(t dpToken) int {
	number := t.value
	length := t.length
	if length < 3 {
		if length == 1 {
			number *= 100
		} else if length == 2 {
			number *= 10
		}
	} else if length > 3 {
		if length > dpMaxSignificantDigits {
			length = dpMaxSignificantDigits
		}
		factor := 1
		for {
			factor *= 10
			length--
			if length <= 3 {
				break
			}
		}
		number /= factor
	}
	return number
}

// parseES5 is V8's ParseES5DateTime. It returns the first token the legacy
// parser must handle, an end-of-input token on full success, or an invalid
// token.
func (p *dateParser) parseES5(day *dpDay, tm *dpTime, tz *dpTimeZone) dpToken {
	sc := &p.tok
	if sc.peek().isAsciiSign() {
		signToken := sc.next()
		if !sc.peek().isFixedLengthNumber(6) {
			return signToken
		}
		sign := signToken.asciiSign()
		year := sc.next().value
		if sign < 0 && year == 0 {
			return signToken
		}
		day.add(sign * year)
	} else if sc.peek().isFixedLengthNumber(4) {
		day.add(sc.next().value)
	} else {
		return sc.next()
	}
	if sc.skipSymbol('-') {
		if !sc.peek().isFixedLengthNumber(2) || !dpIsMonth(sc.peek().value) {
			return sc.next()
		}
		day.add(sc.next().value)
		if sc.skipSymbol('-') {
			if !sc.peek().isFixedLengthNumber(2) || !dpIsDay(sc.peek().value) {
				return sc.next()
			}
			day.add(sc.next().value)
		}
	}
	if !sc.peek().isKeywordType(dpTimeSeparator) {
		if !sc.peek().isEndOfInput() {
			return sc.next()
		}
	} else {
		sc.next()
		if !sc.peek().isFixedLengthNumber(2) || !dpBetween(sc.peek().value, 0, 24) {
			return dpToken{tag: dpTagInvalid, value: -1}
		}
		hourIs24 := sc.peek().value == 24
		tm.add(sc.next().value)
		if !sc.skipSymbol(':') {
			return dpToken{tag: dpTagInvalid, value: -1}
		}
		if !sc.peek().isFixedLengthNumber(2) || !dpIsMinute(sc.peek().value) || (hourIs24 && sc.peek().value > 0) {
			return dpToken{tag: dpTagInvalid, value: -1}
		}
		tm.add(sc.next().value)
		if sc.skipSymbol(':') {
			if !sc.peek().isFixedLengthNumber(2) || !dpIsSecond(sc.peek().value) || (hourIs24 && sc.peek().value > 0) {
				return dpToken{tag: dpTagInvalid, value: -1}
			}
			tm.add(sc.next().value)
			if sc.skipSymbol('.') {
				if !sc.peek().isNumber() || (hourIs24 && sc.peek().value > 0) {
					return dpToken{tag: dpTagInvalid, value: -1}
				}
				tm.add(dpReadMilliseconds(sc.next()))
			}
		}
		if sc.peek().isKeywordZ() {
			sc.next()
			tz.set(0)
		} else if sc.peek().isSymbol('+') || sc.peek().isSymbol('-') {
			if sc.next().value == '+' {
				tz.setSign(1)
			} else {
				tz.setSign(-1)
			}
			if sc.peek().isFixedLengthNumber(4) {
				hourmin := sc.next().value
				hour, minute := hourmin/100, hourmin%100
				if !dpIsHour(hour) || !dpIsMinute(minute) {
					return dpToken{tag: dpTagInvalid, value: -1}
				}
				tz.hour = hour
				tz.minute = minute
			} else {
				if !sc.peek().isFixedLengthNumber(2) || !dpIsHour(sc.peek().value) {
					return dpToken{tag: dpTagInvalid, value: -1}
				}
				tz.hour = sc.next().value
				if !sc.skipSymbol(':') {
					return dpToken{tag: dpTagInvalid, value: -1}
				}
				if !sc.peek().isFixedLengthNumber(2) || !dpIsMinute(sc.peek().value) {
					return dpToken{tag: dpTagInvalid, value: -1}
				}
				tz.minute = sc.next().value
			}
		}
		if !sc.peek().isEndOfInput() {
			return dpToken{tag: dpTagInvalid, value: -1}
		}
	}
	if tz.isEmpty() && tm.isEmpty() {
		tz.set(0)
	}
	day.isoDate = true
	return dpToken{tag: dpTagEndOfInput, value: -1}
}

func (p *dateParser) parse() (dpOutput, bool) {
	var out dpOutput
	tz := newDpTimeZone()
	tm := dpTime{hourOffset: dpNone}
	day := dpDay{namedMonth: dpNone}
	sc := &p.tok

	token := p.parseES5(&day, &tm, &tz)
	if token.isInvalid() {
		return out, false
	}
	hasReadNumber := !day.isEmpty()
	for ; !token.isEndOfInput(); token = sc.next() {
		switch {
		case token.isNumber():
			hasReadNumber = true
			n := token.value
			if sc.skipSymbol(':') {
				if sc.skipSymbol(':') {
					if !tm.isEmpty() {
						return out, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return out, false
					}
					if sc.peek().isSymbol('.') {
						sc.next()
					}
				}
			} else if sc.skipSymbol('.') && tm.isExpecting(n) {
				tm.add(n)
				if !sc.peek().isNumber() {
					return out, false
				}
				ms := dpReadMilliseconds(sc.next())
				if ms < 0 {
					return out, false
				}
				tm.addFinal(ms)
			} else if tz.isExpecting(n) {
				tz.minute = n
			} else if tm.isExpecting(n) {
				tm.addFinal(n)
				peek := sc.peek()
				if !peek.isEndOfInput() && !peek.isWhiteSpace() && !peek.isKeywordZ() && !peek.isAsciiSign() {
					return out, false
				}
			} else {
				if !day.add(n) {
					return out, false
				}
				sc.skipSymbol('-')
			}
		case token.isKeyword():
			if token.kw == dpAmPm && !tm.isEmpty() {
				tm.hourOffset = token.value
			} else if token.kw == dpMonthName {
				day.namedMonth = token.value
				sc.skipSymbol('-')
			} else if token.kw == dpTimeZoneName && hasReadNumber {
				tz.set(token.value)
			} else {
				// Garbage words are illegal once a number has been read, and
				// must be separated from the first number.
				if hasReadNumber {
					return out, false
				}
				if sc.peek().isNumber() {
					return out, false
				}
			}
		case token.isAsciiSign() && (tz.isUTC() || !tm.isEmpty()):
			tz.setSign(token.asciiSign())
			n, length := 0, 0
			if sc.peek().isNumber() {
				t := sc.next()
				length = t.length
				n = t.value
			}
			hasReadNumber = true
			if sc.peek().isSymbol(':') {
				tz.hour = n
				tz.minute = dpNone
			} else if length == 2 || length == 1 {
				tz.hour = n
				tz.minute = 0
			} else if length == 4 || length == 3 {
				tz.hour = n / 100
				tz.minute = n % 100
			} else {
				return out, false
			}
		case (token.isAsciiSign() || token.isSymbol(')')) && hasReadNumber:
			return out, false
		}
	}
	ok := day.write(&out) && tm.write(&out) && tz.write(&out)
	return out, ok
}
