// Ported from packages/ai/src/providers/all.ts (pi v1.5.2).

package providers

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// BuiltinProvider identifies a provider present in the generated catalog.
// ai.KnownProvider additionally includes purely dynamic providers (e.g. "radius")
// configured without a static catalog entry.
type BuiltinProvider = ai.ProviderId

type allModelDataManifest struct {
	GeneratedAt string `json:"generatedAt"`
}

func allPtr(s string) *string {
	return &s
}

func allAbortReason(ctx context.Context) error {
	if ctx != nil {
		if cause := context.Cause(ctx); cause != nil && cause != ctx.Err() {
			return cause
		}
		if ctx.Err() == context.DeadlineExceeded {
			return ctx.Err()
		}
	}
	return ai.AbortNewError("The operation was aborted")
}

func allCatalogModels(
	chats ai.ChatModelCatalog,
	images ai.ImageModelCatalog,
	classifiers ai.ClassifierModelCatalog,
) []ai.AnyModel {
	out := make([]ai.AnyModel, 0, chats.Len()+images.Len()+classifiers.Len())
	if chats != nil {
		for _, m := range chats.Values() {
			out = append(out, m)
		}
	}
	if images != nil {
		for _, m := range images.Values() {
			out = append(out, m)
		}
	}
	if classifiers != nil {
		for _, m := range classifiers.Values() {
			out = append(out, m)
		}
	}
	return out
}

func allMustCreateProvider(opts *ai.CreateProviderOptions) *ai.Provider {
	p, err := ai.CreateProvider(opts)
	if err != nil {
		panic(fmt.Sprintf("providers: invalid builtin provider definition: %v", err))
	}
	return p
}

func allSimpleApiKeyProvider(
	id string,
	name string,
	baseUrl string,
	keyName string,
	envVars []string,
	models ai.ChatModelCatalog,
	streams ai.ProviderStreams,
) *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      id,
		Name:    allPtr(name),
		BaseUrl: allPtr(baseUrl),
		Auth:    ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth(keyName, envVars)},
		Models:  allCatalogModels(models, nil, nil),
		Api:     streams,
	})
}

// GetBuiltinModel returns one generated built-in chat model, or nil if absent.
func GetBuiltinModel(provider BuiltinProvider, modelId string) *ai.Model {
	cat, ok := catalog.Models.Get(provider)
	if !ok || cat == nil {
		return nil
	}
	m, ok := cat.Get(modelId)
	if !ok {
		return nil
	}
	return m
}

// GetBuiltinImageModel returns one generated built-in image model, or nil if absent.
func GetBuiltinImageModel(provider BuiltinProvider, modelId string) *ai.ImageModel {
	cat, ok := catalog.ImageModels.Get(provider)
	if !ok || cat == nil {
		return nil
	}
	m, ok := cat.Get(modelId)
	if !ok {
		return nil
	}
	return m
}

// GetBuiltinClassifierModel returns one generated built-in classifier model, or nil if absent.
func GetBuiltinClassifierModel(provider BuiltinProvider, modelId string) *ai.ClassifierModel {
	cat, ok := catalog.ClassifierModels.Get(provider)
	if !ok || cat == nil {
		return nil
	}
	m, ok := cat.Get(modelId)
	if !ok {
		return nil
	}
	return m
}

// GetBuiltinProviders returns all provider ids present in the generated catalog.
func GetBuiltinProviders() []BuiltinProvider {
	if catalog.Models == nil {
		return []BuiltinProvider{}
	}
	return catalog.Models.Keys()
}

// GetBuiltinModelDataGeneratedAt returns the generation timestamp shared by all
// built-in provider catalogs (epoch milliseconds), or nil when unavailable.
func GetBuiltinModelDataGeneratedAt() *int64 {
	var data []byte
	if _, file, _, ok := runtime.Caller(0); ok {
		data, _ = os.ReadFile(filepath.Join(filepath.Dir(file), "..", "catalog", "data", ".manifest.json"))
	}
	if len(data) == 0 {
		data, _ = os.ReadFile(filepath.Join("ai", "catalog", "data", ".manifest.json"))
	}
	if len(data) == 0 {
		return nil
	}
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil
	}
	var manifest allModelDataManifest
	if err := jsonx.Decode(parsed, &manifest); err != nil || manifest.GeneratedAt == "" {
		return nil
	}
	ms := js.DateParse(manifest.GeneratedAt)
	if math.IsNaN(ms) {
		return nil
	}
	out := int64(ms)
	return &out
}

