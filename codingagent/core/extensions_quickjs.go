package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/quickjs"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

// eqjsConsoleMu guards the optional test console hook.
var (
	eqjsConsoleMu   sync.RWMutex
	eqjsConsoleSink func(level, extPath, message string)
)

// eqjsBundleExtension bundles a TS/JS/MJS/CJS extension entry point in-memory using esbuild.
func eqjsBundleExtension(resolvedPath string) (string, []string, error) {
	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		absPath = resolvedPath
	}
	res := api.Build(api.BuildOptions{
		EntryPoints:       []string{absPath},
		Bundle:            true,
		Format:            api.FormatIIFE,
		GlobalName:        "__piExtModule",
		Platform:          api.PlatformNeutral,
		Target:            api.ES2022,
		Write:             false,
		AbsWorkingDir:     filepath.Dir(absPath),
		MainFields:        []string{"module", "main"},
		ResolveExtensions: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".json"},
		Loader: map[string]api.Loader{
			".ts":   api.LoaderTS,
			".tsx":  api.LoaderTSX,
			".mts":  api.LoaderTS,
			".cts":  api.LoaderTS,
			".js":   api.LoaderJS,
			".jsx":  api.LoaderJSX,
			".mjs":  api.LoaderJS,
			".cjs":  api.LoaderJS,
			".json": api.LoaderJSON,
		},
		Define: map[string]string{
			"import.meta.url": "__piExtMetaUrl",
		},
		Plugins: []api.Plugin{eqjmEsbuildPlugin()},
	})

	warnings := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		warnings = append(warnings, eqjsFormatEsbuildMsg(w))
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			msgs = append(msgs, eqjsFormatEsbuildMsg(e))
		}
		return "", warnings, fmt.Errorf("esbuild bundle failed for %s: %s", resolvedPath, strings.Join(msgs, "; "))
	}
	if len(res.OutputFiles) == 0 {
		return "", warnings, fmt.Errorf("esbuild produced no output for %s", resolvedPath)
	}
	return string(res.OutputFiles[0].Contents), warnings, nil
}

func eqjsFormatEsbuildMsg(msg api.Message) string {
	if msg.Location != nil && msg.Location.File != "" {
		return fmt.Sprintf("%s:%d:%d: %s", msg.Location.File, msg.Location.Line, msg.Location.Column, msg.Text)
	}
	return msg.Text
}

type eqjsCtxEntry struct {
	ectx     ExtensionContext
	trustCtx *ProjectTrustContext
	callCtx  context.Context
}

type eqjsCallResult struct {
	val   any
	event map[string]any
	err   error
}

// eqjsVM manages one isolated QuickJS VM on a single dedicated actor goroutine.
type eqjsVM struct {
	vm       *quickjs.QuickJS
	vmCtx    context.Context
	vmCancel context.CancelFunc

	cwd     string
	extPath string
	extDir  string
	runtime *ExtensionRuntime
	ext     *Extension
	api     ExtensionAPI

	reqCh      chan func()
	postCh     chan func()
	closedCh   chan struct{}
	loopDoneCh chan struct{}
	closeOnce  sync.Once

	retainedMu sync.Mutex
	retained   []*quickjs.JSValueHandle

	timersMu sync.Mutex
	timers   map[int64]*time.Timer

	envMu        sync.RWMutex
	envOverrides map[string]*string

	ctxMu      sync.RWMutex
	ctxEntries map[int]*eqjsCtxEntry
	nextCtxID  int

	waiterMu   sync.Mutex
	waiters    map[int]chan eqjsCallResult
	nextWaiter int

	abortMu    sync.Mutex
	fetchAbort map[int64]context.CancelFunc
	execAbort  map[int64]context.CancelFunc

	nextCallbackID atomic.Int64
	nextSignalID   atomic.Int64
	eventUnsubMu   sync.Mutex
	eventUnsubs    []func()
}

func eqjsExecuteBundledExtension(
	ctx context.Context,
	ext *Extension,
	extAPI ExtensionAPI,
	runtime *ExtensionRuntime,
	cwd string,
	bundledCode string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	evm, err := eqjsNewVM(ext, extAPI, runtime, cwd)
	if err != nil {
		return err
	}
	// Register VM cleanup with runtime so runner.Shutdown tears down the VM.
	erRegisterRuntimeCleanup(runtime, evm.Close)

	if err := evm.loadAndRun(ctx, bundledCode); err != nil {
		evm.Close()
		return err
	}
	return nil
}

func eqjsNewVM(ext *Extension, extAPI ExtensionAPI, runtime *ExtensionRuntime, cwd string) (*eqjsVM, error) {
	vmCtx, vmCancel := context.WithCancel(context.Background())
	qvm, err := quickjs.QuickJSCreate(vmCtx, &quickjs.QuickJSOptions{})
	if err != nil {
		vmCancel()
		return nil, fmt.Errorf("create QuickJS VM: %w", err)
	}
	resolved := ext.ResolvedPath
	if resolved == "" {
		resolved = ext.Path
	}
	absResolved, err := filepath.Abs(resolved)
	if err == nil {
		resolved = absResolved
	}
	if cwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			cwd = wd
		}
	}
	evm := &eqjsVM{
		vm:           qvm,
		vmCtx:        vmCtx,
		vmCancel:     vmCancel,
		cwd:          cwd,
		extPath:      resolved,
		extDir:       filepath.Dir(resolved),
		runtime:      runtime,
		ext:          ext,
		api:          extAPI,
		reqCh:        make(chan func()),
		postCh:       make(chan func(), 256),
		closedCh:     make(chan struct{}),
		loopDoneCh:   make(chan struct{}),
		timers:       make(map[int64]*time.Timer),
		envOverrides: make(map[string]*string),
		ctxEntries:   make(map[int]*eqjsCtxEntry),
		nextCtxID:    1,
		waiters:      make(map[int]chan eqjsCallResult),
		nextWaiter:   1,
		fetchAbort:   make(map[int64]context.CancelFunc),
		execAbort:    make(map[int64]context.CancelFunc),
	}

	initErr := make(chan error, 1)
	go evm.loop(initErr)
	if err := <-initErr; err != nil {
		evm.Close()
		return nil, err
	}
	return evm, nil
}

func (e *eqjsVM) loop(initErr chan<- error) {
	defer func() {
		// Notify any still-waiting callers.
		e.waiterMu.Lock()
		for id, ch := range e.waiters {
			select {
			case ch <- eqjsCallResult{err: errors.New("extension VM closed")}:
			default:
			}
			delete(e.waiters, id)
		}
		e.waiterMu.Unlock()

		e.retainedMu.Lock()
		handles := e.retained
		e.retained = nil
		e.retainedMu.Unlock()
		for _, h := range handles {
			if h != nil {
				h.Dispose()
			}
		}

		e.vm.Dispose()
		e.vmCancel()
		close(e.loopDoneCh)
	}()

	if err := e.initRuntimeOnLoop(); err != nil {
		initErr <- err
		return
	}
	initErr <- nil

	for {
		// Prioritize pending async callbacks cleanly.
		select {
		case <-e.closedCh:
			return
		case fn := <-e.postCh:
			fn()
			continue
		default:
		}
		select {
		case <-e.closedCh:
			return
		case fn := <-e.postCh:
			fn()
		case fn := <-e.reqCh:
			fn()
		}
	}
}

// pumpUntil drains reqCh and postCh on the loop goroutine until done closes.
// MUST ONLY be called from inside a HostFunction executing on e.loop().
func (e *eqjsVM) pumpUntil(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-e.closedCh:
			return
		case fn := <-e.postCh:
			fn()
		case fn := <-e.reqCh:
			fn()
		}
	}
}

// runOffLoopAndPump runs fn on a helper goroutine while pumping reqCh/postCh on e.loop().
// MUST ONLY be called from inside a HostFunction executing on e.loop().
func (e *eqjsVM) runOffLoopAndPump(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	e.pumpUntil(done)
}

func (e *eqjsVM) Close() {
	e.closeOnce.Do(func() {
		e.timersMu.Lock()
		for id, t := range e.timers {
			t.Stop()
			delete(e.timers, id)
		}
		e.timersMu.Unlock()

		e.abortMu.Lock()
		for id, cancel := range e.fetchAbort {
			cancel()
			delete(e.fetchAbort, id)
		}
		for id, cancel := range e.execAbort {
			cancel()
			delete(e.execAbort, id)
		}
		e.abortMu.Unlock()

		e.eventUnsubMu.Lock()
		unsubs := e.eventUnsubs
		e.eventUnsubs = nil
		e.eventUnsubMu.Unlock()
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}

		close(e.closedCh)
		<-e.loopDoneCh
	})
}

func (e *eqjsVM) isClosed() bool {
	select {
	case <-e.closedCh:
		return true
	default:
		return false
	}
}

