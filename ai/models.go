// Ported from packages/ai/src/models.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// modelsKnownModelTypes is the model types this version understands.
// Lookup only; not iterated.
var modelsKnownModelTypes = map[ModelType]struct{}{
	"chat":       {},
	"image":      {},
	"classifier": {},
}

// modelsExtendedThinkingLevels is the clamp order for ModelThinkingLevel.
var modelsExtendedThinkingLevels = []ModelThinkingLevel{
	"off", "minimal", "low", "medium", "high", "xhigh", "max",
}

// ModelsPublication is one generation-checked catalog update.
// Persist absent leaves storage unchanged, null deletes it, and a value writes it.
type ModelsPublication struct {
	Persist jsonx.Opt[ModelsStoreEntry] `json:"persist,omitzero"`
	Update  func()                      `json:"-"`
}

// RefreshModelsContext is the argument of Provider.RefreshModels.
// ctx on RefreshModels is this value's signal.
type RefreshModelsContext struct {
	Credential   Credential                                         `json:"credential,omitzero"`
	Stored       *ModelsStoreEntry                                  `json:"stored,omitzero"`
	Publish      func(publication *ModelsPublication) (bool, error) `json:"-"`
	AllowNetwork bool                                               `json:"allowNetwork"`
	Force        *bool                                              `json:"force,omitzero"`
	BaseUrl      *string                                            `json:"baseUrl,omitzero"`
}

// RefreshModelsAuth selects what the network phase of Provider.RefreshModels needs.
type RefreshModelsAuth string

const (
	RefreshModelsAuthCredential RefreshModelsAuth = "credential"
	RefreshModelsAuthConfigured RefreshModelsAuth = "configured"
)

// ModelsRefreshOptions is the argument of Models.Refresh.
// ctx on Refresh is options.signal. Nil options means the defaults (network allowed).
type ModelsRefreshOptions struct {
	AllowNetwork *bool    `json:"allowNetwork,omitzero"`
	Providers    []string `json:"providers,omitzero"`
	Force        *bool    `json:"force,omitzero"`
}

// ModelsRefreshResult is the outcome of Models.Refresh.
// Errors preserves provider insertion order.
type ModelsRefreshResult struct {
	Aborted bool                     `json:"aborted"`
	Errors  *omap.Map[string, error] `json:"errors"`
}

// ModelsRequestTransforms is the Models-only addition to provider request options.
type ModelsRequestTransforms struct {
	TransformHeaders func(headers ProviderHeaders) (ProviderHeaders, error) `json:"-"`
}

// ModelsApiStreamOptions is ApiStreamOptions plus ModelsRequestTransforms.
// ApiStreamOptions is not ported. The embedded ProviderStreamOptions value is
// *StreamOptions or an *api options struct. Nil means the caller passed no options.
type ModelsApiStreamOptions struct {
	ProviderStreamOptions
	ModelsRequestTransforms
}

// ModelsSimpleStreamOptions is SimpleStreamOptions plus ModelsRequestTransforms.
type ModelsSimpleStreamOptions struct {
	SimpleStreamOptions
	ModelsRequestTransforms
}

// ModelsDeferredFetchOptions is DeferredFetchOptions plus ModelsRequestTransforms.
type ModelsDeferredFetchOptions struct {
	DeferredFetchOptions
	ModelsRequestTransforms
}

// ModelsDeferredCancelOptions is DeferredCancelOptions plus ModelsRequestTransforms.
type ModelsDeferredCancelOptions struct {
	DeferredCancelOptions
	ModelsRequestTransforms
}

// ModelsImagesOptions is ImagesOptions plus ModelsRequestTransforms.
type ModelsImagesOptions struct {
	ImagesOptions
	ModelsRequestTransforms
}

// ModelsClassifierOptions is ClassifierOptions plus ModelsRequestTransforms.
type ModelsClassifierOptions struct {
	ClassifierOptions
	ModelsRequestTransforms
}

// Provider is one runtime provider. Optional function fields are nil when the
// TypeScript property is absent. Pass and store *Provider; callers may replace
// function fields. ctx parameters are the TypeScript AbortSignal.
type Provider struct {
	mu sync.Mutex

	Id      string          `json:"id"`
	Name    string          `json:"name"`
	BaseUrl *string         `json:"baseUrl,omitzero"`
	Headers ProviderHeaders `json:"headers,omitzero"`
	Auth    ProviderAuth    `json:"auth"`

	// GetModels returns the current chat models. It must not fail; Models treats a failure as no models.
	GetModels func() []*Model `json:"-"`
	// GetAllModels returns every model type. Nil means Models uses GetModels.
	GetAllModels func() []AnyModel `json:"-"`
	// RefreshModels restores and optionally fetches the dynamic catalog. Nil means a static provider.
	RefreshModels func(ctx context.Context, refresh *RefreshModelsContext) error `json:"-"`
	// RefreshModelsAuth selects what the network phase of RefreshModels requires.
	// Empty or "credential" resolves/refreshes the credential; "configured" checks auth without OAuth refresh.
	RefreshModelsAuth RefreshModelsAuth `json:"refreshModelsAuth,omitzero"`
	// FilterModels is the credential-specific chat availability policy. Nil means no filter.
	FilterModels func(models []*Model, credential Credential) []*Model `json:"-"`
	// FilterAllModels is the credential-specific policy for every model type. Nil means FilterModels applies to chat only.
	FilterAllModels func(models []AnyModel, credential Credential) []AnyModel `json:"-"`

	Stream         func(ctx context.Context, model *Model, transcript *TranscriptContext, opts ProviderStreamOptions) *AssistantMessageEventStream `json:"-"`
	StreamSimple   func(ctx context.Context, model *Model, transcript *TranscriptContext, opts *SimpleStreamOptions) *AssistantMessageEventStream  `json:"-"`
	FetchDeferred  func(ctx context.Context, model *Model, handle *DeferredHandle, opts *DeferredFetchOptions) *AssistantMessageEventStream        `json:"-"`
	CancelDeferred func(ctx context.Context, model *Model, handle *DeferredHandle, opts *DeferredCancelOptions) error                              `json:"-"`
	GenerateImages func(ctx context.Context, model *ImageModel, c *ImagesContext, opts *ImagesOptions) (*AssistantImages, error)                   `json:"-"`
	Classify       func(ctx context.Context, model *ClassifierModel, c *ClassifierContext, opts *ClassifierOptions) (*ClassifierResult, error)     `json:"-"`

	baselineModels []AnyModel
	dynamicModels  []AnyModel
	single         ProviderStreams
	byApi          map[string]ProviderStreams
	images         map[string]ProviderImages
	classifiers    map[string]ProviderClassifier
}

// Models is the runtime collection of providers.
// ctx parameters are the TypeScript AbortSignal; options structs do not carry one.
// Provider enumeration order is registration order.
type Models interface {
	GetProviders() []*Provider
	GetProvider(id string) *Provider

	// GetModels reads the last-known chat models. Nil provider means every provider.
	// A provider whose GetModels fails contributes nothing.
	GetModels(provider *string) []*Model
	GetModel(provider string, id string) *Model

	// GetModelsOfType reads last-known models of one type. Nil provider means every provider.
	GetModelsOfType(typ ModelType, provider *string) []AnyModel
	GetModelOfType(typ ModelType, provider string, id string) AnyModel
	GetAllModels(provider *string) []AnyModel

	Refresh(ctx context.Context, options *ModelsRefreshOptions) (ModelsRefreshResult, error)

	CheckAuth(ctx context.Context, providerId string, options *AuthOperationOptions) (*AuthCheck, error)
	GetAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]*Model, error)
	GetAvailableOfType(ctx context.Context, typ ModelType, providerId *string, options *AuthOperationOptions) ([]AnyModel, error)
	GetAllAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]AnyModel, error)

	// GetAuth resolves auth for a provider id. GetAuthModel resolves auth for a model,
	// merging the model's static headers. The TypeScript getAuth overloads are these two methods.
	GetAuth(ctx context.Context, providerId string, overrides *AuthResolutionOverrides) (*AuthResult, error)
	GetAuthModel(ctx context.Context, model AnyModel, overrides *AuthResolutionOverrides) (*AuthResult, error)

	Login(ctx context.Context, providerId string, authType AuthType, interaction AuthInteraction, options *LoginOptions) (Credential, error)
	Logout(ctx context.Context, providerId string, options *AuthOperationOptions) error

	Stream(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) *AssistantMessageEventStream
	Complete(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) (*AssistantMessage, error)
	StreamSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream
	CompleteSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error)
	StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream
	FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error)
	CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error
	GenerateImages(ctx context.Context, model *ImageModel, c *ImagesContext, options *ModelsImagesOptions) (*AssistantImages, error)
	Classify(ctx context.Context, model *ClassifierModel, c *ClassifierContext, options *ModelsClassifierOptions) (*ClassifierResult, error)
}

