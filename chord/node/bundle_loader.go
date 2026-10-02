// Ported from packages/chord/src/node/bundle-loader.ts (pi v1.0.0).

package node

import (
	"net/url"

	"github.com/keejkrej/pi-go/chord"
)

// FacetBundlePath is a filesystem path or a URL (TS `string | URL`).
type FacetBundlePath interface {
	isFacetBundlePath()
}

// FacetBundlePathString is a filesystem path.
type FacetBundlePathString string

func (FacetBundlePathString) isFacetBundlePath() {}

// FacetBundlePathUrl is a URL.
type FacetBundlePathUrl struct {
	Url *url.URL
}

func (FacetBundlePathUrl) isFacetBundlePath() {}

// FacetBundleExternalResolver resolves a host-provided external import.
// A nil result means unresolved (TS undefined).
type FacetBundleExternalResolver func(specifier string) FacetBundlePath

// FacetBundleLoaderOptions selects one manifest entry to load.
type FacetBundleLoaderOptions struct {
	ManifestPath FacetBundlePath `json:"manifestPath"`
	Entry        string          `json:"entry"`
	// VerifyIntegrity verifies the entry's SHA-256 integrity before evaluating it. Defaults to true.
	VerifyIntegrity *bool `json:"verifyIntegrity,omitzero"`
	// ResolveExternal resolves host-provided external imports when the bundle is outside the host's package tree.
	ResolveExternal FacetBundleExternalResolver `json:"resolveExternal,omitzero"`
}

// FacetBundleArtifactLoaderOptions materializes one transported artifact.
type FacetBundleArtifactLoaderOptions struct {
	Artifact any `json:"artifact"`
	// ResolveExternal resolves host-provided external imports against the receiving application.
	ResolveExternal FacetBundleExternalResolver `json:"resolveExternal,omitzero"`
	// TemporaryDirectory is the parent directory for materialized module generations.
	// Defaults to the operating system temp directory.
	TemporaryDirectory *string `json:"temporaryDirectory,omitzero"`
}

// ReadFacetBundleArtifactOptions identifies one manifest entry to read.
type ReadFacetBundleArtifactOptions struct {
	ManifestPath FacetBundlePath `json:"manifestPath"`
	Entry        string          `json:"entry"`
}

// ReadFacetBundleManifest reads and validates a versioned facet bundle manifest.
func ReadFacetBundleManifest(path FacetBundlePath) (*FacetBundleManifest, error) {
	panic("unported: ReadFacetBundleManifest")
}

// ReadFacetBundleArtifact reads and verifies one transportable entry from a facet bundle on disk.
func ReadFacetBundleArtifact(options *ReadFacetBundleArtifactOptions) (*FacetBundleArtifact, error) {
	panic("unported: ReadFacetBundleArtifact")
}

// CreateFacetBundleArtifactLoader materializes a transported artifact and creates a fresh compiled CommonJS generation for each load.
func CreateFacetBundleArtifactLoader(options *FacetBundleArtifactLoaderOptions) (chord.FacetLoader, error) {
	panic("unported: CreateFacetBundleArtifactLoader")
}

// CreateFacetBundleLoader creates a reusable loader for one opaque entry in a facet bundle manifest.
func CreateFacetBundleLoader(options *FacetBundleLoaderOptions) (chord.FacetLoader, error) {
	panic("unported: CreateFacetBundleLoader")
}
