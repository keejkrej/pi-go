// Ported from packages/chord/src/services/provider.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// spRemoteMethod is one exposed method. It returns (value, hasValue, err).
// ok == false represents TS undefined; ok == true with nil value represents JSON null.
type spRemoteMethod func(args ...any) (JsonValue, bool, error)

type spServiceMemberKind string

const (
	spServiceMemberKindMethod spServiceMemberKind = "method"
	spServiceMemberKindState  spServiceMemberKind = "state"
)

type spInstanceMember interface {
	isSpInstanceMember()
}

type spMethodMember struct {
	kind   spServiceMemberKind
	method spRemoteMethod
}

func (spMethodMember) isSpInstanceMember() {}

type spStateMember struct {
	kind  spServiceMemberKind
	state ReplicatedStateInternals
}

func (spStateMember) isSpInstanceMember() {}

type spClassifiedRemoteServiceImplementation struct {
	implementation any
	members        *omap.Map[string, spInstanceMember]
}

type spProviderInstance struct {
	address               *ServiceInstanceAddress
	implementation        any
	members               *omap.Map[string, spInstanceMember]
	removeMemberListeners []func()
	active                bool
}

type spBufferedUpdate struct {
	update  ServiceProviderUpdate
	context Context
}

type spProviderSubscriber struct {
	mu                sync.Mutex
	listener          func(update ServiceProviderUpdate, context Context) error
	buffer            []spBufferedUpdate
	snapshotSequences *omap.Map[string, int]
	active            bool
	draining          bool
	terminated        bool
	closed            bool
}

// spServiceHead is the { id, local? } object stored on a provider definition.
type spServiceHead struct {
	id    string
	local *bool
}

type spServiceProviderDefinition struct {
	service spServiceHead
	mode    ServiceMode
}

type spServiceRegistration struct {
	serviceId      string
	mode           ServiceMode
	singleton      *spProviderInstance
	singletonShape map[string]spServiceMemberKind
	instances      *omap.Map[string, *spProviderInstance]
	generations    map[string]int
	subscribers    *omap.Set[*spProviderSubscriber]
}

// ServiceUpdatePublisher publishes one subscription update. The error result is the TS promise rejection.
type ServiceUpdatePublisher func(subscriptionId string, update ServiceProviderUpdate, context Context) error

// RemoteServiceEndpoint hosts one provider for one remote consumer and owns that consumer's subscriptions.
type RemoteServiceEndpoint interface {
	Invoke(call ServiceCall, publish ServiceUpdatePublisher, callContext Context) (value JsonValue, ok bool, err error)
	Dispose()
}

// RemoteServiceProviderEntry is one NewRemoteServiceProvider argument.
// TS accepts ServiceProviderDefinition | { id: string }. A nil Mode is the bare { id } form and defaults to singleton.
// A nil Local means the local field was absent.
type RemoteServiceProviderEntry struct {
	Id    string
	Local *bool
	Mode  *ServiceMode
}

// RemoteServiceProvider hosts allowlisted services for one remote consumer.
type RemoteServiceProvider struct {
	mu            sync.Mutex
	catalogue     []ServiceCatalogueEntry
	registrations *omap.Map[string, *spServiceRegistration]
	disposed      bool
}

// NewRemoteServiceProvider builds a provider from an allowlist. Duplicate ids and local services fail.
func NewRemoteServiceProvider(entries []RemoteServiceProviderEntry) (*RemoteServiceProvider, error) {
	definitions := make([]spServiceProviderDefinition, len(entries))
	for i, entry := range entries {
		mode := ServiceModeSingleton
		if entry.Mode != nil {
			mode = *entry.Mode
		}
		if mode != ServiceModeSingleton && mode != ServiceModeKeyed {
			return nil, js.NewTypeError(fmt.Sprintf("Invalid remote service mode: %s", mode))
		}
		if entry.Local != nil && *entry.Local {
			return nil, js.NewTypeError(fmt.Sprintf("Local service %s cannot be published remotely", entry.Id))
		}
		definitions[i] = spServiceProviderDefinition{
			service: spServiceHead{id: entry.Id, local: entry.Local},
			mode:    mode,
		}
	}

	seen := omap.NewSet[string]()
	for _, def := range definitions {
		if seen.Has(def.service.id) {
			return nil, js.NewTypeError("Remote service catalogue contains duplicate IDs")
		}
		seen.Add(def.service.id)
	}

	catalogue := make([]ServiceCatalogueEntry, len(definitions))
	registrations := omap.NewMap[string, *spServiceRegistration]()
	for i, def := range definitions {
		catalogue[i] = ServiceCatalogueEntry{
			ServiceId: def.service.id,
			Mode:      def.mode,
		}
		registrations.Set(def.service.id, &spServiceRegistration{
			serviceId:   def.service.id,
			mode:        def.mode,
			instances:   omap.NewMap[string, *spProviderInstance](),
			generations: map[string]int{},
			subscribers: omap.NewSet[*spProviderSubscriber](),
		})
	}

	return &RemoteServiceProvider{
		catalogue:     catalogue,
		registrations: registrations,
	}, nil
}

func (p *RemoteServiceProvider) Catalogue() []ServiceCatalogueEntry {
	return slices.Clone(p.catalogue)
}

