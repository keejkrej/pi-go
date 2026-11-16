// Ported from packages/mcp/test/client.test.ts (pi v1.5.2).

package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

type tclientServer struct {
	transport *InMemoryTransport
	mu        sync.Mutex
	messages  []protocol.JsonRpcMessage
	handlers  map[string]func(req *protocol.JsonRpcRequest) (any, error)
}

func (s *tclientServer) setHandler(method string, h func(req *protocol.JsonRpcRequest) (any, error)) {
	s.mu.Lock()
	s.handlers[method] = h
	s.mu.Unlock()
}

func (s *tclientServer) snapshotMessages() []protocol.JsonRpcMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.messages)
}

func tclientNewServer(t *testing.T) (*InMemoryTransport, *tclientServer) {
	t.Helper()
	pair := CreateInMemoryTransportPair()
	srv := &tclientServer{
		transport: pair.Server,
		handlers:  make(map[string]func(req *protocol.JsonRpcRequest) (any, error)),
	}
	srv.setHandler("initialize", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf(
			"protocolVersion", protocol.LatestProtocolVersion,
			"capabilities", jsonx.ObjectOf("tools", jsonx.ObjectOf("listChanged", true)),
			"serverInfo", jsonx.ObjectOf("name", "test-server", "version", "1.0.0"),
			"instructions", "Use test tools.",
		), nil
	})

	pair.Server.OnMessage(func(msg protocol.JsonRpcMessage) {
		srv.mu.Lock()
		srv.messages = append(srv.messages, msg)
		req, isReq := msg.(*protocol.JsonRpcRequest)
		var handler func(*protocol.JsonRpcRequest) (any, error)
		if isReq {
			handler = srv.handlers[req.Method]
		}
		srv.mu.Unlock()
		if !isReq {
			return
		}
		go func() {
			if handler == nil {
				_ = pair.Server.Send(&protocol.JsonRpcErrorResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Error:   protocol.JsonRpcErrorObject{Code: -32601, Message: "Method not found: " + req.Method},
				})
				return
			}
			result, err := handler(req)
			if err != nil {
				var mcpErr *protocol.McpError
				if !errors.As(err, &mcpErr) {
					mcpErr = protocol.NewMcpError(-32603, err.Error(), nil)
				}
				_ = pair.Server.Send(&protocol.JsonRpcErrorResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Error:   protocol.JsonRpcErrorObject{Code: mcpErr.Code, Message: mcpErr.Message, Data: mcpErr.Data},
				})
				return
			}
			_ = pair.Server.Send(&protocol.JsonRpcSuccessResponse{
				Jsonrpc: "2.0",
				Id:      req.Id,
				Result:  result,
			})
		}()
	})
	if err := pair.Server.Start(); err != nil {
		t.Fatal(err)
	}
	return pair.Client, srv
}

func tclientConnect(t *testing.T) (*McpClient, *tclientServer) {
	t.Helper()
	clientTr, srv := tclientNewServer(t)
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "test-client", Version: "2.0.0"},
	})
	if _, err := client.Connect(clientTr); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	return client, srv
}

func TestClient_InitializesBeforeExposingServerInfo(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	if client.ConnectionState() != ClientStateConnected {
		t.Fatalf("state = %q", client.ConnectionState())
	}
	if pv := client.ProtocolVersion(); pv == nil || *pv != protocol.LatestProtocolVersion {
		t.Fatalf("protocolVersion = %v", pv)
	}
	if info := client.ServerInfo(); info == nil || info.Name != "test-server" || info.Version != "1.0.0" {
		t.Fatalf("serverInfo = %#v", info)
	}
	if caps := client.ServerCapabilities(); caps == nil || caps.Tools == nil || caps.Tools.ListChanged == nil || !*caps.Tools.ListChanged {
		t.Fatalf("serverCapabilities = %#v", caps)
	}
	if inst := client.Instructions(); inst == nil || *inst != "Use test tools." {
		t.Fatalf("instructions = %v", inst)
	}

	var msgs []protocol.JsonRpcMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		msgs = srv.snapshotMessages()
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d server messages, want 2: %#v", len(msgs), msgs)
	}
	req0, ok := msgs[0].(*protocol.JsonRpcRequest)
	if !ok || req0.Method != "initialize" || req0.Id.Number != 1 {
		t.Fatalf("msg0 = %#v", msgs[0])
	}
	notif1, ok := msgs[1].(*protocol.JsonRpcNotification)
	if !ok || notif1.Method != "notifications/initialized" {
		t.Fatalf("msg1 = %#v", msgs[1])
	}
}

