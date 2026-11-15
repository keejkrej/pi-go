// Ported from packages/chord/src/services/consumer.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

type scErrorReporter func(error)

type scServiceMemberKind string

const (
	scServiceMemberKindMethod scServiceMemberKind = "method"
	scServiceMemberKindState  scServiceMemberKind = "state"
)

type scMemberSlot struct {
	mu           sync.Mutex
	serviceId    string
	member       string
	invoke       func(args []JsonValue, call Context) (JsonValue, bool, error)
	state        *ReplicatedStateReplica[JsonValue]
	isActive     func() bool
	assertAccess func()
	value        any
	kind         *scServiceMemberKind
	expectedKind *scServiceMemberKind
}

func scNewMemberSlot(
	serviceId string,
	member string,
	invoke func(args []JsonValue, call Context) (JsonValue, bool, error),
	isActive func() bool,
	assertAccess func(),
	reportError scErrorReporter,
) *scMemberSlot {
	m := &scMemberSlot{
		serviceId:    serviceId,
		member:       member,
		invoke:       invoke,
		state:        NewReplicatedStateReplica[JsonValue](reportError),
		isActive:     isActive,
		assertAccess: assertAccess,
	}
	m.value = m
	return m
}

// Get reads a member property ("value" or "subscribe") with access and kind checks.
func (m *scMemberSlot) Get(property any) (any, error) {
	propStr, ok := property.(string)
	if !ok {
		return nil, nil
	}
	return m.getProperty(propStr)
}

// Invoke calls this member as a remote service method with args (ending in Context).
func (m *scMemberSlot) Invoke(args ...any) (any, error) {
	res, _, err := m.call(args)
	return res, err
}

// StateValue reads the current replicated state value, reporting whether it is hydrated.
func (m *scMemberSlot) StateValue() (JsonValue, bool, error) {
	return m.getValueOk()
}

// Subscribe attaches a listener to this replicated state member.
func (m *scMemberSlot) Subscribe(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) (func(), error) {
	return m.subscribe(listener)
}

func (m *scMemberSlot) getProperty(property string) (any, error) {
	switch property {
	case "value":
		val, ok, err := m.getValueOk()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return val, nil
	case "subscribe":
		return func(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) (func(), error) {
			return m.subscribe(listener)
		}, nil
	case "then":
		return nil, nil
	default:
		return nil, nil
	}
}

func (m *scMemberSlot) getValueOk() (JsonValue, bool, error) {
	if err := shCallAssertAccess(m.assertAccess); err != nil {
		return nil, false, err
	}
	if err := m.expect(scServiceMemberKindState); err != nil {
		return nil, false, err
	}
	val, ok := m.state.Value()
	return val, ok, nil
}

func (m *scMemberSlot) SetDescription(kind scServiceMemberKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kind != nil && *m.kind != kind {
		return fmt.Errorf("Remote service member %s.%s changed kind", m.serviceId, m.member)
	}
	k := kind
	m.kind = &k
	if m.expectedKind != nil && *m.expectedKind != kind {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceMemberMismatch,
			fmt.Sprintf("Remote service member %s.%s is %s, not %s", m.serviceId, m.member, kind, *m.expectedKind),
		)
	}
	return nil
}

func (m *scMemberSlot) Hydrate(sequence int, ops []Op, context Context) error {
	if err := m.SetDescription(scServiceMemberKindState); err != nil {
		return err
	}
	return m.state.Hydrate(sequence, ops, context)
}

func (m *scMemberSlot) Update(sequence int, ops []Op, context Context) error {
	if err := m.SetDescription(scServiceMemberKindState); err != nil {
		return err
	}
	return m.state.Update(sequence, ops, context)
}

func (m *scMemberSlot) Clear() {
	m.state.Clear()
}

func (m *scMemberSlot) subscribe(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) (func(), error) {
	if err := shCallAssertAccess(m.assertAccess); err != nil {
		return nil, err
	}
	if listener == nil {
		return nil, js.NewTypeError("Replicated state subscription listener must be a function")
	}
	if err := m.expect(scServiceMemberKindState); err != nil {
		return nil, err
	}
	return m.state.Subscribe(listener), nil
}

func (m *scMemberSlot) expect(kind scServiceMemberKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.expectedKind != nil && *m.expectedKind != kind {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceMemberMismatch,
			fmt.Sprintf("Remote service member %s.%s was used as two different kinds", m.serviceId, m.member),
		)
	}
	k := kind
	m.expectedKind = &k
	if m.kind != nil && *m.kind != kind {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceMemberMismatch,
			fmt.Sprintf("Remote service member %s.%s is %s, not %s", m.serviceId, m.member, *m.kind, kind),
		)
	}
	return nil
}

