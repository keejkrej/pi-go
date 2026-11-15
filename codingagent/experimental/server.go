// Ported from packages/coding-agent/src/experimental/server.ts (pi v1.0.0).

package experimental

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/transports/unix"
)

const (
	// EnvServerDir selects the default Unix server socket and profile directory.
	EnvServerDir = "PI_SERVER_DIR"
	// EnvServerId selects the logical experimental ServerId profile.
	EnvServerId = "PI_SERVER_ID"
)

const (
	serverLockStaleMs          = 30_000
	serverLockRetryMs          = 25
	serverLockWaitMs           = 30_000
	serverDefaultIdFile        = "default-server-id"
	serverActivationTimeout    = 10 * time.Second
	serverActivationRetryDelay = 10 * time.Millisecond
	serverAutoStartupGrace     = 10 * time.Second
	serverAutoIdleGrace        = 1 * time.Second
)

// ResolveServerDirectory resolves the experimental server socket and profile directory.
func ResolveServerDirectory(directory ...string) string {
	raw := ""
	if len(directory) > 0 && directory[0] != "" {
		raw = directory[0]
	} else if envVal, ok := os.LookupEnv(EnvServerDir); ok && envVal != "" {
		raw = envVal
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		raw = filepath.Join(home, ".pi", "server")
	}
	resolved, err := utils.ResolvePath(raw, "", nil)
	if err != nil {
		return filepath.Clean(raw)
	}
	return resolved
}

// EnsurePrivateServerDirectory creates directory with 0700 permissions owned by the current UID.
func EnsurePrivateServerDirectory(directory string) error {
	uid := os.Getuid()
	if uid < 0 {
		return errors.New("Unix socket directory requires a POSIX user ID")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Unix socket directory is not a directory: %s", directory)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != uid {
		return fmt.Errorf("Unix socket directory is not owned by the current user: %s", directory)
	}
	return os.Chmod(directory, 0o700)
}

// ResolveSessionDirectory resolves the durable experimental session catalog root.
func ResolveSessionDirectory(sessionDir ...string) string {
	raw := ""
	if len(sessionDir) > 0 && sessionDir[0] != "" {
		raw = sessionDir[0]
	} else {
		raw = filepath.Join(utils.GetAgentDir(), "experimental", "sessions")
	}
	resolved, err := utils.ResolvePath(raw, "", nil)
	if err != nil {
		return filepath.Clean(raw)
	}
	return resolved
}

// ServerProfile holds one acquired logical server identity and its launcher lock release callback.
type ServerProfile struct {
	ServerId protocol.ServerId
	Release  func() error
}

