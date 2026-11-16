package experimental

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
)

const (
	// RadiusRelayHostSubprotocol is the WebSocket subprotocol negotiated by hosted experimental servers.
	RadiusRelayHostSubprotocol = "pi-session-relay.host.v1"
	// RadiusRelayClientSubprotocol is the WebSocket subprotocol negotiated by remote experimental clients.
	RadiusRelayClientSubprotocol = "pi-session-relay.client.v1"
)

const (
	rrDataHeaderBytes          = 18
	rrDataFrameVersion         = 1
	rrDataFrameType            = 1
	rrMaxPendingBytes          = protocol.DefaultMaxFrameLength * 4
	rrHostRetryInitialMs       = 1000
	rrHostRetryMaxMs           = 30000
	rrMissingAuthRetryMs       = 30000
	rrClientRetryInitialMs     = 1000
	rrClientRetryMaxMs         = 30000
	rrLocalProtocolErrorClose  = websocket.StatusCode(4000)
	rrLocalTransportErrorClose = websocket.StatusCode(4001)
)

var rrConnectionIdRegexp = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// RadiusRelayWebSocket abstracts an active WebSocket connection for production and mock relay transports.
type RadiusRelayWebSocket interface {
	Subprotocol() string
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Close(code websocket.StatusCode, reason string) error
	CloseNow() error
}

// RadiusRelayWebSocketFactory dials an authenticated WebSocket connection for the given URL and subprotocol.
type RadiusRelayWebSocketFactory func(ctx context.Context, endpointURL string, subprotocol string, authorization string) (RadiusRelayWebSocket, error)

// RadiusRelayHostStatus reports host relay connection lifecycle transitions.
type RadiusRelayHostStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitzero"`
}

// RadiusRelayHostOptions configures RadiusRelayHost.
type RadiusRelayHostOptions struct {
	ServerId         protocol.ServerId
	Accept           server.ByteConnectionAcceptor
	Auth             *RadiusRelayAuthResolver
	WebSocketFactory RadiusRelayWebSocketFactory
	OnStatus         func(status RadiusRelayHostStatus)
	SleepFunc        func(ctx context.Context, d time.Duration) bool
}

type rrControlMessage struct {
	Version      int    `json:"version"`
	Type         string `json:"type"`
	ConnectionId string `json:"connection_id,omitzero"`
	Code         *int   `json:"code,omitzero"`
}

type rrWriteItem struct {
	typ  websocket.MessageType
	data []byte
	done chan error
}

type rrOrderedWriter struct {
	sock         RadiusRelayWebSocket
	ctx          context.Context
	mu           sync.Mutex
	closed       bool
	pendingBytes int
	queue        chan rrWriteItem
	doneCh       chan struct{}
}

func rrNewOrderedWriter(ctx context.Context, sock RadiusRelayWebSocket) *rrOrderedWriter {
	w := &rrOrderedWriter{
		sock:   sock,
		ctx:    ctx,
		queue:  make(chan rrWriteItem, 256),
		doneCh: make(chan struct{}),
	}
	go w.loop()
	return w
}

func (w *rrOrderedWriter) loop() {
	defer close(w.doneCh)
	for {
		var item rrWriteItem
		var ok bool
		select {
		case <-w.ctx.Done():
			w.drainErr(w.ctx.Err())
			return
		case item, ok = <-w.queue:
			if !ok {
				return
			}
		}
		err := w.sock.Write(w.ctx, item.typ, item.data)
		w.mu.Lock()
		w.pendingBytes -= len(item.data)
		w.mu.Unlock()
		item.done <- err
	}
}

func (w *rrOrderedWriter) drainErr(err error) {
	if err == nil {
		err = errors.New("Radius relay WebSocket is closed")
	}
	for {
		select {
		case item, ok := <-w.queue:
			if !ok {
				return
			}
			w.mu.Lock()
			w.pendingBytes -= len(item.data)
			w.mu.Unlock()
			item.done <- err
		default:
			return
		}
	}
}

