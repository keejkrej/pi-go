// Ported from packages/ai/src/api/openai-codex-responses.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/sse"
	"github.com/klauspost/compress/zstd"
)

const (
	ocrDefaultCodexBaseURL                 = "https://chatgpt.com/backend-api"
	ocrJWTClaimPath                        = "https://api.openai.com/auth"
	ocrDefaultMaxRetries                   = 0
	ocrBaseDelayMs                         = 1000
	ocrDefaultMaxRetryDelayMs        int64 = 60_000
	ocrDefaultWebsocketConnectMs     int64 = 15_000
	ocrRequestCompressionZstdLevel         = 3
	ocrWebsocketMessageTooBigClose         = 1009
	ocrWebsocketConnLimitReachedCode       = "websocket_connection_limit_reached"
	ocrPreviousResponseNotFoundCode        = "previous_response_not_found"
	ocrOpenAIBetaResponsesWebSockets       = "responses_websockets=2026-02-06"
	ocrSessionWebsocketCacheTTLMs    int64 = 5 * 60 * 1000
	ocrSessionWebsocketMaxAgeMs      int64 = 55 * 60 * 1000
)

var (
	ocrCodexToolCallProviders = map[string]struct{}{
		"openai":       {},
		"openai-codex": {},
		"opencode":     {},
	}
	ocrCodexResponseStatuses = map[string]struct{}{
		"completed":   {},
		"incomplete":  {},
		"failed":      {},
		"cancelled":   {},
		"queued":      {},
		"in_progress": {},
	}
	ocrTerminalRateLimitRe = regexp.MustCompile(`(?i)GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)
	ocrRetryableErrorRe    = regexp.MustCompile(`(?i)rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)
	ocrUsageLimitCodeRe    = regexp.MustCompile(`(?i)usage_limit_reached|usage_not_included|rate_limit_exceeded`)
)

func init() {
	ai.RegisterSessionResourceCleanup(CloseOpenAICodexWebSocketSessions)
}

// OpenAICodexResponsesOptions configures OpenAI Codex Responses streaming requests.
type OpenAICodexResponsesOptions struct {
	ai.StreamOptions
	ReasoningEffort  *ai.ThinkingLevel `json:"reasoningEffort,omitzero"`
	ReasoningSummary *string           `json:"reasoningSummary,omitzero"`
	ServiceTier      *string           `json:"serviceTier,omitzero"`
	TextVerbosity    *string           `json:"textVerbosity,omitzero"`
	ToolChoice       any               `json:"toolChoice,omitzero"`
}

// OpenAICodexWebSocketDebugStats records per-session WebSocket pooling and fallback metrics.
type OpenAICodexWebSocketDebugStats struct {
	Requests                int     `json:"requests"`
	ConnectionsCreated      int     `json:"connectionsCreated"`
	ConnectionsReused       int     `json:"connectionsReused"`
	CachedContextRequests   int     `json:"cachedContextRequests"`
	StoreTrueRequests       int     `json:"storeTrueRequests"`
	FullContextRequests     int     `json:"fullContextRequests"`
	DeltaRequests           int     `json:"deltaRequests"`
	LastInputItems          int     `json:"lastInputItems"`
	LastDeltaInputItems     *int    `json:"lastDeltaInputItems,omitzero"`
	LastPreviousResponseId  *string `json:"lastPreviousResponseId,omitzero"`
	WebsocketFailures       int     `json:"websocketFailures"`
	SseFallbacks            int     `json:"sseFallbacks"`
	WebsocketFallbackActive *bool   `json:"websocketFallbackActive,omitzero"`
	LastWebSocketError      *string `json:"lastWebSocketError,omitzero"`
}

type ocrRetryDelayExceededError struct{ message string }

func (e *ocrRetryDelayExceededError) Error() string { return e.message }

type ocrCodexApiError struct {
	message string
	code    string
	payload *jsonx.Object
	cause   error
}

func (e *ocrCodexApiError) Error() string { return e.message }
func (e *ocrCodexApiError) Name() string  { return "CodexApiError" }
func (e *ocrCodexApiError) Unwrap() error { return e.cause }

type ocrCodexProtocolError struct {
	message string
	payload any
	cause   error
}

func (e *ocrCodexProtocolError) Error() string { return e.message }
func (e *ocrCodexProtocolError) Name() string  { return "CodexProtocolError" }
func (e *ocrCodexProtocolError) Unwrap() error { return e.cause }

type ocrProviderStreamEventCallbackError struct {
	message string
	cause   error
}

func (e *ocrProviderStreamEventCallbackError) Error() string { return e.message }
func (e *ocrProviderStreamEventCallbackError) Name() string {
	return "ProviderStreamEventCallbackError"
}
func (e *ocrProviderStreamEventCallbackError) Unwrap() error { return e.cause }

type ocrWebSocketCloseError struct {
	message  string
	code     int
	reason   string
	wasClean bool
}

func (e *ocrWebSocketCloseError) Error() string { return e.message }
func (e *ocrWebSocketCloseError) Name() string  { return "WebSocketCloseError" }

type ocrCachedWebSocketContinuationState struct {
	lastRequestBody   *jsonx.Object
	lastResponseId    string
	lastResponseItems []any
}

type ocrCachedWebSocketConnection struct {
	conn         *websocket.Conn
	busy         bool
	closed       bool
	createdAt    int64
	idleTimer    *time.Timer
	continuation *ocrCachedWebSocketContinuationState
}

var (
	ocrWSMu                        sync.Mutex
	ocrWebsocketSessionCache       = make(map[string]map[string]*ocrCachedWebSocketConnection)
	ocrWebsocketDebugStats         = make(map[string]*OpenAICodexWebSocketDebugStats)
	ocrWebsocketSseFallbackSession = make(map[string]struct{})
)

func ocrCloneStats(s *OpenAICodexWebSocketDebugStats) *OpenAICodexWebSocketDebugStats {
	if s == nil {
		return nil
	}
	cp := *s
	if s.LastDeltaInputItems != nil {
		v := *s.LastDeltaInputItems
		cp.LastDeltaInputItems = &v
	}
	if s.LastPreviousResponseId != nil {
		v := *s.LastPreviousResponseId
		cp.LastPreviousResponseId = &v
	}
	if s.WebsocketFallbackActive != nil {
		v := *s.WebsocketFallbackActive
		cp.WebsocketFallbackActive = &v
	}
	if s.LastWebSocketError != nil {
		v := *s.LastWebSocketError
		cp.LastWebSocketError = &v
	}
	return &cp
}

func ocrGetOrCreateDebugStatsLocked(sessionId string) *OpenAICodexWebSocketDebugStats {
	stats, ok := ocrWebsocketDebugStats[sessionId]
	if !ok {
		stats = &OpenAICodexWebSocketDebugStats{}
		ocrWebsocketDebugStats[sessionId] = stats
	}
	return stats
}

// GetOpenAICodexWebSocketDebugStats returns a copy of debug stats recorded for sessionId.
func GetOpenAICodexWebSocketDebugStats(sessionId string) *OpenAICodexWebSocketDebugStats {
	ocrWSMu.Lock()
	defer ocrWSMu.Unlock()
	return ocrCloneStats(ocrWebsocketDebugStats[sessionId])
}

// ResetOpenAICodexWebSocketDebugStats clears cached debug stats and SSE fallback markers.
func ResetOpenAICodexWebSocketDebugStats(sessionId *string) {
	ocrWSMu.Lock()
	defer ocrWSMu.Unlock()
	if sessionId != nil && *sessionId != "" {
		delete(ocrWebsocketDebugStats, *sessionId)
		delete(ocrWebsocketSseFallbackSession, *sessionId)
		return
	}
	clear(ocrWebsocketDebugStats)
	clear(ocrWebsocketSseFallbackSession)
}

