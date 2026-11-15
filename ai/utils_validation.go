// Ported from packages/ai/src/utils/validation.ts (pi v1.0.0 / v1.5.2 argumentsError).

package ai

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// uvTypeboxKind is the key of Symbol.for("TypeBox.Kind").
// TypeBox 1.3.27 never puts that symbol on a schema (it stores a hidden ~kind).
// Object.getOwnPropertySymbols therefore does not contain it, and the plain-JSON
// coercion branch in ValidateToolArguments runs for every schema, including
// Type.Object and Type.Unsafe. Do not substitute the unexported kind flag.
const uvTypeboxKind = "TypeBox.Kind"

var (
	uvValidatorMu sync.RWMutex
	// uvValidatorCache maps *typebox.Schema identities to compiled validators without pinning dead schemas.
	uvValidatorCache = make(map[weak.Pointer[typebox.Schema]]*typebox.Validator)
)

func uvSchemaObject(schema any) (*jsonx.Object, bool) {
	switch s := schema.(type) {
	case *typebox.Schema:
		if s == nil || s.Object == nil {
			return nil, false
		}
		return s.Object, true
	case *jsonx.Object:
		if s == nil {
			return nil, false
		}
		return s, true
	default:
		return nil, false
	}
}

// uvGetSchemaTypes reads schema.type. A string becomes one element.
// An array keeps only string elements. Any other shape is empty.
func uvGetSchemaTypes(schema any) []string {
	obj, ok := uvSchemaObject(schema)
	if !ok {
		return nil
	}
	raw, exists := obj.Get("type")
	if !exists {
		return nil
	}
	switch t := raw.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// uvMatchesJsonType reports whether value is a JSON Schema instance of jsonType.
// "integer" means a finite integral number. "object" excludes null and arrays.
func uvMatchesJsonType(value any, jsonType string) bool {
	switch jsonType {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		f, ok := uvAsFiniteFloat(value)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0)
	case "integer":
		f, ok := uvAsFiniteFloat(value)
		return ok && math.Trunc(f) == f
	case "string":
		_, ok := value.(string)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		obj, ok := value.(*jsonx.Object)
		return ok && obj != nil
	default:
		return false
	}
}

func uvAsFiniteFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// uvGetSubSchemaValidator compiles schema. A compile failure returns nil.
// Callers treat nil as "could not check", not as Check returning false.
func uvGetSubSchemaValidator(schema any) *typebox.Validator {
	switch s := schema.(type) {
	case *typebox.Schema:
		if s == nil {
			return nil
		}
		v, err := uvGetValidator(s)
		if err != nil {
			return nil
		}
		return v
	case *jsonx.Object:
		if s == nil {
			return nil
		}
		tbSchema, err := typebox.FromJSON(s)
		if err != nil {
			return nil
		}
		v, err := typebox.Compile(tbSchema)
		if err != nil {
			return nil
		}
		return v
	default:
		tbSchema, err := typebox.FromJSON(schema)
		if err != nil {
			return nil
		}
		v, err := typebox.Compile(tbSchema)
		if err != nil {
			return nil
		}
		return v
	}
}

// uvCoercePrimitiveByType applies one JSON Schema primitive coercion.
// A value that matches no rule for jsonType is returned unchanged.
func uvCoercePrimitiveByType(value any, jsonType string) any {
	switch jsonType {
	case "string":
		if _, ok := value.(string); ok {
			return value
		}
		if b, ok := value.(bool); ok {
			if b {
				return "true"
			}
			return "false"
		}
		if f, ok := uvAsFiniteFloat(value); ok {
			return js.NumberToString(f)
		}
		if value == nil {
			return "null"
		}
		return value

	case "number":
		if f, ok := uvCoerceNumber(value); ok {
			return f
		}
		return value

	case "integer":
		if f, ok := uvCoerceNumber(value); ok {
			return math.Trunc(f)
		}
		return value

	case "boolean":
		if b, ok := uvCoerceBoolean(value); ok {
			return b
		}
		return value

	case "null":
		if uvMatchesNullCoercion(value) {
			return nil
		}
		return value

	default:
		return value
	}
}