// Provide publishes one singleton. service's dynamic type is *Service[T].
func (p *RemoteServiceProvider) Provide(service any, implementation any) error {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.assertActive(); err != nil {
		return err
	}
	if err := p.assertRemotable(head); err != nil {
		return err
	}
	if err := p.assertAllowed(head.id); err != nil {
		return err
	}
	reg, err := p.registration(head.id, ServiceModeSingleton)
	if err != nil {
		return err
	}
	if reg.singleton != nil {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceModeMismatch,
			fmt.Sprintf("Remote service %s already has a provider", head.id),
		)
	}
	classified, err := spClassifyRemoteServiceImplementation(reg.serviceId, implementation)
	if err != nil {
		return err
	}
	shape := spServiceMemberShape(classified.members)
	if err := p.assertSingletonShape(reg, shape); err != nil {
		return err
	}
	reg.singleton = p.createInstance(reg, classified, nil)
	reg.singletonShape = shape
	return nil
}

// Withdraw disconnects one singleton while preserving active subscriptions and remote facades.
func (p *RemoteServiceProvider) Withdraw(service any) error {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if err := p.assertActive(); err != nil {
		p.mu.Unlock()
		return err
	}
	if err := p.assertRemotable(head); err != nil {
		p.mu.Unlock()
		return err
	}
	if err := p.assertAllowed(head.id); err != nil {
		p.mu.Unlock()
		return err
	}
	reg, err := p.registration(head.id, ServiceModeSingleton)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	previous := reg.singleton
	if previous == nil {
		p.mu.Unlock()
		return nil
	}
	previous.active = false
	removals := slices.Clone(previous.removeMemberListeners)
	reg.singleton = nil
	subscribers := p.queueEmitLocked(reg, ServiceProviderUpdateUnavailable{Type: "unavailable"}, nil)
	p.mu.Unlock()

	for _, remove := range removals {
		remove()
	}
	return spDrainSubscribers(subscribers, fmt.Sprintf("Failed to publish remote service %s update", reg.serviceId))
}

// ValidateReplacement checks a singleton replacement without changing the active provider.
func (p *RemoteServiceProvider) ValidateReplacement(service any, implementation any) error {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.assertActive(); err != nil {
		return err
	}
	if err := p.assertRemotable(head); err != nil {
		return err
	}
	if err := p.assertAllowed(head.id); err != nil {
		return err
	}
	reg, err := p.registration(head.id, ServiceModeSingleton)
	if err != nil {
		return err
	}
	classified, err := spClassifyRemoteServiceImplementation(reg.serviceId, implementation)
	if err != nil {
		return err
	}
	return p.assertSingletonShape(reg, spServiceMemberShape(classified.members))
}

// Replace replaces one singleton without making its stable remote facade unavailable.
func (p *RemoteServiceProvider) Replace(service any, implementation any) error {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if err := p.assertActive(); err != nil {
		p.mu.Unlock()
		return err
	}
	if err := p.assertRemotable(head); err != nil {
		p.mu.Unlock()
		return err
	}
	if err := p.assertAllowed(head.id); err != nil {
		p.mu.Unlock()
		return err
	}
	reg, err := p.registration(head.id, ServiceModeSingleton)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	classified, err := spClassifyRemoteServiceImplementation(reg.serviceId, implementation)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	shape := spServiceMemberShape(classified.members)
	if err := p.assertSingletonShape(reg, shape); err != nil {
		p.mu.Unlock()
		return err
	}
	replacement := p.createInstance(reg, classified, nil)
	previous := reg.singleton
	var removals []func()
	if previous != nil {
		previous.active = false
		removals = slices.Clone(previous.removeMemberListeners)
	}
	reg.singleton = replacement
	reg.singletonShape = shape
	snap := p.snapshotInstance(replacement)
	subscribers := p.queueEmitLocked(reg, ServiceProviderUpdateReplaced{Type: "replaced", Snapshot: snap}, nil)
	p.mu.Unlock()

	for _, remove := range removals {
		remove()
	}
	return spDrainSubscribers(subscribers, fmt.Sprintf("Failed to publish remote service %s update", reg.serviceId))
}

// Use returns the local singleton implementation.
// service's dynamic type is *Service[T]; the result's dynamic type is T.
func (p *RemoteServiceProvider) Use(service any) (any, error) {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.assertActive(); err != nil {
		return nil, err
	}
	if err := p.assertRemotable(head); err != nil {
		return nil, err
	}
	if err := p.assertAllowed(head.id); err != nil {
		return nil, err
	}
	reg, ok := p.registrations.Get(head.id)
	if !ok || reg.mode != ServiceModeSingleton || reg.singleton == nil {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotFound,
			fmt.Sprintf("Remote service %s has no local provider", head.id),
		)
	}
	return reg.singleton.implementation, nil
}