func (m *scMemberSlot) call(args []any) (JsonValue, bool, error) {
	if err := shCallAssertAccess(m.assertAccess); err != nil {
		return nil, false, err
	}
	if err := m.expect(scServiceMemberKindMethod); err != nil {
		return nil, false, err
	}
	if !m.isActive() {
		return nil, false, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceStaleInstance,
			fmt.Sprintf("Remote service %s binding is closed", m.serviceId),
		)
	}
	if len(args) == 0 || !scIsContext(args[len(args)-1]) {
		return nil, false, NewRemoteServiceError(
			RemoteServiceErrorCodeServiceInvalidValue,
			fmt.Sprintf("Remote service method %s.%s requires a trailing Context", m.serviceId, m.member),
		)
	}
	ctx := args[len(args)-1].(Context)
	businessArgs := make([]JsonValue, len(args)-1)
	for i := 0; i < len(args)-1; i++ {
		businessArgs[i] = args[i]
	}
	return m.invoke(businessArgs, ctx)
}

type scServiceFacade struct {
	mu           sync.Mutex
	serviceId    string
	address      *ServiceInstanceAddress
	transport    RemoteServiceTransport
	reportError  scErrorReporter
	slots        *omap.Map[string, *scMemberSlot]
	descriptions *omap.Map[string, scServiceMemberKind]
	isActive     func() bool
	assertAccess func()
	proxy        any
}

func scNewServiceFacade(
	serviceId string,
	address *ServiceInstanceAddress,
	transport RemoteServiceTransport,
	isActive func() bool,
	assertAccess func(),
	reportError scErrorReporter,
) *scServiceFacade {
	var addrCopy *ServiceInstanceAddress
	if address != nil {
		c := *address
		addrCopy = &c
	}
	f := &scServiceFacade{
		serviceId:    serviceId,
		address:      addrCopy,
		transport:    transport,
		reportError:  reportError,
		slots:        omap.NewMap[string, *scMemberSlot](),
		descriptions: omap.NewMap[string, scServiceMemberKind](),
		isActive:     isActive,
		assertAccess: assertAccess,
	}
	f.proxy = f
	return f
}

// Get returns the member slot for a remote service member.
func (f *scServiceFacade) Get(property any) (any, error) {
	propStr, ok := property.(string)
	if !ok {
		return nil, nil
	}
	slot, err := f.slot(propStr)
	if err != nil {
		return nil, err
	}
	return slot.value, nil
}

// Call invokes a remote service method member by name.
func (f *scServiceFacade) Call(member string, args ...any) (any, error) {
	slot, err := f.slot(member)
	if err != nil {
		return nil, err
	}
	res, _, err := slot.call(args)
	return res, err
}

