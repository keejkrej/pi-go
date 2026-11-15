// Ported from packages/ai/test/google-{raw-stop-reason,stop-reason-continuation,vertex-api-key-resolution}.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tgtvStrPtr(s string) *string { return &s }

func tgtvContext() *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgtvStrPtr("hello")}, Timestamp: 1},
		},
	})
}

func tgtvModel(modelApi ai.KnownApi, provider, id, baseUrl string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(modelApi),
			Provider: ai.ProviderId(provider),
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     1000,
	}
}

func TestGoogleRawStopReasonsContinuationAndHeaders(t *testing.T) {
	if got := api.MapGoogleStopReason("CONTINUATION"); got != ai.StopReasonLength {
		t.Fatalf("MapGoogleStopReason(CONTINUATION) = %q, want length (#12097)", got)
	}

	var seenGenUA, seenGenApiKey, seenGenClient, seenGenPath string
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		seenGenUA = r.Header.Get("User-Agent")
		seenGenApiKey = r.Header.Get("x-goog-api-key")
		seenGenClient = r.Header.Get("x-goog-api-client")
		seenGenPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")

		switch n {
		case 1:
			_, _ = io.WriteString(w, `data: {"responseId":"google-response-id","candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":0,"totalTokenCount":1}}`+"\n\n")
		case 2:
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"call-1","name":"echo","args":{"value":"truncated"}}}]},"finishReason":"MAX_TOKENS"}]}`+"\n\n")
		case 3:
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"call-2","name":"echo","args":{"value":"done"}}}]},"finishReason":"STOP"}]}`+"\n\n")
		case 4:
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"CONTINUATION","continuationToken":"token-1"}]}`+"\n\n")
		default:
			_, _ = io.WriteString(w, `data: {"responseId":"resp_google","candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`+"\n\n")
		}
	}))
	defer srv.Close()

	gModel := tgtvModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-2.5-flash", srv.URL)
	runGen := func(opts ai.ProviderStreamOptions) *ai.AssistantMessage {
		msg, err := api.StreamGoogleGenerativeAi(context.Background(), gModel, tgtvContext(), opts).Result(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}

	gOpts := &api.GoogleOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tgtvStrPtr("test-api-key")},
		},
	}

	// 1. MALFORMED_FUNCTION_CALL -> error + rawStopReason preserved + default User-Agent & headers.
	m1 := runGen(gOpts)
	if m1.StopReason != ai.StopReasonError || m1.RawStopReason == nil || *m1.RawStopReason != "MALFORMED_FUNCTION_CALL" ||
		m1.ErrorMessage == nil || *m1.ErrorMessage != "Provider stopped with: MALFORMED_FUNCTION_CALL" {
		t.Fatalf("unexpected MALFORMED_FUNCTION_CALL result: %+v", m1)
	}
	if seenGenUA != ai.FormatUserAgent(nil) || seenGenApiKey != "test-api-key" || !strings.Contains(seenGenClient, "google-genai-sdk") ||
		!strings.Contains(seenGenPath, "/v1beta/models/gemini-2.5-flash:streamGenerateContent") {
		t.Fatalf("unexpected Google transport headers/path: ua=%q key=%q client=%q path=%q", seenGenUA, seenGenApiKey, seenGenClient, seenGenPath)
	}

	// 2. MAX_TOKENS with toolCall -> length.
	m2 := runGen(gOpts)
	if m2.StopReason != ai.StopReasonLength || m2.RawStopReason == nil || *m2.RawStopReason != "MAX_TOKENS" {
		t.Fatalf("expected length on MAX_TOKENS+toolCall, got %+v", m2)
	}

	// 3. STOP with toolCall -> toolUse.
	m3 := runGen(gOpts)
	if m3.StopReason != ai.StopReasonToolUse || m3.RawStopReason == nil || *m3.RawStopReason != "STOP" {
		t.Fatalf("expected toolUse on STOP+toolCall, got %+v", m3)
	}

	// 4. CONTINUATION -> length (#12097), single request (no continuation fetch).
	beforeCalls := callCount.Load()
	m4 := runGen(gOpts)
	if callCount.Load() != beforeCalls+1 || m4.StopReason != ai.StopReasonLength {
		t.Fatalf("expected single request & length on CONTINUATION, calls=%d msg=%+v", callCount.Load()-beforeCalls, m4)
	}

	// 5. OnProviderStreamEvent forwarding in order + custom User-Agent override.
	customHdrs := omap.NewMap[string, *string]()
	customHdrs.Set("User-Agent", tgtvStrPtr("custom-agent"))
	var eventsCount int
	m5 := runGen(&api.GoogleOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:  tgtvStrPtr("test-api-key"),
				Headers: customHdrs,
			},
			OnProviderStreamEvent: func(data any, m *ai.Model) error {
				eventsCount++
				return nil
			},
		},
	})
	if seenGenUA != "custom-agent" || eventsCount != 2 || m5.ResponseId == nil || *m5.ResponseId != "resp_google" {
		t.Fatalf("unexpected custom UA / hook state: ua=%q events=%d msg=%+v", seenGenUA, eventsCount, m5)
	}
}

