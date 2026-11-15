package ai_test

import (
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func testOverflowErrorMessage(errorMessage string, provider string) *ai.AssistantMessage {
	if provider == "" {
		provider = "ollama"
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList{},
		Api:          ai.KnownApiOpenaiCompletions,
		Provider:     ai.ProviderId(provider),
		Model:        "qwen3.5:35b",
		StopReason:   ai.StopReasonError,
		ErrorMessage: &errorMessage,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func testOverflowLengthStopMessage(api ai.Api, provider, model string, input, cacheRead, cacheWrite, output int) *ai.AssistantMessage {
	if api == "" {
		api = ai.KnownApiOpenaiCompletions
	}
	if provider == "" {
		provider = "test-provider"
	}
	if model == "" {
		model = "test-model"
	}
	return &ai.AssistantMessage{
		Role:     "assistant",
		Content:  ai.ContentList{},
		Api:      api,
		Provider: ai.ProviderId(provider),
		Model:    model,
		Usage: ai.Usage{
			Input:       input,
			Output:      output,
			CacheRead:   cacheRead,
			CacheWrite:  cacheWrite,
			TotalTokens: input + cacheRead + cacheWrite + output,
		},
		StopReason: ai.StopReasonLength,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func TestIsContextOverflow(t *testing.T) {
	requestSizeOverflowCases := []string{
		`github-copilot API error (413): {"message":"failed to parse request","code":""}`,
		"custom-provider API error (413): Payload Too Large",
		`413 {"error":{"message":"failed to parse request","code":""}}`,
		"413",
	}
	for _, msg := range requestSizeOverflowCases {
		if !ai.IsContextOverflow(testOverflowErrorMessage(msg, ""), 1_000_000) {
			t.Fatalf("expected overflow with contextWindow for %q", msg)
		}
		if !ai.IsContextOverflow(testOverflowErrorMessage(msg, "")) {
			t.Fatalf("expected overflow without contextWindow for %q", msg)
		}
	}

	nonOverflowCases := []string{
		`github-copilot API error (400): {"message":"failed to parse request","code":""}`,
		"custom-provider API error (429): Too many requests",
		`github-copilot API error (400): {"message":"failed to parse request: invalid value 413"}`,
		"custom-provider API error (400): upstream mentioned OpenAI API error (413): invalid input",
		"custom-provider API error (4130): invalid input",
		"4130 failed to parse request",
	}
	for _, msg := range nonOverflowCases {
		if ai.IsContextOverflow(testOverflowErrorMessage(msg, "")) {
			t.Fatalf("did not expect overflow for %q", msg)
		}
	}

	t.Run("limits HTTP-status matching to failed responses", func(t *testing.T) {
		m := testOverflowErrorMessage(`OpenAI API error (413): {"message":"failed to parse request"}`, "")
		m.StopReason = ai.StopReasonAborted
		if ai.IsContextOverflow(m) {
			t.Fatal("expected aborted message not to match")
		}
		m.StopReason = ai.StopReasonStop
		if ai.IsContextOverflow(m) {
			t.Fatal("expected stop message not to match")
		}
	})

	t.Run("provider error messages", func(t *testing.T) {
		tests := []struct {
			name     string
			msg      string
			provider string
			window   int
			want     bool
		}{
			{"explicit Ollama prompt-too-long", "400 `prompt too long; exceeded max context length by 100918 tokens`", "ollama", 32768, true},
			{"z.ai prompt-too-long", `400 {"code":"1261","message":"Prompt too long"}`, "zai", 1048576, true},
			{"z.ai CN prompt-exceeds-max-length", `400 {"code":"1261","message":"Prompt exceeds max length"}`, "zai", 1048576, true},
			{"Together AI context length", "400 The input (516368 tokens) is longer than the model's context length (262144 tokens).", "ollama", 262144, true},
			{"LiteLLM-wrapped OpenAI max context", "Error: 503 litellm.ServiceUnavailableError: litellm.MidStreamFallbackError: litellm.APIConnectionError: APIConnectionError: OpenAIException - Requested token count exceeds the model's maximum context length of 131072 tokens.", "ollama", 131072, true},
			{"LiteLLM pre-call ContextWindowExceededError", "400: {\"message\":\"litellm.ContextWindowExceededError: litellm.BadRequestError: litellm._pre_call_checks: Context Window exceeded for given call. No models have context window large enough for this call.\nModel=Qwen3.8-Flash-Next, Max Input Tokens=262144, Got=265262\nmodel=Qwen3.8-Flash-Next. context_window_fallbacks=None. fallbacks=[].\"}", "ollama", 262144, true},
			{"OpenAI-compatible parenthesized max context", "Error: 400 Input length (265330) exceeds model's maximum context length (262144).", "ollama", 262144, true},
			{"OpenRouter Poolside max allowed input", "Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.", "ollama", 131072, true},
			{"DS4 configured context size", "400 Prompt has 256468 tokens, but the configured context size is 256000 tokens", "ollama", 256000, true},
			{"DS4 comma numbers", "Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens", "ollama", 256000, true},
			{"generic non-overflow Ollama", "500 `model runner crashed unexpectedly`", "ollama", 32768, false},
			{"bodyless 400 Cerebras", "400 status code (no body)", "cerebras", 131072, true},
			{"bodyless 400 non-Cerebras", "400 status code (no body)", "opencode-go", 1000000, false},
			{"Bedrock throttling Too many tokens", "Throttling error: Too many tokens, please wait before trying again.", "ollama", 200000, false},
			{"Bedrock service unavailable", "Service unavailable: The service is temporarily unavailable.", "ollama", 200000, false},
			{"generic rate limit", "Rate limit exceeded, please retry after 30 seconds.", "ollama", 200000, false},
			{"HTTP 429 style", "Too many requests. Please slow down.", "ollama", 200000, false},
		}
		for _, tc := range tests {
			if got := ai.IsContextOverflow(testOverflowErrorMessage(tc.msg, tc.provider), tc.window); got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	t.Run("length stop overflow and recoverability", func(t *testing.T) {
		xiaomi := testOverflowLengthStopMessage("", "xiaomi", "mimo-v2.6-pro", 58, 1048512, 0, 0)
		if !ai.IsContextOverflow(xiaomi, 1048576) {
			t.Fatal("expected Xiaomi zero-output length stop with full context to be overflow")
		}

		recoverable := testOverflowLengthStopMessage(ai.KnownApiOpenaiResponses, "openai", "gpt-5.6-sol", 3, 253584, 25554, 16)
		if !ai.IsRecoverableLength(recoverable, 128000) {
			t.Fatal("expected length stop below desired output limit to be recoverable")
		}

		reached := testOverflowLengthStopMessage("", "", "", 4062, 0, 0, 1024)
		if ai.IsRecoverableLength(reached, 1024) {
			t.Fatal("expected length stop reaching desired output limit not to be recoverable")
		}

		zeroOut := testOverflowLengthStopMessage("", "", "", 100, 0, 0, 0)
		if !ai.IsRecoverableLength(zeroOut, 128000) {
			t.Fatal("expected zero-output length stop to be recoverable")
		}
		if ai.IsContextOverflow(zeroOut, 200000) {
			t.Fatal("expected zero-output length stop far below context not to be overflow")
		}

		normal := testOverflowLengthStopMessage("", "", "", 1000, 0, 0, 4096)
		if ai.IsContextOverflow(normal, 200000) {
			t.Fatal("expected normal length stop not to be overflow")
		}
	})
}
