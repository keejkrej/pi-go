// Ported from packages/mcp/src/transports/streamable-http.ts (pi v1.0.0).

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

const (
	tshMaxErrorBodyBytes              = 8 * 1024
	tshErrorMessageBodyChars          = 500
	tshDefaultReconnectInitialDelayMs = 1000
	tshDefaultReconnectMaxDelayMs     = 30000
	tshDefaultReconnectMaxRetries     = 5
)

// SseEvent is one dispatched SSE event. Empty event and id strings are omitted.
type SseEvent struct {
	Event *string `json:"event,omitzero"`
	Data  string  `json:"data"`
	Id    *string `json:"id,omitzero"`
}

// ConsumeSseOptions are the options of ConsumeSseStream.
type ConsumeSseOptions struct {
	MaxEventBytes *int
	OnEvent       func(event SseEvent)
	// OnId is called for every id field, including events without data (for example resumption priming events).
	OnId func(id string)
	// OnRetry is called for every valid retry field, in milliseconds.
	OnRetry func(delayMs int64)
}

type tshMaxEventBytesError struct {
	limit int
}

func (e tshMaxEventBytesError) Error() string {
	return fmt.Sprintf("MCP SSE event exceeds %d bytes", e.limit)
}

// ConsumeSseStream reads an MCP SSE byte stream until it ends.
func ConsumeSseStream(stream io.Reader, options *ConsumeSseOptions) error {
	if stream == nil {
		return nil
	}
	var opts ConsumeSseOptions
	if options != nil {
		opts = *options
	}
	maxEventBytes := DefaultMaxMessageBytes
	if opts.MaxEventBytes != nil {
		maxEventBytes = *opts.MaxEventBytes
	}

	decoder := js.NewUTF8StreamDecoder()
	buf := make([]byte, 32*1024)
	pending := ""
	firstChunk := true

	var (
		eventName  string
		dataLines  []string
		hasData    bool
		eventId    string
		hasId      bool
		eventBytes int
	)

	dispatch := func() {
		if hasData {
			var evPtr *string
			if eventName != "" {
				e := eventName
				evPtr = &e
			}
			var idPtr *string
			if hasId && eventId != "" {
				idCopy := eventId
				idPtr = &idCopy
			}
			ev := SseEvent{
				Event: evPtr,
				Data:  strings.Join(dataLines, "\n"),
				Id:    idPtr,
			}
			if opts.OnEvent != nil {
				opts.OnEvent(ev)
			}
		}
		eventName = ""
		dataLines = nil
		hasData = false
		eventId = ""
		hasId = false
		eventBytes = 0
	}

	processLine := func(line string) error {
		if line == "" {
			dispatch()
			return nil
		}
		if strings.HasPrefix(line, ":") {
			return nil
		}
		colon := strings.IndexByte(line, ':')
		var field, value string
		if colon < 0 {
			field = line
		} else {
			field = line[:colon]
			value = line[colon+1:]
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
		}
		switch field {
		case "event":
			eventBytes += len(value)
			if eventBytes > maxEventBytes {
				return tshMaxEventBytesError{limit: maxEventBytes}
			}
			eventName = value
		case "data":
			add := len(value)
			if hasData {
				add++ // joined with '\n'
			}
			eventBytes += add
			if eventBytes > maxEventBytes {
				return tshMaxEventBytesError{limit: maxEventBytes}
			}
			dataLines = append(dataLines, value)
			hasData = true
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				eventBytes += len(value)
				if eventBytes > maxEventBytes {
					return tshMaxEventBytesError{limit: maxEventBytes}
				}
				eventId = value
				hasId = true
				if opts.OnId != nil {
					opts.OnId(value)
				}
			}
		case "retry":
			if value != "" && tshIsAsciiDigits(value) {
				if delay, err := strconv.ParseInt(value, 10, 64); err == nil && delay >= 0 {
					if opts.OnRetry != nil {
						opts.OnRetry(delay)
					}
				}
			}
		}
		return nil
	}

	skipLeadingLf := false
	consumeLines := func(final bool) error {
		start := 0
		for i := 0; i < len(pending); i++ {
			ch := pending[i]
			if ch != '\r' && ch != '\n' {
				continue
			}
			if err := processLine(pending[start:i]); err != nil {
				return err
			}
			if ch == '\r' {
				if i+1 < len(pending) {
					if pending[i+1] == '\n' {
						i++
					}
				} else {
					skipLeadingLf = true
				}
			}
			start = i + 1
		}
		pending = pending[start:]
		if final && pending != "" {
			if err := processLine(pending); err != nil {
				return err
			}
			pending = ""
		}
		return nil
	}

	processChunkText := func(text string, final bool) error {
		if firstChunk && text != "" {
			firstChunk = false
			text = strings.TrimPrefix(text, "\uFEFF")
		}
		if skipLeadingLf && len(text) > 0 {
			text = strings.TrimPrefix(text, "\n")
			skipLeadingLf = false
		}
		if text != "" {
			pending += text
		}
		if err := consumeLines(final); err != nil {
			return err
		}
		if len(pending) > maxEventBytes {
			return tshMaxEventBytesError{limit: maxEventBytes}
		}
		return nil
	}

	failStream := func(err error) error {
		if closer, ok := stream.(io.Closer); ok {
			_ = closer.Close()
		}
		return err
	}

	for {
		n, rerr := stream.Read(buf)
		if n > 0 {
			decoded := decoder.Decode(buf[:n])
			if err := processChunkText(decoded, false); err != nil {
				return failStream(err)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				tail := decoder.Flush()
				if err := processChunkText(tail, true); err != nil {
					return failStream(err)
				}
				dispatch()
				return nil
			}
			return failStream(rerr)
		}
	}
}

func tshIsAsciiDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// StreamableHttpReconnectOptions is reconnection of dropped SSE streams
// (the GET stream, and response streams that carry event IDs).
type StreamableHttpReconnectOptions struct {
	// InitialDelayMs is the delay before the first reconnection attempt, unless the server sent a retry field. Default: 1000.
	InitialDelayMs *int64
	// MaxDelayMs is the upper bound for the exponential backoff. Default: 30000.
	MaxDelayMs *int64
	// MaxRetries is the number of consecutive failed attempts before giving up on a stream. Default: 5.
	MaxRetries *int
}

// StreamableHttpTransportOptions are the options of NewStreamableHttpTransport.
type StreamableHttpTransportOptions struct {
	Url     string
	Headers *omap.Map[string, string]
	// Fetch, when set, replaces HTTPClient.
	Fetch McpFetch
	// OpenGetStream opens the server-to-client GET stream after initialization. Nil means true. False disables it.
	OpenGetStream   *bool
	MaxMessageBytes *int
	AuthProvider    AuthProvider
	Reconnect       *StreamableHttpReconnectOptions
	// HTTPClient is the Go-only HTTP client. Nil means the default client.
	HTTPClient *http.Client
}

// McpHttpError is an HTTP failure from a streamable MCP endpoint.
type McpHttpError struct {
	Status  int
	Message string
	Body    string
}

// NewMcpHttpError returns an HTTP error. body is empty when the caller has no body.
func NewMcpHttpError(status int, message, body string) *McpHttpError {
	return &McpHttpError{Status: status, Message: message, Body: body}
}

func (e *McpHttpError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *McpHttpError) Name() string { return "McpHttpError" }

// McpAuthRequiredError is a 401 from the MCP server.
type McpAuthRequiredError struct {
	*McpHttpError
	WwwAuthenticate *string
}

// NewMcpAuthRequiredError builds the error from the rejected response.
func NewMcpAuthRequiredError(response *http.Response, body string) *McpAuthRequiredError {
	var wwwAuth *string
	if response != nil {
		if h := response.Header.Get("www-authenticate"); h != "" {
			wwwAuth = &h
		}
	}
	return &McpAuthRequiredError{
		McpHttpError:    NewMcpHttpError(http.StatusUnauthorized, "MCP server requires authentication", body),
		WwwAuthenticate: wwwAuth,
	}
}

// Name returns the TS error name.
func (e *McpAuthRequiredError) Name() string { return "McpAuthRequiredError" }

