// Ported from packages/ai/src/models.ts (pi v1.0.0).

package ai

import (
	"context"
	"sync"

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
}

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
}

// modelsAppliedRequest is a model plus resolved request auth, without transformHeaders.
type modelsAppliedRequest struct {
	Model   AnyModel
	ApiKey  *string
	Headers ProviderHeaders
	Env     ProviderEnv
}

var _ MutableModels = (*modelsModelsImpl)(nil)

// modelsHasKnownModelType reports whether model.type is known to this version.
// Missing type is chat.
func modelsHasKnownModelType(model AnyModel) bool {
	panic("unported: modelsHasKnownModelType")
}

// modelsWithKnownModelTypes returns entry with unknown model types removed.
func modelsWithKnownModelTypes(entry *ModelsStoreEntry) *ModelsStoreEntry {
	panic("unported: modelsWithKnownModelTypes")
}

// modelsMergeHeaders overlays override onto base. Names match case-insensitively;
// the override name replaces the existing one and keeps the override spelling.
func modelsMergeHeaders(base, override ProviderHeaders) ProviderHeaders {
	panic("unported: modelsMergeHeaders")
}

// CreateModels returns an empty collection. Nil options selects in-memory credentials,
// an in-memory models store, and the default auth context.
func CreateModels(options *CreateModelsOptions) MutableModels {
	panic("unported: CreateModels")
}

func (m *modelsModelsImpl) SetProvider(provider *Provider) {
	panic("unported: SetProvider")
}

func (m *modelsModelsImpl) DeleteProvider(id string) {
	panic("unported: DeleteProvider")
}

func (m *modelsModelsImpl) ClearProviders() {
	panic("unported: ClearProviders")
}

func (m *modelsModelsImpl) GetProviders() []*Provider {
	panic("unported: GetProviders")
}

func (m *modelsModelsImpl) GetProvider(id string) *Provider {
	panic("unported: GetProvider")
}

func (m *modelsModelsImpl) GetModels(provider *string) []*Model {
	panic("unported: GetModels")
}

func (m *modelsModelsImpl) GetAllModels(provider *string) []AnyModel {
	panic("unported: GetAllModels")
}

func (m *modelsModelsImpl) GetModelsOfType(typ ModelType, provider *string) []AnyModel {
	panic("unported: GetModelsOfType")
}

func (m *modelsModelsImpl) GetModel(provider string, id string) *Model {
	panic("unported: GetModel")
}

func (m *modelsModelsImpl) GetModelOfType(typ ModelType, provider string, id string) AnyModel {
	panic("unported: GetModelOfType")
}

func (m *modelsModelsImpl) supersedeProviderRefresh(providerId string) int {
	panic("unported: supersedeProviderRefresh")
}

func (m *modelsModelsImpl) beginProviderRefresh(parent context.Context, providerId string) (context.Context, *modelsRefreshCall) {
	panic("unported: beginProviderRefresh")
}

func (m *modelsModelsImpl) publishProviderModels(ctx context.Context, providerId string, generation int, publication *ModelsPublication) (bool, error) {
	panic("unported: publishProviderModels")
}

func (m *modelsModelsImpl) runProviderRefreshPhase(ctx context.Context, provider *Provider, credential Credential, allowNetwork bool, force *bool, generation int) error {
	panic("unported: runProviderRefreshPhase")
}

func (m *modelsModelsImpl) Refresh(ctx context.Context, options *ModelsRefreshOptions) (ModelsRefreshResult, error) {
	panic("unported: Refresh")
}

func (m *modelsModelsImpl) resolveRefreshCredential(ctx context.Context, provider *Provider, stored Credential) (Credential, error) {
	panic("unported: resolveRefreshCredential")
}

func (m *modelsModelsImpl) readCredential(ctx context.Context, providerId string) (Credential, error) {
	panic("unported: readCredential")
}

func (m *modelsModelsImpl) checkProviderAuth(ctx context.Context, provider *Provider, credential Credential) (*AuthCheck, error) {
	panic("unported: checkProviderAuth")
}

func (m *modelsModelsImpl) CheckAuth(ctx context.Context, providerId string, options *AuthOperationOptions) (*AuthCheck, error) {
	panic("unported: CheckAuth")
}

