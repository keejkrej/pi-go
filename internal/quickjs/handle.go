// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

import (
	"errors"
	"math/big"

	"github.com/tetratelabs/wazero/api"
)

// JSValueHandle is a handle to a JSValue inside the QuickJS WASM instance.
type JSValueHandle struct {
	vm       *QuickJS
	ptr      uint32
	disposed bool
	// singleton marks a cached singleton handle (e.g. undefined, null, true,
	// false, the global object): Dispose() is a no-op. This prevents code that
	// routinely disposes handles (such as the object/array branches of
	// HostToHandle) from freeing the shared heap JSValue* that the cached
	// singleton still references, which would corrupt later reads.
	singleton bool
	// borrowed marks a handle wrapping a JSValue* OWNED BY THE C CALLER: the
	// this/argument handles the host-call trampoline passes to a host
	// callback. The C side frees those values after the call returns, so
	// Dispose() is a no-op and the handle is never registered with an active
	// WithScope() (either would double-free the guest value and corrupt the
	// heap). A callback that needs to retain an argument past its own
	// invocation must Dup() it; the duplicate takes a fresh reference and
	// behaves like any owned handle.
	borrowed bool
	// onDispose is extra cleanup to run when this handle is disposed. Used by
	// NewEphemeralFunction() to unregister its host callback.
	onDispose func()
}

func newHandle(vm *QuickJS, ptr uint32, singleton, borrowed bool) *JSValueHandle {
	h := &JSValueHandle{vm: vm, ptr: ptr, singleton: singleton, borrowed: borrowed}
	// Singletons are shared and outlive any scope; borrowed handles wrap
	// C-owned pointers that a scope must never free.
	if !singleton && !borrowed && vm.activeScope != nil {
		vm.activeScope.add(h)
	}
	return h
}

// Vm returns the QuickJS VM instance this handle belongs to.
func (h *JSValueHandle) Vm() *QuickJS {
	return h.vm
}

// Disposed reports whether Dispose() has been called on this handle.
//
// Note that handle methods do not guard against use after disposal: reading
// from a disposed handle reads freed memory. Check this when a handle's
// lifetime is managed elsewhere (e.g. by WithScope()).
func (h *JSValueHandle) Disposed() bool {
	// singletons are never freed, so they are never "disposed"
	return h.disposed
}

func (h *JSValueHandle) check(id exportID) bool {
	return h.vm.call(id, uint64(h.ptr)) != 0
}

// IsUndefined reports whether the value is undefined.
func (h *JSValueHandle) IsUndefined() bool { return h.check(exQjsIsUndefined) }

// IsNull reports whether the value is null.
func (h *JSValueHandle) IsNull() bool { return h.check(exQjsIsNull) }

// IsBool reports whether the value is a boolean.
func (h *JSValueHandle) IsBool() bool { return h.check(exQjsIsBool) }

// IsNumber reports whether the value is a number.
func (h *JSValueHandle) IsNumber() bool { return h.check(exQjsIsNumber) }

// IsString reports whether the value is a string.
func (h *JSValueHandle) IsString() bool { return h.check(exQjsIsString) }

// IsSymbol reports whether the value is a symbol.
func (h *JSValueHandle) IsSymbol() bool { return h.check(exQjsIsSymbol) }

// IsBigInt reports whether the value is a BigInt.
func (h *JSValueHandle) IsBigInt() bool { return h.check(exQjsIsBigInt) }

// IsObject reports whether the value is an object (including functions).
func (h *JSValueHandle) IsObject() bool { return h.check(exQjsIsObject) }

// IsArray reports whether the value is an array.
func (h *JSValueHandle) IsArray() bool { return h.check(exQjsIsArray) }

// IsFunction reports whether the value is a function.
func (h *JSValueHandle) IsFunction() bool { return h.check(exQjsIsFunction) }

// IsError reports whether the value is an Error.
func (h *JSValueHandle) IsError() bool { return h.check(exQjsIsError) }

// IsPromise reports whether the value is a Promise.
func (h *JSValueHandle) IsPromise() bool { return h.check(exQjsIsPromise) }

// IsArrayBuffer reports whether the value is an ArrayBuffer.
func (h *JSValueHandle) IsArrayBuffer() bool { return h.check(exQjsIsArrayBuffer) }