// Unwrap returns the embedded HTTP error so errors.As matches instanceof McpHttpError.
func (e *McpAuthRequiredError) Unwrap() error { return e.McpHttpError }

// McpSessionExpiredError is a 404 for an MCP session the server no longer has.
type McpSessionExpiredError struct {
	*McpHttpError
}

// NewMcpSessionExpiredError returns a session-expired error.
func NewMcpSessionExpiredError(body string) *McpSessionExpiredError {
	return &McpSessionExpiredError{
		McpHttpError: NewMcpHttpError(404, "MCP session expired", body),
	}
}

// Name returns the TS error name.
func (e *McpSessionExpiredError) Name() string { return "McpSessionExpiredError" }

// Unwrap returns the embedded HTTP error so errors.As matches instanceof McpHttpError.
func (e *McpSessionExpiredError) Unwrap() error { return e.McpHttpError }

// StreamableHttpTransport is an MCP client transport over Streamable HTTP.
type StreamableHttpTransport struct {
	mu sync.Mutex
	TransportEvents

	Url     *url.URL
	Options StreamableHttpTransportOptions

	fetch            McpFetch
	sessionId        *string
	protocolVersion  string
	started          bool
	closed           bool
	getStreamStarted bool
	lastToken        *string
	// ctx cancels in-flight HTTP and reconnect waits (TS AbortController).
	ctx    context.Context
	cancel context.CancelFunc
}

type tshStreamCursor struct {
	lastEventId *string
	retryMs     *int64
	received    bool
}

