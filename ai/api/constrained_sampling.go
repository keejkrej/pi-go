// Ported from packages/ai/src/api/constrained-sampling.ts (pi v1.5.2).

package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type csUnsupportedStrictJsonSchemaError struct {
	msg string
}

func (e *csUnsupportedStrictJsonSchemaError) Error() string { return e.msg }

// UnsupportedStrictSchemaKeywordCheck returns true when a provider's strict mode rejects
// this schema keyword with this value.
type UnsupportedStrictSchemaKeywordCheck func(key string, value any) bool

var csUnsupportedStrictSchemaKeys = []string{
	"$ref",
	"$defs",
	"definitions",
	"allOf",
	"oneOf",
	"patternProperties",
	"dependentSchemas",
	"dependencies",
	"unevaluatedProperties",
	"propertyNames",
	"contains",
	"prefixItems",
	"not",
	"if",
	"then",
	"else",
}

func csIsStructuredSchema(schema any) bool {
	obj, ok := schema.(*jsonx.Object)
	if !ok || obj == nil {
		return false
	}
	var types []string
	if rawType, exists := obj.Get("type"); exists {
		switch t := rawType.(type) {
		case string:
			types = []string{t}
		case []any:
			for _, elem := range t {
				if s, ok := elem.(string); ok {
					types = append(types, s)
				}
			}
		}
	}
	return slices.Contains(types, "object") ||
		slices.Contains(types, "array") ||
		obj.Has("properties") ||
		obj.Has("items")
}

func csSchemaAllowsNull(schema any) bool {
	obj, ok := schema.(*jsonx.Object)
	if !ok || obj == nil {
		return false
	}
	if rawType, exists := obj.Get("type"); exists {
		switch t := rawType.(type) {
		case string:
			if t == "null" {
				return true
			}
		case []any:
			if slices.Contains(t, any("null")) {
				return true
			}
		}
	}
	if rawConst, exists := obj.Get("const"); exists && rawConst == nil {
		return true
	}
	if rawEnum, exists := obj.Get("enum"); exists {
		if arr, ok := rawEnum.([]any); ok && slices.Contains(arr, nil) {
			return true
		}
	}
	if rawAnyOf, exists := obj.Get("anyOf"); exists {
		if arr, ok := rawAnyOf.([]any); ok {
			for _, variant := range arr {
				if csSchemaAllowsNull(variant) {
					return true
				}
			}
		}
	}
	return false
}

func csMakeJsonSchemaNodeStrict(schema any, isUnsupportedKeyword UnsupportedStrictSchemaKeywordCheck) error {
	obj, ok := schema.(*jsonx.Object)
	if !ok || obj == nil {
		return &csUnsupportedStrictJsonSchemaError{"boolean schemas are unsupported"}
	}
	for _, key := range csUnsupportedStrictSchemaKeys {
		if obj.Has(key) {
			return &csUnsupportedStrictJsonSchemaError{fmt.Sprintf("%s schemas are unsupported", key)}
		}
	}
	if isUnsupportedKeyword != nil {
		for _, key := range obj.Keys() {
			val, _ := obj.Get(key)
			if isUnsupportedKeyword(key, val) {
				s, err := jsonx.Stringify(val)
				if err != nil {
					s = "undefined"
				}
				return &csUnsupportedStrictJsonSchemaError{fmt.Sprintf("%s: %s is unsupported", key, s)}
			}
		}
	}

	// OpenAI strict mode rejects nodes without a type; a bare enum counts as typed, a bare const does not.
	if !obj.Has("type") && !obj.Has("anyOf") && !obj.Has("enum") {
		return &csUnsupportedStrictJsonSchemaError{"schemas without a type are unsupported"}
	}

	if rawAnyOf, exists := obj.Get("anyOf"); exists {
		arr, ok := rawAnyOf.([]any)
		if !ok || len(arr) == 0 {
			return &csUnsupportedStrictJsonSchemaError{"anyOf must contain at least one schema"}
		}
		for _, variant := range arr {
			if csIsStructuredSchema(variant) {
				return &csUnsupportedStrictJsonSchemaError{"object and array unions are unsupported"}
			}
			if err := csMakeJsonSchemaNodeStrict(variant, isUnsupportedKeyword); err != nil {
				return err
			}
		}
	}

	if rawItems, exists := obj.Get("items"); exists {
		if _, isArr := rawItems.([]any); isArr {
			return &csUnsupportedStrictJsonSchemaError{"tuple schemas are unsupported"}
		}
		if err := csMakeJsonSchemaNodeStrict(rawItems, isUnsupportedKeyword); err != nil {
			return err
		}
	}

	typ, _ := obj.GetString("type")
	isObjectSchema := typ == "object"
	if obj.Has("properties") && !isObjectSchema {
		return &csUnsupportedStrictJsonSchemaError{"properties require type object"}
	}
	if !isObjectSchema {
		return nil
	}
	if rawAddProps, exists := obj.Get("additionalProperties"); exists {
		b, isBool := rawAddProps.(bool)
		if !isBool || b {
			return &csUnsupportedStrictJsonSchemaError{"schema-valued or true additionalProperties is unsupported"}
		}
	}
	var properties *jsonx.Object
	if rawProps, exists := obj.Get("properties"); exists {
		propsObj, isObj := rawProps.(*jsonx.Object)
		if !isObj || propsObj == nil {
			return &csUnsupportedStrictJsonSchemaError{"object properties must be a schema map"}
		}
		properties = propsObj
	}
	requiredSet := make(map[string]struct{})
	if rawReq, exists := obj.Get("required"); exists {
		reqArr, isArr := rawReq.([]any)
		if !isArr {
			return &csUnsupportedStrictJsonSchemaError{"object required must be a string array"}
		}
		for _, item := range reqArr {
			s, isStr := item.(string)
			if !isStr {
				return &csUnsupportedStrictJsonSchemaError{"object required must be a string array"}
			}
			requiredSet[s] = struct{}{}
		}
	}

	if properties == nil {
		properties = jsonx.NewObject()
	}
	propertyNames := properties.Keys()
	for reqKey := range requiredSet {
		if !slices.Contains(propertyNames, reqKey) {
			return &csUnsupportedStrictJsonSchemaError{"required contains an unknown property"}
		}
	}
	for _, key := range propertyNames {
		prop, _ := properties.Get(key)
		if err := csMakeJsonSchemaNodeStrict(prop, isUnsupportedKeyword); err != nil {
			return err
		}
		if _, isReq := requiredSet[key]; !isReq && !csSchemaAllowsNull(prop) {
			properties.Set(key, jsonx.ObjectOf(
				"anyOf", []any{prop, jsonx.ObjectOf("type", "null")},
			))
		}
	}
	reqList := make([]any, len(propertyNames))
	for i, name := range propertyNames {
		reqList[i] = name
	}
	obj.Set("required", reqList)
	obj.Set("additionalProperties", false)
	return nil
}

