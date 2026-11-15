// Ported from packages/ai/src/api/anthropic-messages.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/sse"
)

// AnthropicEffort controls adaptive thinking allocation for Claude 4.6+ models.
type AnthropicEffort string

const (
	AnthropicEffortLow    AnthropicEffort = "low"
	AnthropicEffortMedium AnthropicEffort = "medium"
	AnthropicEffortHigh   AnthropicEffort = "high"
	AnthropicEffortXhigh  AnthropicEffort = "xhigh"
	AnthropicEffortMax    AnthropicEffort = "max"
)

// AnthropicThinkingDisplay controls how thinking text is returned by the Anthropic API.
type AnthropicThinkingDisplay string

const (
	AnthropicThinkingDisplaySummarized AnthropicThinkingDisplay = "summarized"
	AnthropicThinkingDisplayOmitted    AnthropicThinkingDisplay = "omitted"
)

// AnthropicToolChoice represents an explicit Anthropic tool selection directive.
type AnthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitzero"`
}

// AnthropicOptions configures Anthropic Messages streaming requests.
type AnthropicOptions struct {
	ai.StreamOptions
	ThinkingEnabled      *bool                     `json:"thinkingEnabled,omitzero"`
	ThinkingBudgetTokens *int                      `json:"thinkingBudgetTokens,omitzero"`
	Effort               *AnthropicEffort          `json:"effort,omitzero"`
	ThinkingDisplay      *AnthropicThinkingDisplay `json:"thinkingDisplay,omitzero"`
	InterleavedThinking  *bool                     `json:"interleavedThinking,omitzero"`
	ToolChoice           *AnthropicToolChoice      `json:"toolChoice,omitzero"`
}

const (
	amClaudeCodeVersion               = "2.1.322"
	amAnthropicVersionHeader          = "2023-06-01"
	amFineGrainedToolStreamingBeta    = "fine-grained-tool-streaming-2025-05-14"
	amInterleavedThinkingBeta         = "interleaved-thinking-2025-05-14"
	amServerSideFallbackBeta          = "server-side-fallback-2026-07-01"
	amMidConversationOutputConfigBeta = "mid-conversation-output-config-2026-07-01"
	amThinkingBindingControlsBeta     = "thinking-binding-controls-2026-08-01"
	amInlineToolsBeta                 = "inline-tools-2026-09-15"
	amMessageThreadsBeta              = "message-threads-2026-08-12"
	amOAuthAPIBetaHeader              = "oauth-2025-04-20"
	amFederationBetaHeader            = "oidc-federation-2026-04-01"
	amGrantTypeJWTBearer              = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	amTokenEndpointPath               = "/v1/oauth/token"
	amMaxThreadStateEntries           = 10_000
	amFederationAdvisoryRefreshSec    = 120
	amFederationMandatoryRefreshSec   = 30
	amMaxFederationAssertionBytes     = 16 * 1024
)

var (
	amClaudeCodeTools = []string{
		"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion",
		"EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit",
		"Skill", "Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch",
	}
	amCcToolLookup = func() map[string]string {
		m := make(map[string]string, len(amClaudeCodeTools))
		for _, t := range amClaudeCodeTools {
			m[strings.ToLower(t)] = t
		}
		return m
	}()
	amNormalizeToolCallIdRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	amAnthropicMessageEvts  = map[string]bool{
		"message_start":       true,
		"message_delta":       true,
		"message_stop":        true,
		"content_block_start": true,
		"content_block_delta": true,
		"content_block_stop":  true,
	}
	amNewThreadErrors = map[string]bool{
		"thread_not_found":            true,
		"thread_already_continued":    true,
		"thread_continue_in_flight":   true,
		"thread_fingerprint_mismatch": true,
	}
	amStrictUnsupportedKeywords = map[string]bool{
		"minimum":          true,
		"maximum":          true,
		"exclusiveMinimum": true,
		"exclusiveMaximum": true,
		"multipleOf":       true,
		"maxItems":         true,
		"uniqueItems":      true,
		"minContains":      true,
		"maxContains":      true,
		"minProperties":    true,
		"maxProperties":    true,
	}
	amStrictStringFormats = map[string]bool{
		"date-time": true,
		"time":      true,
		"date":      true,
		"duration":  true,
		"email":     true,
		"hostname":  true,
		"uri":       true,
		"ipv4":      true,
		"ipv6":      true,
		"uuid":      true,
	}

	amThreadMu    sync.Mutex
	amThreadState = make(map[string]*amThreadEntry)
	amThreadOrder []string

	amCleanupReg = ai.RegisterSessionResourceCleanup(func(sessionId *string) {
		amThreadMu.Lock()
		defer amThreadMu.Unlock()
		if sessionId == nil || *sessionId == "" {
			amThreadState = make(map[string]*amThreadEntry)
			amThreadOrder = nil
			return
		}
		delete(amThreadState, *sessionId)
		amRemoveThreadOrderLocked(*sessionId)
	})

	amFedMu    sync.Mutex
	amFedCache = make(map[string]*amFederationTokenCache)
)

type amThreadEntry struct {
	prefixCount int
	prefixHash  string
	replyId     string
	disabled    bool
}

type amCompatConfig struct {
	supportsEagerToolInputStreaming bool
	supportsLongCacheRetention      bool
	sendSessionAffinityHeaders      bool
	sessionAffinityFormat           string
	supportsCacheControlOnTools     bool
	supportsTemperature             bool
	forceAdaptiveThinking           bool
	allowEmptySignature             bool
	supportsStrictTools             bool
	supportsMidConvoEffort          bool
	supportsMidConvoSystemMessages  bool
	supportsMidConvoToolChanges     bool
	supportsMessageThreads          bool
	allowedFallbackModels           []ai.AnthropicAllowedFallbackModel
}

type amFederationConfig struct {
	baseURL           string
	organizationId    string
	workspaceId       string
	federationRuleId  string
	serviceAccountId  string
	identityTokenPath string
}

type amFederationTokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt int64
}

type amAPIError struct {
	Status  int
	ErrType string
	ErrCode string
	Message string
	Body    string
	Headers http.Header
	Cause   error
}

func (e *amAPIError) Error() string                { return e.Message }
func (e *amAPIError) Unwrap() error                { return e.Cause }
func (e *amAPIError) ProviderStatus() *int         { s := e.Status; return &s }
func (e *amAPIError) ProviderHeaders() http.Header { return e.Headers }

