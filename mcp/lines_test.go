// Ported from packages/mcp/test/lines.test.ts (pi v1.5.2).

package mcp

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

func tlinesStartReader(maxMessageBytes *int) (*JsonRpcLineReader, *[]protocol.JsonRpcMessage, *[]string) {
	var messages []protocol.JsonRpcMessage
	var errors []string
	reader := NewJsonRpcLineReader(JsonRpcLineReaderOptions{
		MaxMessageBytes: maxMessageBytes,
		OnMessage: func(msg protocol.JsonRpcMessage) {
			messages = append(messages, msg)
		},
		OnError: func(err error) {
			errors = append(errors, err.Error())
		},
	})
	return reader, &messages, &errors
}

func tlinesFeed(reader *JsonRpcLineReader, data []byte, chunkSize int) {
	for i := 0; i < len(data); i += chunkSize {
		end := min(i+chunkSize, len(data))
		reader.ReadStdout(data[i:end])
	}
}

func TestLines_JoinsMessageSplitAcrossManyChunks(t *testing.T) {
	reader, messages, errors := tlinesStartReader(nil)
	text := strings.Repeat("x", 1024*1024)
	payload, err := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "id", 1, "result", jsonx.ObjectOf("text", text)))
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	tlinesFeed(reader, payload, 4096)

	if len(*errors) != 0 {
		t.Fatalf("unexpected errors: %v", *errors)
	}
	if len(*messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(*messages))
	}
	res, ok := (*messages)[0].(*protocol.JsonRpcSuccessResponse)
	if !ok {
		t.Fatalf("expected *protocol.JsonRpcSuccessResponse, got %T", (*messages)[0])
	}
	obj, ok := res.Result.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected object result, got %T", res.Result)
	}
	gotText, _ := obj.Get("text")
	if gotText != text {
		t.Fatalf("decoded text mismatch (len %d vs %d)", len(gotText.(string)), len(text))
	}
}

func TestLines_MultiByteCRLFBlankLinesAndMultiplePerChunk(t *testing.T) {
	m1, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "id", 1, "result", jsonx.ObjectOf("text", "héllo 🌍")))
	m2, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "method", "notifications/x"))
	m3, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "id", 2, "result", jsonx.NewObject()))
	input := []byte(strings.Join([]string{
		string(m1),
		"",
		string(m2) + "\r",
		"not json",
		string(m3),
		"",
	}, "\n"))

	for chunkSize := 1; chunkSize <= len(input); chunkSize++ {
		reader, messages, errors := tlinesStartReader(nil)
		tlinesFeed(reader, input, chunkSize)
		reader.End()
		if len(*messages) != 3 {
			t.Fatalf("chunkSize=%d: got %d messages, want 3", chunkSize, len(*messages))
		}
		if len(*errors) != 1 {
			t.Fatalf("chunkSize=%d: got %d errors (%v), want 1", chunkSize, len(*errors), *errors)
		}
		res0, ok := (*messages)[0].(*protocol.JsonRpcSuccessResponse)
		if !ok {
			t.Fatalf("chunkSize=%d: msg0 = %T", chunkSize, (*messages)[0])
		}
		if obj, _ := res0.Result.(*jsonx.Object); obj == nil {
			t.Fatalf("chunkSize=%d: expected object result", chunkSize)
		} else if txt, _ := obj.Get("text"); txt != "héllo 🌍" {
			t.Fatalf("chunkSize=%d: text = %q", chunkSize, txt)
		}
		notif, ok := (*messages)[1].(*protocol.JsonRpcNotification)
		if !ok || notif.Method != "notifications/x" {
			t.Fatalf("chunkSize=%d: msg1 = %#v", chunkSize, (*messages)[1])
		}
		if _, ok := (*messages)[2].(*protocol.JsonRpcSuccessResponse); !ok {
			t.Fatalf("chunkSize=%d: msg2 = %T", chunkSize, (*messages)[2])
		}
	}
}

func TestLines_DropsOversizedMessageAndKeepsReading(t *testing.T) {
	maxBytes := 64
	reader, messages, errors := tlinesStartReader(&maxBytes)
	next, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "id", 2, "result", jsonx.NewObject()))
	tlinesFeed(reader, []byte(strings.Repeat("a", 50)), 10)
	tlinesFeed(reader, append([]byte(strings.Repeat("a", 50)+"\n"), append(next, '\n')...), 10)

	if len(*errors) == 0 || (*errors)[0] != "MCP stdio message exceeds 64 bytes" {
		t.Fatalf("errors = %v", *errors)
	}
	if len(*messages) != 1 {
		t.Fatalf("messages = %#v", *messages)
	}
	if res, ok := (*messages)[0].(*protocol.JsonRpcSuccessResponse); !ok || res.Id.Number != 2 {
		t.Fatalf("msg0 = %#v", (*messages)[0])
	}
}

func TestLines_ReportsIncompleteMessageAtEnd(t *testing.T) {
	reader, _, errors := tlinesStartReader(nil)
	tlinesFeed(reader, []byte(`{"jsonrpc":"2.0",`), 4)
	reader.End()
	if len(*errors) != 1 || (*errors)[0] != "MCP stdio server closed with an incomplete JSON-RPC message" {
		t.Fatalf("errors = %v", *errors)
	}
}
