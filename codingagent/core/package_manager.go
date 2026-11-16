// Ported from packages/coding-agent/src/core/package-manager.ts (pi v1.5.2).

package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/hostedgit"
	"github.com/keejkrej/pi-go/internal/ignore"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/minimatch"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/semver"
	"github.com/keejkrej/pi-go/internal/xspawn"
	"golang.org/x/sync/errgroup"
)

// ErrPackagesUnsupported is kept for backward compatibility with legacy callers.
var ErrPackagesUnsupported = errors.New("pi-go barebone build does not support external packages or plugins")

// PathMetadata records source provenance for a discovered resource path.
type PathMetadata = utils.PathMetadata

// ResolvedResource describes a discovered resource path paired with enablement and provenance.
type ResolvedResource struct {
	Path     string       `json:"path"`
	Enabled  bool         `json:"enabled"`
	Metadata PathMetadata `json:"metadata"`
}

// ResolvedPaths groups resolved resource lists by category and records non-fatal install failures.
type ResolvedPaths struct {
	Extensions      []ResolvedResource      `json:"extensions"`
	Skills          []ResolvedResource      `json:"skills"`
	Prompts         []ResolvedResource      `json:"prompts"`
	Themes          []ResolvedResource      `json:"themes"`
	InstallFailures []PackageInstallFailure `json:"installFailures,omitzero"`
}

// PackageInstallFailure describes a failed package installation attempt.
type PackageInstallFailure struct {
	Source  string            `json:"source"`
	Scope   utils.SourceScope `json:"scope,omitzero"`
	Message string            `json:"message,omitzero"`
	Error   string            `json:"error,omitzero"`
}

// MissingSourceAction determines how PackageManager reacts to missing remote sources.
type MissingSourceAction string

const (
	MissingSourceSkip    MissingSourceAction = "skip"
	MissingSourceInstall MissingSourceAction = "install"
	MissingSourceAbort   MissingSourceAction = "abort"
)

// ProgressEvent describes progress updates emitted by package operations.
type ProgressEvent struct {
	Type    string `json:"type"`   // "start" | "progress" | "complete" | "result" | "warning" | "error"
	Action  string `json:"action"` // "install" | "remove" | "update" | "clone" | "pull"
	Source  string `json:"source"`
	Message string `json:"message,omitzero"`
}

// ProgressCallback receives package manager progress events.
type ProgressCallback func(event ProgressEvent)

// PackageUpdate describes an available package update.
type PackageUpdate struct {
	Source         string            `json:"source"`
	Scope          utils.SourceScope `json:"scope,omitzero"`
	Type           string            `json:"type"` // "npm" | "git"
	DisplayName    string            `json:"displayName,omitzero"`
	CurrentVersion string            `json:"currentVersion,omitzero"`
	LatestVersion  string            `json:"latestVersion,omitzero"`
}

// ConfiguredPackage represents a configured package entry and its resolved state.
type ConfiguredPackage struct {
	Source        string            `json:"source"`
	Scope         utils.SourceScope `json:"scope"`
	InstalledPath string            `json:"installedPath,omitzero"`
	Filtered      bool              `json:"filtered,omitzero"`
}

// PackageInstallOptions configures package installation.
type PackageInstallOptions struct {
	Local bool
}

// PackageRemoveOptions configures package removal.
type PackageRemoveOptions struct {
	Local bool
}

// ResolveExtensionSourcesOptions configures ResolveExtensionSources.
type ResolveExtensionSourcesOptions struct {
	Local             bool
	Temporary         bool
	HonorLocalFilters *bool
	OnMissing         func(source string) (MissingSourceAction, error)
}

// PackageManager defines the resource package management contract.
type PackageManager interface {
	Resolve(ctx context.Context, onMissing ...func(source string) (MissingSourceAction, error)) (ResolvedPaths, error)
	ResolveExtensionSources(ctx context.Context, sources []string, options ...*ResolveExtensionSourcesOptions) (ResolvedPaths, error)
	Install(ctx context.Context, source string, options ...*PackageInstallOptions) error
	Remove(ctx context.Context, source string, options ...*PackageRemoveOptions) error
	Update(ctx context.Context, source ...string) error
	GetInstalledPackages() []ConfiguredPackage
	GetInstalledPath(source string, scope ...string) *string
	SetProgressCallback(cb ProgressCallback)
	AddSourceToSettings(source string, local bool) bool
	RemoveSourceFromSettings(source string, local bool) bool
}

// DefaultPackageManagerOptions configures NewDefaultPackageManager.
type DefaultPackageManagerOptions struct {
	Cwd               string
	AgentDir          string
	SettingsManager   *SettingsManager
	BuiltinExtensions []string
	HTTPClient        *http.Client
}

// DefaultPackageManager resolves local, npm, git, built-in, and auto-discovered resources.
type DefaultPackageManager struct {
	cwd               string
	agentDir          string
	settingsManager   *SettingsManager
	builtinExtensions []string
	httpClient        *http.Client

	mu         sync.Mutex
	progressCb ProgressCallback
}

// NewDefaultPackageManager constructs a DefaultPackageManager using options.
func NewDefaultPackageManager(options DefaultPackageManagerOptions) *DefaultPackageManager {
	cwd := options.Cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	agentDir := options.AgentDir
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	sm := options.SettingsManager
	if sm == nil {
		sm = SettingsManagerCreate(cwd, agentDir)
	}
	client := options.HTTPClient
	if client == nil {
		client = ai.NodeHttpProxyDefaultClient()
	}
	return &DefaultPackageManager{
		cwd:               cwd,
		agentDir:          agentDir,
		settingsManager:   sm,
		builtinExtensions: slices.Clone(options.BuiltinExtensions),
		httpClient:        client,
	}
}

// IsOfflineModeEnabled reports whether offline mode is configured via PI_OFFLINE.
func IsOfflineModeEnabled() bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv("PI_OFFLINE")))
	return val == "1" || val == "true" || val == "yes"
}

// GetExtensionTempFolder returns the base directory for temporary extension downloads.
func GetExtensionTempFolder(agentDir string) string {
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	return filepath.Join(agentDir, "tmp", "extensions")
}

// SetProgressCallback sets the callback invoked for package operation progress events.
func (pm *DefaultPackageManager) SetProgressCallback(cb ProgressCallback) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.progressCb = cb
}

func (pm *DefaultPackageManager) emitProgress(event ProgressEvent) {
	pm.mu.Lock()
	cb := pm.progressCb
	pm.mu.Unlock()
	if cb != nil {
		cb(event)
	}
}

func (pm *DefaultPackageManager) withProgress(action, source, message string, op func() error) error {
	pm.emitProgress(ProgressEvent{
		Type:    "start",
		Action:  action,
		Source:  source,
		Message: message,
	})
	err := op()
	if err != nil {
		pm.emitProgress(ProgressEvent{
			Type:    "error",
			Action:  action,
			Source:  source,
			Message: err.Error(),
		})
		return err
	}
	pm.emitProgress(ProgressEvent{
		Type:   "complete",
		Action: action,
		Source: source,
	})
	return nil
}

const (
	pmDefaultNpmRegistry     = "https://registry.npmjs.org"
	pmNetworkTimeout         = 30 * time.Second
	pmUpdateCheckConcurrency = 4
	pmGitUpdateConcurrency   = 4
	pmMaxTarballBytes        = 512 * 1024 * 1024
)

type pmResourceType string

const (
	pmResExtensions pmResourceType = "extensions"
	pmResSkills     pmResourceType = "skills"
	pmResPrompts    pmResourceType = "prompts"
	pmResThemes     pmResourceType = "themes"
)

var pmResourceTypes = []pmResourceType{
	pmResExtensions,
	pmResSkills,
	pmResPrompts,
	pmResThemes,
}

var pmIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