// AcquireServerProfile resolves (or creates) the server ID profile and acquires the launcher lock for it.
func AcquireServerProfile(directory string, requestedServerId ...string) (*ServerProfile, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	var requested string
	if len(requestedServerId) > 0 {
		requested = requestedServerId[0]
	}

	var serverId protocol.ServerId
	if requested != "" {
		if !protocol.IsServerId(requested) {
			return nil, fmt.Errorf("Invalid experimental server ID: %s", requested)
		}
		serverId = protocol.ServerId(requested)
	} else {
		idPath := filepath.Join(directory, serverDefaultIdFile)
		data, err := os.ReadFile(idPath)
		if err == nil {
			value := strings.TrimSpace(string(data))
			if !protocol.IsServerId(value) {
				return nil, fmt.Errorf("Invalid default experimental server identity in %s", idPath)
			}
			serverId = protocol.ServerId(value)
		} else if os.IsNotExist(err) {
			candidate := uuid.NewString()
			f, openErr := os.OpenFile(idPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if openErr == nil {
				_, writeErr := f.WriteString(candidate)
				closeErr := f.Close()
				if writeErr != nil {
					return nil, writeErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
				serverId = protocol.ServerId(candidate)
			} else if os.IsExist(openErr) {
				again, readErr := os.ReadFile(idPath)
				if readErr != nil {
					return nil, readErr
				}
				value := strings.TrimSpace(string(again))
				if !protocol.IsServerId(value) {
					return nil, fmt.Errorf("Invalid default experimental server identity in %s", idPath)
				}
				serverId = protocol.ServerId(value)
			} else {
				return nil, openErr
			}
		} else {
			return nil, err
		}
	}

	realpath := false
	stale := int64(serverLockStaleMs)
	update := int64(serverLockStaleMs / 3)
	retries := (serverLockWaitMs + serverLockRetryMs - 1) / serverLockRetryMs
	factor := 1.0
	retryMs := int64(serverLockRetryMs)
	waitMs := int64(serverLockWaitMs)

	release, err := lockfile.Lock(context.Background(), filepath.Join(directory, "launcher-"+string(serverId)), &lockfile.Options{
		Realpath: &realpath,
		Stale:    &stale,
		Update:   &update,
		Retries: &lockfile.RetryOptions{
			Retries:      &retries,
			Factor:       &factor,
			MinTimeout:   &retryMs,
			MaxTimeout:   &retryMs,
			MaxRetryTime: &waitMs,
		},
	})
	if err != nil {
		return nil, err
	}
	return &ServerProfile{
		ServerId: serverId,
		Release:  release,
	}, nil
}

// AcquireServerActivation acquires the cross-process lock serializing automatic server activation vs foreground startup.
func AcquireServerActivation(directory string, serverId protocol.ServerId) (func() error, error) {
	realpath := false
	stale := int64(serverActivationTimeout/time.Millisecond) * 2
	update := int64(serverActivationTimeout / time.Millisecond)
	minT := int64(25)
	retries := int(serverActivationTimeout/time.Millisecond) / 25
	factor := 1.0
	maxWait := int64(serverActivationTimeout / time.Millisecond)

	return lockfile.Lock(context.Background(), filepath.Join(directory, "activation-"+string(serverId)), &lockfile.Options{
		Realpath: &realpath,
		Stale:    &stale,
		Update:   &update,
		Retries: &lockfile.RetryOptions{
			Retries:      &retries,
			Factor:       &factor,
			MinTimeout:   &minT,
			MaxTimeout:   &minT,
			MaxRetryTime: &maxWait,
		},
	})
}

// ActivatedServer returns the live Client and route from ActivateServer.
type ActivatedServer struct {
	Client *client.Client
	Route  client.UnixServerRoute
}

// ActivateServerOptions configures ActivateServer.
type ActivateServerOptions struct {
	Directory         string
	RequestedServerId string
	SessionDir        string
	Provider          string
	Model             string
}

// ActivateServer ensures the selected logical server is reachable, launching the current Go binary if needed.
func ActivateServer(options ActivateServerOptions) (*ActivatedServer, error) {
	if options.Provider != "" && options.Model == "" {
		return nil, errors.New("Server model provider requires a model")
	}
	if err := EnsurePrivateServerDirectory(options.Directory); err != nil {
		return nil, err
	}
	profile, err := AcquireServerProfile(options.Directory, options.RequestedServerId)
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerId
	if err := profile.Release(); err != nil {
		return nil, err
	}
	sockPath, err := unix.GetUnixSocketPath(serverId, options.Directory)
	if err != nil {
		return nil, err
	}
	route := client.UnixServerRoute{
		ServerId: serverId,
		Path:     sockPath,
	}

	release, err := AcquireServerActivation(options.Directory, serverId)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()

	existing, err := serverTryConnect(route)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if options.Model != "" {
			_ = existing.Dispose()
			return nil, errors.New("Model selection is only valid when automatically activating a new server")
		}
		return &ActivatedServer{Client: existing, Route: route}, nil
	}

	args := []string{options.Directory, string(serverId), options.SessionDir}
	if options.Model != "" {
		var modelPayload any
		if options.Provider != "" {
			modelPayload = jsonx.ObjectOf("provider", options.Provider, "model", options.Model)
		} else {
			modelPayload = jsonx.ObjectOf("model", options.Model)
		}
		rawModel, encErr := jsonx.Marshal(modelPayload)
		if encErr != nil {
			return nil, encErr
		}
		args = append(args, string(rawModel))
	}

	child, err := SpawnInternalProcess(InternalRoleServer, args, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to automatically activate server: %w", err)
	}

	deadline := time.Now().Add(serverActivationTimeout)
	for {
		c, connErr := serverTryConnect(route)
		if connErr != nil {
			_ = TerminateInternalProcess(child)
			return nil, connErr
		}
		if c != nil {
			return &ActivatedServer{Client: c, Route: route}, nil
		}
		if child.Exited() {
			return nil, errors.New("Automatically activated server exited during startup")
		}
		if !time.Now().Before(deadline) {
			_ = TerminateInternalProcess(child)
			return nil, errors.New("Timed out waiting for automatically activated server")
		}
		time.Sleep(serverActivationRetryDelay)
	}
}

func serverTryConnect(route client.UnixServerRoute) (*client.Client, error) {
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{
		Path: route.Path,
	})
	if err != nil {
		return nil, err
	}
	c, err := client.Connect(client.ClientOptions{
		ServerId:         route.ServerId,
		TransportFactory: factory,
	})
	if err == nil {
		return c, nil
	}
	var discErr *client.DisconnectedError
	if errors.As(err, &discErr) {
		return nil, nil
	}
	var srvErr *client.ServerError
	if errors.As(err, &srvErr) && srvErr != nil && srvErr.Code == "version" {
		return nil, nil
	}
	if serverIsConnectRetryableError(err) {
		return nil, nil
	}
	return nil, err
}

func serverIsConnectRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	for _, errno := range []syscall.Errno{
		syscall.ENOENT,
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.EPIPE,
		syscall.ETIMEDOUT,
	} {
		if errors.Is(err, errno) {
			return true
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr != nil {
		return serverIsConnectRetryableError(opErr.Err)
	}
	return false
}

// ServerLifetimeOptions configures optional grace durations and timer scheduling for ServerLifetime.
type ServerLifetimeOptions struct {
	StartupGrace time.Duration
	IdleGrace    time.Duration
	AfterFunc    func(d time.Duration, fn func()) func() bool
}

// ServerLifetime reconciles operator keepAlive, startup grace, client connections, and active session workers.
type ServerLifetime struct {
	keepAlive    bool
	startupGrace time.Duration
	idleGrace    time.Duration
	afterFunc    func(d time.Duration, fn func()) func() bool

	mu              sync.Mutex
	connectionCount int
	workerCount     int
	startupHeld     bool
	startupTimer    func() bool
	startupGen      uint64
	retirementTimer func() bool
	retirementGen   uint64
	retire          func()
	stopped         bool
}

// NewServerLifetime constructs a ServerLifetime tracker with default production grace intervals.
func NewServerLifetime(keepAlive bool) *ServerLifetime {
	return NewServerLifetimeWithOptions(keepAlive, ServerLifetimeOptions{})
}

// NewServerLifetimeWithOptions constructs a ServerLifetime tracker with configurable grace intervals and timer hooks.
func NewServerLifetimeWithOptions(keepAlive bool, options ServerLifetimeOptions) *ServerLifetime {
	startupGrace := options.StartupGrace
	if startupGrace <= 0 {
		startupGrace = serverAutoStartupGrace
	}
	idleGrace := options.IdleGrace
	if idleGrace <= 0 {
		idleGrace = serverAutoIdleGrace
	}
	afterFn := options.AfterFunc
	if afterFn == nil {
		afterFn = func(d time.Duration, fn func()) func() bool {
			return time.AfterFunc(d, fn).Stop
		}
	}
	return &ServerLifetime{
		keepAlive:    keepAlive,
		startupGrace: startupGrace,
		idleGrace:    idleGrace,
		afterFunc:    afterFn,
		startupHeld:  !keepAlive,
	}
}

// Start activates lifetime grace tracking and triggers retire when idle without active holds.
func (l *ServerLifetime) Start(retire func()) {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.retire = retire
	if l.startupHeld {
		l.startupGen++
		gen := l.startupGen
		l.startupTimer = l.afterFunc(l.startupGrace, func() {
			l.mu.Lock()
			if l.stopped || l.startupGen != gen {
				l.mu.Unlock()
				return
			}
			l.startupTimer = nil
			l.startupHeld = false
			retireFn := l.reconcileLocked()
			l.mu.Unlock()
			if retireFn != nil {
				retireFn()
			}
		})
	}
	retireFn := l.reconcileLocked()
	l.mu.Unlock()
	if retireFn != nil {
		retireFn()
	}
}

// SetConnectionCount updates the number of open client connections on the server.
func (l *ServerLifetime) SetConnectionCount(count int) {
	l.mu.Lock()
	l.connectionCount = count
	if count > 0 && l.startupHeld {
		l.startupHeld = false
		if l.startupTimer != nil {
			l.startupGen++
			l.startupTimer()
			l.startupTimer = nil
		}
	}
	retireFn := l.reconcileLocked()
	l.mu.Unlock()
	if retireFn != nil {
		retireFn()
	}
}

// SetWorkerCount updates the number of live or launching session workers tracked by the server.
func (l *ServerLifetime) SetWorkerCount(count int) {
	l.mu.Lock()
	l.workerCount = count
	retireFn := l.reconcileLocked()
	l.mu.Unlock()
	if retireFn != nil {
		retireFn()
	}
}

// Stop cancels pending grace timers and disables future automatic retirement callbacks.
func (l *ServerLifetime) Stop() {
	l.mu.Lock()
	l.stopped = true
	if l.startupTimer != nil {
		l.startupGen++
		l.startupTimer()
		l.startupTimer = nil
	}
	if l.retirementTimer != nil {
		l.retirementGen++
		l.retirementTimer()
		l.retirementTimer = nil
	}
	l.mu.Unlock()
}

func (l *ServerLifetime) reconcileLocked() func() {
	if l.stopped || l.keepAlive || l.startupHeld || l.connectionCount != 0 || l.workerCount != 0 {
		if l.retirementTimer != nil {
			l.retirementGen++
			l.retirementTimer()
			l.retirementTimer = nil
		}
		return nil
	}
	if l.retirementTimer != nil || l.retire == nil {
		return nil
	}
	l.retirementGen++
	gen := l.retirementGen
	l.retirementTimer = l.afterFunc(l.idleGrace, func() {
		l.mu.Lock()
		if l.stopped || l.retirementGen != gen {
			l.mu.Unlock()
			return
		}
		l.retirementTimer = nil
		if l.startupHeld || l.connectionCount != 0 || l.workerCount != 0 {
			l.mu.Unlock()
			return
		}
		retireFn := l.retire
		l.mu.Unlock()
		if retireFn != nil {
			retireFn()
		}
	})
	return nil
}

// RunningServer exposes handles and lifecycle controls for an active experimental server.
type RunningServer struct {
	ServerId   protocol.ServerId
	SessionDir string
	SocketPath string
	Server     *server.Server[SessionCatalogMetadata]
	Workers    *SessionWorkerManager

	closedCh  chan struct{}
	closeOnce sync.Once
	closeMu   sync.Mutex
	closeErr  error
	closedErr error
	closeFn   func() error
}

// Closed returns a channel closed once the server runtime and its cleanup complete.
func (r *RunningServer) Closed() <-chan struct{} {
	return r.closedCh
}

// CloseErr returns any error captured when the underlying server stopped.
func (r *RunningServer) CloseErr() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.closedErr
}

