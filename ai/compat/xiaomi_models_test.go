// Ported from packages/ai/test/xiaomi-models.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var (
	txmXiaomiProviders     = []compat.BuiltinProvider{"xiaomi", "xiaomi-token-plan-cn", "xiaomi-token-plan-ams", "xiaomi-token-plan-sgp"}
	txmDeprecatedModelIds  = []string{"mimo-v2-flash", "mimo-v2-omni", "mimo-v2-pro"}
	txmReplacementModelIds = []string{"mimo-v2.6-flash", "mimo-v2.6-pro"}
)

func txmModelIds(models []*ai.Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Id
	}
	return out
}

func TestXiaomiModels_OmitsDeprecatedModels(t *testing.T) {
	t.Parallel()
	for _, provider := range txmXiaomiProviders {
		ids := txmModelIds(compat.GetModels(provider))
		for _, id := range txmDeprecatedModelIds {
			if slices.Contains(ids, id) {
				t.Fatalf("%s still contains deprecated model %s", provider, id)
			}
		}
	}
}

func TestXiaomiModels_KeepsReplacementModels(t *testing.T) {
	t.Parallel()
	for _, provider := range txmXiaomiProviders {
		ids := txmModelIds(compat.GetModels(provider))
		for _, id := range txmReplacementModelIds {
			if !slices.Contains(ids, id) {
				t.Fatalf("%s missing replacement model %s", provider, id)
			}
		}
	}
}

// Regression check for #10818: MiMo Chat Completions only supports a thinking on/off toggle.
func TestXiaomiModels_ReasoningOffOnToggle(t *testing.T) {
	t.Parallel()
	key := "test-xiaomi-key"
	text := "test"

	capturePayload := func(model *ai.Model, reasoning *ai.ThinkingLevel) *jsonx.Object {
		var payload *jsonx.Object
		ctx := context.Background()
		stream := compat.StreamSimple(
			ctx,
			model,
			&ai.Context{
				Messages: ai.MessageList{
					&ai.UserMessage{
						Role:      "user",
						Content:   ai.UserMessageContent{Text: &text},
						Timestamp: 0,
					},
				},
			},
			&ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey: &key,
						OnPayload: func(value any, _ *ai.Model) (any, error) {
							obj, _ := value.(*jsonx.Object)
							payload = obj
							return nil, errors.New("payload captured")
						},
					},
				},
				Reasoning: reasoning,
			},
		)
		_, _ = stream.Result(ctx)
		if payload == nil {
			t.Fatalf("failed to capture payload for %s/%s", model.Provider, model.Id)
		}
		return payload
	}

	wantLevels := []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff, ai.ModelThinkingLevelHigh}
	for _, provider := range txmXiaomiProviders {
		var reasoningModels []*ai.Model
		for _, m := range compat.GetModels(provider) {
			if m != nil && m.Reasoning {
				reasoningModels = append(reasoningModels, m)
			}
		}
		if len(reasoningModels) == 0 {
			t.Fatalf("%s returned zero reasoning models", provider)
		}
		for _, model := range reasoningModels {
			if got := ai.GetSupportedThinkingLevels(model); !slices.Equal(got, wantLevels) {
				t.Fatalf("%s/%s supported levels = %v, want %v", provider, model.Id, got, wantLevels)
			}
			high := ai.ThinkingLevelHigh
			enabled := capturePayload(model, &high)
			thinkingVal, ok := enabled.Get("thinking")
			thinkingObj, objOk := thinkingVal.(*jsonx.Object)
			if !ok || !objOk {
				t.Fatalf("%s/%s enabled thinking = %T, want *jsonx.Object", provider, model.Id, thinkingVal)
			}
			if typ, _ := thinkingObj.Get("type"); typ != "enabled" {
				t.Fatalf("%s/%s enabled thinking.type = %v, want enabled", provider, model.Id, typ)
			}
			if _, hasEffort := enabled.Get("reasoning_effort"); hasEffort {
				t.Fatalf("%s/%s enabled unexpected reasoning_effort", provider, model.Id)
			}

			disabled := capturePayload(model, nil)
			disabledVal, ok := disabled.Get("thinking")
			disabledObj, objOk := disabledVal.(*jsonx.Object)
			if !ok || !objOk {
				t.Fatalf("%s/%s disabled thinking = %T, want *jsonx.Object", provider, model.Id, disabledVal)
			}
			if typ, _ := disabledObj.Get("type"); typ != "disabled" {
				t.Fatalf("%s/%s disabled thinking.type = %v, want disabled", provider, model.Id, typ)
			}
			if _, hasEffort := disabled.Get("reasoning_effort"); hasEffort {
				t.Fatalf("%s/%s disabled unexpected reasoning_effort", provider, model.Id)
			}
		}
	}
}
