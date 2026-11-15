// Ported from packages/coding-agent/src/core/model-runtime.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
)

type mruRegisteredVirtualModel struct {
	model *ai.Model
	route func(ctx context.Context, request ModelRouteRequest) (ModelRoute, error)
}

type mruSnapshot struct {
	all                 []*ai.Model
	available           []*ai.Model
	configuredProviders map[string]struct{}
	storedProviders     map[string]struct{}
	auth                map[string]*ai.AuthCheck
}

// CreateModelRuntimeOptions configures CreateModelRuntime.
type CreateModelRuntimeOptions struct {
	// Credentials overrides credential storage. Nil creates AuthStorage at AuthPath (or default auth.json).
	Credentials ai.CredentialStore
	// AuthPath overrides auth.json path when Credentials is nil.
	AuthPath *string
	// ModelsPath overrides models.json path. Nil uses utils.GetModelsPath(); &"" disables loading models.json (TS null).
	ModelsPath *string
	// ModelsStore overrides dynamic catalog storage.
	ModelsStore ai.ModelsStore
	// ModelsStorePath overrides models-store.json path when ModelsStore is nil and ModelsPath is enabled.
	ModelsStorePath *string
	// AllowModelNetwork allows CreateModelRuntime's initial refresh to fetch remote catalogs. Defaults to false.
	AllowModelNetwork bool
	// ModelRefreshTimeoutMs bounds the create-time network model refresh in milliseconds.
	ModelRefreshTimeoutMs *int64
	// CatalogBaseUrl overrides the remote model catalog base URL.
	CatalogBaseUrl *string
	// RefreshOnCreate controls initial cache restoration and availability checks. Nil defaults to true.
	RefreshOnCreate *bool
	// Identity sets optional default client identity headers/metadata on underlying models.
	Identity *ai.ClientIdentity
}

// ModelRuntimeAuthOverrides configures explicit auth overrides for GetAuth/GetAuthModel.
type ModelRuntimeAuthOverrides = ai.AuthResolutionOverrides

// CredentialSynchronizationOperation identifies the credential mutation whose follow-up sync failed.
type CredentialSynchronizationOperation string

const (
	CredentialSyncOpLogin            CredentialSynchronizationOperation = "login"
	CredentialSyncOpLogout           CredentialSynchronizationOperation = "logout"
	CredentialSyncOpSetRuntimeApiKey CredentialSynchronizationOperation = "setRuntimeApiKey"
	CredentialSyncOpRemoveRuntimeKey CredentialSynchronizationOperation = "removeRuntimeApiKey"
)

// CredentialSynchronizationError reports that a credential mutation committed,
// but synchronizing the local model/auth snapshot failed.
type CredentialSynchronizationError struct {
	ProviderId string
	Operation  CredentialSynchronizationOperation
	Credential ai.Credential
	Cause      error
}

func (e *CredentialSynchronizationError) Error() string {
	if e == nil {
		return ""
	}
	msg := fmt.Sprintf("Credential %s committed for %s, but local synchronization failed", e.Operation, e.ProviderId)
	if e.Cause != nil {
		return msg + ": " + e.Cause.Error()
	}
	return msg
}

func (e *CredentialSynchronizationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ResolveModelOptions configures ModelRuntime.ResolveModel.
type ResolveModelOptions struct {
	Reason        ModelRouteReason
	ThinkingLevel ai.ModelThinkingLevel
	Failed        *ai.AssistantMessage
	State         any
}

func mruMergeHeaders(base, override ai.ProviderHeaders) ai.ProviderHeaders {
	if base == nil && override == nil {
		return nil
	}
	merged := omap.NewMap[string, *string]()
	if base != nil {
		for k, v := range base.All() {
			merged.Set(k, v)
		}
	}
	if override != nil {
		for name, value := range override.All() {
			lowerName := strings.ToLower(name)
			for _, existingName := range merged.Keys() {
				if strings.ToLower(existingName) == lowerName {
					merged.Delete(existingName)
				}
			}
			merged.Set(name, value)
		}
	}
	return merged
}

func mruStringMapToProviderHeaders(src *omap.Map[string, string]) ai.ProviderHeaders {
	if src == nil || src.Len() == 0 {
		return nil
	}
	out := omap.NewMap[string, *string]()
	for k, v := range src.All() {
		val := v
		out.Set(k, &val)
	}
	return out
}

func mruAnyModelProvider(model ai.AnyModel) string {
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			return string(m.Provider)
		}
	case *ai.ImageModel:
		if m != nil {
			return string(m.Provider)
		}
	case *ai.ClassifierModel:
		if m != nil {
			return string(m.Provider)
		}
	}
	return ""
}

func mruAnyModelTarget(model ai.AnyModel) ai.ClientIdentityTarget {
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			return ai.ClientIdentityTarget{Provider: string(m.Provider), BaseUrl: m.BaseUrl}
		}
	case *ai.ImageModel:
		if m != nil {
			return ai.ClientIdentityTarget{Provider: string(m.Provider), BaseUrl: m.BaseUrl}
		}
	case *ai.ClassifierModel:
		if m != nil {
			return ai.ClientIdentityTarget{Provider: string(m.Provider), BaseUrl: m.BaseUrl}
		}
	}
	return ai.ClientIdentityTarget{}
}

func mruAnyModelWithBaseUrl(model ai.AnyModel, baseUrl *string) ai.AnyModel {
	if baseUrl == nil || *baseUrl == "" {
		return model
	}
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			cp := *m
			cp.BaseUrl = *baseUrl
			return &cp
		}
	case *ai.ImageModel:
		if m != nil {
			cp := *m
			cp.BaseUrl = *baseUrl
			return &cp
		}
	case *ai.ClassifierModel:
		if m != nil {
			cp := *m
			cp.BaseUrl = *baseUrl
			return &cp
		}
	}
	return model
}

func mruCloneProviderStreamOptions[TModel any](
	opts ai.ProviderStreamOptions,
	apiKey *string,
	headers ai.ProviderHeaders,
	env ai.ProviderEnv,
) ai.ProviderStreamOptions {
	if opts == nil {
		return &ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:  apiKey,
				Headers: headers,
				Env:     env,
			},
		}
	}
	rv := reflect.ValueOf(opts)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ai.ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.ApiKey = apiKey
				cpBase.Headers = headers
				cpBase.Env = env
			}
			return copiedOpts
		}
	}
	if base := opts.BaseStreamOptions(); base != nil {
		cp := *base
		cp.ApiKey = apiKey
		cp.Headers = headers
		cp.Env = env
		return &cp
	}
	return &ai.StreamOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
			ApiKey:  apiKey,
			Headers: headers,
			Env:     env,
		},
	}
}

// ModelRuntime is the configured ai.Models collection used by coding-agent and SDK consumers.
type ModelRuntime struct {
	mu sync.Mutex

	models                   ai.MutableModels
	credentials              *RuntimeCredentials
	defaultBuiltins          *omap.Map[string, *ai.Provider]
	builtins                 *omap.Map[string, *ai.Provider]
	nativeExtensionProviders *omap.Map[string, *ai.Provider]
	extensionProviders       *omap.Map[string, *ProviderConfig]
	// virtualModels stores virtual models keyed by providerId, then model id.
	virtualModels           map[string]*omap.Map[string, mruRegisteredVirtualModel]
	compositionErrors       *omap.Map[string, string]
	modelsPath              string
	modelNetworkEnabled     bool
	config                  *ModelConfig
	snapshot                mruSnapshot
	availabilityRefreshSeq  uint64
	availabilityErrorSeq    uint64
	providerAvailabilitySeq map[string]uint64
	availabilityError       *string
	credentialOps           map[string]chan struct{}
	availSem                chan struct{}
	asyncRefreshMu          sync.Mutex
	asyncRefreshRunning     bool
	asyncRefreshQueued      bool
	asyncRefreshClosed      bool
	asyncRefreshGen         uint64
	asyncRefreshCancel      context.CancelFunc
	asyncRefreshDone        chan struct{}
}

var _ ai.Models = (*ModelRuntime)(nil)

