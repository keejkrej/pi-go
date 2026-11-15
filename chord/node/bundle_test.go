// Ported from packages/chord/test/bundle.test.ts (pi v1.0.0) with Go/esbuild bundle,
// manifest, artifact, package, and validation coverage plus CJS-eval boundary assertions.

package node

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tbnPtr[T any](v T) *T {
	return &v
}

func tbnWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func tbnWriteGeneration(t *testing.T, path, generation string) {
	t.Helper()
	encodedGen, err := jsonx.Stringify(generation)
	if err != nil {
		t.Fatalf("Stringify(%q): %v", generation, err)
	}
	src := `import "@earendil-works/chord";` + "\n" +
		`import { decorate } from "./helper.ts";` + "\n" +
		`const Value = { id: "test.bundle.generation", local: true };` + "\n" +
		`export default { id: "bundle-provider", setup(env: any) {` + "\n" +
		`  env.provide(Value, { read() { return decorate(` + encodedGen + `); } });` + "\n" +
		`}};` + "\n"
	tbnWriteFile(t, path, src)
}

func tbnAssertTypeError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected TypeError containing %q, got nil", substr)
	}
	var jsErr *js.JSError
	if !errors.As(err, &jsErr) || jsErr.Name() != "TypeError" {
		t.Fatalf("expected *js.JSError with Name=TypeError, got %T (%v)", err, err)
	}
	if substr != "" && !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err.Error(), substr)
	}
}

