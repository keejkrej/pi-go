// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed quickjs.wasm
var embeddedWasm []byte

// VERSION is the quickjs-wasi npm package version this port matches (dist/version.js).
const VERSION = "3.6.2"

// MaxStackSize is the largest supported QuickJS native stack limit for the shipped WASM binary.
//
// The binary has a 1 MiB linker-defined stack; reserving half of it leaves
// headroom for native frames and stack-overflow exception handling.
const MaxStackSize = 512 * 1024

// Flags for EvalCode(), matching the QuickJS JS_EVAL_* constants (TS: EvalFlags).
const (
	// EvalFlagsTypeGlobal is global script mode (default).
	EvalFlagsTypeGlobal = 0
	// EvalFlagsTypeModule is module mode. EvalCode() returns a handle to a Promise that resolves
	// to the module's namespace object (its exports), or rejects if module
	// evaluation throws. Use together with ExecutePendingJobs() and
	// ResolvePromise().
	EvalFlagsTypeModule = 1 << 0
	// EvalFlagsStrict forces strict mode.
	EvalFlagsStrict = 1 << 3
	// EvalFlagsCompileOnly compiles only; does not execute.
	EvalFlagsCompileOnly = 1 << 5
	// EvalFlagsBacktraceBarrier omits stack frames before this eval from Error backtraces.
	EvalFlagsBacktraceBarrier = 1 << 6
	// EvalFlagsAsync allows top-level await in global scripts. When used, EvalCode()
	// returns a handle to a Promise that resolves to the completion value.
	// Use together with ExecutePendingJobs() and ResolvePromise().
	EvalFlagsAsync = 1 << 7
)

// Flags for Compile() controlling what is included in the bytecode output.
// These can be combined with bitwise OR (TS: CompileFlags).
const (
	// CompileFlagsStripSource strips source code from the bytecode (smaller output, no source in errors).
	CompileFlagsStripSource = 1 << 4
	// CompileFlagsStripDebug strips debug information (line numbers, etc.) from the bytecode.
	CompileFlagsStripDebug = 1 << 5
)

// Intrinsic flags for QuickJSOptions.Intrinsics controlling which built-in
// JavaScript features are available in the VM (TS: Intrinsics).
//
// By default all intrinsics are enabled. Pass a bitmask of these flags
// to create a minimal context. For example, omit IntrinsicsEval to
// prevent eval() usage, or omit IntrinsicsProxy to disallow Proxy.
//
// BaseObjects (Object, Array, Number, String, Boolean, Error, etc.)
// is always included and cannot be disabled.
const (
	// IntrinsicsDate is the Date constructor and prototype methods.
	IntrinsicsDate uint32 = 1 << 0
	// IntrinsicsEval is eval() and the Function() constructor.
	IntrinsicsEval uint32 = 1 << 1
	// IntrinsicsRegexp is the RegExp constructor, prototype methods, and regex literals.
	IntrinsicsRegexp uint32 = 1 << 2
	// IntrinsicsJSON is JSON.parse() and JSON.stringify().
	IntrinsicsJSON uint32 = 1 << 3
	// IntrinsicsProxy is Proxy and Reflect.
	IntrinsicsProxy uint32 = 1 << 4
	// IntrinsicsMapSet is Map, Set, WeakMap, WeakSet.
	IntrinsicsMapSet uint32 = 1 << 5
	// IntrinsicsTypedArrays is ArrayBuffer, the TypedArray variants, DataView.
	IntrinsicsTypedArrays uint32 = 1 << 6
	// IntrinsicsPromise is Promise, async/await.
	IntrinsicsPromise uint32 = 1 << 7
	// IntrinsicsBigInt is BigInt. Note: BigInt is part of BaseObjects in quickjs-ng and cannot be fully removed.
	IntrinsicsBigInt uint32 = 1 << 8
	// IntrinsicsWeakRef is WeakRef and FinalizationRegistry.
	IntrinsicsWeakRef uint32 = 1 << 9
	// IntrinsicsPerformance is performance.now().
	IntrinsicsPerformance uint32 = 1 << 10
	// IntrinsicsDomException is the DOMException class.
	IntrinsicsDomException uint32 = 1 << 11
	// IntrinsicsAtobBtoa is the atob() and btoa() global functions. Also pulls in DOMException as
	// a dependency (errors thrown by these functions are DOMExceptions).
	IntrinsicsAtobBtoa uint32 = 1 << 12
	// IntrinsicsAll enables all intrinsics (default).
	IntrinsicsAll uint32 = 0xFFFFFFFF
)

// ---- Snapshot serialization constants ----

// snapshotMagic is the magic bytes "QJSS" (QuickJS Snapshot).
const snapshotMagic = 0x514A5353

// snapshotVersion is the current serialization format version (2 = added extension metadata).
const snapshotVersion = 2

// snapshotHeaderSize is the fixed header size.
//
// Header layout (version 2):
//
//	0-3:   Magic "QJSS" (u32 big-endian)
//	4:     Version (u8)
//	5-7:   Reserved (zero)
//	8-11:  Memory size in bytes (u32 little-endian)
//	12-15: Stack pointer (u32 little-endian)
//	16-19: Runtime pointer (u32 little-endian)
//	20-23: Context pointer (u32 little-endian)
//	24-27: Extension count (u32 little-endian)
//	28+:   Extension entries (variable length):
//	       nameLen(u32) + name(utf8) + memoryBase(u32) + tableBase(u32) + initFnLen(u32) + initFn(utf8)
//	N+:    Memory data (N = memory size from offset 8)
//
// Version 1 (legacy): no extension metadata, memory starts at offset 24.
const snapshotHeaderSize = 24

// ---- Public types ----

// HostFunction is a host callback backing a guest function. this and args are
// borrowed: they are owned by the C trampoline and freed when the callback
// returns, so a callback that retains one must Dup() it. A non-nil error is
// thrown inside the guest as an Error (TS: a thrown host exception). A nil
// result means undefined.
type HostFunction func(this *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error)

// HandleScope is a batch of handles created inside WithScope(), disposed together
// when the scope ends.
type HandleScope struct {
	tracked   *handleSet
	enclosing *handleSet
}

// Escape removes a handle from the scope so that it outlives it. The handle is
// transferred to the enclosing scope when there is one, otherwise it
// becomes the caller's responsibility to dispose.
//
// Use this for the value you intend to return.
func (s *HandleScope) Escape(handle *JSValueHandle) *JSValueHandle {
	s.tracked.delete(handle)
	if s.enclosing != nil {
		s.enclosing.add(handle)
	}
	return handle
}

// JSPropertyDescriptor holds the property descriptor flags for DefineProp().
type JSPropertyDescriptor struct {
	Writable     bool
	Enumerable   bool
	Configurable bool
}

// JSOwnPropertyDescriptor is an own-property descriptor returned by
// JSValueHandle.GetOwnPropertyDescriptor. It mirrors the result of
// Object.getOwnPropertyDescriptor(): a data property carries Value +
// Writable, an accessor property carries Get + Set.
//
// The Value/Get/Set handles are owned by the caller and must be disposed.
type JSOwnPropertyDescriptor struct {
	// Value is present for data properties. Caller must dispose.
	Value *JSValueHandle
	// Get is present for accessor properties (may be an undefined handle). Caller must dispose.
	Get *JSValueHandle
	// Set is present for accessor properties (may be an undefined handle). Caller must dispose.
	Set *JSValueHandle
	// Writable is present for data properties.
	Writable     *bool
	Enumerable   bool
	Configurable bool
}

// PropertyKey is a property key: a StringKey or a *JSValueHandle (which supports
// symbols, including Symbol.for()). TS: string | JSValueHandle.
type PropertyKey interface{ isPropertyKey() }

// StringKey is a string property key.
type StringKey string

func (StringKey) isPropertyKey() {}

func (*JSValueHandle) isPropertyKey() {}

// MemoryUsage holds memory usage statistics from the QuickJS runtime.
type MemoryUsage struct {
	// MallocSize is the total bytes allocated via malloc.
	MallocSize int `json:"mallocSize"`
	// MallocLimit is the current malloc limit (0 for unlimited).
	MallocLimit int `json:"mallocLimit"`
	// MemoryUsedSize is the total memory used (including overhead).
	MemoryUsedSize int `json:"memoryUsedSize"`
	// MallocCount is the number of malloc calls.
	MallocCount int `json:"mallocCount"`
	// MemoryUsedCount is the number of memory-using objects.
	MemoryUsedCount int `json:"memoryUsedCount"`
	// AtomCount is the number of atoms.
	AtomCount int `json:"atomCount"`
	// AtomSize is the atom memory size.
	AtomSize int `json:"atomSize"`
	// StrCount is the number of strings.
	StrCount int `json:"strCount"`
	// StrSize is the string memory size.
	StrSize int `json:"strSize"`
	// ObjCount is the number of objects.
	ObjCount int `json:"objCount"`
	// ObjSize is the object memory size.
	ObjSize int `json:"objSize"`
	// PropCount is the number of properties.
	PropCount int `json:"propCount"`
	// PropSize is the property memory size.
	PropSize int `json:"propSize"`
	// ShapeCount is the number of shapes.
	ShapeCount int `json:"shapeCount"`
	// ShapeSize is the shape memory size.
	ShapeSize int `json:"shapeSize"`
	// JsFuncCount is the number of JS functions.
	JsFuncCount int `json:"jsFuncCount"`
	// JsFuncSize is the JS function memory size.
	JsFuncSize int `json:"jsFuncSize"`
	// JsFuncCodeSize is the JS function code size.
	JsFuncCodeSize int `json:"jsFuncCodeSize"`
	// JsFuncPc2lineCount is the number of PC-to-line mappings.
	JsFuncPc2lineCount int `json:"jsFuncPc2lineCount"`
	// JsFuncPc2lineSize is the PC-to-line mapping memory size.
	JsFuncPc2lineSize int `json:"jsFuncPc2lineSize"`
	// CFuncCount is the number of C functions.
	CFuncCount int `json:"cFuncCount"`
	// ArrayCount is the number of arrays.
	ArrayCount int `json:"arrayCount"`
	// FastArrayCount is the number of fast arrays.
	FastArrayCount int `json:"fastArrayCount"`
	// FastArrayElements is the number of fast array elements.
	FastArrayElements int `json:"fastArrayElements"`
	// BinaryObjectCount is the number of binary objects (ArrayBuffer, etc.).
	BinaryObjectCount int `json:"binaryObjectCount"`
	// BinaryObjectSize is the binary object memory size.
	BinaryObjectSize int `json:"binaryObjectSize"`
}