func TestClient_PaginatesToolsAndPreservesDefinitions(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("tools/list", func(req *protocol.JsonRpcRequest) (any, error) {
		var hasCursor bool
		if po, ok := req.Params.(*jsonx.Object); ok && po != nil {
			if c, ok := po.Get("cursor"); ok && c != nil {
				hasCursor = true
			}
		}
		if !hasCursor {
			return jsonx.ObjectOf(
				"tools", []any{
					jsonx.ObjectOf("name", "search", "description", "Search", "inputSchema", jsonx.ObjectOf("type", "object")),
				},
				"nextCursor", "page-2",
			), nil
		}
		return jsonx.ObjectOf(
			"tools", []any{
				jsonx.ObjectOf(
					"name", "read",
					"inputSchema", jsonx.ObjectOf("type", "object"),
					"outputSchema", jsonx.ObjectOf("type", "object"),
					"annotations", jsonx.ObjectOf("readOnlyHint", true),
				),
			},
			"nextCursor", "",
		), nil
	})

	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "read" {
		t.Fatalf("tools = %#v", tools)
	}
	if tools[1].Annotations == nil || tools[1].Annotations.ReadOnlyHint == nil || !*tools[1].Annotations.ReadOnlyHint {
		t.Fatalf("tools[1].Annotations = %#v", tools[1].Annotations)
	}
}

func TestClient_ListsAndReadsResources(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("resources/list", func(req *protocol.JsonRpcRequest) (any, error) {
		var cursor any
		if po, ok := req.Params.(*jsonx.Object); ok && po != nil {
			cursor, _ = po.Get("cursor")
		}
		if cursor == nil {
			return jsonx.ObjectOf(
				"resources", []any{jsonx.ObjectOf("uri", "file:///a", "name", "a", "mimeType", "text/plain")},
				"nextCursor", "2",
			), nil
		}
		return jsonx.ObjectOf(
			"resources", []any{jsonx.ObjectOf("uri", "file:///b")},
		), nil
	})
	srv.setHandler("resources/templates/list", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf(
			"resourceTemplates", []any{jsonx.ObjectOf("uriTemplate", "repo://{owner}/{repo}", "name", "repo")},
		), nil
	})
	srv.setHandler("resources/read", func(req *protocol.JsonRpcRequest) (any, error) {
		uri, _ := req.Params.(*jsonx.Object).Get("uri")
		return jsonx.ObjectOf(
			"contents", []any{jsonx.ObjectOf("uri", uri, "text", "hello")},
		), nil
	})

	resources, err := client.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 || resources[0].Name != "a" || resources[1].Name != "file:///b" {
		t.Fatalf("resources = %#v", resources)
	}
	templates, err := client.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0].UriTemplate != "repo://{owner}/{repo}" {
		t.Fatalf("templates = %#v", templates)
	}
	page1, err := client.ListResourcesPage(t.Context(), nil, nil)
	if err != nil || page1.NextCursor == nil || *page1.NextCursor != "2" {
		t.Fatalf("page1 = %#v err=%v", page1, err)
	}
	c2 := "2"
	page2, err := client.ListResourcesPage(t.Context(), &c2, nil)
	if err != nil || len(page2.Resources) != 1 || page2.Resources[0].Name != "file:///b" {
		t.Fatalf("page2 = %#v err=%v", page2, err)
	}
	readRes, err := client.ReadResource(t.Context(), "file:///a", nil)
	if err != nil || len(readRes.Contents) != 1 {
		t.Fatalf("readRes = %#v err=%v", readRes, err)
	}
	if tc, ok := readRes.Contents[0].(*protocol.TextResourceContents); !ok || tc.Text != "hello" {
		t.Fatalf("contents[0] = %#v", readRes.Contents[0])
	}

	srv.setHandler("resources/read", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf("contents", []any{jsonx.ObjectOf("uri", "file:///a")}), nil
	})
	if _, err := client.ReadResource(t.Context(), "file:///a", nil); err == nil || !strings.Contains(err.Error(), "Invalid contents in MCP resources/read result") {
		t.Fatalf("expected invalid contents error, got %v", err)
	}
	srv.setHandler("resources/list", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf("resources", []any{jsonx.ObjectOf("name", "no uri")}), nil
	})
	if _, err := client.ListResources(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "Invalid entry in MCP resources/list result") {
		t.Fatalf("expected invalid entry error, got %v", err)
	}
}

