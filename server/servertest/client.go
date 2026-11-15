// Ported from packages/server/src/testing/client.ts (pi v1.0.0).

package servertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

// WireChannel is the low-level byte stream used by ProtocolTestClient.
type WireChannel interface {
	Send(chunk []byte) error
	SendFragmented(chunk []byte, splitAt int) error
	Close() error
}

type clientMessageWaiter struct {
	from      int
	predicate func(message protocol.ServerMessage) bool
	done      chan struct{}
	message   protocol.ServerMessage
	err       error
}

type clientAttachmentPair struct {
	sessionId    string
	attachmentId string
}

// ProtocolTestClient speaks raw protocol frames over a WireChannel for conformance testing.
type ProtocolTestClient struct {
	mu              sync.Mutex
	channel         WireChannel
	decoder         *protocol.ServerMessageDecoder
	messages        []protocol.ServerMessage
	waiters         map[*clientMessageWaiter]struct{}
	closedDone      chan struct{}
	closedOnce      sync.Once
	requestSequence int64
	attachment      *clientAttachmentPair
	closed          bool
}

// NewProtocolTestClient constructs a ProtocolTestClient over channel.
func NewProtocolTestClient(channel WireChannel) *ProtocolTestClient {
	decoder, _ := protocol.NewServerMessageDecoder(nil)
	return &ProtocolTestClient{
		channel:    channel,
		decoder:    decoder,
		waiters:    make(map[*clientMessageWaiter]struct{}),
		closedDone: make(chan struct{}),
	}
}

// Closed reports whether the underlying connection has been marked closed.
func (c *ProtocolTestClient) Closed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return closed
}

// Messages returns a snapshot copy of all received ServerMessages.
func (c *ProtocolTestClient) Messages() []protocol.ServerMessage {
	c.mu.Lock()
	copied := slices.Clone(c.messages)
	c.mu.Unlock()
	return copied
}

// Hello sends a ClientHello (defaulting to ProtocolVersion) and awaits ServerHello or ServerHelloError.
func (c *ProtocolTestClient) Hello(ctx context.Context, version ...int) (protocol.ServerMessage, error) {
	ver := protocol.ProtocolVersion
	if len(version) > 0 {
		ver = version[0]
	}
	waiter := c.addWaiter(0, func(msg protocol.ServerMessage) bool {
		switch msg.(type) {
		case *protocol.ServerHello, *protocol.ServerHelloError:
			return true
		default:
			return false
		}
	})
	_ = c.SendMessage(&protocol.ClientHello{Type: "hello", Version: ver})
	return c.awaitWaiter(ctx, waiter)
}

// RequestService sends a RequestEnvelope for call and awaits its matching ResponseEnvelope.
func (c *ProtocolTestClient) RequestService(
	ctx context.Context,
	target protocol.RpcTarget,
	call chord.ServiceCall,
	id ...string,
) (protocol.ResponseEnvelope, error) {
	reqId := ""
	if len(id) > 0 && id[0] != "" {
		reqId = id[0]
	} else {
		c.mu.Lock()
		c.requestSequence++
		reqId = fmt.Sprintf("request-%d", c.requestSequence)
		c.mu.Unlock()
	}

	waiter := c.addWaiter(0, func(msg protocol.ServerMessage) bool {
		switch r := msg.(type) {
		case *protocol.ResponseSuccess:
			return r.Id == reqId
		case *protocol.ResponseFailure:
			return r.Id == reqId
		default:
			return false
		}
	})

	callVal, err := jsonx.Encode(call)
	if err != nil {
		c.removeWaiter(waiter)
		return nil, err
	}
	if err := c.SendMessage(&protocol.RequestEnvelope{
		Type:   "request",
		Id:     reqId,
		Target: target,
		Call:   callVal,
	}); err != nil {
		c.removeWaiter(waiter)
		return nil, err
	}
	msg, err := c.awaitWaiter(ctx, waiter)
	if err != nil {
		return nil, err
	}
	resp, ok := msg.(protocol.ResponseEnvelope)
	if !ok {
		return nil, errors.New("unexpected non-response message")
	}
	return resp, nil
}

