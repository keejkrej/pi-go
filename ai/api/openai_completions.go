// Ported from packages/ai/src/api/openai-completions.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

var ocSanitizeToolCallIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// OpenAICompletionsOptions configures OpenAI Chat Completions streaming requests.
type OpenAICompletionsOptions struct {
	ai.StreamOptions
	ToolChoice      any                 `json:"toolChoice,omitzero"`
	ReasoningEffort *ai.ThinkingLevel   `json:"reasoningEffort,omitzero"`
	ThinkingBudgets *ai.ThinkingBudgets `json:"thinkingBudgets,omitzero"`
}

// ConvertCompletionsMessagesOptions configures message conversion for OpenAI Chat Completions.
type ConvertCompletionsMessagesOptions struct {
	GrammarToolInputProperties map[string]string `json:"grammarToolInputProperties,omitzero"`
}

type ocResolvedCompat struct {
	supportsStore                               bool
	supportsDeveloperRole                       bool
	supportsReasoningEffort                     bool
	supportsUsageInStreaming                    bool
	supportsFinishReason                        bool
	maxTokensField                              ai.MaxTokensField
	requiresToolResultName                      bool
	requiresAssistantAfterToolResult            bool
	requiresNonNullAssistantContent             bool
	requiresToolsWithToolHistory                bool
	requiresThinkingAsText                      bool
	requiresReasoningContentOnAssistantMessages bool
	thinkingFormat                              ai.ThinkingFormat
	chatTemplateKwargs                          *omap.Map[string, ai.ChatTemplateKwargValue]
	chatTemplateArgs                            *omap.Map[string, ai.ChatTemplateKwargValue]
	zaiToolStream                               bool
	supportsThinkingTokenBudget                 bool
	thinkingTokenBudgetField                    ai.ThinkingTokenBudgetField
	supportsOpenAIGrammarTools                  bool
	supportsMidConvoSystemMessages              bool
	supportsMidConvoToolAdditions               bool
	supportsStrictMode                          bool
	cacheControlFormat                          ai.CacheControlFormat
	sendSessionAffinityHeaders                  bool
	sessionAffinityFormat                       ai.SessionAffinityFormat
	promptCacheKeyMode                          ai.PromptCacheKeyMode
	supportsLongCacheRetention                  bool
	vllmPriority                                *float64
	openRouterRouting                           *ai.OpenRouterRouting
	vercelGatewayRouting                        *ai.VercelGatewayRouting
}

func ocHasToolHistory(messages []ai.Message) bool {
	for _, msg := range messages {
		switch m := msg.(type) {
		case *ai.ToolResultMessage:
			if m != nil {
				return true
			}
		case *ai.AssistantMessage:
			if m == nil {
				continue
			}
			for _, blk := range m.Content {
				if _, ok := blk.(*ai.ToolCall); ok {
					return true
				}
			}
		}
	}
	return false
}

func ocHasValidCommonReasoningDetailFields(obj *jsonx.Object) bool {
	if obj == nil {
		return false
	}
	if id, has := obj.Get("id"); has && id != nil {
		if _, ok := id.(string); !ok {
			return false
		}
	}
	if format, has := obj.Get("format"); has {
		if _, ok := format.(string); !ok {
			return false
		}
	}
	if obj.Has("index") {
		if _, ok := obj.GetNumber("index"); !ok {
			return false
		}
	}
	return true
}

func ocIsOpenAIReasoningDetail(obj *jsonx.Object) bool {
	if !ocHasValidCommonReasoningDetailFields(obj) {
		return false
	}
	typ, _ := obj.GetString("type")
	switch typ {
	case "reasoning.summary":
		_, ok := obj.GetString("summary")
		return ok
	case "reasoning.encrypted":
		_, ok := obj.GetString("data")
		return ok
	case "reasoning.text":
		if _, ok := obj.GetString("text"); !ok {
			return false
		}
		if sig, has := obj.Get("signature"); has && sig != nil {
			if _, ok := sig.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func ocParseOpenAIReasoningDetails(signature *string) []*jsonx.Object {
	if signature == nil || *signature == "" {
		return nil
	}
	parsed, err := jsonx.ParseString(*signature)
	if err != nil {
		return nil
	}
	arr, ok := parsed.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]*jsonx.Object, 0, len(arr))
	for _, elem := range arr {
		obj, ok := elem.(*jsonx.Object)
		if !ok || !ocIsOpenAIReasoningDetail(obj) {
			return nil
		}
		out = append(out, obj)
	}
	return out
}

func ocParseLegacyEncryptedReasoningDetail(signature *string) *jsonx.Object {
	if signature == nil || *signature == "" {
		return nil
	}
	parsed, err := jsonx.ParseString(*signature)
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || !ocIsOpenAIReasoningDetail(obj) {
		return nil
	}
	typ, _ := obj.GetString("type")
	if typ != "reasoning.encrypted" {
		return nil
	}
	id, ok := obj.GetString("id")
	if !ok || id == "" {
		return nil
	}
	data, ok := obj.GetString("data")
	if !ok || data == "" {
		return nil
	}
	return obj
}

func ocFillMissingCommonReasoningDetailFields(target *jsonx.Object, source *jsonx.Object) {
	if target == nil || source == nil {
		return
	}
	if v, has := target.Get("id"); !has || v == nil {
		if sv, hasSrc := source.Get("id"); hasSrc {
			target.Set("id", sv)
		}
	}
	if f, has := target.GetString("format"); !has || f == "" {
		if sf, hasSrc := source.GetString("format"); hasSrc && sf != "" {
			target.Set("format", sf)
		}
	}
	if v, has := target.Get("index"); !has || v == nil {
		if si, hasSrc := source.Get("index"); hasSrc {
			target.Set("index", si)
		}
	}
}

func ocAppendOpenAIReasoningDetail(details []*jsonx.Object, detail *jsonx.Object) []*jsonx.Object {
	if detail == nil {
		return details
	}
	typ, _ := detail.GetString("type")
	if len(details) > 0 {
		last := details[len(details)-1]
		lastType, _ := last.GetString("type")
		if typ == "reasoning.text" && lastType == "reasoning.text" {
			lastText, _ := last.GetString("text")
			addText, _ := detail.GetString("text")
			last.Set("text", lastText+addText)
			if sig, ok := last.GetString("signature"); !ok || sig == "" {
				if srcSig, okSrc := detail.Get("signature"); okSrc && srcSig != nil {
					if s, isStr := srcSig.(string); !isStr || s != "" {
						last.Set("signature", srcSig)
					}
				}
			}
			ocFillMissingCommonReasoningDetailFields(last, detail)
			return details
		}
		if typ == "reasoning.summary" && lastType == "reasoning.summary" {
			lastSummary, _ := last.GetString("summary")
			addSummary, _ := detail.GetString("summary")
			last.Set("summary", lastSummary+addSummary)
			ocFillMissingCommonReasoningDetailFields(last, detail)
			return details
		}
	}
	return append(details, detail.Clone())
}

func ocIsOpenAICompletionsReasoningField(field string) bool {
	return field == "reasoning" || field == "reasoning_content" || field == "reasoning_text"
}

func ocExtractOptions(options ai.ProviderStreamOptions) *OpenAICompletionsOptions {
	if options == nil {
		return &OpenAICompletionsOptions{}
	}
	if o, ok := options.(*OpenAICompletionsOptions); ok && o != nil {
		cp := *o
		return &cp
	}
	var out OpenAICompletionsOptions
	if base := options.BaseStreamOptions(); base != nil {
		out.StreamOptions = *base
	}
	return &out
}

func ocDetectCompat(model *ai.Model) ocResolvedCompat {
	var provider string
	var baseUrl string
	var modelId string
	if model != nil {
		provider = string(model.Provider)
		baseUrl = model.BaseUrl
		modelId = model.Id
	}

	isZai := provider == "zai" ||
		provider == "zai-coding-cn" ||
		strings.Contains(baseUrl, "api.z.ai") ||
		strings.Contains(baseUrl, "open.bigmodel.cn")
	isTogether := provider == "together" ||
		strings.Contains(baseUrl, "api.together.ai") ||
		strings.Contains(baseUrl, "api.together.xyz")
	isMoonshot := provider == "moonshotai" ||
		provider == "moonshotai-cn" ||
		strings.Contains(baseUrl, "api.moonshot.")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseUrl, "openrouter.ai")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseUrl, "api.cloudflare.com")
	isCloudflareAiGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseUrl, "gateway.ai.cloudflare.com")
	isNvidia := provider == "nvidia" || strings.Contains(baseUrl, "integrate.api.nvidia.com")
	isAntLing := provider == "ant-ling" || strings.Contains(baseUrl, "api.ant-ling.com")
	isCerebras := provider == "cerebras" || strings.Contains(baseUrl, "cerebras.ai")
	isDeepSeek := provider == "deepseek" || strings.Contains(strings.ToLower(baseUrl), "deepseek.com")
	isGrok := provider == "xai" || strings.Contains(baseUrl, "api.x.ai")

	isNonStandard := isNvidia ||
		isCerebras ||
		isGrok ||
		isTogether ||
		strings.Contains(baseUrl, "chutes.ai") ||
		isDeepSeek ||
		isZai ||
		isMoonshot ||
		provider == "opencode" ||
		strings.Contains(baseUrl, "opencode.ai") ||
		isCloudflareWorkersAI ||
		isCloudflareAiGateway ||
		isAntLing

	useMaxTokens := strings.Contains(baseUrl, "chutes.ai") ||
		isDeepSeek ||
		isMoonshot ||
		isCloudflareAiGateway ||
		isTogether ||
		isNvidia ||
		isAntLing ||
		isZai

	isOpenRouterDeveloperRoleModel := isOpenRouter && (strings.HasPrefix(modelId, "anthropic/") || strings.HasPrefix(modelId, "openai/"))
	var cacheControlFormat ai.CacheControlFormat
	if provider == "openrouter" && strings.HasPrefix(modelId, "anthropic/") {
		cacheControlFormat = ai.CacheControlFormatAnthropic
	}

	maxTokensField := ai.MaxTokensFieldMaxCompletionTokens
	if useMaxTokens {
		maxTokensField = ai.MaxTokensFieldMaxTokens
	}

	thinkingFormat := ai.ThinkingFormatOpenai
	switch {
	case isDeepSeek:
		thinkingFormat = ai.ThinkingFormatDeepseek
	case isZai:
		thinkingFormat = ai.ThinkingFormatZai
	case isTogether:
		thinkingFormat = ai.ThinkingFormatTogether
	case isAntLing:
		thinkingFormat = ai.ThinkingFormatAntLing
	case isOpenRouter:
		thinkingFormat = ai.ThinkingFormatOpenrouter
	}

	sessionAffinityFormat := ai.SessionAffinityFormatOpenai
	if isOpenRouter {
		sessionAffinityFormat = ai.SessionAffinityFormatOpenrouter
	}

	return ocResolvedCompat{
		supportsStore:                               !isNonStandard,
		supportsDeveloperRole:                       isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter),
		supportsReasoningEffort:                     !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia && !isAntLing,
		supportsUsageInStreaming:                    true,
		supportsFinishReason:                        true,
		maxTokensField:                              maxTokensField,
		requiresReasoningContentOnAssistantMessages: isDeepSeek,
		thinkingFormat:                              thinkingFormat,
		cacheControlFormat:                          cacheControlFormat,
		sendSessionAffinityHeaders:                  isOpenRouter,
		sessionAffinityFormat:                       sessionAffinityFormat,
		supportsLongCacheRetention:                  !(isTogether || isCloudflareWorkersAI || isCloudflareAiGateway || isNvidia || isAntLing),
	}
}

