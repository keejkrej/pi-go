// Ported from packages/ai/src/api/google-shared.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// GoogleThinkingLevel is the wire thinking_level enum used by Gemini 3+ APIs.
type GoogleThinkingLevel string

const (
	GoogleThinkingLevelUnspecified GoogleThinkingLevel = "THINKING_LEVEL_UNSPECIFIED"
	GoogleThinkingLevelMinimal     GoogleThinkingLevel = "MINIMAL"
	GoogleThinkingLevelLow         GoogleThinkingLevel = "LOW"
	GoogleThinkingLevelMedium      GoogleThinkingLevel = "MEDIUM"
	GoogleThinkingLevelHigh        GoogleThinkingLevel = "HIGH"
)

// GoogleThinkingConfig holds thinking configuration for Gemini API requests.
type GoogleThinkingConfig struct {
	IncludeThoughts *bool                `json:"includeThoughts,omitzero"`
	ThinkingBudget  *int                 `json:"thinkingBudget,omitzero"`
	ThinkingLevel   *GoogleThinkingLevel `json:"thinkingLevel,omitzero"`
}

// ToObject serializes GoogleThinkingConfig preserving explicit zero/false values.
func (c *GoogleThinkingConfig) ToObject() *jsonx.Object {
	if c == nil {
		return nil
	}
	obj := jsonx.NewObject()
	if c.IncludeThoughts != nil {
		obj.Set("includeThoughts", *c.IncludeThoughts)
	}
	if c.ThinkingBudget != nil {
		obj.Set("thinkingBudget", *c.ThinkingBudget)
	}
	if c.ThinkingLevel != nil {
		obj.Set("thinkingLevel", string(*c.ThinkingLevel))
	}
	return obj
}

var (
	gsToolCallCounter       atomic.Int64
	gsBase64SigRe           = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	gsGeminiMajorVersion    = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)
	gsThinkingGemini3Re     = regexp.MustCompile(`gemini-3(?:\.\d+)?-(?:pro|flash)`)
	gsThinkingGemini4PlusRe = regexp.MustCompile(`gemini-(?:[4-9]|[1-9]\d)`)
	gsThinkingGemma4Re      = regexp.MustCompile(`gemma-?4`)
	gsNormalizeToolCallIdRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	gsGoogleMetaKeysToCut   = map[string]bool{
		"$schema":        true,
		"$id":            true,
		"$anchor":        true,
		"$dynamicAnchor": true,
		"$vocabulary":    true,
		"$comment":       true,
		"$defs":          true,
		"definitions":    true,
	}
	gsSchemaMapKeywords = map[string]bool{
		"properties":        true,
		"patternProperties": true,
		"$defs":             true,
		"definitions":       true,
		"dependentSchemas":  true,
		"dependencies":      true,
	}
	gsSingleSchemaKeywords = map[string]bool{
		"additionalProperties":  true,
		"unevaluatedProperties": true,
		"propertyNames":         true,
		"not":                   true,
		"if":                    true,
		"then":                  true,
		"else":                  true,
		"contains":              true,
		"additionalItems":       true,
		"unevaluatedItems":      true,
		"contentSchema":         true,
	}
	gsSchemaArrayKeywords = map[string]bool{
		"allOf":       true,
		"anyOf":       true,
		"oneOf":       true,
		"prefixItems": true,
	}
)

// UsesGoogleThinkingLevel reports whether the model configures thinking via thinkingLevel
// (Gemini 3 Pro/Flash, Gemini 4+, gemini-flash-latest, gemini-flash-lite-latest, Gemma 4)
// rather than thinkingBudget.
func UsesGoogleThinkingLevel(model *ai.Model) bool {
	if model == nil {
		return false
	}
	id := strings.ToLower(model.Id)
	return gsThinkingGemini3Re.MatchString(id) ||
		gsThinkingGemini4PlusRe.MatchString(id) ||
		id == "gemini-flash-latest" ||
		id == "gemini-flash-lite-latest" ||
		gsThinkingGemma4Re.MatchString(id)
}