// MutableModels is a Models collection whose provider set can change.
type MutableModels interface {
	Models
	SetProvider(provider *Provider)
	DeleteProvider(id string)
	ClearProviders()
}

// CreateModelsOptions is the argument of CreateModels. Nil fields use the in-memory defaults.
type CreateModelsOptions struct {
	Credentials CredentialStore `json:"-"`
	ModelsStore ModelsStore     `json:"-"`
	AuthContext AuthContext     `json:"-"`
	Identity    *ClientIdentity `json:"-"`
}

// CreateProviderOptions is the argument of CreateProvider.
// Set Api or ApiByModel, not both. They are the TypeScript api union:
// one ProviderStreams value, or a partial map keyed by model.api.
// Empty Api, Images, and Classifiers together is rejected.
type CreateProviderOptions struct {
	Id              string                                                                       `json:"id"`
	Name            *string                                                                      `json:"name,omitzero"`
	BaseUrl         *string                                                                      `json:"baseUrl,omitzero"`
	Headers         ProviderHeaders                                                              `json:"headers,omitzero"`
	Auth            ProviderAuth                                                                 `json:"auth"`
	Models          []AnyModel                                                                   `json:"models"`
	FetchModels     func(ctx context.Context, refresh *RefreshModelsContext) ([]AnyModel, error) `json:"-"`
	FilterModels    func(models []*Model, credential Credential) []*Model                        `json:"-"`
	FilterAllModels func(models []AnyModel, credential Credential) []AnyModel                    `json:"-"`
	Api             ProviderStreams                                                              `json:"-"`
	ApiByModel      map[string]ProviderStreams                                                   `json:"-"`
	Images          map[string]ProviderImages                                                    `json:"-"`
	Classifiers     map[string]ProviderClassifier                                                `json:"-"`
}

// modelsModelsImpl is the Models collection. Shared across goroutines.
type modelsModelsImpl struct {
	mu                 sync.Mutex
	providers          *omap.Map[string, *Provider]
	credentials        CredentialStore
	modelsStore        ModelsStore
	authContext        AuthContext
	identity           *ClientIdentity
	refreshGenerations map[string]int
	refreshControllers *omap.Map[string, *modelsRefreshCall]
	publicationChains  map[string]*modelsPublicationChain
}

// modelsRefreshCall is one in-flight provider refresh (TS AbortController identity).
type modelsRefreshCall struct {
	generation int
	cancel     context.CancelFunc
}

// modelsPublicationChain sequences publish calls for one provider.
type modelsPublicationChain struct {
	mu   sync.Mutex
	wait chan struct{}
}

type modelsAuthenticatedProvider struct {
	Provider   *Provider
	Credential Credential
	Auth       *AuthCheck
}

// modelsRequestAuth is the auth-related slice of a Models request options value.
type modelsRequestAuth struct {
	ApiKey           *string
	Env              ProviderEnv
	Headers          ProviderHeaders
	TransformHeaders func(headers ProviderHeaders) (ProviderHeaders, error)
	SessionId        *string
	Identity         *ClientIdentity
}

// modelsAppliedRequest is a model plus resolved request auth, without transformHeaders.
type modelsAppliedRequest struct {
	Model    AnyModel
	ApiKey   *string
	Headers  ProviderHeaders
	Env      ProviderEnv
	Identity *ClientIdentity
}

var _ MutableModels = (*modelsModelsImpl)(nil)

func modelsIsNilModel(model AnyModel) bool {
	if model == nil {
		return true
	}
	switch m := model.(type) {
	case *Model:
		return m == nil
	case *ImageModel:
		return m == nil
	case *ClassifierModel:
		return m == nil
	case *UnknownAnyModel:
		return m == nil
	default:
		return false
	}
}

func modelsModelId(model AnyModel) string {
	switch m := model.(type) {
	case *Model:
		if m != nil {
			return m.Id
		}
	case *ImageModel:
		if m != nil {
			return m.Id
		}
	case *ClassifierModel:
		if m != nil {
			return m.Id
		}
	case *UnknownAnyModel:
		if m != nil && m.Raw != nil {
			id, _ := m.Raw.GetString("id")
			return id
		}
	}
	return ""
}

func modelsModelMetadata(model AnyModel) (ProviderId, string, *omap.Map[string, string]) {
	switch m := model.(type) {
	case *Model:
		if m != nil {
			return m.Provider, m.BaseUrl, m.Headers
		}
	case *ImageModel:
		if m != nil {
			return m.Provider, m.BaseUrl, m.Headers
		}
	case *ClassifierModel:
		if m != nil {
			return m.Provider, m.BaseUrl, m.Headers
		}
	case *UnknownAnyModel:
		if m != nil && m.Raw != nil {
			provider, _ := m.Raw.GetString("provider")
			baseUrl, _ := m.Raw.GetString("baseUrl")
			return ProviderId(provider), baseUrl, nil
		}
	}
	return "", "", nil
}

func modelsModelCost(model AnyModel) ModelCost {
	switch m := model.(type) {
	case *Model:
		if m != nil {
			return m.Cost
		}
	case *ImageModel:
		if m != nil {
			return m.Cost
		}
	case *ClassifierModel:
		if m != nil {
			return m.Cost
		}
	}
	return ModelCost{}
}

func modelsWithBaseUrl(model AnyModel, baseUrl *string) AnyModel {
	if baseUrl == nil || *baseUrl == "" {
		return model
	}
	switch m := model.(type) {
	case *Model:
		if m == nil {
			return model
		}
		cp := *m
		cp.BaseUrl = *baseUrl
		return &cp
	case *ImageModel:
		if m == nil {
			return model
		}
		cp := *m
		cp.BaseUrl = *baseUrl
		return &cp
	case *ClassifierModel:
		if m == nil {
			return model
		}
		cp := *m
		cp.BaseUrl = *baseUrl
		return &cp
	default:
		return model
	}
}

func modelsStringMapToProviderHeaders(m *omap.Map[string, string]) ProviderHeaders {
	if m == nil {
		return nil
	}
	out := omap.NewMap[string, *string]()
	for k, v := range m.All() {
		s := v
		out.Set(k, &s)
	}
	return out
}

// modelsHasKnownModelType reports whether model.type is known to this version.
// Missing type is chat.
func modelsHasKnownModelType(model AnyModel) bool {
	if modelsIsNilModel(model) {
		return false
	}
	_, ok := modelsKnownModelTypes[GetModelType(model)]
	return ok
}

// modelsWithKnownModelTypes returns entry with unknown model types removed.
func modelsWithKnownModelTypes(entry *ModelsStoreEntry) *ModelsStoreEntry {
	if entry == nil {
		return nil
	}
	filtered := make(AnyModelList, 0, len(entry.Models))
	for _, model := range entry.Models {
		if modelsHasKnownModelType(model) {
			filtered = append(filtered, model)
		}
	}
	cp := *entry
	cp.Models = filtered
	return &cp
}

// modelsMergeHeaders overlays override onto base. Names match case-insensitively;
// the override name replaces the existing one and keeps the override spelling.
func modelsMergeHeaders(base, override ProviderHeaders) ProviderHeaders {
	if base == nil && override == nil {
		return nil
	}
	merged := omap.NewMap[string, *string]()
	if base != nil {
		for k, v := range base.All() {
			if v == nil {
				merged.Set(k, nil)
			} else {
				s := *v
				merged.Set(k, &s)
			}
		}
	}
	if override != nil {
		for name, value := range override.All() {
			var toDelete []string
			for existingName := range merged.All() {
				if strings.EqualFold(existingName, name) {
					toDelete = append(toDelete, existingName)
				}
			}
			for _, existingName := range toDelete {
				merged.Delete(existingName)
			}
			if value == nil {
				merged.Set(name, nil)
			} else {
				s := *value
				merged.Set(name, &s)
			}
		}
	}
	return merged
}

