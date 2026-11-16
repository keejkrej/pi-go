// Ported from packages/coding-agent/src/core/resource-loader.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/minimatch"
	"github.com/keejkrej/pi-go/tui"
)

// SystemPromptSource identifies the file path backing a loaded system prompt or append snippet.
type SystemPromptSource struct {
	Path string `json:"path"`
}

// LoadContextFilesResult holds discovered AGENTS.md / CLAUDE.md context files.
type LoadContextFilesResult struct {
	AgentsFiles []ContextFile `json:"agentsFiles"`
}

// LoadThemesResult holds loaded custom themes and any diagnostics/collisions.
type LoadThemesResult struct {
	Themes      []*theme.Theme       `json:"themes"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

// ResourceExtensionPath pairs a resource directory or file path with source provenance.
type ResourceExtensionPath struct {
	Path     string             `json:"path"`
	Metadata utils.PathMetadata `json:"metadata"`
}

// ResourceExtensionPaths provides additional resource paths supplied dynamically via ExtendResources.
type ResourceExtensionPaths struct {
	SkillPaths  []ResourceExtensionPath `json:"skillPaths,omitzero"`
	PromptPaths []ResourceExtensionPath `json:"promptPaths,omitzero"`
	ThemePaths  []ResourceExtensionPath `json:"themePaths,omitzero"`
}

// ResolveProjectTrustInput is passed to ResolveProjectTrust callbacks during Reload.
type ResolveProjectTrustInput struct {
	ExtensionsResult LoadExtensionsResult `json:"extensionsResult"`
}

// ResourceLoaderReloadOptions configures DefaultResourceLoader.Reload.
type ResourceLoaderReloadOptions struct {
	ResolveProjectTrust func(ctx context.Context, input ResolveProjectTrustInput) (bool, error)
}

// ResourceLoader is the resource discovery and override contract used by AgentSession.
type ResourceLoader interface {
	Reload(ctx context.Context, options ...*ResourceLoaderReloadOptions) error
	GetExtensions() LoadExtensionsResult
	GetSkills() LoadSkillsResult
	GetPrompts() LoadPromptTemplatesResult
	GetThemes() LoadThemesResult
	GetAgentsFiles() LoadContextFilesResult
	GetSystemPrompt() *string
	GetSystemPromptSource() *SystemPromptSource
	GetAppendSystemPrompt() []string
	GetAppendSystemPromptSources() []SystemPromptSource
	ExtendResources(paths ResourceExtensionPaths)
	GetPathMetadata() map[string]utils.PathMetadata
}

// LoadProjectContextFilesOptions configures LoadProjectContextFiles.
type LoadProjectContextFilesOptions struct {
	Cwd      string
	AgentDir string
}

var rlContextFileNames = []string{
	"AGENTS.override.md",
	"AGENTS.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"CLAUDE.MD",
}

func rlFindContextFileInDir(dir string) *ContextFile {
	for _, filename := range rlContextFileNames {
		candidate := filepath.Join(dir, filename)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		return &ContextFile{
			Path:    candidate,
			Content: string(data),
		}
	}
	return nil
}

func rlDetectNestedWorktreeMainRoot(ancestorDirs []string) (mainRoot string, wtFilename string) {
	// Walk from deepest dir (cwd) upward looking for a linked worktree `.git` file.
	for i := len(ancestorDirs) - 1; i >= 0; i-- {
		dir := ancestorDirs[i]
		gitEntry := filepath.Join(dir, ".git")
		info, err := os.Stat(gitEntry)
		if err != nil || info.IsDir() {
			continue
		}
		raw, err := os.ReadFile(gitEntry)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "gitdir:") {
			continue
		}
		gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if gitDir == "" {
			continue
		}
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(dir, gitDir)
		}
		gitDir = filepath.Clean(gitDir)
		commonRaw, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
		if err != nil {
			continue
		}
		commonRel := strings.TrimSpace(string(commonRaw))
		if commonRel == "" {
			continue
		}
		commonGitDir := commonRel
		if !filepath.IsAbs(commonGitDir) {
			commonGitDir = filepath.Join(gitDir, commonGitDir)
		}
		commonGitDir = filepath.Clean(commonGitDir)
		// Only skip duplicates when commonGitDir is an actual `.git` dir inside a working tree
		// (not e.g. `proj/.bare`), with a HEAD file.
		if filepath.Base(commonGitDir) != ".git" {
			continue
		}
		commonStat, err := os.Stat(commonGitDir)
		if err != nil || !commonStat.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(commonGitDir, "HEAD")); err != nil {
			continue
		}
		candidateMain := filepath.Dir(commonGitDir)
		// Check if candidateMain is a strict ancestor of dir.
		for j := 0; j < i; j++ {
			if utils.PathsEqual(ancestorDirs[j], candidateMain) {
				if wtFile := rlFindContextFileInDir(dir); wtFile != nil {
					return candidateMain, filepath.Base(wtFile.Path)
				}
				return "", ""
			}
		}
		return "", ""
	}
	return "", ""
}

// LoadProjectContextFiles loads global (agentDir) and ancestor hierarchy AGENTS.md / CLAUDE.md files,
// preferring AGENTS.override.md within each directory and skipping nested worktree duplicates.
func LoadProjectContextFiles(options ...*LoadProjectContextFilesOptions) []ContextFile {
	var opts LoadProjectContextFilesOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	cwd := opts.Cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}

	var reversed []string
	cur := filepath.Clean(cwd)
	for cur != "" {
		reversed = append(reversed, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	ancestorDirs := make([]string, len(reversed))
	for i := range reversed {
		ancestorDirs[i] = reversed[len(reversed)-1-i]
	}

	skipMainRoot, skipMainFilename := rlDetectNestedWorktreeMainRoot(ancestorDirs)

	var out []ContextFile
	seenRealPaths := make(map[string]struct{})
	addIfUnique := func(cf *ContextFile) {
		if cf == nil {
			return
		}
		rp, err := filepath.EvalSymlinks(cf.Path)
		if err != nil {
			rp = utils.CanonicalizePath(cf.Path)
		}
		if _, exists := seenRealPaths[rp]; exists {
			return
		}
		seenRealPaths[rp] = struct{}{}
		out = append(out, *cf)
	}

	if agentDir != "" {
		addIfUnique(rlFindContextFileInDir(agentDir))
	}

	for _, dir := range ancestorDirs {
		if agentDir != "" && utils.PathsEqual(dir, agentDir) {
			continue
		}
		cf := rlFindContextFileInDir(dir)
		if cf == nil {
			continue
		}
		if skipMainRoot != "" && utils.PathsEqual(dir, skipMainRoot) && filepath.Base(cf.Path) == skipMainFilename {
			continue
		}
		addIfUnique(cf)
	}

	if out == nil {
		return []ContextFile{}
	}
	return out
}

// DefaultResourceLoaderOptions configures NewDefaultResourceLoader.
type DefaultResourceLoaderOptions struct {
	Cwd                           string
	AgentDir                      string
	SettingsManager               *SettingsManager
	EventBus                      EventBus
	NoExtensions                  bool
	NoSkills                      bool
	NoPromptTemplates             bool
	NoThemes                      bool
	NoContextFiles                bool
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	SystemPrompt                  *string
	AppendSystemPrompt            []string
	ExtensionFactories            []InlineExtension
	DisabledBuiltinExtensions     []string
	ExtensionsOverride            func(LoadExtensionsResult) LoadExtensionsResult
	SkillsOverride                func(LoadSkillsResult) LoadSkillsResult
	PromptsOverride               func(LoadPromptTemplatesResult) LoadPromptTemplatesResult
	ThemesOverride                func(LoadThemesResult) LoadThemesResult
	AgentsFilesOverride           func(LoadContextFilesResult) LoadContextFilesResult
	SystemPromptOverride          func(*string) *string
	AppendSystemPromptOverride    func([]string) []string
}

// DefaultResourceLoader discovers and loads extensions, packages, skills, prompt templates,
// themes, AGENTS/CLAUDE context files, and SYSTEM/APPEND_SYSTEM prompts.
type DefaultResourceLoader struct {
	mu                         sync.RWMutex
	reloadMu                   sync.Mutex
	cwd                        string
	agentDir                   string
	settingsManager            *SettingsManager
	packageManager             *DefaultPackageManager
	eventBus                   EventBus
	noExtensions               bool
	noSkills                   bool
	noPromptTemplates          bool
	noThemes                   bool
	noContextFiles             bool
	additionalExtensionPaths   []string
	additionalSkillPaths       []string
	additionalPromptPaths      []string
	additionalThemePaths       []string
	disabledBuiltinExtensions  []string
	extensionFactories         []InlineExtension
	systemPromptOpt            *string
	appendSystemPromptOpt      []string
	extensionsOverride         func(LoadExtensionsResult) LoadExtensionsResult
	skillsOverride             func(LoadSkillsResult) LoadSkillsResult
	promptsOverride            func(LoadPromptTemplatesResult) LoadPromptTemplatesResult
	themesOverride             func(LoadThemesResult) LoadThemesResult
	agentsFilesOverride        func(LoadContextFilesResult) LoadContextFilesResult
	systemPromptOverride       func(*string) *string
	appendSystemPromptOverride func([]string) []string

	loaded                    bool
	extensions                LoadExtensionsResult
	skills                    LoadSkillsResult
	prompts                   LoadPromptTemplatesResult
	themes                    LoadThemesResult
	agentsFiles               LoadContextFilesResult
	systemPrompt              *string
	systemPromptSource        *SystemPromptSource
	appendSystemPrompt        []string
	appendSystemPromptSources []SystemPromptSource
	pathMetadata              map[string]utils.PathMetadata
}

// NewDefaultResourceLoader constructs a DefaultResourceLoader with empty initial results.
// Call Reload before reading resources.
func NewDefaultResourceLoader(options DefaultResourceLoaderOptions) *DefaultResourceLoader {
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
	builtinNames := rlCollectBuiltinExtensionNames(options.ExtensionFactories)
	pm := NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:               cwd,
		AgentDir:          agentDir,
		SettingsManager:   sm,
		BuiltinExtensions: builtinNames,
	})
	return &DefaultResourceLoader{
		cwd:                        cwd,
		agentDir:                   agentDir,
		settingsManager:            sm,
		packageManager:             pm,
		eventBus:                   options.EventBus,
		noExtensions:               options.NoExtensions,
		noSkills:                   options.NoSkills,
		noPromptTemplates:          options.NoPromptTemplates,
		noThemes:                   options.NoThemes,
		noContextFiles:             options.NoContextFiles,
		additionalExtensionPaths:   slices.Clone(options.AdditionalExtensionPaths),
		additionalSkillPaths:       slices.Clone(options.AdditionalSkillPaths),
		additionalPromptPaths:      slices.Clone(options.AdditionalPromptTemplatePaths),
		additionalThemePaths:       slices.Clone(options.AdditionalThemePaths),
		disabledBuiltinExtensions:  slices.Clone(options.DisabledBuiltinExtensions),
		extensionFactories:         slices.Clone(options.ExtensionFactories),
		systemPromptOpt:            options.SystemPrompt,
		appendSystemPromptOpt:      slices.Clone(options.AppendSystemPrompt),
		extensionsOverride:         options.ExtensionsOverride,
		skillsOverride:             options.SkillsOverride,
		promptsOverride:            options.PromptsOverride,
		themesOverride:             options.ThemesOverride,
		agentsFilesOverride:        options.AgentsFilesOverride,
		systemPromptOverride:       options.SystemPromptOverride,
		appendSystemPromptOverride: options.AppendSystemPromptOverride,
		extensions: LoadExtensionsResult{
			Extensions: []Extension{},
			Errors:     []ExtensionLoadError{},
			Warnings:   []ExtensionLoadWarning{},
			Runtime:    CreateExtensionRuntime(),
		},
		skills: LoadSkillsResult{
			Skills:      []Skill{},
			Diagnostics: []ResourceDiagnostic{},
		},
		prompts: LoadPromptTemplatesResult{
			Templates:   []PromptTemplate{},
			Diagnostics: []ResourceDiagnostic{},
		},
		themes: LoadThemesResult{
			Themes:      []*theme.Theme{},
			Diagnostics: []ResourceDiagnostic{},
		},
		agentsFiles: LoadContextFilesResult{
			AgentsFiles: []ContextFile{},
		},
		appendSystemPrompt:        []string{},
		appendSystemPromptSources: []SystemPromptSource{},
		pathMetadata:              make(map[string]utils.PathMetadata),
	}
}

// GetPackageManager returns the loader's package manager instance.
func (d *DefaultResourceLoader) GetPackageManager() *DefaultPackageManager {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.packageManager
}

// Close releases extension runtime resources held by the loader (e.g., QuickJS VMs).
// Safe to call multiple times.
func (d *DefaultResourceLoader) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	rt := d.extensions.Runtime
	d.extensions.Runtime = nil
	d.mu.Unlock()
	if rt != nil {
		ShutdownExtensionRuntime(rt)
	}
}

func rlDecodeResourcePath(raw string, cwd string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(trimmed), "file://") {
		if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme == "file" {
			return filepath.Clean(parsed.Path)
		}
	}
	resolved, err := utils.ResolvePath(trimmed, cwd, &utils.PathInputOptions{Trim: true})
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return resolved
}

type rlExclusionRule struct {
	raw      string
	resolved []string
	pattern  string
}

func rlSplitConfiguredPaths(entries []string, cwd string, agentDir string) (includes []string, excludes []rlExclusionRule) {
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "!") {
			pat := strings.TrimSpace(trimmed[1:])
			if pat == "" {
				continue
			}
			var resolved []string
			if filepath.IsAbs(pat) || strings.HasPrefix(pat, "~") {
				resolved = append(resolved, rlDecodeResourcePath(pat, cwd))
			} else {
				if agentDir != "" {
					resolved = append(resolved, filepath.Clean(filepath.Join(agentDir, pat)))
				}
				if cwd != "" {
					resolved = append(resolved, filepath.Clean(filepath.Join(cwd, utils.ConfigDirName, pat)))
					resolved = append(resolved, filepath.Clean(filepath.Join(cwd, pat)))
				}
			}
			excludes = append(excludes, rlExclusionRule{raw: pat, resolved: resolved, pattern: filepath.ToSlash(pat)})
			continue
		}
		if strings.HasPrefix(trimmed, "+") {
			inc := strings.TrimSpace(trimmed[1:])
			if inc != "" {
				includes = append(includes, inc)
			}
			continue
		}
		includes = append(includes, trimmed)
	}
	return includes, excludes
}

func rlIsExcluded(filePath string, excludes []rlExclusionRule, baseRoots ...string) bool {
	if len(excludes) == 0 {
		return false
	}
	normPath := filepath.Clean(filePath)
	slashPath := filepath.ToSlash(normPath)
	mmOpts := minimatch.Options{Dot: true, MatchBase: true}
	for _, rule := range excludes {
		for _, candidate := range rule.resolved {
			if utils.PathsEqual(normPath, candidate) || utils.IsPathWithin(candidate, normPath) {
				return true
			}
		}
		for _, root := range baseRoots {
			if root == "" {
				continue
			}
			if rel, err := filepath.Rel(root, normPath); err == nil && !strings.HasPrefix(rel, "..") {
				relSlash := filepath.ToSlash(rel)
				if rule.pattern != "" && minimatch.Match(relSlash, rule.pattern, mmOpts) {
					return true
				}
				if strings.EqualFold(relSlash, filepath.ToSlash(rule.raw)) {
					return true
				}
			}
		}
		if rule.pattern != "" && minimatch.Match(slashPath, rule.pattern, mmOpts) {
			return true
		}
	}
	return false
}

func (d *DefaultResourceLoader) rlResolveTextOptionOrFile(value string) (text string, source *SystemPromptSource, ok bool) {
	if value == "" {
		return "", nil, false
	}
	resolved := rlDecodeResourcePath(value, d.cwd)
	info, err := os.Stat(resolved)
	if err == nil && !info.IsDir() {
		data, readErr := os.ReadFile(resolved)
		if readErr == nil {
			return string(data), &SystemPromptSource{Path: resolved}, true
		}
	}
	return value, nil, true
}

func (d *DefaultResourceLoader) rlDiscoverSystemPrompt(projectTrusted bool) (*string, *SystemPromptSource) {
	if d.systemPromptOpt != nil {
		text, src, ok := d.rlResolveTextOptionOrFile(*d.systemPromptOpt)
		if !ok {
			return nil, nil
		}
		return &text, src
	}
	var candidates []string
	if projectTrusted {
		candidates = append(candidates, filepath.Join(d.cwd, utils.ConfigDirName, "SYSTEM.md"))
	}
	if d.agentDir != "" {
		candidates = append(candidates, filepath.Join(d.agentDir, "SYSTEM.md"))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err == nil {
			s := string(data)
			return &s, &SystemPromptSource{Path: candidate}
		}
	}
	return nil, nil
}

func (d *DefaultResourceLoader) rlDiscoverAppendSystemPrompt(projectTrusted bool) ([]string, []SystemPromptSource) {
	var prompts []string
	var sources []SystemPromptSource

	if len(d.appendSystemPromptOpt) > 0 {
		for _, item := range d.appendSystemPromptOpt {
			text, src, ok := d.rlResolveTextOptionOrFile(item)
			if ok {
				prompts = append(prompts, text)
				if src != nil {
					sources = append(sources, *src)
				}
			}
		}
	}

	var candidates []string
	if projectTrusted {
		candidates = append(candidates, filepath.Join(d.cwd, utils.ConfigDirName, "APPEND_SYSTEM.md"))
	}
	if d.agentDir != "" {
		candidates = append(candidates, filepath.Join(d.agentDir, "APPEND_SYSTEM.md"))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err == nil {
			prompts = append(prompts, string(data))
			sources = append(sources, SystemPromptSource{Path: candidate})
		}
	}
	if prompts == nil {
		prompts = []string{}
	}
	if sources == nil {
		sources = []SystemPromptSource{}
	}
	return prompts, sources
}

func (d *DefaultResourceLoader) rlLoadThemes(
	colorMode tui.TerminalColorMode,
	includeDefaults bool,
	projectTrusted bool,
	extraPaths []string,
	excludes []rlExclusionRule,
	extraMeta map[string]utils.PathMetadata,
	outMeta map[string]utils.PathMetadata,
) LoadThemesResult {
	var themeFiles []struct {
		path string
		meta utils.PathMetadata
	}

	collectFromPath := func(rawPath string, meta utils.PathMetadata) {
		resolved := rlDecodeResourcePath(rawPath, d.cwd)
		if m, ok := extraMeta[resolved]; ok {
			meta = m
		} else if m, ok := extraMeta[rawPath]; ok {
			meta = m
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return
		}
		if info.IsDir() {
			entries, err := os.ReadDir(resolved)
			if err != nil {
				return
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, e := range entries {
				if !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
					continue
				}
				full := filepath.Join(resolved, e.Name())
				st, err := os.Stat(full)
				if err != nil || st.IsDir() {
					continue
				}
				m := meta
				if m.BaseDir == "" {
					m.BaseDir = resolved
				}
				themeFiles = append(themeFiles, struct {
					path string
					meta utils.PathMetadata
				}{path: full, meta: m})
			}
			return
		}
		if strings.HasSuffix(strings.ToLower(resolved), ".json") {
			m := meta
			if m.BaseDir == "" {
				m.BaseDir = filepath.Dir(resolved)
			}
			themeFiles = append(themeFiles, struct {
				path string
				meta utils.PathMetadata
			}{path: resolved, meta: m})
		}
	}

	for _, p := range extraPaths {
		collectFromPath(p, utils.PathMetadata{Source: "settings", Scope: utils.SourceScopeUser, Origin: utils.SourceOriginTopLevel})
	}
	if includeDefaults {
		if projectTrusted {
			collectFromPath(filepath.Join(d.cwd, utils.ConfigDirName, "themes"), utils.PathMetadata{Source: "local", Scope: utils.SourceScopeProject, Origin: utils.SourceOriginTopLevel})
		}
		if d.agentDir != "" {
			collectFromPath(filepath.Join(d.agentDir, "themes"), utils.PathMetadata{Source: "local", Scope: utils.SourceScopeUser, Origin: utils.SourceOriginTopLevel})
		}
	}

	var themesList []*theme.Theme
	var diagnostics []ResourceDiagnostic
	seenReal := make(map[string]struct{})
	byName := make(map[string]*theme.Theme)

	for _, item := range themeFiles {
		if rlIsExcluded(item.path, excludes, d.agentDir, filepath.Join(d.cwd, utils.ConfigDirName), d.cwd) {
			continue
		}
		rp, err := filepath.EvalSymlinks(item.path)
		if err != nil {
			rp = utils.CanonicalizePath(item.path)
		}
		if _, dup := seenReal[rp]; dup {
			continue
		}
		loaded, err := theme.LoadThemeFromPath(item.path, colorMode)
		if err != nil {
			p := item.path
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("Failed to load theme %s: %v", item.path, err),
				Path:    &p,
			})
			continue
		}
		srcInfo := utils.CreateSourceInfo(item.path, item.meta)
		loaded.SourceInfo = &srcInfo
		loaded.SourcePath = item.path
		if existing, exists := byName[loaded.Name]; exists {
			winSource := ""
			if existing.SourceInfo != nil {
				winSource = existing.SourceInfo.Source
			}
			loseSource := item.meta.Source
			losePath := item.path
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name collision: %q already loaded from %s", loaded.Name, existing.SourcePath),
				Path:    &losePath,
				Collision: &ResourceCollision{
					ResourceType: "theme",
					Name:         loaded.Name,
					WinnerPath:   existing.SourcePath,
					LoserPath:    item.path,
					WinnerSource: &winSource,
					LoserSource:  &loseSource,
				},
			})
			continue
		}
		seenReal[rp] = struct{}{}
		byName[loaded.Name] = loaded
		themesList = append(themesList, loaded)
		if outMeta != nil {
			outMeta[item.path] = item.meta
		}
	}

	if themesList == nil {
		themesList = []*theme.Theme{}
	}
	return LoadThemesResult{
		Themes:      themesList,
		Diagnostics: diagnostics,
	}
}

// CollectBuiltinExtensionNames returns unique built-in extension names registered
// globally or passed as built-in inline factories, preserving discovery order.
func CollectBuiltinExtensionNames(extraFactories []InlineExtension) []string {
	return rlCollectBuiltinExtensionNames(extraFactories)
}

func rlCollectBuiltinExtensionNames(extraFactories []InlineExtension) []string {
	seen := make(map[string]struct{})
	var names []string
	add := func(name string) {
		clean := strings.TrimPrefix(strings.TrimSpace(name), utils.BuiltinPathPrefix)
		if clean == "" {
			return
		}
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		names = append(names, clean)
	}
	for _, b := range GetDefaultBuiltinExtensions() {
		add(b.Name)
	}
	for _, f := range extraFactories {
		if f.Builtin || strings.HasPrefix(f.Name, utils.BuiltinPathPrefix) {
			add(f.Name)
		}
	}
	return names
}

func rlCollectPackageInstallWarnings(failures []PackageInstallFailure) []ExtensionLoadWarning {
	if len(failures) == 0 {
		return nil
	}
	warnings := make([]ExtensionLoadWarning, 0, len(failures))
	for _, f := range failures {
		msg := fmt.Sprintf("Package %q could not be resolved", f.Source)
		if f.Message != "" {
			msg = fmt.Sprintf("%s: %s", msg, f.Message)
		} else if f.Error != "" {
			msg = fmt.Sprintf("%s: %s", msg, f.Error)
		}
		warnings = append(warnings, ExtensionLoadWarning{
			Path:    f.Source,
			Warning: msg,
		})
	}
	return warnings
}

var rlHostProvidedPackages = map[string]struct{}{
	"@mariozechner/pi-coding-agent": {},
	"@mariozechner/pi-agent-core":   {},
	"@mariozechner/pi-ai":           {},
	"@mariozechner/pi-tui":          {},
	"@sinclair/typebox":             {},
}

func rlCollectExtensionPackageWarnings(extensions []Extension) []ExtensionLoadWarning {
	var warnings []ExtensionLoadWarning
	checkedPkgs := make(map[string]struct{})
	for _, ext := range extensions {
		p := ext.ResolvedPath
		if p == "" {
			p = ext.Path
		}
		if p == "" || strings.HasPrefix(p, utils.BuiltinPathPrefix) || strings.HasPrefix(p, "inline:") {
			continue
		}
		pkgPath := rlFindNearestPackageJSON(filepath.Dir(p))
		if pkgPath == "" {
			continue
		}
		if _, seen := checkedPkgs[pkgPath]; seen {
			continue
		}
		checkedPkgs[pkgPath] = struct{}{}

		raw, err := os.ReadFile(pkgPath)
		if err != nil {
			continue
		}
		var manifest struct {
			Dependencies     map[string]string `json:"dependencies"`
			PeerDependencies map[string]string `json:"peerDependencies"`
		}
		if err := jsonx.Unmarshal(raw, &manifest); err != nil || len(manifest.Dependencies) == 0 {
			continue
		}
		var bundled []string
		for dep := range manifest.Dependencies {
			if _, isHost := rlHostProvidedPackages[dep]; isHost {
				if _, isPeer := manifest.PeerDependencies[dep]; !isPeer {
					bundled = append(bundled, dep)
				}
			}
		}
		if len(bundled) > 0 {
			sort.Strings(bundled)
			warnings = append(warnings, ExtensionLoadWarning{
				Path: pkgPath,
				Warning: fmt.Sprintf(
					"Package lists host-provided packages in dependencies instead of peerDependencies: %s",
					strings.Join(bundled, ", "),
				),
			})
		}
	}
	return warnings
}

func rlFindNearestPackageJSON(startDir string) string {
	dir := startDir
	for dir != "" && dir != "." {
		base := filepath.Base(dir)
		if base == "node_modules" {
			return ""
		}
		cand := filepath.Join(dir, "package.json")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func rlCollectExtensionTargets(
	cwd string,
	noExtensions bool,
	resolvedExtensions []ResolvedResource,
	cliExtensions []ResolvedResource,
	additionalExtensionPaths []string,
	disabledBuiltinExtensions []string,
	pathMetadata map[string]utils.PathMetadata,
) ([]string, map[string]utils.PathMetadata) {
	disabledSet := make(map[string]struct{}, len(disabledBuiltinExtensions))
	for _, name := range disabledBuiltinExtensions {
		clean := strings.TrimPrefix(strings.TrimSpace(name), utils.BuiltinPathPrefix)
		if clean != "" {
			disabledSet[clean] = struct{}{}
		}
	}

	var paths []string
	extMeta := make(map[string]utils.PathMetadata)
	seen := make(map[string]struct{})

	addResource := func(res ResolvedResource) {
		if pathMetadata != nil {
			pathMetadata[res.Path] = res.Metadata
		}
		if !res.Enabled {
			return
		}
		if strings.HasPrefix(res.Path, utils.BuiltinPathPrefix) {
			name := strings.TrimPrefix(res.Path, utils.BuiltinPathPrefix)
			if _, disabled := disabledSet[name]; disabled {
				return
			}
		}
		if _, dup := seen[res.Path]; dup {
			return
		}
		seen[res.Path] = struct{}{}
		paths = append(paths, res.Path)
		extMeta[res.Path] = res.Metadata
	}

	if !noExtensions {
		for _, res := range resolvedExtensions {
			addResource(res)
		}
	}
	for _, res := range cliExtensions {
		addResource(res)
	}

	// Preserve missing local explicit -e paths so LoadExtensionsWithOptions surfaces load errors.
	for _, raw := range additionalExtensionPaths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "+") {
			continue
		}
		if pmParseSource(trimmed).kind != pmSourceLocal {
			continue
		}
		decoded := rlDecodeResourcePath(trimmed, cwd)
		if _, err := os.Stat(decoded); err == nil {
			continue
		}
		if _, dup := seen[decoded]; dup {
			continue
		}
		seen[decoded] = struct{}{}
		meta := utils.PathMetadata{Source: "path", Scope: utils.SourceScopeTemporary, Origin: utils.SourceOriginTopLevel}
		paths = append(paths, decoded)
		extMeta[decoded] = meta
		if pathMetadata != nil {
			pathMetadata[decoded] = meta
		}
	}

	return paths, extMeta
}

func rlCollectResourceInputs(
	cwd string,
	disabled bool,
	resolvedResources []ResolvedResource,
	cliResources []ResolvedResource,
	additionalPaths []string,
	pathMetadata map[string]utils.PathMetadata,
) ([]string, map[string]utils.PathMetadata) {
	var paths []string
	metaMap := make(map[string]utils.PathMetadata)
	seen := make(map[string]struct{})

	add := func(p string, meta utils.PathMetadata, enabled bool) {
		if pathMetadata != nil {
			pathMetadata[p] = meta
		}
		if !enabled {
			return
		}
		if _, exists := metaMap[p]; !exists {
			metaMap[p] = meta
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}

	if !disabled {
		for _, r := range resolvedResources {
			add(r.Path, r.Metadata, r.Enabled)
		}
		for _, r := range cliResources {
			add(r.Path, r.Metadata, r.Enabled)
		}
	}

	for _, raw := range additionalPaths {
		decoded := rlDecodeResourcePath(raw, cwd)
		if _, exists := metaMap[decoded]; exists {
			if _, dup := seen[decoded]; !dup {
				seen[decoded] = struct{}{}
				paths = append(paths, decoded)
			}
			continue
		}
		meta := utils.PathMetadata{
			Source: "path",
			Scope:  utils.SourceScopeTemporary,
			Origin: utils.SourceOriginTopLevel,
		}
		add(decoded, meta, true)
	}

	return paths, metaMap
}

// Reload discovers extensions, packages, skills, prompt templates, themes,
// context files, and system prompts according to current settings and overrides.
func (d *DefaultResourceLoader) Reload(ctx context.Context, options ...*ResourceLoaderReloadOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	d.mu.RLock()
	cwd := d.cwd
	agentDir := d.agentDir
	sm := d.settingsManager
	pm := d.packageManager
	eventBus := d.eventBus
	noExtensions := d.noExtensions
	noSkills := d.noSkills
	noPromptTemplates := d.noPromptTemplates
	noThemes := d.noThemes
	noContextFiles := d.noContextFiles
	additionalExtensionPaths := slices.Clone(d.additionalExtensionPaths)
	additionalSkillPaths := slices.Clone(d.additionalSkillPaths)
	additionalPromptPaths := slices.Clone(d.additionalPromptPaths)
	additionalThemePaths := slices.Clone(d.additionalThemePaths)
	disabledBuiltinExtensions := slices.Clone(d.disabledBuiltinExtensions)
	extensionFactories := slices.Clone(d.extensionFactories)
	extensionsOverride := d.extensionsOverride
	skillsOverride := d.skillsOverride
	promptsOverride := d.promptsOverride
	themesOverride := d.themesOverride
	agentsFilesOverride := d.agentsFilesOverride
	systemPromptOverride := d.systemPromptOverride
	appendSystemPromptOverride := d.appendSystemPromptOverride
	wasLoaded := d.loaded
	d.mu.RUnlock()

	if sm == nil {
		sm = SettingsManagerCreate(cwd, agentDir)
	}
	if pm == nil {
		pm = NewDefaultPackageManager(DefaultPackageManagerOptions{
			Cwd:               cwd,
			AgentDir:          agentDir,
			SettingsManager:   sm,
			BuiltinExtensions: rlCollectBuiltinExtensionNames(extensionFactories),
		})
	}

	if wasLoaded {
		ClearExtensionCache()
	}

	var cliPaths ResolvedPaths
	if len(additionalExtensionPaths) > 0 {
		resolvedCli, err := pm.ResolveExtensionSources(ctx, additionalExtensionPaths, &ResolveExtensionSourcesOptions{
			Temporary: true,
		})
		if err != nil {
			return err
		}
		cliPaths = resolvedCli
	}

	var resolveTrust func(context.Context, ResolveProjectTrustInput) (bool, error)
	if len(options) > 0 && options[0] != nil {
		resolveTrust = options[0].ResolveProjectTrust
	}

	var resolvedPaths ResolvedPaths
	var extRes LoadExtensionsResult
	haveResolvedAndLoaded := false
	pathMetadata := make(map[string]utils.PathMetadata)

	loadExtensionsFromTargets := func(resolved ResolvedPaths, metaOut map[string]utils.PathMetadata) LoadExtensionsResult {
		extTargets, extMeta := rlCollectExtensionTargets(
			cwd,
			noExtensions,
			resolved.Extensions,
			cliPaths.Extensions,
			additionalExtensionPaths,
			disabledBuiltinExtensions,
			metaOut,
		)
		return LoadExtensionsWithOptions(ctx, extTargets, &LoadExtensionsOptions{
			EventBus:                  eventBus,
			DisabledBuiltinExtensions: disabledBuiltinExtensions,
			Factories:                 extensionFactories,
			Cwd:                       cwd,
			AgentDir:                  agentDir,
			UseCache:                  true,
			PathMetadata:              extMeta,
		})
	}

	if resolveTrust != nil {
		initialTrusted := sm.IsProjectTrusted()
		sm.SetProjectTrusted(false)

		untrustedResolved, err := pm.Resolve(ctx)
		if err != nil {
			sm.SetProjectTrusted(initialTrusted)
			return err
		}
		bootstrapExts := loadExtensionsFromTargets(untrustedResolved, pathMetadata)

		trusted, err := resolveTrust(ctx, ResolveProjectTrustInput{
			ExtensionsResult: bootstrapExts,
		})
		if err != nil {
			ShutdownExtensionRuntime(bootstrapExts.Runtime)
			sm.SetProjectTrusted(initialTrusted)
			return err
		}
		sm.SetProjectTrusted(trusted)

		if !trusted {
			resolvedPaths = untrustedResolved
			extRes = bootstrapExts
			haveResolvedAndLoaded = true
		} else {
			ShutdownExtensionRuntime(bootstrapExts.Runtime)
			pathMetadata = make(map[string]utils.PathMetadata)
		}
	}

	if !haveResolvedAndLoaded {
		resolved, err := pm.Resolve(ctx)
		if err != nil {
			return err
		}
		resolvedPaths = resolved
		extRes = loadExtensionsFromTargets(resolvedPaths, pathMetadata)
	}

	projectTrusted := sm.IsProjectTrusted()

	extRes.Warnings = append(extRes.Warnings, rlCollectPackageInstallWarnings(resolvedPaths.InstallFailures)...)
	extRes.Warnings = append(extRes.Warnings, rlCollectPackageInstallWarnings(cliPaths.InstallFailures)...)
	extRes.Warnings = append(extRes.Warnings, rlCollectExtensionPackageWarnings(extRes.Extensions)...)
	if extensionsOverride != nil {
		extRes = extensionsOverride(extRes)
	}

	capOverrides := sm.GetTerminalCapabilityOverrides()
	tui.SetCapabilityOverrides(&capOverrides)
	caps := tui.GetCapabilities()
	colorMode := tui.GetTerminalColorMode(&caps)

	// Skills.
	_, skillExcludes := rlSplitConfiguredPaths(sm.GetSkillPaths(), cwd, agentDir)
	skillPaths, skillMeta := rlCollectResourceInputs(
		cwd,
		noSkills,
		resolvedPaths.Skills,
		cliPaths.Skills,
		additionalSkillPaths,
		pathMetadata,
	)
	incFalse := false
	skillsRes := LoadSkills(&LoadSkillsOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      skillPaths,
		IncludeDefaults: &incFalse,
		ProjectTrusted:  &projectTrusted,
		PathMetadata:    skillMeta,
	})
	if len(skillExcludes) > 0 {
		var kept []Skill
		for _, s := range skillsRes.Skills {
			if !rlIsExcluded(s.FilePath, skillExcludes, agentDir, filepath.Join(cwd, utils.ConfigDirName), cwd) {
				kept = append(kept, s)
			}
		}
		if kept == nil {
			kept = []Skill{}
		}
		skillsRes.Skills = kept
	}
	if skillsOverride != nil {
		skillsRes = skillsOverride(skillsRes)
	}

	// Prompt templates.
	_, promptExcludes := rlSplitConfiguredPaths(sm.GetPromptTemplatePaths(), cwd, agentDir)
	promptPaths, promptMeta := rlCollectResourceInputs(
		cwd,
		noPromptTemplates,
		resolvedPaths.Prompts,
		cliPaths.Prompts,
		additionalPromptPaths,
		pathMetadata,
	)
	promptsRes := LoadPromptTemplates(&LoadPromptTemplatesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		PromptPaths:     promptPaths,
		IncludeDefaults: &incFalse,
		PathMetadata:    promptMeta,
	})
	if len(promptExcludes) > 0 {
		var kept []PromptTemplate
		for _, p := range promptsRes.Templates {
			if !rlIsExcluded(p.FilePath, promptExcludes, agentDir, filepath.Join(cwd, utils.ConfigDirName), cwd) {
				kept = append(kept, p)
			}
		}
		if kept == nil {
			kept = []PromptTemplate{}
		}
		promptsRes.Templates = kept
	}
	if promptsOverride != nil {
		promptsRes = promptsOverride(promptsRes)
	}

	// Themes.
	_, themeExcludes := rlSplitConfiguredPaths(sm.GetThemePaths(), cwd, agentDir)
	themePaths, themeMeta := rlCollectResourceInputs(
		cwd,
		noThemes,
		resolvedPaths.Themes,
		cliPaths.Themes,
		additionalThemePaths,
		pathMetadata,
	)
	themesRes := d.rlLoadThemes(colorMode, false, projectTrusted, themePaths, themeExcludes, themeMeta, pathMetadata)
	if themesOverride != nil {
		themesRes = themesOverride(themesRes)
	}

	// Context files (AGENTS.md / CLAUDE.md).
	var contextRes LoadContextFilesResult
	if noContextFiles {
		contextRes = LoadContextFilesResult{AgentsFiles: []ContextFile{}}
	} else {
		contextRes = LoadContextFilesResult{
			AgentsFiles: LoadProjectContextFiles(&LoadProjectContextFilesOptions{
				Cwd:      cwd,
				AgentDir: agentDir,
			}),
		}
	}
	if agentsFilesOverride != nil {
		contextRes = agentsFilesOverride(contextRes)
	}

	// SYSTEM.md / APPEND_SYSTEM.md.
	sysPrompt, sysSrc := d.rlDiscoverSystemPrompt(projectTrusted)
	if systemPromptOverride != nil {
		sysPrompt = systemPromptOverride(sysPrompt)
	}

	appPrompts, appSources := d.rlDiscoverAppendSystemPrompt(projectTrusted)
	if appendSystemPromptOverride != nil {
		appPrompts = appendSystemPromptOverride(appPrompts)
	}

	d.mu.Lock()
	prevRuntime := d.extensions.Runtime
	d.settingsManager = sm
	d.packageManager = pm
	d.extensions = extRes
	d.skills = skillsRes
	d.prompts = promptsRes
	d.themes = themesRes
	d.agentsFiles = contextRes
	d.systemPrompt = sysPrompt
	d.systemPromptSource = sysSrc
	d.appendSystemPrompt = appPrompts
	d.appendSystemPromptSources = appSources
	d.pathMetadata = pathMetadata
	d.loaded = true
	d.mu.Unlock()

	if prevRuntime != nil && prevRuntime != extRes.Runtime {
		ShutdownExtensionRuntime(prevRuntime)
	}

	return nil
}

// ExtendResources loads additional skill, prompt, and theme paths with explicit metadata.
func (d *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	d.mu.RLock()
	cwd := d.cwd
	agentDir := d.agentDir
	currentSkills := slices.Clone(d.skills.Skills)
	currentPrompts := slices.Clone(d.prompts.Templates)
	currentThemes := slices.Clone(d.themes.Themes)
	d.mu.RUnlock()

	newMeta := make(map[string]utils.PathMetadata)
	var extraSkills []Skill
	var extraSkillDiags []ResourceDiagnostic
	if len(paths.SkillPaths) > 0 {
		skillPaths := make([]string, 0, len(paths.SkillPaths))
		skillMeta := make(map[string]utils.PathMetadata, len(paths.SkillPaths))
		for _, entry := range paths.SkillPaths {
			decoded := rlDecodeResourcePath(entry.Path, cwd)
			skillPaths = append(skillPaths, decoded)
			skillMeta[decoded] = entry.Metadata
			newMeta[decoded] = entry.Metadata
		}
		incFalse := false
		extra := LoadSkills(&LoadSkillsOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SkillPaths:      skillPaths,
			IncludeDefaults: &incFalse,
			PathMetadata:    skillMeta,
		})
		seen := make(map[string]Skill, len(currentSkills))
		for _, s := range currentSkills {
			seen[s.Name] = s
		}
		for _, s := range extra.Skills {
			if existing, exists := seen[s.Name]; exists {
				ws, ls, lp := existing.SourceInfo.Source, s.SourceInfo.Source, s.FilePath
				extraSkillDiags = append(extraSkillDiags, ResourceDiagnostic{
					Type:    "collision",
					Message: fmt.Sprintf("name collision: %q already loaded from %s", s.Name, existing.FilePath),
					Path:    &lp,
					Collision: &ResourceCollision{
						ResourceType: "skill",
						Name:         s.Name,
						WinnerPath:   existing.FilePath,
						LoserPath:    s.FilePath,
						WinnerSource: &ws,
						LoserSource:  &ls,
					},
				})
				continue
			}
			seen[s.Name] = s
			extraSkills = append(extraSkills, s)
		}
		extraSkillDiags = append(extraSkillDiags, extra.Diagnostics...)
	}

	var extraPrompts []PromptTemplate
	var extraPromptDiags []ResourceDiagnostic
	if len(paths.PromptPaths) > 0 {
		promptPaths := make([]string, 0, len(paths.PromptPaths))
		promptMeta := make(map[string]utils.PathMetadata, len(paths.PromptPaths))
		for _, entry := range paths.PromptPaths {
			decoded := rlDecodeResourcePath(entry.Path, cwd)
			promptPaths = append(promptPaths, decoded)
			promptMeta[decoded] = entry.Metadata
			newMeta[decoded] = entry.Metadata
		}
		incFalse := false
		extra := LoadPromptTemplates(&LoadPromptTemplatesOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			PromptPaths:     promptPaths,
			IncludeDefaults: &incFalse,
			PathMetadata:    promptMeta,
		})
		seen := make(map[string]PromptTemplate, len(currentPrompts))
		for _, p := range currentPrompts {
			seen[p.Name] = p
		}
		for _, p := range extra.Templates {
			if existing, exists := seen[p.Name]; exists {
				ws, ls, lp := existing.SourceInfo.Source, p.SourceInfo.Source, p.FilePath
				extraPromptDiags = append(extraPromptDiags, ResourceDiagnostic{
					Type:    "collision",
					Message: fmt.Sprintf("name collision: %q already loaded from %s", p.Name, existing.FilePath),
					Path:    &lp,
					Collision: &ResourceCollision{
						ResourceType: "prompt",
						Name:         p.Name,
						WinnerPath:   existing.FilePath,
						LoserPath:    p.FilePath,
						WinnerSource: &ws,
						LoserSource:  &ls,
					},
				})
				continue
			}
			seen[p.Name] = p
			extraPrompts = append(extraPrompts, p)
		}
		extraPromptDiags = append(extraPromptDiags, extra.Diagnostics...)
	}

	var extraThemes []*theme.Theme
	var extraThemeDiags []ResourceDiagnostic
	if len(paths.ThemePaths) > 0 {
		caps := tui.GetCapabilities()
		colorMode := tui.GetTerminalColorMode(&caps)
		themePaths := make([]string, 0, len(paths.ThemePaths))
		themeMeta := make(map[string]utils.PathMetadata, len(paths.ThemePaths))
		for _, entry := range paths.ThemePaths {
			decoded := rlDecodeResourcePath(entry.Path, cwd)
			themePaths = append(themePaths, decoded)
			themeMeta[decoded] = entry.Metadata
			newMeta[decoded] = entry.Metadata
		}
		// Load extra themes while checking collisions against already-loaded themes.
		extra := d.rlLoadThemes(colorMode, false, true, themePaths, nil, themeMeta, newMeta)
		existingByName := make(map[string]*theme.Theme, len(currentThemes))
		for _, t := range currentThemes {
			existingByName[t.Name] = t
		}
		for _, t := range extra.Themes {
			if existing, exists := existingByName[t.Name]; exists {
				ws := ""
				if existing.SourceInfo != nil {
					ws = existing.SourceInfo.Source
				}
				ls := ""
				if t.SourceInfo != nil {
					ls = t.SourceInfo.Source
				}
				lp := t.SourcePath
				extraThemeDiags = append(extraThemeDiags, ResourceDiagnostic{
					Type:    "collision",
					Message: fmt.Sprintf("name collision: %q already loaded from %s", t.Name, existing.SourcePath),
					Path:    &lp,
					Collision: &ResourceCollision{
						ResourceType: "theme",
						Name:         t.Name,
						WinnerPath:   existing.SourcePath,
						LoserPath:    t.SourcePath,
						WinnerSource: &ws,
						LoserSource:  &ls,
					},
				})
				continue
			}
			existingByName[t.Name] = t
			extraThemes = append(extraThemes, t)
		}
		extraThemeDiags = append(extraThemeDiags, extra.Diagnostics...)
	}

	d.mu.Lock()
	if d.pathMetadata == nil {
		d.pathMetadata = make(map[string]utils.PathMetadata, len(newMeta))
	}
	for k, v := range newMeta {
		d.pathMetadata[k] = v
	}
	d.skills.Skills = append(d.skills.Skills, extraSkills...)
	d.skills.Diagnostics = append(d.skills.Diagnostics, extraSkillDiags...)
	d.prompts.Templates = append(d.prompts.Templates, extraPrompts...)
	d.prompts.Diagnostics = append(d.prompts.Diagnostics, extraPromptDiags...)
	d.themes.Themes = append(d.themes.Themes, extraThemes...)
	d.themes.Diagnostics = append(d.themes.Diagnostics, extraThemeDiags...)
	d.mu.Unlock()
}

func (d *DefaultResourceLoader) GetExtensions() LoadExtensionsResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.extensions
}

func (d *DefaultResourceLoader) GetSkills() LoadSkillsResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.skills
}

func (d *DefaultResourceLoader) GetPrompts() LoadPromptTemplatesResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.prompts
}

func (d *DefaultResourceLoader) GetThemes() LoadThemesResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.themes
}

func (d *DefaultResourceLoader) GetAgentsFiles() LoadContextFilesResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agentsFiles
}

func (d *DefaultResourceLoader) GetSystemPrompt() *string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.systemPrompt
}

func (d *DefaultResourceLoader) GetSystemPromptSource() *SystemPromptSource {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.systemPromptSource
}

func (d *DefaultResourceLoader) GetAppendSystemPrompt() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]string(nil), d.appendSystemPrompt...)
}

func (d *DefaultResourceLoader) GetAppendSystemPromptSources() []SystemPromptSource {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]SystemPromptSource(nil), d.appendSystemPromptSources...)
}

func (d *DefaultResourceLoader) GetPathMetadata() map[string]utils.PathMetadata {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string]utils.PathMetadata, len(d.pathMetadata))
	for k, v := range d.pathMetadata {
		out[k] = v
	}
	return out
}
