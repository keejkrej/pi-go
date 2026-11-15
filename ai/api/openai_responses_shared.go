// Ported from packages/ai/src/api/openai-responses-shared.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/http"
	"net/textproto"
	"net/url"
	"runtime"
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
	orsOpenAISdkVersion       = "7.19.0"
	orsDefaultTimeoutMs int64 = 600_000
)

// OpenAIResponsesStreamOptions configures stream processing for Responses API transports.
type OpenAIResponsesStreamOptions struct {
	OnProviderStreamEvent      func(data any, model *ai.Model) error
	ServiceTier                *string
	GrammarToolInputProperties map[string]string
	ResolveServiceTier         func(responseServiceTier *string, requestServiceTier *string) *string
	ApplyServiceTierPricing    func(usage *ai.Usage, serviceTier *string)
}

// ResponsesToolSupportOptions configures tool-change capabilities of a Responses transport.
type ResponsesToolSupportOptions struct {
	SupportsMidConvoSystemMessages bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
}

// ConvertResponsesMessagesOptions configures Responses API message serialization.
type ConvertResponsesMessagesOptions struct {
	IncludeSystemPrompt            *bool
	GrammarToolInputProperties     map[string]string
	SupportsMidConvoSystemMessages *bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
	ToolOptions                    *ConvertResponsesToolsOptions
	ConfigurationUpdateEffort      *string
}

// ConvertResponsesToolsOptions configures Responses API tool serialization.
type ConvertResponsesToolsOptions struct {
	Strict                     jsonx.Opt[bool]
	SupportsStrictMode         *bool
	SupportsOpenAIGrammarTools bool
	ToolSearchResult           bool
}

type orsParsedTextSignature struct {
	id    string
	phase ai.TextSignaturePhase
}

func orsEncodeTextSignatureV1(id string, phase ai.TextSignaturePhase) string {
	obj := jsonx.NewObject()
	obj.Set("v", 1)
	obj.Set("id", id)
	if phase != "" {
		obj.Set("phase", string(phase))
	}
	s, err := jsonx.Stringify(obj)
	if err != nil {
		return ""
	}
	return s
}

func orsParseTextSignature(signature *string) *orsParsedTextSignature {
	if signature == nil || *signature == "" {
		return nil
	}
	sig := *signature
	if strings.HasPrefix(sig, "{") {
		if parsed, err := jsonx.ParseString(sig); err == nil {
			if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
				v, hasV := obj.GetNumber("v")
				id, hasId := obj.GetString("id")
				if hasV && v == 1 && hasId {
					out := &orsParsedTextSignature{id: id}
					if phase, ok := obj.GetString("phase"); ok {
						if phase == string(ai.TextSignaturePhaseCommentary) || phase == string(ai.TextSignaturePhaseFinalAnswer) {
							out.phase = ai.TextSignaturePhase(phase)
						}
					}
					return out
				}
			}
		}
	}
	return &orsParsedTextSignature{id: sig}
}

func orsConvertToolResultOutput(model *ai.Model, content ai.ContentList) any {
	var textParts []string
	var images []*ai.ImageContent
	for _, block := range content {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				textParts = append(textParts, b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				images = append(images, b)
			}
		}
	}
	textResult := strings.Join(textParts, "\n")
	hasText := len(js.Trim(textResult)) > 0

	supportsImages := model != nil && slices.Contains(model.Input, ai.ModelModalityImage)
	if len(images) == 0 || !supportsImages {
		if hasText {
			return ai.SanitizeSurrogates(textResult)
		}
		if len(images) > 0 {
			return "(see attached image)"
		}
		return "(no tool output)"
	}

	output := make([]any, 0, len(images)+1)
	if hasText {
		item := jsonx.NewObject()
		item.Set("type", "input_text")
		item.Set("text", ai.SanitizeSurrogates(textResult))
		output = append(output, item)
	}
	for _, image := range images {
		item := jsonx.NewObject()
		item.Set("type", "input_image")
		item.Set("detail", "auto")
		item.Set("image_url", "data:"+image.MimeType+";base64,"+image.Data)
		output = append(output, item)
	}
	return output
}

// GetResponsesToolSupport returns how a Responses transport expresses tool changes after the first request.
func GetResponsesToolSupport(options ResponsesToolSupportOptions) ai.TranscriptToolSupport {
	if options.SupportsMidConvoSystemMessages && options.SupportsAdditionalTools {
		return ai.TranscriptToolSupportChanges
	}
	if options.SupportsToolSearch {
		return ai.TranscriptToolSupportAdditions
	}
	return ai.TranscriptToolSupportNone
}

func orsRenderIncrementalToolsNotice(unavailable *omap.Set[string]) string {
	notice := "This is an incremental tools update. Previously declared tools remain available unless explicitly marked unavailable. A tool declared again here is available again, and its latest definition replaces the earlier one."
	if unavailable != nil && unavailable.Len() > 0 {
		return notice + "\nStill unavailable: " + strings.Join(unavailable.Values(), ", ") + "."
	}
	return notice
}

func orsRenderToolRemovalNotice(names []string) string {
	lines := make([]string, len(names))
	for i, name := range names {
		lines[i] = "- " + name
	}
	return "The following tools are no longer available. Do not call them:\n" + strings.Join(lines, "\n")
}

func orsNormalizeIdPart(part string) string {
	units := js.ToUTF16(part)
	buf := make([]byte, len(units))
	for i, u := range units {
		if (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z') || (u >= '0' && u <= '9') || u == '_' || u == '-' {
			buf[i] = byte(u)
		} else {
			buf[i] = '_'
		}
	}
	if len(buf) > 64 {
		buf = buf[:64]
	}
	return strings.TrimRight(string(buf), "_")
}

func orsBuildForeignResponsesItemId(itemId string) string {
	normalized := "fc_" + ai.ShortHash(itemId)
	if len(normalized) > 64 {
		return normalized[:64]
	}
	return normalized
}

func orsSupportsDeveloperRole(model *ai.Model) bool {
	if model == nil || model.Compat.V == nil {
		return true
	}
	switch c := model.Compat.V.(type) {
	case *ai.OpenAIResponsesCompat:
		if c != nil && c.SupportsDeveloperRole != nil {
			return *c.SupportsDeveloperRole
		}
	case *ai.OpenAICompletionsCompat:
		if c != nil && c.SupportsDeveloperRole != nil {
			return *c.SupportsDeveloperRole
		}
	case *ai.UnknownModelCompat:
		if c != nil && c.Raw != nil {
			if b, ok := c.Raw.GetBool("supportsDeveloperRole"); ok {
				return b
			}
		}
	}
	return true
}

