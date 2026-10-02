package typebox

import (
	"encoding/json"
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	kindAny       = "Any"
	kindArray     = "Array"
	kindBoolean   = "Boolean"
	kindEnum      = "Enum"
	kindInteger   = "Integer"
	kindIntersect = "Intersect"
	kindLiteral   = "Literal"
	kindNever     = "Never"
	kindNull      = "Null"
	kindNumber    = "Number"
	kindObject    = "Object"
	kindRecord    = "Record"
	kindRef       = "Ref"
	kindString    = "String"
	kindTuple     = "Tuple"
	kindUnion     = "Union"
	kindUnknown   = "Unknown"
)

const (
	stringKey  = "^.*$"
	integerKey = "^-?(?:0|[1-9][0-9]*)$"
	numberKey  = "^-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?$"
)

// Schema is a TypeBox type. JSON.stringify order is the embedded object's key
// order. Kind and optional are not enumerable, matching TypeBox 1.3.27.
type Schema struct {
	*jsonx.Object
	kind       string
	optional   bool
	unsafe     bool
	boolSchema *bool
}

// MarshalJSON emits the enumerable schema (or a boolean schema).
func (s *Schema) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	if s.boolSchema != nil {
		if *s.boolSchema {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	}
	if s.Object == nil {
		return []byte("{}"), nil
	}
	return s.Object.MarshalJSON()
}

func applyOpts(o *jsonx.Object, opts []*jsonx.Object) {
	for _, opt := range opts {
		o.Assign(opt)
	}
}

func newKind(kind string, o *jsonx.Object, opts []*jsonx.Object) *Schema {
	applyOpts(o, opts)
	return &Schema{Object: o, kind: kind}
}

func typeObject(kind, typ string, opts []*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("type", typ)
	return newKind(kind, o, opts)
}

// String builds {type:"string", ...opts}.
func String(opts ...*jsonx.Object) *Schema { return typeObject(kindString, "string", opts) }

// Number builds {type:"number", ...opts}.
func Number(opts ...*jsonx.Object) *Schema { return typeObject(kindNumber, "number", opts) }

// Integer builds {type:"integer", ...opts}.
func Integer(opts ...*jsonx.Object) *Schema { return typeObject(kindInteger, "integer", opts) }

// Boolean builds {type:"boolean", ...opts}.
func Boolean(opts ...*jsonx.Object) *Schema { return typeObject(kindBoolean, "boolean", opts) }

// Null builds {type:"null", ...opts}.
func Null(opts ...*jsonx.Object) *Schema { return typeObject(kindNull, "null", opts) }

// Unknown builds {}.
func Unknown(opts ...*jsonx.Object) *Schema {
	return newKind(kindUnknown, jsonx.NewObject(), opts)
}

// Any builds {}.
func Any(opts ...*jsonx.Object) *Schema {
	return newKind(kindAny, jsonx.NewObject(), opts)
}

// Literal builds {type, const}. v must be a string, boolean, or number.
func Literal(v any, opts ...*jsonx.Object) *Schema {
	typ, ok := literalTypeName(v)
	if !ok {
		panic(fmt.Sprintf("typebox: invalid literal %T", v))
	}
	o := jsonx.NewObject()
	o.Set("type", typ)
	o.Set("const", normJSON(v))
	return newKind(kindLiteral, o, opts)
}

// Enum builds {enum:values}. Values are strings or numbers.
func Enum(values []any, opts ...*jsonx.Object) *Schema {
	arr := make([]any, len(values))
	for i, v := range values {
		arr[i] = normJSON(v)
	}
	o := jsonx.NewObject()
	o.Set("enum", arr)
	return newKind(kindEnum, o, opts)
}

func schemaArray(items []*Schema) []any {
	arr := make([]any, len(items))
	for i, s := range items {
		arr[i] = s
	}
	return arr
}

// Union builds {anyOf}.
func Union(items []*Schema, opts ...*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("anyOf", schemaArray(items))
	return newKind(kindUnion, o, opts)
}

// Intersect builds {allOf}.
func Intersect(items []*Schema, opts ...*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("allOf", schemaArray(items))
	return newKind(kindIntersect, o, opts)
}

// Array builds {type:"array", items}.
func Array(item *Schema, opts ...*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("type", "array")
	o.Set("items", item)
	return newKind(kindArray, o, opts)
}

// Tuple builds {type:"array", additionalItems:false, items, minItems}.
func Tuple(items []*Schema, opts ...*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("type", "array")
	o.Set("additionalItems", false)
	o.Set("items", schemaArray(items))
	o.Set("minItems", float64(len(items)))
	return newKind(kindTuple, o, opts)
}

// Object builds {type:"object", required?, properties}. Required lists every
// property that is not Optional, in JavaScript property order. It is omitted
// when every property is optional.
func Object(props *omap.Map[string, *Schema], opts ...*jsonx.Object) *Schema {
	properties := jsonx.NewObject()
	if props != nil {
		for _, k := range props.Keys() {
			s, _ := props.Get(k)
			properties.Set(k, s)
		}
	}
	o := jsonx.NewObject()
	o.Set("type", "object")
	var req []any
	for _, k := range properties.Keys() {
		s, _ := properties.Get(k)
		if sch, ok := s.(*Schema); ok && sch.optional {
			continue
		}
		req = append(req, k)
	}
	if len(req) > 0 {
		o.Set("required", req)
	}
	o.Set("properties", properties)
	return newKind(kindObject, o, opts)
}