func ocrCloseWebSocketSilently(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	if conn == nil {
		return
	}
	_ = conn.Close(code, reason)
}

// CloseOpenAICodexWebSocketSessions closes pooled Codex WebSocket connections for sessionId or all sessions.
func CloseOpenAICodexWebSocketSessions(sessionId *string) {
	var toClose []*websocket.Conn
	ocrWSMu.Lock()
	if sessionId != nil && *sessionId != "" {
		if accountEntries, ok := ocrWebsocketSessionCache[*sessionId]; ok {
			for _, entry := range accountEntries {
				if entry.idleTimer != nil {
					entry.idleTimer.Stop()
					entry.idleTimer = nil
				}
				entry.closed = true
				if entry.conn != nil {
					toClose = append(toClose, entry.conn)
				}
			}
			delete(ocrWebsocketSessionCache, *sessionId)
		}
	} else {
		for _, accountEntries := range ocrWebsocketSessionCache {
			for _, entry := range accountEntries {
				if entry.idleTimer != nil {
					entry.idleTimer.Stop()
					entry.idleTimer = nil
				}
				entry.closed = true
				if entry.conn != nil {
					toClose = append(toClose, entry.conn)
				}
			}
		}
		clear(ocrWebsocketSessionCache)
	}
	ocrWSMu.Unlock()

	for _, conn := range toClose {
		ocrCloseWebSocketSilently(conn, websocket.StatusNormalClosure, "debug_close")
	}
}

func ocrIsWebSocketSseFallbackActive(sessionId string) bool {
	if sessionId == "" {
		return false
	}
	ocrWSMu.Lock()
	defer ocrWSMu.Unlock()
	_, ok := ocrWebsocketSseFallbackSession[sessionId]
	return ok
}

func ocrRecordWebSocketSseFallback(sessionId string) {
	if sessionId == "" {
		return
	}
	ocrWSMu.Lock()
	defer ocrWSMu.Unlock()
	stats := ocrGetOrCreateDebugStatsLocked(sessionId)
	stats.SseFallbacks++
	_, active := ocrWebsocketSseFallbackSession[sessionId]
	stats.WebsocketFallbackActive = &active
}

func ocrRecordWebSocketFailure(sessionId string, err error) {
	if sessionId == "" {
		return
	}
	ocrWSMu.Lock()
	defer ocrWSMu.Unlock()
	ocrWebsocketSseFallbackSession[sessionId] = struct{}{}
	stats := ocrGetOrCreateDebugStatsLocked(sessionId)
	stats.WebsocketFailures++
	formatted := ai.FormatThrownValue(err)
	stats.LastWebSocketError = &formatted
	t := true
	stats.WebsocketFallbackActive = &t
}

func ocrScheduleSessionWebSocketExpiryLocked(sessionId, accountId string, entry *ocrCachedWebSocketConnection) {
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
	}
	entry.idleTimer = time.AfterFunc(time.Duration(ocrSessionWebsocketCacheTTLMs)*time.Millisecond, func() {
		var connToClose *websocket.Conn
		ocrWSMu.Lock()
		if !entry.busy && !entry.closed {
			if accountEntries := ocrWebsocketSessionCache[sessionId]; accountEntries != nil && accountEntries[accountId] == entry {
				delete(accountEntries, accountId)
				if len(accountEntries) == 0 {
					delete(ocrWebsocketSessionCache, sessionId)
				}
				entry.closed = true
				connToClose = entry.conn
			}
		}
		ocrWSMu.Unlock()
		if connToClose != nil {
			ocrCloseWebSocketSilently(connToClose, websocket.StatusNormalClosure, "idle_timeout")
		}
	})
}

func ocrExtractOptions(options ai.ProviderStreamOptions) *OpenAICodexResponsesOptions {
	if options == nil {
		return &OpenAICodexResponsesOptions{}
	}
	if o, ok := options.(*OpenAICodexResponsesOptions); ok && o != nil {
		cp := *o
		return &cp
	}
	var out OpenAICodexResponsesOptions
	if base := options.BaseStreamOptions(); base != nil {
		out.StreamOptions = *base
	}
	return &out
}

func ocrNormalizeTimeoutMs(value *int64) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 {
		return nil, fmt.Errorf("Invalid timeoutMs: %d", *value)
	}
	v := *value
	return &v, nil
}

func ocrGetCompatFlags(model *ai.Model) (supportsStrictMode bool, supportsGrammar bool, supportsAdditional bool, supportsToolSearch bool, supportsMidConvoSys bool) {
	supportsStrictMode = true
	if model == nil || model.Compat.V == nil {
		return
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
		return
	}
	if respCompat.SupportsStrictMode != nil {
		supportsStrictMode = *respCompat.SupportsStrictMode
	}
	if respCompat.SupportsOpenAIGrammarTools != nil {
		supportsGrammar = *respCompat.SupportsOpenAIGrammarTools
	}
	if respCompat.SupportsAdditionalTools != nil {
		supportsAdditional = *respCompat.SupportsAdditionalTools
	}
	if respCompat.SupportsToolSearch != nil {
		supportsToolSearch = *respCompat.SupportsToolSearch
	}
	if respCompat.SupportsMidConvoSystemMessages != nil {
		supportsMidConvoSys = *respCompat.SupportsMidConvoSystemMessages
	}
	return
}

func ocrExtractAccountId(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("Failed to extract accountId from token")
	}
	rawSeg := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	switch len(rawSeg) % 4 {
	case 2:
		rawSeg += "=="
	case 3:
		rawSeg += "="
	case 1:
		return "", errors.New("Failed to extract accountId from token")
	}
	decoded, err := base64.StdEncoding.DecodeString(rawSeg)
	if err != nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	parsed, err := jsonx.Parse(decoded)
	if err != nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	claim, ok := obj.GetObject(ocrJWTClaimPath)
	if !ok || claim == nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	accountID, ok := claim.GetString("chatgpt_account_id")
	if !ok || accountID == "" {
		return "", errors.New("Failed to extract accountId from token")
	}
	return accountID, nil
}

