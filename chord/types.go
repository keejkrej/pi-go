// Ported from packages/chord/src/types.ts (pi v1.0.0).

package chord

import (
	"context"
	"math"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// typesSymbol is the process-local identity of a ContextKey (TS symbol).
type typesSymbol struct {
	description string
}

// ContextKey is a typed identity for one value carried by a Context.
// valueType is a TypeScript-only phantom and has no Go field.
type ContextKey[T any] struct {
	// Token is the process-local symbol. It is not JSON.
	Token *typesSymbol `json:"-"`
}

// Context is immutable invocation-scoped values passed explicitly through operations.
//
// Value is untyped because a Go interface cannot declare a generic method.
// Pass a *ContextKey[T]. The boolean is false when the key is absent; a present
// key may still hold a nil value (TS undefined), which shadows parent values.
type Context interface {
	AbortSignal() context.Context
	Value(key any) (any, bool)
	String() string
}

// JsonValue is one strict JSON value in the jsonx untyped model:
// nil, bool, float64, string, []any, or *jsonx.Object.
// Nil is JSON null. A missing (undefined) value is not a JsonValue; callers
// that must tell them apart use a separate boolean.
type JsonValue = any

// JsonRepresentation is the strict-JSON form of an application value.
// The TypeScript conditional type has no separate runtime representation.
type JsonRepresentation[T any] = JsonValue

// ReplicatedStateDeliveryKind is "hydrate" or "update".
type ReplicatedStateDeliveryKind string

const (
	ReplicatedStateDeliveryKindHydrate ReplicatedStateDeliveryKind = "hydrate"
	ReplicatedStateDeliveryKindUpdate  ReplicatedStateDeliveryKind = "update"
)

// ReplicatedStateDelivery is one hydration or update delivered to a subscriber.
// Object key order: kind, sequence.
type ReplicatedStateDelivery struct {
	Kind     ReplicatedStateDeliveryKind `json:"kind"`
	Sequence int                         `json:"sequence"`
}

// ReplicatedState is a publication of immutable values.
// Value's boolean is false until hydration. The value is not frozen and may
// share containers with an in-process provider; consumers must not mutate it.
// Later updates do not mutate previously returned values.
//
// Each subscription serializes callbacks, awaiting hydration before updates.
// At most 100 deliveries wait behind the running callback; overflow keeps only
// the newest pending value, context, and delivery, so update sequences may skip.
// Failures are reported in isolation and delivery continues. Unsubscribe discards
// pending work without aborting or joining a callback.
type ReplicatedState[T any] interface {
	Value() (T, bool)
	Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func())
}

// MutableReplicatedState is authoritative state with synchronous mutation.
// Value is always present (the boolean from ReplicatedState.Value is true).
// Draft handles are unusable after the change callback returns. Values placed
// through the draft are cloned by value; assigning a deleted object property
// removes it. Replace takes immutable ownership of an alias-free strict-JSON
// root; the caller must not mutate the transferred root after the call.
type MutableReplicatedState[T any] interface {
	ReplicatedState[T]
	Change(call Context, mutate func(draft Draft[T]) error) error
	Replace(call Context, value T) error
}

// ReplicatedStateSourceFrame is one immutable authoritative revision committed
// after an attachment snapshot. Key order: cursor, value, ops, context.
type ReplicatedStateSourceFrame[T any] struct {
	Cursor  int     `json:"cursor"`
	Value   T       `json:"value"`
	Ops     OpList  `json:"ops"`
	Context Context `json:"context"`
}

// ReplicatedStateSourceSnapshot is the fixed snapshot captured at an attachment
// boundary. Key order: value, cursor.
type ReplicatedStateSourceSnapshot[T any] struct {
	Value  T   `json:"value"`
	Cursor int `json:"cursor"`
}

// ReplicatedStateSourceAttachment buffers source frames for one subscriber.
// Activate is single-use. After it begins, every new committed frame is delivered
// in order until disposal, including commits made reentrantly while a prior frame
// is being delivered. Dispose stops delivery, releases source resources, and is
// idempotent.
type ReplicatedStateSourceAttachment[T any] interface {
	Snapshot() ReplicatedStateSourceSnapshot[T]
	Activate(listener func(frame ReplicatedStateSourceFrame[T]))
	Dispose()
}

