// Ported from packages/codemode/src/runtime/host.ts (pi v1.0.0).

package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// hostDefaultTimeoutMs is the sandbox deadline when options omit timeoutMs.
const hostDefaultTimeoutMs int64 = 300_000

// hostIdentifier matches one JavaScript identifier segment.
var hostIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// hostReservedGlobals are names a configured global may not shadow.
var hostReservedGlobals = map[string]struct{}{
	"tools":      {},
	"ALL_TOOLS":  {},
	"console":    {},
	"text":       {},
	"image":      {},
	"exit":       {},
	"globalThis": {},
	"store":      {},
	"load":       {},
}

func hostErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func hostFormatTimeout(ms float64) string {
	return strconv.FormatFloat(ms, 'f', -1, 64)
}

func hostSerializeStore(store map[string]any) map[string]string {
	serialized := make(map[string]string)
	for key, value := range store {
		jsonText, err := jsonx.Stringify(value)
		if err == nil && jsonText != "" {
			serialized[key] = jsonText
		}
	}
	return serialized
}

func hostParseStoreWrites(jsonText string) (*codemode.CodemodeStoreWrites, error) {
	writes := &codemode.CodemodeStoreWrites{
		Set:    omap.NewMap[string, any](),
		Delete: []string{},
	}
	raw, err := jsonx.Parse([]byte(jsonText))
	if err != nil {
		return nil, err
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected storeWrites array, got %T", raw)
	}
	for i, item := range entries {
		pair, ok := item.([]any)
		if !ok || len(pair) == 0 {
			return nil, fmt.Errorf("storeWrites[%d]: expected entry tuple", i)
		}
		key, ok := pair[0].(string)
		if !ok {
			return nil, fmt.Errorf("storeWrites[%d][0]: expected string key", i)
		}
		if len(pair) == 1 || pair[1] == nil {
			writes.Delete = append(writes.Delete, key)
			continue
		}
		valJSON, ok := pair[1].(string)
		if !ok {
			return nil, fmt.Errorf("storeWrites[%d][1]: expected JSON string", i)
		}
		val, err := jsonx.Parse([]byte(valJSON))
		if err != nil {
			return nil, fmt.Errorf("storeWrites[%d][1]: %w", i, err)
		}
		writes.Set.Set(key, val)
	}
	return writes, nil
}

// hostPendingCall is one host call the script is waiting on.
type hostPendingCall struct {
	record    *codemode.CodemodeCall
	startedAt time.Time
	cancel    context.CancelFunc
}

// hostExecutionOptions is the input to one run.
type hostExecutionOptions struct {
	code             string
	tools            *omap.Map[string, *codemode.CodemodeTool]
	globals          *omap.Map[string, *codemode.CodemodeTool]
	timeoutMs        float64
	ctx              context.Context
	memoryLimitBytes *int
	store            map[string]string
	wasm             codemode.CodemodeWasmModule
	workerUrl        string
}

// hostExecution is one script run in its own QuickJS VM.
// A fresh VM per run keeps termination simple: a runaway script, including one that
// only spins the microtask queue, is cancelled and cannot poison a later run.
type hostExecution struct {
	// mu guards finished, output, calls/callRecords, pending, timer, abortStop, and workerStarted.
	// It is not held across tool callbacks or channel sends.
	mu            sync.Mutex
	result        chan codemode.CodemodeResult
	vmCtx         context.Context
	vmCancel      context.CancelFunc
	interrupt     atomic.Int32
	tools         *omap.Map[string, *codemode.CodemodeTool]
	globals       *omap.Map[string, *codemode.CodemodeTool]
	callerCtx     context.Context
	abortStop     func() bool
	timer         *time.Timer
	output        codemode.CodemodeOutputItemList
	calls         []codemode.CodemodeCall
	callRecords   []*codemode.CodemodeCall
	pending       *omap.Map[int, *hostPendingCall]
	toWorker      chan *HostToWorkerMessage
	finished      bool
	workerStarted bool
	workerDone    chan struct{}
}

