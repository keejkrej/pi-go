// Ported from packages/mcp/src/client.ts (pi v1.0.0).

package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
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
	sendCancel    context.CancelFunc
	cancellable   bool
	onProgress    func(progress *protocol.ProgressNotification)
	progressToken *protocol.JsonRpcId
	settled       bool
}

type clientListPageResult struct {
	Items      []*jsonx.Object
	NextCursor *string
}

type clientRequestHandlerEntry struct {
	id uint64
	fn RequestHandler
}

type clientNotificationListenerEntry struct {
	id uint64
	fn NotificationListener
}

type clientErrorListenerEntry struct {
	id uint64
	fn ErrorListener
}

type clientCloseListenerEntry struct {
	id uint64
	fn CloseListener
}

// McpClient is an MCP client session over an McpTransport.
type McpClient struct {
	mu                    sync.Mutex
	Options               McpClientOptions
	state                 ClientState
	transport             McpTransport
	nextRequestId         int
	nextListenerId        uint64
	serverInfo            *protocol.Implementation
	serverCapabilities    *protocol.ServerCapabilities
	instructions          *string
	protocolVersion       *string
	pending               *omap.Map[protocol.JsonRpcId, *clientPendingRequest]
	progressRequests      *omap.Map[protocol.JsonRpcId, protocol.JsonRpcId]
	incoming              *omap.Map[protocol.JsonRpcId, context.CancelFunc]
	requestHandlers       map[string]clientRequestHandlerEntry
	notificationListeners map[string][]clientNotificationListenerEntry
	errorListeners        []clientErrorListenerEntry
	closeListeners        []clientCloseListenerEntry
	disposers             []func()
}

// NewMcpClient returns a client in the idle state.
// It registers ping, and roots/list when options.Roots is set.
func NewMcpClient(options *McpClientOptions) *McpClient {
	var opts McpClientOptions
	if options != nil {
		opts = *options
	}
	c := &McpClient{
		Options:               opts,
		state:                 ClientStateIdle,
		nextRequestId:         1,
		pending:               omap.NewMap[protocol.JsonRpcId, *clientPendingRequest](),
		progressRequests:      omap.NewMap[protocol.JsonRpcId, protocol.JsonRpcId](),
		incoming:              omap.NewMap[protocol.JsonRpcId, context.CancelFunc](),
		requestHandlers:       make(map[string]clientRequestHandlerEntry),
		notificationListeners: make(map[string][]clientNotificationListenerEntry),
	}
	c.SetRequestHandler("ping", func(_ context.Context, _ any) (any, error) {
		return jsonx.ObjectOf(), nil
	})
	if opts.Roots != nil {
		rootsCfg := opts.Roots
		c.SetRequestHandler("roots/list", func(_ context.Context, _ any) (any, error) {
			var list []protocol.Root
			if rootsCfg.Func != nil {
				res, err := rootsCfg.Func()
				if err != nil {
					return nil, err
				}
				list = res
			} else {
				list = rootsCfg.List
			}
			if list == nil {
				list = []protocol.Root{}
			} else {
				list = append([]protocol.Root(nil), list...)
			}
			return jsonx.ObjectOf("roots", list), nil
		})
	}
	return c
}

func (c *McpClient) ConnectionState() ClientState {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s
}

func (c *McpClient) ServerInfo() *protocol.Implementation {
	c.mu.Lock()
	info := c.serverInfo
	c.mu.Unlock()
	return info
}

func (c *McpClient) ServerCapabilities() *protocol.ServerCapabilities {
	c.mu.Lock()
	caps := c.serverCapabilities
	c.mu.Unlock()
	return caps
}

func (c *McpClient) Instructions() *string {
	c.mu.Lock()
	inst := c.instructions
	c.mu.Unlock()
	return inst
}

func (c *McpClient) ProtocolVersion() *string {
	c.mu.Lock()
	pv := c.protocolVersion
	c.mu.Unlock()
	return pv
}

// Connect starts transport and performs initialize. On failure the client is closed.
func (c *McpClient) Connect(transport McpTransport) (*protocol.InitializeResult, error) {
	return c.ConnectContext(context.Background(), transport)
}

