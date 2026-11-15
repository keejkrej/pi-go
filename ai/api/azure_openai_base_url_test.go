package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type tazRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tazRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tazStrPtr(s string) *string { return &s }

func tazModel(baseUrl string) *ai.Model {
	return &ai.Model{
		Id:            "gpt-5.2",
		Name:          "GPT-5.2 Azure",
		Api:           string(ai.KnownApiAzureOpenaiResponses),
		Provider:      "azure-openai-responses",
		BaseUrl:       baseUrl,
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func tazSseResponse(events ...string) *http.Response {
	var buf strings.Builder
	for _, ev := range events {
		buf.WriteString("data: ")
		buf.WriteString(ev)
		buf.WriteString("\n\n")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buf.String())),
	}
}

func tazSimpleContext(text string) *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: &text},
				Timestamp: 1,
			},
		},
	})
}

func TestAzureOpenAIBaseUrlNormalization(t *testing.T) {
	t.Run("normalizes Azure cognitiveservices, foundry, and openai URLs and strips query params on Azure hosts", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want string
		}{
			{
				name: "cognitiveservices root",
				in:   "https://my-resource.cognitiveservices.azure.com",
				want: "https://my-resource.cognitiveservices.azure.com/openai/v1",
			},
			{
				name: "services.ai.azure.com root",
				in:   "https://my-foundry.services.ai.azure.com",
				want: "https://my-foundry.services.ai.azure.com/openai/v1",
			},
			{
				name: "services.ai.azure.com /openai/v1/responses trimmed",
				in:   "https://my-resource.services.ai.azure.com/openai/v1/responses",
				want: "https://my-resource.services.ai.azure.com/openai/v1",
			},
			{
				name: "openai.azure.com /openai path with query",
				in:   "https://my-resource.openai.azure.com/openai?api-version=2024-02-01",
				want: "https://my-resource.openai.azure.com/openai/v1",
			},
			{
				name: "openai.azure.com /openai/v1/responses trimmed",
				in:   "https://my-resource.openai.azure.com/openai/v1/responses/",
				want: "https://my-resource.openai.azure.com/openai/v1",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := ResolveAzureBaseUrl(tazModel(tc.in), &AzureEndpointOptions{
					StreamOptions: ai.StreamOptions{Env: ai.ProviderEnv{}},
				})
				if err != nil {
					t.Fatalf("ResolveAzureBaseUrl(%q) returned error: %v", tc.in, err)
				}
				if got != tc.want {
					t.Fatalf("ResolveAzureBaseUrl(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("preserves explicit non-Azure proxy URL and query string across HTTP request", func(t *testing.T) {
		proxyURL := "https://my-proxy.example.com/v1?custom=true"
		var capturedURL string
		var capturedHeaders http.Header
		var capturedBody map[string]any

		client := &http.Client{
			Transport: tazRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				capturedHeaders = req.Header.Clone()
				raw, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(raw, &capturedBody)
				return tazSseResponse(
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
					`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`,
					`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
				), nil
			}),
		}

		stream := StreamAzureOpenaiResponses(context.Background(), tazModel(proxyURL), tazSimpleContext("hello"), &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{
					ApiKey:     tazStrPtr("az-secret"),
					HTTPClient: client,
					Env:        ai.ProviderEnv{},
				},
			},
		})
		res, resErr := stream.Result(context.Background())
		if resErr != nil {
			t.Fatalf("stream.Result error: %v", resErr)
		}
		if res.StopReason != ai.StopReasonStop {
			t.Fatalf("expected stop, got %s (%v)", res.StopReason, res.ErrorMessage)
		}
		if !strings.HasPrefix(capturedURL, "https://my-proxy.example.com/v1/responses?") {
			t.Fatalf("expected path /v1/responses before query params, got %q", capturedURL)
		}
		if !strings.Contains(capturedURL, "custom=true") || !strings.Contains(capturedURL, "api-version=v1") {
			t.Fatalf("expected custom=true and api-version=v1 in URL query, got %q", capturedURL)
		}
		if capturedHeaders.Get("api-key") != "az-secret" {
			t.Fatalf("expected api-key header, got %q", capturedHeaders.Get("api-key"))
		}
		if storeVal, ok := capturedBody["store"].(bool); !ok || storeVal {
			t.Fatalf("expected store=false, got %v", capturedBody["store"])
		}
	})

	t.Run("errors on invalid Azure OpenAI base URL", func(t *testing.T) {
		_, err := ResolveAzureConfig(tazModel("://bad url"), &AzureEndpointOptions{
			StreamOptions: ai.StreamOptions{Env: ai.ProviderEnv{}},
		})
		if err == nil || !strings.Contains(err.Error(), "Invalid Azure OpenAI base URL") {
			t.Fatalf("expected Invalid Azure OpenAI base URL error, got %v", err)
		}
	})

	t.Run("resolves AZURE_OPENAI_RESOURCE_NAME, deployment mapping, prompt_cache_key clamp, User-Agent, strict fallback, and onProviderStreamEvent", func(t *testing.T) {
		var capturedURL string
		var capturedUA string
		var capturedBody map[string]any
		var seenEvents []string

		client := &http.Client{
			Transport: tazRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				capturedUA = req.Header.Get("User-Agent")
				raw, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(raw, &capturedBody)
				return tazSseResponse(
					`{"type":"response.created","response":{"id":"resp_1"}}`,
					`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
				), nil
			}),
		}

		longSession := strings.Repeat("🚀", 80)
		model := tazModel("")
		disableStrict := false
		modeEnabled := ai.PromptCacheKeyModeEnabled
		model.Compat.V = &ai.OpenAIResponsesCompat{
			PromptCacheKeyMode: &modeEnabled,
			SupportsStrictMode: &disableStrict,
		}
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("q", typebox.String())
		schemaOpts := jsonx.NewObject()
		schemaOpts.Set("additionalProperties", false)
		customHeaders := omap.NewMap[string, *string]()
		customHeaders.Set("User-Agent", tazStrPtr("custom-agent/2.0"))

		ctx := ai.NormalizeContext(&ai.Context{
			Messages: tazSimpleContext("hello").Messages,
			Tools: []*ai.Tool{
				{
					Name:        "search",
					Description: "Search tool",
					Parameters:  typebox.Object(props, schemaOpts),
					ConstrainedSampling: ai.ToolConstrainedSamplingValue{
						V: &ai.ConstrainedSamplingJsonSchema{
							Type:   "json_schema",
							Strict: ai.ConstrainedSamplingStrictPrefer,
						},
					},
				},
			},
		})

		stream := StreamAzureOpenaiResponses(context.Background(), model, ctx, &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{
					ApiKey:     tazStrPtr("az-key"),
					SessionId:  &longSession,
					Headers:    customHeaders,
					HTTPClient: client,
					Env: ai.ProviderEnv{
						"AZURE_OPENAI_RESOURCE_NAME":       "env-res",
						"AZURE_OPENAI_API_VERSION":         "2025-03-01-preview",
						"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-5.2=my-deploy",
					},
					OnProviderStreamEvent: func(data any, evModel *ai.Model) error {
						if evModel != model {
							t.Fatalf("expected model pointer passed to onProviderStreamEvent")
						}
						if obj, ok := data.(*jsonx.Object); ok {
							typ, _ := obj.GetString("type")
							seenEvents = append(seenEvents, typ)
						}
						return nil
					},
				},
			},
		})
		res, resErr := stream.Result(context.Background())
		if resErr != nil {
			t.Fatalf("stream.Result error: %v", resErr)
		}
		if res.StopReason != ai.StopReasonStop {
			t.Fatalf("expected stop, got %s (%v)", res.StopReason, res.ErrorMessage)
		}
		if capturedURL != "https://env-res.openai.azure.com/openai/v1/responses?api-version=2025-03-01-preview" {
			t.Fatalf("unexpected Azure URL: %q", capturedURL)
		}
		if capturedBody["model"] != "my-deploy" {
			t.Fatalf("expected deployment model=my-deploy, got %v", capturedBody["model"])
		}
		if pck, _ := capturedBody["prompt_cache_key"].(string); len([]rune(pck)) != 64 {
			t.Fatalf("expected 64-rune clamped prompt_cache_key, got len=%d (%q)", len([]rune(pck)), pck)
		}
		if capturedUA != "custom-agent/2.0" {
			t.Fatalf("expected custom User-Agent header, got %q", capturedUA)
		}
		if tools, ok := capturedBody["tools"].([]any); !ok || len(tools) != 1 {
			t.Fatalf("expected 1 tool in body, got %v", capturedBody["tools"])
		} else if toolMap, _ := tools[0].(map[string]any); toolMap["strict"] != nil {
			t.Fatalf("expected supportsStrictMode=false to omit strict, got %v", toolMap["strict"])
		}
		if strings.Join(seenEvents, ",") != "response.created,response.completed" {
			t.Fatalf("unexpected onProviderStreamEvent sequence: %v", seenEvents)
		}

		// Verify default User-Agent uses identity headers when not overridden
		var defaultUA string
		clientDefault := &http.Client{
			Transport: tazRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				defaultUA = req.Header.Get("User-Agent")
				return tazSseResponse(`{"type":"response.completed","response":{"status":"completed"}}`), nil
			}),
		}
		mDefault := tazModel("https://env-res.openai.azure.com")
		identity := ai.GetIdentityHeaders(ai.ClientIdentityTarget{Provider: string(mDefault.Provider), BaseUrl: mDefault.BaseUrl}, nil)
		s2 := StreamAzureOpenaiResponses(context.Background(), mDefault, tazSimpleContext("hi"), &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{
					ApiKey:     tazStrPtr("az-key"),
					HTTPClient: clientDefault,
					Env:        ai.ProviderEnv{},
				},
			},
		})
		_, _ = s2.Result(context.Background())
		if wantUA, _ := identity.Get("User-Agent"); wantUA != "" && defaultUA != wantUA {
			t.Fatalf("expected default User-Agent %q, got %q", wantUA, defaultUA)
		}
	})
}