func ocApplyCompatStruct(out *ocResolvedCompat, c *ai.OpenAICompletionsCompat) {
	if c == nil {
		return
	}
	if c.SupportsStore != nil {
		out.supportsStore = *c.SupportsStore
	}
	if c.SupportsDeveloperRole != nil {
		out.supportsDeveloperRole = *c.SupportsDeveloperRole
	}
	if c.SupportsReasoningEffort != nil {
		out.supportsReasoningEffort = *c.SupportsReasoningEffort
	}
	if c.SupportsUsageInStreaming != nil {
		out.supportsUsageInStreaming = *c.SupportsUsageInStreaming
	}
	if c.SupportsFinishReason != nil {
		out.supportsFinishReason = *c.SupportsFinishReason
	}
	if c.MaxTokensField != nil && *c.MaxTokensField != "" {
		out.maxTokensField = *c.MaxTokensField
	}
	if c.RequiresToolResultName != nil {
		out.requiresToolResultName = *c.RequiresToolResultName
	}
	if c.RequiresAssistantAfterToolResult != nil {
		out.requiresAssistantAfterToolResult = *c.RequiresAssistantAfterToolResult
	}
	if c.RequiresNonNullAssistantContent != nil {
		out.requiresNonNullAssistantContent = *c.RequiresNonNullAssistantContent
	}
	if c.RequiresToolsWithToolHistory != nil {
		out.requiresToolsWithToolHistory = *c.RequiresToolsWithToolHistory
	}
	if c.RequiresThinkingAsText != nil {
		out.requiresThinkingAsText = *c.RequiresThinkingAsText
	}
	if c.RequiresReasoningContentOnAssistantMessages != nil {
		out.requiresReasoningContentOnAssistantMessages = *c.RequiresReasoningContentOnAssistantMessages
	}
	if c.ThinkingFormat != nil && *c.ThinkingFormat != "" {
		out.thinkingFormat = *c.ThinkingFormat
	}
	if c.ChatTemplateKwargs != nil {
		out.chatTemplateKwargs = c.ChatTemplateKwargs
	}
	if c.ChatTemplateArgs != nil {
		out.chatTemplateArgs = c.ChatTemplateArgs
	}
	if c.ZaiToolStream != nil {
		out.zaiToolStream = *c.ZaiToolStream
	}
	if c.SupportsThinkingTokenBudget != nil {
		out.supportsThinkingTokenBudget = *c.SupportsThinkingTokenBudget
	}
	if c.ThinkingTokenBudgetField != nil && *c.ThinkingTokenBudgetField != "" {
		out.thinkingTokenBudgetField = *c.ThinkingTokenBudgetField
	}
	if c.SupportsOpenAIGrammarTools != nil {
		out.supportsOpenAIGrammarTools = *c.SupportsOpenAIGrammarTools
	}
	if c.SupportsMidConvoSystemMessages != nil {
		out.supportsMidConvoSystemMessages = *c.SupportsMidConvoSystemMessages
	}
	if c.SupportsMidConvoToolAdditions != nil {
		out.supportsMidConvoToolAdditions = *c.SupportsMidConvoToolAdditions
	}
	if c.SupportsStrictMode != nil {
		out.supportsStrictMode = *c.SupportsStrictMode
	}
	if c.CacheControlFormat != nil {
		out.cacheControlFormat = *c.CacheControlFormat
	}
	if c.SendSessionAffinityHeaders != nil {
		out.sendSessionAffinityHeaders = *c.SendSessionAffinityHeaders
	}
	if c.SessionAffinityFormat != nil && *c.SessionAffinityFormat != "" {
		out.sessionAffinityFormat = *c.SessionAffinityFormat
	}
	if c.PromptCacheKeyMode != nil && *c.PromptCacheKeyMode != "" {
		out.promptCacheKeyMode = *c.PromptCacheKeyMode
	}
	if c.SupportsLongCacheRetention != nil {
		out.supportsLongCacheRetention = *c.SupportsLongCacheRetention
	}
	if c.VllmPriority != nil {
		out.vllmPriority = c.VllmPriority
	}
	if c.OpenRouterRouting != nil {
		out.openRouterRouting = c.OpenRouterRouting
	}
	if c.VercelGatewayRouting != nil {
		out.vercelGatewayRouting = c.VercelGatewayRouting
	}
}

func ocGetCompat(model *ai.Model) ocResolvedCompat {
	out := ocDetectCompat(model)
	if model == nil || model.Compat.V == nil {
		return out
	}
	var rawCompat *ai.OpenAICompletionsCompat
	switch c := model.Compat.V.(type) {
	case *ai.OpenAICompletionsCompat:
		rawCompat = c
	case *ai.UnknownModelCompat:
		if c != nil && c.Raw != nil {
			var decoded ai.OpenAICompletionsCompat
			if err := jsonx.Decode(c.Raw, &decoded); err == nil {
				rawCompat = &decoded
			}
		}
	}
	ocApplyCompatStruct(&out, rawCompat)
	return out
}

func ocCreateHeaders(
	model *ai.Model,
	c *ai.TranscriptContext,
	compat ocResolvedCompat,
	optionsHeaders ai.ProviderHeaders,
	identity *ai.ClientIdentity,
	sessionId string,
) ai.ProviderHeaders {
	out := omap.NewMap[string, *string]()
	var target ai.ClientIdentityTarget
	if model != nil {
		target = ai.ClientIdentityTarget{Provider: string(model.Provider), BaseUrl: model.BaseUrl}
	}
	for k, v := range ai.GetIdentityHeaders(target, identity).All() {
		val := v
		out.Set(k, &val)
	}
	if model != nil && model.Headers != nil {
		for k, v := range model.Headers.All() {
			val := v
			out.Set(k, &val)
		}
	}
	if model != nil && model.Provider == ai.KnownProviderGithubCopilot {
		var msgs []ai.Message
		if c != nil {
			msgs = c.Messages
		}
		copilotHeaders := BuildCopilotDynamicHeaders(CopilotDynamicHeadersParams{
			Messages:  msgs,
			HasImages: HasCopilotVisionInput(msgs),
		})
		for k, v := range copilotHeaders {
			val := v
			out.Set(k, &val)
		}
	}
	if sessionId != "" && compat.sendSessionAffinityHeaders {
		if compat.sessionAffinityFormat == ai.SessionAffinityFormatOpenrouter {
			val := sessionId
			out.Set("x-session-id", &val)
		} else {
			if compat.sessionAffinityFormat == ai.SessionAffinityFormatOpenai {
				val := sessionId
				out.Set("session_id", &val)
			}
			reqID := sessionId
			aff := sessionId
			out.Set("x-client-request-id", &reqID)
			out.Set("x-session-affinity", &aff)
		}
	}
	if optionsHeaders != nil {
		for k, v := range optionsHeaders.All() {
			out.Set(k, v)
		}
	}
	return out
}

