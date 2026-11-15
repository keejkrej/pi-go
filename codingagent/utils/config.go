// Ported from packages/coding-agent/src/config.ts (pi v1.5.2).

package utils

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// PackageName is the npm package name.
	PackageName = "@earendil-works/pi-coding-agent"
	// AppName is the CLI application name.
	AppName = "pi"
	// AppTitle is the application display title.
	AppTitle = "π"
	// ConfigDirName is the user configuration directory name.
	ConfigDirName = ".pi"
	// VERSION matches packages/coding-agent/package.json (pi v1.5.2).
	VERSION = "1.5.2"
	// EnvAgentDir overrides the coding-agent configuration directory.
	EnvAgentDir = "PI_CODING_AGENT_DIR"
	// EnvSessionDir overrides the coding-agent session directory.
	EnvSessionDir = "PI_CODING_AGENT_SESSION_DIR"
)

const (
	// IsBunBinary reports whether the current process is a compiled Bun binary.
	IsBunBinary = false
	// IsBunRuntime reports whether the process runs on Bun.
	IsBunRuntime = false
	// IsBundledNode reports whether the process is a bundled Node build.
	IsBundledNode = false
)

// InstallMethod identifies how pi was installed.
type InstallMethod string

const (
	InstallMethodBunBinary InstallMethod = "bun-binary"
	InstallMethodNpm       InstallMethod = "npm"
	InstallMethodPnpm      InstallMethod = "pnpm"
	InstallMethodYarn      InstallMethod = "yarn"
	InstallMethodBun       InstallMethod = "bun"
	InstallMethodUnknown   InstallMethod = "unknown"
)

// SelfUpdateCommandStep represents one command invocation in a self-update sequence.
type SelfUpdateCommandStep struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Display string   `json:"display"`
}

// SelfUpdateCommand describes a self-update command and optional multi-step sequence.
type SelfUpdateCommand struct {
	Command string                  `json:"command"`
	Args    []string                `json:"args"`
	Display string                  `json:"display"`
	Steps   []SelfUpdateCommandStep `json:"steps,omitzero"`
}

// SelfUpdatePackageTarget identifies the target package (and optional version/spec) for self-update.
type SelfUpdatePackageTarget struct {
	PackageName string
	InstallSpec string
	Version     string
}

// InstallChange describes an update or removal detected at startupPackageJsonPath.
type InstallChange struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitzero"`
}

//go:embed all:assets
var configAssetsEmbed embed.FS

// ConfigAssetsFS returns the embedded assets filesystem rooted at assets/.
func ConfigAssetsFS() fs.FS {
	sub, err := fs.Sub(configAssetsEmbed, "assets")
	if err != nil {
		return configAssetsEmbed
	}
	return sub
}

// ConfigExitHook is invoked by ConfigExit after exit listeners run. Tests may replace it.
var ConfigExitHook = os.Exit

var (
	configExitMu        sync.Mutex
	configExitListeners []func()

	configExtractMu    sync.Mutex
	configExtractedDir = make(map[string]bool)

	configQuickJSMu   sync.RWMutex
	configQuickJSPath string

	configExecPath = os.Executable
	configArgv     = func() []string { return os.Args }
	configPlatform = js.Platform
)

// ConfigOnExit registers a cleanup callback executed by ConfigExit before terminating.
func ConfigOnExit(fn func()) {
	if fn == nil {
		return
	}
	configExitMu.Lock()
	configExitListeners = append(configExitListeners, fn)
	configExitMu.Unlock()
}

// ConfigExit runs registered exit listeners and calls ConfigExitHook(code).
func ConfigExit(code int) {
	configExitMu.Lock()
	listeners := append([]func(){}, configExitListeners...)
	configExitListeners = nil
	configExitMu.Unlock()

	for i := len(listeners) - 1; i >= 0; i-- {
		listeners[i]()
	}
	if ConfigExitHook != nil {
		ConfigExitHook(code)
	}
}

func configExtractRoot() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "pi-go", VERSION)
}

