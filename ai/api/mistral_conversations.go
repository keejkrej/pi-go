// Ported from packages/ai/src/api/mistral-conversations.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/sse"
)

const (
	mcToolCallIdLength      = 9
	mcMaxMistralErrBodyChar = 4000
	mcDefaultHeaderWaitMs   = int64(60_000)
)

var mcNonAlnumPattern = regexp.MustCompile(`[^a-zA-Z0-9]`)

// MistralReasoningEffort is the reasoning_effort setting accepted by Mistral reasoning models.
type MistralReasoningEffort string

const (
	MistralReasoningEffortNone   MistralReasoningEffort = "none"
	MistralReasoningEffortLow    MistralReasoningEffort = "low"
	MistralReasoningEffortMedium MistralReasoningEffort = "medium"
	MistralReasoningEffortHigh   MistralReasoningEffort = "high"
	MistralReasoningEffortMax    MistralReasoningEffort = "max"
)

// MistralOptions configures streaming from the Mistral Chat Completions endpoint.
type MistralOptions struct {
	ai.StreamOptions
	ToolChoice      any                     `json:"toolChoice,omitzero"`
	PromptMode      *string                 `json:"promptMode,omitzero"`
	ReasoningEffort *MistralReasoningEffort `json:"reasoningEffort,omitzero"`
}

type mcHttpError struct {
	statusCode int
	body       string
	headers    http.Header
	message    string
}

func (e *mcHttpError) Error() string                { return e.message }
func (e *mcHttpError) Name() string                 { return "MistralHttpError" }
func (e *mcHttpError) ProviderStatus() *int         { s := e.statusCode; return &s }
func (e *mcHttpError) ProviderHeaders() http.Header { return e.headers }
func (e *mcHttpError) ErrorBody() string            { return e.body }

func mcSupportsMidConvoSystemMessages(model *ai.Model) *bool {
	if model == nil || model.Compat.V == nil {
		return nil
	}
	switch c := model.Compat.V.(type) {
	case *ai.MistralConversationsCompat:
		if c != nil {
			return c.SupportsMidConvoSystemMessages
		}
	case *ai.ProviderCompat:
		if c != nil {
			return c.SupportsMidConvoSystemMessages
		}
	}
	return nil
}

func mcUnwrapOptions(options ai.ProviderStreamOptions) *MistralOptions {
	if options == nil {
		return &MistralOptions{}
	}
	if mOpts, ok := options.(*MistralOptions); ok && mOpts != nil {
		return mOpts
	}
	base := options.BaseStreamOptions()
	if base != nil {
		return &MistralOptions{StreamOptions: *base}
	}
	return &MistralOptions{}
}

func mcCreateOutput(model *ai.Model) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        ai.Api(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		Usage:      ai.Usage{},
		StopReason: ai.StopReasonPending,
		Timestamp:  js.DateNow(),
	}
}

func mcDeriveToolCallId(id string, attempt int) string {
	normalized := mcNonAlnumPattern.ReplaceAllString(id, "")
	if attempt == 0 && len(normalized) == mcToolCallIdLength {
		return normalized
	}
	seedBase := normalized
	if seedBase == "" {
		seedBase = id
	}
	seed := seedBase
	if attempt != 0 {
		seed = fmt.Sprintf("%s:%d", seedBase, attempt)
	}
	hashed := mcNonAlnumPattern.ReplaceAllString(ai.ShortHash(seed), "")
	if len(hashed) > mcToolCallIdLength {
		return hashed[:mcToolCallIdLength]
	}
	return hashed
}

func mcNewToolCallIdNormalizer() func(id string, model *ai.Model, source *ai.AssistantMessage) string {
	idMap := make(map[string]string)
	reverseMap := make(map[string]string)

	return func(id string, _ *ai.Model, _ *ai.AssistantMessage) string {
		if existing, ok := idMap[id]; ok {
			return existing
		}
		for attempt := 0; ; attempt++ {
			candidate := mcDeriveToolCallId(id, attempt)
			owner, taken := reverseMap[candidate]
			if !taken || owner == id {
				idMap[id] = candidate
				reverseMap[candidate] = id
				return candidate
			}
		}
	}
}

func mcFormatError(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *mcHttpError
	if errors.As(err, &httpErr) && httpErr != nil {
		trimmed := js.Trim(httpErr.body)
		if trimmed != "" {
			return fmt.Sprintf("Mistral API error (%d): %s", httpErr.statusCode, ai.TruncateErrorText(trimmed, mcMaxMistralErrBodyChar))
		}
		return fmt.Sprintf("Mistral API error (%d): %s", httpErr.statusCode, httpErr.Error())
	}
	return err.Error()
}

