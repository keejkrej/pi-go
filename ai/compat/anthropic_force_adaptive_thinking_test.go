// Ported from packages/ai/test/anthropic-force-adaptive-thinking.test.ts (pi v1.5.2).

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

func tafatMakeCustomModel(comp *ai.AnthropicMessagesCompat) *ai.Model {
	m := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "vendor--claude-opus-latest",
			Name:     "Vendor Proxy Opus Latest",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "vendor-proxy",
			BaseUrl:  "http://127.0.0.1:9",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     32000,
	}
	if comp != nil {
		m.Compat = ai.ModelCompatValue{V: comp}
	}
	return m
}

func tafatCapturePayload(t *testing.T, model *ai.Model, opts *ai.SimpleStreamOptions) *jsonx.Object {
	t.Helper()
	cp := *model
	cp.BaseUrl = "http://127.0.0.1:9"
	key := "fake-key"
	var captured *jsonx.Object
	var reqOpts ai.SimpleStreamOptions
	if opts != nil {
		reqOpts = *opts
	}
	reqOpts.ApiKey = &key
	reqOpts.OnPayload = func(payload any, _ *ai.Model) (any, error) {
		obj, _ := payload.(*jsonx.Object)
		captured = obj
		return nil, errors.New("payload captured")
	}

	hello := "Hello"
	ctx := context.Background()
	stream := compat.StreamSimple(ctx, &cp, &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hello}, Timestamp: js.DateNow()},
		},
	}, &reqOpts)
	_, _ = stream.Result(ctx)
	if captured == nil {
		t.Fatal("expected payload capture before request failure")
	}
	return captured
}

func TestAnthropicForceAdaptiveThinkingCompatOverride(t *testing.T) {
	t.Parallel()
	med := ai.ThinkingLevelMedium
	xhigh := ai.ThinkingLevelXhigh
	tTrue := true
	tFalse := false

	// 1. Legacy thinking payload for custom model ids by default
	p1 := tafatCapturePayload(t, tafatMakeCustomModel(nil), &ai.SimpleStreamOptions{Reasoning: &med})
	th1, _ := p1.Get("thinking")
	if thObj, ok := th1.(*jsonx.Object); !ok {
		t.Fatalf("p1.thinking = %T", th1)
	} else if typ, _ := thObj.Get("type"); typ != "enabled" {
		t.Fatalf("p1.thinking.type = %v, want enabled", typ)
	}
	if _, hasOut := p1.Get("output_config"); hasOut {
		t.Fatal("p1 unexpected output_config")
	}

	// 2. Adaptive thinking when compat.forceAdaptiveThinking is true
	p2 := tafatCapturePayload(t, tafatMakeCustomModel(&ai.AnthropicMessagesCompat{ForceAdaptiveThinking: &tTrue}), &ai.SimpleStreamOptions{Reasoning: &med})
	th2, _ := p2.Get("thinking")
	th2Obj, ok := th2.(*jsonx.Object)
	if !ok {
		t.Fatalf("p2.thinking = %T", th2)
	}
	if typ, _ := th2Obj.Get("type"); typ != "adaptive" {
		t.Fatalf("p2.thinking.type = %v, want adaptive", typ)
	}
	if disp, _ := th2Obj.Get("display"); disp != "summarized" {
		t.Fatalf("p2.thinking.display = %v, want summarized", disp)
	}
	oc2, _ := p2.Get("output_config")
	if ocObj, ok := oc2.(*jsonx.Object); !ok {
		t.Fatalf("p2.output_config = %T", oc2)
	} else if eff, _ := ocObj.Get("effort"); eff != "medium" {
		t.Fatalf("p2.output_config.effort = %v, want medium", eff)
	}

	// 3. Claude Fable 5 native xhigh effort
	fable5 := compat.GetModel("anthropic", "claude-fable-5")
	if fable5 == nil {
		t.Fatal("missing anthropic/claude-fable-5")
	}
	p3 := tafatCapturePayload(t, fable5, &ai.SimpleStreamOptions{Reasoning: &xhigh})
	th3, _ := p3.Get("thinking")
	if th3Obj, ok := th3.(*jsonx.Object); !ok {
		t.Fatalf("p3.thinking = %T", th3)
	} else if typ, _ := th3Obj.Get("type"); typ != "adaptive" {
		t.Fatalf("p3.thinking.type = %v", typ)
	} else if disp, _ := th3Obj.Get("display"); disp != "summarized" {
		t.Fatalf("p3.thinking.display = %v", disp)
	}
	oc3, _ := p3.Get("output_config")
	if oc3Obj, ok := oc3.(*jsonx.Object); !ok {
		t.Fatalf("p3.output_config = %T", oc3)
	} else if eff, _ := oc3Obj.Get("effort"); eff != "xhigh" {
		t.Fatalf("p3.output_config.effort = %v", eff)
	}

	// 4. Built-in adaptive model opt-out with forceAdaptiveThinking: false
	opus48 := *compat.GetModel("anthropic", "claude-opus-4-8")
	opus48.Compat = ai.ModelCompatValue{V: &ai.AnthropicMessagesCompat{ForceAdaptiveThinking: &tFalse}}
	p4 := tafatCapturePayload(t, &opus48, &ai.SimpleStreamOptions{Reasoning: &med})
	th4, _ := p4.Get("thinking")
	if th4Obj, ok := th4.(*jsonx.Object); !ok {
		t.Fatalf("p4.thinking = %T", th4)
	} else if typ, _ := th4Obj.Get("type"); typ != "enabled" {
		t.Fatalf("p4.thinking.type = %v, want enabled", typ)
	}
	if _, hasOut := p4.Get("output_config"); hasOut {
		t.Fatal("p4 unexpected output_config")
	}

	// 5. thinking.type=disabled when reasoning is off regardless of override
	p5 := tafatCapturePayload(t, tafatMakeCustomModel(&ai.AnthropicMessagesCompat{ForceAdaptiveThinking: &tTrue}), nil)
	th5, _ := p5.Get("thinking")
	if th5Obj, ok := th5.(*jsonx.Object); !ok {
		t.Fatalf("p5.thinking = %T", th5)
	} else if typ, _ := th5Obj.Get("type"); typ != "disabled" {
		t.Fatalf("p5.thinking.type = %v, want disabled", typ)
	}
	if _, hasOut := p5.Get("output_config"); hasOut {
		t.Fatal("p5 unexpected output_config")
	}
}
