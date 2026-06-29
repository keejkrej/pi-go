package agentloop

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ValidateToolArguments deep-clones tc.Arguments, coerces it against
// tool.Parameters() (treated as plain JSON Schema), validates, and returns the
// coerced args. On validation failure it returns a formatted error:
//
//	Validation failed for tool "<name>":
//	  - <path>: <message>
//	...
func ValidateToolArguments(tool AgentTool, tc *ToolCall) (map[string]any, error) {
	schema := tool.Parameters()

	cloned := deepClone(tc.Arguments)

	coerced := coerceWithJSONSchema(cloned, schema)

	out, _ := coerced.(map[string]any)
	if out == nil {
		// Coercion turned the object into a non-object; keep the cloned map so
		// validation can report a sensible error.
		out, _ = cloned.(map[string]any)
		if out == nil {
			out = map[string]any{}
		}
	}

	var failures []string
	validateAgainstSchema(coerced, schema, "root", &failures)
	if len(failures) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Validation failed for tool %q:", tool.Name())
		for _, f := range failures {
			b.WriteString("\n  - ")
			b.WriteString(f)
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	return out, nil
}

// deepClone recursively copies maps and slices; scalars are returned as-is.
func deepClone(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = deepClone(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepClone(item)
		}
		return out
	default:
		return val
	}
}

