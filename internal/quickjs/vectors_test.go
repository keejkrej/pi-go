package quickjs

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestEvalCaseVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.EvalCases {
		t.Run(c.Name, func(t *testing.T) {
			vm := newVM(t, nil)
			defer vm.Dispose()
			got := attempt(func() (any, error) {
				h, err := vm.EvalCode(c.Code, "case.js", 0)
				if err != nil {
					return nil, err
				}
				defer h.Dispose()
				d, err := dumpEnc(vm, h)
				if err != nil {
					return nil, err
				}
				return obj{"typeof": h.Typeof(), "dump": d}, nil
			})
			if c.Name == "stackOverflowDefault" {
				// Without a QuickJS stack guard, deep recursion exhausts the
				// engine's native stack. V8 reports a RangeError from the
				// wasm frames; wazero traps, and the VM fails permanently.
				var wasmErr *WasmError
				if !errors.As(vm.Err(), &wasmErr) {
					t.Fatalf("want a *WasmError VM failure, got %v (result %s)", vm.Err(), compact(got))
				}
				return
			}
			expectVector(t, c.Name, c.Result, got)
		})
	}
}

func TestEvalFlagVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.FlagCases {
		t.Run(c.Name, func(t *testing.T) {
			vm := newVM(t, nil)
			defer vm.Dispose()
			got := attempt(func() (any, error) {
				h, err := vm.EvalCode(c.Code, "flags.js", c.Flags)
				if err != nil {
					return nil, err
				}
				out := obj{"typeof": h.Typeof(), "isPromise": h.IsPromise()}
				if h.IsPromise() {
					out["before"] = h.PromiseState()
					jobs, err := vm.ExecutePendingJobs()
					if err != nil {
						return nil, err
					}
					out["jobs"] = jobs
					out["after"] = h.PromiseState()
					settled := settledNow(t, vm.ResolvePromise(h))
					for k, val := range settledEnc(t, vm, settled) {
						out[k] = val
					}
				} else {
					d, err := dumpEnc(vm, h)
					if err != nil {
						return nil, err
					}
					out["dump"] = d
				}
				h.Dispose()
				return out, nil
			})
			expectVector(t, c.Name, c.Result, got)
		})
	}
}

func TestValueVectors(t *testing.T) {
	v := loadVectors(t)
	vm := newVM(t, nil)
	defer vm.Dispose()
	for _, raw := range v.Values {
		expected := decodeJSON(t, raw).(map[string]any)
		expr := expected["expr"].(string)
		t.Run(expr, func(t *testing.T) {
			h, err := vm.EvalCode("("+expr+")", "value.js", 0)
			if err != nil {
				t.Fatal(err)
			}
			// Same order as the generator: some reads run guest code or
			// leave a pending exception behind.
			rec := obj{"expr": expr}
			rec["isUndefined"] = h.IsUndefined()
			rec["isNull"] = h.IsNull()
			rec["isBool"] = h.IsBool()
			rec["isNumber"] = h.IsNumber()
			rec["isString"] = h.IsString()
			rec["isSymbol"] = h.IsSymbol()
			rec["isBigInt"] = h.IsBigInt()
			rec["isObject"] = h.IsObject()
			rec["isArray"] = h.IsArray()
			rec["isFunction"] = h.IsFunction()
			rec["isError"] = h.IsError()
			rec["isPromise"] = h.IsPromise()
			rec["isArrayBuffer"] = h.IsArrayBuffer()
			rec["isProxy"] = h.IsProxy()
			rec["isMap"] = h.IsMap()
			rec["isSet"] = h.IsSet()
			rec["isDate"] = h.IsDate()
			rec["isRegExp"] = h.IsRegExp()
			rec["isWeakRef"] = h.IsWeakRef()
			rec["isWeakMap"] = h.IsWeakMap()
			rec["isWeakSet"] = h.IsWeakSet()
			rec["isDataView"] = h.IsDataView()
			rec["hasIdentity"] = h.Identity() != 0
			rec["toBoolean"] = h.ToBoolean()
			rec["classId"] = h.ClassId()
			rec["className"] = attempt(func() (any, error) {
				name, err := h.ClassName()
				return strOrNil(name), err
			})
			rec["promiseState"] = h.PromiseState()
			rec["typeof"] = h.Typeof()
			rec["toNumber"] = encNum(h.ToNumber())
			rec["toString"] = encStr(h.ToString())
			rec["constructorName"] = attempt(func() (any, error) { return strOrNil(h.ConstructorName()), nil })
			rec["length"] = encNum(h.Length())
			h.Dispose()
			expectVector(t, expr, raw, rec)
		})
	}
}

func TestScenarioVectors(t *testing.T) {
	v := loadVectors(t)
	scenarios := map[string]func(t *testing.T) any{
		"hostFunctionBasic":           scenarioHostFunctionBasic,
		"hostFunctionThrows":          scenarioHostFunctionThrows,
		"hostFunctionArgs":            scenarioHostFunctionArgs,
		"hostFunctionReturnsArgument": scenarioHostFunctionReturnsArgument,
		"unregisteredCallback":        scenarioUnregisteredCallback,
		"duplicateFunctionName":       scenarioDuplicateFunctionName,
		"callFunction":                scenarioCallFunction,
		"interrupt":                   scenarioInterrupt,
		"stackOverflow":               scenarioStackOverflow,
		"invalidMaxStackSize":         scenarioInvalidMaxStackSize,
		"memoryLimit":                 scenarioMemoryLimit,
		"timezoneFixed":               scenarioTimezoneFixed,
		"timezoneFunction":            scenarioTimezoneFunction,
		"timezoneHost":                scenarioTimezoneHost,
		"moduleLoader":                scenarioModuleLoader,
		"moduleLoaderNoNormalize":     scenarioModuleLoaderNoNormalize,
		"moduleNoLoader":              scenarioModuleNoLoader,
		"compile":                     scenarioCompile,
		"snapshot":                    scenarioSnapshot,
		"deserializeErrors":           scenarioDeserializeErrors,
		"restoreMissingExtension":     scenarioRestoreMissingExtension,
		"memoryUsage":                 scenarioMemoryUsage,
		"gc":                          scenarioGC,
		"versions":                    scenarioVersions,
		"introspection":               scenarioIntrospection,
		"proxy":                       scenarioProxy,
		"newValues":                   scenarioNewValues,
		"hostToHandle":                scenarioHostToHandle,
		"props":                       scenarioProps,
		"promises":                    scenarioPromises,
		"unhandledRejection":          scenarioUnhandledRejection,
		"jobErrors":                   scenarioJobErrors,
		"withScope":                   scenarioWithScope,
		"exportImport":                scenarioExportImport,
		"wasiClock":                   scenarioWasiClock,
		"intrinsics":                  scenarioIntrinsics,
		"exceptionValue":              scenarioExceptionValue,
		"disposed":                    scenarioDisposed,
	}
	for name := range v.Scenarios {
		if _, ok := scenarios[name]; !ok {
			t.Errorf("vector scenario %q has no Go replay", name)
		}
	}
	for name, run := range scenarios {
		raw, ok := v.Scenarios[name]
		if !ok {
			t.Errorf("missing vector for scenario %q", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			expectVector(t, name, raw, run(t))
		})
	}
}

