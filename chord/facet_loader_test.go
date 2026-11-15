// Ported from packages/chord/test/facet-loader.test.ts (pi v1.0.0).

package chord

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

type tflLoaderFunc func() (*LoadedFacets, error)

func (f tflLoaderFunc) Load() (*LoadedFacets, error) {
	return f()
}

type tflGenImpl struct {
	Read func(call Context) (string, error) `json:"read"`
}

type tflRenamedImpl struct {
	Renamed func() string `json:"renamed"`
}

func TestFacetLoader(t *testing.T) {
	localTrue := true
	localGenSvc := tsvcMustDefine[any](t, "test.experimental.local-generation-value", &DefineServiceOptions{Local: &localTrue})
	remoteGenSvc := tsvcMustDefine[any](t, "test.experimental.remote-generation-value")
	firstFacet := DefineFacet(Facet{Id: "first", Setup: func(FacetEnvironment) error { return nil }})
	secondFacet := DefineFacet(Facet{Id: "second", Setup: func(FacetEnvironment) error { return nil }})

	t.Run("combines loaded facets in loader order and disposes generations in reverse", func(t *testing.T) {
		var trace []string
		first := tflLoaderFunc(func() (*LoadedFacets, error) {
			trace = append(trace, "load first")
			return &LoadedFacets{
				Facets: []Facet{firstFacet},
				Dispose: func() error {
					trace = append(trace, "dispose first")
					return nil
				},
			}, nil
		})
		second := tflLoaderFunc(func() (*LoadedFacets, error) {
			trace = append(trace, "load second")
			return &LoadedFacets{
				Facets: []Facet{secondFacet},
				Dispose: func() error {
					trace = append(trace, "dispose second")
					return nil
				},
			}, nil
		})

		loaded, err := CombineFacetLoaders([]FacetLoader{first, second}).Load()
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if len(loaded.Facets) != 2 || loaded.Facets[0].Id != "first" || loaded.Facets[1].Id != "second" {
			t.Fatalf("unexpected facets: %+v", loaded.Facets)
		}
		if err := loaded.Dispose(); err != nil {
			t.Fatalf("Dispose 1 failed: %v", err)
		}
		if err := loaded.Dispose(); err != nil {
			t.Fatalf("Dispose 2 failed: %v", err)
		}
		if !reflect.DeepEqual(trace, []string{"load first", "load second", "dispose second", "dispose first"}) {
			t.Fatalf("unexpected trace %v", trace)
		}
	})

	t.Run("cleans up loaded facets when a later loader fails", func(t *testing.T) {
		disposeCount := 0
		failure := errors.New("load failed")
		first := tflLoaderFunc(func() (*LoadedFacets, error) {
			return &LoadedFacets{
				Facets: []Facet{firstFacet},
				Dispose: func() error {
					disposeCount++
					return nil
				},
			}, nil
		})
		second := tflLoaderFunc(func() (*LoadedFacets, error) {
			return nil, failure
		})
		if _, err := CombineFacetLoaders([]FacetLoader{first, second}).Load(); !errors.Is(err, failure) {
			t.Fatalf("expected failure error, got %v", err)
		}
		if disposeCount != 1 {
			t.Fatalf("expected disposeCount=1, got %d", disposeCount)
		}
	})

	t.Run("keeps local and RPC service handles stable when their provider facet reloads", func(t *testing.T) {
		var traceMu sync.Mutex
		var trace []string
		pushTrace := func(s string) {
			traceMu.Lock()
			trace = append(trace, s)
			traceMu.Unlock()
		}
		var localValue any
		generation := 0
		replacementStarted := make(chan struct{})
		replacementCanContinue := make(chan struct{})

		consumer := DefineFacet(Facet{
			Id: "consumer",
			Setup: func(env FacetEnvironment) error {
				pushTrace("setup consumer")
				localValue = env.Use(localGenSvc)
				env.OnActivate(func() error {
					res, err := tsvcCall(localValue, "read", BackgroundContext)
					if err != nil {
						return err
					}
					pushTrace("activate consumer:" + res.(string))
					return nil
				})
				env.OnDeactivate(func() error {
					pushTrace("deactivate consumer")
					return nil
				})
				return nil
			},
		})
		loader := tflLoaderFunc(func() (*LoadedFacets, error) {
			name := "B"
			if generation == 0 {
				name = "A"
			}
			generation++
			pushTrace("load " + name)
			return &LoadedFacets{
				Facets: []Facet{
					DefineFacet(Facet{
						Id: "provider",
						Setup: func(env FacetEnvironment) error {
							pushTrace("setup provider " + name)
							impl := tflGenImpl{
								Read: func(_ Context) (string, error) {
									return name, nil
								},
							}
							env.Provide(localGenSvc, impl)
							env.Provide(remoteGenSvc, impl)
							env.OnActivate(func() error {
								pushTrace("activate provider " + name)
								if name == "B" {
									close(replacementStarted)
									<-replacementCanContinue
								}
								return nil
							})
							env.OnDeactivate(func() error {
								pushTrace("deactivate provider " + name)
								return nil
							})
							return nil
						},
					}),
				},
				Dispose: func() error {
					pushTrace("unload " + name)
					return nil
				},
			}, nil
		})

		loadedA, err := loader.Load()
		if err != nil {
			t.Fatalf("load A failed: %v", err)
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: append([]Facet{consumer}, loadedA.Facets...)})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		originalLocalHandle := localValue
		localRead := tsvcMemberGet(t, originalLocalHandle, "read")
		remoteServices, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: remoteGenSvc.Id}},
			Transport: CreateLoopbackServiceTransport(host.Services()),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		originalRemoteHandle := remoteServices.Use(remoteGenSvc)
		remoteRead := tsvcMemberGet(t, originalRemoteHandle, "read")
		if err := remoteServices.Ready(BackgroundContext); err != nil {
			t.Fatalf("remoteServices.Ready failed: %v", err)
		}
		if res, err := tsvcCall(localRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A from localRead, got %v %v", res, err)
		}
		if res, err := tsvcCall(remoteRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A from remoteRead, got %v %v", res, err)
		}

		invalidFacet := DefineFacet(Facet{
			Id: "provider",
			Setup: func(env FacetEnvironment) error {
				env.Provide(localGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return "invalid", nil }})
				return nil
			},
		})
		if err := host.Reload([]Facet{invalidFacet}); err == nil || !strings.Contains(err.Error(), "Reloaded facet provider must preserve its service requirements and provisions") {
			t.Fatalf("expected shape/provision error, got %v", err)
		}
		if res, err := tsvcCall(localRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A preserved after invalid reload, got %v %v", res, err)
		}
		if res, err := tsvcCall(remoteRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A preserved after invalid reload, got %v %v", res, err)
		}

		loadedB, err := loader.Load()
		if err != nil {
			t.Fatalf("load B failed: %v", err)
		}
		reloadDone := make(chan error, 1)
		go func() {
			reloadDone <- host.Reload(loadedB.Facets)
		}()
		<-replacementStarted
		if res, err := tsvcCall(localRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A while B activating, got %v %v", res, err)
		}
		if res, err := tsvcCall(remoteRead, "", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A while B activating, got %v %v", res, err)
		}
		close(replacementCanContinue)
		if err := <-reloadDone; err != nil {
			t.Fatalf("reload failed: %v", err)
		}
		_ = loadedA.Dispose()

		if localValue != originalLocalHandle {
			t.Fatal("expected localValue handle preserved")
		}
		if remoteServices.Use(remoteGenSvc) != originalRemoteHandle {
			t.Fatal("expected remote handle preserved")
		}
		if res, err := tsvcCall(localRead, "", BackgroundContext); err != nil || res != "B" {
			t.Fatalf("expected B from localRead, got %v %v", res, err)
		}
		if res, err := tsvcCall(remoteRead, "", BackgroundContext); err != nil || res != "B" {
			t.Fatalf("expected B from remoteRead, got %v %v", res, err)
		}

		_ = remoteServices.Dispose(BackgroundContext)
		_ = host.Dispose()
		_ = loadedB.Dispose()

		expected := []string{
			"load A",
			"setup consumer",
			"setup provider A",
			"activate provider A",
			"activate consumer:A",
			"load B",
			"setup provider B",
			"activate provider B",
			"deactivate provider A",
			"unload A",
			"deactivate consumer",
			"deactivate provider B",
			"unload B",
		}
		traceMu.Lock()
		got := slices.Clone(trace)
		traceMu.Unlock()
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("expected trace %v, got %v", expected, got)
		}
	})

	t.Run("rejects remote singleton member shape changes before reload cutover", func(t *testing.T) {
		var retained any
		providerDisposed := false
		consumer := DefineFacet(Facet{
			Id: "shape-consumer",
			Setup: func(env FacetEnvironment) error {
				retained = env.Use(remoteGenSvc)
				return nil
			},
		})
		provider := DefineFacet(Facet{
			Id: "shape-provider",
			Setup: func(env FacetEnvironment) error {
				env.Provide(remoteGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return "A", nil }})
				env.OnDeactivate(func() error {
					providerDisposed = true
					return nil
				})
				return nil
			},
		})
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		renamedFacet := DefineFacet(Facet{
			Id: "shape-provider",
			Setup: func(env FacetEnvironment) error {
				env.Provide(remoteGenSvc, tflRenamedImpl{Renamed: func() string { return "B" }})
				return nil
			},
		})
		if err := host.Reload([]Facet{renamedFacet}); err == nil || !strings.Contains(err.Error(), "replacement must preserve its member shape") {
			t.Fatalf("expected member shape error, got %v", err)
		}
		if providerDisposed {
			t.Fatal("expected old provider not disposed before cutover")
		}
		if res, err := tsvcCall(retained, "read", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A retained, got %v %v", res, err)
		}
		_ = host.Dispose()
		if !providerDisposed {
			t.Fatal("expected provider disposed on host.Dispose")
		}
	})

	t.Run("terminates the host when old cleanup fails after cutover", func(t *testing.T) {
		cleanupFailure := errors.New("cleanup failed")
		provider := func(name string, failCleanup bool) Facet {
			return DefineFacet(Facet{
				Id: "cleanup-provider",
				Setup: func(env FacetEnvironment) error {
					env.Provide(remoteGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return name, nil }})
					env.OnDeactivate(func() error {
						if failCleanup {
							return cleanupFailure
						}
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{provider("A", true)}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if err := host.Reload([]Facet{provider("B", false)}); err == nil || !strings.Contains(err.Error(), "Facet reload failed after cutover") {
			t.Fatalf("expected reload failed after cutover, got %v", err)
		}
		if err := host.Reload(nil); err == nil || !strings.Contains(err.Error(), "Facet host cannot reload while dead") {
			t.Fatalf("expected dead host error, got %v", err)
		}
		_ = host.Dispose()
	})

	t.Run("cleans failed candidate activation in reverse dependency order", func(t *testing.T) {
		failure := errors.New("consumer activation failed")
		var trace []string
		provider := func(name string) Facet {
			return DefineFacet(Facet{
				Id: "ordered-provider",
				Setup: func(env FacetEnvironment) error {
					env.Provide(remoteGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return name, nil }})
					env.OnActivate(func() error {
						trace = append(trace, "activate provider "+name)
						return nil
					})
					env.OnDeactivate(func() error {
						trace = append(trace, "deactivate provider "+name)
						return nil
					})
					return nil
				},
			})
		}
		consumer := func(name string, fail bool) Facet {
			return DefineFacet(Facet{
				Id: "ordered-consumer",
				Setup: func(env FacetEnvironment) error {
					env.Use(remoteGenSvc)
					env.OnActivate(func() error {
						trace = append(trace, "activate consumer "+name)
						if fail {
							return failure
						}
						return nil
					})
					env.OnDeactivate(func() error {
						trace = append(trace, "deactivate consumer "+name)
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer("A", false), provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		trace = nil

		if err := host.Reload([]Facet{consumer("B", true), provider("B")}); !errors.Is(err, failure) {
			t.Fatalf("expected consumer activation failed, got %v", err)
		}
		expected := []string{
			"activate provider B",
			"activate consumer B",
			"deactivate consumer B",
			"deactivate provider B",
		}
		if !reflect.DeepEqual(trace, expected) {
			t.Fatalf("expected trace %v, got %v", expected, trace)
		}
		_ = host.Dispose()
	})

	t.Run("terminates the host when replacement publication fails after cutover", func(t *testing.T) {
		publicationFailure := errors.New("publication failed")
		provider := func(name string) Facet {
			return DefineFacet(Facet{
				Id: "publication-provider",
				Setup: func(env FacetEnvironment) error {
					env.Provide(remoteGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return name, nil }})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{provider("A")}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		sub, err := host.Services().Subscribe(remoteGenSvc.Id, ServiceModeSingleton, func(_ ServiceProviderUpdate, _ Context) error {
			return publicationFailure
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

	t.Run("keeps the old generation active when replacement activation fails before cutover", func(t *testing.T) {
		failure := errors.New("replacement activation failed")
		var trace []string
		var retained any
		consumer := DefineFacet(Facet{
			Id: "terminal-consumer",
			Setup: func(env FacetEnvironment) error {
				retained = env.Use(remoteGenSvc)
				env.OnDeactivate(func() error {
					trace = append(trace, "deactivate consumer")
					return nil
				})
				return nil
			},
		})
		provider := func(name string, fail bool) Facet {
			return DefineFacet(Facet{
				Id: "terminal-provider",
				Setup: func(env FacetEnvironment) error {
					env.Provide(remoteGenSvc, tflGenImpl{Read: func(_ Context) (string, error) { return name, nil }})
					env.OnActivate(func() error {
						trace = append(trace, "activate "+name)
						if fail {
							return failure
						}
						return nil
					})
					env.OnDeactivate(func() error {
						trace = append(trace, "deactivate "+name)
						return nil
					})
					return nil
				},
			})
		}
		host, err := CreateFacetHost(&FacetOptions{Facets: []Facet{consumer, provider("A", false)}})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if res, err := tsvcCall(retained, "read", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A, got %v %v", res, err)
		}
		if err := host.Reload([]Facet{provider("B", true)}); !errors.Is(err, failure) {
			t.Fatalf("expected failure error, got %v", err)
		}
		if !reflect.DeepEqual(trace, []string{"activate A", "activate B", "deactivate B"}) {
			t.Fatalf("unexpected trace %v", trace)
		}
		if res, err := tsvcCall(retained, "read", BackgroundContext); err != nil || res != "A" {
			t.Fatalf("expected A retained after failed reload, got %v %v", res, err)
		}
		if err := host.Reload(nil); err != nil {
			t.Fatalf("empty Reload failed: %v", err)
		}
		_ = host.Dispose()
		expected := []string{"activate A", "activate B", "deactivate B", "deactivate consumer", "deactivate A"}
		if !reflect.DeepEqual(trace, expected) {
			t.Fatalf("expected trace %v, got %v", expected, trace)
		}
	})

	t.Run("creates a reusable static loader", func(t *testing.T) {
		loader := CreateStaticFacetLoader([]Facet{firstFacet})
		first, err := loader.Load()
		if err != nil {
			t.Fatalf("Load 1 failed: %v", err)
		}
		second, err := loader.Load()
		if err != nil {
			t.Fatalf("Load 2 failed: %v", err)
		}
		if len(first.Facets) != 1 || first.Facets[0].Id != "first" || len(second.Facets) != 1 || second.Facets[0].Id != "first" {
			t.Fatalf("unexpected static facets: %+v %+v", first.Facets, second.Facets)
		}
		if err := first.Dispose(); err != nil {
			t.Fatalf("first.Dispose failed: %v", err)
		}
		if err := second.Dispose(); err != nil {
			t.Fatalf("second.Dispose failed: %v", err)
		}
	})
}