// Spawn publishes one keyed instance. The returned function closes it.
func (p *RemoteServiceProvider) Spawn(service any, key string, implementation any) (func() error, error) {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, js.NewTypeError("Remote service instance key must not be empty")
	}

	p.mu.Lock()
	if err := p.assertActive(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if err := p.assertRemotable(head); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if err := p.assertAllowed(head.id); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	reg, err := p.registration(head.id, ServiceModeKeyed)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if reg.instances.Has(key) {
		p.mu.Unlock()
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceModeMismatch,
			fmt.Sprintf("Remote service %s already has a live instance with key %s", head.id, key),
		)
	}
	generation := reg.generations[key] + 1
	reg.generations[key] = generation
	address := &ServiceInstanceAddress{Key: key, Generation: generation}
	classified, err := spClassifyRemoteServiceImplementation(reg.serviceId, implementation)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	instance := p.createInstance(reg, classified, address)
	reg.instances.Set(key, instance)
	instSnap := p.snapshotInstance(instance)
	subscribers := p.queueEmitLocked(reg, ServiceProviderUpdateSpawned{Type: "spawned", Instance: instSnap}, nil)
	p.mu.Unlock()

	if err := spDrainSubscribers(subscribers, fmt.Sprintf("Failed to publish remote service %s update", reg.serviceId)); err != nil {
		// Deliver close cleanup? Matching TS: emit throws after spawn registered, still return close or err.
		// In TS spawn() throws synchronously after setting instance; here return err cleanly.
		return func() error { return nil }, err
	}

	var closeOnce sync.Mutex
	closed := false
	return func() error {
		closeOnce.Lock()
		if closed {
			closeOnce.Unlock()
			return nil
		}
		closed = true
		closeOnce.Unlock()

		p.mu.Lock()
		current, ok := reg.instances.Get(key)
		if !ok || current != instance {
			p.mu.Unlock()
			return nil
		}
		instance.active = false
		removals := slices.Clone(instance.removeMemberListeners)
		reg.instances.Delete(key)
		subs := p.queueEmitLocked(reg, ServiceProviderUpdateClosed{Type: "closed", Instance: *address}, nil)
		p.mu.Unlock()

		for _, remove := range removals {
			remove()
		}
		return spDrainSubscribers(subs, fmt.Sprintf("Failed to publish remote service %s update", reg.serviceId))
	}, nil
}

func (p *RemoteServiceProvider) Invoke(call ServiceCall, callContext Context) (value JsonValue, ok bool, err error) {
	p.mu.Lock()
	if err := p.assertActive(); err != nil {
		p.mu.Unlock()
		return nil, false, err
	}
	if err := p.assertAllowed(call.ServiceId); err != nil {
		p.mu.Unlock()
		return nil, false, err
	}
	reg, exists := p.registrations.Get(call.ServiceId)
	if !exists {
		p.mu.Unlock()
		return nil, false, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotFound,
			fmt.Sprintf("Unknown remote service %s", call.ServiceId),
		)
	}
	instance, err := p.resolveInstance(reg, call.Instance)
	if err != nil {
		p.mu.Unlock()
		return nil, false, err
	}
	member, hasMember := instance.members.Get(call.Member)
	if !hasMember {
		p.mu.Unlock()
		return nil, false, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceMemberNotFound,
			fmt.Sprintf("Unknown remote service member %s.%s", call.ServiceId, call.Member),
		)
	}
	mm, isMethod := member.(spMethodMember)
	if !isMethod {
		p.mu.Unlock()
		return nil, false, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceMemberMismatch,
			fmt.Sprintf("Remote service member %s.%s is not a method", call.ServiceId, call.Member),
		)
	}
	method := mm.method
	p.mu.Unlock()

	if callContext == nil {
		callContext = BackgroundContext
	}
	invokeArgs := make([]any, len(call.Args)+1)
	copy(invokeArgs, call.Args)
	invokeArgs[len(call.Args)] = callContext
	return method(invokeArgs...)
}

func (p *RemoteServiceProvider) Subscribe(serviceId string, mode ServiceMode, listener func(update ServiceProviderUpdate, context Context) error) (ServiceSubscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.assertActive(); err != nil {
		return nil, err
	}
	if err := p.assertAllowed(serviceId); err != nil {
		return nil, err
	}
	reg, err := p.registration(serviceId, mode)
	if err != nil {
		return nil, err
	}
	if reg.mode == ServiceModeSingleton && reg.singleton == nil {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotFound,
			fmt.Sprintf("Remote service %s has no provider", serviceId),
		)
	}
	subscriber := &spProviderSubscriber{
		listener:          listener,
		snapshotSequences: omap.NewMap[string, int](),
	}
	reg.subscribers.Add(subscriber)
	snapshot := p.snapshot(reg)
	spRecordSnapshotSequences(subscriber.snapshotSequences, snapshot.Instances)
	return &spSubscription{
		provider:     p,
		registration: reg,
		subscriber:   subscriber,
		snapshot:     snapshot,
	}, nil
}

