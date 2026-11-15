// Ported from packages/coding-agent/src/extensions/mcp/runtime.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

const (
	runtDefaultTimeoutSeconds = 60
	runtStderrTailChars       = 2_000
)

// runtConnectRetryDelays are the delays between attempts to connect to an HTTP server
// that failed with a transient error.
var runtConnectRetryDelays = []time.Duration{250 * time.Millisecond, 1_000 * time.Millisecond}

// ServerState describes the connection lifecycle state of an MCP server.
// `disconnected` means the connection dropped (e.g. stdio exited); the next call reconnects.
type ServerState string

const (
	ServerStateConnecting   ServerState = "connecting"
	ServerStateConnected    ServerState = "connected"
	ServerStateDisconnected ServerState = "disconnected"
	ServerStateNeedsAuth    ServerState = "needs-auth"
	ServerStateFailed       ServerState = "failed"
	ServerStateClosed       ServerState = "closed"
)

// McpTransportFactory builds an MCP transport for a configured server entry.
type McpTransportFactory func(entry McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.McpTransport, error)

func runtErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runtIsTransientError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *mcp.McpHttpError
	if errors.As(err, &httpErr) && httpErr != nil {
		return httpErr.Status == 408 || httpErr.Status == 429 || (httpErr.Status >= 500 && httpErr.Status != 501)
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var urlErr *url.Error
	var opErr *net.OpError
	return errors.As(err, &urlErr) || errors.As(err, &opErr)
}

func runtSignInRequiredMessage(entry McpServerEntry) string {
	var provider string
	if entry.Config.IsHttp() && entry.Config.Auth != nil {
		provider = entry.Config.Auth.Provider
	}
	action := "/mcp"
	if provider != "" {
		action = "/login " + provider
	}
	return fmt.Sprintf("MCP server %q requires sign-in. Run %s to sign in.", entry.Name, action)
}

func runtUsesOAuth(entry McpServerEntry) bool {
	cfg := entry.Config
	if !cfg.IsHttp() || cfg.Auth != nil {
		return false
	}
	if cfg.Headers != nil {
		for k := range cfg.Headers.All() {
			if strings.EqualFold(k, "authorization") {
				return false
			}
		}
	}
	return true
}

