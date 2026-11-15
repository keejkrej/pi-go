// Ported from packages/server/src/server.ts (pi v1.0.0).

package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

const (
	serverDefaultHandshakeTimeoutMs = 5_000
	serverMaxUint32                 = 0xffff_ffff
	serverMaxTimerDelayMs           = 2_147_483_647
)

type serverAggregateError struct {
	Message string
	Errors  []error
}

func (e *serverAggregateError) Error() string { return e.Message }

func (e *serverAggregateError) Unwrap() []error { return e.Errors }

// Server hosts protocol connections and routes server and session Chord service calls.
type Server[M SessionMetadata] struct {
	serverId                 protocol.ServerId
	host                     ServerHost[M]
	listeners                []ServerListener
	maxFrameLength           int
	handshakeTimeoutMs       int
	onConnectionCountChanged func(count int)
	onError                  func(err error)

	mu            sync.Mutex
	connections   map[*connState]struct{}
	sessions      *srSessionRouter[M]
	closing       bool
	closeStarted  bool
	closeDone     chan struct{}
	closeErr      error
	closedSettled bool
	closedDone    chan struct{}
	closedErr     error
	starting      bool
	startDone     chan struct{}
	started       bool
	disconnectWg  sync.WaitGroup
}

// NewServer constructs an unstarted Server validated against options.
func NewServer[M SessionMetadata](host ServerHost[M], options ServerOptions) (*Server[M], error) {
	maxFrameLength, handshakeTimeoutMs, err := serverResolveOptions(options)
	if err != nil {
		return nil, err
	}

	s := &Server[M]{
		serverId:                 options.ServerId,
		host:                     host,
		listeners:                options.Listeners,
		maxFrameLength:           maxFrameLength,
		handshakeTimeoutMs:       handshakeTimeoutMs,
		onConnectionCountChanged: options.OnConnectionCountChanged,
		onError:                  options.OnError,
		connections:              make(map[*connState]struct{}),
		closedDone:               make(chan struct{}),
	}
	s.sessions = srNewSessionRouter(srSessionRouterOptions[M]{
		Host:      host,
		ServerId:  options.ServerId,
		IsClosing: s.isClosing,
		PublishAttachment: func(client any, attachment *protocol.SessionTarget, _ chord.Context) error {
			conn, ok := client.(*connState)
			if !ok || conn == nil {
				return nil
			}
			var optAtt jsonx.Opt[protocol.SessionTarget]
			if attachment != nil {
				optAtt = jsonx.Some(*attachment)
			} else {
				optAtt = jsonx.Null[protocol.SessionTarget]()
			}
			s.sendMessage(conn, &protocol.AttachmentEnvelope{
				Type:       "attachment",
				Attachment: optAtt,
			})
			return nil
		},
		ReportError: s.reportError,
	})
	return s, nil
}

// ServerId returns the logical server UUIDv4 identity.
func (s *Server[M]) ServerId() protocol.ServerId {
	return s.serverId
}

// Closed blocks until the server completes shutdown and returns any shutdown error.
func (s *Server[M]) Closed() error {
	<-s.closedDone
	return s.closedErr
}

// ClosedChan returns a channel that closes when shutdown settles.
func (s *Server[M]) ClosedChan() <-chan struct{} {
	return s.closedDone
}

// Start begins listening on all configured ServerListeners.
func (s *Server[M]) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("Server is already started")
	}
	if s.starting {
		s.mu.Unlock()
		return errors.New("Server is already starting")
	}
	if s.closing {
		s.mu.Unlock()
		return errors.New("Server is closing or closed")
	}
	s.starting = true
	startDone := make(chan struct{})
	s.startDone = startDone
	s.mu.Unlock()

	return s.startInternal(startDone)
}

