package quickjs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Go-specific behavior that has no TS counterpart to generate vectors from:
// the context/failure model, goroutines, Go host types, and direct checks of
// the WASI shim and string helpers.

func TestTextDecodeMatchesTextDecoder(t *testing.T) {
	// From node: new TextDecoder().decode(Buffer.from(hex, "hex")) as UTF-16 code units.
	cases := []struct {
		hex   string
		units []uint16
	}{
		{"", nil},
		{"616263", []uint16{97, 98, 99}},
		{"efbbbf616263", []uint16{97, 98, 99}},
		{"efbbbfefbbbf61", []uint16{65279, 97}},
		{"61efbbbf62", []uint16{97, 65279, 98}},
		{"ff", []uint16{65533}},
		{"e282", []uint16{65533}},
		{"e28261", []uint16{65533, 97}},
		{"eda080", []uint16{65533, 65533, 65533}},
		{"edb080", []uint16{65533, 65533, 65533}},
		{"f09080", []uint16{65533}},
		{"f4908080", []uint16{65533, 65533, 65533, 65533}},
		{"c0af", []uint16{65533, 65533}},
		{"c280", []uint16{128}},
		{"f09f9880", []uint16{55357, 56832}},
		{"80bf", []uint16{65533, 65533}},
		{"e0809f", []uint16{65533, 65533, 65533}},
		{"f880808080", []uint16{65533, 65533, 65533, 65533, 65533}},
		{"61ff62e2829f63", []uint16{97, 65533, 98, 8351, 99}},
		// Truncated sequences at end of input are one maximal subpart.
		{"f0", []uint16{65533}},
		{"f09f", []uint16{65533}},
		{"f09f98", []uint16{65533}},
		{"f09f9861", []uint16{65533, 97}},
		{"61f0", []uint16{97, 65533}},
		{"e0a0", []uint16{65533}},
		{"edbf", []uint16{65533, 65533}},
		{"ee80", []uint16{65533}},
		{"c2", []uint16{65533}},
		{"dfbf", []uint16{2047}},
		{"efbfbd", []uint16{65533}},
		{"f48fbfbf", []uint16{56319, 57343}},
	}
	for _, c := range cases {
		b, err := hex.DecodeString(c.hex)
		if err != nil {
			t.Fatal(err)
		}
		got := wtf8ToUTF16(textDecode(b))
		if fmt.Sprint(got) != fmt.Sprint(c.units) {
			t.Errorf("textDecode(%s) = %v, want %v", c.hex, got, c.units)
		}
	}
}