var (
	pmNpmSpecRe      = regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@(.+))?$`)
	pmCommitHashRe   = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	pmExtFileRe      = regexp.MustCompile(`\.(ts|js)$`)
	pmValidNpmNameRe = regexp.MustCompile(`^(?:@[a-zA-Z0-9._-]+/)?[a-zA-Z0-9._-]+$`)
)

type pmParsedSourceKind string

const (
	pmSourceNpm   pmParsedSourceKind = "npm"
	pmSourceGit   pmParsedSourceKind = "git"
	pmSourceLocal pmParsedSourceKind = "local"
)

type pmParsedSource struct {
	kind pmParsedSourceKind
	// npm
	spec   string
	name   string
	ver    string
	npmRng string
	// git
	repo string
	host string
	path string // also holds local path when kind == pmSourceLocal
	ref  string
	// shared
	pinned bool
}

type pmPackageEntry struct {
	pkg   PackageSource
	scope utils.SourceScope
}

type pmResourceEntry struct {
	metadata PathMetadata
	enabled  bool
}

type pmResourceMap struct {
	order []string
	items map[string]*pmResourceEntry
}

func pmNewResourceMap() *pmResourceMap {
	return &pmResourceMap{
		items: make(map[string]*pmResourceEntry),
	}
}

func (m *pmResourceMap) add(p string, metadata PathMetadata, enabled bool) {
	if p == "" {
		return
	}
	if _, exists := m.items[p]; exists {
		return
	}
	m.order = append(m.order, p)
	m.items[p] = &pmResourceEntry{
		metadata: metadata,
		enabled:  enabled,
	}
}

type pmResourceAccumulator struct {
	extensions *pmResourceMap
	skills     *pmResourceMap
	prompts    *pmResourceMap
	themes     *pmResourceMap
}

func pmNewAccumulator() *pmResourceAccumulator {
	return &pmResourceAccumulator{
		extensions: pmNewResourceMap(),
		skills:     pmNewResourceMap(),
		prompts:    pmNewResourceMap(),
		themes:     pmNewResourceMap(),
	}
}

func (a *pmResourceAccumulator) target(rt pmResourceType) *pmResourceMap {
	switch rt {
	case pmResExtensions:
		return a.extensions
	case pmResSkills:
		return a.skills
	case pmResPrompts:
		return a.prompts
	case pmResThemes:
		return a.themes
	default:
		return a.extensions
	}
}

func pmResourcePrecedenceRank(m PathMetadata) int {
	if m.Source == "builtin" {
		return 5
	}
	if m.Origin == utils.SourceOriginPackage {
		return 4
	}
	scopeBase := 2
	if m.Scope == utils.SourceScopeProject {
		scopeBase = 0
	}
	if m.Source == "local" {
		return scopeBase
	}
	return scopeBase + 1
}

func (a *pmResourceAccumulator) toResolvedPaths(failures []PackageInstallFailure) ResolvedPaths {
	convert := func(m *pmResourceMap) []ResolvedResource {
		if m == nil || len(m.order) == 0 {
			return []ResolvedResource{}
		}
		list := make([]ResolvedResource, 0, len(m.order))
		for _, p := range m.order {
			entry := m.items[p]
			list = append(list, ResolvedResource{
				Path:     p,
				Enabled:  entry.enabled,
				Metadata: entry.metadata,
			})
		}
		slices.SortStableFunc(list, func(x, y ResolvedResource) int {
			return pmResourcePrecedenceRank(x.Metadata) - pmResourcePrecedenceRank(y.Metadata)
		})
		seen := make(map[string]struct{}, len(list))
		deduped := make([]ResolvedResource, 0, len(list))
		for _, r := range list {
			key := utils.GetResourcePathKey(r.Path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			deduped = append(deduped, r)
		}
		return deduped
	}
	return ResolvedPaths{
		Extensions:      convert(a.extensions),
		Skills:          convert(a.skills),
		Prompts:         convert(a.prompts),
		Themes:          convert(a.themes),
		InstallFailures: failures,
	}
}

// Parse / Identity / Path Helpers

func pmParseNpmSpec(spec string) (name, version string) {
	m := pmNpmSpecRe.FindStringSubmatch(spec)
	if m == nil {
		return spec, ""
	}
	name = m[1]
	if name == "" {
		name = spec
	}
	return name, m[2]
}

func pmGetNpmVersionRange(version string) string {
	if version == "" {
		return ""
	}
	rng, ok := semver.ValidRange(version)
	if !ok {
		return ""
	}
	return rng
}

func pmIsExactNpmVersion(version string) bool {
	if version == "" {
		return false
	}
	_, rngOk := semver.ValidRange(version)
	_, verOk := semver.Valid(version)
	return rngOk && verOk
}

func pmParseHostedGitShortcut(source string) *pmParsedSource {
	for _, prefix := range []string{"github:", "gitlab:", "bitbucket:", "sourcehut:"} {
		if !strings.HasPrefix(source, prefix) {
			continue
		}
		input := source
		var explicitRef string
		if idx := strings.IndexByte(source[len(prefix):], '#'); idx == -1 {
			if at := strings.LastIndexByte(source[len(prefix):], '@'); at >= 0 {
				explicitRef = source[len(prefix)+at+1:]
				input = source[:len(prefix)+at]
			}
		}
		hg := hostedgit.FromUrl(input, nil)
		if hg == nil {
			continue
		}
		ref := explicitRef
		if ref == "" && hg.Committish != nil {
			ref = *hg.Committish
		}
		noCommittish := true
		noGitPlus := true
		opts := &hostedgit.Options{NoCommittish: &noCommittish, NoGitPlus: &noGitPlus}
		repo := strings.TrimSuffix(hg.Https(opts), ".git")
		repoPath := strings.TrimPrefix(hg.Path(opts), "/")
		return &pmParsedSource{
			kind:   pmSourceGit,
			repo:   repo,
			host:   hg.Domain,
			path:   repoPath,
			ref:    ref,
			pinned: ref != "",
		}
	}
	return nil
}

func pmParseSource(source string) pmParsedSource {
	trimmed := js.Trim(source)
	if strings.HasPrefix(trimmed, "npm:") {
		spec := js.Trim(strings.TrimPrefix(trimmed, "npm:"))
		name, ver := pmParseNpmSpec(spec)
		return pmParsedSource{
			kind:   pmSourceNpm,
			spec:   spec,
			name:   name,
			ver:    ver,
			npmRng: pmGetNpmVersionRange(ver),
			pinned: pmIsExactNpmVersion(ver),
		}
	}

	if hosted := pmParseHostedGitShortcut(trimmed); hosted != nil {
		return *hosted
	}

	if utils.IsLocalPath(trimmed) {
		return pmParsedSource{
			kind: pmSourceLocal,
			path: trimmed,
		}
	}

	if gitParsed := utils.ParseGitUrl(trimmed); gitParsed != nil {
		return pmParsedSource{
			kind:   pmSourceGit,
			repo:   gitParsed.Repo,
			host:   gitParsed.Host,
			path:   gitParsed.Path,
			ref:    gitParsed.Ref,
			pinned: gitParsed.Pinned,
		}
	}

	return pmParsedSource{
		kind: pmSourceLocal,
		path: trimmed,
	}
}

func pmPackageSourceString(pkg PackageSource) string {
	if pkg.String != nil {
		return *pkg.String
	}
	if pkg.Object != nil {
		return pkg.Object.Source
	}
	return ""
}

func pmPathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func pmIsPathInside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "" && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func pmIsSubpathOrEqual(root, child string) bool {
	cleanRoot := filepath.Clean(root)
	cleanChild := filepath.Clean(child)
	if cleanRoot == cleanChild {
		return true
	}
	rel, err := filepath.Rel(cleanRoot, cleanChild)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func pmResolveManagedPath(root string, parts ...string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(append([]string{absRoot}, parts...)...)
	absTarget, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if !pmIsSubpathOrEqual(absRoot, absTarget) {
		return "", fmt.Errorf("refusing to use path outside package install root: %s", absTarget)
	}
	return absTarget, nil
}

func (pm *DefaultPackageManager) assertProjectTrustedForScope(scope utils.SourceScope) error {
	if scope == utils.SourceScopeProject && !pm.settingsManager.IsProjectTrusted() {
		return errors.New("Project is not trusted; refusing to access project package storage")
	}
	return nil
}

func (pm *DefaultPackageManager) baseDirForScope(scope utils.SourceScope) (string, error) {
	if scope == utils.SourceScopeProject {
		if err := pm.assertProjectTrustedForScope(scope); err != nil {
			return "", err
		}
		return filepath.Join(pm.cwd, utils.ConfigDirName), nil
	}
	if scope == utils.SourceScopeUser {
		return pm.agentDir, nil
	}
	return pm.cwd, nil
}

func (pm *DefaultPackageManager) resolvePath(input string) string {
	home, _ := os.UserHomeDir()
	res, err := utils.ResolvePath(input, pm.cwd, &utils.PathInputOptions{
		HomeDir: &home,
		Trim:    true,
	})
	if err != nil {
		return input
	}
	return res
}

func (pm *DefaultPackageManager) resolvePathFromBase(input, baseDir string) string {
	home, _ := os.UserHomeDir()
	res, err := utils.ResolvePath(input, baseDir, &utils.PathInputOptions{
		HomeDir: &home,
		Trim:    true,
	})
	if err != nil {
		return input
	}
	return res
}

func (pm *DefaultPackageManager) packageIdentity(source string, scope ...utils.SourceScope) string {
	parsed := pmParseSource(source)
	switch parsed.kind {
	case pmSourceNpm:
		return "npm:" + parsed.name
	case pmSourceGit:
		return fmt.Sprintf("git:%s/%s", parsed.host, parsed.path)
	default:
		resolved := ""
		if len(scope) > 0 && scope[0] != "" {
			if baseDir, err := pm.baseDirForScope(scope[0]); err == nil {
				resolved = pm.resolvePathFromBase(parsed.path, baseDir)
			}
		}
		if resolved == "" {
			resolved = pm.resolvePath(parsed.path)
		}
		return "local:" + utils.GetPathComparisonKey(resolved)
	}
}

func (pm *DefaultPackageManager) sourceMatchKeyForInput(source string) string {
	return pm.packageIdentity(source)
}

func (pm *DefaultPackageManager) sourceMatchKeyForSettings(source string, scope utils.SourceScope) string {
	return pm.packageIdentity(source, scope)
}

func (pm *DefaultPackageManager) packageSourcesMatch(existing PackageSource, inputSource string, scope utils.SourceScope) bool {
	return pm.sourceMatchKeyForSettings(pmPackageSourceString(existing), scope) == pm.sourceMatchKeyForInput(inputSource)
}

func (pm *DefaultPackageManager) normalizePackageSourceForSettings(source string, scope utils.SourceScope) string {
	parsed := pmParseSource(source)
	if parsed.kind != pmSourceLocal {
		return source
	}
	baseDir, err := pm.baseDirForScope(scope)
	if err != nil {
		return source
	}
	resolved := pm.resolvePath(parsed.path)
	rel, err := filepath.Rel(baseDir, resolved)
	if err != nil {
		return resolved
	}
	if rel == "" {
		return "."
	}
	return rel
}

func (pm *DefaultPackageManager) dedupePackages(packages []pmPackageEntry) []pmPackageEntry {
	var result []pmPackageEntry
	seen := make(map[string]int, len(packages))
	for _, entry := range packages {
		identity := pm.packageIdentity(pmPackageSourceString(entry.pkg), entry.scope)
		idx, exists := seen[identity]
		if !exists {
			seen[identity] = len(result)
			result = append(result, entry)
			continue
		}
		existing := result[idx]
		if existing.scope == utils.SourceScopeProject && entry.scope == utils.SourceScopeUser {
			if existing.pkg.Object != nil && existing.pkg.Object.Autoload != nil && !*existing.pkg.Object.Autoload {
				result = append(result, entry)
			}
		} else if entry.scope == utils.SourceScopeProject {
			result[idx] = entry
		}
	}
	return result
}

func (pm *DefaultPackageManager) findAutoloadDeltaBase(pkg PackageSource, scope utils.SourceScope, sources []pmPackageEntry) (string, utils.SourceScope, bool) {
	if scope != utils.SourceScopeProject || pkg.Object == nil || pkg.Object.Autoload == nil || *pkg.Object.Autoload {
		return "", "", false
	}
	identity := pm.packageIdentity(pkg.Object.Source, scope)
	for _, entry := range sources {
		if entry.scope == utils.SourceScopeUser {
			src := pmPackageSourceString(entry.pkg)
			if pm.packageIdentity(src, utils.SourceScopeUser) == identity {
				return src, utils.SourceScopeUser, true
			}
		}
	}
	return "", "", false
}

// Settings persistence & path lookup

// AddSourceToSettings persists source into global or project packages settings if absent or changed.
func (pm *DefaultPackageManager) AddSourceToSettings(source string, local bool) bool {
	scope := utils.SourceScopeUser
	if local {
		scope = utils.SourceScopeProject
		if !pm.settingsManager.IsProjectTrusted() {
			return false
		}
	}
	var currentPackages []PackageSource
	if scope == utils.SourceScopeProject {
		currentPackages = pm.settingsManager.GetProjectSettings().Packages
	} else {
		currentPackages = pm.settingsManager.GetGlobalSettings().Packages
	}
	normalizedSource := pm.normalizePackageSourceForSettings(source, scope)
	matchIdx := -1
	for i, existing := range currentPackages {
		if pm.packageSourcesMatch(existing, source, scope) {
			matchIdx = i
			break
		}
	}
	if matchIdx != -1 {
		existing := currentPackages[matchIdx]
		if pmPackageSourceString(existing) == normalizedSource {
			return false
		}
		nextPackages := slices.Clone(currentPackages)
		if existing.Object != nil {
			objCopy := *existing.Object
			objCopy.Source = normalizedSource
			nextPackages[matchIdx] = PackageSourceFromObject(objCopy)
		} else {
			nextPackages[matchIdx] = PackageSourceFromString(normalizedSource)
		}
		if scope == utils.SourceScopeProject {
			return pm.settingsManager.SetProjectPackages(nextPackages) == nil
		}
		pm.settingsManager.SetPackages(nextPackages)
		return true
	}

	nextPackages := append(slices.Clone(currentPackages), PackageSourceFromString(normalizedSource))
	if scope == utils.SourceScopeProject {
		return pm.settingsManager.SetProjectPackages(nextPackages) == nil
	}
	pm.settingsManager.SetPackages(nextPackages)
	return true
}

// RemoveSourceFromSettings removes source from global or project packages settings.
func (pm *DefaultPackageManager) RemoveSourceFromSettings(source string, local bool) bool {
	scope := utils.SourceScopeUser
	if local {
		scope = utils.SourceScopeProject
		if !pm.settingsManager.IsProjectTrusted() {
			return false
		}
	}
	var currentPackages []PackageSource
	if scope == utils.SourceScopeProject {
		currentPackages = pm.settingsManager.GetProjectSettings().Packages
	} else {
		currentPackages = pm.settingsManager.GetGlobalSettings().Packages
	}
	nextPackages := make([]PackageSource, 0, len(currentPackages))
	for _, existing := range currentPackages {
		if !pm.packageSourcesMatch(existing, source, scope) {
			nextPackages = append(nextPackages, existing)
		}
	}
	if len(nextPackages) == len(currentPackages) {
		return false
	}
	if scope == utils.SourceScopeProject {
		return pm.settingsManager.SetProjectPackages(nextPackages) == nil
	}
	pm.settingsManager.SetPackages(nextPackages)
	return true
}

func (pm *DefaultPackageManager) tempDir(prefix, suffix, ref string) (string, error) {
	base, err := pmResolveManagedPath(GetExtensionTempFolder(pm.agentDir), prefix)
	if err != nil {
		return "", err
	}
	raw := prefix + "-" + suffix
	if ref != "" {
		raw += "@" + ref
	}
	sum := sha256.Sum256([]byte(raw))
	hashStr := hex.EncodeToString(sum[:])[:8]
	if suffix != "" {
		parts := strings.Split(filepath.ToSlash(suffix), "/")
		return pmResolveManagedPath(base, append([]string{hashStr}, parts...)...)
	}
	return pmResolveManagedPath(base, hashStr)
}

func (pm *DefaultPackageManager) npmInstallRoot(scope utils.SourceScope, temporary bool) (string, error) {
	if temporary || scope == utils.SourceScopeTemporary {
		return pm.tempDir("npm", "", "")
	}
	if scope == utils.SourceScopeProject {
		if err := pm.assertProjectTrustedForScope(scope); err != nil {
			return "", err
		}
		return filepath.Join(pm.cwd, utils.ConfigDirName, "npm"), nil
	}
	return filepath.Join(pm.agentDir, "npm"), nil
}

func (pm *DefaultPackageManager) npmInstallPath(parsed pmParsedSource, scope utils.SourceScope) (string, error) {
	root, err := pm.npmInstallRoot(scope, scope == utils.SourceScopeTemporary)
	if err != nil {
		return "", err
	}
	if err := pmValidateNpmPackageName(parsed.name); err != nil {
		return "", err
	}
	return pmResolveManagedPath(filepath.Join(root, "node_modules"), strings.Split(parsed.name, "/")...)
}

func (pm *DefaultPackageManager) gitInstallRoot(scope utils.SourceScope) (string, error) {
	if scope == utils.SourceScopeTemporary {
		return "", nil
	}
	if scope == utils.SourceScopeProject {
		if err := pm.assertProjectTrustedForScope(scope); err != nil {
			return "", err
		}
		return filepath.Join(pm.cwd, utils.ConfigDirName, "git"), nil
	}
	return filepath.Join(pm.agentDir, "git"), nil
}

func (pm *DefaultPackageManager) gitInstallPath(parsed pmParsedSource, scope utils.SourceScope) (string, error) {
	if scope == utils.SourceScopeTemporary {
		return pm.tempDir("git-"+parsed.host, parsed.path, parsed.ref)
	}
	root, err := pm.gitInstallRoot(scope)
	if err != nil {
		return "", err
	}
	parts := append([]string{parsed.host}, strings.Split(filepath.ToSlash(parsed.path), "/")...)
	return pmResolveManagedPath(root, parts...)
}

// GetInstalledPath returns the installed directory for source in scope ("user", "project", or "temporary"),
// or nil if the source is not present on disk or cannot be accessed safely.
func (pm *DefaultPackageManager) GetInstalledPath(source string, scope ...string) *string {
	resolvedScope := utils.SourceScopeUser
	if len(scope) > 0 && scope[0] != "" {
		resolvedScope = utils.SourceScope(scope[0])
	}
	parsed := pmParseSource(source)
	switch parsed.kind {
	case pmSourceNpm:
		p, err := pm.npmInstallPath(parsed, resolvedScope)
		if err != nil || !pmPathExists(p) {
			return nil
		}
		return &p
	case pmSourceGit:
		p, err := pm.gitInstallPath(parsed, resolvedScope)
		if err != nil || !pmPathExists(p) {
			return nil
		}
		return &p
	case pmSourceLocal:
		baseDir, err := pm.baseDirForScope(resolvedScope)
		if err != nil {
			return nil
		}
		p := pm.resolvePathFromBase(parsed.path, baseDir)
		if !pmPathExists(p) {
			return nil
		}
		return &p
	default:
		return nil
	}
}

// ListConfiguredPackages returns all packages configured in global and project settings.
func (pm *DefaultPackageManager) ListConfiguredPackages() []ConfiguredPackage {
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()
	configured := make([]ConfiguredPackage, 0, len(globalSettings.Packages)+len(projectSettings.Packages))
	for _, pkg := range globalSettings.Packages {
		src := pmPackageSourceString(pkg)
		var installedPath string
		if p := pm.GetInstalledPath(src, string(utils.SourceScopeUser)); p != nil {
			installedPath = *p
		}
		configured = append(configured, ConfiguredPackage{
			Source:        src,
			Scope:         utils.SourceScopeUser,
			InstalledPath: installedPath,
			Filtered:      pkg.Object != nil,
		})
	}
	for _, pkg := range projectSettings.Packages {
		src := pmPackageSourceString(pkg)
		var installedPath string
		if p := pm.GetInstalledPath(src, string(utils.SourceScopeProject)); p != nil {
			installedPath = *p
		}
		configured = append(configured, ConfiguredPackage{
			Source:        src,
			Scope:         utils.SourceScopeProject,
			InstalledPath: installedPath,
			Filtered:      pkg.Object != nil,
		})
	}
	return configured
}

// GetInstalledPackages returns configured packages across global and project settings.
func (pm *DefaultPackageManager) GetInstalledPackages() []ConfiguredPackage {
	return pm.ListConfiguredPackages()
}

// High-level operations: Resolve, ResolveExtensionSources, Install, Remove, Update, CheckForAvailableUpdates

// Resolve discovers and returns all configured and auto-discovered resources.
func (pm *DefaultPackageManager) Resolve(ctx context.Context, onMissing ...func(source string) (MissingSourceAction, error)) (ResolvedPaths, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var missingCb func(string) (MissingSourceAction, error)
	if len(onMissing) > 0 {
		missingCb = onMissing[0]
	}

	acc := pmNewAccumulator()
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()

	allPackages := make([]pmPackageEntry, 0, len(projectSettings.Packages)+len(globalSettings.Packages))
	for _, pkg := range projectSettings.Packages {
		allPackages = append(allPackages, pmPackageEntry{pkg: pkg, scope: utils.SourceScopeProject})
	}
	for _, pkg := range globalSettings.Packages {
		allPackages = append(allPackages, pmPackageEntry{pkg: pkg, scope: utils.SourceScopeUser})
	}

	packageSources := pm.dedupePackages(allPackages)
	installFailures, err := pm.resolvePackageSources(ctx, packageSources, acc, missingCb)
	if err != nil {
		return ResolvedPaths{}, err
	}

	globalBaseDir := pm.agentDir
	projectBaseDir := filepath.Join(pm.cwd, utils.ConfigDirName)

	resolveType := func(rt pmResourceType, projEntries, globEntries []string) {
		target := acc.target(rt)
		pm.resolveLocalEntries(projEntries, rt, target, PathMetadata{
			Source: "local",
			Scope:  utils.SourceScopeProject,
			Origin: utils.SourceOriginTopLevel,
		}, projectBaseDir)
		pm.resolveLocalEntries(globEntries, rt, target, PathMetadata{
			Source: "local",
			Scope:  utils.SourceScopeUser,
			Origin: utils.SourceOriginTopLevel,
		}, globalBaseDir)
	}
	resolveType(pmResExtensions, projectSettings.Extensions, globalSettings.Extensions)
	resolveType(pmResSkills, projectSettings.Skills, globalSettings.Skills)
	resolveType(pmResPrompts, projectSettings.Prompts, globalSettings.Prompts)
	resolveType(pmResThemes, projectSettings.Themes, globalSettings.Themes)

	pm.addAutoDiscoveredResources(acc, globalSettings, projectSettings, globalBaseDir, projectBaseDir)

	for _, name := range pm.builtinExtensions {
		p := utils.BuiltinPathPrefix + name
		projEnabled, hasProjMatch := pmApplyAutoloadDisabledPatterns([]string{p}, pmGetOverridePatterns(projectSettings.Extensions), projectBaseDir)[p]
		scope := utils.SourceScopeUser
		enabled := pmIsEnabledByOverrides(p, globalSettings.Extensions, globalBaseDir)
		if hasProjMatch {
			scope = utils.SourceScopeProject
			enabled = projEnabled
		}
		acc.extensions.add(p, PathMetadata{
			Source: "builtin",
			Scope:  scope,
			Origin: utils.SourceOriginTopLevel,
		}, enabled)
	}

	return acc.toResolvedPaths(installFailures), nil
}

// ResolveExtensionSources resolves an explicit list of extension/package sources (e.g. from -e CLI flags).
func (pm *DefaultPackageManager) ResolveExtensionSources(ctx context.Context, sources []string, options ...*ResolveExtensionSourcesOptions) (ResolvedPaths, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt ResolveExtensionSourcesOptions
	if len(options) > 0 && options[0] != nil {
		opt = *options[0]
	}

	scope := utils.SourceScopeUser
	if opt.Temporary {
		scope = utils.SourceScopeTemporary
	} else if opt.Local {
		scope = utils.SourceScopeProject
	}

	acc := pmNewAccumulator()
	pkgSources := make([]pmPackageEntry, 0, len(sources))
	for _, src := range sources {
		if strings.HasPrefix(src, utils.BuiltinPathPrefix) {
			acc.extensions.add(src, PathMetadata{
				Source: "builtin",
				Scope:  scope,
				Origin: utils.SourceOriginTopLevel,
			}, true)
			continue
		}
		pkgSources = append(pkgSources, pmPackageEntry{
			pkg:   PackageSourceFromString(src),
			scope: scope,
		})
	}

	installFailures, err := pm.resolvePackageSources(ctx, pkgSources, acc, opt.OnMissing)
	if err != nil {
		return ResolvedPaths{}, err
	}
	return acc.toResolvedPaths(installFailures), nil
}

// Install installs a package from an npm, git, or local source without modifying settings.json.
func (pm *DefaultPackageManager) Install(ctx context.Context, source string, options ...*PackageInstallOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	local := len(options) > 0 && options[0] != nil && options[0].Local
	scope := utils.SourceScopeUser
	if local {
		scope = utils.SourceScopeProject
	}
	if err := pm.assertProjectTrustedForScope(scope); err != nil {
		return err
	}
	parsed := pmParseSource(source)
	return pm.withProgress("install", source, fmt.Sprintf("Installing %s...", source), func() error {
		switch parsed.kind {
		case pmSourceNpm:
			return pm.installNpm(ctx, parsed, scope, false)
		case pmSourceGit:
			return pm.installGit(ctx, parsed, scope)
		case pmSourceLocal:
			resolved := pm.resolvePath(parsed.path)
			if !pmPathExists(resolved) {
				return fmt.Errorf("Path does not exist: %s", resolved)
			}
			return nil
		default:
			return fmt.Errorf("Unsupported install source: %s", source)
		}
	})
}

// InstallAndPersist installs a package and adds its source to global or project settings.
func (pm *DefaultPackageManager) InstallAndPersist(ctx context.Context, source string, options ...*PackageInstallOptions) error {
	if err := pm.Install(ctx, source, options...); err != nil {
		return err
	}
	local := len(options) > 0 && options[0] != nil && options[0].Local
	pm.AddSourceToSettings(source, local)
	return nil
}

// Remove uninstalls an npm or git package without modifying settings.json.
func (pm *DefaultPackageManager) Remove(ctx context.Context, source string, options ...*PackageRemoveOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	local := len(options) > 0 && options[0] != nil && options[0].Local
	scope := utils.SourceScopeUser
	if local {
		scope = utils.SourceScopeProject
	}
	if err := pm.assertProjectTrustedForScope(scope); err != nil {
		return err
	}
	parsed := pmParseSource(source)
	return pm.withProgress("remove", source, fmt.Sprintf("Removing %s...", source), func() error {
		switch parsed.kind {
		case pmSourceNpm:
			return pm.uninstallNpm(parsed, scope)
		case pmSourceGit:
			return pm.removeGit(parsed, scope)
		case pmSourceLocal:
			return nil
		default:
			return fmt.Errorf("Unsupported remove source: %s", source)
		}
	})
}

// RemoveAndPersist uninstalls a package and removes its source from settings.json.
func (pm *DefaultPackageManager) RemoveAndPersist(ctx context.Context, source string, options ...*PackageRemoveOptions) (bool, error) {
	if err := pm.Remove(ctx, source, options...); err != nil {
		return false, err
	}
	local := len(options) > 0 && options[0] != nil && options[0].Local
	return pm.RemoveSourceFromSettings(source, local), nil
}

type pmConfiguredUpdateSource struct {
	source string
	scope  utils.SourceScope
	parsed pmParsedSource
}

type pmUpdatedPackage struct {
	source string
	kind   string
	scope  utils.SourceScope
	from   string
	to     string
}

// Update updates all configured non-pinned packages or a single matching source.
func (pm *DefaultPackageManager) Update(ctx context.Context, source ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var targetSource string
	if len(source) > 0 {
		targetSource = source[0]
	}

	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()

	var identity string
	if targetSource != "" {
		identity = pm.packageIdentity(targetSource)
	}

	var updateSources []pmConfiguredUpdateSource
	matched := false
	for _, pkg := range globalSettings.Packages {
		srcStr := pmPackageSourceString(pkg)
		if identity != "" && pm.packageIdentity(srcStr, utils.SourceScopeUser) != identity {
			continue
		}
		matched = true
		updateSources = append(updateSources, pmConfiguredUpdateSource{
			source: srcStr,
			scope:  utils.SourceScopeUser,
			parsed: pmParseSource(srcStr),
		})
	}
	for _, pkg := range projectSettings.Packages {
		srcStr := pmPackageSourceString(pkg)
		if identity != "" && pm.packageIdentity(srcStr, utils.SourceScopeProject) != identity {
			continue
		}
		matched = true
		updateSources = append(updateSources, pmConfiguredUpdateSource{
			source: srcStr,
			scope:  utils.SourceScopeProject,
			parsed: pmParseSource(srcStr),
		})
	}

	if targetSource != "" && !matched {
		return errors.New(pm.buildNoMatchingPackageMessage(targetSource, append(slices.Clone(globalSettings.Packages), projectSettings.Packages...)))
	}

	return pm.updateConfiguredSources(ctx, updateSources)
}

func (pm *DefaultPackageManager) buildNoMatchingPackageMessage(source string, configured []PackageSource) string {
	var candidates []string
	for _, pkg := range configured {
		src := pmPackageSourceString(pkg)
		if src != "" {
			candidates = append(candidates, src)
		}
	}
	if len(candidates) == 0 {
		return fmt.Sprintf("No package matching %q is configured in settings.", source)
	}
	return fmt.Sprintf("No package matching %q is configured. Configured packages: %s", source, strings.Join(candidates, ", "))
}

type pmNpmCheckResult struct {
	entry            pmConfiguredUpdateSource
	installedVersion string
	targetVersion    string
	shouldUpdate     bool
}

func (pm *DefaultPackageManager) updateConfiguredSources(ctx context.Context, sources []pmConfiguredUpdateSource) error {
	if IsOfflineModeEnabled() || len(sources) == 0 {
		return nil
	}

	var npmCandidates []pmConfiguredUpdateSource
	var gitCandidates []pmConfiguredUpdateSource
	for _, entry := range sources {
		switch entry.parsed.kind {
		case pmSourceNpm:
			if !entry.parsed.pinned {
				npmCandidates = append(npmCandidates, entry)
			}
		case pmSourceGit:
			gitCandidates = append(gitCandidates, entry)
		}
	}

	npmChecks := make([]pmNpmCheckResult, len(npmCandidates))
	if len(npmCandidates) > 0 {
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(pmUpdateCheckConcurrency)
		for i, entry := range npmCandidates {
			g.Go(func() error {
				installedVer, targetVer, shouldUpdate := pm.shouldUpdateNpmSource(gCtx, entry.parsed, entry.scope)
				npmChecks[i] = pmNpmCheckResult{
					entry:            entry,
					installedVersion: installedVer,
					targetVersion:    targetVer,
					shouldUpdate:     shouldUpdate,
				}
				return nil
			})
		}
		_ = g.Wait()
	}

	var npmUpdates []pmNpmCheckResult
	for _, res := range npmChecks {
		if res.shouldUpdate {
			npmUpdates = append(npmUpdates, res)
		}
	}

	gitUpdates := make([]*pmUpdatedPackage, len(gitCandidates))

	outerGroup, outerCtx := errgroup.WithContext(ctx)
	if len(npmUpdates) > 0 {
		outerGroup.Go(func() error {
			for _, res := range npmUpdates {
				res := res
				if err := pm.withProgress("update", res.entry.source, fmt.Sprintf("Updating %s...", res.entry.source), func() error {
					specParsed := res.entry.parsed
					if specParsed.ver == "" {
						specParsed.ver = "latest"
					}
					if err := pm.installNpm(outerCtx, specParsed, res.entry.scope, false); err != nil {
						return err
					}
					installedPath, err := pm.npmInstallPath(res.entry.parsed, res.entry.scope)
					if err == nil && res.installedVersion != "" && res.targetVersion != "" {
						after := pmGetInstalledNpmVersion(installedPath)
						if after == res.installedVersion && after != res.targetVersion {
							pm.emitProgress(ProgressEvent{
								Type:    "progress",
								Action:  "update",
								Source:  res.entry.source,
								Message: fmt.Sprintf("%s is still %s (registry reports %s); npm registry metadata may be stale, try again in a few minutes", res.entry.source, after, res.targetVersion),
							})
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
			return nil
		})
	}

	if len(gitCandidates) > 0 {
		outerGroup.Go(func() error {
			g, gCtx := errgroup.WithContext(outerCtx)
			g.SetLimit(pmGitUpdateConcurrency)
			for i, entry := range gitCandidates {
				g.Go(func() error {
					return pm.withProgress("update", entry.source, fmt.Sprintf("Updating %s...", entry.source), func() error {
						targetDir, err := pm.gitInstallPath(entry.parsed, entry.scope)
						if err != nil {
							return err
						}
						from := pm.getGitHead(gCtx, targetDir)
						if err := pm.updateGit(gCtx, entry.parsed, entry.scope); err != nil {
							return err
						}
						to := pm.getGitHead(gCtx, targetDir)
						if from != to {
							gitUpdates[i] = &pmUpdatedPackage{
								source: entry.source,
								kind:   "git",
								scope:  entry.scope,
								from:   from,
								to:     to,
							}
						}
						return nil
					})
				})
			}
			return g.Wait()
		})
	}

	if err := outerGroup.Wait(); err != nil {
		return err
	}

	var updated []pmUpdatedPackage
	for _, res := range npmUpdates {
		installedPath, err := pm.npmInstallPath(res.entry.parsed, res.entry.scope)
		if err != nil {
			continue
		}
		var to string
		if pmPathExists(installedPath) {
			to = pmGetInstalledNpmVersion(installedPath)
		}
		if to != res.installedVersion {
			updated = append(updated, pmUpdatedPackage{
				source: res.entry.source,
				kind:   "npm",
				scope:  res.entry.scope,
				from:   res.installedVersion,
				to:     to,
			})
		}
	}
	for _, u := range gitUpdates {
		if u != nil {
			updated = append(updated, *u)
		}
	}

	for _, pkg := range updated {
		fmtVal := func(v string) string {
			if v == "" {
				return "(none)"
			}
			if pkg.kind == "git" && len(v) > 7 {
				return v[:7]
			}
			return v
		}
		scopeSuffix := ""
		if pkg.scope == utils.SourceScopeProject {
			scopeSuffix = " (project)"
		}
		pm.emitProgress(ProgressEvent{
			Type:    "result",
			Action:  "update",
			Source:  pkg.source,
			Message: fmt.Sprintf("%s%s %s -> %s", pkg.source, scopeSuffix, fmtVal(pkg.from), fmtVal(pkg.to)),
		})
	}

	return nil
}

// CheckForAvailableUpdates checks non-pinned configured npm and git packages for updates without installing.
func (pm *DefaultPackageManager) CheckForAvailableUpdates(ctx context.Context) ([]PackageUpdate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if IsOfflineModeEnabled() {
		return []PackageUpdate{}, nil
	}
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()

	allPackages := make([]pmPackageEntry, 0, len(projectSettings.Packages)+len(globalSettings.Packages))
	for _, pkg := range projectSettings.Packages {
		allPackages = append(allPackages, pmPackageEntry{pkg: pkg, scope: utils.SourceScopeProject})
	}
	for _, pkg := range globalSettings.Packages {
		allPackages = append(allPackages, pmPackageEntry{pkg: pkg, scope: utils.SourceScopeUser})
	}

	deduped := pm.dedupePackages(allPackages)
	var checkEntries []pmPackageEntry
	for _, entry := range deduped {
		if entry.scope != utils.SourceScopeTemporary {
			checkEntries = append(checkEntries, entry)
		}
	}

	results := make([]*PackageUpdate, len(checkEntries))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(pmUpdateCheckConcurrency)
	for i, entry := range checkEntries {
		g.Go(func() error {
			src := pmPackageSourceString(entry.pkg)
			parsed := pmParseSource(src)
			if parsed.kind == pmSourceLocal || parsed.pinned {
				return nil
			}
			switch parsed.kind {
			case pmSourceNpm:
				installedPath, err := pm.npmInstallPath(parsed, entry.scope)
				if err != nil || !pmPathExists(installedPath) {
					return nil
				}
				installedVer, latestVer, hasUpdate := pm.npmHasAvailableUpdate(gCtx, parsed, installedPath)
				if !hasUpdate {
					return nil
				}
				results[i] = &PackageUpdate{
					Source:         src,
					Scope:          entry.scope,
					Type:           "npm",
					DisplayName:    parsed.name,
					CurrentVersion: installedVer,
					LatestVersion:  latestVer,
				}
			case pmSourceGit:
				installedPath, err := pm.gitInstallPath(parsed, entry.scope)
				if err != nil || !pmPathExists(installedPath) {
					return nil
				}
				localHead, remoteHead, hasUpdate := pm.gitHasAvailableUpdate(gCtx, installedPath)
				if !hasUpdate {
					return nil
				}
				results[i] = &PackageUpdate{
					Source:         src,
					Scope:          entry.scope,
					Type:           "git",
					DisplayName:    fmt.Sprintf("%s/%s", parsed.host, parsed.path),
					CurrentVersion: localHead,
					LatestVersion:  remoteHead,
				}
			}
			return nil
		})
	}
	_ = g.Wait()

	updates := make([]PackageUpdate, 0, len(results))
	for _, res := range results {
		if res != nil {
			updates = append(updates, *res)
		}
	}
	return updates, nil
}

// Package source resolution

func (pm *DefaultPackageManager) resolvePackageSources(
	ctx context.Context,
	sources []pmPackageEntry,
	acc *pmResourceAccumulator,
	onMissing func(source string) (MissingSourceAction, error),
) ([]PackageInstallFailure, error) {
	var failures []PackageInstallFailure
	for _, entry := range sources {
		sourceStr := pmPackageSourceString(entry.pkg)
		filter := entry.pkg.Object
		resolvedSource := sourceStr
		resolvedScope := entry.scope
		if baseSrc, baseScope, ok := pm.findAutoloadDeltaBase(entry.pkg, entry.scope, sources); ok {
			resolvedSource = baseSrc
			resolvedScope = baseScope
		}
		parsed := pmParseSource(resolvedSource)
		meta := PathMetadata{
			Source: sourceStr,
			Scope:  entry.scope,
			Origin: utils.SourceOriginPackage,
		}

		if parsed.kind == pmSourceLocal {
			baseDir, err := pm.baseDirForScope(resolvedScope)
			if err != nil {
				continue
			}
			pm.resolveLocalExtensionSource(parsed, acc, filter, meta, baseDir)
			continue
		}

		installMissing := func() (bool, error) {
			if IsOfflineModeEnabled() {
				return false, nil
			}
			if onMissing != nil {
				action, err := onMissing(resolvedSource)
				if err != nil {
					return false, err
				}
				if action == MissingSourceSkip {
					return false, nil
				}
				if action == MissingSourceAbort || action == "error" {
					return false, fmt.Errorf("Missing source: %s", resolvedSource)
				}
			}
			if err := pm.installParsedSource(ctx, parsed, resolvedScope); err != nil {
				msg := err.Error()
				failures = append(failures, PackageInstallFailure{
					Source:  resolvedSource,
					Scope:   resolvedScope,
					Message: msg,
					Error:   msg,
				})
				return false, nil
			}
			return true, nil
		}

		if parsed.kind == pmSourceNpm {
			installedPath, err := pm.npmInstallPath(parsed, resolvedScope)
			if err != nil {
				failures = append(failures, PackageInstallFailure{
					Source:  resolvedSource,
					Scope:   resolvedScope,
					Message: err.Error(),
					Error:   err.Error(),
				})
				continue
			}
			needsInstall := !pmPathExists(installedPath) || !pmInstalledNpmMatchesConfiguredVersion(parsed, installedPath)
			if !needsInstall && resolvedScope == utils.SourceScopeTemporary && !parsed.pinned {
				if _, _, hasUpd := pm.npmHasAvailableUpdate(ctx, parsed, installedPath); hasUpd {
					refreshParsed := parsed
					if refreshParsed.ver == "" {
						refreshParsed.ver = "latest"
					}
					_ = pm.installNpm(ctx, refreshParsed, resolvedScope, true)
				}
			}
			if needsInstall {
				installed, err := installMissing()
				if err != nil {
					return nil, err
				}
				if !installed {
					continue
				}
				installedPath, err = pm.npmInstallPath(parsed, resolvedScope)
				if err != nil {
					continue
				}
			}
			meta.BaseDir = installedPath
			meta.PackageRoot = installedPath
			pm.collectPackageResources(installedPath, acc, filter, meta)
			continue
		}

		if parsed.kind == pmSourceGit {
			installedPath, err := pm.gitInstallPath(parsed, resolvedScope)
			if err != nil {
				failures = append(failures, PackageInstallFailure{
					Source:  resolvedSource,
					Scope:   resolvedScope,
					Message: err.Error(),
					Error:   err.Error(),
				})
				continue
			}
			if !pmPathExists(installedPath) {
				installed, err := installMissing()
				if err != nil {
					return nil, err
				}
				if !installed {
					continue
				}
			} else if resolvedScope == utils.SourceScopeTemporary && !IsOfflineModeEnabled() {
				pm.refreshTemporaryGitSource(ctx, parsed, resolvedSource, installedPath)
			}
			meta.BaseDir = installedPath
			meta.PackageRoot = installedPath
			pm.collectPackageResources(installedPath, acc, filter, meta)
		}
	}
	return failures, nil
}

func (pm *DefaultPackageManager) installParsedSource(ctx context.Context, parsed pmParsedSource, scope utils.SourceScope) error {
	switch parsed.kind {
	case pmSourceNpm:
		return pm.installNpm(ctx, parsed, scope, scope == utils.SourceScopeTemporary)
	case pmSourceGit:
		return pm.installGit(ctx, parsed, scope)
	default:
		return nil
	}
}

func (pm *DefaultPackageManager) resolveLocalExtensionSource(parsed pmParsedSource, acc *pmResourceAccumulator, filter *PackageSourceObject, meta PathMetadata, baseDir string) {
	resolved := pm.resolvePathFromBase(parsed.path, baseDir)
	info, err := os.Stat(resolved)
	if err != nil {
		return
	}
	if !info.IsDir() {
		meta.BaseDir = filepath.Dir(resolved)
		acc.extensions.add(resolved, meta, true)
		return
	}
	meta.BaseDir = resolved
	meta.PackageRoot = resolved
	foundAny := pm.collectPackageResources(resolved, acc, filter, meta)
	if !foundAny {
		acc.extensions.add(resolved, meta, true)
	}
}

// Pure-Go NPM Registry Client & Tarball Installer

type pmNpmPackument struct {
	Name     string                       `json:"name"`
	DistTags map[string]string            `json:"dist-tags"`
	Versions map[string]pmNpmVersionEntry `json:"versions"`
}

type pmNpmVersionEntry struct {
	Name         string                    `json:"name"`
	Version      string                    `json:"version"`
	Dependencies *omap.Map[string, string] `json:"dependencies"`
	Dist         pmNpmDist                 `json:"dist"`
}

type pmNpmDist struct {
	Tarball   string `json:"tarball"`
	Shasum    string `json:"shasum"`
	Integrity string `json:"integrity"`
}

func pmValidateNpmPackageName(name string) error {
	if name == "" {
		return errors.New("empty npm package name")
	}
	if !pmValidNpmNameRe.MatchString(name) {
		return fmt.Errorf("invalid npm package name: %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid npm package name: %q", name)
		}
	}
	return nil
}

func pmEscapeNpmPackageName(name string) string {
	if strings.HasPrefix(name, "@") {
		if idx := strings.IndexByte(name, '/'); idx > 0 {
			return name[:idx] + "%2f" + name[idx+1:]
		}
	}
	return url.PathEscape(name)
}

func pmNpmRegistryURL() string {
	reg := strings.TrimSpace(os.Getenv("NPM_CONFIG_REGISTRY"))
	if reg == "" {
		reg = strings.TrimSpace(os.Getenv("npm_config_registry"))
	}
	if reg == "" {
		reg = pmDefaultNpmRegistry
	}
	return strings.TrimRight(reg, "/")
}

func pmGetInstalledNpmVersion(installedPath string) string {
	pkgPath := filepath.Join(installedPath, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}
	var parsed struct {
		Version string `json:"version"`
	}
	if err := jsonx.Unmarshal([]byte(utils.StripBom(string(data))), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Version)
}

func pmInstalledNpmMatchesConfiguredVersion(parsed pmParsedSource, installedPath string) bool {
	installedVer := pmGetInstalledNpmVersion(installedPath)
	if installedVer == "" {
		return false
	}
	if parsed.npmRng == "" {
		return true
	}
	return semver.Satisfies(installedVer, parsed.npmRng)
}

func (pm *DefaultPackageManager) fetchNpmPackument(ctx context.Context, pkgName string) (*pmNpmPackument, error) {
	if IsOfflineModeEnabled() {
		return nil, errors.New("cannot query npm registry in offline mode")
	}
	if err := pmValidateNpmPackageName(pkgName); err != nil {
		return nil, err
	}
	endpoint := pmNpmRegistryURL() + "/" + pmEscapeNpmPackageName(pkgName)
	reqCtx, cancel := context.WithTimeout(ctx, pmNetworkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", utils.GetPiUserAgent(utils.VERSION))
	resp, err := pm.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("npm registry returned HTTP %d for %s", resp.StatusCode, pkgName)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return nil, err
	}
	var packument pmNpmPackument
	if err := jsonx.Unmarshal(body, &packument); err != nil {
		return nil, fmt.Errorf("invalid npm packument for %s: %w", pkgName, err)
	}
	return &packument, nil
}

func pmResolveNpmVersion(packument *pmNpmPackument, specVer, semverRange string) (pmNpmVersionEntry, error) {
	if packument == nil || len(packument.Versions) == 0 {
		return pmNpmVersionEntry{}, errors.New("npm package has no published versions")
	}
	versions := make([]string, 0, len(packument.Versions))
	for k := range packument.Versions {
		versions = append(versions, k)
	}
	slices.SortFunc(versions, semver.RCompare)

	selected := ""
	switch {
	case specVer == "":
		if latest, ok := packument.DistTags["latest"]; ok && latest != "" {
			if _, exists := packument.Versions[latest]; exists {
				selected = latest
			}
		}
		if selected == "" {
			selected = semver.MaxSatisfying(versions, "*")
		}
	default:
		if _, exists := packument.Versions[specVer]; exists {
			selected = specVer
		} else if tagged, ok := packument.DistTags[specVer]; ok && tagged != "" {
			if _, exists := packument.Versions[tagged]; exists {
				selected = tagged
			}
		} else if semverRange != "" {
			selected = semver.MaxSatisfying(versions, semverRange)
		} else if _, ok := semver.ValidRange(specVer); ok {
			selected = semver.MaxSatisfying(versions, specVer)
		}
	}

	if selected == "" {
		return pmNpmVersionEntry{}, fmt.Errorf("no matching version %q for npm package %s", specVer, packument.Name)
	}
	entry, ok := packument.Versions[selected]
	if !ok {
		return pmNpmVersionEntry{}, fmt.Errorf("version %q missing in packument for %s", selected, packument.Name)
	}
	if entry.Version == "" {
		entry.Version = selected
	}
	return entry, nil
}

func (pm *DefaultPackageManager) npmHasAvailableUpdate(ctx context.Context, parsed pmParsedSource, installedPath string) (installedVer, latestVer string, hasUpdate bool) {
	if IsOfflineModeEnabled() {
		return "", "", false
	}
	installedVer = pmGetInstalledNpmVersion(installedPath)
	if installedVer == "" {
		return "", "", false
	}
	packument, err := pm.fetchNpmPackument(ctx, parsed.name)
	if err != nil {
		return installedVer, "", false
	}
	targetEntry, err := pmResolveNpmVersion(packument, parsed.ver, parsed.npmRng)
	if err != nil {
		return installedVer, "", false
	}
	return installedVer, targetEntry.Version, semver.Gt(targetEntry.Version, installedVer)
}

func (pm *DefaultPackageManager) shouldUpdateNpmSource(ctx context.Context, parsed pmParsedSource, scope utils.SourceScope) (installedVer, targetVer string, shouldUpdate bool) {
	installedPath, err := pm.npmInstallPath(parsed, scope)
	if err != nil {
		return "", "", false
	}
	if pmPathExists(installedPath) {
		installedVer = pmGetInstalledNpmVersion(installedPath)
	}
	if installedVer == "" {
		return "", "", true
	}
	packument, err := pm.fetchNpmPackument(ctx, parsed.name)
	if err != nil {
		return installedVer, "", true
	}
	specVer := parsed.ver
	if specVer == "" {
		specVer = "latest"
	}
	targetEntry, err := pmResolveNpmVersion(packument, specVer, parsed.npmRng)
	if err != nil {
		return installedVer, "", true
	}
	return installedVer, targetEntry.Version, semver.Gt(targetEntry.Version, installedVer)
}

func pmEnsureGitIgnoreDir(dir string) {
	_ = os.MkdirAll(dir, 0o755)
	utils.MarkPathIgnoredByCloudSync(dir)
	ignorePath := filepath.Join(dir, ".gitignore")
	if !pmPathExists(ignorePath) {
		_ = os.WriteFile(ignorePath, []byte("*\n!.gitignore\n"), 0o644)
	}
}

func pmEnsureNpmProject(installRoot string) error {
	if err := os.MkdirAll(installRoot, 0o755); err != nil {
		return err
	}
	pmEnsureGitIgnoreDir(installRoot)
	pkgJsonPath := filepath.Join(installRoot, "package.json")
	if !pmPathExists(pkgJsonPath) {
		doc := jsonx.ObjectOf("name", "pi-extensions", "private", true)
		encoded, err := jsonx.StringifyIndent(doc, "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(pkgJsonPath, []byte(encoded+"\n"), 0o644)
	}
	return nil
}

func pmUpdateNpmRootPackageJSON(installRoot, pkgName, versionSpec string, remove bool) error {
	if err := pmEnsureNpmProject(installRoot); err != nil {
		return err
	}
	pkgJsonPath := filepath.Join(installRoot, "package.json")
	raw, err := os.ReadFile(pkgJsonPath)
	if err != nil {
		return err
	}
	parsed, err := jsonx.Parse([]byte(utils.StripBom(string(raw))))
	if err != nil {
		return err
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		obj = jsonx.ObjectOf("name", "pi-extensions", "private", true)
	}

	var deps *jsonx.Object
	if existing, exists := obj.Get("dependencies"); exists {
		if d, ok := existing.(*jsonx.Object); ok {
			deps = d
		}
	}
	if deps == nil {
		if remove {
			return nil
		}
		deps = jsonx.NewObject()
		obj.Set("dependencies", deps)
	}

	if remove {
		deps.Delete(pkgName)
	} else {
		deps.Set(pkgName, versionSpec)
	}

	encoded, err := jsonx.StringifyIndent(obj, "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pkgJsonPath, []byte(encoded+"\n"), 0o644)
}

func (pm *DefaultPackageManager) installNpm(ctx context.Context, parsed pmParsedSource, scope utils.SourceScope, temporary bool) error {
	installRoot, err := pm.npmInstallRoot(scope, temporary)
	if err != nil {
		return err
	}
	if err := pmEnsureNpmProject(installRoot); err != nil {
		return err
	}
	nodeModulesRoot := filepath.Join(installRoot, "node_modules")
	if err := os.MkdirAll(nodeModulesRoot, 0o755); err != nil {
		return err
	}
	visited := make(map[string]struct{})
	entry, err := pm.installNpmPackageIntoNodeModules(ctx, parsed.name, parsed.ver, parsed.npmRng, nodeModulesRoot, visited)
	if err != nil {
		return err
	}
	recordedVer := parsed.ver
	if recordedVer == "" || recordedVer == "latest" {
		recordedVer = entry.Version
	}
	return pmUpdateNpmRootPackageJSON(installRoot, parsed.name, recordedVer, false)
}

func (pm *DefaultPackageManager) installNpmPackageIntoNodeModules(
	ctx context.Context,
	pkgName, specVer, semverRange, nodeModulesRoot string,
	visited map[string]struct{},
) (pmNpmVersionEntry, error) {
	if err := pmValidateNpmPackageName(pkgName); err != nil {
		return pmNpmVersionEntry{}, err
	}
	packument, err := pm.fetchNpmPackument(ctx, pkgName)
	if err != nil {
		return pmNpmVersionEntry{}, err
	}
	entry, err := pmResolveNpmVersion(packument, specVer, semverRange)
	if err != nil {
		return pmNpmVersionEntry{}, err
	}
	visitKey := pkgName + "@" + entry.Version
	if _, seen := visited[visitKey]; seen {
		return entry, nil
	}
	visited[visitKey] = struct{}{}

	if entry.Dist.Tarball == "" {
		return pmNpmVersionEntry{}, fmt.Errorf("npm package %s@%s has no tarball URL", pkgName, entry.Version)
	}
	targetDir, err := pmResolveManagedPath(nodeModulesRoot, strings.Split(pkgName, "/")...)
	if err != nil {
		return pmNpmVersionEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return pmNpmVersionEntry{}, err
	}

	if err := pm.downloadAndExtractTarball(ctx, entry.Dist, targetDir); err != nil {
		return pmNpmVersionEntry{}, fmt.Errorf("installing %s@%s: %w", pkgName, entry.Version, err)
	}

	if entry.Dependencies != nil && entry.Dependencies.Len() > 0 {
		depNames := entry.Dependencies.Keys()
		slices.SortFunc(depNames, js.CompareUTF16)
		depModulesRoot := filepath.Join(targetDir, "node_modules")
		if err := os.MkdirAll(depModulesRoot, 0o755); err != nil {
			return pmNpmVersionEntry{}, err
		}
		for _, depName := range depNames {
			depRange, _ := entry.Dependencies.Get(depName)
			_, rngOk := semver.ValidRange(depRange)
			depSemverRange := ""
			if rngOk {
				depSemverRange = depRange
			}
			if _, err := pm.installNpmPackageIntoNodeModules(ctx, depName, depRange, depSemverRange, depModulesRoot, visited); err != nil {
				return pmNpmVersionEntry{}, err
			}
		}
	}
	return entry, nil
}

func (pm *DefaultPackageManager) downloadAndExtractTarball(ctx context.Context, dist pmNpmDist, targetDir string) error {
	if IsOfflineModeEnabled() {
		return errors.New("cannot download tarball in offline mode")
	}
	reqCtx, cancel := context.WithTimeout(ctx, pmNetworkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, dist.Tarball, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", utils.GetPiUserAgent(utils.VERSION))
	resp, err := pm.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("tarball download returned HTTP %d for %s", resp.StatusCode, dist.Tarball)
	}

	parentDir := filepath.Dir(targetDir)
	stageDir, err := os.MkdirTemp(parentDir, ".pi-stage-*")
	if err != nil {
		return err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	var verifiers []func() error
	reader := io.Reader(io.LimitReader(resp.Body, pmMaxTarballBytes))

	if dist.Integrity != "" {
		alg, expectedDigest, ok := pmParseSRIIntegrity(dist.Integrity)
		if ok {
			var h hash.Hash
			switch alg {
			case "sha512":
				h = sha512.New()
			case "sha256":
				h = sha256.New()
			case "sha1":
				h = sha1.New()
			}
			if h != nil {
				reader = io.TeeReader(reader, h)
				verifiers = append(verifiers, func() error {
					got := base64.StdEncoding.EncodeToString(h.Sum(nil))
					if got != expectedDigest {
						return fmt.Errorf("tarball %s integrity mismatch: got %s, want %s", alg, got, expectedDigest)
					}
					return nil
				})
			}
		}
	}
	if dist.Shasum != "" {
		h := sha1.New()
		reader = io.TeeReader(reader, h)
		wantHex := strings.ToLower(strings.TrimSpace(dist.Shasum))
		verifiers = append(verifiers, func() error {
			got := hex.EncodeToString(h.Sum(nil))
			if got != wantHex {
				return fmt.Errorf("tarball sha1 shasum mismatch: got %s, want %s", got, wantHex)
			}
			return nil
		})
	}

	if err := pmExtractTarGz(reader, stageDir); err != nil {
		return err
	}
	for _, verify := range verifiers {
		if err := verify(); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		return err
	}
	cleanupStage = false
	return nil
}

func pmParseSRIIntegrity(sri string) (alg, digest string, ok bool) {
	first := strings.Fields(sri)
	if len(first) == 0 {
		return "", "", false
	}
	parts := strings.SplitN(first[0], "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return strings.ToLower(parts[0]), parts[1], true
}

func pmSanitizeTarEntryPath(rawName string) (rel string, skip bool, err error) {
	normalized := strings.ReplaceAll(rawName, "\\", "/")
	if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
		return "", false, fmt.Errorf("archive entry has absolute path: %q", rawName)
	}
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", false, fmt.Errorf("archive entry traverses parent directory: %q", rawName)
		}
	}
	for strings.HasPrefix(normalized, "./") {
		normalized = normalized[2:]
	}
	normalized = strings.TrimSuffix(normalized, "/")
	if normalized == "" || normalized == "." || normalized == "package" {
		return "", true, nil
	}
	if strings.HasPrefix(normalized, "package/") {
		normalized = strings.TrimPrefix(normalized, "package/")
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == "" {
		return "", true, nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false, fmt.Errorf("archive entry escapes package root: %q", rawName)
	}
	return cleaned, false, nil
}

func pmExtractTarGz(r io.Reader, stageDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("invalid gzip stream: %w", err)
	}
	defer gz.Close()

	absStage, err := filepath.Abs(stageDir)
	if err != nil {
		return err
	}
	realStage, err := filepath.EvalSymlinks(absStage)
	if err != nil {
		realStage = absStage
	}

	verifyExistingAncestorWithinStage := func(targetPath string) error {
		dir := filepath.Dir(targetPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		realParent, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		if !pmIsSubpathOrEqual(realStage, realParent) {
			return fmt.Errorf("archive entry escapes extraction root via symlink: %s", targetPath)
		}
		return nil
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeGNULongName || hdr.Typeflag == tar.TypeGNULongLink {
			continue
		}

		rel, skip, err := pmSanitizeTarEntryPath(hdr.Name)
		if err != nil {
			return err
		}
		if skip {
			continue
		}

		dst := filepath.Join(absStage, filepath.FromSlash(rel))
		if !pmIsSubpathOrEqual(absStage, dst) {
			return fmt.Errorf("archive entry escapes stage root: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := verifyExistingAncestorWithinStage(dst); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o755
			}
			mode |= 0o700
			if err := os.MkdirAll(dst, mode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := verifyExistingAncestorWithinStage(dst); err != nil {
				return err
			}
			if _, lerr := os.Lstat(dst); lerr == nil {
				_ = os.Remove(dst)
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			mode |= 0o600
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, copyErr := io.Copy(f, io.LimitReader(tr, pmMaxTarballBytes))
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if hdr.Size >= 0 && n != hdr.Size {
				return fmt.Errorf("truncated file %q in archive: got %d, want %d", hdr.Name, n, hdr.Size)
			}
		case tar.TypeSymlink:
			linkRaw := strings.ReplaceAll(hdr.Linkname, "\\", "/")
			if linkRaw == "" || strings.HasPrefix(linkRaw, "/") || filepath.IsAbs(hdr.Linkname) || (len(linkRaw) >= 2 && linkRaw[1] == ':') {
				return fmt.Errorf("archive symlink target %q -> %q must not be absolute", hdr.Name, hdr.Linkname)
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(dst), filepath.FromSlash(linkRaw)))
			if !pmIsSubpathOrEqual(absStage, resolvedTarget) {
				return fmt.Errorf("archive symlink %q -> %q escapes extraction root", hdr.Name, hdr.Linkname)
			}
			if err := verifyExistingAncestorWithinStage(dst); err != nil {
				return err
			}
			realParent, _ := filepath.EvalSymlinks(filepath.Dir(dst))
			if realParent != "" {
				resolvedRealTarget := filepath.Clean(filepath.Join(realParent, filepath.FromSlash(linkRaw)))
				if !pmIsSubpathOrEqual(realStage, resolvedRealTarget) {
					return fmt.Errorf("archive symlink %q -> %q escapes real extraction root", hdr.Name, hdr.Linkname)
				}
			}
			if _, lerr := os.Lstat(dst); lerr == nil {
				_ = os.Remove(dst)
			}
			if err := os.Symlink(filepath.FromSlash(linkRaw), dst); err != nil {
				return err
			}
			if resolvedLink, rerr := filepath.EvalSymlinks(dst); rerr == nil && !pmIsSubpathOrEqual(realStage, resolvedLink) {
				_ = os.Remove(dst)
				return fmt.Errorf("archive symlink %q resolves outside real extraction root", hdr.Name)
			}
		case tar.TypeLink:
			linkRel, linkSkip, err := pmSanitizeTarEntryPath(hdr.Linkname)
			if err != nil || linkSkip {
				return fmt.Errorf("archive hardlink target invalid: %q -> %q", hdr.Name, hdr.Linkname)
			}
			linkSrc := filepath.Join(absStage, filepath.FromSlash(linkRel))
			if !pmIsSubpathOrEqual(absStage, linkSrc) {
				return fmt.Errorf("archive hardlink %q -> %q escapes extraction root", hdr.Name, hdr.Linkname)
			}
			realLinkSrc, err := filepath.EvalSymlinks(linkSrc)
			if err != nil || !pmIsSubpathOrEqual(realStage, realLinkSrc) {
				return fmt.Errorf("archive hardlink %q -> %q does not stay within extraction root", hdr.Name, hdr.Linkname)
			}
			if err := verifyExistingAncestorWithinStage(dst); err != nil {
				return err
			}
			if _, lerr := os.Lstat(dst); lerr == nil {
				_ = os.Remove(dst)
			}
			if err := os.Link(realLinkSrc, dst); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry type %q for %q", hdr.Typeflag, hdr.Name)
		}
	}

	// Post-extract audit ensures multi-hop symlink chains never resolve outside realStage.
	return filepath.WalkDir(realStage, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(p)
			if err == nil && !pmIsSubpathOrEqual(realStage, resolved) {
				return fmt.Errorf("extracted symlink %q resolves outside package root: %s", p, resolved)
			}
		}
		return nil
	})
}

func (pm *DefaultPackageManager) uninstallNpm(parsed pmParsedSource, scope utils.SourceScope) error {
	installRoot, err := pm.npmInstallRoot(scope, false)
	if err != nil {
		return err
	}
	if !pmPathExists(installRoot) {
		return nil
	}
	if err := pmValidateNpmPackageName(parsed.name); err != nil {
		return err
	}
	targetDir, err := pmResolveManagedPath(filepath.Join(installRoot, "node_modules"), strings.Split(parsed.name, "/")...)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	// Clean empty scope parent directory (e.g. node_modules/@scope).
	if strings.HasPrefix(parsed.name, "@") {
		scopeDir := filepath.Dir(targetDir)
		if entries, err := os.ReadDir(scopeDir); err == nil && len(entries) == 0 {
			_ = os.Remove(scopeDir)
		}
	}
	return pmUpdateNpmRootPackageJSON(installRoot, parsed.name, "", true)
}

// Git Source Operations via xspawn

func pmGitCommandEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_TERMINAL_PROMPT=") {
			out = append(out, "GIT_TERMINAL_PROMPT=0")
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, "GIT_TERMINAL_PROMPT=0")
	}
	return out
}

func pmRunGit(ctx context.Context, cwd string, timeout time.Duration, args ...string) (string, error) {
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := xspawn.CommandIn(runCtx, cwd, pmGitCommandEnv(), "git", args)
	outBytes, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(outBytes))
	if err != nil {
		err = xspawn.VerifyENOENTSync(cmd, err)
		if out != "" {
			return "", fmt.Errorf("git %s failed (%w): %s", strings.Join(args, " "), err, out)
		}
		return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func (pm *DefaultPackageManager) getGitHead(ctx context.Context, targetDir string) string {
	if !pmPathExists(targetDir) {
		return ""
	}
	head, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return head
}

func (pm *DefaultPackageManager) getGitUpstreamRef(ctx context.Context, installedPath string) string {
	upstream, err := pmRunGit(ctx, installedPath, pmNetworkTimeout, "rev-parse", "--abbrev-ref", "@{upstream}")
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(upstream, "origin/") {
		return ""
	}
	branch := strings.TrimPrefix(upstream, "origin/")
	if branch == "" {
		return ""
	}
	return "refs/heads/" + branch
}

func (pm *DefaultPackageManager) getRemoteGitHead(ctx context.Context, installedPath string) (string, error) {
	if upstreamRef := pm.getGitUpstreamRef(ctx, installedPath); upstreamRef != "" {
		if out, err := pmRunGit(ctx, installedPath, pmNetworkTimeout, "ls-remote", "origin", upstreamRef); err == nil {
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && pmCommitHashRe.MatchString(fields[0]) {
					return fields[0], nil
				}
			}
		}
	}
	out, err := pmRunGit(ctx, installedPath, pmNetworkTimeout, "ls-remote", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "HEAD" && len(fields[0]) == 40 {
			return fields[0], nil
		}
	}
	return "", errors.New("Failed to determine remote HEAD")
}

func (pm *DefaultPackageManager) gitHasAvailableUpdate(ctx context.Context, installedPath string) (localHead, remoteHead string, hasUpdate bool) {
	if IsOfflineModeEnabled() {
		return "", "", false
	}
	localHead = pm.getGitHead(ctx, installedPath)
	if localHead == "" {
		return "", "", false
	}
	remoteHead, err := pm.getRemoteGitHead(ctx, installedPath)
	if err != nil {
		return localHead, "", false
	}
	return localHead, remoteHead, localHead != remoteHead
}

type pmGitUpdateTarget struct {
	ref       string
	fetchArgs []string
}

func (pm *DefaultPackageManager) getLocalGitUpdateTarget(ctx context.Context, installedPath string) pmGitUpdateTarget {
	if upstream, err := pmRunGit(ctx, installedPath, pmNetworkTimeout, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		if strings.HasPrefix(upstream, "origin/") {
			branch := strings.TrimPrefix(upstream, "origin/")
			if branch != "" {
				return pmGitUpdateTarget{
					ref:       "@{upstream}",
					fetchArgs: []string{"fetch", "--prune", "--no-tags", "origin", fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)},
				}
			}
		}
	}
	_, _ = pmRunGit(ctx, installedPath, pmNetworkTimeout, "remote", "set-head", "origin", "-a")
	if originHeadRef, err := pmRunGit(ctx, installedPath, pmNetworkTimeout, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		branch := strings.TrimPrefix(originHeadRef, "refs/remotes/origin/")
		if branch != "" {
			return pmGitUpdateTarget{
				ref:       "origin/HEAD",
				fetchArgs: []string{"fetch", "--prune", "--no-tags", "origin", fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)},
			}
		}
	}
	return pmGitUpdateTarget{
		ref:       "origin/HEAD",
		fetchArgs: []string{"fetch", "--prune", "--no-tags", "origin", "+HEAD:refs/remotes/origin/HEAD"},
	}
}

func pmGitUpdateMarkerPath(targetDir string) string {
	return filepath.Join(filepath.Dir(targetDir), "."+filepath.Base(targetDir)+".pi-update-incomplete")
}

func pmReadPackageDependencies(targetDir string) (*omap.Map[string, string], bool) {
	pkgPath := filepath.Join(targetDir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, false
	}
	var manifest struct {
		Dependencies *omap.Map[string, string] `json:"dependencies"`
	}
	if err := jsonx.Unmarshal([]byte(utils.StripBom(string(raw))), &manifest); err != nil {
		return nil, false
	}
	if manifest.Dependencies == nil || manifest.Dependencies.Len() == 0 {
		return nil, false
	}
	return manifest.Dependencies, true
}

func (pm *DefaultPackageManager) installGitDependencies(ctx context.Context, targetDir string) error {
	deps, hasDeps := pmReadPackageDependencies(targetDir)
	if !hasDeps {
		return nil
	}
	nodeModulesRoot := filepath.Join(targetDir, "node_modules")
	if err := os.MkdirAll(nodeModulesRoot, 0o755); err != nil {
		return err
	}
	visited := make(map[string]struct{})
	depNames := deps.Keys()
	slices.SortFunc(depNames, js.CompareUTF16)
	for _, depName := range depNames {
		depRange, _ := deps.Get(depName)
		_, rngOk := semver.ValidRange(depRange)
		depSemverRange := ""
		if rngOk {
			depSemverRange = depRange
		}
		if _, err := pm.installNpmPackageIntoNodeModules(ctx, depName, depRange, depSemverRange, nodeModulesRoot, visited); err != nil {
			return err
		}
	}
	return nil
}

func (pm *DefaultPackageManager) hasMissingGitDependencies(targetDir string) bool {
	deps, hasDeps := pmReadPackageDependencies(targetDir)
	if !hasDeps {
		return false
	}
	nodeModulesDir := filepath.Join(targetDir, "node_modules")
	for _, name := range deps.Keys() {
		if err := pmValidateNpmPackageName(name); err != nil {
			continue
		}
		depPath, err := pmResolveManagedPath(nodeModulesDir, strings.Split(name, "/")...)
		if err != nil {
			continue
		}
		if !pmPathExists(depPath) {
			return true
		}
	}
	return false
}

func (pm *DefaultPackageManager) cleanAndInstallGitDependencies(ctx context.Context, targetDir, markerPath string) error {
	cleaned := true
	if _, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "clean", "-fdx"); err != nil {
		cleaned = false
		pm.emitProgress(ProgressEvent{
			Type:    "progress",
			Action:  "update",
			Source:  targetDir,
			Message: fmt.Sprintf("Could not remove all untracked files in %s (%s). Files may be in use by another pi session; close it and run the update again.", targetDir, err.Error()),
		})
	}
	if err := pm.installGitDependencies(ctx, targetDir); err != nil {
		return err
	}
	if cleaned {
		_ = os.Remove(markerPath)
	}
	return nil
}

func (pm *DefaultPackageManager) ensureGitRef(ctx context.Context, targetDir string, fetchArgs []string, ref string) error {
	if _, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, fetchArgs...); err != nil {
		return err
	}
	localHead, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	commitRef := ref + "^{commit}"
	targetHead, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "rev-parse", commitRef)
	if err != nil {
		return err
	}
	markerPath := pmGitUpdateMarkerPath(targetDir)
	if localHead == targetHead {
		if pmPathExists(markerPath) {
			return pm.cleanAndInstallGitDependencies(ctx, targetDir, markerPath)
		}
		if pm.hasMissingGitDependencies(targetDir) {
			return pm.installGitDependencies(ctx, targetDir)
		}
		return nil
	}
	_ = os.WriteFile(markerPath, []byte(""), 0o644)
	if _, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "reset", "--hard", commitRef); err != nil {
		return err
	}
	return pm.cleanAndInstallGitDependencies(ctx, targetDir, markerPath)
}

func (pm *DefaultPackageManager) installGit(ctx context.Context, parsed pmParsedSource, scope utils.SourceScope) error {
	targetDir, err := pm.gitInstallPath(parsed, scope)
	if err != nil {
		return err
	}
	if pmPathExists(targetDir) {
		if parsed.ref != "" {
			return pm.ensureGitRef(ctx, targetDir, []string{"fetch", "origin", parsed.ref}, "FETCH_HEAD")
		}
		target := pm.getLocalGitUpdateTarget(ctx, targetDir)
		return pm.ensureGitRef(ctx, targetDir, target.fetchArgs, target.ref)
	}
	if IsOfflineModeEnabled() {
		return fmt.Errorf("cannot clone %s in offline mode", parsed.repo)
	}
	gitRoot, err := pm.gitInstallRoot(scope)
	if err != nil {
		return err
	}
	if gitRoot != "" {
		pmEnsureGitIgnoreDir(gitRoot)
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return err
	}
	_ = os.Remove(pmGitUpdateMarkerPath(targetDir))

	cleanupOnError := func() {
		_ = os.RemoveAll(targetDir)
		pmPruneEmptyGitParents(targetDir, gitRoot)
	}
	if _, err := pmRunGit(ctx, "", pmNetworkTimeout*2, "clone", parsed.repo, targetDir); err != nil {
		cleanupOnError()
		return err
	}
	if parsed.ref != "" {
		if _, err := pmRunGit(ctx, targetDir, pmNetworkTimeout, "checkout", parsed.ref); err != nil {
			cleanupOnError()
			return err
		}
	}
	if err := pm.installGitDependencies(ctx, targetDir); err != nil {
		cleanupOnError()
		return err
	}
	return nil
}

func (pm *DefaultPackageManager) updateGit(ctx context.Context, parsed pmParsedSource, scope utils.SourceScope) error {
	targetDir, err := pm.gitInstallPath(parsed, scope)
	if err != nil {
		return err
	}
	if !pmPathExists(targetDir) {
		return pm.installGit(ctx, parsed, scope)
	}
	if parsed.ref != "" {
		return pm.ensureGitRef(ctx, targetDir, []string{"fetch", "origin", parsed.ref}, "FETCH_HEAD")
	}
	target := pm.getLocalGitUpdateTarget(ctx, targetDir)
	return pm.ensureGitRef(ctx, targetDir, target.fetchArgs, target.ref)
}

func (pm *DefaultPackageManager) refreshTemporaryGitSource(ctx context.Context, parsed pmParsedSource, sourceStr, installedPath string) {
	if IsOfflineModeEnabled() {
		return
	}
	if parsed.ref != "" && pmCommitHashRe.MatchString(parsed.ref) {
		head := pm.getGitHead(ctx, installedPath)
		if head != "" && strings.HasPrefix(strings.ToLower(head), strings.ToLower(parsed.ref)) {
			return
		}
	}
	_ = pm.withProgress("pull", sourceStr, fmt.Sprintf("Refreshing %s...", sourceStr), func() error {
		return pm.updateGit(ctx, parsed, utils.SourceScopeTemporary)
	})
}

func (pm *DefaultPackageManager) removeGit(parsed pmParsedSource, scope utils.SourceScope) error {
	targetDir, err := pm.gitInstallPath(parsed, scope)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	_ = os.Remove(pmGitUpdateMarkerPath(targetDir))
	if gitRoot, err := pm.gitInstallRoot(scope); err == nil {
		pmPruneEmptyGitParents(targetDir, gitRoot)
	}
	return nil
}

func pmPruneEmptyGitParents(targetDir, installRoot string) {
	if installRoot == "" {
		return
	}
	resolvedRoot, err := filepath.Abs(installRoot)
	if err != nil {
		return
	}
	current := filepath.Dir(targetDir)
	for pmIsPathInside(resolvedRoot, current) {
		if !pmPathExists(current) {
			current = filepath.Dir(current)
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(current); err != nil {
			break
		}
		current = filepath.Dir(current)
	}
}

// Ignore files, globs, pattern filters, and resource discovery

func pmPrefixIgnorePattern(line, prefix string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	if strings.HasPrefix(trimmed, "!") {
		inner := pmPrefixIgnorePattern(trimmed[1:], prefix)
		if inner == "" {
			return ""
		}
		return "!" + inner
	}
	var pat string
	if strings.HasPrefix(trimmed, "\\#") || strings.HasPrefix(trimmed, "\\!") {
		pat = trimmed[1:]
	} else {
		pat = trimmed
	}
	if prefix == "" {
		return pat
	}
	if strings.HasPrefix(pat, "/") {
		return prefix + pat
	}
	if strings.Contains(strings.TrimSuffix(pat, "/"), "/") {
		return prefix + "/" + pat
	}
	return prefix + "/**/" + pat
}

func pmAddIgnoreRules(ig *ignore.Ignore, dir, root string) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return
	}
	prefix := filepath.ToSlash(rel)
	if prefix == "." {
		prefix = ""
	}
	for _, ignoreFile := range pmIgnoreFileNames {
		p := filepath.Join(dir, ignoreFile)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimRight(line, "\r")
			if prefix == "" {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				ig.Add(line)
			} else {
				prefixed := pmPrefixIgnorePattern(line, prefix)
				if prefixed != "" {
					ig.Add(prefixed)
				}
			}
		}
	}
}

func pmSafeIgnoreCheck(ig *ignore.Ignore, root, fullPath string, isDir bool) bool {
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return false
	}
	relPosix := filepath.ToSlash(rel)
	for strings.HasPrefix(relPosix, "./") {
		relPosix = relPosix[2:]
	}
	if relPosix == "" || relPosix == "." || strings.HasPrefix(relPosix, "../") || strings.HasPrefix(relPosix, "/") {
		return false
	}
	if isDir {
		relPosix += "/"
	}
	return ig.Ignores(relPosix)
}

func pmStatEntry(dir, name string, entry os.DirEntry) (fullPath string, isDir, isFile bool, ok bool) {
	fullPath = filepath.Join(dir, name)
	if entry.Type()&os.ModeSymlink != 0 {
		info, err := os.Stat(fullPath)
		if err != nil {
			return fullPath, false, false, false
		}
		return fullPath, info.IsDir(), !info.IsDir(), true
	}
	return fullPath, entry.IsDir(), entry.Type().IsRegular(), true
}

func pmIsPattern(s string) bool {
	return strings.HasPrefix(s, "!") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
}

func pmHasGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?{") || strings.Contains(s, "[")
}

func pmSplitPatterns(entries []string) (plain, patterns []string) {
	for _, entry := range entries {
		if pmIsPattern(entry) {
			patterns = append(patterns, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	return plain, patterns
}

func pmGetOverridePatterns(entries []string) []string {
	var out []string
	for _, entry := range entries {
		if pmIsPattern(entry) {
			out = append(out, entry)
		}
	}
	return out
}

func pmNormalizePatternPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return p
}

func pmMatchesPattern(filePath, pattern, baseDir string) bool {
	filePosix := filepath.ToSlash(filePath)
	relPath := filePosix
	if rel, err := filepath.Rel(baseDir, filePath); err == nil {
		relPath = filepath.ToSlash(rel)
	}
	fileName := path.Base(filePosix)
	normPat := pmNormalizePatternPath(pattern)

	isSkillFile := fileName == "SKILL.md"
	parentDir := path.Dir(filePosix)
	parentRel := path.Dir(relPath)
	parentName := path.Base(parentDir)

	if !pmHasGlobPattern(normPat) {
		normPatTrimmed := strings.TrimSuffix(normPat, "/")
		if normPatTrimmed == relPath || normPatTrimmed == fileName || normPatTrimmed == filePosix {
			return true
		}
		if resolved, err := filepath.Abs(filepath.Join(baseDir, normPatTrimmed)); err == nil {
			if filepath.ToSlash(resolved) == filePosix {
				return true
			}
		}
		if strings.HasPrefix(relPath, normPatTrimmed+"/") || strings.HasPrefix(filePosix, normPatTrimmed+"/") {
			return true
		}
		if isSkillFile && (normPatTrimmed == parentRel || normPatTrimmed == parentName || normPatTrimmed == parentDir) {
			return true
		}
		return false
	}

	opts := minimatch.Options{Dot: true}
	if minimatch.Match(relPath, normPat, opts) || minimatch.Match(fileName, normPat, opts) || minimatch.Match(filePosix, normPat, opts) {
		return true
	}
	if isSkillFile && (minimatch.Match(parentRel, normPat, opts) || minimatch.Match(parentName, normPat, opts) || minimatch.Match(parentDir, normPat, opts)) {
		return true
	}
	return false
}

func pmApplyPatterns(allPaths, patterns []string, baseDir string) map[string]struct{} {
	includes := make([]string, 0)
	excludes := make([]string, 0)
	forceIncludes := make([]string, 0)
	forceExcludes := make([]string, 0)

	for _, p := range patterns {
		switch {
		case strings.HasPrefix(p, "!"):
			forceExcludes = append(forceExcludes, p[1:])
		case strings.HasPrefix(p, "+"):
			forceIncludes = append(forceIncludes, p[1:])
		case strings.HasPrefix(p, "-"):
			excludes = append(excludes, p[1:])
		default:
			includes = append(includes, p)
		}
	}

	result := make(map[string]struct{})
	if len(includes) == 0 {
		for _, p := range allPaths {
			result[p] = struct{}{}
		}
	} else {
		for _, p := range allPaths {
			for _, inc := range includes {
				if pmMatchesPattern(p, inc, baseDir) {
					result[p] = struct{}{}
					break
				}
			}
		}
	}

	for _, p := range allPaths {
		for _, exc := range excludes {
			if pmMatchesPattern(p, exc, baseDir) {
				delete(result, p)
				break
			}
		}
		for _, fInc := range forceIncludes {
			if pmMatchesPattern(p, fInc, baseDir) {
				result[p] = struct{}{}
				break
			}
		}
		for _, fExc := range forceExcludes {
			if pmMatchesPattern(p, fExc, baseDir) {
				delete(result, p)
				break
			}
		}
	}

	return result
}

func pmApplyAutoloadDisabledPatterns(allPaths, patterns []string, baseDir string) map[string]bool {
	result := make(map[string]bool)
	var overrides []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "+") || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "!") {
			overrides = append(overrides, p)
		}
	}
	for _, p := range allPaths {
		for _, raw := range overrides {
			pat := raw[1:]
			if !pmMatchesPattern(p, pat, baseDir) {
				continue
			}
			result[p] = strings.HasPrefix(raw, "+")
		}
	}
	return result
}

func pmIsEnabledByOverrides(filePath string, patterns []string, baseDir string) bool {
	_, overrides := pmSplitPatterns(patterns)
	enabled := true
	for _, p := range overrides {
		pat := strings.TrimLeft(p, "+-!")
		if !pmMatchesPattern(filePath, pat, baseDir) {
			continue
		}
		if strings.HasPrefix(p, "+") {
			enabled = true
		} else {
			enabled = false
		}
	}
	return enabled
}

func pmExpandPackageGlob(pattern, root string) []string {
	normalized := pmNormalizePatternPath(pattern)
	if normalized == "" || strings.HasPrefix(normalized, "!") {
		return nil
	}
	dirOnly := strings.HasSuffix(normalized, "/")
	cleanPat := strings.TrimSuffix(normalized, "/")

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}

	var matches []string
	visited := make(map[string]struct{})

	var walk func(cur string)
	walk = func(cur string) {
		canon := utils.CanonicalizePath(cur)
		if _, seen := visited[canon]; seen {
			return
		}
		visited[canon] = struct{}{}

		entries, err := os.ReadDir(cur)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			full, isDir, isFile, ok := pmStatEntry(cur, name, entry)
			if !ok || (!isDir && !isFile) {
				continue
			}
			rel, rerr := filepath.Rel(absRoot, full)
			if rerr != nil {
				continue
			}
			relPosix := filepath.ToSlash(rel)
			hasHiddenSeg := false
			for _, seg := range strings.Split(relPosix, "/") {
				if strings.HasPrefix(seg, ".") {
					hasHiddenSeg = true
					break
				}
			}
			if hasHiddenSeg {
				continue
			}

			if (!dirOnly || isDir) && minimatch.Match(relPosix, cleanPat, minimatch.Options{Dot: false}) {
				matches = append(matches, full)
			}
			if isDir {
				walk(full)
			}
		}
	}
	walk(absRoot)

	slices.SortFunc(matches, js.CompareUTF16)
	return matches
}

type pmSkillMode string

const (
	pmSkillModePi     pmSkillMode = "pi"
	pmSkillModeAgents pmSkillMode = "agents"
)

func pmCollectSkillEntries(dir string, mode pmSkillMode) []string {
	var entries []string
	if !pmPathExists(dir) {
		return entries
	}
	ig := ignore.New()
	visited := make(map[string]struct{})

	var walk func(cur string)
	walk = func(cur string) {
		canon := utils.CanonicalizePath(cur)
		if _, seen := visited[canon]; seen {
			return
		}
		visited[canon] = struct{}{}

		pmAddIgnoreRules(ig, cur, dir)
		dirEntries, err := os.ReadDir(cur)
		if err != nil {
			return
		}
		slices.SortFunc(dirEntries, func(a, b os.DirEntry) int {
			return js.CompareUTF16(a.Name(), b.Name())
		})

		for _, entry := range dirEntries {
			if entry.Name() != "SKILL.md" {
				continue
			}
			full, _, isFile, ok := pmStatEntry(cur, entry.Name(), entry)
			if !ok || !isFile {
				continue
			}
			if !pmSafeIgnoreCheck(ig, dir, full, false) {
				entries = append(entries, full)
				return
			}
		}

		for _, entry := range dirEntries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			full, isDir, isFile, ok := pmStatEntry(cur, name, entry)
			if !ok {
				continue
			}
			shouldIncludeMd := isFile && strings.HasSuffix(name, ".md") &&
				!pmSafeIgnoreCheck(ig, dir, full, false) &&
				((mode == pmSkillModePi && cur == dir) || (mode == pmSkillModeAgents && cur != dir))
			if shouldIncludeMd {
				entries = append(entries, full)
				continue
			}
			if !isDir {
				continue
			}
			if pmSafeIgnoreCheck(ig, dir, full, true) {
				continue
			}
			walk(full)
		}
	}
	walk(dir)
	return entries
}

func pmFindGitRepoRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if pmPathExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func pmCollectAncestorAgentsSkillDirs(startDir string) []string {
	resolvedStart, err := filepath.Abs(startDir)
	if err != nil {
		return nil
	}
	gitRoot := pmFindGitRepoRoot(resolvedStart)
	var dirs []string
	dir := resolvedStart
	for {
		dirs = append(dirs, filepath.Join(dir, ".agents", "skills"))
		if gitRoot != "" && dir == gitRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func pmCollectAutoFlatEntries(dir, ext string) []string {
	var out []string
	if !pmPathExists(dir) {
		return out
	}
	ig := ignore.New()
	pmAddIgnoreRules(ig, dir, dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	slices.SortFunc(dirEntries, func(a, b os.DirEntry) int {
		return js.CompareUTF16(a.Name(), b.Name())
	})
	for _, entry := range dirEntries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		full, _, isFile, ok := pmStatEntry(dir, name, entry)
		if !ok || !isFile || !strings.HasSuffix(name, ext) {
			continue
		}
		if pmSafeIgnoreCheck(ig, dir, full, false) {
			continue
		}
		out = append(out, full)
	}
	return out
}

func pmResolveExtensionEntries(dir string) []string {
	pkgPath := filepath.Join(dir, "package.json")
	if manifest := ReadPiManifest(pkgPath); manifest != nil && len(manifest.Extensions) > 0 {
		var entries []string
		for _, extPath := range manifest.Extensions {
			resolved := filepath.Join(dir, extPath)
			if pmPathExists(resolved) {
				entries = append(entries, resolved)
			}
		}
		if len(entries) > 0 {
			return entries
		}
	}
	for _, idxName := range []string{"index.ts", "index.js"} {
		idx := filepath.Join(dir, idxName)
		if pmPathExists(idx) {
			return []string{idx}
		}
	}
	return nil
}

func pmCollectAutoExtensionEntries(dir string) []string {
	if resolved := pmResolveExtensionEntries(dir); resolved != nil {
		return resolved
	}
	var out []string
	if !pmPathExists(dir) {
		return out
	}
	ig := ignore.New()
	pmAddIgnoreRules(ig, dir, dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	slices.SortFunc(dirEntries, func(a, b os.DirEntry) int {
		return js.CompareUTF16(a.Name(), b.Name())
	})
	for _, entry := range dirEntries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		full, isDir, isFile, ok := pmStatEntry(dir, name, entry)
		if !ok {
			continue
		}
		if pmSafeIgnoreCheck(ig, dir, full, isDir) {
			continue
		}
		if isFile && pmExtFileRe.MatchString(name) {
			out = append(out, full)
		} else if isDir {
			out = append(out, pmResolveExtensionEntries(full)...)
		}
	}
	return out
}

func pmCollectResourceFiles(dir string, rt pmResourceType) []string {
	switch rt {
	case pmResSkills:
		return pmCollectSkillEntries(dir, pmSkillModePi)
	case pmResExtensions:
		return pmCollectAutoExtensionEntries(dir)
	case pmResPrompts:
		return pmCollectAutoFlatEntries(dir, ".md")
	case pmResThemes:
		return pmCollectAutoFlatEntries(dir, ".json")
	default:
		return nil
	}
}

func pmHasManifestResourceFields(m *PiManifest) bool {
	if m == nil {
		return false
	}
	return m.Extensions != nil || m.Skills != nil || m.Prompts != nil || m.Themes != nil
}

func pmManifestEntries(m *PiManifest, rt pmResourceType) ([]string, bool) {
	if m == nil {
		return nil, false
	}
	switch rt {
	case pmResExtensions:
		return m.Extensions, m.Extensions != nil
	case pmResSkills:
		return m.Skills, m.Skills != nil
	case pmResPrompts:
		return m.Prompts, m.Prompts != nil
	case pmResThemes:
		return m.Themes, m.Themes != nil
	default:
		return nil, false
	}
}

func pmFilterEntries(filter *PackageSourceObject, rt pmResourceType) ([]string, bool) {
	if filter == nil {
		return nil, false
	}
	switch rt {
	case pmResExtensions:
		return filter.Extensions, filter.Extensions != nil
	case pmResSkills:
		return filter.Skills, filter.Skills != nil
	case pmResPrompts:
		return filter.Prompts, filter.Prompts != nil
	case pmResThemes:
		return filter.Themes, filter.Themes != nil
	default:
		return nil, false
	}
}

func pmResolvePackageChangelogPath(packageRoot string, manifest *PiManifest) string {
	changelogRel := "CHANGELOG.md"
	if manifest != nil && manifest.ChangelogPath != nil {
		changelogRel = *manifest.ChangelogPath
	}
	if changelogRel == "" {
		return ""
	}
	resolved := filepath.Join(packageRoot, changelogRel)
	if !filepath.IsAbs(resolved) {
		resolved, _ = filepath.Abs(resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return ""
	}
	canonRoot := utils.CanonicalizePath(packageRoot)
	canonChangelog := utils.CanonicalizePath(resolved)
	if !pmIsPathInside(canonRoot, canonChangelog) {
		return ""
	}
	return resolved
}

func (pm *DefaultPackageManager) collectPackageResources(
	packageRoot string,
	acc *pmResourceAccumulator,
	filter *PackageSourceObject,
	meta PathMetadata,
) bool {
	manifest := ReadPiManifest(filepath.Join(packageRoot, "package.json"))
	meta.ChangelogPath = pmResolvePackageChangelogPath(packageRoot, manifest)

	if filter != nil {
		autoloadDisabled := filter.Autoload != nil && !*filter.Autoload
		for _, rt := range pmResourceTypes {
			patterns, hasField := pmFilterEntries(filter, rt)
			target := acc.target(rt)
			if autoloadDisabled {
				pm.applyPackageDeltaFilter(packageRoot, patterns, rt, target, meta)
			} else if hasField {
				pm.applyPackageFilter(packageRoot, patterns, rt, target, meta)
			} else {
				pm.collectDefaultResources(packageRoot, manifest, rt, target, meta)
			}
		}
		return true
	}

	if manifest != nil && pmHasManifestResourceFields(manifest) {
		for _, rt := range pmResourceTypes {
			if entries, ok := pmManifestEntries(manifest, rt); ok {
				pm.addManifestEntries(entries, packageRoot, rt, acc.target(rt), meta)
			}
		}
		return true
	}

	hasAnyDir := false
	for _, rt := range pmResourceTypes {
		dir := filepath.Join(packageRoot, string(rt))
		if pmPathExists(dir) {
			for _, f := range pmCollectResourceFiles(dir, rt) {
				acc.target(rt).add(f, meta, true)
			}
			hasAnyDir = true
		}
	}
	return hasAnyDir
}

func (pm *DefaultPackageManager) collectDefaultResources(
	packageRoot string,
	manifest *PiManifest,
	rt pmResourceType,
	target *pmResourceMap,
	meta PathMetadata,
) {
	if entries, ok := pmManifestEntries(manifest, rt); ok {
		pm.addManifestEntries(entries, packageRoot, rt, target, meta)
		return
	}
	dir := filepath.Join(packageRoot, string(rt))
	if pmPathExists(dir) {
		for _, f := range pmCollectResourceFiles(dir, rt) {
			target.add(f, meta, true)
		}
	}
}

func (pm *DefaultPackageManager) applyPackageFilter(
	packageRoot string,
	userPatterns []string,
	rt pmResourceType,
	target *pmResourceMap,
	meta PathMetadata,
) {
	allFiles := pm.collectManifestFiles(packageRoot, rt)
	if len(userPatterns) == 0 {
		for _, f := range allFiles {
			target.add(f, meta, false)
		}
		return
	}
	enabledSet := pmApplyPatterns(allFiles, userPatterns, packageRoot)
	for _, f := range allFiles {
		_, enabled := enabledSet[f]
		target.add(f, meta, enabled)
	}
}

func (pm *DefaultPackageManager) applyPackageDeltaFilter(
	packageRoot string,
	userPatterns []string,
	rt pmResourceType,
	target *pmResourceMap,
	meta PathMetadata,
) {
	if len(userPatterns) == 0 {
		return
	}
	allFiles := pm.collectManifestFiles(packageRoot, rt)
	deltaMap := pmApplyAutoloadDisabledPatterns(allFiles, userPatterns, packageRoot)
	for _, f := range allFiles {
		if enabled, ok := deltaMap[f]; ok {
			target.add(f, meta, enabled)
		}
	}
}

func (pm *DefaultPackageManager) collectManifestFiles(packageRoot string, rt pmResourceType) []string {
	manifest := ReadPiManifest(filepath.Join(packageRoot, "package.json"))
	if entries, ok := pmManifestEntries(manifest, rt); ok && len(entries) > 0 {
		allFiles := pm.collectFilesFromManifestEntries(entries, packageRoot, rt)
		manifestPatterns := pmGetOverridePatterns(entries)
		if len(manifestPatterns) == 0 {
			return allFiles
		}
		enabled := pmApplyPatterns(allFiles, manifestPatterns, packageRoot)
		out := make([]string, 0, len(enabled))
		for _, f := range allFiles {
			if _, keep := enabled[f]; keep {
				out = append(out, f)
			}
		}
		return out
	}
	conventionDir := filepath.Join(packageRoot, string(rt))
	if !pmPathExists(conventionDir) {
		return nil
	}
	return pmCollectResourceFiles(conventionDir, rt)
}

func (pm *DefaultPackageManager) addManifestEntries(
	entries []string,
	root string,
	rt pmResourceType,
	target *pmResourceMap,
	meta PathMetadata,
) {
	allFiles := pm.collectFilesFromManifestEntries(entries, root, rt)
	patterns := pmGetOverridePatterns(entries)
	enabledSet := pmApplyPatterns(allFiles, patterns, root)
	for _, f := range allFiles {
		if _, ok := enabledSet[f]; ok {
			target.add(f, meta, true)
		}
	}
}

func (pm *DefaultPackageManager) collectFilesFromManifestEntries(entries []string, root string, rt pmResourceType) []string {
	var resolved []string
	for _, entry := range entries {
		if pmIsPattern(entry) {
			continue
		}
		if !pmHasGlobPattern(entry) {
			resolved = append(resolved, filepath.Join(root, filepath.FromSlash(entry)))
		} else {
			resolved = append(resolved, pmExpandPackageGlob(entry, root)...)
		}
	}
	return pmCollectFilesFromPaths(resolved, rt)
}

func pmCollectFilesFromPaths(paths []string, rt pmResourceType) []string {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			files = append(files, p)
		} else {
			files = append(files, pmCollectResourceFiles(p, rt)...)
		}
	}
	return files
}

func (pm *DefaultPackageManager) resolveLocalEntries(
	entries []string,
	rt pmResourceType,
	target *pmResourceMap,
	meta PathMetadata,
	baseDir string,
) {
	if len(entries) == 0 {
		return
	}
	plain, patterns := pmSplitPatterns(entries)
	resolvedPlain := make([]string, 0, len(plain))
	for _, p := range plain {
		resolvedPlain = append(resolvedPlain, pm.resolvePathFromBase(p, baseDir))
	}
	allFiles := pmCollectFilesFromPaths(resolvedPlain, rt)
	enabledSet := pmApplyPatterns(allFiles, patterns, baseDir)
	for _, f := range allFiles {
		_, enabled := enabledSet[f]
		target.add(f, meta, enabled)
	}
}

func (pm *DefaultPackageManager) addAutoDiscoveredResources(
	acc *pmResourceAccumulator,
	globalSettings, projectSettings Settings,
	globalBaseDir, projectBaseDir string,
) {
	userMeta := PathMetadata{
		Source:  "auto",
		Scope:   utils.SourceScopeUser,
		Origin:  utils.SourceOriginTopLevel,
		BaseDir: globalBaseDir,
	}
	projectMeta := PathMetadata{
		Source:  "auto",
		Scope:   utils.SourceScopeProject,
		Origin:  utils.SourceOriginTopLevel,
		BaseDir: projectBaseDir,
	}

	home, _ := os.UserHomeDir()
	userAgentsSkillsDir := filepath.Join(home, ".agents", "skills")
	projectTrusted := pm.settingsManager.IsProjectTrusted()

	var projectAgentsSkillDirs []string
	if projectTrusted {
		for _, dir := range pmCollectAncestorAgentsSkillDirs(pm.cwd) {
			if !utils.PathsEqual(dir, userAgentsSkillsDir) {
				projectAgentsSkillDirs = append(projectAgentsSkillDirs, dir)
			}
		}
	}

	addResources := func(rt pmResourceType, paths []string, meta PathMetadata, overrides []string, baseDir string) {
		target := acc.target(rt)
		for _, p := range paths {
			target.add(p, meta, pmIsEnabledByOverrides(p, overrides, baseDir))
		}
	}

	if projectTrusted {
		addResources(pmResExtensions, pmCollectAutoExtensionEntries(filepath.Join(projectBaseDir, "extensions")), projectMeta, projectSettings.Extensions, projectBaseDir)
		addResources(pmResSkills, pmCollectSkillEntries(filepath.Join(projectBaseDir, "skills"), pmSkillModePi), projectMeta, projectSettings.Skills, projectBaseDir)
	}

	for _, agentsSkillsDir := range projectAgentsSkillDirs {
		agentsBaseDir := filepath.Dir(agentsSkillsDir)
		agentsMeta := projectMeta
		agentsMeta.BaseDir = agentsBaseDir
		addResources(pmResSkills, pmCollectSkillEntries(agentsSkillsDir, pmSkillModeAgents), agentsMeta, projectSettings.Skills, agentsBaseDir)
	}

	if projectTrusted {
		addResources(pmResPrompts, pmCollectAutoFlatEntries(filepath.Join(projectBaseDir, "prompts"), ".md"), projectMeta, projectSettings.Prompts, projectBaseDir)
		addResources(pmResThemes, pmCollectAutoFlatEntries(filepath.Join(projectBaseDir, "themes"), ".json"), projectMeta, projectSettings.Themes, projectBaseDir)
	}

	addResources(pmResExtensions, pmCollectAutoExtensionEntries(filepath.Join(globalBaseDir, "extensions")), userMeta, globalSettings.Extensions, globalBaseDir)
	addResources(pmResSkills, pmCollectSkillEntries(filepath.Join(globalBaseDir, "skills"), pmSkillModePi), userMeta, globalSettings.Skills, globalBaseDir)

	userAgentsBaseDir := filepath.Dir(userAgentsSkillsDir)
	userAgentsMeta := userMeta
	userAgentsMeta.BaseDir = userAgentsBaseDir
	addResources(pmResSkills, pmCollectSkillEntries(userAgentsSkillsDir, pmSkillModeAgents), userAgentsMeta, globalSettings.Skills, userAgentsBaseDir)

	addResources(pmResPrompts, pmCollectAutoFlatEntries(filepath.Join(globalBaseDir, "prompts"), ".md"), userMeta, globalSettings.Prompts, globalBaseDir)
	addResources(pmResThemes, pmCollectAutoFlatEntries(filepath.Join(globalBaseDir, "themes"), ".json"), userMeta, globalSettings.Themes, globalBaseDir)
}
