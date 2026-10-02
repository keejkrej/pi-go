// Ported from packages/chord/src/node/manifest.ts (pi v1.0.0).

package node

import "github.com/keejkrej/pi-go/internal/omap"

const (
	FacetBundleFormat                = "chord.facet-bundle"
	FacetBundleFormatVersion         = 2
	FacetBundleManifestFile          = "chord-facets.json"
	FacetBundleArtifactFormat        = "chord.facet-bundle-artifact"
	FacetBundleArtifactFormatVersion = 2
)

// FacetBundleEntry is one content-addressed file named by a facet bundle manifest.
type FacetBundleEntry struct {
	// File is the content-addressed CommonJS filename relative to the manifest.
	File string `json:"file"`
	// Integrity is the SHA-256 subresource-integrity value for the JavaScript file.
	Integrity string `json:"integrity"`
	// ExternalImports are imports intentionally left for the loading application to resolve.
	ExternalImports []string `json:"externalImports"`
	// SourceMap is the source map filename relative to the manifest, when emitted.
	SourceMap *string `json:"sourceMap,omitzero"`
}

// FacetBundlePlugin is the plugin identity stored in a facet bundle.
type FacetBundlePlugin struct {
	Id      string  `json:"id"`
	Version *string `json:"version,omitzero"`
}

// FacetBundleManifest is a versioned facet bundle manifest.
type FacetBundleManifest struct {
	Format        string                              `json:"format"`
	FormatVersion int                                 `json:"formatVersion"`
	Plugin        FacetBundlePlugin                   `json:"plugin"`
	Entries       *omap.Map[string, FacetBundleEntry] `json:"entries"`
}

// FacetBundleArtifact is one self-contained manifest entry suitable for storage or transport to another Node host.
type FacetBundleArtifact struct {
	Format            string            `json:"format"`
	FormatVersion     int               `json:"formatVersion"`
	Plugin            FacetBundlePlugin `json:"plugin"`
	EntryName         string            `json:"entryName"`
	Entry             FacetBundleEntry  `json:"entry"`
	Source            string            `json:"source"`
	SourceMapContents *string           `json:"sourceMapContents,omitzero"`
}