func configEnsureAssetsExtracted() string {
	root := configExtractRoot()
	configExtractMu.Lock()
	defer configExtractMu.Unlock()

	marker := filepath.Join(root, ".extracted")
	if configExtractedDir[root] {
		if _, err := os.Stat(marker); err == nil {
			return root
		}
	}

	if _, err := os.Stat(marker); err == nil {
		configExtractedDir[root] = true
		return root
	}

	rootFS := ConfigAssetsFS()
	_ = fs.WalkDir(rootFS, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || rel == "." {
			return nil
		}
		var destRel string
		switch {
		case rel == "package":
			return os.MkdirAll(root, 0o755)
		case strings.HasPrefix(rel, "package/"):
			destRel = strings.TrimPrefix(rel, "package/")
		case rel == "interactive":
			destRel = "assets"
		case strings.HasPrefix(rel, "interactive/"):
			destRel = "assets/" + strings.TrimPrefix(rel, "interactive/")
		default:
			destRel = rel
		}

		destPath := filepath.Join(root, filepath.FromSlash(destRel))
		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		data, err := fs.ReadFile(rootFS, rel)
		if err != nil {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(destPath), 0o755)
		_ = os.WriteFile(destPath, data, 0o644)
		return nil
	})
	_ = os.MkdirAll(root, 0o755)
	_ = os.WriteFile(marker, []byte(VERSION), 0o644)
	configExtractedDir[root] = true
	return root
}

func configStartupArgv1() string {
	argv := configArgv()
	if len(argv) > 1 {
		return argv[1]
	}
	return ""
}

func configStartupExecPath() string {
	p, err := configExecPath()
	if err != nil {
		return ""
	}
	return p
}