func TestDecodeWtf8(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"\uFEFFabc", "abc"}, // no surrogates: TextDecoder path strips the BOM
		{"\uFEFFa\xed\xa0\x80", "\uFEFFa\xed\xa0\x80"}, // surrogates: byte-exact
		{"x\xed\xbf\xbf", "x\xed\xbf\xbf"},
		{"\xff", "\uFFFD"},
	}
	for _, c := range cases {
		if got := decodeWtf8([]byte(c.in)); got != c.want {
			t.Errorf("decodeWtf8(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for key, want := range map[string]bool{"plain": false, "a\x00b": true, "lone\xed\xa0\x80": true, "\U0001F600": false} {
		if got := stringKeyNeedsValuePath(key); got != want {
			t.Errorf("stringKeyNeedsValuePath(%q) = %v", key, got)
		}
	}
}

func TestToInt32MatchesJS(t *testing.T) {
	// From node: x | 0.
	cases := []struct {
		in   float64
		want int32
	}{
		{0, 0}, {math.Copysign(0, -1), 0}, {1, 1}, {-1, -1}, {1.9, 1}, {-1.9, -1},
		{2147483647, 2147483647}, {2147483648, -2147483648}, {-2147483648, -2147483648},
		{-2147483649, 2147483647}, {4294967301, 5}, {1e20, 1661992960}, {-1e20, -1661992960},
		{1.5e300, 0}, {math.NaN(), 0}, {math.Inf(1), 0}, {math.Inf(-1), 0},
		{8.64e12, -1474199552}, {-8.64e12, 1474199552}, {9007199254740994, 2},
	}
	for _, c := range cases {
		if got := toInt32(c.in); got != c.want {
			t.Errorf("toInt32(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWasmSources(t *testing.T) {
	wasm := EmbeddedWasm()
	if !bytes.HasPrefix(wasm, []byte("\x00asm")) {
		t.Fatalf("embedded wasm has no wasm magic")
	}
	ctx := context.Background()
	m1, err := EmbeddedModule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := EmbeddedModule(ctx)
	if err != nil || m1 != m2 {
		t.Fatalf("EmbeddedModule not cached: %p %p %v", m1, m2, err)
	}
	if _, err := CompileModule(ctx, []byte("not wasm")); err == nil {
		t.Fatal("CompileModule accepted garbage")
	}
	for name, src := range map[string]WasmSource{"bytes": WasmBytes(wasm), "module": m1} {
		vm, err := QuickJSCreate(ctx, &QuickJSOptions{Wasm: src})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := mustEval(t, vm, "6 * 7").ToNumber(); got != 42 {
			t.Errorf("%s: got %v", name, got)
		}
		vm.Dispose()
	}
}

func TestContextCancelAbortsRunningCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, err := QuickJSCreate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Dispose()
	fn, _ := vm.NewFunction("cancel", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		cancel()
		return nil, nil
	})
	vm.Global().SetProp("cancel", fn)
	fn.Dispose()
	_, err = vm.EvalCode("cancel(); while (true) {}", "<eval>", 0)
	var wasmErr *WasmError
	if !errors.As(err, &wasmErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("want a *WasmError wrapping context.Canceled, got %T %v", err, err)
	}
	if wasmErr.Name() != "RuntimeError" {
		t.Errorf("name %q", wasmErr.Name())
	}
	if vm.Err() != err {
		t.Errorf("Err() = %v, want the call's error", vm.Err())
	}
	// Sticky: nothing runs any more.
	if _, err := vm.EvalCode("1", "<eval>", 0); err != wasmErr {
		t.Errorf("after failure EvalCode returned %v", err)
	}
	if _, err := vm.ExecutePendingJobs(); err != wasmErr {
		t.Errorf("after failure ExecutePendingJobs returned %v", err)
	}
}

func TestContextDeadlineAbortsInfiniteLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	vm, err := QuickJSCreate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Dispose()
	start := time.Now()
	_, err = vm.EvalCode("for (;;) {}", "<eval>", 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("abort took %v", elapsed)
	}
}

// worker.ts arms the interrupt from another thread (Atomics.store on a shared
// Int32Array); the Go equivalent is an atomic flag set by another goroutine.
func TestInterruptFromAnotherGoroutine(t *testing.T) {
	var flag atomic.Bool
	vm := newVM(t, &QuickJSOptions{InterruptHandler: flag.Load})
	defer vm.Dispose()
	timer := time.AfterFunc(50*time.Millisecond, func() { flag.Store(true) })
	defer timer.Stop()
	_, err := vm.EvalCode("for (;;) {}", "<eval>", 0)
	var exc *JSException
	if !errors.As(err, &exc) {
		t.Fatalf("want *JSException, got %T %v", err, err)
	}
	if exc.Name() != "InternalError" || exc.Message() != "interrupted" {
		t.Errorf("got %s: %s", exc.Name(), exc.Message())
	}
	exc.Dispose()
	if vm.Err() != nil {
		t.Fatalf("interrupt must not fail the VM: %v", vm.Err())
	}
	flag.Store(false)
	if got := mustEval(t, vm, "1 + 1").ToNumber(); got != 2 {
		t.Errorf("after interrupt got %v", got)
	}
}

// From node: -new Date(s * 1000).getTimezoneOffset() * 60 under TZ=<zone>.
// LMT offsets with seconds are truncated to whole minutes toward zero.
func TestHostTimezoneOffsetSeconds(t *testing.T) {
	prev := time.Local
	defer func() { time.Local = prev }()
	cases := []struct {
		zone string
		secs float64
		want float64
	}{
		{"America/New_York", 0, -18000},
		{"America/New_York", 1720000000, -14400},
		{"America/New_York", -3e9, -17760},
		{"America/New_York", 8.64e12, -14400},
		{"America/New_York", 8.64e12 + 1, math.NaN()},
		{"America/New_York", math.NaN(), math.NaN()},
		{"Asia/Kolkata", 0, 19800},
		{"Asia/Kolkata", -3e9, 19260},
		{"Africa/Monrovia", 0, -2640},
		{"Africa/Monrovia", -3e9, -2580},
		{"UTC", 0, 0},
	}
	for _, c := range cases {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Skipf("no tzdata: %v", err)
		}
		time.Local = loc
		got := hostTimezoneOffsetSeconds(c.secs)
		if got != c.want && !(math.IsNaN(got) && math.IsNaN(c.want)) {
			t.Errorf("%s %v: got %v, want %v", c.zone, c.secs, got, c.want)
		}
	}
}

