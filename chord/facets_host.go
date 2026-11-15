// Ported from packages/chord/src/facets/host.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/internal/js"
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
	return &fhFacetLifecycle{
		id:    id,
		state: fhLifecycleStateSettingUp,
	}
}

func (l *fhFacetLifecycle) AssertSettingUp(operation string) error {
	l.mu.Lock()
	state := l.state
	l.mu.Unlock()
	if state != fhLifecycleStateSettingUp {
		return fmt.Errorf("Facet %s can %s only during setup", l.id, operation)
	}
	return nil
}

func (l *fhFacetLifecycle) AssertRunning(operation string) error {
	l.mu.Lock()
	state := l.state
	l.mu.Unlock()
	if state != fhLifecycleStateSettingUp && state != fhLifecycleStateActive {
		return fmt.Errorf("Facet %s cannot %s while %s", l.id, operation, state)
	}
	return nil
}

func (l *fhFacetLifecycle) AssertActive(operation string) error {
	l.mu.Lock()
	state := l.state
	l.mu.Unlock()
	if state != fhLifecycleStateActive {
		return fmt.Errorf("Facet %s can %s only while active", l.id, operation)
	}
	return nil
}

func (l *fhFacetLifecycle) AssertServiceAccess() error {
	l.mu.Lock()
	ok := l.serviceAccess
	state := l.state
	l.mu.Unlock()
	if !ok {
		return fmt.Errorf("Facet %s service handles cannot be used while %s", l.id, state)
	}
	return nil
}

func (l *fhFacetLifecycle) Revoke() {
	l.mu.Lock()
	l.serviceAccess = false
	l.mu.Unlock()
}

func (l *fhFacetLifecycle) Own(disposal fhDisposal) error {
	l.mu.Lock()
	if l.state != fhLifecycleStateSettingUp && l.state != fhLifecycleStateActive {
		state := l.state
		l.mu.Unlock()
		return fmt.Errorf("Facet %s cannot own resources while %s", l.id, state)
	}
	l.effects = append(l.effects, disposal)
	l.mu.Unlock()
	return nil
}

func (l *fhFacetLifecycle) Observe(start func() fhDisposal) error {
	l.mu.Lock()
	if l.state != fhLifecycleStateSettingUp {
		l.mu.Unlock()
		return fmt.Errorf("Facet %s can observe services only during setup", l.id)
	}
	l.observations = append(l.observations, start)
	l.mu.Unlock()
	return nil
}

func (l *fhFacetLifecycle) OnActivate(callback func() error) error {
	l.mu.Lock()
	if l.state != fhLifecycleStateSettingUp {
		l.mu.Unlock()
		return fmt.Errorf("Facet %s can register activation callbacks only during setup", l.id)
	}
	l.activate = append(l.activate, callback)
	l.mu.Unlock()
	return nil
}

func (l *fhFacetLifecycle) Prepared() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != fhLifecycleStateSettingUp {
		return fmt.Errorf("Facet %s can finish setup only during setup", l.id)
	}
	l.state = fhLifecycleStatePrepared
	return nil
}

func (l *fhFacetLifecycle) Activate() error {
	l.mu.Lock()
	if l.state != fhLifecycleStatePrepared {
		l.mu.Unlock()
		return fmt.Errorf("Facet %s is not prepared", l.id)
	}
	l.state = fhLifecycleStateActive
	l.serviceAccess = true
	observations := slices.Clone(l.observations)
	callbacks := slices.Clone(l.activate)
	l.mu.Unlock()

	for _, start := range observations {
		if start == nil {
			continue
		}
		var startErr error
		var disposal fhDisposal
		func() {
			defer func() {
				if r := recover(); r != nil {
					startErr = ssToError(r)
				}
			}()
			disposal = start()
		}()
		if startErr != nil {
			return startErr
		}
		if disposal != nil {
			l.mu.Lock()
			l.effects = append(l.effects, disposal)
			l.mu.Unlock()
		}
	}
	for _, callback := range callbacks {
		if callback == nil {
			continue
		}
		var cbErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					cbErr = ssToError(r)
				}
			}()
			cbErr = callback()
		}()
		if cbErr != nil {
			return cbErr
		}
	}
	return nil
}

func (l *fhFacetLifecycle) Dispose() error {
	l.mu.Lock()
	if l.state == fhLifecycleStateDead {
		l.mu.Unlock()
		return nil
	}
	l.state = fhLifecycleStateDisposing
	effects := l.effects
	l.effects = nil
	l.observations = nil
	l.activate = nil
	l.mu.Unlock()

	var errs []any
	for i := len(effects) - 1; i >= 0; i-- {
		effect := effects[i]
		if effect == nil {
			continue
		}
		var effectErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					effectErr = ssToError(r)
				}
			}()
			effectErr = effect()
		}()
		if effectErr != nil {
			errs = append(errs, effectErr)
		}
	}

	l.mu.Lock()
	l.serviceAccess = false
	l.state = fhLifecycleStateDead
	l.mu.Unlock()

	return ssThrowCollectedErrors(errs, fmt.Sprintf("Failed to dispose facet %s", l.id))
}

type fhKeyedServiceSource interface {
	Observe(service any, handler func(service any, call Context) error) func()
}

type fhLocalInstance struct {
	key        string
	generation int
	service    any
}

func (i *fhLocalInstance) Key() string     { return i.key }
func (i *fhLocalInstance) Generation() int { return i.generation }
func (i *fhLocalInstance) Service() any    { return i.service }
func (i *fhLocalInstance) Deactivate()     {}

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
	seen := make(map[string]struct{}, len(services))
	regs := omap.NewMap[string, *fhLocalKeyedRegistration]()
	for _, svc := range services {
		if _, dup := seen[svc.Id]; dup {
			return nil, js.NewTypeError("Local keyed service registry has duplicate IDs")
		}
		seen[svc.Id] = struct{}{}
		regs.Set(svc.Id, &fhLocalKeyedRegistration{
			generations: make(map[string]int),
			directory: NewInstanceDirectory[InstanceDirectoryEntry](&InstanceDirectoryOptions{
				Ready:   true,
				OnError: onError,
			}),
		})
	}
	return &fhLocalKeyedServiceRegistry{
		registrations: regs,
	}, nil
}