func ocrResolveCodexUrl(baseUrl string) string {
	raw := baseUrl
	if len(js.Trim(raw)) == 0 {
		raw = ocrDefaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	if strings.HasSuffix(normalized, "/codex/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/codex") {
		return normalized + "/responses"
	}
	return normalized + "/codex/responses"
}

func ocrResolveCodexWebSocketUrl(baseUrl string) (string, error) {
	u, err := url.Parse(ocrResolveCodexUrl(baseUrl))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	return u.String(), nil
}

func ocrBuildBaseCodexHeaders(
	defaultHeaders map[string]string,
	initHeaders *omap.Map[string, string],
	additionalHeaders ai.ProviderHeaders,
	accountId string,
	token string,
) http.Header {
	h := make(http.Header)
	for k, v := range defaultHeaders {
		h.Set(k, v)
	}
	if initHeaders != nil {
		for k, v := range initHeaders.All() {
			h.Set(k, v)
		}
	}
	if additionalHeaders != nil {
		for k, v := range additionalHeaders.All() {
			orsDeleteHeaderCaseInsensitive(h, k)
			if v != nil {
				h.Set(k, *v)
			}
		}
	}
	h.Set("Authorization", "Bearer "+token)
	h.Set("chatgpt-account-id", accountId)
	return h
}

func ocrBuildSSEHeaders(
	defaultHeaders map[string]string,
	initHeaders *omap.Map[string, string],
	additionalHeaders ai.ProviderHeaders,
	accountId string,
	token string,
	sessionId string,
) http.Header {
	h := ocrBuildBaseCodexHeaders(defaultHeaders, initHeaders, additionalHeaders, accountId, token)
	h.Set("OpenAI-Beta", "responses=experimental")
	h.Set("Accept", "text/event-stream")
	h.Set("Content-Type", "application/json")
	if sessionId != "" {
		h.Set("session-id", sessionId)
		h.Set("x-client-request-id", sessionId)
	}
	return h
}

func ocrBuildWebSocketHeaders(
	defaultHeaders map[string]string,
	initHeaders *omap.Map[string, string],
	additionalHeaders ai.ProviderHeaders,
	accountId string,
	token string,
	requestId string,
) http.Header {
	h := ocrBuildBaseCodexHeaders(defaultHeaders, initHeaders, additionalHeaders, accountId, token)
	orsDeleteHeaderCaseInsensitive(h, "accept")
	orsDeleteHeaderCaseInsensitive(h, "content-type")
	orsDeleteHeaderCaseInsensitive(h, "openai-beta")
	h.Set("OpenAI-Beta", ocrOpenAIBetaResponsesWebSockets)
	h.Set("x-client-request-id", requestId)
	h.Set("session-id", requestId)
	return h
}

func ocrHTTPClient(customClient *http.Client, env ai.ProviderEnv) *http.Client {
	if customClient != nil {
		return customClient
	}
	if env == nil {
		return ai.NodeHttpProxyDefaultClient()
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.IdleConnTimeout = 90 * time.Second
	base.Proxy = func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		return ai.ResolveHttpProxyUrlForTarget(req.URL.String(), env)
	}
	return &http.Client{Transport: base}
}

func ocrCompressRequestBodyZstd(bodyBytes []byte) []byte {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(ocrRequestCompressionZstdLevel)))
	if err != nil {
		return nil
	}
	defer enc.Close()
	return enc.EncodeAll(bodyBytes, nil)
}

func ocrLookupCodexThinkingLevel(model *ai.Model, effort string) (string, bool, bool) {
	if model == nil || model.ThinkingLevelMap == nil {
		return "", false, false
	}
	var opt jsonx.Opt[string]
	switch effort {
	case "none", "off", "":
		opt = model.ThinkingLevelMap.Off
	case "minimal":
		opt = model.ThinkingLevelMap.Minimal
	case "low":
		opt = model.ThinkingLevelMap.Low
	case "medium":
		opt = model.ThinkingLevelMap.Medium
	case "high":
		opt = model.ThinkingLevelMap.High
	case "xhigh":
		opt = model.ThinkingLevelMap.Xhigh
	case "max":
		opt = model.ThinkingLevelMap.Max
	}
	if opt.IsNull() {
		return "", true, true
	}
	if s, ok := opt.Get(); ok {
		return s, false, true
	}
	return "", false, false
}

func ocrBuildRequestBody(
	model *ai.Model,
	c *ai.TranscriptContext,
	options *OpenAICodexResponsesOptions,
	cacheSessionId string,
	grammarToolInputProperties map[string]string,
) (*jsonx.Object, error) {
	supportsStrictMode, supportsGrammar, supportsAdditional, supportsToolSearch, supportsMidConvoSys := ocrGetCompatFlags(model)
	transcriptTools := ai.ResolveTranscriptTools(c.Messages, GetResponsesToolSupport(ResponsesToolSupportOptions{
		SupportsMidConvoSystemMessages: supportsMidConvoSys,
		SupportsAdditionalTools:        supportsAdditional,
		SupportsToolSearch:             supportsToolSearch,
	}))
	includeSystemFalse := false
	messages, err := ConvertResponsesMessages(model, c, ocrCodexToolCallProviders, &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt:            &includeSystemFalse,
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: &supportsMidConvoSys,
		SupportsAdditionalTools:        supportsAdditional,
		SupportsToolSearch:             supportsToolSearch,
		ToolOptions: &ConvertResponsesToolsOptions{
			Strict:                     jsonx.Null[bool](),
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsGrammar,
		},
	})
	if err != nil {
		return nil, err
	}

	instructions := ""
	if initSys := ai.GetInitialSystemMessage(c.Messages); initSys != nil {
		instructions = ai.GetSystemMessageText(initSys)
	}
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}

	verbosity := "low"
	if options != nil && options.TextVerbosity != nil && *options.TextVerbosity != "" {
		verbosity = *options.TextVerbosity
	}
	textObj := jsonx.NewObject()
	textObj.Set("verbosity", verbosity)

	var toolChoice any = "auto"
	if options != nil && options.ToolChoice != nil {
		toolChoice = options.ToolChoice
	}

	body := jsonx.NewObject()
	body.Set("model", model.Id)
	body.Set("store", false)
	body.Set("stream", true)
	body.Set("instructions", instructions)
	body.Set("input", messages)
	body.Set("text", textObj)
	body.Set("include", []string{"reasoning.encrypted_content"})
	if cacheSessionId != "" {
		body.Set("prompt_cache_key", cacheSessionId)
	}
	body.Set("tool_choice", toolChoice)
	body.Set("parallel_tool_calls", true)

	if options != nil && options.Temperature != nil {
		body.Set("temperature", *options.Temperature)
	}
	if options != nil && options.ServiceTier != nil {
		body.Set("service_tier", *options.ServiceTier)
	}

	if len(transcriptTools.RequestTools) > 0 {
		convertedTools, err := ConvertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			Strict:                     jsonx.Null[bool](),
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsGrammar,
		})
		if err != nil {
			return nil, err
		}
		body.Set("tools", convertedTools)
	}

	if options != nil && options.ReasoningEffort != nil && *options.ReasoningEffort != "" {
		requested := string(*options.ReasoningEffort)
		var effortStr string
		var skip bool
		if requested == "none" {
			mapped, isNull, isMapped := ocrLookupCodexThinkingLevel(model, "none")
			if isMapped {
				if isNull {
					skip = true
				} else {
					effortStr = mapped
				}
			} else {
				effortStr = "none"
			}
		} else {
			mapped, isNull, isMapped := ocrLookupCodexThinkingLevel(model, requested)
			if isMapped && !isNull {
				effortStr = mapped
			} else {
				effortStr = requested
			}
		}
		if !skip {
			summary := "auto"
			if options.ReasoningSummary != nil && *options.ReasoningSummary != "" {
				summary = *options.ReasoningSummary
			}
			r := jsonx.NewObject()
			r.Set("effort", effortStr)
			r.Set("summary", summary)
			body.Set("reasoning", r)
		}
	} else if model.Reasoning {
		offMapped, isOffNull, isOffMapped := ocrLookupCodexThinkingLevel(model, "off")
		if !isOffNull {
			effortStr := "none"
			if isOffMapped {
				effortStr = offMapped
			}
			r := jsonx.NewObject()
			r.Set("effort", effortStr)
			body.Set("reasoning", r)
		}
	}

	return body, nil
}

