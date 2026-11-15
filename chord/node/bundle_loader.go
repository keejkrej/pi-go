// Ported from packages/chord/src/node/bundle-loader.ts (pi v1.0.0).

package node

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
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
	manifestPath, err := blToFilePath(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read facet bundle manifest %s: %w", manifestPath, err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("Could not read facet bundle manifest %s: %w", manifestPath, err)
	}
	return blValidateManifest(parsed, manifestPath)
}

// ReadFacetBundleArtifact reads and verifies one transportable entry from a facet bundle on disk.
func ReadFacetBundleArtifact(options *ReadFacetBundleArtifactOptions) (*FacetBundleArtifact, error) {
	if options == nil {
		return nil, js.NewTypeError("Read facet bundle artifact options must not be nil")
	}
	if options.Entry == "" {
		return nil, js.NewTypeError("Facet bundle entry name must not be empty")
	}
	manifestPath, err := blToFilePath(options.ManifestPath)
	if err != nil {
		return nil, err
	}
	manifest, err := ReadFacetBundleManifest(FacetBundlePathString(manifestPath))
	if err != nil {
		return nil, err
	}
	entry, ok := manifest.Entries.Get(options.Entry)
	if !ok {
		return nil, fmt.Errorf("Facet bundle %s has no entry named %s", manifest.Plugin.Id, options.Entry)
	}
	modulePath, err := blResolveBundleFile(manifestPath, entry.File, "entry")
	if err != nil {
		return nil, err
	}
	sourceBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, err
	}
	source := string(sourceBytes)
	if err := blVerifySource(source, entry); err != nil {
		return nil, err
	}
	var sourceMapContents *string
	if entry.SourceMap != nil {
		smPath, err := blResolveBundleFile(manifestPath, *entry.SourceMap, "source map")
		if err != nil {
			return nil, err
		}
		smBytes, err := os.ReadFile(smPath)
		if err != nil {
			return nil, err
		}
		sm := string(smBytes)
		sourceMapContents = &sm
	}
	return &FacetBundleArtifact{
		Format:            FacetBundleArtifactFormat,
		FormatVersion:     FacetBundleArtifactFormatVersion,
		Plugin:            manifest.Plugin,
		EntryName:         options.Entry,
		Entry:             entry,
		Source:            source,
		SourceMapContents: sourceMapContents,
	}, nil
}

// CreateFacetBundleArtifactLoader materializes a transported artifact and creates a fresh compiled CommonJS generation for each load.
func CreateFacetBundleArtifactLoader(options *FacetBundleArtifactLoaderOptions) (chord.FacetLoader, error) {
	if options == nil {
		return nil, js.NewTypeError("Facet bundle artifact loader options must not be nil")
	}
	artifact, err := blValidateArtifact(options.Artifact)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	temporaryParent := os.TempDir()
	if options.TemporaryDirectory != nil {
		temporaryParent = bundleResolvePath(cwd, *options.TemporaryDirectory)
	}
	return &blArtifactLoader{
		artifact:        artifact,
		temporaryParent: temporaryParent,
		resolveExternal: options.ResolveExternal,
	}, nil
}

// CreateFacetBundleLoader creates a reusable loader for one opaque entry in a facet bundle manifest.
func CreateFacetBundleLoader(options *FacetBundleLoaderOptions) (chord.FacetLoader, error) {
	if options == nil {
		return nil, js.NewTypeError("Facet bundle loader options must not be nil")
	}
	if options.Entry == "" {
		return nil, js.NewTypeError("Facet bundle entry name must not be empty")
	}
	manifestPath, err := blToFilePath(options.ManifestPath)
	if err != nil {
		return nil, err
	}
	verifyIntegrity := true
	if options.VerifyIntegrity != nil {
		verifyIntegrity = *options.VerifyIntegrity
	}
	return &blBundleLoader{
		manifestPath:    manifestPath,
		entry:           options.Entry,
		verifyIntegrity: verifyIntegrity,
		resolveExternal: options.ResolveExternal,
	}, nil
}

type blBundleLoader struct {
	manifestPath    string
	entry           string
	verifyIntegrity bool
	resolveExternal FacetBundleExternalResolver
}

