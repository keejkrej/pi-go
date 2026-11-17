// Ported from packages/ai/test/anthropic-thinking-disable.test.ts (pi v1.5.2).

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

var tatdErrPayloadCaptured = errors.New("payload captured")

func tatdPtr[T any](v T) *T { return &v }

func tatdCapturePayload(t *testing.T, modelId string, options *ai.SimpleStreamOptions) *jsonx.Object {
	t.Helper()
	base := compat.GetModel("anthropic", modelId)
	if base == nil {
		t.Fatalf("missing anthropic/%s", modelId)
	}
	m := *base
	m.BaseUrl = "http://127.0.0.1:9"

	var opts ai.SimpleStreamOptions
	if options != nil {
		opts = *options
	}
	apiKey := "fake-key"
	opts.ApiKey = &apiKey

	var captured *jsonx.Object
	opts.OnPayload = func(payload any, _ *ai.Model) (any, error) {
		if obj, ok := payload.(*jsonx.Object); ok {
			captured = obj
		}
		return nil, tatdErrPayloadCaptured
	}

	reqCtx := &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: tatdPtr("Hello")},
				Timestamp: js.DateNow(),
			},
		},
	}
	_, _ = compat.CompleteSimple(context.Background(), &m, reqCtx, &opts)
	if captured == nil {
		t.Fatalf("expected payload to be captured before request failure for %s", modelId)
	}
	return captured
}

func TestAnthropicThinkingDisable_Payloads(t *testing.T) {
	// Note: live E2E test with ANTHROPIC_API_KEY is skipped in offline unit suite per docs/porting.md §3.4.

	t.Run("budget-based reasoning off sends disabled", func(t *testing.T) {
		payload := tatdCapturePayload(t, "claude-sonnet-4-5", nil)
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "disabled" {
			t.Fatalf("thinking.type = %v, want disabled", typ)
		}
		if _, has := payload.Get("output_config"); has {
			t.Fatal("expected output_config to be omitted")
		}
	})

	t.Run("adaptive reasoning off sends disabled", func(t *testing.T) {
		payload := tatdCapturePayload(t, "claude-opus-4-6", nil)
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "disabled" {
			t.Fatalf("thinking.type = %v, want disabled", typ)
		}
		if _, has := payload.Get("output_config"); has {
			t.Fatal("expected output_config to be omitted")
		}
	})

	t.Run("claude-opus-4-8 thinking off sends disabled", func(t *testing.T) {
		payload := tatdCapturePayload(t, "claude-opus-4-8", nil)
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "disabled" {
			t.Fatalf("thinking.type = %v, want disabled", typ)
		}
		if _, has := payload.Get("output_config"); has {
			t.Fatal("expected output_config to be omitted")
		}
	})

	t.Run("claude-fable-5 omits thinking when off", func(t *testing.T) {
		payload := tatdCapturePayload(t, "claude-fable-5", nil)
		if _, has := payload.Get("thinking"); has {
			t.Fatal("expected thinking to be omitted for claude-fable-5")
		}
		if _, has := payload.Get("output_config"); has {
			t.Fatal("expected output_config to be omitted for claude-fable-5")
		}
	})

	t.Run("claude-opus-4-8 adaptive summarized high", func(t *testing.T) {
		high := ai.ThinkingLevelHigh
		payload := tatdCapturePayload(t, "claude-opus-4-8", &ai.SimpleStreamOptions{Reasoning: &high})
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "adaptive" {
			t.Fatalf("thinking.type = %v, want adaptive", typ)
		}
		if disp, _ := th.Get("display"); disp != "summarized" {
			t.Fatalf("thinking.display = %v, want summarized", disp)
		}
		ocRaw, _ := payload.Get("output_config")
		oc, _ := ocRaw.(*jsonx.Object)
		if oc == nil {
			t.Fatalf("expected output_config object, got %T", ocRaw)
		}
		if effort, _ := oc.Get("effort"); effort != "high" {
			t.Fatalf("output_config.effort = %v, want high", effort)
		}
	})

	t.Run("claude-sonnet-5 adaptive summarized high", func(t *testing.T) {
		high := ai.ThinkingLevelHigh
		payload := tatdCapturePayload(t, "claude-sonnet-5", &ai.SimpleStreamOptions{Reasoning: &high})
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "adaptive" {
			t.Fatalf("thinking.type = %v, want adaptive", typ)
		}
		if disp, _ := th.Get("display"); disp != "summarized" {
			t.Fatalf("thinking.display = %v, want summarized", disp)
		}
		ocRaw, _ := payload.Get("output_config")
		oc, _ := ocRaw.(*jsonx.Object)
		if oc == nil {
			t.Fatalf("expected output_config object, got %T", ocRaw)
		}
		if effort, _ := oc.Get("effort"); effort != "high" {
			t.Fatalf("output_config.effort = %v, want high", effort)
		}
	})

	t.Run("claude-opus-4-8 xhigh maps to effort=xhigh", func(t *testing.T) {
		xhigh := ai.ThinkingLevelXhigh
		payload := tatdCapturePayload(t, "claude-opus-4-8", &ai.SimpleStreamOptions{Reasoning: &xhigh})
		thRaw, _ := payload.Get("thinking")
		th, _ := thRaw.(*jsonx.Object)
		if th == nil {
			t.Fatalf("expected thinking object, got %T", thRaw)
		}
		if typ, _ := th.Get("type"); typ != "adaptive" {
			t.Fatalf("thinking.type = %v, want adaptive", typ)
		}
		if disp, _ := th.Get("display"); disp != "summarized" {
			t.Fatalf("thinking.display = %v, want summarized", disp)
		}
		ocRaw, _ := payload.Get("output_config")
		oc, _ := ocRaw.(*jsonx.Object)
		if oc == nil {
			t.Fatalf("expected output_config object, got %T", ocRaw)
		}
		if effort, _ := oc.Get("effort"); effort != "xhigh" {
			t.Fatalf("output_config.effort = %v, want xhigh", effort)
		}
	})
}