// WasmSource is the QuickJSOptions.Wasm union: WasmBytes or a compiled *Module
// (TS: BufferSource | WebAssembly.Module).
type WasmSource interface{ isWasmSource() }

// WasmBytes is a raw quickjs.wasm binary, compiled on use.
type WasmBytes []byte

func (WasmBytes) isWasmSource() {}

// TimezoneOffset is the QuickJSOptions.TimezoneOffset union
// (TS: 'host' | number | ((timeSecs: number) => number)).
type TimezoneOffset interface{ isTimezoneOffset() }

// TimezoneOffsetHost mirrors the host environment's timezone (default).
type TimezoneOffsetHost struct{}

// TimezoneOffsetMinutes is a fixed UTC offset in minutes, following the
// getTimezoneOffset() sign convention where west of UTC is positive (-480 for UTC+8).
type TimezoneOffsetMinutes float64

// TimezoneOffsetFunc is called with seconds since epoch and must return the UTC
// offset in minutes for that instant (getTimezoneOffset() convention).
type TimezoneOffsetFunc func(timeSecs float64) float64

func (TimezoneOffsetHost) isTimezoneOffset()    {}
func (TimezoneOffsetMinutes) isTimezoneOffset() {}
func (TimezoneOffsetFunc) isTimezoneOffset()    {}

// ModuleLoader resolves and loads ES modules for import statements.
//
// Both callbacks are synchronous: the engine calls them from inside the WASM
// call stack. Errors returned by either callback propagate to the guest as the
// module resolution error.
type ModuleLoader struct {
	// Normalize resolves a module specifier relative to the importing module.
	// It receives the name of the module containing the import statement and the
	// raw specifier, and returns the normalized/canonical module name.
	// If nil, specifiers are passed through to Load unchanged.
	Normalize func(baseName, specifier string) (string, error)
	// Load returns the source code of a module, given its normalized name (from
	// Normalize, or the raw specifier).
	Load func(moduleName string) (string, error)
}

// QuickJSOptions configures QuickJSCreate and QuickJSRestore.
type QuickJSOptions struct {
	// Wasm is the WASM module bytes or a pre-compiled module. Go-only default:
	// nil uses the embedded quickjs.wasm (EmbeddedModule).
	Wasm WasmSource
	// Wasi holds custom WASI function implementations.
	Wasi WasiOptions
	// MemoryLimit is the maximum memory the QuickJS runtime can allocate, in bytes.
	// When exceeded, allocations fail and surface as JS exceptions
	// (e.g. "InternalError: out of memory").
	MemoryLimit *int
	// MaxStackSize is the maximum native stack space QuickJS may consume, in bytes.
	// Must be between 0 and MaxStackSize. Set to 0 to disable the QuickJS stack guard.
	MaxStackSize *int
	// InterruptHandler is called periodically during JS execution. Return true to
	// interrupt the current execution with an "InternalError: interrupted"
	// exception. It runs on the goroutine executing the VM, so it should be fast
	// and read shared state atomically.
	InterruptHandler func() bool
	// OnUnhandledRejection is called when a promise is rejected without a handler
	// (isHandled false), or when a handler is attached to a previously unhandled
	// rejection (isHandled true). Both handles are disposed automatically after
	// the callback returns.
	OnUnhandledRejection func(promise, reason *JSValueHandle, isHandled bool)
	// ModuleLoader enables ES module import statements.
	ModuleLoader *ModuleLoader
	// Intrinsics is a bitmask of Intrinsics* flags. By default all intrinsics are enabled.
	Intrinsics *uint32
	// TimezoneOffset controls the timezone used by Date within the sandbox.
	// nil means TimezoneOffsetHost.
	TimezoneOffset TimezoneOffset
}

// ---- Compiled modules and the shared wazero runtime ----

// Module is a compiled quickjs-wasi module (TS: WebAssembly.Module). One Module
// can back any number of VMs.
type Module struct {
	compiled wazero.CompiledModule
}

func (*Module) isWasmSource() {}

// EmbeddedWasm returns the embedded quickjs-wasi 3.6.2 quickjs.wasm binary.
// Callers must not modify it.
func EmbeddedWasm() []byte {
	return embeddedWasm
}

// CompileModule compiles a quickjs.wasm binary (TS: WebAssembly.compile).
func CompileModule(ctx context.Context, wasm []byte) (*Module, error) {
	rt, err := sharedRuntime()
	if err != nil {
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return nil, err
	}
	return &Module{compiled: compiled}, nil
}

var embeddedModule struct {
	mu     sync.Mutex
	module *Module
}

// EmbeddedModule compiles the embedded quickjs.wasm once per process and returns
// it. A failed compilation is retried on the next call.
func EmbeddedModule(ctx context.Context) (*Module, error) {
	embeddedModule.mu.Lock()
	defer embeddedModule.mu.Unlock()
	if embeddedModule.module != nil {
		return embeddedModule.module, nil
	}
	module, err := CompileModule(ctx, embeddedWasm)
	if err != nil {
		return nil, err
	}
	embeddedModule.module = module
	return module, nil
}

var sharedWazero struct {
	once sync.Once
	rt   wazero.Runtime
	err  error
}

// sharedRuntime returns the process-wide wazero runtime. Every VM is a separate
// module instance in it. The env and wasi_snapshot_preview1 host modules are
// shared: their functions find the calling VM through the call context.
// WithCloseOnContextDone makes a done VM context abort a running call.
func sharedRuntime() (wazero.Runtime, error) {
	sharedWazero.once.Do(func() {
		ctx := context.Background()
		rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
		if err := instantiateHostModules(ctx, rt); err != nil {
			_ = rt.Close(ctx)
			sharedWazero.err = err
			return
		}
		sharedWazero.rt = rt
	})
	return sharedWazero.rt, sharedWazero.err
}

type vmContextKey struct{}

// vmFromContext returns the VM whose wasm call is running. Host imports are
// only ever invoked from inside a VM call, whose context carries the VM.
func vmFromContext(ctx context.Context) *QuickJS {
	vm, ok := ctx.Value(vmContextKey{}).(*QuickJS)
	if !ok {
		panic("quickjs: host import called outside a VM call")
	}
	return vm
}

// abortIfFailed unwinds the running wasm call when the VM has failed (a nested
// call trapped, the context ended, or linear memory ran out), the analog of a
// host exception propagating through the wasm frames in TS. wazero turns the
// panic into an error for the outermost exported call.
func (vm *QuickJS) abortIfFailed() {
	if vm.failure != nil {
		panic(vm.failure)
	}
}

func instantiateHostModules(ctx context.Context, rt wazero.Runtime) error {
	i32, i64 := api.ValueTypeI32, api.ValueTypeI64
	types := func(ts ...api.ValueType) []api.ValueType { return ts }
	export := func(b wazero.HostModuleBuilder, name string, params, results []api.ValueType, fn api.GoModuleFunc) {
		b.NewFunctionBuilder().WithGoModuleFunction(fn, params, results).Export(name)
	}

	env := rt.NewHostModuleBuilder("env")
	export(env, "host_call", types(i32, i32, i32, i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.handleHostCall(uint32(stack[0]), uint32(stack[1]), uint32(stack[2]), uint32(stack[3]), uint32(stack[4])))
		vm.abortIfFailed()
	})
	export(env, "host_interrupt", nil, types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		var interrupted uint64
		if vm.interruptHandler != nil && vm.interruptHandler() {
			interrupted = 1
		}
		stack[0] = interrupted
	})
	export(env, "host_promise_rejection", types(i32, i32, i32), nil, func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		vm.hostPromiseRejection(uint32(stack[0]), uint32(stack[1]), int32(stack[2]))
		vm.abortIfFailed()
	})
	export(env, "host_module_normalize", types(i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.hostModuleNormalize(uint32(stack[0]), uint32(stack[1])))
		vm.abortIfFailed()
	})
	export(env, "host_module_load", types(i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.hostModuleLoad(uint32(stack[0]), uint32(stack[1])))
		vm.abortIfFailed()
	})
	export(env, "host_get_timezone_offset", types(i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(uint32(vm.hostGetTimezoneOffset(int32(stack[0]), int32(stack[1]))))
	})
	if _, err := env.Instantiate(ctx); err != nil {
		return err
	}

	wasi := rt.NewHostModuleBuilder("wasi_snapshot_preview1")
	export(wasi, "clock_time_get", types(i32, i64, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.ClockTimeGet(uint32(stack[0]), stack[1], uint32(stack[2])))
		vm.abortIfFailed()
	})
	export(wasi, "fd_close", types(i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.FdClose(uint32(stack[0])))
		vm.abortIfFailed()
	})
	export(wasi, "fd_fdstat_get", types(i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.FdFdstatGet(uint32(stack[0]), uint32(stack[1])))
		vm.abortIfFailed()
	})
	export(wasi, "fd_seek", types(i32, i64, i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.FdSeek(uint32(stack[0]), int64(stack[1]), uint32(stack[2]), uint32(stack[3])))
		vm.abortIfFailed()
	})
	export(wasi, "fd_write", types(i32, i32, i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.FdWrite(uint32(stack[0]), uint32(stack[1]), uint32(stack[2]), uint32(stack[3])))
		vm.abortIfFailed()
	})
	export(wasi, "random_get", types(i32, i32), types(i32), func(ctx context.Context, _ api.Module, stack []uint64) {
		vm := vmFromContext(ctx)
		stack[0] = uint64(vm.wasi.RandomGet(uint32(stack[0]), uint32(stack[1])))
		vm.abortIfFailed()
	})
	_, err := wasi.Instantiate(ctx)
	return err
}