func (r *fhLocalKeyedServiceRegistry) Spawn(service any, key string, implementation any) (func() error, error) {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, js.NewTypeError("Local service instance key must not be empty")
	}
	if !fhIsObjectValue(implementation) {
		return nil, js.NewTypeError(fmt.Sprintf("Local service %s implementation must be an object", head.id))
	}

	r.mu.Lock()
	if err := r.assertActive(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	reg, err := r.registration(head.id)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if _, live := reg.directory.Get(key); live {
		r.mu.Unlock()
		return nil, fmt.Errorf("Local service %s already has a live instance with key %s", head.id, key)
	}
	generation := reg.generations[key] + 1
	reg.generations[key] = generation
	r.mu.Unlock()

	instance := &fhLocalInstance{
		key:        key,
		generation: generation,
		service:    implementation,
	}
	if err := reg.directory.Insert(instance); err != nil {
		return nil, err
	}
	var closed atomic.Bool
	return func() error {
		if !closed.CompareAndSwap(false, true) {
			return nil
		}
		reg.directory.Remove(instance)
		return nil
	}, nil
}

func (r *fhLocalKeyedServiceRegistry) Observe(service any, handler func(service any, call Context) error) func() {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	r.mu.Lock()
	if err := r.assertActive(); err != nil {
		r.mu.Unlock()
		panic(err)
	}
	reg, err := r.registration(head.id)
	if err != nil {
		r.mu.Unlock()
		panic(err)
	}
	r.mu.Unlock()

	stop, err := reg.directory.Observe(handler)
	if err != nil {
		panic(err)
	}
	return stop
}

func (r *fhLocalKeyedServiceRegistry) Dispose() error {
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		return nil
	}
	r.disposed = true
	dirs := make([]*InstanceDirectory[InstanceDirectoryEntry], 0, r.registrations.Len())
	for _, reg := range r.registrations.All() {
		dirs = append(dirs, reg.directory)
	}
	r.registrations.Clear()
	r.mu.Unlock()

	for _, dir := range dirs {
		_ = dir.Dispose()
	}
	return nil
}

func (r *fhLocalKeyedServiceRegistry) registration(serviceId string) (*fhLocalKeyedRegistration, error) {
	reg, ok := r.registrations.Get(serviceId)
	if !ok {
		return nil, fmt.Errorf("Local keyed service %s is not registered", serviceId)
	}
	return reg, nil
}

func (r *fhLocalKeyedServiceRegistry) assertActive() error {
	if r.disposed {
		return errors.New("Local keyed service registry is disposed")
	}
	return nil
}

type fhHostServiceSlots struct {
	mu           sync.Mutex
	singletons   *omap.Map[string, *ServiceSlot]
	keyedSources map[string]fhKeyedServiceSource
}

func fhNewHostServiceSlots() *fhHostServiceSlots {
	return &fhHostServiceSlots{
		singletons:   omap.NewMap[string, *ServiceSlot](),
		keyedSources: make(map[string]fhKeyedServiceSource),
	}
}

func (s *fhHostServiceSlots) GetSingleton(service any, assertAccess func()) any {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	local := head.local != nil && *head.local

	s.mu.Lock()
	slot, ok := s.singletons.Get(head.id)
	if !ok {
		slot = NewServiceSlot(head.id, !local)
		s.singletons.Set(head.id, slot)
	}
	s.mu.Unlock()

	return slot.View(assertAccess)
}

func (s *fhHostServiceSlots) HasSingleton(serviceId string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.singletons.Has(serviceId)
}

func (s *fhHostServiceSlots) Observe(service any, assertAccess func(), handler func(service any, call Context) error) (fhDisposal, error) {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return nil, err
	}
	local := head.local != nil && *head.local

	s.mu.Lock()
	source, ok := s.keyedSources[head.id]
	s.mu.Unlock()
	if !ok || source == nil {
		return nil, fmt.Errorf("Service %s is disconnected", head.id)
	}

	var stopped atomic.Bool
	var stop func()
	var obsErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				obsErr = ssToError(r)
			}
		}()
		stop = source.Observe(service, func(target any, call Context) error {
			slot := NewServiceSlot(head.id, !local)
			slot.Bind(target)
			view := slot.View(func() {
				if assertAccess != nil {
					assertAccess()
				}
				var aborted bool
				if call != nil {
					if sig := call.AbortSignal(); sig != nil && sig.Err() != nil {
						aborted = true
					}
				}
				if stopped.Load() || aborted {
					panic(fmt.Errorf("Keyed service %s observation is closed", head.id))
				}
			})
			if handler == nil {
				return nil
			}
			return handler(view, call)
		})
	}()
	if obsErr != nil {
		return nil, obsErr
	}

	return func() error {
		if !stopped.CompareAndSwap(false, true) {
			return nil
		}
		if stop != nil {
			stop()
		}
		return nil
	}, nil
}

func (s *fhHostServiceSlots) BindSingleton(serviceId string, target any) {
	s.mu.Lock()
	slot, ok := s.singletons.Get(serviceId)
	s.mu.Unlock()
	if ok && slot != nil {
		slot.Bind(target)
	}
}

func (s *fhHostServiceSlots) BindKeyed(serviceId string, services fhKeyedServiceSource) {
	s.mu.Lock()
	s.keyedSources[serviceId] = services
	s.mu.Unlock()
}

func (s *fhHostServiceSlots) Dispose() {
	s.mu.Lock()
	slots := make([]*ServiceSlot, 0, s.singletons.Len())
	for _, slot := range s.singletons.All() {
		slots = append(slots, slot)
	}
	s.singletons.Clear()
	clear(s.keyedSources)
	s.mu.Unlock()

	for _, slot := range slots {
		slot.Unbind()
	}
}

type fhServiceInstanceInstaller func(key string, implementation any) func()

type fhStagedServiceInstance struct {
	key            string
	implementation any
	release        func() error
}

type fhStagedServiceSpawner struct {
	mu           sync.Mutex
	lifecycle    *fhFacetLifecycle
	validate     func(key string, implementation any) error
	instances    *omap.Map[string, *fhStagedServiceInstance]
	installer    fhServiceInstanceInstaller
	installerErr func(key string, implementation any) (func() error, error)
}

func fhNewStagedServiceSpawner(lifecycle *fhFacetLifecycle, validate func(key string, implementation any) error) *fhStagedServiceSpawner {
	return &fhStagedServiceSpawner{
		lifecycle: lifecycle,
		validate:  validate,
		instances: omap.NewMap[string, *fhStagedServiceInstance](),
	}
}

func (s *fhStagedServiceSpawner) Connect(installer fhServiceInstanceInstaller) error {
	return s.connectWithErr(func(key string, implementation any) (func() error, error) {
		rel := installer(key, implementation)
		return func() error {
			if rel != nil {
				rel()
			}
			return nil
		}, nil
	})
}