func (p *RemoteServiceProvider) Dispose() error {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return nil
	}
	p.disposed = true

	var removals []func()
	type queuedEmit struct {
		subs []*spProviderSubscriber
	}
	var drains []queuedEmit
	var allSubscribers []*spProviderSubscriber

	for _, reg := range p.registrations.All() {
		if reg.singleton != nil {
			singleton := reg.singleton
			singleton.active = false
			removals = append(removals, singleton.removeMemberListeners...)
			reg.singleton = nil
			subs := p.queueEmitLocked(reg, ServiceProviderUpdateUnavailable{Type: "unavailable"}, nil)
			if len(subs) > 0 {
				drains = append(drains, queuedEmit{subs: subs})
			}
		}
		for key, instance := range reg.instances.All() {
			instance.active = false
			removals = append(removals, instance.removeMemberListeners...)
			reg.instances.Delete(key)
			if instance.address != nil {
				subs := p.queueEmitLocked(reg, ServiceProviderUpdateClosed{Type: "closed", Instance: *instance.address}, nil)
				if len(subs) > 0 {
					drains = append(drains, queuedEmit{subs: subs})
				}
			}
		}
		for subscriber := range reg.subscribers.All() {
			allSubscribers = append(allSubscribers, subscriber)
		}
		reg.subscribers.Clear()
	}
	p.registrations.Clear()
	p.mu.Unlock()

	for _, remove := range removals {
		remove()
	}

	var errs []any
	for _, item := range drains {
		for _, sub := range item.subs {
			errs = append(errs, spDrainSubscriber(sub)...)
		}
	}
	// Terminate/close subscribers only after active ones drain queued terminal updates
	// (unavailable/closed), matching TS synchronous emit before subscriber closure.
	for _, subscriber := range allSubscribers {
		subscriber.mu.Lock()
		if subscriber.active && !subscriber.draining {
			subscriber.closed = true
			subscriber.buffer = nil
		} else {
			subscriber.terminated = true
		}
		subscriber.mu.Unlock()
	}
	return spThrowCollectedErrors(errs, "Failed to dispose remote service provider")
}

func (p *RemoteServiceProvider) registration(serviceId string, mode ServiceMode) (*spServiceRegistration, error) {
	reg, ok := p.registrations.Get(serviceId)
	if !ok {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotFound,
			fmt.Sprintf("Unknown remote service %s", serviceId),
		)
	}
	if reg.mode != mode {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceModeMismatch,
			fmt.Sprintf("Remote service %s is %s, not %s", serviceId, reg.mode, mode),
		)
	}
	return reg, nil
}

func (p *RemoteServiceProvider) createInstance(registration *spServiceRegistration, classified spClassifiedRemoteServiceImplementation, address *ServiceInstanceAddress) *spProviderInstance {
	var addrCopy *ServiceInstanceAddress
	if address != nil {
		c := *address
		addrCopy = &c
	}
	instance := &spProviderInstance{
		address:        addrCopy,
		implementation: classified.implementation,
		members:        classified.members,
		active:         true,
	}
	for name, member := range classified.members.All() {
		sm, ok := member.(spStateMember)
		if !ok {
			continue
		}
		memberName := name
		remove := sm.state.Subscribe(func(ops OpList, sequence int, call Context) error {
			p.mu.Lock()
			if !instance.active {
				p.mu.Unlock()
				return nil
			}
			var instAddr *ServiceInstanceAddress
			if addrCopy != nil {
				c := *addrCopy
				instAddr = &c
			}
			upd := ServiceProviderUpdateState{
				Type:     "state",
				Instance: instAddr,
				Member:   memberName,
				Sequence: sequence,
				Ops:      ops,
			}
			subs := p.queueEmitLocked(registration, upd, call)
			p.mu.Unlock()
			return spDrainSubscribers(subs, fmt.Sprintf("Failed to publish remote service %s update", registration.serviceId))
		})
		instance.removeMemberListeners = append(instance.removeMemberListeners, remove)
	}
	return instance
}

func (p *RemoteServiceProvider) assertSingletonShape(registration *spServiceRegistration, replacement map[string]spServiceMemberKind) error {
	current := registration.singletonShape
	if current == nil || spSameServiceMemberShape(current, replacement) {
		return nil
	}
	return NewRemoteServiceError(
		RemoteServiceErrorCodeServiceMemberMismatch,
		fmt.Sprintf("Remote service %s replacement must preserve its member shape", registration.serviceId),
	)
}

func (p *RemoteServiceProvider) resolveInstance(registration *spServiceRegistration, address *ServiceInstanceAddress) (*spProviderInstance, error) {
	if registration.mode == ServiceModeSingleton {
		if address != nil {
			return nil, NewRemoteServiceError(
				RemoteServiceErrorCodeServiceModeMismatch,
				fmt.Sprintf("Remote service %s is singleton", registration.serviceId),
			)
		}
		if registration.singleton == nil {
			return nil, NewRemoteServiceError(
				RemoteServiceErrorCodeServiceNotFound,
				fmt.Sprintf("Remote service %s has no provider", registration.serviceId),
			)
		}
		return registration.singleton, nil
	}
	if address == nil {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceModeMismatch,
			fmt.Sprintf("Remote service %s is keyed", registration.serviceId),
		)
	}
	instance, ok := registration.instances.Get(address.Key)
	if !ok {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceInstanceNotFound,
			fmt.Sprintf("Remote service %s has no instance %s", registration.serviceId, address.Key),
		)
	}
	if instance.address == nil || instance.address.Generation != address.Generation {
		return nil, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceStaleInstance,
			fmt.Sprintf("Remote service %s instance %s is stale", registration.serviceId, address.Key),
		)
	}
	return instance, nil
}

