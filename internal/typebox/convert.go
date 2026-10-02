package typebox

import (
	"math"
	"math/big"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

const maxSafeInteger = 9007199254740991

// Convert coerces v the way TypeBox Value.Convert does. Object and array
// inputs are mutated in place. Schemas without a TypeBox kind (FromJSON,
// Unsafe) are returned unchanged.
func Convert(s *Schema, v any) any {
	if s == nil {
		return v
	}
	return convertNode(s, v)
}

func convertNode(schema, v any) any {
	s, ok := schema.(*Schema)
	if !ok || s == nil {
		return v
	}
	switch s.kind {
	case kindArray:
		return convertArray(s, v)
	case kindBoolean:
		if b, ok := tryBoolean(v); ok {
			return b
		}
		return v
	case kindEnum:
		return convertNode(evaluateEnum(s), v)
	case kindInteger:
		if n, ok := tryNumber(v); ok {
			return math.Trunc(n)
		}
		return v
	case kindIntersect:
		return convertNode(evaluateIntersect(schemaListOf(s, "allOf")), v)
	case kindLiteral:
		return convertLiteral(s, v)
	case kindNull:
		if tryNull(v) {
			return nil
		}
		return v
	case kindNumber:
		if n, ok := tryNumber(v); ok {
			return n
		}
		return v
	case kindObject:
		return convertObject(s, v)
	case kindRecord:
		return convertRecord(s, v)
	case kindRef:
		return v
	case kindString:
		if str, ok := tryString(v); ok {
			return str
		}
		return v
	case kindTuple:
		return convertTuple(s, v)
	case kindUnion:
		return convertUnion(s, v)
	default:
		return v
	}
}

func tryNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case nil:
		return 0, true
	case string:
		n := js.ToNumber(x)
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
		low := js.ToLower(x)
		if low == "false" {
			return 0, true
		}
		if low == "true" {
			return 1, true
		}
		if bi, ok := tryBigIntFloat(x); ok {
			return bi, true
		}
		return 0, false
	default:
		if f, ok := asFloat(x); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
		return 0, false
	}
}

func tryBigIntFloat(s string) (float64, bool) {
	re := mustRE(`^-?(0|[1-9]\d*)n$`, "")
	if !re.Test(s) {
		return 0, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s[:len(s)-1], 10); !ok {
		return 0, false
	}
	max := big.NewInt(maxSafeInteger)
	min := new(big.Int).Neg(max)
	if n.Cmp(max) > 0 || n.Cmp(min) < 0 {
		return 0, false
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	return f, true
}

func tryBoolean(v any) (bool, bool) {
	switch x := v.(type) {
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
	case nil:
		return false, true
	case string:
		low := js.ToLower(x)
		switch {
		case low == "false":
			return false, true
		case low == "true":
			return true, true
		case x == "0":
			return false, true
		case x == "1":
			return true, true
		default:
			return false, false
		}
	default:
		if f, ok := asFloat(x); ok {
			if f == 0 {
				return false, true
			}
			if f == 1 {
				return true, true
			}
		}
		return false, false
	}
}

func tryString(v any) (string, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", false
		}
		return js.NumberToString(x), true
	case nil:
		return "null", true
	case string:
		return x, true
	default:
		if f, ok := asFloat(x); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return js.NumberToString(f), true
		}
		return "", false
	}
}

func tryNull(v any) bool {
	switch x := v.(type) {
	case bool:
		return !x
	case float64:
		return x == 0
	case nil:
		return true
	case string:
		low := js.ToLower(x)
		return low == "undefined" || low == "null" || x == "" || x == "0"
	default:
		if f, ok := asFloat(x); ok {
			return f == 0
		}
		return false
	}
}

func convertArray(s *Schema, v any) any {
	item, _ := s.Get("items")
	arr, ok := dataArray(v)
	if !ok {
		arr = []any{v}
	}
	out := make([]any, len(arr))
	for i, el := range arr {
		out[i] = convertNode(item, el)
	}
	return out
}

func convertTuple(s *Schema, v any) any {
	arr, ok := dataArray(v)
	if !ok {
		return v
	}
	items, _ := s.GetArray("items")
	n := len(items)
	if len(arr) < n {
		n = len(arr)
	}
	for i := 0; i < n; i++ {
		arr[i] = convertNode(items[i], arr[i])
	}
	return arr
}

type reEntry struct {
	re     *jsre.Regexp
	schema any
}

