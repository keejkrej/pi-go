// Ported from packages/codemode/src/runtime/worker.ts (pi v1.0.0).

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/quickjs"
)

// workerRun runs one script inside a fresh QuickJS VM and relays tool calls and output
// to the host. One VM per run. The host cancels ctx when the script settles, times out,
// or is aborted; cancelling ctx closes the wasm instance. data.Interrupt is also polled
// by the VM interrupt handler.
//
// toHost receives worker messages. toWorker delivers host results. The VM is not safe
// for concurrent use: settle runs on this goroutine, not on a second one.
// A VM failure outside the script is posted as WorkerCrashMessage.
func workerRun(ctx context.Context, data *WorkerData, toHost chan<- WorkerToHostMessage, toWorker <-chan *HostToWorkerMessage) {
	defer close(toHost)

	maxStack := quickjs.MaxStackSize
	opts := &quickjs.QuickJSOptions{
		MaxStackSize: &maxStack,
		Wasi:         workerDiscardOutput,
	}
	if data != nil {
		if data.Wasm != nil {
			opts.Wasm = data.Wasm
		}
		opts.MemoryLimit = data.MemoryLimitBytes
		if data.Interrupt != nil {
			opts.InterruptHandler = func() bool {
				return data.Interrupt.Load() != 0
			}
		}
	}

	vm, err := quickjs.QuickJSCreate(ctx, opts)
	if err != nil {
		if ctx.Err() == nil {
			workerCrash(toHost, err)
		}
		return
	}
	defer vm.Dispose()

	doneSent := false
	sendHost := func(msg WorkerToHostMessage) {
		select {
		case toHost <- msg:
		case <-ctx.Done():
		}
	}

	// Called from the prelude with primitives only.
	bridge, err := vm.NewFunction("bridge", func(_ *quickjs.JSValueHandle, args ...*quickjs.JSValueHandle) (*quickjs.JSValueHandle, error) {
		if len(args) == 0 {
			return vm.Undefined(), nil
		}
		kind := args[0].ToString()
		switch kind {
		case "call", "global":
			if len(args) < 3 {
				return vm.Undefined(), nil
			}
			id := int(args[1].ToNumber())
			name := args[2].ToString()
			var callArgs *string
			if len(args) > 3 && !args[3].IsUndefined() {
				s := args[3].ToString()
				callArgs = &s
			}
			target := WorkerCallTargetTool
			if kind == "global" {
				target = WorkerCallTargetGlobal
			}
			sendHost(&WorkerCallMessage{
				Type:   "call",
				Id:     id,
				Target: target,
				Name:   name,
				Args:   callArgs,
			})
		case "output":
			if len(args) < 3 {
				return vm.Undefined(), nil
			}
			var item codemode.CodemodeOutputItem
			if args[1].ToString() == "image" && len(args) >= 4 {
				item = &codemode.CodemodeImageOutput{
					Type:     "image",
					Data:     args[2].ToString(),
					MimeType: args[3].ToString(),
				}
			} else {
				item = &codemode.CodemodeTextOutput{
					Type: "text",
					Text: args[2].ToString(),
				}
			}
			sendHost(&WorkerOutputMessage{
				Type: "output",
				Item: item,
			})
		case "done":
			doneSent = true
			if len(args) > 1 && args[1].ToBoolean() {
				var valuePtr *string
				if len(args) > 2 && !args[2].IsUndefined() {
					s := args[2].ToString()
					valuePtr = &s
				}
				writes := "[]"
				if len(args) > 3 && !args[3].IsUndefined() {
					writes = args[3].ToString()
				}
				sendHost(&WorkerDoneOkMessage{
					Type:   "done",
					Ok:     true,
					Value:  valuePtr,
					Writes: writes,
				})
			} else {
				errJson := ""
				if len(args) > 2 && !args[2].IsUndefined() {
					errJson = args[2].ToString()
				}
				sendHost(&WorkerDoneErrorMessage{
					Type:  "done",
					Ok:    false,
					Error: errJson,
				})
			}
		}
		return vm.Undefined(), nil
	})
	if err != nil {
		if ctx.Err() == nil && !doneSent {
			workerCrash(toHost, err)
		}
		return
	}
	defer bridge.Dispose()

	toolsData := data.Tools
	if toolsData == nil {
		toolsData = []WorkerDataTool{}
	}
	globalsData := data.Globals
	if globalsData == nil {
		globalsData = []WorkerDataGlobal{}
	}
	storeData := data.Store
	if storeData == nil {
		storeData = map[string]string{}
	}

	var api *quickjs.JSValueHandle
	err = vm.WithScope(func(scope *quickjs.HandleScope) error {
		preludeFn, err := vm.EvalCode(PreludeSource, "codemode-prelude.js", quickjs.EvalFlagsTypeGlobal)
		if err != nil {
			return err
		}
		toolsJSON, err := jsonx.Stringify(toolsData)
		if err != nil {
			return err
		}
		globalsJSON, err := jsonx.Stringify(globalsData)
		if err != nil {
			return err
		}
		storeJSON, err := jsonx.Stringify(storeData)
		if err != nil {
			return err
		}
		res, err := vm.CallFunction(
			preludeFn,
			vm.Undefined(),
			bridge,
			vm.NewString(toolsJSON),
			vm.NewString(globalsJSON),
			vm.NewString(storeJSON),
		)
		if err != nil {
			return err
		}
		api = scope.Escape(res)
		return nil
	})
	if err != nil {
		var jsErr *quickjs.JSException
		if errors.As(err, &jsErr) && jsErr.Handle != nil {
			jsErr.Handle.Dispose()
		}
		if ctx.Err() == nil && !doneSent {
			workerCrash(toHost, err)
		}
		return
	}
	defer api.Dispose()

	settle := api.GetProp("settle")
	defer settle.Dispose()
	run := api.GetProp("run")
	defer run.Dispose()
	stalled := api.GetProp("stalled")
	defer stalled.Dispose()

	drain := func() error {
		if _, err := vm.ExecutePendingJobs(); err != nil {
			return err
		}
		res, err := vm.CallFunction(stalled, api)
		if res != nil {
			res.Dispose()
		}
		return err
	}

	// The prefix shares the first line with the script so reported line numbers
	// match the script as written.
	fn, err := vm.EvalCode(fmt.Sprintf("(async (tools, console) => {%s\n})", data.Code), "codemode.js", quickjs.EvalFlagsTypeGlobal)
	if err != nil {
		var jsErr *quickjs.JSException
		if errors.As(err, &jsErr) {
			errJSON := workerDescribeException(jsErr)
			if jsErr.Handle != nil {
				jsErr.Handle.Dispose()
			}
			sendHost(&WorkerDoneErrorMessage{
				Type:  "done",
				Ok:    false,
				Error: errJSON,
			})
			return
		}
		if ctx.Err() == nil && !doneSent {
			workerCrash(toHost, err)
		}
		return
	}
	runRes, runErr := vm.CallFunction(run, api, fn)
	if runRes != nil {
		runRes.Dispose()
	}
	fn.Dispose()
	if runErr != nil {
		var jsErr *quickjs.JSException
		if errors.As(runErr, &jsErr) && jsErr.Handle != nil {
			jsErr.Handle.Dispose()
		}
		if ctx.Err() == nil && !doneSent {
			workerCrash(toHost, runErr)
		}
		return
	}
	if err := drain(); err != nil {
		if ctx.Err() == nil && !doneSent {
			workerCrash(toHost, err)
		}
		return
	}

	for !doneSent {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-toWorker:
			if !ok {
				return
			}
			if !IsHostToWorkerMessage(msg) {
				continue
			}
			err := vm.WithScope(func(_ *quickjs.HandleScope) error {
				okVal := vm.False()
				if msg.Ok {
					okVal = vm.True()
				}
				payloadVal := vm.Undefined()
				if msg.Payload != nil {
					payloadVal = vm.NewString(*msg.Payload)
				}
				_, err := vm.CallFunction(settle, api, vm.NewNumber(float64(msg.Id)), okVal, payloadVal)
				return err
			})
			if err != nil {
				var jsErr *quickjs.JSException
				if errors.As(err, &jsErr) && jsErr.Handle != nil {
					jsErr.Handle.Dispose()
				}
				if ctx.Err() == nil && !doneSent {
					workerCrash(toHost, err)
				}
				return
			}
			if err := drain(); err != nil {
				if ctx.Err() == nil && !doneSent {
					workerCrash(toHost, err)
				}
				return
			}
		}
	}
}