// ReplicatedStateSource is an authoritative immutable revision source.
// Attach must synchronously and atomically capture one snapshot and register the
// returned attachment to buffer every later committed frame, with no overlap or gap.
// Snapshot values, frame values, and operation batches stay immutable after delivery.
// Chord publishes these references; it never applies or re-diffs them.
type ReplicatedStateSource[T any] interface {
	Attach() ReplicatedStateSourceAttachment[T]
}

// ReplicatedStateSourceOptions receives source-contract and publication-listener
// failures without throwing them into the source.
type ReplicatedStateSourceOptions struct {
	OnError func(error) `json:"onError,omitzero"`
}

// AttachedReplicatedState is a synchronously hydrated publication-only state
// backed by one source attachment. Value is always present. Dispose idempotently
// releases the source attachment. The last published value remains readable.
type AttachedReplicatedState[T any] interface {
	ReplicatedState[T]
	Dispose()
}

// ServiceMode is "singleton" or "keyed".
type ServiceMode string

const (
	ServiceModeSingleton ServiceMode = "singleton"
	ServiceModeKeyed     ServiceMode = "keyed"
)

// Service is the stable identity for one shared service contract.
// Process-local services accept unrestricted object contracts and are never
// published remotely. The TypeScript SERVICE_TYPE phantom has no Go field.
// Key order: id, local.
type Service[T any] struct {
	Id    string `json:"id"`
	Local bool   `json:"local"`
}

// RemoteServiceContract is T when every member is a remote method or
// ReplicatedState whose values are strict JSON. The TypeScript constraint has
// no separate runtime representation. Go rejects `type RemoteServiceContract[T any] = T`.
type RemoteServiceContract[T any] struct {
	Value T
}

// ServiceSpawner starts keyed instances of one service.
type ServiceSpawner[T any] interface {
	Spawn(key string, implementation T) (dispose func())
}

// RemoteServices is the consumer view of remote services.
// Use's argument is *Service[T] and the result's dynamic type is T.
// Observe returns unsubscribe. Ready waits until every currently acquired
// service has installed its initial snapshot.
type RemoteServices interface {
	Use(service any) any
	Observe(service any, handler func(service any, call Context) error) (unsubscribe func())
	Ready(call Context) error
	Dispose(call Context) error
}

// ServiceCatalogueEntry is one allowlisted service. Key order: serviceId, mode.
type ServiceCatalogueEntry struct {
	ServiceId string      `json:"serviceId"`
	Mode      ServiceMode `json:"mode"`
}

// ServiceInstanceAddress names one generation of a keyed instance.
// Key order: key, generation.
type ServiceInstanceAddress struct {
	Key        string `json:"key"`
	Generation int    `json:"generation"`
}

// ServiceMemberSnapshot is one method or state member in an instance snapshot.
type ServiceMemberSnapshot interface {
	isServiceMemberSnapshot()
}

// ServiceMemberSnapshotMethod is `{ name, kind: "method" }`.
// Kind is always "method".
type ServiceMemberSnapshotMethod struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (ServiceMemberSnapshotMethod) isServiceMemberSnapshot() {}

// ServiceMemberSnapshotState is `{ name, kind: "state", sequence, ops }`.
// Kind is always "state".
type ServiceMemberSnapshotState struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Sequence int    `json:"sequence"`
	Ops      OpList `json:"ops"`
}

func (ServiceMemberSnapshotState) isServiceMemberSnapshot() {}

// UnknownServiceMemberSnapshot keeps an unrecognized member snapshot verbatim.
type UnknownServiceMemberSnapshot struct {
	Raw *jsonx.Object
}

func (UnknownServiceMemberSnapshot) isServiceMemberSnapshot() {}

func (u UnknownServiceMemberSnapshot) MarshalJSON() ([]byte, error) {
	if u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if val == nil {
		u.Raw = nil
		return nil
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return js.NewTypeError("unknown service member snapshot is not an object")
	}
	u.Raw = obj
	return nil
}

// ServiceMemberSnapshotList is a JSON array of member snapshots.
type ServiceMemberSnapshotList []ServiceMemberSnapshot