// WorkerPids returns a snapshot of tracked sessionId -> PID entries.
func (r *RunningServer) WorkerPids() map[string]int {
	if r.Workers == nil {
		return map[string]int{}
	}
	return r.Workers.WorkerPids()
}

// Close stops the server, relay host, owned workers (unless replaced), and coordinator connection.
func (r *RunningServer) Close() error {
	r.closeOnce.Do(func() {
		if r.closeFn != nil {
			r.closeErr = r.closeFn()
		}
		<-r.closedCh
	})
	return r.closeErr
}

// StartServerOptions configures StartServer and StartForegroundServer.
type StartServerOptions struct {
	Directory      string
	ServerId       *protocol.ServerId
	SessionDir     string
	Provider       string
	Model          string
	KeepAlive      *bool
	RelayAuth      *experimentalcli.AuthInput
	PluginPackages *[]string
	OnRelayStatus  func(status RadiusRelayHostStatus)
}

type serverResolvedPlugins struct {
	packagePaths          []string
	manifestPaths         []string
	presentationArtifacts []node.FacetBundleArtifact
}

type serverBackendOptions struct {
	path                           string
	serverId                       protocol.ServerId
	sessionDir                     string
	resolveSessionPlugins          func(metadata SessionCatalogMetadata, packagePaths []string, ctx chord.Context) (serverResolvedPlugins, error)
	removeSessionPlugins           func(metadata SessionCatalogMetadata) error
	reloadPresentationFacetBundles func(packagePaths []string) ([]node.FacetBundleArtifact, error)
}

type serverBackendHost struct {
	serverServices server.RoutedServerServiceHost
	resolveSession func(sessionId string, callContext chord.Context) (SessionCatalogMetadata, error)
	openSession    func(metadata SessionCatalogMetadata, callContext chord.Context) (server.RoutedSessionHandle, error)
}

func (h *serverBackendHost) ServerServices() server.RoutedServerServiceHost {
	return h.serverServices
}

func (h *serverBackendHost) ResolveSession(sessionId string, callContext chord.Context) (SessionCatalogMetadata, error) {
	return h.resolveSession(sessionId, callContext)
}

func (h *serverBackendHost) OpenSession(metadata SessionCatalogMetadata, callContext chord.Context) (server.RoutedSessionHandle, error) {
	return h.openSession(metadata, callContext)
}

type serverBackend struct {
	serverId   protocol.ServerId
	sessionDir string
	socketPath string
	srv        *server.Server[SessionCatalogMetadata]
	svc        services.ExperimentalServerServices
	closedCh   chan struct{}
	closedMu   sync.Mutex
	closedErr  error
	closeOnce  sync.Once
	closeErr   error
}

func (b *serverBackend) refreshSessions() error {
	return b.svc.Refresh(chord.BackgroundContext)
}

func (b *serverBackend) close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.srv.Close()
		<-b.closedCh
	})
	return b.closeErr
}