// CreateModels returns an empty collection. Nil options selects in-memory credentials,
// an in-memory models store, and the default auth context.
func CreateModels(options *CreateModelsOptions) MutableModels {
	m := &modelsModelsImpl{
		providers:          omap.NewMap[string, *Provider](),
		refreshGenerations: map[string]int{},
		refreshControllers: omap.NewMap[string, *modelsRefreshCall](),
		publicationChains:  map[string]*modelsPublicationChain{},
	}
	if options != nil {
		m.credentials = options.Credentials
		m.modelsStore = options.ModelsStore
		m.authContext = options.AuthContext
		m.identity = uiCloneIdentity(options.Identity)
	}
	if m.credentials == nil {
		m.credentials = NewInMemoryCredentialStore()
	}
	if m.modelsStore == nil {
		m.modelsStore = NewInMemoryModelsStore()
	}
	if m.authContext == nil {
		m.authContext = DefaultProviderAuthContext()
	}
	return m
}

func (m *modelsModelsImpl) SetProvider(provider *Provider) {
	if provider == nil {
		return
	}
	m.mu.Lock()
	_, cancel := m.supersedeProviderRefreshLocked(provider.Id)
	m.providers.Set(provider.Id, provider)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *modelsModelsImpl) DeleteProvider(id string) {
	m.mu.Lock()
	_, cancel := m.supersedeProviderRefreshLocked(id)
	m.providers.Delete(id)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *modelsModelsImpl) ClearProviders() {
	m.mu.Lock()
	seen := map[string]struct{}{}
	var ids []string
	for _, id := range m.providers.Keys() {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, id := range m.refreshControllers.Keys() {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	var cancels []context.CancelFunc
	for _, id := range ids {
		_, cancel := m.supersedeProviderRefreshLocked(id)
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	m.providers = omap.NewMap[string, *Provider]()
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *modelsModelsImpl) GetProviders() []*Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Provider, 0, m.providers.Len())
	for _, provider := range m.providers.All() {
		out = append(out, provider)
	}
	return out
}

func (m *modelsModelsImpl) GetProvider(id string) *Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider, _ := m.providers.Get(id)
	return provider
}

func modelsSafeProviderChatModels(provider *Provider) (out []*Model) {
	if provider == nil || provider.GetModels == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return provider.GetModels()
}

func modelsSafeProviderAllModels(provider *Provider) (out []AnyModel) {
	if provider == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if provider.GetAllModels != nil {
		return provider.GetAllModels()
	}
	if provider.GetModels == nil {
		return nil
	}
	chat := provider.GetModels()
	out = make([]AnyModel, 0, len(chat))
	for _, model := range chat {
		out = append(out, model)
	}
	return out
}

func (m *modelsModelsImpl) snapshotProviders(provider *string) []*Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	if provider != nil {
		entry, ok := m.providers.Get(*provider)
		if !ok || entry == nil {
			return nil
		}
		return []*Provider{entry}
	}
	out := make([]*Provider, 0, m.providers.Len())
	for _, entry := range m.providers.All() {
		if entry != nil {
			out = append(out, entry)
		}
	}
	return out
}

func (m *modelsModelsImpl) GetModels(provider *string) []*Model {
	entries := m.snapshotProviders(provider)
	if len(entries) == 0 {
		return []*Model{}
	}
	var models []*Model
	for _, entry := range entries {
		models = append(models, modelsSafeProviderChatModels(entry)...)
	}
	if models == nil {
		return []*Model{}
	}
	return models
}

func (m *modelsModelsImpl) GetAllModels(provider *string) []AnyModel {
	entries := m.snapshotProviders(provider)
	if len(entries) == 0 {
		return []AnyModel{}
	}
	var models []AnyModel
	for _, entry := range entries {
		models = append(models, modelsSafeProviderAllModels(entry)...)
	}
	if models == nil {
		return []AnyModel{}
	}
	return models
}

func (m *modelsModelsImpl) GetModelsOfType(typ ModelType, provider *string) []AnyModel {
	all := m.GetAllModels(provider)
	out := make([]AnyModel, 0, len(all))
	for _, model := range all {
		if IsModelType(model, typ) {
			out = append(out, model)
		}
	}
	return out
}

func (m *modelsModelsImpl) GetModel(provider string, id string) *Model {
	for _, model := range m.GetModels(&provider) {
		if model != nil && model.Id == id {
			return model
		}
	}
	return nil
}

func (m *modelsModelsImpl) GetModelOfType(typ ModelType, provider string, id string) AnyModel {
	for _, model := range m.GetModelsOfType(typ, &provider) {
		if modelsModelId(model) == id {
			return model
		}
	}
	return nil
}

func (m *modelsModelsImpl) supersedeProviderRefreshLocked(providerId string) (int, context.CancelFunc) {
	generation := m.refreshGenerations[providerId] + 1
	m.refreshGenerations[providerId] = generation
	previous, ok := m.refreshControllers.Get(providerId)
	if ok && previous != nil {
		m.refreshControllers.Delete(providerId)
		return generation, previous.cancel
	}
	return generation, nil
}

func (m *modelsModelsImpl) supersedeProviderRefresh(providerId string) int {
	m.mu.Lock()
	generation, cancel := m.supersedeProviderRefreshLocked(providerId)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return generation
}

func (m *modelsModelsImpl) beginProviderRefresh(parent context.Context, providerId string) (context.Context, *modelsRefreshCall) {
	parentSig := OperationSignal(parent)
	ctx, cancel := context.WithCancel(parentSig)
	call := &modelsRefreshCall{}

	m.mu.Lock()
	generation, prevCancel := m.supersedeProviderRefreshLocked(providerId)
	call.generation = generation
	call.cancel = cancel
	m.refreshControllers.Set(providerId, call)
	m.mu.Unlock()

	if prevCancel != nil {
		prevCancel()
	}
	return ctx, call
}

func (m *modelsModelsImpl) publishProviderModels(ctx context.Context, providerId string, generation int, publication *ModelsPublication) (bool, error) {
	sig := OperationSignal(ctx)
	if publication == nil {
		publication = &ModelsPublication{}
	}

	m.mu.Lock()
	chain := m.publicationChains[providerId]
	if chain == nil {
		chain = &modelsPublicationChain{}
		m.publicationChains[providerId] = chain
	}
	chain.mu.Lock()
	prev := chain.wait
	next := make(chan struct{})
	chain.wait = next
	chain.mu.Unlock()
	m.mu.Unlock()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(next)
			m.mu.Lock()
			if cur := m.publicationChains[providerId]; cur == chain {
				cur.mu.Lock()
				if cur.wait == next {
					delete(m.publicationChains, providerId)
				}
				cur.mu.Unlock()
			}
			m.mu.Unlock()
		})
	}

	return RaceWithAbortSignal(sig, func() (bool, error) {
		defer release()
		if prev != nil {
			<-prev
		}
		if sig.Err() != nil {
			return false, uaAbortReason(sig)
		}
		m.mu.Lock()
		stale := m.refreshGenerations[providerId] != generation
		m.mu.Unlock()
		if stale {
			return false, nil
		}

		if publication.Persist.IsNull() {
			if err := m.modelsStore.Delete(sig, providerId, nil); err != nil {
				if sig.Err() != nil {
					return false, uaAbortReason(sig)
				}
				return false, err
			}
		} else if entry, ok := publication.Persist.Get(); ok {
			cloned, err := msCloneEntry(&entry)
			if err != nil {
				return false, err
			}
			if err := m.modelsStore.Write(sig, providerId, cloned, nil); err != nil {
				if sig.Err() != nil {
					return false, uaAbortReason(sig)
				}
				return false, err
			}
		}

		if sig.Err() != nil {
			return false, uaAbortReason(sig)
		}
		m.mu.Lock()
		stale = m.refreshGenerations[providerId] != generation
		m.mu.Unlock()
		if stale {
			return false, nil
		}
		if publication.Update != nil {
			publication.Update()
		}
		return true, nil
	})
}

func (m *modelsModelsImpl) runProviderRefreshPhase(ctx context.Context, provider *Provider, credential Credential, allowNetwork bool, force *bool, generation int) error {
	stored, err := m.modelsStore.Read(ctx, provider.Id, nil)
	if err != nil {
		return err
	}
	var storedArg *ModelsStoreEntry
	if stored != nil {
		cloned, err := msCloneEntry(stored)
		if err != nil {
			return err
		}
		storedArg = modelsWithKnownModelTypes(cloned)
	}
	var forceArg *bool
	if allowNetwork {
		forceArg = force
	}
	return provider.RefreshModels(ctx, &RefreshModelsContext{
		Credential: credential,
		Stored:     storedArg,
		Publish: func(publication *ModelsPublication) (bool, error) {
			return m.publishProviderModels(ctx, provider.Id, generation, publication)
		},
		AllowNetwork: allowNetwork,
		Force:        forceArg,
		BaseUrl:      provider.BaseUrl,
	})
}