func (e *eqjsVM) runOnLoop(ctx context.Context, fn func() error) error {
	if e.isClosed() {
		return errors.New("extension VM closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan error, 1)
	task := func() {
		done <- fn()
	}
	select {
	case e.reqCh <- task:
	case <-e.closedCh:
		return errors.New("extension VM closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-e.closedCh:
		return errors.New("extension VM closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *eqjsVM) postToLoop(fn func()) {
	if e.isClosed() || fn == nil {
		return
	}
	select {
	case e.postCh <- fn:
	case <-e.closedCh:
	default:
		go func() {
			select {
			case e.postCh <- fn:
			case <-e.closedCh:
			}
		}()
	}
}

func (e *eqjsVM) retain(h *quickjs.JSValueHandle) *quickjs.JSValueHandle {
	if h == nil {
		return nil
	}
	dup := h.Dup()
	e.retainedMu.Lock()
	e.retained = append(e.retained, dup)
	e.retainedMu.Unlock()
	return dup
}

func (e *eqjsVM) loadAndRun(ctx context.Context, bundledCode string) error {
	metaURL := eqjsPathToFileURL(e.extPath)
	setupCode := fmt.Sprintf("globalThis.__piExtMetaUrl = %q;\n%s", metaURL, bundledCode)
	err := e.runOnLoop(ctx, func() error {
		res, evalErr := e.vm.EvalCode(setupCode, e.extPath, 0)
		if res != nil {
			res.Dispose()
		}
		_, _ = e.vm.ExecutePendingJobs()
		if evalErr != nil {
			return eqjsFormatJSError(e.vm, evalErr)
		}
		return nil
	})
	if err != nil {
		return err
	}

	_, err = e.callGlobalAwait(ctx, "__piRunExtension", func(signalID int64) []*quickjs.JSValueHandle {
		createFn := e.vm.GetGlobal().GetProp("__piCreateAPI")
		defer createFn.Dispose()
		apiVal, callErr := e.vm.CallFunction(createFn, e.vm.Undefined())
		if callErr != nil {
			if apiVal != nil {
				apiVal.Dispose()
			}
			return []*quickjs.JSValueHandle{e.vm.Undefined()}
		}
		return []*quickjs.JSValueHandle{apiVal}
	})
	return err
}

func (e *eqjsVM) callGlobalAwait(
	ctx context.Context,
	globalFnName string,
	makeArgs func(signalID int64) []*quickjs.JSValueHandle,
) (eqjsCallResult, error) {
	return e.callAwaitInternal(ctx, globalFnName, nil, nil, false, makeArgs)
}

func (e *eqjsVM) callFnAwait(
	ctx context.Context,
	label string,
	fnHandle *quickjs.JSValueHandle,
	thisVal *quickjs.JSValueHandle,
	makeArgs func(signalID int64) []*quickjs.JSValueHandle,
) (eqjsCallResult, error) {
	return e.callAwaitInternal(ctx, label, fnHandle, thisVal, false, makeArgs)
}

func (e *eqjsVM) callEventAwait(
	ctx context.Context,
	label string,
	fnHandle *quickjs.JSValueHandle,
	makeArgs func(signalID int64) []*quickjs.JSValueHandle,
) (eqjsCallResult, error) {
	return e.callAwaitInternal(ctx, label, fnHandle, nil, true, makeArgs)
}

func (e *eqjsVM) callAwaitInternal(
	ctx context.Context,
	label string,
	fnHandle *quickjs.JSValueHandle,
	thisVal *quickjs.JSValueHandle,
	isEvent bool,
	makeArgs func(signalID int64) []*quickjs.JSValueHandle,
) (eqjsCallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return eqjsCallResult{}, err
	}
	if e.isClosed() {
		return eqjsCallResult{}, errors.New("extension VM closed")
	}

	var signalID int64
	var stopSignalWatch func()
	if ctx.Done() != nil {
		signalID = e.nextSignalID.Add(1)
		stopWatchCh := make(chan struct{})
		stopSignalWatch = func() {
			close(stopWatchCh)
			e.postToLoop(func() {
				dropFn := e.vm.GetGlobal().GetProp("__piDropSignal")
				defer dropFn.Dispose()
				if dropFn.IsFunction() {
					idH := e.vm.NewNumber(float64(signalID))
					res, _ := e.vm.CallFunction(dropFn, e.vm.Undefined(), idH)
					idH.Dispose()
					if res != nil {
						res.Dispose()
					}
				}
			})
		}
		go func() {
			select {
			case <-ctx.Done():
				reason := "Aborted"
				if ctx.Err() != nil {
					reason = ctx.Err().Error()
				}
				e.postToLoop(func() {
					abortFn := e.vm.GetGlobal().GetProp("__piAbortSignal")
					defer abortFn.Dispose()
					if abortFn.IsFunction() {
						idH := e.vm.NewNumber(float64(signalID))
						rsnH := e.vm.NewString(reason)
						res, _ := e.vm.CallFunction(abortFn, e.vm.Undefined(), idH, rsnH)
						idH.Dispose()
						rsnH.Dispose()
						if res != nil {
							res.Dispose()
						}
						_, _ = e.vm.ExecutePendingJobs()
					}
				})
			case <-stopWatchCh:
			case <-e.closedCh:
			}
		}()
		defer stopSignalWatch()
	}

	replyCh := make(chan eqjsCallResult, 1)
	e.waiterMu.Lock()
	waiterID := e.nextWaiter
	e.nextWaiter++
	e.waiters[waiterID] = replyCh
	e.waiterMu.Unlock()

	cleanupWaiter := func() {
		e.waiterMu.Lock()
		delete(e.waiters, waiterID)
		e.waiterMu.Unlock()
	}

	enqueueErr := e.runOnLoop(ctx, func() error {
		target := fnHandle
		var ownedTarget *quickjs.JSValueHandle
		if target == nil {
			ownedTarget = e.vm.GetGlobal().GetProp(label)
			target = ownedTarget
		}
		if ownedTarget != nil {
			defer ownedTarget.Dispose()
		}
		if target == nil || !target.IsFunction() {
			return fmt.Errorf("JS target %q is not a function", label)
		}

		var rawArgs []*quickjs.JSValueHandle
		if makeArgs != nil {
			rawArgs = makeArgs(signalID)
		}
		arrHandle := e.vm.NewArray()
		for i, a := range rawArgs {
			if a == nil {
				a = e.vm.Undefined()
			}
			elemKey := e.vm.NewString(strconv.Itoa(i))
			e.vm.SetProp(arrHandle, elemKey, a)
			elemKey.Dispose()
			a.Dispose()
		}
		defer arrHandle.Dispose()

		cbID := e.nextCallbackID.Add(1)
		resName := fmt.Sprintf("__piRes_%d", cbID)
		rejName := fmt.Sprintf("__piRej_%d", cbID)
		var settled atomic.Bool

		finish := func(out eqjsCallResult) {
			if !settled.CompareAndSwap(false, true) {
				return
			}
			e.vm.UnregisterHostCallback(resName)
			e.vm.UnregisterHostCallback(rejName)
			e.waiterMu.Lock()
			ch, ok := e.waiters[waiterID]
			delete(e.waiters, waiterID)
			e.waiterMu.Unlock()
			if ok {
				select {
				case ch <- out:
				default:
				}
			}
		}

		resCb, err := e.vm.NewFunction(resName, func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			var val any
			var dumpedEv map[string]any
			if len(args) > 0 && args[0] != nil && !args[0].IsUndefined() {
				if isEvent {
					evProp := args[0].GetProp("event")
					if evProp != nil && !evProp.IsUndefined() {
						if d, derr := eqjsDumpPlain(e.vm, evProp); derr == nil {
							if m, ok := d.(map[string]any); ok {
								dumpedEv = m
							}
						}
					}
					if evProp != nil {
						evProp.Dispose()
					}
					resProp := args[0].GetProp("res")
					if resProp != nil && !resProp.IsUndefined() {
						val, _ = eqjsDumpPlain(e.vm, resProp)
					}
					if resProp != nil {
						resProp.Dispose()
					}
				} else {
					val, _ = eqjsDumpPlain(e.vm, args[0])
				}
			}
			finish(eqjsCallResult{val: val, event: dumpedEv})
			return e.vm.Undefined(), nil
		})
		if err != nil {
			return err
		}
		defer resCb.Dispose()

		rejCb, err := e.vm.NewFunction(rejName, func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			var jsErr error
			if len(args) > 0 && args[0] != nil {
				jsErr = eqjsExtractHandleError(e.vm, args[0])
			} else {
				jsErr = errors.New("JS promise rejected")
			}
			finish(eqjsCallResult{err: jsErr})
			return e.vm.Undefined(), nil
		})
		if err != nil {
			e.vm.UnregisterHostCallback(resName)
			return err
		}
		defer rejCb.Dispose()

		thisArg := thisVal
		if thisArg == nil {
			thisArg = e.vm.Undefined()
		}

		if isEvent {
			awaitEv := e.vm.GetGlobal().GetProp("__piAwaitEvent")
			defer awaitEv.Dispose()
			var evArg, ctxArg *quickjs.JSValueHandle
			// Extract back from arrHandle elements 0 and 1.
			k0 := e.vm.NewString("0")
			k1 := e.vm.NewString("1")
			evArg = e.vm.GetProp(arrHandle, k0)
			ctxArg = e.vm.GetProp(arrHandle, k1)
			k0.Dispose()
			k1.Dispose()
			callRes, callErr := e.vm.CallFunction(awaitEv, e.vm.Undefined(), target, evArg, ctxArg, resCb, rejCb)
			evArg.Dispose()
			ctxArg.Dispose()
			if callRes != nil {
				callRes.Dispose()
			}
			_, _ = e.vm.ExecutePendingJobs()
			if callErr != nil {
				finish(eqjsCallResult{err: eqjsFormatJSError(e.vm, callErr)})
			}
		} else {
			awaitFn := e.vm.GetGlobal().GetProp("__piAwait")
			defer awaitFn.Dispose()
			callRes, callErr := e.vm.CallFunction(awaitFn, e.vm.Undefined(), target, thisArg, arrHandle, resCb, rejCb)
			if callRes != nil {
				callRes.Dispose()
			}
			_, _ = e.vm.ExecutePendingJobs()
			if callErr != nil {
				finish(eqjsCallResult{err: eqjsFormatJSError(e.vm, callErr)})
			}
		}
		return nil
	})
	if enqueueErr != nil {
		cleanupWaiter()
		return eqjsCallResult{}, enqueueErr
	}

	select {
	case out := <-replyCh:
		return out, out.err
	case <-e.closedCh:
		cleanupWaiter()
		return eqjsCallResult{}, errors.New("extension VM closed")
	case <-ctx.Done():
		cleanupWaiter()
		return eqjsCallResult{}, ctx.Err()
	}
}

func (e *eqjsVM) callSyncOnLoop(ctx context.Context, fnHandle *quickjs.JSValueHandle, thisVal *quickjs.JSValueHandle, makeArgs func() []*quickjs.JSValueHandle) (any, error) {
	var out any
	err := e.runOnLoop(ctx, func() error {
		if fnHandle == nil || !fnHandle.IsFunction() {
			return nil
		}
		var args []*quickjs.JSValueHandle
		if makeArgs != nil {
			args = makeArgs()
		}
		tArg := thisVal
		if tArg == nil {
			tArg = e.vm.Undefined()
		}
		res, err := e.vm.CallFunction(fnHandle, tArg, args...)
		for _, a := range args {
			if a != nil {
				a.Dispose()
			}
		}
		if res != nil {
			defer res.Dispose()
		}
		_, _ = e.vm.ExecutePendingJobs()
		if err != nil {
			return eqjsFormatJSError(e.vm, err)
		}
		if res != nil && !res.IsUndefined() {
			dumped, derr := eqjsDumpPlain(e.vm, res)
			if derr != nil {
				return derr
			}
			out = dumped
		}
		return nil
	})
	return out, err
}

func (e *eqjsVM) initRuntimeOnLoop() error {
	hostObj := e.vm.NewObject()
	defer hostObj.Dispose()

	e.installCoreHostFuncs(hostObj)
	e.installEnvHostFuncs(hostObj)
	e.installPathOSHostFuncs(hostObj)
	e.installFSHostFuncs(hostObj)
	e.installCryptoURLHostFuncs(hostObj)
	e.installPiModuleHostFuncs(hostObj)
	e.installAPIHostFuncs(hostObj)
	e.installContextUIHostFuncs(hostObj)

	if g := e.vm.GetGlobal(); g != nil {
		g.SetProp("__piHost", hostObj)
		g.Dispose()
	}

	res, err := e.vm.EvalCode(eqjmRuntimePreludeJS, "<pi-prelude>", 0)
	if res != nil {
		res.Dispose()
	}
	if err != nil {
		return eqjsFormatJSError(e.vm, err)
	}

	awaitEvCode := `
globalThis.__piAwaitEvent = function(fn, ev, ctx, resolveCb, rejectCb) {
  try {
    const r = fn(ev, ctx);
    Promise.resolve(r).then(
      (res) => resolveCb({ res, event: ev }),
      (err) => rejectCb(err)
    );
  } catch (err) {
    rejectCb(err);
  }
};
globalThis.__piMakeMap = function(entries) {
  return new Map(entries || []);
};`
	res2, err2 := e.vm.EvalCode(awaitEvCode, "<pi-await-event>", 0)
	if res2 != nil {
		res2.Dispose()
	}
	_, _ = e.vm.ExecutePendingJobs()
	if err2 != nil {
		return eqjsFormatJSError(e.vm, err2)
	}
	return nil
}

func (e *eqjsVM) setHostMethod(obj *quickjs.JSValueHandle, name string, fn quickjs.HostFunction) {
	cbName := fmt.Sprintf("__h_%d_%s", e.nextCallbackID.Add(1), name)
	fnHandle, err := e.vm.NewFunction(cbName, fn)
	if err != nil {
		return
	}
	obj.SetProp(name, fnHandle)
	fnHandle.Dispose()
}

func (e *eqjsVM) newCallbackHandle(fn quickjs.HostFunction) *quickjs.JSValueHandle {
	cbName := fmt.Sprintf("__cb_%d", e.nextCallbackID.Add(1))
	h, err := e.vm.NewFunction(cbName, fn)
	if err != nil {
		return e.vm.Undefined()
	}
	return h
}

func eqjsDumpPlain(vm *quickjs.QuickJS, handle *quickjs.JSValueHandle) (any, error) {
	if vm == nil || handle == nil {
		return nil, nil
	}
	v, err := vm.Dump(handle)
	if err != nil {
		return nil, err
	}
	return eqjsNormalizeDumped(v, make(map[*quickjs.HostObject]map[string]any)), nil
}

func eqjsNormalizeDumped(v any, seen map[*quickjs.HostObject]map[string]any) any {
	switch x := v.(type) {
	case nil, quickjs.JSUndefined, *quickjs.JSUndefined:
		return nil
	case *quickjs.HostObject:
		if x == nil {
			return nil
		}
		if existing, ok := seen[x]; ok {
			return existing
		}
		m := make(map[string]any, len(x.Keys()))
		seen[x] = m
		for _, k := range x.Keys() {
			rawVal, _ := x.Get(k)
			if _, isUndef := rawVal.(quickjs.JSUndefined); isUndef {
				continue
			}
			if _, isUndefPtr := rawVal.(*quickjs.JSUndefined); isUndefPtr {
				continue
			}
			m[k] = eqjsNormalizeDumped(rawVal, seen)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			out[i] = eqjsNormalizeDumped(elem, seen)
		}
		return out
	case quickjs.ArrayBuffer:
		return []byte(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if _, isUndef := val.(quickjs.JSUndefined); isUndef {
				continue
			}
			if _, isUndefPtr := val.(*quickjs.JSUndefined); isUndefPtr {
				continue
			}
			out[k] = eqjsNormalizeDumped(val, seen)
		}
		return out
	default:
		return v
	}
}

func (e *eqjsVM) installCoreHostFuncs(hostObj *quickjs.JSValueHandle) {
	e.setHostMethod(hostObj, "cwd", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(e.cwd), nil
	})
	e.setHostMethod(hostObj, "consoleLog", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		level := "log"
		msg := ""
		if len(args) > 0 && args[0] != nil {
			level = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			msg = args[1].ToString()
		}
		eqjsConsoleMu.RLock()
		sink := eqjsConsoleSink
		eqjsConsoleMu.RUnlock()
		if sink != nil {
			sink(level, e.extPath, msg)
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(hostObj, "timerSchedule", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		token := int64(args[0].ToNumber())
		ms := int64(args[1].ToNumber())
		if ms < 0 {
			ms = 0
		}
		dur := time.Duration(ms) * time.Millisecond
		var timer *time.Timer
		timer = time.AfterFunc(dur, func() {
			e.timersMu.Lock()
			cur, ok := e.timers[token]
			if ok && cur == timer {
				delete(e.timers, token)
			} else {
				ok = false
			}
			e.timersMu.Unlock()
			if !ok {
				return
			}
			e.postToLoop(func() {
				fireFn := e.vm.GetGlobal().GetProp("__piFireTimer")
				defer fireFn.Dispose()
				if fireFn.IsFunction() {
					tokH := e.vm.NewNumber(float64(token))
					r, _ := e.vm.CallFunction(fireFn, e.vm.Undefined(), tokH)
					tokH.Dispose()
					if r != nil {
						r.Dispose()
					}
					_, _ = e.vm.ExecutePendingJobs()
				}
			})
		})
		e.timersMu.Lock()
		if prev, ok := e.timers[token]; ok {
			prev.Stop()
		}
		e.timers[token] = timer
		e.timersMu.Unlock()
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(hostObj, "timerCancel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			token := int64(args[0].ToNumber())
			e.timersMu.Lock()
			if t, ok := e.timers[token]; ok {
				t.Stop()
				delete(e.timers, token)
			}
			e.timersMu.Unlock()
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(hostObj, "utf8Encode", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var s string
		if len(args) > 0 && args[0] != nil {
			s = args[0].ToString()
		}
		return e.vm.NewArrayBuffer([]byte(s)), nil
	})
	e.setHostMethod(hostObj, "utf8Decode", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString(""), nil
		}
		b, err := args[0].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		return e.vm.NewString(string(b)), nil
	})
	e.setHostMethod(hostObj, "bufferFromString", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var str, enc string
		if len(args) > 0 && args[0] != nil {
			str = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			enc = strings.ToLower(strings.TrimSpace(args[1].ToString()))
		}
		var b []byte
		var err error
		switch enc {
		case "", "utf8", "utf-8":
			b = []byte(str)
		case "base64":
			b, err = base64.StdEncoding.DecodeString(str)
		case "base64url":
			b, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(str, "="))
		case "hex":
			b, err = hex.DecodeString(str)
		case "latin1", "binary", "ascii":
			b = make([]byte, len(str))
			for i := 0; i < len(str); i++ {
				b[i] = str[i]
			}
		default:
			b = []byte(str)
		}
		if err != nil {
			return nil, err
		}
		return e.vm.NewArrayBuffer(b), nil
	})
	e.setHostMethod(hostObj, "bufferToString", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString(""), nil
		}
		b, err := args[0].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		enc := "utf8"
		if len(args) > 1 && args[1] != nil {
			enc = strings.ToLower(strings.TrimSpace(args[1].ToString()))
		}
		switch enc {
		case "", "utf8", "utf-8":
			return e.vm.NewString(string(b)), nil
		case "base64":
			return e.vm.NewString(base64.StdEncoding.EncodeToString(b)), nil
		case "base64url":
			return e.vm.NewString(base64.RawURLEncoding.EncodeToString(b)), nil
		case "hex":
			return e.vm.NewString(hex.EncodeToString(b)), nil
		default:
			return e.vm.NewString(string(b)), nil
		}
	})
	e.setHostMethod(hostObj, "fetchAllocAbort", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewNumber(float64(e.nextSignalID.Add(1))), nil
	})
	e.setHostMethod(hostObj, "fetchAbort", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil && args[0].IsNumber() {
			fid := int64(args[0].ToNumber())
			e.abortMu.Lock()
			cancel := e.fetchAbort[fid]
			delete(e.fetchAbort, fid)
			e.abortMu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(hostObj, "fetch", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var reqURL, method string
		method = http.MethodGet
		headersMap := make(map[string]string)
		var bodyBytes []byte
		if len(args) > 0 && args[0] != nil {
			reqURL = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() {
			method = strings.ToUpper(strings.TrimSpace(args[1].ToString()))
			if method == "" {
				method = http.MethodGet
			}
		}
		if len(args) > 2 && args[2] != nil && args[2].IsObject() {
			if dumped, err := eqjsDumpPlain(e.vm, args[2]); err == nil {
				if m, ok := dumped.(map[string]any); ok {
					for k, v := range m {
						if s, ok := v.(string); ok {
							headersMap[k] = s
						}
					}
				}
			}
		}
		if len(args) > 3 && args[3] != nil && !args[3].IsNull() && !args[3].IsUndefined() {
			if b, err := args[3].ToArrayBuffer(); err == nil {
				bodyBytes = b
			}
		}

		deferred := e.vm.NewPromise()
		reqCtx, cancel := context.WithCancel(e.vmCtx)
		fid := e.nextSignalID.Add(1)
		if len(args) > 4 && args[4] != nil && args[4].IsNumber() {
			if customFID := int64(args[4].ToNumber()); customFID > 0 {
				fid = customFID
			}
		}
		e.abortMu.Lock()
		e.fetchAbort[fid] = cancel
		e.abortMu.Unlock()

		go func() {
			defer func() {
				cancel()
				e.abortMu.Lock()
				delete(e.fetchAbort, fid)
				e.abortMu.Unlock()
			}()
			var bodyReader io.Reader
			if len(bodyBytes) > 0 {
				bodyReader = bytes.NewReader(bodyBytes)
			}
			req, reqErr := http.NewRequestWithContext(reqCtx, method, reqURL, bodyReader)
			if reqErr != nil {
				eqjsRejectDeferred(e, deferred, reqErr)
				return
			}
			for k, v := range headersMap {
				req.Header.Set(k, v)
			}
			resp, respErr := ai.NodeHttpProxyDefaultClient().Do(req)
			if respErr != nil {
				eqjsRejectDeferred(e, deferred, respErr)
				return
			}
			defer resp.Body.Close()
			respBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				eqjsRejectDeferred(e, deferred, readErr)
				return
			}
			respHdrs := make(map[string]any, len(resp.Header))
			for k, vals := range resp.Header {
				respHdrs[strings.ToLower(k)] = strings.Join(vals, ", ")
			}
			finalURL := reqURL
			if resp.Request != nil && resp.Request.URL != nil {
				finalURL = resp.Request.URL.String()
			}
			eqjsResolveDeferred(e, deferred, map[string]any{
				"status":     resp.StatusCode,
				"statusText": resp.Status,
				"url":        finalURL,
				"redirected": finalURL != reqURL,
				"headers":    respHdrs,
				"body":       respBody,
			})
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(hostObj, "urlParse", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var raw, base string
		if len(args) > 0 && args[0] != nil {
			raw = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			base = args[1].ToString()
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("TypeError: Invalid URL: %w", err)
		}
		if base != "" {
			bu, berr := url.Parse(base)
			if berr != nil {
				return nil, fmt.Errorf("TypeError: Invalid base URL: %w", berr)
			}
			u = bu.ResolveReference(u)
		}
		if u.Scheme == "" {
			return nil, fmt.Errorf("TypeError: Invalid URL %q", raw)
		}
		proto := u.Scheme + ":"
		var username, password string
		if u.User != nil {
			username = u.User.Username()
			password, _ = u.User.Password()
		}
		search := ""
		if u.RawQuery != "" {
			search = "?" + u.RawQuery
		}
		hash := ""
		if u.Fragment != "" {
			hash = "#" + u.Fragment
		}
		origin := "null"
		if u.Host != "" {
			origin = proto + "//" + u.Host
		}
		return eqjsToJS(e.vm, map[string]any{
			"protocol": proto,
			"username": username,
			"password": password,
			"hostname": u.Hostname(),
			"port":     u.Port(),
			"host":     u.Host,
			"pathname": u.Path,
			"search":   search,
			"hash":     hash,
			"origin":   origin,
			"href":     u.String(),
		}), nil
	})
}

func (e *eqjsVM) installEnvHostFuncs(hostObj *quickjs.JSValueHandle) {
	envObj := e.vm.NewObject()
	defer envObj.Dispose()
	e.setHostMethod(envObj, "get", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		k := args[0].ToString()
		e.envMu.RLock()
		ov, hasOv := e.envOverrides[k]
		e.envMu.RUnlock()
		if hasOv {
			if ov == nil {
				return e.vm.Undefined(), nil
			}
			return e.vm.NewString(*ov), nil
		}
		if v, ok := os.LookupEnv(k); ok {
			return e.vm.NewString(v), nil
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(envObj, "set", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		k := args[0].ToString()
		var ptr *string
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() && !args[1].IsNull() {
			s := args[1].ToString()
			ptr = &s
		}
		e.envMu.Lock()
		e.envOverrides[k] = ptr
		e.envMu.Unlock()
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(envObj, "del", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			k := args[0].ToString()
			e.envMu.Lock()
			e.envOverrides[k] = nil
			e.envMu.Unlock()
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(envObj, "has", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.False(), nil
		}
		k := args[0].ToString()
		e.envMu.RLock()
		ov, hasOv := e.envOverrides[k]
		e.envMu.RUnlock()
		if hasOv {
			if ov != nil {
				return e.vm.True(), nil
			}
			return e.vm.False(), nil
		}
		if _, ok := os.LookupEnv(k); ok {
			return e.vm.True(), nil
		}
		return e.vm.False(), nil
	})
	e.setHostMethod(envObj, "keys", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		seen := make(map[string]struct{})
		for _, kv := range os.Environ() {
			if idx := strings.IndexByte(kv, '='); idx >= 0 {
				seen[kv[:idx]] = struct{}{}
			}
		}
		e.envMu.RLock()
		for k, v := range e.envOverrides {
			if v == nil {
				delete(seen, k)
			} else {
				seen[k] = struct{}{}
			}
		}
		e.envMu.RUnlock()
		keys := make([]any, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		return eqjsToJS(e.vm, keys), nil
	})
	hostObj.SetProp("env", envObj)
}

func (e *eqjsVM) installPathOSHostFuncs(hostObj *quickjs.JSValueHandle) {
	pathObj := e.vm.NewObject()
	defer pathObj.Dispose()
	e.setHostMethod(pathObj, "sep", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(string(filepath.Separator)), nil
	})
	e.setHostMethod(pathObj, "delimiter", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(string(filepath.ListSeparator)), nil
	})
	e.setHostMethod(pathObj, "join", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		parts := eqjsArgStrings(e.vm, args, 0)
		return e.vm.NewString(filepath.Join(parts...)), nil
	})
	e.setHostMethod(pathObj, "resolve", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		parts := eqjsArgStrings(e.vm, args, 0)
		cur := e.cwd
		for _, p := range parts {
			if p == "" {
				continue
			}
			if filepath.IsAbs(p) {
				cur = filepath.Clean(p)
			} else {
				cur = filepath.Join(cur, p)
			}
		}
		if cur == "" {
			if abs, err := filepath.Abs("."); err == nil {
				cur = abs
			}
		}
		return e.vm.NewString(filepath.Clean(cur)), nil
	})
	e.setHostMethod(pathObj, "dirname", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString("."), nil
		}
		return e.vm.NewString(filepath.Dir(args[0].ToString())), nil
	})
	e.setHostMethod(pathObj, "basename", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString(""), nil
		}
		base := filepath.Base(args[0].ToString())
		if len(args) > 1 && args[1] != nil {
			ext := args[1].ToString()
			if ext != "" && strings.HasSuffix(base, ext) {
				base = strings.TrimSuffix(base, ext)
			}
		}
		return e.vm.NewString(base), nil
	})
	e.setHostMethod(pathObj, "extname", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString(""), nil
		}
		return e.vm.NewString(filepath.Ext(args[0].ToString())), nil
	})
	e.setHostMethod(pathObj, "normalize", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.NewString("."), nil
		}
		return e.vm.NewString(filepath.Clean(args[0].ToString())), nil
	})
	e.setHostMethod(pathObj, "isAbsolute", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil && filepath.IsAbs(args[0].ToString()) {
			return e.vm.True(), nil
		}
		return e.vm.False(), nil
	})
	e.setHostMethod(pathObj, "relative", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var from, to string
		if len(args) > 0 && args[0] != nil {
			from = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			to = args[1].ToString()
		}
		rel, err := filepath.Rel(from, to)
		if err != nil {
			return e.vm.NewString(to), nil
		}
		return e.vm.NewString(rel), nil
	})
	hostObj.SetProp("path", pathObj)

	osObj := e.vm.NewObject()
	defer osObj.Dispose()
	e.setHostMethod(osObj, "homedir", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		h, _ := os.UserHomeDir()
		return e.vm.NewString(h), nil
	})
	e.setHostMethod(osObj, "tmpdir", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(os.TempDir()), nil
	})
	e.setHostMethod(osObj, "platform", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		p := runtime.GOOS
		if p == "windows" {
			p = "win32"
		}
		return e.vm.NewString(p), nil
	})
	e.setHostMethod(osObj, "arch", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		a := runtime.GOARCH
		switch a {
		case "amd64":
			a = "x64"
		case "386":
			a = "ia32"
		}
		return e.vm.NewString(a), nil
	})
	e.setHostMethod(osObj, "hostname", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		h, _ := os.Hostname()
		return e.vm.NewString(h), nil
	})
	e.setHostMethod(osObj, "numCPU", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewNumber(float64(runtime.NumCPU())), nil
	})
	e.setHostMethod(osObj, "eol", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if runtime.GOOS == "windows" {
			return e.vm.NewString("\r\n"), nil
		}
		return e.vm.NewString("\n"), nil
	})
	e.setHostMethod(osObj, "type", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		switch runtime.GOOS {
		case "darwin":
			return e.vm.NewString("Darwin"), nil
		case "windows":
			return e.vm.NewString("Windows_NT"), nil
		default:
			return e.vm.NewString("Linux"), nil
		}
	})
	e.setHostMethod(osObj, "release", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(runtime.Version()), nil
	})
	e.setHostMethod(osObj, "username", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		u := os.Getenv("USER")
		if u == "" {
			u = os.Getenv("USERNAME")
		}
		return e.vm.NewString(u), nil
	})
	e.setHostMethod(osObj, "pid", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewNumber(float64(os.Getpid())), nil
	})
	hostObj.SetProp("os", osObj)
}

