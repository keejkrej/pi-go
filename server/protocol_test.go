// Ported from packages/server/test/protocol.test.ts (pi v1.0.0).

package server_test

import (
	"context"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/protocol/cbor"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
)

const (
	tprotServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")
	tprotTimeout  = 5 * time.Second
)

type tprotInMemoryWire struct {
	mu      sync.Mutex
	closed  bool
	handler server.ByteConnectionHandler
	client  *servertest.ProtocolTestClient
}

func (w *tprotInMemoryWire) Closed() bool {
	w.mu.Lock()
	c := w.closed
	w.mu.Unlock()
	return c
}

func (w *tprotInMemoryWire) Send(chunk []byte) error {
	w.client.Receive(chunk)
	return nil
}

func (w *tprotInMemoryWire) Close(finalChunk []byte) error {
	if len(finalChunk) > 0 {
		w.client.Receive(finalChunk)
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.client.MarkClosed()
	return nil
}

type tprotWireChannel struct {
	wire *tprotInMemoryWire
}

func (c *tprotWireChannel) Send(chunk []byte) error {
	if c.wire.handler.OnData != nil {
		c.wire.handler.OnData(chunk)
	}
	return nil
}

func (c *tprotWireChannel) SendFragmented(chunk []byte, splitAt int) error {
	if c.wire.handler.OnData != nil {
		c.wire.handler.OnData(chunk[:splitAt])
		c.wire.handler.OnData(chunk[splitAt:])
	}
	return nil
}

func (c *tprotWireChannel) Close() error {
	c.wire.mu.Lock()
	if c.wire.closed {
		c.wire.mu.Unlock()
		return nil
	}
	c.wire.closed = true
	h := c.wire.handler
	c.wire.mu.Unlock()

	if h.OnClose != nil {
		h.OnClose()
	}
	c.wire.client.MarkClosed()
	return nil
}

func tprotConnect(t *testing.T) *servertest.ProtocolTestClient {
	t.Helper()
	srv, err := server.NewServer(servertest.NewTestServerHost(), server.ServerOptions{
		Listeners: []server.ServerListener{},
		ServerId:  tprotServerId,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	wire := &tprotInMemoryWire{}
	channel := &tprotWireChannel{wire: wire}
	client := servertest.NewProtocolTestClient(channel)
	wire.client = client
	wire.handler = srv.Accept(wire)
	return client
}

func TestProtocolRequiresHelloAsFirstMessage(t *testing.T) {
	c := tprotConnect(t)
	callVal, _ := jsonx.Encode(chord.ServiceCall{ServiceId: "pi.session-directory", Member: "list", Args: []chord.JsonValue{}})
	if err := c.SendMessage(&protocol.RequestEnvelope{
		Type:   "request",
		Id:     "request-1",
		Target: &protocol.ServerTarget{ServerId: tprotServerId},
		Call:   callVal,
	}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
	defer cancel()
	msg, err := c.Next(ctx, func(m protocol.ServerMessage) bool {
		_, ok := m.(*protocol.ServerHelloError)
		return ok
	})
	if err != nil {
		t.Fatalf("Next hello_error failed: %v", err)
	}
	he := msg.(*protocol.ServerHelloError)
	if he.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", he)
	}
	if err := c.WaitForClose(ctx); err != nil {
		t.Fatalf("WaitForClose failed: %v", err)
	}
}

func TestProtocolRejectsUnsupportedVersions(t *testing.T) {
	c := tprotConnect(t)
	ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
	defer cancel()
	msg, err := c.Hello(ctx, protocol.ProtocolVersion+1)
	if err != nil {
		t.Fatalf("Hello failed: %v", err)
	}
	he, ok := msg.(*protocol.ServerHelloError)
	if !ok || he.Error.Code != "version" {
		t.Fatalf("expected ServerHelloError version, got %#v", msg)
	}
	if err := c.WaitForClose(ctx); err != nil {
		t.Fatalf("WaitForClose failed: %v", err)
	}
}

func TestProtocolAcceptsFragmentedHelloAndRequestFrames(t *testing.T) {
	c := tprotConnect(t)
	ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
	defer cancel()

	helloMsg := &protocol.ClientHello{Type: "hello", Version: protocol.ProtocolVersion}
	helloFrame, err := protocol.EncodeClientMessage(helloMsg, nil)
	if err != nil {
		t.Fatalf("EncodeClientMessage failed: %v", err)
	}

	helloCh := make(chan protocol.ServerMessage, 1)
	helloErrCh := make(chan error, 1)
	go func() {
		m, e := c.Next(ctx, func(m protocol.ServerMessage) bool {
			_, ok := m.(*protocol.ServerHello)
			return ok
		})
		if e != nil {
			helloErrCh <- e
			return
		}
		helloCh <- m
	}()
	time.Sleep(5 * time.Millisecond)

	if err := c.SendFragmentedMessage(helloMsg, len(helloFrame)/2); err != nil {
		t.Fatalf("SendFragmentedMessage(hello) failed: %v", err)
	}
	select {
	case e := <-helloErrCh:
		t.Fatalf("Next(hello) failed: %v", e)
	case m := <-helloCh:
		sh := m.(*protocol.ServerHello)
		if sh.ServerId != tprotServerId {
			t.Fatalf("unexpected ServerId: %s", sh.ServerId)
		}
	}

	callVal, _ := jsonx.Encode(chord.ServiceCall{ServiceId: "pi.session-directory", Member: "list", Args: []chord.JsonValue{}})
	reqMsg := &protocol.RequestEnvelope{
		Type:   "request",
		Id:     "request-1",
		Target: &protocol.ServerTarget{ServerId: tprotServerId},
		Call:   callVal,
	}
	reqFrame, err := protocol.EncodeClientMessage(reqMsg, nil)
	if err != nil {
		t.Fatalf("EncodeClientMessage(req) failed: %v", err)
	}

	respCh := make(chan protocol.ServerMessage, 1)
	respErrCh := make(chan error, 1)
	go func() {
		m, e := c.Next(ctx, func(m protocol.ServerMessage) bool {
			switch m.(type) {
			case *protocol.ResponseSuccess, *protocol.ResponseFailure:
				return true
			default:
				return false
			}
		})
		if e != nil {
			respErrCh <- e
			return
		}
		respCh <- m
	}()
	time.Sleep(5 * time.Millisecond)

	if err := c.SendFragmentedMessage(reqMsg, len(reqFrame)/2); err != nil {
		t.Fatalf("SendFragmentedMessage(req) failed: %v", err)
	}
	select {
	case e := <-respErrCh:
		t.Fatalf("Next(response) failed: %v", e)
	case m := <-respCh:
		rf, ok := m.(*protocol.ResponseFailure)
		if !ok || rf.Error.Code != "internal_error" {
			t.Fatalf("expected internal_error response, got %#v", m)
		}
	}
}

func TestProtocolRejectsHostileFramedInput(t *testing.T) {
	malformed, _ := protocol.EncodeFrame([]byte{0xff})
	schemaPayload, _ := cbor.EncodeCbor(jsonx.ObjectOf("type", "hello", "version", float64(1), "extra", true), nil)
	schemaInvalid, _ := protocol.EncodeFrame(schemaPayload)
	oversized := []byte{1, 0, 0, 1}

	cases := []struct {
		name  string
		bytes []byte
	}{
		{"malformed CBOR", malformed},
		{"schema-invalid CBOR", schemaInvalid},
		{"oversized frame", oversized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tprotConnect(t)
			ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
			defer cancel()

			if err := c.SendBytes(tc.bytes); err != nil {
				t.Fatalf("SendBytes failed: %v", err)
			}
			msg, err := c.Next(ctx, func(m protocol.ServerMessage) bool {
				_, ok := m.(*protocol.ServerHelloError)
				return ok
			})
			if err != nil {
				t.Fatalf("Next(hello_error) failed: %v", err)
			}
			he := msg.(*protocol.ServerHelloError)
			if he.Error.Code != "invalid_request" {
				t.Fatalf("expected invalid_request, got %+v", he)
			}
			if err := c.WaitForClose(ctx); err != nil {
				t.Fatalf("WaitForClose failed: %v", err)
			}
		})
	}
}

func TestProtocolRejectsSecondHelloAfterHandshake(t *testing.T) {
	c := tprotConnect(t)
	ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
	defer cancel()

	if _, err := c.Hello(ctx); err != nil {
		t.Fatalf("initial Hello failed: %v", err)
	}
	if err := c.SendMessage(&protocol.ClientHello{Type: "hello", Version: protocol.ProtocolVersion}); err != nil {
		t.Fatalf("second ClientHello failed: %v", err)
	}
	msg, err := c.Next(ctx, func(m protocol.ServerMessage) bool {
		_, ok := m.(*protocol.ServerHelloError)
		return ok
	})
	if err != nil {
		t.Fatalf("Next(hello_error) failed: %v", err)
	}
	he := msg.(*protocol.ServerHelloError)
	if he.Error.Code != "invalid_request" || !regexp.MustCompile(`first message`).MatchString(he.Error.Message) {
		t.Fatalf("unexpected hello_error: %+v", he)
	}
	if err := c.WaitForClose(ctx); err != nil {
		t.Fatalf("WaitForClose failed: %v", err)
	}
}

func TestProtocolProcessesHelloAndRequestCoalescedInOneChunk(t *testing.T) {
	c := tprotConnect(t)
	ctx, cancel := context.WithTimeout(context.Background(), tprotTimeout)
	defer cancel()

	hello, err := protocol.EncodeClientMessage(&protocol.ClientHello{Type: "hello", Version: protocol.ProtocolVersion}, nil)
	if err != nil {
		t.Fatalf("EncodeClientMessage(hello) failed: %v", err)
	}
	callVal, _ := jsonx.Encode(chord.ServiceCall{ServiceId: "pi.session-directory", Member: "list", Args: []chord.JsonValue{}})
	request, err := protocol.EncodeClientMessage(&protocol.RequestEnvelope{
		Type:   "request",
		Id:     "request-1",
		Target: &protocol.ServerTarget{ServerId: tprotServerId},
		Call:   callVal,
	}, nil)
	if err != nil {
		t.Fatalf("EncodeClientMessage(request) failed: %v", err)
	}

	wire := make([]byte, len(hello)+len(request))
	copy(wire, hello)
	copy(wire[len(hello):], request)

	if err := c.SendBytes(wire); err != nil {
		t.Fatalf("SendBytes failed: %v", err)
	}
	if _, err := c.Next(ctx, func(m protocol.ServerMessage) bool {
		_, ok := m.(*protocol.ServerHello)
		return ok
	}); err != nil {
		t.Fatalf("Next(hello) failed: %v", err)
	}
	respMsg, err := c.Next(ctx, func(m protocol.ServerMessage) bool {
		switch m.(type) {
		case *protocol.ResponseSuccess, *protocol.ResponseFailure:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatalf("Next(response) failed: %v", err)
	}
	rf, ok := respMsg.(*protocol.ResponseFailure)
	if !ok || rf.Id != "request-1" || rf.Error.Code != "internal_error" {
		t.Fatalf("unexpected response: %#v", respMsg)
	}
}

type tprotTruncatedConn struct {
	closed bool
}

func (c *tprotTruncatedConn) Closed() bool       { return c.closed }
func (c *tprotTruncatedConn) Send([]byte) error  { return nil }
func (c *tprotTruncatedConn) Close([]byte) error { c.closed = true; return nil }
func (c *tprotTruncatedConn) isClosed() bool     { return c.closed }
func (c *tprotTruncatedConn) markClosedTest()    { _ = reflect.TypeOf(c) }

func TestProtocolReportsTruncatedFinalFrameOnPeerClose(t *testing.T) {
	var errs []error
	srv, err := server.NewServer(servertest.NewTestServerHost(), server.ServerOptions{
		Listeners: []server.ServerListener{},
		ServerId:  tprotServerId,
		OnError:   func(err error) { errs = append(errs, err) },
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Close() }()

	conn := &tprotTruncatedConn{}
	handler := srv.Accept(conn)
	handler.OnData([]byte{0, 0, 0, 2, 1})
	handler.OnClose()

	if conn.closed {
		t.Fatal("expected peer-closed connection not to be closed by server")
	}
	if len(errs) != 1 || !regexp.MustCompile(`(?i)truncated`).MatchString(errs[0].Error()) {
		t.Fatalf("expected truncated error, got %v", errs)
	}
}