func gsGetGeminiMajorVersion(modelId string) (int, bool) {
	m := gsGeminiMajorVersion.FindStringSubmatch(strings.ToLower(modelId))
	if len(m) < 2 {
		return 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return major, true
}

func gsLookupThinkingLevelMap(m *ai.ThinkingLevelMap, level ai.ModelThinkingLevel) (jsonx.Opt[string], bool) {
	if m == nil {
		return jsonx.Opt[string]{}, false
	}
	var opt jsonx.Opt[string]
	switch level {
	case ai.ModelThinkingLevelOff:
		opt = m.Off
	case ai.ModelThinkingLevelMinimal:
		opt = m.Minimal
	case ai.ModelThinkingLevelLow:
		opt = m.Low
	case ai.ModelThinkingLevelMedium:
		opt = m.Medium
	case ai.ModelThinkingLevelHigh:
		opt = m.High
	case ai.ModelThinkingLevelXhigh:
		opt = m.Xhigh
	case ai.ModelThinkingLevelMax:
		opt = m.Max
	default:
		return jsonx.Opt[string]{}, false
	}
	return opt, !opt.IsZero()
}

func gsModelSupportsThinkingLevel(model *ai.Model, level ai.ModelThinkingLevel) bool {
	if model == nil || model.ThinkingLevelMap == nil {
		return true
	}
	opt, set := gsLookupThinkingLevelMap(model.ThinkingLevelMap, level)
	if !set {
		return true
	}
	return !opt.IsNull()
}

// ResolveGoogleThinkingLevel resolves a pi thinking level against model.ThinkingLevelMap
// and clamps extended levels ("xhigh", "max") into a Google-supported thinking level string.
func ResolveGoogleThinkingLevel(model *ai.Model, reasoning ai.ThinkingLevel) string {
	req := ai.ModelThinkingLevel(reasoning)
	if opt, set := gsLookupThinkingLevelMap(gsModelThinkingLevelMap(model), req); set {
		if val, ok := opt.Get(); ok {
			return val
		}
	}
	clamped := ClampReasoning(&reasoning)
	if clamped != nil {
		req = ai.ModelThinkingLevel(*clamped)
	}
	if req == ai.ModelThinkingLevelXhigh || req == ai.ModelThinkingLevelMax {
		req = ai.ModelThinkingLevelHigh
	}
	if opt, set := gsLookupThinkingLevelMap(gsModelThinkingLevelMap(model), req); set {
		if val, ok := opt.Get(); ok {
			return val
		}
	}
	if req == ai.ModelThinkingLevelMinimal && !gsModelSupportsThinkingLevel(model, ai.ModelThinkingLevelMinimal) {
		return string(ai.ThinkingLevelLow)
	}
	return string(req)
}

func gsModelThinkingLevelMap(model *ai.Model) *ai.ThinkingLevelMap {
	if model == nil {
		return nil
	}
	return model.ThinkingLevelMap
}

// ToGoogleThinkingLevel maps a resolved thinking level string (case-insensitive)
// to a GoogleThinkingLevel enum value.
func ToGoogleThinkingLevel(level string) GoogleThinkingLevel {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "THINKING_LEVEL_UNSPECIFIED", "OFF":
		return GoogleThinkingLevelUnspecified
	case "MINIMAL":
		return GoogleThinkingLevelMinimal
	case "LOW":
		return GoogleThinkingLevelLow
	case "MEDIUM":
		return GoogleThinkingLevelMedium
	case "HIGH", "XHIGH", "MAX":
		return GoogleThinkingLevelHigh
	default:
		return GoogleThinkingLevelUnspecified
	}
}

// GetDisabledGoogleThinkingConfig builds the thinkingConfig that disables or minimizes
// thinking when reasoning is off.
func GetDisabledGoogleThinkingConfig(model *ai.Model) GoogleThinkingConfig {
	includeThoughts := false
	if UsesGoogleThinkingLevel(model) {
		idLower := strings.ToLower(model.Id)
		// Gemini 3 Pro / 3.1 Pro reject MINIMAL and thinkingBudget=0; use LOW with includeThoughts=false.
		if !gsModelSupportsThinkingLevel(model, ai.ModelThinkingLevelMinimal) ||
			strings.Contains(idLower, "gemini-3-pro") ||
			strings.Contains(idLower, "gemini-3.1-pro") {
			lvl := GoogleThinkingLevelLow
			return GoogleThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingLevel:   &lvl,
			}
		}
		lvl := GoogleThinkingLevelMinimal
		return GoogleThinkingConfig{
			IncludeThoughts: &includeThoughts,
			ThinkingLevel:   &lvl,
		}
	}
	zero := 0
	return GoogleThinkingConfig{
		IncludeThoughts: &includeThoughts,
		ThinkingBudget:  &zero,
	}
}

