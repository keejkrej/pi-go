// Ported from packages/chord/src/services/wire.ts (pi v1.0.0).

package chord

import (
	"math"
	"slices"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	swServiceControlId         = "$chord.service"
	swServiceCatalogueMember   = "catalogue"
	swServiceSubscribeMember   = "subscribe"
	swServiceUnsubscribeMember = "unsubscribe"
)

// WireServiceMemberSnapshot is one member in a wire instance snapshot.
type WireServiceMemberSnapshot interface {
	isWireServiceMemberSnapshot()
}

// WireServiceMemberSnapshotMethod is a method member (kind "method").
type WireServiceMemberSnapshotMethod struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (WireServiceMemberSnapshotMethod) isWireServiceMemberSnapshot() {}

// WireServiceMemberSnapshotState is a state member (kind "state").
type WireServiceMemberSnapshotState struct {
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Sequence int        `json:"sequence"`
	Ops      WireOpList `json:"ops"`
}

func (WireServiceMemberSnapshotState) isWireServiceMemberSnapshot() {}

// UnknownWireServiceMemberSnapshot keeps an unrecognized member discriminator verbatim.
type UnknownWireServiceMemberSnapshot struct {
	Raw *jsonx.Object
}

func (*UnknownWireServiceMemberSnapshot) isWireServiceMemberSnapshot() {}

func (u UnknownWireServiceMemberSnapshot) MarshalJSON() ([]byte, error) {
	if u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownWireServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
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
		return js.NewTypeError("unknown wire service member snapshot is not an object")
	}
	u.Raw = obj
	return nil
}

// WireServiceMemberSnapshotList is the members array of a wire instance snapshot.
type WireServiceMemberSnapshotList []WireServiceMemberSnapshot

func (l WireServiceMemberSnapshotList) MarshalJSON() ([]byte, error) {
	out := make([]any, len(l))
	for i, member := range l {
		if sm, ok := member.(WireServiceMemberSnapshotState); ok && sm.Ops == nil {
			sm.Ops = WireOpList{}
			out[i] = sm
		} else if smp, ok := member.(*WireServiceMemberSnapshotState); ok && smp != nil && smp.Ops == nil {
			copyVal := *smp
			copyVal.Ops = WireOpList{}
			out[i] = copyVal
		} else {
			out[i] = member
		}
	}
	return jsonx.Marshal(out)
}

func (l *WireServiceMemberSnapshotList) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	arr, ok := val.([]any)
	if !ok || arr == nil {
		return js.NewTypeError("wire service member snapshot list is not an array")
	}
	out := make(WireServiceMemberSnapshotList, len(arr))
	for i, item := range arr {
		m, err := DecodeWireServiceMemberSnapshot(item)
		if err != nil {
			return err
		}
		out[i] = m
	}
	*l = out
	return nil
}

// UnmarshalWireServiceMemberSnapshot decodes one wire member snapshot.
func UnmarshalWireServiceMemberSnapshot(data []byte) (WireServiceMemberSnapshot, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeWireServiceMemberSnapshot(val)
}

// DecodeWireServiceMemberSnapshot decodes one wire member snapshot from a jsonx value.
func DecodeWireServiceMemberSnapshot(v any) (WireServiceMemberSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, js.NewTypeError("wire service member snapshot is not an object")
	}
	kindVal, ok := obj.Get("kind")
	if !ok {
		return &UnknownWireServiceMemberSnapshot{Raw: obj}, nil
	}
	kind, ok := kindVal.(string)
	if !ok {
		return &UnknownWireServiceMemberSnapshot{Raw: obj}, nil
	}
	switch kind {
	case "method":
		nameVal, okName := obj.Get("name")
		name, isStr := nameVal.(string)
		if !okName || !isStr {
			return nil, js.NewTypeError("Invalid service method snapshot")
		}
		return WireServiceMemberSnapshotMethod{Name: name, Kind: "method"}, nil
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
		ops := make(WireOpList, len(opsArr))
		for i, item := range opsArr {
			op, err := DecodeWireOp(item)
			if err != nil {
				return nil, err
			}
			ops[i] = op
		}
		return WireServiceMemberSnapshotState{
			Name:     name,
			Kind:     "state",
			Sequence: seq,
			Ops:      ops,
		}, nil
	default:
		return &UnknownWireServiceMemberSnapshot{Raw: obj}, nil
	}
}

// WireServiceInstanceSnapshot is one wire service instance.
type WireServiceInstanceSnapshot struct {
	Instance *ServiceInstanceAddress       `json:"instance,omitzero"`
	Members  WireServiceMemberSnapshotList `json:"members"`
}

// WireServiceSubscriptionSnapshot is a wire subscription baseline.
type WireServiceSubscriptionSnapshot struct {
	ServiceId string                        `json:"serviceId"`
	Mode      ServiceMode                   `json:"mode"`
	Instances []WireServiceInstanceSnapshot `json:"instances"`
}

