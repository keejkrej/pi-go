// Ported from packages/coding-agent/test/package-manager.test.ts and package-manager-ssh.test.ts (pi v1.5.2).

package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tpmTarEntry struct {
	name     string
	content  string
	typeflag byte
	linkname string
	mode     int64
}

func tpmBuildTgz(t *testing.T, entries []tpmTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			if tf == tar.TypeDir {
				mode = 0o755
			} else {
				mode = 0o644
			}
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: tf,
			Linkname: e.linkname,
			Mode:     mode,
		}
		if tf == tar.TypeReg {
			hdr.Size = int64(len(e.content))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", e.name, err)
		}
		if tf == tar.TypeReg && len(e.content) > 0 {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatalf("Write(%s): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}

type tpmNpmVersionFixture struct {
	version      string
	tarballBytes []byte
	dependencies map[string]string
	skipDigest   bool
	customDigest string
}

type tpmMockNpmRegistry struct {
	mu       sync.Mutex
	server   *httptest.Server
	packages map[string]struct {
		distTags map[string]string
		versions map[string]tpmNpmVersionFixture
	}
	requests []string
}

func tpmNewMockNpmRegistry(t *testing.T) *tpmMockNpmRegistry {
	t.Helper()
	reg := &tpmMockNpmRegistry{
		packages: make(map[string]struct {
			distTags map[string]string
			versions map[string]tpmNpmVersionFixture
		}),
	}
	reg.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.requests = append(reg.requests, r.URL.Path)
		reg.mu.Unlock()

		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(reqPath, "tarballs/") {
			key := strings.TrimPrefix(reqPath, "tarballs/")
			// Format: <pkg>__<version>.tgz (with / replaced by __)
			reg.mu.Lock()
			var found *tpmNpmVersionFixture
			for pkgName, data := range reg.packages {
				safePkg := strings.ReplaceAll(pkgName, "/", "__")
				for ver, vf := range data.versions {
					if key == safePkg+"__"+ver+".tgz" {
						vfCopy := vf
						found = &vfCopy
						break
					}
				}
				if found != nil {
					break
				}
			}
			reg.mu.Unlock()
			if found == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(found.tarballBytes)
			return
		}

		reg.mu.Lock()
		pkgData, ok := reg.packages[reqPath]
		if !ok {
			reg.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		versionsObj := make(map[string]any, len(pkgData.versions))
		for ver, vf := range pkgData.versions {
			safePkg := strings.ReplaceAll(reqPath, "/", "__")
			tarURL := fmt.Sprintf("%s/tarballs/%s__%s.tgz", reg.server.URL, safePkg, ver)
			dist := map[string]any{
				"tarball": tarURL,
			}
			if vf.customDigest != "" {
				dist["integrity"] = vf.customDigest
			} else if !vf.skipDigest {
				sum1 := sha1.Sum(vf.tarballBytes)
				sum512 := sha512.Sum512(vf.tarballBytes)
				dist["shasum"] = hex.EncodeToString(sum1[:])
				dist["integrity"] = "sha512-" + base64.StdEncoding.EncodeToString(sum512[:])
			}
			entry := map[string]any{
				"name":    reqPath,
				"version": ver,
				"dist":    dist,
			}
			if len(vf.dependencies) > 0 {
				entry["dependencies"] = vf.dependencies
			}
			versionsObj[ver] = entry
		}
		distTagsCopy := make(map[string]string, len(pkgData.distTags))
		for k, v := range pkgData.distTags {
			distTagsCopy[k] = v
		}
		reg.mu.Unlock()

		resp := map[string]any{
			"name":      reqPath,
			"dist-tags": distTagsCopy,
			"versions":  versionsObj,
		}
		w.Header().Set("Content-Type", "application/json")
		if payload, err := jsonx.Marshal(resp); err == nil {
			_, _ = w.Write(payload)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	t.Cleanup(reg.server.Close)
	return reg
}

func (r *tpmMockNpmRegistry) publish(pkgName, tag string, vf tpmNpmVersionFixture) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.packages[pkgName]
	if entry.distTags == nil {
		entry.distTags = make(map[string]string)
	}
	if entry.versions == nil {
		entry.versions = make(map[string]tpmNpmVersionFixture)
	}
	entry.versions[vf.version] = vf
	if tag != "" {
		entry.distTags[tag] = vf.version
	}
	r.packages[pkgName] = entry
}

func (r *tpmMockNpmRegistry) setTag(pkgName, tag, ver string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.packages[pkgName]
	if entry.distTags == nil {
		entry.distTags = make(map[string]string)
	}
	entry.distTags[tag] = ver
	r.packages[pkgName] = entry
}

func (r *tpmMockNpmRegistry) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func tpmSetupFixture(t *testing.T, trusted bool) (tempDir, agentDir string, sm *SettingsManager, pm *DefaultPackageManager) {
	t.Helper()
	tempDir = t.TempDir()
	agentDir = filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sm = SettingsManagerInMemory()
	sm.SetProjectTrusted(trusted)
	pm = NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:             tempDir,
		AgentDir:        agentDir,
		SettingsManager: sm,
	})
	return tempDir, agentDir, sm, pm
}

func tpmWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", p, err)
	}
}