// NewStreamableHttpTransport builds a transport. It does not open a connection.
func NewStreamableHttpTransport(options *StreamableHttpTransportOptions) (*StreamableHttpTransport, error) {
	if options == nil {
		return nil, errors.New("StreamableHttpTransport options are required")
	}
	parsed, err := url.Parse(options.Url)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("Invalid URL: %s", options.Url)
	}
	opts := *options
	if options.Headers != nil {
		opts.Headers = options.Headers.Clone()
	}
	fetchFn := opts.Fetch
	if fetchFn == nil {
		client := opts.HTTPClient
		if client == nil {
			client = ai.NodeHttpProxyDefaultClient()
		}
		fetchFn = tshClientFetch(client)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamableHttpTransport{
		Url:     parsed,
		Options: opts,
		fetch:   fetchFn,
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

func tshClientFetch(client *http.Client) McpFetch {
	return func(ctx context.Context, input string, init *McpRequestInit) (*http.Response, error) {
		method := http.MethodGet
		var body io.Reader
		if init != nil {
			if init.Method != "" {
				method = init.Method
			}
			if len(init.Body) > 0 {
				body = bytes.NewReader(init.Body)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, input, body)
		if err != nil {
			return nil, err
		}
		if init != nil && init.Headers != nil {
			for k, vals := range init.Headers {
				for _, v := range vals {
					req.Header.Add(k, v)
				}
			}
		}
		return client.Do(req)
	}
}

// SessionId returns the server-assigned session id, or nil when the server has not sent one.
func (t *StreamableHttpTransport) SessionId() *string {
	t.mu.Lock()
	sid := t.sessionId
	t.mu.Unlock()
	return sid
}

// Start marks the transport started. The GET stream opens after initialization.
func (t *StreamableHttpTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return errors.New("MCP Streamable HTTP transport already started")
	}
	if t.closed {
		return protocol.NewMcpConnectionClosedError()
	}
	t.started = true
	return nil
}

// SetProtocolVersion records the negotiated protocol version sent on later requests.
func (t *StreamableHttpTransport) SetProtocolVersion(version string) {
	t.mu.Lock()
	t.protocolVersion = version
	t.mu.Unlock()
}

// Send posts one JSON-RPC message.
func (t *StreamableHttpTransport) Send(message protocol.JsonRpcMessage) error {
	return t.SendWithContext(context.Background(), message)
}

// SendWithContext posts one JSON-RPC message with per-send cancellation support.
func (t *StreamableHttpTransport) SendWithContext(ctx context.Context, message protocol.JsonRpcMessage) error {
	t.mu.Lock()
	active := t.started && !t.closed
	maxMsgBytes := DefaultMaxMessageBytes
	if t.Options.MaxMessageBytes != nil {
		maxMsgBytes = *t.Options.MaxMessageBytes
	}
	t.mu.Unlock()
	if !active {
		return protocol.NewMcpConnectionClosedError()
	}

	payload, err := jsonx.Marshal(message)
	if err != nil {
		return err
	}
	extra := http.Header{
		"Accept":       []string{"application/json, text/event-stream"},
		"Content-Type": []string{"application/json"},
	}
	resp, err := t.authorizedFetch(ctx, http.MethodPost, extra, payload)
	if err != nil {
		return err
	}

	if err := t.checkResponse(resp); err != nil {
		_ = tshDiscard(resp)
		return err
	}
	t.captureSession(resp)

	if !protocol.IsJsonRpcRequest(message) {
		_ = tshDiscard(resp)
		if notif, ok := message.(*protocol.JsonRpcNotification); ok && notif != nil && notif.Method == "notifications/initialized" {
			t.startGetStream()
		}
		return nil
	}

	reqMethod := ""
	var reqId protocol.JsonRpcId
	if req, ok := message.(*protocol.JsonRpcRequest); ok && req != nil {
		reqMethod = req.Method
		reqId = req.Id
	}

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		_ = tshDiscard(resp)
		return NewMcpHttpError(resp.StatusCode, fmt.Sprintf("MCP server accepted request %s without a response", reqMethod), "")
	}

	ct := tshContentType(resp)
	if ct == "application/json" {
		text, truncated, err := tshReadBodyText(resp, maxMsgBytes)
		if err != nil {
			return err
		}
		if truncated {
			return fmt.Errorf("MCP JSON response exceeds %d bytes", maxMsgBytes)
		}
		body, err := jsonx.ParseString(strings.TrimPrefix(text, "\uFEFF"))
		if err != nil {
			return err
		}
		items, isArr := body.([]any)
		if !isArr {
			items = []any{body}
		}
		for _, item := range items {
			parsedMsg, err := protocol.ParseJsonRpcMessage(item)
			if err != nil {
				return err
			}
			t.emitMessage(parsedMsg)
		}
		return nil
	}

	if ct == "text/event-stream" && resp.Body != nil {
		go t.consumeResponseStream(resp, reqId)
		return nil
	}

	_ = tshDiscard(resp)
	missing := ct
	if missing == "" {
		missing = "missing"
	}
	return NewMcpHttpError(resp.StatusCode, "Unsupported MCP response content type: "+missing, "")
}

// Close aborts in-flight work and deletes the session when one was established.
func (t *StreamableHttpTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	started := t.started
	hasSession := t.sessionId != nil && *t.sessionId != ""
	lastToken := t.lastToken
	fetchFn := t.fetch
	headers := t.buildHeadersLocked(nil, lastToken)
	urlStr := t.Url.String()
	cancel := t.cancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if started && hasSession {
		delCtx, delCancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := fetchFn(delCtx, urlStr, &McpRequestInit{
			Method:  http.MethodDelete,
			Headers: headers,
		})
		if err == nil && resp != nil {
			_ = tshDiscard(resp)
		}
		delCancel()
	}

	t.emitClose()
	return nil
}

func (t *StreamableHttpTransport) authorizedFetch(ctx context.Context, method string, extra http.Header, body []byte) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	authProvider := t.Options.AuthProvider
	fetchFn := t.fetch
	tCtx := t.ctx
	serverUrl := t.Url
	urlStr := serverUrl.String()
	t.mu.Unlock()

	var unauthHandler AuthProviderUnauthorized
	if authProvider != nil {
		unauthHandler, _ = authProvider.(AuthProviderUnauthorized)
	}

	for attempt := 0; ; attempt++ {
		headers, token, err := t.headers(extra)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, protocol.NewMcpAbortError()
		}
		resp, err := fetchFn(tCtx, urlStr, &McpRequestInit{
			Method:  method,
			Headers: headers,
			Body:    body,
		})
		if err != nil {
			return nil, err
		}
		if attempt > 0 || unauthHandler == nil || !tshNeedsAuthorization(resp) {
			return resp, nil
		}
		err = unauthHandler.OnUnauthorized(&UnauthorizedContext{
			Response:  resp,
			ServerUrl: serverUrl,
			Fetch:     fetchFn,
			Token:     token,
		})
		_ = tshDiscard(resp)
		if err != nil {
			return nil, err
		}
	}
}

