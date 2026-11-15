// Ported from packages/client/src/client.ts (pi v1.0.0).

package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/protocol"
)

type clientServiceResult struct {
	value chord.JsonValue
	ok    bool
}

type clientPendingRequest struct {
	mu      sync.Mutex
	sent    bool
	aborted bool
	resolve func(result clientServiceResult)
	reject  func(err error)
	cleanup func()
}

type clientActiveServiceListener struct {
	mu                sync.Mutex
	target            protocol.RpcTarget
	listener          func(update chord.ServiceProviderUpdate) error
	decoder           chord.ServiceStateDecoder
	queuedWireUpdates []chord.JsonValue
	queued            []chord.ServiceProviderUpdate
	pendingDeliveries []chord.ServiceProviderUpdate
	hydrated          bool
	ready             bool
	delivering        bool
	disposed          bool
	wg                sync.WaitGroup
}

type clientSubscription struct {
	id       string
	target   protocol.RpcTarget
	snapshot chord.ServiceSubscriptionSnapshot
	start    func()
	dispose  func() error
}

func (s *clientSubscription) Id() string { return s.id }

func (s *clientSubscription) Target() protocol.RpcTarget { return s.target }

func (s *clientSubscription) Snapshot() chord.ServiceSubscriptionSnapshot { return s.snapshot }

func (s *clientSubscription) Start() { s.start() }

func (s *clientSubscription) Dispose() error { return s.dispose() }

// Client is a stateful Pi RPC and Chord service client over one ByteTransport lifecycle.
type Client struct {
	mu                       sync.Mutex
	options                  ClientOptions
	connection               *connConnection
	pendingRequests          map[string]*clientPendingRequest
	connectionStateListeners *omap.Map[uint64, func(ConnectionStateChange)]
	attachmentListeners      *omap.Map[uint64, AttachmentChangeListener]
	serviceListeners         map[string]*clientActiveServiceListener
	listenerSequence         uint64
	requestSequence          int64
	serviceSubSequence       int64
	hello                    *protocol.ServerHello
	attachment               *protocol.SessionTarget
	disposed                 bool
	disposeDone              chan struct{}
}

// NewClient constructs an unconnected Client validated against options.
func NewClient(options ClientOptions) (*Client, error) {
	if !protocol.IsServerId(options.ServerId) {
		return nil, js.NewTypeError("serverId must be a canonical lowercase UUIDv4")
	}
	c := &Client{
		options:                  options,
		pendingRequests:          make(map[string]*clientPendingRequest),
		connectionStateListeners: omap.NewMap[uint64, func(ConnectionStateChange)](),
		attachmentListeners:      omap.NewMap[uint64, AttachmentChangeListener](),
		serviceListeners:         make(map[string]*clientActiveServiceListener),
	}
	conn, err := connNewConnection(connOptions{
		TransportFactory: options.TransportFactory,
		ServerId:         options.ServerId,
		MaxFrameLength:   options.MaxFrameLength,
		OnHandshake: func(hello *protocol.ServerHello) {
			c.mu.Lock()
			copied := *hello
			c.hello = &copied
			c.mu.Unlock()
		},
		OnMessage: func(message protocol.ServerMessage) {
			c.handleMessage(message)
		},
		OnStateChange: func(change ConnectionStateChange) {
			c.handleConnectionStateChange(change)
		},
	})
	if err != nil {
		return nil, err
	}
	c.connection = conn
	return c, nil
}

// Connect creates a Client and completes its initial connection handshake,
// disposing the Client if startup fails.
func Connect(options ClientOptions) (*Client, error) {
	client, err := NewClient(options)
	if err != nil {
		return nil, err
	}
	if _, err := client.Connect(); err != nil {
		_ = client.Dispose()
		return nil, err
	}
	return client, nil
}

// Disposed reports whether Dispose has been called.
func (c *Client) Disposed() bool {
	c.mu.Lock()
	disposed := c.disposed
	c.mu.Unlock()
	return disposed
}

// ConnectionState returns the current transport lifecycle state.
func (c *Client) ConnectionState() ConnectionState {
	return c.connection.State()
}

// Connected reports whether the client handshake has completed and the connection is open.
func (c *Client) Connected() bool {
	return c.connection.State() == ConnectionStateConnected
}