// CreateModelRuntime initializes a ModelRuntime, loads models.json and cached catalogs, and checks auth.
func CreateModelRuntime(ctx context.Context, options ...*CreateModelRuntimeOptions) (*ModelRuntime, error) {
	ctx = utils.OperationSignal(ctx)
	var opts CreateModelRuntimeOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	var baseCreds ai.CredentialStore = opts.Credentials
	if baseCreds == nil {
		if opts.AuthPath != nil && *opts.AuthPath != "" {
			baseCreds = AuthStorageCreate(*opts.AuthPath)
		} else {
			baseCreds = AuthStorageCreate()
		}
	}
	credentials := NewRuntimeCredentials(baseCreds)

	modelsPath := utils.GetModelsPath()
	if opts.ModelsPath != nil {
		modelsPath = *opts.ModelsPath
	}

	config := LoadModelConfig(modelsPath)

	var modelsStore ai.ModelsStore = opts.ModelsStore
	if modelsStore == nil {
		if modelsPath != "" {
			storePath := filepath.Join(filepath.Dir(modelsPath), "models-store.json")
			if opts.ModelsStorePath != nil && *opts.ModelsStorePath != "" {
				storePath = *opts.ModelsStorePath
			}
			modelsStore = NewFileModelsStore(storePath)
		} else {
			modelsStore = NewInMemoryCodingAgentModelsStore()
		}
	}

	catalogBaseUrl := ""
	if opts.CatalogBaseUrl != nil {
		catalogBaseUrl = *opts.CatalogBaseUrl
	}
	builtinGeneratedAt := providers.GetBuiltinModelDataGeneratedAt()
	builtinRaw := providers.BuiltinProviders()
	wrappedBuiltins := make([]*ai.Provider, 0, len(builtinRaw))
	for _, provider := range builtinRaw {
		if provider == nil {
			continue
		}
		if provider.Id == RadiusProviderId {
			wrappedBuiltins = append(wrappedBuiltins, provider)
			continue
		}
		if provider.Id == string(ai.KnownProviderOpenrouter) {
			wrappedBuiltins = append(
				wrappedBuiltins,
				WithPersonalizedCatalog(provider, providers.PersonalizeOpenRouterCatalog, catalogBaseUrl, builtinGeneratedAt),
			)
			continue
		}
		wrappedBuiltins = append(wrappedBuiltins, WithRemoteCatalog(provider, catalogBaseUrl, builtinGeneratedAt))
	}

	_, offline := os.LookupEnv("PI_OFFLINE")
	modelNetworkEnabled := !offline

	defaultBuiltins := omap.NewMap[string, *ai.Provider]()
	builtins := omap.NewMap[string, *ai.Provider]()
	for _, p := range wrappedBuiltins {
		defaultBuiltins.Set(p.Id, p)
		builtins.Set(p.Id, p)
	}

	runtime := &ModelRuntime{
		models: ai.CreateModels(&ai.CreateModelsOptions{
			Credentials: credentials,
			ModelsStore: modelsStore,
			Identity:    opts.Identity,
		}),
		credentials:              credentials,
		defaultBuiltins:          defaultBuiltins,
		builtins:                 builtins,
		nativeExtensionProviders: omap.NewMap[string, *ai.Provider](),
		extensionProviders:       omap.NewMap[string, *ProviderConfig](),
		virtualModels:            make(map[string]*omap.Map[string, mruRegisteredVirtualModel]),
		compositionErrors:        omap.NewMap[string, string](),
		modelsPath:               modelsPath,
		modelNetworkEnabled:      modelNetworkEnabled,
		config:                   config,
		snapshot: mruSnapshot{
			configuredProviders: make(map[string]struct{}),
			storedProviders:     make(map[string]struct{}),
			auth:                make(map[string]*ai.AuthCheck),
		},
		providerAvailabilitySeq: make(map[string]uint64),
		credentialOps:           make(map[string]chan struct{}),
		availSem:                make(chan struct{}, 1),
	}

	runtime.mu.Lock()
	runtime.configureRadiusProvidersLocked()
	runtime.rebuildProvidersLocked()
	runtime.mu.Unlock()

	refreshFromNetwork := runtime.modelNetworkEnabled && opts.AllowModelNetwork
	refreshCtx := ctx
	var cancel context.CancelFunc
	if refreshFromNetwork && opts.ModelRefreshTimeoutMs != nil {
		refreshCtx, cancel = context.WithTimeout(ctx, time.Duration(*opts.ModelRefreshTimeoutMs)*time.Millisecond)
		defer cancel()
	}

	refreshOnCreate := opts.RefreshOnCreate == nil || *opts.RefreshOnCreate
	if refreshOnCreate {
		if _, err := runtime.Refresh(refreshCtx, &ai.ModelsRefreshOptions{
			AllowNetwork: &refreshFromNetwork,
		}); err != nil {
			return nil, err
		}
	}

	return runtime, nil
}

func (r *ModelRuntime) configureRadiusProvidersLocked() {
	r.builtins = omap.NewMap[string, *ai.Provider]()
	for k, v := range r.defaultBuiltins.All() {
		r.builtins.Set(k, v)
	}
	for _, providerId := range r.config.GetProviderIds() {
		cfg := r.config.GetProvider(providerId)
		if cfg == nil || cfg.OAuth == nil || *cfg.OAuth != "radius" || cfg.BaseUrl == nil || *cfg.BaseUrl == "" {
			continue
		}
		name := providerId
		if cfg.Name != nil && *cfg.Name != "" {
			name = *cfg.Name
		}
		gw := mruStripTrailingV1(*cfg.BaseUrl)
		idCopy := providerId
		r.builtins.Set(providerId, providers.RadiusProvider(&providers.RadiusProviderOptions{
			Id:      &idCopy,
			Name:    &name,
			Gateway: &gw,
		}))
	}
}

func mruStripTrailingV1(baseUrl string) string {
	trimmed := strings.TrimSuffix(baseUrl, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return strings.TrimSuffix(trimmed, "/v1")
	}
	return baseUrl
}

func (r *ModelRuntime) providerIdsLocked() []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	add := func(id string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, id := range r.builtins.Keys() {
		add(id)
	}
	for _, id := range r.nativeExtensionProviders.Keys() {
		add(id)
	}
	for _, id := range r.config.GetProviderIds() {
		add(id)
	}
	for _, id := range r.extensionProviders.Keys() {
		add(id)
	}
	for id := range r.virtualModels {
		add(id)
	}
	return ids
}

func (r *ModelRuntime) recomposeProviderLocked(providerId string) *ai.Provider {
	provider := r.composeProviderLocked(providerId)
	var vModels []*ai.Model
	if vmMap := r.virtualModels[providerId]; vmMap != nil && vmMap.Len() > 0 {
		vModels = make([]*ai.Model, 0, vmMap.Len())
		for _, entry := range vmMap.All() {
			vModels = append(vModels, entry.model)
		}
	}
	if len(vModels) > 0 {
		r.models.SetProvider(WithVirtualModels(providerId, provider, vModels))
	} else if provider != nil {
		r.models.SetProvider(provider)
	} else {
		r.models.DeleteProvider(providerId)
	}
	return provider
}

func (r *ModelRuntime) composeProviderLocked(providerId string) *ai.Provider {
	base, ok := r.nativeExtensionProviders.Get(providerId)
	if !ok {
		base, _ = r.builtins.Get(providerId)
	}
	ext, hasExt := r.extensionProviders.Get(providerId)
	cfgProv := r.config.GetProvider(providerId)
	if cfgProv == nil && (!hasExt || ext == nil) {
		r.compositionErrors.Delete(providerId)
		return base
	}
	composed, err := ComposeModelProvider(providerId, base, r.config, ext)
	if err != nil {
		r.compositionErrors.Set(providerId, err.Error())
		return base
	}
	r.compositionErrors.Delete(providerId)
	return composed
}

func (r *ModelRuntime) rebuildProvidersLocked() {
	r.compositionErrors = omap.NewMap[string, string]()
	wantIds := r.providerIdsLocked()
	wantSet := make(map[string]struct{}, len(wantIds))
	for _, id := range wantIds {
		wantSet[id] = struct{}{}
	}
	for _, existing := range r.models.GetProviders() {
		if existing != nil {
			if _, keep := wantSet[existing.Id]; !keep {
				r.models.DeleteProvider(existing.Id)
			}
		}
	}
	for _, providerId := range wantIds {
		r.recomposeProviderLocked(providerId)
	}
	r.updateModelSnapshotLocked()
}

func (r *ModelRuntime) invalidateAvailabilityLocked() {
	r.availabilityRefreshSeq++
	for providerId, pseq := range r.providerAvailabilitySeq {
		r.providerAvailabilitySeq[providerId] = pseq + 1
	}
}