func tpmResourcePaths(resources []ResolvedResource) []string {
	out := make([]string, len(resources))
	for i, r := range resources {
		out[i] = r.Path
	}
	return out
}

func tpmEnabledPaths(resources []ResolvedResource) []string {
	var out []string
	for _, r := range resources {
		if r.Enabled {
			out = append(out, r.Path)
		}
	}
	return out
}

func tpmRunGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func tpmConfigureGitInsteadOf(t *testing.T, rewrites map[string]string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", fmt.Sprintf("%d", len(rewrites)))
	keys := make([]string, 0, len(rewrites))
	for k := range rewrites {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for i, fromURL := range keys {
		localPath := rewrites[fromURL]
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i), fmt.Sprintf("url.%s.insteadOf", localPath))
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i), fromURL)
	}
}

func TestPackageManagerGitSourceParsing(t *testing.T) {
	_, _, _, pm := tpmSetupFixture(t, true)

	if p := pmParseSource("https://github.com/user/repo"); p.kind != pmSourceGit || p.host != "github.com" || p.path != "user/repo" {
		t.Fatalf("https parsed = %+v", p)
	}
	if p := pmParseSource("ssh://git@github.com/user/repo"); p.kind != pmSourceGit || p.host != "github.com" || p.path != "user/repo" || p.repo != "ssh://git@github.com/user/repo" {
		t.Fatalf("ssh parsed = %+v", p)
	}
	if p := pmParseSource("git:git@github.com:user/repo"); p.kind != pmSourceGit || p.host != "github.com" || p.path != "user/repo" || p.repo != "git@github.com:user/repo" || p.pinned {
		t.Fatalf("git:git@ parsed = %+v", p)
	}
	if p := pmParseSource("git:github.com/user/repo@v1.0.0"); p.kind != pmSourceGit || p.ref != "v1.0.0" || !p.pinned {
		t.Fatalf("git:host@ref parsed = %+v", p)
	}
	if p := pmParseSource("github:owner/repo@v2.0.0"); p.kind != pmSourceGit || p.host != "github.com" || p.path != "owner/repo" || p.ref != "v2.0.0" || !p.pinned {
		t.Fatalf("github: parsed = %+v", p)
	}
	if p := pmParseSource("git@github.com:user/repo"); p.kind != pmSourceLocal {
		t.Fatalf("bare git@ without git: prefix parsed as %+v, want local", p)
	}
	if p := pmParseSource("github.com/user/repo"); p.kind != pmSourceLocal {
		t.Fatalf("bare host/path without git: prefix parsed as %+v, want local", p)
	}

	idPrefixed := pm.packageIdentity("git:git@github.com:user/repo")
	idHTTPS := pm.packageIdentity("https://github.com/user/repo")
	idSSH := pm.packageIdentity("ssh://git@github.com/user/repo")
	idHosted := pm.packageIdentity("github:user/repo")
	if idPrefixed != "git:github.com/user/repo" || idPrefixed != idHTTPS || idPrefixed != idSSH || idPrefixed != idHosted {
		t.Fatalf("identities mismatch: prefixed=%q https=%q ssh=%q hosted=%q", idPrefixed, idHTTPS, idSSH, idHosted)
	}
}