// IsGoogleThinkingPart reports whether a Gemini response Part is a thinking part.
// Non-thinking text or functionCall parts may still carry thoughtSignature without thought=true.
func IsGoogleThinkingPart(part *jsonx.Object) bool {
	if part == nil {
		return false
	}
	v, ok := part.Get("thought")
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// RetainGoogleThoughtSignature keeps the incoming thought signature if non-empty,
// otherwise preserves the existing one across streaming deltas.
func RetainGoogleThoughtSignature(existing, incoming *string) *string {
	if incoming != nil && *incoming != "" {
		s := *incoming
		return &s
	}
	if existing != nil && *existing != "" {
		s := *existing
		return &s
	}
	return nil
}

// RequiresGoogleToolCallId reports whether tool call IDs must be included on functionCall
// and functionResponse parts for the given model ID.
func RequiresGoogleToolCallId(modelId string) bool {
	major, ok := gsGetGeminiMajorVersion(modelId)
	return strings.HasPrefix(modelId, "claude-") ||
		strings.HasPrefix(modelId, "gpt-oss-") ||
		(ok && major >= 3)
}

func gsIsValidThoughtSignature(sig *string) bool {
	if sig == nil || *sig == "" {
		return false
	}
	s := *sig
	if len(s)%4 != 0 {
		return false
	}
	return gsBase64SigRe.MatchString(s)
}

func gsResolveThoughtSignature(isSameProviderAndModel bool, sig *string) *string {
	if !isSameProviderAndModel {
		return nil
	}
	if !gsIsValidThoughtSignature(sig) {
		return nil
	}
	s := *sig
	return &s
}

func gsSupportsMultimodalFunctionResponse(modelId string) bool {
	if major, ok := gsGetGeminiMajorVersion(modelId); ok {
		return major >= 3
	}
	return true
}

func gsModelAcceptsImage(model *ai.Model) bool {
	if model == nil {
		return false
	}
	for _, mod := range model.Input {
		if mod == ai.ModelModalityImage {
			return true
		}
	}
	return false
}

// ConvertGoogleMessages converts a normalized transcript into Gemini Content objects.
func ConvertGoogleMessages(model *ai.Model, c *ai.TranscriptContext) []*jsonx.Object {
	if c == nil || model == nil {
		return nil
	}
	collapsed := ai.ResolveTranscript(c, nil)
	rawMsgs := ai.WithoutInitialSystemMessage(collapsed.Messages)
	normalizeToolCallId := func(id string, m *ai.Model, _ *ai.AssistantMessage) string {
		if m == nil || !RequiresGoogleToolCallId(m.Id) {
			return id
		}
		sanitized := gsNormalizeToolCallIdRe.ReplaceAllString(id, "_")
		if len(sanitized) > 64 {
			return sanitized[:64]
		}
		return sanitized
	}
	transformed := TransformMessages(rawMsgs, model, normalizeToolCallId)

	acceptsImages := gsModelAcceptsImage(model)
	needsToolCallId := RequiresGoogleToolCallId(model.Id)
	supportsMultimodalFR := gsSupportsMultimodalFunctionResponse(model.Id)

	var contents []*jsonx.Object

	for _, m := range transformed {
		switch msg := m.(type) {
		case *ai.UserMessage:
			parts := gsConvertUserMessageParts(msg, acceptsImages)
			if len(parts) > 0 {
				content := jsonx.NewObject()
				content.Set("role", "user")
				content.Set("parts", gsObjectsToAny(parts))
				contents = append(contents, content)
			}

		case *ai.SystemMessage:
			rendered := ai.SanitizeSurrogates(ai.RenderSystemMessageUpdate(msg))
			if rendered != "" {
				part := jsonx.NewObject()
				part.Set("text", rendered)
				content := jsonx.NewObject()
				content.Set("role", "user")
				content.Set("parts", []any{part})
				contents = append(contents, content)
			}

		case *ai.AssistantMessage:
			isSameProviderAndModel := msg.Provider == model.Provider && msg.Model == model.Id
			parts := gsConvertAssistantMessageParts(msg, model.Id, isSameProviderAndModel, needsToolCallId)
			if len(parts) > 0 {
				content := jsonx.NewObject()
				content.Set("role", "model")
				content.Set("parts", gsObjectsToAny(parts))
				contents = append(contents, content)
			}

		case *ai.ToolResultMessage:
			frPart, imageParts := gsConvertToolResultParts(msg, acceptsImages, needsToolCallId, supportsMultimodalFR)
			contents = gsAppendToolResultParts(contents, frPart, imageParts, supportsMultimodalFR)
		}
	}

	return contents
}

func gsConvertUserMessageParts(msg *ai.UserMessage, acceptsImages bool) []*jsonx.Object {
	var parts []*jsonx.Object
	if msg.Content.Text != nil {
		part := jsonx.NewObject()
		part.Set("text", ai.SanitizeSurrogates(*msg.Content.Text))
		parts = append(parts, part)
		return parts
	}
	for _, block := range msg.Content.Blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			part := jsonx.NewObject()
			part.Set("text", ai.SanitizeSurrogates(b.Text))
			parts = append(parts, part)
		case *ai.ImageContent:
			if !acceptsImages {
				continue
			}
			inlineData := jsonx.NewObject()
			inlineData.Set("mimeType", b.MimeType)
			inlineData.Set("data", b.Data)
			part := jsonx.NewObject()
			part.Set("inlineData", inlineData)
			parts = append(parts, part)
		}
	}
	return parts
}