func (s *Server[M]) startInternal(startDone chan struct{}) error {
	started := make([]ServerListener, 0, len(s.listeners))
	var startErr error
	for _, listener := range s.listeners {
		if err := listener.Start(func(connection ByteConnection) ByteConnectionHandler {
			return s.Accept(connection)
		}); err != nil {
			startErr = err
			break
		}
		started = append(started, listener)
	}

	if startErr == nil {
		s.mu.Lock()
		s.started = true
		s.starting = false
		s.startDone = nil
		s.mu.Unlock()
		close(startDone)
		return nil
	}

	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()

	var cleanupErrors []error
	if len(started) > 0 {
		errCh := make(chan error, len(started))
		var wg sync.WaitGroup
		for _, listener := range started {
			wg.Add(1)
			go func(l ServerListener) {
				defer wg.Done()
				if err := l.Close(); err != nil {
					errCh <- err
				}
			}(listener)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			cleanupErrors = append(cleanupErrors, err)
		}
	}

	if err := s.closeServerState(); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}

	s.mu.Lock()
	s.starting = false
	s.startDone = nil
	s.mu.Unlock()
	close(startDone)

	if len(cleanupErrors) > 0 {
		allErrs := append([]error{startErr}, cleanupErrors...)
		failure := &serverAggregateError{
			Message: "Server startup and cleanup failed",
			Errors:  allErrs,
		}
		s.settleClosed(failure)
		return failure
	}
	s.settleClosed(nil)
	return startErr
}

// Accept attaches one inbound ByteConnection to the server protocol state machine.
func (s *Server[M]) Accept(connection ByteConnection) ByteConnectionHandler {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		go s.closeConnection(connection, nil)
		return ByteConnectionHandler{
			OnData:  func([]byte) {},
			OnClose: func() {},
			OnError: func(err error) { s.reportError(err) },
		}
	}

	decoder, _ := protocol.NewClientMessageDecoder(&protocol.FrameDecoderOptions{
		MaxFrameLength: &s.maxFrameLength,
	})
	state := &connState{
		connection:           connection,
		decoder:              decoder,
		serviceStateEncoders: make(map[string]chord.ServiceStateEncoder),
		stage:                connStageAwaitingHello,
		activeRequests:       make(map[string]*connActiveRequest),
		outbound:             connNewOutboundQueue(connection),
	}
	state.mu.Lock()
	state.handshakeTimer = time.AfterFunc(time.Duration(s.handshakeTimeoutMs)*time.Millisecond, func() {
		s.failProtocol(state, protocol.ProtocolError{
			Code:    "invalid_request",
			Message: "Handshake timeout",
		})
	})
	state.mu.Unlock()

	s.connections[state] = struct{}{}
	count := len(s.connections)
	s.mu.Unlock()

	s.notifyConnectionCountChanged(count)

	return ByteConnectionHandler{
		OnData:  func(chunk []byte) { s.receive(state, chunk) },
		OnClose: func() { s.transportClosed(state) },
		OnError: func(err error) {
			s.reportError(err)
			s.closeConnection(connection, nil)
			s.disconnect(state)
		},
	}
}

// Close shuts down listeners, active connections, and routed sessions.
func (s *Server[M]) Close() error {
	s.mu.Lock()
	if s.closeStarted {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return s.closeErr
	}
	s.closing = true
	s.closeStarted = true
	closeDone := make(chan struct{})
	s.closeDone = closeDone
	startDone := s.startDone
	s.mu.Unlock()

	err := s.closeInternal(startDone)
	s.closeErr = err
	close(closeDone)
	return err
}