func (f *scServiceFacade) Install(snapshot ServiceInstanceSnapshot, call Context) error {
	if !scSameAddress(snapshot.Instance, f.address) {
		return errors.New("Remote service snapshot has the wrong address")
	}
	members, err := scValidateMembers(snapshot.Members)
	if err != nil {
		return err
	}

	type memberAction struct {
		snapshot ServiceMemberSnapshot
		slot     *scMemberSlot
	}
	var actions []memberAction

	f.mu.Lock()
	for name := range f.slots.All() {
		if !members.Has(name) {
			f.mu.Unlock()
			return NewRemoteServiceError(
				RemoteServiceErrorCodeServiceMemberNotFound,
				fmt.Sprintf("Unknown remote service member %s.%s", f.serviceId, name),
			)
		}
	}
	f.descriptions.Clear()
	for name, member := range members.All() {
		f.descriptions.Set(name, scMemberKind(member))
	}
	for name, member := range members.All() {
		slot, hasSlot := f.slots.Get(name)
		if _, isState := member.(ServiceMemberSnapshotState); isState {
			if !hasSlot {
				slot = f.createSlotLocked(name)
			}
			actions = append(actions, memberAction{snapshot: member, slot: slot})
		} else if hasSlot {
			actions = append(actions, memberAction{snapshot: member, slot: slot})
		}
	}
	f.mu.Unlock()

	for _, action := range actions {
		switch ms := action.snapshot.(type) {
		case ServiceMemberSnapshotState:
			if err := action.slot.Hydrate(ms.Sequence, ms.Ops, call); err != nil {
				return err
			}
		case ServiceMemberSnapshotMethod:
			if err := action.slot.SetDescription(scServiceMemberKindMethod); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *scServiceFacade) Update(member string, sequence int, ops []Op, context Context) error {
	f.mu.Lock()
	kind, ok := f.descriptions.Get(member)
	if !ok || kind != scServiceMemberKindState {
		serviceId := f.serviceId
		f.mu.Unlock()
		return fmt.Errorf("Remote service update targets non-state member %s.%s", serviceId, member)
	}
	slot, hasSlot := f.slots.Get(member)
	if !hasSlot {
		slot = f.createSlotLocked(member)
	}
	f.mu.Unlock()
	return slot.Update(sequence, ops, context)
}

func (f *scServiceFacade) Clear() {
	f.mu.Lock()
	slots := make([]*scMemberSlot, 0, f.slots.Len())
	for _, slot := range f.slots.All() {
		slots = append(slots, slot)
	}
	f.mu.Unlock()
	for _, slot := range slots {
		slot.Clear()
	}
}

func (f *scServiceFacade) slot(member string) (*scMemberSlot, error) {
	f.mu.Lock()
	if slot, ok := f.slots.Get(member); ok {
		f.mu.Unlock()
		return slot, nil
	}
	slot := f.createSlotLocked(member)
	kind, hasKind := f.descriptions.Get(member)
	f.mu.Unlock()
	if hasKind {
		if err := slot.SetDescription(kind); err != nil {
			return nil, err
		}
	}
	return slot, nil
}

func (f *scServiceFacade) createSlotLocked(member string) *scMemberSlot {
	var addrCopy *ServiceInstanceAddress
	if f.address != nil {
		c := *f.address
		addrCopy = &c
	}
	serviceId := f.serviceId
	transport := f.transport
	slot := scNewMemberSlot(
		serviceId,
		member,
		func(args []JsonValue, context Context) (JsonValue, bool, error) {
			return transport.Invoke(ServiceCall{
				ServiceId: serviceId,
				Instance:  addrCopy,
				Member:    member,
				Args:      args,
			}, context)
		},
		f.isActive,
		f.assertAccess,
		f.reportError,
	)
	f.slots.Set(member, slot)
	return slot
}

type scSingletonBinding struct {
	facade       *scServiceFacade
	subscription ServiceSubscription
	// starting is the in-flight start. Nil means there is no in-flight start.
	starting func() error
	active   bool
	revision int
}

type scKeyedInstance struct {
	key        string
	generation int
	service    any
	deactivate func()
	facade     *scServiceFacade
}

func (e *scKeyedInstance) Key() string { return e.key }

func (e *scKeyedInstance) Generation() int { return e.generation }

func (e *scKeyedInstance) Service() any { return e.service }

func (e *scKeyedInstance) Deactivate() { e.deactivate() }

type scKeyedBinding struct {
	mu           sync.Mutex
	serviceId    string
	transport    RemoteServiceTransport
	reportError  scErrorReporter
	assertAccess func()
	onEmpty      func()
	instances    *InstanceDirectory[*scKeyedInstance]
	subscription ServiceSubscription
	// starting is the in-flight start. Nil means there is no in-flight start.
	starting func() error
	closed   bool
	bound    bool
	revision int
}

func scNewKeyedBinding(
	service any,
	transport RemoteServiceTransport,
	reportError scErrorReporter,
	assertAccess func(),
	onEmpty func(),
	bound bool,
) *scKeyedBinding {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}
	return &scKeyedBinding{
		serviceId:    head.id,
		transport:    transport,
		reportError:  reportError,
		assertAccess: assertAccess,
		onEmpty:      onEmpty,
		instances: NewInstanceDirectory[*scKeyedInstance](&InstanceDirectoryOptions{
			Ready:   false,
			OnError: reportError,
		}),
		bound: bound,
	}
}

func (b *scKeyedBinding) Observe(handler func(service any, context Context) error) (func(), error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("Remote keyed service binding is closed")
	}
	serviceId := b.serviceId
	b.mu.Unlock()

	var stopped atomic.Bool
	stop, err := b.instances.Observe(func(service any, call Context) error {
		slot := NewServiceSlot(serviceId, true)
		slot.Bind(service)
		view := slot.View(func() {
			if b.assertAccess != nil {
				b.assertAccess()
			}
			var aborted bool
			if call != nil {
				if sig := call.AbortSignal(); sig != nil && sig.Err() != nil {
					aborted = true
				}
			}
			if stopped.Load() || aborted {
				panic(NewRemoteServiceError(
					RemoteServiceErrorCodeServiceStaleInstance,
					fmt.Sprintf("Remote service %s observation is closed", serviceId),
				))
			}
		})
		if handler == nil {
			return nil
		}
		return handler(view, call)
	})
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		stop()
		return nil, errors.New("Remote keyed service binding is closed")
	}
	if b.bound && b.starting == nil {
		revision := b.revision
		starting := scStartFuture(func() error {
			return b.start(revision)
		})
		b.starting = starting
		go func() {
			if startErr := starting(); startErr != nil {
				b.mu.Lock()
				report := !b.closed && b.revision == revision && b.bound && b.reportError != nil
				reportFn := b.reportError
				b.mu.Unlock()
				if report {
					reportFn(startErr)
				}
			}
		}()
	}
	b.mu.Unlock()

	return func() {
		if !stopped.CompareAndSwap(false, true) {
			return
		}
		stop()
		if b.instances.ObserverCount() == 0 && b.onEmpty != nil {
			b.onEmpty()
		}
	}, nil
}