// ServerId returns the expected logical server identity.
func (c *Client) ServerId() protocol.ServerId {
	return c.options.ServerId
}

// Hello returns the ServerHello received for the current connection, or nil.
func (c *Client) Hello() *protocol.ServerHello {
	c.mu.Lock()
	hello := c.hello
	if hello == nil {
		c.mu.Unlock()
		return nil
	}
	copied := *hello
	c.mu.Unlock()
	return &copied
}

// Attachment returns the active SessionTarget pushed by the server, or nil.
func (c *Client) Attachment() *protocol.SessionTarget {
	c.mu.Lock()
	att := c.attachment
	if att == nil {
		c.mu.Unlock()
		return nil
	}
	copied := *att
	c.mu.Unlock()
	return &copied
}

// Connect opens the underlying transport and completes the protocol handshake.
func (c *Client) Connect() (*protocol.ServerHello, error) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return nil, NewClientDisposedError()
	}
	c.hello = nil
	c.mu.Unlock()
	return c.connection.Connect()
}

// Reconnect connects again after disconnecting.
func (c *Client) Reconnect() (*protocol.ServerHello, error) {
	return c.Connect()
}

// Disconnect closes the active connection without disposing the Client.
func (c *Client) Disconnect(reason string) {
	if reason == "" {
		reason = errorsDefaultDisconnectedMessage
	}
	c.connection.Disconnect(NewDisconnectedError(reason, nil))
}

// OnConnectionStateChange registers a listener for connection state transitions.
func (c *Client) OnConnectionStateChange(listener func(change ConnectionStateChange)) (Unsubscribe, error) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return nil, NewClientDisposedError()
	}
	c.listenerSequence++
	id := c.listenerSequence
	c.connectionStateListeners.Set(id, listener)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.connectionStateListeners.Delete(id)
		c.mu.Unlock()
	}, nil
}

// OnAttachmentChange registers a listener for server attachment updates.
func (c *Client) OnAttachmentChange(listener AttachmentChangeListener) (Unsubscribe, error) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return nil, NewClientDisposedError()
	}
	c.listenerSequence++
	id := c.listenerSequence
	c.attachmentListeners.Set(id, listener)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.attachmentListeners.Delete(id)
		c.mu.Unlock()
	}, nil
}

// Request invokes one low-level protocol call against an explicit routed target.
// ok is false when the server omitted the result field (TS undefined).
func (c *Client) Request(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall) (chord.JsonValue, bool, error) {
	res, err := clientRequestRaw[clientServiceResult](c, ctx, target, call, nil)
	if err != nil {
		return nil, false, err
	}
	return res.value, res.ok, nil
}

// ServiceCatalogue fetches and parses the Chord service catalogue from target.
func (c *Client) ServiceCatalogue(ctx context.Context, target protocol.RpcTarget) ([]chord.ServiceCatalogueEntry, error) {
	res, err := clientRequestRaw[clientServiceResult](c, ctx, target, chord.CreateServiceCatalogueCall(), nil)
	if err != nil {
		return nil, err
	}
	if !res.ok {
		validationError := protocol.NewProtocolValidationError("Invalid service catalogue")
		c.connection.Fail(validationError)
		return nil, validationError
	}
	catalogue, err := chord.ParseServiceCatalogue(res.value)
	if err != nil {
		validationError := protocol.NewProtocolValidationError(err.Error())
		c.connection.Fail(validationError)
		return nil, validationError
	}
	return catalogue, nil
}