// ConvertResponsesMessages converts a TranscriptContext into OpenAI Responses input items.
func ConvertResponsesMessages(
	model *ai.Model,
	c *ai.TranscriptContext,
	allowedToolCallProviders map[string]struct{},
	options *ConvertResponsesMessagesOptions,
) ([]any, error) {
	var supportsMidConvo *bool
	if options != nil {
		supportsMidConvo = options.SupportsMidConvoSystemMessages
	}
	normalizedContext := ai.ResolveTranscript(c, supportsMidConvo)
	messages := make([]any, 0)

	normalizeToolCallId := func(id string, targetModel *ai.Model, source *ai.AssistantMessage) string {
		if targetModel == nil {
			return orsNormalizeIdPart(id)
		}
		if _, allowed := allowedToolCallProviders[string(targetModel.Provider)]; !allowed {
			return orsNormalizeIdPart(id)
		}
		if !strings.Contains(id, "|") {
			return orsNormalizeIdPart(id)
		}
		parts := strings.SplitN(id, "|", 3)
		callId := parts[0]
		itemId := ""
		if len(parts) > 1 {
			itemId = parts[1]
		}
		normalizedCallId := orsNormalizeIdPart(callId)
		isForeignToolCall := source == nil || string(source.Provider) != string(targetModel.Provider) || string(source.Api) != targetModel.Api
		var normalizedItemId string
		if isForeignToolCall {
			normalizedItemId = orsBuildForeignResponsesItemId(itemId)
		} else {
			normalizedItemId = orsNormalizeIdPart(itemId)
		}
		if !strings.HasPrefix(normalizedItemId, "fc_") {
			normalizedItemId = orsNormalizeIdPart("fc_" + normalizedItemId)
		}
		return normalizedCallId + "|" + normalizedItemId
	}

	transformedMessages := TransformMessages(normalizedContext.Messages, model, normalizeToolCallId)
	var toolSupportOpts ResponsesToolSupportOptions
	var toolOpts *ConvertResponsesToolsOptions
	var grammarProps map[string]string
	var configUpdateEffort *string
	includeInitialSystemMessage := true
	if options != nil {
		if options.SupportsMidConvoSystemMessages != nil {
			toolSupportOpts.SupportsMidConvoSystemMessages = *options.SupportsMidConvoSystemMessages
		}
		toolSupportOpts.SupportsAdditionalTools = options.SupportsAdditionalTools
		toolSupportOpts.SupportsToolSearch = options.SupportsToolSearch
		toolOpts = options.ToolOptions
		grammarProps = options.GrammarToolInputProperties
		configUpdateEffort = options.ConfigurationUpdateEffort
		if options.IncludeSystemPrompt != nil {
			includeInitialSystemMessage = *options.IncludeSystemPrompt
		}
	}
	toolSupport := GetResponsesToolSupport(toolSupportOpts)
	transcriptTools := ai.ResolveTranscriptTools(normalizedContext.Messages, toolSupport)

	instructionRole := "system"
	if model != nil && model.Reasoning && orsSupportsDeveloperRole(model) {
		instructionRole = "developer"
	}

	declaredAdditionalTools := false
	unavailableTools := omap.NewSet[string]()

	appendSystemToolAdditions := func(msg *ai.SystemMessage, seed string) error {
		tools := msg.ToolsAdded
		for _, tool := range tools {
			if tool != nil {
				unavailableTools.Delete(tool.Name)
			}
		}
		if len(tools) == 0 {
			return nil
		}
		if toolSupport == ai.TranscriptToolSupportChanges {
			if declaredAdditionalTools {
				item := jsonx.NewObject()
				item.Set("role", instructionRole)
				item.Set("content", orsRenderIncrementalToolsNotice(unavailableTools))
				messages = append(messages, item)
			}
			declaredAdditionalTools = true
			converted, err := ConvertResponsesTools(tools, toolOpts)
			if err != nil {
				return err
			}
			item := jsonx.NewObject()
			item.Set("type", "additional_tools")
			item.Set("role", "developer")
			item.Set("tools", converted)
			messages = append(messages, item)
			return nil
		}
		if toolSupport != ai.TranscriptToolSupportAdditions {
			return nil
		}
		names := make([]string, len(tools))
		for i, tool := range tools {
			if tool != nil {
				names[i] = tool.Name
			}
		}
		callId := "pi_tool_load_" + ai.ShortHash(seed+":"+strings.Join(names, ","))
		searchArgs := jsonx.NewObject()
		searchArgs.Set("query", strings.Join(names, " "))
		searchArgs.Set("limit", len(names))

		searchCall := jsonx.NewObject()
		searchCall.Set("type", "tool_search_call")
		searchCall.Set("call_id", callId)
		searchCall.Set("execution", "client")
		searchCall.Set("status", "completed")
		searchCall.Set("arguments", searchArgs)
		messages = append(messages, searchCall)

		var resultToolOpts ConvertResponsesToolsOptions
		if toolOpts != nil {
			resultToolOpts = *toolOpts
		}
		resultToolOpts.ToolSearchResult = true
		converted, err := ConvertResponsesTools(tools, &resultToolOpts)
		if err != nil {
			return err
		}
		searchOutput := jsonx.NewObject()
		searchOutput.Set("type", "tool_search_output")
		searchOutput.Set("call_id", callId)
		searchOutput.Set("execution", "client")
		searchOutput.Set("status", "completed")
		searchOutput.Set("tools", converted)
		messages = append(messages, searchOutput)
		return nil
	}

	pushConfigurationUpdate := func(effort string) {
		reasoning := jsonx.NewObject()
		reasoning.Set("effort", effort)
		item := jsonx.NewObject()
		item.Set("type", "configuration_update")
		item.Set("reasoning", reasoning)
		messages = append(messages, item)
	}

	msgIndex := 0
	sourceIndex := 0
	systemIndex := 0
	for _, msg := range transformedMessages {
		isLeadingSystemMessage := sourceIndex == 0 && msg != nil && msg.GetRole() == "system"
		sourceIndex++
		switch m := msg.(type) {
		case *ai.SystemMessage:
			if m == nil {
				break
			}
			anchorsToolChanges := systemIndex >= transcriptTools.FoldedSystemMessages
			systemIndex++
			if anchorsToolChanges {
				if err := appendSystemToolAdditions(m, fmt.Sprintf("system:%d", msgIndex)); err != nil {
					return nil, err
				}
			}
			if !isLeadingSystemMessage || includeInitialSystemMessage {
				var firstPart string
				if isLeadingSystemMessage {
					firstPart = ai.GetSystemMessageText(m)
				} else {
					firstPart = ai.RenderSystemMessageUpdate(m)
				}
				var parts []string
				if len(firstPart) > 0 {
					parts = append(parts, firstPart)
				}
				if anchorsToolChanges {
					added := make(map[string]struct{}, len(m.ToolsAdded))
					for _, tool := range m.ToolsAdded {
						if tool != nil {
							added[tool.Name] = struct{}{}
						}
					}
					var removed []string
					for _, tool := range m.ToolsRemoved {
						if _, wasAdded := added[tool.Name]; !wasAdded {
							removed = append(removed, tool.Name)
						}
					}
					if len(removed) > 0 {
						parts = append(parts, orsRenderToolRemovalNotice(removed))
					}
					for _, name := range removed {
						unavailableTools.Add(name)
					}
				}
				text := strings.Join(parts, "\n\n")
				if len(text) > 0 {
					item := jsonx.NewObject()
					item.Set("role", instructionRole)
					item.Set("content", ai.SanitizeSurrogates(text))
					messages = append(messages, item)
				}
			}

		case *ai.UserMessage:
			if m == nil {
				break
			}
			if m.Content.Text != nil {
				textItem := jsonx.NewObject()
				textItem.Set("type", "input_text")
				textItem.Set("text", ai.SanitizeSurrogates(*m.Content.Text))
				userItem := jsonx.NewObject()
				userItem.Set("role", "user")
				userItem.Set("content", []any{textItem})
				messages = append(messages, userItem)
			} else {
				content := make([]any, 0, len(m.Content.Blocks))
				for _, block := range m.Content.Blocks {
					switch b := block.(type) {
					case *ai.TextContent:
						if b != nil {
							textItem := jsonx.NewObject()
							textItem.Set("type", "input_text")
							textItem.Set("text", ai.SanitizeSurrogates(b.Text))
							content = append(content, textItem)
						}
					case *ai.ImageContent:
						if b != nil {
							imgItem := jsonx.NewObject()
							imgItem.Set("type", "input_image")
							imgItem.Set("detail", "auto")
							imgItem.Set("image_url", "data:"+b.MimeType+";base64,"+b.Data)
							content = append(content, imgItem)
						}
					}
				}
				if len(content) == 0 {
					if !isLeadingSystemMessage {
						msgIndex++
					}
					continue
				}
				userItem := jsonx.NewObject()
				userItem.Set("role", "user")
				userItem.Set("content", content)
				messages = append(messages, userItem)
			}

		case *ai.AssistantMessage:
			if m == nil {
				break
			}
			output := make([]any, 0, len(m.Content))
			isSameProviderAndApi := model != nil && string(m.Provider) == string(model.Provider) && string(m.Api) == model.Api
			isSameModel := isSameProviderAndApi && m.Model == model.Id
			isDifferentModel := isSameProviderAndApi && m.Model != model.Id
			textBlockIndex := 0

			for _, block := range m.Content {
				switch b := block.(type) {
				case *ai.ThinkingContent:
					if b != nil && b.ThinkingSignature != nil && *b.ThinkingSignature != "" {
						parsed, err := jsonx.ParseString(*b.ThinkingSignature)
						if err != nil {
							return nil, err
						}
						output = append(output, parsed)
					}
				case *ai.TextContent:
					if b == nil {
						continue
					}
					parsedSig := orsParseTextSignature(b.TextSignature)
					var fallbackMessageId string
					if textBlockIndex == 0 {
						fallbackMessageId = fmt.Sprintf("msg_pi_%d", msgIndex)
					} else {
						fallbackMessageId = fmt.Sprintf("msg_pi_%d_%d", msgIndex, textBlockIndex)
					}
					textBlockIndex++
					msgId := ""
					if parsedSig != nil {
						msgId = parsedSig.id
					}
					if msgId == "" {
						msgId = fallbackMessageId
					} else if js.Len(msgId) > 64 {
						msgId = "msg_" + ai.ShortHash(msgId)
					}

					outText := jsonx.NewObject()
					outText.Set("type", "output_text")
					outText.Set("text", ai.SanitizeSurrogates(b.Text))
					outText.Set("annotations", []any{})

					msgItem := jsonx.NewObject()
					msgItem.Set("type", "message")
					msgItem.Set("role", "assistant")
					msgItem.Set("content", []any{outText})
					msgItem.Set("status", "completed")
					msgItem.Set("id", msgId)
					if parsedSig != nil && parsedSig.phase != "" {
						msgItem.Set("phase", string(parsedSig.phase))
					}
					output = append(output, msgItem)

				case *ai.ToolCall:
					if b == nil {
						continue
					}
					callId := b.Id
					itemIdRaw := ""
					hasItemId := false
					if parts := strings.SplitN(b.Id, "|", 3); len(parts) > 1 {
						callId = parts[0]
						itemIdRaw = parts[1]
						hasItemId = itemIdRaw != ""
					}
					customInputProp, isCustomTool := grammarProps[b.Name]
					itemId := itemIdRaw
					itemIdPrefix := "fc_"
					if isCustomTool {
						itemIdPrefix = "ctc_"
					}
					if isDifferentModel || !hasItemId || !strings.HasPrefix(itemId, itemIdPrefix) {
						itemId = ""
						hasItemId = false
					}

					if isCustomTool {
						rawInput, err := GetGrammarToolInput(b.Name, b.Arguments, customInputProp)
						if err != nil {
							return nil, err
						}
						item := jsonx.NewObject()
						item.Set("type", "custom_tool_call")
						if hasItemId {
							item.Set("id", itemId)
						}
						item.Set("call_id", callId)
						item.Set("name", b.Name)
						item.Set("input", ai.SanitizeSurrogates(rawInput))
						if isSameModel && b.Namespace != nil {
							item.Set("namespace", *b.Namespace)
						}
						output = append(output, item)
					} else {
						argsObj := b.Arguments
						if argsObj == nil {
							argsObj = jsonx.NewObject()
						}
						argsStr, err := jsonx.Stringify(argsObj)
						if err != nil {
							return nil, err
						}
						item := jsonx.NewObject()
						item.Set("type", "function_call")
						if hasItemId {
							item.Set("id", itemId)
						}
						item.Set("call_id", callId)
						item.Set("name", b.Name)
						item.Set("arguments", argsStr)
						if isSameModel && b.Namespace != nil {
							item.Set("namespace", *b.Namespace)
						}
						output = append(output, item)
					}
				}
			}

			if len(output) == 0 {
				if !isLeadingSystemMessage {
					msgIndex++
				}
				continue
			}
			if configUpdateEffort != nil && isSameModel && m.ProviderThinkingLevel != nil {
				pushConfigurationUpdate(*m.ProviderThinkingLevel)
			}
			messages = append(messages, output...)

		case *ai.ToolResultMessage:
			if m == nil {
				break
			}
			callId := m.ToolCallId
			if before, _, found := strings.Cut(m.ToolCallId, "|"); found {
				callId = before
			}
			outContent := orsConvertToolResultOutput(model, m.Content)
			item := jsonx.NewObject()
			if _, isCustom := grammarProps[m.ToolName]; isCustom {
				item.Set("type", "custom_tool_call_output")
			} else {
				item.Set("type", "function_call_output")
			}
			item.Set("call_id", callId)
			item.Set("output", outContent)
			messages = append(messages, item)
		}
		if !isLeadingSystemMessage {
			msgIndex++
		}
	}

	if configUpdateEffort != nil {
		pushConfigurationUpdate(*configUpdateEffort)
	}

	return messages, nil
}