func (b *scKeyedBinding) Rebind(bound bool, context Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.bound = bound
	b.revision++
	revision := b.revision
	b.mu.Unlock()

	if err := b.reset(context, false); err != nil {
		return err
	}

	b.mu.Lock()
	if b.closed || b.revision != revision || b.bound != bound {
		b.mu.Unlock()
		return nil
	}
	if bound && b.instances.ObserverCount() > 0 {
		starting := scStartFuture(func() error {
			return b.start(revision)
		})
		b.starting = starting
		b.mu.Unlock()
		return starting()
	}
	b.mu.Unlock()
	return nil
}

func (b *scKeyedBinding) Ready() error {
	b.mu.Lock()
	starting := b.starting
	b.mu.Unlock()
	if starting == nil {
		return nil
	}
	return starting()
}

func (b *scKeyedBinding) Close(context Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.revision++
	b.mu.Unlock()

	err := b.reset(context, true)
	_ = b.instances.Dispose()
	return err
}

func (b *scKeyedBinding) reset(context Context, waitForStarting bool) error {
	b.mu.Lock()
	starting := b.starting
	b.starting = nil
	subscription := b.subscription
	b.subscription = nil
	b.mu.Unlock()

	b.instances.Reset()

	var wg sync.WaitGroup
	var closeErr error
	if waitForStarting && starting != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = starting()
		}()
	}
	if subscription != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeErr = subscription.Close(context)
		}()
	}
	wg.Wait()
	return closeErr
}

func (b *scKeyedBinding) start(revision int) error {
	subscription, err := b.transport.Subscribe(
		b.serviceId,
		ServiceModeKeyed,
		func(update ServiceProviderUpdate, ctx Context) {
			b.mu.Lock()
			match := b.revision == revision
			b.mu.Unlock()
			if match {
				b.update(update, ctx)
			}
		},
		BackgroundContext,
	)
	if err != nil {
		return err
	}

	b.mu.Lock()
	if b.closed || !b.bound || b.revision != revision {
		b.mu.Unlock()
		_ = subscription.Close(BackgroundContext)
		return nil
	}
	b.subscription = subscription
	serviceId := b.serviceId
	b.mu.Unlock()

	snap := subscription.Snapshot()
	if snap.Mode != ServiceModeKeyed || snap.ServiceId != serviceId {
		return fmt.Errorf("Remote service %s returned the wrong keyed snapshot", serviceId)
	}
	for _, instSnap := range snap.Instances {
		b.mu.Lock()
		stale := b.closed || !b.bound || b.revision != revision
		b.mu.Unlock()
		if stale {
			return nil
		}
		if err := b.spawn(instSnap, ServiceDeliveryContext()); err != nil {
			return err
		}
	}
	b.mu.Lock()
	stale := b.closed || !b.bound || b.revision != revision
	b.mu.Unlock()
	if stale {
		return nil
	}
	if err := subscription.Activate(); err != nil {
		return err
	}
	b.mu.Lock()
	stale = b.closed || !b.bound || b.revision != revision
	b.mu.Unlock()
	if stale {
		return nil
	}
	return b.instances.Ready()
}

func (b *scKeyedBinding) update(update ServiceProviderUpdate, context Context) {
	b.mu.Lock()
	closed := b.closed
	serviceId := b.serviceId
	reportError := b.reportError
	b.mu.Unlock()
	if closed {
		return
	}

	report := func(err error) {
		if err != nil && reportError != nil {
			reportError(err)
		}
	}

	switch u := update.(type) {
	case ServiceProviderUpdateReset:
		if err := scValidateResetSnapshot(u.Snapshot, serviceId, ServiceModeKeyed); err != nil {
			report(err)
			return
		}
		snapshots := omap.NewMap[string, ServiceInstanceSnapshot]()
		for _, snap := range u.Snapshot.Instances {
			if snap.Instance == nil {
				report(errors.New("Remote service reset has an invalid instance address"))
				return
			}
			snapshots.Set(snap.Instance.Key, snap)
		}
		for instance := range b.instances.Values() {
			nextSnap, ok := snapshots.Get(instance.Key())
			if !ok || nextSnap.Instance == nil || nextSnap.Instance.Generation != instance.Generation() {
				b.instances.Remove(instance)
			}
		}
		for _, snap := range snapshots.All() {
			existing, ok := b.instances.Get(snap.Instance.Key)
			if !ok {
				if err := b.spawn(snap, context); err != nil {
					report(err)
					return
				}
			} else {
				if err := existing.facade.Install(snap, context); err != nil {
					report(err)
					return
				}
			}
		}
	case ServiceProviderUpdateUnavailable, ServiceProviderUpdateReplaced:
		report(errors.New("Keyed service received a singleton lifecycle update"))
	case ServiceProviderUpdateSpawned:
		if err := b.spawn(u.Instance, context); err != nil {
			report(err)
		}
	case ServiceProviderUpdateClosed:
		existing, ok := b.instances.Get(u.Instance.Key)
		if ok && existing.Generation() == u.Instance.Generation {
			b.instances.Remove(existing)
		}
	case ServiceProviderUpdateState:
		if u.Instance == nil {
			report(errors.New("Keyed state update has no instance address"))
			return
		}
		existing, ok := b.instances.Get(u.Instance.Key)
		if !ok || existing.Generation() != u.Instance.Generation {
			return
		}
		if err := existing.facade.Update(u.Member, u.Sequence, u.Ops, context); err != nil {
			report(err)
		}
	default:
		report(fmt.Errorf("Unknown service provider update %T", update))
	}
}