func ocrGetServiceTierCostMultiplier(modelId string, serviceTier *string) float64 {
	if serviceTier == nil {
		return 1
	}
	switch *serviceTier {
	case "flex":
		return 0.5
	case "priority":
		if modelId == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

func ocrApplyServiceTierPricing(usage *ai.Usage, serviceTier *string, modelId string) {
	if usage == nil {
		return
	}
	multiplier := ocrGetServiceTierCostMultiplier(modelId, serviceTier)
	if multiplier == 1 {
		return
	}
	usage.Cost.Input *= multiplier
	usage.Cost.Output *= multiplier
	usage.Cost.CacheRead *= multiplier
	usage.Cost.CacheWrite *= multiplier
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

func ocrResolveCodexServiceTier(responseServiceTier *string, requestServiceTier *string) *string {
	if responseServiceTier != nil && *responseServiceTier == "default" && requestServiceTier != nil && (*requestServiceTier == "flex" || *requestServiceTier == "priority") {
		return requestServiceTier
	}
	if responseServiceTier != nil {
		return responseServiceTier
	}
	return requestServiceTier
}

func ocrIsTerminalRateLimitError(errorText string) bool {
	return ocrTerminalRateLimitRe.MatchString(errorText)
}

func ocrIsRetryableError(status int, errorText string) bool {
	if status == 429 && ocrIsTerminalRateLimitError(errorText) {
		return false
	}
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}
	return ocrRetryableErrorRe.MatchString(errorText)
}

func ocrGetRetryAfterDelayMs(headers http.Header) *int64 {
	if retryAfterMs := headers.Get("retry-after-ms"); retryAfterMs != "" {
		if millis, err := strconv.ParseFloat(js.Trim(retryAfterMs), 64); err == nil && !math.IsNaN(millis) && !math.IsInf(millis, 0) {
			d := int64(math.Max(0, millis))
			return &d
		}
	}
	if retryAfter := headers.Get("retry-after"); retryAfter != "" {
		if seconds, err := strconv.ParseFloat(js.Trim(retryAfter), 64); err == nil && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
			d := int64(math.Max(0, seconds*1000))
			return &d
		}
		if parsedTime, err := http.ParseTime(js.Trim(retryAfter)); err == nil {
			d := max(int64(0), parsedTime.UnixMilli()-js.DateNow())
			return &d
		}
	}
	return nil
}

func ocrValidateRetryDelayMs(delayMs int64, options *OpenAICodexResponsesOptions) (int64, error) {
	maxRetryDelayMs := ocrDefaultMaxRetryDelayMs
	if options != nil && options.MaxRetryDelayMs != nil {
		maxRetryDelayMs = *options.MaxRetryDelayMs
	}
	if maxRetryDelayMs > 0 && delayMs > maxRetryDelayMs {
		return 0, &ocrRetryDelayExceededError{
			message: fmt.Sprintf("Server requested %ds retry delay (max: %ds)", int(math.Ceil(float64(delayMs)/1000)), int(math.Ceil(float64(maxRetryDelayMs)/1000))),
		}
	}
	return delayMs, nil
}

func ocrSleepWithCtx(ctx context.Context, ms int64) error {
	if ctx != nil && ctx.Err() != nil {
		return errors.New("Request was aborted")
	}
	if ms <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-ctx.Done():
		return errors.New("Request was aborted")
	case <-timer.C:
		return nil
	}
}

func ocrParseErrorResponse(statusCode int, statusText string, raw string) string {
	message := raw
	if message == "" {
		message = statusText
	}
	if message == "" {
		message = "Request failed"
	}
	var friendlyMessage string
	if parsed, err := jsonx.ParseString(raw); err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			if errObj, ok := obj.GetObject("error"); ok && errObj != nil {
				code, _ := errObj.GetString("code")
				if code == "" {
					code, _ = errObj.GetString("type")
				}
				if ocrUsageLimitCodeRe.MatchString(code) || statusCode == 429 {
					plan := ""
					if planType, ok := errObj.GetString("plan_type"); ok && planType != "" {
						plan = fmt.Sprintf(" (%s plan)", strings.ToLower(planType))
					}
					when := ""
					if resetsAt, ok := errObj.GetNumber("resets_at"); ok && resetsAt != 0 {
						mins := max(0, int(math.Round((resetsAt*1000-float64(js.DateNow()))/60000)))
						when = fmt.Sprintf(" Try again in ~%d min.", mins)
					}
					friendlyMessage = js.Trim(fmt.Sprintf("You have hit your ChatGPT usage limit%s.%s", plan, when))
				}
				if errMsg, ok := errObj.GetString("message"); ok && errMsg != "" {
					message = errMsg
				} else if friendlyMessage != "" {
					message = friendlyMessage
				}
			}
		}
	}
	if friendlyMessage != "" {
		return friendlyMessage
	}
	return message
}

func ocrExtractCodexEventError(event *jsonx.Object) (code string, message string) {
	if event == nil {
		return "", ""
	}
	nested, _ := event.GetObject("error")
	if c, ok := event.GetString("code"); ok && c != "" {
		code = c
	} else if nested != nil {
		code, _ = nested.GetString("code")
	}
	if m, ok := event.GetString("message"); ok && m != "" {
		message = m
	} else if nested != nil {
		message, _ = nested.GetString("message")
	}
	return code, message
}

func ocrIsCodexNonTransportError(err error) bool {
	var apiErr *ocrCodexApiError
	var protoErr *ocrCodexProtocolError
	var cbErr *ocrProviderStreamEventCallbackError
	return errors.As(err, &apiErr) || errors.As(err, &protoErr) || errors.As(err, &cbErr)
}

func ocrIsWebSocketConnectionLimitReachedError(err error) bool {
	var apiErr *ocrCodexApiError
	return errors.As(err, &apiErr) && apiErr.code == ocrWebsocketConnLimitReachedCode
}

func ocrIsPreviousResponseNotFoundError(err error) bool {
	var apiErr *ocrCodexApiError
	return errors.As(err, &apiErr) && apiErr.code == ocrPreviousResponseNotFoundCode
}

func ocrMapCodexEvents(
	events iter.Seq2[*jsonx.Object, error],
	output *ai.AssistantMessage,
	model *ai.Model,
	onProviderStreamEvent func(any, *ai.Model) error,
) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		for event, err := range events {
			if err != nil {
				yield(nil, err)
				return
			}
			if onProviderStreamEvent != nil {
				if cbErr := onProviderStreamEvent(event, model); cbErr != nil {
					yield(nil, &ocrProviderStreamEventCallbackError{
						message: ai.FormatThrownValue(cbErr),
						cause:   cbErr,
					})
					return
				}
			}
			if event == nil {
				continue
			}
			typ, _ := event.GetString("type")
			if typ == "" {
				continue
			}

			switch typ {
			case "error":
				code, message := ocrExtractCodexEventError(event)
				detail := message
				if detail == "" {
					detail = code
				}
				if detail == "" {
					detail, _ = jsonx.Stringify(event)
				}
				yield(nil, &ocrCodexApiError{
					message: "Codex error: " + detail,
					code:    code,
					payload: event,
				})
				return

			case "response.failed":
				var code, message string
				if respObj, ok := event.GetObject("response"); ok && respObj != nil {
					if errObj, ok := respObj.GetObject("error"); ok && errObj != nil {
						code, _ = errObj.GetString("code")
						message, _ = errObj.GetString("message")
					}
				}
				if message == "" {
					message = "Codex response failed"
				}
				yield(nil, &ocrCodexApiError{
					message: message,
					code:    code,
					payload: event,
				})
				return

			case "response.done", "response.completed", "response.incomplete":
				cloned := event.Clone()
				cloned.Set("type", "response.completed")
				if respObj, ok := cloned.GetObject("response"); ok && respObj != nil {
					if endTurn, ok := respObj.GetBool("end_turn"); ok {
						output.EndTurn = &endTurn
					}
					respClone := respObj.Clone()
					if st, ok := respClone.GetString("status"); ok {
						if _, valid := ocrCodexResponseStatuses[st]; !valid {
							respClone.Delete("status")
						}
					} else {
						respClone.Delete("status")
					}
					cloned.Set("response", respClone)
				}
				yield(cloned, nil)
				return

			default:
				if !yield(event, nil) {
					return
				}
			}
		}
	}
}