func TestGoogleVertexApiKeyResolutionAndPaths(t *testing.T) {
	var paths []string
	var authHeaders, apiKeyHeaders, uaHeaders []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		apiKeyHeaders = append(apiKeyHeaders, r.Header.Get("x-goog-api-key"))
		uaHeaders = append(uaHeaders, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"responseId":"vertex-response-id","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer srv.Close()

	// 1. Explicit real API key uses x-goog-api-key + /v1/publishers/google/models/...
	vModel := tgtvModel(ai.KnownApiGoogleVertex, "google-vertex", "gemini-3-flash-preview", srv.URL)
	msgKey, err := api.StreamGoogleVertex(context.Background(), vModel, tgtvContext(), &api.GoogleVertexOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tgtvStrPtr("AIzaSyRealLookingApiKey12345")},
		},
	}).Result(context.Background())
	if err != nil || msgKey.StopReason != ai.StopReasonStop {
		t.Fatalf("vertex API key mode failed: %v, %+v", err, msgKey)
	}
	if apiKeyHeaders[0] != "AIzaSyRealLookingApiKey12345" || authHeaders[0] != "" ||
		!strings.Contains(paths[0], "/v1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent") {
		t.Fatalf("unexpected Vertex API-key path/headers: path=%q key=%q auth=%q", paths[0], apiKeyHeaders[0], authHeaders[0])
	}

	// 2. Placeholder marker (<authenticated>) falls back to ADC / Authorization mode (bypassed via explicit Authorization header here = 0 network).
	authHdrs := omap.NewMap[string, *string]()
	authHdrs.Set("Authorization", tgtvStrPtr("Bearer test-adc-token"))
	msgMarker, err := api.StreamGoogleVertex(context.Background(), vModel, tgtvContext(), &api.GoogleVertexOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:  tgtvStrPtr("<authenticated>"),
				Headers: authHdrs,
			},
		},
		Project:  tgtvStrPtr("test-project"),
		Location: tgtvStrPtr("us-central1"),
	}).Result(context.Background())
	if err != nil || msgMarker.StopReason != ai.StopReasonStop {
		t.Fatalf("vertex marker fallback failed: %v, %+v", err, msgMarker)
	}
	if apiKeyHeaders[1] != "" || authHeaders[1] != "Bearer test-adc-token" ||
		!strings.Contains(paths[1], "/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent") {
		t.Fatalf("unexpected Vertex ADC fallback path/headers: path=%q key=%q auth=%q", paths[1], apiKeyHeaders[1], authHeaders[1])
	}

	// 3. BaseUrl that already contains /v1 does not duplicate /v1.
	vVersioned := tgtvModel(ai.KnownApiGoogleVertex, "google-vertex", "gemini-3-flash-preview",
		fmt.Sprintf("%s/v1/projects/test-project/locations/global", srv.URL))
	_, err = api.StreamGoogleVertex(context.Background(), vVersioned, tgtvContext(), &api.GoogleVertexOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Headers: authHdrs},
		},
		Project:  tgtvStrPtr("test-project"),
		Location: tgtvStrPtr("us-central1"),
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(paths[2], "/v1/projects/test-project/locations/global/publishers/google/models/") {
		t.Fatalf("expected non-duplicated versioned Vertex path, got %q", paths[2])
	}

	// 4. Arbitrary <...> placeholder API key falls back to ADC / Authorization header.
	msgCustomMarker, err := api.StreamGoogleVertex(context.Background(), vModel, tgtvContext(), &api.GoogleVertexOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:  tgtvStrPtr("<custom-adc-marker>"),
				Headers: authHdrs,
			},
		},
		Project:  tgtvStrPtr("test-project"),
		Location: tgtvStrPtr("us-central1"),
	}).Result(context.Background())
	if err != nil || msgCustomMarker.StopReason != ai.StopReasonStop {
		t.Fatalf("vertex custom <...> marker failed: %v, %+v", err, msgCustomMarker)
	}
	if apiKeyHeaders[3] != "" || authHeaders[3] != "Bearer test-adc-token" {
		t.Fatalf("expected <custom-adc-marker> to use Bearer auth, got x-goog-api-key=%q auth=%q", apiKeyHeaders[3], authHeaders[3])
	}

	// 5. Gemini 2.5 Flash minimal thinking budget resolves to 128.
	flash25 := tgtvModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-2.5-flash", srv.URL)
	flash25.Reasoning = true
	lvlMin := ai.ThinkingLevelMinimal
	var capturedFlashCfg *jsonx.Object
	_, _ = api.StreamSimpleGoogleGenerativeAi(context.Background(), flash25, tgtvContext(), &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: tgtvStrPtr("k"),
				OnPayload: func(p any, m *ai.Model) (any, error) {
					capturedFlashCfg = p.(*jsonx.Object).Clone()
					return nil, errors.New("stop")
				},
			},
		},
		Reasoning: &lvlMin,
	}).Result(context.Background())
	cfgObj, _ := capturedFlashCfg.GetObject("config")
	tcObj, _ := cfgObj.GetObject("thinkingConfig")
	if b, _ := tcObj.GetNumber("thinkingBudget"); b != 128 {
		t.Fatalf("expected gemini-2.5-flash minimal thinkingBudget=128, got %v", tcObj)
	}

	// 6. thoughtsTokenCount sets Usage.Reasoning without synthesizing TotalTokens, and default maxRetries=0 does not retry 429.
	var retryHits atomic.Int32
	srvUsageAndRetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := retryHits.Add(1)
		if r.URL.Path == "/v1beta/models/gemini-retry:streamGenerateContent" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_ = n
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":15}}`+"\n\n")
	}))
	defer srvUsageAndRetry.Close()

	usageMsg, err := api.StreamGoogleGenerativeAi(context.Background(), tgtvModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-2.5-flash", srvUsageAndRetry.URL), tgtvContext(), &api.GoogleOptions{
		StreamOptions: ai.StreamOptions{ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tgtvStrPtr("k")}},
	}).Result(context.Background())
	if err != nil || usageMsg.Usage.Reasoning == nil || *usageMsg.Usage.Reasoning != 15 || usageMsg.Usage.TotalTokens != 0 {
		t.Fatalf("expected Usage.Reasoning=15 and TotalTokens=0 (not synthesized), got %+v (err=%v)", usageMsg.Usage, err)
	}

	retryHits.Store(0)
	err429Msg, _ := api.StreamGoogleGenerativeAi(context.Background(), tgtvModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-retry", srvUsageAndRetry.URL), tgtvContext(), &api.GoogleOptions{
		StreamOptions: ai.StreamOptions{ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tgtvStrPtr("k")}},
	}).Result(context.Background())
	if err429Msg.StopReason != ai.StopReasonError || retryHits.Load() != 1 {
		t.Fatalf("expected default maxRetries=0 (1 HTTP attempt on 429), got attempts=%d msg=%+v", retryHits.Load(), err429Msg)
	}
}