func gsConvertAssistantMessageParts(
	msg *ai.AssistantMessage,
	modelId string,
	isSameProviderAndModel bool,
	needsToolCallId bool,
) []*jsonx.Object {
	_ = modelId
	var parts []*jsonx.Object
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *ai.TextContent:
			sig := gsResolveThoughtSignature(isSameProviderAndModel, b.TextSignature)
			if strings.TrimSpace(b.Text) == "" && sig == nil {
				continue
			}
			part := jsonx.NewObject()
			part.Set("text", ai.SanitizeSurrogates(b.Text))
			if sig != nil {
				part.Set("thoughtSignature", *sig)
			}
			parts = append(parts, part)

		case *ai.ThinkingContent:
			sig := gsResolveThoughtSignature(isSameProviderAndModel, b.ThinkingSignature)
			if strings.TrimSpace(b.Thinking) == "" && sig == nil {
				continue
			}
			if isSameProviderAndModel {
				part := jsonx.NewObject()
				part.Set("thought", true)
				part.Set("text", ai.SanitizeSurrogates(b.Thinking))
				if sig != nil {
					part.Set("thoughtSignature", *sig)
				}
				parts = append(parts, part)
			} else if strings.TrimSpace(b.Thinking) != "" {
				part := jsonx.NewObject()
				part.Set("text", ai.SanitizeSurrogates(b.Thinking))
				parts = append(parts, part)
			}

		case *ai.ToolCall:
			sig := gsResolveThoughtSignature(isSameProviderAndModel, b.ThoughtSignature)
			fc := jsonx.NewObject()
			if needsToolCallId && b.Id != "" {
				fc.Set("id", b.Id)
			}
			fc.Set("name", b.Name)
			args := b.Arguments
			if args == nil {
				args = jsonx.NewObject()
			}
			fc.Set("args", args)

			part := jsonx.NewObject()
			part.Set("functionCall", fc)
			if sig != nil {
				part.Set("thoughtSignature", *sig)
			}
			parts = append(parts, part)
		}
	}
	return parts
}

