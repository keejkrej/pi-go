// Ported from packages/ai/src/utils/identity.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// UserAgentModeKind identifies built-in User-Agent presets ("short" | "detailed").
type UserAgentModeKind string

const (
	UserAgentModeShort    UserAgentModeKind = "short"
	UserAgentModeDetailed UserAgentModeKind = "detailed"
)

func (UserAgentModeKind) isUserAgentMode() {}

// UserAgentMode is a sealed union of UserAgentModeKind ("short" | "detailed"),
// *UserAgentModeCustom ({ custom: string }), and *UnknownUserAgentMode.
type UserAgentMode interface {
	isUserAgentMode()
}

// UserAgentModeCustom specifies a custom User-Agent string ({ custom: string }).
type UserAgentModeCustom struct {
	Custom string `json:"custom"`
}

func (*UserAgentModeCustom) isUserAgentMode() {}

// UnknownUserAgentMode preserves an unrecognized JSON representation.
type UnknownUserAgentMode struct {
	Raw any
}

func (*UnknownUserAgentMode) isUserAgentMode() {}

func (u *UnknownUserAgentMode) MarshalJSON() ([]byte, error) {
	if u == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

// UserAgentModeValue is a JSON-serializable holder for UserAgentMode.
type UserAgentModeValue struct {
	Val UserAgentMode
}

func (v UserAgentModeValue) IsZero() bool { return v.Val == nil }

func (v UserAgentModeValue) MarshalJSON() ([]byte, error) {
	if v.Val == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.Val)
}

func (v *UserAgentModeValue) UnmarshalJSON(data []byte) error {
	decoded, err := UnmarshalUserAgentMode(data)
	if err != nil {
		return err
	}
	v.Val = decoded
	return nil
}

// DecodeUserAgentMode decodes a jsonx value into UserAgentMode.
func DecodeUserAgentMode(v any) (UserAgentMode, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		switch UserAgentModeKind(s) {
		case UserAgentModeShort, UserAgentModeDetailed:
			return UserAgentModeKind(s), nil
		default:
			return &UnknownUserAgentMode{Raw: v}, nil
		}
	}
	if obj, ok := v.(*jsonx.Object); ok && obj != nil {
		if custom, exists := obj.GetString("custom"); exists {
			return &UserAgentModeCustom{Custom: custom}, nil
		}
	}
	return &UnknownUserAgentMode{Raw: v}, nil
}

// UnmarshalUserAgentMode parses JSON bytes into UserAgentMode.
func UnmarshalUserAgentMode(data []byte) (UserAgentMode, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeUserAgentMode(v)
}

// ClientAttribution identifies the calling application for provider attribution headers.
type ClientAttribution struct {
	Name       string   `json:"name"`
	Url        string   `json:"url"`
	Categories []string `json:"categories,omitzero"`
}

// PiAttribution is pi's standard client attribution.
var PiAttribution = ClientAttribution{
	Name:       "pi",
	Url:        "https://pi.dev",
	Categories: []string{"cli-agent"},
}

// ClientIdentity configures how pi identifies requests to providers.
type ClientIdentity struct {
	UserAgent   UserAgentModeValue           `json:"userAgent,omitzero"`
	Attribution jsonx.Opt[ClientAttribution] `json:"attribution,omitzero"`
	Version     *string                      `json:"version,omitzero"`
}

// ClientIdentityTarget describes the destination provider and base URL of a request.
type ClientIdentityTarget struct {
	Provider string `json:"provider"`
	BaseUrl  string `json:"baseUrl"`
}

const (
	uiNvidiaNimHost = "integrate.api.nvidia.com"
	uiOpenCodeHost  = "opencode.ai"
)

func uiCloneIdentity(src *ClientIdentity) *ClientIdentity {
	if src == nil {
		return nil
	}
	cp := &ClientIdentity{
		UserAgent:   src.UserAgent,
		Attribution: src.Attribution,
	}
	if attr, ok := src.Attribution.Get(); ok {
		attrCopy := attr
		if attr.Categories != nil {
			attrCopy.Categories = append([]string(nil), attr.Categories...)
		}
		cp.Attribution = jsonx.Some(attrCopy)
	}
	if src.Version != nil {
		v := *src.Version
		cp.Version = &v
	}
	return cp
}

