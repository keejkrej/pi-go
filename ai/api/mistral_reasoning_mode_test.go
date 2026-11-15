// Ported from packages/ai/test/mistral-reasoning-mode.test.ts (pi v1.5.2).

package api_test

import (
	"errors"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmrmStrPtr(s string) *string { return &s }

func tmrmModel(id string, reasoning bool, levelMap *ai.ThinkingLevelMap) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(ai.KnownApiMistralConversations),
			Provider: "mistral",
			BaseUrl:  "http://127.0.0.1:9",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:        reasoning,
		ThinkingLevelMap: levelMap,
		ContextWindow:    128000,
		MaxTokens:        16384,
	}
}

func tmrmCapturePayload(t *testing.T, model *ai.Model, opts *ai.SimpleStreamOptions) *jsonx.Object {
	t.Helper()
	var captured *jsonx.Object
	var sendOpts ai.SimpleStreamOptions
	if opts != nil {
		sendOpts = *opts
	}
	sendOpts.ApiKey = tmrmStrPtr("fake-key")
	sendOpts.OnPayload = func(payload any, m *ai.Model) (any, error) {
		captured = payload.(*jsonx.Object).Clone()
		return nil, errors.New("payload captured")
	}
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmrmStrPtr("Hello")}, Timestamp: 1},
		},
	})
	_, _ = api.StreamSimpleMistralConversations(nil, model, transcript, &sendOpts).Result(nil)
	if captured == nil {
		t.Fatalf("expected payload capture for model %s", model.Id)
	}
	return captured
}

func TestMistralReasoningModeSelection(t *testing.T) {
	noneHigh := &ai.ThinkingLevelMap{
		Off:     jsonx.Some("none"),
		Minimal: jsonx.Null[string](),
		Low:     jsonx.Null[string](),
		Medium:  jsonx.Null[string](),
		High:    jsonx.Some("high"),
		Xhigh:   jsonx.Null[string](),
		Max:     jsonx.Null[string](),
	}
	glm52 := *noneHigh
	glm52.Max = jsonx.Some("max")
	glm53 := *noneHigh
	glm53.Off = jsonx.Null[string]()
	glm53.Low = jsonx.Some("low")
	glm53.Max = jsonx.Some("max")

	// Magistral without thinkingLevelMap uses promptMode="reasoning".
	lvlMed := ai.ThinkingLevelMedium
	pMag := tmrmCapturePayload(t, tmrmModel("magistral-medium-latest", true, nil), &ai.SimpleStreamOptions{Reasoning: &lvlMed})
	if pm, _ := pMag.GetString("promptMode"); pm != "reasoning" || pMag.Has("reasoningEffort") {
		t.Fatalf("expected promptMode=reasoning without reasoningEffort, got %v", pMag)
	}
	pMagOff := tmrmCapturePayload(t, tmrmModel("magistral-medium-latest", true, nil), nil)
	if pMagOff.Has("promptMode") || pMagOff.Has("reasoningEffort") {
		t.Fatalf("expected no reasoning controls when off, got %v", pMagOff)
	}

	// Medium and GLM-5.2 use reasoningEffort and clamp unsupported levels (#8700, #9375).
	lvlLow := ai.ThinkingLevelLow
	lvlHigh := ai.ThinkingLevelHigh
	for _, tc := range []struct {
		id string
		m  *ai.ThinkingLevelMap
	}{
		{"mistral-small-2603", noneHigh},
		{"mistral-medium-latest", noneHigh},
		{"zai-glm-5-2", &glm52},
	} {
		pHigh := tmrmCapturePayload(t, tmrmModel(tc.id, true, tc.m), &ai.SimpleStreamOptions{Reasoning: &lvlHigh})
		if re, _ := pHigh.GetString("reasoningEffort"); re != "high" || pHigh.Has("promptMode") {
			t.Fatalf("%s high: got %v", tc.id, pHigh)
		}
		pClamped := tmrmCapturePayload(t, tmrmModel(tc.id, true, tc.m), &ai.SimpleStreamOptions{Reasoning: &lvlLow})
		if re, _ := pClamped.GetString("reasoningEffort"); re != "high" {
			t.Fatalf("%s low->high clamp: got %v", tc.id, pClamped)
		}
		pOff := tmrmCapturePayload(t, tmrmModel(tc.id, true, tc.m), nil)
		if re, _ := pOff.GetString("reasoningEffort"); re != "none" || pOff.Has("promptMode") {
			t.Fatalf("%s off: got %v", tc.id, pOff)
		}
	}

	lvlMax := ai.ThinkingLevelMax
	p52Max := tmrmCapturePayload(t, tmrmModel("zai-glm-5-2", true, &glm52), &ai.SimpleStreamOptions{Reasoning: &lvlMax})
	if re, _ := p52Max.GetString("reasoningEffort"); re != "max" {
		t.Fatalf("glm-5.2 max: got %v", p52Max)
	}

	for _, lvl := range []ai.ThinkingLevel{ai.ThinkingLevelLow, ai.ThinkingLevelHigh, ai.ThinkingLevelMax} {
		p := tmrmCapturePayload(t, tmrmModel("zai-glm-5-3", true, &glm53), &ai.SimpleStreamOptions{Reasoning: &lvl})
		if re, _ := p.GetString("reasoningEffort"); re != string(lvl) {
			t.Fatalf("glm-5.3 %s: got %v", lvl, p)
		}
	}
	p53Med := tmrmCapturePayload(t, tmrmModel("zai-glm-5-3", true, &glm53), &ai.SimpleStreamOptions{Reasoning: &lvlMed})
	if re, _ := p53Med.GetString("reasoningEffort"); re != "high" {
		t.Fatalf("glm-5.3 medium->high: got %v", p53Med)
	}

	// Non-reasoning model omits reasoning controls even when reasoning requested.
	pNon := tmrmCapturePayload(t, tmrmModel("mistral-medium-2505", false, nil), &ai.SimpleStreamOptions{Reasoning: &lvlMed})
	if pNon.Has("reasoningEffort") || pNon.Has("promptMode") {
		t.Fatalf("non-reasoning model should omit reasoning controls, got %v", pNon)
	}

	// Session ID sets promptCacheKey unless cacheRetention == "none".
	pCache := tmrmCapturePayload(t, tmrmModel("mistral-large-latest", false, nil), &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{SessionId: tmrmStrPtr("session-123")},
	})
	if pk, _ := pCache.GetString("promptCacheKey"); pk != "session-123" {
		t.Fatalf("expected promptCacheKey=session-123, got %v", pCache)
	}
	retNone := ai.CacheRetentionNone
	pNoCache := tmrmCapturePayload(t, tmrmModel("mistral-large-latest", false, nil), &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{SessionId: tmrmStrPtr("session-123"), CacheRetention: &retNone},
	})
	if pNoCache.Has("promptCacheKey") {
		t.Fatalf("expected promptCacheKey omitted when cacheRetention=none, got %v", pNoCache)
	}
}
