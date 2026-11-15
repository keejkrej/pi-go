// Ported from packages/ai/src/api/azure-openai-responses.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const aorMinOutputTokens = 16

var aorAzureToolCallProviders = map[string]struct{}{
	"openai":       {},
	"openai-codex": {},
	"opencode":     {},
	"azure":        {},
}

// AzureOpenAIResponsesOptions configures Azure OpenAI Responses API streaming requests.
type AzureOpenAIResponsesOptions struct {
	AzureEndpointOptions
	ReasoningEffort  *ai.ThinkingLevel `json:"reasoningEffort,omitzero"`
	ToolChoice       any               `json:"toolChoice,omitzero"`
	ReasoningSummary *string           `json:"reasoningSummary,omitzero"`
}

func aorFormatError(err error) string {
	return ai.FormatProviderError(ai.NormalizeProviderError(err), "Azure OpenAI API error")
}

func aorGetCompat(model *ai.Model) *ai.OpenAIResponsesCompat {
	if model == nil || model.Compat.V == nil {
		return nil
	}
	switch c := model.Compat.V.(type) {
	case *ai.OpenAIResponsesCompat:
		return c
	case *ai.UnknownModelCompat:
		if c != nil && c.Raw != nil {
			var decoded ai.OpenAIResponsesCompat
			if err := jsonx.Decode(c.Raw, &decoded); err == nil {
				return &decoded
			}
		}
	}
	return nil
}

func aorExtractOptions(options ai.ProviderStreamOptions) *AzureOpenAIResponsesOptions {
	if options == nil {
		return &AzureOpenAIResponsesOptions{}
	}
	if o, ok := options.(*AzureOpenAIResponsesOptions); ok && o != nil {
		cp := *o
		return &cp
	}
	if o, ok := options.(*AzureEndpointOptions); ok && o != nil {
		return &AzureOpenAIResponsesOptions{AzureEndpointOptions: *o}
	}
	var out AzureOpenAIResponsesOptions
	if base := options.BaseStreamOptions(); base != nil {
		out.StreamOptions = *base
	}
	return &out
}