// IsProxy reports whether this value is a Proxy exotic object.
//
// This is an engine-level check: it never fires proxy traps and cannot be
// determined (or spoofed) from within guest JavaScript. Use GetProxyTarget /
// GetProxyHandler to introspect a detected proxy without executing guest code.
func (h *JSValueHandle) IsProxy() bool { return h.check(exQjsIsProxy) }

// IsMap reports whether this value is a Map (engine brand check: trap-free,
// spoof-proof, and unaffected by prototype/constructor mutation). A Proxy
// wrapping a Map returns false.
func (h *JSValueHandle) IsMap() bool { return h.check(exQjsIsMap) }

// IsSet reports whether this value is a Set (engine brand check). A Proxy
// wrapping a Set returns false.
func (h *JSValueHandle) IsSet() bool { return h.check(exQjsIsSet) }

// IsDate reports whether this value is a Date (engine brand check). A Proxy
// wrapping a Date returns false.
func (h *JSValueHandle) IsDate() bool { return h.check(exQjsIsDate) }

// IsRegExp reports whether this value is a RegExp (engine brand check). A Proxy
// wrapping a RegExp returns false.
func (h *JSValueHandle) IsRegExp() bool { return h.check(exQjsIsRegexp) }

// IsWeakRef reports whether this value is a WeakRef (engine brand check).
func (h *JSValueHandle) IsWeakRef() bool { return h.check(exQjsIsWeakRef) }

// IsWeakMap reports whether this value is a WeakMap (engine brand check).
func (h *JSValueHandle) IsWeakMap() bool { return h.check(exQjsIsWeakMap) }

// IsWeakSet reports whether this value is a WeakSet (engine brand check).
func (h *JSValueHandle) IsWeakSet() bool { return h.check(exQjsIsWeakSet) }

// IsDataView reports whether this value is a DataView (engine brand check).
func (h *JSValueHandle) IsDataView() bool { return h.check(exQjsIsDataView) }

// Identity is a numeric identity for the underlying heap value, or 0 for values
// that are not heap-allocated (numbers, booleans, null, undefined).
//
// Two handles to the same underlying object always report the same identity,
// and two live handles to different objects always report different
// identities, so this is the value to key a map on when deduplicating or
// detecting cycles across handles (Dump() uses it for exactly that).
//
// The identity is only meaningful while the value is alive; it is an address,
// so it may be reused after every handle to the value has been disposed.
func (h *JSValueHandle) Identity() int {
	return int(int32(h.vm.call(exQjsGetValuePtr, uint64(h.ptr))))
}

// ToBoolean extracts the value as a boolean, applying JavaScript truthiness
// (equivalent to !!value inside the VM).
func (h *JSValueHandle) ToBoolean() bool {
	return h.check(exQjsGetBool)
}

// ClassId is the internal QuickJS class ID of this value, or 0 for non-objects.
// Class IDs are stable within a VM instance but are an engine implementation
// detail, so prefer the dedicated Is* methods.
func (h *JSValueHandle) ClassId() int {
	return int(int32(h.vm.call(exQjsGetClassId, uint64(h.ptr))))
}

// ClassName is the engine-level class name of this value, e.g. "Object", "Map",
// "Date", "RegExp", or nil for non-objects and unnamed internal classes.
//
// Unlike ConstructorName (which reads the constructor and name properties and
// can therefore fire getters/proxy traps and be spoofed), this is trap-free: it
// reads the engine's class table directly and never executes guest code. Note
// that the engine registers the Proxy class under the name "Object", so use
// IsProxy to detect proxies.
func (h *JSValueHandle) ClassName() (*string, error) {
	vm := h.vm
	nameHandle := newHandle(vm, uint32(vm.call(exQjsGetClassName, uint64(h.ptr))), false, false)
	// qjs_get_class_name can return JS_EXCEPTION (e.g. OOM while
	// materializing the name atom as a string); surface it instead of
	// stringifying the exception sentinel and leaving the real error
	// pending on the context.
	if nameHandle.check(exQjsIsException) {
		nameHandle.Dispose()
		return nil, vm.pendingException()
	}
	defer nameHandle.Dispose()
	if nameHandle.IsUndefined() {
		return nil, vm.failure
	}
	name := nameHandle.ToString()
	return &name, vm.failure
}