// SubscribeService subscribes to a remote Chord service and buffers updates until Start is called.
func (c *Client) SubscribeService(
	ctx context.Context,
	target protocol.RpcTarget,
	serviceId string,
	mode chord.ServiceMode,
	listener func(update chord.ServiceProviderUpdate) error,
) (ServiceSubscription, error) {
	c.mu.Lock()
	c.serviceSubSequence++
	subscriptionId := fmt.Sprintf("service-%d", c.serviceSubSequence)
	active := &clientActiveServiceListener{
		target:   target,
		listener: listener,
		decoder:  chord.CreateServiceStateDecoder(),
	}
	c.serviceListeners[subscriptionId] = active
	c.mu.Unlock()

	snapshot, err := clientRequestRaw(
		c,
		ctx,
		target,
		chord.CreateServiceSubscribeCall(subscriptionId, serviceId, mode),
		func(result clientServiceResult) (chord.ServiceSubscriptionSnapshot, error) {
			if !result.ok {
				return chord.ServiceSubscriptionSnapshot{}, js.NewTypeError("Invalid service subscription snapshot")
			}
			wireSnap, err := chord.ParseWireServiceSubscriptionSnapshot(result.value)
			if err != nil {
				return chord.ServiceSubscriptionSnapshot{}, err
			}
			active.mu.Lock()
			decoded, err := active.decoder.DecodeSnapshot(wireSnap)
			if err != nil {
				active.mu.Unlock()
				return chord.ServiceSubscriptionSnapshot{}, err
			}
			active.hydrated = true
			queuedWire := active.queuedWireUpdates
			active.queuedWireUpdates = nil
			for _, wireVal := range queuedWire {
				wireUpdate, parseErr := chord.ParseWireServiceProviderUpdate(wireVal)
				if parseErr != nil {
					active.mu.Unlock()
					return chord.ServiceSubscriptionSnapshot{}, parseErr
				}
				update, decodeErr := active.decoder.DecodeUpdate(wireUpdate)
				if decodeErr != nil {
					active.mu.Unlock()
					return chord.ServiceSubscriptionSnapshot{}, decodeErr
				}
				active.queued = append(active.queued, update)
			}
			active.mu.Unlock()
			return decoded, nil
		},
	)
	if err != nil {
		c.mu.Lock()
		if c.serviceListeners[subscriptionId] == active {
			delete(c.serviceListeners, subscriptionId)
		}
		c.mu.Unlock()
		return nil, err
	}

	c.mu.Lock()
	stillActive := c.serviceListeners[subscriptionId] == active
	c.mu.Unlock()
	if !stillActive {
		return nil, NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
	}

	var disposeMu sync.Mutex
	disposed := false
	return &clientSubscription{
		id:       subscriptionId,
		target:   target,
		snapshot: snapshot,
		start: func() {
			disposeMu.Lock()
			alreadyDisposed := disposed
			disposeMu.Unlock()
			if alreadyDisposed {
				return
			}
			active.mu.Lock()
			if active.disposed || active.ready {
				active.mu.Unlock()
				return
			}
			active.ready = true
			queued := active.queued
			active.queued = nil
			if len(queued) > 0 {
				active.pendingDeliveries = append(active.pendingDeliveries, queued...)
				c.ensureDeliveryWorkerLocked(active)
			}
			active.mu.Unlock()
		},
		dispose: func() error {
			disposeMu.Lock()
			if disposed {
				disposeMu.Unlock()
				return nil
			}
			disposed = true
			disposeMu.Unlock()

			c.mu.Lock()
			if c.serviceListeners[subscriptionId] == active {
				delete(c.serviceListeners, subscriptionId)
			}
			c.mu.Unlock()

			var unsubErr error
			if c.Connected() && c.targetIsCurrent(target) {
				_, _, unsubErr = c.Request(context.Background(), target, chord.CreateServiceUnsubscribeCall(subscriptionId))
			}

			active.mu.Lock()
			active.disposed = true
			active.queuedWireUpdates = nil
			active.queued = nil
			active.pendingDeliveries = nil
			active.mu.Unlock()

			active.wg.Wait()
			return unsubErr
		},
	}, nil
}

