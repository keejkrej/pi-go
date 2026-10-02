// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

import (
	"encoding/binary"
	"math"
	"math/big"
	"reflect"
	"sort"

	"github.com/tetratelabs/wazero/api"
)

// Host values produced by Dump and accepted by HostToHandle. The mapping from
// TS host values is:
//
//	undefined          JSUndefined{}
//	null               nil
//	boolean            bool
//	number             float64
//	string             string (WTF-8, see the package doc)
//	bigint             *big.Int
//	Symbol.for(desc)   GlobalSymbol{Description: desc}
//	ArrayBuffer        ArrayBuffer
//	Uint8Array         []byte
//	Uint16Array        []uint16
//	Uint32Array        []uint32
//	Float64Array       []float64
//	Error              *HostError
//	Array              []any
//	plain object       *HostObject
type (
	// JSUndefined is the host form of undefined.
	JSUndefined struct{}

	// GlobalSymbol is the host form of a global symbol (Symbol.for(Description)).
	GlobalSymbol struct {
		Description string
	}

	// ArrayBuffer is the host form of an ArrayBuffer: a copy of its bytes.
	ArrayBuffer []byte
)

// HostObject is the host form of a plain object: string keys in property order
// (TS: a plain host object, which keeps insertion order).
type HostObject struct {
	keys   []string
	values map[string]any
}

// NewHostObject creates an empty HostObject.
func NewHostObject() *HostObject {
	return &HostObject{values: map[string]any{}}
}

// Keys returns the keys in order.
func (o *HostObject) Keys() []string {
	return append([]string(nil), o.keys...)
}

// Get returns the value for key and whether it is present.
func (o *HostObject) Get(key string) (any, bool) {
	value, ok := o.values[key]
	return value, ok
}

// Set sets the value for key, appending the key when it is new.
func (o *HostObject) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Len returns the number of keys.
func (o *HostObject) Len() int {
	return len(o.keys)
}

