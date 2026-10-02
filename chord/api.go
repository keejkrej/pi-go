// Ported from packages/chord/src/api.ts (pi v1.0.0).

package chord

// CreateFacetHost creates an active host for one complete set of facets.
func CreateFacetHost(options *FacetOptions) (FacetHost, error) {
	panic("unported: CreateFacetHost")
}

func CreateStaticFacetLoader(facets []Facet) FacetLoader {
	panic("unported: CreateStaticFacetLoader")
}

func CombineFacetLoaders(loaders []FacetLoader) FacetLoader {
	panic("unported: CombineFacetLoaders")
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
	panic("unported: DefineService")
}

func CreateRemoteServiceBinding(options *RemoteServiceBindingOptions) (RemoteServiceBinding, error) {
	panic("unported: CreateRemoteServiceBinding")
}

// NewReplicatedState creates authoritative state by taking immutable ownership of an alias-free strict-JSON root.
// The caller must not mutate initial after this call.
// It is the initial-value overload of replicatedState.
func NewReplicatedState[T any](initial T) (MutableReplicatedState[T], error) {
	panic("unported: NewReplicatedState")
}

// NewReplicatedStateSource is the ReplicatedStateSource overload of replicatedState.
// Nil options mean the source reports failures on its own.
func NewReplicatedStateSource[T any](source ReplicatedStateSource[T], options *ReplicatedStateSourceOptions) (AttachedReplicatedState[T], error) {
	panic("unported: NewReplicatedStateSource")
}

func apiIsReplicatedStateSource(value any) bool {
	panic("unported: apiIsReplicatedStateSource")
}
