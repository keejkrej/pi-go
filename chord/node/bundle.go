// Ported from packages/chord/src/node/bundle.ts (pi v1.0.0).

package node

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

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
	if err := bundleValidateOptions(options); err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	workingDirectory := cwd
	if options.WorkingDirectory != nil {
		workingDirectory = bundleResolvePath(cwd, *options.WorkingDirectory)
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return nil, err
	}
	outputDirectory := bundleResolvePath(workingDirectory, options.Outdir)
	outputParent := filepath.Dir(outputDirectory)
	if err := os.MkdirAll(outputParent, 0o755); err != nil {
		return nil, err
	}
	temporaryDirectory := filepath.Join(outputParent, fmt.Sprintf(".%s.tmp-%s", filepath.Base(outputDirectory), uuid.NewString()))
	if err := os.Mkdir(temporaryDirectory, 0o755); err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()

	entryNames := make([]string, 0, options.Entries.Len())
	for name := range options.Entries.All() {
		entryNames = append(entryNames, name)
	}
	slices.SortFunc(entryNames, js.LocaleCompare)

	entries := omap.NewMap[string, FacetBundleEntry]()
	for _, entryName := range entryNames {
		source, _ := options.Entries.Get(entryName)
		entry, err := bundleEntry(bundleEntryInput{
			entryName:          entryName,
			source:             bundleResolvePath(workingDirectory, source),
			temporaryDirectory: temporaryDirectory,
			workingDirectory:   workingDirectory,
			options:            options,
		})
		if err != nil {
			return nil, err
		}
		entries.Set(entryName, entry)
	}

	var pluginVersion *string
	if options.Plugin.Version != nil {
		v := *options.Plugin.Version
		pluginVersion = &v
	}
	manifest := FacetBundleManifest{
		Format:        FacetBundleFormat,
		FormatVersion: FacetBundleFormatVersion,
		Plugin: FacetBundlePlugin{
			Id:      options.Plugin.Id,
			Version: pluginVersion,
		},
		Entries: entries,
	}
	encoded, err := jsonx.MarshalIndent(manifest, "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(temporaryDirectory, FacetBundleManifestFile), encoded, 0o644); err != nil {
		return nil, err
	}
	if err := bundleReplaceDirectory(temporaryDirectory, outputDirectory); err != nil {
		return nil, err
	}
	cleanup = false
	return &BundleFacetsResult{
		Manifest:     manifest,
		ManifestPath: filepath.Join(outputDirectory, FacetBundleManifestFile),
	}, nil
}

type bundleEntryInput struct {
	entryName          string
	source             string
	temporaryDirectory string
	workingDirectory   string
	options            *BundleFacetsOptions
}

type bundleMetafile struct {
	Outputs map[string]bundleMetafileOutput
}

type bundleMetafileOutput struct {
	EntryPoint *string
	Imports    []bundleMetafileLink
}

type bundleMetafileLink struct {
	Path     string
	External bool
}

func bundleParseMetafile(raw string) (bundleMetafile, error) {
	parsed, err := jsonx.ParseString(raw)
	if err != nil {
		return bundleMetafile{}, err
	}
	root, ok := parsed.(*jsonx.Object)
	if !ok || root == nil {
		return bundleMetafile{}, errors.New("metafile root must be an object")
	}
	outputsObj, ok := root.GetObject("outputs")
	if !ok || outputsObj == nil {
		return bundleMetafile{}, errors.New("metafile outputs must be an object")
	}
	outputs := make(map[string]bundleMetafileOutput, outputsObj.Len())
	for outPath, val := range outputsObj.All() {
		outObj, ok := val.(*jsonx.Object)
		if !ok || outObj == nil {
			continue
		}
		var out bundleMetafileOutput
		if epVal, hasEP := outObj.Get("entryPoint"); hasEP && epVal != nil {
			if epStr, isStr := epVal.(string); isStr {
				out.EntryPoint = &epStr
			}
		}
		if impArr, hasImp := outObj.GetArray("imports"); hasImp {
			out.Imports = make([]bundleMetafileLink, 0, len(impArr))
			for _, item := range impArr {
				impObj, ok := item.(*jsonx.Object)
				if !ok || impObj == nil {
					continue
				}
				p, _ := impObj.GetString("path")
				ext, _ := impObj.GetBool("external")
				out.Imports = append(out.Imports, bundleMetafileLink{Path: p, External: ext})
			}
		}
		outputs[outPath] = out
	}
	return bundleMetafile{Outputs: outputs}, nil
}