// WireServiceProviderUpdate is one wire provider publication.
type WireServiceProviderUpdate interface {
	isWireServiceProviderUpdate()
}

// WireServiceProviderUpdateState is a state revision. Instance is omitted for a singleton.
type WireServiceProviderUpdateState struct {
	Type     string                  `json:"type"`
	Instance *ServiceInstanceAddress `json:"instance,omitzero"`
	Member   string                  `json:"member"`
	Sequence int                     `json:"sequence"`
	Ops      WireOpList              `json:"ops"`
}

func (WireServiceProviderUpdateState) isWireServiceProviderUpdate() {}

// WireServiceProviderUpdateReset is a full subscription rebaseline.
type WireServiceProviderUpdateReset struct {
	Type     string                          `json:"type"`
	Snapshot WireServiceSubscriptionSnapshot `json:"snapshot"`
}

func (WireServiceProviderUpdateReset) isWireServiceProviderUpdate() {}

// WireServiceProviderUpdateUnavailable marks the singleton provider unavailable.
type WireServiceProviderUpdateUnavailable struct {
	Type string `json:"type"`
}

func (WireServiceProviderUpdateUnavailable) isWireServiceProviderUpdate() {}

// WireServiceProviderUpdateReplaced replaces one singleton without an unavailable gap.
type WireServiceProviderUpdateReplaced struct {
	Type     string                      `json:"type"`
	Snapshot WireServiceInstanceSnapshot `json:"snapshot"`
}

func (WireServiceProviderUpdateReplaced) isWireServiceProviderUpdate() {}

// WireServiceProviderUpdateSpawned announces one keyed instance.
type WireServiceProviderUpdateSpawned struct {
	Type     string                      `json:"type"`
	Instance WireServiceInstanceSnapshot `json:"instance"`
}

func (WireServiceProviderUpdateSpawned) isWireServiceProviderUpdate() {}

// WireServiceProviderUpdateClosed announces one keyed instance closed.
type WireServiceProviderUpdateClosed struct {
	Type     string                 `json:"type"`
	Instance ServiceInstanceAddress `json:"instance"`
}

func (WireServiceProviderUpdateClosed) isWireServiceProviderUpdate() {}

// UnknownWireServiceProviderUpdate keeps an unrecognized update discriminator verbatim.
type UnknownWireServiceProviderUpdate struct {
	Raw *jsonx.Object
}

func (*UnknownWireServiceProviderUpdate) isWireServiceProviderUpdate() {}

func (u UnknownWireServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	if u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownWireServiceProviderUpdate) UnmarshalJSON(data []byte) error {
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
		return js.NewTypeError("unknown wire service provider update is not an object")
	}
	u.Raw = obj
	return nil
}

// UnmarshalWireServiceProviderUpdate decodes one wire provider update.
func UnmarshalWireServiceProviderUpdate(data []byte) (WireServiceProviderUpdate, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeWireServiceProviderUpdate(val)
}

