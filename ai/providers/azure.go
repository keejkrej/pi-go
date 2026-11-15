// Ported from packages/ai/src/providers/azure.ts (pi v1.5.2).

package providers

import (
	"context"
	"maps"
	"reflect"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func azExtractEndpointOptions(options ai.ProviderStreamOptions) *api.AzureEndpointOptions {
	if options == nil {
		return nil
	}
	rv := reflect.ValueOf(options)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return nil
	}
	if az, ok := options.(*api.AzureEndpointOptions); ok {
		return az
	}
	if base := options.BaseStreamOptions(); base != nil {
		return &api.AzureEndpointOptions{StreamOptions: *base}
	}
	return nil
}

func azResolveAzureModel(model *ai.Model, options ai.ProviderStreamOptions) (*ai.Model, error) {
	baseUrl, err := api.ResolveAzureBaseUrl(model, azExtractEndpointOptions(options))
	if err != nil {
		return nil, err
	}
	cp := *model
	cp.BaseUrl = baseUrl
	return &cp, nil
}

func azWrapOnPayload(
	prev func(payload any, model *ai.Model) (any, error),
	deploymentName string,
) func(payload any, model *ai.Model) (any, error) {
	return func(payload any, payloadModel *ai.Model) (any, error) {
		next := payload
		switch p := payload.(type) {
		case *jsonx.Object:
			cloned := p.Clone()
			cloned.Set("model", deploymentName)
			next = cloned
		case map[string]any:
			cloned := maps.Clone(p)
			cloned["model"] = deploymentName
			next = cloned
		}
		if prev == nil {
			return next, nil
		}
		replaced, err := prev(next, payloadModel)
		if err != nil {
			return nil, err
		}
		if replaced != nil {
			return replaced, nil
		}
		return next, nil
	}
}

// azWithDeploymentName sends the deployment name as the request's model, keeping model.Id as the catalog id.
func azWithDeploymentName(model *ai.Model, options ai.ProviderStreamOptions) ai.ProviderStreamOptions {
	deploymentName := api.ResolveDeploymentName(model, azExtractEndpointOptions(options))
	if deploymentName == model.Id {
		return options
	}
	isNil := options == nil
	if !isNil {
		rv := reflect.ValueOf(options)
		isNil = rv.Kind() == reflect.Ptr && rv.IsNil()
	}
	if isNil {
		return &ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				OnPayload: azWrapOnPayload(nil, deploymentName),
			},
		}
	}
	rv := reflect.ValueOf(options)
	if rv.Kind() == reflect.Ptr && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ai.ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.OnPayload = azWrapOnPayload(cpBase.OnPayload, deploymentName)
			}
			return copiedOpts
		}
	}
	base := options.BaseStreamOptions()
	var cp ai.StreamOptions
	if base != nil {
		cp = *base
	}
	cp.OnPayload = azWrapOnPayload(cp.OnPayload, deploymentName)
	return &cp
}

// azAzureStreams resolves the Azure endpoint and deployment before dispatch, inside LazyStream
// so an unconfigured endpoint errors on the stream instead of throwing synchronously out of Stream().
func azAzureStreams(streams ai.ProviderStreams) ai.ProviderStreams {
	out := ai.ProviderStreams{}
	if streams.Stream != nil {
		out.Stream = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				resolved, err := azResolveAzureModel(model, options)
				if err != nil {
					return nil, err
				}
				return streams.Stream(ctx, resolved, transcript, azWithDeploymentName(model, options)), nil
			}, nil, ctx)
		}
	}
	if streams.StreamSimple != nil {
		out.StreamSimple = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				resolved, err := azResolveAzureModel(model, options)
				if err != nil {
					return nil, err
				}
				deploymentName := api.ResolveDeploymentName(model, azExtractEndpointOptions(options))
				wrapped := options
				if deploymentName != model.Id {
					var cp ai.SimpleStreamOptions
					if options != nil {
						cp = *options
					}
					cp.OnPayload = azWrapOnPayload(cp.OnPayload, deploymentName)
					wrapped = &cp
				}
				return streams.StreamSimple(ctx, resolved, transcript, wrapped), nil
			}, nil, ctx)
		}
	}
	return out
}

// AzureProvider constructs the built-in Azure OpenAI provider.
func AzureProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "azure",
		Name:   allPtr("Azure"),
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Azure OpenAI API key", []string{"AZURE_OPENAI_API_KEY"})},
		Models: allCatalogModels(catalog.AzureModels, nil, nil),
		ApiByModel: map[string]ai.ProviderStreams{
			"azure-openai-responses": api.AzureOpenAIResponsesApi(),
			"openai-completions":     azAzureStreams(api.OpenAICompletionsApi()),
		},
	})
}
