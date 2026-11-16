// Ported from packages/chord/test/facets.test.ts (pi v1.0.0).

package chord

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type tfacetsHostValues struct {
	Name string `json:"name"`
	Use  string `json:"use"`
}

func tfacetsBoolPtr(b bool) *bool { return &b }

type tfacetsSource struct {
	acceptsUnavailable bool
	catalogueFn        func(call Context) ([]ServiceCatalogueEntry, error)
	openFn             func(options *RemoteServiceSourceOpenOptions) RemoteServices
}

func (s *tfacetsSource) AcceptsUnavailableServices() bool { return s.acceptsUnavailable }

func (s *tfacetsSource) Catalogue(call Context) ([]ServiceCatalogueEntry, error) {
	return s.catalogueFn(call)
}

func (s *tfacetsSource) Open(options *RemoteServiceSourceOpenOptions) RemoteServices {
	return s.openFn(options)
}

type tfacetsCustomRemoteServices struct {
	RemoteServices
	readyFn   func(call Context) error
	disposeFn func(call Context) error
}

func (s *tfacetsCustomRemoteServices) Ready(call Context) error {
	if s.readyFn != nil {
		return s.readyFn(call)
	}
	return s.RemoteServices.Ready(call)
}

func (s *tfacetsCustomRemoteServices) Dispose(call Context) error {
	if s.disposeFn != nil {
		return s.disposeFn(call)
	}
	return s.RemoteServices.Dispose(call)
}

type tfacetsReadImpl struct {
	Read func(call Context) (string, error) `json:"read"`
}

func tfacetsProviderUse(host FacetHost, svc any) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ssToError(r)
		}
	}()
	return host.Services().Use(svc)
}

