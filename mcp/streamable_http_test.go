// Ported from packages/mcp/test/streamable-http.test.ts (pi v1.5.2).

package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
	"github.com/keejkrej/pi-go/mcp/internal/testkit"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

type tshtRecordedRequest struct {
	method  string
	headers http.Header
	message *jsonx.Object
}

type tshtServerState struct {
	mu       sync.Mutex
	requests []tshtRecordedRequest
}

func (s *tshtServerState) snapshot() []tshtRecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func tshtStartServer(
	t *testing.T,
	handler func(w http.ResponseWriter, r *http.Request, s *tshtServerState),
) (string, *tshtServerState) {
	t.Helper()
	state := &tshtServerState{}
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		handler(w, r, state)
	})
	return origin + "/mcp", state
}

func tshtReadMessage(r *http.Request) (*jsonx.Object, error) {
	body, err := testkit.ReadBody(r)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.ParseString(body)
	if err != nil {
		return nil, err
	}
	obj, _ := v.(*jsonx.Object)
	return obj, nil
}

func tshtProtocolHandler(w http.ResponseWriter, r *http.Request, s *tshtServerState, body *jsonx.Object) {
	if r.Method == http.MethodGet {
		s.mu.Lock()
		s.requests = append(s.requests, tshtRecordedRequest{method: http.MethodGet, headers: r.Header.Clone()})
		s.mu.Unlock()
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodDelete {
		s.mu.Lock()
		s.requests = append(s.requests, tshtRecordedRequest{method: http.MethodDelete, headers: r.Header.Clone()})
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	msg := body
	if msg == nil {
		var err error
		msg, err = tshtReadMessage(r)
		if err != nil || msg == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	s.requests = append(s.requests, tshtRecordedRequest{method: r.Method, headers: r.Header.Clone(), message: msg})
	s.mu.Unlock()

	idVal, hasId := msg.Get("id")
	if !hasId {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	methodVal, _ := msg.Get("method")
	method, _ := methodVal.(string)
	switch method {
	case "initialize":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-1")
		w.WriteHeader(http.StatusOK)
		raw, _ := jsonx.Marshal(jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"protocolVersion", mcpprotocol.LatestProtocolVersion,
				"capabilities", jsonx.ObjectOf("tools", jsonx.ObjectOf()),
				"serverInfo", jsonx.ObjectOf("name", "http-fixture", "version", "1.0.0"),
			),
		))
		_, _ = w.Write(raw)
		return
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		raw, _ := jsonx.Marshal(jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"tools", []any{jsonx.ObjectOf("name", "echo", "inputSchema", jsonx.ObjectOf("type", "object"))},
			),
		))
		_, _ = w.Write(raw)
		return
	default:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id: tool-result\n"))
		raw, _ := jsonx.Marshal(jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"content", []any{jsonx.ObjectOf("type", "text", "text", "hello")},
			),
		))
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}
}

type tshtChunkReader struct {
	chunks    [][]byte
	idx       int
	cancelled atomic.Bool
}

func (r *tshtChunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	c := r.chunks[r.idx]
	r.idx++
	n := copy(p, c)
	if n < len(c) {
		r.chunks[r.idx-1] = c[n:]
		r.idx--
	}
	return n, nil
}

func (r *tshtChunkReader) Close() error {
	r.cancelled.Store(true)
	return nil
}

type tshtEndlessReader struct {
	chunk     []byte
	reads     atomic.Int64
	maxReads  int64
	cancelled atomic.Bool
}

func (r *tshtEndlessReader) Read(p []byte) (int, error) {
	n := r.reads.Add(1)
	if r.maxReads > 0 && n > r.maxReads {
		return 0, io.EOF
	}
	copied := copy(p, r.chunk)
	return copied, nil
}

func (r *tshtEndlessReader) Close() error {
	r.cancelled.Store(true)
	return nil
}

type tshtAuthFunc struct {
	tokenFn        func() (*string, error)
	unauthorizedFn func(*mcp.UnauthorizedContext) error
}

func (a *tshtAuthFunc) Token() (*string, error) {
	return a.tokenFn()
}

