package jsonx

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TypeError is the error JSON.stringify throws (TypeError in JS): a circular structure or
// a BigInt value.
type TypeError struct {
	Message string
}

func (e *TypeError) Error() string { return e.Message }

// Name returns the JS error class name.
func (e *TypeError) Name() string { return "TypeError" }

// RangeError is the error JSON.stringify throws (RangeError in JS) when the value is
// nested too deeply for V8's native stack: "Maximum call stack size exceeded".
type RangeError struct {
	Message string
}

func (e *RangeError) Error() string { return e.Message }

// Name returns the JS error class name.
func (e *RangeError) Name() string { return "RangeError" }

// stringifyMaxDepth is the deepest container nesting JSON.stringify accepts. V8's
// serializer recurses on the native stack and throws a RangeError when it runs out; the
// exact depth depends on the caller's stack, and node v24 manages 6185 nested arrays or
// objects from a shallow stack (JSON.parse has no limit). The same fixed limit keeps
// deeply nested parsed documents from overflowing the goroutine stack, which Go cannot
// recover from.
const stringifyMaxDepth = 6185

// internalMaxDepth bounds the conversions Decode makes for json.Unmarshaler targets.
// They stand for no JS operation, so they must not fail at V8's depth, but parsed input
// nests without limit and Go aborts the process when a goroutine stack overflows (1 GB,
// reached near a million levels in race-instrumented builds).
const internalMaxDepth = 100_000

func stackOverflowError() error { return &RangeError{Message: "Maximum call stack size exceeded"} }

// Stringify implements JSON.stringify(v). v may hold untyped values (nil, bool, float64,
// string, []any, *Object) and typed Go values. When JS would return undefined (v is a
// function), the result is "" with a nil error.
func Stringify(v any) (string, error) {
	b, err := marshal(v, "")
	return string(b), err
}

// StringifyIndent implements JSON.stringify(v, null, indent). As in JS only the first 10
// UTF-16 code units of indent are used, and an empty indent gives compact output. For a
// numeric JS indent n pass strings.Repeat(" ", n).
func StringifyIndent(v any, indent string) (string, error) {
	b, err := marshal(v, jsGap(indent))
	return string(b), err
}

// MustStringify is Stringify for values that cannot fail (no cycles, no BigInt). It
// panics on error.
func MustStringify(v any) string {
	s, err := Stringify(v)
	if err != nil {
		panic("jsonx.MustStringify: " + err.Error())
	}
	return s
}

// Marshal returns the JSON.stringify bytes of v (typed or untyped). It returns nil and a
// nil error when JS would return undefined.
func Marshal(v any) ([]byte, error) {
	return marshal(v, "")
}

// MarshalIndent is Marshal with JSON.stringify(v, null, indent) formatting.
func MarshalIndent(v any, indent string) ([]byte, error) {
	return marshal(v, jsGap(indent))
}

// Quote returns JSON.stringify(s).
func Quote(s string) string {
	return string(appendQuoted(make([]byte, 0, len(s)+2), s))
}

// AppendQuote appends JSON.stringify(s) to dst.
func AppendQuote(dst []byte, s string) []byte {
	return appendQuoted(dst, s)
}

