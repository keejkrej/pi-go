package typebox

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	maxErrors      = 8
	maxSchemaDepth = 1000
)

// ValidationError is one localized (en) TypeBox error.
type ValidationError struct {
	InstancePath string
	SchemaPath   string
	Keyword      string
	Message      string
	Params       *jsonx.Object
}

type walk struct {
	root  any
	depth int
}

type errCtx struct {
	errors []ValidationError
}

func (c *errCtx) atCapacity() bool { return len(c.errors) >= maxErrors }

func (c *errCtx) add(keyword, schemaPath, instancePath string, params *jsonx.Object) {
	if c.atCapacity() {
		return
	}
	if params == nil {
		params = jsonx.NewObject()
	}
	c.errors = append(c.errors, ValidationError{
		InstancePath: instancePath,
		SchemaPath:   schemaPath,
		Keyword:      keyword,
		Message:      messageEN(keyword, params),
		Params:       params,
	})
}

func (c *errCtx) addExisting(e ValidationError) {
	if !c.atCapacity() {
		c.errors = append(c.errors, e)
	}
}

func emptyParams() *jsonx.Object { return jsonx.NewObject() }

// Check reports whether v matches s.
func Check(s *Schema, v any) bool {
	if s == nil {
		return false
	}
	return checkSchema(walk{root: s}, s, v)
}

// Errors returns localized errors for v. The slice is empty when v matches.
func Errors(s *Schema, v any) []ValidationError {
	if s == nil {
		return []ValidationError{}
	}
	ctx := &errCtx{}
	errorSchema(walk{root: s}, ctx, "#", "", s, v)
	if ctx.errors == nil {
		return []ValidationError{}
	}
	return ctx.errors
}

func checkSchema(w walk, schema, value any) bool {
	if w.depth > maxSchemaDepth {
		return false
	}
	w.depth++
	if b, ok := schemaBool(schema); ok {
		return b
	}
	if _, ok := schemaObject(schema); !ok {
		return false
	}
	if isTypeKeyword(schema) && !checkType(schema, value) {
		return false
	}
	if _, ok := dataObject(value); ok {
		if isRequiredKeyword(schema) && !checkRequired(schema, value) {
			return false
		}
		if isAdditionalProperties(schema) && !checkAdditionalProperties(w, schema, value) {
			return false
		}
		if isPatternProperties(schema) && !checkPatternProperties(w, schema, value) {
			return false
		}
		if isPropertiesKeyword(schema) && !checkProperties(w, schema, value) {
			return false
		}
	}
	if _, ok := dataArray(value); ok {
		if isAdditionalItems(schema) && !checkAdditionalItems(w, schema, value) {
			return false
		}
		if isItemsKeyword(schema) && !checkItems(w, schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "maxItems"); ok && !checkMaxItems(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "minItems"); ok && !checkMinItems(schema, value) {
			return false
		}
		if isPrefixItems(schema) && !checkPrefixItems(w, schema, value) {
			return false
		}
		if isUniqueItems(schema) && !checkUniqueItems(schema, value) {
			return false
		}
	}
	if _, ok := value.(string); ok {
		if _, ok := finiteKey(schema, "maxLength"); ok && !checkMaxLength(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "minLength"); ok && !checkMinLength(schema, value) {
			return false
		}
		if isFormatKeyword(schema) && !checkFormat(schema, value) {
			return false
		}
		if isPatternKeyword(schema) && !checkPattern(schema, value) {
			return false
		}
	}
	if _, ok := finite(value); ok {
		if _, ok := finiteKey(schema, "exclusiveMaximum"); ok && !checkExclusiveMaximum(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "exclusiveMinimum"); ok && !checkExclusiveMinimum(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "maximum"); ok && !checkMaximum(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "minimum"); ok && !checkMinimum(schema, value) {
			return false
		}
		if _, ok := finiteKey(schema, "multipleOf"); ok && !checkMultipleOf(schema, value) {
			return false
		}
	}
	if isRefKeyword(schema) && !checkRef(w, schema, value) {
		return false
	}
	if isConstKeyword(schema) && !checkConst(schema, value) {
		return false
	}
	if isEnumKeyword(schema) && !checkEnum(schema, value) {
		return false
	}
	if isNotKeyword(schema) && !checkNot(w, schema, value) {
		return false
	}
	if isAllOfKeyword(schema) && !checkAllOf(w, schema, value) {
		return false
	}
	if isAnyOfKeyword(schema) && !checkAnyOf(w, schema, value) {
		return false
	}
	if isOneOfKeyword(schema) && !checkOneOf(w, schema, value) {
		return false
	}
	return true
}

