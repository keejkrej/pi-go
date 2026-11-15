// Ported from packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func taetscMakeModel(allowEmptySignature *bool) *ai.Model {
	m := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "mimo-v2.6-pro",
			Name:     "MiMo-V2.5-Pro",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "xiaomi-token-plan-ams",
			BaseUrl:  "http://127.0.0.1:9/anthropic",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 1048576,
		MaxTokens:     1024,
	}
	if allowEmptySignature != nil {
		m.Compat = ai.ModelCompatValue{V: &ai.AnthropicMessagesCompat{AllowEmptySignature: allowEmptySignature}}
	}
	return m
}

func taetscMakeContext(thinkingSignature, thinking, provider, model string, extra ...ai.Content) *ai.Context {
	first, second := "first", "second"
	blocks := ai.ContentList{
		&ai.ThinkingContent{
			Type:              "thinking",
			Thinking:          thinking,
			ThinkingSignature: &thinkingSignature,
		},
	}
	blocks = append(blocks, extra...)
	assistant := &ai.AssistantMessage{
		Role:       "assistant",
		Content:    blocks,
		Provider:   ai.ProviderId(provider),
		Api:        ai.KnownApiAnthropicMessages,
		Model:      model,
		Timestamp:  js.DateNow(),
		StopReason: ai.StopReasonStop,
	}
	return &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &first}, Timestamp: js.DateNow()},
			assistant,
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &second}, Timestamp: js.DateNow()},
		},
	}
}

func taetscCapturePayload(t *testing.T, model *ai.Model, c *ai.Context) *jsonx.Object {
	t.Helper()
	var captured *jsonx.Object
	key := "fake-key"
	ctx := context.Background()
	stream := compat.StreamSimple(ctx, model, c, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: &key,
				OnPayload: func(payload any, _ *ai.Model) (any, error) {
					obj, _ := payload.(*jsonx.Object)
					captured = obj
					return nil, errors.New("payload captured")
				},
			},
		},
	})
	_, _ = stream.Result(ctx)
	if captured == nil {
		t.Fatal("expected payload capture before request")
	}
	return captured
}

func taetscAssistantContent(t *testing.T, payload *jsonx.Object) []*jsonx.Object {
	t.Helper()
	rawMsgs, ok := payload.Get("messages")
	if !ok {
		t.Fatal("payload missing messages")
	}
	msgs, ok := rawMsgs.([]any)
	if !ok {
		t.Fatalf("messages = %T, want []any", rawMsgs)
	}
	for _, item := range msgs {
		msgObj, ok := item.(*jsonx.Object)
		if !ok {
			continue
		}
		if role, _ := msgObj.Get("role"); role == "assistant" {
			rawContent, _ := msgObj.Get("content")
			contentSlice, ok := rawContent.([]any)
			if !ok {
				t.Fatalf("assistant.content = %T", rawContent)
			}
			out := make([]*jsonx.Object, len(contentSlice))
			for i, b := range contentSlice {
				out[i], _ = b.(*jsonx.Object)
			}
			return out
		}
	}
	t.Fatal("assistant message not found in payload")
	return nil
}

