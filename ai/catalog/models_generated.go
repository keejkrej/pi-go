// Ported from packages/ai/src/models.generated.ts (pi v1.0.0; updated for pi v1.5.2).

package catalog

import (
	"embed"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

//go:embed data/*.json
var mgDataFS embed.FS

var mgModelsCatalog, mgImageModelsCatalog, mgClassifierModelsCatalog = mgBuildCatalogs()

// Models maps each built-in provider id to its chat model catalog.
var Models *omap.Map[ai.ProviderId, ai.ChatModelCatalog] = mgModelsCatalog

// ImageModels maps each built-in provider id to its image model catalog.
var ImageModels *omap.Map[ai.ProviderId, ai.ImageModelCatalog] = mgImageModelsCatalog

// ClassifierModels maps each built-in provider id to its classifier model catalog.
var ClassifierModels *omap.Map[ai.ProviderId, ai.ClassifierModelCatalog] = mgClassifierModelsCatalog

type mgProviderCatalogEntry struct {
	provider   ai.ProviderId
	chat       ai.ChatModelCatalog
	image      ai.ImageModelCatalog
	classifier ai.ClassifierModelCatalog
}

func mgLoadProviderCatalog(provider ai.ProviderId, filename string) (ai.ChatModelCatalog, ai.ImageModelCatalog, ai.ClassifierModelCatalog) {
	data, err := mgDataFS.ReadFile("data/" + filename)
	if err != nil {
		panic(fmt.Sprintf("catalog: read %s: %v", filename, err))
	}
	groups, err := ai.UnmarshalModelGroups(data)
	if err != nil {
		panic(fmt.Sprintf("catalog: parse %s: %v", filename, err))
	}
	return ai.FlattenChatModelCatalog(provider, groups),
		ai.FlattenImageModelCatalog(provider, groups),
		ai.FlattenClassifierModelCatalog(provider, groups)
}

func mgBuildCatalogs() (
	*omap.Map[ai.ProviderId, ai.ChatModelCatalog],
	*omap.Map[ai.ProviderId, ai.ImageModelCatalog],
	*omap.Map[ai.ProviderId, ai.ClassifierModelCatalog],
) {
	entries := []mgProviderCatalogEntry{
		{ai.KnownProviderAmazonBedrock, AmazonBedrockModels, AmazonBedrockImageModels, AmazonBedrockClassifierModels},
		{ai.KnownProviderAntLing, AntLingModels, AntLingImageModels, AntLingClassifierModels},
		{ai.KnownProviderAnthropic, AnthropicModels, AnthropicImageModels, AnthropicClassifierModels},
		{ai.KnownProviderAzure, AzureModels, AzureImageModels, AzureClassifierModels},
		{ai.KnownProviderBaseten, BasetenModels, BasetenImageModels, BasetenClassifierModels},
		{ai.KnownProviderCerebras, CerebrasModels, CerebrasImageModels, CerebrasClassifierModels},
		{ai.KnownProviderCloudflareAiGateway, CloudflareAiGatewayModels, CloudflareAiGatewayImageModels, CloudflareAiGatewayClassifierModels},
		{ai.KnownProviderCloudflareWorkersAi, CloudflareWorkersAiModels, CloudflareWorkersAiImageModels, CloudflareWorkersAiClassifierModels},
		{ai.KnownProviderDeepseek, DeepseekModels, DeepseekImageModels, DeepseekClassifierModels},
		{ai.KnownProviderFireworks, FireworksModels, FireworksImageModels, FireworksClassifierModels},
		{ai.KnownProviderGithubCopilot, GithubCopilotModels, GithubCopilotImageModels, GithubCopilotClassifierModels},
		{ai.KnownProviderGoogle, GoogleModels, GoogleImageModels, GoogleClassifierModels},
		{ai.KnownProviderGoogleVertex, GoogleVertexModels, GoogleVertexImageModels, GoogleVertexClassifierModels},
		{ai.KnownProviderGroq, GroqModels, GroqImageModels, GroqClassifierModels},
		{ai.KnownProviderHuggingface, HuggingfaceModels, HuggingfaceImageModels, HuggingfaceClassifierModels},
		{ai.KnownProviderKimiCoding, KimiCodingModels, KimiCodingImageModels, KimiCodingClassifierModels},
		{ai.KnownProviderMeta, MetaModels, MetaImageModels, MetaClassifierModels},
		{ai.KnownProviderMinimax, MinimaxModels, MinimaxImageModels, MinimaxClassifierModels},
		{ai.KnownProviderMinimaxCn, MinimaxCnModels, MinimaxCnImageModels, MinimaxCnClassifierModels},
		{ai.KnownProviderMistral, MistralModels, MistralImageModels, MistralClassifierModels},
		{ai.KnownProviderMoonshotai, MoonshotaiModels, MoonshotaiImageModels, MoonshotaiClassifierModels},
		{ai.KnownProviderMoonshotaiCn, MoonshotaiCnModels, MoonshotaiCnImageModels, MoonshotaiCnClassifierModels},
		{ai.KnownProviderNvidia, NvidiaModels, NvidiaImageModels, NvidiaClassifierModels},
		{ai.KnownProviderOpenai, OpenaiModels, OpenaiImageModels, OpenaiClassifierModels},
		{ai.KnownProviderOpenaiCodex, OpenaiCodexModels, OpenaiCodexImageModels, OpenaiCodexClassifierModels},
		{ai.KnownProviderOpencode, OpencodeModels, OpencodeImageModels, OpencodeClassifierModels},
		{ai.KnownProviderOpencodeGo, OpencodeGoModels, OpencodeGoImageModels, OpencodeGoClassifierModels},
		{ai.KnownProviderOpenrouter, OpenrouterModels, OpenrouterImageModels, OpenrouterClassifierModels},
		{ai.KnownProviderQwenTokenPlan, QwenTokenPlanModels, QwenTokenPlanImageModels, QwenTokenPlanClassifierModels},
		{ai.KnownProviderQwenTokenPlanCn, QwenTokenPlanCnModels, QwenTokenPlanCnImageModels, QwenTokenPlanCnClassifierModels},
		{ai.KnownProviderQwenTokenPlanIndividual, QwenTokenPlanIndividualModels, QwenTokenPlanIndividualImageModels, QwenTokenPlanIndividualClassifierModels},
		{ai.KnownProviderQwenTokenPlanIndividualCn, QwenTokenPlanIndividualCnModels, QwenTokenPlanIndividualCnImageModels, QwenTokenPlanIndividualCnClassifierModels},
		{ai.KnownProviderRadius, RadiusModels, RadiusImageModels, RadiusClassifierModels},
		{ai.KnownProviderTogether, TogetherModels, TogetherImageModels, TogetherClassifierModels},
		{ai.KnownProviderTypesafe, TypesafeModels, TypesafeImageModels, TypesafeClassifierModels},
		{ai.KnownProviderVercelAiGateway, VercelAiGatewayModels, VercelAiGatewayImageModels, VercelAiGatewayClassifierModels},
		{ai.KnownProviderXai, XaiModels, XaiImageModels, XaiClassifierModels},
		{ai.KnownProviderXiaomi, XiaomiModels, XiaomiImageModels, XiaomiClassifierModels},
		{ai.KnownProviderXiaomiTokenPlanAms, XiaomiTokenPlanAmsModels, XiaomiTokenPlanAmsImageModels, XiaomiTokenPlanAmsClassifierModels},
		{ai.KnownProviderXiaomiTokenPlanCn, XiaomiTokenPlanCnModels, XiaomiTokenPlanCnImageModels, XiaomiTokenPlanCnClassifierModels},
		{ai.KnownProviderXiaomiTokenPlanSgp, XiaomiTokenPlanSgpModels, XiaomiTokenPlanSgpImageModels, XiaomiTokenPlanSgpClassifierModels},
		{ai.KnownProviderZai, ZaiModels, ZaiImageModels, ZaiClassifierModels},
		{ai.KnownProviderZaiCodingCn, ZaiCodingCnModels, ZaiCodingCnImageModels, ZaiCodingCnClassifierModels},
	}
	models := omap.NewMap[ai.ProviderId, ai.ChatModelCatalog]()
	images := omap.NewMap[ai.ProviderId, ai.ImageModelCatalog]()
	classifiers := omap.NewMap[ai.ProviderId, ai.ClassifierModelCatalog]()
	for _, entry := range entries {
		models.Set(entry.provider, entry.chat)
		images.Set(entry.provider, entry.image)
		classifiers.Set(entry.provider, entry.classifier)
	}
	return models, images, classifiers
}