func (l ServiceMemberSnapshotList) MarshalJSON() ([]byte, error) {
	out := make([]any, len(l))
	for i, member := range l {
		if sm, ok := member.(ServiceMemberSnapshotState); ok && sm.Ops == nil {
			sm.Ops = OpList{}
			out[i] = sm
		} else if smp, ok := member.(*ServiceMemberSnapshotState); ok && smp != nil && smp.Ops == nil {
			copyVal := *smp
			copyVal.Ops = OpList{}
			out[i] = copyVal
		} else {
			out[i] = member
		}
	}
	return jsonx.Marshal(out)
}

func (l *ServiceMemberSnapshotList) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	arr, ok := val.([]any)
	if !ok || arr == nil {
		return js.NewTypeError("service member snapshot list is not an array")
	}
	out := make(ServiceMemberSnapshotList, len(arr))
	for i, item := range arr {
		m, err := DecodeServiceMemberSnapshot(item)
		if err != nil {
			return err
		}
		out[i] = m
	}
	*l = out
	return nil
}

// UnmarshalServiceMemberSnapshot decodes one member snapshot.
func UnmarshalServiceMemberSnapshot(data []byte) (ServiceMemberSnapshot, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeServiceMemberSnapshot(val)
}

// DecodeServiceMemberSnapshot decodes one member snapshot from a jsonx value.
func DecodeServiceMemberSnapshot(v any) (ServiceMemberSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, js.NewTypeError("service member snapshot is not an object")
	}
	kindVal, ok := obj.Get("kind")
	if !ok {
		return UnknownServiceMemberSnapshot{Raw: obj}, nil
	}
	kind, ok := kindVal.(string)
	if !ok {
		return UnknownServiceMemberSnapshot{Raw: obj}, nil
	}
	switch kind {
	case "method":
		nameVal, okName := obj.Get("name")
		name, isStr := nameVal.(string)
		if !okName || !isStr {
			return nil, js.NewTypeError("Invalid service method snapshot")
		}
		return ServiceMemberSnapshotMethod{Name: name, Kind: "method"}, nil
	case "state":
		nameVal, okName := obj.Get("name")
		name, isStr := nameVal.(string)
		seqVal, okSeq := obj.Get("sequence")
		seq, isInt := typesToInt(seqVal)
		opsVal, okOps := obj.Get("ops")
		opsArr, isArr := opsVal.([]any)
		if !okName || !isStr || !okSeq || !isInt || !okOps || !isArr || opsArr == nil {
			return nil, js.NewTypeError("Invalid service state snapshot")
		}
		ops := make(OpList, len(opsArr))
		for i, item := range opsArr {
			op, err := DecodeOp(item)
			if err != nil {
				return nil, err
			}
			ops[i] = op
		}
		return ServiceMemberSnapshotState{
			Name:     name,
			Kind:     "state",
			Sequence: seq,
			Ops:      ops,
		}, nil
	default:
		return UnknownServiceMemberSnapshot{Raw: obj}, nil
	}
}

// ServiceInstanceSnapshot is one instance baseline.
// Key order when an address is present: instance, members. A nil Instance is omitted.
type ServiceInstanceSnapshot struct {
	Instance *ServiceInstanceAddress   `json:"instance,omitzero"`
	Members  ServiceMemberSnapshotList `json:"members"`
}

// ServiceSubscriptionSnapshot is the atomic baseline for one service subscription.
// Key order: serviceId, mode, instances.
type ServiceSubscriptionSnapshot struct {
	ServiceId string                    `json:"serviceId"`
	Mode      ServiceMode               `json:"mode"`
	Instances []ServiceInstanceSnapshot `json:"instances"`
}

// ServiceProviderUpdate is one subscription event.
type ServiceProviderUpdate interface {
	isServiceProviderUpdate()
}

// ServiceProviderUpdateState is a state member update.
// Type is always "state". Key order: type, instance (omitted when nil), member, sequence, ops.
type ServiceProviderUpdateState struct {
	Type     string                  `json:"type"`
	Instance *ServiceInstanceAddress `json:"instance,omitzero"`
	Member   string                  `json:"member"`
	Sequence int                     `json:"sequence"`
	Ops      OpList                  `json:"ops"`
}

func (ServiceProviderUpdateState) isServiceProviderUpdate() {}

