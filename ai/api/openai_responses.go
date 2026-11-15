// Ported from packages/ai/src/api/openai-responses.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	orMinOutputTokens           = 16
	orChatGPTUsageURL           = "https://chatgpt.com/settings/usage"
	orChatGPTWorkspaceLimitHint = "You've reached your ChatGPT workspace's usage limit. Ask your workspace admin to increase it at admin.openai.com → Workspace → Usage limits."
	orConfigUpdateRequestEffort = "medium"
)

var orOpenAIToolCallProviders = map[string]struct{}{
	"openai":       {},
	"openai-codex": {},
	"opencode":     {},
}

// OpenAIResponsesOptions configures OpenAI Responses API streaming requests.
type OpenAIResponsesOptions struct {
	ai.StreamOptions
	ReasoningEffort  *ai.ThinkingLevel `json:"reasoningEffort,omitzero"`
	ReasoningSummary *string           `json:"reasoningSummary,omitzero"`
	ServiceTier      *string           `json:"serviceTier,omitzero"`
	ToolChoice       any               `json:"toolChoice,omitzero"`
}

type orResolvedCompat struct {
	supportsDeveloperRole           bool
	supportsMidConvoSystemMessages  bool
	sessionAffinityFormat           ai.SessionAffinityFormat
	promptCacheKeyMode              ai.PromptCacheKeyMode
	supportsLongCacheRetention      bool
	supportsStrictMode              bool
	supportsOpenAIGrammarTools      bool
	supportsAdditionalTools         bool
	supportsToolSearch              bool
	supportsExplicitPromptCacheMode bool
	supportsMaxOutputTokens         bool
	supportsMidConvoEffort          bool
}

func orIsChatGPTSignIn(model *ai.Model, apiKey *string) bool {
	return model != nil &&
		model.Provider == ai.KnownProviderOpenai &&
		model.BaseUrl == "https://api.openai.com/v1" &&
		apiKey != nil &&
		!strings.HasPrefix(*apiKey, "sk-")
}

func orGetClientApiKey(provider string, apiKey *string, headers ai.ProviderHeaders) (string, error) {
	if apiKey != nil && *apiKey != "" {
		return *apiKey, nil
	}
	if orsHasHeaderCaseInsensitive(headers, "authorization", "cf-aig-authorization") {
		return "unused", nil
	}
	return "", fmt.Errorf("No API key for provider: %s", provider)
}

func orDetectSessionAffinityFormat(model *ai.Model) ai.SessionAffinityFormat {
	if model != nil && (model.Provider == ai.KnownProviderOpenrouter || strings.Contains(model.BaseUrl, "openrouter.ai")) {
		return ai.SessionAffinityFormatOpenrouter
	}
	return ai.SessionAffinityFormatOpenai
}