func gsConvertToolResultParts(
	msg *ai.ToolResultMessage,
	acceptsImages bool,
	needsToolCallId bool,
	supportsMultimodalFR bool,
) (*jsonx.Object, []*jsonx.Object) {
	var texts []string
	var imageParts []*jsonx.Object
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *ai.TextContent:
			texts = append(texts, b.Text)
		case *ai.ImageContent:
			if !acceptsImages {
				continue
			}
			inlineData := jsonx.NewObject()
			inlineData.Set("mimeType", b.MimeType)
			inlineData.Set("data", b.Data)
			part := jsonx.NewObject()
			part.Set("inlineData", inlineData)
			imageParts = append(imageParts, part)
		}
	}

	joinedText := strings.Join(texts, "\n")
	hasText := strings.TrimSpace(joinedText) != ""
	hasImages := len(imageParts) > 0
	responseValue := ""
	if hasText {
		responseValue = ai.SanitizeSurrogates(joinedText)
	} else if hasImages {
		responseValue = "(see attached image)"
	}

	respObj := jsonx.NewObject()
	if msg.IsError {
		respObj.Set("error", responseValue)
	} else {
		respObj.Set("output", responseValue)
	}

	fr := jsonx.NewObject()
	fr.Set("name", msg.ToolName)
	if needsToolCallId && msg.ToolCallId != "" {
		fr.Set("id", msg.ToolCallId)
	}
	fr.Set("response", respObj)
	if hasImages && supportsMultimodalFR {
		fr.Set("parts", gsObjectsToAny(imageParts))
	}

	frPart := jsonx.NewObject()
	frPart.Set("functionResponse", fr)
	return frPart, imageParts
}

func gsAppendToolResultParts(
	contents []*jsonx.Object,
	frPart *jsonx.Object,
	imageParts []*jsonx.Object,
	supportsMultimodalFR bool,
) []*jsonx.Object {
	if len(contents) > 0 && gsIsPureFunctionResponseUserContent(contents[len(contents)-1]) {
		last := contents[len(contents)-1]
		if parts, ok := last.GetArray("parts"); ok {
			last.Set("parts", append(parts, frPart))
		}
	} else {
		userTurn := jsonx.NewObject()
		userTurn.Set("role", "user")
		userTurn.Set("parts", []any{frPart})
		contents = append(contents, userTurn)
	}

	if len(imageParts) > 0 && !supportsMultimodalFR {
		textPart := jsonx.NewObject()
		textPart.Set("text", "Tool result image:")
		parts := make([]any, 0, 1+len(imageParts))
		parts = append(parts, textPart)
		for _, imgPart := range imageParts {
			parts = append(parts, imgPart)
		}
		imgTurn := jsonx.NewObject()
		imgTurn.Set("role", "user")
		imgTurn.Set("parts", parts)
		contents = append(contents, imgTurn)
	}
	return contents
}

func gsIsPureFunctionResponseUserContent(content *jsonx.Object) bool {
	if content == nil {
		return false
	}
	roleVal, _ := content.Get("role")
	if role, _ := roleVal.(string); role != "user" {
		return false
	}
	parts, ok := content.GetArray("parts")
	if !ok || len(parts) == 0 {
		return false
	}
	for _, raw := range parts {
		p, ok := raw.(*jsonx.Object)
		if !ok || p == nil || !p.Has("functionResponse") {
			return false
		}
	}
	return true
}

// ConvertGoogleTools converts pi Tool definitions into Gemini FunctionDeclaration tool wrappers.
// When useParameters is true, emits "parameters" with JSON Schema meta keys stripped recursively.
// When useParameters is false, emits "parametersJsonSchema" with string/number const rewritten to single-value enum.
func ConvertGoogleTools(tools []*ai.Tool, useParameters bool, supportsStrict ...bool) ([]*jsonx.Object, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	supportsStrictMode := true
	if len(supportsStrict) > 0 {
		supportsStrictMode = supportsStrict[0]
	}
	decls := make([]*jsonx.Object, 0, len(tools))
	for _, tool := range tools {
		strict, err := ResolveJsonSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		paramsSchema, err := GetJsonSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		decl := jsonx.NewObject()
		decl.Set("name", tool.Name)
		decl.Set("description", tool.Description)
		var rawParams *jsonx.Object
		if paramsSchema != nil && paramsSchema.Object != nil {
			rawParams = paramsSchema.Object
		} else if paramsSchema != nil {
			if encoded, mErr := jsonx.Marshal(paramsSchema); mErr == nil {
				if parsed, pErr := jsonx.Parse(encoded); pErr == nil {
					if obj, ok := parsed.(*jsonx.Object); ok {
						rawParams = obj
					}
				}
			}
		}
		if rawParams != nil {
			if useParameters {
				decl.Set("parameters", gsStripSchemaMetaKeys(rawParams))
			} else {
				decl.Set("parametersJsonSchema", gsRewriteConstToEnumSchema(rawParams))
			}
		}
		decls = append(decls, decl)
	}
	wrapper := jsonx.NewObject()
	wrapper.Set("functionDeclarations", gsObjectsToAny(decls))
	return []*jsonx.Object{wrapper}, nil
}