func clientRequestRaw[T any](
	c *Client,
	ctx context.Context,
	target protocol.RpcTarget,
	call chord.ServiceCall,
	transform func(result clientServiceResult) (T, error),
) (T, error) {
	var zero T
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return zero, NewClientDisposedError()
	}
	if !c.Connected() {
		c.mu.Unlock()
		return zero, NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
	}
	if ctx != nil && ctx.Err() != nil {
		c.mu.Unlock()
		return zero, clientAbortError(ctx)
	}
	c.requestSequence++
	id := fmt.Sprintf("request-%d", c.requestSequence)
	maxFrameLength := c.connection.MaxFrameLength()

	resolvers := CreatePromiseResolvers[T]()
	req := &clientPendingRequest{
		resolve: func(result clientServiceResult) {
			if transform == nil {
				resolvers.Resolve(any(result).(T))
				return
			}
			transformed, err := transform(result)
			if err != nil {
				msg := err.Error()
				if msg == "" {
					msg = "Invalid service operation stream"
				}
				validationError := protocol.NewProtocolValidationError(msg)
				c.connection.Fail(validationError)
				resolvers.Reject(validationError)
				return
			}
			resolvers.Resolve(transformed)
		},
		reject: func(err error) {
			resolvers.Reject(err)
		},
	}

	sendCancel := func() {
		req.mu.Lock()
		sent := req.sent
		req.mu.Unlock()
		if !sent || !c.Connected() {
			return
		}
		cancelFrame, err := protocol.EncodeClientMessage(
			&protocol.CancelEnvelope{Type: "cancel", Id: id, Target: target},
			&protocol.FrameDecoderOptions{MaxFrameLength: &maxFrameLength},
		)
		if err != nil {
			c.connection.Fail(ToError(err))
			return
		}
		if err := c.connection.Send(cancelFrame); err != nil {
			if c.Connected() {
				c.connection.Fail(ToError(err))
			}
		}
	}

	var stopAfter func() bool
	if ctx != nil && ctx.Done() != nil {
		stopAfter = context.AfterFunc(ctx, func() {
			req.mu.Lock()
			if req.aborted {
				req.mu.Unlock()
				return
			}
			req.aborted = true
			sent := req.sent
			req.mu.Unlock()
			resolvers.Reject(clientAbortError(ctx))
			if sent {
				sendCancel()
			}
		})
	}
	req.cleanup = func() {
		if stopAfter != nil {
			stopAfter()
		}
	}

	c.pendingRequests[id] = req
	c.mu.Unlock()

	validatedCall, err := chord.ParseServiceCall(call)
	var frame []byte
	if err == nil {
		var encodedCall chord.JsonValue
		encodedCall, err = jsonx.Encode(validatedCall)
		if err == nil {
			frame, err = protocol.EncodeClientMessage(
				&protocol.RequestEnvelope{
					Type:   "request",
					Id:     id,
					Target: target,
					Call:   encodedCall,
				},
				&protocol.FrameDecoderOptions{MaxFrameLength: &maxFrameLength},
			)
		}
	}
	if err != nil {
		if taken := c.takePendingRequest(id); taken != nil {
			taken.reject(ToError(err))
		}
		return resolvers.Await()
	}

	if sendErr := c.connection.Send(frame); sendErr != nil {
		if taken := c.takePendingRequest(id); taken != nil {
			taken.reject(ToDisconnectedError(sendErr))
		}
		return resolvers.Await()
	}

	req.mu.Lock()
	req.sent = true
	aborted := req.aborted
	req.mu.Unlock()
	if aborted {
		sendCancel()
	}
	return resolvers.Await()
}

func (c *Client) handleMessage(message protocol.ServerMessage) {
	switch m := message.(type) {
	case *protocol.AttachmentEnvelope:
		var next *protocol.SessionTarget
		if st, ok := m.Attachment.Get(); ok {
			if st.ServerId != c.options.ServerId {
				c.connection.Fail(protocol.NewProtocolValidationError("Attachment update belongs to another server"))
				return
			}
			copied := st
			next = &copied
		}
		c.setAttachment(next)
		return

	case *protocol.ServiceEventEnvelope:
		c.mu.Lock()
		active := c.serviceListeners[m.SubscriptionId]
		c.mu.Unlock()
		if active == nil {
			return
		}
		active.mu.Lock()
		if active.disposed {
			active.mu.Unlock()
			return
		}
		if !active.hydrated {
			active.queuedWireUpdates = append(active.queuedWireUpdates, m.Update)
			active.mu.Unlock()
			return
		}
		wireUpdate, err := chord.ParseWireServiceProviderUpdate(m.Update)
		if err != nil {
			active.mu.Unlock()
			msg := err.Error()
			if msg == "" {
				msg = "Invalid service operation stream"
			}
			c.connection.Fail(protocol.NewProtocolValidationError(msg))
			return
		}
		update, err := active.decoder.DecodeUpdate(wireUpdate)
		if err != nil {
			active.mu.Unlock()
			msg := err.Error()
			if msg == "" {
				msg = "Invalid service operation stream"
			}
			c.connection.Fail(protocol.NewProtocolValidationError(msg))
			return
		}
		if active.ready {
			active.pendingDeliveries = append(active.pendingDeliveries, update)
			c.ensureDeliveryWorkerLocked(active)
		} else {
			active.queued = append(active.queued, update)
		}
		active.mu.Unlock()
		return

	case *protocol.ResponseSuccess:
		pending := c.takePendingRequest(m.Id)
		if pending == nil {
			c.connection.Fail(protocol.NewProtocolValidationError("Response has no matching request"))
			return
		}
		var res clientServiceResult
		if !m.Result.IsZero() {
			res.ok = true
			if !m.Result.IsNull() {
				res.value, _ = m.Result.Get()
			}
		}
		pending.resolve(res)
		return

	case *protocol.ResponseFailure:
		pending := c.takePendingRequest(m.Id)
		if pending == nil {
			c.connection.Fail(protocol.NewProtocolValidationError("Response has no matching request"))
			return
		}
		pending.reject(NewServerError(m.Error))
		return

	default:
		c.connection.Fail(protocol.NewProtocolValidationError("Response has no matching request"))
	}
}