// ConnectContext starts transport and performs initialize using ctx. On failure the client is closed.
func (c *McpClient) ConnectContext(ctx context.Context, transport McpTransport) (*protocol.InitializeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if transport == nil {
		return nil, errors.New("MCP transport is required")
	}

	c.mu.Lock()
	if c.state != ClientStateIdle {
		st := c.state
		c.mu.Unlock()
		return nil, fmt.Errorf("Cannot connect MCP client in %s state", st)
	}
	c.state = ClientStateConnecting
	c.transport = transport
	opts := c.Options
	c.mu.Unlock()

	d1 := transport.OnMessage(func(message protocol.JsonRpcMessage) { c.handleMessage(message) })
	d2 := transport.OnError(func(err error) { c.emitError(err) })
	d3 := transport.OnClose(func() { c.handleTransportClose() })

	c.mu.Lock()
	if c.state != ClientStateConnecting {
		c.mu.Unlock()
		d1()
		d2()
		d3()
		return nil, protocol.NewMcpConnectionClosedError()
	}
	c.disposers = []func(){d1, d2, d3}
	c.mu.Unlock()

	cleanupOnErr := func(err error) (*protocol.InitializeResult, error) {
		_ = c.Close()
		return nil, err
	}

	if err := transport.Start(); err != nil {
		return cleanupOnErr(err)
	}

	var caps protocol.ClientCapabilities
	if opts.Capabilities != nil {
		caps = *opts.Capabilities
	}
	if opts.Roots != nil && caps.Roots == nil {
		caps.Roots = &protocol.ClientCapabilitiesRoots{}
	}
	reqProtoVersion := string(protocol.LatestProtocolVersion)
	if opts.ProtocolVersion != nil && *opts.ProtocolVersion != "" {
		reqProtoVersion = string(*opts.ProtocolVersion)
	}
	initParams := protocol.InitializeParams{
		ProtocolVersion: reqProtoVersion,
		Capabilities:    caps,
		ClientInfo: protocol.Implementation{
			Name:    opts.Name,
			Version: opts.Version,
			Title:   opts.Title,
		},
	}
	encParams, err := jsonx.Encode(initParams)
	if err != nil {
		return cleanupOnErr(err)
	}
	initParamsObj, _ := encParams.(*jsonx.Object)

	rawRes, err := c.requestInternal(ctx, "initialize", initParamsObj, nil, true)
	if err != nil {
		return cleanupOnErr(err)
	}
	result, err := clientValidateInitializeResult(rawRes)
	if err != nil {
		return cleanupOnErr(err)
	}
	if !slices.Contains(protocol.SupportedProtocolVersions, protocol.SupportedProtocolVersion(result.ProtocolVersion)) {
		return cleanupOnErr(fmt.Errorf("MCP server selected unsupported protocol version %s", result.ProtocolVersion))
	}

	c.mu.Lock()
	c.protocolVersion = &result.ProtocolVersion
	c.serverInfo = &result.ServerInfo
	c.serverCapabilities = &result.Capabilities
	c.instructions = result.Instructions
	c.mu.Unlock()

	transport.SetProtocolVersion(result.ProtocolVersion)
	if err := c.notifyInternal("notifications/initialized", nil, true); err != nil {
		return cleanupOnErr(err)
	}

	c.mu.Lock()
	if c.state != ClientStateConnecting {
		c.mu.Unlock()
		return nil, protocol.NewMcpConnectionClosedError()
	}
	c.state = ClientStateConnected
	c.mu.Unlock()
	return result, nil
}

// Request sends one JSON-RPC request and waits for its response.
func (c *McpClient) Request(ctx context.Context, method string, params *jsonx.Object, options *McpRequestOptions) (any, error) {
	return c.requestInternal(ctx, method, params, options, false)
}

// Notify sends one JSON-RPC notification.
func (c *McpClient) Notify(method string, params *jsonx.Object) error {
	return c.notifyInternal(method, params, false)
}

// SetRequestHandler registers handler for method and returns an unsubscribe function.
func (c *McpClient) SetRequestHandler(method string, handler RequestHandler) func() {
	c.mu.Lock()
	c.nextListenerId++
	id := c.nextListenerId
	c.requestHandlers[method] = clientRequestHandlerEntry{id: id, fn: handler}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if cur, ok := c.requestHandlers[method]; ok && cur.id == id {
			delete(c.requestHandlers, method)
		}
		c.mu.Unlock()
	}
}

// OnNotification registers listener for method and returns an unsubscribe function.
func (c *McpClient) OnNotification(method string, listener NotificationListener) func() {
	c.mu.Lock()
	c.nextListenerId++
	id := c.nextListenerId
	c.notificationListeners[method] = append(c.notificationListeners[method], clientNotificationListenerEntry{id: id, fn: listener})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		list := c.notificationListeners[method]
		for i, item := range list {
			if item.id == id {
				list = append(list[:i], list[i+1:]...)
				if len(list) == 0 {
					delete(c.notificationListeners, method)
				} else {
					c.notificationListeners[method] = list
				}
				break
			}
		}
		c.mu.Unlock()
	}
}