// pendingException wraps the context's pending exception as a *JSException
// (TS: throw new JSException(this.vm.getException())), or returns the VM
// failure.
func (vm *QuickJS) pendingException() error {
	if vm.failure != nil {
		return vm.failure
	}
	exc := newJSException(vm.GetException())
	if vm.failure != nil {
		return vm.failure
	}
	return exc
}

// PromiseState gets the promise state: 0 = pending, 1 = fulfilled, 2 = rejected.
func (h *JSValueHandle) PromiseState() int {
	return int(int32(h.vm.call(exQjsPromiseState, uint64(h.ptr))))
}

// Typeof gets the typeof this value as a string. Returns the same values as the
// native typeof operator.
func (h *JSValueHandle) Typeof() string {
	return h.vm.Typeof(h)
}

// Length gets the length property of this value (for arrays, strings, etc.).
func (h *JSValueHandle) Length() float64 {
	lenHandle := h.GetProp("length")
	n := lenHandle.ToNumber()
	lenHandle.Dispose()
	return n
}

// ConstructorName gets the constructor name of this object, or nil if unavailable.
func (h *JSValueHandle) ConstructorName() *string {
	ctor := h.GetProp("constructor")
	if ctor.IsUndefined() || ctor.IsNull() {
		ctor.Dispose()
		return nil
	}
	name := ctor.GetProp("name")
	ctor.Dispose()
	if name.IsUndefined() || name.IsNull() {
		name.Dispose()
		return nil
	}
	result := name.ToString()
	name.Dispose()
	return &result
}

// propertyKeyList reads the elements of a key array returned by one of the
// qjs_get_own_property_* exports. ok is false when the export threw.
func (h *JSValueHandle) propertyKeyList(id exportID, keepSymbols bool) (keys []PropertyKey, ok bool) {
	vm := h.vm
	keysHandle := newHandle(vm, uint32(vm.call(id, uint64(h.ptr))), false, false)
	if keysHandle.check(exQjsIsException) {
		keysHandle.Dispose()
		return nil, false
	}
	lenHandle := keysHandle.GetProp("length")
	length := api.DecodeF64(vm.call(exQjsGetFloat64, uint64(lenHandle.ptr)))
	lenHandle.Dispose()
	for i := 0; float64(i) < length; i++ {
		keyHandle := newHandle(vm, uint32(vm.call(exQjsGetPropUint32, uint64(keysHandle.ptr), uint64(uint32(i)))), false, false)
		if keepSymbols && keyHandle.IsSymbol() {
			keys = append(keys, keyHandle)
			continue
		}
		keys = append(keys, StringKey(keyHandle.ToString()))
		keyHandle.Dispose()
		if vm.failure != nil {
			break
		}
	}
	keysHandle.Dispose()
	return keys, true
}

func stringKeys(keys []PropertyKey) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, string(key.(StringKey)))
	}
	return result
}

// Keys gets the own enumerable string property names (equivalent to Object.keys()).
func (h *JSValueHandle) Keys() []string {
	keys, ok := h.propertyKeyList(exQjsGetOwnPropertyNames, false)
	if !ok {
		return []string{}
	}
	return stringKeys(keys)
}

// GetOwnPropertyNames gets all own property names including non-enumerable
// ones (equivalent to Object.getOwnPropertyNames()).
func (h *JSValueHandle) GetOwnPropertyNames() []string {
	keys, ok := h.propertyKeyList(exQjsGetOwnPropertyNamesAll, false)
	if !ok {
		return []string{}
	}
	return stringKeys(keys)
}

// GetOwnPropertyKeys gets ALL own property keys (strings and symbols),
// including non-enumerable (equivalent to Reflect.ownKeys()).
//
// String keys are returned as StringKey; symbol keys are returned as
// *JSValueHandle, which the caller must dispose.
//
// Trap-free for ordinary objects; fires the ownKeys trap for a Proxy (check
// IsProxy first if that matters).
func (h *JSValueHandle) GetOwnPropertyKeys() []PropertyKey {
	keys, ok := h.propertyKeyList(exQjsGetOwnPropertyKeys, true)
	if !ok {
		return []PropertyKey{}
	}
	if keys == nil {
		keys = []PropertyKey{}
	}
	return keys
}