// ConvertResponsesTools converts pi tool declarations into OpenAI Responses tool definitions.
func ConvertResponsesTools(tools []*ai.Tool, options *ConvertResponsesToolsOptions) ([]any, error) {
	defaultStrict := jsonx.Some(false)
	supportsStrictMode := true
	supportsOpenAIGrammarTools := false
	toolSearchResult := false
	if options != nil {
		if !options.Strict.IsZero() {
			defaultStrict = options.Strict
		}
		if options.SupportsStrictMode != nil {
			supportsStrictMode = *options.SupportsStrictMode
		}
		supportsOpenAIGrammarTools = options.SupportsOpenAIGrammarTools
		toolSearchResult = options.ToolSearchResult
	}

	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		grammar, err := ResolveGrammarConstrainedSampling(tool, supportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			formatObj := jsonx.NewObject()
			formatObj.Set("type", "grammar")
			formatObj.Set("syntax", string(grammar.Format))
			formatObj.Set("definition", grammar.Definition)

			item := jsonx.NewObject()
			item.Set("type", "custom")
			item.Set("name", tool.Name)
			item.Set("description", tool.Description)
			item.Set("format", formatObj)
			if toolSearchResult {
				item.Set("defer_loading", true)
			}
			out = append(out, item)
			continue
		}

		constrainedStrict, err := ResolveJsonSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		strict := defaultStrict
		if constrainedStrict != nil {
			strict = jsonx.Some(*constrainedStrict)
		}
		isStrictTrue := strict.IsSome() && strict.OrElse(false)
		paramsSchema, err := GetJsonSchemaToolParameters(tool, &isStrictTrue)
		if err != nil {
			return nil, err
		}
		var paramsVal any = paramsSchema
		if paramsSchema != nil && paramsSchema.Object != nil {
			paramsVal = paramsSchema.Object
		}

		item := jsonx.NewObject()
		item.Set("type", "function")
		item.Set("name", tool.Name)
		item.Set("description", tool.Description)
		item.Set("parameters", paramsVal)
		if toolSearchResult {
			item.Set("defer_loading", true)
		}
		if supportsStrictMode {
			if strict.IsNull() {
				item.Set("strict", nil)
			} else {
				item.Set("strict", strict.OrElse(false))
			}
		}
		out = append(out, item)
	}
	return out, nil
}

type orsCustomInputState struct {
	property string
	buffer   *GrammarToolInputJsonBuffer
	unsent   string
}

type orsResponsesOutputSlot struct {
	kind          string // "thinking" | "text" | "toolCall"
	thinkingBlock *ai.ThinkingContent
	textBlock     *ai.TextContent
	toolCallBlock *ai.ToolCall
	contentIndex  int
	inputKind     string // "json" | "custom"
	argsStream    *ai.ToolCallArgumentsStream
	custom        *orsCustomInputState
}