func ocResolveThinkingTokenBudgetField(compat ocResolvedCompat) ai.ThinkingTokenBudgetField {
	if compat.thinkingTokenBudgetField != "" {
		return compat.thinkingTokenBudgetField
	}
	if compat.supportsThinkingTokenBudget {
		return ai.ThinkingTokenBudgetFieldThinkingTokenBudget
	}
	return ""
}

func ocResolveClampedThinkingBudget(
	model *ai.Model,
	options *OpenAICompletionsOptions,
	maxTokensFieldVal *int,
) *int {
	if model == nil || !model.Reasoning || options == nil || options.ReasoningEffort == nil || *options.ReasoningEffort == "" {
		return nil
	}
	ceiling := model.MaxTokens
	if maxTokensFieldVal != nil {
		ceiling = *maxTokensFieldVal
	}
	budget := ClampThinkingBudgetToAnswerRoom(
		ThinkingBudgetForLevel(*options.ReasoningEffort, options.ThinkingBudgets),
		ceiling,
	)
	if budget > 0 {
		return &budget
	}
	return nil
}

// ocLookupThinkingLevel returns (mappedString, isExplicitNull, isMapped).
func ocLookupThinkingLevel(model *ai.Model, effort *ai.ThinkingLevel) (string, bool, bool) {
	if model == nil || model.ThinkingLevelMap == nil {
		return "", false, false
	}
	var opt jsonx.Opt[string]
	if effort == nil || *effort == "" {
		opt = model.ThinkingLevelMap.Off
	} else {
		switch *effort {
		case ai.ThinkingLevelMinimal:
			opt = model.ThinkingLevelMap.Minimal
		case ai.ThinkingLevelLow:
			opt = model.ThinkingLevelMap.Low
		case ai.ThinkingLevelMedium:
			opt = model.ThinkingLevelMap.Medium
		case ai.ThinkingLevelHigh:
			opt = model.ThinkingLevelMap.High
		case ai.ThinkingLevelXhigh:
			opt = model.ThinkingLevelMap.Xhigh
		case ai.ThinkingLevelMax:
			opt = model.ThinkingLevelMap.Max
		}
	}
	if opt.IsNull() {
		return "", true, true
	}
	if s, ok := opt.Get(); ok {
		return s, false, true
	}
	return "", false, false
}

func ocResolveChatTemplateKwargValue(
	model *ai.Model,
	options *OpenAICompletionsOptions,
	val ai.ChatTemplateKwargValue,
	thinkingBudget *int,
) (any, bool) {
	if val == nil {
		return nil, true
	}
	switch v := val.(type) {
	case ai.ChatTemplateKwargString:
		return string(v), true
	case ai.ChatTemplateKwargNumber:
		return float64(v), true
	case ai.ChatTemplateKwargBool:
		return bool(v), true
	case ai.ChatTemplateKwargNull:
		return nil, true
	case *ai.ChatTemplateKwargVar:
		if v == nil {
			return nil, true
		}
		var effort *ai.ThinkingLevel
		if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
			effort = options.ReasoningEffort
		}
		if effort == nil && v.OmitWhenOff != nil && *v.OmitWhenOff {
			return nil, false
		}
		switch v.Var {
		case ai.ChatTemplateKwargVarNameThinkingEnabled:
			return effort != nil, true
		case ai.ChatTemplateKwargVarNameThinkingBudget:
			if thinkingBudget == nil {
				return nil, false
			}
			return *thinkingBudget, true
		default:
			mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
			if !isMapped {
				if effort == nil {
					return nil, false
				}
				return string(*effort), true
			}
			if isNull {
				return nil, false
			}
			return mapped, true
		}
	default:
		return val, true
	}
}

func ocBuildChatTemplateValues(
	model *ai.Model,
	options *OpenAICompletionsOptions,
	values *omap.Map[string, ai.ChatTemplateKwargValue],
	thinkingBudget *int,
) *jsonx.Object {
	if values == nil || values.Len() == 0 {
		return nil
	}
	resolved := jsonx.NewObject()
	for k, v := range values.All() {
		rv, keep := ocResolveChatTemplateKwargValue(model, options, v, thinkingBudget)
		if keep {
			resolved.Set(k, rv)
		}
	}
	if resolved.Len() == 0 {
		return nil
	}
	return resolved
}

func ocGetCompatCacheControl(compat ocResolvedCompat, cacheRetention ai.CacheRetention) *jsonx.Object {
	if compat.cacheControlFormat != ai.CacheControlFormatAnthropic || cacheRetention == ai.CacheRetentionNone {
		return nil
	}
	obj := jsonx.NewObject()
	obj.Set("type", "ephemeral")
	if cacheRetention == ai.CacheRetentionLong && compat.supportsLongCacheRetention {
		obj.Set("ttl", "1h")
	}
	return obj
}

func ocAddCacheControlToTextContent(msgObj *jsonx.Object, cacheControl *jsonx.Object) bool {
	if msgObj == nil {
		return false
	}
	contentVal, has := msgObj.Get("content")
	if !has || contentVal == nil {
		return false
	}
	if s, ok := contentVal.(string); ok {
		if len(s) == 0 {
			return false
		}
		part := jsonx.NewObject()
		part.Set("type", "text")
		part.Set("text", s)
		part.Set("cache_control", cacheControl.Clone())
		msgObj.Set("content", []*jsonx.Object{part})
		return true
	}
	if parts, ok := contentVal.([]*jsonx.Object); ok {
		for i := len(parts) - 1; i >= 0; i-- {
			part := parts[i]
			if part == nil {
				continue
			}
			if typ, _ := part.GetString("type"); typ == "text" {
				part.Set("cache_control", cacheControl.Clone())
				return true
			}
		}
	}
	return false
}

func ocApplyAnthropicCacheControl(messages []*jsonx.Object, tools []*jsonx.Object, cacheControl *jsonx.Object) {
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		role, _ := msg.GetString("role")
		if role == "system" || role == "developer" {
			ocAddCacheControlToTextContent(msg, cacheControl)
			break
		}
	}
	if len(tools) > 0 && tools[len(tools)-1] != nil {
		tools[len(tools)-1].Set("cache_control", cacheControl.Clone())
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}
		role, _ := msg.GetString("role")
		if role == "user" || role == "assistant" || role == "tool" {
			if ocAddCacheControlToTextContent(msg, cacheControl) {
				break
			}
		}
	}
}

func ocNormalizeToolCallID(id string, model *ai.Model) string {
	if strings.Contains(id, "|") {
		sep := strings.IndexByte(id, '|')
		callID := ocSanitizeToolCallIDRe.ReplaceAllString(id[:sep], "_")
		itemID := ocSanitizeToolCallIDRe.ReplaceAllString(id[sep+1:], "_")
		combinedID := callID
		if len(itemID) > 0 {
			combinedID = callID + "_" + itemID
		}
		if len(combinedID) <= 40 {
			return combinedID
		}
		hash := ai.ShortHash(id)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		prefixLen := max(1, 40-len(hash)-1)
		prefix := callID
		if len(prefix) > prefixLen {
			prefix = prefix[:prefixLen]
		}
		return prefix + "_" + hash
	}
	if model != nil && model.Provider == ai.KnownProviderOpenai {
		if js.Len(id) > 40 {
			return js.Slice(id, 0, 40)
		}
	}
	return id
}