func schemaTypes(schema map[string]any) []string {
	t, ok := schema["type"]
	if !ok {
		return nil
	}
	switch tv := t.(type) {
	case string:
		return []string{tv}
	case []string:
		return tv
	case []any:
		var out []string
		for _, e := range tv {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func asSchema(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asSchemaList(v any) ([]map[string]any, bool) {
	switch lv := v.(type) {
	case []map[string]any:
		return lv, true
	case []any:
		out := make([]map[string]any, 0, len(lv))
		for _, e := range lv {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// matchesJSONType reports whether value already matches the named JSON type.
// Go json decodes numbers as float64.
func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func coercePrimitiveByType(value any, typ string) any {
	switch typ {
	case "number":
		if value == nil {
			return float64(0)
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) {
				return parsed
			}
		}
		if b, ok := value.(bool); ok {
			if b {
				return float64(1)
			}
			return float64(0)
		}
		return value
	case "integer":
		if value == nil {
			return float64(0)
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && parsed == math.Trunc(parsed) {
				return parsed
			}
		}
		if b, ok := value.(bool); ok {
			if b {
				return float64(1)
			}
			return float64(0)
		}
		return value
	case "boolean":
		if value == nil {
			return false
		}
		if s, ok := value.(string); ok {
			if s == "true" {
				return true
			}
			if s == "false" {
				return false
			}
		}
		if f, ok := value.(float64); ok {
			if f == 1 {
				return true
			}
			if f == 0 {
				return false
			}
		}
		return value
	case "string":
		if value == nil {
			return ""
		}
		switch v := value.(type) {
		case float64:
			return formatNumber(v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
		return value
	case "null":
		switch v := value.(type) {
		case string:
			if v == "" {
				return nil
			}
		case float64:
			if v == 0 {
				return nil
			}
		case bool:
			if !v {
				return nil
			}
		}
		return value
	default:
		return value
	}
}

// formatNumber mirrors JS String(number): integral floats render without a
// decimal point.
func formatNumber(f float64) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func coerceWithJSONSchema(value any, schema map[string]any) any {
	if schema == nil {
		return value
	}
	next := value

	if allOf, ok := asSchemaList(schema["allOf"]); ok {
		for _, sub := range allOf {
			next = coerceWithJSONSchema(next, sub)
		}
	}
	if anyOf, ok := asSchemaList(schema["anyOf"]); ok {
		next = coerceWithUnionSchema(next, anyOf)
	}
	if oneOf, ok := asSchemaList(schema["oneOf"]); ok {
		next = coerceWithUnionSchema(next, oneOf)
	}

	types := schemaTypes(schema)
	matchesUnionMember := false
	if len(types) > 1 {
		for _, t := range types {
			if matchesJSONType(next, t) {
				matchesUnionMember = true
				break
			}
		}
	}
	if len(types) > 0 && !matchesUnionMember {
		for _, t := range types {
			candidate := coercePrimitiveByType(next, t)
			if !sameValue(candidate, next) {
				next = candidate
				break
			}
		}
	}

	if containsStr(types, "object") {
		if m, ok := next.(map[string]any); ok {
			applySchemaObjectCoercion(m, schema)
		}
	}
	if containsStr(types, "array") {
		if a, ok := next.([]any); ok {
			applySchemaArrayCoercion(a, schema)
		}
	}

	return next
}

func coerceWithUnionSchema(value any, schemas []map[string]any) any {
	for _, sub := range schemas {
		candidate := deepClone(value)
		coerced := coerceWithJSONSchema(candidate, sub)
		var failures []string
		validateAgainstSchema(coerced, sub, "root", &failures)
		if len(failures) == 0 {
			return coerced
		}
	}
	return value
}

func applySchemaObjectCoercion(value map[string]any, schema map[string]any) {
	props, _ := asSchema(schema["properties"])
	definedKeys := map[string]bool{}
	if props != nil {
		for k, ps := range props {
			definedKeys[k] = true
			if _, present := value[k]; !present {
				continue
			}
			if sub, ok := asSchema(ps); ok {
				value[k] = coerceWithJSONSchema(value[k], sub)
			}
		}
	}
	if ap, ok := asSchema(schema["additionalProperties"]); ok {
		for k, pv := range value {
			if definedKeys[k] {
				continue
			}
			value[k] = coerceWithJSONSchema(pv, ap)
		}
	}
}

func applySchemaArrayCoercion(value []any, schema map[string]any) {
	if tuple, ok := asSchemaList(schema["items"]); ok && isTupleItems(schema["items"]) {
		for i := range value {
			if i >= len(tuple) {
				continue
			}
			value[i] = coerceWithJSONSchema(value[i], tuple[i])
		}
		return
	}
	if itemSchema, ok := asSchema(schema["items"]); ok {
		for i := range value {
			value[i] = coerceWithJSONSchema(value[i], itemSchema)
		}
	}
}

func isTupleItems(items any) bool {
	switch items.(type) {
	case []map[string]any, []any:
		return true
	default:
		return false
	}
}

// validateAgainstSchema collects failing paths into failures.
func validateAgainstSchema(value any, schema map[string]any, path string, failures *[]string) {
	if schema == nil {
		return
	}
	types := schemaTypes(schema)
	if len(types) > 0 {
		matched := false
		for _, t := range types {
			if matchesJSONType(value, t) {
				matched = true
				break
			}
		}
		if !matched {
			*failures = append(*failures, fmt.Sprintf("%s: Expected %s", path, strings.Join(types, " | ")))
			return
		}
	}

	if containsStr(types, "object") {
		if m, ok := value.(map[string]any); ok {
			validateObject(m, schema, path, failures)
		}
	}
	if containsStr(types, "array") {
		if a, ok := value.([]any); ok {
			validateArray(a, schema, path, failures)
		}
	}
}

func validateObject(value map[string]any, schema map[string]any, path string, failures *[]string) {
	if req, ok := schema["required"]; ok {
		var keys []string
		switch rv := req.(type) {
		case []string:
			keys = rv
		case []any:
			for _, e := range rv {
				if s, ok := e.(string); ok {
					keys = append(keys, s)
				}
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, present := value[k]; !present {
				*failures = append(*failures, fmt.Sprintf("%s: Expected required property", joinPath(path, k)))
			}
		}
	}
	if props, ok := asSchema(schema["properties"]); ok {
		// Iterate deterministically.
		var keys []string
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, present := value[k]
			if !present {
				continue
			}
			if sub, ok := asSchema(props[k]); ok {
				validateAgainstSchema(v, sub, joinPath(path, k), failures)
			}
		}
	}
}

func validateArray(value []any, schema map[string]any, path string, failures *[]string) {
	if tuple, ok := asSchemaList(schema["items"]); ok && isTupleItems(schema["items"]) {
		for i, item := range value {
			if i >= len(tuple) {
				continue
			}
			validateAgainstSchema(item, tuple[i], fmt.Sprintf("%s.%d", path, i), failures)
		}
		return
	}
	if itemSchema, ok := asSchema(schema["items"]); ok {
		for i, item := range value {
			validateAgainstSchema(item, itemSchema, fmt.Sprintf("%s.%d", path, i), failures)
		}
	}
}

func joinPath(base, key string) string {
	if base == "" || base == "root" {
		return key
	}
	return base + "." + key
}

func containsStr(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// sameValue reports whether two coercion results are equal for the purpose of
// "did the value change". Scalars are compared by value; composite values are
// considered "changed" only when identity differs, which for our use is
// approximated by structural comparison of scalars (composites never enter
// coercePrimitiveByType).
func sameValue(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	default:
		// Composite or other: treat as unchanged (identity preserved).
		return true
	}
}
