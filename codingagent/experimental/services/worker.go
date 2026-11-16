// Ported from packages/coding-agent/src/experimental/services/worker.ts (pi v1.0.0).

package services

import (
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/durable"
)

// SessionWorkerRuntime supplies durable harness, model runtime, and facet loader for one Session worker.
type SessionWorkerRuntime struct {
	Harness         durable.Harness
	Conversation    durable.Conversation
	ModelRuntime    *core.ModelRuntime
	SettingsManager *core.SettingsManager
	FacetLoader     chord.FacetLoader
	Cleanup         func(ctx chord.Context) error
}

// WorkerServiceScope scopes worker service subscriptions to a server connection and attachment ID.
type WorkerServiceScope struct {
	ServerConnectionId string
	AttachmentId       string
}

type workerScopedEndpoint struct {
	scope    WorkerServiceScope
	endpoint chord.RemoteServiceEndpoint
}

// SessionWorkerServices hosts the Chord FacetHost and endpoints for a running Session worker.
type SessionWorkerServices interface {
	Invoke(call chord.ServiceCall, scope WorkerServiceScope, ctx chord.Context) (chord.JsonValue, bool, error)
	RemoveSubscriptions(matches func(scope WorkerServiceScope) bool)
	Dispose() error
}

// CreateSessionWorkerServicesOptions configures CreateSessionWorkerServices.
type CreateSessionWorkerServicesOptions struct {
	Harness         durable.Harness
	Conversation    durable.Conversation
	ModelRuntime    *core.ModelRuntime
	SettingsManager *core.SettingsManager
	FacetLoader     chord.FacetLoader
	Publish         func(scope WorkerServiceScope, subscriptionId string, update chord.ServiceProviderUpdate, ctx chord.Context) error
}

type workerSessionPlugins struct {
	mu     sync.Mutex
	reload func(ctx chord.Context) error
}

func (p *workerSessionPlugins) setReload(fn func(ctx chord.Context) error) {
	p.mu.Lock()
	p.reload = fn
	p.mu.Unlock()
}

func (p *workerSessionPlugins) Reload(ctx chord.Context) error {
	p.mu.Lock()
	fn := p.reload
	p.mu.Unlock()
	if fn == nil {
		return errors.New("Session plugins are not ready")
	}
	return fn(ctx)
}

type workerServicesImpl struct {
	options       CreateSessionWorkerServicesOptions
	pluginLoader  chord.FacetLoader
	builtins      *chord.LoadedFacets
	mu            sync.Mutex
	loadedPlugins *chord.LoadedFacets
	facetHost     chord.FacetHost
	endpoints     map[string]*workerScopedEndpoint
	reloadQueue   connSerialQueue
	disposed      bool
}

// CreateSessionWorkerServices activates built-in and plugin facets for a Session worker.
func CreateSessionWorkerServices(options CreateSessionWorkerServicesOptions) (SessionWorkerServices, error) {
	modelsFacet, err := CreateModelsServiceFacet(CreateModelsServiceFacetOptions{
		Harness:         options.Harness,
		Conversation:    options.Conversation,
		ModelRuntime:    options.ModelRuntime,
		SettingsManager: options.SettingsManager,
		Context:         chord.BackgroundContext,
	})
	if err != nil {
		return nil, err
	}
	transcriptFacet, err := CreateTranscriptServiceFacet(options.Conversation, chord.BackgroundContext)
	if err != nil {
		return nil, err
	}
	agentControllerFacet := chord.DefineFacet(chord.Facet{
		Id: "@pi/agent-controller-runtime",
		Setup: func(env chord.FacetEnvironment) error {
			env.Provide(AgentControllerService, ProvideAgentController(CreateAgentController(options.Harness, options.Conversation)))
			return nil
		},
	})
	sessionPlugins := &workerSessionPlugins{}
	pluginRuntimeFacet := chord.DefineFacet(chord.Facet{
		Id: "@pi/session-plugins-runtime",
		Setup: func(env chord.FacetEnvironment) error {
			env.Provide(SessionPluginsService, ProvideSessionPlugins(sessionPlugins))
			return nil
		},
	})

	builtins, err := chord.CreateStaticFacetLoader([]chord.Facet{
		agentControllerFacet,
		pluginRuntimeFacet,
		modelsFacet,
		transcriptFacet,
	}).Load()
	if err != nil {
		return nil, err
	}

	pluginLoader := options.FacetLoader
	if pluginLoader == nil {
		pluginLoader = chord.CreateStaticFacetLoader([]chord.Facet{})
	}
	loadedPlugins, err := pluginLoader.Load()
	if err != nil {
		_ = builtins.Dispose()
		return nil, err
	}

	allFacets := make([]chord.Facet, 0, len(builtins.Facets)+len(loadedPlugins.Facets))
	allFacets = append(allFacets, builtins.Facets...)
	allFacets = append(allFacets, loadedPlugins.Facets...)

	facetHost, err := chord.CreateFacetHost(&chord.FacetOptions{Facets: allFacets})
	if err != nil {
		var cleanupErrs []error
		if e := loadedPlugins.Dispose(); e != nil {
			cleanupErrs = append(cleanupErrs, e)
		}
		if e := builtins.Dispose(); e != nil {
			cleanupErrs = append(cleanupErrs, e)
		}
		if len(cleanupErrs) > 0 {
			return nil, fmt.Errorf("Session facets failed to start and clean up: %w", errors.Join(append([]error{err}, cleanupErrs...)...))
		}
		return nil, err
	}

	impl := &workerServicesImpl{
		options:       options,
		pluginLoader:  pluginLoader,
		builtins:      builtins,
		loadedPlugins: loadedPlugins,
		facetHost:     facetHost,
		endpoints:     make(map[string]*workerScopedEndpoint),
	}
	sessionPlugins.setReload(impl.reloadPlugins)
	return impl, nil
}