// StringifyWithReplacer implements JSON.stringify(v, replacer, indent) for a replacer
// function. The replacer is called like the JS one: first with key "" and the whole value,
// then for every property (array elements get their index as key) with the value after
// toJSON-style conversion (typed Go values are converted with Encode first). Returning
// keep=false means undefined: the property is omitted, an array element becomes null, and
// at the top level the result is "".
func StringifyWithReplacer(v any, replacer func(key string, value any) (replaced any, keep bool), indent string) (string, error) {
	// path tracks the containers being replaced like the serializer's stack, so a cycle
	// fails with V8's TypeError and deep nesting with its RangeError instead of
	// recursing until the goroutine stack overflows.
	path := encoder{maxDepth: stringifyMaxDepth}
	// key is the property name (string) or array index (int) of val in its holder.
	var replace func(key any, val any) (any, bool, error)
	replace = func(key any, val any) (any, bool, error) {
		val, err := toUntyped(val)
		if err != nil {
			return nil, false, err
		}
		name, isName := key.(string)
		if !isName {
			name = strconv.Itoa(key.(int))
		}
		nv, keep := replacer(name, val)
		if !keep {
			return nil, false, nil
		}
		nv, err = toUntyped(nv)
		if err != nil {
			return nil, false, err
		}
		switch x := nv.(type) {
		case *Object:
			if x == nil {
				return nil, true, nil
			}
			if err := path.push(stackKey{ptr: uintptr(reflect.ValueOf(x).UnsafePointer()), typ: objectPtrType}, key, "Object"); err != nil {
				return nil, false, err
			}
			defer path.pop()
			if err := path.enter(); err != nil {
				return nil, false, err
			}
			defer func() { path.depth-- }()
			out := &Object{}
			for i := 0; i < len(x.entries); i++ {
				k := x.entries[i].key
				cv, ok, err := replace(k, x.entries[i].val)
				if err != nil {
					return nil, false, err
				}
				if ok {
					out.set(k, cv)
				}
			}
			return out, true, nil
		case []any:
			if x == nil {
				return nil, true, nil
			}
			if len(x) > 0 {
				if err := path.push(stackKey{ptr: uintptr(reflect.ValueOf(x).UnsafePointer()), n: len(x), typ: anySliceType}, key, "Array"); err != nil {
					return nil, false, err
				}
				defer path.pop()
			}
			if err := path.enter(); err != nil {
				return nil, false, err
			}
			defer func() { path.depth-- }()
			out := make([]any, len(x))
			for i, el := range x {
				cv, ok, err := replace(i, el)
				if err != nil {
					return nil, false, err
				}
				if ok {
					out[i] = cv
				}
			}
			return out, true, nil
		default:
			return nv, true, nil
		}
	}
	res, keep, err := replace("", v)
	if err != nil || !keep {
		return "", err
	}
	return StringifyIndent(res, indent)
}

// toUntyped converts typed values to the untyped model; untyped containers are returned
// as they are (their children are converted lazily by the caller).
func toUntyped(v any) (any, error) {
	switch v.(type) {
	case nil, bool, float64, string, *Object, []any:
		return v, nil
	}
	return Encode(v)
}

// jsGap applies the JSON.stringify rule that a string gap is cut to 10 UTF-16 units.
func jsGap(indent string) string {
	if len(indent) <= 10 {
		return indent
	}
	units := toUTF16(indent)
	if len(units) <= 10 {
		return indent
	}
	return fromUTF16(units[:10])
}

func marshal(v any, gap string) ([]byte, error) {
	return marshalDepth(v, gap, stringifyMaxDepth)
}

func marshalDepth(v any, gap string, maxDepth int) ([]byte, error) {
	e := encoder{gap: gap, maxDepth: maxDepth}
	ok, err := e.encodeAny(v, "")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return e.buf, nil
}

// encoder ports V8's JsonStringifier output rules onto Go values.
type encoder struct {
	buf      []byte
	gap      string
	depth    int
	maxDepth int
	stack    []stackEntry
	onStk    map[stackKey]int // built once the stack is deep, for O(1) cycle checks
}

type stackKey struct {
	ptr uintptr
	n   int
	typ reflect.Type
}

type stackEntry struct {
	id   stackKey
	key  any // string property name or int array index
	ctor string
}

const stackMapThreshold = 32

// push records a container on the serialization stack, failing like V8 when the
// container is already being serialized (a cycle).
func (e *encoder) push(id stackKey, key any, ctor string) error {
	if e.onStk != nil {
		if i, ok := e.onStk[id]; ok {
			return circularError(e.stack, i, key)
		}
	} else {
		for i := range e.stack {
			if e.stack[i].id == id {
				return circularError(e.stack, i, key)
			}
		}
	}
	e.stack = append(e.stack, stackEntry{id: id, key: key, ctor: ctor})
	if e.onStk != nil {
		e.onStk[id] = len(e.stack) - 1
	} else if len(e.stack) > stackMapThreshold {
		e.onStk = make(map[stackKey]int, len(e.stack)*2)
		for i, s := range e.stack {
			e.onStk[s.id] = i
		}
	}
	return nil
}

