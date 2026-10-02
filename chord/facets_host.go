// Ported from packages/chord/src/facets/host.ts (pi v1.0.0).

package chord

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

type fhLifecycleState string

const (
	fhLifecycleStateSettingUp fhLifecycleState = "setting_up"
	fhLifecycleStatePrepared  fhLifecycleState = "prepared"
	fhLifecycleStateActive    fhLifecycleState = "active"
	fhLifecycleStateDisposing fhLifecycleState = "disposing"
	fhLifecycleStateDead      fhLifecycleState = "dead"
)

type fhGenerationPhase string

const (
	fhGenerationPhaseSetup      fhGenerationPhase = "setup"
	fhGenerationPhaseAssembling fhGenerationPhase = "assembling"
	fhGenerationPhaseConnecting fhGenerationPhase = "connecting"
	fhGenerationPhaseActivating fhGenerationPhase = "activating"
	fhGenerationPhaseActive     fhGenerationPhase = "active"
	fhGenerationPhaseReloading  fhGenerationPhase = "reloading"
	fhGenerationPhaseDisposing  fhGenerationPhase = "disposing"
	fhGenerationPhaseDead       fhGenerationPhase = "dead"
)

// fhDisposal is a resource cleanup that may be asynchronous.
type fhDisposal func() error

type fhFacetServiceReference struct {
	serviceId string
	service   any
	mode      ServiceMode
}

type fhExternalService struct {
	service any
	mode    ServiceMode
	source  RemoteServiceSource
}

type fhFacetRuntime struct {
	facetId        string
	requires       []fhFacetServiceReference
	provides       []fhFacetServiceReference
	lifecycle      *fhFacetLifecycle
	provisions     []fhFacetProvision
	singletonViews map[string]any
}

type fhFacetLifecycle struct {
	mu            sync.Mutex
	id            string
	effects       []fhDisposal
	observations  []func() fhDisposal
	activate      []func() error
	state         fhLifecycleState // initial TS value is fhLifecycleStateSettingUp
	serviceAccess bool
}

func fhNewFacetLifecycle(id string) *fhFacetLifecycle {
	panic("unported: fhNewFacetLifecycle")
}

func (l *fhFacetLifecycle) AssertSettingUp(operation string) error {
	panic("unported: fhFacetLifecycle.AssertSettingUp")
}

func (l *fhFacetLifecycle) AssertRunning(operation string) error {
	panic("unported: fhFacetLifecycle.AssertRunning")
}

func (l *fhFacetLifecycle) AssertActive(operation string) error {
	panic("unported: fhFacetLifecycle.AssertActive")
}

func (l *fhFacetLifecycle) AssertServiceAccess() error {
	panic("unported: fhFacetLifecycle.AssertServiceAccess")
}

func (l *fhFacetLifecycle) Revoke() {
	panic("unported: fhFacetLifecycle.Revoke")
}

func (l *fhFacetLifecycle) Own(disposal fhDisposal) error {
	panic("unported: fhFacetLifecycle.Own")
}

func (l *fhFacetLifecycle) Observe(start func() fhDisposal) error {
	panic("unported: fhFacetLifecycle.Observe")
}

func (l *fhFacetLifecycle) OnActivate(callback func() error) error {
	panic("unported: fhFacetLifecycle.OnActivate")
}

func (l *fhFacetLifecycle) Prepared() error {
	panic("unported: fhFacetLifecycle.Prepared")
}

func (l *fhFacetLifecycle) Activate() error {
	panic("unported: fhFacetLifecycle.Activate")
}

func (l *fhFacetLifecycle) Dispose() error {
	panic("unported: fhFacetLifecycle.Dispose")
}

type fhKeyedServiceSource interface {
	Observe(service any, handler func(service any, call Context) error) func()
}

type fhLocalKeyedRegistration struct {
	generations map[string]int
	directory   *InstanceDirectory[InstanceDirectoryEntry]
}

type fhLocalKeyedServiceRegistry struct {
	mu            sync.Mutex
	registrations *omap.Map[string, *fhLocalKeyedRegistration]
	disposed      bool
}

func fhNewLocalKeyedServiceRegistry(services []ServiceRef, onError func(error)) (*fhLocalKeyedServiceRegistry, error) {
	panic("unported: fhNewLocalKeyedServiceRegistry")
}