func (w *rrOrderedWriter) send(typ websocket.MessageType, payload []byte) error {
	copied := make([]byte, len(payload))
	copy(copied, payload)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return errors.New("Radius relay WebSocket is closed")
	}
	if w.pendingBytes+len(copied) > rrMaxPendingBytes {
		w.mu.Unlock()
		return errors.New("Radius relay exceeded its pending byte limit")
	}
	w.pendingBytes += len(copied)
	item := rrWriteItem{
		typ:  typ,
		data: copied,
		done: make(chan error, 1),
	}
	w.mu.Unlock()

	select {
	case <-w.doneCh:
		w.mu.Lock()
		w.pendingBytes -= len(copied)
		w.mu.Unlock()
		return errors.New("Radius relay WebSocket is closed")
	case <-w.ctx.Done():
		w.mu.Lock()
		w.pendingBytes -= len(copied)
		w.mu.Unlock()
		return w.ctx.Err()
	case w.queue <- item:
	}

	select {
	case err := <-item.done:
		return err
	case <-w.doneCh:
		select {
		case err := <-item.done:
			return err
		default:
			return errors.New("Radius relay WebSocket is closed")
		}
	}
}

func (w *rrOrderedWriter) close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	<-w.doneCh
}

type rrServerByteConnection struct {
	sendFn  func(chunk []byte) error
	closeFn func(finalChunk []byte) error
	mu      sync.Mutex
	closed  bool
}

func (c *rrServerByteConnection) Closed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return closed
}

func (c *rrServerByteConnection) Send(chunk []byte) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errors.New("Radius relay connection is closed")
	}
	return c.sendFn(chunk)
}

func (c *rrServerByteConnection) Close(finalChunk []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.closeFn(finalChunk)
}

func (c *rrServerByteConnection) markClosed() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

type rrActiveRelayConn struct {
	connection *rrServerByteConnection
	handler    server.ByteConnectionHandler
}

// RadiusRelayHost maintains the experimental server's multiplexed, authenticated Radius relay connection.
type RadiusRelayHost struct {
	options RadiusRelayHostOptions
	ctx     context.Context
	cancel  context.CancelFunc

	mu          sync.Mutex
	started     bool
	closed      bool
	socket      RadiusRelayWebSocket
	writer      *rrOrderedWriter
	connections map[string]*rrActiveRelayConn
	loopDone    chan struct{}
}