func (m *modelsModelsImpl) Refresh(ctx context.Context, options *ModelsRefreshOptions) (ModelsRefreshResult, error) {
	allowNetwork := true
	var force *bool
	var selected map[string]struct{}
	if options != nil {
		if options.AllowNetwork != nil {
			allowNetwork = *options.AllowNetwork
		}
		force = options.Force
		if options.Providers != nil {
			selected = make(map[string]struct{}, len(options.Providers))
			for _, id := range options.Providers {
				selected[id] = struct{}{}
			}
		}
	}

	callerCtx := OperationSignal(ctx)
	errorsOut := omap.NewMap[string, error]()
	if callerCtx.Err() != nil {
		return ModelsRefreshResult{Aborted: true, Errors: errorsOut}, nil
	}

	var refreshable []*Provider
	m.mu.Lock()
	for _, provider := range m.providers.All() {
		if provider == nil || provider.RefreshModels == nil {
			continue
		}
		if selected != nil {
			if _, ok := selected[provider.Id]; !ok {
				continue
			}
		}
		refreshable = append(refreshable, provider)
	}
	m.mu.Unlock()

	errSlots := make([]error, len(refreshable))
	var wg sync.WaitGroup
	for i, provider := range refreshable {
		pCtx, call := m.beginProviderRefresh(callerCtx, provider.Id)
		wg.Add(1)
		go func(idx int, p *Provider, providerCtx context.Context, c *modelsRefreshCall) {
			defer wg.Done()
			defer func() {
				m.mu.Lock()
				if cur, ok := m.refreshControllers.Get(p.Id); ok && cur == c {
					m.refreshControllers.Delete(p.Id)
				}
				m.mu.Unlock()
				c.cancel()
			}()

			_, err := RaceWithAbortSignal(providerCtx, func() (struct{}, error) {
				storedCredential, credentialError := m.readCredential(providerCtx, p.Id)
				// Restore cached provider state before auth resolution or network access.
				if phase1Err := m.runProviderRefreshPhase(providerCtx, p, storedCredential, false, nil, c.generation); phase1Err != nil {
					return struct{}{}, phase1Err
				}
				if credentialError != nil {
					return struct{}{}, credentialError
				}
				if !allowNetwork || providerCtx.Err() != nil {
					return struct{}{}, nil
				}
				netCred, shouldRun, netErr := m.resolveNetworkRefresh(providerCtx, p, storedCredential)
				if netErr != nil {
					return struct{}{}, netErr
				}
				if !shouldRun {
					return struct{}{}, nil
				}
				return struct{}{}, m.runProviderRefreshPhase(providerCtx, p, netCred, true, force, c.generation)
			})
			if err != nil && providerCtx.Err() == nil {
				errSlots[idx] = err
			}
		}(i, provider, pCtx, call)
	}
	wg.Wait()

	for i, provider := range refreshable {
		if errSlots[i] != nil {
			errorsOut.Set(provider.Id, errSlots[i])
		}
	}
	return ModelsRefreshResult{
		Aborted: callerCtx.Err() != nil,
		Errors:  errorsOut,
	}, nil
}

func (m *modelsModelsImpl) resolveNetworkRefresh(ctx context.Context, provider *Provider, stored Credential) (Credential, bool, error) {
	if provider.RefreshModelsAuth == RefreshModelsAuthConfigured {
		check, err := m.checkProviderAuth(ctx, provider, stored)
		if err != nil {
			return nil, false, err
		}
		return nil, check != nil, nil
	}
	credential, err := m.resolveRefreshCredential(ctx, provider, stored)
	if err != nil {
		return nil, false, err
	}
	if credential == nil {
		return nil, false, nil
	}
	return credential, true, nil
}

func (m *modelsModelsImpl) resolveRefreshCredential(ctx context.Context, provider *Provider, stored Credential) (Credential, error) {
	if oauthCred, ok := stored.(*OAuthCredential); ok && oauthCred != nil {
		oauth := provider.Auth.OAuth
		if oauth == nil {
			return nil, nil
		}
		if float64(js.DateNow()) < oauthCred.Expires {
			return oauthCred, nil
		}
		if ctx.Err() != nil {
			return nil, nil
		}
		// A refresh that has started survives cancellation or a superseding model refresh, so a
		// rotated refresh token is always persisted. A newer refresh then sees the fresh credential.
		refreshed, err := RefreshStoredOAuthCredential(ctx, m.credentials, provider.Id, oauth, func(current *OAuthCredential) bool {
			return float64(js.DateNow()) >= current.Expires
		})
		if err != nil {
			return nil, err
		}
		if refreshed == nil {
			return nil, nil
		}
		return refreshed, nil
	}

	apiKey := provider.Auth.ApiKey
	if apiKey == nil || apiKey.Resolve == nil {
		return nil, nil
	}
	var cred *ApiKeyCredential
	if c, ok := stored.(*ApiKeyCredential); ok {
		cred = c
	}
	result, err := apiKey.Resolve(ctx, ApiKeyResolveInput{
		Ctx:        m.authContext,
		Credential: cred,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &ApiKeyCredential{
		Type: AuthTypeApiKey,
		Key:  result.Auth.ApiKey,
		Env:  maps.Clone(result.Env),
	}, nil
}

func (m *modelsModelsImpl) readCredential(ctx context.Context, providerId string) (Credential, error) {
	cred, err := m.credentials.Read(ctx, providerId, nil)
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store read failed for %s", providerId), err)
	}
	return cred, nil
}

func (m *modelsModelsImpl) checkProviderAuth(ctx context.Context, provider *Provider, credential Credential) (*AuthCheck, error) {
	if oauthCred, ok := credential.(*OAuthCredential); ok && oauthCred != nil {
		if provider.Auth.OAuth == nil {
			return nil, nil
		}
		src := "OAuth"
		return &AuthCheck{Source: &src, Type: AuthTypeOAuth}, nil
	}
	apiKey := provider.Auth.ApiKey
	if apiKey == nil {
		return nil, nil
	}
	if apiKey.Check != nil {
		var cred *ApiKeyCredential
		if c, ok := credential.(*ApiKeyCredential); ok {
			cred = c
		}
		check, err := apiKey.Check(ctx, ApiKeyCheckInput{
			Ctx:        m.authContext,
			Credential: cred,
		})
		if err != nil {
			return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("API key auth check failed for provider %s", provider.Id), err)
		}
		return check, nil
	}
	resolution, err := ResolveProviderAuth(ctx, provider.Id, provider.Auth, m.credentials, m.authContext, nil)
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, nil
	}
	return &AuthCheck{Source: resolution.Source, Type: AuthTypeApiKey}, nil
}

func (m *modelsModelsImpl) CheckAuth(ctx context.Context, providerId string, options *AuthOperationOptions) (*AuthCheck, error) {
	sig := OperationSignal(ctx)
	return RaceWithAbortSignal(sig, func() (*AuthCheck, error) {
		if sig.Err() != nil {
			return nil, uaAbortReason(sig)
		}
		provider := m.GetProvider(providerId)
		if provider == nil {
			return nil, nil
		}
		credential, err := m.readCredential(sig, providerId)
		if err != nil {
			return nil, err
		}
		return m.checkProviderAuth(sig, provider, credential)
	})
}