func (c *Client) ensureDeliveryWorkerLocked(active *clientActiveServiceListener) {
	if active.delivering || len(active.pendingDeliveries) == 0 || active.disposed {
		return
	}
	active.delivering = true
	active.wg.Add(1)
	go c.runDeliveryWorker(active)
}

func (c *Client) runDeliveryWorker(active *clientActiveServiceListener) {
	defer active.wg.Done()
	for {
		active.mu.Lock()
		if active.disposed || len(active.pendingDeliveries) == 0 {
			active.delivering = false
			active.mu.Unlock()
			return
		}
		update := active.pendingDeliveries[0]
		active.pendingDeliveries[0] = nil
		active.pendingDeliveries = active.pendingDeliveries[1:]
		if len(active.pendingDeliveries) == 0 {
			active.pendingDeliveries = nil
		}
		listener := active.listener
		active.mu.Unlock()

		if listener != nil {
			if err := listener(update); err != nil {
				c.reportListenerError(err)
			}
		}
	}
}

func (c *Client) handleConnectionStateChange(change ConnectionStateChange) {
	if change.State == ConnectionStateDisconnected {
		c.mu.Lock()
		c.hello = nil
		pending := make([]*clientPendingRequest, 0, len(c.pendingRequests))
		for id, req := range c.pendingRequests {
			delete(c.pendingRequests, id)
			pending = append(pending, req)
		}
		for id := range c.serviceListeners {
			delete(c.serviceListeners, id)
		}
		c.mu.Unlock()

		c.setAttachment(nil)

		rejectErr := change.Error
		if rejectErr == nil {
			rejectErr = NewDisconnectedError(errorsDefaultDisconnectedMessage, nil)
		}
		for _, req := range pending {
			if req.cleanup != nil {
				req.cleanup()
			}
			req.reject(rejectErr)
		}
	}

	c.mu.Lock()
	listeners := make([]func(ConnectionStateChange), 0, c.connectionStateListeners.Len())
	for _, listener := range c.connectionStateListeners.All() {
		listeners = append(listeners, listener)
	}
	c.mu.Unlock()

	for _, listener := range listeners {
		listener(change)
	}
}

func (c *Client) takePendingRequest(id string) *clientPendingRequest {
	c.mu.Lock()
	req, ok := c.pendingRequests[id]
	if ok {
		delete(c.pendingRequests, id)
	}
	c.mu.Unlock()
	if ok && req.cleanup != nil {
		req.cleanup()
	}
	return req
}

func (c *Client) rejectPendingRequests(err error) {
	c.mu.Lock()
	pending := make([]*clientPendingRequest, 0, len(c.pendingRequests))
	for id, req := range c.pendingRequests {
		delete(c.pendingRequests, id)
		pending = append(pending, req)
	}
	c.mu.Unlock()
	for _, req := range pending {
		if req.cleanup != nil {
			req.cleanup()
		}
		req.reject(err)
	}
}

