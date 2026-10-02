// Ported from packages/chord/src/services/handle.ts (pi v1.0.0).

package chord

import "sync"

// ServiceSlot is a host-owned mutable target with consumer-owned guarded views.
type ServiceSlot struct {
	mu             sync.Mutex
	serviceId      string
	wrapObjects    bool
	implementation any
}

func NewServiceSlot(serviceId string, wrapObjects bool) *ServiceSlot {
	panic("unported: NewServiceSlot")
}

func (s *ServiceSlot) View(assertAccess func()) any {
	panic("unported: ServiceSlot.View")
}

func (s *ServiceSlot) Bind(implementation any) {
	panic("unported: ServiceSlot.Bind")
}

func (s *ServiceSlot) Unbind() {
	panic("unported: ServiceSlot.Unbind")
}

func (s *ServiceSlot) Resolve(property any, assertAccess func()) (shResolvedValue, error) {
	panic("unported: ServiceSlot.Resolve")
}

type shResolvedValue struct {
	Value    any
	Receiver any
}

type shValueResolver func() (shResolvedValue, error)

type shServiceView struct {
	mu           sync.Mutex
	slot         *ServiceSlot
	assertAccess func()
	wrapObjects  bool
	members      map[any]*shValueView
	proxy        any
}

func shNewServiceView(slot *ServiceSlot, assertAccess func(), wrapObjects bool) *shServiceView {
	panic("unported: shNewServiceView")
}

func (v *shServiceView) getMember(property any) (any, error) {
	panic("unported: shServiceView.getMember")
}

type shValueView struct {
	mu       sync.Mutex
	resolve  shValueResolver
	children map[any]*shValueView
	proxy    any
}

func shNewValueView(resolve shValueResolver, callable bool) *shValueView {
	panic("unported: shNewValueView")
}

func (v *shValueView) invoke(args []any) (any, error) {
	panic("unported: shValueView.invoke")
}

func (v *shValueView) get(property any) (any, error) {
	panic("unported: shValueView.get")
}

func shIsObject(value any) bool {
	panic("unported: shIsObject")
}