func (e *encoder) pop() {
	last := e.stack[len(e.stack)-1]
	e.stack = e.stack[:len(e.stack)-1]
	if e.onStk != nil {
		delete(e.onStk, last.id)
	}
}

// circularError builds V8's "Converting circular structure to JSON" message.
func circularError(stack []stackEntry, start int, lastKey any) error {
	var b strings.Builder
	b.WriteString("Converting circular structure to JSON")
	b.WriteString("\n    --> starting at object with constructor '")
	b.WriteString(stack[start].ctor)
	b.WriteString("'")
	normal := func(s stackEntry) {
		b.WriteString("\n    |     ")
		b.WriteString(circularKey(s.key))
		b.WriteString(" -> object with constructor '")
		b.WriteString(s.ctor)
		b.WriteString("'")
	}
	index := start + 1
	prefixEnd := min(len(stack), index+2)
	for ; index < prefixEnd; index++ {
		normal(stack[index])
	}
	if len(stack) > index+1 {
		b.WriteString("\n    |     ...")
	}
	index = max(index, len(stack)-1)
	for ; index < len(stack); index++ {
		normal(stack[index])
	}
	b.WriteString("\n    --- ")
	b.WriteString(circularKey(lastKey))
	b.WriteString(" closes the circle")
	return &TypeError{Message: b.String()}
}

func circularKey(key any) string {
	switch k := key.(type) {
	case int:
		return "index " + strconv.Itoa(k)
	case string:
		if k == "" {
			return "<anonymous>"
		}
		return "property '" + k + "'"
	}
	return "<anonymous>"
}

func (e *encoder) newline() {
	if e.gap == "" {
		return
	}
	e.buf = append(e.buf, '\n')
	for i := 0; i < e.depth; i++ {
		e.buf = append(e.buf, e.gap...)
	}
}

// enter increases the nesting depth for a container, failing like V8 when the
// serializer would run out of stack.
func (e *encoder) enter() error {
	e.depth++
	if e.depth > e.maxDepth {
		return stackOverflowError()
	}
	return nil
}

// memberStart writes the separator, newline, key and colon of an object member and
// returns the buffer length to roll back to if the value turns out to be undefined.
func (e *encoder) memberStart(first bool, key string) int {
	mark := len(e.buf)
	if !first {
		e.buf = append(e.buf, ',')
	}
	e.newline()
	e.buf = appendQuoted(e.buf, key)
	e.buf = append(e.buf, ':')
	if e.gap != "" {
		e.buf = append(e.buf, ' ')
	}
	return mark
}

