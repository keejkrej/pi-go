package jsonx

import (
	"errors"
	"strconv"
	"unicode/utf8"
)

// SyntaxError is the error JSON.parse throws for malformed input. Message is the V8
// (Node 24) message text, for example
// `Unexpected token 'a', "abc" is not valid JSON` or
// `Expected ',' or '}' after property value in JSON at position 7 (line 1 column 8)`.
type SyntaxError struct {
	Message string
	// Position is the UTF-16 offset V8 reports for the error (the end of the input for
	// "Unexpected end of JSON input").
	Position int
}

func (e *SyntaxError) Error() string { return e.Message }

// Name returns the JS error class name.
func (e *SyntaxError) Name() string { return "SyntaxError" }

// Parse implements JSON.parse(text): the result is nil, bool, float64, string, []any or
// *Object. data is the text of the JS string (see the package doc for how invalid UTF-8
// and WTF-8 surrogates are read). Errors are *SyntaxError with the V8 message.
func Parse(data []byte) (any, error) {
	p := parser{data: data}
	v, ok := p.value()
	if ok {
		p.skipWS()
		if p.pos < len(p.data) {
			ok = p.fail(p.pos, msgNonWhitespace)
		}
	}
	if !ok {
		return nil, newSyntaxError(data, p.errPos, p.errMsg)
	}
	return v, nil
}

// ParseString is Parse for a string argument.
func ParseString(text string) (any, error) { return Parse([]byte(text)) }

// ParseErrorMessage returns the message V8's JSON.parse(text) error carries for err.
// For a *SyntaxError (from Parse or wrapped) it is the error's message. Any other error,
// for example one from encoding/json, is mapped by parsing text with JSON.parse rules;
// if text is valid JSON the error's own text is returned.
func ParseErrorMessage(err error, text string) string {
	if err == nil {
		return ""
	}
	var se *SyntaxError
	if errors.As(err, &se) {
		return se.Message
	}
	if _, perr := Parse([]byte(text)); perr != nil {
		return perr.Error()
	}
	return err.Error()
}

// V8 message templates that carry a position.
const (
	msgNonWhitespace       = "Unexpected non-whitespace character after JSON"
	msgPropNameOrRBrace    = "Expected property name or '}' in JSON"
	msgDoubleQuotedProp    = "Expected double-quoted property name in JSON"
	msgColonAfterProp      = "Expected ':' after property name in JSON"
	msgCommaOrRBrace       = "Expected ',' or '}' after property value in JSON"
	msgCommaOrRBrack       = "Expected ',' or ']' after array element in JSON"
	msgUnterminatedString  = "Unterminated string in JSON"
	msgBadControlChar      = "Bad control character in string literal in JSON"
	msgBadUnicodeEscape    = "Bad Unicode escape in JSON"
	msgBadEscapedChar      = "Bad escaped character in JSON"
	msgNoNumberAfterMinus  = "No number after minus sign in JSON"
	msgUnterminatedFrac    = "Unterminated fractional number in JSON"
	msgExponentMissingNum  = "Exponent part is missing a number in JSON"
	jsonMaxContextChars    = 10
	jsonMinLenForContext   = jsonMaxContextChars*2 + 1
	unexpectedEndOfJSONMsg = "Unexpected end of JSON input"
)

// parser is a port of V8's JsonParser (src/json/json-parser.cc) over UTF-8 bytes.
type parser struct {
	data   []byte
	pos    int
	errPos int    // byte offset of the error
	errMsg string // explicit message template; "" = derive from the token at errPos
}

func (p *parser) fail(pos int, msg string) bool {
	p.errPos = pos
	p.errMsg = msg
	return false
}

func (p *parser) skipWS() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isNumberPart mirrors V8's NumberPart scan flag.
func isNumberPart(c byte) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