// Attach invokes pi.session-management.attach for sessionId on serverId.
func (c *ProtocolTestClient) Attach(ctx context.Context, serverId protocol.ServerId, sessionId string) (protocol.ResponseEnvelope, error) {
	return c.RequestService(
		ctx,
		&protocol.ServerTarget{ServerId: serverId},
		chord.ServiceCall{
			ServiceId: "pi.session-management",
			Member:    "attach",
			Args:      []chord.JsonValue{sessionId},
		},
	)
}

// RequestSessionService targets the client's tracked attachment for sessionId (or "missing-attachment").
func (c *ProtocolTestClient) RequestSessionService(
	ctx context.Context,
	serverId protocol.ServerId,
	sessionId string,
	call chord.ServiceCall,
	id ...string,
) (protocol.ResponseEnvelope, error) {
	c.mu.Lock()
	att := c.attachment
	c.mu.Unlock()

	var target protocol.RpcTarget
	if att == nil || att.sessionId != sessionId {
		target = &protocol.SessionTarget{
			ServerId:     serverId,
			SessionId:    sessionId,
			AttachmentId: "missing-attachment",
		}
	} else {
		target = &protocol.SessionTarget{
			ServerId:     serverId,
			SessionId:    att.sessionId,
			AttachmentId: att.attachmentId,
		}
	}
	return c.RequestService(ctx, target, call, id...)
}

// SendMessage validates and encodes message before writing it to the WireChannel.
func (c *ProtocolTestClient) SendMessage(message protocol.ClientMessage) error {
	frame, err := protocol.EncodeClientMessage(message, nil)
	if err != nil {
		return err
	}
	return c.channel.Send(frame)
}

// SendBytes writes raw bytes to the WireChannel.
func (c *ProtocolTestClient) SendBytes(chunk []byte) error {
	return c.channel.Send(chunk)
}

// SendFragmentedMessage encodes message and splits its wire frame at splitAt.
func (c *ProtocolTestClient) SendFragmentedMessage(message protocol.ClientMessage, splitAt int) error {
	frame, err := protocol.EncodeClientMessage(message, nil)
	if err != nil {
		return err
	}
	return c.channel.SendFragmented(frame, splitAt)
}

// Next waits for the first received message matching predicate starting at index 0.
func (c *ProtocolTestClient) Next(ctx context.Context, predicate func(message protocol.ServerMessage) bool) (protocol.ServerMessage, error) {
	return c.NextFrom(ctx, 0, predicate)
}

// NextFrom waits for the first received message matching predicate starting at index.
func (c *ProtocolTestClient) NextFrom(ctx context.Context, index int, predicate func(message protocol.ServerMessage) bool) (protocol.ServerMessage, error) {
	waiter := c.addWaiter(index, predicate)
	return c.awaitWaiter(ctx, waiter)
}