func mcShouldUsePromptCaching(opts *MistralOptions) bool {
	if opts == nil || opts.SessionId == nil || *opts.SessionId == "" {
		return false
	}
	if opts.CacheRetention != nil && *opts.CacheRetention == ai.CacheRetentionNone {
		return false
	}
	return true
}

func mcApplyHeaderOverrides(h http.Header, overrides *omap.Map[string, string]) {
	if overrides == nil {
		return
	}
	for k, v := range overrides.All() {
		h.Set(k, v)
	}
}

func mcApplyProviderHeaderOverrides(h http.Header, overrides ai.ProviderHeaders) {
	if overrides == nil {
		return
	}
	for name, val := range overrides.All() {
		if val == nil {
			h.Del(name)
		} else {
			h.Set(name, *val)
		}
	}
}

func mcHasHeaderOverride(overrides any, target string) bool {
	switch m := overrides.(type) {
	case *omap.Map[string, string]:
		if m == nil {
			return false
		}
		for k := range m.All() {
			if strings.EqualFold(k, target) {
				return true
			}
		}
	case ai.ProviderHeaders:
		if m == nil {
			return false
		}
		for k := range m.All() {
			if strings.EqualFold(k, target) {
				return true
			}
		}
	}
	return false
}

func mcBuildHeaders(model *ai.Model, apiKey string, opts *MistralOptions) http.Header {
	headers := make(http.Header)
	idHeaders := ai.GetIdentityHeaders(ai.ClientIdentityTarget{
		Provider: string(model.Provider),
		BaseUrl:  model.BaseUrl,
	}, opts.Identity)
	mcApplyHeaderOverrides(headers, idHeaders)
	headers.Set("accept", "text/event-stream")
	headers.Set("authorization", "Bearer "+apiKey)
	headers.Set("content-type", "application/json")
	mcApplyHeaderOverrides(headers, model.Headers)
	mcApplyProviderHeaderOverrides(headers, opts.Headers)

	hasExplicitAffinity := mcHasHeaderOverride(model.Headers, "x-affinity") || mcHasHeaderOverride(opts.Headers, "x-affinity")
	if mcShouldUsePromptCaching(opts) && !hasExplicitAffinity {
		headers.Set("x-affinity", *opts.SessionId)
	}
	return headers
}

func mcRemapProperty(obj *jsonx.Object, source, target string) {
	if obj == nil || !obj.Has(source) {
		return
	}
	val, _ := obj.Get(source)
	obj.Delete(source)
	obj.Set(target, val)
}

func mcToWireContentChunk(chunk any) any {
	obj, ok := chunk.(*jsonx.Object)
	if !ok || obj == nil {
		return chunk
	}
	wire := jsonx.CloneValue(obj).(*jsonx.Object)
	for _, pair := range [][2]string{
		{"imageUrl", "image_url"},
		{"documentUrl", "document_url"},
		{"documentName", "document_name"},
		{"fileId", "file_id"},
		{"referenceIds", "reference_ids"},
		{"inputAudio", "input_audio"},
	} {
		mcRemapProperty(wire, pair[0], pair[1])
	}
	return wire
}

func mcToWireMessage(msg any) any {
	obj, ok := msg.(*jsonx.Object)
	if !ok || obj == nil {
		return msg
	}
	wire := jsonx.CloneValue(obj).(*jsonx.Object)
	mcRemapProperty(wire, "toolCalls", "tool_calls")
	mcRemapProperty(wire, "toolCallId", "tool_call_id")
	if rawContent, ok := wire.Get("content"); ok {
		if arr, ok := rawContent.([]any); ok {
			outArr := make([]any, len(arr))
			for i, chunk := range arr {
				outArr[i] = mcToWireContentChunk(chunk)
			}
			wire.Set("content", outArr)
		}
	}
	return wire
}