func orsCustomInput(property string, initialInput string) (*orsCustomInputState, error) {
	buffer := &GrammarToolInputJsonBuffer{}
	unsentPtr, err := AppendGrammarToolInputJsonDelta(buffer, property, initialInput)
	if err != nil {
		return nil, err
	}
	unsent := ""
	if unsentPtr != nil {
		unsent = *unsentPtr
	}
	return &orsCustomInputState{
		property: property,
		buffer:   buffer,
		unsent:   unsent,
	}, nil
}

func orsSendCustomInput(block *ai.ToolCall, input *orsCustomInputState, jsonDelta *string, flush bool) *string {
	if flush {
		argsObj := jsonx.NewObject()
		argsObj.Set(input.property, input.buffer.flushInput())
		block.Arguments = argsObj
	}
	if input.unsent == "" {
		return jsonDelta
	}
	sent := input.unsent
	if jsonDelta != nil {
		sent += *jsonDelta
	}
	input.unsent = ""
	return &sent
}

func orsAppendCustomInput(block *ai.ToolCall, input *orsCustomInputState, delta string) (*string, error) {
	jsonDelta, err := AppendGrammarToolInputJsonDelta(input.buffer, input.property, delta)
	if err != nil {
		return nil, err
	}
	return orsSendCustomInput(block, input, jsonDelta, false), nil
}

func orsCloseCustomInput(block *ai.ToolCall, input *orsCustomInputState, finalInput *string) (*string, error) {
	jsonDelta, err := CloseGrammarToolInputJson(input.buffer, input.property, finalInput)
	if err != nil {
		return nil, err
	}
	return orsSendCustomInput(block, input, jsonDelta, true), nil
}

// orsClonePartial returns a point-in-time snapshot of output for live events so stream consumers
// reading event.Partial concurrently with subsequent producer mutations do not data-race under -race.
func orsClonePartial(src *ai.AssistantMessage) *ai.AssistantMessage {
	if src == nil {
		return nil
	}
	contentCopy := make(ai.ContentList, len(src.Content))
	for i, blk := range src.Content {
		switch b := blk.(type) {
		case *ai.TextContent:
			if b != nil {
				cp := *b
				contentCopy[i] = &cp
			}
		case *ai.ThinkingContent:
			if b != nil {
				cp := *b
				contentCopy[i] = &cp
			}
		case *ai.ToolCall:
			if b != nil {
				cp := *b
				if b.Arguments != nil {
					cp.Arguments = b.Arguments.Clone()
				}
				contentCopy[i] = &cp
			}
		default:
			contentCopy[i] = blk
		}
	}
	usageCopy := src.Usage
	if src.Usage.Reasoning != nil {
		r := *src.Usage.Reasoning
		usageCopy.Reasoning = &r
	}
	if src.Usage.CacheWrite1h != nil {
		cw := *src.Usage.CacheWrite1h
		usageCopy.CacheWrite1h = &cw
	}
	if src.Usage.Cost.TotalFromProvider != nil {
		tfp := *src.Usage.Cost.TotalFromProvider
		usageCopy.Cost.TotalFromProvider = &tfp
	}
	return &ai.AssistantMessage{
		Role:                  src.Role,
		Content:               contentCopy,
		Api:                   src.Api,
		Provider:              src.Provider,
		Model:                 src.Model,
		ResponseModel:         src.ResponseModel,
		ResponseId:            src.ResponseId,
		ProviderThinkingLevel: src.ProviderThinkingLevel,
		ThinkingLevel:         src.ThinkingLevel,
		Diagnostics:           src.Diagnostics,
		Usage:                 usageCopy,
		StopReason:            src.StopReason,
		Deferred:              src.Deferred,
		ErrorMessage:          src.ErrorMessage,
		RawStopReason:         src.RawStopReason,
		EndTurn:               src.EndTurn,
		Timestamp:             src.Timestamp,
		DurationMs:            src.DurationMs,
	}
}

func orsIntField(obj *jsonx.Object, key string) int {
	if obj == nil {
		return 0
	}
	if n, ok := obj.GetNumber(key); ok {
		return int(n)
	}
	return 0
}