func (a *tshtAuthFunc) OnUnauthorized(ctx *mcp.UnauthorizedContext) error {
	if a.unauthorizedFn != nil {
		return a.unauthorizedFn(ctx)
	}
	return nil
}

func TestStreamableHttp_ConsumeSse_ChunkedCrlfCommentsIdsMultiline(t *testing.T) {
	r := &tshtChunkReader{
		chunks: [][]byte{
			[]byte(": keepalive\r\nid: 7\r\ndata: {\"one\":\r\n"),
			[]byte("data: 1}\r\n\r\n"),
		},
	}
	var events []mcp.SseEvent
	if err := mcp.ConsumeSseStream(r, &mcp.ConsumeSseOptions{
		OnEvent: func(ev mcp.SseEvent) { events = append(events, ev) },
	}); err != nil {
		t.Fatal(err)
	}
	id7 := "7"
	want := []mcp.SseEvent{{Id: &id7, Data: "{\"one\":\n1}"}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestStreamableHttp_ConsumeSse_LineEndingsCrLfCrlfAndSingleByteChunks(t *testing.T) {
	parse := func(chunks [][]byte) []mcp.SseEvent {
		var events []mcp.SseEvent
		if err := mcp.ConsumeSseStream(&tshtChunkReader{chunks: chunks}, &mcp.ConsumeSseOptions{
			OnEvent: func(ev mcp.SseEvent) { events = append(events, ev) },
		}); err != nil {
			t.Fatal(err)
		}
		return events
	}
	id1 := "1"
	want := []mcp.SseEvent{{Id: &id1, Data: "a\nb"}, {Data: "c"}}
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		text := strings.Join([]string{"id: 1", "data: a", "data: b", "", "data: c", "", ""}, sep)
		if got := parse([][]byte{[]byte(text)}); !reflect.DeepEqual(got, want) {
			t.Fatalf("sep=%q whole: got %#v, want %#v", sep, got, want)
		}
		single := make([][]byte, 0, len(text))
		for i := 0; i < len(text); i++ {
			single = append(single, []byte{text[i]})
		}
		if got := parse(single); !reflect.DeepEqual(got, want) {
			t.Fatalf("sep=%q byte-by-byte: got %#v, want %#v", sep, got, want)
		}
	}
	mixed := parse([][]byte{[]byte("id: 1\rdata: a\r\ndata: b\n\r"), []byte("\ndata: c\r\r")})
	if !reflect.DeepEqual(mixed, want) {
		t.Fatalf("mixed: got %#v, want %#v", mixed, want)
	}
}

func TestStreamableHttp_ConsumeSse_DispatchesCrTerminatedEventsWhileOpen(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()

	received := make(chan mcp.SseEvent, 1)
	done := make(chan error, 1)
	go func() {
		done <- mcp.ConsumeSseStream(pr, &mcp.ConsumeSseOptions{
			OnEvent: func(ev mcp.SseEvent) { received <- ev },
		})
	}()

	if _, err := pw.Write([]byte("data: first\r\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-received:
		if ev.Data != "first" {
			t.Fatalf("ev = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CR-terminated event while stream stays open")
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStreamableHttp_ConsumeSse_RejectsOversizedEventWithoutBlankLine(t *testing.T) {
	r := &tshtEndlessReader{chunk: []byte("data: xxxxxxxxxxxxxxxx\n"), maxReads: 1000}
	limit := 256
	err := mcp.ConsumeSseStream(r, &mcp.ConsumeSseOptions{
		MaxEventBytes: &limit,
		OnEvent:       func(mcp.SseEvent) {},
	})
	if err == nil || !strings.Contains(err.Error(), "MCP SSE event exceeds 256 bytes") {
		t.Fatalf("err = %v, want MCP SSE event exceeds 256 bytes", err)
	}
	if r.reads.Load() >= 100 {
		t.Fatalf("reads = %d, want < 100", r.reads.Load())
	}
}

func TestStreamableHttp_ConsumeSse_CancelsStreamOnOversizedEvent(t *testing.T) {
	tracker := &tshtChunkReader{
		chunks: [][]byte{[]byte("data: " + strings.Repeat("x", 512) + "\n\n")},
	}
	limit := 128
	err := mcp.ConsumeSseStream(tracker, &mcp.ConsumeSseOptions{
		MaxEventBytes: &limit,
		OnEvent:       func(mcp.SseEvent) {},
	})
	if err == nil || !strings.Contains(err.Error(), "MCP SSE event exceeds 128 bytes") {
		t.Fatalf("err = %v", err)
	}
	if !tracker.cancelled.Load() {
		t.Fatal("expected failed SSE stream to be closed/cancelled")
	}
}

func TestStreamableHttp_HandlesJsonAndSseWithSessionAndProtocolHeaders(t *testing.T) {
	rawURL, state := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		tshtProtocolHandler(w, r, s, nil)
	})
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	if sid := tr.SessionId(); sid == nil || *sid != "session-1" {
		t.Fatalf("sessionId = %v, want session-1", sid)
	}
	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	res, err := client.CallTool(t.Context(), "echo", jsonx.ObjectOf("text", "hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tshtFirstText(t, res); got != "hello" {
		t.Fatalf("callTool text = %q", got)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	reqs := state.snapshot()
	var sawList, sawGet, sawDelete bool
	for _, entry := range reqs {
		if entry.method == http.MethodGet {
			sawGet = true
		}
		if entry.method == http.MethodDelete {
			sawDelete = true
		}
		if entry.message != nil {
			if m, _ := entry.message.Get("method"); m == "tools/list" {
				sawList = true
				if got := entry.headers.Get("mcp-session-id"); got != "session-1" {
					t.Fatalf("mcp-session-id = %q", got)
				}
				if got := entry.headers.Get("mcp-protocol-version"); got != mcpprotocol.LatestProtocolVersion {
					t.Fatalf("mcp-protocol-version = %q", got)
				}
			}
		}
	}
	if !sawList || !sawGet || !sawDelete {
		t.Fatalf("sawList=%v sawGet=%v sawDelete=%v", sawList, sawGet, sawDelete)
	}
}

func TestStreamableHttp_ClassifiesAuthenticationFailures(t *testing.T) {
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, _ *tshtServerState) {
		_, _ = testkit.ReadBody(r)
		w.Header().Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/meta"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("login required"))
	})
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	_, err = client.Connect(tr)
	var authErr *mcp.McpAuthRequiredError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected McpAuthRequiredError, got %v", err)
	}
	if authErr.Name() != "McpAuthRequiredError" || authErr.Status != 401 || authErr.Body != "login required" {
		t.Fatalf("authErr = %#v", authErr)
	}
	if authErr.WwwAuthenticate == nil || *authErr.WwwAuthenticate != `Bearer resource_metadata="https://example.com/meta"` {
		t.Fatalf("wwwAuthenticate = %v", authErr.WwwAuthenticate)
	}
}

func TestStreamableHttp_FailsOnlyRequestWhoseSseStreamBreaks(t *testing.T) {
	slowGate := make(chan struct{})
	var releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(slowGate) }) }
	defer releaseSlow()

	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, err := tshtReadMessage(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var name string
		if p, ok := msg.Get("params"); ok {
			if po, ok := p.(*jsonx.Object); ok {
				n, _ := po.Get("name")
				name, _ = n.(string)
			}
		}
		if name == "broken" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: not json\n\n"))
			return
		}
		if name == "slow" {
			<-slowGate
		}
		tshtProtocolHandler(w, r, s, msg)
	})

	openGet := false
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL, OpenGetStream: &openGet})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	var errMu sync.Mutex
	var errs []error
	client.OnError(func(e error) {
		errMu.Lock()
		errs = append(errs, e)
		errMu.Unlock()
	})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	slowCh := make(chan struct {
		res *mcpprotocol.CallToolResult
		err error
	}, 1)
	go func() {
		res, callErr := client.CallTool(t.Context(), "slow", nil, nil)
		slowCh <- struct {
			res *mcpprotocol.CallToolResult
			err error
		}{res, callErr}
	}()

	_, brokenErr := client.CallTool(t.Context(), "broken", nil, nil)
	if brokenErr == nil || !strings.Contains(brokenErr.Error(), "MCP response stream failed") {
		t.Fatalf("brokenErr = %v", brokenErr)
	}
	releaseSlow()
	slowOut := <-slowCh
	if slowOut.err != nil {
		t.Fatalf("slow error = %v", slowOut.err)
	}
	if got := tshtFirstText(t, slowOut.res); got != "hello" {
		t.Fatalf("slow text = %q", got)
	}
	errMu.Lock()
	nErrs := len(errs)
	errMu.Unlock()
	if nErrs != 1 {
		t.Fatalf("errs = %d, want 1", nErrs)
	}
}