func runtExpandHome(value string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, `~\`)) {
		return filepath.Join(home, value[2:])
	}
	return value
}

func runtResolvePath(base, target string) string {
	expanded := runtExpandHome(target)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(filepath.Join(base, expanded))
}

func runtPathToFileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	slash := filepath.ToSlash(path)
	if len(slash) >= 2 && slash[1] == ':' && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

// CreateDefaultTransport constructs the default StreamableHttpTransport or StdioTransport for an entry.
func CreateDefaultTransport(
	entry McpServerEntry,
	cwd string,
	authProvider mcp.AuthProvider,
	httpClient ...*http.Client,
) (mcp.McpTransport, error) {
	cfg := entry.Config
	if cfg.IsHttp() {
		var resolvedHeaders *omap.Map[string, string]
		if cfg.Headers != nil {
			var err error
			resolvedHeaders, err = core.ResolveHeadersOrThrow(cfg.Headers, fmt.Sprintf("MCP server %q", entry.Name))
			if err != nil {
				return nil, err
			}
		}
		client := ai.NodeHttpProxyDefaultClient()
		if len(httpClient) > 0 && httpClient[0] != nil {
			client = httpClient[0]
		}
		return mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
			Url:          *cfg.Url,
			Headers:      resolvedHeaders,
			AuthProvider: authProvider,
			HTTPClient:   client,
		})
	}

	var env *omap.Map[string, string]
	if cfg.Env != nil {
		env = omap.NewMap[string, string]()
		for key, value := range cfg.Env.All() {
			resolved, err := core.ResolveConfigValueOrThrow(value, fmt.Sprintf("MCP server %q env %q", entry.Name, key))
			if err != nil {
				return nil, err
			}
			env.Set(key, resolved)
		}
	}
	args := make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		args[i] = runtExpandHome(arg)
	}
	subCwd := "."
	if cfg.Cwd != nil && *cfg.Cwd != "" {
		subCwd = *cfg.Cwd
	}
	resolvedCwd := runtResolvePath(cwd, subCwd)
	var cmd string
	if cfg.Command != nil {
		cmd = *cfg.Command
	}
	stderr := mcp.StdioTransportStderrPipe
	return mcp.NewStdioTransport(&mcp.StdioTransportOptions{
		Command: runtExpandHome(cmd),
		Args:    args,
		Cwd:     &resolvedCwd,
		Env:     env,
		Stderr:  &stderr,
	}), nil
}

func runtWithoutTemplates[T any](list func() (T, error), empty T) (T, error) {
	res, err := list()
	if err == nil {
		return res, nil
	}
	var mcpErr *mcpprotocol.McpError
	if errors.As(err, &mcpErr) && mcpErr != nil && mcpErr.Code == mcpprotocol.JsonRpcErrorCodes.MethodNotFound {
		return empty, nil
	}
	var zero T
	return zero, err
}

type runtProviderAuthAdapter struct {
	provider      string
	providerToken func(ctx context.Context, provider string) (*string, error)
}

func (a *runtProviderAuthAdapter) Token() (*string, error) {
	if a.providerToken == nil {
		return nil, nil
	}
	return a.providerToken(context.Background(), a.provider)
}

type runtOpenCall struct {
	done   chan struct{}
	client *mcp.McpClient
	err    error
}

// McpServerConnectionOptions configures a McpServerConnection.
type McpServerConnectionOptions struct {
	Entry           McpServerEntry
	Cwd             string
	CreateTransport McpTransportFactory
	Credentials     *McpOAuthCredentialStore
	HTTPClient      *http.Client
	ProviderToken   func(ctx context.Context, provider string) (*string, error)
	OnTools         func(connection *McpServerConnection)
	OnChange        func(connection *McpServerConnection)
	Log             *McpServerLog
}

// McpServerConnection manages one configured MCP server and lazily reconnects on dropped connections.
type McpServerConnection struct {
	Entry McpServerEntry

	mu             sync.Mutex
	state          ServerState
	errMsg         *string
	tools          []mcpprotocol.Tool
	hasResources   bool
	instructions   *string
	challenge      *mcpoauth.OAuthChallenge
	client         *mcp.McpClient
	opening        *runtOpenCall
	stderrTail     *string
	closed         bool
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	bgWg           sync.WaitGroup

	cwd             string
	httpClient      *http.Client
	createTransport McpTransportFactory
	oauthProvider   mcpoauth.McpAuthProvider
	providerAuth    mcp.AuthProvider
	onTools         func(connection *McpServerConnection)
	onChange        func(connection *McpServerConnection)
	log             *McpServerLog
}

// NewMcpServerConnection constructs an McpServerConnection in the initial connecting state.
func NewMcpServerConnection(options McpServerConnectionOptions) *McpServerConnection {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = ai.NodeHttpProxyDefaultClient()
	}
	createTransport := options.CreateTransport
	if createTransport == nil {
		createTransport = func(entry McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.McpTransport, error) {
			return CreateDefaultTransport(entry, cwd, authProvider, httpClient)
		}
	}
	conn := &McpServerConnection{
		Entry:           options.Entry,
		state:           ServerStateConnecting,
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
		cwd:             options.Cwd,
		httpClient:      httpClient,
		createTransport: createTransport,
		onTools:         options.OnTools,
		onChange:        options.OnChange,
		log:             options.Log,
	}

	var providerName string
	if options.Entry.Config.IsHttp() && options.Entry.Config.Auth != nil {
		providerName = options.Entry.Config.Auth.Provider
	}
	if oauthUrl := conn.OAuthUrl(); oauthUrl != nil && options.Credentials != nil {
		conn.oauthProvider = CreateMcpAuthProvider(CreateMcpAuthProviderOptions{
			ServerUrl:  *oauthUrl,
			Store:      options.Credentials.ForServer(options.Entry.Name, *oauthUrl),
			HTTPClient: httpClient,
			Settings: func() mcpoauth.McpOAuthSettings {
				s, _ := conn.OAuthSettings()
				return s
			},
			OnChallenge: func(challenge mcpoauth.OAuthChallenge) {
				conn.mu.Lock()
				c := challenge
				conn.challenge = &c
				conn.mu.Unlock()
			},
		})
	} else if providerName != "" {
		conn.providerAuth = &runtProviderAuthAdapter{
			provider:      providerName,
			providerToken: options.ProviderToken,
		}
	}
	return conn
}

func (c *McpServerConnection) Name() string {
	return c.Entry.Name
}

func (c *McpServerConnection) State() ServerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *McpServerConnection) Error() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.errMsg == nil {
		return nil
	}
	s := *c.errMsg
	return &s
}

func (c *McpServerConnection) Tools() []mcpprotocol.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]mcpprotocol.Tool, len(c.tools))
	copy(out, c.tools)
	return out
}

func (c *McpServerConnection) HasResources() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasResources
}

func (c *McpServerConnection) Instructions() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.instructions == nil {
		return nil
	}
	s := *c.instructions
	return &s
}

func (c *McpServerConnection) Challenge() *mcpoauth.OAuthChallenge {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.challenge == nil {
		return nil
	}
	ch := *c.challenge
	return &ch
}

// ClearChallenge clears any cached OAuth challenge before reconnecting after sign-in.
func (c *McpServerConnection) ClearChallenge() {
	c.mu.Lock()
	c.challenge = nil
	c.mu.Unlock()
}

func (c *McpServerConnection) TimeoutMs() int64 {
	if c.Entry.Config.Timeout != nil {
		return int64(*c.Entry.Config.Timeout * 1000)
	}
	return int64(runtDefaultTimeoutSeconds * 1000)
}

// OAuthUrl returns the server URL when it authenticates using OAuth.
func (c *McpServerConnection) OAuthUrl() *string {
	if runtUsesOAuth(c.Entry) && c.Entry.Config.IsHttp() && c.Entry.Config.Url != nil {
		s := *c.Entry.Config.Url
		return &s
	}
	return nil
}

func runtNonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// OAuthSettings resolves the server's OAuth settings (including secret config values).
func (c *McpServerConnection) OAuthSettings() (mcpoauth.McpOAuthSettings, error) {
	if !c.Entry.Config.IsHttp() || c.Entry.Config.OAuth == nil {
		return mcpoauth.McpOAuthSettings{}, nil
	}
	oauth := c.Entry.Config.OAuth
	var clientSecret *string
	if oauth.ClientSecret != "" {
		resolved, err := core.ResolveConfigValueOrThrow(
			oauth.ClientSecret,
			fmt.Sprintf("MCP server %q oauth.clientSecret", c.Entry.Name),
		)
		if err != nil {
			return mcpoauth.McpOAuthSettings{}, err
		}
		clientSecret = &resolved
	}
	var authMetaUrl *url.URL
	if oauth.AuthServerMetadataUrl != "" {
		parsed, err := url.Parse(oauth.AuthServerMetadataUrl)
		if err != nil || !parsed.IsAbs() {
			return mcpoauth.McpOAuthSettings{}, fmt.Errorf("MCP server %q oauth.authServerMetadataUrl is invalid", c.Entry.Name)
		}
		authMetaUrl = parsed
	}
	return mcpoauth.McpOAuthSettings{
		ClientId:              runtNonEmptyPtr(oauth.ClientId),
		ClientSecret:          clientSecret,
		CallbackPort:          oauth.CallbackPort,
		CallbackUrl:           runtNonEmptyPtr(oauth.CallbackUrl),
		Scope:                 runtNonEmptyPtr(oauth.Scope),
		ClientName:            runtNonEmptyPtr(oauth.ClientName),
		ClientRegistration:    oauth.ClientRegistration,
		AuthServerMetadataUrl: authMetaUrl,
	}, nil
}

func (c *McpServerConnection) rawAuthProvider() mcp.AuthProvider {
	if c.oauthProvider != nil {
		return c.oauthProvider
	}
	return c.providerAuth
}

func (c *McpServerConnection) hasAuthProvider() bool {
	return c.oauthProvider != nil || c.providerAuth != nil
}

// GetClient returns an active connected client, opening or reconnecting lazily.
func (c *McpServerConnection) GetClient(ctx context.Context) (*mcp.McpClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("MCP server %q is shut down", c.Entry.Name)
	}
	if c.client != nil && c.client.ConnectionState() == mcp.ClientStateConnected {
		client := c.client
		c.mu.Unlock()
		return client, nil
	}
	call := c.opening
	if call == nil {
		call = &runtOpenCall{done: make(chan struct{})}
		c.opening = call
		c.bgWg.Add(1)
		go func() {
			defer c.bgWg.Done()
			client, err := c.open()
			call.client = client
			call.err = err
			c.mu.Lock()
			if c.opening == call {
				c.opening = nil
			}
			c.mu.Unlock()
			close(call.done)
		}()
	}
	c.mu.Unlock()

	select {
	case <-call.done:
		return call.client, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *McpServerConnection) CallTool(
	ctx context.Context,
	name string,
	args *jsonx.Object,
	options *mcp.McpRequestOptions,
) (*mcpprotocol.CallToolResult, error) {
	return runtWithClient(c, ctx, false, func(client *mcp.McpClient) (*mcpprotocol.CallToolResult, error) {
		return client.CallTool(ctx, name, args, options)
	})
}

func (c *McpServerConnection) ReadResource(
	ctx context.Context,
	uri string,
	options *mcp.McpRequestOptions,
) (*mcpprotocol.ReadResourceResult, error) {
	return runtWithClient(c, ctx, true, func(client *mcp.McpClient) (*mcpprotocol.ReadResourceResult, error) {
		return client.ReadResource(ctx, uri, options)
	})
}

func (c *McpServerConnection) ResourcesPage(
	ctx context.Context,
	cursor *string,
	options *mcp.McpRequestOptions,
) (*mcpprotocol.ListResourcesResult, error) {
	return runtWithClient(c, ctx, true, func(client *mcp.McpClient) (*mcpprotocol.ListResourcesResult, error) {
		return client.ListResourcesPage(ctx, cursor, options)
	})
}

func (c *McpServerConnection) ResourceTemplatesPage(
	ctx context.Context,
	cursor *string,
	options *mcp.McpRequestOptions,
) (*mcpprotocol.ListResourceTemplatesResult, error) {
	return runtWithClient(c, ctx, true, func(client *mcp.McpClient) (*mcpprotocol.ListResourceTemplatesResult, error) {
		return runtWithoutTemplates(func() (*mcpprotocol.ListResourceTemplatesResult, error) {
			return client.ListResourceTemplatesPage(ctx, cursor, options)
		}, &mcpprotocol.ListResourceTemplatesResult{ResourceTemplates: []mcpprotocol.ResourceTemplate{}})
	})
}

func (c *McpServerConnection) AllResources(
	ctx context.Context,
	options *mcp.McpRequestOptions,
) ([]mcpprotocol.Resource, error) {
	return runtWithClient(c, ctx, true, func(client *mcp.McpClient) ([]mcpprotocol.Resource, error) {
		return client.ListResources(ctx, options)
	})
}

func (c *McpServerConnection) AllResourceTemplates(
	ctx context.Context,
	options *mcp.McpRequestOptions,
) ([]mcpprotocol.ResourceTemplate, error) {
	return runtWithClient(c, ctx, true, func(client *mcp.McpClient) ([]mcpprotocol.ResourceTemplate, error) {
		return runtWithoutTemplates(func() ([]mcpprotocol.ResourceTemplate, error) {
			return client.ListResourceTemplates(ctx, options)
		}, []mcpprotocol.ResourceTemplate{})
	})
}

func runtWithClient[T any](
	c *McpServerConnection,
	ctx context.Context,
	readOnly bool,
	run func(client *mcp.McpClient) (T, error),
) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var zero T
	for attempt := 1; ; attempt++ {
		client, err := c.GetClient(ctx)
		if err != nil {
			return zero, err
		}
		res, err := run(client)
		if err == nil {
			return res, nil
		}
		var httpErr *mcp.McpHttpError
		if readOnly && attempt == 1 && errors.As(err, &httpErr) && runtIsTransientError(err) {
			if sleepErr := runtSleep(ctx, c.shutdownCtx, runtConnectRetryDelays[0]); sleepErr != nil {
				return zero, sleepErr
			}
			continue
		}
		var expired *mcp.McpSessionExpiredError
		if errors.As(err, &expired) && attempt == 1 {
			c.mu.Lock()
			if c.client == client {
				c.client = nil
			}
			c.mu.Unlock()
			continue
		}
		if !c.needsSignIn(err) {
			return zero, err
		}
		c.dropClient(client)
		c.markNeedsAuth()
		return zero, errors.New(runtSignInRequiredMessage(c.Entry))
	}
}

// Reconnect drops any existing connection and establishes a fresh one with current credentials.
func (c *McpServerConnection) Reconnect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	opening := c.opening
	c.mu.Unlock()
	if opening != nil {
		select {
		case <-opening.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client != nil {
		c.dropClient(client)
	}
	_, err := c.GetClient(ctx)
	return err
}

// SignOut closes any active connection and resets state to needs-auth unless already closed.
func (c *McpServerConnection) SignOut(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	opening := c.opening
	c.mu.Unlock()
	if opening != nil {
		select {
		case <-opening.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client != nil {
		c.dropClient(client)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		c.markNeedsAuth()
	}
	return nil
}

func (c *McpServerConnection) needsSignIn(err error) bool {
	if err == nil {
		return false
	}
	var oauthErr *mcpoauth.McpOAuthAuthorizationRequiredError
	if errors.As(err, &oauthErr) {
		return true
	}
	if c.hasAuthProvider() {
		var authReq *mcp.McpAuthRequiredError
		if errors.As(err, &authReq) {
			return true
		}
	}
	return false
}

func (c *McpServerConnection) markNeedsAuth() {
	c.mu.Lock()
	c.state = ServerStateNeedsAuth
	c.errMsg = nil
	cb := c.onChange
	c.mu.Unlock()
	if cb != nil {
		cb(c)
	}
}

func (c *McpServerConnection) notifyChange() {
	c.mu.Lock()
	cb := c.onChange
	c.mu.Unlock()
	if cb != nil {
		cb(c)
	}
}

func (c *McpServerConnection) dropClient(client *mcp.McpClient) {
	if client == nil {
		return
	}
	c.mu.Lock()
	if c.client == client {
		c.client = nil
	}
	c.mu.Unlock()
	_ = client.Close()
}

func (c *McpServerConnection) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *McpServerConnection) open() (*mcp.McpClient, error) {
	c.mu.Lock()
	c.state = ServerStateConnecting
	cb := c.onChange
	isHttp := c.Entry.Config.IsHttp()
	c.mu.Unlock()
	if cb != nil {
		cb(c)
	}

	var retries []time.Duration
	if isHttp {
		retries = runtConnectRetryDelays
	}
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		c.stderrTail = nil
		c.mu.Unlock()

		client, err := c.connectOnce()
		if err == nil {
			return client, nil
		}
		if c.isClosed() || attempt >= len(retries) || !runtIsTransientError(err) {
			return nil, c.connectFailed(err)
		}
		if sleepErr := runtSleep(c.shutdownCtx, nil, retries[attempt]); sleepErr != nil || c.isClosed() {
			return nil, c.connectFailed(err)
		}
	}
}

func (c *McpServerConnection) connectOnce() (*mcp.McpClient, error) {
	timeoutMs := c.TimeoutMs()
	cwdName := filepath.Base(c.cwd)
	client := mcp.NewMcpClient(&mcp.McpClientOptions{
		Implementation: mcpprotocol.Implementation{
			Name:    "pi",
			Version: utils.VERSION,
		},
		RequestTimeoutMs: &timeoutMs,
		Roots: &mcp.McpClientRoots{
			List: []mcpprotocol.Root{{Uri: runtPathToFileURL(c.cwd), Name: &cwdName}},
		},
	})
	if c.log != nil {
		logTarget := c.log
		serverName := c.Entry.Name
		client.OnNotification("notifications/message", func(params any) {
			logTarget.Write(serverName, params)
		})
	}

	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			_ = client.Close()
		})
	}
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-c.shutdownCtx.Done():
			closeClient()
		case <-watchDone:
		}
	}()

	transport, err := c.createTransport(c.Entry, c.cwd, c.rawAuthProvider())
	if err != nil {
		closeClient()
		return nil, err
	}

	if _, err := client.ConnectContext(c.shutdownCtx, transport); err != nil {
		closeClient()
		c.recordStdioStderr(transport)
		return nil, err
	}

	client.OnNotification("notifications/tools/list_changed", func(_ any) {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		c.bgWg.Add(1)
		go func() {
			defer c.bgWg.Done()
			c.refreshTools(client)
		}()
	})
	stdio, _ := transport.(*mcp.StdioTransport)
	client.OnClose(func() {
		c.handleClientClose(client, stdio)
	})

	caps := client.ServerCapabilities()
	var tools []mcpprotocol.Tool
	if caps != nil && caps.Tools != nil {
		tools, err = client.ListTools(c.shutdownCtx, nil)
		if err != nil {
			closeClient()
			c.recordStdioStderr(transport)
			return nil, err
		}
	}
	if c.isClosed() {
		closeClient()
		c.recordStdioStderr(transport)
		return nil, errors.New("shut down while connecting")
	}
	if client.ConnectionState() != mcp.ClientStateConnected {
		closeClient()
		c.recordStdioStderr(transport)
		return nil, errors.New("connection closed during setup")
	}

	var instructions *string
	if rawInst := client.Instructions(); rawInst != nil {
		if trimmed := strings.TrimSpace(*rawInst); trimmed != "" {
			instructions = &trimmed
		}
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		closeClient()
		return nil, errors.New("shut down while connecting")
	}
	c.client = client
	c.tools = tools
	c.hasResources = caps != nil && caps.Resources != nil
	c.instructions = instructions
	c.state = ServerStateConnected
	c.errMsg = nil
	onTools := c.onTools
	onChange := c.onChange
	c.mu.Unlock()

	if onTools != nil {
		onTools(c)
	}
	if onChange != nil {
		onChange(c)
	}
	return client, nil
}

func (c *McpServerConnection) recordStdioStderr(transport mcp.McpTransport) {
	stdio, ok := transport.(*mcp.StdioTransport)
	if !ok || stdio == nil {
		return
	}
	trimmed := strings.TrimSpace(stdio.Stderr())
	if trimmed == "" {
		return
	}
	tail := core.SliceTail(trimmed, runtStderrTailChars)
	if tail == "" {
		return
	}
	c.mu.Lock()
	c.stderrTail = &tail
	c.mu.Unlock()
}

func (c *McpServerConnection) connectFailed(err error) error {
	closed := c.isClosed()
	if c.needsSignIn(err) && !closed {
		c.markNeedsAuth()
		return errors.New(runtSignInRequiredMessage(c.Entry))
	}
	c.mu.Lock()
	if c.closed {
		c.state = ServerStateClosed
	} else {
		c.state = ServerStateFailed
	}
	msg := runtErrorMessage(err)
	if c.stderrTail != nil && *c.stderrTail != "" {
		msg = msg + "\n" + *c.stderrTail
	}
	c.errMsg = &msg
	cb := c.onChange
	c.mu.Unlock()
	if cb != nil {
		cb(c)
	}
	return fmt.Errorf("MCP server %q failed to connect: %s", c.Entry.Name, msg)
}

func (c *McpServerConnection) handleClientClose(client *mcp.McpClient, stdio *mcp.StdioTransport) {
	c.mu.Lock()
	if c.client != client || c.closed {
		c.mu.Unlock()
		return
	}
	c.client = nil
	c.state = ServerStateDisconnected
	errText := "Connection closed"
	if stdio != nil {
		if trimmed := strings.TrimSpace(stdio.Stderr()); trimmed != "" {
			if tail := core.SliceTail(trimmed, runtStderrTailChars); tail != "" {
				errText = "Connection closed\n" + tail
			}
		}
	}
	c.errMsg = &errText
	cb := c.onChange
	c.mu.Unlock()
	if cb != nil {
		cb(c)
	}
}

func (c *McpServerConnection) refreshTools(client *mcp.McpClient) {
	tools, err := client.ListTools(c.shutdownCtx, nil)
	c.mu.Lock()
	if c.client != client || c.closed {
		c.mu.Unlock()
		return
	}
	var onTools func(*McpServerConnection)
	if err != nil {
		msg := "Failed to refresh tools: " + runtErrorMessage(err)
		c.errMsg = &msg
	} else {
		c.tools = tools
		onTools = c.onTools
	}
	onChange := c.onChange
	c.mu.Unlock()

	if onTools != nil {
		onTools(c)
	}
	if onChange != nil {
		onChange(c)
	}
}

// Close aborts in-flight connection attempts, closes active clients, and waits for credential saves.
func (c *McpServerConnection) Close() error {
	c.mu.Lock()
	c.closed = true
	c.state = ServerStateClosed
	client := c.client
	c.client = nil
	cb := c.onChange
	c.mu.Unlock()

	c.shutdownCancel()
	if cb != nil {
		cb(c)
	}
	if client != nil {
		_ = client.Close()
	}
	c.bgWg.Wait()
	if c.oauthProvider != nil {
		c.oauthProvider.Settled()
	}
	return nil
}

func runtSleep(ctx1, ctx2 context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	var ch2 <-chan struct{}
	if ctx2 != nil {
		ch2 = ctx2.Done()
	}
	select {
	case <-timer.C:
		return nil
	case <-ctx1.Done():
		return ctx1.Err()
	case <-ch2:
		return ctx2.Err()
	}
}
