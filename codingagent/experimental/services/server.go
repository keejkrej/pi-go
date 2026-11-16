// Ported from packages/coding-agent/src/experimental/services/server.ts (pi v1.0.0).

package services

import (
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/server"
)

// ExperimentalServerServices hosts server-scoped Chord services for connected presentations.
type ExperimentalServerServices interface {
	Host() server.RoutedServerServiceHost
	Refresh(ctx ...chord.Context) error
	Dispose() error
}

// PreparedSessionPlugins holds resolved package paths and presentation plugin payloads for a session.
type PreparedSessionPlugins struct {
	PackagePaths        []string
	PresentationPlugins chord.JsonValue
}

// CreateExperimentalServerServicesOptions configures CreateExperimentalServerServices.
type CreateExperimentalServerServicesOptions struct {
	List                      func(ctx chord.Context) ([]SessionSummary, error)
	Create                    func(createOptions SessionCreateOptions, ctx chord.Context) (SessionSummary, error)
	Remove                    func(sessionId string, ctx chord.Context) error
	PrepareSessionPlugins     func(sessionId string, packagePaths []string, ctx chord.Context) (PreparedSessionPlugins, error)
	ReloadPresentationPlugins func(packagePaths []string, ctx chord.Context) (chord.JsonValue, error)
}

type serverServicesImpl struct {
	options     CreateExperimentalServerServicesOptions
	directory   chord.MutableReplicatedState[chord.JsonValue]
	queue       connSerialQueue
	mu          sync.Mutex
	revision    int
	attachments map[*serverProviderAttachment]struct{}
	disposed    bool
}

// CreateExperimentalServerServices constructs the server-scoped service host and directory state.
func CreateExperimentalServerServices(options CreateExperimentalServerServicesOptions) (ExperimentalServerServices, error) {
	initialSessions, err := options.List(chord.BackgroundContext)
	if err != nil {
		return nil, err
	}
	if initialSessions == nil {
		initialSessions = []SessionSummary{}
	}
	initialJson, err := connEncodeJsonValue(SessionDirectoryState{
		Revision: 1,
		Sessions: initialSessions,
	})
	if err != nil {
		return nil, err
	}
	directory, err := chord.NewReplicatedState(initialJson)
	if err != nil {
		return nil, err
	}
	return &serverServicesImpl{
		options:     options,
		directory:   directory,
		revision:    1,
		attachments: make(map[*serverProviderAttachment]struct{}),
	}, nil
}

func (s *serverServicesImpl) Host() server.RoutedServerServiceHost {
	return s
}

func (s *serverServicesImpl) refreshNow(ctx chord.Context) error {
	sessions, err := s.options.List(ctx)
	if err != nil {
		return err
	}
	if sessions == nil {
		sessions = []SessionSummary{}
	}
	s.mu.Lock()
	s.revision++
	rev := s.revision
	s.mu.Unlock()

	sessionsJson, err := connEncodeJsonValue(sessions)
	if err != nil {
		return err
	}
	return s.directory.Change(ctx, func(draft chord.Draft[chord.JsonValue]) error {
		root, ok := draft.Value.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("unexpected SessionDirectoryState draft %T", draft.Value)
		}
		root.Set("revision", float64(rev))
		root.Set("sessions", sessionsJson)
		return nil
	})
}

func (s *serverServicesImpl) serialize(fn func() error) error {
	var err error
	s.queue.runSync(func() {
		err = fn()
	})
	return err
}

func (s *serverServicesImpl) Refresh(ctxs ...chord.Context) error {
	ctx := chord.BackgroundContext
	if len(ctxs) > 0 && ctxs[0] != nil {
		ctx = ctxs[0]
	}
	return s.serialize(func() error {
		return s.refreshNow(ctx)
	})
}