func TestClient_StructuredContentAndJsonRpcErrors(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("tools/call", func(req *protocol.JsonRpcRequest) (any, error) {
		po := req.Params.(*jsonx.Object)
		nameVal, _ := po.Get("name")
		if nameVal == "fail" {
			return nil, protocol.NewMcpError(1234, "tool failed", jsonx.ObjectOf("retryable", false))
		}
		var countVal any
		if args, ok := po.Get("arguments"); ok {
			if ao, ok := args.(*jsonx.Object); ok {
				countVal, _ = ao.Get("count")
			}
		}
		return jsonx.ObjectOf(
			"content", []any{jsonx.ObjectOf("type", "text", "text", "ok")},
			"structuredContent", jsonx.ObjectOf("count", countVal),
		), nil
	})

	res, err := client.CallTool(t.Context(), "count", jsonx.ObjectOf("count", 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	scVal, scOk := res.StructuredContent.Get()
	if !scOk {
		t.Fatal("expected structuredContent")
	}
	if !jsonx.DeepEqual(scVal, jsonx.ObjectOf("count", 3)) {
		t.Fatalf("structuredContent = %#v", scVal)
	}

	_, err = client.CallTool(t.Context(), "fail", nil, nil)
	var mcpErr *protocol.McpError
	if !errors.As(err, &mcpErr) || mcpErr.Code != 1234 || mcpErr.Message != "tool failed" ||
		!jsonx.DeepEqual(mcpErr.Data, jsonx.ObjectOf("retryable", false)) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_RenewsTimeoutOnProgress(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("tools/call", func(req *protocol.JsonRpcRequest) (any, error) {
		metaVal, _ := req.Params.(*jsonx.Object).Get("_meta")
		tokVal, _ := metaVal.(*jsonx.Object).Get("progressToken")
		time.Sleep(80 * time.Millisecond)
		_ = srv.transport.Send(&protocol.JsonRpcNotification{
			Jsonrpc: "2.0",
			Method:  "notifications/progress",
			Params: jsonx.ObjectOf(
				"progressToken", tokVal,
				"progress", 1,
				"total", 2,
			),
		})
		time.Sleep(100 * time.Millisecond)
		return jsonx.ObjectOf("content", []any{jsonx.ObjectOf("type", "text", "text", "done")}), nil
	})

	var mu sync.Mutex
	var progressEvents []*protocol.ProgressNotification
	timeoutMs := int64(150)
	res, err := client.CallTool(t.Context(), "slow", nil, &McpRequestOptions{
		TimeoutMs: &timeoutMs,
		OnProgress: func(p *protocol.ProgressNotification) {
			mu.Lock()
			progressEvents = append(progressEvents, p)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].(*protocol.TextContent).Text != "done" {
		t.Fatalf("res = %#v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(progressEvents) != 1 || progressEvents[0].Progress != 1 || *progressEvents[0].Total != 2 {
		t.Fatalf("progressEvents = %#v", progressEvents)
	}
}

func TestClient_CancelsAbortedAndTimedOutRequests(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	started := make(chan struct{})
	var startOnce sync.Once
	block := make(chan struct{})
	defer close(block)
	srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
		startOnce.Do(func() { close(started) })
		<-block
		return jsonx.ObjectOf("content", []any{}), nil
	})

	ctx, cancel := context.WithCancelCause(t.Context())
	callErrCh := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, "wait", nil, nil)
		callErrCh <- err
	}()
	<-started
	cancel(errors.New("stop"))

	var abortErr *protocol.McpAbortError
	if err := <-callErrCh; !errors.As(err, &abortErr) {
		t.Fatalf("expected McpAbortError, got %v", err)
	}
	deadline := time.Now().Add(time.Second)
	var foundCancel bool
	for time.Now().Before(deadline) && !foundCancel {
		for _, m := range srv.snapshotMessages() {
			if n, ok := m.(*protocol.JsonRpcNotification); ok && n.Method == "notifications/cancelled" {
				po := n.Params.(*jsonx.Object)
				rid, _ := po.Get("requestId")
				reason, _ := po.Get("reason")
				isReq2 := false
				switch v := rid.(type) {
				case protocol.JsonRpcId:
					isReq2 = !v.IsString && v.Number == 2
				case float64:
					isReq2 = v == 2
				}
				if isReq2 && reason == "stop" {
					foundCancel = true
					break
				}
			}
		}
		if !foundCancel {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !foundCancel {
		t.Fatal("expected notifications/cancelled to be sent for aborted call")
	}

	timeoutMs := int64(5)
	_, err := client.CallTool(t.Context(), "wait", nil, &McpRequestOptions{TimeoutMs: &timeoutMs})
	var toErr *protocol.McpTimeoutError
	if !errors.As(err, &toErr) {
		t.Fatalf("expected McpTimeoutError, got %v", err)
	}
}

func TestClient_ReportsTransportErrorsWithoutFailingPendingRequests(t *testing.T) {
	clientTr, srv := tclientNewServer(t)
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "test-client", Version: "1.0.0"},
	})
	if _, err := client.Connect(clientTr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	var mu sync.Mutex
	var errs []error
	client.OnError(func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	})

	started := make(chan struct{})
	release := make(chan struct{})
	srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
		close(started)
		<-release
		return jsonx.ObjectOf("content", []any{}), nil
	})

	resCh := make(chan *protocol.CallToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := client.CallTool(t.Context(), "wait", nil, nil)
		resCh <- r
		errCh <- err
	}()
	<-started
	clientTr.EmitError(errors.New("stray log line"))
	close(release)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if r := <-resCh; len(r.Content) != 0 {
		t.Fatalf("expected empty content, got %#v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || errs[0].Error() != "stray log line" {
		t.Fatalf("errs = %v", errs)
	}
}

