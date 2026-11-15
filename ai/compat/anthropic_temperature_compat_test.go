// Ported from packages/ai/test/anthropic-temperature-compat.test.ts (pi v1.5.2).

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

func tatcMakeCustomModel(comp *ai.AnthropicMessagesCompat) *ai.Model {
	m := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "vendor--claude-opus-4-7",
			Name:     "Vendor Proxy Opus 4.7",
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

func tatcCapturePayload(t *testing.T, model *ai.Model, temp float64) *jsonx.Object {
	t.Helper()
	cp := *model
	cp.BaseUrl = "http://127.0.0.1:9"
	key := "fake-key"
	var captured *jsonx.Object
	hello := "Hello"
	ctx := context.Background()
	stream := compat.StreamSimple(ctx, &cp, &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hello}, Timestamp: js.DateNow()},
		},
	}, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: &key,
				OnPayload: func(payload any, _ *ai.Model) (any, error) {
					obj, _ := payload.(*jsonx.Object)
					captured = obj
					return nil, errors.New("payload captured")
				},
			},
			Temperature: &temp,
		},
	})
	_, _ = stream.Result(ctx)
	if captured == nil {
		t.Fatal("expected payload capture before request failure")
	}
	return captured
}

func TestAnthropicTemperatureCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		model *ai.Model
		temp  float64
		want  *float64
		label string
	}{
		{compat.GetModel("anthropic", "claude-opus-4-7"), 0, nil, "opus-4-7 temp 0"},
		{compat.GetModel("anthropic", "claude-opus-4-8"), 0, nil, "opus-4-8 temp 0"},
		{compat.GetModel("anthropic", "claude-opus-4-7"), 1, nil, "opus-4-7 temp 1"},
		{compat.GetModel("anthropic", "claude-sonnet-5"), 0, nil, "sonnet-5 temp 0"},
		{compat.GetModel("anthropic", "claude-opus-4-6"), 0, func() *float64 { v := 0.0; return &v }(), "opus-4-6 temp 0"},
		{compat.GetModel("anthropic", "claude-sonnet-4-6"), 0, func() *float64 { v := 0.0; return &v }(), "sonnet-4-6 temp 0"},
		{
			tatcMakeCustomModel(&ai.AnthropicMessagesCompat{SupportsTemperature: func() *bool { b := false; return &b }()}),
			0,
			nil,
			"custom supportsTemperature=false",
		},
	} {
		if tc.model == nil {
			t.Fatalf("missing model for %s", tc.label)
		}
		payload := tatcCapturePayload(t, tc.model, tc.temp)
		got, ok := payload.Get("temperature")
		if tc.want == nil {
			if ok {
				t.Fatalf("%s: expected omitted temperature, got %v", tc.label, got)
			}
		} else {
			if !ok {
				t.Fatalf("%s: expected temperature %v, got omitted", tc.label, *tc.want)
			}
			f, fOk := got.(float64)
			if !fOk || f != *tc.want {
				t.Fatalf("%s: temperature = %v (%T), want %v", tc.label, got, got, *tc.want)
			}
		}
	}
}