func hostNewExecution(options *hostExecutionOptions) *hostExecution {
	vmCtx, vmCancel := context.WithCancel(context.Background())
	e := &hostExecution{
		result:     make(chan codemode.CodemodeResult, 1),
		vmCtx:      vmCtx,
		vmCancel:   vmCancel,
		tools:      options.tools,
		globals:    options.globals,
		callerCtx:  options.ctx,
		output:     codemode.CodemodeOutputItemList{},
		calls:      []codemode.CodemodeCall{},
		pending:    omap.NewMap[int, *hostPendingCall](),
		toWorker:   make(chan *HostToWorkerMessage, 16),
		workerDone: make(chan struct{}),
	}

	if !math.IsInf(options.timeoutMs, 0) && !math.IsNaN(options.timeoutMs) {
		msg := fmt.Sprintf("Execution timed out after %s ms", hostFormatTimeout(options.timeoutMs))
		delay := time.Duration(options.timeoutMs * float64(time.Millisecond))
		if delay < 0 {
			delay = 0
		}
		e.mu.Lock()
		if !e.finished {
			e.timer = time.AfterFunc(delay, func() {
				e.finish(&codemode.CodemodeError{
					Kind:    codemode.CodemodeErrorKindTimeout,
					Message: msg,
				}, jsonx.Opt[any]{}, nil)
			})
		}
		e.mu.Unlock()
	}

	if options.ctx != nil {
		if options.ctx.Err() != nil {
			e.onAbort()
		} else {
			stop := context.AfterFunc(options.ctx, e.onAbort)
			e.mu.Lock()
			if e.finished {
				e.mu.Unlock()
				stop()
			} else {
				e.abortStop = stop
				e.mu.Unlock()
			}
		}
	}

	if options.wasm != nil {
		e.start(options, options.wasm)
	} else {
		go func() {
			wasm, err := codemode.LoadQuickJSWasm(nil)
			if err != nil {
				e.finish(&codemode.CodemodeError{
					Kind:    codemode.CodemodeErrorKindSandbox,
					Message: "Failed to load QuickJS: " + hostErrorMessage(err),
				}, jsonx.Opt[any]{}, nil)
				return
			}
			e.start(options, wasm)
		}()
	}

	return e
}

// Result waits for the run to finish. Script failures resolve as CodemodeFailure;
// they are not errors.
func (e *hostExecution) Result() codemode.CodemodeResult {
	res := <-e.result
	// Keep the result available in case both Result() and Abort() await it.
	e.result <- res
	return res
}

// Abort finishes the run with kind "aborted" and waits for it.
func (e *hostExecution) Abort(message string) codemode.CodemodeResult {
	e.finish(&codemode.CodemodeError{
		Kind:    codemode.CodemodeErrorKindAborted,
		Message: message,
	}, jsonx.Opt[any]{}, nil)
	return e.Result()
}

func (e *hostExecution) start(options *hostExecutionOptions, wasm codemode.CodemodeWasmModule) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	e.workerStarted = true
	e.mu.Unlock()

	tools := make([]WorkerDataTool, 0, options.tools.Len())
	for _, tool := range options.tools.Values() {
		desc := ""
		if tool.Description != nil {
			desc = *tool.Description
		}
		tools = append(tools, WorkerDataTool{
			Name:        tool.Name,
			JsName:      codemode.ToCodemodeIdentifier(tool.Name),
			Description: desc,
		})
	}
	globals := make([]WorkerDataGlobal, 0, options.globals.Len())
	for _, global := range options.globals.Values() {
		globals = append(globals, WorkerDataGlobal{
			Name:   global.Name,
			Spread: global.Spread != nil && *global.Spread,
		})
	}

	data := &WorkerData{
		Code:             options.code,
		Tools:            tools,
		Globals:          globals,
		Wasm:             wasm,
		MemoryLimitBytes: options.memoryLimitBytes,
		Store:            options.store,
		Interrupt:        &e.interrupt,
	}

	toHost := make(chan WorkerToHostMessage, 16)
	go workerRun(e.vmCtx, data, toHost, e.toWorker)
	go func() {
		defer close(e.workerDone)
		for msg := range toHost {
			e.handleMessage(msg)
		}
		e.finish(&codemode.CodemodeError{
			Kind:    codemode.CodemodeErrorKindSandbox,
			Message: "Worker exited with code 0 before the script settled",
		}, jsonx.Opt[any]{}, nil)
	}()
}

