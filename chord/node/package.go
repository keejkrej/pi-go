// Ported from packages/chord/src/node/package.ts (pi v1.0.0).

package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

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

type packFacetEntryConfig struct {
	source   string
	disabled bool
}

type packMetadata struct {
	packageDirectory string
	packageJsonPath  string
	name             string
	version          string
	peerDependencies []string
	configuredFacets *omap.Map[string, packFacetEntryConfig]
	external         []string
	sourceMap        bool
}

type packChordConfig struct {
	facets    *omap.Map[string, packFacetEntryConfig]
	external  []string
	sourceMap bool
}

// BundleFacetPackage builds a plugin package using package.json metadata and application-provided facet conventions.
func BundleFacetPackage(options *BundleFacetPackageOptions) (*BundleFacetPackageResult, error) {
	if options == nil {
		return nil, js.NewTypeError("Facet package options must not be nil")
	}
	metadata, err := packReadMetadata(options.PackagePath)
	if err != nil {
		return nil, err
	}
	entries, err := packResolveFacetEntries(metadata, options.DefaultFacets)
	if err != nil {
		return nil, err
	}
	external := make([]string, 0, 2*(len(metadata.peerDependencies)+len(metadata.external)))
	for _, specifier := range metadata.peerDependencies {
		external = append(external, specifier, specifier+"/*")
	}
	for _, specifier := range metadata.external {
		external = append(external, specifier, specifier+"/*")
	}
	version := metadata.version
	sourceMap := metadata.sourceMap
	pkgDir := metadata.packageDirectory
	result, err := BundleFacets(&BundleFacetsOptions{
		Plugin: FacetBundlePlugin{
			Id:      metadata.name,
			Version: &version,
		},
		Entries:          entries,
		Outdir:           options.Outdir,
		WorkingDirectory: &pkgDir,
		External:         external,
		SourceMap:        &sourceMap,
	})
	if err != nil {
		return nil, err
	}
	return &BundleFacetPackageResult{
		BundleFacetsResult: *result,
		PackageDirectory:   metadata.packageDirectory,
		PackageJsonPath:    metadata.packageJsonPath,
	}, nil
}