// OnError registers listener and returns an unsubscribe function.
func (c *McpClient) OnError(listener ErrorListener) func() {
	c.mu.Lock()
	c.nextListenerId++
	id := c.nextListenerId
	c.errorListeners = append(c.errorListeners, clientErrorListenerEntry{id: id, fn: listener})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		for i, item := range c.errorListeners {
			if item.id == id {
				c.errorListeners = append(c.errorListeners[:i], c.errorListeners[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// OnClose registers listener, called once when the connection closes,
// whether the transport dropped or Close was called.
func (c *McpClient) OnClose(listener CloseListener) func() {
	c.mu.Lock()
	c.nextListenerId++
	id := c.nextListenerId
	c.closeListeners = append(c.closeListeners, clientCloseListenerEntry{id: id, fn: listener})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		for i, item := range c.closeListeners {
			if item.id == id {
				c.closeListeners = append(c.closeListeners[:i], c.closeListeners[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

func (c *McpClient) Ping(ctx context.Context, options *McpRequestOptions) error {
	_, err := c.Request(ctx, "ping", nil, options)
	return err
}

func (c *McpClient) ListTools(ctx context.Context, options *McpRequestOptions) ([]protocol.Tool, error) {
	items, err := c.listAll(ctx, "tools/list", "tools", clientIsTool, options)
	if err != nil {
		return nil, err
	}
	tools := make([]protocol.Tool, 0, len(items))
	for _, item := range items {
		var tool protocol.Tool
		if err := jsonx.Decode(item, &tool); err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// ListResources returns every resource, following nextCursor through all pages.
func (c *McpClient) ListResources(ctx context.Context, options *McpRequestOptions) ([]protocol.Resource, error) {
	items, err := c.listAll(ctx, "resources/list", "resources", clientIsResource, options)
	if err != nil {
		return nil, err
	}
	resources := make([]protocol.Resource, 0, len(items))
	for _, item := range items {
		resources = append(resources, *clientToResource(item))
	}
	return resources, nil
}

// ListResourcesPage returns one page of resources, starting at cursor.
// A nil cursor requests the first page.
func (c *McpClient) ListResourcesPage(ctx context.Context, cursor *string, options *McpRequestOptions) (*protocol.ListResourcesResult, error) {
	page, err := c.listPage(ctx, "resources/list", "resources", clientIsResource, cursor, options)
	if err != nil {
		return nil, err
	}
	resources := make([]protocol.Resource, 0, len(page.Items))
	for _, item := range page.Items {
		resources = append(resources, *clientToResource(item))
	}
	return &protocol.ListResourcesResult{
		Resources:  resources,
		NextCursor: clientPageCursor(page),
	}, nil
}

// ListResourceTemplates returns every resource template, following nextCursor through all pages.
func (c *McpClient) ListResourceTemplates(ctx context.Context, options *McpRequestOptions) ([]protocol.ResourceTemplate, error) {
	items, err := c.listAll(ctx, "resources/templates/list", "resourceTemplates", clientIsResourceTemplate, options)
	if err != nil {
		return nil, err
	}
	templates := make([]protocol.ResourceTemplate, 0, len(items))
	for _, item := range items {
		templates = append(templates, *clientToResourceTemplate(item))
	}
	return templates, nil
}

// ListResourceTemplatesPage returns one page of resource templates, starting at cursor.
// A nil cursor requests the first page.
func (c *McpClient) ListResourceTemplatesPage(ctx context.Context, cursor *string, options *McpRequestOptions) (*protocol.ListResourceTemplatesResult, error) {
	page, err := c.listPage(ctx, "resources/templates/list", "resourceTemplates", clientIsResourceTemplate, cursor, options)
	if err != nil {
		return nil, err
	}
	templates := make([]protocol.ResourceTemplate, 0, len(page.Items))
	for _, item := range page.Items {
		templates = append(templates, *clientToResourceTemplate(item))
	}
	return &protocol.ListResourceTemplatesResult{
		ResourceTemplates: templates,
		NextCursor:        clientPageCursor(page),
	}, nil
}

func (c *McpClient) ReadResource(ctx context.Context, uri string, options *McpRequestOptions) (*protocol.ReadResourceResult, error) {
	raw, err := c.Request(ctx, "resources/read", jsonx.ObjectOf("uri", uri), options)
	if err != nil {
		return nil, err
	}
	return clientValidateReadResourceResult(raw)
}

// ListPrompts returns every prompt, following nextCursor through all pages.
func (c *McpClient) ListPrompts(ctx context.Context, options *McpRequestOptions) ([]protocol.Prompt, error) {
	items, err := c.listAll(ctx, "prompts/list", "prompts", clientIsPrompt, options)
	if err != nil {
		return nil, err
	}
	prompts := make([]protocol.Prompt, 0, len(items))
	for _, item := range items {
		var p protocol.Prompt
		if err := jsonx.Decode(item, &p); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, nil
}

// ListPromptsPage returns one page of prompts, starting at cursor.
func (c *McpClient) ListPromptsPage(ctx context.Context, cursor *string, options *McpRequestOptions) (*protocol.ListPromptsResult, error) {
	page, err := c.listPage(ctx, "prompts/list", "prompts", clientIsPrompt, cursor, options)
	if err != nil {
		return nil, err
	}
	prompts := make([]protocol.Prompt, 0, len(page.Items))
	for _, item := range page.Items {
		var p protocol.Prompt
		if err := jsonx.Decode(item, &p); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return &protocol.ListPromptsResult{
		Prompts:    prompts,
		NextCursor: clientPageCursor(page),
	}, nil
}

// GetPrompt fetches a prompt by name with optional arguments.
func (c *McpClient) GetPrompt(ctx context.Context, name string, args *omap.Map[string, string], options *McpRequestOptions) (*protocol.GetPromptResult, error) {
	params := jsonx.ObjectOf("name", name)
	if args != nil {
		argsObj := jsonx.NewObject()
		for k, v := range args.All() {
			argsObj.Set(k, v)
		}
		params.Set("arguments", argsObj)
	}
	raw, err := c.Request(ctx, "prompts/get", params, options)
	if err != nil {
		return nil, err
	}
	return clientValidateGetPromptResult(raw)
}

func (c *McpClient) CallTool(ctx context.Context, name string, args *jsonx.Object, options *McpRequestOptions) (*protocol.CallToolResult, error) {
	params := jsonx.ObjectOf("name", name)
	if args != nil {
		params.Set("arguments", args)
	}
	raw, err := c.Request(ctx, "tools/call", params, options)
	if err != nil {
		return nil, err
	}
	return clientValidateCallToolResult(raw)
}

func (c *McpClient) Close() error {
	c.mu.Lock()
	transport := c.transport
	c.transport = nil
	disposers := c.disposers
	c.disposers = nil
	c.mu.Unlock()

	for _, dispose := range disposers {
		dispose()
	}
	c.markClosed(protocol.NewMcpConnectionClosedError())
	if transport != nil {
		return transport.Close()
	}
	return nil
}

func clientValidateInitializeResult(value any) (*protocol.InitializeResult, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	pv, ok := obj.Get("protocolVersion")
	if !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	if _, ok := pv.(string); !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	caps, ok := obj.Get("capabilities")
	if !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	if !protocol.IsObject(caps) {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	siVal, ok := obj.Get("serverInfo")
	if !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	siObj, ok := siVal.(*jsonx.Object)
	if !ok || siObj == nil {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	nameVal, _ := siObj.Get("name")
	verVal, _ := siObj.Get("version")
	if _, ok := nameVal.(string); !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	if _, ok := verVal.(string); !ok {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	if instVal, hasInst := obj.Get("instructions"); hasInst {
		if _, ok := instVal.(string); !ok {
			return nil, clientInvalid("Invalid MCP initialize result")
		}
	}
	var out protocol.InitializeResult
	if err := jsonx.Decode(obj, &out); err != nil {
		return nil, clientInvalid("Invalid MCP initialize result")
	}
	return &out, nil
}

func clientInvalid(message string) *protocol.McpError {
	return protocol.NewMcpError(protocol.JsonRpcErrorCodes.InvalidRequest, message, nil)
}

func clientValidateListPage(method, key string, value any, isItem func(item *jsonx.Object) bool) (*clientListPageResult, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, clientInvalid(fmt.Sprintf("Invalid MCP %s result", method))
	}
	itemsVal, ok := obj.Get(key)
	if !ok {
		return nil, clientInvalid(fmt.Sprintf("Invalid MCP %s result", method))
	}
	arr, ok := itemsVal.([]any)
	if !ok || arr == nil {
		return nil, clientInvalid(fmt.Sprintf("Invalid MCP %s result", method))
	}
	items := make([]*jsonx.Object, 0, len(arr))
	for _, rawItem := range arr {
		itemObj, ok := rawItem.(*jsonx.Object)
		if !ok || itemObj == nil || !isItem(itemObj) {
			return nil, clientInvalid(fmt.Sprintf("Invalid entry in MCP %s result", method))
		}
		items = append(items, itemObj)
	}
	var nextCursor *string
	if ncVal, hasNc := obj.Get("nextCursor"); hasNc && ncVal != nil {
		ncStr, ok := ncVal.(string)
		if !ok {
			return nil, clientInvalid(fmt.Sprintf("Invalid MCP %s cursor", method))
		}
		if ncStr != "" {
			nextCursor = &ncStr
		}
	}
	return &clientListPageResult{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func clientIsTool(tool *jsonx.Object) bool {
	if tool == nil {
		return false
	}
	nameVal, _ := tool.Get("name")
	schemaVal, _ := tool.Get("inputSchema")
	_, nameOk := nameVal.(string)
	return nameOk && protocol.IsObject(schemaVal)
}

func clientIsPrompt(prompt *jsonx.Object) bool {
	if prompt == nil {
		return false
	}
	nameVal, _ := prompt.Get("name")
	_, nameOk := nameVal.(string)
	return nameOk
}

func clientIsResource(resource *jsonx.Object) bool {
	if resource == nil {
		return false
	}
	uriVal, _ := resource.Get("uri")
	if _, ok := uriVal.(string); !ok {
		return false
	}
	if nameVal, hasName := resource.Get("name"); hasName {
		if _, ok := nameVal.(string); !ok {
			return false
		}
	}
	return true
}

func clientIsResourceTemplate(template *jsonx.Object) bool {
	if template == nil {
		return false
	}
	uriVal, _ := template.Get("uriTemplate")
	if _, ok := uriVal.(string); !ok {
		return false
	}
	if nameVal, hasName := template.Get("name"); hasName {
		if _, ok := nameVal.(string); !ok {
			return false
		}
	}
	return true
}

func clientToResource(item *jsonx.Object) *protocol.Resource {
	var res protocol.Resource
	_ = jsonx.Decode(item, &res)
	if nameVal, hasName := item.Get("name"); !hasName || nameVal == nil {
		res.Name = res.Uri
	}
	return &res
}

func clientToResourceTemplate(item *jsonx.Object) *protocol.ResourceTemplate {
	var tmpl protocol.ResourceTemplate
	_ = jsonx.Decode(item, &tmpl)
	if nameVal, hasName := item.Get("name"); !hasName || nameVal == nil {
		tmpl.Name = tmpl.UriTemplate
	}
	return &tmpl
}

func clientPageCursor(page *clientListPageResult) *string {
	if page == nil {
		return nil
	}
	return page.NextCursor
}

func clientValidateReadResourceResult(value any) (*protocol.ReadResourceResult, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, clientInvalid("Invalid MCP resources/read result")
	}
	contentsVal, ok := obj.Get("contents")
	if !ok {
		return nil, clientInvalid("Invalid MCP resources/read result")
	}
	arr, ok := contentsVal.([]any)
	if !ok || arr == nil {
		return nil, clientInvalid("Invalid MCP resources/read result")
	}
	for _, entry := range arr {
		eObj, ok := entry.(*jsonx.Object)
		if !ok || eObj == nil {
			return nil, clientInvalid("Invalid contents in MCP resources/read result")
		}
		uriVal, _ := eObj.Get("uri")
		if _, ok := uriVal.(string); !ok {
			return nil, clientInvalid("Invalid contents in MCP resources/read result")
		}
		textVal, _ := eObj.Get("text")
		blobVal, _ := eObj.Get("blob")
		_, textOk := textVal.(string)
		_, blobOk := blobVal.(string)
		if !textOk && !blobOk {
			return nil, clientInvalid("Invalid contents in MCP resources/read result")
		}
	}
	var out protocol.ReadResourceResult
	if err := jsonx.Decode(obj, &out); err != nil {
		return nil, clientInvalid("Invalid MCP resources/read result")
	}
	return &out, nil
}

func clientValidateGetPromptResult(value any) (*protocol.GetPromptResult, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, clientInvalid("Invalid MCP prompts/get result")
	}
	msgsVal, ok := obj.Get("messages")
	if !ok {
		return nil, clientInvalid("Invalid MCP prompts/get result")
	}
	if _, ok := msgsVal.([]any); !ok {
		return nil, clientInvalid("Invalid MCP prompts/get result")
	}
	var out protocol.GetPromptResult
	if err := jsonx.Decode(obj, &out); err != nil {
		return nil, clientInvalid("Invalid MCP prompts/get result")
	}
	return &out, nil
}

func clientValidateCallToolResult(value any) (*protocol.CallToolResult, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, clientInvalid("Invalid MCP tools/call result")
	}
	contentVal, hasContent := obj.Get("content")
	if hasContent {
		if _, ok := contentVal.([]any); !ok {
			return nil, clientInvalid("Invalid MCP tools/call result")
		}
	}
	var out protocol.CallToolResult
	if err := jsonx.Decode(obj, &out); err != nil {
		return nil, clientInvalid("Invalid MCP tools/call result")
	}
	if out.Content == nil {
		out.Content = protocol.ContentBlockList{}
	}
	return &out, nil
}

func (c *McpClient) listPage(ctx context.Context, method, key string, isItem func(item *jsonx.Object) bool, cursor *string, options *McpRequestOptions) (*clientListPageResult, error) {
	var params *jsonx.Object
	if cursor != nil {
		params = jsonx.ObjectOf("cursor", *cursor)
	}
	res, err := c.Request(ctx, method, params, options)
	if err != nil {
		return nil, err
	}
	return clientValidateListPage(method, key, res, isItem)
}

func (c *McpClient) listAll(ctx context.Context, method, key string, isItem func(item *jsonx.Object) bool, options *McpRequestOptions) ([]*jsonx.Object, error) {
	var items []*jsonx.Object
	cursors := omap.NewSet[string]()
	var cursor *string
	for pageNumber := 0; pageNumber < clientMaxListPages; pageNumber++ {
		page, err := c.listPage(ctx, method, key, isItem, cursor, options)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.NextCursor == nil {
			return items, nil
		}
		if cursors.Has(*page.NextCursor) {
			return nil, fmt.Errorf("MCP %s returned duplicate cursor: %s", method, *page.NextCursor)
		}
		cursors.Add(*page.NextCursor)
		nc := *page.NextCursor
		cursor = &nc
	}
	return nil, fmt.Errorf("MCP %s exceeded %d pages", method, clientMaxListPages)
}

func (c *McpClient) requestInternal(ctx context.Context, method string, params *jsonx.Object, options *McpRequestOptions, allowConnecting bool) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, protocol.NewMcpAbortError()
	}

	c.mu.Lock()
	transport, err := c.requireTransportLocked(allowConnecting)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	id := protocol.JsonRpcId{Number: float64(c.nextRequestId)}
	c.nextRequestId++

	timeoutMs := clientDefaultRequestTimeoutMs
	if c.Options.RequestTimeoutMs != nil {
		timeoutMs = *c.Options.RequestTimeoutMs
	}
	var onProgress func(*protocol.ProgressNotification)
	if options != nil {
		if options.TimeoutMs != nil {
			timeoutMs = *options.TimeoutMs
		}
		onProgress = options.OnProgress
	}

	var progressToken *protocol.JsonRpcId
	requestParams := params
	if onProgress != nil {
		pt := id
		progressToken = &pt
		cloned := jsonx.NewObject()
		if params != nil {
			for k, v := range params.All() {
				cloned.Set(k, v)
			}
		}
		meta := jsonx.NewObject()
		if params != nil {
			if existingMeta, hasMeta := params.Get("_meta"); hasMeta {
				if metaObj, ok := existingMeta.(*jsonx.Object); ok && metaObj != nil {
					for k, v := range metaObj.All() {
						meta.Set(k, v)
					}
				}
			}
		}
		meta.Set("progressToken", pt)
		cloned.Set("_meta", meta)
		requestParams = cloned
	}

	var rawParams any
	if requestParams != nil {
		rawParams = requestParams
	}
	message := &protocol.JsonRpcRequest{
		Jsonrpc: "2.0",
		Id:      id,
		Method:  method,
		Params:  rawParams,
	}

	type responseOutcome struct {
		val any
		err error
	}
	waitCh := make(chan responseOutcome, 1)
	sendCtx, sendCancel := context.WithCancel(context.Background())

	entry := &clientPendingRequest{
		resolve: func(value any) {
			waitCh <- responseOutcome{val: value}
		},
		reject: func(reason error) {
			waitCh <- responseOutcome{err: reason}
		},
		timeoutMs:     timeoutMs,
		sendCancel:    sendCancel,
		cancellable:   method != "initialize",
		onProgress:    onProgress,
		progressToken: progressToken,
	}

	c.pending.Set(id, entry)
	if progressToken != nil {
		c.progressRequests.Set(*progressToken, id)
	}
	c.armTimeout(id, entry)

	cancellable := entry.cancellable
	stopAbort := context.AfterFunc(ctx, func() {
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		var reason string
		switch {
		case cause == nil:
			reason = "Aborted"
		case cause == context.Canceled || cause == context.DeadlineExceeded:
			reason = js.ErrorString(cause)
		default:
			reason = cause.Error()
		}
		c.cancelPending(id, protocol.NewMcpAbortError(), cancellable, &reason)
	})
	entry.onAbort = func() {
		_ = stopAbort()
	}
	c.mu.Unlock()

	go func() {
		var sendErr error
		if sc, ok := transport.(McpTransportSendContext); ok {
			sendErr = sc.SendWithContext(sendCtx, message)
		} else {
			sendErr = transport.Send(message)
		}
		if sendErr != nil {
			c.cancelPending(id, sendErr, false, nil)
		}
	}()

	res := <-waitCh
	return res.val, res.err
}

func (c *McpClient) notifyInternal(method string, params *jsonx.Object, allowConnecting bool) error {
	transport, err := c.requireTransport(allowConnecting)
	if err != nil {
		return err
	}
	var rawParams any
	if params != nil {
		rawParams = params
	}
	return transport.Send(&protocol.JsonRpcNotification{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  rawParams,
	})
}

func (c *McpClient) requireTransport(allowConnecting bool) (McpTransport, error) {
	c.mu.Lock()
	t, err := c.requireTransportLocked(allowConnecting)
	c.mu.Unlock()
	return t, err
}

func (c *McpClient) requireTransportLocked(allowConnecting bool) (McpTransport, error) {
	if c.transport != nil && (c.state == ClientStateConnected || (allowConnecting && c.state == ClientStateConnecting)) {
		return c.transport, nil
	}
	return nil, protocol.NewMcpConnectionClosedError(fmt.Sprintf("MCP client is %s", c.state))
}

func (c *McpClient) handleMessage(message protocol.JsonRpcMessage) {
	switch m := message.(type) {
	case *protocol.JsonRpcSuccessResponse:
		c.handleResponse(m)
	case *protocol.JsonRpcErrorResponse:
		c.handleResponse(m)
	case *protocol.JsonRpcRequest:
		go func() { _ = c.handleRequest(m) }()
	case *protocol.JsonRpcNotification:
		c.handleNotification(m.Method, m.Params)
	default:
		c.emitError(protocol.NewMcpError(protocol.JsonRpcErrorCodes.InvalidRequest, "Received invalid JSON-RPC message", nil))
	}
}

func (c *McpClient) handleResponse(message protocol.JsonRpcResponse) {
	var id protocol.JsonRpcId
	switch r := message.(type) {
	case *protocol.JsonRpcSuccessResponse:
		id = r.Id
	case *protocol.JsonRpcErrorResponse:
		id = r.Id
	default:
		c.emitError(protocol.NewMcpError(protocol.JsonRpcErrorCodes.InvalidRequest, "Received invalid JSON-RPC message", nil))
		return
	}

	c.mu.Lock()
	entry, ok := c.pending.Get(id)
	if !ok || entry == nil || entry.settled {
		c.mu.Unlock()
		idText := js.NumberToString(id.Number)
		if id.IsString {
			idText = id.String
		}
		c.emitError(fmt.Errorf("Received response for unknown MCP request %s", idText))
		return
	}
	c.removePendingLocked(id, entry)
	entry.settled = true
	reject := entry.reject
	resolve := entry.resolve
	c.mu.Unlock()

	switch r := message.(type) {
	case *protocol.JsonRpcErrorResponse:
		reject(protocol.NewMcpError(r.Error.Code, r.Error.Message, r.Error.Data))
	case *protocol.JsonRpcSuccessResponse:
		resolve(r.Result)
	}
}

func (c *McpClient) handleRequest(message *protocol.JsonRpcRequest) error {
	c.mu.Lock()
	transport := c.transport
	handlerEntry, hasHandler := c.requestHandlers[message.Method]
	c.mu.Unlock()

	if transport == nil {
		return nil
	}
	if !hasHandler {
		err := transport.Send(&protocol.JsonRpcErrorResponse{
			Jsonrpc: "2.0",
			Id:      message.Id,
			Error: protocol.JsonRpcErrorObject{
				Code:    protocol.JsonRpcErrorCodes.MethodNotFound,
				Message: "Method not found: " + message.Method,
			},
		})
		if err != nil {
			c.emitError(err)
		}
		return nil
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.mu.Lock()
	if c.state == ClientStateClosed {
		c.mu.Unlock()
		return nil
	}
	c.incoming.Set(message.Id, cancel)
	c.mu.Unlock()

	result, err := handlerEntry.fn(reqCtx, message.Params)

	c.mu.Lock()
	c.incoming.Delete(message.Id)
	c.mu.Unlock()

	if err != nil {
		var mcpErr *protocol.McpError
		errObj := protocol.JsonRpcErrorObject{
			Code:    protocol.JsonRpcErrorCodes.InternalError,
			Message: err.Error(),
		}
		if errors.As(err, &mcpErr) {
			errObj = protocol.JsonRpcErrorObject{
				Code:    mcpErr.Code,
				Message: mcpErr.Message,
				Data:    mcpErr.Data,
			}
		}
		if sendErr := transport.Send(&protocol.JsonRpcErrorResponse{
			Jsonrpc: "2.0",
			Id:      message.Id,
			Error:   errObj,
		}); sendErr != nil {
			c.emitError(sendErr)
		}
		return nil
	}

	if result == nil {
		result = jsonx.ObjectOf()
	}
	if sendErr := transport.Send(&protocol.JsonRpcSuccessResponse{
		Jsonrpc: "2.0",
		Id:      message.Id,
		Result:  result,
	}); sendErr != nil {
		c.emitError(sendErr)
	}
	return nil
}

func (c *McpClient) handleNotification(method string, params any) {
	if method == "notifications/progress" {
		c.handleProgress(params)
	} else if method == "notifications/cancelled" {
		c.handleCancelled(params)
	}

	c.mu.Lock()
	listeners := append([]clientNotificationListenerEntry(nil), c.notificationListeners[method]...)
	c.mu.Unlock()

	for _, l := range listeners {
		l.fn(params)
	}
}

func (c *McpClient) handleProgress(params any) {
	obj, ok := params.(*jsonx.Object)
	if !ok || obj == nil {
		return
	}
	ptVal, hasPt := obj.Get("progressToken")
	progVal, hasProg := obj.Get("progress")
	if !hasPt || !hasProg || !protocol.IsJsonRpcId(ptVal) {
		return
	}
	progNum, ok := progVal.(float64)
	if !ok || math.IsNaN(progNum) || math.IsInf(progNum, 0) {
		return
	}
	var progNotif protocol.ProgressNotification
	if err := jsonx.Decode(obj, &progNotif); err != nil {
		return
	}

	c.mu.Lock()
	requestId, ok := c.progressRequests.Get(progNotif.ProgressToken)
	if !ok {
		c.mu.Unlock()
		return
	}
	entry, ok := c.pending.Get(requestId)
	if !ok || entry == nil || entry.settled {
		c.mu.Unlock()
		return
	}
	c.armTimeout(requestId, entry)
	onProgress := entry.onProgress
	c.mu.Unlock()

	if onProgress != nil {
		onProgress(&progNotif)
	}
}

func (c *McpClient) handleCancelled(params any) {
	obj, ok := params.(*jsonx.Object)
	if !ok || obj == nil {
		return
	}
	idVal, hasId := obj.Get("requestId")
	if !hasId || !protocol.IsJsonRpcId(idVal) {
		return
	}
	var cancelled protocol.CancelledNotification
	if err := jsonx.Decode(obj, &cancelled); err != nil {
		return
	}

	c.mu.Lock()
	cancelFn, ok := c.incoming.Get(cancelled.RequestId)
	c.mu.Unlock()
	if ok && cancelFn != nil {
		cancelFn()
	}
}

// armTimeout schedules or resets the timeout timer. Caller holds c.mu.
func (c *McpClient) armTimeout(id protocol.JsonRpcId, entry *clientPendingRequest) {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	if entry.timeoutMs <= 0 {
		return
	}
	timeoutMs := entry.timeoutMs
	cancellable := entry.cancellable
	entry.timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		reason := "Request timed out"
		c.cancelPending(id, protocol.NewMcpTimeoutError(timeoutMs), cancellable, &reason)
	})
}

func (c *McpClient) cancelPending(id protocol.JsonRpcId, err error, notifyServer bool, reason *string) {
	c.mu.Lock()
	entry, ok := c.pending.Get(id)
	if !ok || entry == nil || entry.settled {
		c.mu.Unlock()
		return
	}
	c.removePendingLocked(id, entry)
	entry.settled = true
	reject := entry.reject
	transport := c.transport
	c.mu.Unlock()

	reject(err)

	if notifyServer && transport != nil {
		params := jsonx.ObjectOf("requestId", id)
		if reason != nil && *reason != "" {
			params.Set("reason", *reason)
		}
		go func() {
			if sendErr := transport.Send(&protocol.JsonRpcNotification{
				Jsonrpc: "2.0",
				Method:  "notifications/cancelled",
				Params:  params,
			}); sendErr != nil {
				c.emitError(sendErr)
			}
		}()
	}
}

func (c *McpClient) removePending(id protocol.JsonRpcId, entry *clientPendingRequest) {
	c.mu.Lock()
	c.removePendingLocked(id, entry)
	c.mu.Unlock()
}

func (c *McpClient) removePendingLocked(id protocol.JsonRpcId, entry *clientPendingRequest) {
	c.pending.Delete(id)
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	if entry.progressToken != nil {
		c.progressRequests.Delete(*entry.progressToken)
	}
	if entry.onAbort != nil {
		entry.onAbort()
	}
	if entry.sendCancel != nil {
		entry.sendCancel()
	}
}

func (c *McpClient) rejectPending(err error) {
	c.mu.Lock()
	var rejects []func(error)
	for id, entry := range c.pending.All() {
		if entry != nil && !entry.settled {
			c.removePendingLocked(id, entry)
			entry.settled = true
			rejects = append(rejects, entry.reject)
		}
	}
	c.pending = omap.NewMap[protocol.JsonRpcId, *clientPendingRequest]()
	c.progressRequests = omap.NewMap[protocol.JsonRpcId, protocol.JsonRpcId]()
	c.mu.Unlock()

	for _, r := range rejects {
		r(err)
	}
}

func (c *McpClient) handleTransportClose() {
	c.markClosed(protocol.NewMcpConnectionClosedError())
}

func (c *McpClient) markClosed(err error) {
	c.mu.Lock()
	wasClosed := c.state == ClientStateClosed
	c.state = ClientStateClosed

	var rejects []func(error)
	for id, entry := range c.pending.All() {
		if entry != nil && !entry.settled {
			c.removePendingLocked(id, entry)
			entry.settled = true
			rejects = append(rejects, entry.reject)
		}
	}
	c.pending = omap.NewMap[protocol.JsonRpcId, *clientPendingRequest]()
	c.progressRequests = omap.NewMap[protocol.JsonRpcId, protocol.JsonRpcId]()

	var cancels []context.CancelFunc
	for _, cancelFn := range c.incoming.All() {
		if cancelFn != nil {
			cancels = append(cancels, cancelFn)
		}
	}
	c.incoming = omap.NewMap[protocol.JsonRpcId, context.CancelFunc]()

	var closeListeners []clientCloseListenerEntry
	if !wasClosed {
		closeListeners = append([]clientCloseListenerEntry(nil), c.closeListeners...)
	}
	c.mu.Unlock()

	for _, r := range rejects {
		r(err)
	}
	for _, cancelFn := range cancels {
		cancelFn()
	}
	for _, l := range closeListeners {
		l.fn()
	}
}

func (c *McpClient) emitError(err any) {
	normalized := protocol.ToError(err)
	c.mu.Lock()
	listeners := append([]clientErrorListenerEntry(nil), c.errorListeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		l.fn(normalized)
	}
}

func (c *McpClient) disposeTransportListeners() {
	c.mu.Lock()
	disposers := c.disposers
	c.disposers = nil
	c.mu.Unlock()
	for _, d := range disposers {
		d()
	}
}