func (r *ModelRuntime) updateModelSnapshotLocked() {
	all := r.models.GetModels(nil)
	available := make([]*ai.Model, 0, len(all))
	for _, m := range all {
		if m == nil {
			continue
		}
		if _, ok := r.snapshot.configuredProviders[string(m.Provider)]; ok {
			available = append(available, m)
		}
	}
	r.snapshot = mruSnapshot{
		all:                 append([]*ai.Model(nil), all...),
		available:           available,
		configuredProviders: r.snapshot.configuredProviders,
		storedProviders:     r.snapshot.storedProviders,
		auth:                r.snapshot.auth,
	}
}

func (r *ModelRuntime) runAvailabilityRefresh(ctx context.Context, seq, errorSeq uint64) error {
	ctx = utils.OperationSignal(ctx)
	provList := r.models.GetProviders()

	type checkPair struct {
		id    string
		check *ai.AuthCheck
		err   error
	}

	var wg sync.WaitGroup
	var avail []*ai.Model
	var availErr error
	checks := make([]checkPair, len(provList))
	var creds []ai.CredentialInfo
	var credsErr error

	wg.Add(2 + len(provList))
	go func() {
		defer wg.Done()
		avail, availErr = r.models.GetAvailable(ctx, nil, nil)
	}()
	go func() {
		defer wg.Done()
		creds, credsErr = r.credentials.List(ctx, nil)
	}()
	for i, p := range provList {
		go func(idx int, prov *ai.Provider) {
			defer wg.Done()
			chk, err := r.models.CheckAuth(ctx, prov.Id, nil)
			checks[idx] = checkPair{id: prov.Id, check: chk, err: err}
		}(i, p)
	}
	wg.Wait()

	if availErr != nil {
		return availErr
	}
	for _, pair := range checks {
		if pair.err != nil {
			return pair.err
		}
	}
	if credsErr != nil {
		return credsErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if seq != r.availabilityRefreshSeq {
		return nil
	}

	authMap := make(map[string]*ai.AuthCheck, len(checks))
	configured := make(map[string]struct{})
	for _, pair := range checks {
		authMap[pair.id] = pair.check
		if pair.check != nil {
			configured[pair.id] = struct{}{}
		}
	}
	stored := make(map[string]struct{}, len(creds))
	for _, entry := range creds {
		stored[entry.ProviderId] = struct{}{}
	}
	r.snapshot = mruSnapshot{
		all:                 append([]*ai.Model(nil), r.models.GetModels(nil)...),
		available:           append([]*ai.Model(nil), avail...),
		configuredProviders: configured,
		storedProviders:     stored,
		auth:                authMap,
	}
	if errorSeq == r.availabilityErrorSeq {
		r.availabilityError = nil
	}
	return nil
}

func (r *ModelRuntime) queueAvailabilityRefresh(ctx context.Context) error {
	ctx = utils.OperationSignal(ctx)
	if r.availSem != nil {
		select {
		case r.availSem <- struct{}{}:
		case <-ctx.Done():
			return &utils.AbortError{Message: "The operation was aborted"}
		}
		defer func() { <-r.availSem }()
	}
	r.mu.Lock()
	r.availabilityRefreshSeq++
	seq := r.availabilityRefreshSeq
	for providerId, pseq := range r.providerAvailabilitySeq {
		r.providerAvailabilitySeq[providerId] = pseq + 1
	}
	r.availabilityErrorSeq++
	errorSeq := r.availabilityErrorSeq
	r.mu.Unlock()

	err := r.runAvailabilityRefresh(ctx, seq, errorSeq)
	if err != nil {
		r.mu.Lock()
		if errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
			msg := err.Error()
			r.availabilityError = &msg
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

func (r *ModelRuntime) refreshProviderAvailability(ctx context.Context, providerId string) error {
	ctx = utils.OperationSignal(ctx)
	r.mu.Lock()
	r.availabilityRefreshSeq++
	pseq := r.providerAvailabilitySeq[providerId] + 1
	r.providerAvailabilitySeq[providerId] = pseq
	r.availabilityErrorSeq++
	errorSeq := r.availabilityErrorSeq
	r.mu.Unlock()

	var wg sync.WaitGroup
	var provAvail []*ai.Model
	var availErr error
	var authChk *ai.AuthCheck
	var chkErr error
	var cred ai.Credential
	var credErr error

	wg.Add(3)
	go func() {
		defer wg.Done()
		provAvail, availErr = r.models.GetAvailable(ctx, &providerId, nil)
	}()
	go func() {
		defer wg.Done()
		authChk, chkErr = r.models.CheckAuth(ctx, providerId, nil)
	}()
	go func() {
		defer wg.Done()
		cred, credErr = r.credentials.Read(ctx, providerId, nil)
	}()
	wg.Wait()

	var err error
	if availErr != nil {
		err = availErr
	} else if chkErr != nil {
		err = chkErr
	} else if credErr != nil {
		err = credErr
	} else if ctx.Err() != nil {
		err = &utils.AbortError{Message: "The operation was aborted"}
	}

	if err != nil {
		r.mu.Lock()
		if r.providerAvailabilitySeq[providerId] == pseq && errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
			msg := err.Error()
			r.availabilityError = &msg
		}
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerAvailabilitySeq[providerId] != pseq {
		return nil
	}

	configured := make(map[string]struct{}, len(r.snapshot.configuredProviders)+1)
	for k := range r.snapshot.configuredProviders {
		configured[k] = struct{}{}
	}
	stored := make(map[string]struct{}, len(r.snapshot.storedProviders)+1)
	for k := range r.snapshot.storedProviders {
		stored[k] = struct{}{}
	}
	authByProvider := make(map[string]*ai.AuthCheck, len(r.snapshot.auth)+1)
	for k, v := range r.snapshot.auth {
		authByProvider[k] = v
	}

	if authChk != nil {
		configured[providerId] = struct{}{}
		authByProvider[providerId] = authChk
	} else {
		delete(configured, providerId)
		delete(authByProvider, providerId)
	}
	if cred != nil {
		stored[providerId] = struct{}{}
	} else {
		delete(stored, providerId)
	}

	all := r.models.GetModels(nil)
	availableById := make(map[string]*ai.Model, len(r.snapshot.available)+len(provAvail))
	for _, m := range r.snapshot.available {
		if m != nil && string(m.Provider) != providerId {
			availableById[string(m.Provider)+"\x00"+m.Id] = m
		}
	}
	for _, m := range provAvail {
		if m != nil {
			availableById[string(m.Provider)+"\x00"+m.Id] = m
		}
	}
	available := make([]*ai.Model, 0, len(all))
	for _, m := range all {
		if m == nil {
			continue
		}
		if hit, ok := availableById[string(m.Provider)+"\x00"+m.Id]; ok {
			available = append(available, hit)
		}
	}

	r.snapshot = mruSnapshot{
		all:                 append([]*ai.Model(nil), all...),
		available:           available,
		configuredProviders: configured,
		storedProviders:     stored,
		auth:                authByProvider,
	}
	if errorSeq == r.availabilityErrorSeq {
		r.availabilityError = nil
	}
	return nil
}

// GetProviders returns all composed providers in registration order.
func (r *ModelRuntime) GetProviders() []*ai.Provider {
	return r.models.GetProviders()
}

// GetProvider returns a composed provider by id, or nil if absent.
func (r *ModelRuntime) GetProvider(id string) *ai.Provider {
	return r.models.GetProvider(id)
}

// GetModels reads the last-known chat models (all providers when provider is nil).
func (r *ModelRuntime) GetModels(provider *string) []*ai.Model {
	return r.models.GetModels(provider)
}

// GetModel returns one chat model by provider and id, or nil if absent.
func (r *ModelRuntime) GetModel(provider, id string) *ai.Model {
	return r.models.GetModel(provider, id)
}

// GetModelsOfType reads last-known models of one type.
func (r *ModelRuntime) GetModelsOfType(typ ai.ModelType, provider *string) []ai.AnyModel {
	return r.models.GetModelsOfType(typ, provider)
}

// GetModelOfType returns one model of typ by provider and id, or nil if absent.
func (r *ModelRuntime) GetModelOfType(typ ai.ModelType, provider, id string) ai.AnyModel {
	return r.models.GetModelOfType(typ, provider, id)
}

// GetAllModels returns last-known models of all types.
func (r *ModelRuntime) GetAllModels(provider *string) []ai.AnyModel {
	return r.models.GetAllModels(provider)
}

// GetAvailableOfType returns models of typ whose provider has working credentials.
func (r *ModelRuntime) GetAvailableOfType(ctx context.Context, typ ai.ModelType, providerId *string, options *ai.AuthOperationOptions) ([]ai.AnyModel, error) {
	return r.models.GetAvailableOfType(ctx, typ, providerId, options)
}

// GetAllAvailable returns models of all types whose provider has working credentials.
func (r *ModelRuntime) GetAllAvailable(ctx context.Context, providerId *string, options *ai.AuthOperationOptions) ([]ai.AnyModel, error) {
	return r.models.GetAllAvailable(ctx, providerId, options)
}

// CheckAuth returns availability check metadata for providerId, or nil when unconfigured.
func (r *ModelRuntime) CheckAuth(ctx context.Context, providerId string, options *ai.AuthOperationOptions) (*ai.AuthCheck, error) {
	return r.models.CheckAuth(ctx, providerId, options)
}

// GetAvailable returns available chat models, updating the availability snapshot when providerId is nil.
func (r *ModelRuntime) GetAvailable(ctx context.Context, providerId *string, options *ai.AuthOperationOptions) ([]*ai.Model, error) {
	ctx = utils.OperationSignal(ctx)
	if providerId != nil && *providerId != "" {
		r.mu.Lock()
		r.availabilityErrorSeq++
		errorSeq := r.availabilityErrorSeq
		r.mu.Unlock()

		available, err := r.models.GetAvailable(ctx, providerId, options)
		if err != nil {
			r.mu.Lock()
			if errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
				msg := err.Error()
				r.availabilityError = &msg
			}
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Lock()
		if errorSeq == r.availabilityErrorSeq {
			r.availabilityError = nil
		}
		r.mu.Unlock()
		return available, nil
	}

	if err := r.queueAvailabilityRefresh(ctx); err != nil {
		return nil, err
	}
	return r.GetAvailableSnapshot(), nil
}

// GetAvailableSnapshot returns the cached slice of available chat models.
func (r *ModelRuntime) GetAvailableSnapshot() []*ai.Model {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*ai.Model(nil), r.snapshot.available...)
}

// GetError returns joined configuration, composition, and availability errors, if any.
func (r *ModelRuntime) GetError() *string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []string
	if configErr := r.config.GetError(); configErr != nil {
		errs = append(errs, *configErr)
	}
	for providerId, compErr := range r.compositionErrors.All() {
		errs = append(errs, fmt.Sprintf("Provider %q: %s", providerId, compErr))
	}
	if r.availabilityError != nil {
		errs = append(errs, "Availability refresh: "+*r.availabilityError)
	}
	if len(errs) == 0 {
		return nil
	}
	joined := strings.Join(errs, "\n\n")
	return &joined
}

// GetRegisteredProviderConfig returns a registered extension provider config, or nil.
func (r *ModelRuntime) GetRegisteredProviderConfig(providerId string) *ProviderConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, _ := r.extensionProviders.Get(providerId)
	return cfg
}

