// Ported from packages/ai/src/compat.ts (pi v1.5.2).

package compat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/omap"
)

// BuiltinProvider identifies a built-in provider in the generated catalog.
type BuiltinProvider = providers.BuiltinProvider

// GetModel is a deprecated static catalog read. Prefer providers.GetBuiltinModel or ai.Models.GetModel.
func GetModel(provider BuiltinProvider, modelId string) *ai.Model {
	return providers.GetBuiltinModel(provider, modelId)
}

// GetModels is a deprecated static catalog read. Prefer providers.GetBuiltinModels or ai.Models.GetModels.
func GetModels(provider BuiltinProvider) []*ai.Model {
	return providers.GetBuiltinModels(provider)
}

// GetProviders is a deprecated static catalog read. Prefer providers.GetBuiltinProviders or ai.Models.GetProviders.
func GetProviders() []BuiltinProvider {
	return providers.GetBuiltinProviders()
}

// ApiStreamFunction streams raw transcript messages for one API provider.
type ApiStreamFunction = func(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream

// ApiStreamSimpleFunction streams transcript messages with simple options for one API provider.
type ApiStreamSimpleFunction = func(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream

// ApiProvider registers custom stream/streamSimple handlers for an API identifier.
type ApiProvider struct {
	Api          string
	Stream       ApiStreamFunction
	StreamSimple ApiStreamSimpleFunction
}

type compatRegisteredApiProvider struct {
	provider ApiProvider
	sourceId *string
}

const compatAmbientAuthMarker = "<authenticated>"

var (
	compatMu                  sync.RWMutex
	compatApiRegistry         = omap.NewMap[string, *compatRegisteredApiProvider]()
	compatBuiltinApiInstances = make(map[string]*compatRegisteredApiProvider)
	compatModels              ai.MutableModels
)

func init() {
	RegisterBuiltInApiProviders()
	compatModels = providers.BuiltinModels(nil)
}

func compatWrapStream(apiId string, fn ApiStreamFunction) ApiStreamFunction {
	return func(ctx context.Context, model *ai.Model, c *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
		if model == nil || model.Api != apiId {
			actual := "<nil>"
			if model != nil {
				actual = model.Api
			}
			err := fmt.Errorf("Mismatched api: %s expected %s", actual, apiId)
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				return nil, err
			}, nil, ctx)
		}
		if fn == nil {
			err := fmt.Errorf("API provider %s has nil Stream handler", apiId)
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				return nil, err
			}, nil, ctx)
		}
		return fn(ctx, model, c, options)
	}
}

func compatWrapStreamSimple(apiId string, fn ApiStreamSimpleFunction) ApiStreamSimpleFunction {
	return func(ctx context.Context, model *ai.Model, c *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		if model == nil || model.Api != apiId {
			actual := "<nil>"
			if model != nil {
				actual = model.Api
			}
			err := fmt.Errorf("Mismatched api: %s expected %s", actual, apiId)
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				return nil, err
			}, nil, ctx)
		}
		if fn == nil {
			err := fmt.Errorf("API provider %s has nil StreamSimple handler", apiId)
			return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
				return nil, err
			}, nil, ctx)
		}
		return fn(ctx, model, c, options)
	}
}

// RegisterApiProvider registers provider in the global compatibility API registry.
func RegisterApiProvider(provider ApiProvider, sourceId *string) {
	entry := &compatRegisteredApiProvider{
		provider: ApiProvider{
			Api:          provider.Api,
			Stream:       compatWrapStream(provider.Api, provider.Stream),
			StreamSimple: compatWrapStreamSimple(provider.Api, provider.StreamSimple),
		},
		sourceId: sourceId,
	}
	compatMu.Lock()
	compatApiRegistry.Set(provider.Api, entry)
	compatMu.Unlock()
}

// GetApiProvider returns the registered API provider for apiId, or nil when absent.
func GetApiProvider(apiId string) *ApiProvider {
	compatMu.RLock()
	entry, ok := compatApiRegistry.Get(apiId)
	compatMu.RUnlock()
	if !ok || entry == nil {
		return nil
	}
	cp := entry.provider
	return &cp
}

// GetApiProviders returns all registered API providers in insertion order.
func GetApiProviders() []ApiProvider {
	compatMu.RLock()
	entries := compatApiRegistry.Values()
	compatMu.RUnlock()
	out := make([]ApiProvider, 0, len(entries))
	for _, entry := range entries {
		if entry != nil {
			out = append(out, entry.provider)
		}
	}
	return out
}

// UnregisterApiProviders removes all API providers registered with sourceId.
func UnregisterApiProviders(sourceId string) {
	compatMu.Lock()
	defer compatMu.Unlock()
	for _, key := range compatApiRegistry.Keys() {
		entry, ok := compatApiRegistry.Get(key)
		if ok && entry != nil && entry.sourceId != nil && *entry.sourceId == sourceId {
			compatApiRegistry.Delete(key)
		}
	}
}

func compatClearApiProvidersLocked() {
	compatApiRegistry.Clear()
}

func compatRandomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// RegisterFauxProvider registers a scripted faux provider into the global API registry.
func RegisterFauxProvider(options *providers.RegisterFauxProviderOptions) *providers.FauxProviderRegistration {
	core := providers.CreateFauxCore(options)
	sourceId := "faux-provider-" + compatRandomSuffix()
	RegisterApiProvider(ApiProvider{
		Api:          core.Api,
		Stream:       core.Stream,
		StreamSimple: core.StreamSimple,
	}, &sourceId)
	return &providers.FauxProviderRegistration{
		Api:                     core.Api,
		Models:                  core.Models,
		GetModel:                core.GetModel,
		GetModelId:              core.GetModelId,
		State:                   core.State,
		SetResponses:            core.SetResponses,
		AppendResponses:         core.AppendResponses,
		GetPendingResponseCount: core.GetPendingResponseCount,
		Unregister: func() {
			UnregisterApiProviders(sourceId)
		},
	}
}

func compatBuiltinApis() []struct {
	api     string
	streams ai.ProviderStreams
} {
	return []struct {
		api     string
		streams ai.ProviderStreams
	}{
		{"anthropic-messages", api.AnthropicMessagesApi()},
		{"openai-completions", api.OpenAICompletionsApi()},
		{"openai-responses", api.OpenAIResponsesApi()},
		{"openai-codex-responses", api.OpenAICodexResponsesApi()},
		{"azure-openai-responses", api.AzureOpenAIResponsesApi()},
		{"google-generative-ai", api.GoogleGenerativeAiApi()},
		{"google-vertex", api.GoogleVertexApi()},
		{"mistral-conversations", api.MistralConversationsApi()},
		{"bedrock-converse-stream", api.BedrockConverseStreamApi()},
		{"pi-messages", api.PiMessagesApi()},
	}
}

// RegisterBuiltInApiProviders registers the built-in API implementations into the API registry
// without clobbering existing entries so tests or extensions registered earlier stay intact.
func RegisterBuiltInApiProviders() {
	for _, item := range compatBuiltinApis() {
		compatMu.Lock()
		existing, has := compatApiRegistry.Get(item.api)
		if !has || existing == nil {
			entry := &compatRegisteredApiProvider{
				provider: ApiProvider{
					Api:          item.api,
					Stream:       compatWrapStream(item.api, item.streams.Stream),
					StreamSimple: compatWrapStreamSimple(item.api, item.streams.StreamSimple),
				},
			}
			compatApiRegistry.Set(item.api, entry)
			existing = entry
		}
		compatBuiltinApiInstances[item.api] = existing
		compatMu.Unlock()
	}
}

// ResetApiProviders clears all API providers and re-registers the built-in APIs.
func ResetApiProviders() {
	compatMu.Lock()
	compatClearApiProvidersLocked()
	compatBuiltinApiInstances = make(map[string]*compatRegisteredApiProvider)
	compatMu.Unlock()
	RegisterBuiltInApiProviders()
}

func compatHasExplicitApiKey(apiKey *string) bool {
	return apiKey != nil && strings.TrimSpace(*apiKey) != ""
}

func compatIsNilOptions(options ai.ProviderStreamOptions) bool {
	if options == nil {
		return true
	}
	rv := reflect.ValueOf(options)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}

func compatBaseStreamOptions(options ai.ProviderStreamOptions) *ai.StreamOptions {
	if compatIsNilOptions(options) {
		return nil
	}
	return options.BaseStreamOptions()
}

func compatWithEnvApiKey(model *ai.Model, options ai.ProviderStreamOptions) ai.ProviderStreamOptions {
	base := compatBaseStreamOptions(options)
	if base != nil && compatHasExplicitApiKey(base.ApiKey) {
		return options
	}
	var env ai.ProviderEnv
	if base != nil {
		env = base.Env
	}
	apiKey := ai.GetEnvApiKey(string(model.Provider), env)
	if apiKey == nil || *apiKey == "" || *apiKey == compatAmbientAuthMarker {
		return options
	}
	keyCopy := *apiKey
	if compatIsNilOptions(options) {
		return &ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &keyCopy},
		}
	}
	rv := reflect.ValueOf(options)
	if rv.Kind() == reflect.Ptr && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ai.ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.ApiKey = &keyCopy
			}
			return copiedOpts
		}
	}
	cp := *base
	cp.ApiKey = &keyCopy
	return &cp
}

func compatWithEnvApiKeySimple(model *ai.Model, options *ai.SimpleStreamOptions) *ai.SimpleStreamOptions {
	if options != nil && compatHasExplicitApiKey(options.ApiKey) {
		return options
	}
	var env ai.ProviderEnv
	if options != nil {
		env = options.Env
	}
	apiKey := ai.GetEnvApiKey(string(model.Provider), env)
	if apiKey == nil || *apiKey == "" || *apiKey == compatAmbientAuthMarker {
		return options
	}
	keyCopy := *apiKey
	var cp ai.SimpleStreamOptions
	if options != nil {
		cp = *options
	}
	cp.ApiKey = &keyCopy
	return &cp
}

