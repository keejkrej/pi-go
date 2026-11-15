// Ported from packages/ai/src/api/classifier-shared.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ClassifierHttpError is an HTTP or timeout failure in the shape ai.RetryProviderRequest
// and ai.NormalizeProviderError understand.
type ClassifierHttpError struct {
	Message string
	ErrName string
	Status  *int
	Headers http.Header
	Body    string
	Cause   error
}

func (e *ClassifierHttpError) Error() string { return e.Message }

func (e *ClassifierHttpError) Name() string {
	if e.ErrName != "" {
		return e.ErrName
	}
	return "Error"
}

func (e *ClassifierHttpError) Unwrap() error { return e.Cause }

func (e *ClassifierHttpError) ProviderStatus() *int { return e.Status }

func (e *ClassifierHttpError) ProviderHeaders() http.Header { return e.Headers }

func (e *ClassifierHttpError) ErrorBody() string { return e.Body }

func clshNewHttpError(label string, response *http.Response, body string) *ClassifierHttpError {
	status := response.StatusCode
	return &ClassifierHttpError{
		Message: fmt.Sprintf("%s returned %d", label, status),
		Status:  &status,
		Headers: response.Header.Clone(),
		Body:    body,
	}
}

func clshNewTimeoutError(timeoutMs int64) *ClassifierHttpError {
	return &ClassifierHttpError{
		Message: fmt.Sprintf("Request timed out after %dms", timeoutMs),
		ErrName: "TimeoutError",
	}
}

// IsRecord reports whether value is a non-nil JSON object / record map.
func IsRecord(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case *jsonx.Object:
		return v != nil
	case jsonx.Object:
		return true
	case map[string]any:
		return v != nil
	case map[string]string:
		return v != nil
	default:
		rv := reflect.ValueOf(value)
		return rv.Kind() == reflect.Map && !rv.IsNil()
	}
}