func (m *modelsModelsImpl) getAuthenticatedProviders(ctx context.Context, providerId *string) ([]modelsAuthenticatedProvider, error) {
	panic("unported: getAuthenticatedProviders")
}

func (m *modelsModelsImpl) GetAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]*Model, error) {
	panic("unported: GetAvailable")
}

func (m *modelsModelsImpl) GetAvailableOfType(ctx context.Context, typ ModelType, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	panic("unported: GetAvailableOfType")
}

func (m *modelsModelsImpl) GetAllAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	panic("unported: GetAllAvailable")
}

func (m *modelsModelsImpl) GetAuth(ctx context.Context, providerId string, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	panic("unported: GetAuth")
}

func (m *modelsModelsImpl) GetAuthModel(ctx context.Context, model AnyModel, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	panic("unported: GetAuthModel")
}

func (m *modelsModelsImpl) Login(ctx context.Context, providerId string, authType AuthType, interaction AuthInteraction, options *LoginOptions) (Credential, error) {
	panic("unported: Login")
}

func (m *modelsModelsImpl) Logout(ctx context.Context, providerId string, options *AuthOperationOptions) error {
	panic("unported: Logout")
}

func (m *modelsModelsImpl) requireProvider(model AnyModel) (*Provider, error) {
	panic("unported: requireProvider")
}

func (m *modelsModelsImpl) requireChatProvider(model *Model) (*Provider, error) {
	panic("unported: requireChatProvider")
}

func (m *modelsModelsImpl) applyAuth(ctx context.Context, model AnyModel, req *modelsRequestAuth) (*modelsAppliedRequest, error) {
	panic("unported: applyAuth")
}

func (m *modelsModelsImpl) Stream(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) *AssistantMessageEventStream {
	panic("unported: Stream")
}

func (m *modelsModelsImpl) Complete(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) (*AssistantMessage, error) {
	panic("unported: Complete")
}

func (m *modelsModelsImpl) StreamSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream {
	panic("unported: StreamSimple")
}

func (m *modelsModelsImpl) CompleteSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error) {
	panic("unported: CompleteSimple")
}

func (m *modelsModelsImpl) StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream {
	panic("unported: StreamDeferred")
}

func (m *modelsModelsImpl) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error) {
	panic("unported: FetchDeferred")
}

func (m *modelsModelsImpl) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error {
	panic("unported: CancelDeferred")
}

func (m *modelsModelsImpl) GenerateImages(ctx context.Context, model *ImageModel, c *ImagesContext, options *ModelsImagesOptions) (*AssistantImages, error) {
	panic("unported: GenerateImages")
}

func (m *modelsModelsImpl) Classify(ctx context.Context, model *ClassifierModel, c *ClassifierContext, options *ModelsClassifierOptions) (*ClassifierResult, error) {
	panic("unported: Classify")
}

// CreateProvider builds a provider from static models and API implementations.
// It rejects a provider with no chat, image, or classifier implementation.
func CreateProvider(input *CreateProviderOptions) (*Provider, error) {
	panic("unported: CreateProvider")
}

// HasApi reports whether model is a chat model whose api equals api.
// Non-chat models do not match, even when their api id is equal.
func HasApi(model AnyModel, api string) bool {
	panic("unported: HasApi")
}

// CalculateCost writes usage.Cost from model.Cost and returns it.
// The highest tier whose inputTokensAbove is below total input usage applies.
// cacheWrite1h is billed at twice the input rate; the rest of cacheWrite uses cacheWrite.
func CalculateCost(model AnyModel, usage *Usage) *UsageCost {
	panic("unported: CalculateCost")
}

// GetSupportedThinkingLevels returns the thinking levels model accepts, in clamp order.
// A model without reasoning accepts only "off".
func GetSupportedThinkingLevels(model *Model) []ModelThinkingLevel {
	panic("unported: GetSupportedThinkingLevels")
}

// ClampThinkingLevel returns level when model supports it, otherwise the nearest
// supported level at or above it, then below it, then the first supported level.
func ClampThinkingLevel(model *Model, level ModelThinkingLevel) ModelThinkingLevel {
	panic("unported: ClampThinkingLevel")
}

// ModelsAreEqual reports whether a and b have the same type, id, and provider.
// A nil model is not equal to anything.
func ModelsAreEqual(a, b AnyModel) bool {
	panic("unported: ModelsAreEqual")
}