func TestStreamableHttp_OpensGetStreamAfterInitializeAndSendsLastEventIdOnlyOnResume(t *testing.T) {
	var orderMu sync.Mutex
	var order []string
	rawURL, state := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method == http.MethodPost {
			msg, err := tshtReadMessage(r)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mVal, _ := msg.Get("method")
			orderMu.Lock()
			order = append(order, mVal.(string))
			orderMu.Unlock()
			tshtProtocolHandler(w, r, s, msg)
			return
		}
		orderMu.Lock()
		order = append(order, r.Method)
		orderMu.Unlock()
		tshtProtocolHandler(w, r, s, nil)
	})
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(t.Context(), "echo", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	orderMu.Lock()
	getIdx := slices.Index(order, http.MethodGet)
	initIdx := slices.Index(order, "notifications/initialized")
	orderMu.Unlock()
	if initIdx < 0 || getIdx <= initIdx {
		t.Fatalf("expected GET after notifications/initialized, order=%v", order)
	}
	for _, entry := range state.snapshot() {
		if entry.headers.Get("Last-Event-Id") != "" {
			t.Fatalf("unexpected Last-Event-ID header: %q", entry.headers.Get("Last-Event-Id"))
		}
	}
}

func TestStreamableHttp_ResumesResponseStreamClosedBeforeAnswer(t *testing.T) {
	var mu sync.Mutex
	var resumeHeaders []string
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method == http.MethodGet && r.Header.Get("Last-Event-Id") != "" {
			mu.Lock()
			resumeHeaders = append(resumeHeaders, r.Header.Get("Last-Event-Id"))
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			payload, _ := jsonx.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      2,
				"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "resumed"}}},
			})
			_, _ = w.Write([]byte("id: 2\ndata: " + string(payload) + "\n\n"))
			return
		}
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, _ := tshtReadMessage(r)
		if m, _ := msg.Get("method"); m != "tools/call" {
			tshtProtocolHandler(w, r, s, msg)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id: 1\nretry: 5\ndata:\n\n"))
	})

	openGet := false
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL, OpenGetStream: &openGet})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	var errMu sync.Mutex
	var errs []error
	client.OnError(func(e error) {
		errMu.Lock()
		errs = append(errs, e)
		errMu.Unlock()
	})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	res, err := client.CallTool(t.Context(), "echo", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tshtFirstText(t, res); got != "resumed" {
		t.Fatalf("resumed text = %q", got)
	}
	mu.Lock()
	gotResume := slices.Clone(resumeHeaders)
	mu.Unlock()
	if !reflect.DeepEqual(gotResume, []string{"1"}) {
		t.Fatalf("resumeHeaders = %#v", gotResume)
	}
	errMu.Lock()
	nErrs := len(errs)
	errMu.Unlock()
	if nErrs != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	_ = client.Close()
}

