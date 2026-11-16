// Ported from packages/chord/src/services/state-codec.ts (pi v1.0.0).

package chord

import (
	"errors"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ServiceStateEncoder is the stateful operation encoder for every replicated state in one service subscription.
type ServiceStateEncoder interface {
	EncodeSnapshot(snapshot ServiceSubscriptionSnapshot) (WireServiceSubscriptionSnapshot, error)
	EncodeUpdate(update ServiceProviderUpdate) (WireServiceProviderUpdate, error)
}

// ServiceStateDecoder is the stateful operation decoder for every replicated state in one service subscription.
type ServiceStateDecoder interface {
	DecodeSnapshot(snapshot WireServiceSubscriptionSnapshot) (ServiceSubscriptionSnapshot, error)
	DecodeUpdate(update WireServiceProviderUpdate) (ServiceProviderUpdate, error)
}

type sscCodecEntry[C any] struct {
	instance *ServiceInstanceAddress
	codec    C
}

type sscStateCodecRegistry[C any] struct {
	mu      sync.Mutex
	create  func() C
	entries *omap.Map[string, sscCodecEntry[C]]
}

func sscNewStateCodecRegistry[C any](create func() C) *sscStateCodecRegistry[C] {
	return &sscStateCodecRegistry[C]{
		create:  create,
		entries: omap.NewMap[string, sscCodecEntry[C]](),
	}
}

func (r *sscStateCodecRegistry[C]) reset() {
	r.mu.Lock()
	r.entries.Clear()
	r.mu.Unlock()
}

func (r *sscStateCodecRegistry[C]) add(instance *ServiceInstanceAddress, member string) (C, error) {
	key := sscStateKey(instance, member)
	codec := r.create()
	var addrCopy *ServiceInstanceAddress
	if instance != nil {
		c := *instance
		addrCopy = &c
	}
	r.mu.Lock()
	r.entries.Set(key, sscCodecEntry[C]{instance: addrCopy, codec: codec})
	r.mu.Unlock()
	return codec, nil
}

func (r *sscStateCodecRegistry[C]) get(instance *ServiceInstanceAddress, member string) (C, error) {
	key := sscStateKey(instance, member)
	r.mu.Lock()
	entry, ok := r.entries.Get(key)
	r.mu.Unlock()
	if !ok {
		var zero C
		return zero, errors.New("Unknown service state")
	}
	return entry.codec, nil
}

func (r *sscStateCodecRegistry[C]) removeInstance(instance ServiceInstanceAddress) {
	r.mu.Lock()
	var toDelete []string
	for key, entry := range r.entries.All() {
		if entry.instance != nil && entry.instance.Key == instance.Key && entry.instance.Generation == instance.Generation {
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		r.entries.Delete(key)
	}
	r.mu.Unlock()
}

type sscEncoder struct {
	mu     sync.Mutex
	codecs *sscStateCodecRegistry[Encoder]
}

func (e *sscEncoder) EncodeSnapshot(snapshot ServiceSubscriptionSnapshot) (WireServiceSubscriptionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.encodeSnapshotLocked(snapshot)
}

func (e *sscEncoder) encodeSnapshotLocked(snapshot ServiceSubscriptionSnapshot) (WireServiceSubscriptionSnapshot, error) {
	e.codecs.reset()
	instances := make([]WireServiceInstanceSnapshot, len(snapshot.Instances))
	for i, instance := range snapshot.Instances {
		encoded, err := sscEncodeInstance(instance, e.codecs)
		if err != nil {
			return WireServiceSubscriptionSnapshot{}, err
		}
		instances[i] = encoded
	}
	return WireServiceSubscriptionSnapshot{
		ServiceId: snapshot.ServiceId,
		Mode:      snapshot.Mode,
		Instances: instances,
	}, nil
}

func (e *sscEncoder) EncodeUpdate(update ServiceProviderUpdate) (WireServiceProviderUpdate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch u := update.(type) {
	case ServiceProviderUpdateState:
		return e.encodeStateUpdateLocked(u)
	case *ServiceProviderUpdateState:
		return e.encodeStateUpdateLocked(*u)
	case ServiceProviderUpdateReset:
		snap, err := e.encodeSnapshotLocked(u.Snapshot)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case *ServiceProviderUpdateReset:
		snap, err := e.encodeSnapshotLocked(u.Snapshot)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case ServiceProviderUpdateUnavailable:
		return WireServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case *ServiceProviderUpdateUnavailable:
		return WireServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case ServiceProviderUpdateReplaced:
		e.codecs.reset()
		inst, err := sscEncodeInstance(u.Snapshot, e.codecs)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReplaced{Type: "replaced", Snapshot: inst}, nil
	case *ServiceProviderUpdateReplaced:
		e.codecs.reset()
		inst, err := sscEncodeInstance(u.Snapshot, e.codecs)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReplaced{Type: "replaced", Snapshot: inst}, nil
	case ServiceProviderUpdateSpawned:
		inst, err := sscEncodeInstance(u.Instance, e.codecs)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case *ServiceProviderUpdateSpawned:
		inst, err := sscEncodeInstance(u.Instance, e.codecs)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case ServiceProviderUpdateClosed:
		e.codecs.removeInstance(u.Instance)
		return WireServiceProviderUpdateClosed{Type: "closed", Instance: u.Instance}, nil
	case *ServiceProviderUpdateClosed:
		e.codecs.removeInstance(u.Instance)
		return WireServiceProviderUpdateClosed{Type: "closed", Instance: u.Instance}, nil
	case UnknownServiceProviderUpdate:
		return &UnknownWireServiceProviderUpdate{Raw: u.Raw}, nil
	case *UnknownServiceProviderUpdate:
		return &UnknownWireServiceProviderUpdate{Raw: u.Raw}, nil
	default:
		return nil, errors.New("Unknown service provider update")
	}
}

func (e *sscEncoder) encodeStateUpdateLocked(u ServiceProviderUpdateState) (WireServiceProviderUpdate, error) {
	var codec Encoder
	var err error
	if IsBase(u.Ops) {
		codec, err = e.codecs.add(u.Instance, u.Member)
	} else {
		codec, err = e.codecs.get(u.Instance, u.Member)
	}
	if err != nil {
		return nil, err
	}
	wireOps := codec.Encode(u.Ops)
	if wireOps == nil {
		wireOps = WireOpList{}
	}
	return WireServiceProviderUpdateState{
		Type:     "state",
		Instance: u.Instance,
		Member:   u.Member,
		Sequence: u.Sequence,
		Ops:      wireOps,
	}, nil
}

type sscDecoder struct {
	mu     sync.Mutex
	codecs *sscStateCodecRegistry[Decoder]
}

func (d *sscDecoder) DecodeSnapshot(snapshot WireServiceSubscriptionSnapshot) (ServiceSubscriptionSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.decodeSnapshotLocked(snapshot)
}

func (d *sscDecoder) decodeSnapshotLocked(snapshot WireServiceSubscriptionSnapshot) (ServiceSubscriptionSnapshot, error) {
	d.codecs.reset()
	instances := make([]ServiceInstanceSnapshot, len(snapshot.Instances))
	for i, instance := range snapshot.Instances {
		decoded, err := sscDecodeInstance(instance, d.codecs)
		if err != nil {
			return ServiceSubscriptionSnapshot{}, err
		}
		instances[i] = decoded
	}
	return ServiceSubscriptionSnapshot{
		ServiceId: snapshot.ServiceId,
		Mode:      snapshot.Mode,
		Instances: instances,
	}, nil
}

func (d *sscDecoder) DecodeUpdate(update WireServiceProviderUpdate) (ServiceProviderUpdate, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch u := update.(type) {
	case WireServiceProviderUpdateState:
		return d.decodeStateUpdateLocked(u)
	case *WireServiceProviderUpdateState:
		return d.decodeStateUpdateLocked(*u)
	case WireServiceProviderUpdateReset:
		snap, err := d.decodeSnapshotLocked(u.Snapshot)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case *WireServiceProviderUpdateReset:
		snap, err := d.decodeSnapshotLocked(u.Snapshot)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case WireServiceProviderUpdateUnavailable:
		return ServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case *WireServiceProviderUpdateUnavailable:
		return ServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case WireServiceProviderUpdateReplaced:
		d.codecs.reset()
		inst, err := sscDecodeInstance(u.Snapshot, d.codecs)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReplaced{Type: "replaced", Snapshot: inst}, nil
	case *WireServiceProviderUpdateReplaced:
		d.codecs.reset()
		inst, err := sscDecodeInstance(u.Snapshot, d.codecs)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateReplaced{Type: "replaced", Snapshot: inst}, nil
	case WireServiceProviderUpdateSpawned:
		inst, err := sscDecodeInstance(u.Instance, d.codecs)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case *WireServiceProviderUpdateSpawned:
		inst, err := sscDecodeInstance(u.Instance, d.codecs)
		if err != nil {
			return nil, err
		}
		return ServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case WireServiceProviderUpdateClosed:
		d.codecs.removeInstance(u.Instance)
		return ServiceProviderUpdateClosed{Type: "closed", Instance: u.Instance}, nil
	case *WireServiceProviderUpdateClosed:
		d.codecs.removeInstance(u.Instance)
		return ServiceProviderUpdateClosed{Type: "closed", Instance: u.Instance}, nil
	case *UnknownWireServiceProviderUpdate:
		return UnknownServiceProviderUpdate{Raw: u.Raw}, nil
	default:
		return nil, errors.New("Unknown wire service provider update")
	}
}

func (d *sscDecoder) decodeStateUpdateLocked(u WireServiceProviderUpdateState) (ServiceProviderUpdate, error) {
	var codec Decoder
	var err error
	if IsBase(u.Ops) {
		codec, err = d.codecs.add(u.Instance, u.Member)
	} else {
		codec, err = d.codecs.get(u.Instance, u.Member)
	}
	if err != nil {
		return nil, err
	}
	ops, err := codec.Decode(u.Ops)
	if err != nil {
		return nil, err
	}
	if ops == nil {
		ops = OpList{}
	}
	return ServiceProviderUpdateState{
		Type:     "state",
		Instance: u.Instance,
		Member:   u.Member,
		Sequence: u.Sequence,
		Ops:      ops,
	}, nil
}

// CreateServiceStateEncoder returns a stateful encoder for one subscription.
func CreateServiceStateEncoder() ServiceStateEncoder {
	return &sscEncoder{
		codecs: sscNewStateCodecRegistry(NewEncoder),
	}
}

// CreateServiceStateDecoder returns a stateful decoder for one subscription.
func CreateServiceStateDecoder() ServiceStateDecoder {
	return &sscDecoder{
		codecs: sscNewStateCodecRegistry(NewDecoder),
	}
}

func sscEncodeInstance(instance ServiceInstanceSnapshot, codecs *sscStateCodecRegistry[Encoder]) (WireServiceInstanceSnapshot, error) {
	members := make(WireServiceMemberSnapshotList, len(instance.Members))
	for i, member := range instance.Members {
		switch m := member.(type) {
		case ServiceMemberSnapshotMethod:
			members[i] = WireServiceMemberSnapshotMethod{Name: m.Name, Kind: m.Kind}
		case *ServiceMemberSnapshotMethod:
			members[i] = WireServiceMemberSnapshotMethod{Name: m.Name, Kind: m.Kind}
		case ServiceMemberSnapshotState:
			codec, err := codecs.add(instance.Instance, m.Name)
			if err != nil {
				return WireServiceInstanceSnapshot{}, err
			}
			wireOps := codec.Encode(m.Ops)
			if wireOps == nil {
				wireOps = WireOpList{}
			}
			members[i] = WireServiceMemberSnapshotState{
				Name:     m.Name,
				Kind:     m.Kind,
				Sequence: m.Sequence,
				Ops:      wireOps,
			}
		case *ServiceMemberSnapshotState:
			codec, err := codecs.add(instance.Instance, m.Name)
			if err != nil {
				return WireServiceInstanceSnapshot{}, err
			}
			wireOps := codec.Encode(m.Ops)
			if wireOps == nil {
				wireOps = WireOpList{}
			}
			members[i] = WireServiceMemberSnapshotState{
				Name:     m.Name,
				Kind:     m.Kind,
				Sequence: m.Sequence,
				Ops:      wireOps,
			}
		case UnknownServiceMemberSnapshot:
			members[i] = &UnknownWireServiceMemberSnapshot{Raw: m.Raw}
		case *UnknownServiceMemberSnapshot:
			members[i] = &UnknownWireServiceMemberSnapshot{Raw: m.Raw}
		default:
			return WireServiceInstanceSnapshot{}, errors.New("Unknown service member snapshot")
		}
	}
	return WireServiceInstanceSnapshot{
		Instance: instance.Instance,
		Members:  members,
	}, nil
}

func sscDecodeInstance(instance WireServiceInstanceSnapshot, codecs *sscStateCodecRegistry[Decoder]) (ServiceInstanceSnapshot, error) {
	members := make(ServiceMemberSnapshotList, len(instance.Members))
	for i, member := range instance.Members {
		switch m := member.(type) {
		case WireServiceMemberSnapshotMethod:
			members[i] = ServiceMemberSnapshotMethod{Name: m.Name, Kind: m.Kind}
		case *WireServiceMemberSnapshotMethod:
			members[i] = ServiceMemberSnapshotMethod{Name: m.Name, Kind: m.Kind}
		case WireServiceMemberSnapshotState:
			codec, err := codecs.add(instance.Instance, m.Name)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
			ops, err := codec.Decode(m.Ops)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
			if ops == nil {
				ops = OpList{}
			}
			members[i] = ServiceMemberSnapshotState{
				Name:     m.Name,
				Kind:     m.Kind,
				Sequence: m.Sequence,
				Ops:      ops,
			}
		case *WireServiceMemberSnapshotState:
			codec, err := codecs.add(instance.Instance, m.Name)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
			ops, err := codec.Decode(m.Ops)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
			if ops == nil {
				ops = OpList{}
			}
			members[i] = ServiceMemberSnapshotState{
				Name:     m.Name,
				Kind:     m.Kind,
				Sequence: m.Sequence,
				Ops:      ops,
			}
		case *UnknownWireServiceMemberSnapshot:
			members[i] = UnknownServiceMemberSnapshot{Raw: m.Raw}
		default:
			return ServiceInstanceSnapshot{}, errors.New("Unknown wire service member snapshot")
		}
	}
	return ServiceInstanceSnapshot{
		Instance: instance.Instance,
		Members:  members,
	}, nil
}

func sscStateKey(instance *ServiceInstanceAddress, member string) string {
	var raw []byte
	if instance == nil {
		raw, _ = jsonx.Marshal([]any{nil, nil, member})
	} else {
		raw, _ = jsonx.Marshal([]any{instance.Key, float64(instance.Generation), member})
	}
	return string(raw)
}