func gsStripSchemaMetaKeys(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			if gsGoogleMetaKeysToCut[k] {
				continue
			}
			val, _ := x.Get(k)
			out.Set(k, gsStripSchemaMetaKeys(val))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = gsStripSchemaMetaKeys(item)
		}
		return out
	default:
		return v
	}
}

func gsCloneJSONValue(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			out.Set(k, gsCloneJSONValue(val))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = gsCloneJSONValue(item)
		}
		return out
	default:
		return v
	}
}

func gsIsStringOrFiniteNumberConst(v any) bool {
	switch x := v.(type) {
	case string:
		return true
	case int, int64, int32, uint, uint64:
		return true
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		f := float64(x)
		return !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return false
	}
}

func gsRewriteConstToEnumSchema(node any) any {
	obj, ok := node.(*jsonx.Object)
	if !ok || obj == nil {
		return gsCloneJSONValue(node)
	}
	out := jsonx.NewObject()
	hasConst := false
	var constVal any
	if cv, exists := obj.Get("const"); exists && !obj.Has("enum") && gsIsStringOrFiniteNumberConst(cv) {
		hasConst = true
		constVal = cv
	}

	for _, k := range obj.Keys() {
		val, _ := obj.Get(k)
		if k == "const" && hasConst {
			out.Set("enum", []any{gsCloneJSONValue(constVal)})
			continue
		}
		if gsSchemaMapKeywords[k] {
			if mObj, isObj := val.(*jsonx.Object); isObj && mObj != nil {
				rewrittenMap := jsonx.NewObject()
				for _, entryKey := range mObj.Keys() {
					entryVal, _ := mObj.Get(entryKey)
					if entryObj, isEntryObj := entryVal.(*jsonx.Object); isEntryObj && entryObj != nil {
						rewrittenMap.Set(entryKey, gsRewriteConstToEnumSchema(entryObj))
					} else {
						rewrittenMap.Set(entryKey, gsCloneJSONValue(entryVal))
					}
				}
				out.Set(k, rewrittenMap)
				continue
			}
			out.Set(k, gsCloneJSONValue(val))
			continue
		}
		if gsSingleSchemaKeywords[k] {
			if subObj, isObj := val.(*jsonx.Object); isObj && subObj != nil {
				out.Set(k, gsRewriteConstToEnumSchema(subObj))
			} else {
				out.Set(k, gsCloneJSONValue(val))
			}
			continue
		}
		if k == "items" {
			switch items := val.(type) {
			case *jsonx.Object:
				out.Set(k, gsRewriteConstToEnumSchema(items))
			case []any:
				arr := make([]any, len(items))
				for i, item := range items {
					arr[i] = gsRewriteConstToEnumSchema(item)
				}
				out.Set(k, arr)
			default:
				out.Set(k, gsCloneJSONValue(val))
			}
			continue
		}
		if gsSchemaArrayKeywords[k] {
			if items, isArr := val.([]any); isArr {
				arr := make([]any, len(items))
				for i, item := range items {
					arr[i] = gsRewriteConstToEnumSchema(item)
				}
				out.Set(k, arr)
				continue
			}
		}
		out.Set(k, gsCloneJSONValue(val))
	}
	return out
}

// SupportsGoogleStrictToolSampling reports whether the Gemini model supports VALIDATED strict tool sampling.
func SupportsGoogleStrictToolSampling(modelId string) bool {
	major, ok := gsGetGeminiMajorVersion(modelId)
	return ok && major >= 3
}

// MapGoogleToolChoice maps a pi tool choice string to Gemini FunctionCallingConfigMode.
func MapGoogleToolChoice(choice string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "none":
		return "NONE"
	case "required", "any":
		return "ANY"
	case "validated":
		return "VALIDATED"
	default:
		return "AUTO"
	}
}

