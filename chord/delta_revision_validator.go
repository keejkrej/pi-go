// Ported from packages/chord/src/delta/revision-validator.ts (pi v1.0.0).

package chord

import (
	"math"
	"sync"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type drvSliceKey struct {
	ptr *any
	len int
}

// JsonRevisionValidator validates immutable replica revisions, skipping containers
// validated in earlier revisions. The validated-container set is a TS WeakSet;
// the implementation owns that storage. The validator is safe for shared use.
type JsonRevisionValidator struct {
	mu               sync.Mutex
	validatedObjects map[*jsonx.Object]struct{}
	validatedSlices  map[drvSliceKey]struct{}
}

// NewJsonRevisionValidator returns an empty validator.
func NewJsonRevisionValidator() *JsonRevisionValidator {
	return &JsonRevisionValidator{
		validatedObjects: make(map[*jsonx.Object]struct{}),
		validatedSlices:  make(map[drvSliceKey]struct{}),
	}
}

// Validate checks value as strict JSON without cycles and returns it unchanged.
func (v *JsonRevisionValidator) Validate(value JsonValue) (JsonValue, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.drvValidate(
		value,
		make(map[*jsonx.Object]struct{}),
		make(map[drvSliceKey]struct{}),
		make(map[*jsonx.Object]struct{}),
		make(map[drvSliceKey]struct{}),
	); err != nil {
		return nil, err
	}
	return value, nil
}

func (v *JsonRevisionValidator) drvValidate(
	value JsonValue,
	ancObj map[*jsonx.Object]struct{},
	ancSlice map[drvSliceKey]struct{},
	finObj map[*jsonx.Object]struct{},
	finSlice map[drvSliceKey]struct{},
) error {
	if value == nil {
		return nil
	}
	switch val := value.(type) {
	case bool, string:
		return nil
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return js.NewTypeError("Replicated state values must be strict JSON")
		}
		return nil
	case []any:
		if val == nil {
			return js.NewTypeError("Replicated state arrays must be dense and contain only indexed entries")
		}
		if len(val) == 0 {
			return nil
		}
		key := drvSliceKey{ptr: unsafe.SliceData(val), len: len(val)}
		if _, ok := v.validatedSlices[key]; ok {
			return nil
		}
		if _, ok := finSlice[key]; ok {
			return nil
		}
		if _, ok := ancSlice[key]; ok {
			return js.NewTypeError("Replicated state cannot contain cycles")
		}
		ancSlice[key] = struct{}{}
		defer delete(ancSlice, key)
		for _, elem := range val {
			if err := v.drvValidate(elem, ancObj, ancSlice, finObj, finSlice); err != nil {
				return err
			}
		}
		finSlice[key] = struct{}{}
		v.validatedSlices[key] = struct{}{}
		return nil
	case *jsonx.Object:
		if val == nil {
			return js.NewTypeError("Replicated state containers must be plain objects or arrays")
		}
		if _, ok := v.validatedObjects[val]; ok {
			return nil
		}
		if _, ok := finObj[val]; ok {
			return nil
		}
		if _, ok := ancObj[val]; ok {
			return js.NewTypeError("Replicated state cannot contain cycles")
		}
		ancObj[val] = struct{}{}
		defer delete(ancObj, val)
		for _, elem := range val.All() {
			if err := v.drvValidate(elem, ancObj, ancSlice, finObj, finSlice); err != nil {
				return err
			}
		}
		finObj[val] = struct{}{}
		v.validatedObjects[val] = struct{}{}
		return nil
	default:
		return js.NewTypeError("Replicated state values must be strict JSON")
	}
}
