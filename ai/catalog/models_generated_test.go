package catalog_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
)

func mgRequireChatModel(t *testing.T, cat ai.ChatModelCatalog, id string) *ai.Model {
	t.Helper()
	m, ok := cat.Get(id)
	if !ok || m == nil {
		t.Fatalf("model %q not found in catalog", id)
	}
	return m
}

func TestModelsGenerated_AllProvidersLoaded(t *testing.T) {
	const wantProviders = 43
	if got := catalog.Models.Len(); got != wantProviders {
		t.Errorf("Models.Len() = %d, want %d", got, wantProviders)
	}
	if got := catalog.ImageModels.Len(); got != wantProviders {
		t.Errorf("ImageModels.Len() = %d, want %d", got, wantProviders)
	}
	if got := catalog.ClassifierModels.Len(); got != wantProviders {
		t.Errorf("ClassifierModels.Len() = %d, want %d", got, wantProviders)
	}

	for provider, cat := range catalog.Models.All() {
		if cat == nil {
			t.Errorf("Models[%q] = nil", provider)
		}
	}
}

func TestModelsGenerated_AzureCatalogV152(t *testing.T) {
	gpt6Sol := mgRequireChatModel(t, catalog.AzureModels, "gpt-6-sol")
	if gpt6Sol.Provider != ai.KnownProviderAzure {
		t.Errorf("Provider = %q, want %q", gpt6Sol.Provider, ai.KnownProviderAzure)
	}
	if gpt6Sol.Api != string(ai.KnownApiAzureOpenaiResponses) {
		t.Errorf("Api = %q, want %q", gpt6Sol.Api, ai.KnownApiAzureOpenaiResponses)
	}
	if gpt6Sol.ContextWindow != 1050000 {
		t.Errorf("ContextWindow = %d, want 1050000", gpt6Sol.ContextWindow)
	}
	openaiGpt6Sol := mgRequireChatModel(t, catalog.OpenaiModels, "gpt-6-sol")
	if diff := cmp.Diff(openaiGpt6Sol.Cost, gpt6Sol.Cost); diff != "" {
		t.Errorf("Azure gpt-6-sol Cost mismatch (-openai +azure):\n%s", diff)
	}
	respCompat, ok := gpt6Sol.Compat.V.(*ai.OpenAIResponsesCompat)
	if !ok {
		t.Fatalf("Azure gpt-6-sol Compat = %T, want *ai.OpenAIResponsesCompat", gpt6Sol.Compat.V)
	}
	if respCompat.SupportsOpenAIGrammarTools == nil || !*respCompat.SupportsOpenAIGrammarTools {
		t.Errorf("Azure gpt-6-sol SupportsOpenAIGrammarTools = %v, want true", respCompat.SupportsOpenAIGrammarTools)
	}

	dsPro := mgRequireChatModel(t, catalog.AzureModels, "deepseek-v4-pro")
	if dsPro.Provider != ai.KnownProviderAzure {
		t.Errorf("deepseek-v4-pro Provider = %q, want %q", dsPro.Provider, ai.KnownProviderAzure)
	}
	if dsPro.Api != string(ai.KnownApiOpenaiCompletions) {
		t.Errorf("deepseek-v4-pro Api = %q, want %q", dsPro.Api, ai.KnownApiOpenaiCompletions)
	}
	compCompat, ok := dsPro.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok {
		t.Fatalf("Azure deepseek-v4-pro Compat = %T, want *ai.OpenAICompletionsCompat", dsPro.Compat.V)
	}
	if compCompat.ThinkingFormat == nil || *compCompat.ThinkingFormat != ai.ThinkingFormatOpenai {
		t.Errorf("Azure deepseek-v4-pro ThinkingFormat = %v, want %q", compCompat.ThinkingFormat, ai.ThinkingFormatOpenai)
	}
	if compCompat.SupportsLongCacheRetention == nil || *compCompat.SupportsLongCacheRetention {
		t.Errorf("Azure deepseek-v4-pro SupportsLongCacheRetention = %v, want false", compCompat.SupportsLongCacheRetention)
	}
}

func TestModelsGenerated_QwenTokenPlanIndividualCn(t *testing.T) {
	if catalog.QwenTokenPlanIndividualCnModels.Len() == 0 {
		t.Fatal("QwenTokenPlanIndividualCnModels is empty")
	}
	qwenMax := mgRequireChatModel(t, catalog.QwenTokenPlanIndividualCnModels, "qwen3.8-max")
	if qwenMax.Provider != ai.KnownProviderQwenTokenPlanIndividualCn {
		t.Errorf("Provider = %q, want %q", qwenMax.Provider, ai.KnownProviderQwenTokenPlanIndividualCn)
	}
}

func TestModelsGenerated_ImagesClassifiersAndThinkingNulls(t *testing.T) {
	if got := catalog.OpenrouterImageModels.Len(); got != 59 {
		t.Errorf("OpenrouterImageModels.Len() = %d, want 59", got)
	}
	if got := catalog.OpenrouterClassifierModels.Len(); got != 10 {
		t.Errorf("OpenrouterClassifierModels.Len() = %d, want 10", got)
	}
	if got := catalog.CloudflareWorkersAiClassifierModels.Len(); got != 1 {
		t.Errorf("CloudflareWorkersAiClassifierModels.Len() = %d, want 1", got)
	}

	astra := mgRequireChatModel(t, catalog.GithubCopilotModels, "gpt-6-astra")
	if astra.ThinkingLevelMap == nil || !astra.ThinkingLevelMap.Minimal.IsNull() {
		t.Errorf("gpt-6-astra ThinkingLevelMap.Minimal = %+v, want explicit null", astra.ThinkingLevelMap)
	}
}