func TestAnthropicEmptyThinkingSignatureCompat(t *testing.T) {
	t.Parallel()
	// 1. Converts empty-signature thinking to text by default
	p1 := taetscCapturePayload(t, taetscMakeModel(nil), taetscMakeContext("", "internal reasoning", "xiaomi-token-plan-ams", "mimo-v2.6-pro"))
	c1 := taetscAssistantContent(t, p1)
	if len(c1) != 1 {
		t.Fatalf("len(c1) = %d, want 1", len(c1))
	}
	if typ, _ := c1[0].Get("type"); typ != "text" {
		t.Fatalf("c1[0].type = %v, want text", typ)
	}
	if txt, _ := c1[0].Get("text"); txt != "internal reasoning" {
		t.Fatalf("c1[0].text = %v", txt)
	}

	// 2. Preserves empty thinking text when the signature is present
	p2 := taetscCapturePayload(t, taetscMakeModel(nil), taetscMakeContext("signed-thinking", "", "xiaomi-token-plan-ams", "mimo-v2.6-pro"))
	c2 := taetscAssistantContent(t, p2)
	if len(c2) != 1 {
		t.Fatalf("len(c2) = %d, want 1", len(c2))
	}
	if typ, _ := c2[0].Get("type"); typ != "thinking" {
		t.Fatalf("c2[0].type = %v, want thinking", typ)
	}
	if th, _ := c2[0].Get("thinking"); th != "" {
		t.Fatalf("c2[0].thinking = %v, want empty", th)
	}
	if sig, _ := c2[0].Get("signature"); sig != "signed-thinking" {
		t.Fatalf("c2[0].signature = %v", sig)
	}

	// 3. Preserves empty-signature thinking when allowEmptySignature is enabled
	allowTrue := true
	p3 := taetscCapturePayload(t, taetscMakeModel(&allowTrue), taetscMakeContext(" ", "internal reasoning", "xiaomi-token-plan-ams", "mimo-v2.6-pro"))
	c3 := taetscAssistantContent(t, p3)
	if len(c3) != 1 {
		t.Fatalf("len(c3) = %d, want 1", len(c3))
	}
	if typ, _ := c3[0].Get("type"); typ != "thinking" {
		t.Fatalf("c3[0].type = %v, want thinking", typ)
	}
	if th, _ := c3[0].Get("thinking"); th != "internal reasoning" {
		t.Fatalf("c3[0].thinking = %v", th)
	}
	if sig, _ := c3[0].Get("signature"); sig != "" {
		t.Fatalf("c3[0].signature = %v, want empty", sig)
	}

	// 4. Every Vercel AI Gateway model allows empty thinking signatures (#9676)
	vagModels := compat.GetModels("vercel-ai-gateway")
	if len(vagModels) == 0 {
		t.Fatal("expected vercel-ai-gateway models")
	}
	for _, m := range vagModels {
		var allow *bool
		switch v := m.Compat.V.(type) {
		case *ai.AnthropicMessagesCompat:
			allow = v.AllowEmptySignature
		case *ai.OpenAICompletionsCompat:
			_ = v
		}
		// Vercel AI Gateway uses Anthropic/OpenAI models; check allowEmptySignature via AnthropicMessagesCompat if applicable,
		// or decode compat JSON if needed.
		raw, err := jsonx.Marshal(m.Compat)
		if err != nil {
			t.Fatalf("marshal compat for %s: %v", m.Id, err)
		}
		parsed, err := jsonx.Parse(raw)
		if err != nil {
			t.Fatalf("parse compat for %s: %v", m.Id, err)
		}
		obj, _ := parsed.(*jsonx.Object)
		if obj == nil {
			t.Fatalf("vercel-ai-gateway/%s missing compat (%v)", m.Id, allow)
		}
		if v, ok := obj.Get("allowEmptySignature"); !ok || v != true {
			t.Fatalf("vercel-ai-gateway/%s compat.allowEmptySignature = %v (ok=%v)", m.Id, v, ok)
		}
	}

	// 5. Fireworks unsigned thinking survival (#9323)
	fwIds := []string{
		"accounts/fireworks/models/deepseek-v4p1-flash",
		"accounts/fireworks/models/qwen3p8-max",
		"accounts/fireworks/models/qwen3p8-2p4t-a95b",
		"accounts/fireworks/models/ember-1",
	}
	for _, modelId := range fwIds {
		m := compat.GetModel("fireworks", modelId)
		if m == nil {
			t.Fatalf("missing fireworks/%s", modelId)
		}
		ac, _ := m.Compat.V.(*ai.AnthropicMessagesCompat)
		if ac == nil || ac.AllowEmptySignature == nil || !*ac.AllowEmptySignature {
			t.Fatalf("fireworks/%s expected allowEmptySignature=true, got %+v", modelId, m.Compat.V)
		}
		reqCtx := taetscMakeContext("", "internal reasoning", "fireworks", modelId, &ai.TextContent{Type: "text", Text: "answer"})
		payload := taetscCapturePayload(t, m, reqCtx)
		content := taetscAssistantContent(t, payload)
		if len(content) != 2 {
			t.Fatalf("fireworks/%s content len = %d, want 2", modelId, len(content))
		}
		b0, b1 := content[0], content[1]
		if typ, _ := b0.Get("type"); typ != "thinking" {
			t.Fatalf("b0.type = %v", typ)
		}
		if th, _ := b0.Get("thinking"); th != "internal reasoning" {
			t.Fatalf("b0.thinking = %v", th)
		}
		if sig, _ := b0.Get("signature"); sig != "" {
			t.Fatalf("b0.signature = %v", sig)
		}
		if typ, _ := b1.Get("type"); typ != "text" {
			t.Fatalf("b1.type = %v", typ)
		}
		if txt, _ := b1.Get("text"); txt != "answer" {
			t.Fatalf("b1.text = %v", txt)
		}
	}
}