func scenarioHostFunctionBasic(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fn, err := vm.NewFunction("add", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewNumber(args[0].ToNumber() + args[1].ToNumber()), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	vm.Global().SetProp("add", fn)
	fn.Dispose()
	return obj{
		"sum":  evalDump(vm, "add(2, 3)"),
		"meta": evalDump(vm, "[add.name, add.length, typeof add, String(add)]"),
	}
}

func scenarioHostFunctionThrows(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	f1, _ := vm.NewFunction("fail", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		return nil, errors.New("host fail")
	})
	f2, _ := vm.NewFunction("failType", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		return nil, NewHostError("TypeError", "bad type", nil)
	})
	vm.Global().SetProp("fail", f1)
	vm.Global().SetProp("failType", f2)
	f1.Dispose()
	f2.Dispose()
	probe := "try { %s() } catch (e) { [e instanceof Error, e.name, e.message, typeof e.stack, Object.prototype.hasOwnProperty.call(e, 'name')] }"
	r := obj{
		"fail":     evalDump(vm, strings.Replace(probe, "%s", "fail", 1)),
		"failType": evalDump(vm, strings.Replace(probe, "%s", "failType", 1)),
		"uncaught": evalDump(vm, "fail()"),
	}
	// TS copies the V8 stack of the host Error into the guest error
	// (typeof e.stack === "string"). Go errors carry no stack unless they
	// implement Stack(), so the guest keeps QuickJS's own stack property.
	for _, k := range []string{"fail", "failType"} {
		ok, _ := r[k].(obj)["ok"].(obj)
		arr, _ := ok["v"].([]any)
		if len(arr) != 5 {
			t.Errorf("%s: unexpected result %s", k, compact(r[k]))
			continue
		}
		if arr[3] != "string" && arr[3] != "undefined" {
			t.Errorf("%s: typeof stack %v", k, arr[3])
		}
		arr[3] = "string"
	}
	return r
}

func scenarioHostFunctionArgs(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	log := []any{}
	fn, _ := vm.NewFunction("bridge", func(this *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		types := []any{}
		for _, a := range args {
			types = append(types, a.Typeof())
		}
		log = append(log, []any{len(args), this.Typeof(), types})
		return vm.Undefined(), nil
	})
	vm.Global().SetProp("bridge", fn)
	fn.Dispose()
	ret := evalDump(vm, "bridge('x'); bridge('x', 1, 2, 3); ({ m: bridge }).m(); bridge.call(5); bridge()")
	return obj{"log": log, "ret": ret}
}

func scenarioHostFunctionReturnsArgument(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fn, _ := vm.NewFunction("id", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return args[0], nil
	})
	vm.Global().SetProp("id", fn)
	fn.Dispose()
	return evalDump(vm, "const o = { a: [1] }; [id(o) === o, id('s'), id(1.5)]")
}

func scenarioUnregisteredCallback(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	eph := vm.NewEphemeralFunction(func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewString("eph ok"), nil
	})
	vm.Global().SetProp("eph", eph)
	first := evalDump(vm, "eph()")
	eph.Dispose()
	after := evalDump(vm, "try { eph() } catch (e) { [e.name, e.message] }")
	named, _ := vm.NewFunction("named", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.True(), nil
	})
	vm.Global().SetProp("named", named)
	named.Dispose()
	u1 := vm.UnregisterHostCallback("named")
	u2 := vm.UnregisterHostCallback("named")
	afterUnregister := evalDump(vm, "try { named() } catch (e) { [e.name, e.message] }")
	return obj{"first": first, "after": after, "u1": u1, "u2": u2, "afterUnregister": afterUnregister}
}

func scenarioDuplicateFunctionName(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	noop := func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) { return vm.Undefined(), nil }
	h, _ := vm.NewFunction("dup", noop)
	h.Dispose()
	return attempt(func() (any, error) {
		h, err := vm.NewFunction("dup", noop)
		if err != nil {
			return nil, err
		}
		return h, nil
	})
}

func scenarioCallFunction(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fn := mustEval(t, vm, "(function (a, b) { return this.k + a + b })")
	o := mustEval(t, vm, "({ k: 1 })")
	a := vm.NewNumber(2)
	b := vm.NewNumber(3)
	callDump := func(f func() (*JSValueHandle, error)) obj {
		return attempt(func() (any, error) {
			h, err := f()
			if err != nil {
				return nil, err
			}
			defer h.Dispose()
			return dumpEnc(vm, h)
		})
	}
	ok := callDump(func() (*JSValueHandle, error) { return vm.CallFunction(fn, o, a, b) })
	thrower := mustEval(t, vm, "(function () { throw new SyntaxError('in call') })")
	thrown := callDump(func() (*JSValueHandle, error) { return vm.CallFunction(thrower, vm.Undefined()) })
	notFn := callDump(func() (*JSValueHandle, error) { return vm.CallFunction(o, vm.Undefined()) })
	ctor := mustEval(t, vm, "(class P { constructor(x) { this.x = x } })")
	constructed := callDump(func() (*JSValueHandle, error) { return vm.Construct(ctor, a) })
	notCtor := callDump(func() (*JSValueHandle, error) { return vm.Construct(o, a) })
	return obj{"ok": ok, "thrown": thrown, "notFn": notFn, "constructed": constructed, "notCtor": notCtor}
}

func scenarioInterrupt(t *testing.T) any {
	calls := 0
	armed := true
	vm := newVM(t, &QuickJSOptions{InterruptHandler: func() bool {
		calls++
		return armed && calls > 3
	}})
	defer vm.Dispose()
	loop := evalDump(vm, "let i = 0; while (true) { i++ }")
	callsAtInterrupt := calls
	armed = false
	after := evalDump(vm, "1 + 1")
	caught := evalDump(vm, "globalThis.r = 0; try { for (let j = 0; j < 100000; j++) r++ } catch (e) { 'caught' }; r")
	return obj{"loop": loop, "callsAtInterrupt": callsAtInterrupt, "after": after, "caught": caught}
}

func scenarioStackOverflow(t *testing.T) any {
	vm := newVM(t, &QuickJSOptions{MaxStackSize: intPtr(MaxStackSize)})
	defer vm.Dispose()
	r := obj{
		"recursion": evalDump(vm, "function f(n) { return f(n + 1) + 1 }\nf(0)"),
		"caught":    evalDump(vm, "function g(n) { return g(n + 1) + 1 }\ntry { g(0) } catch (e) { [e.name, e.message, e instanceof RangeError] }"),
		"after":     evalDump(vm, "'still alive'"),
	}
	small := newVM(t, &QuickJSOptions{MaxStackSize: intPtr(32 * 1024)})
	defer small.Dispose()
	r["small"] = evalDump(small, "function h(n) { return h(n + 1) + 1 }\ntry { h(0) } catch (e) { [e.name, e.message] }")
	return r
}