func (e *eqjsVM) resolveFSPath(raw string) string {
	p := raw
	if strings.HasPrefix(p, "file://") {
		if u, err := url.Parse(p); err == nil && u.Path != "" {
			p = u.Path
		}
	}
	if p == "" {
		return e.cwd
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(e.cwd, p)
}

func (e *eqjsVM) installFSHostFuncs(hostObj *quickjs.JSValueHandle) {
	fsObj := e.vm.NewObject()
	defer fsObj.Dispose()

	e.setHostMethod(fsObj, "existsSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.False(), nil
		}
		_, err := os.Stat(e.resolveFSPath(args[0].ToString()))
		if err == nil {
			return e.vm.True(), nil
		}
		return e.vm.False(), nil
	})
	e.setHostMethod(fsObj, "readFileSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("ENOENT: missing path")
		}
		data, err := os.ReadFile(e.resolveFSPath(args[0].ToString()))
		if err != nil {
			return nil, err
		}
		return e.vm.NewArrayBuffer(data), nil
	})
	e.setHostMethod(fsObj, "writeFileSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("writeFileSync requires path and data")
		}
		b, err := args[1].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(e.resolveFSPath(args[0].ToString()), b, 0o644); err != nil {
			return nil, err
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(fsObj, "appendFileSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("appendFileSync requires path and data")
		}
		b, err := args[1].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(e.resolveFSPath(args[0].ToString()), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		_, werr := f.Write(b)
		cerr := f.Close()
		if werr != nil {
			return nil, werr
		}
		if cerr != nil {
			return nil, cerr
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(fsObj, "mkdirSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("mkdirSync requires path")
		}
		p := e.resolveFSPath(args[0].ToString())
		rec := len(args) > 1 && args[1] != nil && args[1].ToBoolean()
		var err error
		if rec {
			err = os.MkdirAll(p, 0o755)
		} else {
			err = os.Mkdir(p, 0o755)
		}
		if err != nil {
			return nil, err
		}
		return e.vm.NewString(p), nil
	})
	e.setHostMethod(fsObj, "readdirSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("readdirSync requires path")
		}
		entries, err := os.ReadDir(e.resolveFSPath(args[0].ToString()))
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(entries))
		for _, ent := range entries {
			out = append(out, map[string]any{
				"name":      ent.Name(),
				"isFile":    ent.Type().IsRegular(),
				"isDir":     ent.IsDir(),
				"isSymlink": ent.Type()&os.ModeSymlink != 0,
			})
		}
		return eqjsToJS(e.vm, out), nil
	})
	statHelper := func(lstat bool, args []*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("stat requires path")
		}
		p := e.resolveFSPath(args[0].ToString())
		var info os.FileInfo
		var err error
		if lstat {
			info, err = os.Lstat(p)
		} else {
			info, err = os.Stat(p)
		}
		if err != nil {
			return nil, err
		}
		return eqjsToJS(e.vm, map[string]any{
			"size":      float64(info.Size()),
			"mode":      float64(info.Mode().Perm()),
			"mtimeMs":   float64(info.ModTime().UnixMilli()),
			"isFile":    info.Mode().IsRegular(),
			"isDir":     info.IsDir(),
			"isSymlink": info.Mode()&os.ModeSymlink != 0,
		}), nil
	}
	e.setHostMethod(fsObj, "statSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return statHelper(false, args)
	})
	e.setHostMethod(fsObj, "lstatSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return statHelper(true, args)
	})
	e.setHostMethod(fsObj, "unlinkSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("unlinkSync requires path")
		}
		return e.vm.Undefined(), os.Remove(e.resolveFSPath(args[0].ToString()))
	})
	e.setHostMethod(fsObj, "rmSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("rmSync requires path")
		}
		p := e.resolveFSPath(args[0].ToString())
		rec := len(args) > 1 && args[1] != nil && args[1].ToBoolean()
		force := len(args) > 2 && args[2] != nil && args[2].ToBoolean()
		var err error
		if rec {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if err != nil && force && os.IsNotExist(err) {
			err = nil
		}
		return e.vm.Undefined(), err
	})
	e.setHostMethod(fsObj, "renameSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("renameSync requires two paths")
		}
		return e.vm.Undefined(), os.Rename(e.resolveFSPath(args[0].ToString()), e.resolveFSPath(args[1].ToString()))
	})
	e.setHostMethod(fsObj, "copyFileSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("copyFileSync requires two paths")
		}
		data, err := os.ReadFile(e.resolveFSPath(args[0].ToString()))
		if err != nil {
			return nil, err
		}
		return e.vm.Undefined(), os.WriteFile(e.resolveFSPath(args[1].ToString()), data, 0o644)
	})
	e.setHostMethod(fsObj, "realpathSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("realpathSync requires path")
		}
		resolved, err := filepath.EvalSymlinks(e.resolveFSPath(args[0].ToString()))
		if err != nil {
			return nil, err
		}
		return e.vm.NewString(resolved), nil
	})
	e.setHostMethod(fsObj, "chmodSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("chmodSync requires path and mode")
		}
		return e.vm.Undefined(), os.Chmod(e.resolveFSPath(args[0].ToString()), os.FileMode(uint32(args[1].ToNumber())))
	})
	e.setHostMethod(fsObj, "accessSync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("accessSync requires path")
		}
		_, err := os.Stat(e.resolveFSPath(args[0].ToString()))
		return e.vm.Undefined(), err
	})
	hostObj.SetProp("fs", fsObj)
}

func eqjsHashFactory(alg string) (func() hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(alg)) {
	case "sha256", "sha-256":
		return sha256.New, nil
	case "sha512", "sha-512":
		return sha512.New, nil
	case "sha1", "sha-1":
		return sha1.New, nil
	case "md5":
		return md5.New, nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm %q", alg)
	}
}

func (e *eqjsVM) installCryptoURLHostFuncs(hostObj *quickjs.JSValueHandle) {
	cObj := e.vm.NewObject()
	defer cObj.Dispose()
	e.setHostMethod(cObj, "randomBytes", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		n := 0
		if len(args) > 0 && args[0] != nil {
			n = int(args[0].ToNumber())
		}
		if n < 0 {
			return nil, errors.New("RangeError: size must be >= 0")
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		return e.vm.NewArrayBuffer(buf), nil
	})
	e.setHostMethod(cObj, "randomUUID", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		s := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		return e.vm.NewString(s), nil
	})
	e.setHostMethod(cObj, "uuidv7", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(ai.Uuidv7()), nil
	})
	e.setHostMethod(cObj, "randomInt", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("randomInt requires min and max")
		}
		lo := int64(args[0].ToNumber())
		hi := int64(args[1].ToNumber())
		if hi <= lo {
			return nil, errors.New("RangeError: max must be greater than min")
		}
		n, err := rand.Int(rand.Reader, big.NewInt(hi-lo))
		if err != nil {
			return nil, err
		}
		return e.vm.NewNumber(float64(lo + n.Int64())), nil
	})
	e.setHostMethod(cObj, "hash", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("hash requires algorithm and data")
		}
		mk, err := eqjsHashFactory(args[0].ToString())
		if err != nil {
			return nil, err
		}
		data, err := args[1].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		h := mk()
		h.Write(data)
		return e.vm.NewArrayBuffer(h.Sum(nil)), nil
	})
	e.setHostMethod(cObj, "hmac", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return nil, errors.New("hmac requires algorithm, key, and data")
		}
		mk, err := eqjsHashFactory(args[0].ToString())
		if err != nil {
			return nil, err
		}
		key, err := args[1].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		data, err := args[2].ToArrayBuffer()
		if err != nil {
			return nil, err
		}
		m := hmac.New(mk, key)
		m.Write(data)
		return e.vm.NewArrayBuffer(m.Sum(nil)), nil
	})
	hostObj.SetProp("crypto", cObj)

	urlObj := e.vm.NewObject()
	defer urlObj.Dispose()
	e.setHostMethod(urlObj, "fileURLToPath", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("fileURLToPath requires a URL")
		}
		raw := args[0].ToString()
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "file" {
			return nil, fmt.Errorf("invalid file URL %q", raw)
		}
		return e.vm.NewString(filepath.FromSlash(u.Path)), nil
	})
	e.setHostMethod(urlObj, "pathToFileURL", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("pathToFileURL requires a path")
		}
		return e.vm.NewString(eqjsPathToFileURL(e.resolveFSPath(args[0].ToString()))), nil
	})
	hostObj.SetProp("url", urlObj)
}

func eqjsPathToFileURL(p string) string {
	slashed := filepath.ToSlash(p)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

func (e *eqjsVM) installPiModuleHostFuncs(hostObj *quickjs.JSValueHandle) {
	agentDir := utils.GetAgentDir()
	constObj := eqjsToJS(e.vm, map[string]any{
		"version":       utils.VERSION,
		"appName":       utils.AppName,
		"configDirName": utils.ConfigDirName,
		"agentDir":      agentDir,
		"modelsPath":    filepath.Join(agentDir, "models.json"),
		"settingsPath":  filepath.Join(agentDir, "settings.json"),
		"sessionsDir":   filepath.Join(agentDir, "sessions"),
	})
	hostObj.SetProp("constants", constObj)
	constObj.Dispose()

	// typebox validation bridge
	tbObj := e.vm.NewObject()
	defer tbObj.Dispose()
	parseTB := func(h *quickjs.JSValueHandle) (*typebox.Schema, error) {
		dumped, err := eqjsDumpPlain(e.vm, h)
		if err != nil {
			return nil, err
		}
		raw, err := jsonx.Marshal(dumped)
		if err != nil {
			return nil, err
		}
		return typebox.FromJSON(raw)
	}
	e.setHostMethod(tbObj, "check", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.False(), nil
		}
		s, err := parseTB(args[0])
		if err != nil {
			return nil, err
		}
		val, err := eqjsDumpPlain(e.vm, args[1])
		if err != nil {
			return nil, err
		}
		rawVal, _ := jsonx.Marshal(val)
		parsedVal, _ := jsonx.Parse(rawVal)
		if typebox.Check(s, parsedVal) {
			return e.vm.True(), nil
		}
		return e.vm.False(), nil
	})
	e.setHostMethod(tbObj, "errors", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return eqjsToJS(e.vm, []any{}), nil
		}
		s, err := parseTB(args[0])
		if err != nil {
			return nil, err
		}
		val, _ := eqjsDumpPlain(e.vm, args[1])
		rawVal, _ := jsonx.Marshal(val)
		parsedVal, _ := jsonx.Parse(rawVal)
		errs := typebox.Errors(s, parsedVal)
		out := make([]any, 0, len(errs))
		for _, ve := range errs {
			out = append(out, map[string]any{
				"path":    ve.InstancePath,
				"message": ve.Message,
				"keyword": ve.Keyword,
			})
		}
		return eqjsToJS(e.vm, out), nil
	})
	e.setHostMethod(tbObj, "convert", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		s, err := parseTB(args[0])
		if err != nil {
			return nil, err
		}
		val, _ := eqjsDumpPlain(e.vm, args[1])
		rawVal, _ := jsonx.Marshal(val)
		parsedVal, _ := jsonx.Parse(rawVal)
		conv := typebox.Convert(s, parsedVal)
		return eqjsToJS(e.vm, conv), nil
	})
	hostObj.SetProp("typebox", tbObj)

	// tui bridge
	tuiObj := e.vm.NewObject()
	defer tuiObj.Dispose()
	e.setHostMethod(tuiObj, "visibleWidth", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var s string
		if len(args) > 0 && args[0] != nil {
			s = args[0].ToString()
		}
		return e.vm.NewNumber(float64(tui.VisibleWidth(s))), nil
	})
	e.setHostMethod(tuiObj, "truncateToWidth", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var text string
		maxW := 80
		ellipsis := "..."
		pad := false
		if len(args) > 0 && args[0] != nil {
			text = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			maxW = int(args[1].ToNumber())
		}
		if len(args) > 2 && args[2] != nil {
			ellipsis = args[2].ToString()
		}
		if len(args) > 3 && args[3] != nil {
			pad = args[3].ToBoolean()
		}
		return e.vm.NewString(tui.TruncateToWidth(text, maxW, ellipsis, pad)), nil
	})
	e.setHostMethod(tuiObj, "wrapTextWithAnsi", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var text string
		width := 80
		if len(args) > 0 && args[0] != nil {
			text = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			width = int(args[1].ToNumber())
		}
		return eqjsToJS(e.vm, tui.WrapTextWithAnsi(text, width)), nil
	})
	e.setHostMethod(tuiObj, "parseKey", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var data string
		if len(args) > 0 && args[0] != nil {
			data = args[0].ToString()
		}
		parsed := tui.ParseKey(data)
		if parsed == nil {
			return e.vm.Undefined(), nil
		}
		return e.vm.NewString(*parsed), nil
	})
	e.setHostMethod(tuiObj, "matchesKey", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var data, key string
		if len(args) > 0 && args[0] != nil {
			data = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			key = args[1].ToString()
		}
		if tui.MatchesKey(data, tui.KeyId(key)) {
			return e.vm.True(), nil
		}
		return e.vm.False(), nil
	})
	hostObj.SetProp("tui", tuiObj)

	// ai bridge
	aiObj := e.vm.NewObject()
	defer aiObj.Dispose()
	e.setHostMethod(aiObj, "getProviders", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		provs := compat.GetProviders()
		out := make([]string, len(provs))
		for i, p := range provs {
			out[i] = string(p)
		}
		return eqjsToJS(e.vm, out), nil
	})
	e.setHostMethod(aiObj, "getModels", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var prov string
		if len(args) > 0 && args[0] != nil {
			prov = args[0].ToString()
		}
		if prov != "" {
			return eqjsToJS(e.vm, compat.GetModels(compat.BuiltinProvider(prov))), nil
		}
		var all []*ai.Model
		for _, p := range compat.GetProviders() {
			all = append(all, compat.GetModels(p)...)
		}
		return eqjsToJS(e.vm, all), nil
	})
	e.setHostMethod(aiObj, "getModel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var prov, id string
		if len(args) > 0 && args[0] != nil {
			prov = args[0].ToString()
		}
		if len(args) > 1 && args[1] != nil {
			id = args[1].ToString()
		}
		m := compat.GetModel(compat.BuiltinProvider(prov), id)
		if m == nil {
			return e.vm.Undefined(), nil
		}
		return eqjsToJS(e.vm, m), nil
	})
	e.setHostMethod(aiObj, "getEnvApiKey", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var prov string
		if len(args) > 0 && args[0] != nil {
			prov = args[0].ToString()
		}
		k := ai.GetEnvApiKey(prov, nil)
		if k == nil {
			return e.vm.Undefined(), nil
		}
		return e.vm.NewString(*k), nil
	})
	hostObj.SetProp("ai", aiObj)

	// coding bridge
	codingObj := e.vm.NewObject()
	defer codingObj.Dispose()
	e.setHostMethod(codingObj, "parseFrontmatter", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var content string
		if len(args) > 0 && args[0] != nil {
			content = args[0].ToString()
		}
		parsed, err := utils.ParseFrontmatter[map[string]any](content)
		if err != nil {
			return nil, err
		}
		return eqjsToJS(e.vm, map[string]any{
			"frontmatter": parsed.Frontmatter,
			"body":        parsed.Body,
		}), nil
	})
	e.setHostMethod(codingObj, "execBuiltinTool", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 4 {
			return nil, errors.New("execBuiltinTool requires name, cwd, toolCallId, params")
		}
		name := args[0].ToString()
		toolCwd := args[1].ToString()
		if toolCwd == "" {
			toolCwd = e.cwd
		}
		callID := args[2].ToString()
		dumpedParams, _ := eqjsDumpPlain(e.vm, args[3])
		rawParams, _ := jsonx.Marshal(dumpedParams)
		parsedObj, _ := jsonx.Parse(rawParams)
		objParams, _ := parsedObj.(*jsonx.Object)
		if objParams == nil {
			objParams = jsonx.NewObject()
		}
		def, err := CreateToolDefinition(ToolName(name), toolCwd)
		if err != nil {
			return nil, err
		}
		deferred := e.vm.NewPromise()
		go func() {
			var ectx ExtensionToolContext
			if e.runtime != nil && e.runtime.CreateContext != nil {
				if base := e.runtime.CreateContext(); base != nil {
					if ec, ok := base.(*erContext); ok {
						ectx = &erToolContext{
							erContext:  *ec,
							toolCallId: callID,
							signal:     e.vmCtx,
						}
					} else {
						ectx = &eqjsToolContextWrapper{ExtensionContext: base}
					}
				}
			}
			res, execErr := def.Execute(e.vmCtx, callID, objParams, nil, ectx)
			if execErr != nil {
				eqjsRejectDeferred(e, deferred, execErr)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})
	hostObj.SetProp("coding", codingObj)
}

func eqjsResolveDeferred(e *eqjsVM, d *quickjs.Deferred, val any) {
	e.postToLoop(func() {
		if e.vm.Err() != nil {
			return
		}
		h := eqjsToJS(e.vm, val)
		d.Resolve(h)
		h.Dispose()
		_, _ = e.vm.ExecutePendingJobs()
	})
}