func (p *RemoteServiceProvider) snapshot(registration *spServiceRegistration) ServiceSubscriptionSnapshot {
	var instances []ServiceInstanceSnapshot
	if registration.mode == ServiceModeSingleton {
		if registration.singleton != nil {
			instances = []ServiceInstanceSnapshot{p.snapshotInstance(registration.singleton)}
		} else {
			instances = []ServiceInstanceSnapshot{}
		}
	} else {
		live := registration.instances.Values()
		slices.SortFunc(live, func(left, right *spProviderInstance) int {
			return strings.Compare(left.address.Key, right.address.Key)
		})
		instances = make([]ServiceInstanceSnapshot, len(live))
		for i, instance := range live {
			instances[i] = p.snapshotInstance(instance)
		}
	}
	return ServiceSubscriptionSnapshot{
		ServiceId: registration.serviceId,
		Mode:      registration.mode,
		Instances: instances,
	}
}

func (p *RemoteServiceProvider) snapshotInstance(instance *spProviderInstance) ServiceInstanceSnapshot {
	members := make(ServiceMemberSnapshotList, 0, instance.members.Len())
	for name, member := range instance.members.All() {
		switch m := member.(type) {
		case spMethodMember:
			members = append(members, ServiceMemberSnapshotMethod{Name: name, Kind: "method"})
		case spStateMember:
			value, sequence := m.state.Snapshot()
			members = append(members, ServiceMemberSnapshotState{
				Name:     name,
				Kind:     "state",
				Sequence: sequence,
				Ops:      OpList{OpReplace{Value: value}},
			})
		}
	}
	var addr *ServiceInstanceAddress
	if instance.address != nil {
		c := *instance.address
		addr = &c
	}
	return ServiceInstanceSnapshot{
		Instance: addr,
		Members:  members,
	}
}

// queueEmitLocked appends update to every matching subscriber buffer while p.mu is held.
// Callers must unlock p.mu and then call spDrainSubscribers on the returned slice.
func (p *RemoteServiceProvider) queueEmitLocked(registration *spServiceRegistration, update ServiceProviderUpdate, context Context) []*spProviderSubscriber {
	if registration.subscribers.Len() == 0 {
		return nil
	}
	deliveryContext := context
	if deliveryContext == nil {
		deliveryContext = ServiceDeliveryContext()
	}
	subscribers := registration.subscribers.Values()
	for _, subscriber := range subscribers {
		subscriber.mu.Lock()
		if subscriber.closed || spUpdateCoveredBySnapshot(subscriber.snapshotSequences, update) {
			subscriber.mu.Unlock()
			continue
		}
		if len(subscriber.buffer) == 100 {
			snapshot := p.snapshot(registration)
			subscriber.buffer = nil
			subscriber.snapshotSequences.Clear()
			spRecordSnapshotSequences(subscriber.snapshotSequences, snapshot.Instances)
			subscriber.buffer = append(subscriber.buffer, spBufferedUpdate{
				update:  ServiceProviderUpdateReset{Type: "reset", Snapshot: snapshot},
				context: deliveryContext,
			})
		} else {
			subscriber.buffer = append(subscriber.buffer, spBufferedUpdate{
				update:  update,
				context: deliveryContext,
			})
		}
		subscriber.mu.Unlock()
	}
	return subscribers
}

func (p *RemoteServiceProvider) emit(registration *spServiceRegistration, update ServiceProviderUpdate, context Context) error {
	p.mu.Lock()
	subscribers := p.queueEmitLocked(registration, update, context)
	p.mu.Unlock()
	return spDrainSubscribers(subscribers, fmt.Sprintf("Failed to publish remote service %s update", registration.serviceId))
}

func (p *RemoteServiceProvider) assertRemotable(service spServiceHead) error {
	if service.local != nil && *service.local {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotAllowed,
			fmt.Sprintf("Service %s is process-local", service.id),
		)
	}
	return nil
}

func (p *RemoteServiceProvider) assertAllowed(serviceId string) error {
	if !p.registrations.Has(serviceId) {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotAllowed,
			fmt.Sprintf("Remote service %s is not allowlisted", serviceId),
		)
	}
	return nil
}

func (p *RemoteServiceProvider) assertActive() error {
	if p.disposed {
		return errors.New("Remote service provider is disposed")
	}
	return nil
}

type spSubscription struct {
	provider     *RemoteServiceProvider
	registration *spServiceRegistration
	subscriber   *spProviderSubscriber
	snapshot     ServiceSubscriptionSnapshot
}

func (s *spSubscription) Snapshot() ServiceSubscriptionSnapshot {
	return s.snapshot
}

func (s *spSubscription) Activate() error {
	s.subscriber.mu.Lock()
	if s.subscriber.closed || s.subscriber.active {
		s.subscriber.mu.Unlock()
		return nil
	}
	s.subscriber.active = true
	s.subscriber.mu.Unlock()
	errs := spDrainSubscriber(s.subscriber)
	return spThrowCollectedErrors(errs, "Failed to activate remote service subscription")
}

func (s *spSubscription) Close(context Context) error {
	s.subscriber.mu.Lock()
	if s.subscriber.closed {
		s.subscriber.mu.Unlock()
		return nil
	}
	s.subscriber.closed = true
	s.subscriber.buffer = nil
	s.subscriber.mu.Unlock()

	s.provider.mu.Lock()
	s.registration.subscribers.Delete(s.subscriber)
	s.provider.mu.Unlock()
	return nil
}

func spDrainSubscribers(subscribers []*spProviderSubscriber, message string) error {
	if len(subscribers) == 0 {
		return nil
	}
	var errs []any
	for _, sub := range subscribers {
		errs = append(errs, spDrainSubscriber(sub)...)
	}
	return spThrowCollectedErrors(errs, message)
}