func TestStickyFailureAfterTrap(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fn := mustEval(t, vm, "(function () { return 1 })")
	// No maxStackSize: the guest has no stack guard and the wasm call traps.
	_, err := vm.EvalCode("function f(n) { return f(n + 1) + 1 }\nf(0)", "<eval>", 0)
	var wasmErr *WasmError
	if !errors.As(err, &wasmErr) {
		t.Fatalf("want *WasmError, got %T %v", err, err)
	}
	if vm.Err() != wasmErr {
		t.Fatalf("Err() = %v", vm.Err())
	}
	checks := map[string]error{}
	_, checks["EvalCode"] = vm.EvalCode("1", "<eval>", 0)
	_, checks["CallFunction"] = vm.CallFunction(fn, vm.Undefined())
	_, checks["Compile"] = vm.Compile("1", "<compile>", 0, 0)
	_, checks["EvalBytecode"] = vm.EvalBytecode([]byte{1})
	_, checks["ExecutePendingJobs"] = vm.ExecutePendingJobs()
	_, checks["Snapshot"] = vm.Snapshot()
	_, checks["Dump"] = vm.Dump(fn)
	for name, err := range checks {
		if err != wasmErr {
			t.Errorf("%s returned %v, want the sticky failure", name, err)
		}
	}
	// Non-error methods do not panic; they return zero values.
	if s := vm.NewString("x").ToString(); s != "" && s != "<null>" {
		t.Errorf("NewString after failure: %q", s)
	}
}

func TestDisposeInsideHostCallback(t *testing.T) {
	vm := newVM(t, nil)
	ran := false
	fn, _ := vm.NewFunction("kill", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		vm.Dispose()
		return nil, nil
	})
	after, _ := vm.NewFunction("after", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		ran = true
		return nil, nil
	})
	vm.Global().SetProp("kill", fn)
	vm.Global().SetProp("after", after)
	fn.Dispose()
	after.Dispose()
	_, err := vm.EvalCode("kill(); after()", "<eval>", 0)
	if !errors.Is(err, errDisposed) {
		t.Fatalf("want the disposed error, got %v", err)
	}
	if ran {
		t.Error("guest code ran after Dispose")
	}
	if vm.mod != nil {
		t.Error("instance not closed after the outermost call returned")
	}
	if _, err := vm.EvalCode("1", "<eval>", 0); !errors.Is(err, errDisposed) {
		t.Errorf("EvalCode after dispose: %v", err)
	}
	vm.Dispose()
}

func TestHostCallbackPanicFailsVM(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	fn, _ := vm.NewFunction("boom", func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
		panic("host exploded")
	})
	vm.Global().SetProp("boom", fn)
	fn.Dispose()
	_, err := vm.EvalCode("try { boom() } catch (e) { 'caught' }", "<eval>", 0)
	var wasmErr *WasmError
	if !errors.As(err, &wasmErr) || !strings.Contains(err.Error(), "host exploded") {
		t.Fatalf("want a *WasmError carrying the panic, got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("message should be the first line only: %q", err.Error())
	}
}

