// Ported from packages/chord/src/node/package.ts (pi v1.0.0).

package node

import "github.com/keejkrej/pi-go/internal/omap"

// BundleFacetPackageOptions selects a plugin package to bundle.
type BundleFacetPackageOptions struct {
	// PackagePath is the plugin package directory or its package.json path.
	PackagePath string `json:"packagePath"`
	Outdir      string `json:"outdir"`
	// DefaultFacets are application conventions applied when the corresponding source file exists.
	DefaultFacets *omap.Map[string, string] `json:"defaultFacets,omitzero"`
}

// BundleFacetPackageResult is a facet bundle plus the package it was built from.
type BundleFacetPackageResult struct {
	BundleFacetsResult
	PackageDirectory string `json:"packageDirectory"`
	PackageJsonPath  string `json:"packageJsonPath"`
}

// BundleFacetPackage builds a plugin package using package.json metadata and application-provided facet conventions.
func BundleFacetPackage(options *BundleFacetPackageOptions) (*BundleFacetPackageResult, error) {
	panic("unported: BundleFacetPackage")
}