func bundleEntry(input bundleEntryInput) (FacetBundleEntry, error) {
	entryPrefix := "facet-" + bundleShortHash(input.entryName)
	requestedPlatform := FacetBundlePlatformNode
	if input.options.Platform != nil {
		requestedPlatform = *input.options.Platform
	}
	platform, err := bundleMapPlatform(requestedPlatform)
	if err != nil {
		return FacetBundleEntry{}, err
	}
	target, engines, err := bundleBuildTarget(input.options.Target, requestedPlatform)
	if err != nil {
		return FacetBundleEntry{}, err
	}

	external := bundleUniqueStrings(append([]string{"@earendil-works/chord", "@earendil-works/chord/*"}, input.options.External...))
	var defineMap map[string]string
	if input.options.Define != nil {
		defineMap = make(map[string]string, input.options.Define.Len())
		for k, v := range input.options.Define.All() {
			defineMap[k] = v
		}
	}

	emitSourceMap := input.options.SourceMap != nil && *input.options.SourceMap
	sourceMapMode := api.SourceMapNone
	if emitSourceMap {
		sourceMapMode = api.SourceMapExternal
	}
	minify := input.options.Minify != nil && *input.options.Minify

	result := api.Build(api.BuildOptions{
		AbsWorkingDir:     input.workingDirectory,
		Banner:            map[string]string{"js": `"use strict";`},
		Bundle:            true,
		Define:            defineMap,
		EntryNames:        entryPrefix + "-[hash]",
		EntryPoints:       []string{input.source},
		External:          external,
		Format:            api.FormatCommonJS,
		LegalComments:     api.LegalCommentsNone,
		LogLevel:          api.LogLevelSilent,
		Metafile:          true,
		MinifyWhitespace:  minify,
		MinifyIdentifiers: minify,
		MinifySyntax:      minify,
		Outdir:            input.temporaryDirectory,
		OutExtension:      map[string]string{".js": ".cjs"},
		Platform:          platform,
		Sourcemap:         sourceMapMode,
		Supported:         map[string]bool{"dynamic-import": false},
		Target:            target,
		Engines:           engines,
		Write:             true,
	})
	if len(result.Errors) > 0 {
		diagnostics := make([]string, 0, len(result.Errors))
		for _, msg := range result.Errors {
			diagnostics = append(diagnostics, bundleFormatEsbuildMessage(msg))
		}
		detail := "\n" + strings.Join(diagnostics, "\n")
		return FacetBundleEntry{}, fmt.Errorf("Could not bundle facet entry %s%s", input.entryName, detail)
	}
	if result.Metafile == "" {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced no build metadata", input.entryName)
	}

	meta, err := bundleParseMetafile(result.Metafile)
	if err != nil {
		return FacetBundleEntry{}, fmt.Errorf("Could not parse facet entry %s build metadata: %w", input.entryName, err)
	}

	type namedOutput struct {
		path   string
		output bundleMetafileOutput
	}
	var cjsOutputs []namedOutput
	for outPath, output := range meta.Outputs {
		if output.EntryPoint != nil && filepath.Ext(outPath) == ".cjs" {
			cjsOutputs = append(cjsOutputs, namedOutput{path: outPath, output: output})
		}
	}
	if len(cjsOutputs) != 1 {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s did not produce exactly one JavaScript file", input.entryName)
	}
	primary := cjsOutputs[0]
	absoluteOutputPath := bundleResolvePath(input.workingDirectory, primary.path)
	file, err := filepath.Rel(input.temporaryDirectory, absoluteOutputPath)
	if err != nil || file == "" || file == "." || file == ".." || strings.HasPrefix(file, ".."+string(filepath.Separator)) || filepath.Base(file) != file {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced an invalid output path", input.entryName)
	}

	var sourceMap *string
	allowedOutputs := map[string]struct{}{
		absoluteOutputPath: {},
	}
	if emitSourceMap {
		sm := file + ".map"
		sourceMap = &sm
		allowedOutputs[filepath.Join(input.temporaryDirectory, sm)] = struct{}{}
	}
	for outPath := range meta.Outputs {
		resolvedOut := bundleResolvePath(input.workingDirectory, outPath)
		if _, ok := allowedOutputs[resolvedOut]; !ok {
			return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced files other than its JavaScript bundle and source map", input.entryName)
		}
	}

	contents, err := os.ReadFile(absoluteOutputPath)
	if err != nil {
		return FacetBundleEntry{}, err
	}
	if sourceMap != nil {
		if _, err := os.Stat(filepath.Join(input.temporaryDirectory, *sourceMap)); err != nil {
			return FacetBundleEntry{}, err
		}
	}

	digest := sha256.Sum256(contents)
	externalImports := make([]string, 0)
	seenExternal := make(map[string]struct{})
	for _, imp := range primary.output.Imports {
		if !imp.External {
			continue
		}
		if _, ok := seenExternal[imp.Path]; !ok {
			seenExternal[imp.Path] = struct{}{}
			externalImports = append(externalImports, imp.Path)
		}
	}
	slices.Sort(externalImports)

	return FacetBundleEntry{
		File:            file,
		Integrity:       "sha256-" + base64.StdEncoding.EncodeToString(digest[:]),
		ExternalImports: externalImports,
		SourceMap:       sourceMap,
	}, nil
}