// NewRadiusRelayHost constructs a RadiusRelayHost for one server identity.
func NewRadiusRelayHost(options RadiusRelayHostOptions) *RadiusRelayHost {
	if options.SleepFunc == nil {
		options.SleepFunc = rrSleepContext
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &RadiusRelayHost{
		options:     options,
		ctx:         ctx,
		cancel:      cancel,
		connections: make(map[string]*rrActiveRelayConn),
	}
}

// Start launches the background Radius relay host loop.
func (h *RadiusRelayHost) Start() {
	h.mu.Lock()
	if h.started || h.closed {
		h.mu.Unlock()
		return
	}
	h.started = true
	done := make(chan struct{})
	h.loopDone = done
	h.mu.Unlock()
	go func() {
		defer close(done)
		h.run()
	}()
}

// Close stops the Radius relay host and drops active relay connections.
func (h *RadiusRelayHost) Close() error {
	h.mu.Lock()
	if h.closed {
		done := h.loopDone
		h.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	h.closed = true
	sock := h.socket
	done := h.loopDone
	h.mu.Unlock()

	h.cancel()
	if sock != nil {
		_ = sock.Close(websocket.StatusNormalClosure, "Pi server stopped")
	}
	h.dropConnections(nil)
	if done != nil {
		<-done
	}
	return nil
}

func (h *RadiusRelayHost) emitStatus(status RadiusRelayHostStatus) {
	if h.options.OnStatus != nil {
		h.options.OnStatus(status)
	}
}

func (h *RadiusRelayHost) isClosed() bool {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	return closed || h.ctx.Err() != nil
}

func (h *RadiusRelayHost) run() {
	retryMs := rrHostRetryInitialMs
	for !h.isClosed() {
		auth, err := h.options.Auth.Resolve(h.ctx, false)
		if err != nil {
			if h.isClosed() {
				return
			}
			h.emitStatus(RadiusRelayHostStatus{Status: "retrying", Error: err.Error()})
			if !h.options.SleepFunc(h.ctx, time.Duration(retryMs)*time.Millisecond) {
				return
			}
			retryMs = min(retryMs*2, rrHostRetryMaxMs)
			continue
		}
		if auth == nil {
			h.emitStatus(RadiusRelayHostStatus{Status: "not_authenticated"})
			if !h.options.SleepFunc(h.ctx, time.Duration(rrMissingAuthRetryMs)*time.Millisecond) {
				return
			}
			continue
		}

		wsURL, err := rrRelayWebSocketURL(auth.Gateway, h.options.ServerId)
		if err != nil {
			if h.isClosed() {
				return
			}
			h.emitStatus(RadiusRelayHostStatus{Status: "retrying", Error: err.Error()})
			if !h.options.SleepFunc(h.ctx, time.Duration(retryMs)*time.Millisecond) {
				return
			}
			retryMs = min(retryMs*2, rrHostRetryMaxMs)
			continue
		}

		h.emitStatus(RadiusRelayHostStatus{Status: "connecting"})
		factory := h.options.WebSocketFactory
		if factory == nil {
			factory = rrDefaultWebSocketFactory
		}
		sock, err := rrOpenWebSocket(h.ctx, wsURL, RadiusRelayHostSubprotocol, auth.Token, factory)
		if err != nil {
			if h.isClosed() {
				return
			}
			h.emitStatus(RadiusRelayHostStatus{Status: "retrying", Error: err.Error()})
			if !h.options.SleepFunc(h.ctx, time.Duration(retryMs)*time.Millisecond) {
				return
			}
			retryMs = min(retryMs*2, rrHostRetryMaxMs)
			continue
		}

		retryMs = rrHostRetryInitialMs
		h.emitStatus(RadiusRelayHostStatus{Status: "connected"})
		err = h.serve(sock)
		if h.isClosed() {
			return
		}
		if err == nil {
			err = errors.New("Radius relay host disconnected")
		}
		h.emitStatus(RadiusRelayHostStatus{Status: "retrying", Error: err.Error()})
		if !h.options.SleepFunc(h.ctx, time.Duration(retryMs)*time.Millisecond) {
			return
		}
		retryMs = min(retryMs*2, rrHostRetryMaxMs)
	}
}

func (h *RadiusRelayHost) serve(sock RadiusRelayWebSocket) error {
	serveCtx, cancelServe := context.WithCancel(h.ctx)
	writer := rrNewOrderedWriter(serveCtx, sock)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancelServe()
		writer.close()
		_ = sock.CloseNow()
		return nil
	}
	h.socket = sock
	h.writer = writer
	h.mu.Unlock()

	defer func() {
		cancelServe()
		writer.close()
		h.mu.Lock()
		if h.socket == sock {
			h.socket = nil
			h.writer = nil
		}
		h.mu.Unlock()
	}()

	var terminalErr error
	for {
		msgType, data, err := sock.Read(serveCtx)
		if err != nil {
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) && closeErr.Code == websocket.StatusNormalClosure {
				terminalErr = nil
			} else if !h.isClosed() {
				if errors.As(err, &closeErr) {
					if closeErr.Reason != "" {
						terminalErr = fmt.Errorf("Radius relay host closed (%d: %s)", closeErr.Code, closeErr.Reason)
					} else {
						terminalErr = fmt.Errorf("Radius relay host closed (%d)", closeErr.Code)
					}
				} else {
					terminalErr = err
				}
			}
			break
		}
		if err := h.handleHostMessage(msgType, data, sock); err != nil {
			_ = sock.Close(rrLocalProtocolErrorClose, "Radius relay protocol error")
			terminalErr = err
			break
		}
	}

	h.dropConnections(terminalErr)
	return terminalErr
}

func (h *RadiusRelayHost) handleHostMessage(msgType websocket.MessageType, data []byte, sock RadiusRelayWebSocket) error {
	if msgType == websocket.MessageText {
		control, err := rrParseHostControlMessage(data)
		if err != nil {
			return err
		}
		switch control.Type {
		case "ping":
			go func() {
				if err := h.sendControl(rrControlMessage{Version: 1, Type: "pong"}); err != nil {
					_ = sock.Close(rrLocalTransportErrorClose, "Radius relay send failed")
				}
			}()
			return nil
		case "pong":
			return nil
		case "connection_open":
			return h.openConnection(control.ConnectionId)
		case "connection_close":
			h.remoteCloseConnection(control.ConnectionId)
			return nil
		default:
			return errors.New("Invalid Radius relay control message")
		}
	}
	if msgType != websocket.MessageBinary {
		return errors.New("Radius relay data message must be binary")
	}
	connId, payload, ok := ParseRelayDataFrame(data)
	if !ok {
		return errors.New("Invalid Radius relay data frame")
	}
	h.mu.Lock()
	active := h.connections[connId]
	h.mu.Unlock()
	if active == nil {
		code := 1000
		go func() {
			_ = h.sendControl(rrControlMessage{
				Version:      1,
				Type:         "connection_close",
				ConnectionId: connId,
				Code:         &code,
			})
		}()
		return nil
	}
	if active.handler.OnData != nil {
		active.handler.OnData(payload)
	}
	return nil
}