func amToClaudeCodeName(name string) string {
	if canonical, ok := amCcToolLookup[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

func amFromClaudeCodeName(name string, tools []*ai.Tool) string {
	if len(tools) > 0 {
		lower := strings.ToLower(name)
		for _, tool := range tools {
			if tool != nil && strings.ToLower(tool.Name) == lower {
				return tool.Name
			}
		}
	}
	return name
}

func amNormalizeToolCallId(id string, _ *ai.Model, _ *ai.AssistantMessage) string {
	sanitized := amNormalizeToolCallIdRe.ReplaceAllString(id, "_")
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return sanitized
}

func amGetCompat(model *ai.Model) amCompatConfig {
	isOpenRouter := false
	baseUrl := ""
	if model != nil {
		baseUrl = model.BaseUrl
		isOpenRouter = model.Provider == ai.KnownProviderOpenrouter || strings.Contains(baseUrl, "openrouter.ai")
	}
	cfg := amCompatConfig{
		supportsEagerToolInputStreaming: true,
		supportsLongCacheRetention:      true,
		sendSessionAffinityHeaders:      isOpenRouter,
		supportsCacheControlOnTools:     true,
		supportsTemperature:             true,
		allowEmptySignature:             false,
		supportsStrictTools:             false,
		supportsMidConvoSystemMessages:  false,
		supportsMidConvoToolChanges:     false,
		supportsMessageThreads:          strings.Contains(baseUrl, "api.anthropic.com"),
	}
	if isOpenRouter {
		cfg.sessionAffinityFormat = "openrouter"
	}
	if model == nil || model.Compat.V == nil {
		return cfg
	}

	applyBool := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	switch c := model.Compat.V.(type) {
	case *ai.AnthropicMessagesCompat:
		if c != nil {
			applyBool(&cfg.supportsEagerToolInputStreaming, c.SupportsEagerToolInputStreaming)
			applyBool(&cfg.supportsLongCacheRetention, c.SupportsLongCacheRetention)
			applyBool(&cfg.sendSessionAffinityHeaders, c.SendSessionAffinityHeaders)
			if c.SessionAffinityFormat != nil {
				cfg.sessionAffinityFormat = string(*c.SessionAffinityFormat)
			}
			applyBool(&cfg.supportsCacheControlOnTools, c.SupportsCacheControlOnTools)
			applyBool(&cfg.supportsTemperature, c.SupportsTemperature)
			applyBool(&cfg.forceAdaptiveThinking, c.ForceAdaptiveThinking)
			applyBool(&cfg.allowEmptySignature, c.AllowEmptySignature)
			applyBool(&cfg.supportsStrictTools, c.SupportsStrictTools)
			applyBool(&cfg.supportsMidConvoEffort, c.SupportsMidConvoEffort)
			applyBool(&cfg.supportsMidConvoSystemMessages, c.SupportsMidConvoSystemMessages)
			applyBool(&cfg.supportsMidConvoToolChanges, c.SupportsMidConvoToolChanges)
			applyBool(&cfg.supportsMessageThreads, c.SupportsMessageThreads)
			cfg.allowedFallbackModels = c.AllowedFallbackModels
		}
	case *ai.ProviderCompat:
		if c != nil {
			applyBool(&cfg.supportsEagerToolInputStreaming, c.SupportsEagerToolInputStreaming)
			applyBool(&cfg.supportsLongCacheRetention, c.SupportsLongCacheRetention)
			applyBool(&cfg.sendSessionAffinityHeaders, c.SendSessionAffinityHeaders)
			if c.SessionAffinityFormat != nil {
				cfg.sessionAffinityFormat = string(*c.SessionAffinityFormat)
			}
			applyBool(&cfg.supportsCacheControlOnTools, c.SupportsCacheControlOnTools)
			applyBool(&cfg.supportsTemperature, c.SupportsTemperature)
			applyBool(&cfg.forceAdaptiveThinking, c.ForceAdaptiveThinking)
			applyBool(&cfg.allowEmptySignature, c.AllowEmptySignature)
			applyBool(&cfg.supportsStrictTools, c.SupportsStrictTools)
			applyBool(&cfg.supportsMidConvoEffort, c.SupportsMidConvoEffort)
			applyBool(&cfg.supportsMidConvoSystemMessages, c.SupportsMidConvoSystemMessages)
			applyBool(&cfg.supportsMidConvoToolChanges, c.SupportsMidConvoToolChanges)
			applyBool(&cfg.supportsMessageThreads, c.SupportsMessageThreads)
			cfg.allowedFallbackModels = c.AllowedFallbackModels
		}
	}
	return cfg
}

func amResolveCacheRetention(cacheRetention *ai.CacheRetention, env ai.ProviderEnv) ai.CacheRetention {
	if cacheRetention != nil && *cacheRetention != "" {
		return *cacheRetention
	}
	if ai.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func amGetCacheControl(
	model *ai.Model,
	cacheRetention *ai.CacheRetention,
	env ai.ProviderEnv,
) (ai.CacheRetention, *jsonx.Object) {
	retention := amResolveCacheRetention(cacheRetention, env)
	if retention == ai.CacheRetentionNone {
		return retention, nil
	}
	cc := jsonx.NewObject()
	cc.Set("type", "ephemeral")
	if retention == ai.CacheRetentionLong && amGetCompat(model).supportsLongCacheRetention {
		cc.Set("ttl", "1h")
	}
	return retention, cc
}

// StreamAnthropicMessages streams a response from the Anthropic Messages API
// using raw net/http + internal/sse.
func StreamAnthropicMessages(
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
			msg := ggaInitOutput(nil)
			errMsg := "Anthropic Messages stream called with nil model"
			msg.StopReason = ai.StopReasonError
			msg.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
			stream.End(msg)
			return
		}

		opts := amExtractOptions(options)
		compat := amGetCompat(model)
		normCtx := ai.ResolveTranscript(c, &compat.supportsMidConvoSystemMessages)
		currentTools := ai.GetCurrentTools(normCtx.Messages)
		output := ggaInitOutput(model)
		output.StopReason = ai.StopReasonPending

		defer func() {
			if r := recover(); r != nil {
				errMsg := ai.FormatThrownValue(r)
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
			}
		}()

		cacheRetention, _ := amGetCacheControl(model, opts.CacheRetention, opts.Env)
		var cacheSessionId string
		if cacheRetention != ai.CacheRetentionNone && opts.SessionId != nil {
			cacheSessionId = *opts.SessionId
		}
		var threadSessionId string
		if compat.supportsMessageThreads {
			threadSessionId = cacheSessionId
		}

		apiKey := ""
		if opts.ApiKey != nil {
			apiKey = *opts.ApiKey
		}

		var copilotDynamicHeaders map[string]string
		if model.Provider == ai.KnownProviderGithubCopilot {
			hasImages := HasCopilotVisionInput(normCtx.Messages)
			copilotDynamicHeaders = BuildCopilotDynamicHeaders(CopilotDynamicHeadersParams{
				Messages:  normCtx.Messages,
				HasImages: hasImages,
			})
		}

		federation := amGetFederation(model, apiKey, opts.Headers, opts.Env)
		if federation == nil && !amHasRequestAuth(apiKey, opts.Headers) && !amHasRequestAuth(apiKey, clshToProviderHeaders(model.Headers)) {
			errMsg := "No API key for provider: " + string(model.Provider)
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		isOAuth := model.Provider != ai.KnownProviderGithubCopilot && amIsOAuthToken(apiKey)
		httpClient := clshResolveHTTPClient(opts.HTTPClient, opts.Env)
		var hdrTimeout int64
		if opts.TimeoutMs != nil {
			hdrTimeout = *opts.TimeoutMs
		}

		var authTokenOverride string
		if federation != nil {
			tok, err := amResolveFederationToken(ctx, federation, httpClient)
			if err != nil {
				errMsg := ai.WithCauseDetail(err)
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
			authTokenOverride = tok
		}

		baseURL := amResolveBaseURL(model, opts.Env)
		endpoint := baseURL + "/v1/messages?beta=true"
		staticHeaders, explicitBetaHeader := amBuildBaseHeaders(
			model, apiKey, authTokenOverride, &opts, copilotDynamicHeaders, cacheSessionId, compat, isOAuth,
		)

		disablePreferredStrictTools := HasExceededStrictToolLimits(opts.SessionId)

		var (
			resp                 *http.Response
			cancelHdrWait        context.CancelFunc
			params               *jsonx.Object
			thread               *amPreparedThreadRequest
			inputTransformations []any
			usageModel           = model
		)

		retryOpts := &ai.ProviderRetryOptions{
			MaxRetries:      opts.MaxRetries,
			MaxRetryDelayMs: opts.MaxRetryDelayMs,
		}

		for {
			if ctx.Err() != nil {
				errMsg := "Request was aborted"
				output.StopReason = ai.StopReasonAborted
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: output})
				stream.End(output)
				return
			}

			built := amBuildParams(model, normCtx, isOAuth, &opts, compat, disablePreferredStrictTools, threadSessionId != "")
			if built.providerThinkingLevel != "" {
				lvl := string(built.providerThinkingLevel)
				output.ProviderThinkingLevel = &lvl
			}
			params = built.params

			if opts.OnPayload != nil {
				updated, err := opts.OnPayload(params, model)
				if err != nil {
					errMsg := ai.WithCauseDetail(err)
					output.StopReason = ggaAbortOrError(ctx)
					output.ErrorMessage = &errMsg
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
					stream.End(output)
					return
				}
				if updated != nil {
					if obj, ok := updated.(*jsonx.Object); ok && obj != nil {
						obj.Set("stream", true)
						params = obj
					}
				}
			}

			var prepared *amPreparedThreadRequest
			disableCont := opts.DisableContinuation != nil && *opts.DisableContinuation
			if threadSessionId != "" && !disableCont {
				prepared = amPrepareThreadRequest(model, threadSessionId, params)
			}

			sendBody := func(bodyObj *jsonx.Object) (*http.Response, context.CancelFunc, error) {
				reqHeaders := staticHeaders.Clone()
				wireBody := amExtractSDKHeaderParams(bodyObj, reqHeaders, explicitBetaHeader)
				bodyBytes, err := jsonx.Marshal(wireBody)
				if err != nil {
					return nil, nil, err
				}
				return amSendWithRetry(ctx, httpClient, endpoint, reqHeaders, bodyBytes, hdrTimeout, retryOpts)
			}

			var sendErr error
			if prepared != nil {
				resp, cancelHdrWait, thread, sendErr = amSendThreadRequest(ctx, prepared, params, sendBody, output)
			} else {
				resp, cancelHdrWait, sendErr = sendBody(params)
				thread = nil
			}

			if sendErr == nil {
				break
			}

			var apiErr *amAPIError
			if ctx.Err() == nil &&
				!disablePreferredStrictTools &&
				built.hasPreferredStrictTools &&
				errors.As(sendErr, &apiErr) &&
				apiErr.Status == 400 &&
				apiErr.ErrType == "invalid_request_error" &&
				IsStrictToolLimitErrorMessage(apiErr.Message) {
				disablePreferredStrictTools = true
				MarkStrictToolLimitsExceeded(opts.SessionId)
				ai.AppendAssistantMessageDiagnostic(output, &ai.AssistantMessageDiagnostic{
					Type:      "anthropic_strict_tools_fallback",
					Timestamp: js.DateNow(),
					Error:     &ai.DiagnosticErrorInfo{Message: apiErr.Message},
					Details:   jsonx.ObjectOf("reason", "strict_tool_limits_exceeded"),
				})
				continue
			}

			errMsg := ai.WithCauseDetail(sendErr)
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

		if cancelHdrWait != nil {
			defer cancelHdrWait()
		}
		defer resp.Body.Close()

		if opts.OnResponse != nil {
			if cbErr := opts.OnResponse(&ai.ProviderResponse{
				Status:  resp.StatusCode,
				Headers: ai.HeadersToRecord(resp.Header),
			}, model); cbErr != nil {
				errMsg := ai.WithCauseDetail(cbErr)
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
		}

		stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: output})

		transformations, finalUsageModel, streamErr := amConsumeSSEStream(
			ctx, resp.Body, model, currentTools, isOAuth, stream, output, opts.OnProviderStreamEvent,
		)
		if finalUsageModel != nil {
			usageModel = finalUsageModel
		}
		_ = usageModel
		if len(transformations) > 0 {
			inputTransformations = transformations
		}

		if streamErr != nil {
			errMsg := ai.WithCauseDetail(streamErr)
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

		if output.StopReason == "" || output.StopReason == ai.StopReasonPending {
			errMsg := "Anthropic stream ended without a stop reason"
			output.StopReason = ai.StopReasonError
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: output})
			stream.End(output)
			return
		}
		if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		if thread != nil {
			amRecordThreadReply(model, thread, params, output, isOAuth, compat.allowEmptySignature)
		}
		if len(inputTransformations) > 0 {
			details := make([]any, 0, len(inputTransformations))
			for _, tr := range inputTransformations {
				if obj, ok := tr.(*jsonx.Object); ok && obj != nil {
					item := map[string]any{}
					if v, ok := obj.Get("type"); ok && v != nil {
						item["type"] = v
					}
					if v, ok := obj.Get("path"); ok && v != nil {
						item["path"] = v
					}
					if v, ok := obj.Get("reason"); ok && v != nil {
						item["reason"] = v
					}
					details = append(details, item)
				}
			}
			ai.AppendAssistantMessageDiagnostic(output, &ai.AssistantMessageDiagnostic{
				Type:      "anthropic_input_transformations",
				Timestamp: js.DateNow(),
				Details:   jsonx.ObjectOf("transformations", details),
			})
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

// StreamSimpleAnthropicMessages maps uniform SimpleStreamOptions onto AnthropicOptions
// and invokes StreamAnthropicMessages.
func StreamSimpleAnthropicMessages(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	var apiKey *string
	var env ai.ProviderEnv
	var headers ai.ProviderHeaders
	if options != nil {
		apiKey = options.ApiKey
		env = options.Env
		headers = options.Headers
	}
	keyStr := ""
	if apiKey != nil {
		keyStr = *apiKey
	}
	if amGetFederation(model, keyStr, headers, env) == nil && !amHasRequestAuth(keyStr, headers) {
		var modelHdrs ai.ProviderHeaders
		if model != nil {
			modelHdrs = clshToProviderHeaders(model.Headers)
		}
		if !amHasRequestAuth(keyStr, modelHdrs) {
			stream := ai.NewAssistantMessageEventStream()
			msg := ggaInitOutput(model)
			providerName := ""
			if model != nil {
				providerName = string(model.Provider)
			}
			errMsg := "No API key for provider: " + providerName
			msg.StopReason = ai.StopReasonError
			msg.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
			stream.End(msg)
			return stream
		}
	}

	base := BuildBaseOptions(model, c, options, apiKey)
	aOpts := &AnthropicOptions{
		StreamOptions: base,
	}
	if options != nil && options.ToolChoice != nil {
		switch *options.ToolChoice {
		case ai.ToolChoiceAuto:
			aOpts.ToolChoice = &AnthropicToolChoice{Type: "auto"}
		case ai.ToolChoiceNone:
			aOpts.ToolChoice = &AnthropicToolChoice{Type: "none"}
		default:
			if string(*options.ToolChoice) == "any" || string(*options.ToolChoice) == "required" {
				aOpts.ToolChoice = &AnthropicToolChoice{Type: "any"}
			} else {
				aOpts.ToolChoice = &AnthropicToolChoice{Type: string(*options.ToolChoice)}
			}
		}
	}

	if options == nil || options.Reasoning == nil {
		disabled := false
		aOpts.ThinkingEnabled = &disabled
		return StreamAnthropicMessages(ctx, model, c, aOpts)
	}

	compat := amGetCompat(model)
	if compat.forceAdaptiveThinking {
		enabled := true
		effort := amMapThinkingLevelToEffort(model, options.Reasoning)
		aOpts.ThinkingEnabled = &enabled
		aOpts.Effort = &effort
		return StreamAnthropicMessages(ctx, model, c, aOpts)
	}

	modelMaxTokens := 4096
	if model != nil && model.MaxTokens > 0 {
		modelMaxTokens = model.MaxTokens
	}
	adjustedMax, adjustedBudget := AdjustMaxTokensForThinking(
		base.MaxTokens,
		modelMaxTokens,
		*options.Reasoning,
		options.ThinkingBudgets,
	)
	clampedMax := ClampMaxTokensToContext(model, c, adjustedMax)
	budget := adjustedBudget
	room := clampedMax - 1024
	if room < 0 {
		room = 0
	}
	if budget > room {
		budget = room
	}

	enabled := true
	aOpts.MaxTokens = &clampedMax
	aOpts.ThinkingEnabled = &enabled
	aOpts.ThinkingBudgetTokens = &budget
	return StreamAnthropicMessages(ctx, model, c, aOpts)
}

func amMapThinkingLevelToEffort(model *ai.Model, level *ai.ThinkingLevel) AnthropicEffort {
	if level != nil && model != nil && model.ThinkingLevelMap != nil {
		opt, set := gsLookupThinkingLevelMap(model.ThinkingLevelMap, ai.ModelThinkingLevel(*level))
		if set {
			if val, ok := opt.Get(); ok && val != "" {
				return AnthropicEffort(val)
			}
		}
	}
	if level == nil {
		return AnthropicEffortHigh
	}
	switch *level {
	case ai.ThinkingLevelMinimal, ai.ThinkingLevelLow:
		return AnthropicEffortLow
	case ai.ThinkingLevelMedium:
		return AnthropicEffortMedium
	case ai.ThinkingLevelHigh:
		return AnthropicEffortHigh
	default:
		return AnthropicEffortHigh
	}
}

func amExtractOptions(options ai.ProviderStreamOptions) AnthropicOptions {
	if options == nil {
		return AnthropicOptions{}
	}
	switch o := options.(type) {
	case *AnthropicOptions:
		if o != nil {
			return *o
		}
	case *ai.SimpleStreamOptions:
		if o != nil {
			out := AnthropicOptions{StreamOptions: o.StreamOptions}
			if o.ToolChoice != nil {
				out.ToolChoice = &AnthropicToolChoice{Type: string(*o.ToolChoice)}
			}
			return out
		}
	case *ai.StreamOptions:
		if o != nil {
			return AnthropicOptions{StreamOptions: *o}
		}
	default:
		if base := options.BaseStreamOptions(); base != nil {
			return AnthropicOptions{StreamOptions: *base}
		}
	}
	return AnthropicOptions{}
}

func amIsOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

func amHasHeader(headers ai.ProviderHeaders, name string) bool {
	if headers == nil {
		return false
	}
	target := strings.ToLower(name)
	for k, v := range headers.All() {
		if strings.ToLower(k) == target && v != nil && strings.TrimSpace(*v) != "" {
			return true
		}
	}
	return false
}

func amHasRequestAuth(apiKey string, headers ai.ProviderHeaders) bool {
	if strings.TrimSpace(apiKey) != "" {
		return true
	}
	return amHasHeader(headers, "authorization") ||
		amHasHeader(headers, "x-api-key") ||
		amHasHeader(headers, "cf-aig-authorization")
}

func amResolveBaseURL(model *ai.Model, env ai.ProviderEnv) string {
	base := ""
	if model != nil {
		base = strings.TrimSpace(model.BaseUrl)
	}
	if base == "" {
		base = strings.TrimSpace(ai.GetProviderEnvValue("ANTHROPIC_BASE_URL", env))
	}
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return strings.TrimRight(base, "/")
}

func amGetFederation(model *ai.Model, apiKey string, headers ai.ProviderHeaders, env ai.ProviderEnv) *amFederationConfig {
	if model == nil || model.Provider != ai.KnownProviderAnthropic {
		return nil
	}
	if amHasRequestAuth(apiKey, headers) || amHasRequestAuth(apiKey, clshToProviderHeaders(model.Headers)) {
		return nil
	}
	ruleId := strings.TrimSpace(ai.GetProviderEnvValue(ai.AnthropicFederationRuleIdEnv, env))
	orgId := strings.TrimSpace(ai.GetProviderEnvValue(ai.AnthropicOrganizationIdEnv, env))
	tokenFile := strings.TrimSpace(ai.GetProviderEnvValue(ai.AnthropicIdentityTokenFileEnv, env))
	if ruleId == "" || orgId == "" || tokenFile == "" {
		return nil
	}
	return &amFederationConfig{
		baseURL:           amResolveBaseURL(model, env),
		organizationId:    orgId,
		workspaceId:       strings.TrimSpace(ai.GetProviderEnvValue(ai.AnthropicWorkspaceIdEnv, env)),
		federationRuleId:  ruleId,
		serviceAccountId:  strings.TrimSpace(ai.GetProviderEnvValue(ai.AnthropicServiceAccountIdEnv, env)),
		identityTokenPath: tokenFile,
	}
}

func amRequireSecureTokenEndpoint(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("Invalid token endpoint base URL %q: %v", baseURL, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(strings.Trim(u.Hostname(), "[]"))
	if u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("Refusing to send credential over non-https token endpoint %q", baseURL)
}

func amResolveFederationToken(ctx context.Context, cfg *amFederationConfig, httpClient *http.Client) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("nil federation config")
	}
	cacheKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		cfg.baseURL, cfg.organizationId, cfg.workspaceId, cfg.federationRuleId, cfg.serviceAccountId, cfg.identityTokenPath)

	amFedMu.Lock()
	entry, ok := amFedCache[cacheKey]
	if !ok {
		entry = &amFederationTokenCache{}
		amFedCache[cacheKey] = entry
	}
	amFedMu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	nowSec := time.Now().Unix()
	if entry.token != "" && entry.expiresAt > nowSec+amFederationMandatoryRefreshSec {
		return entry.token, nil
	}

	if err := amRequireSecureTokenEndpoint(cfg.baseURL); err != nil {
		return "", err
	}
	rawJWT, err := os.ReadFile(cfg.identityTokenPath)
	if err != nil {
		return "", fmt.Errorf("Failed to read identity token file at %s: %v", cfg.identityTokenPath, err)
	}
	jwtStr := strings.TrimSpace(string(rawJWT))
	if jwtStr == "" {
		return "", fmt.Errorf("Identity token file at %s is empty", cfg.identityTokenPath)
	}
	if len(jwtStr) > amMaxFederationAssertionBytes {
		return "", fmt.Errorf("Identity token is %d KiB, exceeds the 16 KiB assertion limit", (len(jwtStr)+1023)/1024)
	}

	reqObj := jsonx.NewObject()
	reqObj.Set("grant_type", amGrantTypeJWTBearer)
	reqObj.Set("assertion", jwtStr)
	reqObj.Set("federation_rule_id", cfg.federationRuleId)
	reqObj.Set("organization_id", cfg.organizationId)
	if cfg.serviceAccountId != "" {
		reqObj.Set("service_account_id", cfg.serviceAccountId)
	}
	if cfg.workspaceId != "" {
		reqObj.Set("workspace_id", cfg.workspaceId)
	}
	bodyBytes, err := jsonx.Marshal(reqObj)
	if err != nil {
		return "", err
	}

	tokenURL := cfg.baseURL + amTokenEndpointPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("Failed to reach token endpoint %s: %v", tokenURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", amOAuthAPIBetaHeader+","+amFederationBetaHeader)
	req.Header.Set("User-Agent", ai.FormatUserAgent(nil))

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Failed to reach token endpoint %s: %v", tokenURL, err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	requestId := resp.Header.Get("Request-Id")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hint := ""
		if resp.StatusCode == 401 {
			hintMiddle := ""
			if cfg.workspaceId == "" {
				hintMiddle = "If your federation rule is scoped to multiple workspaces, set the ANTHROPIC_WORKSPACE_ID environment variable, the 'workspace_id' config key, or the `workspaceId` option. "
			}
			hint = fmt.Sprintf(" Ensure your federation rule matches your identity token. %sView your authentication events in the Workload identity page of Claude Console for more details.", hintMiddle)
		}
		reqIdPart := ""
		if requestId != "" {
			reqIdPart = fmt.Sprintf(" (request-id %s)", requestId)
		}
		return "", fmt.Errorf("Token exchange failed with status %d%s: %s%s",
			resp.StatusCode, reqIdPart, amRedactFederationErrorBody(string(respBytes)), hint)
	}

	parsed, err := jsonx.Parse(respBytes)
	if err != nil {
		return "", fmt.Errorf("Token endpoint returned non-JSON response (status %d)", resp.StatusCode)
	}
	tokObj, ok := parsed.(*jsonx.Object)
	if !ok || tokObj == nil {
		return "", fmt.Errorf("Token endpoint response missing access_token")
	}
	accessVal, _ := tokObj.Get("access_token")
	accessToken, _ := accessVal.(string)
	if accessToken == "" {
		return "", fmt.Errorf("Token endpoint response missing access_token")
	}
	if ttVal, hasTT := tokObj.Get("token_type"); hasTT {
		if tt, ok := ttVal.(string); ok && tt != "" && !strings.EqualFold(tt, "Bearer") {
			return "", fmt.Errorf("Token endpoint response: unsupported token_type %q (want Bearer)", tt)
		}
	}
	expVal, hasExp := tokObj.Get("expires_in")
	expiresIn := ggaToInt(expVal)
	if !hasExp || expiresIn <= 0 {
		return "", fmt.Errorf("Token endpoint response missing required fields")
	}

	entry.token = accessToken
	entry.expiresAt = time.Now().Unix() + int64(expiresIn)
	return entry.token, nil
}

func amRedactFederationErrorBody(text string) string {
	parsed, err := jsonx.Parse([]byte(text))
	if err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			redacted := jsonx.NewObject()
			for _, k := range []string{"error", "error_description", "error_uri"} {
				if v, ok := obj.Get(k); ok {
					redacted.Set(k, v)
				}
			}
			if encoded, err := jsonx.Marshal(redacted); err == nil {
				return string(encoded)
			}
		}
	}
	return ai.TruncateErrorText(text, 2000)
}

