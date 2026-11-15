// Ported from packages/coding-agent/src/experimental/client-runtime.ts (pi v1.0.0).

package experimental

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/protocol"
)

// ClientRuntimeRoute describes a resolved local Unix or remote Radius transport route.
type ClientRuntimeRoute struct {
	Transport string            `json:"transport"`
	ServerId  protocol.ServerId `json:"serverId"`
	Path      string            `json:"path,omitzero"`
}

// ClientRuntimeServer holds the live RPC Client and service sources for one connected server.
type ClientRuntimeServer struct {
	Route   ClientRuntimeRoute
	Client  *client.Client
	Server  services.ServerServiceSource
	Session services.SessionServiceSource
}

// ActivatedClientRuntimeServer bundles one connected server with resolved built-in facades.
type ActivatedClientRuntimeServer struct {
	ClientRuntimeServer
	Directory  services.SessionDirectory
	Management services.SessionManagement
	Plugins    services.PresentationPlugins
	Models     services.Models
	Agent      services.AgentController
	Transcript services.Transcript
}

// ClientRuntime owns one or more connected experimental server routes and their cleanup.
type ClientRuntime struct {
	Servers []ClientRuntimeServer

	mu         sync.Mutex
	disposed   bool
	disposeErr error
	disposeFn  func() error
}

// Dispose releases all reconnectors, service sources, and client connections owned by the runtime.
func (r *ClientRuntime) Dispose() error {
	r.mu.Lock()
	if r.disposed {
		err := r.disposeErr
		r.mu.Unlock()
		return err
	}
	r.disposed = true
	fn := r.disposeFn
	r.mu.Unlock()

	var err error
	if fn != nil {
		err = fn()
	}
	r.mu.Lock()
	r.disposeErr = err
	r.mu.Unlock()
	return err
}

// OpenClientRuntimeOptions configures OpenClientRuntime.
type OpenClientRuntimeOptions struct {
	Directory string
}

