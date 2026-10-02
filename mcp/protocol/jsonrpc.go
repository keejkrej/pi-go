// Ported from packages/mcp/src/protocol/jsonrpc.ts (pi v1.0.0).

package protocol

import (
	"context"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// JsonRpcId is a JSON-RPC id, a string or a finite number.
// IsString selects String; otherwise Number is the id, including 0.
// The zero value is the number 0. Values are comparable and are map keys.
type JsonRpcId struct {
	IsString bool
	String   string
	Number   float64
}

// JsonRpcRequest is a request with an id.
type JsonRpcRequest struct {
	Jsonrpc string    `json:"jsonrpc"`
	Id      JsonRpcId `json:"id"`
	Method  string    `json:"method"`
	Params  any       `json:"params,omitzero"` // nil interface omits the field; a typed nil does not
}

// JsonRpcNotification is a request without an id.
type JsonRpcNotification struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitzero"` // nil interface omits the field; a typed nil does not
}

// JsonRpcErrorObject is the error member of an error response.
type JsonRpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitzero"`
}

// JsonRpcSuccessResponse is a successful response.
type JsonRpcSuccessResponse struct {
	Jsonrpc string    `json:"jsonrpc"`
	Id      JsonRpcId `json:"id"`
	Result  any       `json:"result"`
}

// JsonRpcErrorResponse is a failed response.
type JsonRpcErrorResponse struct {
	Jsonrpc string             `json:"jsonrpc"`
	Id      JsonRpcId          `json:"id"`
	Error   JsonRpcErrorObject `json:"error"`
}

// JsonRpcResponse is a sealed union: *JsonRpcSuccessResponse, *JsonRpcErrorResponse,
// *UnknownJsonRpcResponse.
type JsonRpcResponse interface{ isJsonRpcResponse() }

// JsonRpcMessage is a sealed union: *JsonRpcRequest, *JsonRpcNotification,
// *JsonRpcSuccessResponse, *JsonRpcErrorResponse, *UnknownJsonRpcResponse,
// *UnknownJsonRpcMessage.
type JsonRpcMessage interface{ isJsonRpcMessage() }

// UnknownJsonRpcResponse keeps a response that is neither success nor error.
type UnknownJsonRpcResponse struct {
	Raw *jsonx.Object
}

// UnknownJsonRpcMessage keeps a message that is not a request, notification, or response.
type UnknownJsonRpcMessage struct {
	Raw *jsonx.Object
}

func (*JsonRpcRequest) isJsonRpcMessage()         {}
func (*JsonRpcNotification) isJsonRpcMessage()    {}
func (*JsonRpcSuccessResponse) isJsonRpcMessage() {}
func (*JsonRpcErrorResponse) isJsonRpcMessage()   {}
func (*UnknownJsonRpcResponse) isJsonRpcMessage() {}
func (*UnknownJsonRpcMessage) isJsonRpcMessage()  {}

func (*JsonRpcSuccessResponse) isJsonRpcResponse() {}
func (*JsonRpcErrorResponse) isJsonRpcResponse()   {}
func (*UnknownJsonRpcResponse) isJsonRpcResponse() {}

func (id JsonRpcId) MarshalJSON() ([]byte, error) {
	panic("unported: JsonRpcId.MarshalJSON")
}

func (id *JsonRpcId) UnmarshalJSON(data []byte) error {
	panic("unported: JsonRpcId.UnmarshalJSON")
}

func (u *UnknownJsonRpcResponse) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownJsonRpcResponse.MarshalJSON")
}

func (u *UnknownJsonRpcMessage) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownJsonRpcMessage.MarshalJSON")
}

// UnmarshalJsonRpcMessage decodes one JSON-RPC message.
// An unrecognized shape becomes *UnknownJsonRpcMessage.
func UnmarshalJsonRpcMessage(data []byte) (JsonRpcMessage, error) {
	panic("unported: UnmarshalJsonRpcMessage")
}

// DecodeJsonRpcMessage decodes one JSON-RPC message from a jsonx value.
func DecodeJsonRpcMessage(v any) (JsonRpcMessage, error) {
	panic("unported: DecodeJsonRpcMessage")
}

// UnmarshalJsonRpcResponse decodes one JSON-RPC response.
// An unrecognized shape becomes *UnknownJsonRpcResponse.
func UnmarshalJsonRpcResponse(data []byte) (JsonRpcResponse, error) {
	panic("unported: UnmarshalJsonRpcResponse")
}