func (b *scKeyedBinding) spawn(snapshot ServiceInstanceSnapshot, call Context) error {
	address := snapshot.Instance
	if address == nil {
		return errors.New("Keyed service instance snapshot has no address")
	}
	var active atomic.Bool
	active.Store(true)
	b.mu.Lock()
	serviceId := b.serviceId
	transport := b.transport
	assertAccess := b.assertAccess
	reportError := b.reportError
	b.mu.Unlock()

	facade := scNewServiceFacade(
		serviceId,
		address,
		transport,
		func() bool {
			if !active.Load() {
				return false
			}
			b.mu.Lock()
			closed := b.closed
			b.mu.Unlock()
			return !closed
		},
		assertAccess,
		reportError,
	)
	if err := facade.Install(snapshot, call); err != nil {
		return err
	}
	return b.instances.Replace(&scKeyedInstance{
		key:        address.Key,
		generation: address.Generation,
		service:    facade.proxy,
		facade:     facade,
		deactivate: func() {
			active.Store(false)
			facade.Clear()
		},
	})
}

type RemoteServiceBindingImpl struct {
	mu           sync.Mutex
	transport    RemoteServiceTransport
	allowlist    map[string]struct{}
	reportError  scErrorReporter
	modes        map[string]ServiceMode
	assertAccess func()
	singletons   *omap.Map[string, *scSingletonBinding]
	keyed        *omap.Map[string, *scKeyedBinding]
	// bound defaults to true when RemoteServiceBindingOptions.Bound is omitted.
	bound             bool
	readinessRevision int
	// bindingTransition is the in-flight rebind. Nil means the previous transition has settled.
	bindingTransition func() error
	disposed          bool
}

func NewRemoteServiceBindingImpl(options *RemoteServiceBindingOptions) (*RemoteServiceBindingImpl, error) {
	if options == nil {
		return nil, js.NewTypeError("Remote service binding options must not be nil")
	}
	if options.Transport == nil {
		return nil, js.NewTypeError("Remote service binding transport must not be nil")
	}
	allowlist := make(map[string]struct{}, len(options.Services))
	for _, svc := range options.Services {
		if _, exists := allowlist[svc.Id]; exists {
			return nil, js.NewTypeError("Remote service binding has duplicate service IDs")
		}
		allowlist[svc.Id] = struct{}{}
	}
	bound := true
	if options.Bound != nil {
		bound = *options.Bound
	}
	return &RemoteServiceBindingImpl{
		transport:    options.Transport,
		allowlist:    allowlist,
		reportError:  options.OnError,
		modes:        make(map[string]ServiceMode),
		assertAccess: options.AssertAccess,
		singletons:   omap.NewMap[string, *scSingletonBinding](),
		keyed:        omap.NewMap[string, *scKeyedBinding](),
		bound:        bound,
	}, nil
}

