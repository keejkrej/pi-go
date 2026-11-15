// Ported from packages/coding-agent/src/core/resource-loader.ts (pi v1.5.2).
// Barebone build: discovers local/user skills, prompt templates, themes,
// AGENTS.md/CLAUDE.md context files, and SYSTEM.md/APPEND_SYSTEM.md without
// loading external npm/git packages or JS/TS extensions.

package core

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
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
	DisabledBuiltinExtensions     []string
	SkillsOverride                func(LoadSkillsResult) LoadSkillsResult
	PromptsOverride               func(LoadPromptTemplatesResult) LoadPromptTemplatesResult
	ThemesOverride                func(LoadThemesResult) LoadThemesResult
	AgentsFilesOverride           func(LoadContextFilesResult) LoadContextFilesResult
	SystemPromptOverride          func(*string) *string
	AppendSystemPromptOverride    func([]string) []string
}

// DefaultResourceLoader is the barebone ResourceLoader implementation.
type DefaultResourceLoader struct {
	mu                         sync.RWMutex
	cwd                        string
	agentDir                   string
	settingsManager            *SettingsManager
	eventBus                   EventBus
	noExtensions               bool
	noSkills                   bool
	noPromptTemplates          bool
	noThemes                   bool
	noContextFiles             bool
	additionalSkillPaths       []string
	additionalPromptPaths      []string
	additionalThemePaths       []string
	systemPromptOpt            *string
	appendSystemPromptOpt      []string
	skillsOverride             func(LoadSkillsResult) LoadSkillsResult
	promptsOverride            func(LoadPromptTemplatesResult) LoadPromptTemplatesResult
	themesOverride             func(LoadThemesResult) LoadThemesResult
	agentsFilesOverride        func(LoadContextFilesResult) LoadContextFilesResult
	systemPromptOverride       func(*string) *string
	appendSystemPromptOverride func([]string) []string

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
	return &DefaultResourceLoader{
		cwd:                        cwd,
		agentDir:                   agentDir,
		settingsManager:            options.SettingsManager,
		eventBus:                   options.EventBus,
		noExtensions:               options.NoExtensions,
		noSkills:                   options.NoSkills,
		noPromptTemplates:          options.NoPromptTemplates,
		noThemes:                   options.NoThemes,
		noContextFiles:             options.NoContextFiles,
		additionalSkillPaths:       append([]string(nil), options.AdditionalSkillPaths...),
		additionalPromptPaths:      append([]string(nil), options.AdditionalPromptTemplatePaths...),
		additionalThemePaths:       append([]string(nil), options.AdditionalThemePaths...),
		systemPromptOpt:            options.SystemPrompt,
		appendSystemPromptOpt:      append([]string(nil), options.AppendSystemPrompt...),
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
				m.BaseDir = resolved
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

	for _, p := range d.additionalThemePaths {
		collectFromPath(p, utils.PathMetadata{Source: "path", Scope: utils.SourceScopeTemporary, Origin: utils.SourceOriginTopLevel})
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
		d.pathMetadata[item.path] = item.meta
	}

	if themesList == nil {
		themesList = []*theme.Theme{}
	}
	return LoadThemesResult{
		Themes:      themesList,
		Diagnostics: diagnostics,
	}
}

// Reload discovers all barebone local/user resources according to current settings and overrides.
func (d *DefaultResourceLoader) Reload(ctx context.Context, options ...*ResourceLoaderReloadOptions) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	sm := d.settingsManager
	if sm == nil {
		sm = SettingsManagerCreate(d.cwd, d.agentDir)
		d.settingsManager = sm
	}

	projectTrusted := sm.IsProjectTrusted()
	if len(options) > 0 && options[0] != nil && options[0].ResolveProjectTrust != nil {
		trusted, err := options[0].ResolveProjectTrust(ctx, ResolveProjectTrustInput{
			ExtensionsResult: d.extensions,
		})
		if err != nil {
			return err
		}
		projectTrusted = trusted
	}

	capOverrides := sm.GetTerminalCapabilityOverrides()
	tui.SetCapabilityOverrides(&capOverrides)
	caps := tui.GetCapabilities()
	colorMode := tui.GetTerminalColorMode(&caps)

	d.pathMetadata = make(map[string]utils.PathMetadata)

	// Extensions are always empty in the barebone build.
	d.extensions = LoadExtensionsResult{
		Extensions: []Extension{},
		Errors:     []ExtensionLoadError{},
		Warnings:   []ExtensionLoadWarning{},
		Runtime:    CreateExtensionRuntime(),
	}

	// Skills.
	var skillExcludes []rlExclusionRule
	var skillSettingsIncludes []string
	if len(sm.GetSkillPaths()) > 0 {
		skillSettingsIncludes, skillExcludes = rlSplitConfiguredPaths(sm.GetSkillPaths(), d.cwd, d.agentDir)
	}
	var allSkillPaths []string
	allSkillPaths = append(allSkillPaths, d.additionalSkillPaths...)
	allSkillPaths = append(allSkillPaths, skillSettingsIncludes...)
	decodedSkillPaths := make([]string, len(allSkillPaths))
	for i, p := range allSkillPaths {
		decodedSkillPaths[i] = rlDecodeResourcePath(p, d.cwd)
	}
	incSkills := !d.noSkills
	skillsRes := LoadSkills(&LoadSkillsOptions{
		Cwd:             d.cwd,
		AgentDir:        d.agentDir,
		SkillPaths:      decodedSkillPaths,
		IncludeDefaults: &incSkills,
		ProjectTrusted:  &projectTrusted,
		PathMetadata:    d.pathMetadata,
	})
	if len(skillExcludes) > 0 {
		var kept []Skill
		for _, s := range skillsRes.Skills {
			if !rlIsExcluded(s.FilePath, skillExcludes, d.agentDir, filepath.Join(d.cwd, utils.ConfigDirName), d.cwd) {
				kept = append(kept, s)
			}
		}
		if kept == nil {
			kept = []Skill{}
		}
		skillsRes.Skills = kept
	}
	if d.skillsOverride != nil {
		skillsRes = d.skillsOverride(skillsRes)
	}
	d.skills = skillsRes

	// Prompt templates.
	promptIncludes, promptExcludes := rlSplitConfiguredPaths(sm.GetPromptTemplatePaths(), d.cwd, d.agentDir)
	var allPromptPaths []string
	allPromptPaths = append(allPromptPaths, d.additionalPromptPaths...)
	allPromptPaths = append(allPromptPaths, promptIncludes...)
	decodedPromptPaths := make([]string, len(allPromptPaths))
	for i, p := range allPromptPaths {
		decodedPromptPaths[i] = rlDecodeResourcePath(p, d.cwd)
	}
	incPrompts := !d.noPromptTemplates
	// Respect project trust for default project prompt templates.
	promptsMeta := make(map[string]utils.PathMetadata)
	var promptsRes LoadPromptTemplatesResult
	if incPrompts && !projectTrusted {
		incFalse := false
		userPromptPaths := append([]string(nil), decodedPromptPaths...)
		if d.agentDir != "" {
			userPromptsDir := filepath.Join(d.agentDir, "prompts")
			userPromptPaths = append(userPromptPaths, userPromptsDir)
			promptsMeta[userPromptsDir] = utils.PathMetadata{Source: "local", Scope: utils.SourceScopeUser, Origin: utils.SourceOriginTopLevel}
		}
		promptsRes = LoadPromptTemplates(&LoadPromptTemplatesOptions{
			Cwd:             d.cwd,
			AgentDir:        d.agentDir,
			PromptPaths:     userPromptPaths,
			IncludeDefaults: &incFalse,
			PathMetadata:    promptsMeta,
		})
	} else {
		promptsRes = LoadPromptTemplates(&LoadPromptTemplatesOptions{
			Cwd:             d.cwd,
			AgentDir:        d.agentDir,
			PromptPaths:     decodedPromptPaths,
			IncludeDefaults: &incPrompts,
			PathMetadata:    d.pathMetadata,
		})
	}
	if len(promptExcludes) > 0 {
		var kept []PromptTemplate
		for _, p := range promptsRes.Templates {
			if !rlIsExcluded(p.FilePath, promptExcludes, d.agentDir, filepath.Join(d.cwd, utils.ConfigDirName), d.cwd) {
				kept = append(kept, p)
			}
		}
		if kept == nil {
			kept = []PromptTemplate{}
		}
		promptsRes.Templates = kept
	}
	if d.promptsOverride != nil {
		promptsRes = d.promptsOverride(promptsRes)
	}
	d.prompts = promptsRes

	// Themes.
	themeIncludes, themeExcludes := rlSplitConfiguredPaths(sm.GetThemePaths(), d.cwd, d.agentDir)
	themesRes := d.rlLoadThemes(colorMode, !d.noThemes, projectTrusted, themeIncludes, themeExcludes, d.pathMetadata)
	if d.themesOverride != nil {
		themesRes = d.themesOverride(themesRes)
	}
	d.themes = themesRes

	// Context files (AGENTS.md / CLAUDE.md).
	var contextRes LoadContextFilesResult
	if d.noContextFiles {
		contextRes = LoadContextFilesResult{AgentsFiles: []ContextFile{}}
	} else {
		contextRes = LoadContextFilesResult{
			AgentsFiles: LoadProjectContextFiles(&LoadProjectContextFilesOptions{
				Cwd:      d.cwd,
				AgentDir: d.agentDir,
			}),
		}
	}
	if d.agentsFilesOverride != nil {
		contextRes = d.agentsFilesOverride(contextRes)
	}
	d.agentsFiles = contextRes

	// SYSTEM.md / APPEND_SYSTEM.md.
	sysPrompt, sysSrc := d.rlDiscoverSystemPrompt(projectTrusted)
	if d.systemPromptOverride != nil {
		sysPrompt = d.systemPromptOverride(sysPrompt)
	}
	d.systemPrompt = sysPrompt
	d.systemPromptSource = sysSrc

	appPrompts, appSources := d.rlDiscoverAppendSystemPrompt(projectTrusted)
	if d.appendSystemPromptOverride != nil {
		appPrompts = d.appendSystemPromptOverride(appPrompts)
	}
	d.appendSystemPrompt = appPrompts
	d.appendSystemPromptSources = appSources

	return nil
}

