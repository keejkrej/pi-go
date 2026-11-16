// Ported from packages/chord/src/services/handle.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ServiceSlot is a host-owned mutable target with consumer-owned guarded views.
type ServiceSlot struct {
	mu             sync.Mutex
	serviceId      string
	wrapObjects    bool
	implementation any
}

func NewServiceSlot(serviceId string, wrapObjects bool) *ServiceSlot {
	return &ServiceSlot{
		serviceId:   serviceId,
		wrapObjects: wrapObjects,
	}
}

func (s *ServiceSlot) View(assertAccess func()) any {
	return shNewServiceView(s, assertAccess, s.wrapObjects).proxy
}

func (s *ServiceSlot) Bind(implementation any) {
	s.mu.Lock()
	s.implementation = implementation
	s.mu.Unlock()
}

func (s *ServiceSlot) Unbind() {
	s.mu.Lock()
	s.implementation = nil
	s.mu.Unlock()
}

func (s *ServiceSlot) Resolve(property any, assertAccess func()) (shResolvedValue, error) {
	if err := shCallAssertAccess(assertAccess); err != nil {
		return shResolvedValue{}, err
	}
	s.mu.Lock()
	implementation := s.implementation
	serviceId := s.serviceId
	s.mu.Unlock()
	if implementation == nil {
		return shResolvedValue{}, fmt.Errorf("Service %s is disconnected", serviceId)
	}
	val, err := shGetProperty(implementation, property)
	if err != nil {
		return shResolvedValue{}, err
	}
	return shResolvedValue{
		Value:    val,
		Receiver: implementation,
	}, nil
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
	v := &shServiceView{
		slot:         slot,
		assertAccess: assertAccess,
		wrapObjects:  wrapObjects,
		members:      make(map[any]*shValueView),
	}
	v.proxy = v
	return v
}

// Get resolves one service member through the slot access guard.
func (v *shServiceView) Get(property any) (any, error) {
	return v.getMember(property)
}

// Call resolves a callable member and invokes it with args.
func (v *shServiceView) Call(member string, args ...any) (any, error) {
	m, err := v.getMember(member)
	if err != nil {
		return nil, err
	}
	if vv, ok := m.(*shValueView); ok {
		return vv.invoke(args)
	}
	if ms, ok := m.(*scMemberSlot); ok {
		res, _, callErr := ms.call(args)
		return res, callErr
	}
	return shInvokeCallable(m, args)
}

func (v *shServiceView) getMember(property any) (any, error) {
	resolve := func() (shResolvedValue, error) {
		return v.slot.Resolve(property, v.assertAccess)
	}
	resolved, err := resolve()
	if err != nil {
		return nil, err
	}
	current := resolved.Value
	isFunc := shIsFunction(current)
	if !shIsObject(current) || (!isFunc && !v.wrapObjects) {
		return current, nil
	}
	if !shComparableKey(property) {
		return shNewValueView(resolve, isFunc).proxy, nil
	}
	v.mu.Lock()
	member, ok := v.members[property]
	if !ok {
		member = shNewValueView(resolve, isFunc)
		v.members[property] = member
	}
	v.mu.Unlock()
	return member.proxy, nil
}

type shValueView struct {
	mu       sync.Mutex
	resolve  shValueResolver
	callable bool
	children map[any]*shValueView
	proxy    any
}

func shNewValueView(resolve shValueResolver, callable bool) *shValueView {
	v := &shValueView{
		resolve:  resolve,
		callable: callable,
		children: make(map[any]*shValueView),
	}
	v.proxy = v
	return v
}

// Invoke calls the underlying function value through dynamic re-resolution.
func (v *shValueView) Invoke(args ...any) (any, error) {
	return v.invoke(args)
}

// Get reads a nested property through dynamic re-resolution.
func (v *shValueView) Get(property any) (any, error) {
	return v.get(property)
}

// StateValue reads a wrapped ReplicatedState value, distinguishing absent (not yet hydrated) from JSON null.
func (v *shValueView) StateValue() (any, bool, error) {
	parent, err := v.resolve()
	if err != nil {
		return nil, false, err
	}
	if ms, ok := parent.Value.(*scMemberSlot); ok {
		return ms.getValueOk()
	}
	if st, ok := parent.Value.(ReplicatedState[any]); ok {
		val, present := st.Value()
		return val, present, nil
	}
	if val, present, ok := shReflectStateValue(parent.Value); ok {
		return val, present, nil
	}
	val, err := v.get("value")
	if err != nil {
		return nil, false, err
	}
	return val, val != nil, nil
}