func (t *StreamableHttpTransport) headers(extra http.Header) (http.Header, *string, error) {
	t.mu.Lock()
	authProvider := t.Options.AuthProvider
	t.mu.Unlock()

	var token *string
	if authProvider != nil {
		tok, err := authProvider.Token()
		if err != nil {
			return nil, nil, err
		}
		token = tok
	}

	t.mu.Lock()
	t.lastToken = token
	hdrs := t.buildHeadersLocked(extra, token)
	t.mu.Unlock()
	return hdrs, token, nil
}

func (t *StreamableHttpTransport) buildHeadersLocked(extra http.Header, token *string) http.Header {
	headers := make(http.Header)
	if t.Options.Headers != nil {
		for k, v := range t.Options.Headers.All() {
			headers.Set(k, v)
		}
	}
	for k, vals := range extra {
		headers.Del(k)
		for _, v := range vals {
			headers.Add(k, v)
		}
	}
	if t.sessionId != nil && *t.sessionId != "" {
		headers.Set("Mcp-Session-Id", *t.sessionId)
	}
	if t.protocolVersion != "" {
		headers.Set("Mcp-Protocol-Version", t.protocolVersion)
	}
	if token != nil && *token != "" {
		headers.Set("Authorization", "Bearer "+*token)
	}
	return headers
}

func (t *StreamableHttpTransport) captureSession(resp *http.Response) {
	if resp == nil {
		return
	}
	sid := resp.Header.Get("mcp-session-id")
	if sid != "" {
		t.mu.Lock()
		t.sessionId = &sid
		t.mu.Unlock()
	}
}

func (t *StreamableHttpTransport) checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _, _ := tshReadBodyText(resp, tshMaxErrorBodyBytes)
	if resp.StatusCode == http.StatusUnauthorized {
		return NewMcpAuthRequiredError(resp, body)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.mu.Lock()
		hasSession := t.sessionId != nil && *t.sessionId != ""
		t.mu.Unlock()
		if hasSession {
			return NewMcpSessionExpiredError(body)
		}
	}
	return NewMcpHttpError(resp.StatusCode, tshDescribeHttpFailure(resp.StatusCode, body), body)
}

func (t *StreamableHttpTransport) consumeSse(resp *http.Response, cursor *tshStreamCursor, onMessage func(protocol.JsonRpcMessage)) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	t.mu.Lock()
	maxEventBytes := DefaultMaxMessageBytes
	if t.Options.MaxMessageBytes != nil {
		maxEventBytes = *t.Options.MaxMessageBytes
	}
	tCtx := t.ctx
	t.mu.Unlock()

	stop := context.AfterFunc(tCtx, func() {
		_ = resp.Body.Close()
	})
	defer stop()
	defer func() { _ = resp.Body.Close() }()

	return ConsumeSseStream(resp.Body, &ConsumeSseOptions{
		MaxEventBytes: &maxEventBytes,
		OnId: func(id string) {
			idCopy := id
			cursor.lastEventId = &idCopy
		},
		OnRetry: func(delayMs int64) {
			d := delayMs
			cursor.retryMs = &d
		},
		OnEvent: func(event SseEvent) {
			cursor.received = true
			if strings.TrimSpace(event.Data) == "" || (event.Event != nil && *event.Event != "message") {
				return
			}
			parsed, err := jsonx.ParseString(event.Data)
			if err != nil {
				t.emitError(err)
				return
			}
			msg, err := protocol.ParseJsonRpcMessage(parsed)
			if err != nil {
				t.emitError(err)
				return
			}
			if onMessage != nil {
				onMessage(msg)
			}
			t.emitMessage(msg)
		},
	})
}

