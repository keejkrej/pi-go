// Ported from packages/server/test/conformance.test.ts (pi v1.0.0).

package server_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
)

const (
	ctServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")
	ctTimeout  = 5 * time.Second
)

type ctInMemoryWire struct {
	mu      sync.Mutex
	closed  bool
	handler server.ByteConnectionHandler
	client  *servertest.ProtocolTestClient
}

func (w *ctInMemoryWire) Closed() bool {
	w.mu.Lock()
	c := w.closed
	w.mu.Unlock()
	return c
}

func (w *ctInMemoryWire) Send(chunk []byte) error {
	w.client.Receive(chunk)
	return nil
}

func (w *ctInMemoryWire) Close(finalChunk []byte) error {
	if len(finalChunk) > 0 {
		w.client.Receive(finalChunk)
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.client.MarkClosed()
	return nil
}

type ctWireChannel struct {
	wire *ctInMemoryWire
}

func (c *ctWireChannel) Send(chunk []byte) error {
	if c.wire.handler.OnData != nil {
		c.wire.handler.OnData(chunk)
	}
	return nil
}

func (c *ctWireChannel) SendFragmented(chunk []byte, splitAt int) error {
	if c.wire.handler.OnData != nil {
		c.wire.handler.OnData(chunk[:splitAt])
		c.wire.handler.OnData(chunk[splitAt:])
	}
	return nil
}

func (c *ctWireChannel) Close() error {
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

func ctNewServer[M server.SessionMetadata](t *testing.T, host server.ServerHost[M], onError ...func(error)) *server.Server[M] {
	t.Helper()
	var errFn func(error)
	if len(onError) > 0 {
		errFn = onError[0]
	}
	srv, err := server.NewServer(host, server.ServerOptions{
		Listeners: []server.ServerListener{},
		ServerId:  ctServerId,
		OnError:   errFn,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func ctConnect[M server.SessionMetadata](srv *server.Server[M]) *servertest.ProtocolTestClient {
	wire := &ctInMemoryWire{}
	channel := &ctWireChannel{wire: wire}
	client := servertest.NewProtocolTestClient(channel)
	wire.client = client
	wire.handler = srv.Accept(wire)
	return client
}

func ctSessionCall(member string, args ...chord.JsonValue) chord.ServiceCall {
	if args == nil {
		args = []chord.JsonValue{}
	}
	return chord.ServiceCall{ServiceId: "test.session", Member: member, Args: args}
}

func ctLatestAttachmentId(t *testing.T, c *servertest.ProtocolTestClient, sessionId string) string {
	t.Helper()
	msgs := c.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if att, ok := msgs[i].(*protocol.AttachmentEnvelope); ok {
			if st, has := att.Attachment.Get(); has && st.SessionId == sessionId {
				return st.AttachmentId
			}
		}
	}
	t.Fatalf("Missing attachment for %s", sessionId)
	return ""
}

func ctPollUntil(t *testing.T, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(ctTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

type ctBackendMetadata struct {
	id         string
	path       string
	modifiedAt int
}

func (m ctBackendMetadata) SessionId() string { return m.id }

type ctCustomMetadataHost struct {
	serverServices server.RoutedServerServiceHost
	metadata       ctBackendMetadata
	onOpen         func(ctBackendMetadata)
}

func (h *ctCustomMetadataHost) ServerServices() server.RoutedServerServiceHost {
	return h.serverServices
}
func (h *ctCustomMetadataHost) ResolveSession(string, chord.Context) (ctBackendMetadata, error) {
	return h.metadata, nil
}
func (h *ctCustomMetadataHost) OpenSession(candidate ctBackendMetadata, _ chord.Context) (server.RoutedSessionHandle, error) {
	h.onOpen(candidate)
	return &ctNoopHandle{}, nil
}

type ctNoopHandle struct{}

func (h *ctNoopHandle) AttachClient(chord.Context) (server.RoutedSessionAttachment, error) {
	return &ctNoopAttachment{}, nil
}
func (h *ctNoopHandle) Terminated() <-chan error  { return nil }
func (h *ctNoopHandle) Close(chord.Context) error { return nil }

type ctNoopAttachment struct{}

func (a *ctNoopAttachment) InvokeService(chord.ServiceCall, chord.ServiceUpdatePublisher, chord.Context) (chord.JsonValue, bool, error) {
	return nil, false, nil
}
func (a *ctNoopAttachment) Release(chord.Context) error { return nil }

func TestSessionProtocolConformance(t *testing.T) {
	t.Run("handshake identifies the logical server without listing sessions", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed()
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		msg, err := c.Hello(ctx)
		if err != nil {
			t.Fatalf("Hello failed: %v", err)
		}
		sh, ok := msg.(*protocol.ServerHello)
		if !ok || sh.ServerId != ctServerId {
			t.Fatalf("unexpected hello: %#v", msg)
		}
		if host.HarnessCount() != 0 {
			t.Fatalf("expected 0 harnesses, got %d", host.HarnessCount())
		}
	})

	t.Run("rejects a semantically invalid service call after envelope decoding", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		c := ctConnect(ctNewServer(t, host))
		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()

		if _, err := c.Hello(ctx); err != nil {
			t.Fatalf("Hello failed: %v", err)
		}
		respCh := make(chan protocol.ServerMessage, 1)
		respErr := make(chan error, 1)
		go func() {
			m, e := c.Next(ctx, func(m protocol.ServerMessage) bool {
				switch r := m.(type) {
				case *protocol.ResponseSuccess:
					return r.Id == "invalid-call"
				case *protocol.ResponseFailure:
					return r.Id == "invalid-call"
				default:
					return false
				}
			})
			if e != nil {
				respErr <- e
				return
			}
			respCh <- m
		}()
		time.Sleep(5 * time.Millisecond)

		if err := c.SendMessage(&protocol.RequestEnvelope{
			Type:   "request",
			Id:     "invalid-call",
			Target: &protocol.ServerTarget{ServerId: ctServerId},
			Call:   jsonx.ObjectOf("arbitrary", true),
		}); err != nil {
			t.Fatalf("SendMessage failed: %v", err)
		}
		select {
		case e := <-respErr:
			t.Fatalf("Next failed: %v", e)
		case m := <-respCh:
			rf, ok := m.(*protocol.ResponseFailure)
			if !ok || rf.Error.Code != "invalid_request" {
				t.Fatalf("expected invalid_request failure, got %#v", m)
			}
		}
	})

	t.Run("attach passes concrete repository metadata to the Harness host", func(t *testing.T) {
		want := ctBackendMetadata{id: "session-1", path: "/sessions/session-1.jsonl", modifiedAt: 2}
		var got ctBackendMetadata
		host := &ctCustomMetadataHost{
			serverServices: servertest.CreateTestServerServices(),
			metadata:       want,
			onOpen:         func(candidate ctBackendMetadata) { got = candidate },
		}
		c := ctConnect(ctNewServer(t, host))
		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		if _, err := c.Hello(ctx); err != nil {
			t.Fatalf("Hello failed: %v", err)
		}
		resp, err := c.Attach(ctx, ctServerId, "session-1")
		if err != nil {
			t.Fatalf("Attach failed: %v", err)
		}
		if _, ok := resp.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess, got %#v", resp)
		}
		if got != want {
			t.Fatalf("expected metadata %+v, got %+v", want, got)
		}
	})

	t.Run("routes opaque server services and publishes attachment changes out of band", func(t *testing.T) {
		backing := servertest.NewTestServerHost()
		backing.Seed("session-1")
		var releaseCount atomic.Int64

		customServices := &ctCustomServerServices{
			onAttach: func(presentation server.RoutedServerPresentation) server.RoutedServerServiceAttachment {
				return &ctCustomServerServiceAttachment{
					presentation: presentation,
					onRelease:    func() { releaseCount.Add(1) },
				}
			},
		}
		host := &ctDelegateHost{
			backing:        backing,
			serverServices: customServices,
		}
		c := ctConnect(ctNewServer[server.SessionMetadata](t, host))
		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		if _, err := c.Hello(ctx); err != nil {
			t.Fatalf("Hello failed: %v", err)
		}

		attCh := make(chan protocol.ServerMessage, 1)
		attErr := make(chan error, 1)
		go func() {
			m, e := c.Next(ctx, func(m protocol.ServerMessage) bool {
				a, ok := m.(*protocol.AttachmentEnvelope)
				return ok && a.Attachment.IsSome()
			})
			if e != nil {
				attErr <- e
				return
			}
			attCh <- m
		}()
		time.Sleep(5 * time.Millisecond)

		attachResp, err := c.Attach(ctx, ctServerId, "session-1")
		if err != nil {
			t.Fatalf("Attach failed: %v", err)
		}
		if _, ok := attachResp.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess, got %#v", attachResp)
		}
		select {
		case e := <-attErr:
			t.Fatalf("Next(attachment) failed: %v", e)
		case m := <-attCh:
			ae := m.(*protocol.AttachmentEnvelope)
			st, ok := ae.Attachment.Get()
			if !ok || st.SessionId != "session-1" || st.AttachmentId == "" {
				t.Fatalf("unexpected attachment envelope: %+v", ae)
			}
		}
		harness, err := backing.LatestHarness("session-1")
		if err != nil || harness.AttachedClients() != 1 {
			t.Fatalf("expected 1 attached client, err=%v", err)
		}

		detCh := make(chan protocol.ServerMessage, 1)
		detErr := make(chan error, 1)
		go func() {
			m, e := c.Next(ctx, func(m protocol.ServerMessage) bool {
				a, ok := m.(*protocol.AttachmentEnvelope)
				return ok && a.Attachment.IsNull()
			})
			if e != nil {
				detErr <- e
				return
			}
			detCh <- m
		}()
		time.Sleep(5 * time.Millisecond)

		detResp, err := c.RequestService(ctx, &protocol.ServerTarget{ServerId: ctServerId}, chord.ServiceCall{
			ServiceId: "pi.session-management",
			Member:    "detach",
			Args:      []chord.JsonValue{},
		})
		if err != nil {
			t.Fatalf("detach RequestService failed: %v", err)
		}
		if _, ok := detResp.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess on detach, got %#v", detResp)
		}
		select {
		case e := <-detErr:
			t.Fatalf("Next(detach attachment) failed: %v", e)
		case <-detCh:
		}
		if harness.AttachedClients() != 0 {
			t.Fatalf("expected 0 attached clients, got %d", harness.AttachedClients())
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		ctPollUntil(t, func() bool { return releaseCount.Load() == 1 }, "server service releaseCount == 1")
	})

	t.Run("permits multiple client attachments per Session", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		srv := ctNewServer(t, host)
		first := ctConnect(srv)
		second := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		if _, err := first.Hello(ctx); err != nil {
			t.Fatalf("first.Hello failed: %v", err)
		}
		if _, err := second.Hello(ctx); err != nil {
			t.Fatalf("second.Hello failed: %v", err)
		}

		if r, err := first.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("first.Attach 1 failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok, got %#v", r)
		}
		if r, err := first.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("first.Attach 2 failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok, got %#v", r)
		}
		harness, err := host.LatestHarness("session-1")
		if err != nil || harness.AttachedClients() != 1 {
			t.Fatalf("expected 1 attached client, err=%v", err)
		}

		if r, err := second.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("second.Attach failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok, got %#v", r)
		}
		if len(host.Harnesses("session-1")) != 1 || harness.AttachedClients() != 2 {
			t.Fatalf("expected 1 harness and 2 attached clients, got %d harnesses, %d attached", len(host.Harnesses("session-1")), harness.AttachedClients())
		}

		if err := first.Close(); err != nil {
			t.Fatalf("first.Close failed: %v", err)
		}
		ctPollUntil(t, func() bool { return harness.AttachedClients() == 1 }, "attachedClients == 1")
		if r, err := second.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("second re-attach failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok, got %#v", r)
		}
	})

	t.Run("clears connection ownership when attachment release fails", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		var errMu sync.Mutex
		var errs []error
		srv := ctNewServer(t, host, func(err error) {
			errMu.Lock()
			errs = append(errs, err)
			errMu.Unlock()
		})
		first := ctConnect(srv)
		second := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = first.Hello(ctx)
		_, _ = second.Hello(ctx)
		_, _ = first.Attach(ctx, ctServerId, "session-1")

		harness, _ := host.LatestHarness("session-1")
		releaseErr := errors.New("release failed")
		harness.SetFailAttachmentRelease(releaseErr)

		_ = first.Close()
		ctPollUntil(t, func() bool { return harness.AttachmentReleaseCount() == 1 }, "attachmentReleaseCount == 1")
		ctPollUntil(t, func() bool {
			errMu.Lock()
			defer errMu.Unlock()
			return slices.Contains(errs, releaseErr)
		}, "releaseError logged")

		harness.SetFailAttachmentRelease(nil)
		if r, err := second.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("second.Attach failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok, got %#v", r)
		}
	})

	t.Run("requires the requesting client to hold the targeted Session attachment", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		host.Seed("session-2")
		srv := ctNewServer(t, host)
		attached := ctConnect(srv)
		unattached := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = attached.Hello(ctx)
		_, _ = unattached.Hello(ctx)

		if r, err := unattached.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run")); err != nil {
			t.Fatalf("unattached call failed: %v", err)
		} else if rf, ok := r.(*protocol.ResponseFailure); !ok || rf.Error.Code != "session_not_attached" {
			t.Fatalf("expected session_not_attached, got %#v", r)
		}

		_, _ = attached.Attach(ctx, ctServerId, "session-1")
		if r, err := attached.RequestSessionService(ctx, ctServerId, "session-2", ctSessionCall("run")); err != nil {
			t.Fatalf("wrong session call failed: %v", err)
		} else if rf, ok := r.(*protocol.ResponseFailure); !ok || rf.Error.Code != "session_not_attached" {
			t.Fatalf("expected session_not_attached, got %#v", r)
		}

		if r, err := attached.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run", "Hello")); err != nil {
			t.Fatalf("attached call failed: %v", err)
		} else if rs, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess, got %#v", r)
		} else if val, has := rs.Result.Get(); !has {
			t.Fatalf("expected result value, got %#v", rs.Result)
		} else if obj, isObj := val.(*jsonx.Object); !isObj {
			t.Fatalf("expected object result, got %#v", val)
		} else if okVal, _ := obj.Get("ok"); okVal != true {
			t.Fatalf("expected {ok: true}, got %#v", obj)
		}

		harness, _ := host.LatestHarness("session-1")
		wantCalls := []chord.ServiceCall{ctSessionCall("run", "Hello")}
		if !reflect.DeepEqual(harness.ServiceCalls(), wantCalls) {
			t.Fatalf("expected calls %+v, got %+v", wantCalls, harness.ServiceCalls())
		}
	})

	t.Run("rejects a stale attachment route after switching Sessions", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		host.Seed("session-2")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		firstAttachmentId := ctLatestAttachmentId(t, c, "session-1")
		_, _ = c.Attach(ctx, ctServerId, "session-2")

		resp, err := c.RequestService(ctx, &protocol.SessionTarget{
			ServerId:     ctServerId,
			SessionId:    "session-1",
			AttachmentId: firstAttachmentId,
		}, ctSessionCall("run", "stale"))
		if err != nil {
			t.Fatalf("RequestService failed: %v", err)
		}
		if rf, ok := resp.(*protocol.ResponseFailure); !ok || rf.Error.Code != "session_not_attached" {
			t.Fatalf("expected session_not_attached, got %#v", resp)
		}
		h1, _ := host.LatestHarness("session-1")
		if len(h1.ServiceCalls()) != 0 {
			t.Fatalf("expected 0 service calls on session-1, got %v", h1.ServiceCalls())
		}
	})

	t.Run("preserves opaque service results and bounds adapter defects", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		harness, _ := host.LatestHarness("session-1")

		harness.SetNextServiceResult(jsonx.ObjectOf("accepted", false, "reason", "closed"), true)
		resp, err := c.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run"))
		if err != nil {
			t.Fatalf("RequestSessionService failed: %v", err)
		}
		rs, ok := resp.(*protocol.ResponseSuccess)
		if !ok {
			t.Fatalf("expected ResponseSuccess, got %#v", resp)
		}
		resVal, has := rs.Result.Get()
		if !has {
			t.Fatalf("expected result value, got %#v", rs.Result)
		}
		obj := resVal.(*jsonx.Object)
		if acc, _ := obj.Get("accepted"); acc != false {
			t.Fatalf("expected accepted=false, got %#v", obj)
		}
		if reason, _ := obj.Get("reason"); reason != "closed" {
			t.Fatalf("expected reason=closed, got %#v", obj)
		}

		harness.SetNextServiceError(errors.New("private adapter detail"))
		resp2, err := c.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run"))
		if err != nil {
			t.Fatalf("RequestSessionService 2 failed: %v", err)
		}
		if rf, ok := resp2.(*protocol.ResponseFailure); !ok || rf.Error.Code != "internal_error" || rf.Error.Message != "Internal server error" {
			t.Fatalf("expected bounded internal_error, got %#v", resp2)
		}
	})

	t.Run("admits concurrent service calls to the attached Session", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		harness, _ := host.LatestHarness("session-1")

		gate := harness.GateNextServiceCall()
		type respPair struct {
			resp protocol.ResponseEnvelope
			err  error
		}
		firstCh := make(chan respPair, 1)
		go func() {
			r, e := c.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run", "first"))
			firstCh <- respPair{r, e}
		}()
		<-gate.Entered.Done()

		secondResp, secondErr := c.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run", "second"))
		if secondErr != nil {
			t.Fatalf("second service call failed: %v", secondErr)
		}
		if _, ok := secondResp.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess for second call, got %#v", secondResp)
		}
		wantCalls := []chord.ServiceCall{ctSessionCall("run", "first"), ctSessionCall("run", "second")}
		if !reflect.DeepEqual(harness.ServiceCalls(), wantCalls) {
			t.Fatalf("expected calls %+v, got %+v", wantCalls, harness.ServiceCalls())
		}
		gate.Release.Resolve(struct{}{})
		firstRes := <-firstCh
		if firstRes.err != nil {
			t.Fatalf("first service call failed: %v", firstRes.err)
		}
		if _, ok := firstRes.resp.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess for first call, got %#v", firstRes.resp)
		}
	})

	t.Run("keeps attachment demand until an accepted service call settles after disconnect", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		harness, _ := host.LatestHarness("session-1")

		gate := harness.GateNextServiceCall()
		errCh := make(chan error, 1)
		go func() {
			_, e := c.RequestSessionService(ctx, ctServerId, "session-1", ctSessionCall("run"))
			errCh <- e
		}()
		<-gate.Entered.Done()

		_ = c.Close()
		if harness.AttachedClients() != 1 {
			t.Fatalf("expected 1 attached client while service call active, got %d", harness.AttachedClients())
		}
		gate.Release.Resolve(struct{}{})
		if gotErr := <-errCh; gotErr == nil || !regexp.MustCompile(`(?i)closed`).MatchString(gotErr.Error()) {
			t.Fatalf("expected closed error, got %v", gotErr)
		}
		ctPollUntil(t, func() bool { return harness.AttachedClients() == 0 }, "attachedClients == 0")
	})

	t.Run("rejects requests addressed to another server before repository access", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		resp, err := c.Attach(ctx, "00000000-0000-4000-8000-000000000002", "session-1")
		if err != nil {
			t.Fatalf("Attach failed: %v", err)
		}
		if rf, ok := resp.(*protocol.ResponseFailure); !ok || rf.Error.Code != "wrong_server" {
			t.Fatalf("expected wrong_server, got %#v", resp)
		}
		if host.HarnessCount() != 0 {
			t.Fatalf("expected 0 harnesses, got %d", host.HarnessCount())
		}
	})

	t.Run("reports an unknown session without creating a Harness", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		resp, err := c.Attach(ctx, ctServerId, "missing")
		if err != nil {
			t.Fatalf("Attach failed: %v", err)
		}
		if rf, ok := resp.(*protocol.ResponseFailure); !ok || rf.Error.Code != "session_not_found" {
			t.Fatalf("expected session_not_found, got %#v", resp)
		}
		if host.HarnessCount() != 0 {
			t.Fatalf("expected 0 harnesses, got %d", host.HarnessCount())
		}
	})

	t.Run("rejects an ambiguous session ID without creating a Harness", func(t *testing.T) {
		host := &ctAmbiguousHost{serverServices: servertest.CreateTestServerServices()}
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		resp, err := c.Attach(ctx, ctServerId, "duplicate")
		if err != nil {
			t.Fatalf("Attach failed: %v", err)
		}
		if rf, ok := resp.(*protocol.ResponseFailure); !ok || rf.Error.Code != "session_ambiguous" {
			t.Fatalf("expected session_ambiguous, got %#v", resp)
		}
	})

	t.Run("invalidates a terminated Harness handle and allows a later attach", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		c := ctConnect(ctNewServer(t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		firstHarness, _ := host.LatestHarness("session-1")

		firstHarness.Terminate(errors.New("worker crashed"))
		ctPollUntil(t, func() bool { return firstHarness.AttachedClients() == 0 }, "firstHarness attachedClients == 0")
		if firstHarness.AttachmentReleaseCount() != 1 {
			t.Fatalf("expected attachmentReleaseCount == 1, got %d", firstHarness.AttachmentReleaseCount())
		}

		if r, err := c.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("re-attach failed: %v", err)
		} else if _, ok := r.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ok on re-attach, got %#v", r)
		}
		if len(host.Harnesses("session-1")) != 2 {
			t.Fatalf("expected 2 harnesses created, got %d", len(host.Harnesses("session-1")))
		}
	})

	t.Run("connection loss releases its attachment, while server shutdown closes the Harness", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		srv := ctNewServer(t, host)
		c := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)
		_, _ = c.Attach(ctx, ctServerId, "session-1")
		harness, _ := host.LatestHarness("session-1")

		_ = c.Close()
		ctPollUntil(t, func() bool { return harness.AttachedClients() == 0 }, "attachedClients == 0")
		if harness.CloseCount() != 0 {
			t.Fatalf("expected closeCount 0 before server close, got %d", harness.CloseCount())
		}
		if err := srv.Close(); err != nil {
			t.Fatalf("srv.Close failed: %v", err)
		}
		if harness.CloseCount() != 1 {
			t.Fatalf("expected closeCount 1 after server close, got %d", harness.CloseCount())
		}
	})
}