// FindNodePackageDir locates the package root containing package.json starting from startDir,
// skipping binary metadata copied into dist/.
func FindNodePackageDir(startDir string) string {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			parent := filepath.Dir(dir)
			parentPkg := filepath.Join(parent, "package.json")
			_, parentErr := os.Stat(parentPkg)
			if filepath.Base(dir) == "dist" && parentErr == nil {
				// Skip dist/package.json emitted beside bundled artifacts.
			} else {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return startDir
}

func configSlashLower(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, `\`, `/`))
}

func configSplitPathComponents(p string) []string {
	norm := strings.ReplaceAll(p, `\`, `/`)
	return strings.Split(norm, "/")
}

func configDetectInstallMethodFromPaths(candidates []string) InstallMethod {
	if IsBunBinary {
		return InstallMethodBunBinary
	}

	var normalized []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		s := configSlashLower(c)
		normalized = append(normalized, s)
		if !strings.Contains(c, `\`) {
			if real, err := filepath.EvalSymlinks(c); err == nil {
				rs := configSlashLower(real)
				if rs != s {
					normalized = append(normalized, rs)
				}
			}
		}
	}

	for _, p := range normalized {
		if strings.Contains(p, "/.pnpm/") ||
			strings.Contains(p, "/pnpm/global/") ||
			strings.Contains(p, "/pnpm/store/") ||
			strings.Contains(p, "/pnpm-") ||
			strings.Contains(p, "/pnpm/") {
			return InstallMethodPnpm
		}
	}
	for _, p := range normalized {
		if strings.Contains(p, "/.yarn/") || strings.Contains(p, "/yarn/global/") {
			return InstallMethodYarn
		}
	}
	for _, p := range normalized {
		if strings.Contains(p, "/.bun/") || strings.Contains(p, "/bun/install/global/") {
			return InstallMethodBun
		}
	}
	for _, p := range normalized {
		if strings.Contains(p, "/node_modules/") {
			return InstallMethodNpm
		}
	}
	return InstallMethodUnknown
}

func configCandidatePaths() []string {
	var candidates []string
	if envPkg := os.Getenv("PI_PACKAGE_DIR"); envPkg != "" {
		candidates = append(candidates, envPkg)
	}
	if a1 := configStartupArgv1(); a1 != "" {
		candidates = append(candidates, a1)
	}
	if ep := configStartupExecPath(); ep != "" {
		candidates = append(candidates, ep)
	}
	return candidates
}

// DetectInstallMethod detects how pi was installed (pnpm, yarn, bun, npm, bun-binary, or unknown).
func DetectInstallMethod() InstallMethod {
	return configDetectInstallMethodFromPaths(configCandidatePaths())
}

func configQuoteArg(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t\r\n") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func configFormatDisplay(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, configQuoteArg(command))
	for _, a := range args {
		parts = append(parts, configQuoteArg(a))
	}
	return strings.Join(parts, " ")
}

func configResolvePackageDirForSelfUpdate() string {
	if envPkg := os.Getenv("PI_PACKAGE_DIR"); envPkg != "" {
		return envPkg
	}
	if a1 := configStartupArgv1(); a1 != "" {
		return FindNodePackageDir(filepath.Dir(a1))
	}
	if ep := configStartupExecPath(); ep != "" {
		return FindNodePackageDir(filepath.Dir(ep))
	}
	return ""
}

func configFindNpmGlobalPrefix(pkgDir string) string {
	if pkgDir == "" || configPlatform() == "win32" || strings.Contains(pkgDir, `\`) {
		return ""
	}
	// Check <prefix>/lib/node_modules/<pkg>
	parts := strings.Split(filepath.Clean(pkgDir), string(filepath.Separator))
	for i := len(parts) - 1; i >= 1; i-- {
		if parts[i] == "node_modules" && parts[i-1] == "lib" {
			prefix := strings.Join(parts[:i-1], string(filepath.Separator))
			if prefix == "" {
				return "/"
			}
			return prefix
		}
	}
	return ""
}

func configIsDirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	probe, err := os.CreateTemp(dir, ".pi-write-check-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

func configResolveInstallPathsForManager() []string {
	var out []string
	if a1 := configStartupArgv1(); a1 != "" {
		out = append(out, a1)
	}
	if pkg := os.Getenv("PI_PACKAGE_DIR"); pkg != "" {
		out = append(out, pkg)
	}
	if ep := configStartupExecPath(); ep != "" {
		out = append(out, ep)
	}
	return out
}

func configPathWithinRoot(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}
	rClean := strings.TrimRight(configSlashLower(root), "/")
	cClean := configSlashLower(candidate)
	return cClean == rClean || strings.HasPrefix(cClean, rClean+"/")
}

func configVerifyManagedPackageDir(method InstallMethod) (writable bool, managed bool) {
	paths := configResolveInstallPathsForManager()
	pkgDir := configResolvePackageDirForSelfUpdate()

	switch method {
	case InstallMethodNpm:
		if pkgDir == "" {
			return false, true
		}
		if configPlatform() == "win32" || strings.Contains(pkgDir, `\`) {
			return true, true
		}
		if _, err := os.Stat(pkgDir); err != nil {
			return true, true
		}
		return configIsDirWritable(pkgDir) && configIsDirWritable(filepath.Dir(pkgDir)), true

	case InstallMethodPnpm:
		res := SpawnProcessSync("pnpm", []string{"root", "-g"}, nil)
		if res.Error != nil || res.Status != 0 {
			return false, false
		}
		globalRoot := strings.TrimSpace(res.Stdout)
		globalParent := filepath.Dir(globalRoot)
		if !slices.ContainsFunc(paths, func(p string) bool {
			return configPathWithinRoot(globalRoot, p) || configPathWithinRoot(globalParent, p)
		}) {
			return false, false
		}
		return pkgDir == "" || configIsDirWritable(pkgDir), true

	case InstallMethodYarn:
		res := SpawnProcessSync("yarn", []string{"global", "dir"}, nil)
		if res.Error != nil || res.Status != 0 {
			return false, false
		}
		globalDir := strings.TrimSpace(res.Stdout)
		if !slices.ContainsFunc(paths, func(p string) bool {
			return configPathWithinRoot(globalDir, p)
		}) {
			return false, false
		}
		return pkgDir == "" || configIsDirWritable(pkgDir), true

	case InstallMethodBun:
		res := SpawnProcessSync("bun", []string{"pm", "bin", "-g"}, nil)
		if res.Error != nil || res.Status != 0 {
			return false, false
		}
		bunBin := strings.TrimSpace(res.Stdout)
		bunInstallRoot := filepath.Dir(bunBin)
		if !slices.ContainsFunc(paths, func(p string) bool {
			return configPathWithinRoot(bunInstallRoot, p)
		}) {
			return false, false
		}
		return pkgDir == "" || configIsDirWritable(pkgDir), true
	}

	return false, false
}

func configResolveTarget(packageName string, targets ...SelfUpdatePackageTarget) (targetPkg string, installSpec string, version string) {
	targetPkg = packageName
	installSpec = packageName
	version = VERSION
	if len(targets) > 0 {
		t := targets[0]
		if t.PackageName != "" {
			targetPkg = t.PackageName
		}
		if t.Version != "" {
			version = t.Version
		}
		switch {
		case t.InstallSpec != "":
			installSpec = t.InstallSpec
		case t.PackageName != "" && t.Version != "":
			installSpec = t.PackageName + "@" + t.Version
		case t.PackageName != "":
			installSpec = t.PackageName
		}
	}
	return targetPkg, installSpec, version
}

// GetConfiguredSelfUpdateArgv resolves a configured self-update argv from PI_SELF_UPDATE_COMMAND or setting.
func GetConfiguredSelfUpdateArgv(setting []string) []string {
	if envCmd := strings.TrimSpace(os.Getenv("PI_SELF_UPDATE_COMMAND")); envCmd != "" {
		fields := strings.Fields(envCmd)
		if len(fields) > 0 {
			return fields
		}
	}
	if len(setting) > 0 {
		out := make([]string, len(setting))
		copy(out, setting)
		return out
	}
	return nil
}

// MakeConfiguredSelfUpdateCommand substitutes {packageName}, {installSpec}, and {version} into argv.
func MakeConfiguredSelfUpdateCommand(argv []string, target SelfUpdatePackageTarget) *SelfUpdateCommand {
	if len(argv) == 0 {
		return nil
	}
	targetPkg, installSpec, version := configResolveTarget(target.PackageName, target)
	replacer := strings.NewReplacer(
		"{packageName}", targetPkg,
		"{installSpec}", installSpec,
		"{version}", version,
	)
	cmd := replacer.Replace(argv[0])
	args := make([]string, 0, len(argv)-1)
	for _, a := range argv[1:] {
		args = append(args, replacer.Replace(a))
	}
	return &SelfUpdateCommand{
		Command: cmd,
		Args:    args,
		Display: configFormatDisplay(cmd, args),
	}
}

func configMakeStep(command string, args []string) SelfUpdateCommandStep {
	return SelfUpdateCommandStep{
		Command: command,
		Args:    args,
		Display: configFormatDisplay(command, args),
	}
}

func configSelfUpdateCommandForMethod(
	method InstallMethod,
	packageName string,
	npmCommand []string,
	target ...SelfUpdatePackageTarget,
) *SelfUpdateCommand {
	if method == InstallMethodUnknown || method == InstallMethodBunBinary {
		return nil
	}

	targetPkg, installSpec, _ := configResolveTarget(packageName, target...)
	renamed := targetPkg != packageName
	var installStep SelfUpdateCommandStep
	var uninstallStep *SelfUpdateCommandStep

	switch method {
	case InstallMethodNpm:
		npmExe := "npm"
		var prefixArgs []string
		if len(npmCommand) > 0 {
			npmExe = npmCommand[0]
			prefixArgs = append([]string(nil), npmCommand[1:]...)
		} else if prefix := configFindNpmGlobalPrefix(configResolvePackageDirForSelfUpdate()); prefix != "" {
			prefixArgs = []string{"--prefix", prefix}
		}
		installArgs := append(append([]string(nil), prefixArgs...), "install", "-g", "--ignore-scripts", "--min-release-age=0", installSpec)
		installStep = configMakeStep(npmExe, installArgs)
		if renamed {
			uArgs := append(append([]string(nil), prefixArgs...), "uninstall", "-g", packageName)
			s := configMakeStep(npmExe, uArgs)
			uninstallStep = &s
		}

	case InstallMethodPnpm:
		installStep = configMakeStep("pnpm", []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", installSpec})
		if renamed {
			s := configMakeStep("pnpm", []string{"remove", "-g", packageName})
			uninstallStep = &s
		}

	case InstallMethodYarn:
		installStep = configMakeStep("yarn", []string{"global", "add", "--ignore-scripts", installSpec})
		if renamed {
			s := configMakeStep("yarn", []string{"global", "remove", packageName})
			uninstallStep = &s
		}

	case InstallMethodBun:
		installStep = configMakeStep("bun", []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", installSpec})
		if renamed {
			s := configMakeStep("bun", []string{"uninstall", "-g", packageName})
			uninstallStep = &s
		}
	}

	if uninstallStep != nil {
		steps := []SelfUpdateCommandStep{*uninstallStep, installStep}
		return &SelfUpdateCommand{
			Command: installStep.Command,
			Args:    installStep.Args,
			Display: uninstallStep.Display + " && " + installStep.Display,
			Steps:   steps,
		}
	}
	return &SelfUpdateCommand{
		Command: installStep.Command,
		Args:    installStep.Args,
		Display: installStep.Display,
	}
}

// GetSelfUpdateCommand returns the command to self-update pi for the active installation method,
// or nil when self-update cannot run safely. npmCommand optionally specifies the npm invocation
// (for example, `["npm", "--prefix", "/custom/prefix"]`); an empty slice is treated as unset.
func GetSelfUpdateCommand(
	packageName string,
	npmCommand []string,
	target ...SelfUpdatePackageTarget,
) *SelfUpdateCommand {
	method := DetectInstallMethod()
	if method == InstallMethodUnknown || method == InstallMethodBunBinary {
		return nil
	}

	writable, managed := configVerifyManagedPackageDir(method)
	if !managed || !writable {
		return nil
	}

	return configSelfUpdateCommandForMethod(method, packageName, npmCommand, target...)
}

// GetSelfUpdateUnavailableInstruction returns guidance when automatic self-update is unavailable.
func GetSelfUpdateUnavailableInstruction(
	packageName string,
	npmCommand []string,
	target ...SelfUpdatePackageTarget,
) string {
	method := DetectInstallMethod()
	if method == InstallMethodBunBinary {
		return "Download from: https://github.com/earendil-works/pi/releases/latest"
	}
	cmd := configSelfUpdateCommandForMethod(method, packageName, npmCommand, target...)
	if cmd != nil {
		writable, managed := configVerifyManagedPackageDir(method)
		if managed && !writable {
			return fmt.Sprintf(
				"This installation is managed by a global %s install, but the install path is not writable. Update it yourself with: %s",
				method,
				cmd.Display,
			)
		}
	}
	_, installSpec, _ := configResolveTarget(packageName, target...)
	return "Update " + installSpec + " using the package manager, wrapper, or source checkout that provides this installation."
}

// GetUpdateInstruction returns the update instruction string to show to users.
func GetUpdateInstruction(packageName string) string {
	method := DetectInstallMethod()
	if cmd := configSelfUpdateCommandForMethod(method, packageName, nil); cmd != nil {
		return "Run: " + cmd.Display
	}
	return GetSelfUpdateUnavailableInstruction(packageName, nil)
}

// GetPackageDir returns the coding-agent package root directory, respecting PI_PACKAGE_DIR
// and otherwise extracting embedded assets into os.UserCacheDir()/pi-go/<VERSION>/.
func GetPackageDir() string {
	if envDir := os.Getenv("PI_PACKAGE_DIR"); envDir != "" {
		if strings.HasPrefix(envDir, "~") {
			return ExpandTildePath(envDir)
		}
		return envDir
	}
	return configEnsureAssetsExtracted()
}

func configResolvePackageSubdir(binaryRel string, nodeSubpaths ...string) string {
	pkgDir := GetPackageDir()
	binCandidate := filepath.Join(pkgDir, binaryRel)
	if _, err := os.Stat(binCandidate); err == nil {
		return binCandidate
	}
	for _, sub := range nodeSubpaths {
		candidate := filepath.Join(pkgDir, sub)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return binCandidate
}

// GetThemesDir returns the directory containing built-in theme JSON files.
func GetThemesDir() string {
	return configResolvePackageSubdir(
		"theme",
		filepath.Join("dist", "modes", "interactive", "theme"),
		filepath.Join("src", "modes", "interactive", "theme"),
	)
}

// GetExportTemplateDir returns the directory containing export-html templates and vendor scripts.
func GetExportTemplateDir() string {
	return configResolvePackageSubdir(
		"export-html",
		filepath.Join("dist", "core", "export-html"),
		filepath.Join("src", "core", "export-html"),
	)
}

// GetPackageJsonPath returns the path to the package's package.json.
func GetPackageJsonPath() string {
	return filepath.Join(GetPackageDir(), "package.json")
}

// GetReadmePath returns the path to README.md.
func GetReadmePath() string {
	return filepath.Join(GetPackageDir(), "README.md")
}

// GetDocsPath returns the path to docs/.
func GetDocsPath() string {
	return filepath.Join(GetPackageDir(), "docs")
}

// GetExamplesPath returns the path to examples/.
func GetExamplesPath() string {
	return filepath.Join(GetPackageDir(), "examples")
}

// GetChangelogPath returns the path to CHANGELOG.md.
func GetChangelogPath() string {
	return filepath.Join(GetPackageDir(), "CHANGELOG.md")
}

// GetInteractiveAssetsDir returns the directory containing interactive image assets.
func GetInteractiveAssetsDir() string {
	return configResolvePackageSubdir(
		"assets",
		filepath.Join("dist", "modes", "interactive", "assets"),
		filepath.Join("src", "modes", "interactive", "assets"),
	)
}

// GetBundledInteractiveAssetPath returns the path to a bundled interactive asset by filename.
func GetBundledInteractiveAssetPath(name string) string {
	return filepath.Join(GetInteractiveAssetsDir(), name)
}

// SetEmbeddedQuickJSWasmPath records the path to an embedded QuickJS Wasm module.
func SetEmbeddedQuickJSWasmPath(p string) {
	configQuickJSMu.Lock()
	configQuickJSPath = p
	configQuickJSMu.Unlock()
}

// GetQuickJSWasmPath returns the configured QuickJS Wasm path, if set.
func GetQuickJSWasmPath() string {
	configQuickJSMu.RLock()
	p := configQuickJSPath
	configQuickJSMu.RUnlock()
	return p
}

// ResolveCodemodeWorkerSpecifier resolves the codemode worker entrypoint for the given runtime mode.
func ResolveCodemodeWorkerSpecifier(mode string, moduleURL string) *string {
	switch mode {
	case "bun-binary":
		s := "./src/extensions/codemode/worker.ts"
		return &s
	case "bundled-node":
		u, err := url.Parse(moduleURL)
		if err != nil {
			s := "codemode-worker.js"
			return &s
		}
		ref, err := url.Parse("./codemode-worker.js")
		if err != nil {
			s := "codemode-worker.js"
			return &s
		}
		resolved := u.ResolveReference(ref).String()
		return &resolved
	default:
		return nil
	}
}

// GetCodemodeWorkerSpecifier returns the codemode worker specifier for the current runtime.
func GetCodemodeWorkerSpecifier() *string {
	if IsBunBinary {
		return ResolveCodemodeWorkerSpecifier("bun-binary", "")
	}
	return nil
}

// DetectInstallChange reads packageJsonPath to detect whether the installation was updated or removed.
func DetectInstallChange(packageJsonPath ...string) *InstallChange {
	target := GetPackageJsonPath()
	if len(packageJsonPath) > 0 && packageJsonPath[0] != "" {
		target = packageJsonPath[0]
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &InstallChange{Kind: "removed"}
		}
		return nil
	}
	clean := StripBom(string(data))
	parsed, err := jsonx.Parse([]byte(clean))
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	version, ok := obj.GetString("version")
	if !ok || version == "" {
		return nil
	}
	if version != VERSION {
		return &InstallChange{Kind: "updated", Version: version}
	}
	return nil
}

// ExpandTildePath expands a leading "~" or "~/" to the user's home directory.
func ExpandTildePath(p string) string {
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, filepath.FromSlash(p[2:]))
	}
	return p
}

// GetShareViewerUrl returns the pi.dev session viewer URL for a gist ID.
func GetShareViewerUrl(gistId string) string {
	base := os.Getenv("PI_SHARE_VIEWER_URL")
	if base == "" {
		base = "https://pi.dev/session/"
	}
	return base + "#" + gistId
}

// GetAgentDir returns the coding-agent configuration directory.
func GetAgentDir() string {
	if envDir := os.Getenv(EnvAgentDir); envDir != "" {
		return ExpandTildePath(envDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ConfigDirName, "agent")
}

// GetCustomThemesDir returns the user's custom themes directory.
func GetCustomThemesDir() string {
	return filepath.Join(GetAgentDir(), "themes")
}

// GetModelsPath returns the path to the user's models.json.
func GetModelsPath() string {
	return filepath.Join(GetAgentDir(), "models.json")
}

// GetAuthPath returns the path to the user's auth.json.
func GetAuthPath() string {
	return filepath.Join(GetAgentDir(), "auth.json")
}

// GetSettingsPath returns the path to the user's settings.json.
func GetSettingsPath() string {
	return filepath.Join(GetAgentDir(), "settings.json")
}

// GetToolsDir returns the directory containing custom tools.
func GetToolsDir() string {
	return filepath.Join(GetAgentDir(), "tools")
}

// GetBinDir returns the directory for managed tool binaries (fd, rg, etc.).
func GetBinDir() string {
	return filepath.Join(GetAgentDir(), "bin")
}

// GetPromptsDir returns the directory for prompt templates.
func GetPromptsDir() string {
	return filepath.Join(GetAgentDir(), "prompts")
}

// GetSessionsDir returns the directory for session JSONL files.
func GetSessionsDir() string {
	if envDir := os.Getenv(EnvSessionDir); envDir != "" {
		return ExpandTildePath(envDir)
	}
	return filepath.Join(GetAgentDir(), "sessions")
}

// GetDebugLogPath returns the debug log file path.
func GetDebugLogPath() string {
	return filepath.Join(GetAgentDir(), AppName+"-debug.log")
}