func ocrStartWebSocketOutputOnFirstEvent(
	events iter.Seq2[*jsonx.Object, error],
	onStart func(),
) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		started := false
		for event, err := range events {
			if err != nil {
				yield(nil, err)
				return
			}
			if !started {
				started = true
				onStart()
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

func ocrIterCodexSSE(ctx context.Context, body io.Reader) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		reader := sse.NewReader(body)
		for {
			if ctx != nil && ctx.Err() != nil {
				yield(nil, errors.New("Request was aborted"))
				return
			}
			ev, err := reader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if ctx != nil && ctx.Err() != nil {
					yield(nil, errors.New("Request was aborted"))
					return
				}
				yield(nil, err)
				return
			}
			data := js.Trim(ev.Data)
			if data == "" || data == "[DONE]" {
				continue
			}
			parsed, parseErr := jsonx.ParseString(data)
			if parseErr != nil {
				yield(nil, &ocrCodexProtocolError{
					message: "Invalid Codex SSE JSON: " + ai.FormatThrownValue(parseErr),
					cause:   parseErr,
					payload: data,
				})
				return
			}
			obj, ok := parsed.(*jsonx.Object)
			if !ok || obj == nil {
				yield(nil, &ocrCodexProtocolError{
					message: "Invalid Codex SSE JSON: expected JSON object",
					payload: data,
				})
				return
			}
			if !yield(obj, nil) {
				return
			}
		}
	}
}

func ocrFormatWebSocketCloseError(closeErr websocket.CloseError) error {
	code := int(closeErr.Code)
	reason := closeErr.Reason
	if reason == "" && code == ocrWebsocketMessageTooBigClose {
		reason = "message too big"
	}
	msg := "WebSocket closed"
	if code > 0 {
		msg += " " + strconv.Itoa(code)
	}
	if reason != "" {
		msg += " " + reason
	}
	return &ocrWebSocketCloseError{
		message:  js.Trim(msg),
		code:     code,
		reason:   closeErr.Reason,
		wasClean: true,
	}
}

func ocrConnectWebSocket(
	sig context.Context,
	wsURL string,
	headers http.Header,
	connectTimeoutMs int64,
	client *http.Client,
	env ai.ProviderEnv,
) (*websocket.Conn, error) {
	if sig != nil && sig.Err() != nil {
		return nil, errors.New("Request was aborted")
	}
	if env != nil {
		if u, err := url.Parse(wsURL); err == nil {
			if u.Scheme == "wss" {
				u.Scheme = "https"
			} else if u.Scheme == "ws" {
				u.Scheme = "http"
			}
			if _, proxyErr := ai.ResolveHttpProxyUrlForTarget(u.String(), env); proxyErr != nil {
				return nil, proxyErr
			}
		}
	}

	dialCtx := sig
	if dialCtx == nil {
		dialCtx = context.Background()
	}
	var cancelDial context.CancelFunc
	if connectTimeoutMs > 0 {
		dialCtx, cancelDial = context.WithTimeout(dialCtx, time.Duration(connectTimeoutMs)*time.Millisecond)
		defer cancelDial()
	}

	dialHeaders := headers.Clone()
	conn, resp, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: dialHeaders,
	})
	if err != nil {
		if sig != nil && sig.Err() != nil {
			return nil, errors.New("Request was aborted")
		}
		if connectTimeoutMs > 0 && errors.Is(dialCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("WebSocket connect timeout after %dms", connectTimeoutMs)
		}
		var closeErr websocket.CloseError
		if errors.As(err, &closeErr) {
			return nil, ocrFormatWebSocketCloseError(closeErr)
		}
		if resp != nil && resp.StatusCode >= 400 {
			return nil, fmt.Errorf("WebSocket handshake failed: HTTP %d", resp.StatusCode)
		}
		return nil, err
	}
	conn.SetReadLimit(-1)
	return conn, nil
}

type ocrAcquiredWebSocket struct {
	conn    *websocket.Conn
	entry   *ocrCachedWebSocketConnection
	reused  bool
	release func(keep bool)
}

func ocrAcquireWebSocket(
	sig context.Context,
	wsURL string,
	headers http.Header,
	sessionId string,
	accountId string,
	connectTimeoutMs int64,
	client *http.Client,
	env ai.ProviderEnv,
) (*ocrAcquiredWebSocket, error) {
	if sessionId == "" {
		conn, err := ocrConnectWebSocket(sig, wsURL, headers, connectTimeoutMs, client, env)
		if err != nil {
			return nil, err
		}
		return &ocrAcquiredWebSocket{
			conn:   conn,
			reused: false,
			release: func(bool) {
				ocrCloseWebSocketSilently(conn, websocket.StatusNormalClosure, "done")
			},
		}, nil
	}

	var staleConn *websocket.Conn
	ocrWSMu.Lock()
	accountEntries := ocrWebsocketSessionCache[sessionId]
	cached := accountEntries[accountId]
	if cached != nil {
		if cached.idleTimer != nil {
			cached.idleTimer.Stop()
			cached.idleTimer = nil
		}
		expired := !cached.busy && (js.DateNow()-cached.createdAt >= ocrSessionWebsocketMaxAgeMs)
		if !cached.busy && !cached.closed && !expired {
			cached.busy = true
			ocrWSMu.Unlock()
			return &ocrAcquiredWebSocket{
				conn:   cached.conn,
				entry:  cached,
				reused: true,
				release: func(keep bool) {
					var closeConn *websocket.Conn
					ocrWSMu.Lock()
					if !keep || cached.closed {
						cached.closed = true
						cached.busy = false
						if ae := ocrWebsocketSessionCache[sessionId]; ae != nil && ae[accountId] == cached {
							delete(ae, accountId)
							if len(ae) == 0 {
								delete(ocrWebsocketSessionCache, sessionId)
							}
						}
						closeConn = cached.conn
					} else {
						cached.busy = false
						ocrScheduleSessionWebSocketExpiryLocked(sessionId, accountId, cached)
					}
					ocrWSMu.Unlock()
					if closeConn != nil {
						ocrCloseWebSocketSilently(closeConn, websocket.StatusNormalClosure, "done")
					}
				},
			}, nil
		}
		if cached.busy {
			ocrWSMu.Unlock()
			conn, err := ocrConnectWebSocket(sig, wsURL, headers, connectTimeoutMs, client, env)
			if err != nil {
				return nil, err
			}
			return &ocrAcquiredWebSocket{
				conn:   conn,
				reused: false,
				release: func(bool) {
					ocrCloseWebSocketSilently(conn, websocket.StatusNormalClosure, "done")
				},
			}, nil
		}
		cached.closed = true
		staleConn = cached.conn
		delete(accountEntries, accountId)
		if len(accountEntries) == 0 {
			delete(ocrWebsocketSessionCache, sessionId)
		}
	}
	ocrWSMu.Unlock()

	if staleConn != nil {
		reason := "done"
		ocrCloseWebSocketSilently(staleConn, websocket.StatusNormalClosure, reason)
	}

	conn, err := ocrConnectWebSocket(sig, wsURL, headers, connectTimeoutMs, client, env)
	if err != nil {
		return nil, err
	}

	entry := &ocrCachedWebSocketConnection{
		conn:      conn,
		busy:      true,
		createdAt: js.DateNow(),
	}
	ocrWSMu.Lock()
	accountEntries = ocrWebsocketSessionCache[sessionId]
	if accountEntries == nil {
		accountEntries = make(map[string]*ocrCachedWebSocketConnection)
		ocrWebsocketSessionCache[sessionId] = accountEntries
	}
	accountEntries[accountId] = entry
	ocrWSMu.Unlock()

	return &ocrAcquiredWebSocket{
		conn:   conn,
		entry:  entry,
		reused: false,
		release: func(keep bool) {
			var closeConn *websocket.Conn
			ocrWSMu.Lock()
			if !keep || entry.closed {
				entry.closed = true
				entry.busy = false
				if entry.idleTimer != nil {
					entry.idleTimer.Stop()
					entry.idleTimer = nil
				}
				if ae := ocrWebsocketSessionCache[sessionId]; ae != nil && ae[accountId] == entry {
					delete(ae, accountId)
					if len(ae) == 0 {
						delete(ocrWebsocketSessionCache, sessionId)
					}
				}
				closeConn = entry.conn
			} else {
				entry.busy = false
				ocrScheduleSessionWebSocketExpiryLocked(sessionId, accountId, entry)
			}
			ocrWSMu.Unlock()
			if closeConn != nil {
				ocrCloseWebSocketSilently(closeConn, websocket.StatusNormalClosure, "done")
			}
		},
	}, nil
}

