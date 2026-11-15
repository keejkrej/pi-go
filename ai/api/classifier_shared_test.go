// Ported / test coverage for ai/api/classifier_shared.go.

package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tcmSampleModel(baseUrl string) *ai.ClassifierModel {
	headers := omap.NewMap[string, string]()
	headers.Set("X-Model-Header", "from-model")
	headers.Set("X-Suppress-Me", "should-drop")
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "clf-model",
			Name:     "clf-model",
			Api:      "typesafe-system-one",
			Provider: "typesafe",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost: ai.ModelCost{
				Input:  2.0,
				Output: 6.0,
			},
			Headers: headers,
		},
		Type: ai.ModelTypeClassifier,
	}
}

func TestClassifierSharedHelpers(t *testing.T) {
	if api.IsRecord(nil) {
		t.Fatal("nil must not be a record")
	}
	if api.IsRecord([]any{1, 2}) {
		t.Fatal("slice must not be a record")
	}
	if !api.IsRecord(jsonx.ObjectOf("a", 1)) {
		t.Fatal("*jsonx.Object must be a record")
	}
	if !api.IsRecord(map[string]any{"a": 1}) {
		t.Fatal("map[string]any must be a record")
	}

	val, err := api.RequiredNumber("test", 0.75, "confidence")
	if err != nil || val != 0.75 {
		t.Fatalf("RequiredNumber(0.75) = %v, %v", val, err)
	}
	if _, err := api.RequiredNumber("test", "nope", "score"); err == nil {
		t.Fatal("expected error for string")
	}

	model := tcmSampleModel("https://example.invalid")
	if u := api.ParseClassifierUsage(nil, model); u != nil {
		t.Fatalf("expected nil usage for nil input, got %+v", u)
	}
	if u := api.ParseClassifierUsage(jsonx.ObjectOf("other", 1), model); u != nil {
		t.Fatalf("expected nil usage when token fields absent, got %+v", u)
	}
	usage := api.ParseClassifierUsage(jsonx.ObjectOf("input_tokens", float64(500), "output_tokens", float64(250)), model)
	if usage == nil {
		t.Fatal("expected parsed usage")
	}
	if usage.Input != 500 || usage.Output != 250 || usage.TotalTokens != 750 {
		t.Fatalf("unexpected usage counts: %+v", usage)
	}
	if usage.Cost.Total <= 0 {
		t.Fatalf("expected non-zero calculated cost, got %+v", usage.Cost)
	}
}

func TestPostClassifierRequestRetriesHeadersAndTimeout(t *testing.T) {
	var attempts atomic.Int32
	var seenAuth, seenModel, seenSuppressed, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		seenAuth = r.Header.Get("Authorization")
		seenModel = r.Header.Get("X-Model-Header")
		seenSuppressed = r.Header.Get("X-Suppress-Me")
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)

		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("X-Resp-Tag", "ok")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	model := tcmSampleModel(srv.URL)
	apiKey := "secret-key"
	optHeaders := omap.NewMap[string, *string]()
	override := "from-opt"
	optHeaders.Set("X-Opt", &override)
	optHeaders.Set("X-Suppress-Me", nil)

	var seenRespStatus int
	var seenRespHeader string
	retries := 2

	res, err := api.PostClassifierRequest(context.Background(), "TypeSafe", srv.URL+"/classify", model, jsonx.ObjectOf("a", 1), &ai.ClassifierOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
			ApiKey:     &apiKey,
			Headers:    optHeaders,
			MaxRetries: &retries,
			OnPayload: func(payload any, m *ai.ClassifierModel) (any, error) {
				obj := payload.(*jsonx.Object)
				obj.Set("b", 2)
				return obj, nil
			},
			OnResponse: func(resp *ai.ProviderResponse, m *ai.ClassifierModel) error {
				seenRespStatus = resp.Status
				seenRespHeader = resp.Headers["x-resp-tag"]
				return nil
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("PostClassifierRequest error: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
	if seenAuth != "Bearer secret-key" || seenModel != "from-model" || seenSuppressed != "" {
		t.Fatalf("unexpected headers: auth=%q model=%q suppressed=%q", seenAuth, seenModel, seenSuppressed)
	}
	if seenRespStatus != 200 || seenRespHeader != "ok" {
		t.Fatalf("unexpected OnResponse: status=%d tag=%q", seenRespStatus, seenRespHeader)
	}
	var parsed map[string]any
	if err := jsonx.Unmarshal([]byte(seenBody), &parsed); err != nil || parsed["b"] != float64(2) {
		t.Fatalf("unexpected sent body: %s (%v)", seenBody, err)
	}
	if !api.IsRecord(res) {
		t.Fatalf("expected record result, got %T", res)
	}

	// NoRetryStatuses skips retries.
	attempts.Store(0)
	srvNoRetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("down"))
	}))
	defer srvNoRetry.Close()

	_, err = api.PostClassifierRequest(context.Background(), "TypeSafe", srvNoRetry.URL, model, jsonx.NewObject(), &ai.ClassifierOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
			ApiKey:     &apiKey,
			MaxRetries: &retries,
		},
	}, []int{http.StatusServiceUnavailable})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected no retry on 503 when excluded, got %d attempts", attempts.Load())
	}
	var clfErr *api.ClassifierHttpError
	if !errors.As(err, &clfErr) || clfErr.Status == nil || *clfErr.Status != 503 {
		t.Fatalf("expected *ClassifierHttpError with 503, got %T: %v", err, err)
	}

	// Timeout triggers TimeoutError name without retries=0.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(250 * time.Millisecond):
			_, _ = w.Write([]byte(`{}`))
		case <-r.Context().Done():
		}
	}))
	defer slowSrv.Close()

	timeoutMs := int64(25)
	zeroRetries := 0
	_, err = api.PostClassifierRequest(context.Background(), "TypeSafe", slowSrv.URL, model, jsonx.NewObject(), &ai.ClassifierOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
			ApiKey:     &apiKey,
			TimeoutMs:  &timeoutMs,
			MaxRetries: &zeroRetries,
		},
	}, nil)
	if !errors.As(err, &clfErr) || clfErr.Name() != "TimeoutError" {
		t.Fatalf("expected TimeoutError, got %T (%v)", err, err)
	}
}
