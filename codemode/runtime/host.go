// Ported from packages/codemode/src/runtime/host.ts (pi v1.0.0).

package runtime

import (
	"context"
	"regexp"
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
	panic("unported: hostErrorMessage")
}

func hostSerializeStore(store map[string]any) map[string]string {
	panic("unported: hostSerializeStore")
}

func hostParseStoreWrites(jsonText string) (*codemode.CodemodeStoreWrites, error) {
	panic("unported: hostParseStoreWrites")
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
	// mu guards finished, output, calls, and pending. It is not held across tool
	// callbacks or channel sends.
	mu         sync.Mutex
	result     chan codemode.CodemodeResult
	vmCancel   context.CancelFunc
	interrupt  atomic.Int32
	tools      *omap.Map[string, *codemode.CodemodeTool]
	globals    *omap.Map[string, *codemode.CodemodeTool]
	callerCtx  context.Context
	timer      *time.Timer
	output     codemode.CodemodeOutputItemList
	calls      []codemode.CodemodeCall
	pending    *omap.Map[int, *hostPendingCall]
	finished   bool
	workerDone chan struct{}
}

func hostNewExecution(options *hostExecutionOptions) *hostExecution {
	panic("unported: hostNewExecution")
}

// Result waits for the run to finish. Script failures resolve as CodemodeFailure;
// they are not errors.
func (e *hostExecution) Result() codemode.CodemodeResult {
	panic("unported: hostExecution.Result")
}

// Abort finishes the run with kind "aborted" and waits for it.
func (e *hostExecution) Abort(message string) codemode.CodemodeResult {
	panic("unported: hostExecution.Abort")
}

func (e *hostExecution) start(options *hostExecutionOptions, wasm codemode.CodemodeWasmModule) {
	panic("unported: hostExecution.start")
}

func (e *hostExecution) onAbort() {
	panic("unported: hostExecution.onAbort")
}

func (e *hostExecution) post(message *HostToWorkerMessage) {
	panic("unported: hostExecution.post")
}

func (e *hostExecution) handleMessage(message any) {
	panic("unported: hostExecution.handleMessage")
}

func (e *hostExecution) handleDone(message WorkerToHostMessage) {
	panic("unported: hostExecution.handleDone")
}

func (e *hostExecution) handleCall(message *WorkerCallMessage) {
	panic("unported: hostExecution.handleCall")
}

func (e *hostExecution) finish(scriptErr *codemode.CodemodeError, value jsonx.Opt[any], writes *string) {
	panic("unported: hostExecution.finish")
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
	panic("unported: NewCodemodeSandbox")
}

// RegisterTool adds a tool. It returns an error if a tool with the same name is already registered.
func (s *CodemodeSandbox) RegisterTool(tool *codemode.CodemodeTool) error {
	panic("unported: CodemodeSandbox.RegisterTool")
}

// UnregisterTool removes a tool by name. It reports whether the tool was registered.
func (s *CodemodeSandbox) UnregisterTool(name string) bool {
	panic("unported: CodemodeSandbox.UnregisterTool")
}

// Tools returns the registered tools in registration order.
func (s *CodemodeSandbox) Tools() []*codemode.CodemodeTool {
	panic("unported: CodemodeSandbox.Tools")
}

// Globals returns the configured globals in registration order.
func (s *CodemodeSandbox) Globals() []*codemode.CodemodeTool {
	panic("unported: CodemodeSandbox.Globals")
}

// Execute runs code as an async function body: return and top-level await work.
// It does not return an error for script failures; those come back as *CodemodeFailure.
// An error is returned only when the sandbox is closed.
// The script can use store(key, value) and load(key) on options.Store.
// ctx is the caller's abort signal (TS options.signal).
func (s *CodemodeSandbox) Execute(ctx context.Context, code string, options *codemode.CodemodeExecuteOptions) (codemode.CodemodeResult, error) {
	panic("unported: CodemodeSandbox.Execute")
}

// Close aborts in-flight executions (they resolve with kind "aborted") and makes later Execute calls fail.
func (s *CodemodeSandbox) Close() error {
	panic("unported: CodemodeSandbox.Close")
}