// Dispose permanently closes the client, rejects pending requests, and clears listeners.
func (c *Client) Dispose() error {
	c.mu.Lock()
	if c.disposeDone != nil {
		done := c.disposeDone
		c.mu.Unlock()
		<-done
		return nil
	}
	done := make(chan struct{})
	c.disposeDone = done
	c.disposed = true
	c.mu.Unlock()

	err := NewClientDisposedError()
	c.rejectPendingRequests(err)
	c.connection.Disconnect(err)

	c.mu.Lock()
	c.hello = nil
	c.mu.Unlock()

	c.setAttachment(nil)

	c.mu.Lock()
	c.connectionStateListeners.Clear()
	c.attachmentListeners.Clear()
	for id := range c.serviceListeners {
		delete(c.serviceListeners, id)
	}
	c.mu.Unlock()

	close(done)
	return nil
}

func (c *Client) setAttachment(attachment *protocol.SessionTarget) {
	c.mu.Lock()
	prev := c.attachment
	same := (prev == nil && attachment == nil) ||
		(prev != nil && attachment != nil &&
			prev.ServerId == attachment.ServerId &&
			prev.SessionId == attachment.SessionId &&
			prev.AttachmentId == attachment.AttachmentId)
	if same {
		c.mu.Unlock()
		return
	}
	if attachment == nil {
		c.attachment = nil
	} else {
		copied := *attachment
		c.attachment = &copied
	}
	listeners := make([]AttachmentChangeListener, 0, c.attachmentListeners.Len())
	for _, listener := range c.attachmentListeners.All() {
		listeners = append(listeners, listener)
	}
	c.mu.Unlock()

	for _, listener := range listeners {
		var arg *protocol.SessionTarget
		if attachment != nil {
			copied := *attachment
			arg = &copied
		}
		listener(arg)
	}
}

func (c *Client) targetIsCurrent(target protocol.RpcTarget) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch t := target.(type) {
	case *protocol.ServerTarget:
		return t != nil && c.hello != nil && c.hello.ServerId == t.ServerId
	case *protocol.SessionTarget:
		return t != nil && c.attachment != nil &&
			c.attachment.ServerId == t.ServerId &&
			c.attachment.SessionId == t.SessionId &&
			c.attachment.AttachmentId == t.AttachmentId
	default:
		return false
	}
}

func (c *Client) reportListenerError(err error) {
	if c.options.OnListenerError != nil {
		c.options.OnListenerError(ToError(err))
	}
}

// CreateClientServiceTransport adapts a lazily resolved routed client target to a Chord RemoteServiceTransport.
func CreateClientServiceTransport(client *Client, getTarget func() protocol.RpcTarget) chord.RemoteServiceTransport {
	return &clientChordServiceTransport{
		client:    client,
		getTarget: getTarget,
	}
}

type clientChordServiceTransport struct {
	client    *Client
	getTarget func() protocol.RpcTarget
}

func (t *clientChordServiceTransport) target() (protocol.RpcTarget, error) {
	if t.getTarget == nil {
		return nil, errors.New("Remote service target is unavailable")
	}
	resolved := t.getTarget()
	if resolved == nil {
		return nil, errors.New("Remote service target is unavailable")
	}
	return resolved, nil
}

func (t *clientChordServiceTransport) Invoke(call chord.ServiceCall, callContext chord.Context) (chord.JsonValue, bool, error) {
	target, err := t.target()
	if err != nil {
		return nil, false, err
	}
	var sig context.Context
	if callContext != nil {
		sig = callContext.AbortSignal()
	}
	return t.client.Request(sig, target, call)
}

func (t *clientChordServiceTransport) Subscribe(
	serviceId string,
	mode chord.ServiceMode,
	listener func(update chord.ServiceProviderUpdate, call chord.Context),
	callContext chord.Context,
) (chord.ServiceSubscription, error) {
	target, err := t.target()
	if err != nil {
		return nil, err
	}
	var sig context.Context
	if callContext != nil {
		sig = callContext.AbortSignal()
	}
	sub, err := t.client.SubscribeService(sig, target, serviceId, mode, func(update chord.ServiceProviderUpdate) error {
		listener(update, chord.BackgroundContext)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &clientChordServiceSubscription{sub: sub}, nil
}

type clientChordServiceSubscription struct {
	sub ServiceSubscription
}

func (s *clientChordServiceSubscription) Snapshot() chord.ServiceSubscriptionSnapshot {
	return s.sub.Snapshot()
}

func (s *clientChordServiceSubscription) Activate() error {
	s.sub.Start()
	return nil
}

func (s *clientChordServiceSubscription) Close(_ chord.Context) error {
	return s.sub.Dispose()
}

func clientAbortError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}