func (s *fhStagedServiceSpawner) connectWithErr(installer func(key string, implementation any) (func() error, error)) error {
	s.mu.Lock()
	if s.installer != nil || s.installerErr != nil {
		s.mu.Unlock()
		return errors.New("Facet service provider is already connected")
	}
	s.installerErr = installer
	s.installer = func(key string, implementation any) func() {
		rel, err := installer(key, implementation)
		if err != nil {
			panic(err)
		}
		return func() {
			if rel != nil {
				if err := rel(); err != nil {
					panic(err)
				}
			}
		}
	}
	snapshot := make([]*fhStagedServiceInstance, 0, s.instances.Len())
	for _, inst := range s.instances.All() {
		snapshot = append(snapshot, inst)
	}
	s.mu.Unlock()

	for _, inst := range snapshot {
		rel, err := installer(inst.key, inst.implementation)
		if err != nil {
			return err
		}
		s.mu.Lock()
		stillLive := false
		if cur, ok := s.instances.Get(inst.key); ok && cur == inst {
			inst.release = rel
			stillLive = true
		}
		s.mu.Unlock()
		if !stillLive && rel != nil {
			if err := rel(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *fhStagedServiceSpawner) Spawn(key string, implementation any) func() {
	if err := s.lifecycle.AssertActive("spawn service instances"); err != nil {
		panic(err)
	}
	if s.validate != nil {
		if err := s.validate(key, implementation); err != nil {
			panic(err)
		}
	}

	s.mu.Lock()
	if s.instances.Has(key) {
		s.mu.Unlock()
		panic(fmt.Errorf("Facet service already has a live instance with key %s", key))
	}
	instance := &fhStagedServiceInstance{
		key:            key,
		implementation: implementation,
	}
	s.instances.Set(key, instance)
	installer := s.installerErr
	s.mu.Unlock()

	if installer != nil {
		rel, err := installer(key, implementation)
		if err != nil {
			s.mu.Lock()
			if cur, ok := s.instances.Get(key); ok && cur == instance {
				s.instances.Delete(key)
			}
			s.mu.Unlock()
			panic(err)
		}
		s.mu.Lock()
		if cur, ok := s.instances.Get(key); ok && cur == instance {
			instance.release = rel
			rel = nil
		}
		s.mu.Unlock()
		if rel != nil {
			if err := rel(); err != nil {
				panic(err)
			}
		}
	}

	closeErr := func() error {
		s.mu.Lock()
		cur, ok := s.instances.Get(key)
		if !ok || cur != instance {
			s.mu.Unlock()
			return nil
		}
		s.instances.Delete(key)
		rel := instance.release
		instance.release = nil
		s.mu.Unlock()
		if rel != nil {
			return rel()
		}
		return nil
	}
	if err := s.lifecycle.Own(closeErr); err != nil {
		_ = closeErr()
		panic(err)
	}
	return func() {
		if err := closeErr(); err != nil {
			panic(err)
		}
	}
}

type fhFacetProvision interface {
	isFhFacetProvision()
}

type fhSingletonProvision struct {
	kind                   ServiceMode
	service                any
	serviceId              string
	serviceLocal           bool
	implementation         any
	install                func(provider *RemoteServiceProvider)
	validateReplacement    func(provider *RemoteServiceProvider)
	replace                func(provider *RemoteServiceProvider)
	installErr             func(provider *RemoteServiceProvider) error
	validateReplacementErr func(provider *RemoteServiceProvider) error
	replaceErr             func(provider *RemoteServiceProvider) error
}

func (*fhSingletonProvision) isFhFacetProvision() {}

type fhKeyedProvision struct {
	kind             ServiceMode
	service          any
	serviceId        string
	serviceLocal     bool
	connectLocal     func(registry *fhLocalKeyedServiceRegistry)
	connectRemote    func(provider *RemoteServiceProvider)
	connectLocalErr  func(registry *fhLocalKeyedServiceRegistry) error
	connectRemoteErr func(provider *RemoteServiceProvider) error
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
	if options == nil {
		return nil, js.NewTypeError("Facet options must not be nil")
	}
	seen := make(map[string]struct{}, len(options.Facets))
	for _, f := range options.Facets {
		if len(f.Id) == 0 {
			return nil, errors.New("Facet ID must not be empty")
		}
		if _, dup := seen[f.Id]; dup {
			return nil, errors.New("Facet IDs must be unique within a generation")
		}
		seen[f.Id] = struct{}{}
	}
	return &FacetKernel{
		initialFacets:  slices.Clone(options.Facets),
		serviceSources: slices.Clone(options.ServiceSources),
		onError:        options.OnError,
		facets:         omap.NewMap[string, *fhFacetRuntime](),
		serviceSlots:   fhNewHostServiceSlots(),
		phase:          fhGenerationPhaseSetup,
	}, nil
}

func (k *FacetKernel) Provider() (*RemoteServiceProvider, error) {
	k.mu.Lock()
	p := k.provider
	k.mu.Unlock()
	if p == nil {
		return nil, errors.New("Facet service provider is not assembled")
	}
	return p, nil
}

func (k *FacetKernel) Activate() error {
	records := make([]*fhFacetRuntime, 0, len(k.initialFacets))
	err := func() error {
		for _, facet := range k.initialFacets {
			record := k.createFacetRuntime(facet.Id)
			k.mu.Lock()
			k.facets.Set(facet.Id, record)
			k.mu.Unlock()
			if err := k.setupFacet(facet, record); err != nil {
				return err
			}
			records = append(records, record)
		}

		k.mu.Lock()
		k.phase = fhGenerationPhaseAssembling
		k.mu.Unlock()

		externalServices, err := k.resolveExternalServices(records)
		if err != nil {
			return err
		}
		order, err := fhValidateFacets(records, externalServices)
		if err != nil {
			return err
		}
		k.mu.Lock()
		k.activationOrder = order
		k.mu.Unlock()

		if err := k.assembleProviders(); err != nil {
			return err
		}
		if err := k.bindServices(externalServices); err != nil {
			return err
		}

		k.mu.Lock()
		k.phase = fhGenerationPhaseConnecting
		bindings := make([]RemoteServices, 0, len(k.sourceBindings)+1)
		for _, sb := range k.sourceBindings {
			bindings = append(bindings, sb.services)
		}
		internal := k.internalServices
		k.mu.Unlock()
		if internal != nil {
			bindings = append(bindings, internal)
		}
		readyFns := make([]func() error, 0, len(bindings))
		for _, s := range bindings {
			svc := s
			readyFns = append(readyFns, func() error {
				return svc.Ready(BackgroundContext)
			})
		}
		if err := scWaitAll(readyFns); err != nil {
			return err
		}

		k.mu.Lock()
		k.phase = fhGenerationPhaseActivating
		k.mu.Unlock()

		for _, id := range order {
			k.mu.Lock()
			rec, ok := k.facets.Get(id)
			k.mu.Unlock()
			if ok && rec != nil {
				if err := rec.lifecycle.Activate(); err != nil {
					return err
				}
			}
		}

		k.mu.Lock()
		k.phase = fhGenerationPhaseActive
		k.mu.Unlock()
		return nil
	}()
	if err != nil {
		cleanupErrs, _ := k.terminate(nil)
		if len(cleanupErrs) > 0 {
			return ssJoinErrors("Facet generation startup and cleanup failed", append([]any{err}, cleanupErrs...))
		}
		return err
	}
	return nil
}

func (k *FacetKernel) Reload(facets []Facet) error {
	k.mu.Lock()
	phase := k.phase
	if phase != fhGenerationPhaseActive {
		k.mu.Unlock()
		return fmt.Errorf("Facet host cannot reload while %s", phase)
	}
	seen := make(map[string]struct{}, len(facets))
	for _, f := range facets {
		if len(f.Id) == 0 {
			k.mu.Unlock()
			return errors.New("Facet ID must not be empty")
		}
		if _, dup := seen[f.Id]; dup {
			k.mu.Unlock()
			return errors.New("Reloaded facet IDs must be unique")
		}
		seen[f.Id] = struct{}{}
		if !k.facets.Has(f.Id) {
			k.mu.Unlock()
			return fmt.Errorf("Facet %s is not active", f.Id)
		}
	}
	k.phase = fhGenerationPhaseReloading
	activationOrder := slices.Clone(k.activationOrder)
	k.mu.Unlock()

	staged := make([]*fhFacetRuntime, 0, len(facets))
	candidates := make([]*fhFacetRuntime, 0, len(facets))
	stageErr := func() error {
		for _, facet := range facets {
			record := k.createFacetRuntime(facet.Id)
			staged = append(staged, record)
			if err := k.setupFacet(facet, record); err != nil {
				return err
			}
			k.mu.Lock()
			previous, _ := k.facets.Get(facet.Id)
			k.mu.Unlock()
			if !fhSameFacetShape(previous, record) {
				return fmt.Errorf("Reloaded facet %s must preserve its service requirements and provisions", facet.Id)
			}
			if err := k.validateReplacementProvisions(record.provisions); err != nil {
				return err
			}
			candidates = append(candidates, record)
		}
		return nil
	}()
	if stageErr != nil {
		revStaged := slices.Clone(staged)
		slices.Reverse(revStaged)
		cleanupErrs, _ := fhDisposeFacetRecords(revStaged)
		if len(cleanupErrs) > 0 {
			abortErrs, _ := k.abort(nil)
			errs := make([]any, 0, 1+len(cleanupErrs)+len(abortErrs))
			errs = append(errs, stageErr)
			errs = append(errs, cleanupErrs...)
			errs = append(errs, abortErrs...)
			return ssJoinErrors("Facet reload setup and cleanup failed", errs)
		}
		k.mu.Lock()
		k.phase = fhGenerationPhaseActive
		k.mu.Unlock()
		return stageErr
	}

	replacements := make(map[string]*fhFacetRuntime, len(candidates))
	for _, rec := range candidates {
		replacements[rec.facetId] = rec
	}
	var candidateOrder []*fhFacetRuntime
	for _, id := range activationOrder {
		if candidate, ok := replacements[id]; ok {
			candidateOrder = append(candidateOrder, candidate)
		}
	}

	activateErr := func() error {
		for _, candidate := range candidateOrder {
			if err := candidate.lifecycle.Activate(); err != nil {
				return err
			}
		}
		for _, candidate := range candidateOrder {
			if err := k.validateReplacementProvisions(candidate.provisions); err != nil {
				return err
			}
		}
		return nil
	}()
	if activateErr != nil {
		revCandidates := slices.Clone(candidateOrder)
		slices.Reverse(revCandidates)
		cleanupErrs, _ := fhDisposeFacetRecords(revCandidates)
		if len(cleanupErrs) > 0 {
			abortErrs, _ := k.abort(nil)
			errs := make([]any, 0, 1+len(cleanupErrs)+len(abortErrs))
			errs = append(errs, activateErr)
			errs = append(errs, cleanupErrs...)
			errs = append(errs, abortErrs...)
			return ssJoinErrors("Facet reload activation and cleanup failed", errs)
		}
		k.mu.Lock()
		k.phase = fhGenerationPhaseActive
		k.mu.Unlock()
		return activateErr
	}

	previous := make([]*fhFacetRuntime, 0, len(candidateOrder))
	k.mu.Lock()
	for _, candidate := range candidateOrder {
		if prev, ok := k.facets.Get(candidate.facetId); ok {
			previous = append(previous, prev)
		}
		k.facets.Set(candidate.facetId, candidate)
	}
	provider := k.provider
	localKeyed := k.localKeyedServices
	k.mu.Unlock()

	cutoverErr := func() error {
		for _, candidate := range candidateOrder {
			for _, provision := range candidate.provisions {
				sp, ok := provision.(*fhSingletonProvision)
				if !ok {
					continue
				}
				if sp.serviceLocal {
					k.serviceSlots.BindSingleton(sp.serviceId, sp.implementation)
				} else if err := sp.callReplace(provider); err != nil {
					return err
				}
			}
		}
		revPrev := slices.Clone(previous)
		slices.Reverse(revPrev)
		retirementErrs, _ := fhDisposeFacetRecords(revPrev)
		if len(retirementErrs) > 0 {
			return ssThrowCollectedErrors(retirementErrs, "Failed to retire replaced facets")
		}
		for _, candidate := range candidateOrder {
			for _, provision := range candidate.provisions {
				kp, ok := provision.(*fhKeyedProvision)
				if !ok {
					continue
				}
				if kp.serviceLocal {
					if err := kp.callConnectLocal(localKeyed); err != nil {
						return err
					}
				} else {
					if err := kp.callConnectRemote(provider); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}()
	if cutoverErr != nil {
		abortErrs, _ := k.abort(previous)
		return ssJoinErrors("Facet reload failed after cutover", append([]any{cutoverErr}, abortErrs...))
	}

	k.mu.Lock()
	k.phase = fhGenerationPhaseActive
	k.mu.Unlock()
	return nil
}

func (k *FacetKernel) Dispose() error {
	k.mu.Lock()
	phase := k.phase
	k.mu.Unlock()
	if phase == fhGenerationPhaseDead {
		return nil
	}
	if phase != fhGenerationPhaseActive {
		return fmt.Errorf("Facet host cannot be disposed while %s", phase)
	}
	errs, _ := k.terminate(nil)
	return ssThrowCollectedErrors(errs, "Failed to dispose facet generation")
}

func (k *FacetKernel) createFacetRuntime(facetId string) *fhFacetRuntime {
	return &fhFacetRuntime{
		facetId:        facetId,
		lifecycle:      fhNewFacetLifecycle(facetId),
		singletonViews: make(map[string]any),
	}
}

func (k *FacetKernel) setupFacet(facet Facet, record *fhFacetRuntime) error {
	var setupErr error
	if facet.Setup != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					setupErr = ssToError(r)
				}
			}()
			setupErr = facet.Setup(k.environment(record))
		}()
		if setupErr != nil {
			return setupErr
		}
	}
	return record.lifecycle.Prepared()
}

func (k *FacetKernel) validateReplacementProvisions(provisions []fhFacetProvision) error {
	provider, err := k.Provider()
	if err != nil {
		return err
	}
	for _, provision := range provisions {
		sp, ok := provision.(*fhSingletonProvision)
		if !ok || sp.serviceLocal {
			continue
		}
		if err := sp.callValidateReplacement(provider); err != nil {
			return err
		}
	}
	return nil
}

type fhFacetEnv struct {
	kernel  *FacetKernel
	runtime *fhFacetRuntime
}

func (e *fhFacetEnv) Provide(service any, implementation any) {
	if err := e.runtime.lifecycle.AssertSettingUp("provide services"); err != nil {
		panic(err)
	}
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	if !fhIsObjectValue(implementation) {
		panic(js.NewTypeError(fmt.Sprintf("Service %s implementation must be an object", head.id)))
	}
	local := head.local != nil && *head.local
	e.runtime.provides = fhRecordServiceReference(e.runtime.provides, service, ServiceModeSingleton)
	p := &fhSingletonProvision{
		kind:           ServiceModeSingleton,
		service:        service,
		serviceId:      head.id,
		serviceLocal:   local,
		implementation: implementation,
		installErr: func(provider *RemoteServiceProvider) error {
			return provider.Provide(service, implementation)
		},
		validateReplacementErr: func(provider *RemoteServiceProvider) error {
			return provider.ValidateReplacement(service, implementation)
		},
		replaceErr: func(provider *RemoteServiceProvider) error {
			return provider.Replace(service, implementation)
		},
	}
	p.install = func(provider *RemoteServiceProvider) {
		if err := p.installErr(provider); err != nil {
			panic(err)
		}
	}
	p.validateReplacement = func(provider *RemoteServiceProvider) {
		if err := p.validateReplacementErr(provider); err != nil {
			panic(err)
		}
	}
	p.replace = func(provider *RemoteServiceProvider) {
		if err := p.replaceErr(provider); err != nil {
			panic(err)
		}
	}
	e.runtime.provisions = append(e.runtime.provisions, p)
}

func (e *fhFacetEnv) ProvideMany(service any) any {
	if err := e.runtime.lifecycle.AssertSettingUp("provide service instances"); err != nil {
		panic(err)
	}
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	local := head.local != nil && *head.local
	e.runtime.provides = fhRecordServiceReference(e.runtime.provides, service, ServiceModeKeyed)
	instances := fhNewStagedServiceSpawner(e.runtime.lifecycle, func(key string, implementation any) error {
		if len(key) == 0 {
			return js.NewTypeError("Facet service instance key must not be empty")
		}
		if !fhIsObjectValue(implementation) {
			return js.NewTypeError(fmt.Sprintf("Facet service %s implementation must be an object", head.id))
		}
		if !local {
			if err := ValidateRemoteServiceImplementation(head.id, implementation); err != nil {
				return err
			}
		}
		return nil
	})
	kp := &fhKeyedProvision{
		kind:         ServiceModeKeyed,
		service:      service,
		serviceId:    head.id,
		serviceLocal: local,
		connectLocalErr: func(registry *fhLocalKeyedServiceRegistry) error {
			return instances.connectWithErr(func(key string, implementation any) (func() error, error) {
				return registry.Spawn(service, key, implementation)
			})
		},
		connectRemoteErr: func(provider *RemoteServiceProvider) error {
			return instances.connectWithErr(func(key string, implementation any) (func() error, error) {
				return provider.Spawn(service, key, implementation)
			})
		},
	}
	kp.connectLocal = func(registry *fhLocalKeyedServiceRegistry) {
		if err := kp.connectLocalErr(registry); err != nil {
			panic(err)
		}
	}
	kp.connectRemote = func(provider *RemoteServiceProvider) {
		if err := kp.connectRemoteErr(provider); err != nil {
			panic(err)
		}
	}
	e.runtime.provisions = append(e.runtime.provisions, kp)
	return instances
}

func (e *fhFacetEnv) Use(service any) any {
	if err := e.runtime.lifecycle.AssertSettingUp("acquire services"); err != nil {
		panic(err)
	}
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	e.runtime.requires = fhRecordServiceReference(e.runtime.requires, service, ServiceModeSingleton)
	if view, ok := e.runtime.singletonViews[head.id]; ok {
		return view
	}
	view := e.kernel.serviceSlots.GetSingleton(service, func() {
		if err := e.runtime.lifecycle.AssertServiceAccess(); err != nil {
			panic(err)
		}
	})
	e.runtime.singletonViews[head.id] = view
	return view
}

func (e *fhFacetEnv) Observe(service any, handler func(service any, call Context) error) {
	if err := e.runtime.lifecycle.AssertSettingUp("observe services"); err != nil {
		panic(err)
	}
	e.runtime.requires = fhRecordServiceReference(e.runtime.requires, service, ServiceModeKeyed)
	if err := e.runtime.lifecycle.Observe(func() fhDisposal {
		d, err := e.kernel.serviceSlots.Observe(service, func() {
			if err := e.runtime.lifecycle.AssertServiceAccess(); err != nil {
				panic(err)
			}
		}, handler)
		if err != nil {
			panic(err)
		}
		return d
	}); err != nil {
		panic(err)
	}
}

func (e *fhFacetEnv) ReplicatedState(initial any) MutableReplicatedState[any] {
	if err := e.runtime.lifecycle.AssertRunning("create replicated state"); err != nil {
		panic(err)
	}
	return NewMutableReplicatedStateImpl(initial)
}

func (e *fhFacetEnv) Own(disposal func() error) {
	if err := e.runtime.lifecycle.Own(fhDisposal(disposal)); err != nil {
		panic(err)
	}
}

func (e *fhFacetEnv) OnActivate(callback func() error) {
	if err := e.runtime.lifecycle.OnActivate(callback); err != nil {
		panic(err)
	}
}

func (e *fhFacetEnv) OnDeactivate(callback func() error) {
	if err := e.runtime.lifecycle.Own(fhDisposal(callback)); err != nil {
		panic(err)
	}
}

func (k *FacetKernel) environment(runtime *fhFacetRuntime) FacetEnvironment {
	return &fhFacetEnv{kernel: k, runtime: runtime}
}

func (k *FacetKernel) resolveExternalServices(records []*fhFacetRuntime) (*omap.Map[string, fhExternalService], error) {
	k.mu.Lock()
	sources := slices.Clone(k.serviceSources)
	onError := k.onError
	k.mu.Unlock()

	type catEntry struct {
		source  RemoteServiceSource
		entries []ServiceCatalogueEntry
		err     error
	}
	catalogues := make([]catEntry, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func(idx int, s RemoteServiceSource) {
			defer wg.Done()
			entries, err := s.Catalogue(BackgroundContext)
			catalogues[idx] = catEntry{source: s, entries: entries, err: err}
		}(i, src)
	}
	wg.Wait()
	for _, cat := range catalogues {
		if cat.err != nil {
			return nil, cat.err
		}
	}

	type offeredEntry struct {
		mode      ServiceMode
		source    RemoteServiceSource
		sourceIdx int
	}
	offered := omap.NewMap[string, offeredEntry]()
	for idx, cat := range catalogues {
		for _, entry := range cat.entries {
			if offered.Has(entry.ServiceId) {
				return nil, fmt.Errorf("Facet host service %s is offered by more than one source", entry.ServiceId)
			}
			offered.Set(entry.ServiceId, offeredEntry{
				mode:      entry.Mode,
				source:    cat.source,
				sourceIdx: idx,
			})
		}
	}

	local := make(map[string]struct{})
	for _, rec := range records {
		for _, prov := range rec.provides {
			local[prov.serviceId] = struct{}{}
		}
	}

	external := omap.NewMap[string, fhExternalService]()
	externalSourceIdx := make(map[string]int)
	for _, rec := range records {
		for _, req := range rec.requires {
			if _, isLocal := local[req.serviceId]; isLocal || external.Has(req.serviceId) {
				continue
			}
			srcEntry, ok := offered.Get(req.serviceId)
			if !ok {
				var deferredIdx []int
				for idx, src := range sources {
					if src != nil && src.AcceptsUnavailableServices() {
						deferredIdx = append(deferredIdx, idx)
					}
				}
				if len(deferredIdx) > 1 {
					return nil, fmt.Errorf("Facet host service %s has more than one deferred source", req.serviceId)
				}
				if len(deferredIdx) == 1 {
					idx := deferredIdx[0]
					srcEntry = offeredEntry{
						mode:      req.mode,
						source:    sources[idx],
						sourceIdx: idx,
					}
					ok = true
				}
			}
			if ok {
				external.Set(req.serviceId, fhExternalService{
					service: req.service,
					mode:    srcEntry.mode,
					source:  srcEntry.source,
				})
				externalSourceIdx[req.serviceId] = srcEntry.sourceIdx
			}
		}
	}

	bySourceOrder := make([]int, 0)
	refsBySourceIdx := make(map[int][]ServiceRef)
	for serviceId := range external.All() {
		idx := externalSourceIdx[serviceId]
		if _, exists := refsBySourceIdx[idx]; !exists {
			bySourceOrder = append(bySourceOrder, idx)
		}
		refsBySourceIdx[idx] = append(refsBySourceIdx[idx], ServiceRef{Id: serviceId})
	}

	bindings := make([]fhSourceBinding, 0, len(bySourceOrder))
	for _, idx := range bySourceOrder {
		src := sources[idx]
		opened := src.Open(&RemoteServiceSourceOpenOptions{
			Services: refsBySourceIdx[idx],
			AssertAccess: func() {
				if err := k.assertServiceTargetAccess(); err != nil {
					panic(err)
				}
			},
			OnError: onError,
		})
		bindings = append(bindings, fhSourceBinding{
			source:   src,
			services: opened,
		})
	}

	k.mu.Lock()
	k.sourceBindings = bindings
	k.mu.Unlock()
	return external, nil
}

func (k *FacetKernel) assembleProviders() error {
	provisions := k.provisions()
	var remoteDefs []RemoteServiceProviderEntry
	var remoteRefs []ServiceRef
	var localKeyedRefs []ServiceRef
	for _, provision := range provisions {
		switch p := provision.(type) {
		case *fhSingletonProvision:
			if !p.serviceLocal {
				mode := ServiceModeSingleton
				local := p.serviceLocal
				remoteDefs = append(remoteDefs, RemoteServiceProviderEntry{
					Id:    p.serviceId,
					Local: &local,
					Mode:  &mode,
				})
				remoteRefs = append(remoteRefs, ServiceRef{Id: p.serviceId})
			}
		case *fhKeyedProvision:
			if !p.serviceLocal {
				mode := ServiceModeKeyed
				local := p.serviceLocal
				remoteDefs = append(remoteDefs, RemoteServiceProviderEntry{
					Id:    p.serviceId,
					Local: &local,
					Mode:  &mode,
				})
				remoteRefs = append(remoteRefs, ServiceRef{Id: p.serviceId})
			} else {
				localKeyedRefs = append(localKeyedRefs, ServiceRef{Id: p.serviceId})
			}
		}
	}

	provider, err := NewRemoteServiceProvider(remoteDefs)
	if err != nil {
		return err
	}
	k.mu.Lock()
	onError := k.onError
	k.mu.Unlock()

	internalServices, err := NewRemoteServiceBindingImpl(&RemoteServiceBindingOptions{
		Services:  remoteRefs,
		Transport: CreateLoopbackServiceTransport(provider),
		AssertAccess: func() {
			if err := k.assertServiceTargetAccess(); err != nil {
				panic(err)
			}
		},
		OnError: onError,
	})
	if err != nil {
		_ = provider.Dispose()
		return err
	}

	localKeyedServices, err := fhNewLocalKeyedServiceRegistry(localKeyedRefs, onError)
	if err != nil {
		_ = internalServices.Dispose(BackgroundContext)
		_ = provider.Dispose()
		return err
	}

	k.mu.Lock()
	k.provider = provider
	k.internalServices = internalServices
	k.localKeyedServices = localKeyedServices
	k.mu.Unlock()

	for _, provision := range provisions {
		switch p := provision.(type) {
		case *fhSingletonProvision:
			if !p.serviceLocal {
				if err := p.callInstall(provider); err != nil {
					return err
				}
			}
		case *fhKeyedProvision:
			if p.serviceLocal {
				if err := p.callConnectLocal(localKeyedServices); err != nil {
					return err
				}
			} else {
				if err := p.callConnectRemote(provider); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (k *FacetKernel) bindServices(externalServices *omap.Map[string, fhExternalService]) error {
	internalBinding, err := k.internalServiceBinding()
	if err != nil {
		return err
	}
	localKeyed, err := k.localKeyedRegistry()
	if err != nil {
		return err
	}

	for _, provision := range k.provisions() {
		switch p := provision.(type) {
		case *fhSingletonProvision:
			if !k.serviceSlots.HasSingleton(p.serviceId) {
				continue
			}
			var target any
			if p.serviceLocal {
				target = p.implementation
			} else {
				target = internalBinding.Use(p.service)
			}
			k.serviceSlots.BindSingleton(p.serviceId, target)
		case *fhKeyedProvision:
			if p.serviceLocal {
				k.serviceSlots.BindKeyed(p.serviceId, localKeyed)
			} else {
				k.serviceSlots.BindKeyed(p.serviceId, internalBinding)
			}
		}
	}

	k.mu.Lock()
	sourceBindings := slices.Clone(k.sourceBindings)
	sources := slices.Clone(k.serviceSources)
	k.mu.Unlock()

	for serviceId, ext := range externalServices.All() {
		services := fhLookupSourceBinding(sourceBindings, sources, ext.source)
		if services == nil {
			return fmt.Errorf("Service source for %s is not open", serviceId)
		}
		if ext.mode == ServiceModeSingleton {
			k.serviceSlots.BindSingleton(serviceId, services.Use(ext.service))
		} else {
			k.serviceSlots.BindKeyed(serviceId, services)
		}
	}
	return nil
}

func (k *FacetKernel) provisions() []fhFacetProvision {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []fhFacetProvision
	for _, rec := range k.facets.All() {
		out = append(out, rec.provisions...)
	}
	return out
}

func (k *FacetKernel) localKeyedRegistry() (*fhLocalKeyedServiceRegistry, error) {
	k.mu.Lock()
	reg := k.localKeyedServices
	k.mu.Unlock()
	if reg == nil {
		return nil, errors.New("Facet keyed services are not assembled")
	}
	return reg, nil
}

func (k *FacetKernel) internalServiceBinding() (RemoteServices, error) {
	k.mu.Lock()
	svc := k.internalServices
	k.mu.Unlock()
	if svc == nil {
		return nil, errors.New("Facet remote services are not assembled")
	}
	return svc, nil
}

func (k *FacetKernel) disposeServiceBindings() ([]any, error) {
	k.mu.Lock()
	bindings := make([]RemoteServices, 0, len(k.sourceBindings)+1)
	for _, sb := range k.sourceBindings {
		bindings = append(bindings, sb.services)
	}
	k.sourceBindings = nil
	if k.internalServices != nil {
		bindings = append(bindings, k.internalServices)
		k.internalServices = nil
	}
	k.mu.Unlock()

	if len(bindings) == 0 {
		return nil, nil
	}
	errs := make([]error, len(bindings))
	var wg sync.WaitGroup
	for i, svc := range bindings {
		if svc == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, s RemoteServices) {
			defer wg.Done()
			errs[idx] = s.Dispose(BackgroundContext)
		}(i, svc)
	}
	wg.Wait()

	var rejected []any
	for _, err := range errs {
		if err != nil {
			rejected = append(rejected, err)
		}
	}
	return rejected, nil
}

func (k *FacetKernel) assertServiceTargetAccess() error {
	k.mu.Lock()
	phase := k.phase
	k.mu.Unlock()
	if phase != fhGenerationPhaseActivating &&
		phase != fhGenerationPhaseActive &&
		phase != fhGenerationPhaseReloading &&
		phase != fhGenerationPhaseDisposing {
		return fmt.Errorf("Facet service targets cannot be used during %s", phase)
	}
	return nil
}

func (k *FacetKernel) abort(extraRecords []*fhFacetRuntime) ([]any, error) {
	k.mu.Lock()
	records := make([]*fhFacetRuntime, 0, k.facets.Len())
	for _, rec := range k.facets.All() {
		records = append(records, rec)
	}
	k.mu.Unlock()

	for _, rec := range records {
		rec.lifecycle.Revoke()
	}
	for _, rec := range extraRecords {
		rec.lifecycle.Revoke()
	}
	return k.terminate(extraRecords)
}

func (k *FacetKernel) terminate(extraRecords []*fhFacetRuntime) ([]any, error) {
	k.mu.Lock()
	k.phase = fhGenerationPhaseDisposing
	k.mu.Unlock()

	errs, _ := k.disposeLifecycles()

	revExtra := slices.Clone(extraRecords)
	slices.Reverse(revExtra)
	extraErrs, _ := fhDisposeFacetRecords(revExtra)
	errs = append(errs, extraErrs...)

	k.mu.Lock()
	localKeyed := k.localKeyedServices
	k.localKeyedServices = nil
	provider := k.provider
	k.mu.Unlock()

	if localKeyed != nil {
		if err := localKeyed.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}

	bindingErrs, _ := k.disposeServiceBindings()
	errs = append(errs, bindingErrs...)

	k.serviceSlots.Dispose()

	if provider != nil {
		if err := provider.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}

	k.mu.Lock()
	k.phase = fhGenerationPhaseDead
	k.mu.Unlock()
	return errs, nil
}

func (k *FacetKernel) disposeLifecycles() ([]any, error) {
	k.mu.Lock()
	var order []string
	if len(k.activationOrder) > 0 {
		order = slices.Clone(k.activationOrder)
	} else {
		order = k.facets.Keys()
	}
	slices.Reverse(order)
	k.mu.Unlock()

	var errs []any
	for _, id := range order {
		k.mu.Lock()
		rec, ok := k.facets.Get(id)
		if ok {
			k.facets.Delete(id)
		}
		k.mu.Unlock()
		if !ok || rec == nil {
			continue
		}
		if err := rec.lifecycle.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs, nil
}

func (p *fhSingletonProvision) callInstall(provider *RemoteServiceProvider) error {
	if p.installErr != nil {
		return p.installErr(provider)
	}
	if p.install != nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = ssToError(r)
				}
			}()
			p.install(provider)
		}()
		return err
	}
	return nil
}

func (p *fhSingletonProvision) callValidateReplacement(provider *RemoteServiceProvider) error {
	if p.validateReplacementErr != nil {
		return p.validateReplacementErr(provider)
	}
	if p.validateReplacement != nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = ssToError(r)
				}
			}()
			p.validateReplacement(provider)
		}()
		return err
	}
	return nil
}