// DecodeWireServiceProviderUpdate decodes one wire provider update from a jsonx value.
func DecodeWireServiceProviderUpdate(v any) (WireServiceProviderUpdate, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, js.NewTypeError("wire service provider update is not an object")
	}
	typeVal, ok := obj.Get("type")
	if !ok {
		return &UnknownWireServiceProviderUpdate{Raw: obj}, nil
	}
	typ, ok := typeVal.(string)
	if !ok {
		return &UnknownWireServiceProviderUpdate{Raw: obj}, nil
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
		ops := make(WireOpList, len(opsArr))
		for i, item := range opsArr {
			op, err := DecodeWireOp(item)
			if err != nil {
				return nil, err
			}
			ops[i] = op
		}
		return WireServiceProviderUpdateState{
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
		snap, err := swDecodeSubscriptionSnapshot(snapVal)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReset{Type: "reset", Snapshot: snap}, nil
	case "unavailable":
		return WireServiceProviderUpdateUnavailable{Type: "unavailable"}, nil
	case "replaced":
		snapVal, okSnap := obj.Get("snapshot")
		if !okSnap {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		snap, err := swDecodeInstanceSnapshot(snapVal)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateReplaced{Type: "replaced", Snapshot: snap}, nil
	case "spawned":
		instVal, okInst := obj.Get("instance")
		if !okInst {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		inst, err := swDecodeInstanceSnapshot(instVal)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateSpawned{Type: "spawned", Instance: inst}, nil
	case "closed":
		instVal, okInst := obj.Get("instance")
		if !okInst {
			return nil, js.NewTypeError("Invalid service provider update")
		}
		addr, err := typesDecodeInstanceAddress(instVal)
		if err != nil {
			return nil, err
		}
		return WireServiceProviderUpdateClosed{Type: "closed", Instance: addr}, nil
	default:
		return &UnknownWireServiceProviderUpdate{Raw: obj}, nil
	}
}

// ServiceControlCall is a decoded $chord.service call. It is not a persisted JSON document.
type ServiceControlCall interface {
	isServiceControlCall()
}

// ServiceControlCallCatalogue is the catalogue control call.
type ServiceControlCallCatalogue struct {
	Type string `json:"type"`
}

func (ServiceControlCallCatalogue) isServiceControlCall() {}

// ServiceControlCallSubscribe is the subscribe control call.
type ServiceControlCallSubscribe struct {
	Type           string      `json:"type"`
	SubscriptionId string      `json:"subscriptionId"`
	ServiceId      string      `json:"serviceId"`
	Mode           ServiceMode `json:"mode"`
}

func (ServiceControlCallSubscribe) isServiceControlCall() {}

// ServiceControlCallUnsubscribe is the unsubscribe control call.
type ServiceControlCallUnsubscribe struct {
	Type           string `json:"type"`
	SubscriptionId string `json:"subscriptionId"`
}

func (ServiceControlCallUnsubscribe) isServiceControlCall() {}

// CreateServiceCatalogueCall builds the catalogue control call.
func CreateServiceCatalogueCall() ServiceCall {
	return ServiceCall{
		ServiceId: swServiceControlId,
		Member:    swServiceCatalogueMember,
		Args:      []JsonValue{},
	}
}

// CreateServiceSubscribeCall builds the subscribe control call.
func CreateServiceSubscribeCall(subscriptionId string, serviceId string, mode ServiceMode) ServiceCall {
	return ServiceCall{
		ServiceId: swServiceControlId,
		Member:    swServiceSubscribeMember,
		Args:      []JsonValue{subscriptionId, serviceId, string(mode)},
	}
}

// CreateServiceUnsubscribeCall builds the unsubscribe control call.
func CreateServiceUnsubscribeCall(subscriptionId string) ServiceCall {
	return ServiceCall{
		ServiceId: swServiceControlId,
		Member:    swServiceUnsubscribeMember,
		Args:      []JsonValue{subscriptionId},
	}
}

// DecodeServiceControlCall decodes a control call. A nil result means the call is not a control call (TS undefined).
func DecodeServiceControlCall(call ServiceCall) ServiceControlCall {
	if call.ServiceId != swServiceControlId || call.Instance != nil {
		return nil
	}
	if call.Member == swServiceCatalogueMember && len(call.Args) == 0 {
		return ServiceControlCallCatalogue{Type: "catalogue"}
	}
	if call.Member == swServiceSubscribeMember && len(call.Args) == 3 &&
		swIsId(call.Args[0]) && swIsId(call.Args[1]) && swIsMode(call.Args[2]) {
		subID, _ := call.Args[0].(string)
		svcID, _ := call.Args[1].(string)
		return ServiceControlCallSubscribe{
			Type:           "subscribe",
			SubscriptionId: subID,
			ServiceId:      svcID,
			Mode:           swToMode(call.Args[2]),
		}
	}
	if call.Member == swServiceUnsubscribeMember && len(call.Args) == 1 && swIsId(call.Args[0]) {
		subID, _ := call.Args[0].(string)
		return ServiceControlCallUnsubscribe{
			Type:           "unsubscribe",
			SubscriptionId: subID,
		}
	}
	return nil
}

// ParseServiceCall validates a service call.
func ParseServiceCall(value any) (ServiceCall, error) {
	switch c := value.(type) {
	case ServiceCall:
		if err := swValidateTypedCall(c); err != nil {
			return ServiceCall{}, err
		}
		return c, nil
	case *ServiceCall:
		if c == nil {
			return ServiceCall{}, js.NewTypeError("Invalid service call")
		}
		if err := swValidateTypedCall(*c); err != nil {
			return ServiceCall{}, err
		}
		return *c, nil
	}
	call, err := swRecord(value, "service call")
	if err != nil {
		return ServiceCall{}, err
	}
	if err := swAssertKeys(call, []string{"serviceId", "member", "args"}, []string{"instance"}, "service call"); err != nil {
		return ServiceCall{}, err
	}
	serviceIdVal, _ := call.Get("serviceId")
	memberVal, _ := call.Get("member")
	argsVal, _ := call.Get("args")
	argsArr, isArr := argsVal.([]any)
	if !swIsId(serviceIdVal) || !swIsId(memberVal) || !isArr || argsArr == nil {
		return ServiceCall{}, js.NewTypeError("Invalid service call")
	}
	var inst *ServiceInstanceAddress
	if instVal, hasInst := call.Get("instance"); hasInst {
		if err := swAssertAddress(instVal); err != nil {
			return ServiceCall{}, err
		}
		addr, err := typesDecodeInstanceAddress(instVal)
		if err != nil {
			return ServiceCall{}, err
		}
		inst = &addr
	}
	return ServiceCall{
		ServiceId: serviceIdVal.(string),
		Instance:  inst,
		Member:    memberVal.(string),
		Args:      argsArr,
	}, nil
}

// ParseServiceCatalogue validates a service catalogue.
func ParseServiceCatalogue(value any) ([]ServiceCatalogueEntry, error) {
	if entries, ok := value.([]ServiceCatalogueEntry); ok {
		if entries == nil {
			return nil, js.NewTypeError("Invalid service catalogue")
		}
		seen := omap.NewSet[string]()
		for _, entry := range entries {
			if !swIsId(entry.ServiceId) || !swIsMode(entry.Mode) || seen.Has(entry.ServiceId) {
				return nil, js.NewTypeError("Invalid service catalogue")
			}
			seen.Add(entry.ServiceId)
		}
		return entries, nil
	}
	arr, ok := value.([]any)
	if !ok || arr == nil {
		return nil, js.NewTypeError("Invalid service catalogue")
	}
	ids := omap.NewSet[string]()
	out := make([]ServiceCatalogueEntry, len(arr))
	for i, candidate := range arr {
		entry, err := swRecord(candidate, "service catalogue entry")
		if err != nil {
			return nil, err
		}
		if err := swAssertKeys(entry, []string{"serviceId", "mode"}, nil, "service catalogue entry"); err != nil {
			return nil, err
		}
		serviceIdVal, _ := entry.Get("serviceId")
		modeVal, _ := entry.Get("mode")
		if !swIsId(serviceIdVal) || !swIsMode(modeVal) || ids.Has(serviceIdVal.(string)) {
			return nil, js.NewTypeError("Invalid service catalogue")
		}
		svcID := serviceIdVal.(string)
		ids.Add(svcID)
		out[i] = ServiceCatalogueEntry{
			ServiceId: svcID,
			Mode:      swToMode(modeVal),
		}
	}
	return out, nil
}

// ParseServiceSubscriptionSnapshot validates a decoded subscription snapshot.
func ParseServiceSubscriptionSnapshot(value any) (ServiceSubscriptionSnapshot, error) {
	switch s := value.(type) {
	case ServiceSubscriptionSnapshot:
		if err := swValidateTypedSubscriptionSnapshot(s); err != nil {
			return ServiceSubscriptionSnapshot{}, err
		}
		return s, nil
	case *ServiceSubscriptionSnapshot:
		if s == nil {
			return ServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
		}
		if err := swValidateTypedSubscriptionSnapshot(*s); err != nil {
			return ServiceSubscriptionSnapshot{}, err
		}
		return *s, nil
	}
	if err := swAssertSubscriptionSnapshot(value, swAssertOpAny); err != nil {
		return ServiceSubscriptionSnapshot{}, err
	}
	return typesDecodeSubscriptionSnapshot(value)
}

// ParseWireServiceSubscriptionSnapshot validates a wire subscription snapshot.
func ParseWireServiceSubscriptionSnapshot(value any) (WireServiceSubscriptionSnapshot, error) {
	switch s := value.(type) {
	case WireServiceSubscriptionSnapshot:
		if err := swValidateTypedWireSubscriptionSnapshot(s); err != nil {
			return WireServiceSubscriptionSnapshot{}, err
		}
		return s, nil
	case *WireServiceSubscriptionSnapshot:
		if s == nil {
			return WireServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
		}
		if err := swValidateTypedWireSubscriptionSnapshot(*s); err != nil {
			return WireServiceSubscriptionSnapshot{}, err
		}
		return *s, nil
	}
	if err := swAssertSubscriptionSnapshot(value, swAssertWireOpAny); err != nil {
		return WireServiceSubscriptionSnapshot{}, err
	}
	return swDecodeSubscriptionSnapshot(value)
}

// ParseServiceProviderUpdate validates a decoded provider update.
func ParseServiceProviderUpdate(value any) (ServiceProviderUpdate, error) {
	if upd, ok := value.(ServiceProviderUpdate); ok && upd != nil {
		if err := swValidateTypedProviderUpdate(upd); err != nil {
			return nil, err
		}
		return upd, nil
	}
	if err := swAssertProviderUpdate(value, swAssertOpAny); err != nil {
		return nil, err
	}
	return DecodeServiceProviderUpdate(value)
}

// ParseWireServiceProviderUpdate validates a wire provider update.
func ParseWireServiceProviderUpdate(value any) (WireServiceProviderUpdate, error) {
	if upd, ok := value.(WireServiceProviderUpdate); ok && upd != nil {
		if err := swValidateTypedWireProviderUpdate(upd); err != nil {
			return nil, err
		}
		return upd, nil
	}
	if err := swAssertProviderUpdate(value, swAssertWireOpAny); err != nil {
		return nil, err
	}
	return DecodeWireServiceProviderUpdate(value)
}

func swAssertOpAny(value any) error {
	_, err := AssertValidOp(value)
	return err
}

func swAssertWireOpAny(value any) error {
	_, err := AssertValidWireOp(value)
	return err
}

func swAssertSubscriptionSnapshot(value any, assertOp func(any) error) error {
	snapshot, err := swRecord(value, "service subscription snapshot")
	if err != nil {
		return err
	}
	if err := swAssertKeys(snapshot, []string{"serviceId", "mode", "instances"}, nil, "service subscription snapshot"); err != nil {
		return err
	}
	serviceIdVal, _ := snapshot.Get("serviceId")
	modeVal, _ := snapshot.Get("mode")
	instVal, _ := snapshot.Get("instances")
	instArr, isArr := instVal.([]any)
	if !swIsId(serviceIdVal) || !swIsMode(modeVal) || !isArr || instArr == nil {
		return js.NewTypeError("Invalid service subscription snapshot")
	}
	for _, instance := range instArr {
		if err := swAssertInstance(instance, assertOp); err != nil {
			return err
		}
	}
	return nil
}

func swAssertProviderUpdate(value any, assertOp func(any) error) error {
	update, err := swRecord(value, "service provider update")
	if err != nil {
		return err
	}
	typVal, _ := update.Get("type")
	typ, _ := typVal.(string)
	switch typ {
	case "state":
		if err := swAssertKeys(update, []string{"type", "member", "sequence", "ops"}, []string{"instance"}, "state update"); err != nil {
			return err
		}
		memberVal, _ := update.Get("member")
		seqVal, _ := update.Get("sequence")
		opsVal, _ := update.Get("ops")
		opsArr, isArr := opsVal.([]any)
		if !swIsId(memberVal) || !swIsInteger(seqVal, 1) || !isArr || opsArr == nil {
			return js.NewTypeError("Invalid service state update")
		}
		if instVal, hasInst := update.Get("instance"); hasInst {
			if err := swAssertAddress(instVal); err != nil {
				return err
			}
		}
		for _, op := range opsArr {
			if err := assertOp(op); err != nil {
				return err
			}
		}
		return nil
	case "reset":
		if err := swAssertKeys(update, []string{"type", "snapshot"}, nil, "reset update"); err != nil {
			return err
		}
		snapVal, _ := update.Get("snapshot")
		if err := swAssertSubscriptionSnapshot(snapVal, assertOp); err != nil {
			return err
		}
		snapObj := snapVal.(*jsonx.Object)
		instVal, _ := snapObj.Get("instances")
		for _, rawInst := range instVal.([]any) {
			instObj := rawInst.(*jsonx.Object)
			membersVal, _ := instObj.Get("members")
			for _, rawMember := range membersVal.([]any) {
				memberObj := rawMember.(*jsonx.Object)
				kindVal, _ := memberObj.Get("kind")
				if kindVal == "state" {
					opsVal, _ := memberObj.Get("ops")
					opsArr := opsVal.([]any)
					if len(opsArr) != 1 {
						return js.NewTypeError("Service reset must contain full root replacements")
					}
					firstOp, isTuple := opsArr[0].([]any)
					if !isTuple || len(firstOp) == 0 || firstOp[0] != "r" {
						return js.NewTypeError("Service reset must contain full root replacements")
					}
				}
			}
		}
		return nil
	case "unavailable":
		return swAssertKeys(update, []string{"type"}, nil, "unavailable update")
	case "replaced":
		if err := swAssertKeys(update, []string{"type", "snapshot"}, nil, "replacement update"); err != nil {
			return err
		}
		snapVal, _ := update.Get("snapshot")
		return swAssertInstance(snapVal, assertOp)
	case "spawned":
		if err := swAssertKeys(update, []string{"type", "instance"}, nil, "spawn update"); err != nil {
			return err
		}
		instVal, _ := update.Get("instance")
		return swAssertInstance(instVal, assertOp)
	case "closed":
		if err := swAssertKeys(update, []string{"type", "instance"}, nil, "close update"); err != nil {
			return err
		}
		instVal, _ := update.Get("instance")
		return swAssertAddress(instVal)
	default:
		return js.NewTypeError("Invalid service provider update")
	}
}

func swAssertInstance(value any, assertOp func(any) error) error {
	instance, err := swRecord(value, "service instance snapshot")
	if err != nil {
		return err
	}
	if err := swAssertKeys(instance, []string{"members"}, []string{"instance"}, "service instance snapshot"); err != nil {
		return err
	}
	if instVal, hasInst := instance.Get("instance"); hasInst {
		if err := swAssertAddress(instVal); err != nil {
			return err
		}
	}
	membersVal, _ := instance.Get("members")
	membersArr, isArr := membersVal.([]any)
	if !isArr || membersArr == nil {
		return js.NewTypeError("Invalid service instance snapshot")
	}
	for _, candidate := range membersArr {
		member, err := swRecord(candidate, "service member snapshot")
		if err != nil {
			return err
		}
		kindVal, _ := member.Get("kind")
		if kindVal == "method" {
			if err := swAssertKeys(member, []string{"name", "kind"}, nil, "service method snapshot"); err != nil {
				return err
			}
			nameVal, _ := member.Get("name")
			if !swIsId(nameVal) {
				return js.NewTypeError("Invalid service method snapshot")
			}
			continue
		}
		if kindVal == "state" {
			if err := swAssertKeys(member, []string{"name", "kind", "sequence", "ops"}, nil, "service state snapshot"); err != nil {
				return err
			}
			nameVal, _ := member.Get("name")
			seqVal, _ := member.Get("sequence")
			opsVal, _ := member.Get("ops")
			opsArr, isOpsArr := opsVal.([]any)
			if !swIsId(nameVal) || !swIsInteger(seqVal, 0) || !isOpsArr || opsArr == nil {
				return js.NewTypeError("Invalid service state snapshot")
			}
			for _, op := range opsArr {
				if err := assertOp(op); err != nil {
					return err
				}
			}
			continue
		}
		return js.NewTypeError("Invalid service member snapshot")
	}
	return nil
}

func swAssertAddress(value any) error {
	address, err := swRecord(value, "service instance address")
	if err != nil {
		return err
	}
	if err := swAssertKeys(address, []string{"key", "generation"}, nil, "service instance address"); err != nil {
		return err
	}
	keyVal, _ := address.Get("key")
	genVal, _ := address.Get("generation")
	if !swIsId(keyVal) || !swIsInteger(genVal, 1) {
		return js.NewTypeError("Invalid service instance address")
	}
	return nil
}

func swRecord(value any, description string) (*jsonx.Object, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, js.NewTypeError("Invalid " + description)
	}
	return obj, nil
}

func swAssertKeys(value *jsonx.Object, required []string, optional []string, description string) error {
	for _, key := range required {
		if !value.Has(key) {
			return js.NewTypeError("Invalid " + description)
		}
	}
	for _, key := range value.Keys() {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return js.NewTypeError("Invalid " + description)
		}
	}
	return nil
}