// ---- QuickJS VM ----

// QuickJS is one QuickJS VM: a wasm instance with its own linear memory,
// runtime, and context.
type QuickJS struct {
	module *Module
	mod    api.Module
	memory api.Memory
	// ctx carries the VM (for host imports) and bounds every wasm call.
	ctx context.Context
	// funcs holds idle wazero call engines per export. A call engine cannot be
	// re-entered, and host callbacks call back into the VM while an outer call
	// of the same export is still running, so each call takes its own engine.
	funcs [exportCount][]api.Function
	// callDepth counts wasm calls in progress (nested through host callbacks).
	callDepth int
	disposed  bool
	// failure is the first wasm-level failure (trap, aborted call, exhausted
	// linear memory). Afterwards no wasm code runs and error-returning methods
	// report it.
	failure error
	// hostCallbacks is the registry of host callbacks, keyed by function name.
	hostCallbacks map[string]HostFunction
	// nextInternalId is the counter for internal-only callbacks (e.g. promise settle handlers).
	nextInternalId            int
	interruptHandler          func() bool
	unhandledRejectionHandler func(promise, reason *JSValueHandle, isHandled bool)
	moduleNormalizeHandler    func(baseName, specifier string) (string, error)
	moduleLoadHandler         func(moduleName string) (string, error)
	timezoneOffsetHandler     func(timeSecs float64) float64
	// Cached singleton handles.
	global     *JSValueHandle
	versions   map[string]string
	undefined  *JSValueHandle
	null       *JSValueHandle
	trueValue  *JSValueHandle
	falseValue *JSValueHandle
	// ownedHandles holds handles that must be freed on dispose (e.g. unresolved promise resolve/reject functions).
	ownedHandles map[*JSValueHandle]struct{}
	// activeScope is the innermost active WithScope() batch, if any. New
	// non-singleton handles register themselves here so they can be freed together.
	activeScope *handleSet
	wasi        *WasiImports
	memoryProxy *Memory
}

func newQuickJS(module *Module) *QuickJS {
	return &QuickJS{
		module:         module,
		hostCallbacks:  map[string]HostFunction{},
		nextInternalId: 1,
		ownedHandles:   map[*JSValueHandle]struct{}{},
	}
}

// ---- Cached property accessors ----

// Versions reports version information for the runtime and loaded native
// libraries. It always includes "quickjs-wasi" (the npm package version) and
// "quickjs" (the QuickJS engine version).
func (vm *QuickJS) Versions() map[string]string {
	vm.mustNotBeDisposed()
	if vm.versions == nil {
		vm.versions = map[string]string{
			"quickjs-wasi": VERSION,
			"quickjs":      vm.readCString(uint32(vm.call(exQjsGetQuickjsVersion))),
		}
	}
	return vm.versions
}

// Global returns the global object. Cached; do not dispose.
func (vm *QuickJS) Global() *JSValueHandle {
	if vm.global == nil {
		vm.global = newHandle(vm, uint32(vm.call(exQjsGetGlobal)), true, false)
	}
	return vm.global
}

// Undefined returns the undefined value. Cached; do not dispose.
func (vm *QuickJS) Undefined() *JSValueHandle {
	if vm.undefined == nil {
		vm.undefined = newHandle(vm, uint32(vm.call(exQjsGetUndefined)), true, false)
	}
	return vm.undefined
}

// Null returns the null value. Cached; do not dispose.
func (vm *QuickJS) Null() *JSValueHandle {
	if vm.null == nil {
		vm.null = newHandle(vm, uint32(vm.call(exQjsGetNull)), true, false)
	}
	return vm.null
}

// True returns the true value. Cached; do not dispose.
func (vm *QuickJS) True() *JSValueHandle {
	if vm.trueValue == nil {
		vm.trueValue = newHandle(vm, uint32(vm.call(exQjsGetTrue)), true, false)
	}
	return vm.trueValue
}

// False returns the false value. Cached; do not dispose.
func (vm *QuickJS) False() *JSValueHandle {
	if vm.falseValue == nil {
		vm.falseValue = newHandle(vm, uint32(vm.call(exQjsGetFalse)), true, false)
	}
	return vm.falseValue
}

// QuickJSCreate creates a fresh QuickJS VM instance (TS: QuickJS.create).
//
// ctx bounds the VM's lifetime: when it is done, a running wasm call is aborted,
// the instance is closed, and the VM fails with a *WasmError. options may be nil.
func QuickJSCreate(ctx context.Context, options *QuickJSOptions) (*QuickJS, error) {
	opts, err := quickjsNormalizeOptions(options)
	if err != nil {
		return nil, err
	}
	module, err := quickjsResolveModule(ctx, opts.Wasm)
	if err != nil {
		return nil, err
	}
	vm := newQuickJS(module)
	if err := quickjsInstantiate(ctx, module, vm, opts.Wasi); err != nil {
		return nil, err
	}
	// Initialize the WASI reactor.
	vm.call(exInitialize)
	// Initialize QuickJS runtime and context.
	var result uint64
	if opts.Intrinsics != nil {
		result = vm.call(exQjsInit2, uint64(*opts.Intrinsics))
	} else {
		result = vm.call(exQjsInit)
	}
	if vm.failure != nil {
		err := vm.failure
		vm.Dispose()
		return nil, err
	}
	if int32(result) != 0 {
		vm.Dispose()
		return nil, errors.New("Failed to initialize QuickJS runtime")
	}
	// Apply runtime limits.
	quickjsApplyLimits(vm, opts)
	if vm.failure != nil {
		err := vm.failure
		vm.Dispose()
		return nil, err
	}
	return vm, nil
}

// QuickJSRestore restores a QuickJS VM from a snapshot (TS: QuickJS.restore).
// ctx and options are as for QuickJSCreate. Host callbacks must be
// re-registered with RegisterHostCallback.
func QuickJSRestore(ctx context.Context, snapshot *Snapshot, options *QuickJSOptions) (*QuickJS, error) {
	opts, err := quickjsNormalizeOptions(options)
	if err != nil {
		return nil, err
	}
	module, err := quickjsResolveModule(ctx, opts.Wasm)
	if err != nil {
		return nil, err
	}
	vm := newQuickJS(module)
	if err := quickjsInstantiate(ctx, module, vm, opts.Wasi); err != nil {
		return nil, err
	}
	// Grow memory first, to the snapshot's size.
	currentPages := vm.memory.Size() / 65536
	neededPages := uint32((len(snapshot.Memory) + 65535) / 65536)
	if neededPages > currentPages {
		if _, ok := vm.memory.Grow(neededPages - currentPages); !ok {
			vm.Dispose()
			return nil, &HostError{name: "RangeError", message: "WebAssembly.Memory.grow(): Maximum memory size exceeded"}
		}
	}
	// Native extensions are not supported, so a snapshot that needs any cannot
	// be restored (TS: no matching descriptor was provided).
	if len(snapshot.Extensions) > 0 {
		vm.Dispose()
		return nil, errors.New(`Extension "` + snapshot.Extensions[0].Name + `" required by snapshot but not provided`)
	}
	// Copy snapshot data into the module's own memory.
	if !vm.memory.Write(0, snapshot.Memory) {
		vm.Dispose()
		return nil, &HostError{name: "RangeError", message: "offset is out of bounds"}
	}
	// Set runtime/context pointers (they already exist in the restored memory).
	vm.call(exQjsSetRuntimeAndContext, uint64(uint32(snapshot.RuntimePtr)), uint64(uint32(snapshot.ContextPtr)))
	// Restore the stack pointer.
	if g, ok := vm.mod.ExportedGlobal("__stack_pointer").(api.MutableGlobal); ok {
		g.Set(uint64(uint32(snapshot.StackPointer)))
	} else {
		vm.fail(errors.New("Main module does not export a mutable __stack_pointer"))
	}
	// Apply runtime limits.
	quickjsApplyLimits(vm, opts)
	if vm.failure != nil {
		err := vm.failure
		vm.Dispose()
		return nil, err
	}
	return vm, nil
}

func quickjsNormalizeOptions(options *QuickJSOptions) (*QuickJSOptions, error) {
	if options == nil {
		return &QuickJSOptions{}, nil
	}
	if options.MaxStackSize != nil && (*options.MaxStackSize < 0 || *options.MaxStackSize > MaxStackSize) {
		return nil, &HostError{name: "RangeError", message: fmt.Sprintf("maxStackSize must be an integer between 0 and %d", MaxStackSize)}
	}
	return options, nil
}

func quickjsApplyLimits(vm *QuickJS, opts *QuickJSOptions) {
	if opts.MemoryLimit != nil {
		vm.call(exQjsSetMemoryLimit, uint64(uint32(*opts.MemoryLimit)))
	}
	if opts.MaxStackSize != nil {
		vm.call(exQjsSetMaxStackSize, uint64(uint32(*opts.MaxStackSize)))
	}
	if opts.InterruptHandler != nil {
		vm.interruptHandler = opts.InterruptHandler
		vm.call(exQjsSetInterruptHandler, 1)
	}
	if opts.OnUnhandledRejection != nil {
		vm.unhandledRejectionHandler = opts.OnUnhandledRejection
		vm.call(exQjsSetPromiseRejectionHandler, 1)
	}
	if opts.ModuleLoader != nil {
		vm.moduleLoadHandler = opts.ModuleLoader.Load
		vm.moduleNormalizeHandler = opts.ModuleLoader.Normalize
		vm.call(exQjsSetModuleLoader, 1)
	}
	// Configure the timezone handler.
	// The internal handler always returns the UTC offset in *seconds*
	// (positive east of UTC), which is what libc's __secs_to_zone expects.
	switch tz := opts.TimezoneOffset.(type) {
	case TimezoneOffsetFunc:
		// User callback returns minutes (getTimezoneOffset convention:
		// positive west of UTC). Convert to seconds with sign flip.
		vm.timezoneOffsetHandler = func(timeSecs float64) float64 { return -tz(timeSecs) * 60 }
	case TimezoneOffsetMinutes:
		// Fixed offset in minutes, convert to seconds with sign flip.
		offsetSecs := -float64(tz) * 60
		vm.timezoneOffsetHandler = func(float64) float64 { return offsetSecs }
	default:
		// 'host' (default): use the host's timezone.
		vm.timezoneOffsetHandler = hostTimezoneOffsetSeconds
	}
}