func (l *blBundleLoader) Load() (*chord.LoadedFacets, error) {
	manifest, err := ReadFacetBundleManifest(FacetBundlePathString(l.manifestPath))
	if err != nil {
		return nil, err
	}
	entry, ok := manifest.Entries.Get(l.entry)
	if !ok {
		return nil, fmt.Errorf("Facet bundle %s has no entry named %s", manifest.Plugin.Id, l.entry)
	}
	modulePath, err := blResolveBundleFile(l.manifestPath, entry.File, "entry")
	if err != nil {
		return nil, fmt.Errorf("Could not load facet bundle entry %s/%s: %w", manifest.Plugin.Id, l.entry, err)
	}
	sourceBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("Could not load facet bundle entry %s/%s: %w", manifest.Plugin.Id, l.entry, err)
	}
	source := string(sourceBytes)
	if l.verifyIntegrity {
		if err := blVerifySource(source, entry); err != nil {
			return nil, fmt.Errorf("Could not load facet bundle entry %s/%s: %w", manifest.Plugin.Id, l.entry, err)
		}
	}
	for _, specifier := range entry.ExternalImports {
		if !blIsNodeBuiltin(specifier) {
			if err := blValidatePackageSpecifier(specifier); err != nil {
				return nil, fmt.Errorf("Could not load facet bundle entry %s/%s: %w", manifest.Plugin.Id, l.entry, err)
			}
		}
		if l.resolveExternal != nil {
			if resolved := l.resolveExternal(specifier); resolved != nil {
				if _, err := blValidateExternalTarget(resolved); err != nil {
					return nil, fmt.Errorf("Could not load facet bundle entry %s/%s: %w", manifest.Plugin.Id, l.entry, err)
				}
			}
		}
	}
	// CommonJS vm execution and host ESM/Node module resolution have no Go runtime in pi-go.
	return nil, fmt.Errorf(
		"Could not load facet bundle entry %s/%s: executing CommonJS facet bundles is unsupported in pi-go",
		manifest.Plugin.Id,
		l.entry,
	)
}

type blArtifactLoader struct {
	artifact        *FacetBundleArtifact
	temporaryParent string
	resolveExternal FacetBundleExternalResolver
}

