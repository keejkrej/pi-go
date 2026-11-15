// Ported from packages/coding-agent/src/core/prompt-templates.ts (pi v1.5.2).

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

// PromptTemplate represents a markdown prompt template usable as a slash command.
type PromptTemplate struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	ArgumentHint *string          `json:"argumentHint,omitzero"`
	Content      string           `json:"content"`
	SourceInfo   utils.SourceInfo `json:"sourceInfo"`
	FilePath     string           `json:"filePath"`
}

// LoadPromptTemplatesOptions configures prompt template discovery.
type LoadPromptTemplatesOptions struct {
	Cwd             string
	AgentDir        string
	PromptPaths     []string
	IncludeDefaults *bool
	PathMetadata    map[string]utils.PathMetadata
}

// LoadPromptTemplatesResult holds loaded prompt templates and non-fatal diagnostics.
type LoadPromptTemplatesResult struct {
	Templates   []PromptTemplate     `json:"templates"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

type pteFrontmatter struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

// ParseCommandArgs splits a command argument string into positional arguments using
// shell-like single and double quoting rules:
//   - Quotes only start quoting at the beginning of an argument (apostrophes inside words stay literal)
//   - A backslash inside a quoted argument escapes only the matching quote character
//   - An unclosed quote is treated as literal text
//   - Empty quotes ("", ”) produce an empty string argument
func ParseCommandArgs(argsString string) []string {
	var args []string
	var cur strings.Builder
	inToken := false

	i := 0
	n := len(argsString)
	for i < n {
		r := argsString[i]
		if !inToken && (r == '"' || r == '\'') {
			quote := r
			quoteStart := i
			i++
			var quoted strings.Builder
			closed := false
			for i < n {
				c := argsString[i]
				if c == '\\' && i+1 < n && argsString[i+1] == quote {
					quoted.WriteByte(quote)
					i += 2
					continue
				}
				if c == quote {
					i++
					closed = true
					break
				}
				quoted.WriteByte(c)
				i++
			}
			if closed {
				cur.WriteString(quoted.String())
				inToken = true
				continue
			}
			// Unclosed quote: rewind to opening quote and treat it as literal text.
			i = quoteStart
			cur.WriteByte(quote)
			inToken = true
			i++
			continue
		}

		if unicode.IsSpace(rune(r)) {
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
			i++
			continue
		}

		// Handle multi-byte UTF-8 runes cleanly outside quote checks.
		if r >= 0x80 {
			for _, ru := range argsString[i:] {
				if unicode.IsSpace(ru) {
					if inToken {
						args = append(args, cur.String())
						cur.Reset()
						inToken = false
					}
					i += len(string(ru))
					break
				}
				cur.WriteRune(ru)
				inToken = true
				i += len(string(ru))
				break
			}
			continue
		}

		cur.WriteByte(r)
		inToken = true
		i++
	}

	if inToken {
		args = append(args, cur.String())
	}
	if args == nil {
		return []string{}
	}
	return args
}

var ptePlaceholderRe = regexp.MustCompile(`\$\{(?:@|ARGUMENTS):-([^}]*)\}|\$\{(\d+):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$ARGUMENTS|\$@|\$(\d+)`)

// SubstituteArgs expands $1, $2, $@, $ARGUMENTS, ${N:-default}, ${@:-default}, ${ARGUMENTS:-default},
// ${@:N}, and ${@:N:L} placeholders in a single non-recursive pass.
func SubstituteArgs(content string, args []string) string {
	joined := strings.Join(args, " ")
	return ptePlaceholderRe.ReplaceAllStringFunc(content, func(match string) string {
		groups := ptePlaceholderRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		// Branch 1: ${@:-default} or ${ARGUMENTS:-default}
		if strings.HasPrefix(match, "${@:-") || strings.HasPrefix(match, "${ARGUMENTS:-") {
			if len(args) == 0 {
				return groups[1]
			}
			return joined
		}
		// Branch 2: ${N:-default}
		if groups[2] != "" {
			idx, err := strconv.Atoi(groups[2])
			if err == nil && idx >= 1 && idx <= len(args) && args[idx-1] != "" {
				return args[idx-1]
			}
			return groups[3]
		}
		// Branch 3: ${@:N} or ${@:N:L}
		if groups[4] != "" {
			start, err := strconv.Atoi(groups[4])
			if err != nil {
				return ""
			}
			if start < 1 {
				start = 1
			}
			startIdx := start - 1
			if startIdx >= len(args) {
				return ""
			}
			if groups[5] != "" {
				length, err := strconv.Atoi(groups[5])
				if err != nil || length <= 0 {
					return ""
				}
				endIdx := startIdx + length
				if endIdx > len(args) {
					endIdx = len(args)
				}
				return strings.Join(args[startIdx:endIdx], " ")
			}
			return strings.Join(args[startIdx:], " ")
		}
		// Branch 4: $ARGUMENTS or $@
		if match == "$ARGUMENTS" || match == "$@" {
			return joined
		}
		// Branch 5: $N
		if groups[6] != "" {
			idx, err := strconv.Atoi(groups[6])
			if err == nil && idx >= 1 && idx <= len(args) {
				return args[idx-1]
			}
			return ""
		}
		return match
	})
}

// ExpandPromptTemplate expands a `/name args` invocation if a matching PromptTemplate exists.
func ExpandPromptTemplate(text string, templates []PromptTemplate) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}
	spaceIdx := strings.IndexFunc(text, unicode.IsSpace)
	var cmdName, argsString string
	if spaceIdx == -1 {
		cmdName = text[1:]
	} else {
		cmdName = text[1:spaceIdx]
		argsString = text[spaceIdx+1:]
	}
	for _, tmpl := range templates {
		if tmpl.Name == cmdName {
			return SubstituteArgs(tmpl.Content, ParseCommandArgs(argsString))
		}
	}
	return text
}

func pteFirstNonEmptyLine(content string) string {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 60 {
				return trimmed[:60] + "..."
			}
			return trimmed
		}
	}
	return ""
}

func pteBuildSourceLabel(sourceInfo utils.SourceInfo) string {
	switch sourceInfo.Origin {
	case utils.SourceOriginPackage:
		return "(" + sourceInfo.Source + ")"
	default:
		switch sourceInfo.Scope {
		case utils.SourceScopeUser:
			return "(user)"
		case utils.SourceScopeProject:
			return "(project)"
		case utils.SourceScopeTemporary:
			return "(path)"
		default:
			return ""
		}
	}
}

func pteFormatDescription(description string, content string, sourceInfo utils.SourceInfo) string {
	desc := strings.TrimSpace(description)
	if desc == "" {
		desc = pteFirstNonEmptyLine(content)
	}
	if desc == "" {
		label := pteBuildSourceLabel(sourceInfo)
		return label
	}
	_ = pteBuildSourceLabel(sourceInfo)
	return desc
}

func pteLoadFromFile(filePath string, sourceInfo utils.SourceInfo) (*PromptTemplate, []ResourceDiagnostic) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		p := filePath
		return nil, []ResourceDiagnostic{{
			Type:    "warning",
			Message: fmt.Sprintf("Failed to read prompt template %s: %v", filePath, err),
			Path:    &p,
		}}
	}

	parsed, err := utils.ParseFrontmatter[pteFrontmatter](string(data))
	if err != nil {
		p := filePath
		return nil, []ResourceDiagnostic{{
			Type:    "warning",
			Message: fmt.Sprintf("Failed to parse prompt template frontmatter in %s: %v", filePath, err),
			Path:    &p,
		}}
	}

	name := strings.TrimSuffix(filepath.Base(filePath), ".md")
	var argHint *string
	if h := strings.TrimSpace(parsed.Frontmatter.ArgumentHint); h != "" {
		argHint = &h
	}
	desc := strings.TrimSpace(parsed.Frontmatter.Description)
	if desc == "" {
		desc = pteFirstNonEmptyLine(parsed.Body)
	}

	return &PromptTemplate{
		Name:         name,
		Description:  desc,
		ArgumentHint: argHint,
		Content:      parsed.Body,
		SourceInfo:   sourceInfo,
		FilePath:     filePath,
	}, nil
}

func pteSourceInfo(filePath string, source string, scope utils.SourceScope, origin utils.SourceOrigin, baseDir string) utils.SourceInfo {
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

func pteLoadFromDir(dir string, source string, scope utils.SourceScope, origin utils.SourceOrigin) ([]PromptTemplate, []ResourceDiagnostic) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var templates []PromptTemplate
	var diagnostics []ResourceDiagnostic
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		stat, err := os.Stat(fullPath)
		if err != nil || stat.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		tmpl, diags := pteLoadFromFile(fullPath, pteSourceInfo(fullPath, source, scope, origin, dir))
		diagnostics = append(diagnostics, diags...)
		if tmpl != nil {
			templates = append(templates, *tmpl)
		}
	}
	return templates, diagnostics
}

// LoadPromptTemplates discovers prompt templates from explicit paths and default user/project dirs.
func LoadPromptTemplates(options ...*LoadPromptTemplatesOptions) LoadPromptTemplatesResult {
	var opts LoadPromptTemplatesOptions
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

	var templates []PromptTemplate
	var diagnostics []ResourceDiagnostic
	seenRealPaths := make(map[string]struct{})
	seenNames := make(map[string]PromptTemplate)

	addTemplate := func(tmpl PromptTemplate) {
		realPath, err := filepath.EvalSymlinks(tmpl.FilePath)
		if err != nil {
			realPath = utils.CanonicalizePath(tmpl.FilePath)
		}
		if _, dup := seenRealPaths[realPath]; dup {
			return
		}
		if existing, exists := seenNames[tmpl.Name]; exists {
			winSource := existing.SourceInfo.Source
			loseSource := tmpl.SourceInfo.Source
			losePath := tmpl.FilePath
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name collision: %q already loaded from %s", tmpl.Name, existing.FilePath),
				Path:    &losePath,
				Collision: &ResourceCollision{
					ResourceType: "prompt",
					Name:         tmpl.Name,
					WinnerPath:   existing.FilePath,
					LoserPath:    tmpl.FilePath,
					WinnerSource: &winSource,
					LoserSource:  &loseSource,
				},
			})
			return
		}
		seenRealPaths[realPath] = struct{}{}
		seenNames[tmpl.Name] = tmpl
		templates = append(templates, tmpl)
	}

	loadPath := func(rawPath string, source string, scope utils.SourceScope, origin utils.SourceOrigin, warnMissing bool) {
		resolved, err := utils.ResolvePath(rawPath, cwd, &utils.PathInputOptions{Trim: true})
		if err != nil {
			resolved = filepath.Clean(rawPath)
		}
		if meta, ok := opts.PathMetadata[resolved]; ok {
			source, scope, origin = meta.Source, meta.Scope, meta.Origin
		} else if meta, ok := opts.PathMetadata[rawPath]; ok {
			source, scope, origin = meta.Source, meta.Scope, meta.Origin
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if warnMissing {
				p := resolved
				diagnostics = append(diagnostics, ResourceDiagnostic{
					Type:    "warning",
					Message: fmt.Sprintf("prompt template path does not exist: %s", resolved),
					Path:    &p,
				})
			}
			return
		}
		if info.IsDir() {
			loaded, diags := pteLoadFromDir(resolved, source, scope, origin)
			diagnostics = append(diagnostics, diags...)
			for _, t := range loaded {
				addTemplate(t)
			}
			return
		}
		if strings.HasSuffix(strings.ToLower(resolved), ".md") {
			tmpl, diags := pteLoadFromFile(resolved, pteSourceInfo(resolved, source, scope, origin, filepath.Dir(resolved)))
			diagnostics = append(diagnostics, diags...)
			if tmpl != nil {
				addTemplate(*tmpl)
			}
		}
	}

	for _, p := range opts.PromptPaths {
		loadPath(p, "path", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, true)
	}

	if includeDefaults {
		loadPath(filepath.Join(cwd, utils.ConfigDirName, "prompts"), "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, false)
		loadPath(filepath.Join(agentDir, "prompts"), "local", utils.SourceScopeUser, utils.SourceOriginTopLevel, false)
	}

	if templates == nil {
		templates = []PromptTemplate{}
	}
	return LoadPromptTemplatesResult{
		Templates:   templates,
		Diagnostics: diagnostics,
	}
}