func (r *fhLocalKeyedServiceRegistry) Spawn(service any, key string, implementation any) (func(), error) {
	panic("unported: fhLocalKeyedServiceRegistry.Spawn")
}

func (r *fhLocalKeyedServiceRegistry) Observe(service any, handler func(service any, call Context) error) func() {
	panic("unported: fhLocalKeyedServiceRegistry.Observe")
}

func (r *fhLocalKeyedServiceRegistry) Dispose() error {
	panic("unported: fhLocalKeyedServiceRegistry.Dispose")
}

func (r *fhLocalKeyedServiceRegistry) registration(serviceId string) (*fhLocalKeyedRegistration, error) {
	panic("unported: fhLocalKeyedServiceRegistry.registration")
}

func (r *fhLocalKeyedServiceRegistry) assertActive() error {
	panic("unported: fhLocalKeyedServiceRegistry.assertActive")
}

type fhHostServiceSlots struct {
	mu           sync.Mutex
	singletons   *omap.Map[string, *ServiceSlot]
	keyedSources map[string]fhKeyedServiceSource
}

func fhNewHostServiceSlots() *fhHostServiceSlots {
	panic("unported: fhNewHostServiceSlots")
}

func (s *fhHostServiceSlots) GetSingleton(service any, assertAccess func()) any {
	panic("unported: fhHostServiceSlots.GetSingleton")
}

func (s *fhHostServiceSlots) HasSingleton(serviceId string) bool {
	panic("unported: fhHostServiceSlots.HasSingleton")
}

func (s *fhHostServiceSlots) Observe(service any, assertAccess func(), handler func(service any, call Context) error) (fhDisposal, error) {
	panic("unported: fhHostServiceSlots.Observe")
}

func (s *fhHostServiceSlots) BindSingleton(serviceId string, target any) {
	panic("unported: fhHostServiceSlots.BindSingleton")
}

func (s *fhHostServiceSlots) BindKeyed(serviceId string, services fhKeyedServiceSource) {
	panic("unported: fhHostServiceSlots.BindKeyed")
}

func (s *fhHostServiceSlots) Dispose() {
	panic("unported: fhHostServiceSlots.Dispose")
}

type fhServiceInstanceInstaller func(key string, implementation any) func()

type fhStagedServiceInstance struct {
	key            string
	implementation any
	release        func()
}

type fhStagedServiceSpawner struct {
	mu        sync.Mutex
	lifecycle *fhFacetLifecycle
	validate  func(key string, implementation any) error
	instances *omap.Map[string, *fhStagedServiceInstance]
	installer fhServiceInstanceInstaller
}

func fhNewStagedServiceSpawner(lifecycle *fhFacetLifecycle, validate func(key string, implementation any) error) *fhStagedServiceSpawner {
	panic("unported: fhNewStagedServiceSpawner")
}

func (s *fhStagedServiceSpawner) Connect(installer fhServiceInstanceInstaller) error {
	panic("unported: fhStagedServiceSpawner.Connect")
}

func (s *fhStagedServiceSpawner) Spawn(key string, implementation any) func() {
	panic("unported: fhStagedServiceSpawner.Spawn")
}

type fhFacetProvision interface {
	isFhFacetProvision()
}

type fhSingletonProvision struct {
	kind                ServiceMode
	service             any
	implementation      any
	install             func(provider *RemoteServiceProvider)
	validateReplacement func(provider *RemoteServiceProvider)
	replace             func(provider *RemoteServiceProvider)
}

func (*fhSingletonProvision) isFhFacetProvision() {}

type fhKeyedProvision struct {
	kind          ServiceMode
	service       any
	connectLocal  func(registry *fhLocalKeyedServiceRegistry)
	connectRemote func(provider *RemoteServiceProvider)
}

func (*fhKeyedProvision) isFhFacetProvision() {}

// FacetKernel is the private lifecycle and dependency kernel behind the atomic host entry point.
type FacetKernel struct {
	mu             sync.Mutex
	initialFacets  []Facet
	serviceSources []RemoteServiceSource
	onError        func(error)
	facets         *omap.Map[string, *fhFacetRuntime]
	serviceSlots   *fhHostServiceSlots
	// sourceBindings preserves source open order. RemoteServiceSource is an interface and cannot be an omap key.
	sourceBindings     []fhSourceBinding
	activationOrder    []string
	provider           *RemoteServiceProvider
	internalServices   RemoteServices
	localKeyedServices *fhLocalKeyedServiceRegistry
	phase              fhGenerationPhase // initial TS value is fhGenerationPhaseSetup
}