func (e *hostExecution) onAbort() {
	msg := "Execution aborted"
	if e.callerCtx != nil {
		if cause := context.Cause(e.callerCtx); cause != nil && cause != context.Canceled && cause != context.DeadlineExceeded {
			msg = cause.Error()
		}
	}
	e.finish(&codemode.CodemodeError{
		Kind:    codemode.CodemodeErrorKindAborted,
		Message: msg,
	}, jsonx.Opt[any]{}, nil)
}

func (e *hostExecution) post(message *HostToWorkerMessage) {
	select {
	case e.toWorker <- message:
	case <-e.workerDone:
	case <-e.vmCtx.Done():
	}
}

func (e *hostExecution) handleMessage(message any) {
	if !IsWorkerToHostMessage(message) {
		return
	}
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	switch msg := message.(type) {
	case *WorkerOutputMessage:
		e.mu.Lock()
		if !e.finished {
			e.output = append(e.output, msg.Item)
		}
		e.mu.Unlock()
	case *WorkerCallMessage:
		e.handleCall(msg)
	case *WorkerDoneOkMessage:
		e.handleDone(msg)
	case *WorkerDoneErrorMessage:
		e.handleDone(msg)
	case *WorkerCrashMessage:
		e.finish(&codemode.CodemodeError{
			Kind:    codemode.CodemodeErrorKindSandbox,
			Message: msg.Message,
		}, jsonx.Opt[any]{}, nil)
	}
}

func (e *hostExecution) handleDone(message WorkerToHostMessage) {
	switch msg := message.(type) {
	case *WorkerDoneErrorMessage:
		var parsed struct {
			Name    *string `json:"name,omitzero"`
			Message string  `json:"message"`
			Stack   *string `json:"stack,omitzero"`
		}
		var errVal codemode.CodemodeError
		raw, err := jsonx.Parse([]byte(msg.Error))
		if err != nil {
			errVal = codemode.CodemodeError{
				Kind:    codemode.CodemodeErrorKindScript,
				Message: msg.Error,
			}
		} else if decErr := jsonx.Decode(raw, &parsed); decErr != nil {
			errVal = codemode.CodemodeError{
				Kind:    codemode.CodemodeErrorKindScript,
				Message: msg.Error,
			}
		} else {
			errVal = codemode.CodemodeError{
				Kind:    codemode.CodemodeErrorKindScript,
				Name:    parsed.Name,
				Message: parsed.Message,
				Stack:   parsed.Stack,
			}
		}
		e.finish(&errVal, jsonx.Opt[any]{}, nil)
	case *WorkerDoneOkMessage:
		var valueOpt jsonx.Opt[any]
		if msg.Value != nil {
			val, err := jsonx.Parse([]byte(*msg.Value))
			if err != nil {
				e.finish(&codemode.CodemodeError{
					Kind:    codemode.CodemodeErrorKindSandbox,
					Message: "Failed to parse script result: " + hostErrorMessage(err),
				}, jsonx.Opt[any]{}, nil)
				return
			}
			if val == nil {
				valueOpt = jsonx.Null[any]()
			} else {
				valueOpt = jsonx.Some(val)
			}
		}
		writes := msg.Writes
		e.finish(nil, valueOpt, &writes)
	}
}