func orResolveCacheRetention(cacheRetention *ai.CacheRetention, env ai.ProviderEnv) ai.CacheRetention {
	if cacheRetention != nil && *cacheRetention != "" {
		return *cacheRetention
	}
	if ai.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func orGetCompat(model *ai.Model) orResolvedCompat {
	out := orResolvedCompat{
		supportsDeveloperRole:           true,
		supportsMidConvoSystemMessages:  false,
		sessionAffinityFormat:           orDetectSessionAffinityFormat(model),
		promptCacheKeyMode:              ai.PromptCacheKeyModeAuto,
		supportsLongCacheRetention:      true,
		supportsStrictMode:              false,
		supportsOpenAIGrammarTools:      false,
		supportsAdditionalTools:         false,
		supportsToolSearch:              false,
		supportsExplicitPromptCacheMode: false,
		supportsMaxOutputTokens:         true,
		supportsMidConvoEffort:          false,
	}
	if model == nil || model.Compat.V == nil {
		return out
	}
	var respCompat *ai.OpenAIResponsesCompat
	switch c := model.Compat.V.(type) {
	case *ai.OpenAIResponsesCompat:
		respCompat = c
	case *ai.UnknownModelCompat:
		if c != nil && c.Raw != nil {
			var decoded ai.OpenAIResponsesCompat
			if err := jsonx.Decode(c.Raw, &decoded); err == nil {
				respCompat = &decoded
			}
		}
	}
	if respCompat == nil {
		return out
	}
	if respCompat.SupportsDeveloperRole != nil {
		out.supportsDeveloperRole = *respCompat.SupportsDeveloperRole
	}
	if respCompat.SupportsMidConvoSystemMessages != nil {
		out.supportsMidConvoSystemMessages = *respCompat.SupportsMidConvoSystemMessages
	}
	if respCompat.SessionAffinityFormat != nil && *respCompat.SessionAffinityFormat != "" {
		out.sessionAffinityFormat = *respCompat.SessionAffinityFormat
	}
	if respCompat.PromptCacheKeyMode != nil && *respCompat.PromptCacheKeyMode != "" {
		out.promptCacheKeyMode = *respCompat.PromptCacheKeyMode
	}
	if respCompat.SupportsLongCacheRetention != nil {
		out.supportsLongCacheRetention = *respCompat.SupportsLongCacheRetention
	}
	if respCompat.SupportsStrictMode != nil {
		out.supportsStrictMode = *respCompat.SupportsStrictMode
	}
	if respCompat.SupportsOpenAIGrammarTools != nil {
		out.supportsOpenAIGrammarTools = *respCompat.SupportsOpenAIGrammarTools
	}
	if respCompat.SupportsAdditionalTools != nil {
		out.supportsAdditionalTools = *respCompat.SupportsAdditionalTools
	}
	if respCompat.SupportsToolSearch != nil {
		out.supportsToolSearch = *respCompat.SupportsToolSearch
	}
	if respCompat.SupportsExplicitPromptCacheMode != nil {
		out.supportsExplicitPromptCacheMode = *respCompat.SupportsExplicitPromptCacheMode
	}
	if respCompat.SupportsMaxOutputTokens != nil {
		out.supportsMaxOutputTokens = *respCompat.SupportsMaxOutputTokens
	}
	if respCompat.SupportsMidConvoEffort != nil {
		out.supportsMidConvoEffort = *respCompat.SupportsMidConvoEffort
	}
	return out
}

func orGetConfigurationUpdateEffort(model *ai.Model, options *OpenAIResponsesOptions, compat orResolvedCompat) *string {
	if !compat.supportsMidConvoEffort || options == nil || options.ReasoningEffort == nil || *options.ReasoningEffort == "" || orIsChatGPTSignIn(model, options.ApiKey) {
		return nil
	}
	effort := orsClampOpenAIReasoningEffort(model, ai.ModelThinkingLevel(*options.ReasoningEffort))
	return &effort
}

func orGetPromptCacheRetention(compat orResolvedCompat, cacheRetention ai.CacheRetention) string {
	if cacheRetention == ai.CacheRetentionLong && compat.supportsLongCacheRetention && !compat.supportsExplicitPromptCacheMode {
		return "24h"
	}
	return ""
}

func orGetPromptCacheOptions(compat orResolvedCompat, cacheRetention ai.CacheRetention) *jsonx.Object {
	if !compat.supportsExplicitPromptCacheMode {
		return nil
	}
	if cacheRetention == ai.CacheRetentionNone {
		obj := jsonx.NewObject()
		obj.Set("mode", "explicit")
		return obj
	}
	if cacheRetention == ai.CacheRetentionLong && compat.supportsLongCacheRetention {
		obj := jsonx.NewObject()
		obj.Set("ttl", "30m")
		return obj
	}
	return nil
}

func orExtractOptions(options ai.ProviderStreamOptions) *OpenAIResponsesOptions {
	if options == nil {
		return &OpenAIResponsesOptions{}
	}
	if o, ok := options.(*OpenAIResponsesOptions); ok && o != nil {
		cp := *o
		return &cp
	}
	var out OpenAIResponsesOptions
	if base := options.BaseStreamOptions(); base != nil {
		out.StreamOptions = *base
	}
	return &out
}

func orBuildDefaultHeaders(
	model *ai.Model,
	c *ai.TranscriptContext,
	compat orResolvedCompat,
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
		copilotHeaders := BuildCopilotDynamicHeaders(CopilotDynamicHeadersParams{
			Messages:  c.Messages,
			HasImages: HasCopilotVisionInput(c.Messages),
		})
		for k, v := range copilotHeaders {
			val := v
			out.Set(k, &val)
		}
	}
	if sessionId != "" {
		if compat.sessionAffinityFormat == ai.SessionAffinityFormatOpenrouter {
			val := sessionId
			out.Set("x-session-id", &val)
		} else {
			if compat.sessionAffinityFormat == ai.SessionAffinityFormatOpenai {
				val := sessionId
				out.Set("session-id", &val)
			}
			val := sessionId
			out.Set("x-client-request-id", &val)
		}
	}
	if optionsHeaders != nil {
		for k, v := range optionsHeaders.All() {
			out.Set(k, v)
		}
	}
	return out
}