func amMapToProviderHeaders(m map[string]string) ai.ProviderHeaders {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := omap.NewMap[string, *string]()
	for _, k := range keys {
		val := m[k]
		out.Set(k, &val)
	}
	return out
}

func amBuildBaseHeaders(
	model *ai.Model,
	apiKey string,
	federatedBearer string,
	opts *AnthropicOptions,
	copilotDynamicHeaders map[string]string,
	cacheSessionId string,
	compat amCompatConfig,
	isOAuth bool,
) (http.Header, *string) {
	var sources []ai.ProviderHeaders
	var explicitBeta *string
	setExplicitBeta := false

	scanBeta := func(h ai.ProviderHeaders) {
		if h == nil {
			return
		}
		for k, v := range h.All() {
			if strings.ToLower(k) == "anthropic-beta" {
				setExplicitBeta = true
				if v == nil {
					explicitBeta = nil
				} else {
					s := *v
					explicitBeta = &s
				}
			}
		}
	}

	target := ai.ClientIdentityTarget{Provider: string(model.Provider), BaseUrl: model.BaseUrl}
	if model.Provider == ai.KnownProviderGithubCopilot {
		idHdr := ai.GetIdentityHeaders(target, opts.Identity)
		if idHdr != nil && idHdr.Len() > 0 {
			sources = append(sources, clshToProviderHeaders(idHdr))
		}
		base := map[string]string{
			"accept":            "application/json",
			"content-type":      "application/json",
			"anthropic-version": amAnthropicVersionHeader,
			"anthropic-dangerous-direct-browser-access": "true",
		}
		if apiKey != "" {
			base["authorization"] = "Bearer " + apiKey
		}
		sources = append(sources, amMapToProviderHeaders(base))
		if model.Headers != nil {
			mh := clshToProviderHeaders(model.Headers)
			scanBeta(mh)
			sources = append(sources, mh)
		}
		if len(copilotDynamicHeaders) > 0 {
			sources = append(sources, amMapToProviderHeaders(copilotDynamicHeaders))
		}
		if opts.Headers != nil {
			scanBeta(opts.Headers)
			sources = append(sources, opts.Headers)
		}
	} else if isOAuth {
		base := map[string]string{
			"accept":            "application/json",
			"content-type":      "application/json",
			"anthropic-version": amAnthropicVersionHeader,
			"anthropic-dangerous-direct-browser-access": "true",
			"user-agent":    "claude-cli/" + amClaudeCodeVersion,
			"x-app":         "cli",
			"authorization": "Bearer " + apiKey,
		}
		sources = append(sources, amMapToProviderHeaders(base))
		if model.Headers != nil {
			mh := clshToProviderHeaders(model.Headers)
			scanBeta(mh)
			sources = append(sources, mh)
		}
		if opts.Headers != nil {
			scanBeta(opts.Headers)
			sources = append(sources, opts.Headers)
		}
	} else {
		idHdr := ai.GetIdentityHeaders(target, opts.Identity)
		if idHdr != nil && idHdr.Len() > 0 {
			sources = append(sources, clshToProviderHeaders(idHdr))
		}
		base := map[string]string{
			"accept":            "application/json",
			"content-type":      "application/json",
			"anthropic-version": amAnthropicVersionHeader,
			"anthropic-dangerous-direct-browser-access": "true",
		}
		if federatedBearer != "" {
			base["authorization"] = "Bearer " + federatedBearer
		} else if apiKey != "" {
			base["x-api-key"] = apiKey
		}
		sources = append(sources, amMapToProviderHeaders(base))
		if cacheSessionId != "" && compat.sendSessionAffinityHeaders {
			hdrName := "x-session-affinity"
			if compat.sessionAffinityFormat == "openrouter" {
				hdrName = "x-session-id"
			}
			sources = append(sources, clshStaticProviderHeaders(hdrName, cacheSessionId))
		}
		if model.Headers != nil {
			mh := clshToProviderHeaders(model.Headers)
			scanBeta(mh)
			sources = append(sources, mh)
		}
		if opts.Headers != nil {
			scanBeta(opts.Headers)
			sources = append(sources, opts.Headers)
		}
	}

	merged := ai.ProviderHeadersToOrderedRecord(sources...)
	headers := make(http.Header)
	if merged != nil {
		for k, v := range merged.All() {
			headers.Set(k, v)
		}
	}
	if setExplicitBeta && explicitBeta == nil {
		empty := ""
		return headers, &empty
	}
	return headers, explicitBeta
}