func TestClient_AcceptsOlderProtocolVersionAndRejectsUnsupported(t *testing.T) {
	clientTr, srv := tclientNewServer(t)
	srv.setHandler("initialize", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf(
			"protocolVersion", "2024-11-05",
			"capabilities", jsonx.NewObject(),
			"serverInfo", jsonx.ObjectOf("name", "old-server", "version", "0.1.0"),
		), nil
	})
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "test-client", Version: "1.0.0"},
	})
	if _, err := client.Connect(clientTr); err != nil {
		t.Fatal(err)
	}
	if pv := client.ProtocolVersion(); pv == nil || *pv != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", pv)
	}
	_ = client.Close()

	badTr, badSrv := tclientNewServer(t)
	badSrv.setHandler("initialize", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf(
			"protocolVersion", "1999-01-01",
			"capabilities", jsonx.NewObject(),
			"serverInfo", jsonx.ObjectOf("name", "ancient-server", "version", "0.1.0"),
		), nil
	})
	rejected := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "test-client", Version: "1.0.0"},
	})
	if _, err := rejected.Connect(badTr); err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("expected unsupported protocol version error, got %v", err)
	}
	if rejected.ConnectionState() != ClientStateClosed {
		t.Fatalf("state = %q, want closed", rejected.ConnectionState())
	}
}