func ocConvertMessagesResolved(
	model *ai.Model,
	c *ai.TranscriptContext,
	compat ocResolvedCompat,
	options *ConvertCompletionsMessagesOptions,
) ([]*jsonx.Object, error) {
	normalizedContext := ai.ResolveTranscript(c, &compat.supportsMidConvoSystemMessages)
	params := make([]*jsonx.Object, 0)

	transformedMessages := TransformMessages(normalizedContext.Messages, model, func(id string, _ *ai.Model, _ *ai.AssistantMessage) string {
		return ocNormalizeToolCallID(id, model)
	})
	support := ai.TranscriptToolSupportNone
	if compat.supportsMidConvoSystemMessages && compat.supportsMidConvoToolAdditions {
		support = ai.TranscriptToolSupportAdditions
	}
	transcriptTools := ai.ResolveTranscriptTools(normalizedContext.Messages, support)

	instructionRole := "system"
	if model != nil && model.Reasoning && compat.supportsDeveloperRole {
		instructionRole = "developer"
	}

	var lastRole string
	systemIndex := 0
	var grammarProps map[string]string
	if options != nil {
		grammarProps = options.GrammarToolInputProperties
	}

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		if msg == nil {
			continue
		}
		role := msg.GetRole()
		if compat.requiresAssistantAfterToolResult && lastRole == "toolResult" && role == "user" {
			bridge := jsonx.NewObject()
			bridge.Set("role", "assistant")
			bridge.Set("content", "I have processed the tool results.")
			params = append(params, bridge)
		}

		switch m := msg.(type) {
		case *ai.SystemMessage:
			var addedTools []*ai.Tool
			if systemIndex >= transcriptTools.FoldedSystemMessages {
				addedTools = m.ToolsAdded
			}
			systemIndex++
			if len(addedTools) > 0 {
				convertedTools, err := ocConvertTools(addedTools, compat)
				if err != nil {
					return nil, err
				}
				kimiMsg := jsonx.NewObject()
				kimiMsg.Set("role", "system")
				kimiMsg.Set("tools", convertedTools)
				params = append(params, kimiMsg)
			}
			var text string
			if i == 0 {
				text = ai.GetSystemMessageText(m)
			} else {
				text = ai.RenderSystemMessageUpdate(m)
			}
			if len(text) > 0 {
				sysMsg := jsonx.NewObject()
				sysMsg.Set("role", instructionRole)
				sysMsg.Set("content", ai.SanitizeSurrogates(text))
				params = append(params, sysMsg)
			}

		case *ai.UserMessage:
			if m.Content.Text != nil {
				userMsg := jsonx.NewObject()
				userMsg.Set("role", "user")
				userMsg.Set("content", ai.SanitizeSurrogates(*m.Content.Text))
				params = append(params, userMsg)
			} else if len(m.Content.Blocks) > 0 {
				var parts []*jsonx.Object
				for _, item := range m.Content.Blocks {
					switch blk := item.(type) {
					case *ai.TextContent:
						if blk != nil && len(blk.Text) > 0 {
							part := jsonx.NewObject()
							part.Set("type", "text")
							part.Set("text", ai.SanitizeSurrogates(blk.Text))
							parts = append(parts, part)
						}
					case *ai.ImageContent:
						if blk != nil {
							imgURL := jsonx.NewObject()
							imgURL.Set("url", fmt.Sprintf("data:%s;base64,%s", blk.MimeType, blk.Data))
							part := jsonx.NewObject()
							part.Set("type", "image_url")
							part.Set("image_url", imgURL)
							parts = append(parts, part)
						}
					}
				}
				if len(parts) == 0 {
					continue
				}
				userMsg := jsonx.NewObject()
				userMsg.Set("role", "user")
				userMsg.Set("content", parts)
				params = append(params, userMsg)
			}

		case *ai.AssistantMessage:
			assistantMsg := jsonx.NewObject()
			assistantMsg.Set("role", "assistant")
			if compat.requiresAssistantAfterToolResult || compat.requiresNonNullAssistantContent {
				assistantMsg.Set("content", "")
			} else {
				assistantMsg.Set("content", nil)
			}

			var assistantTextParts []*jsonx.Object
			var textBuilder strings.Builder
			var thinkingBlocks []*ai.ThinkingContent
			var nonEmptyThinkingBlocks []*ai.ThinkingContent
			var toolCalls []*ai.ToolCall

			for _, blk := range m.Content {
				switch b := blk.(type) {
				case *ai.TextContent:
					if b != nil && len(js.Trim(b.Text)) > 0 {
						sanitized := ai.SanitizeSurrogates(b.Text)
						part := jsonx.NewObject()
						part.Set("type", "text")
						part.Set("text", sanitized)
						assistantTextParts = append(assistantTextParts, part)
						textBuilder.WriteString(sanitized)
					}
				case *ai.ThinkingContent:
					if b != nil {
						thinkingBlocks = append(thinkingBlocks, b)
						if len(js.Trim(b.Thinking)) > 0 {
							nonEmptyThinkingBlocks = append(nonEmptyThinkingBlocks, b)
						}
					}
				case *ai.ToolCall:
					if b != nil {
						toolCalls = append(toolCalls, b)
					}
				}
			}
			assistantText := textBuilder.String()

			var signedReasoningDetails []*jsonx.Object
			for _, tb := range thinkingBlocks {
				if parsed := ocParseOpenAIReasoningDetails(tb.ThinkingSignature); parsed != nil {
					signedReasoningDetails = parsed
					break
				}
			}
			var legacyReasoningDetails []*jsonx.Object
			for _, tc := range toolCalls {
				if parsed := ocParseLegacyEncryptedReasoningDetail(tc.ThoughtSignature); parsed != nil {
					legacyReasoningDetails = append(legacyReasoningDetails, parsed)
				}
			}
			preservedReasoningDetails := signedReasoningDetails
			if preservedReasoningDetails == nil && len(legacyReasoningDetails) > 0 {
				preservedReasoningDetails = legacyReasoningDetails
			}

			if len(nonEmptyThinkingBlocks) > 0 {
				if compat.requiresThinkingAsText {
					thinkingParts := make([]string, 0, len(nonEmptyThinkingBlocks))
					for _, tb := range nonEmptyThinkingBlocks {
						thinkingParts = append(thinkingParts, ai.SanitizeSurrogates(tb.Thinking))
					}
					thinkingPart := jsonx.NewObject()
					thinkingPart.Set("type", "text")
					thinkingPart.Set("text", strings.Join(thinkingParts, "\n\n"))
					combined := make([]*jsonx.Object, 0, 1+len(assistantTextParts))
					combined = append(combined, thinkingPart)
					combined = append(combined, assistantTextParts...)
					assistantMsg.Set("content", combined)
				} else {
					if len(assistantText) > 0 {
						assistantMsg.Set("content", assistantText)
					}
					if preservedReasoningDetails == nil {
						var sig string
						if nonEmptyThinkingBlocks[0].ThinkingSignature != nil {
							sig = *nonEmptyThinkingBlocks[0].ThinkingSignature
						}
						if model != nil && model.Provider == ai.KnownProviderOpencodeGo && sig == "reasoning" {
							sig = "reasoning_content"
						}
						if sig != "" && ocIsOpenAICompletionsReasoningField(sig) {
							rawThinkParts := make([]string, 0, len(nonEmptyThinkingBlocks))
							for _, tb := range nonEmptyThinkingBlocks {
								rawThinkParts = append(rawThinkParts, tb.Thinking)
							}
							assistantMsg.Set(sig, strings.Join(rawThinkParts, "\n"))
						}
					}
				}
			} else if len(assistantText) > 0 {
				assistantMsg.Set("content", assistantText)
			}

			if len(toolCalls) > 0 {
				convertedCalls := make([]*jsonx.Object, 0, len(toolCalls))
				for _, tc := range toolCalls {
					tcObj := jsonx.NewObject()
					tcObj.Set("id", tc.Id)
					if prop, isCustom := grammarProps[tc.Name]; isCustom {
						inputStr, err := GetGrammarToolInput(tc.Name, tc.Arguments, prop)
						if err != nil {
							return nil, err
						}
						customObj := jsonx.NewObject()
						customObj.Set("name", tc.Name)
						customObj.Set("input", ai.SanitizeSurrogates(inputStr))
						tcObj.Set("type", "custom")
						tcObj.Set("custom", customObj)
					} else {
						argsObj := tc.Arguments
						if argsObj == nil {
							argsObj = jsonx.NewObject()
						}
						argsStr, err := jsonx.Stringify(argsObj)
						if err != nil {
							return nil, err
						}
						fnObj := jsonx.NewObject()
						fnObj.Set("name", tc.Name)
						fnObj.Set("arguments", argsStr)
						tcObj.Set("type", "function")
						tcObj.Set("function", fnObj)
					}
					convertedCalls = append(convertedCalls, tcObj)
				}
				assistantMsg.Set("tool_calls", convertedCalls)
			}
			if preservedReasoningDetails != nil {
				assistantMsg.Set("reasoning_details", preservedReasoningDetails)
			}
			if compat.requiresReasoningContentOnAssistantMessages && model != nil && model.Reasoning && !assistantMsg.Has("reasoning_content") {
				assistantMsg.Set("reasoning_content", "")
			}

			hasContent := false
			if contentVal, exists := assistantMsg.Get("content"); exists && contentVal != nil {
				switch cv := contentVal.(type) {
				case string:
					hasContent = len(cv) > 0
				case []*jsonx.Object:
					hasContent = len(cv) > 0
				}
			}
			if !hasContent && !assistantMsg.Has("tool_calls") {
				continue
			}
			params = append(params, assistantMsg)

		case *ai.ToolResultMessage:
			var imageBlocks []*jsonx.Object
			modelSupportsImage := model != nil && slices.Contains(model.Input, ai.ModelModalityImage)
			j := i
			for ; j < len(transformedMessages); j++ {
				toolMsg, ok := transformedMessages[j].(*ai.ToolResultMessage)
				if !ok || toolMsg == nil {
					break
				}
				var textParts []string
				hasImages := false
				for _, blk := range toolMsg.Content {
					switch b := blk.(type) {
					case *ai.TextContent:
						if b != nil {
							textParts = append(textParts, b.Text)
						}
					case *ai.ImageContent:
						if b != nil {
							hasImages = true
						}
					}
				}
				textResult := strings.Join(textParts, "\n")
				hasText := len(js.Trim(textResult)) > 0
				toolResultText := textResult
				if !hasText {
					if hasImages {
						toolResultText = "(see attached image)"
					} else {
						toolResultText = "(no tool output)"
					}
				}
				toolResultMsg := jsonx.NewObject()
				toolResultMsg.Set("role", "tool")
				toolResultMsg.Set("content", ai.SanitizeSurrogates(toolResultText))
				toolResultMsg.Set("tool_call_id", toolMsg.ToolCallId)
				if compat.requiresToolResultName && toolMsg.ToolName != "" {
					toolResultMsg.Set("name", toolMsg.ToolName)
				}
				params = append(params, toolResultMsg)

				if hasImages && modelSupportsImage {
					for _, blk := range toolMsg.Content {
						if img, ok := blk.(*ai.ImageContent); ok && img != nil {
							imgURL := jsonx.NewObject()
							imgURL.Set("url", fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data))
							part := jsonx.NewObject()
							part.Set("type", "image_url")
							part.Set("image_url", imgURL)
							imageBlocks = append(imageBlocks, part)
						}
					}
				}
			}

			i = j - 1

			if len(imageBlocks) > 0 {
				if compat.requiresAssistantAfterToolResult {
					bridge := jsonx.NewObject()
					bridge.Set("role", "assistant")
					bridge.Set("content", "I have processed the tool results.")
					params = append(params, bridge)
				}
				textPart := jsonx.NewObject()
				textPart.Set("type", "text")
				textPart.Set("text", "Attached image(s) from tool result:")
				userParts := make([]*jsonx.Object, 0, 1+len(imageBlocks))
				userParts = append(userParts, textPart)
				userParts = append(userParts, imageBlocks...)
				userMsg := jsonx.NewObject()
				userMsg.Set("role", "user")
				userMsg.Set("content", userParts)
				params = append(params, userMsg)
				lastRole = "user"
			} else {
				lastRole = "toolResult"
			}
			continue
		}

		lastRole = role
	}

	return params, nil
}

