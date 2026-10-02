// Ported from packages/mcp/src/client.ts (pi v1.0.0).

package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

const (
	clientDefaultRequestTimeoutMs int64 = 30_000
	clientMaxListPages                  = 1_000
)

// ClientState is the connection lifecycle of an McpClient.
type ClientState string

const (
	ClientStateIdle       ClientState = "idle"
	ClientStateConnecting ClientState = "connecting"
	ClientStateConnected  ClientState = "connected"
	ClientStateClosed     ClientState = "closed"
)

// NotificationListener receives notification params for one method.
type NotificationListener func(params any)

// ErrorListener receives a normalized transport or client error.
type ErrorListener func(err error)

// CloseListener is called once when the connection closes.
type CloseListener func()

// RequestHandler answers a server-initiated request.
// ctx is cancelled when the server sends notifications/cancelled.
// A nil result is sent as an empty object.
type RequestHandler func(ctx context.Context, params any) (any, error)

// McpClientRoots is McpClientOptions.roots.
// A nil *McpClientRoots means the option is absent.
// A non-nil Func is the function form; otherwise List is the static list
// (a nil List is an empty list).
type McpClientRoots struct {
	List []protocol.Root
	Func func() ([]protocol.Root, error)
}

// McpClientOptions is the configuration passed to NewMcpClient.
// It extends protocol.Implementation (name, version, and optional title).
type McpClientOptions struct {
	protocol.Implementation
	Capabilities     *protocol.ClientCapabilities
	ProtocolVersion  *protocol.SupportedProtocolVersion
	RequestTimeoutMs *int64
	Roots            *McpClientRoots
}

// McpRequestOptions tunes one client call.
// Cancellation is the ctx argument of the call (TS McpRequestOptions.signal).
type McpRequestOptions struct {
	TimeoutMs  *int64
	OnProgress func(progress *protocol.ProgressNotification)
}

type clientPendingRequest struct {
	resolve       func(value any)
	reject        func(reason error)
	timeoutMs     int64
	timer         *time.Timer
	onAbort       func()
	cancellable   bool
	onProgress    func(progress *protocol.ProgressNotification)
	progressToken *protocol.JsonRpcId
}

type clientListPageResult struct {
	Items      []*jsonx.Object
	NextCursor *string
}

// McpClient is an MCP client session over an McpTransport.
type McpClient struct {
	mu                    sync.Mutex
	Options               McpClientOptions
	state                 ClientState
	transport             McpTransport
	nextRequestId         int
	serverInfo            *protocol.Implementation
	serverCapabilities    *protocol.ServerCapabilities
	instructions          *string
	protocolVersion       *string
	pending               *omap.Map[protocol.JsonRpcId, *clientPendingRequest]
	progressRequests      *omap.Map[protocol.JsonRpcId, protocol.JsonRpcId]
	incoming              *omap.Map[protocol.JsonRpcId, context.CancelFunc]
	requestHandlers       map[string]RequestHandler
	notificationListeners map[string][]NotificationListener
	errorListeners        []ErrorListener
	closeListeners        []CloseListener
	disposers             []func()
}

// NewMcpClient returns a client in the idle state.
// It registers ping, and roots/list when options.Roots is set.
func NewMcpClient(options *McpClientOptions) *McpClient {
	panic("unported: NewMcpClient")
}

func (c *McpClient) ConnectionState() ClientState { return c.state }

func (c *McpClient) ServerInfo() *protocol.Implementation { return c.serverInfo }

func (c *McpClient) ServerCapabilities() *protocol.ServerCapabilities { return c.serverCapabilities }

func (c *McpClient) Instructions() *string { return c.instructions }

func (c *McpClient) ProtocolVersion() *string { return c.protocolVersion }

// Connect starts transport and performs initialize. On failure the client is closed.
func (c *McpClient) Connect(transport McpTransport) (*protocol.InitializeResult, error) {
	panic("unported: McpClient.Connect")
}

// Request sends one JSON-RPC request and waits for its response.
func (c *McpClient) Request(ctx context.Context, method string, params *jsonx.Object, options *McpRequestOptions) (any, error) {
	panic("unported: McpClient.Request")
}

// Notify sends one JSON-RPC notification.
func (c *McpClient) Notify(method string, params *jsonx.Object) error {
	panic("unported: McpClient.Notify")
}

// SetRequestHandler registers handler for method and returns an unsubscribe function.
func (c *McpClient) SetRequestHandler(method string, handler RequestHandler) func() {
	panic("unported: McpClient.SetRequestHandler")
}

// OnNotification registers listener for method and returns an unsubscribe function.
func (c *McpClient) OnNotification(method string, listener NotificationListener) func() {
	panic("unported: McpClient.OnNotification")
}

// OnError registers listener and returns an unsubscribe function.
func (c *McpClient) OnError(listener ErrorListener) func() {
	panic("unported: McpClient.OnError")
}

// OnClose registers listener, called once when the connection closes,
// whether the transport dropped or Close was called.
func (c *McpClient) OnClose(listener CloseListener) func() {
	panic("unported: McpClient.OnClose")
}

func (c *McpClient) Ping(ctx context.Context, options *McpRequestOptions) error {
	panic("unported: McpClient.Ping")
}

func (c *McpClient) ListTools(ctx context.Context, options *McpRequestOptions) ([]protocol.Tool, error) {
	panic("unported: McpClient.ListTools")
}

// ListResources returns every resource, following nextCursor through all pages.
func (c *McpClient) ListResources(ctx context.Context, options *McpRequestOptions) ([]protocol.Resource, error) {
	panic("unported: McpClient.ListResources")
}