func (l *blArtifactLoader) Load() (*chord.LoadedFacets, error) {
	if err := os.MkdirAll(l.temporaryParent, 0o755); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(l.temporaryParent, "chord-facet-")
	if err != nil {
		return nil, err
	}
	cleanupOnErr := true
	defer func() {
		if cleanupOnErr {
			_ = os.RemoveAll(directory)
		}
	}()

	if err := blMaterializeArtifact(directory, l.artifact); err != nil {
		return nil, err
	}
	loader, err := CreateFacetBundleLoader(&FacetBundleLoaderOptions{
		ManifestPath:    FacetBundlePathString(filepath.Join(directory, FacetBundleManifestFile)),
		Entry:           l.artifact.EntryName,
		ResolveExternal: l.resolveExternal,
	})
	if err != nil {
		return nil, err
	}
	loaded, err := loader.Load()
	if err != nil {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			cleanupOnErr = false
			return nil, fmt.Errorf("Facet bundle artifact loading and cleanup failed: %w", errors.Join(err, cleanupErr))
		}
		cleanupOnErr = false
		return nil, err
	}

	cleanupOnErr = false
	disposed := false
	return &chord.LoadedFacets{
		Facets: loaded.Facets,
		Dispose: func() error {
			if disposed {
				return nil
			}
			disposed = true
			var errs []error
			if loaded.Dispose != nil {
				if err := loaded.Dispose(); err != nil {
					errs = append(errs, err)
				}
			}
			if err := os.RemoveAll(directory); err != nil {
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}, nil
}

func blMaterializeArtifact(directory string, artifact *FacetBundleArtifact) error {
	entries := omap.NewMap[string, FacetBundleEntry]()
	entries.Set(artifact.EntryName, artifact.Entry)
	manifest := FacetBundleManifest{
		Format:        FacetBundleFormat,
		FormatVersion: FacetBundleFormatVersion,
		Plugin:        artifact.Plugin,
		Entries:       entries,
	}
	encoded, err := jsonx.MarshalIndent(manifest, "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(directory, artifact.Entry.File), []byte(artifact.Source), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, FacetBundleManifestFile), encoded, 0o644); err != nil {
		return err
	}
	if artifact.Entry.SourceMap != nil {
		if err := os.WriteFile(filepath.Join(directory, *artifact.Entry.SourceMap), []byte(*artifact.SourceMapContents), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func blToFilePath(path FacetBundlePath) (string, error) {
	if path == nil {
		return "", js.NewTypeError("Facet bundle manifest path must not be nil")
	}
	switch p := path.(type) {
	case FacetBundlePathString:
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return bundleResolvePath(cwd, string(p)), nil
	case FacetBundlePathUrl:
		if p.Url == nil {
			return "", js.NewTypeError("Facet bundle manifest URL must not be nil")
		}
		if p.Url.Scheme != "file" {
			return "", js.NewTypeError(fmt.Sprintf("Facet bundle manifest must be a file URL, not %s:", p.Url.Scheme))
		}
		if p.Url.Host != "" && p.Url.Host != "localhost" {
			return "", js.NewTypeError("Facet bundle manifest file URL host must be empty or localhost")
		}
		return filepath.FromSlash(p.Url.Path), nil
	default:
		return "", js.NewTypeError("Facet bundle manifest path must be a string or file URL")
	}
}

func blValidateExternalTarget(target FacetBundlePath) (string, error) {
	switch t := target.(type) {
	case FacetBundlePathString:
		if u, err := url.Parse(string(t)); err == nil && u.Scheme != "" {
			if u.Scheme == "file" {
				return filepath.FromSlash(u.Path), nil
			}
			if u.Scheme == "node" {
				return string(t), nil
			}
			return "", fmt.Errorf("Facet bundle external target must be a file, package, or built-in module: %s", string(t))
		}
		return string(t), nil
	case FacetBundlePathUrl:
		if t.Url == nil {
			return "", fmt.Errorf("Facet bundle external target must be a file, package, or built-in module: <nil>")
		}
		if t.Url.Scheme == "file" {
			return filepath.FromSlash(t.Url.Path), nil
		}
		if t.Url.Scheme == "node" {
			return t.Url.String(), nil
		}
		return "", fmt.Errorf("Facet bundle external target must be a file, package, or built-in module: %s", t.Url.String())
	default:
		return "", fmt.Errorf("Facet bundle external target must be a file, package, or built-in module")
	}
}

func blResolveBundleFile(manifestPath, file, label string) (string, error) {
	if file == "" || filepath.IsAbs(file) || filepath.Base(file) != file || file == "." || file == ".." || strings.ContainsAny(file, `/\`) {
		return "", fmt.Errorf("Facet bundle %s must be a filename relative to its manifest", label)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(manifestPath), file)), nil
}

func blVerifySource(source string, entry FacetBundleEntry) error {
	expected, err := blParseIntegrity(entry.Integrity)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(source))
	actual := base64.StdEncoding.EncodeToString(digest[:])
	if actual != expected {
		return fmt.Errorf("Facet bundle integrity check failed for %s", entry.File)
	}
	return nil
}

func blParseIntegrity(integrity string) (string, error) {
	const prefix = "sha256-"
	if !strings.HasPrefix(integrity, prefix) || len(integrity) == len(prefix) {
		return "", errors.New("Facet bundle entry has an invalid SHA-256 integrity value")
	}
	return integrity[len(prefix):], nil
}

var blNodeBuiltins = map[string]struct{}{
	"assert":              {},
	"assert/strict":       {},
	"async_hooks":         {},
	"buffer":              {},
	"child_process":       {},
	"cluster":             {},
	"console":             {},
	"constants":           {},
	"crypto":              {},
	"dgram":               {},
	"diagnostics_channel": {},
	"dns":                 {},
	"dns/promises":        {},
	"domain":              {},
	"events":              {},
	"fs":                  {},
	"fs/promises":         {},
	"http":                {},
	"http2":               {},
	"https":               {},
	"inspector":           {},
	"inspector/promises":  {},
	"module":              {},
	"net":                 {},
	"os":                  {},
	"path":                {},
	"path/posix":          {},
	"path/win32":          {},
	"perf_hooks":          {},
	"process":             {},
	"punycode":            {},
	"querystring":         {},
	"readline":            {},
	"readline/promises":   {},
	"repl":                {},
	"stream":              {},
	"stream/consumers":    {},
	"stream/promises":     {},
	"stream/web":          {},
	"string_decoder":      {},
	"sys":                 {},
	"test":                {},
	"timers":              {},
	"timers/promises":     {},
	"tls":                 {},
	"trace_events":        {},
	"tty":                 {},
	"url":                 {},
	"util":                {},
	"util/types":          {},
	"v8":                  {},
	"vm":                  {},
	"wasi":                {},
	"worker_threads":      {},
	"zlib":                {},
}

func blIsNodeBuiltin(specifier string) bool {
	if trimmed, ok := strings.CutPrefix(specifier, "node:"); ok {
		_, exists := blNodeBuiltins[trimmed]
		return exists
	}
	_, exists := blNodeBuiltins[specifier]
	return exists
}

func blValidatePackageSpecifier(specifier string) error {
	segments := strings.Split(specifier, "/")
	packageParts := 1
	if strings.HasPrefix(specifier, "@") {
		packageParts = 2
	}
	if strings.HasPrefix(specifier, ".") ||
		strings.HasPrefix(specifier, "/") ||
		strings.HasPrefix(specifier, "#") ||
		strings.Contains(specifier, ":") ||
		strings.Contains(specifier, `\`) ||
		len(segments) < packageParts {
		return fmt.Errorf("Facet bundle artifact has an unsupported external import: %s", specifier)
	}
	for _, part := range segments {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("Facet bundle artifact has an unsupported external import: %s", specifier)
		}
	}
	return nil
}

func blValidateArtifact(value any) (*FacetBundleArtifact, error) {
	if value == nil {
		return nil, errors.New("Invalid facet bundle artifact")
	}
	var obj *jsonx.Object
	switch v := value.(type) {
	case *jsonx.Object:
		if v == nil {
			return nil, errors.New("Invalid facet bundle artifact")
		}
		obj = v
	default:
		encoded, err := jsonx.Marshal(value)
		if err != nil {
			return nil, errors.New("Invalid facet bundle artifact")
		}
		parsed, err := jsonx.Parse(encoded)
		if err != nil {
			return nil, errors.New("Invalid facet bundle artifact")
		}
		var ok bool
		obj, ok = parsed.(*jsonx.Object)
		if !ok || obj == nil {
			return nil, errors.New("Invalid facet bundle artifact")
		}
	}

	formatVal, _ := obj.Get("format")
	formatStr, ok := formatVal.(string)
	if !ok || formatStr != FacetBundleArtifactFormat {
		return nil, errors.New("Invalid facet bundle artifact")
	}
	versionVal, _ := obj.Get("formatVersion")
	versionNum, ok := versionVal.(float64)
	if !ok || versionNum != float64(FacetBundleArtifactFormatVersion) {
		return nil, errors.New("Invalid facet bundle artifact")
	}
	entryNameVal, _ := obj.Get("entryName")
	entryName, ok := entryNameVal.(string)
	if !ok || entryName == "" {
		return nil, errors.New("Invalid facet bundle artifact")
	}
	sourceVal, _ := obj.Get("source")
	source, ok := sourceVal.(string)
	if !ok {
		return nil, errors.New("Invalid facet bundle artifact")
	}

	pluginVal, _ := obj.Get("plugin")
	entryVal, _ := obj.Get("entry")
	synthetic := jsonx.ObjectOf(
		"format", FacetBundleFormat,
		"formatVersion", float64(FacetBundleFormatVersion),
		"plugin", pluginVal,
		"entries", jsonx.ObjectOf(entryName, entryVal),
	)
	manifest, err := blValidateManifest(synthetic, "facet bundle artifact")
	if err != nil {
		return nil, err
	}
	entry, _ := manifest.Entries.Get(entryName)

	smContentsVal, hasSmContents := obj.Get("sourceMapContents")
	var sourceMapContents *string
	if entry.SourceMap == nil {
		if hasSmContents {
			return nil, errors.New("Facet bundle artifact has source map contents without a source map")
		}
	} else {
		smStr, ok := smContentsVal.(string)
		if !hasSmContents || !ok {
			return nil, errors.New("Facet bundle artifact is missing its source map contents")
		}
		sourceMapContents = &smStr
	}

	if err := blVerifySource(source, entry); err != nil {
		return nil, err
	}

	return &FacetBundleArtifact{
		Format:            FacetBundleArtifactFormat,
		FormatVersion:     FacetBundleArtifactFormatVersion,
		Plugin:            manifest.Plugin,
		EntryName:         entryName,
		Entry:             entry,
		Source:            source,
		SourceMapContents: sourceMapContents,
	}, nil
}

func blValidateManifest(value any, manifestPath string) (*FacetBundleManifest, error) {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("Invalid facet bundle manifest format in %s", manifestPath)
	}
	formatVal, _ := obj.Get("format")
	if s, ok := formatVal.(string); !ok || s != FacetBundleFormat {
		return nil, fmt.Errorf("Invalid facet bundle manifest format in %s", manifestPath)
	}
	verVal, hasVer := obj.Get("formatVersion")
	verNum, verIsNum := verVal.(float64)
	if !hasVer || !verIsNum || math.IsNaN(verNum) || verNum != float64(FacetBundleFormatVersion) {
		verDesc := "undefined"
		if hasVer {
			verDesc = fmt.Sprintf("%v", verVal)
		}
		return nil, fmt.Errorf("Unsupported facet bundle manifest version in %s: %s", manifestPath, verDesc)
	}

	pluginVal, _ := obj.Get("plugin")
	pluginObj, ok := pluginVal.(*jsonx.Object)
	if !ok || pluginObj == nil {
		return nil, fmt.Errorf("Facet bundle manifest has an invalid plugin identity in %s", manifestPath)
	}
	pluginIDVal, _ := pluginObj.Get("id")
	pluginID, ok := pluginIDVal.(string)
	if !ok || pluginID == "" {
		return nil, fmt.Errorf("Facet bundle manifest has an invalid plugin identity in %s", manifestPath)
	}
	var pluginVersion *string
	if verFieldVal, hasPluginVer := pluginObj.Get("version"); hasPluginVer {
		s, ok := verFieldVal.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("Facet bundle manifest has an invalid plugin version in %s", manifestPath)
		}
		pluginVersion = &s
	}

	entriesVal, _ := obj.Get("entries")
	entriesObj, ok := entriesVal.(*jsonx.Object)
	if !ok || entriesObj == nil || entriesObj.Len() == 0 {
		return nil, fmt.Errorf("Facet bundle manifest has no entries in %s", manifestPath)
	}

	entries := omap.NewMap[string, FacetBundleEntry]()
	for name, candidate := range entriesObj.All() {
		candObj, ok := candidate.(*jsonx.Object)
		if name == "" || !ok || candObj == nil {
			return nil, fmt.Errorf("Facet bundle manifest has an invalid entry in %s", manifestPath)
		}
		fileVal, _ := candObj.Get("file")
		file, ok := fileVal.(string)
		if !ok {
			return nil, fmt.Errorf("Facet bundle entry %s has no file", name)
		}
		if _, err := blResolveBundleFile(manifestPath, file, "entry "+name); err != nil {
			return nil, err
		}
		integrityVal, _ := candObj.Get("integrity")
		integrity, ok := integrityVal.(string)
		if !ok {
			return nil, fmt.Errorf("Facet bundle entry %s has no integrity", name)
		}
		if _, err := blParseIntegrity(integrity); err != nil {
			return nil, err
		}

		extVal, _ := candObj.Get("externalImports")
		extArr, ok := extVal.([]any)
		if !ok {
			return nil, fmt.Errorf("Facet bundle entry %s has invalid external imports", name)
		}
		externalImports := make([]string, 0, len(extArr))
		seenExt := make(map[string]struct{}, len(extArr))
		for _, item := range extArr {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("Facet bundle entry %s has invalid external imports", name)
			}
			if _, dup := seenExt[s]; dup {
				return nil, fmt.Errorf("Facet bundle entry %s has duplicate external imports", name)
			}
			seenExt[s] = struct{}{}
			externalImports = append(externalImports, s)
		}

		var sourceMap *string
		if smVal, hasSM := candObj.Get("sourceMap"); hasSM {
			sm, ok := smVal.(string)
			if !ok {
				return nil, fmt.Errorf("Facet bundle entry %s has an invalid source map", name)
			}
			if _, err := blResolveBundleFile(manifestPath, sm, "entry "+name+" source map"); err != nil {
				return nil, err
			}
			sourceMap = &sm
		}

		entries.Set(name, FacetBundleEntry{
			File:            file,
			Integrity:       integrity,
			ExternalImports: externalImports,
			SourceMap:       sourceMap,
		})
	}

	return &FacetBundleManifest{
		Format:        FacetBundleFormat,
		FormatVersion: FacetBundleFormatVersion,
		Plugin: FacetBundlePlugin{
			Id:      pluginID,
			Version: pluginVersion,
		},
		Entries: entries,
	}, nil
}