// MergeClientIdentity merges identities field by field; later sources override earlier ones,
// while unset fields are skipped. Explicitly setting Attribution to jsonx.Null clears earlier attribution.
func MergeClientIdentity(sources ...*ClientIdentity) *ClientIdentity {
	var merged *ClientIdentity
	for _, source := range sources {
		if source == nil {
			continue
		}
		if merged == nil {
			merged = &ClientIdentity{}
		}
		if !source.UserAgent.IsZero() {
			merged.UserAgent = source.UserAgent
		}
		if !source.Attribution.IsZero() {
			if attr, ok := source.Attribution.Get(); ok {
				attrCopy := attr
				if attr.Categories != nil {
					attrCopy.Categories = append([]string(nil), attr.Categories...)
				}
				merged.Attribution = jsonx.Some(attrCopy)
			} else {
				merged.Attribution = jsonx.Null[ClientAttribution]()
			}
		}
		if source.Version != nil {
			v := *source.Version
			merged.Version = &v
		}
	}
	return merged
}

// GetClientVersion returns the client version configured in identity, defaulting to PiAiVersion.
func GetClientVersion(identity *ClientIdentity) string {
	if identity != nil && identity.Version != nil {
		return *identity.Version
	}
	return PiAiVersion
}

func uiRuntimeDescription() string {
	ver := strings.TrimPrefix(runtime.Version(), "go")
	return "go/" + ver
}

// FormatUserAgent formats the User-Agent header value for identity.
func FormatUserAgent(identity *ClientIdentity) string {
	var mode UserAgentMode = UserAgentModeDetailed
	if identity != nil && identity.UserAgent.Val != nil {
		mode = identity.UserAgent.Val
	}
	switch m := mode.(type) {
	case *UserAgentModeCustom:
		if m != nil {
			return m.Custom
		}
	case UserAgentModeKind:
		if m == UserAgentModeShort {
			return "pi"
		}
	}
	return fmt.Sprintf("pi/%s (%s; %s; %s)", GetClientVersion(identity), js.Platform(), uiRuntimeDescription(), js.Arch())
}

func uiMatchesHost(baseUrl, expectedHost string) bool {
	u, err := url.Parse(baseUrl)
	if err != nil {
		return false
	}
	return u.Hostname() == expectedHost
}

func uiIsOpenRouter(target ClientIdentityTarget) bool {
	return target.Provider == "openrouter" || strings.Contains(target.BaseUrl, "openrouter.ai")
}

func uiIsNvidiaNim(target ClientIdentityTarget) bool {
	return target.Provider == "nvidia" || uiMatchesHost(target.BaseUrl, uiNvidiaNimHost)
}

func uiIsOpenCode(target ClientIdentityTarget) bool {
	return target.Provider == "opencode" || target.Provider == "opencode-go" || uiMatchesHost(target.BaseUrl, uiOpenCodeHost)
}

// GetAttributionHeaders returns provider-specific attribution headers for target and identity.
func GetAttributionHeaders(target ClientIdentityTarget, identity *ClientIdentity) *omap.Map[string, string] {
	out := omap.NewMap[string, string]()
	if identity == nil {
		return out
	}
	attr, ok := identity.Attribution.Get()
	if !ok {
		return out
	}
	if uiIsOpenRouter(target) {
		out.Set("HTTP-Referer", attr.Url)
		out.Set("X-OpenRouter-Title", attr.Name)
		if len(attr.Categories) > 0 {
			out.Set("X-OpenRouter-Categories", strings.Join(attr.Categories, ","))
		}
		return out
	}
	if uiIsNvidiaNim(target) {
		out.Set("X-BILLING-INVOKE-ORIGIN", uiCapitalizeFirst(attr.Name))
		return out
	}
	if uiIsOpenCode(target) {
		out.Set("x-opencode-client", attr.Name)
		return out
	}
	return out
}

func uiCapitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// GetOpenCodeSessionHeaders returns the x-opencode-session header map when targeting
// OpenCode with a non-empty sessionId, or nil otherwise.
func GetOpenCodeSessionHeaders(target ClientIdentityTarget, sessionId *string) *omap.Map[string, string] {
	if sessionId == nil || *sessionId == "" || !uiIsOpenCode(target) {
		return nil
	}
	out := omap.NewMap[string, string]()
	out.Set("x-opencode-session", *sessionId)
	return out
}

// GetIdentityHeaders returns User-Agent and provider attribution headers for target and identity.
func GetIdentityHeaders(target ClientIdentityTarget, identity *ClientIdentity) *omap.Map[string, string] {
	out := omap.NewMap[string, string]()
	out.Set("User-Agent", FormatUserAgent(identity))
	for k, v := range GetAttributionHeaders(target, identity).All() {
		out.Set(k, v)
	}
	return out
}

func uiToProviderHeaders(m *omap.Map[string, string]) ProviderHeaders {
	if m == nil || m.Len() == 0 {
		return nil
	}
	out := omap.NewMap[string, *string]()
	for k, v := range m.All() {
		val := v
		out.Set(k, &val)
	}
	return out
}

