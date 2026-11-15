// Ported from packages/mcp/src/protocol/jsonrpc.ts (pi v1.0.0).

package protocol

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
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
	if id.IsString {
		return jsonx.Marshal(id.String)
	}
	if math.IsNaN(id.Number) || math.IsInf(id.Number, 0) {
		return nil, errors.New("invalid JSON-RPC id: non-finite number")
	}
	return jsonx.Marshal(id.Number)
}

func (id *JsonRpcId) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		*id = JsonRpcId{IsString: true, String: x}
		return nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errors.New("invalid JSON-RPC id: non-finite number")
		}
		if x == 0 {
			x = 0
		}
		*id = JsonRpcId{Number: x}
		return nil
	default:
		return errors.New("invalid JSON-RPC id")
	}
}

func (u *UnknownJsonRpcResponse) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownJsonRpcMessage) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

// UnmarshalJsonRpcMessage decodes one JSON-RPC message.
// An unrecognized shape becomes *UnknownJsonRpcMessage.
func UnmarshalJsonRpcMessage(data []byte) (JsonRpcMessage, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeJsonRpcMessage(v)
}

// DecodeJsonRpcMessage decodes one JSON-RPC message from a jsonx value.
func DecodeJsonRpcMessage(v any) (JsonRpcMessage, error) {
	switch m := v.(type) {
	case *JsonRpcRequest, *JsonRpcNotification, *JsonRpcSuccessResponse, *JsonRpcErrorResponse, *UnknownJsonRpcResponse, *UnknownJsonRpcMessage:
		return m.(JsonRpcMessage), nil
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected JSON object for JSON-RPC message")
	}
	if msg := jsonDecodeKnownMessage(obj); msg != nil {
		return msg, nil
	}
	return &UnknownJsonRpcMessage{Raw: obj}, nil
}

// UnmarshalJsonRpcResponse decodes one JSON-RPC response.
// An unrecognized shape becomes *UnknownJsonRpcResponse.
func UnmarshalJsonRpcResponse(data []byte) (JsonRpcResponse, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeJsonRpcResponse(v)
}

// DecodeJsonRpcResponse decodes one JSON-RPC response from a jsonx value.
func DecodeJsonRpcResponse(v any) (JsonRpcResponse, error) {
	switch r := v.(type) {
	case *JsonRpcSuccessResponse, *JsonRpcErrorResponse, *UnknownJsonRpcResponse:
		return r.(JsonRpcResponse), nil
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected JSON object for JSON-RPC response")
	}
	if resp := jsonDecodeKnownResponse(obj); resp != nil {
		return resp, nil
	}
	return &UnknownJsonRpcResponse{Raw: obj}, nil
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
	return &McpError{
		Code:    code,
		Message: message,
		Data:    data,
	}
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
	msg := "MCP connection closed"
	if len(message) > 0 {
		msg = message[0]
	}
	return &McpConnectionClosedError{Message: msg}
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
	return &McpTimeoutError{
		TimeoutMs: timeoutMs,
		Message:   fmt.Sprintf("MCP request timed out after %dms", timeoutMs),
	}
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
	msg := "MCP request aborted"
	if len(message) > 0 {
		msg = message[0]
	}
	return &McpAbortError{Message: msg}
}

func (e *McpAbortError) Error() string { return e.Message }

func (e *McpAbortError) Name() string { return "AbortError" }

func (e *McpAbortError) Unwrap() error { return context.Canceled }

// IsObject reports whether value is a non-null JSON object.
func IsObject(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case *jsonx.Object:
		return v != nil
	case jsonx.Object:
		return true
	case *UnknownJsonRpcMessage:
		return v != nil && v.Raw != nil
	case *UnknownJsonRpcResponse:
		return v != nil && v.Raw != nil
	case *UnknownContentBlock:
		return v != nil && v.Raw != nil
	case *UnknownResourceContents:
		return v != nil && v.Raw != nil
	case *UnknownLlmContent:
		return v != nil && v.Raw != nil
	case JsonRpcId, *JsonRpcId, time.Time, *time.Time:
		return false
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
		if rv.Type() == reflect.TypeFor[JsonRpcId]() || rv.Type() == reflect.TypeFor[time.Time]() {
			return false
		}
	}
	switch rv.Kind() {
	case reflect.Map:
		return !rv.IsNil()
	case reflect.Struct:
		return true
	default:
		return false
	}
}