// Subscribe attaches a ReplicatedState listener through the wrapped member view.
func (v *shValueView) Subscribe(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) (func(), error) {
	subVal, err := v.get("subscribe")
	if err != nil {
		return nil, err
	}
	var res any
	if vv, ok := subVal.(*shValueView); ok {
		res, err = vv.invoke([]any{listener})
	} else {
		res, err = shInvokeCallable(subVal, []any{listener})
	}
	if err != nil {
		return nil, err
	}
	if unsub, ok := res.(func()); ok {
		return unsub, nil
	}
	return nil, js.NewTypeError("Service member subscribe did not return an unsubscribe function")
}

func (v *shValueView) invoke(args []any) (any, error) {
	resolved, err := v.resolve()
	if err != nil {
		return nil, err
	}
	if !shIsFunction(resolved.Value) {
		return nil, js.NewTypeError("Service member is not callable")
	}
	return shInvokeCallable(resolved.Value, args)
}

func (v *shValueView) get(property any) (any, error) {
	resolve := func() (shResolvedValue, error) {
		parent, err := v.resolve()
		if err != nil {
			return shResolvedValue{}, err
		}
		if !shIsObject(parent.Value) {
			return shResolvedValue{}, js.NewTypeError("Service member does not have properties")
		}
		val, err := shGetProperty(parent.Value, property)
		if err != nil {
			return shResolvedValue{}, err
		}
		return shResolvedValue{
			Value:    val,
			Receiver: parent.Value,
		}, nil
	}
	resolved, err := resolve()
	if err != nil {
		return nil, err
	}
	current := resolved.Value
	if !shIsFunction(current) {
		return current, nil
	}
	if !shComparableKey(property) {
		return shNewValueView(resolve, true).proxy, nil
	}
	v.mu.Lock()
	child, ok := v.children[property]
	if !ok {
		child = shNewValueView(resolve, true)
		v.children[property] = child
	}
	v.mu.Unlock()
	return child.proxy, nil
}

func shIsObject(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !rv.IsNil()
	case reflect.Struct, reflect.Array, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func shIsFunction(value any) bool {
	if value == nil {
		return false
	}
	if vv, ok := value.(*shValueView); ok {
		return vv.callable
	}
	if _, ok := value.(*scMemberSlot); ok {
		return true
	}
	rv := reflect.ValueOf(value)
	return rv.Kind() == reflect.Func && !rv.IsNil()
}

func shComparableKey(key any) bool {
	if key == nil {
		return true
	}
	return reflect.TypeOf(key).Comparable()
}

func shCallAssertAccess(fn func()) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	fn()
	return nil
}

func shGetProperty(target any, property any) (any, error) {
	if target == nil {
		return nil, js.NewTypeError("Cannot read properties of nil")
	}
	switch t := target.(type) {
	case *scServiceFacade:
		propStr, ok := property.(string)
		if !ok {
			return nil, nil
		}
		slot, err := t.slot(propStr)
		if err != nil {
			return nil, err
		}
		return slot.value, nil
	case *scMemberSlot:
		propStr, ok := property.(string)
		if !ok {
			return nil, nil
		}
		return t.getProperty(propStr)
	case *shServiceView:
		return t.getMember(property)
	case *shValueView:
		return t.get(property)
	case *jsonx.Object:
		propStr, ok := property.(string)
		if !ok {
			return nil, nil
		}
		v, _ := t.Get(propStr)
		return v, nil
	case map[string]any:
		propStr, ok := property.(string)
		if !ok {
			return nil, nil
		}
		return t[propStr], nil
	}

	propStr, isStr := property.(string)
	if isStr {
		if st, ok := target.(ReplicatedState[any]); ok {
			switch propStr {
			case "value", "Value":
				v, _ := st.Value()
				return v, nil
			case "subscribe", "Subscribe":
				return func(listener func(value JsonValue, call Context, delivery ReplicatedStateDelivery) error) func() {
					return st.Subscribe(listener)
				}, nil
			}
		}
		if handled, val, err := shGetTypedStateProperty(target, propStr); handled {
			return val, err
		}
	}

	rv := reflect.ValueOf(target)
	if rv.Kind() == reflect.Map && !rv.IsNil() {
		if property != nil {
			pk := reflect.ValueOf(property)
			if pk.Type().AssignableTo(rv.Type().Key()) {
				elem := rv.MapIndex(pk)
				if elem.IsValid() {
					return elem.Interface(), nil
				}
			}
		}
		return nil, nil
	}

	if !isStr {
		return nil, nil
	}

	// Struct / pointer to struct: match exported field (via json tag or name) or exported method.
	elem := rv
	if elem.Kind() == reflect.Ptr {
		if elem.IsNil() {
			return nil, nil
		}
		elem = elem.Elem()
	}
	if elem.Kind() == reflect.Struct {
		rt := elem.Type()
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() {
				continue
			}
			name := spStructMemberName(sf)
			if name == propStr || sf.Name == propStr {
				return elem.Field(i).Interface(), nil
			}
		}
	}

	if m := shFindMethod(rv, propStr); m.IsValid() {
		return m.Interface(), nil
	}
	if elem.IsValid() && elem != rv {
		if m := shFindMethod(elem, propStr); m.IsValid() {
			return m.Interface(), nil
		}
	}
	return nil, nil
}