func TestRoutedSessionAcquisitionFailures(t *testing.T) {
	t.Run("releases a lease acquired concurrently with Harness termination", func(t *testing.T) {
		acquiring := servertest.NewDeferred[struct{}]()
		continueAcquiring := servertest.NewDeferred[struct{}]()
		terminated := make(chan error, 1)
		var releaseCount atomic.Int64

		host := &ctConcurrentTerminateHost{
			serverServices: servertest.CreateTestServerServices(),
			terminated:     terminated,
			onAttach: func() (server.RoutedSessionAttachment, error) {
				acquiring.Resolve(struct{}{})
				continueAcquiring.Await()
				return &ctCountingAttachment{
					onRelease: func() { releaseCount.Add(1) },
				}, nil
			},
		}
		c := ctConnect(ctNewServer[server.SessionMetadata](t, host))

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		type attachPair struct {
			resp protocol.ResponseEnvelope
			err  error
		}
		attCh := make(chan attachPair, 1)
		go func() {
			r, e := c.Attach(ctx, ctServerId, "session-1")
			attCh <- attachPair{r, e}
		}()
		<-acquiring.Done()

		terminated <- errors.New("worker crashed")
		time.Sleep(10 * time.Millisecond)
		continueAcquiring.Resolve(struct{}{})

		att := <-attCh
		if att.err != nil {
			t.Fatalf("Attach wire failed: %v", att.err)
		}
		if rf, ok := att.resp.(*protocol.ResponseFailure); !ok || rf.Error.Code != "server_draining" {
			t.Fatalf("expected server_draining, got %#v", att.resp)
		}
		if releaseCount.Load() != 1 {
			t.Fatalf("expected releaseCount 1, got %d", releaseCount.Load())
		}
	})

	t.Run("shares a Harness creation failure, releases the Session, and allows a later retry", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		host.SetNextOpenSessionError(errors.New("Harness creation failed"))
		srv := ctNewServer(t, host)
		first := ctConnect(srv)
		second := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = first.Hello(ctx)
		_, _ = second.Hello(ctx)

		gate := host.GateNextOpenSession()
		type respErr struct {
			resp protocol.ResponseEnvelope
			err  error
		}
		ch1 := make(chan respErr, 1)
		ch2 := make(chan respErr, 1)
		go func() {
			r, e := first.Attach(ctx, ctServerId, "session-1")
			ch1 <- respErr{r, e}
		}()
		<-gate.Entered.Done()
		go func() {
			r, e := second.Attach(ctx, ctServerId, "session-1")
			ch2 <- respErr{r, e}
		}()
		// Ensure second request reaches SessionRouter before opening gate.
		time.Sleep(15 * time.Millisecond)
		gate.Release.Resolve(struct{}{})

		r1, r2 := <-ch1, <-ch2
		if r1.err != nil || r2.err != nil {
			t.Fatalf("wire failures r1=%v r2=%v", r1.err, r2.err)
		}
		rf1, ok1 := r1.resp.(*protocol.ResponseFailure)
		rf2, ok2 := r2.resp.(*protocol.ResponseFailure)
		if !ok1 || !ok2 || rf1.Error.Code != "internal_error" || rf2.Error.Code != "internal_error" {
			t.Fatalf("expected internal_error on both, got %#v / %#v", r1.resp, r2.resp)
		}
		if host.OpenSessionCount() != 1 {
			t.Fatalf("expected openSessionCount 1, got %d", host.OpenSessionCount())
		}

		if r3, err := first.Attach(ctx, ctServerId, "session-1"); err != nil {
			t.Fatalf("retry attach failed: %v", err)
		} else if _, ok := r3.(*protocol.ResponseSuccess); !ok {
			t.Fatalf("expected ResponseSuccess on retry, got %#v", r3)
		}
		if host.OpenSessionCount() != 2 || len(host.Harnesses("session-1")) != 1 {
			t.Fatalf("expected openSessionCount=2 and 1 harness, got count=%d harnesses=%d", host.OpenSessionCount(), len(host.Harnesses("session-1")))
		}
	})

	t.Run("closes a Harness acquired while server shutdown is in progress", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		srv := ctNewServer(t, host)
		c := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		gate := host.GateNextOpenSession()
		attErr := make(chan error, 1)
		go func() {
			_, e := c.Attach(ctx, ctServerId, "session-1")
			attErr <- e
		}()
		<-gate.Entered.Done()

		closeDone := make(chan error, 1)
		go func() {
			closeDone <- srv.Close()
		}()
		time.Sleep(10 * time.Millisecond)
		gate.Release.Resolve(struct{}{})

		if err := <-closeDone; err != nil {
			t.Fatalf("srv.Close failed: %v", err)
		}
		if err := <-attErr; err == nil || !regexp.MustCompile(`(?i)closed`).MatchString(err.Error()) {
			t.Fatalf("expected closed error on attach, got %v", err)
		}
		h, err := host.LatestHarness("session-1")
		if err != nil || h.CloseCount() != 1 {
			t.Fatalf("expected closeCount 1, err=%v", err)
		}
	})

	t.Run("fails shutdown when an in-flight acquisition cannot release its Harness", func(t *testing.T) {
		host := servertest.NewTestServerHost()
		host.Seed("session-1")
		host.SetNextHarnessCloseError(errors.New("close failed"))
		srv := ctNewServer(t, host)
		c := ctConnect(srv)

		ctx, cancel := context.WithTimeout(context.Background(), ctTimeout)
		defer cancel()
		_, _ = c.Hello(ctx)

		gate := host.GateNextOpenSession()
		attErr := make(chan error, 1)
		go func() {
			_, e := c.Attach(ctx, ctServerId, "session-1")
			attErr <- e
		}()
		<-gate.Entered.Done()

		closeDone := make(chan error, 1)
		go func() {
			closeDone <- srv.Close()
		}()
		time.Sleep(10 * time.Millisecond)
		gate.Release.Resolve(struct{}{})

		closeErr := <-closeDone
		if closeErr == nil || !strings.Contains(closeErr.Error(), "Failed to close routed Sessions") {
			t.Fatalf("expected 'Failed to close routed Sessions' on Close(), got %v", closeErr)
		}
		closedErr := srv.Closed()
		if closedErr == nil || !strings.Contains(closedErr.Error(), "Failed to close routed Sessions") {
			t.Fatalf("expected 'Failed to close routed Sessions' on Closed(), got %v", closedErr)
		}
		if err := <-attErr; err == nil || !regexp.MustCompile(`(?i)closed`).MatchString(err.Error()) {
			t.Fatalf("expected closed error on attach, got %v", err)
		}
		h, _ := host.LatestHarness("session-1")
		if h.CloseCount() != 1 {
			t.Fatalf("expected closeCount 1, got %d", h.CloseCount())
		}
		_ = h.Close(chord.BackgroundContext)
	})
}