// DecodeJsonRpcResponse decodes one JSON-RPC response from a jsonx value.
func DecodeJsonRpcResponse(v any) (JsonRpcResponse, error) {
	panic("unported: DecodeJsonRpcResponse")
}

type jsonJsonRpcErrorCodes struct {
	ParseError     int `json:"parseError"`
	InvalidRequest int `json:"invalidRequest"`
	MethodNotFound int `json:"methodNotFound"`
	InvalidParams  int `json:"invalidParams"`
	InternalError  int `json:"internalError"`
}

// JsonRpcErrorCodes holds the standard JSON-RPC 2.0 error codes.
var JsonRpcErrorCodes = jsonJsonRpcErrorCodes{
	ParseError:     -32700,
	InvalidRequest: -32600,
	MethodNotFound: -32601,
	InvalidParams:  -32602,
	InternalError:  -32603,
}

// McpError is a JSON-RPC error raised by the MCP client.
type McpError struct {
	Code    int
	Message string
	Data    any
}

// NewMcpError returns an error whose Name is "McpError". data may be nil.
func NewMcpError(code int, message string, data any) *McpError {
	panic("unported: NewMcpError")
}

func (e *McpError) Error() string { return e.Message }

func (e *McpError) Name() string { return "McpError" }

// McpConnectionClosedError is raised when the transport is not usable.
type McpConnectionClosedError struct {
	Message string
}

// NewMcpConnectionClosedError returns an error whose Name is "McpConnectionClosedError".
// With no argument the message is "MCP connection closed".
func NewMcpConnectionClosedError(message ...string) *McpConnectionClosedError {
	panic("unported: NewMcpConnectionClosedError")
}

func (e *McpConnectionClosedError) Error() string { return e.Message }

func (e *McpConnectionClosedError) Name() string { return "McpConnectionClosedError" }

// McpTimeoutError is raised when a request exceeds its timeout.
type McpTimeoutError struct {
	TimeoutMs int64
	Message   string
}

// NewMcpTimeoutError returns an error whose Name is "McpTimeoutError".
// The message is "MCP request timed out after <timeoutMs>ms".
func NewMcpTimeoutError(timeoutMs int64) *McpTimeoutError {
	panic("unported: NewMcpTimeoutError")
}

func (e *McpTimeoutError) Error() string { return e.Message }

func (e *McpTimeoutError) Name() string { return "McpTimeoutError" }

// McpAbortError is raised when a request is aborted. Its Name is "AbortError".
type McpAbortError struct {
	Message string
}

// NewMcpAbortError returns an error whose Name is "AbortError".
// With no argument the message is "MCP request aborted".
func NewMcpAbortError(message ...string) *McpAbortError {
	panic("unported: NewMcpAbortError")
}

func (e *McpAbortError) Error() string { return e.Message }

func (e *McpAbortError) Name() string { return "AbortError" }

func (e *McpAbortError) Unwrap() error { return context.Canceled }

// IsObject reports whether value is a non-null JSON object.
func IsObject(value any) bool {
	panic("unported: IsObject")
}

// ToError returns value when it is already an error, otherwise an error whose text is String(value).
func ToError(value any) error {
	panic("unported: ToError")
}

// IsJsonRpcId reports whether value is a string or a finite number.
func IsJsonRpcId(value any) bool {
	panic("unported: IsJsonRpcId")
}

// IsJsonRpcRequest reports whether message is a JSON-RPC request.
func IsJsonRpcRequest(message any) bool {
	panic("unported: IsJsonRpcRequest")
}

// IsJsonRpcNotification reports whether message is a JSON-RPC notification.
func IsJsonRpcNotification(message any) bool {
	panic("unported: IsJsonRpcNotification")
}

// IsJsonRpcResponse reports whether message is a JSON-RPC success or error response.
func IsJsonRpcResponse(message any) bool {
	panic("unported: IsJsonRpcResponse")
}

// ParseJsonRpcMessage returns message when it is a valid JSON-RPC message.
// Otherwise it returns *McpError with JsonRpcErrorCodes.InvalidRequest and
// the message "Invalid JSON-RPC message".
func ParseJsonRpcMessage(value any) (JsonRpcMessage, error) {
	panic("unported: ParseJsonRpcMessage")
}