// Optional marks s so Object leaves the property out of "required".
func Optional(s *Schema) *Schema {
	if s == nil {
		return nil
	}
	c := *s
	c.optional = true
	return &c
}

// Unsafe wraps a foreign schema (Type.Unsafe). The value is not coerced.
func Unsafe(raw *jsonx.Object) *Schema {
	if raw == nil {
		raw = jsonx.NewObject()
	} else {
		raw = raw.Clone()
	}
	return &Schema{Object: raw, unsafe: true}
}

// Ref builds {$ref}.
func Ref(ref string, opts ...*jsonx.Object) *Schema {
	o := jsonx.NewObject()
	o.Set("$ref", ref)
	return newKind(kindRef, o, opts)
}

// FromJSON wraps a parsed JSON schema. Boolean schemas and objects are
// accepted. The result has no TypeBox kind, so Convert does not coerce it.
func FromJSON(v any) (*Schema, error) {
	switch x := v.(type) {
	case *Schema:
		if x == nil {
			return nil, fmt.Errorf("typebox: nil schema")
		}
		return x, nil
	case bool:
		b := x
		return &Schema{boolSchema: &b}, nil
	case *jsonx.Object:
		if x == nil {
			return nil, fmt.Errorf("typebox: nil schema")
		}
		return &Schema{Object: x}, nil
	case string:
		parsed, err := jsonx.Parse([]byte(x))
		if err != nil {
			return nil, err
		}
		return FromJSON(parsed)
	case []byte:
		parsed, err := jsonx.Parse(x)
		if err != nil {
			return nil, err
		}
		return FromJSON(parsed)
	case json.RawMessage:
		parsed, err := jsonx.Parse(x)
		if err != nil {
			return nil, err
		}
		return FromJSON(parsed)
	default:
		return nil, fmt.Errorf("typebox: cannot build schema from %T", v)
	}
}

func createRecord(pattern string, value *Schema) *Schema {
	pp := jsonx.NewObject()
	pp.Set(pattern, value)
	o := jsonx.NewObject()
	o.Set("type", "object")
	o.Set("patternProperties", pp)
	return &Schema{Object: o, kind: kindRecord}
}

func objectProps(props *omap.Map[string, *Schema]) *Schema {
	return Object(props)
}

func oneProp(key string, value *Schema) *Schema {
	m := omap.NewMap[string, *Schema]()
	m.Set(key, value)
	return objectProps(m)
}

// Record builds a patternProperties object, or a fixed-key object when the
// key type narrows to literals (TypeBox FromKey).
func Record(key, value *Schema, opts ...*jsonx.Object) *Schema {
	built := fromKey(key, value)
	applyOpts(built.Object, opts)
	return built
}

func fromKey(key, value *Schema) *Schema {
	if key == nil {
		return objectProps(nil)
	}
	switch key.kind {
	case kindAny:
		return createRecord(stringKey, value)
	case kindBoolean:
		m := omap.NewMap[string, *Schema]()
		m.Set("true", value)
		m.Set("false", value)
		return objectProps(m)
	case kindEnum:
		return fromKey(evaluateEnum(key), value)
	case kindInteger:
		return createRecord(integerKey, value)
	case kindIntersect:
		return fromKey(evaluateIntersect(schemaListOf(key, "allOf")), value)
	case kindLiteral:
		return fromLiteralKey(key, value)
	case kindNumber:
		return createRecord(numberKey, value)
	case kindUnion:
		return fromUnionKey(schemaListOf(key, "anyOf"), value)
	case kindString:
		if pat, ok := key.GetString("pattern"); ok {
			return createRecord(pat, value)
		}
		return createRecord(stringKey, value)
	default:
		return objectProps(nil)
	}
}

func fromLiteralKey(key, value *Schema) *Schema {
	c, ok := key.Get("const")
	if !ok {
		return objectProps(nil)
	}
	switch x := c.(type) {
	case string:
		return oneProp(x, value)
	case float64:
		return oneProp(numString(x), value)
	case bool:
		if x {
			return oneProp("true", value)
		}
		return oneProp("false", value)
	default:
		return objectProps(nil)
	}
}

func schemaListOf(s *Schema, key string) []*Schema {
	if s == nil || s.Object == nil {
		return nil
	}
	a, ok := s.GetArray(key)
	if !ok {
		return nil
	}
	out := make([]*Schema, 0, len(a))
	for _, el := range a {
		if sch, ok := el.(*Schema); ok {
			out = append(out, sch)
		}
	}
	return out
}

func flattenSchemas(types []*Schema) []*Schema {
	var out []*Schema
	var walk func(*Schema)
	walk = func(s *Schema) {
		if s != nil && s.kind == kindUnion {
			for _, a := range schemaListOf(s, "anyOf") {
				walk(a)
			}
			return
		}
		out = append(out, s)
	}
	for _, s := range types {
		walk(s)
	}
	return out
}

func fromUnionKey(types []*Schema, value *Schema) *Schema {
	flat := flattenSchemas(types)
	for _, t := range flat {
		if t != nil && (t.kind == kindString || t.kind == kindNumber || t.kind == kindInteger) {
			return createRecord(stringKey, value)
		}
	}
	m := omap.NewMap[string, *Schema]()
	for _, t := range flat {
		if t == nil || t.kind != kindLiteral {
			continue
		}
		c, ok := t.Get("const")
		if !ok {
			continue
		}
		switch x := c.(type) {
		case string:
			m.Set(x, value)
		case float64:
			m.Set(numString(x), value)
		}
	}
	return objectProps(m)
}