func (m *modelsModelsImpl) getAuthenticatedProviders(ctx context.Context, providerId *string) ([]modelsAuthenticatedProvider, error) {
	if ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	providers := m.snapshotProviders(providerId)
	if len(providers) == 0 {
		return nil, nil
	}
	slots := make([]modelsAuthenticatedProvider, len(providers))
	errs := make([]error, len(providers))
	var wg sync.WaitGroup
	for i, provider := range providers {
		wg.Add(1)
		go func(idx int, p *Provider) {
			defer wg.Done()
			credential, err := m.readCredential(ctx, p.Id)
			if err != nil {
				errs[idx] = err
				return
			}
			auth, err := m.checkProviderAuth(ctx, p, credential)
			if err != nil {
				errs[idx] = err
				return
			}
			slots[idx] = modelsAuthenticatedProvider{
				Provider:   p,
				Credential: credential,
				Auth:       auth,
			}
		}(i, provider)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	out := make([]modelsAuthenticatedProvider, 0, len(slots))
	for _, entry := range slots {
		if entry.Auth != nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (m *modelsModelsImpl) GetAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]*Model, error) {
	sig := OperationSignal(ctx)
	return RaceWithAbortSignal(sig, func() ([]*Model, error) {
		providers, err := m.getAuthenticatedProviders(sig, providerId)
		if err != nil {
			return nil, err
		}
		out := []*Model{}
		for _, entry := range providers {
			models := modelsSafeProviderChatModels(entry.Provider)
			if entry.Provider.FilterModels != nil {
				models = entry.Provider.FilterModels(models, entry.Credential)
			}
			out = append(out, models...)
		}
		return out, nil
	})
}

func (m *modelsModelsImpl) GetAvailableOfType(ctx context.Context, typ ModelType, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	all, err := m.GetAllAvailable(ctx, providerId, options)
	if err != nil {
		return nil, err
	}
	out := make([]AnyModel, 0, len(all))
	for _, model := range all {
		if IsModelType(model, typ) {
			out = append(out, model)
		}
	}
	return out, nil
}

func (m *modelsModelsImpl) GetAllAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	sig := OperationSignal(ctx)
	return RaceWithAbortSignal(sig, func() ([]AnyModel, error) {
		providers, err := m.getAuthenticatedProviders(sig, providerId)
		if err != nil {
			return nil, err
		}
		out := []AnyModel{}
		for _, entry := range providers {
			models := modelsSafeProviderAllModels(entry.Provider)
			if entry.Provider.FilterAllModels != nil {
				models = entry.Provider.FilterAllModels(models, entry.Credential)
				out = append(out, models...)
				continue
			}
			if entry.Provider.FilterModels == nil {
				out = append(out, models...)
				continue
			}
			availableChatIds := make(map[string]struct{})
			for _, cm := range entry.Provider.FilterModels(modelsSafeProviderChatModels(entry.Provider), entry.Credential) {
				if cm != nil {
					availableChatIds[cm.Id] = struct{}{}
				}
			}
			for _, model := range models {
				if !IsModelType(model, ModelTypeChat) {
					out = append(out, model)
					continue
				}
				if _, ok := availableChatIds[modelsModelId(model)]; ok {
					out = append(out, model)
				}
			}
		}
		return out, nil
	})
}

func (m *modelsModelsImpl) GetAuth(ctx context.Context, providerId string, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	sig := OperationSignal(ctx)
	provider := m.GetProvider(providerId)
	if provider == nil {
		return nil, nil
	}
	return ResolveProviderAuth(sig, provider.Id, provider.Auth, m.credentials, m.authContext, overrides)
}

func (m *modelsModelsImpl) GetAuthModel(ctx context.Context, model AnyModel, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	sig := OperationSignal(ctx)
	providerId, _, modelHeaders := modelsModelMetadata(model)
	result, err := m.GetAuth(sig, string(providerId), overrides)
	if err != nil || result == nil || modelHeaders == nil {
		return result, err
	}
	cp := *result
	cp.Auth.Headers = modelsMergeHeaders(result.Auth.Headers, modelsStringMapToProviderHeaders(modelHeaders))
	return &cp, nil
}

func (m *modelsModelsImpl) Login(ctx context.Context, providerId string, authType AuthType, interaction AuthInteraction, options *LoginOptions) (Credential, error) {
	sig := OperationSignal(ctx)
	if sig.Err() != nil {
		return nil, uaAbortReason(sig)
	}
	provider := m.GetProvider(providerId)
	if provider == nil {
		return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Unknown provider: %s", providerId))
	}

	var loginOp func() (Credential, error)
	switch authType {
	case AuthTypeOAuth:
		if provider.Auth.OAuth == nil || provider.Auth.OAuth.Login == nil {
			return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("%s does not support %s login", provider.Name, authType))
		}
		fn := provider.Auth.OAuth.Login
		loginOp = func() (Credential, error) {
			cred, err := fn(sig, interaction, options)
			if err != nil {
				return nil, err
			}
			if cred == nil {
				return nil, nil
			}
			return cred, nil
		}
	case AuthTypeApiKey:
		if provider.Auth.ApiKey == nil || provider.Auth.ApiKey.Login == nil {
			return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("%s does not support %s login", provider.Name, authType))
		}
		fn := provider.Auth.ApiKey.Login
		loginOp = func() (Credential, error) {
			cred, err := fn(sig, interaction)
			if err != nil {
				return nil, err
			}
			if cred == nil {
				return nil, nil
			}
			return cred, nil
		}
	default:
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("%s does not support %s login", provider.Name, authType))
	}

	credential, err := RaceWithAbortSignal(sig, loginOp)
	if err != nil {
		return nil, err
	}

	var startedOnce sync.Once
	startedCh := make(chan struct{})
	type modifyRes struct {
		err error
	}
	doneCh := make(chan modifyRes, 1)
	go func() {
		_, modifyErr := m.credentials.Modify(sig, providerId, func(_ Credential) (Credential, error) {
			startedOnce.Do(func() { close(startedCh) })
			return credential, nil
		}, nil)
		doneCh <- modifyRes{err: modifyErr}
	}()

	waitCommitted := func(res modifyRes) (Credential, error) {
		if res.err != nil {
			if sig.Err() != nil {
				return nil, uaAbortReason(sig)
			}
			return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store modify failed for %s", providerId), res.err)
		}
		return credential, nil
	}

	select {
	case <-startedCh:
		return waitCommitted(<-doneCh)
	case res := <-doneCh:
		return waitCommitted(res)
	case <-sig.Done():
		select {
		case <-startedCh:
			return waitCommitted(<-doneCh)
		default:
			return nil, uaAbortReason(sig)
		}
	}
}

func (m *modelsModelsImpl) Logout(ctx context.Context, providerId string, options *AuthOperationOptions) error {
	sig := OperationSignal(ctx)
	if sig.Err() != nil {
		return uaAbortReason(sig)
	}
	if err := m.credentials.Delete(sig, providerId, nil); err != nil {
		if sig.Err() != nil {
			return uaAbortReason(sig)
		}
		return NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store delete failed for %s", providerId), err)
	}
	return nil
}

func (m *modelsModelsImpl) requireProvider(model AnyModel) (*Provider, error) {
	providerId, _, _ := modelsModelMetadata(model)
	provider := m.GetProvider(string(providerId))
	if provider == nil {
		return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Unknown provider: %s", providerId))
	}
	return provider, nil
}

func (m *modelsModelsImpl) requireChatProvider(model *Model) (*Provider, error) {
	if _, err := AssertChatModel(model); err != nil {
		return nil, err
	}
	return m.requireProvider(model)
}

func (m *modelsModelsImpl) applyAuth(ctx context.Context, model AnyModel, req *modelsRequestAuth) (*modelsAppliedRequest, error) {
	if _, err := m.requireProvider(model); err != nil {
		return nil, err
	}
	var overrides AuthResolutionOverrides
	var reqApiKey *string
	var reqEnv ProviderEnv
	var reqHeaders ProviderHeaders
	var transformHeaders func(headers ProviderHeaders) (ProviderHeaders, error)
	var sessionId *string
	var reqIdentity *ClientIdentity
	if req != nil {
		overrides.ApiKey = req.ApiKey
		overrides.Env = req.Env
		reqApiKey = req.ApiKey
		reqEnv = req.Env
		reqHeaders = req.Headers
		transformHeaders = req.TransformHeaders
		sessionId = req.SessionId
		reqIdentity = req.Identity
	}

	resolution, err := m.GetAuthModel(ctx, model, &overrides)
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		providerId, _, _ := modelsModelMetadata(model)
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Provider is not configured: %s", providerId))
	}

	apiKey := resolution.Auth.ApiKey
	if reqApiKey != nil {
		apiKey = reqApiKey
	}
	requestModel := modelsWithBaseUrl(model, resolution.Auth.BaseUrl)
	reqProviderId, reqBaseUrl, _ := modelsModelMetadata(requestModel)

	sessionHeaders := uiToProviderHeaders(GetOpenCodeSessionHeaders(ClientIdentityTarget{
		Provider: string(reqProviderId),
		BaseUrl:  reqBaseUrl,
	}, sessionId))
	headers := modelsMergeHeaders(modelsMergeHeaders(sessionHeaders, resolution.Auth.Headers), reqHeaders)
	if transformHeaders != nil {
		if headers == nil {
			headers = omap.NewMap[string, *string]()
		}
		headers, err = transformHeaders(headers)
		if err != nil {
			return nil, err
		}
	}

	var env ProviderEnv
	if resolution.Env != nil || reqEnv != nil {
		env = make(ProviderEnv, len(resolution.Env)+len(reqEnv))
		for k, v := range resolution.Env {
			env[k] = v
		}
		for k, v := range reqEnv {
			env[k] = v
		}
	}
	identity := MergeClientIdentity(m.identity, reqIdentity)
	return &modelsAppliedRequest{
		Model:    requestModel,
		ApiKey:   apiKey,
		Headers:  headers,
		Env:      env,
		Identity: identity,
	}, nil
}

