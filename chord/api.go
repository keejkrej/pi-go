// Ported from packages/chord/src/api.ts (pi v1.0.0).

package chord

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
)

type apiFacetHost struct {
	kernel *FacetKernel
}

func (h *apiFacetHost) Services() *RemoteServiceProvider {
	p, err := h.kernel.Provider()
	if err != nil {
		panic(err)
	}
	return p
}

func (h *apiFacetHost) Reload(facets []Facet) error {
	return h.kernel.Reload(facets)
}

func (h *apiFacetHost) Dispose() error {
	return h.kernel.Dispose()
}

type apiStaticFacetLoader struct {
	facets []Facet
}

func (l *apiStaticFacetLoader) Load() (*LoadedFacets, error) {
	return &LoadedFacets{
		Facets:  slices.Clone(l.facets),
		Dispose: func() error { return nil },
	}, nil
}

type apiCombinedFacetLoader struct {
	loaders []FacetLoader
}

func (l *apiCombinedFacetLoader) Load() (*LoadedFacets, error) {
	loaded := make([]*LoadedFacets, 0, len(l.loaders))
	var facets []Facet
	for _, loader := range l.loaders {
		if loader == nil {
			rev := slices.Clone(loaded)
			slices.Reverse(rev)
			cleanupErrs, _ := DisposeLoadedFacets(rev)
			err := js.NewTypeError("Facet loader must not be nil")
			if len(cleanupErrs) > 0 {
				return nil, ssJoinErrors("Facet loader failed during load and cleanup", append([]any{err}, cleanupErrs...))
			}
			return nil, err
		}
		entry, err := loader.Load()
		if err != nil {
			rev := slices.Clone(loaded)
			slices.Reverse(rev)
			cleanupErrs, _ := DisposeLoadedFacets(rev)
			if len(cleanupErrs) > 0 {
				return nil, ssJoinErrors("Facet loader failed during load and cleanup", append([]any{err}, cleanupErrs...))
			}
			return nil, err
		}
		loaded = append(loaded, entry)
		if entry != nil {
			facets = append(facets, entry.Facets...)
		}
	}

	var mu sync.Mutex
	disposed := false
	return &LoadedFacets{
		Facets: facets,
		Dispose: func() error {
			mu.Lock()
			if disposed {
				mu.Unlock()
				return nil
			}
			disposed = true
			mu.Unlock()

			rev := slices.Clone(loaded)
			slices.Reverse(rev)
			errs, _ := DisposeLoadedFacets(rev)
			return ssThrowCollectedErrors(errs, "Failed to dispose loaded facets")
		},
	}, nil
}

// CreateFacetHost creates an active host for one complete set of facets.
func CreateFacetHost(options *FacetOptions) (FacetHost, error) {
	kernel, err := NewFacetKernel(options)
	if err != nil {
		return nil, err
	}
	if err := kernel.Activate(); err != nil {
		return nil, err
	}
	return &apiFacetHost{kernel: kernel}, nil
}

func CreateStaticFacetLoader(facets []Facet) FacetLoader {
	return &apiStaticFacetLoader{facets: slices.Clone(facets)}
}

func CombineFacetLoaders(loaders []FacetLoader) FacetLoader {
	return &apiCombinedFacetLoader{loaders: slices.Clone(loaders)}
}

func DefineFacet(facet Facet) Facet {
	return facet
}

// DefineServiceOptions is the optional flag bag for DefineService.
// Nil options mean the service is not process-local.
type DefineServiceOptions struct {
	Local *bool `json:"local,omitzero"`
}

func DefineService[T any](id string, options *DefineServiceOptions) (*Service[T], error) {
	if id == "" {
		return nil, js.NewTypeError("Service ID must not be empty")
	}
	// TODO: check if the reserved namespace should be part of Chord.
	if strings.HasPrefix(id, "$chord.") {
		return nil, js.NewTypeError("Service IDs beginning with $chord. are reserved")
	}
	local := false
	if options != nil && options.Local != nil {
		local = *options.Local
	}
	return &Service[T]{Id: id, Local: local}, nil
}

func CreateRemoteServiceBinding(options *RemoteServiceBindingOptions) (RemoteServiceBinding, error) {
	return NewRemoteServiceBindingImpl(options)
}

// NewReplicatedState creates authoritative state by taking immutable ownership of an alias-free strict-JSON root.
// The caller must not mutate initial after this call.
// It is the initial-value overload of replicatedState.
func NewReplicatedState[T any](initial T) (MutableReplicatedState[T], error) {
	return NewMutableReplicatedStateImpl(initial), nil
}

// NewReplicatedStateSource is the ReplicatedStateSource overload of replicatedState.
// Nil options mean the source reports failures on its own.
func NewReplicatedStateSource[T any](source ReplicatedStateSource[T], options *ReplicatedStateSourceOptions) (AttachedReplicatedState[T], error) {
	if source == nil {
		return nil, js.NewTypeError("Replicated state source must not be nil")
	}
	return AttachReplicatedStateSource(source, options)
}

func apiIsReplicatedStateSource(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	return rv.IsValid() && rv.MethodByName("Attach").IsValid()
}