func (s *Server[M]) closeInternal(startDone chan struct{}) error {
	if startDone != nil {
		<-startDone
	}

	var errs []error
	if len(s.listeners) > 0 {
		errCh := make(chan error, len(s.listeners))
		var wg sync.WaitGroup
		for _, listener := range s.listeners {
			wg.Add(1)
			go func(l ServerListener) {
				defer wg.Done()
				if err := l.Close(); err != nil {
					errCh <- err
				}
			}(listener)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			errs = append(errs, err)
		}
	}

	if err := s.closeServerState(); err != nil {
		errs = append(errs, err)
	}

	s.disconnectWg.Wait()

	s.mu.Lock()
	s.started = false
	s.mu.Unlock()

	if len(errs) > 0 {
		var failure error
		if len(errs) == 1 {
			failure = errs[0]
		} else {
			failure = &serverAggregateError{
				Message: "Server shutdown failed",
				Errors:  errs,
			}
		}
		s.settleClosed(failure)
		return failure
	}
	s.settleClosed(nil)
	return nil
}

func (s *Server[M]) receive(state *connState, chunk []byte) {
	if state.isTerminal() {
		return
	}
	state.decodeMu.Lock()
	if state.isTerminal() {
		state.decodeMu.Unlock()
		return
	}
	messages, err := state.decoder.Push(chunk)
	state.decodeMu.Unlock()
	if err != nil {
		s.failProtocol(state, s.toProtocolError(err))
		return
	}
	for _, msg := range messages {
		if state.isTerminal() {
			return
		}
		s.dispatchMessage(state, msg)
	}
}

func (s *Server[M]) dispatchMessage(state *connState, message protocol.ClientMessage) {
	state.mu.Lock()
	if state.isTerminalLocked() {
		state.mu.Unlock()
		return
	}
	stage := state.stage
	if stage == connStageAwaitingHello {
		hello, ok := message.(*protocol.ClientHello)
		if !ok {
			state.mu.Unlock()
			s.failProtocol(state, protocol.ProtocolError{
				Code:    "invalid_request",
				Message: "The first client message must be hello",
			})
			return
		}
		state.stage = connStageHandshaking
		state.handshakeDone = make(chan struct{})
		state.mu.Unlock()

		go func() {
			defer close(state.handshakeDone)
			if err := s.finishHandshake(state, hello); err != nil {
				s.failProtocol(state, s.toProtocolError(err))
			}
		}()
		return
	}

	if _, isHello := message.(*protocol.ClientHello); isHello {
		state.mu.Unlock()
		s.failProtocol(state, protocol.ProtocolError{
			Code:    "invalid_request",
			Message: "hello may only be sent as the first message",
		})
		return
	}

	if stage == connStageReady {
		state.mu.Unlock()
		s.dispatchReadyMessage(state, message)
		return
	}

	if stage != connStageHandshaking {
		state.mu.Unlock()
		return
	}

	state.pendingPostHello = append(state.pendingPostHello, message)
	state.mu.Unlock()
}

func (s *Server[M]) dispatchReadyMessage(state *connState, message protocol.ClientMessage) {
	switch m := message.(type) {
	case *protocol.CancelEnvelope:
		s.handleCancel(state, m)
	case *protocol.RequestEnvelope:
		go s.handleRequest(state, m)
	}
}

func (s *Server[M]) finishHandshake(state *connState, hello *protocol.ClientHello) error {
	if !protocol.IsSupportedProtocolVersion(float64(hello.Version)) {
		s.failProtocol(state, protocol.ProtocolError{
			Code:    "version",
			Message: fmt.Sprintf("Unsupported protocol version %d; expected %d", hello.Version, protocol.ProtocolVersion),
		})
		return nil
	}

	state.mu.Lock()
	stale := s.isClosing() || state.disconnected || state.stage != connStageHandshaking || state.connection.Closed()
	state.mu.Unlock()
	if stale {
		return nil
	}

	services, err := s.host.ServerServices().AttachClient(
		&serverPresentation[M]{server: s, state: state},
		chord.TodoContext,
	)
	if err != nil {
		return err
	}

	state.mu.Lock()
	stale = s.isClosing() || state.disconnected || state.stage != connStageHandshaking || state.connection.Closed()
	if stale {
		state.mu.Unlock()
		if services != nil {
			_ = services.Release(chord.TodoContext)
		}
		return nil
	}
	state.serverServices = services
	state.mu.Unlock()

	sent := s.sendMessage(state, &protocol.ServerHello{
		Type:     "hello",
		Version:  protocol.ProtocolVersion,
		ServerId: s.serverId,
	})
	if !sent {
		return nil
	}

	state.mu.Lock()
	if !state.disconnected && state.stage == connStageHandshaking {
		state.stage = connStageReady
		if state.handshakeTimer != nil {
			state.handshakeTimer.Stop()
		}
		queued := state.pendingPostHello
		state.pendingPostHello = nil
		state.mu.Unlock()
		for _, msg := range queued {
			if state.isTerminal() {
				break
			}
			s.dispatchReadyMessage(state, msg)
		}
		return nil
	}
	state.mu.Unlock()
	return nil
}