func mcToWirePayload(payload any) any {
	obj, ok := payload.(*jsonx.Object)
	if !ok || obj == nil {
		return payload
	}
	wire := jsonx.CloneValue(obj).(*jsonx.Object)
	for _, pair := range [][2]string{
		{"topP", "top_p"},
		{"maxTokens", "max_tokens"},
		{"randomSeed", "random_seed"},
		{"responseFormat", "response_format"},
		{"toolChoice", "tool_choice"},
		{"presencePenalty", "presence_penalty"},
		{"frequencyPenalty", "frequency_penalty"},
		{"parallelToolCalls", "parallel_tool_calls"},
		{"reasoningEffort", "reasoning_effort"},
		{"promptMode", "prompt_mode"},
		{"promptCacheKey", "prompt_cache_key"},
		{"safePrompt", "safe_prompt"},
	} {
		mcRemapProperty(wire, pair[0], pair[1])
	}
	if rawMessages, ok := wire.Get("messages"); ok {
		if arr, ok := rawMessages.([]any); ok {
			outMsgs := make([]any, len(arr))
			for i, m := range arr {
				outMsgs[i] = mcToWireMessage(m)
			}
			wire.Set("messages", outMsgs)
		}
	}
	if rawRespFmt, ok := wire.Get("response_format"); ok {
		if rfObj, ok := rawRespFmt.(*jsonx.Object); ok && rfObj != nil {
			wireRF := jsonx.CloneValue(rfObj).(*jsonx.Object)
			mcRemapProperty(wireRF, "jsonSchema", "json_schema")
			if rawJS, ok := wireRF.Get("json_schema"); ok {
				if jsObj, ok := rawJS.(*jsonx.Object); ok && jsObj != nil {
					wireJS := jsonx.CloneValue(jsObj).(*jsonx.Object)
					mcRemapProperty(wireJS, "schemaDefinition", "schema")
					wireRF.Set("json_schema", wireJS)
				}
			}
			wire.Set("response_format", wireRF)
		}
	}
	return wire
}

func mcToFunctionTools(tools []*ai.Tool) ([]any, error) {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		strict, err := ResolveJsonSchemaStrictSampling(tool, true, nil)
		if err != nil {
			return nil, err
		}
		params, err := GetJsonSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		encodedParams, err := jsonx.Encode(params)
		if err != nil {
			return nil, err
		}
		if encodedParams == nil {
			encodedParams = jsonx.NewObject()
		}
		strictVal := false
		if strict != nil {
			strictVal = *strict
		}
		out = append(out, jsonx.ObjectOf(
			"type", "function",
			"function", jsonx.ObjectOf(
				"name", tool.Name,
				"description", tool.Description,
				"parameters", encodedParams,
				"strict", strictVal,
			),
		))
	}
	return out, nil
}

func mcBuildToolResultText(text string, hasImages, supportsImages, isError bool) string {
	trimmed := js.Trim(text)
	errorPrefix := ""
	if isError {
		errorPrefix = "[tool error] "
	}
	if trimmed != "" {
		imageSuffix := ""
		if hasImages && !supportsImages {
			imageSuffix = "\n[tool image omitted: model does not support images]"
		}
		return errorPrefix + trimmed + imageSuffix
	}
	if hasImages {
		if supportsImages {
			if isError {
				return "[tool error] (see attached image)"
			}
			return "(see attached image)"
		}
		if isError {
			return "[tool error] (image omitted: model does not support images)"
		}
		return "(image omitted: model does not support images)"
	}
	if isError {
		return "[tool error] (no tool output)"
	}
	return "(no tool output)"
}

