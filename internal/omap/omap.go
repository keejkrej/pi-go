package omap

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"reflect"
	"slices"
	"strconv"

	"github.com/keejkrej/pi-go/internal/js"
)

type mapEntry[K comparable, V any] struct {
	key     K
	value   V
	deleted bool
}

// Map is an insertion-ordered map with JS Map semantics. The zero value is an
// empty map ready to use. Read methods accept a nil *Map and treat it as empty.
type Map[K comparable, V any] struct {
	entries   []*mapEntry[K, V]
	index     map[K]*mapEntry[K, V]
	deleted   int
	iterating int
}

// NewMap returns an empty Map (new Map()).
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{}
}

// Get returns the value stored for k (map.get(k)) and whether k is present.
func (m *Map[K, V]) Get(k K) (V, bool) {
	if m == nil || m.index == nil {
		var zero V
		return zero, false
	}
	e, ok := m.index[k]
	if !ok {
		var zero V
		return zero, false
	}
	return e.value, true
}

// Set stores v under k (map.set(k, v)). An existing key keeps its position; a
// new key is appended.
func (m *Map[K, V]) Set(k K, v V) {
	if m.index == nil {
		m.index = make(map[K]*mapEntry[K, V])
	}
	if e, ok := m.index[k]; ok {
		e.value = v
		return
	}
	e := &mapEntry[K, V]{key: k, value: v}
	m.index[k] = e
	m.entries = append(m.entries, e)
}

// Delete removes k (map.delete(k)) and reports whether it was present.
func (m *Map[K, V]) Delete(k K) bool {
	if m == nil || m.index == nil {
		return false
	}
	e, ok := m.index[k]
	if !ok {
		return false
	}
	delete(m.index, k)
	e.deleted = true
	var zero V
	e.value = zero
	m.deleted++
	m.maybeCompact()
	return true
}

// Has reports whether k is present (map.has(k)).
func (m *Map[K, V]) Has(k K) bool {
	if m == nil || m.index == nil {
		return false
	}
	_, ok := m.index[k]
	return ok
}

// Len returns the number of entries (map.size).
func (m *Map[K, V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.index)
}

// Clear removes every entry (map.clear()). An iteration in progress ends,
// unless entries are added afterwards, which it then visits (JS semantics).
func (m *Map[K, V]) Clear() {
	if m == nil {
		return
	}
	if m.iterating > 0 {
		for _, e := range m.entries {
			if !e.deleted {
				e.deleted = true
				var zero V
				e.value = zero
				m.deleted++
			}
		}
	} else {
		m.entries = nil
		m.deleted = 0
	}
	m.index = nil
}

// Keys returns a snapshot of the keys in insertion order ([...map.keys()]).
func (m *Map[K, V]) Keys() []K {
	if m == nil {
		return nil
	}
	out := make([]K, 0, len(m.index))
	for _, e := range m.entries {
		if !e.deleted {
			out = append(out, e.key)
		}
	}
	return out
}

// Values returns a snapshot of the values in insertion order ([...map.values()]).
func (m *Map[K, V]) Values() []V {
	if m == nil {
		return nil
	}
	out := make([]V, 0, len(m.index))
	for _, e := range m.entries {
		if !e.deleted {
			out = append(out, e.value)
		}
	}
	return out
}

// All iterates the live entries in insertion order (for (const [k, v] of map)).
// Entries deleted before they are reached are skipped, and entries added
// during the iteration are visited.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m == nil {
			return
		}
		m.iterating++
		defer func() {
			m.iterating--
			m.maybeCompact()
		}()
		for i := 0; i < len(m.entries); i++ {
			e := m.entries[i]
			if e.deleted {
				continue
			}
			if !yield(e.key, e.value) {
				return
			}
		}
	}
}

// Clone returns a shallow copy with the same order (new Map(map)).
func (m *Map[K, V]) Clone() *Map[K, V] {
	out := NewMap[K, V]()
	if m == nil {
		return out
	}
	for _, e := range m.entries {
		if !e.deleted {
			out.Set(e.key, e.value)
		}
	}
	return out
}

// ObjectKeys returns the keys in JS property order, the order Object.keys and
// JSON.stringify use for the plain object a TS Record represents: array-index
// keys ascending first, then the rest in insertion order. It fails for key
// types that have no JSON object key form.
func (m *Map[K, V]) ObjectKeys() ([]K, error) {
	if m == nil {
		return nil, nil
	}
	ordered, err := m.objectOrder()
	if err != nil {
		return nil, err
	}
	out := make([]K, len(ordered))
	for i, item := range ordered {
		out[i] = item.entry.key
	}
	return out, nil
}