func (s *Server[M]) handleCancel(state *connState, envelope *protocol.CancelEnvelope) {
	if envelope.Target == nil {
		return
	}
	targetServerId := serverTargetServerId(envelope.Target)
	if targetServerId != s.serverId {
		return
	}

	state.mu.Lock()
	active := state.activeRequests[envelope.Id]
	var cancel context.CancelCauseFunc
	if active != nil && serverSameTarget(active.target, envelope.Target) {
		cancel = active.cancel
	}
	state.mu.Unlock()

	if cancel != nil {
		cancel(&js.JSError{ErrName: "AbortError", Message: "RPC request cancelled"})
	}
}

func (s *Server[M]) handleRequest(state *connState, envelope *protocol.RequestEnvelope) {
	state.mu.Lock()
	if state.isTerminalLocked() {
		state.mu.Unlock()
		return
	}
	if _, exists := state.activeRequests[envelope.Id]; exists {
		state.mu.Unlock()
		s.sendMessage(state, &protocol.ResponseFailure{
			Type:  "response",
			Id:    envelope.Id,
			Ok:    false,
			Error: protocol.ProtocolError{Code: "invalid_request", Message: "Request ID is already active"},
		})
		return
	}
	state.mu.Unlock()

	call, parseErr := chord.ParseServiceCall(envelope.Call)
	if parseErr != nil {
		s.sendMessage(state, &protocol.ResponseFailure{
			Type:  "response",
			Id:    envelope.Id,
			Ok:    false,
			Error: protocol.ProtocolError{Code: "invalid_request", Message: "Invalid service call"},
		})
		return
	}

	reqCtx, cancel := context.WithCancelCause(context.Background())
	active := &connActiveRequest{
		target: envelope.Target,
		cancel: cancel,
	}

	state.mu.Lock()
	if state.isTerminalLocked() {
		state.mu.Unlock()
		cancel(errors.New("Client disconnected"))
		return
	}
	if _, exists := state.activeRequests[envelope.Id]; exists {
		state.mu.Unlock()
		cancel(errors.New("Duplicate request ID"))
		s.sendMessage(state, &protocol.ResponseFailure{
			Type:  "response",
			Id:    envelope.Id,
			Ok:    false,
			Error: protocol.ProtocolError{Code: "invalid_request", Message: "Request ID is already active"},
		})
		return
	}
	state.activeRequests[envelope.Id] = active
	serverServices := state.serverServices
	state.mu.Unlock()

	defer func() {
		cancel(nil)
		state.mu.Lock()
		if state.activeRequests[envelope.Id] == active {
			delete(state.activeRequests, envelope.Id)
		}
		state.mu.Unlock()
	}()

	callContext := chord.WithAbortSignal(reqCtx, chord.TodoContext)
	control := chord.DecodeServiceControlCall(call)
	var subscribing *chord.ServiceControlCallSubscribe
	if sub, ok := control.(chord.ServiceControlCallSubscribe); ok {
		subscribing = &sub
	}

	var (
		subMu                    sync.Mutex
		pendingUpdates           []chord.ServiceProviderUpdate
		subscriptionReady        = subscribing == nil
		installedSubscriptionEnc = false
		responded                = false
	)

	publish := func(subscriptionId string, update chord.ServiceProviderUpdate, _ chord.Context) error {
		subMu.Lock()
		if subscribing != nil && subscriptionId == subscribing.SubscriptionId && !subscriptionReady {
			pendingUpdates = append(pendingUpdates, update)
			subMu.Unlock()
			return nil
		}
		subMu.Unlock()
		s.sendServiceUpdate(state, subscriptionId, update)
		return nil
	}

	handleErr := func(err error) {
		if subscribing != nil && installedSubscriptionEnc && !responded {
			state.mu.Lock()
			delete(state.serviceStateEncoders, subscribing.SubscriptionId)
			state.mu.Unlock()
		}
		if responded {
			s.reportError(err)
			s.closeConnection(state.connection, nil)
			s.disconnect(state)
			return
		}
		protoErr := s.toProtocolError(err)
		if reqCtx.Err() != nil {
			protoErr = protocol.ProtocolError{Code: "cancelled", Message: "RPC request cancelled"}
		}
		s.sendMessage(state, &protocol.ResponseFailure{
			Type:  "response",
			Id:    envelope.Id,
			Ok:    false,
			Error: protoErr,
		})
	}

	if serverTargetServerId(envelope.Target) != s.serverId {
		handleErr(NewWrongServerError())
		return
	}
	if subscribing != nil {
		state.mu.Lock()
		_, dup := state.serviceStateEncoders[subscribing.SubscriptionId]
		state.mu.Unlock()
		if dup {
			handleErr(protocol.NewProtocolValidationError(fmt.Sprintf("Duplicate service subscription %s", subscribing.SubscriptionId)))
			return
		}
	}

	var (
		resVal chord.JsonValue
		resOk  bool
		err    error
	)
	if _, isSession := envelope.Target.(*protocol.SessionTarget); isSession {
		resVal, resOk, err = s.sessions.ExecuteServiceCall(call, envelope.Target, state, publish, callContext)
	} else if serverServices != nil {
		resVal, resOk, err = serverServices.InvokeService(call, publish, callContext)
	} else {
		err = protocol.NewProtocolValidationError(fmt.Sprintf("Unknown service member %s.%s", call.ServiceId, call.Member))
	}
	if err != nil {
		handleErr(err)
		return
	}

	if subscribing != nil {
		if !resOk {
			handleErr(protocol.NewProtocolValidationError("Service subscription did not return a snapshot"))
			return
		}
		snap, parseErr := chord.ParseServiceSubscriptionSnapshot(resVal)
		if parseErr != nil {
			handleErr(parseErr)
			return
		}
		stateEncoder := chord.CreateServiceStateEncoder()
		wireSnap, encErr := stateEncoder.EncodeSnapshot(snap)
		if encErr != nil {
			handleErr(encErr)
			return
		}
		jsonSnap, jsonErr := jsonx.Encode(wireSnap)
		if jsonErr != nil {
			handleErr(jsonErr)
			return
		}
		resVal = jsonSnap
		resOk = true

		state.mu.Lock()
		state.serviceStateEncoders[subscribing.SubscriptionId] = stateEncoder
		state.mu.Unlock()
		installedSubscriptionEnc = true
	} else if unsub, ok := control.(chord.ServiceControlCallUnsubscribe); ok {
		state.mu.Lock()
		delete(state.serviceStateEncoders, unsub.SubscriptionId)
		state.mu.Unlock()
	} else if _, isCatalogue := control.(chord.ServiceControlCallCatalogue); isCatalogue {
		if !resOk {
			handleErr(protocol.NewProtocolValidationError("Invalid service catalogue"))
			return
		}
		catalogue, parseErr := chord.ParseServiceCatalogue(resVal)
		if parseErr != nil {
			handleErr(parseErr)
			return
		}
		wireCatalogue := make([]any, len(catalogue))
		for i, entry := range catalogue {
			wireCatalogue[i] = jsonx.ObjectOf("serviceId", entry.ServiceId, "mode", string(entry.Mode))
		}
		resVal = wireCatalogue
	}

	var resultOpt jsonx.Opt[chord.JsonValue]
	if resOk {
		if resVal == nil {
			resultOpt = jsonx.Null[chord.JsonValue]()
		} else {
			resultOpt = jsonx.Some(resVal)
		}
	}
	s.sendMessage(state, &protocol.ResponseSuccess{
		Type:   "response",
		Id:     envelope.Id,
		Ok:     true,
		Result: resultOpt,
	})
	responded = true

	if subscribing != nil {
		for {
			subMu.Lock()
			if len(pendingUpdates) == 0 {
				subscriptionReady = true
				subMu.Unlock()
				break
			}
			batch := pendingUpdates
			pendingUpdates = nil
			subMu.Unlock()
			for _, update := range batch {
				s.sendServiceUpdate(state, subscribing.SubscriptionId, update)
			}
		}
	}
}