func amGetBetaFeatures(
	model *ai.Model,
	c *ai.TranscriptContext,
	opts *AnthropicOptions,
	compat amCompatConfig,
	isOAuthToken bool,
	nativeToolChanges bool,
	messageThreads bool,
) []string {
	var hasConfigured bool
	var configured *string
	checkHdr := func(h ai.ProviderHeaders) {
		if h == nil {
			return
		}
		for k, v := range h.All() {
			if strings.ToLower(k) == "anthropic-beta" {
				hasConfigured = true
				configured = v
			}
		}
	}
	if model != nil && model.Headers != nil {
		checkHdr(clshToProviderHeaders(model.Headers))
	}
	if opts != nil {
		checkHdr(opts.Headers)
	}
	if hasConfigured {
		if configured == nil {
			return nil
		}
		parts := strings.Split(*configured, ",")
		var out []string
		seen := make(map[string]bool)
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				out = append(out, trimmed)
			}
		}
		return out
	}

	var features []string
	seen := make(map[string]bool)
	add := func(f string) {
		if f != "" && !seen[f] {
			seen[f] = true
			features = append(features, f)
		}
	}

	if isOAuthToken {
		add("claude-code-20250219")
		add("oauth-2025-04-20")
	}
	if len(ai.GetCurrentTools(c.Messages)) > 0 && !compat.supportsEagerToolInputStreaming {
		add(amFineGrainedToolStreamingBeta)
	}
	interleaved := true
	if opts != nil && opts.InterleavedThinking != nil {
		interleaved = *opts.InterleavedThinking
	}
	if model.Reasoning &&
		opts != nil && opts.ThinkingEnabled != nil && *opts.ThinkingEnabled &&
		interleaved &&
		!compat.forceAdaptiveThinking {
		add(amInterleavedThinkingBeta)
	}
	if len(compat.allowedFallbackModels) > 0 {
		add(amServerSideFallbackBeta)
	}
	if compat.supportsMidConvoEffort {
		add(amMidConversationOutputConfigBeta)
		add(amThinkingBindingControlsBeta)
	}
	if nativeToolChanges {
		add(amInlineToolsBeta)
	}
	if messageThreads {
		add(amMessageThreadsBeta)
	}
	return features
}

type amBuildParamsResult struct {
	params                  *jsonx.Object
	hasPreferredStrictTools bool
	providerThinkingLevel   AnthropicEffort
}

func amIsStrictUnsupportedKeyword(key string, value any) bool {
	if amStrictUnsupportedKeywords[key] {
		return true
	}
	if key == "minItems" {
		switch x := value.(type) {
		case int:
			return x != 0 && x != 1
		case int64:
			return x != 0 && x != 1
		case float64:
			return x != 0 && x != 1
		default:
			return true
		}
	}
	if key == "format" {
		s, ok := value.(string)
		return !ok || !amStrictStringFormats[s]
	}
	return false
}

func amBuildParams(
	model *ai.Model,
	c *ai.TranscriptContext,
	isOAuth bool,
	opts *AnthropicOptions,
	compat amCompatConfig,
	disablePreferredStrictTools bool,
	messageThreads bool,
) amBuildParamsResult {
	_, cacheControl := amGetCacheControl(model, opts.CacheRetention, opts.Env)
	initialSysMsg := ai.GetInitialSystemMessage(c.Messages)
	initialSysText := ""
	if initialSysMsg != nil {
		initialSysText = ai.GetSystemMessageText(initialSysMsg)
	}
	transformed := TransformMessages(c.Messages, model, amNormalizeToolCallId)
	convMessages := transformed
	if initialSysMsg != nil && len(transformed) > 0 {
		if _, ok := transformed[0].(*ai.SystemMessage); ok {
			convMessages = transformed[1:]
		}
	}

	hasPreferredStrictTools := false
	convertToolDefs := func(tools []*ai.Tool, toolCC *jsonx.Object) []*jsonx.Object {
		defs := make([]*jsonx.Object, 0, len(tools))
		for i, tool := range tools {
			if tool == nil {
				continue
			}
			preferred := PrefersJsonSchemaStrictSampling(tool)
			var strictPtr *bool
			if !(disablePreferredStrictTools && preferred) {
				strictPtr, _ = ResolveJsonSchemaStrictSampling(tool, compat.supportsStrictTools, amIsStrictUnsupportedKeyword)
			}
			isStrict := strictPtr != nil && *strictPtr
			if isStrict && preferred {
				hasPreferredStrictTools = true
			}
			paramsSchema, _ := GetJsonSchemaToolParameters(tool, strictPtr)
			var schemaObj *jsonx.Object
			if paramsSchema != nil && paramsSchema.Object != nil {
				schemaObj = paramsSchema.Object
			} else if paramsSchema != nil {
				if rawBytes, err := jsonx.Marshal(paramsSchema); err == nil {
					if parsed, err := jsonx.Parse(rawBytes); err == nil {
						schemaObj, _ = parsed.(*jsonx.Object)
					}
				}
			}

			legacySchema := jsonx.NewObject()
			legacySchema.Set("type", "object")
			var props any = jsonx.NewObject()
			var req any = []string{}
			if schemaObj != nil {
				if p, ok := schemaObj.Get("properties"); ok && p != nil {
					props = p
				}
				if r, ok := schemaObj.Get("required"); ok && r != nil {
					req = r
				}
			}
			legacySchema.Set("properties", props)
			legacySchema.Set("required", req)
			if schemaObj != nil {
				if ap, ok := schemaObj.Get("additionalProperties"); ok && ap != nil {
					legacySchema.Set("additionalProperties", ap)
				}
				if defsVal, ok := schemaObj.Get("$defs"); ok && defsVal != nil {
					legacySchema.Set("$defs", defsVal)
				}
				if legacyDefs, ok := schemaObj.Get("definitions"); ok && legacyDefs != nil {
					legacySchema.Set("definitions", legacyDefs)
				}
			}

			inputSchema := legacySchema
			if isStrict && schemaObj != nil {
				merged := gsCloneJSONValue(schemaObj).(*jsonx.Object)
				for _, k := range legacySchema.Keys() {
					v, _ := legacySchema.Get(k)
					merged.Set(k, v)
				}
				inputSchema = merged
			}

			def := jsonx.NewObject()
			toolName := tool.Name
			if isOAuth {
				toolName = amToClaudeCodeName(toolName)
			}
			def.Set("name", toolName)
			def.Set("description", tool.Description)
			if compat.supportsEagerToolInputStreaming {
				def.Set("eager_input_streaming", true)
			}
			if isStrict {
				def.Set("strict", true)
			}
			def.Set("input_schema", inputSchema)
			if toolCC != nil && i == len(tools)-1 {
				def.Set("cache_control", gsCloneJSONValue(toolCC))
			}
			defs = append(defs, def)
		}
		return defs
	}

	var initialTools []*ai.Tool
	if initialSysMsg != nil {
		initialTools = initialSysMsg.ToolsAdded
	}
	nativeToolChanges := compat.supportsMidConvoSystemMessages && compat.supportsMidConvoToolChanges && len(initialTools) > 0

	thinkingDisabled := model.Reasoning &&
		opts.ThinkingEnabled != nil && !*opts.ThinkingEnabled &&
		(model.ThinkingLevelMap == nil || model.ThinkingLevelMap.Off.IsZero() || !model.ThinkingLevelMap.Off.IsNull())
	effortMessages := compat.supportsMidConvoEffort && !thinkingDisabled

	var toolConverter func([]*ai.Tool) []*jsonx.Object
	if nativeToolChanges {
		toolConverter = func(tools []*ai.Tool) []*jsonx.Object {
			return convertToolDefs(tools, nil)
		}
	}
	managedProvider := ""
	if effortMessages {
		managedProvider = string(model.Provider)
	}
	converted := amConvertMessages(
		convMessages, isOAuth, cacheControl, compat.allowEmptySignature, managedProvider, toolConverter,
	)

	activeEffort := AnthropicEffortHigh
	if opts.Effort != nil && *opts.Effort != "" {
		activeEffort = *opts.Effort
	}

	wireMessages := converted.messages
	if effortMessages {
		wireMessages = amInsertThinkingLevelMessages(converted, activeEffort)
	}

	betaFeatures := amGetBetaFeatures(model, c, opts, compat, isOAuth, nativeToolChanges, messageThreads)

	maxTokens := model.MaxTokens
	if opts.MaxTokens != nil {
		maxTokens = *opts.MaxTokens
	}

	params := jsonx.NewObject()
	params.Set("model", model.Id)
	params.Set("messages", gsObjectsToAny(wireMessages))
	params.Set("max_tokens", maxTokens)
	params.Set("stream", true)
	if len(betaFeatures) > 0 {
		params.Set("betas", betaFeatures)
	}

	if isOAuth {
		var sysBlocks []*jsonx.Object
		identityBlk := jsonx.NewObject()
		identityBlk.Set("type", "text")
		identityBlk.Set("text", "You are Claude Code, Anthropic's official CLI for Claude.")
		if cacheControl != nil {
			identityBlk.Set("cache_control", gsCloneJSONValue(cacheControl))
		}
		sysBlocks = append(sysBlocks, identityBlk)
		if initialSysText != "" {
			promptBlk := jsonx.NewObject()
			promptBlk.Set("type", "text")
			promptBlk.Set("text", ai.SanitizeSurrogates(initialSysText))
			if cacheControl != nil {
				promptBlk.Set("cache_control", gsCloneJSONValue(cacheControl))
			}
			sysBlocks = append(sysBlocks, promptBlk)
		}
		params.Set("system", gsObjectsToAny(sysBlocks))
	} else if initialSysText != "" {
		promptBlk := jsonx.NewObject()
		promptBlk.Set("type", "text")
		promptBlk.Set("text", ai.SanitizeSurrogates(initialSysText))
		if cacheControl != nil {
			promptBlk.Set("cache_control", gsCloneJSONValue(cacheControl))
		}
		params.Set("system", []any{promptBlk})
	}

	thinkingActive := opts.ThinkingEnabled != nil && *opts.ThinkingEnabled
	if opts.Temperature != nil && !thinkingActive && !compat.supportsMidConvoEffort && compat.supportsTemperature {
		params.Set("temperature", *opts.Temperature)
	}

	var toolCC *jsonx.Object
	if compat.supportsCacheControlOnTools {
		toolCC = cacheControl
	}
	if nativeToolChanges {
		toolDefs := convertToolDefs(initialTools, toolCC)
		placeholder := amDeferredPlaceholderTool()
		toolDefs = append(toolDefs, placeholder)
		params.Set("tools", gsObjectsToAny(toolDefs))
	} else {
		tools := ai.GetCurrentTools(c.Messages)
		if len(tools) > 0 {
			params.Set("tools", gsObjectsToAny(convertToolDefs(tools, toolCC)))
		}
	}

	display := AnthropicThinkingDisplaySummarized
	if opts.ThinkingDisplay != nil && *opts.ThinkingDisplay != "" {
		display = *opts.ThinkingDisplay
	}

	if thinkingDisabled {
		th := jsonx.NewObject()
		th.Set("type", "disabled")
		params.Set("thinking", th)
	} else if effortMessages {
		bb := jsonx.NewObject()
		bb.Set("prefix_mismatch_behavior", "drop_block")
		th := jsonx.NewObject()
		th.Set("type", "adaptive")
		th.Set("display", string(display))
		th.Set("block_binding", bb)
		params.Set("thinking", th)

		oc := jsonx.NewObject()
		oc.Set("effort", "low")
		params.Set("output_config", oc)
	} else if model.Reasoning && thinkingActive {
		if compat.forceAdaptiveThinking {
			th := jsonx.NewObject()
			th.Set("type", "adaptive")
			th.Set("display", string(display))
			params.Set("thinking", th)
			if opts.Effort != nil && *opts.Effort != "" {
				oc := jsonx.NewObject()
				oc.Set("effort", string(*opts.Effort))
				params.Set("output_config", oc)
			}
		} else {
			budget := 1024
			if opts.ThinkingBudgetTokens != nil && *opts.ThinkingBudgetTokens > 0 {
				budget = *opts.ThinkingBudgetTokens
			}
			th := jsonx.NewObject()
			th.Set("type", "enabled")
			th.Set("budget_tokens", budget)
			th.Set("display", string(display))
			params.Set("thinking", th)
		}
	}

	if opts.Metadata != nil {
		if uidVal, ok := opts.Metadata.Get("user_id"); ok {
			if uidStr, ok := uidVal.(string); ok {
				meta := jsonx.NewObject()
				meta.Set("user_id", uidStr)
				params.Set("metadata", meta)
			}
		}
	}

	if opts.ToolChoice != nil && opts.ToolChoice.Type != "" {
		tc := jsonx.NewObject()
		tc.Set("type", opts.ToolChoice.Type)
		if opts.ToolChoice.Name != "" {
			tc.Set("name", opts.ToolChoice.Name)
		}
		params.Set("tool_choice", tc)
	}

	if len(compat.allowedFallbackModels) > 0 {
		fbs := make([]*jsonx.Object, 0, len(compat.allowedFallbackModels))
		for _, fb := range compat.allowedFallbackModels {
			fbObj := jsonx.NewObject()
			fbObj.Set("model", fb.Model)
			fbs = append(fbs, fbObj)
		}
		params.Set("fallbacks", gsObjectsToAny(fbs))
	}

	var finalLevel AnthropicEffort
	if effortMessages {
		finalLevel = activeEffort
	}
	return amBuildParamsResult{
		params:                  params,
		hasPreferredStrictTools: hasPreferredStrictTools,
		providerThinkingLevel:   finalLevel,
	}
}