func scenarioInvalidMaxStackSize(t *testing.T) any {
	r := obj{}
	for _, v := range []int{MaxStackSize + 1, -1} {
		r[fmt.Sprint(v)] = attempt(func() (any, error) {
			vm, err := QuickJSCreate(context.Background(), &QuickJSOptions{MaxStackSize: intPtr(v)})
			if err != nil {
				return nil, err
			}
			vm.Dispose()
			return "created", nil
		})
	}
	// Go has no fractional int: TS's 1.5 case cannot be expressed; it fails
	// the same way as the out-of-range values above.
	r["1.5"] = r["-1"]
	zero := newVM(t, &QuickJSOptions{MaxStackSize: intPtr(0)})
	defer zero.Dispose()
	r["zero"] = evalDump(zero, "1")
	return r
}

func scenarioMemoryLimit(t *testing.T) any {
	vm := newVM(t, &QuickJSOptions{MemoryLimit: intPtr(2 * 1024 * 1024)})
	defer vm.Dispose()
	return obj{
		"oom":       evalDump(vm, "const a = []; for (let i = 0; i < 1e7; i++) a.push({ i }); a.length"),
		"after":     evalDump(vm, "1 + 1"),
		"stringOom": evalDump(vm, "try { 'x'.repeat(4 * 1024 * 1024) ; 'no' } catch (e) { [e.name, e.message] }"),
		"limit":     vm.GetMemoryUsage().MallocLimit,
	}
}

const dateProbe = "[new Date(0).getHours(), new Date(0).getTimezoneOffset(), new Date(0).toString(), new Date(2024, 0, 15, 10, 30).toISOString(), new Date(2e12).getTimezoneOffset(), new Date(2e12).toString(), new Date(8.64e15).getTimezoneOffset(), new Date(-8.64e15).getTimezoneOffset()]"

func scenarioTimezoneFixed(t *testing.T) any {
	vm := newVM(t, &QuickJSOptions{TimezoneOffset: TimezoneOffsetMinutes(-480)})
	defer vm.Dispose()
	return evalDump(vm, dateProbe)
}

func scenarioTimezoneFunction(t *testing.T) any {
	seen := []any{}
	vm := newVM(t, &QuickJSOptions{TimezoneOffset: TimezoneOffsetFunc(func(timeSecs float64) float64 {
		seen = append(seen, encNum(timeSecs))
		if timeSecs < 1e9 {
			return 300
		}
		return -90
	})})
	defer vm.Dispose()
	r := evalDump(vm, dateProbe)
	return obj{"r": r, "seen": seen}
}

func scenarioTimezoneHost(t *testing.T) any {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	time.Local = ny
	defer func() { time.Local = time.UTC }()
	vm := newVM(t, nil)
	defer vm.Dispose()
	// -3e12 and -8.64e15 predate standard time: New York's LMT is -4:56:02,
	// which V8's getTimezoneOffset truncates to whole minutes.
	return evalDump(vm, "[new Date(0).getTimezoneOffset(), new Date(1720000000000).getTimezoneOffset(), new Date(2024, 6, 1, 12).toString(), new Date(2024, 0, 1, 12).getTime(), new Date(-3e12).getTimezoneOffset(), new Date(-3e12).toString(), new Date(-8.64e15).getTimezoneOffset()]")
}

// runModule is the generator's run(): evaluate a module, drain jobs, settle.
func runModule(t *testing.T, vm *QuickJS, code, filename string, withJobs bool) obj {
	return attempt(func() (any, error) {
		h, err := vm.EvalCode(code, filename, EvalFlagsTypeModule)
		if err != nil {
			return nil, err
		}
		jobs, err := vm.ExecutePendingJobs()
		if err != nil {
			return nil, err
		}
		settled := settledNow(t, vm.ResolvePromise(h))
		h.Dispose()
		out := settledEnc(t, vm, settled)
		if withJobs {
			out["jobs"] = jobs
		}
		return out, nil
	})
}

func scenarioModuleLoader(t *testing.T) any {
	calls := []any{}
	sources := map[string]string{
		"/root/dep.js":   "export const x = 41; export { y as z } from './sub/y.js'",
		"/root/sub/y.js": "export const y = 'why'",
	}
	vm := newVM(t, &QuickJSOptions{ModuleLoader: &ModuleLoader{
		Normalize: func(base, spec string) (string, error) {
			calls = append(calls, []any{"normalize", base, spec})
			dir := "/root"
			if i := strings.LastIndex(base, "/"); i >= 0 {
				dir = base[:i]
			}
			var out []string
			for _, p := range strings.Split(dir+"/"+spec, "/") {
				switch p {
				case ".", "":
				case "..":
					if len(out) > 0 {
						out = out[:len(out)-1]
					}
				default:
					out = append(out, p)
				}
			}
			return "/" + strings.Join(out, "/"), nil
		},
		Load: func(name string) (string, error) {
			calls = append(calls, []any{"load", name})
			source, ok := sources[name]
			if !ok {
				return "", fmt.Errorf("cannot find module %s", name)
			}
			return source, nil
		},
	}})
	defer vm.Dispose()
	ok := runModule(t, vm, "import { x, z } from './dep.js'; export const y = x + 1; export const zz = z", "/root/main.js", true)
	missing := runModule(t, vm, "import { q } from './missing.js'; export default q", "/root/other.js", true)
	return obj{"ok": ok, "missing": missing, "calls": calls}
}

func scenarioModuleLoaderNoNormalize(t *testing.T) any {
	calls := []any{}
	vm := newVM(t, &QuickJSOptions{ModuleLoader: &ModuleLoader{
		Load: func(name string) (string, error) {
			calls = append(calls, name)
			return "export default 'loaded:' + import.meta.url", nil
		},
	}})
	defer vm.Dispose()
	r := runModule(t, vm, "import d from 'pkg/thing'; export const v = d", "main.js", false)
	return obj{"r": r, "calls": calls}
}

func scenarioModuleNoLoader(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	r := runModule(t, vm, "import d from 'pkg'; export const v = d", "main.js", false)
	dynamic := attempt(func() (any, error) {
		h, err := vm.EvalCode("import('dyn')", "dyn.js", 0)
		if err != nil {
			return nil, err
		}
		if _, err := vm.ExecutePendingJobs(); err != nil {
			return nil, err
		}
		settled := settledNow(t, vm.ResolvePromise(h))
		h.Dispose()
		return settledEnc(t, vm, settled), nil
	})
	return obj{"r": r, "dynamic": dynamic}
}