func (m *Map[K, V]) maybeCompact() {
	if m.iterating > 0 || m.deleted == 0 {
		return
	}
	if m.deleted < 32 && m.deleted*2 < len(m.entries) {
		return
	}
	live := m.entries[:0]
	for _, e := range m.entries {
		if !e.deleted {
			live = append(live, e)
		}
	}
	clear(m.entries[len(live):])
	m.entries = live
	m.deleted = 0
}

type objectItem[K comparable, V any] struct {
	name  string
	entry *mapEntry[K, V]
}

func (m *Map[K, V]) objectOrder() ([]objectItem[K, V], error) {
	var indexed, named []objectItem[K, V]
	for _, e := range m.entries {
		if e.deleted {
			continue
		}
		name, err := keyString(e.key)
		if err != nil {
			return nil, err
		}
		item := objectItem[K, V]{name: name, entry: e}
		if _, ok := js.ArrayIndex(name); ok {
			indexed = append(indexed, item)
		} else {
			named = append(named, item)
		}
	}
	if len(indexed) == 0 {
		return named, nil
	}
	slices.SortStableFunc(indexed, func(a, b objectItem[K, V]) int {
		ai, _ := js.ArrayIndex(a.name)
		bi, _ := js.ArrayIndex(b.name)
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		}
		return 0
	})
	return append(indexed, named...), nil
}

// MarshalJSON encodes the map as a JSON object in JS property order (see
// ObjectKeys). Values are encoded with encoding/json without HTML escaping.
func (m *Map[K, V]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	ordered, err := m.objectOrder()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, item := range ordered {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encodeJSONValue(&buf, item.name); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := encodeJSONValue(&buf, item.entry.value); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object, replacing the current contents. Keys are
// inserted in the order JSON.parse exposes them (array-index keys ascending
// first, then document order); a duplicate key keeps its first position and
// its last value. Values decode with encoding/json, so untyped values should
// use jsonx types rather than any.
func (m *Map[K, V]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("omap: cannot unmarshal %s into Map", jsonKind(tok))
	}
	var indexed, named []string
	values := make(map[string]V)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := tok.(string)
		if !ok {
			return errors.New("omap: invalid object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if _, seen := values[name]; !seen {
			if _, isIndex := js.ArrayIndex(name); isIndex {
				indexed = append(indexed, name)
			} else {
				named = append(named, name)
			}
		}
		values[name] = v
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	slices.SortStableFunc(indexed, func(a, b string) int {
		ai, _ := js.ArrayIndex(a)
		bi, _ := js.ArrayIndex(b)
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		}
		return 0
	})
	m.Clear()
	for _, name := range append(indexed, named...) {
		k, err := parseKey[K](name)
		if err != nil {
			return err
		}
		m.Set(k, values[name])
	}
	return nil
}

func jsonKind(tok json.Token) string {
	switch tok.(type) {
	case json.Delim:
		return "array"
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", tok)
}

func encodeJSONValue(buf *bytes.Buffer, v any) error {
	if cleaned, ok := jsonFinite(v); ok {
		v = cleaned
	}
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Write(jsonStringify(bytes.TrimSuffix(tmp.Bytes(), []byte("\n"))))
	return nil
}

// jsonFinite replaces NaN and ±Inf with null and clears -0, including inside
// []any, map[string]any, and float slices. The bool reports whether v changed.
// Struct fields are left alone: encoding/json still rejects a NaN or Inf field.
// -0 in a struct is rewritten later from the "-0" token.
func jsonFinite(v any) (any, bool) {
	switch x := v.(type) {
	case float32:
		if y, ok := cleanFloat(float64(x), true); ok {
			return y, true
		}
	case float64:
		if y, ok := cleanFloat(x, false); ok {
			return y, true
		}
	case *float32:
		if x == nil {
			return nil, false
		}
		if y, ok := cleanFloat(float64(*x), true); ok {
			return y, true
		}
	case *float64:
		if x == nil {
			return nil, false
		}
		if y, ok := cleanFloat(*x, false); ok {
			return y, true
		}
	case []float32:
		for _, n := range x {
			if _, ok := cleanFloat(float64(n), true); ok {
				out := make([]any, len(x))
				for i, m := range x {
					if y, changed := cleanFloat(float64(m), true); changed {
						out[i] = y
					} else {
						out[i] = m
					}
				}
				return out, true
			}
		}
	case []float64:
		for _, n := range x {
			if _, ok := cleanFloat(n, false); ok {
				out := make([]any, len(x))
				for i, m := range x {
					if y, changed := cleanFloat(m, false); changed {
						out[i] = y
					} else {
						out[i] = m
					}
				}
				return out, true
			}
		}
	case []any:
		var out []any
		for i, e := range x {
			c, changed := jsonFinite(e)
			if changed && out == nil {
				out = make([]any, len(x))
				copy(out, x)
			}
			if out != nil {
				out[i] = c
			}
		}
		if out != nil {
			return out, true
		}
	case map[string]any:
		var out map[string]any
		for k, e := range x {
			c, changed := jsonFinite(e)
			if changed && out == nil {
				out = make(map[string]any, len(x))
				for k2, e2 := range x {
					out[k2] = e2
				}
			}
			if out != nil {
				out[k] = c
			}
		}
		if out != nil {
			return out, true
		}
	}
	return v, false
}

func cleanFloat(x float64, bits32 bool) (any, bool) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil, true
	}
	if x == 0 && math.Signbit(x) {
		if bits32 {
			return float32(0), true
		}
		return 0.0, true
	}
	return nil, false
}

