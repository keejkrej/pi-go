// Generates testdata/vectors.json for internal/quickjs by running the real
// quickjs-wasi 3.6.2 glue under node. Regenerate with:
//
//   QUICKJS_WASI_DIR=/path/to/node_modules/quickjs-wasi node gen_vectors.mjs vectors.json
//
// The Go tests (vectors_test.go) replay every scenario below call for call.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const pkgDir = process.env.QUICKJS_WASI_DIR;
if (!pkgDir) throw new Error("set QUICKJS_WASI_DIR to the quickjs-wasi package directory");
const { CompileFlags, EvalFlags, Intrinsics, JSException, MAX_STACK_SIZE, QuickJS } = await import(
	pathToFileURL(join(pkgDir, "dist/index.js")).href
);

// Host timezone pinned to UTC; the Go tests pin time.Local the same way.
process.env.TZ = "UTC";
const wasm = readFileSync(join(pkgDir, "quickjs.wasm"));
const module = await WebAssembly.compile(wasm);
const create = (opts = {}) => QuickJS.create({ wasm: module, ...opts });

function isHostStack(s) {
	return /file:\/\/|node:internal|\(node:|gen_vectors\.mjs/.test(s);
}
function encStr(s) {
	if (s.isWellFormed()) return s;
	return { $: "wtf16", v: Array.from({ length: s.length }, (_, i) => s.charCodeAt(i)) };
}
function encNum(n) {
	if (Number.isNaN(n)) return { $: "number", v: "NaN" };
	if (n === Infinity) return { $: "number", v: "Infinity" };
	if (n === -Infinity) return { $: "number", v: "-Infinity" };
	if (Object.is(n, -0)) return { $: "number", v: "-0" };
	return n;
}
function enc(v, seen = new Map()) {
	if (v === undefined) return { $: "undefined" };
	if (v === null) return null;
	switch (typeof v) {
		case "boolean":
			return v;
		case "number":
			return encNum(v);
		case "string":
			return encStr(v);
		case "bigint":
			return { $: "bigint", v: v.toString() };
		case "symbol": {
			const k = Symbol.keyFor(v);
			return k === undefined ? { $: "localsymbol" } : { $: "symbol", v: encStr(k) };
		}
		case "function":
			return { $: "function" };
	}
	if (v instanceof ArrayBuffer) return { $: "ArrayBuffer", v: Array.from(new Uint8Array(v)) };
	if (v instanceof Uint8Array) return { $: "Uint8Array", v: Array.from(v) };
	if (v instanceof Uint16Array) return { $: "Uint16Array", v: Array.from(v) };
	if (v instanceof Uint32Array) return { $: "Uint32Array", v: Array.from(v) };
	if (v instanceof Float64Array) return { $: "Float64Array", v: Array.from(v, encNum) };
	if (v instanceof Error) {
		return {
			$: "Error",
			name: encStr(v.name),
			message: encStr(v.message),
			stack: v.stack === undefined || isHostStack(v.stack) ? { $: "host" } : encStr(v.stack),
		};
	}
	if (Array.isArray(v)) {
		if (v.length === 0) return { $: "array", v: [] };
		if (seen.has(v)) return { $: "ref", id: seen.get(v) };
		const id = seen.size;
		seen.set(v, id);
		return { $: "array", id, v: v.map((x) => enc(x, seen)) };
	}
	if (seen.has(v)) return { $: "ref", id: seen.get(v) };
	const id = seen.size;
	seen.set(v, id);
	return { $: "object", id, v: Object.entries(v).map(([k, x]) => [encStr(k), enc(x, seen)]) };
}
function excInfo(e) {
	if (e instanceof JSException) {
		const r = {
			kind: "JSException",
			name: encStr(e.name),
			message: encStr(e.message),
			stack: e.stack === undefined ? null : encStr(e.stack),
		};
		e.dispose();
		return r;
	}
	if (e instanceof Error) return { kind: e.constructor.name, name: e.name, message: encStr(e.message) };
	return { kind: typeof e, message: String(e) };
}
function attempt(fn) {
	try {
		return { ok: fn() };
	} catch (e) {
		return { error: excInfo(e) };
	}
}
async function attemptAsync(fn) {
	try {
		return { ok: await fn() };
	} catch (e) {
		return { error: excInfo(e) };
	}
}
const dumpEnc = (vm, h) => enc(vm.dump(h));
const evalDump = (vm, code, filename = "<eval>", flags = 0) =>
	attempt(() => {
		const h = vm.evalCode(code, filename, flags);
		try {
			return dumpEnc(vm, h);
		} finally {
			h.dispose();
		}
	});

// ---- eval cases: fresh VM, evalCode, dump + typeof ----
const evalCases = [
	["number", "1 + 2"],
	["string", "'hello ' + 'world'"],
	["bomString", "'\\uFEFFbom'"],
	["bomMidString", "'a\\uFEFFb'"],
	["bomWithSurrogate", "'\\uFEFF\\uD800x'"],
	["nulString", "'a\\u0000b'"],
	["emoji", "'\\uD83D\\uDE00 emoji'"],
	["loneLow", "'lone \\uDC00 low'"],
	["loneHighEnd", "'end\\uD83D'"],
	["undefined", "undefined"],
	["null", "null"],
	["true", "true"],
	["false", "false"],
	["nan", "NaN"],
	["negZero", "-0"],
	["infinity", "-Infinity"],
	["big", "1e300"],
	["fraction", "0.1 + 0.2"],
	["bigint", "123456789012345678n"],
	["bigintNeg", "-5n"],
	["bigintHuge", "2n ** 64n + 7n"],
	["bigintMin", "-(2n ** 63n)"],
	["globalSymbol", "Symbol.for('x')"],
	["localSymbol", "Symbol('local')"],
	["wellKnownSymbol", "Symbol.iterator"],
	["arrayBuffer", "new Uint8Array([1, 2, 3, 4]).buffer"],
	["uint8", "new Uint8Array([1, 2, 255])"],
	["int8", "new Int8Array([-1, 2])"],
	["clamped", "new Uint8ClampedArray([300, -5])"],
	["uint16", "new Uint16Array([1, 65535])"],
	["int16", "new Int16Array([-2])"],
	["int32", "new Int32Array([-1, 7])"],
	["float32", "new Float32Array([1.5])"],
	["float64", "new Float64Array([1.5, NaN, -0])"],
	["bigint64", "new BigInt64Array([1n])"],
	["subarray", "new Uint8Array([1, 2, 3, 4]).subarray(1, 3)"],
	["dataView", "new DataView(new ArrayBuffer(2))"],
	["array", "[1, 'two', [3, [4]], null, undefined]"],
	["emptyArray", "[]"],
	["holeyArray", "[, 1]"],
	["cyclicArray", "(() => { const a = [1]; a.push(a); return a })()"],
	["cyclicObject", "(() => { const o = { x: 1 }; o.self = o; return o })()"],
	["sharedObject", "(() => { const s = { v: 1 }; return [s, s, { s }] })()"],
	["keyOrder", "({ b: 1, a: 2, 10: 'ten', 2: 'two' })"],
	["protoKey", "({ ['__proto__']: 5, x: 1 })"],
	["getter", "({ get g() { return 42 }, n: 1 })"],
	["methods", "({ f() {}, n: 1 })"],
	["nulKey", "({ 'a\\u0000b': 1 })"],
	["surrogateKey", "({ ['k\\uD800']: 2 })"],
	["map", "new Map([[1, 2]])"],
	["date", "new Date(0)"],
	["regexp", "/re/g"],
	["error", "new Error('m')"],
	["typeError", "new TypeError('t')"],
	["namedError", "(() => { const e = new Error('x'); e.name = 'Custom'; return e })()"],
	["errorNoStack", "(() => { const e = new RangeError('r'); delete e.stack; return e })()"],
	["errorProps", "Object.assign(new Error('with props'), { code: 42 })"],
	["aggregateError", "new AggregateError([1], 'agg')"],
	["promise", "Promise.resolve(1)"],
	["function", "(function foo() {})"],
	["arrow", "() => 1"],
	["classInstance", "new (class Point { constructor() { this.x = 1 } })()"],
	["throwError", "throw new Error('boom')"],
	["throwTypeError", "throw new TypeError('bad type')"],
	["throwNumber", "throw 42"],
	["throwString", "throw 'str'"],
	["throwNull", "throw null"],
	["throwUndefined", "throw undefined"],
	["throwObjectMessage", "throw { message: 'obj msg' }"],
	["throwObjectName", "throw { name: 'N' }"],
	["throwNamed", "throw Object.assign(new Error('e'), { name: 'Named' })"],
	["throwSubclass", "class E extends Error { constructor(m) { super(m); this.name = 'E' } }; throw new E('custom')"],
	["syntaxError", "let let = 1"],
	["referenceError", "undefinedVariable"],
	["nullProperty", "null.x"],
	["nestedStack", "function a() { b() }\nfunction b() { throw new Error('deep') }\na()"],
	["stackOverflowDefault", "function f(n) { return f(n + 1) + 1 }\nf(0)"],
	["json", "JSON.parse('{\"a\":[1,2,{\"b\":null}]}')"],
	["unicode", "'h\\u00e9llo \\u4e16\\u754c'"],
];
const evalResults = [];
for (const [name, code] of evalCases) {
	const vm = await create();
	const r = attempt(() => {
		const h = vm.evalCode(code, "case.js");
		try {
			return { typeof: h.typeof, dump: dumpEnc(vm, h) };
		} finally {
			h.dispose();
		}
	});
	evalResults.push({ name, code, result: r });
	vm.dispose();
}

// ---- eval flags ----
const flagCases = [
	["strict", "x = 1", EvalFlags.STRICT],
	["sloppy", "x = 1", 0],
	["compileOnly", "1 + 2", EvalFlags.COMPILE_ONLY],
	["module", "export const a = 1; export default 'd'", EvalFlags.TYPE_MODULE],
	["moduleThrows", "throw new Error('mod boom')", EvalFlags.TYPE_MODULE],
	["moduleSyntax", "export const = 1", EvalFlags.TYPE_MODULE],
	["async", "await Promise.resolve(5); 42", EvalFlags.ASYNC],
	["asyncThrows", "await null; throw new RangeError('later')", EvalFlags.ASYNC],
	["asyncValue", "const v = await Promise.resolve({ k: 'v' }); v", EvalFlags.ASYNC],
	["backtraceBarrier", "throw new Error('barrier')", EvalFlags.BACKTRACE_BARRIER],
];
const flagResults = [];
for (const [name, code, flags] of flagCases) {
	const vm = await create();
	const r = await attemptAsync(async () => {
		const h = vm.evalCode(code, "flags.js", flags);
		const out = { typeof: h.typeof, isPromise: h.isPromise };
		if (h.isPromise) {
			out.before = h.promiseState;
			out.jobs = vm.executePendingJobs();
			out.after = h.promiseState;
			const settled = await vm.resolvePromise(h);
			if ("value" in settled) {
				out.value = dumpEnc(vm, settled.value);
				settled.value.dispose();
			} else {
				out.error = dumpEnc(vm, settled.error);
				settled.error.dispose();
			}
		} else {
			out.dump = dumpEnc(vm, h);
		}
		h.dispose();
		return out;
	});
	flagResults.push({ name, code, flags, result: r });
	vm.dispose();
}

// ---- value introspection ----
const valueExprs = [
	"undefined",
	"null",
	"true",
	"0",
	"-0",
	"42.5",
	"NaN",
	"''",
	"'42'",
	"'abc'",
	"7n",
	"Symbol.for('s')",
	"({})",
	"[1, 2, 3]",
	"(function named(a, b) {})",
	"() => {}",
	"new Error('e')",
	"Promise.resolve(1)",
	"Promise.reject(2)",
	"new Promise(() => {})",
	"new ArrayBuffer(8)",
	"new Uint8Array(3)",
	"new Proxy({}, {})",
	"new Map()",
	"new Set()",
	"new Date(0)",
	"/x/",
	"new WeakRef({})",
	"new WeakMap()",
	"new WeakSet()",
	"new DataView(new ArrayBuffer(1))",
	"Object.create(null)",
	"new (class Foo {})()",
	"({ toString() { return 'custom' } })",
	"({ toString() { throw new Error('nope') } })",
	"Math",
	"globalThis",
];
const valueResults = [];
{
	const vm = await create();
	for (const expr of valueExprs) {
		const h = vm.evalCode(`(${expr})`, "value.js");
		const rec = {
			expr,
			isUndefined: h.isUndefined,
			isNull: h.isNull,
			isBool: h.isBool,
			isNumber: h.isNumber,
			isString: h.isString,
			isSymbol: h.isSymbol,
			isBigInt: h.isBigInt,
			isObject: h.isObject,
			isArray: h.isArray,
			isFunction: h.isFunction,
			isError: h.isError,
			isPromise: h.isPromise,
			isArrayBuffer: h.isArrayBuffer,
			isProxy: h.isProxy,
			isMap: h.isMap,
			isSet: h.isSet,
			isDate: h.isDate,
			isRegExp: h.isRegExp,
			isWeakRef: h.isWeakRef,
			isWeakMap: h.isWeakMap,
			isWeakSet: h.isWeakSet,
			isDataView: h.isDataView,
			hasIdentity: h.identity !== 0,
			toBoolean: h.toBoolean(),
			classId: h.classId,
			className: attempt(() => h.className ?? null),
			promiseState: h.promiseState,
			typeof: h.typeof,
			toNumber: encNum(h.toNumber()),
			toString: encStr(h.toString()),
			constructorName: attempt(() => h.constructorName ?? null),
			length: encNum(h.length),
		};
		valueResults.push(rec);
		h.dispose();
	}
	vm.dispose();
}

// ---- named scenarios ----
const scenarios = {};

scenarios.hostFunctionBasic = await (async () => {
	const vm = await create();
	const fn = vm.newFunction("add", (a, b) => vm.newNumber(a.toNumber() + b.toNumber()));
	vm.global.setProp("add", fn);
	fn.dispose();
	const r = {
		sum: evalDump(vm, "add(2, 3)"),
		meta: evalDump(vm, "[add.name, add.length, typeof add, String(add)]"),
	};
	vm.dispose();
	return r;
})();

scenarios.hostFunctionThrows = await (async () => {
	const vm = await create();
	const f1 = vm.newFunction("fail", () => {
		throw new Error("host fail");
	});
	const f2 = vm.newFunction("failType", () => {
		throw new TypeError("bad type");
	});
	vm.global.setProp("fail", f1);
	vm.global.setProp("failType", f2);
	f1.dispose();
	f2.dispose();
	const probe = "try { %s() } catch (e) { [e instanceof Error, e.name, e.message, typeof e.stack, Object.prototype.hasOwnProperty.call(e, 'name')] }";
	const r = {
		fail: evalDump(vm, probe.replace("%s", "fail")),
		failType: evalDump(vm, probe.replace("%s", "failType")),
		uncaught: evalDump(vm, "fail()"),
	};
	vm.dispose();
	return r;
})();

scenarios.hostFunctionArgs = await (async () => {
	const vm = await create();
	const log = [];
	const fn = vm.newFunction("bridge", function (...args) {
		log.push([args.length, this.typeof, args.map((a) => a.typeof)]);
		return vm.undefined;
	});
	vm.global.setProp("bridge", fn);
	fn.dispose();
	const ret = evalDump(vm, "bridge('x'); bridge('x', 1, 2, 3); ({ m: bridge }).m(); bridge.call(5); bridge()");
	vm.dispose();
	return { log, ret };
})();

scenarios.hostFunctionReturnsArgument = await (async () => {
	const vm = await create();
	const fn = vm.newFunction("id", (a) => a);
	vm.global.setProp("id", fn);
	fn.dispose();
	const r = evalDump(vm, "const o = { a: [1] }; [id(o) === o, id('s'), id(1.5)]");
	vm.dispose();
	return r;
})();

scenarios.unregisteredCallback = await (async () => {
	const vm = await create();
	const eph = vm.newEphemeralFunction(() => vm.newString("eph ok"));
	vm.global.setProp("eph", eph);
	const first = evalDump(vm, "eph()");
	eph.dispose();
	const after = evalDump(vm, "try { eph() } catch (e) { [e.name, e.message] }");
	const named = vm.newFunction("named", () => vm.true);
	vm.global.setProp("named", named);
	named.dispose();
	const u1 = vm.unregisterHostCallback("named");
	const u2 = vm.unregisterHostCallback("named");
	const afterUnregister = evalDump(vm, "try { named() } catch (e) { [e.name, e.message] }");
	vm.dispose();
	return { first, after, u1, u2, afterUnregister };
})();

scenarios.duplicateFunctionName = await (async () => {
	const vm = await create();
	vm.newFunction("dup", () => vm.undefined).dispose();
	const r = attempt(() => vm.newFunction("dup", () => vm.undefined));
	vm.dispose();
	return r;
})();

scenarios.callFunction = await (async () => {
	const vm = await create();
	const fn = vm.evalCode("(function (a, b) { return this.k + a + b })");
	const obj = vm.evalCode("({ k: 1 })");
	const a = vm.newNumber(2);
	const b = vm.newNumber(3);
	const ok = attempt(() => vm.callFunction(fn, obj, a, b).consume((h) => dumpEnc(vm, h)));
	const thrower = vm.evalCode("(function () { throw new SyntaxError('in call') })");
	const thrown = attempt(() => vm.callFunction(thrower, vm.undefined).consume((h) => dumpEnc(vm, h)));
	const notFn = attempt(() => vm.callFunction(obj, vm.undefined).consume((h) => dumpEnc(vm, h)));
	const ctor = vm.evalCode("(class P { constructor(x) { this.x = x } })");
	const constructed = attempt(() => vm.construct(ctor, a).consume((h) => dumpEnc(vm, h)));
	const notCtor = attempt(() => vm.construct(obj, a).consume((h) => dumpEnc(vm, h)));
	vm.dispose();
	return { ok, thrown, notFn, constructed, notCtor };
})();

scenarios.interrupt = await (async () => {
	let calls = 0;
	let armed = true;
	const vm = await create({
		interruptHandler: () => {
			calls++;
			return armed && calls > 3;
		},
	});
	const loop = evalDump(vm, "let i = 0; while (true) { i++ }");
	const callsAtInterrupt = calls;
	armed = false;
	const after = evalDump(vm, "1 + 1");
	const caught = evalDump(vm, "globalThis.r = 0; try { for (let j = 0; j < 100000; j++) r++ } catch (e) { 'caught' }; r");
	vm.dispose();
	return { loop, callsAtInterrupt, after, caught };
})();

scenarios.stackOverflow = await (async () => {
	const vm = await create({ maxStackSize: MAX_STACK_SIZE });
	const r = {
		recursion: evalDump(vm, "function f(n) { return f(n + 1) + 1 }\nf(0)"),
		caught: evalDump(vm, "function g(n) { return g(n + 1) + 1 }\ntry { g(0) } catch (e) { [e.name, e.message, e instanceof RangeError] }"),
		after: evalDump(vm, "'still alive'"),
	};
	vm.dispose();
	const small = await create({ maxStackSize: 32 * 1024 });
	r.small = evalDump(small, "function h(n) { return h(n + 1) + 1 }\ntry { h(0) } catch (e) { [e.name, e.message] }");
	small.dispose();
	return r;
})();

scenarios.invalidMaxStackSize = await (async () => {
	const r = {};
	for (const v of [MAX_STACK_SIZE + 1, -1, 1.5]) {
		r[String(v)] = await attemptAsync(async () => {
			const vm = await create({ maxStackSize: v });
			vm.dispose();
			return "created";
		});
	}
	const zero = await create({ maxStackSize: 0 });
	r.zero = evalDump(zero, "1");
	zero.dispose();
	return r;
})();

scenarios.memoryLimit = await (async () => {
	const vm = await create({ memoryLimit: 2 * 1024 * 1024 });
	const r = {
		oom: evalDump(vm, "const a = []; for (let i = 0; i < 1e7; i++) a.push({ i }); a.length"),
		after: evalDump(vm, "1 + 1"),
		stringOom: evalDump(vm, "try { 'x'.repeat(4 * 1024 * 1024) ; 'no' } catch (e) { [e.name, e.message] }"),
		limit: vm.getMemoryUsage().mallocLimit,
	};
	vm.dispose();
	return r;
})();

const dateProbe =
	"[new Date(0).getHours(), new Date(0).getTimezoneOffset(), new Date(0).toString(), new Date(2024, 0, 15, 10, 30).toISOString(), new Date(2e12).getTimezoneOffset(), new Date(2e12).toString(), new Date(8.64e15).getTimezoneOffset(), new Date(-8.64e15).getTimezoneOffset()]";

scenarios.timezoneFixed = await (async () => {
	const vm = await create({ timezoneOffset: -480 });
	const r = evalDump(vm, dateProbe);
	vm.dispose();
	return r;
})();

scenarios.timezoneFunction = await (async () => {
	const seen = [];
	const vm = await create({
		timezoneOffset: (t) => {
			seen.push(t);
			return t < 1e9 ? 300 : -90;
		},
	});
	const r = evalDump(vm, dateProbe);
	vm.dispose();
	return { r, seen: seen.map(encNum) };
})();

scenarios.timezoneHost = await (async () => {
	const prev = process.env.TZ;
	process.env.TZ = "America/New_York";
	const vm = await create();
	const r = evalDump(
		vm,
		"[new Date(0).getTimezoneOffset(), new Date(1720000000000).getTimezoneOffset(), new Date(2024, 6, 1, 12).toString(), new Date(2024, 0, 1, 12).getTime(), new Date(-3e12).getTimezoneOffset(), new Date(-3e12).toString(), new Date(-8.64e15).getTimezoneOffset()]",
	);
	vm.dispose();
	process.env.TZ = prev;
	if (new Date(0).getTimezoneOffset() !== 0) throw new Error("TZ restore failed");
	return r;
})();

scenarios.moduleLoader = await (async () => {
	const calls = [];
	const sources = {
		"/root/dep.js": "export const x = 41; export { y as z } from './sub/y.js'",
		"/root/sub/y.js": "export const y = 'why'",
	};
	const vm = await create({
		moduleLoader: {
			normalize: (base, spec) => {
				calls.push(["normalize", base, spec]);
				const dir = base.includes("/") ? base.slice(0, base.lastIndexOf("/")) : "/root";
				const parts = (dir + "/" + spec).split("/");
				const out = [];
				for (const p of parts) {
					if (p === "." || p === "") continue;
					if (p === "..") out.pop();
					else out.push(p);
				}
				return "/" + out.join("/");
			},
			load: (name) => {
				calls.push(["load", name]);
				if (!(name in sources)) throw new Error(`cannot find module ${name}`);
				return sources[name];
			},
		},
	});
	const run = async (code, filename) => {
		return attemptAsync(async () => {
			const h = vm.evalCode(code, filename, EvalFlags.TYPE_MODULE);
			const jobs = vm.executePendingJobs();
			const settled = await vm.resolvePromise(h);
			h.dispose();
			if ("value" in settled) return { jobs, value: settled.value.consume((v) => dumpEnc(vm, v)) };
			return { jobs, error: settled.error.consume((v) => dumpEnc(vm, v)) };
		});
	};
	const ok = await run("import { x, z } from './dep.js'; export const y = x + 1; export const zz = z", "/root/main.js");
	const missing = await run("import { q } from './missing.js'; export default q", "/root/other.js");
	vm.dispose();
	return { ok, missing, calls };
})();

scenarios.moduleLoaderNoNormalize = await (async () => {
	const calls = [];
	const vm = await create({
		moduleLoader: {
			load: (name) => {
				calls.push(name);
				return "export default 'loaded:' + import.meta.url";
			},
		},
	});
	const r = await attemptAsync(async () => {
		const h = vm.evalCode("import d from 'pkg/thing'; export const v = d", "main.js", EvalFlags.TYPE_MODULE);
		vm.executePendingJobs();
		const settled = await vm.resolvePromise(h);
		h.dispose();
		return "value" in settled ? { value: settled.value.consume((v) => dumpEnc(vm, v)) } : { error: settled.error.consume((v) => dumpEnc(vm, v)) };
	});
	vm.dispose();
	return { r, calls };
})();

scenarios.moduleNoLoader = await (async () => {
	const vm = await create();
	const r = await attemptAsync(async () => {
		const h = vm.evalCode("import d from 'pkg'; export const v = d", "main.js", EvalFlags.TYPE_MODULE);
		vm.executePendingJobs();
		const settled = await vm.resolvePromise(h);
		h.dispose();
		return "value" in settled ? { value: settled.value.consume((v) => dumpEnc(vm, v)) } : { error: settled.error.consume((v) => dumpEnc(vm, v)) };
	});
	const dynamic = await attemptAsync(async () => {
		const h = vm.evalCode("import('dyn')", "dyn.js");
		vm.executePendingJobs();
		const settled = await vm.resolvePromise(h);
		h.dispose();
		return "value" in settled ? { value: settled.value.consume((v) => dumpEnc(vm, v)) } : { error: settled.error.consume((v) => dumpEnc(vm, v)) };
	});
	vm.dispose();
	return { r, dynamic };
})();

const hex = (u8) => Buffer.from(u8).toString("hex");

scenarios.compile = await (async () => {
	const vm = await create();
	const r = {};
	const bc = vm.compile("1 + 2", "<compile>");
	r.bytecode = hex(bc);
	r.evalBytecode = attempt(() => vm.evalBytecode(bc).consume((h) => dumpEnc(vm, h)));
	r.stripped = hex(vm.compile("function f(a) { return a * 2 }\nf(21)", "strip.js", 0, CompileFlags.STRIP_SOURCE | CompileFlags.STRIP_DEBUG));
	r.debug = hex(vm.compile("function f(a) { return a * 2 }\nf(21)", "strip.js"));
	const mod = vm.compile("export const z = 9", "mod.js", EvalFlags.TYPE_MODULE);
	r.module = hex(mod);
	r.moduleEval = await attemptAsync(async () => {
		const h = vm.evalBytecode(mod);
		vm.executePendingJobs();
		const settled = await vm.resolvePromise(h);
		h.dispose();
		return "value" in settled ? { value: settled.value.consume((v) => dumpEnc(vm, v)) } : { error: settled.error.consume((v) => dumpEnc(vm, v)) };
	});
	r.syntaxError = attempt(() => hex(vm.compile("let let = 1", "bad.js")));
	r.afterError = attempt(() => hex(vm.compile("2", "<compile>")));
	vm.dispose();
	const vm2 = await create();
	r.crossVm = attempt(() => vm2.evalBytecode(bc).consume((h) => dumpEnc(vm2, h)));
	vm2.dispose();
	return r;
})();

scenarios.snapshot = await (async () => {
	const vm = await create();
	const add = vm.newFunction("hostAdd", (a, b) => vm.newNumber(a.toNumber() + b.toNumber()));
	vm.global.setProp("hostAdd", add);
	add.dispose();
	vm.evalCode("globalThis.counter = 41; globalThis.keep = { tag: 'kept' }").dispose();
	const keep = vm.global.getProp("keep");
	const token = vm.exportHandle(keep);
	const snap = vm.snapshot();
	const ser = QuickJS.serializeSnapshot(snap);
	const des = QuickJS.deserializeSnapshot(ser);
	const r = {
		header: hex(ser.slice(0, 8)),
		lengthMatches: ser.length === snap.memory.byteLength + 28,
		roundTrip:
			des.stackPointer === snap.stackPointer &&
			des.runtimePtr === snap.runtimePtr &&
			des.contextPtr === snap.contextPtr &&
			Buffer.compare(Buffer.from(des.memory), Buffer.from(snap.memory)) === 0,
		extensions: des.extensions.length,
	};
	const vm2 = await QuickJS.restore(des, { wasm: module });
	r.counter = evalDump(vm2, "++counter");
	r.unregistered = evalDump(vm2, "try { hostAdd(1, 2) } catch (e) { [e.name, e.message] }");
	vm2.registerHostCallback("hostAdd", (a, b) => vm2.newNumber(a.toNumber() * b.toNumber()));
	r.reregistered = evalDump(vm2, "hostAdd(6, 7)");
	r.imported = attempt(() => vm2.importHandle(token).consume((h) => dumpEnc(vm2, h)));
	r.originalCounter = evalDump(vm, "counter");
	vm2.dispose();
	keep.dispose();
	vm.dispose();
	return r;
})();

scenarios.deserializeErrors = (() => {
	const r = {};
	r.tooSmall = attempt(() => QuickJS.deserializeSnapshot(new Uint8Array(10)));
	const badMagic = new Uint8Array(32);
	badMagic.set([0xde, 0xad, 0xbe, 0xef]);
	r.badMagic = attempt(() => QuickJS.deserializeSnapshot(badMagic));
	const mk = (version, extra, memSize) => {
		const b = new Uint8Array(24 + extra.length);
		const v = new DataView(b.buffer);
		v.setUint32(0, 0x514a5353, false);
		v.setUint8(4, version);
		v.setUint32(8, memSize, true);
		v.setUint32(12, 1, true);
		v.setUint32(16, 2, true);
		v.setUint32(20, 3, true);
		b.set(extra, 24);
		return b;
	};
	r.badVersion = attempt(() => QuickJS.deserializeSnapshot(mk(3, [], 0)));
	r.v2NoExtCount = attempt(() => QuickJS.deserializeSnapshot(mk(2, [], 0)));
	r.v1Empty = attempt(() => {
		const s = QuickJS.deserializeSnapshot(mk(1, [], 0));
		return [s.memory.length, s.stackPointer, s.runtimePtr, s.contextPtr, s.extensions.length];
	});
	r.v1Memory = attempt(() => {
		const s = QuickJS.deserializeSnapshot(mk(1, [9, 8, 7, 6], 3));
		return [Array.from(s.memory), s.stackPointer];
	});
	r.truncated = attempt(() => QuickJS.deserializeSnapshot(mk(2, [0, 0, 0, 0], 100)));
	const ext = QuickJS.serializeSnapshot({
		memory: new Uint8Array([1, 2, 3]),
		stackPointer: 4,
		runtimePtr: 5,
		contextPtr: 6,
		extensions: [{ name: "ext-é", memoryBase: 7, tableBase: 8, initFn: "qjs_ext_init" }],
	});
	r.extSerialized = hex(ext);
	r.extRoundTrip = attempt(() => {
		const s = QuickJS.deserializeSnapshot(ext);
		return [Array.from(s.memory), s.stackPointer, s.runtimePtr, s.contextPtr, s.extensions];
	});
	r.extTruncated = attempt(() => QuickJS.deserializeSnapshot(ext.slice(0, 30)));
	return r;
})();

scenarios.restoreMissingExtension = await (async () => {
	const vm = await create();
	const snap = vm.snapshot();
	vm.dispose();
	snap.extensions = [{ name: "url", memoryBase: 0, tableBase: 0, initFn: "qjs_ext_url_init" }];
	return attemptAsync(async () => {
		const vm2 = await QuickJS.restore(snap, { wasm: module });
		vm2.dispose();
		return "restored";
	});
})();

scenarios.memoryUsage = await (async () => {
	const vm = await create();
	const fresh = vm.getMemoryUsage();
	vm.evalCode("var a = [1, 2, 3]; ({ x: 'y' })").dispose();
	const after = vm.getMemoryUsage();
	vm.dispose();
	return { fresh, after };
})();

scenarios.gc = await (async () => {
	const vm = await create();
	const initial = vm.gcThreshold;
	vm.gcThreshold = 12345;
	const set = vm.gcThreshold;
	vm.runGC();
	vm.gcThreshold = 0;
	const zero = vm.gcThreshold;
	vm.dispose();
	return { initial, set, zero };
})();

scenarios.versions = await (async () => {
	const vm = await create();
	const v = vm.versions;
	vm.dispose();
	return v;
})();

scenarios.introspection = await (async () => {
	const vm = await create();
	const o = vm.evalCode(
		"(() => { const o = { b: 1, a: 'x', 3: 'three', 'nul\\u0000key': 4, ['lone\\uD800']: 5 }; Object.defineProperty(o, 'hidden', { value: 6, enumerable: false }); Object.defineProperty(o, 'acc', { get() { return 7 }, set(v) {}, enumerable: true, configurable: false }); o[Symbol.for('sym')] = 8; o[Symbol('local')] = 9; return o })()",
	);
	const symKeys = [];
	const keys = o.getOwnPropertyKeys().map((k) => {
		if (typeof k === "string") return encStr(k);
		const d = enc(vm.dump(k));
		symKeys.push(k);
		return { sym: d };
	});
	const descriptor = (key) =>
		attempt(() => {
			const d = o.getOwnPropertyDescriptor(key);
			if (d === undefined) return null;
			const out = { enumerable: d.enumerable, configurable: d.configurable };
			if ("value" in d) {
				out.value = enc(vm.dump(d.value));
				out.writable = d.writable;
				d.value.dispose();
			} else {
				out.get = d.get.typeof;
				out.set = d.set.typeof;
				d.get.dispose();
				d.set.dispose();
			}
			return out;
		});
	const r = {
		keys: o.keys().map(encStr),
		names: o.getOwnPropertyNames().map(encStr),
		allKeys: keys,
		descB: descriptor("b"),
		descHidden: descriptor("hidden"),
		descAcc: descriptor("acc"),
		descMissing: descriptor("missing"),
		descNul: descriptor("nul\u0000key"),
		descSym: descriptor(symKeys[0]),
		hasB: o.hasOwnProperty("b"),
		hasNul: o.hasOwnProperty("nul\u0000key"),
		hasNulTruncated: o.hasOwnProperty("nul"),
		hasLone: o.hasOwnProperty("lone\uD800"),
		hasToString: o.hasOwnProperty("toString"),
		enumB: o.propertyIsEnumerable("b"),
		enumHidden: o.propertyIsEnumerable("hidden"),
		enumLone: o.propertyIsEnumerable("lone\uD800"),
		getNul: o.getProp("nul\u0000key").consume((h) => enc(vm.dump(h))),
		getLone: o.getProp("lone\uD800").consume((h) => enc(vm.dump(h))),
		getAcc: o.getProp("acc").consume((h) => enc(vm.dump(h))),
		protoIsObjectProto: o.getPrototypeOf().consume((p) => {
			const objProto = vm.evalCode("Object.prototype");
			const same = p.identity === objProto.identity;
			objProto.dispose();
			return same;
		}),
		nullProto: vm.evalCode("Object.create(null)").consume((h) => h.getPrototypeOf().consume((p) => p.isNull)),
		constructorName: o.constructorName ?? null,
		length: encNum(o.length),
		arrayLength: vm.evalCode("[1, 2, 3]").consume((h) => h.length),
	};
	for (const k of symKeys) k.dispose();
	const desc = vm.evalCode("({ get boom() { throw new Error('getter ran') } })");
	r.descNoGetter = attempt(() => {
		const d = desc.getOwnPropertyDescriptor("boom");
		const out = [d.get.typeof, d.set.typeof, d.enumerable, d.configurable];
		d.get.dispose();
		d.set.dispose();
		return out;
	});
	desc.dispose();
	const proxyDesc = vm.evalCode("new Proxy({}, { getOwnPropertyDescriptor() { throw new Error('trap!') } })");
	r.descProxyThrows = attempt(() => proxyDesc.getOwnPropertyDescriptor("x"));
	proxyDesc.dispose();
	const throwingKeys = vm.evalCode("new Proxy({}, { ownKeys() { throw new Error('keys trap') } })");
	r.throwingKeys = [throwingKeys.keys(), throwingKeys.getOwnPropertyNames(), throwingKeys.getOwnPropertyKeys()];
	throwingKeys.dispose();
	o.dispose();
	vm.dispose();
	return r;
})();

scenarios.proxy = await (async () => {
	const vm = await create();
	const p = vm.evalCode("new Proxy({ a: 1 }, { get() { return 'trap' }, tag: 'handler' })");
	const plain = vm.evalCode("({ a: 1 })");
	const r = {
		isProxy: p.isProxy,
		className: p.className ?? null,
		target: attempt(() => p.getProxyTarget().consume((h) => enc(vm.dump(h)))),
		handlerTag: attempt(() => p.getProxyHandler().consume((h) => h.getProp("tag").consume((t) => t.toString()))),
		viaGet: p.getProp("a").consume((h) => h.toString()),
		notProxyTarget: attempt(() => plain.getProxyTarget().consume((h) => enc(vm.dump(h)))),
		notProxyHandler: attempt(() => plain.getProxyHandler().consume((h) => enc(vm.dump(h)))),
		revoked: attempt(() =>
			vm.evalCode("(() => { const r = Proxy.revocable({}, {}); r.revoke(); return r.proxy })()").consume((h) => {
				const out = [h.isProxy];
				out.push(attempt(() => h.getProxyTarget().consume((t) => enc(vm.dump(t)))));
				return out;
			}),
		),
	};
	p.dispose();
	plain.dispose();
	vm.dispose();
	return r;
})();

scenarios.newValues = await (async () => {
	const vm = await create();
	const probe = (h, code) => {
		vm.global.setProp("v", h);
		h.dispose();
		return evalDump(vm, code);
	};
	const r = {
		loneString: probe(vm.newString("\uD800x"), "[v.length, v.charCodeAt(0), v.charCodeAt(1)]"),
		loneStringBack: vm.newString("a\uDC00").consume((h) => encStr(h.toString())),
		nulString: probe(vm.newString("a\u0000b"), "[v.length, v.charCodeAt(1)]"),
		emoji: probe(vm.newString("\u{1F600}"), "[v.length, v.codePointAt(0)]"),
		bomString: probe(vm.newString("﻿x"), "[v.length, v.charCodeAt(0)]"),
		negZero: probe(vm.newNumber(-0), "Object.is(v, -0)"),
		nan: probe(vm.newNumber(NaN), "Number.isNaN(v)"),
		bigMin: probe(vm.newBigInt(-(2n ** 63n)), "[v, typeof v]"),
		bigTop: probe(vm.newBigInt(2n ** 63n), "v"),
		bigNegOne: probe(vm.newBigInt(-1n), "v"),
		bigWrap: probe(vm.newBigInt(2n ** 64n + 5n), "v"),
		bigToBigInt: vm.newBigInt(123456789012n).consume((h) => h.toBigInt().toString()),
		toBigIntFail: attempt(() => vm.newString("x").consume((h) => h.toBigInt().toString())),
		toBigIntNumber: attempt(() => vm.newNumber(5).consume((h) => h.toBigInt().toString())),
		symbol: probe(vm.newSymbolFor("sym"), "[typeof v, Symbol.keyFor(v), v === Symbol.for('sym')]"),
		arrayBuffer: probe(vm.newArrayBuffer(new Uint8Array([1, 2, 3]).buffer), "[v instanceof ArrayBuffer, v.byteLength, Array.from(new Uint8Array(v))]"),
		uint8: probe(vm.newUint8Array(new Uint8Array([4, 5])), "[v instanceof Uint8Array, Array.from(v)]"),
		emptyBuffer: probe(vm.newArrayBuffer(new ArrayBuffer(0)), "v.byteLength"),
		object: probe(vm.newObject(), "Object.getPrototypeOf(v) === Object.prototype"),
		array: probe(vm.newArray(), "Array.isArray(v) && v.length === 0"),
		errorString: probe(vm.newError("msg"), "[v instanceof Error, v.name, v.message, Object.getOwnPropertyNames(v).sort()]"),
		errorNamed: probe(
			vm.newError(Object.assign(new RangeError("range msg"), { stack: "custom stack" })),
			"[v instanceof Error, v.name, v.message, v.stack, Object.getOwnPropertyNames(v).sort()]",
		),
		toArrayBufferTyped: vm.evalCode("new Uint16Array([1, 2, 3]).subarray(1)").consume((h) => Array.from(new Uint8Array(h.toArrayBuffer()))),
		toArrayBufferFail: attempt(() => vm.evalCode("({})").consume((h) => Array.from(new Uint8Array(h.toArrayBuffer())))),
		toArrayBufferDetached: attempt(() =>
			vm.evalCode("(() => { const b = new ArrayBuffer(4); return b.transfer ? (b.transfer(), b) : b })()").consume((h) => Array.from(new Uint8Array(h.toArrayBuffer()))),
		),
		dumpDetached: attempt(() => vm.evalCode("(() => { const b = new ArrayBuffer(4); return b.transfer ? (b.transfer(), b) : b })()").consume((h) => enc(vm.dump(h)))),
		toUint8: vm.evalCode("new Uint8Array([9, 8])").consume((h) => Array.from(h.toUint8Array())),
		getUndefined: vm.getUndefined().consume((h) => h.isUndefined),
		getNull: vm.getNull().consume((h) => h.isNull),
		getTrue: vm.getTrue().consume((h) => h.toBoolean()),
		getFalse: vm.getFalse().consume((h) => h.toBoolean()),
		getGlobal: vm.getGlobal().consume((h) => h.getProp("Math").consume((m) => m.isObject)),
	};
	vm.dispose();
	return r;
})();

scenarios.hostToHandle = await (async () => {
	const vm = await create();
	const value = {
		n: 1.5,
		s: "str",
		b: true,
		nul: null,
		u: undefined,
		big: 99n,
		sym: Symbol.for("hs"),
		arr: [1, "two", [false]],
		nested: { z: 1, a: 2 },
		err: Object.assign(new TypeError("host type"), { stack: "host stack" }),
		buf: new Uint8Array([1, 2]).buffer,
		u8: new Uint8Array([3, 4]),
		u16: new Uint16Array([258]),
		f64: new Float64Array([1]),
	};
	const h = vm.hostToHandle(value);
	vm.global.setProp("v", h);
	const r = {
		inspect: evalDump(
			vm,
			"Object.entries(v).map(([k, x]) => [k, typeof x, x === null ? 'null' : x instanceof Error ? [x.name, x.message, x.stack] : x instanceof ArrayBuffer ? ['ab', Array.from(new Uint8Array(x))] : ArrayBuffer.isView(x) ? [x.constructor.name, Array.from(new Uint8Array(x.buffer, x.byteOffset, x.byteLength))] : typeof x === 'symbol' ? Symbol.keyFor(x) : typeof x === 'bigint' ? String(x) : Array.isArray(x) ? JSON.stringify(x) : typeof x === 'object' ? JSON.stringify(x) : x])",
		),
		dumpBack: enc(vm.dump(h)),
		singletons: [vm.hostToHandle(undefined) === vm.undefined, vm.hostToHandle(null) === vm.null, vm.hostToHandle(true) === vm.true, vm.hostToHandle(false) === vm.false],
		localSymbol: attempt(() => vm.hostToHandle(Symbol("local"))),
		fn: vm.hostToHandle(() => 1) === vm.undefined,
	};
	h.dispose();
	vm.dispose();
	return r;
})();

scenarios.props = await (async () => {
	const vm = await create();
	const o = vm.newObject();
	const one = vm.newNumber(1);
	o.setProp("plain", one);
	o.setProp("nul\u0000key", one);
	o.setProp("lone\uDC00", one);
	vm.setProp(o, "viaVm", one);
	vm.setProp(o, "viaVm\u0000nul", one);
	const sym = vm.newSymbolFor("symkey");
	vm.setProp(o, sym, one);
	o.defineProp("ro", one, { enumerable: true });
	o.defineProp("all", one, { enumerable: true, writable: true, configurable: true });
	o.defineProp("none", one);
	o.defineProp("ro\u0000nul", one, { configurable: true });
	o.defineProp(sym, vm.newNumber(2), { writable: true });
	vm.defineProp(o, "vmDefined", one, { writable: true });
	vm.defineProp(o, "vmDefined\u0000x", one, { enumerable: true });
	vm.global.setProp("o", o);
	const r = {
		keys: evalDump(vm, "Reflect.ownKeys(o).map(k => typeof k === 'symbol' ? 'sym:' + Symbol.keyFor(k) : [k.length, k.charCodeAt(k.length - 1)])"),
		descriptors: evalDump(vm, "Reflect.ownKeys(o).map(k => { const d = Object.getOwnPropertyDescriptor(o, k); return [d.writable, d.enumerable, d.configurable, d.value] })"),
		getSym: vm.getProp(o, sym).consume((h) => enc(vm.dump(h))),
		getStrKey: vm.newString("plain").consume((k) => vm.getProp(o, k).consume((h) => enc(vm.dump(h)))),
	};
	sym.dispose();
	one.dispose();
	o.dispose();
	vm.dispose();
	return r;
})();

scenarios.promises = await (async () => {
	const vm = await create();
	const r = {};
	const d = vm.newPromise();
	vm.global.setProp("p", d.handle);
	vm.evalCode("globalThis.got = 'pending'; p.then(v => { got = v })").dispose();
	let settled = false;
	d.settled.then(() => {
		settled = true;
	});
	r.stateBefore = d.handle.promiseState;
	d.resolve(vm.newNumber(7));
	r.stateAfterResolve = d.handle.promiseState;
	r.jobs = vm.executePendingJobs();
	r.got = evalDump(vm, "got");
	await Promise.resolve();
	await Promise.resolve();
	r.settled = settled;
	const d2 = vm.newPromise();
	vm.global.setProp("p2", d2.handle);
	vm.evalCode("globalThis.why = null; p2.catch(e => { why = e })").dispose();
	d2.reject(vm.newString("nope"));
	r.jobs2 = vm.executePendingJobs();
	r.why = evalDump(vm, "why");
	r.state2 = d2.handle.promiseState;
	// resolvePromise on a pending promise
	const pending = vm.evalCode("new Promise(r => { globalThis.resolveLater = r })");
	const waiting = vm.resolvePromise(pending);
	vm.evalCode("resolveLater({ late: true })").dispose();
	r.jobs3 = vm.executePendingJobs();
	const res = await waiting;
	r.late = "value" in res ? enc(vm.dump(res.value)) : { error: enc(vm.dump(res.error)) };
	("value" in res ? res.value : res.error).dispose();
	const rejecting = vm.evalCode("new Promise((_, r) => { globalThis.rejectLater = r })");
	const waiting2 = vm.resolvePromise(rejecting);
	vm.evalCode("rejectLater(new Error('later'))").dispose();
	vm.executePendingJobs();
	const res2 = await waiting2;
	r.lateError = "value" in res2 ? { value: enc(vm.dump(res2.value)) } : { error: enc(vm.dump(res2.error)) };
	// non-promise
	const notPromise = vm.newNumber(3);
	const res3 = await vm.resolvePromise(notPromise);
	r.notPromise = enc(vm.dump(res3.value));
	// promiseThenRaw
	const src = vm.evalCode("Promise.resolve(10)");
	const onF = vm.newFunction("onF", (v) => vm.newNumber(v.toNumber() * 2));
	const onR = vm.newFunction("onR", () => vm.newNumber(-1));
	const chained = vm.promiseThenRaw(src, onF, onR);
	r.chainedBefore = chained.promiseState;
	r.jobs4 = vm.executePendingJobs();
	r.chained = [chained.promiseState, enc(vm.dump(vm.evalCode("0").consume(() => vm.undefined)))];
	const res4 = await vm.resolvePromise(chained);
	r.chainedValue = enc(vm.dump(res4.value));
	vm.dispose();
	return r;
})();

scenarios.unhandledRejection = await (async () => {
	const events = [];
	const vm = await create({
		onUnhandledRejection: (promise, reason, isHandled) => {
			events.push([isHandled, promise.isPromise, enc(vm.dump(reason))]);
		},
	});
	vm.evalCode("globalThis.p = Promise.reject(new Error('x'))").dispose();
	const jobs1 = vm.executePendingJobs();
	vm.evalCode("p.catch(() => {})").dispose();
	const jobs2 = vm.executePendingJobs();
	const marked = vm.evalCode("new Promise((_, r) => { globalThis.rej = r })");
	vm.markPromiseHandled(marked);
	vm.evalCode("rej('marked')").dispose();
	const jobs3 = vm.executePendingJobs();
	vm.evalCode("(async () => { throw new TypeError('async fail') })()").dispose();
	const jobs4 = vm.executePendingJobs();
	marked.dispose();
	vm.dispose();
	return { events, jobs: [jobs1, jobs2, jobs3, jobs4] };
})();

scenarios.jobErrors = await (async () => {
	let armed = false;
	const vm = await create({ interruptHandler: () => armed });
	vm.evalCode("Promise.resolve().then(() => { while (true) {} })").dispose();
	armed = true;
	const r = { first: attempt(() => vm.executePendingJobs()) };
	armed = false;
	r.second = attempt(() => vm.executePendingJobs());
	r.alive = evalDump(vm, "'ok'");
	vm.dispose();
	return r;
})();

scenarios.withScope = await (async () => {
	const vm = await create();
	let inner;
	let escaped;
	let nestedEscaped;
	let nestedInner;
	const result = vm.withScope((scope) => {
		inner = vm.newString("inner");
		escaped = scope.escape(vm.newString("escaped"));
		vm.withScope((s2) => {
			nestedInner = vm.newNumber(1);
			nestedEscaped = s2.escape(vm.newNumber(2));
		});
		return [inner.disposed, nestedInner.disposed, nestedEscaped.disposed];
	});
	const r = {
		during: result,
		after: [inner.disposed, escaped.disposed, nestedInner.disposed, nestedEscaped.disposed],
		escapedValue: escaped.toString(),
	};
	escaped.dispose();
	const thrown = attempt(() =>
		vm.withScope(() => {
			inner = vm.newString("x");
			throw new Error("scope fail");
		}),
	);
	r.thrown = thrown;
	r.disposedAfterThrow = inner.disposed;
	const fn = vm.newFunction("scoped", (a) => vm.newString("got " + a.toString()));
	vm.global.setProp("scoped", fn);
	fn.dispose();
	r.callbackInScope = vm.withScope(() => vm.evalCode("scoped('arg')").toString());
	vm.dispose();
	return r;
})();

scenarios.exportImport = await (async () => {
	const vm = await create();
	const other = await create();
	const h = vm.evalCode("({ t: 1 })");
	const token = vm.exportHandle(h);
	const r = {
		tokenPositive: token > 0,
		imported: attempt(() => vm.importHandle(token).consume((x) => enc(vm.dump(x)))),
		zero: attempt(() => vm.importHandle(0)),
		negative: attempt(() => vm.importHandle(-5)),
		huge: attempt(() => vm.importHandle(2 ** 31)),
		otherVm: attempt(() => other.exportHandle(h)),
	};
	let borrowedErr;
	const fn = vm.newFunction("exp", (a) => {
		borrowedErr = attempt(() => vm.exportHandle(a));
		return vm.undefined;
	});
	vm.global.setProp("exp", fn);
	fn.dispose();
	vm.evalCode("exp({})").dispose();
	r.borrowed = borrowedErr;
	const d = vm.newObject();
	d.dispose();
	r.disposed = attempt(() => vm.exportHandle(d));
	h.dispose();
	other.dispose();
	vm.dispose();
	return r;
})();

scenarios.wasiClock = await (async () => {
	const vm = await create({
		wasi: (memory) => ({
			clock_time_get(_id, _precision, ptr) {
				new DataView(memory.buffer).setBigUint64(ptr, 1234567890123000000n, true);
				return 0;
			},
		}),
	});
	const r = evalDump(vm, "[Date.now(), new Date().toISOString()]");
	vm.dispose();
	return r;
})();

scenarios.intrinsics = await (async () => {
	const probe = "[typeof Date, typeof JSON, typeof Proxy, typeof Promise, typeof eval, typeof Map, typeof Uint8Array, typeof WeakRef, typeof RegExp, typeof performance, typeof atob, typeof DOMException, typeof BigInt, typeof Reflect]";
	const r = {};
	for (const [name, flags] of [
		["minimal", 0],
		["dateJson", Intrinsics.DATE | Intrinsics.JSON],
		["all", Intrinsics.ALL],
		["noEval", Intrinsics.ALL & ~Intrinsics.EVAL],
	]) {
		const vm = await create({ intrinsics: flags });
		r[name] = evalDump(vm, probe);
		vm.dispose();
	}
	const vm = await create();
	r.default = evalDump(vm, probe);
	vm.dispose();
	return r;
})();

scenarios.exceptionValue = await (async () => {
	const vm = await create();
	const exc = vm.null.getProp("x");
	const r = {
		isException: vm.typeof(exc),
		dump: enc(vm.dump(exc)),
		pendingCleared: evalDump(vm, "1"),
		symbolToString: vm.evalCode("Symbol('d')").consume((h) => h.toString()),
		afterSymbol: vm.getException().consume((e) => enc(vm.dump(e))),
		throwingToString: vm.evalCode("({ toString() { throw new Error('ts') } })").consume((h) => h.toString()),
		afterThrowing: vm.getException().consume((e) => enc(vm.dump(e))),
		noPending: vm.getException().consume((e) => enc(vm.dump(e))),
	};
	vm.dispose();
	return r;
})();

scenarios.disposed = await (async () => {
	const vm = await create();
	const h = vm.newObject();
	vm.dispose();
	return {
		evalCode: attempt(() => vm.evalCode("1")),
		newString: attempt(() => vm.newString("x")),
		handleDispose: attempt(() => {
			h.dispose();
			return h.disposed;
		}),
		disposeTwice: attempt(() => {
			vm.dispose();
			return "ok";
		}),
	};
})();

const out = {
	generator: "quickjs-wasi 3.6.2 via node " + process.version,
	evalCases: evalResults,
	flagCases: flagResults,
	values: valueResults,
	scenarios,
};
writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + "\n");
console.log("ok", Object.keys(scenarios).length, "scenarios");