// MakeStrictJsonSchema converts a tool schema to the strict subset expected by provider constrained sampling.
func MakeStrictJsonSchema(
	schema *typebox.Schema,
	isUnsupportedKeyword UnsupportedStrictSchemaKeywordCheck,
) (*jsonx.Object, error) {
	rawBytes, err := jsonx.Marshal(schema)
	if err != nil {
		return nil, err
	}
	cloned, err := jsonx.Parse(rawBytes)
	if err != nil {
		return nil, err
	}
	obj, ok := cloned.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, &csUnsupportedStrictJsonSchemaError{"root schema must have type object"}
	}
	if err := csMakeJsonSchemaNodeStrict(obj, isUnsupportedKeyword); err != nil {
		return nil, err
	}
	typ, _ := obj.GetString("type")
	if typ != "object" {
		return nil, &csUnsupportedStrictJsonSchemaError{"root schema must have type object"}
	}
	return obj, nil
}

// PrefersJsonSchemaStrictSampling returns true for a tool that asks for strict JSON-schema sampling
// only when possible (strict: "prefer").
func PrefersJsonSchemaStrictSampling(tool *ai.Tool) bool {
	if tool == nil || tool.ConstrainedSampling.V == nil {
		return false
	}
	cfg, ok := tool.ConstrainedSampling.V.(*ai.ConstrainedSamplingJsonSchema)
	return ok && cfg != nil && cfg.Strict == ai.ConstrainedSamplingStrictPrefer
}

// GetJsonSchemaToolParameters returns strict-transformed parameters when strict is true, or tool.Parameters otherwise.
func GetJsonSchemaToolParameters(tool *ai.Tool, strict *bool) (*typebox.Schema, error) {
	if strict != nil && *strict {
		strictObj, err := MakeStrictJsonSchema(tool.Parameters, nil)
		if err != nil {
			return nil, err
		}
		return typebox.Unsafe(strictObj), nil
	}
	return tool.Parameters, nil
}

// GrammarConstrainedSamplingFormat is the grammar syntax kind ("lark" or "regex").
type GrammarConstrainedSamplingFormat string

const (
	GrammarConstrainedSamplingFormatLark  GrammarConstrainedSamplingFormat = "lark"
	GrammarConstrainedSamplingFormatRegex GrammarConstrainedSamplingFormat = "regex"
)

// GrammarConstrainedSampling describes resolved grammar sampling parameters for a custom tool.
type GrammarConstrainedSampling struct {
	Format        GrammarConstrainedSamplingFormat
	Definition    string
	InputProperty string
}