func amDeferredPlaceholderTool() *jsonx.Object {
	props := jsonx.NewObject()
	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []string{})
	tool := jsonx.NewObject()
	tool.Set("name", "__pi_deferred_placeholder__")
	tool.Set("description", "Reserved placeholder. Never available. Never call this.")
	tool.Set("input_schema", schema)
	tool.Set("defer_loading", true)
	return tool
}

type amConvertedMessages struct {
	messages        []*jsonx.Object
	assistantLevels map[int]AnthropicEffort
}

func amConvertImageBlock(img *ai.ImageContent) *jsonx.Object {
	src := jsonx.NewObject()
	src.Set("type", "base64")
	src.Set("media_type", img.MimeType)
	src.Set("data", img.Data)
	blk := jsonx.NewObject()
	blk.Set("type", "image")
	blk.Set("source", src)
	return blk
}

func amConvertContentBlocks(content []ai.Content) any {
	hasImages := false
	for _, c := range content {
		if _, ok := c.(*ai.ImageContent); ok {
			hasImages = true
			break
		}
	}
	if !hasImages {
		var texts []string
		for _, c := range content {
			if tc, ok := c.(*ai.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		return ai.SanitizeSurrogates(strings.Join(texts, "\n"))
	}

	var blocks []*jsonx.Object
	hasText := false
	for _, c := range content {
		switch b := c.(type) {
		case *ai.TextContent:
			hasText = true
			blk := jsonx.NewObject()
			blk.Set("type", "text")
			blk.Set("text", ai.SanitizeSurrogates(b.Text))
			blocks = append(blocks, blk)
		case *ai.ImageContent:
			blocks = append(blocks, amConvertImageBlock(b))
		}
	}
	if !hasText {
		placeholder := jsonx.NewObject()
		placeholder.Set("type", "text")
		placeholder.Set("text", "(see attached image)")
		blocks = append([]*jsonx.Object{placeholder}, blocks...)
	}
	return gsObjectsToAny(blocks)
}

func amConvertMessages(
	transformed []ai.Message,
	isOAuth bool,
	cacheControl *jsonx.Object,
	allowEmptySignature bool,
	managedProvider string,
	convertToolDefs func([]*ai.Tool) []*jsonx.Object,
) amConvertedMessages {
	var params []*jsonx.Object
	assistantLevels := make(map[int]AnthropicEffort)
	var pendingSystemMessages []*jsonx.Object

	flushPending := func() {
		if len(pendingSystemMessages) > 0 {
			params = append(params, pendingSystemMessages...)
			pendingSystemMessages = nil
		}
	}

	for i := 0; i < len(transformed); i++ {
		switch msg := transformed[i].(type) {
		case *ai.SystemMessage:
			text := ai.RenderSystemMessageUpdate(msg)
			var blocks []*jsonx.Object
			if len(text) > 0 {
				blk := jsonx.NewObject()
				blk.Set("type", "text")
				blk.Set("text", ai.SanitizeSurrogates(text))
				blocks = append(blocks, blk)
			}
			if convertToolDefs != nil {
				redefined := make(map[string]bool, len(msg.ToolsAdded))
				for _, t := range msg.ToolsAdded {
					redefined[t.Name] = true
				}
				for _, t := range msg.ToolsRemoved {
					if redefined[t.Name] {
						continue
					}
					toolName := t.Name
					if isOAuth {
						toolName = amToClaudeCodeName(toolName)
					}
					ref := jsonx.NewObject()
					ref.Set("type", "tool_reference")
					ref.Set("name", toolName)
					rem := jsonx.NewObject()
					rem.Set("type", "tool_removal")
					rem.Set("tool", ref)
					blocks = append(blocks, rem)
				}
				for _, def := range convertToolDefs(msg.ToolsAdded) {
					td := jsonx.NewObject()
					td.Set("type", "tool_definition")
					td.Set("definition", def)
					addBlk := jsonx.NewObject()
					addBlk.Set("type", "tool_addition")
					addBlk.Set("tool", td)
					blocks = append(blocks, addBlk)
				}
			}
			if len(blocks) > 0 {
				mObj := jsonx.NewObject()
				mObj.Set("role", "system")
				mObj.Set("content", gsObjectsToAny(blocks))
				pendingSystemMessages = append(pendingSystemMessages, mObj)
			}

		case *ai.UserMessage:
			if msg.Content.Text != nil {
				if strings.TrimSpace(*msg.Content.Text) != "" {
					mObj := jsonx.NewObject()
					mObj.Set("role", "user")
					mObj.Set("content", ai.SanitizeSurrogates(*msg.Content.Text))
					params = append(params, mObj)
				}
				continue
			}
			var blocks []*jsonx.Object
			for _, item := range msg.Content.Blocks {
				switch b := item.(type) {
				case *ai.TextContent:
					san := ai.SanitizeSurrogates(b.Text)
					if strings.TrimSpace(san) == "" {
						continue
					}
					blk := jsonx.NewObject()
					blk.Set("type", "text")
					blk.Set("text", san)
					blocks = append(blocks, blk)
				case *ai.ImageContent:
					blocks = append(blocks, amConvertImageBlock(b))
				}
			}
			if len(blocks) == 0 {
				continue
			}
			mObj := jsonx.NewObject()
			mObj.Set("role", "user")
			mObj.Set("content", gsObjectsToAny(blocks))
			params = append(params, mObj)

		case *ai.AssistantMessage:
			var blocks []*jsonx.Object
			for _, item := range msg.Content {
				switch b := item.(type) {
				case *ai.TextContent:
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					blk := jsonx.NewObject()
					blk.Set("type", "text")
					blk.Set("text", ai.SanitizeSurrogates(b.Text))
					blocks = append(blocks, blk)
				case *ai.ThinkingContent:
					if b.Redacted != nil && *b.Redacted {
						blk := jsonx.NewObject()
						blk.Set("type", "redacted_thinking")
						if b.ThinkingSignature != nil {
							blk.Set("data", *b.ThinkingSignature)
						}
						blocks = append(blocks, blk)
						continue
					}
					hasSig := b.ThinkingSignature != nil && strings.TrimSpace(*b.ThinkingSignature) != ""
					if strings.TrimSpace(b.Thinking) == "" && !hasSig {
						continue
					}
					if !hasSig {
						if allowEmptySignature {
							blk := jsonx.NewObject()
							blk.Set("type", "thinking")
							blk.Set("thinking", ai.SanitizeSurrogates(b.Thinking))
							blk.Set("signature", "")
							blocks = append(blocks, blk)
						} else {
							blk := jsonx.NewObject()
							blk.Set("type", "text")
							blk.Set("text", ai.SanitizeSurrogates(b.Thinking))
							blocks = append(blocks, blk)
						}
					} else {
						blk := jsonx.NewObject()
						blk.Set("type", "thinking")
						blk.Set("thinking", ai.SanitizeSurrogates(b.Thinking))
						blk.Set("signature", *b.ThinkingSignature)
						blocks = append(blocks, blk)
					}
				case *ai.ToolCall:
					blk := jsonx.NewObject()
					blk.Set("type", "tool_use")
					blk.Set("id", b.Id)
					toolName := b.Name
					if isOAuth {
						toolName = amToClaudeCodeName(toolName)
					}
					blk.Set("name", toolName)
					args := b.Arguments
					if args == nil {
						args = jsonx.NewObject()
					}
					blk.Set("input", args)
					blocks = append(blocks, blk)
				}
			}
			if len(blocks) == 0 {
				continue
			}
			flushPending()
			msgIdx := len(params)
			mObj := jsonx.NewObject()
			mObj.Set("role", "assistant")
			mObj.Set("content", gsObjectsToAny(blocks))
			params = append(params, mObj)
			if managedProvider != "" &&
				msg.Api == ai.KnownApiAnthropicMessages &&
				string(msg.Provider) == managedProvider &&
				msg.ProviderThinkingLevel != nil &&
				amIsAnthropicEffort(*msg.ProviderThinkingLevel) {
				assistantLevels[msgIdx] = AnthropicEffort(*msg.ProviderThinkingLevel)
			}

		case *ai.ToolResultMessage:
			var toolResults []*jsonx.Object
			var errorImages []*jsonx.Object
			j := i
			for j < len(transformed) {
				tr, ok := transformed[j].(*ai.ToolResultMessage)
				if !ok {
					break
				}
				split := SplitErrorToolResultImages(tr)
				contentForTool := tr.Content
				if split != nil {
					contentForTool = make([]ai.Content, len(split.Content))
					for k, tc := range split.Content {
						contentForTool[k] = tc
					}
				}
				trObj := jsonx.NewObject()
				trObj.Set("type", "tool_result")
				trObj.Set("tool_use_id", tr.ToolCallId)
				trObj.Set("content", amConvertContentBlocks(contentForTool))
				trObj.Set("is_error", tr.IsError)
				toolResults = append(toolResults, trObj)
				if split != nil {
					capBlk := jsonx.NewObject()
					capBlk.Set("type", "text")
					capBlk.Set("text", split.Caption)
					errorImages = append(errorImages, capBlk)
					for _, img := range split.Images {
						errorImages = append(errorImages, amConvertImageBlock(img))
					}
				}
				j++
			}
			i = j - 1
			combined := make([]*jsonx.Object, 0, len(toolResults)+len(errorImages))
			combined = append(combined, toolResults...)
			combined = append(combined, errorImages...)
			mObj := jsonx.NewObject()
			mObj.Set("role", "user")
			mObj.Set("content", gsObjectsToAny(combined))
			params = append(params, mObj)
		}
	}

	flushPending()

	if cacheControl != nil && len(params) > 0 {
		lastMsg := params[len(params)-1]
		roleVal, _ := lastMsg.Get("role")
		if roleStr, _ := roleVal.(string); roleStr == "user" || roleStr == "system" {
			contentVal, _ := lastMsg.Get("content")
			switch cv := contentVal.(type) {
			case []any:
				if len(cv) > 0 {
					if lastBlk, ok := cv[len(cv)-1].(*jsonx.Object); ok && lastBlk != nil {
						tVal, _ := lastBlk.Get("type")
						if tStr, _ := tVal.(string); tStr == "text" || tStr == "image" || tStr == "tool_result" || tStr == "tool_addition" || tStr == "tool_removal" {
							lastBlk.Set("cache_control", gsCloneJSONValue(cacheControl))
						}
					}
				}
			case []*jsonx.Object:
				if len(cv) > 0 {
					lastBlk := cv[len(cv)-1]
					tVal, _ := lastBlk.Get("type")
					if tStr, _ := tVal.(string); tStr == "text" || tStr == "image" || tStr == "tool_result" || tStr == "tool_addition" || tStr == "tool_removal" {
						lastBlk.Set("cache_control", gsCloneJSONValue(cacheControl))
					}
				}
			case string:
				blk := jsonx.NewObject()
				blk.Set("type", "text")
				blk.Set("text", cv)
				blk.Set("cache_control", gsCloneJSONValue(cacheControl))
				lastMsg.Set("content", []any{blk})
			}
		}
	}

	return amConvertedMessages{
		messages:        params,
		assistantLevels: assistantLevels,
	}
}

func amIsAnthropicEffort(v string) bool {
	switch AnthropicEffort(v) {
	case AnthropicEffortLow, AnthropicEffortMedium, AnthropicEffortHigh, AnthropicEffortXhigh, AnthropicEffortMax:
		return true
	default:
		return false
	}
}

func amInsertThinkingLevelMessages(converted amConvertedMessages, activeEffort AnthropicEffort) []*jsonx.Object {
	out := make([]*jsonx.Object, 0, len(converted.messages)+len(converted.assistantLevels)+1)
	makeMarker := func(effort AnthropicEffort) *jsonx.Object {
		oc := jsonx.NewObject()
		oc.Set("effort", string(effort))
		m := jsonx.NewObject()
		m.Set("role", "system")
		m.Set("content", []any{})
		m.Set("output_config", oc)
		return m
	}
	for i, msg := range converted.messages {
		if hist, ok := converted.assistantLevels[i]; ok {
			out = append(out, makeMarker(hist))
		}
		out = append(out, msg)
	}
	out = append(out, makeMarker(activeEffort))
	return out
}

// --- Message threads helpers ---

type amPreparedThreadRequest struct {
	sessionId string
	body      *jsonx.Object
}

func amRemoveThreadOrderLocked(sessionId string) {
	for i, id := range amThreadOrder {
		if id == sessionId {
			amThreadOrder = append(amThreadOrder[:i], amThreadOrder[i+1:]...)
			return
		}
	}
}

func amSetThreadState(sessionId string, entry *amThreadEntry) {
	amThreadMu.Lock()
	defer amThreadMu.Unlock()
	delete(amThreadState, sessionId)
	amRemoveThreadOrderLocked(sessionId)
	amThreadState[sessionId] = entry
	amThreadOrder = append(amThreadOrder, sessionId)
	if len(amThreadOrder) > amMaxThreadStateEntries {
		oldest := amThreadOrder[0]
		amThreadOrder = amThreadOrder[1:]
		delete(amThreadState, oldest)
	}
}

func amNormalizeThreadJSON(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		if x == nil {
			return nil
		}
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			if k == "cache_control" {
				continue
			}
			val, _ := x.Get(k)
			if k == "content" {
				if s, ok := val.(string); ok {
					tb := jsonx.NewObject()
					tb.Set("type", "text")
					tb.Set("text", s)
					out.Set(k, []any{tb})
					continue
				}
			}
			out.Set(k, amNormalizeThreadJSON(val))
		}
		return out
	case []*jsonx.Object:
		arr := make([]any, len(x))
		for i, item := range x {
			arr[i] = amNormalizeThreadJSON(item)
		}
		return arr
	case []any:
		arr := make([]any, len(x))
		for i, item := range x {
			arr[i] = amNormalizeThreadJSON(item)
		}
		return arr
	default:
		return v
	}
}