// ConvertMessagesOpenaiCompletions converts a TranscriptContext into OpenAI Chat Completions message objects.
func ConvertMessagesOpenaiCompletions(
	model *ai.Model,
	c *ai.TranscriptContext,
	compat *ai.OpenAICompletionsCompat,
	options *ConvertCompletionsMessagesOptions,
) ([]*jsonx.Object, error) {
	resolved := ocGetCompat(model)
	ocApplyCompatStruct(&resolved, compat)
	return ocConvertMessagesResolved(model, c, resolved, options)
}

func ocConvertTools(tools []*ai.Tool, compat ocResolvedCompat) ([]*jsonx.Object, error) {
	out := make([]*jsonx.Object, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		grammar, err := ResolveGrammarConstrainedSampling(tool, compat.supportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			grammarBody := jsonx.NewObject()
			grammarBody.Set("syntax", string(grammar.Format))
			grammarBody.Set("definition", grammar.Definition)

			formatObj := jsonx.NewObject()
			formatObj.Set("type", "grammar")
			formatObj.Set("grammar", grammarBody)

			customObj := jsonx.NewObject()
			customObj.Set("name", tool.Name)
			customObj.Set("description", tool.Description)
			customObj.Set("format", formatObj)

			toolObj := jsonx.NewObject()
			toolObj.Set("type", "custom")
			toolObj.Set("custom", customObj)
			out = append(out, toolObj)
			continue
		}

		strict, err := ResolveJsonSchemaStrictSampling(tool, compat.supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		var strictBool *bool
		if strict != nil {
			strictBool = strict
		}
		paramsSchema, err := GetJsonSchemaToolParameters(tool, strictBool)
		if err != nil {
			return nil, err
		}
		var paramsVal any = paramsSchema
		if paramsSchema != nil && paramsSchema.Object != nil {
			paramsVal = paramsSchema.Object
		}

		fnObj := jsonx.NewObject()
		fnObj.Set("name", tool.Name)
		fnObj.Set("description", tool.Description)
		fnObj.Set("parameters", paramsVal)
		if compat.supportsStrictMode {
			strictVal := false
			if strict != nil {
				strictVal = *strict
			}
			fnObj.Set("strict", strictVal)
		}

		toolObj := jsonx.NewObject()
		toolObj.Set("type", "function")
		toolObj.Set("function", fnObj)
		out = append(out, toolObj)
	}
	return out, nil
}

func ocBuildParams(
	model *ai.Model,
	c *ai.TranscriptContext,
	options *OpenAICompletionsOptions,
	compat ocResolvedCompat,
	cacheRetention ai.CacheRetention,
	grammarToolInputProperties map[string]string,
) (*jsonx.Object, error) {
	support := ai.TranscriptToolSupportNone
	if compat.supportsMidConvoSystemMessages && compat.supportsMidConvoToolAdditions {
		support = ai.TranscriptToolSupportAdditions
	}
	transcriptTools := ai.ResolveTranscriptTools(c.Messages, support)
	messages, err := ocConvertMessagesResolved(model, c, compat, &ConvertCompletionsMessagesOptions{
		GrammarToolInputProperties: grammarToolInputProperties,
	})
	if err != nil {
		return nil, err
	}
	cacheControl := ocGetCompatCacheControl(compat, cacheRetention)
	promptCacheKeyMode := compat.promptCacheKeyMode
	if promptCacheKeyMode == "" {
		promptCacheKeyMode = ai.PromptCacheKeyModeAuto
	}
	supportsPromptCacheKey := promptCacheKeyMode == ai.PromptCacheKeyModeEnabled ||
		(promptCacheKeyMode == ai.PromptCacheKeyModeAuto &&
			((model != nil && strings.Contains(model.BaseUrl, "api.openai.com")) ||
				(cacheRetention == ai.CacheRetentionLong && compat.supportsLongCacheRetention)))

	params := jsonx.NewObject()
	if model != nil {
		params.Set("model", model.Id)
	}
	params.Set("messages", messages)
	params.Set("stream", true)

	if cacheRetention != ai.CacheRetentionNone && supportsPromptCacheKey && options != nil {
		if clamped := ClampOpenAIPromptCacheKey(options.SessionId); clamped != nil {
			params.Set("prompt_cache_key", *clamped)
		}
	}
	if cacheRetention == ai.CacheRetentionLong && compat.supportsLongCacheRetention {
		params.Set("prompt_cache_retention", "24h")
	}

	if compat.supportsUsageInStreaming {
		streamOpts := jsonx.NewObject()
		streamOpts.Set("include_usage", true)
		params.Set("stream_options", streamOpts)
	}
	if compat.supportsStore {
		params.Set("store", false)
	}

	var maxTokensFieldVal *int
	if options != nil && options.MaxTokens != nil && *options.MaxTokens > 0 {
		v := *options.MaxTokens
		maxTokensFieldVal = &v
		if compat.maxTokensField == ai.MaxTokensFieldMaxTokens {
			params.Set("max_tokens", v)
		} else {
			params.Set("max_completion_tokens", v)
		}
	}
	if options != nil && options.Temperature != nil {
		params.Set("temperature", *options.Temperature)
	}

	var tools []*jsonx.Object
	if len(transcriptTools.RequestTools) > 0 {
		tools, err = ocConvertTools(transcriptTools.RequestTools, compat)
		if err != nil {
			return nil, err
		}
		params.Set("tools", tools)
		if compat.zaiToolStream {
			params.Set("tool_stream", true)
		}
	} else if compat.requiresToolsWithToolHistory && ocHasToolHistory(c.Messages) {
		tools = []*jsonx.Object{}
		params.Set("tools", tools)
	}

	if cacheControl != nil {
		ocApplyAnthropicCacheControl(messages, tools, cacheControl)
	}

	if options != nil && options.ToolChoice != nil {
		params.Set("tool_choice", options.ToolChoice)
	}
	if compat.vllmPriority != nil {
		params.Set("priority", *compat.vllmPriority)
	}

	thinkingTokenBudgetField := ocResolveThinkingTokenBudgetField(compat)
	thinkingBudget := ocResolveClampedThinkingBudget(model, options, maxTokensFieldVal)

	var effort *ai.ThinkingLevel
	if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
		effort = options.ReasoningEffort
	}

	if model != nil && model.Reasoning {
		switch compat.thinkingFormat {
		case ai.ThinkingFormatZai:
			thinkingObj := jsonx.NewObject()
			if effort != nil {
				thinkingObj.Set("type", "enabled")
				thinkingObj.Set("clear_thinking", false)
			} else {
				thinkingObj.Set("type", "disabled")
			}
			params.Set("thinking", thinkingObj)
			if effort != nil && compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if !isMapped {
					params.Set("reasoning_effort", string(*effort))
				} else if !isNull {
					params.Set("reasoning_effort", mapped)
				}
			}

		case ai.ThinkingFormatQwen:
			params.Set("enable_thinking", effort != nil)
			if effort != nil && compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					params.Set("reasoning_effort", mapped)
				} else {
					params.Set("reasoning_effort", string(*effort))
				}
			}

		case ai.ThinkingFormatQwenChatTemplate:
			kw := jsonx.NewObject()
			kw.Set("enable_thinking", effort != nil)
			kw.Set("preserve_thinking", true)
			params.Set("chat_template_kwargs", kw)

		case ai.ThinkingFormatChatTemplate:
			if kw := ocBuildChatTemplateValues(model, options, compat.chatTemplateKwargs, thinkingBudget); kw != nil {
				params.Set("chat_template_kwargs", kw)
			}

		case ai.ThinkingFormatBaseten:
			if args := ocBuildChatTemplateValues(model, options, compat.chatTemplateArgs, thinkingBudget); args != nil {
				params.Set("chat_template_args", args)
			}
			if compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if !isMapped {
					if effort != nil {
						params.Set("reasoning_effort", string(*effort))
					}
				} else if !isNull {
					params.Set("reasoning_effort", mapped)
				}
			}

		case ai.ThinkingFormatDeepseek:
			if effort != nil {
				t := jsonx.NewObject()
				t.Set("type", "enabled")
				params.Set("thinking", t)
			} else {
				_, isOffNull, _ := ocLookupThinkingLevel(model, nil)
				if !isOffNull {
					t := jsonx.NewObject()
					t.Set("type", "disabled")
					params.Set("thinking", t)
				}
			}
			if effort != nil && compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					params.Set("reasoning_effort", mapped)
				} else {
					params.Set("reasoning_effort", string(*effort))
				}
			}

		case ai.ThinkingFormatOpenrouter:
			rObj := jsonx.NewObject()
			if effort != nil {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					rObj.Set("effort", mapped)
				} else {
					rObj.Set("effort", string(*effort))
				}
				params.Set("reasoning", rObj)
			} else {
				offMapped, isOffNull, isOffMapped := ocLookupThinkingLevel(model, nil)
				if !isOffNull {
					if isOffMapped {
						rObj.Set("effort", offMapped)
					} else {
						rObj.Set("effort", "none")
					}
					params.Set("reasoning", rObj)
				}
			}

		case ai.ThinkingFormatAntLing:
			if effort != nil {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					rObj := jsonx.NewObject()
					rObj.Set("effort", mapped)
					params.Set("reasoning", rObj)
				}
			}

		case ai.ThinkingFormatTogether:
			rObj := jsonx.NewObject()
			rObj.Set("enabled", effort != nil)
			params.Set("reasoning", rObj)
			if effort != nil && compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					params.Set("reasoning_effort", mapped)
				} else {
					params.Set("reasoning_effort", string(*effort))
				}
			}

		case ai.ThinkingFormatStringThinking:
			if effort != nil {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					params.Set("thinking", mapped)
				} else {
					params.Set("thinking", string(*effort))
				}
			} else {
				offMapped, isOffNull, isOffMapped := ocLookupThinkingLevel(model, nil)
				if !isOffNull {
					if isOffMapped {
						params.Set("thinking", offMapped)
					} else {
						params.Set("thinking", "none")
					}
				}
			}

		default:
			if effort != nil && compat.supportsReasoningEffort {
				mapped, isNull, isMapped := ocLookupThinkingLevel(model, effort)
				if isMapped && !isNull {
					params.Set("reasoning_effort", mapped)
				} else {
					params.Set("reasoning_effort", string(*effort))
				}
			} else if effort == nil && compat.supportsReasoningEffort {
				offMapped, isOffNull, isOffMapped := ocLookupThinkingLevel(model, nil)
				if isOffMapped && !isOffNull {
					params.Set("reasoning_effort", offMapped)
				}
			}
		}
	}

	if thinkingTokenBudgetField != "" && thinkingBudget != nil {
		params.Set(string(thinkingTokenBudgetField), *thinkingBudget)
	}

	if compat.openRouterRouting != nil {
		params.Set("provider", compat.openRouterRouting)
	}

	if compat.vercelGatewayRouting != nil && (compat.vercelGatewayRouting.Only != nil || compat.vercelGatewayRouting.Order != nil) {
		gw := jsonx.NewObject()
		if compat.vercelGatewayRouting.Only != nil {
			gw.Set("only", compat.vercelGatewayRouting.Only)
		}
		if compat.vercelGatewayRouting.Order != nil {
			gw.Set("order", compat.vercelGatewayRouting.Order)
		}
		providerOpts := jsonx.NewObject()
		providerOpts.Set("gateway", gw)
		params.Set("providerOptions", providerOpts)
	}

	level := ai.ModelThinkingLevelOff
	if effort != nil {
		level = ai.ModelThinkingLevel(*effort)
	}
	var reqSamplingParams *jsonx.Object
	if options != nil {
		reqSamplingParams = options.SamplingParams
	}
	if samplingParams := ResolveSamplingParams(model, level, reqSamplingParams); samplingParams != nil {
		params.Assign(samplingParams)
	}

	return params, nil
}

