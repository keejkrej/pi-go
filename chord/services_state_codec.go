// Ported from packages/chord/src/services/state-codec.ts (pi v1.0.0).

package chord

import (
	"sync"

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
	panic("unported: sscNewStateCodecRegistry")
}

func (r *sscStateCodecRegistry[C]) reset() {
	panic("unported: reset")
}

func (r *sscStateCodecRegistry[C]) add(instance *ServiceInstanceAddress, member string) (C, error) {
	panic("unported: add")
}

func (r *sscStateCodecRegistry[C]) get(instance *ServiceInstanceAddress, member string) (C, error) {
	panic("unported: get")
}

func (r *sscStateCodecRegistry[C]) removeInstance(instance ServiceInstanceAddress) {
	panic("unported: removeInstance")
}

type sscEncoder struct {
	mu     sync.Mutex
	codecs *sscStateCodecRegistry[Encoder]
}

func (e *sscEncoder) EncodeSnapshot(snapshot ServiceSubscriptionSnapshot) (WireServiceSubscriptionSnapshot, error) {
	panic("unported: EncodeSnapshot")
}

func (e *sscEncoder) EncodeUpdate(update ServiceProviderUpdate) (WireServiceProviderUpdate, error) {
	panic("unported: EncodeUpdate")
}

type sscDecoder struct {
	mu     sync.Mutex
	codecs *sscStateCodecRegistry[Decoder]
}

func (d *sscDecoder) DecodeSnapshot(snapshot WireServiceSubscriptionSnapshot) (ServiceSubscriptionSnapshot, error) {
	panic("unported: DecodeSnapshot")
}

func (d *sscDecoder) DecodeUpdate(update WireServiceProviderUpdate) (ServiceProviderUpdate, error) {
	panic("unported: DecodeUpdate")
}

// CreateServiceStateEncoder returns a stateful encoder for one subscription.
func CreateServiceStateEncoder() ServiceStateEncoder {
	panic("unported: CreateServiceStateEncoder")
}

// CreateServiceStateDecoder returns a stateful decoder for one subscription.
func CreateServiceStateDecoder() ServiceStateDecoder {
	panic("unported: CreateServiceStateDecoder")
}

func sscEncodeInstance(instance ServiceInstanceSnapshot, codecs *sscStateCodecRegistry[Encoder]) (WireServiceInstanceSnapshot, error) {
	panic("unported: sscEncodeInstance")
}

func sscDecodeInstance(instance WireServiceInstanceSnapshot, codecs *sscStateCodecRegistry[Decoder]) (ServiceInstanceSnapshot, error) {
	panic("unported: sscDecodeInstance")
}

func sscStateKey(instance *ServiceInstanceAddress, member string) string {
	panic("unported: sscStateKey")
}

func sscSameAddress(left *ServiceInstanceAddress, right ServiceInstanceAddress) bool {
	panic("unported: sscSameAddress")
}

func sscDescribeState(instance *ServiceInstanceAddress, member string) string {
	panic("unported: sscDescribeState")
}