func TestBundleFacetsContentAddressedDeterminismAndManifest(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "src")
	outputDir := filepath.Join(dir, "bundle")
	helperPath := filepath.Join(sourceDir, "helper.ts")
	entryPath := filepath.Join(sourceDir, "entry.ts")
	presentationPath := filepath.Join(sourceDir, "presentation.ts")

	tbnWriteFile(t, helperPath, `export const decorate = (value: string): string => "generation:" + value;`+"\n")
	tbnWriteGeneration(t, entryPath, "A")
	tbnWriteFile(t, presentationPath, `export default { id: "bundle-presentation", setup() {} };`+"\n")

	entries := omap.NewMap[string, string]()
	entries.Set("worker", entryPath)
	entries.Set("presentation", presentationPath)

	firstBuild, err := BundleFacets(&BundleFacetsOptions{
		Plugin:    FacetBundlePlugin{Id: "test-bundle", Version: tbnPtr("1")},
		Entries:   entries,
		Outdir:    outputDir,
		SourceMap: tbnPtr(true),
	})
	if err != nil {
		t.Fatalf("BundleFacets(first) error = %v", err)
	}

	firstEntry, ok := firstBuild.Manifest.Entries.Get("worker")
	if !ok {
		t.Fatalf("missing worker entry in manifest")
	}
	presEntry, ok := firstBuild.Manifest.Entries.Get("presentation")
	if !ok {
		t.Fatalf("missing presentation entry in manifest")
	}

	// Entries are sorted using localeCompare order (presentation before worker).
	if got := firstBuild.Manifest.Entries.Keys(); !slices.Equal(got, []string{"presentation", "worker"}) {
		t.Fatalf("manifest entry keys = %v, want [presentation worker]", got)
	}

	filePattern := regexp.MustCompile(`^facet-[a-f0-9]{12}-[A-Z0-9]+\.cjs$`)
	if !filePattern.MatchString(firstEntry.File) {
		t.Fatalf("worker file %q does not match content-addressed pattern", firstEntry.File)
	}
	if firstEntry.SourceMap == nil || *firstEntry.SourceMap != firstEntry.File+".map" {
		t.Fatalf("worker sourceMap = %v, want %q", firstEntry.SourceMap, firstEntry.File+".map")
	}
	if !slices.Equal(firstEntry.ExternalImports, []string{"@earendil-works/chord"}) {
		t.Fatalf("worker externalImports = %v, want [@earendil-works/chord]", firstEntry.ExternalImports)
	}
	if presEntry.File == firstEntry.File {
		t.Fatalf("distinct entries produced identical filename %q", firstEntry.File)
	}

	outEntries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("ReadDir(outputDir): %v", err)
	}
	var cjsCount int
	for _, de := range outEntries {
		if strings.HasSuffix(de.Name(), ".cjs") {
			cjsCount++
		}
	}
	if cjsCount != 2 {
		t.Fatalf("got %d .cjs files in outputDir, want 2", cjsCount)
	}

	firstSourceBytes, err := os.ReadFile(filepath.Join(outputDir, firstEntry.File))
	if err != nil {
		t.Fatalf("ReadFile(worker): %v", err)
	}
	firstSource := string(firstSourceBytes)
	if !strings.Contains(firstSource, `require("@earendil-works/chord")`) {
		t.Fatalf("bundled worker missing CommonJS external require:\n%s", firstSource)
	}
	if !strings.Contains(firstSource, `"use strict";`) {
		t.Fatalf("bundled worker missing strict banner:\n%s", firstSource)
	}
	manifestBytes, err := os.ReadFile(firstBuild.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest): %v", err)
	}
	if !strings.HasSuffix(string(manifestBytes), "\n") {
		t.Fatalf("manifest JSON missing trailing newline")
	}

	// ReadFacetBundleManifest works via string path and file:// URL.
	readBack, err := ReadFacetBundleManifest(FacetBundlePathString(firstBuild.ManifestPath))
	if err != nil {
		t.Fatalf("ReadFacetBundleManifest(string): %v", err)
	}
	if readBack.Format != FacetBundleFormat || readBack.FormatVersion != FacetBundleFormatVersion || readBack.Plugin.Id != "test-bundle" {
		t.Fatalf("unexpected manifest header: %+v", readBack)
	}
	fileURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(firstBuild.ManifestPath)}
	if !strings.HasPrefix(fileURL.Path, "/") {
		fileURL.Path = "/" + fileURL.Path
	}
	readViaURL, err := ReadFacetBundleManifest(FacetBundlePathUrl{Url: fileURL})
	if err != nil {
		t.Fatalf("ReadFacetBundleManifest(file://): %v", err)
	}
	if readViaURL.Entries.Len() != 2 {
		t.Fatalf("ReadFacetBundleManifest(file://) entries len = %d, want 2", readViaURL.Entries.Len())
	}

	// ReadFacetBundleArtifact verifies integrity and reads source + sourceMapContents.
	artifact, err := ReadFacetBundleArtifact(&ReadFacetBundleArtifactOptions{
		ManifestPath: FacetBundlePathString(firstBuild.ManifestPath),
		Entry:        "worker",
	})
	if err != nil {
		t.Fatalf("ReadFacetBundleArtifact: %v", err)
	}
	if artifact.Format != FacetBundleArtifactFormat || artifact.FormatVersion != FacetBundleArtifactFormatVersion {
		t.Fatalf("unexpected artifact format: %+v", artifact)
	}
	if artifact.EntryName != "worker" || artifact.Source != firstSource || artifact.SourceMapContents == nil {
		t.Fatalf("artifact fields mismatch: entryName=%q hasMap=%v", artifact.EntryName, artifact.SourceMapContents != nil)
	}

	// Roundtrip artifact through JSON as another host receives it.
	rawArtifactJSON, err := jsonx.Marshal(artifact)
	if err != nil {
		t.Fatalf("jsonx.Marshal(artifact): %v", err)
	}
	parsedArtifact, err := jsonx.Parse(rawArtifactJSON)
	if err != nil {
		t.Fatalf("jsonx.Parse(artifact): %v", err)
	}
	materializedDir := filepath.Join(dir, "materialized")
	artLoader, err := CreateFacetBundleArtifactLoader(&FacetBundleArtifactLoaderOptions{
		Artifact:           parsedArtifact,
		TemporaryDirectory: &materializedDir,
	})
	if err != nil {
		t.Fatalf("CreateFacetBundleArtifactLoader: %v", err)
	}
	// In pi-go, CommonJS vm execution is unsupported, and artifact temp dir must be cleaned up on error.
	if _, err := artLoader.Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported CJS execution error from artifact loader, got %v", err)
	}
	if rem, err := os.ReadDir(materializedDir); err != nil || len(rem) != 0 {
		t.Fatalf("expected materializedDir cleanup on load error, got entries=%v err=%v", rem, err)
	}

	// Deterministic rebuild yields identical metadata.
	secondBuild, err := BundleFacets(&BundleFacetsOptions{
		Plugin:    FacetBundlePlugin{Id: "test-bundle", Version: tbnPtr("1")},
		Entries:   entries,
		Outdir:    outputDir,
		SourceMap: tbnPtr(true),
	})
	if err != nil {
		t.Fatalf("BundleFacets(second) error = %v", err)
	}
	secondEntry, _ := secondBuild.Manifest.Entries.Get("worker")
	if secondEntry.File != firstEntry.File || secondEntry.Integrity != firstEntry.Integrity {
		t.Fatalf("rebuild non-deterministic: %+v vs %+v", secondEntry, firstEntry)
	}

	// Changing source produces a new content hash and replaces previous directory contents.
	tbnWriteGeneration(t, entryPath, "B")
	thirdBuild, err := BundleFacets(&BundleFacetsOptions{
		Plugin:    FacetBundlePlugin{Id: "test-bundle", Version: tbnPtr("2")},
		Entries:   entries,
		Outdir:    outputDir,
		SourceMap: tbnPtr(true),
	})
	if err != nil {
		t.Fatalf("BundleFacets(third) error = %v", err)
	}
	thirdEntry, _ := thirdBuild.Manifest.Entries.Get("worker")
	if thirdEntry.File == firstEntry.File {
		t.Fatalf("expected modified entry to have a new content-addressed filename, still %q", thirdEntry.File)
	}
	if _, err := os.Stat(filepath.Join(outputDir, firstEntry.File)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale output %q to be removed after directory replace, stat err=%v", firstEntry.File, err)
	}
}