func swIsId(value any) bool {
	s, ok := value.(string)
	return ok && len(s) > 0
}

func swIsMode(value any) bool {
	switch m := value.(type) {
	case ServiceMode:
		return m == ServiceModeSingleton || m == ServiceModeKeyed
	case string:
		return m == string(ServiceModeSingleton) || m == string(ServiceModeKeyed)
	default:
		return false
	}
}

func swToMode(value any) ServiceMode {
	switch m := value.(type) {
	case ServiceMode:
		return m
	case string:
		return ServiceMode(m)
	default:
		return ""
	}
}

func swIsInteger(value any, minimum int) bool {
	switch n := value.(type) {
	case int:
		return n >= minimum
	case int64:
		return n >= int64(minimum)
	case float64:
		return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n && n >= float64(minimum)
	default:
		return false
	}
}

func swDecodeInstanceSnapshot(v any) (WireServiceInstanceSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return WireServiceInstanceSnapshot{}, js.NewTypeError("Invalid service instance snapshot")
	}
	var inst *ServiceInstanceAddress
	if instVal, hasInst := obj.Get("instance"); hasInst {
		addr, err := typesDecodeInstanceAddress(instVal)
		if err != nil {
			return WireServiceInstanceSnapshot{}, err
		}
		inst = &addr
	}
	membersVal, okMembers := obj.Get("members")
	membersArr, isArr := membersVal.([]any)
	if !okMembers || !isArr || membersArr == nil {
		return WireServiceInstanceSnapshot{}, js.NewTypeError("Invalid service instance snapshot")
	}
	members := make(WireServiceMemberSnapshotList, len(membersArr))
	for i, item := range membersArr {
		m, err := DecodeWireServiceMemberSnapshot(item)
		if err != nil {
			return WireServiceInstanceSnapshot{}, err
		}
		members[i] = m
	}
	return WireServiceInstanceSnapshot{Instance: inst, Members: members}, nil
}