// OpenClientRuntime resolves or automatically activates server routes and opens service sources for each.
func OpenClientRuntime(command experimentalcli.ClientCommand, options ...OpenClientRuntimeOptions) (*ClientRuntime, error) {
	if command.Auth != nil && (command.Connect == nil || command.Connect.Transport != experimentalcli.TransportTypeRadius) {
		return nil, errors.New("Authentication is only supported for experimental Radius connections")
	}
	if command.Provider != nil && *command.Provider != "" && (command.Model == nil || *command.Model == "") {
		return nil, errors.New("Server model provider requires a model")
	}
	if command.Connect != nil && command.Model != nil && *command.Model != "" {
		return nil, errors.New("Model selection is only valid when automatically activating a new server")
	}
	if command.Connect != nil && command.Connect.Transport == experimentalcli.TransportTypeRadius && command.PluginPackages != nil {
		return nil, errors.New("Plugin package paths can only be configured on a local Unix server")
	}

	var opt OpenClientRuntimeOptions
	if len(options) > 0 {
		opt = options[0]
	}
	directory := ResolveServerDirectory(opt.Directory)
	var routes []ClientRuntimeRoute
	var activatedClient *client.Client

	var modelStr, providerStr string
	if command.Model != nil {
		modelStr = *command.Model
	}
	if command.Provider != nil {
		providerStr = *command.Provider
	}

	if command.Connect != nil {
		if command.Connect.Transport == experimentalcli.TransportTypeRadius {
			routes = []ClientRuntimeRoute{{
				Transport: "radius",
				ServerId:  command.Connect.ServerId,
			}}
		} else {
			route, err := crRouteFromExplicitPath(command.Connect.Path)
			if err != nil {
				return nil, err
			}
			routes = []ClientRuntimeRoute{{
				Transport: "unix",
				ServerId:  route.ServerId,
				Path:      route.Path,
			}}
		}
	} else {
		discovered, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{
			Directory: directory,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range discovered {
			routes = append(routes, ClientRuntimeRoute{
				Transport: "unix",
				ServerId:  r.ServerId,
				Path:      r.Path,
			})
		}
		if len(routes) > 0 && modelStr != "" {
			return nil, errors.New("Model selection is only valid when automatically activating a new server")
		}
		if len(routes) == 0 {
			activated, err := ActivateServer(ActivateServerOptions{
				Directory:         directory,
				RequestedServerId: os.Getenv(EnvServerId),
				SessionDir:        ResolveSessionDirectory(),
				Provider:          providerStr,
				Model:             modelStr,
			})
			if err != nil {
				return nil, err
			}
			routes = []ClientRuntimeRoute{{
				Transport: "unix",
				ServerId:  activated.Route.ServerId,
				Path:      activated.Route.Path,
			}}
			activatedClient = activated.Client
		}
	}

	if command.PluginPackages != nil && len(routes) != 1 {
		return nil, errors.New("Plugin selection requires exactly one local server")
	}

	var clients []*client.Client
	var reconnectors []*RadiusClientReconnect
	var serverSources []services.ServerServiceSource
	var sessionSources []services.SessionServiceSource
	var servers []ClientRuntimeServer

	cleanup := func() error {
		var errs []error
		for _, rec := range reconnectors {
			if err := rec.Dispose(); err != nil {
				errs = append(errs, err)
			}
		}
		for _, src := range sessionSources {
			if err := src.Dispose(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
		for _, src := range serverSources {
			if err := src.Dispose(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
		for _, c := range clients {
			if err := c.Dispose(); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) == 1 {
			return errs[0]
		}
		if len(errs) > 1 {
			return fmt.Errorf("Failed to dispose experimental client runtime: %w", errors.Join(errs...))
		}
		return nil
	}

	for _, route := range routes {
		c := activatedClient
		activatedClient = nil
		if c == nil {
			var factory client.ByteTransportFactory
			var err error
			if route.Transport == "unix" {
				factory, err = client.CreateUnixTransportFactory(client.UnixTransportOptions{
					Path: route.Path,
				})
				if err != nil {
					cleanupErr := cleanup()
					return nil, crWrapStartupCleanupError(err, cleanupErr)
				}
			} else {
				factory = CreateRadiusClientTransportFactory(RadiusClientTransportOptions{
					ServerId: route.ServerId,
					Auth:     NewRadiusRelayAuthResolver(command.Auth),
				})
			}

			c, err = client.Connect(client.ClientOptions{
				ServerId:         route.ServerId,
				TransportFactory: factory,
			})
			if err != nil {
				var srvErr *client.ServerError
				if command.Connect == nil && route.Transport == "unix" && errors.As(err, &srvErr) && srvErr != nil && srvErr.Code == "version" {
					activated, actErr := ActivateServer(ActivateServerOptions{
						Directory:         directory,
						RequestedServerId: string(route.ServerId),
						SessionDir:        ResolveSessionDirectory(),
					})
					if actErr != nil {
						cleanupErr := cleanup()
						return nil, crWrapStartupCleanupError(actErr, cleanupErr)
					}
					c = activated.Client
				} else {
					cleanupErr := cleanup()
					return nil, crWrapStartupCleanupError(err, cleanupErr)
				}
			}
		}

		clients = append(clients, c)
		srvSrc := services.CreateServerServiceSource(c)
		serverSources = append(serverSources, srvSrc)
		if route.Transport == "radius" {
			reconnectServices := srvSrc.Open(&chord.RemoteServiceSourceOpenOptions{
				Services:     []chord.ServiceRef{{Id: services.SessionManagementService.Id}},
				AssertAccess: func() {},
				OnError:      func(error) {},
			})
			reconnectMgmt := services.AsSessionManagement(reconnectServices.Use(services.SessionManagementService))
			rec, err := NewRadiusClientReconnect(c, func(sessionId string) error {
				if err := reconnectServices.Ready(chord.BackgroundContext); err != nil {
					return err
				}
				return reconnectMgmt.Attach(sessionId, chord.BackgroundContext)
			})
			if err != nil {
				_ = reconnectServices.Dispose(chord.BackgroundContext)
				cleanupErr := cleanup()
				return nil, crWrapStartupCleanupError(err, cleanupErr)
			}
			reconnectors = append(reconnectors, rec)
		}

		sessSrc := services.CreateSessionServiceSource(c)
		sessionSources = append(sessionSources, sessSrc)
		servers = append(servers, ClientRuntimeServer{
			Route:   route,
			Client:  c,
			Server:  srvSrc,
			Session: sessSrc,
		})
	}

	return &ClientRuntime{
		Servers:   servers,
		disposeFn: cleanup,
	}, nil
}

type crWrappedManagement struct {
	server ClientRuntimeServer
	remote services.SessionManagement
}

func (m *crWrappedManagement) Create(options services.SessionCreateOptions, ctx chord.Context) (services.SessionSummary, error) {
	return m.remote.Create(options, ctx)
}

func (m *crWrappedManagement) Remove(sessionId string, ctx chord.Context) error {
	att := m.server.Client.Attachment()
	removesCurrent := att != nil && att.SessionId == sessionId
	if err := m.remote.Remove(sessionId, ctx); err != nil {
		return err
	}
	if removesCurrent {
		return m.server.Session.WhenDetached(ctx)
	}
	return nil
}

func (m *crWrappedManagement) Attach(sessionId string, ctx chord.Context) error {
	if err := m.remote.Attach(sessionId, ctx); err != nil {
		return err
	}
	return m.server.Session.WhenAttached(sessionId, ctx)
}

func (m *crWrappedManagement) Detach(ctx chord.Context) error {
	if err := m.remote.Detach(ctx); err != nil {
		return err
	}
	return m.server.Session.WhenDetached(ctx)
}

// ActivateBuiltinClientServices acquires and hydrates built-in service facades for one ClientRuntimeServer.
func ActivateBuiltinClientServices(srv ClientRuntimeServer) (*ActivatedClientRuntimeServer, error) {
	serverServices := srv.Server.Open(&chord.RemoteServiceSourceOpenOptions{
		Services: []chord.ServiceRef{
			{Id: services.SessionDirectoryService.Id},
			{Id: services.SessionManagementService.Id},
			{Id: services.PresentationPluginsService.Id},
		},
		AssertAccess: func() {},
		OnError:      func(error) {},
	})
	sessionServices := srv.Session.Open(&chord.RemoteServiceSourceOpenOptions{
		Services: []chord.ServiceRef{
			{Id: services.ModelsService.Id},
			{Id: services.AgentControllerService.Id},
			{Id: services.TranscriptService.Id},
		},
		AssertAccess: func() {},
		OnError:      func(error) {},
	})

	dir := services.AsSessionDirectory(serverServices.Use(services.SessionDirectoryService))
	remoteMgmt := services.AsSessionManagement(serverServices.Use(services.SessionManagementService))
	plug := services.AsPresentationPlugins(serverServices.Use(services.PresentationPluginsService))
	models := services.AsModels(sessionServices.Use(services.ModelsService))
	agent := services.AsAgentController(sessionServices.Use(services.AgentControllerService))
	transcript := services.AsTranscript(sessionServices.Use(services.TranscriptService))

	if err := serverServices.Ready(chord.BackgroundContext); err != nil {
		return nil, err
	}
	if err := sessionServices.Ready(chord.BackgroundContext); err != nil {
		return nil, err
	}

	return &ActivatedClientRuntimeServer{
		ClientRuntimeServer: srv,
		Directory:           dir,
		Management: &crWrappedManagement{
			server: srv,
			remote: remoteMgmt,
		},
		Plugins:    plug,
		Models:     models,
		Agent:      agent,
		Transcript: transcript,
	}, nil
}

func crRouteFromExplicitPath(path string) (client.UnixServerRoute, error) {
	name := filepath.Base(path)
	var serverId string
	if strings.HasSuffix(name, ".sock") {
		serverId = strings.TrimSuffix(name, ".sock")
	}
	if !protocol.IsServerId(serverId) {
		return client.UnixServerRoute{}, errors.New("--connect path must end with <uuidv4-server-id>.sock")
	}
	return client.UnixServerRoute{
		ServerId: protocol.ServerId(serverId),
		Path:     path,
	}, nil
}

func crWrapStartupCleanupError(err error, cleanupErr error) error {
	if cleanupErr != nil {
		return fmt.Errorf("Experimental client startup and cleanup failed: %w", errors.Join(err, cleanupErr))
	}
	return err
}
