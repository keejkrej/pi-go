package omap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
)

// Set is an insertion-ordered set with JS Set semantics. The zero value is an
// empty set ready to use. Read methods accept a nil *Set and treat it as empty.
type Set[T comparable] struct {
	m Map[T, struct{}]
}

// NewSet returns an empty Set (new Set()).
func NewSet[T comparable]() *Set[T] {
	return &Set[T]{}
}

// NewSetOf returns a Set holding values in order, duplicates dropped
// (new Set(values)).
func NewSetOf[T comparable](values ...T) *Set[T] {
	s := &Set[T]{}
	for _, v := range values {
		s.Add(v)
	}
	return s
}

// Add inserts v (set.add(v)). An existing value keeps its position.
func (s *Set[T]) Add(v T) {
	s.m.Set(v, struct{}{})
}

// Has reports whether v is present (set.has(v)).
func (s *Set[T]) Has(v T) bool {
	if s == nil {
		return false
	}
	return s.m.Has(v)
}

// Delete removes v (set.delete(v)) and reports whether it was present.
func (s *Set[T]) Delete(v T) bool {
	if s == nil {
		return false
	}
	return s.m.Delete(v)
}

// Len returns the number of values (set.size).
func (s *Set[T]) Len() int {
	if s == nil {
		return 0
	}
	return s.m.Len()
}

// Clear removes every value (set.clear()).
func (s *Set[T]) Clear() {
	if s == nil {
		return
	}
	s.m.Clear()
}

// Values returns a snapshot of the values in insertion order ([...set]).
func (s *Set[T]) Values() []T {
	if s == nil {
		return nil
	}
	return s.m.Keys()
}

// All iterates the values in insertion order with JS Set iteration semantics
// (see Map.All).
func (s *Set[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		if s == nil {
			return
		}
		for v := range s.m.All() {
			if !yield(v) {
				return
			}
		}
	}
}

// Clone returns a copy with the same order (new Set(set)).
func (s *Set[T]) Clone() *Set[T] {
	out := NewSet[T]()
	if s == nil {
		return out
	}
	for v := range s.m.All() {
		out.Add(v)
	}
	return out
}

// MarshalJSON encodes the set as a JSON array in insertion order, the shape TS
// code persists with [...set] or Array.from(set).
func (s *Set[T]) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	i := 0
	for v := range s.m.All() {
		if i > 0 {
			buf.WriteByte(',')
		}
		i++
		if err := encodeJSONValue(&buf, v); err != nil {
			return nil, err
		}
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes a JSON array, replacing the current contents
// (new Set(array)).
func (s *Set[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return fmt.Errorf("omap: cannot unmarshal non-array JSON into Set")
	}
	var values []T
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return err
	}
	s.m.Clear()
	for _, v := range values {
		s.Add(v)
	}
	return nil
}