func entriesRegExp(obj *jsonx.Object) []reEntry {
	if obj == nil {
		return nil
	}
	out := make([]reEntry, 0, obj.Len())
	for _, k := range obj.Keys() {
		re, err := compileRE("^"+k+"$", "")
		val, _ := obj.Get(k)
		if err != nil {
			re = nil
		}
		out = append(out, reEntry{re: re, schema: val})
	}
	return out
}

func schemaObjectValue(v any) bool {
	if _, ok := schemaBool(v); ok {
		return false
	}
	_, ok := schemaObject(v)
	return ok
}

func convertProps(entries []reEntry, v *jsonx.Object) {
	keys := v.Keys()
	for _, e := range entries {
		for _, key := range keys {
			if e.re == nil || !e.re.Test(key) {
				continue
			}
			cur, _ := v.Get(key)
			v.Set(key, convertNode(e.schema, cur))
		}
	}
}

func fromAdditional(entries []reEntry, add any, v *jsonx.Object) {
	keys := v.Keys()
	for _, e := range entries {
		for _, key := range keys {
			if e.re != nil && e.re.Test(key) {
				continue
			}
			cur, _ := v.Get(key)
			v.Set(key, convertNode(add, cur))
		}
	}
}

func convertObject(s *Schema, v any) any {
	o, ok := dataObject(v)
	if !ok {
		return v
	}
	props, _ := s.GetObject("properties")
	entries := entriesRegExp(props)
	convertProps(entries, o)
	if add, ok := s.Get("additionalProperties"); ok && schemaObjectValue(add) {
		fromAdditional(entries, add, o)
	}
	return o
}

func convertRecord(s *Schema, v any) any {
	o, ok := dataObject(v)
	if !ok {
		return v
	}
	pp, _ := s.GetObject("patternProperties")
	entries := entriesRegExp(pp)
	convertProps(entries, o)
	if add, ok := s.Get("additionalProperties"); ok && schemaObjectValue(add) {
		fromAdditional(entries, add, o)
	}
	return o
}

func convertLiteral(s *Schema, v any) any {
	c, ok := s.Get("const")
	if ok && isValueLike(c) && valueEqual(v, c) {
		return v
	}
	typ, _ := s.GetString("type")
	switch typ {
	case "boolean":
		if b, ok := tryBoolean(v); ok && valueEqual(b, c) {
			return b
		}
	case "number":
		if n, ok := tryNumber(v); ok && valueEqual(n, c) {
			return n
		}
	case "string":
		if str, ok := tryString(v); ok && valueEqual(str, c) {
			return str
		}
	}
	return v
}

func convertUnion(s *Schema, v any) any {
	arms := schemaListOf(s, "anyOf")
	for _, arm := range arms {
		if checkSchema(walk{root: arm}, arm, v) {
			return v
		}
	}
	for _, arm := range arms {
		converted := convertNode(arm, jsonx.CloneValue(v))
		if checkSchema(walk{root: s}, s, converted) {
			return converted
		}
	}
	return v
}

func neverSchema() *Schema {
	o := jsonx.NewObject()
	o.Set("not", jsonx.NewObject())
	return &Schema{Object: o, kind: kindNever}
}

func evaluateEnum(s *Schema) *Schema {
	if s == nil {
		return neverSchema()
	}
	vals, _ := s.GetArray("enum")
	lits := make([]*Schema, len(vals))
	for i, v := range vals {
		lits[i] = Literal(v)
	}
	return evaluateUnionFast(lits)
}

func evaluateUnionFast(types []*Schema) *Schema {
	switch len(types) {
	case 0:
		return neverSchema()
	case 1:
		return types[0]
	default:
		return Union(types)
	}
}

func evaluateType(s *Schema) *Schema {
	if s == nil {
		return s
	}
	switch s.kind {
	case kindEnum:
		return evaluateEnum(s)
	case kindIntersect:
		return evaluateIntersect(schemaListOf(s, "allOf"))
	case kindUnion:
		return evaluateUnionFast(flattenSchemas(schemaListOf(s, "anyOf")))
	default:
		return s
	}
}

func evaluateIntersect(types []*Schema) *Schema {
	dist := distribute(types, nil)
	return evaluateUnionFast(broaden(dist))
}

func distribute(types []*Schema, result []*Schema) []*Schema {
	if len(types) == 0 {
		return result
	}
	left := types[0]
	if left != nil && left.kind == kindUnion {
		return distribute(types[1:], distributeUnion(schemaListOf(left, "anyOf"), result))
	}
	return distribute(types[1:], distributeType(left, result))
}