func modelsWithAppliedStreamOptions(opts ProviderStreamOptions, applied *modelsAppliedRequest) ProviderStreamOptions {
	if opts == nil {
		return &StreamOptions{
			ProviderRequestOptions: ProviderRequestOptions[Model]{
				ApiKey:   applied.ApiKey,
				Headers:  applied.Headers,
				Env:      applied.Env,
				Identity: applied.Identity,
			},
		}
	}
	rv := reflect.ValueOf(opts)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.ApiKey = applied.ApiKey
				cpBase.Headers = applied.Headers
				cpBase.Env = applied.Env
				cpBase.Identity = applied.Identity
			}
			return copiedOpts
		}
	}
	if base := opts.BaseStreamOptions(); base != nil {
		cp := *base
		cp.ApiKey = applied.ApiKey
		cp.Headers = applied.Headers
		cp.Env = applied.Env
		cp.Identity = applied.Identity
		return &cp
	}
	return &StreamOptions{
		ProviderRequestOptions: ProviderRequestOptions[Model]{
			ApiKey:   applied.ApiKey,
			Headers:  applied.Headers,
			Env:      applied.Env,
			Identity: applied.Identity,
		},
	}
}

func modelsApplyRequestOptions[TModel any](dst *ProviderRequestOptions[TModel], applied *modelsAppliedRequest) {
	dst.ApiKey = applied.ApiKey
	dst.Headers = applied.Headers
	dst.Env = applied.Env
	dst.Identity = applied.Identity
}

func (m *modelsModelsImpl) Stream(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) *AssistantMessageEventStream {
	transcript := NormalizeContext(c)
	var telOpts TelemetryRequestOptions
	var providerOpts ProviderStreamOptions
	var reqAuth *modelsRequestAuth
	if options != nil {
		providerOpts = options.ProviderStreamOptions
		reqAuth = &modelsRequestAuth{TransformHeaders: options.TransformHeaders}
		if providerOpts != nil {
			if base := providerOpts.BaseStreamOptions(); base != nil {
				telOpts.TelemetryContext = base.TelemetryContext
				reqAuth.ApiKey = base.ApiKey
				reqAuth.Env = base.Env
				reqAuth.Headers = base.Headers
				reqAuth.SessionId = base.SessionId
				reqAuth.Identity = base.Identity
			}
		}
	}
	sig := OperationSignal(ctx)
	return LazyStream(
		model,
		func() (*AssistantMessageEventStream, error) {
			provider, err := m.requireChatProvider(model)
			if err != nil {
				return nil, err
			}
			if provider.Stream == nil {
				return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support streaming", model.Provider))
			}
			applied, err := m.applyAuth(sig, model, reqAuth)
			if err != nil {
				return nil, err
			}
			reqModel := applied.Model.(*Model)
			return provider.Stream(sig, reqModel, transcript, modelsWithAppliedStreamOptions(providerOpts, applied)), nil
		},
		ObserveModelRequest(model, &telOpts, false),
		sig,
	)
}

func (m *modelsModelsImpl) Complete(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) (*AssistantMessage, error) {
	return m.Stream(ctx, model, c, options).Result(OperationSignal(ctx))
}

func (m *modelsModelsImpl) StreamSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream {
	transcript := NormalizeContext(c)
	var telOpts TelemetryRequestOptions
	var reqAuth *modelsRequestAuth
	var baseSimple SimpleStreamOptions
	if options != nil {
		baseSimple = options.SimpleStreamOptions
		telOpts.TelemetryContext = baseSimple.TelemetryContext
		if baseSimple.Reasoning != nil {
			r := ModelThinkingLevel(*baseSimple.Reasoning)
			telOpts.Reasoning = &r
		}
		reqAuth = &modelsRequestAuth{
			ApiKey:           baseSimple.ApiKey,
			Env:              baseSimple.Env,
			Headers:          baseSimple.Headers,
			TransformHeaders: options.TransformHeaders,
			SessionId:        baseSimple.SessionId,
			Identity:         baseSimple.Identity,
		}
	}
	sig := OperationSignal(ctx)
	return LazyStream(
		model,
		func() (*AssistantMessageEventStream, error) {
			provider, err := m.requireChatProvider(model)
			if err != nil {
				return nil, err
			}
			if provider.StreamSimple == nil {
				return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support simple streaming", model.Provider))
			}
			applied, err := m.applyAuth(sig, model, reqAuth)
			if err != nil {
				return nil, err
			}
			reqModel := applied.Model.(*Model)
			reqOpts := baseSimple
			modelsApplyRequestOptions(&reqOpts.ProviderRequestOptions, applied)
			return provider.StreamSimple(sig, reqModel, transcript, &reqOpts), nil
		},
		ObserveModelRequest(model, &telOpts, false),
		sig,
	)
}

func (m *modelsModelsImpl) CompleteSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error) {
	return m.StreamSimple(ctx, model, c, options).Result(OperationSignal(ctx))
}

func (m *modelsModelsImpl) StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream {
	var telOpts TelemetryRequestOptions
	var reqAuth *modelsRequestAuth
	var baseDeferred DeferredFetchOptions
	if options != nil {
		baseDeferred = options.DeferredFetchOptions
		telOpts.TelemetryContext = baseDeferred.TelemetryContext
		reqAuth = &modelsRequestAuth{
			ApiKey:           baseDeferred.ApiKey,
			Env:              baseDeferred.Env,
			Headers:          baseDeferred.Headers,
			TransformHeaders: options.TransformHeaders,
			Identity:         baseDeferred.Identity,
		}
	}
	sig := OperationSignal(ctx)
	return LazyStream(
		model,
		func() (*AssistantMessageEventStream, error) {
			provider, err := m.requireChatProvider(model)
			if err != nil {
				return nil, err
			}
			if provider.FetchDeferred == nil {
				return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider))
			}
			applied, err := m.applyAuth(sig, model, reqAuth)
			if err != nil {
				return nil, err
			}
			reqModel := applied.Model.(*Model)
			reqOpts := baseDeferred
			modelsApplyRequestOptions(&reqOpts.ProviderRequestOptions, applied)
			return provider.FetchDeferred(sig, reqModel, handle, &reqOpts), nil
		},
		ObserveModelRequest(model, &telOpts, true),
		sig,
	)
}

func (m *modelsModelsImpl) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error) {
	return m.StreamDeferred(ctx, model, handle, options).Result(OperationSignal(ctx))
}

func (m *modelsModelsImpl) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error {
	sig := OperationSignal(ctx)
	provider, err := m.requireChatProvider(model)
	if err != nil {
		return err
	}
	if provider.CancelDeferred == nil {
		return NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider))
	}
	var baseCancel DeferredCancelOptions
	var reqAuth *modelsRequestAuth
	if options != nil {
		baseCancel = options.DeferredCancelOptions
		reqAuth = &modelsRequestAuth{
			ApiKey:           baseCancel.ApiKey,
			Env:              baseCancel.Env,
			Headers:          baseCancel.Headers,
			TransformHeaders: options.TransformHeaders,
			Identity:         baseCancel.Identity,
		}
	}
	applied, err := m.applyAuth(sig, model, reqAuth)
	if err != nil {
		return err
	}
	reqModel := applied.Model.(*Model)
	reqOpts := baseCancel
	modelsApplyRequestOptions(&reqOpts, applied)
	return provider.CancelDeferred(sig, reqModel, handle, &reqOpts)
}

