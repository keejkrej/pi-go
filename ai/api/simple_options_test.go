// Ported from packages/ai/test/clamp-max-tokens.test.ts and simple-options behavior (pi v1.5.2).

package api

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const tsoWindow = 1_048_576

func tsoModelWith(maxTokens int, contextWindow ...int) *ai.Model {
	cw := tsoWindow
	if len(contextWindow) > 0 {
		cw = contextWindow[0]
	}
	return &ai.Model{
		Id:            "gpt-4o-mini",
		Name:          "GPT-4o mini",
		Api:           string(ai.KnownApiOpenaiCompletions),
		Provider:      "openai",
		BaseUrl:       "https://api.openai.com/v1",
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: cw,
		MaxTokens:     maxTokens,
	}
}

func tsoContextWithInput(inputTokens int) *ai.TranscriptContext {
	assistant := &ai.AssistantMessage{
		Role:     "assistant",
		Content:  ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
		Api:      ai.KnownApiOpenaiCompletions,
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Usage: ai.Usage{
			Input:       inputTokens,
			TotalTokens: inputTokens,
		},
		StopReason: ai.StopReasonStop,
		Timestamp:  2,
	}
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "hi"}}},
				Timestamp: 1,
			},
			assistant,
		},
	})
}

func TestClampMaxTokensToContext(t *testing.T) {
	t.Run("leaves 15% of the input unused so an OpenRouter overcount still fits (#10497)", func(t *testing.T) {
		maxTokens := ClampMaxTokensToContext(tsoModelWith(943_718), tsoContextWithInput(220_815), 943_718)
		want := tsoWindow - 220_815 - 33_123
		if maxTokens != want {
			t.Fatalf("got %d, want %d", maxTokens, want)
		}
		if 229_747+maxTokens > tsoWindow {
			t.Fatalf("229747 + %d = %d exceeds window %d", maxTokens, 229_747+maxTokens, tsoWindow)
		}
	})

	t.Run("leaves at least 4096 tokens unused for small inputs", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(tsoWindow), tsoContextWithInput(10_000), tsoWindow)
		want := tsoWindow - 10_000 - 4096
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("keeps the model output limit when it already fits", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(64_000), tsoContextWithInput(100_000), 64_000)
		if got != 64_000 {
			t.Fatalf("got %d, want 64000", got)
		}
	})

	t.Run("sends up to 32768 tokens when the proportional margin leaves no room", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(943_718), tsoContextWithInput(912_000), 943_718)
		if got != 32_768 {
			t.Fatalf("got %d, want 32768", got)
		}
	})

	t.Run("uses the real room minus 4096 when less than 32768 tokens fit", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(943_718), tsoContextWithInput(1_030_000), 943_718)
		want := tsoWindow - 1_030_000 - 4096
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("keeps only the fixed 4096 margin for small windows", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(32_768, 32_768), tsoContextWithInput(20_000), 32_768)
		want := 32_768 - 20_000 - 4096
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("sends 1 token when the input already exceeds the window", func(t *testing.T) {
		got := ClampMaxTokensToContext(tsoModelWith(943_718), tsoContextWithInput(1_050_000), 943_718)
		if got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})
}

func TestSimpleOptionsHelpers(t *testing.T) {
	t.Run("ClampReasoning clamps xhigh and max to high and leaves lower levels unchanged", func(t *testing.T) {
		xhigh := ai.ThinkingLevelXhigh
		maxLvl := ai.ThinkingLevelMax
		low := ai.ThinkingLevelLow

		if ClampReasoning(nil) != nil {
			t.Fatalf("expected nil for nil input")
		}
		if got := ClampReasoning(&xhigh); got == nil || *got != ai.ThinkingLevelHigh {
			t.Fatalf("expected xhigh -> high, got %v", got)
		}
		if got := ClampReasoning(&maxLvl); got == nil || *got != ai.ThinkingLevelHigh {
			t.Fatalf("expected max -> high, got %v", got)
		}
		if got := ClampReasoning(&low); got == nil || *got != ai.ThinkingLevelLow {
			t.Fatalf("expected low -> low, got %v", got)
		}
	})

	t.Run("AdjustMaxTokensForThinking adds budget and preserves headroom", func(t *testing.T) {
		base := 1024
		maxTokens, thinkingBudget := AdjustMaxTokensForThinking(&base, 16384, ai.ThinkingLevelLow, nil)
		if thinkingBudget != 2048 {
			t.Fatalf("expected thinkingBudget=2048, got %d", thinkingBudget)
		}
		if maxTokens != 3072 {
			t.Fatalf("expected maxTokens=3072, got %d", maxTokens)
		}

		baseMedium := 4096
		maxMed, budgetMed := AdjustMaxTokensForThinking(&baseMedium, 8192, ai.ThinkingLevelMedium, nil)
		if maxMed != 8192 || budgetMed != 8192-1024 {
			t.Fatalf("expected medium clamp (max=8192, budget=7168), got max=%d budget=%d", maxMed, budgetMed)
		}

		baseSmall := 1024
		maxSmall, budgetSmall := AdjustMaxTokensForThinking(&baseSmall, 4096, ai.ThinkingLevelHigh, &ai.ThinkingBudgets{
			High: func() *int { v := 8192; return &v }(),
		})
		if maxSmall != 4096 || budgetSmall != 4096-1024 {
			t.Fatalf("expected headroom clamp (max=4096, budget=3072), got max=%d budget=%d", maxSmall, budgetSmall)
		}
	})

	t.Run("ResolveSamplingParams merges model and level defaults with request overrides", func(t *testing.T) {
		modelDefault := jsonx.NewObject()
		modelDefault.Set("temperature", 0.5)
		lowSampling := jsonx.NewObject()
		lowSampling.Set("top_p", 0.9)
		lowSampling.Set("min_p", 0.05)

		m := &ai.Model{
			Reasoning:      true,
			SamplingParams: modelDefault,
			SamplingParamsByThinkingLevel: &ai.SamplingParamsByThinkingLevel{
				Low: lowSampling,
			},
		}
		reqSampling := jsonx.NewObject()
		reqSampling.Set("min_p", 0.2)
		reqSampling.Set("top_k", 40)

		merged := ResolveSamplingParams(m, ai.ModelThinkingLevelLow, reqSampling)
		if merged == nil {
			t.Fatalf("expected non-nil merged sampling params")
		}
		raw, err := jsonx.Stringify(merged)
		if err != nil {
			t.Fatalf("stringify failed: %v", err)
		}
		want := `{"temperature":0.5,"top_p":0.9,"min_p":0.2,"top_k":40}`
		if raw != want {
			t.Fatalf("got %s, want %s", raw, want)
		}
	})
}