// GetOwnPropertyDescriptor gets the own property descriptor for a key WITHOUT
// invoking getters (equivalent to Object.getOwnPropertyDescriptor()).
//
// A data property yields Value + Writable, an accessor property yields Get +
// Set (handles to the accessor functions themselves, never invoked). Returns
// nil if there is no such own property. The Value/Get/Set handles are owned by
// the caller and must be disposed. A guest exception is a *JSException.
//
// Trap-free for ordinary objects; fires the getOwnPropertyDescriptor trap for a
// Proxy (check IsProxy first if that matters).
func (h *JSValueHandle) GetOwnPropertyDescriptor(key PropertyKey) (*JSOwnPropertyDescriptor, error) {
	vm := h.vm
	var keyHandle *JSValueHandle
	var keyPtr uint32
	switch k := key.(type) {
	case StringKey:
		keyHandle = vm.NewString(string(k))
		keyPtr = keyHandle.ptr
	case *JSValueHandle:
		keyPtr = k.ptr
	}
	descPtr := uint32(vm.call(exQjsGetOwnPropertyDescriptor, uint64(h.ptr), uint64(keyPtr)))
	if keyHandle != nil {
		keyHandle.Dispose()
	}
	if vm.failure != nil {
		return nil, vm.failure
	}
	if descPtr == 0 {
		return nil, nil // no such own property
	}
	descHandle := newHandle(vm, descPtr, false, false)
	defer descHandle.Dispose()
	if descHandle.check(exQjsIsException) {
		return nil, vm.pendingException()
	}
	readBool := func(name string) bool {
		flag := descHandle.GetProp(name)
		defer flag.Dispose()
		return flag.check(exQjsGetBool)
	}
	enumerable := readBool("enumerable")
	configurable := readBool("configurable")
	var desc *JSOwnPropertyDescriptor
	if descHandle.HasOwnProperty("value") {
		value := descHandle.GetProp("value")
		writable := readBool("writable")
		desc = &JSOwnPropertyDescriptor{Value: value, Writable: &writable, Enumerable: enumerable, Configurable: configurable}
	} else {
		desc = &JSOwnPropertyDescriptor{Get: descHandle.GetProp("get"), Set: descHandle.GetProp("set"), Enumerable: enumerable, Configurable: configurable}
	}
	if vm.failure != nil {
		return nil, vm.failure
	}
	return desc, nil
}

// stringKeyCall runs a qjs_*(obj, cstring) export, or its qjs_*_value(obj,
// key) form for keys that cannot cross the C-string API.
func (h *JSValueHandle) stringKeyCall(name string, cstringID, valueID exportID, extra ...uint64) uint64 {
	vm := h.vm
	if stringKeyNeedsValuePath(name) {
		// A NUL or lone surrogate in the key cannot cross the C-string
		// API; go through a length-aware guest string key instead.
		keyHandle := vm.NewString(name)
		defer keyHandle.Dispose()
		return vm.call(valueID, append([]uint64{uint64(h.ptr), uint64(keyHandle.ptr)}, extra...)...)
	}
	namePtr, _, err := vm.writeString(name)
	if err != nil {
		vm.fail(err)
		return 0
	}
	result := vm.call(cstringID, append([]uint64{uint64(h.ptr), uint64(namePtr)}, extra...)...)
	vm.call(exWasmFree, uint64(namePtr))
	return result
}

// HasOwnProperty checks if a property is an own property (equivalent to
// Object.prototype.hasOwnProperty).
func (h *JSValueHandle) HasOwnProperty(name string) bool {
	return int32(h.stringKeyCall(name, exQjsHasOwnProperty, exQjsHasOwnPropertyValue)) == 1
}

// PropertyIsEnumerable checks if a property is enumerable (equivalent to
// Object.prototype.propertyIsEnumerable).
func (h *JSValueHandle) PropertyIsEnumerable(name string) bool {
	return int32(h.stringKeyCall(name, exQjsPropertyIsEnumerable, exQjsPropertyIsEnumerableValue)) == 1
}