func orsScalarString(obj *jsonx.Object, key string) string {
	if obj == nil {
		return "undefined"
	}
	raw, exists := obj.Get(key)
	if !exists {
		return "undefined"
	}
	switch v := raw.(type) {
	case nil:
		return "null"
	case string:
		return v
	case float64:
		return jsonx.FormatNumber(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		if s, err := jsonx.Stringify(v); err == nil {
			return s
		}
		return fmt.Sprint(v)
	}
}

func orsMapStopReason(status string, incompleteReason *string) (ai.StopReason, *string, error) {
	if status == "" {
		return ai.StopReasonStop, nil, nil
	}
	switch status {
	case "completed", "in_progress", "queued":
		return ai.StopReasonStop, nil, nil
	case "incomplete":
		if incompleteReason != nil && *incompleteReason == "max_output_tokens" {
			return ai.StopReasonLength, nil, nil
		}
		msg := "Response incomplete without a provider reason"
		if incompleteReason != nil && *incompleteReason != "" {
			msg = "Response incomplete: " + *incompleteReason
		}
		return ai.StopReasonError, &msg, nil
	case "failed", "cancelled":
		return ai.StopReasonError, nil, nil
	default:
		return "", nil, fmt.Errorf("Unhandled stop reason: %s", status)
	}
}

// ProcessResponsesStream consumes OpenAI Responses events, mutates output, and pushes AssistantMessageEvents onto stream.
func ProcessResponsesStream(
	events iter.Seq2[*jsonx.Object, error],
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
	model *ai.Model,
	options *OpenAIResponsesStreamOptions,
) error {
	sawTerminalResponseEvent := false
	outputSlots := make(map[int]*orsResponsesOutputSlot)
	unfinishedToolCalls := make(map[*ai.ToolCall]struct{})
	reasoningBlocksById := make(map[string]*ai.ThinkingContent)

	applyMessagePhaseStopReason := func(item *jsonx.Object) {
		if item == nil {
			return
		}
		if typ, _ := item.GetString("type"); typ == "message" {
			if phase, _ := item.GetString("phase"); phase == "final_answer" {
				output.StopReason = ai.StopReasonStop
			}
		}
	}

	getSlot := func(outputIndex int, kind string) *orsResponsesOutputSlot {
		slot := outputSlots[outputIndex]
		if slot != nil && slot.kind == kind {
			return slot
		}
		return nil
	}

	pushToolCallDelta := func(slot *orsResponsesOutputSlot, delta *string) {
		if delta == nil {
			return
		}
		stream.Push(&ai.AssistantMessageEventToolcallDelta{
			Type:         "toolcall_delta",
			ContentIndex: slot.contentIndex,
			Delta:        *delta,
			Partial:      orsClonePartial(output),
		})
	}

	createSlot := func(outputIndex int, item *jsonx.Object) (*orsResponsesOutputSlot, error) {
		if item == nil {
			return nil, nil
		}
		itemType, _ := item.GetString("type")
		switch itemType {
		case "reasoning":
			block := &ai.ThinkingContent{Type: "thinking", Thinking: ""}
			output.Content = append(output.Content, block)
			slot := &orsResponsesOutputSlot{
				kind:          "thinking",
				thinkingBlock: block,
				contentIndex:  len(output.Content) - 1,
			}
			outputSlots[outputIndex] = slot
			stream.Push(&ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: slot.contentIndex,
				Partial:      orsClonePartial(output),
			})
			return slot, nil

		case "message":
			applyMessagePhaseStopReason(item)
			block := &ai.TextContent{Type: "text", Text: ""}
			output.Content = append(output.Content, block)
			slot := &orsResponsesOutputSlot{
				kind:         "text",
				textBlock:    block,
				contentIndex: len(output.Content) - 1,
			}
			outputSlots[outputIndex] = slot
			stream.Push(&ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: slot.contentIndex,
				Partial:      orsClonePartial(output),
			})
			return slot, nil

		case "function_call":
			callId, _ := item.GetString("call_id")
			itemId, _ := item.GetString("id")
			name, _ := item.GetString("name")
			argsStream := ai.NewToolCallArgumentsStream()
			argsObj := jsonx.NewObject()
			if rawArgs, ok := item.GetString("arguments"); ok && rawArgs != "" {
				argsObj = argsStream.Append(rawArgs)
			}
			block := &ai.ToolCall{
				Type:      "toolCall",
				Id:        callId + "|" + itemId,
				Name:      name,
				Arguments: argsObj,
			}
			if ns, ok := item.GetString("namespace"); ok {
				block.Namespace = &ns
			}
			output.Content = append(output.Content, block)
			unfinishedToolCalls[block] = struct{}{}
			slot := &orsResponsesOutputSlot{
				kind:          "toolCall",
				toolCallBlock: block,
				contentIndex:  len(output.Content) - 1,
				inputKind:     "json",
				argsStream:    argsStream,
			}
			outputSlots[outputIndex] = slot
			stream.Push(&ai.AssistantMessageEventToolcallStart{
				Type:         "toolcall_start",
				ContentIndex: slot.contentIndex,
				Partial:      orsClonePartial(output),
			})
			return slot, nil

		case "custom_tool_call":
			callId, _ := item.GetString("call_id")
			itemId, _ := item.GetString("id")
			name, _ := item.GetString("name")
			inputProperty := "input"
			if options != nil && options.GrammarToolInputProperties != nil {
				if p, ok := options.GrammarToolInputProperties[name]; ok {
					inputProperty = p
				}
			}
			rawInput, _ := item.GetString("input")
			argsObj := jsonx.NewObject()
			argsObj.Set(inputProperty, rawInput)
			block := &ai.ToolCall{
				Type:      "toolCall",
				Id:        callId + "|" + itemId,
				Name:      name,
				Arguments: argsObj,
			}
			if ns, ok := item.GetString("namespace"); ok {
				block.Namespace = &ns
			}
			customState, err := orsCustomInput(inputProperty, rawInput)
			if err != nil {
				return nil, err
			}
			output.Content = append(output.Content, block)
			unfinishedToolCalls[block] = struct{}{}
			slot := &orsResponsesOutputSlot{
				kind:          "toolCall",
				toolCallBlock: block,
				contentIndex:  len(output.Content) - 1,
				inputKind:     "custom",
				custom:        customState,
			}
			outputSlots[outputIndex] = slot
			stream.Push(&ai.AssistantMessageEventToolcallStart{
				Type:         "toolcall_start",
				ContentIndex: slot.contentIndex,
				Partial:      orsClonePartial(output),
			})
			return slot, nil
		}
		return nil, nil
	}

	getOrCreateSlot := func(outputIndex int, item *jsonx.Object) (*orsResponsesOutputSlot, error) {
		if slot := outputSlots[outputIndex]; slot != nil {
			return slot, nil
		}
		return createSlot(outputIndex, item)
	}

	backfillReasoningSignatures := func(responseOutput []any) {
		for _, rawItem := range responseOutput {
			item, ok := rawItem.(*jsonx.Object)
			if !ok || item == nil {
				continue
			}
			if typ, _ := item.GetString("type"); typ != "reasoning" {
				continue
			}
			enc, ok := item.GetString("encrypted_content")
			if !ok || enc == "" {
				continue
			}
			itemId, _ := item.GetString("id")
			block := reasoningBlocksById[itemId]
			if block == nil || block.ThinkingSignature == nil || *block.ThinkingSignature == "" {
				continue
			}
			parsedSig, err := jsonx.ParseString(*block.ThinkingSignature)
			if err != nil {
				continue
			}
			storedObj, ok := parsedSig.(*jsonx.Object)
			if !ok || storedObj == nil {
				continue
			}
			if existingEnc, hasEnc := storedObj.GetString("encrypted_content"); hasEnc && existingEnc != "" {
				continue
			}
			storedObj.Set("encrypted_content", enc)
			if updated, err := jsonx.Stringify(storedObj); err == nil {
				block.ThinkingSignature = &updated
			}
		}
	}

	finalizeResponse := func(response *jsonx.Object) error {
		sawTerminalResponseEvent = true
		if response != nil {
			if outArr, ok := response.GetArray("output"); ok {
				backfillReasoningSignatures(outArr)
			}
			if id, ok := response.GetString("id"); ok && id != "" {
				output.ResponseId = &id
			}
			if usageObj, ok := response.GetObject("usage"); ok {
				inputDetails, _ := usageObj.GetObject("input_tokens_details")
				cachedTokens := orsIntField(inputDetails, "cached_tokens")
				cacheWriteTokens := orsIntField(inputDetails, "cache_write_tokens")
				outputDetails, _ := usageObj.GetObject("output_tokens_details")
				reasoningTokens := orsIntField(outputDetails, "reasoning_tokens")
				inputTokens := max(0, orsIntField(usageObj, "input_tokens")-cachedTokens-cacheWriteTokens)
				output.Usage = ai.Usage{
					Input:       inputTokens,
					Output:      orsIntField(usageObj, "output_tokens"),
					CacheRead:   cachedTokens,
					CacheWrite:  cacheWriteTokens,
					Reasoning:   &reasoningTokens,
					TotalTokens: orsIntField(usageObj, "total_tokens"),
				}
			}
		}
		ai.CalculateCost(model, &output.Usage)
		if options != nil && options.ApplyServiceTierPricing != nil {
			var respTier *string
			if response != nil {
				if st, ok := response.GetString("service_tier"); ok {
					respTier = &st
				}
			}
			var effectiveTier *string
			if options.ResolveServiceTier != nil {
				effectiveTier = options.ResolveServiceTier(respTier, options.ServiceTier)
			} else if respTier != nil {
				effectiveTier = respTier
			} else {
				effectiveTier = options.ServiceTier
			}
			options.ApplyServiceTierPricing(&output.Usage, effectiveTier)
		}

		status := ""
		hasStatus := false
		var incompleteReason *string
		if response != nil {
			status, hasStatus = response.GetString("status")
			if incDetails, ok := response.GetObject("incomplete_details"); ok {
				if reason, ok := incDetails.GetString("reason"); ok {
					incompleteReason = &reason
				}
			}
		}
		if incompleteReason != nil && *incompleteReason != "" {
			raw := status + "." + *incompleteReason
			output.RawStopReason = &raw
		} else if hasStatus {
			raw := status
			output.RawStopReason = &raw
		}
		mappedStop, mappedErrMsg, err := orsMapStopReason(status, incompleteReason)
		if err != nil {
			return err
		}
		output.StopReason = mappedStop
		output.ErrorMessage = mappedErrMsg
		if output.StopReason == ai.StopReasonStop {
			for _, block := range output.Content {
				if _, isTool := block.(*ai.ToolCall); isTool {
					output.StopReason = ai.StopReasonToolUse
					break
				}
			}
		}
		return nil
	}

	for event, iterErr := range events {
		if iterErr != nil {
			return iterErr
		}
		if event == nil {
			continue
		}
		if options != nil && options.OnProviderStreamEvent != nil {
			if err := options.OnProviderStreamEvent(event, model); err != nil {
				return err
			}
		}
		eventType, _ := event.GetString("type")
		switch eventType {
		case "response.created":
			if respObj, ok := event.GetObject("response"); ok {
				if id, ok := respObj.GetString("id"); ok {
					output.ResponseId = &id
				}
			}

		case "response.output_item.added":
			outputIndex := orsIntField(event, "output_index")
			item, _ := event.GetObject("item")
			if _, err := createSlot(outputIndex, item); err != nil {
				return err
			}

		case "response.reasoning_summary_text.delta":
			slot := getSlot(orsIntField(event, "output_index"), "thinking")
			if slot == nil {
				continue
			}
			delta, _ := event.GetString("delta")
			slot.thinkingBlock.Thinking += delta
			stream.Push(&ai.AssistantMessageEventThinkingDelta{
				Type:         "thinking_delta",
				ContentIndex: slot.contentIndex,
				Delta:        delta,
				Partial:      orsClonePartial(output),
			})

		case "response.reasoning_summary_part.done":
			slot := getSlot(orsIntField(event, "output_index"), "thinking")
			if slot == nil {
				continue
			}
			slot.thinkingBlock.Thinking += "\n\n"
			stream.Push(&ai.AssistantMessageEventThinkingDelta{
				Type:         "thinking_delta",
				ContentIndex: slot.contentIndex,
				Delta:        "\n\n",
				Partial:      orsClonePartial(output),
			})

		case "response.reasoning_text.delta":
			slot := getSlot(orsIntField(event, "output_index"), "thinking")
			if slot == nil {
				continue
			}
			delta, _ := event.GetString("delta")
			slot.thinkingBlock.Thinking += delta
			stream.Push(&ai.AssistantMessageEventThinkingDelta{
				Type:         "thinking_delta",
				ContentIndex: slot.contentIndex,
				Delta:        delta,
				Partial:      orsClonePartial(output),
			})

		case "response.output_text.delta", "response.refusal.delta":
			slot := getSlot(orsIntField(event, "output_index"), "text")
			if slot == nil {
				continue
			}
			delta, _ := event.GetString("delta")
			slot.textBlock.Text += delta
			stream.Push(&ai.AssistantMessageEventTextDelta{
				Type:         "text_delta",
				ContentIndex: slot.contentIndex,
				Delta:        delta,
				Partial:      orsClonePartial(output),
			})

		case "response.function_call_arguments.delta":
			slot := getSlot(orsIntField(event, "output_index"), "toolCall")
			if slot == nil || slot.inputKind != "json" {
				continue
			}
			delta, hasDelta := event.GetString("delta")
			if !hasDelta {
				continue
			}
			slot.toolCallBlock.Arguments = slot.argsStream.Append(delta)
			pushToolCallDelta(slot, &delta)

		case "response.function_call_arguments.done":
			slot := getSlot(orsIntField(event, "output_index"), "toolCall")
			if slot == nil || slot.inputKind != "json" {
				continue
			}
			fullArgs, _ := event.GetString("arguments")
			deltaPtr, argsObj := slot.argsStream.Replace(fullArgs)
			slot.toolCallBlock.Arguments = argsObj
			if deltaPtr != nil && *deltaPtr != "" {
				pushToolCallDelta(slot, deltaPtr)
			}

		case "response.custom_tool_call_input.delta":
			slot := getSlot(orsIntField(event, "output_index"), "toolCall")
			if slot == nil || slot.inputKind != "custom" {
				continue
			}
			delta, _ := event.GetString("delta")
			sent, err := orsAppendCustomInput(slot.toolCallBlock, slot.custom, delta)
			if err != nil {
				return err
			}
			pushToolCallDelta(slot, sent)

		case "response.custom_tool_call_input.done":
			slot := getSlot(orsIntField(event, "output_index"), "toolCall")
			if slot == nil || slot.inputKind != "custom" {
				continue
			}
			var finalInput *string
			if s, ok := event.GetString("input"); ok {
				finalInput = &s
			}
			sent, err := orsCloseCustomInput(slot.toolCallBlock, slot.custom, finalInput)
			if err != nil {
				return err
			}
			pushToolCallDelta(slot, sent)

		case "response.output_item.done":
			outputIndex := orsIntField(event, "output_index")
			item, _ := event.GetObject("item")
			applyMessagePhaseStopReason(item)
			slot, err := getOrCreateSlot(outputIndex, item)
			if err != nil {
				return err
			}
			if item == nil || slot == nil {
				continue
			}
			itemType, _ := item.GetString("type")
			switch {
			case itemType == "reasoning" && slot.kind == "thinking":
				var summaryParts []string
				if summaryArr, ok := item.GetArray("summary"); ok {
					for _, entry := range summaryArr {
						if obj, ok := entry.(*jsonx.Object); ok && obj != nil {
							if txt, ok := obj.GetString("text"); ok {
								summaryParts = append(summaryParts, txt)
							}
						}
					}
				}
				var contentParts []string
				if contentArr, ok := item.GetArray("content"); ok {
					for _, entry := range contentArr {
						if obj, ok := entry.(*jsonx.Object); ok && obj != nil {
							if txt, ok := obj.GetString("text"); ok {
								contentParts = append(contentParts, txt)
							}
						}
					}
				}
				summaryText := strings.Join(summaryParts, "\n\n")
				contentText := strings.Join(contentParts, "\n\n")
				if summaryText != "" {
					slot.thinkingBlock.Thinking = summaryText
				} else if contentText != "" {
					slot.thinkingBlock.Thinking = contentText
				}
				if sig, err := jsonx.Stringify(item); err == nil {
					slot.thinkingBlock.ThinkingSignature = &sig
				}
				if id, ok := item.GetString("id"); ok {
					reasoningBlocksById[id] = slot.thinkingBlock
				}
				stream.Push(&ai.AssistantMessageEventThinkingEnd{
					Type:         "thinking_end",
					ContentIndex: slot.contentIndex,
					Content:      slot.thinkingBlock.Thinking,
					Partial:      orsClonePartial(output),
				})
				delete(outputSlots, outputIndex)

			case itemType == "message" && slot.kind == "text":
				var parts []string
				if contentArr, ok := item.GetArray("content"); ok {
					for _, entry := range contentArr {
						if obj, ok := entry.(*jsonx.Object); ok && obj != nil {
							if cType, _ := obj.GetString("type"); cType == "output_text" {
								if txt, ok := obj.GetString("text"); ok {
									parts = append(parts, txt)
								}
							} else if ref, ok := obj.GetString("refusal"); ok {
								parts = append(parts, ref)
							}
						}
					}
				}
				slot.textBlock.Text = strings.Join(parts, "")
				id, _ := item.GetString("id")
				phase, _ := item.GetString("phase")
				sig := orsEncodeTextSignatureV1(id, ai.TextSignaturePhase(phase))
				slot.textBlock.TextSignature = &sig
				stream.Push(&ai.AssistantMessageEventTextEnd{
					Type:         "text_end",
					ContentIndex: slot.contentIndex,
					Content:      slot.textBlock.Text,
					Partial:      orsClonePartial(output),
				})
				delete(outputSlots, outputIndex)

			case itemType == "function_call" && slot.kind == "toolCall" && slot.inputKind == "json":
				if rawArgs, ok := item.GetString("arguments"); ok && rawArgs != "" {
					slot.argsStream.Replace(rawArgs)
				}
				argsObj, argsErr := slot.argsStream.Finish()
				slot.toolCallBlock.Arguments = argsObj
				slot.toolCallBlock.ArgumentsError = argsErr
				if ns, ok := item.GetString("namespace"); ok {
					slot.toolCallBlock.Namespace = &ns
				}
				delete(unfinishedToolCalls, slot.toolCallBlock)
				tcCopy := *slot.toolCallBlock
				if tcCopy.Arguments != nil {
					tcCopy.Arguments = tcCopy.Arguments.Clone()
				}
				stream.Push(&ai.AssistantMessageEventToolcallEnd{
					Type:         "toolcall_end",
					ContentIndex: slot.contentIndex,
					ToolCall:     &tcCopy,
					Partial:      orsClonePartial(output),
				})
				delete(outputSlots, outputIndex)

			case itemType == "custom_tool_call" && slot.kind == "toolCall" && slot.inputKind == "custom":
				var finalInput *string
				if s, ok := item.GetString("input"); ok {
					finalInput = &s
				}
				sent, err := orsCloseCustomInput(slot.toolCallBlock, slot.custom, finalInput)
				if err != nil {
					return err
				}
				pushToolCallDelta(slot, sent)
				if ns, ok := item.GetString("namespace"); ok {
					slot.toolCallBlock.Namespace = &ns
				}
				delete(unfinishedToolCalls, slot.toolCallBlock)
				tcCopy := *slot.toolCallBlock
				if tcCopy.Arguments != nil {
					tcCopy.Arguments = tcCopy.Arguments.Clone()
				}
				stream.Push(&ai.AssistantMessageEventToolcallEnd{
					Type:         "toolcall_end",
					ContentIndex: slot.contentIndex,
					ToolCall:     &tcCopy,
					Partial:      orsClonePartial(output),
				})
				delete(outputSlots, outputIndex)
			}

		case "response.completed", "response.incomplete":
			respObj, _ := event.GetObject("response")
			if err := finalizeResponse(respObj); err != nil {
				return err
			}

		case "error":
			code := orsScalarString(event, "code")
			msg := orsScalarString(event, "message")
			return fmt.Errorf("Error Code %s: %s", code, msg)

		case "response.failed":
			sawTerminalResponseEvent = true
			respObj, _ := event.GetObject("response")
			var errObj *jsonx.Object
			var incObj *jsonx.Object
			if respObj != nil {
				if st, ok := respObj.GetString("status"); ok {
					output.RawStopReason = &st
				}
				errObj, _ = respObj.GetObject("error")
				incObj, _ = respObj.GetObject("incomplete_details")
			}
			var msg string
			if errObj != nil {
				code, _ := errObj.GetString("code")
				if code == "" {
					code = "unknown"
				}
				errMsg, _ := errObj.GetString("message")
				if errMsg == "" {
					errMsg = "no message"
				}
				msg = code + ": " + errMsg
			} else if incObj != nil {
				if reason, ok := incObj.GetString("reason"); ok && reason != "" {
					msg = "incomplete: " + reason
				}
			}
			if msg == "" {
				msg = "Unknown error (no error details in response)"
			}
			return errors.New(msg)
		}
	}

	if !sawTerminalResponseEvent {
		return errors.New("OpenAI Responses stream ended before a terminal response event")
	}

	if output.StopReason == ai.StopReasonToolUse {
		for _, block := range output.Content {
			if tc, ok := block.(*ai.ToolCall); ok {
				if _, unfinished := unfinishedToolCalls[tc]; unfinished {
					return fmt.Errorf("OpenAI Responses stream completed with an unfinished tool call: %s (%s)", tc.Name, tc.Id)
				}
			}
		}
	}
	return nil
}

