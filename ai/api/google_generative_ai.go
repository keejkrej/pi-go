// Ported from packages/ai/src/api/google-generative-ai.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/sse"
)

// GoogleThinkingOption configures explicit thinking behavior on GoogleOptions / GoogleVertexOptions.
type GoogleThinkingOption struct {
	Enabled      bool                 `json:"enabled"`
	BudgetTokens *int                 `json:"budgetTokens,omitzero"`
	Level        *GoogleThinkingLevel `json:"level,omitzero"`
}

// GoogleOptions configures Google Generative AI streaming requests.
type GoogleOptions struct {
	ai.StreamOptions
	ToolChoice *string               `json:"toolChoice,omitzero"`
	Thinking   *GoogleThinkingOption `json:"thinking,omitzero"`
}

var (
	ggaApiVersionSuffixRe = regexp.MustCompile(`/v\d+(?:[a-zA-Z]+\d*)?(?:/|$)`)
)

// StreamGoogleGenerativeAi streams a response from the Google Generative AI (Gemini) API
// using raw net/http + internal/sse against the REST SSE endpoint.
func StreamGoogleGenerativeAi(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()

	go func() {
		if ctx == nil {
			ctx = context.Background()
		}
		if model == nil {
			ai.AbortNewError("model is required")
			msg := ggaInitOutput(nil)
			errMsg := "Google Generative AI stream called with nil model"
			msg.StopReason = ai.StopReasonError
			msg.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
			stream.End(msg)
			return
		}

		opts := ggaExtractOptions(options)
		normCtx := c
		if normCtx == nil {
			normCtx = &ai.TranscriptContext{}
		}
		output := ggaInitOutput(model)

		defer func() {
			if r := recover(); r != nil {
				errMsg := ai.FormatThrownValue(r)
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
			}
		}()

		apiKey := ggaResolveApiKey(&opts)
		if apiKey == "" {
			errMsg := "Google Generative AI API key is required. Set GEMINI_API_KEY environment variable or pass it as an argument."
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		params, err := ggaBuildParams(model, normCtx, &opts)
		if err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		if opts.OnPayload != nil {
			updated, err := opts.OnPayload(params, model)
			if err != nil {
				errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
			if updated != nil {
				if obj, ok := updated.(*jsonx.Object); ok && obj != nil {
					params = obj
				}
			}
		}

		restBody := ggaParamsToRESTBody(params)
		bodyBytes, err := jsonx.Marshal(restBody)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to marshal request body: %v", err)
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		endpoint := ggaBuildEndpointUrl(model)
		headers := ggaBuildHeaders(model, &opts, "x-goog-api-key", apiKey)

		httpClient := clshResolveHTTPClient(opts.HTTPClient, opts.Env)
		var hdrTimeout int64
		if opts.TimeoutMs != nil {
			hdrTimeout = *opts.TimeoutMs
		}
		maxRetries := 0
		if opts.MaxRetries != nil {
			maxRetries = *opts.MaxRetries
		}

		resp, cancelHdrWait, err := ggaDoStreamRequestWithRetry(ctx, httpClient, endpoint, headers, bodyBytes, hdrTimeout, &GsGoogleRetryConfig{
			MaxRetries:      maxRetries,
			MaxRetryDelayMs: opts.MaxRetryDelayMs,
		})
		if cancelHdrWait != nil {
			defer cancelHdrWait()
		}
		if err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			if ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
				errMsg = "Request was aborted"
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}
		defer resp.Body.Close()

		if opts.OnResponse != nil {
			if cbErr := opts.OnResponse(&ai.ProviderResponse{
				Status:  resp.StatusCode,
				Headers: ai.HeadersToRecord(resp.Header),
			}, model); cbErr != nil {
				errMsg := ai.FormatProviderError(ai.NormalizeProviderError(cbErr))
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
		}

		if err := ggaConsumeSSEStream(ctx, resp.Body, model, stream, output, opts.OnProviderStreamEvent); err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			if ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
				errMsg = "Request was aborted"
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		if ctx.Err() != nil {
			errMsg := "Request was aborted"
			output.StopReason = ai.StopReasonAborted
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: output})
			stream.End(output)
			return
		}

		if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
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

// StreamSimpleGoogleGenerativeAi maps uniform SimpleStreamOptions onto GoogleOptions
// and invokes StreamGoogleGenerativeAi.
func StreamSimpleGoogleGenerativeAi(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	var env ai.ProviderEnv
	var explicitApiKey *string
	if options != nil {
		env = options.Env
		explicitApiKey = options.ApiKey
	}
	var resolvedKey *string
	if explicitApiKey != nil && *explicitApiKey != "" {
		resolvedKey = explicitApiKey
	} else if envKey := ai.GetEnvApiKey(string(ai.KnownProviderGoogle), env); envKey != nil {
		resolvedKey = envKey
	}

	base := BuildBaseOptions(model, c, options, resolvedKey)
	gOpts := &GoogleOptions{
		StreamOptions: base,
	}
	if options != nil && options.ToolChoice != nil {
		tc := string(*options.ToolChoice)
		gOpts.ToolChoice = &tc
	}

	var customBudgets *ai.ThinkingBudgets
	if options != nil {
		customBudgets = options.ThinkingBudgets
	}

	if options == nil || options.Reasoning == nil {
		if model != nil && model.Reasoning {
			disabled := false
			gOpts.Thinking = &GoogleThinkingOption{Enabled: disabled}
		}
		return StreamGoogleGenerativeAi(ctx, model, c, gOpts)
	}

	resolved := ResolveGoogleThinkingLevel(model, *options.Reasoning)
	if strings.EqualFold(resolved, "off") {
		if model != nil && model.Reasoning {
			gOpts.Thinking = &GoogleThinkingOption{Enabled: false}
		}
		return StreamGoogleGenerativeAi(ctx, model, c, gOpts)
	}

	if UsesGoogleThinkingLevel(model) {
		lvl := ToGoogleThinkingLevel(resolved)
		gOpts.Thinking = &GoogleThinkingOption{
			Enabled: true,
			Level:   &lvl,
		}
		return StreamGoogleGenerativeAi(ctx, model, c, gOpts)
	}

	budget := ggaGetGoogleBudget(model, resolved, customBudgets)
	gOpts.Thinking = &GoogleThinkingOption{
		Enabled:      true,
		BudgetTokens: &budget,
	}
	return StreamGoogleGenerativeAi(ctx, model, c, gOpts)
}

func ggaGetGoogleBudget(model *ai.Model, level string, customBudgets *ai.ThinkingBudgets) int {
	norm := strings.ToLower(strings.TrimSpace(level))
	if customBudgets != nil {
		switch norm {
		case "minimal":
			if customBudgets.Minimal != nil {
				return *customBudgets.Minimal
			}
		case "low":
			if customBudgets.Low != nil {
				return *customBudgets.Low
			}
		case "medium":
			if customBudgets.Medium != nil {
				return *customBudgets.Medium
			}
		case "high":
			if customBudgets.High != nil {
				return *customBudgets.High
			}
		}
	}

	id := ""
	if model != nil {
		id = model.Id
	}
	if strings.Contains(id, "2.5-pro") {
		switch norm {
		case "minimal":
			return 128
		case "low":
			return 2048
		case "medium":
			return 8192
		case "high":
			return 32768
		}
	}
	if strings.Contains(id, "2.5-flash-lite") {
		switch norm {
		case "minimal":
			return 512
		case "low":
			return 2048
		case "medium":
			return 8192
		case "high":
			return 24576
		}
	}
	if strings.Contains(id, "2.5-flash") {
		switch norm {
		case "minimal":
			return 128
		case "low":
			return 2048
		case "medium":
			return 8192
		case "high":
			return 24576
		}
	}
	return -1
}

func ggaExtractOptions(options ai.ProviderStreamOptions) GoogleOptions {
	if options == nil {
		return GoogleOptions{}
	}
	switch o := options.(type) {
	case *GoogleOptions:
		if o != nil {
			return *o
		}
	case *ai.SimpleStreamOptions:
		if o != nil {
			out := GoogleOptions{StreamOptions: o.StreamOptions}
			if o.ToolChoice != nil {
				s := string(*o.ToolChoice)
				out.ToolChoice = &s
			}
			return out
		}
	case *ai.StreamOptions:
		if o != nil {
			return GoogleOptions{StreamOptions: *o}
		}
	default:
		if base := options.BaseStreamOptions(); base != nil {
			return GoogleOptions{StreamOptions: *base}
		}
	}
	return GoogleOptions{}
}

func ggaResolveApiKey(opts *GoogleOptions) string {
	if opts != nil && opts.ApiKey != nil && strings.TrimSpace(*opts.ApiKey) != "" {
		return strings.TrimSpace(*opts.ApiKey)
	}
	var env ai.ProviderEnv
	if opts != nil {
		env = opts.Env
	}
	if keyPtr := ai.GetEnvApiKey(string(ai.KnownProviderGoogle), env); keyPtr != nil && strings.TrimSpace(*keyPtr) != "" {
		return strings.TrimSpace(*keyPtr)
	}
	return ""
}

func ggaInitOutput(model *ai.Model) *ai.AssistantMessage {
	var apiName ai.Api
	var provider ai.ProviderId
	var modelId string
	if model != nil {
		apiName = ai.Api(model.Api)
		provider = model.Provider
		modelId = model.Id
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        apiName,
		Provider:   provider,
		Model:      modelId,
		Usage:      ai.Usage{},
		StopReason: ai.StopReasonStop,
		Timestamp:  js.DateNow(),
	}
}

func ggaAbortOrError(ctx context.Context) ai.StopReason {
	if ctx != nil && ctx.Err() != nil {
		return ai.StopReasonAborted
	}
	return ai.StopReasonError
}

func ggaBuildEndpointUrl(model *ai.Model) string {
	base := "https://generativelanguage.googleapis.com"
	if model != nil && strings.TrimSpace(model.BaseUrl) != "" {
		base = strings.TrimRight(strings.TrimSpace(model.BaseUrl), "/")
	}
	if !ggaApiVersionSuffixRe.MatchString(base) {
		base += "/v1beta"
	}
	modelPath := ggaFormatModelPath(model.Id)
	return base + "/" + modelPath + ":streamGenerateContent?alt=sse"
}

func ggaFormatModelPath(modelId string) string {
	trimmed := strings.TrimLeft(strings.TrimSpace(modelId), "/")
	if strings.HasPrefix(trimmed, "models/") || strings.HasPrefix(trimmed, "tunedModels/") {
		return trimmed
	}
	return "models/" + trimmed
}

func ggaBuildHeaders(model *ai.Model, opts *GoogleOptions, authHeaderName, authHeaderVal string) http.Header {
	var sources []ai.ProviderHeaders
	var baseUrl string
	var provider string
	if model != nil {
		provider = string(model.Provider)
		baseUrl = model.BaseUrl
	}
	identity := ai.GetIdentityHeaders(ai.ClientIdentityTarget{Provider: provider, BaseUrl: baseUrl}, opts.Identity)
	if identity != nil && identity.Len() > 0 {
		sources = append(sources, clshToProviderHeaders(identity))
	}
	if model != nil && model.Headers != nil {
		sources = append(sources, clshToProviderHeaders(model.Headers))
	}
	if opts.Headers != nil {
		sources = append(sources, opts.Headers)
	}

	merged := ai.ProviderHeadersToOrderedRecord(sources...)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	headers.Set("x-goog-api-client", "google-genai-sdk/2.32.0")
	if authHeaderName != "" && authHeaderVal != "" {
		headers.Set(authHeaderName, authHeaderVal)
	}
	if merged != nil {
		for k, v := range merged.All() {
			headers.Set(k, v)
		}
	}
	return headers
}

func ggaBuildParams(model *ai.Model, c *ai.TranscriptContext, opts *GoogleOptions) (*jsonx.Object, error) {
	contents := ConvertGoogleMessages(model, c)
	config := jsonx.NewObject()

	var thinkingLevel ai.ModelThinkingLevel = ai.ModelThinkingLevelOff
	if opts.Thinking != nil && opts.Thinking.Enabled {
		if opts.Thinking.Level != nil {
			thinkingLevel = ai.ModelThinkingLevel(strings.ToLower(string(*opts.Thinking.Level)))
		} else {
			thinkingLevel = ai.ModelThinkingLevelLow
		}
	}
	if sampling := ResolveSamplingParams(model, thinkingLevel, opts.SamplingParams); sampling != nil {
		for _, k := range sampling.Keys() {
			val, _ := sampling.Get(k)
			config.Set(k, val)
		}
	}
	if opts.Temperature != nil {
		config.Set("temperature", *opts.Temperature)
	}
	if opts.MaxTokens != nil {
		config.Set("maxOutputTokens", *opts.MaxTokens)
	}

	collapsed := ai.ResolveTranscript(c, nil)
	if initSys := ai.GetInitialSystemMessage(collapsed.Messages); initSys != nil {
		if sysPrompt := ai.GetSystemMessageText(initSys); sysPrompt != "" {
			config.Set("systemInstruction", ai.SanitizeSurrogates(sysPrompt))
		}
	}

	currentTools := ai.GetCurrentTools(c.Messages)
	if len(currentTools) > 0 {
		supportsStrict := SupportsGoogleStrictToolSampling(model.Id)
		var tcPtr *ai.ToolChoice
		if opts.ToolChoice != nil {
			tc := ai.ToolChoice(*opts.ToolChoice)
			tcPtr = &tc
		}
		mode, err := ResolveGoogleFunctionCallingMode(currentTools, tcPtr, supportsStrict)
		if err != nil {
			return nil, err
		}
		convertedTools, err := ConvertGoogleTools(currentTools, false, supportsStrict)
		if err != nil {
			return nil, err
		}
		if len(convertedTools) > 0 {
			config.Set("tools", convertedTools)
		}
		if mode != "" {
			fcc := jsonx.NewObject()
			fcc.Set("mode", mode)
			tcObj := jsonx.NewObject()
			tcObj.Set("functionCallingConfig", fcc)
			config.Set("toolConfig", tcObj)
		}
	}

	config.Set("automaticContinuation", false)

	if opts.Thinking != nil {
		if opts.Thinking.Enabled && model.Reasoning {
			var tc GoogleThinkingConfig
			inc := true
			tc.IncludeThoughts = &inc
			if opts.Thinking.BudgetTokens != nil {
				b := *opts.Thinking.BudgetTokens
				tc.ThinkingBudget = &b
			} else if opts.Thinking.Level != nil {
				lvl := *opts.Thinking.Level
				tc.ThinkingLevel = &lvl
			}
			config.Set("thinkingConfig", tc.ToObject())
		} else if !opts.Thinking.Enabled && model.Reasoning {
			disabled := GetDisabledGoogleThinkingConfig(model)
			config.Set("thinkingConfig", disabled.ToObject())
		}
	}

	params := jsonx.NewObject()
	params.Set("model", model.Id)
	params.Set("contents", contents)
	params.Set("config", config)
	return params, nil
}

func ggaParamsToRESTBody(params *jsonx.Object) *jsonx.Object {
	body := jsonx.NewObject()
	if params == nil {
		return body
	}
	if contents, ok := params.Get("contents"); ok {
		body.Set("contents", contents)
	}
	cfgVal, hasCfg := params.Get("config")
	cfg, _ := cfgVal.(*jsonx.Object)
	if !hasCfg || cfg == nil {
		return body
	}

	genCfg := jsonx.NewObject()
	for _, k := range cfg.Keys() {
		v, _ := cfg.Get(k)
		switch k {
		case "automaticContinuation", "abortSignal", "httpOptions":
			continue
		case "systemInstruction":
			switch s := v.(type) {
			case string:
				if s != "" {
					part := jsonx.NewObject()
					part.Set("text", s)
					si := jsonx.NewObject()
					si.Set("parts", []*jsonx.Object{part})
					body.Set("systemInstruction", si)
				}
			case *jsonx.Object:
				if s != nil {
					body.Set("systemInstruction", s)
				}
			}
		case "tools":
			body.Set("tools", v)
		case "toolConfig":
			body.Set("toolConfig", v)
		case "safetySettings":
			body.Set("safetySettings", v)
		case "cachedContent":
			body.Set("cachedContent", v)
		case "labels":
			body.Set("labels", v)
		default:
			genCfg.Set(k, v)
		}
	}
	if len(genCfg.Keys()) > 0 {
		body.Set("generationConfig", genCfg)
	}
	return body
}

func ggaDoStreamRequestWithRetry(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	headers http.Header,
	bodyBytes []byte,
	hdrTimeoutMs int64,
	retryCfg *GsGoogleRetryConfig,
) (*http.Response, context.CancelFunc, error) {
	type attemptResult struct {
		resp   *http.Response
		cancel context.CancelFunc
	}

	res, err := RetryGoogleRequest(ctx, func(attemptCtx context.Context) (attemptResult, error) {
		hdrWaitCtx, cancelHdrWait := context.WithCancel(attemptCtx)
		if hdrTimeoutMs > 0 {
			timer := ggaStartHeaderTimeoutWatcher(attemptCtx, hdrWaitCtx, cancelHdrWait, hdrTimeoutMs)
			defer timer()
		}

		req, err := http.NewRequestWithContext(hdrWaitCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			cancelHdrWait()
			return attemptResult{}, err
		}
		for k, vals := range headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			cancelHdrWait()
			return attemptResult{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			rawBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			cancelHdrWait()
			return attemptResult{}, &gsHTTPStatusError{
				Status:  resp.StatusCode,
				Headers: resp.Header,
				Message: ggaFormatHTTPError(resp.StatusCode, rawBody),
			}
		}
		return attemptResult{resp: resp, cancel: cancelHdrWait}, nil
	}, retryCfg)
	if err != nil {
		return nil, nil, err
	}
	return res.resp, res.cancel, nil
}

func ggaStartHeaderTimeoutWatcher(parentCtx, waitCtx context.Context, cancel context.CancelFunc, timeoutMs int64) func() {
	doneCh := make(chan struct{})
	go func() {
		timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		select {
		case <-doneCh:
			timer.Stop()
		case <-parentCtx.Done():
			timer.Stop()
		case <-timer.C:
			cancel()
		case <-waitCtx.Done():
			timer.Stop()
		}
	}()
	return func() { close(doneCh) }
}

func ggaFormatHTTPError(status int, body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	if parsed, err := jsonx.Parse([]byte(trimmed)); err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			if errVal, ok := obj.Get("error"); ok {
				if errObj, ok := errVal.(*jsonx.Object); ok && errObj != nil {
					if msg, ok := errObj.Get("message"); ok {
						if ms, ok := msg.(string); ok && ms != "" {
							return fmt.Sprintf("HTTP %d: %s", status, ms)
						}
					}
				}
			}
		}
	}
	return fmt.Sprintf("HTTP %d: %s", status, ai.TruncateErrorText(trimmed, 2000))
}

