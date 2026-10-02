// Ported from packages/chord/src/services/consumer.ts (pi v1.0.0).

package chord

import (
	"sync"

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
	panic("unported: scNewMemberSlot")
}

func (m *scMemberSlot) SetDescription(kind scServiceMemberKind) error {
	panic("unported: scMemberSlot.SetDescription")
}

func (m *scMemberSlot) Hydrate(sequence int, ops []Op, context Context) error {
	panic("unported: scMemberSlot.Hydrate")
}

func (m *scMemberSlot) Update(sequence int, ops []Op, context Context) error {
	panic("unported: scMemberSlot.Update")
}

func (m *scMemberSlot) Clear() {
	panic("unported: scMemberSlot.Clear")
}

func (m *scMemberSlot) subscribe(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) (func(), error) {
	panic("unported: scMemberSlot.subscribe")
}

func (m *scMemberSlot) expect(kind scServiceMemberKind) error {
	panic("unported: scMemberSlot.expect")
}

func (m *scMemberSlot) call(args []any) (JsonValue, bool, error) {
	panic("unported: scMemberSlot.call")
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
	panic("unported: scNewServiceFacade")
}

func (f *scServiceFacade) Install(snapshot ServiceInstanceSnapshot, call Context) error {
	panic("unported: scServiceFacade.Install")
}

func (f *scServiceFacade) Update(member string, sequence int, ops []Op, context Context) error {
	panic("unported: scServiceFacade.Update")
}

func (f *scServiceFacade) Clear() {
	panic("unported: scServiceFacade.Clear")
}

func (f *scServiceFacade) slot(member string) (*scMemberSlot, error) {
	panic("unported: scServiceFacade.slot")
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
	service      any
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
	panic("unported: scNewKeyedBinding")
}

func (b *scKeyedBinding) Observe(handler func(service any, context Context) error) (func(), error) {
	panic("unported: scKeyedBinding.Observe")
}

func (b *scKeyedBinding) Rebind(bound bool, context Context) error {
	panic("unported: scKeyedBinding.Rebind")
}

func (b *scKeyedBinding) Ready() error {
	panic("unported: scKeyedBinding.Ready")
}

func (b *scKeyedBinding) Close(context Context) error {
	panic("unported: scKeyedBinding.Close")
}

func (b *scKeyedBinding) reset(context Context, waitForStarting bool) error {
	panic("unported: scKeyedBinding.reset")
}

func (b *scKeyedBinding) start(revision int) error {
	panic("unported: scKeyedBinding.start")
}

func (b *scKeyedBinding) update(update ServiceProviderUpdate, context Context) {
	panic("unported: scKeyedBinding.update")
}

func (b *scKeyedBinding) spawn(snapshot ServiceInstanceSnapshot, call Context) error {
	panic("unported: scKeyedBinding.spawn")
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
	panic("unported: NewRemoteServiceBindingImpl")
}

func (b *RemoteServiceBindingImpl) Use(service any) any {
	panic("unported: RemoteServiceBindingImpl.Use")
}

func (b *RemoteServiceBindingImpl) Observe(service any, handler func(service any, call Context) error) func() {
	panic("unported: RemoteServiceBindingImpl.Observe")
}

func (b *RemoteServiceBindingImpl) Ready(call Context) error {
	panic("unported: RemoteServiceBindingImpl.Ready")
}

func (b *RemoteServiceBindingImpl) Rebind(bound bool, call Context) error {
	panic("unported: RemoteServiceBindingImpl.Rebind")
}

func (b *RemoteServiceBindingImpl) Dispose(call Context) error {
	panic("unported: RemoteServiceBindingImpl.Dispose")
}

func (b *RemoteServiceBindingImpl) startSingleton(serviceId string, binding *scSingletonBinding, revision int) error {
	panic("unported: RemoteServiceBindingImpl.startSingleton")
}

func (b *RemoteServiceBindingImpl) assertHandleAccess() error {
	panic("unported: RemoteServiceBindingImpl.assertHandleAccess")
}

func (b *RemoteServiceBindingImpl) assertRemotable(service any) error {
	panic("unported: RemoteServiceBindingImpl.assertRemotable")
}

func (b *RemoteServiceBindingImpl) assertAvailable(serviceId string, mode ServiceMode) error {
	panic("unported: RemoteServiceBindingImpl.assertAvailable")
}

func scValidateResetSnapshot(snapshot ServiceSubscriptionSnapshot, serviceId string, mode ServiceMode) error {
	panic("unported: scValidateResetSnapshot")
}

func scValidateMembers(members ServiceMemberSnapshotList) (*omap.Map[string, ServiceMemberSnapshot], error) {
	panic("unported: scValidateMembers")
}

func scSameAddress(left, right *ServiceInstanceAddress) bool {
	panic("unported: scSameAddress")
}

func scIsContext(value any) bool {
	panic("unported: scIsContext")
}

func scToError(value any) error {
	panic("unported: scToError")
}

var _ RemoteServiceBinding = (*RemoteServiceBindingImpl)(nil)