// hostTimezoneOffsetSeconds is -new Date(timeSecs * 1000).getTimezoneOffset() * 60
// for the host's local timezone: the offset east of UTC in seconds, NaN for an
// invalid date.
func hostTimezoneOffsetSeconds(timeSecs float64) float64 {
	ms := timeSecs * 1000
	// TimeClip: dates beyond 8.64e15 ms are invalid.
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return math.NaN()
	}
	_, offset := time.UnixMilli(int64(ms)).In(time.Local).Zone()
	// V8's getTimezoneOffset is whole minutes, truncated toward zero: New
	// York's LMT of -4:56:02 yields 296, so the handler returns -17760.
	minutes := -offset / 60
	return -float64(minutes) * 60
}

func quickjsResolveModule(ctx context.Context, wasmInput WasmSource) (*Module, error) {
	switch w := wasmInput.(type) {
	case *Module:
		if w != nil {
			return w, nil
		}
	case WasmBytes:
		if w != nil {
			return CompileModule(ctx, w)
		}
	}
	// Go-only default: the embedded quickjs.wasm. TS requires the option and throws
	// a TypeError explaining how to load the binary.
	return EmbeddedModule(ctx)
}

func quickjsInstantiate(ctx context.Context, module *Module, vm *QuickJS, wasiOptions WasiOptions) error {
	rt, err := sharedRuntime()
	if err != nil {
		return err
	}
	// The memory proxy defers to the actual memory once set. This allows WASI
	// override factories to close over the memory before the instance exists.
	vm.memoryProxy = &Memory{}
	// Build the builtins (no user overrides).
	wasiBuiltins := createWasiShim(func() api.Memory { return vm.memory })
	// Resolve user overrides via factory.
	var wasiUserOverrides *WasiImports
	if wasiOptions != nil {
		wasiUserOverrides = wasiOptions(vm.memoryProxy)
	}
	// Final shim for the main module: builtins + user overrides.
	vm.wasi = wasiBuiltins.merge(wasiUserOverrides)
	vm.ctx = context.WithValue(ctx, vmContextKey{}, vm)
	instance, err := rt.InstantiateModule(vm.ctx, module.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return err
	}
	memory := instance.ExportedMemory("memory")
	if memory == nil {
		_ = instance.Close(context.Background())
		return errors.New("Main module does not export memory")
	}
	vm.mod = instance
	vm.memory = memory
	vm.memoryProxy.mem = memory
	return nil
}

// handleHostCall is called from WASM when a host function is invoked from QuickJS code.
func (vm *QuickJS) handleHostCall(namePtr, nameLen, thisPtr, argc, argvPtr uint32) uint32 {
	name := textDecode(vm.readBytes(namePtr, nameLen))
	callback, ok := vm.hostCallbacks[name]
	if !ok {
		// Throw inside the guest, as the docs promise: NewEphemeralFunction
		// ("calling it after the handle is disposed throws, because the
		// callback is gone") and UnregisterHostCallback ("any QuickJS
		// function still referencing the name will throw when called").
		// A string (not a host error object): the error is library-generated;
		// there is no host stack worth preserving.
		errHandle := vm.NewError(`Host callback "` + name + `" is not registered: it was unregistered, ` +
			`its ephemeral function handle was disposed, or it was never ` +
			`re-registered after a snapshot restore.`)
		vm.call(exQjsThrow, uint64(errHandle.ptr))
		errHandle.Dispose()
		return 0
	}
	// thisPtr and the argv entries are OWNED BY THE C TRAMPOLINE, which
	// frees them after this call returns. Wrap them as borrowed handles:
	// Dispose() is a no-op and they are exempt from WithScope() tracking.
	// Callbacks retain arguments past their invocation via Dup().
	thisHandle := newHandle(vm, thisPtr, false, true)
	var args []*JSValueHandle
	if argc > 0 && argvPtr != 0 {
		args = make([]*JSValueHandle, 0, argc)
		for i := range argc {
			argPtr := vm.readU32(argvPtr + i*4)
			args = append(args, newHandle(vm, argPtr, false, true))
		}
	}
	result, err := callback(thisHandle, args...)
	if err != nil {
		// Throw an exception inside QuickJS and return NULL to signal
		// to the C trampoline that an exception was thrown.
		errHandle := vm.NewError(err)
		vm.call(exQjsThrow, uint64(errHandle.ptr))
		errHandle.Dispose()
		return 0
	}
	if result == nil {
		result = vm.Undefined()
	}
	return uint32(vm.call(exQjsDupValue, uint64(result.ptr)))
}

func (vm *QuickJS) hostPromiseRejection(promisePtr, reasonPtr uint32, isHandled int32) {
	if vm.unhandledRejectionHandler == nil {
		// No handler registered; free the heap-allocated values.
		vm.call(exQjsFreeValue, uint64(promisePtr))
		vm.call(exQjsFreeValue, uint64(reasonPtr))
		return
	}
	promise := newHandle(vm, promisePtr, false, false)
	reason := newHandle(vm, reasonPtr, false, false)
	defer func() {
		promise.Dispose()
		reason.Dispose()
	}()
	vm.unhandledRejectionHandler(promise, reason, isHandled != 0)
}

// throwIntoContext throws a host-side error into the QuickJS context so module
// loader failures surface with their real message instead of the generic
// "could not load module" error.
func (vm *QuickJS) throwIntoContext(err error) {
	errHandle := vm.NewError(err)
	vm.call(exQjsThrow, uint64(errHandle.ptr))
	errHandle.Dispose()
}

// hostModuleNormalize resolves a specifier relative to a base name. It returns a
// malloc'd null-terminated string in WASM memory, or 0 (NULL) on error.
func (vm *QuickJS) hostModuleNormalize(baseNamePtr, namePtr uint32) uint32 {
	if vm.moduleNormalizeHandler == nil {
		// No normalize handler; return a copy of the specifier as-is.
		name := vm.readCString(namePtr)
		ptr, _, err := vm.writeString(name)
		if err != nil {
			vm.fail(err)
			return 0
		}
		return ptr
	}
	baseName := vm.readCString(baseNamePtr)
	specifier := vm.readCString(namePtr)
	normalized, err := vm.moduleNormalizeHandler(baseName, specifier)
	if err == nil {
		var ptr uint32
		ptr, _, err = vm.writeString(normalized)
		if err == nil {
			return ptr
		}
	}
	vm.throwIntoContext(err)
	return 0
}

// hostModuleLoad returns the source code of a module as a malloc'd string
// pointer and writes its length to *outLenPtr.
func (vm *QuickJS) hostModuleLoad(namePtr, outLenPtr uint32) uint32 {
	if vm.moduleLoadHandler == nil {
		return 0
	}
	name := vm.readCString(namePtr)
	source, err := vm.moduleLoadHandler(name)
	if err == nil {
		var ptr, length uint32
		ptr, length, err = vm.writeString(source)
		if err == nil {
			vm.writeU32(outLenPtr, length)
			return ptr
		}
	}
	vm.throwIntoContext(err)
	return 0
}

// hostGetTimezoneOffset receives the time as split i32 (hi, lo) and returns the
// UTC offset in seconds.
func (vm *QuickJS) hostGetTimezoneOffset(hi, lo int32) int32 {
	timeSecs := float64(int64(hi)<<32 | int64(uint32(lo)))
	if vm.timezoneOffsetHandler == nil {
		return 0
	}
	return toInt32(vm.timezoneOffsetHandler(timeSecs))
}

// toInt32 is the ECMAScript ToInt32 conversion that a JS number undergoes when
// returned to a wasm i32.
func toInt32(x float64) int32 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(x), 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return int32(uint32(m))
}

// ---- String helpers ----

// writeString writes a string into WASM memory as a NUL-terminated buffer and
// returns its pointer and byte length. The caller must free it.
//
// The Go string's bytes are copied as-is: well-formed UTF-8 is UTF-8, and WTF-8
// surrogate sequences (from guest strings with lone surrogates) reach the guest
// intact because quickjs's decoder accepts them, so guest strings round-trip.
func (vm *QuickJS) writeString(str string) (ptr, length uint32, err error) {
	if uint64(len(str))+1 > math.MaxUint32 {
		return 0, 0, errMallocFailed
	}
	ptr = uint32(vm.call(exWasmMalloc, uint64(len(str)+1)))
	if vm.failure != nil {
		return 0, 0, vm.failure
	}
	if ptr == 0 {
		return 0, 0, errMallocFailed
	}
	buf, ok := vm.memory.Read(ptr, uint32(len(str))+1)
	if !ok {
		return 0, 0, rangeErrorOutOfBounds()
	}
	copy(buf, str)
	buf[len(str)] = 0
	return ptr, uint32(len(str)), nil
}

// readCString reads a NUL-terminated C string from WASM memory (TS: TextDecoder).
func (vm *QuickJS) readCString(ptr uint32) string {
	if vm.failure != nil {
		return ""
	}
	vm.mustNotBeDisposed()
	size := vm.memory.Size()
	end := ptr
	for end < size {
		b, _ := vm.memory.ReadByte(end)
		if b == 0 {
			break
		}
		end++
	}
	return textDecode(vm.readBytes(ptr, end-ptr))
}

var errMallocFailed = errors.New("wasm_malloc failed")

// ---- Public API ----