func amHashThreadPrefix(model *ai.Model, params *jsonx.Object, messages []*jsonx.Object) string {
	getOrNil := func(k string) any {
		if params == nil {
			return nil
		}
		if v, ok := params.Get(k); ok {
			return v
		}
		return nil
	}
	settingsArr := []any{
		string(model.Provider),
		model.BaseUrl,
		getOrNil("model"),
		getOrNil("system"),
		getOrNil("tools"),
		getOrNil("thinking"),
		getOrNil("output_config"),
		getOrNil("tool_choice"),
		getOrNil("betas"),
		getOrNil("fallbacks"),
	}
	settingsBytes, _ := jsonx.Marshal(settingsArr)
	normMsgs := amNormalizeThreadJSON(messages)
	convBytes, _ := jsonx.Marshal(normMsgs)
	return ai.ShortHash(string(settingsBytes) + string(convBytes))
}

func amCloneParams(params *jsonx.Object) *jsonx.Object {
	if params == nil {
		return jsonx.NewObject()
	}
	out := jsonx.NewObject()
	for _, k := range params.Keys() {
		v, _ := params.Get(k)
		out.Set(k, v)
	}
	return out
}

func amExtractParamMessages(params *jsonx.Object) []*jsonx.Object {
	if params == nil {
		return nil
	}
	val, ok := params.Get("messages")
	if !ok {
		return nil
	}
	if list, ok := val.([]*jsonx.Object); ok {
		return list
	}
	if anyList, ok := val.([]any); ok {
		out := make([]*jsonx.Object, 0, len(anyList))
		for _, item := range anyList {
			if obj, ok := item.(*jsonx.Object); ok && obj != nil {
				out = append(out, obj)
			}
		}
		return out
	}
	return nil
}

func amCreateThreadRequest(sessionId string, params *jsonx.Object) *amPreparedThreadRequest {
	body := amCloneParams(params)
	th := jsonx.NewObject()
	th.Set("type", "create")
	body.Set("thread", th)
	return &amPreparedThreadRequest{sessionId: sessionId, body: body}
}

func amPrepareThreadRequest(model *ai.Model, sessionId string, params *jsonx.Object) *amPreparedThreadRequest {
	amThreadMu.Lock()
	state := amThreadState[sessionId]
	amThreadMu.Unlock()

	if state != nil && state.disabled {
		return nil
	}
	msgs := amExtractParamMessages(params)
	if state != nil && len(msgs) > state.prefixCount && amHashThreadPrefix(model, params, msgs[:state.prefixCount]) == state.prefixHash {
		amThreadMu.Lock()
		delete(amThreadState, sessionId)
		amRemoveThreadOrderLocked(sessionId)
		amThreadMu.Unlock()

		contBody := jsonx.NewObject()
		for _, k := range params.Keys() {
			if k == "system" || k == "tools" {
				continue
			}
			v, _ := params.Get(k)
			contBody.Set(k, v)
		}
		contBody.Set("messages", gsObjectsToAny(msgs[state.prefixCount:]))
		th := jsonx.NewObject()
		th.Set("type", "continue")
		th.Set("previous_message_id", state.replyId)
		contBody.Set("thread", th)
		return &amPreparedThreadRequest{sessionId: sessionId, body: contBody}
	}
	return amCreateThreadRequest(sessionId, params)
}