func TestHostFunctionResults(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	stack := "Thrown: at host"
	fns := map[string]HostFunction{
		"nothing": func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) { return nil, nil },
		"withStack": func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
			return nil, NewHostError("SyntaxError", "bad", &stack)
		},
		"wrapped": func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
			return nil, fmt.Errorf("outer: %w", errors.New("inner"))
		},
		"nested": func(*JSValueHandle, ...*JSValueHandle) (*JSValueHandle, error) {
			// Re-enter the same export (qjs_eval) from inside a host call.
			return vm.EvalCode("'nested ' + (1 + 1)", "nested.js", 0)
		},
	}
	for name, fn := range fns {
		h, err := vm.NewFunction(name, fn)
		if err != nil {
			t.Fatal(err)
		}
		vm.Global().SetProp(name, h)
		h.Dispose()
	}
	got := evalDump(vm, "const c = f => { try { return f() } catch (e) { return [e.name, e.message, e.stack] } }; [nothing(), c(withStack), c(wrapped), nested()]")
	want := `{"ok":{"$":"array","id":0,"v":[{"$":"undefined"},{"$":"array","id":1,"v":["SyntaxError","bad","Thrown: at host"]},{"$":"array","id":2,"v":["Error","outer: inner",{"$":"undefined"}]},"nested 2"]}}`
	if compact(normalize(t, got)) != want {
		// QuickJS may attach its own stack to errors created without one.
		var diffs []string
		diff("results", decodeJSON(t, json.RawMessage(want)), normalize(t, got), &diffs)
		for _, d := range diffs {
			if !strings.Contains(d, "[2].v[2]") {
				t.Error(d)
			}
		}
	}
}

func TestReentrantHostCalls(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	recurse := mustEval(t, vm, "(n) => 'g' + down(n)")
	depth := 0
	maxDepth := 0
	down, _ := vm.NewFunction("down", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		depth++
		defer func() { depth-- }()
		maxDepth = max(maxDepth, depth)
		n := args[0].ToNumber()
		if n == 0 {
			return vm.NewString("bottom"), nil
		}
		return vm.CallFunction(recurse, vm.Undefined(), vm.NewNumber(n-1))
	})
	vm.Global().SetProp("down", down)
	down.Dispose()
	res, err := vm.CallFunction(recurse, vm.Undefined(), vm.NewNumber(30))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.ToString(), strings.Repeat("g", 31)+"bottom"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if maxDepth != 31 {
		t.Errorf("max nesting %d", maxDepth)
	}
	// A guest exception thrown deep inside propagates through every level.
	_, err = vm.EvalCode("down.call(null, 3); globalThis.down = () => { throw new TypeError('deep') }; (n => 'x' + down(n))(1)", "<eval>", 0)
	var jsErr *JSException
	if !errors.As(err, &jsErr) || jsErr.Name() != "TypeError" || jsErr.Message() != "deep" {
		t.Errorf("got %v", err)
	}
}

func TestParallelVMs(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vm, err := QuickJSCreate(context.Background(), &QuickJSOptions{MaxStackSize: intPtr(MaxStackSize)})
			if err != nil {
				errs <- err
				return
			}
			defer vm.Dispose()
			fn, err := vm.NewFunction("id", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
				return vm.NewNumber(args[0].ToNumber() + float64(i)), nil
			})
			if err != nil {
				errs <- err
				return
			}
			vm.Global().SetProp("id", fn)
			fn.Dispose()
			h, err := vm.EvalCode("let s = 0; for (let j = 0; j < 2000; j++) s += id(j); s", "<eval>", 0)
			if err != nil {
				errs <- err
				return
			}
			if got, want := h.ToNumber(), float64(1999*2000/2+2000*i); got != want {
				errs <- fmt.Errorf("worker %d: got %v, want %v", i, got, want)
			}
			h.Dispose()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestHostToHandleGoTypes(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	h := vm.HostToHandle(map[string]any{
		"b":      int64(-2),
		"a":      uint8(1),
		"f":      float32(1.5),
		"i32":    []int32{-1, 2},
		"f32":    []float32{0.5},
		"s":      struct{}{},
		"nilBig": (*big.Int)(nil),
		"nilObj": (*HostObject)(nil),
		"err":    errors.New("plain"),
		"nested": map[string]any{"y": []any{uint(3), nil}},
	})
	vm.Global().SetProp("v", h)
	h.Dispose()
	got := evalDump(vm, "[Object.keys(v).join(), v.a, v.b, v.f, Array.from(new Int32Array(v.i32)), Array.from(new Float32Array(v.f32)), v.s, v.nilBig, v.nilObj, [v.err.name, v.err.message], JSON.stringify(v.nested)]")
	want := `{"ok":{"$":"array","id":0,"v":["a,b,err,f,f32,i32,nested,nilBig,nilObj,s",1,-2,1.5,{"$":"array","id":1,"v":[-1,2]},{"$":"array","id":2,"v":[0.5]},{"$":"undefined"},{"$":"undefined"},null,{"$":"array","id":3,"v":["Error","plain"]},"{\"y\":[3,null]}"]}}`
	expectVector(t, "goTypes", json.RawMessage(want), got)
}