func uvCoerceNumber(value any) (float64, bool) {
	switch x := value.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case nil:
		return 0, true
	case string:
		n := js.ToNumber(x)
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
		low := js.ToLower(x)
		if low == "true" {
			return 1, true
		}
		if low == "false" {
			return 0, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func uvCoerceBoolean(value any) (bool, bool) {
	switch x := value.(type) {
	case bool:
		return x, true
	case float64:
		if x == 0 {
			return false, true
		}
		if x == 1 {
			return true, true
		}
		return false, false
	case int:
		if x == 0 {
			return false, true
		}
		if x == 1 {
			return true, true
		}
		return false, false
	case nil:
		return false, true
	case string:
		low := js.ToLower(x)
		switch {
		case low == "true" || x == "1":
			return true, true
		case low == "false" || x == "0":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func uvMatchesNullCoercion(value any) bool {
	switch x := value.(type) {
	case nil:
		return true
	case bool:
		return !x
	case float64:
		return x == 0
	case int:
		return x == 0
	case string:
		low := js.ToLower(x)
		return x == "" || x == "0" || low == "null" || low == "undefined"
	default:
		return false
	}
}

// uvApplySchemaObjectCoercion mutates value in place.
// properties are visited in jsonx key order, then additionalProperties
// (when it is a schema) for keys that are not in properties, in the same order.
func uvApplySchemaObjectCoercion(value *jsonx.Object, schema any) {
	if value == nil {
		return
	}
	schemaObj, ok := uvSchemaObject(schema)
	if !ok {
		return
	}
	var declared map[string]struct{}
	if propsRaw, hasProps := schemaObj.Get("properties"); hasProps {
		if props, ok := uvSchemaObject(propsRaw); ok {
			declared = make(map[string]struct{}, props.Len())
			for _, key := range props.Keys() {
				declared[key] = struct{}{}
				propSchema, _ := props.Get(key)
				if val, exists := value.Get(key); exists {
					value.Set(key, uvCoerceWithJsonSchema(val, propSchema))
				}
			}
		}
	}
	if addRaw, hasAdd := schemaObj.Get("additionalProperties"); hasAdd {
		if addSchema, ok := uvSchemaObject(addRaw); ok {
			for _, key := range value.Keys() {
				if _, isDeclared := declared[key]; isDeclared {
					continue
				}
				val, _ := value.Get(key)
				value.Set(key, uvCoerceWithJsonSchema(val, addSchema))
			}
		}
	}
}

// uvApplySchemaArrayCoercion mutates value in place.
// An items array is positional and stops when an index has no schema.
// A single items schema applies to every element.
func uvApplySchemaArrayCoercion(value []any, schema any) {
	schemaObj, ok := uvSchemaObject(schema)
	if !ok {
		return
	}
	itemsRaw, hasItems := schemaObj.Get("items")
	if !hasItems {
		return
	}
	switch items := itemsRaw.(type) {
	case []any:
		limit := len(value)
		if len(items) < limit {
			limit = len(items)
		}
		for i := 0; i < limit; i++ {
			value[i] = uvCoerceWithJsonSchema(value[i], items[i])
		}
	default:
		if _, isObj := uvSchemaObject(items); isObj {
			for i := 0; i < len(value); i++ {
				value[i] = uvCoerceWithJsonSchema(value[i], items)
			}
		}
	}
}

// uvCoerceWithUnionSchema returns the first arm that already validates,
// otherwise the first arm that validates after coercion of a clone.
// No accepting arm returns value unchanged.
func uvCoerceWithUnionSchema(value any, schemas []any) any {
	for _, sub := range schemas {
		v := uvGetSubSchemaValidator(sub)
		if v != nil && v.Check(value) {
			return value
		}
	}
	for _, sub := range schemas {
		v := uvGetSubSchemaValidator(sub)
		if v == nil {
			continue
		}
		coerced := uvCoerceWithJsonSchema(jsonx.CloneValue(value), sub)
		if v.Check(coerced) {
			return coerced
		}
	}
	return value
}

// uvCoerceWithJsonSchema coerces value with allOf, then anyOf, then oneOf,
// then primitive types. Several types skip primitive coercion when value
// already matches one of them. Object and array coercion then mutate in place.
func uvCoerceWithJsonSchema(value any, schema any) any {
	schemaObj, ok := uvSchemaObject(schema)
	if !ok {
		return value
	}
	current := value
	if allOf, hasAllOf := schemaObj.GetArray("allOf"); hasAllOf {
		for _, sub := range allOf {
			current = uvCoerceWithJsonSchema(current, sub)
		}
	}
	if anyOf, hasAnyOf := schemaObj.GetArray("anyOf"); hasAnyOf {
		current = uvCoerceWithUnionSchema(current, anyOf)
	}
	if oneOf, hasOneOf := schemaObj.GetArray("oneOf"); hasOneOf {
		current = uvCoerceWithUnionSchema(current, oneOf)
	}

	types := uvGetSchemaTypes(schemaObj)
	if len(types) > 0 {
		alreadyMatches := len(types) > 1 && slices.ContainsFunc(types, func(t string) bool {
			return uvMatchesJsonType(current, t)
		})
		if !alreadyMatches {
			if slices.Contains(types, "array") {
				if _, isArr := current.([]any); !isArr {
					current = []any{current}
				}
			}
			// Prefer numeric/string/null conversions before boolean in multi-type arrays (e.g. ["boolean", "number"] with "1").
			for _, t := range uvOrderedCoercionTypes(types) {
				coerced := uvCoercePrimitiveByType(current, t)
				if uvMatchesJsonType(coerced, t) && !jsonx.DeepEqual(coerced, current) {
					current = coerced
					break
				}
			}
		}
	}

	if obj, isObj := current.(*jsonx.Object); isObj && obj != nil {
		uvApplySchemaObjectCoercion(obj, schemaObj)
	} else if arr, isArr := current.([]any); isArr {
		uvApplySchemaArrayCoercion(arr, schemaObj)
	}
	return current
}

func uvOrderedCoercionTypes(types []string) []string {
	if len(types) <= 1 {
		return types
	}
	hasBool := slices.Contains(types, "boolean")
	hasNum := slices.Contains(types, "number") || slices.Contains(types, "integer")
	if !hasBool || !hasNum {
		return types
	}
	ordered := make([]string, 0, len(types))
	for _, t := range types {
		if t != "boolean" {
			ordered = append(ordered, t)
		}
	}
	ordered = append(ordered, "boolean")
	return ordered
}

// uvNormalizeOptionalNulls deletes an own null property when the key is not
// required, the property schema's $ref is not a string, and the compiled
// property schema rejects null. A schema that fails to compile does not reject
// null, so the property stays. Other values are walked into. Arrays walk tuple
// items by index, or every element when items is one schema, and then return.
func uvNormalizeOptionalNulls(value any, schema any) {
	schemaObj, ok := uvSchemaObject(schema)
	if !ok {
		return
	}
	if arr, isArr := value.([]any); isArr {
		itemsRaw, hasItems := schemaObj.Get("items")
		if !hasItems {
			return
		}
		switch items := itemsRaw.(type) {
		case []any:
			limit := len(arr)
			if len(items) < limit {
				limit = len(items)
			}
			for i := 0; i < limit; i++ {
				uvNormalizeOptionalNulls(arr[i], items[i])
			}
		default:
			if _, isObj := uvSchemaObject(items); isObj {
				for _, elem := range arr {
					uvNormalizeOptionalNulls(elem, items)
				}
			}
		}
		return
	}

	obj, isObj := value.(*jsonx.Object)
	if !isObj || obj == nil {
		return
	}

	requiredSet := map[string]struct{}{}
	if reqRaw, hasReq := schemaObj.GetArray("required"); hasReq {
		for _, item := range reqRaw {
			if s, ok := item.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}

	var propsObj *jsonx.Object
	if propsRaw, hasProps := schemaObj.Get("properties"); hasProps {
		if p, ok := uvSchemaObject(propsRaw); ok {
			propsObj = p
		}
	}
	var addObj *jsonx.Object
	if addRaw, hasAdd := schemaObj.Get("additionalProperties"); hasAdd {
		if a, ok := uvSchemaObject(addRaw); ok {
			addObj = a
		}
	}
	var patternEntries []struct {
		re     *jsre.Regexp
		schema any
	}
	if patRaw, hasPat := schemaObj.Get("patternProperties"); hasPat {
		if patObj, ok := uvSchemaObject(patRaw); ok {
			for _, pat := range patObj.Keys() {
				re, err := jsre.Compile(pat, "u")
				if err != nil {
					continue
				}
				sub, _ := patObj.Get(pat)
				patternEntries = append(patternEntries, struct {
					re     *jsre.Regexp
					schema any
				}{re: re, schema: sub})
			}
		}
	}

	for _, key := range obj.Keys() {
		propVal, _ := obj.Get(key)
		_, isRequired := requiredSet[key]

		var propSchemas []any
		matchedDeclaredOrPattern := false
		if propsObj != nil {
			if ps, hasProp := propsObj.Get(key); hasProp {
				propSchemas = append(propSchemas, ps)
				matchedDeclaredOrPattern = true
			}
		}
		for _, entry := range patternEntries {
			if entry.re.Test(key) {
				propSchemas = append(propSchemas, entry.schema)
				matchedDeclaredOrPattern = true
			}
		}
		if !matchedDeclaredOrPattern && addObj != nil {
			propSchemas = append(propSchemas, addObj)
		}

		deleted := false
		for _, propSchema := range propSchemas {
			if uvShouldDeleteOptionalNull(propVal, isRequired, propSchema) {
				obj.Delete(key)
				deleted = true
				break
			}
		}
		if deleted {
			continue
		}
		for _, propSchema := range propSchemas {
			uvNormalizeOptionalNulls(propVal, propSchema)
		}
	}
}

func uvShouldDeleteOptionalNull(val any, isRequired bool, propSchema any) bool {
	if val != nil || isRequired {
		return false
	}
	psObj, ok := uvSchemaObject(propSchema)
	if !ok {
		return false
	}
	if _, hasRef := psObj.GetString("$ref"); hasRef {
		return false
	}
	v := uvGetSubSchemaValidator(psObj)
	if v == nil {
		return false
	}
	return !v.Check(nil)
}

// uvGetValidator returns the cached Compile of schema, compiling and storing it
// on a miss. The cache key is the *typebox.Schema pointer. Compile's error is
// returned and not cached.
func uvGetValidator(schema *typebox.Schema) (*typebox.Validator, error) {
	if schema == nil {
		return nil, fmt.Errorf("validation schema is nil")
	}
	wp := weak.Make(schema)
	uvValidatorMu.RLock()
	cached := uvValidatorCache[wp]
	uvValidatorMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	compiled, err := typebox.Compile(schema)
	if err != nil {
		return nil, err
	}

	uvValidatorMu.Lock()
	if existing := uvValidatorCache[wp]; existing != nil {
		uvValidatorMu.Unlock()
		return existing, nil
	}
	uvValidatorCache[wp] = compiled
	uvValidatorMu.Unlock()

	runtime.AddCleanup(schema, func(key weak.Pointer[typebox.Schema]) {
		uvValidatorMu.Lock()
		delete(uvValidatorCache, key)
		uvValidatorMu.Unlock()
	}, wp)

	return compiled, nil
}

// uvFormatValidationPath formats one TypeBox error path.
// keyword "required" uses params.requiredProperties[0] appended to instancePath
// when that array is non-empty. Other paths are instancePath with the leading
// slash removed and remaining slashes turned into dots. An empty path is "root".
func uvFormatValidationPath(validationError typebox.ValidationError) string {
	path := validationError.InstancePath
	if validationError.Keyword == "required" && validationError.Params != nil {
		if reqProps, ok := validationError.Params.GetArray("requiredProperties"); ok && len(reqProps) > 0 {
			if missing, ok := reqProps[0].(string); ok {
				if path == "" {
					path = "/" + missing
				} else {
					path = path + "/" + missing
				}
			}
		}
	}
	if path == "" {
		return "root"
	}
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "root"
	}
	parts := strings.Split(trimmed, "/")
	for i, seg := range parts {
		seg = strings.ReplaceAll(seg, "~1", "/")
		seg = strings.ReplaceAll(seg, "~0", "~")
		parts[i] = seg
	}
	return strings.Join(parts, ".")
}

// ValidateToolCall finds the tool named by toolCall and validates its arguments
// against that tool's schema. It returns the validated arguments.
// A missing tool returns an error whose message is Tool "<name>" not found,
// with the name in double quotes.
func ValidateToolCall(tools []*Tool, toolCall *ToolCall) (any, error) {
	if toolCall == nil {
		return nil, fmt.Errorf(`Tool "" not found`)
	}
	for _, tool := range tools {
		if tool != nil && tool.Name == toolCall.Name {
			return ValidateToolArguments(tool, toolCall)
		}
	}
	return nil, fmt.Errorf(`Tool %q not found`, toolCall.Name)
}

// ValidateToolArguments validates toolCall.arguments against tool.parameters.
// It returns the validated arguments, coerced when the schema requires it.
func ValidateToolArguments(tool *Tool, toolCall *ToolCall) (any, error) {
	if toolCall == nil {
		return nil, fmt.Errorf("toolCall is nil")
	}
	if toolCall.ArgumentsError != nil {
		return nil, fmt.Errorf(`Invalid arguments for tool %q: %s`, toolCall.Name, *toolCall.ArgumentsError)
	}
	if tool == nil {
		return nil, fmt.Errorf(`tool %q is nil`, toolCall.Name)
	}

	validator, err := uvGetValidator(tool.Parameters)
	if err != nil {
		return nil, err
	}

	var argsCopy any
	if toolCall.Arguments != nil {
		argsCopy = jsonx.CloneValue(toolCall.Arguments)
	} else {
		argsCopy = jsonx.NewObject()
	}

	uvNormalizeOptionalNulls(argsCopy, tool.Parameters)
	args := typebox.Convert(tool.Parameters, argsCopy)
	args = uvCoerceWithJsonSchema(args, tool.Parameters)

	if validator.Check(args) {
		return args, nil
	}

	errs := validator.Errors(args)
	var errSummary string
	if len(errs) == 0 {
		errSummary = "Unknown validation error"
	} else {
		lines := make([]string, len(errs))
		for i, e := range errs {
			lines[i] = fmt.Sprintf("  - %s: %s", uvFormatValidationPath(e), e.Message)
		}
		errSummary = strings.Join(lines, "\n")
	}

	receivedJSON, err := jsonx.StringifyIndent(toolCall.Arguments, "  ")
	if err != nil {
		receivedJSON = "null"
	}
	return nil, fmt.Errorf("Validation failed for tool %q:\n%s\n\nReceived arguments:\n%s", toolCall.Name, errSummary, receivedJSON)
}