type eqjsToolContextWrapper struct {
	ExtensionContext
}

func (w *eqjsToolContextWrapper) Tools() []*agent.AgentTool {
	return nil
}

func (w *eqjsToolContextWrapper) ExecuteTool(_ context.Context, _ string, _ any, _ ...*ExecuteToolOptions) (agent.AgentToolCallOutcome, error) {
	return agent.AgentToolCallOutcome{}, errors.New("ExecuteTool is unavailable outside an active runner tool call")
}

func eqjsRejectDeferred(e *eqjsVM, d *quickjs.Deferred, err error) {
	e.postToLoop(func() {
		if e.vm.Err() != nil {
			return
		}
		errH := e.vm.NewError(err)
		d.Reject(errH)
		errH.Dispose()
		_, _ = e.vm.ExecutePendingJobs()
	})
}

func (e *eqjsVM) installAPIHostFuncs(hostObj *quickjs.JSValueHandle) {
	apiObj := e.vm.NewObject()
	defer apiObj.Dispose()

	e.setHostMethod(apiObj, "on", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return nil, errors.New("pi.on requires event name and handler function")
		}
		eventName := args[0].ToString()
		fnHandle := e.retain(args[1])
		unsub, err := e.registerEventHandler(eventName, fnHandle)
		if err != nil {
			return nil, err
		}
		unsubFn := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			if unsub != nil {
				unsub()
			}
			return e.vm.Undefined(), nil
		})
		return unsubFn, nil
	})

	e.setHostMethod(apiObj, "eventsOn", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return nil, errors.New("pi.events.on requires channel and handler")
		}
		chName := args[0].ToString()
		fnHandle := e.retain(args[1])
		unsub := e.api.Events().On(chName, func(data any) {
			if e.isClosed() {
				return
			}
			_, _ = e.callFnAwait(e.vmCtx, "eventbus:"+chName, fnHandle, nil, func(_ int64) []*quickjs.JSValueHandle {
				return []*quickjs.JSValueHandle{eqjsToJS(e.vm, data)}
			})
		})
		e.eventUnsubMu.Lock()
		e.eventUnsubs = append(e.eventUnsubs, unsub)
		e.eventUnsubMu.Unlock()
		return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			unsub()
			return e.vm.Undefined(), nil
		}), nil
	})

	e.setHostMethod(apiObj, "eventsEmit", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		chName := args[0].ToString()
		var payload any
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() {
			payload, _ = eqjsDumpPlain(e.vm, args[1])
		}
		// Run Emit off the loop goroutine while servicing reqCh/postCh so same-VM or
		// cross-VM listeners can re-enter without self-deadlock.
		done := make(chan struct{})
		go func() {
			defer close(done)
			e.api.Events().Emit(chName, payload)
		}()
		e.pumpUntil(done)
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerTool", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil || !args[0].IsObject() {
			return nil, errors.New("registerTool expects tool definition object")
		}
		toolObj := args[0]
		var paramsJSON, outJSON string
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() {
			paramsJSON = args[1].ToString()
		}
		if len(args) > 2 && args[2] != nil && !args[2].IsUndefined() {
			outJSON = args[2].ToString()
		}
		def, err := e.buildToolDefinition(toolObj, paramsJSON, outJSON)
		if err != nil {
			return nil, err
		}
		e.api.RegisterTool(def)
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerCommand", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsObject() {
			return nil, errors.New("registerCommand expects name and options object")
		}
		name := args[0].ToString()
		optsObj := args[1]
		cmd := RegisteredCommand{Name: name}
		if d := optsObj.GetProp("description"); d != nil {
			if d.IsString() {
				s := d.ToString()
				cmd.Description = &s
			}
			d.Dispose()
		}
		hProp := optsObj.GetProp("handler")
		if hProp != nil && hProp.IsFunction() {
			hFn := e.retain(hProp)
			cmd.Handler = func(ctx context.Context, cmdArgs string, ectx ExtensionCommandContext) error {
				_, err := e.callFnAwait(ctx, "command:"+name, hFn, nil, func(signalID int64) []*quickjs.JSValueHandle {
					ctxID := e.allocCtx(ectx, nil, ctx)
					ctxJS := e.makeContextJS(ctxID, signalID, true, false)
					return []*quickjs.JSValueHandle{e.vm.NewString(cmdArgs), ctxJS}
				})
				return err
			}
		}
		if hProp != nil {
			hProp.Dispose()
		}
		compProp := optsObj.GetProp("getArgumentCompletions")
		if compProp != nil && compProp.IsFunction() {
			compFn := e.retain(compProp)
			cmd.GetArgumentCompletions = func(ctx context.Context, prefix string) ([]tui.AutocompleteItem, error) {
				res, err := e.callFnAwait(ctx, "command_completions:"+name, compFn, nil, func(_ int64) []*quickjs.JSValueHandle {
					return []*quickjs.JSValueHandle{e.vm.NewString(prefix)}
				})
				if err != nil || res.val == nil {
					return nil, err
				}
				return eqjsDecodeAutocompleteItems(res.val), nil
			}
		}
		if compProp != nil {
			compProp.Dispose()
		}
		e.api.RegisterCommand(name, cmd)
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerShortcut", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsObject() {
			return nil, errors.New("registerShortcut expects key and options")
		}
		key := tui.KeyId(args[0].ToString())
		optsObj := args[1]
		var sopts ExtensionShortcutOptions
		if d := optsObj.GetProp("description"); d != nil {
			if d.IsString() {
				s := d.ToString()
				sopts.Description = &s
			}
			d.Dispose()
		}
		hProp := optsObj.GetProp("handler")
		if hProp != nil && hProp.IsFunction() {
			hFn := e.retain(hProp)
			sopts.Handler = func(ctx context.Context, ectx ExtensionContext) error {
				_, err := e.callFnAwait(ctx, "shortcut:"+string(key), hFn, nil, func(signalID int64) []*quickjs.JSValueHandle {
					ctxID := e.allocCtx(ectx, nil, ctx)
					return []*quickjs.JSValueHandle{e.makeContextJS(ctxID, signalID, false, false)}
				})
				return err
			}
		}
		if hProp != nil {
			hProp.Dispose()
		}
		e.api.RegisterShortcut(key, sopts)
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerFlag", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil {
			return nil, errors.New("registerFlag expects name and options")
		}
		name := args[0].ToString()
		dumped, _ := eqjsDumpPlain(e.vm, args[1])
		var opts ExtensionFlagOptions
		if m, ok := dumped.(map[string]any); ok {
			if d, ok := m["description"].(string); ok {
				opts.Description = &d
			}
			if t, ok := m["type"].(string); ok {
				opts.Type = t
			}
			opts.Default = m["default"]
		}
		e.api.RegisterFlag(name, opts)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "getFlag", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		v := e.api.GetFlag(args[0].ToString())
		if v == nil {
			return e.vm.Undefined(), nil
		}
		return eqjsToJS(e.vm, v), nil
	})

	e.setHostMethod(apiObj, "registerMessageRenderer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return e.vm.Undefined(), nil
		}
		ct := args[0].ToString()
		fn := e.retain(args[1])
		e.api.RegisterMessageRenderer(ct, func(message *CustomMessage, options MessageRenderOptions, _ *theme.Theme) tui.Component {
			return e.callComponentRenderer("messageRenderer:"+ct, fn, message, options)
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "registerEntryRenderer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return e.vm.Undefined(), nil
		}
		ct := args[0].ToString()
		fn := e.retain(args[1])
		e.api.RegisterEntryRenderer(ct, func(entry *CustomEntry, options EntryRenderOptions, _ *theme.Theme) tui.Component {
			return e.callComponentRenderer("entryRenderer:"+ct, fn, entry, options)
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "registerToolRenderer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil || !args[0].IsFunction() {
			return e.vm.Undefined(), nil
		}
		fn := e.retain(args[0])
		e.api.RegisterToolRenderer(func(toolName string, next func() *ToolRenderers) *ToolRenderers {
			res, err := e.callSyncOnLoop(e.vmCtx, fn, nil, func() []*quickjs.JSValueHandle {
				nextWrapper := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
					if next != nil {
						_ = next()
					}
					return e.vm.Undefined(), nil
				})
				return []*quickjs.JSValueHandle{e.vm.NewString(toolName), nextWrapper}
			})
			if err != nil || res == nil {
				return next()
			}
			m, ok := res.(map[string]any)
			if !ok {
				return next()
			}
			out := &ToolRenderers{}
			if rs, ok := m["renderShell"].(string); ok {
				out.RenderShell = ToolRenderShell(rs)
			}
			return out
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "registerMarkdownTransformer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil || !args[0].IsFunction() {
			return e.vm.Undefined(), nil
		}
		fn := e.retain(args[0])
		var mopts *MarkdownTransformerOptions
		if len(args) > 1 && args[1] != nil && !args[1].IsNull() && !args[1].IsUndefined() {
			if dumped, err := eqjsDumpPlain(e.vm, args[1]); err == nil {
				if m, ok := dumped.(map[string]any); ok {
					if rawTypes, ok := m["messageTypes"].([]any); ok {
						mopts = &MarkdownTransformerOptions{}
						for _, rt := range rawTypes {
							if s, ok := rt.(string); ok {
								mopts.MessageTypes = append(mopts.MessageTypes, MarkdownMessageType(s))
							}
						}
					}
				}
			}
		}
		transformer := func(markdown string, rctx MarkdownTransformContext) string {
			res, err := e.callSyncOnLoop(e.vmCtx, fn, nil, func() []*quickjs.JSValueHandle {
				return []*quickjs.JSValueHandle{
					e.vm.NewString(markdown),
					eqjsToJS(e.vm, map[string]any{
						"messageType":    string(rctx.MessageType),
						"isStreaming":    rctx.IsStreaming,
						"availableWidth": rctx.AvailableWidth,
						"messageId":      rctx.MessageId,
						"timestamp":      rctx.Timestamp,
					}),
				}
			})
			if err != nil {
				return markdown
			}
			if s, ok := res.(string); ok {
				return s
			}
			return markdown
		}
		if mopts != nil {
			e.api.RegisterMarkdownTransformer(transformer, mopts)
		} else {
			e.api.RegisterMarkdownTransformer(transformer)
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "sendMessage", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		dumped, _ := eqjsDumpPlain(e.vm, args[0])
		var msg CustomMessageInput
		if m, ok := dumped.(map[string]any); ok {
			msg.CustomType, _ = m["customType"].(string)
			msg.Content = m["content"]
			msg.Display, _ = m["display"].(bool)
			msg.Details = m["details"]
		}
		var opts *SendMessageOptions
		if len(args) > 1 && args[1] != nil && !args[1].IsNull() && !args[1].IsUndefined() {
			if od, err := eqjsDumpPlain(e.vm, args[1]); err == nil {
				if m, ok := od.(map[string]any); ok {
					opts = &SendMessageOptions{}
					opts.TriggerTurn, _ = m["triggerTurn"].(bool)
					opts.DeliverAs, _ = m["deliverAs"].(string)
				}
			}
		}
		if opts != nil {
			e.api.SendMessage(msg, opts)
		} else {
			e.api.SendMessage(msg)
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "sendUserMessage", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		content, _ := eqjsDumpPlain(e.vm, args[0])
		var opts *SendUserMessageOptions
		if len(args) > 1 && args[1] != nil && !args[1].IsNull() && !args[1].IsUndefined() {
			if od, err := eqjsDumpPlain(e.vm, args[1]); err == nil {
				if m, ok := od.(map[string]any); ok {
					if da, ok := m["deliverAs"].(string); ok && da != "" {
						opts = &SendUserMessageOptions{DeliverAs: da}
					}
				}
			}
		}
		if opts != nil {
			e.api.SendUserMessage(content, opts)
		} else {
			e.api.SendUserMessage(content)
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "appendEntry", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return e.vm.Undefined(), nil
		}
		ct := args[0].ToString()
		var data any
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() {
			data, _ = eqjsDumpPlain(e.vm, args[1])
		}
		e.api.AppendEntry(ct, data)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "setSessionName", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			e.api.SetSessionName(args[0].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "getSessionName", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		name := e.api.GetSessionName()
		if name == nil {
			return e.vm.Undefined(), nil
		}
		return e.vm.NewString(*name), nil
	})
	e.setHostMethod(apiObj, "setLabel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		id := args[0].ToString()
		var lbl *string
		if args[1] != nil && !args[1].IsNull() && !args[1].IsUndefined() {
			s := args[1].ToString()
			lbl = &s
		}
		e.api.SetLabel(id, lbl)
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "exec", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("pi.exec requires command")
		}
		cmd := args[0].ToString()
		var cmdArgs []string
		if len(args) > 1 && args[1] != nil {
			if dumped, err := eqjsDumpPlain(e.vm, args[1]); err == nil {
				if slice, ok := dumped.([]any); ok {
					for _, x := range slice {
						cmdArgs = append(cmdArgs, fmt.Sprint(x))
					}
				}
			}
		}
		var execOpts ExecOptions
		if len(args) > 2 && args[2] != nil && !args[2].IsNull() && !args[2].IsUndefined() {
			if dumped, err := eqjsDumpPlain(e.vm, args[2]); err == nil {
				if m, ok := dumped.(map[string]any); ok {
					if t, ok := eqjsAsInt64(m["timeout"]); ok {
						execOpts.Timeout = &t
					}
					if c, ok := m["cwd"].(string); ok {
						execOpts.Cwd = c
					}
					if mb, ok := eqjsAsInt64(m["maxBuffer"]); ok {
						mbi := int(mb)
						execOpts.MaxBuffer = &mbi
					}
				}
			}
		}

		deferred := e.vm.NewPromise()
		go func() {
			res, err := e.api.Exec(e.vmCtx, cmd, cmdArgs, &execOpts)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})

	e.setHostMethod(apiObj, "getActiveTools", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return eqjsToJS(e.vm, e.api.GetActiveTools()), nil
	})
	e.setHostMethod(apiObj, "getAllTools", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return eqjsToJS(e.vm, e.api.GetAllTools()), nil
	})
	e.setHostMethod(apiObj, "getSettings", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return eqjsToJS(e.vm, e.api.GetSettings()), nil
	})
	e.setHostMethod(apiObj, "setActiveTools", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var names []string
		if len(args) > 0 && args[0] != nil {
			if dumped, err := eqjsDumpPlain(e.vm, args[0]); err == nil {
				if s, ok := dumped.([]any); ok {
					for _, x := range s {
						if str, ok := x.(string); ok {
							names = append(names, str)
						}
					}
				}
			}
		}
		e.api.SetActiveTools(names)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "getCommands", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return eqjsToJS(e.vm, e.api.GetCommands()), nil
	})
	e.setHostMethod(apiObj, "setModel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var m *ai.Model
		if len(args) > 0 && args[0] != nil && !args[0].IsNull() && !args[0].IsUndefined() {
			dumped, _ := eqjsDumpPlain(e.vm, args[0])
			m = eqjsDecodeModel(dumped)
		}
		deferred := e.vm.NewPromise()
		go func() {
			ok, err := e.api.SetModel(e.vmCtx, m)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, ok)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(apiObj, "getThinkingLevel", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return e.vm.NewString(string(e.api.GetThinkingLevel())), nil
	})
	e.setHostMethod(apiObj, "setThinkingLevel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			e.api.SetThinkingLevel(agent.ThinkingLevel(args[0].ToString()))
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerNativeProvider", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil && args[0].IsObject() {
			dumped, _ := eqjsDumpPlain(e.vm, args[0])
			if p := eqjsDecodeNativeProvider(dumped); p != nil {
				e.api.RegisterNativeProvider(p)
			}
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "registerProvider", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsObject() {
			return nil, errors.New("registerProvider expects name and config")
		}
		name := args[0].ToString()
		cfg, err := e.buildProviderConfig(args[1])
		if err != nil {
			return nil, err
		}
		e.api.RegisterProvider(name, cfg)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "unregisterProvider", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			e.api.UnregisterProvider(args[0].ToString())
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(apiObj, "registerMcpServer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsObject() {
			return nil, errors.New("registerMcpServer expects name and config")
		}
		name := args[0].ToString()
		dumped, _ := eqjsDumpPlain(e.vm, args[1])
		cfg, err := eqjsDecodeMcpConfig(dumped)
		if err != nil {
			return nil, err
		}
		if err := eqjsCallSafe(func() {
			e.api.RegisterMcpServer(name, cfg)
		}); err != nil {
			return nil, err
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "unregisterMcpServer", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) > 0 && args[0] != nil {
			e.api.UnregisterMcpServer(args[0].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "getMcpServers", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		return eqjsToJS(e.vm, e.api.GetMcpServers()), nil
	})

	e.setHostMethod(apiObj, "registerVirtualModel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 || args[0] == nil || !args[0].IsObject() {
			return nil, errors.New("registerVirtualModel expects definition object")
		}
		def := e.buildVirtualModelDef(args[0])
		e.api.RegisterVirtualModel(def)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(apiObj, "unregisterVirtualModel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) >= 2 {
			e.api.UnregisterVirtualModel(args[0].ToString(), args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})

	hostObj.SetProp("api", apiObj)
}

func (e *eqjsVM) allocCtx(ectx ExtensionContext, trustCtx *ProjectTrustContext, callCtx context.Context) int {
	e.ctxMu.Lock()
	defer e.ctxMu.Unlock()
	id := e.nextCtxID
	e.nextCtxID++
	e.ctxEntries[id] = &eqjsCtxEntry{
		ectx:     ectx,
		trustCtx: trustCtx,
		callCtx:  callCtx,
	}
	// Keep context map bounded while retaining recent closure refs.
	if len(e.ctxEntries) > 4096 {
		delete(e.ctxEntries, id-4096)
	}
	return id
}

func (e *eqjsVM) getCtxEntry(id int) *eqjsCtxEntry {
	e.ctxMu.RLock()
	entry := e.ctxEntries[id]
	e.ctxMu.RUnlock()
	if entry != nil {
		return entry
	}
	if e.runtime != nil && e.runtime.CreateContext != nil {
		return &eqjsCtxEntry{ectx: e.runtime.CreateContext()}
	}
	return &eqjsCtxEntry{}
}

func (e *eqjsVM) makeContextJS(ctxID int, signalID int64, isCommand, isTool bool) *quickjs.JSValueHandle {
	mkFn := e.vm.GetGlobal().GetProp("__piMakeContext")
	defer mkFn.Dispose()
	idH := e.vm.NewNumber(float64(ctxID))
	sigH := e.vm.NewNumber(float64(signalID))
	var cmdH, toolH *quickjs.JSValueHandle
	if isCommand {
		cmdH = e.vm.True()
	} else {
		cmdH = e.vm.False()
	}
	if isTool {
		toolH = e.vm.True()
	} else {
		toolH = e.vm.False()
	}
	res, err := e.vm.CallFunction(mkFn, e.vm.Undefined(), idH, sigH, cmdH, toolH)
	idH.Dispose()
	sigH.Dispose()
	if err != nil {
		if res != nil {
			res.Dispose()
		}
		return e.vm.NewObject()
	}
	return res
}