func (w *workerServicesImpl) reloadPlugins(_ chord.Context) error {
	var err error
	w.reloadQueue.runSync(func() {
		w.mu.Lock()
		disposed := w.disposed
		w.mu.Unlock()
		if disposed {
			err = errors.New("Session worker services are disposed")
			return
		}

		candidate, loadErr := w.pluginLoader.Load()
		if loadErr != nil {
			err = loadErr
			return
		}
		if reloadErr := w.facetHost.Reload(candidate.Facets); reloadErr != nil {
			if cleanupErr := candidate.Dispose(); cleanupErr != nil {
				err = fmt.Errorf("Session plugin reload and cleanup failed: %w", errors.Join(reloadErr, cleanupErr))
				return
			}
			err = reloadErr
			return
		}

		w.mu.Lock()
		retired := w.loadedPlugins
		w.loadedPlugins = candidate
		w.mu.Unlock()

		if retired != nil {
			err = retired.Dispose()
		}
	})
	return err
}

func (w *workerServicesImpl) RemoveSubscriptions(matches func(scope WorkerServiceScope) bool) {
	w.mu.Lock()
	var removed []*workerScopedEndpoint
	for key, entry := range w.endpoints {
		if !matches(entry.scope) {
			continue
		}
		removed = append(removed, entry)
		delete(w.endpoints, key)
	}
	w.mu.Unlock()

	for _, entry := range removed {
		entry.endpoint.Dispose()
	}
}

func (w *workerServicesImpl) Invoke(call chord.ServiceCall, scope WorkerServiceScope, ctx chord.Context) (chord.JsonValue, bool, error) {
	key := workerScopeKey(scope)
	w.mu.Lock()
	if w.disposed {
		w.mu.Unlock()
		return nil, false, errors.New("Session worker services are disposed")
	}
	entry, ok := w.endpoints[key]
	if !ok {
		entry = &workerScopedEndpoint{
			scope:    scope,
			endpoint: chord.CreateRemoteServiceEndpoint(w.facetHost.Services()),
		}
		w.endpoints[key] = entry
	}
	w.mu.Unlock()

	return entry.endpoint.Invoke(call, func(subscriptionId string, update chord.ServiceProviderUpdate, callCtx chord.Context) error {
		if w.options.Publish == nil {
			return nil
		}
		return w.options.Publish(scope, subscriptionId, update, callCtx)
	}, ctx)
}

func (w *workerServicesImpl) Dispose() error {
	w.mu.Lock()
	if w.disposed {
		w.mu.Unlock()
		return nil
	}
	w.disposed = true
	w.mu.Unlock()

	w.RemoveSubscriptions(func(WorkerServiceScope) bool { return true })
	w.reloadQueue.wait()

	var errs []error
	if err := w.facetHost.Dispose(); err != nil {
		errs = append(errs, err)
	}
	w.mu.Lock()
	loaded := w.loadedPlugins
	w.loadedPlugins = nil
	w.mu.Unlock()

	if loaded != nil {
		if err := loaded.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.builtins != nil {
		if err := w.builtins.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose Session facets: %w", errors.Join(errs...))
	}
	return nil
}

func workerScopeKey(scope WorkerServiceScope) string {
	return scope.ServerConnectionId + "\x00" + scope.AttachmentId
}