// GetPrototypeOf gets the prototype of this object (equivalent to Object.getPrototypeOf()).
func (h *JSValueHandle) GetPrototypeOf() *JSValueHandle {
	return newHandle(h.vm, uint32(h.vm.call(exQjsGetPrototypeOf, uint64(h.ptr))), false, false)
}

func (h *JSValueHandle) proxyPart(id exportID) (*JSValueHandle, error) {
	vm := h.vm
	handle := newHandle(vm, uint32(vm.call(id, uint64(h.ptr))), false, false)
	if handle.check(exQjsIsException) {
		handle.Dispose()
		return nil, vm.pendingException()
	}
	if vm.failure != nil {
		return nil, vm.failure
	}
	return handle, nil
}

// GetProxyTarget gets the [[ProxyTarget]] of this Proxy without firing any
// traps. Returns a *JSException if this value is not a Proxy; check IsProxy
// first. Note the target may itself be a Proxy.
func (h *JSValueHandle) GetProxyTarget() (*JSValueHandle, error) {
	return h.proxyPart(exQjsGetProxyTarget)
}

// GetProxyHandler gets the [[ProxyHandler]] of this Proxy without firing any
// traps. Returns a *JSException if this value is not a Proxy; check IsProxy first.
func (h *JSValueHandle) GetProxyHandler() (*JSValueHandle, error) {
	return h.proxyPart(exQjsGetProxyHandler)
}

// GetProp gets a property by name.
func (h *JSValueHandle) GetProp(name string) *JSValueHandle {
	vm := h.vm
	if stringKeyNeedsValuePath(name) {
		keyHandle := vm.NewString(name)
		defer keyHandle.Dispose()
		return vm.GetProp(h, keyHandle)
	}
	return newHandle(vm, uint32(h.stringKeyCall(name, exQjsGetPropString, exQjsGetPropValue)), false, false)
}

// SetProp sets a property by name.
func (h *JSValueHandle) SetProp(name string, value *JSValueHandle) {
	vm := h.vm
	if stringKeyNeedsValuePath(name) {
		keyHandle := vm.NewString(name)
		defer keyHandle.Dispose()
		vm.SetProp(h, keyHandle, value)
		return
	}
	h.stringKeyCall(name, exQjsSetPropString, exQjsSetPropValue, uint64(value.ptr))
}

// DefineProp defines a property with explicit property descriptor flags.
// Unlike SetProp, this allows controlling the writable, enumerable, and
// configurable attributes, matching Object.defineProperty() semantics. A
// *JSValueHandle key supports symbols. All flags default to false (nil
// descriptor).
func (h *JSValueHandle) DefineProp(key PropertyKey, value *JSValueHandle, descriptor *JSPropertyDescriptor) {
	flags := descriptorFlags(descriptor)
	switch k := key.(type) {
	case StringKey:
		h.stringKeyCall(string(k), exQjsDefinePropString, exQjsDefinePropValue, uint64(value.ptr), flags)
	case *JSValueHandle:
		h.vm.call(exQjsDefinePropValue, uint64(h.ptr), uint64(k.ptr), uint64(value.ptr), flags)
	}
}

// ToNumber extracts the value as a number.
func (h *JSValueHandle) ToNumber() float64 {
	return api.DecodeF64(h.vm.call(exQjsGetFloat64, uint64(h.ptr)))
}

// ToBigInt extracts the value as a BigInt (64-bit signed range).
func (h *JSValueHandle) ToBigInt() (*big.Int, error) {
	vm := h.vm
	loPtr := uint32(vm.call(exWasmMalloc, 4))
	hiPtr := uint32(vm.call(exWasmMalloc, 4))
	ret := int32(vm.call(exQjsGetBigInt64, uint64(h.ptr), uint64(loPtr), uint64(hiPtr)))
	if vm.failure != nil {
		return nil, vm.failure
	}
	if ret != 0 {
		vm.call(exWasmFree, uint64(loPtr))
		vm.call(exWasmFree, uint64(hiPtr))
		return nil, errors.New("Failed to convert value to BigInt")
	}
	lo := vm.readU32(loPtr)
	hi := int32(vm.readU32(hiPtr)) // signed for the high word
	vm.call(exWasmFree, uint64(loPtr))
	vm.call(exWasmFree, uint64(hiPtr))
	if vm.failure != nil {
		return nil, vm.failure
	}
	return big.NewInt(int64(hi)<<32 | int64(lo)), nil
}