// ServiceProviderUpdateReset is a full subscription rebaseline after overflow.
// Type is always "reset". Every state member is a root replacement at its new sequence.
// Key order: type, snapshot.
type ServiceProviderUpdateReset struct {
	Type     string                      `json:"type"`
	Snapshot ServiceSubscriptionSnapshot `json:"snapshot"`
}

func (ServiceProviderUpdateReset) isServiceProviderUpdate() {}

// ServiceProviderUpdateUnavailable reports that the service is unavailable.
// Type is always "unavailable".
type ServiceProviderUpdateUnavailable struct {
	Type string `json:"type"`
}

func (ServiceProviderUpdateUnavailable) isServiceProviderUpdate() {}

// ServiceProviderUpdateReplaced is a singleton provider replacement.
// Type is always "replaced". Key order: type, snapshot.
type ServiceProviderUpdateReplaced struct {
	Type     string                  `json:"type"`
	Snapshot ServiceInstanceSnapshot `json:"snapshot"`
}

func (ServiceProviderUpdateReplaced) isServiceProviderUpdate() {}

// ServiceProviderUpdateSpawned is a new keyed instance.
// Type is always "spawned". Key order: type, instance.
type ServiceProviderUpdateSpawned struct {
	Type     string                  `json:"type"`
	Instance ServiceInstanceSnapshot `json:"instance"`
}

func (ServiceProviderUpdateSpawned) isServiceProviderUpdate() {}

// ServiceProviderUpdateClosed is a closed keyed instance.
// Type is always "closed". Key order: type, instance.
type ServiceProviderUpdateClosed struct {
	Type     string                 `json:"type"`
	Instance ServiceInstanceAddress `json:"instance"`
}

func (ServiceProviderUpdateClosed) isServiceProviderUpdate() {}

// UnknownServiceProviderUpdate keeps an unrecognized update verbatim.
type UnknownServiceProviderUpdate struct {
	Raw *jsonx.Object
}

func (UnknownServiceProviderUpdate) isServiceProviderUpdate() {}

func (u UnknownServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	if u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownServiceProviderUpdate) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if val == nil {
		u.Raw = nil
		return nil
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return js.NewTypeError("unknown service provider update is not an object")
	}
	u.Raw = obj
	return nil
}

// UnmarshalServiceProviderUpdate decodes one provider update.
func UnmarshalServiceProviderUpdate(data []byte) (ServiceProviderUpdate, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeServiceProviderUpdate(val)
}