func TestBundleFacetsExternalAndDynamicImportLowering(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.ts")
	outputDir := filepath.Join(dir, "bundle")
	tbnWriteFile(
		t,
		entryPath,
		`import { named } from "@example/host";`+"\n"+
			`export const loadDynamic = () => import("@example/dynamic");`+"\n"+
			`if (named !== "host-named") throw new Error("external mismatch");`+"\n"+
			`export default { id: "external-facet", setup() {} };`+"\n",
	)
	entries := omap.NewMap[string, string]()
	entries.Set("worker", entryPath)

	res, err := BundleFacets(&BundleFacetsOptions{
		Plugin:   FacetBundlePlugin{Id: "external-bundle"},
		Entries:  entries,
		External: []string{"@example/host", "@example/dynamic", "@example/host"},
		Outdir:   outputDir,
	})
	if err != nil {
		t.Fatalf("BundleFacets: %v", err)
	}
	entry, ok := res.Manifest.Entries.Get("worker")
	if !ok {
		t.Fatalf("missing worker entry")
	}
	if entry.SourceMap != nil {
		t.Fatalf("expected nil sourceMap when SourceMap is omitted, got %q", *entry.SourceMap)
	}
	if !slices.Equal(entry.ExternalImports, []string{"@example/dynamic", "@example/host"}) {
		t.Fatalf("externalImports = %v, want [@example/dynamic @example/host]", entry.ExternalImports)
	}

	sourceBytes, err := os.ReadFile(filepath.Join(outputDir, entry.File))
	if err != nil {
		t.Fatalf("ReadFile(bundle): %v", err)
	}
	source := string(sourceBytes)
	if strings.Contains(source, "import(") {
		t.Fatalf("dynamic import was not lowered to require:\n%s", source)
	}
	if !strings.Contains(source, `require("@example/dynamic")`) {
		t.Fatalf("lowered bundle missing require(\"@example/dynamic\"):\n%s", source)
	}

	// Custom resolver rejects invalid URI scheme before CJS runtime check.
	badSchemeLoader, err := CreateFacetBundleLoader(&FacetBundleLoaderOptions{
		ManifestPath: FacetBundlePathString(res.ManifestPath),
		Entry:        "worker",
		ResolveExternal: func(specifier string) FacetBundlePath {
			if specifier == "@example/host" {
				u, _ := url.Parse("https://example.com/host.js")
				return FacetBundlePathUrl{Url: u}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateFacetBundleLoader: %v", err)
	}
	if _, err := badSchemeLoader.Load(); err == nil || !strings.Contains(err.Error(), "must be a file, package, or built-in module") {
		t.Fatalf("expected unsupported external target error, got %v", err)
	}

	// Valid resolver passes external specifier validation and reaches CJS eval boundary.
	validLoader, err := CreateFacetBundleLoader(&FacetBundleLoaderOptions{
		ManifestPath: FacetBundlePathString(res.ManifestPath),
		Entry:        "worker",
		ResolveExternal: func(specifier string) FacetBundlePath {
			return FacetBundlePathUrl{Url: &url.URL{Scheme: "file", Path: "/tmp/host.mjs"}}
		},
	})
	if err != nil {
		t.Fatalf("CreateFacetBundleLoader: %v", err)
	}
	if _, err := validLoader.Load(); err == nil || !strings.Contains(err.Error(), "unsupported in pi-go") {
		t.Fatalf("expected CJS unsupported error, got %v", err)
	}
}

func TestBundleFacetsPlatformsTargetsDefinesMinifyAndBuildFailure(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.ts")
	tbnWriteFile(
		t,
		entryPath,
		`declare const __BUILD_TAG__: string;`+"\n"+
			`export const tag: string = __BUILD_TAG__;`+"\n"+
			`export const arrow = (x: number) => x ?? 42;`+"\n",
	)
	entries := omap.NewMap[string, string]()
	entries.Set("main", entryPath)
	defines := omap.NewMap[string, string]()
	defines.Set("__BUILD_TAG__", `"custom-define-ok"`)

	platform := FacetBundlePlatformBrowser
	outBrowser := filepath.Join(dir, "out-browser")
	res, err := BundleFacets(&BundleFacetsOptions{
		Plugin:           FacetBundlePlugin{Id: "define-test"},
		Entries:          entries,
		Outdir:           "out-browser",
		WorkingDirectory: &dir,
		Define:           defines,
		Minify:           tbnPtr(true),
		Platform:         &platform,
		Target:           BundleFacetsTargetList{"es2020", "chrome115"},
	})
	if err != nil {
		t.Fatalf("BundleFacets(browser): %v", err)
	}
	entry, _ := res.Manifest.Entries.Get("main")
	b, err := os.ReadFile(filepath.Join(outBrowser, entry.File))
	if err != nil {
		t.Fatalf("ReadFile(outBrowser): %v", err)
	}
	if !strings.Contains(string(b), "custom-define-ok") {
		t.Fatalf("define was not applied in bundle:\n%s", string(b))
	}

	// Single string target on neutral platform.
	neutral := FacetBundlePlatformNeutral
	resNeutral, err := BundleFacets(&BundleFacetsOptions{
		Plugin:   FacetBundlePlugin{Id: "neutral-test"},
		Entries:  entries,
		Outdir:   filepath.Join(dir, "out-neutral"),
		Define:   defines,
		Platform: &neutral,
		Target:   BundleFacetsTargetString("es2022"),
	})
	if err != nil {
		t.Fatalf("BundleFacets(neutral): %v", err)
	}
	if resNeutral.Manifest.Plugin.Version != nil {
		t.Fatalf("expected omitted plugin version to remain nil, got %v", *resNeutral.Manifest.Plugin.Version)
	}

	// Syntax error during build fails with diagnostics and leaves no temporary directories behind.
	brokenPath := filepath.Join(dir, "broken.ts")
	tbnWriteFile(t, brokenPath, "export const x = {{{;\n")
	badEntries := omap.NewMap[string, string]()
	badEntries.Set("broken", brokenPath)
	_, err = BundleFacets(&BundleFacetsOptions{
		Plugin:  FacetBundlePlugin{Id: "broken-test"},
		Entries: badEntries,
		Outdir:  filepath.Join(dir, "out-broken"),
	})
	if err == nil || !strings.Contains(err.Error(), "Could not bundle facet entry broken") {
		t.Fatalf("expected bundle build failure for broken entry, got %v", err)
	}
	leftovers, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(dir): %v", err)
	}
	for _, de := range leftovers {
		if strings.HasPrefix(de.Name(), ".out-broken.tmp-") {
			t.Fatalf("temporary directory %q was not cleaned up on build error", de.Name())
		}
	}

	// Invalid target rejected.
	_, err = BundleFacets(&BundleFacetsOptions{
		Plugin:  FacetBundlePlugin{Id: "bad-target"},
		Entries: entries,
		Outdir:  filepath.Join(dir, "out-bad-target"),
		Target:  BundleFacetsTargetString("bogus99"),
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid target") {
		t.Fatalf("expected invalid target error, got %v", err)
	}
}

func TestBundleFacetsValidationErrors(t *testing.T) {
	validEntries := omap.NewMap[string, string]()
	validEntries.Set("a", "a.ts")

	_, err := BundleFacets(nil)
	tbnAssertTypeError(t, err, "must not be nil")

	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: ""}, Entries: validEntries, Outdir: "out"})
	tbnAssertTypeError(t, err, "plugin ID must not be empty")

	emptyVer := ""
	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p", Version: &emptyVer}, Entries: validEntries, Outdir: "out"})
	tbnAssertTypeError(t, err, "plugin version must not be empty")

	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: nil, Outdir: "out"})
	tbnAssertTypeError(t, err, "at least one entry")

	emptyNameEntries := omap.NewMap[string, string]()
	emptyNameEntries.Set("", "a.ts")
	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: emptyNameEntries, Outdir: "out"})
	tbnAssertTypeError(t, err, "entry name must not be empty")

	emptySrcEntries := omap.NewMap[string, string]()
	emptySrcEntries.Set("main", "")
	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: emptySrcEntries, Outdir: "out"})
	tbnAssertTypeError(t, err, "must have a source path")

	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: validEntries, External: []string{""}, Outdir: "out"})
	tbnAssertTypeError(t, err, "external import must not be empty")

	badPlatform := FacetBundlePlatform("plan9")
	_, err = BundleFacets(&BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: validEntries, Outdir: "out", Platform: &badPlatform})
	tbnAssertTypeError(t, err, "Invalid facet bundle platform")
}

func TestBundleFacetPackageConventionsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(dir): %v", err)
	}
	pkgJSONPath := filepath.Join(dir, "package.json")
	tbnWriteFile(
		t,
		pkgJSONPath,
		`{"name":"@example/conventional-plugin","version":"1.2.3","peerDependencies":{"@example/host":"^1.0.0"}}`+"\n",
	)
	tbnWriteFile(t, filepath.Join(dir, "src/session.ts"), `import "@example/host/plugin"; export default { id: "package-session", setup() {} };`+"\n")
	tbnWriteFile(t, filepath.Join(dir, "src/tui.ts"), `export default { id: "package-tui", setup() {} };`+"\n")
	tbnWriteFile(t, filepath.Join(dir, "src/contract.ts"), `export const ignored = true;`+"\n")
	tbnWriteFile(t, filepath.Join(dir, "src/presentation.ts"), `export default { id: "configured-tui", setup() {} };`+"\n")

	defaults := omap.NewMap[string, string]()
	defaults.Set("session", "src/session.ts")
	defaults.Set("tui", "src/tui.ts")
	defaults.Set("browser", "src/browser.ts") // missing conventional path should be skipped

	conventional, err := BundleFacetPackage(&BundleFacetPackageOptions{
		PackagePath:   dir,
		Outdir:        filepath.Join(dir, "build"),
		DefaultFacets: defaults,
	})
	if err != nil {
		t.Fatalf("BundleFacetPackage(conventional): %v", err)
	}
	if conventional.PackageDirectory != canonicalDir {
		t.Fatalf("PackageDirectory = %q, want %q", conventional.PackageDirectory, canonicalDir)
	}
	if conventional.Manifest.Plugin.Id != "@example/conventional-plugin" || conventional.Manifest.Plugin.Version == nil || *conventional.Manifest.Plugin.Version != "1.2.3" {
		t.Fatalf("unexpected plugin metadata: %+v", conventional.Manifest.Plugin)
	}
	if got := conventional.Manifest.Entries.Keys(); !slices.Equal(got, []string{"session", "tui"}) {
		t.Fatalf("conventional entries = %v, want [session tui]", got)
	}
	sessionEntry, _ := conventional.Manifest.Entries.Get("session")
	if !slices.Equal(sessionEntry.ExternalImports, []string{"@example/host/plugin"}) {
		t.Fatalf("session externalImports = %v, want [@example/host/plugin]", sessionEntry.ExternalImports)
	}
	tuiEntry, _ := conventional.Manifest.Entries.Get("tui")
	if tuiEntry.SourceMap == nil || !strings.HasSuffix(*tuiEntry.SourceMap, ".cjs.map") {
		t.Fatalf("expected tui sourceMap, got %v", tuiEntry.SourceMap)
	}

	// Override tui, disable session, and turn off sourceMap via package.json path.
	tbnWriteFile(
		t,
		pkgJSONPath,
		`{"name":"@example/conventional-plugin","version":"2.0.0","chord":{"facets":{"session":false,"tui":"src/presentation.ts"},"sourceMap":false}}`+"\n",
	)
	configured, err := BundleFacetPackage(&BundleFacetPackageOptions{
		PackagePath:   pkgJSONPath,
		Outdir:        filepath.Join(dir, "build"),
		DefaultFacets: defaults,
	})
	if err != nil {
		t.Fatalf("BundleFacetPackage(configured): %v", err)
	}
	if got := configured.Manifest.Entries.Keys(); !slices.Equal(got, []string{"tui"}) {
		t.Fatalf("configured entries = %v, want [tui]", got)
	}
	confTui, _ := configured.Manifest.Entries.Get("tui")
	if confTui.SourceMap != nil {
		t.Fatalf("expected nil sourceMap when chord.sourceMap=false, got %q", *confTui.SourceMap)
	}
}