func bundleValidateOptions(options *BundleFacetsOptions) error {
	if options == nil {
		return js.NewTypeError("Facet bundle options must not be nil")
	}
	if options.Plugin.Id == "" {
		return js.NewTypeError("Facet bundle plugin ID must not be empty")
	}
	if options.Plugin.Version != nil && *options.Plugin.Version == "" {
		return js.NewTypeError("Facet bundle plugin version must not be empty")
	}
	if options.Entries == nil || options.Entries.Len() == 0 {
		return js.NewTypeError("Facet bundle must contain at least one entry")
	}
	for name, source := range options.Entries.All() {
		if name == "" {
			return js.NewTypeError("Facet bundle entry name must not be empty")
		}
		if source == "" {
			return js.NewTypeError(fmt.Sprintf("Facet bundle entry %s must have a source path", name))
		}
	}
	for _, external := range options.External {
		if external == "" {
			return js.NewTypeError("Facet bundle external import must not be empty")
		}
	}
	if options.Platform != nil {
		if _, err := bundleMapPlatform(*options.Platform); err != nil {
			return err
		}
	}
	return nil
}

func bundleMapPlatform(p FacetBundlePlatform) (api.Platform, error) {
	switch p {
	case FacetBundlePlatformNode:
		return api.PlatformNode, nil
	case FacetBundlePlatformBrowser:
		return api.PlatformBrowser, nil
	case FacetBundlePlatformNeutral:
		return api.PlatformNeutral, nil
	default:
		return 0, js.NewTypeError(fmt.Sprintf("Invalid facet bundle platform: %s", string(p)))
	}
}

var bundleValidTargets = map[string]api.Target{
	"esnext": api.ESNext,
	"es5":    api.ES5,
	"es6":    api.ES2015,
	"es2015": api.ES2015,
	"es2016": api.ES2016,
	"es2017": api.ES2017,
	"es2018": api.ES2018,
	"es2019": api.ES2019,
	"es2020": api.ES2020,
	"es2021": api.ES2021,
	"es2022": api.ES2022,
	"es2023": api.ES2023,
	"es2024": api.ES2024,
	"es2025": api.ES2025,
}

var bundleValidEngines = map[string]api.EngineName{
	"chrome":  api.EngineChrome,
	"deno":    api.EngineDeno,
	"edge":    api.EngineEdge,
	"firefox": api.EngineFirefox,
	"hermes":  api.EngineHermes,
	"ie":      api.EngineIE,
	"ios":     api.EngineIOS,
	"node":    api.EngineNode,
	"opera":   api.EngineOpera,
	"rhino":   api.EngineRhino,
	"safari":  api.EngineSafari,
}

func bundleBuildTarget(targetOpt BundleFacetsTarget, requestedPlatform FacetBundlePlatform) (api.Target, []api.Engine, error) {
	var rawTargets []string
	switch v := targetOpt.(type) {
	case nil:
		if requestedPlatform == FacetBundlePlatformNode {
			rawTargets = []string{"node22.19"}
		} else {
			rawTargets = []string{"es2022"}
		}
	case BundleFacetsTargetString:
		rawTargets = []string{string(v)}
	case BundleFacetsTargetList:
		rawTargets = []string(v)
	default:
		return 0, nil, js.NewTypeError("Invalid facet bundle target")
	}

	var target api.Target
	var engines []api.Engine
outer:
	for _, value := range rawTargets {
		lower := strings.ToLower(value)
		if valid, ok := bundleValidTargets[lower]; ok {
			target = valid
			continue
		}
		for engine, name := range bundleValidEngines {
			if strings.HasPrefix(lower, engine) {
				version := value[len(engine):]
				if version == "" {
					return 0, nil, fmt.Errorf("Target %q is missing a version number", value)
				}
				engines = append(engines, api.Engine{Name: name, Version: version})
				continue outer
			}
		}
		return 0, nil, fmt.Errorf("Invalid target %q", value)
	}
	return target, engines, nil
}

func bundleReplaceDirectory(temporaryDirectory, outputDirectory string) error {
	backupDirectory := fmt.Sprintf("%s.old-%s", outputDirectory, uuid.NewString())
	movedExisting := false
	if err := os.Rename(outputDirectory, backupDirectory); err == nil {
		movedExisting = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryDirectory, outputDirectory); err != nil {
		if movedExisting {
			_ = os.Rename(backupDirectory, outputDirectory)
		}
		return err
	}
	if movedExisting {
		return os.RemoveAll(backupDirectory)
	}
	return nil
}

func bundleShortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:12]
}

func bundleFormatEsbuildMessage(message api.Message) string {
	if message.Location == nil {
		return message.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", message.Location.File, message.Location.Line, message.Location.Column+1, message.Text)
}

func bundleResolvePath(base, target string) string {
	if target == "" {
		return filepath.Clean(base)
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

func bundleUniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
