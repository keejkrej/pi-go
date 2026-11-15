package protocol

import (
	"errors"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestJsonRpc_ParseAndDecodeMessages(t *testing.T) {
	reqBytes := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"c1"}}`)
	msg, err := UnmarshalJsonRpcMessage(reqBytes)
	if err != nil {
		t.Fatalf("UnmarshalJsonRpcMessage(req): %v", err)
	}
	req, ok := msg.(*JsonRpcRequest)
	if !ok {
		t.Fatalf("expected *JsonRpcRequest, got %T", msg)
	}
	if req.Method != "tools/list" || req.Id.IsString || req.Id.Number != 1 {
		t.Fatalf("unexpected request: %#v", req)
	}

	notifBytes := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1,"progressToken":"p"}}`)
	nMsg, err := UnmarshalJsonRpcMessage(notifBytes)
	if err != nil {
		t.Fatalf("UnmarshalJsonRpcMessage(notif): %v", err)
	}
	if _, ok := nMsg.(*JsonRpcNotification); !ok {
		t.Fatalf("expected *JsonRpcNotification, got %T", nMsg)
	}

	resBytes := []byte(`{"jsonrpc":"2.0","id":"abc","result":{"ok":true}}`)
	rMsg, err := UnmarshalJsonRpcMessage(resBytes)
	if err != nil {
		t.Fatalf("UnmarshalJsonRpcMessage(result): %v", err)
	}
	res, ok := rMsg.(*JsonRpcSuccessResponse)
	if !ok || !res.Id.IsString || res.Id.String != "abc" {
		t.Fatalf("unexpected result: %#v", rMsg)
	}

	errBytes := []byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32601,"message":"not found","data":{"x":1}}}`)
	eMsg, err := UnmarshalJsonRpcMessage(errBytes)
	if err != nil {
		t.Fatalf("UnmarshalJsonRpcMessage(error): %v", err)
	}
	eRes, ok := eMsg.(*JsonRpcErrorResponse)
	if !ok || eRes.Id.IsString || eRes.Id.Number != 2 || eRes.Error.Code != -32601 {
		t.Fatalf("unexpected error response: %#v", eMsg)
	}

	if _, err := ParseJsonRpcMessage(jsonx.ObjectOf("jsonrpc", "1.0", "method", "ping")); err == nil {
		t.Fatal("expected ParseJsonRpcMessage to reject jsonrpc 1.0")
	}
	uMsg, err := UnmarshalJsonRpcMessage([]byte(`{"jsonrpc":"2.0","id":true,"method":"ping"}`))
	if err != nil {
		t.Fatalf("unexpected unmarshal error for unknown shape: %v", err)
	}
	if _, err := ParseJsonRpcMessage(uMsg); err == nil {
		t.Fatal("expected ParseJsonRpcMessage to reject invalid id type")
	}
}

func TestJsonRpc_ErrorsAndRoundTrip(t *testing.T) {
	mcpErr := NewMcpError(1234, "boom", map[string]any{"retry": true})
	var asMcp *McpError
	if !errors.As(mcpErr, &asMcp) || asMcp.Code != 1234 || asMcp.Error() != "boom" {
		t.Fatalf("unexpected McpError: %#v", mcpErr)
	}
	if ToError("raw") == nil || ToError(mcpErr) != mcpErr {
		t.Fatal("unexpected ToError behavior")
	}
	if NewMcpConnectionClosedError("").Name() != "McpConnectionClosedError" {
		t.Fatal("unexpected McpConnectionClosedError name")
	}
	if NewMcpTimeoutError(500).Name() != "McpTimeoutError" {
		t.Fatal("unexpected McpTimeoutError name")
	}
	if NewMcpAbortError("").Name() != "AbortError" {
		t.Fatal("unexpected McpAbortError name")
	}

	orig := &JsonRpcErrorResponse{
		Jsonrpc: "2.0",
		Id:      JsonRpcId{Number: 0},
		Error:   JsonRpcErrorObject{Code: -32700, Message: "parse error"},
	}
	b, err := jsonx.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded, err := UnmarshalJsonRpcMessage(b)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	back, ok := decoded.(*JsonRpcErrorResponse)
	if !ok || back.Id.IsString || back.Id.Number != 0 || back.Error.Code != -32700 {
		t.Fatalf("round-trip mismatch: %#v", decoded)
	}
}