func ocrParseWebSocket(sig context.Context, conn *websocket.Conn, idleTimeoutMs *int64) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		readCtx := context.Background()
		if sig != nil {
			readCtx = sig
		}
		sawCompletion := false
		for {
			if sig != nil && sig.Err() != nil {
				yield(nil, errors.New("Request was aborted"))
				return
			}
			var timedOut atomic.Bool
			var idleTimer *time.Timer
			if idleTimeoutMs != nil && *idleTimeoutMs > 0 {
				limit := *idleTimeoutMs
				idleTimer = time.AfterFunc(time.Duration(limit)*time.Millisecond, func() {
					timedOut.Store(true)
					ocrCloseWebSocketSilently(conn, websocket.StatusNormalClosure, "idle_timeout")
				})
			}
			_, payload, err := conn.Read(readCtx)
			if idleTimer != nil {
				idleTimer.Stop()
			}
			if err != nil {
				if sawCompletion {
					return
				}
				if sig != nil && sig.Err() != nil {
					yield(nil, errors.New("Request was aborted"))
					return
				}
				if timedOut.Load() && idleTimeoutMs != nil {
					yield(nil, fmt.Errorf("WebSocket idle timeout after %dms", *idleTimeoutMs))
					return
				}
				var closeErr websocket.CloseError
				if errors.As(err, &closeErr) {
					yield(nil, ocrFormatWebSocketCloseError(closeErr))
					return
				}
				if errors.Is(err, io.EOF) {
					yield(nil, errors.New("WebSocket stream closed before response.completed"))
					return
				}
				yield(nil, err)
				return
			}

			text := string(payload)
			if text == "" {
				continue
			}
			parsed, parseErr := jsonx.ParseString(text)
			if parseErr != nil {
				yield(nil, &ocrCodexProtocolError{
					message: "Invalid Codex WebSocket JSON: " + ai.FormatThrownValue(parseErr),
					cause:   parseErr,
					payload: text,
				})
				return
			}
			obj, ok := parsed.(*jsonx.Object)
			if !ok || obj == nil {
				yield(nil, &ocrCodexProtocolError{
					message: "Invalid Codex WebSocket JSON: expected JSON object",
					payload: text,
				})
				return
			}
			typ, _ := obj.GetString("type")
			if typ == "response.completed" || typ == "response.done" || typ == "response.incomplete" {
				sawCompletion = true
			}
			if !yield(obj, nil) {
				return
			}
			if sawCompletion {
				return
			}
		}
	}
}

func ocrExtractAnySlice(obj *jsonx.Object, key string) []any {
	if obj == nil {
		return nil
	}
	v, has := obj.Get(key)
	if !has || v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func ocrRequestBodyWithoutInput(body *jsonx.Object) *jsonx.Object {
	if body == nil {
		return jsonx.NewObject()
	}
	clone := body.Clone()
	clone.Delete("input")
	clone.Delete("previous_response_id")
	return clone
}

func ocrGetCachedWebSocketInputDelta(body *jsonx.Object, cont *ocrCachedWebSocketContinuationState) ([]any, bool) {
	if cont == nil || body == nil {
		return nil, false
	}
	currNoIn, err1 := jsonx.Stringify(ocrRequestBodyWithoutInput(body))
	prevNoIn, err2 := jsonx.Stringify(ocrRequestBodyWithoutInput(cont.lastRequestBody))
	if err1 != nil || err2 != nil || currNoIn != prevNoIn {
		return nil, false
	}
	currentInput := ocrExtractAnySlice(body, "input")
	prevInput := ocrExtractAnySlice(cont.lastRequestBody, "input")
	baseline := make([]any, 0, len(prevInput)+len(cont.lastResponseItems))
	baseline = append(baseline, prevInput...)
	baseline = append(baseline, cont.lastResponseItems...)
	if len(currentInput) < len(baseline) {
		return nil, false
	}
	prefixJSON, err3 := jsonx.Stringify(currentInput[:len(baseline)])
	baselineJSON, err4 := jsonx.Stringify(baseline)
	if err3 != nil || err4 != nil || prefixJSON != baselineJSON {
		return nil, false
	}
	return currentInput[len(baseline):], true
}

func ocrBuildCachedWebSocketRequestBody(entry *ocrCachedWebSocketConnection, body *jsonx.Object) *jsonx.Object {
	if entry == nil {
		return body
	}
	ocrWSMu.Lock()
	cont := entry.continuation
	ocrWSMu.Unlock()
	if cont == nil || cont.lastResponseId == "" {
		return body
	}
	delta, ok := ocrGetCachedWebSocketInputDelta(body, cont)
	if !ok {
		ocrWSMu.Lock()
		entry.continuation = nil
		ocrWSMu.Unlock()
		return body
	}
	out := body.Clone()
	out.Set("previous_response_id", cont.lastResponseId)
	out.Set("input", delta)
	return out
}

func ocrProcessWebSocketStream(
	sig context.Context,
	wsURL string,
	body *jsonx.Object,
	wsHeaders http.Header,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
	model *ai.Model,
	onStart func(),
	idleTimeoutMs *int64,
	websocketConnectTimeoutMs *int64,
	cacheSessionId string,
	accountId string,
	grammarToolInputProperties map[string]string,
	options *OpenAICodexResponsesOptions,
	client *http.Client,
) error {
	connectTimeout := ocrDefaultWebsocketConnectMs
	if websocketConnectTimeoutMs != nil {
		connectTimeout = *websocketConnectTimeoutMs
	}
	var env ai.ProviderEnv
	if options != nil {
		env = options.Env
	}
	acq, err := ocrAcquireWebSocket(sig, wsURL, wsHeaders, cacheSessionId, accountId, connectTimeout, client, env)
	if err != nil {
		return err
	}

	keepConnection := true
	defer func() {
		acq.release(keepConnection)
	}()

	useCachedContext := options != nil && options.Transport != nil && (*options.Transport == ai.TransportWebsocketCached || *options.Transport == ai.TransportAuto)
	requestBody := body
	if useCachedContext && acq.entry != nil {
		requestBody = ocrBuildCachedWebSocketRequestBody(acq.entry, body)
	}

	if cacheSessionId != "" {
		ocrWSMu.Lock()
		stats := ocrGetOrCreateDebugStatsLocked(cacheSessionId)
		stats.Requests++
		if acq.reused {
			stats.ConnectionsReused++
		} else {
			stats.ConnectionsCreated++
		}
		if useCachedContext {
			stats.CachedContextRequests++
		}
		if st, ok := requestBody.GetBool("store"); ok && st {
			stats.StoreTrueRequests++
		}
		inputItems := len(ocrExtractAnySlice(requestBody, "input"))
		stats.LastInputItems = inputItems
		if prevID, ok := requestBody.GetString("previous_response_id"); ok && prevID != "" {
			stats.DeltaRequests++
			dCount := inputItems
			stats.LastDeltaInputItems = &dCount
			stats.LastPreviousResponseId = &prevID
		} else {
			stats.FullContextRequests++
			stats.LastDeltaInputItems = nil
			stats.LastPreviousResponseId = nil
		}
		ocrWSMu.Unlock()
	}

	cleanupOnFailure := func() {
		if acq.entry != nil {
			ocrWSMu.Lock()
			acq.entry.continuation = nil
			ocrWSMu.Unlock()
		}
		keepConnection = false
	}

	frame := jsonx.NewObject()
	frame.Set("type", "response.create")
	frame.Assign(requestBody)
	frameBytes, err := jsonx.Marshal(frame)
	if err != nil {
		cleanupOnFailure()
		return err
	}

	writeCtx := context.Background()
	if sig != nil {
		writeCtx = sig
	}
	if err := acq.conn.Write(writeCtx, websocket.MessageText, frameBytes); err != nil {
		cleanupOnFailure()
		if sig != nil && sig.Err() != nil {
			return errors.New("Request was aborted")
		}
		var closeErr websocket.CloseError
		if errors.As(err, &closeErr) {
			return ocrFormatWebSocketCloseError(closeErr)
		}
		return err
	}

	var onProviderStreamEvent func(any, *ai.Model) error
	var serviceTier *string
	if options != nil {
		onProviderStreamEvent = options.OnProviderStreamEvent
		serviceTier = options.ServiceTier
	}

	events := ocrStartWebSocketOutputOnFirstEvent(
		ocrMapCodexEvents(
			ocrParseWebSocket(sig, acq.conn, idleTimeoutMs),
			output,
			model,
			onProviderStreamEvent,
		),
		onStart,
	)
	err = ProcessResponsesStream(events, output, stream, model, &OpenAIResponsesStreamOptions{
		ServiceTier:                serviceTier,
		GrammarToolInputProperties: grammarToolInputProperties,
		ResolveServiceTier:         ocrResolveCodexServiceTier,
		ApplyServiceTierPricing: func(usage *ai.Usage, st *string) {
			ocrApplyServiceTierPricing(usage, st, model.Id)
		},
	})
	if err != nil {
		cleanupOnFailure()
		return err
	}

	if sig != nil && sig.Err() != nil {
		keepConnection = false
	} else if useCachedContext && acq.entry != nil && output.ResponseId != nil && *output.ResponseId != "" {
		includeFalse := false
		respCtx := &ai.TranscriptContext{Messages: []ai.Message{output}}
		converted, convErr := ConvertResponsesMessages(model, respCtx, ocrCodexToolCallProviders, &ConvertResponsesMessagesOptions{
			IncludeSystemPrompt:        &includeFalse,
			GrammarToolInputProperties: grammarToolInputProperties,
		})
		if convErr == nil {
			filtered := make([]any, 0, len(converted))
			for _, item := range converted {
				if obj, ok := item.(*jsonx.Object); ok && obj != nil {
					if typ, _ := obj.GetString("type"); typ == "function_call_output" || typ == "custom_tool_call_output" {
						continue
					}
				}
				filtered = append(filtered, item)
			}
			ocrWSMu.Lock()
			acq.entry.continuation = &ocrCachedWebSocketContinuationState{
				lastRequestBody:   body.Clone(),
				lastResponseId:    *output.ResponseId,
				lastResponseItems: filtered,
			}
			ocrWSMu.Unlock()
		}
	}
	return nil
}

func ocrAssertSuccessfulOutput(output *ai.AssistantMessage) error {
	if output.StopReason == ai.StopReasonPending {
		return errors.New("Codex stream ended without a stop reason")
	}
	if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
		if output.ErrorMessage != nil && *output.ErrorMessage != "" {
			return errors.New(*output.ErrorMessage)
		}
		return errors.New("An unknown error occurred")
	}
	return nil
}