// ToArrayBuffer extracts the value as ArrayBuffer bytes (copied out of WASM
// memory). Works on ArrayBuffer values. For typed arrays, it copies the viewed
// slice of the underlying buffer.
func (h *JSValueHandle) ToArrayBuffer() ([]byte, error) {
	vm := h.vm
	lenOutPtr := uint32(vm.call(exWasmMalloc, 4))
	if vm.call(exQjsIsArrayBuffer, uint64(h.ptr)) != 0 {
		dataPtr := uint32(vm.call(exQjsGetArrayBuffer, uint64(h.ptr), uint64(lenOutPtr)))
		if vm.failure != nil {
			return nil, vm.failure
		}
		if dataPtr == 0 {
			vm.call(exWasmFree, uint64(lenOutPtr))
			return nil, errors.New("Failed to get ArrayBuffer data")
		}
		length := vm.readU32(lenOutPtr)
		vm.call(exWasmFree, uint64(lenOutPtr))
		// Copy out of WASM memory.
		data := vm.readBytes(dataPtr, length)
		if vm.failure != nil {
			return nil, vm.failure
		}
		return data, nil
	}
	// Try typed array -> underlying ArrayBuffer.
	vm.call(exWasmFree, uint64(lenOutPtr))
	view, abHandle, ok := vm.typedArrayBuffer(h)
	if vm.failure != nil {
		return nil, vm.failure
	}
	if !ok {
		return nil, errors.New("Value is not an ArrayBuffer or typed array")
	}
	// Get the raw data from the underlying ArrayBuffer.
	abLenPtr := uint32(vm.call(exWasmMalloc, 4))
	abDataPtr := uint32(vm.call(exQjsGetArrayBuffer, uint64(abHandle.ptr), uint64(abLenPtr)))
	vm.call(exWasmFree, uint64(abLenPtr))
	abHandle.Dispose()
	if vm.failure != nil {
		return nil, vm.failure
	}
	if abDataPtr == 0 {
		return nil, errors.New("Failed to get ArrayBuffer data from typed array")
	}
	// Copy the relevant slice out of WASM memory.
	data := vm.readBytes(abDataPtr+view.byteOffset, view.byteLength)
	if vm.failure != nil {
		return nil, vm.failure
	}
	return data, nil
}

type typedArrayView struct {
	byteOffset, byteLength, bytesPerElement uint32
}

// typedArrayBuffer runs qjs_get_typed_array_buffer. When the value is a typed
// array it returns the view geometry and the underlying ArrayBuffer handle
// (caller disposes). Otherwise ok is false and the handle is already disposed.
func (vm *QuickJS) typedArrayBuffer(h *JSValueHandle) (view typedArrayView, abHandle *JSValueHandle, ok bool) {
	byteOffsetPtr := uint32(vm.call(exWasmMalloc, 4))
	byteLengthPtr := uint32(vm.call(exWasmMalloc, 4))
	bytesPerElemPtr := uint32(vm.call(exWasmMalloc, 4))
	abPtr := uint32(vm.call(exQjsGetTypedArrayBuffer, uint64(h.ptr), uint64(byteOffsetPtr), uint64(byteLengthPtr), uint64(bytesPerElemPtr)))
	abHandle = newHandle(vm, abPtr, false, false)
	if abHandle.check(exQjsIsException) {
		abHandle.Dispose()
		vm.call(exWasmFree, uint64(byteOffsetPtr))
		vm.call(exWasmFree, uint64(byteLengthPtr))
		vm.call(exWasmFree, uint64(bytesPerElemPtr))
		return view, nil, false
	}
	view.byteOffset = vm.readU32(byteOffsetPtr)
	view.byteLength = vm.readU32(byteLengthPtr)
	view.bytesPerElement = vm.readU32(bytesPerElemPtr)
	vm.call(exWasmFree, uint64(byteOffsetPtr))
	vm.call(exWasmFree, uint64(byteLengthPtr))
	vm.call(exWasmFree, uint64(bytesPerElemPtr))
	return view, abHandle, true
}