func TestDumpGoValues(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	h := mustEval(t, vm, "(() => { const a = [1]; const o = { a, n: -0, big: -(2n ** 62n), wide: 2n ** 70n, u: undefined, e: new RangeError('r') }; a.push(o); return o })()")
	defer h.Dispose()
	d, err := vm.Dump(h)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := d.(*HostObject)
	if !ok {
		t.Fatalf("dump returned %T", d)
	}
	if got := strings.Join(o.Keys(), ","); got != "a,n,big,wide,u,e" {
		t.Errorf("keys %s", got)
	}
	// Like TS (BigInt64 read), dump keeps the low 64 bits as a signed value.
	if wide, _ := o.Get("wide"); wide.(*big.Int).Sign() != 0 {
		t.Errorf("wide = %v", wide)
	}
	a, _ := o.Get("a")
	arr := a.([]any)
	if arr[1] != any(o) {
		t.Error("cycle back to the object is not the same *HostObject")
	}
	n, _ := o.Get("n")
	if f := n.(float64); f != 0 || !math.Signbit(f) {
		t.Errorf("n = %v", n)
	}
	b, _ := o.Get("big")
	if b.(*big.Int).String() != "-4611686018427387904" {
		t.Errorf("big = %v", b)
	}
	if u, _ := o.Get("u"); u != (JSUndefined{}) {
		t.Errorf("u = %#v", u)
	}
	e, _ := o.Get("e")
	if he := e.(*HostError); he.Name() != "RangeError" || he.Message() != "r" || he.Stack() == nil {
		t.Errorf("e = %#v", e)
	}
}

func TestDeferredSettledAndResolvePromise(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	d := vm.NewPromise()
	settled := d.Settled()
	if d.Settled() != settled {
		t.Error("Settled() returned a different channel")
	}
	result := vm.ResolvePromise(d.Handle)
	isClosed := func() bool {
		select {
		case <-settled:
			return true
		default:
			return false
		}
	}
	d.Resolve(vm.NewString("done"))
	if isClosed() {
		t.Error("settled before jobs ran")
	}
	select {
	case <-result:
		t.Fatal("ResolvePromise delivered before jobs ran")
	default:
	}
	if _, err := vm.ExecutePendingJobs(); err != nil {
		t.Fatal(err)
	}
	if !isClosed() {
		t.Error("not settled after jobs ran")
	}
	r := settledNow(t, result)
	if r.Error != nil || r.Value.ToString() != "done" {
		t.Errorf("result %+v", r)
	}
	r.Value.Dispose()
	d.Handle.Dispose()
}

func TestJSExceptionHandle(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	_, err := vm.EvalCode("throw Object.assign(new Error('with code'), { code: 42 })", "thrower.js", 0)
	var jsErr *JSException
	if !errors.As(err, &jsErr) {
		t.Fatalf("got %T", err)
	}
	code := jsErr.Handle.GetProp("code")
	if code.ToNumber() != 42 {
		t.Errorf("code = %v", code.ToNumber())
	}
	code.Dispose()
	if jsErr.Stack() == nil || !strings.Contains(*jsErr.Stack(), "thrower.js") {
		t.Errorf("stack = %v", jsErr.Stack())
	}
	jsErr.Dispose()
	if !jsErr.Handle.Disposed() {
		t.Error("handle not disposed")
	}
}