func (h *RadiusRelayHost) openConnection(connId string) error {
	h.mu.Lock()
	if _, exists := h.connections[connId]; exists {
		h.mu.Unlock()
		return errors.New("Radius relay reused a connection ID")
	}
	h.mu.Unlock()

	conn := &rrServerByteConnection{
		sendFn: func(chunk []byte) error {
			return h.sendData(connId, chunk)
		},
		closeFn: func(finalChunk []byte) error {
			return h.serverCloseConnection(connId, finalChunk)
		},
	}
	handler := h.options.Accept(conn)
	if conn.Closed() {
		code := 1012
		go func() {
			_ = h.sendControl(rrControlMessage{
				Version:      1,
				Type:         "connection_close",
				ConnectionId: connId,
				Code:         &code,
			})
		}()
		return nil
	}

	h.mu.Lock()
	if h.closed || h.writer == nil {
		h.mu.Unlock()
		conn.markClosed()
		if handler.OnClose != nil {
			handler.OnClose()
		}
		return nil
	}
	if _, exists := h.connections[connId]; exists {
		h.mu.Unlock()
		conn.markClosed()
		return errors.New("Radius relay reused a connection ID")
	}
	h.connections[connId] = &rrActiveRelayConn{
		connection: conn,
		handler:    handler,
	}
	h.mu.Unlock()
	return nil
}

func (h *RadiusRelayHost) remoteCloseConnection(connId string) {
	h.mu.Lock()
	active := h.connections[connId]
	if active != nil {
		delete(h.connections, connId)
	}
	h.mu.Unlock()
	if active == nil {
		return
	}
	active.connection.markClosed()
	if active.handler.OnClose != nil {
		active.handler.OnClose()
	}
}

func (h *RadiusRelayHost) serverCloseConnection(connId string, finalChunk []byte) error {
	h.mu.Lock()
	active := h.connections[connId]
	if active != nil {
		delete(h.connections, connId)
	}
	writer := h.writer
	h.mu.Unlock()
	if active == nil {
		return nil
	}
	if writer == nil {
		return errors.New("Radius relay host is disconnected")
	}
	if finalChunk != nil {
		frame, err := EncodeRelayDataFrame(connId, finalChunk)
		if err != nil {
			return err
		}
		if err := writer.send(websocket.MessageBinary, frame); err != nil {
			return err
		}
	}
	code := 1000
	return h.sendControl(rrControlMessage{
		Version:      1,
		Type:         "connection_close",
		ConnectionId: connId,
		Code:         &code,
	})
}

func (h *RadiusRelayHost) sendData(connId string, chunk []byte) error {
	h.mu.Lock()
	_, active := h.connections[connId]
	writer := h.writer
	h.mu.Unlock()
	if !active {
		return errors.New("Radius relay connection is closed")
	}
	if writer == nil {
		return errors.New("Radius relay host is disconnected")
	}
	frame, err := EncodeRelayDataFrame(connId, chunk)
	if err != nil {
		return err
	}
	return writer.send(websocket.MessageBinary, frame)
}

func (h *RadiusRelayHost) sendControl(msg rrControlMessage) error {
	h.mu.Lock()
	writer := h.writer
	h.mu.Unlock()
	if writer == nil {
		return errors.New("Radius relay host is disconnected")
	}
	raw, err := jsonx.Marshal(msg)
	if err != nil {
		return err
	}
	return writer.send(websocket.MessageText, raw)
}

