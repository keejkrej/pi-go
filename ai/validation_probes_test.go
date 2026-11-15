package ai_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func vpSchemaJSON(t *testing.T, raw string) *typebox.Schema {
	t.Helper()
	v, err := jsonx.ParseString(raw)
	if err != nil {
		t.Fatalf("parse schema json %s: %v", raw, err)
	}
	s, err := typebox.FromJSON(v)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	return s
}

func vpObjFromJSON(t *testing.T, raw string) *jsonx.Object {
	t.Helper()
	v, err := jsonx.ParseString(raw)
	if err != nil {
		t.Fatalf("parse json %s: %v", raw, err)
	}
	obj, ok := v.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected JSON object, got %T", v)
	}
	return obj
}

func vpValidateJSON(t *testing.T, schema *typebox.Schema, argsJSON string) (*jsonx.Object, error) {
	t.Helper()
	tool := &ai.Tool{
		Name:        "probe",
		Description: "Validation probe",
		Parameters:  schema,
	}
	call := &ai.ToolCall{
		Type:      "toolCall",
		Id:        "probe-1",
		Name:      "probe",
		Arguments: vpObjFromJSON(t, argsJSON),
	}
	res, err := ai.ValidateToolArguments(tool, call)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	return obj, nil
}

func TestValidationProbesRecursiveAndRefs(t *testing.T) {
	t.Run("self-recursive tree via $defs/$ref accepts nested nodes and rejects invalid leaf", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"$defs": {
				"Node": {
					"type": "object",
					"properties": {
						"value": { "type": "string" },
						"children": {
							"type": "array",
							"items": { "$ref": "#/$defs/Node" }
						}
					},
					"required": ["value"]
				}
			},
			"type": "object",
			"properties": {
				"root": { "$ref": "#/$defs/Node" }
			},
			"required": ["root"]
		}`)

		res, err := vpValidateJSON(t, schema, `{
			"root": {
				"value": "a",
				"children": [
					{ "value": "b", "children": [{ "value": "c" }] }
				]
			}
		}`)
		if err != nil {
			t.Fatalf("expected valid tree, got %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil validated object")
		}

		_, err = vpValidateJSON(t, schema, `{
			"root": {
				"value": "a",
				"children": [
					{ "children": [] }
				]
			}
		}`)
		if err == nil {
			t.Fatal("expected missing required value in nested child to fail")
		}
	})

	t.Run("mutually recursive A and B schemas", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"definitions": {
				"A": {
					"type": "object",
					"properties": {
						"label": { "type": "string" },
						"b": { "$ref": "#/definitions/B" }
					},
					"required": ["label"]
				},
				"B": {
					"type": "object",
					"properties": {
						"count": { "type": "integer" },
						"a": { "$ref": "#/definitions/A" }
					},
					"required": ["count"]
				}
			},
			"type": "object",
			"properties": {
				"start": { "$ref": "#/definitions/A" }
			},
			"required": ["start"]
		}`)

		if _, err := vpValidateJSON(t, schema, `{"start":{"label":"x","b":{"count":2,"a":{"label":"y"}}}}`); err != nil {
			t.Fatalf("expected mutual recursion to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"start":{"label":"x","b":{"count":1.5}}}`); err == nil {
			t.Fatal("expected float in integer field to fail")
		}
	})

	t.Run("unresolved internal or external $ref fails cleanly without panic", func(t *testing.T) {
		missingRef := vpSchemaJSON(t, `{"type":"object","properties":{"x":{"$ref":"#/$defs/Nope"}},"required":["x"]}`)
		if _, err := vpValidateJSON(t, missingRef, `{"x":"hi"}`); err == nil {
			t.Fatal("expected unresolved $ref to fail")
		}

		externalRef := vpSchemaJSON(t, `{"type":"object","properties":{"x":{"$ref":"https://example.com/x.json"}},"required":["x"]}`)
		if _, err := vpValidateJSON(t, externalRef, `{"x":"hi"}`); err == nil {
			t.Fatal("expected external $ref to fail cleanly")
		}
	})

	t.Run("JSON pointer tilde escapes (~0 and ~1)", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"$defs": {
				"a/b~c": { "type": "integer" }
			},
			"type": "object",
			"properties": {
				"val": { "$ref": "#/$defs/a~1b~0c" }
			},
			"required": ["val"]
		}`)
		if _, err := vpValidateJSON(t, schema, `{"val":7}`); err != nil {
			t.Fatalf("expected escaped $ref to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"val":"not-int"}`); err == nil {
			t.Fatal("expected non-integer to fail escaped $ref")
		}
	})
}