func TestFacetHost(t *testing.T) {
	sourceSvc := tservMustDefine[any](t, "test.experimental.source")
	projectionSvc := tservMustDefine[any](t, "test.experimental.projection")
	keyedValueSvc := tservMustDefine[any](t, "test.experimental.keyed-value")
	watchedSvc := tservMustDefine[any](t, "test.experimental.watched")
	localTrue := true
	hostValuesSvc := tservMustDefine[any](t, "test.experimental.host-values", &DefineServiceOptions{Local: &localTrue})
	localKeyedValueSvc := tservMustDefine[any](t, "test.experimental.local-keyed-value", &DefineServiceOptions{Local: &localTrue})
	leftValueSvc := tservMustDefine[any](t, "test.experimental.left-value")
	rightValueSvc := tservMustDefine[any](t, "test.experimental.right-value")
	combinedValueSvc := tservMustDefine[any](t, "test.experimental.combined-value")

	t.Run("discovers setup dependencies before connecting stable service handles", func(t *testing.T) {
		var traceMu sync.Mutex
		var trace []string
		pushTrace := func(s string) {
			traceMu.Lock()
			trace = append(trace, s)
			traceMu.Unlock()
		}
		var sourceHandle any
		projection := DefineFacet(Facet{
			Id: "projection",
			Setup: func(env FacetEnvironment) error {
				pushTrace("setup projection")
				sourceHandle = env.Use(sourceSvc)
				if _, err := tservCall(sourceHandle, "read", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet projection service handles cannot be used while setting_up") {
					t.Fatalf("expected setting_up guard error, got %v", err)
				}
				env.Provide(projectionSvc, tfacetsReadImpl{
					Read: func(ctx Context) (string, error) {
						res, err := tservCall(sourceHandle, "read", ctx)
						if err != nil {
							return "", err
						}
						return res.(string), nil
					},
				})
				env.OnActivate(func() error {
					pushTrace("activate projection")
					return nil
				})
				env.OnDeactivate(func() error {
					pushTrace("dispose projection")
					return nil
				})
				return nil
			},
		})
		source := DefineFacet(Facet{
			Id: "source",
			Setup: func(env FacetEnvironment) error {
				pushTrace("setup source")
				env.Provide(sourceSvc, tfacetsReadImpl{
					Read: func(_ Context) (string, error) {
						return "value", nil
					},
				})
				env.OnActivate(func() error {
					pushTrace("activate source")
					return nil
				})
				env.OnDeactivate(func() error {
					pushTrace("dispose source")
					return nil
				})
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{projection, source}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}

		expected := []string{"setup projection", "setup source", "activate source", "activate projection"}
		traceMu.Lock()
		got := slices.Clone(trace)
		traceMu.Unlock()
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("expected trace %v, got %v", expected, got)
		}
		if res, err := tservCall(sourceHandle, "read", BackgroundContext); err != nil || res != "value" {
			t.Fatalf("expected value from sourceHandle, got %v %v", res, err)
		}
		projSvc, err := tfacetsProviderUse(host, projectionSvc)
		if err != nil {
			t.Fatalf("host.Services().Use failed: %v", err)
		}
		if res, err := tservCall(projSvc, "read", BackgroundContext); err != nil || res != "value" {
			t.Fatalf("expected value from host.Services().Use, got %v %v", res, err)
		}

		if err := host.Dispose(); err != nil {
			t.Fatalf("Dispose failed: %v", err)
		}
		traceMu.Lock()
		tail := slices.Clone(trace[len(trace)-2:])
		traceMu.Unlock()
		if !reflect.DeepEqual(tail, []string{"dispose projection", "dispose source"}) {
			t.Fatalf("expected dispose trace [dispose projection dispose source], got %v", tail)
		}
	})

	t.Run("connects keyed observations only when the observing facet activates", func(t *testing.T) {
		var traceMu sync.Mutex
		var trace []string
		pushTrace := func(s string) {
			traceMu.Lock()
			trace = append(trace, s)
			traceMu.Unlock()
		}
		observer := DefineFacet(Facet{
			Id: "observer",
			Setup: func(env FacetEnvironment) error {
				env.Observe(keyedValueSvc, func(service any, call Context) error {
					res, err := tservCall(service, "read", call)
					if err != nil {
						return err
					}
					pushTrace("observe " + res.(string))
					return nil
				})
				env.OnActivate(func() error {
					pushTrace("activate observer")
					return nil
				})
				return nil
			},
		})
		provider := DefineFacet(Facet{
			Id: "provider",
			Setup: func(env FacetEnvironment) error {
				values := env.ProvideMany(keyedValueSvc).(ServiceSpawner[any])
				env.OnActivate(func() error {
					pushTrace("activate provider")
					values.Spawn("one", tfacetsReadImpl{
						Read: func(_ Context) (string, error) {
							return "one", nil
						},
					})
					return nil
				})
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{observer, provider}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		tservWaitFor(t, "observe one", func() bool {
			traceMu.Lock()
			defer traceMu.Unlock()
			return slices.Contains(trace, "observe one")
		})
		traceMu.Lock()
		got := slices.Clone(trace)
		traceMu.Unlock()
		if !reflect.DeepEqual(got, []string{"activate provider", "activate observer", "observe one"}) {
			t.Fatalf("unexpected trace %v", got)
		}

		remoteServices, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: keyedValueSvc.Id}},
			Transport: CreateLoopbackServiceTransport(host.Services()),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		var remoteMu sync.Mutex
		var remoteValues []string
		remoteServices.Observe(keyedValueSvc, func(service any, call Context) error {
			res, err := tservCall(service, "read", call)
			if err != nil {
				return err
			}
			remoteMu.Lock()
			remoteValues = append(remoteValues, res.(string))
			remoteMu.Unlock()
			return nil
		})
		if err := remoteServices.Ready(BackgroundContext); err != nil {
			t.Fatalf("remoteServices.Ready failed: %v", err)
		}
		tservWaitFor(t, "remote observed one", func() bool {
			remoteMu.Lock()
			defer remoteMu.Unlock()
			return reflect.DeepEqual(remoteValues, []string{"one"})
		})
		_ = remoteServices.Dispose(BackgroundContext)
		_ = host.Dispose()
	})

	t.Run("routes remotely exposable keyed services through the host provider", func(t *testing.T) {
		type obs struct {
			service any
			ctx     Context
		}
		var obsMu sync.Mutex
		var observed []obs
		consumer := DefineFacet(Facet{
			Id: "remote-keyed-consumer",
			Setup: func(env FacetEnvironment) error {
				env.Observe(keyedValueSvc, func(service any, call Context) error {
					obsMu.Lock()
					observed = append(observed, obs{service: service, ctx: call})
					obsMu.Unlock()
					return nil
				})
				return nil
			},
		})
		provider := func(value string) Facet {
			return DefineFacet(Facet{
				Id: "remote-keyed-provider",
				Setup: func(env FacetEnvironment) error {
					values := env.ProvideMany(keyedValueSvc).(ServiceSpawner[any])
					env.OnActivate(func() error {
						values.Spawn("current", tfacetsReadImpl{
							Read: func(_ Context) (string, error) {
								return value, nil
							},
						})
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		tservWaitFor(t, "first remote keyed observed", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 1
		})
		obsMu.Lock()
		first := observed[0]
		obsMu.Unlock()
		if res, err := tservCall(first.service, "read", first.ctx); err != nil || res != "A" {
			t.Fatalf("expected A, got %v %v", res, err)
		}

		if err := host.Reload([]Facet{provider("B")}); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
		tservWaitFor(t, "second remote keyed observed", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 2
		})
		if first.ctx.AbortSignal() == nil || first.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected first context aborted")
		}
		if _, err := tservCall(first.service, "read", first.ctx); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed error, got %v", err)
		}
		obsMu.Lock()
		second := observed[1]
		obsMu.Unlock()
		if res, err := tservCall(second.service, "read", second.ctx); err != nil || res != "B" {
			t.Fatalf("expected B, got %v %v", res, err)
		}

		_ = host.Dispose()
		if second.ctx.AbortSignal() == nil || second.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected second context aborted")
		}
	})

	t.Run("terminates the host when keyed replacement publication fails", func(t *testing.T) {
		failure := errors.New("spawn publication failed")
		provider := func(value string) Facet {
			return DefineFacet(Facet{
				Id: "failing-keyed-provider",
				Setup: func(env FacetEnvironment) error {
					values := env.ProvideMany(keyedValueSvc).(ServiceSpawner[any])
					env.OnActivate(func() error {
						values.Spawn("current", tfacetsReadImpl{
							Read: func(_ Context) (string, error) {
								return value, nil
							},
						})
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		sub, err := host.Services().Subscribe(keyedValueSvc.Id, ServiceModeKeyed, func(u ServiceProviderUpdate, _ Context) error {
			if _, ok := u.(ServiceProviderUpdateSpawned); ok {
				return failure
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		_ = sub.Activate()

		if err := host.Reload([]Facet{provider("B")}); err == nil || !strings.Contains(err.Error(), "Facet reload failed after cutover") {
			t.Fatalf("expected reload failed after cutover, got %v", err)
		}
		if err := host.Reload(nil); err == nil || !strings.Contains(err.Error(), "Facet host cannot reload while dead") {
			t.Fatalf("expected dead host error, got %v", err)
		}
		if _, err := tfacetsProviderUse(host, keyedValueSvc); err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("expected disposed error from host.Services().Use, got %v", err)
		}
		_ = host.Dispose()
	})

	t.Run("terminates the host when keyed retirement publication fails", func(t *testing.T) {
		failure := errors.New("close publication failed")
		provider := func(value string) Facet {
			return DefineFacet(Facet{
				Id: "failing-keyed-retirement-provider",
				Setup: func(env FacetEnvironment) error {
					values := env.ProvideMany(keyedValueSvc).(ServiceSpawner[any])
					env.OnActivate(func() error {
						values.Spawn("current", tfacetsReadImpl{
							Read: func(_ Context) (string, error) {
								return value, nil
							},
						})
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		sub, err := host.Services().Subscribe(keyedValueSvc.Id, ServiceModeKeyed, func(u ServiceProviderUpdate, _ Context) error {
			if _, ok := u.(ServiceProviderUpdateClosed); ok {
				return failure
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		_ = sub.Activate()

		if err := host.Reload([]Facet{provider("B")}); err == nil || !strings.Contains(err.Error(), "Facet reload failed after cutover") {
			t.Fatalf("expected reload failed after cutover, got %v", err)
		}
		if err := host.Reload(nil); err == nil || !strings.Contains(err.Error(), "Facet host cannot reload while dead") {
			t.Fatalf("expected dead host error, got %v", err)
		}
		_ = host.Dispose()
	})

	t.Run("keeps unrestricted local keyed services process-local across provider reloads", func(t *testing.T) {
		type obs struct {
			service any
			ctx     Context
		}
		var obsMu sync.Mutex
		var observed []obs
		consumer := DefineFacet(Facet{
			Id: "local-keyed-consumer",
			Setup: func(env FacetEnvironment) error {
				env.Observe(localKeyedValueSvc, func(service any, call Context) error {
					obsMu.Lock()
					observed = append(observed, obs{service: service, ctx: call})
					obsMu.Unlock()
					return nil
				})
				return nil
			},
		})
		provider := func(value string) Facet {
			return DefineFacet(Facet{
				Id: "local-keyed-provider",
				Setup: func(env FacetEnvironment) error {
					values := env.ProvideMany(localKeyedValueSvc).(ServiceSpawner[any])
					env.OnActivate(func() error {
						meta := omap.NewMap[string, string]()
						meta.Set("value", value)
						values.Spawn("current", jsonx.ObjectOf(
							"metadata", meta,
							"read", func() string { return value },
						))
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		tservWaitFor(t, "local keyed observed", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 1
		})
		obsMu.Lock()
		first := observed[0]
		obsMu.Unlock()
		if res, err := tservCall(first.service, "read"); err != nil || res != "A" {
			t.Fatalf("expected A, got %v %v", res, err)
		}
		meta1, ok := tservMemberGet(t, first.service, "metadata").(*omap.Map[string, string])
		if !ok {
			t.Fatalf("expected *omap.Map metadata")
		}
		if metaVal, ok := meta1.Get("value"); !ok || metaVal != "A" {
			t.Fatalf("expected metadata A, got %q %v", metaVal, ok)
		}
		for _, entry := range host.Services().Catalogue() {
			if entry.ServiceId == localKeyedValueSvc.Id {
				t.Fatalf("did not expect local service in catalogue: %+v", entry)
			}
		}
		if _, err := tfacetsProviderUse(host, localKeyedValueSvc); err == nil || !strings.Contains(err.Error(), "process-local") {
			t.Fatalf("expected process-local error on host.Services().Use, got %v", err)
		}

		if err := host.Reload([]Facet{provider("B")}); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
		tservWaitFor(t, "local keyed reloaded", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 2
		})
		if first.ctx.AbortSignal() == nil || first.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected first context aborted")
		}
		expectedClosed := "Keyed service " + localKeyedValueSvc.Id + " observation is closed"
		if _, err := tservCall(first.service, "read"); err == nil || !strings.Contains(err.Error(), expectedClosed) {
			t.Fatalf("expected %q, got %v", expectedClosed, err)
		}
		obsMu.Lock()
		second := observed[1]
		obsMu.Unlock()
		if res, err := tservCall(second.service, "read"); err != nil || res != "B" {
			t.Fatalf("expected B, got %v %v", res, err)
		}
		meta2, ok := tservMemberGet(t, second.service, "metadata").(*omap.Map[string, string])
		if !ok {
			t.Fatalf("expected *omap.Map metadata")
		}
		if metaVal, ok := meta2.Get("value"); !ok || metaVal != "B" {
			t.Fatalf("expected metadata B, got %q %v", metaVal, ok)
		}
		_ = host.Dispose()
		if second.ctx.AbortSignal() == nil || second.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected second context aborted on dispose")
		}
	})

	t.Run("keeps remotely exposable local state replicas stable across provider reloads", func(t *testing.T) {
		var sources []MutableReplicatedState[any]
		var revisions []float64
		var watched any
		consumer := DefineFacet(Facet{
			Id: "state-consumer",
			Setup: func(env FacetEnvironment) error {
				watched = env.Use(watchedSvc)
				env.OnActivate(func() error {
					unsub, err := tservStateSubscribe(t, tservMemberGet(t, watched, "state"), func(v JsonValue, _ Context, _ ReplicatedStateDelivery) error {
						revisions = append(revisions, tservGet(v, "value").(float64))
						return nil
					})
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
					return nil
				})
				return nil
			},
		})
		provider := func(value float64) Facet {
			return DefineFacet(Facet{
				Id: "state-provider",
				Setup: func(env FacetEnvironment) error {
					st := env.ReplicatedState(tservObj("value", value))
					sources = append(sources, st)
					env.Provide(watchedSvc, tservObj("state", st))
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider(1)}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		retainedState := tservMemberGet(t, watched, "state")
		if v, ok, _ := tservStateVal(t, retainedState); !ok || tservGet(v, "value") != float64(1) {
			t.Fatalf("expected value 1, got %v %v", v, ok)
		}
		if !reflect.DeepEqual(revisions, []float64{1}) {
			t.Fatalf("expected [1], got %v", revisions)
		}

		if err := host.Reload([]Facet{provider(2)}); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
		if tservMemberGet(t, watched, "state") != retainedState {
			t.Fatal("expected retainedState identity preserved across reload")
		}
		if v, ok, _ := tservStateVal(t, retainedState); !ok || tservGet(v, "value") != float64(2) {
			t.Fatalf("expected value 2, got %v %v", v, ok)
		}
		if !reflect.DeepEqual(revisions, []float64{1, 2}) {
			t.Fatalf("expected [1 2], got %v", revisions)
		}
		_ = sources[0].Change(BackgroundContext, func(draft Draft[any]) error {
			draft.Value.(*jsonx.Object).Set("value", float64(3))
			return nil
		})
		if v, ok, _ := tservStateVal(t, retainedState); !ok || tservGet(v, "value") != float64(2) {
			t.Fatalf("expected detached source not to affect value 2, got %v", v)
		}
		if !reflect.DeepEqual(revisions, []float64{1, 2}) {
			t.Fatalf("expected [1 2], got %v", revisions)
		}

		_ = host.Dispose()
		if _, _, err := tservStateVal(t, retainedState); err == nil || !strings.Contains(err.Error(), "Facet state-consumer service handles cannot be used while dead") {
			t.Fatalf("expected dead guard error, got %v", err)
		}
	})

	t.Run("scopes singleton service views to each facet lifecycle", func(t *testing.T) {
		var consumerHandles []any
		var cleanupValues []string
		var peerHandle any
		consumer := func(generation string) Facet {
			return DefineFacet(Facet{
				Id: "scoped-consumer",
				Setup: func(env FacetEnvironment) error {
					src := env.Use(sourceSvc)
					if env.Use(sourceSvc) != src {
						t.Fatal("expected memoized env.Use view within same setup")
					}
					consumerHandles = append(consumerHandles, src)
					if _, err := tservCall(src, "read", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet scoped-consumer service handles cannot be used while setting_up") {
						t.Fatalf("expected setting_up guard error, got %v", err)
					}
					env.OnDeactivate(func() error {
						res, err := tservCall(src, "read", BackgroundContext)
						if err != nil {
							return err
						}
						cleanupValues = append(cleanupValues, generation+":"+res.(string))
						return nil
					})
					return nil
				},
			})
		}
		peer := DefineFacet(Facet{
			Id: "peer-consumer",
			Setup: func(env FacetEnvironment) error {
				peerHandle = env.Use(sourceSvc)
				return nil
			},
		})
		provider := DefineFacet(Facet{
			Id: "scoped-provider",
			Setup: func(env FacetEnvironment) error {
				env.Provide(sourceSvc, tfacetsReadImpl{
					Read: func(_ Context) (string, error) {
						return "value", nil
					},
				})
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer("A"), peer, provider}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if consumerHandles[0] == peerHandle {
			t.Fatal("expected separate service view per facet")
		}
		retainedOldRead := tservMemberGet(t, consumerHandles[0], "read")

		if err := host.Reload([]Facet{consumer("B")}); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
		if !reflect.DeepEqual(cleanupValues, []string{"A:value"}) {
			t.Fatalf("expected [A:value], got %v", cleanupValues)
		}
		if consumerHandles[1] == consumerHandles[0] {
			t.Fatal("expected new view on consumer reload")
		}
		if _, err := tservCall(consumerHandles[0], "read", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet scoped-consumer service handles cannot be used while dead") {
			t.Fatalf("expected dead guard on old consumer view, got %v", err)
		}
		if _, err := tservCall(retainedOldRead, "", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet scoped-consumer service handles cannot be used while dead") {
			t.Fatalf("expected dead guard on retained old read, got %v", err)
		}
		if res, err := tservCall(consumerHandles[1], "read", BackgroundContext); err != nil || res != "value" {
			t.Fatalf("expected value on new view, got %v %v", res, err)
		}
		if res, err := tservCall(peerHandle, "read", BackgroundContext); err != nil || res != "value" {
			t.Fatalf("expected value on peer view, got %v %v", res, err)
		}

		_ = host.Dispose()
		if !reflect.DeepEqual(cleanupValues, []string{"A:value", "B:value"}) {
			t.Fatalf("expected [A:value B:value], got %v", cleanupValues)
		}
		if _, err := tservCall(consumerHandles[1], "read", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet scoped-consumer service handles cannot be used while dead") {
			t.Fatalf("expected dead guard on consumerHandles[1], got %v", err)
		}
		if _, err := tservCall(peerHandle, "read", BackgroundContext); err == nil || !strings.Contains(err.Error(), "Facet peer-consumer service handles cannot be used while dead") {
			t.Fatalf("expected dead guard on peerHandle, got %v", err)
		}
	})

	t.Run("owns resources registered during activation", func(t *testing.T) {
		var st MutableReplicatedState[any]
		deliveries := 0
		consumer := DefineFacet(Facet{
			Id: "consumer",
			Setup: func(env FacetEnvironment) error {
				watched := env.Use(watchedSvc)
				env.OnActivate(func() error {
					unsub, err := tservStateSubscribe(t, tservMemberGet(t, watched, "state"), func(_ JsonValue, _ Context, _ ReplicatedStateDelivery) error {
						deliveries++
						return nil
					})
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
					return nil
				})
				return nil
			},
		})
		provider := DefineFacet(Facet{
			Id: "provider",
			Setup: func(env FacetEnvironment) error {
				st = env.ReplicatedState(tservObj("value", float64(0)))
				env.Provide(watchedSvc, tservObj("state", st))
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if deliveries != 1 {
			t.Fatalf("expected 1 delivery, got %d", deliveries)
		}
		_ = st.Change(BackgroundContext, func(draft Draft[any]) error {
			draft.Value.(*jsonx.Object).Set("value", float64(1))
			return nil
		})
		if deliveries != 2 {
			t.Fatalf("expected 2 deliveries, got %d", deliveries)
		}
		_ = host.Dispose()
		_ = st.Change(BackgroundContext, func(draft Draft[any]) error {
			draft.Value.(*jsonx.Object).Set("value", float64(2))
			return nil
		})
		if deliveries != 2 {
			t.Fatalf("expected deliveries unchanged at 2 after dispose, got %d", deliveries)
		}
	})

	t.Run("provides arbitrary host services through the facet graph", func(t *testing.T) {
		values := &tfacetsHostValues{Name: "session", Use: "host value"}
		var checked bool
		consumer := DefineFacet(Facet{
			Id: "host-service-consumer",
			Setup: func(env FacetEnvironment) error {
				hostValues := env.Use(hostValuesSvc)
				if _, err := hostValues.(*shServiceView).Get("use"); err == nil || !strings.Contains(err.Error(), "Facet host-service-consumer service handles cannot be used while setting_up") {
					t.Fatalf("expected setting_up guard error on use property, got %v", err)
				}
				env.OnActivate(func() error {
					if hostValues == any(values) {
						t.Fatal("expected view proxy rather than raw values pointer")
					}
					n, err := hostValues.(*shServiceView).Get("name")
					if err != nil || n != "session" {
						t.Fatalf("expected name=session, got %v %v", n, err)
					}
					u, err := hostValues.(*shServiceView).Get("use")
					if err != nil || u != "host value" {
						t.Fatalf("expected use=host value, got %v %v", u, err)
					}
					checked = true
					return nil
				})
				return nil
			},
		})
		provider := DefineFacet(Facet{
			Id: "host-service-provider",
			Setup: func(env FacetEnvironment) error {
				env.Provide(hostValuesSvc, values)
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if !checked {
			t.Fatal("expected activation check to run")
		}
		if _, err := tfacetsProviderUse(host, hostValuesSvc); err == nil || !strings.Contains(err.Error(), "Service test.experimental.host-values is process-local") {
			t.Fatalf("expected process-local error on host.Services().Use, got %v", err)
		}
		_ = host.Dispose()
	})

	t.Run("combines connected services and facet-provided services in one host", func(t *testing.T) {
		leftProvider := tservMustProvider(t, tservEntry(leftValueSvc))
		_ = leftProvider.Provide(leftValueSvc, tfacetsReadImpl{
			Read: func(_ Context) (string, error) { return "left", nil },
		})
		rightProvider := tservMustProvider(t, tservEntry(rightValueSvc))
		_ = rightProvider.Provide(rightValueSvc, tfacetsReadImpl{
			Read: func(_ Context) (string, error) { return "right", nil },
		})
		leftNamespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: leftValueSvc.Id}},
			Transport: CreateLoopbackServiceTransport(leftProvider),
			Bound:     tfacetsBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("leftNamespace failed: %v", err)
		}
		rightNamespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: rightValueSvc.Id}},
			Transport: CreateLoopbackServiceTransport(rightProvider),
			Bound:     tfacetsBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("rightNamespace failed: %v", err)
		}
		wrapSource := func(ns RemoteServiceBinding, p *RemoteServiceProvider) RemoteServiceSource {
			return &tfacetsSource{
				acceptsUnavailable: false,
				catalogueFn: func(_ Context) ([]ServiceCatalogueEntry, error) {
					return p.Catalogue(), nil
				},
				openFn: func(_ *RemoteServiceSourceOpenOptions) RemoteServices {
					return &tfacetsCustomRemoteServices{
						RemoteServices: ns,
						readyFn: func(call Context) error {
							if err := ns.Rebind(true, call); err != nil {
								return err
							}
							return ns.Ready(call)
						},
					}
				},
			}
		}
		facet := DefineFacet(Facet{
			Id: "combined",
			Setup: func(env FacetEnvironment) error {
				left := env.Use(leftValueSvc)
				right := env.Use(rightValueSvc)
				env.Provide(combinedValueSvc, tfacetsReadImpl{
					Read: func(ctx Context) (string, error) {
						lv, err := tservCall(left, "read", ctx)
						if err != nil {
							return "", err
						}
						rv, err := tservCall(right, "read", ctx)
						if err != nil {
							return "", err
						}
						return lv.(string) + " " + rv.(string), nil
					},
				})
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{
			Facets:         []Facet{facet},
			ServiceSources: []RemoteServiceSource{wrapSource(leftNamespace, leftProvider), wrapSource(rightNamespace, rightProvider)},
		})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		combSvc, err := tfacetsProviderUse(host, combinedValueSvc)
		if err != nil {
			t.Fatalf("host.Services().Use failed: %v", err)
		}
		if res, err := tservCall(combSvc, "read", BackgroundContext); err != nil || res != "left right" {
			t.Fatalf("expected 'left right', got %v %v", res, err)
		}
		_ = host.Dispose()
		_ = leftNamespace.Dispose(BackgroundContext)
		_ = rightNamespace.Dispose(BackgroundContext)
		_ = leftProvider.Dispose()
		_ = rightProvider.Dispose()
	})

	t.Run("rejects a service offered by multiple sources", func(t *testing.T) {
		duplicate := &tfacetsSource{
			acceptsUnavailable: false,
			catalogueFn: func(_ Context) ([]ServiceCatalogueEntry, error) {
				return []ServiceCatalogueEntry{{ServiceId: leftValueSvc.Id, Mode: ServiceModeSingleton}}, nil
			},
			openFn: func(_ *RemoteServiceSourceOpenOptions) RemoteServices {
				t.Fatal("Ambiguous sources must not open")
				return nil
			},
		}
		consumer := DefineFacet(Facet{
			Id: "duplicate-consumer",
			Setup: func(env FacetEnvironment) error {
				env.Use(leftValueSvc)
				return nil
			},
		})
		expected := "Facet host service " + leftValueSvc.Id + " is offered by more than one source"
		if _, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{duplicate, duplicate}}); err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected %q, got %v", expected, err)
		}
	})

	t.Run("reopens source bindings from changed catalogues for a replacement generation", func(t *testing.T) {
		leftProvider := tservMustProvider(t, tservEntry(leftValueSvc))
		_ = leftProvider.Provide(leftValueSvc, tfacetsReadImpl{Read: func(_ Context) (string, error) { return "left", nil }})
		rightProvider := tservMustProvider(t, tservEntry(rightValueSvc))
		_ = rightProvider.Provide(rightValueSvc, tfacetsReadImpl{Read: func(_ Context) (string, error) { return "right", nil }})
		currentProvider := leftProvider
		opened := 0
		disposed := 0
		src := &tfacetsSource{
			acceptsUnavailable: false,
			catalogueFn: func(_ Context) ([]ServiceCatalogueEntry, error) {
				return currentProvider.Catalogue(), nil
			},
			openFn: func(options *RemoteServiceSourceOpenOptions) RemoteServices {
				opened++
				ns, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
					Services:     options.Services,
					Transport:    CreateLoopbackServiceTransport(currentProvider),
					Bound:        tfacetsBoolPtr(false),
					AssertAccess: options.AssertAccess,
					OnError:      options.OnError,
				})
				if err != nil {
					panic(err)
				}
				return &tfacetsCustomRemoteServices{
					RemoteServices: ns,
					readyFn: func(call Context) error {
						if err := ns.Rebind(true, call); err != nil {
							return err
						}
						return ns.Ready(call)
					},
					disposeFn: func(call Context) error {
						disposed++
						return ns.Dispose(call)
					},
				}
			},
		}
		var values []string
		leftConsumer := DefineFacet(Facet{
			Id: "left-consumer",
			Setup: func(env FacetEnvironment) error {
				left := env.Use(leftValueSvc)
				env.OnActivate(func() error {
					res, err := tservCall(left, "read", BackgroundContext)
					if err != nil {
						return err
					}
					values = append(values, res.(string))
					return nil
				})
				return nil
			},
		})
		first, err := CreateFacetHost(&FacetOptions{Facets: []Facet{leftConsumer}, ServiceSources: []RemoteServiceSource{src}})
		if err != nil {
			t.Fatalf("first host failed: %v", err)
		}
		_ = first.Dispose()

		currentProvider = rightProvider
		rightConsumer := DefineFacet(Facet{
			Id: "right-consumer",
			Setup: func(env FacetEnvironment) error {
				right := env.Use(rightValueSvc)
				env.OnActivate(func() error {
					res, err := tservCall(right, "read", BackgroundContext)
					if err != nil {
						return err
					}
					values = append(values, res.(string))
					return nil
				})
				return nil
			},
		})
		second, err := CreateFacetHost(&FacetOptions{Facets: []Facet{rightConsumer}, ServiceSources: []RemoteServiceSource{src}})
		if err != nil {
			t.Fatalf("second host failed: %v", err)
		}
		_ = second.Dispose()

		if !reflect.DeepEqual(values, []string{"left", "right"}) {
			t.Fatalf("expected [left right], got %v", values)
		}
		if opened != 2 || disposed != 2 {
			t.Fatalf("expected opened=2 disposed=2, got %d %d", opened, disposed)
		}
		_ = leftProvider.Dispose()
		_ = rightProvider.Dispose()
	})

	t.Run("rejects missing dependencies, cycles, and asynchronous setup", func(t *testing.T) {
		activated := false
		missing := DefineFacet(Facet{
			Id: "missing",
			Setup: func(env FacetEnvironment) error {
				env.Use(sourceSvc)
				env.OnActivate(func() error {
					activated = true
					return nil
				})
				return nil
			},
		})
		expectedMissing := "Facet missing requires local/test.experimental.source/singleton, but no facet provides it"
		if _, err := CreateFacetHost(&FacetOptions{Facets: []Facet{missing}}); err == nil || !strings.Contains(err.Error(), expectedMissing) {
			t.Fatalf("expected %q, got %v", expectedMissing, err)
		}
		if activated {
			t.Fatal("expected missing facet not to activate")
		}

		first := DefineFacet(Facet{
			Id: "first",
			Setup: func(env FacetEnvironment) error {
				env.Use(projectionSvc)
				env.Provide(sourceSvc, tfacetsReadImpl{Read: func(_ Context) (string, error) { return "first", nil }})
				return nil
			},
		})
		second := DefineFacet(Facet{
			Id: "second",
			Setup: func(env FacetEnvironment) error {
				env.Use(sourceSvc)
				env.Provide(projectionSvc, tfacetsReadImpl{Read: func(_ Context) (string, error) { return "second", nil }})
				return nil
			},
		})
		if _, err := CreateFacetHost(&FacetOptions{Facets: []Facet{first, second}}); err == nil || !strings.Contains(err.Error(), "Facet dependency cycle: first, second") {
			t.Fatalf("expected cycle error, got %v", err)
		}
		// Note: Go Facet.Setup has synchronous signature func(FacetEnvironment) error,
		// so JS Promise/async-setup detection has no Go runtime equivalent.
	})
}
