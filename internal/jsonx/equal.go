package jsonx

import "reflect"

// DeepEqual reports whether a and b are equal JSON values: objects compare by their key
// sets and values regardless of key order (Jest toEqual semantics), arrays element-wise,
// numbers numerically (any Go numeric type; NaN equals NaN). Other Go values fall back to
// reflect.DeepEqual.
func DeepEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *Object:
		y, ok := b.(*Object)
		return ok && x.Equal(y)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !DeepEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	if isGoNumber(a) {
		if !isGoNumber(b) {
			return false
		}
		x := normalizeNumber(a).(float64)
		y := normalizeNumber(b).(float64)
		return x == y || (x != x && y != y)
	}
	return reflect.DeepEqual(a, b)
}

// isGoNumber reports whether v holds a Go integer or float value.
func isGoNumber(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return true
	}
	return false
}
