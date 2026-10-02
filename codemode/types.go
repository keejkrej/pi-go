// Ported from packages/codemode/src/types.ts (pi v1.0.0).

package codemode

import (
	"context"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// CodemodeToolContext is the per-call context passed to a tool.
// The TS signal is the ctx argument of CodemodeTool.Execute, not a field of that callback.
type CodemodeToolContext struct {
	// Ctx is cancelled when the script finishes (including unawaited calls), the
	// execution times out, the caller aborts, or the sandbox is closed.
	Ctx context.Context `json:"-"`
}

// CodemodeJsonSchema is a JSON Schema document. Only used to render declarations;
// values are not validated against it. A boolean schema is a bool. An object
// schema is a *jsonx.Object (other jsonx values are accepted). Nil means the schema was omitted.
type CodemodeJsonSchema any

// CodemodeTool is one script-callable tool or global.
type CodemodeTool struct {
	// Name is how the script calls the tool: tools.<id>(args), where <id> is Name
	// with non-identifier characters replaced (see ToCodemodeIdentifier), and also
	// tools["<name>"](args). Globals are called as <name>(args) and must be identifiers,
	// or <namespace>.<member>, which groups them into a frozen namespace object.
	Name string `json:"name"`
	// Description is a doc comment in RenderDeclarations, and is listed in ALL_TOOLS for tools.
	Description *string `json:"description,omitzero"`
	// InputSchema is the schema of the single argument. Rendered as the parameter type; unknown when nil.
	InputSchema CodemodeJsonSchema `json:"inputSchema,omitzero"`
	// OutputSchema is the schema of the resolved value. Rendered as the promise type; unknown when nil.
	OutputSchema CodemodeJsonSchema `json:"outputSchema,omitzero"`
	// Spread, globals only: Execute receives all call arguments as an array instead of the first one.
	Spread *bool `json:"spread,omitzero"`
	// Signature, globals only, replaces the rendering from the schemas. It is the TypeScript
	// parameter list and return type, for example "(type: string, id?: string): Promise<Model[]>".
	Signature *string `json:"signature,omitzero"`
	// Execute is called with args after a JSON round trip. The return value must be
	// JSON-serializable; a returned error surfaces in the script as an Error with the same message.
	// ctx is the TS context.signal (CodemodeToolContext).
	Execute func(ctx context.Context, args any) (any, error) `json:"-"`
}

// CodemodeOutputItem is one item of the script's output, in the order the script produced it.
// text() and console.* produce text items, image() image items. Data is base64.
type CodemodeOutputItem interface{ isCodemodeOutputItem() }

// CodemodeTextOutput is { type: "text", text }.
type CodemodeTextOutput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (*CodemodeTextOutput) isCodemodeOutputItem() {}

// CodemodeImageOutput is { type: "image", data, mimeType }.
type CodemodeImageOutput struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (*CodemodeImageOutput) isCodemodeOutputItem() {}

// UnknownCodemodeOutputItem keeps an output item whose type is not text or image.
type UnknownCodemodeOutputItem struct {
	Raw *jsonx.Object
}

func (*UnknownCodemodeOutputItem) isCodemodeOutputItem() {}

func (u *UnknownCodemodeOutputItem) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownCodemodeOutputItem.MarshalJSON")
}

func (u *UnknownCodemodeOutputItem) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownCodemodeOutputItem.UnmarshalJSON")
}

// CodemodeOutputItemList is a JSON array of output items.
type CodemodeOutputItemList []CodemodeOutputItem

func (l *CodemodeOutputItemList) UnmarshalJSON(data []byte) error {
	panic("unported: CodemodeOutputItemList.UnmarshalJSON")
}

// UnmarshalCodemodeOutputItem decodes one output item.
func UnmarshalCodemodeOutputItem(data []byte) (CodemodeOutputItem, error) {
	panic("unported: UnmarshalCodemodeOutputItem")
}

// DecodeCodemodeOutputItem decodes one output item from a jsonx value.
func DecodeCodemodeOutputItem(v any) (CodemodeOutputItem, error) {
	panic("unported: DecodeCodemodeOutputItem")
}

// CodemodeCallStatus is the outcome of one recorded tool call.
type CodemodeCallStatus string

const (
	CodemodeCallStatusOk        CodemodeCallStatus = "ok"
	CodemodeCallStatusError     CodemodeCallStatus = "error"
	CodemodeCallStatusCancelled CodemodeCallStatus = "cancelled"
)

// CodemodeCall is one recorded tool call. Globals are not recorded.
type CodemodeCall struct {
	Name       string             `json:"name"`
	Status     CodemodeCallStatus `json:"status"`
	DurationMs int64              `json:"durationMs"`
}

// CodemodeErrorKind classifies a failed execution.
type CodemodeErrorKind string

const (
	// CodemodeErrorKindScript means the script threw or failed to parse.
	// Name and Stack come from the script's error.
	CodemodeErrorKindScript CodemodeErrorKind = "script"
	// CodemodeErrorKindTimeout means the overall deadline expired. The run was terminated.
	CodemodeErrorKindTimeout CodemodeErrorKind = "timeout"
	// CodemodeErrorKindAborted means the caller's signal fired or the sandbox was closed.
	CodemodeErrorKindAborted CodemodeErrorKind = "aborted"
	// CodemodeErrorKindSandbox means the VM failed outside the script's control
	// (for example a wasm trap).
	CodemodeErrorKindSandbox CodemodeErrorKind = "sandbox"
)

