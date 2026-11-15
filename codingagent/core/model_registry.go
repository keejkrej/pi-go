// Ported from packages/coding-agent/src/core/model-registry.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"

	"github.com/keejkrej/pi-go/ai"
)

// ResolvedRequestAuth is the compatibility auth result returned by ModelRegistry.GetApiKeyAndHeaders.
type ResolvedRequestAuth struct {
	Ok      bool               `json:"ok"`
	ApiKey  *string            `json:"apiKey,omitzero"`
	Headers ai.ProviderHeaders `json:"headers,omitzero"`
	BaseUrl *string            `json:"baseUrl,omitzero"`
	Env     ai.ProviderEnv     `json:"env,omitzero"`
	Error   *string            `json:"error,omitzero"`
}

// ModelRegistry is the synchronous compatibility facade exposed to extensions.
// Coding-agent internals use *ModelRuntime directly.
type ModelRegistry struct {
	runtime *ModelRuntime
}

// NewModelRegistry constructs a ModelRegistry backed by runtime.
func NewModelRegistry(runtime *ModelRuntime) *ModelRegistry {
	return &ModelRegistry{runtime: runtime}
}

// GetRuntime returns the underlying *ModelRuntime.
func (r *ModelRegistry) GetRuntime() *ModelRuntime {
	if r == nil {
		return nil
	}
	return r.runtime
}

// Refresh reloads models.json and refreshes provider catalogs asynchronously.
func (r *ModelRegistry) Refresh(ctx context.Context, options ...*ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
	var opts *ai.ModelsRefreshOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return r.runtime.Refresh(ctx, opts)
}

// GetError returns joined configuration, composition, and availability errors, if any.
func (r *ModelRegistry) GetError() *string {
	return r.runtime.GetError()
}

// GetAll returns all known chat models.
func (r *ModelRegistry) GetAll() []*ai.Model {
	return r.runtime.GetModels(nil)
}

// GetAvailable returns the cached snapshot of available chat models.
func (r *ModelRegistry) GetAvailable() []*ai.Model {
	return r.runtime.GetAvailableSnapshot()
}

// Find finds a chat model by provider and id, or nil if absent.
func (r *ModelRegistry) Find(provider, modelId string) *ai.Model {
	return r.runtime.GetModel(provider, modelId)
}

// FindOfType finds a model of typ by provider and id, or nil if absent.
func (r *ModelRegistry) FindOfType(typ ai.ModelType, provider, modelId string) ai.AnyModel {
	return r.runtime.GetModelOfType(typ, provider, modelId)
}

// HasConfiguredAuth reports whether model's provider has configured auth in the latest snapshot.
func (r *ModelRegistry) HasConfiguredAuth(model *ai.Model) bool {
	if model == nil {
		return false
	}
	return r.runtime.HasConfiguredAuth(string(model.Provider))
}

// GetApiKeyAndHeaders resolves request-time auth and headers for model.
func (r *ModelRegistry) GetApiKeyAndHeaders(ctx context.Context, model *ai.Model) ResolvedRequestAuth {
	if model == nil {
		msg := "Model is nil"
		return ResolvedRequestAuth{Ok: false, Error: &msg}
	}
	resolution, err := r.runtime.GetAuthModel(ctx, model, nil)
	if err != nil {
		cause := errors.Unwrap(err)
		msg := err.Error()
		if cause != nil {
			msg = cause.Error()
		}
		if msg == "authHeader requires a resolved API key" {
			msg = `No API key found for "` + string(model.Provider) + `"`
		}
		return ResolvedRequestAuth{Ok: false, Error: &msg}
	}
	if resolution == nil {
		compat, compatErr := r.runtime.GetCompatibilityRequestConfig(model)
		if compatErr != nil {
			msg := compatErr.Error()
			return ResolvedRequestAuth{Ok: false, Error: &msg}
		}
		if compat.AuthHeader {
			msg := `No API key found for "` + string(model.Provider) + `"`
			return ResolvedRequestAuth{Ok: false, Error: &msg}
		}
		return ResolvedRequestAuth{Ok: true, Headers: compat.Headers}
	}
	return ResolvedRequestAuth{
		Ok:      true,
		ApiKey:  resolution.Auth.ApiKey,
		Headers: resolution.Auth.Headers,
		BaseUrl: resolution.Auth.BaseUrl,
		Env:     resolution.Env,
	}
}

// GetProviderAuthStatus returns the auth configuration status for provider.
func (r *ModelRegistry) GetProviderAuthStatus(provider string) AuthStatus {
	return r.runtime.GetProviderAuthStatus(provider)
}

// GetProvider returns a composed provider by id, or nil if absent.
func (r *ModelRegistry) GetProvider(provider string) *ai.Provider {
	return r.runtime.GetProvider(provider)
}

// Stream streams through the configured provider with request-time authentication.
func (r *ModelRegistry) Stream(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsApiStreamOptions) *ai.AssistantMessageEventStream {
	return r.runtime.Stream(ctx, model, c, options)
}