func serverStartBackend(
	options serverBackendOptions,
	workers *SessionWorkerManager,
	onConnectionCountChanged func(count int),
) (*serverBackend, error) {
	serverId := options.serverId
	sessionDir := ResolveSessionDirectory(options.sessionDir)

	listSessions := func() ([]SessionCatalogMetadata, error) {
		catalog, err := ListCatalogSessions(sessionDir)
		if err != nil {
			return nil, err
		}
		byPath := make(map[string]SessionCatalogMetadata, len(catalog))
		for _, meta := range catalog {
			byPath[meta.Path] = meta
		}
		for _, meta := range workers.TrackedSessions() {
			byPath[meta.Path] = meta
		}
		out := make([]SessionCatalogMetadata, 0, len(byPath))
		for _, meta := range byPath {
			out = append(out, meta)
		}
		return out, nil
	}

	resolveSession := func(sessionId string, _ chord.Context) (SessionCatalogMetadata, error) {
		for _, meta := range workers.TrackedSessions() {
			if meta.Id == sessionId {
				return meta, nil
			}
		}
		if meta, ok := ReadCatalogSession(sessionDir, sessionId); ok {
			return meta, nil
		}
		return SessionCatalogMetadata{}, server.NewSessionNotFoundError(fmt.Sprintf("Unknown session: %s", sessionId))
	}

	createSession := func(createOptions services.SessionCreateOptions) (SessionCatalogMetadata, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return SessionCatalogMetadata{}, err
		}
		return CreateCatalogSession(sessionDir, SessionCreateCatalogOptions{
			Id:  createOptions.Id,
			Cwd: cwd,
		})
	}

	summarize := func(meta SessionCatalogMetadata) services.SessionSummary {
		return services.SessionSummary{
			ServerId:  serverId,
			SessionId: meta.Id,
			CreatedAt: meta.CreatedAt,
		}
	}

	serverServices, err := services.CreateExperimentalServerServices(services.CreateExperimentalServerServicesOptions{
		List: func(ctx chord.Context) ([]services.SessionSummary, error) {
			sessions, err := listSessions()
			if err != nil {
				return nil, err
			}
			summaries := make([]services.SessionSummary, 0, len(sessions))
			for _, meta := range sessions {
				summaries = append(summaries, summarize(meta))
			}
			sort.Slice(summaries, func(i, j int) bool {
				if summaries[i].SessionId != summaries[j].SessionId {
					return summaries[i].SessionId < summaries[j].SessionId
				}
				return summaries[i].CreatedAt < summaries[j].CreatedAt
			})
			return summaries, nil
		},
		Create: func(createOptions services.SessionCreateOptions, ctx chord.Context) (services.SessionSummary, error) {
			meta, err := createSession(createOptions)
			if err != nil {
				return services.SessionSummary{}, err
			}
			return summarize(meta), nil
		},
		Remove: func(sessionId string, ctx chord.Context) error {
			meta, err := resolveSession(sessionId, ctx)
			if err != nil {
				return err
			}
			if err := workers.CloseSession(meta, ctx); err != nil {
				return err
			}
			if err := DeleteCatalogSession(meta); err != nil {
				return err
			}
			return options.removeSessionPlugins(meta)
		},
		PrepareSessionPlugins: func(sessionId string, packagePaths []string, ctx chord.Context) (services.PreparedSessionPlugins, error) {
			meta, err := resolveSession(sessionId, ctx)
			if err != nil {
				return services.PreparedSessionPlugins{}, err
			}
			selected, err := options.resolveSessionPlugins(meta, packagePaths, ctx)
			if err == nil {
				err = workers.AssertSessionPluginManifestPaths(meta, selected.manifestPaths)
			}
			if err != nil {
				var conflictErr *SessionPluginSelectionConflictError
				if errors.As(err, &conflictErr) {
					return services.PreparedSessionPlugins{}, server.NewServerError("service_invalid_value", conflictErr.Error())
				}
				return services.PreparedSessionPlugins{}, err
			}
			presData, err := plugins.CreatePresentationFacetData(selected.presentationArtifacts)
			if err != nil {
				return services.PreparedSessionPlugins{}, err
			}
			return services.PreparedSessionPlugins{
				PackagePaths:        selected.packagePaths,
				PresentationPlugins: presData,
			}, nil
		},
		ReloadPresentationPlugins: func(packagePaths []string, ctx chord.Context) (chord.JsonValue, error) {
			artifacts, err := options.reloadPresentationFacetBundles(packagePaths)
			if err != nil {
				return nil, err
			}
			return plugins.CreatePresentationFacetData(artifacts)
		},
	})
	if err != nil {
		return nil, err
	}

	host := &serverBackendHost{
		serverServices: serverServices.Host(),
		resolveSession: resolveSession,
		openSession: func(metadata SessionCatalogMetadata, ctx chord.Context) (server.RoutedSessionHandle, error) {
			selected, err := options.resolveSessionPlugins(metadata, nil, ctx)
			if err != nil {
				return nil, err
			}
			handle, err := workers.OpenSession(metadata, ctx, selected.manifestPaths)
			if err != nil {
				var conflictErr *SessionPluginSelectionConflictError
				if errors.As(err, &conflictErr) || strings.Contains(err.Error(), "executing CommonJS facet bundles is unsupported in pi-go") {
					return nil, server.NewServerError("service_invalid_value", err.Error())
				}
				return nil, err
			}
			return handle, nil
		},
	}

	mode := 0o600
	srv, err := unix.CreateUnixServer[SessionCatalogMetadata](host, unix.UnixServerOptions{
		ServerId:                 serverId,
		Path:                     options.path,
		Mode:                     &mode,
		OnConnectionCountChanged: onConnectionCountChanged,
	})
	if err != nil {
		_ = serverServices.Dispose()
		return nil, err
	}
	if err := srv.Start(); err != nil {
		cleanup1 := srv.Close()
		cleanup2 := serverServices.Dispose()
		return nil, errors.Join(err, cleanup1, cleanup2)
	}

	backend := &serverBackend{
		serverId:   serverId,
		sessionDir: sessionDir,
		socketPath: options.path,
		srv:        srv,
		svc:        serverServices,
		closedCh:   make(chan struct{}),
	}
	go func() {
		<-srv.ClosedChan()
		srvErr := srv.Closed()
		dispErr := serverServices.Dispose()
		backend.closedMu.Lock()
		backend.closedErr = errors.Join(srvErr, dispErr)
		backend.closedMu.Unlock()
		close(backend.closedCh)
	}()
	return backend, nil
}