// ToUint8Array extracts the value as bytes (copied out of WASM memory). Works
// on Uint8Array, ArrayBuffer, and other typed array values.
func (h *JSValueHandle) ToUint8Array() ([]byte, error) {
	return h.ToArrayBuffer()
}

// ToString extracts the value as a string. Works on any value.
//
// For values that are not already strings this performs a JavaScript string
// conversion, which executes guest code: toString() / valueOf() /
// Symbol.toPrimitive on the value or its prototype chain, and proxy traps.
// Guard with IsString when the caller must not run guest code. A conversion
// that throws yields "<null>" (the exception stays pending).
//
// The result is length-aware WTF-8: embedded U+0000 code units survive, and
// lone surrogates come back as WTF-8 surrogate sequences.
func (h *JSValueHandle) ToString() string {
	vm := h.vm
	lenPtr := uint32(vm.call(exWasmMalloc, 4))
	if vm.failure != nil {
		return ""
	}
	// A 0 return would make qjs_get_string_len write the length to address
	// 0: silent corruption instead of an error.
	if lenPtr == 0 {
		vm.fail(errMallocFailed)
		return ""
	}
	defer vm.call(exWasmFree, uint64(lenPtr))
	cstrPtr := uint32(vm.call(exQjsGetStringLen, uint64(h.ptr), uint64(lenPtr)))
	if vm.failure != nil {
		return ""
	}
	if cstrPtr == 0 {
		return "<null>"
	}
	length := vm.readU32(lenPtr)
	str := decodeWtf8(vm.readBytes(cstrPtr, length))
	vm.call(exQjsFreeCstring, uint64(cstrPtr))
	return str
}

// Consume uses this handle, then disposes it.
func (h *JSValueHandle) Consume(fn func(h *JSValueHandle)) {
	defer h.Dispose()
	fn(h)
}

// ConsumeHandle calls fn with the handle, then disposes it, and returns fn's
// result (TS: handle.consume(fn) with a return value).
func ConsumeHandle[T any](h *JSValueHandle, fn func(h *JSValueHandle) T) T {
	defer h.Dispose()
	return fn(h)
}

// Dup duplicates this handle (increments the refcount).
func (h *JSValueHandle) Dup() *JSValueHandle {
	return newHandle(h.vm, uint32(h.vm.call(exQjsDupValue, uint64(h.ptr))), false, false)
}

// Dispose disposes this handle, freeing the heap-allocated JSValue. Safe to
// call after the VM has been disposed (becomes a no-op).
func (h *JSValueHandle) Dispose() {
	// Cached singleton handles (undefined/null/true/false/global) share a
	// single heap-allocated JSValue that the VM keeps referencing. Freeing it
	// here would leave the cached handle pointing at freed memory, so
	// disposing a singleton is intentionally a no-op. Borrowed handles
	// (host-callback this/arguments) wrap pointers owned by the C caller,
	// which frees them itself after the call returns.
	if h.singleton || h.borrowed {
		return
	}
	if !h.disposed {
		h.disposed = true
		if h.onDispose != nil {
			onDispose := h.onDispose
			h.onDispose = nil
			onDispose()
		}
		// If the VM is already disposed, the WASM instance is gone; no
		// need to (and we can't) call qjs_free_value.
		if !h.vm.disposed {
			h.vm.call(exQjsFreeValue, uint64(h.ptr))
		}
	}
}

// handleSet is an insertion-ordered set of handles (TS: Set<JSValueHandle>).
type handleSet struct {
	order []*JSValueHandle
	index map[*JSValueHandle]int
}

func newHandleSet() *handleSet {
	return &handleSet{index: map[*JSValueHandle]int{}}
}

func (s *handleSet) add(h *JSValueHandle) {
	if _, ok := s.index[h]; ok {
		return
	}
	s.index[h] = len(s.order)
	s.order = append(s.order, h)
}

func (s *handleSet) delete(h *JSValueHandle) {
	if i, ok := s.index[h]; ok {
		s.order[i] = nil
		delete(s.index, h)
	}
}

func (s *handleSet) each(fn func(h *JSValueHandle)) {
	for _, h := range s.order {
		if h != nil {
			fn(h)
		}
	}
}