func TestStreamableHttp_FailsRequestWhoseResponseStreamEndsWithoutAnswer(t *testing.T) {
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, _ := tshtReadMessage(r)
		if m, _ := msg.Get("method"); m != "tools/call" {
			tshtProtocolHandler(w, r, s, msg)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(": nothing here\n\n"))
	})
	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL, OpenGetStream: &openGet})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	to := int64(5_000)
	_, err := client.CallTool(t.Context(), "echo", jsonx.NewObject(), &mcp.McpRequestOptions{TimeoutMs: &to})
	if err == nil || !strings.Contains(err.Error(), "MCP response stream failed: stream ended without a response") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttp_ReconnectsGetStreamAfterDrop(t *testing.T) {
	var mu sync.Mutex
	gets := 0
	var lastEventIds []string
	secondClosed := make(chan struct{})
	defer close(secondClosed)

	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodGet {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		mu.Lock()
		gets++
		n := gets
		lastEventIds = append(lastEventIds, r.Header.Get("Last-Event-Id"))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := jsonx.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
		if n == 1 {
			_, _ = w.Write([]byte("id: g1\ndata: " + string(notif) + "\n\n"))
			return
		}
		_, _ = w.Write([]byte("id: g2\ndata: " + string(notif) + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-secondClosed:
		}
	})
	initDelay := int64(1)
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:       rawURL,
		Reconnect: &mcp.StreamableHttpReconnectOptions{InitialDelayMs: &initDelay},
	})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	var changes atomic.Int32
	secondChange := make(chan struct{})
	client.OnNotification("notifications/tools/list_changed", func(any) {
		if changes.Add(1) == 2 {
			close(secondChange)
		}
	})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondChange:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second list_changed notification")
	}
	_ = client.Close()

	mu.Lock()
	gotIds := slices.Clone(lastEventIds)
	mu.Unlock()
	if !reflect.DeepEqual(gotIds, []string{"", "g1"}) {
		t.Fatalf("lastEventIds = %#v", gotIds)
	}
}