func (t *StreamableHttpTransport) consumeResponseStream(resp *http.Response, requestId protocol.JsonRpcId) {
	cursor := &tshStreamCursor{}
	answered := false
	onMessage := func(msg protocol.JsonRpcMessage) {
		switch r := msg.(type) {
		case *protocol.JsonRpcSuccessResponse:
			if r != nil && r.Id == requestId {
				answered = true
			}
		case *protocol.JsonRpcErrorResponse:
			if r != nil && r.Id == requestId {
				answered = true
			}
		}
	}

	curResp := resp
	var failure error
	maxRetries := t.maxRetries()

	for attempt := 0; ; {
		if curResp != nil {
			failure = t.consumeSse(curResp, cursor, onMessage)
			curResp = nil
		}
		if answered || t.isClosed() {
			return
		}
		if failure != nil && !t.isRetryable(failure) {
			break
		}
		if cursor.lastEventId == nil || attempt >= maxRetries {
			break
		}
		if cursor.received {
			attempt = 0
		}
		cursor.received = false
		delay := t.reconnectDelay(attempt, cursor.retryMs)
		attempt++
		if !t.sleep(delay) {
			return
		}
		nextResp, err := t.openSseStream(cursor.lastEventId)
		if err != nil {
			failure = err
			if !t.isRetryable(err) {
				break
			}
			continue
		}
		if nextResp == nil {
			break
		}
		curResp = nextResp
	}

	if t.isClosed() {
		return
	}
	reason := "stream ended without a response"
	if failure != nil {
		reason = failure.Error()
	}
	t.emitMessage(&protocol.JsonRpcErrorResponse{
		Jsonrpc: "2.0",
		Id:      requestId,
		Error: protocol.JsonRpcErrorObject{
			Code:    protocol.JsonRpcErrorCodes.InternalError,
			Message: "MCP response stream failed: " + reason,
		},
	})
}

func (t *StreamableHttpTransport) startGetStream() {
	t.mu.Lock()
	if (t.Options.OpenGetStream != nil && !*t.Options.OpenGetStream) || t.getStreamStarted || t.closed {
		t.mu.Unlock()
		return
	}
	t.getStreamStarted = true
	t.mu.Unlock()
	go t.runGetStream()
}

func (t *StreamableHttpTransport) runGetStream() {
	cursor := &tshStreamCursor{}
	maxRetries := t.maxRetries()
	maxDelay := time.Duration(t.maxDelay()) * time.Millisecond

	for attempt := 0; !t.isClosed(); {
		resp, err := t.openSseStream(cursor.lastEventId)
		if err != nil {
			if t.isClosed() {
				return
			}
			if !t.isRetryable(err) {
				t.emitError(err)
				return
			}
		} else if resp == nil {
			// Server does not offer a GET stream (405).
			return
		} else {
			openedAt := time.Now()
			consumeErr := t.consumeSse(resp, cursor, nil)
			if consumeErr != nil {
				if t.isClosed() {
					return
				}
				if !t.isRetryable(consumeErr) {
					t.emitError(consumeErr)
					return
				}
			}
			if cursor.received || time.Since(openedAt) > maxDelay {
				attempt = 0
			}
		}

		cursor.received = false
		if attempt >= maxRetries {
			t.emitError(errors.New("MCP server-to-client stream dropped and could not be reopened"))
			return
		}
		delay := t.reconnectDelay(attempt, cursor.retryMs)
		attempt++
		if !t.sleep(delay) {
			return
		}
	}
}

func (t *StreamableHttpTransport) openSseStream(lastEventId *string) (*http.Response, error) {
	extra := http.Header{
		"Accept": []string{"text/event-stream"},
	}
	if lastEventId != nil {
		extra.Set("Last-Event-Id", *lastEventId)
	}
	resp, err := t.authorizedFetch(context.Background(), http.MethodGet, extra, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		_ = tshDiscard(resp)
		return nil, nil
	}
	if err := t.checkResponse(resp); err != nil {
		_ = tshDiscard(resp)
		return nil, err
	}
	t.captureSession(resp)
	ct := tshContentType(resp)
	if ct != "text/event-stream" || resp.Body == nil {
		_ = tshDiscard(resp)
		missing := ct
		if missing == "" {
			missing = "missing"
		}
		return nil, NewMcpHttpError(resp.StatusCode, "Unsupported MCP GET response content type: "+missing, "")
	}
	return resp, nil
}