func TestValidationProbesTypeBoxConstructors(t *testing.T) {
	t.Run("Record with string, narrowed literal union, and integer keys", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("scores", typebox.Record(typebox.Any(), typebox.Number()))
		unionKey := typebox.Union([]*typebox.Schema{typebox.Literal("left"), typebox.Literal("right")})
		props.Set("sides", typebox.Optional(typebox.Record(unionKey, typebox.String())))
		props.Set("indexed", typebox.Optional(typebox.Record(typebox.Integer(), typebox.Boolean())))
		schema := typebox.Object(props)

		out, err := vpValidateJSON(t, schema, `{
			"scores": { "alice": "9.5", "bob": 10 },
			"sides": { "left": "L", "right": "R" },
			"indexed": { "0": "true", "1": false }
		}`)
		if err != nil {
			t.Fatalf("expected valid records, got %v", err)
		}
		scores, _ := out.GetObject("scores")
		if v, ok := scores.GetNumber("alice"); !ok || v != 9.5 {
			t.Fatalf("expected coerced alice=9.5, got %v (ok=%v)", v, ok)
		}
		indexed, _ := out.GetObject("indexed")
		if v, ok := indexed.GetBool("0"); !ok || !v {
			t.Fatalf("expected coerced indexed[\"0\"]=true, got %v (ok=%v)", v, ok)
		}

		if _, err := vpValidateJSON(t, schema, `{"scores":{"alice":"nan-word"}}`); err == nil {
			t.Fatal("expected non-numeric record value to fail")
		}
	})

	t.Run("Intersect and Union discriminated objects", func(t *testing.T) {
		aProps := omap.NewMap[string, *typebox.Schema]()
		aProps.Set("a", typebox.String())
		bProps := omap.NewMap[string, *typebox.Schema]()
		bProps.Set("b", typebox.Integer())

		u1Props := omap.NewMap[string, *typebox.Schema]()
		u1Props.Set("kind", typebox.Literal("one"))
		u1Props.Set("val", typebox.String())
		u2Props := omap.NewMap[string, *typebox.Schema]()
		u2Props.Set("kind", typebox.Literal("two"))
		u2Props.Set("num", typebox.Number())

		rootProps := omap.NewMap[string, *typebox.Schema]()
		rootProps.Set("combined", typebox.Intersect([]*typebox.Schema{
			typebox.Object(aProps),
			typebox.Object(bProps),
		}))
		rootProps.Set("choice", typebox.Union([]*typebox.Schema{
			typebox.Object(u1Props),
			typebox.Object(u2Props),
		}))
		schema := typebox.Object(rootProps)

		if _, err := vpValidateJSON(t, schema, `{"combined":{"a":"hi","b":3},"choice":{"kind":"two","num":4.5}}`); err != nil {
			t.Fatalf("expected intersect+union to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"combined":{"a":"hi"},"choice":{"kind":"one","val":"ok"}}`); err == nil {
			t.Fatal("expected missing intersect member to fail")
		}
		if _, err := vpValidateJSON(t, schema, `{"combined":{"a":"hi","b":3},"choice":{"kind":"three"}}`); err == nil {
			t.Fatal("expected unmatched union variant to fail")
		}
	})

	t.Run("Tuple and Enum", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("pair", typebox.Tuple([]*typebox.Schema{typebox.String(), typebox.Integer()}))
		props.Set("color", typebox.Enum([]any{"red", "green", "blue"}))
		schema := typebox.Object(props)

		if _, err := vpValidateJSON(t, schema, `{"pair":["k",5],"color":"green"}`); err != nil {
			t.Fatalf("expected tuple+enum to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"pair":["k"],"color":"green"}`); err == nil {
			t.Fatal("expected short tuple to fail")
		}
		if _, err := vpValidateJSON(t, schema, `{"pair":["k",5],"color":"yellow"}`); err == nil {
			t.Fatal("expected invalid enum to fail")
		}
	})
}