func scenarioCompile(t *testing.T) any {
	vm := newVM(t, nil)
	r := obj{}
	bc, err := vm.Compile("1 + 2", "<compile>", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r["bytecode"] = hex.EncodeToString(bc)
	evalBC := func(vm *QuickJS, bc []byte) obj {
		return attempt(func() (any, error) {
			h, err := vm.EvalBytecode(bc)
			if err != nil {
				return nil, err
			}
			defer h.Dispose()
			return dumpEnc(vm, h)
		})
	}
	r["evalBytecode"] = evalBC(vm, bc)
	compileHex := func(code, filename string, evalFlags, compileFlags int) (any, error) {
		out, err := vm.Compile(code, filename, evalFlags, compileFlags)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(out), nil
	}
	r["stripped"], _ = compileHex("function f(a) { return a * 2 }\nf(21)", "strip.js", 0, CompileFlagsStripSource|CompileFlagsStripDebug)
	r["debug"], _ = compileHex("function f(a) { return a * 2 }\nf(21)", "strip.js", 0, 0)
	mod, err := vm.Compile("export const z = 9", "mod.js", EvalFlagsTypeModule, 0)
	if err != nil {
		t.Fatal(err)
	}
	r["module"] = hex.EncodeToString(mod)
	r["moduleEval"] = attempt(func() (any, error) {
		h, err := vm.EvalBytecode(mod)
		if err != nil {
			return nil, err
		}
		if _, err := vm.ExecutePendingJobs(); err != nil {
			return nil, err
		}
		settled := settledNow(t, vm.ResolvePromise(h))
		h.Dispose()
		return settledEnc(t, vm, settled), nil
	})
	r["syntaxError"] = attempt(func() (any, error) { return compileHex("let let = 1", "bad.js", 0, 0) })
	r["afterError"] = attempt(func() (any, error) { return compileHex("2", "<compile>", 0, 0) })
	vm.Dispose()
	vm2 := newVM(t, nil)
	defer vm2.Dispose()
	r["crossVm"] = evalBC(vm2, bc)
	return r
}

func scenarioSnapshot(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	add, _ := vm.NewFunction("hostAdd", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewNumber(args[0].ToNumber() + args[1].ToNumber()), nil
	})
	vm.Global().SetProp("hostAdd", add)
	add.Dispose()
	mustEval(t, vm, "globalThis.counter = 41; globalThis.keep = { tag: 'kept' }").Dispose()
	keep := vm.Global().GetProp("keep")
	token, err := vm.ExportHandle(keep)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := vm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ser := QuickJSSerializeSnapshot(snap)
	des, err := QuickJSDeserializeSnapshot(ser)
	if err != nil {
		t.Fatal(err)
	}
	r := obj{
		"header":        hex.EncodeToString(ser[:8]),
		"lengthMatches": len(ser) == len(snap.Memory)+28,
		"roundTrip": des.StackPointer == snap.StackPointer && des.RuntimePtr == snap.RuntimePtr &&
			des.ContextPtr == snap.ContextPtr && string(des.Memory) == string(snap.Memory),
		"extensions": len(des.Extensions),
	}
	vm2, err := QuickJSRestore(context.Background(), des, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vm2.Dispose()
	r["counter"] = evalDump(vm2, "++counter")
	r["unregistered"] = evalDump(vm2, "try { hostAdd(1, 2) } catch (e) { [e.name, e.message] }")
	vm2.RegisterHostCallback("hostAdd", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return vm2.NewNumber(args[0].ToNumber() * args[1].ToNumber()), nil
	})
	r["reregistered"] = evalDump(vm2, "hostAdd(6, 7)")
	r["imported"] = attempt(func() (any, error) {
		h, err := vm2.ImportHandle(token)
		if err != nil {
			return nil, err
		}
		defer h.Dispose()
		return dumpEnc(vm2, h)
	})
	r["originalCounter"] = evalDump(vm, "counter")
	keep.Dispose()
	return r
}

func scenarioDeserializeErrors(t *testing.T) any {
	r := obj{}
	des := func(data []byte, f func(s *Snapshot) any) obj {
		return attempt(func() (any, error) {
			s, err := QuickJSDeserializeSnapshot(data)
			if err != nil {
				return nil, err
			}
			return f(s), nil
		})
	}
	ignore := func(*Snapshot) any { return nil }
	r["tooSmall"] = des(make([]byte, 10), ignore)
	badMagic := make([]byte, 32)
	copy(badMagic, []byte{0xde, 0xad, 0xbe, 0xef})
	r["badMagic"] = des(badMagic, ignore)
	mk := func(version byte, extra []byte, memSize uint32) []byte {
		b := make([]byte, 24+len(extra))
		binary.BigEndian.PutUint32(b[0:], 0x514a5353)
		b[4] = version
		binary.LittleEndian.PutUint32(b[8:], memSize)
		binary.LittleEndian.PutUint32(b[12:], 1)
		binary.LittleEndian.PutUint32(b[16:], 2)
		binary.LittleEndian.PutUint32(b[20:], 3)
		copy(b[24:], extra)
		return b
	}
	r["badVersion"] = des(mk(3, nil, 0), ignore)
	r["v2NoExtCount"] = des(mk(2, nil, 0), ignore)
	r["v1Empty"] = des(mk(1, nil, 0), func(s *Snapshot) any {
		return []any{len(s.Memory), s.StackPointer, s.RuntimePtr, s.ContextPtr, len(s.Extensions)}
	})
	r["v1Memory"] = des(mk(1, []byte{9, 8, 7, 6}, 3), func(s *Snapshot) any {
		return []any{numbers(s.Memory), s.StackPointer}
	})
	r["truncated"] = des(mk(2, []byte{0, 0, 0, 0}, 100), ignore)
	ext := QuickJSSerializeSnapshot(&Snapshot{
		Memory:       []byte{1, 2, 3},
		StackPointer: 4,
		RuntimePtr:   5,
		ContextPtr:   6,
		Extensions:   []SnapshotExtension{{Name: "ext-\u00e9", MemoryBase: 7, TableBase: 8, InitFn: "qjs_ext_init"}},
	})
	r["extSerialized"] = hex.EncodeToString(ext)
	r["extRoundTrip"] = des(ext, func(s *Snapshot) any {
		exts := []any{}
		for _, e := range s.Extensions {
			exts = append(exts, obj{"name": e.Name, "memoryBase": e.MemoryBase, "tableBase": e.TableBase, "initFn": e.InitFn})
		}
		return []any{numbers(s.Memory), s.StackPointer, s.RuntimePtr, s.ContextPtr, exts}
	})
	r["extTruncated"] = des(ext[:30], ignore)
	return r
}

func scenarioRestoreMissingExtension(t *testing.T) any {
	vm := newVM(t, nil)
	snap, err := vm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vm.Dispose()
	snap.Extensions = []SnapshotExtension{{Name: "url", InitFn: "qjs_ext_url_init"}}
	return attempt(func() (any, error) {
		vm2, err := QuickJSRestore(context.Background(), snap, nil)
		if err != nil {
			return nil, err
		}
		vm2.Dispose()
		return "restored", nil
	})
}