func ocParseChunkUsage(rawUsage *jsonx.Object, model *ai.Model) ai.Usage {
	if rawUsage == nil {
		return ai.Usage{}
	}
	promptTokens := orsIntField(rawUsage, "prompt_tokens")

	cacheReadTokens := 0
	foundCached := false
	promptDetails, _ := rawUsage.GetObject("prompt_tokens_details")
	if promptDetails != nil {
		if n, ok := promptDetails.GetNumber("cached_tokens"); ok {
			cacheReadTokens = int(n)
			foundCached = true
		}
	}
	if !foundCached {
		if n, ok := rawUsage.GetNumber("prompt_cache_hit_tokens"); ok {
			cacheReadTokens = int(n)
			foundCached = true
		}
	}
	if !foundCached {
		if n, ok := rawUsage.GetNumber("cached_tokens"); ok {
			cacheReadTokens = int(n)
		}
	}

	cacheWriteTokens := orsIntField(promptDetails, "cache_write_tokens")
	input := max(0, promptTokens-cacheReadTokens-cacheWriteTokens)
	outputTokens := orsIntField(rawUsage, "completion_tokens")

	compDetails, _ := rawUsage.GetObject("completion_tokens_details")
	reasoningTokens := orsIntField(compDetails, "reasoning_tokens")
	if reasoningTokens == 0 {
		reasoningTokens = orsIntField(rawUsage, "reasoning_tokens")
	}

	usage := ai.Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		Reasoning:   &reasoningTokens,
		TotalTokens: input + outputTokens + cacheReadTokens + cacheWriteTokens,
	}
	ai.CalculateCost(model, &usage)
	if ai.IsOpenRouterModel(model) {
		var accounting ai.OpenRouterUsageAccounting
		if err := jsonx.Decode(rawUsage, &accounting); err == nil {
			ai.ApplyOpenRouterReportedCost(&usage, &accounting)
		}
	}
	return usage
}

func ocMapStopReason(reason string) (ai.StopReason, *string) {
	if reason == "" {
		return ai.StopReasonStop, nil
	}
	switch reason {
	case "stop", "end":
		return ai.StopReasonStop, nil
	case "length":
		return ai.StopReasonLength, nil
	case "function_call", "tool_calls":
		return ai.StopReasonToolUse, nil
	case "content_filter":
		msg := "Provider finish_reason: content_filter"
		return ai.StopReasonError, &msg
	case "network_error":
		msg := "Provider finish_reason: network_error"
		return ai.StopReasonError, &msg
	default:
		msg := fmt.Sprintf("Provider finish_reason: %s", reason)
		return ai.StopReasonError, &msg
	}
}

