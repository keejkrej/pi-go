// Ported from packages/ai/test/google-shared-convert-tools.test.ts (pi v1.5.2).

package api_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tgsctMakeTool(schema *jsonx.Object) *ai.Tool {
	return &ai.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Parameters:  &typebox.Schema{Object: schema},
	}
}

func tgsctFirstDecl(t *testing.T, res []*jsonx.Object) *jsonx.Object {
	t.Helper()
	if len(res) == 0 {
		t.Fatal("expected non-empty ConvertGoogleTools result")
	}
	decls, _ := res[0].GetArray("functionDeclarations")
	if len(decls) == 0 {
		t.Fatal("expected functionDeclarations[0]")
	}
	return decls[0].(*jsonx.Object)
}

func TestGoogleSharedConvertToolsMetaStrippingAndConstRewrite(t *testing.T) {
	// Empty returns nil.
	emptyRes, err := api.ConvertGoogleTools(nil, false)
	if err != nil || emptyRes != nil {
		t.Fatalf("expected (nil, nil) for empty tools, got (%v, %v)", emptyRes, err)
	}
	emptyRes2, err := api.ConvertGoogleTools([]*ai.Tool{}, true)
	if err != nil || emptyRes2 != nil {
		t.Fatalf("expected (nil, nil) for empty tools(useParameters=true), got (%v, %v)", emptyRes2, err)
	}

	// Strips JSON Schema meta keys recursively when useParameters=true without mutating input.
	orig := jsonx.ObjectOf(
		"$schema", "http://json-schema.org/draft-07/schema#",
		"$id", "urn:bash-tool",
		"$comment", "comment",
		"$defs", jsonx.ObjectOf("commandDef", jsonx.ObjectOf("type", "string")),
		"definitions", jsonx.ObjectOf("legacyDef", jsonx.ObjectOf("type", "number")),
		"type", "object",
		"properties", jsonx.ObjectOf(
			"command", jsonx.ObjectOf("type", "string"),
			"deep", jsonx.ObjectOf(
				"$schema", "http://json-schema.org/draft-07/schema#",
				"$id", "urn:nested",
				"type", "string",
			),
			"refProp", jsonx.ObjectOf("$ref", "#/$defs/someDef", "type", "string"),
		),
		"required", []any{"command"},
	)
	snapshot, _ := jsonx.Stringify(orig)

	res, err := api.ConvertGoogleTools([]*ai.Tool{tgsctMakeTool(orig)}, true)
	if err != nil {
		t.Fatalf("ConvertGoogleTools error: %v", err)
	}
	decl := tgsctFirstDecl(t, res)
	params, _ := decl.GetObject("parameters")
	if params == nil {
		t.Fatalf("expected parameters field, got %v", decl)
	}
	for _, meta := range []string{"$schema", "$id", "$comment", "$defs", "definitions"} {
		if params.Has(meta) {
			t.Fatalf("expected meta key %q stripped: %v", meta, params)
		}
	}
	props, _ := params.GetObject("properties")
	deep, _ := props.GetObject("deep")
	if deep.Has("$schema") || deep.Has("$id") {
		t.Fatalf("expected nested meta keys stripped: %v", deep)
	}
	refProp, _ := props.GetObject("refProp")
	if refVal, _ := refProp.GetString("$ref"); refVal != "#/$defs/someDef" {
		t.Fatalf("expected $ref preserved, got %v", refProp)
	}
	after, _ := jsonx.Stringify(orig)
	if after != snapshot {
		t.Fatalf("input tool schema was mutated:\nwant: %s\n got: %s", snapshot, after)
	}

	// useParameters=false preserves $schema in parametersJsonSchema and rewrites const -> enum (#11308).
	constSchema := jsonx.ObjectOf(
		"$schema", "http://json-schema.org/draft-07/schema#",
		"type", "object",
		"properties", jsonx.ObjectOf(
			"origin", jsonx.ObjectOf("type", "string", "const", "zq-src-7"),
			"mode", jsonx.ObjectOf("anyOf", []any{
				jsonx.ObjectOf("type", "string", "const", "a"),
				jsonx.ObjectOf("type", "string", "const", "b"),
			}),
			"level", jsonx.ObjectOf("oneOf", []any{
				jsonx.ObjectOf("const", 1),
				jsonx.ObjectOf("const", 2),
			}),
			"flag", jsonx.ObjectOf("type", "boolean", "const", true),
			"list", jsonx.ObjectOf("type", "array", "items", jsonx.ObjectOf("const", "x")),
			"literal", jsonx.ObjectOf("type", "object", "default", jsonx.ObjectOf("const", "keep-literal")),
			"const", jsonx.ObjectOf("type", "string"),
		),
		"required", []any{"origin"},
	)
	constSnap, _ := jsonx.Stringify(constSchema)

	resPJS, err := api.ConvertGoogleTools([]*ai.Tool{tgsctMakeTool(constSchema)}, false)
	if err != nil {
		t.Fatalf("ConvertGoogleTools(false) error: %v", err)
	}
	pjs, _ := tgsctFirstDecl(t, resPJS).GetObject("parametersJsonSchema")
	if s, _ := pjs.GetString("$schema"); s != "http://json-schema.org/draft-07/schema#" {
		t.Fatalf("expected $schema preserved in parametersJsonSchema, got %v", pjs)
	}
	pProps, _ := pjs.GetObject("properties")
	originObj, _ := pProps.GetObject("origin")
	if originObj.Has("const") {
		t.Fatalf("expected string const rewritten to enum: %v", originObj)
	}
	if enumArr, _ := originObj.GetArray("enum"); len(enumArr) != 1 || enumArr[0] != "zq-src-7" {
		t.Fatalf("expected enum=[\"zq-src-7\"], got %v", originObj)
	}
	// Literal data under "default" keeps "const".
	litObj, _ := pProps.GetObject("literal")
	litDefault, _ := litObj.GetObject("default")
	if v, _ := litDefault.GetString("const"); v != "keep-literal" {
		t.Fatalf("expected literal default.const preserved, got %v", litDefault)
	}
	// Original constSchema not mutated.
	constAfter, _ := jsonx.Stringify(constSchema)
	if constAfter != constSnap {
		t.Fatalf("input constSchema mutated:\nwant: %s\n got: %s", constSnap, constAfter)
	}

	// Strict tool sampling mode resolution.
	strictTool := tgsctMakeTool(jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject()))
	strictTool.ConstrainedSampling = ai.ToolConstrainedSamplingValue{
		V: &ai.ConstrainedSamplingJsonSchema{
			Type:   "json_schema",
			Strict: ai.ConstrainedSamplingStrictRequire,
		},
	}
	if !api.SupportsGoogleStrictToolSampling("gemini-3.1-pro-preview") {
		t.Fatal("expected gemini-3.1-pro-preview to support strict tool sampling")
	}
	if api.SupportsGoogleStrictToolSampling("gemini-2.5-pro") {
		t.Fatal("expected gemini-2.5-pro not to support strict tool sampling")
	}
	mode, err := api.ResolveGoogleFunctionCallingMode([]*ai.Tool{strictTool}, nil, true)
	if err != nil || mode != "VALIDATED" {
		t.Fatalf("expected VALIDATED, got %q, %v", mode, err)
	}
	if _, err := api.ResolveGoogleFunctionCallingMode([]*ai.Tool{strictTool}, nil, false); err == nil ||
		!strings.Contains(err.Error(), `Tool "test_tool" requires JSON-schema constrained sampling`) {
		t.Fatalf("expected strict unsupported error, got %v", err)
	}

	// Empty mode returned when toolChoice is nil and no tool enables strict sampling.
	plainTool := tgsctMakeTool(jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject()))
	emptyMode, err := api.ResolveGoogleFunctionCallingMode([]*ai.Tool{plainTool}, nil, true)
	if err != nil || emptyMode != "" {
		t.Fatalf("expected empty calling mode, got %q, %v", emptyMode, err)
	}

	// Extended meta keys ($anchor, $dynamicAnchor, $vocabulary), contentSchema const rewrite, and const+enum preservation.
	extMetaSchema := jsonx.ObjectOf(
		"type", "object",
		"$anchor", "root",
		"$dynamicAnchor", "dyn",
		"$vocabulary", jsonx.ObjectOf("https://example.com", true),
		"properties", jsonx.ObjectOf(
			"content", jsonx.ObjectOf(
				"type", "string",
				"contentSchema", jsonx.ObjectOf("const", "json_payload"),
			),
			"keptConstAndEnum", jsonx.ObjectOf(
				"type", "string",
				"const", "only_value",
				"enum", []any{"only_value", "backup"},
			),
		),
	)
	resExtOpenAPI, err := api.ConvertGoogleTools([]*ai.Tool{tgsctMakeTool(extMetaSchema)}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	extParams, _ := tgsctFirstDecl(t, resExtOpenAPI).GetObject("parameters")
	for _, k := range []string{"$anchor", "$dynamicAnchor", "$vocabulary"} {
		if extParams.Has(k) {
			t.Fatalf("expected meta key %q stripped", k)
		}
	}
	resExtJSON, err := api.ConvertGoogleTools([]*ai.Tool{tgsctMakeTool(extMetaSchema)}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	jsParams, _ := tgsctFirstDecl(t, resExtJSON).GetObject("parametersJsonSchema")
	jsProps, _ := jsParams.GetObject("properties")
	contentProp, _ := jsProps.GetObject("content")
	csObj, _ := contentProp.GetObject("contentSchema")
	if csEnum, ok := csObj.GetArray("enum"); !ok || len(csEnum) != 1 || csEnum[0] != "json_payload" || csObj.Has("const") {
		t.Fatalf("expected contentSchema const rewritten to enum, got %v", csObj)
	}
	keptProp, _ := jsProps.GetObject("keptConstAndEnum")
	if keptConst, _ := keptProp.GetString("const"); keptConst != "only_value" {
		t.Fatalf("expected const preserved alongside explicit enum, got %v", keptProp)
	}
	if keptEnum, ok := keptProp.GetArray("enum"); !ok || len(keptEnum) != 2 {
		t.Fatalf("expected existing enum preserved alongside const, got %v", keptProp)
	}
}