func swDecodeSubscriptionSnapshot(v any) (WireServiceSubscriptionSnapshot, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return WireServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
	}
	serviceIdVal, okId := obj.Get("serviceId")
	serviceId, isIdStr := serviceIdVal.(string)
	modeVal, okMode := obj.Get("mode")
	modeStr, isModeStr := modeVal.(string)
	instVal, okInst := obj.Get("instances")
	instArr, isArr := instVal.([]any)
	if !okId || !isIdStr || !okMode || !isModeStr || !okInst || !isArr || instArr == nil {
		return WireServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
	}
	instances := make([]WireServiceInstanceSnapshot, len(instArr))
	for i, item := range instArr {
		inst, err := swDecodeInstanceSnapshot(item)
		if err != nil {
			return WireServiceSubscriptionSnapshot{}, err
		}
		instances[i] = inst
	}
	return WireServiceSubscriptionSnapshot{
		ServiceId: serviceId,
		Mode:      ServiceMode(modeStr),
		Instances: instances,
	}, nil
}

func swValidateTypedCall(c ServiceCall) error {
	if !swIsId(c.ServiceId) || !swIsId(c.Member) || c.Args == nil {
		return js.NewTypeError("Invalid service call")
	}
	if c.Instance != nil {
		if !swIsId(c.Instance.Key) || c.Instance.Generation < 1 {
			return js.NewTypeError("Invalid service instance address")
		}
	}
	return nil
}