func spDrainSubscriber(subscriber *spProviderSubscriber) []any {
	subscriber.mu.Lock()
	if !subscriber.active || subscriber.closed || subscriber.draining {
		subscriber.mu.Unlock()
		return nil
	}
	subscriber.draining = true
	listener := subscriber.listener
	subscriber.mu.Unlock()

	var errs []any
	defer func() {
		subscriber.mu.Lock()
		subscriber.draining = false
		if subscriber.terminated {
			subscriber.closed = true
		}
		subscriber.mu.Unlock()
	}()

	for {
		subscriber.mu.Lock()
		if subscriber.closed || len(subscriber.buffer) == 0 {
			subscriber.mu.Unlock()
			return errs
		}
		entry := subscriber.buffer[0]
		copy(subscriber.buffer, subscriber.buffer[1:])
		subscriber.buffer[len(subscriber.buffer)-1] = spBufferedUpdate{}
		subscriber.buffer = subscriber.buffer[:len(subscriber.buffer)-1]
		subscriber.mu.Unlock()

		if listener != nil {
			if err := listener(entry.update, entry.context); err != nil {
				errs = append(errs, err)
			}
		}
	}
}

func spRecordSnapshotSequences(sequences *omap.Map[string, int], instances []ServiceInstanceSnapshot) {
	for _, instance := range instances {
		for _, member := range instance.Members {
			if sm, ok := member.(ServiceMemberSnapshotState); ok {
				sequences.Set(spStateMemberKey(instance.Instance, sm.Name), sm.Sequence)
			} else if smp, ok := member.(*ServiceMemberSnapshotState); ok && smp != nil {
				sequences.Set(spStateMemberKey(instance.Instance, smp.Name), smp.Sequence)
			}
		}
	}
}

func spUpdateCoveredBySnapshot(sequences *omap.Map[string, int], update ServiceProviderUpdate) bool {
	switch u := update.(type) {
	case ServiceProviderUpdateState:
		key := spStateMemberKey(u.Instance, u.Member)
		sequence, ok := sequences.Get(key)
		if !ok {
			return false
		}
		if u.Sequence <= sequence {
			return true
		}
		sequences.Delete(key)
		return false
	case ServiceProviderUpdateReset:
		sequences.Clear()
		spRecordSnapshotSequences(sequences, u.Snapshot.Instances)
		return false
	case ServiceProviderUpdateReplaced:
		sequences.Clear()
		spRecordSnapshotSequences(sequences, []ServiceInstanceSnapshot{u.Snapshot})
		return false
	case ServiceProviderUpdateSpawned:
		spRecordSnapshotSequences(sequences, []ServiceInstanceSnapshot{u.Instance})
		return false
	case ServiceProviderUpdateUnavailable:
		sequences.Clear()
		return false
	case ServiceProviderUpdateClosed:
		prefixBytes, _ := jsonx.Marshal([]any{u.Instance.Key, float64(u.Instance.Generation)})
		prefix := string(prefixBytes[:len(prefixBytes)-1]) + ","
		var toDelete []string
		for key := range sequences.All() {
			if strings.HasPrefix(key, prefix) {
				toDelete = append(toDelete, key)
			}
		}
		for _, key := range toDelete {
			sequences.Delete(key)
		}
		return false
	default:
		return false
	}
}

func spStateMemberKey(instance *ServiceInstanceAddress, member string) string {
	var b []byte
	if instance == nil {
		b, _ = jsonx.Marshal([]any{member})
	} else {
		b, _ = jsonx.Marshal([]any{instance.Key, float64(instance.Generation), member})
	}
	return string(b)
}

// CreateRemoteServiceEndpoint returns an endpoint that owns one consumer's subscriptions on provider.
func CreateRemoteServiceEndpoint(provider *RemoteServiceProvider) RemoteServiceEndpoint {
	return &spRemoteServiceEndpoint{
		provider:      provider,
		subscriptions: omap.NewMap[string, ServiceSubscription](),
	}
}

type spRemoteServiceEndpoint struct {
	mu            sync.Mutex
	provider      *RemoteServiceProvider
	subscriptions *omap.Map[string, ServiceSubscription]
	disposed      bool
}