func TestBundleFacetPackageValidationAndEscapeRejection(t *testing.T) {
	dir := t.TempDir()

	// Empty path throws TypeError.
	_, err := BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: "", Outdir: filepath.Join(dir, "build")})
	tbnAssertTypeError(t, err, "must not be empty")

	// Escaping configured path rejected.
	escapeDir := filepath.Join(dir, "escape")
	tbnWriteFile(t, filepath.Join(escapeDir, "package.json"), `{"name":"invalid-plugin","version":"1.0.0","chord":{"facets":{"tui":"../outside.ts"}}}`)
	_, err = BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: escapeDir, Outdir: filepath.Join(escapeDir, "build")})
	if err == nil || !strings.Contains(err.Error(), "escapes the package directory") {
		t.Fatalf("expected escape error, got %v", err)
	}

	// Absolute configured path rejected.
	absDir := filepath.Join(dir, "abs")
	tbnWriteFile(t, filepath.Join(absDir, "package.json"), `{"name":"abs-plugin","version":"1.0.0","chord":{"facets":{"tui":"/tmp/x.ts"}}}`)
	_, err = BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: absDir, Outdir: filepath.Join(absDir, "build")})
	if err == nil || !strings.Contains(err.Error(), "must be relative to the package directory") {
		t.Fatalf("expected relative-path error, got %v", err)
	}

	// Symlink pointing outside packageDirectory is rejected.
	outsideTarget := filepath.Join(dir, "outside.ts")
	tbnWriteFile(t, outsideTarget, "export default {};\n")
	symDir := filepath.Join(dir, "symlink")
	tbnWriteFile(t, filepath.Join(symDir, "package.json"), `{"name":"sym-plugin","version":"1.0.0","chord":{"facets":{"tui":"src/link.ts"}}}`)
	if err := os.MkdirAll(filepath.Join(symDir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(symDir, "src/link.ts")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	_, err = BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: symDir, Outdir: filepath.Join(symDir, "build")})
	if err == nil || !strings.Contains(err.Error(), "resolves outside the package directory") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}

	// Unknown chord field rejected.
	unknownFieldDir := filepath.Join(dir, "unknown-field")
	tbnWriteFile(t, filepath.Join(unknownFieldDir, "package.json"), `{"name":"p","version":"1.0.0","chord":{"extra":1}}`)
	_, err = BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: unknownFieldDir, Outdir: filepath.Join(unknownFieldDir, "build")})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}

	// Empty resolved facet entries rejected.
	emptyEntriesDir := filepath.Join(dir, "no-entries")
	tbnWriteFile(t, filepath.Join(emptyEntriesDir, "package.json"), `{"name":"empty-plugin","version":"1.0.0"}`)
	_, err = BundleFacetPackage(&BundleFacetPackageOptions{PackagePath: emptyEntriesDir, Outdir: filepath.Join(emptyEntriesDir, "build")})
	if err == nil || !strings.Contains(err.Error(), "no configured or conventional facet entries") {
		t.Fatalf("expected no entries error, got %v", err)
	}
}