func ggaConsumeSSEStream(
	ctx context.Context,
	body io.Reader,
	model *ai.Model,
	stream *ai.AssistantMessageEventStream,
	output *ai.AssistantMessage,
	onStreamEvent func(data any, model *ai.Model) error,
) error {
	stream.Push(&ai.AssistantMessageEventStart{
		Type:    "start",
		Partial: output,
	})

	reader := sse.NewReader(body)
	currentBlockKind := ""
	currentBlockIdx := -1

	closeOpenBlock := func() {
		if currentBlockIdx < 0 || currentBlockIdx >= len(output.Content) {
			currentBlockKind = ""
			currentBlockIdx = -1
			return
		}
		switch currentBlockKind {
		case "thinking":
			if tb, ok := output.Content[currentBlockIdx].(*ai.ThinkingContent); ok {
				stream.Push(&ai.AssistantMessageEventThinkingEnd{
					Type:         "thinking_end",
					ContentIndex: currentBlockIdx,
					Content:      tb.Thinking,
					Partial:      output,
				})
			}
		case "text":
			if tb, ok := output.Content[currentBlockIdx].(*ai.TextContent); ok {
				stream.Push(&ai.AssistantMessageEventTextEnd{
					Type:         "text_end",
					ContentIndex: currentBlockIdx,
					Content:      tb.Text,
					Partial:      output,
				})
			}
		}
		currentBlockKind = ""
		currentBlockIdx = -1
	}

	usedToolIds := make(map[string]bool)

	for {
		if ctx.Err() != nil {
			return ai.AbortNewError("Request was aborted")
		}
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		dataStr := strings.TrimSpace(ev.Data)
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		parsed, err := jsonx.Parse([]byte(dataStr))
		if err != nil {
			continue
		}
		chunk, ok := parsed.(*jsonx.Object)
		if !ok || chunk == nil {
			continue
		}

		if onStreamEvent != nil {
			if err := onStreamEvent(chunk, model); err != nil {
				return err
			}
		}

		if rid, ok := chunk.Get("responseId"); ok {
			if s, ok := rid.(string); ok && s != "" {
				output.ResponseId = &s
			}
		}

		if errVal, ok := chunk.Get("error"); ok && errVal != nil {
			if errObj, ok := errVal.(*jsonx.Object); ok && errObj != nil {
				code := 500
				if c, ok := errObj.Get("code"); ok {
					code = ggaToInt(c)
				}
				msgStr := ai.SafeJsonStringify(errObj)
				if m, ok := errObj.Get("message"); ok {
					if ms, ok := m.(string); ok && ms != "" {
						msgStr = ms
					}
				}
				return &gsHTTPStatusError{
					Status:  code,
					Message: fmt.Sprintf("HTTP %d: %s", code, msgStr),
				}
			}
		}

		if candVal, ok := chunk.Get("candidates"); ok {
			if cands, ok := candVal.([]any); ok && len(cands) > 0 {
				if cand, ok := cands[0].(*jsonx.Object); ok && cand != nil {
					if contentVal, ok := cand.Get("content"); ok {
						if contentObj, ok := contentVal.(*jsonx.Object); ok && contentObj != nil {
							if partsVal, ok := contentObj.Get("parts"); ok {
								if parts, ok := partsVal.([]any); ok {
									for _, pAny := range parts {
										part, ok := pAny.(*jsonx.Object)
										if !ok || part == nil {
											continue
										}

										var partSig *string
										if sigVal, ok := part.Get("thoughtSignature"); ok {
											if sigStr, ok := sigVal.(string); ok && sigStr != "" {
												s := sigStr
												partSig = &s
											}
										}

										if textVal, hasText := part.Get("text"); hasText {
											textStr, _ := textVal.(string)
											isThinking := IsGoogleThinkingPart(part)
											desiredKind := "text"
											if isThinking {
												desiredKind = "thinking"
											}
											if currentBlockKind != desiredKind {
												closeOpenBlock()
												currentBlockIdx = len(output.Content)
												currentBlockKind = desiredKind
												if isThinking {
													output.Content = append(output.Content, &ai.ThinkingContent{
														Type:              "thinking",
														Thinking:          "",
														ThinkingSignature: partSig,
													})
													stream.Push(&ai.AssistantMessageEventThinkingStart{
														Type:         "thinking_start",
														ContentIndex: currentBlockIdx,
														Partial:      output,
													})
												} else {
													output.Content = append(output.Content, &ai.TextContent{
														Type:          "text",
														Text:          "",
														TextSignature: partSig,
													})
													stream.Push(&ai.AssistantMessageEventTextStart{
														Type:         "text_start",
														ContentIndex: currentBlockIdx,
														Partial:      output,
													})
												}
											}

											if isThinking {
												tb := output.Content[currentBlockIdx].(*ai.ThinkingContent)
												tb.Thinking += textStr
												tb.ThinkingSignature = RetainGoogleThoughtSignature(tb.ThinkingSignature, partSig)
												if textStr != "" {
													stream.Push(&ai.AssistantMessageEventThinkingDelta{
														Type:         "thinking_delta",
														ContentIndex: currentBlockIdx,
														Delta:        textStr,
														Partial:      output,
													})
												}
											} else {
												tb := output.Content[currentBlockIdx].(*ai.TextContent)
												tb.Text += textStr
												tb.TextSignature = RetainGoogleThoughtSignature(tb.TextSignature, partSig)
												if textStr != "" {
													stream.Push(&ai.AssistantMessageEventTextDelta{
														Type:         "text_delta",
														ContentIndex: currentBlockIdx,
														Delta:        textStr,
														Partial:      output,
													})
												}
											}
										}

										if fcVal, hasFC := part.Get("functionCall"); hasFC {
											if fc, ok := fcVal.(*jsonx.Object); ok && fc != nil {
												closeOpenBlock()
												fcName, _ := fc.Get("name")
												nameStr, _ := fcName.(string)
												callId := ""
												if idVal, ok := fc.Get("id"); ok {
													if s, ok := idVal.(string); ok {
														callId = s
													}
												}
												if callId == "" || usedToolIds[callId] {
													callId = GsNextToolCallId(nameStr)
												}
												usedToolIds[callId] = true

												argsObj := jsonx.NewObject()
												if argsVal, ok := fc.Get("args"); ok {
													if obj, ok := argsVal.(*jsonx.Object); ok && obj != nil {
														argsObj = obj
													}
												}

												tcBlock := &ai.ToolCall{
													Type:             "toolCall",
													Id:               callId,
													Name:             nameStr,
													Arguments:        argsObj,
													ThoughtSignature: partSig,
												}
												idx := len(output.Content)
												output.Content = append(output.Content, tcBlock)

												stream.Push(&ai.AssistantMessageEventToolcallStart{
													Type:         "toolcall_start",
													ContentIndex: idx,
													Partial:      output,
												})
												argsJSON := "{}"
												if encoded, err := jsonx.Marshal(argsObj); err == nil && utf8.Valid(encoded) {
													argsJSON = string(encoded)
												}
												stream.Push(&ai.AssistantMessageEventToolcallDelta{
													Type:         "toolcall_delta",
													ContentIndex: idx,
													Delta:        argsJSON,
													Partial:      output,
												})
												stream.Push(&ai.AssistantMessageEventToolcallEnd{
													Type:         "toolcall_end",
													ContentIndex: idx,
													ToolCall:     tcBlock,
													Partial:      output,
												})
											}
										}
									}
								}
							}
						}
					}

					if frVal, ok := cand.Get("finishReason"); ok {
						if frStr, ok := frVal.(string); ok && frStr != "" {
							raw := frStr
							output.RawStopReason = &raw
							mapped := MapGoogleStopReason(frStr)
							output.StopReason = mapped
							if mapped == ai.StopReasonError {
								errStr := "Provider stopped with: " + frStr
								output.ErrorMessage = &errStr
							}
						}
					}
				}
			}
		}

		if usageVal, ok := chunk.Get("usageMetadata"); ok {
			if usageObj, ok := usageVal.(*jsonx.Object); ok && usageObj != nil {
				promptTokens := ggaGetIntField(usageObj, "promptTokenCount")
				candTokens := ggaGetIntField(usageObj, "candidatesTokenCount")
				thoughtTokens := ggaGetIntField(usageObj, "thoughtsTokenCount")
				cachedTokens := ggaGetIntField(usageObj, "cachedContentTokenCount")
				totalTokens := ggaGetIntField(usageObj, "totalTokenCount")

				inputTokens := promptTokens - cachedTokens
				if inputTokens < 0 {
					inputTokens = 0
				}
				outputTokens := candTokens + thoughtTokens
				reasoningTokens := thoughtTokens

				output.Usage.Input = inputTokens
				output.Usage.Output = outputTokens
				output.Usage.CacheRead = cachedTokens
				output.Usage.CacheWrite = 0
				output.Usage.Reasoning = &reasoningTokens
				output.Usage.TotalTokens = totalTokens
				ai.CalculateCost(model, &output.Usage)
			}
		}
	}

	closeOpenBlock()

	if output.StopReason == ai.StopReasonStop {
		for _, block := range output.Content {
			if _, ok := block.(*ai.ToolCall); ok {
				output.StopReason = ai.StopReasonToolUse
				break
			}
		}
	}
	return nil
}

func ggaGetIntField(obj *jsonx.Object, key string) int {
	if obj == nil {
		return 0
	}
	v, ok := obj.Get(key)
	if !ok {
		return 0
	}
	return ggaToInt(v)
}

func ggaToInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return 0
}