// ListResourcesPage returns one page of resources, starting at cursor.
// A nil cursor requests the first page.
func (c *McpClient) ListResourcesPage(ctx context.Context, cursor *string, options *McpRequestOptions) (*protocol.ListResourcesResult, error) {
	panic("unported: McpClient.ListResourcesPage")
}

// ListResourceTemplates returns every resource template, following nextCursor through all pages.
func (c *McpClient) ListResourceTemplates(ctx context.Context, options *McpRequestOptions) ([]protocol.ResourceTemplate, error) {
	panic("unported: McpClient.ListResourceTemplates")
}

// ListResourceTemplatesPage returns one page of resource templates, starting at cursor.
// A nil cursor requests the first page.
func (c *McpClient) ListResourceTemplatesPage(ctx context.Context, cursor *string, options *McpRequestOptions) (*protocol.ListResourceTemplatesResult, error) {
	panic("unported: McpClient.ListResourceTemplatesPage")
}

func (c *McpClient) ReadResource(ctx context.Context, uri string, options *McpRequestOptions) (*protocol.ReadResourceResult, error) {
	panic("unported: McpClient.ReadResource")
}

func (c *McpClient) CallTool(ctx context.Context, name string, args *jsonx.Object, options *McpRequestOptions) (*protocol.CallToolResult, error) {
	panic("unported: McpClient.CallTool")
}

func (c *McpClient) Close() error {
	panic("unported: McpClient.Close")
}

func clientValidateInitializeResult(value any) (*protocol.InitializeResult, error) {
	panic("unported: clientValidateInitializeResult")
}

func clientInvalid(message string) *protocol.McpError {
	panic("unported: clientInvalid")
}

func clientValidateListPage(method, key string, value any, isItem func(item *jsonx.Object) bool) (*clientListPageResult, error) {
	panic("unported: clientValidateListPage")
}

func clientIsTool(tool *jsonx.Object) bool {
	panic("unported: clientIsTool")
}

func clientIsResource(resource *jsonx.Object) bool {
	panic("unported: clientIsResource")
}

func clientIsResourceTemplate(template *jsonx.Object) bool {
	panic("unported: clientIsResourceTemplate")
}

func clientToResource(item *jsonx.Object) *protocol.Resource {
	panic("unported: clientToResource")
}

func clientToResourceTemplate(item *jsonx.Object) *protocol.ResourceTemplate {
	panic("unported: clientToResourceTemplate")
}

func clientPageCursor(page *clientListPageResult) *string {
	panic("unported: clientPageCursor")
}

func clientValidateReadResourceResult(value any) (*protocol.ReadResourceResult, error) {
	panic("unported: clientValidateReadResourceResult")
}

func clientValidateCallToolResult(value any) (*protocol.CallToolResult, error) {
	panic("unported: clientValidateCallToolResult")
}

func (c *McpClient) listPage(ctx context.Context, method, key string, isItem func(item *jsonx.Object) bool, cursor *string, options *McpRequestOptions) (*clientListPageResult, error) {
	panic("unported: McpClient.listPage")
}

func (c *McpClient) listAll(ctx context.Context, method, key string, isItem func(item *jsonx.Object) bool, options *McpRequestOptions) ([]*jsonx.Object, error) {
	panic("unported: McpClient.listAll")
}

func (c *McpClient) requestInternal(ctx context.Context, method string, params *jsonx.Object, options *McpRequestOptions, allowConnecting bool) (any, error) {
	panic("unported: McpClient.requestInternal")
}

func (c *McpClient) notifyInternal(method string, params *jsonx.Object, allowConnecting bool) error {
	panic("unported: McpClient.notifyInternal")
}

func (c *McpClient) requireTransport(allowConnecting bool) (McpTransport, error) {
	panic("unported: McpClient.requireTransport")
}

func (c *McpClient) handleMessage(message protocol.JsonRpcMessage) {
	panic("unported: McpClient.handleMessage")
}

func (c *McpClient) handleResponse(message protocol.JsonRpcResponse) {
	panic("unported: McpClient.handleResponse")
}

func (c *McpClient) handleRequest(message *protocol.JsonRpcRequest) error {
	panic("unported: McpClient.handleRequest")
}

func (c *McpClient) handleNotification(method string, params any) {
	panic("unported: McpClient.handleNotification")
}

func (c *McpClient) handleProgress(params any) {
	panic("unported: McpClient.handleProgress")
}

func (c *McpClient) handleCancelled(params any) {
	panic("unported: McpClient.handleCancelled")
}

func (c *McpClient) armTimeout(id protocol.JsonRpcId, entry *clientPendingRequest) {
	panic("unported: McpClient.armTimeout")
}

func (c *McpClient) cancelPending(id protocol.JsonRpcId, err error, notifyServer bool, reason *string) {
	panic("unported: McpClient.cancelPending")
}

func (c *McpClient) removePending(id protocol.JsonRpcId, entry *clientPendingRequest) {
	panic("unported: McpClient.removePending")
}

func (c *McpClient) rejectPending(err error) {
	panic("unported: McpClient.rejectPending")
}

func (c *McpClient) handleTransportClose() {
	panic("unported: McpClient.handleTransportClose")
}

func (c *McpClient) markClosed(err error) {
	panic("unported: McpClient.markClosed")
}

func (c *McpClient) emitError(err any) {
	panic("unported: McpClient.emitError")
}

func (c *McpClient) disposeTransportListeners() {
	panic("unported: McpClient.disposeTransportListeners")
}