func (e *eqjsVM) installContextUIHostFuncs(hostObj *quickjs.JSValueHandle) {
	ctxObj := e.vm.NewObject()
	defer ctxObj.Dispose()

	e.setHostMethod(ctxObj, "getProp", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		prop := args[1].ToString()
		if entry.trustCtx != nil {
			switch prop {
			case "mode":
				return e.vm.NewString(string(entry.trustCtx.Mode)), nil
			case "hasUI":
				return eqjsBool(e.vm, entry.trustCtx.HasUI), nil
			case "cwd":
				return e.vm.NewString(entry.trustCtx.Cwd), nil
			}
		}
		if entry.ectx == nil {
			return e.vm.Undefined(), nil
		}
		switch prop {
		case "mode":
			return e.vm.NewString(string(entry.ectx.Mode())), nil
		case "hasUI":
			return eqjsBool(e.vm, entry.ectx.HasUI()), nil
		case "cwd":
			return e.vm.NewString(entry.ectx.Cwd()), nil
		case "model":
			return eqjsToJS(e.vm, entry.ectx.Model()), nil
		case "scopedModels":
			return eqjsToJS(e.vm, entry.ectx.ScopedModels()), nil
		case "thinkingLevel":
			return e.vm.NewString(string(entry.ectx.ThinkingLevel())), nil
		case "isIdle":
			return eqjsBool(e.vm, entry.ectx.IsIdle()), nil
		case "isProjectTrusted":
			return eqjsBool(e.vm, entry.ectx.IsProjectTrusted()), nil
		case "hasPendingMessages":
			return eqjsBool(e.vm, entry.ectx.HasPendingMessages()), nil
		case "getContextUsage":
			return eqjsToJS(e.vm, entry.ectx.GetContextUsage()), nil
		case "getSystemPrompt":
			return e.vm.NewString(entry.ectx.GetSystemPrompt()), nil
		case "getSystemPromptOptions":
			if cmdCtx, ok := entry.ectx.(ExtensionCommandContext); ok {
				return eqjsToJS(e.vm, cmdCtx.GetSystemPromptOptions()), nil
			}
			return e.vm.Undefined(), nil
		default:
			return e.vm.Undefined(), nil
		}
	})

	e.setHostMethod(ctxObj, "callAction", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if entry.ectx == nil {
			return e.vm.Undefined(), nil
		}
		switch args[1].ToString() {
		case "abort":
			go entry.ectx.Abort()
		case "shutdown":
			go entry.ectx.Shutdown()
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(ctxObj, "compact", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if entry.ectx == nil {
			return e.vm.Undefined(), nil
		}
		var opts *CompactOptions
		if len(args) > 1 && args[1] != nil && args[1].IsObject() {
			opts = &CompactOptions{}
			if ci := args[1].GetProp("customInstructions"); ci != nil {
				if ci.IsString() {
					opts.CustomInstructions = ci.ToString()
				}
				ci.Dispose()
			}
			if oc := args[1].GetProp("onComplete"); oc != nil {
				if oc.IsFunction() {
					ocFn := e.retain(oc)
					opts.OnComplete = func(res CompactionResult) {
						_, _ = e.callFnAwait(e.vmCtx, "compact.onComplete", ocFn, nil, func(_ int64) []*quickjs.JSValueHandle {
							return []*quickjs.JSValueHandle{eqjsToJS(e.vm, res)}
						})
					}
				}
				oc.Dispose()
			}
			if oe := args[1].GetProp("onError"); oe != nil {
				if oe.IsFunction() {
					oeFn := e.retain(oe)
					opts.OnError = func(err error) {
						_, _ = e.callFnAwait(e.vmCtx, "compact.onError", oeFn, nil, func(_ int64) []*quickjs.JSValueHandle {
							return []*quickjs.JSValueHandle{e.vm.NewError(err)}
						})
					}
				}
				oe.Dispose()
			}
		}
		if opts != nil {
			entry.ectx.Compact(opts)
		} else {
			entry.ectx.Compact()
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(ctxObj, "cmdAsync", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return nil, errors.New("cmdAsync requires ctxId and method")
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		method := args[1].ToString()
		var arg any
		if len(args) > 2 && args[2] != nil && !args[2].IsNull() && !args[2].IsUndefined() {
			arg, _ = eqjsDumpPlain(e.vm, args[2])
		}
		cmdCtx, ok := entry.ectx.(ExtensionCommandContext)
		if !ok || cmdCtx == nil {
			return nil, fmt.Errorf("context does not support %s outside command handlers", method)
		}
		deferred := e.vm.NewPromise()
		go func() {
			callCtx := e.vmCtx
			if entry.callCtx != nil {
				callCtx = entry.callCtx
			}
			switch method {
			case "waitForIdle":
				if err := cmdCtx.WaitForIdle(callCtx); err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, nil)
			case "newSession":
				res, err := cmdCtx.NewSession(callCtx)
				if err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, res)
			case "fork":
				m, _ := arg.(map[string]any)
				entryId, _ := m["entryId"].(string)
				res, err := cmdCtx.Fork(callCtx, entryId)
				if err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, res)
			case "navigateTree":
				m, _ := arg.(map[string]any)
				targetId, _ := m["targetId"].(string)
				res, err := cmdCtx.NavigateTree(callCtx, targetId)
				if err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, res)
			case "switchSession":
				m, _ := arg.(map[string]any)
				p, _ := m["path"].(string)
				res, err := cmdCtx.SwitchSession(callCtx, p)
				if err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, res)
			case "reload":
				if err := cmdCtx.Reload(callCtx); err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, nil)
			case "sendUserMessage":
				m, _ := arg.(map[string]any)
				content := m["content"]
				var sopts *SendUserMessageOptions
				if om, ok := m["opts"].(map[string]any); ok {
					if da, ok := om["deliverAs"].(string); ok && da != "" {
						sopts = &SendUserMessageOptions{DeliverAs: da}
					}
				}
				var err error
				if sopts != nil {
					err = cmdCtx.SendUserMessage(callCtx, content, sopts)
				} else {
					err = cmdCtx.SendUserMessage(callCtx, content)
				}
				if err != nil {
					eqjsRejectDeferred(e, deferred, err)
					return
				}
				eqjsResolveDeferred(e, deferred, nil)
			default:
				eqjsRejectDeferred(e, deferred, fmt.Errorf("unknown command context method %q", method))
			}
		}()
		return deferred.Handle, nil
	})

	e.setHostMethod(ctxObj, "toolCall", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		tctx, ok := entry.ectx.(ExtensionToolContext)
		if !ok || tctx == nil {
			return eqjsToJS(e.vm, []any{}), nil
		}
		if args[1].ToString() == "tools" {
			tools := tctx.Tools()
			out := make([]any, 0, len(tools))
			for _, t := range tools {
				if t == nil {
					continue
				}
				out = append(out, map[string]any{
					"name":        t.Name,
					"label":       t.Label,
					"description": t.Description,
					"parameters":  t.Parameters,
				})
			}
			return eqjsToJS(e.vm, out), nil
		}
		return e.vm.Undefined(), nil
	})

	e.setHostMethod(ctxObj, "executeTool", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return nil, errors.New("executeTool requires ctxId, name, args")
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		tctx, ok := entry.ectx.(ExtensionToolContext)
		if !ok || tctx == nil {
			return nil, errors.New("executeTool is only available inside tool execution")
		}
		toolName := args[1].ToString()
		dumpedArgs, _ := eqjsDumpPlain(e.vm, args[2])
		deferred := e.vm.NewPromise()
		go func() {
			callCtx := e.vmCtx
			if entry.callCtx != nil {
				callCtx = entry.callCtx
			}
			outcome, err := tctx.ExecuteTool(callCtx, toolName, dumpedArgs)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, outcome)
		}()
		return deferred.Handle, nil
	})

	e.setHostMethod(ctxObj, "smCall", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if entry.ectx == nil || entry.ectx.SessionManager() == nil {
			return e.vm.Undefined(), nil
		}
		sm := entry.ectx.SessionManager()
		switch args[1].ToString() {
		case "getCwd":
			return e.vm.NewString(sm.GetCwd()), nil
		case "getSessionDir":
			return e.vm.NewString(sm.GetSessionDir()), nil
		case "getSessionId":
			return e.vm.NewString(sm.GetSessionId()), nil
		case "getSessionFile":
			return eqjsToJS(e.vm, sm.GetSessionFile()), nil
		case "getLeafId":
			return eqjsToJS(e.vm, sm.GetLeafId()), nil
		case "getLeafEntry":
			return eqjsToJS(e.vm, sm.GetLeafEntry()), nil
		case "getEntry":
			var id string
			if len(args) > 2 && args[2] != nil {
				id = args[2].ToString()
			}
			return eqjsToJS(e.vm, sm.GetEntry(id)), nil
		case "getLabel":
			var id string
			if len(args) > 2 && args[2] != nil {
				id = args[2].ToString()
			}
			return eqjsToJS(e.vm, sm.GetLabel(id)), nil
		case "getBranch":
			if len(args) > 2 && args[2] != nil && args[2].IsString() {
				return eqjsToJS(e.vm, sm.GetBranch(args[2].ToString())), nil
			}
			return eqjsToJS(e.vm, sm.GetBranch()), nil
		case "getEntries":
			return eqjsToJS(e.vm, sm.GetEntries()), nil
		case "getTree":
			return eqjsToJS(e.vm, sm.GetTree()), nil
		case "getSessionName":
			return eqjsToJS(e.vm, sm.GetSessionName()), nil
		case "getHeader":
			return eqjsToJS(e.vm, sm.GetHeader()), nil
		default:
			return e.vm.Undefined(), nil
		}
	})

	e.setHostMethod(ctxObj, "mrCall", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if entry.ectx == nil || entry.ectx.ModelRegistry() == nil {
			return e.vm.Undefined(), nil
		}
		mr := entry.ectx.ModelRegistry()
		switch args[1].ToString() {
		case "find":
			if len(args) < 4 {
				return e.vm.Undefined(), nil
			}
			return eqjsToJS(e.vm, mr.Find(args[2].ToString(), args[3].ToString())), nil
		case "getAll":
			return eqjsToJS(e.vm, mr.GetAll()), nil
		case "getAvailable":
			return eqjsToJS(e.vm, mr.GetAvailable()), nil
		case "getApiKeyForProvider":
			var prov string
			if len(args) > 2 && args[2] != nil {
				prov = args[2].ToString()
			}
			return eqjsToJS(e.vm, mr.GetApiKeyForProvider(e.vmCtx, prov)), nil
		case "getApiKey":
			if len(args) < 3 || args[2] == nil || args[2].IsNull() || args[2].IsUndefined() {
				return e.vm.Undefined(), nil
			}
			d, _ := eqjsDumpPlain(e.vm, args[2])
			m := eqjsDecodeModel(d)
			auth := mr.GetApiKeyAndHeaders(e.vmCtx, m)
			if !auth.Ok || auth.ApiKey == nil {
				return e.vm.Undefined(), nil
			}
			return e.vm.NewString(*auth.ApiKey), nil
		case "getApiKeyAndHeaders":
			if len(args) < 3 || args[2] == nil || args[2].IsNull() || args[2].IsUndefined() {
				return eqjsToJS(e.vm, map[string]any{"ok": false}), nil
			}
			d, _ := eqjsDumpPlain(e.vm, args[2])
			m := eqjsDecodeModel(d)
			auth := mr.GetApiKeyAndHeaders(e.vmCtx, m)
			return eqjsToJS(e.vm, auth), nil
		case "isUsingOAuth":
			if len(args) < 3 || args[2] == nil || args[2].IsNull() || args[2].IsUndefined() {
				return e.vm.False(), nil
			}
			d, _ := eqjsDumpPlain(e.vm, args[2])
			m := eqjsDecodeModel(d)
			return eqjsBool(e.vm, mr.IsUsingOAuth(m)), nil
		default:
			return e.vm.Undefined(), nil
		}
	})

	e.setHostMethod(ctxObj, "themeCall", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return e.vm.NewString(""), nil
		}
		op := args[0].ToString()
		tok := args[1].ToString()
		txt := args[2].ToString()
		th := theme.CurrentTheme()
		if th == nil {
			if op == "getFgAnsi" || op == "getBgAnsi" {
				return e.vm.NewString(""), nil
			}
			return e.vm.NewString(txt), nil
		}
		switch op {
		case "fg":
			return e.vm.NewString(th.Fg(tok, txt)), nil
		case "bg":
			return e.vm.NewString(th.Bg(tok, txt)), nil
		case "bold":
			return e.vm.NewString(th.Bold(txt)), nil
		case "italic":
			return e.vm.NewString(th.Italic(txt)), nil
		case "dim":
			return e.vm.NewString(th.Fg("dim", txt)), nil
		case "strikethrough":
			return e.vm.NewString(th.Strikethrough(txt)), nil
		case "underline":
			return e.vm.NewString(th.Underline(txt)), nil
		case "inverse":
			return e.vm.NewString(th.Inverse(txt)), nil
		case "getFgAnsi":
			return e.vm.NewString(th.GetFgAnsi(tok)), nil
		case "getBgAnsi":
			return e.vm.NewString(th.GetBgAnsi(tok)), nil
		default:
			return e.vm.NewString(txt), nil
		}
	})

	hostObj.SetProp("ctx", ctxObj)

	// UI bridge
	uiObj := e.vm.NewObject()
	defer uiObj.Dispose()

	getTrustOrFullUI := func(entry *eqjsCtxEntry) (ProjectTrustUIContext, ExtensionUIContext) {
		if entry.trustCtx != nil && entry.trustCtx.Ui != nil {
			full, _ := entry.trustCtx.Ui.(ExtensionUIContext)
			return entry.trustCtx.Ui, full
		}
		if entry.ectx != nil && entry.ectx.UI() != nil {
			return entry.ectx.UI(), entry.ectx.UI()
		}
		noOp := NoOpUIContext()
		return noOp, noOp
	}

	e.setHostMethod(uiObj, "select", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		title := args[1].ToString()
		var opts []string
		if d, err := eqjsDumpPlain(e.vm, args[2]); err == nil {
			if arr, ok := d.([]any); ok {
				for _, x := range arr {
					opts = append(opts, fmt.Sprint(x))
				}
			}
		}
		dialogOpts := eqjsParseDialogOpts(e.vm, args, 3)
		pui, _ := getTrustOrFullUI(entry)
		deferred := e.vm.NewPromise()
		go func() {
			var res *string
			var err error
			if dialogOpts != nil {
				res, err = pui.Select(e.vmCtx, title, opts, dialogOpts)
			} else {
				res, err = pui.Select(e.vmCtx, title, opts)
			}
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(uiObj, "confirm", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		title := args[1].ToString()
		msg := args[2].ToString()
		dialogOpts := eqjsParseDialogOpts(e.vm, args, 3)
		pui, _ := getTrustOrFullUI(entry)
		deferred := e.vm.NewPromise()
		go func() {
			var ok bool
			var err error
			if dialogOpts != nil {
				ok, err = pui.Confirm(e.vmCtx, title, msg, dialogOpts)
			} else {
				ok, err = pui.Confirm(e.vmCtx, title, msg)
			}
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, ok)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(uiObj, "input", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		title := args[1].ToString()
		var placeholder *string
		if len(args) > 2 && args[2] != nil && !args[2].IsNull() && !args[2].IsUndefined() {
			s := args[2].ToString()
			placeholder = &s
		}
		dialogOpts := eqjsParseDialogOpts(e.vm, args, 3)
		pui, _ := getTrustOrFullUI(entry)
		deferred := e.vm.NewPromise()
		go func() {
			var res *string
			var err error
			if dialogOpts != nil {
				res, err = pui.Input(e.vmCtx, title, placeholder, dialogOpts)
			} else {
				res, err = pui.Input(e.vmCtx, title, placeholder)
			}
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(uiObj, "editor", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		title := args[1].ToString()
		var prefill string
		hasPrefill := false
		if len(args) > 2 && args[2] != nil && !args[2].IsNull() && !args[2].IsUndefined() {
			prefill = args[2].ToString()
			hasPrefill = true
		}
		_, fui := getTrustOrFullUI(entry)
		deferred := e.vm.NewPromise()
		go func() {
			if fui == nil {
				eqjsResolveDeferred(e, deferred, nil)
				return
			}
			var res *string
			var err error
			if hasPrefill {
				res, err = fui.Editor(e.vmCtx, title, prefill)
			} else {
				res, err = fui.Editor(e.vmCtx, title)
			}
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(uiObj, "notify", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		pui, _ := getTrustOrFullUI(entry)
		msg := args[1].ToString()
		if len(args) > 2 && args[2] != nil && args[2].IsString() && args[2].ToString() != "" {
			pui.Notify(msg, args[2].ToString())
		} else {
			pui.Notify(msg)
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setStatus", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		var text *string
		if args[2] != nil && !args[2].IsNull() && !args[2].IsUndefined() {
			s := args[2].ToString()
			text = &s
		}
		fui.SetStatus(args[1].ToString(), text)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setWorkingMessage", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		if args[1] == nil || args[1].IsNull() || args[1].IsUndefined() {
			fui.SetWorkingMessage()
		} else {
			fui.SetWorkingMessage(args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setWorkingVisible", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui != nil {
			fui.SetWorkingVisible(args[1].ToBoolean())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setWorkingIndicator", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		if args[1] == nil || args[1].IsNull() || args[1].IsUndefined() {
			fui.SetWorkingIndicator(nil)
			return e.vm.Undefined(), nil
		}
		var wi WorkingIndicatorOptions
		if d, err := eqjsDumpPlain(e.vm, args[1]); err == nil {
			raw, _ := jsonx.Marshal(d)
			_ = jsonx.Unmarshal(raw, &wi)
		}
		fui.SetWorkingIndicator(&wi)
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setHiddenThinkingLabel", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		if args[1] == nil || args[1].IsNull() || args[1].IsUndefined() {
			fui.SetHiddenThinkingLabel()
		} else {
			fui.SetHiddenThinkingLabel(args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setWidget", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 3 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		key := args[1].ToString()
		var wopts *ExtensionWidgetOptions
		if len(args) > 3 && args[3] != nil && args[3].IsObject() {
			if p := args[3].GetProp("placement"); p != nil {
				if p.IsString() {
					wopts = &ExtensionWidgetOptions{Placement: WidgetPlacement(p.ToString())}
				}
				p.Dispose()
			}
		}
		if args[2] == nil || args[2].IsNull() || args[2].IsUndefined() {
			if wopts != nil {
				fui.SetWidgetLines(key, nil, wopts)
			} else {
				fui.SetWidgetLines(key, nil)
			}
			return e.vm.Undefined(), nil
		}
		if args[2].IsFunction() {
			fn := e.retain(args[2])
			factory := func(t tui.TUI, _ *theme.Theme) tui.Component {
				return e.callComponentFactory("widget:"+key, fn, t, false, nil)
			}
			e.runOffLoopAndPump(func() {
				if wopts != nil {
					fui.SetWidgetFactory(key, factory, wopts)
				} else {
					fui.SetWidgetFactory(key, factory)
				}
			})
			return e.vm.Undefined(), nil
		}
		var lines []string
		if d, err := eqjsDumpPlain(e.vm, args[2]); err == nil {
			if arr, ok := d.([]any); ok {
				for _, x := range arr {
					lines = append(lines, fmt.Sprint(x))
				}
			}
		}
		e.runOffLoopAndPump(func() {
			if wopts != nil {
				fui.SetWidgetLines(key, lines, wopts)
			} else {
				fui.SetWidgetLines(key, lines)
			}
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setFooter", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		if args[1] == nil || !args[1].IsFunction() {
			e.runOffLoopAndPump(func() {
				fui.SetFooter(nil)
			})
			return e.vm.Undefined(), nil
		}
		fn := e.retain(args[1])
		e.runOffLoopAndPump(func() {
			fui.SetFooter(func(t tui.TUI, _ *theme.Theme, footerData ReadonlyFooterDataProvider) tui.Component {
				return e.callComponentFactory("footer", fn, t, true, footerData)
			})
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setHeader", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.vm.Undefined(), nil
		}
		if args[1] == nil || !args[1].IsFunction() {
			e.runOffLoopAndPump(func() {
				fui.SetHeader(nil)
			})
			return e.vm.Undefined(), nil
		}
		fn := e.retain(args[1])
		e.runOffLoopAndPump(func() {
			fui.SetHeader(func(t tui.TUI, _ *theme.Theme) tui.Component {
				return e.callComponentFactory("header", fn, t, false, nil)
			})
		})
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setTitle", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui != nil {
			fui.SetTitle(args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "custom", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return nil, errors.New("ctx.ui.custom requires a factory function")
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return nil, errors.New("UI custom not available")
		}
		factoryFn := e.retain(args[1])
		deferred := e.vm.NewPromise()
		go func() {
			res, err := fui.Custom(e.vmCtx, func(t tui.TUI, _ *theme.Theme, _ *tui.KeybindingsManager, done func(any)) (tui.Component, error) {
				var compHandle *quickjs.JSValueHandle
				callErr := e.runOnLoop(e.vmCtx, func() error {
					doneCb := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, cbArgs ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
						var v any
						if len(cbArgs) > 0 && cbArgs[0] != nil && !cbArgs[0].IsUndefined() {
							v, _ = eqjsDumpPlain(e.vm, cbArgs[0])
						}
						go done(v)
						return e.vm.Undefined(), nil
					})
					defer doneCb.Dispose()
					tuiShim := e.eqjsNewTuiShim(t)
					defer tuiShim.Dispose()
					kbShim := e.vm.NewObject()
					defer kbShim.Dispose()
					var themeVal *quickjs.JSValueHandle
					if g := e.vm.GetGlobal(); g != nil {
						themeVal = g.GetProp("__piTheme")
						g.Dispose()
					} else {
						themeVal = e.vm.Undefined()
					}
					defer themeVal.Dispose()

					r, err := e.vm.CallFunction(factoryFn, e.vm.Undefined(), tuiShim, themeVal, kbShim, doneCb)
					if err != nil {
						if r != nil {
							r.Dispose()
						}
						return eqjsFormatJSError(e.vm, err)
					}
					if r != nil && r.IsObject() {
						compHandle = e.retain(r)
					}
					if r != nil {
						r.Dispose()
					}
					return nil
				})
				if callErr != nil {
					return nil, callErr
				}
				return &eqjsComponent{evm: e, obj: compHandle}, nil
			})
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, res)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(uiObj, "pasteToEditor", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			fui.PasteToEditor(args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "setEditorText", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			fui.SetEditorText(args[1].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(uiObj, "getEditorText", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 {
			return e.vm.NewString(""), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			return e.vm.NewString(fui.GetEditorText()), nil
		}
		return e.vm.NewString(""), nil
	})
	e.setHostMethod(uiObj, "onTerminalInput", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 || args[1] == nil || !args[1].IsFunction() {
			return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
				return e.vm.Undefined(), nil
			}), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		_, fui := getTrustOrFullUI(entry)
		if fui == nil {
			return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
				return e.vm.Undefined(), nil
			}), nil
		}
		fn := e.retain(args[1])
		unsub := fui.OnTerminalInput(func(data string) *TerminalInputResult {
			res, err := e.callSyncOnLoop(e.vmCtx, fn, nil, func() []*quickjs.JSValueHandle {
				return []*quickjs.JSValueHandle{e.vm.NewString(data)}
			})
			if err != nil || res == nil {
				return nil
			}
			if m, ok := res.(map[string]any); ok {
				out := &TerminalInputResult{}
				if c, ok := m["consume"].(bool); ok {
					out.Consume = c
				}
				if d, ok := m["data"].(string); ok {
					out.Data = &d
				}
				return out
			}
			return nil
		})
		return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			unsub()
			return e.vm.Undefined(), nil
		}), nil
	})
	e.setHostMethod(uiObj, "getAllThemes", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 {
			return eqjsToJS(e.vm, []any{}), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			return eqjsToJS(e.vm, fui.GetAllThemes()), nil
		}
		return eqjsToJS(e.vm, []any{}), nil
	})
	e.setHostMethod(uiObj, "setTheme", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return eqjsToJS(e.vm, SetThemeResult{Success: false}), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			return eqjsToJS(e.vm, fui.SetThemeByName(args[1].ToString())), nil
		}
		return eqjsToJS(e.vm, SetThemeResult{Success: false}), nil
	})
	e.setHostMethod(uiObj, "getToolsExpanded", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 {
			return e.vm.False(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			return eqjsBool(e.vm, fui.GetToolsExpanded()), nil
		}
		return e.vm.False(), nil
	})
	e.setHostMethod(uiObj, "setToolsExpanded", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) < 2 {
			return e.vm.Undefined(), nil
		}
		entry := e.getCtxEntry(int(args[0].ToNumber()))
		if _, fui := getTrustOrFullUI(entry); fui != nil {
			fui.SetToolsExpanded(args[1].ToBoolean())
		}
		return e.vm.Undefined(), nil
	})

	hostObj.SetProp("ui", uiObj)
}