func TestClient_DefaultsMissingToolContentAndNonObjectStructuredContent(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf("structuredContent", jsonx.ObjectOf("ok", true)), nil
	})
	res, err := client.CallTool(t.Context(), "structured", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 0 || !res.StructuredContent.IsSome() {
		t.Fatalf("res = %#v", res)
	}

	srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
		return jsonx.ObjectOf("content", "not a list"), nil
	})
	if _, err := client.CallTool(t.Context(), "broken", nil, nil); err == nil || !strings.Contains(err.Error(), "Invalid MCP tools/call result") {
		t.Fatalf("expected invalid result error, got %v", err)
	}

	// #10416: MCP 2026-07-28 allows any JSON value as structured content.
	values := []any{
		[]any{jsonx.ObjectOf("id", 1), jsonx.ObjectOf("id", 2)},
		nil,
		"text",
		3,
		false,
	}
	for _, v := range values {
		srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
			return jsonx.ObjectOf("content", []any{}, "structuredContent", v), nil
		})
		r, err := client.CallTool(t.Context(), "structured", nil, nil)
		if err != nil {
			t.Fatalf("value=%#v failed: %v", v, err)
		}
		if v == nil {
			if !r.StructuredContent.IsNull() {
				t.Fatalf("expected Null structuredContent, got %#v", r.StructuredContent)
			}
		} else if gotSC, ok := r.StructuredContent.Get(); !ok || !jsonx.DeepEqual(gotSC, v) {
			t.Fatalf("structuredContent = %#v (ok=%v), want %#v", gotSC, ok, v)
		}
	}
}