// ExtendResources loads additional skill, prompt, and theme paths with explicit metadata.
func (d *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(paths.SkillPaths) > 0 {
		skillPaths := make([]string, 0, len(paths.SkillPaths))
		skillMeta := make(map[string]utils.PathMetadata, len(paths.SkillPaths))
		for _, entry := range paths.SkillPaths {
			decoded := rlDecodeResourcePath(entry.Path, d.cwd)
			skillPaths = append(skillPaths, decoded)
			skillMeta[decoded] = entry.Metadata
			d.pathMetadata[decoded] = entry.Metadata
		}
		incFalse := false
		extra := LoadSkills(&LoadSkillsOptions{
			Cwd:             d.cwd,
			AgentDir:        d.agentDir,
			SkillPaths:      skillPaths,
			IncludeDefaults: &incFalse,
			PathMetadata:    skillMeta,
		})
		seen := make(map[string]Skill, len(d.skills.Skills))
		for _, s := range d.skills.Skills {
			seen[s.Name] = s
		}
		for _, s := range extra.Skills {
			if existing, exists := seen[s.Name]; exists {
				ws, ls, lp := existing.SourceInfo.Source, s.SourceInfo.Source, s.FilePath
				d.skills.Diagnostics = append(d.skills.Diagnostics, ResourceDiagnostic{
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
			d.skills.Skills = append(d.skills.Skills, s)
		}
		d.skills.Diagnostics = append(d.skills.Diagnostics, extra.Diagnostics...)
	}

	if len(paths.PromptPaths) > 0 {
		promptPaths := make([]string, 0, len(paths.PromptPaths))
		promptMeta := make(map[string]utils.PathMetadata, len(paths.PromptPaths))
		for _, entry := range paths.PromptPaths {
			decoded := rlDecodeResourcePath(entry.Path, d.cwd)
			promptPaths = append(promptPaths, decoded)
			promptMeta[decoded] = entry.Metadata
			d.pathMetadata[decoded] = entry.Metadata
		}
		incFalse := false
		extra := LoadPromptTemplates(&LoadPromptTemplatesOptions{
			Cwd:             d.cwd,
			AgentDir:        d.agentDir,
			PromptPaths:     promptPaths,
			IncludeDefaults: &incFalse,
			PathMetadata:    promptMeta,
		})
		seen := make(map[string]PromptTemplate, len(d.prompts.Templates))
		for _, p := range d.prompts.Templates {
			seen[p.Name] = p
		}
		for _, p := range extra.Templates {
			if existing, exists := seen[p.Name]; exists {
				ws, ls, lp := existing.SourceInfo.Source, p.SourceInfo.Source, p.FilePath
				d.prompts.Diagnostics = append(d.prompts.Diagnostics, ResourceDiagnostic{
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
			d.prompts.Templates = append(d.prompts.Templates, p)
		}
		d.prompts.Diagnostics = append(d.prompts.Diagnostics, extra.Diagnostics...)
	}

	if len(paths.ThemePaths) > 0 {
		caps := tui.GetCapabilities()
		colorMode := tui.GetTerminalColorMode(&caps)
		themePaths := make([]string, 0, len(paths.ThemePaths))
		themeMeta := make(map[string]utils.PathMetadata, len(paths.ThemePaths))
		for _, entry := range paths.ThemePaths {
			decoded := rlDecodeResourcePath(entry.Path, d.cwd)
			themePaths = append(themePaths, decoded)
			themeMeta[decoded] = entry.Metadata
			d.pathMetadata[decoded] = entry.Metadata
		}
		// Load extra themes while checking collisions against already-loaded themes.
		extra := d.rlLoadThemes(colorMode, false, true, themePaths, nil, themeMeta)
		existingByName := make(map[string]*theme.Theme, len(d.themes.Themes))
		for _, t := range d.themes.Themes {
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
				d.themes.Diagnostics = append(d.themes.Diagnostics, ResourceDiagnostic{
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
			d.themes.Themes = append(d.themes.Themes, t)
		}
		d.themes.Diagnostics = append(d.themes.Diagnostics, extra.Diagnostics...)
	}
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
