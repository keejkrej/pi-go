// Ported from packages/coding-agent/src/experimental/plugins/bundled.ts (pi v1.0.0).

package plugins

import (
	"errors"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	bundPresentationFacetBundlesKey = "presentationFacetBundles"
	bundPiPluginApi                 = "@earendil-works/pi-coding-agent/experimental/plugin"
)

type bundOptionalSessionFacetLoader struct {
	manifestPath string
	loader       chord.FacetLoader
}

func (l *bundOptionalSessionFacetLoader) Load() (*chord.LoadedFacets, error) {
	manifest, err := node.ReadFacetBundleManifest(node.FacetBundlePathString(l.manifestPath))
	if err != nil {
		return nil, err
	}
	if _, hasSession := manifest.Entries.Get("session"); hasSession {
		return l.loader.Load()
	}
	return &chord.LoadedFacets{
		Facets: []chord.Facet{},
		Dispose: func() error {
			return nil
		},
	}, nil
}

// CreateSessionPluginFacetLoader returns a combined FacetLoader for all configured manifest paths, or nil if empty.
func CreateSessionPluginFacetLoader(manifestPaths []string) (chord.FacetLoader, error) {
	if len(manifestPaths) == 0 {
		return nil, nil
	}
	loaders := make([]chord.FacetLoader, 0, len(manifestPaths))
	for _, manifestPath := range manifestPaths {
		loader, err := bundCreateOptionalSessionFacetLoader(manifestPath)
		if err != nil {
			return nil, err
		}
		loaders = append(loaders, loader)
	}
	return chord.CombineFacetLoaders(loaders), nil
}

func bundCreateOptionalSessionFacetLoader(manifestPath string) (chord.FacetLoader, error) {
	loader, err := node.CreateFacetBundleLoader(&node.FacetBundleLoaderOptions{
		ManifestPath:    node.FacetBundlePathString(manifestPath),
		Entry:           "session",
		ResolveExternal: bundResolvePluginExternal,
	})
	if err != nil {
		return nil, err
	}
	return &bundOptionalSessionFacetLoader{
		manifestPath: manifestPath,
		loader:       loader,
	}, nil
}

// CreatePresentationFacetData encodes presentation bundle artifacts into a JSON value for transport to clients.
func CreatePresentationFacetData(artifacts []node.FacetBundleArtifact) (chord.JsonValue, error) {
	encoded := make([]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		val, err := jsonx.Encode(artifact)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, val)
	}
	return jsonx.ObjectOf(bundPresentationFacetBundlesKey, encoded), nil
}

// CreatePresentationFacetLoaders creates local facet loaders only from artifacts selected and sent by the connected server.
func CreatePresentationFacetLoaders(data chord.JsonValue) ([]chord.FacetLoader, error) {
	obj, ok := data.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("Invalid presentation plugin data")
	}
	rawArtifacts, exists := obj.Get(bundPresentationFacetBundlesKey)
	if !exists {
		return nil, nil
	}
	artifacts, ok := rawArtifacts.([]any)
	if !ok {
		return nil, errors.New("Invalid presentation plugin bundle list")
	}
	loaders := make([]chord.FacetLoader, 0, len(artifacts))
	for _, artifact := range artifacts {
		loader, err := node.CreateFacetBundleArtifactLoader(&node.FacetBundleArtifactLoaderOptions{
			Artifact:        artifact,
			ResolveExternal: bundResolvePluginExternal,
		})
		if err != nil {
			return nil, err
		}
		loaders = append(loaders, loader)
	}
	return loaders, nil
}

func bundResolvePluginExternal(specifier string) node.FacetBundlePath {
	if specifier != bundPiPluginApi {
		return nil
	}
	return node.FacetBundlePathString(bundPiPluginApi)
}
