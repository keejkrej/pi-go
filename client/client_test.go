// Ported from packages/client/test/client.test.ts (pi v1.0.0).

package client_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/client/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/protocol/cbor"
)

const (
	testServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")
	testTimeout  = 5 * time.Second
)

var testServerTarget = &protocol.ServerTarget{ServerId: testServerId}

func connectTestClient(server *testkit.MemoryByteServer, expectedServerId ...protocol.ServerId) (*client.Client, error) {
	sid := testServerId
	if len(expectedServerId) > 0 && expectedServerId[0] != "" {
		sid = expectedServerId[0]
	}
	return client.Connect(client.ClientOptions{
		ServerId: sid,
		TransportFactory: func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
			return server.Connect(handlers)
		},
	})
}

func attachTestClient(t *testing.T, c *client.Client, server *testkit.MemoryByteServer, sessionId string) {
	t.Helper()
	expectedMessages := len(server.Messages()) + 1
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	type reqRes struct {
		err error
	}
	done := make(chan reqRes, 1)
	go func() {
		_, _, err := c.Request(ctx, testServerTarget, chord.ServiceCall{
			ServiceId: "pi.session-management",
			Member:    "attach",
			Args:      []chord.JsonValue{sessionId},
		})
		done <- reqRes{err: err}
	}()

	if err := server.WaitForMessages(ctx, expectedMessages); err != nil {
		t.Fatalf("WaitForMessages(%d) failed: %v", expectedMessages, err)
	}
	msgs := server.Messages()
	req, ok := msgs[len(msgs)-1].(*protocol.RequestEnvelope)
	if !ok {
		t.Fatalf("missing attach request, got %T", msgs[len(msgs)-1])
	}
	if err := server.Send(&protocol.AttachmentEnvelope{
		Type: "attachment",
		Attachment: jsonx.Some(protocol.SessionTarget{
			ServerId:     testServerId,
			SessionId:    sessionId,
			AttachmentId: "attachment-" + sessionId,
		}),
	}); err != nil {
		t.Fatalf("Send attachment failed: %v", err)
	}
	if err := server.Send(&protocol.ResponseSuccess{
		Type:   "response",
		Id:     req.Id,
		Ok:     true,
		Result: jsonx.Null[chord.JsonValue](),
	}); err != nil {
		t.Fatalf("Send response failed: %v", err)
	}
	res := <-done
	if res.err != nil {
		t.Fatalf("attach request failed: %v", res.err)
	}
}