// StreamOpenaiCodexResponses streams responses from OpenAI Codex (ChatGPT backend) via WebSocket or SSE.
func StreamOpenaiCodexResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := ocrExtractOptions(options)
	_, _, _, _, supportsMidConvoSys := ocrGetCompatFlags(model)
	normalizedContext := ai.ResolveTranscript(c, &supportsMidConvoSys)
	sig := ai.OperationSignal(ctx)

	go func() {
		var modelProvider ai.ProviderId
		var modelID string
		if model != nil {
			modelProvider = model.Provider
			modelID = model.Id
		}
		output := &ai.AssistantMessage{
			Role:       "assistant",
			Content:    ai.ContentList{},
			Api:        ai.KnownApiOpenaiCodexResponses,
			Provider:   modelProvider,
			Model:      modelID,
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
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End(output)
		}

		if model == nil {
			fail(errors.New("Missing model for OpenAI Codex Responses"))
			return
		}
		if opts.ApiKey == nil || *opts.ApiKey == "" {
			fail(fmt.Errorf("No API key for provider: %s", model.Provider))
			return
		}
		apiKey := *opts.ApiKey

		accountId, err := ocrExtractAccountId(apiKey)
		if err != nil {
			fail(err)
			return
		}

		_, supportsGrammar, _, _, _ := ocrGetCompatFlags(model)
		grammarToolInputProperties, err := CreateGrammarToolInputProperties(
			ai.GetDeclaredTools(normalizedContext.Messages),
			supportsGrammar,
		)
		if err != nil {
			fail(err)
			return
		}

		var cacheSessionID string
		cacheRetention := ai.CacheRetentionShort
		if opts.CacheRetention != nil && *opts.CacheRetention != "" {
			cacheRetention = *opts.CacheRetention
		}
		if cacheRetention != ai.CacheRetentionNone && opts.SessionId != nil {
			cacheSessionID = *opts.SessionId
		}
		var codexSessionID string
		if clamped := ClampOpenAIPromptCacheKey(&cacheSessionID); clamped != nil {
			codexSessionID = *clamped
		}

		body, err := ocrBuildRequestBody(model, normalizedContext, opts, codexSessionID, grammarToolInputProperties)
		if err != nil {
			fail(err)
			return
		}
		if opts.OnPayload != nil {
			nextBody, err := opts.OnPayload(body, model)
			if err != nil {
				fail(err)
				return
			}
			if nextBody != nil {
				if nextObj, ok := nextBody.(*jsonx.Object); ok {
					body = nextObj
				}
			}
		}

		websocketRequestID := codexSessionID
		if websocketRequestID == "" {
			websocketRequestID = ai.Uuidv7()
		}

		identityHeaders := map[string]string{"originator": "pi"}
		for k, v := range ai.GetIdentityHeaders(ai.ClientIdentityTarget{Provider: string(model.Provider), BaseUrl: model.BaseUrl}, opts.Identity).All() {
			identityHeaders[k] = v
		}
		sseHeaders := ocrBuildSSEHeaders(identityHeaders, model.Headers, opts.Headers, accountId, apiKey, codexSessionID)
		wsHeaders := ocrBuildWebSocketHeaders(identityHeaders, model.Headers, opts.Headers, accountId, apiKey, websocketRequestID)

		bodyBytes, err := jsonx.Marshal(body)
		if err != nil {
			fail(err)
			return
		}

		httpTimeoutMs, err := ocrNormalizeTimeoutMs(opts.TimeoutMs)
		if err != nil {
			fail(err)
			return
		}
		websocketConnectTimeoutMs, err := ocrNormalizeTimeoutMs(opts.WebsocketConnectTimeoutMs)
		if err != nil {
			fail(err)
			return
		}

		transport := ai.TransportAuto
		if opts.Transport != nil && *opts.Transport != "" {
			transport = *opts.Transport
		}
		httpClient := ocrHTTPClient(opts.HTTPClient, opts.Env)

		startEmitted := false
		websocketDisabledForSession := transport != ai.TransportSse && ocrIsWebSocketSseFallbackActive(cacheSessionID)
		if websocketDisabledForSession {
			ocrRecordWebSocketSseFallback(cacheSessionID)
		}

		if transport != ai.TransportSse && !websocketDisabledForSession {
			wsURL, err := ocrResolveCodexWebSocketUrl(model.BaseUrl)
			if err != nil {
				fail(err)
				return
			}
			retriedConnLimit := false
			retriedMissingCont := false
			for {
				websocketStarted := false
				err := ocrProcessWebSocketStream(
					sig,
					wsURL,
					body,
					wsHeaders,
					output,
					stream,
					model,
					func() {
						websocketStarted = true
						if !startEmitted {
							startEmitted = true
							stream.Push(&ai.AssistantMessageEventStart{
								Type:    "start",
								Partial: orsClonePartial(output),
							})
						}
					},
					httpTimeoutMs,
					websocketConnectTimeoutMs,
					cacheSessionID,
					accountId,
					grammarToolInputProperties,
					opts,
					httpClient,
				)
				if err == nil {
					if sig.Err() != nil {
						fail(errors.New("Request was aborted"))
						return
					}
					if err := ocrAssertSuccessfulOutput(output); err != nil {
						fail(err)
						return
					}
					stream.Push(&ai.AssistantMessageEventDone{
						Type:    "done",
						Reason:  output.StopReason,
						Message: output,
					})
					stream.End(output)
					return
				}

				aborted := sig.Err() != nil
				connLimitBeforeStart := !websocketStarted && ocrIsWebSocketConnectionLimitReachedError(err)
				prevNotFound := ocrIsPreviousResponseNotFoundError(err)
				if !aborted && prevNotFound && !retriedMissingCont {
					retriedMissingCont = true
					continue
				}
				if !aborted && connLimitBeforeStart && !retriedConnLimit {
					retriedConnLimit = true
					continue
				}
				if aborted || (ocrIsCodexNonTransportError(err) && !connLimitBeforeStart) {
					fail(err)
					return
				}

				details := jsonx.NewObject()
				details.Set("configuredTransport", string(transport))
				details.Set("eventsEmitted", websocketStarted)
				details.Set("requestBytes", len(bodyBytes))
				if websocketStarted {
					details.Set("phase", "after_message_stream_start")
				} else {
					details.Set("phase", "before_message_stream_start")
					details.Set("fallbackTransport", "sse")
				}
				ai.AppendAssistantMessageDiagnostic(output, ai.CreateAssistantMessageDiagnostic("provider_transport_failure", err, details))
				ocrRecordWebSocketFailure(cacheSessionID, err)
				if websocketStarted {
					fail(err)
					return
				}
				ocrRecordWebSocketSseFallback(cacheSessionID)
				break
			}
		}

		sseURL := ocrResolveCodexUrl(model.BaseUrl)
		ssePayload := bodyBytes
		if compressed := ocrCompressRequestBodyZstd(bodyBytes); compressed != nil {
			sseHeaders.Set("Content-Encoding", "zstd")
			ssePayload = compressed
		}

		maxRetries := ocrDefaultMaxRetries
		if opts.MaxRetries != nil {
			maxRetries = *opts.MaxRetries
		}

		var resp *http.Response
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if sig.Err() != nil {
				fail(errors.New("Request was aborted"))
				return
			}

			reqCtx, cancelReq := context.WithCancel(sig)
			var headerTimedOut atomic.Bool
			var headerTimer *time.Timer
			if httpTimeoutMs != nil && *httpTimeoutMs > 0 {
				limit := *httpTimeoutMs
				headerTimer = time.AfterFunc(time.Duration(limit)*time.Millisecond, func() {
					headerTimedOut.Store(true)
					cancelReq()
				})
			}

			req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, sseURL, bytes.NewReader(ssePayload))
			var attemptErr error
			if reqErr != nil {
				if headerTimer != nil {
					headerTimer.Stop()
				}
				cancelReq()
				attemptErr = reqErr
			} else {
				req.Header = sseHeaders.Clone()
				r, doErr := httpClient.Do(req)
				if headerTimer != nil {
					headerTimer.Stop()
				}
				if doErr != nil {
					cancelReq()
					if sig.Err() != nil {
						fail(errors.New("Request was aborted"))
						return
					}
					if headerTimedOut.Load() && httpTimeoutMs != nil {
						attemptErr = fmt.Errorf("Codex SSE response headers timed out after %dms", *httpTimeoutMs)
					} else {
						attemptErr = doErr
					}
				} else {
					var cbErr error
					if opts.OnResponse != nil {
						cbErr = opts.OnResponse(&ai.ProviderResponse{
							Status:  r.StatusCode,
							Headers: ai.HeadersToRecord(r.Header),
						}, model)
					}
					if cbErr != nil {
						_ = r.Body.Close()
						cancelReq()
						attemptErr = cbErr
					} else if r.StatusCode >= 200 && r.StatusCode < 300 {
						r.Body = &orsCancelReadCloser{ReadCloser: r.Body, cancel: cancelReq}
						resp = r
						break
					} else {
						rawBody, _ := io.ReadAll(r.Body)
						_ = r.Body.Close()
						cancelReq()
						errText := string(rawBody)
						if attempt < maxRetries && ocrIsRetryableError(r.StatusCode, errText) {
							var delayMs int64
							if retryAfter := ocrGetRetryAfterDelayMs(r.Header); retryAfter != nil {
								validated, valErr := ocrValidateRetryDelayMs(*retryAfter, opts)
								if valErr != nil {
									fail(valErr)
									return
								}
								delayMs = validated
							} else {
								delayMs = int64(ocrBaseDelayMs) * (1 << attempt)
							}
							if sleepErr := ocrSleepWithCtx(sig, delayMs); sleepErr != nil {
								fail(sleepErr)
								return
							}
							continue
						}
						attemptErr = errors.New(ocrParseErrorResponse(r.StatusCode, r.Status, errText))
					}
				}
			}

			if sig.Err() != nil {
				fail(errors.New("Request was aborted"))
				return
			}
			lastErr = attemptErr
			var retryExceeded *ocrRetryDelayExceededError
			if attempt < maxRetries && !errors.As(lastErr, &retryExceeded) && !strings.Contains(lastErr.Error(), "usage limit") {
				delayMs := int64(ocrBaseDelayMs) * (1 << attempt)
				if sleepErr := ocrSleepWithCtx(sig, delayMs); sleepErr != nil {
					fail(sleepErr)
					return
				}
				continue
			}
			fail(lastErr)
			return
		}

		if resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if lastErr != nil {
				fail(lastErr)
			} else {
				fail(errors.New("Failed after retries"))
			}
			return
		}
		defer resp.Body.Close()

		if !startEmitted {
			startEmitted = true
			stream.Push(&ai.AssistantMessageEventStart{
				Type:    "start",
				Partial: orsClonePartial(output),
			})
		}

		err = ProcessResponsesStream(
			ocrMapCodexEvents(ocrIterCodexSSE(sig, resp.Body), output, model, opts.OnProviderStreamEvent),
			output,
			stream,
			model,
			&OpenAIResponsesStreamOptions{
				ServiceTier:                opts.ServiceTier,
				GrammarToolInputProperties: grammarToolInputProperties,
				ResolveServiceTier:         ocrResolveCodexServiceTier,
				ApplyServiceTierPricing: func(usage *ai.Usage, serviceTier *string) {
					ocrApplyServiceTierPricing(usage, serviceTier, model.Id)
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
		if err := ocrAssertSuccessfulOutput(output); err != nil {
			fail(err)
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

// StreamSimpleOpenaiCodexResponses adapts SimpleStreamOptions and delegates to StreamOpenaiCodexResponses.
func StreamSimpleOpenaiCodexResponses(
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
	opts := &OpenAICodexResponsesOptions{StreamOptions: base}
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
	return StreamOpenaiCodexResponses(ctx, model, c, opts)
}