func TestStreamableHttp_RejectsRequestAcceptedWithoutResponse(t *testing.T) {
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, _ := tshtReadMessage(r)
		if m, _ := msg.Get("method"); m != "tools/call" {
			tshtProtocolHandler(w, r, s, msg)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL, OpenGetStream: &openGet})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_, err := client.CallTool(t.Context(), "echo", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "without a response") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttp_AppliesMaxMessageBytesToJsonResponses(t *testing.T) {
	send := func(descBytes int) ([]mcpprotocol.JsonRpcMessage, error) {
		body, _ := jsonx.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"tools": []any{map[string]any{"name": "echo", "description": strings.Repeat("x", descBytes), "inputSchema": map[string]any{}}},
			},
		})
		openGet := false
		maxBytes := 512
		tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
			Url:             "http://127.0.0.1/mcp",
			MaxMessageBytes: &maxBytes,
			OpenGetStream:   &openGet,
			Fetch: func(context.Context, string, *mcp.McpRequestInit) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(body)),
				}, nil
			},
		})
		if err != nil {
			return nil, err
		}
		var msgs []mcpprotocol.JsonRpcMessage
		tr.OnMessage(func(m mcpprotocol.JsonRpcMessage) { msgs = append(msgs, m) })
		if err := tr.Start(); err != nil {
			return nil, err
		}
		defer func() { _ = tr.Close() }()
		if err := tr.Send(&mcpprotocol.JsonRpcRequest{Jsonrpc: "2.0", Id: mcpprotocol.JsonRpcId{Number: 1}, Method: "tools/list"}); err != nil {
			return nil, err
		}
		return msgs, nil
	}

	msgs, err := send(32)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("send(32): msgs=%d err=%v", len(msgs), err)
	}
	_, err = send(2048)
	if err == nil || !strings.Contains(err.Error(), "MCP JSON response exceeds 512 bytes") {
		t.Fatalf("send(2048): err=%v", err)
	}
}