func pollUntil(t *testing.T, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestRequiresCanonicalUUIDv4ServerIdentity(t *testing.T) {
	_, err := client.NewClient(client.ClientOptions{
		ServerId: protocol.ServerId("invalid-server"),
		TransportFactory: func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
			return nil, errors.New("rejected")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "serverId") {
		t.Fatalf("expected serverId validation error, got %v", err)
	}
}

func TestConnectsOnlyToExpectedLogicalServer(t *testing.T) {
	matching := testkit.NewMemoryByteServer()
	c, err := connectTestClient(matching)
	if err != nil {
		t.Fatalf("connect matching failed: %v", err)
	}
	if h := c.Hello(); h == nil || h.ServerId != testServerId {
		t.Fatalf("unexpected hello: %+v", h)
	}
	if err := c.Dispose(); err != nil {
		t.Fatalf("dispose failed: %v", err)
	}

	wrong := testkit.NewMemoryByteServer(protocol.ServerId("00000000-0000-4000-8000-000000000002"))
	_, err = connectTestClient(wrong)
	var pve *protocol.ProtocolValidationError
	if !errors.As(err, &pve) {
		t.Fatalf("expected ProtocolValidationError, got %T (%v)", err, err)
	}
}

func TestUpdatesAttachmentStateFromOutOfBandServerRouting(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	var mu sync.Mutex
	var changes []string
	_, err = c.OnAttachmentChange(func(att *protocol.SessionTarget) {
		mu.Lock()
		defer mu.Unlock()
		if att == nil {
			changes = append(changes, "<nil>")
		} else {
			changes = append(changes, att.SessionId)
		}
	})
	if err != nil {
		t.Fatalf("OnAttachmentChange failed: %v", err)
	}

	attachTestClient(t, c, server, "session-1")
	att := c.Attachment()
	if att == nil || att.SessionId != "session-1" || att.AttachmentId != "attachment-session-1" {
		t.Fatalf("unexpected attachment: %+v", att)
	}
	msgs := server.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected >= 2 messages, got %d", len(msgs))
	}
	req, ok := msgs[1].(*protocol.RequestEnvelope)
	if !ok {
		t.Fatalf("expected RequestEnvelope at index 1, got %T", msgs[1])
	}
	if !reflect.DeepEqual(req.Target, testServerTarget) {
		t.Fatalf("unexpected target: %#v", req.Target)
	}
	call, err := chord.ParseServiceCall(req.Call)
	if err != nil {
		t.Fatalf("ParseServiceCall failed: %v", err)
	}
	if call.ServiceId != "pi.session-management" || call.Member != "attach" || !reflect.DeepEqual(call.Args, []chord.JsonValue{"session-1"}) {
		t.Fatalf("unexpected call: %+v", call)
	}

	if err := server.Send(&protocol.AttachmentEnvelope{
		Type:       "attachment",
		Attachment: jsonx.Null[protocol.SessionTarget](),
	}); err != nil {
		t.Fatalf("send clear attachment failed: %v", err)
	}
	if c.Attachment() != nil {
		t.Fatalf("expected nil attachment, got %+v", c.Attachment())
	}
	mu.Lock()
	gotChanges := append([]string(nil), changes...)
	mu.Unlock()
	if !reflect.DeepEqual(gotChanges, []string{"session-1", "<nil>"}) {
		t.Fatalf("unexpected attachment changes: %v", gotChanges)
	}
}

func TestBuffersServiceUpdatesUntilSubscriptionSnapshotArrives(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	attachTestClient(t, c, server, "session-1")
	target := c.Attachment()
	if target == nil {
		t.Fatal("missing attachment")
	}
	transport := client.CreateClientServiceTransport(c, func() protocol.RpcTarget {
		a := c.Attachment()
		if a == nil {
			return nil
		}
		return a
	})

	var updMu sync.Mutex
	var updates []chord.ServiceProviderUpdate
	type subRes struct {
		sub chord.ServiceSubscription
		err error
	}
	opening := make(chan subRes, 1)
	go func() {
		sub, err := transport.Subscribe(
			"pi.models",
			chord.ServiceModeSingleton,
			func(update chord.ServiceProviderUpdate, _ chord.Context) {
				updMu.Lock()
				updates = append(updates, update)
				updMu.Unlock()
			},
			chord.BackgroundContext,
		)
		opening <- subRes{sub: sub, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.WaitForMessages(ctx, 3); err != nil {
		t.Fatalf("WaitForMessages(3) failed: %v", err)
	}
	msgs := server.Messages()
	subReq, ok := msgs[2].(*protocol.RequestEnvelope)
	if !ok {
		t.Fatalf("expected RequestEnvelope at 2, got %T", msgs[2])
	}
	if !reflect.DeepEqual(subReq.Target, target) {
		t.Fatalf("expected target %+v, got %+v", target, subReq.Target)
	}
	subCall, err := chord.ParseServiceCall(subReq.Call)
	if err != nil {
		t.Fatalf("ParseServiceCall failed: %v", err)
	}
	if subCall.ServiceId != "$chord.service" || subCall.Member != "subscribe" ||
		!reflect.DeepEqual(subCall.Args, []chord.JsonValue{"service-1", "pi.models", "singleton"}) {
		t.Fatalf("unexpected subscribe call: %+v", subCall)
	}

	if err := server.Send(&protocol.ServiceEventEnvelope{
		Type:           "service_update",
		SubscriptionId: "service-1",
		Update: jsonx.ObjectOf(
			"type", "state",
			"member", "state",
			"sequence", float64(1),
			"ops", []any{[]any{"s", []any{"revision"}, float64(1)}},
		),
	}); err != nil {
		t.Fatalf("send buffered state update failed: %v", err)
	}

	updMu.Lock()
	if len(updates) != 0 {
		updMu.Unlock()
		t.Fatalf("expected 0 updates before snapshot, got %d", len(updates))
	}
	updMu.Unlock()

	if err := server.Send(&protocol.ResponseSuccess{
		Type: "response",
		Id:   "request-2",
		Ok:   true,
		Result: jsonx.Some[chord.JsonValue](jsonx.ObjectOf(
			"serviceId", "pi.models",
			"mode", "singleton",
			"instances", []any{
				jsonx.ObjectOf(
					"members", []any{
						jsonx.ObjectOf(
							"name", "state",
							"kind", "state",
							"sequence", float64(0),
							"ops", []any{
								[]any{"r", jsonx.ObjectOf("revision", float64(0))},
							},
						),
					},
				),
			},
		)),
	}); err != nil {
		t.Fatalf("send subscribe response failed: %v", err)
	}

	sr := <-opening
	if sr.err != nil {
		t.Fatalf("Subscribe failed: %v", sr.err)
	}
	sub := sr.sub

	updMu.Lock()
	if len(updates) != 0 {
		updMu.Unlock()
		t.Fatalf("expected 0 updates before Activate, got %d", len(updates))
	}
	updMu.Unlock()

	// Update for an unknown subscription should be ignored cleanly without dropping connection.
	if err := server.Send(&protocol.ServiceEventEnvelope{
		Type:           "service_update",
		SubscriptionId: "closed-subscription",
		Update: jsonx.ObjectOf(
			"type", "state",
			"member", "state",
			"sequence", float64(99),
			"ops", []any{[]any{"s", float64(99), float64(99)}},
		),
	}); err != nil {
		t.Fatalf("send unknown subscription update failed: %v", err)
	}
	if !c.Connected() {
		t.Fatal("expected client to remain connected after unknown subscription update")
	}

	snap := sub.Snapshot()
	if len(snap.Instances) != 1 || len(snap.Instances[0].Members) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	m0, ok := snap.Instances[0].Members[0].(chord.ServiceMemberSnapshotState)
	if !ok || m0.Name != "state" || m0.Kind != "state" || m0.Sequence != 0 {
		t.Fatalf("unexpected snapshot member: %#v", snap.Instances[0].Members[0])
	}

	if err := sub.Activate(); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	pollUntil(t, func() bool {
		updMu.Lock()
		defer updMu.Unlock()
		return len(updates) == 1
	}, "first buffered state update")

	if err := server.Send(&protocol.ServiceEventEnvelope{
		Type:           "service_update",
		SubscriptionId: "service-1",
		Update: jsonx.ObjectOf(
			"type", "state",
			"member", "state",
			"sequence", float64(2),
			"ops", []any{[]any{"s", []any{"revision"}, float64(2)}},
		),
	}); err != nil {
		t.Fatalf("send update 2 failed: %v", err)
	}
	if err := server.Send(&protocol.ServiceEventEnvelope{
		Type:           "service_update",
		SubscriptionId: "service-1",
		Update: jsonx.ObjectOf(
			"type", "state",
			"member", "state",
			"sequence", float64(3),
			"ops", []any{
				[]any{"#", float64(0), []any{"revision"}},
				[]any{"s", float64(0), float64(3)},
			},
		),
	}); err != nil {
		t.Fatalf("send update 3 failed: %v", err)
	}

	pollUntil(t, func() bool {
		updMu.Lock()
		defer updMu.Unlock()
		return len(updates) == 3
	}, "all 3 updates")

	updMu.Lock()
	u3, ok := updates[2].(chord.ServiceProviderUpdateState)
	updMu.Unlock()
	if !ok || len(u3.Ops) != 1 {
		t.Fatalf("unexpected third update: %#v", updates[2])
	}
	opSet, ok := u3.Ops[0].(chord.OpSet)
	if !ok || !reflect.DeepEqual(opSet.Path, chord.NonEmptyPath{chord.SegString("revision")}) || opSet.Value != float64(3) {
		t.Fatalf("expected decoded op [s, [revision], 3], got %#v", u3.Ops[0])
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- sub.Close(chord.BackgroundContext)
	}()
	if err := server.WaitForMessages(ctx, 4); err != nil {
		t.Fatalf("WaitForMessages(4) failed: %v", err)
	}
	unsubReq, ok := server.Messages()[3].(*protocol.RequestEnvelope)
	if !ok {
		t.Fatalf("expected RequestEnvelope at 3, got %T", server.Messages()[3])
	}
	unsubCall, err := chord.ParseServiceCall(unsubReq.Call)
	if err != nil {
		t.Fatalf("ParseServiceCall failed: %v", err)
	}
	if unsubCall.ServiceId != "$chord.service" || unsubCall.Member != "unsubscribe" ||
		!reflect.DeepEqual(unsubCall.Args, []chord.JsonValue{"service-1"}) {
		t.Fatalf("unexpected unsubscribe call: %+v", unsubCall)
	}
	if err := server.Send(&protocol.ResponseSuccess{
		Type: "response",
		Id:   "request-3",
		Ok:   true,
	}); err != nil {
		t.Fatalf("send unsubscribe response failed: %v", err)
	}
	if err := <-closeErrCh; err != nil {
		t.Fatalf("sub.Close failed: %v", err)
	}
}

func TestCorrelatesOutOfOrderGenericServiceResponses(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	type resVal struct {
		val chord.JsonValue
		err error
	}
	ch1 := make(chan resVal, 1)
	ch2 := make(chan resVal, 1)
	go func() {
		v, _, err := c.Request(ctx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "first", Args: []chord.JsonValue{}})
		ch1 <- resVal{v, err}
	}()
	if err := server.WaitForMessages(ctx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	go func() {
		v, _, err := c.Request(ctx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "second", Args: []chord.JsonValue{}})
		ch2 <- resVal{v, err}
	}()

	if err := server.WaitForMessages(ctx, 3); err != nil {
		t.Fatalf("WaitForMessages(3) failed: %v", err)
	}
	if err := server.Send(&protocol.ResponseSuccess{Type: "response", Id: "request-2", Ok: true, Result: jsonx.Some[chord.JsonValue]("second")}); err != nil {
		t.Fatalf("send response 2 failed: %v", err)
	}
	if err := server.Send(&protocol.ResponseSuccess{Type: "response", Id: "request-1", Ok: true, Result: jsonx.Some[chord.JsonValue]("first")}); err != nil {
		t.Fatalf("send response 1 failed: %v", err)
	}
	r1, r2 := <-ch1, <-ch2
	if r1.err != nil || r2.err != nil || r1.val != "first" || r2.val != "second" {
		t.Fatalf("unexpected responses: r1=%+v r2=%+v", r1, r2)
	}
}

func TestExposesBoundedServerErrors(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.Request(ctx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "missing", Args: []chord.JsonValue{}})
		errCh <- err
	}()
	if err := server.WaitForMessages(ctx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	if err := server.Send(&protocol.ResponseFailure{
		Type:  "response",
		Id:    "request-1",
		Ok:    false,
		Error: protocol.ProtocolError{Code: "session_not_found", Message: "Unknown session"},
	}); err != nil {
		t.Fatalf("send error response failed: %v", err)
	}
	got := <-errCh
	var se *client.ServerError
	if !errors.As(got, &se) || se.Code != "session_not_found" {
		t.Fatalf("expected ServerError(session_not_found), got %T (%v)", got, got)
	}
}

func TestDoesNotSendPreAbortedUntypedRPCRequest(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	reason := errors.New("already cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(reason)

	_, _, err = c.Request(ctx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "noop", Args: []chord.JsonValue{}})
	if !errors.Is(err, reason) {
		t.Fatalf("expected %v, got %v", reason, err)
	}
	if got := len(server.Messages()); got != 1 {
		t.Fatalf("expected only hello message (1), got %d", got)
	}
}

func TestCancelsOneUntypedRPCRequestWithoutDisconnecting(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	reason := errors.New("stop this request")
	ctx, cancel := context.WithCancelCause(context.Background())
	waitCtx, waitCancel := context.WithTimeout(context.Background(), testTimeout)
	defer waitCancel()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.Request(ctx, testServerTarget, chord.ServiceCall{
			ServiceId: "test",
			Member:    "mutate",
			Args:      []chord.JsonValue{jsonx.ObjectOf("value", float64(42))},
		})
		errCh <- err
	}()

	if err := server.WaitForMessages(waitCtx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	req, ok := server.Messages()[1].(*protocol.RequestEnvelope)
	if !ok || req.Id != "request-1" || !reflect.DeepEqual(req.Target, testServerTarget) {
		t.Fatalf("unexpected request envelope: %#v", server.Messages()[1])
	}

	cancel(reason)
	if got := <-errCh; !errors.Is(got, reason) {
		t.Fatalf("expected cancellation reason %v, got %v", reason, got)
	}
	if err := server.WaitForMessages(waitCtx, 3); err != nil {
		t.Fatalf("WaitForMessages(3) failed: %v", err)
	}
	cancelEnv, ok := server.Messages()[2].(*protocol.CancelEnvelope)
	if !ok || cancelEnv.Id != "request-1" || !reflect.DeepEqual(cancelEnv.Target, testServerTarget) {
		t.Fatalf("unexpected cancel envelope: %#v", server.Messages()[2])
	}

	if err := server.Send(&protocol.ResponseFailure{
		Type:  "response",
		Id:    "request-1",
		Ok:    false,
		Error: protocol.ProtocolError{Code: "cancelled", Message: "cancelled"},
	}); err != nil {
		t.Fatalf("send cancelled response failed: %v", err)
	}
	if !c.Connected() {
		t.Fatal("expected client to remain connected after request cancellation")
	}
}

func TestRejectsPendingRequestsAfterDisconnectOrDisposal(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), testTimeout)
	defer waitCancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.Request(waitCtx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "pending", Args: []chord.JsonValue{}})
		errCh <- err
	}()
	if err := server.WaitForMessages(waitCtx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	server.Disconnect()
	var de *client.DisconnectedError
	if err := <-errCh; !errors.As(err, &de) {
		t.Fatalf("expected DisconnectedError, got %T (%v)", err, err)
	}

	if err := c.Dispose(); err != nil {
		t.Fatalf("Dispose failed: %v", err)
	}
	_, _, err = c.Request(waitCtx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "disposed", Args: []chord.JsonValue{}})
	var cde *client.ClientDisposedError
	if !errors.As(err, &cde) {
		t.Fatalf("expected ClientDisposedError, got %T (%v)", err, err)
	}
}