// ToError returns value when it is already an error, otherwise an error whose text is String(value).
func ToError(value any) error {
	if err, ok := value.(error); ok && err != nil {
		rv := reflect.ValueOf(value)
		if !(rv.Kind() == reflect.Pointer && rv.IsNil()) {
			return err
		}
	}
	return errors.New(jsonValueToString(value))
}

// IsJsonRpcId reports whether value is a string or a finite number.
func IsJsonRpcId(value any) bool {
	_, ok := jsonDecodeId(value)
	return ok
}

// IsJsonRpcRequest reports whether message is a JSON-RPC request.
func IsJsonRpcRequest(message any) bool {
	switch r := message.(type) {
	case *JsonRpcRequest:
		return r != nil && r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	case JsonRpcRequest:
		return r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	}
	obj, err := jsonToObject(message)
	if err != nil || obj == nil {
		return false
	}
	return jsonDecodeRequest(obj) != nil
}

// IsJsonRpcNotification reports whether message is a JSON-RPC notification.
func IsJsonRpcNotification(message any) bool {
	switch n := message.(type) {
	case *JsonRpcNotification:
		return n != nil && n.Jsonrpc == "2.0"
	case JsonRpcNotification:
		return n.Jsonrpc == "2.0"
	}
	obj, err := jsonToObject(message)
	if err != nil || obj == nil {
		return false
	}
	return jsonDecodeNotification(obj) != nil
}

// IsJsonRpcResponse reports whether message is a JSON-RPC success or error response.
func IsJsonRpcResponse(message any) bool {
	switch r := message.(type) {
	case *JsonRpcSuccessResponse:
		return r != nil && r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	case JsonRpcSuccessResponse:
		return r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	case *JsonRpcErrorResponse:
		return r != nil && r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	case JsonRpcErrorResponse:
		return r.Jsonrpc == "2.0" && IsJsonRpcId(r.Id)
	}
	obj, err := jsonToObject(message)
	if err != nil || obj == nil {
		return false
	}
	return jsonDecodeKnownResponse(obj) != nil
}

// ParseJsonRpcMessage returns message when it is a valid JSON-RPC message.
// Otherwise it returns *McpError with JsonRpcErrorCodes.InvalidRequest and
// the message "Invalid JSON-RPC message".
func ParseJsonRpcMessage(value any) (JsonRpcMessage, error) {
	invalid := func() error {
		return NewMcpError(JsonRpcErrorCodes.InvalidRequest, "Invalid JSON-RPC message", nil)
	}
	switch m := value.(type) {
	case *JsonRpcRequest:
		if !IsJsonRpcRequest(m) {
			return nil, invalid()
		}
		return m, nil
	case *JsonRpcNotification:
		if !IsJsonRpcNotification(m) {
			return nil, invalid()
		}
		return m, nil
	case *JsonRpcSuccessResponse:
		if !IsJsonRpcResponse(m) {
			return nil, invalid()
		}
		return m, nil
	case *JsonRpcErrorResponse:
		if !IsJsonRpcResponse(m) {
			return nil, invalid()
		}
		return m, nil
	case *UnknownJsonRpcMessage, *UnknownJsonRpcResponse:
		return nil, invalid()
	}
	decoded, err := DecodeJsonRpcMessage(value)
	if err != nil {
		return nil, invalid()
	}
	switch decoded.(type) {
	case *UnknownJsonRpcMessage, *UnknownJsonRpcResponse:
		return nil, invalid()
	default:
		return decoded, nil
	}
}

func jsonToObject(v any) (*jsonx.Object, error) {
	if obj, ok := v.(*jsonx.Object); ok {
		return obj, nil
	}
	if !IsObject(v) {
		return nil, errors.New("expected JSON object")
	}
	enc, err := jsonx.Encode(v)
	if err != nil {
		return nil, err
	}
	obj, ok := enc.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("expected JSON object")
	}
	return obj, nil
}

