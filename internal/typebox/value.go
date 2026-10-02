package typebox

import (
	"math"
	"reflect"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func schemaObject(n any) (*jsonx.Object, bool) {
	switch x := n.(type) {
	case *Schema:
		if x == nil || x.boolSchema != nil {
			return nil, false
		}
		if x.Object == nil {
			return jsonx.NewObject(), true
		}
		return x.Object, true
	case *jsonx.Object:
		if x == nil {
			return nil, false
		}
		return x, true
	default:
		return nil, false
	}
}

func schemaBool(n any) (bool, bool) {
	switch x := n.(type) {
	case bool:
		return x, true
	case *Schema:
		if x != nil && x.boolSchema != nil {
			return *x.boolSchema, true
		}
	}
	return false, false
}

func isSchemaNode(n any) bool {
	if _, ok := schemaBool(n); ok {
		return true
	}
	_, ok := schemaObject(n)
	return ok
}

func getKey(n any, key string) (any, bool) {
	o, ok := schemaObject(n)
	if !ok {
		return nil, false
	}
	return o.Get(key)
}

func hasKey(n any, key string) bool {
	o, ok := schemaObject(n)
	return ok && o.Has(key)
}

func objectKey(n any, key string) (*jsonx.Object, bool) {
	v, ok := getKey(n, key)
	if !ok {
		return nil, false
	}
	return schemaObject(v)
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	default:
		return 0, false
	}
}

func finite(v any) (float64, bool) {
	f, ok := asFloat(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func isIntegerNumber(v any) bool {
	f, ok := finite(v)
	return ok && f == math.Trunc(f)
}

func dataObject(v any) (*jsonx.Object, bool) {
	o, ok := v.(*jsonx.Object)
	return o, ok && o != nil
}

func dataArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func isUnsafeKey(key string) bool {
	return key == "__proto__" || key == "constructor" || key == "prototype"
}

func normJSON(v any) any {
	if f, ok := asFloat(v); ok {
		return f
	}
	return v
}

func literalTypeName(v any) (string, bool) {
	switch v.(type) {
	case string:
		return "string", true
	case bool:
		return "boolean", true
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number", true
	default:
		return "", false
	}
}

func isValueLike(v any) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case bool, string:
		return true
	}
	_, ok := finite(v)
	return ok
}

func valueEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	af, aok := finite(a)
	bf, bok := finite(b)
	if aok || bok {
		return aok && bok && af == bf
	}
	return false
}

func deepEqual(a, b any) bool {
	if aa, aok := dataArray(a); aok {
		bb, bok := dataArray(b)
		if !bok || len(aa) != len(bb) {
			return false
		}
		for i := range aa {
			if !deepEqual(aa[i], bb[i]) {
				return false
			}
		}
		return true
	}
	if _, bok := dataArray(b); bok {
		return false
	}
	if oa, aok := dataObject(a); aok {
		ob, bok := dataObject(b)
		if !bok || oa.Len() != ob.Len() {
			return false
		}
		for _, k := range oa.Keys() {
			bv, ok := ob.Get(k)
			if !ok {
				return false
			}
			av, _ := oa.Get(k)
			if !deepEqual(av, bv) {
				return false
			}
		}
		return true
	}
	if _, bok := dataObject(b); bok {
		return false
	}
	if isValueLike(a) || isValueLike(b) {
		return valueEqual(a, b)
	}
	return reflect.DeepEqual(a, b)
}

func numString(v any) string {
	f, ok := asFloat(v)
	if !ok {
		return ""
	}
	return js.NumberToString(f)
}

func stringsOf(v any) []string {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(a))
	for i, el := range a {
		out[i], _ = el.(string)
	}
	return out
}

func escapeRegexp(key string) string {
	var b strings.Builder
	for _, r := range key {
		if strings.ContainsRune(`[.*+?^${}()|[\]\\]`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
