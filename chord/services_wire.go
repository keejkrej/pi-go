// Ported from packages/chord/src/services/wire.ts (pi v1.0.0).

package chord

import "github.com/keejkrej/pi-go/internal/jsonx"

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
	panic("unported: MarshalJSON")
}

func (u *UnknownWireServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
	panic("unported: UnmarshalJSON")
}

// WireServiceMemberSnapshotList is the members array of a wire instance snapshot.
type WireServiceMemberSnapshotList []WireServiceMemberSnapshot

func (l WireServiceMemberSnapshotList) MarshalJSON() ([]byte, error) {
	panic("unported: MarshalJSON")
}

func (l *WireServiceMemberSnapshotList) UnmarshalJSON(data []byte) error {
	panic("unported: UnmarshalJSON")
}

// UnmarshalWireServiceMemberSnapshot decodes one wire member snapshot.
func UnmarshalWireServiceMemberSnapshot(data []byte) (WireServiceMemberSnapshot, error) {
	panic("unported: UnmarshalWireServiceMemberSnapshot")
}

// DecodeWireServiceMemberSnapshot decodes one wire member snapshot from a jsonx value.
func DecodeWireServiceMemberSnapshot(v any) (WireServiceMemberSnapshot, error) {
	panic("unported: DecodeWireServiceMemberSnapshot")
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
	panic("unported: MarshalJSON")
}

func (u *UnknownWireServiceProviderUpdate) UnmarshalJSON(data []byte) error {
	panic("unported: UnmarshalJSON")
}

// UnmarshalWireServiceProviderUpdate decodes one wire provider update.
func UnmarshalWireServiceProviderUpdate(data []byte) (WireServiceProviderUpdate, error) {
	panic("unported: UnmarshalWireServiceProviderUpdate")
}

// DecodeWireServiceProviderUpdate decodes one wire provider update from a jsonx value.
func DecodeWireServiceProviderUpdate(v any) (WireServiceProviderUpdate, error) {
	panic("unported: DecodeWireServiceProviderUpdate")
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
	panic("unported: CreateServiceCatalogueCall")
}

// CreateServiceSubscribeCall builds the subscribe control call.
func CreateServiceSubscribeCall(subscriptionId string, serviceId string, mode ServiceMode) ServiceCall {
	panic("unported: CreateServiceSubscribeCall")
}

// CreateServiceUnsubscribeCall builds the unsubscribe control call.
func CreateServiceUnsubscribeCall(subscriptionId string) ServiceCall {
	panic("unported: CreateServiceUnsubscribeCall")
}

// DecodeServiceControlCall decodes a control call. A nil result means the call is not a control call (TS undefined).
func DecodeServiceControlCall(call ServiceCall) ServiceControlCall {
	panic("unported: DecodeServiceControlCall")
}

// ParseServiceCall validates a service call.
func ParseServiceCall(value any) (ServiceCall, error) {
	panic("unported: ParseServiceCall")
}

// ParseServiceCatalogue validates a service catalogue.
func ParseServiceCatalogue(value any) ([]ServiceCatalogueEntry, error) {
	panic("unported: ParseServiceCatalogue")
}

// ParseServiceSubscriptionSnapshot validates a decoded subscription snapshot.
func ParseServiceSubscriptionSnapshot(value any) (ServiceSubscriptionSnapshot, error) {
	panic("unported: ParseServiceSubscriptionSnapshot")
}

// ParseWireServiceSubscriptionSnapshot validates a wire subscription snapshot.
func ParseWireServiceSubscriptionSnapshot(value any) (WireServiceSubscriptionSnapshot, error) {
	panic("unported: ParseWireServiceSubscriptionSnapshot")
}

// ParseServiceProviderUpdate validates a decoded provider update.
func ParseServiceProviderUpdate(value any) (ServiceProviderUpdate, error) {
	panic("unported: ParseServiceProviderUpdate")
}

// ParseWireServiceProviderUpdate validates a wire provider update.
func ParseWireServiceProviderUpdate(value any) (WireServiceProviderUpdate, error) {
	panic("unported: ParseWireServiceProviderUpdate")
}

func swAssertSubscriptionSnapshot(value any, assertOp func(any) error) error {
	panic("unported: swAssertSubscriptionSnapshot")
}

func swAssertProviderUpdate(value any, assertOp func(any) error) error {
	panic("unported: swAssertProviderUpdate")
}

func swAssertInstance(value any, assertOp func(any) error) error {
	panic("unported: swAssertInstance")
}

func swAssertAddress(value any) error {
	panic("unported: swAssertAddress")
}

func swRecord(value any, description string) (*jsonx.Object, error) {
	panic("unported: swRecord")
}

func swAssertKeys(value *jsonx.Object, required []string, optional []string, description string) error {
	panic("unported: swAssertKeys")
}

func swIsId(value any) bool {
	panic("unported: swIsId")
}

func swIsMode(value any) bool {
	panic("unported: swIsMode")
}

func swIsInteger(value any, minimum int) bool {
	panic("unported: swIsInteger")
}