// throwIfException returns a *JSException if a result handle is an exception.
// Used internally by EvalCode and CallFunction.
func (vm *QuickJS) throwIfException(result *JSValueHandle) (*JSValueHandle, error) {
	if vm.failure != nil {
		return nil, vm.failure
	}
	if vm.call(exQjsIsException, uint64(result.ptr)) != 0 {
		exc := vm.GetException()
		result.Dispose()
		// Track the handle so it gets cleaned up if the VM is disposed
		// before the caller disposes the exception.
		vm.ownedHandles[exc] = struct{}{}
		jsErr := newJSException(exc)
		if vm.failure != nil {
			return nil, vm.failure
		}
		return nil, jsErr
	}
	return result, nil
}

// EvalCode evaluates JavaScript code and returns the result as a handle. If the
// code throws, the error is a *JSException.
//
// filename is used in error stack traces (TS default "<eval>"). flags is a
// bitwise OR of EvalFlags* constants. For example, pass EvalFlagsAsync to allow
// top-level await; the returned handle will be a Promise that resolves to the
// completion value. With EvalFlagsTypeModule the returned handle is a Promise
// that resolves to the module's namespace object (its exports).
func (vm *QuickJS) EvalCode(code, filename string, flags int) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	codePtr, codeLen, err := vm.writeString(code)
	if err != nil {
		return nil, err
	}
	fnPtr, _, err := vm.writeString(filename)
	if err != nil {
		return nil, err
	}
	resultPtr := uint32(vm.call(exQjsEval, uint64(codePtr), uint64(codeLen), uint64(fnPtr), uint64(uint32(flags))))
	vm.call(exWasmFree, uint64(codePtr))
	vm.call(exWasmFree, uint64(fnPtr))
	return vm.throwIfException(newHandle(vm, resultPtr, false, false))
}

// Compile compiles JavaScript source code to bytecode without executing it.
// The returned bytes can be stored, transferred, or later executed with
// EvalBytecode().
//
// filename is used in error stack traces (TS default "<compile>"). evalFlags is a
// bitwise OR of EvalFlags* constants; use EvalFlagsTypeModule to compile as a
// module. compileFlags is a bitwise OR of CompileFlags* constants.
func (vm *QuickJS) Compile(code, filename string, evalFlags, compileFlags int) ([]byte, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	codePtr, codeLen, err := vm.writeString(code)
	if err != nil {
		return nil, err
	}
	fnPtr, _, err := vm.writeString(filename)
	if err != nil {
		return nil, err
	}
	// Allocate space for the output length (size_t = 4 bytes in wasm32).
	outLenPtr := uint32(vm.call(exWasmMalloc, 4))
	bufPtr := uint32(vm.call(exQjsCompile, uint64(codePtr), uint64(codeLen), uint64(fnPtr), uint64(uint32(evalFlags)), uint64(uint32(compileFlags)), uint64(outLenPtr)))
	vm.call(exWasmFree, uint64(codePtr))
	vm.call(exWasmFree, uint64(fnPtr))
	if vm.failure != nil {
		return nil, vm.failure
	}
	if bufPtr == 0 {
		vm.call(exWasmFree, uint64(outLenPtr))
		// Compilation failed; report the QuickJS exception.
		// TS parity: the exception handle is never disposed.
		exc := vm.GetException()
		msg := exc.ToString()
		if vm.failure != nil {
			return nil, vm.failure
		}
		return nil, errors.New("Compilation error: " + msg)
	}
	outLen := vm.readU32(outLenPtr)
	vm.call(exWasmFree, uint64(outLenPtr))
	// Copy the bytecode out of WASM memory before freeing.
	bytecode := vm.readBytes(bufPtr, outLen)
	vm.call(exWasmFree, uint64(bufPtr))
	if vm.failure != nil {
		return nil, vm.failure
	}
	return bytecode, nil
}

// EvalBytecode executes previously compiled bytecode (from Compile()) and
// returns the evaluation result as a handle.
//
// For module bytecode (compiled with EvalFlagsTypeModule), the returned handle
// is a Promise that resolves to the module's namespace object (its exports).
func (vm *QuickJS) EvalBytecode(bytecode []byte) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	bufPtr := uint32(vm.call(exWasmMalloc, uint64(len(bytecode))))
	vm.writeBytes(bufPtr, bytecode)
	resultPtr := uint32(vm.call(exQjsEvalBytecode, uint64(bufPtr), uint64(len(bytecode))))
	vm.call(exWasmFree, uint64(bufPtr))
	return vm.throwIfException(newHandle(vm, resultPtr, false, false))
}

// ExecutePendingJobs executes all pending microtask jobs (promise reactions,
// etc.) and returns the number of jobs executed.
func (vm *QuickJS) ExecutePendingJobs() (int, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return 0, err
	}
	count := 0
	for vm.call(exQjsIsJobPending) != 0 {
		result := int32(vm.call(exQjsExecutePendingJob))
		if vm.failure != nil {
			return count, vm.failure
		}
		if result < 0 {
			exc := vm.GetException()
			msg := exc.ToString()
			if vm.failure != nil {
				return count, vm.failure
			}
			return count, errors.New("Job execution error: " + msg)
		}
		count++
	}
	if vm.failure != nil {
		return count, vm.failure
	}
	return count, nil
}

// RunGC explicitly triggers garbage collection. QuickJS runs GC automatically,
// but this can be useful to reclaim memory at a known point or before
// taking a snapshot.
func (vm *QuickJS) RunGC() {
	vm.mustNotBeDisposed()
	vm.call(exQjsRunGc)
}

// GcThreshold is the GC threshold in bytes. When allocated memory exceeds this
// value, garbage collection is triggered automatically.
func (vm *QuickJS) GcThreshold() int {
	vm.mustNotBeDisposed()
	return int(int32(vm.call(exQjsGetGcThreshold)))
}

// SetGcThreshold sets the GC threshold in bytes. Set to 0 to disable automatic GC.
func (vm *QuickJS) SetGcThreshold(threshold int) {
	vm.mustNotBeDisposed()
	vm.call(exQjsSetGcThreshold, uint64(uint32(threshold)))
}

// GetMemoryUsage returns detailed memory usage statistics from the QuickJS
// runtime: counts and sizes for atoms, strings, objects, functions, etc.
func (vm *QuickJS) GetMemoryUsage() MemoryUsage {
	vm.mustNotBeDisposed()
	// Allocate a buffer for 26 int64 fields (26 * 8 = 208 bytes).
	bufPtr := uint32(vm.call(exWasmMalloc, 26*8))
	vm.call(exQjsComputeMemoryUsage, uint64(bufPtr))
	var view [26]int
	raw := vm.readBytes(bufPtr, 26*8)
	if len(raw) == 26*8 {
		for i := range view {
			var v uint64
			for b := 7; b >= 0; b-- {
				v = v<<8 | uint64(raw[i*8+b])
			}
			view[i] = int(int64(v))
		}
	}
	result := MemoryUsage{
		MallocSize:         view[0],
		MallocLimit:        view[1],
		MemoryUsedSize:     view[2],
		MallocCount:        view[3],
		MemoryUsedCount:    view[4],
		AtomCount:          view[5],
		AtomSize:           view[6],
		StrCount:           view[7],
		StrSize:            view[8],
		ObjCount:           view[9],
		ObjSize:            view[10],
		PropCount:          view[11],
		PropSize:           view[12],
		ShapeCount:         view[13],
		ShapeSize:          view[14],
		JsFuncCount:        view[15],
		JsFuncSize:         view[16],
		JsFuncCodeSize:     view[17],
		JsFuncPc2lineCount: view[18],
		JsFuncPc2lineSize:  view[19],
		CFuncCount:         view[20],
		ArrayCount:         view[21],
		FastArrayCount:     view[22],
		FastArrayElements:  view[23],
		BinaryObjectCount:  view[24],
		BinaryObjectSize:   view[25],
	}
	vm.call(exWasmFree, uint64(bufPtr))
	return result
}

// GetGlobal returns a new handle to the global object. Prefer the cached Global().
func (vm *QuickJS) GetGlobal() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetGlobal)), false, false)
}

// NewString creates a new QuickJS string value.
func (vm *QuickJS) NewString(str string) *JSValueHandle {
	vm.mustNotBeDisposed()
	ptr, length, err := vm.writeString(str)
	if err != nil {
		vm.fail(err)
		return newHandle(vm, 0, false, false)
	}
	resultPtr := uint32(vm.call(exQjsNewString, uint64(ptr), uint64(length)))
	vm.call(exWasmFree, uint64(ptr))
	return newHandle(vm, resultPtr, false, false)
}

// NewNumber creates a new QuickJS number value.
func (vm *QuickJS) NewNumber(num float64) *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsNewNumber, api.EncodeF64(num))), false, false)
}

// NewBigInt creates a new QuickJS BigInt value from the low 64 bits (two's
// complement) of val, like the TS glue.
func (vm *QuickJS) NewBigInt(val *big.Int) *JSValueHandle {
	vm.mustNotBeDisposed()
	// Split the bigint into lo/hi 32-bit halves.
	mask := big.NewInt(0xffffffff)
	lo := new(big.Int).And(val, mask).Uint64()
	hi := new(big.Int).And(new(big.Int).Rsh(val, 32), mask).Uint64()
	return newHandle(vm, uint32(vm.call(exQjsNewBigInt64, lo, hi)), false, false)
}

// NewObject creates a new QuickJS object value.
func (vm *QuickJS) NewObject() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsNewObject)), false, false)
}

// NewArray creates a new QuickJS array value.
func (vm *QuickJS) NewArray() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsNewArray)), false, false)
}

// NewSymbolFor creates a global symbol (Symbol.for(description)).
// Global symbols with the same description are always the same symbol,
// even across snapshot/restore.
func (vm *QuickJS) NewSymbolFor(description string) *JSValueHandle {
	vm.mustNotBeDisposed()
	ptr, length, err := vm.writeString(description)
	if err != nil {
		vm.fail(err)
		return newHandle(vm, 0, false, false)
	}
	result := newHandle(vm, uint32(vm.call(exQjsNewSymbol, uint64(ptr), uint64(length), 1)), false, false)
	vm.call(exWasmFree, uint64(ptr))
	return result
}