func (m *modelsModelsImpl) GenerateImages(ctx context.Context, model *ImageModel, c *ImagesContext, options *ModelsImagesOptions) (*AssistantImages, error) {
	sig := OperationSignal(ctx)
	errResult := func(err error) (*AssistantImages, error) {
		errModel := model
		if errModel == nil {
			errModel = &ImageModel{}
		}
		return ImageErrorResult(errModel, err, sig.Err() != nil), nil
	}
	if _, err := AssertImageModel(model); err != nil {
		return errResult(err)
	}
	provider, err := m.requireProvider(model)
	if err != nil {
		return errResult(err)
	}
	if provider.GenerateImages == nil {
		return errResult(NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support image generation", model.Provider)))
	}
	var baseImages ImagesOptions
	var reqAuth *modelsRequestAuth
	if options != nil {
		baseImages = options.ImagesOptions
		reqAuth = &modelsRequestAuth{
			ApiKey:           baseImages.ApiKey,
			Env:              baseImages.Env,
			Headers:          baseImages.Headers,
			TransformHeaders: options.TransformHeaders,
			Identity:         baseImages.Identity,
		}
	}
	applied, err := m.applyAuth(sig, model, reqAuth)
	if err != nil {
		return errResult(err)
	}
	reqModel := applied.Model.(*ImageModel)
	reqOpts := baseImages
	modelsApplyRequestOptions(&reqOpts.ProviderRequestOptions, applied)
	return provider.GenerateImages(sig, reqModel, c, &reqOpts)
}

func (m *modelsModelsImpl) Classify(ctx context.Context, model *ClassifierModel, c *ClassifierContext, options *ModelsClassifierOptions) (*ClassifierResult, error) {
	sig := OperationSignal(ctx)
	errResult := func(err error) (*ClassifierResult, error) {
		errModel := model
		if errModel == nil {
			errModel = &ClassifierModel{}
		}
		return ClassifierErrorResult(errModel, err, sig.Err() != nil), nil
	}
	if _, err := AssertClassifierModel(model); err != nil {
		return errResult(err)
	}
	if err := AssertClassifierInputSupported(model, c); err != nil {
		return errResult(err)
	}
	provider, err := m.requireProvider(model)
	if err != nil {
		return errResult(err)
	}
	if provider.Classify == nil {
		return errResult(NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support classification", model.Provider)))
	}
	var baseClassify ClassifierOptions
	var reqAuth *modelsRequestAuth
	if options != nil {
		baseClassify = options.ClassifierOptions
		reqAuth = &modelsRequestAuth{
			ApiKey:           baseClassify.ApiKey,
			Env:              baseClassify.Env,
			Headers:          baseClassify.Headers,
			TransformHeaders: options.TransformHeaders,
			Identity:         baseClassify.Identity,
		}
	}
	applied, err := m.applyAuth(sig, model, reqAuth)
	if err != nil {
		return errResult(err)
	}
	reqModel := applied.Model.(*ClassifierModel)
	reqOpts := baseClassify
	modelsApplyRequestOptions(&reqOpts.ProviderRequestOptions, applied)
	return provider.Classify(sig, reqModel, c, &reqOpts)
}

func modelsProviderStreamsConfigured(s ProviderStreams) bool {
	return s.Stream != nil || s.StreamSimple != nil || s.FetchDeferred != nil || s.CancelDeferred != nil
}

// CreateProvider builds a provider from static models and API implementations.
// It rejects a provider with no chat, image, or classifier implementation.
func CreateProvider(input *CreateProviderOptions) (*Provider, error) {
	if input == nil {
		return nil, fmt.Errorf("CreateProvider: input is required")
	}

	hasSingle := modelsProviderStreamsConfigured(input.Api)
	if hasSingle && len(input.ApiByModel) > 0 {
		return nil, fmt.Errorf("Provider %s: cannot specify both Api and ApiByModel", input.Id)
	}

	var byApi map[string]ProviderStreams
	var streamList []ProviderStreams
	if hasSingle {
		streamList = []ProviderStreams{input.Api}
	} else if len(input.ApiByModel) > 0 {
		byApi = make(map[string]ProviderStreams, len(input.ApiByModel))
		for k, v := range input.ApiByModel {
			if modelsProviderStreamsConfigured(v) {
				byApi[k] = v
				streamList = append(streamList, v)
			}
		}
	}

	var images map[string]ProviderImages
	if len(input.Images) > 0 {
		images = make(map[string]ProviderImages, len(input.Images))
		for k, v := range input.Images {
			if v.GenerateImages != nil {
				images[k] = v
			}
		}
	}

	var classifiers map[string]ProviderClassifier
	if len(input.Classifiers) > 0 {
		classifiers = make(map[string]ProviderClassifier, len(input.Classifiers))
		for k, v := range input.Classifiers {
			if v.Classify != nil {
				classifiers[k] = v
			}
		}
	}

	if len(streamList) == 0 && len(images) == 0 && len(classifiers) == 0 {
		return nil, fmt.Errorf("Provider %s: at least one of \"api\", \"images\", or \"classifiers\" is required.", input.Id)
	}

	name := input.Id
	if input.Name != nil {
		name = *input.Name
	}

	baselineModels := slices.Clone(input.Models)
	var modelsMu sync.RWMutex
	var dynamicModels []AnyModel

	currentModels := func() []AnyModel {
		modelsMu.RLock()
		defer modelsMu.RUnlock()
		merged := make([]AnyModel, 0, len(baselineModels)+len(dynamicModels))
		merged = append(merged, baselineModels...)
		for _, model := range dynamicModels {
			mType := GetModelType(model)
			mId := modelsModelId(model)
			idx := -1
			for i, existing := range merged {
				if GetModelType(existing) == mType && modelsModelId(existing) == mId {
					idx = i
					break
				}
			}
			if idx >= 0 {
				merged[idx] = model
			} else {
				merged = append(merged, model)
			}
		}
		return merged
	}

	apiFor := func(model *Model) (ProviderStreams, bool) {
		if hasSingle {
			return input.Api, true
		}
		if model == nil || byApi == nil {
			return ProviderStreams{}, false
		}
		impl, ok := byApi[model.Api]
		return impl, ok
	}

	dispatch := func(ctx context.Context, model *Model, run func(streams ProviderStreams) *AssistantMessageEventStream) *AssistantMessageEventStream {
		streams, ok := apiFor(model)
		if !ok {
			apiName := ""
			if model != nil {
				apiName = model.Api
			}
			return LazyStream(
				model,
				func() (*AssistantMessageEventStream, error) {
					return nil, NewModelsError(ModelsErrorCodeStream, fmt.Sprintf("Provider %s has no API implementation for %q", input.Id, apiName))
				},
				nil,
				ctx,
			)
		}
		return run(streams)
	}

	provider := &Provider{
		Id:      input.Id,
		Name:    name,
		BaseUrl: input.BaseUrl,
		Headers: input.Headers,
		Auth:    input.Auth,
		GetModels: func() []*Model {
			all := currentModels()
			out := make([]*Model, 0, len(all))
			for _, model := range all {
				if chat, ok := model.(*Model); ok && chat != nil && IsModelType(chat, ModelTypeChat) {
					out = append(out, chat)
				}
			}
			return out
		},
		GetAllModels:    currentModels,
		FilterModels:    input.FilterModels,
		FilterAllModels: input.FilterAllModels,
		baselineModels:  baselineModels,
		single:          input.Api,
		byApi:           byApi,
		images:          images,
		classifiers:     classifiers,
		Stream: func(ctx context.Context, model *Model, transcript *TranscriptContext, opts ProviderStreamOptions) *AssistantMessageEventStream {
			return dispatch(ctx, model, func(streams ProviderStreams) *AssistantMessageEventStream {
				if streams.Stream == nil {
					return LazyStream(model, func() (*AssistantMessageEventStream, error) {
						return nil, NewModelsError(ModelsErrorCodeStream, fmt.Sprintf("Provider %s has no Stream implementation for %q", input.Id, model.Api))
					}, nil, ctx)
				}
				return streams.Stream(ctx, model, transcript, opts)
			})
		},
		StreamSimple: func(ctx context.Context, model *Model, transcript *TranscriptContext, opts *SimpleStreamOptions) *AssistantMessageEventStream {
			return dispatch(ctx, model, func(streams ProviderStreams) *AssistantMessageEventStream {
				if streams.StreamSimple == nil {
					return LazyStream(model, func() (*AssistantMessageEventStream, error) {
						return nil, NewModelsError(ModelsErrorCodeStream, fmt.Sprintf("Provider %s has no StreamSimple implementation for %q", input.Id, model.Api))
					}, nil, ctx)
				}
				return streams.StreamSimple(ctx, model, transcript, opts)
			})
		},
	}

	if input.FetchModels != nil {
		fetchModels := input.FetchModels
		provider.RefreshModels = func(ctx context.Context, refresh *RefreshModelsContext) error {
			if refresh == nil {
				return nil
			}
			if refresh.Stored != nil {
				restored := make([]AnyModel, 0, len(refresh.Stored.Models))
				for _, model := range refresh.Stored.Models {
					if pId, _, _ := modelsModelMetadata(model); string(pId) == input.Id {
						restored = append(restored, model)
					}
				}
				ok, err := refresh.Publish(&ModelsPublication{
					Update: func() {
						modelsMu.Lock()
						dynamicModels = restored
						modelsMu.Unlock()
					},
				})
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}
			if !refresh.AllowNetwork || ctx.Err() != nil {
				return nil
			}
			fetched, err := fetchModels(ctx, refresh)
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			refreshed := make([]AnyModel, 0, len(fetched))
			for _, model := range fetched {
				if modelsHasKnownModelType(model) {
					refreshed = append(refreshed, model)
				}
			}
			now := js.DateNow()
			_, err = refresh.Publish(&ModelsPublication{
				Persist: jsonx.Some(ModelsStoreEntry{
					Models:    AnyModelList(refreshed),
					CheckedAt: &now,
				}),
				Update: func() {
					modelsMu.Lock()
					dynamicModels = refreshed
					modelsMu.Unlock()
				},
			})
			return err
		}
	}

	hasFetchDeferred := false
	hasCancelDeferred := false
	for _, s := range streamList {
		if s.FetchDeferred != nil {
			hasFetchDeferred = true
		}
		if s.CancelDeferred != nil {
			hasCancelDeferred = true
		}
	}
	if hasFetchDeferred {
		provider.FetchDeferred = func(ctx context.Context, model *Model, handle *DeferredHandle, opts *DeferredFetchOptions) *AssistantMessageEventStream {
			return LazyStream(
				model,
				func() (*AssistantMessageEventStream, error) {
					impl, ok := apiFor(model)
					if !ok || impl.FetchDeferred == nil {
						apiName := ""
						if model != nil {
							apiName = model.Api
						}
						return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support deferred responses for %q", input.Id, apiName))
					}
					return impl.FetchDeferred(ctx, model, handle, opts), nil
				},
				nil,
				ctx,
			)
		}
	}
	if hasCancelDeferred {
		provider.CancelDeferred = func(ctx context.Context, model *Model, handle *DeferredHandle, opts *DeferredCancelOptions) error {
			impl, ok := apiFor(model)
			if !ok || impl.CancelDeferred == nil {
				apiName := ""
				if model != nil {
					apiName = model.Api
				}
				return NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s cannot cancel deferred responses for %q", input.Id, apiName))
			}
			return impl.CancelDeferred(ctx, model, handle, opts)
		}
	}
	if len(images) > 0 {
		provider.GenerateImages = func(ctx context.Context, model *ImageModel, c *ImagesContext, opts *ImagesOptions) (*AssistantImages, error) {
			if model == nil {
				return ImageErrorResult(&ImageModel{}, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s has no image generation implementation for \"\"", input.Id))), nil
			}
			impl, ok := images[model.Api]
			if !ok || impl.GenerateImages == nil {
				return ImageErrorResult(model, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s has no image generation implementation for %q", input.Id, model.Api))), nil
			}
			return impl.GenerateImages(ctx, model, c, opts)
		}
	}
	if len(classifiers) > 0 {
		provider.Classify = func(ctx context.Context, model *ClassifierModel, c *ClassifierContext, opts *ClassifierOptions) (*ClassifierResult, error) {
			if model == nil {
				return ClassifierErrorResult(&ClassifierModel{}, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s has no classifier implementation for \"\"", input.Id))), nil
			}
			impl, ok := classifiers[model.Api]
			if !ok || impl.Classify == nil {
				return ClassifierErrorResult(model, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s has no classifier implementation for %q", input.Id, model.Api))), nil
			}
			return impl.Classify(ctx, model, c, opts)
		}
	}

	return provider, nil
}