func (e *eqjsVM) buildToolDefinition(toolObj *quickjs.JSValueHandle, paramsJSON, outJSON string) (ToolDefinition, error) {
	var def ToolDefinition
	if p := toolObj.GetProp("name"); p != nil {
		def.Name = p.ToString()
		p.Dispose()
	}
	if def.Name == "" {
		return ToolDefinition{}, errors.New("tool definition requires name")
	}
	if p := toolObj.GetProp("label"); p != nil {
		if p.IsString() {
			def.Label = p.ToString()
		}
		p.Dispose()
	}
	if p := toolObj.GetProp("description"); p != nil {
		if p.IsString() {
			def.Description = p.ToString()
		}
		p.Dispose()
	}
	if p := toolObj.GetProp("promptSnippet"); p != nil {
		if p.IsString() {
			s := p.ToString()
			def.PromptSnippet = &s
		}
		p.Dispose()
	}
	if p := toolObj.GetProp("promptGuidelines"); p != nil {
		if d, err := eqjsDumpPlain(e.vm, p); err == nil {
			if arr, ok := d.([]any); ok {
				for _, x := range arr {
					if s, ok := x.(string); ok {
						def.PromptGuidelines = append(def.PromptGuidelines, s)
					}
				}
			}
		}
		p.Dispose()
	}
	if paramsJSON != "" && paramsJSON != "undefined" && paramsJSON != "null" {
		schema, err := typebox.FromJSON(paramsJSON)
		if err != nil {
			return ToolDefinition{}, fmt.Errorf("invalid tool parameters schema for %q: %w", def.Name, err)
		}
		def.Parameters = schema
	}
	if outJSON != "" && outJSON != "undefined" && outJSON != "null" {
		if schema, err := typebox.FromJSON(outJSON); err == nil {
			def.OutputSchema = schema
		}
	}
	if p := toolObj.GetProp("renderShell"); p != nil {
		if p.IsString() {
			def.RenderShell = ToolRenderShell(p.ToString())
		}
		p.Dispose()
	}
	if p := toolObj.GetProp("executionMode"); p != nil {
		if p.IsString() {
			def.ExecutionMode = agent.ToolExecutionMode(p.ToString())
		}
		p.Dispose()
	}
	if p := toolObj.GetProp("defaultActive"); p != nil {
		if p.IsBool() {
			b := p.ToBoolean()
			def.DefaultActive = &b
		}
		p.Dispose()
	}

	if prepProp := toolObj.GetProp("prepareArguments"); prepProp != nil {
		if prepProp.IsFunction() {
			prepFn := e.retain(prepProp)
			def.PrepareArguments = func(args any) any {
				res, err := e.callSyncOnLoop(e.vmCtx, prepFn, nil, func() []*quickjs.JSValueHandle {
					return []*quickjs.JSValueHandle{eqjsToJS(e.vm, args)}
				})
				if err != nil {
					return args
				}
				return res
			}
		}
		prepProp.Dispose()
	}

	execProp := toolObj.GetProp("execute")
	if execProp != nil && execProp.IsFunction() {
		execFn := e.retain(execProp)
		def.Execute = func(ctx context.Context, toolCallId string, params *jsonx.Object, onUpdate agent.AgentToolUpdateCallback, ectx ExtensionToolContext) (agent.AgentToolResult, error) {
			res, err := e.callFnAwait(ctx, "tool:"+def.Name, execFn, nil, func(signalID int64) []*quickjs.JSValueHandle {
				ctxID := e.allocCtx(ectx, nil, ctx)
				ctxJS := e.makeContextJS(ctxID, signalID, false, true)
				sigJS := ctxJS.GetProp("signal")
				updJS := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, uargs ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
					if onUpdate != nil && len(uargs) > 0 && uargs[0] != nil {
						if d, derr := eqjsDumpPlain(e.vm, uargs[0]); derr == nil {
							if partial, perr := eqjsDecodeToolResult(d); perr == nil {
								go onUpdate(partial)
							}
						}
					}
					return e.vm.Undefined(), nil
				})
				return []*quickjs.JSValueHandle{
					e.vm.NewString(toolCallId),
					eqjsToJS(e.vm, params),
					sigJS,
					updJS,
					ctxJS,
				}
			})
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			return eqjsDecodeToolResult(res.val)
		}
	}
	if execProp != nil {
		execProp.Dispose()
	}

	if rcProp := toolObj.GetProp("renderCall"); rcProp != nil {
		if rcProp.IsFunction() {
			rcFn := e.retain(rcProp)
			def.RenderCall = func(args *jsonx.Object, _ *theme.Theme, rctx ToolRenderContext) tui.Component {
				return e.callComponentRenderer("tool.renderCall:"+def.Name, rcFn, args, rctx)
			}
		}
		rcProp.Dispose()
	}
	if rrProp := toolObj.GetProp("renderResult"); rrProp != nil {
		if rrProp.IsFunction() {
			rrFn := e.retain(rrProp)
			def.RenderResult = func(result agent.AgentToolResult, options ToolRenderResultOptions, _ *theme.Theme, rctx ToolRenderContext) tui.Component {
				var compHandle *quickjs.JSValueHandle
				_ = e.runOnLoop(e.vmCtx, func() error {
					thVal := e.vm.GetGlobal().GetProp("__piTheme")
					defer thVal.Dispose()
					a0 := eqjsToJS(e.vm, result)
					a1 := eqjsToJS(e.vm, options)
					a2 := eqjsToJS(e.vm, rctx)
					defer a0.Dispose()
					defer a1.Dispose()
					defer a2.Dispose()
					r, err := e.vm.CallFunction(rrFn, e.vm.Undefined(), a0, a1, thVal, a2)
					if err == nil && r != nil && r.IsObject() {
						compHandle = e.retain(r)
					}
					if r != nil {
						r.Dispose()
					}
					return nil
				})
				return &eqjsComponent{evm: e, obj: compHandle}
			}
		}
		rrProp.Dispose()
	}
	return def, nil
}