func (e *spRemoteServiceEndpoint) Invoke(call ServiceCall, publish ServiceUpdatePublisher, callContext Context) (value JsonValue, ok bool, err error) {
	e.mu.Lock()
	if e.disposed {
		e.mu.Unlock()
		return nil, false, errors.New("Remote service endpoint is disposed")
	}
	e.mu.Unlock()

	if callContext == nil {
		callContext = BackgroundContext
	}

	control := DecodeServiceControlCall(call)
	switch ctrl := control.(type) {
	case ServiceControlCallCatalogue:
		return e.provider.Catalogue(), true, nil
	case ServiceControlCallSubscribe:
		e.mu.Lock()
		if e.disposed {
			e.mu.Unlock()
			return nil, false, errors.New("Remote service endpoint is disposed")
		}
		if e.subscriptions.Has(ctrl.SubscriptionId) {
			e.mu.Unlock()
			return nil, false, errors.New("Service subscription ID is already active")
		}
		e.mu.Unlock()

		sub, err := e.provider.Subscribe(ctrl.ServiceId, ctrl.Mode, func(update ServiceProviderUpdate, updateContext Context) error {
			if publish != nil {
				_ = publish(ctrl.SubscriptionId, update, updateContext)
			}
			return nil
		})
		if err != nil {
			return nil, false, err
		}

		e.mu.Lock()
		if e.disposed {
			e.mu.Unlock()
			_ = sub.Close(callContext)
			return nil, false, errors.New("Remote service endpoint is disposed")
		}
		if e.subscriptions.Has(ctrl.SubscriptionId) {
			e.mu.Unlock()
			_ = sub.Close(callContext)
			return nil, false, errors.New("Service subscription ID is already active")
		}
		e.subscriptions.Set(ctrl.SubscriptionId, sub)
		e.mu.Unlock()

		if err := sub.Activate(); err != nil {
			return nil, false, err
		}
		return sub.Snapshot(), true, nil
	case ServiceControlCallUnsubscribe:
		e.mu.Lock()
		if e.disposed {
			e.mu.Unlock()
			return nil, false, errors.New("Remote service endpoint is disposed")
		}
		sub, exists := e.subscriptions.Get(ctrl.SubscriptionId)
		if !exists {
			e.mu.Unlock()
			return nil, false, errors.New("Service subscription was not found")
		}
		e.subscriptions.Delete(ctrl.SubscriptionId)
		e.mu.Unlock()

		if err := sub.Close(callContext); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	default:
		return e.provider.Invoke(call, callContext)
	}
}

func (e *spRemoteServiceEndpoint) Dispose() {
	e.mu.Lock()
	if e.disposed {
		e.mu.Unlock()
		return
	}
	e.disposed = true
	subs := e.subscriptions.Values()
	e.subscriptions.Clear()
	e.mu.Unlock()

	for _, sub := range subs {
		_ = sub.Close(nil)
	}
}

// ValidateRemoteServiceImplementation rejects an implementation that cannot be published remotely.
func ValidateRemoteServiceImplementation(serviceId string, implementation any) error {
	_, err := spClassifyRemoteServiceImplementation(serviceId, implementation)
	return err
}

func spClassifyRemoteServiceImplementation(serviceId string, implementation any) (spClassifiedRemoteServiceImplementation, error) {
	if implementation == nil {
		return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
	}

	rawMembers := map[string]any{}
	switch obj := implementation.(type) {
	case *jsonx.Object:
		if obj == nil {
			return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
		}
		for _, k := range obj.Keys() {
			v, _ := obj.Get(k)
			rawMembers[k] = v
		}
	case map[string]any:
		if obj == nil {
			return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
		}
		for k, v := range obj {
			rawMembers[k] = v
		}
	default:
		if GetReplicatedStateInternals(implementation) != nil {
			return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
		}
		rv := reflect.ValueOf(implementation)
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
		}
		if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
			if rv.IsNil() {
				return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
			}
			iter := rv.MapRange()
			for iter.Next() {
				rawMembers[iter.Key().String()] = iter.Value().Interface()
			}
		} else if rv.Kind() == reflect.Struct || (rv.Kind() == reflect.Ptr && rv.Elem().Kind() == reflect.Struct) {
			structVal := rv
			if structVal.Kind() == reflect.Ptr {
				structVal = structVal.Elem()
			}
			st := structVal.Type()
			for i := 0; i < st.NumField(); i++ {
				field := st.Field(i)
				if !field.IsExported() {
					continue
				}
				name := spStructMemberName(field)
				if name == "" {
					continue
				}
				rawMembers[name] = structVal.Field(i).Interface()
			}
			rt := rv.Type()
			for i := 0; i < rt.NumMethod(); i++ {
				m := rt.Method(i)
				if !m.IsExported() {
					continue
				}
				if _, exists := rawMembers[m.Name]; !exists {
					rawMembers[m.Name] = rv.Method(i).Interface()
				}
			}
		} else {
			return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s implementation must be an object", serviceId))
		}
	}

	names := make([]string, 0, len(rawMembers))
	for name := range rawMembers {
		names = append(names, name)
	}
	slices.Sort(names)

	members := omap.NewMap[string, spInstanceMember]()
	for _, name := range names {
		val := rawMembers[name]
		if state := GetReplicatedStateInternals(val); state != nil {
			members.Set(name, spStateMember{kind: spServiceMemberKindState, state: state})
			continue
		}
		if fn, ok := spAsRemoteMethod(val); ok {
			members.Set(name, spMethodMember{kind: spServiceMemberKindMethod, method: fn})
			continue
		}
		return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service member %s.%s is not remotely exposable", serviceId, name))
	}
	if members.Len() == 0 {
		return spClassifiedRemoteServiceImplementation{}, js.NewTypeError(fmt.Sprintf("Remote service %s has no members", serviceId))
	}
	return spClassifiedRemoteServiceImplementation{
		implementation: implementation,
		members:        members,
	}, nil
}

func spStructMemberName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	if tag != "" {
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "" {
			return tag
		}
	}
	return field.Name
}