func (e *hostExecution) handleCall(message *WorkerCallMessage) {
	id := message.Id
	name := message.Name
	isTool := message.Target == WorkerCallTargetTool

	callCtx, cancel := context.WithCancel(e.vmCtx)
	pending := &hostPendingCall{
		startedAt: time.Now(),
		cancel:    cancel,
	}

	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		cancel()
		return
	}
	var record *codemode.CodemodeCall
	if isTool {
		record = &codemode.CodemodeCall{
			Name:       name,
			Status:     codemode.CodemodeCallStatusCancelled,
			DurationMs: 0,
		}
		e.callRecords = append(e.callRecords, record)
		pending.record = record
	}
	e.pending.Set(id, pending)
	e.mu.Unlock()

	go func() {
		defer cancel()

		var tool *codemode.CodemodeTool
		var exists bool
		if isTool {
			tool, exists = e.tools.Get(name)
		} else {
			tool, exists = e.globals.Get(name)
		}

		var (
			status codemode.CodemodeCallStatus
			reply  *HostToWorkerMessage
		)
		if !exists || tool == nil {
			kindWord := "global"
			if isTool {
				kindWord = "tool"
			}
			errMsg := fmt.Sprintf("Unknown %s %q", kindWord, name)
			reply = &HostToWorkerMessage{
				Type:    "result",
				Id:      id,
				Ok:      false,
				Payload: &errMsg,
			}
			status = codemode.CodemodeCallStatusError
		} else {
			var (
				args any
				err  error
			)
			if message.Args != nil {
				args, err = jsonx.Parse([]byte(*message.Args))
			}
			var val any
			if err == nil {
				if tool.Execute == nil {
					err = fmt.Errorf("tool %q has no Execute callback", name)
				} else {
					val, err = tool.Execute(callCtx, args)
				}
			}
			if err != nil {
				errMsg := hostErrorMessage(err)
				reply = &HostToWorkerMessage{
					Type:    "result",
					Id:      id,
					Ok:      false,
					Payload: &errMsg,
				}
				status = codemode.CodemodeCallStatusError
			} else if opt, isOpt := val.(jsonx.Opt[any]); isOpt && opt.IsZero() {
				reply = &HostToWorkerMessage{
					Type: "result",
					Id:   id,
					Ok:   true,
				}
				status = codemode.CodemodeCallStatusOk
			} else {
				encoded, serErr := jsonx.Stringify(val)
				if serErr != nil {
					errMsg := hostErrorMessage(serErr)
					reply = &HostToWorkerMessage{
						Type:    "result",
						Id:      id,
						Ok:      false,
						Payload: &errMsg,
					}
					status = codemode.CodemodeCallStatusError
				} else {
					reply = &HostToWorkerMessage{
						Type:    "result",
						Id:      id,
						Ok:      true,
						Payload: &encoded,
					}
					status = codemode.CodemodeCallStatusOk
				}
			}
		}

		// Already cancelled by finish(): the record keeps "cancelled" and the
		// worker is gone or going.
		e.mu.Lock()
		_, stillPending := e.pending.Get(id)
		if !stillPending || e.finished {
			e.mu.Unlock()
			return
		}
		e.pending.Delete(id)
		if record != nil {
			record.Status = status
			record.DurationMs = time.Since(pending.startedAt).Milliseconds()
		}
		e.mu.Unlock()

		e.post(reply)
	}()
}