// WaitForClose blocks until the client connection closes or ctx is cancelled.
func (c *ProtocolTestClient) WaitForClose(ctx context.Context) error {
	if ctx == nil {
		<-c.closedDone
		return nil
	}
	select {
	case <-c.closedDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the underlying WireChannel.
func (c *ProtocolTestClient) Close() error {
	return c.channel.Close()
}

// Receive feeds inbound wire bytes into the client decoder and resolves matching waiters.
func (c *ProtocolTestClient) Receive(chunk []byte) {
	messages, err := c.decoder.Push(chunk)
	if err != nil {
		c.Fail(err)
		return
	}
	for _, msg := range messages {
		var matched []*clientMessageWaiter
		c.mu.Lock()
		if attEnv, ok := msg.(*protocol.AttachmentEnvelope); ok {
			if st, has := attEnv.Attachment.Get(); has {
				c.attachment = &clientAttachmentPair{
					sessionId:    st.SessionId,
					attachmentId: st.AttachmentId,
				}
			} else {
				c.attachment = nil
			}
		}
		c.messages = append(c.messages, msg)
		for w := range c.waiters {
			if w.predicate(msg) {
				delete(c.waiters, w)
				w.message = msg
				matched = append(matched, w)
			}
		}
		c.mu.Unlock()

		for _, w := range matched {
			close(w.done)
		}
	}
}

// MarkClosed marks the client closed and fails all pending waiters.
func (c *ProtocolTestClient) MarkClosed() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	c.closedOnce.Do(func() {
		close(c.closedDone)
	})
	c.Fail(errors.New("Wire connection closed"))
}

// Fail rejects all pending waiters with err.
func (c *ProtocolTestClient) Fail(err error) {
	c.mu.Lock()
	pending := make([]*clientMessageWaiter, 0, len(c.waiters))
	for w := range c.waiters {
		delete(c.waiters, w)
		pending = append(pending, w)
	}
	c.mu.Unlock()

	for _, w := range pending {
		w.err = err
		close(w.done)
	}
}

func (c *ProtocolTestClient) addWaiter(from int, predicate func(protocol.ServerMessage) bool) *clientMessageWaiter {
	waiter := &clientMessageWaiter{
		from:      from,
		predicate: predicate,
		done:      make(chan struct{}),
	}

	c.mu.Lock()
	if from < 0 {
		from = 0
	}
	if from <= len(c.messages) {
		for _, msg := range c.messages[from:] {
			if predicate(msg) {
				waiter.message = msg
				close(waiter.done)
				c.mu.Unlock()
				return waiter
			}
		}
	}
	if c.closed {
		waiter.err = errors.New("Wire client is closed")
		close(waiter.done)
		c.mu.Unlock()
		return waiter
	}
	c.waiters[waiter] = struct{}{}
	c.mu.Unlock()
	return waiter
}

func (c *ProtocolTestClient) removeWaiter(w *clientMessageWaiter) {
	c.mu.Lock()
	delete(c.waiters, w)
	c.mu.Unlock()
}

func (c *ProtocolTestClient) awaitWaiter(ctx context.Context, w *clientMessageWaiter) (protocol.ServerMessage, error) {
	if ctx == nil {
		<-w.done
		return w.message, w.err
	}
	select {
	case <-w.done:
		return w.message, w.err
	case <-ctx.Done():
		c.removeWaiter(w)
		return nil, ctx.Err()
	}
}

type clientUnixChannel struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
	wg     *sync.WaitGroup
}

func (ch *clientUnixChannel) Send(chunk []byte) error {
	return clientWriteAll(ch.conn, chunk)
}

func (ch *clientUnixChannel) SendFragmented(chunk []byte, splitAt int) error {
	if splitAt < 0 {
		splitAt = 0
	}
	if splitAt > len(chunk) {
		splitAt = len(chunk)
	}
	if err := clientWriteAll(ch.conn, chunk[:splitAt]); err != nil {
		return err
	}
	return clientWriteAll(ch.conn, chunk[splitAt:])
}

func (ch *clientUnixChannel) Close() error {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		ch.wg.Wait()
		return nil
	}
	ch.closed = true
	ch.mu.Unlock()

	err := ch.conn.Close()
	ch.wg.Wait()
	return err
}

func clientWriteAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// ConnectUnixTestClient connects a ProtocolTestClient to a Unix domain socket at path.
func ConnectUnixTestClient(path string) (*ProtocolTestClient, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	ch := &clientUnixChannel{
		conn: conn,
		wg:   &wg,
	}
	client := NewProtocolTestClient(ch)
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := conn.Read(buf)
			if n > 0 {
				client.Receive(slices.Clone(buf[:n]))
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, net.ErrClosed) {
					client.Fail(readErr)
				}
				client.MarkClosed()
				return
			}
		}
	}()
	return client, nil
}