func (e *eqjsVM) buildProviderConfig(cfgObj *quickjs.JSValueHandle) (ProviderConfig, error) {
	var cfg ProviderConfig
	if p := cfgObj.GetProp("name"); p != nil {
		if p.IsString() {
			s := p.ToString()
			cfg.Name = &s
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("baseUrl"); p != nil {
		if p.IsString() {
			s := p.ToString()
			cfg.BaseUrl = &s
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("apiKey"); p != nil {
		if p.IsString() {
			s := p.ToString()
			cfg.ApiKey = &s
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("api"); p != nil {
		if p.IsString() {
			s := p.ToString()
			cfg.Api = &s
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("authHeader"); p != nil {
		if p.IsBool() {
			b := p.ToBoolean()
			cfg.AuthHeader = &b
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("headers"); p != nil {
		if d, err := eqjsDumpPlain(e.vm, p); err == nil {
			if m, ok := d.(map[string]any); ok {
				hdrs := omap.NewMap[string, string]()
				for _, k := range erSortedKeys(m) {
					if s, ok := m[k].(string); ok {
						hdrs.Set(k, s)
					}
				}
				cfg.Headers = hdrs
			}
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("models"); p != nil {
		if d, err := eqjsDumpPlain(e.vm, p); err == nil {
			if arr, ok := d.([]any); ok {
				for _, item := range arr {
					if pm := eqjsDecodeProviderModel(item); pm != nil {
						cfg.Models = append(cfg.Models, pm)
					}
				}
			}
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("streamSimple"); p != nil {
		if p.IsFunction() {
			ssFn := e.retain(p)
			cfg.StreamSimple = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				outStream := ai.CreateAssistantMessageEventStream()
				go func() {
					doneCh := make(chan struct{})
					var closeDoneOnce sync.Once
					endDone := func() { closeDoneOnce.Do(func() { close(doneCh) }) }

					_ = e.runOnLoop(ctx, func() error {
						pushCb := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, cargs ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
							if len(cargs) > 0 && cargs[0] != nil {
								if d, err := eqjsDumpPlain(e.vm, cargs[0]); err == nil {
									if raw, merr := jsonx.Marshal(d); merr == nil {
										if ev, derr := ai.UnmarshalAssistantMessageEvent(raw); derr == nil && ev != nil {
											outStream.Push(ev)
										}
									}
								}
							}
							return e.vm.Undefined(), nil
						})
						doneCb := e.newCallbackHandle(func(_ *quickjs.JSValueHandle, cargs ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
							var finalMsg *ai.AssistantMessage
							var streamErr error
							if len(cargs) > 1 && cargs[1] != nil && !cargs[1].IsNull() && !cargs[1].IsUndefined() {
								streamErr = eqjsExtractHandleError(e.vm, cargs[1])
							}
							if len(cargs) > 0 && cargs[0] != nil && !cargs[0].IsNull() && !cargs[0].IsUndefined() {
								if d, err := eqjsDumpPlain(e.vm, cargs[0]); err == nil {
									raw, _ := jsonx.Marshal(d)
									var am ai.AssistantMessage
									if jsonx.Unmarshal(raw, &am) == nil {
										finalMsg = &am
									}
								}
							}
							if streamErr != nil {
								errMsg := &ai.AssistantMessage{
									Role:         "assistant",
									StopReason:   ai.StopReasonError,
									ErrorMessage: eqjsStrPtr(streamErr.Error()),
									Timestamp:    time.Now().UnixMilli(),
								}
								if model != nil {
									errMsg.Api = ai.Api(model.Api)
									errMsg.Provider = model.Provider
									errMsg.Model = model.Id
								}
								outStream.Push(&ai.AssistantMessageEventError{
									Type:   "error",
									Reason: ai.StopReasonError,
									Error:  errMsg,
								})
								outStream.End(errMsg)
							} else if finalMsg != nil {
								outStream.End(finalMsg)
							} else {
								outStream.End()
							}
							endDone()
							return e.vm.Undefined(), nil
						})

						aModel := eqjsToJS(e.vm, model)
						aTx := eqjsToJS(e.vm, transcript)
						aOpts := eqjsToJS(e.vm, options)
						res, err := e.vm.CallFunction(ssFn, e.vm.Undefined(), aModel, aTx, aOpts)
						aModel.Dispose()
						aTx.Dispose()
						aOpts.Dispose()
						if err != nil {
							pushCb.Dispose()
							doneCb.Dispose()
							if res != nil {
								res.Dispose()
							}
							errStr := eqjsFormatJSError(e.vm, err).Error()
							outStream.End(&ai.AssistantMessage{
								Role:         "assistant",
								StopReason:   ai.StopReasonError,
								ErrorMessage: &errStr,
								Timestamp:    time.Now().UnixMilli(),
							})
							endDone()
							return nil
						}
						consumeFn := e.vm.GetGlobal().GetProp("__piConsumeStream")
						cr, cerr := e.vm.CallFunction(consumeFn, e.vm.Undefined(), res, pushCb, doneCb)
						consumeFn.Dispose()
						pushCb.Dispose()
						doneCb.Dispose()
						if res != nil {
							res.Dispose()
						}
						if cr != nil {
							cr.Dispose()
						}
						_, _ = e.vm.ExecutePendingJobs()
						if cerr != nil {
							errStr := eqjsFormatJSError(e.vm, cerr).Error()
							outStream.End(&ai.AssistantMessage{
								Role:         "assistant",
								StopReason:   ai.StopReasonError,
								ErrorMessage: &errStr,
								Timestamp:    time.Now().UnixMilli(),
							})
							endDone()
						}
						return nil
					})
					select {
					case <-doneCh:
					case <-ctx.Done():
						errStr := ctx.Err().Error()
						outStream.End(&ai.AssistantMessage{
							Role:         "assistant",
							StopReason:   ai.StopReasonAborted,
							ErrorMessage: &errStr,
							Timestamp:    time.Now().UnixMilli(),
						})
					case <-e.closedCh:
						outStream.End()
					}
				}()
				return outStream
			}
		}
		p.Dispose()
	}
	if p := cfgObj.GetProp("oauth"); p != nil {
		if p.IsObject() {
			cfg.OAuth = e.buildExtensionOAuthConfig(p)
		}
		p.Dispose()
	}
	return cfg, nil
}

func (e *eqjsVM) buildExtensionOAuthConfig(oauthObj *quickjs.JSValueHandle) *ExtensionOAuthConfig {
	out := &ExtensionOAuthConfig{}
	if p := oauthObj.GetProp("name"); p != nil {
		if p.IsString() {
			out.Name = p.ToString()
		}
		p.Dispose()
	}
	if p := oauthObj.GetProp("isSubscription"); p != nil {
		if p.IsBool() {
			out.IsSubscription = p.ToBoolean()
		}
		p.Dispose()
	}
	if p := oauthObj.GetProp("usesCallbackServer"); p != nil {
		if p.IsBool() {
			out.UsesCallbackServer = p.ToBoolean()
		}
		p.Dispose()
	}
	retainedSelf := e.retain(oauthObj)

	if p := oauthObj.GetProp("login"); p != nil {
		if p.IsFunction() {
			loginFn := e.retain(p)
			out.Login = func(ctx context.Context, callbacks compat.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
				callRes, err := e.callAwaitInternal(ctx, "oauth.login", loginFn, retainedSelf, false, func(signalID int64) []*quickjs.JSValueHandle {
					return []*quickjs.JSValueHandle{e.makeOAuthCallbacksJS(ctx, callbacks, signalID)}
				})
				if err != nil {
					return ai.OAuthCredentials{}, err
				}
				return eqjsDecodeOAuthCredentials(callRes.val)
			}
		}
		p.Dispose()
	}
	if p := oauthObj.GetProp("refreshToken"); p != nil {
		if p.IsFunction() {
			refreshFn := e.retain(p)
			out.RefreshToken = func(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error) {
				callRes, err := e.callAwaitInternal(ctx, "oauth.refreshToken", refreshFn, retainedSelf, false, func(_ int64) []*quickjs.JSValueHandle {
					return []*quickjs.JSValueHandle{eqjsToJS(e.vm, credentials)}
				})
				if err != nil {
					return ai.OAuthCredentials{}, err
				}
				return eqjsDecodeOAuthCredentials(callRes.val)
			}
		}
		p.Dispose()
	}
	if p := oauthObj.GetProp("getApiKey"); p != nil {
		if p.IsFunction() {
			keyFn := e.retain(p)
			out.GetApiKey = func(credentials ai.OAuthCredentials) string {
				var apiKey string
				_ = e.runOnLoop(context.Background(), func() error {
					arg := eqjsToJS(e.vm, credentials)
					res, err := e.vm.CallFunction(keyFn, retainedSelf, arg)
					arg.Dispose()
					if err != nil {
						return err
					}
					if res != nil {
						if !res.IsNull() && !res.IsUndefined() {
							apiKey = res.ToString()
						}
						res.Dispose()
					}
					return nil
				})
				return apiKey
			}
		}
		p.Dispose()
	}
	if p := oauthObj.GetProp("modifyModels"); p != nil {
		if p.IsFunction() {
			modFn := e.retain(p)
			out.ModifyModels = func(models []*ai.Model, credentials ai.OAuthCredentials) []*ai.Model {
				result := models
				_ = e.runOnLoop(context.Background(), func() error {
					mArg := eqjsToJS(e.vm, models)
					cArg := eqjsToJS(e.vm, credentials)
					res, err := e.vm.CallFunction(modFn, retainedSelf, mArg, cArg)
					mArg.Dispose()
					cArg.Dispose()
					if err != nil {
						return err
					}
					if res != nil {
						if d, derr := eqjsDumpPlain(e.vm, res); derr == nil {
							if raw, merr := jsonx.Marshal(d); merr == nil {
								var decoded []*ai.Model
								if jsonx.Unmarshal(raw, &decoded) == nil && decoded != nil {
									result = decoded
								}
							}
						}
						res.Dispose()
					}
					return nil
				})
				return result
			}
		}
		p.Dispose()
	}
	return out
}

func (e *eqjsVM) makeOAuthCallbacksJS(ctx context.Context, callbacks compat.OAuthLoginCallbacks, signalID int64) *quickjs.JSValueHandle {
	cbObj := e.vm.NewObject()
	if signalID != 0 {
		ctxHolder := e.makeContextJS(0, signalID, false, false)
		sig := ctxHolder.GetProp("signal")
		ctxHolder.Dispose()
		cbObj.SetProp("signal", sig)
		sig.Dispose()
	}
	e.setHostMethod(cbObj, "onAuth", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if callbacks != nil && len(args) > 0 && args[0] != nil {
			if d, err := eqjsDumpPlain(e.vm, args[0]); err == nil {
				if raw, merr := jsonx.Marshal(d); merr == nil {
					var info compat.OAuthAuthInfo
					if jsonx.Unmarshal(raw, &info) == nil {
						callbacks.OnAuth(&info)
					}
				}
			}
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(cbObj, "onDeviceCode", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if callbacks != nil && len(args) > 0 && args[0] != nil {
			if d, err := eqjsDumpPlain(e.vm, args[0]); err == nil {
				if raw, merr := jsonx.Marshal(d); merr == nil {
					var info compat.OAuthDeviceCodeInfo
					if jsonx.Unmarshal(raw, &info) == nil {
						callbacks.OnDeviceCode(&info)
					}
				}
			}
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(cbObj, "onProgress", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if callbacks != nil && len(args) > 0 && args[0] != nil {
			callbacks.OnProgress(args[0].ToString())
		}
		return e.vm.Undefined(), nil
	})
	e.setHostMethod(cbObj, "onPrompt", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		deferred := e.vm.NewPromise()
		var prompt compat.OAuthPrompt
		if len(args) > 0 && args[0] != nil {
			if d, err := eqjsDumpPlain(e.vm, args[0]); err == nil {
				if raw, merr := jsonx.Marshal(d); merr == nil {
					_ = jsonx.Unmarshal(raw, &prompt)
				}
			}
		}
		go func() {
			if callbacks == nil {
				eqjsRejectDeferred(e, deferred, errors.New("oauth onPrompt callback not provided"))
				return
			}
			val, err := callbacks.OnPrompt(ctx, &prompt)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, val)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(cbObj, "onManualCodeInput", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		deferred := e.vm.NewPromise()
		go func() {
			if callbacks == nil {
				eqjsRejectDeferred(e, deferred, errors.New("oauth onManualCodeInput callback not provided"))
				return
			}
			val, err := callbacks.OnManualCodeInput(ctx)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, val)
		}()
		return deferred.Handle, nil
	})
	e.setHostMethod(cbObj, "onSelect", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		deferred := e.vm.NewPromise()
		var prompt compat.OAuthSelectPrompt
		if len(args) > 0 && args[0] != nil {
			if d, err := eqjsDumpPlain(e.vm, args[0]); err == nil {
				if raw, merr := jsonx.Marshal(d); merr == nil {
					_ = jsonx.Unmarshal(raw, &prompt)
				}
			}
		}
		go func() {
			if callbacks == nil {
				eqjsRejectDeferred(e, deferred, errors.New("oauth onSelect callback not provided"))
				return
			}
			val, err := callbacks.OnSelect(ctx, &prompt)
			if err != nil {
				eqjsRejectDeferred(e, deferred, err)
				return
			}
			eqjsResolveDeferred(e, deferred, val)
		}()
		return deferred.Handle, nil
	})
	return cbObj
}

func eqjsDecodeOAuthCredentials(val any) (ai.OAuthCredentials, error) {
	m, ok := val.(map[string]any)
	if !ok {
		return ai.OAuthCredentials{}, errors.New("oauth handler must return credentials object")
	}
	raw, err := jsonx.Marshal(m)
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var cred ai.OAuthCredentials
	if err := jsonx.Unmarshal(raw, &cred); err != nil {
		return ai.OAuthCredentials{}, err
	}
	extra := jsonx.NewObject()
	for _, k := range erSortedKeys(m) {
		if k == "refresh" || k == "access" || k == "expires" {
			continue
		}
		extra.Set(k, m[k])
	}
	if extra.Len() > 0 {
		cred.Extra = extra
	}
	return cred, nil
}

func (e *eqjsVM) buildVirtualModelDef(obj *quickjs.JSValueHandle) VirtualModelDefinition {
	var def VirtualModelDefinition
	if d, err := eqjsDumpPlain(e.vm, obj); err == nil {
		raw, _ := jsonx.Marshal(d)
		_ = jsonx.Unmarshal(raw, &def)
	}
	if rProp := obj.GetProp("route"); rProp != nil {
		if rProp.IsFunction() {
			rFn := e.retain(rProp)
			def.Route = func(ctx context.Context, req ModelRouteRequest) (ModelRoute, error) {
				res, err := e.callFnAwait(ctx, "virtualModel.route", rFn, nil, func(_ int64) []*quickjs.JSValueHandle {
					return []*quickjs.JSValueHandle{eqjsToJS(e.vm, req)}
				})
				if err != nil {
					return ModelRoute{}, err
				}
				var route ModelRoute
				raw, _ := jsonx.Marshal(res.val)
				if err := jsonx.Unmarshal(raw, &route); err != nil {
					return ModelRoute{}, err
				}
				return route, nil
			}
		}
		rProp.Dispose()
	}
	return def
}

func eqjsRegisterEvent[E any, R any](
	e *eqjsVM,
	register func(ExtensionHandler[E, R]) func(),
	eventName string,
	fnHandle *quickjs.JSValueHandle,
	parseResult func(res any, mutatedEvent map[string]any, origEvent *E) (*R, error),
) func() {
	return register(func(ctx context.Context, event *E, ectx ExtensionContext) (*R, error) {
		out, err := e.callEventAwait(ctx, "on:"+eventName, fnHandle, func(signalID int64) []*quickjs.JSValueHandle {
			ctxID := e.allocCtx(ectx, nil, ctx)
			return []*quickjs.JSValueHandle{
				eqjsEventToJS(e.vm, eventName, event),
				e.makeContextJS(ctxID, signalID, false, false),
			}
		})
		if err != nil {
			return nil, err
		}
		if parseResult == nil {
			return nil, nil
		}
		return parseResult(out.val, out.event, event)
	})
}

func (e *eqjsVM) registerEventHandler(eventName string, fnHandle *quickjs.JSValueHandle) (func(), error) {
	switch eventName {
	case "project_trust":
		return e.api.OnProjectTrust(func(event ProjectTrustEvent, pctx ProjectTrustContext) (ProjectTrustEventResult, error) {
			out, err := e.callEventAwait(context.Background(), "on:project_trust", fnHandle, func(signalID int64) []*quickjs.JSValueHandle {
				ctxCopy := pctx
				ctxID := e.allocCtx(nil, &ctxCopy, context.Background())
				return []*quickjs.JSValueHandle{
					eqjsToJS(e.vm, event),
					e.makeContextJS(ctxID, signalID, false, false),
				}
			})
			if err != nil || out.val == nil {
				return ProjectTrustEventResult{}, err
			}
			var r ProjectTrustEventResult
			raw, _ := jsonx.Marshal(out.val)
			if err := jsonx.Unmarshal(raw, &r); err != nil {
				return ProjectTrustEventResult{}, err
			}
			return r, nil
		}), nil
	case "resources_discover":
		return eqjsRegisterEvent(e, e.api.OnResourcesDiscover, eventName, fnHandle, eqjsParseJSONResult[ResourcesDiscoverEvent, ResourcesDiscoverResult]), nil
	case "session_start":
		return eqjsRegisterEvent(e, e.api.OnSessionStart, eventName, fnHandle, nil), nil
	case "session_info_changed":
		return eqjsRegisterEvent(e, e.api.OnSessionInfoChanged, eventName, fnHandle, nil), nil
	case "session_before_switch":
		return eqjsRegisterEvent(e, e.api.OnSessionBeforeSwitch, eventName, fnHandle, eqjsParseJSONResult[SessionBeforeSwitchEvent, SessionBeforeSwitchResult]), nil
	case "session_before_fork":
		return eqjsRegisterEvent(e, e.api.OnSessionBeforeFork, eventName, fnHandle, eqjsParseJSONResult[SessionBeforeForkEvent, SessionBeforeForkResult]), nil
	case "session_before_compact":
		return eqjsRegisterEvent(e, e.api.OnSessionBeforeCompact, eventName, fnHandle, eqjsParseJSONResult[SessionBeforeCompactEvent, SessionBeforeCompactResult]), nil
	case "session_compact":
		return eqjsRegisterEvent(e, e.api.OnSessionCompact, eventName, fnHandle, nil), nil
	case "session_compact_failed":
		return eqjsRegisterEvent(e, e.api.OnSessionCompactFailed, eventName, fnHandle, nil), nil
	case "session_shutdown":
		return eqjsRegisterEvent(e, e.api.OnSessionShutdown, eventName, fnHandle, nil), nil
	case "mcp_servers_change":
		return eqjsRegisterEvent(e, e.api.OnMcpServersChange, eventName, fnHandle, nil), nil
	case "session_before_tree":
		return eqjsRegisterEvent(e, e.api.OnSessionBeforeTree, eventName, fnHandle, eqjsParseJSONResult[SessionBeforeTreeEvent, SessionBeforeTreeResult]), nil
	case "session_tree":
		return eqjsRegisterEvent(e, e.api.OnSessionTree, eventName, fnHandle, nil), nil
	case "context":
		return eqjsRegisterEvent(e, e.api.OnContext, eventName, fnHandle, eqjsParseContextResult[ContextEvent]), nil
	case "context_with_system":
		return eqjsRegisterEvent(e, e.api.OnContextWithSystem, eventName, fnHandle, eqjsParseContextResult[ContextWithSystemEvent]), nil
	case "cache_warming_decision":
		return eqjsRegisterEvent(e, e.api.OnCacheWarmingDecision, eventName, fnHandle, eqjsParseJSONResult[CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult]), nil
	case "before_provider_request":
		return eqjsRegisterEvent(e, e.api.OnBeforeProviderRequest, eventName, fnHandle, func(res any, _ map[string]any, _ *BeforeProviderRequestEvent) (*any, error) {
			if res == nil {
				return nil, nil
			}
			return &res, nil
		}), nil
	case "before_provider_headers":
		return eqjsRegisterEvent(e, e.api.OnBeforeProviderHeaders, eventName, fnHandle, func(res any, mutated map[string]any, orig *BeforeProviderHeadersEvent) (*NoResult, error) {
			if orig == nil || orig.Headers == nil {
				return nil, nil
			}
			var hdrMap map[string]any
			if m, ok := res.(map[string]any); ok {
				if h, ok := m["headers"].(map[string]any); ok {
					hdrMap = h
				}
			}
			if hdrMap == nil && mutated != nil {
				hdrMap, _ = mutated["headers"].(map[string]any)
			}
			if hdrMap != nil {
				eqjsSyncHeadersMap(orig.Headers, hdrMap)
			}
			return nil, nil
		}), nil
	case "after_provider_response":
		return eqjsRegisterEvent(e, e.api.OnAfterProviderResponse, eventName, fnHandle, nil), nil
	case "provider_stream_event":
		return eqjsRegisterEvent(e, e.api.OnProviderStreamEvent, eventName, fnHandle, nil), nil
	case "before_agent_start":
		return eqjsRegisterEvent(e, e.api.OnBeforeAgentStart, eventName, fnHandle, eqjsParseJSONResult[BeforeAgentStartEvent, BeforeAgentStartEventResult]), nil
	case "agent_start":
		return eqjsRegisterEvent(e, e.api.OnAgentStart, eventName, fnHandle, nil), nil
	case "agent_end":
		return eqjsRegisterEvent(e, e.api.OnAgentEnd, eventName, fnHandle, nil), nil
	case "agent_before_settle":
		return eqjsRegisterEvent(e, e.api.OnAgentBeforeSettle, eventName, fnHandle, eqjsParseBoundaryResult[AgentBeforeSettleEvent]), nil
	case "agent_settled":
		return eqjsRegisterEvent(e, e.api.OnAgentSettled, eventName, fnHandle, nil), nil
	case "ui_prompt_start":
		return eqjsRegisterEvent(e, e.api.OnUIPromptStart, eventName, fnHandle, nil), nil
	case "ui_prompt_end":
		return eqjsRegisterEvent(e, e.api.OnUIPromptEnd, eventName, fnHandle, nil), nil
	case "turn_start":
		return eqjsRegisterEvent(e, e.api.OnTurnStart, eventName, fnHandle, nil), nil
	case "turn_end":
		return eqjsRegisterEvent(e, e.api.OnTurnEnd, eventName, fnHandle, eqjsParseBoundaryResult[TurnEndEvent]), nil
	case "message_start":
		return eqjsRegisterEvent(e, e.api.OnMessageStart, eventName, fnHandle, nil), nil
	case "message_update":
		return eqjsRegisterEvent(e, e.api.OnMessageUpdate, eventName, fnHandle, nil), nil
	case "message_end":
		return eqjsRegisterEvent(e, e.api.OnMessageEnd, eventName, fnHandle, func(res any, _ map[string]any, _ *MessageEndEvent) (*MessageEndEventResult, error) {
			m, ok := res.(map[string]any)
			if !ok {
				return nil, nil
			}
			rawMsg, has := m["message"]
			if !has || rawMsg == nil {
				return nil, nil
			}
			msg, err := eqjsDecodeAgentMessage(rawMsg)
			if err != nil {
				return nil, err
			}
			return &MessageEndEventResult{Message: msg}, nil
		}), nil
	case "tool_execution_start":
		return eqjsRegisterEvent(e, e.api.OnToolExecutionStart, eventName, fnHandle, nil), nil
	case "tool_execution_update":
		return eqjsRegisterEvent(e, e.api.OnToolExecutionUpdate, eventName, fnHandle, nil), nil
	case "tool_execution_end":
		return eqjsRegisterEvent(e, e.api.OnToolExecutionEnd, eventName, fnHandle, nil), nil
	case "model_select":
		return eqjsRegisterEvent(e, e.api.OnModelSelect, eventName, fnHandle, nil), nil
	case "thinking_level_select":
		return eqjsRegisterEvent(e, e.api.OnThinkingLevelSelect, eventName, fnHandle, nil), nil
	case "tool_call":
		return eqjsRegisterEvent(e, e.api.OnToolCall, eventName, fnHandle, func(res any, mutated map[string]any, orig *ToolCallEvent) (*ToolCallEventResult, error) {
			if orig != nil && mutated != nil {
				if inp, ok := mutated["input"]; ok {
					eqjsSyncJSONObject(&orig.Input, inp)
				}
			}
			return eqjsParseJSONResult[ToolCallEvent, ToolCallEventResult](res, mutated, orig)
		}), nil
	case "tool_result":
		return eqjsRegisterEvent(e, e.api.OnToolResult, eventName, fnHandle, func(res any, _ map[string]any, _ *ToolResultEvent) (*ToolResultEventResult, error) {
			if res == nil {
				return nil, nil
			}
			raw, err := jsonx.Marshal(res)
			if err != nil {
				return nil, err
			}
			var r ToolResultEventResult
			if err := jsonx.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			return &r, nil
		}), nil
	case "user_bash":
		return eqjsRegisterEvent(e, e.api.OnUserBash, eventName, fnHandle, eqjsParseJSONResult[UserBashEvent, UserBashEventResult]), nil
	case "input":
		return eqjsRegisterEvent(e, e.api.OnInput, eventName, fnHandle, eqjsParseJSONResult[InputEvent, InputEventResult]), nil
	default:
		return nil, fmt.Errorf("unknown extension event %q", eventName)
	}
}

type eqjsComponent struct {
	evm *eqjsVM
	obj *quickjs.JSValueHandle
}

func (c *eqjsComponent) Render(width int) []string {
	if c == nil || c.obj == nil || c.evm == nil || c.evm.isClosed() {
		return nil
	}
	var lines []string
	_ = c.evm.runOnLoop(c.evm.vmCtx, func() error {
		fn := c.obj.GetProp("render")
		defer fn.Dispose()
		if !fn.IsFunction() {
			return nil
		}
		wH := c.evm.vm.NewNumber(float64(width))
		res, err := c.evm.vm.CallFunction(fn, c.obj, wH)
		wH.Dispose()
		if res != nil {
			defer res.Dispose()
		}
		if err != nil {
			return err
		}
		d, derr := eqjsDumpPlain(c.evm.vm, res)
		if derr != nil {
			return derr
		}
		if arr, ok := d.([]any); ok {
			lines = make([]string, len(arr))
			for i, x := range arr {
				lines[i] = fmt.Sprint(x)
			}
		}
		return nil
	})
	return lines
}

func (c *eqjsComponent) Invalidate() {
	if c == nil || c.obj == nil || c.evm == nil || c.evm.isClosed() {
		return
	}
	_ = c.evm.runOnLoop(c.evm.vmCtx, func() error {
		fn := c.obj.GetProp("invalidate")
		defer fn.Dispose()
		if fn.IsFunction() {
			r, _ := c.evm.vm.CallFunction(fn, c.obj)
			if r != nil {
				r.Dispose()
			}
		}
		return nil
	})
}

func (c *eqjsComponent) HandleInput(data string) {
	if c == nil || c.obj == nil || c.evm == nil || c.evm.isClosed() {
		return
	}
	_ = c.evm.runOnLoop(c.evm.vmCtx, func() error {
		fn := c.obj.GetProp("handleInput")
		defer fn.Dispose()
		if fn.IsFunction() {
			dH := c.evm.vm.NewString(data)
			r, _ := c.evm.vm.CallFunction(fn, c.obj, dH)
			dH.Dispose()
			if r != nil {
				r.Dispose()
			}
			_, _ = c.evm.vm.ExecutePendingJobs()
		}
		return nil
	})
}

func (c *eqjsComponent) Dispose() {
	if c == nil || c.obj == nil || c.evm == nil || c.evm.isClosed() {
		return
	}
	_ = c.evm.runOnLoop(c.evm.vmCtx, func() error {
		fn := c.obj.GetProp("dispose")
		defer fn.Dispose()
		if fn.IsFunction() {
			r, _ := c.evm.vm.CallFunction(fn, c.obj)
			if r != nil {
				r.Dispose()
			}
			_, _ = c.evm.vm.ExecutePendingJobs()
		}
		return nil
	})
}

func (e *eqjsVM) eqjsNewTuiShim(t tui.TUI) *quickjs.JSValueHandle {
	tuiShim := e.vm.NewObject()
	e.setHostMethod(tuiShim, "requestRender", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if t != nil {
			var forcePtr *bool
			if len(args) > 0 && args[0] != nil && args[0].ToBoolean() {
				f := true
				forcePtr = &f
			}
			t.RequestRender(forcePtr)
		}
		return e.vm.Undefined(), nil
	})
	return tuiShim
}

func (e *eqjsVM) eqjsBuildFooterDataJS(footerData ReadonlyFooterDataProvider) *quickjs.JSValueHandle {
	fdObj := e.vm.NewObject()
	e.setHostMethod(fdObj, "getGitBranch", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if footerData == nil {
			return e.vm.Null(), nil
		}
		return eqjsToJS(e.vm, footerData.GetGitBranch()), nil
	})
	e.setHostMethod(fdObj, "getExtensionStatuses", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		var statuses map[string]string
		if footerData != nil {
			statuses = footerData.GetExtensionStatuses()
		}
		keys := make([]string, 0, len(statuses))
		for k := range statuses {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]any, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, []any{k, statuses[k]})
		}
		g := e.vm.GetGlobal()
		if g == nil {
			return e.vm.Undefined(), nil
		}
		makeMapFn := g.GetProp("__piMakeMap")
		g.Dispose()
		defer makeMapFn.Dispose()
		if !makeMapFn.IsFunction() {
			return e.vm.Undefined(), nil
		}
		pairsHandle := eqjsToJS(e.vm, pairs)
		defer pairsHandle.Dispose()
		res, err := e.vm.CallFunction(makeMapFn, e.vm.Undefined(), pairsHandle)
		if err != nil {
			if res != nil {
				res.Dispose()
			}
			return nil, err
		}
		return res, nil
	})
	e.setHostMethod(fdObj, "getAvailableProviderCount", func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if footerData == nil {
			return e.vm.NewNumber(0), nil
		}
		return e.vm.NewNumber(float64(footerData.GetAvailableProviderCount())), nil
	})
	e.setHostMethod(fdObj, "onBranchChange", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if footerData == nil || len(args) == 0 || args[0] == nil || !args[0].IsFunction() {
			return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
				return e.vm.Undefined(), nil
			}), nil
		}
		cbHandle := e.retain(args[0])
		var unsubscribed atomic.Bool
		var unsubOnce sync.Once
		rawUnsub := footerData.OnBranchChange(func() {
			if e.isClosed() || unsubscribed.Load() {
				return
			}
			e.postToLoop(func() {
				if e.isClosed() || unsubscribed.Load() {
					return
				}
				r, _ := e.vm.CallFunction(cbHandle, e.vm.Undefined())
				if r != nil {
					r.Dispose()
				}
				_, _ = e.vm.ExecutePendingJobs()
			})
		})
		unsubFn := func() {
			unsubOnce.Do(func() {
				unsubscribed.Store(true)
				if rawUnsub != nil {
					rawUnsub()
				}
			})
		}
		e.eventUnsubMu.Lock()
		e.eventUnsubs = append(e.eventUnsubs, unsubFn)
		e.eventUnsubMu.Unlock()
		return e.newCallbackHandle(func(_ *quickjs.JSValueHandle, _ ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
			unsubFn()
			return e.vm.Undefined(), nil
		}), nil
	})
	return fdObj
}

func (e *eqjsVM) callComponentRenderer(_ string, fn *quickjs.JSValueHandle, arg0 any, arg1 any) tui.Component {
	var compHandle *quickjs.JSValueHandle
	_ = e.runOnLoop(e.vmCtx, func() error {
		thVal := e.vm.GetGlobal().GetProp("__piTheme")
		defer thVal.Dispose()
		a0 := eqjsToJS(e.vm, arg0)
		a1 := eqjsToJS(e.vm, arg1)
		defer a0.Dispose()
		defer a1.Dispose()
		r, err := e.vm.CallFunction(fn, e.vm.Undefined(), a0, a1, thVal)
		if err == nil && r != nil && r.IsObject() {
			compHandle = e.retain(r)
		}
		if r != nil {
			r.Dispose()
		}
		return nil
	})
	return &eqjsComponent{evm: e, obj: compHandle}
}

func (e *eqjsVM) callComponentFactory(_ string, fn *quickjs.JSValueHandle, t tui.TUI, includeFooterData bool, footerData ReadonlyFooterDataProvider) tui.Component {
	var compHandle *quickjs.JSValueHandle
	_ = e.runOnLoop(e.vmCtx, func() error {
		tuiShim := e.eqjsNewTuiShim(t)
		defer tuiShim.Dispose()
		var thVal *quickjs.JSValueHandle
		if g := e.vm.GetGlobal(); g != nil {
			thVal = g.GetProp("__piTheme")
			g.Dispose()
		} else {
			thVal = e.vm.Undefined()
		}
		defer thVal.Dispose()
		args := []*quickjs.JSValueHandle{tuiShim, thVal}
		if includeFooterData {
			fdObj := e.eqjsBuildFooterDataJS(footerData)
			defer fdObj.Dispose()
			args = append(args, fdObj)
		}
		r, err := e.vm.CallFunction(fn, e.vm.Undefined(), args...)
		if err == nil && r != nil && r.IsObject() {
			compHandle = e.retain(r)
		}
		if r != nil {
			r.Dispose()
		}
		return nil
	})
	return &eqjsComponent{evm: e, obj: compHandle}
}

// --- JS<->Go Helpers ---

func eqjsBool(vm *quickjs.QuickJS, b bool) *quickjs.JSValueHandle {
	if b {
		return vm.True()
	}
	return vm.False()
}

func eqjsPlainJSONValue(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		if x == nil {
			return nil
		}
		m := make(map[string]any, x.Len())
		for k, val := range x.All() {
			m[k] = eqjsPlainJSONValue(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = eqjsPlainJSONValue(item)
		}
		return out
	default:
		return v
	}
}

func eqjsToJS(vm *quickjs.QuickJS, v any) *quickjs.JSValueHandle {
	switch x := v.(type) {
	case nil:
		return vm.Null()
	case bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, string, []byte, []any, map[string]any:
		return vm.HostToHandle(x)
	case *jsonx.Object:
		if x == nil {
			return vm.Null()
		}
		return vm.HostToHandle(eqjsPlainJSONValue(x))
	case []string:
		arr := make([]any, len(x))
		for i, s := range x {
			arr[i] = s
		}
		return vm.HostToHandle(arr)
	case *string:
		if x == nil {
			return vm.Null()
		}
		return vm.NewString(*x)
	default:
		encoded, err := jsonx.Encode(v)
		if err != nil {
			return vm.Undefined()
		}
		return vm.HostToHandle(eqjsPlainJSONValue(encoded))
	}
}

func eqjsEventToJS[E any](vm *quickjs.QuickJS, eventName string, event *E) *quickjs.JSValueHandle {
	if h, ok := any(event).(*BeforeProviderHeadersEvent); ok && h != nil {
		hdrs := make(map[string]any)
		if h.Headers != nil {
			for k, v := range h.Headers.All() {
				if v != nil {
					hdrs[k] = *v
				}
			}
		}
		return eqjsToJS(vm, map[string]any{
			"type":    eventName,
			"headers": hdrs,
		})
	}
	return eqjsToJS(vm, event)
}

func eqjsSyncHeadersMap(dst *omap.Map[string, *string], src map[string]any) {
	for _, k := range dst.Keys() {
		if _, exists := src[k]; !exists {
			dst.Delete(k)
		}
	}
	for _, k := range erSortedKeys(src) {
		v := src[k]
		if v == nil {
			dst.Delete(k)
			continue
		}
		if s, ok := v.(string); ok {
			val := s
			dst.Set(k, &val)
		}
	}
}

func eqjsSyncJSONObject(dst **jsonx.Object, rawInput any) {
	raw, err := jsonx.Marshal(rawInput)
	if err != nil {
		return
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return
	}
	if obj, ok := parsed.(*jsonx.Object); ok {
		if *dst != nil {
			cur := *dst
			for _, k := range cur.Keys() {
				if !obj.Has(k) {
					cur.Delete(k)
				}
			}
			for k, v := range obj.All() {
				cur.Set(k, v)
			}
			return
		}
		*dst = obj
	}
}

func eqjsParseJSONResult[E any, R any](res any, _ map[string]any, _ *E) (*R, error) {
	if res == nil {
		return nil, nil
	}
	raw, err := jsonx.Marshal(res)
	if err != nil {
		return nil, err
	}
	var out R
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func eqjsParseContextResult[E any](res any, _ map[string]any, _ *E) (*ContextEventResult, error) {
	m, ok := res.(map[string]any)
	if !ok {
		return nil, nil
	}
	rawMsgs, has := m["messages"]
	if !has || rawMsgs == nil {
		return nil, nil
	}
	arr, ok := rawMsgs.([]any)
	if !ok {
		return nil, nil
	}
	msgs := make([]agent.AgentMessage, 0, len(arr))
	for _, item := range arr {
		decoded, err := eqjsDecodeAgentMessage(item)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, decoded)
	}
	return &ContextEventResult{Messages: msgs}, nil
}

func eqjsParseBoundaryResult[E any](res any, _ map[string]any, _ *E) (*BoundaryResult, error) {
	m, ok := res.(map[string]any)
	if !ok {
		return nil, nil
	}
	out := &BoundaryResult{}
	if cont, ok := m["continue"].(bool); ok {
		out.Continue = &cont
	}
	if entries, ok := m["entries"].([]any); ok {
		for _, item := range entries {
			if draft := eqjsDecodeBoundaryDraft(item); draft != nil {
				out.Entries = append(out.Entries, draft)
			}
		}
	}
	if len(out.Entries) == 0 && out.Continue == nil {
		return nil, nil
	}
	return out, nil
}

func eqjsDecodeBoundaryDraft(v any) SessionBoundaryDraft {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	raw, err := jsonx.Marshal(m)
	if err != nil {
		return nil
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "custom":
		var d CustomEntryDraft
		if jsonx.Unmarshal(raw, &d) == nil {
			return d
		}
	case "custom_message":
		var d CustomMessageEntryDraft
		if jsonx.Unmarshal(raw, &d) == nil {
			return d
		}
	case "context_edit":
		var d ContextEditEntryDraft
		if jsonx.Unmarshal(raw, &d) == nil {
			return d
		}
	case "compaction":
		var d CompactionEntryDraft
		if jsonx.Unmarshal(raw, &d) == nil {
			return d
		}
	}
	return nil
}

func eqjsDecodeAgentMessage(v any) (agent.AgentMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	if m, ok := v.(map[string]any); ok {
		if role, _ := m["role"].(string); role == "custom" {
			var cm CustomMessage
			if err := jsonx.Unmarshal(raw, &cm); err != nil {
				return nil, err
			}
			return &cm, nil
		}
	}
	return agent.UnmarshalAgentMessage(raw)
}

func eqjsDecodeToolResult(v any) (agent.AgentToolResult, error) {
	if v == nil {
		return agent.AgentToolResult{Content: ai.ContentList{}}, nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	var out agent.AgentToolResult
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return agent.AgentToolResult{}, err
	}
	if out.Content == nil {
		out.Content = ai.ContentList{}
	}
	return out, nil
}

func eqjsDecodeAutocompleteItems(v any) []tui.AutocompleteItem {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]tui.AutocompleteItem, 0, len(arr))
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		item := tui.AutocompleteItem{}
		item.Value, _ = m["value"].(string)
		item.Label, _ = m["label"].(string)
		if item.Label == "" {
			item.Label = item.Value
		}
		if d, ok := m["description"].(string); ok {
			item.Description = &d
		}
		out = append(out, item)
	}
	return out
}

