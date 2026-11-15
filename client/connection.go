// Ported from packages/client/src/connection.ts (pi v1.0.0).

package client

import (
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

const connMaxUint32 = 0xffff_ffff

type connOptions struct {
	TransportFactory ByteTransportFactory
	ServerId         protocol.ServerId
	MaxFrameLength   *int
	OnHandshake      func(hello *protocol.ServerHello)
	OnMessage        func(message protocol.ServerMessage)
	OnStateChange    func(change ConnectionStateChange)
}

type connLifecycle struct {
	state     ConnectionState
	id        int64
	decoder   *protocol.ServerMessageDecoder
	transport ByteTransport
	handshake *PromiseResolvers[*protocol.ServerHello]
	openDone  chan struct{}
	sendQueue *connSendQueue
}

type connSendQueue struct {
	mu        sync.Mutex
	closed    bool
	items     [][]byte
	notify    chan struct{}
	transport ByteTransport
	onErr     func(error)
	done      chan struct{}
}

func connNewSendQueue(transport ByteTransport, onErr func(error)) *connSendQueue {
	q := &connSendQueue{
		notify:    make(chan struct{}, 1),
		transport: transport,
		onErr:     onErr,
		done:      make(chan struct{}),
	}
	go q.run()
	return q
}

func (q *connSendQueue) enqueue(frame []byte) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	q.items = append(q.items, frame)
	select {
	case q.notify <- struct{}{}:
	default:
	}
	q.mu.Unlock()
	return true
}

func (q *connSendQueue) stop() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.items = nil
		select {
		case q.notify <- struct{}{}:
		default:
		}
	}
	q.mu.Unlock()
}

func (q *connSendQueue) run() {
	defer close(q.done)
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.mu.Unlock()
			<-q.notify
			q.mu.Lock()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		item := q.items[0]
		q.items[0] = nil
		q.items = q.items[1:]
		if len(q.items) == 0 {
			q.items = nil
		}
		q.mu.Unlock()

		if err := q.transport.Send(item); err != nil {
			q.stop()
			if q.onErr != nil {
				q.onErr(err)
			}
			return
		}
	}
}

type connConnection struct {
	mu             sync.Mutex
	inMu           sync.Mutex
	options        connOptions
	maxFrameLength int
	lifecycle      connLifecycle
	sequence       int64
}

func connNewConnection(options connOptions) (*connConnection, error) {
	maxFrameLength := protocol.DefaultMaxFrameLength
	if options.MaxFrameLength != nil {
		maxFrameLength = *options.MaxFrameLength
	}
	if maxFrameLength <= 0 || maxFrameLength > connMaxUint32 {
		return nil, js.NewTypeError(fmt.Sprintf("Client maxFrameLength must be an integer between 1 and %d", connMaxUint32))
	}
	return &connConnection{
		options:        options,
		maxFrameLength: maxFrameLength,
		lifecycle:      connLifecycle{state: ConnectionStateDisconnected},
	}, nil
}

func (c *connConnection) State() ConnectionState {
	c.mu.Lock()
	state := c.lifecycle.state
	c.mu.Unlock()
	return state
}

func (c *connConnection) MaxFrameLength() int {
	return c.maxFrameLength
}

func (c *connConnection) Connect() (*protocol.ServerHello, error) {
	c.mu.Lock()
	if c.lifecycle.state != ConnectionStateDisconnected {
		state := c.lifecycle.state
		c.mu.Unlock()
		return nil, NewDisconnectedError(fmt.Sprintf("Client is already %s", state), nil)
	}
	c.sequence++
	id := c.sequence
	decoder, err := protocol.NewServerMessageDecoder(&protocol.FrameDecoderOptions{
		MaxFrameLength: &c.maxFrameLength,
	})
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	handshake := CreatePromiseResolvers[*protocol.ServerHello]()
	openDone := make(chan struct{})
	c.lifecycle = connLifecycle{
		state:     ConnectionStateConnecting,
		id:        id,
		decoder:   decoder,
		handshake: handshake,
		openDone:  openDone,
	}
	c.mu.Unlock()

	c.options.OnStateChange(ConnectionStateChange{State: ConnectionStateConnecting})

	handlers := ByteTransportHandlers{
		OnData: func(chunk []byte) {
			c.handleData(id, chunk)
		},
		OnClose: func() {
			c.inMu.Lock()
			defer c.inMu.Unlock()
			c.mu.Lock()
			cur := c.isCurrentLocked(id)
			c.mu.Unlock()
			if cur {
				c.handleClose(id)
			}
		},
		OnError: func(err error) {
			c.mu.Lock()
			cur := c.isCurrentLocked(id)
			c.mu.Unlock()
			if cur {
				c.failAndClose(ToDisconnectedError(err))
			}
		},
	}

	go c.openTransport(id, handlers, openDone)

	hello, err := handshake.Await()
	if err != nil {
		<-openDone
		return nil, err
	}
	return hello, nil
}