func (p *fhSingletonProvision) callReplace(provider *RemoteServiceProvider) error {
	if p.replaceErr != nil {
		return p.replaceErr(provider)
	}
	if p.replace != nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = ssToError(r)
				}
			}()
			p.replace(provider)
		}()
		return err
	}
	return nil
}

func (p *fhKeyedProvision) callConnectLocal(registry *fhLocalKeyedServiceRegistry) error {
	if p.connectLocalErr != nil {
		return p.connectLocalErr(registry)
	}
	if p.connectLocal != nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = ssToError(r)
				}
			}()
			p.connectLocal(registry)
		}()
		return err
	}
	return nil
}

func (p *fhKeyedProvision) callConnectRemote(provider *RemoteServiceProvider) error {
	if p.connectRemoteErr != nil {
		return p.connectRemoteErr(provider)
	}
	if p.connectRemote != nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = ssToError(r)
				}
			}()
			p.connectRemote(provider)
		}()
		return err
	}
	return nil
}

func fhDisposeFacetRecords(records []*fhFacetRuntime) ([]any, error) {
	var errs []any
	for _, record := range records {
		if record == nil || record.lifecycle == nil {
			continue
		}
		if err := record.lifecycle.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs, nil
}

func fhValidateFacets(records []*fhFacetRuntime, externalServices *omap.Map[string, fhExternalService]) ([]string, error) {
	type providerInfo struct {
		hasFacetId bool
		facetId    string
		hasMode    bool
		mode       ServiceMode
	}
	providers := omap.NewMap[string, providerInfo]()
	for serviceId, ext := range externalServices.All() {
		providers.Set(serviceId, providerInfo{
			hasFacetId: false,
			hasMode:    true,
			mode:       ext.mode,
		})
	}
	for _, rec := range records {
		for _, prov := range rec.provides {
			if existing, ok := providers.Get(prov.serviceId); ok {
				if existing.hasMode && existing.mode != prov.mode {
					return nil, fmt.Errorf("Service %s is provided as both singleton and keyed", prov.serviceId)
				}
				if !existing.hasFacetId {
					return nil, fmt.Errorf("Service %s is provided by both the host and %s", prov.serviceId, rec.facetId)
				}
				return nil, fmt.Errorf("Service %s is provided by both %s and %s", prov.serviceId, existing.facetId, rec.facetId)
			}
			providers.Set(prov.serviceId, providerInfo{
				hasFacetId: true,
				facetId:    rec.facetId,
				hasMode:    true,
				mode:       prov.mode,
			})
		}
	}

	dependencies := omap.NewMap[string, *omap.Set[string]]()
	dependents := omap.NewMap[string, *omap.Set[string]]()
	for _, rec := range records {
		dependencies.Set(rec.facetId, omap.NewSet[string]())
		dependents.Set(rec.facetId, omap.NewSet[string]())
	}
	for _, rec := range records {
		for _, req := range rec.requires {
			prov, ok := providers.Get(req.serviceId)
			if !ok {
				return nil, fmt.Errorf("Facet %s requires local/%s/%s, but no facet provides it", rec.facetId, req.serviceId, req.mode)
			}
			if prov.hasMode && prov.mode != req.mode {
				owner := "the host"
				if prov.hasFacetId {
					owner = prov.facetId
				}
				return nil, fmt.Errorf("Facet %s requires %s as %s, but %s provides it as %s", rec.facetId, req.serviceId, req.mode, owner, prov.mode)
			}
			if !prov.hasFacetId || prov.facetId == rec.facetId {
				continue
			}
			if depSet, ok := dependencies.Get(rec.facetId); ok {
				depSet.Add(prov.facetId)
			}
			if revSet, ok := dependents.Get(prov.facetId); ok {
				revSet.Add(rec.facetId)
			}
		}
	}
	return fhTopologicalOrder(records, dependencies, dependents)
}

func fhTopologicalOrder(records []*fhFacetRuntime, dependencies *omap.Map[string, *omap.Set[string]], dependents *omap.Map[string, *omap.Set[string]]) ([]string, error) {
	remaining := omap.NewMap[string, int]()
	for id, values := range dependencies.All() {
		remaining.Set(id, values.Len())
	}
	var ready []string
	for _, rec := range records {
		if count, _ := remaining.Get(rec.facetId); count == 0 {
			ready = append(ready, rec.facetId)
		}
	}
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		if deps, ok := dependents.Get(id); ok {
			for dependent := range deps.All() {
				cur, _ := remaining.Get(dependent)
				next := cur - 1
				remaining.Set(dependent, next)
				if next == 0 {
					ready = append(ready, dependent)
				}
			}
		}
	}
	if len(order) != len(records) {
		var cycle []string
		for _, rec := range records {
			if count, _ := remaining.Get(rec.facetId); count > 0 {
				cycle = append(cycle, rec.facetId)
			}
		}
		return nil, fmt.Errorf("Facet dependency cycle: %s", strings.Join(cycle, ", "))
	}
	return order, nil
}