// StreamSimple streams with provider-neutral options and request-time authentication.
func (r *ModelRegistry) StreamSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	return r.runtime.StreamSimple(ctx, model, c, options)
}

// Complete completes a streaming request through the configured provider.
func (r *ModelRegistry) Complete(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsApiStreamOptions) (*ai.AssistantMessage, error) {
	return r.runtime.Complete(ctx, model, c, options)
}

// GetModelsOfType reads every known model of typ, optionally filtered by provider.
func (r *ModelRegistry) GetModelsOfType(typ ai.ModelType, provider *string) []ai.AnyModel {
	return r.runtime.GetModelsOfType(typ, provider)
}

// GetAvailableOfType returns available models of typ, optionally filtered by provider.
func (r *ModelRegistry) GetAvailableOfType(ctx context.Context, typ ai.ModelType, provider *string, options *ai.AuthOperationOptions) ([]ai.AnyModel, error) {
	return r.runtime.GetAvailableOfType(ctx, typ, provider, options)
}

// GetModelOfType returns a model of typ by provider and modelId, or nil if absent.
func (r *ModelRegistry) GetModelOfType(typ ai.ModelType, provider, modelId string) ai.AnyModel {
	return r.runtime.GetModelOfType(typ, provider, modelId)
}

// Classify classifies structured state with request-time authentication.
func (r *ModelRegistry) Classify(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ModelsClassifierOptions) (*ai.ClassifierResult, error) {
	return r.runtime.Classify(ctx, model, c, options)
}

// GenerateImages generates images with request-time authentication.
func (r *ModelRegistry) GenerateImages(ctx context.Context, model *ai.ImageModel, c *ai.ImagesContext, options *ai.ModelsImagesOptions) (*ai.AssistantImages, error) {
	return r.runtime.GenerateImages(ctx, model, c, options)
}

// GetProviderDisplayName returns the human-readable provider name, or provider id if unknown.
func (r *ModelRegistry) GetProviderDisplayName(provider string) string {
	if p := r.runtime.GetProvider(provider); p != nil && p.Name != "" {
		return p.Name
	}
	return provider
}

// GetProviderAuth resolves authentication for provider.
func (r *ModelRegistry) GetProviderAuth(ctx context.Context, provider string) (*ai.AuthResult, error) {
	return r.runtime.GetAuth(ctx, provider, nil)
}

// GetApiKeyForProvider resolves the API key string for provider, or nil on error/absence.
func (r *ModelRegistry) GetApiKeyForProvider(ctx context.Context, provider string) *string {
	res, err := r.runtime.GetAuth(ctx, provider, nil)
	if err != nil || res == nil {
		return nil
	}
	return res.Auth.ApiKey
}

// IsUsingOAuth reports whether model's provider currently authenticates via OAuth.
func (r *ModelRegistry) IsUsingOAuth(model *ai.Model) bool {
	if model == nil {
		return false
	}
	return r.runtime.IsUsingOAuth(string(model.Provider))
}

// RegisterNativeProvider registers a native *ai.Provider from an extension.
func (r *ModelRegistry) RegisterNativeProvider(provider *ai.Provider) error {
	return r.runtime.RegisterNativeProvider(provider)
}

// RegisterProvider validates and merges a declarative ProviderConfigInput registration.
func (r *ModelRegistry) RegisterProvider(providerName string, config ProviderConfigInput) error {
	return r.runtime.RegisterProvider(providerName, config)
}

// UnregisterProvider removes extension provider registrations for providerName.
func (r *ModelRegistry) UnregisterProvider(providerName string) {
	r.runtime.UnregisterProvider(providerName)
}

// RegisterVirtualModel registers a virtual model definition on the underlying runtime.
func (r *ModelRegistry) RegisterVirtualModel(definition VirtualModelDefinition) error {
	return r.runtime.RegisterVirtualModel(definition)
}

// UnregisterVirtualModel removes a registered virtual model from the underlying runtime.
func (r *ModelRegistry) UnregisterVirtualModel(providerName, id string) {
	r.runtime.UnregisterVirtualModel(providerName, id)
}

// GetRegisteredProviderConfig returns the registered extension config for providerName, if any.
func (r *ModelRegistry) GetRegisteredProviderConfig(providerName string) *ProviderConfigInput {
	return r.runtime.GetRegisteredProviderConfig(providerName)
}

// GetRegisteredNativeProvider returns the registered native *ai.Provider for providerName, if any.
func (r *ModelRegistry) GetRegisteredNativeProvider(providerName string) *ai.Provider {
	return r.runtime.GetRegisteredNativeProvider(providerName)
}

// GetRegisteredProviderIds returns all extension-registered provider ids.
func (r *ModelRegistry) GetRegisteredProviderIds() []string {
	return r.runtime.GetRegisteredProviderIds()
}