func TestClient_DoesNotSendCancelledForTimedOutInitialize(t *testing.T) {
	clientTr, srv := tclientNewServer(t)
	block := make(chan struct{})
	defer close(block)
	srv.setHandler("initialize", func(*protocol.JsonRpcRequest) (any, error) {
		<-block
		return jsonx.NewObject(), nil
	})
	timeoutMs := int64(5)
	client := NewMcpClient(&McpClientOptions{
		Implementation:   protocol.Implementation{Name: "test-client", Version: "1.0.0"},
		RequestTimeoutMs: &timeoutMs,
	})
	_, err := client.Connect(clientTr)
	var toErr *protocol.McpTimeoutError
	if !errors.As(err, &toErr) {
		t.Fatalf("expected McpTimeoutError, got %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	for _, m := range srv.snapshotMessages() {
		if n, ok := m.(*protocol.JsonRpcNotification); ok && n.Method == "notifications/cancelled" {
			t.Fatal("did not expect notifications/cancelled for initialize timeout")
		}
	}
}

func TestClient_NotifiesCloseListenersOnceOnTransportDrop(t *testing.T) {
	client, srv := tclientConnect(t)
	var mu sync.Mutex
	closedCalls := 0
	client.OnClose(func() {
		mu.Lock()
		closedCalls++
		mu.Unlock()
	})

	started := make(chan struct{})
	block := make(chan struct{})
	defer close(block)
	srv.setHandler("tools/call", func(*protocol.JsonRpcRequest) (any, error) {
		close(started)
		<-block
		return jsonx.NewObject(), nil
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := client.CallTool(t.Context(), "wait", nil, nil)
		errCh <- err
	}()
	<-started
	_ = srv.transport.Close()

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "MCP connection closed") {
		t.Fatalf("expected MCP connection closed error, got %v", err)
	}
	if client.ConnectionState() != ClientStateClosed {
		t.Fatalf("state = %q", client.ConnectionState())
	}
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	if closedCalls != 1 {
		t.Fatalf("closedCalls = %d, want 1", closedCalls)
	}
}

func TestClient_AnswersRootsListAndDispatchesNotifications(t *testing.T) {
	clientTr, srv := tclientNewServer(t)
	name := "workspace"
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "test-client", Version: "1.0.0"},
		Roots:          &McpClientRoots{List: []protocol.Root{{Uri: "file:///workspace", Name: &name}}},
	})
	if _, err := client.Connect(clientTr); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	notifCh := make(chan any, 1)
	client.OnNotification("notifications/tools/list_changed", func(params any) {
		notifCh <- params
	})

	_ = srv.transport.Send(&protocol.JsonRpcRequest{
		Jsonrpc: "2.0",
		Id:      protocol.JsonRpcId{IsString: true, String: "roots"},
		Method:  "roots/list",
	})
	_ = srv.transport.Send(&protocol.JsonRpcNotification{
		Jsonrpc: "2.0",
		Method:  "notifications/tools/list_changed",
	})

	gotParams := <-notifCh
	if gotParams != nil {
		t.Fatalf("expected nil notification params, got %#v", gotParams)
	}

	var rootsRes *protocol.JsonRpcSuccessResponse
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && rootsRes == nil {
		for _, m := range srv.snapshotMessages() {
			if r, ok := m.(*protocol.JsonRpcSuccessResponse); ok && r.Id.String == "roots" {
				rootsRes = r
				break
			}
		}
		if rootsRes == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if rootsRes == nil {
		t.Fatal("did not receive roots/list response")
	}
	obj, ok := rootsRes.Result.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected *jsonx.Object result, got %T", rootsRes.Result)
	}
	want := jsonx.ObjectOf("roots", []any{jsonx.ObjectOf("uri", "file:///workspace", "name", "workspace")})
	if !jsonx.DeepEqual(obj, want) {
		t.Fatalf("roots result = %v, want %v", obj, want)
	}
}

func TestClient_ListsAndGetsPrompts(t *testing.T) {
	client, srv := tclientConnect(t)
	defer func() { _ = client.Close() }()

	srv.setHandler("prompts/list", func(req *protocol.JsonRpcRequest) (any, error) {
		if po, ok := req.Params.(*jsonx.Object); ok && po != nil {
			if c, has := po.Get("cursor"); has && c == "p2" {
				return jsonx.ObjectOf("prompts", []any{
					jsonx.ObjectOf("name", "review"),
				}), nil
			}
		}
		return jsonx.ObjectOf(
			"prompts", []any{jsonx.ObjectOf("name", "summarize", "description", "Summarize file")},
			"nextCursor", "p2",
		), nil
	})
	srv.setHandler("prompts/get", func(req *protocol.JsonRpcRequest) (any, error) {
		po := req.Params.(*jsonx.Object)
		argsObj, _ := po.Get("arguments")
		argMap := argsObj.(*jsonx.Object)
		topic, _ := argMap.Get("topic")
		return jsonx.ObjectOf(
			"description", "Prompt body",
			"messages", []any{
				jsonx.ObjectOf("role", "user", "content", jsonx.ObjectOf("type", "text", "text", "Explain "+topic.(string))),
			},
		), nil
	})

	prompts, err := client.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || prompts[0].Name != "summarize" || prompts[1].Name != "review" {
		t.Fatalf("prompts = %#v", prompts)
	}

	args := omap.NewMap[string, string]()
	args.Set("topic", "mcp")
	got, err := client.GetPrompt(t.Context(), "summarize", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("prompt result = %#v", got)
	}
}