func TestStreamableHttp_DecodesJsonResponsesSplitInsideBomAndMultiByte(t *testing.T) {
	rawJSON, _ := jsonx.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  map[string]any{"text": "é€"},
	})
	payload := append([]byte("\uFEFF"), rawJSON...)
	chunks := make([][]byte, 0, len(payload))
	for _, b := range payload {
		chunks = append(chunks, []byte{b})
	}
	openGet := false
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           "http://127.0.0.1/mcp",
		OpenGetStream: &openGet,
		Fetch: func(context.Context, string, *mcp.McpRequestInit) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       &tshtChunkReader{chunks: chunks},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []mcpprotocol.JsonRpcMessage
	tr.OnMessage(func(m mcpprotocol.JsonRpcMessage) { msgs = append(msgs, m) })
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Send(&mcpprotocol.JsonRpcRequest{Jsonrpc: "2.0", Id: mcpprotocol.JsonRpcId{Number: 1}, Method: "tools/list"}); err != nil {
		t.Fatal(err)
	}
	_ = tr.Close()
	if len(msgs) != 1 {
		t.Fatalf("msgs = %#v", msgs)
	}
	resp, ok := msgs[0].(*mcpprotocol.JsonRpcSuccessResponse)
	if !ok || resp.Id.Number != 1 {
		t.Fatalf("resp = %#v", msgs[0])
	}
	resObj, _ := resp.Result.(*jsonx.Object)
	if txt, _ := resObj.Get("text"); txt != "é€" {
		t.Fatalf("text = %v", txt)
	}
}

func TestStreamableHttp_ReadsOnlyExcerptOfHttpErrorBodiesAndCancelsRest(t *testing.T) {
	endless := &tshtEndlessReader{chunk: []byte(strings.Repeat("x", 1024))}
	openGet := false
	tr, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           "http://127.0.0.1/mcp",
		OpenGetStream: &openGet,
		Fetch: func(context.Context, string, *mcp.McpRequestInit) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       endless,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	sendErr := tr.Send(&mcpprotocol.JsonRpcRequest{Jsonrpc: "2.0", Id: mcpprotocol.JsonRpcId{Number: 1}, Method: "tools/list"})
	_ = tr.Close()
	var httpErr *mcp.McpHttpError
	if !errors.As(sendErr, &httpErr) {
		t.Fatalf("expected McpHttpError, got %v", sendErr)
	}
	if len(httpErr.Body) != 8*1024 {
		t.Fatalf("len(Body) = %d, want %d", len(httpErr.Body), 8*1024)
	}
	if !endless.cancelled.Load() {
		t.Fatal("expected oversized error body reader to be closed")
	}
}

func TestStreamableHttp_IncludesResponseBodyInHttpErrors(t *testing.T) {
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, _ *tshtServerState) {
		_, _ = testkit.ReadBody(r)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid Accept header"))
	})
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	_, err := client.Connect(tr)
	if err == nil || !strings.Contains(err.Error(), "MCP HTTP request failed with status 400: Invalid Accept header") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttp_Hands401AndInsufficientScope403ToAuthProvider(t *testing.T) {
	var mu sync.Mutex
	type seenEntry struct {
		status int
		token  string
	}
	var seen []seenEntry
	token := "old"

	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, _ := tshtReadMessage(r)
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer old" {
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if m, _ := msg.Get("method"); m == "tools/call" && authHeader == "Bearer new" {
			w.Header().Set("Www-Authenticate", `Bearer error="insufficient_scope", scope="admin"`)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		tshtProtocolHandler(w, r, s, msg)
	})

	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           rawURL,
		OpenGetStream: &openGet,
		AuthProvider: &tshtAuthFunc{
			tokenFn: func() (*string, error) {
				mu.Lock()
				t := token
				mu.Unlock()
				return &t, nil
			},
			unauthorizedFn: func(ctx *mcp.UnauthorizedContext) error {
				mu.Lock()
				tok := ""
				if ctx.Token != nil {
					tok = *ctx.Token
				}
				seen = append(seen, seenEntry{status: ctx.Response.StatusCode, token: tok})
				if ctx.Response.StatusCode == http.StatusUnauthorized {
					token = "new"
				} else {
					token = "admin"
				}
				mu.Unlock()
				return nil
			},
		},
	})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	res, err := client.CallTool(t.Context(), "echo", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tshtFirstText(t, res); got != "hello" {
		t.Fatalf("res text = %q", got)
	}
	want := []seenEntry{{401, "old"}, {403, "new"}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen = %#v, want %#v", seen, want)
	}
}

