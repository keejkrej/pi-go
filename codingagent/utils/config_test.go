package utils

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVersionAndAssets(t *testing.T) {
	if VERSION != "1.5.2" {
		t.Fatalf("expected VERSION 1.5.2, got %q", VERSION)
	}
	for _, rel := range []string{
		"package/package.json",
		"package/README.md",
		"package/CHANGELOG.md",
		"theme/dark.json",
		"theme/light.json",
		"theme/theme-schema.json",
		"export-html/template.html",
		"export-html/template.css",
		"export-html/template.js",
		"interactive/clankolas.png",
	} {
		data, err := fs.ReadFile(ConfigAssetsFS(), rel)
		if err != nil || len(data) == 0 {
			t.Fatalf("expected embedded asset %s to be readable: err=%v len=%d", rel, err, len(data))
		}
	}
}

func TestGetPiUserAgent(t *testing.T) {
	ua := GetPiUserAgent("1.2.3")
	pattern := regexp.MustCompile(`^pi/1\.2\.3 \([^;]+; [^)]+\)$`)
	if !pattern.MatchString(ua) {
		t.Fatalf("unexpected user agent format: %q", ua)
	}
}

func TestConfigPathsAndInstallDetection(t *testing.T) {
	origExec := configExecPath
	origArgv := configArgv
	origPlat := configPlatform
	t.Cleanup(func() {
		configExecPath = origExec
		configArgv = origArgv
		configPlatform = origPlat
	})

	tmp := t.TempDir()
	t.Setenv(EnvAgentDir, filepath.Join(tmp, "agent"))
	t.Setenv(EnvSessionDir, filepath.Join(tmp, "sessions"))

	if got := GetAgentDir(); got != filepath.Join(tmp, "agent") {
		t.Fatalf("GetAgentDir() = %q", got)
	}
	if got := GetSessionsDir(); got != filepath.Join(tmp, "sessions") {
		t.Fatalf("GetSessionsDir() = %q", got)
	}
	if got := GetCustomThemesDir(); got != filepath.Join(tmp, "agent", "themes") {
		t.Fatalf("GetCustomThemesDir() = %q", got)
	}

	// FindNodePackageDir: locate an ancestor directory with package.json matching expected name.
	pkgDir := filepath.Join(tmp, "pkg-root")
	nested := filepath.Join(pkgDir, "dist", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"`+PackageName+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindNodePackageDir(nested); got != pkgDir {
		t.Fatalf("FindNodePackageDir() = %q, want %q", got, pkgDir)
	}

	// Install method detection via argv / execPath patterns (config.test.ts).
	configPlatform = func() string { return "darwin" }
	configExecPath = func() (string, error) { return "/usr/local/bin/node", nil }
	configArgv = func() []string {
		return []string{"node", "/usr/local/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"}
	}
	if m := DetectInstallMethod(); m != InstallMethodNpm {
		t.Fatalf("expected npm install method, got %q", m)
	}

	configArgv = func() []string {
		return []string{"node", "/Users/u/.local/share/pnpm/global/5/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"}
	}
	if m := DetectInstallMethod(); m != InstallMethodPnpm {
		t.Fatalf("expected pnpm install method, got %q", m)
	}

	configArgv = func() []string {
		return []string{"bun", "/Users/u/.bun/install/global/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"}
	}
	if m := DetectInstallMethod(); m != InstallMethodBun {
		t.Fatalf("expected bun install method, got %q", m)
	}

	// Self-update command generation (config.test.ts)
	configArgv = func() []string {
		return []string{"node", "/usr/local/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"}
	}
	cmd := GetSelfUpdateCommand(PackageName, nil, SelfUpdatePackageTarget{
		PackageName: PackageName,
		InstallSpec: PackageName + "@latest",
		Version:     "latest",
	})
	if cmd == nil || cmd.Command != "npm" || len(cmd.Args) == 0 || !strings.Contains(cmd.Display, PackageName+"@latest") {
		t.Fatalf("unexpected npm self-update command: %+v", cmd)
	}

	custom := MakeConfiguredSelfUpdateCommand([]string{"my-updater", "upgrade", "{packageName}", "{version}"}, SelfUpdatePackageTarget{
		PackageName: PackageName,
		InstallSpec: PackageName,
		Version:     "1.5.2",
	})
	if custom == nil || custom.Command != "my-updater" || len(custom.Args) != 3 || custom.Args[2] != "1.5.2" {
		t.Fatalf("unexpected configured self-update command: %+v", custom)
	}

	// DetectInstallChange detects version updates or removal at packageJsonPath.
	pkgJson := filepath.Join(pkgDir, "package.json")
	if err := os.WriteFile(pkgJson, []byte(`{"name":"`+PackageName+`","version":"9.9.9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ch := DetectInstallChange(pkgJson); ch == nil || ch.Kind != "updated" || ch.Version != "9.9.9" {
		t.Fatalf("expected updated install change 9.9.9, got %+v", ch)
	}
	if _, err := os.ReadFile(GetChangelogPath()); err != nil {
		t.Fatalf("expected extracted changelog at %s: %v", GetChangelogPath(), err)
	}
	if !strings.HasSuffix(GetThemesDir(), "theme") {
		t.Fatalf("unexpected themes dir: %s", GetThemesDir())
	}
	if !strings.HasPrefix(GetShareViewerUrl("abc"), "https://") {
		t.Fatalf("unexpected share viewer URL: %s", GetShareViewerUrl("abc"))
	}
}

func TestCodemodeWorkerConfig(t *testing.T) {
	if got := GetCodemodeWorkerSpecifier(); IsBunBinary && got == nil {
		t.Fatal("expected non-nil codemode worker specifier when IsBunBinary is true")
	} else if !IsBunBinary && got != nil {
		t.Fatalf("expected nil codemode worker specifier when IsBunBinary is false, got %q", *got)
	}
	bunSpec := ResolveCodemodeWorkerSpecifier("bun-binary", "")
	if bunSpec == nil || *bunSpec != "./src/extensions/codemode/worker.ts" {
		t.Fatalf("unexpected bun-binary codemode worker specifier: %v", bunSpec)
	}
	nodeSpec := ResolveCodemodeWorkerSpecifier("bundled-node", "file:///app/dist/cli.js")
	if nodeSpec == nil || *nodeSpec != "file:///app/dist/codemode-worker.js" {
		t.Fatalf("unexpected bundled-node codemode worker specifier: %v", nodeSpec)
	}
	if tsxSpec := ResolveCodemodeWorkerSpecifier("tsx", "file:///app/src/cli.ts"); tsxSpec != nil {
		t.Fatalf("expected nil codemode worker specifier for tsx mode, got %q", *tsxSpec)
	}
}

func TestSourceInfo(t *testing.T) {
	meta := PathMetadata{
		Source:  "npm:my-pkg",
		Scope:   SourceScopeUser,
		Origin:  SourceOriginPackage,
		BaseDir: "/tmp/skill",
	}
	info := CreateSourceInfo("/tmp/skill/SKILL.md", meta)
	if info.Path != "/tmp/skill/SKILL.md" || info.Scope != SourceScopeUser || info.BaseDir != "/tmp/skill" {
		t.Fatalf("unexpected SourceInfo: %+v", info)
	}
	pkgKey := GetSourcePackageKey(SourcePackageIdentity{
		Source: "npm:my-pkg",
		Scope:  SourceScopeUser,
	})
	if !strings.Contains(pkgKey, "npm:my-pkg") || !strings.Contains(pkgKey, string(SourceScopeUser)) {
		t.Fatalf("unexpected GetSourcePackageKey: %q", pkgKey)
	}

	syn := CreateSyntheticSourceInfo("builtin:foo", SyntheticSourceOptions{
		Source: "local",
		Scope:  SourceScopeUser,
		Origin: SourceOriginPackage,
	})
	if !IsSyntheticPath(syn.Path) {
		t.Fatalf("expected synthetic path to be recognized: %q", syn.Path)
	}
	if src := GetSyntheticPathSource(syn.Path); src == nil || *src == "" {
		t.Fatalf("expected non-empty synthetic source for %q, got %v", syn.Path, src)
	}
	if key := GetResourcePathKey(info.Path); key == "" {
		t.Fatal("expected non-empty resource path key")
	}
}

func TestOutputFilesAndWebUrl(t *testing.T) {
	p, err := WriteOutputFile("pi-test-output", ".txt", []byte("hello output"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "hello output" {
		t.Fatalf("read written output file: got=%q err=%v", string(got), err)
	}

	streamPath, f, err := CreateOutputFileStream("pi-stream", ".log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(streamPath) })
	_, _ = f.Write([]byte("streamed"))
	_ = f.Close()
	got, err = os.ReadFile(streamPath)
	if err != nil || !bytes.Equal(got, []byte("streamed")) {
		t.Fatalf("streamed file mismatch: got=%q err=%v", string(got), err)
	}

	if u := WebUrl("https://example.com/foo"); u == nil || *u != "https://example.com/foo" {
		t.Fatalf("expected valid https URL, got %v", u)
	}
	if u := WebUrl("ftp://example.com/foo"); u != nil {
		t.Fatalf("expected non-http(s) URL to return nil, got %v", *u)
	}
	if d := DisplayUrl("https://example.com/path"); !strings.Contains(d, "example.com") {
		t.Fatalf("unexpected DisplayUrl: %q", d)
	}
}