type fhSourceBinding struct {
	source   RemoteServiceSource
	services RemoteServices
}

func NewFacetKernel(options *FacetOptions) (*FacetKernel, error) {
	panic("unported: NewFacetKernel")
}

func (k *FacetKernel) Provider() (*RemoteServiceProvider, error) {
	panic("unported: FacetKernel.Provider")
}

func (k *FacetKernel) Activate() error {
	panic("unported: FacetKernel.Activate")
}

func (k *FacetKernel) Reload(facets []Facet) error {
	panic("unported: FacetKernel.Reload")
}

func (k *FacetKernel) Dispose() error {
	panic("unported: FacetKernel.Dispose")
}

func (k *FacetKernel) createFacetRuntime(facetId string) *fhFacetRuntime {
	panic("unported: FacetKernel.createFacetRuntime")
}

func (k *FacetKernel) setupFacet(facet Facet, record *fhFacetRuntime) error {
	panic("unported: FacetKernel.setupFacet")
}

func (k *FacetKernel) validateReplacementProvisions(provisions []fhFacetProvision) error {
	panic("unported: FacetKernel.validateReplacementProvisions")
}

func (k *FacetKernel) environment(runtime *fhFacetRuntime) FacetEnvironment {
	panic("unported: FacetKernel.environment")
}

func (k *FacetKernel) resolveExternalServices(records []*fhFacetRuntime) (*omap.Map[string, fhExternalService], error) {
	panic("unported: FacetKernel.resolveExternalServices")
}

func (k *FacetKernel) assembleProviders() error {
	panic("unported: FacetKernel.assembleProviders")
}

func (k *FacetKernel) bindServices(externalServices *omap.Map[string, fhExternalService]) error {
	panic("unported: FacetKernel.bindServices")
}

func (k *FacetKernel) provisions() []fhFacetProvision {
	panic("unported: FacetKernel.provisions")
}

func (k *FacetKernel) localKeyedRegistry() (*fhLocalKeyedServiceRegistry, error) {
	panic("unported: FacetKernel.localKeyedRegistry")
}

func (k *FacetKernel) internalServiceBinding() (RemoteServices, error) {
	panic("unported: FacetKernel.internalServiceBinding")
}

func (k *FacetKernel) disposeServiceBindings() ([]any, error) {
	panic("unported: FacetKernel.disposeServiceBindings")
}

func (k *FacetKernel) assertServiceTargetAccess() error {
	panic("unported: FacetKernel.assertServiceTargetAccess")
}

func (k *FacetKernel) abort(extraRecords []*fhFacetRuntime) ([]any, error) {
	panic("unported: FacetKernel.abort")
}

func (k *FacetKernel) terminate(extraRecords []*fhFacetRuntime) ([]any, error) {
	panic("unported: FacetKernel.terminate")
}

func (k *FacetKernel) disposeLifecycles() ([]any, error) {
	panic("unported: FacetKernel.disposeLifecycles")
}

func fhDisposeFacetRecords(records []*fhFacetRuntime) ([]any, error) {
	panic("unported: fhDisposeFacetRecords")
}

func fhValidateFacets(records []*fhFacetRuntime, externalServices *omap.Map[string, fhExternalService]) ([]string, error) {
	panic("unported: fhValidateFacets")
}

func fhTopologicalOrder(records []*fhFacetRuntime, dependencies *omap.Map[string, *omap.Set[string]], dependents *omap.Map[string, *omap.Set[string]]) ([]string, error) {
	panic("unported: fhTopologicalOrder")
}

func fhRecordServiceReference(target []fhFacetServiceReference, service any, mode ServiceMode) []fhFacetServiceReference {
	panic("unported: fhRecordServiceReference")
}

func fhSameFacetShape(left, right *fhFacetRuntime) bool {
	panic("unported: fhSameFacetShape")
}

func fhSameReferences(left, right []fhFacetServiceReference) bool {
	panic("unported: fhSameReferences")
}

func fhIsPromiseLike(value any) bool {
	panic("unported: fhIsPromiseLike")
}

var (
	_ ServiceSpawner[any]  = (*fhStagedServiceSpawner)(nil)
	_ fhKeyedServiceSource = (*fhLocalKeyedServiceRegistry)(nil)
	_ fhKeyedServiceSource = (*RemoteServiceBindingImpl)(nil)
)