func mcToChatMessages(messages []ai.Message, supportsImages bool) []any {
	result := make([]any, 0, len(messages))
	for idx, msg := range messages {
		switch m := msg.(type) {
		case *ai.SystemMessage:
			var text string
			if idx == 0 {
				text = ai.GetSystemMessageText(m)
			} else {
				text = ai.RenderSystemMessageUpdate(m)
			}
			if len(text) > 0 {
				result = append(result, jsonx.ObjectOf(
					"role", "system",
					"content", ai.SanitizeSurrogates(text),
				))
			}

		case *ai.UserMessage:
			if m.Content.Text != nil {
				result = append(result, jsonx.ObjectOf(
					"role", "user",
					"content", ai.SanitizeSurrogates(*m.Content.Text),
				))
				continue
			}
			hadImages := false
			var parts []any
			for _, item := range m.Content.Blocks {
				switch blk := item.(type) {
				case *ai.TextContent:
					parts = append(parts, jsonx.ObjectOf(
						"type", "text",
						"text", ai.SanitizeSurrogates(blk.Text),
					))
				case *ai.ImageContent:
					hadImages = true
					if supportsImages {
						parts = append(parts, jsonx.ObjectOf(
							"type", "image_url",
							"imageUrl", fmt.Sprintf("data:%s;base64,%s", blk.MimeType, blk.Data),
						))
					}
				}
			}
			if len(parts) > 0 {
				result = append(result, jsonx.ObjectOf("role", "user", "content", parts))
			} else if hadImages && !supportsImages {
				result = append(result, jsonx.ObjectOf(
					"role", "user",
					"content", "(image omitted: model does not support images)",
				))
			}

		case *ai.AssistantMessage:
			var contentParts []any
			var toolCalls []any
			for _, block := range m.Content {
				switch b := block.(type) {
				case *ai.TextContent:
					if js.Trim(b.Text) != "" {
						contentParts = append(contentParts, jsonx.ObjectOf(
							"type", "text",
							"text", ai.SanitizeSurrogates(b.Text),
						))
					}
				case *ai.ThinkingContent:
					if js.Trim(b.Thinking) != "" {
						contentParts = append(contentParts, jsonx.ObjectOf(
							"type", "thinking",
							"thinking", []any{
								jsonx.ObjectOf("type", "text", "text", ai.SanitizeSurrogates(b.Thinking)),
							},
						))
					}
				case *ai.ToolCall:
					var args any = b.Arguments
					if b.Arguments == nil {
						args = jsonx.NewObject()
					}
					argsStr, _ := jsonx.Stringify(args)
					toolCalls = append(toolCalls, jsonx.ObjectOf(
						"id", b.Id,
						"type", "function",
						"function", jsonx.ObjectOf("name", b.Name, "arguments", argsStr),
						"index", 0,
					))
				}
			}
			if len(contentParts) > 0 || len(toolCalls) > 0 {
				msgObj := jsonx.ObjectOf("role", "assistant", "prefix", false)
				if len(contentParts) > 0 {
					msgObj.Set("content", contentParts)
				}
				if len(toolCalls) > 0 {
					msgObj.Set("toolCalls", toolCalls)
				}
				result = append(result, msgObj)
			}

		case *ai.ToolResultMessage:
			var textParts []string
			hasImages := false
			for _, part := range m.Content {
				switch p := part.(type) {
				case *ai.TextContent:
					textParts = append(textParts, ai.SanitizeSurrogates(p.Text))
				case *ai.ImageContent:
					hasImages = true
				}
			}
			toolText := mcBuildToolResultText(strings.Join(textParts, "\n"), hasImages, supportsImages, m.IsError)
			toolContent := []any{jsonx.ObjectOf("type", "text", "text", toolText)}
			if supportsImages {
				for _, part := range m.Content {
					if img, ok := part.(*ai.ImageContent); ok && img != nil {
						toolContent = append(toolContent, jsonx.ObjectOf(
							"type", "image_url",
							"imageUrl", fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data),
						))
					}
				}
			}
			result = append(result, jsonx.ObjectOf(
				"role", "tool",
				"toolCallId", m.ToolCallId,
				"name", m.ToolName,
				"content", toolContent,
			))
		}
	}
	return result
}

func mcBuildChatPayload(
	model *ai.Model,
	transcriptCtx *ai.TranscriptContext,
	messages []ai.Message,
	opts *MistralOptions,
) (*jsonx.Object, error) {
	supportsImages := slices.Contains(model.Input, ai.ModelModalityImage)
	payload := jsonx.ObjectOf(
		"model", model.Id,
		"stream", true,
		"messages", mcToChatMessages(messages, supportsImages),
	)
	currentTools := ai.GetCurrentTools(transcriptCtx.Messages)
	if len(currentTools) > 0 {
		fnTools, err := mcToFunctionTools(currentTools)
		if err != nil {
			return nil, err
		}
		payload.Set("tools", fnTools)
	}
	if opts != nil {
		if opts.Temperature != nil {
			payload.Set("temperature", *opts.Temperature)
		}
		if opts.MaxTokens != nil {
			payload.Set("maxTokens", *opts.MaxTokens)
		}
		if opts.ToolChoice != nil {
			payload.Set("toolChoice", opts.ToolChoice)
		}
		if opts.PromptMode != nil && *opts.PromptMode != "" {
			payload.Set("promptMode", *opts.PromptMode)
		}
		if opts.ReasoningEffort != nil && *opts.ReasoningEffort != "" {
			payload.Set("reasoningEffort", string(*opts.ReasoningEffort))
		}
		if mcShouldUsePromptCaching(opts) {
			payload.Set("promptCacheKey", *opts.SessionId)
		}
	}
	return payload, nil
}

func mcGetCachedPromptTokens(usage *jsonx.Object, promptTokens int) int {
	if usage == nil {
		return 0
	}
	extractNested := func(parentKey, childKey string) (float64, bool) {
		rawParent, ok := usage.Get(parentKey)
		if !ok {
			return 0, false
		}
		pObj, ok := rawParent.(*jsonx.Object)
		if !ok || pObj == nil {
			return 0, false
		}
		rawVal, ok := pObj.Get(childKey)
		if !ok {
			return 0, false
		}
		return clshToFloat64(rawVal)
	}

	var cached float64
	if v, ok := extractNested("promptTokensDetails", "cachedTokens"); ok {
		cached = v
	} else if v, ok := extractNested("prompt_tokens_details", "cached_tokens"); ok {
		cached = v
	} else if v, ok := extractNested("promptTokenDetails", "cachedTokens"); ok {
		cached = v
	} else if v, ok := extractNested("prompt_token_details", "cached_tokens"); ok {
		cached = v
	} else if rawVal, ok := usage.Get("numCachedTokens"); ok {
		if v, ok := clshToFloat64(rawVal); ok {
			cached = v
		}
	} else if rawVal, ok := usage.Get("num_cached_tokens"); ok {
		if v, ok := clshToFloat64(rawVal); ok {
			cached = v
		}
	}

	n := int(cached)
	if n < 0 {
		n = 0
	}
	if n > promptTokens {
		n = promptTokens
	}
	return n
}