// GetBuiltinModels returns all generated built-in chat models for provider.
func GetBuiltinModels(provider BuiltinProvider) []*ai.Model {
	cat, ok := catalog.Models.Get(provider)
	if !ok || cat == nil {
		return []*ai.Model{}
	}
	return cat.Values()
}

// GetBuiltinImageModels returns all generated built-in image models for provider.
func GetBuiltinImageModels(provider BuiltinProvider) []*ai.ImageModel {
	cat, ok := catalog.ImageModels.Get(provider)
	if !ok || cat == nil {
		return []*ai.ImageModel{}
	}
	return cat.Values()
}

// GetBuiltinClassifierModels returns all generated built-in classifier models for provider.
func GetBuiltinClassifierModels(provider BuiltinProvider) []*ai.ClassifierModel {
	cat, ok := catalog.ClassifierModels.Get(provider)
	if !ok || cat == nil {
		return []*ai.ClassifierModel{}
	}
	return cat.Values()
}

// GetAllBuiltinModels returns all generated built-in chat, image, and classifier models for provider.
func GetAllBuiltinModels(provider BuiltinProvider) []ai.AnyModel {
	chats := GetBuiltinModels(provider)
	images := GetBuiltinImageModels(provider)
	classifiers := GetBuiltinClassifierModels(provider)
	out := make([]ai.AnyModel, 0, len(chats)+len(images)+len(classifiers))
	for _, m := range chats {
		out = append(out, m)
	}
	for _, m := range images {
		out = append(out, m)
	}
	for _, m := range classifiers {
		out = append(out, m)
	}
	return out
}

// BuiltinProviders returns all built-in providers, freshly constructed.
func BuiltinProviders() []*ai.Provider {
	return []*ai.Provider{
		AmazonBedrockProvider(),
		AntLingProvider(),
		AnthropicProvider(),
		AzureProvider(),
		BasetenProvider(),
		CerebrasProvider(),
		CloudflareAIGatewayProvider(),
		CloudflareWorkersAIProvider(),
		DeepseekProvider(),
		FireworksProvider(),
		GithubCopilotProvider(),
		GoogleProvider(),
		GoogleVertexProvider(),
		GroqProvider(),
		HuggingfaceProvider(),
		KimiCodingProvider(),
		MetaProvider(),
		MinimaxProvider(),
		MinimaxCnProvider(),
		MistralProvider(),
		MoonshotaiProvider(),
		MoonshotaiCnProvider(),
		NvidiaProvider(),
		OpenaiProvider(),
		OpenaiCodexProvider(),
		OpencodeProvider(),
		OpencodeGoProvider(),
		OpenrouterProvider(),
		QwenTokenPlanProvider(),
		QwenTokenPlanCnProvider(),
		QwenTokenPlanIndividualProvider(),
		QwenTokenPlanIndividualCnProvider(),
		RadiusProvider(nil),
		TogetherProvider(),
		TypesafeProvider(),
		VercelAIGatewayProvider(),
		XaiProvider(),
		XiaomiProvider(),
		XiaomiTokenPlanAmsProvider(),
		XiaomiTokenPlanCnProvider(),
		XiaomiTokenPlanSgpProvider(),
		ZaiProvider(),
		ZaiCodingCnProvider(),
	}
}

// BuiltinModels returns an ai.MutableModels collection with every built-in provider registered.
func BuiltinModels(options *ai.CreateModelsOptions) ai.MutableModels {
	models := ai.CreateModels(options)
	for _, p := range BuiltinProviders() {
		models.SetProvider(p)
	}
	return models
}