func ocExtractOpenRouterRawMetadata(err error) string {
	var sdkErr *ai.ProviderSdkError
	if !errors.As(err, &sdkErr) || sdkErr == nil {
		return ""
	}
	obj, ok := sdkErr.ParsedError.(*jsonx.Object)
	if !ok || obj == nil {
		return ""
	}
	meta, hasMeta := obj.GetObject("metadata")
	if !hasMeta || meta == nil {
		if innerErr, hasErr := obj.GetObject("error"); hasErr && innerErr != nil {
			meta, hasMeta = innerErr.GetObject("metadata")
		}
	}
	if !hasMeta || meta == nil {
		return ""
	}
	raw, hasRaw := meta.Get("raw")
	if !hasRaw || raw == nil || raw == false || raw == "" {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	return orsScalarString(meta, "raw")
}

type ocToolCallState struct {
	streamIndex  *int
	inputKind    string // "json" | "custom"
	argsStream   *ai.ToolCallArgumentsStream
	customProp   string
	customBuffer *GrammarToolInputJsonBuffer
}

// StreamOpenaiCompletions streams responses from an OpenAI-compatible Chat Completions endpoint.
func StreamOpenaiCompletions(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := ocExtractOptions(options)
	compat := ocGetCompat(model)
	normalizedContext := ai.ResolveTranscript(c, &compat.supportsMidConvoSystemMessages)
	sig := ai.OperationSignal(ctx)

	go func() {
		var modelAPI ai.Api
		var modelProvider ai.ProviderId
		var modelID string
		if model != nil {
			modelAPI = ai.Api(model.Api)
			modelProvider = model.Provider
			modelID = model.Id
		}

		output := &ai.AssistantMessage{
			Role:       "assistant",
			Content:    ai.ContentList{},
			Api:        modelAPI,
			Provider:   modelProvider,
			Model:      modelID,
			Usage:      ai.Usage{},
			StopReason: ai.StopReasonPending,
			Timestamp:  js.DateNow(),
		}

		var streamedReasoningDetails []*jsonx.Object
		applyStreamedReasoningDetails := func(block *ai.ThinkingContent) {
			if block != nil && streamedReasoningDetails != nil {
				if encoded, err := jsonx.Stringify(streamedReasoningDetails); err == nil {
					block.ThinkingSignature = &encoded
				}
			}
		}

		fail := func(err error) {
			for _, blk := range output.Content {
				if tb, ok := blk.(*ai.ThinkingContent); ok {
					applyStreamedReasoningDetails(tb)
				}
			}
			if sig.Err() != nil {
				output.StopReason = ai.StopReasonAborted
			} else {
				output.StopReason = ai.StopReasonError
			}
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			if rawMeta := ocExtractOpenRouterRawMetadata(err); rawMeta != "" && !strings.Contains(errMsg, rawMeta) {
				errMsg += "\n" + rawMeta
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End(output)
		}

		if model == nil {
			fail(errors.New("Missing model for OpenAI Completions"))
			return
		}

		apiKey, err := orGetClientApiKey(string(model.Provider), opts.ApiKey, opts.Headers)
		if err != nil {
			fail(err)
			return
		}

		grammarToolInputProperties, err := CreateGrammarToolInputProperties(
			ai.GetDeclaredTools(normalizedContext.Messages),
			compat.supportsOpenAIGrammarTools,
		)
		if err != nil {
			fail(err)
			return
		}

		cacheRetention := orResolveCacheRetention(opts.CacheRetention, opts.Env)
		cacheSessionID := ""
		if cacheRetention != ai.CacheRetentionNone && opts.SessionId != nil {
			cacheSessionID = *opts.SessionId
		}

		params, err := ocBuildParams(model, normalizedContext, opts, compat, cacheRetention, grammarToolInputProperties)
		if err != nil {
			fail(err)
			return
		}
		var payload any = params
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

		defaultHeaders := ocCreateHeaders(model, normalizedContext, compat, opts.Headers, opts.Identity, cacheSessionID)
		resp, err := orsDoOpenAIStreamRequest(sig, orsOpenAIHttpConfig{
			BaseUrl:         model.BaseUrl,
			Path:            "/chat/completions",
			ApiKey:          apiKey,
			DefaultHeaders:  defaultHeaders,
			Body:            payload,
			HTTPClient:      opts.HTTPClient,
			TimeoutMs:       opts.TimeoutMs,
			MaxRetries:      opts.MaxRetries,
			MaxRetryDelayMs: opts.MaxRetryDelayMs,
		})
		if err != nil {
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

		stream.Push(&ai.AssistantMessageEventStart{
			Type:    "start",
			Partial: orsClonePartial(output),
		})

		var textBlock *ai.TextContent
		var thinkingBlock *ai.ThinkingContent
		hasFinishReason := false
		toolCallBlocksByIndex := make(map[int]*ai.ToolCall)
		toolCallBlocksByID := make(map[string]*ai.ToolCall)
		toolCallStates := make(map[*ai.ToolCall]*ocToolCallState)

		getContentIndex := func(target ai.Content) int {
			for idx, blk := range output.Content {
				if blk == target {
					return idx
				}
			}
			return -1
		}

		ensureTextBlock := func() *ai.TextContent {
			if textBlock == nil {
				textBlock = &ai.TextContent{Type: "text", Text: ""}
				output.Content = append(output.Content, textBlock)
				stream.Push(&ai.AssistantMessageEventTextStart{
					Type:         "text_start",
					ContentIndex: getContentIndex(textBlock),
					Partial:      orsClonePartial(output),
				})
			}
			return textBlock
		}

		ensureThinkingBlock := func(thinkingSig string) *ai.ThinkingContent {
			if thinkingBlock == nil {
				var sigPtr *string
				if thinkingSig != "" {
					s := thinkingSig
					sigPtr = &s
				} else {
					s := ""
					sigPtr = &s
				}
				thinkingBlock = &ai.ThinkingContent{
					Type:              "thinking",
					Thinking:          "",
					ThinkingSignature: sigPtr,
				}
				output.Content = append(output.Content, thinkingBlock)
				stream.Push(&ai.AssistantMessageEventThinkingStart{
					Type:         "thinking_start",
					ContentIndex: getContentIndex(thinkingBlock),
					Partial:      orsClonePartial(output),
				})
			}
			return thinkingBlock
		}

		ensureToolCallBlock := func(tcObj *jsonx.Object) (*ai.ToolCall, *ocToolCallState) {
			var streamIndex *int
			if n, ok := tcObj.GetNumber("index"); ok {
				idx := int(n)
				streamIndex = &idx
			}
			tcID, _ := tcObj.GetString("id")
			fnObj, hasFn := tcObj.GetObject("function")
			customObj, hasCustom := tcObj.GetObject("custom")
			var name string
			if hasFn && fnObj != nil {
				name, _ = fnObj.GetString("name")
			}
			if name == "" && hasCustom && customObj != nil {
				name, _ = customObj.GetString("name")
			}

			var block *ai.ToolCall
			if streamIndex != nil {
				block = toolCallBlocksByIndex[*streamIndex]
			}
			if block == nil && tcID != "" {
				block = toolCallBlocksByID[tcID]
			}

			isCustomCall := hasCustom && customObj != nil && (!hasFn || fnObj == nil)
			if block == nil {
				var state *ocToolCallState
				var initArgs *jsonx.Object
				if isCustomCall {
					prop := "input"
					if p, ok := grammarToolInputProperties[name]; ok {
						prop = p
					}
					initArgs = jsonx.NewObject()
					initArgs.Set(prop, "")
					state = &ocToolCallState{
						streamIndex:  streamIndex,
						inputKind:    "custom",
						customProp:   prop,
						customBuffer: &GrammarToolInputJsonBuffer{},
					}
				} else {
					initArgs = jsonx.NewObject()
					state = &ocToolCallState{
						streamIndex: streamIndex,
						inputKind:   "json",
						argsStream:  ai.NewToolCallArgumentsStream(),
					}
				}
				block = &ai.ToolCall{
					Type:      "toolCall",
					Id:        tcID,
					Name:      name,
					Arguments: initArgs,
				}
				toolCallStates[block] = state
				if streamIndex != nil {
					toolCallBlocksByIndex[*streamIndex] = block
				}
				if tcID != "" {
					toolCallBlocksByID[tcID] = block
				}
				output.Content = append(output.Content, block)
				stream.Push(&ai.AssistantMessageEventToolcallStart{
					Type:         "toolcall_start",
					ContentIndex: getContentIndex(block),
					Partial:      orsClonePartial(output),
				})
			}

			state := toolCallStates[block]
			if streamIndex != nil && state.streamIndex == nil {
				state.streamIndex = streamIndex
				toolCallBlocksByIndex[*streamIndex] = block
			}
			if tcID != "" {
				toolCallBlocksByID[tcID] = block
			}
			if block.Name == "" && name != "" {
				block.Name = name
			}
			if isCustomCall && state.inputKind != "custom" {
				prop := "input"
				if p, ok := grammarToolInputProperties[block.Name]; ok {
					prop = p
				}
				argsObj := jsonx.NewObject()
				argsObj.Set(prop, "")
				block.Arguments = argsObj
				state.inputKind = "custom"
				state.customProp = prop
				state.customBuffer = &GrammarToolInputJsonBuffer{}
				state.argsStream = nil
			}
			return block, state
		}

		finishBlock := func(blk ai.Content) error {
			contentIndex := getContentIndex(blk)
			if contentIndex == -1 {
				return nil
			}
			switch b := blk.(type) {
			case *ai.TextContent:
				stream.Push(&ai.AssistantMessageEventTextEnd{
					Type:         "text_end",
					ContentIndex: contentIndex,
					Content:      b.Text,
					Partial:      orsClonePartial(output),
				})
			case *ai.ThinkingContent:
				applyStreamedReasoningDetails(b)
				stream.Push(&ai.AssistantMessageEventThinkingEnd{
					Type:         "thinking_end",
					ContentIndex: contentIndex,
					Content:      b.Thinking,
					Partial:      orsClonePartial(output),
				})
			case *ai.ToolCall:
				state := toolCallStates[b]
				delete(toolCallStates, b)
				if state != nil {
					if state.inputKind == "custom" {
						deltaPtr, err := CloseGrammarToolInputJson(state.customBuffer, state.customProp, nil)
						if err != nil {
							return err
						}
						if deltaPtr != nil {
							stream.Push(&ai.AssistantMessageEventToolcallDelta{
								Type:         "toolcall_delta",
								ContentIndex: contentIndex,
								Delta:        *deltaPtr,
								Partial:      orsClonePartial(output),
							})
						}
					} else if state.argsStream != nil {
						argsObj, argsErr := state.argsStream.Finish()
						b.Arguments = argsObj
						b.ArgumentsError = argsErr
					}
				}
				tcCopy := *b
				if tcCopy.Arguments != nil {
					tcCopy.Arguments = tcCopy.Arguments.Clone()
				}
				stream.Push(&ai.AssistantMessageEventToolcallEnd{
					Type:         "toolcall_end",
					ContentIndex: contentIndex,
					ToolCall:     &tcCopy,
					Partial:      orsClonePartial(output),
				})
			}
			return nil
		}

		for chunk, iterErr := range orsIterOpenAISSEEvents(sig, resp.Body, resp.Header) {
			if iterErr != nil {
				fail(iterErr)
				return
			}
			if opts.OnProviderStreamEvent != nil {
				if err := opts.OnProviderStreamEvent(chunk, model); err != nil {
					fail(err)
					return
				}
			}
			if chunk == nil {
				continue
			}

			if output.ResponseId == nil {
				if id, ok := chunk.GetString("id"); ok && id != "" {
					output.ResponseId = &id
				}
			}
			if output.ResponseModel == nil {
				if chunkModel, ok := chunk.GetString("model"); ok && chunkModel != "" && chunkModel != model.Id {
					output.ResponseModel = &chunkModel
				}
			}
			chunkUsage, hasChunkUsage := chunk.GetObject("usage")
			if hasChunkUsage && chunkUsage != nil {
				output.Usage = ocParseChunkUsage(chunkUsage, model)
			}

			choices, hasChoices := chunk.GetArray("choices")
			if !hasChoices || len(choices) == 0 {
				continue
			}
			choice, ok := choices[0].(*jsonx.Object)
			if !ok || choice == nil {
				continue
			}

			if !hasChunkUsage || chunkUsage == nil {
				if choiceUsage, ok := choice.GetObject("usage"); ok && choiceUsage != nil {
					output.Usage = ocParseChunkUsage(choiceUsage, model)
				}
			}

			if fr, ok := choice.GetString("finish_reason"); ok && fr != "" {
				raw := fr
				output.RawStopReason = &raw
				mappedStop, mappedErrMsg := ocMapStopReason(fr)
				output.StopReason = mappedStop
				if mappedErrMsg != nil {
					output.ErrorMessage = mappedErrMsg
				}
				hasFinishReason = true
			}

			deltaObj, hasDelta := choice.GetObject("delta")
			if !hasDelta || deltaObj == nil {
				continue
			}

			reasoningFields := [3]string{"reasoning_content", "reasoning", "reasoning_text"}
			var foundReasoningField string
			var foundReasoningDelta string
			for _, field := range reasoningFields {
				if v, ok := deltaObj.GetString(field); ok && len(v) > 0 {
					foundReasoningField = field
					foundReasoningDelta = v
					break
				}
			}
			if foundReasoningField != "" && len(foundReasoningDelta) > 0 {
				thinkingSig := foundReasoningField
				if model.Provider == ai.KnownProviderOpencodeGo && foundReasoningField == "reasoning" {
					thinkingSig = "reasoning_content"
				}
				blk := ensureThinkingBlock(thinkingSig)
				blk.Thinking += foundReasoningDelta
				stream.Push(&ai.AssistantMessageEventThinkingDelta{
					Type:         "thinking_delta",
					ContentIndex: getContentIndex(blk),
					Delta:        foundReasoningDelta,
					Partial:      orsClonePartial(output),
				})
			}

			if rdArr, ok := deltaObj.GetArray("reasoning_details"); ok {
				for _, elem := range rdArr {
					detailObj, ok := elem.(*jsonx.Object)
					if !ok || !ocIsOpenAIReasoningDetail(detailObj) {
						continue
					}
					ensureThinkingBlock("")
					streamedReasoningDetails = ocAppendOpenAIReasoningDetail(streamedReasoningDetails, detailObj)
				}
			}

			if textDelta, ok := deltaObj.GetString("content"); ok && len(textDelta) > 0 {
				blk := ensureTextBlock()
				blk.Text += textDelta
				stream.Push(&ai.AssistantMessageEventTextDelta{
					Type:         "text_delta",
					ContentIndex: getContentIndex(blk),
					Delta:        textDelta,
					Partial:      orsClonePartial(output),
				})
			}

			if tcArr, ok := deltaObj.GetArray("tool_calls"); ok {
				for _, elem := range tcArr {
					tcObj, ok := elem.(*jsonx.Object)
					if !ok || tcObj == nil {
						continue
					}
					blk, state := ensureToolCallBlock(tcObj)
					if tcID, ok := tcObj.GetString("id"); ok && tcID != "" && blk.Id == "" {
						blk.Id = tcID
						toolCallBlocksByID[tcID] = blk
					}
					fnObj, hasFn := tcObj.GetObject("function")
					customObj, hasCustom := tcObj.GetObject("custom")
					var tcName string
					if hasFn && fnObj != nil {
						tcName, _ = fnObj.GetString("name")
					}
					if tcName == "" && hasCustom && customObj != nil {
						tcName, _ = customObj.GetString("name")
					}
					if blk.Name == "" && tcName != "" {
						blk.Name = tcName
					}

					deltaStr := ""
					if state.inputKind == "json" && hasFn && fnObj != nil {
						if fnArgs, ok := fnObj.GetString("arguments"); ok && fnArgs != "" {
							deltaStr = fnArgs
							blk.Arguments = state.argsStream.Append(deltaStr)
						}
					} else if state.inputKind == "custom" && hasCustom && customObj != nil {
						if custIn, ok := customObj.GetString("input"); ok && custIn != "" {
							jsonDelta, err := AppendGrammarToolInputJsonDelta(state.customBuffer, state.customProp, custIn)
							if err != nil {
								fail(err)
								return
							}
							if jsonDelta != nil {
								deltaStr = *jsonDelta
							}
							argsObj := jsonx.NewObject()
							argsObj.Set(state.customProp, state.customBuffer.Input)
							blk.Arguments = argsObj
						}
					}
					stream.Push(&ai.AssistantMessageEventToolcallDelta{
						Type:         "toolcall_delta",
						ContentIndex: getContentIndex(blk),
						Delta:        deltaStr,
						Partial:      orsClonePartial(output),
					})
				}
			}
		}

		for _, blk := range output.Content {
			if err := finishBlock(blk); err != nil {
				fail(err)
				return
			}
		}

		if sig.Err() != nil || output.StopReason == ai.StopReasonAborted {
			fail(errors.New("Request was aborted"))
			return
		}
		if !hasFinishReason && !compat.supportsFinishReason {
			hasToolCall := false
			for _, blk := range output.Content {
				if _, ok := blk.(*ai.ToolCall); ok {
					hasToolCall = true
					break
				}
			}
			if hasToolCall {
				output.StopReason = ai.StopReasonToolUse
			} else {
				output.StopReason = ai.StopReasonStop
			}
		}
		if output.StopReason == ai.StopReasonError {
			msg := "Provider returned an error stop reason"
			if output.ErrorMessage != nil && *output.ErrorMessage != "" {
				msg = *output.ErrorMessage
			}
			fail(errors.New(msg))
			return
		}
		if (compat.supportsFinishReason && !hasFinishReason) || output.StopReason == ai.StopReasonPending {
			fail(errors.New("Stream ended without finish_reason"))
			return
		}

		stream.Push(&ai.AssistantMessageEventDone{
			Type:    "done",
			Reason:  output.StopReason,
			Message: output,
		})
		stream.End(output)
	}()

	return stream
}

// StreamSimpleOpenaiCompletions adapts SimpleStreamOptions and delegates to StreamOpenaiCompletions.
func StreamSimpleOpenaiCompletions(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	var apiKey *string
	if options != nil {
		apiKey = options.ApiKey
	}
	base := BuildBaseOptions(model, c, options, apiKey)
	opts := &OpenAICompletionsOptions{StreamOptions: base}
	if options != nil {
		if options.ToolChoice != nil {
			opts.ToolChoice = string(*options.ToolChoice)
		}
		if options.Reasoning != nil {
			clamped := ai.ClampThinkingLevel(model, ai.ModelThinkingLevel(*options.Reasoning))
			if clamped != ai.ModelThinkingLevelOff {
				level := ai.ThinkingLevel(clamped)
				opts.ReasoningEffort = &level
			}
		}
		opts.ThinkingBudgets = options.ThinkingBudgets
	}
	return StreamOpenaiCompletions(ctx, model, c, opts)
}
