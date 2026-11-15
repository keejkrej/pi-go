// Ported from packages/coding-agent/test/experimental-radius-relay.test.ts (pi v1.0.0).

package experimental_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
)

const (
	terrServerId     = protocol.ServerId("00000000-0000-4000-8000-000000000001")
	terrConnectionId = "00000000-0000-4000-8000-000000000002"
)

type terrReadFrame struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

type terrSentFrame struct {
	typ  websocket.MessageType
	data []byte
}

type terrDialRecord struct {
	socket        *terrFakeWebSocket
	endpointURL   string
	subprotocol   string
	authorization string
}

type terrFakeWebSocket struct {
	subprotocol string
	readCh      chan terrReadFrame
	mu          sync.Mutex
	sent        []terrSentFrame
	sentCh      chan terrSentFrame
	closed      bool
}

func terrNewFakeWebSocket(subprotocol string) *terrFakeWebSocket {
	return &terrFakeWebSocket{
		subprotocol: subprotocol,
		readCh:      make(chan terrReadFrame, 32),
		sentCh:      make(chan terrSentFrame, 32),
	}
}

func (s *terrFakeWebSocket) Subprotocol() string {
	return s.subprotocol
}

func (s *terrFakeWebSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case frame, ok := <-s.readCh:
		if !ok {
			return 0, nil, websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "closed"}
		}
		return frame.typ, frame.data, frame.err
	}
}

func (s *terrFakeWebSocket) Write(_ context.Context, typ websocket.MessageType, p []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("socket is not open")
	}
	cp := bytes.Clone(p)
	frame := terrSentFrame{typ: typ, data: cp}
	s.sent = append(s.sent, frame)
	s.mu.Unlock()
	select {
	case s.sentCh <- frame:
	default:
	}
	return nil
}

func (s *terrFakeWebSocket) Close(code websocket.StatusCode, reason string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	select {
	case s.readCh <- terrReadFrame{err: websocket.CloseError{Code: code, Reason: reason}}:
	default:
	}
	return nil
}

func (s *terrFakeWebSocket) CloseNow() error {
	return s.Close(websocket.StatusNormalClosure, "")
}

func (s *terrFakeWebSocket) pushText(text string) {
	s.readCh <- terrReadFrame{typ: websocket.MessageText, data: []byte(text)}
}

func (s *terrFakeWebSocket) pushBinary(data []byte) {
	s.readCh <- terrReadFrame{typ: websocket.MessageBinary, data: bytes.Clone(data)}
}

func (s *terrFakeWebSocket) pushErr(err error) {
	s.readCh <- terrReadFrame{err: err}
}

func (s *terrFakeWebSocket) sentCopy() []terrSentFrame {
	s.mu.Lock()
	out := make([]terrSentFrame, len(s.sent))
	copy(out, s.sent)
	s.mu.Unlock()
	return out
}

func terrSocketFactory(negotiateSubprotocol ...string) (*[]terrDialRecord, chan terrDialRecord, experimental.RadiusRelayWebSocketFactory) {
	var mu sync.Mutex
	dials := make([]terrDialRecord, 0, 4)
	dialCh := make(chan terrDialRecord, 16)
	factory := func(_ context.Context, endpointURL string, subprotocol string, authorization string) (experimental.RadiusRelayWebSocket, error) {
		chosen := subprotocol
		if len(negotiateSubprotocol) > 0 {
			chosen = negotiateSubprotocol[0]
		}
		sock := terrNewFakeWebSocket(chosen)
		rec := terrDialRecord{
			socket:        sock,
			endpointURL:   endpointURL,
			subprotocol:   subprotocol,
			authorization: authorization,
		}
		mu.Lock()
		dials = append(dials, rec)
		mu.Unlock()
		dialCh <- rec
		return sock, nil
	}
	return &dials, dialCh, factory
}

