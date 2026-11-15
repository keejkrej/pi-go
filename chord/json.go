// Ported from packages/chord/src/json.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// CopyJsonOptions controls strict-JSON copying.
type CopyJsonOptions struct {
	// OmitUndefinedProperties omits undefined object properties while preserving strict array semantics.
	OmitUndefinedProperties *bool `json:"omitUndefinedProperties,omitzero"`
}

// jsonSliceKey identifies a non-empty []any slice backing range for cycle detection.
type jsonSliceKey struct {
	ptr *any
	len int
}

// CopyJson copies a value into an alias-free strict-JSON tree owned by the caller.
// A nil options pointer means the defaults.
func CopyJson(value any, options *CopyJsonOptions) (JsonValue, error) {
	omitUndefined := options != nil && options.OmitUndefinedProperties != nil && *options.OmitUndefinedProperties
	return jsonCopy(value, nil, nil, omitUndefined)
}

func jsonCopy(
	value any,
	objAncestors map[*jsonx.Object]struct{},
	sliceAncestors map[jsonSliceKey]struct{},
	omitUndefinedProperties bool,
) (JsonValue, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case string, bool:
		return v, nil
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, nil
		}
		return nil, js.NewTypeError("Value contains a non-finite number and is not strict JSON")
	case []any:
		if v == nil {
			return nil, js.NewTypeError("Value must contain strict JSON dense plain arrays")
		}
		var key jsonSliceKey
		if len(v) > 0 {
			if sliceAncestors == nil {
				sliceAncestors = make(map[jsonSliceKey]struct{})
			}
			key = jsonSliceKey{ptr: unsafe.SliceData(v), len: len(v)}
			if _, seen := sliceAncestors[key]; seen {
				return nil, js.NewTypeError("Value contains cycles and is not strict JSON")
			}
			sliceAncestors[key] = struct{}{}
			defer delete(sliceAncestors, key)
		}
		result := make([]any, len(v))
		for i, elem := range v {
			copied, err := jsonCopy(elem, objAncestors, sliceAncestors, omitUndefinedProperties)
			if err != nil {
				return nil, err
			}
			result[i] = copied
		}
		return result, nil
	case *jsonx.Object:
		if v == nil {
			return nil, js.NewTypeError("Value must contain strict JSON plain objects or arrays")
		}
		if objAncestors == nil {
			objAncestors = make(map[*jsonx.Object]struct{})
		}
		if _, seen := objAncestors[v]; seen {
			return nil, js.NewTypeError("Value contains cycles and is not strict JSON")
		}
		objAncestors[v] = struct{}{}
		defer delete(objAncestors, v)

		result := jsonx.NewObject()
		for key, elem := range v.All() {
			copied, err := jsonCopy(elem, objAncestors, sliceAncestors, omitUndefinedProperties)
			if err != nil {
				return nil, err
			}
			result.Set(key, copied)
		}
		return result, nil
	default:
		_ = omitUndefinedProperties
		return nil, js.NewTypeError(fmt.Sprintf("Value contains a non-JSON %T; expected strict JSON", value))
	}
}

// IsJsonValue reports whether value is finite strict JSON with plain objects and no cycles.
func IsJsonValue(value any) bool {
	return jsonCheck(value, nil, nil)
}

func jsonCheck(
	value any,
	objAncestors map[*jsonx.Object]struct{},
	sliceAncestors map[jsonSliceKey]struct{},
) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string, bool:
		return true
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	case []any:
		if v == nil {
			return false
		}
		if len(v) > 0 {
			if sliceAncestors == nil {
				sliceAncestors = make(map[jsonSliceKey]struct{})
			}
			key := jsonSliceKey{ptr: unsafe.SliceData(v), len: len(v)}
			if _, seen := sliceAncestors[key]; seen {
				return false
			}
			sliceAncestors[key] = struct{}{}
			defer delete(sliceAncestors, key)
		}
		for _, elem := range v {
			if !jsonCheck(elem, objAncestors, sliceAncestors) {
				return false
			}
		}
		return true
	case *jsonx.Object:
		if v == nil {
			return false
		}
		if objAncestors == nil {
			objAncestors = make(map[*jsonx.Object]struct{})
		}
		if _, seen := objAncestors[v]; seen {
			return false
		}
		objAncestors[v] = struct{}{}
		defer delete(objAncestors, v)
		for _, elem := range v.All() {
			if !jsonCheck(elem, objAncestors, sliceAncestors) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