// jsonStringify rewrites encoding/json toward JSON.stringify: U+2028 and
// U+2029 stay raw inside strings (Go escapes them as \u2028 / \u2029), and a
// numeric -0 is 0. A literal "\\u2028" is left escaped.
func jsonStringify(src []byte) []byte {
	if !bytes.Contains(src, []byte(`\u202`)) && !bytes.Contains(src, []byte("-0")) {
		return src
	}
	dst := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		if src[i] != '"' {
			if src[i] == '-' && i+1 < len(src) && src[i+1] == '0' &&
				(i == 0 || jsonDelim(src[i-1])) &&
				(i+2 == len(src) || !jsonNumberCont(src[i+2])) {
				dst = append(dst, '0')
				i += 2
				continue
			}
			dst = append(dst, src[i])
			i++
			continue
		}
		dst = append(dst, '"')
		i++
		for i < len(src) {
			if src[i] == '"' {
				dst = append(dst, '"')
				i++
				break
			}
			if src[i] == '\\' && i+5 < len(src) && src[i+1] == 'u' &&
				src[i+2] == '2' && src[i+3] == '0' && src[i+4] == '2' &&
				(src[i+5] == '8' || src[i+5] == '9') {
				if src[i+5] == '8' {
					dst = append(dst, 0xE2, 0x80, 0xA8)
				} else {
					dst = append(dst, 0xE2, 0x80, 0xA9)
				}
				i += 6
				continue
			}
			if src[i] == '\\' && i+1 < len(src) {
				dst = append(dst, '\\', src[i+1])
				i += 2
				continue
			}
			dst = append(dst, src[i])
			i++
		}
	}
	return dst
}

func jsonDelim(c byte) bool {
	switch c {
	case '{', '}', '[', ']', ':', ',', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

func jsonNumberCont(c byte) bool {
	return c == '.' || c == 'e' || c == 'E' || (c >= '0' && c <= '9')
}

// keyString converts a key to its JS property-key string.
func keyString[K comparable](k K) (string, error) {
	if tm, ok := any(k).(encoding.TextMarshaler); ok {
		b, err := tm.MarshalText()
		return string(b), err
	}
	rv := reflect.ValueOf(k)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return js.NumberToString(rv.Float()), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	}
	return "", fmt.Errorf("omap: unsupported key type %T for JSON", k)
}

func parseKey[K comparable](name string) (K, error) {
	var k K
	if tu, ok := any(&k).(encoding.TextUnmarshaler); ok {
		err := tu.UnmarshalText([]byte(name))
		return k, err
	}
	rv := reflect.ValueOf(&k).Elem()
	switch rv.Kind() {
	case reflect.String:
		rv.SetString(name)
		return k, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(name, 10, rv.Type().Bits())
		if err != nil {
			return k, fmt.Errorf("omap: invalid key %q: %w", name, err)
		}
		rv.SetInt(n)
		return k, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(name, 10, rv.Type().Bits())
		if err != nil {
			return k, fmt.Errorf("omap: invalid key %q: %w", name, err)
		}
		rv.SetUint(n)
		return k, nil
	case reflect.Float32, reflect.Float64:
		f := js.ToNumber(name)
		if js.NumberToString(f) != name {
			return k, fmt.Errorf("omap: invalid key %q", name)
		}
		rv.SetFloat(f)
		return k, nil
	case reflect.Bool:
		b, err := strconv.ParseBool(name)
		if err != nil || (name != "true" && name != "false") {
			return k, fmt.Errorf("omap: invalid key %q", name)
		}
		rv.SetBool(b)
		return k, nil
	}
	return k, fmt.Errorf("omap: unsupported key type %T for JSON", k)
}