// parseFrame is one open container on the parser's explicit stack. V8's JsonParser
// keeps a continuation stack instead of recursing, so JSON.parse accepts any nesting
// depth; recursing here would let deep input overflow the goroutine stack, which Go
// cannot recover from.
type parseFrame struct {
	obj *Object // the object being filled, or nil for an array
	arr []any   // the array being filled
	key string  // the property whose value is being parsed (objects only)
}

// value parses one JSON value, including everything nested in it.
func (p *parser) value() (any, bool) {
	var stack []parseFrame
	for {
		// Parse the start of a value. Containers push a frame and loop back here for
		// their first member; scalars and empty containers fall through as v.
		p.skipWS()
		if p.pos >= len(p.data) {
			return nil, p.fail(p.pos, "")
		}
		var v any
		switch c := p.data[p.pos]; c {
		case '"':
			p.pos++
			s, ok := p.str()
			if !ok {
				return nil, false
			}
			v = s
		case '{':
			p.pos++ // {
			p.skipWS()
			if p.pos < len(p.data) && p.data[p.pos] == '}' {
				p.pos++
				v = &Object{}
				break
			}
			if p.pos >= len(p.data) || p.data[p.pos] != '"' {
				return nil, p.fail(p.pos, msgPropNameOrRBrace)
			}
			key, ok := p.propertyKey()
			if !ok {
				return nil, false
			}
			stack = append(stack, parseFrame{obj: &Object{}, key: key})
			continue
		case '[':
			p.pos++ // [
			p.skipWS()
			if p.pos < len(p.data) && p.data[p.pos] == ']' {
				p.pos++
				v = []any{}
				break
			}
			stack = append(stack, parseFrame{arr: []any{}})
			continue
		case 't':
			if !p.literal("true") {
				return nil, false
			}
			v = true
		case 'f':
			if !p.literal("false") {
				return nil, false
			}
			v = false
		case 'n':
			if !p.literal("null") {
				return nil, false
			}
			v = nil
		default:
			if c != '-' && !isDigit(c) {
				return nil, p.fail(p.pos, "")
			}
			n, ok := p.number()
			if !ok {
				return nil, false
			}
			v = n
		}
		// Store the finished value in its container; close every container that ends
		// here, until one continues with another member or the stack is empty.
		for {
			if len(stack) == 0 {
				return v, true
			}
			top := &stack[len(stack)-1]
			p.skipWS()
			if top.obj != nil {
				top.obj.set(top.key, v)
				if p.pos < len(p.data) && p.data[p.pos] == ',' {
					p.pos++
					p.skipWS()
					if p.pos >= len(p.data) || p.data[p.pos] != '"' {
						return nil, p.fail(p.pos, msgDoubleQuotedProp)
					}
					key, ok := p.propertyKey()
					if !ok {
						return nil, false
					}
					top.key = key
					break
				}
				if p.pos < len(p.data) && p.data[p.pos] == '}' {
					p.pos++
					v = top.obj
					stack = stack[:len(stack)-1]
					continue
				}
				return nil, p.fail(p.pos, msgCommaOrRBrace)
			}
			top.arr = append(top.arr, v)
			if p.pos < len(p.data) && p.data[p.pos] == ',' {
				p.pos++
				break
			}
			if p.pos < len(p.data) && p.data[p.pos] == ']' {
				p.pos++
				v = top.arr
				stack = stack[:len(stack)-1]
				continue
			}
			return nil, p.fail(p.pos, msgCommaOrRBrack)
		}
	}
}

// propertyKey scans a property name and the colon after it; p.pos is at the opening
// quote.
func (p *parser) propertyKey() (string, bool) {
	p.pos++ // "
	key, ok := p.str()
	if !ok {
		return "", false
	}
	p.skipWS()
	if p.pos >= len(p.data) || p.data[p.pos] != ':' {
		return "", p.fail(p.pos, msgColonAfterProp)
	}
	p.pos++
	return key, true
}