// NewArrayBuffer creates a new QuickJS ArrayBuffer by copying data from a host buffer.
func (vm *QuickJS) NewArrayBuffer(data []byte) *JSValueHandle {
	vm.mustNotBeDisposed()
	ptr := uint32(vm.call(exWasmMalloc, uint64(len(data))))
	if ptr == 0 {
		vm.fail(errMallocFailed)
		return newHandle(vm, 0, false, false)
	}
	vm.writeBytes(ptr, data)
	result := newHandle(vm, uint32(vm.call(exQjsNewArrayBuffer, uint64(ptr), uint64(len(data)))), false, false)
	vm.call(exWasmFree, uint64(ptr))
	return result
}

// NewUint8Array creates a new QuickJS Uint8Array by copying data from a host buffer.
func (vm *QuickJS) NewUint8Array(data []byte) *JSValueHandle {
	vm.mustNotBeDisposed()
	ptr := uint32(vm.call(exWasmMalloc, uint64(len(data))))
	if ptr == 0 {
		vm.fail(errMallocFailed)
		return newHandle(vm, 0, false, false)
	}
	vm.writeBytes(ptr, data)
	result := newHandle(vm, uint32(vm.call(exQjsNewUint8Array, uint64(ptr), uint64(len(data)))), false, false)
	vm.call(exWasmFree, uint64(ptr))
	return result
}

// GetUndefined returns a new undefined handle. Prefer the cached Undefined().
func (vm *QuickJS) GetUndefined() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetUndefined)), false, false)
}

// GetNull returns a new null handle. Prefer the cached Null().
func (vm *QuickJS) GetNull() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetNull)), false, false)
}

// GetTrue returns a new true handle. Prefer the cached True().
func (vm *QuickJS) GetTrue() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetTrue)), false, false)
}

// GetFalse returns a new false handle. Prefer the cached False().
func (vm *QuickJS) GetFalse() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetFalse)), false, false)
}

// NewFunction creates a new QuickJS function backed by a host callback.
//
// When the function is called inside QuickJS, the host callback is invoked
// with the this value and arguments as JSValueHandles. The callback stays
// registered under name for the lifetime of the VM, so that it can be
// re-registered after a snapshot is restored.
func (vm *QuickJS) NewFunction(name string, fn HostFunction) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	if _, exists := vm.hostCallbacks[name]; exists {
		return nil, errors.New(`Host callback with name "` + name + `" is already registered`)
	}
	vm.hostCallbacks[name] = fn
	return vm.newHostFunction(name)
}

// newHostFunction creates the guest function for a registered callback name.
func (vm *QuickJS) newHostFunction(name string) (*JSValueHandle, error) {
	namePtr, nameLen, err := vm.writeString(name)
	if err != nil {
		return nil, err
	}
	resultPtr := uint32(vm.call(exQjsNewHostFunction, uint64(namePtr), uint64(nameLen), 0))
	vm.call(exWasmFree, uint64(namePtr))
	if vm.failure != nil {
		return nil, vm.failure
	}
	return newHandle(vm, resultPtr, false, false), nil
}

// WithScope runs fn with a handle scope: every handle created during the call
// is disposed when it returns, except those passed to scope.Escape(). It
// returns fn's error.
//
// Scopes nest: Escape() transfers the handle to the enclosing scope when there
// is one, so it is still cleaned up at the outer boundary.
//
// Host callbacks are safe to trigger inside a scope: the this/argument
// handles the trampoline passes to a callback wrap C-owned pointers and
// are exempt from scope tracking, so the scope frees only handles the host
// actually owns. Handles a callback CREATES (including Dup()s of its
// arguments) are tracked normally.
func (vm *QuickJS) WithScope(fn func(scope *HandleScope) error) error {
	if err := vm.assertNotDisposed(); err != nil {
		return err
	}
	enclosing := vm.activeScope
	tracked := newHandleSet()
	vm.activeScope = tracked
	scope := &HandleScope{tracked: tracked, enclosing: enclosing}
	defer func() {
		vm.activeScope = enclosing
		tracked.each(func(handle *JSValueHandle) { handle.Dispose() })
	}()
	return fn(scope)
}

// ExportHandle exports a handle as a snapshot-portable token.
//
// A handle's heap box lives in the VM's linear memory, so a Snapshot() taken
// while the handle is alive carries it, and a VM restored from that snapshot
// has the identical box at the identical offset. ImportHandle(token) on the
// restored VM (or on this VM) re-materializes an owned handle for the same
// guest value without evaluating any guest code.
//
// Contract: the handle must stay undisposed until after Snapshot(); the token
// is only meaningful to THIS VM and VMs restored from a snapshot of it taken
// while the handle was alive; ImportHandle duplicates the underlying value, so
// it can be called any number of times.
func (vm *QuickJS) ExportHandle(handle *JSValueHandle) (int, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return 0, err
	}
	if handle.vm != vm {
		return 0, errors.New("exportHandle: handle belongs to a different VM")
	}
	if handle.Disposed() {
		return 0, errors.New("exportHandle: handle is disposed")
	}
	if handle.borrowed {
		// Host-callback this/argument handles wrap boxes OWNED BY THE C
		// TRAMPOLINE, freed when the callback returns; a token minted
		// from one would point at freed memory in every restored VM.
		return 0, errors.New("exportHandle: cannot export a borrowed handle (host-callback " +
			"this/argument); its box is freed when the callback returns. " +
			"dup() it and export the duplicate.")
	}
	return int(int32(handle.ptr)), nil
}

// ImportHandle re-materializes a handle from a token produced by ExportHandle,
// on this VM, or on a VM restored from a snapshot taken while the exported
// handle was alive. It returns a NEW owned handle (the underlying value's
// refcount is incremented); dispose it like any other handle.
func (vm *QuickJS) ImportHandle(token int) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	// Best-effort validation before handing the value to qjs_dup_value,
	// which dereferences it as a raw JSValue* inside the WASM instance.
	if token <= 0 || token >= int(vm.memory.Size()) {
		return nil, fmt.Errorf("importHandle: invalid token %d", token)
	}
	result := newHandle(vm, uint32(vm.call(exQjsDupValue, uint64(uint32(token)))), false, false)
	if vm.failure != nil {
		return nil, vm.failure
	}
	return result, nil
}

// NewEphemeralFunction creates a QuickJS function backed by a host callback
// whose registration is tied to the returned handle: disposing the handle
// unregisters the callback.
//
// Use this for short-lived callbacks (e.g. a visitor passed to
// Map.prototype.forEach) where the name is an implementation detail. The guest
// must not retain the function past disposal: calling it afterwards throws.
// Ephemeral functions do not survive snapshot/restore.
func (vm *QuickJS) NewEphemeralFunction(fn HostFunction) *JSValueHandle {
	vm.mustNotBeDisposed()
	name := fmt.Sprintf("__ephemeral:%d", vm.nextInternalId)
	vm.nextInternalId++
	vm.hostCallbacks[name] = fn
	handle, err := vm.newHostFunction(name)
	if err != nil {
		vm.fail(err)
		handle = newHandle(vm, 0, false, false)
	}
	handle.onDispose = func() {
		delete(vm.hostCallbacks, name)
	}
	return handle
}

// UnregisterHostCallback removes a host callback registered with NewFunction()
// or RegisterHostCallback(). It reports whether a callback was removed.
//
// Any QuickJS function still referencing the name will throw when called,
// so only unregister once the guest can no longer reach it.
func (vm *QuickJS) UnregisterHostCallback(name string) bool {
	_, ok := vm.hostCallbacks[name]
	delete(vm.hostCallbacks, name)
	return ok
}

// newInternalFunction creates an internal host function that bypasses the
// duplicate-name check. Used for ephemeral callbacks (promise settle handlers,
// ResolvePromise, etc.) that are not intended to survive snapshot/restore.
func (vm *QuickJS) newInternalFunction(name string, fn HostFunction) *JSValueHandle {
	vm.hostCallbacks[name] = fn
	handle, err := vm.newHostFunction(name)
	if err != nil {
		vm.fail(err)
		return newHandle(vm, 0, false, false)
	}
	return handle
}

// Deferred is a guest promise with host-side resolve/reject functions
// (TS: Deferred, returned by newPromise()).
type Deferred struct {
	// Handle is the QuickJS promise object.
	Handle        *JSValueHandle
	vm            *QuickJS
	resolveHandle *JSValueHandle
	rejectHandle  *JSValueHandle
	settled       chan struct{}
}

// Settled returns a channel that is closed when the QuickJS promise settles
// (TS: the settled host Promise). The first call attaches the guest reaction;
// it fires while the VM executes pending jobs.
func (d *Deferred) Settled() <-chan struct{} {
	if d.settled == nil {
		vm := d.vm
		settled := make(chan struct{})
		d.settled = settled
		settleName := fmt.Sprintf("__settle:%d", vm.nextInternalId)
		vm.nextInternalId++
		onSettleFn := vm.newInternalFunction(settleName, func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
			select {
			case <-settled:
			default:
				close(settled)
			}
			delete(vm.hostCallbacks, settleName)
			return vm.Undefined(), nil
		})
		vm.PromiseThenRaw(d.Handle, onSettleFn, onSettleFn).Dispose()
		onSettleFn.Dispose()
	}
	return d.settled
}

// Resolve resolves the promise with a QuickJS value.
func (d *Deferred) Resolve(value *JSValueHandle) {
	vm := d.vm
	vm.callFunctionRaw(d.resolveHandle, vm.Undefined(), value).Dispose()
	delete(vm.ownedHandles, d.resolveHandle)
	d.resolveHandle.Dispose()
}

// Reject rejects the promise with a QuickJS value.
func (d *Deferred) Reject(value *JSValueHandle) {
	vm := d.vm
	vm.callFunctionRaw(d.rejectHandle, vm.Undefined(), value).Dispose()
	delete(vm.ownedHandles, d.rejectHandle)
	d.rejectHandle.Dispose()
}