func swValidateTypedAddress(a ServiceInstanceAddress) error {
	if !swIsId(a.Key) || a.Generation < 1 {
		return js.NewTypeError("Invalid service instance address")
	}
	return nil
}

func swValidateTypedInstance(inst ServiceInstanceSnapshot) error {
	if inst.Instance != nil {
		if err := swValidateTypedAddress(*inst.Instance); err != nil {
			return err
		}
	}
	if inst.Members == nil {
		return js.NewTypeError("Invalid service instance snapshot")
	}
	for _, candidate := range inst.Members {
		switch m := candidate.(type) {
		case ServiceMemberSnapshotMethod:
			if m.Kind != "method" || !swIsId(m.Name) {
				return js.NewTypeError("Invalid service method snapshot")
			}
		case *ServiceMemberSnapshotMethod:
			if m == nil || m.Kind != "method" || !swIsId(m.Name) {
				return js.NewTypeError("Invalid service method snapshot")
			}
		case ServiceMemberSnapshotState:
			if m.Kind != "state" || !swIsId(m.Name) || m.Sequence < 0 || m.Ops == nil {
				return js.NewTypeError("Invalid service state snapshot")
			}
			for _, op := range m.Ops {
				if _, err := AssertValidOp(op); err != nil {
					return err
				}
			}
		case *ServiceMemberSnapshotState:
			if m == nil || m.Kind != "state" || !swIsId(m.Name) || m.Sequence < 0 || m.Ops == nil {
				return js.NewTypeError("Invalid service state snapshot")
			}
			for _, op := range m.Ops {
				if _, err := AssertValidOp(op); err != nil {
					return err
				}
			}
		default:
			return js.NewTypeError("Invalid service member snapshot")
		}
	}
	return nil
}