func clshToFloat64(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	case float32:
		f := float64(n)
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

// RequiredNumber extracts a finite number from value or returns an error naming field.
func RequiredNumber(label string, value any, field string) (float64, error) {
	if f, ok := clshToFloat64(value); ok {
		return f, nil
	}
	return 0, fmt.Errorf("%s returned an invalid %s", label, field)
}

func clshRecordGet(value any, key string) (any, bool) {
	switch v := value.(type) {
	case *jsonx.Object:
		if v == nil {
			return nil, false
		}
		return v.Get(key)
	case jsonx.Object:
		return v.Get(key)
	case map[string]any:
		if v == nil {
			return nil, false
		}
		val, ok := v[key]
		return val, ok
	default:
		return nil, false
	}
}

func clshToProviderHeaders(m *omap.Map[string, string]) ai.ProviderHeaders {
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

func clshStaticProviderHeaders(kv ...string) ai.ProviderHeaders {
	out := omap.NewMap[string, *string]()
	for i := 0; i+1 < len(kv); i += 2 {
		val := kv[i+1]
		out.Set(kv[i], &val)
	}
	return out
}

func clshApplyMergedHeaders(req *http.Request, sources ...ai.ProviderHeaders) {
	merged := ai.ProviderHeadersToOrderedRecord(sources...)
	if merged == nil {
		return
	}
	for k, v := range merged.All() {
		req.Header.Set(k, v)
	}
}

func clshHasProxyEnv(env ai.ProviderEnv) bool {
	if len(env) == 0 {
		return false
	}
	for _, key := range []string{
		"http_proxy", "HTTP_PROXY",
		"https_proxy", "HTTPS_PROXY",
		"all_proxy", "ALL_PROXY",
		"no_proxy", "NO_PROXY",
	} {
		if env[key] != "" {
			return true
		}
	}
	return false
}

func clshResolveHTTPClient(client *http.Client, env ai.ProviderEnv) *http.Client {
	if client != nil {
		return client
	}
	if !clshHasProxyEnv(env) {
		return ai.NodeHttpProxyDefaultClient()
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.IdleConnTimeout = 90 * time.Second
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		return ai.ResolveHttpProxyUrlForTarget(req.URL.String(), env)
	}
	return &http.Client{Transport: tr}
}

func clshCombineAttemptContext(ctx context.Context, timeoutMs *int64) (context.Context, context.CancelFunc, context.Context) {
	base := ctx
	if base == nil {
		base = context.Background()
	}
	if timeoutMs == nil {
		return base, func() {}, nil
	}
	d := time.Duration(*timeoutMs) * time.Millisecond
	attemptCtx, cancel := context.WithTimeout(base, d)
	return attemptCtx, cancel, attemptCtx
}

func clshCheckTimeout(err error, ctx context.Context, timeoutCtx context.Context, timeoutMs *int64) error {
	if err == nil {
		return nil
	}
	if timeoutMs != nil && timeoutCtx != nil && timeoutCtx.Err() == context.DeadlineExceeded && (ctx == nil || ctx.Err() == nil) {
		return clshNewTimeoutError(*timeoutMs)
	}
	if ctx != nil && ctx.Err() != nil {
		return ai.AbortNewError("Request aborted")
	}
	return err
}

func clshClassifierRequestHeaders(
	model *ai.ClassifierModel,
	apiKey string,
	optionsHeaders ai.ProviderHeaders,
	identity *ai.ClientIdentity,
) *omap.Map[string, string] {
	idHeaders := ai.GetIdentityHeaders(ai.ClientIdentityTarget{
		Provider: string(model.Provider),
		BaseUrl:  model.BaseUrl,
	}, identity)
	return ai.ProviderHeadersToOrderedRecord(
		clshToProviderHeaders(idHeaders),
		clshStaticProviderHeaders("authorization", "Bearer "+apiKey, "content-type", "application/json"),
		clshToProviderHeaders(model.Headers),
		optionsHeaders,
	)
}

// PostClassifierRequest posts one JSON classifier request with bearer auth, OnPayload/OnResponse
// hooks, a fresh timeout per attempt, and provider retries.
func PostClassifierRequest(
	ctx context.Context,
	label string,
	rawUrl string,
	model *ai.ClassifierModel,
	body any,
	options *ai.ClassifierOptions,
	noRetryStatuses []int,
) (any, error) {
	if model == nil {
		return nil, fmt.Errorf("No API key for provider: ")
	}
	if options == nil || options.ApiKey == nil || *options.ApiKey == "" {
		return nil, fmt.Errorf("No API key for provider: %s", model.Provider)
	}
	apiKey := *options.ApiKey
	payload := body
	if options.OnPayload != nil {
		transformed, err := options.OnPayload(payload, model)
		if err != nil {
			return nil, err
		}
		if transformed != nil {
			payload = transformed
		}
	}

	httpClient := clshResolveHTTPClient(options.HTTPClient, options.Env)
	mergedHeaders := clshClassifierRequestHeaders(model, apiKey, options.Headers, options.Identity)
	maxRetries := 2
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}

	type clshAttemptResult struct {
		response *http.Response
		json     any
	}

	res, err := ai.RetryProviderRequest(ctx, func(retryCtx context.Context) (clshAttemptResult, error) {
		bodyBytes, err := jsonx.Marshal(payload)
		if err != nil {
			return clshAttemptResult{}, err
		}
		attemptCtx, cancel, timeoutCtx := clshCombineAttemptContext(retryCtx, options.TimeoutMs)
		defer cancel()

		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, rawUrl, bytes.NewReader(bodyBytes))
		if err != nil {
			return clshAttemptResult{}, err
		}
		if mergedHeaders != nil {
			for k, v := range mergedHeaders.All() {
				req.Header.Set(k, v)
			}
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return clshAttemptResult{}, clshCheckTimeout(err, retryCtx, timeoutCtx, options.TimeoutMs)
		}
		defer resp.Body.Close()
		respBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return clshAttemptResult{}, clshCheckTimeout(readErr, retryCtx, timeoutCtx, options.TimeoutMs)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return clshAttemptResult{}, clshNewHttpError(label, resp, string(respBytes))
		}
		parsed, parseErr := jsonx.Parse(respBytes)
		if parseErr != nil {
			return clshAttemptResult{}, parseErr
		}
		return clshAttemptResult{response: resp, json: parsed}, nil
	}, &ai.ProviderRetryOptions{
		MaxRetries:      &maxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		NoRetryStatuses: noRetryStatuses,
	})
	if err != nil {
		return nil, err
	}

	if options.OnResponse != nil {
		if err := options.OnResponse(&ai.ProviderResponse{
			Status:  res.response.StatusCode,
			Headers: ai.HeadersToRecord(res.response.Header),
		}, model); err != nil {
			return nil, err
		}
	}
	return res.json, nil
}

func clshTokenCount(value any) int {
	if f, ok := clshToFloat64(value); ok && f > 0 {
		return int(f)
	}
	return 0
}

// ParseClassifierUsage parses a `{ input_tokens, output_tokens }` usage object and prices it
// from the model catalog. Missing or non-object usage returns nil.
func ParseClassifierUsage(value any, model *ai.ClassifierModel) *ai.Usage {
	if !IsRecord(value) {
		return nil
	}
	inVal, hasIn := clshRecordGet(value, "input_tokens")
	outVal, hasOut := clshRecordGet(value, "output_tokens")
	if !hasIn && !hasOut {
		return nil
	}
	input := clshTokenCount(inVal)
	output := clshTokenCount(outVal)
	usage := &ai.Usage{
		Input:       input,
		Output:      output,
		TotalTokens: input + output,
	}
	ai.CalculateCost(model, usage)
	return usage
}

func clshTrimTrailingSlashes(s string) string {
	return strings.TrimRight(js.Trim(s), "/")
}