func errorSchema(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if ctx.atCapacity() || w.depth > maxSchemaDepth {
		return false
	}
	w.depth++
	if b, ok := schemaBool(schema); ok {
		if b {
			return true
		}
		ctx.add("boolean", schemaPath, instancePath, emptyParams())
		return false
	}
	if _, ok := schemaObject(schema); !ok {
		return false
	}
	acc := true
	if isTypeKeyword(schema) && !errorType(ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if _, ok := dataObject(value); ok {
		inner := true
		if isRequiredKeyword(schema) && !errorRequired(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isAdditionalProperties(schema) && !errorAdditionalProperties(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isPatternProperties(schema) && !errorPatternProperties(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isPropertiesKeyword(schema) && !errorProperties(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if !inner {
			acc = false
		}
	}
	if arr, ok := dataArray(value); ok {
		inner := true
		_ = arr
		if isAdditionalItems(schema) && !errorAdditionalItems(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isItemsKeyword(schema) && !errorItems(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "maxItems"); ok && !errorMaxItems(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "minItems"); ok && !errorMinItems(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isPrefixItems(schema) && !errorPrefixItems(w, ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isUniqueItems(schema) && !errorUniqueItems(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if !inner {
			acc = false
		}
	}
	if _, ok := value.(string); ok {
		inner := true
		if _, ok := finiteKey(schema, "maxLength"); ok && !errorMaxLength(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "minLength"); ok && !errorMinLength(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isFormatKeyword(schema) && !errorFormat(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if isPatternKeyword(schema) && !errorPattern(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if !inner {
			acc = false
		}
	}
	if _, ok := finite(value); ok {
		inner := true
		if _, ok := finiteKey(schema, "exclusiveMaximum"); ok && !errorExclusiveMaximum(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "exclusiveMinimum"); ok && !errorExclusiveMinimum(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "maximum"); ok && !errorMaximum(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "minimum"); ok && !errorMinimum(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if _, ok := finiteKey(schema, "multipleOf"); ok && !errorMultipleOf(ctx, schemaPath, instancePath, schema, value) {
			inner = false
		}
		if !inner {
			acc = false
		}
	}
	if isRefKeyword(schema) && !errorRef(w, ctx, instancePath, schema, value) {
		acc = false
	}
	if isConstKeyword(schema) && !errorConst(ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if isEnumKeyword(schema) && !errorEnum(ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if isNotKeyword(schema) && !errorNot(w, ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if isAllOfKeyword(schema) && !errorAllOf(w, ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if isAnyOfKeyword(schema) && !errorAnyOf(w, ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	if isOneOfKeyword(schema) && !errorOneOf(w, ctx, schemaPath, instancePath, schema, value) {
		acc = false
	}
	return acc
}

func isTypeKeyword(n any) bool {
	v, ok := getKey(n, "type")
	if !ok {
		return false
	}
	if _, ok := v.(string); ok {
		return true
	}
	_, ok = isStringArray(v)
	return ok
}

func isStringArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	for _, el := range a {
		if _, ok := el.(string); !ok {
			return nil, false
		}
	}
	return a, true
}

func isSchemaArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	for _, el := range a {
		if !isSchemaNode(el) {
			return nil, false
		}
	}
	return a, true
}

func isRequiredKeyword(n any) bool {
	v, ok := getKey(n, "required")
	if !ok {
		return false
	}
	_, ok = isStringArray(v)
	return ok
}

func isAdditionalProperties(n any) bool {
	v, ok := getKey(n, "additionalProperties")
	return ok && isSchemaNode(v)
}

func isPatternProperties(n any) bool {
	o, ok := objectKey(n, "patternProperties")
	if !ok {
		return false
	}
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if !isSchemaNode(v) {
			return false
		}
	}
	return true
}

func isPropertiesKeyword(n any) bool {
	o, ok := objectKey(n, "properties")
	if !ok {
		return false
	}
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if !isSchemaNode(v) {
			return false
		}
	}
	return true
}

func isItemsKeyword(n any) bool {
	v, ok := getKey(n, "items")
	if !ok {
		return false
	}
	if isSchemaNode(v) {
		return true
	}
	_, ok = isSchemaArray(v)
	return ok
}

func isItemsSized(n any) bool {
	v, ok := getKey(n, "items")
	if !ok {
		return false
	}
	_, ok = isSchemaArray(v)
	return ok
}

func isAdditionalItems(n any) bool {
	v, ok := getKey(n, "additionalItems")
	return ok && isSchemaNode(v)
}

func isPrefixItems(n any) bool {
	v, ok := getKey(n, "prefixItems")
	if !ok {
		return false
	}
	_, ok = isSchemaArray(v)
	return ok
}

func isUniqueItems(n any) bool {
	v, ok := getKey(n, "uniqueItems")
	if !ok {
		return false
	}
	_, ok = v.(bool)
	return ok
}

func isFormatKeyword(n any) bool {
	v, ok := getKey(n, "format")
	if !ok {
		return false
	}
	_, ok = v.(string)
	return ok
}

func isPatternKeyword(n any) bool {
	v, ok := getKey(n, "pattern")
	if !ok {
		return false
	}
	_, ok = v.(string)
	return ok
}

func isRefKeyword(n any) bool {
	v, ok := getKey(n, "$ref")
	if !ok {
		return false
	}
	_, ok = v.(string)
	return ok
}

func isConstKeyword(n any) bool { return hasKey(n, "const") }

func isEnumKeyword(n any) bool {
	v, ok := getKey(n, "enum")
	if !ok {
		return false
	}
	_, ok = v.([]any)
	return ok
}

func isNotKeyword(n any) bool {
	v, ok := getKey(n, "not")
	return ok && isSchemaNode(v)
}

func isAllOfKeyword(n any) bool { return schemaArrayKey(n, "allOf") }
func isAnyOfKeyword(n any) bool { return schemaArrayKey(n, "anyOf") }
func isOneOfKeyword(n any) bool { return schemaArrayKey(n, "oneOf") }

func schemaArrayKey(n any, key string) bool {
	v, ok := getKey(n, key)
	if !ok {
		return false
	}
	_, ok = isSchemaArray(v)
	return ok
}

func finiteKey(n any, key string) (float64, bool) {
	v, ok := getKey(n, key)
	if !ok {
		return 0, false
	}
	return finite(v)
}

func getArrayKey(n any, key string) ([]any, bool) {
	v, ok := getKey(n, key)
	if !ok {
		return nil, false
	}
	a, ok := v.([]any)
	return a, ok
}

func checkTypeName(typ string, value any) bool {
	switch typ {
	case "object":
		_, ok := dataObject(value)
		return ok
	case "array":
		_, ok := dataArray(value)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		return isIntegerNumber(value)
	case "number":
		_, ok := finite(value)
		return ok
	case "null":
		return value == nil
	case "string":
		_, ok := value.(string)
		return ok
	case "bigint", "constructor", "function", "symbol", "undefined", "void":
		return false
	default:
		return true
	}
}

func checkType(schema, value any) bool {
	t, _ := getKey(schema, "type")
	if s, ok := t.(string); ok {
		return checkTypeName(s, value)
	}
	a, _ := t.([]any)
	for _, el := range a {
		s, _ := el.(string)
		if checkTypeName(s, value) {
			return true
		}
	}
	return false
}

func errorType(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkType(schema, value) {
		return true
	}
	t, _ := getKey(schema, "type")
	p := jsonx.NewObject()
	p.Set("type", t)
	ctx.add("type", schemaPath, instancePath, p)
	return false
}

func checkRequired(schema, value any) bool {
	req, _ := getArrayKey(schema, "required")
	o, _ := dataObject(value)
	for _, k := range req {
		s, _ := k.(string)
		if o == nil || !o.Has(s) {
			return false
		}
	}
	return true
}

func errorRequired(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	req, _ := getArrayKey(schema, "required")
	o, _ := dataObject(value)
	missing := []any{}
	for _, k := range req {
		s, _ := k.(string)
		if o == nil || !o.Has(s) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return true
	}
	p := jsonx.NewObject()
	p.Set("requiredProperties", missing)
	ctx.add("required", schemaPath, instancePath, p)
	return false
}

func propertiesPattern(schema any) string {
	var patterns []string
	if isPatternProperties(schema) {
		o, _ := objectKey(schema, "patternProperties")
		patterns = append(patterns, o.Keys()...)
	}
	if isPropertiesKeyword(schema) {
		o, _ := objectKey(schema, "properties")
		for _, k := range o.Keys() {
			patterns = append(patterns, "^"+escapeRegexp(k)+"$")
		}
	}
	if len(patterns) == 0 {
		return "(?!)"
	}
	return "(" + strings.Join(patterns, "|") + ")"
}

func checkAdditionalProperties(w walk, schema, value any) bool {
	o, _ := dataObject(value)
	re, err := compileRE(propertiesPattern(schema), "u")
	if err != nil {
		return false
	}
	add, _ := getKey(schema, "additionalProperties")
	for _, key := range o.Keys() {
		if re.Test(key) {
			continue
		}
		prop, _ := o.Get(key)
		if !checkSchema(w, add, prop) {
			return false
		}
	}
	return true
}

func errorAdditionalProperties(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	o, _ := dataObject(value)
	re, err := compileRE(propertiesPattern(schema), "u")
	add, _ := getKey(schema, "additionalProperties")
	bad := []any{}
	okAll := err == nil
	for _, key := range o.Keys() {
		matched := err == nil && re.Test(key)
		pass := matched
		if !matched {
			prop, _ := o.Get(key)
			pass = errorSchema(w, ctx, schemaPath+"/additionalProperties", instancePath+"/"+key, add, prop)
		}
		if !pass {
			bad = append(bad, key)
			okAll = false
		}
	}
	if okAll {
		return true
	}
	p := jsonx.NewObject()
	p.Set("additionalProperties", bad)
	ctx.add("additionalProperties", schemaPath, instancePath, p)
	return false
}

func checkPatternProperties(w walk, schema, value any) bool {
	pp, _ := objectKey(schema, "patternProperties")
	o, _ := dataObject(value)
	for _, pattern := range pp.Keys() {
		sub, _ := pp.Get(pattern)
		re, err := compileRE(pattern, "u")
		if err != nil {
			return false
		}
		for _, key := range o.Keys() {
			if !re.Test(key) {
				continue
			}
			prop, _ := o.Get(key)
			if !checkSchema(w, sub, prop) {
				return false
			}
		}
	}
	return true
}

func errorPatternProperties(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	pp, _ := objectKey(schema, "patternProperties")
	o, _ := dataObject(value)
	okAll := true
	for _, pattern := range pp.Keys() {
		sub, _ := pp.Get(pattern)
		re, err := compileRE(pattern, "u")
		if err != nil {
			p := jsonx.NewObject()
			p.Set("pattern", pattern)
			ctx.add("pattern", schemaPath+"/patternProperties/"+pattern, instancePath, p)
			okAll = false
			continue
		}
		for _, key := range o.Keys() {
			if !re.Test(key) {
				continue
			}
			prop, _ := o.Get(key)
			if !errorSchema(w, ctx, schemaPath+"/patternProperties/"+pattern, instancePath+"/"+key, sub, prop) {
				okAll = false
			}
		}
	}
	return okAll
}

func checkProperties(w walk, schema, value any) bool {
	props, _ := objectKey(schema, "properties")
	o, _ := dataObject(value)
	for _, key := range props.Keys() {
		if o == nil || !o.Has(key) {
			continue
		}
		sub, _ := props.Get(key)
		prop, _ := o.Get(key)
		if !checkSchema(w, sub, prop) {
			return false
		}
	}
	return true
}

func errorProperties(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	props, _ := objectKey(schema, "properties")
	o, _ := dataObject(value)
	okAll := true
	for _, key := range props.Keys() {
		if o == nil || !o.Has(key) {
			continue
		}
		sub, _ := props.Get(key)
		prop, _ := o.Get(key)
		if !errorSchema(w, ctx, schemaPath+"/properties/"+key, instancePath+"/"+key, sub, prop) {
			okAll = false
		}
	}
	return okAll
}

func checkAdditionalItems(w walk, schema, value any) bool {
	if !isItemsSized(schema) {
		return true
	}
	items, _ := getArrayKey(schema, "items")
	arr, _ := dataArray(value)
	add, _ := getKey(schema, "additionalItems")
	for i := len(items); i < len(arr); i++ {
		if !checkSchema(w, add, arr[i]) {
			return false
		}
	}
	return true
}

func errorAdditionalItems(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if !isItemsSized(schema) {
		return true
	}
	items, _ := getArrayKey(schema, "items")
	arr, _ := dataArray(value)
	add, _ := getKey(schema, "additionalItems")
	for i := len(items); i < len(arr); i++ {
		if !errorSchema(w, ctx, schemaPath+"/additionalItems", instancePath+"/"+strconv.Itoa(i), add, arr[i]) {
			return false
		}
	}
	return true
}

func checkItems(w walk, schema, value any) bool {
	if isItemsSized(schema) {
		items, _ := getArrayKey(schema, "items")
		arr, _ := dataArray(value)
		for i, sub := range items {
			if i >= len(arr) {
				break
			}
			if !checkSchema(w, sub, arr[i]) {
				return false
			}
		}
		return true
	}
	item, _ := getKey(schema, "items")
	arr, _ := dataArray(value)
	offset := 0
	if isPrefixItems(schema) {
		p, _ := getArrayKey(schema, "prefixItems")
		offset = len(p)
	}
	for i := offset; i < len(arr); i++ {
		if !checkSchema(w, item, arr[i]) {
			return false
		}
	}
	return true
}

func errorItems(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	arr, _ := dataArray(value)
	if isItemsSized(schema) {
		items, _ := getArrayKey(schema, "items")
		okAll := true
		for i, sub := range items {
			if i >= len(arr) {
				continue
			}
			if !errorSchema(w, ctx, schemaPath+"/items/"+strconv.Itoa(i), instancePath+"/"+strconv.Itoa(i), sub, arr[i]) {
				okAll = false
			}
		}
		return okAll
	}
	item, _ := getKey(schema, "items")
	offset := 0
	if isPrefixItems(schema) {
		p, _ := getArrayKey(schema, "prefixItems")
		offset = len(p)
	}
	okAll := true
	for i := offset; i < len(arr); i++ {
		if !errorSchema(w, ctx, schemaPath+"/items", instancePath+"/"+strconv.Itoa(i), item, arr[i]) {
			okAll = false
		}
	}
	return okAll
}

func checkMinItems(schema, value any) bool {
	n, _ := finiteKey(schema, "minItems")
	arr, _ := dataArray(value)
	return float64(len(arr)) >= n
}

func checkMaxItems(schema, value any) bool {
	n, _ := finiteKey(schema, "maxItems")
	arr, _ := dataArray(value)
	return float64(len(arr)) <= n
}

func errorMinItems(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMinItems(schema, value) {
		return true
	}
	n, _ := finiteKey(schema, "minItems")
	p := jsonx.NewObject()
	p.Set("limit", n)
	ctx.add("minItems", schemaPath, instancePath, p)
	return false
}

func errorMaxItems(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMaxItems(schema, value) {
		return true
	}
	n, _ := finiteKey(schema, "maxItems")
	p := jsonx.NewObject()
	p.Set("limit", n)
	ctx.add("maxItems", schemaPath, instancePath, p)
	return false
}

func checkPrefixItems(w walk, schema, value any) bool {
	arr, _ := dataArray(value)
	if len(arr) == 0 {
		return true
	}
	items, _ := getArrayKey(schema, "prefixItems")
	for i, sub := range items {
		if i >= len(arr) {
			break
		}
		if !checkSchema(w, sub, arr[i]) {
			return false
		}
	}
	return true
}

func errorPrefixItems(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	arr, _ := dataArray(value)
	if len(arr) == 0 {
		return true
	}
	items, _ := getArrayKey(schema, "prefixItems")
	okAll := true
	for i, sub := range items {
		if i >= len(arr) {
			continue
		}
		if !errorSchema(w, ctx, schemaPath+"/prefixItems/"+strconv.Itoa(i), instancePath+"/"+strconv.Itoa(i), sub, arr[i]) {
			okAll = false
		}
	}
	return okAll
}

func checkUniqueItems(schema, value any) bool {
	flag, _ := getKey(schema, "uniqueItems")
	if b, _ := flag.(bool); !b {
		return true
	}
	arr, _ := dataArray(value)
	seen := map[string]struct{}{}
	for _, el := range arr {
		k := canon(el)
		if _, ok := seen[k]; ok {
			return false
		}
		seen[k] = struct{}{}
	}
	return true
}

func errorUniqueItems(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	flag, _ := getKey(schema, "uniqueItems")
	if b, _ := flag.(bool); !b {
		return true
	}
	arr, _ := dataArray(value)
	seen := map[string]struct{}{}
	dups := []any{}
	for i, el := range arr {
		k := canon(el)
		if _, ok := seen[k]; ok {
			dups = append(dups, float64(i))
			continue
		}
		seen[k] = struct{}{}
	}
	if len(dups) == 0 {
		return true
	}
	p := jsonx.NewObject()
	p.Set("duplicateItems", dups)
	ctx.add("uniqueItems", schemaPath, instancePath, p)
	return false
}

func checkMinLength(schema, value any) bool {
	n, _ := finiteKey(schema, "minLength")
	s, _ := value.(string)
	return isMinLength(s, n)
}

func checkMaxLength(schema, value any) bool {
	n, _ := finiteKey(schema, "maxLength")
	s, _ := value.(string)
	return isMaxLength(s, n)
}

func errorMinLength(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMinLength(schema, value) {
		return true
	}
	n, _ := finiteKey(schema, "minLength")
	p := jsonx.NewObject()
	p.Set("limit", n)
	ctx.add("minLength", schemaPath, instancePath, p)
	return false
}

func errorMaxLength(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMaxLength(schema, value) {
		return true
	}
	n, _ := finiteKey(schema, "maxLength")
	p := jsonx.NewObject()
	p.Set("limit", n)
	ctx.add("maxLength", schemaPath, instancePath, p)
	return false
}

func checkFormat(schema, value any) bool {
	name, _ := getKey(schema, "format")
	s, _ := name.(string)
	v, _ := value.(string)
	return formatOK(s, v)
}

func errorFormat(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkFormat(schema, value) {
		return true
	}
	name, _ := getKey(schema, "format")
	p := jsonx.NewObject()
	p.Set("format", name)
	ctx.add("format", schemaPath, instancePath, p)
	return false
}

func checkPattern(schema, value any) bool {
	pat, _ := getKey(schema, "pattern")
	s, ok := pat.(string)
	if !ok {
		return false
	}
	v, _ := value.(string)
	matched, err := reTest(s, "u", v)
	return err == nil && matched
}

func errorPattern(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkPattern(schema, value) {
		return true
	}
	pat, _ := getKey(schema, "pattern")
	p := jsonx.NewObject()
	p.Set("pattern", pat)
	ctx.add("pattern", schemaPath, instancePath, p)
	return false
}

func checkExclusiveMinimum(schema, value any) bool {
	limit, _ := finiteKey(schema, "exclusiveMinimum")
	v, _ := finite(value)
	return v > limit
}

func checkExclusiveMaximum(schema, value any) bool {
	limit, _ := finiteKey(schema, "exclusiveMaximum")
	v, _ := finite(value)
	return v < limit
}

func checkMinimum(schema, value any) bool {
	limit, _ := finiteKey(schema, "minimum")
	v, _ := finite(value)
	return v >= limit
}

func checkMaximum(schema, value any) bool {
	limit, _ := finiteKey(schema, "maximum")
	v, _ := finite(value)
	return v <= limit
}

func cmpParams(cmp string, limit float64) *jsonx.Object {
	p := jsonx.NewObject()
	p.Set("comparison", cmp)
	p.Set("limit", limit)
	return p
}

func errorExclusiveMinimum(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkExclusiveMinimum(schema, value) {
		return true
	}
	limit, _ := finiteKey(schema, "exclusiveMinimum")
	ctx.add("exclusiveMinimum", schemaPath, instancePath, cmpParams(">", limit))
	return false
}

func errorExclusiveMaximum(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkExclusiveMaximum(schema, value) {
		return true
	}
	limit, _ := finiteKey(schema, "exclusiveMaximum")
	ctx.add("exclusiveMaximum", schemaPath, instancePath, cmpParams("<", limit))
	return false
}

func errorMinimum(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMinimum(schema, value) {
		return true
	}
	limit, _ := finiteKey(schema, "minimum")
	ctx.add("minimum", schemaPath, instancePath, cmpParams(">=", limit))
	return false
}

func errorMaximum(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMaximum(schema, value) {
		return true
	}
	limit, _ := finiteKey(schema, "maximum")
	ctx.add("maximum", schemaPath, instancePath, cmpParams("<=", limit))
	return false
}

func jsMod(a, b float64) float64 { return math.Mod(a, b) }

func isMultipleOfNumber(dividend, divisor float64) bool {
	if math.IsNaN(dividend) || math.IsInf(dividend, 0) {
		return true
	}
	recip := 1 / divisor
	if isIntegerNumber(dividend) && jsMod(recip, 1) == 0 {
		return true
	}
	mod := math.Mod(dividend, divisor)
	return math.Min(math.Abs(mod), math.Min(math.Abs(mod-divisor), math.Abs(mod+divisor))) < 1e-10
}

func checkMultipleOf(schema, value any) bool {
	div, _ := finiteKey(schema, "multipleOf")
	v, ok := finite(value)
	if !ok {
		return true
	}
	return isMultipleOfNumber(v, div)
}

func errorMultipleOf(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkMultipleOf(schema, value) {
		return true
	}
	div, _ := finiteKey(schema, "multipleOf")
	p := jsonx.NewObject()
	p.Set("multipleOf", div)
	ctx.add("multipleOf", schemaPath, instancePath, p)
	return false
}

func checkConst(schema, value any) bool {
	c, _ := getKey(schema, "const")
	if isValueLike(c) {
		return valueEqual(value, c)
	}
	return deepEqual(value, c)
}

func errorConst(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkConst(schema, value) {
		return true
	}
	c, _ := getKey(schema, "const")
	p := jsonx.NewObject()
	p.Set("allowedValue", c)
	ctx.add("const", schemaPath, instancePath, p)
	return false
}

func checkEnum(schema, value any) bool {
	opts, _ := getArrayKey(schema, "enum")
	for _, opt := range opts {
		if isValueLike(opt) {
			if valueEqual(value, opt) {
				return true
			}
			continue
		}
		if deepEqual(value, opt) {
			return true
		}
	}
	return false
}

func errorEnum(ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkEnum(schema, value) {
		return true
	}
	opts, _ := getKey(schema, "enum")
	p := jsonx.NewObject()
	p.Set("allowedValues", opts)
	ctx.add("enum", schemaPath, instancePath, p)
	return false
}

func checkNot(w walk, schema, value any) bool {
	sub, _ := getKey(schema, "not")
	return !checkSchema(w, sub, value)
}

func errorNot(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	if checkNot(w, schema, value) {
		return true
	}
	ctx.add("not", schemaPath, instancePath, emptyParams())
	return false
}

func checkAllOf(w walk, schema, value any) bool {
	arr, _ := getArrayKey(schema, "allOf")
	for _, arm := range arr {
		if !checkSchema(w, arm, value) {
			return false
		}
	}
	return true
}

func errorAllOf(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	arr, _ := getArrayKey(schema, "allOf")
	var failed []*errCtx
	passed := 0
	for i, arm := range arr {
		next := &errCtx{}
		path := fmt.Sprintf("%s/allOf/%d", schemaPath, i)
		if errorSchema(w, next, path, instancePath, arm, value) {
			passed++
		} else {
			failed = append(failed, next)
		}
	}
	if passed == len(arr) {
		return true
	}
	for _, f := range failed {
		for _, e := range f.errors {
			ctx.addExisting(e)
		}
	}
	return false
}

func checkAnyOf(w walk, schema, value any) bool {
	arr, _ := getArrayKey(schema, "anyOf")
	for _, arm := range arr {
		if checkSchema(w, arm, value) {
			return true
		}
	}
	return false
}

func errorAnyOf(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	arr, _ := getArrayKey(schema, "anyOf")
	var failed []*errCtx
	passed := false
	for i, arm := range arr {
		next := &errCtx{}
		path := fmt.Sprintf("%s/anyOf/%d", schemaPath, i)
		if errorSchema(w, next, path, instancePath, arm, value) {
			passed = true
		} else {
			failed = append(failed, next)
		}
	}
	if passed {
		return true
	}
	for _, f := range failed {
		for _, e := range f.errors {
			ctx.addExisting(e)
		}
	}
	ctx.add("anyOf", schemaPath, instancePath, emptyParams())
	return false
}

func checkOneOf(w walk, schema, value any) bool {
	arr, _ := getArrayKey(schema, "oneOf")
	n := 0
	for _, arm := range arr {
		if checkSchema(w, arm, value) {
			n++
		}
	}
	return n == 1
}

func errorOneOf(w walk, ctx *errCtx, schemaPath, instancePath string, schema, value any) bool {
	arr, _ := getArrayKey(schema, "oneOf")
	var failed []*errCtx
	passing := []any{}
	for i, arm := range arr {
		next := &errCtx{}
		path := fmt.Sprintf("%s/oneOf/%d", schemaPath, i)
		if errorSchema(w, next, path, instancePath, arm, value) {
			passing = append(passing, float64(i))
		} else {
			failed = append(failed, next)
		}
	}
	if len(passing) == 1 {
		return true
	}
	if len(passing) == 0 {
		for _, f := range failed {
			for _, e := range f.errors {
				ctx.addExisting(e)
			}
		}
	}
	p := jsonx.NewObject()
	p.Set("passingSchemas", passing)
	ctx.add("oneOf", schemaPath, instancePath, p)
	return false
}

func checkRef(w walk, schema, value any) bool {
	ref, _ := getKey(schema, "$ref")
	s, _ := ref.(string)
	target, ok := resolveRef(w.root, s)
	if !ok || !isSchemaNode(target) {
		return false
	}
	return checkSchema(w, target, value)
}

func errorRef(w walk, ctx *errCtx, instancePath string, schema, value any) bool {
	ref, _ := getKey(schema, "$ref")
	s, _ := ref.(string)
	target, ok := resolveRef(w.root, s)
	// An unresolved ref is the boolean schema false (stack.Ref ?? false).
	if !ok || !isSchemaNode(target) {
		target = false
	}
	next := &errCtx{}
	if errorSchema(w, next, "#", instancePath, target, value) {
		return true
	}
	for _, e := range next.errors {
		ctx.addExisting(e)
	}
	return false
}

func resolveRef(root any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#") {
		return nil, false
	}
	if strings.HasSuffix(ref, "#") {
		return root, true
	}
	frag, err := js.DecodeURIComponent(ref[1:])
	if err != nil || !strings.HasPrefix(frag, "/") {
		return nil, false
	}
	return pointerGet(root, frag)
}

func pointerIndices(pointer string) []string {
	if pointer == "" {
		return nil
	}
	parts := strings.Split(pointer, "/")
	if len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		parts[i] = strings.ReplaceAll(p, "~0", "~")
	}
	return parts
}

func isNumericIndex(s string) bool {
	if s == "0" {
		return true
	}
	if s == "" || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func pointerGet(root any, pointer string) (any, bool) {
	cur := root
	for _, idx := range pointerIndices(pointer) {
		next, ok := pointerIndex(cur, idx)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func pointerIndex(value any, index string) (any, bool) {
	if isUnsafeKey(index) {
		return nil, false
	}
	switch x := value.(type) {
	case *Schema:
		if x == nil || x.boolSchema != nil || x.Object == nil {
			return nil, false
		}
		return x.Object.Get(index)
	case *jsonx.Object:
		if x == nil {
			return nil, false
		}
		return x.Get(index)
	case []any:
		if !isNumericIndex(index) {
			return nil, false
		}
		i, err := strconv.Atoi(index)
		if err != nil || i < 0 || i >= len(x) {
			return nil, false
		}
		return x[i], true
	default:
		return nil, false
	}
}

func canon(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return jsonx.Quote(x)
	case float64:
		return js.NumberToString(x)
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(canon(el))
		}
		b.WriteByte(']')
		return b.String()
	case *jsonx.Object:
		if x == nil {
			return "null"
		}
		keys := x.Keys()
		sort.Slice(keys, func(i, j int) bool { return js.CompareUTF16(keys[i], keys[j]) < 0 })
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(jsonx.Quote(k))
			b.WriteByte(':')
			val, _ := x.Get(k)
			b.WriteString(canon(val))
		}
		b.WriteByte('}')
		return b.String()
	default:
		if f, ok := asFloat(x); ok {
			return js.NumberToString(f)
		}
		return jsonx.Quote(fmt.Sprint(x))
	}
}

func limitOf(params *jsonx.Object) string {
	v, _ := params.Get("limit")
	return numString(v)
}

func messageEN(keyword string, params *jsonx.Object) string {
	switch keyword {
	case "additionalProperties":
		return "must not have additional properties"
	case "anyOf":
		return "must match a schema in anyOf"
	case "boolean":
		return "schema is false"
	case "const":
		return "must be equal to constant"
	case "enum":
		return "must be equal to one of the allowed values"
	case "exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum":
		cmp, _ := params.GetString("comparison")
		limit, _ := params.Get("limit")
		return "must be " + cmp + " " + numString(limit)
	case "format":
		f, _ := params.GetString("format")
		return `must match format "` + f + `"`
	case "maxItems":
		return "must not have more than " + limitOf(params) + " items"
	case "maxLength":
		return "must not have more than " + limitOf(params) + " characters"
	case "minItems":
		return "must not have fewer than " + limitOf(params) + " items"
	case "minLength":
		return "must not have fewer than " + limitOf(params) + " characters"
	case "multipleOf":
		m, _ := params.Get("multipleOf")
		return "must be multiple of " + numString(m)
	case "not":
		return "must not be valid"
	case "oneOf":
		return "must match exactly one schema in oneOf"
	case "pattern":
		p, _ := params.GetString("pattern")
		return `must match pattern "` + p + `"`
	case "required":
		req, _ := params.Get("requiredProperties")
		return "must have required properties " + strings.Join(stringsOf(req), ", ")
	case "type":
		t, _ := params.Get("type")
		if s, ok := t.(string); ok {
			return "must be " + s
		}
		return "must be either " + strings.Join(stringsOf(t), " or ")
	case "uniqueItems":
		return "must not have duplicate items"
	default:
		return "an unknown validation error occurred"
	}
}
