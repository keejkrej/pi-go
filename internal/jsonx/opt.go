package jsonx

import "reflect"

// Opt holds a persisted `T | null` value with three states: absent (the zero value),
// null, and a value. Absent and null both marshal as JSON null; with the `omitzero` tag
// option an absent Opt omits the field (TS `foo?: T | null`). Unmarshaling null gives
// the null state, any other JSON value gives a value.
type Opt[T any] struct {
	value T
	state optState
}

type optState uint8

const (
	optAbsent optState = iota
	optNull
	optSome
)

// Some returns an Opt holding v.
func Some[T any](v T) Opt[T] { return Opt[T]{value: v, state: optSome} }

// Null returns an Opt in the null state.
func Null[T any]() Opt[T] { return Opt[T]{state: optNull} }

// IsZero reports whether the Opt is absent. encoding/json and jsonx call it for omitzero.
func (o Opt[T]) IsZero() bool { return o.state == optAbsent }

// IsNull reports whether the Opt is explicitly null (absent is not null).
func (o Opt[T]) IsNull() bool { return o.state == optNull }

// IsSome reports whether the Opt holds a value.
func (o Opt[T]) IsSome() bool { return o.state == optSome }

// Get returns the value and true, or the zero T and false when absent or null.
func (o Opt[T]) Get() (T, bool) {
	if o.state != optSome {
		var zero T
		return zero, false
	}
	return o.value, true
}

// OrElse returns the value, or def when absent or null (TS `x ?? def`).
func (o Opt[T]) OrElse(def T) T {
	if o.state != optSome {
		return def
	}
	return o.value
}

// Equal reports whether both Opts are in the same state with equal values (DeepEqual
// semantics). go-cmp uses this method.
func (o Opt[T]) Equal(other Opt[T]) bool {
	if o.state != other.state {
		return false
	}
	return o.state != optSome || DeepEqual(any(o.value), any(other.value))
}

// MarshalJSON implements json.Marshaler with JSON.stringify output.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if o.state != optSome {
		return []byte("null"), nil
	}
	b, err := Marshal(o.value)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return []byte("null"), nil
	}
	return b, nil
}

// UnmarshalJSON implements json.Unmarshaler with JSON.parse semantics.
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	v, err := Parse(data)
	if err != nil {
		return err
	}
	return Decode(v, o)
}

// optEncoder lets the encoder see through Opt without calling MarshalJSON.
type optEncoder interface {
	jsonxOpt() (any, bool)
}

func (o Opt[T]) jsonxOpt() (any, bool) {
	if o.state != optSome {
		return nil, false
	}
	return o.value, true
}

// optDecoder lets the decoder fill an Opt directly from an untyped value.
type optDecoder interface {
	jsonxDecodeOpt(d *decoder, src any)
}

func (o *Opt[T]) jsonxDecodeOpt(d *decoder, src any) {
	if src == nil {
		*o = Null[T]()
		return
	}
	var v T
	d.value(src, reflect.ValueOf(&v).Elem())
	*o = Some(v)
}