func shFindMethod(rv reflect.Value, name string) reflect.Value {
	if !rv.IsValid() {
		return reflect.Value{}
	}
	if m := rv.MethodByName(name); m.IsValid() {
		return m
	}
	if len(name) > 0 {
		upper := strings.ToUpper(name[:1]) + name[1:]
		if upper != name {
			if m := rv.MethodByName(upper); m.IsValid() {
				return m
			}
		}
	}
	return reflect.Value{}
}

func shReflectStateValue(target any) (any, bool, bool) {
	if target == nil {
		return nil, false, false
	}
	rv := reflect.ValueOf(target)
	vm := rv.MethodByName("Value")
	sm := rv.MethodByName("Subscribe")
	if !vm.IsValid() || !sm.IsValid() {
		return nil, false, false
	}
	vt := vm.Type()
	if vt.NumIn() != 0 || vt.NumOut() != 2 || vt.Out(1).Kind() != reflect.Bool {
		return nil, false, false
	}
	outs := vm.Call(nil)
	return outs[0].Interface(), outs[1].Bool(), true
}

func shGetTypedStateProperty(target any, prop string) (bool, any, error) {
	if target == nil {
		return false, nil, nil
	}
	rv := reflect.ValueOf(target)
	vm := rv.MethodByName("Value")
	sm := rv.MethodByName("Subscribe")
	if !vm.IsValid() || !sm.IsValid() {
		return false, nil, nil
	}
	vt := vm.Type()
	if vt.NumIn() != 0 || vt.NumOut() != 2 || vt.Out(1).Kind() != reflect.Bool {
		return false, nil, nil
	}
	switch prop {
	case "value", "Value":
		outs := vm.Call(nil)
		if !outs[1].Bool() {
			return true, nil, nil
		}
		return true, outs[0].Interface(), nil
	case "subscribe", "Subscribe":
		st := sm.Type()
		if st.NumIn() != 1 || st.NumOut() != 1 {
			return false, nil, nil
		}
		listenerType := st.In(0)
		return true, func(listener any) (any, error) {
			if listener == nil {
				return nil, js.NewTypeError("Replicated state subscription listener must be a function")
			}
			lv := reflect.ValueOf(listener)
			if lv.Kind() != reflect.Func || lv.IsNil() {
				return nil, js.NewTypeError("Replicated state subscription listener must be a function")
			}
			var arg reflect.Value
			if lv.Type().AssignableTo(listenerType) {
				arg = lv
			} else if listenerType.Kind() == reflect.Func && listenerType.NumIn() == 3 {
				arg = reflect.MakeFunc(listenerType, func(args []reflect.Value) []reflect.Value {
					callArgs := make([]reflect.Value, lv.Type().NumIn())
					for i := 0; i < lv.Type().NumIn() && i < len(args); i++ {
						if args[i].Type().AssignableTo(lv.Type().In(i)) {
							callArgs[i] = args[i]
						} else if !args[i].IsValid() || args[i].IsZero() {
							callArgs[i] = reflect.Zero(lv.Type().In(i))
						} else if args[i].CanInterface() {
							if conv, ok := shTryConvert(args[i].Interface(), lv.Type().In(i)); ok {
								callArgs[i] = conv
							} else {
								callArgs[i] = reflect.Zero(lv.Type().In(i))
							}
						} else {
							callArgs[i] = reflect.Zero(lv.Type().In(i))
						}
					}
					outs := lv.Call(callArgs)
					if listenerType.NumOut() == 0 {
						return nil
					}
					if len(outs) > 0 && outs[len(outs)-1].Type().AssignableTo(listenerType.Out(0)) {
						return []reflect.Value{outs[len(outs)-1]}
					}
					return []reflect.Value{reflect.Zero(listenerType.Out(0))}
				})
			} else {
				return nil, js.NewTypeError("Incompatible state listener signature")
			}
			outs := sm.Call([]reflect.Value{arg})
			return outs[0].Interface(), nil
		}, nil
	}
	return false, nil, nil
}