// GetRegisteredProviderIds returns all extension-registered provider ids.
func (r *ModelRuntime) GetRegisteredProviderIds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{})
	var ids []string
	for _, id := range r.extensionProviders.Keys() {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	for _, id := range r.nativeExtensionProviders.Keys() {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}

// GetRegisteredNativeProvider returns an extension-registered native Provider, or nil.
func (r *ModelRuntime) GetRegisteredNativeProvider(providerId string) *ai.Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, _ := r.nativeExtensionProviders.Get(providerId)
	return p
}

// GetCompatibilityRequestConfig returns compatibility headers and authHeader behavior for model.
func (r *ModelRuntime) GetCompatibilityRequestConfig(model *ai.Model) (CompatibilityRequestConfig, error) {
	if model == nil {
		return CompatibilityRequestConfig{}, nil
	}
	r.mu.Lock()
	cfgProv := r.config.GetProvider(string(model.Provider))
	extProv, _ := r.extensionProviders.Get(string(model.Provider))
	r.mu.Unlock()
	return ResolveCompatibilityRequestConfig(model, cfgProv, extProv)
}

// IsUsingOAuth reports whether providerId currently authenticates via OAuth.
func (r *ModelRuntime) IsUsingOAuth(providerId string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	chk := r.snapshot.auth[providerId]
	return chk != nil && chk.Type == ai.AuthTypeOAuth
}

// IsUsingSubscription reports whether providerId currently uses subscription-backed OAuth.
func (r *ModelRuntime) IsUsingSubscription(providerId string) bool {
	if !r.IsUsingOAuth(providerId) {
		return false
	}
	p := r.models.GetProvider(providerId)
	return p != nil && p.Auth.OAuth != nil && p.Auth.OAuth.IsSubscription
}

// HasConfiguredAuth reports whether providerId has configured auth in the latest snapshot.
func (r *ModelRuntime) HasConfiguredAuth(providerId string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.snapshot.configuredProviders[providerId]
	return ok
}

// GetAuth resolves auth for a provider id.
func (r *ModelRuntime) GetAuth(ctx context.Context, providerId string, overrides *ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	return r.models.GetAuth(ctx, providerId, overrides)
}

// GetAuthModel resolves auth for model and merges configured model-level headers.
func (r *ModelRuntime) GetAuthModel(ctx context.Context, model ai.AnyModel, overrides *ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	resolution, err := r.models.GetAuthModel(ctx, model, overrides)
	if err != nil || resolution == nil {
		return resolution, err
	}

	providerId := mruAnyModelProvider(model)
	r.mu.Lock()
	cfgProv := r.config.GetProvider(providerId)
	extProv, _ := r.extensionProviders.Get(providerId)
	r.mu.Unlock()

	var mergedEnv map[string]string
	if len(resolution.Env) > 0 || (overrides != nil && len(overrides.Env) > 0) {
		mergedEnv = make(map[string]string, len(resolution.Env)+8)
		for k, v := range resolution.Env {
			mergedEnv[k] = v
		}
		if overrides != nil {
			for k, v := range overrides.Env {
				mergedEnv[k] = v
			}
		}
	}

	configuredHeaders, err := ResolveConfiguredModelHeaders(model, cfgProv, extProv, mergedEnv)
	if err != nil {
		return nil, err
	}

	cp := *resolution
	cp.Auth.Headers = mruMergeHeaders(resolution.Auth.Headers, mruStringMapToProviderHeaders(configuredHeaders))
	return &cp, nil
}

func (r *ModelRuntime) getCredentialSemaphoreLocked(providerId string) chan struct{} {
	sem := r.credentialOps[providerId]
	if sem == nil {
		sem = make(chan struct{}, 1)
		r.credentialOps[providerId] = sem
	}
	return sem
}

func (r *ModelRuntime) enqueueCredentialOperation(ctx context.Context, providerId string, task func(ctx context.Context) error) error {
	ctx = utils.OperationSignal(ctx)
	if ctx.Err() != nil {
		return &utils.AbortError{Message: "The operation was aborted"}
	}

	r.mu.Lock()
	sem := r.getCredentialSemaphoreLocked(providerId)
	r.mu.Unlock()

	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return &utils.AbortError{Message: "The operation was aborted"}
	}
	defer func() { <-sem }()

	if ctx.Err() != nil {
		return &utils.AbortError{Message: "The operation was aborted"}
	}
	return task(ctx)
}

func (r *ModelRuntime) synchronizeCredentialState(
	ctx context.Context,
	providerId string,
	operation CredentialSynchronizationOperation,
	credential ai.Credential,
) error {
	fail := func(cause error) error {
		return &CredentialSynchronizationError{
			ProviderId: providerId,
			Operation:  operation,
			Credential: credential,
			Cause:      cause,
		}
	}

	if ctx.Err() != nil {
		return fail(&utils.AbortError{Message: "The operation was aborted"})
	}

	r.mu.Lock()
	r.recomposeProviderLocked(providerId)
	compErr, hasCompErr := r.compositionErrors.Get(providerId)
	r.mu.Unlock()
	if hasCompErr {
		return fail(errors.New(compErr))
	}

	allowNetwork := false
	res, err := r.models.Refresh(ctx, &ai.ModelsRefreshOptions{
		AllowNetwork: &allowNetwork,
		Providers:    []string{providerId},
	})
	if err != nil {
		return fail(err)
	}
	if res.Aborted || ctx.Err() != nil {
		return fail(&utils.AbortError{Message: "The operation was aborted"})
	}
	if res.Errors != nil {
		if refErr, ok := res.Errors.Get(providerId); ok && refErr != nil {
			return fail(refErr)
		}
	}

	r.mu.Lock()
	r.updateModelSnapshotLocked()
	r.mu.Unlock()

	if err := r.refreshProviderAvailability(ctx, providerId); err != nil {
		return fail(err)
	}
	return nil
}