func TestPackageManagerLocalManifestFiltersIgnoreAndPrecedence(t *testing.T) {
	tempDir, agentDir, sm, pm := tpmSetupFixture(t, true)

	// Conventional local package with extensions, skills, prompts, themes, changelog, and ignore files.
	pkgDir := filepath.Join(tempDir, "my-pkg")
	tpmWriteFile(t, filepath.Join(pkgDir, "extensions", "a.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(pkgDir, "extensions", "ignored.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(pkgDir, "extensions", "sub", "index.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(pkgDir, "extensions", ".gitignore"), "ignored.ts\n")
	tpmWriteFile(t, filepath.Join(pkgDir, "skills", "skill-a", "SKILL.md"), "# Skill A")
	tpmWriteFile(t, filepath.Join(pkgDir, "skills", "skill-b.md"), "# Skill B")
	tpmWriteFile(t, filepath.Join(pkgDir, "prompts", "review.md"), "# Review")
	tpmWriteFile(t, filepath.Join(pkgDir, "themes", "solar.json"), `{"name":"solar"}`)
	tpmWriteFile(t, filepath.Join(pkgDir, "HISTORY.md"), "# Changelog")
	tpmWriteFile(t, filepath.Join(pkgDir, "package.json"), `{
		"name": "my-pkg",
		"pi": {
			"changelogPath": "HISTORY.md"
		}
	}`)

	// Extension package with explicit pi manifest patterns + glob exclusion + node_modules subentry.
	manifestPkg := filepath.Join(tempDir, "manifest-pkg")
	tpmWriteFile(t, filepath.Join(manifestPkg, "ext", "keep.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(manifestPkg, "ext", "skip.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(manifestPkg, "node_modules", "dep", "extensions", "dep.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(manifestPkg, "package.json"), `{
		"name": "manifest-pkg",
		"pi": {
			"extensions": ["ext/*.ts", "node_modules/dep/extensions", "!**/skip.ts"]
		}
	}`)

	sm.SetPackages([]PackageSource{
		PackageSourceFromString(pkgDir),
		PackageSourceFromObject(PackageSourceObject{
			Source:     manifestPkg,
			Extensions: []string{"-**/dep.ts"},
		}),
	})

	// Auto-discovered user resource symlink deduplication test + project higher precedence.
	projectExt := filepath.Join(tempDir, ".pi", "extensions", "shared.ts")
	userExt := filepath.Join(agentDir, "extensions", "shared.ts")
	tpmWriteFile(t, projectExt, "// project wins")
	tpmWriteFile(t, userExt, "// user loses if symlinked or distinct path")
	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(agentDir, "extensions", "shared-link.ts")
		if err := os.Symlink(projectExt, linkPath); err != nil {
			t.Fatalf("symlink failed: %v", err)
		}
	}

	resolved, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	extPaths := tpmResourcePaths(resolved.Extensions)
	if !slices.Contains(extPaths, filepath.Join(pkgDir, "extensions", "a.ts")) {
		t.Fatalf("missing a.ts in %v", extPaths)
	}
	if !slices.Contains(extPaths, filepath.Join(pkgDir, "extensions", "sub", "index.ts")) {
		t.Fatalf("missing sub/index.ts in %v", extPaths)
	}
	if slices.Contains(extPaths, filepath.Join(pkgDir, "extensions", "ignored.ts")) {
		t.Fatalf("ignored.ts should be ignored by .gitignore: %v", extPaths)
	}
	if !slices.Contains(extPaths, filepath.Join(manifestPkg, "ext", "keep.ts")) {
		t.Fatalf("missing manifest ext/keep.ts in %v", extPaths)
	}
	if slices.Contains(extPaths, filepath.Join(manifestPkg, "ext", "skip.ts")) {
		t.Fatalf("skip.ts should be excluded by manifest !**/skip.ts: %v", extPaths)
	}

	// Verify package filter `-**/dep.ts` keeps resource in list with Enabled=false.
	var foundDep *ResolvedResource
	for i := range resolved.Extensions {
		if resolved.Extensions[i].Path == filepath.Join(manifestPkg, "node_modules", "dep", "extensions", "dep.ts") {
			foundDep = &resolved.Extensions[i]
			break
		}
	}
	if foundDep == nil || foundDep.Enabled {
		t.Fatalf("expected dep.ts present with Enabled=false, got %+v", foundDep)
	}

	// Verify ChangelogPath attached cleanly on my-pkg resources.
	for _, r := range resolved.Prompts {
		if r.Path == filepath.Join(pkgDir, "prompts", "review.md") {
			if r.Metadata.ChangelogPath != filepath.Join(pkgDir, "HISTORY.md") {
				t.Fatalf("ChangelogPath = %q, want HISTORY.md", r.Metadata.ChangelogPath)
			}
		}
	}

	// Verify symlink deduplicated against projectExt (which has higher precedence rank 1 < 3).
	if runtime.GOOS != "windows" {
		if slices.Contains(extPaths, filepath.Join(agentDir, "extensions", "shared-link.ts")) {
			t.Fatalf("expected symlink pointing to projectExt to be deduplicated, got %v", extPaths)
		}
	}

	// Verify security: changelogPath traversing outside packageRoot is rejected.
	tpmWriteFile(t, filepath.Join(tempDir, "outside.md"), "# secret")
	tpmWriteFile(t, filepath.Join(pkgDir, "package.json"), `{
		"name": "my-pkg",
		"pi": {
			"changelogPath": "../outside.md"
		}
	}`)
	resolved2, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve 2 failed: %v", err)
	}
	for _, r := range resolved2.Prompts {
		if r.Path == filepath.Join(pkgDir, "prompts", "review.md") && r.Metadata.ChangelogPath != "" {
			t.Fatalf("expected escaping changelogPath to be rejected, got %q", r.Metadata.ChangelogPath)
		}
	}
}

func TestPackageManagerAutoloadDeltaAndBuiltinsAndUntrustedProject(t *testing.T) {
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, "agent")
	userPkg := filepath.Join(tempDir, "user-pkg")
	tpmWriteFile(t, filepath.Join(userPkg, "extensions", "one.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(userPkg, "extensions", "two.ts"), "export default function() {}")

	// Set up .git repo root so ancestor .agents/skills stops at tempDir.
	if err := os.MkdirAll(filepath.Join(tempDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tempDir, "sub", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpmWriteFile(t, filepath.Join(tempDir, ".agents", "skills", "team-skill", "SKILL.md"), "# Team")
	tpmWriteFile(t, filepath.Join(subDir, ".pi", "extensions", "proj-ext.ts"), "export default function() {}")

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(true)
	sm.SetPackages([]PackageSource{PackageSourceFromString(userPkg)})
	autoloadFalse := false
	_ = sm.SetProjectPackages([]PackageSource{
		PackageSourceFromObject(PackageSourceObject{
			Source:     userPkg,
			Autoload:   &autoloadFalse,
			Extensions: []string{"-one.ts"},
		}),
	})
	sm.SetExtensionPaths([]string{"-builtin:mcp"})
	_ = sm.SetProjectExtensionPaths([]string{"+builtin:mcp"})

	pm := NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:               subDir,
		AgentDir:          agentDir,
		SettingsManager:   sm,
		BuiltinExtensions: []string{"mcp", "search"},
	})
	resolved, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	var oneEnabled, twoEnabled, mcpEnabled, searchEnabled bool
	for _, r := range resolved.Extensions {
		switch r.Path {
		case filepath.Join(userPkg, "extensions", "one.ts"):
			oneEnabled = r.Enabled
		case filepath.Join(userPkg, "extensions", "two.ts"):
			twoEnabled = r.Enabled
		case "builtin:mcp":
			mcpEnabled = r.Enabled
		case "builtin:search":
			searchEnabled = r.Enabled
		}
	}
	if oneEnabled || !twoEnabled {
		t.Fatalf("autoload:false delta mismatch: oneEnabled=%v twoEnabled=%v", oneEnabled, twoEnabled)
	}
	if !mcpEnabled || !searchEnabled {
		t.Fatalf("builtin override mismatch: mcpEnabled=%v searchEnabled=%v", mcpEnabled, searchEnabled)
	}
	if !slices.Contains(tpmResourcePaths(resolved.Skills), filepath.Join(tempDir, ".agents", "skills", "team-skill", "SKILL.md")) {
		t.Fatalf("expected ancestor .agents/skills to be discovered: %+v", resolved.Skills)
	}

	// Untrusted project excludes .pi resources and .agents/skills and blocks local mutations.
	sm.SetProjectTrusted(false)
	untrustedResolved, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("untrusted Resolve failed: %v", err)
	}
	if slices.Contains(tpmResourcePaths(untrustedResolved.Extensions), filepath.Join(subDir, ".pi", "extensions", "proj-ext.ts")) {
		t.Fatalf("untrusted project should not load .pi/extensions")
	}
	if slices.Contains(tpmResourcePaths(untrustedResolved.Skills), filepath.Join(tempDir, ".agents", "skills", "team-skill", "SKILL.md")) {
		t.Fatalf("untrusted project should not load .agents/skills")
	}
	if err := pm.Install(t.Context(), "npm:example", &PackageInstallOptions{Local: true}); err == nil {
		t.Fatal("expected error installing to untrusted project scope")
	}
}

func TestPackageManagerNpmOfflineInstallUpdateRemoveAndDeps(t *testing.T) {
	reg := tpmNewMockNpmRegistry(t)
	t.Setenv("NPM_CONFIG_REGISTRY", reg.server.URL)

	depV1 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"shared-dep","version":"1.0.0"}`},
		{name: "package/index.js", content: "module.exports = 1;\n"},
	})
	reg.publish("shared-dep", "latest", tpmNpmVersionFixture{version: "1.0.0", tarballBytes: depV1})

	pkgV1 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"hello-ext","version":"1.0.0","dependencies":{"shared-dep":"^1.0.0"}}`},
		{name: "package/extensions/main.ts", content: "export default function() {}"},
	})
	pkgV2 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"hello-ext","version":"1.2.0","dependencies":{"shared-dep":"^1.0.0"}}`},
		{name: "package/extensions/main.ts", content: "export default function() {} // v2"},
	})
	scopedV1 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"@acme/tools","version":"2.1.0"}`},
		{name: "package/skills/helper/SKILL.md", content: "# Helper"},
	})
	pinnedV1 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"pinned-ext","version":"1.0.0"}`},
		{name: "package/prompts/p.md", content: "# Pinned"},
	})
	pinnedV2 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"pinned-ext","version":"2.0.0"}`},
		{name: "package/prompts/p.md", content: "# Pinned 2"},
	})

	reg.publish("hello-ext", "latest", tpmNpmVersionFixture{
		version:      "1.0.0",
		tarballBytes: pkgV1,
		dependencies: map[string]string{"shared-dep": "^1.0.0"},
	})
	reg.publish("@acme/tools", "latest", tpmNpmVersionFixture{version: "2.1.0", tarballBytes: scopedV1})
	reg.publish("pinned-ext", "latest", tpmNpmVersionFixture{version: "1.0.0", tarballBytes: pinnedV1})

	tempDir, agentDir, sm, pm := tpmSetupFixture(t, true)
	var events []ProgressEvent
	var evMu sync.Mutex
	pm.SetProgressCallback(func(ev ProgressEvent) {
		evMu.Lock()
		events = append(events, ev)
		evMu.Unlock()
	})

	if err := pm.InstallAndPersist(t.Context(), "npm:hello-ext"); err != nil {
		t.Fatalf("InstallAndPersist hello-ext: %v", err)
	}
	if err := pm.InstallAndPersist(t.Context(), "npm:@acme/tools@^2.0.0", &PackageInstallOptions{Local: true}); err != nil {
		t.Fatalf("InstallAndPersist @acme/tools local: %v", err)
	}
	if err := pm.InstallAndPersist(t.Context(), "npm:pinned-ext@1.0.0"); err != nil {
		t.Fatalf("InstallAndPersist pinned-ext: %v", err)
	}

	globalPath := filepath.Join(agentDir, "npm", "node_modules", "hello-ext")
	localScopedPath := filepath.Join(tempDir, ".pi", "npm", "node_modules", "@acme", "tools")
	depPath := filepath.Join(globalPath, "node_modules", "shared-dep", "index.js")
	if !pmPathExists(filepath.Join(globalPath, "extensions", "main.ts")) {
		t.Fatalf("missing installed global npm extension at %s", globalPath)
	}
	if !pmPathExists(depPath) {
		t.Fatalf("missing transitive npm dependency at %s", depPath)
	}
	if !pmPathExists(filepath.Join(localScopedPath, "skills", "helper", "SKILL.md")) {
		t.Fatalf("missing installed local scoped npm package at %s", localScopedPath)
	}
	if got := pm.GetInstalledPath("npm:hello-ext"); got == nil || *got != globalPath {
		t.Fatalf("GetInstalledPath user = %v, want %s", got, globalPath)
	}
	if got := pm.GetInstalledPath("npm:@acme/tools", "project"); got == nil || *got != localScopedPath {
		t.Fatalf("GetInstalledPath project = %v, want %s", got, localScopedPath)
	}

	// Verify <agentDir>/npm/package.json tracks installed dependency.
	npmPkgBytes, err := os.ReadFile(filepath.Join(agentDir, "npm", "package.json"))
	if err != nil || !strings.Contains(string(npmPkgBytes), `"hello-ext"`) {
		t.Fatalf("expected hello-ext in global npm package.json, got %q err=%v", string(npmPkgBytes), err)
	}

	// Publish new versions and check updates.
	reg.publish("hello-ext", "latest", tpmNpmVersionFixture{
		version:      "1.2.0",
		tarballBytes: pkgV2,
		dependencies: map[string]string{"shared-dep": "^1.0.0"},
	})
	reg.publish("pinned-ext", "latest", tpmNpmVersionFixture{version: "2.0.0", tarballBytes: pinnedV2})

	updates, err := pm.CheckForAvailableUpdates(t.Context())
	if err != nil {
		t.Fatalf("CheckForAvailableUpdates: %v", err)
	}
	if len(updates) != 1 || updates[0].DisplayName != "hello-ext" || updates[0].LatestVersion != "1.2.0" {
		t.Fatalf("unexpected updates: %+v", updates)
	}

	if err := pm.Update(t.Context()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ver := pmGetInstalledNpmVersion(globalPath); ver != "1.2.0" {
		t.Fatalf("updated hello-ext version = %q, want 1.2.0", ver)
	}
	if ver := pmGetInstalledNpmVersion(filepath.Join(agentDir, "npm", "node_modules", "pinned-ext")); ver != "1.0.0" {
		t.Fatalf("pinned-ext should remain 1.0.0, got %q", ver)
	}

	evMu.Lock()
	var resultMsgs []string
	for _, ev := range events {
		if ev.Type == "result" && ev.Action == "update" {
			resultMsgs = append(resultMsgs, ev.Message)
		}
	}
	evMu.Unlock()
	if !slices.Contains(resultMsgs, "npm:hello-ext 1.0.0 -> 1.2.0") {
		t.Fatalf("expected result progress event, got %v", resultMsgs)
	}

	// Stale metadata warning: registry advertises 1.3.0 but dist serves 1.2.0 tarball.
	reg.publish("hello-ext", "latest", tpmNpmVersionFixture{
		version:      "1.3.0",
		tarballBytes: pkgV2,
		dependencies: map[string]string{"shared-dep": "^1.0.0"},
	})
	if err := pm.Update(t.Context(), "npm:hello-ext"); err != nil {
		t.Fatalf("stale update: %v", err)
	}
	evMu.Lock()
	sawStaleWarn := false
	for _, ev := range events {
		if ev.Type == "progress" && strings.Contains(ev.Message, "stale") {
			sawStaleWarn = true
			break
		}
	}
	evMu.Unlock()
	if !sawStaleWarn {
		t.Fatal("expected stale registry metadata warning progress event")
	}

	// PI_OFFLINE prevents network calls cleanly.
	reqBefore := reg.requestCount()
	t.Setenv("PI_OFFLINE", "1")
	if updatesOffline, err := pm.CheckForAvailableUpdates(t.Context()); err != nil || len(updatesOffline) != 0 {
		t.Fatalf("offline CheckForAvailableUpdates = %+v err=%v", updatesOffline, err)
	}
	if err := pm.Update(t.Context()); err != nil {
		t.Fatalf("offline Update err=%v", err)
	}
	if reg.requestCount() != reqBefore {
		t.Fatalf("expected zero registry requests in offline mode, got %d -> %d", reqBefore, reg.requestCount())
	}
	t.Setenv("PI_OFFLINE", "")

	// Missing package handling on Resolve (onMissing Skip / Abort / non-fatal InstallFailure).
	sm.SetPackages(append(sm.GetGlobalSettings().Packages, PackageSourceFromString("npm:does-not-exist")))
	skippedResolved, err := pm.Resolve(t.Context(), func(source string) (MissingSourceAction, error) {
		if source == "npm:does-not-exist" {
			return MissingSourceSkip, nil
		}
		return MissingSourceInstall, nil
	})
	if err != nil || len(skippedResolved.InstallFailures) != 0 {
		t.Fatalf("Resolve with skip: failures=%+v err=%v", skippedResolved.InstallFailures, err)
	}
	if _, err := pm.Resolve(t.Context(), func(string) (MissingSourceAction, error) {
		return MissingSourceAbort, nil
	}); err == nil {
		t.Fatal("expected Resolve error on MissingSourceAbort")
	}
	failedResolved, err := pm.Resolve(t.Context())
	if err != nil || len(failedResolved.InstallFailures) != 1 || failedResolved.InstallFailures[0].Source != "npm:does-not-exist" {
		t.Fatalf("expected non-fatal InstallFailures entry, got %+v err=%v", failedResolved.InstallFailures, err)
	}

	// Remove scoped local package cleanly.
	removed, err := pm.RemoveAndPersist(t.Context(), "npm:@acme/tools", &PackageRemoveOptions{Local: true})
	if err != nil || !removed {
		t.Fatalf("RemoveAndPersist local = (%v, %v)", removed, err)
	}
	if pmPathExists(localScopedPath) || pmPathExists(filepath.Dir(localScopedPath)) {
		t.Fatalf("expected scoped package and empty @acme dir removed")
	}
}

func TestPackageManagerNpmTarSecurity(t *testing.T) {
	reg := tpmNewMockNpmRegistry(t)
	t.Setenv("NPM_CONFIG_REGISTRY", reg.server.URL)

	cases := []struct {
		name  string
		vf    tpmNpmVersionFixture
		check string
	}{
		{
			name: "slip-pkg",
			vf: tpmNpmVersionFixture{
				version: "1.0.0",
				tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
					{name: "package/../../evil.txt", content: "pwned"},
				}),
			},
			check: "traverses parent directory",
		},
		{
			name: "abs-pkg",
			vf: tpmNpmVersionFixture{
				version: "1.0.0",
				tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
					{name: "/tmp/abs-evil.txt", content: "pwned"},
				}),
			},
			check: "absolute path",
		},
		{
			name: "digest-pkg",
			vf: tpmNpmVersionFixture{
				version: "1.0.0",
				tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
					{name: "package/package.json", content: `{"name":"digest-pkg","version":"1.0.0"}`},
				}),
				customDigest: "sha512-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			},
			check: "integrity mismatch",
		},
	}

	if runtime.GOOS != "windows" {
		cases = append(cases,
			struct {
				name  string
				vf    tpmNpmVersionFixture
				check string
			}{
				name: "symlink-escape-pkg",
				vf: tpmNpmVersionFixture{
					version: "1.0.0",
					tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
						{name: "package/package.json", content: `{"name":"symlink-escape-pkg","version":"1.0.0"}`},
						{name: "package/badlink", typeflag: tar.TypeSymlink, linkname: "../../../outside"},
					}),
				},
				check: "escapes extraction root",
			},
			struct {
				name  string
				vf    tpmNpmVersionFixture
				check string
			}{
				name: "symlink-write-through-pkg",
				vf: tpmNpmVersionFixture{
					version: "1.0.0",
					tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
						{name: "package/dirlink", typeflag: tar.TypeSymlink, linkname: "/etc"},
					}),
				},
				check: "must not be absolute",
			},
		)
	}

	_, agentDir, _, pm := tpmSetupFixture(t, true)
	for _, tc := range cases {
		reg.publish(tc.name, "latest", tc.vf)
		err := pm.Install(t.Context(), "npm:"+tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.check) {
			t.Fatalf("%s: expected error containing %q, got %v", tc.name, tc.check, err)
		}
		if pmPathExists(filepath.Join(agentDir, "npm", "node_modules", tc.name)) {
			t.Fatalf("%s: expected failed install not to leave targetDir behind", tc.name)
		}
	}
}

func TestPackageManagerGitLifecycleAndOfflineAndDeps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}

	reg := tpmNewMockNpmRegistry(t)
	t.Setenv("NPM_CONFIG_REGISTRY", reg.server.URL)
	depTgz := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"git-dep","version":"1.0.0"}`},
		{name: "package/index.js", content: "module.exports = 'ok';\n"},
	})
	reg.publish("git-dep", "latest", tpmNpmVersionFixture{version: "1.0.0", tarballBytes: depTgz})

	// Create local origin repo with commit + v1.0.0 tag.
	originDir := filepath.Join(t.TempDir(), "origin-repo")
	tpmRunGitCmd(t, "", "init", originDir)
	tpmRunGitCmd(t, originDir, "config", "user.email", "test@example.com")
	tpmRunGitCmd(t, originDir, "config", "user.name", "Test")
	tpmWriteFile(t, filepath.Join(originDir, "package.json"), `{"name":"git-ext","version":"1.0.0","dependencies":{"git-dep":"^1.0.0"}}`)
	tpmWriteFile(t, filepath.Join(originDir, "extensions", "tool.ts"), "export default function() {}")
	tpmRunGitCmd(t, originDir, "add", "-A")
	tpmRunGitCmd(t, originDir, "commit", "-m", "init")
	commit1 := tpmRunGitCmd(t, originDir, "rev-parse", "HEAD")
	tpmRunGitCmd(t, originDir, "tag", "v1.0.0")

	fakeRemote := "https://git.example.test/acme/git-ext"
	hostedRemote := "https://github.com/acme/hosted-ext"
	tpmConfigureGitInsteadOf(t, map[string]string{
		fakeRemote:   originDir,
		hostedRemote: originDir,
	})

	_, agentDir, sm, pm := tpmSetupFixture(t, true)
	if err := pm.InstallAndPersist(t.Context(), fakeRemote); err != nil {
		t.Fatalf("InstallAndPersist git: %v", err)
	}
	installedDir := filepath.Join(agentDir, "git", "git.example.test", "acme", "git-ext")
	if !pmPathExists(filepath.Join(installedDir, "extensions", "tool.ts")) {
		t.Fatalf("expected cloned extension at %s", installedDir)
	}
	if !pmPathExists(filepath.Join(installedDir, "node_modules", "git-dep", "index.js")) {
		t.Fatalf("expected pure-Go installed git npm dependency in %s", installedDir)
	}

	// Push commit 2 and untracked file inside installed clone to verify update + git clean -fdx.
	tpmWriteFile(t, filepath.Join(installedDir, "untracked-stale.txt"), "stale")
	tpmWriteFile(t, filepath.Join(originDir, "extensions", "tool2.ts"), "export default function() {}")
	tpmRunGitCmd(t, originDir, "add", "-A")
	tpmRunGitCmd(t, originDir, "commit", "-m", "v2")
	commit2 := tpmRunGitCmd(t, originDir, "rev-parse", "HEAD")

	updates, err := pm.CheckForAvailableUpdates(t.Context())
	if err != nil || len(updates) != 1 || updates[0].Type != "git" || updates[0].LatestVersion != commit2 {
		t.Fatalf("CheckForAvailableUpdates git = %+v err=%v", updates, err)
	}

	var updateMsgs []string
	pm.SetProgressCallback(func(ev ProgressEvent) {
		if ev.Type == "result" && ev.Action == "update" {
			updateMsgs = append(updateMsgs, ev.Message)
		}
	})
	if err := pm.Update(t.Context()); err != nil {
		t.Fatalf("Update git: %v", err)
	}
	if head := tpmRunGitCmd(t, installedDir, "rev-parse", "HEAD"); head != commit2 {
		t.Fatalf("HEAD after update = %s, want %s", head, commit2)
	}
	if pmPathExists(filepath.Join(installedDir, "untracked-stale.txt")) {
		t.Fatalf("expected git clean -fdx to remove untracked-stale.txt")
	}
	wantMsg := fmt.Sprintf("%s %s -> %s", fakeRemote, commit1[:7], commit2[:7])
	if !slices.Contains(updateMsgs, wantMsg) {
		t.Fatalf("expected update result %q, got %v", wantMsg, updateMsgs)
	}

	// Verify hostedgit github:owner/repo@v1.0.0 checks out tag v1.0.0 (commit1).
	if err := pm.InstallAndPersist(t.Context(), "github:acme/hosted-ext@v1.0.0", &PackageInstallOptions{Local: true}); err != nil {
		t.Fatalf("Install github: @v1.0.0: %v", err)
	}
	hostedPath := pm.GetInstalledPath("github:acme/hosted-ext@v1.0.0", "project")
	if hostedPath == nil {
		t.Fatal("expected hostedPath to exist")
	}
	if head := tpmRunGitCmd(t, *hostedPath, "rev-parse", "HEAD"); head != commit1 {
		t.Fatalf("pinned tag HEAD = %s, want %s", head, commit1)
	}

	// Temporary commit hash skip check (works even if remote rewrite points nowhere or offline).
	tempRes, err := pm.ResolveExtensionSources(t.Context(), []string{fakeRemote + "@" + commit1}, &ResolveExtensionSourcesOptions{Temporary: true})
	if err != nil || len(tempRes.Extensions) != 1 {
		t.Fatalf("temp ResolveExtensionSources = %+v err=%v", tempRes, err)
	}
	// Resolve second time at same commit hash skips refresh cleanly.
	pullCalled := false
	pm.SetProgressCallback(func(ev ProgressEvent) {
		if ev.Action == "pull" {
			pullCalled = true
		}
	})
	if _, err := pm.ResolveExtensionSources(t.Context(), []string{fakeRemote + "@" + commit1}, &ResolveExtensionSourcesOptions{Temporary: true}); err != nil {
		t.Fatalf("second temp resolve: %v", err)
	}
	if pullCalled {
		t.Fatal("expected immutable commit SHA temporary source not to re-pull")
	}

	// Remove cleans directory and empty parent directories + settings.
	_ = sm
	removed, err := pm.RemoveAndPersist(t.Context(), fakeRemote)
	if err != nil || !removed {
		t.Fatalf("RemoveAndPersist git = (%v, %v)", removed, err)
	}
	if pmPathExists(filepath.Join(agentDir, "git", "git.example.test")) {
		t.Fatalf("expected empty parent dirs pruned after git remove")
	}

	// Custom HTTPClient option check: verify provided HTTPClient is wired and honored.
	customHit := false
	customReg := tpmNewMockNpmRegistry(t)
	customReg.publish("custom-http-pkg", "latest", tpmNpmVersionFixture{
		version: "1.0.0",
		tarballBytes: tpmBuildTgz(t, []tpmTarEntry{
			{name: "package/package.json", content: `{"name":"custom-http-pkg","version":"1.0.0"}`},
		}),
	})
	t.Setenv("NPM_CONFIG_REGISTRY", customReg.server.URL)
	baseClient := customReg.server.Client()
	customClient := &http.Client{
		Transport: tpmRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			customHit = true
			return baseClient.Transport.RoundTrip(req)
		}),
	}
	pmCustom := NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:             t.TempDir(),
		AgentDir:        t.TempDir(),
		SettingsManager: SettingsManagerInMemory(),
		HTTPClient:      customClient,
	})
	if err := pmCustom.Install(t.Context(), "npm:custom-http-pkg"); err != nil || !customHit {
		t.Fatalf("custom HTTPClient Install err=%v customHit=%v", err, customHit)
	}
}

type tpmRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tpmRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPackageManagerFiltersIgnoreGlobsAndTempNpmRefresh(t *testing.T) {
	tempDir, _, sm, pm := tpmSetupFixture(t, true)

	// Directory glob trailing slash ("./extension-groups/*/") + .fdignore + empty [] filter + direct file.
	globPkg := filepath.Join(tempDir, "glob-pkg")
	tpmWriteFile(t, filepath.Join(globPkg, "extension-groups", "alpha", "index.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(globPkg, "extension-groups", "alpha", "skip.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(globPkg, "extension-groups", "loose.ts"), "export default function() {}")
	tpmWriteFile(t, filepath.Join(globPkg, "prompts", "good.md"), "# Good")
	tpmWriteFile(t, filepath.Join(globPkg, "prompts", "skip.md"), "# Skip")
	tpmWriteFile(t, filepath.Join(globPkg, "prompts", ".fdignore"), "skip.md\n")
	tpmWriteFile(t, filepath.Join(globPkg, "package.json"), `{
		"name": "glob-pkg",
		"pi": {
			"extensions": ["./extension-groups/*/"]
		}
	}`)

	directFile := filepath.Join(tempDir, "single.ts")
	tpmWriteFile(t, directFile, "export default function() {}")

	// Empty [] prompts disables all prompts in globPkg while keeping extension-groups/alpha/index.ts.
	sm.SetPackages([]PackageSource{
		PackageSourceFromObject(PackageSourceObject{
			Source:  globPkg,
			Prompts: []string{},
		}),
		PackageSourceFromString(directFile),
	})

	// Test local normalization helper AddSourceToSettings / RemoveSourceFromSettings.
	extraPkg := filepath.Join(tempDir, ".pi", "local-extra")
	tpmWriteFile(t, filepath.Join(extraPkg, "themes", "dark.json"), "{}")
	if !pm.AddSourceToSettings(extraPkg, true) {
		t.Fatal("expected AddSourceToSettings local true")
	}
	if pm.AddSourceToSettings(extraPkg, true) {
		t.Fatal("expected duplicate AddSourceToSettings local false")
	}
	configured := pm.GetInstalledPackages()
	foundNormalized := false
	for _, cp := range configured {
		if cp.Scope == "project" && cp.Source == "local-extra" {
			foundNormalized = true
		}
	}
	if !foundNormalized {
		t.Fatalf("expected relative local-extra in configured packages, got %+v", configured)
	}

	resolved, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	extPaths := tpmResourcePaths(resolved.Extensions)
	if !slices.Contains(extPaths, filepath.Join(globPkg, "extension-groups", "alpha", "index.ts")) {
		t.Fatalf("expected extension-groups/alpha/index.ts via trailing slash glob, got %v", extPaths)
	}
	if slices.Contains(extPaths, filepath.Join(globPkg, "extension-groups", "loose.ts")) {
		t.Fatalf("expected loose.ts NOT matched by trailing slash dir glob, got %v", extPaths)
	}
	if !slices.Contains(extPaths, directFile) {
		t.Fatalf("expected direct single.ts extension, got %v", extPaths)
	}
	if enabledPrompts := tpmEnabledPaths(resolved.Prompts); len(enabledPrompts) != 0 {
		t.Fatalf("expected empty prompts filter [] to disable prompts, got %v", enabledPrompts)
	}

	// Temporary unpinned npm source refreshes cleanly to newer version on second ResolveExtensionSources.
	reg := tpmNewMockNpmRegistry(t)
	t.Setenv("NPM_CONFIG_REGISTRY", reg.server.URL)
	v1 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"temp-pkg","version":"1.0.0"}`},
		{name: "package/extensions/ext.ts", content: "v1"},
	})
	v2 := tpmBuildTgz(t, []tpmTarEntry{
		{name: "package/package.json", content: `{"name":"temp-pkg","version":"1.1.0"}`},
		{name: "package/extensions/ext.ts", content: "v2"},
	})
	reg.publish("temp-pkg", "latest", tpmNpmVersionFixture{version: "1.0.0", tarballBytes: v1})
	resTemp1, err := pm.ResolveExtensionSources(t.Context(), []string{"npm:temp-pkg"}, &ResolveExtensionSourcesOptions{Temporary: true})
	if err != nil || len(resTemp1.Extensions) != 1 {
		t.Fatalf("temp resolve 1: %+v err=%v", resTemp1, err)
	}
	reg.publish("temp-pkg", "latest", tpmNpmVersionFixture{version: "1.1.0", tarballBytes: v2})
	if _, err := pm.ResolveExtensionSources(t.Context(), []string{"npm:temp-pkg"}, &ResolveExtensionSourcesOptions{Temporary: true}); err != nil {
		t.Fatalf("temp resolve 2: %v", err)
	}
	data, err := os.ReadFile(resTemp1.Extensions[0].Path)
	if err != nil || string(data) != "v2" {
		t.Fatalf("expected temporary unpinned npm package refreshed to v2, got %q err=%v", string(data), err)
	}
}