func eqjsDecodeModel(v any) *ai.Model {
	if v == nil {
		return nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return nil
	}
	var m ai.Model
	if err := jsonx.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

func eqjsDecodeNativeProvider(v any) *ai.Provider {
	if v == nil {
		return nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return nil
	}
	var p ai.Provider
	if err := jsonx.Unmarshal(raw, &p); err != nil {
		return nil
	}
	return &p
}

func eqjsDecodeProviderModel(v any) ProviderModelConfig {
	if v == nil {
		return nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return nil
	}
	var cfg ProviderChatModelConfig
	if err := jsonx.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func eqjsDecodeMcpConfig(v any) (McpServerConfig, error) {
	if v == nil {
		return McpServerConfig{}, nil
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return McpServerConfig{}, err
	}
	var cfg McpServerConfig
	if err := jsonx.Unmarshal(raw, &cfg); err != nil {
		return McpServerConfig{}, err
	}
	return cfg, nil
}

func eqjsParseDialogOpts(vm *quickjs.QuickJS, args []*quickjs.JSValueHandle, idx int) *ExtensionUIDialogOptions {
	if len(args) <= idx || args[idx] == nil || args[idx].IsNull() || args[idx].IsUndefined() {
		return nil
	}
	d, err := eqjsDumpPlain(vm, args[idx])
	if err != nil {
		return nil
	}
	m, ok := d.(map[string]any)
	if !ok {
		return nil
	}
	if t, ok := eqjsAsInt64(m["timeout"]); ok {
		return &ExtensionUIDialogOptions{Timeout: &t}
	}
	return nil
}

func eqjsArgStrings(_ *quickjs.QuickJS, args []*quickjs.JSValueHandle, idx int) []string {
	if len(args) <= idx || args[idx] == nil {
		return nil
	}
	if args[idx].IsArray() {
		n := int(args[idx].Length())
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			item := args[idx].GetProp(strconv.Itoa(i))
			if item != nil {
				out = append(out, item.ToString())
				item.Dispose()
			}
		}
		return out
	}
	out := make([]string, 0, len(args)-idx)
	for i := idx; i < len(args); i++ {
		if args[i] != nil {
			out = append(out, args[i].ToString())
		}
	}
	return out
}

func eqjsAsInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	default:
		return 0, false
	}
}

func eqjsStrPtr(s string) *string { return &s }

func eqjsCallSafe(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	fn()
	return nil
}

func eqjsFormatJSError(vm *quickjs.QuickJS, err error) error {
	if err == nil {
		return nil
	}
	var jsErr *quickjs.JSException
	if errors.As(err, &jsErr) {
		msg := jsErr.Message()
		if jsErr.Name() != "" && msg != "" {
			msg = jsErr.Name() + ": " + msg
		} else if jsErr.Name() != "" {
			msg = jsErr.Name()
		}
		if st := jsErr.Stack(); st != nil && *st != "" {
			return fmt.Errorf("%s\n%s", msg, strings.TrimSpace(*st))
		}
		return errors.New(msg)
	}
	_ = vm
	return err
}

func eqjsExtractHandleError(_ *quickjs.QuickJS, h *quickjs.JSValueHandle) error {
	if h == nil || h.IsUndefined() || h.IsNull() {
		return errors.New("JS promise rejected")
	}
	if h.IsString() {
		return errors.New(h.ToString())
	}
	if h.IsObject() {
		var name, msg, stack string
		if n := h.GetProp("name"); n != nil {
			if n.IsString() {
				name = n.ToString()
			}
			n.Dispose()
		}
		if m := h.GetProp("message"); m != nil {
			if m.IsString() {
				msg = m.ToString()
			}
			m.Dispose()
		}
		if s := h.GetProp("stack"); s != nil {
			if s.IsString() {
				stack = strings.TrimSpace(s.ToString())
			}
			s.Dispose()
		}
		full := msg
		if name != "" && msg != "" {
			full = name + ": " + msg
		} else if name != "" {
			full = name
		}
		if full == "" {
			full = h.ToString()
		}
		if stack != "" {
			return fmt.Errorf("%s\n%s", full, stack)
		}
		return errors.New(full)
	}
	return errors.New(h.ToString())
}

// Keep sort import used in future helpers cleanly without unused errors.
var _ = sort.Strings