func packReadMetadata(packagePath string) (packMetadata, error) {
	if packagePath == "" {
		return packMetadata{}, js.NewTypeError("Facet package path must not be empty")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return packMetadata{}, err
	}
	candidate := bundleResolvePath(cwd, packagePath)
	candidateStats, err := os.Stat(candidate)
	if err != nil {
		return packMetadata{}, fmt.Errorf("Could not access facet package %s: %w", candidate, err)
	}

	var packageDirectory string
	var packageJsonPath string
	if candidateStats.IsDir() {
		packageDirectory, err = filepath.EvalSymlinks(candidate)
		if err != nil {
			return packMetadata{}, fmt.Errorf("Could not resolve facet package directory %s: %w", candidate, err)
		}
		packageJsonPath = filepath.Join(packageDirectory, "package.json")
	} else if candidateStats.Mode().IsRegular() && filepath.Base(candidate) == "package.json" {
		packageJsonPath, err = filepath.EvalSymlinks(candidate)
		if err != nil {
			return packMetadata{}, fmt.Errorf("Could not resolve facet package metadata %s: %w", candidate, err)
		}
		packageDirectory = filepath.Dir(packageJsonPath)
	} else {
		return packMetadata{}, fmt.Errorf("Facet package path must name a directory or package.json: %s", candidate)
	}

	raw, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return packMetadata{}, fmt.Errorf("Could not read facet package metadata %s: %w", packageJsonPath, err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return packMetadata{}, fmt.Errorf("Could not read facet package metadata %s: %w", packageJsonPath, err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		return packMetadata{}, fmt.Errorf("Facet package metadata must be an object: %s", packageJsonPath)
	}
	nameVal, _ := obj.Get("name")
	name, ok := nameVal.(string)
	if !ok || name == "" {
		return packMetadata{}, fmt.Errorf("Facet package must have a non-empty name: %s", packageJsonPath)
	}
	versionVal, _ := obj.Get("version")
	version, ok := versionVal.(string)
	if !ok || version == "" {
		return packMetadata{}, fmt.Errorf("Facet package must have a non-empty version: %s", packageJsonPath)
	}

	peerRaw, hasPeer := obj.Get("peerDependencies")
	peerDependencies, err := packParsePeerDependencies(peerRaw, hasPeer, packageJsonPath)
	if err != nil {
		return packMetadata{}, err
	}
	chordRaw, hasChord := obj.Get("chord")
	chordCfg, err := packParseChordConfiguration(chordRaw, hasChord, packageJsonPath)
	if err != nil {
		return packMetadata{}, err
	}

	return packMetadata{
		packageDirectory: packageDirectory,
		packageJsonPath:  packageJsonPath,
		name:             name,
		version:          version,
		peerDependencies: peerDependencies,
		configuredFacets: chordCfg.facets,
		external:         chordCfg.external,
		sourceMap:        chordCfg.sourceMap,
	}, nil
}

func packParsePeerDependencies(value any, present bool, packageJsonPath string) ([]string, error) {
	if !present {
		return nil, nil
	}
	obj, ok := value.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("Facet package has invalid peerDependencies: %s", packageJsonPath)
	}
	names := make([]string, 0, obj.Len())
	for name, version := range obj.All() {
		if name == "" {
			return nil, fmt.Errorf("Facet package has invalid peerDependencies: %s", packageJsonPath)
		}
		if _, isStr := version.(string); !isStr {
			return nil, fmt.Errorf("Facet package has invalid peerDependencies: %s", packageJsonPath)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func packParseChordConfiguration(value any, present bool, packageJsonPath string) (packChordConfig, error) {
	if !present {
		return packChordConfig{
			facets:    omap.NewMap[string, packFacetEntryConfig](),
			sourceMap: true,
		}, nil
	}
	obj, ok := value.(*jsonx.Object)
	if !ok {
		return packChordConfig{}, fmt.Errorf("Facet package chord configuration must be an object: %s", packageJsonPath)
	}
	for key := range obj.All() {
		if key != "facets" && key != "external" && key != "sourceMap" {
			return packChordConfig{}, fmt.Errorf("Facet package chord configuration has an unknown field: %s", packageJsonPath)
		}
	}

	facets := omap.NewMap[string, packFacetEntryConfig]()
	if facetsVal, hasFacets := obj.Get("facets"); hasFacets {
		facetsObj, ok := facetsVal.(*jsonx.Object)
		if !ok {
			return packChordConfig{}, fmt.Errorf("Facet package chord.facets must be an object: %s", packageJsonPath)
		}
		for name, source := range facetsObj.All() {
			if name == "" {
				return packChordConfig{}, fmt.Errorf("Facet package has an invalid chord.facets entry: %s", packageJsonPath)
			}
			switch v := source.(type) {
			case string:
				if v == "" {
					return packChordConfig{}, fmt.Errorf("Facet package has an invalid chord.facets entry: %s", packageJsonPath)
				}
				facets.Set(name, packFacetEntryConfig{source: v})
			case bool:
				if v {
					return packChordConfig{}, fmt.Errorf("Facet package has an invalid chord.facets entry: %s", packageJsonPath)
				}
				facets.Set(name, packFacetEntryConfig{disabled: true})
			default:
				return packChordConfig{}, fmt.Errorf("Facet package has an invalid chord.facets entry: %s", packageJsonPath)
			}
		}
	}

	var external []string
	if extVal, hasExternal := obj.Get("external"); hasExternal {
		extArr, ok := extVal.([]any)
		if !ok {
			return packChordConfig{}, fmt.Errorf("Facet package chord.external must contain non-empty strings: %s", packageJsonPath)
		}
		seen := make(map[string]struct{}, len(extArr))
		external = make([]string, 0, len(extArr))
		for _, item := range extArr {
			s, isStr := item.(string)
			if !isStr || s == "" {
				return packChordConfig{}, fmt.Errorf("Facet package chord.external must contain non-empty strings: %s", packageJsonPath)
			}
			if _, ok := seen[s]; !ok {
				seen[s] = struct{}{}
				external = append(external, s)
			}
		}
		slices.Sort(external)
	}

	sourceMap := true
	if smVal, hasSM := obj.Get("sourceMap"); hasSM {
		b, ok := smVal.(bool)
		if !ok {
			return packChordConfig{}, fmt.Errorf("Facet package chord.sourceMap must be a boolean: %s", packageJsonPath)
		}
		sourceMap = b
	}

	return packChordConfig{
		facets:    facets,
		external:  external,
		sourceMap: sourceMap,
	}, nil
}

func packResolveFacetEntries(metadata packMetadata, defaultFacets *omap.Map[string, string]) (*omap.Map[string, string], error) {
	entries := omap.NewMap[string, string]()
	if defaultFacets != nil {
		for name, source := range defaultFacets.All() {
			if err := packValidateFacetMapping(name, source, "default"); err != nil {
				return nil, err
			}
			path, err := packResolvePackageEntry(metadata.packageDirectory, source, name)
			if err != nil {
				return nil, err
			}
			entryStats, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			if !entryStats.Mode().IsRegular() {
				return nil, fmt.Errorf("Default facet entry %s is not a file: %s", name, path)
			}
			canonicalPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil, err
			}
			if err := packValidateCanonicalPackageEntry(metadata.packageDirectory, canonicalPath, name); err != nil {
				return nil, err
			}
			entries.Set(name, canonicalPath)
		}
	}

	for name, cfg := range metadata.configuredFacets.All() {
		if cfg.disabled {
			entries.Delete(name)
			continue
		}
		if err := packValidateFacetMapping(name, cfg.source, "configured"); err != nil {
			return nil, err
		}
		path, err := packResolvePackageEntry(metadata.packageDirectory, cfg.source, name)
		if err != nil {
			return nil, err
		}
		entryStats, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("Could not access configured facet entry %s: %s: %w", name, path, err)
		}
		if !entryStats.Mode().IsRegular() {
			return nil, fmt.Errorf("Configured facet entry %s is not a file: %s", name, path)
		}
		canonicalPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		if err := packValidateCanonicalPackageEntry(metadata.packageDirectory, canonicalPath, name); err != nil {
			return nil, err
		}
		entries.Set(name, canonicalPath)
	}

	if entries.Len() == 0 {
		return nil, fmt.Errorf("Facet package %s has no configured or conventional facet entries", metadata.name)
	}
	return entries, nil
}

func packValidateFacetMapping(name, source, kind string) error {
	if name == "" {
		return fmt.Errorf("Facet package %s entry name must not be empty", kind)
	}
	if source == "" {
		return fmt.Errorf("Facet package %s entry %s must have a source path", kind, name)
	}
	return nil
}

func packResolvePackageEntry(packageDirectory, source, name string) (string, error) {
	if filepath.IsAbs(source) {
		return "", fmt.Errorf("Facet package entry %s must be relative to the package directory", name)
	}
	path := bundleResolvePath(packageDirectory, source)
	rel, err := filepath.Rel(packageDirectory, path)
	if err != nil || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("Facet package entry %s escapes the package directory", name)
	}
	return path, nil
}

func packValidateCanonicalPackageEntry(packageDirectory, path, name string) error {
	rel, err := filepath.Rel(packageDirectory, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("Facet package entry %s resolves outside the package directory", name)
	}
	return nil
}