func TestStreamableHttp_DoesNotRetryCancelledOrTimedOutRequestsDuringAuthRecovery(t *testing.T) {
	var tokenMu sync.Mutex
	token := "old"
	var execMu sync.Mutex
	var executed []string

	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method != http.MethodPost {
			tshtProtocolHandler(w, r, s, nil)
			return
		}
		msg, _ := tshtReadMessage(r)
		m, _ := msg.Get("method")
		tokenMu.Lock()
		auth := r.Header.Get("Authorization")
		tokenMu.Unlock()
		if m == "tools/call" && auth == "Bearer old" {
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if m == "tools/call" {
			p, _ := msg.Get("params")
			po, _ := p.(*jsonx.Object)
			name, _ := po.Get("name")
			execMu.Lock()
			executed = append(executed, name.(string))
			execMu.Unlock()
		}
		tshtProtocolHandler(w, r, s, msg)
	})

	refreshGate := make(chan struct{})
	var refreshOnce sync.Once
	var refreshes atomic.Int32
	var waiting atomic.Int32
	waitingGate := make(chan struct{})
	var waitOnce sync.Once
	var refreshMu sync.Mutex
	var refreshDone chan struct{}

	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           rawURL,
		OpenGetStream: &openGet,
		AuthProvider: &tshtAuthFunc{
			tokenFn: func() (*string, error) {
				tokenMu.Lock()
				t := token
				tokenMu.Unlock()
				return &t, nil
			},
			unauthorizedFn: func(*mcp.UnauthorizedContext) error {
				refreshMu.Lock()
				ch := refreshDone
				if ch == nil {
					ch = make(chan struct{})
					refreshDone = ch
					refreshes.Add(1)
					go func() {
						<-refreshGate
						tokenMu.Lock()
						token = "new"
						tokenMu.Unlock()
						close(ch)
					}()
				}
				refreshMu.Unlock()
				if waiting.Add(1) == 3 {
					waitOnce.Do(func() { close(waitingGate) })
				}
				<-ch
				return nil
			},
		},
	})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() {
		refreshOnce.Do(func() { close(refreshGate) })
		_ = client.Close()
	}()

	ctxAbort, cancelAbort := context.WithCancel(t.Context())
	abortedCh := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctxAbort, "aborted", nil, nil)
		abortedCh <- err
	}()
	timedOutCh := make(chan error, 1)
	go func() {
		to := int64(200)
		_, err := client.CallTool(t.Context(), "timed-out", nil, &mcp.McpRequestOptions{TimeoutMs: &to})
		timedOutCh <- err
	}()
	keptCh := make(chan struct {
		res *mcpprotocol.CallToolResult
		err error
	}, 1)
	go func() {
		res, err := client.CallTool(t.Context(), "kept", nil, nil)
		keptCh <- struct {
			res *mcpprotocol.CallToolResult
			err error
		}{res, err}
	}()

	<-waitingGate
	cancelAbort()

	var abortErr *mcpprotocol.McpAbortError
	if err := <-abortedCh; !errors.As(err, &abortErr) {
		t.Fatalf("expected McpAbortError, got %v", err)
	}
	var toErr *mcpprotocol.McpTimeoutError
	if err := <-timedOutCh; !errors.As(err, &toErr) {
		t.Fatalf("expected McpTimeoutError, got %v", err)
	}

	refreshOnce.Do(func() { close(refreshGate) })
	keptOut := <-keptCh
	if keptOut.err != nil {
		t.Fatalf("kept error = %v", keptOut.err)
	}
	if got := tshtFirstText(t, keptOut.res); got != "hello" {
		t.Fatalf("kept text = %q", got)
	}
	execMu.Lock()
	gotExec := slices.Clone(executed)
	execMu.Unlock()
	if !reflect.DeepEqual(gotExec, []string{"kept"}) {
		t.Fatalf("executed = %#v, want [kept]", gotExec)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes.Load())
	}
}

func tshtFirstText(t *testing.T, res *mcpprotocol.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result content: %#v", res)
	}
	tc, ok := res.Content[0].(*mcpprotocol.TextContent)
	if !ok || tc == nil {
		t.Fatalf("expected *TextContent, got %#v", res.Content[0])
	}
	return tc.Text
}