func jsonDecodeId(v any) (JsonRpcId, bool) {
	switch x := v.(type) {
	case JsonRpcId:
		if !x.IsString && (math.IsNaN(x.Number) || math.IsInf(x.Number, 0)) {
			return JsonRpcId{}, false
		}
		return x, true
	case *JsonRpcId:
		if x == nil {
			return JsonRpcId{}, false
		}
		if !x.IsString && (math.IsNaN(x.Number) || math.IsInf(x.Number, 0)) {
			return JsonRpcId{}, false
		}
		return *x, true
	case string:
		return JsonRpcId{IsString: true, String: x}, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return JsonRpcId{}, false
		}
		if x == 0 {
			x = 0
		}
		return JsonRpcId{Number: x}, true
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return JsonRpcId{}, false
		}
		if f == 0 {
			f = 0
		}
		return JsonRpcId{Number: f}, true
	case int:
		return JsonRpcId{Number: float64(x)}, true
	case int64:
		return JsonRpcId{Number: float64(x)}, true
	case int32:
		return JsonRpcId{Number: float64(x)}, true
	case int16:
		return JsonRpcId{Number: float64(x)}, true
	case int8:
		return JsonRpcId{Number: float64(x)}, true
	case uint:
		return JsonRpcId{Number: float64(x)}, true
	case uint64:
		return JsonRpcId{Number: float64(x)}, true
	case uint32:
		return JsonRpcId{Number: float64(x)}, true
	case uint16:
		return JsonRpcId{Number: float64(x)}, true
	case uint8:
		return JsonRpcId{Number: float64(x)}, true
	default:
		return JsonRpcId{}, false
	}
}

func jsonHasVersion(obj *jsonx.Object) bool {
	v, ok := obj.Get("jsonrpc")
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s == "2.0"
}

func jsonDecodeRequest(obj *jsonx.Object) *JsonRpcRequest {
	if !jsonHasVersion(obj) {
		return nil
	}
	methodVal, hasMethod := obj.Get("method")
	if !hasMethod {
		return nil
	}
	method, ok := methodVal.(string)
	if !ok {
		return nil
	}
	idVal, hasId := obj.Get("id")
	if !hasId {
		return nil
	}
	id, ok := jsonDecodeId(idVal)
	if !ok {
		return nil
	}
	paramsVal, _ := obj.Get("params")
	return &JsonRpcRequest{
		Jsonrpc: "2.0",
		Id:      id,
		Method:  method,
		Params:  paramsVal,
	}
}

func jsonDecodeNotification(obj *jsonx.Object) *JsonRpcNotification {
	if !jsonHasVersion(obj) {
		return nil
	}
	if _, hasId := obj.Get("id"); hasId {
		return nil
	}
	methodVal, hasMethod := obj.Get("method")
	if !hasMethod {
		return nil
	}
	method, ok := methodVal.(string)
	if !ok {
		return nil
	}
	paramsVal, _ := obj.Get("params")
	return &JsonRpcNotification{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  paramsVal,
	}
}

func jsonDecodeKnownResponse(obj *jsonx.Object) JsonRpcResponse {
	if !jsonHasVersion(obj) {
		return nil
	}
	idVal, hasId := obj.Get("id")
	if !hasId {
		return nil
	}
	id, ok := jsonDecodeId(idVal)
	if !ok {
		return nil
	}
	if resVal, hasResult := obj.Get("result"); hasResult {
		return &JsonRpcSuccessResponse{
			Jsonrpc: "2.0",
			Id:      id,
			Result:  resVal,
		}
	}
	errVal, hasErr := obj.Get("error")
	if !hasErr {
		return nil
	}
	errObj, ok := errVal.(*jsonx.Object)
	if !ok || errObj == nil {
		return nil
	}
	codeVal, hasCode := errObj.Get("code")
	msgVal, hasMsg := errObj.Get("message")
	if !hasCode || !hasMsg {
		return nil
	}
	codeFloat, ok := codeVal.(float64)
	if !ok || math.IsNaN(codeFloat) || math.IsInf(codeFloat, 0) {
		return nil
	}
	if codeFloat < float64(math.MinInt) || codeFloat > float64(math.MaxInt) {
		return nil
	}
	msgStr, ok := msgVal.(string)
	if !ok {
		return nil
	}
	dataVal, _ := errObj.Get("data")
	return &JsonRpcErrorResponse{
		Jsonrpc: "2.0",
		Id:      id,
		Error: JsonRpcErrorObject{
			Code:    int(codeFloat),
			Message: msgStr,
			Data:    dataVal,
		},
	}
}

func jsonDecodeKnownMessage(obj *jsonx.Object) JsonRpcMessage {
	if resp := jsonDecodeKnownResponse(obj); resp != nil {
		if msg, ok := resp.(JsonRpcMessage); ok {
			return msg
		}
	}
	if req := jsonDecodeRequest(obj); req != nil {
		return req
	}
	if notif := jsonDecodeNotification(obj); notif != nil {
		return notif
	}
	return nil
}

func jsonValueToString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return js.NumberToString(v)
	case float32:
		return js.NumberToString(float64(v))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	case *jsonx.Object:
		return "[object Object]"
	default:
		return fmt.Sprint(v)
	}
}
