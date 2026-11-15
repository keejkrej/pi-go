// Ported from packages/ai/test/openai-completions-thinking-token-budget.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

var tocttbErrCaptured = errors.New("payload captured")

func tocttbPtr[T any](v T) *T { return &v }

func tocttbVllmModel(compatCfg *ai.OpenAICompletionsCompat) *ai.Model {
	if compatCfg == nil {
		zai := ai.ThinkingFormatZai
		sup := true
		compatCfg = &ai.OpenAICompletionsCompat{
			ThinkingFormat:              &zai,
			SupportsThinkingTokenBudget: &sup,
		}
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "zai-org/glm-5.2",
			Name:     "GLM 5.2 (local vLLM)",
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: "local-vllm",
			BaseUrl:  "http://127.0.0.1:9/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 262144,
		MaxTokens:     16384,
		Compat:        ai.ModelCompatValue{V: compatCfg},
	}
}

func tocttbCapture(t *testing.T, model *ai.Model, opts *ai.SimpleStreamOptions) *jsonx.Object {
	t.Helper()
	var cp ai.SimpleStreamOptions
	if opts != nil {
		cp = *opts
	}
	apiKey := "test"
	cp.ApiKey = &apiKey
	var captured *jsonx.Object
	cp.OnPayload = func(payload any, _ *ai.Model) (any, error) {
		if obj, ok := payload.(*jsonx.Object); ok {
			captured = obj
		}
		return nil, tocttbErrCaptured
	}
	reqCtx := &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocttbPtr("Hi")}, Timestamp: js.DateNow()},
		},
	}
	_, _ = compat.CompleteSimple(context.Background(), model, reqCtx, &cp)
	if captured == nil {
		t.Fatal("expected captured payload")
	}
	return captured
}

func tocttbInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	default:
		return 0, false
	}
}