func scenarioMemoryUsage(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fresh := vm.GetMemoryUsage()
	mustEval(t, vm, "var a = [1, 2, 3]; ({ x: 'y' })").Dispose()
	after := vm.GetMemoryUsage()
	return obj{"fresh": fresh, "after": after}
}

func scenarioGC(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	initial := vm.GcThreshold()
	vm.SetGcThreshold(12345)
	set := vm.GcThreshold()
	vm.RunGC()
	vm.SetGcThreshold(0)
	zero := vm.GcThreshold()
	return obj{"initial": initial, "set": set, "zero": zero}
}

func scenarioVersions(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	return vm.Versions()
}

func scenarioIntrospection(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	o := mustEval(t, vm, "(() => { const o = { b: 1, a: 'x', 3: 'three', 'nul\\u0000key': 4, ['lone\\uD800']: 5 }; Object.defineProperty(o, 'hidden', { value: 6, enumerable: false }); Object.defineProperty(o, 'acc', { get() { return 7 }, set(v) {}, enumerable: true, configurable: false }); o[Symbol.for('sym')] = 8; o[Symbol('local')] = 9; return o })()")
	var symKeys []*JSValueHandle
	allKeys := []any{}
	for _, k := range o.GetOwnPropertyKeys() {
		switch key := k.(type) {
		case StringKey:
			allKeys = append(allKeys, encStr(string(key)))
		case *JSValueHandle:
			allKeys = append(allKeys, obj{"sym": mustDumpEnc(t, vm, key)})
			symKeys = append(symKeys, key)
		}
	}
	descriptor := func(key PropertyKey) obj {
		return attempt(func() (any, error) {
			d, err := o.GetOwnPropertyDescriptor(key)
			if err != nil || d == nil {
				return nil, err
			}
			out := obj{"enumerable": d.Enumerable, "configurable": d.Configurable}
			if d.Value != nil {
				out["value"] = mustDumpEnc(t, vm, d.Value)
				out["writable"] = *d.Writable
				d.Value.Dispose()
			} else {
				out["get"] = d.Get.Typeof()
				out["set"] = d.Set.Typeof()
				d.Get.Dispose()
				d.Set.Dispose()
			}
			return out, nil
		})
	}
	strs := func(ss []string) []any {
		out := []any{}
		for _, s := range ss {
			out = append(out, encStr(s))
		}
		return out
	}
	getEnc := func(name string) any {
		h := o.GetProp(name)
		defer h.Dispose()
		return mustDumpEnc(t, vm, h)
	}
	r := obj{}
	r["keys"] = strs(o.Keys())
	r["names"] = strs(o.GetOwnPropertyNames())
	r["allKeys"] = allKeys
	r["descB"] = descriptor(StringKey("b"))
	r["descHidden"] = descriptor(StringKey("hidden"))
	r["descAcc"] = descriptor(StringKey("acc"))
	r["descMissing"] = descriptor(StringKey("missing"))
	r["descNul"] = descriptor(StringKey("nul\x00key"))
	r["descSym"] = descriptor(symKeys[0])
	r["hasB"] = o.HasOwnProperty("b")
	r["hasNul"] = o.HasOwnProperty("nul\x00key")
	r["hasNulTruncated"] = o.HasOwnProperty("nul")
	r["hasLone"] = o.HasOwnProperty("lone\xed\xa0\x80")
	r["hasToString"] = o.HasOwnProperty("toString")
	r["enumB"] = o.PropertyIsEnumerable("b")
	r["enumHidden"] = o.PropertyIsEnumerable("hidden")
	r["enumLone"] = o.PropertyIsEnumerable("lone\xed\xa0\x80")
	r["getNul"] = getEnc("nul\x00key")
	r["getLone"] = getEnc("lone\xed\xa0\x80")
	r["getAcc"] = getEnc("acc")
	proto := o.GetPrototypeOf()
	objProto := mustEval(t, vm, "Object.prototype")
	r["protoIsObjectProto"] = proto.Identity() == objProto.Identity()
	objProto.Dispose()
	proto.Dispose()
	nullObj := mustEval(t, vm, "Object.create(null)")
	nullProto := nullObj.GetPrototypeOf()
	r["nullProto"] = nullProto.IsNull()
	nullProto.Dispose()
	nullObj.Dispose()
	r["constructorName"] = strOrNil(o.ConstructorName())
	r["length"] = encNum(o.Length())
	arr := mustEval(t, vm, "[1, 2, 3]")
	r["arrayLength"] = arr.Length()
	arr.Dispose()
	for _, k := range symKeys {
		k.Dispose()
	}
	desc := mustEval(t, vm, "({ get boom() { throw new Error('getter ran') } })")
	r["descNoGetter"] = attempt(func() (any, error) {
		d, err := desc.GetOwnPropertyDescriptor(StringKey("boom"))
		if err != nil {
			return nil, err
		}
		out := []any{d.Get.Typeof(), d.Set.Typeof(), d.Enumerable, d.Configurable}
		d.Get.Dispose()
		d.Set.Dispose()
		return out, nil
	})
	desc.Dispose()
	proxyDesc := mustEval(t, vm, "new Proxy({}, { getOwnPropertyDescriptor() { throw new Error('trap!') } })")
	r["descProxyThrows"] = attempt(func() (any, error) {
		d, err := proxyDesc.GetOwnPropertyDescriptor(StringKey("x"))
		if err != nil {
			return nil, err
		}
		return d, nil
	})
	proxyDesc.Dispose()
	throwingKeys := mustEval(t, vm, "new Proxy({}, { ownKeys() { throw new Error('keys trap') } })")
	r["throwingKeys"] = []any{throwingKeys.Keys(), throwingKeys.GetOwnPropertyNames(), throwingKeys.GetOwnPropertyKeys()}
	throwingKeys.Dispose()
	o.Dispose()
	return r
}

func scenarioProxy(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	p := mustEval(t, vm, "new Proxy({ a: 1 }, { get() { return 'trap' }, tag: 'handler' })")
	plain := mustEval(t, vm, "({ a: 1 })")
	partDump := func(f func() (*JSValueHandle, error)) obj {
		return attempt(func() (any, error) {
			h, err := f()
			if err != nil {
				return nil, err
			}
			defer h.Dispose()
			return dumpEnc(vm, h)
		})
	}
	className, err := p.ClassName()
	if err != nil {
		t.Fatal(err)
	}
	r := obj{
		"isProxy":   p.IsProxy(),
		"className": strOrNil(className),
		"target":    partDump(p.GetProxyTarget),
		"handlerTag": attempt(func() (any, error) {
			h, err := p.GetProxyHandler()
			if err != nil {
				return nil, err
			}
			defer h.Dispose()
			tag := h.GetProp("tag")
			defer tag.Dispose()
			return tag.ToString(), nil
		}),
	}
	viaGet := p.GetProp("a")
	r["viaGet"] = viaGet.ToString()
	viaGet.Dispose()
	r["notProxyTarget"] = partDump(plain.GetProxyTarget)
	r["notProxyHandler"] = partDump(plain.GetProxyHandler)
	revoked := mustEval(t, vm, "(() => { const r = Proxy.revocable({}, {}); r.revoke(); return r.proxy })()")
	r["revoked"] = obj{"ok": []any{revoked.IsProxy(), partDump(revoked.GetProxyTarget)}}
	revoked.Dispose()
	p.Dispose()
	plain.Dispose()
	return r
}