// GrammarToolInputJsonBuffer tracks incremental JSON argument synthesis for grammar tools.
type GrammarToolInputJsonBuffer struct {
	Input   string
	Started bool
	Closed  bool
	chunks  []string
}

func (b *GrammarToolInputJsonBuffer) appendDelta(delta string) {
	if b.Input != "" && len(b.chunks) == 0 {
		b.chunks = append(b.chunks, b.Input)
	}
	b.chunks = append(b.chunks, delta)
}

func (b *GrammarToolInputJsonBuffer) flushInput() string {
	if len(b.chunks) == 0 {
		return b.Input
	}
	if len(b.chunks) == 1 {
		b.Input = b.chunks[0]
	} else {
		b.Input = strings.Join(b.chunks, "")
	}
	b.chunks = []string{b.Input}
	return b.Input
}

// GetGrammarToolInput extracts the string property inputProperty from arguments for toolName.
func GetGrammarToolInput(toolName string, arguments *jsonx.Object, inputProperty string) (string, error) {
	if arguments == nil {
		return "", fmt.Errorf(`Grammar tool call "%s" requires argument "%s" to be a string.`, toolName, inputProperty)
	}
	raw, exists := arguments.Get(inputProperty)
	if !exists {
		return "", fmt.Errorf(`Grammar tool call "%s" requires argument "%s" to be a string.`, toolName, inputProperty)
	}
	input, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf(`Grammar tool call "%s" requires argument "%s" to be a string.`, toolName, inputProperty)
	}
	return input, nil
}

func csGrammarToolInputJsonStart(buffer *GrammarToolInputJsonBuffer, inputProperty string) (string, error) {
	if buffer.Started {
		return "", nil
	}
	buffer.Started = true
	encodedProp, err := jsonx.Stringify(inputProperty)
	if err != nil {
		return "", err
	}
	return "{" + encodedProp + `:"`, nil
}

// AppendGrammarToolInputJsonDelta appends inputDelta to buffer and returns the matching piece of JSON
// arguments text, or nil for an empty delta. Runs in time linear in inputDelta.
func AppendGrammarToolInputJsonDelta(
	buffer *GrammarToolInputJsonBuffer,
	inputProperty string,
	inputDelta string,
) (*string, error) {
	if buffer.Closed {
		return nil, fmt.Errorf(`grammar tool input for property "%s" changed after it was closed`, inputProperty)
	}
	if len(inputDelta) == 0 {
		return nil, nil
	}
	buffer.appendDelta(inputDelta)
	prefix, err := csGrammarToolInputJsonStart(buffer, inputProperty)
	if err != nil {
		return nil, err
	}
	encoded, err := jsonx.Stringify(inputDelta)
	if err != nil {
		return nil, err
	}
	out := prefix + encoded[1:len(encoded)-1]
	return &out, nil
}

// CloseGrammarToolInputJson closes buffer and returns the remaining JSON arguments text.
// Closing again with the same input returns nil.
func CloseGrammarToolInputJson(
	buffer *GrammarToolInputJsonBuffer,
	inputProperty string,
	finalInput *string,
) (*string, error) {
	accum := buffer.flushInput()
	if buffer.Closed {
		if finalInput == nil || *finalInput == accum {
			return nil, nil
		}
		return nil, fmt.Errorf(`grammar tool input for property "%s" changed after it was closed`, inputProperty)
	}
	rest := ""
	if finalInput != nil {
		if len(*finalInput) < len(accum) || (*finalInput)[:len(accum)] != accum {
			return nil, fmt.Errorf(`grammar tool input for property "%s" changed non-monotonically`, inputProperty)
		}
		rest = (*finalInput)[len(accum):]
		buffer.Input = *finalInput
		buffer.chunks = []string{buffer.Input}
	}
	buffer.Closed = true
	prefix, err := csGrammarToolInputJsonStart(buffer, inputProperty)
	if err != nil {
		return nil, err
	}
	encoded, err := jsonx.Stringify(rest)
	if err != nil {
		return nil, err
	}
	out := prefix + encoded[1:len(encoded)-1] + `"}`
	return &out, nil
}