func (e *hostExecution) finish(scriptErr *codemode.CodemodeError, value jsonx.Opt[any], writes *string) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	e.finished = true
	timer := e.timer
	e.timer = nil
	abortStop := e.abortStop
	e.abortStop = nil

	now := time.Now()
	var cancels []context.CancelFunc
	for _, pending := range e.pending.Values() {
		if pending.record != nil {
			pending.record.DurationMs = now.Sub(pending.startedAt).Milliseconds()
		}
		cancels = append(cancels, pending.cancel)
	}
	e.pending.Clear()

	outputSnapshot := append(codemode.CodemodeOutputItemList{}, e.output...)
	callsSnapshot := make([]codemode.CodemodeCall, len(e.callRecords))
	for i, rec := range e.callRecords {
		callsSnapshot[i] = *rec
	}
	e.calls = callsSnapshot
	started := e.workerStarted
	e.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	if abortStop != nil {
		abortStop()
	}
	for _, cancel := range cancels {
		cancel()
	}

	var finalRes codemode.CodemodeResult
	if scriptErr != nil {
		finalRes = &codemode.CodemodeFailure{
			Ok:     false,
			Error:  *scriptErr,
			Output: outputSnapshot,
			Calls:  callsSnapshot,
		}
	} else {
		sw := codemode.CodemodeStoreWrites{
			Set:    omap.NewMap[string, any](),
			Delete: []string{},
		}
		if writes != nil {
			parsedSW, err := hostParseStoreWrites(*writes)
			if err != nil {
				finalRes = &codemode.CodemodeFailure{
					Ok: false,
					Error: codemode.CodemodeError{
						Kind:    codemode.CodemodeErrorKindSandbox,
						Message: "Failed to parse store writes: " + hostErrorMessage(err),
					},
					Output: outputSnapshot,
					Calls:  callsSnapshot,
				}
			} else {
				sw = *parsedSW
			}
		}
		if finalRes == nil {
			finalRes = &codemode.CodemodeSuccess{
				Ok:          true,
				Value:       value,
				Output:      outputSnapshot,
				Calls:       callsSnapshot,
				StoreWrites: sw,
			}
		}
	}

	e.interrupt.Store(1)
	e.vmCancel()

	if !started {
		e.result <- finalRes
		return
	}
	go func() {
		<-e.workerDone
		e.result <- finalRes
	}()
}

// CodemodeSandbox runs JavaScript in a QuickJS VM (a separate wasm instance).
// The script sees tools.<name>(args) for every registered tool, ALL_TOOLS, the output
// helpers text, image, exit, and console.*, store/load, and the configured globals;
// nothing else (no timers, fetch, process, require, modules).
//
// Each Execute gets its own VM. The sandbox only holds the tool table and defaults.
// Close aborts in-flight executions.
type CodemodeSandbox struct {
	// mu guards the maps, running, and closed. It is not held across Execute's VM run
	// or across tool callbacks.
	mu               sync.Mutex
	toolsByName      *omap.Map[string, *codemode.CodemodeTool]
	globalsByName    *omap.Map[string, *codemode.CodemodeTool]
	timeoutMs        float64
	memoryLimitBytes *int
	wasm             codemode.CodemodeWasmModule
	workerUrl        string
	running          *omap.Set[*hostExecution]
	closed           bool
}

// NewCodemodeSandbox builds a sandbox. A nil options uses the defaults.
// It returns an error for an invalid or duplicate global name.
func NewCodemodeSandbox(options *codemode.CodemodeSandboxOptions) (*CodemodeSandbox, error) {
	if options == nil {
		options = &codemode.CodemodeSandboxOptions{}
	}
	timeoutMs := float64(hostDefaultTimeoutMs)
	if options.TimeoutMs != nil {
		timeoutMs = *options.TimeoutMs
	}
	workerUrl := ""
	if options.WorkerUrl != nil {
		workerUrl = *options.WorkerUrl
	}
	s := &CodemodeSandbox{
		toolsByName:      omap.NewMap[string, *codemode.CodemodeTool](),
		globalsByName:    omap.NewMap[string, *codemode.CodemodeTool](),
		timeoutMs:        timeoutMs,
		memoryLimitBytes: options.MemoryLimitBytes,
		wasm:             options.Wasm,
		workerUrl:        workerUrl,
		running:          omap.NewSet[*hostExecution](),
	}
	for _, tool := range options.Tools {
		if err := s.RegisterTool(tool); err != nil {
			return nil, err
		}
	}

	namespaces := omap.NewSet[string]()
	for _, global := range options.Globals {
		parts := strings.Split(global.Name, ".")
		valid := len(parts) <= 2
		if valid {
			for _, part := range parts {
				if !hostIdentifier.MatchString(part) {
					valid = false
					break
				}
			}
		}
		if valid {
			if _, reserved := hostReservedGlobals[parts[0]]; reserved {
				valid = false
			}
		}
		if !valid {
			return nil, fmt.Errorf("Invalid global name %q", global.Name)
		}
		if s.globalsByName.Has(global.Name) {
			return nil, fmt.Errorf("Global %q is already registered", global.Name)
		}
		if len(parts) == 2 {
			namespaces.Add(parts[0])
		}
		s.globalsByName.Set(global.Name, global)
	}
	for _, name := range namespaces.Values() {
		if s.globalsByName.Has(name) {
			return nil, fmt.Errorf("Global %q conflicts with the namespace %q", name, name)
		}
	}
	return s, nil
}