func (h *RadiusRelayHost) dropConnections(terminalErr error) {
	h.mu.Lock()
	list := make([]*rrActiveRelayConn, 0, len(h.connections))
	for _, active := range h.connections {
		list = append(list, active)
	}
	clear(h.connections)
	h.mu.Unlock()

	for _, active := range list {
		active.connection.markClosed()
		if terminalErr != nil {
			if active.handler.OnError != nil {
				active.handler.OnError(terminalErr)
			}
		} else if active.handler.OnClose != nil {
			active.handler.OnClose()
		}
	}
}

// RadiusClientTransportOptions configures CreateRadiusClientTransportFactory.
type RadiusClientTransportOptions struct {
	ServerId         protocol.ServerId
	Auth             *RadiusRelayAuthResolver
	WebSocketFactory RadiusRelayWebSocketFactory
}

// CreateRadiusClientTransportFactory creates a ByteTransportFactory dialing through Radius relay.
func CreateRadiusClientTransportFactory(options RadiusClientTransportOptions) client.ByteTransportFactory {
	return func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
		ctx := context.Background()
		auth, err := options.Auth.Resolve(ctx, true)
		if err != nil {
			return nil, err
		}
		if auth == nil {
			return nil, errors.New("Radius authentication is required")
		}
		wsURL, err := rrRelayWebSocketURL(auth.Gateway, options.ServerId)
		if err != nil {
			return nil, err
		}
		factory := options.WebSocketFactory
		if factory == nil {
			factory = rrDefaultWebSocketFactory
		}
		sock, err := rrOpenWebSocket(ctx, wsURL, RadiusRelayClientSubprotocol, auth.Token, factory)
		if err != nil {
			return nil, err
		}
		return rrNewClientByteTransport(sock, handlers), nil
	}
}

type rrClientByteTransport struct {
	sock     RadiusRelayWebSocket
	handlers client.ByteTransportHandlers
	ctx      context.Context
	cancel   context.CancelFunc
	writer   *rrOrderedWriter
	mu       sync.Mutex
	closed   bool
	wg       sync.WaitGroup
}

func rrNewClientByteTransport(sock RadiusRelayWebSocket, handlers client.ByteTransportHandlers) *rrClientByteTransport {
	ctx, cancel := context.WithCancel(context.Background())
	t := &rrClientByteTransport{
		sock:     sock,
		handlers: handlers,
		ctx:      ctx,
		cancel:   cancel,
		writer:   rrNewOrderedWriter(ctx, sock),
	}
	t.wg.Add(1)
	go t.readLoop()
	return t
}

func (t *rrClientByteTransport) readLoop() {
	defer t.wg.Done()
	for {
		msgType, data, err := t.sock.Read(t.ctx)
		if err != nil {
			wasCanceled := t.ctx.Err() != nil
			if t.markClosed() {
				var closeErr websocket.CloseError
				if errors.As(err, &closeErr) || wasCanceled {
					if t.handlers.OnClose != nil {
						t.handlers.OnClose()
					}
				} else if t.handlers.OnError != nil {
					t.handlers.OnError(err)
				}
			}
			return
		}
		if msgType != websocket.MessageBinary {
			t.fail(errors.New("Radius relay client received a non-binary message"))
			return
		}
		if t.handlers.OnData != nil {
			t.handlers.OnData(data)
		}
	}
}

func (t *rrClientByteTransport) Send(chunk []byte) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return errors.New("Radius relay client is closed")
	}
	return t.writer.send(websocket.MessageBinary, chunk)
}

func (t *rrClientByteTransport) Close() {
	if !t.markClosed() {
		return
	}
	_ = t.sock.Close(websocket.StatusNormalClosure, "Pi client closed")
}

func (t *rrClientByteTransport) fail(err error) {
	if !t.markClosed() {
		return
	}
	_ = t.sock.Close(rrLocalTransportErrorClose, "Radius relay transport error")
	if t.handlers.OnError != nil {
		t.handlers.OnError(err)
	}
}

func (t *rrClientByteTransport) markClosed() bool {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return false
	}
	t.closed = true
	t.mu.Unlock()
	t.cancel()
	t.writer.close()
	return true
}

// RadiusReconnectClient defines the client surface needed by RadiusClientReconnect.
type RadiusReconnectClient interface {
	Attachment() *protocol.SessionTarget
	Connected() bool
	ConnectionState() client.ConnectionState
	Disconnect(reason string)
	OnAttachmentChange(listener client.AttachmentChangeListener) (client.Unsubscribe, error)
	OnConnectionStateChange(listener func(change client.ConnectionStateChange)) (client.Unsubscribe, error)
	Reconnect() (*protocol.ServerHello, error)
}