// literal scans true/false/null; the first character already matched (V8 ScanLiteral).
func (p *parser) literal(lit string) bool {
	if len(p.data)-p.pos >= len(lit) && string(p.data[p.pos:p.pos+len(lit)]) == lit {
		p.pos += len(lit)
		return true
	}
	p.pos++
	for i := 1; i < len(lit) && p.pos < len(p.data); i++ {
		if p.data[p.pos] != lit[i] {
			return p.fail(p.pos, "")
		}
		p.pos++
	}
	return p.fail(p.pos, "")
}

// scanNumber validates a number token at p.pos with V8's rules and advances past it.
func (p *parser) scanNumber() bool {
	neg := false
	if p.data[p.pos] == '-' {
		neg = true
		p.pos++
	}
	if p.pos < len(p.data) && p.data[p.pos] == '0' {
		p.pos++
		if p.pos < len(p.data) && isNumberPart(p.data[p.pos]) {
			if isDigit(p.data[p.pos]) {
				return p.fail(p.pos, "") // "Unexpected number"
			}
		} else if !neg {
			return true
		}
	} else {
		start := p.pos
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
		if p.pos == start {
			return p.fail(p.pos, msgNoNumberAfterMinus)
		}
	}
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.data) || !isDigit(p.data[p.pos]) {
			return p.fail(p.pos, msgUnterminatedFrac)
		}
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
	}
	if p.pos < len(p.data) && p.data[p.pos]|0x20 == 'e' {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '-' || p.data[p.pos] == '+') {
			p.pos++
		}
		if p.pos >= len(p.data) || !isDigit(p.data[p.pos]) {
			return p.fail(p.pos, msgExponentMissingNum)
		}
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
	}
	return true
}

func (p *parser) number() (any, bool) {
	start := p.pos
	if !p.scanNumber() {
		return nil, false
	}
	return numberValue(p.data[start:p.pos]), true
}

// numberValue converts a validated JSON number token to a float64 (correctly rounded,
// overflow to ±Infinity, -0 preserved), as V8's StringToDouble does.
func numberValue(tok []byte) float64 {
	// Fast path: up to 15 plain digits are exact.
	neg := false
	digits := tok
	if len(digits) > 0 && digits[0] == '-' {
		neg = true
		digits = digits[1:]
	}
	if len(digits) > 0 && len(digits) <= 15 {
		var n int64
		plain := true
		for _, c := range digits {
			if !isDigit(c) {
				plain = false
				break
			}
			n = n*10 + int64(c-'0')
		}
		if plain {
			f := float64(n)
			if neg {
				f = -f
			}
			return f
		}
	}
	f, _ := strconv.ParseFloat(string(tok), 64)
	return f
}