func (s *Server[M]) transportClosed(state *connState) {
	state.mu.Lock()
	shouldEnd := !state.disconnected && state.stage != connStageClosing
	decoder := state.decoder
	state.mu.Unlock()

	if shouldEnd {
		state.decodeMu.Lock()
		err := decoder.End()
		state.decodeMu.Unlock()
		if err != nil {
			s.reportError(err)
		}
	}
	s.disconnect(state)
}

func (s *Server[M]) disconnect(state *connState) {
	state.mu.Lock()
	if state.disconnected {
		state.mu.Unlock()
		return
	}
	state.disconnected = true
	state.stage = connStageClosed
	if state.handshakeTimer != nil {
		state.handshakeTimer.Stop()
	}
	cancels := make([]context.CancelCauseFunc, 0, len(state.activeRequests))
	for _, req := range state.activeRequests {
		cancels = append(cancels, req.cancel)
	}
	clear(state.activeRequests)
	clear(state.serviceStateEncoders)
	state.pendingPostHello = nil
	serverServices := state.serverServices
	state.serverServices = nil
	outbound := state.outbound
	state.mu.Unlock()

	if outbound != nil {
		outbound.stop()
	}

	disconnectErr := errors.New("Client disconnected")
	for _, cancel := range cancels {
		cancel(disconnectErr)
	}

	s.mu.Lock()
	_, existed := s.connections[state]
	delete(s.connections, state)
	count := len(s.connections)
	s.mu.Unlock()

	if existed {
		s.notifyConnectionCountChanged(count)
	}

	s.disconnectWg.Add(1)
	go func() {
		defer s.disconnectWg.Done()
		if err := s.sessions.Disconnect(state, chord.TodoContext); err != nil {
			s.reportError(err)
		}
		if serverServices != nil {
			if err := serverServices.Release(chord.TodoContext); err != nil {
				s.reportError(err)
			}
		}
	}()
}