func TestBundleManifestAndArtifactValidation(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.ts")
	outputDir := filepath.Join(dir, "bundle")
	tbnWriteFile(t, entryPath, `export default { id: "sample", setup() {} };`+"\n")
	entries := omap.NewMap[string, string]()
	entries.Set("main", entryPath)

	res, err := BundleFacets(&BundleFacetsOptions{
		Plugin:    FacetBundlePlugin{Id: "valid-bundle", Version: tbnPtr("1.0.0")},
		Entries:   entries,
		Outdir:    outputDir,
		SourceMap: tbnPtr(true),
	})
	if err != nil {
		t.Fatalf("BundleFacets: %v", err)
	}

	// Non-file URL fails with TypeError.
	httpURL, _ := url.Parse("https://example.com/chord-facets.json")
	_, err = ReadFacetBundleManifest(FacetBundlePathUrl{Url: httpURL})
	tbnAssertTypeError(t, err, "must be a file URL, not https:")

	_, err = CreateFacetBundleLoader(&FacetBundleLoaderOptions{ManifestPath: FacetBundlePathUrl{Url: httpURL}, Entry: "main"})
	tbnAssertTypeError(t, err, "must be a file URL, not https:")

	// Duplicate / bad external specifiers and Node builtins validation.
	if err := blValidatePackageSpecifier("./relative"); err == nil {
		t.Fatalf("expected relative specifier error")
	}
	if err := blValidatePackageSpecifier("pkg/../escape"); err == nil {
		t.Fatalf("expected dot-segment specifier error")
	}
	if err := blValidatePackageSpecifier("@scope"); err == nil {
		t.Fatalf("expected incomplete scoped package error")
	}
	if err := blValidatePackageSpecifier("@scope/pkg/sub"); err != nil {
		t.Fatalf("unexpected scoped subpath rejection: %v", err)
	}
	if !blIsNodeBuiltin("node:fs") || !blIsNodeBuiltin("path") || blIsNodeBuiltin("@earendil-works/chord") {
		t.Fatalf("unexpected blIsNodeBuiltin result")
	}

	// Valid artifact passes ReadFacetBundleArtifact; tampering source rejects ReadFacetBundleArtifact and Load.
	artifact, err := ReadFacetBundleArtifact(&ReadFacetBundleArtifactOptions{
		ManifestPath: FacetBundlePathString(res.ManifestPath),
		Entry:        "main",
	})
	if err != nil {
		t.Fatalf("ReadFacetBundleArtifact: %v", err)
	}
	if artifact.SourceMapContents == nil {
		t.Fatalf("expected non-nil SourceMapContents")
	}

	// Missing sourceMapContents rejected when artifact.Entry.SourceMap is set.
	badArtifact := *artifact
	badArtifact.SourceMapContents = nil
	if _, err := CreateFacetBundleArtifactLoader(&FacetBundleArtifactLoaderOptions{Artifact: &badArtifact}); err == nil || !strings.Contains(err.Error(), "missing its source map contents") {
		t.Fatalf("expected missing source map contents error, got %v", err)
	}

	// Extra sourceMapContents rejected when artifact.Entry.SourceMap is nil.
	extraMapArtifact := *artifact
	extraMapArtifact.Entry.SourceMap = nil
	if _, err := CreateFacetBundleArtifactLoader(&FacetBundleArtifactLoaderOptions{Artifact: &extraMapArtifact}); err == nil || !strings.Contains(err.Error(), "without a source map") {
		t.Fatalf("expected extra source map contents error, got %v", err)
	}

	// Tampered bundle file fails integrity check during CreateFacetBundleLoader.Load() and ReadFacetBundleArtifact.
	mainEntry, _ := res.Manifest.Entries.Get("main")
	loader, err := CreateFacetBundleLoader(&FacetBundleLoaderOptions{
		ManifestPath: FacetBundlePathString(res.ManifestPath),
		Entry:        "main",
	})
	if err != nil {
		t.Fatalf("CreateFacetBundleLoader: %v", err)
	}
	tbnWriteFile(t, filepath.Join(outputDir, mainEntry.File), "export default {};\n")
	if _, err := loader.Load(); err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("expected integrity check failure on Load(), got %v", err)
	}
	if _, err := ReadFacetBundleArtifact(&ReadFacetBundleArtifactOptions{ManifestPath: FacetBundlePathString(res.ManifestPath), Entry: "main"}); err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("expected integrity check failure on ReadFacetBundleArtifact, got %v", err)
	}

	// VerifyIntegrity=false skips SHA-256 check and reaches unsupported CJS boundary.
	skipVerifyLoader, err := CreateFacetBundleLoader(&FacetBundleLoaderOptions{
		ManifestPath:    FacetBundlePathString(res.ManifestPath),
		Entry:           "main",
		VerifyIntegrity: tbnPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateFacetBundleLoader(verify=false): %v", err)
	}
	if _, err := skipVerifyLoader.Load(); err == nil || !strings.Contains(err.Error(), "unsupported in pi-go") {
		t.Fatalf("expected CJS unsupported error when VerifyIntegrity=false, got %v", err)
	}

	// Corrupt manifest validations.
	badManifestPath := filepath.Join(dir, "bad-manifest.json")
	tbnWriteFile(t, badManifestPath, `{"format":"wrong","formatVersion":2,"plugin":{"id":"x"},"entries":{"a":{"file":"a.cjs","integrity":"sha256-abc","externalImports":[]}}}`)
	if _, err := ReadFacetBundleManifest(FacetBundlePathString(badManifestPath)); err == nil || !strings.Contains(err.Error(), "Invalid facet bundle manifest format") {
		t.Fatalf("expected format error, got %v", err)
	}

	tbnWriteFile(t, badManifestPath, `{"format":"chord.facet-bundle","formatVersion":1,"plugin":{"id":"x"},"entries":{"a":{"file":"a.cjs","integrity":"sha256-abc","externalImports":[]}}}`)
	if _, err := ReadFacetBundleManifest(FacetBundlePathString(badManifestPath)); err == nil || !strings.Contains(err.Error(), "Unsupported facet bundle manifest version") {
		t.Fatalf("expected version error, got %v", err)
	}

	tbnWriteFile(t, badManifestPath, `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"x"},"entries":{"a":{"file":"../a.cjs","integrity":"sha256-abc","externalImports":[]}}}`)
	if _, err := ReadFacetBundleManifest(FacetBundlePathString(badManifestPath)); err == nil || !strings.Contains(err.Error(), "must be a filename relative to its manifest") {
		t.Fatalf("expected entry filename path error, got %v", err)
	}

	tbnWriteFile(t, badManifestPath, `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"x"},"entries":{"a":{"file":"a.cjs","integrity":"sha256-abc","externalImports":["pkg","pkg"]}}}`)
	if _, err := ReadFacetBundleManifest(FacetBundlePathString(badManifestPath)); err == nil || !strings.Contains(err.Error(), "duplicate external imports") {
		t.Fatalf("expected duplicate external imports error, got %v", err)
	}
}