func (b *RemoteServiceBindingImpl) Use(service any) any {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}

	b.mu.Lock()
	if err := b.assertRemotableHead(head); err != nil {
		b.mu.Unlock()
		panic(err)
	}
	if err := b.assertAvailableLocked(head.id, ServiceModeSingleton); err != nil {
		b.mu.Unlock()
		panic(err)
	}
	if existing, ok := b.singletons.Get(head.id); ok {
		proxy := existing.facade.proxy
		b.mu.Unlock()
		return proxy
	}

	binding := &scSingletonBinding{
		active:   true,
		revision: 0,
	}
	serviceId := head.id
	facade := scNewServiceFacade(
		serviceId,
		nil,
		b.transport,
		func() bool {
			b.mu.Lock()
			ok := binding.active && !b.disposed && b.bound
			b.mu.Unlock()
			return ok
		},
		func() {
			if err := b.assertHandleAccess(); err != nil {
				panic(err)
			}
		},
		b.reportError,
	)
	binding.facade = facade
	b.singletons.Set(serviceId, binding)
	b.readinessRevision++
	bound := b.bound
	reportError := b.reportError
	if bound {
		revision := binding.revision
		starting := scStartFuture(func() error {
			return b.startSingleton(serviceId, binding, revision)
		})
		binding.starting = starting
		b.mu.Unlock()
		go func() {
			if startErr := starting(); startErr != nil {
				b.mu.Lock()
				report := binding.active && binding.revision == revision && !b.disposed && b.bound
				b.mu.Unlock()
				if report && reportError != nil {
					reportError(startErr)
				}
			}
		}()
		return facade.proxy
	}
	b.mu.Unlock()
	return facade.proxy
}

func (b *RemoteServiceBindingImpl) Observe(service any, handler func(service any, call Context) error) func() {
	head, err := spExtractServiceHead(service)
	if err != nil {
		panic(err)
	}

	b.mu.Lock()
	if err := b.assertRemotableHead(head); err != nil {
		b.mu.Unlock()
		panic(err)
	}
	if err := b.assertAvailableLocked(head.id, ServiceModeKeyed); err != nil {
		b.mu.Unlock()
		panic(err)
	}
	serviceId := head.id
	kb, ok := b.keyed.Get(serviceId)
	if !ok {
		var created *scKeyedBinding
		created = scNewKeyedBinding(
			service,
			b.transport,
			b.reportError,
			func() {
				if err := b.assertHandleAccess(); err != nil {
					panic(err)
				}
			},
			func() {
				b.mu.Lock()
				cur, has := b.keyed.Get(serviceId)
				if !has || cur != created {
					b.mu.Unlock()
					return
				}
				b.keyed.Delete(serviceId)
				b.readinessRevision++
				reportError := b.reportError
				b.mu.Unlock()
				if closeErr := created.Close(BackgroundContext); closeErr != nil && reportError != nil {
					reportError(closeErr)
				}
			},
			b.bound,
		)
		b.keyed.Set(serviceId, created)
		b.readinessRevision++
		kb = created
	}
	b.mu.Unlock()

	stop, err := kb.Observe(handler)
	if err != nil {
		panic(err)
	}
	return stop
}

func (b *RemoteServiceBindingImpl) Ready(call Context) error {
	for {
		b.mu.Lock()
		if b.disposed {
			b.mu.Unlock()
			return errors.New("Remote service binding is disposed")
		}
		revision := b.readinessRevision
		var starts []func() error
		if b.bindingTransition != nil {
			starts = append(starts, b.bindingTransition)
		}
		for _, binding := range b.singletons.All() {
			if binding.starting != nil {
				starts = append(starts, binding.starting)
			}
		}
		for _, kb := range b.keyed.All() {
			k := kb
			starts = append(starts, k.Ready)
		}
		b.mu.Unlock()

		_, err := AwaitWithContext(func() (struct{}, error) {
			return struct{}{}, scWaitAll(starts)
		}, call)
		if err != nil {
			return err
		}

		b.mu.Lock()
		disposed := b.disposed
		done := revision == b.readinessRevision
		b.mu.Unlock()
		if disposed {
			return errors.New("Remote service binding is disposed")
		}
		if done {
			return nil
		}
	}
}

func (b *RemoteServiceBindingImpl) Rebind(bound bool, call Context) error {
	type singletonTask struct {
		serviceId string
		binding   *scSingletonBinding
		sub       ServiceSubscription
		revision  int
		start     func() error
	}

	b.mu.Lock()
	if b.disposed {
		b.mu.Unlock()
		return errors.New("Remote service binding is disposed")
	}
	b.bound = bound
	b.readinessRevision++

	tasks := make([]*singletonTask, 0, b.singletons.Len())
	for serviceId, binding := range b.singletons.All() {
		binding.revision++
		sub := binding.subscription
		binding.subscription = nil
		t := &singletonTask{
			serviceId: serviceId,
			binding:   binding,
			sub:       sub,
			revision:  binding.revision,
		}
		task := t
		t.start = scStartFuture(func() error {
			if task.sub != nil {
				if err := task.sub.Close(call); err != nil {
					return err
				}
			}
			if bound {
				return b.startSingleton(task.serviceId, task.binding, task.revision)
			}
			return nil
		})
		binding.starting = t.start
		tasks = append(tasks, t)
	}

	keyedList := make([]*scKeyedBinding, 0, b.keyed.Len())
	for _, kb := range b.keyed.All() {
		keyedList = append(keyedList, kb)
	}

	completion := scStartFuture(func() error {
		var transitions []func() error
		for _, t := range tasks {
			transitions = append(transitions, t.start)
		}
		for _, kb := range keyedList {
			k := kb
			transitions = append(transitions, func() error {
				return k.Rebind(bound, call)
			})
		}
		errs := scWaitAllSettled(transitions)
		if len(errs) > 0 {
			return ssJoinErrors("Failed to rebind services", errs)
		}
		return nil
	})
	b.bindingTransition = completion
	b.mu.Unlock()

	for _, t := range tasks {
		t.binding.facade.Clear()
	}

	return completion()
}