func compatHasResolvedCloudflareAuth(base *ai.StreamOptions) bool {
	if base == nil {
		return false
	}
	if compatHasExplicitApiKey(base.ApiKey) {
		return true
	}
	if base.Headers != nil {
		if val, ok := base.Headers.Get("cf-aig-authorization"); ok && val != nil {
			return true
		}
	}
	return false
}

func compatGetBuiltinProviderForModel(model *ai.Model) *ai.Provider {
	compatMu.RLock()
	cur, hasCur := compatApiRegistry.Get(model.Api)
	builtin := compatBuiltinApiInstances[model.Api]
	compatMu.RUnlock()
	if !hasCur || cur == nil || builtin == nil || cur != builtin {
		return nil
	}
	provider := compatModels.GetProvider(string(model.Provider))
	if provider == nil || provider.GetModels == nil {
		return nil
	}
	for _, candidate := range provider.GetModels() {
		if candidate != nil && candidate.Api == model.Api {
			return provider
		}
	}
	return nil
}

func compatResolveApiProvider(apiId string) (*ApiProvider, error) {
	provider := GetApiProvider(apiId)
	if provider == nil {
		return nil, fmt.Errorf("No API provider registered for api: %s", apiId)
	}
	return provider, nil
}

func compatStreamInternal(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options ai.ProviderStreamOptions,
) (*ai.AssistantMessageEventStream, error) {
	if model == nil {
		return nil, fmt.Errorf("No API provider registered for api: <nil>")
	}
	transcript := ai.NormalizeContext(c)
	if builtinProvider := compatGetBuiltinProviderForModel(model); builtinProvider != nil {
		base := compatBaseStreamOptions(options)
		if strings.HasPrefix(string(model.Provider), "cloudflare-") && !compatHasResolvedCloudflareAuth(base) {
			var modelOpts *ai.ModelsApiStreamOptions
			if !compatIsNilOptions(options) {
				modelOpts = &ai.ModelsApiStreamOptions{ProviderStreamOptions: options}
			}
			return compatModels.Stream(ctx, model, &ai.Context{Messages: transcript.Messages}, modelOpts), nil
		}
		return builtinProvider.Stream(ctx, model, transcript, compatWithEnvApiKey(model, options)), nil
	}
	provider, err := compatResolveApiProvider(model.Api)
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, model, transcript, compatWithEnvApiKey(model, options)), nil
}

func compatStreamSimpleInternal(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options *ai.SimpleStreamOptions,
) (*ai.AssistantMessageEventStream, error) {
	if model == nil {
		return nil, fmt.Errorf("No API provider registered for api: <nil>")
	}
	transcript := ai.NormalizeContext(c)
	if builtinProvider := compatGetBuiltinProviderForModel(model); builtinProvider != nil {
		var base *ai.StreamOptions
		if options != nil {
			base = options.BaseStreamOptions()
		}
		if strings.HasPrefix(string(model.Provider), "cloudflare-") && !compatHasResolvedCloudflareAuth(base) {
			var modelOpts *ai.ModelsSimpleStreamOptions
			if options != nil {
				modelOpts = &ai.ModelsSimpleStreamOptions{SimpleStreamOptions: *options}
			}
			return compatModels.StreamSimple(ctx, model, &ai.Context{Messages: transcript.Messages}, modelOpts), nil
		}
		return builtinProvider.StreamSimple(ctx, model, transcript, compatWithEnvApiKeySimple(model, options)), nil
	}
	provider, err := compatResolveApiProvider(model.Api)
	if err != nil {
		return nil, err
	}
	return provider.StreamSimple(ctx, model, transcript, compatWithEnvApiKeySimple(model, options)), nil
}

// Stream dispatches a chat request through the built-in providers or API registry with env API key injection.
func Stream(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	s, err := compatStreamInternal(ctx, model, c, options)
	if err != nil {
		return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
			return nil, err
		}, nil, ctx)
	}
	return s
}

// Complete runs Stream and waits for its final AssistantMessage result.
func Complete(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options ai.ProviderStreamOptions,
) (*ai.AssistantMessage, error) {
	s, err := compatStreamInternal(ctx, model, c, options)
	if err != nil {
		return nil, err
	}
	return s.Result(ai.OperationSignal(ctx))
}

// StreamSimple dispatches a chat request with SimpleStreamOptions through the built-in providers or API registry.
func StreamSimple(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	s, err := compatStreamSimpleInternal(ctx, model, c, options)
	if err != nil {
		return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
			return nil, err
		}, nil, ctx)
	}
	return s
}

// CompleteSimple runs StreamSimple and waits for its final AssistantMessage result.
func CompleteSimple(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options *ai.SimpleStreamOptions,
) (*ai.AssistantMessage, error) {
	s, err := compatStreamSimpleInternal(ctx, model, c, options)
	if err != nil {
		return nil, err
	}
	return s.Result(ai.OperationSignal(ctx))
}