// RegisterTool adds a tool. It returns an error if a tool with the same name is already registered.
func (s *CodemodeSandbox) RegisterTool(tool *codemode.CodemodeTool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolsByName.Has(tool.Name) {
		return fmt.Errorf("Tool %q is already registered", tool.Name)
	}
	s.toolsByName.Set(tool.Name, tool)
	return nil
}

// UnregisterTool removes a tool by name. It reports whether the tool was registered.
func (s *CodemodeSandbox) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolsByName.Delete(name)
}

// Tools returns the registered tools in registration order.
func (s *CodemodeSandbox) Tools() []*codemode.CodemodeTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	vals := s.toolsByName.Values()
	out := make([]*codemode.CodemodeTool, len(vals))
	copy(out, vals)
	return out
}

// Globals returns the configured globals in registration order.
func (s *CodemodeSandbox) Globals() []*codemode.CodemodeTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	vals := s.globalsByName.Values()
	out := make([]*codemode.CodemodeTool, len(vals))
	copy(out, vals)
	return out
}

// Execute runs code as an async function body: return and top-level await work.
// It does not return an error for script failures; those come back as *CodemodeFailure.
// An error is returned only when the sandbox is closed.
// The script can use store(key, value) and load(key) on options.Store.
// ctx is the caller's abort signal (TS options.signal).
func (s *CodemodeSandbox) Execute(ctx context.Context, code string, options *codemode.CodemodeExecuteOptions) (codemode.CodemodeResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("Sandbox is closed")
	}

	toolsCopy := omap.NewMap[string, *codemode.CodemodeTool]()
	for _, k := range s.toolsByName.Keys() {
		v, _ := s.toolsByName.Get(k)
		toolsCopy.Set(k, v)
	}
	globalsCopy := omap.NewMap[string, *codemode.CodemodeTool]()
	for _, k := range s.globalsByName.Keys() {
		v, _ := s.globalsByName.Get(k)
		globalsCopy.Set(k, v)
	}

	timeoutMs := s.timeoutMs
	var storeSnapshot map[string]any
	if options != nil {
		if options.TimeoutMs != nil {
			timeoutMs = *options.TimeoutMs
		}
		storeSnapshot = options.Store
	}

	exec := hostNewExecution(&hostExecutionOptions{
		code:             code,
		tools:            toolsCopy,
		globals:          globalsCopy,
		timeoutMs:        timeoutMs,
		ctx:              ctx,
		memoryLimitBytes: s.memoryLimitBytes,
		store:            hostSerializeStore(storeSnapshot),
		wasm:             s.wasm,
		workerUrl:        s.workerUrl,
	})
	s.running.Add(exec)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running.Delete(exec)
		s.mu.Unlock()
	}()

	return exec.Result(), nil
}

// Close aborts in-flight executions (they resolve with kind "aborted") and makes later Execute calls fail.
func (s *CodemodeSandbox) Close() error {
	s.mu.Lock()
	s.closed = true
	active := make([]*hostExecution, 0, s.running.Len())
	for _, exec := range s.running.Values() {
		active = append(active, exec)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(len(active))
	for _, exec := range active {
		go func(e *hostExecution) {
			defer wg.Done()
			e.Abort("Sandbox closed")
		}(exec)
	}
	wg.Wait()
	return nil
}
