// Ported from packages/chord/src/services/provider.ts (pi v1.0.0).

package chord

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

// spRemoteMethod is one exposed method. TS invokes it with the implementation as this
// and the call arguments followed by Context. The promise result is (any, error).
type spRemoteMethod func(args ...any) (any, error)

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
	panic("unported: NewRemoteServiceProvider")
}

func (p *RemoteServiceProvider) Catalogue() []ServiceCatalogueEntry {
	return p.catalogue
}

// Provide publishes one singleton. service's dynamic type is *Service[T].
func (p *RemoteServiceProvider) Provide(service any, implementation any) error {
	panic("unported: Provide")
}

// Withdraw disconnects one singleton while preserving active subscriptions and remote facades.
func (p *RemoteServiceProvider) Withdraw(service any) error {
	panic("unported: Withdraw")
}

// ValidateReplacement checks a singleton replacement without changing the active provider.
func (p *RemoteServiceProvider) ValidateReplacement(service any, implementation any) error {
	panic("unported: ValidateReplacement")
}

// Replace replaces one singleton without making its stable remote facade unavailable.
func (p *RemoteServiceProvider) Replace(service any, implementation any) error {
	panic("unported: Replace")
}

// Use returns the local singleton implementation.
// service's dynamic type is *Service[T]; the result's dynamic type is T.
func (p *RemoteServiceProvider) Use(service any) (any, error) {
	panic("unported: Use")
}

// Spawn publishes one keyed instance. The returned function closes it.
func (p *RemoteServiceProvider) Spawn(service any, key string, implementation any) (func() error, error) {
	panic("unported: Spawn")
}

func (p *RemoteServiceProvider) Invoke(call ServiceCall, callContext Context) (value JsonValue, ok bool, err error) {
	panic("unported: Invoke")
}

func (p *RemoteServiceProvider) Subscribe(serviceId string, mode ServiceMode, listener func(update ServiceProviderUpdate, context Context) error) (ServiceSubscription, error) {
	panic("unported: Subscribe")
}

func (p *RemoteServiceProvider) Dispose() error {
	panic("unported: Dispose")
}

func (p *RemoteServiceProvider) registration(serviceId string, mode ServiceMode) (*spServiceRegistration, error) {
	panic("unported: registration")
}

func (p *RemoteServiceProvider) createInstance(registration *spServiceRegistration, classified spClassifiedRemoteServiceImplementation, address *ServiceInstanceAddress) *spProviderInstance {
	panic("unported: createInstance")
}

func (p *RemoteServiceProvider) assertSingletonShape(registration *spServiceRegistration, replacement map[string]spServiceMemberKind) error {
	panic("unported: assertSingletonShape")
}

func (p *RemoteServiceProvider) resolveInstance(registration *spServiceRegistration, address *ServiceInstanceAddress) (*spProviderInstance, error) {
	panic("unported: resolveInstance")
}

func (p *RemoteServiceProvider) snapshot(registration *spServiceRegistration) ServiceSubscriptionSnapshot {
	panic("unported: snapshot")
}

func (p *RemoteServiceProvider) snapshotInstance(instance *spProviderInstance) ServiceInstanceSnapshot {
	panic("unported: snapshotInstance")
}

func (p *RemoteServiceProvider) emit(registration *spServiceRegistration, update ServiceProviderUpdate, context Context) error {
	panic("unported: emit")
}

func (p *RemoteServiceProvider) assertRemotable(service spServiceHead) error {
	panic("unported: assertRemotable")
}

func (p *RemoteServiceProvider) assertAllowed(serviceId string) error {
	panic("unported: assertAllowed")
}

func (p *RemoteServiceProvider) assertActive() error {
	panic("unported: assertActive")
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
	panic("unported: Activate")
}

func (s *spSubscription) Close(context Context) error {
	panic("unported: Close")
}

func spDrainSubscriber(subscriber *spProviderSubscriber) []any {
	panic("unported: spDrainSubscriber")
}

func spRecordSnapshotSequences(sequences *omap.Map[string, int], instances []ServiceInstanceSnapshot) {
	panic("unported: spRecordSnapshotSequences")
}

func spUpdateCoveredBySnapshot(sequences *omap.Map[string, int], update ServiceProviderUpdate) bool {
	panic("unported: spUpdateCoveredBySnapshot")
}

func spStateMemberKey(instance *ServiceInstanceAddress, member string) string {
	panic("unported: spStateMemberKey")
}

// CreateRemoteServiceEndpoint returns an endpoint that owns one consumer's subscriptions on provider.
func CreateRemoteServiceEndpoint(provider *RemoteServiceProvider) RemoteServiceEndpoint {
	panic("unported: CreateRemoteServiceEndpoint")
}

type spRemoteServiceEndpoint struct {
	mu            sync.Mutex
	provider      *RemoteServiceProvider
	subscriptions *omap.Map[string, ServiceSubscription]
	disposed      bool
}

func (e *spRemoteServiceEndpoint) Invoke(call ServiceCall, publish ServiceUpdatePublisher, callContext Context) (value JsonValue, ok bool, err error) {
	panic("unported: Invoke")
}

func (e *spRemoteServiceEndpoint) Dispose() {
	panic("unported: Dispose")
}

// ValidateRemoteServiceImplementation rejects an implementation that cannot be published remotely.
func ValidateRemoteServiceImplementation(serviceId string, implementation any) error {
	panic("unported: ValidateRemoteServiceImplementation")
}

func spClassifyRemoteServiceImplementation(serviceId string, implementation any) (spClassifiedRemoteServiceImplementation, error) {
	panic("unported: spClassifyRemoteServiceImplementation")
}

func spThrowCollectedErrors(errs []any, message string) error {
	panic("unported: spThrowCollectedErrors")
}

func spServiceMemberShape(members *omap.Map[string, spInstanceMember]) map[string]spServiceMemberKind {
	panic("unported: spServiceMemberShape")
}

func spSameServiceMemberShape(left map[string]spServiceMemberKind, right map[string]spServiceMemberKind) bool {
	panic("unported: spSameServiceMemberShape")
}
