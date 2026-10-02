package jsonx

import (
	"encoding/json"
	"fmt"
	"iter"
	"sort"
)

// Object is an ordered JSON object with JavaScript property order: keys that are array
// indices ("0" .. "4294967294" in canonical form) come first in ascending numeric order,
// then every other key in insertion order. Setting an existing key keeps its position,
// exactly like JS property assignment. The zero value is an empty object ready to use.
//
// Values follow the untyped model (nil, bool, float64, string, []any, *Object); typed Go
// values may also be stored and are serialized by Stringify/Marshal.
//
// Object is not safe for concurrent mutation.
type Object struct {
	// entries holds the properties in JS property order: entries[:nIdx] are the
	// array-index keys sorted numerically, entries[nIdx:] the named keys.
	entries []objectEntry
	nIdx    int
	// named maps a named key to its position relative to nIdx. It is non-nil exactly
	// when there are more than objectIndexThreshold named keys, so the struct is a pure
	// function of its entries.
	named map[string]int
}

type objectEntry struct {
	key string
	val any
}

// objectIndexThreshold is the named-key count above which lookups use a map.
const objectIndexThreshold = 8

// NewObject returns an empty object.
func NewObject() *Object { return &Object{} }

// ObjectOf builds an object from alternating keys and values, in order:
// ObjectOf("type", "text", "text", s) is the JS literal {type: "text", text: s}.
// It panics if the argument count is odd or a key is not a string.
func ObjectOf(kv ...any) *Object {
	if len(kv)%2 != 0 {
		panic("jsonx.ObjectOf: odd number of arguments")
	}
	o := &Object{}
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			panic(fmt.Sprintf("jsonx.ObjectOf: key %d is %T, not string", i/2, kv[i]))
		}
		o.Set(k, kv[i+1])
	}
	return o
}

// IsArrayIndex reports whether key is a canonical array index ("0" .. "4294967294"),
// the keys JavaScript orders numerically before all other own properties.
func IsArrayIndex(key string) bool {
	n := len(key)
	if n == 0 || n > 10 {
		return false
	}
	if key[0] == '0' {
		return n == 1
	}
	for i := 0; i < n; i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	return n < 10 || key <= "4294967294"
}

// indexKeyLess compares two canonical array-index keys numerically.
func indexKeyLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// OrderKeys returns keys (given in insertion order, without duplicates) in JavaScript
// property order: array indices ascending first, then the rest in their given order.
func OrderKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if IsArrayIndex(k) {
			out = append(out, k)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return indexKeyLess(out[i], out[j]) })
	for _, k := range keys {
		if !IsArrayIndex(k) {
			out = append(out, k)
		}
	}
	return out
}

// find returns the position of key in entries, or -1.
func (o *Object) find(key string) int {
	if IsArrayIndex(key) {
		idx := o.entries[:o.nIdx]
		i := sort.Search(len(idx), func(i int) bool { return !indexKeyLess(idx[i].key, key) })
		if i < len(idx) && idx[i].key == key {
			return i
		}
		return -1
	}
	if o.named != nil {
		if p, ok := o.named[key]; ok {
			return o.nIdx + p
		}
		return -1
	}
	for i := o.nIdx; i < len(o.entries); i++ {
		if o.entries[i].key == key {
			return i
		}
	}
	return -1
}

func (o *Object) rebuildNamed() {
	n := len(o.entries) - o.nIdx
	if n <= objectIndexThreshold {
		o.named = nil
		return
	}
	o.named = make(map[string]int, n)
	for i, e := range o.entries[o.nIdx:] {
		o.named[e.key] = i
	}
}

// Set assigns key = v (JS assignment): an existing key keeps its position, a new key is
// added in property order. Go integer and float32 values are stored as float64, since
// every JS number is a double.
func (o *Object) Set(key string, v any) {
	o.set(key, normalizeNumber(v))
}

func (o *Object) set(key string, v any) {
	if p := o.find(key); p >= 0 {
		o.entries[p].val = v
		return
	}
	if IsArrayIndex(key) {
		idx := o.entries[:o.nIdx]
		i := sort.Search(len(idx), func(i int) bool { return indexKeyLess(key, idx[i].key) })
		o.entries = append(o.entries, objectEntry{})
		copy(o.entries[i+1:], o.entries[i:])
		o.entries[i] = objectEntry{key: key, val: v}
		o.nIdx++
		return
	}
	o.entries = append(o.entries, objectEntry{key: key, val: v})
	if o.named != nil {
		o.named[key] = len(o.entries) - 1 - o.nIdx
	} else if len(o.entries)-o.nIdx > objectIndexThreshold {
		o.rebuildNamed()
	}
}

// Get returns the value of key and whether the key exists.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	if p := o.find(key); p >= 0 {
		return o.entries[p].val, true
	}
	return nil, false
}

// Has reports whether key exists (JS `key in obj` for own properties).
func (o *Object) Has(key string) bool {
	return o != nil && o.find(key) >= 0
}