func (s *serverServicesImpl) AttachClient(presentation server.RoutedServerPresentation, _ chord.Context) (server.RoutedServerServiceAttachment, error) {
	mode := chord.ServiceModeSingleton
	provider, err := chord.NewRemoteServiceProvider([]chord.RemoteServiceProviderEntry{
		{Id: SessionDirectoryService.Id, Mode: &mode},
		{Id: SessionManagementService.Id, Mode: &mode},
		{Id: PresentationPluginsService.Id, Mode: &mode},
	})
	if err != nil {
		return nil, err
	}

	var prepMu sync.Mutex
	var preparedPluginPackagePaths []string
	hasPrepared := false

	if err := provider.Provide(SessionDirectoryService, ProvideSessionDirectory(s.directory)); err != nil {
		_ = provider.Dispose()
		return nil, err
	}
	if err := provider.Provide(PresentationPluginsService, ProvidePresentationPlugins(&serverPresentationPlugins{
		prepareSession: func(req PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error) {
			var res chord.JsonValue
			err := s.serialize(func() error {
				selected, err := s.options.PrepareSessionPlugins(req.SessionId, req.PackagePaths, ctx)
				if err != nil {
					return err
				}
				prepMu.Lock()
				preparedPluginPackagePaths = append([]string(nil), selected.PackagePaths...)
				hasPrepared = true
				prepMu.Unlock()
				res = selected.PresentationPlugins
				return nil
			})
			return res, err
		},
		reload: func(ctx chord.Context) (chord.JsonValue, error) {
			var res chord.JsonValue
			err := s.serialize(func() error {
				prepMu.Lock()
				ready := hasPrepared
				paths := append([]string(nil), preparedPluginPackagePaths...)
				prepMu.Unlock()
				if !ready {
					return errors.New("No Session plugin selection is prepared")
				}
				var err error
				res, err = s.options.ReloadPresentationPlugins(paths, ctx)
				return err
			})
			return res, err
		},
	})); err != nil {
		_ = provider.Dispose()
		return nil, err
	}

	if err := provider.Provide(SessionManagementService, ProvideSessionManagement(&serverSessionManagement{
		create: func(createOptions SessionCreateOptions, ctx chord.Context) (SessionSummary, error) {
			var created SessionSummary
			err := s.serialize(func() error {
				var err error
				created, err = s.options.Create(createOptions, ctx)
				if err != nil {
					return err
				}
				return s.refreshNow(ctx)
			})
			return created, err
		},
		remove: func(sessionId string, ctx chord.Context) error {
			return s.serialize(func() error {
				if err := presentation.PrepareSessionRemoval(sessionId, ctx); err != nil {
					return err
				}
				if err := s.options.Remove(sessionId, ctx); err != nil {
					return err
				}
				return s.refreshNow(ctx)
			})
		},
		attach: func(sessionId string, ctx chord.Context) error {
			return s.serialize(func() error {
				return presentation.AttachSession(sessionId, ctx)
			})
		},
		detach: func(ctx chord.Context) error {
			return s.serialize(func() error {
				if err := presentation.DetachSession(ctx); err != nil {
					return err
				}
				prepMu.Lock()
				preparedPluginPackagePaths = nil
				hasPrepared = false
				prepMu.Unlock()
				return nil
			})
		},
	})); err != nil {
		_ = provider.Dispose()
		return nil, err
	}

	var holder *serverProviderAttachment
	att := serverNewProviderAttachment(provider, func() {
		s.mu.Lock()
		delete(s.attachments, holder)
		s.mu.Unlock()
	})
	holder = att

	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		_ = att.Release(chord.BackgroundContext)
		return nil, errors.New("Server services are disposed")
	}
	s.attachments[att] = struct{}{}
	s.mu.Unlock()
	return att, nil
}

func (s *serverServicesImpl) Dispose() error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil
	}
	s.disposed = true
	list := make([]*serverProviderAttachment, 0, len(s.attachments))
	for a := range s.attachments {
		list = append(list, a)
	}
	clear(s.attachments)
	s.mu.Unlock()

	var errs []error
	for _, a := range list {
		if err := a.Release(chord.BackgroundContext); err != nil {
			errs = append(errs, err)
		}
	}
	s.queue.wait()
	if len(errs) > 0 {
		return fmt.Errorf("Failed to release server service attachments: %w", errors.Join(errs...))
	}
	return nil
}

type serverPresentationPlugins struct {
	prepareSession func(request PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error)
	reload         func(ctx chord.Context) (chord.JsonValue, error)
}

func (p *serverPresentationPlugins) PrepareSession(request PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error) {
	return p.prepareSession(request, ctx)
}

func (p *serverPresentationPlugins) Reload(ctx chord.Context) (chord.JsonValue, error) {
	return p.reload(ctx)
}

type serverSessionManagement struct {
	create func(options SessionCreateOptions, ctx chord.Context) (SessionSummary, error)
	remove func(sessionId string, ctx chord.Context) error
	attach func(sessionId string, ctx chord.Context) error
	detach func(ctx chord.Context) error
}

func (m *serverSessionManagement) Create(options SessionCreateOptions, ctx chord.Context) (SessionSummary, error) {
	return m.create(options, ctx)
}

func (m *serverSessionManagement) Remove(sessionId string, ctx chord.Context) error {
	return m.remove(sessionId, ctx)
}

func (m *serverSessionManagement) Attach(sessionId string, ctx chord.Context) error {
	return m.attach(sessionId, ctx)
}

func (m *serverSessionManagement) Detach(ctx chord.Context) error {
	return m.detach(ctx)
}

type serverProviderAttachment struct {
	mu        sync.Mutex
	provider  *chord.RemoteServiceProvider
	endpoint  chord.RemoteServiceEndpoint
	onRelease func()
	released  bool
}

func serverNewProviderAttachment(provider *chord.RemoteServiceProvider, onRelease func()) *serverProviderAttachment {
	return &serverProviderAttachment{
		provider:  provider,
		endpoint:  chord.CreateRemoteServiceEndpoint(provider),
		onRelease: onRelease,
	}
}

func (a *serverProviderAttachment) InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error) {
	a.mu.Lock()
	released := a.released
	endpoint := a.endpoint
	a.mu.Unlock()
	if released {
		return nil, false, errors.New("Server service attachment is released")
	}
	return endpoint.Invoke(call, publish, callContext)
}

func (a *serverProviderAttachment) Release(_ chord.Context) error {
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return nil
	}
	a.released = true
	endpoint := a.endpoint
	provider := a.provider
	onRelease := a.onRelease
	a.mu.Unlock()

	endpoint.Dispose()
	err := provider.Dispose()
	if onRelease != nil {
		onRelease()
	}
	return err
}