// SetRuntimeApiKey sets an in-memory runtime API key override for providerId and synchronizes state.
func (r *ModelRuntime) SetRuntimeApiKey(ctx context.Context, providerId, apiKey string) error {
	return r.enqueueCredentialOperation(ctx, providerId, func(opCtx context.Context) error {
		r.credentials.SetRuntimeApiKey(providerId, apiKey)
		keyCopy := apiKey
		return r.synchronizeCredentialState(opCtx, providerId, CredentialSyncOpSetRuntimeApiKey, &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  &keyCopy,
		})
	})
}

// RemoveRuntimeApiKey removes an in-memory runtime API key override for providerId and synchronizes state.
func (r *ModelRuntime) RemoveRuntimeApiKey(ctx context.Context, providerId string) error {
	return r.enqueueCredentialOperation(ctx, providerId, func(opCtx context.Context) error {
		r.credentials.RemoveRuntimeApiKey(providerId)
		return r.synchronizeCredentialState(opCtx, providerId, CredentialSyncOpRemoveRuntimeKey, nil)
	})
}

// SettleCredentialWrites waits until any background credential writes finish.
func (r *ModelRuntime) SettleCredentialWrites(ctx ...context.Context) error {
	return r.credentials.Settled(ctx...)
}

// ListCredentials lists stored non-secret credential metadata.
func (r *ModelRuntime) ListCredentials(ctx context.Context, options *ai.AuthOperationOptions) ([]ai.CredentialInfo, error) {
	return r.credentials.List(ctx, options)
}

// GetProviderAuthStatus summarizes how providerId is configured in the runtime.
func (r *ModelRuntime) GetProviderAuthStatus(providerId string) AuthStatus {
	if r.credentials.HasRuntimeApiKey(providerId) {
		src := "runtime"
		return AuthStatus{Configured: true, Source: &src}
	}
	r.mu.Lock()
	_, isStored := r.snapshot.storedProviders[providerId]
	cfgProv := r.config.GetProvider(providerId)
	extProv, _ := r.extensionProviders.Get(providerId)
	authChk := r.snapshot.auth[providerId]
	r.mu.Unlock()

	if isStored {
		src := "stored"
		return AuthStatus{Configured: true, Source: &src}
	}
	if configured := ConfiguredRequestAuthStatus(cfgProv, extProv); configured != nil {
		return *configured
	}
	if authChk != nil {
		src := "environment"
		return AuthStatus{Configured: true, Source: &src, Label: authChk.Source}
	}
	return AuthStatus{Configured: false}
}

type mruPreparedRequest struct {
	provider *ai.Provider
	model    ai.AnyModel
	apiKey   *string
	headers  ai.ProviderHeaders
	env      ai.ProviderEnv
}

func (r *ModelRuntime) prepareRequest(
	ctx context.Context,
	model ai.AnyModel,
	apiKey *string,
	env ai.ProviderEnv,
	reqHeaders ai.ProviderHeaders,
	sessionId *string,
	transformHeaders func(headers ai.ProviderHeaders) (ai.ProviderHeaders, error),
) (*mruPreparedRequest, error) {
	providerId := mruAnyModelProvider(model)
	provider := r.models.GetProvider(providerId)
	if provider == nil {
		return nil, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Unknown provider: %s", providerId))
	}
	resolution, err := r.GetAuthModel(ctx, model, &ai.AuthResolutionOverrides{
		ApiKey: apiKey,
		Env:    env,
	})
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, ai.NewModelsError(ai.ModelsErrorCodeAuth, fmt.Sprintf("Provider is not configured: %s", providerId))
	}

	reqModel := mruAnyModelWithBaseUrl(model, resolution.Auth.BaseUrl)
	target := mruAnyModelTarget(reqModel)
	sessionHeaders := mruStringMapToProviderHeaders(ai.GetOpenCodeSessionHeaders(target, sessionId))

	headers := mruMergeHeaders(mruMergeHeaders(sessionHeaders, resolution.Auth.Headers), reqHeaders)
	if transformHeaders != nil {
		if headers == nil {
			headers = omap.NewMap[string, *string]()
		}
		headers, err = transformHeaders(headers)
		if err != nil {
			return nil, err
		}
	}

	var mergedEnv ai.ProviderEnv
	if len(resolution.Env) > 0 || len(env) > 0 {
		mergedEnv = make(ai.ProviderEnv, len(resolution.Env)+len(env))
		for k, v := range resolution.Env {
			mergedEnv[k] = v
		}
		for k, v := range env {
			mergedEnv[k] = v
		}
	}

	effectiveKey := resolution.Auth.ApiKey
	if apiKey != nil {
		effectiveKey = apiKey
	}

	return &mruPreparedRequest{
		provider: provider,
		model:    reqModel,
		apiKey:   effectiveKey,
		headers:  headers,
		env:      mergedEnv,
	}, nil
}

// Stream streams through the configured provider with request-time authentication.
func (r *ModelRuntime) Stream(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsApiStreamOptions) *ai.AssistantMessageEventStream {
	transcript := ai.NormalizeContext(c)
	sig := utils.OperationSignal(ctx)
	return ai.LazyStream(
		model,
		func() (*ai.AssistantMessageEventStream, error) {
			chatModel, err := ai.AssertChatModel(model)
			if err != nil {
				return nil, err
			}
			var providerOpts ai.ProviderStreamOptions
			var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
			var reqApiKey *string
			var reqEnv ai.ProviderEnv
			var reqHeaders ai.ProviderHeaders
			var sessionId *string
			var identity *ai.ClientIdentity
			if options != nil {
				providerOpts = options.ProviderStreamOptions
				transformHeaders = options.TransformHeaders
				if providerOpts != nil {
					if base := providerOpts.BaseStreamOptions(); base != nil {
						reqApiKey = base.ApiKey
						reqEnv = base.Env
						reqHeaders = base.Headers
						sessionId = base.SessionId
						identity = base.Identity
					}
				}
			}
			prepared, err := r.prepareRequest(sig, chatModel, reqApiKey, reqEnv, reqHeaders, sessionId, transformHeaders)
			if err != nil {
				return nil, err
			}
			if prepared.provider.Stream == nil {
				return nil, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support streaming", chatModel.Provider))
			}
			appliedOpts := mruCloneProviderStreamOptions[ai.Model](providerOpts, prepared.apiKey, prepared.headers, prepared.env)
			if base := appliedOpts.BaseStreamOptions(); base != nil && identity != nil {
				base.Identity = identity
			}
			return prepared.provider.Stream(sig, prepared.model.(*ai.Model), transcript, appliedOpts), nil
		},
		nil,
		sig,
	)
}

// Complete completes a streaming request through the configured provider.
func (r *ModelRuntime) Complete(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsApiStreamOptions) (*ai.AssistantMessage, error) {
	return r.Stream(ctx, model, c, options).Result(utils.OperationSignal(ctx))
}