// workerDiscardOutput drops QuickJS engine diagnostics on fd 1 and 2.
// Reporting every byte as written keeps libc from retrying. It is the Wasi option
// passed to quickjs.QuickJSCreate.
func workerDiscardOutput(memory *quickjs.Memory) *quickjs.WasiImports {
	return &quickjs.WasiImports{
		FdWrite: func(_ uint32, iovsPtr, iovsLen, nwrittenPtr uint32) uint32 {
			mem := memory.API()
			if mem == nil {
				return 0
			}
			var written uint32
			for i := uint32(0); i < iovsLen; i++ {
				bufLen, ok := mem.ReadUint32Le(iovsPtr + i*8 + 4)
				if !ok {
					return 21 // EINVAL / EFAULT fallback
				}
				written += bufLen
			}
			mem.WriteUint32Le(nwrittenPtr, written)
			return 0
		},
	}
}

// workerDescribeException encodes a guest exception as ScriptErrorJson
// ({ name, message, stack }).
func workerDescribeException(err *quickjs.JSException) string {
	head := err.Name()
	if err.Message() != "" {
		head = err.Name() + ": " + err.Message()
	}
	fullStack := head
	if s := err.Stack(); s != nil {
		trimmed := js.TrimEnd(*s)
		if trimmed != "" {
			fullStack = head + "\n" + trimmed
		}
	}
	obj := jsonx.NewObject()
	obj.Set("name", err.Name())
	obj.Set("message", err.Message())
	obj.Set("stack", fullStack)
	encoded, _ := jsonx.Stringify(obj)
	return encoded
}

// workerCrash posts a crash message. err is formatted as "Name: message" when it has a name.
func workerCrash(toHost chan<- WorkerToHostMessage, err error) {
	msg := ""
	if err != nil {
		var named interface{ Name() string }
		if errors.As(err, &named) && named.Name() != "" {
			msg = named.Name() + ": " + err.Error()
		} else {
			msg = err.Error()
		}
	}
	toHost <- &WorkerCrashMessage{
		Type:    "crash",
		Message: msg,
	}
}