// Delete removes key (JS `delete obj[key]`) and reports whether it existed.
func (o *Object) Delete(key string) bool {
	if o == nil {
		return false
	}
	p := o.find(key)
	if p < 0 {
		return false
	}
	copy(o.entries[p:], o.entries[p+1:])
	o.entries[len(o.entries)-1] = objectEntry{}
	o.entries = o.entries[:len(o.entries)-1]
	if p < o.nIdx {
		o.nIdx--
		return true
	}
	if o.named != nil {
		if p == len(o.entries) && len(o.entries)-o.nIdx > objectIndexThreshold {
			delete(o.named, key)
		} else {
			o.rebuildNamed()
		}
	}
	return true
}

// Len returns the number of properties.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.entries)
}

// Keys returns the keys in JS property order (Object.keys).
func (o *Object) Keys() []string {
	if o == nil {
		return []string{}
	}
	keys := make([]string, len(o.entries))
	for i, e := range o.entries {
		keys[i] = e.key
	}
	return keys
}

// Values returns the values in JS property order (Object.values).
func (o *Object) Values() []any {
	if o == nil {
		return []any{}
	}
	vals := make([]any, len(o.entries))
	for i, e := range o.entries {
		vals[i] = e.val
	}
	return vals
}

// All iterates over the properties in JS property order (Object.entries). The object
// must not gain or lose keys during iteration; assigning existing keys is fine.
func (o *Object) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if o == nil {
			return
		}
		for i := 0; i < len(o.entries); i++ {
			if !yield(o.entries[i].key, o.entries[i].val) {
				return
			}
		}
	}
}

// Clone returns a deep copy. Nested *Object and []any values are copied recursively;
// other values are copied as-is.
func (o *Object) Clone() *Object {
	if o == nil {
		return nil
	}
	c := &Object{entries: make([]objectEntry, len(o.entries)), nIdx: o.nIdx}
	for i, e := range o.entries {
		c.entries[i] = objectEntry{key: e.key, val: CloneValue(e.val)}
	}
	c.rebuildNamed()
	return c
}

// Assign copies every property of src into o in src's property order, like
// Object.assign(o, src) or the spread {...o, ...src}. It returns o.
func (o *Object) Assign(src *Object) *Object {
	if src == nil {
		return o
	}
	for i := 0; i < len(src.entries); i++ {
		o.set(src.entries[i].key, src.entries[i].val)
	}
	return o
}

// Equal reports deep equality with other, ignoring key order (the semantics of Jest's
// toEqual). go-cmp uses this method, so cmp.Diff works on values containing *Object.
func (o *Object) Equal(other *Object) bool {
	if o == nil || other == nil {
		return o == nil && other == nil
	}
	if len(o.entries) != len(other.entries) {
		return false
	}
	for _, e := range o.entries {
		ov, ok := other.Get(e.key)
		if !ok || !DeepEqual(e.val, ov) {
			return false
		}
	}
	return true
}

// String returns the compact JSON text of the object.
func (o *Object) String() string {
	s, err := Stringify(o)
	if err != nil {
		return "<jsonx.Object: " + err.Error() + ">"
	}
	return s
}

// MarshalJSON implements json.Marshaler with JSON.stringify output.
func (o *Object) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	return Marshal(o)
}

// UnmarshalJSON implements json.Unmarshaler using JSON.parse semantics. A JSON null
// leaves the object unchanged; any other non-object value is an error.
func (o *Object) UnmarshalJSON(data []byte) error {
	v, err := Parse(data)
	if err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		return nil
	case *Object:
		*o = *x
		return nil
	default:
		return &json.UnmarshalTypeError{Value: kindName(v), Type: objectType}
	}
}

// GetString returns the value of key if it is a string.
func (o *Object) GetString(key string) (string, bool) {
	v, _ := o.Get(key)
	s, ok := v.(string)
	return s, ok
}

// GetNumber returns the value of key if it is a number.
func (o *Object) GetNumber(key string) (float64, bool) {
	v, _ := o.Get(key)
	f, ok := v.(float64)
	return f, ok
}

// GetBool returns the value of key if it is a boolean.
func (o *Object) GetBool(key string) (bool, bool) {
	v, _ := o.Get(key)
	b, ok := v.(bool)
	return b, ok
}

// GetObject returns the value of key if it is a non-null object.
func (o *Object) GetObject(key string) (*Object, bool) {
	v, _ := o.Get(key)
	m, ok := v.(*Object)
	return m, ok && m != nil
}

// GetArray returns the value of key if it is an array.
func (o *Object) GetArray(key string) ([]any, bool) {
	v, _ := o.Get(key)
	a, ok := v.([]any)
	return a, ok
}

// CloneValue deep-copies an untyped JSON value: *Object and []any are copied
// recursively, everything else is returned as-is.
func CloneValue(v any) any {
	switch x := v.(type) {
	case *Object:
		return x.Clone()
	case []any:
		if x == nil {
			return x
		}
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = CloneValue(e)
		}
		return c
	default:
		return v
	}
}

// normalizeNumber converts Go numeric values to float64 (JS numbers are doubles).
func normalizeNumber(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case uintptr:
		return float64(x)
	case float32:
		return float64(x)
	}
	return v
}

// kindName names the JSON kind of an untyped value, as encoding/json error messages do.
func kindName(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number " + formatNumber(x)
	case string:
		return "string"
	case []any:
		return "array"
	case *Object:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