func orBuildParams(
	model *ai.Model,
	c *ai.TranscriptContext,
	options *OpenAIResponsesOptions,
	compat orResolvedCompat,
	grammarToolInputProperties map[string]string,
	configurationUpdateEffort *string,
) (*jsonx.Object, error) {
	toolSupport := GetResponsesToolSupport(ResponsesToolSupportOptions{
		SupportsMidConvoSystemMessages: compat.supportsMidConvoSystemMessages,
		SupportsAdditionalTools:        compat.supportsAdditionalTools,
		SupportsToolSearch:             compat.supportsToolSearch,
	})
	transcriptTools := ai.ResolveTranscriptTools(c.Messages, toolSupport)
	supportsStrict := compat.supportsStrictMode
	messages, err := ConvertResponsesMessages(model, c, orOpenAIToolCallProviders, &ConvertResponsesMessagesOptions{
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: &compat.supportsMidConvoSystemMessages,
		SupportsAdditionalTools:        compat.supportsAdditionalTools,
		SupportsToolSearch:             compat.supportsToolSearch,
		ToolOptions: &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &supportsStrict,
			SupportsOpenAIGrammarTools: compat.supportsOpenAIGrammarTools,
		},
		ConfigurationUpdateEffort: configurationUpdateEffort,
	})
	if err != nil {
		return nil, err
	}

	var cacheRetentionOpt *ai.CacheRetention
	var env ai.ProviderEnv
	var sessionId *string
	var apiKey *string
	if options != nil {
		cacheRetentionOpt = options.CacheRetention
		env = options.Env
		sessionId = options.SessionId
		apiKey = options.ApiKey
	}
	cacheRetention := orResolveCacheRetention(cacheRetentionOpt, env)
	sendPromptCacheKey := cacheRetention != ai.CacheRetentionNone && compat.promptCacheKeyMode != ai.PromptCacheKeyModeDisabled
	omitUnsupportedFields := orIsChatGPTSignIn(model, apiKey)

	params := jsonx.NewObject()
	params.Set("model", model.Id)
	params.Set("input", messages)
	params.Set("stream", true)
	if sendPromptCacheKey {
		if clamped := ClampOpenAIPromptCacheKey(sessionId); clamped != nil {
			params.Set("prompt_cache_key", *clamped)
		}
	}
	if !omitUnsupportedFields {
		if retention := orGetPromptCacheRetention(compat, cacheRetention); retention != "" {
			params.Set("prompt_cache_retention", retention)
		}
		if cacheOpts := orGetPromptCacheOptions(compat, cacheRetention); cacheOpts != nil {
			params.Set("prompt_cache_options", cacheOpts)
		}
	}
	params.Set("store", false)

	if options != nil && options.MaxTokens != nil && *options.MaxTokens > 0 && compat.supportsMaxOutputTokens && !omitUnsupportedFields {
		params.Set("max_output_tokens", max(*options.MaxTokens, orMinOutputTokens))
	}
	if options != nil && options.Temperature != nil && !omitUnsupportedFields {
		params.Set("temperature", *options.Temperature)
	}
	if options != nil && options.ServiceTier != nil {
		params.Set("service_tier", *options.ServiceTier)
	}
	if len(transcriptTools.RequestTools) > 0 {
		convertedTools, err := ConvertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &supportsStrict,
			SupportsOpenAIGrammarTools: compat.supportsOpenAIGrammarTools,
		})
		if err != nil {
			return nil, err
		}
		params.Set("tools", convertedTools)
	}
	if options != nil && options.ToolChoice != nil {
		params.Set("tool_choice", options.ToolChoice)
	}

	var reasoningEffort string
	if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
		reasoningEffort = string(*options.ReasoningEffort)
	} else if options != nil && options.ReasoningSummary != nil && *options.ReasoningSummary != "" {
		reasoningEffort = "medium"
	}

	if model.Reasoning {
		if reasoningEffort != "" {
			requestedEffort := reasoningEffort
			if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
				requestedEffort = orsClampOpenAIReasoningEffort(model, ai.ModelThinkingLevel(*options.ReasoningEffort))
			}
			effort := requestedEffort
			if configurationUpdateEffort != nil {
				effort = orConfigUpdateRequestEffort
			}
			summary := "auto"
			if options != nil && options.ReasoningSummary != nil && *options.ReasoningSummary != "" {
				summary = *options.ReasoningSummary
			}
			reasoningObj := jsonx.NewObject()
			reasoningObj.Set("effort", effort)
			reasoningObj.Set("summary", summary)
			params.Set("reasoning", reasoningObj)
			params.Set("include", []any{"reasoning.encrypted_content"})
		} else if model.Provider != ai.KnownProviderGithubCopilot && (model.ThinkingLevelMap == nil || !model.ThinkingLevelMap.Off.IsNull()) {
			offEffort := "none"
			if model.ThinkingLevelMap != nil {
				if v, ok := model.ThinkingLevelMap.Off.Get(); ok {
					offEffort = v
				}
			}
			reasoningObj := jsonx.NewObject()
			reasoningObj.Set("effort", offEffort)
			params.Set("reasoning", reasoningObj)
		}
		if model.Provider == ai.KnownProviderXai {
			params.Set("include", []any{"reasoning.encrypted_content"})
		}
	}

	samplingLevel := ai.ModelThinkingLevelOff
	if reasoningEffort != "" {
		samplingLevel = ai.ModelThinkingLevel(reasoningEffort)
	}
	var reqSampling *jsonx.Object
	if options != nil {
		reqSampling = options.SamplingParams
	}
	if samplingParams := ResolveSamplingParams(model, samplingLevel, reqSampling); samplingParams != nil {
		params.Assign(samplingParams)
	}

	return params, nil
}

