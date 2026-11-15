// Ported from packages/coding-agent/src/core/skills.ts (pi v1.5.2).

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/ignore"
)

const (
	skillsMaxNameLength        = 64
	skillsMaxDescriptionLength = 1024
)

var (
	skillsIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}
	skillsValidNameRegex  = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// SkillFrontmatter represents parsed YAML frontmatter for a SKILL.md or skill markdown file.
type SkillFrontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

// Skill represents a loaded skill and its source provenance.
type Skill struct {
	Name                   string           `json:"name"`
	Description            string           `json:"description"`
	FilePath               string           `json:"filePath"`
	BaseDir                string           `json:"baseDir"`
	SourceInfo             utils.SourceInfo `json:"sourceInfo"`
	DisableModelInvocation bool             `json:"disableModelInvocation"`
}

// LoadSkillsResult holds discovered skills and any warnings/collisions encountered.
type LoadSkillsResult struct {
	Skills      []Skill              `json:"skills"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

// LoadSkillsFromDirOptions configures LoadSkillsFromDir.
type LoadSkillsFromDirOptions struct {
	Dir              string
	Source           string
	Scope            utils.SourceScope
	Origin           utils.SourceOrigin
	IncludeRootFiles *bool
}

// LoadSkillsOptions configures LoadSkills across default and explicit paths.
type LoadSkillsOptions struct {
	Cwd             string
	AgentDir        string
	SkillPaths      []string
	IncludeDefaults *bool
	ProjectTrusted  *bool
	PathMetadata    map[string]utils.PathMetadata
}

// FormatSkillsForPromptOptions configures skill formatting in the system prompt.
type FormatSkillsForPromptOptions struct {
	Method string
}

type skillsIgnoreMatcher struct {
	ig      *ignore.Ignore
	hasRule bool
}

func skillsNewIgnoreMatcher() *skillsIgnoreMatcher {
	return &skillsIgnoreMatcher{ig: ignore.New()}
}

func skillsToIgnoreRelPath(rootDir string, targetPath string, isDir bool) (string, bool) {
	rel, err := filepath.Rel(rootDir, targetPath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if isDir && !strings.HasSuffix(rel, "/") {
		rel += "/"
	}
	return rel, true
}

func skillsLoadIgnoreRules(dir string, rootDir string, matcher *skillsIgnoreMatcher) {
	relPrefix := ""
	if dir != rootDir {
		rel, ok := skillsToIgnoreRelPath(rootDir, dir, true)
		if ok {
			relPrefix = rel
		}
	}
	for _, filename := range skillsIgnoreFileNames {
		p := filepath.Join(dir, filename)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		var patterns []string
		for _, raw := range lines {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			patterns = append(patterns, skillsPrefixIgnorePattern(trimmed, relPrefix))
		}
		if len(patterns) > 0 {
			matcher.ig.Add(patterns...)
			matcher.hasRule = true
		}
	}
}

func skillsPrefixIgnorePattern(line string, relPrefix string) string {
	if relPrefix == "" {
		return line
	}
	negated := ""
	pattern := line
	if strings.HasPrefix(pattern, "!") {
		negated = "!"
		pattern = pattern[1:]
	}
	if strings.HasPrefix(pattern, "/") {
		return negated + relPrefix + pattern[1:]
	}
	return negated + relPrefix + pattern
}

func skillsValidateName(name string, _ string) []string {
	var warnings []string
	if name != "" {
		if len(name) > skillsMaxNameLength {
			warnings = append(warnings, fmt.Sprintf("name exceeds %d characters (%d)", skillsMaxNameLength, len(name)))
		}
		if !skillsValidNameRegex.MatchString(name) {
			warnings = append(warnings, "name contains invalid characters (must be lowercase a-z, 0-9, or hyphens)")
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			warnings = append(warnings, "name must not start or end with a hyphen")
		}
		if strings.Contains(name, "--") {
			warnings = append(warnings, "name must not contain consecutive hyphens")
		}
	}
	return warnings
}

func skillsValidateDescription(description string) []string {
	var warnings []string
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		warnings = append(warnings, "description is required")
		return warnings
	}
	if len(description) > skillsMaxDescriptionLength {
		warnings = append(warnings, fmt.Sprintf("description exceeds %d characters (%d)", skillsMaxDescriptionLength, len(description)))
	}
	return warnings
}

var skillsYamlLineRe = regexp.MustCompile(`\byaml:\s*line\s+(\d+)`)

func skillsFormatYamlErr(err error) string {
	msg := err.Error()
	if !strings.Contains(msg, "at line") {
		if m := skillsYamlLineRe.FindStringSubmatch(msg); len(m) == 2 {
			return fmt.Sprintf("Failed to parse YAML frontmatter at line %s: %s", m[1], msg)
		}
	}
	return fmt.Sprintf("Failed to parse YAML frontmatter: %s", msg)
}

func skillsLoadFromFile(filePath string, sourceInfo utils.SourceInfo) (*Skill, []ResourceDiagnostic) {
	var diagnostics []ResourceDiagnostic
	data, err := os.ReadFile(filePath)
	if err != nil {
		msg := err.Error()
		p := filePath
		diagnostics = append(diagnostics, ResourceDiagnostic{
			Type:    "warning",
			Message: msg,
			Path:    &p,
		})
		return nil, diagnostics
	}

	parsed, err := utils.ParseFrontmatter[SkillFrontmatter](string(data))
	if err != nil {
		p := filePath
		diagnostics = append(diagnostics, ResourceDiagnostic{
			Type:    "warning",
			Message: skillsFormatYamlErr(err),
			Path:    &p,
		})
		return nil, diagnostics
	}

	fm := parsed.Frontmatter
	parentDir := filepath.Dir(filePath)
	parentName := filepath.Base(parentDir)
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = parentName
	}

	for _, w := range skillsValidateDescription(fm.Description) {
		p := filePath
		diagnostics = append(diagnostics, ResourceDiagnostic{
			Type:    "warning",
			Message: w,
			Path:    &p,
		})
	}
	for _, w := range skillsValidateName(name, parentName) {
		p := filePath
		diagnostics = append(diagnostics, ResourceDiagnostic{
			Type:    "warning",
			Message: w,
			Path:    &p,
		})
	}

	if strings.TrimSpace(fm.Description) == "" {
		return nil, diagnostics
	}

	skill := &Skill{
		Name:                   name,
		Description:            fm.Description,
		FilePath:               filePath,
		BaseDir:                parentDir,
		SourceInfo:             sourceInfo,
		DisableModelInvocation: fm.DisableModelInvocation,
	}
	return skill, diagnostics
}

func skillsBuildSourceInfo(filePath string, source string, scope utils.SourceScope, origin utils.SourceOrigin, baseDir string) utils.SourceInfo {
	if origin != "" || scope != "" {
		if scope == "" {
			scope = utils.SourceScopeTemporary
		}
		if origin == "" {
			origin = utils.SourceOriginTopLevel
		}
		return utils.CreateSourceInfo(filePath, utils.PathMetadata{
			Source:  source,
			Scope:   scope,
			Origin:  origin,
			BaseDir: baseDir,
		})
	}
	return utils.CreateSyntheticSourceInfo(filePath, utils.SyntheticSourceOptions{
		Source:  source,
		BaseDir: baseDir,
	})
}

func skillsLoadFromDirInternal(
	dir string,
	rootDir string,
	source string,
	scope utils.SourceScope,
	origin utils.SourceOrigin,
	includeRootFiles bool,
	matcher *skillsIgnoreMatcher,
	visitedDirs map[string]struct{},
) LoadSkillsResult {
	result := LoadSkillsResult{
		Skills:      []Skill{},
		Diagnostics: []ResourceDiagnostic{},
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return result
	}

	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = filepath.Clean(dir)
	}
	if _, seen := visitedDirs[realDir]; seen {
		return result
	}
	visitedDirs[realDir] = struct{}{}

	skillsLoadIgnoreRules(dir, rootDir, matcher)

	// If a subdirectory (or root dir) contains SKILL.md directly, treat the directory
	// as a single skill root and skip descending further or loading extra sibling .md files.
	skillMdPath := filepath.Join(dir, "SKILL.md")
	if st, err := os.Stat(skillMdPath); err == nil && !st.IsDir() {
		if rel, ok := skillsToIgnoreRelPath(rootDir, skillMdPath, false); ok && matcher.hasRule && matcher.ig.Ignores(rel) {
			return result
		}
		srcInfo := skillsBuildSourceInfo(skillMdPath, source, scope, origin, dir)
		skill, diags := skillsLoadFromFile(skillMdPath, srcInfo)
		result.Diagnostics = append(result.Diagnostics, diags...)
		if skill != nil {
			result.Skills = append(result.Skills, *skill)
		}
		return result
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		entryName := entry.Name()
		if strings.HasPrefix(entryName, ".") || entryName == "node_modules" {
			continue
		}
		fullPath := filepath.Join(dir, entryName)

		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		isDir := stat.IsDir()

		if rel, ok := skillsToIgnoreRelPath(rootDir, fullPath, isDir); ok && matcher.hasRule && matcher.ig.Ignores(rel) {
			continue
		}

		if isDir {
			sub := skillsLoadFromDirInternal(fullPath, rootDir, source, scope, origin, false, matcher, visitedDirs)
			result.Skills = append(result.Skills, sub.Skills...)
			result.Diagnostics = append(result.Diagnostics, sub.Diagnostics...)
			continue
		}

		if includeRootFiles && strings.HasSuffix(strings.ToLower(entryName), ".md") {
			srcInfo := skillsBuildSourceInfo(fullPath, source, scope, origin, filepath.Dir(fullPath))
			skill, diags := skillsLoadFromFile(fullPath, srcInfo)
			result.Diagnostics = append(result.Diagnostics, diags...)
			if skill != nil {
				result.Skills = append(result.Skills, *skill)
			}
		}
	}

	return result
}

// LoadSkillsFromDir discovers skills from a directory tree.
func LoadSkillsFromDir(options LoadSkillsFromDirOptions) LoadSkillsResult {
	includeRootFiles := true
	if options.IncludeRootFiles != nil {
		includeRootFiles = *options.IncludeRootFiles
	}
	return skillsLoadFromDirInternal(
		options.Dir,
		options.Dir,
		options.Source,
		options.Scope,
		options.Origin,
		includeRootFiles,
		skillsNewIgnoreMatcher(),
		make(map[string]struct{}),
	)
}

func skillsNormalizeInputPath(input string, cwd string) string {
	resolved, err := utils.ResolvePath(input, cwd, &utils.PathInputOptions{Trim: true})
	if err != nil {
		return filepath.Clean(input)
	}
	return resolved
}

func skillsFindGitRepoRoot(startDir string) string {
	dir := filepath.Clean(startDir)
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func skillsCollectProjectAgentsSkillDirs(cwd string) []string {
	repoRoot := skillsFindGitRepoRoot(cwd)
	var dirs []string
	cur := filepath.Clean(cwd)
	for {
		candidate := filepath.Join(cur, ".agents", "skills")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dirs = append(dirs, candidate)
		}
		if repoRoot != "" && utils.PathsEqual(cur, repoRoot) {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return dirs
}

func skillsEscapeXml(str string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(str)
}

// FormatSkillsForPrompt renders enabled skills as an XML <available_skills> block for the system prompt.
func FormatSkillsForPrompt(skills []Skill, options ...*FormatSkillsForPromptOptions) string {
	var visible []Skill
	for _, s := range skills {
		if !s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	method := "read"
	if len(options) > 0 && options[0] != nil && options[0].Method != "" {
		method = options[0].Method
	}

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString("The following skills provide specialized instructions for specific tasks.\n")
	switch method {
	case "bash":
		b.WriteString("Use bash to load a skill's file when the task matches its description.\n")
	case "indirect":
		b.WriteString("Load a skill's file when the task matches its description.\n")
	default:
		b.WriteString("Use the read tool to load a skill's file when the task matches its description.\n")
	}
	b.WriteString("Resolve relative paths against the skill's directory (parent of SKILL.md). Use absolute paths in tool calls.\n\n")
	b.WriteString("<available_skills>\n")
	for _, s := range visible {
		b.WriteString("  <skill>\n")
		b.WriteString("    <name>" + skillsEscapeXml(s.Name) + "</name>\n")
		b.WriteString("    <description>" + skillsEscapeXml(s.Description) + "</description>\n")
		b.WriteString("    <location>" + skillsEscapeXml(s.FilePath) + "</location>\n")
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// LoadSkills loads skills from configured explicit paths and optionally default project/user directories.
func LoadSkills(options ...*LoadSkillsOptions) LoadSkillsResult {
	var opts LoadSkillsOptions
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
	includeDefaults := true
	if opts.IncludeDefaults != nil {
		includeDefaults = *opts.IncludeDefaults
	}
	projectTrusted := true
	if opts.ProjectTrusted != nil {
		projectTrusted = *opts.ProjectTrusted
	}

	skillMap := make(map[string]Skill)
	var skillOrder []string
	var diagnostics []ResourceDiagnostic
	seenRealPaths := make(map[string]struct{})

	addSkill := func(skill Skill) {
		realPath, err := filepath.EvalSymlinks(skill.FilePath)
		if err != nil {
			realPath = utils.CanonicalizePath(skill.FilePath)
		}
		if _, dup := seenRealPaths[realPath]; dup {
			return
		}
		if existing, exists := skillMap[skill.Name]; exists {
			winSource := existing.SourceInfo.Source
			loseSource := skill.SourceInfo.Source
			losePath := skill.FilePath
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name collision: %q already loaded from %s", skill.Name, existing.FilePath),
				Path:    &losePath,
				Collision: &ResourceCollision{
					ResourceType: "skill",
					Name:         skill.Name,
					WinnerPath:   existing.FilePath,
					LoserPath:    skill.FilePath,
					WinnerSource: &winSource,
					LoserSource:  &loseSource,
				},
			})
			return
		}
		seenRealPaths[realPath] = struct{}{}
		skillMap[skill.Name] = skill
		skillOrder = append(skillOrder, skill.Name)
	}

	loadPath := func(resolvedPath string, source string, scope utils.SourceScope, origin utils.SourceOrigin, explicit bool) {
		info, err := os.Stat(resolvedPath)
		if err != nil {
			if explicit {
				p := resolvedPath
				diagnostics = append(diagnostics, ResourceDiagnostic{
					Type:    "warning",
					Message: fmt.Sprintf("skill path does not exist: %s", resolvedPath),
					Path:    &p,
				})
			}
			return
		}
		if info.IsDir() {
			loaded := LoadSkillsFromDir(LoadSkillsFromDirOptions{
				Dir:    resolvedPath,
				Source: source,
				Scope:  scope,
				Origin: origin,
			})
			diagnostics = append(diagnostics, loaded.Diagnostics...)
			for _, s := range loaded.Skills {
				addSkill(s)
			}
			return
		}
		if strings.HasSuffix(strings.ToLower(resolvedPath), ".md") {
			srcInfo := skillsBuildSourceInfo(resolvedPath, source, scope, origin, filepath.Dir(resolvedPath))
			skill, diags := skillsLoadFromFile(resolvedPath, srcInfo)
			diagnostics = append(diagnostics, diags...)
			if skill != nil {
				addSkill(*skill)
			}
		} else if explicit {
			p := resolvedPath
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("skill path is not a markdown file or directory: %s", resolvedPath),
				Path:    &p,
			})
		}
	}

	// Explicit skill paths first (preserving metadata if provided).
	for _, rawPath := range opts.SkillPaths {
		resolved := skillsNormalizeInputPath(rawPath, cwd)
		meta, hasMeta := opts.PathMetadata[resolved]
		if !hasMeta {
			meta, hasMeta = opts.PathMetadata[rawPath]
		}
		if hasMeta {
			loadPath(resolved, meta.Source, meta.Scope, meta.Origin, true)
		} else {
			loadPath(resolved, "path", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, true)
		}
	}

	if includeDefaults {
		if projectTrusted {
			loadPath(filepath.Join(cwd, utils.ConfigDirName, "skills"), "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, false)
			for _, agentsDir := range skillsCollectProjectAgentsSkillDirs(cwd) {
				loadPath(agentsDir, "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, false)
			}
		}
		loadPath(filepath.Join(agentDir, "skills"), "local", utils.SourceScopeUser, utils.SourceOriginTopLevel, false)
		if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
			loadPath(filepath.Join(homeDir, ".agents", "skills"), "local", utils.SourceScopeUser, utils.SourceOriginTopLevel, false)
		}
	}

	skills := make([]Skill, 0, len(skillOrder))
	for _, name := range skillOrder {
		skills = append(skills, skillMap[name])
	}
	return LoadSkillsResult{
		Skills:      skills,
		Diagnostics: diagnostics,
	}
}
