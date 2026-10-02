// Ported from packages/mcp/src/transports/streamable-http.ts (pi v1.0.0).

package mcp

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"

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

// ConsumeSseStream reads an MCP SSE byte stream until it ends.
func ConsumeSseStream(stream io.Reader, options *ConsumeSseOptions) error {
	panic("unported: ConsumeSseStream")
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
	panic("unported: NewMcpAuthRequiredError")
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
	// ctx cancels in-flight HTTP and reconnect waits (TS AbortController).
	ctx    context.Context
	cancel context.CancelFunc
}

// NewStreamableHttpTransport builds a transport. It does not open a connection.
func NewStreamableHttpTransport(options *StreamableHttpTransportOptions) (*StreamableHttpTransport, error) {
	panic("unported: NewStreamableHttpTransport")
}

// SessionId returns the server-assigned session id, or nil when the server has not sent one.
func (t *StreamableHttpTransport) SessionId() *string { return t.sessionId }

// Start marks the transport started. The GET stream opens after initialization.
func (t *StreamableHttpTransport) Start() error {
	panic("unported: StreamableHttpTransport.Start")
}

// SetProtocolVersion records the negotiated protocol version sent on later requests.
func (t *StreamableHttpTransport) SetProtocolVersion(version string) {
	panic("unported: StreamableHttpTransport.SetProtocolVersion")
}

// Send posts one JSON-RPC message.
func (t *StreamableHttpTransport) Send(message protocol.JsonRpcMessage) error {
	panic("unported: StreamableHttpTransport.Send")
}

// Close aborts in-flight work and deletes the session when one was established.
func (t *StreamableHttpTransport) Close() error {
	panic("unported: StreamableHttpTransport.Close")
}

// StreamableHttpTransport implements McpTransport.
var _ McpTransport = (*StreamableHttpTransport)(nil)
