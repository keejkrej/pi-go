package omap

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
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
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
	return nil
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