func swValidateTypedWireInstance(inst WireServiceInstanceSnapshot) error {
	if inst.Instance != nil {
		if err := swValidateTypedAddress(*inst.Instance); err != nil {
			return err
		}
	}
	if inst.Members == nil {
		return js.NewTypeError("Invalid service instance snapshot")
	}
	for _, candidate := range inst.Members {
		switch m := candidate.(type) {
		case WireServiceMemberSnapshotMethod:
			if m.Kind != "method" || !swIsId(m.Name) {
				return js.NewTypeError("Invalid service method snapshot")
			}
		case *WireServiceMemberSnapshotMethod:
			if m == nil || m.Kind != "method" || !swIsId(m.Name) {
				return js.NewTypeError("Invalid service method snapshot")
			}
		case WireServiceMemberSnapshotState:
			if m.Kind != "state" || !swIsId(m.Name) || m.Sequence < 0 || m.Ops == nil {
				return js.NewTypeError("Invalid service state snapshot")
			}
			for _, op := range m.Ops {
				if _, err := AssertValidWireOp(op); err != nil {
					return err
				}
			}
		case *WireServiceMemberSnapshotState:
			if m == nil || m.Kind != "state" || !swIsId(m.Name) || m.Sequence < 0 || m.Ops == nil {
				return js.NewTypeError("Invalid service state snapshot")
			}
			for _, op := range m.Ops {
				if _, err := AssertValidWireOp(op); err != nil {
					return err
				}
			}
		default:
			return js.NewTypeError("Invalid service member snapshot")
		}
	}
	return nil
}

func swValidateTypedSubscriptionSnapshot(s ServiceSubscriptionSnapshot) error {
	if !swIsId(s.ServiceId) || !swIsMode(s.Mode) || s.Instances == nil {
		return js.NewTypeError("Invalid service subscription snapshot")
	}
	for _, inst := range s.Instances {
		if err := swValidateTypedInstance(inst); err != nil {
			return err
		}
	}
	return nil
}

func swValidateTypedWireSubscriptionSnapshot(s WireServiceSubscriptionSnapshot) error {
	if !swIsId(s.ServiceId) || !swIsMode(s.Mode) || s.Instances == nil {
		return js.NewTypeError("Invalid service subscription snapshot")
	}
	for _, inst := range s.Instances {
		if err := swValidateTypedWireInstance(inst); err != nil {
			return err
		}
	}
	return nil
}