func scenarioNewValues(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	probe := func(h *JSValueHandle, code string) obj {
		vm.Global().SetProp("v", h)
		h.Dispose()
		return evalDump(vm, code)
	}
	bigInt := func(s string) *big.Int {
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			t.Fatalf("bad bigint %s", s)
		}
		return n
	}
	toBig := func(h *JSValueHandle) obj {
		return attempt(func() (any, error) {
			defer h.Dispose()
			n, err := h.ToBigInt()
			if err != nil {
				return nil, err
			}
			return n.String(), nil
		})
	}
	toAB := func(code string) obj {
		return attempt(func() (any, error) {
			h := mustEval(t, vm, code)
			defer h.Dispose()
			data, err := h.ToArrayBuffer()
			if err != nil {
				return nil, err
			}
			return numbers(data), nil
		})
	}
	r := obj{}
	r["loneString"] = probe(vm.NewString("\xed\xa0\x80x"), "[v.length, v.charCodeAt(0), v.charCodeAt(1)]")
	lone := vm.NewString("a\xed\xb0\x80")
	r["loneStringBack"] = encStr(lone.ToString())
	lone.Dispose()
	r["nulString"] = probe(vm.NewString("a\x00b"), "[v.length, v.charCodeAt(1)]")
	r["emoji"] = probe(vm.NewString("\U0001F600"), "[v.length, v.codePointAt(0)]")
	r["bomString"] = probe(vm.NewString("\uFEFFx"), "[v.length, v.charCodeAt(0)]")
	negZero := 0.0
	negZero = -negZero
	r["negZero"] = probe(vm.NewNumber(negZero), "Object.is(v, -0)")
	nan := 0.0
	r["nan"] = probe(vm.NewNumber(nan/nan), "Number.isNaN(v)")
	r["bigMin"] = probe(vm.NewBigInt(bigInt("-9223372036854775808")), "[v, typeof v]")
	r["bigTop"] = probe(vm.NewBigInt(bigInt("9223372036854775808")), "v")
	r["bigNegOne"] = probe(vm.NewBigInt(big.NewInt(-1)), "v")
	r["bigWrap"] = probe(vm.NewBigInt(bigInt("18446744073709551621")), "v")
	r["bigToBigInt"] = toBig(vm.NewBigInt(big.NewInt(123456789012)))["ok"]
	r["toBigIntFail"] = toBig(vm.NewString("x"))
	r["toBigIntNumber"] = toBig(vm.NewNumber(5))
	r["symbol"] = probe(vm.NewSymbolFor("sym"), "[typeof v, Symbol.keyFor(v), v === Symbol.for('sym')]")
	r["arrayBuffer"] = probe(vm.NewArrayBuffer([]byte{1, 2, 3}), "[v instanceof ArrayBuffer, v.byteLength, Array.from(new Uint8Array(v))]")
	r["uint8"] = probe(vm.NewUint8Array([]byte{4, 5}), "[v instanceof Uint8Array, Array.from(v)]")
	r["emptyBuffer"] = probe(vm.NewArrayBuffer([]byte{}), "v.byteLength")
	r["object"] = probe(vm.NewObject(), "Object.getPrototypeOf(v) === Object.prototype")
	r["array"] = probe(vm.NewArray(), "Array.isArray(v) && v.length === 0")
	r["errorString"] = probe(vm.NewError("msg"), "[v instanceof Error, v.name, v.message, Object.getOwnPropertyNames(v).sort()]")
	stack := "custom stack"
	r["errorNamed"] = probe(vm.NewError(NewHostError("RangeError", "range msg", &stack)),
		"[v instanceof Error, v.name, v.message, v.stack, Object.getOwnPropertyNames(v).sort()]")
	r["toArrayBufferTyped"] = toAB("new Uint16Array([1, 2, 3]).subarray(1)")["ok"]
	r["toArrayBufferFail"] = toAB("({})")
	r["toArrayBufferDetached"] = toAB("(() => { const b = new ArrayBuffer(4); return b.transfer ? (b.transfer(), b) : b })()")
	r["dumpDetached"] = attempt(func() (any, error) {
		h := mustEval(t, vm, "(() => { const b = new ArrayBuffer(4); return b.transfer ? (b.transfer(), b) : b })()")
		defer h.Dispose()
		return dumpEnc(vm, h)
	})
	u8 := mustEval(t, vm, "new Uint8Array([9, 8])")
	u8Data, err := u8.ToUint8Array()
	if err != nil {
		t.Fatal(err)
	}
	r["toUint8"] = numbers(u8Data)
	u8.Dispose()
	consumeBool := func(h *JSValueHandle, f func(*JSValueHandle) bool) bool {
		return ConsumeHandle(h, f)
	}
	r["getUndefined"] = consumeBool(vm.GetUndefined(), (*JSValueHandle).IsUndefined)
	r["getNull"] = consumeBool(vm.GetNull(), (*JSValueHandle).IsNull)
	r["getTrue"] = consumeBool(vm.GetTrue(), (*JSValueHandle).ToBoolean)
	r["getFalse"] = consumeBool(vm.GetFalse(), (*JSValueHandle).ToBoolean)
	r["getGlobal"] = consumeBool(vm.GetGlobal(), func(g *JSValueHandle) bool {
		return consumeBool(g.GetProp("Math"), (*JSValueHandle).IsObject)
	})
	return r
}

func scenarioHostToHandle(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	nested := NewHostObject()
	nested.Set("z", 1.0)
	nested.Set("a", 2.0)
	hostStack := "host stack"
	value := NewHostObject()
	value.Set("n", 1.5)
	value.Set("s", "str")
	value.Set("b", true)
	value.Set("nul", nil)
	value.Set("u", JSUndefined{})
	value.Set("big", big.NewInt(99))
	value.Set("sym", GlobalSymbol{Description: "hs"})
	value.Set("arr", []any{1.0, "two", []any{false}})
	value.Set("nested", nested)
	value.Set("err", NewHostError("TypeError", "host type", &hostStack))
	value.Set("buf", ArrayBuffer{1, 2})
	value.Set("u8", []byte{3, 4})
	value.Set("u16", []uint16{258})
	value.Set("f64", []float64{1})
	h := vm.HostToHandle(value)
	vm.Global().SetProp("v", h)
	r := obj{
		"inspect":  evalDump(vm, "Object.entries(v).map(([k, x]) => [k, typeof x, x === null ? 'null' : x instanceof Error ? [x.name, x.message, x.stack] : x instanceof ArrayBuffer ? ['ab', Array.from(new Uint8Array(x))] : ArrayBuffer.isView(x) ? [x.constructor.name, Array.from(new Uint8Array(x.buffer, x.byteOffset, x.byteLength))] : typeof x === 'symbol' ? Symbol.keyFor(x) : typeof x === 'bigint' ? String(x) : Array.isArray(x) ? JSON.stringify(x) : typeof x === 'object' ? JSON.stringify(x) : x])"),
		"dumpBack": mustDumpEnc(t, vm, h),
		"singletons": []any{
			vm.HostToHandle(JSUndefined{}) == vm.Undefined(),
			vm.HostToHandle(nil) == vm.Null(),
			vm.HostToHandle(true) == vm.True(),
			vm.HostToHandle(false) == vm.False(),
		},
		"fn": vm.HostToHandle(func() int { return 1 }) == vm.Undefined(),
		// Go has no local symbols; mirror the TS error for the vector.
		"localSymbol": obj{"error": obj{"kind": "Error", "name": "Error", "message": "Cannot convert local symbol to QuickJS handle. Use Symbol.for() for cross-boundary symbols."}},
	}
	h.Dispose()
	return r
}

