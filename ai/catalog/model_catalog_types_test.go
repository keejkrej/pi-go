// Ported from packages/ai/test/model-catalog-types.test.ts (pi v1.0.0).

package catalog_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmctRequireChatModel(t *testing.T, cat ai.ChatModelCatalog, id string) *ai.Model {
	t.Helper()
	m, ok := cat.Get(id)
	if !ok || m == nil {
		t.Fatalf("model %q not found in catalog", id)
	}
	return m
}

func TestModelCatalogTypes_DerivesModelApiIdAndProviderFromGroupedModelData(t *testing.T) {
	cases := []struct {
		id       string
		provider ai.ProviderId
	}{
		{id: "grok-4.5", provider: ai.KnownProviderXai},
		{id: "grok-4.6", provider: ""},
		{id: "grok-4.7", provider: ""},
		{id: "grok-4.3", provider: ""},
	}
	for _, tc := range cases {
		m := tmctRequireChatModel(t, catalog.XaiModels, tc.id)
		if m.Api != string(ai.KnownApiOpenaiResponses) {
			t.Errorf("XaiModels[%q].Api = %q, want %q", tc.id, m.Api, ai.KnownApiOpenaiResponses)
		}
		if m.Id != tc.id {
			t.Errorf("XaiModels[%q].Id = %q, want %q", tc.id, m.Id, tc.id)
		}
		if tc.provider != "" && m.Provider != tc.provider {
			t.Errorf("XaiModels[%q].Provider = %q, want %q", tc.id, m.Provider, tc.provider)
		}
	}
}

func TestModelCatalogTypes_RoutesGithubCopilotGrok45ThroughResponsesApi(t *testing.T) {
	m := tmctRequireChatModel(t, catalog.GithubCopilotModels, "grok-4.5")
	if m.Api != string(ai.KnownApiOpenaiResponses) {
		t.Errorf("GithubCopilotModels[%q].Api = %q, want %q", "grok-4.5", m.Api, ai.KnownApiOpenaiResponses)
	}
}

func TestModelCatalogTypes_RoutesAllGithubCopilotGptModelsThroughResponsesApi(t *testing.T) {
	var gptModels []*ai.Model
	for _, model := range catalog.GithubCopilotModels.Values() {
		if strings.HasPrefix(model.Id, "gpt-") {
			gptModels = append(gptModels, model)
		}
	}
	if len(gptModels) == 0 {
		t.Fatal("expected at least one gpt- model in GithubCopilotModels")
	}
	for _, model := range gptModels {
		if model.Api != string(ai.KnownApiOpenaiResponses) {
			t.Errorf("GithubCopilotModels[%q].Api = %q, want %q", model.Id, model.Api, ai.KnownApiOpenaiResponses)
		}
	}

	astra := tmctRequireChatModel(t, catalog.GithubCopilotModels, "gpt-6-astra")
	if astra.Api != string(ai.KnownApiOpenaiResponses) {
		t.Errorf("GithubCopilotModels[%q].Api = %q, want %q", "gpt-6-astra", astra.Api, ai.KnownApiOpenaiResponses)
	}

	for _, modelId := range []string{"gpt-6-sol", "gpt-6-luna"} {
		model := tmctRequireChatModel(t, catalog.GithubCopilotModels, modelId)
		if model.Api != string(ai.KnownApiOpenaiResponses) {
			t.Errorf("%s.Api = %q, want %q", modelId, model.Api, ai.KnownApiOpenaiResponses)
		}
		if model.ContextWindow != 1000000 {
			t.Errorf("%s.ContextWindow = %d, want 1000000", modelId, model.ContextWindow)
		}
		if model.MaxTokens != 128000 {
			t.Errorf("%s.MaxTokens = %d, want 128000", modelId, model.MaxTokens)
		}
		if model.ThinkingLevelMap == nil {
			t.Fatalf("%s.ThinkingLevelMap = nil, want non-nil", modelId)
		}
		if !model.ThinkingLevelMap.Off.Equal(jsonx.Some("none")) {
			t.Errorf("%s.ThinkingLevelMap.Off = %+v, want Some(%q)", modelId, model.ThinkingLevelMap.Off, "none")
		}
		if !model.ThinkingLevelMap.Max.Equal(jsonx.Some("max")) {
			t.Errorf("%s.ThinkingLevelMap.Max = %+v, want Some(%q)", modelId, model.ThinkingLevelMap.Max, "max")
		}
	}
}
