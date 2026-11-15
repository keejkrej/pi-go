// Ported from packages/durable/src/harness/json.ts (pi v1.0.0).

package durable

import "github.com/keejkrej/pi-go/internal/jsonx"

// hjAssignJson assigns value at target[key] leaf by leaf so Chord's diff emits
// string appends on growing string leaves instead of replacing whole containers.
func hjAssignJson(target *jsonx.Object, key string, value JsonValue) {
	if target == nil {
		return
	}
	hjAssignAt(
		func() (JsonValue, bool) { return target.Get(key) },
		func(v JsonValue) { target.Set(key, v) },
		value,
	)
}

func hjAssignAt(get func() (JsonValue, bool), set func(JsonValue), value JsonValue) {
	value = hjNormalizeValue(value)
	current, exists := get()
	if !exists {
		set(value)
		return
	}
	if curObj, ok := current.(*jsonx.Object); ok {
		if nextObj, ok2 := value.(*jsonx.Object); ok2 {
			var removals []string
			for k := range curObj.All() {
				if !nextObj.Has(k) {
					removals = append(removals, k)
				}
			}
			for _, k := range removals {
				curObj.Delete(k)
			}
			for k, child := range nextObj.All() {
				name := k
				hjAssignAt(
					func() (JsonValue, bool) { return curObj.Get(name) },
					func(v JsonValue) { curObj.Set(name, v) },
					child,
				)
			}
			return
		}
	}
	if curArr, ok := current.([]any); ok {
		if nextArr, ok2 := value.([]any); ok2 && len(curArr) <= len(nextArr) {
			items := curArr
			grew := false
			for i := 0; i < len(nextArr); i++ {
				if i < len(items) {
					idx := i
					hjAssignAt(
						func() (JsonValue, bool) { return items[idx], true },
						func(v JsonValue) { items[idx] = v },
						nextArr[i],
					)
				} else {
					items = append(items, hjNormalizeValue(nextArr[i]))
					grew = true
				}
			}
			if grew {
				set(items)
			}
			return
		}
	}
	if !jsonx.DeepEqual(current, value) {
		set(value)
	}
}

func hjNormalizeValue(v JsonValue) JsonValue {
	switch x := v.(type) {
	case nil, bool, float64, string, *jsonx.Object, []any:
		return v
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	default:
		encoded, err := jsonx.Encode(v)
		if err != nil {
			return v
		}
		return encoded
	}
}