func mcMapChatStopReason(reason string) (ai.StopReason, string) {
	switch reason {
	case "", "stop":
		return ai.StopReasonStop, ""
	case "length", "model_length":
		return ai.StopReasonLength, ""
	case "tool_calls":
		return ai.StopReasonToolUse, ""
	case "error":
		// Mistral reports transient server failures this way; "server error" makes the message retryable.
		return ai.StopReasonError, "Provider stopped with: error (server error)"
	default:
		return ai.StopReasonError, "Provider stopped with: " + reason
	}
}

func mcConsumeChatStream(
	model *ai.Model,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
	body io.Reader,
	onProviderStreamEvent func(data any, model *ai.Model) error,
) error {
	var currentBlock ai.Content
	blockIndex := func() int { return len(output.Content) - 1 }

	type openToolCall struct {
		contentIndex int
		arguments    *ai.ToolCallArgumentsStream
	}
	toolCallsByKey := make(map[string]*openToolCall)
	var toolCallOrder []string

	finishCurrentBlock := func(block ai.Content) {
		if block == nil {
			return
		}
		switch b := block.(type) {
		case *ai.TextContent:
			stream.Push(&ai.AssistantMessageEventTextEnd{
				Type:         "text_end",
				ContentIndex: blockIndex(),
				Content:      b.Text,
				Partial:      output,
			})
		case *ai.ThinkingContent:
			stream.Push(&ai.AssistantMessageEventThinkingEnd{
				Type:         "thinking_end",
				ContentIndex: blockIndex(),
				Content:      b.Thinking,
				Partial:      output,
			})
		}
	}

	appendTextDelta := func(deltaText string) {
		if deltaText == "" {
			return
		}
		tb, isText := currentBlock.(*ai.TextContent)
		if !isText || tb == nil {
			finishCurrentBlock(currentBlock)
			tb = &ai.TextContent{Type: "text", Text: ""}
			currentBlock = tb
			output.Content = append(output.Content, tb)
			stream.Push(&ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: blockIndex(),
				Partial:      output,
			})
		}
		tb.Text += deltaText
		stream.Push(&ai.AssistantMessageEventTextDelta{
			Type:         "text_delta",
			ContentIndex: blockIndex(),
			Delta:        deltaText,
			Partial:      output,
		})
	}

	appendThinkingDelta := func(deltaText string) {
		if deltaText == "" {
			return
		}
		tb, isThinking := currentBlock.(*ai.ThinkingContent)
		if !isThinking || tb == nil {
			finishCurrentBlock(currentBlock)
			tb = &ai.ThinkingContent{Type: "thinking", Thinking: ""}
			currentBlock = tb
			output.Content = append(output.Content, tb)
			stream.Push(&ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: blockIndex(),
				Partial:      output,
			})
		}
		tb.Thinking += deltaText
		stream.Push(&ai.AssistantMessageEventThinkingDelta{
			Type:         "thinking_delta",
			ContentIndex: blockIndex(),
			Delta:        deltaText,
			Partial:      output,
		})
	}

	sseReader := sse.NewReader(body)
	for {
		ev, err := sseReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		data := js.Trim(ev.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		parsed, err := jsonx.Parse([]byte(data))
		if err != nil {
			return err
		}
		chunkObj, ok := parsed.(*jsonx.Object)
		if !ok || chunkObj == nil {
			return errors.New("Invalid Mistral streaming event")
		}
		rawChoices, hasChoices := chunkObj.Get("choices")
		choices, okChoices := rawChoices.([]any)
		if !hasChoices || !okChoices {
			return errors.New("Invalid Mistral streaming event")
		}

		if onProviderStreamEvent != nil {
			if err := onProviderStreamEvent(chunkObj, model); err != nil {
				return err
			}
		}

		if (output.ResponseId == nil || *output.ResponseId == "") && chunkObj.Has("id") {
			if id, ok := chunkObj.GetString("id"); ok && id != "" {
				output.ResponseId = &id
			}
		}

		if rawUsage, hasUsage := chunkObj.Get("usage"); hasUsage {
			if usageObj, ok := rawUsage.(*jsonx.Object); ok && usageObj != nil {
				promptTokens := 0
				if rawPrompt, ok := usageObj.Get("prompt_tokens"); ok {
					if v, ok := clshToFloat64(rawPrompt); ok {
						promptTokens = int(v)
					}
				}
				cached := mcGetCachedPromptTokens(usageObj, promptTokens)
				completionTokens := 0
				if rawComp, ok := usageObj.Get("completion_tokens"); ok {
					if v, ok := clshToFloat64(rawComp); ok {
						completionTokens = int(v)
					}
				}
				totalTokens := 0
				if rawTot, ok := usageObj.Get("total_tokens"); ok {
					if v, ok := clshToFloat64(rawTot); ok {
						totalTokens = int(v)
					}
				}
				output.Usage.Input = max(0, promptTokens-cached)
				output.Usage.Output = completionTokens
				output.Usage.CacheRead = cached
				output.Usage.CacheWrite = 0
				if totalTokens > 0 {
					output.Usage.TotalTokens = totalTokens
				} else {
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
				}
				ai.CalculateCost(model, &output.Usage)
			}
		}

		if len(choices) == 0 {
			continue
		}
		choiceObj, ok := choices[0].(*jsonx.Object)
		if !ok || choiceObj == nil {
			continue
		}
		if rawFinish, hasFinish := choiceObj.Get("finish_reason"); hasFinish {
			if finishReason, ok := rawFinish.(string); ok && finishReason != "" {
				output.RawStopReason = &finishReason
				sr, errMsg := mcMapChatStopReason(finishReason)
				output.StopReason = sr
				if errMsg != "" {
					output.ErrorMessage = &errMsg
				}
			}
		}

		rawDelta, hasDelta := choiceObj.Get("delta")
		if !hasDelta {
			continue
		}
		deltaObj, ok := rawDelta.(*jsonx.Object)
		if !ok || deltaObj == nil {
			continue
		}

		if rawContent, hasContent := deltaObj.Get("content"); hasContent && rawContent != nil {
			switch c := rawContent.(type) {
			case string:
				appendTextDelta(ai.SanitizeSurrogates(c))
			case []any:
				for _, item := range c {
					switch it := item.(type) {
					case string:
						appendTextDelta(ai.SanitizeSurrogates(it))
					case *jsonx.Object:
						if it == nil {
							continue
						}
						typ, _ := it.GetString("type")
						switch typ {
						case "thinking":
							var sb strings.Builder
							if rawParts, ok := it.Get("thinking"); ok {
								if parts, ok := rawParts.([]any); ok {
									for _, part := range parts {
										if pObj, ok := part.(*jsonx.Object); ok && pObj != nil {
											if txt, ok := pObj.GetString("text"); ok && len(txt) > 0 {
												sb.WriteString(txt)
											}
										}
									}
								}
							}
							appendThinkingDelta(ai.SanitizeSurrogates(sb.String()))
						case "text":
							txt, _ := it.GetString("text")
							appendTextDelta(ai.SanitizeSurrogates(txt))
						}
					}
				}
			}
		}

		if rawToolCalls, hasTC := deltaObj.Get("tool_calls"); hasTC && rawToolCalls != nil {
			if tcList, ok := rawToolCalls.([]any); ok {
				for _, item := range tcList {
					tcObj, ok := item.(*jsonx.Object)
					if !ok || tcObj == nil {
						continue
					}
					if currentBlock != nil {
						finishCurrentBlock(currentBlock)
						currentBlock = nil
					}
					tcIndex := 0
					hasIdx := false
					if rawIdx, ok := tcObj.Get("index"); ok {
						if v, ok := clshToFloat64(rawIdx); ok && !math.IsNaN(v) {
							tcIndex = int(v)
							hasIdx = true
						}
					}
					rawID, _ := tcObj.GetString("id")
					callID := rawID
					if callID == "" || callID == "null" {
						callID = mcDeriveToolCallId(fmt.Sprintf("toolcall:%d", tcIndex), 0)
					}
					key := "id:" + callID
					if hasIdx {
						key = "idx:" + strconv.Itoa(tcIndex)
					}
					var fnObj *jsonx.Object
					if rawFn, ok := tcObj.Get("function"); ok {
						fnObj, _ = rawFn.(*jsonx.Object)
					}
					fnName := ""
					if fnObj != nil {
						fnName, _ = fnObj.GetString("name")
					}
					open := toolCallsByKey[key]
					if open == nil {
						tcBlock := &ai.ToolCall{
							Type:      "toolCall",
							Id:        callID,
							Name:      fnName,
							Arguments: jsonx.NewObject(),
						}
						output.Content = append(output.Content, tcBlock)
						open = &openToolCall{
							contentIndex: len(output.Content) - 1,
							arguments:    ai.NewToolCallArgumentsStream(),
						}
						toolCallsByKey[key] = open
						toolCallOrder = append(toolCallOrder, key)
						stream.Push(&ai.AssistantMessageEventToolcallStart{
							Type:         "toolcall_start",
							ContentIndex: open.contentIndex,
							Partial:      output,
						})
					}
					argsDelta := ""
					if fnObj != nil {
						if rawArgs, ok := fnObj.Get("arguments"); ok && rawArgs != nil {
							switch a := rawArgs.(type) {
							case string:
								argsDelta = a
							default:
								s, err := jsonx.Stringify(a)
								if err == nil {
									argsDelta = s
								} else {
									argsDelta = "{}"
								}
							}
						}
					}
					if tcBlock, ok := output.Content[open.contentIndex].(*ai.ToolCall); ok && tcBlock != nil {
						tcBlock.Arguments = open.arguments.Append(argsDelta)
					}
					stream.Push(&ai.AssistantMessageEventToolcallDelta{
						Type:         "toolcall_delta",
						ContentIndex: open.contentIndex,
						Delta:        argsDelta,
						Partial:      output,
					})
				}
			}
		}
	}

	finishCurrentBlock(currentBlock)
	for _, key := range toolCallOrder {
		open := toolCallsByKey[key]
		if tcBlock, ok := output.Content[open.contentIndex].(*ai.ToolCall); ok && tcBlock != nil {
			argsObj, argsErr := open.arguments.Finish()
			tcBlock.Arguments = argsObj
			tcBlock.ArgumentsError = argsErr
			stream.Push(&ai.AssistantMessageEventToolcallEnd{
				Type:         "toolcall_end",
				ContentIndex: open.contentIndex,
				ToolCall:     tcBlock,
				Partial:      output,
			})
		}
	}
	return nil
}