// Dump converts a QuickJS handle to a host value (see the JSUndefined docs for
// the mapping). Strings, numbers, booleans, null, undefined, bigint, global
// symbols, ArrayBuffers, typed arrays, arrays, errors and plain objects are
// converted; functions and local symbols become JSUndefined{}. Circular
// references resolve to the same host []any / *HostObject. Non-nil errors are
// the ones the TS glue throws (e.g. a detached ArrayBuffer) or a VM failure.
func (vm *QuickJS) Dump(handle *JSValueHandle) (any, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	result, err := vm.dump(handle, map[int]any{})
	if err == nil && vm.failure != nil {
		err = vm.failure
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (vm *QuickJS) dump(handle *JSValueHandle, visited map[int]any) (any, error) {
	p := uint64(handle.ptr)
	is := func(id exportID) bool { return vm.call(id, p) != 0 }
	if vm.failure != nil {
		return nil, vm.failure
	}
	if is(exQjsIsUndefined) {
		return JSUndefined{}, nil
	}
	if is(exQjsIsNull) {
		return nil, nil
	}
	if is(exQjsIsBool) {
		return vm.call(exQjsGetBool, p) != 0, nil
	}
	if is(exQjsIsNumber) {
		return api.DecodeF64(vm.call(exQjsGetFloat64, p)), nil
	}
	if is(exQjsIsString) {
		return handle.ToString(), nil
	}
	if is(exQjsIsBigInt) {
		return handle.ToBigInt()
	}
	if is(exQjsIsSymbol) {
		descOutPtr := uint32(vm.call(exWasmMalloc, 4))
		kind := int32(vm.call(exQjsGetSymbolDescription, p, uint64(descOutPtr)))
		descPtr := vm.readU32(descOutPtr)
		vm.call(exWasmFree, uint64(descOutPtr))
		switch kind {
		case 1:
			// Global symbol: reconstruct as Symbol.for(description).
			descHandle := newHandle(vm, descPtr, false, false)
			description := descHandle.ToString()
			descHandle.Dispose()
			return GlobalSymbol{Description: description}, nil
		case 2:
			// Local (anonymous) symbol: can't be reconstructed on host.
			newHandle(vm, descPtr, false, false).Dispose()
			return JSUndefined{}, nil
		}
		return JSUndefined{}, nil
	}
	if is(exQjsIsArrayBuffer) {
		data, err := handle.ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		return ArrayBuffer(data), nil
	}
	if is(exQjsIsException) {
		exc := vm.GetException()
		msg := exc.ToString()
		exc.Dispose()
		return &HostError{name: "Error", message: msg}, nil
	}
	// Functions cannot be meaningfully serialized.
	if is(exQjsIsFunction) {
		return JSUndefined{}, nil
	}
	// Detect circular references using the underlying JS object pointer.
	// If we've already visited this object, return the same host object
	// (preserving the circular structure on the host side).
	if is(exQjsIsObject) {
		if objPtr := handle.Identity(); objPtr != 0 {
			if existing, ok := visited[objPtr]; ok {
				return existing, nil
			}
		}
	}
	// Check for typed arrays (before the regular array check; typed arrays are not arrays).
	if is(exQjsIsObject) {
		if view, abHandle, ok := vm.typedArrayBuffer(handle); ok {
			abLenPtr := uint32(vm.call(exWasmMalloc, 4))
			abDataPtr := uint32(vm.call(exQjsGetArrayBuffer, uint64(abHandle.ptr), uint64(abLenPtr)))
			vm.call(exWasmFree, uint64(abLenPtr))
			abHandle.Dispose()
			if abDataPtr != 0 {
				rawBytes := vm.readBytes(abDataPtr+view.byteOffset, view.byteLength)
				switch view.bytesPerElement {
				case 2:
					return decodeUint16s(rawBytes), nil
				case 4:
					return decodeUint32s(rawBytes), nil
				case 8:
					return decodeFloat64s(rawBytes), nil
				}
				return rawBytes, nil
			}
		}
	}
	if is(exQjsIsArray) {
		lenHandle := handle.GetProp("length")
		length := api.DecodeF64(vm.call(exQjsGetFloat64, uint64(lenHandle.ptr)))
		lenHandle.Dispose()
		n := 0
		if length > 0 {
			n = int(math.Ceil(length))
		}
		// The slice is allocated at its final length and registered in the
		// visited map BEFORE populating it, so circular references within
		// the array resolve to this same array (sharing its backing store).
		arr := make([]any, n)
		if objPtr := handle.Identity(); objPtr != 0 {
			visited[objPtr] = arr
		}
		for i := range n {
			elemHandle := newHandle(vm, uint32(vm.call(exQjsGetPropUint32, p, uint64(uint32(i)))), false, false)
			elem, err := vm.dump(elemHandle, visited)
			elemHandle.Dispose()
			if err != nil {
				return nil, err
			}
			arr[i] = elem
		}
		return arr, nil
	}
	if is(exQjsIsError) {
		nameHandle := handle.GetProp("name")
		msgHandle := handle.GetProp("message")
		stackHandle := handle.GetProp("stack")
		name := "Error"
		if !nameHandle.IsUndefined() {
			name = nameHandle.ToString()
		}
		message := ""
		if !msgHandle.IsUndefined() {
			message = msgHandle.ToString()
		}
		var stack *string
		if !stackHandle.IsUndefined() {
			s := stackHandle.ToString()
			stack = &s
		}
		nameHandle.Dispose()
		msgHandle.Dispose()
		stackHandle.Dispose()
		return &HostError{name: name, message: message, stack: stack}, nil
	}
	if is(exQjsIsObject) {
		keysHandle := newHandle(vm, uint32(vm.call(exQjsGetOwnPropertyNames, p)), false, false)
		if keysHandle.check(exQjsIsException) {
			keysHandle.Dispose()
			return NewHostObject(), nil
		}
		lenHandle := keysHandle.GetProp("length")
		length := api.DecodeF64(vm.call(exQjsGetFloat64, uint64(lenHandle.ptr)))
		lenHandle.Dispose()
		obj := NewHostObject()
		// Register the object in the visited map BEFORE populating it,
		// so circular references resolve to this same object.
		if objPtr := handle.Identity(); objPtr != 0 {
			visited[objPtr] = obj
		}
		for i := 0; float64(i) < length; i++ {
			keyHandle := newHandle(vm, uint32(vm.call(exQjsGetPropUint32, uint64(keysHandle.ptr), uint64(uint32(i)))), false, false)
			key := keyHandle.ToString()
			keyHandle.Dispose()
			valHandle := handle.GetProp(key)
			val, err := vm.dump(valHandle, visited)
			valHandle.Dispose()
			if err != nil {
				return nil, err
			}
			// TS: obj[key] = value on a plain host object, where the
			// "__proto__" key sets the prototype instead of an own property.
			if key != "__proto__" {
				obj.Set(key, val)
			}
		}
		keysHandle.Dispose()
		return obj, nil
	}
	return JSUndefined{}, nil
}

func decodeUint16s(b []byte) []uint16 {
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return out
}

func decodeUint32s(b []byte) []uint32 {
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return out
}

func decodeFloat64s(b []byte) []float64 {
	out := make([]float64, len(b)/8)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return out
}

// HostToHandle converts a host value to a QuickJS handle (see the JSUndefined
// docs for the mapping). It also accepts Go's other numeric types (as numbers),
// other typed slices such as []int32 or []float32 (as an ArrayBuffer of their
// little-endian bytes, like TS's handling of other typed arrays), any error (via
// NewError), and map[string]any (as an object, keys in sorted order). Values of
// any other type convert to undefined, like TS functions.
//
// The result may be a cached singleton (Undefined(), Null(), True(), False());
// disposing it is a no-op.
func (vm *QuickJS) HostToHandle(value any) *JSValueHandle {
	vm.mustNotBeDisposed()
	switch v := value.(type) {
	case nil:
		return vm.Null()
	case JSUndefined:
		return vm.Undefined()
	case bool:
		if v {
			return vm.True()
		}
		return vm.False()
	case float64:
		return vm.NewNumber(v)
	case float32:
		return vm.NewNumber(float64(v))
	case int:
		return vm.NewNumber(float64(v))
	case int8:
		return vm.NewNumber(float64(v))
	case int16:
		return vm.NewNumber(float64(v))
	case int32:
		return vm.NewNumber(float64(v))
	case int64:
		return vm.NewNumber(float64(v))
	case uint:
		return vm.NewNumber(float64(v))
	case uint8:
		return vm.NewNumber(float64(v))
	case uint16:
		return vm.NewNumber(float64(v))
	case uint32:
		return vm.NewNumber(float64(v))
	case uint64:
		return vm.NewNumber(float64(v))
	case string:
		return vm.NewString(v)
	case *big.Int:
		if v == nil {
			return vm.Undefined()
		}
		return vm.NewBigInt(v)
	case GlobalSymbol:
		return vm.NewSymbolFor(v.Description)
	case error:
		return vm.NewError(v)
	case ArrayBuffer:
		return vm.NewArrayBuffer(v)
	case []byte:
		return vm.NewUint8Array(v)
	case []any:
		arr := vm.NewArray()
		for i, elem := range v {
			elemHandle := vm.HostToHandle(elem)
			vm.call(exQjsSetPropUint32, uint64(arr.ptr), uint64(uint32(i)), uint64(elemHandle.ptr))
			elemHandle.Dispose()
		}
		return arr
	case *HostObject:
		if v == nil {
			return vm.Null()
		}
		obj := vm.NewObject()
		for _, key := range v.keys {
			valHandle := vm.HostToHandle(v.values[key])
			obj.SetProp(key, valHandle)
			valHandle.Dispose()
		}
		return obj
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		obj := vm.NewObject()
		for _, key := range keys {
			valHandle := vm.HostToHandle(v[key])
			obj.SetProp(key, valHandle)
			valHandle.Dispose()
		}
		return obj
	}
	// Other typed arrays: convert via the little-endian bytes of the slice.
	if data, ok := typedSliceBytes(value); ok {
		return vm.NewArrayBuffer(data)
	}
	return vm.Undefined()
}

// typedSliceBytes returns the little-endian bytes of a slice of fixed-size
// numbers ([]int16, []uint32, []float32, ...).
func typedSliceBytes(value any) ([]byte, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	switch rv.Type().Elem().Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		return nil, false
	}
	data, err := binary.Append([]byte{}, binary.LittleEndian, value)
	if err != nil {
		return nil, false
	}
	return data, true
}