func terrWaitUntil(t *testing.T, desc string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

type terrFakeReconnectClient struct {
	mu                  sync.Mutex
	connected           bool
	state               client.ConnectionState
	attachment          *protocol.SessionTarget
	connectionListeners map[int]func(client.ConnectionStateChange)
	attachmentListeners map[int]client.AttachmentChangeListener
	nextId              int
	attempts            atomic.Int32
}

func terrNewFakeReconnectClient() *terrFakeReconnectClient {
	return &terrFakeReconnectClient{
		connected:           true,
		state:               client.ConnectionState("connected"),
		attachment:          &protocol.SessionTarget{ServerId: terrServerId, SessionId: "demo-1", AttachmentId: "att-1"},
		connectionListeners: make(map[int]func(client.ConnectionStateChange)),
		attachmentListeners: make(map[int]client.AttachmentChangeListener),
	}
}

func (c *terrFakeReconnectClient) Attachment() *protocol.SessionTarget {
	c.mu.Lock()
	out := c.attachment
	c.mu.Unlock()
	return out
}

func (c *terrFakeReconnectClient) Connected() bool {
	c.mu.Lock()
	out := c.connected
	c.mu.Unlock()
	return out
}

func (c *terrFakeReconnectClient) ConnectionState() client.ConnectionState {
	c.mu.Lock()
	out := c.state
	c.mu.Unlock()
	return out
}

func (c *terrFakeReconnectClient) Disconnect(reason string) {
	c.mu.Lock()
	c.connected = false
	c.state = client.ConnectionStateDisconnected
	listeners := make([]func(client.ConnectionStateChange), 0, len(c.connectionListeners))
	for _, fn := range c.connectionListeners {
		listeners = append(listeners, fn)
	}
	c.mu.Unlock()
	for _, fn := range listeners {
		fn(client.ConnectionStateChange{State: client.ConnectionStateDisconnected})
	}
}

func (c *terrFakeReconnectClient) OnAttachmentChange(listener client.AttachmentChangeListener) (client.Unsubscribe, error) {
	c.mu.Lock()
	id := c.nextId
	c.nextId++
	c.attachmentListeners[id] = listener
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.attachmentListeners, id)
		c.mu.Unlock()
	}, nil
}

func (c *terrFakeReconnectClient) OnConnectionStateChange(listener func(change client.ConnectionStateChange)) (client.Unsubscribe, error) {
	c.mu.Lock()
	id := c.nextId
	c.nextId++
	c.connectionListeners[id] = listener
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.connectionListeners, id)
		c.mu.Unlock()
	}, nil
}

func (c *terrFakeReconnectClient) Reconnect() (*protocol.ServerHello, error) {
	attempt := c.attempts.Add(1)
	if attempt == 1 {
		return nil, errors.New("temporary failure")
	}
	c.mu.Lock()
	c.connected = true
	c.state = client.ConnectionState("connected")
	listeners := make([]func(client.ConnectionStateChange), 0, len(c.connectionListeners))
	for _, fn := range c.connectionListeners {
		listeners = append(listeners, fn)
	}
	c.mu.Unlock()
	for _, fn := range listeners {
		fn(client.ConnectionStateChange{State: client.ConnectionState("connected")})
	}
	return &protocol.ServerHello{
		Type:     "hello",
		Version:  protocol.ProtocolVersion,
		ServerId: terrServerId,
	}, nil
}

func TestExperimentalRadiusRelayMatchesMultiplexingEnvelope(t *testing.T) {
	payload := []byte{0, 1, 2, 255}
	frame, err := experimental.EncodeRelayDataFrame(terrConnectionId, payload)
	if err != nil {
		t.Fatalf("EncodeRelayDataFrame: %v", err)
	}
	gotId, gotPayload, ok := experimental.ParseRelayDataFrame(frame)
	if !ok {
		t.Fatalf("ParseRelayDataFrame returned ok=false")
	}
	if gotId != terrConnectionId {
		t.Fatalf("connectionId = %q, want %q", gotId, terrConnectionId)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload = %v, want %v", gotPayload, payload)
	}
}