func spAsRemoteMethod(val any) (spRemoteMethod, bool) {
	if val == nil {
		return nil, false
	}
	if m, ok := val.(spRemoteMethod); ok && m != nil {
		return m, true
	}
	rv := reflect.ValueOf(val)
	if !rv.IsValid() || rv.Kind() != reflect.Func || rv.IsNil() {
		return nil, false
	}
	ft := rv.Type()
	errType := reflect.TypeFor[error]()
	return func(args ...any) (JsonValue, bool, error) {
		inVals, err := spBindCallArgs(ft, args)
		if err != nil {
			return nil, false, err
		}
		outs := rv.Call(inVals)
		nOut := len(outs)
		if nOut == 0 {
			return nil, false, nil
		}
		var callErr error
		lastIdx := nOut - 1
		if ft.Out(lastIdx).Implements(errType) {
			if !outs[lastIdx].IsNil() {
				callErr = outs[lastIdx].Interface().(error)
			}
			nOut--
		}
		if callErr != nil {
			return nil, false, callErr
		}
		switch nOut {
		case 0:
			return nil, false, nil
		case 1:
			return outs[0].Interface(), true, nil
		case 2:
			if outs[1].Kind() == reflect.Bool {
				return outs[0].Interface(), outs[1].Bool(), nil
			}
			return outs[0].Interface(), true, nil
		default:
			return outs[0].Interface(), true, nil
		}
	}, true
}

func spBindCallArgs(ft reflect.Type, args []any) ([]reflect.Value, error) {
	numIn := ft.NumIn()
	if ft.IsVariadic() {
		fixedCount := numIn - 1
		if len(args) < fixedCount {
			return nil, js.NewTypeError(fmt.Sprintf("Remote method requires at least %d argument(s), got %d", fixedCount, len(args)))
		}
		out := make([]reflect.Value, len(args))
		for i := 0; i < fixedCount; i++ {
			v, err := spConvertArg(args[i], ft.In(i), i)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		elemType := ft.In(fixedCount).Elem()
		for i := fixedCount; i < len(args); i++ {
			v, err := spConvertArg(args[i], elemType, i)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	if len(args) < numIn {
		return nil, js.NewTypeError(fmt.Sprintf("Remote method requires %d argument(s), got %d", numIn, len(args)))
	}
	out := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		v, err := spConvertArg(args[i], ft.In(i), i)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func spConvertArg(val any, target reflect.Type, idx int) (reflect.Value, error) {
	if val == nil {
		switch target.Kind() {
		case reflect.Interface, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
			return reflect.Zero(target), nil
		default:
			return reflect.Value{}, js.NewTypeError(fmt.Sprintf("Remote method argument %d cannot be nil for %s", idx, target))
		}
	}
	rv := reflect.ValueOf(val)
	if rv.Type().AssignableTo(target) {
		return rv, nil
	}
	if f, ok := val.(float64); ok && (target.Kind() == reflect.Int || target.Kind() == reflect.Int64) {
		if !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f {
			return reflect.ValueOf(int(f)).Convert(target), nil
		}
	}
	if rv.Type().ConvertibleTo(target) && (rv.Kind() != reflect.Float64 || target.Kind() != reflect.String) {
		return rv.Convert(target), nil
	}
	return reflect.Value{}, js.NewTypeError(fmt.Sprintf("Remote method argument %d has type %s, expected %s", idx, rv.Type(), target))
}

func spExtractServiceHead(service any) (spServiceHead, error) {
	if service == nil {
		return spServiceHead{}, js.NewTypeError("Remote service definition must not be nil")
	}
	if head, ok := service.(spServiceHead); ok {
		if head.id == "" {
			return spServiceHead{}, js.NewTypeError("Remote service ID must not be empty")
		}
		return head, nil
	}
	rv := reflect.ValueOf(service)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return spServiceHead{}, js.NewTypeError("Remote service definition must not be nil")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return spServiceHead{}, js.NewTypeError("Remote service definition must be a struct")
	}
	idField := rv.FieldByName("Id")
	if !idField.IsValid() || idField.Kind() != reflect.String {
		return spServiceHead{}, js.NewTypeError("Remote service definition missing Id")
	}
	id := idField.String()
	if id == "" {
		return spServiceHead{}, js.NewTypeError("Remote service ID must not be empty")
	}
	var localPtr *bool
	localField := rv.FieldByName("Local")
	if localField.IsValid() {
		switch localField.Kind() {
		case reflect.Bool:
			b := localField.Bool()
			localPtr = &b
		case reflect.Ptr:
			if localField.Type().Elem().Kind() == reflect.Bool && !localField.IsNil() {
				b := localField.Elem().Bool()
				localPtr = &b
			}
		}
	}
	return spServiceHead{id: id, local: localPtr}, nil
}

func spThrowCollectedErrors(errs []any, message string) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return ssToError(errs[0])
	}
	return ssJoinErrors(message, errs)
}

func spServiceMemberShape(members *omap.Map[string, spInstanceMember]) map[string]spServiceMemberKind {
	shape := make(map[string]spServiceMemberKind, members.Len())
	for name, member := range members.All() {
		switch member.(type) {
		case spMethodMember:
			shape[name] = spServiceMemberKindMethod
		case spStateMember:
			shape[name] = spServiceMemberKindState
		}
	}
	return shape
}

func spSameServiceMemberShape(left map[string]spServiceMemberKind, right map[string]spServiceMemberKind) bool {
	if len(left) != len(right) {
		return false
	}
	for name, kind := range left {
		if rKind, ok := right[name]; !ok || rKind != kind {
			return false
		}
	}
	return true
}