func swValidateTypedProviderUpdate(u ServiceProviderUpdate) error {
	switch v := u.(type) {
	case ServiceProviderUpdateState:
		return swValidateTypedStateUpdate(v)
	case *ServiceProviderUpdateState:
		if v == nil {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedStateUpdate(*v)
	case ServiceProviderUpdateReset:
		return swValidateTypedReset(v)
	case *ServiceProviderUpdateReset:
		if v == nil {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedReset(*v)
	case ServiceProviderUpdateUnavailable:
		if v.Type != "unavailable" {
			return js.NewTypeError("Invalid service provider update")
		}
		return nil
	case *ServiceProviderUpdateUnavailable:
		if v == nil || v.Type != "unavailable" {
			return js.NewTypeError("Invalid service provider update")
		}
		return nil
	case ServiceProviderUpdateReplaced:
		if v.Type != "replaced" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedInstance(v.Snapshot)
	case *ServiceProviderUpdateReplaced:
		if v == nil || v.Type != "replaced" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedInstance(v.Snapshot)
	case ServiceProviderUpdateSpawned:
		if v.Type != "spawned" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedInstance(v.Instance)
	case *ServiceProviderUpdateSpawned:
		if v == nil || v.Type != "spawned" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedInstance(v.Instance)
	case ServiceProviderUpdateClosed:
		if v.Type != "closed" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedAddress(v.Instance)
	case *ServiceProviderUpdateClosed:
		if v == nil || v.Type != "closed" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedAddress(v.Instance)
	default:
		return js.NewTypeError("Invalid service provider update")
	}
}

func swValidateTypedStateUpdate(v ServiceProviderUpdateState) error {
	if v.Type != "state" || !swIsId(v.Member) || v.Sequence < 1 || v.Ops == nil {
		return js.NewTypeError("Invalid service state update")
	}
	if v.Instance != nil {
		if err := swValidateTypedAddress(*v.Instance); err != nil {
			return err
		}
	}
	for _, op := range v.Ops {
		if _, err := AssertValidOp(op); err != nil {
			return err
		}
	}
	return nil
}

func swValidateTypedReset(v ServiceProviderUpdateReset) error {
	if v.Type != "reset" {
		return js.NewTypeError("Invalid service provider update")
	}
	if err := swValidateTypedSubscriptionSnapshot(v.Snapshot); err != nil {
		return err
	}
	for _, inst := range v.Snapshot.Instances {
		for _, member := range inst.Members {
			if sm, ok := member.(ServiceMemberSnapshotState); ok {
				if len(sm.Ops) != 1 || sm.Ops[0] == nil || sm.Ops[0].Verb() != "r" {
					return js.NewTypeError("Service reset must contain full root replacements")
				}
			}
		}
	}
	return nil
}

func swValidateTypedWireProviderUpdate(u WireServiceProviderUpdate) error {
	switch v := u.(type) {
	case WireServiceProviderUpdateState:
		return swValidateTypedWireStateUpdate(v)
	case *WireServiceProviderUpdateState:
		if v == nil {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireStateUpdate(*v)
	case WireServiceProviderUpdateReset:
		return swValidateTypedWireReset(v)
	case *WireServiceProviderUpdateReset:
		if v == nil {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireReset(*v)
	case WireServiceProviderUpdateUnavailable:
		if v.Type != "unavailable" {
			return js.NewTypeError("Invalid service provider update")
		}
		return nil
	case *WireServiceProviderUpdateUnavailable:
		if v == nil || v.Type != "unavailable" {
			return js.NewTypeError("Invalid service provider update")
		}
		return nil
	case WireServiceProviderUpdateReplaced:
		if v.Type != "replaced" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireInstance(v.Snapshot)
	case *WireServiceProviderUpdateReplaced:
		if v == nil || v.Type != "replaced" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireInstance(v.Snapshot)
	case WireServiceProviderUpdateSpawned:
		if v.Type != "spawned" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireInstance(v.Instance)
	case *WireServiceProviderUpdateSpawned:
		if v == nil || v.Type != "spawned" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedWireInstance(v.Instance)
	case WireServiceProviderUpdateClosed:
		if v.Type != "closed" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedAddress(v.Instance)
	case *WireServiceProviderUpdateClosed:
		if v == nil || v.Type != "closed" {
			return js.NewTypeError("Invalid service provider update")
		}
		return swValidateTypedAddress(v.Instance)
	default:
		return js.NewTypeError("Invalid service provider update")
	}
}

func swValidateTypedWireStateUpdate(v WireServiceProviderUpdateState) error {
	if v.Type != "state" || !swIsId(v.Member) || v.Sequence < 1 || v.Ops == nil {
		return js.NewTypeError("Invalid service state update")
	}
	if v.Instance != nil {
		if err := swValidateTypedAddress(*v.Instance); err != nil {
			return err
		}
	}
	for _, op := range v.Ops {
		if _, err := AssertValidWireOp(op); err != nil {
			return err
		}
	}
	return nil
}

func swValidateTypedWireReset(v WireServiceProviderUpdateReset) error {
	if v.Type != "reset" {
		return js.NewTypeError("Invalid service provider update")
	}
	if err := swValidateTypedWireSubscriptionSnapshot(v.Snapshot); err != nil {
		return err
	}
	for _, inst := range v.Snapshot.Instances {
		for _, member := range inst.Members {
			if sm, ok := member.(WireServiceMemberSnapshotState); ok {
				if len(sm.Ops) != 1 || sm.Ops[0] == nil || sm.Ops[0].Verb() != "r" {
					return js.NewTypeError("Service reset must contain full root replacements")
				}
			}
		}
	}
	return nil
}