// str scans a string body; p.pos is just after the opening quote (V8 ScanJsonString).
func (p *parser) str() (string, bool) {
	start := p.pos
	data := p.data
	for p.pos < len(data) {
		c := data[p.pos]
		if c == '"' {
			s := string(data[start:p.pos])
			p.pos++
			return s, true
		}
		if c == '\\' || c < 0x20 || c >= utf8.RuneSelf {
			break
		}
		p.pos++
	}
	buf := make([]byte, 0, p.pos-start+16)
	buf = append(buf, data[start:p.pos]...)
	surrogates := false
	for {
		if p.pos >= len(data) {
			return "", p.fail(p.pos, msgUnterminatedString)
		}
		c := data[p.pos]
		switch {
		case c == '"':
			p.pos++
			if surrogates {
				buf = joinSurrogatePairs(buf)
			}
			return string(buf), true
		case c == '\\':
			p.pos++
			if p.pos >= len(data) {
				return "", p.fail(p.pos, "") // Unexpected end of JSON input
			}
			switch e := data[p.pos]; e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				var r rune
				for i := 0; i < 4; i++ {
					p.pos++
					if p.pos >= len(data) {
						return "", p.fail(p.pos, msgBadUnicodeEscape)
					}
					h := hexValue(data[p.pos])
					if h < 0 {
						return "", p.fail(p.pos, msgBadUnicodeEscape)
					}
					r = r<<4 | rune(h)
				}
				if isSurrogate(r) {
					surrogates = true
				}
				buf = appendWTF8(buf, r)
			default:
				if e >= utf8.RuneSelf {
					// V8 reports characters above Latin-1 as an unexpected token.
					if r, _ := decodeCharAt(data, p.pos); r > 0xFF {
						return "", p.fail(p.pos, "")
					}
				}
				return "", p.fail(p.pos, msgBadEscapedChar)
			}
			p.pos++
		case c < 0x20:
			return "", p.fail(p.pos, msgBadControlChar)
		case c < utf8.RuneSelf:
			buf = append(buf, c)
			p.pos++
		default:
			r, size := decodeCharAt(data, p.pos)
			switch {
			case r == utf8.RuneError && size <= 3 && !(size == 3 && data[p.pos] == 0xEF):
				buf = append(buf, "�"...)
			case isSurrogate(r):
				surrogates = true
				buf = append(buf, data[p.pos:p.pos+size]...)
			default:
				buf = append(buf, data[p.pos:p.pos+size]...)
			}
			p.pos += size
		}
	}
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// newSyntaxError builds V8's message for an error at byte offset bytePos.
func newSyntaxError(data []byte, bytePos int, tmpl string) *SyntaxError {
	units := toUTF16(data)
	pos := utf16Len(data[:bytePos])
	if tmpl != "" {
		line, col := jsonFileLocation(units, pos)
		return &SyntaxError{
			Message:  tmpl + " at position " + strconv.Itoa(pos) + " (line " + strconv.Itoa(line) + " column " + strconv.Itoa(col) + ")",
			Position: pos,
		}
	}
	if pos >= len(units) {
		return &SyntaxError{Message: unexpectedEndOfJSONMsg, Position: pos}
	}
	switch u := units[pos]; {
	case u == '"' || u == '-' || (u >= '0' && u <= '9'):
		kind := "number"
		if u == '"' {
			kind = "string"
		}
		line, col := jsonFileLocation(units, pos)
		return &SyntaxError{
			Message:  "Unexpected " + kind + " in JSON at position " + strconv.Itoa(pos) + " (line " + strconv.Itoa(line) + " column " + strconv.Itoa(col) + ")",
			Position: pos,
		}
	}
	src := fromUTF16(units)
	switch src {
	case "[object Object]", "undefined", "Infinity", "NaN":
		return &SyntaxError{Message: `"` + src + `" is not valid JSON`, Position: pos}
	}
	token := fromUTF16(units[pos : pos+1])
	length := len(units)
	var msg string
	switch {
	case length < jsonMinLenForContext:
		msg = "Unexpected token '" + token + "', \"" + src + "\" is not valid JSON"
	case pos < jsonMaxContextChars:
		msg = "Unexpected token '" + token + "', \"" + fromUTF16(units[:pos+jsonMaxContextChars]) + "\"... is not valid JSON"
	case pos < length-jsonMaxContextChars:
		msg = "Unexpected token '" + token + "', ...\"" + fromUTF16(units[pos-jsonMaxContextChars:pos+jsonMaxContextChars]) + "\"... is not valid JSON"
	default:
		msg = "Unexpected token '" + token + "', ...\"" + fromUTF16(units[pos-jsonMaxContextChars:]) + "\" is not valid JSON"
	}
	return &SyntaxError{Message: msg, Position: pos}
}

// jsonFileLocation ports V8 JsonParser::CalculateFileLocation: 1-based line and column of
// UTF-16 offset end, where \r\n, \r and \n each end a line.
func jsonFileLocation(units []uint16, end int) (line, col int) {
	line = 1
	lastBreak := 0
	cursor := 0
	for ; cursor < end; cursor++ {
		if units[cursor] == '\r' && cursor < end-1 && units[cursor+1] == '\n' {
			cursor++
		}
		if units[cursor] == '\r' || units[cursor] == '\n' {
			line++
			lastBreak = cursor + 1
		}
	}
	return line, 1 + cursor - lastBreak
}