func (t *StreamableHttpTransport) isClosed() bool {
	t.mu.Lock()
	c := t.closed
	t.mu.Unlock()
	return c
}

func (t *StreamableHttpTransport) isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var maxErr tshMaxEventBytesError
	if errors.As(err, &maxErr) {
		return false
	}
	var httpErr *McpHttpError
	if errors.As(err, &httpErr) {
		return tshIsTransientStatus(httpErr.Status)
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return !t.isClosed()
}

func (t *StreamableHttpTransport) reconnectDelay(attempt int, serverDelayMs *int64) int64 {
	if serverDelayMs != nil {
		return *serverDelayMs
	}
	var initial int64 = tshDefaultReconnectInitialDelayMs
	if t.Options.Reconnect != nil && t.Options.Reconnect.InitialDelayMs != nil {
		initial = *t.Options.Reconnect.InitialDelayMs
	}
	maxDelay := t.maxDelay()
	if initial <= 0 {
		return 0
	}
	delay := initial
	for i := 0; i < attempt && delay < maxDelay; i++ {
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func (t *StreamableHttpTransport) maxDelay() int64 {
	if t.Options.Reconnect != nil && t.Options.Reconnect.MaxDelayMs != nil {
		return *t.Options.Reconnect.MaxDelayMs
	}
	return tshDefaultReconnectMaxDelayMs
}

func (t *StreamableHttpTransport) maxRetries() int {
	if t.Options.Reconnect != nil && t.Options.Reconnect.MaxRetries != nil {
		return *t.Options.Reconnect.MaxRetries
	}
	return tshDefaultReconnectMaxRetries
}

func (t *StreamableHttpTransport) sleep(ms int64) bool {
	t.mu.Lock()
	ctx := t.ctx
	t.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	if ms <= 0 {
		return true
	}
	return js.Sleep(ctx, ms) == nil
}

func tshContentType(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	ct := resp.Header.Get("content-type")
	if ct == "" {
		return ""
	}
	base, _, _ := strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func tshNeedsAuthorization(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode != http.StatusForbidden {
		return false
	}
	h := strings.ToLower(resp.Header.Get("www-authenticate"))
	return strings.Contains(h, "error=\"insufficient_scope\"") || strings.Contains(h, "error=insufficient_scope")
}

func tshIsTransientStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func tshDiscard(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	return resp.Body.Close()
}

func tshReadBodyText(resp *http.Response, maxBytes int) (string, bool, error) {
	if resp == nil || resp.Body == nil {
		return "", false, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if maxBytes < 0 {
		maxBytes = 0
	}
	buf := make([]byte, 32*1024)
	decoder := js.NewUTF8StreamDecoder()
	var sb strings.Builder
	remaining := maxBytes
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if n > remaining {
				if remaining > 0 {
					sb.WriteString(decoder.Decode(buf[:remaining]))
				}
				return sb.String(), true, nil
			}
			remaining -= n
			sb.WriteString(decoder.Decode(buf[:n]))
		}
		if err != nil {
			if err == io.EOF {
				sb.WriteString(decoder.Flush())
				return sb.String(), false, nil
			}
			return "", false, err
		}
	}
}

func tshDescribeHttpFailure(status int, body string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return fmt.Sprintf("MCP HTTP request failed with status %d", status)
	}
	snippet := text
	if js.Len(snippet) > tshErrorMessageBodyChars {
		snippet = js.Slice(snippet, 0, tshErrorMessageBodyChars-3) + "..."
	}
	return fmt.Sprintf("MCP HTTP request failed with status %d: %s", status, snippet)
}

// StreamableHttpTransport implements McpTransport.
var _ McpTransport = (*StreamableHttpTransport)(nil)