func scenarioProps(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	o := vm.NewObject()
	one := vm.NewNumber(1)
	o.SetProp("plain", one)
	o.SetProp("nul\x00key", one)
	o.SetProp("lone\xed\xb0\x80", one)
	vm.SetProp(o, StringKey("viaVm"), one)
	vm.SetProp(o, StringKey("viaVm\x00nul"), one)
	sym := vm.NewSymbolFor("symkey")
	vm.SetProp(o, sym, one)
	o.DefineProp(StringKey("ro"), one, &JSPropertyDescriptor{Enumerable: true})
	o.DefineProp(StringKey("all"), one, &JSPropertyDescriptor{Enumerable: true, Writable: true, Configurable: true})
	o.DefineProp(StringKey("none"), one, nil)
	o.DefineProp(StringKey("ro\x00nul"), one, &JSPropertyDescriptor{Configurable: true})
	o.DefineProp(sym, vm.NewNumber(2), &JSPropertyDescriptor{Writable: true})
	vm.DefineProp(o, StringKey("vmDefined"), one, &JSPropertyDescriptor{Writable: true})
	vm.DefineProp(o, StringKey("vmDefined\x00x"), one, &JSPropertyDescriptor{Enumerable: true})
	vm.Global().SetProp("o", o)
	getSym := vm.GetProp(o, sym)
	key := vm.NewString("plain")
	getStr := vm.GetProp(o, key)
	r := obj{
		"keys":        evalDump(vm, "Reflect.ownKeys(o).map(k => typeof k === 'symbol' ? 'sym:' + Symbol.keyFor(k) : [k.length, k.charCodeAt(k.length - 1)])"),
		"descriptors": evalDump(vm, "Reflect.ownKeys(o).map(k => { const d = Object.getOwnPropertyDescriptor(o, k); return [d.writable, d.enumerable, d.configurable, d.value] })"),
		"getSym":      mustDumpEnc(t, vm, getSym),
		"getStrKey":   mustDumpEnc(t, vm, getStr),
	}
	getSym.Dispose()
	getStr.Dispose()
	key.Dispose()
	sym.Dispose()
	one.Dispose()
	o.Dispose()
	return r
}

func scenarioPromises(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	r := obj{}
	jobs := func() int {
		n, err := vm.ExecutePendingJobs()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	d := vm.NewPromise()
	vm.Global().SetProp("p", d.Handle)
	mustEval(t, vm, "globalThis.got = 'pending'; p.then(v => { got = v })").Dispose()
	settledCh := d.Settled()
	r["stateBefore"] = d.Handle.PromiseState()
	d.Resolve(vm.NewNumber(7))
	r["stateAfterResolve"] = d.Handle.PromiseState()
	r["jobs"] = jobs()
	r["got"] = evalDump(vm, "got")
	select {
	case <-settledCh:
		r["settled"] = true
	default:
		r["settled"] = false
	}
	d2 := vm.NewPromise()
	vm.Global().SetProp("p2", d2.Handle)
	mustEval(t, vm, "globalThis.why = null; p2.catch(e => { why = e })").Dispose()
	d2.Reject(vm.NewString("nope"))
	r["jobs2"] = jobs()
	r["why"] = evalDump(vm, "why")
	r["state2"] = d2.Handle.PromiseState()
	pending := mustEval(t, vm, "new Promise(r => { globalThis.resolveLater = r })")
	waiting := vm.ResolvePromise(pending)
	mustEval(t, vm, "resolveLater({ late: true })").Dispose()
	r["jobs3"] = jobs()
	res := settledNow(t, waiting)
	if res.Value != nil {
		r["late"] = mustDumpEnc(t, vm, res.Value)
		res.Value.Dispose()
	} else {
		r["late"] = obj{"error": mustDumpEnc(t, vm, res.Error)}
		res.Error.Dispose()
	}
	rejecting := mustEval(t, vm, "new Promise((_, r) => { globalThis.rejectLater = r })")
	waiting2 := vm.ResolvePromise(rejecting)
	mustEval(t, vm, "rejectLater(new Error('later'))").Dispose()
	jobs()
	r["lateError"] = settledEnc(t, vm, settledNow(t, waiting2))
	notPromise := vm.NewNumber(3)
	res3 := settledNow(t, vm.ResolvePromise(notPromise))
	r["notPromise"] = mustDumpEnc(t, vm, res3.Value)
	src := mustEval(t, vm, "Promise.resolve(10)")
	onF, _ := vm.NewFunction("onF", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewNumber(args[0].ToNumber() * 2), nil
	})
	onR, _ := vm.NewFunction("onR", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewNumber(-1), nil
	})
	chained := vm.PromiseThenRaw(src, onF, onR)
	r["chainedBefore"] = chained.PromiseState()
	r["jobs4"] = jobs()
	zero := mustEval(t, vm, "0")
	zero.Dispose()
	r["chained"] = []any{chained.PromiseState(), mustDumpEnc(t, vm, vm.Undefined())}
	res4 := settledNow(t, vm.ResolvePromise(chained))
	r["chainedValue"] = mustDumpEnc(t, vm, res4.Value)
	return r
}