func aorBuildDefaultHeaders(model *ai.Model, options *AzureOpenAIResponsesOptions) ai.ProviderHeaders {
	out := omap.NewMap[string, *string]()
	var target ai.ClientIdentityTarget
	var identity *ai.ClientIdentity
	if model != nil {
		target = ai.ClientIdentityTarget{Provider: string(model.Provider), BaseUrl: model.BaseUrl}
	}
	if options != nil {
		identity = options.Identity
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
	if options != nil && options.Headers != nil {
		for k, v := range options.Headers.All() {
			out.Set(k, v)
		}
	}
	return out
}

func aorBuildParams(
	model *ai.Model,
	c *ai.TranscriptContext,
	options *AzureOpenAIResponsesOptions,
	deploymentName string,
	grammarToolInputProperties map[string]string,
) (*jsonx.Object, error) {
	compat := aorGetCompat(model)
	supportsMidConvo := false
	supportsAdditionalTools := false
	supportsToolSearch := false
	supportsStrictMode := true
	supportsGrammarTools := false
	promptCacheKeyDisabled := false
	if compat != nil {
		if compat.SupportsMidConvoSystemMessages != nil {
			supportsMidConvo = *compat.SupportsMidConvoSystemMessages
		}
		if compat.SupportsAdditionalTools != nil {
			supportsAdditionalTools = *compat.SupportsAdditionalTools
		}
		if compat.SupportsToolSearch != nil {
			supportsToolSearch = *compat.SupportsToolSearch
		}
		if compat.SupportsStrictMode != nil {
			supportsStrictMode = *compat.SupportsStrictMode
		}
		if compat.SupportsOpenAIGrammarTools != nil {
			supportsGrammarTools = *compat.SupportsOpenAIGrammarTools
		}
		if compat.PromptCacheKeyMode != nil && *compat.PromptCacheKeyMode == ai.PromptCacheKeyModeDisabled {
			promptCacheKeyDisabled = true
		}
	}

	transcriptTools := ai.ResolveTranscriptTools(
		c.Messages,
		GetResponsesToolSupport(ResponsesToolSupportOptions{
			SupportsMidConvoSystemMessages: supportsMidConvo,
			SupportsAdditionalTools:        supportsAdditionalTools,
			SupportsToolSearch:             supportsToolSearch,
		}),
	)
	messages, err := ConvertResponsesMessages(model, c, aorAzureToolCallProviders, &ConvertResponsesMessagesOptions{
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: &supportsMidConvo,
		SupportsAdditionalTools:        supportsAdditionalTools,
		SupportsToolSearch:             supportsToolSearch,
		ToolOptions: &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsGrammarTools,
		},
	})
	if err != nil {
		return nil, err
	}

	params := jsonx.NewObject()
	params.Set("model", deploymentName)
	params.Set("input", messages)
	params.Set("stream", true)
	if !promptCacheKeyDisabled && options != nil {
		if clamped := ClampOpenAIPromptCacheKey(options.SessionId); clamped != nil {
			params.Set("prompt_cache_key", *clamped)
		}
	}
	params.Set("store", false)

	if options != nil && options.MaxTokens != nil && *options.MaxTokens > 0 {
		params.Set("max_output_tokens", max(*options.MaxTokens, aorMinOutputTokens))
	}
	if options != nil && options.Temperature != nil {
		params.Set("temperature", *options.Temperature)
	}
	if len(transcriptTools.RequestTools) > 0 {
		convertedTools, err := ConvertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsGrammarTools,
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
			effort := reasoningEffort
			if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
				effort = orsClampOpenAIReasoningEffort(model, ai.ModelThinkingLevel(*options.ReasoningEffort))
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
		} else if model.ThinkingLevelMap == nil || !model.ThinkingLevelMap.Off.IsNull() {
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

// StreamAzureOpenaiResponses streams responses from an Azure OpenAI Responses API endpoint.
func StreamAzureOpenaiResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := aorExtractOptions(options)
	compat := aorGetCompat(model)
	var supportsMidConvo *bool
	supportsGrammar := false
	if compat != nil {
		supportsMidConvo = compat.SupportsMidConvoSystemMessages
		if compat.SupportsOpenAIGrammarTools != nil {
			supportsGrammar = *compat.SupportsOpenAIGrammarTools
		}
	}
	normalizedContext := ai.ResolveTranscript(c, supportsMidConvo)
	sig := ai.OperationSignal(ctx)

	go func() {
		deploymentName := ResolveDeploymentName(model, &opts.AzureEndpointOptions)
		output := &ai.AssistantMessage{
			Role:       "assistant",
			Content:    ai.ContentList{},
			Api:        ai.KnownApiAzureOpenaiResponses,
			Provider:   model.Provider,
			Model:      model.Id,
			Usage:      ai.Usage{},
			StopReason: ai.StopReasonPending,
			Timestamp:  js.DateNow(),
		}

		fail := func(err error) {
			if sig.Err() != nil {
				output.StopReason = ai.StopReasonAborted
			} else {
				output.StopReason = ai.StopReasonError
			}
			errMsg := aorFormatError(err)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End(output)
		}

		if opts.ApiKey == nil || *opts.ApiKey == "" {
			fail(fmt.Errorf("No API key for provider: %s", model.Provider))
			return
		}
		resolvedCfg, err := ResolveAzureConfig(model, &opts.AzureEndpointOptions)
		if err != nil {
			fail(err)
			return
		}
		grammarToolInputProperties, err := CreateGrammarToolInputProperties(
			ai.GetDeclaredTools(normalizedContext.Messages),
			supportsGrammar,
		)
		if err != nil {
			fail(err)
			return
		}
		params, err := aorBuildParams(model, normalizedContext, opts, deploymentName, grammarToolInputProperties)
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

		defaultHeaders := aorBuildDefaultHeaders(model, opts)
		resp, err := orsDoOpenAIStreamRequest(sig, orsOpenAIHttpConfig{
			BaseUrl:         resolvedCfg.BaseUrl,
			Path:            "/responses",
			ApiVersion:      resolvedCfg.ApiVersion,
			ApiKey:          *opts.ApiKey,
			UseAzureApiKey:  true,
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
				GrammarToolInputProperties: grammarToolInputProperties,
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
			fail(errors.New("Azure OpenAI Responses stream ended without a stop reason"))
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

// StreamSimpleAzureOpenaiResponses adapts SimpleStreamOptions and delegates to StreamAzureOpenaiResponses.
func StreamSimpleAzureOpenaiResponses(
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
	opts := &AzureOpenAIResponsesOptions{
		AzureEndpointOptions: AzureEndpointOptions{StreamOptions: base},
	}
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
	return StreamAzureOpenaiResponses(ctx, model, c, opts)
}