// WithClientIdentity wraps models with identity as the default request identity
// (preserving MutableModels capability when models is mutable).
func WithClientIdentity(models Models, identity *ClientIdentity) Models {
	baseId := uiCloneIdentity(identity)
	wrapped := &uiModelsWithIdentity{
		inner:    models,
		identity: baseId,
	}
	if mut, ok := models.(MutableModels); ok {
		return &uiMutableModelsWithIdentity{
			uiModelsWithIdentity: wrapped,
			mutable:              mut,
		}
	}
	return wrapped
}

// WithClientIdentityMutable wraps a MutableModels collection with identity as its default request identity.
func WithClientIdentityMutable(models MutableModels, identity *ClientIdentity) MutableModels {
	baseId := uiCloneIdentity(identity)
	return &uiMutableModelsWithIdentity{
		uiModelsWithIdentity: &uiModelsWithIdentity{
			inner:    models,
			identity: baseId,
		},
		mutable: models,
	}
}

type uiModelsWithIdentity struct {
	inner    Models
	identity *ClientIdentity
}

type uiMutableModelsWithIdentity struct {
	*uiModelsWithIdentity
	mutable MutableModels
}

func (m *uiMutableModelsWithIdentity) SetProvider(provider *Provider) {
	m.mutable.SetProvider(provider)
}

func (m *uiMutableModelsWithIdentity) DeleteProvider(id string) {
	m.mutable.DeleteProvider(id)
}

func (m *uiMutableModelsWithIdentity) ClearProviders() {
	m.mutable.ClearProviders()
}

func (m *uiModelsWithIdentity) merged(reqIdentity *ClientIdentity) *ClientIdentity {
	return MergeClientIdentity(m.identity, reqIdentity)
}

func (m *uiModelsWithIdentity) GetProviders() []*Provider       { return m.inner.GetProviders() }
func (m *uiModelsWithIdentity) GetProvider(id string) *Provider { return m.inner.GetProvider(id) }
func (m *uiModelsWithIdentity) GetModels(provider *string) []*Model {
	return m.inner.GetModels(provider)
}
func (m *uiModelsWithIdentity) GetModel(provider, id string) *Model {
	return m.inner.GetModel(provider, id)
}
func (m *uiModelsWithIdentity) GetModelsOfType(typ ModelType, provider *string) []AnyModel {
	return m.inner.GetModelsOfType(typ, provider)
}
func (m *uiModelsWithIdentity) GetModelOfType(typ ModelType, provider, id string) AnyModel {
	return m.inner.GetModelOfType(typ, provider, id)
}
func (m *uiModelsWithIdentity) GetAllModels(provider *string) []AnyModel {
	return m.inner.GetAllModels(provider)
}
func (m *uiModelsWithIdentity) Refresh(ctx context.Context, options *ModelsRefreshOptions) (ModelsRefreshResult, error) {
	return m.inner.Refresh(ctx, options)
}
func (m *uiModelsWithIdentity) CheckAuth(ctx context.Context, providerId string, options *AuthOperationOptions) (*AuthCheck, error) {
	return m.inner.CheckAuth(ctx, providerId, options)
}
func (m *uiModelsWithIdentity) GetAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]*Model, error) {
	return m.inner.GetAvailable(ctx, providerId, options)
}
func (m *uiModelsWithIdentity) GetAvailableOfType(ctx context.Context, typ ModelType, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	return m.inner.GetAvailableOfType(ctx, typ, providerId, options)
}
func (m *uiModelsWithIdentity) GetAllAvailable(ctx context.Context, providerId *string, options *AuthOperationOptions) ([]AnyModel, error) {
	return m.inner.GetAllAvailable(ctx, providerId, options)
}
func (m *uiModelsWithIdentity) GetAuth(ctx context.Context, providerId string, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	return m.inner.GetAuth(ctx, providerId, overrides)
}
func (m *uiModelsWithIdentity) GetAuthModel(ctx context.Context, model AnyModel, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	return m.inner.GetAuthModel(ctx, model, overrides)
}
func (m *uiModelsWithIdentity) Login(ctx context.Context, providerId string, authType AuthType, interaction AuthInteraction, options *LoginOptions) (Credential, error) {
	return m.inner.Login(ctx, providerId, authType, interaction, options)
}
func (m *uiModelsWithIdentity) Logout(ctx context.Context, providerId string, options *AuthOperationOptions) error {
	return m.inner.Logout(ctx, providerId, options)
}