func TestExperimentalRadiusRelayBridgesMultiplexedHostClients(t *testing.T) {
	_, dialCh, factory := terrSocketFactory()
	var mu sync.Mutex
	var received [][]byte
	var accepted server.ByteConnection
	var statuses []string
	var onCloseCalled atomic.Bool

	host := experimental.NewRadiusRelayHost(experimental.RadiusRelayHostOptions{
		ServerId: terrServerId,
		Accept: func(connection server.ByteConnection) server.ByteConnectionHandler {
			mu.Lock()
			accepted = connection
			mu.Unlock()
			return server.ByteConnectionHandler{
				OnData: func(chunk []byte) {
					mu.Lock()
					received = append(received, bytes.Clone(chunk))
					mu.Unlock()
				},
				OnClose: func() {
					onCloseCalled.Store(true)
				},
				OnError: func(error) {},
			}
		},
		Auth:             experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
		WebSocketFactory: factory,
		OnStatus: func(status experimental.RadiusRelayHostStatus) {
			mu.Lock()
			statuses = append(statuses, status.Status)
			mu.Unlock()
		},
	})
	t.Cleanup(func() {
		_ = host.Close()
	})
	host.Start()

	var dial terrDialRecord
	select {
	case dial = <-dialCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial host WebSocket dial")
	}
	if dial.authorization != "Bearer secret" {
		t.Fatalf("authorization = %q, want Bearer secret", dial.authorization)
	}
	if dial.subprotocol != experimental.RadiusRelayHostSubprotocol {
		t.Fatalf("subprotocol = %q, want %q", dial.subprotocol, experimental.RadiusRelayHostSubprotocol)
	}

	terrWaitUntil(t, "connected status", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, st := range statuses {
			if st == "connected" {
				return true
			}
		}
		return false
	})

	openBytes, _ := jsonx.Marshal(map[string]any{
		"version":       1,
		"type":          "connection_open",
		"connection_id": terrConnectionId,
	})
	dial.socket.pushText(string(openBytes))
	terrWaitUntil(t, "accepted ByteConnection", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return accepted != nil
	})

	inboundFrame, err := experimental.EncodeRelayDataFrame(terrConnectionId, []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("EncodeRelayDataFrame(inbound): %v", err)
	}
	dial.socket.pushBinary(inboundFrame)
	terrWaitUntil(t, "inbound chunk delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 1
	})
	if !bytes.Equal(received[0], []byte{1, 2, 3}) {
		t.Fatalf("received[0] = %v, want [1 2 3]", received[0])
	}

	mu.Lock()
	conn := accepted
	mu.Unlock()
	if err := conn.Send([]byte{4, 5, 6}); err != nil {
		t.Fatalf("accepted.Send: %v", err)
	}
	terrWaitUntil(t, "outbound binary relay frame", func() bool {
		for _, frame := range dial.socket.sentCopy() {
			if frame.typ == websocket.MessageBinary {
				return true
			}
		}
		return false
	})
	sent := dial.socket.sentCopy()
	last := sent[len(sent)-1]
	parsedId, parsedPayload, ok := experimental.ParseRelayDataFrame(last.data)
	if !ok || parsedId != terrConnectionId || !bytes.Equal(parsedPayload, []byte{4, 5, 6}) {
		t.Fatalf("unexpected outbound frame: ok=%v id=%q payload=%v", ok, parsedId, parsedPayload)
	}

	closeBytes, _ := jsonx.Marshal(map[string]any{
		"version":       1,
		"type":          "connection_close",
		"connection_id": terrConnectionId,
	})
	dial.socket.pushText(string(closeBytes))
	terrWaitUntil(t, "connection OnClose", onCloseCalled.Load)

	if err := host.Close(); err != nil {
		t.Fatalf("host.Close: %v", err)
	}
}