func TestStreamableHttp_ClosesSessionWithLastTokenWithoutAskingAuthProvider(t *testing.T) {
	rawURL, state := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		tshtProtocolHandler(w, r, s, nil)
	})
	var calls atomic.Int32
	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           rawURL,
		OpenGetStream: &openGet,
		AuthProvider: &tshtAuthFunc{
			tokenFn: func() (*string, error) {
				v := fmt.Sprintf("token-%d", calls.Add(1))
				return &v, nil
			},
		},
	})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatalf("Close asked auth provider for a new token: before=%d after=%d", before, calls.Load())
	}
	wantToken := fmt.Sprintf("Bearer token-%d", before)
	var deletes []tshtRecordedRequest
	for _, entry := range state.snapshot() {
		if entry.method == http.MethodDelete {
			deletes = append(deletes, entry)
		}
	}
	if len(deletes) != 1 || deletes[0].headers.Get("Authorization") != wantToken || deletes[0].headers.Get("Mcp-Session-Id") != "session-1" {
		t.Fatalf("deletes = %#v, want 1 DELETE with %q and session-1", deletes, wantToken)
	}
}

func TestStreamableHttp_PassesFetchThroughToUnauthorizedContext(t *testing.T) {
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method == http.MethodPost && r.Header.Get("Authorization") == "" {
			_, _ = testkit.ReadBody(r)
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		tshtProtocolHandler(w, r, s, nil)
	})

	runCase := func(customFetch mcp.McpFetch) {
		var mu sync.Mutex
		var token *string
		openGet := false
		tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
			Url:           rawURL,
			Fetch:         customFetch,
			OpenGetStream: &openGet,
			AuthProvider: &tshtAuthFunc{
				tokenFn: func() (*string, error) {
					mu.Lock()
					defer mu.Unlock()
					return token, nil
				},
				unauthorizedFn: func(u *mcp.UnauthorizedContext) error {
					resp, err := u.Fetch(t.Context(), rawURL, nil)
					if err != nil {
						return err
					}
					_ = resp.Body.Close()
					mu.Lock()
					tok := "token"
					token = &tok
					mu.Unlock()
					return nil
				},
			},
		})
		client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
		if _, err := client.Connect(tr); err != nil {
			t.Fatal(err)
		}
		tools, err := client.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools) != 1 || tools[0].Name != "echo" {
			t.Fatalf("tools = %#v", tools)
		}
		_ = client.Close()
	}

	var fetchCalls atomic.Int32
	customFetch := func(ctx context.Context, input string, init *mcp.McpRequestInit) (*http.Response, error) {
		fetchCalls.Add(1)
		var bodyReader io.Reader
		method := http.MethodGet
		if init != nil {
			if init.Method != "" {
				method = init.Method
			}
			if init.Body != nil {
				bodyReader = bytes.NewReader(init.Body)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, input, bodyReader)
		if err != nil {
			return nil, err
		}
		if init != nil && init.Headers != nil {
			req.Header = init.Headers.Clone()
		}
		return http.DefaultClient.Do(req)
	}

	runCase(customFetch)
	if fetchCalls.Load() == 0 {
		t.Fatal("expected customFetch to be invoked")
	}
	runCase(nil)
}

func TestStreamableHttp_ClassifiesExpiredEstablishedSession(t *testing.T) {
	var posts atomic.Int32
	rawURL, _ := tshtStartServer(t, func(w http.ResponseWriter, r *http.Request, s *tshtServerState) {
		if r.Method == http.MethodPost && posts.Add(1) > 2 {
			_, _ = testkit.ReadBody(r)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("gone"))
			return
		}
		tshtProtocolHandler(w, r, s, nil)
	})
	openGet := false
	tr, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{Url: rawURL, OpenGetStream: &openGet})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "http-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_, err := client.ListTools(t.Context(), nil)
	var expired *mcp.McpSessionExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("expected McpSessionExpiredError, got %v", err)
	}
}