// StreamSimple streams with provider-neutral options, virtual model routing, and request-time auth.
func (r *ModelRuntime) StreamSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	transcript := ai.NormalizeContext(c)
	sig := utils.OperationSignal(ctx)
	if IsVirtualModel(model) {
		return ai.LazyStream(
			model,
			func() (*ai.AssistantMessageEventStream, error) {
				thinkingLevel := ai.ModelThinkingLevelOff
				if options != nil && options.Reasoning != nil {
					thinkingLevel = ai.ModelThinkingLevel(*options.Reasoning)
				}
				route, err := r.ResolveModel(sig, model, transcript.Messages, ResolveModelOptions{
					Reason:        ModelRouteReasonDirect,
					ThinkingLevel: thinkingLevel,
				})
				if err != nil {
					return nil, err
				}
				forwarded := &ai.ModelsSimpleStreamOptions{}
				if options != nil {
					*forwarded = *options
				}
				if forwarded.MaxTokens != nil && route.Model != nil && route.Model.MaxTokens > 0 && *forwarded.MaxTokens > route.Model.MaxTokens {
					capped := route.Model.MaxTokens
					forwarded.MaxTokens = &capped
				}
				if route.ThinkingLevel == ai.ModelThinkingLevelOff {
					forwarded.Reasoning = nil
				} else {
					rLvl := ai.ThinkingLevel(route.ThinkingLevel)
					forwarded.Reasoning = &rLvl
				}
				if route.Model != nil && route.Model.Provider != model.Provider {
					forwarded.ApiKey = nil
					forwarded.Headers = nil
					forwarded.Env = nil
				}
				return r.StreamSimple(sig, route.Model, c, forwarded), nil
			},
			nil,
			sig,
		)
	}

	return ai.LazyStream(
		model,
		func() (*ai.AssistantMessageEventStream, error) {
			chatModel, err := ai.AssertChatModel(model)
			if err != nil {
				return nil, err
			}
			var base ai.SimpleStreamOptions
			var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
			if options != nil {
				base = options.SimpleStreamOptions
				transformHeaders = options.TransformHeaders
			}
			prepared, err := r.prepareRequest(sig, chatModel, base.ApiKey, base.Env, base.Headers, base.SessionId, transformHeaders)
			if err != nil {
				return nil, err
			}
			if prepared.provider.StreamSimple == nil {
				return nil, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support simple streaming", chatModel.Provider))
			}
			reqOpts := base
			reqOpts.ApiKey = prepared.apiKey
			reqOpts.Headers = prepared.headers
			reqOpts.Env = prepared.env
			return prepared.provider.StreamSimple(sig, prepared.model.(*ai.Model), transcript, &reqOpts), nil
		},
		nil,
		sig,
	)
}

// CompleteSimple completes a StreamSimple request.
func (r *ModelRuntime) CompleteSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) (*ai.AssistantMessage, error) {
	return r.StreamSimple(ctx, model, c, options).Result(utils.OperationSignal(ctx))
}

// StreamDeferred polls a deferred response handle through the configured provider.
func (r *ModelRuntime) StreamDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredFetchOptions) *ai.AssistantMessageEventStream {
	sig := utils.OperationSignal(ctx)
	return ai.LazyStream(
		model,
		func() (*ai.AssistantMessageEventStream, error) {
			chatModel, err := ai.AssertChatModel(model)
			if err != nil {
				return nil, err
			}
			var base ai.DeferredFetchOptions
			var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
			if options != nil {
				base = options.DeferredFetchOptions
				transformHeaders = options.TransformHeaders
			}
			prepared, err := r.prepareRequest(sig, chatModel, base.ApiKey, base.Env, base.Headers, nil, transformHeaders)
			if err != nil {
				return nil, err
			}
			if prepared.provider.FetchDeferred == nil {
				return nil, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support deferred responses", chatModel.Provider))
			}
			reqOpts := base
			reqOpts.ApiKey = prepared.apiKey
			reqOpts.Headers = prepared.headers
			reqOpts.Env = prepared.env
			return prepared.provider.FetchDeferred(sig, prepared.model.(*ai.Model), handle, &reqOpts), nil
		},
		nil,
		sig,
	)
}

// FetchDeferred fetches the result of a deferred response handle.
func (r *ModelRuntime) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredFetchOptions) (*ai.AssistantMessage, error) {
	return r.StreamDeferred(ctx, model, handle, options).Result(utils.OperationSignal(ctx))
}

// CancelDeferred cancels a deferred response handle.
func (r *ModelRuntime) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredCancelOptions) error {
	sig := utils.OperationSignal(ctx)
	chatModel, err := ai.AssertChatModel(model)
	if err != nil {
		return err
	}
	var base ai.DeferredCancelOptions
	var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
	if options != nil {
		base = options.DeferredCancelOptions
		transformHeaders = options.TransformHeaders
	}
	prepared, err := r.prepareRequest(sig, chatModel, base.ApiKey, base.Env, base.Headers, nil, transformHeaders)
	if err != nil {
		return err
	}
	if prepared.provider.CancelDeferred == nil {
		return ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support deferred responses", chatModel.Provider))
	}
	reqOpts := base
	reqOpts.ApiKey = prepared.apiKey
	reqOpts.Headers = prepared.headers
	reqOpts.Env = prepared.env
	return prepared.provider.CancelDeferred(sig, prepared.model.(*ai.Model), handle, &reqOpts)
}

// GenerateImages generates images with request-time authentication and never rejects on provider/auth errors.
func (r *ModelRuntime) GenerateImages(ctx context.Context, model *ai.ImageModel, c *ai.ImagesContext, options *ai.ModelsImagesOptions) (*ai.AssistantImages, error) {
	sig := utils.OperationSignal(ctx)
	errModel := model
	if errModel == nil {
		errModel = &ai.ImageModel{}
	}
	if _, err := ai.AssertImageModel(model); err != nil {
		return ai.ImageErrorResult(errModel, err, sig.Err() != nil), nil
	}
	var base ai.ImagesOptions
	var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
	if options != nil {
		base = options.ImagesOptions
		transformHeaders = options.TransformHeaders
	}
	prepared, err := r.prepareRequest(sig, model, base.ApiKey, base.Env, base.Headers, nil, transformHeaders)
	if err != nil {
		return ai.ImageErrorResult(errModel, err, sig.Err() != nil), nil
	}
	if prepared.provider.GenerateImages == nil {
		return ai.ImageErrorResult(errModel, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support image generation", model.Provider)), sig.Err() != nil), nil
	}
	reqOpts := base
	reqOpts.ApiKey = prepared.apiKey
	reqOpts.Headers = prepared.headers
	reqOpts.Env = prepared.env
	res, err := prepared.provider.GenerateImages(sig, prepared.model.(*ai.ImageModel), c, &reqOpts)
	if err != nil {
		return ai.ImageErrorResult(errModel, err, sig.Err() != nil), nil
	}
	return res, nil
}

// Classify classifies structured state with request-time authentication and never rejects on provider/auth errors.
func (r *ModelRuntime) Classify(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ModelsClassifierOptions) (*ai.ClassifierResult, error) {
	sig := utils.OperationSignal(ctx)
	errModel := model
	if errModel == nil {
		errModel = &ai.ClassifierModel{}
	}
	if _, err := ai.AssertClassifierModel(model); err != nil {
		return ai.ClassifierErrorResult(errModel, err, sig.Err() != nil), nil
	}
	if err := ai.AssertClassifierInputSupported(model, c); err != nil {
		return ai.ClassifierErrorResult(errModel, err, sig.Err() != nil), nil
	}
	var base ai.ClassifierOptions
	var transformHeaders func(ai.ProviderHeaders) (ai.ProviderHeaders, error)
	if options != nil {
		base = options.ClassifierOptions
		transformHeaders = options.TransformHeaders
	}
	prepared, err := r.prepareRequest(sig, model, base.ApiKey, base.Env, base.Headers, nil, transformHeaders)
	if err != nil {
		return ai.ClassifierErrorResult(errModel, err, sig.Err() != nil), nil
	}
	if prepared.provider.Classify == nil {
		return ai.ClassifierErrorResult(errModel, ai.NewModelsError(ai.ModelsErrorCodeProvider, fmt.Sprintf("Provider %s does not support classification", model.Provider)), sig.Err() != nil), nil
	}
	reqOpts := base
	reqOpts.ApiKey = prepared.apiKey
	reqOpts.Headers = prepared.headers
	reqOpts.Env = prepared.env
	res, err := prepared.provider.Classify(sig, prepared.model.(*ai.ClassifierModel), c, &reqOpts)
	if err != nil {
		return ai.ClassifierErrorResult(errModel, err, sig.Err() != nil), nil
	}
	return res, nil
}

// Login authenticates with providerId using authType and synchronizes runtime snapshots.
func (r *ModelRuntime) Login(
	ctx context.Context,
	providerId string,
	authType ai.AuthType,
	interaction ai.AuthInteraction,
	options *ai.LoginOptions,
) (ai.Credential, error) {
	var out ai.Credential
	err := r.enqueueCredentialOperation(ctx, providerId, func(opCtx context.Context) error {
		cred, loginErr := r.models.Login(opCtx, providerId, authType, interaction, options)
		if loginErr != nil {
			return loginErr
		}
		out = cred
		return r.synchronizeCredentialState(opCtx, providerId, CredentialSyncOpLogin, cred)
	})
	return out, err
}

// Logout removes stored credentials for providerId and synchronizes runtime snapshots.
func (r *ModelRuntime) Logout(ctx context.Context, providerId string, options *ai.AuthOperationOptions) error {
	return r.enqueueCredentialOperation(ctx, providerId, func(opCtx context.Context) error {
		if err := r.models.Logout(opCtx, providerId, options); err != nil {
			return err
		}
		return r.synchronizeCredentialState(opCtx, providerId, CredentialSyncOpLogout, nil)
	})
}

// Refresh reloads models.json, recomposes providers, refreshes dynamic catalogs, and refreshes availability.
func (r *ModelRuntime) Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
	sig := utils.OperationSignal(ctx)
	r.asyncRefreshMu.Lock()
	r.asyncRefreshGen++
	r.asyncRefreshRunning = false
	r.asyncRefreshQueued = false
	oldCancel := r.asyncRefreshCancel
	oldDone := r.asyncRefreshDone
	r.asyncRefreshCancel = nil
	r.asyncRefreshDone = nil
	r.asyncRefreshMu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldDone != nil {
		select {
		case <-oldDone:
		case <-sig.Done():
			return ai.ModelsRefreshResult{Aborted: true, Errors: omap.NewMap[string, error]()}, nil
		}
	}
	return r.refreshInternal(sig, options, false, 0)
}