// HasApi reports whether model is a chat model whose api equals api.
// Non-chat models do not match, even when their api id is equal.
func HasApi(model AnyModel, api string) bool {
	chat, ok := model.(*Model)
	return ok && chat != nil && IsModelType(chat, ModelTypeChat) && chat.Api == api
}

// CalculateCost writes usage.Cost from model.Cost and returns it.
// The highest tier whose inputTokensAbove is below total input usage applies.
// cacheWrite1h is billed at twice the input rate; the rest of cacheWrite uses cacheWrite.
func CalculateCost(model AnyModel, usage *Usage) *UsageCost {
	if usage == nil {
		return nil
	}
	cost := modelsModelCost(model)
	inputTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	rates := cost.ModelCostRates
	matchedThreshold := -1
	for _, tier := range cost.Tiers {
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > matchedThreshold {
			rates = tier.ModelCostRates
			matchedThreshold = tier.InputTokensAbove
		}
	}

	// Anthropic charges 2x base input for 1h cache writes.
	longWrite := 0
	if usage.CacheWrite1h != nil {
		longWrite = *usage.CacheWrite1h
	}
	shortWrite := usage.CacheWrite - longWrite
	usage.Cost.Input = (rates.Input / 1000000) * float64(usage.Input)
	usage.Cost.Output = (rates.Output / 1000000) * float64(usage.Output)
	usage.Cost.CacheRead = (rates.CacheRead / 1000000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (rates.CacheWrite*float64(shortWrite) + rates.Input*2*float64(longWrite)) / 1000000
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	totalFromProvider := false
	usage.Cost.TotalFromProvider = &totalFromProvider
	return &usage.Cost
}

func modelsThinkingLevelMapping(levelMap *ThinkingLevelMap, level ModelThinkingLevel) jsonx.Opt[string] {
	if levelMap == nil {
		return jsonx.Opt[string]{}
	}
	switch level {
	case ModelThinkingLevelOff:
		return levelMap.Off
	case ModelThinkingLevelMinimal:
		return levelMap.Minimal
	case ModelThinkingLevelLow:
		return levelMap.Low
	case ModelThinkingLevelMedium:
		return levelMap.Medium
	case ModelThinkingLevelHigh:
		return levelMap.High
	case ModelThinkingLevelXhigh:
		return levelMap.Xhigh
	case ModelThinkingLevelMax:
		return levelMap.Max
	default:
		return jsonx.Opt[string]{}
	}
}

// GetSupportedThinkingLevels returns the thinking levels model accepts, in clamp order.
// A model without reasoning accepts only "off".
func GetSupportedThinkingLevels(model *Model) []ModelThinkingLevel {
	if model == nil || !model.Reasoning {
		return []ModelThinkingLevel{ModelThinkingLevelOff}
	}
	out := make([]ModelThinkingLevel, 0, len(modelsExtendedThinkingLevels))
	for _, level := range modelsExtendedThinkingLevels {
		mapped := modelsThinkingLevelMapping(model.ThinkingLevelMap, level)
		if mapped.IsNull() {
			continue
		}
		if level == ModelThinkingLevelXhigh || level == ModelThinkingLevelMax {
			if mapped.IsSome() {
				out = append(out, level)
			}
			continue
		}
		out = append(out, level)
	}
	return out
}

// ClampThinkingLevel returns level when model supports it, otherwise the nearest
// supported level at or above it, then below it, then the first supported level.
func ClampThinkingLevel(model *Model, level ModelThinkingLevel) ModelThinkingLevel {
	availableLevels := GetSupportedThinkingLevels(model)
	if slices.Contains(availableLevels, level) {
		return level
	}
	fallback := ModelThinkingLevelOff
	if len(availableLevels) > 0 {
		fallback = availableLevels[0]
	}
	requestedIndex := slices.Index(modelsExtendedThinkingLevels, level)
	if requestedIndex == -1 {
		return fallback
	}
	for i := requestedIndex; i < len(modelsExtendedThinkingLevels); i++ {
		candidate := modelsExtendedThinkingLevels[i]
		if slices.Contains(availableLevels, candidate) {
			return candidate
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		candidate := modelsExtendedThinkingLevels[i]
		if slices.Contains(availableLevels, candidate) {
			return candidate
		}
	}
	return fallback
}

// ModelsAreEqual reports whether a and b have the same type, id, and provider.
// A nil model is not equal to anything.
func ModelsAreEqual(a, b AnyModel) bool {
	if modelsIsNilModel(a) || modelsIsNilModel(b) {
		return false
	}
	pa, _, _ := modelsModelMetadata(a)
	pb, _, _ := modelsModelMetadata(b)
	return GetModelType(a) == GetModelType(b) && modelsModelId(a) == modelsModelId(b) && pa == pb
}