// encodeAny writes v and reports false when v is undefined in JS terms (a function).
func (e *encoder) encodeAny(v any, key any) (bool, error) {
	switch x := v.(type) {
	case nil:
		e.buf = append(e.buf, "null"...)
	case string:
		e.buf = appendQuoted(e.buf, x)
	case float64:
		e.buf = appendNumber(e.buf, x)
	case bool:
		e.buf = strconv.AppendBool(e.buf, x)
	case *Object:
		if x == nil {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.object(x, key)
	case []any:
		if x == nil {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.array(x, key)
	case int:
		e.buf = appendInt(e.buf, int64(x))
	case int64:
		e.buf = appendInt(e.buf, x)
	case int32:
		e.buf = strconv.AppendInt(e.buf, int64(x), 10)
	case float32:
		e.buf = appendFloat(e.buf, float64(x), 32)
	case map[string]any:
		if x == nil {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.stringMap(x, key)
	case time.Time:
		e.buf = appendTime(e.buf, x)
	case json.Number:
		return true, e.number(x)
	case json.RawMessage:
		if x == nil {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		if err := e.raw(x, key); err != nil {
			return false, &json.MarshalerError{Type: rawMessageType, Err: err}
		}
	default:
		return e.reflectValue(reflect.ValueOf(v), key, false)
	}
	return true, nil
}

func (e *encoder) object(o *Object, key any) error {
	if err := e.push(stackKey{ptr: uintptr(reflect.ValueOf(o).UnsafePointer()), typ: objectPtrType}, key, "Object"); err != nil {
		return err
	}
	e.buf = append(e.buf, '{')
	if err := e.enter(); err != nil {
		return err
	}
	first := true
	for i := 0; i < len(o.entries); i++ {
		k := o.entries[i].key
		mark := e.memberStart(first, k)
		ok, err := e.encodeAny(o.entries[i].val, k)
		if err != nil {
			return err
		}
		if ok {
			first = false
		} else {
			e.buf = e.buf[:mark]
		}
	}
	e.depth--
	if !first {
		e.newline()
	}
	e.buf = append(e.buf, '}')
	e.pop()
	return nil
}

func (e *encoder) array(a []any, key any) error {
	if len(a) > 0 {
		if err := e.push(stackKey{ptr: uintptr(reflect.ValueOf(a).UnsafePointer()), n: len(a), typ: anySliceType}, key, "Array"); err != nil {
			return err
		}
		defer e.pop()
	}
	e.buf = append(e.buf, '[')
	if err := e.enter(); err != nil {
		return err
	}
	for i, el := range a {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newline()
		ok, err := e.encodeAny(el, i)
		if err != nil {
			return err
		}
		if !ok {
			e.buf = append(e.buf, "null"...)
		}
	}
	e.depth--
	if len(a) > 0 {
		e.newline()
	}
	e.buf = append(e.buf, ']')
	return nil
}

func (e *encoder) stringMap(m map[string]any, key any) error {
	if err := e.push(stackKey{ptr: uintptr(reflect.ValueOf(m).UnsafePointer()), typ: reflect.TypeOf(m)}, key, "Object"); err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortMapKeys(keys)
	e.buf = append(e.buf, '{')
	if err := e.enter(); err != nil {
		return err
	}
	first := true
	for _, k := range keys {
		mark := e.memberStart(first, k)
		ok, err := e.encodeAny(m[k], k)
		if err != nil {
			return err
		}
		if ok {
			first = false
		} else {
			e.buf = e.buf[:mark]
		}
	}
	e.depth--
	if !first {
		e.newline()
	}
	e.buf = append(e.buf, '}')
	e.pop()
	return nil
}

// sortMapKeys orders Go map keys like a JS object would hold them: array indices
// ascending first, the rest by string order (Go maps have no insertion order).
func sortMapKeys(keys []string) {
	slices.SortFunc(keys, func(a, b string) int {
		ai, bi := IsArrayIndex(a), IsArrayIndex(b)
		switch {
		case ai && bi:
			if indexKeyLess(a, b) {
				return -1
			}
			if a == b {
				return 0
			}
			return 1
		case ai:
			return -1
		case bi:
			return 1
		}
		return strings.Compare(a, b)
	})
}

func (e *encoder) number(n json.Number) error {
	s := string(n)
	if s == "" {
		s = "0"
	}
	p := parser{data: []byte(s)}
	if (s[0] != '-' && !isDigit(s[0])) || !p.scanNumber() || p.pos != len(s) {
		return &json.MarshalerError{Type: numberType, Err: &json.UnsupportedValueError{Str: strconv.Quote(s)}}
	}
	e.buf = appendNumberToken(e.buf, []byte(s))
	return nil
}

// appendNumberToken writes a validated JSON number token the way
// JSON.stringify(JSON.parse(token)) does: the token becomes a double first, so "1.50"
// is written as 1.5, "-0" as 0 and 123456789012345678901 as 123456789012345680000.
func appendNumberToken(dst []byte, tok []byte) []byte {
	return appendNumber(dst, numberValue(tok))
}

// raw writes JSON text produced by a json.Marshaler (or held in a json.RawMessage) the
// way JS would serialize the value that text denotes: JSON.stringify(JSON.parse(text)),
// with the current indentation. Keys therefore get JS property order, duplicates keep
// the last value, and numbers are doubles.
func (e *encoder) raw(data []byte, key any) error {
	v, err := Parse(data)
	if err != nil {
		return err
	}
	_, err = e.encodeAny(v, key)
	return err
}

// appendTime writes a time like Date.prototype.toJSON (toISOString, or null when the
// time is outside the JS Date range).
func appendTime(dst []byte, t time.Time) []byte {
	const maxMs = 8.64e15
	sec := t.Unix()
	if sec > maxMs/1000 || sec < -maxMs/1000 {
		return append(dst, "null"...)
	}
	ms := t.UnixMilli()
	if ms > maxMs || ms < -maxMs {
		return append(dst, "null"...)
	}
	return append(append(append(dst, '"'), FormatISOTime(t)...), '"')
}

// FormatISOTime formats t like JS Date.prototype.toISOString: UTC, millisecond
// precision, and the expanded ±YYYYYY year form outside 0000-9999.
func FormatISOTime(t time.Time) string {
	u := time.UnixMilli(t.UnixMilli()).UTC()
	var b []byte
	year := u.Year()
	switch {
	case year < 0:
		b = append(b, '-')
		b = appendPadded(b, -year, 6)
	case year > 9999:
		b = append(b, '+')
		b = appendPadded(b, year, 6)
	default:
		b = appendPadded(b, year, 4)
	}
	b = append(b, '-')
	b = appendPadded(b, int(u.Month()), 2)
	b = append(b, '-')
	b = appendPadded(b, u.Day(), 2)
	b = append(b, 'T')
	b = appendPadded(b, u.Hour(), 2)
	b = append(b, ':')
	b = appendPadded(b, u.Minute(), 2)
	b = append(b, ':')
	b = appendPadded(b, u.Second(), 2)
	b = append(b, '.')
	b = appendPadded(b, u.Nanosecond()/1e6, 3)
	b = append(b, 'Z')
	return string(b)
}

func appendPadded(dst []byte, n, width int) []byte {
	s := strconv.Itoa(n)
	for i := len(s); i < width; i++ {
		dst = append(dst, '0')
	}
	return append(dst, s...)
}

const hexDigits = "0123456789abcdef"

// appendQuoted writes s as a JSON string literal the way V8's well-formed
// JSON.stringify does.
func appendQuoted(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size != 1 {
			i += size
			continue
		}
		dst = append(dst, s[start:i]...)
		r, size = decodeCharAt(s, i)
		switch {
		case isSurrogate(r):
			if r <= 0xDBFF && i+size < len(s) {
				if r2, size2 := decodeCharAt(s, i+size); r2 >= 0xDC00 && r2 <= 0xDFFF {
					dst = utf8.AppendRune(dst, 0x10000+(r-0xD800)<<10+(r2-0xDC00))
					i += size + size2
					start = i
					continue
				}
			}
			dst = append(dst, '\\', 'u', hexDigits[r>>12&0xF], hexDigits[r>>8&0xF], hexDigits[r>>4&0xF], hexDigits[r&0xF])
		default:
			dst = append(dst, "�"...)
		}
		i += size
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// reflectValue encodes typed Go values.
func (e *encoder) reflectValue(rv reflect.Value, key any, quoted bool) (bool, error) {
	if !rv.IsValid() {
		e.buf = append(e.buf, "null"...)
		return true, nil
	}
	t := rv.Type()
	if rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return e.encodeAny(rv.Elem().Interface(), key)
	}
	if t.Kind() == reflect.Pointer {
		// Pointers to types with JS-specific encodings must not reach their
		// promoted MarshalJSON (time.Time would be written as RFC 3339).
		switch t.Elem() {
		case timeType, numberType, rawMessageType:
			if rv.IsNil() {
				e.buf = append(e.buf, "null"...)
				return true, nil
			}
			return e.reflectValue(rv.Elem(), key, quoted)
		}
	}
	switch t {
	case objectPtrType:
		return e.encodeAny(rv.Interface(), key)
	case objectType:
		var o *Object
		if rv.CanAddr() {
			o = rv.Addr().Interface().(*Object)
		} else {
			c := rv.Interface().(Object)
			o = &c
		}
		return true, e.object(o, key)
	case timeType, numberType, rawMessageType:
		return e.encodeAny(rv.Interface(), key)
	case bigIntType:
		return false, &TypeError{Message: "Do not know how to serialize a BigInt"}
	}
	if t.Kind() == reflect.Pointer && t.Elem() == bigIntType {
		if rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return false, &TypeError{Message: "Do not know how to serialize a BigInt"}
	}
	if t.Implements(optEncoderType) {
		if t.Kind() == reflect.Pointer && rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		val, some := rv.Interface().(optEncoder).jsonxOpt()
		if !some {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return e.encodeAny(val, key)
	}
	// Unlike encoding/json, pointer-receiver marshalers are honored on non-addressable
	// values too (struct passed by value, map values, interface contents): the type's
	// JSON form must not depend on how the value was reached, or bytes would differ
	// between Stringify(x) and Stringify(&x).
	if t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(marshalerType) {
		return true, e.marshaler(addressable(rv), t, key)
	}
	if t.Implements(marshalerType) {
		if t.Kind() == reflect.Pointer && rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.marshaler(rv, t, key)
	}
	if t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textMarshalerType) {
		return true, e.textMarshaler(addressable(rv), t)
	}
	if t.Implements(textMarshalerType) {
		if t.Kind() == reflect.Pointer && rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.textMarshaler(rv, t)
	}
	switch rv.Kind() {
	case reflect.Bool:
		if quoted {
			e.buf = append(e.buf, '"')
		}
		e.buf = strconv.AppendBool(e.buf, rv.Bool())
		if quoted {
			e.buf = append(e.buf, '"')
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if quoted {
			e.buf = append(e.buf, '"')
		}
		e.buf = appendInt(e.buf, rv.Int())
		if quoted {
			e.buf = append(e.buf, '"')
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if quoted {
			e.buf = append(e.buf, '"')
		}
		e.buf = appendUint(e.buf, rv.Uint())
		if quoted {
			e.buf = append(e.buf, '"')
		}
	case reflect.Float32, reflect.Float64:
		bits := 64
		if rv.Kind() == reflect.Float32 {
			bits = 32
		}
		f := rv.Float()
		if quoted && !math.IsNaN(f) && !math.IsInf(f, 0) {
			e.buf = append(e.buf, '"')
			e.buf = appendFloat(e.buf, f, bits)
			e.buf = append(e.buf, '"')
		} else {
			e.buf = appendFloat(e.buf, f, bits)
		}
	case reflect.String:
		if quoted {
			e.buf = appendQuoted(e.buf, string(appendQuoted(nil, rv.String())))
		} else {
			e.buf = appendQuoted(e.buf, rv.String())
		}
	case reflect.Struct:
		return true, e.structValue(rv, key)
	case reflect.Map:
		if rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		return true, e.mapValue(rv, key)
	case reflect.Slice:
		if rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		et := t.Elem()
		if et.Kind() == reflect.Uint8 && !reflect.PointerTo(et).Implements(marshalerType) && !reflect.PointerTo(et).Implements(textMarshalerType) {
			b := rv.Bytes()
			e.buf = append(e.buf, '"')
			e.buf = base64.StdEncoding.AppendEncode(e.buf, b)
			e.buf = append(e.buf, '"')
			return true, nil
		}
		if rv.Len() > 0 {
			if err := e.push(stackKey{ptr: uintptr(rv.UnsafePointer()), n: rv.Len(), typ: t}, key, "Array"); err != nil {
				return false, err
			}
			defer e.pop()
		}
		return true, e.arrayValue(rv)
	case reflect.Array:
		return true, e.arrayValue(rv)
	case reflect.Pointer:
		if rv.IsNil() {
			e.buf = append(e.buf, "null"...)
			return true, nil
		}
		switch t.Elem().Kind() {
		case reflect.Struct, reflect.Array, reflect.Pointer, reflect.Interface:
			ctor := "Object"
			if t.Elem().Kind() == reflect.Array {
				ctor = "Array"
			}
			if err := e.push(stackKey{ptr: uintptr(rv.UnsafePointer()), typ: t}, key, ctor); err != nil {
				return false, err
			}
			defer e.pop()
		}
		return e.reflectValue(rv.Elem(), key, quoted)
	case reflect.Func:
		return false, nil
	default:
		return false, &json.UnsupportedTypeError{Type: t}
	}
	return true, nil
}

// addressable returns a pointer to rv, boxing a copy when rv is not addressable.
func addressable(rv reflect.Value) reflect.Value {
	if rv.CanAddr() {
		return rv.Addr()
	}
	p := reflect.New(rv.Type())
	p.Elem().Set(rv)
	return p
}

func (e *encoder) marshaler(rv reflect.Value, t reflect.Type, key any) error {
	m, ok := rv.Interface().(json.Marshaler)
	if !ok {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	b, err := m.MarshalJSON()
	if err != nil {
		return &json.MarshalerError{Type: t, Err: err}
	}
	if err := e.raw(b, key); err != nil {
		return &json.MarshalerError{Type: t, Err: err}
	}
	return nil
}

func (e *encoder) textMarshaler(rv reflect.Value, t reflect.Type) error {
	m, ok := rv.Interface().(encoding.TextMarshaler)
	if !ok {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	b, err := m.MarshalText()
	if err != nil {
		return &json.MarshalerError{Type: t, Err: err}
	}
	e.buf = appendQuoted(e.buf, string(b))
	return nil
}

func (e *encoder) arrayValue(rv reflect.Value) error {
	n := rv.Len()
	e.buf = append(e.buf, '[')
	if err := e.enter(); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newline()
		ok, err := e.reflectValue(rv.Index(i), i, false)
		if err != nil {
			return err
		}
		if !ok {
			e.buf = append(e.buf, "null"...)
		}
	}
	e.depth--
	if n > 0 {
		e.newline()
	}
	e.buf = append(e.buf, ']')
	return nil
}

func (e *encoder) structValue(rv reflect.Value, key any) error {
	fields := cachedTypeFields(rv.Type())
	e.buf = append(e.buf, '{')
	if err := e.enter(); err != nil {
		return err
	}
	first := true
fieldLoop:
	for i := range fields.list {
		f := &fields.list[i]
		fv := rv
		for _, idx := range f.index {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue fieldLoop
				}
				fv = fv.Elem()
			}
			fv = fv.Field(idx)
		}
		if (f.omitEmpty && isEmptyValue(fv)) || (f.omitZero && f.isZero(fv)) {
			continue
		}
		mark := e.memberStart(first, f.name)
		ok, err := e.reflectValue(fv, f.name, f.quoted)
		if err != nil {
			return err
		}
		if ok {
			first = false
		} else {
			e.buf = e.buf[:mark]
		}
	}
	e.depth--
	if !first {
		e.newline()
	}
	e.buf = append(e.buf, '}')
	return nil
}

func (e *encoder) mapValue(rv reflect.Value, key any) error {
	t := rv.Type()
	kt := t.Key()
	switch kt.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !kt.Implements(textMarshalerType) {
			return &json.UnsupportedTypeError{Type: t}
		}
	}
	if err := e.push(stackKey{ptr: uintptr(rv.UnsafePointer()), typ: t}, key, "Object"); err != nil {
		return err
	}
	defer e.pop()
	type kv struct {
		ks string
		v  reflect.Value
	}
	pairs := make([]kv, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := iter.Key()
		var ks string
		switch {
		case k.Kind() == reflect.String:
			ks = k.String()
		case kt.Implements(textMarshalerType):
			if k.Kind() == reflect.Pointer && k.IsNil() {
				ks = ""
				break
			}
			b, err := k.Interface().(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return &json.MarshalerError{Type: kt, Err: err}
			}
			ks = string(b)
		case k.CanInt():
			ks = strconv.FormatInt(k.Int(), 10)
		default:
			ks = strconv.FormatUint(k.Uint(), 10)
		}
		pairs = append(pairs, kv{ks, iter.Value()})
	}
	keys := make([]string, len(pairs))
	byKey := make(map[string]reflect.Value, len(pairs))
	for i, p := range pairs {
		keys[i] = p.ks
		byKey[p.ks] = p.v
	}
	sortMapKeys(keys)
	e.buf = append(e.buf, '{')
	if err := e.enter(); err != nil {
		return err
	}
	first := true
	for _, k := range keys {
		mark := e.memberStart(first, k)
		ok, err := e.reflectValue(byKey[k], k, false)
		if err != nil {
			return err
		}
		if ok {
			first = false
		} else {
			e.buf = e.buf[:mark]
		}
	}
	e.depth--
	if !first {
		e.newline()
	}
	e.buf = append(e.buf, '}')
	return nil
}
