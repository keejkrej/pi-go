// Ported from packages/ai/test/qwen-token-plan-models.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tqtpmModelCase struct {
	provider compat.BuiltinProvider
	modelId  string
}

type tqtpmEnvAuthContext struct {
	env map[string]string
}

func (a tqtpmEnvAuthContext) Env(_ context.Context, name string) (*string, error) {
	v, ok := a.env[name]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (a tqtpmEnvAuthContext) FileExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

type tqtpmSSERoundTripper struct{}

func (tqtpmSSERoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	body := "data: " +
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}` +
		"\n\ndata: [DONE]\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func tqtpmPtr[T any](v T) *T { return &v }

func tqtpmModelIds(models []*ai.Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Id
	}
	return out
}

func tqtpmFindModel(provider compat.BuiltinProvider, modelId string) *ai.Model {
	for _, m := range compat.GetModels(provider) {
		if m != nil && m.Id == modelId {
			return m
		}
	}
	return nil
}

var (
	tqtpmTextModels = []string{
		"MiniMax-M2.5",
		"deepseek-v4-pro",
		"glm-5",
		"glm-5.1",
		"glm-5.2",
		"kimi-k2.5",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"qwen3.6-flash",
		"qwen3.6-plus",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	tqtpmIndividualTextModels = []string{
		"deepseek-v4-flash-0731",
		"deepseek-v4-pro",
		"deepseek-v4-pro-0813",
		"deepseek-v4.1-flash",
		"deepseek-v4.1-flash-1017",
		"glm-5.2",
		"glm-5.3",
		"qwen3.6-flash",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	tqtpmImageModels    = []string{"qwen-image-2.0", "qwen-image-2.0-pro", "wan2.7-image", "wan2.7-image-pro"}
	tqtpmThinkingModels = []string{
		"deepseek-v4-pro",
		"glm-5",
		"glm-5.1",
		"glm-5.2",
		"kimi-k2.5",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"qwen3.6-flash",
		"qwen3.6-plus",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	tqtpmReasoningEffortModels = []string{"deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"}
	tqtpmQwen38Models          = []string{"qwen3.8-flash", "qwen3.8-max"}
)

func tqtpmThinkingCases() []tqtpmModelCase {
	var cases []tqtpmModelCase
	for _, p := range []compat.BuiltinProvider{"qwen-token-plan", "qwen-token-plan-cn"} {
		for _, id := range tqtpmThinkingModels {
			cases = append(cases, tqtpmModelCase{p, id})
		}
	}
	for _, p := range []compat.BuiltinProvider{"qwen-token-plan-individual", "qwen-token-plan-individual-cn"} {
		for _, id := range tqtpmIndividualTextModels {
			cases = append(cases, tqtpmModelCase{p, id})
		}
	}
	return cases
}

func tqtpmReasoningEffortCases() []tqtpmModelCase {
	var cases []tqtpmModelCase
	for _, p := range []compat.BuiltinProvider{"qwen-token-plan", "qwen-token-plan-cn"} {
		for _, id := range tqtpmReasoningEffortModels {
			cases = append(cases, tqtpmModelCase{p, id})
		}
	}
	indivEffort := []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "glm-5.3"}
	for _, p := range []compat.BuiltinProvider{"qwen-token-plan-individual", "qwen-token-plan-individual-cn"} {
		for _, id := range indivEffort {
			cases = append(cases, tqtpmModelCase{p, id})
		}
	}
	return cases
}

func tqtpmQwen38Cases() []tqtpmModelCase {
	var cases []tqtpmModelCase
	for _, p := range []compat.BuiltinProvider{
		"qwen-token-plan",
		"qwen-token-plan-cn",
		"qwen-token-plan-individual",
		"qwen-token-plan-individual-cn",
	} {
		for _, id := range tqtpmQwen38Models {
			cases = append(cases, tqtpmModelCase{p, id})
		}
	}
	return cases
}

func tqtpmStreamPayload(t *testing.T, model *ai.Model, reasoning ai.ThinkingLevel) *jsonx.Object {
	t.Helper()
	var captured *jsonx.Object
	hi := "Hi"
	ctx := context.Background()
	stream := compat.StreamSimple(
		ctx,
		model,
		&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: &hi},
					Timestamp: js.DateNow(),
				},
			},
		},
		&ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     tqtpmPtr("test"),
					HTTPClient: &http.Client{Transport: tqtpmSSERoundTripper{}},
					OnPayload: func(params any, _ *ai.Model) (any, error) {
						obj, _ := params.(*jsonx.Object)
						captured = obj
						return nil, nil
					},
				},
			},
			Reasoning: &reasoning,
		},
	)
	if _, err := stream.Result(ctx); err != nil {
		t.Fatalf("stream.Result failed: %v", err)
	}
	if captured == nil {
		t.Fatalf("expected captured *jsonx.Object payload for %s/%s", model.Provider, model.Id)
	}
	return captured
}

func TestQwenTokenPlanModels_IndividualCatalogs(t *testing.T) {
	t.Parallel()
	want := slices.Clone(tqtpmIndividualTextModels)
	slices.Sort(want)

	for _, p := range []compat.BuiltinProvider{"qwen-token-plan-individual", "qwen-token-plan-individual-cn"} {
		ids := tqtpmModelIds(compat.GetModels(p))
		slices.Sort(ids)
		if !slices.Equal(ids, want) {
			t.Fatalf("%s model ids = %v, want %v", p, ids, want)
		}
	}
}

func TestQwenTokenPlanModels_PerPlanEnvKeyPrecedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider  string
		ctor      func() *ai.Provider
		planVar   string
		sharedVar string
	}{
		{"qwen-token-plan", providers.QwenTokenPlanProvider, "QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		{"qwen-token-plan-cn", providers.QwenTokenPlanCnProvider, "QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
		{"qwen-token-plan-individual", providers.QwenTokenPlanIndividualProvider, "QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		{"qwen-token-plan-individual-cn", providers.QwenTokenPlanIndividualCnProvider, "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
	}
	ctx := context.Background()
	for _, tc := range cases {
		resolve := func(env map[string]string) string {
			models := ai.CreateModels(&ai.CreateModelsOptions{AuthContext: tqtpmEnvAuthContext{env: env}})
			models.SetProvider(tc.ctor())
			auth, err := models.GetAuth(ctx, tc.provider, nil)
			if err != nil || auth == nil || auth.Auth.ApiKey == nil {
				return ""
			}
			return *auth.Auth.ApiKey
		}

		both := ai.ProviderEnv{tc.planVar: "plan", tc.sharedVar: "shared"}
		sharedOnly := ai.ProviderEnv{tc.sharedVar: "shared"}
		if got := resolve(both); got != "plan" {
			t.Fatalf("%s both env auth = %q, want plan", tc.provider, got)
		}
		if got := resolve(sharedOnly); got != "shared" {
			t.Fatalf("%s shared env auth = %q, want shared", tc.provider, got)
		}
		if got := ai.GetEnvApiKey(tc.provider, both); got == nil || *got != "plan" {
			t.Fatalf("%s GetEnvApiKey(both) = %v, want plan", tc.provider, got)
		}
		if got := ai.GetEnvApiKey(tc.provider, sharedOnly); got == nil || *got != "shared" {
			t.Fatalf("%s GetEnvApiKey(shared) = %v, want shared", tc.provider, got)
		}
	}
}

func TestQwenTokenPlanModels_IsolatesPerPlanKeys(t *testing.T) {
	t.Parallel()
	if got := ai.FindEnvKeys("qwen-token-plan", ai.ProviderEnv{"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY": "test"}); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	if got := ai.FindEnvKeys("qwen-token-plan-individual-cn", ai.ProviderEnv{"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY": "test"}); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestQwenTokenPlanModels_TextAndOmittedImageModels(t *testing.T) {
	t.Parallel()
	for _, p := range []compat.BuiltinProvider{"qwen-token-plan", "qwen-token-plan-cn"} {
		ids := tqtpmModelIds(compat.GetModels(p))
		for _, expected := range tqtpmTextModels {
			if !slices.Contains(ids, expected) {
				t.Fatalf("%s should include %s", p, expected)
			}
		}
		for _, excluded := range tqtpmImageModels {
			if slices.Contains(ids, excluded) {
				t.Fatalf("%s should not include %s", p, excluded)
			}
		}
	}
}

func TestQwenTokenPlanModels_SendsQwenThinkingFields(t *testing.T) {
	t.Parallel()
	for _, tc := range tqtpmThinkingCases() {
		model := tqtpmFindModel(tc.provider, tc.modelId)
		if model == nil {
			t.Fatalf("missing model %s/%s", tc.provider, tc.modelId)
		}
		payload := tqtpmStreamPayload(t, model, ai.ThinkingLevelHigh)
		if v, ok := payload.Get("enable_thinking"); !ok || v != true {
			t.Fatalf("%s/%s: expected enable_thinking=true, got %v (ok=%v)", tc.provider, tc.modelId, v, ok)
		}
		if _, hasThinking := payload.Get("thinking"); hasThinking {
			t.Fatalf("%s/%s: unexpected thinking property in payload", tc.provider, tc.modelId)
		}
	}
}

func TestQwenTokenPlanModels_ThinkingLevelMaps(t *testing.T) {
	t.Parallel()
	for _, tc := range tqtpmReasoningEffortCases() {
		model := tqtpmFindModel(tc.provider, tc.modelId)
		if model == nil {
			t.Fatalf("missing model %s/%s", tc.provider, tc.modelId)
		}
		lowWant := jsonx.Null[string]()
		if tc.modelId == "glm-5.3" {
			lowWant = jsonx.Some("low")
		}
		if model.ThinkingLevelMap == nil ||
			!model.ThinkingLevelMap.Minimal.IsNull() ||
			!model.ThinkingLevelMap.Low.Equal(lowWant) ||
			!model.ThinkingLevelMap.Medium.IsNull() ||
			!model.ThinkingLevelMap.High.Equal(jsonx.Some("high")) ||
			!model.ThinkingLevelMap.Xhigh.IsNull() ||
			!model.ThinkingLevelMap.Max.Equal(jsonx.Some("max")) {
			t.Fatalf("%s/%s unexpected thinkingLevelMap: %+v", tc.provider, tc.modelId, model.ThinkingLevelMap)
		}
	}

	glm53 := tqtpmFindModel("qwen-token-plan-individual", "glm-5.3")
	if glm53 == nil || glm53.ThinkingLevelMap == nil ||
		!glm53.ThinkingLevelMap.Minimal.IsNull() ||
		!glm53.ThinkingLevelMap.Low.Equal(jsonx.Some("low")) ||
		!glm53.ThinkingLevelMap.Medium.IsNull() ||
		!glm53.ThinkingLevelMap.High.Equal(jsonx.Some("high")) ||
		!glm53.ThinkingLevelMap.Xhigh.IsNull() ||
		!glm53.ThinkingLevelMap.Max.Equal(jsonx.Some("max")) {
		t.Fatalf("unexpected glm-5.3 thinkingLevelMap: %+v", glm53)
	}

	for _, tc := range tqtpmQwen38Cases() {
		model := tqtpmFindModel(tc.provider, tc.modelId)
		if model == nil || model.ThinkingLevelMap == nil ||
			!model.ThinkingLevelMap.Minimal.IsNull() ||
			!model.ThinkingLevelMap.Low.Equal(jsonx.Some("low")) ||
			!model.ThinkingLevelMap.Medium.Equal(jsonx.Some("medium")) ||
			!model.ThinkingLevelMap.High.IsNull() ||
			!model.ThinkingLevelMap.Xhigh.Equal(jsonx.Some("xhigh")) ||
			!model.ThinkingLevelMap.Max.IsNull() {
			t.Fatalf("%s/%s unexpected qwen3.8 thinkingLevelMap: %+v", tc.provider, tc.modelId, model)
		}
	}
}

func TestQwenTokenPlanModels_OmitsRetiredModels(t *testing.T) {
	t.Parallel()
	retired := []string{"qwen3.8-max-preview", "deepseek-v3.2", "deepseek-v4-flash"}
	for _, p := range []compat.BuiltinProvider{
		"qwen-token-plan",
		"qwen-token-plan-cn",
		"qwen-token-plan-individual",
		"qwen-token-plan-individual-cn",
	} {
		ids := tqtpmModelIds(compat.GetModels(p))
		for _, id := range retired {
			if slices.Contains(ids, id) {
				t.Fatalf("%s still includes retired model %s", p, id)
			}
		}
	}
}

func TestQwenTokenPlanModels_SendsReasoningEffortPayloads(t *testing.T) {
	t.Parallel()
	for _, tc := range tqtpmReasoningEffortCases() {
		model := tqtpmFindModel(tc.provider, tc.modelId)
		if model == nil {
			t.Fatalf("missing model %s/%s", tc.provider, tc.modelId)
		}
		payload := tqtpmStreamPayload(t, model, ai.ThinkingLevelHigh)
		if v, ok := payload.Get("reasoning_effort"); !ok || v != "high" {
			t.Fatalf("%s/%s: expected reasoning_effort=high, got %v (ok=%v)", tc.provider, tc.modelId, v, ok)
		}
	}
	for _, tc := range tqtpmQwen38Cases() {
		model := tqtpmFindModel(tc.provider, tc.modelId)
		if model == nil {
			t.Fatalf("missing model %s/%s", tc.provider, tc.modelId)
		}
		payload := tqtpmStreamPayload(t, model, ai.ThinkingLevelXhigh)
		if v, ok := payload.Get("enable_thinking"); !ok || v != true {
			t.Fatalf("%s/%s: expected enable_thinking=true, got %v", tc.provider, tc.modelId, v)
		}
		if v, ok := payload.Get("reasoning_effort"); !ok || v != "xhigh" {
			t.Fatalf("%s/%s: expected reasoning_effort=xhigh, got %v", tc.provider, tc.modelId, v)
		}
		if _, hasThinking := payload.Get("thinking"); hasThinking {
			t.Fatalf("%s/%s: unexpected thinking property in payload", tc.provider, tc.modelId)
		}
	}
}