// StartServer starts a replaceable experimental server behind the stable coordinator socket.
func StartServer(options ...StartServerOptions) (*RunningServer, error) {
	var opts StartServerOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Provider != "" && opts.Model == "" {
		return nil, errors.New("Server model provider requires a model")
	}

	var workerModel *SessionWorkerModel
	if opts.Model != "" {
		workerModel = &SessionWorkerModel{
			Provider: opts.Provider,
			Model:    opts.Model,
		}
	}
	directory := ResolveServerDirectory(opts.Directory)
	var requestedId string
	if opts.ServerId != nil {
		requestedId = string(*opts.ServerId)
	} else if envVal, ok := os.LookupEnv(EnvServerId); ok && envVal != "" {
		requestedId = envVal
	}

	profile, err := AcquireServerProfile(directory, requestedId)
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerId
	keepAlive := true
	if opts.KeepAlive != nil {
		keepAlive = *opts.KeepAlive
	}
	lifetime := NewServerLifetime(keepAlive)

	var (
		backend      *serverBackend
		coordinator  *CoordinatorConnection
		startupLease *CoordinatorStartupLease
		workers      *SessionWorkerManager
		relay        *RadiusRelayHost
		released     bool
	)

	cleanupStartup := func(startErr error) error {
		lifetime.Stop()
		if startupLease != nil {
			_ = startupLease.Close()
		}
		if coordinator != nil && coordinator.WasReplaced() && workers != nil {
			workers.Detach()
		}
		var errs []error
		if relay != nil {
			if e := relay.Close(); e != nil {
				errs = append(errs, e)
			}
		}
		if backend != nil {
			if e := backend.close(); e != nil {
				errs = append(errs, e)
			}
		}
		if (coordinator == nil || !coordinator.WasReplaced()) && workers != nil {
			if e := workers.Shutdown(); e != nil {
				errs = append(errs, e)
			}
		}
		if coordinator != nil {
			if e := coordinator.Close(); e != nil {
				errs = append(errs, e)
			}
		}
		if !released && profile.Release != nil {
			if e := profile.Release(); e != nil {
				errs = append(errs, e)
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("Server runtime startup and cleanup failed: %w", errors.Join(append([]error{startErr}, errs...)...))
		}
		return startErr
	}

	if err := EnsurePrivateServerDirectory(directory); err != nil {
		return nil, cleanupStartup(err)
	}

	var configuredPackages []string
	var restoreErr error
	if opts.PluginPackages != nil {
		pkgs := *opts.PluginPackages
		if pkgs == nil {
			pkgs = []string{}
		}
		configuredPackages, restoreErr = plugins.RestoreServerPluginPackageProfile(directory, serverId, pkgs)
	} else {
		configuredPackages, restoreErr = plugins.RestoreServerPluginPackageProfile(directory, serverId)
	}
	if restoreErr != nil {
		return nil, cleanupStartup(restoreErr)
	}

	var pluginMu sync.Mutex
	pluginPackages := make(map[string]plugins.ConfiguredServerPluginPackage)
	getPluginPackage := func(pkgPath string) (plugins.ConfiguredServerPluginPackage, error) {
		pluginMu.Lock()
		defer pluginMu.Unlock()
		if existing := pluginPackages[pkgPath]; existing != nil {
			return existing, nil
		}
		created, err := plugins.CreateServerPluginPackage(directory, serverId, pkgPath)
		if err != nil {
			return nil, err
		}
		pluginPackages[pkgPath] = created
		return created, nil
	}

	buildPluginSelection := func(packagePaths []string) (serverResolvedPlugins, error) {
		normalized, err := plugins.NormalizePluginPackagePaths(packagePaths)
		if err != nil {
			return serverResolvedPlugins{}, err
		}
		manifests := make([]string, 0, len(normalized))
		var artifacts []node.FacetBundleArtifact
		for _, pkgPath := range normalized {
			pkg, pkgErr := getPluginPackage(pkgPath)
			if pkgErr != nil {
				return serverResolvedPlugins{}, pkgErr
			}
			manifests = append(manifests, pkg.ManifestPath())
			built, buildErr := pkg.Build()
			if buildErr != nil {
				return serverResolvedPlugins{}, buildErr
			}
			artifacts = append(artifacts, built...)
		}
		return serverResolvedPlugins{
			packagePaths:          normalized,
			manifestPaths:         manifests,
			presentationArtifacts: artifacts,
		}, nil
	}

	defaultSelection, err := buildPluginSelection(configuredPackages)
	if err != nil {
		return nil, cleanupStartup(err)
	}
	sessionPluginSelections := make(map[string]serverResolvedPlugins)

	resolveSessionPlugins := func(metadata SessionCatalogMetadata, requestedPackagePaths []string, _ chord.Context) (serverResolvedPlugins, error) {
		if requestedPackagePaths != nil {
			normalized, err := plugins.NormalizePluginPackagePaths(requestedPackagePaths)
			if err != nil {
				return serverResolvedPlugins{}, err
			}
			pluginMu.Lock()
			current, hasCurrent := sessionPluginSelections[metadata.Path]
			pluginMu.Unlock()
			if hasCurrent && swmSameStrings(current.packagePaths, normalized) {
				return current, nil
			}
			reqManifests := make([]string, 0, len(normalized))
			for _, pkgPath := range normalized {
				pkg, pkgErr := getPluginPackage(pkgPath)
				if pkgErr != nil {
					return serverResolvedPlugins{}, pkgErr
				}
				reqManifests = append(reqManifests, pkg.ManifestPath())
			}
			if workers != nil {
				if err := workers.AssertSessionPluginManifestPaths(metadata, reqManifests); err != nil {
					return serverResolvedPlugins{}, err
				}
			}
			candidate, err := buildPluginSelection(normalized)
			if err != nil {
				return serverResolvedPlugins{}, err
			}
			if workers != nil {
				if err := workers.AssertSessionPluginManifestPaths(metadata, candidate.manifestPaths); err != nil {
					return serverResolvedPlugins{}, err
				}
			}
			if err := plugins.WriteSessionPluginPackageProfile(directory, serverId, metadata.Path, candidate.packagePaths); err != nil {
				return serverResolvedPlugins{}, err
			}
			pluginMu.Lock()
			sessionPluginSelections[metadata.Path] = candidate
			pluginMu.Unlock()
			return candidate, nil
		}

		pluginMu.Lock()
		cached, hasCached := sessionPluginSelections[metadata.Path]
		defSel := defaultSelection
		pluginMu.Unlock()
		if hasCached {
			return cached, nil
		}
		storedPaths, found, err := plugins.ReadSessionPluginPackageProfile(directory, serverId, metadata.Path)
		if err != nil {
			return serverResolvedPlugins{}, err
		}
		var selected serverResolvedPlugins
		if !found {
			selected = defSel
			if err := plugins.WriteSessionPluginPackageProfile(directory, serverId, metadata.Path, selected.packagePaths); err != nil {
				return serverResolvedPlugins{}, err
			}
		} else {
			selected, err = buildPluginSelection(storedPaths)
			if err != nil {
				return serverResolvedPlugins{}, err
			}
		}
		pluginMu.Lock()
		sessionPluginSelections[metadata.Path] = selected
		pluginMu.Unlock()
		return selected, nil
	}

	removeSessionPlugins := func(metadata SessionCatalogMetadata) error {
		pluginMu.Lock()
		delete(sessionPluginSelections, metadata.Path)
		pluginMu.Unlock()
		return plugins.RemoveSessionPluginPackageProfile(directory, serverId, metadata.Path)
	}

	reloadPresentationFacetBundles := func(packagePaths []string) ([]node.FacetBundleArtifact, error) {
		reloaded, err := buildPluginSelection(packagePaths)
		if err != nil {
			return nil, err
		}
		pluginMu.Lock()
		if swmSameStrings(defaultSelection.packagePaths, reloaded.packagePaths) {
			defaultSelection = reloaded
		}
		for sessPath, sel := range sessionPluginSelections {
			if swmSameStrings(sel.packagePaths, reloaded.packagePaths) {
				sessionPluginSelections[sessPath] = reloaded
			}
		}
		pluginMu.Unlock()
		return reloaded.presentationArtifacts, nil
	}

	socketPath, err := unix.GetUnixSocketPath(serverId, directory)
	if err != nil {
		return nil, cleanupStartup(err)
	}
	controlPath := filepath.Join(directory, fmt.Sprintf("control-%s.sock", serverId))
	nonceBytes := make([]byte, 6)
	_, _ = rand.Read(nonceBytes)
	serverNonce := hex.EncodeToString(nonceBytes)
	serverPath := filepath.Join(directory, fmt.Sprintf("server-%s-%s.sock", serverId, serverNonce))

	startupLease, err = EnsureCoordinator(socketPath, controlPath)
	if err != nil {
		return nil, cleanupStartup(err)
	}
	coordinator = NewCoordinatorConnection(CoordinatorConnectionOptions{
		ControlPath: controlPath,
		Endpoint:    serverPath,
	})
	sessionDir := ResolveSessionDirectory(opts.SessionDir)
	workers = NewSessionWorkerManager(coordinator, sessionDir, workerModel, func(count int) {
		lifetime.SetWorkerCount(count)
	})
	backend, err = serverStartBackend(
		serverBackendOptions{
			path:                           serverPath,
			serverId:                       serverId,
			sessionDir:                     opts.SessionDir,
			resolveSessionPlugins:          resolveSessionPlugins,
			removeSessionPlugins:           removeSessionPlugins,
			reloadPresentationFacetBundles: reloadPresentationFacetBundles,
		},
		workers,
		func(count int) {
			lifetime.SetConnectionCount(count)
		},
	)
	if err != nil {
		return nil, cleanupStartup(err)
	}
	if err := coordinator.Connect(); err != nil {
		return nil, cleanupStartup(err)
	}
	_ = startupLease.Close()
	startupLease = nil

	if err := workers.Discover(coordinator.PeerIds()); err != nil {
		return nil, cleanupStartup(err)
	}
	if err := backend.refreshSessions(); err != nil {
		return nil, cleanupStartup(err)
	}

	relay = NewRadiusRelayHost(RadiusRelayHostOptions{
		ServerId: serverId,
		Accept:   backend.srv.Accept,
		Auth:     NewRadiusRelayAuthResolver(opts.RelayAuth),
		OnStatus: opts.OnRelayStatus,
	})
	relay.Start()

	runtimeClosedCh := make(chan struct{})
	var runtimeCloseOnce sync.Once
	var runtimeCloseMu sync.Mutex
	var runtimeCloseErr error
	var runtimeClosedErr error

	performClose := func() error {
		lifetime.Stop()
		runtimeCloseOnce.Do(func() {
			rErr := relay.Close()
			bErr := backend.close()
			var wErr error
			if coordinator.WasReplaced() {
				workers.Detach()
			} else {
				wErr = workers.Shutdown()
			}
			cErr := coordinator.Close()
			runtimeCloseMu.Lock()
			runtimeCloseErr = errors.Join(rErr, bErr, wErr, cErr)
			runtimeCloseMu.Unlock()
		})
		<-runtimeClosedCh
		runtimeCloseMu.Lock()
		err := runtimeCloseErr
		runtimeCloseMu.Unlock()
		return err
	}

	running := &RunningServer{
		ServerId:   serverId,
		SessionDir: backend.sessionDir,
		SocketPath: socketPath,
		Server:     backend.srv,
		Workers:    workers,
		closedCh:   runtimeClosedCh,
		closeFn:    performClose,
	}

	go func() {
		defer close(runtimeClosedCh)
		<-backend.closedCh
		backend.closedMu.Lock()
		bErr := backend.closedErr
		backend.closedMu.Unlock()
		rErr := relay.Close()
		running.closeMu.Lock()
		running.closedErr = errors.Join(bErr, rErr)
		running.closeMu.Unlock()
		runtimeCloseMu.Lock()
		runtimeClosedErr = running.closedErr
		_ = runtimeClosedErr
		runtimeCloseMu.Unlock()
	}()

	go func() {
		select {
		case <-coordinator.Replaced():
			lifetime.Stop()
			workers.Detach()
			_ = relay.Close()
			_ = backend.close()
			_ = coordinator.Close()
		case <-runtimeClosedCh:
		}
	}()

	lifetime.Start(func() {
		go func() { _ = running.Close() }()
	})

	released = true
	if err := profile.Release(); err != nil {
		_ = running.Close()
		return nil, err
	}
	return running, nil
}

// StartForegroundServer starts an operator-held keepAlive server serialized against automatic cold activation.
func StartForegroundServer(options ...StartServerOptions) (*RunningServer, error) {
	var opts StartServerOptions
	if len(options) > 0 {
		opts = options[0]
	}
	directory := ResolveServerDirectory(opts.Directory)
	if err := EnsurePrivateServerDirectory(directory); err != nil {
		return nil, err
	}
	var requestedId string
	if opts.ServerId != nil {
		requestedId = string(*opts.ServerId)
	} else if envVal, ok := os.LookupEnv(EnvServerId); ok && envVal != "" {
		requestedId = envVal
	}
	profile, err := AcquireServerProfile(directory, requestedId)
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerId
	if err := profile.Release(); err != nil {
		return nil, err
	}

	release, err := AcquireServerActivation(directory, serverId)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()

	keepAlive := true
	opts.Directory = directory
	opts.ServerId = &serverId
	opts.KeepAlive = &keepAlive
	return StartServer(opts)
}

func serverParseModelOptions(raw string) (*SessionWorkerModel, error) {
	var parsed any
	parsed, err := jsonx.ParseString(raw)
	if err != nil {
		return nil, fmt.Errorf("Internal server received invalid model options: %w", err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		return nil, errors.New("Internal server received invalid model options")
	}
	for _, key := range obj.Keys() {
		if key != "provider" && key != "model" {
			return nil, errors.New("Internal server received invalid model options")
		}
	}
	modelVal, hasModel := obj.Get("model")
	modelStr, okModel := modelVal.(string)
	if !hasModel || !okModel || modelStr == "" {
		return nil, errors.New("Internal server received invalid model options")
	}
	out := &SessionWorkerModel{Model: modelStr}
	if provVal, hasProv := obj.Get("provider"); hasProv {
		provStr, okProv := provVal.(string)
		if !okProv || provStr == "" {
			return nil, errors.New("Internal server received invalid model options")
		}
		out.Provider = provStr
	}
	return out, nil
}

// RunServerProcess executes an automatically activated experimental server until idle demand expires or signaled.
func RunServerProcess(args []string) error {
	if len(args) > 4 {
		return errors.New("Internal server received unexpected arguments")
	}
	var directory, rawServerId, sessionDir string
	if len(args) > 0 {
		directory = args[0]
	}
	if len(args) > 1 {
		rawServerId = args[1]
	}
	if len(args) > 2 {
		sessionDir = args[2]
	}
	if directory == "" || !filepath.IsAbs(directory) {
		return errors.New("Internal server requires an absolute server directory")
	}
	if !protocol.IsServerId(rawServerId) {
		return errors.New("Internal server requires a canonical server ID")
	}
	if sessionDir == "" || !filepath.IsAbs(sessionDir) {
		return errors.New("Internal server requires an absolute Session directory")
	}
	var provider, model string
	if len(args) == 4 && args[3] != "" {
		wm, err := serverParseModelOptions(args[3])
		if err != nil {
			return err
		}
		provider = wm.Provider
		model = wm.Model
	}

	serverId := protocol.ServerId(rawServerId)
	keepAlive := false
	runtime, err := StartServer(StartServerOptions{
		Directory:  directory,
		ServerId:   &serverId,
		SessionDir: sessionDir,
		KeepAlive:  &keepAlive,
		Provider:   provider,
		Model:      model,
	})
	if err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		return runtime.Close()
	case <-runtime.Closed():
		closeErr := runtime.Close()
		if err := runtime.CloseErr(); err != nil {
			return errors.Join(err, closeErr)
		}
		return closeErr
	}
}