// =============================================================================
// Shared OpenAI JS SDK-equivalent raw HTTP + SSE transport helpers
// =============================================================================

type orsApiError struct {
	sdkErr  *ai.ProviderSdkError
	headers http.Header
}

func (e *orsApiError) Error() string                { return e.sdkErr.Error() }
func (e *orsApiError) Name() string                 { return e.sdkErr.Name() }
func (e *orsApiError) Unwrap() error                { return e.sdkErr }
func (e *orsApiError) ProviderStatus() *int         { return e.sdkErr.ProviderStatus() }
func (e *orsApiError) ProviderHeaders() http.Header { return e.headers }

type orsConnectionError struct {
	errName string
	message string
	cause   error
}

func (e *orsConnectionError) Error() string                { return e.message }
func (e *orsConnectionError) Name() string                 { return e.errName }
func (e *orsConnectionError) Unwrap() error                { return e.cause }
func (e *orsConnectionError) ProviderStatus() *int         { return nil }
func (e *orsConnectionError) ProviderHeaders() http.Header { return nil }

type orsOpenAIHttpConfig struct {
	BaseUrl         string
	Path            string
	ApiVersion      string // non-empty for Azure OpenAI (?api-version=...)
	ApiKey          string // Bearer token for OpenAI or api-key for Azure
	UseAzureApiKey  bool
	DefaultHeaders  ai.ProviderHeaders
	Body            any
	HTTPClient      *http.Client
	TimeoutMs       *int64
	MaxRetries      *int
	MaxRetryDelayMs *int64
}

func orsNormalizedPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	case "android":
		return "Android"
	default:
		return "Other:" + runtime.GOOS
	}
}

func orsNormalizedArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "x32"
	case "arm":
		return "arm"
	default:
		return "other:" + runtime.GOARCH
	}
}

func orsMakeOpenAIStatusMessage(status *int, errVal any, fallbackMsg string) string {
	msg := ""
	if errObj, ok := errVal.(*jsonx.Object); ok && errObj != nil {
		if rawMsg, hasMsg := errObj.Get("message"); hasMsg && rawMsg != nil {
			if s, ok := rawMsg.(string); ok {
				if s != "" {
					msg = s
				}
			} else if encoded, err := jsonx.Stringify(rawMsg); err == nil {
				msg = encoded
			}
		}
	}
	if msg == "" && errVal != nil && errVal != false {
		if s, ok := errVal.(string); ok && s == "" {
			// Empty string falsy in JS.
		} else if encoded, err := jsonx.Stringify(errVal); err == nil {
			msg = encoded
		}
	}
	if msg == "" {
		msg = fallbackMsg
	}
	if status != nil && *status != 0 {
		if msg != "" {
			return fmt.Sprintf("%d %s", *status, msg)
		}
		return fmt.Sprintf("%d status code (no body)", *status)
	}
	if msg != "" {
		return msg
	}
	return "(no status code or body)"
}

func orsBuildOpenAIStatusError(status int, rawText string, headers http.Header) error {
	var parsedErr any
	fallbackMsg := rawText
	if rawText != "" {
		if parsed, err := jsonx.ParseString(rawText); err == nil {
			fallbackMsg = ""
			if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
				if inner, hasInner := obj.Get("error"); hasInner && inner != nil {
					parsedErr = inner
				} else {
					parsedErr = obj
				}
			}
		}
	}
	st := status
	msg := orsMakeOpenAIStatusMessage(&st, parsedErr, fallbackMsg)
	errName := "APIError"
	switch status {
	case 400:
		errName = "BadRequestError"
	case 401:
		errName = "AuthenticationError"
	case 403:
		errName = "PermissionDeniedError"
	case 404:
		errName = "NotFoundError"
	case 409:
		errName = "ConflictError"
	case 422:
		errName = "UnprocessableEntityError"
	case 429:
		errName = "RateLimitError"
	default:
		if status >= 500 {
			errName = "InternalServerError"
		}
	}
	return &orsApiError{
		sdkErr: &ai.ProviderSdkError{
			ErrName:     errName,
			Status:      &st,
			ParsedError: parsedErr,
			Message:     msg,
		},
		headers: headers,
	}
}