// RadiusClientReconnectOptions configures optional timing hooks for RadiusClientReconnect.
type RadiusClientReconnectOptions struct {
	SleepFunc func(ctx context.Context, d time.Duration) bool
}

// RadiusClientReconnect maintains automatic reconnection and session reattachment for a Radius client.
type RadiusClientReconnect struct {
	client           RadiusReconnectClient
	reattach         func(sessionId string) error
	sleepFn          func(ctx context.Context, d time.Duration) bool
	ctx              context.Context
	cancel           context.CancelFunc
	removeConn       client.Unsubscribe
	removeAttachment client.Unsubscribe

	mu               sync.Mutex
	desiredSessionId string
	reconnecting     bool
	disposed         bool
	wg               sync.WaitGroup
}

// NewRadiusClientReconnect starts monitoring one connected client and reattaches its selected session across drops.
func NewRadiusClientReconnect(c RadiusReconnectClient, reattach func(sessionId string) error, options ...RadiusClientReconnectOptions) (*RadiusClientReconnect, error) {
	sleepFn := rrSleepContext
	if len(options) > 0 && options[0].SleepFunc != nil {
		sleepFn = options[0].SleepFunc
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &RadiusClientReconnect{
		client:   c,
		reattach: reattach,
		sleepFn:  sleepFn,
		ctx:      ctx,
		cancel:   cancel,
	}
	if attachment := c.Attachment(); attachment != nil {
		r.desiredSessionId = attachment.SessionId
	}
	var err error
	r.removeAttachment, err = c.OnAttachmentChange(func(attachment *protocol.SessionTarget) {
		connected := c.Connected()
		r.mu.Lock()
		if attachment != nil {
			r.desiredSessionId = attachment.SessionId
		} else if connected {
			r.desiredSessionId = ""
		}
		r.mu.Unlock()
	})
	if err != nil {
		cancel()
		return nil, err
	}
	r.removeConn, err = c.OnConnectionStateChange(func(change client.ConnectionStateChange) {
		if change.State == client.ConnectionStateDisconnected {
			r.startReconnect()
		}
	})
	if err != nil {
		r.removeAttachment()
		cancel()
		return nil, err
	}
	return r, nil
}

// Dispose stops background reconnection attempts and disconnects the client if active.
func (r *RadiusClientReconnect) Dispose() error {
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		r.wg.Wait()
		return nil
	}
	r.disposed = true
	r.mu.Unlock()

	if r.removeConn != nil {
		r.removeConn()
	}
	if r.removeAttachment != nil {
		r.removeAttachment()
	}
	r.cancel()
	if r.client.ConnectionState() != client.ConnectionStateDisconnected {
		r.client.Disconnect("Radius reconnect stopped")
	}
	r.wg.Wait()
	return nil
}

func (r *RadiusClientReconnect) startReconnect() {
	r.mu.Lock()
	if r.disposed || r.reconnecting {
		r.mu.Unlock()
		return
	}
	r.reconnecting = true
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			r.reconnecting = false
			r.mu.Unlock()
		}()
		r.runReconnect()
	}()
}

func (r *RadiusClientReconnect) runReconnect() {
	retryMs := rrClientRetryInitialMs
	for {
		r.mu.Lock()
		disposed := r.disposed
		r.mu.Unlock()
		if disposed || r.client.Connected() {
			return
		}
		if !r.sleepFn(r.ctx, time.Duration(retryMs)*time.Millisecond) {
			return
		}
		if _, err := r.client.Reconnect(); err != nil {
			r.mu.Lock()
			disposed = r.disposed
			r.mu.Unlock()
			if disposed || r.ctx.Err() != nil {
				return
			}
			if r.client.Connected() {
				r.client.Disconnect(err.Error())
			}
			retryMs = min(retryMs*2, rrClientRetryMaxMs)
			continue
		}
		r.mu.Lock()
		sessionId := r.desiredSessionId
		r.mu.Unlock()
		if sessionId != "" && r.reattach != nil {
			if err := r.reattach(sessionId); err != nil {
				r.mu.Lock()
				disposed = r.disposed
				r.mu.Unlock()
				if disposed || r.ctx.Err() != nil {
					return
				}
				if r.client.Connected() {
					r.client.Disconnect(err.Error())
				}
				retryMs = min(retryMs*2, rrClientRetryMaxMs)
				continue
			}
		}
		return
	}
}