func orGetServiceTierCostMultiplier(modelId string, serviceTier *string) float64 {
	if serviceTier == nil {
		return 1
	}
	switch *serviceTier {
	case "flex":
		return 0.5
	case "priority", "fast":
		if modelId == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

func orApplyServiceTierPricing(usage *ai.Usage, serviceTier *string, modelId string) {
	if usage == nil {
		return
	}
	multiplier := orGetServiceTierCostMultiplier(modelId, serviceTier)
	if multiplier == 1 {
		return
	}
	usage.Cost.Input *= multiplier
	usage.Cost.Output *= multiplier
	usage.Cost.CacheRead *= multiplier
	usage.Cost.CacheWrite *= multiplier
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

// StreamOpenaiResponses streams responses from an OpenAI Responses API endpoint.
func StreamOpenaiResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := orExtractOptions(options)
	compat := orGetCompat(model)
	normalizedContext := ai.ResolveTranscript(c, &compat.supportsMidConvoSystemMessages)
	sig := ai.OperationSignal(ctx)

	go func() {
		configUpdateEffort := orGetConfigurationUpdateEffort(model, opts, compat)
		output := &ai.AssistantMessage{
			Role:                  "assistant",
			Content:               ai.ContentList{},
			Api:                   ai.Api(model.Api),
			Provider:              model.Provider,
			Model:                 model.Id,
			ProviderThinkingLevel: configUpdateEffort,
			Usage:                 ai.Usage{},
			StopReason:            ai.StopReasonPending,
			Timestamp:             js.DateNow(),
		}

		fail := func(err error) {
			if sig.Err() != nil {
				output.StopReason = ai.StopReasonAborted
			} else {
				output.StopReason = ai.StopReasonError
			}
			prefix := string(model.Provider) + " API error"
			if model.Provider == ai.KnownProviderOpenai {
				prefix = "OpenAI API error"
			}
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err), prefix)
			if strings.Contains(errMsg, "subscription_sharing_usage_limit_exceeded") {
				errMsg = errMsg + "\nCheck your ChatGPT usage: " + orChatGPTUsageURL
			} else if strings.Contains(errMsg, "subscription_sharing_workplace_individual_usage_limit_exceeded") {
				errMsg = errMsg + "\n" + orChatGPTWorkspaceLimitHint
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End(output)
		}

		apiKey, err := orGetClientApiKey(string(model.Provider), opts.ApiKey, opts.Headers)
		if err != nil {
			fail(err)
			return
		}
		cacheRetention := orResolveCacheRetention(opts.CacheRetention, opts.Env)
		cacheSessionId := ""
		if cacheRetention != ai.CacheRetentionNone && opts.SessionId != nil {
			cacheSessionId = *opts.SessionId
		}
		grammarToolInputProperties, err := CreateGrammarToolInputProperties(
			ai.GetDeclaredTools(normalizedContext.Messages),
			compat.supportsOpenAIGrammarTools,
		)
		if err != nil {
			fail(err)
			return
		}

		params, err := orBuildParams(model, normalizedContext, opts, compat, grammarToolInputProperties, configUpdateEffort)
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

		defaultHeaders := orBuildDefaultHeaders(model, normalizedContext, compat, opts.Headers, opts.Identity, cacheSessionId)
		resp, err := orsDoOpenAIStreamRequest(sig, orsOpenAIHttpConfig{
			BaseUrl:         model.BaseUrl,
			Path:            "/responses",
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

		err = ProcessResponsesStream(
			orsIterOpenAISSEEvents(sig, resp.Body, resp.Header),
			output,
			stream,
			model,
			&OpenAIResponsesStreamOptions{
				OnProviderStreamEvent:      opts.OnProviderStreamEvent,
				ServiceTier:                opts.ServiceTier,
				GrammarToolInputProperties: grammarToolInputProperties,
				ApplyServiceTierPricing: func(usage *ai.Usage, serviceTier *string) {
					orApplyServiceTierPricing(usage, serviceTier, model.Id)
				},
			},
		)
		if err != nil {
			fail(err)
			return
		}
		if sig.Err() != nil {
			fail(errors.New("Request was aborted"))
			return
		}
		if output.StopReason == ai.StopReasonPending {
			fail(errors.New("OpenAI Responses stream ended without a stop reason"))
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
		stream.End(output)
	}()

	return stream
}

// StreamSimpleOpenaiResponses adapts SimpleStreamOptions and delegates to StreamOpenaiResponses.
func StreamSimpleOpenaiResponses(
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
	opts := &OpenAIResponsesOptions{StreamOptions: base}
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
	}
	return StreamOpenaiResponses(ctx, model, c, opts)
}