func mcLookupThinkingLevel(m *ai.ThinkingLevelMap, level ai.ModelThinkingLevel) jsonx.Opt[string] {
	if m == nil {
		return jsonx.Opt[string]{}
	}
	switch level {
	case ai.ModelThinkingLevelOff:
		return m.Off
	case ai.ModelThinkingLevelMinimal:
		return m.Minimal
	case ai.ModelThinkingLevelLow:
		return m.Low
	case ai.ModelThinkingLevelMedium:
		return m.Medium
	case ai.ModelThinkingLevelHigh:
		return m.High
	case ai.ModelThinkingLevelXhigh:
		return m.Xhigh
	case ai.ModelThinkingLevelMax:
		return m.Max
	default:
		return jsonx.Opt[string]{}
	}
}

// StreamMistralConversations streams responses from the native Mistral Chat Completions endpoint.
func StreamMistralConversations(
	ctx context.Context,
	model *ai.Model,
	transcriptCtx *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := mcUnwrapOptions(options)
	normalizedContext := ai.ResolveTranscript(transcriptCtx, mcSupportsMidConvoSystemMessages(model))

	go func() {
		output := mcCreateOutput(model)
		fail := func(err error) {
			if ctx != nil && ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
			} else {
				output.StopReason = ai.StopReasonError
			}
			msg := mcFormatError(err)
			output.ErrorMessage = &msg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End()
		}

		if opts.ApiKey == nil || *opts.ApiKey == "" {
			fail(fmt.Errorf("No API key for provider: %s", model.Provider))
			return
		}
		apiKey := *opts.ApiKey

		normalizeId := mcNewToolCallIdNormalizer()
		transformedMessages := TransformMessages(normalizedContext.Messages, model, normalizeId)

		var payload any
		var err error
		payload, err = mcBuildChatPayload(model, normalizedContext, transformedMessages, opts)
		if err != nil {
			fail(err)
			return
		}
		if opts.OnPayload != nil {
			nextPayload, err := opts.OnPayload(payload, model)
			if err != nil {
				fail(err)
				return
			}
			if nextPayload != nil {
				payload = nextPayload
			}
		}

		parsedBase, err := url.Parse(model.BaseUrl)
		if err != nil {
			fail(err)
			return
		}
		parsedBase.Path = strings.TrimRight(parsedBase.Path, "/") + "/"
		endpointUrl := parsedBase.ResolveReference(&url.URL{Path: "v1/chat/completions"})

		bodyBytes, err := jsonx.Marshal(mcToWirePayload(payload))
		if err != nil {
			fail(err)
			return
		}

		timeoutMs := mcDefaultHeaderWaitMs
		if opts.TimeoutMs != nil {
			timeoutMs = *opts.TimeoutMs
		}
		parentCtx := ctx
		if parentCtx == nil {
			parentCtx = context.Background()
		}

		// The timeout covers only the wait for response headers; body reads keep parentCtx.
		hdrWaitCtx, cancelHdrWait := context.WithCancel(parentCtx)
		defer cancelHdrWait()

		var timedOut atomic.Bool
		hdrDone := make(chan struct{})
		timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		go func() {
			select {
			case <-timer.C:
				timedOut.Store(true)
				cancelHdrWait()
			case <-hdrDone:
			}
		}()

		req, err := http.NewRequestWithContext(hdrWaitCtx, http.MethodPost, endpointUrl.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			close(hdrDone)
			timer.Stop()
			fail(err)
			return
		}
		req.Header = mcBuildHeaders(model, apiKey, opts)

		httpClient := clshResolveHTTPClient(opts.HTTPClient, opts.Env)
		resp, err := httpClient.Do(req)
		close(hdrDone)
		timer.Stop()
		if err != nil {
			if timedOut.Load() && parentCtx.Err() == nil {
				fail(fmt.Errorf("Mistral response headers timed out after %dms", timeoutMs))
				return
			}
			fail(err)
			return
		}
		defer resp.Body.Close()

		if opts.OnResponse != nil {
			if err := opts.OnResponse(&ai.ProviderResponse{
				Status:  resp.StatusCode,
				Headers: ai.HeadersToRecord(resp.Header),
			}, model); err != nil {
				fail(err)
				return
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				fail(readErr)
				return
			}
			statusText := strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" ")
			msg := statusText
			if msg == "" {
				msg = fmt.Sprintf("Request failed with status %d", resp.StatusCode)
			}
			fail(&mcHttpError{
				statusCode: resp.StatusCode,
				body:       string(errBody),
				headers:    resp.Header.Clone(),
				message:    msg,
			})
			return
		}

		stream.Push(&ai.AssistantMessageEventStart{
			Type:    "start",
			Partial: output,
		})

		if err := mcConsumeChatStream(model, output, stream, resp.Body, opts.OnProviderStreamEvent); err != nil {
			fail(err)
			return
		}

		if parentCtx.Err() != nil {
			fail(errors.New("Request was aborted"))
			return
		}
		if output.StopReason == ai.StopReasonPending {
			fail(errors.New("Mistral stream ended without a finish reason"))
			return
		}
		if output.StopReason == ai.StopReasonAborted || output.StopReason == ai.StopReasonError {
			msg := "An unknown error occurred"
			if output.ErrorMessage != nil && *output.ErrorMessage != "" {
				msg = *output.ErrorMessage
			}
			fail(errors.New(msg))
			return
		}

		stream.Push(&ai.AssistantMessageEventDone{
			Type:    "done",
			Reason:  output.StopReason,
			Message: output,
		})
		stream.End()
	}()

	return stream
}