func shTryConvert(val any, target reflect.Type) (reflect.Value, bool) {
	if val == nil {
		return reflect.Zero(target), true
	}
	rv := reflect.ValueOf(val)
	if rv.Type().AssignableTo(target) {
		return rv, true
	}
	if rv.Type().ConvertibleTo(target) && (rv.Kind() != reflect.Float64 || target.Kind() != reflect.String) {
		return rv.Convert(target), true
	}
	return reflect.Value{}, false
}

func shInvokeCallable(fn any, args []any) (any, error) {
	if fn == nil {
		return nil, js.NewTypeError("Service member is not callable")
	}
	if vv, ok := fn.(*shValueView); ok {
		return vv.Invoke(args...)
	}
	if ms, ok := fn.(*scMemberSlot); ok {
		res, _, err := ms.call(args)
		return res, err
	}
	if rm, ok := fn.(spRemoteMethod); ok {
		res, _, err := rm(args...)
		return res, err
	}
	rv := reflect.ValueOf(fn)
	if !rv.IsValid() || rv.Kind() != reflect.Func || rv.IsNil() {
		return nil, js.NewTypeError("Service member is not callable")
	}
	ft := rv.Type()
	inVals, err := shBindArgs(ft, args)
	if err != nil {
		return nil, err
	}
	outs := rv.Call(inVals)
	nOut := len(outs)
	if nOut == 0 {
		return nil, nil
	}
	errType := reflect.TypeFor[error]()
	if ft.Out(nOut - 1).Implements(errType) {
		if !outs[nOut-1].IsNil() {
			return nil, outs[nOut-1].Interface().(error)
		}
		nOut--
	}
	switch nOut {
	case 0:
		return nil, nil
	case 1:
		return outs[0].Interface(), nil
	case 2:
		if outs[1].Kind() == reflect.Bool && !outs[1].Bool() {
			return nil, nil
		}
		return outs[0].Interface(), nil
	default:
		return outs[0].Interface(), nil
	}
}

func shBindArgs(ft reflect.Type, args []any) ([]reflect.Value, error) {
	numIn := ft.NumIn()
	if ft.IsVariadic() {
		fixed := numIn - 1
		if len(args) < fixed {
			return nil, js.NewTypeError(fmt.Sprintf("Callable requires at least %d argument(s), got %d", fixed, len(args)))
		}
		out := make([]reflect.Value, len(args))
		for i := 0; i < fixed; i++ {
			v, err := shConvertArg(args[i], ft.In(i), i)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		elemType := ft.In(fixed).Elem()
		for i := fixed; i < len(args); i++ {
			v, err := shConvertArg(args[i], elemType, i)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	if len(args) < numIn {
		return nil, js.NewTypeError(fmt.Sprintf("Callable requires %d argument(s), got %d", numIn, len(args)))
	}
	out := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		v, err := shConvertArg(args[i], ft.In(i), i)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func shConvertArg(val any, target reflect.Type, idx int) (reflect.Value, error) {
	if val == nil {
		switch target.Kind() {
		case reflect.Interface, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
			return reflect.Zero(target), nil
		default:
			return reflect.Value{}, js.NewTypeError(fmt.Sprintf("Argument %d cannot be nil for %s", idx, target))
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
	return reflect.Value{}, js.NewTypeError(fmt.Sprintf("Argument %d has type %s, expected %s", idx, rv.Type(), target))
}