func rrParseHostControlMessage(raw []byte) (rrControlMessage, error) {
	var msg rrControlMessage
	if err := jsonx.Unmarshal(raw, &msg); err != nil {
		return rrControlMessage{}, errors.New("Invalid Radius relay control message")
	}
	if msg.Version != 1 {
		return rrControlMessage{}, errors.New("Unsupported Radius relay control version")
	}
	switch msg.Type {
	case "ping", "pong":
		return msg, nil
	case "connection_open", "connection_close":
		if !rrConnectionIdRegexp.MatchString(msg.ConnectionId) {
			return rrControlMessage{}, errors.New("Invalid Radius relay control message")
		}
		if msg.Code != nil && (*msg.Code < 1000 || *msg.Code > 4999) {
			return rrControlMessage{}, errors.New("Invalid Radius relay control message")
		}
		return msg, nil
	default:
		return rrControlMessage{}, errors.New("Invalid Radius relay control message")
	}
}

// EncodeRelayDataFrame builds an 18-byte Radius relay header followed by payload bytes.
func EncodeRelayDataFrame(connectionId string, payload []byte) ([]byte, error) {
	if !rrConnectionIdRegexp.MatchString(connectionId) {
		return nil, errors.New("Invalid Radius relay connection ID")
	}
	frame := make([]byte, rrDataHeaderBytes+len(payload))
	frame[0] = rrDataFrameVersion
	frame[1] = rrDataFrameType
	compact := make([]byte, 0, 32)
	for i := 0; i < len(connectionId); i++ {
		if connectionId[i] != '-' {
			compact = append(compact, connectionId[i])
		}
	}
	if _, err := hex.Decode(frame[2:rrDataHeaderBytes], compact); err != nil {
		return nil, err
	}
	copy(frame[rrDataHeaderBytes:], payload)
	return frame, nil
}

// ParseRelayDataFrame parses one binary Radius relay data frame into connection UUID and payload bytes.
func ParseRelayDataFrame(frame []byte) (string, []byte, bool) {
	if len(frame) < rrDataHeaderBytes {
		return "", nil, false
	}
	if frame[0] != rrDataFrameVersion || frame[1] != rrDataFrameType {
		return "", nil, false
	}
	hexStr := hex.EncodeToString(frame[2:rrDataHeaderBytes])
	connId := fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
	if !rrConnectionIdRegexp.MatchString(connId) {
		return "", nil, false
	}
	payload := make([]byte, len(frame)-rrDataHeaderBytes)
	copy(payload, frame[rrDataHeaderBytes:])
	return connId, payload, true
}

func rrRelayWebSocketURL(gateway string, serverId protocol.ServerId) (string, error) {
	parsed, err := url.Parse(gateway)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("Unsupported Radius gateway protocol: %s:", parsed.Scheme)
	}
	parsed.Path = fmt.Sprintf("/v1/session-relays/%s/connect", serverId)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func rrDefaultWebSocketFactory(ctx context.Context, endpointURL string, subprotocol string, authorization string) (RadiusRelayWebSocket, error) {
	conn, _, err := websocket.Dial(ctx, endpointURL, &websocket.DialOptions{
		Subprotocols: []string{subprotocol},
		HTTPHeader: http.Header{
			"Authorization": []string{authorization},
		},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(rrMaxPendingBytes)
	return conn, nil
}

func rrOpenWebSocket(
	ctx context.Context,
	endpointURL string,
	subprotocol string,
	token string,
	factory RadiusRelayWebSocketFactory,
) (RadiusRelayWebSocket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sock, err := factory(ctx, endpointURL, subprotocol, "Bearer "+token)
	if err != nil {
		return nil, err
	}
	if wc, ok := sock.(*websocket.Conn); ok {
		wc.SetReadLimit(rrMaxPendingBytes)
	}
	if sock.Subprotocol() != subprotocol {
		_ = sock.Close(websocket.StatusNormalClosure, "Radius relay connection failed")
		return nil, fmt.Errorf("Radius relay selected unexpected WebSocket protocol %q", sock.Subprotocol())
	}
	return sock, nil
}

func rrSleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