// StreamSimpleMistralConversations adapts SimpleStreamOptions to MistralOptions.
func StreamSimpleMistralConversations(
	ctx context.Context,
	model *ai.Model,
	transcriptCtx *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	var apiKeyPtr *string
	if options != nil {
		apiKeyPtr = options.ApiKey
	}
	base := BuildBaseOptions(model, transcriptCtx, options, apiKeyPtr)
	var toolChoiceAny any
	if options != nil && options.ToolChoice != nil {
		toolChoiceAny = *options.ToolChoice
	}

	var reasoning *ai.ModelThinkingLevel
	if options != nil && options.Reasoning != nil {
		clamped := ai.ClampThinkingLevel(model, ai.ModelThinkingLevel(*options.Reasoning))
		if clamped != ai.ModelThinkingLevelOff {
			reasoning = &clamped
		}
	}

	var effortMap *ai.ThinkingLevelMap
	if model != nil && model.Reasoning {
		effortMap = model.ThinkingLevelMap
	}

	var reasoningEffort *MistralReasoningEffort
	if effortMap != nil {
		if reasoning != nil {
			mapped := mcLookupThinkingLevel(effortMap, *reasoning)
			val := "high"
			if v, ok := mapped.Get(); ok {
				val = v
			}
			eff := MistralReasoningEffort(val)
			reasoningEffort = &eff
		} else if v, ok := effortMap.Off.Get(); ok {
			eff := MistralReasoningEffort(v)
			reasoningEffort = &eff
		}
	}

	var promptMode *string
	if model != nil && model.Reasoning && effortMap == nil && reasoning != nil {
		pm := "reasoning"
		promptMode = &pm
	}

	return StreamMistralConversations(ctx, model, transcriptCtx, &MistralOptions{
		StreamOptions:   base,
		ToolChoice:      toolChoiceAny,
		PromptMode:      promptMode,
		ReasoningEffort: reasoningEffort,
	})
}