// ResolveGoogleFunctionCallingMode validates constrainedSampling requirements across tools
// and returns the effective Gemini FunctionCallingConfig.mode ("AUTO", "NONE", "ANY", "VALIDATED", or "").
func ResolveGoogleFunctionCallingMode(tools []*ai.Tool, toolChoice *ai.ToolChoice, supportsStrict bool) (string, error) {
	anyStrict := false
	for _, tool := range tools {
		strict, err := ResolveJsonSchemaStrictSampling(tool, supportsStrict, nil)
		if err != nil {
			return "", err
		}
		if strict != nil && *strict {
			anyStrict = true
		}
	}
	if toolChoice != nil && (strings.EqualFold(string(*toolChoice), "none") || strings.EqualFold(string(*toolChoice), "any")) {
		return MapGoogleToolChoice(string(*toolChoice)), nil
	}
	choiceStr := ""
	if toolChoice != nil {
		choiceStr = string(*toolChoice)
	}
	if anyStrict {
		return "VALIDATED", nil
	}
	if choiceStr != "" {
		return MapGoogleToolChoice(choiceStr), nil
	}
	return "", nil
}

// MapGoogleStopReason maps a Gemini finishReason string to a pi StopReason.
func MapGoogleStopReason(reason string) ai.StopReason {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "STOP":
		return ai.StopReasonStop
	case "MAX_TOKENS", "CONTINUATION":
		return ai.StopReasonLength
	default:
		return ai.StopReasonError
	}
}

// GsNextToolCallId generates a unique tool call ID matching pi's counter format.
func GsNextToolCallId(toolName string) string {
	n := gsToolCallCounter.Add(1)
	return fmt.Sprintf("%s_%d_%d", toolName, js.DateNow(), n)
}

// GsGoogleRetryConfig configures Google API retry behavior.
type GsGoogleRetryConfig struct {
	MaxRetries      int
	MaxRetryDelayMs *int64
}

type gsHTTPStatusError struct {
	Status  int
	Headers http.Header
	Message string
	Cause   error
}

func (e *gsHTTPStatusError) Error() string                { return e.Message }
func (e *gsHTTPStatusError) Unwrap() error                { return e.Cause }
func (e *gsHTTPStatusError) ProviderStatus() *int         { s := e.Status; return &s }
func (e *gsHTTPStatusError) ProviderHeaders() http.Header { return e.Headers }

type gsHTTPStatusCoder interface {
	StatusCode() int
}

type gsHTTPStatusGetter interface {
	Status() int
}

// RetryGoogleRequest delegates request retries to ai.RetryProviderRequest using HTTP status
// and x-should-retry headers (default maxRetries=0, 0.5*2^i backoff capped at 8s).
func RetryGoogleRequest[T any](
	ctx context.Context,
	request func(ctx context.Context) (T, error),
	cfg *GsGoogleRetryConfig,
) (T, error) {
	var retryOpts ai.ProviderRetryOptions
	if cfg != nil {
		mr := cfg.MaxRetries
		retryOpts.MaxRetries = &mr
		retryOpts.MaxRetryDelayMs = cfg.MaxRetryDelayMs
	}
	return ai.RetryProviderRequest(ctx, func(attemptCtx context.Context) (T, error) {
		res, err := request(attemptCtx)
		if err == nil {
			return res, nil
		}
		var pe ai.ProviderError
		if errors.As(err, &pe) && pe != nil {
			return res, err
		}
		// Normalize headers-less SDK-like status errors to ai.ProviderRetryError, matching TS google-shared.ts.
		var sc gsHTTPStatusCoder
		if errors.As(err, &sc) && sc != nil {
			s := sc.StatusCode()
			return res, &ai.ProviderRetryError{Message: err.Error(), Status: &s, Cause: err}
		}
		var sg gsHTTPStatusGetter
		if errors.As(err, &sg) && sg != nil {
			s := sg.Status()
			return res, &ai.ProviderRetryError{Message: err.Error(), Status: &s, Cause: err}
		}
		return res, err
	}, &retryOpts)
}

// gsObjectsToAny converts a typed []*jsonx.Object slice into []any so jsonx.Object.GetArray callers succeed.
func gsObjectsToAny(objs []*jsonx.Object) []any {
	out := make([]any, len(objs))
	for i, obj := range objs {
		out[i] = obj
	}
	return out
}