// DecodeServiceProviderUpdate decodes one provider update from a jsonx value.
func DecodeServiceProviderUpdate(v any) (ServiceProviderUpdate, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, js.NewTypeError("service provider update is not an object")
	}
	typeVal, ok := obj.Get("type")
	if !ok {
		return UnknownServiceProviderUpdate{Raw: obj}, nil
	}
	typ, ok := typeVal.(string)
	if !ok {
		return UnknownServiceProviderUpdate{Raw: obj}, nil
	}
	switch typ {
	case "state":
		var inst *ServiceInstanceAddress
		if instVal, hasInst := obj.Get("instance"); hasInst {
			addr, err := typesDecodeInstanceAddress(instVal)
			if err != nil {
				return nil, err
			}
			inst = &addr
		}
		memberVal, okMember := obj.Get("member")
		member, isStr := memberVal.(string)
		seqVal, okSeq := obj.Get("sequence")
		seq, isInt := typesToInt(seqVal)
		opsVal, okOps := obj.Get("ops")
		opsArr, isArr := opsVal.([]any)
		if !okMember || !isStr || !okSeq || !isInt || !okOps || !isArr || opsArr == nil {
			return nil, js.NewTypeError("Invalid service state update")
		}
		ops := make(OpList, len(opsArr))
		for i, item := range opsArr {
			op, err := DecodeOp(item)
			if err != nil {
				return nil, err
			}
			ops[i] = op
		}
		return ServiceProviderUpdateState{
			Type:     "state",
			Instance: inst,
			Member:   member,
			Sequence: seq,
			Ops:      ops,
		}, nil
	case "reset":
		snapVal, okSnap := obj.Get("snapshot")
		if !okSnap {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		snap, err := typesDecodeSubscriptionSnapshot(snapVal)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case "unavailable":
		return ServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case "replaced":
		snapVal, okSnap := obj.Get("snapshot")
		if !okSnap {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		snap, err := typesDecodeInstanceSnapshot(snapVal)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReplaced{Type: "replaced", Snapshot: snap}, nil
	case "spawned":
		instVal, okInst := obj.Get("instance")
		if !okInst {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		inst, err := typesDecodeInstanceSnapshot(instVal)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case "closed":
		instVal, okInst := obj.Get("instance")
		if !okInst {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		addr, err := typesDecodeInstanceAddress(instVal)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateClosed{Type: "closed", Instance: addr}, nil
	default:
		return UnknownServiceProviderUpdate{Raw: obj}, nil
	}
}

func typesToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		if n < math.MinInt || n > math.MaxInt {
			return 0, false
		}
		return int(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, false
		}
		// Ensure exact integer representation bounds before converting to int.
		if n < -9007199254740991 || n > 9007199254740991 {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func typesDecodeInstanceAddress(v any) (ServiceInstanceAddress, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return ServiceInstanceAddress{}, js.NewTypeError("Invalid service instance address")
	}
	keyVal, okKey := obj.Get("key")
	key, isStr := keyVal.(string)
	genVal, okGen := obj.Get("generation")
	gen, isInt := typesToInt(genVal)
	if !okKey || !isStr || !okGen || !isInt {
		return ServiceInstanceAddress{}, js.NewTypeError("Invalid service instance address")
	}
	return ServiceInstanceAddress{Key: key, Generation: gen}, nil
}

func typesDecodeInstanceSnapshot(v any) (ServiceInstanceSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return ServiceInstanceSnapshot{}, js.NewTypeError("Invalid service instance snapshot")
	}
	var inst *ServiceInstanceAddress
	if instVal, hasInst := obj.Get("instance"); hasInst {
		addr, err := typesDecodeInstanceAddress(instVal)
		if err != nil {
			return ServiceInstanceSnapshot{}, err
		}
		inst = &addr
	}
	membersVal, okMembers := obj.Get("members")
	membersArr, isArr := membersVal.([]any)
	if !okMembers || !isArr || membersArr == nil {
		return ServiceInstanceSnapshot{}, js.NewTypeError("Invalid service instance snapshot")
	}
	members := make(ServiceMemberSnapshotList, len(membersArr))
	for i, item := range membersArr {
		m, err := DecodeServiceMemberSnapshot(item)
		if err != nil {
			return ServiceInstanceSnapshot{}, err
		}
		members[i] = m
	}
	return ServiceInstanceSnapshot{Instance: inst, Members: members}, nil
}

func typesDecodeSubscriptionSnapshot(v any) (ServiceSubscriptionSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return ServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
	}
	serviceIdVal, okId := obj.Get("serviceId")
	serviceId, isIdStr := serviceIdVal.(string)
	modeVal, okMode := obj.Get("mode")
	modeStr, isModeStr := modeVal.(string)
	instVal, okInst := obj.Get("instances")
	instArr, isArr := instVal.([]any)
	if !okId || !isIdStr || !okMode || !isModeStr || !okInst || !isArr || instArr == nil {
		return ServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
	}
	instances := make([]ServiceInstanceSnapshot, len(instArr))
	for i, item := range instArr {
		inst, err := typesDecodeInstanceSnapshot(item)
		if err != nil {
			return ServiceSubscriptionSnapshot{}, err
		}
		instances[i] = inst
	}
	return ServiceSubscriptionSnapshot{
		ServiceId: serviceId,
		Mode:      ServiceMode(modeStr),
		Instances: instances,
	}, nil
}

// ServiceCall is one remote invocation.
// Args are borrowed immutable values. Chord validates but does not clone them.
// Key order: serviceId, instance (omitted when nil), member, args.
// Args must be a non-nil slice when marshaled so it encodes as [].
type ServiceCall struct {
	ServiceId string                  `json:"serviceId"`
	Instance  *ServiceInstanceAddress `json:"instance,omitzero"`
	Member    string                  `json:"member"`
	Args      []JsonValue             `json:"args"`
}

// ServiceSubscription is one remote subscription.
// Snapshot is the atomic baseline. Later updates buffer until activation, with a
// full reset on pending delivery 101. Close accepts a nil Context when the caller
// omits it. Activate and Close report failures through error.
type ServiceSubscription interface {
	Snapshot() ServiceSubscriptionSnapshot
	Activate() error
	Close(call Context) error
}

// RemoteServiceTransport is the pluggable wire boundary consumed by a remote
// service binding. Implementations choose transport, framing, routing, and
// envelope encoding. Values crossing this boundary must remain strict JSON.
// Chord does not clone values or require a particular application wire protocol.
//
// Invoke's boolean is false when the method result is undefined, distinct from
// JSON null (a nil JsonValue with true).
type RemoteServiceTransport interface {
	Invoke(call ServiceCall, callContext Context) (value JsonValue, ok bool, err error)
	Subscribe(serviceId string, mode ServiceMode, listener func(update ServiceProviderUpdate, call Context), callContext Context) (ServiceSubscription, error)
}

// ServiceRef is the `{ readonly id: string }` reference used by binding and source options.
type ServiceRef struct {
	Id string `json:"id"`
}

// RemoteServiceBindingOptions configures a remote service binding.
// Key order: services, transport, bound, onError, assertAccess.
type RemoteServiceBindingOptions struct {
	Services     []ServiceRef           `json:"services"`
	Transport    RemoteServiceTransport `json:"transport"`
	Bound        *bool                  `json:"bound,omitzero"`
	OnError      func(error)            `json:"onError,omitzero"`
	AssertAccess func()                 `json:"assertAccess,omitzero"`
}

// RemoteServiceBinding is a consumer binding that can be toggled.
type RemoteServiceBinding interface {
	RemoteServices
	Rebind(bound bool, call Context) error
}

// FacetEnvironment is the setup surface of one facet.
// Observe does not return unsubscribe; the facet host owns the registration.
// ReplicatedState takes immutable ownership of an alias-free strict-JSON root.
// The caller must not mutate initial after the call.
// Callbacks that return a rejected promise or throw report that failure as error.
type FacetEnvironment interface {
	Use(service any) any
	Observe(service any, handler func(service any, call Context) error)
	Provide(service any, implementation any)
	ProvideMany(service any) any
	ReplicatedState(initial any) MutableReplicatedState[any]
	Own(disposal func() error)
	OnActivate(callback func() error)
	OnDeactivate(callback func() error)
}

// Facet is one application facet. Key order: id, setup.
// Setup must be synchronous; an error is a thrown setup failure.
type Facet struct {
	Id    string                           `json:"id"`
	Setup func(env FacetEnvironment) error `json:"setup"`
}

// RemoteServiceSourceOpenOptions is the argument of RemoteServiceSource.Open.
// Key order: services, assertAccess, onError.
type RemoteServiceSourceOpenOptions struct {
	Services     []ServiceRef `json:"services"`
	AssertAccess func()       `json:"assertAccess"`
	OnError      func(error)  `json:"onError"`
}

// RemoteServiceSource supplies remote services to a facet host.
// AcceptsUnavailableServices reports whether this currently unavailable source
// may provisionally own absent requirements.
type RemoteServiceSource interface {
	AcceptsUnavailableServices() bool
	Catalogue(call Context) ([]ServiceCatalogueEntry, error)
	Open(options *RemoteServiceSourceOpenOptions) RemoteServices
}

// FacetOptions configures a facet host.
// Key order: facets, serviceSources, onError.
type FacetOptions struct {
	Facets         []Facet               `json:"facets"`
	ServiceSources []RemoteServiceSource `json:"serviceSources,omitzero"`
	OnError        func(error)           `json:"onError,omitzero"`
}

// FacetHost is an active host for one complete set of facets.
// Services is the provider owned by services/provider.ts.
// Reload activates and replaces facets with matching IDs without disconnecting
// consumer service handles.
type FacetHost interface {
	Services() *RemoteServiceProvider
	Reload(facets []Facet) error
	Dispose() error
}

// LoadedFacets is the result of FacetLoader.Load.
// Key order: facets, dispose.
type LoadedFacets struct {
	Facets  []Facet      `json:"facets"`
	Dispose func() error `json:"dispose"`
}

// FacetLoader loads one set of facets.
type FacetLoader interface {
	Load() (*LoadedFacets, error)
}
