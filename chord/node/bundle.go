// Ported from packages/chord/src/node/bundle.ts (pi v1.0.0).

package node

import "github.com/keejkrej/pi-go/internal/omap"

// FacetBundlePlatform is the esbuild platform for a facet bundle.
type FacetBundlePlatform string

const (
	FacetBundlePlatformNode    FacetBundlePlatform = "node"
	FacetBundlePlatformBrowser FacetBundlePlatform = "browser"
	FacetBundlePlatformNeutral FacetBundlePlatform = "neutral"
)

// BundleFacetsTarget is an esbuild target (TS `string | readonly string[]`).
// A nil BundleFacetsTarget means the option was omitted.
type BundleFacetsTarget interface {
	isBundleFacetsTarget()
}

// BundleFacetsTargetString is a single esbuild target.
type BundleFacetsTargetString string

func (BundleFacetsTargetString) isBundleFacetsTarget() {}

// BundleFacetsTargetList is a list of esbuild targets.
type BundleFacetsTargetList []string

func (BundleFacetsTargetList) isBundleFacetsTarget() {}

// BundleFacetsOptions selects facet sources and bundle settings.
type BundleFacetsOptions struct {
	Plugin FacetBundlePlugin `json:"plugin"`
	// Entries are opaque application-selected entry names mapped to TypeScript or JavaScript source files.
	Entries          *omap.Map[string, string] `json:"entries"`
	Outdir           string                    `json:"outdir"`
	WorkingDirectory *string                   `json:"workingDirectory,omitzero"`
	// External lists additional package imports intentionally left for the loading application to resolve.
	External  []string                  `json:"external,omitzero"`
	SourceMap *bool                     `json:"sourceMap,omitzero"`
	Minify    *bool                     `json:"minify,omitzero"`
	Define    *omap.Map[string, string] `json:"define,omitzero"`
	Platform  *FacetBundlePlatform      `json:"platform,omitzero"`
	Target    BundleFacetsTarget        `json:"target,omitzero"`
}

// BundleFacetsResult is the manifest written by BundleFacets.
type BundleFacetsResult struct {
	Manifest     FacetBundleManifest `json:"manifest"`
	ManifestPath string              `json:"manifestPath"`
}

// BundleFacets bundles each opaque facet entry into an independent content-addressed CommonJS file.
func BundleFacets(options *BundleFacetsOptions) (*BundleFacetsResult, error) {
	panic("unported: BundleFacets")
}