func (c *connConnection) Disconnect(reason error) {
	c.mu.Lock()
	if c.lifecycle.state == ConnectionStateDisconnected {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if reason == nil {
		reason = NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
	}
	c.failAndClose(reason)
}

func (c *connConnection) Fail(err error) {
	c.failAndClose(ToError(err))
}

func (c *connConnection) Send(frame []byte) error {
	c.mu.Lock()
	if c.lifecycle.state != ConnectionStateConnected || c.lifecycle.sendQueue == nil {
		c.mu.Unlock()
		return NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
	}
	queue := c.lifecycle.sendQueue
	c.mu.Unlock()

	if !queue.enqueue(frame) {
		return NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
	}
	return nil
}

func (c *connConnection) openTransport(id int64, handlers ByteTransportHandlers, openDone chan struct{}) {
	transport, err := c.options.TransportFactory(handlers)
	if err != nil {
		close(openDone)
		c.mu.Lock()
		cur := c.isCurrentLocked(id)
		c.mu.Unlock()
		if cur {
			c.fail(ToDisconnectedError(err))
		}
		return
	}

	c.mu.Lock()
	if c.lifecycle.state != ConnectionStateConnecting || c.lifecycle.id != id {
		c.mu.Unlock()
		transport.Close()
		close(openDone)
		return
	}
	c.lifecycle.transport = transport
	c.mu.Unlock()
	close(openDone)

	helloFrame, encodeErr := protocol.EncodeClientMessage(
		&protocol.ClientHello{Type: "hello", Version: protocol.ProtocolVersion},
		&protocol.FrameDecoderOptions{MaxFrameLength: &c.maxFrameLength},
	)
	if encodeErr != nil {
		c.mu.Lock()
		cur := c.isCurrentLocked(id)
		c.mu.Unlock()
		if cur {
			c.failAndClose(ToDisconnectedError(encodeErr))
		}
		return
	}

	if sendErr := transport.Send(helloFrame); sendErr != nil {
		c.mu.Lock()
		cur := c.isCurrentLocked(id)
		c.mu.Unlock()
		if cur {
			c.failAndClose(ToDisconnectedError(sendErr))
		}
	}
}

func (c *connConnection) handleData(id int64, chunk []byte) {
	c.inMu.Lock()
	defer c.inMu.Unlock()

	c.mu.Lock()
	if c.lifecycle.state == ConnectionStateDisconnected || c.lifecycle.id != id {
		c.mu.Unlock()
		return
	}
	if c.lifecycle.state == ConnectionStateConnecting && c.lifecycle.transport == nil {
		c.mu.Unlock()
		c.failAndClose(protocol.NewProtocolValidationError("Received server data before the client hello was sent"))
		return
	}
	decoder := c.lifecycle.decoder
	c.mu.Unlock()

	messages, err := decoder.Push(chunk)
	if err != nil {
		c.failAndClose(ToError(err))
		return
	}
	for _, message := range messages {
		c.mu.Lock()
		if c.lifecycle.state == ConnectionStateDisconnected || c.lifecycle.id != id {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		c.handleMessage(id, message)
	}
}

func (c *connConnection) handleMessage(id int64, message protocol.ServerMessage) {
	c.mu.Lock()
	if c.lifecycle.state == ConnectionStateDisconnected || c.lifecycle.id != id {
		c.mu.Unlock()
		return
	}
	state := c.lifecycle.state
	hasTransport := c.lifecycle.transport != nil
	transport := c.lifecycle.transport
	handshake := c.lifecycle.handshake
	c.mu.Unlock()

	if state == ConnectionStateConnecting {
		switch m := message.(type) {
		case *protocol.ServerHelloError:
			c.failAndClose(NewServerError(m.Error))
			return
		case *protocol.ServerHello:
			quotedGot, _ := jsonx.Marshal(string(m.ServerId))
			quotedWant, _ := jsonx.Marshal(string(c.options.ServerId))
			if m.ServerId != c.options.ServerId {
				c.failAndClose(protocol.NewProtocolValidationError(
					fmt.Sprintf("Connected server %s does not match %s", string(quotedGot), string(quotedWant)),
				))
				return
			}
			if !hasTransport {
				c.failAndClose(protocol.NewProtocolValidationError("Received server hello before the client hello was sent"))
				return
			}
			sendQueue := connNewSendQueue(transport, func(err error) {
				c.mu.Lock()
				cur := c.lifecycle.state != ConnectionStateDisconnected && c.lifecycle.transport == transport
				c.mu.Unlock()
				if cur {
					c.failAndClose(ToDisconnectedError(err))
				}
			})
			c.mu.Lock()
			if c.lifecycle.state != ConnectionStateConnecting || c.lifecycle.id != id {
				c.mu.Unlock()
				sendQueue.stop()
				return
			}
			c.lifecycle.state = ConnectionStateConnected
			c.lifecycle.sendQueue = sendQueue
			c.mu.Unlock()

			c.options.OnHandshake(m)

			c.mu.Lock()
			if c.lifecycle.state != ConnectionStateConnected || c.lifecycle.id != id {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

			c.options.OnStateChange(ConnectionStateChange{State: ConnectionStateConnected})

			c.mu.Lock()
			if c.lifecycle.state != ConnectionStateConnected || c.lifecycle.id != id {
				c.mu.Unlock()
				return
			}
			c.lifecycle.handshake = nil
			c.mu.Unlock()

			if handshake != nil {
				handshake.Resolve(m)
			}
			return
		default:
			c.failAndClose(protocol.NewProtocolValidationError("Expected server hello as first message"))
			return
		}
	}

	if state != ConnectionStateConnected {
		return
	}

	switch message.(type) {
	case *protocol.ServerHello, *protocol.ServerHelloError:
		c.failAndClose(protocol.NewProtocolValidationError("Unexpected handshake message"))
		return
	}
	c.options.OnMessage(message)
}

func (c *connConnection) handleClose(id int64) {
	c.mu.Lock()
	if c.lifecycle.state == ConnectionStateDisconnected || c.lifecycle.id != id {
		c.mu.Unlock()
		return
	}
	decoder := c.lifecycle.decoder
	c.mu.Unlock()

	var err error = NewDisconnectedError("Byte transport closed", nil)
	if endErr := decoder.End(); endErr != nil {
		err = ToError(endErr)
	}
	c.fail(err)
}

func (c *connConnection) failAndClose(err error) {
	c.failInternal(err, true)
}

func (c *connConnection) fail(err error) {
	c.failInternal(err, false)
}

func (c *connConnection) failInternal(err error, closeTransport bool) {
	c.mu.Lock()
	if c.lifecycle.state == ConnectionStateDisconnected {
		c.mu.Unlock()
		return
	}
	transport := c.lifecycle.transport
	handshake := c.lifecycle.handshake
	sendQueue := c.lifecycle.sendQueue
	c.lifecycle = connLifecycle{state: ConnectionStateDisconnected}
	c.mu.Unlock()

	if sendQueue != nil {
		sendQueue.stop()
	}
	if closeTransport && transport != nil {
		transport.Close()
	}
	c.options.OnStateChange(ConnectionStateChange{
		State: ConnectionStateDisconnected,
		Error: err,
	})
	// Resolve/reject handshake last so callers returning from Connect() observe closed transport and disconnected state.
	if handshake != nil {
		handshake.Reject(err)
	}
}

func (c *connConnection) isCurrentLocked(id int64) bool {
	return c.lifecycle.state != ConnectionStateDisconnected && c.lifecycle.id == id
}