func (b *RemoteServiceBindingImpl) Dispose(call Context) error {
	b.mu.Lock()
	if b.disposed {
		b.mu.Unlock()
		return nil
	}
	b.disposed = true

	singletons := make([]*scSingletonBinding, 0, b.singletons.Len())
	var closes []func() error
	for _, binding := range b.singletons.All() {
		binding.active = false
		singletons = append(singletons, binding)
		if binding.starting != nil {
			s := binding.starting
			closes = append(closes, func() error {
				_ = s()
				return nil
			})
		}
		if binding.subscription != nil {
			sub := binding.subscription
			closes = append(closes, func() error {
				return sub.Close(call)
			})
		}
	}
	keyedList := make([]*scKeyedBinding, 0, b.keyed.Len())
	for _, kb := range b.keyed.All() {
		k := kb
		keyedList = append(keyedList, k)
		closes = append(closes, func() error {
			return k.Close(call)
		})
	}
	b.singletons.Clear()
	b.keyed.Clear()
	b.mu.Unlock()

	for _, binding := range singletons {
		binding.facade.Clear()
	}
	errs := scWaitAllSettled(closes)
	if len(errs) > 0 {
		return ssJoinErrors("Failed to dispose services", errs)
	}
	return nil
}

func (b *RemoteServiceBindingImpl) startSingleton(serviceId string, binding *scSingletonBinding, revision int) error {
	subscription, err := b.transport.Subscribe(
		serviceId,
		ServiceModeSingleton,
		func(update ServiceProviderUpdate, ctx Context) {
			b.mu.Lock()
			active := binding.active && binding.revision == revision
			reportError := b.reportError
			b.mu.Unlock()
			if !active {
				return
			}
			report := func(err error) {
				if err != nil && reportError != nil {
					reportError(err)
				}
			}
			switch u := update.(type) {
			case ServiceProviderUpdateReset:
				if err := scValidateResetSnapshot(u.Snapshot, serviceId, ServiceModeSingleton); err != nil {
					report(err)
					return
				}
				if len(u.Snapshot.Instances) == 0 {
					binding.facade.Clear()
				} else {
					if err := binding.facade.Install(u.Snapshot.Instances[0], ctx); err != nil {
						report(err)
						return
					}
				}
			case ServiceProviderUpdateUnavailable:
				binding.facade.Clear()
			case ServiceProviderUpdateReplaced:
				if u.Snapshot.Instance != nil {
					report(errors.New("Singleton replacement has an instance address"))
					return
				}
				if err := binding.facade.Install(u.Snapshot, ctx); err != nil {
					report(err)
					return
				}
			case ServiceProviderUpdateState:
				if u.Instance == nil {
					if err := binding.facade.Update(u.Member, u.Sequence, u.Ops, ctx); err != nil {
						report(err)
						return
					}
				}
			}
		},
		BackgroundContext,
	)
	if err != nil {
		return err
	}

	b.mu.Lock()
	stale := !binding.active || b.disposed || !b.bound || binding.revision != revision
	if !stale {
		binding.subscription = subscription
	}
	b.mu.Unlock()
	if stale {
		_ = subscription.Close(BackgroundContext)
		return nil
	}

	snap := subscription.Snapshot()
	if snap.Mode != ServiceModeSingleton || snap.ServiceId != serviceId || len(snap.Instances) != 1 {
		return fmt.Errorf("Remote service %s returned an invalid singleton snapshot", serviceId)
	}
	if err := binding.facade.Install(snap.Instances[0], ServiceDeliveryContext()); err != nil {
		return err
	}
	b.mu.Lock()
	stale = !binding.active || b.disposed || !b.bound || binding.revision != revision
	b.mu.Unlock()
	if stale {
		return nil
	}
	return subscription.Activate()
}

func (b *RemoteServiceBindingImpl) assertHandleAccess() error {
	b.mu.Lock()
	disposed := b.disposed
	assertAccess := b.assertAccess
	b.mu.Unlock()
	if disposed {
		return errors.New("Remote service binding is disposed")
	}
	return shCallAssertAccess(assertAccess)
}