func (s *Server[M]) sendServiceUpdate(state *connState, subscriptionId string, update chord.ServiceProviderUpdate) {
	state.mu.Lock()
	if state.disconnected || state.connection.Closed() {
		state.mu.Unlock()
		return
	}
	stateEncoder := state.serviceStateEncoders[subscriptionId]
	outbound := state.outbound
	state.mu.Unlock()
	if stateEncoder == nil || outbound == nil {
		return
	}

	err := outbound.send(func() ([]byte, error) {
		wireUpdate, encErr := stateEncoder.EncodeUpdate(update)
		if encErr != nil {
			return nil, encErr
		}
		jsonUpdate, jsonErr := jsonx.Encode(wireUpdate)
		if jsonErr != nil {
			return nil, jsonErr
		}
		return protocol.EncodeServerMessage(&protocol.ServiceEventEnvelope{
			Type:           "service_update",
			SubscriptionId: subscriptionId,
			Update:         jsonUpdate,
		}, &protocol.FrameDecoderOptions{MaxFrameLength: &s.maxFrameLength})
	})
	if err != nil {
		state.mu.Lock()
		alreadyDisc := state.disconnected
		state.mu.Unlock()
		if !alreadyDisc {
			s.reportError(err)
			s.closeConnection(state.connection, nil)
			s.disconnect(state)
		}
	}
}