func orsBuildOpenAIURL(cfg orsOpenAIHttpConfig) (string, error) {
	u, err := url.Parse(strings.TrimRight(cfg.BaseUrl, "/"))
	if err != nil {
		return "", err
	}
	path := cfg.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	if cfg.ApiVersion != "" {
		q := u.Query()
		q.Set("api-version", cfg.ApiVersion)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func orsDeleteHeaderCaseInsensitive(h http.Header, name string) {
	canonical := textproto.CanonicalMIMEHeaderKey(name)
	delete(h, canonical)
	lower := strings.ToLower(name)
	for k := range h {
		if strings.ToLower(k) == lower {
			delete(h, k)
		}
	}
}

func orsHasHeaderCaseInsensitive(headers ai.ProviderHeaders, names ...string) bool {
	if headers == nil {
		return false
	}
	for name, val := range headers.All() {
		lower := strings.ToLower(name)
		for _, target := range names {
			if lower == target && val != nil && js.Trim(*val) != "" {
				return true
			}
		}
	}
	return false
}

func orsMapToProviderHeaders(mapsIn ...map[string]string) ai.ProviderHeaders {
	out := omap.NewMap[string, *string]()
	for _, m := range mapsIn {
		for k, v := range m {
			val := v
			out.Set(k, &val)
		}
	}
	return out
}

func orsOrderedMapToProviderHeaders(m *omap.Map[string, string]) ai.ProviderHeaders {
	if m == nil || m.Len() == 0 {
		return nil
	}
	out := omap.NewMap[string, *string]()
	for k, v := range m.All() {
		val := v
		out.Set(k, &val)
	}
	return out
}

func orsCloneMapHeaders(m *omap.Map[string, string]) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}

func orsBuildOpenAIHeaders(cfg orsOpenAIHttpConfig, timeoutMs int64) http.Header {
	h := make(http.Header)
	clientUA := "OpenAI/Go " + orsOpenAISdkVersion
	if cfg.UseAzureApiKey {
		clientUA = "AzureOpenAI/Go " + orsOpenAISdkVersion
	}
	h.Set("Accept", "application/json")
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", clientUA)
	h.Set("X-Stainless-Retry-Count", "0")
	h.Set("X-Stainless-Timeout", strconv.FormatFloat(float64(timeoutMs)/1000, 'f', -1, 64))
	h.Set("X-Stainless-Lang", "go")
	h.Set("X-Stainless-Package-Version", orsOpenAISdkVersion)
	h.Set("X-Stainless-OS", orsNormalizedPlatform())
	h.Set("X-Stainless-Arch", orsNormalizedArch())
	h.Set("X-Stainless-Runtime", "go")
	h.Set("X-Stainless-Runtime-Version", runtime.Version())

	if cfg.ApiKey != "" {
		if cfg.UseAzureApiKey {
			h.Set("api-key", cfg.ApiKey)
		} else {
			h.Set("Authorization", "Bearer "+cfg.ApiKey)
		}
	}

	if cfg.DefaultHeaders != nil {
		for name, val := range cfg.DefaultHeaders.All() {
			orsDeleteHeaderCaseInsensitive(h, name)
			if val != nil {
				h.Set(name, *val)
			}
		}
	}
	return h
}

func orsDoOpenAIStreamRequest(
	ctx context.Context,
	cfg orsOpenAIHttpConfig,
) (*http.Response, error) {
	parentCtx := ai.OperationSignal(ctx)
	targetURL, err := orsBuildOpenAIURL(cfg)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := jsonx.Marshal(cfg.Body)
	if err != nil {
		return nil, err
	}

	timeoutMs := orsDefaultTimeoutMs
	if cfg.TimeoutMs != nil {
		timeoutMs = *cfg.TimeoutMs
	}

	client := cfg.HTTPClient
	if client == nil {
		client = ai.NodeHttpProxyDefaultClient()
	}

	maxRetries := 0
	if cfg.MaxRetries != nil {
		maxRetries = *cfg.MaxRetries
	}
	maxDelayMs := int64(60_000)
	if cfg.MaxRetryDelayMs != nil {
		maxDelayMs = *cfg.MaxRetryDelayMs
	}

	return ai.RetryProviderRequest(parentCtx, func(retryCtx context.Context) (*http.Response, error) {
		if retryCtx.Err() != nil {
			return nil, ai.AbortNewError("Request was aborted")
		}
		headers := orsBuildOpenAIHeaders(cfg, timeoutMs)

		attemptClient := client
		if cfg.UseAzureApiKey && headers.Get("api-key") != "" && attemptClient.CheckRedirect == nil {
			// Match OpenAI JS SDK Azure client: disable redirects when sending static api-key.
			cp := *attemptClient
			cp.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			attemptClient = &cp
		}

		reqCtx, cancelReq := context.WithCancel(retryCtx)
		var timedOut atomic.Bool
		var timer *time.Timer
		if timeoutMs >= 0 {
			timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
				timedOut.Store(true)
				cancelReq()
			})
		}

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			if timer != nil {
				timer.Stop()
			}
			cancelReq()
			return nil, err
		}
		req.Header = maps.Clone(headers)

		resp, err := attemptClient.Do(req)
		if err != nil {
			if timer != nil {
				timer.Stop()
			}
			cancelReq()
			if retryCtx.Err() != nil {
				return nil, ai.AbortNewError("Request was aborted")
			}
			if timedOut.Load() {
				return nil, &orsConnectionError{errName: "APIConnectionTimeoutError", message: "Request timed out."}
			}
			return nil, &orsConnectionError{errName: "APIConnectionError", message: "Connection error.", cause: err}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			rawBody, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if timer != nil {
				timer.Stop()
			}
			cancelReq()
			if retryCtx.Err() != nil {
				return nil, ai.AbortNewError("Request was aborted")
			}
			if timedOut.Load() {
				return nil, &orsConnectionError{errName: "APIConnectionTimeoutError", message: "Request timed out."}
			}
			bodyText := ""
			if readErr == nil {
				bodyText = string(rawBody)
			}
			return nil, orsBuildOpenAIStatusError(resp.StatusCode, bodyText, resp.Header.Clone())
		}

		// Headers received: disarm header/error timeout just like OpenAI SDK fetchWithTimeout finally{clearTimeout}.
		if timer != nil {
			timer.Stop()
		}
		resp.Body = &orsCancelReadCloser{ReadCloser: resp.Body, cancel: cancelReq}
		return resp, nil
	}, &ai.ProviderRetryOptions{
		MaxRetries:      &maxRetries,
		MaxRetryDelayMs: &maxDelayMs,
	})
}

type orsCancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *orsCancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func orsIterOpenAISSEEvents(ctx context.Context, body io.Reader, respHeaders http.Header) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		reader := sse.NewReader(body)
		for {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			ev, err := reader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if ctx != nil && ctx.Err() != nil {
					return
				}
				yield(nil, err)
				return
			}
			if ev.Data == "[DONE]" {
				return
			}
			parsed, parseErr := jsonx.ParseString(ev.Data)
			if parseErr != nil {
				yield(nil, errors.New("Error reading response: malformed server-sent event JSON."))
				return
			}
			obj, _ := parsed.(*jsonx.Object)
			if ev.Event == "error" {
				var errVal any = parsed
				if obj != nil {
					if inner, hasInner := obj.Get("error"); hasInner && inner != nil {
						errVal = inner
					}
				}
				msg := orsMakeOpenAIStatusMessage(nil, errVal, "")
				yield(nil, &orsApiError{
					sdkErr: &ai.ProviderSdkError{
						ErrName:     "APIError",
						ParsedError: errVal,
						Message:     msg,
					},
					headers: respHeaders,
				})
				return
			}
			if obj != nil {
				if inner, hasInner := obj.Get("error"); hasInner && inner != nil && inner != false && inner != "" {
					msg := orsMakeOpenAIStatusMessage(nil, inner, "")
					yield(nil, &orsApiError{
						sdkErr: &ai.ProviderSdkError{
							ErrName:     "APIError",
							ParsedError: inner,
							Message:     msg,
						},
						headers: respHeaders,
					})
					return
				}
			}
			if obj == nil {
				continue
			}
			if !yield(obj, nil) {
				return
			}
		}
	}
}

func orsStrPtr(s string) *string { return &s }

func orsClampOpenAIReasoningEffort(model *ai.Model, level ai.ModelThinkingLevel) string {
	key := string(level)
	if model != nil && model.ThinkingLevelMap != nil {
		switch level {
		case ai.ModelThinkingLevelOff:
			if v, ok := model.ThinkingLevelMap.Off.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelMinimal:
			if v, ok := model.ThinkingLevelMap.Minimal.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelLow:
			if v, ok := model.ThinkingLevelMap.Low.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelMedium:
			if v, ok := model.ThinkingLevelMap.Medium.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelHigh:
			if v, ok := model.ThinkingLevelMap.High.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelXhigh:
			if v, ok := model.ThinkingLevelMap.Xhigh.Get(); ok {
				return v
			}
		case ai.ModelThinkingLevelMax:
			if v, ok := model.ThinkingLevelMap.Max.Get(); ok {
				return v
			}
		}
	}
	return key
}