// CodemodeError is the failure of one execution.
type CodemodeError struct {
	Kind    CodemodeErrorKind `json:"kind"`
	Name    *string           `json:"name,omitzero"`
	Message string            `json:"message"`
	Stack   *string           `json:"stack,omitzero"`
}

// CodemodeStoreWrites are the keys the script changed with store().
// Only successful executions report writes.
type CodemodeStoreWrites struct {
	// Set is insertion order of store() calls. Values are jsonx values. Nil marshals as null;
	// writers use omap.NewMap.
	Set *omap.Map[string, any] `json:"set"`
	// Delete are keys stored as undefined. Writers use a non-nil slice so it encodes as [].
	Delete []string `json:"delete"`
}

// CodemodeResult is one finished script. Output is kept for failed executions too, up to the failure.
// exit() completes with an absent Value.
type CodemodeResult interface{ isCodemodeResult() }

// CodemodeSuccess is { ok: true, value, output, calls, storeWrites }.
type CodemodeSuccess struct {
	Ok bool `json:"ok"`
	// Value is absent for undefined (exit() or a script that returns undefined),
	// null for JSON null, and some for any other JSON value.
	Value jsonx.Opt[any] `json:"value,omitzero"`
	// Output and Calls are required. Writers use empty non-nil slices so they encode as [].
	Output      CodemodeOutputItemList `json:"output"`
	Calls       []CodemodeCall         `json:"calls"`
	StoreWrites CodemodeStoreWrites    `json:"storeWrites"`
}

func (*CodemodeSuccess) isCodemodeResult() {}

// CodemodeFailure is { ok: false, error, output, calls }.
type CodemodeFailure struct {
	Ok    bool          `json:"ok"`
	Error CodemodeError `json:"error"`
	// Output and Calls are required. Writers use empty non-nil slices so they encode as [].
	Output CodemodeOutputItemList `json:"output"`
	Calls  []CodemodeCall         `json:"calls"`
}

func (*CodemodeFailure) isCodemodeResult() {}

// UnknownCodemodeResult keeps a result whose ok field is not a boolean.
type UnknownCodemodeResult struct {
	Raw *jsonx.Object
}

func (*UnknownCodemodeResult) isCodemodeResult() {}

func (u *UnknownCodemodeResult) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownCodemodeResult.MarshalJSON")
}

func (u *UnknownCodemodeResult) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownCodemodeResult.UnmarshalJSON")
}

// UnmarshalCodemodeResult decodes one execution result.
func UnmarshalCodemodeResult(data []byte) (CodemodeResult, error) {
	panic("unported: UnmarshalCodemodeResult")
}

// DecodeCodemodeResult decodes one execution result from a jsonx value.
func DecodeCodemodeResult(v any) (CodemodeResult, error) {
	panic("unported: DecodeCodemodeResult")
}

// CodemodeSandboxOptions configures a sandbox.
// A nil *CodemodeSandboxOptions means the defaults.
type CodemodeSandboxOptions struct {
	Tools   []*CodemodeTool `json:"tools,omitzero"`
	Globals []*CodemodeTool `json:"globals,omitzero"`
	// TimeoutMs is the overall deadline per execution, including time spent in tools.
	// Nil uses 300000. math.Inf(1) disables the deadline (TS Infinity); the execution
	// then only ends when the script settles or is aborted.
	TimeoutMs *float64 `json:"timeoutMs,omitzero"`
	// MemoryLimitBytes is the maximum memory the QuickJS VM may allocate.
	// Allocations beyond it fail inside the script as InternalError: out of memory.
	// Nil means no limit beyond wasm32's 4 GiB address space.
	MemoryLimitBytes *int `json:"memoryLimitBytes,omitzero"`
	// Wasm is a compiled quickjs.wasm, usually from LoadQuickJSWasm.
	// Nil loads the embedded module.
	Wasm CodemodeWasmModule `json:"-"`
	// WorkerUrl is the TS worker entry (string or URL). The Go sandbox runs the VM
	// in-process and does not load a worker module from this value.
	WorkerUrl *string `json:"workerUrl,omitzero"`
}

// CodemodeExecuteOptions overrides one execution.
// The caller's abort signal is the ctx argument of the sandbox Execute method (TS options.signal).
type CodemodeExecuteOptions struct {
	// TimeoutMs overrides the sandbox default for this execution.
	// Nil keeps the sandbox default. math.Inf(1) disables the deadline (TS Infinity).
	TimeoutMs *float64 `json:"timeoutMs,omitzero"`
	// Store is the values the script reads with load(key). Values are jsonx values
	// (object values are *jsonx.Object). The script's own store() calls come back as
	// CodemodeSuccess.StoreWrites; persisting them is up to the caller. Nil means no keys.
	Store map[string]any `json:"store,omitzero"`
}