func (r *ModelRuntime) isCurrentAsyncRefresh(ctx context.Context, asyncGen uint64) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	r.asyncRefreshMu.Lock()
	current := r.asyncRefreshGen == asyncGen
	r.asyncRefreshMu.Unlock()
	return current
}

func (r *ModelRuntime) refreshInternal(sig context.Context, options *ai.ModelsRefreshOptions, isAsync bool, asyncGen uint64) (ai.ModelsRefreshResult, error) {
	if isAsync && !r.isCurrentAsyncRefresh(sig, asyncGen) {
		return ai.ModelsRefreshResult{Aborted: true, Errors: omap.NewMap[string, error]()}, nil
	}
	cfg := LoadModelConfig(r.modelsPath)

	r.mu.Lock()
	r.config = cfg
	r.configureRadiusProvidersLocked()
	var targetProviders []string
	hasProviderFilter := options != nil && options.Providers != nil
	if hasProviderFilter {
		seen := make(map[string]struct{}, len(options.Providers))
		for _, id := range options.Providers {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			targetProviders = append(targetProviders, id)
			r.recomposeProviderLocked(id)
		}
		r.updateModelSnapshotLocked()
	} else {
		r.rebuildProvidersLocked()
	}
	allowNetwork := r.modelNetworkEnabled
	var force *bool
	if options != nil {
		if options.AllowNetwork != nil {
			allowNetwork = *options.AllowNetwork
		}
		force = options.Force
	}
	r.mu.Unlock()

	if isAsync && !r.isCurrentAsyncRefresh(sig, asyncGen) {
		return ai.ModelsRefreshResult{Aborted: true, Errors: omap.NewMap[string, error]()}, nil
	}

	refreshOpts := &ai.ModelsRefreshOptions{
		AllowNetwork: &allowNetwork,
		Force:        force,
	}
	if hasProviderFilter {
		refreshOpts.Providers = targetProviders
	}
	res, err := r.models.Refresh(sig, refreshOpts)
	if err != nil {
		return ai.ModelsRefreshResult{}, err
	}

	errs := omap.NewMap[string, error]()
	if res.Errors != nil {
		for k, v := range res.Errors.All() {
			errs.Set(k, v)
		}
	}

	r.mu.Lock()
	r.updateModelSnapshotLocked()
	r.mu.Unlock()

	if hasProviderFilter {
		var wg sync.WaitGroup
		var errsMu sync.Mutex
		for _, id := range targetProviders {
			wg.Add(1)
			go func(providerId string) {
				defer wg.Done()
				if pErr := r.refreshProviderAvailability(sig, providerId); pErr != nil && sig.Err() == nil {
					errsMu.Lock()
					errs.Set(providerId, pErr)
					errsMu.Unlock()
				}
			}(id)
		}
		wg.Wait()
	} else {
		_ = r.queueAvailabilityRefresh(sig)
	}

	return ai.ModelsRefreshResult{
		Aborted: res.Aborted || sig.Err() != nil,
		Errors:  errs,
	}, nil
}

func (r *ModelRuntime) markProvisionallyConfiguredLocked(providerId string, configuredStatus *AuthStatus, typ ai.AuthType) {
	_, isStored := r.snapshot.storedProviders[providerId]
	if !isStored && (configuredStatus == nil || !configuredStatus.Configured) {
		return
	}
	configured := make(map[string]struct{}, len(r.snapshot.configuredProviders)+1)
	for k := range r.snapshot.configuredProviders {
		configured[k] = struct{}{}
	}
	configured[providerId] = struct{}{}

	auth := make(map[string]*ai.AuthCheck, len(r.snapshot.auth)+1)
	for k, v := range r.snapshot.auth {
		auth[k] = v
	}
	if auth[providerId] == nil {
		src := "configured provider"
		auth[providerId] = &ai.AuthCheck{Type: typ, Source: &src}
	}

	available := make([]*ai.Model, 0, len(r.snapshot.all))
	for _, m := range r.snapshot.all {
		if m == nil {
			continue
		}
		if _, ok := configured[string(m.Provider)]; ok {
			available = append(available, m)
		}
	}
	r.snapshot.auth = auth
	r.snapshot.configuredProviders = configured
	r.snapshot.available = available
}

// Close cancels any background refresh task started by provider registration and waits for it to exit.
func (r *ModelRuntime) Close() {
	r.asyncRefreshMu.Lock()
	r.asyncRefreshClosed = true
	r.asyncRefreshGen++
	r.asyncRefreshRunning = false
	r.asyncRefreshQueued = false
	oldCancel := r.asyncRefreshCancel
	oldDone := r.asyncRefreshDone
	r.asyncRefreshCancel = nil
	r.asyncRefreshDone = nil
	r.asyncRefreshMu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldDone != nil {
		<-oldDone
	}
}

// triggerAsyncNoNetRefresh schedules a non-network refresh after provider/virtual-model mutations.
func (r *ModelRuntime) triggerAsyncNoNetRefresh() {
	r.asyncRefreshMu.Lock()
	if r.asyncRefreshClosed {
		r.asyncRefreshMu.Unlock()
		return
	}
	if r.asyncRefreshRunning {
		r.asyncRefreshQueued = true
		r.asyncRefreshMu.Unlock()
		return
	}
	r.asyncRefreshGen++
	gen := r.asyncRefreshGen
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.asyncRefreshRunning = true
	r.asyncRefreshQueued = false
	r.asyncRefreshCancel = cancel
	r.asyncRefreshDone = done
	r.asyncRefreshMu.Unlock()

	go func() {
		defer close(done)
		defer cancel()
		for {
			allowNetwork := false
			_, _ = r.refreshInternal(ctx, &ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork}, true, gen)
			r.asyncRefreshMu.Lock()
			if r.asyncRefreshGen != gen {
				r.asyncRefreshMu.Unlock()
				return
			}
			if !r.asyncRefreshQueued || ctx.Err() != nil {
				r.asyncRefreshRunning = false
				r.asyncRefreshQueued = false
				r.asyncRefreshCancel = nil
				r.asyncRefreshDone = nil
				r.asyncRefreshMu.Unlock()
				return
			}
			r.asyncRefreshQueued = false
			r.asyncRefreshMu.Unlock()
		}
	}()
}

// RegisterNativeProvider registers or replaces a native Provider from an extension.
func (r *ModelRuntime) RegisterNativeProvider(provider *ai.Provider) error {
	if provider == nil || strings.TrimSpace(provider.Id) == "" {
		return errors.New("Provider id must not be empty.")
	}
	r.mu.Lock()
	r.extensionProviders.Delete(provider.Id)
	r.nativeExtensionProviders.Set(provider.Id, provider)
	r.recomposeProviderLocked(provider.Id)
	r.invalidateAvailabilityLocked()
	r.updateModelSnapshotLocked()
	typ := ai.AuthTypeApiKey
	if provider.Auth.OAuth != nil && provider.Auth.ApiKey == nil {
		typ = ai.AuthTypeOAuth
	}
	r.markProvisionallyConfiguredLocked(provider.Id, ConfiguredRequestAuthStatus(r.config.GetProvider(provider.Id), nil), typ)
	r.mu.Unlock()

	r.triggerAsyncNoNetRefresh()
	return nil
}