func (s *Server[M]) sendMessage(state *connState, message protocol.ServerMessage) bool {
	state.mu.Lock()
	if state.disconnected || state.connection.Closed() {
		state.mu.Unlock()
		return false
	}
	outbound := state.outbound
	state.mu.Unlock()
	if outbound == nil {
		return false
	}

	err := outbound.send(func() ([]byte, error) {
		return protocol.EncodeServerMessage(message, &protocol.FrameDecoderOptions{
			MaxFrameLength: &s.maxFrameLength,
		})
	})
	if err != nil {
		state.mu.Lock()
		alreadyDisc := state.disconnected
		state.mu.Unlock()
		if !alreadyDisc {
			s.reportError(err)
			s.closeConnection(state.connection, nil)
			s.disconnect(state)
		}
		return false
	}
	return true
}

func (s *Server[M]) failProtocol(state *connState, err protocol.ProtocolError) {
	state.mu.Lock()
	if state.disconnected || state.stage == connStageClosing || state.stage == connStageClosed {
		state.mu.Unlock()
		return
	}
	state.stage = connStageClosing
	if state.handshakeTimer != nil {
		state.handshakeTimer.Stop()
	}
	outbound := state.outbound
	state.mu.Unlock()

	if outbound != nil {
		outbound.stop()
	}

	var finalFrame []byte
	msg := &protocol.ServerHelloError{Type: "hello_error", Error: err}
	frame, encErr := protocol.EncodeServerMessage(msg, &protocol.FrameDecoderOptions{
		MaxFrameLength: &s.maxFrameLength,
	})
	if encErr != nil {
		s.reportError(encErr)
	} else {
		finalFrame = frame
	}

	s.closeConnection(state.connection, finalFrame)
	s.disconnect(state)
}