// NewPromise creates a new promise and returns a Deferred holding its handle,
// a Settled() notification, and Resolve/Reject.
func (vm *QuickJS) NewPromise() *Deferred {
	vm.mustNotBeDisposed()
	resolveOutPtr := uint32(vm.call(exWasmMalloc, 4))
	rejectOutPtr := uint32(vm.call(exWasmMalloc, 4))
	promisePtr := uint32(vm.call(exQjsNewPromise, uint64(resolveOutPtr), uint64(rejectOutPtr)))
	resolvePtr := vm.readU32(resolveOutPtr)
	rejectPtr := vm.readU32(rejectOutPtr)
	vm.call(exWasmFree, uint64(resolveOutPtr))
	vm.call(exWasmFree, uint64(rejectOutPtr))
	promiseHandle := newHandle(vm, promisePtr, false, false)
	resolveHandle := newHandle(vm, resolvePtr, false, false)
	rejectHandle := newHandle(vm, rejectPtr, false, false)
	// Track resolve/reject handles so they can be freed on VM dispose
	// if the promise is never resolved/rejected.
	vm.ownedHandles[resolveHandle] = struct{}{}
	vm.ownedHandles[rejectHandle] = struct{}{}
	return &Deferred{Handle: promiseHandle, vm: vm, resolveHandle: resolveHandle, rejectHandle: rejectHandle}
}

// PromiseResult is the settlement of a guest promise: exactly one of Value
// (fulfilled) and Error (rejected) is set. The handle is owned by the receiver.
type PromiseResult struct {
	Value *JSValueHandle
	Error *JSValueHandle
}

// ResolvePromise returns a channel that receives the settled value/error of a
// QuickJS promise (TS: a host Promise of { value } | { error }). The channel is
// buffered and receives exactly once: immediately when the promise has already
// settled, otherwise while the VM executes pending jobs.
//
// If the handle is not a promise, it is treated as an already-fulfilled value.
func (vm *QuickJS) ResolvePromise(promiseHandle *JSValueHandle) <-chan PromiseResult {
	vm.mustNotBeDisposed()
	out := make(chan PromiseResult, 1)
	// If the handle is not a promise, treat it as a fulfilled value.
	if vm.call(exQjsIsPromise, uint64(promiseHandle.ptr)) == 0 {
		out <- PromiseResult{Value: promiseHandle.Dup()}
		return out
	}
	// Check if already settled.
	switch int32(vm.call(exQjsPromiseState, uint64(promiseHandle.ptr))) {
	case 1:
		// fulfilled
		out <- PromiseResult{Value: newHandle(vm, uint32(vm.call(exQjsPromiseResult, uint64(promiseHandle.ptr))), false, false)}
		return out
	case 2:
		// rejected
		out <- PromiseResult{Error: newHandle(vm, uint32(vm.call(exQjsPromiseResult, uint64(promiseHandle.ptr))), false, false)}
		return out
	}
	// Pending: attach a then/catch to get notified.
	id := vm.nextInternalId
	vm.nextInternalId++
	fulfilledName := fmt.Sprintf("__onFulfilled:%d", id)
	rejectedName := fmt.Sprintf("__onRejected:%d", id)
	settle := func(args []*JSValueHandle) *JSValueHandle {
		if len(args) > 0 {
			return args[0].Dup()
		}
		return vm.Undefined()
	}
	onFulfilled := vm.newInternalFunction(fulfilledName, func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		val := settle(args)
		delete(vm.hostCallbacks, fulfilledName)
		delete(vm.hostCallbacks, rejectedName)
		out <- PromiseResult{Value: val}
		return vm.Undefined(), nil
	})
	onRejected := vm.newInternalFunction(rejectedName, func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		val := settle(args)
		delete(vm.hostCallbacks, fulfilledName)
		delete(vm.hostCallbacks, rejectedName)
		out <- PromiseResult{Error: val}
		return vm.Undefined(), nil
	})
	// Subscribe via the engine-level primitive: JS_PromiseThen does not
	// consult Promise.prototype.then or Symbol.species, so guest code
	// that patches either cannot intercept the subscription (or run at
	// all during it).
	vm.PromiseThenRaw(promiseHandle, onFulfilled, onRejected).Dispose()
	onFulfilled.Dispose()
	onRejected.Dispose()
	return out
}

// PromiseThenRaw subscribes to a promise without executing guest code, via
// quickjs-ng's JS_PromiseThen: no Promise.prototype.then lookup, no
// Symbol.species. It returns the chained promise. Handler handles are borrowed
// (the caller still owns and disposes them).
func (vm *QuickJS) PromiseThenRaw(promise, onFulfilled, onRejected *JSValueHandle) *JSValueHandle {
	return newHandle(vm, uint32(vm.call(exQjsPromiseThen, uint64(promise.ptr), uint64(onFulfilled.ptr), uint64(onRejected.ptr))), false, false)
}

// MarkPromiseHandled marks a promise as handled: an eventual (or
// already-recorded) rejection will not be reported to OnUnhandledRejection.
// No-op if the handle is not a promise.
func (vm *QuickJS) MarkPromiseHandled(promise *JSValueHandle) {
	vm.mustNotBeDisposed()
	vm.call(exQjsPromiseMarkAsHandled, uint64(promise.ptr))
}

// CallFunction calls a QuickJS function. If the function throws, the error is
// a *JSException.
func (vm *QuickJS) CallFunction(fn, thisVal *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	return vm.throwIfException(vm.callFunctionRaw(fn, thisVal, args...))
}

// Construct invokes a QuickJS constructor with new, i.e. new ctor(...args).
// If the constructor throws (including when ctor is not a constructor), the
// error is a *JSException.
func (vm *QuickJS) Construct(ctor *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	argvPtr := vm.writeArgv(args)
	resultPtr := uint32(vm.call(exQjsCallConstructor, uint64(ctor.ptr), uint64(len(args)), uint64(argvPtr)))
	if argvPtr != 0 {
		vm.call(exWasmFree, uint64(argvPtr))
	}
	return vm.throwIfException(newHandle(vm, resultPtr, false, false))
}

// callFunctionRaw calls a QuickJS function without checking for an exception.
// Used by promise plumbing where exceptions are handled differently.
func (vm *QuickJS) callFunctionRaw(fn, thisVal *JSValueHandle, args ...*JSValueHandle) *JSValueHandle {
	vm.mustNotBeDisposed()
	argvPtr := vm.writeArgv(args)
	resultPtr := uint32(vm.call(exQjsCall, uint64(fn.ptr), uint64(thisVal.ptr), uint64(len(args)), uint64(argvPtr)))
	if argvPtr != 0 {
		vm.call(exWasmFree, uint64(argvPtr))
	}
	return newHandle(vm, resultPtr, false, false)
}

// writeArgv allocates a JSValue* array for a call; 0 when there are no args.
func (vm *QuickJS) writeArgv(args []*JSValueHandle) uint32 {
	if len(args) == 0 {
		return 0
	}
	argvPtr := uint32(vm.call(exWasmMalloc, uint64(len(args)*4)))
	for i, arg := range args {
		vm.writeU32(argvPtr+uint32(i)*4, arg.ptr)
	}
	return argvPtr
}

// SetProp sets a property on an object. A *JSValueHandle key supports symbols
// (including Symbol.for()).
func (vm *QuickJS) SetProp(obj *JSValueHandle, key PropertyKey, value *JSValueHandle) {
	vm.mustNotBeDisposed()
	switch k := key.(type) {
	case StringKey:
		namePtr, _, err := vm.writeString(string(k))
		if err != nil {
			vm.fail(err)
			return
		}
		vm.call(exQjsSetPropString, uint64(obj.ptr), uint64(namePtr), uint64(value.ptr))
		vm.call(exWasmFree, uint64(namePtr))
	case *JSValueHandle:
		vm.call(exQjsSetPropValue, uint64(obj.ptr), uint64(k.ptr), uint64(value.ptr))
	}
}

// DefineProp defines a property on an object with explicit property descriptor
// flags. Unlike SetProp, this controls the writable, enumerable, and
// configurable attributes, matching Object.defineProperty() semantics. A nil
// descriptor means all flags false.
func (vm *QuickJS) DefineProp(obj *JSValueHandle, key PropertyKey, value *JSValueHandle, descriptor *JSPropertyDescriptor) {
	vm.mustNotBeDisposed()
	flags := descriptorFlags(descriptor)
	switch k := key.(type) {
	case StringKey:
		namePtr, _, err := vm.writeString(string(k))
		if err != nil {
			vm.fail(err)
			return
		}
		vm.call(exQjsDefinePropString, uint64(obj.ptr), uint64(namePtr), uint64(value.ptr), flags)
		vm.call(exWasmFree, uint64(namePtr))
	case *JSValueHandle:
		vm.call(exQjsDefinePropValue, uint64(obj.ptr), uint64(k.ptr), uint64(value.ptr), flags)
	}
}

func descriptorFlags(descriptor *JSPropertyDescriptor) uint64 {
	var flags uint64
	if descriptor != nil {
		if descriptor.Configurable {
			flags |= 1 // JS_PROP_CONFIGURABLE
		}
		if descriptor.Writable {
			flags |= 2 // JS_PROP_WRITABLE
		}
		if descriptor.Enumerable {
			flags |= 4 // JS_PROP_ENUMERABLE
		}
	}
	return flags
}

// GetProp gets a property from an object using a handle key. Supports symbol
// keys (including Symbol.for()).
func (vm *QuickJS) GetProp(obj, key *JSValueHandle) *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetPropValue, uint64(obj.ptr), uint64(key.ptr))), false, false)
}

// GetException returns the current exception, if any.
func (vm *QuickJS) GetException() *JSValueHandle {
	vm.mustNotBeDisposed()
	return newHandle(vm, uint32(vm.call(exQjsGetException)), false, false)
}