type funcByteTransport struct {
	sendFn  func([]byte) error
	closeFn func()
}

func (f *funcByteTransport) Send(chunk []byte) error {
	if f.sendFn != nil {
		return f.sendFn(chunk)
	}
	return nil
}

func (f *funcByteTransport) Close() {
	if f.closeFn != nil {
		f.closeFn()
	}
}

func TestRejectsServerDataDeliveredBeforeClientHelloIsSent(t *testing.T) {
	closeCount := 0
	sendCount := 0
	c, err := client.NewClient(client.ClientOptions{
		ServerId: testServerId,
		TransportFactory: func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
			frame, err := protocol.EncodeServerMessage(&protocol.ServerHello{
				Type:     "hello",
				Version:  protocol.ProtocolVersion,
				ServerId: testServerId,
			}, nil)
			if err != nil {
				return nil, err
			}
			handlers.OnData(frame)
			return &funcByteTransport{
				sendFn:  func([]byte) error { sendCount++; return nil },
				closeFn: func() { closeCount++ },
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	_, err = c.Connect()
	var pve *protocol.ProtocolValidationError
	if !errors.As(err, &pve) || pve.Message != "Received server data before the client hello was sent" {
		t.Fatalf("unexpected error: %T (%v)", err, err)
	}
	if c.ConnectionState() != client.ConnectionStateDisconnected || sendCount != 0 || closeCount != 1 {
		t.Fatalf("unexpected state=%s sendCount=%d closeCount=%d", c.ConnectionState(), sendCount, closeCount)
	}
}

func TestRejectsTypedHandshakeErrorsAndClosesTransport(t *testing.T) {
	var handlers client.ByteTransportHandlers
	closeCount := 0
	c, err := client.NewClient(client.ClientOptions{
		ServerId: testServerId,
		TransportFactory: func(created client.ByteTransportHandlers) (client.ByteTransport, error) {
			handlers = created
			return &funcByteTransport{
				sendFn: func([]byte) error {
					frame, err := protocol.EncodeServerMessage(&protocol.ServerHelloError{
						Type:  "hello_error",
						Error: protocol.ProtocolError{Code: "version", Message: "Unsupported protocol version"},
					}, nil)
					if err != nil {
						return err
					}
					handlers.OnData(frame)
					return nil
				},
				closeFn: func() { closeCount++ },
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	_, err = c.Connect()
	var se *client.ServerError
	if !errors.As(err, &se) || se.Code != "version" || se.Message != "Unsupported protocol version" {
		t.Fatalf("unexpected error: %T (%v)", err, err)
	}
	if c.ConnectionState() != client.ConnectionStateDisconnected || closeCount != 1 {
		t.Fatalf("unexpected state=%s closeCount=%d", c.ConnectionState(), closeCount)
	}
}

func TestRejectsPendingRequestsAndReconnectsThroughFreshTransport(t *testing.T) {
	first := testkit.NewMemoryByteServer()
	second := testkit.NewMemoryByteServer()
	connection := 0
	c, err := client.NewClient(client.ClientOptions{
		ServerId: testServerId,
		TransportFactory: func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
			srv := first
			if connection > 0 {
				srv = second
			}
			connection++
			return srv.Connect(handlers)
		},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	var states []client.ConnectionState
	_, err = c.OnConnectionStateChange(func(change client.ConnectionStateChange) {
		states = append(states, change.State)
	})
	if err != nil {
		t.Fatalf("OnConnectionStateChange failed: %v", err)
	}

	if _, err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	attachTestClient(t, c, first, "session-1")
	att := c.Attachment()
	if att == nil {
		t.Fatal("missing attachment")
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), testTimeout)
	defer waitCancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.Request(waitCtx, att, chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []chord.JsonValue{}})
		errCh <- err
	}()
	if err := first.WaitForMessages(waitCtx, 3); err != nil {
		t.Fatalf("WaitForMessages(3) failed: %v", err)
	}
	req, ok := first.Messages()[2].(*protocol.RequestEnvelope)
	if !ok {
		t.Fatalf("expected RequestEnvelope, got %T", first.Messages()[2])
	}
	call, err := chord.ParseServiceCall(req.Call)
	if err != nil || call.ServiceId != "test.session" || call.Member != "run" {
		t.Fatalf("unexpected call: %+v (%v)", call, err)
	}
	first.Disconnect()

	var de *client.DisconnectedError
	if err := <-errCh; !errors.As(err, &de) {
		t.Fatalf("expected DisconnectedError, got %T (%v)", err, err)
	}

	hello, err := c.Reconnect()
	if err != nil || hello.ServerId != testServerId {
		t.Fatalf("Reconnect failed: hello=%+v err=%v", hello, err)
	}
	if connection != 2 || !c.Connected() || len(second.Messages()) != 1 {
		t.Fatalf("unexpected state connection=%d connected=%v secondMsgs=%d", connection, c.Connected(), len(second.Messages()))
	}
	wantStates := []client.ConnectionState{
		client.ConnectionStateConnecting,
		client.ConnectionStateConnected,
		client.ConnectionStateDisconnected,
		client.ConnectionStateConnecting,
		client.ConnectionStateConnected,
	}
	if !reflect.DeepEqual(states, wantStates) {
		t.Fatalf("unexpected state transitions: got %v, want %v", states, wantStates)
	}
}

func TestReportsTransportFailuresWithoutLeavingRequestsPending(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), testTimeout)
	defer waitCancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.Request(waitCtx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "pending", Args: []chord.JsonValue{}})
		errCh <- err
	}()
	if err := server.WaitForMessages(waitCtx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	cause := errors.New("read failed")
	server.Error(cause)

	var de *client.DisconnectedError
	if err := <-errCh; !errors.As(err, &de) || de.Message != "read failed" || !errors.Is(de, cause) {
		t.Fatalf("expected DisconnectedError wrapping read failed, got %T (%v)", err, err)
	}
	if c.ConnectionState() != client.ConnectionStateDisconnected {
		t.Fatalf("expected disconnected, got %s", c.ConnectionState())
	}
}

func TestDisconnectsOnInvalidOrTruncatedServerFraming(t *testing.T) {
	invalidServer := testkit.NewMemoryByteServer()
	invalidClient, err := connectTestClient(invalidServer)
	if err != nil {
		t.Fatalf("connect invalidServer failed: %v", err)
	}
	defer func() { _ = invalidClient.Dispose() }()

	payload, err := cbor.EncodeCbor(jsonx.ObjectOf(
		"type", "response",
		"id", "unknown",
		"ok", true,
		"result", float64(1),
	), nil)
	if err != nil {
		t.Fatalf("EncodeCbor failed: %v", err)
	}
	frame, err := protocol.EncodeFrame(payload)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	if err := invalidServer.SendRaw(frame); err != nil {
		t.Fatalf("SendRaw failed: %v", err)
	}
	if invalidClient.ConnectionState() != client.ConnectionStateDisconnected {
		t.Fatalf("expected disconnected on invalid frame, got %s", invalidClient.ConnectionState())
	}

	truncatedServer := testkit.NewMemoryByteServer()
	truncatedClient, err := connectTestClient(truncatedServer)
	if err != nil {
		t.Fatalf("connect truncatedServer failed: %v", err)
	}
	defer func() { _ = truncatedClient.Dispose() }()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), testTimeout)
	defer waitCancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := truncatedClient.Request(waitCtx, testServerTarget, chord.ServiceCall{ServiceId: "test", Member: "pending", Args: []chord.JsonValue{}})
		errCh <- err
	}()
	if err := truncatedServer.WaitForMessages(waitCtx, 2); err != nil {
		t.Fatalf("WaitForMessages(2) failed: %v", err)
	}
	if err := truncatedServer.SendRaw([]byte{0, 0, 0, 2, 1}); err != nil {
		t.Fatalf("SendRaw truncated failed: %v", err)
	}
	truncatedServer.Disconnect()

	var pve *protocol.ProtocolValidationError
	gotErr := <-errCh
	if !errors.As(gotErr, &pve) || !regexp.MustCompile(`(?i)truncated`).MatchString(pve.Message) {
		t.Fatalf("expected ProtocolValidationError mentioning truncated, got %T (%v)", gotErr, gotErr)
	}
	if truncatedClient.ConnectionState() != client.ConnectionStateDisconnected {
		t.Fatalf("expected disconnected after truncated frame, got %s", truncatedClient.ConnectionState())
	}
}

func TestDisconnectsWhenResponseHasNoMatchingRequest(t *testing.T) {
	server := testkit.NewMemoryByteServer()
	c, err := connectTestClient(server)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer func() { _ = c.Dispose() }()

	if err := server.Send(&protocol.ResponseSuccess{
		Type:   "response",
		Id:     "unknown-request",
		Ok:     true,
		Result: jsonx.Some[chord.JsonValue]([]any{}),
	}); err != nil {
		t.Fatalf("send unmatched response failed: %v", err)
	}
	if c.ConnectionState() != client.ConnectionStateDisconnected {
		t.Fatalf("expected disconnected, got %s", c.ConnectionState())
	}
	if server.ClientCloseCount() != 1 {
		t.Fatalf("expected ClientCloseCount 1, got %d", server.ClientCloseCount())
	}
}