// RegisterProvider validates and merges an extension ProviderConfig registration.
func (r *ModelRuntime) RegisterProvider(providerId string, config ProviderConfigInput) error {
	r.mu.Lock()
	base, _ := r.builtins.Get(providerId)
	cfgProv := r.config.GetProvider(providerId)
	if err := ValidateExtensionProvider(providerId, base, cfgProv, &config); err != nil {
		r.mu.Unlock()
		return err
	}
	r.nativeExtensionProviders.Delete(providerId)
	previous, _ := r.extensionProviders.Get(providerId)
	effective := mruMergeExtensionProviderConfig(previous, &config)
	r.extensionProviders.Set(providerId, effective)
	r.recomposeProviderLocked(providerId)
	r.invalidateAvailabilityLocked()
	r.updateModelSnapshotLocked()
	typ := ai.AuthTypeApiKey
	if effective.OAuth != nil && effective.ApiKey == nil {
		typ = ai.AuthTypeOAuth
	}
	r.markProvisionallyConfiguredLocked(providerId, ConfiguredRequestAuthStatus(cfgProv, effective), typ)
	r.mu.Unlock()

	r.triggerAsyncNoNetRefresh()
	return nil
}

func mruMergeExtensionProviderConfig(previous, next *ProviderConfig) *ProviderConfig {
	merged := &ProviderConfig{}
	if previous != nil {
		*merged = *previous
	}
	if next == nil {
		return merged
	}
	if next.Name != nil {
		merged.Name = next.Name
	}
	if next.BaseUrl != nil {
		merged.BaseUrl = next.BaseUrl
	}
	if next.ApiKey != nil {
		merged.ApiKey = next.ApiKey
	}
	if next.Api != nil {
		merged.Api = next.Api
	}
	if next.Headers != nil {
		merged.Headers = next.Headers
	}
	if next.AuthHeader != nil {
		merged.AuthHeader = next.AuthHeader
	}
	if next.OAuth != nil {
		merged.OAuth = next.OAuth
	}
	if next.StreamSimple != nil {
		merged.StreamSimple = next.StreamSimple
	}
	if next.Images != nil {
		merged.Images = next.Images
	}
	if next.Classifiers != nil {
		merged.Classifiers = next.Classifiers
	}
	if next.Models != nil {
		merged.Models = next.Models
	}
	if next.RefreshModels != nil {
		merged.RefreshModels = next.RefreshModels
	}
	return merged
}

// UnregisterProvider removes extension provider registrations for providerId.
func (r *ModelRuntime) UnregisterProvider(providerId string) {
	r.mu.Lock()
	r.extensionProviders.Delete(providerId)
	r.nativeExtensionProviders.Delete(providerId)
	r.recomposeProviderLocked(providerId)
	r.invalidateAvailabilityLocked()
	r.updateModelSnapshotLocked()
	r.mu.Unlock()

	r.triggerAsyncNoNetRefresh()
}

// RegisterVirtualModel registers a virtual model under definition.Provider.
func (r *ModelRuntime) RegisterVirtualModel(definition VirtualModelDefinition) error {
	if strings.TrimSpace(definition.Provider) == "" || strings.TrimSpace(definition.Id) == "" {
		return errors.New("Virtual model provider and id must not be empty.")
	}
	existing := r.models.GetModel(definition.Provider, definition.Id)
	if existing != nil && !IsVirtualModel(existing) {
		return fmt.Errorf("Virtual model %s/%s conflicts with a physical model.", definition.Provider, definition.Id)
	}

	r.mu.Lock()
	models := r.virtualModels[definition.Provider]
	if models == nil {
		models = omap.NewMap[string, mruRegisteredVirtualModel]()
		r.virtualModels[definition.Provider] = models
	}
	models.Set(definition.Id, mruRegisteredVirtualModel{
		model: CreateVirtualModel(definition),
		route: definition.Route,
	})
	composed := r.recomposeProviderLocked(definition.Provider)
	if composed == nil {
		if _, ok := r.snapshot.configuredProviders[definition.Provider]; !ok {
			auth := make(map[string]*ai.AuthCheck, len(r.snapshot.auth)+1)
			for k, v := range r.snapshot.auth {
				auth[k] = v
			}
			src := "virtual"
			auth[definition.Provider] = &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &src}
			configured := make(map[string]struct{}, len(r.snapshot.configuredProviders)+1)
			for k := range r.snapshot.configuredProviders {
				configured[k] = struct{}{}
			}
			configured[definition.Provider] = struct{}{}
			r.snapshot.auth = auth
			r.snapshot.configuredProviders = configured
		}
	}
	r.invalidateAvailabilityLocked()
	r.updateModelSnapshotLocked()
	r.mu.Unlock()

	r.triggerAsyncNoNetRefresh()
	return nil
}

// UnregisterVirtualModel removes a previously registered virtual model.
func (r *ModelRuntime) UnregisterVirtualModel(providerId, id string) {
	r.mu.Lock()
	models := r.virtualModels[providerId]
	if models == nil {
		r.mu.Unlock()
		return
	}
	if _, ok := models.Get(id); !ok {
		r.mu.Unlock()
		return
	}
	models.Delete(id)
	if models.Len() == 0 {
		delete(r.virtualModels, providerId)
	}
	r.recomposeProviderLocked(providerId)
	r.invalidateAvailabilityLocked()
	r.updateModelSnapshotLocked()
	r.mu.Unlock()

	r.triggerAsyncNoNetRefresh()
}

// ResolveModel asks a virtual model's router for the physical model and clamped thinking level for one request.
func (r *ModelRuntime) ResolveModel(ctx context.Context, model *ai.Model, messages []ai.Message, options ResolveModelOptions) (ModelRoute, error) {
	if model == nil {
		return ModelRoute{}, errors.New("Virtual model is nil.")
	}
	name := fmt.Sprintf("Virtual model %s/%s", model.Provider, model.Id)

	r.mu.Lock()
	var entry mruRegisteredVirtualModel
	var found bool
	if vmMap := r.virtualModels[string(model.Provider)]; vmMap != nil {
		entry, found = vmMap.Get(model.Id)
	}
	r.mu.Unlock()

	if !found || entry.route == nil {
		return ModelRoute{}, fmt.Errorf("%s is not registered.", name)
	}

	// Convert ai.Message slice to agent.AgentMessage slice without changing slice semantics.
	agentMsgs := make([]agent.AgentMessage, len(messages))
	for i, m := range messages {
		agentMsgs[i] = m
	}
	latest := FindLatestResponse(agentMsgs)
	var previous *ModelRoutePrevious
	if latest != nil {
		if prevModel := r.GetPhysicalModel(string(latest.Provider), latest.Model); prevModel != nil {
			previous = &ModelRoutePrevious{
				Model:         prevModel,
				ThinkingLevel: latest.ThinkingLevel,
			}
		}
	}
	var failed *ModelRouteFailed
	if options.Failed != nil {
		if failedModel := r.GetPhysicalModel(string(options.Failed.Provider), options.Failed.Model); failedModel != nil {
			failed = &ModelRouteFailed{
				Model:         failedModel,
				ThinkingLevel: options.Failed.ThinkingLevel,
				Message:       options.Failed,
			}
		}
	}

	route, err := entry.route(utils.OperationSignal(ctx), ModelRouteRequest{
		Model:         model,
		ThinkingLevel: options.ThinkingLevel,
		Reason:        options.Reason,
		Previous:      previous,
		Failed:        failed,
		State:         options.State,
		Messages:      messages,
	})
	if err != nil {
		return ModelRoute{}, err
	}
	if route.Model == nil {
		return ModelRoute{}, fmt.Errorf("%s routed to nil model.", name)
	}
	target := r.GetPhysicalModel(string(route.Model.Provider), route.Model.Id)
	routed := fmt.Sprintf("%s routed to %s/%s", name, route.Model.Provider, route.Model.Id)
	if target == nil {
		return ModelRoute{}, fmt.Errorf("%s, which is not a physical model.", routed)
	}
	if !r.HasConfiguredAuth(string(target.Provider)) {
		return ModelRoute{}, fmt.Errorf("%s, which has no credentials.", routed)
	}
	return ModelRoute{
		Model:         target,
		ThinkingLevel: ai.ClampThinkingLevel(target, route.ThinkingLevel),
		State:         route.State,
	}, nil
}

// GetPhysicalModel returns a catalog chat model that is not virtual.
func (r *ModelRuntime) GetPhysicalModel(providerId, modelId string) *ai.Model {
	model := r.models.GetModel(providerId, modelId)
	if model != nil && !IsVirtualModel(model) {
		return model
	}
	return nil
}