func (b *RemoteServiceBindingImpl) assertRemotable(service any) error {
	head, err := spExtractServiceHead(service)
	if err != nil {
		return err
	}
	return b.assertRemotableHead(head)
}

func (b *RemoteServiceBindingImpl) assertRemotableHead(head spServiceHead) error {
	if head.local != nil && *head.local {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotAllowed,
			fmt.Sprintf("Service %s is process-local", head.id),
		)
	}
	return nil
}

func (b *RemoteServiceBindingImpl) assertAvailable(serviceId string, mode ServiceMode) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.assertAvailableLocked(serviceId, mode)
}

func (b *RemoteServiceBindingImpl) assertAvailableLocked(serviceId string, mode ServiceMode) error {
	if b.disposed {
		return errors.New("Remote service binding is disposed")
	}
	if _, ok := b.allowlist[serviceId]; !ok {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceNotAllowed,
			fmt.Sprintf("Remote service %s is not allowlisted", serviceId),
		)
	}
	if existing, ok := b.modes[serviceId]; ok && existing != mode {
		return NewRemoteServiceError(
			RemoteServiceErrorCodeServiceModeMismatch,
			fmt.Sprintf("Remote service %s is already used as %s", serviceId, existing),
		)
	}
	b.modes[serviceId] = mode
	return nil
}

func scStartFuture(fn func() error) func() error {
	ch := make(chan struct{})
	var err error
	go func() {
		err = fn()
		close(ch)
	}()
	return func() error {
		<-ch
		return err
	}
}

func scWaitAll(fns []func() error) error {
	if len(fns) == 0 {
		return nil
	}
	if len(fns) == 1 {
		return fns[0]()
	}
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		if fn == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, f func() error) {
			defer wg.Done()
			errs[idx] = f()
		}(i, fn)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func scWaitAllSettled(fns []func() error) []any {
	if len(fns) == 0 {
		return nil
	}
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		if fn == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, f func() error) {
			defer wg.Done()
			errs[idx] = f()
		}(i, fn)
	}
	wg.Wait()
	var rejected []any
	for _, err := range errs {
		if err != nil {
			rejected = append(rejected, err)
		}
	}
	return rejected
}

func scValidateResetSnapshot(snapshot ServiceSubscriptionSnapshot, serviceId string, mode ServiceMode) error {
	if snapshot.ServiceId != serviceId || snapshot.Mode != mode || (mode == ServiceModeSingleton && len(snapshot.Instances) > 1) {
		return errors.New("Remote service reset has the wrong service or mode")
	}
	keys := make(map[string]struct{}, len(snapshot.Instances))
	for _, instance := range snapshot.Instances {
		if mode == ServiceModeSingleton {
			if instance.Instance != nil {
				return errors.New("Remote service reset has an invalid instance address")
			}
		} else {
			if instance.Instance == nil {
				return errors.New("Remote service reset has an invalid instance address")
			}
		}
		if instance.Instance != nil {
			if _, exists := keys[instance.Instance.Key]; exists {
				return errors.New("Remote service reset repeats an instance key")
			}
			keys[instance.Instance.Key] = struct{}{}
		}
		for _, member := range instance.Members {
			if st, ok := member.(ServiceMemberSnapshotState); ok {
				if len(st.Ops) != 1 || st.Ops[0].Verb() != "r" {
					return errors.New("Remote service reset must contain full root replacements")
				}
			}
		}
	}
	return nil
}

func scValidateMembers(members ServiceMemberSnapshotList) (*omap.Map[string, ServiceMemberSnapshot], error) {
	result := omap.NewMap[string, ServiceMemberSnapshot]()
	for _, member := range members {
		var name string
		switch m := member.(type) {
		case ServiceMemberSnapshotMethod:
			name = m.Name
		case ServiceMemberSnapshotState:
			name = m.Name
		case UnknownServiceMemberSnapshot:
			return nil, errors.New("Remote service has invalid member descriptions")
		default:
			return nil, errors.New("Remote service has invalid member descriptions")
		}
		if len(name) == 0 || result.Has(name) {
			return nil, errors.New("Remote service has invalid member descriptions")
		}
		result.Set(name, member)
	}
	return result, nil
}

func scMemberKind(member ServiceMemberSnapshot) scServiceMemberKind {
	switch member.(type) {
	case ServiceMemberSnapshotState:
		return scServiceMemberKindState
	default:
		return scServiceMemberKindMethod
	}
}

func scSameAddress(left, right *ServiceInstanceAddress) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Key == right.Key && left.Generation == right.Generation
}

func scIsContext(value any) bool {
	if value == nil {
		return false
	}
	_, ok := value.(Context)
	return ok
}

func scToError(value any) error {
	return ssToError(value)
}

var _ RemoteServiceBinding = (*RemoteServiceBindingImpl)(nil)