func amSendThreadRequest(
	ctx context.Context,
	thread *amPreparedThreadRequest,
	params *jsonx.Object,
	send func(body *jsonx.Object) (*http.Response, context.CancelFunc, error),
	output *ai.AssistantMessage,
) (*http.Response, context.CancelFunc, *amPreparedThreadRequest, error) {
	resp, cancel, err := send(thread.body)
	if err == nil {
		return resp, cancel, thread, nil
	}
	var apiErr *amAPIError
	if ctx.Err() != nil || !errors.As(err, &apiErr) {
		return nil, nil, nil, err
	}
	startNewThread := apiErr.ErrCode != "" && amNewThreadErrors[apiErr.ErrCode]
	threadsUnsupported := apiErr.ErrCode == "thread_unsupported_request" ||
		(apiErr.ErrCode == "" && apiErr.Status == 400 && strings.Contains(apiErr.Message, "thread"))
	if !startNewThread && !threadsUnsupported {
		return nil, nil, nil, err
	}
	reason := apiErr.ErrCode
	if reason == "" {
		reason = strconv.Itoa(apiErr.Status)
	}
	ai.AppendAssistantMessageDiagnostic(output, &ai.AssistantMessageDiagnostic{
		Type:      "anthropic_thread_fallback",
		Timestamp: js.DateNow(),
		Error:     &ai.DiagnosticErrorInfo{Message: apiErr.Message},
		Details:   jsonx.ObjectOf("reason", reason),
	})
	if startNewThread {
		created := amCreateThreadRequest(thread.sessionId, params)
		resp2, cancel2, err2 := send(created.body)
		return resp2, cancel2, created, err2
	}
	resp2, cancel2, err2 := send(params)
	if err2 == nil {
		amSetThreadState(thread.sessionId, &amThreadEntry{disabled: true})
	}
	return resp2, cancel2, nil, err2
}

func amRecordThreadReply(
	model *ai.Model,
	thread *amPreparedThreadRequest,
	params *jsonx.Object,
	output *ai.AssistantMessage,
	isOAuth bool,
	allowEmptySignature bool,
) {
	if output.ResponseId == nil || *output.ResponseId == "" || output.ResponseModel != nil {
		return
	}
	convertedReply := amConvertMessages(
		TransformMessages([]ai.Message{output}, model, amNormalizeToolCallId),
		isOAuth,
		nil,
		allowEmptySignature,
		"",
		nil,
	)
	var assistantReplies []*jsonx.Object
	for _, m := range convertedReply.messages {
		if r, ok := m.Get("role"); ok && r == "assistant" {
			assistantReplies = append(assistantReplies, m)
		}
	}
	existing := amExtractParamMessages(params)
	prefix := make([]*jsonx.Object, 0, len(existing)+len(assistantReplies))
	prefix = append(prefix, existing...)
	prefix = append(prefix, assistantReplies...)
	amSetThreadState(thread.sessionId, &amThreadEntry{
		prefixCount: len(prefix),
		prefixHash:  amHashThreadPrefix(model, params, prefix),
		replyId:     *output.ResponseId,
	})
}

// --- HTTP wire conversion & retry ---

func amExtractSDKHeaderParams(params *jsonx.Object, headers http.Header, explicitBetaHeader *string) *jsonx.Object {
	wire := jsonx.NewObject()
	if params == nil {
		return wire
	}
	var betas []string
	var userProfileId, workspaceId string

	for _, k := range params.Keys() {
		v, _ := params.Get(k)
		switch k {
		case "betas":
			switch b := v.(type) {
			case []string:
				betas = append(betas, b...)
			case []any:
				for _, item := range b {
					if s, ok := item.(string); ok && s != "" {
						betas = append(betas, s)
					}
				}
			}
		case "user_profile_id":
			if s, ok := v.(string); ok {
				userProfileId = s
			}
		case "workspace_id":
			if s, ok := v.(string); ok {
				workspaceId = s
			}
		default:
			wire.Set(k, v)
		}
	}

	if explicitBetaHeader == nil && len(betas) > 0 {
		headers.Set("anthropic-beta", strings.Join(betas, ","))
	} else if explicitBetaHeader != nil && *explicitBetaHeader != "" {
		headers.Set("anthropic-beta", *explicitBetaHeader)
	}
	if userProfileId != "" && headers.Get("anthropic-user-profile-id") == "" {
		headers.Set("anthropic-user-profile-id", userProfileId)
	}
	if workspaceId != "" && headers.Get("anthropic-workspace-id") == "" {
		headers.Set("anthropic-workspace-id", workspaceId)
	}
	return wire
}

func amSendWithRetry(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	headers http.Header,
	bodyBytes []byte,
	hdrTimeoutMs int64,
	retryOpts *ai.ProviderRetryOptions,
) (*http.Response, context.CancelFunc, error) {
	type resPair struct {
		resp   *http.Response
		cancel context.CancelFunc
	}
	result, err := ai.RetryProviderRequest(ctx, func(attemptCtx context.Context) (resPair, error) {
		hdrWaitCtx, cancelHdrWait := context.WithCancel(attemptCtx)
		if hdrTimeoutMs > 0 {
			stopTimer := ggaStartHeaderTimeoutWatcher(attemptCtx, hdrWaitCtx, cancelHdrWait, hdrTimeoutMs)
			defer stopTimer()
		}

		req, err := http.NewRequestWithContext(hdrWaitCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			cancelHdrWait()
			return resPair{}, err
		}
		for k, vals := range headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			cancelHdrWait()
			return resPair{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			rawBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			cancelHdrWait()
			return resPair{}, amParseAPIError(resp.StatusCode, resp.Header, rawBody)
		}
		return resPair{resp: resp, cancel: cancelHdrWait}, nil
	}, retryOpts)
	if err != nil {
		return nil, nil, err
	}
	return result.resp, result.cancel, nil
}

