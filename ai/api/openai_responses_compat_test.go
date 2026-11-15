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

type torcRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f torcRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func torcBoolPtr(b bool) *bool    { return &b }
func torcStrPtr(s string) *string { return &s }

func torcCompletedResp() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n",
		)),
	}
}

func torcCaptureClient(bodyOut *map[string]any, headersOut *http.Header) *http.Client {
	return &http.Client{
		Transport: torcRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if headersOut != nil {
				*headersOut = req.Header.Clone()
			}
			raw, _ := io.ReadAll(req.Body)
			if bodyOut != nil {
				*bodyOut = nil
				_ = json.Unmarshal(raw, bodyOut)
			}
			return torcCompletedResp(), nil
		}),
	}
}

func torcModel(provider ai.ProviderId, baseURL string, id string) *ai.Model {
	return &ai.Model{
		Id:            id,
		Name:          id,
		Api:           string(ai.KnownApiOpenaiResponses),
		Provider:      provider,
		BaseUrl:       baseURL,
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}

func torcSimpleCtx(prompt string, tools ...*ai.Tool) *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		SystemPrompt: torcStrPtr("sys"),
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &prompt}, Timestamp: 1},
		},
		Tools: tools,
	})
}

func TestOpenAIResponsesCompatAndDefaults(t *testing.T) {
	t.Run("omits reasoning when none requested even when ThinkingLevelMap maps off", func(t *testing.T) {
		m := torcModel("github-copilot", "https://api.githubcopilot.com", "gpt-6-sol")
		m.ThinkingLevelMap = &ai.ThinkingLevelMap{Off: jsonx.Some("none")}

		var body map[string]any
		var onPayloadSeen bool
		res, err := StreamOpenaiResponses(context.Background(), m, torcSimpleCtx("hi"), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     torcStrPtr("tok"),
				HTTPClient: torcCaptureClient(&body, nil),
				OnPayload: func(payload any, _ *ai.Model) (any, error) {
					obj, ok := payload.(*jsonx.Object)
					onPayloadSeen = ok && obj != nil
					return nil, nil
				},
			},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected error/stop: %v / %s", err, res.StopReason)
		}
		if !onPayloadSeen {
			t.Fatalf("expected OnPayload callback invoked with *jsonx.Object")
		}
		if _, hasReasoning := body["reasoning"]; hasReasoning {
			t.Fatalf("expected reasoning omitted when unrequested on Copilot, got %v", body["reasoning"])
		}
	})

	t.Run("supports additional_tools + tool_choice=required and strict=false vs strict=true", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("path", typebox.String())
		props.Set("offset", typebox.Optional(typebox.Number()))
		ordinary := &ai.Tool{Name: "ordinary", Description: "Ordinary", Parameters: typebox.Object(props)}

		cProps := omap.NewMap[string, *typebox.Schema]()
		cProps.Set("value", typebox.String())
		constrained := &ai.Tool{
			Name:        "constrained",
			Description: "Constrained",
			Parameters:  typebox.Object(cProps),
			ConstrainedSampling: ai.ToolConstrainedSamplingValue{
				V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer},
			},
		}

		// SupportsMidConvoSystemMessages + SupportsAdditionalTools puts tools inside additional_tools input item
		mAdd := torcModel("openai", "https://api.openai.com/v1", "gpt-5.6-luna")
		mAdd.Compat.V = &ai.OpenAIResponsesCompat{
			SupportsMidConvoSystemMessages: torcBoolPtr(true),
			SupportsAdditionalTools:        torcBoolPtr(true),
		}
		var addBody map[string]any
		_, err := StreamOpenaiResponses(context.Background(), mAdd, torcSimpleCtx("call ordinary", ordinary), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torcStrPtr("sk-test"), HTTPClient: torcCaptureClient(&addBody, nil)},
			ToolChoice:    "required",
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if addBody["tool_choice"] != "required" {
			t.Fatalf("expected tool_choice=required, got %v", addBody["tool_choice"])
		}
		rawInput, _ := json.Marshal(addBody["input"])
		if !strings.Contains(string(rawInput), `"type":"additional_tools"`) {
			t.Fatalf("expected additional_tools item in input, got %s", string(rawInput))
		}

		// SupportsStrictMode=true emits explicit strict=false for ordinary and strict=true for constrained
		mStrict := torcModel("cloudflare-ai-gateway", "https://gateway.ai.cloudflare.com/v1/acc/gw/openai", "gpt-5.6-sol")
		mStrict.Compat.V = &ai.OpenAIResponsesCompat{SupportsStrictMode: torcBoolPtr(true)}
		var strictBody map[string]any
		_, err = StreamOpenaiResponses(context.Background(), mStrict, torcSimpleCtx("use tool", ordinary, constrained), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torcStrPtr("sk-test"), HTTPClient: torcCaptureClient(&strictBody, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tools := strictBody["tools"].([]any)
		t0 := tools[0].(map[string]any)
		t1 := tools[1].(map[string]any)
		if t0["strict"] != false || t1["strict"] != true {
			t.Fatalf("expected ordinary strict=false and constrained strict=true, got %v / %v", t0["strict"], t1["strict"])
		}

		// SupportsMaxOutputTokens=false omits max_output_tokens
		mNoMax := torcModel("proxy", "https://proxy.example.com/v1", "m1")
		mNoMax.Compat.V = &ai.OpenAIResponsesCompat{SupportsMaxOutputTokens: torcBoolPtr(false)}
		var noMaxBody map[string]any
		maxT := 500
		_, err = StreamOpenaiResponses(context.Background(), mNoMax, torcSimpleCtx("hi"), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torcStrPtr("sk-test"), MaxTokens: &maxT, HTTPClient: torcCaptureClient(&noMaxBody, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, hasMax := noMaxBody["max_output_tokens"]; hasMax {
			t.Fatalf("expected max_output_tokens omitted when SupportsMaxOutputTokens=false, got %v", noMaxBody["max_output_tokens"])
		}
	})

	t.Run("chatgpt sign-in omits unsupported fields and preserves store=false on API-key OpenAI", func(t *testing.T) {
		m := torcModel("openai", "https://api.openai.com/v1", "gpt-5.2")
		maxT := 2048
		temp := 0.7
		tier := "priority"

		// ChatGPT OAuth token (non sk-* against api.openai.com)
		var chatgptBody map[string]any
		_, err := StreamOpenaiResponses(context.Background(), m, torcSimpleCtx("hi"), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:      torcStrPtr("eyJ.oauth.token"),
				MaxTokens:   &maxT,
				Temperature: &temp,
				HTTPClient:  torcCaptureClient(&chatgptBody, nil),
				Env:         ai.ProviderEnv{"PI_CACHE_RETENTION": "long"},
			},
			ServiceTier: &tier,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, hasMax := chatgptBody["max_output_tokens"]; hasMax {
			t.Fatalf("expected max_output_tokens omitted for ChatGPT sign-in, got %v", chatgptBody["max_output_tokens"])
		}
		if _, hasTemp := chatgptBody["temperature"]; hasTemp {
			t.Fatalf("expected temperature omitted for ChatGPT sign-in, got %v", chatgptBody["temperature"])
		}
		if _, hasPCR := chatgptBody["prompt_cache_retention"]; hasPCR {
			t.Fatalf("expected prompt_cache_retention omitted for ChatGPT sign-in, got %v", chatgptBody["prompt_cache_retention"])
		}

		// Standard sk-* key retains max_output_tokens, temperature, service_tier, store=false
		var skBody map[string]any
		_, err = StreamOpenaiResponses(context.Background(), m, torcSimpleCtx("hi"), &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:      torcStrPtr("sk-real"),
				MaxTokens:   &maxT,
				Temperature: &temp,
				HTTPClient:  torcCaptureClient(&skBody, nil),
			},
			ServiceTier: &tier,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skBody["store"] != false || skBody["service_tier"] != "priority" || skBody["max_output_tokens"] != float64(2048) || skBody["temperature"] != 0.7 {
			t.Fatalf("unexpected sk-* OpenAI Responses body: store=%v service_tier=%v max_output_tokens=%v temperature=%v",
				skBody["store"], skBody["service_tier"], skBody["max_output_tokens"], skBody["temperature"])
		}
	})

	t.Run("applies prompt_cache_key clamp, prompt_cache_retention, session affinity, and mid-convo configuration_update effort", func(t *testing.T) {
		m := torcModel("openai", "https://api.openai.com/v1", "gpt-5.2")
		modeEnabled := ai.PromptCacheKeyModeEnabled
		m.Compat.V = &ai.OpenAIResponsesCompat{
			PromptCacheKeyMode:     &modeEnabled,
			SupportsMidConvoEffort: torcBoolPtr(true),
		}
		longSess := strings.Repeat("✨", 80)
		lowLevel := ai.ThinkingLevelLow
		highLevel := ai.ThinkingLevelHigh
		prevEffort := "low"

		ctx := ai.NormalizeContext(&ai.Context{
			SystemPrompt: torcStrPtr("sys"),
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: torcStrPtr("q1")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiResponses, Provider: "openai", Model: "gpt-5.2",
					ProviderThinkingLevel: &prevEffort,
					Content:               ai.ContentList{&ai.TextContent{Type: "text", Text: "a1"}},
					StopReason:            ai.StopReasonStop, Timestamp: 2,
				},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: torcStrPtr("q2")}, Timestamp: 3},
			},
		})

		var body map[string]any
		var hdrs http.Header
		_, err := StreamOpenaiResponses(context.Background(), m, ctx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     torcStrPtr("sk-123"),
				SessionId:  &longSess,
				HTTPClient: torcCaptureClient(&body, &hdrs),
				Env:        ai.ProviderEnv{"PI_CACHE_RETENTION": "long"},
			},
			ReasoningEffort: &highLevel,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pck, _ := body["prompt_cache_key"].(string); len([]rune(pck)) != 64 {
			t.Fatalf("expected 64-rune clamped prompt_cache_key, got %d", len([]rune(pck)))
		}
		if body["prompt_cache_retention"] != "24h" {
			t.Fatalf("expected prompt_cache_retention=24h, got %v", body["prompt_cache_retention"])
		}
		if hdrs.Get("session-id") != longSess || hdrs.Get("x-client-request-id") != longSess {
			t.Fatalf("expected OpenAI Responses session-id + x-client-request-id headers, got %+v", hdrs)
		}
		rawInput, _ := json.Marshal(body["input"])
		if !strings.Contains(string(rawInput), `"type":"configuration_update"`) ||
			!strings.Contains(string(rawInput), `"effort":"high"`) {
			t.Fatalf("expected configuration_update items on mid-convo effort change: %s", string(rawInput))
		}

		// Disabled promptCacheKeyMode & CacheRetention=None omit prompt_cache_key and configuration_update omitted when compat flag off
		mOff := torcModel("openai", "https://api.openai.com/v1", "gpt-5.2")
		noneRet := ai.CacheRetentionNone
		var offBody map[string]any
		_, err = StreamOpenaiResponses(context.Background(), mOff, ctx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:         torcStrPtr("sk-123"),
				SessionId:      &longSess,
				CacheRetention: &noneRet,
				HTTPClient:     torcCaptureClient(&offBody, nil),
			},
			ReasoningEffort: &lowLevel,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, hasPCK := offBody["prompt_cache_key"]; hasPCK {
			t.Fatalf("expected prompt_cache_key omitted on CacheRetention=none, got %v", offBody["prompt_cache_key"])
		}
		rawOffInput, _ := json.Marshal(offBody["input"])
		if strings.Contains(string(rawOffInput), `"configuration_update"`) {
			t.Fatalf("expected no configuration_update when SupportsMidConvoEffort is false: %s", string(rawOffInput))
		}
	})
}