func csInferGrammarInputProperty(tool *ai.Tool) (string, error) {
	if tool == nil || tool.Parameters == nil || tool.Parameters.Object == nil {
		return "", errors.New("grammar constrained sampling requires an object parameter schema")
	}
	schema := tool.Parameters.Object
	if typ, _ := schema.GetString("type"); typ != "object" {
		return "", errors.New("grammar constrained sampling requires an object parameter schema")
	}
	rawReq, hasReq := schema.Get("required")
	reqArr, ok := rawReq.([]any)
	if !hasReq || !ok || len(reqArr) != 1 {
		return "", errors.New("grammar constrained sampling requires exactly one required string property")
	}
	inputProperty, isStr := reqArr[0].(string)
	if !isStr {
		return "", errors.New("grammar constrained sampling requires exactly one required string property")
	}
	props, hasProps := schema.GetObject("properties")
	if !hasProps || props == nil {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	rawEntry, hasEntry := props.Get(inputProperty)
	if !hasEntry {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	var propType string
	switch p := rawEntry.(type) {
	case *typebox.Schema:
		if p != nil && p.Object != nil {
			propType, _ = p.Object.GetString("type")
		}
	case *jsonx.Object:
		if p != nil {
			propType, _ = p.GetString("type")
		}
	}
	if propType != "string" {
		return "", fmt.Errorf("grammar constrained sampling property %s must have type string", inputProperty)
	}
	return inputProperty, nil
}

// ResolveJsonSchemaStrictSampling decides whether a JSON-schema tool is sent in strict mode.
// isUnsupportedKeyword lets a provider reject extra keywords its strict mode does not accept,
// so "prefer" tools fall back to non-strict.
func ResolveJsonSchemaStrictSampling(
	tool *ai.Tool,
	supportsStrictMode bool,
	isUnsupportedKeyword UnsupportedStrictSchemaKeywordCheck,
) (*bool, error) {
	if tool == nil || tool.ConstrainedSampling.V == nil {
		return nil, nil
	}
	cfg, ok := tool.ConstrainedSampling.V.(*ai.ConstrainedSamplingJsonSchema)
	if !ok || cfg == nil {
		return nil, nil
	}
	if supportsStrictMode {
		if _, err := MakeStrictJsonSchema(tool.Parameters, isUnsupportedKeyword); err != nil {
			var unsupportedErr *csUnsupportedStrictJsonSchemaError
			if !errors.As(err, &unsupportedErr) {
				return nil, err
			}
			if cfg.Strict != ai.ConstrainedSamplingStrictRequire {
				return nil, nil
			}
			return nil, fmt.Errorf(`Tool "%s" requires JSON-schema constrained sampling, but %s.`, tool.Name, unsupportedErr.Error())
		}
		t := true
		return &t, nil
	}
	if cfg.Strict == ai.ConstrainedSamplingStrictRequire {
		return nil, fmt.Errorf(`Tool "%s" requires JSON-schema constrained sampling, but strict tools are unsupported.`, tool.Name)
	}
	return nil, nil
}

// ResolveGrammarConstrainedSampling resolves grammar format, definition, and input property for a grammar tool.
func ResolveGrammarConstrainedSampling(
	tool *ai.Tool,
	supportsOpenAIGrammarTools bool,
) (*GrammarConstrainedSampling, error) {
	if tool == nil || tool.ConstrainedSampling.V == nil {
		return nil, nil
	}
	cfg, ok := tool.ConstrainedSampling.V.(*ai.ConstrainedSamplingGrammar)
	if !ok || cfg == nil {
		return nil, nil
	}
	if !supportsOpenAIGrammarTools {
		return nil, nil
	}
	larkDef := ""
	hasLark := false
	if cfg.Variants.OpenaiLark != nil && js.Trim(*cfg.Variants.OpenaiLark) != "" {
		larkDef = *cfg.Variants.OpenaiLark
		hasLark = true
	}
	regexDef := ""
	hasRegex := false
	if cfg.Variants.OpenaiRegex != nil && js.Trim(*cfg.Variants.OpenaiRegex) != "" {
		regexDef = *cfg.Variants.OpenaiRegex
		hasRegex = true
	}
	if !hasLark && !hasRegex {
		return nil, fmt.Errorf(`Tool "%s" cannot use grammar constrained sampling: no supported grammar variant was provided.`, tool.Name)
	}
	inputProperty, err := csInferGrammarInputProperty(tool)
	if err != nil {
		return nil, fmt.Errorf(`Tool "%s" cannot use grammar constrained sampling: %s.`, tool.Name, err.Error())
	}
	format := GrammarConstrainedSamplingFormatRegex
	definition := regexDef
	if hasLark {
		format = GrammarConstrainedSamplingFormatLark
		definition = larkDef
	}
	return &GrammarConstrainedSampling{
		Format:        format,
		Definition:    definition,
		InputProperty: inputProperty,
	}, nil
}

// CreateGrammarToolInputProperties builds a map from grammar tool name to its inferred input property.
func CreateGrammarToolInputProperties(
	tools []*ai.Tool,
	supportsOpenAIGrammarTools bool,
) (map[string]string, error) {
	properties := make(map[string]string)
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(tool, supportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			properties[tool.Name] = grammar.InputProperty
		}
	}
	return properties, nil
}