func TestSnapshotRestoreGrownMemoryAndOptions(t *testing.T) {
	vm := newVM(t, nil)
	mustEval(t, vm, "globalThis.big = 'x'.repeat(8 * 1024 * 1024); globalThis.n = 1").Dispose()
	snap, err := vm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vm.Dispose()
	if len(snap.Memory) <= 8*1024*1024 {
		t.Fatalf("memory %d bytes, expected growth", len(snap.Memory))
	}
	data := QuickJSSerializeSnapshot(snap)
	restoredSnap, err := QuickJSDeserializeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	interrupts := 0
	vm2, err := QuickJSRestore(context.Background(), restoredSnap, &QuickJSOptions{
		InterruptHandler: func() bool { interrupts++; return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm2.Dispose()
	if got := evalDump(vm2, "[big.length, n]"); compact(normalize(t, got)) != `{"ok":{"$":"array","id":0,"v":[8388608,1]}}` {
		t.Errorf("restored state %s", compact(got))
	}
	_, err = vm2.EvalCode("for (;;) {}", "<eval>", 0)
	var jsErr *JSException
	if !errors.As(err, &jsErr) || jsErr.Message() != "interrupted" || interrupts == 0 {
		t.Errorf("restored VM ignored the interrupt handler: %v", err)
	}
}

func TestWasiShimBuiltins(t *testing.T) {
	vm := newVM(t, nil)
	defer vm.Dispose()
	var stdout, stderr bytes.Buffer
	prevOut, prevErr := wasiStdout, wasiStderr
	wasiStdout, wasiStderr = &stdout, &stderr
	defer func() { wasiStdout, wasiStderr = prevOut, prevErr }()

	base := uint32(vm.call(exWasmMalloc, 64))
	if base == 0 {
		t.Fatal("malloc failed")
	}
	defer vm.call(exWasmFree, uint64(base))
	textPtr, iovs, nwritten := base, base+16, base+40
	vm.writeBytes(textPtr, []byte("hi \xff!\xef\xbb\xbfz"))
	vm.writeU32(iovs, textPtr)     // iov[0] "hi \xff"
	vm.writeU32(iovs+4, 4)         //
	vm.writeU32(iovs+8, textPtr+4) // iov[1] "!\uFEFFz"
	vm.writeU32(iovs+12, 5)
	shim := vm.wasi
	if errno := shim.FdWrite(1, iovs, 2, nwritten); errno != 0 {
		t.Fatalf("fd_write errno %d", errno)
	}
	if got := stdout.String(); got != "hi \uFFFD!\uFEFFz" {
		t.Errorf("stdout %q", got)
	}
	if got := vm.readU32(nwritten); got != 9 {
		t.Errorf("nwritten %d", got)
	}
	// Each chunk is decoded separately: a leading BOM in a chunk is stripped.
	vm.writeU32(iovs, textPtr+5)
	vm.writeU32(iovs+4, 4)
	if errno := shim.FdWrite(2, iovs, 1, nwritten); errno != 0 || stderr.String() != "z" {
		t.Errorf("fd_write(2) errno %d stderr %q", errno, stderr.String())
	}
	if errno := shim.FdWrite(3, iovs, 1, nwritten); errno != 8 {
		t.Errorf("fd_write(3) errno %d", errno)
	}

	vm.writeBytes(base, bytes.Repeat([]byte{0xAA}, 24))
	if errno := shim.FdFdstatGet(1, base); errno != 0 {
		t.Errorf("fdstat errno %d", errno)
	}
	stat := vm.readBytes(base, 24)
	wantStat := append([]byte{2, 0xAA, 0, 0}, bytes.Repeat([]byte{0xAA}, 4)...)
	wantStat = append(wantStat, make([]byte, 16)...)
	if !bytes.Equal(stat, wantStat) {
		t.Errorf("fdstat bytes %x", stat)
	}
	if errno := shim.FdFdstatGet(0, base); errno != 8 {
		t.Errorf("fdstat(0) errno %d", errno)
	}
	if shim.FdClose(1) != 52 || shim.FdSeek(1, 0, 0, base) != 52 {
		t.Error("fd_close/fd_seek should be ENOSYS")
	}
	if errno := shim.ClockTimeGet(2, 0, base); errno != 52 {
		t.Errorf("clock 2 errno %d", errno)
	}
	before := uint64(time.Now().UnixMilli()) * 1e6
	if errno := shim.ClockTimeGet(1, 0, base); errno != 0 {
		t.Errorf("clock errno %d", errno)
	}
	if ns, _ := vm.memory.ReadUint64Le(base); ns < before || ns%1e6 != 0 {
		t.Errorf("clock ns %d (before %d)", ns, before)
	}
	vm.writeBytes(base, make([]byte, 32))
	if errno := shim.RandomGet(base, 32); errno != 0 || bytes.Equal(vm.readBytes(base, 32), make([]byte, 32)) {
		t.Errorf("random_get errno %d", errno)
	}
	func() {
		defer func() {
			var hostErr *HostError
			if err, _ := recover().(error); !errors.As(err, &hostErr) || hostErr.Name() != "RangeError" {
				t.Errorf("out-of-bounds random_get: %v", err)
			}
		}()
		shim.RandomGet(vm.memory.Size()-4, 8)
	}()
	func() {
		defer func() {
			var hostErr *HostError
			if err, _ := recover().(error); !errors.As(err, &hostErr) || hostErr.Name() != "QuotaExceededError" ||
				hostErr.Message() != "The requested length exceeds 65,536 bytes" {
				t.Errorf("oversized random_get: %v", err)
			}
		}()
		shim.RandomGet(0, 65537) // in bounds; only the quota fails, nothing is written
	}()
	randBuf := uint32(vm.call(exWasmMalloc, 65536))
	if randBuf == 0 {
		t.Fatal("malloc failed")
	}
	defer vm.call(exWasmFree, uint64(randBuf))
	if errno := shim.RandomGet(randBuf, 65536); errno != 0 {
		t.Errorf("random_get(65536) errno %d", errno)
	}
}

func TestWasiOverrideFailureAbortsCall(t *testing.T) {
	broken := false
	vm := newVM(t, &QuickJSOptions{Wasi: func(memory *Memory) *WasiImports {
		return &WasiImports{ClockTimeGet: func(_ uint32, _ uint64, resultPtr uint32) uint32 {
			if broken {
				panic(NewHostError("TypeError", "clock unavailable", nil))
			}
			memory.API().WriteUint64Le(resultPtr, 0)
			return 0
		}}
	}})
	defer vm.Dispose()
	if got := mustEval(t, vm, "Date.now()").ToNumber(); got != 0 {
		t.Errorf("Date.now() = %v", got)
	}
	broken = true
	_, err := vm.EvalCode("Date.now()", "<eval>", 0)
	var hostErr *HostError
	if !errors.As(err, &hostErr) || hostErr.Message() != "clock unavailable" || vm.Err() != hostErr {
		t.Fatalf("got %T %v", err, err)
	}
}

// TestCodemodeWorkerFlow drives the VM the way pi's codemode worker does
// (packages/codemode/src/runtime/worker.ts): a prelude returns an api object,
// tool calls go out through a bridge host function and are settled later.
func TestCodemodeWorkerFlow(t *testing.T) {
	const prelude = `(function (bridge, tools, globals, store) {
	const pending = new Map();
	let nextId = 1;
	let finished = false;
	return {
		settle(id, ok, payload) {
			const p = pending.get(id);
			pending.delete(id);
			if (ok) p.resolve(payload === undefined ? undefined : JSON.parse(payload));
			else p.reject(new Error(payload));
		},
		run(fn) {
			finished = false;
			const t = {};
			for (const name of JSON.parse(tools)) {
				t[name] = (args) => new Promise((resolve, reject) => {
					const id = nextId++;
					pending.set(id, { resolve, reject });
					bridge("call", id, name, JSON.stringify(args));
				});
			}
			const console = { log: (...a) => bridge("output", "text", a.join(" ")) };
			fn(t, console).then(
				(v) => { finished = true; bridge("done", true, v === undefined ? undefined : JSON.stringify(v), store) },
				(e) => { finished = true; bridge("done", false, String(e)) },
			);
		},
		stalled() {
			if (!finished && pending.size === 0) { finished = true; bridge("done", false, "stalled") }
		},
	};
})`
	var interrupt atomic.Int32
	var discarded atomic.Int32
	vm := newVM(t, &QuickJSOptions{
		MemoryLimit:      intPtr(64 * 1024 * 1024),
		MaxStackSize:     intPtr(MaxStackSize),
		InterruptHandler: func() bool { return interrupt.Load() != 0 },
		Wasi: func(memory *Memory) *WasiImports {
			return &WasiImports{FdWrite: func(_, iovsPtr, iovsLen, nwrittenPtr uint32) uint32 {
				var written uint32
				for i := range iovsLen {
					n, _ := memory.API().ReadUint32Le(iovsPtr + i*8 + 4)
					written += n
				}
				memory.API().WriteUint32Le(nwrittenPtr, written)
				discarded.Add(1)
				return 0
			}}
		},
	})
	defer vm.Dispose()
	var messages []string
	bridge, err := vm.NewFunction("bridge", func(_ *JSValueHandle, args ...*JSValueHandle) (*JSValueHandle, error) {
		parts := []string{}
		for _, a := range args {
			if a.IsUndefined() {
				parts = append(parts, "<undefined>")
			} else {
				parts = append(parts, a.ToString())
			}
		}
		messages = append(messages, strings.Join(parts, "|"))
		return vm.Undefined(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var api *JSValueHandle
	err = vm.WithScope(func(scope *HandleScope) error {
		preludeFn, err := vm.EvalCode(prelude, "codemode-prelude.js", 0)
		if err != nil {
			return err
		}
		result, err := vm.CallFunction(preludeFn, vm.Undefined(), bridge,
			vm.NewString(`["add"]`), vm.NewString(`{}`), vm.NewString(`{"k":1}`))
		if err != nil {
			return err
		}
		api = scope.Escape(result)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	settle, run, stalled := api.GetProp("settle"), api.GetProp("run"), api.GetProp("stalled")
	drain := func() {
		t.Helper()
		if _, err := vm.ExecutePendingJobs(); err != nil {
			t.Fatal(err)
		}
		h, err := vm.CallFunction(stalled, api)
		if err != nil {
			t.Fatal(err)
		}
		h.Dispose()
	}
	start := func(code string) {
		t.Helper()
		fn, err := vm.EvalCode("(async (tools, console) => {"+code+"\n})", "codemode.js", 0)
		if err != nil {
			t.Fatal(err)
		}
		h, err := vm.CallFunction(run, api, fn)
		if err != nil {
			t.Fatal(err)
		}
		h.Dispose()
		fn.Dispose()
		drain()
	}

	start("const r = await tools.add({ a: 1, b: 2 }); console.log('sum', r); return r * 2")
	if want := []string{`call|1|add|{"a":1,"b":2}`}; fmt.Sprint(messages) != fmt.Sprint(want) {
		t.Fatalf("messages %q", messages)
	}
	if err := vm.WithScope(func(*HandleScope) error {
		h, err := vm.CallFunction(settle, api, vm.NewNumber(1), vm.True(), vm.NewString("3"))
		if err == nil {
			h.Dispose()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	want := []string{`call|1|add|{"a":1,"b":2}`, "output|text|sum 3", `done|true|6|{"k":1}`}
	if fmt.Sprint(messages) != fmt.Sprint(want) {
		t.Errorf("messages %q", messages)
	}

	// A script that never calls out stalls; one that throws reports the error.
	messages = nil
	start("await new Promise(() => {})")
	start("throw new TypeError('bad tool')")
	if want := []string{"done|false|stalled", "done|false|TypeError: bad tool"}; fmt.Sprint(messages) != fmt.Sprint(want) {
		t.Errorf("messages %q", messages)
	}

	// Syntax errors surface as JSException at evalCode time.
	_, err = vm.EvalCode("(async (tools, console) => {let let = 1\n})", "codemode.js", 0)
	var jsErr *JSException
	if !errors.As(err, &jsErr) || jsErr.Name() != "SyntaxError" || jsErr.Stack() == nil || !strings.Contains(*jsErr.Stack(), "codemode.js") {
		t.Errorf("syntax error: %v", err)
	}

	// The interrupt flag stops a runaway script. The interrupt error is
	// uncatchable, so it escapes the async function and the run() call (the
	// worker reports it as a crash).
	messages = nil
	fn := mustEval(t, vm, "(async () => { for (;;) {} })")
	interrupt.Store(1)
	_, err = vm.CallFunction(run, api, fn)
	interrupt.Store(0)
	fn.Dispose()
	if !errors.As(err, &jsErr) || jsErr.Name() != "InternalError" || jsErr.Message() != "interrupted" {
		t.Errorf("interrupt: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("messages %q", messages)
	}

	// Deep recursion is a catchable RangeError thanks to MaxStackSize.
	messages = nil
	start("const f = n => f(n + 1) + 1; f(0)")
	if want := []string{"done|false|RangeError: Maximum call stack size exceeded"}; fmt.Sprint(messages) != fmt.Sprint(want) {
		t.Errorf("messages %q", messages)
	}
	if vm.Err() != nil {
		t.Errorf("VM failed: %v", vm.Err())
	}
	if discarded.Load() != 0 {
		t.Errorf("unexpected engine output (%d writes)", discarded.Load())
	}
}
