package partialjson

import (
	"errors"
	"math"
	"strconv"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Allow controls what types may be partially parsed (partial-json options.ts). Combine
// the constants with bitwise OR, or remove one with &^ (TS: `~Allow.OBJ`).
//
// Sometimes you don't allow every type to be partially parsed. For example, you may not
// want a partial number because it may increase its size gradually before it's complete.
// The default in TS is ALL, which in most cases is the best option.
type Allow int

const (
	// STR allows partial strings like `"hello \u12` to be parsed as `"hello "`.
	STR Allow = 0b000000001
	// NUM allows partial numbers like `123.` to be parsed as `123`.
	NUM Allow = 0b000000010
	// ARR allows partial arrays like `[1, 2,` to be parsed as `[1, 2]`.
	ARR Allow = 0b000000100
	// OBJ allows partial objects like `{"a": 1, "b":` to be parsed as `{"a": 1}`.
	OBJ Allow = 0b000001000
	// NULL allows `nu` to be parsed as `null`.
	NULL Allow = 0b000010000
	// BOOL allows `tr` to be parsed as `true`, and `fa` to be parsed as `false`.
	BOOL Allow = 0b000100000
	// NAN allows `Na` to be parsed as `NaN`.
	NAN Allow = 0b001000000
	// INFINITY allows `Inf` to be parsed as `Infinity`.
	INFINITY Allow = 0b010000000
	// Infinity is TS `_INFINITY`: it allows `-Inf` to be parsed as `-Infinity`.
	Infinity Allow = 0b100000000
	// INF is INFINITY | Infinity (both signs).
	INF = INFINITY | Infinity
	// SPECIAL is NULL | BOOL | INF | NAN.
	SPECIAL = NULL | BOOL | INF | NAN
	// ATOM is STR | NUM | SPECIAL.
	ATOM = STR | NUM | SPECIAL
	// COLLECTION is ARR | OBJ.
	COLLECTION = ARR | OBJ
	// ALL allows every type to be partial (the TS default for parse).
	ALL = ATOM | COLLECTION
)

// PartialJSON is thrown when the JSON is incomplete in a way allow does not permit.
type PartialJSON struct {
	Message string
}

func (e *PartialJSON) Error() string { return e.Message }

// Name returns the JS error name. The TS class does not set `name`, so it is the
// inherited "Error" (String(err) is "Error: <message>").
func (e *PartialJSON) Name() string { return "Error" }

// MalformedJSON is thrown when the JSON is malformed.
type MalformedJSON struct {
	Message string
}

func (e *MalformedJSON) Error() string { return e.Message }

// Name returns the JS error name. The TS class does not set `name`, so it is the
// inherited "Error" (String(err) is "Error: <message>").
func (e *MalformedJSON) Name() string { return "Error" }

// Parse parses incomplete JSON. allow specifies what types are allowed to be partial
// (TS default: ALL). The result uses the jsonx value model.
//
// Errors: *PartialJSON if the JSON is incomplete (related to allow), *MalformedJSON if the
// JSON is malformed, a plain error "<input> is empty" for blank input, and, as in the npm
// package, a raw *jsonx.SyntaxError when the last-backslash fallback for a partial string
// fails.
func Parse(jsonString string, allow Allow) (any, error) {
	return ParseJSON(jsonString, allow)
}

// ParseJSON is the same function as Parse (TS exports it under both names).
func ParseJSON(jsonString string, allow Allow) (any, error) {
	trimmed := trimUnits(toUTF16(jsonString))
	if len(trimmed) == 0 {
		return nil, errors.New(jsonString + " is empty")
	}
	p := &parser{s: trimmed, length: len(trimmed), allow: allow}
	return p.parseAny()
}

type parser struct {
	s      []uint16
	length int
	index  int
	allow  Allow
}

// at returns jsonString[i] as a code unit, or -1 for undefined.
func (p *parser) at(i int) int {
	if i >= 0 && i < p.length {
		return int(p.s[i])
	}
	return -1
}

// substring implements String.prototype.substring(start, end) on code units.
func (p *parser) substring(start, end int) []uint16 {
	start = min(max(start, 0), p.length)
	end = min(max(end, 0), p.length)
	if start > end {
		start, end = end, start
	}
	return p.s[start:end]
}

// equalsASCII reports whether units spell exactly the ASCII string lit.
func equalsASCII(units []uint16, lit string) bool {
	if len(units) != len(lit) {
		return false
	}
	for i, u := range units {
		if u != uint16(lit[i]) {
			return false
		}
	}
	return true
}

// literalStartsWith implements lit.startsWith(units) for an ASCII lit.
func literalStartsWith(lit string, units []uint16) bool {
	if len(units) > len(lit) {
		return false
	}
	return equalsASCII(units, lit[:len(units)])
}

// lastIndexOf implements jsonString.lastIndexOf(c) for a single code unit.
func (p *parser) lastIndexOf(c uint16) int {
	for i := p.length - 1; i >= 0; i-- {
		if p.s[i] == c {
			return i
		}
	}
	return -1
}

func (p *parser) markPartialJSON(msg string) error {
	return &PartialJSON{Message: msg + " at position " + strconv.Itoa(p.index)}
}

func (p *parser) throwMalformedError(msg string) error {
	return &MalformedJSON{Message: msg + " at position " + strconv.Itoa(p.index)}
}

// jsonParse is JSON.parse on the given code units plus an ASCII suffix.
func jsonParse(units []uint16, suffix string) (any, error) {
	return jsonx.ParseString(fromUTF16(units) + suffix)
}

// errorString is String(e) for an error thrown by JSON.parse.
func errorString(err error) string {
	var se *jsonx.SyntaxError
	if errors.As(err, &se) {
		return "SyntaxError: " + se.Message
	}
	return "Error: " + err.Error()
}

func (p *parser) has(a Allow) bool { return p.allow&a != 0 }

func (p *parser) parseAny() (any, error) {
	p.skipBlank()
	if p.index >= p.length {
		return nil, p.markPartialJSON("Unexpected end of input")
	}
	switch p.at(p.index) {
	case '"':
		return p.parseStr()
	case '{':
		return p.parseObj()
	case '[':
		return p.parseArr()
	}
	rest := p.substring(p.index, p.length)
	remaining := p.length - p.index
	if equalsASCII(p.substring(p.index, p.index+4), "null") || (p.has(NULL) && remaining < 4 && literalStartsWith("null", rest)) {
		p.index += 4
		return nil, nil
	}
	if equalsASCII(p.substring(p.index, p.index+4), "true") || (p.has(BOOL) && remaining < 4 && literalStartsWith("true", rest)) {
		p.index += 4
		return true, nil
	}
	if equalsASCII(p.substring(p.index, p.index+5), "false") || (p.has(BOOL) && remaining < 5 && literalStartsWith("false", rest)) {
		p.index += 5
		return false, nil
	}
	if equalsASCII(p.substring(p.index, p.index+8), "Infinity") || (p.has(INFINITY) && remaining < 8 && literalStartsWith("Infinity", rest)) {
		p.index += 8
		return math.Inf(1), nil
	}
	if equalsASCII(p.substring(p.index, p.index+9), "-Infinity") || (p.has(Infinity) && 1 < remaining && remaining < 9 && literalStartsWith("-Infinity", rest)) {
		p.index += 9
		return math.Inf(-1), nil
	}
	if equalsASCII(p.substring(p.index, p.index+3), "NaN") || (p.has(NAN) && remaining < 3 && literalStartsWith("NaN", rest)) {
		p.index += 3
		return math.NaN(), nil
	}
	return p.parseNum()
}

func (p *parser) parseStr() (any, error) {
	start := p.index
	escape := false
	p.index++ // skip initial quote
	for p.index < p.length && (p.s[p.index] != '"' || (escape && p.s[p.index-1] == '\\')) {
		escape = p.s[p.index] == '\\' && !escape
		p.index++
	}
	escapeNum := 0
	if escape {
		escapeNum = 1
	}
	if p.at(p.index) == '"' {
		p.index++
		v, err := jsonParse(p.substring(start, p.index-escapeNum), "")
		if err != nil {
			return nil, p.throwMalformedError(errorString(err))
		}
		return v, nil
	} else if p.has(STR) {
		v, err := jsonParse(p.substring(start, p.index-escapeNum), `"`)
		if err == nil {
			return v, nil
		}
		// SyntaxError: Invalid escape sequence. A failure here propagates as is.
		return jsonParse(p.substring(start, p.lastIndexOf('\\')), `"`)
	}
	return nil, p.markPartialJSON("Unterminated string literal")
}

func (p *parser) parseObj() (any, error) {
	p.index++ // skip initial brace
	p.skipBlank()
	obj := jsonx.NewObject()
	// The loop body mirrors the TS try block: any error ends up in the catch below.
	err := func() error {
		for p.at(p.index) != '}' {
			p.skipBlank()
			if p.index >= p.length && p.has(OBJ) {
				return errReturnObj
			}
			key, err := p.parseStr()
			if err != nil {
				return err
			}
			p.skipBlank()
			p.index++ // skip colon
			value, err := p.parseAny()
			if err != nil {
				if p.has(OBJ) {
					return errReturnObj
				}
				return err
			}
			setProperty(obj, key, value)
			p.skipBlank()
			if p.at(p.index) == ',' {
				p.index++ // skip comma
			}
		}
		return nil
	}()
	if err == errReturnObj {
		return obj, nil
	}
	if err != nil {
		if p.has(OBJ) {
			return obj, nil
		}
		return nil, p.markPartialJSON("Expected '}' at end of object")
	}
	p.index++ // skip final brace
	return obj, nil
}

// errReturnObj signals an early `return obj` from inside the TS try block.
var errReturnObj = errors.New("partialjson: return obj")

func (p *parser) parseArr() (any, error) {
	p.index++ // skip initial bracket
	arr := []any{}
	for p.at(p.index) != ']' {
		v, err := p.parseAny()
		if err != nil {
			if p.has(ARR) {
				return arr, nil
			}
			return nil, p.markPartialJSON("Expected ']' at end of array")
		}
		arr = append(arr, v)
		p.skipBlank()
		if p.at(p.index) == ',' {
			p.index++ // skip comma
		}
	}
	p.index++ // skip final bracket
	return arr, nil
}

func (p *parser) parseNum() (any, error) {
	if p.index == 0 {
		if equalsASCII(p.s, "-") {
			return nil, p.throwMalformedError("Not sure what '-' is")
		}
		v, err := jsonParse(p.s, "")
		if err == nil {
			return v, nil
		}
		if p.has(NUM) {
			if v2, err2 := jsonParse(p.substring(0, p.lastIndexOf('e')), ""); err2 == nil {
				return v2, nil
			}
		}
		return nil, p.throwMalformedError(errorString(err))
	}
	start := p.index
	if p.at(p.index) == '-' {
		p.index++
	}
	for p.index < p.length && p.s[p.index] != ',' && p.s[p.index] != ']' && p.s[p.index] != '}' {
		p.index++
	}
	if p.index == p.length && !p.has(NUM) {
		return nil, p.markPartialJSON("Unterminated number literal")
	}
	v, err := jsonParse(p.substring(start, p.index), "")
	if err == nil {
		return v, nil
	}
	if equalsASCII(p.substring(start, p.index), "-") {
		return nil, p.markPartialJSON("Not sure what '-' is")
	}
	v, err = jsonParse(p.substring(start, p.lastIndexOf('e')), "")
	if err != nil {
		return nil, p.throwMalformedError(errorString(err))
	}
	return v, nil
}

func (p *parser) skipBlank() {
	for p.index < p.length {
		switch p.s[p.index] {
		case ' ', '\n', '\r', '\t':
			p.index++
			continue
		}
		return
	}
}

// setProperty implements `obj[key] = value` for a freshly created plain object.
func setProperty(obj *jsonx.Object, key, value any) {
	name := propertyKey(key)
	if name == "__proto__" {
		// TS parity: assigning "__proto__" on a plain object goes through the
		// Object.prototype setter and never creates an own property.
		return
	}
	obj.Set(name, value)
}

// propertyKey is ToPropertyKey for the values JSON.parse can return. parseStr only ever
// yields strings here, but the conversion keeps the port total.
func propertyKey(key any) string {
	switch k := key.(type) {
	case string:
		return k
	case nil:
		return "null"
	case bool:
		if k {
			return "true"
		}
		return "false"
	case float64:
		switch {
		case math.IsNaN(k):
			return "NaN"
		case math.IsInf(k, 1):
			return "Infinity"
		case math.IsInf(k, -1):
			return "-Infinity"
		}
		return jsonx.FormatNumber(k)
	case []any:
		s := ""
		for i, item := range k {
			if i > 0 {
				s += ","
			}
			if item != nil {
				s += propertyKey(item)
			}
		}
		return s
	default:
		return "[object Object]"
	}
}
