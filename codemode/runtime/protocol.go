// Ported from packages/codemode/src/runtime/protocol.ts (pi v1.0.0).

package runtime

import (
	"fmt"
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
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownWorkerToHostMessage) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if v == nil {
		u.Raw = nil
		return nil
	}
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return fmt.Errorf("expected worker message object, got %T", v)
	}
	u.Raw = obj
	return nil
}

// UnmarshalWorkerToHostMessage decodes one worker-to-host message.
func UnmarshalWorkerToHostMessage(data []byte) (WorkerToHostMessage, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeWorkerToHostMessage(v)
}

// DecodeWorkerToHostMessage decodes one worker-to-host message from a jsonx value.
func DecodeWorkerToHostMessage(v any) (WorkerToHostMessage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("expected worker message object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typeName, _ := rawType.(string)
	switch typeName {
	case "call":
		var msg WorkerCallMessage
		if err := jsonx.Decode(obj, &msg); err != nil {
			return nil, err
		}
		return &msg, nil
	case "output":
		rawItem, hasItem := obj.Get("item")
		if !hasItem {
			return nil, fmt.Errorf("output message missing item")
		}
		item, err := codemode.DecodeCodemodeOutputItem(rawItem)
		if err != nil {
			return nil, err
		}
		return &WorkerOutputMessage{
			Type: "output",
			Item: item,
		}, nil
	case "done":
		rawOk, hasOk := obj.Get("ok")
		okBool, isBool := rawOk.(bool)
		if !hasOk || !isBool {
			return &UnknownWorkerToHostMessage{Raw: obj}, nil
		}
		if okBool {
			var msg WorkerDoneOkMessage
			if err := jsonx.Decode(obj, &msg); err != nil {
				return nil, err
			}
			return &msg, nil
		}
		var msg WorkerDoneErrorMessage
		if err := jsonx.Decode(obj, &msg); err != nil {
			return nil, err
		}
		return &msg, nil
	case "crash":
		var msg WorkerCrashMessage
		if err := jsonx.Decode(obj, &msg); err != nil {
			return nil, err
		}
		return &msg, nil
	default:
		return &UnknownWorkerToHostMessage{Raw: obj}, nil
	}
}

// HostToWorkerMessage is { type: "result", id, ok, payload }.
// Payload is the JSON result when Ok, otherwise the error message. Nil payload means undefined.
type HostToWorkerMessage struct {
	Type    string  `json:"type"`
	Id      int     `json:"id"`
	Ok      bool    `json:"ok"`
	Payload *string `json:"payload,omitzero"`
}

func protObjectType(value any) (string, bool) {
	switch obj := value.(type) {
	case *jsonx.Object:
		if obj == nil {
			return "", false
		}
		rawType, _ := obj.Get("type")
		s, ok := rawType.(string)
		return s, ok
	case map[string]any:
		if obj == nil {
			return "", false
		}
		s, ok := obj["type"].(string)
		return s, ok
	default:
		return "", false
	}
}

// IsWorkerToHostMessage reports whether value is a worker-to-host message.
func IsWorkerToHostMessage(value any) bool {
	switch m := value.(type) {
	case *WorkerCallMessage:
		return m != nil
	case *WorkerOutputMessage:
		return m != nil
	case *WorkerDoneOkMessage:
		return m != nil
	case *WorkerDoneErrorMessage:
		return m != nil
	case *WorkerCrashMessage:
		return m != nil
	default:
		t, ok := protObjectType(value)
		if !ok {
			return false
		}
		return t == "call" || t == "output" || t == "done" || t == "crash"
	}
}

// IsHostToWorkerMessage reports whether value is a host-to-worker result.
func IsHostToWorkerMessage(value any) bool {
	switch m := value.(type) {
	case *HostToWorkerMessage:
		return m != nil && m.Type == "result"
	default:
		t, ok := protObjectType(value)
		return ok && t == "result"
	}
}