type ctCustomServerServices struct {
	onAttach func(server.RoutedServerPresentation) server.RoutedServerServiceAttachment
}

func (s *ctCustomServerServices) AttachClient(presentation server.RoutedServerPresentation, _ chord.Context) (server.RoutedServerServiceAttachment, error) {
	return s.onAttach(presentation), nil
}

type ctCustomServerServiceAttachment struct {
	presentation server.RoutedServerPresentation
	onRelease    func()
}

func (a *ctCustomServerServiceAttachment) InvokeService(call chord.ServiceCall, _ chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error) {
	if call.ServiceId != "pi.session-management" {
		return nil, false, errors.New("Unexpected service")
	}
	if call.Member == "attach" && len(call.Args) > 0 {
		if sid, ok := call.Args[0].(string); ok {
			if err := a.presentation.AttachSession(sid, callContext); err != nil {
				return nil, false, err
			}
			return nil, true, nil
		}
	}
	if call.Member == "detach" {
		if err := a.presentation.DetachSession(callContext); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	return nil, false, errors.New("Unexpected service member")
}

func (a *ctCustomServerServiceAttachment) Release(chord.Context) error {
	a.onRelease()
	return nil
}

type ctDelegateHost struct {
	backing        *servertest.TestServerHost
	serverServices server.RoutedServerServiceHost
}

func (h *ctDelegateHost) ServerServices() server.RoutedServerServiceHost { return h.serverServices }
func (h *ctDelegateHost) ResolveSession(sessionId string, callContext chord.Context) (server.SessionMetadata, error) {
	return h.backing.ResolveSession(sessionId, callContext)
}
func (h *ctDelegateHost) OpenSession(metadata server.SessionMetadata, callContext chord.Context) (server.RoutedSessionHandle, error) {
	return h.backing.OpenSession(metadata, callContext)
}

type ctAmbiguousHost struct {
	serverServices server.RoutedServerServiceHost
}

func (h *ctAmbiguousHost) ServerServices() server.RoutedServerServiceHost { return h.serverServices }
func (h *ctAmbiguousHost) ResolveSession(string, chord.Context) (ctBackendMetadata, error) {
	return ctBackendMetadata{}, server.NewSessionAmbiguousError()
}
func (h *ctAmbiguousHost) OpenSession(ctBackendMetadata, chord.Context) (server.RoutedSessionHandle, error) {
	return nil, errors.New("must not create a Harness for an ambiguous session")
}

type ctConcurrentTerminateHost struct {
	serverServices server.RoutedServerServiceHost
	terminated     <-chan error
	onAttach       func() (server.RoutedSessionAttachment, error)
}

func (h *ctConcurrentTerminateHost) ServerServices() server.RoutedServerServiceHost {
	return h.serverServices
}
func (h *ctConcurrentTerminateHost) ResolveSession(string, chord.Context) (server.SessionMetadata, error) {
	return server.BasicSessionMetadata{Id: "session-1"}, nil
}
func (h *ctConcurrentTerminateHost) OpenSession(server.SessionMetadata, chord.Context) (server.RoutedSessionHandle, error) {
	return &ctConcurrentTerminateHandle{terminated: h.terminated, onAttach: h.onAttach}, nil
}

type ctConcurrentTerminateHandle struct {
	terminated <-chan error
	onAttach   func() (server.RoutedSessionAttachment, error)
}

func (h *ctConcurrentTerminateHandle) AttachClient(chord.Context) (server.RoutedSessionAttachment, error) {
	return h.onAttach()
}
func (h *ctConcurrentTerminateHandle) Terminated() <-chan error  { return h.terminated }
func (h *ctConcurrentTerminateHandle) Close(chord.Context) error { return nil }

type ctCountingAttachment struct {
	onRelease func()
}

func (a *ctCountingAttachment) InvokeService(chord.ServiceCall, chord.ServiceUpdatePublisher, chord.Context) (chord.JsonValue, bool, error) {
	return nil, false, nil
}
func (a *ctCountingAttachment) Release(chord.Context) error {
	a.onRelease()
	return nil
}