func (s *Server[M]) closeServerState() error {
	s.mu.Lock()
	conns := make([]*connState, 0, len(s.connections))
	for conn := range s.connections {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	for _, conn := range conns {
		conn.mu.Lock()
		conn.stage = connStageClosing
		if conn.handshakeTimer != nil {
			conn.handshakeTimer.Stop()
		}
		outbound := conn.outbound
		conn.mu.Unlock()
		if outbound != nil {
			outbound.stop()
		}
	}

	var closeWg sync.WaitGroup
	for _, conn := range conns {
		closeWg.Add(1)
		go func(c *connState) {
			defer closeWg.Done()
			s.closeConnection(c.connection, nil)
		}(conn)
	}
	closeWg.Wait()

	for _, conn := range conns {
		s.disconnect(conn)
	}

	err := s.sessions.Close(chord.BackgroundContext)

	s.mu.Lock()
	clear(s.connections)
	s.mu.Unlock()

	return err
}

func (s *Server[M]) closeConnection(connection ByteConnection, finalChunk []byte) {
	if err := connection.Close(finalChunk); err != nil {
		s.reportError(err)
	}
}

func (s *Server[M]) toProtocolError(err error) protocol.ProtocolError {
	var serverErr *ServerError
	if errors.As(err, &serverErr) {
		return protocol.ProtocolError{Code: serverErr.Code, Message: serverErr.Message}
	}
	var remoteErr *chord.RemoteServiceError
	if errors.As(err, &remoteErr) {
		return protocol.ProtocolError{Code: protocol.ProtocolErrorCode(remoteErr.Code), Message: remoteErr.Message}
	}
	var validationErr *protocol.ProtocolValidationError
	if errors.As(err, &validationErr) {
		return protocol.ProtocolError{Code: "invalid_request", Message: validationErr.Message}
	}
	s.reportError(err)
	return protocol.ProtocolError{Code: "internal_error", Message: InternalServerErrorMessage}
}

func (s *Server[M]) notifyConnectionCountChanged(count int) {
	if s.onConnectionCountChanged == nil {
		return
	}
	s.onConnectionCountChanged(count)
}

func (s *Server[M]) reportError(err error) {
	if err == nil || s.onError == nil {
		return
	}
	s.onError(err)
}

func (s *Server[M]) settleClosed(err error) {
	s.mu.Lock()
	if s.closedSettled {
		s.mu.Unlock()
		return
	}
	s.closedSettled = true
	s.closedErr = err
	s.mu.Unlock()
	close(s.closedDone)
}

func (s *Server[M]) isClosing() bool {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	return closing
}

type serverPresentation[M SessionMetadata] struct {
	server *Server[M]
	state  *connState
}

func (p *serverPresentation[M]) AttachSession(sessionId string, callContext chord.Context) error {
	return p.server.sessions.AttachClient(p.state, sessionId, callContext)
}

func (p *serverPresentation[M]) DetachSession(callContext chord.Context) error {
	return p.server.sessions.DetachClient(p.state, callContext)
}

func (p *serverPresentation[M]) PrepareSessionRemoval(sessionId string, callContext chord.Context) error {
	return p.server.sessions.RemoveSession(sessionId, callContext)
}

func serverTargetServerId(target protocol.RpcTarget) protocol.ServerId {
	switch t := target.(type) {
	case *protocol.ServerTarget:
		if t != nil {
			return t.ServerId
		}
	case *protocol.SessionTarget:
		if t != nil {
			return t.ServerId
		}
	}
	return ""
}

func serverSameTarget(left, right protocol.RpcTarget) bool {
	if serverTargetServerId(left) != serverTargetServerId(right) {
		return false
	}
	lSess, lIsSess := left.(*protocol.SessionTarget)
	rSess, rIsSess := right.(*protocol.SessionTarget)
	if !lIsSess || !rIsSess {
		return !lIsSess && !rIsSess
	}
	return lSess != nil && rSess != nil && lSess.SessionId == rSess.SessionId && lSess.AttachmentId == rSess.AttachmentId
}

func serverResolveOptions(options ServerOptions) (maxFrameLength int, handshakeTimeoutMs int, err error) {
	if options.Listeners == nil {
		return 0, 0, js.NewTypeError("Server listeners must be an array")
	}
	if !protocol.IsServerId(options.ServerId) {
		return 0, 0, js.NewTypeError("serverId must be a canonical lowercase UUIDv4")
	}
	maxFrameLength = protocol.DefaultMaxFrameLength
	if options.MaxFrameLength != nil {
		maxFrameLength = *options.MaxFrameLength
	}
	if maxFrameLength <= 0 || maxFrameLength > serverMaxUint32 {
		return 0, 0, js.NewTypeError(fmt.Sprintf("Server maxFrameLength must be an integer between 1 and %d", serverMaxUint32))
	}
	handshakeTimeoutMs = serverDefaultHandshakeTimeoutMs
	if options.HandshakeTimeoutMs != nil {
		handshakeTimeoutMs = *options.HandshakeTimeoutMs
	}
	if handshakeTimeoutMs <= 0 || handshakeTimeoutMs > serverMaxTimerDelayMs {
		return 0, 0, js.NewTypeError(fmt.Sprintf("Server handshakeTimeoutMs must be an integer between 1 and %d", serverMaxTimerDelayMs))
	}
	return maxFrameLength, handshakeTimeoutMs, nil
}