func TestOpenAICompletionsThinkingTokenBudget(t *testing.T) {
	t.Run("sends the configured budget for the requested level", func(t *testing.T) {
		med := ai.ThinkingLevelMedium
		b := 4096
		p := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{
			Reasoning:       &med,
			ThinkingBudgets: &ai.ThinkingBudgets{Medium: &b},
		})
		v, ok := p.Get("thinking_token_budget")
		if n, _ := tocttbInt(v); !ok || n != 4096 {
			t.Fatalf("thinking_token_budget = %v (ok=%v), want 4096", v, ok)
		}
	})

	t.Run("omits the budget when neither the field nor the alias is set", func(t *testing.T) {
		zai := ai.ThinkingFormatZai
		med := ai.ThinkingLevelMedium
		b := 4096
		p := tocttbCapture(t, tocttbVllmModel(&ai.OpenAICompletionsCompat{ThinkingFormat: &zai}), &ai.SimpleStreamOptions{
			Reasoning:       &med,
			ThinkingBudgets: &ai.ThinkingBudgets{Medium: &b},
		})
		for _, k := range []string{"thinking_token_budget", "thinking_budget", "thinking_budget_tokens"} {
			if _, has := p.Get(k); has {
				t.Fatalf("unexpected %s in payload", k)
			}
		}
	})

	t.Run("omits the budget when thinking is off", func(t *testing.T) {
		b := 8192
		p := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{
			ThinkingBudgets: &ai.ThinkingBudgets{High: &b},
		})
		if _, has := p.Get("thinking_token_budget"); has {
			t.Fatal("unexpected thinking_token_budget when reasoning off")
		}
	})

	t.Run("clamps xhigh and max to the high budget", func(t *testing.T) {
		b := 8192
		xhigh := ai.ThinkingLevelXhigh
		maxLevel := ai.ThinkingLevelMax
		px := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{
			Reasoning:       &xhigh,
			ThinkingBudgets: &ai.ThinkingBudgets{High: &b},
		})
		pm := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{
			Reasoning:       &maxLevel,
			ThinkingBudgets: &ai.ThinkingBudgets{High: &b},
		})
		vx, _ := px.Get("thinking_token_budget")
		vm, _ := pm.Get("thinking_token_budget")
		if nx, _ := tocttbInt(vx); nx != 8192 {
			t.Fatalf("xhigh budget = %v, want 8192", vx)
		}
		if nm, _ := tocttbInt(vm); nm != 8192 {
			t.Fatalf("max budget = %v, want 8192", vm)
		}
	})

	t.Run("leaves room for the answer when the budget meets the response ceiling", func(t *testing.T) {
		high := ai.ThinkingLevelHigh
		p := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{Reasoning: &high})
		v, _ := p.Get("thinking_token_budget")
		if n, _ := tocttbInt(v); n != 16384-1024 {
			t.Fatalf("thinking_token_budget = %v, want %d", v, 16384-1024)
		}
	})

	t.Run("uses the caller max_tokens as the ceiling when it is lower than the model cap", func(t *testing.T) {
		high := ai.ThinkingLevelHigh
		b := 8192
		maxTok := 4096
		p := tocttbCapture(t, tocttbVllmModel(nil), &ai.SimpleStreamOptions{
			StreamOptions:   ai.StreamOptions{MaxTokens: &maxTok},
			Reasoning:       &high,
			ThinkingBudgets: &ai.ThinkingBudgets{High: &b},
		})
		v, _ := p.Get("thinking_token_budget")
		if n, _ := tocttbInt(v); n != 4096-1024 {
			t.Fatalf("thinking_token_budget = %v, want %d", v, 4096-1024)
		}
	})

	for _, field := range []ai.ThinkingTokenBudgetField{
		ai.ThinkingTokenBudgetFieldThinkingBudget,
		ai.ThinkingTokenBudgetFieldThinkingBudgetTokens,
	} {
		t.Run("sends "+string(field)+" when thinkingTokenBudgetField is set", func(t *testing.T) {
			qwen := ai.ThinkingFormatQwen
			f := field
			med := ai.ThinkingLevelMedium
			b := 4096
			p := tocttbCapture(t, tocttbVllmModel(&ai.OpenAICompletionsCompat{
				ThinkingFormat:           &qwen,
				ThinkingTokenBudgetField: &f,
			}), &ai.SimpleStreamOptions{
				Reasoning:       &med,
				ThinkingBudgets: &ai.ThinkingBudgets{Medium: &b},
			})
			v, _ := p.Get(string(field))
			if n, _ := tocttbInt(v); n != 4096 {
				t.Fatalf("%s = %v, want 4096", field, v)
			}
			if _, has := p.Get("thinking_token_budget"); has {
				t.Fatal("unexpected thinking_token_budget")
			}
		})
	}

	t.Run("lets thinkingTokenBudgetField win over the boolean alias", func(t *testing.T) {
		zai := ai.ThinkingFormatZai
		sup := true
		f := ai.ThinkingTokenBudgetFieldThinkingBudget
		med := ai.ThinkingLevelMedium
		b := 4096
		p := tocttbCapture(t, tocttbVllmModel(&ai.OpenAICompletionsCompat{
			ThinkingFormat:              &zai,
			SupportsThinkingTokenBudget: &sup,
			ThinkingTokenBudgetField:    &f,
		}), &ai.SimpleStreamOptions{
			Reasoning:       &med,
			ThinkingBudgets: &ai.ThinkingBudgets{Medium: &b},
		})
		v, _ := p.Get("thinking_budget")
		if n, _ := tocttbInt(v); n != 4096 {
			t.Fatalf("thinking_budget = %v, want 4096", v)
		}
		if _, has := p.Get("thinking_token_budget"); has {
			t.Fatal("unexpected thinking_token_budget")
		}
	})

	t.Run("puts the clamped budget in chat_template_kwargs when $var is thinking.budget", func(t *testing.T) {
		ct := ai.ThinkingFormatChatTemplate
		kwargs := omap.NewMap[string, ai.ChatTemplateKwargValue]()
		kwargs.Set("enable_thinking", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingEnabled})
		kwargs.Set("thinking_budget", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingBudget})
		high := ai.ThinkingLevelHigh
		p := tocttbCapture(t, tocttbVllmModel(&ai.OpenAICompletionsCompat{
			ThinkingFormat:     &ct,
			ChatTemplateKwargs: kwargs,
		}), &ai.SimpleStreamOptions{Reasoning: &high})
		ctkRaw, _ := p.Get("chat_template_kwargs")
		ctk, _ := ctkRaw.(*jsonx.Object)
		if ctk == nil {
			t.Fatalf("expected chat_template_kwargs object, got %T", ctkRaw)
		}
		if en, _ := ctk.Get("enable_thinking"); en != true {
			t.Fatalf("enable_thinking = %v, want true", en)
		}
		tb, _ := ctk.Get("thinking_budget")
		if n, _ := tocttbInt(tb); n != 16384-1024 {
			t.Fatalf("thinking_budget = %v, want %d", tb, 16384-1024)
		}
		if _, has := p.Get("thinking_token_budget"); has {
			t.Fatal("unexpected thinking_token_budget")
		}
	})

	t.Run("omits thinking.budget from chat_template_kwargs when thinking is off", func(t *testing.T) {
		ct := ai.ThinkingFormatChatTemplate
		kwargs := omap.NewMap[string, ai.ChatTemplateKwargValue]()
		kwargs.Set("enable_thinking", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingEnabled})
		kwargs.Set("thinking_budget", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingBudget})
		p := tocttbCapture(t, tocttbVllmModel(&ai.OpenAICompletionsCompat{
			ThinkingFormat:     &ct,
			ChatTemplateKwargs: kwargs,
		}), &ai.SimpleStreamOptions{})
		ctkRaw, _ := p.Get("chat_template_kwargs")
		ctk, _ := ctkRaw.(*jsonx.Object)
		if ctk == nil {
			t.Fatalf("expected chat_template_kwargs object, got %T", ctkRaw)
		}
		if en, _ := ctk.Get("enable_thinking"); en != false {
			t.Fatalf("enable_thinking = %v, want false", en)
		}
		if _, has := ctk.Get("thinking_budget"); has {
			t.Fatal("unexpected thinking_budget when thinking is off")
		}
	})
}
