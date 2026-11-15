// Ported from packages/coding-agent/test/experimental-presentation-facets.test.ts (pi v1.0.0).

package experimental_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

func tepfMkdirTemp(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatalf("MkdirTemp(%q): %v", prefix, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func tepfWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func tepfDecodeArtifacts(t *testing.T, data chord.JsonValue) []node.FacetBundleArtifact {
	t.Helper()
	raw, err := jsonx.Marshal(data)
	if err != nil {
		t.Fatalf("jsonx.Marshal(data): %v", err)
	}
	var wrapper struct {
		PresentationFacetBundles []node.FacetBundleArtifact `json:"presentationFacetBundles"`
	}
	if err := jsonx.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("jsonx.Unmarshal(artifacts): %v", err)
	}
	return wrapper.PresentationFacetBundles
}

func TestExperimentalPresentationFacetsRejectsLocalPluginPathsForRadiusServers(t *testing.T) {
	sid := protocol.ServerId(uuid.NewString())
	_, err := experimental.OpenClientRuntime(experimentalcli.ClientCommand{
		Command: "client",
		Connect: &experimentalcli.TransportAddress{
			Transport: experimentalcli.TransportType("radius"),
			ServerId:  sid,
		},
		PluginPackages: []string{"./local-plugin"},
	})
	if err == nil || !strings.Contains(err.Error(), "only be configured on a local Unix server") {
		t.Fatalf("expected Radius local plugin rejection error, got %v", err)
	}
}

func TestExperimentalPresentationFacetsRestoresPluginPackageSelections(t *testing.T) {
	directory := tepfMkdirTemp(t, "pi-pp-*")
	serverId := protocol.ServerId(uuid.NewString())
	packagePaths := []string{
		filepath.Join(directory, "first-plugin"),
		filepath.Join(directory, "second-plugin"),
	}

	got, err := plugins.RestoreServerPluginPackageProfile(directory, serverId, packagePaths)
	if err != nil {
		t.Fatalf("RestoreServerPluginPackageProfile(set): %v", err)
	}
	if !slices.Equal(got, packagePaths) {
		t.Fatalf("RestoreServerPluginPackageProfile(set) = %v, want %v", got, packagePaths)
	}

	got, err = plugins.RestoreServerPluginPackageProfile(directory, serverId)
	if err != nil {
		t.Fatalf("RestoreServerPluginPackageProfile(read1): %v", err)
	}
	if !slices.Equal(got, packagePaths) {
		t.Fatalf("RestoreServerPluginPackageProfile(read1) = %v, want %v", got, packagePaths)
	}

	got, err = plugins.RestoreServerPluginPackageProfile(directory, serverId, []string{})
	if err != nil {
		t.Fatalf("RestoreServerPluginPackageProfile(clear): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("RestoreServerPluginPackageProfile(clear) = %v, want empty", got)
	}

	got, err = plugins.RestoreServerPluginPackageProfile(directory, serverId)
	if err != nil {
		t.Fatalf("RestoreServerPluginPackageProfile(read2): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("RestoreServerPluginPackageProfile(read2) = %v, want empty", got)
	}
}

func TestExperimentalPresentationFacetsBuildsConventionalPluginEntriesAndReloads(t *testing.T) {
	directory := tepfMkdirTemp(t, "pi-pf-*")
	serverId := protocol.ServerId(uuid.NewString())
	packagePath := filepath.Join(directory, "pi-example-plugin")

	tepfWriteFile(
		t,
		filepath.Join(packagePath, "package.json"),
		`{"name":"@earendil-works/test-plugin","version":"1.0.0","peerDependencies":{"@earendil-works/chord":"^0.84.4","@earendil-works/pi-coding-agent":"^0.84.4"}}`+"\n",
	)
	sourcePath := filepath.Join(packagePath, "src", "tui.ts")
	tepfWriteFile(
		t,
		sourcePath,
		"import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-a\", setup(env) { env.use(SlashCommands); } });\n",
	)

	plugin, err := plugins.CreateServerPluginPackage(directory, serverId, packagePath)
	if err != nil {
		t.Fatalf("CreateServerPluginPackage: %v", err)
	}
	first, err := plugin.Build()
	if err != nil {
		t.Fatalf("plugin.Build(first): %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("len(first) = %d, want 1", len(first))
	}
	manifestPattern := regexp.MustCompile(`/plugin-builds/` + regexp.QuoteMeta(string(serverId)) + `/pi-example-plugin-[a-f0-9]{12}/chord-facets\.json$`)
	if !manifestPattern.MatchString(plugin.ManifestPath()) {
		t.Fatalf("ManifestPath() = %q, want match %s", plugin.ManifestPath(), manifestPattern.String())
	}
	if first[0].Plugin.Id != "@earendil-works/test-plugin" || first[0].Plugin.Version == nil || *first[0].Plugin.Version != "1.0.0" {
		t.Fatalf("unexpected plugin identity: %+v", first[0].Plugin)
	}

	firstData, err := plugins.CreatePresentationFacetData(first)
	if err != nil {
		t.Fatalf("CreatePresentationFacetData(first): %v", err)
	}
	firstLoaders, err := plugins.CreatePresentationFacetLoaders(firstData)
	if err != nil {
		t.Fatalf("CreatePresentationFacetLoaders(first): %v", err)
	}
	if len(firstLoaders) != 1 {
		t.Fatalf("len(firstLoaders) = %d, want 1", len(firstLoaders))
	}
	// Pure-Go pi-go cannot execute CJS JS bundles, so Load must return an explicit unsupported error without panicking.
	if _, err := firstLoaders[0].Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected pure-Go CJS unsupported error from first loader, got %v", err)
	}

	// Verify native Go facet loading works cleanly as the CGO_ENABLED=0 execution path.
	nativeLoader := chord.CreateStaticFacetLoader([]chord.Facet{
		chord.DefineFacet(chord.Facet{
			Id:    "built-a",
			Setup: func(env chord.FacetEnvironment) error { return nil },
		}),
	})
	nativeLoaded, err := nativeLoader.Load()
	if err != nil {
		t.Fatalf("nativeLoader.Load(): %v", err)
	}
	if len(nativeLoaded.Facets) != 1 || nativeLoaded.Facets[0].Id != "built-a" {
		t.Fatalf("unexpected native facets: %+v", nativeLoaded.Facets)
	}
	if err := nativeLoaded.Dispose(); err != nil {
		t.Fatalf("nativeLoaded.Dispose(): %v", err)
	}

	tepfWriteFile(
		t,
		sourcePath,
		"import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-b\", setup(env) { env.use(SlashCommands); } });\n",
	)
	second, err := plugin.Build()
	if err != nil {
		t.Fatalf("plugin.Build(second): %v", err)
	}
	if len(second) != 1 || second[0].Source == first[0].Source {
		t.Fatalf("expected rebuilt source to change; first=%q second=%q", first[0].Source, second[0].Source)
	}
	if !strings.Contains(second[0].Source, "built-b") {
		t.Fatalf("expected rebuilt source to contain built-b, got %q", second[0].Source)
	}

	secondPackagePath := filepath.Join(directory, "second-plugin")
	tepfWriteFile(
		t,
		filepath.Join(secondPackagePath, "package.json"),
		`{"name":"@earendil-works/second-test-plugin","version":"1.0.0","peerDependencies":{"@earendil-works/chord":"^0.84.4"}}`+"\n",
	)
	tepfWriteFile(
		t,
		filepath.Join(secondPackagePath, "src", "tui.ts"),
		"import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"second-built\", setup() {} });\n",
	)

	serverDir := filepath.Join(directory, "server")
	sessionDir := filepath.Join(directory, "sessions")
	running, err := experimental.StartServer(experimental.StartServerOptions{
		Directory:  serverDir,
		SessionDir: sessionDir,
	})
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	t.Cleanup(func() {
		_ = running.Close()
	})

	runtime, err := experimental.OpenClientRuntime(experimentalcli.ClientCommand{
		Command: "client",
		Connect: &experimentalcli.TransportAddress{
			Transport: experimentalcli.TransportTypeUnix,
			Path:      running.SocketPath,
		},
		PluginPackages: []string{packagePath, secondPackagePath},
	})
	if err != nil {
		t.Fatalf("OpenClientRuntime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Dispose()
	})
	if len(runtime.Servers) != 1 {
		t.Fatalf("len(runtime.Servers) = %d, want 1", len(runtime.Servers))
	}

	activated, err := experimental.ActivateBuiltinClientServices(runtime.Servers[0])
	if err != nil {
		t.Fatalf("ActivateBuiltinClientServices: %v", err)
	}
	sessionId := uuid.NewString()
	if _, err := activated.Management.Create(services.SessionCreateOptions{Id: &sessionId}, chord.BackgroundContext); err != nil {
		t.Fatalf("Management.Create: %v", err)
	}

	presentationPlugins, err := activated.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{
		SessionId:    sessionId,
		PackagePaths: []string{packagePath, secondPackagePath},
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("Plugins.PrepareSession: %v", err)
	}

	restoredProfile, err := plugins.RestoreServerPluginPackageProfile(serverDir, running.ServerId)
	if err != nil {
		t.Fatalf("RestoreServerPluginPackageProfile(running): %v", err)
	}
	if len(restoredProfile) != 0 {
		t.Fatalf("expected empty persisted server profile, got %v", restoredProfile)
	}

	preparedArtifacts := tepfDecodeArtifacts(t, presentationPlugins)
	if len(preparedArtifacts) != 2 {
		t.Fatalf("len(preparedArtifacts) = %d, want 2", len(preparedArtifacts))
	}
	if !strings.Contains(preparedArtifacts[0].Source, "built-b") || !strings.Contains(preparedArtifacts[1].Source, "second-built") {
		t.Fatalf("unexpected prepared sources: [0]=%q [1]=%q", preparedArtifacts[0].Source, preparedArtifacts[1].Source)
	}
	preparedLoaders, err := plugins.CreatePresentationFacetLoaders(presentationPlugins)
	if err != nil {
		t.Fatalf("CreatePresentationFacetLoaders(prepared): %v", err)
	}
	if len(preparedLoaders) != 2 {
		t.Fatalf("len(preparedLoaders) = %d, want 2", len(preparedLoaders))
	}
	for i, loader := range preparedLoaders {
		if _, err := loader.Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("preparedLoaders[%d].Load() = %v, want unsupported error", i, err)
		}
	}

	tepfWriteFile(
		t,
		sourcePath,
		"import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-c\", setup(env) { env.use(SlashCommands); } });\n",
	)
	remoteServices := runtime.Servers[0].Server.Open(&chord.RemoteServiceSourceOpenOptions{
		Services:     []chord.ServiceRef{{Id: services.PresentationPluginsService.Id}},
		AssertAccess: func() {},
		OnError:      func(error) {},
	})
	defer func() {
		_ = remoteServices.Dispose(chord.BackgroundContext)
	}()
	if err := remoteServices.Ready(chord.BackgroundContext); err != nil {
		t.Fatalf("remoteServices.Ready: %v", err)
	}
	reloadedData, err := services.AsPresentationPlugins(remoteServices.Use(services.PresentationPluginsService)).Reload(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("PresentationPlugins.Reload: %v", err)
	}
	reloadedArtifacts := tepfDecodeArtifacts(t, reloadedData)
	if len(reloadedArtifacts) != 2 {
		t.Fatalf("len(reloadedArtifacts) = %d, want 2", len(reloadedArtifacts))
	}
	if !strings.Contains(reloadedArtifacts[0].Source, "built-c") || !strings.Contains(reloadedArtifacts[1].Source, "second-built") {
		t.Fatalf("unexpected reloaded sources: [0]=%q [1]=%q", reloadedArtifacts[0].Source, reloadedArtifacts[1].Source)
	}
}

func TestExperimentalPresentationFacetsBuildsExamplePluginPackage(t *testing.T) {
	directory := tepfMkdirTemp(t, "pi-ex-*")
	serverId := protocol.ServerId(uuid.NewString())
	packagePath := filepath.Join(directory, "pi-example-plugin")

	tepfWriteFile(
		t,
		filepath.Join(packagePath, "package.json"),
		`{"name":"@earendil-works/pi-example-plugin","version":"1.0.0","peerDependencies":{"@earendil-works/chord":"^0.84.4","@earendil-works/pi-coding-agent":"^0.84.4"}}`+"\n",
	)
	tepfWriteFile(
		t,
		filepath.Join(packagePath, "src", "session.ts"),
		"import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"@earendil-works/pi-example-plugin/session\", setup() {} });\n",
	)
	tepfWriteFile(
		t,
		filepath.Join(packagePath, "src", "tui.ts"),
		"import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"@earendil-works/pi-example-plugin/tui\", setup() {} });\n",
	)

	plugin, err := plugins.CreateServerPluginPackage(directory, serverId, packagePath)
	if err != nil {
		t.Fatalf("CreateServerPluginPackage: %v", err)
	}
	artifacts, err := plugin.Build()
	if err != nil {
		t.Fatalf("plugin.Build(): %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(artifacts))
	}

	manifest, err := node.ReadFacetBundleManifest(node.FacetBundlePathString(plugin.ManifestPath()))
	if err != nil {
		t.Fatalf("ReadFacetBundleManifest: %v", err)
	}
	if manifest.Plugin.Id != "@earendil-works/pi-example-plugin" || manifest.Plugin.Version == nil || *manifest.Plugin.Version != "1.0.0" {
		t.Fatalf("unexpected manifest plugin: %+v", manifest.Plugin)
	}
	if manifest.Entries.Len() != 2 {
		t.Fatalf("manifest.Entries.Len() = %d, want 2", manifest.Entries.Len())
	}
	if _, hasSession := manifest.Entries.Get("session"); !hasSession {
		t.Fatalf("expected session entry in manifest")
	}
	if _, hasTui := manifest.Entries.Get("tui"); !hasTui {
		t.Fatalf("expected tui entry in manifest")
	}

	data, err := plugins.CreatePresentationFacetData(artifacts)
	if err != nil {
		t.Fatalf("CreatePresentationFacetData: %v", err)
	}
	loaders, err := plugins.CreatePresentationFacetLoaders(data)
	if err != nil {
		t.Fatalf("CreatePresentationFacetLoaders: %v", err)
	}
	if len(loaders) != 1 {
		t.Fatalf("len(loaders) = %d, want 1", len(loaders))
	}
	if _, err := loaders[0].Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected pure-Go CJS unsupported error from example loader, got %v", err)
	}
}
