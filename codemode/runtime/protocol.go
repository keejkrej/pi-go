// Ported from packages/codemode/src/runtime/protocol.ts (pi v1.0.0).

package runtime

import (
	"sync/atomic"

	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// WorkerDataTool is one tool the worker installs. JsName is the identifier the script
// uses; Description is listed in ALL_TOOLS.
type WorkerDataTool struct {
	Name        string `json:"name"`
	JsName      string `json:"jsName"`
	Description string `json:"description"`
}

// WorkerDataGlobal is one global the worker installs. Spread means the call passes every argument.
type WorkerDataGlobal struct {
	Name   string `json:"name"`
	Spread bool   `json:"spread"`
}

// WorkerData is the input to one worker run.
// Tool arguments, results, and values cross as JSON strings: the worker passes them
// into and out of the QuickJS VM as strings and never builds structured values itself.
type WorkerData struct {
	Code    string             `json:"code"`
	Tools   []WorkerDataTool   `json:"tools"`
	Globals []WorkerDataGlobal `json:"globals"`
	// Wasm is the compiled quickjs-wasi module.
	Wasm codemode.CodemodeWasmModule `json:"-"`
	// MemoryLimitBytes is nil for no limit beyond wasm32's address space.
	MemoryLimitBytes *int `json:"memoryLimitBytes,omitzero"`
	// Store is the load() snapshot: key to JSON text.
	Store map[string]string `json:"store"`
	// Interrupt is polled by the VM interrupt handler. The host sets it non-zero before
	// terminating the run. TS: a SharedArrayBuffer holding one Int32, because
	// worker.terminate() cannot stop a thread spinning in wasm.
	Interrupt *atomic.Int32 `json:"-"`
}

// ScriptErrorJson is a JSON-encoded { name?, message, stack? } of an error thrown by the script.
type ScriptErrorJson = string

// WorkerCallTarget is where a host call is dispatched.
type WorkerCallTarget string

const (
	WorkerCallTargetTool   WorkerCallTarget = "tool"
	WorkerCallTargetGlobal WorkerCallTarget = "global"
)

// WorkerToHostMessage is one message from the worker to the host.
type WorkerToHostMessage interface{ isWorkerToHostMessage() }

// WorkerCallMessage is { type: "call", id, target, name, args }.
// Args is the JSON text of the arguments, nil when the script passed undefined.
type WorkerCallMessage struct {
	Type   string           `json:"type"`
	Id     int              `json:"id"`
	Target WorkerCallTarget `json:"target"`
	Name   string           `json:"name"`
	Args   *string          `json:"args,omitzero"`
}

func (*WorkerCallMessage) isWorkerToHostMessage() {}

// WorkerOutputMessage is { type: "output", item }.
type WorkerOutputMessage struct {
	Type string                      `json:"type"`
	Item codemode.CodemodeOutputItem `json:"item"`
}

func (*WorkerOutputMessage) isWorkerToHostMessage() {}

// WorkerDoneOkMessage is { type: "done", ok: true, value, writes }.
// Writes is a JSON array of [key, json] for store() and [key] for deletions.
// Value is nil when the script result is undefined.
type WorkerDoneOkMessage struct {
	Type   string  `json:"type"`
	Ok     bool    `json:"ok"`
	Value  *string `json:"value,omitzero"`
	Writes string  `json:"writes"`
}

func (*WorkerDoneOkMessage) isWorkerToHostMessage() {}

// WorkerDoneErrorMessage is { type: "done", ok: false, error }.
type WorkerDoneErrorMessage struct {
	Type  string          `json:"type"`
	Ok    bool            `json:"ok"`
	Error ScriptErrorJson `json:"error"`
}

func (*WorkerDoneErrorMessage) isWorkerToHostMessage() {}

// WorkerCrashMessage is { type: "crash", message }.
// The VM failed outside the script's control, for example a wasm trap.
type WorkerCrashMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (*WorkerCrashMessage) isWorkerToHostMessage() {}

// UnknownWorkerToHostMessage keeps a worker message whose type is not recognized.
type UnknownWorkerToHostMessage struct {
	Raw *jsonx.Object
}

func (*UnknownWorkerToHostMessage) isWorkerToHostMessage() {}

func (u *UnknownWorkerToHostMessage) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownWorkerToHostMessage.MarshalJSON")
}

func (u *UnknownWorkerToHostMessage) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownWorkerToHostMessage.UnmarshalJSON")
}

// UnmarshalWorkerToHostMessage decodes one worker-to-host message.
func UnmarshalWorkerToHostMessage(data []byte) (WorkerToHostMessage, error) {
	panic("unported: UnmarshalWorkerToHostMessage")
}

// DecodeWorkerToHostMessage decodes one worker-to-host message from a jsonx value.
func DecodeWorkerToHostMessage(v any) (WorkerToHostMessage, error) {
	panic("unported: DecodeWorkerToHostMessage")
}

// HostToWorkerMessage is { type: "result", id, ok, payload }.
// Payload is the JSON result when Ok, otherwise the error message. Nil payload means undefined.
type HostToWorkerMessage struct {
	Type    string  `json:"type"`
	Id      int     `json:"id"`
	Ok      bool    `json:"ok"`
	Payload *string `json:"payload,omitzero"`
}

// IsWorkerToHostMessage reports whether value is a worker-to-host message.
func IsWorkerToHostMessage(value any) bool {
	panic("unported: IsWorkerToHostMessage")
}

// IsHostToWorkerMessage reports whether value is a host-to-worker result.
func IsHostToWorkerMessage(value any) bool {
	panic("unported: IsHostToWorkerMessage")
}