func TestExperimentalRadiusRelayReconnectsServerHostAfterDrop(t *testing.T) {
	_, dialCh, factory := terrSocketFactory()
	sleepCh := make(chan time.Duration, 4)
	releaseCh := make(chan struct{}, 4)

	host := experimental.NewRadiusRelayHost(experimental.RadiusRelayHostOptions{
		ServerId: terrServerId,
		Accept: func(connection server.ByteConnection) server.ByteConnectionHandler {
			return server.ByteConnectionHandler{}
		},
		Auth:             experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
		WebSocketFactory: factory,
		SleepFunc: func(ctx context.Context, d time.Duration) bool {
			sleepCh <- d
			select {
			case <-ctx.Done():
				return false
			case <-releaseCh:
				return true
			}
		},
	})
	t.Cleanup(func() {
		_ = host.Close()
	})
	host.Start()

	var first terrDialRecord
	select {
	case first = <-dialCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first host dial")
	}

	first.socket.pushErr(websocket.CloseError{Code: websocket.StatusCode(1006), Reason: "lost"})
	select {
	case d := <-sleepCh:
		if d != time.Second {
			t.Fatalf("retry delay = %v, want 1s", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for retry backoff sleep")
	}
	releaseCh <- struct{}{}

	select {
	case <-dialCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second host dial")
	}
	if err := host.Close(); err != nil {
		t.Fatalf("host.Close: %v", err)
	}
}

func TestExperimentalRadiusRelayClientByteTransportLifecycle(t *testing.T) {
	t.Run("uses authenticated WebSocket as client byte transport", func(t *testing.T) {
		_, dialCh, factory := terrSocketFactory()
		var mu sync.Mutex
		var received [][]byte
		var onCloseCount atomic.Int32
		var onErrorCount atomic.Int32

		transport, err := experimental.CreateRadiusClientTransportFactory(experimental.RadiusClientTransportOptions{
			ServerId:         terrServerId,
			Auth:             experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
			WebSocketFactory: factory,
		})(client.ByteTransportHandlers{
			OnData: func(chunk []byte) {
				mu.Lock()
				received = append(received, bytes.Clone(chunk))
				mu.Unlock()
			},
			OnClose: func() {
				onCloseCount.Add(1)
			},
			OnError: func(error) {
				onErrorCount.Add(1)
			},
		})
		if err != nil {
			t.Fatalf("CreateRadiusClientTransportFactory: %v", err)
		}
		defer transport.Close()

		var dial terrDialRecord
		select {
		case dial = <-dialCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for client WebSocket dial")
		}
		if dial.authorization != "Bearer secret" {
			t.Fatalf("authorization = %q, want Bearer secret", dial.authorization)
		}

		if err := transport.Send([]byte{1, 2, 3}); err != nil {
			t.Fatalf("transport.Send: %v", err)
		}
		terrWaitUntil(t, "client outbound frame", func() bool {
			return len(dial.socket.sentCopy()) == 1
		})
		if !bytes.Equal(dial.socket.sentCopy()[0].data, []byte{1, 2, 3}) {
			t.Fatalf("sent[0] = %v, want [1 2 3]", dial.socket.sentCopy()[0].data)
		}

		dial.socket.pushBinary([]byte{4, 5, 6})
		terrWaitUntil(t, "client inbound chunk", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(received) == 1
		})
		if !bytes.Equal(received[0], []byte{4, 5, 6}) {
			t.Fatalf("received[0] = %v, want [4 5 6]", received[0])
		}

		dial.socket.pushErr(websocket.CloseError{Code: websocket.StatusCode(1006), Reason: "lost"})
		terrWaitUntil(t, "client OnClose", func() bool {
			return onCloseCount.Load() == 1
		})
		if onErrorCount.Load() != 0 {
			t.Fatalf("expected OnError count 0, got %d", onErrorCount.Load())
		}
	})

	t.Run("reports established abnormal closures as errors so client can reconnect", func(t *testing.T) {
		_, dialCh, factory := terrSocketFactory()
		var mu sync.Mutex
		var gotErr error
		var onCloseCount atomic.Int32

		transport, err := experimental.CreateRadiusClientTransportFactory(experimental.RadiusClientTransportOptions{
			ServerId:         terrServerId,
			Auth:             experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
			WebSocketFactory: factory,
		})(client.ByteTransportHandlers{
			OnData: func([]byte) {},
			OnClose: func() {
				onCloseCount.Add(1)
			},
			OnError: func(err error) {
				mu.Lock()
				gotErr = err
				mu.Unlock()
			},
		})
		if err != nil {
			t.Fatalf("CreateRadiusClientTransportFactory: %v", err)
		}
		defer transport.Close()

		dial := <-dialCh
		dial.socket.pushErr(errors.New("network lost"))
		terrWaitUntil(t, "client OnError", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return gotErr != nil
		})
		if !strings.Contains(gotErr.Error(), "network lost") {
			t.Fatalf("gotErr = %v, want network lost", gotErr)
		}
		if onCloseCount.Load() != 0 {
			t.Fatalf("expected OnClose count 0, got %d", onCloseCount.Load())
		}
	})

	t.Run("rejects unexpected subprotocol and surfaces dial failure", func(t *testing.T) {
		_, _, mismatchFactory := terrSocketFactory("wrong-protocol")
		_, err := experimental.CreateRadiusClientTransportFactory(experimental.RadiusClientTransportOptions{
			ServerId:         terrServerId,
			Auth:             experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
			WebSocketFactory: mismatchFactory,
		})(client.ByteTransportHandlers{})
		if err == nil || !strings.Contains(err.Error(), "unexpected WebSocket protocol") {
			t.Fatalf("expected unexpected WebSocket protocol error, got %v", err)
		}

		_, err = experimental.CreateRadiusClientTransportFactory(experimental.RadiusClientTransportOptions{
			ServerId: terrServerId,
			Auth:     experimental.NewRadiusRelayAuthResolver(&experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"}),
			WebSocketFactory: func(context.Context, string, string, string) (experimental.RadiusRelayWebSocket, error) {
				return nil, errors.New("Radius WebSocket connection failed")
			},
		})(client.ByteTransportHandlers{})
		if err == nil || !strings.Contains(err.Error(), "Radius WebSocket connection failed") {
			t.Fatalf("expected Radius WebSocket connection failed error, got %v", err)
		}
	})
}

func TestExperimentalRadiusRelayReconnectsEstablishedClientAndRestoresSession(t *testing.T) {
	fakeClient := terrNewFakeReconnectClient()
	var mu sync.Mutex
	var reattached []string
	reattachDone := make(chan string, 2)
	sleepCh := make(chan time.Duration, 4)
	releaseCh := make(chan struct{}, 4)

	reconnect, err := experimental.NewRadiusClientReconnect(
		fakeClient,
		func(sessionId string) error {
			mu.Lock()
			reattached = append(reattached, sessionId)
			mu.Unlock()
			reattachDone <- sessionId
			return nil
		},
		experimental.RadiusClientReconnectOptions{
			SleepFunc: func(ctx context.Context, d time.Duration) bool {
				sleepCh <- d
				select {
				case <-ctx.Done():
					return false
				case <-releaseCh:
					return true
				}
			},
		},
	)
	if err != nil {
		t.Fatalf("NewRadiusClientReconnect: %v", err)
	}
	defer func() {
		_ = reconnect.Dispose()
	}()

	fakeClient.Disconnect("connection lost")

	select {
	case d := <-sleepCh:
		if d != time.Second {
			t.Fatalf("first reconnect backoff = %v, want 1s", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first reconnect backoff")
	}
	releaseCh <- struct{}{}

	terrWaitUntil(t, "first reconnect attempt", func() bool {
		return fakeClient.attempts.Load() == 1
	})

	select {
	case d := <-sleepCh:
		if d != 2*time.Second {
			t.Fatalf("second reconnect backoff = %v, want 2s", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second reconnect backoff")
	}
	releaseCh <- struct{}{}

	select {
	case sid := <-reattachDone:
		if sid != "demo-1" {
			t.Fatalf("reattached sessionId = %q, want demo-1", sid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for session reattach")
	}
	if got := fakeClient.attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	mu.Lock()
	gotReattached := append([]string(nil), reattached...)
	mu.Unlock()
	if len(gotReattached) != 1 || gotReattached[0] != "demo-1" {
		t.Fatalf("reattached = %v, want [demo-1]", gotReattached)
	}
}