func TestValidationProbesJSONSchemaKeywords(t *testing.T) {
	t.Run("additionalProperties false and typed", func(t *testing.T) {
		strict := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "a": { "type": "string" } },
			"additionalProperties": false
		}`)
		if _, err := vpValidateJSON(t, strict, `{"a":"ok","extra":1}`); err == nil {
			t.Fatal("expected extra property to be rejected")
		}
		if _, err := vpValidateJSON(t, strict, `{"a":"ok"}`); err != nil {
			t.Fatalf("expected strict match, got %v", err)
		}

		typed := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "a": { "type": "string" } },
			"additionalProperties": { "type": "integer" }
		}`)
		if _, err := vpValidateJSON(t, typed, `{"a":"ok","extra":42}`); err != nil {
			t.Fatalf("expected typed additionalProperties to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, typed, `{"a":"ok","extra":"nope"}`); err == nil {
			t.Fatal("expected string additional property to fail integer schema")
		}
	})

	t.Run("oneOf, not, and allOf combinators", func(t *testing.T) {
		oneOfSchema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"v": {
					"oneOf": [
						{ "type": "integer", "multipleOf": 3 },
						{ "type": "integer", "multipleOf": 5 }
					]
				}
			},
			"required": ["v"]
		}`)
		if _, err := vpValidateJSON(t, oneOfSchema, `{"v":9}`); err != nil {
			t.Fatalf("expected 9 to match exactly one branch, got %v", err)
		}
		if _, err := vpValidateJSON(t, oneOfSchema, `{"v":15}`); err == nil {
			t.Fatal("expected 15 matching both branches of oneOf to fail")
		}

		notSchema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"v": { "not": { "type": "string" } }
			},
			"required": ["v"]
		}`)
		if _, err := vpValidateJSON(t, notSchema, `{"v":123}`); err != nil {
			t.Fatalf("expected number to pass not string, got %v", err)
		}
		if _, err := vpValidateJSON(t, notSchema, `{"v":"abc"}`); err == nil {
			t.Fatal("expected string to fail not string")
		}

		allOfSchema := vpSchemaJSON(t, `{
			"type": "object",
			"allOf": [
				{ "properties": { "a": { "type": "string" } }, "required": ["a"] },
				{ "properties": { "b": { "type": "integer", "minimum": 10 } }, "required": ["b"] }
			]
		}`)
		if _, err := vpValidateJSON(t, allOfSchema, `{"a":"ok","b":12}`); err != nil {
			t.Fatalf("expected allOf match to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, allOfSchema, `{"a":"ok","b":2}`); err == nil {
			t.Fatal("expected failed minimum in allOf branch to fail")
		}
	})

	t.Run("uniqueItems with structural object equality", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"items": { "type": "array", "uniqueItems": true }
			},
			"required": ["items"]
		}`)
		if _, err := vpValidateJSON(t, schema, `{"items":[{"a":1,"b":2},{"b":2,"a":3}]}`); err != nil {
			t.Fatalf("expected distinct items to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"items":[{"a":1,"b":2},{"b":2,"a":1}]}`); err == nil {
			t.Fatal("expected structurally equal objects to fail uniqueItems")
		}
	})

	t.Run("multipleOf, exclusiveMinimum, minLength with astral runes", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"step": { "type": "number", "multipleOf": 0.25, "exclusiveMinimum": 0 },
				"emoji": { "type": "string", "minLength": 2, "maxLength": 2 }
			}
		}`)
		if _, err := vpValidateJSON(t, schema, `{"step":0.75,"emoji":"😀🎉"}`); err != nil {
			t.Fatalf("expected 2 astral runes and multipleOf 0.25 to pass, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"step":0}`); err == nil {
			t.Fatal("expected 0 to fail exclusiveMinimum: 0")
		}
		if _, err := vpValidateJSON(t, schema, `{"step":0.3}`); err == nil {
			t.Fatal("expected 0.3 to fail multipleOf: 0.25")
		}
		if _, err := vpValidateJSON(t, schema, `{"emoji":"😀"}`); err == nil {
			t.Fatal("expected single astral rune to fail minLength: 2")
		}
	})

	t.Run("patternProperties, prefixItems, minItems/maxItems, and string formats", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"meta": {
					"type": "object",
					"patternProperties": {
						"^x-": { "type": "string" }
					},
					"additionalProperties": false
				},
				"coords": {
					"type": "array",
					"prefixItems": [{ "type": "number" }, { "type": "number" }],
					"minItems": 2,
					"maxItems": 3
				},
				"email": { "type": "string", "format": "email" },
				"id": { "type": "string", "format": "uuid" }
			}
		}`)
		if _, err := vpValidateJSON(t, schema, `{
			"meta": { "x-env": "prod" },
			"coords": [1.5, 2.5],
			"email": "user@example.com",
			"id": "550e8400-e29b-41d4-a716-446655440000"
		}`); err != nil {
			t.Fatalf("expected valid patternProperties/prefixItems/formats, got %v", err)
		}
		if _, err := vpValidateJSON(t, schema, `{"meta":{"other":"val"}}`); err == nil {
			t.Fatal("expected disallowed property outside patternProperties to fail")
		}
		if _, err := vpValidateJSON(t, schema, `{"coords":[1.5]}`); err == nil {
			t.Fatal("expected coords shorter than minItems to fail")
		}
		if _, err := vpValidateJSON(t, schema, `{"email":"not-an-email"}`); err == nil {
			t.Fatal("expected invalid email format to fail")
		}
		if _, err := vpValidateJSON(t, schema, `{"id":"bad-uuid"}`); err == nil {
			t.Fatal("expected invalid uuid format to fail")
		}
	})

	t.Run("boolean false schema rejects and boolean true schema accepts", func(t *testing.T) {
		rejectAll := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "x": false },
			"required": ["x"]
		}`)
		if _, err := vpValidateJSON(t, rejectAll, `{"x":1}`); err == nil {
			t.Fatal("expected boolean false property schema to reject")
		}

		acceptAny := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "x": true },
			"required": ["x"]
		}`)
		if _, err := vpValidateJSON(t, acceptAny, `{"x":{"anything":[1,2,3]}}`); err != nil {
			t.Fatalf("expected boolean true schema to accept any value, got %v", err)
		}
	})
}

func TestValidationProbesInvalidSchemasAndEdgeKeys(t *testing.T) {
	t.Run("invalid regex pattern fails gracefully without panic", func(t *testing.T) {
		badRegex := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "s": { "type": "string", "pattern": "[unclosed" } },
			"required": ["s"]
		}`)
		_, err := vpValidateJSON(t, badRegex, `{"s":"hello"}`)
		if err == nil {
			t.Fatal("expected invalid regex pattern to fail validation or compilation")
		}
	})

	t.Run("prototype-like keys handled safely and input arguments are not mutated", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": {
				"__proto__": { "type": "string" },
				"constructor": { "type": "integer" },
				"count": { "type": "integer" }
			}
		}`)
		origArgs := vpObjFromJSON(t, `{"__proto__":"safe","constructor":"7","count":"42"}`)
		tool := &ai.Tool{
			Name:        "edge_tool",
			Description: "Edge keys",
			Parameters:  schema,
		}
		call := &ai.ToolCall{
			Type:      "toolCall",
			Id:        "call-edge",
			Name:      "edge_tool",
			Arguments: origArgs,
		}
		validatedRaw, err := ai.ValidateToolCall([]*ai.Tool{tool}, call)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		validated, ok := validatedRaw.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object, got %T", validatedRaw)
		}
		if protoVal, _ := validated.GetString("__proto__"); protoVal != "safe" {
			t.Fatalf("expected __proto__='safe', got %q", protoVal)
		}
		if ctorVal, ok := validated.GetNumber("constructor"); !ok || ctorVal != 7 {
			t.Fatalf("expected coerced constructor=7, got %v (ok=%v)", ctorVal, ok)
		}
		// Original call.Arguments must retain original string values.
		if rawCount, ok := origArgs.GetString("count"); !ok || rawCount != "42" {
			t.Fatalf("expected caller Arguments.count to remain string \"42\", got %T (%v)", func() any { v, _ := origArgs.Get("count"); return v }(), ok)
		}
	})

	t.Run("validation error mentions tool name and argument details", func(t *testing.T) {
		schema := vpSchemaJSON(t, `{
			"type": "object",
			"properties": { "limit": { "type": "integer", "minimum": 1 } },
			"required": ["limit"]
		}`)
		tool := &ai.Tool{
			Name:        "fetch_items",
			Description: "Fetch",
			Parameters:  schema,
		}
		call := &ai.ToolCall{
			Type:      "toolCall",
			Id:        "call-bad",
			Name:      "fetch_items",
			Arguments: vpObjFromJSON(t, `{"limit":0}`),
		}
		_, err := ai.ValidateToolCall([]*ai.Tool{tool}, call)
		if err == nil {
			t.Fatal("expected error for limit=0")
		}
		msg := err.Error()
		if !strings.Contains(msg, `fetch_items`) || !strings.Contains(msg, `limit`) {
			t.Fatalf("expected error to mention tool name and field, got: %s", msg)
		}
	})
}