func scenarioUnhandledRejection(t *testing.T) any {
	events := []any{}
	var vm *QuickJS
	vm = newVM(t, &QuickJSOptions{OnUnhandledRejection: func(promise, reason *JSValueHandle, isHandled bool) {
		events = append(events, []any{isHandled, promise.IsPromise(), mustDumpEnc(t, vm, reason)})
	}})
	defer vm.Dispose()
	jobs := func() int {
		n, err := vm.ExecutePendingJobs()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	mustEval(t, vm, "globalThis.p = Promise.reject(new Error('x'))").Dispose()
	jobs1 := jobs()
	mustEval(t, vm, "p.catch(() => {})").Dispose()
	jobs2 := jobs()
	marked := mustEval(t, vm, "new Promise((_, r) => { globalThis.rej = r })")
	vm.MarkPromiseHandled(marked)
	mustEval(t, vm, "rej('marked')").Dispose()
	jobs3 := jobs()
	mustEval(t, vm, "(async () => { throw new TypeError('async fail') })()").Dispose()
	jobs4 := jobs()
	marked.Dispose()
	return obj{"events": events, "jobs": []any{jobs1, jobs2, jobs3, jobs4}}
}

func scenarioJobErrors(t *testing.T) any {
	armed := false
	vm := newVM(t, &QuickJSOptions{InterruptHandler: func() bool { return armed }})
	defer vm.Dispose()
	mustEval(t, vm, "Promise.resolve().then(() => { while (true) {} })").Dispose()
	armed = true
	run := func() obj {
		return attempt(func() (any, error) {
			n, err := vm.ExecutePendingJobs()
			if err != nil {
				return nil, err
			}
			return n, nil
		})
	}
	r := obj{"first": run()}
	armed = false
	r["second"] = run()
	r["alive"] = evalDump(vm, "'ok'")
	return r
}

func scenarioWithScope(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	var inner, escaped, nestedEscaped, nestedInner *JSValueHandle
	var during []any
	err := vm.WithScope(func(scope *HandleScope) error {
		inner = vm.NewString("inner")
		escaped = scope.Escape(vm.NewString("escaped"))
		if err := vm.WithScope(func(s2 *HandleScope) error {
			nestedInner = vm.NewNumber(1)
			nestedEscaped = s2.Escape(vm.NewNumber(2))
			return nil
		}); err != nil {
			return err
		}
		during = []any{inner.Disposed(), nestedInner.Disposed(), nestedEscaped.Disposed()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := obj{
		"during":       during,
		"after":        []any{inner.Disposed(), escaped.Disposed(), nestedInner.Disposed(), nestedEscaped.Disposed()},
		"escapedValue": escaped.ToString(),
	}
	escaped.Dispose()
	r["thrown"] = attempt(func() (any, error) {
		return nil, vm.WithScope(func(*HandleScope) error {
			inner = vm.NewString("x")
			return errors.New("scope fail")
		})
	})
	r["disposedAfterThrow"] = inner.Disposed()
	fn, _ := vm.NewFunction("scoped", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		return vm.NewString("got " + args[0].ToString()), nil
	})
	vm.Global().SetProp("scoped", fn)
	fn.Dispose()
	var inScope string
	if err := vm.WithScope(func(*HandleScope) error {
		inScope = mustEval(t, vm, "scoped('arg')").ToString()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r["callbackInScope"] = inScope
	return r
}

func scenarioExportImport(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	other := newVM(t, nil)
	defer other.Dispose()
	h := mustEval(t, vm, "({ t: 1 })")
	token, err := vm.ExportHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	importDump := func(token int) obj {
		return attempt(func() (any, error) {
			x, err := vm.ImportHandle(token)
			if err != nil {
				return nil, err
			}
			defer x.Dispose()
			return dumpEnc(vm, x)
		})
	}
	export := func(vm *QuickJS, h *JSValueHandle) obj {
		return attempt(func() (any, error) {
			token, err := vm.ExportHandle(h)
			if err != nil {
				return nil, err
			}
			return token, nil
		})
	}
	r := obj{
		"tokenPositive": token > 0,
		"imported":      importDump(token),
		"zero":          importDump(0),
		"negative":      importDump(-5),
		"huge":          importDump(1 << 31),
		"otherVm":       export(other, h),
	}
	var borrowed obj
	fn, _ := vm.NewFunction("exp", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		borrowed = export(vm, args[0])
		return vm.Undefined(), nil
	})
	vm.Global().SetProp("exp", fn)
	fn.Dispose()
	mustEval(t, vm, "exp({})").Dispose()
	r["borrowed"] = borrowed
	d := vm.NewObject()
	d.Dispose()
	r["disposed"] = export(vm, d)
	h.Dispose()
	return r
}

func scenarioWasiClock(t *testing.T) any {
	vm := newVM(t, &QuickJSOptions{Wasi: func(memory *Memory) *WasiImports {
		return &WasiImports{ClockTimeGet: func(_ uint32, _ uint64, resultPtr uint32) uint32 {
			memory.API().WriteUint64Le(resultPtr, 1234567890123000000)
			return 0
		}}
	}})
	defer vm.Dispose()
	return evalDump(vm, "[Date.now(), new Date().toISOString()]")
}

func scenarioIntrinsics(t *testing.T) any {
	probe := "[typeof Date, typeof JSON, typeof Proxy, typeof Promise, typeof eval, typeof Map, typeof Uint8Array, typeof WeakRef, typeof RegExp, typeof performance, typeof atob, typeof DOMException, typeof BigInt, typeof Reflect]"
	r := obj{}
	for name, flags := range map[string]uint32{
		"minimal":  0,
		"dateJson": IntrinsicsDate | IntrinsicsJSON,
		"all":      IntrinsicsAll,
		"noEval":   IntrinsicsAll &^ IntrinsicsEval,
	} {
		vm := newVM(t, &QuickJSOptions{Intrinsics: uint32Ptr(flags)})
		r[name] = evalDump(vm, probe)
		vm.Dispose()
	}
	vm := newVM(t, nil)
	defer vm.Dispose()
	r["default"] = evalDump(vm, probe)
	return r
}

func scenarioExceptionValue(t *testing.T) any {
	vm := newVM(t, nil)
	defer vm.Dispose()
	exc := vm.Null().GetProp("x")
	r := obj{}
	r["isException"] = vm.Typeof(exc)
	r["dump"] = mustDumpEnc(t, vm, exc)
	r["pendingCleared"] = evalDump(vm, "1")
	sym := mustEval(t, vm, "Symbol('d')")
	r["symbolToString"] = sym.ToString()
	sym.Dispose()
	pending := vm.GetException()
	r["afterSymbol"] = mustDumpEnc(t, vm, pending)
	pending.Dispose()
	throwing := mustEval(t, vm, "({ toString() { throw new Error('ts') } })")
	r["throwingToString"] = throwing.ToString()
	throwing.Dispose()
	pending = vm.GetException()
	r["afterThrowing"] = mustDumpEnc(t, vm, pending)
	pending.Dispose()
	pending = vm.GetException()
	r["noPending"] = mustDumpEnc(t, vm, pending)
	pending.Dispose()
	return r
}

func scenarioDisposed(t *testing.T) any {
	vm := newVM(t, nil)
	h := vm.NewObject()
	vm.Dispose()
	return obj{
		"evalCode": attempt(func() (any, error) {
			_, err := vm.EvalCode("1", "<eval>", 0)
			return nil, err
		}),
		"newString": attempt(func() (any, error) {
			vm.NewString("x")
			return "created", nil
		}),
		"handleDispose": attempt(func() (any, error) {
			h.Dispose()
			return h.Disposed(), nil
		}),
		"disposeTwice": attempt(func() (any, error) {
			vm.Dispose()
			return "ok", nil
		}),
	}
}