func uiCopyProviderStreamOptionsWithIdentity(opts ProviderStreamOptions, defaultId *ClientIdentity) ProviderStreamOptions {
	if opts == nil {
		return &StreamOptions{
			ProviderRequestOptions: ProviderRequestOptions[Model]{
				Identity: MergeClientIdentity(defaultId, nil),
			},
		}
	}
	base := opts.BaseStreamOptions()
	var reqId *ClientIdentity
	if base != nil {
		reqId = base.Identity
	}
	merged := MergeClientIdentity(defaultId, reqId)

	// Shallow copy the concrete struct so the caller's options struct is never mutated.
	rv := reflect.ValueOf(opts)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.Identity = merged
			}
			return copiedOpts
		}
	}
	if base != nil {
		cp := *base
		cp.Identity = merged
		return &cp
	}
	return &StreamOptions{
		ProviderRequestOptions: ProviderRequestOptions[Model]{Identity: merged},
	}
}

func (m *uiModelsWithIdentity) wrapApiStreamOptions(options *ModelsApiStreamOptions) *ModelsApiStreamOptions {
	var out ModelsApiStreamOptions
	if options != nil {
		out = *options
	}
	out.ProviderStreamOptions = uiCopyProviderStreamOptionsWithIdentity(out.ProviderStreamOptions, m.identity)
	return &out
}

func (m *uiModelsWithIdentity) wrapSimpleOptions(options *ModelsSimpleStreamOptions) *ModelsSimpleStreamOptions {
	var out ModelsSimpleStreamOptions
	if options != nil {
		out = *options
	}
	out.Identity = m.merged(out.Identity)
	return &out
}

func (m *uiModelsWithIdentity) wrapDeferredFetchOptions(options *ModelsDeferredFetchOptions) *ModelsDeferredFetchOptions {
	var out ModelsDeferredFetchOptions
	if options != nil {
		out = *options
	}
	out.Identity = m.merged(out.Identity)
	return &out
}

func (m *uiModelsWithIdentity) wrapDeferredCancelOptions(options *ModelsDeferredCancelOptions) *ModelsDeferredCancelOptions {
	var out ModelsDeferredCancelOptions
	if options != nil {
		out = *options
	}
	out.Identity = m.merged(out.Identity)
	return &out
}

func (m *uiModelsWithIdentity) wrapImagesOptions(options *ModelsImagesOptions) *ModelsImagesOptions {
	var out ModelsImagesOptions
	if options != nil {
		out = *options
	}
	out.Identity = m.merged(out.Identity)
	return &out
}

func (m *uiModelsWithIdentity) wrapClassifierOptions(options *ModelsClassifierOptions) *ModelsClassifierOptions {
	var out ModelsClassifierOptions
	if options != nil {
		out = *options
	}
	out.Identity = m.merged(out.Identity)
	return &out
}

func (m *uiModelsWithIdentity) Stream(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) *AssistantMessageEventStream {
	return m.inner.Stream(ctx, model, c, m.wrapApiStreamOptions(options))
}
func (m *uiModelsWithIdentity) Complete(ctx context.Context, model *Model, c *Context, options *ModelsApiStreamOptions) (*AssistantMessage, error) {
	return m.inner.Complete(ctx, model, c, m.wrapApiStreamOptions(options))
}
func (m *uiModelsWithIdentity) StreamSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream {
	return m.inner.StreamSimple(ctx, model, c, m.wrapSimpleOptions(options))
}
func (m *uiModelsWithIdentity) CompleteSimple(ctx context.Context, model *Model, c *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error) {
	return m.inner.CompleteSimple(ctx, model, c, m.wrapSimpleOptions(options))
}
func (m *uiModelsWithIdentity) StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream {
	return m.inner.StreamDeferred(ctx, model, handle, m.wrapDeferredFetchOptions(options))
}
func (m *uiModelsWithIdentity) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error) {
	return m.inner.FetchDeferred(ctx, model, handle, m.wrapDeferredFetchOptions(options))
}
func (m *uiModelsWithIdentity) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error {
	return m.inner.CancelDeferred(ctx, model, handle, m.wrapDeferredCancelOptions(options))
}
func (m *uiModelsWithIdentity) GenerateImages(ctx context.Context, model *ImageModel, c *ImagesContext, options *ModelsImagesOptions) (*AssistantImages, error) {
	return m.inner.GenerateImages(ctx, model, c, m.wrapImagesOptions(options))
}
func (m *uiModelsWithIdentity) Classify(ctx context.Context, model *ClassifierModel, c *ClassifierContext, options *ModelsClassifierOptions) (*ClassifierResult, error) {
	return m.inner.Classify(ctx, model, c, m.wrapClassifierOptions(options))
}