func distributeUnion(types []*Schema, distribution []*Schema) []*Schema {
	var result []*Schema
	for _, left := range types {
		result = append(result, distribute([]*Schema{left}, distribution)...)
	}
	return result
}

func distributeType(typ *Schema, types []*Schema) []*Schema {
	if len(types) == 0 {
		return []*Schema{typ}
	}
	out := make([]*Schema, 0, len(types))
	for _, left := range types {
		out = append(out, distributeOp(left, typ))
	}
	return out
}

func distributeOp(left, right *Schema) *Schema {
	el := evaluateType(left)
	er := evaluateType(right)
	if (el != nil && el.kind == kindUnion) || (er != nil && er.kind == kindUnion) {
		return evaluateIntersect([]*Schema{el, er})
	}
	return narrow(el, er)
}

func canComposite(s *Schema) bool {
	return s != nil && (s.kind == kindObject || s.kind == kindTuple)
}

func literalBaseKind(s *Schema) string {
	if s == nil {
		return ""
	}
	switch t, _ := s.GetString("type"); t {
	case "string":
		return kindString
	case "number":
		return kindNumber
	case "boolean":
		return kindBoolean
	default:
		return ""
	}
}

func narrow(left, right *Schema) *Schema {
	if left == nil {
		return neverSchema()
	}
	switch left.kind {
	case kindNever, kindAny:
		return left
	case kindUnknown:
		return right
	}
	if right == nil {
		return neverSchema()
	}
	switch right.kind {
	case kindNever, kindAny:
		return right
	case kindUnknown:
		return left
	}
	lc, rc := canComposite(left), canComposite(right)
	if lc && rc {
		return composite(left, right)
	}
	if lc {
		return left
	}
	if rc {
		return right
	}
	if left.kind != "" && left.kind == right.kind {
		return right
	}
	if left.kind == kindLiteral && literalBaseKind(left) == right.kind {
		return left
	}
	if right.kind == kindLiteral && literalBaseKind(right) == left.kind {
		return right
	}
	return neverSchema()
}

func broaden(types []*Schema) []*Schema {
	var result []*Schema
	for _, t := range types {
		ev := evaluateType(t)
		if ev == nil || ev.kind == kindNever {
			continue
		}
		if ev.kind == kindAny || ev.kind == kindUnknown {
			return []*Schema{ev}
		}
		result = append(result, ev)
	}
	return flattenSchemas(result)
}

func composite(left, right *Schema) *Schema {
	lp := propsOf(left)
	rp := propsOf(right)
	seen := map[string]bool{}
	var keys []string
	if lp != nil {
		for _, k := range lp.Keys() {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	if rp != nil {
		for _, k := range rp.Keys() {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
	}
	m := omap.NewMap[string, *Schema]()
	for _, k := range keys {
		m.Set(k, compositeProp(schemaAt(lp, k), schemaAt(rp, k)))
	}
	return Object(m)
}

func propsOf(s *Schema) *jsonx.Object {
	if s == nil {
		return jsonx.NewObject()
	}
	if s.kind == kindObject {
		o, _ := s.GetObject("properties")
		if o == nil {
			return jsonx.NewObject()
		}
		return o
	}
	if s.kind == kindTuple {
		items, _ := s.GetArray("items")
		o := jsonx.NewObject()
		for i, it := range items {
			o.Set(intKey(i), it)
		}
		return o
	}
	return jsonx.NewObject()
}

func intKey(i int) string {
	return js.NumberToString(float64(i))
}

func schemaAt(o *jsonx.Object, key string) *Schema {
	if o == nil {
		return nil
	}
	v, ok := o.Get(key)
	if !ok {
		return nil
	}
	s, _ := v.(*Schema)
	return s
}

func compositeProp(left, right *Schema) *Schema {
	switch {
	case left != nil && right == nil:
		return withOptional(left, false)
	case left == nil && right != nil:
		return withOptional(right, false)
	case left == nil && right == nil:
		return neverSchema()
	}
	opt := left.optional && right.optional
	ev := evaluateIntersect([]*Schema{stripOptional(left), stripOptional(right)})
	return withOptional(ev, opt)
}

func stripOptional(s *Schema) *Schema {
	if s == nil || !s.optional {
		return s
	}
	c := *s
	c.optional = false
	return &c
}

func withOptional(s *Schema, opt bool) *Schema {
	if s == nil {
		return nil
	}
	if s.optional == opt {
		return s
	}
	c := *s
	c.optional = opt
	return &c
}