func fhRecordServiceReference(target []fhFacetServiceReference, service any, mode ServiceMode) []fhFacetServiceReference {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	for _, ref := range target {
		if ref.serviceId == head.id && ref.mode == mode {
			return target
		}
	}
	return append(target, fhFacetServiceReference{
		serviceId: head.id,
		service:   service,
		mode:      mode,
	})
}

func fhSameFacetShape(left, right *fhFacetRuntime) bool {
	if left == nil || right == nil {
		return left == right
	}
	return fhSameReferences(left.requires, right.requires) && fhSameReferences(left.provides, right.provides)
}

func fhSameReferences(left, right []fhFacetServiceReference) bool {
	if len(left) != len(right) {
		return false
	}
	for _, l := range left {
		found := false
		for _, r := range right {
			if l.serviceId == r.serviceId && l.mode == r.mode {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func fhIsPromiseLike(_ any) bool {
	return false
}

func fhIsObjectValue(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Interface:
		return !rv.IsNil()
	case reflect.Struct:
		return true
	default:
		return false
	}
}

func fhLookupSourceBinding(bindings []fhSourceBinding, _ []RemoteServiceSource, target RemoteServiceSource) RemoteServices {
	ta := any(target)
	tt := reflect.TypeOf(ta)
	for _, b := range bindings {
		ba := any(b.source)
		if tt != nil && tt == reflect.TypeOf(ba) && tt.Comparable() && ba == ta {
			return b.services
		}
	}
	return nil
}

var (
	_ ServiceSpawner[any]  = (*fhStagedServiceSpawner)(nil)
	_ fhKeyedServiceSource = (*fhLocalKeyedServiceRegistry)(nil)
	_ fhKeyedServiceSource = (*RemoteServiceBindingImpl)(nil)
)
