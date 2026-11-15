// Ported from packages/coding-agent/test/experimental-plugin-reload.test.ts (pi v1.0.0).

package services_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
)

type teprFacetLoader struct {
	mu          sync.Mutex
	generation  int
	activations []int
	disposals   []int
}

func (l *teprFacetLoader) Load() (*chord.LoadedFacets, error) {
	l.mu.Lock()
	l.generation++
	current := l.generation
	l.mu.Unlock()

	return &chord.LoadedFacets{
		Facets: []chord.Facet{
			chord.DefineFacet(chord.Facet{
				Id: "reloadable-session-plugin",
				Setup: func(env chord.FacetEnvironment) error {
					env.OnActivate(func() error {
						l.mu.Lock()
						l.activations = append(l.activations, current)
						l.mu.Unlock()
						return nil
					})
					return nil
				},
			}),
		},
		Dispose: func() error {
			l.mu.Lock()
			l.disposals = append(l.disposals, current)
			l.mu.Unlock()
			return nil
		},
	}, nil
}

func TestPluginReloadLoadsAndCutsOverFreshSessionFacetGeneration(t *testing.T) {
	fc, err := testkit.OpenFauxConversation()
	if err != nil {
		t.Fatalf("OpenFauxConversation failed: %v", err)
	}
	defer func() { _ = fc.Close() }()

	loader := &teprFacetLoader{}
	workerServices, err := services.CreateSessionWorkerServices(services.CreateSessionWorkerServicesOptions{
		Harness:      fc.Harness,
		Conversation: fc.Conversation,
		FacetLoader:  loader,
		Publish: func(_ services.WorkerServiceScope, _ string, _ chord.ServiceProviderUpdate, _ chord.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateSessionWorkerServices failed: %v", err)
	}

	loader.mu.Lock()
	if !reflect.DeepEqual(loader.activations, []int{1}) {
		loader.mu.Unlock()
		t.Fatalf("expected activations [1], got %v", loader.activations)
	}
	loader.mu.Unlock()

	if _, _, err := workerServices.Invoke(
		chord.ServiceCall{ServiceId: services.SessionPluginsService.Id, Member: "reload", Args: []chord.JsonValue{}},
		services.WorkerServiceScope{ServerConnectionId: "server-1", AttachmentId: "attachment-1"},
		chord.BackgroundContext,
	); err != nil {
		t.Fatalf("reload invoke failed: %v", err)
	}

	loader.mu.Lock()
	if !reflect.DeepEqual(loader.activations, []int{1, 2}) || !reflect.DeepEqual(loader.disposals, []int{1}) {
		a, d := loader.activations, loader.disposals
		loader.mu.Unlock()
		t.Fatalf("expected activations=[1 2] disposals=[1], got activations=%v disposals=%v", a, d)
	}
	loader.mu.Unlock()

	if err := workerServices.Dispose(); err != nil {
		t.Fatalf("workerServices.Dispose failed: %v", err)
	}
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if !reflect.DeepEqual(loader.disposals, []int{1, 2}) {
		t.Fatalf("expected disposals [1 2], got %v", loader.disposals)
	}
}