// NewError creates a new QuickJS Error object. messageOrError is a string
// message or a Go error (TS: string | Error). For an error, the message is its
// Message() method when it has one (else err.Error()), the name is its Name()
// method when it has one (else "Error"), and the stack is its Stack() method
// when it has one and it is non-empty. Any other value is formatted with
// fmt.Sprint and used as the message.
func (vm *QuickJS) NewError(messageOrError any) *JSValueHandle {
	vm.mustNotBeDisposed()
	errHandle := newHandle(vm, uint32(vm.call(exQjsNewError)), false, false)
	err, isErr := messageOrError.(error)
	if !isErr {
		message, ok := messageOrError.(string)
		if !ok {
			message = fmt.Sprint(messageOrError)
		}
		msgHandle := vm.NewString(message)
		errHandle.SetProp("message", msgHandle)
		msgHandle.Dispose()
		return errHandle
	}
	message := err.Error()
	if withMessage, ok := err.(interface{ Message() string }); ok {
		message = withMessage.Message()
	}
	msgHandle := vm.NewString(message)
	errHandle.SetProp("message", msgHandle)
	msgHandle.Dispose()
	name := "Error"
	if named, ok := err.(interface{ Name() string }); ok {
		name = named.Name()
	}
	if name != "" {
		nameHandle := vm.NewString(name)
		errHandle.SetProp("name", nameHandle)
		nameHandle.Dispose()
	}
	if stacked, ok := err.(interface{ Stack() *string }); ok {
		if stack := stacked.Stack(); stack != nil && *stack != "" {
			stackHandle := vm.NewString(*stack)
			errHandle.SetProp("stack", stackHandle)
			stackHandle.Dispose()
		}
	}
	return errHandle
}

// Typeof returns the typeof a handle as a string.
func (vm *QuickJS) Typeof(handle *JSValueHandle) string {
	vm.mustNotBeDisposed()
	p := uint64(handle.ptr)
	switch {
	case vm.call(exQjsIsUndefined, p) != 0:
		return "undefined"
	case vm.call(exQjsIsNull, p) != 0:
		return "object" // typeof null === 'object'
	case vm.call(exQjsIsBool, p) != 0:
		return "boolean"
	case vm.call(exQjsIsNumber, p) != 0:
		return "number"
	case vm.call(exQjsIsBigInt, p) != 0:
		return "bigint"
	case vm.call(exQjsIsString, p) != 0:
		return "string"
	case vm.call(exQjsIsSymbol, p) != 0:
		return "symbol"
	case vm.call(exQjsIsFunction, p) != 0:
		return "function"
	case vm.call(exQjsIsObject, p) != 0:
		return "object"
	}
	return "unknown"
}

// RegisterHostCallback re-registers a host callback after restoring from a
// snapshot. The name must match the name passed to NewFunction() before the
// snapshot.
func (vm *QuickJS) RegisterHostCallback(name string, fn HostFunction) {
	vm.hostCallbacks[name] = fn
}

// Dispose disposes the VM and closes its wasm instance, releasing its linear
// memory. Disposing handles afterwards is a no-op.
//
// Go-only: when a host callback disposes the VM while wasm frames are still
// running, the running calls are aborted (they fail with the disposed error)
// and the instance is closed once the outermost call has unwound.
func (vm *QuickJS) Dispose() {
	if !vm.disposed {
		vm.disposed = true
		vm.global = nil
		vm.undefined = nil
		vm.null = nil
		vm.trueValue = nil
		vm.falseValue = nil
		clear(vm.ownedHandles)
		clear(vm.hostCallbacks)
		vm.activeScope = nil
		vm.module = nil
		if vm.callDepth > 0 {
			if vm.failure == nil {
				vm.failure = errDisposed
			}
			return
		}
		vm.closeInstance()
	}
}

// closeInstance closes the wasm instance. Only called with no call running.
func (vm *QuickJS) closeInstance() {
	if vm.mod != nil {
		_ = vm.mod.Close(context.Background())
	}
	vm.mod = nil
	vm.memory = nil
	vm.funcs = [exportCount][]api.Function{}
}

// Err reports the VM's wasm-level failure, if any (Go-only). Methods without an
// error result record failures here: a trap, a call aborted because the VM's
// context ended, or exhausted linear memory. After a failure no wasm code runs
// and every error-returning method returns it.
func (vm *QuickJS) Err() error {
	return vm.failure
}

var errDisposed = errors.New("QuickJS instance has been disposed")

func (vm *QuickJS) assertNotDisposed() error {
	if vm.disposed {
		return errDisposed
	}
	return nil
}

// mustNotBeDisposed panics on use after Dispose, a programmer error (TS throws).
func (vm *QuickJS) mustNotBeDisposed() {
	if vm.disposed {
		panic(errDisposed)
	}
}

// ---- wasm calls and memory access ----

// call invokes a wasm export and returns its first result (0 for none). After a
// failure it returns 0 without running wasm code.
func (vm *QuickJS) call(id exportID, params ...uint64) uint64 {
	if vm.failure != nil {
		return 0
	}
	if vm.disposed {
		panic(errDisposed)
	}
	pool := vm.funcs[id]
	var fn api.Function
	if n := len(pool); n > 0 {
		fn = pool[n-1]
		vm.funcs[id] = pool[:n-1]
	} else {
		fn = vm.mod.ExportedFunction(exportNames[id])
		if fn == nil {
			vm.fail(&HostError{name: "TypeError", message: "vm.exports." + exportNames[id] + " is not a function"})
			return 0
		}
	}
	vm.callDepth++
	results, err := fn.Call(vm.ctx, params...)
	vm.callDepth--
	if vm.disposed {
		// Disposed by a host callback during the call.
		if vm.callDepth == 0 {
			vm.closeInstance()
		}
		return 0
	}
	vm.funcs[id] = append(vm.funcs[id], fn)
	if err != nil {
		vm.fail(err)
		return 0
	}
	if len(results) == 0 {
		return 0
	}
	return results[0]
}

// fail records the first wasm-level failure. Glue errors raised inside host
// imports (they reach here wrapped by wazero's panic recovery) are recorded
// unwrapped, everything else becomes a *WasmError.
func (vm *QuickJS) fail(err error) {
	if vm.failure != nil {
		return
	}
	var hostErr *HostError
	switch {
	case errors.As(err, &hostErr):
		vm.failure = hostErr
	case errors.Is(err, errMallocFailed):
		vm.failure = errMallocFailed
	case errors.Is(err, errDisposed):
		vm.failure = errDisposed
	default:
		var cause error
		if vm.ctx != nil && vm.ctx.Err() != nil {
			cause = context.Cause(vm.ctx)
		}
		vm.failure = newWasmError(err, cause)
	}
}

func rangeErrorOutOfBounds() error {
	return &HostError{name: "RangeError", message: "Offset is outside the bounds of the DataView"}
}

// readBytes copies n bytes from linear memory (TS: new Uint8Array(buffer, ptr, n).slice()).
func (vm *QuickJS) readBytes(ptr, n uint32) []byte {
	if vm.failure != nil {
		return nil
	}
	vm.mustNotBeDisposed()
	view, ok := vm.memory.Read(ptr, n)
	if !ok {
		vm.fail(&HostError{name: "RangeError", message: fmt.Sprintf("Invalid typed array length: %d", n)})
		return nil
	}
	return append([]byte(nil), view...)
}

func (vm *QuickJS) writeBytes(ptr uint32, data []byte) {
	if vm.failure != nil {
		return
	}
	vm.mustNotBeDisposed()
	if !vm.memory.Write(ptr, data) {
		vm.fail(rangeErrorOutOfBounds())
	}
}

func (vm *QuickJS) readU32(ptr uint32) uint32 {
	if vm.failure != nil {
		return 0
	}
	vm.mustNotBeDisposed()
	v, ok := vm.memory.ReadUint32Le(ptr)
	if !ok {
		vm.fail(rangeErrorOutOfBounds())
	}
	return v
}

func (vm *QuickJS) writeU32(ptr, v uint32) {
	if vm.failure != nil {
		return
	}
	vm.mustNotBeDisposed()
	if !vm.memory.WriteUint32Le(ptr, v) {
		vm.fail(rangeErrorOutOfBounds())
	}
}

// ---- Errors ----

// HostError is a host-side JS error value: an error the TS glue raises with a
// specific class (RangeError, TypeError), or the host form of a guest Error
// produced by Dump (name, message, and stack copied).
type HostError struct {
	name    string
	message string
	stack   *string
}

// NewHostError creates a HostError. stack may be nil.
func NewHostError(name, message string, stack *string) *HostError {
	return &HostError{name: name, message: message, stack: stack}
}

// Error returns the message.
func (e *HostError) Error() string { return e.message }

// Name returns the JS error name.
func (e *HostError) Name() string { return e.name }

// Message returns the message.
func (e *HostError) Message() string { return e.message }

// Stack returns the stack, nil when absent.
func (e *HostError) Stack() *string { return e.stack }

// WasmError reports a failure of the wasm instance itself (TS: a
// WebAssembly.RuntimeError or an exception thrown through the wasm frames): a
// trap, a call aborted because the VM's context ended (Cause is the context
// cause, so errors.Is(err, context.Canceled) holds), or an exhausted heap.
type WasmError struct {
	message string
	err     error
	cause   error
}

func newWasmError(err error, cause error) *WasmError {
	var wasmErr *WasmError
	if errors.As(err, &wasmErr) {
		return wasmErr
	}
	message := err.Error()
	if line, _, found := strings.Cut(message, "\n"); found {
		message = line
	}
	message = strings.TrimPrefix(message, "wasm error: ")
	return &WasmError{message: message, err: err, cause: cause}
}

// Error returns the trap message.
func (e *WasmError) Error() string { return e.message }

// Name returns "RuntimeError".
func (e *WasmError) Name() string { return "RuntimeError" }

// Unwrap returns the underlying wazero error and the context cause, if any.
func (e *WasmError) Unwrap() []error {
	if e.cause != nil {
		return []error{e.err, e.cause}
	}
	return []error{e.err}
}