func amParseAPIError(status int, headers http.Header, rawBody []byte) *amAPIError {
	bodyStr := strings.TrimSpace(string(rawBody))
	apiErr := &amAPIError{
		Status:  status,
		Body:    bodyStr,
		Headers: headers,
	}
	if bodyStr == "" {
		apiErr.Message = fmt.Sprintf("HTTP %d", status)
		return apiErr
	}
	if parsed, err := jsonx.Parse([]byte(bodyStr)); err == nil {
		if root, ok := parsed.(*jsonx.Object); ok && root != nil {
			if errVal, ok := root.Get("error"); ok {
				if errObj, ok := errVal.(*jsonx.Object); ok && errObj != nil {
					if t, ok := errObj.Get("type"); ok {
						apiErr.ErrType, _ = t.(string)
					}
					if m, ok := errObj.Get("message"); ok {
						apiErr.Message, _ = m.(string)
					}
					if detailsVal, ok := errObj.Get("details"); ok {
						if dObj, ok := detailsVal.(*jsonx.Object); ok && dObj != nil {
							if code, ok := dObj.Get("error_code"); ok {
								apiErr.ErrCode, _ = code.(string)
							}
						}
					}
				}
			}
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = fmt.Sprintf("%d %s", status, bodyStr)
	} else {
		apiErr.Message = fmt.Sprintf("%d %s", status, bodyStr)
	}
	return apiErr
}

// --- SSE parsing ---

type amOpenBlock struct {
	contentIndex int
	arguments    *ai.ToolCallArgumentsStream
}

func amConsumeSSEStream(
	ctx context.Context,
	body io.Reader,
	model *ai.Model,
	currentTools []*ai.Tool,
	isOAuth bool,
	stream *ai.AssistantMessageEventStream,
	output *ai.AssistantMessage,
	onProviderStreamEvent func(data any, model *ai.Model) error,
) ([]any, *ai.Model, error) {
	reader := sse.NewReader(body)
	openBlocks := make(map[int]*amOpenBlock)
	var inputTransformations []any
	usageModel := model
	sawMessageStart := false
	sawMessageEnd := false

	for {
		if ctx.Err() != nil {
			return inputTransformations, usageModel, ai.AbortNewError("Request was aborted")
		}
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return inputTransformations, usageModel, err
		}

		evType := ev.Event
		if evType == "error" {
			return inputTransformations, usageModel, errors.New(ev.Data)
		}
		if !amAnthropicMessageEvts[evType] {
			continue
		}

		parsed, err := ai.ParseJsonWithRepair(ev.Data)
		if err != nil {
			return inputTransformations, usageModel, fmt.Errorf(
				"Could not parse Anthropic SSE event %s: %v; data=%s", evType, err, ev.Data,
			)
		}
		eventObj, ok := parsed.(*jsonx.Object)
		if !ok || eventObj == nil {
			return inputTransformations, usageModel, fmt.Errorf(
				"Could not parse Anthropic SSE event %s: expected JSON object; data=%s", evType, ev.Data,
			)
		}

		if onProviderStreamEvent != nil {
			if err := onProviderStreamEvent(eventObj, model); err != nil {
				return inputTransformations, usageModel, err
			}
		}

		typeVal, _ := eventObj.Get("type")
		msgEventType, _ := typeVal.(string)
		switch msgEventType {
		case "message_start":
			sawMessageStart = true
			if msgVal, ok := eventObj.Get("message"); ok {
				if msgObj, ok := msgVal.(*jsonx.Object); ok && msgObj != nil {
					if idVal, ok := msgObj.Get("id"); ok {
						if idStr, ok := idVal.(string); ok && idStr != "" {
							output.ResponseId = &idStr
						}
					}
					if trVal, ok := msgObj.Get("input_transformations"); ok {
						if arr, ok := trVal.([]any); ok {
							inputTransformations = arr
						}
					}
					if rmVal, ok := msgObj.Get("model"); ok {
						if rmStr, ok := rmVal.(string); ok && rmStr != "" && rmStr != model.Id {
							output.ResponseModel = &rmStr
							usageModel = amResolveFallbackUsageModel(model, rmStr)
						}
					}
					if usageVal, ok := msgObj.Get("usage"); ok {
						if uObj, ok := usageVal.(*jsonx.Object); ok && uObj != nil {
							output.Usage.Input = ggaGetIntField(uObj, "input_tokens")
							output.Usage.Output = ggaGetIntField(uObj, "output_tokens")
							output.Usage.CacheRead = ggaGetIntField(uObj, "cache_read_input_tokens")
							output.Usage.CacheWrite = ggaGetIntField(uObj, "cache_creation_input_tokens")
							if ccVal, ok := uObj.Get("cache_creation"); ok {
								if ccObj, ok := ccVal.(*jsonx.Object); ok && ccObj != nil {
									c1h := ggaGetIntField(ccObj, "ephemeral_1h_input_tokens")
									output.Usage.CacheWrite1h = &c1h
								}
							}
							output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
							ai.CalculateCost(usageModel, &output.Usage)
						}
					}
				}
			}

		case "content_block_start":
			idx := ggaGetIntField(eventObj, "index")
			cbVal, _ := eventObj.Get("content_block")
			cbObj, ok := cbVal.(*jsonx.Object)
			if !ok || cbObj == nil {
				continue
			}
			cbTypeVal, _ := cbObj.Get("type")
			cbType, _ := cbTypeVal.(string)
			switch cbType {
			case "fallback":
				if len(output.Content) > 0 {
					return inputTransformations, usageModel, errors.New("Anthropic performed an unsupported mid-output model fallback")
				}
			case "text":
				txt := ""
				if tVal, ok := cbObj.Get("text"); ok {
					txt, _ = tVal.(string)
				}
				contentIdx := len(output.Content)
				output.Content = append(output.Content, &ai.TextContent{Type: "text", Text: txt})
				openBlocks[idx] = &amOpenBlock{contentIndex: contentIdx}
				stream.Push(&ai.AssistantMessageEventTextStart{
					Type:         "text_start",
					ContentIndex: contentIdx,
					Partial:      output,
				})
			case "thinking":
				th := ""
				if tVal, ok := cbObj.Get("thinking"); ok {
					th, _ = tVal.(string)
				}
				sig := ""
				if sVal, ok := cbObj.Get("signature"); ok {
					sig, _ = sVal.(string)
				}
				contentIdx := len(output.Content)
				output.Content = append(output.Content, &ai.ThinkingContent{
					Type:              "thinking",
					Thinking:          th,
					ThinkingSignature: &sig,
				})
				openBlocks[idx] = &amOpenBlock{contentIndex: contentIdx}
				stream.Push(&ai.AssistantMessageEventThinkingStart{
					Type:         "thinking_start",
					ContentIndex: contentIdx,
					Partial:      output,
				})
			case "redacted_thinking":
				dataStr := ""
				if dVal, ok := cbObj.Get("data"); ok {
					dataStr, _ = dVal.(string)
				}
				contentIdx := len(output.Content)
				redacted := true
				output.Content = append(output.Content, &ai.ThinkingContent{
					Type:              "thinking",
					Thinking:          "[Reasoning redacted]",
					ThinkingSignature: &dataStr,
					Redacted:          &redacted,
				})
				openBlocks[idx] = &amOpenBlock{contentIndex: contentIdx}
				stream.Push(&ai.AssistantMessageEventThinkingStart{
					Type:         "thinking_start",
					ContentIndex: contentIdx,
					Partial:      output,
				})
			case "tool_use":
				idVal, _ := cbObj.Get("id")
				idStr, _ := idVal.(string)
				nameVal, _ := cbObj.Get("name")
				nameStr, _ := nameVal.(string)
				if isOAuth {
					nameStr = amFromClaudeCodeName(nameStr, currentTools)
				}
				argsObj := jsonx.NewObject()
				if inVal, ok := cbObj.Get("input"); ok {
					if obj, ok := inVal.(*jsonx.Object); ok && obj != nil {
						argsObj = obj
					}
				}
				contentIdx := len(output.Content)
				output.Content = append(output.Content, &ai.ToolCall{
					Type:      "toolCall",
					Id:        idStr,
					Name:      nameStr,
					Arguments: argsObj,
				})
				openBlocks[idx] = &amOpenBlock{
					contentIndex: contentIdx,
					arguments:    ai.NewToolCallArgumentsStream(),
				}
				stream.Push(&ai.AssistantMessageEventToolcallStart{
					Type:         "toolcall_start",
					ContentIndex: contentIdx,
					Partial:      output,
				})
			}

		case "content_block_delta":
			idx := ggaGetIntField(eventObj, "index")
			open, ok := openBlocks[idx]
			if !ok || open.contentIndex >= len(output.Content) {
				continue
			}
			dVal, _ := eventObj.Get("delta")
			dObj, ok := dVal.(*jsonx.Object)
			if !ok || dObj == nil {
				continue
			}
			dtVal, _ := dObj.Get("type")
			deltaType, _ := dtVal.(string)
			switch deltaType {
			case "text_delta":
				if tc, ok := output.Content[open.contentIndex].(*ai.TextContent); ok {
					txt, _ := dObj.Get("text")
					deltaStr, _ := txt.(string)
					tc.Text += deltaStr
					stream.Push(&ai.AssistantMessageEventTextDelta{
						Type:         "text_delta",
						ContentIndex: open.contentIndex,
						Delta:        deltaStr,
						Partial:      output,
					})
				}
			case "thinking_delta":
				if tc, ok := output.Content[open.contentIndex].(*ai.ThinkingContent); ok {
					th, _ := dObj.Get("thinking")
					deltaStr, _ := th.(string)
					tc.Thinking += deltaStr
					stream.Push(&ai.AssistantMessageEventThinkingDelta{
						Type:         "thinking_delta",
						ContentIndex: open.contentIndex,
						Delta:        deltaStr,
						Partial:      output,
					})
				}
			case "input_json_delta":
				if tc, ok := output.Content[open.contentIndex].(*ai.ToolCall); ok && open.arguments != nil {
					pj, _ := dObj.Get("partial_json")
					deltaStr, _ := pj.(string)
					if parsedArgs := open.arguments.Append(deltaStr); parsedArgs != nil {
						tc.Arguments = parsedArgs
					}
					stream.Push(&ai.AssistantMessageEventToolcallDelta{
						Type:         "toolcall_delta",
						ContentIndex: open.contentIndex,
						Delta:        deltaStr,
						Partial:      output,
					})
				}
			case "signature_delta":
				if tc, ok := output.Content[open.contentIndex].(*ai.ThinkingContent); ok {
					sig, _ := dObj.Get("signature")
					deltaStr, _ := sig.(string)
					prev := ""
					if tc.ThinkingSignature != nil {
						prev = *tc.ThinkingSignature
					}
					combined := prev + deltaStr
					tc.ThinkingSignature = &combined
				}
			}

		case "content_block_stop":
			idx := ggaGetIntField(eventObj, "index")
			open, ok := openBlocks[idx]
			delete(openBlocks, idx)
			if !ok || open.contentIndex >= len(output.Content) {
				continue
			}
			switch b := output.Content[open.contentIndex].(type) {
			case *ai.TextContent:
				stream.Push(&ai.AssistantMessageEventTextEnd{
					Type:         "text_end",
					ContentIndex: open.contentIndex,
					Content:      b.Text,
					Partial:      output,
				})
			case *ai.ThinkingContent:
				stream.Push(&ai.AssistantMessageEventThinkingEnd{
					Type:         "thinking_end",
					ContentIndex: open.contentIndex,
					Content:      b.Thinking,
					Partial:      output,
				})
			case *ai.ToolCall:
				if open.arguments != nil {
					args, argsErr := open.arguments.Finish()
					if args != nil {
						b.Arguments = args
					}
					b.ArgumentsError = argsErr
				}
				stream.Push(&ai.AssistantMessageEventToolcallEnd{
					Type:         "toolcall_end",
					ContentIndex: open.contentIndex,
					ToolCall:     b,
					Partial:      output,
				})
			}

		case "message_delta":
			if trVal, ok := eventObj.Get("input_transformations"); ok {
				if arr, ok := trVal.([]any); ok {
					inputTransformations = arr
				}
			}
			if dVal, ok := eventObj.Get("delta"); ok {
				if dObj, ok := dVal.(*jsonx.Object); ok && dObj != nil {
					if srVal, ok := dObj.Get("stop_reason"); ok {
						if srStr, ok := srVal.(string); ok && srStr != "" {
							raw := srStr
							output.RawStopReason = &raw
							var detailsObj *jsonx.Object
							if sdVal, ok := dObj.Get("stop_details"); ok {
								detailsObj, _ = sdVal.(*jsonx.Object)
							}
							mapped, errMsg, err := amMapStopReason(srStr, detailsObj)
							if err != nil {
								return inputTransformations, usageModel, err
							}
							output.StopReason = mapped
							if errMsg != "" {
								e := errMsg
								output.ErrorMessage = &e
							}
						}
					}
				}
			}
			if usageVal, ok := eventObj.Get("usage"); ok {
				if uObj, ok := usageVal.(*jsonx.Object); ok && uObj != nil {
					if v, ok := uObj.Get("input_tokens"); ok && v != nil {
						output.Usage.Input = ggaToInt(v)
					}
					if v, ok := uObj.Get("output_tokens"); ok && v != nil {
						output.Usage.Output = ggaToInt(v)
					}
					if v, ok := uObj.Get("cache_read_input_tokens"); ok && v != nil {
						output.Usage.CacheRead = ggaToInt(v)
					}
					if v, ok := uObj.Get("cache_creation_input_tokens"); ok && v != nil {
						output.Usage.CacheWrite = ggaToInt(v)
					}
					if ccVal, ok := uObj.Get("cache_creation"); ok && ccVal != nil {
						if ccObj, ok := ccVal.(*jsonx.Object); ok && ccObj != nil {
							if v, ok := ccObj.Get("ephemeral_1h_input_tokens"); ok && v != nil {
								c1h := ggaToInt(v)
								output.Usage.CacheWrite1h = &c1h
							}
						}
					}
					if odVal, ok := uObj.Get("output_tokens_details"); ok && odVal != nil {
						if odObj, ok := odVal.(*jsonx.Object); ok && odObj != nil {
							if v, ok := odObj.Get("thinking_tokens"); ok && v != nil {
								tt := ggaToInt(v)
								output.Usage.Reasoning = &tt
							}
						}
					}
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
					ai.CalculateCost(usageModel, &output.Usage)
					if ai.IsOpenRouterModel(model) {
						if reported := amParseOpenRouterUsageAccounting(uObj); reported != nil {
							ai.ApplyOpenRouterReportedCost(&output.Usage, reported)
						}
					}
				}
			}

		case "message_stop":
			sawMessageEnd = true
		}
	}

	if sawMessageStart && !sawMessageEnd {
		return inputTransformations, usageModel, errors.New("Anthropic stream ended before message_stop")
	}
	return inputTransformations, usageModel, nil
}

func amResolveFallbackUsageModel(model *ai.Model, responseModel string) *ai.Model {
	compat := amGetCompat(model)
	for _, fb := range compat.allowedFallbackModels {
		hasCost := fb.Cost.Input != 0 || fb.Cost.Output != 0 || fb.Cost.CacheRead != 0 || fb.Cost.CacheWrite != 0 || len(fb.Cost.Tiers) > 0
		if fb.Provider == model.Provider && fb.Model == responseModel && hasCost {
			cloned := *model
			cloned.Id = responseModel
			cloned.Cost = fb.Cost
			return &cloned
		}
	}
	return model
}

func amParseOpenRouterUsageAccounting(uObj *jsonx.Object) *ai.OpenRouterUsageAccounting {
	rawBytes, err := jsonx.Marshal(uObj)
	if err != nil {
		return nil
	}
	var out ai.OpenRouterUsageAccounting
	if err := jsonx.Unmarshal(rawBytes, &out); err != nil {
		return nil
	}
	return &out
}

func amMapStopReason(reason string, stopDetails *jsonx.Object) (ai.StopReason, string, error) {
	switch reason {
	case "end_turn":
		return ai.StopReasonStop, "", nil
	case "max_tokens":
		return ai.StopReasonLength, "", nil
	case "tool_use":
		return ai.StopReasonToolUse, "", nil
	case "refusal":
		expl := ""
		if stopDetails != nil {
			if e, ok := stopDetails.Get("explanation"); ok {
				expl, _ = e.(string)
			}
		}
		if expl == "" {
			expl = "The model refused to complete the request"
		}
		return ai.StopReasonError, expl, nil
	case "pause_turn":
		return ai.StopReasonStop, "", nil
	case "stop_sequence":
		return ai.StopReasonStop, "", nil
	case "sensitive":
		return ai.StopReasonError, "Provider stopped with: sensitive", nil
	default:
		return "", "", fmt.Errorf("Unhandled stop reason: %s", reason)
	}
}

func amHasHeaderSliceContain(slice []string, target string) bool {
	return slices.Contains(slice, target)
}
